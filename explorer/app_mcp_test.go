package main

// Tests of the MCP tools on the real install (skipped without one): every tool through the JSON-RPC transport, with a
// fake frontend that reports view snapshots and answers drive commands.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	stdpng "image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"owexplorer/internal/names"
	"owexplorer/internal/texture"
)

const (
	tAnubisMap = "080000000000005b" // Temple of Anubis map data (docs/Explorer.md)
	tDoorRec   = 1021               // the ship door (placed 0x1d7)
	tBrain     = "0400000000000039" // gamemode brain entity definition (docs/MatchLogic.md)
	tTexture   = "0c0000000000007e"
)

type mcpHarness struct {
	t     *testing.T
	a     *App
	url   string
	mu    sync.Mutex
	snap  map[string]any // the fake frontend's state
	cmds  []driveCmd
	calls map[string]int
}

func newHarness(t *testing.T) *mcpHarness {
	repo := names.FindRepo(".")
	install := os.Getenv("OW_INSTALL")
	if install == "" {
		install = filepath.Join(repo, "game", "owbetaclient")
	}
	if _, err := os.Stat(filepath.Join(install, ".build.info")); err != nil {
		t.Skip("no install at", install)
	}
	a := &App{repo: repo, child: true, install: install} // child: never writes the remembered install
	a.loadState()
	if st := a.OpenInstall(install); !st.Open {
		t.Fatalf("open: %+v", st)
	}
	deadline := time.Now().Add(120 * time.Second)
	for {
		c, _ := a.catalog()
		if p := c.Progress(); p.Phase == "done" {
			break
		} else if p.Phase == "failed" || time.Now().After(deadline) {
			t.Fatalf("index: %+v", p)
		}
		time.Sleep(50 * time.Millisecond)
	}
	h := &mcpHarness{t: t, a: a, calls: map[string]int{}}
	m := a.mcpInit()
	m.emit = func(name string, data any) {
		if cmd, ok := data.(driveCmd); ok && name == "mcp:drive" {
			h.mu.Lock()
			h.cmds = append(h.cmds, cmd)
			h.mu.Unlock()
			go h.frontend(cmd)
		}
	}
	ts := httptest.NewServer(m.srv.Handler())
	t.Cleanup(func() { ts.Close(); a.shutdown(nil) })
	h.url = ts.URL + "/mcp"
	return h
}

// view sets the fake frontend's snapshot and reports it (SetView).
func (h *mcpHarness) view(panes ...map[string]any) {
	h.mu.Lock()
	h.snap = map[string]any{"focus": 1, "mapCursor": "x 1.00  z 2.00", "panes": panes}
	b, _ := json.Marshal(h.snap)
	h.mu.Unlock()
	h.a.SetView(string(b))
}

func pane(id int, guid string, typ int, tab string, extra map[string]any) map[string]any {
	p := map[string]any{"id": id, "current": guid, "tab": tab, "instance": -1, "path": "", "graphNode": -1,
		"docs": []map[string]any{{"guid": guid, "tab": tab, "label": "x"}}, "active": 0, "extra": extra}
	if guid != "" {
		p["resource"] = map[string]any{"guid": guid, "type": typ, "typeLabel": "", "kind": "stu"}
	}
	return p
}

// frontend applies a drive command the way app.vue does and reports the result.
func (h *mcpHarness) frontend(cmd driveCmd) {
	time.Sleep(20 * time.Millisecond)
	h.mu.Lock()
	panes := h.snap["panes"].([]map[string]any)
	switch cmd.Op {
	case "open":
		p := panes[0]
		if cmd.Where == "split" {
			p = pane(2, "", 0, "", map[string]any{})
			panes = append(panes, p)
		}
		p["current"], p["resource"] = cmd.GUID, map[string]any{"guid": cmd.GUID, "kind": "stu"}
		p["tab"] = map[bool]string{true: cmd.Tab, false: "info"}[cmd.Tab != ""]
		if cmd.Instance != nil {
			p["instance"] = *cmd.Instance
			if p["tab"] == "map" {
				p["extra"] = map[string]any{"map": map[string]any{"record": *cmd.Instance, "mode": "2d"}}
			}
		}
	case "select":
		p := panes[0]
		if cmd.Tab != "" {
			p["tab"] = cmd.Tab
		}
		if cmd.Instance != nil {
			p["instance"] = *cmd.Instance
			if p["tab"] == "map" {
				p["extra"] = map[string]any{"map": map[string]any{"record": *cmd.Instance}}
			}
		}
		if cmd.Node != nil {
			p["graphNode"] = *cmd.Node
		}
		if cmd.Path != "" {
			p["path"] = cmd.Path
		}
	}
	h.mu.Unlock()
	h.view(panes...)
}

// call runs a tool through JSON-RPC; the decoded result, or the error text.
func (h *mcpHarness) call(name string, args any) (map[string]any, string) {
	h.t.Helper()
	h.calls[name]++
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args}})
	req, _ := http.NewRequest(http.MethodPost, h.url, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer r.Body.Close()
	var resp struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil || resp.Error != nil {
		h.t.Fatalf("%s: rpc %v %v", name, err, resp.Error)
	}
	text := resp.Result.Content[0].Text
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		h.t.Fatalf("%s: result not an object: %s", name, text[:min(len(text), 300)])
	}
	if resp.Result.IsError {
		return nil, out["error"].(string)
	}
	return out, ""
}

func (h *mcpHarness) ok(name string, args any) map[string]any {
	h.t.Helper()
	out, e := h.call(name, args)
	if e != "" {
		h.t.Fatalf("%s %v: %s", name, args, e)
	}
	return out
}

func (h *mcpHarness) fails(name string, args any, want string) {
	h.t.Helper()
	_, e := h.call(name, args)
	if !strings.Contains(e, want) {
		h.t.Fatalf("%s %v: error %q, want %q", name, args, e, want)
	}
}

// get walks a decoded JSON value: keys and indices.
func get(v any, path ...any) any {
	for _, k := range path {
		switch x := v.(type) {
		case map[string]any:
			v = x[k.(string)]
		case []any:
			i := k.(int)
			if i >= len(x) {
				return nil
			}
			v = x[i]
		default:
			return nil
		}
	}
	return v
}

func js(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestMCPTools(t *testing.T) {
	h := newHarness(t)

	// status
	st := h.ok("status", map[string]any{})
	if get(st, "resources").(float64) < 100000 || get(st, "index", "phase") != "done" || get(st, "driveEnabled") != false {
		t.Fatalf("status: %s", js(st))
	}

	// describe: entity definition, graph, map, texture, string, package, not a resource
	d := h.ok("describe", map[string]any{"guid": "0x" + tBrain})
	comps := get(d, "summary", "components").([]any)
	var graph string
	for _, c := range comps {
		if gs, ok := get(c, "graphs").([]any); ok && len(gs) > 0 {
			graph = get(gs[0], "guid").(string)
		}
	}
	if len(comps) == 0 || graph == "" || get(d, "refsIn", "total").(float64) == 0 {
		t.Fatalf("describe entity: %s", js(d))
	}
	d = h.ok("describe", map[string]any{"guid": graph})
	if get(d, "summary", "nodes").(float64) == 0 || get(d, "summary", "entries") == nil {
		t.Fatalf("describe graph: %s", js(d))
	}
	d = h.ok("describe", map[string]any{"guid": "ow://" + tAnubisMap})
	if get(d, "summary", "classes", "networked").(float64) != 22 || len(get(d, "summary", "networkedEntities").([]any)) != 22 {
		t.Fatalf("describe map: %s", js(get(d, "summary", "classes")))
	}
	d = h.ok("describe", map[string]any{"guid": tTexture})
	if !strings.Contains(get(d, "summary", "size").(string), "×") || get(d, "summary", "mips").(float64) < 1 {
		t.Fatalf("describe texture: %s", js(d))
	}
	s := h.ok("search", map[string]any{"type": "0x07C", "limit": 1})
	str := get(s, "rows", 0, "guid").(string)
	if d = h.ok("describe", map[string]any{"guid": str}); get(d, "summary", "text") == nil {
		t.Fatalf("describe string: %s", js(d))
	}
	s = h.ok("search", map[string]any{"type": 0x077, "limit": 1})
	if d = h.ok("describe", map[string]any{"guid": get(s, "rows", 0, "guid")}); get(d, "summary", "records") == nil {
		t.Fatalf("describe package: %s", js(d))
	}
	if d = h.ok("describe", map[string]any{"guid": "0xdeadbeef"}); get(d, "resource", "notAResource") != true {
		t.Fatalf("describe missing: %s", js(d))
	}
	h.fails("describe", map[string]any{"guid": "xyz"}, "not a GUID")
	h.fails("describe", map[string]any{"gid": tBrain}, "unknown field")

	// search
	s = h.ok("search", map[string]any{"text": "anubis"})
	if get(s, "total").(float64) == 0 {
		t.Fatalf("search anubis: %s", js(s))
	}
	h.fails("search", map[string]any{}, "give text")

	// refs: the brain is placed by maps (in), uses its graphs (out); depth 2; type filter as hex
	r := h.ok("refs", map[string]any{"guid": tBrain, "direction": "both", "limit": 10})
	if get(r, "in", "total").(float64) == 0 || get(r, "out", "total").(float64) == 0 || len(get(r, "out", "rows").([]any)) > 10 {
		t.Fatalf("refs: %s", js(r))
	}
	r = h.ok("refs", map[string]any{"guid": tBrain, "types": []any{"0x1B"}})
	for _, row := range get(r, "out", "rows").([]any) {
		if !strings.HasPrefix(get(row, "guid").(string), "") || get(row, "type").(float64) != 0x1B {
			t.Fatalf("type filter: %s", js(row))
		}
	}
	r = h.ok("refs", map[string]any{"guid": tBrain, "direction": "in", "depth": 2, "limit": 40})
	if n := len(get(r, "in", "edges").([]any)); n == 0 || n > 40 {
		t.Fatalf("refs depth 2: %d edges", n)
	}
	h.fails("refs", map[string]any{"guid": tBrain, "direction": "up"}, "direction")

	// entity
	e := h.ok("entity", map[string]any{"guid": tBrain, "refs": 5})
	if len(get(e, "refs").([]any)) > 5 || get(e, "components", 0, "type") == nil {
		t.Fatalf("entity: %s", js(e))
	}
	found := false
	for _, c := range get(e, "components").([]any) {
		for _, g := range asList(get(c, "graphs")) {
			if get(g, "summary", "nodes") != nil {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("entity graphs without counts: %s", js(e))
	}
	h.fails("entity", map[string]any{"guid": graph}, "not an entity definition")

	// statescript_graph: summary, nodes (paged), full, one node
	g := h.ok("statescript_graph", map[string]any{"guid": graph, "detail": "summary"})
	if get(g, "nodeList") != nil || get(g, "entryNodes") == nil {
		t.Fatalf("graph summary: %s", js(g))
	}
	g = h.ok("statescript_graph", map[string]any{"guid": graph, "limit": 3})
	nl := get(g, "nodeList").([]any)
	if len(nl) != 3 || get(nl[0], "kind") == nil {
		t.Fatalf("graph nodes: %s", js(g))
	}
	g = h.ok("statescript_graph", map[string]any{"guid": graph, "detail": "full", "limit": 50})
	withParams := false
	for _, n := range get(g, "nodeList").([]any) {
		if _, ok := get(n, "params").([]any); ok {
			withParams = true
		}
	}
	if !withParams {
		t.Fatalf("graph full: no params in the first 50 nodes")
	}
	node := int(get(nl[1], "index").(float64))
	g = h.ok("statescript_graph", map[string]any{"guid": graph, "node": node})
	if int(get(g, "node", "index").(float64)) != node {
		t.Fatalf("graph node: %s", js(g))
	}
	h.fails("statescript_graph", map[string]any{"guid": graph, "node": 99999}, "no node")
	h.fails("statescript_graph", map[string]any{"guid": tBrain}, "not a Statescript graph")

	// stu_tree: instance table, a subtree, a path inside it
	tr := h.ok("stu_tree", map[string]any{"guid": tBrain})
	root := int(get(tr, "root").(float64))
	if get(tr, "instanceCount").(float64) == 0 {
		t.Fatalf("tree table: %s", js(tr))
	}
	tr = h.ok("stu_tree", map[string]any{"guid": tBrain, "instance": root, "depth": 1})
	kids := get(tr, "tree", "children").([]any)
	if len(kids) == 0 {
		t.Fatalf("tree: %s", js(tr))
	}
	path := get(kids[0], "path").(string)
	tr = h.ok("stu_tree", map[string]any{"guid": tBrain, "path": path})
	if get(tr, "tree", "path") != path {
		t.Fatalf("tree path: %s", js(tr))
	}
	h.fails("stu_tree", map[string]any{"guid": tBrain, "instance": 9999}, "out of range")

	// map_placements, placement
	mp := h.ok("map_placements", map[string]any{"map": tAnubisMap, "class": "networked"})
	if get(mp, "total").(float64) != 22 {
		t.Fatalf("placements networked: %s", js(mp))
	}
	door := h.ok("placement", map[string]any{"map": tAnubisMap, "index": tDoorRec})
	pos := get(door, "placement", "pos").([]any)
	if get(door, "placement", "placed") != "0x1d7" || get(door, "fields") == nil {
		t.Fatalf("placement: %s", js(get(door, "placement")))
	}
	mp = h.ok("map_placements", map[string]any{"map": tAnubisMap, "near": []any{pos[0], pos[1], pos[2], 0.5}, "limit": 5})
	if int(get(mp, "rows", 0, "i").(float64)) != tDoorRec {
		t.Fatalf("near: %s", js(mp))
	}
	h.fails("map_placements", map[string]any{"map": tAnubisMap, "near": []any{1, 2}}, "near")
	h.fails("placement", map[string]any{"map": tAnubisMap, "index": 999999}, "no record")

	// current_view: nothing reported yet, then a map with the door selected + a graph node in the split pane
	cv := h.ok("current_view", map[string]any{})
	if get(cv, "windows", 0, "error") == nil {
		t.Fatalf("current_view before a snapshot: %s", js(cv))
	}
	h.view(pane(1, tAnubisMap, 2, "map", map[string]any{"map": map[string]any{"record": tDoorRec, "mode": "3d",
		"camera": map[string]any{"mode": "orbit", "pos": []float64{1, 2, 3}}}}),
		func() map[string]any {
			p := pane(2, graph, 0x1B, "graph", map[string]any{})
			p["graphNode"] = 1
			return p
		}())
	cv = h.ok("current_view", map[string]any{})
	w := get(cv, "windows", 0)
	if get(w, "panes", 0, "selection", "placement", "placed") != "0x1d7" || get(w, "panes", 0, "view", "mode") != "3d" ||
		get(w, "panes", 1, "selection", "node", "kind") == nil || get(w, "panes", 0, "focused") != true || get(w, "statusCursor") == nil {
		t.Fatalf("current_view: %s", js(cv))
	}
	// tree selection with a path, texture mip, deps node
	h.view(func() map[string]any {
		p := pane(1, tBrain, 3, "tree", nil)
		p["instance"] = root
		p["path"] = path
		return p
	}(),
		pane(2, tTexture, 4, "texture", map[string]any{"texture": map[string]any{"mip": 0, "surface": 0}}))
	w = get(h.ok("current_view", map[string]any{"window": 0}), "windows", 0)
	if get(w, "panes", 0, "selection", "field", "path") != path || get(w, "panes", 1, "selection", "mipSize") == nil {
		t.Fatalf("current_view tree / texture: %s", js(w))
	}
	h.view(pane(1, tBrain, 3, "deps", map[string]any{"deps": map[string]any{"selected": graph}}))
	w = get(h.ok("current_view", map[string]any{}), "windows", 0)
	if get(w, "panes", 0, "selection", "node", "guid") != graph {
		t.Fatalf("current_view deps: %s", js(w))
	}

	// drive tools: off by default, then open + select through the fake frontend
	h.fails("open", map[string]any{"guid": tAnubisMap}, "drive tools are off")
	h.fails("select", map[string]any{"record": 1}, "drive tools are off")
	h.a.SetMcpDrive(true)
	o := h.ok("open", map[string]any{"guid": tAnubisMap, "tab": "map", "select": tDoorRec})
	if get(o, "ok") != true || get(o, "pane", "selection", "placement", "record").(float64) != tDoorRec {
		t.Fatalf("open: %s", js(o))
	}
	o = h.ok("select", map[string]any{"record": 1027})
	if get(o, "ok") != true || get(o, "pane", "selection", "placement", "record").(float64) != 1027 {
		t.Fatalf("select record: %s", js(o))
	}
	h.ok("open", map[string]any{"guid": graph})
	o = h.ok("select", map[string]any{"node": node})
	if get(o, "ok") != true || get(o, "pane", "tab") != "graph" || int(get(o, "pane", "selection", "node", "index").(float64)) != node {
		t.Fatalf("select node: %s", js(o))
	}
	h.ok("open", map[string]any{"guid": tBrain, "where": "split", "tab": "tree"})
	h.fails("open", map[string]any{"guid": "0xdeadbeef"}, "not a resource")
	h.fails("open", map[string]any{"guid": tBrain, "tab": "nope"}, "unknown tab")
	h.fails("select", map[string]any{"record": 1, "mip": 2}, "give one of")
	if len(h.cmds) != 5 {
		t.Fatalf("drive commands sent: %d %+v", len(h.cmds), h.cmds)
	}

	// audio (T065): the voice sound 0x16F3 has 2 WEMs and a bank with one event; a voice WEM as Ogg; describe's summaries
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	ae := h.ok("audio_export", map[string]any{"guid": "0d400000000016f3"})
	if get(ae, "total").(float64) != 2 || get(ae, "files", 0, "rate").(float64) != 48000 {
		t.Fatalf("audio_export: %s", js(ae))
	}
	if b, err := os.ReadFile(get(ae, "files", 1, "path").(string)); err != nil || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		t.Fatalf("audio_export wav: %v", err)
	}
	ae = h.ok("audio_export", map[string]any{"guid": "08d000000000a1c0", "format": "ogg"})
	if b, err := os.ReadFile(get(ae, "files", 0, "path").(string)); err != nil || string(b[:4]) != "OggS" {
		t.Fatalf("audio_export ogg: %v %s", err, js(ae))
	}
	h.fails("audio_export", map[string]any{"guid": tBrain}, "has no WEMs")
	h.fails("audio_export", map[string]any{"guid": "0d400000000016f3", "format": "mp3"}, "wav or ogg")
	ds := h.ok("describe", map[string]any{"guid": "0d400000000016f3"})
	if get(ds, "summary", "bank") != "0420000000002d32" || get(ds, "summary", "events", 0, "children", 0, "typeName") != "Action" {
		t.Fatalf("describe sound: %s", js(get(ds, "summary")))
	}
	ds = h.ok("describe", map[string]any{"guid": "034000000000001d"})
	if get(ds, "summary", "named", "name") != "WEAPON_TAIL" || get(ds, "summary", "named", "hashOk") != true ||
		len(asList(get(ds, "summary", "named", "switches"))) != 2 {
		t.Fatalf("describe switch group: %s", js(get(ds, "summary")))
	}

	// every tool was called
	for _, tl := range h.a.mcp.srv.Tools() {
		if h.calls[tl.Name] == 0 && tl.Name != "reply" && tl.Name != "asks" && tl.Name != "texture_image" && tl.Name != "model_image" && tl.Name != "material_image" { // TestAsk, TestTextureImage, TestModelImage, TestMaterialImage
			t.Errorf("tool %s not tested", tl.Name)
		}
	}
	fmt.Println("tools:", len(h.a.mcp.srv.Tools()), "calls:", h.calls)
}

func asList(v any) []any { l, _ := v.([]any); return l }

// stream subscribes to a window's Ask event stream.
func stream(t *testing.T, port int) (func() map[string]any, func()) {
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/mcp/events", port), nil)
	ctx, cancel := context.WithCancel(context.Background())
	r, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(r.Body)
	lines := make(chan map[string]any, 16)
	go func() {
		for {
			l, err := br.ReadBytes('\n')
			if err != nil {
				close(lines)
				return
			}
			var ev map[string]any
			_ = json.Unmarshal(l, &ev)
			lines <- ev
		}
	}()
	next := func() map[string]any {
		t.Helper()
		select {
		case ev := <-lines:
			return ev
		case <-time.After(3 * time.Second):
			t.Fatal("no event within 3 s")
			return nil
		}
	}
	return next, func() { cancel(); r.Body.Close() }
}

// Ask Claude: a subscriber gets the ask with its context, replies through the reply tool, a new subscriber gets the
// unanswered asks, cancel withdraws; a second window's ask goes through the hub and its reply is forwarded back.
func TestAsk(t *testing.T) {
	h := newHarness(t)
	base := 45000 + os.Getpid()%2000*8
	t.Setenv("OWEXPLORER_MCP", fmt.Sprint(base))
	h.a.startMCP()
	if h.a.mcp.port != base {
		t.Fatalf("hub port %d (want %d: %s)", h.a.mcp.port, base, h.a.mcp.err)
	}
	h.url = fmt.Sprintf("http://127.0.0.1:%d/mcp", base)
	next, stop := stream(t, base)
	if ev := next(); ev["type"] != "subscribed" || ev["pending"].(float64) != 0 {
		t.Fatalf("hello: %v", ev)
	}
	time.Sleep(50 * time.Millisecond)
	if st := h.a.AskState(); st.Listeners != 1 {
		t.Fatalf("listeners: %+v", st)
	}
	rec := tDoorRec
	id, err := h.a.Ask("what is this door?", AskSubject{Kind: "placement", Map: tAnubisMap, Record: &rec}, "")
	if err != nil {
		t.Fatal(err)
	}
	ev := next()
	if ev["type"] != "ask" || ev["id"] != id || ev["question"] != "what is this door?" || ev["context"] != nil || len(js(ev)) > 500 {
		t.Fatalf("ask event (short line, no context): %s", js(ev))
	}
	if p := h.ok("asks", map[string]any{}); len(p["pending"].([]any)) != 1 || get(p, "pending", 0, "context", "placement", "placed") != "0x1d7" {
		t.Fatalf("asks (with context): %s", js(p))
	}
	h.ok("reply", map[string]any{"id": id, "text": "checking…", "final": false})
	h.ok("reply", map[string]any{"id": id, "text": "The ship door (entity 0x040000000000005b)."})
	st := h.a.AskState()
	if len(st.Messages) != 3 || st.Messages[0].State != "answered" || st.Messages[2].Role != "claude" {
		t.Fatalf("conversation: %+v", st.Messages)
	}
	if p := h.ok("asks", map[string]any{}); len(p["pending"].([]any)) != 0 {
		t.Fatalf("answered ask still pending: %s", js(p))
	}
	h.fails("reply", map[string]any{"id": "nope", "text": "x"}, "not an ask id")

	// replay: an ask made while nobody listens reaches the next subscriber; cancel withdraws it
	stop()
	time.Sleep(50 * time.Millisecond)
	node := 16
	id2, _ := h.a.Ask("and this node?", AskSubject{Kind: "graphNode", GUID: "0580000000000042", Node: &node}, id)
	next, stop = stream(t, base)
	defer stop()
	if ev := next(); ev["pending"].(float64) != 1 {
		t.Fatalf("hello after reconnect: %v", ev)
	}
	if ev := next(); ev["id"] != id2 || ev["followUpOf"] != id || get(ev, "subject", "node").(float64) != 16 {
		t.Fatalf("replayed ask: %s", js(ev))
	}
	if p := h.ok("asks", map[string]any{}); get(p, "pending", 0, "context", "node", "index").(float64) != 16 {
		t.Fatalf("asks graph node context: %s", js(p))
	}
	h.a.AskCancel(id2)
	if ev := next(); ev["type"] != "cancel" || ev["id"] != id2 {
		t.Fatalf("cancel: %v", ev)
	}

	// a second window: its ask goes to the hub's stream, the reply comes back to it
	child := &App{child: true}
	child.startMCP()
	defer child.mcp.srv.Close()
	if child.mcp.port != base+1 {
		t.Fatalf("child port %d", child.mcp.port)
	}
	id3, err := child.Ask("what am I looking at?", AskSubject{Kind: "view"}, "")
	if err != nil || !strings.HasPrefix(id3, fmt.Sprint(base+1)+"-") {
		t.Fatalf("child ask: %q %v", id3, err)
	}
	if ev := next(); ev["id"] != id3 {
		t.Fatalf("child ask on the hub: %v", ev)
	}
	if st := child.AskState(); st.Listeners != 1 {
		t.Fatalf("child sees listeners: %+v", st)
	}
	h.ok("reply", map[string]any{"id": id3, "text": "a map"})
	if st := child.AskState(); len(st.Messages) != 2 || st.Messages[0].State != "answered" || st.Messages[1].Text != "a map" {
		t.Fatalf("child conversation: %+v", st.Messages)
	}
}

// texture_image: image content + a saved file, on one texture per format (BC1, BC3, BC5, BC6H, R8), a cube (cross),
// a delta texture and a payload GUID.
func TestTextureImage(t *testing.T) {
	h := newHarness(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	c, _ := h.a.catalog()
	want := map[uint8]string{71: "BC1", 72: "BC1", 77: "BC3", 78: "BC3", 83: "BC5", 95: "BC6H", 61: "R8"}
	found := map[string]string{}
	cube := ""
	for _, r := range c.Store.ResourcesOfType(texture.TypeTexture) {
		b, err := c.Store.Read(r.GUID)
		if err != nil {
			continue
		}
		hd, err := texture.ParseHeader(b)
		if err != nil {
			continue
		}
		g := fmt.Sprintf("%016x", r.GUID)
		if name, ok := want[hd.Format]; ok && found[name] == "" && hd.Flags&texture.FlagDelta == 0 && (hd.SurfaceCount() == 1 || name == "BC6H") { // BC6H: only cube maps
			found[name] = g
		}
		if cube == "" && hd.Flags&texture.FlagCube != 0 && hd.SurfaceCount() >= 6 && texture.LookupFormat(hd.Format) != nil {
			cube = g
		}
		if len(found) == 5 && cube != "" {
			break
		}
	}
	if len(found) != 5 || cube == "" {
		t.Fatalf("formats found: %v, cube %q", found, cube)
	}
	// call returns the image part's PNG and the text part's info
	call := func(args map[string]any) ([]byte, map[string]any, string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "texture_image", "arguments": args}})
		req, _ := http.NewRequest(http.MethodPost, h.url, strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var resp struct {
			Result struct {
				Content []struct{ Type, Text, Data, MimeType string } `json:"content"`
				IsError bool                                          `json:"isError"`
			} `json:"result"`
		}
		_ = json.NewDecoder(r.Body).Decode(&resp)
		cs := resp.Result.Content
		if resp.Result.IsError {
			return nil, nil, cs[0].Text
		}
		if len(cs) != 2 || cs[0].Type != "image" || cs[0].MimeType != "image/png" || cs[1].Type != "text" {
			t.Fatalf("content parts: %+v", cs)
		}
		png, _ := base64.StdEncoding.DecodeString(cs[0].Data)
		var info map[string]any
		_ = json.Unmarshal([]byte(cs[1].Text), &info)
		return png, info, ""
	}
	size := func(png []byte) (int, int) {
		cfg, err := stdpng.DecodeConfig(bytes.NewReader(png))
		if err != nil {
			t.Fatal(err)
		}
		return cfg.Width, cfg.Height
	}
	for name, g := range found {
		png, info, e := call(map[string]any{"guid": g})
		if e != "" {
			t.Fatalf("%s %s: %s", name, g, e)
		}
		w, hh := size(png)
		if max(w, hh) > 512 || fmt.Sprintf("%d×%d", w, hh) != info["image"] {
			t.Fatalf("%s: %dx%d vs %v", name, w, hh, info)
		}
		saved, err := os.ReadFile(info["path"].(string))
		if err != nil || !bytes.Equal(saved, png) {
			t.Fatalf("%s: saved file %v: %v", name, info["path"], err)
		}
		t.Logf("%s %s: %s mip %v (%v) → %v, %d bytes", name, g, info["format"], info["mip"], info["mipSize"], info["image"], len(png))
	}
	// options: one channel, a small size, an explicit exposure (HDR), no save
	png, info, _ := call(map[string]any{"guid": found["BC3"], "channels": "a", "max_size": 64, "save": false})
	if w, hh := size(png); max(w, hh) > 64 || info["path"] != nil || info["channels"] != "a" {
		t.Fatalf("options: %dx%d %v", w, hh, info)
	}
	if _, info, e := call(map[string]any{"guid": found["BC6H"], "exposure": -1}); e != "" || info["exposure"].(float64) != -1 {
		t.Fatalf("exposure: %v %s", info, e)
	}
	// cube: the cross is 4:3
	png, info, e := call(map[string]any{"guid": cube, "cross": true, "max_size": 256})
	if w, hh := size(png); e != "" || w*3 != hh*4 {
		t.Fatalf("cross: %dx%d %v %s", w, hh, info, e)
	}
	// delta texture (composed with its base), and a payload GUID shows its texture
	if _, info, e := call(map[string]any{"guid": "0C00000000002E66"}); e != "" || info["deltaOf"] == nil {
		t.Fatalf("delta: %v %s", info, e)
	}
	if _, info, e := call(map[string]any{"guid": "032000010000007E", "mip": 0}); e != "" || info["texture"] != "0c0000000000007e" || info["payload"] == nil {
		t.Fatalf("payload: %v %s", info, e)
	}
	for _, bad := range []map[string]any{{"guid": tBrain}, {"guid": found["BC1"], "mip": 99}, {"guid": found["BC1"], "cross": true},
		{"guid": found["BC1"], "channels": "x"}, {"guid": found["BC1"], "surface": 3}} {
		if _, _, e := call(bad); e == "" {
			t.Fatalf("accepted %v", bad)
		}
	}
}

// model_image: the D.Va mech (model 11CE) with the look of its model component; a look GUID renders its model;
// a submesh highlight; errors for other types.
func TestModelImage(t *testing.T) {
	h := newHarness(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	call := func(args map[string]any) ([]byte, map[string]any, string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "model_image", "arguments": args}})
		req, _ := http.NewRequest(http.MethodPost, h.url, strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var resp struct {
			Result struct {
				Content []struct{ Type, Text, Data, MimeType string } `json:"content"`
				IsError bool                                          `json:"isError"`
			} `json:"result"`
		}
		_ = json.NewDecoder(r.Body).Decode(&resp)
		cs := resp.Result.Content
		if resp.Result.IsError {
			return nil, nil, cs[0].Text
		}
		if len(cs) != 2 || cs[0].Type != "image" || cs[1].Type != "text" {
			t.Fatalf("content parts: %+v", cs)
		}
		png, _ := base64.StdEncoding.DecodeString(cs[0].Data)
		var info map[string]any
		_ = json.Unmarshal([]byte(cs[1].Text), &info)
		return png, info, ""
	}
	png, info, e := call(map[string]any{"guid": "0d000000000011ce", "size": 256})
	if e != "" {
		t.Fatal(e)
	}
	cfg, err := stdpng.DecodeConfig(bytes.NewReader(png))
	if err != nil || cfg.Width != 256 || info["look"] != "09800000000014b0" || info["triangles"].(float64) != 47160 ||
		info["texturedGroups"].(float64) < 10 {
		t.Fatalf("%v %+v", err, info)
	}
	if b, err := os.ReadFile(info["path"].(string)); err != nil || !bytes.Equal(b, png) {
		t.Errorf("saved file differs: %v", err)
	}
	if _, info, e = call(map[string]any{"guid": "09800000000014b0", "lod": 8, "submesh": 3, "save": false}); e != "" ||
		info["model"] != "0d000000000011ce" || info["lod"].(float64) != 8 || info["path"] != nil {
		t.Errorf("look → model: %s %+v", e, info)
	}
	if _, _, e = call(map[string]any{"guid": "0c00000000004f13"}); !strings.Contains(e, "not a model") {
		t.Errorf("texture: %q", e)
	}
}

// TestMapVisual: every map's placed models resolve (MapVisual, the 3D map mode's visual geometry, T064).
func TestMapVisual(t *testing.T) {
	h := newHarness(t)
	c, _ := h.a.catalog()
	total := 0
	for _, r := range c.Store.ResourcesOfType(0x002) {
		v, err := h.a.MapVisual(fmt.Sprintf("%016x", r.GUID))
		if err != nil {
			t.Errorf("%016x: %v", r.GUID, err)
			continue
		}
		looks := 0
		for _, b := range v.Batches {
			if b.Look != "" {
				looks++
			}
			if len(b.Instances) != 10*len(b.Records) {
				t.Errorf("%016x %s: %d floats for %d records", r.GUID, b.Model, len(b.Instances), len(b.Records))
			}
		}
		t.Logf("%016x %-22s %4d batches (%4d with a look), %5d instances, %9d triangles, %d failed", r.GUID,
			c.Name(r.GUID).Text, len(v.Batches), looks, v.Instances, v.Triangles, v.Failed)
		if v.Failed > 0 {
			t.Errorf("%016x: %d batches failed", r.GUID, v.Failed)
		}
		total += v.Instances
	}
	if total == 0 {
		t.Error("no instances")
	}
}
