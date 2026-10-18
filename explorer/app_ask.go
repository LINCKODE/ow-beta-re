package main

// "Ask Claude" (docs/Explorer.md "Ask Claude"): the explorer asks the Claude Code session that is connected to its MCP
// server. No second Claude process: the session subscribes to an event stream with its Monitor tool
// (curl -sN http://127.0.0.1:17889/mcp/events), every Ask is one line (the question + the selection), and the session
// answers with the MCP tool `reply`, which the Ask panel shows.
//
// Windows: the stream is served by the window holding the base port (the hub; the one .mcp.json names). Another window
// publishes its asks to the hub (POST /mcp/events/publish); a reply reaches the hub and is forwarded to the window that
// asked (the ask id is "<port>-<n>").

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"owexplorer/internal/mcp"
)

// AskSubject is what the user asks about (the frontend fills in what applies).
type AskSubject struct {
	Kind     string `json:"kind"` // placement, graphNode, resource, texture, field, view, model, submesh, material
	Label    string `json:"label,omitempty"`
	GUID     string `json:"guid,omitempty"`
	Map      string `json:"map,omitempty"`
	Record   *int   `json:"record,omitempty"`
	Node     *int   `json:"node,omitempty"` // m_nodes index
	Instance *int   `json:"instance,omitempty"`
	Path     string `json:"path,omitempty"`
	Mip      *int   `json:"mip,omitempty"`
	Submesh  *int   `json:"submesh,omitempty"` // model submesh (T064)
	Look     string `json:"look,omitempty"`    // the look the Model tab shows
}

// AskMsg is one message of the Ask panel's conversation.
type AskMsg struct {
	ID      string      `json:"id"`   // the ask's id (a reply carries the id it answers)
	Role    string      `json:"role"` // user, claude, note
	Text    string      `json:"text"`
	Subject *AskSubject `json:"subject,omitempty"`
	At      int64       `json:"at"`              // unix ms
	State   string      `json:"state,omitempty"` // user messages: waiting, answered, cancelled
}

// AskState is the panel's state.
type AskState struct {
	Listeners int      `json:"listeners"` // Claude sessions subscribed to the hub's stream
	Hub       int      `json:"hub"`       // the hub's port
	Events    string   `json:"events"`    // the stream's URL
	Messages  []AskMsg `json:"messages"`
	Error     string   `json:"error,omitempty"`
}

type askHub struct {
	mu      sync.Mutex
	subs    map[chan []byte]bool
	pending []pendingAsk // unanswered asks, replayed to a new subscriber

	cmu  sync.Mutex
	msgs []AskMsg // this window's conversation
	n    int
}

type pendingAsk struct {
	id   string
	line []byte // the stream line (short)
	full []byte // the whole event with its context (the asks tool)
	at   time.Time
}

// askLine is an ask as one stream line. It stays short (question first, no context): a Monitor notification shows
// only the start of a long line. The context is in the asks tool.
type askLine struct {
	Type       string      `json:"type"`
	ID         string      `json:"id"`
	Question   string      `json:"question"`
	About      string      `json:"about,omitempty"`
	Subject    *AskSubject `json:"subject,omitempty"`
	FollowUpOf string      `json:"followUpOf,omitempty"`
	Next       string      `json:"next"`
}

func shortLine(ev map[string]any) []byte {
	if ev["type"] != "ask" {
		b, _ := json.Marshal(ev)
		return b
	}
	l := askLine{Type: "ask", Next: "call the owexplorer tool asks for this question's context (the selection), investigate, then reply {id, text}"}
	l.ID, _ = ev["id"].(string)
	l.Question, _ = ev["question"].(string)
	l.FollowUpOf, _ = ev["followUpOf"].(string)
	if raw, err := json.Marshal(ev["subject"]); err == nil {
		var sj AskSubject
		if json.Unmarshal(raw, &sj) == nil && sj.Kind != "" {
			l.About, sj.Label = sj.Label, ""
			l.Subject = &sj
		}
	}
	b, _ := json.Marshal(l)
	return b
}

func (a *App) askHub() *askHub {
	m := a.mcpInit()
	m.vmu.Lock()
	defer m.vmu.Unlock()
	if a.ask == nil {
		a.ask = &askHub{subs: map[chan []byte]bool{}}
	}
	return a.ask
}

// askRoutes are the stream endpoints (registered on the MCP server).
func (a *App) askRoutes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"/mcp/events": a.serveEvents,
		"/mcp/events/publish": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "POST only", http.StatusMethodNotAllowed)
				return
			}
			var ev map[string]any
			if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			a.askHub().publish(ev)
			w.WriteHeader(http.StatusNoContent)
		},
	}
}

func (h *askHub) listeners() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// publish sends an event to every subscriber; an ask stays pending until it is answered or cancelled.
func (h *askHub) publish(ev map[string]any) {
	line := shortLine(ev)
	full, _ := json.Marshal(ev)
	id, _ := ev["id"].(string)
	h.mu.Lock()
	defer h.mu.Unlock()
	switch ev["type"] {
	case "ask":
		h.pending = append(h.pending, pendingAsk{id, line, full, time.Now()})
	case "cancel":
		h.dropLocked(id)
	}
	for ch := range h.subs {
		select {
		case ch <- line:
		default: // a stuck subscriber doesn't block the others
		}
	}
}

func (h *askHub) drop(id string) {
	h.mu.Lock()
	h.dropLocked(id)
	h.mu.Unlock()
}

func (h *askHub) dropLocked(id string) {
	for i, p := range h.pending {
		if p.id == id {
			h.pending = append(h.pending[:i:i], h.pending[i+1:]...)
			return
		}
	}
}

// serveEvents is the stream a Claude session watches: one JSON line per event, no heartbeats (every line wakes the
// session). First a "subscribed" line, then the unanswered asks, then new events as they come.
func (a *App) serveEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	h := a.askHub()
	ch := make(chan []byte, 64)
	h.mu.Lock()
	h.subs[ch] = true
	backlog := make([][]byte, 0, len(h.pending))
	for _, p := range h.pending {
		backlog = append(backlog, p.line)
	}
	h.mu.Unlock()
	a.askChanged()
	defer func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
		a.askChanged()
	}()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	hello, _ := json.Marshal(map[string]any{"type": "subscribed", "server": "owexplorer", "pending": len(backlog),
		"howto": "each ask line: answer with the owexplorer tools, then call the MCP tool reply {id, text}; the user reads the reply in the explorer's Ask panel"})
	w.Write(append(hello, '\n'))
	for _, l := range backlog {
		w.Write(append(l, '\n'))
	}
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case l := <-ch:
			if _, err := w.Write(append(l, '\n')); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// askChanged tells the frontend to refresh the panel.
func (a *App) askChanged() {
	if a.mcp != nil {
		a.mcp.emit("ask", nil)
	}
}

func (h *askHub) add(m AskMsg) {
	h.cmu.Lock()
	h.msgs = append(h.msgs, m)
	h.cmu.Unlock()
}

func (h *askHub) setState(id, state string) bool {
	h.cmu.Lock()
	defer h.cmu.Unlock()
	for i := range h.msgs {
		if h.msgs[i].ID == id && h.msgs[i].Role == "user" {
			h.msgs[i].State = state
			return true
		}
	}
	return false
}

// ---- bound methods (the Ask panel) ------------------------------------------------------------------

// Ask sends a question about a subject to the listening Claude session(s); returns the ask id.
func (a *App) Ask(question string, subject AskSubject, followUp string) (string, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return "", fmt.Errorf("empty question")
	}
	m := a.mcpInit()
	if m.port == 0 {
		return "", fmt.Errorf("the MCP server is not running (%s)", m.err)
	}
	h := a.askHub()
	h.cmu.Lock()
	h.n++
	id := fmt.Sprintf("%d-%d", m.port, h.n)
	h.cmu.Unlock()
	ev := map[string]any{"type": "ask", "id": id, "question": question, "subject": subject,
		"window": m.port, "at": time.Now().Format(time.TimeOnly)}
	if followUp != "" {
		ev["followUpOf"] = followUp
	}
	if ctx := a.askContext(subject); ctx != nil {
		ev["context"] = ctx
	}
	ev["reply"] = fmt.Sprintf("call the owexplorer MCP tool reply with id %q and your answer (markdown)", id)
	if b, _ := json.Marshal(ev); len(b) > 16<<10 { // the session can call the tools
		ev["context"] = "too large for the event: call describe / placement / statescript_graph for the subject"
	}
	subj := subject
	h.add(AskMsg{ID: id, Role: "user", Text: question, Subject: &subj, At: time.Now().UnixMilli(), State: "waiting"})
	if err := a.publishAsk(ev); err != nil {
		h.setState(id, "cancelled")
		a.askChanged()
		return "", err
	}
	a.askChanged()
	return id, nil
}

// publishAsk publishes on this window's hub (it holds the base port) or the base port's.
func (a *App) publishAsk(ev map[string]any) error {
	m := a.mcpInit()
	if m.srv.Primary() || m.base == 0 {
		a.askHub().publish(ev)
		return nil
	}
	b, _ := json.Marshal(ev)
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/mcp/events/publish", m.base), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("the explorer window on port %d (the one Claude listens to) is not reachable: %v", m.base, err)
	}
	r.Body.Close()
	return nil
}

// AskCancel withdraws an unanswered ask (the panel's stop button).
func (a *App) AskCancel(id string) {
	h := a.askHub()
	if h.setState(id, "cancelled") {
		_ = a.publishAsk(map[string]any{"type": "cancel", "id": id})
	}
	a.askChanged()
}

// AskClear starts a new conversation in the panel (unanswered asks are withdrawn).
func (a *App) AskClear() {
	h := a.askHub()
	h.cmu.Lock()
	var open []string
	for _, m := range h.msgs {
		if m.Role == "user" && m.State == "waiting" {
			open = append(open, m.ID)
		}
	}
	h.msgs = nil
	h.cmu.Unlock()
	for _, id := range open {
		_ = a.publishAsk(map[string]any{"type": "cancel", "id": id})
	}
	a.askChanged()
}

func (a *App) AskState() AskState {
	m := a.mcpInit()
	h := a.askHub()
	st := AskState{Hub: m.base, Events: fmt.Sprintf("http://127.0.0.1:%d/mcp/events", m.base)}
	if m.port == 0 {
		st.Error = "the MCP server is not running: " + m.err
	}
	if m.srv.Primary() {
		st.Listeners = h.listeners()
	} else if m.base != 0 { // ask the hub
		if r, err := (&http.Client{Timeout: time.Second}).Get(fmt.Sprintf("http://127.0.0.1:%d/mcp/health", m.base)); err == nil {
			var hh map[string]any
			_ = json.NewDecoder(r.Body).Decode(&hh)
			r.Body.Close()
			if n, ok := hh["listeners"].(float64); ok {
				st.Listeners = int(n)
			}
		}
	}
	h.cmu.Lock()
	st.Messages = append([]AskMsg{}, h.msgs...)
	h.cmu.Unlock()
	return st
}

// PlacementBrief is one map record with its primary resource and, for an entity, its definition's graphs (the map view's
// selection card).
func (a *App) PlacementBrief(guid string, record int) (map[string]any, error) {
	return a.placementBrief(guid, record)
}

// askContext is what the ask event carries about its subject (the session can call the tools for more).
func (a *App) askContext(s AskSubject) any {
	call := func(name string, args map[string]any) any {
		b, _ := json.Marshal(args)
		res, err := a.mcp.srv.Call(context.Background(), name, b)
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		return res
	}
	switch s.Kind {
	case "placement":
		if s.Record != nil {
			if pb, err := a.placementBrief(s.Map, *s.Record); err == nil {
				if s.Instance != nil { // a model instance picked in the 3D view (T064)
					return map[string]any{"map": s.Map, "placement": pb, "modelInstance": *s.Instance}
				}
				return map[string]any{"map": s.Map, "placement": pb}
			} else {
				return map[string]any{"error": err.Error()}
			}
		}
	case "graphNode":
		if s.Node != nil {
			return call("statescript_graph", map[string]any{"guid": s.GUID, "node": *s.Node})
		}
	case "field":
		args := map[string]any{"guid": s.GUID, "depth": 2}
		if s.Path != "" {
			args["path"] = s.Path
		} else if s.Instance != nil {
			args["instance"] = *s.Instance
		}
		return call("stu_tree", args)
	case "resource", "texture":
		if s.GUID != "" {
			res := call("describe", map[string]any{"guid": s.GUID})
			if s.Mip != nil {
				return map[string]any{"describe": res, "mip": *s.Mip}
			}
			return res
		}
	case "model", "submesh", "material":
		if s.GUID != "" {
			res := call("describe", map[string]any{"guid": s.GUID})
			if s.Submesh != nil {
				sel := map[string]any{}
				a.modelSelection(map[string]any{"model": s.GUID, "look": s.Look, "submesh": float64(*s.Submesh)}, sel)
				return map[string]any{"describe": res, "submesh": sel["submeshDetail"], "look": s.Look}
			}
			return res
		}
	case "view":
		return nil // the session calls current_view
	}
	return nil
}

// ---- MCP tools: reply, asks -----------------------------------------------------------------------

func (a *App) askTools() []mcp.Tool {
	return []mcp.Tool{
		tool("reply", "Answer an Ask from the explorer's \"Ask Claude\" button: the text (markdown) is shown in the "+
			"explorer's Ask panel. Links: 16-hex-digit GUIDs; ow://<graph>/graph/<node index> (e.g. [#57 Wait](ow://05800000000001db/graph/57)) "+
			"centres a Statescript node; ow://<map>/map/<record> a map placement; ow://<guid>/tree/<instance>; "+
			"ow://<texture>/texture/<mip>; a bare #N = node N of the last graph GUID named before it (or of the graph asked "+
			"about). Several replies to one id are allowed (final=false for an interim note).",
			obj(map[string]any{"id": pStr("the ask's id (from the event line)"), "text": pStr("the answer, markdown"),
				"final": map[string]any{"type": "boolean", "description": "default true: the question is answered"}}, "id", "text"),
			typed(a.toolReply)),
		tool("asks", "The explorer's unanswered Ask questions with their context (the selection: placement, graph node, "+
			"resource summary, …). The event stream's lines are short; call this for the context. Subscribe to new "+
			"questions: a Monitor on `while true; do curl -sN http://127.0.0.1:17889/mcp/events; sleep 2; done`.", obj(map[string]any{}), typed(a.toolAsks)),
	}
}

func (a *App) toolReply(ctx context.Context, q struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Final *bool  `json:"final"`
}) (any, error) {
	port, err := strconv.Atoi(strings.SplitN(q.ID, "-", 2)[0])
	if err != nil || !strings.Contains(q.ID, "-") {
		return nil, fmt.Errorf("not an ask id: %q", q.ID)
	}
	final := q.Final == nil || *q.Final
	h := a.askHub()
	if final {
		h.drop(q.ID)
	}
	m := a.mcpInit()
	if !contains(m.srv.Ports(), port) { // asked in another window
		args := map[string]any{"id": q.ID, "text": q.Text, "final": final}
		res, err := mcp.CallPeer(ctx, port, "reply", args)
		if err != nil {
			return nil, fmt.Errorf("the window that asked (port %d) is gone: %v", port, err)
		}
		return res, nil
	}
	if !h.setState(q.ID, map[bool]string{true: "answered", false: "waiting"}[final]) {
		return nil, fmt.Errorf("no ask %q in this window (cleared?)", q.ID)
	}
	h.add(AskMsg{ID: q.ID, Role: "claude", Text: q.Text, At: time.Now().UnixMilli()})
	a.askChanged()
	return map[string]any{"ok": true, "shown": "in the explorer's Ask panel"}, nil
}

func (a *App) toolAsks(ctx context.Context, _ struct{}) (any, error) {
	h := a.askHub()
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []json.RawMessage{}
	for _, p := range h.pending {
		out = append(out, p.full)
	}
	return map[string]any{"pending": out, "listeners": len(h.subs)}, nil
}
