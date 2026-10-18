package view

import "testing"

// A small graph: entry → a → c, entry → b → c, plus a cycle c → a and a long link entry → d (layer 3).
func TestLayout(t *testing.T) {
	g := &Graph{Nodes: []GraphNode{{Kind: "Entry"}, {Kind: "Action"}, {Kind: "Action"}, {Kind: "State"}, {Kind: "Action"}, {Kind: "Other"}}}
	for _, e := range [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}, {3, 1}, {3, 4}, {0, 4}} {
		g.Links = append(g.Links, GraphLink{From: e[0], To: e[1]})
	}
	l := Layout(g)
	col := func(i int) int { return int(l.Pos[i][0] / (NodeW + layerGap)) }
	if col(0) != 0 || col(1) != 1 || col(2) != 1 || col(3) != 2 || col(4) != 3 {
		t.Fatalf("layers: %v", l.Pos)
	}
	if l.Reversed != 1 || l.Crossings != 0 || l.Layers != 4 {
		t.Fatalf("reversed %d crossings %d layers %d", l.Reversed, l.Crossings, l.Layers)
	}
	if r := l.Routes[6]; len(r) != 4 { // entry → d spans 3 layers: 2 bends
		t.Fatalf("long link route: %v", r)
	}
	if r := l.Routes[4]; len(r) != 2 { // the back edge c → a: adjacent layers
		t.Fatalf("back edge route: %v", r)
	}
}
