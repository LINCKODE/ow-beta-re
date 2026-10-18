package replication

import (
	"fmt"
	"slices"

	"owserver/inst/bits"
)

// LocalGhost is what one client knows of one entity's Statescript in the local format (the GDC's
// StatescriptGhost, Statescript.md §7.2), driven by transport acks (docs/StatescriptSync.md §1: the client sends
// no Statescript ack; §6.1):
//
//   - Base is the state at BaseCF, the end of the newest packet the client acknowledged. The client has applied
//     at least that (it may have applied more: acks can be lost).
//   - Every packet since is a union from BaseCF to the newest simulated frame: start = BaseCF, so start <= the
//     client's last applied end + 1 always holds, and a lost packet leaves no gap: the next one covers it and
//     replaces the queued lone packets it covers (§3.1). Nothing is ever resent as it was.
//   - A union names everything that changed in any frame since BaseCF, with its value at the end (the client may
//     hold an intermediate value from a packet whose ack was lost: a var changed and changed back must be sent).
//   - An instance id whose record (graph, parent, slot) changes is destroyed first and created again only once
//     no packet the client may have applied still names the old record (§2.3: a known id's record must match, or
//     the sub-frame fails and the client waits behind it forever).
type LocalGhost struct {
	BaseCF uint32
	Base   *ScriptState
	sent   []sentState // packets since Base, oldest first
}

type sentState struct {
	end     uint32
	state   *ScriptState // what that packet carried (after holding back conflicting instances)
	touched *Touched     // what changed from the previous packet's state to this one
}

// NewLocalGhost starts from the state a full update ending at cf carries (the client gets it reliably:
// deltas starting at cf wait behind it, §3.2).
func NewLocalGhost(cf uint32, s *ScriptState) *LocalGhost { return &LocalGhost{BaseCF: cf, Base: s} }

// Touched marks the parts of a ScriptState that changed at some frame of a union.
type Touched struct {
	Owner     map[uint32]bool
	Instances map[uint16]*InstanceTouched
}

// InstanceTouched is one instance's changed parts. Restate: the instance was destroyed or its record changed
// at some frame, so the client may not know it: it is sent as a new record with everything.
type InstanceTouched struct {
	Restate bool
	Vars    map[uint32]bool
	States  map[int]bool
}

func (t *Touched) inst(id uint16) *InstanceTouched {
	if t.Instances == nil {
		t.Instances = map[uint16]*InstanceTouched{}
	}
	it := t.Instances[id]
	if it == nil {
		it = &InstanceTouched{Vars: map[uint32]bool{}, States: map[int]bool{}}
		t.Instances[id] = it
	}
	return it
}

// Merge adds o's marks to t.
func (t *Touched) Merge(o *Touched) {
	if o == nil {
		return
	}
	for id := range o.Owner {
		if t.Owner == nil {
			t.Owner = map[uint32]bool{}
		}
		t.Owner[id] = true
	}
	for id, oi := range o.Instances {
		it := t.inst(id)
		it.Restate = it.Restate || oi.Restate
		for v := range oi.Vars {
			it.Vars[v] = true
		}
		for s := range oi.States {
			it.States[s] = true
		}
	}
}

func sameRecord(a, b *InstanceState) bool {
	return a.Layout.Index == b.Layout.Index && a.Parent == b.Parent && a.Slot == b.Slot
}

// Diff marks what changed from prev to cur (the same rules as LocalDelta's "changed").
func Diff(prev, cur *ScriptState) *Touched {
	t := &Touched{}
	var prevOwner = map[uint32]bool{}
	if prev != nil {
		for id, v := range prev.Owner {
			prevOwner[id] = true
			if cv, ok := cur.Owner[id]; !ok || !sameValue(cv, v) || prev.OwnerUsers[id] != cur.OwnerUsers[id] {
				if t.Owner == nil {
					t.Owner = map[uint32]bool{}
				}
				t.Owner[id] = true
			}
		}
	}
	for id := range cur.Owner {
		if !prevOwner[id] {
			if t.Owner == nil {
				t.Owner = map[uint32]bool{}
			}
			t.Owner[id] = true
		}
	}
	for i := range cur.Instances {
		in := &cur.Instances[i]
		was := prev.find(in.ID)
		if was == nil || !sameRecord(was, in) {
			t.inst(in.ID).Restate = true
			continue
		}
		var it *InstanceTouched
		mark := func() *InstanceTouched {
			if it == nil {
				it = t.inst(in.ID)
			}
			return it
		}
		for id, v := range in.Vars {
			if o, ok := was.Vars[id]; !ok || !sameValue(o, v) || was.Users[id] != in.Users[id] {
				mark().Vars[id] = true
			}
		}
		for id := range was.Vars {
			if _, ok := in.Vars[id]; !ok {
				mark().Vars[id] = true
			}
		}
		for s, d := range in.States {
			od, ok := was.States[s]
			if !ok || od.Changes != d.Changes || (DataChanges && !in.Layout.TimeData[s] && !d.equal(od)) {
				mark().States[s] = true
			}
		}
		for s := range was.States {
			if _, ok := in.States[s]; !ok {
				mark().States[s] = true
			}
		}
	}
	if prev != nil {
		for i := range prev.Instances {
			if cur.find(prev.Instances[i].ID) == nil {
				t.inst(prev.Instances[i].ID).Restate = true // destroyed (a later incarnation must be restated)
			}
		}
	}
	return t
}

// Next encodes the union packet [BaseCF, end] that brings the client to cur (the state at the end of end), and
// records it as sent. The caller reports the packet's fate with Acked / Lost.
func (g *LocalGhost) Next(end uint32, cur *ScriptState) (*bits.Writer, error) {
	if int32(end-g.BaseCF) <= 0 {
		return nil, fmt.Errorf("replication: ghost: end %d not after base %d", end, g.BaseCF)
	}
	cur = g.holdBack(cur)
	prev := g.Base
	if n := len(g.sent); n > 0 {
		prev = g.sent[n-1].state
	}
	touched := Diff(prev, cur)
	union := &Touched{}
	for _, s := range g.sent {
		union.Merge(s.touched)
	}
	union.Merge(touched)
	w, err := localDelta(g.BaseCF, end, g.Base, cur, union)
	if err != nil {
		return nil, err
	}
	g.sent = append(g.sent, sentState{end: end, state: cur, touched: touched})
	return w, nil
}

// holdBack drops from cur the instances whose record differs from one the client may still hold (Base or any
// packet since), and the children of dropped instances: they are destroyed now and created by a later packet.
func (g *LocalGhost) holdBack(cur *ScriptState) *ScriptState {
	conflict := map[uint16]bool{}
	check := func(s *ScriptState) {
		if s == nil {
			return
		}
		for i := range cur.Instances {
			in := &cur.Instances[i]
			if was := s.find(in.ID); was != nil && !sameRecord(was, in) {
				conflict[in.ID] = true
			}
		}
	}
	check(g.Base)
	for _, s := range g.sent {
		check(s.state)
	}
	if len(conflict) > 0 {
		cur = without(cur, conflict)
	}
	return g.holdStates(cur)
}

// without is cur minus the instances in drop and their children (a new entry's parent must be known, §2.3).
func without(cur *ScriptState, conflict map[uint16]bool) *ScriptState {
	for changed := true; changed; { // children of held-back instances (their parent must be known, §2.3)
		changed = false
		for i := range cur.Instances {
			in := &cur.Instances[i]
			if in.Parent != 0 && conflict[in.Parent] && !conflict[in.ID] {
				conflict[in.ID], changed = true, true
			}
		}
	}
	out := &ScriptState{Owner: cur.Owner}
	for _, in := range cur.Instances {
		if !conflict[in.ID] {
			out.Instances = append(out.Instances, in)
		}
	}
	return out
}

// holdStates keeps, as the client was last sent them, the states the server doesn't correct (Layout.Frozen)
// and the states whose data names an instance the client won't know after this packet (StateData.Refs; ids <= 0
// are the server's own local instances). The recorded state is then what the client really got.
func (g *LocalGhost) holdStates(cur *ScriptState) *ScriptState {
	last := g.LastState()
	known := map[int16]bool{}
	for _, in := range cur.Instances {
		known[int16(in.ID)] = true
	}
	hold := func(in *InstanceState, s int, d StateData, active bool) bool {
		if in.Layout.Frozen[s] {
			return true
		}
		if active {
			for _, r := range d.Refs {
				if r <= 0 || !known[r] {
					return true
				}
			}
		}
		return false
	}
	var out *ScriptState
	for i := range cur.Instances {
		in := &cur.Instances[i]
		was := last.find(in.ID)
		if was != nil && !sameRecord(was, in) {
			was = nil
		}
		var states, ended map[int]StateData
		keep := func(s int) { // s as last sent
			if states == nil {
				states, ended = cloneStates(in.States), cloneStates(in.Ended)
			}
			delete(states, s)
			delete(ended, s)
			if was != nil {
				if d, ok := was.States[s]; ok {
					states[s] = d
				}
			}
		}
		for s, d := range in.States {
			if hold(in, s, d, true) {
				keep(s)
			}
		}
		for s := range in.Ended {
			if hold(in, s, StateData{}, false) {
				keep(s)
			}
		}
		if was != nil {
			for s := range was.States { // ended on the server, frozen: still active as sent
				if _, on := in.States[s]; !on && in.Layout.Frozen[s] {
					keep(s)
				}
			}
		}
		if states == nil {
			continue
		}
		if out == nil {
			out = &ScriptState{Owner: cur.Owner, Instances: slices.Clone(cur.Instances)}
		}
		out.Instances[i].States, out.Instances[i].Ended = states, ended
	}
	if out == nil {
		return cur
	}
	return out
}

func cloneStates(m map[int]StateData) map[int]StateData {
	out := make(map[int]StateData, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Acked: the packet ending at end arrived; it becomes the base. Lost packets need nothing: the next union
// covers them (the caller sends one even without new frames, Unacked).
func (g *LocalGhost) Acked(end uint32) {
	for i, s := range g.sent {
		if s.end == end {
			g.Base, g.BaseCF = s.state, s.end
			g.sent = g.sent[i+1:]
			return
		}
	}
}

// Unacked reports whether packets since the base are unacknowledged.
func (g *LocalGhost) Unacked() bool { return len(g.sent) > 0 }

// LastEnd is the end of the newest packet encoded (BaseCF if none since).
func (g *LocalGhost) LastEnd() uint32 {
	if n := len(g.sent); n > 0 {
		return g.sent[n-1].end
	}
	return g.BaseCF
}

// LastState is the newest state encoded (Base if none since).
func (g *LocalGhost) LastState() *ScriptState {
	if n := len(g.sent); n > 0 {
		return g.sent[n-1].state
	}
	return g.Base
}
