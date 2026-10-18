package catalog

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"owcontent/content"
	"owexplorer/internal/refindex"
)

// NeighborQuery asks for one side of a resource in the reference index: what it uses ("out") or what uses
// it ("in"), filtered by resource types, a package (neighbours listed by it), edge kinds and a search text.
type NeighborQuery struct {
	GUID    string   `json:"guid"`
	Dir     string   `json:"dir"` // "out" | "in"
	Types   []int    `json:"types"`
	Package string   `json:"package"`
	Kinds   []string `json:"kinds"`
	Search  string   `json:"search"`
	// NoMissing drops GUIDs that are not resources of the install (config-var ids and other non-resource ids).
	NoMissing bool `json:"noMissing"`
	Start     int  `json:"start"`
	Count     int  `json:"count"`
}

// NodeRow is a resource as the graph and the neighbour lists show it.
type NodeRow struct {
	GUID    string   `json:"guid"`
	Type    uint32   `json:"type"`
	Name    string   `json:"name,omitempty"`
	Guess   bool     `json:"guess,omitempty"`
	Note    string   `json:"note,omitempty"`
	Size    uint32   `json:"size"`
	Missing bool     `json:"missing,omitempty"` // not a resource of the install (e.g. a config-var id)
	Kinds   []string `json:"kinds,omitempty"`   // edge kinds to the queried resource
	Aux     int      `json:"aux,omitempty"`     // maps: record index + 1
	In      int      `json:"in"`                // resources using it
	Out     int      `json:"out"`               // resources it uses
}

type TypeCount struct {
	Type  uint32 `json:"type"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type KindCount struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// NeighborPage is one page of neighbours plus counts over the whole side.
type NeighborPage struct {
	Ready  bool        `json:"ready"` // the index is loaded
	All    int         `json:"all"`   // neighbours before filtering
	Total  int         `json:"total"` // after filtering
	Start  int         `json:"start"`
	ByType []TypeCount `json:"byType"` // over the kind/package/search-filtered side (type filter not applied)
	ByKind []KindCount `json:"byKind"` // over the unfiltered side
	Rows   []NodeRow   `json:"rows"`
}

// Node describes one resource for the graph.
func (c *Catalog) Node(g uint64) NodeRow {
	n := c.Name(g)
	row := NodeRow{GUID: fmt.Sprintf("%016x", g), Type: content.TypeOf(g), Name: n.Text, Guess: n.Guess, Note: n.Note}
	if r, ok := c.Find(g); ok {
		row.Size = r.Size
	} else {
		row.Missing = true
	}
	if x := c.Index(); x != nil {
		row.Out, row.In = x.Degree(g)
	}
	return row
}

var pkgSet struct {
	sync.Mutex
	guid uint64
	x    *refindex.Index
	set  map[uint64]bool
}

// packageMembers is the set of resources a package lists (cached for the last package asked).
func (c *Catalog) packageMembers(x *refindex.Index, pkg uint64) map[uint64]bool {
	pkgSet.Lock()
	defer pkgSet.Unlock()
	if pkgSet.guid == pkg && pkgSet.x == x && pkgSet.set != nil {
		return pkgSet.set
	}
	set := map[uint64]bool{}
	for _, r := range c.Store.PackageResources(pkg) {
		set[r.GUID] = true
	}
	pkgSet.guid, pkgSet.x, pkgSet.set = pkg, x, set
	return set
}

// Neighbors answers a NeighborQuery.
func (c *Catalog) Neighbors(q NeighborQuery) (NeighborPage, error) {
	p := NeighborPage{Start: q.Start, ByType: []TypeCount{}, ByKind: []KindCount{}, Rows: []NodeRow{}}
	g, ok := ParseGUID(q.GUID)
	if !ok {
		return p, fmt.Errorf("not a GUID: %q", q.GUID)
	}
	x := c.Index()
	if x == nil {
		return p, nil
	}
	p.Ready = true
	var links []refindex.Link
	if q.Dir == "in" {
		links = x.In(g)
	} else {
		links = x.Out(g)
	}
	p.All = len(links)
	kindCount := make([]int, len(x.Kinds))
	for _, l := range links {
		for k := range x.Kinds {
			if l.Mask&(1<<k) != 0 {
				kindCount[k]++
			}
		}
	}
	for k, n := range kindCount {
		if n > 0 {
			p.ByKind = append(p.ByKind, KindCount{x.Kinds[k].Name, x.Kinds[k].Label, n})
		}
	}
	mask := x.MaskOf(q.Kinds)
	if len(q.Kinds) == 0 {
		mask = ^uint32(0)
	}
	var members map[uint64]bool
	if q.Package != "" {
		if pg, ok := ParseGUID(q.Package); ok {
			members = c.packageMembers(x, pg)
		}
	}
	types := map[uint32]bool{}
	for _, t := range q.Types {
		types[uint32(t)] = true
	}
	search := strings.ToLower(strings.TrimSpace(q.Search))
	byType := map[uint32]int{}
	var keep []refindex.Link
	for _, l := range links {
		if l.Mask&mask == 0 || (members != nil && !members[l.GUID]) || (q.NoMissing && !c.Exists(l.GUID)) {
			continue
		}
		if search != "" && !strings.Contains(fmt.Sprintf("%016x", l.GUID), strings.TrimPrefix(search, "0x")) &&
			!strings.Contains(strings.ToLower(c.Name(l.GUID).Text), search) {
			continue
		}
		t := content.TypeOf(l.GUID)
		byType[t]++
		if len(types) > 0 && !types[t] {
			continue
		}
		keep = append(keep, l)
	}
	for t, n := range byType {
		p.ByType = append(p.ByType, TypeCount{t, c.TypeLabel(t), n})
	}
	sort.Slice(p.ByType, func(i, j int) bool { return p.ByType[i].Count > p.ByType[j].Count })
	sort.SliceStable(keep, func(i, j int) bool {
		ti, tj := content.TypeOf(keep[i].GUID), content.TypeOf(keep[j].GUID)
		if ti != tj {
			return ti < tj
		}
		return keep[i].GUID < keep[j].GUID
	})
	p.Total = len(keep)
	count := q.Count // 0 = 100, < 0 = counts only
	if count == 0 {
		count = 100
	}
	for k := max(0, q.Start); k < len(keep) && k < q.Start+count; k++ {
		row := c.Node(keep[k].GUID)
		row.Kinds = x.KindNames(keep[k].Mask)
		row.Aux = int(keep[k].Aux)
		p.Rows = append(p.Rows, row)
	}
	return p, nil
}

// KindRows lists the index's edge kinds.
func (c *Catalog) KindRows() []refindex.KindInfo {
	if x := c.Index(); x != nil {
		return x.Kinds
	}
	return []refindex.KindInfo{}
}

// IndexOf is the position of guid in the list q selects (-1 if it is not in it).
func (c *Catalog) IndexOf(q Query, guid uint64) int {
	i, ok := c.find(guid)
	if !ok {
		return -1
	}
	ids := c.match(q)
	k := sort.Search(len(ids), func(k int) bool { return int(ids[k]) >= i })
	if k < len(ids) && int(ids[k]) == i {
		return k
	}
	return -1
}

// TypeStats describes the sizes of one resource type.
type TypeStats struct {
	Type   uint32 `json:"type"`
	Label  string `json:"label"`
	Source string `json:"source"`
	STU    bool   `json:"stu"`
	Count  int    `json:"count"`
	Bytes  uint64 `json:"bytes"`
	Min    uint32 `json:"min"`
	Median uint32 `json:"median"`
	Max    uint32 `json:"max"`
}

var typeSizes sync.Map // *Catalog+type → TypeStats

// TypeStats of type t.
func (c *Catalog) TypeStats(t uint32) TypeStats {
	key := fmt.Sprintf("%p/%d", c, t)
	if v, ok := typeSizes.Load(key); ok {
		return v.(TypeStats)
	}
	st := TypeStats{Type: t, Label: "unknown"}
	if i, ok := c.typeIdx[t]; ok {
		row := c.types[i]
		st.Label, st.Source, st.STU, st.Count, st.Bytes = row.Label, row.Source, row.STU, row.Count, row.Bytes
	}
	var sizes []uint32
	for _, r := range c.res {
		if r.Type == t {
			sizes = append(sizes, r.Size)
		}
	}
	if len(sizes) > 0 {
		sort.Slice(sizes, func(i, j int) bool { return sizes[i] < sizes[j] })
		st.Min, st.Median, st.Max = sizes[0], sizes[len(sizes)/2], sizes[len(sizes)-1]
	}
	typeSizes.Store(key, st)
	return st
}
