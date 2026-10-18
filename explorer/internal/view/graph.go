package view

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"owexplorer/internal/names"
	"owstu/stu"
)

// Statescript graph (docs/StatescriptRuntime.md §1.2–1.4): nodes in m_nodes order, links from
// m_graph.m_links (output plug → input plug), containers from m_graph.m_items.

var (
	tGraph       = uint32(0x0df7e24f) // STUStatescriptGraph
	tPlug        = uint32(0x84201dca) // STUGraphPlug
	tInputPlug   = uint32(0x11d7f052) // STUStatescriptInputPlug
	tContainer   = uint32(0x5dbbdd70) // STUGraphContainer
	tConfigVar   = names.Hash("STUConfigVar")
	tEntry       = uint32(0x2f937ae3)
	tCondition   = uint32(0x6f10080f)
	tTerminator  = uint32(0x15b44727)
	tAction      = uint32(0x2ecefc8b)
	tState       = uint32(0xa7213568)
	hGraph       = names.Hash("m_graph")
	hNodes       = names.Hash("m_nodes")
	hItems       = names.Hash("m_items")
	hLinks       = names.Hash("m_links")
	hPos         = names.Hash("m_pos")
	hUniqueID    = names.Hash("m_uniqueID")
	hOutputPlug  = names.Hash("m_outputPlug")
	hInputPlug   = names.Hash("m_inputPlug")
	hParentNode  = names.Hash("m_parentNode")
	hIsState     = names.Hash("m_isState")
	hClientOnly  = names.Hash("m_clientOnly")
	hServerOnly  = names.Hash("m_serverOnly")
	hNodeIndex   = uint32(0x53bfb779)
	hRemoteIndex = uint32(0x4b062861)
	hScope       = uint32(0xefd56e99)
	hStateIndex  = uint32(0x63d0b555)
	hChildren    = uint32(0xcceed0b9) // STUGraphContainer child items
	hStates      = names.Hash("m_states")
	hEntries     = names.Hash("m_entries")
	hSyncVars    = names.Hash("m_syncVars")
	hRemoteSync  = names.Hash("m_remoteSyncNodes")
)

// ErrNotGraph: the document's root is not an STUStatescriptGraph.
var ErrNotGraph = errors.New("not a Statescript graph")

type Plug struct {
	Name     string `json:"name"`
	In       bool   `json:"in"`
	Instance int    `json:"instance"`
	Type     string `json:"type"`
	Links    int    `json:"links"`
}

type Param struct {
	Name     string      `json:"name"`
	Type     string      `json:"type"`
	Instance int         `json:"instance"`
	Summary  string      `json:"summary"`
	Refs     []EntityRef `json:"refs"` // resources the config var references (links in the UI)
}

type GraphNode struct {
	Index      int     `json:"index"` // m_nodes index (the runtime's node id)
	Instance   int     `json:"instance"`
	Kind       string  `json:"kind"` // Entry Condition Action Terminator State Other
	Type       string  `json:"type"`
	TypeHash   uint32  `json:"typeHash"`
	Named      bool    `json:"named"`
	Short      string  `json:"short"` // type name without the kind prefix
	Display    string  `json:"display,omitempty"`
	Comment    string  `json:"comment,omitempty"`
	Debug      string  `json:"debug,omitempty"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	UniqueID   int64   `json:"uniqueId"`
	StateIndex int     `json:"stateIndex"` // m_states index, -1 if not a state
	RemoteSync int     `json:"remoteSync"` // m_remoteSyncNodes index, -1 if not synced
	Scope      int64   `json:"scope"`      // entity-admin scope enum (0 = owner EA)
	ClientOnly bool    `json:"clientOnly"`
	ServerOnly bool    `json:"serverOnly"`
	// Network side (docs/Explorer.md "Graph view: network side"): client (m_clientOnly), server (m_serverOnly, or a
	// type without a client class), both. NetWhy says which source decided; NetConflict when the sources disagree.
	Net         string  `json:"net"`
	NetWhy      string  `json:"netWhy"`
	NetConflict string  `json:"netConflict,omitempty"`
	Plugs       []Plug  `json:"plugs"`
	Params      []Param `json:"params"`
}

type GraphLink struct {
	From     int    `json:"from"` // node position in Graph.Nodes
	To       int    `json:"to"`
	FromPlug string `json:"fromPlug"`
	ToPlug   string `json:"toPlug"`
	Instance int    `json:"instance"`
}

type GraphContainer struct {
	Instance int     `json:"instance"`
	Type     string  `json:"type"`
	Title    string  `json:"title,omitempty"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Nodes    []int   `json:"nodes"`      // positions in Graph.Nodes (direct children)
	Children []int   `json:"containers"` // instances of nested containers
}

type Graph struct {
	Nodes      []GraphNode      `json:"nodes"`
	Links      []GraphLink      `json:"links"`
	Containers []GraphContainer `json:"containers"`
	States     int              `json:"states"`
	Entries    int              `json:"entries"`
	SyncVars   int              `json:"syncVars"`
	RemoteSync int              `json:"remoteSync"`
	Unlinked   int              `json:"unlinked"`         // links whose plugs could not be resolved to nodes
	Layout     *GraphLayout     `json:"layout,omitempty"` // layered layout (App.Graph; layout.go)
}

func kindOf(t uint32) string {
	switch {
	case stu.IsA(t, tState):
		return "State"
	case stu.IsA(t, tEntry):
		return "Entry"
	case stu.IsA(t, tCondition):
		return "Condition"
	case stu.IsA(t, tAction):
		return "Action"
	case stu.IsA(t, tTerminator):
		return "Terminator"
	}
	return "Other"
}

var kindPrefixes = []string{"STUStatescriptState", "STUStatescriptAction", "STUStatescriptCondition_",
	"STUStatescriptCondition", "STUStatescriptEntry", "STUStatescript", "STUConfigVar"}

// ShortType drops the kind prefix of a node / config-var type name.
func ShortType(t string) string {
	for _, p := range kindPrefixes {
		if strings.HasPrefix(t, p) && len(t) > len(p) {
			return strings.TrimPrefix(t[len(p):], "_")
		}
	}
	return t
}

func objIndex(v stu.Value) int {
	i, err := v.Object()
	if err != nil {
		return -1
	}
	return i
}

func instType(d *stu.Document, i int) uint32 {
	in, err := d.Instance(i)
	if err != nil {
		return 0
	}
	return in.Type
}

func str(v stu.Value) string { s, _ := v.Str(); return s }
func num(v stu.Value) int64  { x, _ := v.Int(); return x }
func flag(v stu.Value) bool  { x, err := v.Int(); return err == nil && x != 0 }
func count(v stu.Value) int  { return v.Len() }
func pos(v stu.Value) (float64, float64) {
	fs, err := v.Floats()
	if err != nil || len(fs) < 2 {
		return 0, 0
	}
	return float64(fs[0]), float64(fs[1])
}

// BuildGraph extracts the Statescript graph of a document whose root is an STUStatescriptGraph.
func BuildGraph(d *stu.Document, r Resolver) (*Graph, error) {
	root := d.Decode(d.Root())
	if !stu.IsA(root.TypeHash(), tGraph) {
		return nil, ErrNotGraph
	}
	g := &Graph{
		States: count(root.Field(hStates)), Entries: count(root.Field(hEntries)),
		SyncVars: count(root.Field(hSyncVars)), RemoteSync: count(root.Field(hRemoteSync)),
	}
	nodePos := map[int]int{} // instance → position in g.Nodes
	plugOwner := map[int]plugRef{}
	for _, it := range root.Field(hNodes).Items() {
		ni := objIndex(it)
		if ni < 0 {
			continue
		}
		v := d.Decode(ni)
		t := instType(d, ni)
		name, named := r.TypeName(t)
		n := GraphNode{
			Index: len(g.Nodes), Instance: ni, Kind: kindOf(t), Type: name, TypeHash: t, Named: named,
			Short: ShortType(name), Display: str(v.Field(hDisplayName)), Comment: str(v.Field(hComment)),
			Debug: str(v.Field(hDebugText)), UniqueID: num(v.Field(hUniqueID)), StateIndex: -1, RemoteSync: -1,
			Scope: num(v.Field(hScope)), ClientOnly: flag(v.Field(hClientOnly)), ServerOnly: flag(v.Field(hServerOnly)),
			Plugs: []Plug{}, Params: []Param{},
		}
		if idx := v.Field(hNodeIndex); idx.Valid() {
			n.Index = int(num(idx))
		}
		netSide(&n, r)
		if x := v.Field(hRemoteIndex); x.Valid() {
			n.RemoteSync = int(num(x))
		}
		if flag(v.Field(hIsState)) {
			n.StateIndex = int(num(v.Field(hStateIndex)))
		}
		n.X, n.Y = pos(v.Field(hPos))
		// Plugs and config vars: object fields (or object lists) by the target's type.
		for _, f := range v.Items() {
			fi := f.Info()
			fname, _ := r.TypeName(fi.FieldHash)
			var targets []stu.Value
			var labels []string
			switch fi.Type {
			case stu.Object:
				targets, labels = []stu.Value{f}, []string{fname}
			case stu.List:
				if fi.Kind == stu.KindObjectList {
					for k, e := range f.Items() {
						targets = append(targets, e)
						labels = append(labels, fname+"["+strconv.Itoa(k)+"]")
					}
				}
			}
			for k, tv := range targets {
				ti := objIndex(tv)
				if ti < 0 {
					continue
				}
				tt := instType(d, ti)
				tname, _ := r.TypeName(tt)
				switch {
				case stu.IsA(tt, tPlug):
					n.Plugs = append(n.Plugs, Plug{Name: plugName(labels[k]), In: stu.IsA(tt, tInputPlug), Instance: ti,
						Type: tname, Links: count(d.Decode(ti).Field(hLinks))})
					plugOwner[ti] = plugRef{len(g.Nodes), plugName(labels[k])}
				case stu.IsA(tt, tConfigVar):
					pr := Param{Name: labels[k], Type: ShortType(tname), Instance: ti, Summary: CVSummary(d, ti, r, 2),
						Refs: []EntityRef{}}
					collectRefs(d, d.Decode(ti), "", r, &pr.Refs, 0)
					n.Params = append(n.Params, pr)
				}
			}
		}
		nodePos[ni] = len(g.Nodes)
		g.Nodes = append(g.Nodes, n)
	}
	gv := root.Field(hGraph)
	for _, it := range gv.Field(hLinks).Items() {
		li := objIndex(it)
		if li < 0 {
			continue
		}
		lv := d.Decode(li)
		op, ip := objIndex(lv.Field(hOutputPlug)), objIndex(lv.Field(hInputPlug))
		from, okf := owner(d, op, plugOwner, nodePos)
		to, okt := owner(d, ip, plugOwner, nodePos)
		if !okf || !okt {
			g.Unlinked++
			continue
		}
		g.Links = append(g.Links, GraphLink{From: from.node, To: to.node, FromPlug: from.name, ToPlug: to.name, Instance: li})
	}
	for _, it := range gv.Field(hItems).Items() {
		ci := objIndex(it)
		if ci < 0 {
			continue
		}
		if _, isNode := nodePos[ci]; isNode {
			continue
		}
		t := instType(d, ci)
		if !stu.IsA(t, tContainer) {
			continue
		}
		v := d.Decode(ci)
		tname, _ := r.TypeName(t)
		c := GraphContainer{Instance: ci, Type: ShortType(tname), Title: str(v.Field(hDisplayName)), Nodes: []int{}, Children: []int{}}
		if c.Title == "" {
			c.Title = str(v.Field(hBodyText))
		}
		c.X, c.Y = pos(v.Field(hPos))
		for _, ch := range v.Field(hChildren).Items() {
			x := objIndex(ch)
			if p, ok := nodePos[x]; ok {
				c.Nodes = append(c.Nodes, p)
			} else if x >= 0 {
				c.Children = append(c.Children, x)
			}
		}
		g.Containers = append(g.Containers, c)
	}
	return g, nil
}

type plugRef struct {
	node int
	name string
}

// owner finds the node (position) and plug name of a plug instance: the node field that holds it, else
// the plug's m_parentNode.
func owner(d *stu.Document, plug int, owners map[int]plugRef, nodePos map[int]int) (plugRef, bool) {
	if o, ok := owners[plug]; ok {
		return o, true
	}
	if plug < 0 {
		return plugRef{}, false
	}
	parent := objIndex(d.Decode(plug).Field(hParentNode))
	if p, ok := nodePos[parent]; ok {
		return plugRef{p, "plug " + strconv.Itoa(plug)}, true
	}
	return plugRef{}, false
}

// plugName turns a field name into a plug label: m_onBeginPlug → onBegin, m_transitionPlug[0] → transition[0].
func plugName(field string) string {
	s := strings.TrimPrefix(field, "m_")
	if i := strings.Index(s, "Plug"); i > 0 {
		s = s[:i] + s[i+4:]
	}
	return s
}

// CVSummary is a one-line description of a config var instance (depth-limited).
func CVSummary(d *stu.Document, i int, r Resolver, depth int) string {
	t := instType(d, i)
	name, _ := r.TypeName(t)
	v := d.Decode(i)
	var parts []string
	for _, f := range v.Items() {
		fi := f.Info()
		if fi.FieldHash == 0x83e83924 { // evaluator slot, written at runtime
			continue
		}
		s := valueSummary(d, f, r, depth-1)
		if s == "" {
			continue
		}
		if v.Len() > 1 {
			fname, _ := r.TypeName(fi.FieldHash)
			s = strings.TrimPrefix(fname, "m_") + "=" + s
		}
		parts = append(parts, s)
		if len(parts) == 4 {
			parts = append(parts, "…")
			break
		}
	}
	return ShortType(name) + "(" + strings.Join(parts, ", ") + ")"
}

func valueSummary(d *stu.Document, v stu.Value, r Resolver, depth int) string {
	info := v.Info()
	switch info.Type {
	case stu.Null:
		return ""
	case stu.Primitive:
		s, _ := primitive(v, info.TypeHash)
		return s
	case stu.String:
		return strconv.Quote(clip(str(v), 40))
	case stu.Enum:
		return strconv.FormatInt(num(v), 10)
	case stu.Ref:
		if info.GUID == 0 {
			return ""
		}
		if n := r.GUIDName(info.GUID); n != "" {
			return strconv.Quote(clip(n, 40))
		}
		return fmt.Sprintf("%x", info.GUID)
	case stu.Object:
		if depth <= 0 {
			t, _ := r.TypeName(instType(d, info.Instance))
			return ShortType(t)
		}
		return CVSummary(d, info.Instance, r, depth)
	case stu.List:
		if info.Count == 0 {
			return ""
		}
		return "[" + strconv.Itoa(info.Count) + "]"
	case stu.Inline:
		t, _ := r.TypeName(info.TypeHash)
		return ShortType(t) + "{…}"
	case stu.Map:
		return "{" + strconv.Itoa(info.Count) + "}"
	}
	return ""
}

// netSide sets a node's network side from its flags and whether its type has a client class.
func netSide(n *GraphNode, r Resolver) {
	hasClient, known := true, false
	if cc, ok := r.(interface{ ClientClass(uint32) (bool, bool) }); ok {
		hasClient, known = cc.ClientClass(n.TypeHash)
		if !known {
			hasClient = true
		}
	}
	var why []string
	switch {
	case n.ClientOnly && n.ServerOnly:
		n.Net, n.NetConflict = "client", "both m_clientOnly and m_serverOnly are set"
		why = append(why, "m_clientOnly flag")
	case n.ClientOnly:
		n.Net = "client"
		why = append(why, "m_clientOnly flag")
		if known && !hasClient {
			n.NetConflict = "client-only flag, but the type has no client class"
		}
	case n.ServerOnly:
		n.Net = "server"
		why = append(why, "m_serverOnly flag")
		if known && !hasClient {
			why = append(why, "no client class")
		}
	case known && !hasClient:
		n.Net = "server"
		why = append(why, "the type has no client class (catalogue)")
	default:
		n.Net = "both"
		if known {
			why = append(why, "no flag; the type has a client class")
		} else {
			why = append(why, "no flag; type not in the catalogue")
		}
	}
	n.NetWhy = strings.Join(why, ", ")
}
