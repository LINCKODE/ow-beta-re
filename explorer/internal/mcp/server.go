// Package mcp is the explorer's MCP server (docs/Explorer.md "MCP server"): a localhost HTTP server that lets a Claude
// session call the explorer's tools.
//
// Two transports on the same port:
//   - MCP streamable HTTP (JSON response mode): POST /mcp with JSON-RPC 2.0 (initialize, tools/list, tools/call, ping;
//     notifications get 202). GET /mcp is 405: the server never streams. Claude Code: {"type": "http", "url": …/mcp}.
//   - Prometheus' plain shape (prometheus/Prometheus-Core/MCP/McpServer.cpp): POST /mcp/tools/list,
//     POST /mcp/tools/call {"name", "arguments"} → {"isError", "result" | "error"}, GET /mcp/health.
//
// Requests from web pages are refused (an Origin header other than localhost), so a browser tab can't call the tools.
package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Tool is one callable tool. Drive tools change the UI and run only while the window's drive toggle is on.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any // JSON schema of the arguments (an object)
	Drive       bool
	Handler     func(ctx context.Context, args json.RawMessage) (any, error)
}

// MaxResult caps a tool result (JSON bytes); bigger results become an error asking for a narrower query.
const MaxResult = 512 << 10

// MaxContent caps a Content result (e.g. an image), base64 included.
const MaxContent = 8 << 20

// Content is a tool result made of MCP content parts (text, image) instead of one JSON text part. The plain
// /mcp/tools/call endpoint returns it as {"content": [...]}.
type Content []map[string]any

// Text is a text part.
func Text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

// Image is an image part (base64 data).
func Image(data []byte, mime string) map[string]any {
	return map[string]any{"type": "image", "data": base64.StdEncoding.EncodeToString(data), "mimeType": mime}
}

// Server serves a set of tools.
type Server struct {
	Name, Version, Instructions string
	// DriveEnabled reports the drive toggle (nil = always off).
	DriveEnabled func() bool
	// Health adds fields to GET /mcp/health.
	Health func() map[string]any
	// OnCall runs after every tool call (the UI's call counter).
	OnCall func(name string, err error)
	// Routes adds HTTP handlers (e.g. the Ask event stream); set before Listen / Handler.
	Routes map[string]http.HandlerFunc

	tools    []Tool
	byName   map[string]int
	requests atomic.Int64
	failed   atomic.Int64

	mu      sync.Mutex
	ports   []int // ports listened on; the first one is the one this window was given
	base    int
	servers []*http.Server
	stop    chan struct{}
}

func New(name, version string, tools []Tool) *Server {
	s := &Server{Name: name, Version: version, tools: tools, byName: map[string]int{}, stop: make(chan struct{})}
	for i, t := range tools {
		s.byName[t.Name] = i
	}
	return s
}

// Tools lists the tools (for tests and docs).
func (s *Server) Tools() []Tool { return s.tools }

// Requests is the number of tool calls served and how many failed.
func (s *Server) Requests() (total, failed int64) { return s.requests.Load(), s.failed.Load() }

// ---- tool calls -----------------------------------------------------------------------------------

// ErrDriveOff is returned by drive tools while the toggle is off.
var ErrDriveOff = errors.New("drive tools are off: the user enables them with the MCP toggle in the explorer's status bar")

// Call runs a tool: the result, or an error (unknown tool, bad arguments, drive off, the tool's own error).
func (s *Server) Call(ctx context.Context, name string, args json.RawMessage) (res any, err error) {
	s.requests.Add(1)
	defer func() {
		if r := recover(); r != nil {
			res, err = nil, fmt.Errorf("panic in %s: %v", name, r)
		}
		if err != nil {
			s.failed.Add(1)
		}
		if s.OnCall != nil {
			s.OnCall(name, err)
		}
	}()
	i, ok := s.byName[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %q", name)
	}
	t := s.tools[i]
	if t.Drive && (s.DriveEnabled == nil || !s.DriveEnabled()) {
		return nil, ErrDriveOff
	}
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	res, err = t.Handler(ctx, args)
	if err != nil {
		return nil, err
	}
	if c, ok := res.(Content); ok {
		n := 0
		for _, p := range c {
			for _, v := range p {
				if s, ok := v.(string); ok {
					n += len(s)
				}
			}
		}
		if n > MaxContent {
			return nil, fmt.Errorf("result too large (%d bytes, cap %d): ask for a smaller image", n, MaxContent)
		}
		return c, nil
	}
	b, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("result of %s: %w", name, err)
	}
	if len(b) > MaxResult {
		return nil, fmt.Errorf("result too large (%d bytes, cap %d): narrow the query (limit / offset / depth / filters)", len(b), MaxResult)
	}
	return json.RawMessage(b), nil
}

// Args decodes a tool's arguments strictly (unknown fields are an error: a typo shouldn't be silently ignored).
func Args(raw json.RawMessage, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("arguments: %w", err)
	}
	return nil
}

// ---- HTTP -----------------------------------------------------------------------------------------

// localOrigin: no Origin (not a browser) or a local one. The explorer's own webview doesn't call the server.
func localOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	h := u.Hostname()
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Handler is the HTTP handler (also used by tests through httptest).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.serveRPC)
	mux.HandleFunc("/mcp/tools/list", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
			return
		}
		writeJSON(w, 200, map[string]any{"tools": s.toolList()})
	})
	mux.HandleFunc("/mcp/tools/call", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
			return
		}
		var body struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			writeJSON(w, 400, map[string]any{"isError": true, "error": fmt.Sprintf("bad request: want {\"name\", \"arguments\"} (%v)", err)})
			return
		}
		res, err := s.Call(r.Context(), body.Name, body.Arguments)
		if err != nil {
			writeJSON(w, 200, map[string]any{"isError": true, "error": err.Error()})
			return
		}
		if c, ok := res.(Content); ok {
			res = map[string]any{"content": c}
		}
		writeJSON(w, 200, map[string]any{"isError": false, "result": res})
	})
	mux.HandleFunc("/mcp/health", func(w http.ResponseWriter, r *http.Request) {
		total, failed := s.Requests()
		h := map[string]any{"ok": true, "server": s.Name, "pid": os.Getpid(), "ports": s.Ports(),
			"requests_total": total, "requests_failed": failed, "drive_enabled": s.DriveEnabled != nil && s.DriveEnabled()}
		if s.Health != nil {
			for k, v := range s.Health() {
				h[k] = v
			}
		}
		writeJSON(w, 200, h)
	})
	for path, h := range s.Routes {
		mux.HandleFunc(path, h)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localOrigin(r) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) toolList() []map[string]any {
	out := []map[string]any{}
	for _, t := range s.tools {
		desc := t.Description
		if t.Drive {
			desc += " [drive tool: changes the user's view; only while the user has enabled drive tools in the explorer]"
		}
		schema := t.Schema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{"name": t.Name, "description": desc, "inputSchema": schema})
	}
	return out
}

// ---- JSON-RPC (MCP streamable HTTP, JSON responses) ----------------------------------------------

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

func (s *Server) serveRPC(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete: // session end: the server is stateless
		w.WriteHeader(http.StatusOK)
		return
	default: // GET would open an SSE stream: this server never sends unsolicited messages
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST only (no server-sent events)", http.StatusMethodNotAllowed)
		return
	}
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, 400, rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32700, "parse error: " + err.Error()}})
		return
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '[' { // a batch (protocol 2025-03-26)
		var reqs []rpcReq
		if err := json.Unmarshal(raw, &reqs); err != nil {
			writeJSON(w, 400, rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32600, err.Error()}})
			return
		}
		var out []rpcResp
		for _, q := range reqs {
			if resp, ok := s.rpc(r.Context(), q); ok {
				out = append(out, resp)
			}
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSON(w, 200, out)
		return
	}
	var q rpcReq
	if err := json.Unmarshal(raw, &q); err != nil {
		writeJSON(w, 400, rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32600, err.Error()}})
		return
	}
	resp, ok := s.rpc(r.Context(), q)
	if !ok {
		w.WriteHeader(http.StatusAccepted) // a notification or a response: nothing to answer
		return
	}
	writeJSON(w, 200, resp)
}

// rpc answers one message; ok = false for notifications (no id).
func (s *Server) rpc(ctx context.Context, q rpcReq) (rpcResp, bool) {
	if len(q.ID) == 0 || string(q.ID) == "null" {
		return rpcResp{}, false
	}
	resp := rpcResp{JSONRPC: "2.0", ID: q.ID}
	switch q.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(q.Params, &p)
		v := protocolVersions[0]
		if slices.Contains(protocolVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		resp.Result = map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
			"instructions":    s.Instructions,
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": s.toolList()}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(q.Params, &p); err != nil || p.Name == "" {
			resp.Error = &rpcErr{-32602, fmt.Sprintf("invalid params: want {name, arguments} (%v)", err)}
			break
		}
		if _, ok := s.byName[p.Name]; !ok {
			resp.Error = &rpcErr{-32602, "unknown tool: " + p.Name}
			break
		}
		res, err := s.Call(ctx, p.Name, p.Arguments)
		if c, ok := res.(Content); ok && err == nil {
			resp.Result = map[string]any{"content": c, "isError": false}
			break
		}
		var text string
		if err != nil {
			b, _ := json.Marshal(map[string]string{"error": err.Error()})
			text = string(b)
		} else {
			b, _ := json.Marshal(res)
			text = string(b)
		}
		resp.Result = map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": err != nil}
	default:
		resp.Error = &rpcErr{-32601, "method not found: " + q.Method}
	}
	return resp, true
}

// ---- listening ------------------------------------------------------------------------------------

// Listen serves on the first free port of base … base+span-1 (every explorer window has its own server). A window that
// didn't get base retries it every few seconds, so the configured URL (base) keeps working when the first window closes.
func (s *Server) Listen(base, span int) (int, error) {
	s.mu.Lock()
	s.base = base
	s.mu.Unlock()
	for p := base; p < base+span; p++ {
		if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p)); err == nil {
			s.serve(ln, p)
			if p != base {
				go s.claimBase()
			}
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port in 127.0.0.1:%d-%d", base, base+span-1)
}

func (s *Server) serve(ln net.Listener, port int) {
	hs := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	s.mu.Lock()
	s.ports = append(s.ports, port)
	s.servers = append(s.servers, hs)
	s.mu.Unlock()
	go hs.Serve(ln)
}

func (s *Server) claimBase() {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.base)); err == nil {
				s.serve(ln, s.base)
				return
			}
		}
	}
}

// Ports are the ports this server listens on (the first: its own; later: the base port it took over).
func (s *Server) Ports() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.ports)
}

// Primary: this server holds the base port (the one .mcp.json names).
func (s *Server) Primary() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Contains(s.ports, s.base)
}

func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	for _, hs := range s.servers {
		_ = hs.Close()
	}
}

// ---- other windows --------------------------------------------------------------------------------

var peerClient = &http.Client{Timeout: 3 * time.Second}

// Peer is another explorer window's server.
type Peer struct {
	Port   int            `json:"port"`
	Health map[string]any `json:"health"`
}

// Peers probes base … base+span-1 for other explorer servers (not this process).
func (s *Server) Peers(base, span int) []Peer {
	var out []Peer
	if base <= 0 { // not listening (tests)
		return nil
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	own := s.Ports()
	for p := base; p < base+span; p++ {
		if slices.Contains(own, p) {
			continue
		}
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			c := &http.Client{Timeout: 400 * time.Millisecond}
			r, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/mcp/health", p))
			if err != nil {
				return
			}
			defer r.Body.Close()
			var h map[string]any
			if json.NewDecoder(r.Body).Decode(&h) != nil || h["server"] != s.Name {
				return
			}
			if pid, _ := h["pid"].(float64); int(pid) == os.Getpid() {
				return
			}
			mu.Lock()
			out = append(out, Peer{Port: p, Health: h})
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	slices.SortFunc(out, func(a, b Peer) int { return a.Port - b.Port })
	return out
}

// CallPeer calls a tool on another window's server.
func CallPeer(ctx context.Context, port int, name string, args any) (json.RawMessage, error) {
	body, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/mcp/tools/call", port), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	r, err := peerClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	var out struct {
		IsError bool            `json:"isError"`
		Error   string          `json:"error"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.IsError {
		return nil, errors.New(out.Error)
	}
	return out.Result, nil
}
