package main

// The explorer's MCP server (docs/Explorer.md "MCP server"): read tools over the catalogue and the user's current view,
// and drive tools (open, select) behind a UI toggle. Transport: internal/mcp. Read tools: app_mcp_tools.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"owexplorer/internal/catalog"
	"owexplorer/internal/mcp"
	"owexplorer/internal/view"
	"owstu/stu"
)

// Every window runs its own server on the first free port of mcpBase … mcpBase+mcpSpan-1; mcpBase is the one in
// .mcp.json (a window that didn't get it takes it over when it frees up).
const (
	mcpBase = 17889
	mcpSpan = 16
)

type mcpState struct {
	srv   *mcp.Server
	port  int
	base  int // the hub's port (.mcp.json's): mcpBase unless $OWEXPLORER_MCP says otherwise
	err   string
	drive atomic.Bool

	vmu    sync.Mutex
	view   json.RawMessage // the frontend's last snapshot (SetView)
	viewAt time.Time
	seq    int64
	notify chan struct{} // closed and replaced on every SetView

	emit func(name string, data any) // frontend events (Wails; tests capture them)
}

// McpInfo is the MCP server's state for the status bar.
type McpInfo struct {
	Port     int    `json:"port"`
	Ports    []int  `json:"ports"`
	Primary  bool   `json:"primary"`
	Drive    bool   `json:"drive"`
	Requests int64  `json:"requests"`
	Failed   int64  `json:"failed"`
	Last     string `json:"last,omitempty"` // last tool called
	Error    string `json:"error,omitempty"`
}

var mcpLast atomic.Value // string

func (a *App) mcpInit() *mcpState {
	if a.mcp != nil {
		return a.mcp
	}
	m := &mcpState{notify: make(chan struct{})}
	m.emit = func(name string, data any) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, name, data)
		}
	}
	m.srv = mcp.New("owexplorer", "1", append(a.mcpTools(), a.askTools()...))
	m.srv.Routes = a.askRoutes()
	m.srv.Instructions = mcpInstructions
	m.srv.DriveEnabled = m.drive.Load
	m.srv.OnCall = func(name string, err error) {
		mcpLast.Store(name)
		m.emit("mcp", a.McpStatus())
	}
	m.srv.Health = func() map[string]any {
		h := map[string]any{"child": a.child, "link": a.StartupLink(), "listeners": a.askHub().listeners()}
		if v, ok := a.snapshot(); ok {
			if len(v.Panes) > 0 {
				for _, p := range v.Panes {
					if p.ID == v.Focus && p.Resource != nil {
						h["resource"] = p.Resource.GUID
					}
				}
			}
		}
		return h
	}
	a.mcp = m
	return m
}

// startMCP starts this window's server (App.startup). $OWEXPLORER_MCP=0 disables it; =<port> changes the base.
func (a *App) startMCP() {
	m := a.mcpInit()
	base := mcpBase
	if env := os.Getenv("OWEXPLORER_MCP"); env != "" {
		if env == "0" || env == "off" {
			m.err = "disabled ($OWEXPLORER_MCP)"
			return
		}
		if p, err := strconv.Atoi(env); err == nil && p > 0 {
			base = p
		}
	}
	m.base = base
	port, err := m.srv.Listen(base, mcpSpan)
	if err != nil {
		m.err = err.Error()
		return
	}
	m.port = port
}

const mcpInstructions = `Storage explorer for the Overwatch 0.8 beta install (desktop app, docs/Explorer.md).
Start with current_view: what the user is looking at in every explorer window (open resource, tab, selection:
map placement, graph node, tree path, texture mip, 3D camera / cursor point). Then describe / entity /
statescript_graph / placement / stu_tree / refs / map_placements / search to explain it. GUIDs are 16 hex digits
(0x…, ow://… and short forms are accepted). Drive tools (open, select) only work while the user has enabled them.

The explorer has an "Ask Claude" button. When the user asks you to watch / listen to the explorer, subscribe with the
Monitor tool: command ` + "`while true; do curl -sN http://127.0.0.1:17889/mcp/events; sleep 2; done`" + ` (it reconnects when
the explorer restarts), timeout_ms 1800000, and re-arm it when it expires (unanswered questions are replayed on reconnect; the asks tool lists them too). Every line of type "ask" is
a question from the user about a selection: call asks for its context, investigate with these tools and the repository's
docs (docs/README.md), then call reply {id, text} with a concise markdown answer (say what is measured vs inferred);
the user reads it in the explorer's Ask panel. A "cancel" line withdraws a question.

References in replies become links in the panel: a 16-hex-digit GUID opens the resource;
ow://<graph>/graph/<node index> (bare or as [#57 Wait](ow://…)) opens a Statescript graph and centres that node;
ow://<map>/map/<record> focuses a map placement; ow://<guid>/tree/<instance>, ow://<texture>/texture/<mip>. A bare #N is
node N of the last graph GUID named before it in the reply (or of the graph the question is about).`

// ---- frontend → backend ---------------------------------------------------------------------------

// SetView receives the frontend's view snapshot (JSON; composables/useExplorer.ts mcpSnapshot).
func (a *App) SetView(snapshot string) {
	m := a.mcpInit()
	m.vmu.Lock()
	m.view = json.RawMessage(snapshot)
	m.viewAt = time.Now()
	m.seq++
	close(m.notify)
	m.notify = make(chan struct{})
	m.vmu.Unlock()
}

func (a *App) McpStatus() McpInfo {
	m := a.mcpInit()
	total, failed := m.srv.Requests()
	info := McpInfo{Port: m.port, Ports: m.srv.Ports(), Primary: m.srv.Primary(), Drive: m.drive.Load(), Requests: total,
		Failed: failed, Error: m.err}
	if s, ok := mcpLast.Load().(string); ok {
		info.Last = s
	}
	return info
}

// SetMcpDrive is the UI toggle for the drive tools (off at every start).
func (a *App) SetMcpDrive(on bool) McpInfo {
	a.mcpInit().drive.Store(on)
	return a.McpStatus()
}

// ---- the view snapshot ----------------------------------------------------------------------------

type snapResource struct {
	GUID      string `json:"guid"`
	Type      uint32 `json:"type"`
	TypeLabel string `json:"typeLabel"`
	Kind      string `json:"kind"`
}

type snapDoc struct {
	GUID  string `json:"guid"`
	Tab   string `json:"tab"`
	Label string `json:"label"`
}

type snapPane struct {
	ID        int                        `json:"id"`
	Current   string                     `json:"current"`
	Resource  *snapResource              `json:"resource"`
	Error     string                     `json:"error"`
	Tab       string                     `json:"tab"`
	Instance  int                        `json:"instance"`
	Path      string                     `json:"path"`
	GraphNode int                        `json:"graphNode"`
	Docs      []snapDoc                  `json:"docs"`
	Active    int                        `json:"active"`
	History   int                        `json:"history"`
	Extra     map[string]json.RawMessage `json:"extra"`
}

type snapshot struct {
	Focus     int        `json:"focus"`
	Wide      bool       `json:"wide"`
	MapMax    int        `json:"mapMax"`
	MapCursor string     `json:"mapCursor"`
	Rail      string     `json:"rail"`
	Filter    any        `json:"filter"`
	Panes     []snapPane `json:"panes"`
}

func (a *App) snapshot() (snapshot, bool) {
	m := a.mcpInit()
	m.vmu.Lock()
	raw := m.view
	m.vmu.Unlock()
	var s snapshot
	if raw == nil || json.Unmarshal(raw, &s) != nil {
		return s, false
	}
	return s, true
}

// waitView waits (up to d) for a snapshot that satisfies ok, after the one numbered after.
func (a *App) waitView(after int64, d time.Duration, ok func(snapshot) bool) (snapshot, bool) {
	m := a.mcpInit()
	deadline := time.After(d)
	for {
		m.vmu.Lock()
		seq, ch := m.seq, m.notify
		m.vmu.Unlock()
		if seq > after {
			if s, good := a.snapshot(); good && ok(s) {
				return s, true
			}
		}
		select {
		case <-ch:
		case <-deadline:
			s, _ := a.snapshot()
			return s, false
		}
	}
}

// ---- current_view ---------------------------------------------------------------------------------

type paneView struct {
	Pane      string         `json:"pane"` // main, split
	Focused   bool           `json:"focused"`
	Resource  any            `json:"resource,omitempty"`
	Tab       string         `json:"tab"`
	Selection map[string]any `json:"selection,omitempty"`
	View      any            `json:"view,omitempty"` // the tab's own state (map mode / camera, texture display, …)
	Error     string         `json:"error,omitempty"`
	DocTabs   []snapDoc      `json:"docTabs"`
	ActiveDoc int            `json:"activeDoc"`
}

type windowView struct {
	Port      int        `json:"port"`
	PID       int        `json:"pid"`
	Primary   bool       `json:"primary"`
	Child     bool       `json:"child"` // opened from another window
	Link      string     `json:"link,omitempty"`
	AgeMs     int64      `json:"ageMs"` // since the frontend last reported
	Maximised bool       `json:"mapMaximised,omitempty"`
	Cursor    string     `json:"statusCursor,omitempty"` // the map's status-bar text (world position under the cursor)
	Panes     []paneView `json:"panes"`
	Error     string     `json:"error,omitempty"`
}

func (a *App) currentView() windowView {
	m := a.mcpInit()
	w := windowView{Port: m.port, PID: os.Getpid(), Primary: m.srv.Primary(), Child: a.child, Link: a.StartupLink(), Panes: []paneView{}}
	s, ok := a.snapshot()
	if !ok {
		w.Error = "the window hasn't reported its view yet"
		return w
	}
	m.vmu.Lock()
	w.AgeMs = time.Since(m.viewAt).Milliseconds()
	m.vmu.Unlock()
	w.Maximised, w.Cursor = s.MapMax != 0, s.MapCursor
	for _, p := range s.Panes {
		w.Panes = append(w.Panes, a.paneView(p, s))
	}
	return w
}

func (a *App) paneView(p snapPane, s snapshot) paneView {
	pv := paneView{Pane: map[bool]string{true: "main", false: "split"}[p.ID == 1], Focused: p.ID == s.Focus, Tab: p.Tab,
		Error: p.Error, DocTabs: p.Docs, ActiveDoc: p.Active}
	if pv.DocTabs == nil {
		pv.DocTabs = []snapDoc{}
	}
	if p.Resource == nil {
		return pv
	}
	c, err := a.catalog()
	if err != nil {
		pv.Error = err.Error()
		return pv
	}
	g, _ := catalog.ParseGUID(p.Resource.GUID)
	pv.Resource = a.brief(c, g)
	if raw, ok := p.Extra[p.Tab]; ok {
		var v any
		_ = json.Unmarshal(raw, &v)
		pv.View = v
	}
	pv.Selection = a.selection(c, g, p)
	return pv
}

// selection explains the pane's selection for its tab: the map placement, the graph node, the tree instance / field,
// the texture mip, the dependency graph's node.
func (a *App) selection(c *catalog.Catalog, g uint64, p snapPane) map[string]any {
	sel := map[string]any{}
	extra := func(k string) map[string]any {
		var m map[string]any
		_ = json.Unmarshal(p.Extra[k], &m)
		return m
	}
	switch p.Tab {
	case "map":
		rec := p.Instance
		if e := extra("map"); e != nil {
			if r, ok := e["record"].(float64); ok {
				rec = int(r)
			}
		}
		if rec >= 0 {
			if pl, err := a.placementBrief(fmt.Sprintf("%016x", g), rec); err == nil {
				sel["placement"] = pl
			} else {
				sel["placement"] = map[string]any{"record": rec, "error": err.Error()}
			}
		}
	case "graph":
		if p.GraphNode >= 0 {
			_ = c.WithDoc(g, func(d *stu.Document) error {
				gr, err := view.BuildGraph(d, c)
				if err == nil && p.GraphNode < len(gr.Nodes) {
					sel["node"] = graphNodeOut(gr, p.GraphNode, true)
				}
				return nil
			})
		}
	case "tree", "entity":
		if p.Instance >= 0 {
			_ = c.WithDoc(g, func(d *stu.Document) error {
				if p.Instance >= d.NumInstances() {
					return nil
				}
				doc := view.Describe(d, c)
				for _, in := range doc.Instances {
					if in.Index == p.Instance {
						sel["instance"] = map[string]any{"index": in.Index, "type": in.Type, "summary": in.Summary, "root": in.Root}
					}
				}
				if p.Path != "" && p.Tab == "tree" {
					if n := findPath(view.Tree(d, p.Instance, c), p.Path); n != nil {
						sel["field"] = trimNode(*n, 1, 20)
					}
				}
				return nil
			})
		}
	case "texture":
		if e := extra("texture"); e != nil {
			sel["mip"], sel["surface"] = e["mip"], e["surface"]
			if tv, err := a.Texture(fmt.Sprintf("%016x", g)); err == nil {
				if m, ok := e["mip"].(float64); ok && int(m) < len(tv.Mips) {
					sel["mipSize"] = fmt.Sprintf("%d×%d", tv.Mips[int(m)].Width, tv.Mips[int(m)].Height)
				}
			}
		}
	case "model":
		if e := extra("model"); e != nil {
			a.modelSelection(e, sel)
		}
	case "deps":
		if e := extra("deps"); e != nil {
			if s, ok := e["selected"].(string); ok && s != "" {
				if sg, ok := catalog.ParseGUID(s); ok {
					sel["node"] = a.brief(c, sg)
				}
			}
		}
	}
	if len(sel) == 0 {
		return nil
	}
	return sel
}

func findPath(n view.Node, path string) *view.Node {
	if n.Path == path {
		return &n
	}
	for _, ch := range n.Children {
		if strings.HasPrefix(path, ch.Path) || ch.Path == path {
			if r := findPath(ch, path); r != nil {
				return r
			}
		}
	}
	for _, ch := range n.Children { // paths are not always prefixes of their children's (map keys)
		if r := findPath(ch, path); r != nil {
			return r
		}
	}
	return nil
}

// ---- drive tools ----------------------------------------------------------------------------------

type driveCmd struct {
	Op       string `json:"op"` // open, select
	GUID     string `json:"guid,omitempty"`
	Tab      string `json:"tab,omitempty"`
	Where    string `json:"where,omitempty"` // current, tab, background, split
	Pane     string `json:"pane,omitempty"`  // main, split, focused
	Instance *int   `json:"instance,omitempty"`
	Path     string `json:"path,omitempty"`
	Node     *int   `json:"graphNode,omitempty"` // position in the graph view's node list
}

var tabs = map[string]bool{"info": true, "map": true, "texture": true, "model": true, "material": true, "tree": true, "graph": true, "entity": true,
	"deps": true, "refs": true, "hex": true, "text": true}

type openArgs struct {
	GUID   string `json:"guid"`
	Tab    string `json:"tab"`
	Where  string `json:"where"`
	Select *int   `json:"select"`
	Window int    `json:"window"`
}

func (a *App) toolOpen(ctx context.Context, raw json.RawMessage) (any, error) {
	var q openArgs
	if err := mcp.Args(raw, &q); err != nil {
		return nil, err
	}
	if res, fwd, err := a.forward(ctx, q.Window, "open", raw); fwd {
		return res, err
	}
	c, err := a.catalog()
	if err != nil {
		return nil, err
	}
	g, ok := catalog.ParseGUID(q.GUID)
	if !ok || !c.Exists(g) {
		return nil, fmt.Errorf("not a resource of this install: %q", q.GUID)
	}
	guid := fmt.Sprintf("%016x", g)
	if q.Tab != "" && !tabs[q.Tab] {
		return nil, fmt.Errorf("unknown tab %q (info map texture tree graph entity deps refs hex text)", q.Tab)
	}
	switch q.Where {
	case "":
		q.Where = "current"
	case "current", "tab", "background", "split", "window":
	default:
		return nil, fmt.Errorf("where: current, tab, background, split or window")
	}
	if q.Where == "window" {
		link := "ow://" + guid
		if q.Tab != "" {
			link += "/" + q.Tab
			if q.Select != nil {
				link += fmt.Sprintf("/%d", *q.Select)
			}
		}
		if err := a.openWindow(link); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "opened": link, "note": "a new window (process) opens in about a second; its server takes the next free port (status lists it)"}, nil
	}
	m := a.mcpInit()
	m.vmu.Lock()
	seq := m.seq
	m.vmu.Unlock()
	m.emit("mcp:drive", driveCmd{Op: "open", GUID: guid, Tab: q.Tab, Where: q.Where, Instance: q.Select})
	if q.Where == "background" {
		return map[string]any{"ok": true, "note": "opened in a background tab"}, nil
	}
	s, ok := a.waitView(seq, 4*time.Second, func(s snapshot) bool {
		for _, p := range s.Panes {
			if p.Resource == nil || p.Resource.GUID != guid || (q.Tab != "" && p.Tab != q.Tab) {
				continue
			}
			if q.Select == nil {
				return true
			}
			switch p.Tab { // the selection has landed too
			case "graph":
				return p.GraphNode >= 0
			case "map":
				var m struct {
					Record int `json:"record"`
				}
				return json.Unmarshal(p.Extra["map"], &m) == nil && m.Record == *q.Select
			default:
				return p.Instance == *q.Select
			}
		}
		return false
	})
	return a.driveResult(s, ok, func(p snapPane) bool { return p.Resource != nil && p.Resource.GUID == guid })
}

type selectArgs struct {
	Pane     string `json:"pane"`
	Tab      string `json:"tab"`
	Record   *int   `json:"record"`
	Instance *int   `json:"instance"`
	Path     string `json:"path"`
	Node     *int   `json:"node"`
	Mip      *int   `json:"mip"`
	Submesh  *int   `json:"submesh"`
	Window   int    `json:"window"`
}

func (a *App) toolSelect(ctx context.Context, raw json.RawMessage) (any, error) {
	var q selectArgs
	if err := mcp.Args(raw, &q); err != nil {
		return nil, err
	}
	if res, fwd, err := a.forward(ctx, q.Window, "select", raw); fwd {
		return res, err
	}
	if q.Tab != "" && !tabs[q.Tab] {
		return nil, fmt.Errorf("unknown tab %q", q.Tab)
	}
	s, ok := a.snapshot()
	if !ok {
		return nil, fmt.Errorf("the window hasn't reported its view yet")
	}
	if q.Pane == "" {
		q.Pane = "focused"
	}
	var target *snapPane
	for i, p := range s.Panes {
		if (q.Pane == "main" && p.ID == 1) || (q.Pane == "split" && p.ID != 1) || (q.Pane == "focused" && p.ID == s.Focus) {
			target = &s.Panes[i]
		}
	}
	if target == nil || target.Resource == nil {
		return nil, fmt.Errorf("pane %q has no open resource", q.Pane)
	}
	cmd := driveCmd{Op: "select", Pane: q.Pane, Tab: q.Tab, Path: q.Path}
	n := 0
	if q.Submesh != nil && cmd.Tab == "" {
		cmd.Tab = "model"
	}
	for _, v := range []*int{q.Record, q.Instance, q.Mip, q.Submesh} {
		if v != nil {
			cmd.Instance = v
			n++
		}
	}
	if n > 1 {
		return nil, fmt.Errorf("give one of record (map), instance (tree / entity), mip (texture) or submesh (model)")
	}
	if q.Node != nil { // m_nodes index → the graph view's node position
		c, err := a.catalog()
		if err != nil {
			return nil, err
		}
		g, _ := catalog.ParseGUID(target.Resource.GUID)
		pos := -1
		err = c.WithDoc(g, func(d *stu.Document) error {
			gr, err := view.BuildGraph(d, c)
			if err != nil {
				return err
			}
			for i, nd := range gr.Nodes {
				if nd.Index == *q.Node {
					pos = i
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("node: the pane's resource is not a graph (%v)", err)
		}
		if pos < 0 {
			return nil, fmt.Errorf("graph %s has no node %d", target.Resource.GUID, *q.Node)
		}
		cmd.Node = &pos
		if cmd.Tab == "" {
			cmd.Tab = "graph"
		}
	}
	if q.Path != "" && cmd.Tab == "" {
		cmd.Tab = "tree"
	}
	m := a.mcpInit()
	m.vmu.Lock()
	seq := m.seq
	m.vmu.Unlock()
	m.emit("mcp:drive", cmd)
	id := target.ID
	s, ok = a.waitView(seq, 3*time.Second, func(s snapshot) bool {
		for _, p := range s.Panes {
			if p.ID != id {
				continue
			}
			rec := p.Instance
			if e, ok := p.Extra["map"]; ok && p.Tab == "map" {
				var m struct {
					Record int `json:"record"`
				}
				if json.Unmarshal(e, &m) == nil {
					rec = m.Record
				}
			}
			return (cmd.Tab == "" || p.Tab == cmd.Tab) && (cmd.Instance == nil || rec == *cmd.Instance) &&
				(cmd.Node == nil || p.GraphNode == *cmd.Node) && (cmd.Path == "" || p.Path == cmd.Path)
		}
		return false
	})
	return a.driveResult(s, ok, func(p snapPane) bool { return p.ID == id })
}

func (a *App) driveResult(s snapshot, ok bool, which func(snapPane) bool) (any, error) {
	out := map[string]any{"ok": ok}
	if !ok {
		out["note"] = "the view didn't confirm the change within the timeout (still loading?); call current_view"
	}
	for _, p := range s.Panes {
		if which(p) {
			out["pane"] = a.paneView(p, s)
		}
	}
	return out, nil
}

// forward runs a tool on another window (window = its port); fwd = false: this window.
func (a *App) forward(ctx context.Context, window int, name string, raw json.RawMessage) (any, bool, error) {
	if window == 0 || window == a.mcpInit().port || contains(a.mcp.srv.Ports(), window) {
		return nil, false, nil
	}
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	delete(args, "window")
	res, err := mcp.CallPeer(ctx, window, name, args)
	return res, true, err
}

func contains(xs []int, x int) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
