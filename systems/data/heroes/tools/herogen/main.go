// Command herogen builds the per-hero Statescript start-up data of build 24919 offline (T040,
// docs/MatchLogic.md "Hero start-up").
//
// For every STUHero (0x02E...) it reads the gameplay entity definition (m_gameplayEntity, 0x040...)
// and from it:
//   - the STUStatescriptComponent's graphs with their STUStatescriptGraphWithOverrides values,
//   - the STUWeaponComponent's manager script and weapon scripts (weapon index = instance var 0x1c),
//
// and for every graph (and every SubScript child a start state plays, recursively) its sync layout
// (m_syncVars slots, eligible m_remoteSyncNodes, the m_states kinds of the local format), its
// m_publicSchema defaults and its start states (states a plain entry links to directly). It writes:
//
//	systems/data/heroes/heroes.json         everything, readable
//	server/internal/heroes/heroes_gen.go    the same tables for the game server
//
// Build systems/content and systems/stu first (cmake, see their READMEs), then:
//
//	cd systems/data/heroes/tools/herogen && go run .        # or: go run . -install DIR
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"hash/crc32"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	"owcontent/content"
	"owstu/stu"
)

// hash is the STU type / field hash of a name: CRC32 of the lowercased name.
func hash(name string) uint32 { return crc32.ChecksumIEEE([]byte(strings.ToLower(name))) }

var (
	tState      = hash("STUStatescriptState")
	tEntry      = hash("STUStatescriptEntry") // plain entries only: exact type
	tSubScript  = hash("STUStatescriptStateSubScript")
	tComponent  = hash("STUStatescriptComponent")
	tWeapons    = hash("STUWeaponComponent")
	tSetVar     = hash("STUStatescriptActionSetVar")
	tBoolSwitch = hash("STUStatescriptStateBooleanSwitch")
	tNoOp       = hash("STUStatescriptActionNoOp")
	tCondBool   = uint32(0xb1032daf) // STUStatescriptCondition_Bool
	tAbility    = hash("STUStatescriptStateAbility")
	tStack      = hash("STUStatescriptStateStack")
	tWait       = hash("STUStatescriptStateWait")
)

// State types whose local-format sync has a "has update" bit first (FUN_7ff650ecfee0 = 1;
// docs/MatchLogic.md §9). Exact types, as the client compares them.
var updateBitStates = []uint32{0x7bf31629, 0x860ee5fc, 0xffbfa88d}

// Statescript primitive types (docs/StatescriptRuntime.md §1.7).
const (
	pByte   = 1
	pInt    = 2
	pFloat  = 3
	pInt64  = 4
	pString = 5
)

func main() {
	root := repoRoot()
	install := flag.String("install", filepath.Join(root, "game/owbetaclient"), "0.8 install directory")
	outJSON := flag.String("json", filepath.Join(root, "systems/data/heroes/heroes.json"), "output")
	outGo := flag.String("go", filepath.Join(root, "server/internal/heroes/heroes_gen.go"), "Go tables output")
	strs := flag.String("strings", filepath.Join(root, "systems/data/lobby_scripts/strings_enUS.tsv"), "string table (GUID<TAB>text)")
	flag.Parse()

	names, err := loadStrings(*strs)
	if err != nil {
		log.Fatal(err)
	}
	s, err := content.Open(*install, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	g := &gen{s: s, names: names, graphs: map[uint32]*Graph{}}

	var heroes []*Hero
	for _, r := range s.ResourcesOfType(content.TypeOf(0x02E0000000000000)) {
		h, err := g.hero(r.GUID)
		if err != nil {
			log.Fatalf("hero %#016x: %v", r.GUID, err)
		}
		if h == nil {
			continue
		}
		heroes = append(heroes, h)
	}
	sort.Slice(heroes, func(i, j int) bool { return heroes[i].Hero < heroes[j].Hero })
	var graphs []*Graph
	for _, gr := range g.graphs {
		graphs = append(graphs, gr)
	}
	sort.Slice(graphs, func(i, j int) bool { return graphs[i].Index < graphs[j].Index })
	for _, h := range heroes {
		var gl []string
		for _, u := range h.Graphs {
			gl = append(gl, fmt.Sprintf("%#x", u.Graph))
		}
		log.Printf("%#016x %-12q def %#05x  %d graphs %v", uint64(h.Hero), h.Name, uint32(h.Def), len(h.Graphs), gl)
	}

	b, err := json.MarshalIndent(catalogue{Source: "systems/data/heroes/tools/herogen (T040); docs/MatchLogic.md \"Hero start-up\"", Heroes: heroes, Graphs: graphs}, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(*outJSON), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*outJSON, append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(*outGo), 0o755); err != nil {
		log.Fatal(err)
	}
	src, err := format.Source(goTables(heroes, graphs))
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*outGo, src, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s and %s (%d heroes, %d graphs)", *outJSON, *outGo, len(heroes), len(graphs))
}

// repoRoot is new-stuff/, found from this file's location (go run keeps it).
func repoRoot() string {
	_, f, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(f), "../../../../.."))
}

func loadStrings(path string) (map[uint64]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := map[uint64]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "\t")
		if !ok {
			continue
		}
		if g, err := strconv.ParseUint(strings.TrimPrefix(k, "0x"), 16, 64); err == nil {
			m[g] = v
		}
	}
	return m, sc.Err()
}

// --- output -------------------------------------------------------------------------------------

type catalogue struct {
	Source string   `json:"source"`
	Heroes []*Hero  `json:"heroes"`
	Graphs []*Graph `json:"graphs"`
}

type hex64 uint64

func (h hex64) MarshalJSON() ([]byte, error) { return fmt.Appendf(nil, "\"0x%016x\"", uint64(h)), nil }

type hex32 uint32

func (h hex32) MarshalJSON() ([]byte, error) { return fmt.Appendf(nil, "\"0x%x\"", uint32(h)), nil }

// Hero is one STUHero and the Statescript instances its gameplay entity starts with.
type Hero struct {
	Hero   hex64    `json:"hero"` // STUHero 0x02E...
	Name   string   `json:"name"`
	Def    hex32    `json:"def"` // gameplay entity definition 0x0400000000000000|Def
	Graphs []*Use   `json:"graphs"`
	Notes  []string `json:"notes,omitempty"`
}

// Use is a graph instantiated on the hero: a definition graph (with its overrides) or a weapon script.
type Use struct {
	Graph     hex32  `json:"graph"`            // 0x0580000000000000|Graph
	Source    string `json:"source"`           // "component" (STUStatescriptComponent), "manager", "weapon"
	Weapon    int    `json:"weapon,omitempty"` // weapon scripts: the weapon index (instance var 0x1c), 1-based
	Overrides []Var  `json:"overrides,omitempty"`
}

// Var is a variable value: var 0x0D80000000000000|ID.
type Var struct {
	ID    hex32   `json:"id"`
	Type  int     `json:"type"` // primitive type: 1 BYTE, 2 INT, 3 FLT, 4 INT64, 5 STRING
	Int   int64   `json:"int,omitempty"`
	Float float32 `json:"float,omitempty"`
	Str   string  `json:"str,omitempty"`
	From  string  `json:"from"` // the config var type it came from
}

// Graph is a Statescript graph's sync layout, schema and start.
type Graph struct {
	Index hex32 `json:"graph"`
	// SyncVars: m_syncVars (graph +0x130) slots in order; eligible ones (id != 0, kind 0) are the
	// changed bits of the sync var section, in this order.
	SyncVars []hex32 `json:"sync_vars"`
	// RemoteNodes: eligible m_remoteSyncNodes (states, neither client- nor server-only).
	RemoteNodes int `json:"remote_nodes"`
	// RemoteStates: m_states index of each eligible remote node, in update order.
	RemoteStates []int `json:"remote_states"`
	// States: m_states kinds as the local format sees them ('.' synced, 'C' client-only, 'V'
	// server-only, 'S' has-update type).
	States string `json:"states"`
	// StateTypes: the STU type of each m_states entry (the state data format depends on it).
	StateTypes []hex32 `json:"state_types"`
	Schema     []Var   `json:"schema"`
	// Start: states the plain entries begin (through SetVars, NoOps and conditions), by m_states index.
	Start []int `json:"start"`
	// Entry: what the plain entries run, in order (programs, see Step).
	Entry []Step `json:"entry,omitempty"`
	// Progs: the subgraph programs of states, by state index.
	Progs map[string]StateProg `json:"progs,omitempty"`
	// Conds: the m_condition of each BooleanSwitch start state the server can evaluate from the
	// start-up's variables (its state data is the condition's value), by state index.
	Conds map[string]Set `json:"conds,omitempty"`
	// Unencodable: states whose state data depends on a runtime value the start-up doesn't have: an
	// Ability / Stack whose priority config var (0x92c13ac6 / m_priority) is set and not a
	// STUConfigVarStackPriority (the data then carries the priority, 32 bits + 1). None in 0.8.
	Unencodable []int `json:"unencodable,omitempty"`
	// Children: the graph a SubScript start state plays (m_statescriptGraph literal), by state index.
	Children map[string]Child `json:"children,omitempty"`
	// Unresolved: what the plain entries link to that is not a state (actions, conditions: run by the
	// runtime, not modelled here), by node type.
	Unresolved []string `json:"unresolved,omitempty"`
	// Assumed: branches taken on an assumption (conditions the generator cannot evaluate).
	Assumed []string `json:"assumed,omitempty"`
	Notes   []string `json:"notes,omitempty"`
}

// Step is one thing a firing plug does: begin a state (State, m_states index), a SetVar, or a
// Condition_Bool (Cond) with the steps of its true and false plugs.
type Step struct {
	Kind  string `json:"kind"` // "begin", "set", "cond"
	State int    `json:"state,omitempty"`
	Set   *Set   `json:"set,omitempty"`
	Cond  *Set   `json:"cond,omitempty"`
	True  []Step `json:"true,omitempty"`
	False []Step `json:"false,omitempty"`
}

// StateProg: the programs a state's subgraph plugs run when it begins.
type StateProg struct {
	Sub   []Step `json:"sub,omitempty"`   // m_subgraphPlug (every state)
	True  []Step `json:"true,omitempty"`  // BooleanSwitch m_trueSubgraphPlug (value true)
	False []Step `json:"false,omitempty"` // BooleanSwitch m_falseSubgraphPlug (value false)
	Top   []Step `json:"top,omitempty"`   // Stack m_topSubgraphPlug (on top of its priority stack)
	// Finished: a Wait with a literal timeout of at most settleWait seconds is shown finished in
	// the start-up snapshot: its m_onFinishedPlug program runs instead of the Wait being active [?].
	Finished []Step `json:"finished,omitempty"`
	Settle   bool   `json:"settle,omitempty"`
}

// settleWait: the longest literal Wait timeout the start-up settles (seconds).
const settleWait = 0.25

// Set is an entry's SetVar: target var (instance bag, or the entity bag if Owner) = a literal value
// or the value of another var (From, in the instance bag or FromOwner the entity bag).
type Set struct {
	ID        hex32 `json:"id"`
	Owner     bool  `json:"owner,omitempty"`
	Value     *Var  `json:"value,omitempty"`
	From      hex32 `json:"from,omitempty"`
	FromOwner bool  `json:"from_owner,omitempty"`
	Expr      *Expr `json:"expr,omitempty"` // conditions: an expression over literals and plain vars
	// StackFree: condition 0xc81f8768 ("this priority may take the stack slot"): true at start-up,
	// when no Stack holds the slot yet [?].
	StackFree bool `json:"stack_free,omitempty"`
}

// Expr is a STUConfigVarExpression / ExpressionTyped whose operands the server can read: the
// bytecode (docs/StatescriptConfigVars.md §3), its dynamic vars as plain var paths, its float
// constants and its config-var operands as literals.
type Expr struct {
	Ops    []int     `json:"ops"`
	Vars   []Set     `json:"vars,omitempty"` // From / FromOwner of each m_dynamicVars entry
	Consts []float32 `json:"consts,omitempty"`
	CVs    []Var     `json:"cvs,omitempty"`
}

// Child is the graph a SubScript state plays and the state's data flag.
type Child struct {
	Graph hex32 `json:"graph"`
	Flag  bool  `json:"flag,omitempty"`
	// Init: the SubScript's init data (0x7eb65d0d, STUStatescriptSubScriptInitData {m_out_Var, m_value}):
	// child var ID = the parent's var From (or a literal Value), set when the child starts.
	Init []Set `json:"init,omitempty"`
}

// --- generation ---------------------------------------------------------------------------------

type gen struct {
	s      *content.Store
	names  map[uint64]string
	graphs map[uint32]*Graph
}

// doc is a decoded STU document: instance values (JSON) and types.
type doc struct {
	vals  []map[string]any
	types []uint32
}

func (g *gen) load(guid uint64) (*doc, error) {
	b, err := g.s.Read(guid)
	if err != nil {
		return nil, err
	}
	d, err := stu.Open(b, stu.File, 0)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	var raw struct {
		Root []struct {
			Value map[string]any `json:"value"`
		} `json:"root"`
	}
	if err := json.Unmarshal([]byte(d.JSON(false)), &raw); err != nil {
		return nil, err
	}
	out := &doc{}
	for i, in := range d.Instances() {
		out.types = append(out.types, in.Type)
		var v map[string]any
		if i < len(raw.Root) {
			v = raw.Root[i].Value
		}
		out.vals = append(out.vals, v)
	}
	return out, nil
}

func fields(v any) map[string]any {
	m, _ := v.(map[string]any)
	f, _ := m["$fields"].(map[string]any)
	return f
}

func typeName(v any) string {
	m, _ := v.(map[string]any)
	t, _ := m["$type"].(string)
	return t
}

// inst returns the instance index of a {"$instance": n} value, or -1.
func inst(v any) int {
	m, ok := v.(map[string]any)
	if !ok {
		return -1
	}
	n, ok := m["$instance"].(float64)
	if !ok {
		return -1
	}
	return int(n)
}

func (d *doc) deref(v any) (any, uint32) {
	if i := inst(v); i >= 0 && i < len(d.vals) {
		return d.vals[i], d.types[i]
	}
	return v, 0
}

func guidOf(v any) uint64 {
	m, _ := v.(map[string]any)
	s, _ := m["$guid"].(string)
	g, _ := strconv.ParseUint(s, 0, 64)
	return g
}

func num(v any) float64 { f, _ := v.(float64); return f }

func list(v any) []any { l, _ := v.([]any); return l }

func (g *gen) hero(guid uint64) (*Hero, error) {
	d, err := g.load(guid)
	if err != nil {
		return nil, err
	}
	f := fields(d.vals[0])
	def := guidOf(f["m_gameplayEntity"])
	if def == 0 {
		return nil, nil
	}
	h := &Hero{Hero: hex64(guid), Def: hex32(def)}
	// The name: the first string reference of the hero the string table knows (m_name has no
	// OWLib name in this build; field 0x... ).
	for _, k := range sortedKeys(f) {
		if n, ok := g.names[guidOf(f[k])]; ok && n != "" {
			h.Name = n
			break
		}
	}
	ed, err := g.load(def)
	if err != nil {
		return nil, fmt.Errorf("definition %#016x: %w", def, err)
	}
	cm, _ := fields(ed.vals[0])["m_componentMap"].(map[string]any)
	var comp, weapons any
	for _, c := range cm {
		v, t := ed.deref(c)
		switch t {
		case tComponent:
			comp = v
		case tWeapons:
			weapons = v
		}
	}
	if comp != nil {
		for _, gw := range list(fields(comp)["0x0175ad85"]) {
			gf := fields(gw)
			u := &Use{Graph: hex32(guidOf(gf["m_graph"])), Source: "component"}
			for _, e := range list(gf["0x953d7942"]) {
				ev, _ := ed.deref(e)
				v, err := g.configVar(ed, fields(ev))
				if err != nil {
					h.Notes = append(h.Notes, fmt.Sprintf("graph %#x override: %v", uint32(u.Graph), err))
					continue
				}
				u.Overrides = append(u.Overrides, v)
			}
			if err := g.graph(uint32(u.Graph), 0); err != nil {
				return nil, err
			}
			h.Graphs = append(h.Graphs, u)
		}
	}
	if weapons != nil {
		wf := fields(weapons)
		if m := guidOf(wf["m_managerScript"]); m != 0 {
			h.Graphs = append(h.Graphs, &Use{Graph: hex32(m), Source: "manager"})
			if err := g.graph(uint32(m), 0); err != nil {
				return nil, err
			}
		}
		// Weapon index: counts only weapons with a script (Prometheus player_spawner::attach_weapons).
		idx := 1
		for _, w := range list(wf["m_weapons"]) {
			ww := fields(w)
			if gg := guidOf(fields(ww["m_graph"])["m_graph"]); gg != 0 {
				h.Notes = append(h.Notes, fmt.Sprintf("weapon m_graph %#x not instantiated (unused by every 0.8 hero so far)", gg))
			}
			sc := guidOf(ww["m_script"])
			if sc == 0 {
				continue
			}
			h.Graphs = append(h.Graphs, &Use{Graph: hex32(sc), Source: "weapon", Weapon: idx})
			idx++
			if err := g.graph(uint32(sc), 0); err != nil {
				return nil, err
			}
		}
	}
	return h, nil
}

// configVar evaluates a STUStatescriptSchemaEntry {0x4f6799b5 var id, m_value config var}: the literal
// config var types only (StatescriptConfigVarCatalogue.md: Int → INT, Bool → BYTE, Float → FLT,
// String → STRING, asset references → INT64 GUID). A null config var yields nothing and the client
// stores the pre-initialised INT 0 (StatescriptRuntime.md §5.3).
func (g *gen) configVar(d *doc, e map[string]any) (Var, error) {
	id := guidOf(e["0x4f6799b5"])
	if id>>52 != 0x0d8 {
		return Var{}, fmt.Errorf("var id %#x is not a 0x0D8 identifier", id)
	}
	v := Var{ID: hex32(id)}
	cv, _ := d.deref(e["m_value"])
	if cv == nil {
		v.Type, v.From = pInt, "null"
		return v, nil
	}
	t := typeName(cv)
	v.From = t
	cf := fields(cv)
	switch t {
	case "STUConfigVarInt":
		v.Type, v.Int = pInt, int64(num(cf["m_value"]))
	case "STUConfigVarBool":
		v.Type, v.Int = pByte, int64(num(cf["m_value"]))
	case "STUConfigVarFloat":
		v.Type, v.Float = pFloat, float32(num(cf["m_value"]))
	case "STUConfigVarString":
		v.Type = pString
		v.Str, _ = cf["m_value"].(string)
	default:
		// Asset references: the one GUID field (cv+0x18).
		var refs []uint64
		n := 0
		for k, x := range cf {
			if k == "0x83e83924" {
				continue
			}
			n++
			if m, ok := x.(map[string]any); ok {
				if _, ok := m["$guid"]; ok {
					refs = append(refs, guidOf(x))
				}
			}
		}
		if len(refs) != 1 || n != 1 {
			return Var{}, fmt.Errorf("var %#x: config var %s is not a literal this generator knows", id&0xffffffff, t)
		}
		v.Type, v.Int = pInt64, int64(refs[0])
	}
	return v, nil
}

// graph reads a graph's layout, schema and start once (and its SubScript children, recursively).
func (g *gen) graph(idx uint32, depth int) error {
	if _, ok := g.graphs[idx]; ok {
		return nil
	}
	guid := 0x0580000000000000 | uint64(idx)
	d, err := g.load(guid)
	if err != nil {
		return fmt.Errorf("graph %#016x: %w", guid, err)
	}
	gr := &Graph{Index: hex32(idx), SyncVars: []hex32{}, Schema: []Var{}, Start: []int{}, RemoteStates: []int{}}
	g.graphs[idx] = gr
	f := fields(d.vals[0])

	for _, sv := range list(f["m_syncVars"]) {
		sf := fields(sv)
		id := guidOf(sf["0x4f6799b5"])
		kind, _ := strconv.ParseUint(fmt.Sprint(sf["0x9e2a316c"]), 0, 32)
		if id == 0 || kind != 0 {
			continue
		}
		gr.SyncVars = append(gr.SyncVars, hex32(id))
	}

	stateIndex := map[int]int{} // instance → m_states index
	gr.StateTypes = []hex32{}
	var states strings.Builder
	for si, s := range list(f["m_states"]) {
		i := inst(s)
		stateIndex[i] = si
		gr.StateTypes = append(gr.StateTypes, hex32(d.types[i]))
		sf := fields(d.vals[i])
		switch {
		case num(sf["m_clientOnly"]) != 0:
			states.WriteByte('C')
		case num(sf["m_serverOnly"]) != 0:
			states.WriteByte('V')
		case slices.Contains(updateBitStates, d.types[i]):
			states.WriteByte('S')
		default:
			states.WriteByte('.')
		}
	}
	gr.States = states.String()
	for i, si := range stateIndex {
		field := map[uint32]string{tAbility: "0x92c13ac6", tStack: "m_priority"}[d.types[i]]
		if field == "" {
			continue
		}
		if cv, _ := d.deref(fields(d.vals[i])[field]); cv != nil && typeName(cv) != "0x2adc2e14" {
			gr.Unencodable = append(gr.Unencodable, si)
			gr.Notes = append(gr.Notes, fmt.Sprintf("state %d: %s is %s: its state data carries the priority value", si, field, typeName(cv)))
		}
	}
	slices.Sort(gr.Unencodable)

	for _, n := range list(f["m_remoteSyncNodes"]) {
		i := inst(n)
		if i < 0 || !stu.IsA(d.types[i], tState) {
			continue
		}
		nf := fields(d.vals[i])
		if num(nf["m_clientOnly"]) != 0 || num(nf["m_serverOnly"]) != 0 {
			continue
		}
		gr.RemoteNodes++
		gr.RemoteStates = append(gr.RemoteStates, stateIndex[i])
	}

	if ps, _ := d.deref(f["m_publicSchema"]); ps != nil {
		for _, e := range list(fields(ps)["m_entries"]) {
			ev, _ := d.deref(e)
			v, err := g.configVar(d, fields(ev))
			if err != nil {
				gr.Notes = append(gr.Notes, "schema: "+err.Error())
				continue
			}
			gr.Schema = append(gr.Schema, v)
		}
	}

	// Input plug instance → the state whose m_beginPlug it is.
	begin := map[int]int{}
	for i, si := range stateIndex {
		if p := inst(fields(d.vals[i])["m_beginPlug"]); p >= 0 {
			begin[p] = si
		}
	}
	// Programs (docs/MatchLogic.md §14.7): what a plug does when it fires, as the server's runtime
	// would run it, in link order, depth first (TraverseFrom): begin a state; a SetVar whose source and
	// target are literals or plain var paths; a Condition_Bool whose condition the server can evaluate
	// (both branches kept, chosen at start-up), one it can't is taken as false [?]; NoOps are followed.
	// Anything else (other actions) needs the runtime and ends that path. Client-only nodes are
	// skipped (the server doesn't run them). Entries are programs; so are the subgraph plugs of states
	// (m_subgraphPlug of every state, a BooleanSwitch's true / false subgraph by its value, a Stack's
	// top subgraph), which fire when the state begins.
	unresolved := map[string]int{}
	var prog func(plug int, path map[int]bool) []Step
	prog = func(plug int, path map[int]bool) []Step {
		steps := []Step{}
		if plug < 0 {
			return steps
		}
		for _, l := range list(fields(d.vals[plug])["m_links"]) {
			in := inst(fields(d.vals[inst(l)])["m_inputPlug"])
			if si, ok := begin[in]; ok {
				steps = append(steps, Step{Kind: "begin", State: si})
				continue
			}
			node := inst(fields(d.vals[in])["m_parentNode"])
			if node < 0 || path[node] || len(path) > 64 {
				continue
			}
			nf := fields(d.vals[node])
			if num(nf["m_clientOnly"]) != 0 {
				continue
			}
			path[node] = true
			switch d.types[node] {
			case tCondBool:
				cv, _ := d.deref(nf["m_condition"])
				if c, err := g.cond(d, nf["m_condition"]); err == nil {
					steps = append(steps, Step{Kind: "cond", Cond: &c, True: prog(inst(nf["m_truePlug"]), path), False: prog(inst(nf["m_falsePlug"]), path)})
				} else {
					gr.Assumed = append(gr.Assumed, fmt.Sprintf("Condition_Bool node %d (%s) taken as false", int(num(nf["0x53bfb779"])), typeName(cv)))
					steps = append(steps, prog(inst(nf["m_falsePlug"]), path)...)
				}
			case tNoOp:
				steps = append(steps, prog(inst(nf["m_outPlug"]), path)...)
			case tSetVar:
				if sv, err := g.setVar(d, nf); err == nil {
					steps = append(steps, Step{Kind: "set", Set: &sv})
					steps = append(steps, prog(inst(nf["m_outPlug"]), path)...)
				} else {
					gr.Notes = append(gr.Notes, "SetVar: "+err.Error())
					unresolved[stu.NameOr(d.types[node])]++
				}
			default:
				unresolved[stu.NameOr(d.types[node])]++
			}
			delete(path, node)
		}
		return steps
	}
	for _, e := range list(f["m_entries"]) {
		i := inst(e)
		if i < 0 || d.types[i] != tEntry {
			continue
		}
		ef := fields(d.vals[i])
		if num(ef["m_clientOnly"]) != 0 {
			continue
		}
		gr.Entry = append(gr.Entry, prog(inst(ef["m_outPlug"]), map[int]bool{})...)
	}
	for i, si := range stateIndex {
		nf := fields(d.vals[i])
		sp := StateProg{Sub: prog(inst(nf["m_subgraphPlug"]), map[int]bool{})}
		switch d.types[i] {
		case tBoolSwitch:
			sp.True = prog(inst(nf["m_trueSubgraphPlug"]), map[int]bool{})
			sp.False = prog(inst(nf["m_falseSubgraphPlug"]), map[int]bool{})
			if c, err := g.cond(d, nf["m_condition"]); err == nil {
				if gr.Conds == nil {
					gr.Conds = map[string]Set{}
				}
				gr.Conds[strconv.Itoa(si)] = c
			} else {
				gr.Notes = append(gr.Notes, fmt.Sprintf("BooleanSwitch state %d: %v", si, err))
			}
		case tStack:
			sp.Top = prog(inst(nf["m_topSubgraphPlug"]), map[int]bool{})
		case tWait:
			if cv, _ := d.deref(nf["m_timeout"]); typeName(cv) == "STUConfigVarFloat" && num(fields(cv)["m_value"]) <= settleWait {
				sp.Finished = prog(inst(nf["m_onFinishedPlug"]), map[int]bool{})
				sp.Settle = true
			}
		}
		if len(sp.Sub)+len(sp.True)+len(sp.False)+len(sp.Top)+len(sp.Finished) > 0 || sp.Settle {
			if gr.Progs == nil {
				gr.Progs = map[string]StateProg{}
			}
			gr.Progs[strconv.Itoa(si)] = sp
		}
	}
	for _, k := range sortedKeys(unresolved) {
		gr.Unresolved = append(gr.Unresolved, fmt.Sprintf("%s ×%d", k, unresolved[k]))
	}
	// Reachable states (either branch of every condition): entries, then the subgraphs of reached
	// states. Start lists those reached from the entries directly (as T040 did); SubScript children are
	// read for every reachable SubScript.
	reach := map[int]bool{}
	var mark func(steps []Step, top bool)
	mark = func(steps []Step, top bool) {
		for _, st := range steps {
			switch st.Kind {
			case "begin":
				if top && !slices.Contains(gr.Start, st.State) {
					gr.Start = append(gr.Start, st.State)
				}
				if reach[st.State] {
					continue
				}
				reach[st.State] = true
				if sp, ok := gr.Progs[strconv.Itoa(st.State)]; ok {
					mark(sp.Sub, false)
					mark(sp.True, false)
					mark(sp.False, false)
					mark(sp.Top, false)
					mark(sp.Finished, false)
				}
			case "cond":
				mark(st.True, top)
				mark(st.False, top)
			}
		}
	}
	mark(gr.Entry, true)
	slices.Sort(gr.Start)

	// SubScript start states: their child graphs.
	var children []uint32
	for i, si := range stateIndex {
		if d.types[i] != tSubScript || !reach[si] {
			continue
		}
		cv, _ := d.deref(fields(d.vals[i])["m_statescriptGraph"])
		child := guidOf(fields(cv)["m_graph"])
		if typeName(cv) != "STUConfigVarStatescriptGraph" || child == 0 {
			gr.Notes = append(gr.Notes, fmt.Sprintf("SubScript state %d: child graph is not a literal (%s)", si, typeName(cv)))
			continue
		}
		if gr.Children == nil {
			gr.Children = map[string]Child{}
		}
		// State data of an active SubScript (FUN_7ff650f50ea0): the child's instance id and the
		// node's 0xdb3a2efb flag (node +0x120, copied to state +0xAA on begin).
		ch := Child{Graph: hex32(child), Flag: num(fields(d.vals[i])["0xdb3a2efb"]) != 0}
		for _, idata := range list(fields(d.vals[i])["0x7eb65d0d"]) {
			s, err := g.setVar(d, fields(idata))
			if err != nil {
				gr.Notes = append(gr.Notes, fmt.Sprintf("SubScript state %d init data: %v", si, err))
				continue
			}
			ch.Init = append(ch.Init, s)
		}
		gr.Children[strconv.Itoa(si)] = ch
		children = append(children, uint32(child))
	}
	if depth > 8 {
		return fmt.Errorf("graph %#x: SubScript nesting deeper than 8", idx)
	}
	for _, c := range children {
		if err := g.graph(c, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// varPath reads an STUConfigVarDynamic path of one var: [id] or [0x…03, id] = the instance bag,
// [0x…04, id] = the entity bag (StatescriptRuntime.md §1.7).
func varPath(cv any) (id uint32, owner bool, ok bool) {
	if typeName(cv) != "STUConfigVarDynamic" {
		return 0, false, false
	}
	p := list(fields(cv)["0x4f6799b5"])
	var ids []uint64
	for _, x := range p {
		ids = append(ids, guidOf(x))
	}
	switch {
	case len(ids) == 1:
		return uint32(ids[0]), false, ids[0]>>52 == 0x0d8 && ids[0]&0xffffffff > 0x10
	case len(ids) == 2 && (ids[0] == 0x0d80000000000003 || ids[0] == 0x0d80000000000004):
		return uint32(ids[1]), ids[0] == 0x0d80000000000004, ids[1]>>52 == 0x0d8 && ids[1]&0xffffffff > 0x10
	}
	return 0, false, false
}

// cond reads a condition the server can evaluate at start-up: a plain var path, an expression over
// such paths, or a literal.
func (g *gen) cond(d *doc, ref any) (Set, error) {
	cv, _ := d.deref(ref)
	c := Set{}
	if from, owner, ok := varPath(cv); ok {
		c.From, c.FromOwner = hex32(from), owner
		return c, nil
	}
	if typeName(cv) == "0xc81f8768" {
		c.StackFree = true
		return c, nil
	}
	if isExpr(cv) {
		e, err := g.expr(d, cv)
		if err != nil {
			return c, err
		}
		c.Expr = e
		return c, nil
	}
	if cv == nil {
		return c, fmt.Errorf("no condition")
	}
	v, err := g.configVar(d, map[string]any{"0x4f6799b5": map[string]any{"$guid": "0x0d80000000000000"}, "m_value": ref})
	if err != nil {
		return c, fmt.Errorf("condition %s needs the runtime", typeName(cv))
	}
	c.Value = &v
	return c, nil
}

// Expression config vars: the float calculator and the typed one share layout and opcodes.
func isExpr(cv any) bool {
	t := typeName(cv)
	return t == "STUConfigVarExpression" || t == "STUConfigVarExpressionTyped" || t == "0xec502a81"
}

// expr reads an expression whose operands are plain var paths, constants and literal config vars.
func (g *gen) expr(d *doc, cv any) (*Expr, error) {
	if !isExpr(cv) {
		return nil, fmt.Errorf("not an expression")
	}
	f := fields(cv)
	ef := fields(f["m_expression"])
	e := &Expr{Ops: []int{}}
	for _, o := range list(ef["m_opcodes"]) {
		e.Ops = append(e.Ops, int(num(o)))
	}
	for _, v := range list(ef["m_dynamicVars"]) {
		dv, _ := d.deref(v)
		id, owner, ok := varPath(dv)
		if !ok {
			return nil, fmt.Errorf("expression operand %s is not a plain var path", typeName(dv))
		}
		e.Vars = append(e.Vars, Set{From: hex32(id), FromOwner: owner})
	}
	for _, c := range list(ef["0x1b183605"]) {
		e.Consts = append(e.Consts, float32(num(c)))
	}
	for _, c := range list(f["m_configVars"]) {
		v, err := g.configVar(d, map[string]any{"0x4f6799b5": map[string]any{"$guid": "0x0d80000000000000"}, "m_value": c})
		if err != nil {
			return nil, fmt.Errorf("expression config var: %w", err)
		}
		e.CVs = append(e.CVs, v)
	}
	return e, nil
}

// setVar reads a SetVar node (m_out_Var, m_value) the generator can apply statically.
func (g *gen) setVar(d *doc, nf map[string]any) (Set, error) {
	out, _ := d.deref(nf["m_out_Var"])
	id, owner, ok := varPath(out)
	if !ok {
		return Set{}, fmt.Errorf("target %s is not a plain var path", typeName(out))
	}
	s := Set{ID: hex32(id), Owner: owner}
	val, _ := d.deref(nf["m_value"])
	if from, fromOwner, ok := varPath(val); ok {
		s.From, s.FromOwner = hex32(from), fromOwner
		return s, nil
	}
	v, err := g.configVar(d, map[string]any{"0x4f6799b5": map[string]any{"$guid": fmt.Sprintf("%#x", 0x0d80000000000000|uint64(id))}, "m_value": nf["m_value"]})
	if err != nil {
		return Set{}, err
	}
	s.Value = &v
	return s, nil
}

func sortedKeys[V any](m map[string]V) []string {
	k := make([]string, 0, len(m))
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

// --- Go tables ----------------------------------------------------------------------------------

func goValue(v Var) string {
	switch v.Type {
	case pFloat:
		return fmt.Sprintf("Float(%s)", strconv.FormatFloat(float64(v.Float), 'g', -1, 32))
	case pString:
		return fmt.Sprintf("String(%q)", v.Str)
	case pByte:
		return fmt.Sprintf("Byte(%d)", v.Int)
	case pInt64:
		return fmt.Sprintf("Int64(%#016x)", uint64(v.Int))
	default:
		return fmt.Sprintf("Int(%d)", v.Int)
	}
}

func goVars(vs []Var) string {
	if len(vs) == 0 {
		return "nil"
	}
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = fmt.Sprintf("{%#x, %s}", uint32(v.ID), goValue(v))
	}
	return "[]Var{" + strings.Join(parts, ", ") + "}"
}

// goSet is a Set literal (without its type name).
func goSet(s Set) string {
	src := fmt.Sprintf("From: %#x, FromOwner: %t", uint32(s.From), s.FromOwner)
	if s.Value != nil {
		src = fmt.Sprintf("Value: ptr(%s)", goValue(*s.Value))
	}
	if s.Expr != nil {
		src = "Expr: " + goExpr(s.Expr)
	}
	if s.StackFree {
		src = "StackFree: true"
	}
	return fmt.Sprintf("{ID: %#x, Owner: %t, %s}", uint32(s.ID), s.Owner, src)
}

func goSteps(steps []Step) string {
	var parts []string
	for _, st := range steps {
		switch st.Kind {
		case "begin":
			parts = append(parts, fmt.Sprintf("{Kind: StepBegin, State: %d}", st.State))
		case "set":
			parts = append(parts, fmt.Sprintf("{Kind: StepSet, Set: &Set%s}", goSet(*st.Set)))
		case "cond":
			parts = append(parts, fmt.Sprintf("{Kind: StepCond, Cond: &Set%s, True: %s, False: %s}", goSet(*st.Cond), goSteps(st.True), goSteps(st.False)))
		}
	}
	if len(parts) == 0 {
		return "nil"
	}
	return "[]Step{" + strings.Join(parts, ", ") + "}"
}

func goExpr(e *Expr) string {
	var vs, cs, fs []string
	for _, v := range e.Vars {
		vs = append(vs, fmt.Sprintf("{From: %#x, FromOwner: %t}", uint32(v.From), v.FromOwner))
	}
	for _, c := range e.Consts {
		fs = append(fs, strconv.FormatFloat(float64(c), 'g', -1, 32))
	}
	for _, c := range e.CVs {
		cs = append(cs, goValue(c))
	}
	return fmt.Sprintf("&Expr{Ops: []byte{%s}, Vars: []Set{%s}, Consts: []float32{%s}, CVs: []Value{%s}}",
		goInts(e.Ops, false), strings.Join(vs, ", "), strings.Join(fs, ", "), strings.Join(cs, ", "))
}

func goInts[T ~uint32 | ~int](xs []T, hex bool) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		if hex {
			parts[i] = fmt.Sprintf("%#x", uint64(x))
		} else {
			parts[i] = strconv.Itoa(int(x))
		}
	}
	return strings.Join(parts, ", ")
}

func goTables(heroes []*Hero, graphs []*Graph) []byte {
	var b strings.Builder
	b.WriteString("// Code generated by systems/data/heroes/tools/herogen; DO NOT EDIT.\n\npackage heroes\n\n")
	b.WriteString("// All is every hero of build 24919 (one per STUHero with a gameplay entity), sorted by hero GUID.\n")
	b.WriteString("var All = []Hero{\n")
	for _, h := range heroes {
		fmt.Fprintf(&b, "\t{Hero: %#016x, Name: %q, Def: %#x, Graphs: []Use{\n", uint64(h.Hero), h.Name, uint32(h.Def))
		for _, u := range h.Graphs {
			fmt.Fprintf(&b, "\t\t{Graph: %#x, Source: %q, Weapon: %d, Overrides: %s},\n", uint32(u.Graph), u.Source, u.Weapon, goVars(u.Overrides))
		}
		b.WriteString("\t}},\n")
	}
	b.WriteString("}\n\n")
	b.WriteString("// baseStates are the state types without a client class of their own: they run as the base\n")
	b.WriteString("// STUStatescriptState, whose state data is empty (systems/data/statescript/statescript_node_catalogue.json).\n")
	b.WriteString("var baseStates = map[uint32]bool{\n")
	for _, t := range baseStateTypes() {
		fmt.Fprintf(&b, "\t%#08x: true,\n", t)
	}
	b.WriteString("}\n\n")
	b.WriteString("// Graphs are the layouts, schemas and starts of every graph a hero uses (and their SubScript children).\n")
	b.WriteString("var Graphs = map[uint32]*Graph{\n")
	for _, g := range graphs {
		fmt.Fprintf(&b, "\t%#x: {Index: %#x, SyncVars: []uint32{%s}, RemoteStates: []int{%s}, States: %q,\n", uint32(g.Index), uint32(g.Index),
			goInts(g.SyncVars, true), goInts(g.RemoteStates, false), g.States)
		fmt.Fprintf(&b, "\t\tStateTypes: []uint32{%s},\n", goInts(g.StateTypes, true))
		fmt.Fprintf(&b, "\t\tSchema: %s,\n", goVars(g.Schema))
		fmt.Fprintf(&b, "\t\tStart: []int{%s}", goInts(g.Start, false))
		if len(g.Unencodable) > 0 {
			fmt.Fprintf(&b, ",\n\t\tUnencodable: []int{%s}", goInts(g.Unencodable, false))
		}
		if len(g.Entry) > 0 {
			fmt.Fprintf(&b, ",\n\t\tEntry: %s", goSteps(g.Entry))
		}
		if len(g.Progs) > 0 {
			keys := sortedKeys(g.Progs)
			sort.Slice(keys, func(i, j int) bool { a, _ := strconv.Atoi(keys[i]); c, _ := strconv.Atoi(keys[j]); return a < c })
			var parts []string
			for _, k := range keys {
				p := g.Progs[k]
				var f []string
				for _, x := range []struct {
					n string
					s []Step
				}{{"Sub", p.Sub}, {"True", p.True}, {"False", p.False}, {"Top", p.Top}, {"Finished", p.Finished}} {
					if len(x.s) > 0 {
						f = append(f, x.n+": "+goSteps(x.s))
					}
				}
				if p.Settle {
					f = append(f, "Settle: true")
				}
				parts = append(parts, fmt.Sprintf("%s: {%s}", k, strings.Join(f, ", ")))
			}
			fmt.Fprintf(&b, ",\n\t\tProgs: map[int]StateProg{%s}", strings.Join(parts, ", "))
		}
		if len(g.Conds) > 0 {
			keys := sortedKeys(g.Conds)
			sort.Slice(keys, func(i, j int) bool { a, _ := strconv.Atoi(keys[i]); c, _ := strconv.Atoi(keys[j]); return a < c })
			var parts []string
			for _, k := range keys {
				c := g.Conds[k]
				parts = append(parts, fmt.Sprintf("%s: %s", k, goSet(c)))
			}
			fmt.Fprintf(&b, ",\n\t\tConds: map[int]Set{%s}", strings.Join(parts, ", "))
		}
		if len(g.Children) > 0 {
			keys := sortedKeys(g.Children)
			sort.Slice(keys, func(i, j int) bool { a, _ := strconv.Atoi(keys[i]); c, _ := strconv.Atoi(keys[j]); return a < c })
			var parts []string
			for _, k := range keys {
				c := g.Children[k]
				init := "nil"
				if len(c.Init) > 0 {
					var is []string
					for _, s := range c.Init {
						src := fmt.Sprintf("From: %#x, FromOwner: %t", uint32(s.From), s.FromOwner)
						if s.Value != nil {
							src = fmt.Sprintf("Value: ptr(%s)", goValue(*s.Value))
						}
						is = append(is, fmt.Sprintf("{ID: %#x, Owner: %t, %s}", uint32(s.ID), s.Owner, src))
					}
					init = "[]Set{" + strings.Join(is, ", ") + "}"
				}
				parts = append(parts, fmt.Sprintf("%s: {%#x, %t, %s}", k, uint32(c.Graph), c.Flag, init))
			}
			fmt.Fprintf(&b, ",\n\t\tChildren: map[int]Child{%s}", strings.Join(parts, ", "))
		}
		b.WriteString("},\n")
	}
	b.WriteString("}\n")
	return []byte(b.String())
}

// baseStateTypes lists the state types the node catalogue says fall back to the base
// STUStatescriptState's client class (a7213568).
func baseStateTypes() []uint32 {
	b, err := os.ReadFile(filepath.Join(repoRoot(), "systems/data/statescript/statescript_node_catalogue.json"))
	if err != nil {
		log.Fatal(err)
	}
	var cat []struct {
		Hash     string `json:"hash"`
		Kind     string `json:"kind"`
		ImplFrom string `json:"impl_from"`
	}
	if err := json.Unmarshal(b, &cat); err != nil {
		log.Fatal(err)
	}
	var out []uint32
	for _, c := range cat {
		if c.Kind == "State" && c.ImplFrom == "a7213568" {
			h, _ := strconv.ParseUint(c.Hash, 16, 32)
			out = append(out, uint32(h))
		}
	}
	slices.Sort(out)
	return out
}
