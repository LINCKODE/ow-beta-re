// Package heroes is the Statescript start-up of every hero of build 24919, generated offline from
// the install by systems/data/heroes/tools/herogen (heroes_gen.go; the readable form is
// systems/data/heroes/heroes.json). Format and meaning: docs/MatchLogic.md "Hero start-up".
//
// A hero's gameplay entity starts with the graphs of its STUStatescriptComponent (each with the
// graph's m_publicSchema defaults, then the definition's GraphWithOverrides values) and its
// STUWeaponComponent (manager script, then each weapon script with its weapon index in var 0x1c).
// The client does that itself only for entities it creates (StartDefaultScripts); for a hero the
// server creates, the server's lock-in full update has to carry it. Start computes that update's
// content: instances (with SubScript children), their variables, the owner (entity bag) variables and
// the states the plain entries begin.
package heroes

import (
	"math"
	"slices"
)

// Type is a Statescript primitive type (docs/StatescriptRuntime.md §1.7).
type Type uint8

const (
	TypeByte   Type = 1
	TypeInt    Type = 2
	TypeFloat  Type = 3
	TypeInt64  Type = 4 // GUIDs
	TypeString Type = 5
)

// Value is a Statescript variable value.
type Value struct {
	Type Type
	Bits uint64 // Byte, Int (sign-extended), Int64; Float: the float32 bits
	Str  string
}

func Byte(v int64) Value       { return Value{Type: TypeByte, Bits: uint64(v)} }
func Int(v int64) Value        { return Value{Type: TypeInt, Bits: uint64(v)} }
func Int64(v uint64) Value     { return Value{Type: TypeInt64, Bits: v} }
func String(s string) Value    { return Value{Type: TypeString, Str: s} }
func Float(f float32) Value    { return Value{Type: TypeFloat, Bits: uint64(math.Float32bits(f))} }
func (v Value) Float() float32 { return math.Float32frombits(uint32(v.Bits)) }

// Truthy is the client's truthiness (FUN 0xee97b0): non-zero, |x| > ε for floats, non-empty strings.
func (v Value) Truthy() bool {
	switch v.Type {
	case TypeFloat:
		return math.Abs(float64(v.Float())) > 1e-6
	case TypeString:
		return v.Str != ""
	case TypeInt:
		return int32(v.Bits) != 0
	}
	return v.Bits != 0
}
func ptr(v Value) *Value { return &v }

// Var is a variable: id 0x0D80000000000000|ID.
type Var struct {
	ID    uint32
	Value Value
}

// Hero is a STUHero and the graphs its gameplay entity (0x0400000000000000|Def) starts.
type Hero struct {
	Hero   uint64 // STUHero, 0x02E...
	Name   string
	Def    uint32
	Graphs []Use
}

// Use is a graph instantiated on the hero.
type Use struct {
	Graph     uint32 // 0x0580000000000000|Graph
	Source    string // "component", "manager", "weapon"
	Weapon    int    // weapon scripts: weapon index (var 0x1c), 1-based
	Overrides []Var  // the definition's GraphWithOverrides values
}

// Graph is a graph's sync layout, schema defaults and start.
type Graph struct {
	Index        uint32
	SyncVars     []uint32          // eligible m_syncVars in slot order: the changed bits of a full update
	RemoteStates []int             // m_states index of each eligible m_remoteSyncNodes entry, in update order
	States       string            // local-format kinds per m_states: '.' synced, 'C'/'V' skipped, 'S' has-update
	StateTypes   []uint32          // STU type of each m_states entry (the state data format depends on it)
	Schema       []Var             // m_publicSchema defaults, in schema order
	Start        []int             // m_states the plain entries begin (informational)
	Entry        []Step            // what the plain entries run, in order
	Progs        map[int]StateProg // subgraph programs of states, by m_states index
	Conds        map[int]Set       // BooleanSwitch states: their condition (From / Value / Expr; ID unused)
	Unencodable  []int             // Ability / Stack states whose data carries a runtime priority value
	Children     map[int]Child     // SubScript states: the graph they play
}

// StepKind is what a Step does.
type StepKind uint8

const (
	StepBegin StepKind = iota // begin state State
	StepSet                   // run the SetVar Set
	StepCond                  // Condition_Bool Cond: run True or False
)

// Step is one thing a firing plug does, in link order (docs/MatchLogic.md §14.7).
type Step struct {
	Kind        StepKind
	State       int
	Set, Cond   *Set
	True, False []Step
}

// StateProg is what a state's subgraph plugs run when it begins: Sub (every state), True / False
// (BooleanSwitch, by its value), Top (Stack, on top of its priority stack).
type StateProg struct {
	Sub, True, False, Top []Step
	// Finished / Settle: a Wait with a literal timeout of at most 0.25 s is shown finished in the
	// start-up snapshot (not active); its on-finished program runs instead [?].
	Finished []Step
	Settle   bool
}

// Set is an entry's SetVar: ID (entity bag if Owner) = Value, or the var From (entity bag if FromOwner),
// or (conditions only) an expression.
type Set struct {
	ID        uint32
	Owner     bool
	Value     *Value
	From      uint32
	FromOwner bool
	Expr      *Expr
	StackFree bool // condition 0xc81f8768 ("may take the stack slot"): true at start-up [?]
}

// Child is the graph a SubScript start state plays, and the state's data flag (node 0xdb3a2efb).
type Child struct {
	Graph uint32
	Flag  bool
	Init  []Set // SubScript init data: child var ID = the parent's var From (Owner: the entity bag)
}

// ByDef returns the hero whose gameplay entity definition is 0x0400000000000000|def.
func ByDef(def uint32) (*Hero, bool) {
	for i := range All {
		if All[i].Def == def {
			return &All[i], true
		}
	}
	return nil, false
}

// ByHero returns the hero with STUHero GUID hero.
func ByHero(hero uint64) (*Hero, bool) {
	for i := range All {
		if All[i].Hero == hero {
			return &All[i], true
		}
	}
	return nil, false
}

// localHeroFlags: owner vars the client sets to true on its local hero ($1a1, $656, $94c).
var localHeroFlags = []uint32{0x1a1, 0x656, 0x94c}

// Owner vars the start-up sets on the entity bag, besides the entries' SetVars.
const (
	VarWeaponIndex  = 0x1c // weapon script instance var: its weapon index
	VarWeaponList   = 0x1d // entity bag: the valid weapon indices (a dictionary)
	VarPlayerScript = 0xfd // entity bag: 1 = the scripts run as a player (Prometheus player_spawner)
)

// Instance is a Statescript instance of the start-up.
type Instance struct {
	ID     uint16
	Graph  *Graph
	Parent uint16 // 0 = none; else the parent instance, whose SubScript state Slot plays this one
	Slot   int    // the parent's m_states index
	Vars   []Var  // instance bag, in the order they are set (later ones win)
	Active []Active
	// Skipped: start states left inactive: their state data format is not known (Stack, Ability,
	// Switch, ...), their condition needs the runtime, or their kind is 'S' / client- / server-only.
	Skipped []int
}

// Active is a state the start-up begins, with its state data (what the client's reader for the
// state's class, vfunc +0x1F8 after the mirror objects' fields, reads in a local full update).
type Active struct {
	State int    // m_states index
	Data  Data   // the state data's format
	Child uint16 // DataSubScript: the child instance
	Flag  bool   // DataSubScript: the node's 0xdb3a2efb flag; DataSwitch: the switch value
	Bits  uint32 // DataBits
	NBits int    // DataBits
}

// Data is a state data format.
type Data uint8

const (
	DataNone      Data = iota // no bits
	DataSubScript             // small(child instance) + 1 bit (FUN_7ff650f50f40)
	DataSwitch                // 1 bit: the BooleanSwitch value (FUN_7ff650f55560; 1 bit measured live)
	DataBits                  // Active.NBits fixed bits (Active.Bits, LSB first): see fixedData
)

// fixedData: state types whose local-format state data is a fixed bit pattern at start-up
// (docs/MatchLogic.md "State data per type"; decoded in Ghidra, the values as a client-started hero
// has them [L, T044]).
var fixedData = map[uint32]struct {
	Bits  uint32
	NBits int
}{
	// Ability (reader 0x10871b0): mirror u8 flags (+0x15c) = 0, no priority (0x92c13ac6 is null or a
	// StackPriority in every hero), cooldown bit 0, stack predecessor bit 0: 10 zero bits [G, L].
	0xf53c5da8: {0, 10},
	// Stack (0xf55bf0): predecessor bit 0, no priority (m_priority null or StackPriority), on-top bit
	// 1 (+0xc4), under bit 0 (+0xc5): 0b010 as a client-started hero has it [G, L].
	0xf7d6498c: {0b010, 3},
	// TrackMovement (0xf55560): the tracking bool (+0x120) = 0 [G, L].
	0x8de7e50f: {0, 1},
	// CameraTransform (0xdc4990): stack predecessor bit 0 [G].
	0xa5f89e3f: {0, 1},
	// BlinkEffect (0xd68bc0): nothing in the local format (the remote one carries a u32) [G].
	0x429991a7: {0, 0},
}

// State types whose state data is empty: the class's data reader (vfunc +0x1F8) and mirror list
// (+0x208) are both the no-op 0x5a0c20 (vtables read from the client), or 0 data bits were measured
// on live states (ss_serialize_local).
var emptyData = map[uint32]bool{
	0xa7213568: true, // STUStatescriptState
	0x4ab09851: true,
	0xc2a01675: true, // UXViewModel
	0xc739baac: true, // UXScreen
	0xa97b875f: true, // PopulateTips
	0xff77458c: true, // LogicalButton
	0x85c28a9a: true, // DataFlowMapping
	0x6e81f6d4: true, // CosmeticEntity
	0x619d9e95: true, // ModifyHealth
	0xd78490d1: true, // ApplyAura
	0x2168e38f: true,
	0x65f158da: true, // Wait
	0x82505a1b: true,
	0xa7ca1073: true, // TrackStatTime
	0xe67578c0: true, // Watch
	0x0459c53d: true, // UIInWorld
	0x9f74c07c: true, // ClientonlyController
	0xf6e1e486: true, // LedgeClimb (T044: +0x1F0..+0x208 all no-op; 0 bits live)
}

const (
	typeSubScript     = 0x3db862c4
	typeBooleanSwitch = 0xe811ecbb
	typeStack         = 0xf7d6498c
)

// Startup is what a hero's lock-in full update carries.
type Startup struct {
	Hero      *Hero
	Owner     []Var    // entity bag (a full update clears it first)
	Weapons   []uint16 // VarWeaponList: the weapon indices (dictionary keys and values)
	Instances []Instance
}

// WeaponDict is the valid-weapon dictionary $1d as the client's player spawner fills it: a filler entry 0 -> 0,
// then each weapon index w -> w. Without the filler, cycling weapons (next / previous weapon, the scroll wheel)
// selects no weapon and the weapon scripts go inactive (Prometheus 92f7d9a; reproduced on the instance server,
// T053 [L]).
func (st *Startup) WeaponDict() (keys, values []Value) {
	if len(st.Weapons) == 0 {
		return nil, nil
	}
	keys, values = []Value{Int(0)}, []Value{Int(0)}
	for _, w := range st.Weapons {
		keys, values = append(keys, Int(int64(w))), append(values, Int(int64(w)))
	}
	return keys, values
}

// Start computes a hero's start-up: instances 1..n for the hero's graphs in definition order, then
// the SubScript children of their start states (ids after them, depth first). Per instance: schema
// defaults, then the definition's overrides, then the weapon index; the entries' SetVars applied in
// order; the start states active.
func Start(def uint32) (*Startup, bool) {
	h, ok := ByDef(def)
	if !ok {
		return nil, false
	}
	st := &Startup{Hero: h}
	// StartDefaultScripts' four entity-bag values (docs/StatescriptLoading.md §4).
	for id := uint32(0x317); id <= 0x31a; id++ {
		st.Owner = append(st.Owner, Var{id, Float(1)})
	}
	st.Owner = append(st.Owner, Var{VarPlayerScript, Byte(1)})
	// Entity-bag flags the client's own code sets on its local hero (no graph writes them; read live on
	// Genji, T044 [L]). The full update clears the entity bag, so the start-up carries them, and the
	// start's conditions see them (Genji 0x157 #2 / #7: the jump branch).
	for _, id := range localHeroFlags {
		st.Owner = append(st.Owner, Var{id, Byte(1)})
	}
	for _, u := range h.Graphs {
		g := Graphs[u.Graph]
		if g == nil {
			continue
		}
		in := Instance{ID: uint16(len(st.Instances) + 1), Graph: g}
		in.Vars = append(in.Vars, g.Schema...)
		in.Vars = append(in.Vars, u.Overrides...)
		if u.Source == "weapon" {
			in.Vars = append(in.Vars, Var{VarWeaponIndex, Int(int64(u.Weapon))})
			st.Weapons = append(st.Weapons, uint16(u.Weapon))
		}
		st.Instances = append(st.Instances, in)
	}
	// Entries, then (depth first) the subgraphs of the states they begin; SubScript children are
	// appended and run in turn.
	for i := 0; i < len(st.Instances); i++ {
		r := runner{st: st, i: i, begun: map[int]bool{}}
		r.run(st.Instances[i].Graph.Entry, 0)
	}
	return st, true
}

// Weapon selection: the weapon manager (0x015) Watch state #8's OnBegin picks the first valid
// weapon: $1f := the first key of $1d whose value is set, $1e := $1f (action 0x17c9f80b, which the
// start-up doesn't model). The weapon scripts' "is this the current weapon" switches ($1f == $1c)
// depend on it [D].
const (
	managerGraph     = 0x15
	managerWatch     = 8
	VarCurrentWeapon = 0x1f
	VarNextWeapon    = 0x1e
)

// runner runs one instance's programs.
type runner struct {
	st    *Startup
	i     int
	begun map[int]bool
}

func (r *runner) in() *Instance { return &r.st.Instances[r.i] }

func (r *runner) eval(c *Set) bool {
	if c.StackFree {
		return true
	}
	in := r.in()
	if c.Expr != nil {
		v, ok := c.Expr.Eval(in.Vars, r.st.Owner)
		return ok && v.Truthy()
	}
	v, ok := c.apply(in.Vars, r.st.Owner)
	return ok && v.Truthy()
}

func (r *runner) run(steps []Step, depth int) {
	if depth > 64 {
		return
	}
	for _, s := range steps {
		switch s.Kind {
		case StepSet:
			in := r.in()
			v, ok := s.Set.apply(in.Vars, r.st.Owner)
			if !ok {
				continue
			}
			if s.Set.Owner {
				r.st.Owner = set(r.st.Owner, s.Set.ID, v)
			} else {
				in.Vars = set(in.Vars, s.Set.ID, v)
			}
		case StepCond:
			if r.eval(s.Cond) {
				r.run(s.True, depth+1)
			} else {
				r.run(s.False, depth+1)
			}
		case StepBegin:
			r.begin(s.State, depth+1)
		}
	}
}

// begin begins state si once: a synced state is activated with its state data (or left inactive
// if the start-up can't produce the data, and then its subgraphs don't run either); a server-only
// (or has-update) state runs on the server without being sent; client-only states are the client's.
func (r *runner) begin(si, depth int) {
	if r.begun[si] {
		return
	}
	r.begun[si] = true
	in := r.in()
	g := in.Graph
	typ := g.StateTypes[si]
	a, ok := Active{State: si}, false
	switch {
	case g.States[si] == 'C':
		return
	case g.States[si] != '.':
		ok = true // runs on the server, not sent
	case typ == typeSubScript:
		if c, has := g.Children[si]; has && Graphs[c.Graph] != nil {
			child := Instance{ID: uint16(len(r.st.Instances) + 1), Graph: Graphs[c.Graph], Parent: in.ID, Slot: si}
			for _, s := range c.Init {
				if v, ok := s.apply(in.Vars, r.st.Owner); ok {
					if s.Owner {
						r.st.Owner = set(r.st.Owner, s.ID, v)
					} else {
						child.Vars = set(child.Vars, s.ID, v)
					}
				}
			}
			a.Data, a.Child, a.Flag, ok = DataSubScript, child.ID, c.Flag, true
			r.st.Instances = append(r.st.Instances, child)
			in = r.in() // the append may have moved it
		}
	case typ == typeBooleanSwitch:
		if c, has := g.Conds[si]; has {
			a.Data, a.Flag, ok = DataSwitch, r.eval(&c), true
		}
	case slices.Contains(g.Unencodable, si):
	case hasFixed(typ):
		f := fixedData[typ]
		a.Data, a.Bits, a.NBits, ok = DataBits, f.Bits, f.NBits, true
	case emptyData[typ] || baseStates[typ]:
		a.Data, ok = DataNone, true
	}
	if !ok {
		in.Skipped = append(in.Skipped, si)
		return
	}
	if p, has := g.Progs[si]; has && p.Settle {
		r.run(p.Finished, depth)
		return
	}
	if g.States[si] == '.' {
		in.Active = append(in.Active, a)
	}
	if g.Index == managerGraph && si == managerWatch && len(r.st.Weapons) > 0 {
		if cur, ok := Lookup(r.st.Owner, VarCurrentWeapon); !ok || !slices.Contains(r.st.Weapons, uint16(cur.Bits)) {
			w := Int(int64(r.st.Weapons[0]))
			r.st.Owner = set(r.st.Owner, VarCurrentWeapon, w)
			r.st.Owner = set(r.st.Owner, VarNextWeapon, w)
		}
	}
	p, has := g.Progs[si]
	if !has {
		return
	}
	switch typ {
	case typeBooleanSwitch:
		if a.Flag {
			r.run(p.True, depth)
		} else {
			r.run(p.False, depth)
		}
	case typeStack:
		r.run(p.Top, depth) // the start-up sends every Stack on top (fixedData)
	}
	r.run(p.Sub, depth)
}

func hasFixed(typ uint32) bool { _, ok := fixedData[typ]; return ok }

// NoStateData reports whether a state type carries no local-format state data (the no-op data readers, and
// the types without a client class of their own).
func NoStateData(typ uint32) bool { return emptyData[typ] || baseStates[typ] }

// apply evaluates a SetVar against the instance and entity bags; false if its source is unset.
func (s Set) apply(inst, owner []Var) (Value, bool) {
	if s.Value != nil {
		return *s.Value, true
	}
	from := inst
	if s.FromOwner {
		from = owner
	}
	return Lookup(from, s.From)
}

// Lookup is the last value of var id in vs.
func Lookup(vs []Var, id uint32) (Value, bool) {
	for i := len(vs) - 1; i >= 0; i-- {
		if vs[i].ID == id {
			return vs[i].Value, true
		}
	}
	return Value{}, false
}

// set replaces var id in vs or appends it.
func set(vs []Var, id uint32, v Value) []Var {
	if i := slices.IndexFunc(vs, func(x Var) bool { return x.ID == id }); i >= 0 {
		vs[i].Value = v
		return vs
	}
	return append(vs, Var{id, v})
}

// Final returns vs with one entry per id (the last value), in first-set order.
func Final(vs []Var) []Var {
	var out []Var
	for _, v := range vs {
		out = set(out, v.ID, v.Value)
	}
	return out
}
