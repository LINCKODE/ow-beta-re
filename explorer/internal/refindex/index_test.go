package refindex

import (
	"path/filepath"
	"testing"
)

// The CSR index from a handful of edges: duplicates merge (kinds OR-ed, first aux kept), both directions,
// degrees, and the cache round trip.
func TestAssembleAndCache(t *testing.T) {
	kinds := []KindInfo{{Name: "a.x"}, {Name: "a.y"}, {Name: "b.z"}}
	edges := []rawEdge{
		{3, 1, 1 << 0, 0},
		{1, 2, 1 << 0, 0},
		{1, 2, 1 << 1, 7}, // same pair, other kind: merged
		{1, 3, 1 << 2, 0},
		{2, 3, 1 << 0, 0},
		{9, 3, 1 << 1, 5}, // 9 is only a source
	}
	x := assemble("k", kinds, edges, []NameRec{{GUID: 1, Text: "one"}}, nil)
	if x.NumEdges() != 5 {
		t.Fatalf("edges %d", x.NumEdges())
	}
	out := x.Out(1)
	if len(out) != 2 || out[0].GUID != 2 || out[0].Mask != 3 || out[0].Aux != 7 || out[1].GUID != 3 {
		t.Fatalf("out(1) %+v", out)
	}
	in := x.In(3)
	if len(in) != 3 || in[0].GUID != 1 || in[1].GUID != 2 || in[2].GUID != 9 || in[2].Aux != 5 {
		t.Fatalf("in(3) %+v", in)
	}
	if o, i := x.Degree(3); o != 1 || i != 3 {
		t.Fatalf("degree(3) %d %d", o, i)
	}
	if o, i := x.Degree(42); o != 0 || i != 0 || x.Out(42) != nil {
		t.Fatal("unknown node")
	}
	if m := x.MaskOf([]string{"a.y", "b.z", "nope"}); m != 6 {
		t.Fatalf("mask %b", m)
	}
	if n := x.KindNames(5); len(n) != 2 || n[0] != "a.x" || n[1] != "b.z" {
		t.Fatalf("kind names %v", n)
	}

	path := filepath.Join(t.TempDir(), "i.gob")
	if err := x.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, "other key"); err == nil {
		t.Fatal("loaded a cache built for another key")
	}
	y, err := Load(path, "k")
	if err != nil {
		t.Fatal(err)
	}
	if y.NumEdges() != 5 || len(y.In(3)) != 3 || y.Names[0].Text != "one" || y.Out(1)[0].Mask != 3 {
		t.Fatalf("cache round trip: %+v", y)
	}
}

func TestEmitter(t *testing.T) {
	e := &Emitter{base: 4, n: 2}
	e.Ref(1, 2, 1, 9)
	e.Ref(1, 1, 0, 0) // self
	e.Ref(0, 2, 0, 0) // zero
	e.Ref(1, 2, 2, 0) // kind out of range
	e.Name(5, "  ", "x", false)
	e.Name(5, "five", "x", false)
	if len(e.edges) != 1 || e.edges[0].mask != 1<<5 || e.edges[0].aux != 9 || len(e.names) != 1 {
		t.Fatalf("%+v %+v", e.edges, e.names)
	}
}
