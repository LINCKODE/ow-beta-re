package lobby

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

// LoadProtocolDump reads Prometheus's jam_protocols.json ("Dump all to JSON")
// and indexes its entries by protocol CRC. The entries carry each message's
// field table (types, offsets, sizes, nested structs).
func LoadProtocolDump(path string) (map[uint32]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []map[string]any
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, err
	}
	out := map[uint32]any{}
	for _, e := range entries {
		if crc, ok := e["crc"].(float64); ok {
			out[uint32(crc)] = e
		}
	}
	return out, nil
}

// ProtocolDetail is a protocol with its messages' field tables from the dump.
func ProtocolDetail(p *Protocol, dump map[uint32]any) map[string]any {
	d := map[string]any{
		"name":        p.Name,
		"crc":         fmt.Sprintf("0x%08x", p.CRC),
		"first":       p.Base,
		"last":        p.Max - 1,
		"server_type": p.ServerType,
	}
	if e, ok := dump[p.CRC].(map[string]any); ok {
		d["messages"] = e["messages"]
	}
	return d
}

// Control is the gateway's local HTTP API for live experiments.
//
// /eval runs arbitrary Lua inside the gateway on purpose: it's a development
// tool for poking the live client connection. It has no authentication, so it
// must only listen on a loopback address (enforced in ListenAndServe), and the
// Lua state has no os/io libraries (see LuaVM.Reset).
type Control struct {
	Hub  *Hub
	Lua  *LuaVM
	Dump map[uint32]any
}

func (c *Control) ListenAndServe(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", c.help)
	mux.HandleFunc("GET /sessions", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, c.Hub.Sessions()) })
	mux.HandleFunc("GET /protocols", c.protocols)
	mux.HandleFunc("GET /protocols/{name}", c.protocol)
	mux.HandleFunc("GET /traffic", c.traffic)
	mux.HandleFunc("POST /send", c.send)
	mux.HandleFunc("POST /eval", c.eval)
	mux.HandleFunc("POST /lua/reset", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, c.Lua.Reset()+"\n") })

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("control API address %s is not a loopback address; it has an unauthenticated eval", addr)
	}
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	c.Hub.log.Info("control API listening", "addr", "http://"+addr+"/")
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}

const controlHelp = `lobby gateway control API
  GET  /sessions                         connected clients
  GET  /protocols                        all JAM protocols (name, CRC, message ID range)
  GET  /protocols/{name}                 one protocol with its messages' field tables (from the dump)
  GET  /traffic?since=SEQ&n=N&session=S  recent messages (payload hex); poll with since = last seq
  POST /send  {"session":0,"protocol":"","msg":20500,"hex":"..."}
                                         protocol may be empty (looked up by msg); session 0 = newest
  POST /eval  <lua source>               run Lua in the gateway's persistent state (5 s limit)
  POST /lua/reset                        fresh Lua state, rerun the init script

Example: curl -s localhost:3726/eval --data-binary 'send(nil, 20501, "")'

`

func (c *Control) help(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, controlHelp+LuaHelp+"\n")
}

func (c *Control) protocols(w http.ResponseWriter, r *http.Request) {
	var out []map[string]any
	for _, p := range Protocols() {
		d := ProtocolDetail(&p, nil)
		if e, ok := c.Dump[p.CRC].(map[string]any); ok {
			if ms, ok := e["messages"].([]any); ok {
				d["messages"] = len(ms)
			}
		}
		out = append(out, d)
	}
	writeJSON(w, out)
}

func (c *Control) protocol(w http.ResponseWriter, r *http.Request) {
	p := ProtocolByName(r.PathValue("name"))
	if p == nil {
		http.Error(w, "no such protocol", http.StatusNotFound)
		return
	}
	writeJSON(w, ProtocolDetail(p, c.Dump))
}

func (c *Control) traffic(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since, _ := strconv.ParseUint(q.Get("since"), 10, 64)
	session, _ := strconv.ParseUint(q.Get("session"), 10, 64)
	n := 50
	if s := q.Get("n"); s != "" {
		n, _ = strconv.Atoi(s)
	}
	writeJSON(w, c.Hub.Traffic(since, n, session))
}

func (c *Control) send(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Session  uint64 `json:"session"`
		Protocol string `json:"protocol"`
		Msg      uint16 `json:"msg"`
		Hex      string `json:"hex"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	payload, err := hex.DecodeString(req.Hex)
	if err != nil {
		http.Error(w, "hex: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := c.Hub.Send(req.Session, req.Protocol, req.Msg, payload, "http"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	io.WriteString(w, "sent\n")
}

func (c *Control) eval(w http.ResponseWriter, r *http.Request) {
	src, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	out, err := c.Lua.Eval(string(src))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, out+"error: "+err.Error()+"\n")
		return
	}
	io.WriteString(w, out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
