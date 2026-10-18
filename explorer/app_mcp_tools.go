package main

// Read tools of the MCP server (app_mcp.go, docs/Explorer.md "MCP server"). Results are small JSON: lists are paged
// (offset / limit), trees cut at a depth, and every GUID is a 16-digit hex string with its name where one is known.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"owcontent/content"
	"owexplorer/internal/catalog"
	"owexplorer/internal/mapview"
	"owexplorer/internal/mcp"
	"owexplorer/internal/view"
	"owstu/stu"
)

// ---- schemas --------------------------------------------------------------------------------------

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
func pStr(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func pInt(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
func pEnum(desc string, vals ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": vals}
}
func pTypeID(desc string) map[string]any {
	return map[string]any{"type": []string{"integer", "string"}, "description": desc + ` (a number, or hex like "0x01B")`}
}

var (
	pGUID   = pStr(`resource GUID: 16 hex digits, "0x…", "ow://…" or a short hex form`)
	pOffset = pInt("first row (paging), default 0")
	pWindow = pInt("the window's MCP port (current_view / status list them); default: this window")
)

// typeID is a resource / record type given as a number or a hex string ("0x01B", "1b").
type typeID int

func (t *typeID) UnmarshalJSON(b []byte) error {
	var n int
	if json.Unmarshal(b, &n) == nil {
		*t = typeID(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("type: a number or a hex string")
	}
	v, err := strconv.ParseInt(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "0x"), 16, 32)
	if err != nil {
		return fmt.Errorf("type %q: not hex", s)
	}
	*t = typeID(v)
	return nil
}

func tool(name, desc string, schema map[string]any, h func(ctx context.Context, raw json.RawMessage) (any, error)) mcp.Tool {
	return mcp.Tool{Name: name, Description: desc, Schema: schema, Handler: h}
}

// typed wraps a handler with strict argument decoding.
func typed[T any](f func(ctx context.Context, q T) (any, error)) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var q T
		if err := mcp.Args(raw, &q); err != nil {
			return nil, err
		}
		return f(ctx, q)
	}
}

func (a *App) mcpTools() []mcp.Tool {
	ts := []mcp.Tool{
		tool("status", "The explorer: install, reference index state, this window (port, primary), the other explorer windows "+
			"and whether drive tools are enabled.", obj(map[string]any{}), typed(a.toolStatus)),
		tool("current_view", "What the user is looking at, per window and pane: the open resource (GUID, type, name), the view "+
			"tab, the selection explained (map placement with its definition, Statescript graph node, STU tree instance / "+
			"field, texture mip, dependency-graph node), the tab's own state (map 2D/3D, 3D camera and the point under the "+
			"cursor, texture display), the document tabs. Start here.",
			obj(map[string]any{"window": map[string]any{"type": "integer", "description": "one window's MCP port; default: every window"}}),
			typed(a.toolCurrentView)),
		tool("describe", "What a resource is: name, type, size, packages, reference counts by kind and type, and a summary "+
			"for its kind (entity definition: components + Statescript graphs; graph: entries, node counts, config vars; "+
			"map: mode, placements by class; texture: format, size, mips; other STU: root type + top-level fields; string: text).",
			obj(map[string]any{"guid": pGUID}, "guid"), typed(a.toolDescribe)),
		tool("refs", "The reference index around a resource: what it uses (out), what uses it (in). depth > 1 walks further "+
			"(breadth-first, capped). Filters: edge kinds (stu.field, map.placement, package.member, …), resource types.",
			obj(map[string]any{"guid": pGUID, "direction": pEnum("default out", "out", "in", "both"),
				"depth":           pInt("1–3, default 1"),
				"kinds":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "edge kinds (default all but package.member unless the resource is a package)"},
				"types":           map[string]any{"type": "array", "items": pTypeID("resource type"), "description": "resource types to keep"},
				"search":          pStr("name or GUID substring"),
				"include_missing": map[string]any{"type": "boolean", "description": "keep ids that are not resources (config-var ids, …); default false"},
				"offset":          pOffset, "limit": pInt("rows (depth 1) or edges (depth > 1), default 50, max 500")}, "guid"),
			typed(a.toolRefs)),
		tool("search", "Find resources by name, note or GUID substring, optionally of one type.",
			obj(map[string]any{"text": pStr("name / note / hex GUID substring"), "type": pTypeID("resource type, e.g. 3 = entity definition, 0x1B = Statescript graph, 0x02 = map data, 0x04 = texture"),
				"offset": pOffset, "limit": pInt("default 50, max 500")}), typed(a.toolSearch)),
		tool("entity", "An entity definition (type 0x003): its components (type, instance), the Statescript graphs each "+
			"component runs (with node / entry / state counts), and the resources it references.",
			obj(map[string]any{"guid": pGUID, "refs": pInt("max references listed, default 100")}, "guid"), typed(a.toolEntity)),
		tool("statescript_graph", "A Statescript graph (type 0x01B). Every node has net = client / both / server (m_clientOnly; "+
			"m_serverOnly or a type without a client class; netWhy says which, netConflict when they disagree). "+
			"detail=summary: counts, entries, states, config vars, "+
			"referenced resources; nodes (default): + every node (index, kind, type, display name, flags, outgoing links); "+
			"full: + each node's parameters (config-var summaries) and plugs. node=<m_nodes index>: that node in full with its "+
			"links in and out. Node type names come from the Statescript catalogue (guessed ones are marked).",
			obj(map[string]any{"guid": pGUID, "detail": pEnum("default nodes", "summary", "nodes", "full"),
				"node": pInt("one node by m_nodes index"), "offset": pOffset, "limit": pInt("nodes, default 200")}, "guid"),
			typed(a.toolGraph)),
		tool("stu_tree", "The decoded STU tree of a resource: without instance, the instance table; with instance (and "+
			"optionally a path from a previous result), that subtree cut at depth.",
			obj(map[string]any{"guid": pGUID, "instance": pInt("instance index (default: the instance table)"),
				"path": pStr("a node path inside the instance (e.g. i2.f1a2b3c4d.[3])"), "depth": pInt("default 3, max 8"),
				"max_children": pInt("per list / map / object, default 40")}, "guid"),
			typed(a.toolTree)),
		tool("map_placements", "A map's placements (map data 0x002 or a map header 0x09F): record index, class (networked, "+
			"mover, gameplay, prop, static, light, …), record type, primary GUID + name, position, yaw. Filters: class, record "+
			"type name, text (name / GUID / override type), a sphere around a point.",
			obj(map[string]any{"map": pGUID, "class": pStr("placement class"), "type": pStr("record type name, e.g. entity, model, light"),
				"text":   pStr("substring of name / GUID / override type"),
				"near":   map[string]any{"type": "array", "items": map[string]any{"type": "number"}, "description": "[x, y, z, radius] world coordinates (radius in m)"},
				"offset": pOffset, "limit": pInt("default 100, max 1000")}, "map"),
			typed(a.toolPlacements)),
		tool("placement", "One map record: what it places, where, its decoded fields and instance data (the per-placement "+
			"overrides), and for an entity its definition (components, graphs).",
			obj(map[string]any{"map": pGUID, "index": pInt("record index (map_placements' i, current_view's placement.record)"),
				"instance_data": map[string]any{"type": "boolean", "description": "include the instance-data JSON (default true, each capped at 12 KB)"}}, "map", "index"),
			typed(a.toolPlacement)),
		textureImageTool(a),
		modelImageTool(a),
		materialImageTool(a),
		audioExportTool(a),
		{Name: "open", Drive: true, Description: "Open a resource in the user's explorer: in the focused pane (current), a new tab, " +
			"a background tab, the split pane or a new window; optionally on a view tab with a selection (map record, tree " +
			"instance, texture mip). Returns the pane as current_view shows it.",
			Schema: obj(map[string]any{"guid": pGUID, "tab": pEnum("view tab", "info", "map", "texture", "model", "material", "audio", "tree", "graph", "entity", "deps", "refs", "hex", "text"),
				"where":  pEnum("default current", "current", "tab", "background", "split", "window"),
				"select": pInt("with tab: map record / tree instance / texture mip / model submesh / graph node (m_nodes index; centred)"), "window": pWindow}, "guid"),
			Handler: a.toolOpen},
		{Name: "select", Drive: true, Description: "Change the selection in a pane of the user's explorer: the view tab, a map " +
			"record (the map view focuses it), a tree instance / path, a Statescript graph node (m_nodes index), a texture mip. " +
			"Returns the pane as current_view shows it.",
			Schema: obj(map[string]any{"pane": pEnum("default focused", "focused", "main", "split"),
				"tab":    pEnum("view tab", "info", "map", "texture", "model", "material", "audio", "tree", "graph", "entity", "deps", "refs", "hex", "text"),
				"record": pInt("map record index"), "instance": pInt("STU instance (tree / entity)"), "path": pStr("tree node path"),
				"node": pInt("graph node, m_nodes index"), "mip": pInt("texture mip"), "submesh": pInt("model submesh (Model tab)"), "window": pWindow}),
			Handler: a.toolSelect},
	}
	return ts
}

// ---- helpers --------------------------------------------------------------------------------------

func (a *App) guidArg(s string) (*catalog.Catalog, uint64, error) {
	c, err := a.catalog()
	if err != nil {
		return nil, 0, err
	}
	g, ok := catalog.ParseGUID(s)
	if !ok {
		return nil, 0, fmt.Errorf("not a GUID: %q", s)
	}
	return c, g, nil
}

func hexg(g uint64) string { return fmt.Sprintf("%016x", g) }

func pageArgs(offset, limit, def, maxN int) (int, int) {
	if limit <= 0 {
		limit = def
	}
	return max(0, offset), min(limit, maxN)
}

// brief is a resource in one line: GUID, type, name (+ guess, source, note), size; missing if not in the install.
type briefRow struct {
	GUID     string   `json:"guid"`
	Type     string   `json:"type"` // "0x01B StatescriptGraph"
	Name     string   `json:"name,omitempty"`
	Guess    bool     `json:"nameGuessed,omitempty"`
	NameFrom string   `json:"nameSource,omitempty"`
	Note     string   `json:"note,omitempty"`
	Size     uint32   `json:"size,omitempty"`
	Missing  bool     `json:"notAResource,omitempty"`
	Kinds    []string `json:"kinds,omitempty"`
}

func (a *App) brief(c *catalog.Catalog, g uint64) briefRow {
	t := content.TypeOf(g)
	n := c.Name(g)
	b := briefRow{GUID: hexg(g), Type: fmt.Sprintf("0x%03X %s", t, c.TypeLabel(t)), Name: n.Text, Guess: n.Guess, NameFrom: n.Source, Note: n.Note}
	if r, ok := c.Find(g); ok {
		b.Size = r.Size
	} else {
		b.Missing = true
	}
	return b
}

// ---- status / current_view ------------------------------------------------------------------------

func (a *App) toolStatus(ctx context.Context, _ struct{}) (any, error) {
	st := a.Status()
	m := a.mcpInit()
	out := map[string]any{"install": st.Install, "describe": st.Describe, "open": st.Open, "resources": st.Resources,
		"types": st.Types, "packages": st.Packages, "index": st.Index, "error": st.Error,
		"window":       map[string]any{"port": m.port, "ports": m.srv.Ports(), "primary": m.srv.Primary(), "child": a.child, "link": a.StartupLink()},
		"driveEnabled": m.drive.Load()}
	var peers []map[string]any
	for _, p := range m.srv.Peers(m.base, mcpSpan) {
		peers = append(peers, map[string]any{"port": p.Port, "pid": p.Health["pid"], "child": p.Health["child"],
			"link": p.Health["link"], "resource": p.Health["resource"], "driveEnabled": p.Health["drive_enabled"]})
	}
	out["otherWindows"] = peers
	return out, nil
}

func (a *App) toolCurrentView(ctx context.Context, q struct {
	Window int  `json:"window"`
	This   bool `json:"this"` // internal: only this window (peer calls)
}) (any, error) {
	m := a.mcpInit()
	if q.This || (q.Window != 0 && contains(m.srv.Ports(), q.Window)) {
		return a.currentView(), nil
	}
	if q.Window != 0 {
		res, err := mcp.CallPeer(ctx, q.Window, "current_view", map[string]any{"this": true})
		if err != nil {
			return nil, err
		}
		return map[string]any{"windows": []json.RawMessage{res}}, nil
	}
	wins := []any{a.currentView()}
	for _, p := range m.srv.Peers(m.base, mcpSpan) {
		if res, err := mcp.CallPeer(ctx, p.Port, "current_view", map[string]any{"this": true}); err == nil {
			wins = append(wins, res)
		} else {
			wins = append(wins, map[string]any{"port": p.Port, "error": err.Error()})
		}
	}
	return map[string]any{"windows": wins}, nil
}

// ---- describe -------------------------------------------------------------------------------------

func (a *App) toolDescribe(ctx context.Context, q struct {
	GUID string `json:"guid"`
}) (any, error) {
	c, g, err := a.guidArg(q.GUID)
	if err != nil {
		return nil, err
	}
	b := a.brief(c, g)
	out := map[string]any{"resource": b}
	if b.Missing {
		out["note"] = "not a resource of this install (maybe a config-var id or a GUID from another build); refs(direction=in) shows who uses it"
		return out, nil
	}
	info, err := a.Resource(hexg(g))
	if err != nil {
		return nil, err
	}
	var pk []briefRow
	for _, p := range info.Packages[:min(len(info.Packages), 12)] {
		pg, _ := catalog.ParseGUID(p)
		pk = append(pk, a.brief(c, pg))
	}
	out["packages"] = pk
	out["packageCount"] = len(info.Packages)
	out["format"] = info.Kind
	for _, dir := range []string{"out", "in"} {
		p, _ := c.Neighbors(catalog.NeighborQuery{GUID: hexg(g), Dir: dir, Count: -1})
		if p.Ready {
			bt := p.ByType[:min(len(p.ByType), 12)]
			out["refs"+map[string]string{"out": "Out", "in": "In"}[dir]] = map[string]any{"total": p.All, "byKind": p.ByKind, "byType": bt}
		}
	}
	summary, err := a.kindSummary(c, g, info)
	if err != nil {
		out["summaryError"] = err.Error()
	} else if summary != nil {
		out["summary"] = summary
	}
	out["tabs"] = tabsOf(info)
	return out, nil
}

// tabsOf mirrors the frontend's tabsFor (useExplorer.ts).
func tabsOf(r ResourceInfo) []string {
	var t []string
	if r.Type == 0x02 || r.Type == 0x9f {
		t = append(t, "map")
	}
	if r.Kind == "texture" {
		t = append(t, "texture")
	}
	if r.Kind == "model" || r.Type == 0x1a {
		t = append(t, "model")
	}
	if r.Kind == "material" {
		t = append(t, "material")
	}
	if isAudio(r.Type) {
		t = append(t, "audio")
	}
	if r.Kind == "stu" {
		if r.Type == 0x1b {
			t = append(t, "graph")
		}
		if r.Type == 0x03 {
			t = append(t, "entity")
		}
		t = append(t, "tree")
	} else if r.Kind == "text" {
		t = append(t, "text")
	}
	return append(t, "info", "deps", "refs", "hex")
}

func (a *App) kindSummary(c *catalog.Catalog, g uint64, info ResourceInfo) (any, error) {
	t := content.TypeOf(g)
	switch {
	case t == 0x002 || t == 0x09F:
		return a.mapSummary(c, g)
	case info.Kind == "texture":
		tv, err := a.Texture(hexg(g))
		if err != nil {
			return nil, err
		}
		return map[string]any{"texture": tv.GUID, "payloadOf": tv.Payload, "format": tv.Format, "hdr": tv.HDR,
			"channels": tv.Channels, "srgb": tv.SRGB, "size": fmt.Sprintf("%d×%d", tv.Width, tv.Height), "mips": len(tv.Mips),
			"surfaces": tv.Surfaces, "cube": tv.Cube, "flags": tv.FlagNames, "deltaOf": tv.Base, "payloads": len(tv.Payloads),
			"decoder": tv.Decoder, "error": tv.Error}, nil
	case info.Kind == "model" || info.Kind == "material":
		return a.modelSummary(c, g)
	case isAudio(t): // WEMs, sounds, banks, switch / state groups (app_mcp_audio.go, T065)
		return a.audioSummary(c, g)
	case info.Kind == "text":
		return map[string]any{"text": clipStr(info.Text, 2000)}, nil
	case t == content.TypeAssetPackage:
		iv, err := a.Info(hexg(g))
		if err != nil {
			return nil, err
		}
		return iv.Package, nil
	case info.Kind == "stu":
		var out any
		err := c.WithDoc(g, func(d *stu.Document) error {
			switch t {
			case 0x003:
				e, err := view.BuildEntity(d, c)
				if err != nil {
					return err
				}
				out = a.entityOut(c, e, 30, false)
			case 0x01B:
				gr, err := view.BuildGraph(d, c)
				if err != nil {
					return err
				}
				out = a.graphSummary(c, gr)
			default:
				doc := view.Describe(d, c)
				root := view.Tree(d, doc.Root, c)
				out = map[string]any{"rootType": instType(doc, doc.Root), "instances": len(doc.Instances),
					"crcOk": doc.AllCRCOK, "root": trimNode(root, 2, 30)}
			}
			return nil
		})
		return out, err
	}
	return nil, nil
}

func instType(doc view.Document, i int) string {
	for _, in := range doc.Instances {
		if in.Index == i {
			return in.Type
		}
	}
	return ""
}

func clipStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("… (%d more bytes)", len(s)-n)
}

// ---- refs -----------------------------------------------------------------------------------------

type refsArgs struct {
	GUID           string   `json:"guid"`
	Direction      string   `json:"direction"`
	Depth          int      `json:"depth"`
	Kinds          []string `json:"kinds"`
	Types          []typeID `json:"types"`
	Search         string   `json:"search"`
	IncludeMissing bool     `json:"include_missing"`
	Offset         int      `json:"offset"`
	Limit          int      `json:"limit"`
}

func (a *App) toolRefs(ctx context.Context, q refsArgs) (any, error) {
	c, g, err := a.guidArg(q.GUID)
	if err != nil {
		return nil, err
	}
	if c.Index() == nil {
		return nil, fmt.Errorf("the reference index is not ready yet (status: index)")
	}
	dirs := []string{"out"}
	switch q.Direction {
	case "", "out":
	case "in":
		dirs = []string{"in"}
	case "both":
		dirs = []string{"out", "in"}
	default:
		return nil, fmt.Errorf("direction: out, in or both")
	}
	kinds := q.Kinds
	if len(kinds) == 0 && content.TypeOf(g) != content.TypeAssetPackage { // membership edges drown everything else
		for _, k := range c.KindRows() {
			if k.Name != "package.member" {
				kinds = append(kinds, k.Name)
			}
		}
	}
	var types []int
	for _, t := range q.Types {
		types = append(types, int(t))
	}
	offset, limit := pageArgs(q.Offset, q.Limit, 50, 500)
	depth := max(1, min(q.Depth, 3))
	out := map[string]any{"resource": a.brief(c, g)}
	for _, dir := range dirs {
		nq := catalog.NeighborQuery{GUID: hexg(g), Dir: dir, Kinds: kinds, Types: types, Search: q.Search,
			NoMissing: !q.IncludeMissing, Start: offset, Count: limit}
		if depth == 1 {
			p, err := c.Neighbors(nq)
			if err != nil {
				return nil, err
			}
			out[dir] = map[string]any{"all": p.All, "total": p.Total, "offset": p.Start, "byKind": p.ByKind,
				"byType": p.ByType[:min(len(p.ByType), 20)], "rows": p.Rows}
			continue
		}
		// breadth-first: edges (from, to, kinds, depth), each resource expanded once
		type edge struct {
			From  string   `json:"from"`
			To    string   `json:"to"`
			Kinds []string `json:"kinds"`
			Depth int      `json:"depth"`
		}
		var edges []edge
		nodes := map[string]briefRow{hexg(g): a.brief(c, g)}
		frontier := []string{hexg(g)}
		truncated := false
		for d := 1; d <= depth && len(frontier) > 0 && !truncated; d++ {
			var next []string
			for _, f := range frontier {
				nq.GUID, nq.Start, nq.Count = f, 0, limit
				p, err := c.Neighbors(nq)
				if err != nil {
					return nil, err
				}
				if p.Total > len(p.Rows) {
					truncated = true
				}
				for _, r := range p.Rows {
					if len(edges) >= limit {
						truncated = true
						break
					}
					from, to := f, r.GUID
					if dir == "in" {
						from, to = r.GUID, f
					}
					edges = append(edges, edge{from, to, r.Kinds, d})
					if _, seen := nodes[r.GUID]; !seen {
						x, _ := catalog.ParseGUID(r.GUID)
						nodes[r.GUID] = a.brief(c, x)
						next = append(next, r.GUID)
					}
				}
			}
			frontier = next
		}
		out[dir] = map[string]any{"edges": edges, "nodes": nodes, "truncated": truncated}
	}
	return out, nil
}

// ---- search ---------------------------------------------------------------------------------------

func (a *App) toolSearch(ctx context.Context, q struct {
	Text   string  `json:"text"`
	Type   *typeID `json:"type"`
	Offset int     `json:"offset"`
	Limit  int     `json:"limit"`
}) (any, error) {
	c, err := a.catalog()
	if err != nil {
		return nil, err
	}
	offset, limit := pageArgs(q.Offset, q.Limit, 50, 500)
	t := -1
	if q.Type != nil {
		t = int(*q.Type)
	}
	if t < 0 && strings.TrimSpace(q.Text) == "" {
		return nil, fmt.Errorf("give text and / or type")
	}
	p := c.List(catalog.Query{Type: t, Search: q.Text, Start: offset, Count: limit})
	rows := make([]briefRow, 0, len(p.Rows))
	for _, r := range p.Rows {
		g, _ := catalog.ParseGUID(r.GUID)
		rows = append(rows, a.brief(c, g))
	}
	return map[string]any{"total": p.Total, "offset": p.Start, "rows": rows}, nil
}

// ---- entity ---------------------------------------------------------------------------------------

func (a *App) toolEntity(ctx context.Context, q struct {
	GUID string `json:"guid"`
	Refs int    `json:"refs"`
}) (any, error) {
	c, g, err := a.guidArg(q.GUID)
	if err != nil {
		return nil, err
	}
	if content.TypeOf(g) != 0x003 {
		return nil, fmt.Errorf("%016x is a %s, not an entity definition (0x003)", g, c.TypeLabel(content.TypeOf(g)))
	}
	var out map[string]any
	err = c.WithDoc(g, func(d *stu.Document) error {
		e, err := view.BuildEntity(d, c)
		if err != nil {
			return err
		}
		out = a.entityOut(c, e, max(1, cmp0(q.Refs, 100)), true)
		return nil
	})
	if err != nil {
		return nil, err
	}
	out["resource"] = a.brief(c, g)
	return out, nil
}

func cmp0(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

// entityOut: components with their graphs (and, withGraphs, each graph's counts: that opens the graphs), and the refs.
func (a *App) entityOut(c *catalog.Catalog, e *view.Entity, maxRefs int, withGraphs bool) map[string]any {
	type graphOut struct {
		GUID      string         `json:"guid"`
		Name      string         `json:"name,omitempty"`
		Overrides int            `json:"overrides"`
		Summary   map[string]any `json:"summary,omitempty"`
	}
	type compOut struct {
		Key      string     `json:"key"`
		Type     string     `json:"type"`
		Instance int        `json:"instance"`
		Graphs   []graphOut `json:"graphs,omitempty"`
		Refs     int        `json:"refs"`
	}
	var comps []compOut
	var refs []map[string]any
	add := func(r view.EntityRef, where string) {
		if len(refs) < maxRefs {
			g, _ := catalog.ParseGUID(r.GUID)
			b := a.brief(c, g)
			refs = append(refs, map[string]any{"at": where + r.Path, "guid": r.GUID, "type": b.Type, "name": b.Name, "note": b.Note})
		}
	}
	total := len(e.Refs)
	for _, r := range e.Refs {
		add(r, "")
	}
	for _, cp := range e.Components {
		co := compOut{Key: cp.Key, Type: cp.Type, Instance: cp.Instance, Refs: len(cp.Refs)}
		for _, gr := range cp.Graphs {
			goo := graphOut{GUID: gr.GUID, Name: gr.Name, Overrides: gr.Overrides}
			if withGraphs {
				if gg, ok := catalog.ParseGUID(gr.GUID); ok {
					_ = c.WithDoc(gg, func(d *stu.Document) error {
						if g2, err := view.BuildGraph(d, c); err == nil {
							goo.Summary = a.graphCounts(g2)
						}
						return nil
					})
				}
			}
			co.Graphs = append(co.Graphs, goo)
		}
		total += len(cp.Refs)
		for _, r := range cp.Refs {
			add(r, cp.Type+".")
		}
		comps = append(comps, co)
	}
	return map[string]any{"components": comps, "refs": refs, "refsTotal": total}
}

// ---- Statescript graphs ---------------------------------------------------------------------------

func (a *App) graphCounts(gr *view.Graph) map[string]any {
	kinds, net := map[string]int{}, map[string]int{}
	for _, n := range gr.Nodes {
		kinds[n.Kind]++
		net[n.Net]++
	}
	return map[string]any{"nodes": len(gr.Nodes), "byKind": kinds, "byNet": net, "links": len(gr.Links), "entries": gr.Entries,
		"states": gr.States, "syncVars": gr.SyncVars, "remoteSyncNodes": gr.RemoteSync}
}

func nodeName(n view.GraphNode) string {
	s := n.Short
	if n.Display != "" {
		s += " \"" + n.Display + "\""
	}
	return s
}

func (a *App) graphSummary(c *catalog.Catalog, gr *view.Graph) map[string]any {
	out := a.graphCounts(gr)
	var entries, states []string
	params := 0
	unnamed := map[string]int{}
	refs := map[string]int{}
	for _, n := range gr.Nodes {
		switch n.Kind {
		case "Entry":
			entries = append(entries, fmt.Sprintf("#%d %s", n.Index, nodeName(n)))
		case "State":
			states = append(states, fmt.Sprintf("#%d %s", n.Index, nodeName(n)))
		}
		if !n.Named {
			unnamed[n.Type]++
		}
		params += len(n.Params)
		for _, p := range n.Params {
			for _, r := range p.Refs {
				refs[r.GUID]++
			}
		}
	}
	out["entryNodes"] = entries
	if len(states) > 40 {
		states = append(states[:40], fmt.Sprintf("… %d more", len(states)-40))
	}
	out["stateNodes"] = states
	out["configVars"] = params
	if len(unnamed) > 0 {
		out["unnamedNodeTypes"] = unnamed
	}
	var rl []briefRow
	for gs := range refs {
		g, _ := catalog.ParseGUID(gs)
		rl = append(rl, a.brief(c, g))
	}
	sort.Slice(rl, func(i, j int) bool { return rl[i].GUID < rl[j].GUID })
	if len(rl) > 60 {
		out["referencedTotal"] = len(rl)
		rl = rl[:60]
	}
	out["referenced"] = rl
	var titles []string
	for _, ct := range gr.Containers {
		if ct.Title != "" {
			titles = append(titles, ct.Title)
		}
	}
	if len(titles) > 0 {
		out["containers"] = titles
	}
	return out
}

// graphNodeOut is one node; full adds parameters, plugs and the links in and out (by m_nodes index).
func graphNodeOut(gr *view.Graph, pos int, full bool) map[string]any {
	n := gr.Nodes[pos]
	o := map[string]any{"index": n.Index, "kind": n.Kind, "type": n.Type, "named": n.Named, "net": n.Net}
	if full {
		o["netWhy"] = n.NetWhy
	}
	if n.NetConflict != "" {
		o["netConflict"] = n.NetConflict
	}
	if n.Display != "" {
		o["display"] = n.Display
	}
	if n.Comment != "" {
		o["comment"] = n.Comment
	}
	if n.StateIndex >= 0 {
		o["stateIndex"] = n.StateIndex
	}
	if n.RemoteSync >= 0 {
		o["remoteSync"] = n.RemoteSync
	}
	if n.Scope != 0 {
		o["scope"] = n.Scope
	}
	if n.ClientOnly {
		o["clientOnly"] = true
	}
	if n.ServerOnly {
		o["serverOnly"] = true
	}
	outs := map[string][]int{}
	var ins []map[string]any
	for _, l := range gr.Links {
		if l.From == pos {
			outs[l.FromPlug] = append(outs[l.FromPlug], gr.Nodes[l.To].Index)
		}
		if full && l.To == pos {
			ins = append(ins, map[string]any{"from": gr.Nodes[l.From].Index, "fromType": gr.Nodes[l.From].Short, "plug": l.FromPlug, "toPlug": l.ToPlug})
		}
	}
	if len(outs) > 0 {
		o["out"] = outs
	}
	if full {
		if len(ins) > 0 {
			o["in"] = ins
		}
		var ps []map[string]any
		for _, p := range n.Params {
			pm := map[string]any{"name": p.Name, "type": p.Type, "value": p.Summary}
			if len(p.Refs) > 0 {
				pm["refs"] = p.Refs
			}
			ps = append(ps, pm)
		}
		if len(ps) > 0 {
			o["params"] = ps
		}
		if n.Debug != "" {
			o["debug"] = n.Debug
		}
	} else if len(n.Params) > 0 {
		o["params"] = len(n.Params)
	}
	return o
}

func (a *App) toolGraph(ctx context.Context, q struct {
	GUID   string `json:"guid"`
	Detail string `json:"detail"`
	Node   *int   `json:"node"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}) (any, error) {
	c, g, err := a.guidArg(q.GUID)
	if err != nil {
		return nil, err
	}
	if content.TypeOf(g) != 0x01B {
		return nil, fmt.Errorf("%016x is a %s, not a Statescript graph (0x01B)", g, c.TypeLabel(content.TypeOf(g)))
	}
	switch q.Detail {
	case "", "nodes", "summary", "full":
	default:
		return nil, fmt.Errorf("detail: summary, nodes or full")
	}
	var out map[string]any
	err = c.WithDoc(g, func(d *stu.Document) error {
		gr, err := view.BuildGraph(d, c)
		if err != nil {
			return err
		}
		if q.Node != nil {
			for i, n := range gr.Nodes {
				if n.Index == *q.Node {
					out = map[string]any{"graph": a.brief(c, g), "node": graphNodeOut(gr, i, true)}
					return nil
				}
			}
			return fmt.Errorf("no node %d (the graph has %d)", *q.Node, len(gr.Nodes))
		}
		out = a.graphSummary(c, gr)
		out["graph"] = a.brief(c, g)
		if q.Detail == "summary" {
			return nil
		}
		offset, limit := pageArgs(q.Offset, q.Limit, 200, 2000)
		var nodes []map[string]any
		for i := offset; i < len(gr.Nodes) && i < offset+limit; i++ {
			nodes = append(nodes, graphNodeOut(gr, i, q.Detail == "full"))
		}
		out["nodeList"] = nodes
		out["offset"] = offset
		if offset+limit < len(gr.Nodes) {
			out["more"] = len(gr.Nodes) - offset - limit
		}
		return nil
	})
	return out, err
}

// ---- STU tree -------------------------------------------------------------------------------------

// trimNode cuts a tree at depth and every child list at maxChildren.
func trimNode(n view.Node, depth, maxChildren int) map[string]any {
	o := map[string]any{"path": n.Path, "label": n.Label, "kind": n.Kind}
	if n.Type != "" {
		o["type"] = n.Type
	}
	if n.Value != "" {
		o["value"] = clipStr(n.Value, 400)
	}
	if n.Alt != "" {
		o["alt"] = n.Alt
	}
	if n.GUID != "" {
		o["guid"] = n.GUID
		if n.RefName != "" {
			o["refName"] = n.RefName
		}
		if n.Missing {
			o["notAResource"] = true
		}
	}
	if n.Instance >= 0 && n.Kind == "object" {
		o["instance"] = n.Instance
	}
	if n.Count > 0 {
		o["count"] = n.Count
	}
	if len(n.Children) > 0 {
		if depth <= 0 {
			o["children"] = fmt.Sprintf("%d (deeper: stu_tree path=%q)", len(n.Children), n.Path)
		} else {
			var ch []map[string]any
			for _, x := range n.Children[:min(len(n.Children), maxChildren)] {
				ch = append(ch, trimNode(x, depth-1, maxChildren))
			}
			o["children"] = ch
			if len(n.Children) > maxChildren {
				o["truncated"] = len(n.Children) - maxChildren
			}
		}
	}
	return o
}

func (a *App) toolTree(ctx context.Context, q struct {
	GUID        string `json:"guid"`
	Instance    *int   `json:"instance"`
	Path        string `json:"path"`
	Depth       int    `json:"depth"`
	MaxChildren int    `json:"max_children"`
}) (any, error) {
	c, g, err := a.guidArg(q.GUID)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = c.WithDoc(g, func(d *stu.Document) error {
		doc := view.Describe(d, c)
		inst := -1
		if q.Instance != nil {
			inst = *q.Instance
		} else if q.Path != "" { // i<instance>.…
			if k := strings.IndexByte(q.Path, '.'); strings.HasPrefix(q.Path, "i") {
				s := q.Path[1:]
				if k > 0 {
					s = q.Path[1:k]
				}
				inst, _ = strconv.Atoi(s)
			}
		}
		if inst < 0 {
			rows := doc.Instances
			var list []map[string]any
			for _, r := range rows[:min(len(rows), 200)] {
				list = append(list, map[string]any{"index": r.Index, "type": r.Type, "root": r.Root, "summary": r.Summary, "errors": r.Errors})
			}
			out = map[string]any{"resource": a.brief(c, g), "root": doc.Root, "crcOk": doc.AllCRCOK, "instances": list,
				"instanceCount": len(rows)}
			return nil
		}
		if inst >= d.NumInstances() {
			return fmt.Errorf("instance %d out of range (%d)", inst, d.NumInstances())
		}
		n := view.Tree(d, inst, c)
		if q.Path != "" {
			f := findPath(n, q.Path)
			if f == nil {
				return fmt.Errorf("no node at path %q in instance %d", q.Path, inst)
			}
			n = *f
		}
		out = map[string]any{"resource": a.brief(c, g), "instance": inst, "type": instType(doc, inst),
			"tree": trimNode(n, max(1, min(cmp0(q.Depth, 3), 8)), cmp0(q.MaxChildren, 40))}
		return nil
	})
	return out, err
}

// ---- maps -----------------------------------------------------------------------------------------

var mapViews struct {
	sync.Mutex
	guid string
	v    *mapview.View
}

// mapBuilt is the map view of a map (cached for the last map asked; the map data itself is cached by openMap).
func (a *App) mapBuilt(guid string) (*catalog.Catalog, *mapview.View, error) {
	c, m, err := a.openMap(guid)
	if err != nil {
		return nil, nil, err
	}
	key := hexg(m.GUID)
	mapViews.Lock()
	defer mapViews.Unlock()
	if mapViews.guid == key && mapViews.v != nil {
		return c, mapViews.v, nil
	}
	a.mmu.Lock()
	v := m.Build(c)
	a.mmu.Unlock()
	mapViews.guid, mapViews.v = key, v
	return c, v, nil
}

var recordTypes = []string{"type 0", "model group", "model", "occluder", "reflection probe", "type 5", "text label", "map text",
	"placed model", "light", "sound volume", "entity", "sound emitter", "effect", "volume 0x44", "volume 0x45"}

func recordType(t int) string {
	if t >= 0 && t < len(recordTypes) {
		return recordTypes[t]
	}
	return fmt.Sprintf("type %d", t)
}

func round2(x float32) float64 { return math.Round(float64(x)*100) / 100 }

func placementRow(p mapview.Placement) map[string]any {
	o := map[string]any{"i": p.I, "class": p.Class, "recordType": recordType(p.Type)}
	if p.GUID != "" {
		o["guid"] = p.GUID
	}
	if p.Name != "" {
		o["name"] = p.Name
	}
	if p.Placed >= 0 {
		o["placed"] = fmt.Sprintf("0x%x", p.Placed)
	}
	if p.HasPos {
		o["pos"] = []float64{round2(p.Pos[0]), round2(p.Pos[1]), round2(p.Pos[2])}
		o["yawDeg"] = math.Round(float64(p.Yaw)*1800/math.Pi) / 10
	}
	if p.Look != "" {
		o["look"] = p.Look
	}
	if len(p.Overrides) > 0 {
		o["overrides"] = p.Overrides
	}
	if p.Group > 0 {
		o["groupInstances"] = p.Group
	}
	return o
}

func (a *App) mapSummary(c *catalog.Catalog, g uint64) (map[string]any, error) {
	_, v, err := a.mapBuilt(hexg(g))
	if err != nil {
		return nil, err
	}
	mg, _ := catalog.ParseGUID(v.GUID)
	out := map[string]any{"mapData": a.brief(c, mg), "placements": len(v.Placements), "placedEntries": v.Placed,
		"classes": v.Classes, "sections": len(v.Sections), "assets": len(v.Assets), "collision": v.Collision}
	var env []map[string]any
	for _, e := range v.Env {
		env = append(env, map[string]any{"label": e.Label, "guid": e.GUID, "name": e.Name})
	}
	out["environment"] = env
	var net []map[string]any
	for _, p := range v.Placements {
		if p.Class == "networked" && len(net) < 60 {
			net = append(net, placementRow(p))
		}
	}
	out["networkedEntities"] = net
	return out, nil
}

func (a *App) placementBrief(mapGUID string, rec int) (map[string]any, error) {
	c, v, err := a.mapBuilt(mapGUID)
	if err != nil {
		return nil, err
	}
	for _, p := range v.Placements {
		if p.I == rec {
			o := placementRow(p)
			o["record"] = rec
			if g, ok := catalog.ParseGUID(p.GUID); ok && p.GUID != "" {
				b := a.brief(c, g)
				o["primary"] = b
				if mp := a.modelPlacement(mapGUID, rec); mp != nil {
					o["model"] = mp
				}
				if content.TypeOf(g) == 0x003 {
					_ = c.WithDoc(g, func(d *stu.Document) error {
						if e, err := view.BuildEntity(d, c); err == nil {
							var comps, graphs []string
							for _, cp := range e.Components {
								comps = append(comps, cp.Type)
								for _, gr := range cp.Graphs {
									graphs = append(graphs, strings.TrimSpace(gr.GUID+" "+gr.Name))
								}
							}
							o["definition"] = map[string]any{"components": comps, "graphs": graphs}
						}
						return nil
					})
				}
			}
			return o, nil
		}
	}
	return nil, fmt.Errorf("map %s has no record %d", mapGUID, rec)
}

func (a *App) toolPlacements(ctx context.Context, q struct {
	Map    string    `json:"map"`
	Class  string    `json:"class"`
	Type   string    `json:"type"`
	Text   string    `json:"text"`
	Near   []float64 `json:"near"`
	Offset int       `json:"offset"`
	Limit  int       `json:"limit"`
}) (any, error) {
	c, v, err := a.mapBuilt(q.Map)
	if err != nil {
		return nil, err
	}
	if len(q.Near) != 0 && len(q.Near) != 4 {
		return nil, fmt.Errorf("near: [x, y, z, radius]")
	}
	text := strings.ToLower(strings.TrimSpace(q.Text))
	var keep []mapview.Placement
	for _, p := range v.Placements {
		if q.Class != "" && p.Class != q.Class || q.Type != "" && recordType(p.Type) != q.Type {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(p.Name), text) && !strings.Contains(p.GUID, strings.TrimPrefix(text, "0x")) &&
			!slices.ContainsFunc(p.Overrides, func(o string) bool { return strings.Contains(strings.ToLower(o), text) }) {
			continue
		}
		if len(q.Near) == 4 {
			if !p.HasPos {
				continue
			}
			dx, dy, dz := float64(p.Pos[0])-q.Near[0], float64(p.Pos[1])-q.Near[1], float64(p.Pos[2])-q.Near[2]
			if dx*dx+dy*dy+dz*dz > q.Near[3]*q.Near[3] {
				continue
			}
		}
		keep = append(keep, p)
	}
	if len(q.Near) == 4 {
		d := func(p mapview.Placement) float64 {
			dx, dy, dz := float64(p.Pos[0])-q.Near[0], float64(p.Pos[1])-q.Near[1], float64(p.Pos[2])-q.Near[2]
			return dx*dx + dy*dy + dz*dz
		}
		sort.SliceStable(keep, func(i, j int) bool { return d(keep[i]) < d(keep[j]) })
	}
	offset, limit := pageArgs(q.Offset, q.Limit, 100, 1000)
	var rows []map[string]any
	for i := offset; i < len(keep) && i < offset+limit; i++ {
		rows = append(rows, placementRow(keep[i]))
	}
	mg, _ := catalog.ParseGUID(v.GUID)
	return map[string]any{"map": a.brief(c, mg), "classes": v.Classes, "total": len(keep), "offset": offset, "rows": rows}, nil
}

func (a *App) toolPlacement(ctx context.Context, q struct {
	Map          string `json:"map"`
	Index        int    `json:"index"`
	InstanceData *bool  `json:"instance_data"`
}) (any, error) {
	pb, err := a.placementBrief(q.Map, q.Index)
	if err != nil {
		return nil, err
	}
	det, err := a.MapRecord(q.Map, q.Index)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"placement": pb, "recordSize": det.Size, "uuid": det.UUID, "fields": det.Fields}
	if det.Instances > 0 {
		out["instances"] = det.Instances
	}
	if len(det.Shapes) > 0 {
		out["shapes"] = det.Shapes
	}
	if q.InstanceData == nil || *q.InstanceData {
		var ids []map[string]any
		for _, b := range det.InstanceData {
			ids = append(ids, map[string]any{"slot": b.Slot, "size": b.Size, "types": b.Types, "json": clipStr(b.JSON, 12<<10)})
		}
		out["instanceData"] = ids
	}
	return out, nil
}
