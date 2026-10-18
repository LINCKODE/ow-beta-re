// Package refindex is the explorer's general reference index: a directed graph over every resource of an
// install ("A uses B"), built by pluggable sources (STU fields, map data, asset packages, …; T063–T065 add
// textures, models, materials, audio) and cached on disk. Each edge carries the kinds of reference that link
// the two resources (a bit mask: a pair is stored once) and an optional aux value (e.g. the map record that
// places a definition). docs/Explorer.md "Reference index".
package refindex

import (
	"sort"

	"owcontent/content"
)

// Kind is an edge kind, an index into Index.Kinds (at most 32 kinds).
type Kind uint8

// KindInfo describes one kind of reference.
type KindInfo struct {
	Name   string `json:"name"`   // stable id, "<source>.<what>": "stu.field", "map.placement", …
	Label  string `json:"label"`  // for the UI: "STU field", "placement", …
	Source string `json:"source"` // Source.ID() that emits it
}

// Link is one neighbour of a resource.
type Link struct {
	GUID uint64
	Mask uint32 // kinds (bit k = Kind k)
	Aux  uint32 // source-specific (maps: record index + 1; 0 = none)
}

// Index is a built (or loaded) reference graph. It is immutable and safe for concurrent use.
type Index struct {
	Key   string     // cache key it was built for
	Kinds []KindInfo // edge kinds, by Kind
	Nodes []uint64   // every GUID that has an edge, sorted

	// Outgoing edges in CSR form (node i: [OutOff[i], OutOff[i+1])), targets as node indices, sorted.
	OutOff  []uint32
	OutTo   []uint32
	OutMask []uint32
	OutAux  []uint32

	// Incoming: rebuilt from the outgoing arrays at build/load (not cached). InEdge = index into Out*.
	inOff  []uint32
	inFrom []uint32
	inEdge []uint32

	Names []NameRec    // names found in the content
	Stats []SourceStat // per source
}

// NameRec is a name a source found for a resource.
type NameRec struct {
	GUID   uint64
	Text   string
	Source string
	Guess  bool
}

// SourceStat is what one source did.
type SourceStat struct {
	ID      string  `json:"id"`
	Scanned int     `json:"scanned"`
	Failed  int     `json:"failed"`
	Edges   int     `json:"edges"`
	Names   int     `json:"names"`
	Seconds float64 `json:"seconds"`
}

func (x *Index) node(g uint64) (int, bool) {
	i := sort.Search(len(x.Nodes), func(i int) bool { return x.Nodes[i] >= g })
	return i, i < len(x.Nodes) && x.Nodes[i] == g
}

// NumEdges is the number of (from, to) pairs.
func (x *Index) NumEdges() int { return len(x.OutTo) }

// Out lists what g uses.
func (x *Index) Out(g uint64) []Link {
	i, ok := x.node(g)
	if !ok {
		return nil
	}
	a, b := x.OutOff[i], x.OutOff[i+1]
	out := make([]Link, 0, b-a)
	for e := a; e < b; e++ {
		out = append(out, Link{x.Nodes[x.OutTo[e]], x.OutMask[e], x.OutAux[e]})
	}
	return out
}

// In lists what uses g.
func (x *Index) In(g uint64) []Link {
	i, ok := x.node(g)
	if !ok {
		return nil
	}
	a, b := x.inOff[i], x.inOff[i+1]
	out := make([]Link, 0, b-a)
	for k := a; k < b; k++ {
		e := x.inEdge[k]
		out = append(out, Link{x.Nodes[x.inFrom[k]], x.OutMask[e], x.OutAux[e]})
	}
	return out
}

// Degree is the number of distinct resources g uses (out) and that use it (in).
func (x *Index) Degree(g uint64) (out, in int) {
	i, ok := x.node(g)
	if !ok {
		return 0, 0
	}
	return int(x.OutOff[i+1] - x.OutOff[i]), int(x.inOff[i+1] - x.inOff[i])
}

// KindByName returns a kind's index.
func (x *Index) KindByName(name string) (Kind, bool) {
	for i, k := range x.Kinds {
		if k.Name == name {
			return Kind(i), true
		}
	}
	return 0, false
}

// MaskOf returns the mask of the named kinds (unknown names are ignored).
func (x *Index) MaskOf(names []string) uint32 {
	var m uint32
	for _, n := range names {
		if k, ok := x.KindByName(n); ok {
			m |= 1 << k
		}
	}
	return m
}

// KindNames lists the kinds of a mask.
func (x *Index) KindNames(mask uint32) []string {
	out := []string{}
	for k := range x.Kinds {
		if mask&(1<<k) != 0 {
			out = append(out, x.Kinds[k].Name)
		}
	}
	return out
}

// buildIn derives the incoming CSR from the outgoing one.
func (x *Index) buildIn() {
	n := len(x.Nodes)
	x.inOff = make([]uint32, n+1)
	for _, t := range x.OutTo {
		x.inOff[t+1]++
	}
	for i := 0; i < n; i++ {
		x.inOff[i+1] += x.inOff[i]
	}
	x.inFrom = make([]uint32, len(x.OutTo))
	x.inEdge = make([]uint32, len(x.OutTo))
	fill := append([]uint32(nil), x.inOff[:n]...)
	for from := 0; from < n; from++ { // ascending from: each in-list ends up sorted by source
		for e := x.OutOff[from]; e < x.OutOff[from+1]; e++ {
			t := x.OutTo[e]
			x.inFrom[fill[t]] = uint32(from)
			x.inEdge[fill[t]] = e
			fill[t]++
		}
	}
}

// ---- building ------------------------------------------------------------------------------------------

type rawEdge struct {
	from, to uint64
	mask     uint32
	aux      uint32
}

// assemble turns the collected edges into the CSR index.
func assemble(key string, kinds []KindInfo, edges []rawEdge, names []NameRec, stats []SourceStat) *Index {
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].from != edges[j].from {
			return edges[i].from < edges[j].from
		}
		return edges[i].to < edges[j].to
	})
	// merge duplicate pairs: OR the kinds, keep the first non-zero aux
	w := 0
	for _, e := range edges {
		if w > 0 && edges[w-1].from == e.from && edges[w-1].to == e.to {
			edges[w-1].mask |= e.mask
			if edges[w-1].aux == 0 {
				edges[w-1].aux = e.aux
			}
			continue
		}
		edges[w] = e
		w++
	}
	edges = edges[:w]
	nodes := make([]uint64, 0, 2*len(edges)/3)
	for _, e := range edges {
		nodes = append(nodes, e.from, e.to)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i] < nodes[j] })
	u := 0
	for i, g := range nodes {
		if i == 0 || g != nodes[u-1] {
			nodes[u] = g
			u++
		}
	}
	x := &Index{Key: key, Kinds: kinds, Nodes: nodes[:u], Names: names, Stats: stats}
	x.OutOff = make([]uint32, len(x.Nodes)+1)
	x.OutTo = make([]uint32, len(edges))
	x.OutMask = make([]uint32, len(edges))
	x.OutAux = make([]uint32, len(edges))
	for k, e := range edges {
		f, _ := x.node(e.from)
		t, _ := x.node(e.to)
		x.OutOff[f+1]++
		x.OutTo[k], x.OutMask[k], x.OutAux[k] = uint32(t), e.mask, e.aux
	}
	for i := range x.Nodes {
		x.OutOff[i+1] += x.OutOff[i]
	}
	x.buildIn()
	return x
}

// TypeOf is the resource type of a GUID.
func TypeOf(g uint64) uint32 { return content.TypeOf(g) }
