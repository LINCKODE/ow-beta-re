package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"owexplorer/internal/names"
	"owexplorer/internal/refindex"
	"owexplorer/internal/view"
	"owstu/stu"
)

func open(t *testing.T) *Catalog {
	repo := names.FindRepo(".")
	install := os.Getenv("OW_INSTALL")
	if install == "" {
		install = filepath.Join(repo, "game", "owbetaclient")
	}
	if _, err := os.Stat(filepath.Join(install, ".build.info")); err != nil {
		t.Skip("no install at", install)
	}
	c, err := Open(install, repo)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Every graph and entity definition goes through every view without errors.
func TestViewsOnAllGraphsAndEntities(t *testing.T) {
	c := open(t)
	defer c.Close()
	t.Logf("open %v, %d resources, %d types", c.OpenTime, c.NumResources(), len(c.Types()))
	t0 := time.Now()
	nodes, links, unlinked, crossings, reversed := 0, 0, 0, 0, 0
	var layoutTime time.Duration
	net, conflicts := map[string]int{}, map[string]int{}
	for _, typ := range []uint32{0x01B, 0x003} {
		ids := c.match(Query{Type: int(typ)})
		if len(ids) == 0 {
			t.Fatalf("no resources of type %03X", typ)
		}
		for _, i := range ids {
			g := c.res[i].GUID
			err := c.WithDoc(g, func(d *stu.Document) error {
				doc := view.Describe(d, c)
				for _, in := range doc.Instances {
					view.Tree(d, in.Index, c)
				}
				if typ == 0x01B {
					gr, err := view.BuildGraph(d, c)
					if err != nil {
						return err
					}
					for _, n := range gr.Nodes {
						net[n.Net+" · "+n.NetWhy]++
						if n.NetConflict != "" {
							conflicts[n.NetConflict]++
							t.Logf("network conflict: graph %016x node #%d %s: %s", g, n.Index, n.Type, n.NetConflict)
						}
					}
					nodes += len(gr.Nodes)
					links += len(gr.Links)
					unlinked += gr.Unlinked
					// layered layout (graph view): every node placed, no overlaps in a column, routes end at the ports
					l0 := time.Now()
					lay := view.Layout(gr)
					layoutTime += time.Since(l0)
					crossings += lay.Crossings
					reversed += lay.Reversed
					if err := checkLayout(gr, lay); err != nil {
						return fmt.Errorf("layout: %w", err)
					}
				} else if _, err := view.BuildEntity(d, c); err != nil {
					return err
				}
				return nil
			})
			if err != nil {
				t.Errorf("%016x: %v", g, err)
			}
		}
	}
	t.Logf("views: %v; graphs: %d nodes, %d links, %d unresolved links", time.Since(t0), nodes, links, unlinked)
	t.Logf("layered layouts: %v, %d crossings, %d back edges", layoutTime, crossings, reversed)
	t.Logf("network side: %v; conflicts: %v", net, conflicts)
	if len(net) < 3 {
		t.Errorf("network sides: %v (want client, both and server)", net)
	}
}

func checkLayout(g *view.Graph, l *view.GraphLayout) error {
	if len(l.Pos) != len(g.Nodes) || len(l.Routes) != len(g.Links) {
		return fmt.Errorf("%d positions for %d nodes, %d routes for %d links", len(l.Pos), len(g.Nodes), len(l.Routes), len(g.Links))
	}
	cols := map[float64][]float64{}
	for _, p := range l.Pos {
		cols[p[0]] = append(cols[p[0]], p[1])
	}
	for x, ys := range cols {
		sort.Float64s(ys)
		for i := 1; i < len(ys); i++ {
			if ys[i] < ys[i-1]+view.NodeH-0.5 {
				return fmt.Errorf("column x=%v: boxes at y=%v and %v overlap", x, ys[i-1], ys[i])
			}
		}
	}
	for k, lk := range g.Links {
		r := l.Routes[k]
		if lk.From < 0 || lk.To < 0 {
			continue
		}
		a, b := l.Pos[lk.From], l.Pos[lk.To]
		if len(r) < 2 || r[0] != [2]float64{a[0] + view.NodeW, a[1] + view.NodeH/2} || r[len(r)-1] != [2]float64{b[0], b[1] + view.NodeH/2} {
			return fmt.Errorf("link %d: route %v doesn't join its ports", k, r)
		}
	}
	return nil
}

// waitIndex runs StartIndex and waits for it to finish.
func waitIndex(t *testing.T, c *Catalog) Progress {
	done := make(chan struct{}, 16)
	c.StartIndex(func() { done <- struct{}{} })
	deadline := time.After(90 * time.Second)
	for {
		select {
		case <-done:
			if p := c.Progress(); p.Phase == "done" || p.Phase == "failed" {
				return p
			}
		case <-deadline:
			t.Fatalf("index not done after 90 s: %+v", c.Progress())
		}
	}
}

func TestIndexAndSearch(t *testing.T) {
	c := open(t)
	defer c.Close()
	c.IndexCache = filepath.Join(t.TempDir(), "refindex.gob")
	t0 := time.Now()
	p := waitIndex(t, c)
	t.Logf("index built in %v: %+v", time.Since(t0), p)
	x := c.Index()
	if p.Phase != "done" || p.Cached || x == nil || p.Named == 0 {
		t.Fatalf("index %+v", p)
	}
	for _, st := range x.Stats {
		t.Logf("source %-8s scanned %6d failed %4d edges %8d names %5d in %.1f s", st.ID, st.Scanned, st.Failed, st.Edges, st.Names, st.Seconds)
		if st.Failed > 0 && st.ID != "strings" {
			t.Errorf("source %s: %d failures", st.ID, st.Failed)
		}
	}
	// every kind is used
	used := make([]int, len(x.Kinds))
	for _, m := range x.OutMask {
		for k := range x.Kinds {
			if m&(1<<k) != 0 {
				used[k]++
			}
		}
	}
	for k, n := range used {
		t.Logf("kind %-16s %8d pairs", x.Kinds[k].Name, n)
		if n == 0 {
			t.Errorf("kind %s has no edges", x.Kinds[k].Name)
		}
	}
	has := func(from, to uint64, kind string) bool {
		k, _ := x.KindByName(kind)
		for _, l := range x.Out(from) {
			if l.GUID == to && l.Mask&(1<<k) != 0 {
				return true
			}
		}
		return false
	}
	const anubis, brain, anubisPkg = 0x080000000000005B, 0x0400000000000039, 0x06E00000C5E10A94
	for _, e := range []struct {
		from, to uint64
		kind     string
	}{
		{anubis, brain, "map.env"},                // environment +0xF0 (docs/Maps.md §7)
		{anubis, anubisPkg, "package.owner"},      // the map's asset package
		{anubisPkg, anubis, "package.member"},     // which lists the map data
		{0x079000000000005B, anubis, "stu.field"}, // map header m_map
		// textures (T063): payloads by index, the low-detail variant shares payload 0, a delta names its base
		{0x0C0000000000007E, 0x032000010000007E, "texture.payload"},
		{0x0C00000F0000007E, 0x032000000000007E, "texture.payload"},
		{0x0C00000000002E66, 0x0C0000000000032C, "texture.base"},
		{0x0C00000000002E66, 0x032000010000032C, "texture.base"},
		// models / materials (T064): an entity's model component pairs model and look, a map places them together,
		// a material names its textures, a model's loader requests 0x08E resources
		{0x0D000000000011CE, 0x09800000000014B0, "model.look"},
		{0x0D00000000000BC0, 0x0980000000000C5C, "map.look"},
		{0x0E00000000002228, 0x0C00000000004F13, "material.texture"},
		{0x0D0000000000090F, 0x0B10000000000040, "model.effect"},
	} {
		if !has(e.from, e.to, e.kind) {
			t.Errorf("no %s edge %016x -> %016x", e.kind, e.from, e.to)
		}
	}
	// the most used resources (STU fields only: every resource is in a package)
	type deg struct {
		g uint64
		n int
	}
	var top []deg
	stuK, _ := x.KindByName("stu.field")
	for _, g := range x.Nodes {
		n := 0
		for _, l := range x.In(g) {
			if l.Mask&(1<<stuK) != 0 {
				n++
			}
		}
		top = append(top, deg{g, n})
	}
	sort.Slice(top, func(i, j int) bool { return top[i].n > top[j].n })
	for _, d := range top[:5] {
		t.Logf("most used: %016x (%s) by %d STU resources", d.g, c.Name(d.g).Text, d.n)
	}
	if len(c.RefsTo(0x058000000000000F)) == 0 {
		t.Error("graph 0x058…0F referenced by nobody")
	}
	np, err := c.Neighbors(NeighborQuery{GUID: "0400000000000039", Dir: "in", Kinds: []string{"map.env", "map.placement"}, Count: 50})
	if err != nil || np.Total == 0 {
		t.Errorf("brain 0x39 used by no map: %+v %v", np, err)
	}
	if n := c.Name(anubis); n.Text != "TEMPLE OF ANUBIS" {
		t.Errorf("map data name %+v", n)
	}
	// the cache gives the same index
	t1 := time.Now()
	y, err := refindex.Load(c.IndexCache, x.Key)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("cache load %v", time.Since(t1))
	if y.NumEdges() != x.NumEdges() || len(y.Names) != len(x.Names) || len(y.In(anubis)) != len(x.In(anubis)) {
		t.Errorf("cache differs: %d/%d edges, %d/%d names", y.NumEdges(), x.NumEdges(), len(y.Names), len(x.Names))
	}
	c2 := open(t)
	defer c2.Close()
	c2.IndexCache = c.IndexCache
	if p := waitIndex(t, c2); !p.Cached {
		t.Errorf("second open did not use the cache: %+v", p)
	}

	// every type: one resource through the info view's queries
	seen := map[uint32]bool{}
	for _, r := range c.res {
		if seen[r.Type] {
			continue
		}
		seen[r.Type] = true
		g := fmt.Sprintf("%016x", r.GUID)
		st := c.TypeStats(r.Type)
		n := c.Node(r.GUID)
		_, e1 := c.Neighbors(NeighborQuery{GUID: g, Dir: "out", Count: -1})
		_, e2 := c.Neighbors(NeighborQuery{GUID: g, Dir: "in", Count: -1})
		if st.Count == 0 || st.Max < st.Min || n.Missing || e1 != nil || e2 != nil {
			t.Errorf("type %03X: stats %+v node %+v %v %v", r.Type, st, n, e1, e2)
		}
	}
	t.Logf("info queries on %d types", len(seen))
	if n := c.Name(0x0400000000000022); n.Text != "Reaper" || n.Note != "hero entity definition" {
		t.Errorf("hero definition name %+v", n)
	}
	if pg := c.List(Query{Type: -1, Search: "0x0580000000000", Count: 5}); pg.Total < 300 {
		t.Errorf("hex search: %d", pg.Total)
	}
	if pg := c.List(Query{Type: -1, Search: "tracer", Count: 5}); pg.Total == 0 {
		t.Error("name search found nothing")
	} else {
		t.Logf("tracer: %d, first %+v", pg.Total, pg.Rows[0])
	}
	if i := c.IndexOf(Query{Type: 0x002}, anubis); i < 0 {
		t.Error("IndexOf: Anubis not in the 002 list")
	}
	t2 := time.Now()
	pk := c.Store.ResourcePackages(0x058000000000000F)
	t.Logf("ResourcePackages: %d packages in %v", len(pk), time.Since(t2))
	if pg := c.List(Query{Type: -1, Package: c.Packages()[0].GUID, Count: 5}); pg.Total == 0 {
		t.Error("package listing empty")
	}
	for _, ty := range c.Types() {
		if ty.Label == "unknown" {
			t.Logf("unlabelled type %03X (%d)", ty.Type, ty.Count)
		}
	}
}
