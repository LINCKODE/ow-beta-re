// Package control is the instance server's local HTTP API for inspection and
// experiments (loopback only: it can send anything to a client):
//
//	GET  /conns                players: lifecycle state, entities, clock (frame P,
//	                           input frame, lead, flags, resyncs), replication and
//	                           JAM counters, plus the link's view (addr, handshake)
//	GET  /matches[?full=1]     matches (alias /worlds): map, frame, players, world
//	                           and ECS counts; full = every entity
//	GET  /stats                the game loop (ticks, late ticks, queued events)
//	GET  /debug/draw[?conn=N]  the match as world-space debug shapes for Prometheus' Debug Draw window
//	                           (docs/DebugDrawing.md §3; conn 0 or absent = the first match)
//	POST /clock                {"Target": 10, "Band": 1, ...}: the clock steering policy
//	                           (clock.Policy; zero fields = defaults) for every player
//	GET  /traffic?n=50         recent datagrams, both directions (hex)
//	GET  /jamlog?conn=N&ping=0 JAM messages received (ping=0 leaves out 21608)
//	POST /setup?conn=N         join now (controller + world), as on 21602
//	POST /lockin               {"conn": N, "hero": 0x02E...}: as on 21603
//	POST /jam                  {"conn": N, "msg": 20301, "payload": "hex", "unreliable": false}
//	POST /spawn                {"conn": N, "id": 200, "def": 14, "pos": [..], "rot": [..],
//	                           "scale": [..], "flags": 56, "map_index": n, "mover": {"pos", "yaw"}}:
//	                           creates 0x40000000|id on that client only (not in the world)
//	POST /filter               {"conn": N, "id": n, "bits": 262144}: component 0x10 update
//	POST /game/pos             {"body": n, "pos": [x, y, z], "yaw": rad}: where the server believes a hero is
//	                           (position source for the rules host, docs/InstanceServer.md §12.9)
//	POST /game/send            {"conn": N, "target": id (0 = all), "msg": "0x024...", "params": {"0x0a4...":
//	                           {"type": 2, "bits": 0}}}: a raw rules-layer game message
//	POST /scripts              {"conn": N, "entity": n, "graphs": [{"index", "sync_vars",
//	                           "remote_nodes"}], "active": {"1": [{"index", "data", "off"}]}}:
//	                           remote Statescript full update (layouts: Prometheus graph_sync_info)
//
// Numbers in JSON bodies may be given as hex strings ("0x02E0000000000029").
package control

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"owserver/inst/clock"
	"owserver/inst/jam"
	"owserver/inst/link"
	"owserver/inst/match"
	"owserver/inst/replication"
	"owserver/inst/server"
	"owserver/inst/sim"

	"owstatescript/statescript"
)

// API is the control API over a running server.
type API struct {
	Log    *slog.Logger
	Server *server.Server
}

// ListenAndServe serves on addr (loopback only) until ctx ends.
func (a *API) ListenAndServe(ctx context.Context, addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("control API address %s is not a loopback address", addr)
	}
	srv := &http.Server{Addr: addr, Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second}
	context.AfterFunc(ctx, func() { srv.Close() })
	a.Log.Info("control API", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Conn is a player with the link's view of its connection.
type Conn struct {
	match.PlayerInfo
	Link *link.ConnInfo `json:"link,omitempty"`
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /conns", func(w http.ResponseWriter, r *http.Request) {
		var ps []match.PlayerInfo
		if !a.do(w, r, func(m *match.Manager) error { ps = m.Players(); return nil }) {
			return
		}
		links := map[uint32]link.ConnInfo{}
		for _, c := range a.Server.Link.Conns() {
			links[c.ID] = c
		}
		out := []Conn{}
		for _, p := range ps {
			c := Conn{PlayerInfo: p}
			if l, ok := links[p.ConnID]; ok {
				c.Link = &l
			}
			out = append(out, c)
		}
		writeJSON(w, out)
	})
	matches := func(w http.ResponseWriter, r *http.Request) {
		var out []match.MatchInfo
		full := r.URL.Query().Get("full") == "1"
		if a.do(w, r, func(m *match.Manager) error { out = m.Matches(full); return nil }) {
			writeJSON(w, out)
		}
	}
	mux.HandleFunc("GET /matches", matches)
	mux.HandleFunc("GET /worlds", matches)
	mux.HandleFunc("POST /clock", func(w http.ResponseWriter, r *http.Request) {
		var p clock.Policy
		if !decode(w, r, &p) {
			return
		}
		var out clock.Policy
		if a.do(w, r, func(m *match.Manager) error { out = m.SetPolicy(p); return nil }) {
			writeJSON(w, out)
		}
	})
	// The server-side Statescript of a hero body: POST {"body", "on"} switches its
	// trace, GET drains it (JSON lines, Prometheus ss_trace's vocabulary).
	simTrace := func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Body Num   `json:"body"`
			On   *bool `json:"on"`
		}
		if r.Method == http.MethodPost {
			if !decode(w, r, &req) {
				return
			}
		} else {
			b, _ := strconv.ParseUint(r.URL.Query().Get("body"), 0, 32)
			req.Body = Num(b)
		}
		var out string
		if a.do(w, r, func(m *match.Manager) (err error) { out, err = m.SimTrace(uint32(req.Body), req.On); return }) {
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.Write([]byte(out))
		}
	}
	mux.HandleFunc("GET /simdeltas", func(w http.ResponseWriter, r *http.Request) {
		b, _ := strconv.ParseUint(r.URL.Query().Get("body"), 0, 32)
		var out []sim.DeltaRecord
		if a.do(w, r, func(m *match.Manager) (err error) { out, err = m.SimDeltas(uint32(b)); return }) {
			writeJSON(w, out)
		}
	})
	// Shadow mode (--shadow, T052): the server's snapshot of a hero body at a command frame, and a re-seed.
	mux.HandleFunc("GET /shadow", func(w http.ResponseWriter, r *http.Request) {
		b, _ := strconv.ParseUint(r.URL.Query().Get("body"), 0, 32)
		cf, _ := strconv.ParseUint(r.URL.Query().Get("cf"), 0, 32)
		var out *sim.ShadowSnapshot
		if a.do(w, r, func(m *match.Manager) (err error) { out, err = m.ShadowState(uint32(b), uint32(cf)); return }) {
			writeJSON(w, out)
		}
	})
	mux.HandleFunc("POST /shadow/reset", func(w http.ResponseWriter, r *http.Request) {
		b, _ := strconv.ParseUint(r.URL.Query().Get("body"), 0, 32)
		var f uint32
		if a.do(w, r, func(m *match.Manager) (err error) { f, err = m.ShadowReset(uint32(b)); return }) {
			writeJSON(w, map[string]any{"frame": f})
		}
	})
	mux.HandleFunc("GET /simtrace", simTrace)
	mux.HandleFunc("POST /simtrace", simTrace)
	mux.HandleFunc("GET /debug/draw", func(w http.ResponseWriter, r *http.Request) {
		var conn uint64
		if c := r.URL.Query().Get("conn"); c != "" {
			var err error
			if conn, err = strconv.ParseUint(c, 0, 32); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		var out *match.DebugDraw
		if a.do(w, r, func(m *match.Manager) (err error) { out, err = m.DebugShapes(uint32(conn)); return }) {
			writeJSON(w, out)
		}
	})
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.Server.Stats()) })
	mux.HandleFunc("GET /traffic", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		writeJSON(w, a.Server.Link.Traffic(n))
	})
	mux.HandleFunc("GET /jamlog", func(w http.ResponseWriter, r *http.Request) {
		conn, _ := strconv.Atoi(r.URL.Query().Get("conn"))
		var out []match.JamLogEntry
		if a.do(w, r, func(m *match.Manager) error {
			out = m.JamLog(uint32(conn), r.URL.Query().Get("ping") == "0")
			return nil
		}) {
			writeJSON(w, out)
		}
	})
	mux.HandleFunc("POST /setup", func(w http.ResponseWriter, r *http.Request) {
		conn, _ := strconv.Atoi(r.URL.Query().Get("conn"))
		if a.do(w, r, func(m *match.Manager) error { return m.Join(uint32(conn)) }) {
			writeJSON(w, map[string]any{"setup": true})
		}
	})
	mux.HandleFunc("POST /lockin", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Conn uint32 `json:"conn"`
			Hero Num    `json:"hero"`
		}
		if decode(w, r, &req) && a.do(w, r, func(m *match.Manager) error { return m.LockIn(req.Conn, uint64(req.Hero)) }) {
			writeJSON(w, map[string]any{"locked_in": true})
		}
	})
	mux.HandleFunc("POST /game/pos", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Body uint32     `json:"body"`
			Pos  [3]float32 `json:"pos"`
			Yaw  *float32   `json:"yaw"` // radians
		}
		if decode(w, r, &req) && a.do(w, r, func(m *match.Manager) error { return m.SetPosition(req.Body, req.Pos, req.Yaw) }) {
			writeJSON(w, map[string]any{"ok": true})
		}
	})
	mux.HandleFunc("POST /game/send", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Conn   uint32 `json:"conn"`
			Target uint32 `json:"target"`
			Msg    Num    `json:"msg"`
			Params map[string]struct {
				Type uint32 `json:"type"`
				Bits Num    `json:"bits"`
			} `json:"params"`
		}
		if !decode(w, r, &req) {
			return
		}
		params := map[uint64]statescript.Value{}
		for k, v := range req.Params {
			key, err := strconv.ParseUint(strings.TrimPrefix(k, "0x"), 16, 64)
			if err != nil {
				http.Error(w, "param keys are hex ids", http.StatusBadRequest)
				return
			}
			params[key] = statescript.Value{Type: statescript.Type(v.Type), Bits: uint64(v.Bits)}
		}
		if a.do(w, r, func(m *match.Manager) error { return m.GameSend(req.Conn, req.Target, uint64(req.Msg), params) }) {
			writeJSON(w, map[string]any{"sent": true})
		}
	})
	mux.HandleFunc("POST /jam", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Conn       uint32 `json:"conn"`
			Msg        uint16 `json:"msg"`
			Payload    string `json:"payload"`
			Unreliable bool   `json:"unreliable"`
		}
		if !decode(w, r, &req) {
			return
		}
		payload, err := hex.DecodeString(strings.NewReplacer(" ", "", ":", "").Replace(req.Payload))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, _, ok := jam.InstanceChannelOf(req.Msg); !ok {
			http.Error(w, fmt.Sprintf("message %d isn't on the instance stream's channels", req.Msg), http.StatusBadRequest)
			return
		}
		msg := &jam.Raw{Id: req.Msg, Payload: payload}
		if a.do(w, r, func(m *match.Manager) error { return m.SendJam(req.Conn, msg, !req.Unreliable) }) {
			writeJSON(w, map[string]any{"queued": true, "name": jam.Name(req.Msg)})
		}
	})
	mux.HandleFunc("POST /spawn", func(w http.ResponseWriter, r *http.Request) {
		req := struct {
			Conn  uint32             `json:"conn"`
			ID    uint32             `json:"id"`
			Def   Num                `json:"def"`
			Pos   [3]float32         `json:"pos"`
			Rot   [4]float32         `json:"rot"`
			Scale [3]float32         `json:"scale"`
			Flags uint8              `json:"flags"`
			Map   *uint32            `json:"map_index"`
			Mover *replication.Mover `json:"mover"`
		}{Rot: [4]float32{0, 0, 0, 1}, Scale: [3]float32{1, 1, 1}}
		if !decode(w, r, &req) {
			return
		}
		e := replication.Entry{Entity: replication.EntityID(req.ID), Op: replication.OpCreate, Create: replication.Create{
			Def: uint32(req.Def), Flags: req.Flags, Pos: req.Pos, Rot: req.Rot, Scale: req.Scale, Mover: req.Mover}}
		if req.Map != nil {
			e.Create.FromMap, e.Create.MapIndex = true, *req.Map
		}
		if a.do(w, r, func(m *match.Manager) error { return m.Queue(req.Conn, e) }) {
			writeJSON(w, map[string]any{"queued": true, "entity": fmt.Sprintf("%#x", e.Entity)})
		}
	})
	mux.HandleFunc("POST /filter", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Conn uint32 `json:"conn"`
			ID   uint32 `json:"id"`
			Bits Num    `json:"bits"`
		}
		if !decode(w, r, &req) {
			return
		}
		e := replication.Entry{Entity: replication.EntityID(req.ID), Op: replication.OpUpdate,
			Components: []replication.ComponentUpdate{replication.Default.Mirror.FilterBits(uint64(req.Bits))}}
		if a.do(w, r, func(m *match.Manager) error { return m.Queue(req.Conn, e) }) {
			writeJSON(w, map[string]any{"queued": true, "entity": fmt.Sprintf("%#x", e.Entity)})
		}
	})
	mux.HandleFunc("POST /scripts", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Conn   uint32                         `json:"conn"`
			Entity uint32                         `json:"entity"`
			Graphs []replication.Graph            `json:"graphs"`
			Active map[string][]replication.State `json:"active"`
		}
		if !decode(w, r, &req) {
			return
		}
		active := map[int][]replication.State{}
		for k, v := range req.Active {
			i, err := strconv.Atoi(k)
			if err != nil {
				http.Error(w, "active keys are instance numbers", http.StatusBadRequest)
				return
			}
			active[i] = v
		}
		e := replication.Entry{Entity: replication.EntityID(req.Entity)}
		if a.do(w, r, func(m *match.Manager) error {
			f, ok := m.Frame(req.Conn)
			if !ok {
				return errors.New("no such player")
			}
			e.Scripts = replication.Default.Script.RemoteFullUpdate(f+1, req.Graphs, active)
			return m.Queue(req.Conn, e)
		}) {
			writeJSON(w, map[string]any{"queued": true, "entity": fmt.Sprintf("%#x", e.Entity), "graphs": len(req.Graphs)})
		}
	})
	return mux
}

// do runs f on the game loop; on an error it answers 409 and returns false.
func (a *API) do(w http.ResponseWriter, r *http.Request, f func(m *match.Manager) error) bool {
	if err := a.Server.Do(r.Context(), f); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return false
	}
	return true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

// Num is a JSON number that may also be written as a string ("0x5b", "91").
type Num uint64

func (n *Num) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	v, err := strconv.ParseUint(s, 0, 64)
	if err != nil {
		return fmt.Errorf("number %s: %w", b, err)
	}
	*n = Num(v)
	return nil
}
