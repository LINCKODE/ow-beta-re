package sim

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"

	obits "owserver/inst/bits"
	"owserver/inst/replication"
	"owserver/inst/wire"
	"owserver/internal/heroes"

	"owstatescript/statescript"
	"owstu/stu"
)

// GraphSource reads a Statescript graph (GUID type 0x058) as STUD bytes (CASC).
type GraphSource interface {
	Read(guid uint64) ([]byte, error)
}

// Engine holds the Statescript runtime (graphs, evaluators) of the process. One
// engine serves every match; like the matches it belongs to the game loop.
type Engine struct {
	log     *slog.Logger
	src     GraphSource
	rt      *statescript.Runtime
	loaded  map[uint64]error
	layouts map[uint64]*replication.GraphLayout
	// Policy decides which state types the server corrects; set before the first match.
	Policy Policy
	caps   map[uint32]statescript.TypeCap
}

func NewEngine(log *slog.Logger, src GraphSource) *Engine {
	return &Engine{log: log, src: src, rt: statescript.NewRuntime(), loaded: map[uint64]error{},
		layouts: map[uint64]*replication.GraphLayout{}}
}

func (e *Engine) Close() { e.rt.Close() }

// Policy is which state types the server corrects (docs/InstanceServer.md §10.3). The server keeps no per-type
// knowledge: it asks the runtime what it implements (ss_runtime_type_cap) and narrows that by evidence.
//   - no state data in the format: its activity is sent (the spec allows activity only; a state's lifetime runs
//     through links, which the runtime models for every type);
//   - data the runtime writes from its state (SS_DATA_WRITER): sent;
//   - data the runtime can only echo from an import: frozen;
//   - types the shadow compare found mismatches for: frozen;
//   - MinLevel > 0: also types implemented below that level.
type Policy struct {
	MinLevel statescript.Level
	Mismatch map[uint32]bool
	// States are single states (graph index, m_states index) the shadow compare found mismatches for (LoadEvidence).
	States map[[2]int]bool
}

// LoadEvidence reads a shadow compare's per-state evidence (server/cmd/shadowcmp -evidence) and freezes the states
// with mismatches (docs/InstanceServer.md §10.4). Returns how many.
func (p *Policy) LoadEvidence(b []byte) (int, error) {
	var ev struct {
		States map[string]struct {
			Mismatches int `json:"mismatches"`
		} `json:"states"`
	}
	if err := json.Unmarshal(b, &ev); err != nil {
		return 0, err
	}
	n := 0
	for k, e := range ev.States {
		var g, st int
		if _, err := fmt.Sscanf(k, "0x%x:%d", &g, &st); err != nil {
			return n, fmt.Errorf("evidence key %q: %w", k, err)
		}
		if e.Mismatches > 0 {
			if p.States == nil {
				p.States = map[[2]int]bool{}
			}
			p.States[[2]int{g, st}] = true
			n++
		}
	}
	return n, nil
}

// cap is the runtime's capability for a type (cached; the runtime's implementation doesn't change at run time).
func (e *Engine) cap(t uint32) (statescript.TypeCap, bool) {
	if e.caps == nil {
		e.caps = map[uint32]statescript.TypeCap{}
		for _, c := range e.rt.TypeCaps() {
			e.caps[c.Type] = c
		}
	}
	c, ok := e.caps[t]
	return c, ok
}

// Corrects reports whether the server corrects (sends) states of type t in a format, and why not. A state the
// server doesn't correct is frozen at what the client was last sent: a client prediction lasts only until the
// next confirmation (§10.3), so "leave it to the client" is not an option.
func (e *Engine) Corrects(t uint32, local bool) (bool, string) {
	c, ok := e.cap(t)
	switch {
	case !ok:
		return false, "unknown type"
	case c.Kind != statescript.KindState:
		return false, "not a state"
	case e.Policy.Mismatch[t]:
		return false, "shadow mismatch"
	case c.Level < e.Policy.MinLevel:
		return false, fmt.Sprintf("level %d", c.Level)
	}
	d := c.DataLocal
	if !local {
		d = c.DataRemote
	}
	if d == statescript.DataEcho {
		return false, "no data writer"
	}
	return true, ""
}

// StackPriority fields (STU 98b439ca).
const (
	stuStackPriority = 0x98b439ca
	fieldWinsTies    = 0xa6518307
)

// readStackPriority decodes a StackPriority record: m_priority and "wins ties".
func readStackPriority(b []byte) (float32, bool, error) {
	d, err := stu.Open(b, stu.File, 0)
	if err != nil {
		return 0, false, err
	}
	defer d.Close()
	root := d.Decode(d.Root())
	if root.TypeHash() != stuStackPriority {
		return 0, false, fmt.Errorf("type %08x", root.TypeHash())
	}
	var prio float64
	var ties int64
	found := false
	for _, f := range root.Items() {
		if f.FieldHash() == fieldWinsTies {
			ties, _ = f.Int()
		} else if v, err := f.Float(); err == nil {
			prio, found = v, true // m_priority, the record's only float
		}
	}
	if !found {
		return 0, false, fmt.Errorf("no m_priority")
	}
	return float32(prio), ties != 0, nil
}

// LoadStackPriorities reads StackPriority records (GUID type 0x0C4) and registers them
// with the runtime: Stack and Ability states need their priority (an Ability without
// its record never takes its stack slot). Call before the first match.
func (e *Engine) LoadStackPriorities(guids []uint64) (int, error) {
	n := 0
	for _, g := range guids {
		b, err := e.src.Read(g)
		if err != nil {
			return n, fmt.Errorf("stack priority %#016x: %w", g, err)
		}
		prio, ties, err := readStackPriority(b)
		if err != nil {
			return n, fmt.Errorf("stack priority %#016x: %w", g, err)
		}
		if err := e.rt.SetStackPriority(g, prio, ties); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// load loads a graph and every graph it references (SubScript children, PlayScript literals).
func (e *Engine) load(guid uint64) error {
	if err, done := e.loaded[guid]; done {
		return err
	}
	b, err := e.src.Read(guid)
	if err == nil {
		err = e.rt.LoadGraph(guid, b, statescript.File, 0)
	}
	e.loaded[guid] = err
	if err != nil {
		return fmt.Errorf("graph %#016x: %w", guid, err)
	}
	for _, ref := range e.rt.GraphRefs(guid) {
		if err := e.load(ref); err != nil {
			e.log.Warn("sim: referenced graph", "from", fmt.Sprintf("%#016x", guid), "err", err)
		}
	}
	return nil
}

// Runtime is the engine's Statescript runtime (for the match host, inst/game: one runtime per process).
func (e *Engine) Runtime() *statescript.Runtime { return e.rt }

// Load loads a graph and the graphs it references (once).
func (e *Engine) Load(guid uint64) error { return e.load(guid) }

// Source is where the engine reads graphs (and, for the match host, entity definitions) from.
func (e *Engine) Source() GraphSource { return e.src }

// layout is a loaded graph's local-format layout.
func (e *Engine) layout(guid uint64) (*replication.GraphLayout, error) {
	if l := e.layouts[guid]; l != nil {
		return l, nil
	}
	slots, err := e.rt.GraphSyncSlots(guid)
	if err != nil {
		return nil, err
	}
	states, err := e.rt.GraphStates(guid)
	if err != nil {
		return nil, err
	}
	nb, err := e.rt.GraphIndexBits(guid)
	if err != nil {
		return nil, err
	}
	bc, err := e.rt.GraphBitCounts(guid)
	if err != nil {
		return nil, err
	}
	// The index widths are fields of the graph asset (m_nodesBitCount +0x178, m_statesBitCount +0x17C).
	if bc.States != nb {
		e.log.Warn("sim: graph state index width differs from the asset's", "graph", fmt.Sprintf("%#x", guid), "computed", nb, "asset", bc.States)
	}
	l := &replication.GraphLayout{Index: uint16(guid), IdxBits: bc.States, NodeBits: bc.Nodes}
	for _, s := range slots {
		l.SyncSlots = append(l.SyncSlots, varLow(s.Var))
	}
	kinds := make([]byte, len(states))
	l.Types = make([]uint32, len(states))
	for i, s := range states {
		kinds[i] = map[statescript.StateKind]byte{statescript.StateClientOnly: 'C', statescript.StateServerOnly: 'V',
			statescript.StateHasUpdate: 'S'}[s.Kind]
		if kinds[i] == 0 {
			kinds[i] = '.'
		}
		l.Types[i] = s.Type
		if kinds[i] == '.' || kinds[i] == 'S' {
			// A state whose data the runtime can't produce exactly is frozen: one wrong length shifts the rest of
			// the delta and the client waits behind it for good (T051 [L]), or crashes (§3.4).
			if ok, _ := e.Corrects(s.Type, true); !ok || e.Policy.States[[2]int{int(uint16(guid)), i}] {
				if l.Frozen == nil {
					l.Frozen = map[int]bool{}
				}
				l.Frozen[i] = true
			}
		}
	}
	l.States = string(kinds)
	e.layouts[guid] = l
	return l, nil
}

// varLow is the part of a var id the wire carries (0x0D8 identifiers: the low bits).
func varLow(id uint64) uint32 { return uint32(id & 0xFFFFFFFF) }

// Runtime is a match's Host on the Statescript runtime.
type Runtime struct {
	e      *Engine
	heroes map[uint32]*hero
	frame  uint32
	// Ring > 0 keeps a snapshot per simulated frame, the last Ring frames (shadow mode, shadow.go).
	Ring int
	// Unsent: the deltas are encoded but never sent (shadow mode): each counts as acknowledged at once.
	Unsent bool
}

// NewRuntime makes a match's host on engine e.
func (e *Engine) NewRuntime() *Runtime { return &Runtime{e: e, heroes: map[uint32]*hero{}} }

// hero is one hero body's server-side Statescript.
type hero struct {
	body, def uint32
	sm        *statescript.SyncManager
	layouts   map[int16]*replication.GraphLayout
	seedData  map[[2]int]replication.StateData // start states' data (instance, state)

	frame  uint32                     // last simulated command frame
	inputs map[uint32]wire.InputEntry // received, not simulated yet
	last   wire.InputEntry

	ghost  *replication.LocalGhost // what the owner's client knows (acked base + packets since)
	resend bool                    // a delta was lost: send a union even without new frames

	recent []DeltaRecord          // the last deltas sent (debugging)
	snaps  map[uint32]shadowFrame // per simulated frame (Runtime.Ring)

	// Stats.
	Simulated, Deltas, Skipped, InputErrs, Lost int
	Unencodable                                 map[string]int
	inputErr                                    error
}

// typeBooleanSwitch is STUStatescriptStateBooleanSwitch.
const typeBooleanSwitch = 0xe811ecbb

// maxCatchUp bounds the frames simulated per tick for one hero (a client far ahead).
const maxCatchUp = 64

func (r *Runtime) SpawnHero(body, def, cf uint32) (*heroes.Startup, uint32, bool) {
	st, ok := heroes.Start(def)
	if !ok {
		return nil, 0, false
	}
	if cf == 0 {
		cf = r.frame
	}
	r.RemoveHero(body)
	h, err := r.seed(body, def, cf, st)
	if err != nil {
		r.e.log.Error("sim: hero start-up not simulated (the client gets it anyway)", "body", fmt.Sprintf("%#x", body), "err", err)
		return st, 0, true
	}
	r.heroes[body] = h
	r.keepSnapshot(h, cf)
	r.e.log.Info("sim: hero simulated", "body", fmt.Sprintf("%#x", body), "def", fmt.Sprintf("%#x", def),
		"instances", len(st.Instances), "cf", cf)
	return st, cf, true
}

// seed creates the server's instances from the start-up the client gets (the
// client's replication of the lock-in full update): instances with their ids and
// SubScript parents, then the owner and instance vars, then the start states with
// their data.
func (r *Runtime) seed(body, def, cf uint32, st *heroes.Startup) (*hero, error) {
	for _, in := range st.Instances {
		if err := r.e.load(statescript.GraphGUID(in.Graph.Index)); err != nil {
			return nil, err
		}
	}
	sm := r.e.rt.NewSyncManager(body, statescript.Server)
	if sm == nil {
		return nil, errors.New("no sync manager")
	}
	h := &hero{body: body, def: def, sm: sm, layouts: map[int16]*replication.GraphLayout{},
		seedData: map[[2]int]replication.StateData{}, frame: cf, inputs: map[uint32]wire.InputEntry{},
		Unencodable: map[string]int{}}
	sm.SetClock(cf, int64(cf)*16)
	fail := func(err error) (*hero, error) { sm.Close(); return nil, err }
	for _, in := range st.Instances {
		g := statescript.GraphGUID(in.Graph.Index)
		if _, err := sm.ImportInstance(g, int16(in.ID), int16(in.Parent), uint32(in.Slot)); err != nil {
			return fail(fmt.Errorf("instance %d (graph %#x): %w", in.ID, in.Graph.Index, err))
		}
		l, err := r.e.layout(g)
		if err != nil {
			return fail(err)
		}
		h.layouts[int16(in.ID)] = l
	}
	for _, v := range heroes.Final(st.Owner) {
		if sv, ok := ssValue(v.Value); ok {
			if err := sm.SetVar(0, statescript.VarID(uint64(v.ID)), sv); err != nil {
				return fail(fmt.Errorf("owner var %#x: %w", v.ID, err))
			}
		}
	}
	if hk, hv := st.WeaponDict(); len(hk) > 0 { // the valid weapon indices, a dictionary (as the client gets it)
		var ks, vs []statescript.Value
		for i := range hk {
			ks = append(ks, statescript.Value{Type: statescript.Int, Bits: hk[i].Bits})
			vs = append(vs, statescript.Value{Type: statescript.Int, Bits: hv[i].Bits})
		}
		if err := sm.SetDict(0, statescript.VarID(heroes.VarWeaponList), ks, vs); err != nil {
			return fail(fmt.Errorf("owner var $1d: %w", err))
		}
	}
	for _, in := range st.Instances {
		for _, v := range heroes.Final(in.Vars) {
			if sv, ok := ssValue(v.Value); ok {
				if err := sm.SetVar(int16(in.ID), statescript.VarID(uint64(v.ID)), sv); err != nil {
					return fail(fmt.Errorf("instance %d var %#x: %w", in.ID, v.ID, err))
				}
			}
		}
	}
	for _, in := range st.Instances {
		for _, a := range in.Active {
			d := replication.ActiveData(a)
			if err := sm.ImportState(int16(in.ID), a.State, d.Bits, d.N); err != nil {
				if errors.Is(err, statescript.ErrUnimplemented) {
					h.Unencodable[fmt.Sprintf("import %#x", in.Graph.StateTypes[a.State])]++
					continue
				}
				return fail(fmt.Errorf("instance %d state %d: %w", in.ID, a.State, err))
			}
			h.seedData[[2]int{int(in.ID), a.State}] = d
		}
	}
	h.ghost = replication.NewLocalGhost(cf, h.snapshot())
	return h, nil
}

// ssValue converts a start-up value (strings cannot be passed).
func ssValue(v heroes.Value) (statescript.Value, bool) {
	switch v.Type {
	case heroes.TypeByte, heroes.TypeInt, heroes.TypeFloat, heroes.TypeInt64:
		return statescript.Value{Type: statescript.Type(v.Type), Bits: v.Bits}, true
	}
	return statescript.Value{}, false
}

// HeroValue converts a runtime value to the encoders' type (false: not syncable here).
func HeroValue(v statescript.Value) (heroes.Value, bool) { return heroValue(v) }

// heroValue converts a runtime value to the encoders' type (false: not syncable here).
func heroValue(v statescript.Value) (heroes.Value, bool) {
	switch v.Type {
	case statescript.Byte:
		return heroes.Value{Type: heroes.TypeByte, Bits: v.Bits & 0xff}, true
	case statescript.Int:
		return heroes.Value{Type: heroes.TypeInt, Bits: uint64(int64(int32(v.Bits)))}, true
	case statescript.Float:
		f := math.Float32frombits(uint32(v.Bits))
		return heroes.Float(f), true
	case statescript.Int64:
		return heroes.Value{Type: heroes.TypeInt64, Bits: v.Bits}, true
	}
	return heroes.Value{}, false
}

// snapshot reads the sync manager's state as the local format carries it. A state
// whose data the runtime cannot produce yet keeps what the client was last sent
// (its start-up data, or its absence).
func (h *hero) snapshot() *replication.ScriptState {
	// State data is written at the runtime's clock after Run(f, 1), the end of the step window (f*16 + 15), as the
	// client's own writer runs. The one clock-dependent field, the Anim time, counts from the animation's play start
	// at the end of the window the state began in (the client starts it from its post-run list, T054 G:0xd99c60), so
	// it matches there; the earlier CF*16 override only hid that for states begun at a window's first ms.
	s := &replication.ScriptState{Owner: map[uint32]heroes.Value{}}
	for _, v := range h.sm.Vars(0) {
		if hv, ok := heroValue(v.Value); ok {
			s.Owner[varLow(v.ID)] = hv
		}
	}
	s.OwnerUsers = varUsers(h.sm.VarUsers(0))
	for _, id := range h.sm.Instances() {
		info, err := h.sm.InstanceInfo(id)
		l := h.layouts[id]
		if err != nil || l == nil || id <= 0 {
			continue // local (negative) instances are the server's own
		}
		in := replication.InstanceState{ID: uint16(id), Layout: l, Parent: uint16(info.Parent), Slot: info.Slot,
			Vars: map[uint32]heroes.Value{}, States: map[int]replication.StateData{}}
		for _, v := range h.sm.Vars(id) {
			if hv, ok := heroValue(v.Value); ok {
				in.Vars[varLow(v.ID)] = hv
			}
		}
		in.Users = varUsers(h.sm.VarUsers(id))
		var last *replication.ScriptState
		if h.ghost != nil {
			last = h.ghost.LastState()
		}
		for _, st := range h.sm.ActiveStates(id) {
			changes, _ := h.sm.StateChanges(id, st)
			refs, _ := h.sm.StateDataRefs(id, st, true)
			b, n, err := h.sm.StateDataFormat(id, st, true, false)
			if err == nil {
				// A BooleanSwitch goes out with every delta: the client's rollback (the 40 CF prediction timeout,
				// StatescriptSync.md §5.3/§5.5) restores its mirrored value from the client's own earlier prediction,
				// and nothing else would correct it while the server's value doesn't change (T054: 0x304 s0 / s56
				// dropped to 0 40 CF after the lock-in, measured; the deflect CosmeticEntity s43 then never toggled).
				// Its reader (G:0xf55560) does nothing when the bit equals the stored value, so re-sending is safe.
				if st < len(l.Types) && l.Types[st] == typeBooleanSwitch {
					changes += h.frame
				}
				in.States[st] = replication.StateData{Bits: b, N: n, Changes: changes, Refs: refs}
				continue
			}
			// No data from the runtime (an echo type begun on the server): the state is frozen (Layout.Frozen)
			// unless the policy is broken; keep what the client was last sent so nothing guessed goes out.
			if was := findInstance(last, uint16(id)); was != nil {
				if d, ok := was.States[st]; ok {
					in.States[st] = d
				}
			}
			if !l.Frozen[st] {
				h.Unencodable[fmt.Sprintf("graph %#x state %d", l.Index, st)]++
			}
		}
		// "has update" states that were active in the last snapshot and aren't now: their end goes out
		// with their data.
		if was := findInstance(last, uint16(id)); was != nil {
			for st := range was.States {
				if _, on := in.States[st]; on || st >= len(l.States) || l.States[st] != 'S' {
					continue
				}
				if b, n, err := h.sm.StateDataFormat(id, st, true, false); err == nil {
					if in.Ended == nil {
						in.Ended = map[int]replication.StateData{}
					}
					in.Ended[st] = replication.StateData{Bits: b, N: n}
				}
			}
		}
		for _, ev := range h.sm.Events(id) {
			in.Events = append(in.Events, replication.Event{Kind: int(ev.Kind), State: ev.State, Data: ev.Data, Time: ev.Time})
		}
		s.Instances = append(s.Instances, in)
	}
	sort.Slice(s.Instances, func(i, j int) bool { return s.Instances[i].ID < s.Instances[j].ID })
	// SYNC_ALL actions executed since the client's acknowledged base (a union carries all of them; the client
	// skips the CFs it has applied, §2.9). Others are never transmitted (Statescript.md §9.1).
	if h.ghost != nil {
		acts, _ := h.sm.Actions(h.ghost.BaseCF)
		for _, a := range acts {
			if a.Remote >= 0 && a.Instance > 0 {
				s.Actions = append(s.Actions, replication.Action{CF: a.CF, Instance: uint16(a.Instance), Node: a.Node})
			}
		}
	}
	return s
}

// varUsers converts the runtime's var writers (instance 0 / negative = none on the wire; local instances are the
// server's own, the encoder names only instances the client knows).
func varUsers(us map[uint64]statescript.VarUser) map[uint32]replication.VarUser {
	var out map[uint32]replication.VarUser
	for id, u := range us {
		if u.Instance <= 0 {
			continue
		}
		if out == nil {
			out = map[uint32]replication.VarUser{}
		}
		out[varLow(id)] = replication.VarUser{Instance: uint16(u.Instance), State: int(u.State), Priority: u.Priority != 0,
			StateDefined: u.StateDefined, Extra: uint16(u.Extra)}
	}
	return out
}

func findInstance(s *replication.ScriptState, id uint16) *replication.InstanceState {
	if s == nil {
		return nil
	}
	for i := range s.Instances {
		if s.Instances[i].ID == id {
			return &s.Instances[i]
		}
	}
	return nil
}

func (r *Runtime) RemoveHero(body uint32) {
	if h := r.heroes[body]; h != nil {
		h.sm.Close()
		delete(r.heroes, body)
	}
}

func (r *Runtime) Input(body uint32, in []wire.InputEntry) {
	h := r.heroes[body]
	if h == nil {
		return
	}
	for _, e := range in {
		if int32(e.Frame-h.frame) > 0 {
			h.inputs[e.Frame] = e
		}
	}
}

// Tick simulates, per hero, every frame whose input has arrived (the client runs
// ahead of P, so its input for a frame arrives before the server's own frame gets
// there). A frame missing from the history (never seen: lost packets are resent in
// the history until acknowledged) takes the previous frame's input.
func (r *Runtime) Tick(frame uint32) {
	r.frame = frame
	for _, h := range r.heroes {
		newest := h.frame
		for f := range h.inputs {
			if int32(f-newest) > 0 {
				newest = f
			}
		}
		if int32(newest-h.frame) > maxCatchUp {
			h.Skipped += int(newest - h.frame - maxCatchUp)
			h.frame = newest - maxCatchUp
		}
		for h.frame != newest {
			f := h.frame + 1
			in, ok := h.inputs[f]
			if !ok {
				in = h.last
				in.Frame = f
			}
			delete(h.inputs, f)
			if err := h.sm.Input(f, in.Buttons, in.Extra); err != nil {
				h.InputErrs++
				if h.inputErr == nil {
					h.inputErr = err
					r.e.log.Warn("sim: input not applied", "body", fmt.Sprintf("%#x", h.body), "err", err)
				}
			}
			h.sm.Run(f, 1)
			h.last, h.frame = in, f
			h.Simulated++
			r.keepSnapshot(h, f)
		}
		for f := range h.inputs {
			if int32(f-h.frame) <= 0 {
				delete(h.inputs, f)
			}
		}
	}
}

func (r *Runtime) Pending(body uint32) bool {
	h := r.heroes[body]
	return h != nil && (int32(h.frame-h.ghost.LastEnd()) > 0 || h.resend)
}

// Delta encodes the union [acked base, frame] (replication.LocalGhost): start = the end of the newest packet the
// client acknowledged, so a lost packet leaves no gap. It returns the packet and its end CF, which the caller
// reports back with Acked / Lost.
func (r *Runtime) Delta(body uint32) (*obits.Writer, uint32) {
	h := r.heroes[body]
	if h == nil || !r.Pending(body) {
		return nil, 0
	}
	prev, start := h.ghost.LastState(), h.ghost.BaseCF
	cur := h.snapshot()
	w, err := h.ghost.Next(h.frame, cur)
	if err != nil {
		r.e.log.Error("sim: delta", "body", fmt.Sprintf("%#x", body), "err", err)
		return nil, 0
	}
	h.resend = false
	h.recent = append(h.recent, DeltaRecord{Start: start, End: h.frame, Bits: int(w.Len()),
		Hex: hex.EncodeToString(w.Buf()), Changes: describe(prev, h.ghost.LastState())})
	if len(h.recent) > maxRecent {
		h.recent = h.recent[len(h.recent)-maxRecent:]
	}
	h.Deltas++
	if r.Unsent {
		h.ghost.Acked(h.frame)
	}
	return w, h.frame
}

// Acked: the packet carrying body's delta ending at end was acknowledged by the client's transport.
func (r *Runtime) Acked(body, end uint32) {
	if h := r.heroes[body]; h != nil {
		h.ghost.Acked(end)
	}
}

// Lost: a packet carrying one of body's deltas was lost; the next delta (a union) covers it, even without new
// frames.
func (r *Runtime) Lost(body uint32) {
	if h := r.heroes[body]; h != nil && h.ghost.Unacked() {
		h.resend = true
		h.Lost++
	}
}

// DeltaRecord is a delta the host encoded: its CF range, packet bits (hex, LSB
// first) and what changed.
type DeltaRecord struct {
	Start, End uint32
	Bits       int
	Hex        string
	Changes    string
}

const maxRecent = 128

// RecentDeltas returns body's last deltas, oldest first.
func (r *Runtime) RecentDeltas(body uint32) ([]DeltaRecord, error) {
	h := r.heroes[body]
	if h == nil {
		return nil, errors.New("no such hero")
	}
	return append([]DeltaRecord(nil), h.recent...), nil
}

// describe summarises the changes between two snapshots.
func describe(prev, cur *replication.ScriptState) string {
	var sb strings.Builder
	for _, in := range cur.Instances {
		was := findInstance(prev, in.ID)
		if was == nil {
			fmt.Fprintf(&sb, " new i%d g%#x;", in.ID, in.Layout.Index)
			continue
		}
		for _, st := range sortedStates(in.States) {
			d := in.States[st]
			od, ok := was.States[st]
			switch {
			case !ok:
				fmt.Fprintf(&sb, " i%d +s%d(%c %db);", in.ID, st, in.Layout.States[st], d.N)
			case od.N != d.N || hex.EncodeToString(od.Bits) != hex.EncodeToString(d.Bits):
				fmt.Fprintf(&sb, " i%d ~s%d(%c %d>%db);", in.ID, st, in.Layout.States[st], od.N, d.N)
			}
		}
		for _, st := range sortedStates(was.States) {
			if _, ok := in.States[st]; !ok {
				fmt.Fprintf(&sb, " i%d -s%d(%c);", in.ID, st, in.Layout.States[st])
			}
		}
		for id, v := range in.Vars {
			if o, ok := was.Vars[id]; !ok || o != v {
				fmt.Fprintf(&sb, " i%d $%x=%#x;", in.ID, id, v.Bits)
			}
		}
	}
	for _, in := range prev.Instances {
		if findInstance(cur, in.ID) == nil {
			fmt.Fprintf(&sb, " gone i%d;", in.ID)
		}
	}
	return sb.String()
}

func sortedStates(m map[int]replication.StateData) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// HeroStats is one hero's simulation (control API).
type HeroStats struct {
	Body, Def, Frame, SentCF, AckedCF uint32
	Instances                         int
	Simulated, Deltas, Skipped, Lost  int
	InputErrs                         int
	InputErr                          string         `json:",omitempty"`
	Unencodable                       map[string]int `json:",omitempty"`
	UnmodelledNodes, UnmodelledStates map[string]int `json:",omitempty"`
	// Frozen: synced states the server doesn't correct, "type (reason)" -> count (policy, §10.3).
	Frozen    map[string]int `json:",omitempty"`
	Corrected int            // synced states the server corrects
}

func (r *Runtime) Stats() any {
	var out []HeroStats
	for _, h := range r.heroes {
		s := HeroStats{Body: h.body, Def: h.def, Frame: h.frame, SentCF: h.ghost.LastEnd(), AckedCF: h.ghost.BaseCF, Lost: h.Lost, Instances: len(h.sm.Instances()),
			Simulated: h.Simulated, Deltas: h.Deltas, Skipped: h.Skipped, InputErrs: h.InputErrs, Unencodable: h.Unencodable,
			UnmodelledNodes:  hexKeys(h.sm.Unmodelled(statescript.UnmodelledNodes)),
			UnmodelledStates: hexKeys(h.sm.Unmodelled(statescript.UnmodelledStates))}
		s.Frozen, s.Corrected = r.policyReport(h)
		if h.inputErr != nil {
			s.InputErr = h.inputErr.Error()
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Body < out[j].Body })
	return out
}

// policyReport counts a hero's synced states by the policy's decision.
func (r *Runtime) policyReport(h *hero) (map[string]int, int) {
	frozen, corrected := map[string]int{}, 0
	for _, l := range h.layouts {
		for i, k := range l.States {
			if k != '.' && k != 'S' {
				continue
			}
			if !l.Frozen[i] {
				corrected++
				continue
			}
			_, why := r.e.Corrects(l.Types[i], true)
			if why == "" {
				why = "shadow evidence"
			}
			frozen[fmt.Sprintf("%08x (%s)", l.Types[i], why)]++
		}
	}
	return frozen, corrected
}

func hexKeys(m map[uint32]int) map[string]int {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[fmt.Sprintf("%08x", k)] = v
	}
	return out
}

func (r *Runtime) Close() {
	for body := range r.heroes {
		r.RemoveHero(body)
	}
}

// Trace enables the runtime trace of body's sync manager; DrainTrace returns it
// (JSON lines, the ss_trace vocabulary).
func (r *Runtime) Trace(body uint32, on bool) error {
	h := r.heroes[body]
	if h == nil {
		return errors.New("no such hero")
	}
	return h.sm.Trace(on)
}

func (r *Runtime) DrainTrace(body uint32) (string, error) {
	h := r.heroes[body]
	if h == nil {
		return "", errors.New("no such hero")
	}
	return h.sm.DrainTrace(), nil
}

// HeroView is a cheap per-tick summary of a hero's simulation for the live server view
// (docs/DebugDrawing.md §5): counters and the sync manager to read the instances from.
type HeroView struct {
	Frame, SentCF, AckedCF                      uint32
	Simulated, Deltas, Skipped, InputErrs, Lost int
	SM                                          *statescript.SyncManager
}

// HeroView reports body's simulation; false when the body has none.
func (r *Runtime) HeroView(body uint32) (HeroView, bool) {
	h := r.heroes[body]
	if h == nil {
		return HeroView{}, false
	}
	return HeroView{Frame: h.frame, SentCF: h.ghost.LastEnd(), AckedCF: h.ghost.BaseCF, Simulated: h.Simulated, Deltas: h.Deltas,
		Skipped: h.Skipped, InputErrs: h.InputErrs, Lost: h.Lost, SM: h.sm}, true
}
