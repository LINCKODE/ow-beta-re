package instance

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Control is the game server's local HTTP API for experiments:
//
//	GET  /conns                 assigned connections
//	GET  /traffic?n=50          recent datagrams (both directions, hex)
//	GET  /jamlog?conn=1&ping=0  JAM messages received from the client (ping=0 leaves out 21608)
//	POST /setup?conn=1          join now: the local controller + the match's world
//	                            (normally done on 21602, map loaded; the map must be loaded)
//	GET  /worlds                the matches' worlds (built at the first assignment) and their connections
//	POST /send                  {"conn": 1, "data": "hex", "raw": false}
//	                            data = header+seq+body, the MAC is prepended;
//	                            raw = true sends data as is
//	POST /jam                   {"conn": 1, "msg": 20301, "payload": "hex", "unreliable": false}
//	                            sends one JAM message on the JAM stream (channel from the message ID)
//	POST /spawn                 {"conn": 1, "id": 1, "def": 14, "pos": [0,0,0], "rot": [0,0,0,1],
//	                             "scale": [1,1,1], "local": true, "flags": 56}
//	                            (flags: see Spawn.Flags; 0x38 = local player controller;
//	                            "map_index": n spawns the map's placed entity n instead of def;
//	                            "mover": {"pos": [x,y,z], "yaw": 0} adds a movement snapshot,
//	                            which entities with a movement state (0x12, doors) need)
//	                            creates entity 0x40000000|id from entity definition 0x04000000_def;
//	                            local = also send 20301 naming it the local entity
//	POST /scripts               {"conn": 1, "local": true}: resends the hero's start-up (what lock-in
//	                            sends: heroes.Start, local full update); {"conn": 1, "active": {"3":
//	                            [{"index": 8, "data": [false]}]}}: remote full update of the hero's
//	                            graphs; active = per instance (1-based) the remote state indexes to
//	                            activate and their state data bits;
//	                            with "graphs": [{"index", "states", "sync_var_ids", "vars"}] those graphs
//	                            instead of the hero's own (states = ss_serialize_local's layout);
//	                            or {"conn": 1, "start_cf": n, "end_cf": n, "payload_hex": "...", "payload_bits": n}
//	                            sends a ready-made payload (Prometheus ss_serialize_local; start_cf 0 =
//	                            full update, else a delta, e.g. from the client's last applied CF);
//	                            or {"conn": 1, "entity": 48, "graphs": [{"index": 41, "sync_vars": 9,
//	                            "remote_nodes": 2}]}: remote full update creating those graphs on any
//	                            networked entity (layouts: Prometheus graph_sync_info); with "active",
//	                            the index is the state's position among the graph's eligible remote
//	                            sync nodes (m_remoteSyncNodes)
//
// It sends arbitrary datagrams to the client, so it only listens on loopback.
type Control struct {
	Server *Server
}

func (c *Control) ListenAndServe(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /conns", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, c.Server.Conns()) })
	mux.HandleFunc("GET /traffic", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		writeJSON(w, c.Server.Traffic(n))
	})
	mux.HandleFunc("GET /jamlog", func(w http.ResponseWriter, r *http.Request) {
		conn, _ := strconv.Atoi(r.URL.Query().Get("conn"))
		writeJSON(w, c.Server.JamLog(uint32(conn), r.URL.Query().Get("ping") == "0"))
	})
	mux.HandleFunc("POST /setup", func(w http.ResponseWriter, r *http.Request) {
		conn, _ := strconv.Atoi(r.URL.Query().Get("conn"))
		if err := c.Server.Setup(uint32(conn)); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, map[string]any{"setup": true})
	})
	mux.HandleFunc("GET /worlds", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, c.Server.Worlds()) })
	mux.HandleFunc("POST /send", c.send)
	mux.HandleFunc("POST /jam", c.jam)
	mux.HandleFunc("POST /spawn", c.spawn)
	mux.HandleFunc("POST /scripts", c.scripts)
	// POST /filter {"conn": N, "id": 2, "bits": 262144}: set an entity's filter bits
	// (component 0x10, team = 0x40000 << t) with a mirrored component update.
	mux.HandleFunc("POST /filter", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Conn uint32 `json:"conn"`
			ID   uint8  `json:"id"`
			Bits uint64 `json:"bits"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		up := Spawn{ID: req.ID, Update: true, Components: []ComponentUpdate{Mirror10(req.Bits)}}
		if err := c.Server.SendPacket(req.Conn, nil, []Spawn{up}); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, map[string]any{"sent": true, "entity": fmt.Sprintf("%#x", up.EntityID()), "bits": fmt.Sprintf("%#x", req.Bits)})
	})

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("control API address %s is not a loopback address", addr)
	}
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	c.Server.Log.Info("control API", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (c *Control) send(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Conn uint32 `json:"conn"`
		Data string `json:"data"`
		Raw  bool   `json:"raw"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, err := hex.DecodeString(strings.NewReplacer(" ", "", ":", "").Replace(req.Data))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := c.Server.Send(req.Conn, data, !req.Raw); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"sent": true})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func (c *Control) jam(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Conn       uint32 `json:"conn"`
		Msg        uint16 `json:"msg"`
		Payload    string `json:"payload"`
		Unreliable bool   `json:"unreliable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	payload, err := hex.DecodeString(strings.NewReplacer(" ", "", ":", "").Replace(req.Payload))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ch, idx, ok := ChannelOf(req.Msg)
	if !ok {
		http.Error(w, fmt.Sprintf("message %d isn't on the JAM stream's channels", req.Msg), http.StatusBadRequest)
		return
	}
	if err := c.Server.SendJam(req.Conn, JamMsg{Reliable: !req.Unreliable, Channel: ch, Index: idx, Payload: payload}); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"sent": true, "channel": ch, "index": idx})
}

func (c *Control) spawn(w http.ResponseWriter, r *http.Request) {
	req := struct {
		Conn  uint32     `json:"conn"`
		ID    uint8      `json:"id"`
		Def   uint32     `json:"def"`
		Pos   [3]float32 `json:"pos"`
		Rot   [4]float32 `json:"rot"`
		Scale [3]float32 `json:"scale"`
		Local bool       `json:"local"`
		Flags uint8      `json:"flags"`
		Map   *uint32    `json:"map_index"` // spawn the map's placed entity with this index instead of def
		// Mover sends a movement snapshot with the create (entities with component
		// 0x12 stay at the origin without one): {"pos": [x,y,z], "yaw": rad, "frame": n}.
		Mover *Mover `json:"mover"`
	}{Rot: [4]float32{0, 0, 0, 1}, Scale: [3]float32{1, 1, 1}}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Flags&^0x3c != 0 {
		http.Error(w, "flags 0x01, 0x02 and 0x40 need data the encoder doesn't write yet", http.StatusBadRequest)
		return
	}
	sp := Spawn{ID: req.ID, Def: req.Def, Pos: req.Pos, Rot: req.Rot, Scale: req.Scale, Flags: req.Flags, Mover: req.Mover}
	if req.Map != nil {
		sp.FromMap, sp.MapIndex = true, *req.Map
	}
	var msgs []JamMsg
	if req.Local {
		msgs = append(msgs, LocalEntityMsg(sp.EntityID()))
	}
	if err := c.Server.SendPacket(req.Conn, msgs, []Spawn{sp}); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"sent": true, "entity": fmt.Sprintf("%#x", sp.EntityID()), "def": fmt.Sprintf("%#016x", 0x0400000000000000|uint64(req.Def))})
}

func (c *Control) scripts(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Conn        uint32 `json:"conn"`
		Local       bool   `json:"local"`
		StartCF     uint32 `json:"start_cf"`
		EndCF       uint32 `json:"end_cf"`
		PayloadHex  string `json:"payload_hex"`
		PayloadBits uint   `json:"payload_bits"`
		// Entity + Graphs: a remote full update for any networked entity instead of
		// the hero body (graph layouts from Prometheus graph_sync_info).
		Entity *uint8 `json:"entity"`
		// With local: true and no entity, Graphs replaces the hero's own graph list in a
		// local full update of the hero body (States = the layout from Prometheus's
		// ss_serialize_local, SyncVarIDs the eligible m_syncVars in slot order, Vars
		// the values to set; e.g. the tutorial's player graph 0xB40).
		Graphs []struct {
			Index       uint16           `json:"index"`
			SyncVars    int              `json:"sync_vars"`
			RemoteNodes int              `json:"remote_nodes"`
			States      string           `json:"states"`
			SyncVarIDs  []uint16         `json:"sync_var_ids"`
			Vars        map[uint16]int32 `json:"vars"`
		} `json:"graphs"`
		Active map[string][]struct {
			Index int    `json:"index"`
			Data  []bool `json:"data"`
			Off   bool   `json:"off"`
		} `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	active := map[int][]ScriptState{}
	for k, states := range req.Active {
		inst, err := strconv.Atoi(k)
		if err != nil {
			http.Error(w, "active keys are instance numbers", http.StatusBadRequest)
			return
		}
		for _, st := range states {
			active[inst] = append(active[inst], ScriptState{Index: st.Index, Data: st.Data, Off: st.Off})
		}
	}
	if req.Entity != nil {
		graphs := make([]ScriptGraph, 0, len(req.Graphs))
		for _, g := range req.Graphs {
			graphs = append(graphs, ScriptGraph{Index: g.Index, SyncVars: g.SyncVars, RemoteNodes: g.RemoteNodes})
		}
		if err := c.Server.SendEntityScripts(req.Conn, *req.Entity, graphs, active); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, map[string]any{"sent": true, "entity": fmt.Sprintf("%#x", 0x40000000|uint32(*req.Entity)), "graphs": len(graphs)})
		return
	}
	if req.Local && len(req.Graphs) > 0 {
		graphs := make([]ScriptGraph, 0, len(req.Graphs))
		for _, g := range req.Graphs {
			graphs = append(graphs, ScriptGraph{Index: g.Index, SyncVars: g.SyncVars, RemoteNodes: g.RemoteNodes,
				States: g.States, SyncVarIDs: g.SyncVarIDs, Vars: g.Vars})
		}
		if err := c.Server.SendHeroGraphs(req.Conn, graphs, active); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, map[string]any{"sent": true, "graphs": len(graphs)})
		return
	}
	if req.PayloadHex != "" {
		payload, err := hex.DecodeString(req.PayloadHex)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := c.Server.SendScriptPayload(req.Conn, req.StartCF, req.EndCF, payload, req.PayloadBits); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, map[string]any{"sent": true})
		return
	}
	if err := c.Server.SendScripts(req.Conn, req.Local, active); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"sent": true})
}
