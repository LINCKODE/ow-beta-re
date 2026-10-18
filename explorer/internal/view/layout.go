package view

import (
	"math"
	"sort"
)

// Layered (Sugiyama) layout of a Statescript graph for the graph view (docs/Explorer.md "Graph view"): links flow left
// to right, entries in the first layer, few crossings. View only: the game data keeps its editor positions (m_pos).
//
//  1. cycles: a DFS from the entries (then from every unvisited node) reverses its back edges;
//  2. layers: longest path from the sources;
//  3. a link spanning several layers gets a dummy node in each layer between (its route bends there);
//  4. order within a layer: barycenter sweeps down and up, keeping the order with the fewest crossings;
//  5. y: each node moves towards the mean of its neighbours, overlaps are pushed apart in layer order.

// Node box of the graph view (GraphView.vue W, H).
const (
	NodeW    = 190.0
	NodeH    = 46.0
	layerGap = 110.0 // between the columns
	nodeGap  = 22.0  // between boxes of a column
	dummyH   = 6.0
	partGap  = 60.0 // between the rows of connected parts
)

// GraphLayout is the layered layout: a position per node (Graph.Nodes order) and a route per link (Graph.Links order).
type GraphLayout struct {
	Pos       [][2]float64   `json:"pos"`    // top-left of each node box
	Routes    [][][2]float64 `json:"routes"` // per link: points from the output port to the input port
	Layers    int            `json:"layers"`
	Crossings int            `json:"crossings"`
	Reversed  int            `json:"reversed"` // back edges (cycles)
}

type lnode struct {
	real  int // Graph.Nodes position, -1 for a dummy
	layer int
	order int
	y     float64
	h     float64
}

// Layout computes the layered layout of g: each connected part on its own (layoutOne), the parts packed in rows,
// biggest first, so a graph of many small entry chains comes out wide rather than one tall column.
func Layout(g *Graph) *GraphLayout {
	n := len(g.Nodes)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for _, l := range g.Links {
		if l.From >= 0 && l.To >= 0 && l.From < n && l.To < n {
			parent[find(l.From)] = find(l.To)
		}
	}
	comp := map[int][]int{}
	var roots []int
	for i := range n {
		r := find(i)
		if comp[r] == nil {
			roots = append(roots, r)
		}
		comp[r] = append(comp[r], i)
	}
	if len(roots) <= 1 {
		return layoutOne(g)
	}
	out := &GraphLayout{Pos: make([][2]float64, n), Routes: make([][][2]float64, len(g.Links))}
	type part struct {
		nodes, links []int
		l            *GraphLayout
		w, h         float64
	}
	parts := make([]*part, 0, len(roots))
	where := make([]int, n) // node → index in its part
	byRoot := map[int]*part{}
	for _, r := range roots {
		pt := &part{nodes: comp[r]}
		for k, i := range pt.nodes {
			where[i] = k
		}
		byRoot[r] = pt
		parts = append(parts, pt)
	}
	for k, l := range g.Links {
		if l.From >= 0 && l.To >= 0 && l.From < n && l.To < n {
			pt := byRoot[find(l.From)]
			pt.links = append(pt.links, k)
		}
	}
	area := 0.0
	for _, pt := range parts {
		sub := &Graph{}
		for _, i := range pt.nodes {
			sub.Nodes = append(sub.Nodes, g.Nodes[i])
		}
		for _, k := range pt.links {
			l := g.Links[k]
			l.From, l.To = where[l.From], where[l.To]
			sub.Links = append(sub.Links, l)
		}
		pt.l = layoutOne(sub)
		for _, p := range pt.l.Pos {
			pt.w, pt.h = max(pt.w, p[0]+NodeW), max(pt.h, p[1]+NodeH)
		}
		for _, r := range pt.l.Routes {
			for _, q := range r {
				pt.h = max(pt.h, q[1])
			}
		}
		area += (pt.w + layerGap) * (pt.h + partGap)
		out.Crossings += pt.l.Crossings
		out.Reversed += pt.l.Reversed
		out.Layers = max(out.Layers, pt.l.Layers)
	}
	// rows ("shelves") about 1.8 × as wide as the whole is tall; tallest parts first
	sort.SliceStable(parts, func(a, b int) bool { return parts[a].h*parts[a].w > parts[b].h*parts[b].w })
	rowW := math.Sqrt(area * 1.8)
	x, y, rowH := 0.0, 0.0, 0.0
	for _, pt := range parts {
		if x > 0 && x+pt.w > rowW {
			x, y, rowH = 0, y+rowH+partGap, 0
		}
		for k, i := range pt.nodes {
			p := pt.l.Pos[k]
			out.Pos[i] = [2]float64{p[0] + x, p[1] + y}
		}
		for k, li := range pt.links {
			var r [][2]float64
			for _, q := range pt.l.Routes[k] {
				r = append(r, [2]float64{q[0] + x, q[1] + y})
			}
			out.Routes[li] = r
		}
		x += pt.w + layerGap
		rowH = max(rowH, pt.h)
	}
	return out
}

func layoutOne(g *Graph) *GraphLayout {
	n := len(g.Nodes)
	out := &GraphLayout{Pos: make([][2]float64, n), Routes: make([][][2]float64, len(g.Links))}
	if n == 0 {
		return out
	}
	// edges between distinct nodes (self-loops and duplicates are drawn but don't shape the layout)
	type edge struct{ a, b int }
	adj := make([][]int, n)
	seen := map[edge]bool{}
	var edges []edge
	for _, l := range g.Links {
		if l.From == l.To || l.From < 0 || l.To < 0 || l.From >= n || l.To >= n || seen[edge{l.From, l.To}] {
			continue
		}
		seen[edge{l.From, l.To}] = true
		edges = append(edges, edge{l.From, l.To})
		adj[l.From] = append(adj[l.From], l.To)
	}
	// 1. cycles: DFS from the entries first
	state := make([]int8, n) // 0 new, 1 on stack, 2 done
	back := map[edge]bool{}
	var dfs func(v int)
	dfs = func(v int) {
		state[v] = 1
		for _, w := range adj[v] {
			switch state[w] {
			case 0:
				dfs(w)
			case 1:
				back[edge{v, w}] = true
			}
		}
		state[v] = 2
	}
	roots := make([]int, 0, n)
	for i, nd := range g.Nodes {
		if nd.Kind == "Entry" {
			roots = append(roots, i)
		}
	}
	for i := range g.Nodes {
		roots = append(roots, i)
	}
	for _, r := range roots {
		if state[r] == 0 {
			dfs(r)
		}
	}
	out.Reversed = len(back)
	// the DAG: back edges reversed
	dag := make([][]int, n)
	indeg := make([]int, n)
	for _, e := range edges {
		a, b := e.a, e.b
		if back[e] {
			a, b = b, a
		}
		if seen[edge{a, b}] && back[e] { // a reversed edge duplicating a forward one
			continue
		}
		dag[a] = append(dag[a], b)
		indeg[b]++
	}
	// 2. layers: longest path (Kahn order)
	layer := make([]int, n)
	q := []int{}
	deg := append([]int(nil), indeg...)
	for i := range n {
		if deg[i] == 0 {
			q = append(q, i)
		}
	}
	for k := 0; k < len(q); k++ {
		v := q[k]
		for _, w := range dag[v] {
			layer[w] = max(layer[w], layer[v]+1)
			if deg[w]--; deg[w] == 0 {
				q = append(q, w)
			}
		}
	}
	// pull sources (other than entries) towards their first successor: shorter links
	for i := range n {
		if indeg[i] == 0 && g.Nodes[i].Kind != "Entry" && len(dag[i]) > 0 {
			m := math.MaxInt
			for _, w := range dag[i] {
				m = min(m, layer[w])
			}
			layer[i] = max(0, m-1)
		}
	}
	nl := 0
	for _, l := range layer {
		nl = max(nl, l+1)
	}
	out.Layers = nl
	// 3. dummies: the chain of layout nodes of every DAG edge
	nodes := make([]*lnode, n)
	layers := make([][]*lnode, nl)
	for i := range n {
		nodes[i] = &lnode{real: i, layer: layer[i], h: NodeH}
	}
	type chain struct {
		a, b  int
		nodes []*lnode // a, dummies…, b in layer order
	}
	chains := map[edge]*chain{}
	var allChains []*chain
	for a := range n {
		for _, b := range dag[a] {
			c := &chain{a: a, b: b, nodes: []*lnode{nodes[a]}}
			for l := layer[a] + 1; l < layer[b]; l++ {
				d := &lnode{real: -1, layer: l, h: dummyH}
				nodes = append(nodes, d)
				c.nodes = append(c.nodes, d)
			}
			c.nodes = append(c.nodes, nodes[b])
			chains[edge{a, b}] = c
			allChains = append(allChains, c)
		}
	}
	up := map[*lnode][]*lnode{}
	down := map[*lnode][]*lnode{}
	for _, c := range allChains {
		for k := 0; k+1 < len(c.nodes); k++ {
			down[c.nodes[k]] = append(down[c.nodes[k]], c.nodes[k+1])
			up[c.nodes[k+1]] = append(up[c.nodes[k+1]], c.nodes[k])
		}
	}
	for _, v := range nodes {
		layers[v.layer] = append(layers[v.layer], v)
	}
	// 4. ordering: initial order = BFS from the sources, then barycenter sweeps
	visited := map[*lnode]bool{}
	var bfs []*lnode
	for _, v := range layers[0] {
		if !visited[v] {
			visited[v] = true
			bfs = append(bfs, v)
		}
	}
	for k := 0; k < len(bfs); k++ {
		for _, w := range down[bfs[k]] {
			if !visited[w] {
				visited[w] = true
				bfs = append(bfs, w)
			}
		}
	}
	for _, v := range nodes {
		if !visited[v] {
			visited[v] = true
			bfs = append(bfs, v)
		}
	}
	rank := map[*lnode]int{}
	for i, v := range bfs {
		rank[v] = i
	}
	for _, L := range layers {
		sort.SliceStable(L, func(i, j int) bool { return rank[L[i]] < rank[L[j]] })
		for i, v := range L {
			v.order = i
		}
	}
	crossings := func() int {
		total := 0
		for l := 0; l+1 < nl; l++ {
			type seg struct{ a, b int }
			var segs []seg
			for _, v := range layers[l] {
				for _, w := range down[v] {
					segs = append(segs, seg{v.order, w.order})
				}
			}
			for i := range segs {
				for j := i + 1; j < len(segs); j++ {
					if (segs[i].a-segs[j].a)*(segs[i].b-segs[j].b) < 0 {
						total++
					}
				}
			}
		}
		return total
	}
	save := func() [][]*lnode {
		s := make([][]*lnode, nl)
		for l := range layers {
			s[l] = append([]*lnode(nil), layers[l]...)
		}
		return s
	}
	best, bestC := save(), crossings()
	sweep := func(l int, nb map[*lnode][]*lnode) {
		L := layers[l]
		bc := make(map[*lnode]float64, len(L))
		for _, v := range L {
			if ns := nb[v]; len(ns) > 0 {
				s := 0.0
				for _, w := range ns {
					s += float64(w.order)
				}
				bc[v] = s / float64(len(ns))
			} else {
				bc[v] = float64(v.order) // keeps its place
			}
		}
		sort.SliceStable(L, func(i, j int) bool { return bc[L[i]] < bc[L[j]] })
		for i, v := range L {
			v.order = i
		}
	}
	for it := 0; it < 12 && bestC > 0; it++ {
		for l := 1; l < nl; l++ {
			sweep(l, up)
		}
		for l := nl - 2; l >= 0; l-- {
			sweep(l, down)
		}
		if c := crossings(); c < bestC {
			best, bestC = save(), c
		}
	}
	layers = best
	for _, L := range layers {
		for i, v := range L {
			v.order = i
		}
	}
	out.Crossings = bestC
	// 5. y: stacked, then towards the neighbours' mean (alternating directions), overlaps pushed apart
	for _, L := range layers {
		y := 0.0
		for _, v := range L {
			v.y = y
			y += v.h + nodeGap
		}
	}
	center := func(v *lnode) float64 { return v.y + v.h/2 }
	place := func(L []*lnode, want map[*lnode]float64) {
		// forward pass: each at its wish, at least a gap below the previous
		for i, v := range L {
			y := want[v] - v.h/2
			if i > 0 {
				p := L[i-1]
				y = max(y, p.y+p.h+nodeGap)
			}
			v.y = y
		}
		// backward pass: pull the ones pushed down back up where the wish allows
		for i := len(L) - 2; i >= 0; i-- {
			v, nx := L[i], L[i+1]
			v.y = min(max(v.y, want[v]-v.h/2), nx.y-v.h-nodeGap)
		}
	}
	for it := 0; it < 6; it++ {
		for pass := range 2 {
			ls := make([]int, 0, nl)
			for l := range nl {
				ls = append(ls, l)
			}
			nb := up
			if pass == 1 {
				nb = down
				for i, j := 0, len(ls)-1; i < j; i, j = i+1, j-1 {
					ls[i], ls[j] = ls[j], ls[i]
				}
			}
			for _, l := range ls {
				want := map[*lnode]float64{}
				for _, v := range layers[l] {
					ns := nb[v]
					if len(ns) == 0 {
						want[v] = center(v)
						continue
					}
					s := 0.0
					for _, w := range ns {
						s += center(w)
					}
					want[v] = s / float64(len(ns))
				}
				place(layers[l], want)
			}
		}
	}
	// normalise: top at 0
	top := math.Inf(1)
	for _, v := range nodes {
		top = min(top, v.y)
	}
	x := func(l int) float64 { return float64(l) * (NodeW + layerGap) }
	for i := range n {
		v := nodes[i]
		out.Pos[i] = [2]float64{x(v.layer), math.Round(v.y - top)}
	}
	// routes: output port → dummy centres → input port (reversed edges run through their chain backwards)
	for k, l := range g.Links {
		a, b := l.From, l.To
		if a < 0 || b < 0 || a >= n || b >= n {
			continue
		}
		pa, pb := out.Pos[a], out.Pos[b]
		pts := [][2]float64{{pa[0] + NodeW, pa[1] + NodeH/2}}
		c, rev := chains[edge{a, b}], false
		if c == nil {
			c, rev = chains[edge{b, a}], true
		}
		if c != nil && a != b {
			mid := c.nodes[1 : len(c.nodes)-1]
			if rev {
				for i := len(mid) - 1; i >= 0; i-- {
					pts = append(pts, [2]float64{x(mid[i].layer) + NodeW/2, math.Round(mid[i].y - top + dummyH/2)})
				}
			} else {
				for _, d := range mid {
					pts = append(pts, [2]float64{x(d.layer) + NodeW/2, math.Round(d.y - top + dummyH/2)})
				}
			}
		}
		pts = append(pts, [2]float64{pb[0], pb[1] + NodeH/2})
		out.Routes[k] = pts
	}
	return out
}
