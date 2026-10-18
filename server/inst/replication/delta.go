package replication

import (
	"bytes"
	"fmt"
	"slices"

	"owserver/inst/bits"
	"owserver/internal/heroes"
)

// Local-format Statescript deltas (stream 1, start CF != 0; the client reads them with
// FUN_7ff650ced600): what changed on the entity the client controls between the
// packet's start and end CF. The layout is the one Prometheus' ss_serialize_local
// writes with delta=true and baseline "diff", which the client accepted from
// server_echo, except for the header's rollback bit and CF
// (FUN_7ff650ce89f0 / cec7b0 [G]):
//
//	bit 0 (ignored), bit 1 (local), bit 1 (rollback, pkt+0x51), count(CF) (pkt+0x4c)
//
// With rollback set the client undoes every prediction it recorded, sets its clock to
// CF, applies the packet (the server's state at the end of CF), and replays its stored
// input after CF: that is the confirmation. Without it (server_echo sent 0 and CF 0 =
// the client's own CF) the packet is applied on top of the predictions, which the
// client then rolls back itself 40 CF after they were recorded (FUN_7ff650cea070).
// The client applies a delta when start <= its last applied end CF + 1 and end >
// it; a gap waits for a packet that fills it (FUN_7ff650cec290).
//
//	instances (FUN_7ff650cecab0): per instance small(id delta), then
//	  1 = destroyed (cecba0) [?: never sent by the echo]
//	  0 0 = known, unchanged; 0 1 + record = new (the full update's record)
//	small(0)
//	owner vars: count(id) + value per changed var, count(0)
//	per instance (not destroyed), in list order:
//	  sync vars (cee790): a changed set over every m_syncVars slot, then the
//	    changed values in slot order; other vars count(id) + value, count(0)
//	  states (cee430): a changed set over every m_states entry, then per changed
//	    state: ["has update" types: bit 1] active bit, if active the state data and
//	    one bit (read and dropped)
//	bit 1: events follow; per instance bit 0 + small(id delta) + its pending events
//	  (they replace the client's), bit 0 + small(0) ends; bit 0 (no further segment)
//	bit 0 (FUN_7ff650cee920: nothing)
//
// A changed set (FUN_7ff651050c90): n <= 8: n bits; else a present bit per 8-bit
// chunk, then each present chunk's bits.

// GraphLayout is what the local format needs to know about a graph.
type GraphLayout struct {
	Index     uint16   // graph 0x058...|Index
	SyncSlots []uint32 // var id per m_syncVars slot (0 = a slot without a var); all slots, eligible or not
	States    string   // per m_states: '.' synced, 'C' client-only, 'V' server-only, 'S' "has update" type
	Types     []uint32 // STU type per m_states entry
	IdxBits   int      // width of a state index in the events tail (graph +0x17C)
	NodeBits  int      // width of an m_nodes index in the actions section (graph +0x178)
	// TimeData marks states whose data moves with time while active (Ability cooldown, WeaponVolley, ChaseVar,
	// Anim): only their lifetime / semantic changes (StateData.Changes) are sent, as re-applying them restarts
	// what the client animates from them (the cooldown radial, the volley's per-shot ammo) [H, T051].
	// Unused once the runtime counts every client-visible data change in Changes (DataChanges = false).
	TimeData map[int]bool
	// Frozen marks states the server does not correct (docs/InstanceServer.md §10.3: the runtime can't produce
	// them exactly): a LocalGhost keeps them as the client was last sent them.
	Frozen map[int]bool
}

// DataChanges also marks an active state changed when its data bits differ. Off: the
// runtime counts every client-visible data change in StateData.Changes
// (ss_sm_state_changes, T054) and leaves out what only moves with time; comparing bits
// here would re-send time-moving data (WeaponVolley, ChaseVar, Ability cooldown) every
// tick, and each re-apply restarts what the client animates from it (the per-shot ammo,
// the cooldown radial: T051, and the T053 play test [H]).
var DataChanges = false

// StateData is an active state's local-format state data.
type StateData struct {
	Bits []byte // LSB first
	N    int
	// Changes counts the state's lifetime changes (begin, re-enter, end). A delta
	// marks a state changed when it was activated / deactivated or this moved; data
	// that only moves with time (WeaponVolley, Anim, Ability timers) is not re-sent,
	// as the client's own sync records only lifetime changes (Statescript.md §7.1).
	// Re-sending a running WeaponVolley every frame stops its projectiles (T051).
	Changes uint32
	// Refs are the instance ids the data names (a stack predecessor): a LocalGhost holds the state back while
	// one of them is not among the instances the packet leaves the client with (unknown ids crash it, §4.3).
	Refs []int16
}

// Equal compares the data bits.
func (d StateData) Equal(o StateData) bool { return d.equal(o) }

func (d StateData) equal(o StateData) bool {
	if d.N != o.N {
		return false
	}
	return bytes.Equal(bits.FromBytes(d.Bits, uint(d.N)).Buf(), bits.FromBytes(o.Bits, uint(o.N)).Buf())
}

// Event kinds of the events tail.
const (
	EventTimer  = 1 // Data = the timer's user data
	EventFinish = 2 // Lifetime finish
)

// Event is a pending notification the local format carries.
type Event struct {
	Kind  int
	State int
	Data  int32
	Time  int64 // ms
}

// VarUser is a var's writer (sync spec §2.7 state reference, G:cf1520): the writing state (instance, m_states
// index), its write priority (one bit on the wire), and state-defined (readers see the state's own value, Extra
// = which one: a running WeaponVolley's per-shot ammo). Instance 0 = no writer.
type VarUser struct {
	Instance     uint16
	State        int
	Priority     bool
	StateDefined bool
	Extra        uint16
}

// InstanceState is one instance of a ScriptState.
type InstanceState struct {
	ID     uint16
	Layout *GraphLayout
	Parent uint16
	Slot   uint32
	Vars   map[uint32]heroes.Value
	Users  map[uint32]VarUser // writers of Vars (absent = none)
	States map[int]StateData // active states (m_states index) with their data
	Ended  map[int]StateData // "has update" states that are not active, with their last data (their end is sent with it)
	Events []Event           // pending, in queue order
}

// ScriptState is an entity's Statescript as the local format carries it at a
// command frame.
type ScriptState struct {
	Owner      map[uint32]heroes.Value // the entity (owner) bag
	OwnerUsers map[uint32]VarUser     // writers of Owner vars
	Instances []InstanceState         // ascending ids
	// Actions are SYNC_ALL action nodes the server executed (in order), for the actions section (§2.9): a
	// packet carries those after its start CF (the client skips a timed block whose CF it has applied).
	Actions []Action
}

// Action is an executed action node: its command frame, instance and m_nodes index.
type Action struct {
	CF       uint32
	Instance uint16
	Node     int
}

func (s *ScriptState) find(id uint16) *InstanceState {
	if s == nil {
		return nil
	}
	i, ok := slices.BinarySearchFunc(s.Instances, id, func(in InstanceState, id uint16) int { return int(in.ID) - int(id) })
	if !ok {
		return nil
	}
	return &s.Instances[i]
}

// changedSet writes a changed set.
func changedSet(w *bits.Writer, set []bool) {
	if len(set) <= 8 {
		w.Bools(set...)
		return
	}
	chunks := (len(set)-1)/8 + 1
	present := make([]bool, chunks)
	for i, b := range set {
		if b {
			present[i/8] = true
		}
	}
	w.Bools(present...)
	for c := range chunks {
		if present[c] {
			w.Bools(set[c*8 : min(len(set), c*8+8)]...)
		}
	}
}

func sameValue(a, b heroes.Value) bool { return a.Type == b.Type && a.Bits == b.Bits }

func instanceRecord(p *bits.Writer, in *InstanceState) {
	p.Bits(uint32(in.Layout.Index), 16)
	p.Bits(0, 1) // default owner value
	if in.Parent != 0 {
		p.Bits(1, 1)
		count(p, uint32(in.Parent))
		count(p, in.Slot)
	} else {
		p.Bits(0, 1)
	}
}

// LocalDelta encodes what changed from prev (the state at startCF, which the client
// has) to cur (the state at the end of endCF) as a local-format rollback delta.
func LocalDelta(startCF, endCF uint32, prev, cur *ScriptState) (*bits.Writer, error) {
	return localDelta(startCF, endCF, prev, cur, nil)
}

// localDelta is LocalDelta for a union (LocalGhost): union marks what changed at some frame between prev and
// cur; it is sent with its value in cur even where cur equals prev. A restated instance (union Restate: the
// client may or may not know it) goes out as a new record with every var and every state's activity.
func localDelta(startCF, endCF uint32, prev, cur *ScriptState, union *Touched) (*bits.Writer, error) {
	if union == nil {
		union = &Touched{}
	}
	restated := func(id uint16) bool { it := union.Instances[id]; return it != nil && it.Restate }
	if startCF == 0 || endCF < startCF {
		return nil, fmt.Errorf("replication: delta CF range %d..%d", startCF, endCF)
	}
	var p bits.Writer
	p.Bits(0, 1)     // (ignored)
	p.Bits(1, 1)     // local
	p.Bits(1, 1)     // rollback
	count(&p, endCF) // the CF this state is the end of

	// Instance list: cur's instances and prev's destroyed ones, by id.
	type item struct {
		id        uint16
		destroyed bool
	}
	var list []item
	for _, in := range cur.Instances {
		list = append(list, item{id: in.ID})
	}
	if prev != nil {
		for _, in := range prev.Instances {
			if cur.find(in.ID) == nil {
				list = append(list, item{id: in.ID, destroyed: true})
			}
		}
	}
	for id, it := range union.Instances {
		// Destroyed at some frame and not in cur: the client may hold it from a packet it applied (a destroy
		// of an id it doesn't know is a no-op, G:cecba0 / G:ced2f0).
		if it.Restate && cur.find(id) == nil && prev.find(id) == nil {
			list = append(list, item{id: id, destroyed: true})
		}
	}
	slices.SortFunc(list, func(a, b item) int { return int(a.id) - int(b.id) })
	prevID := uint16(0)
	for _, it := range list {
		if it.id <= prevID {
			return nil, fmt.Errorf("replication: instance ids must increase (%d after %d)", it.id, prevID)
		}
		small(&p, uint32(it.id-prevID))
		prevID = it.id
		if it.destroyed {
			p.Bits(1, 1)
			continue
		}
		p.Bits(0, 1)
		in := cur.find(it.id)
		if was := prev.find(it.id); was != nil && sameRecord(was, in) && !restated(it.id) {
			p.Bits(0, 1)
			continue
		}
		p.Bits(1, 1)
		instanceRecord(&p, in)
	}
	small(&p, 0)

	// Owner vars: the changed ones (removed ones cannot be expressed).
	var prevOwner map[uint32]heroes.Value
	if prev != nil {
		prevOwner = prev.Owner
	}
	for _, id := range sortedIDs(cur.Owner) {
		v := cur.Owner[id]
		if !ValueOK(v) || id == 0 {
			continue
		}
		if o, ok := prevOwner[id]; ok && sameValue(o, v) && prev.OwnerUsers[id] == cur.OwnerUsers[id] && !union.Owner[id] {
			continue
		}
		count(&p, id)
		userValue(&p, v, cur.OwnerUsers[id], cur, nil)
	}
	count(&p, 0)

	for i := range cur.Instances {
		in := &cur.Instances[i]
		was := prev.find(in.ID)
		if was != nil && (!sameRecord(was, in) || restated(in.ID)) {
			was = nil // a new (or restated) instance under this id
		}
		ut := union.Instances[in.ID]
		changed := func(id uint32, v heroes.Value) bool {
			if was == nil || (ut != nil && ut.Vars[id]) {
				return true
			}
			o, ok := was.Vars[id]
			return !ok || !sameValue(o, v) || was.Users[id] != in.Users[id]
		}
		// Sync vars.
		set := make([]bool, len(in.Layout.SyncSlots))
		synced := map[uint32]bool{}
		for s, id := range in.Layout.SyncSlots {
			if id == 0 {
				continue
			}
			synced[id] = true
			v, ok := in.Vars[id]
			set[s] = ok && ValueOK(v) && changed(id, v)
		}
		changedSet(&p, set)
		for s, on := range set {
			if on {
				id := in.Layout.SyncSlots[s]
				userValue(&p, in.Vars[id], in.Users[id], cur, in)
			}
		}
		for _, id := range sortedIDs(in.Vars) {
			v := in.Vars[id]
			if synced[id] || id == 0 || !ValueOK(v) || !changed(id, v) {
				continue
			}
			count(&p, id)
			userValue(&p, v, in.Users[id], cur, in)
		}
		count(&p, 0)
		// States.
		kinds := in.Layout.States
		stSet := make([]bool, len(kinds))
		for s := range kinds {
			if kinds[s] == 'C' || kinds[s] == 'V' {
				continue
			}
			d, on := in.States[s]
			var od StateData
			wasOn := false
			if was != nil {
				od, wasOn = was.States[s]
			}
			touched := was == nil || (ut != nil && ut.States[s])
			if kinds[s] == 'S' && !on {
				// A "has update" state's end carries its data (FUN_7ff650cee430 reads it active or not).
				_, have := in.Ended[s]
				stSet[s] = (wasOn || touched) && have
				continue
			}
			stSet[s] = on != wasOn || (on && (d.Changes != od.Changes || (DataChanges && !in.Layout.TimeData[s] && !d.equal(od))))
			// Changed at some frame of the union (the client may hold an intermediate activity), or a restated
			// instance (the client may hold it with any states active): '0' deactivates, '1' sets.
			if (ut != nil && ut.States[s]) || (was == nil && restated(in.ID)) {
				stSet[s] = true
			}
		}
		changedSet(&p, stSet)
		for s, ch := range stSet {
			if !ch {
				continue
			}
			d, on := in.States[s]
			if kinds[s] == 'S' {
				p.Bits(1, 1) // has update
				p.Bool(on)
				if !on {
					d = in.Ended[s]
				}
				if d.N > 0 {
					p.Append(bits.FromBytes(d.Bits, uint(d.N)))
				}
				p.Bits(0, 1) // read and dropped after the data
				continue
			}
			p.Bool(on)
			if on {
				if d.N > 0 {
					p.Append(bits.FromBytes(d.Bits, uint(d.N)))
				}
				p.Bits(0, 1) // read and dropped after the data
			}
		}
	}

	// Events: every instance's pending Timer / Lifetime-finish notifications.
	p.Bits(1, 1)
	pid := uint16(0)
	base := int64(endCF) * 16
	for i := range cur.Instances {
		in := &cur.Instances[i]
		p.Bits(0, 1)
		small(&p, uint32(in.ID-pid))
		pid = in.ID
		writeEvents(&p, in, base)
	}
	p.Bits(0, 1)
	small(&p, 0)
	p.Bits(0, 1) // no further segment
	writeActions(&p, startCF, cur)
	return WrapScriptPacket(startCF, endCF, &p), nil
}

// userValue writes a var value with its state reference (G:cf1520): '0' no writer; '1', then form '0' + the state
// index in u<m_statesBitCount> (a state of this instance: instance vars only, never on an owner var, which crashes
// the client), or form '1' + count(instance) + small(state); then the priority bit, then '0' | '1' small(extra)
// (state-defined). A writer instance the packet doesn't leave the client with (or a server-local id) is not
// named: the var goes out without a writer.
func userValue(w *bits.Writer, v heroes.Value, u VarUser, cur *ScriptState, self *InstanceState) {
	if u.Instance == 0 || cur.find(u.Instance) == nil {
		scriptValue(w, v)
		return
	}
	w.Bits(1, 1)
	if self != nil && u.Instance == self.ID {
		w.Bits(0, 1)
		w.Bits(uint32(u.State), uint(self.Layout.IdxBits))
	} else {
		w.Bits(1, 1)
		count(w, uint32(u.Instance))
		small(w, uint32(u.State))
	}
	w.Bool(u.Priority)
	w.Bool(u.StateDefined)
	if u.StateDefined {
		small(w, uint32(u.Extra))
	}
	tagged(w, v)
}

// writeActions writes the actions section (FUN_7ff650cee920, docs/StatescriptSync.md §2.9): one timed block per
// command frame after startCF, '1' cf(CF) count(L) {'1' record}* '0' (L = the records' bit length, the client
// skips the block when CF <= its last applied end), then '0'; no untimed records ('0'). record = '1'
// small(instance) (the first of a block) | '0' (the previous record's instance), the node's m_nodes index
// (u<NodeBits>), '0' = execute. Actions of instances the packet doesn't leave the client with are dropped.
func writeActions(p *bits.Writer, startCF uint32, cur *ScriptState) {
	var blockCF uint32
	var block *bits.Writer
	var prevInst uint16
	flush := func() {
		if block == nil {
			return
		}
		p.Bits(1, 1)
		cf(p, blockCF)
		count(p, uint32(block.Len()))
		p.Append(block)
		p.Bits(0, 1)
		block = nil
	}
	for _, a := range cur.Actions {
		in := cur.find(a.Instance)
		if int32(a.CF-startCF) <= 0 || in == nil || in.Layout.NodeBits <= 0 || a.Node < 0 || a.Node >= 1<<in.Layout.NodeBits {
			continue
		}
		if block == nil || a.CF != blockCF {
			flush()
			block, blockCF, prevInst = &bits.Writer{}, a.CF, 0
		}
		block.Bits(1, 1)
		if a.Instance == prevInst {
			block.Bits(0, 1)
		} else {
			block.Bits(1, 1)
			small(block, uint32(a.Instance))
			prevInst = a.Instance
		}
		block.Bits(uint32(a.Node), uint(in.Layout.NodeBits))
		block.Bits(0, 1) // execute
	}
	flush()
	p.Bits(0, 1) // no more timed blocks
	p.Bits(0, 1) // no untimed records
}

// writeEvents writes an instance's pending events (FUN_7ff650cf1620's format):
// Timer: user data 1 -> 00; else 01 + (0 -> bit 1 | bit 0 + small(data)); Lifetime
// finish: 10; then the state index (IdxBits) and the time relative to the previous
// event (first: the packet's end CF x 16 ms): 1 = same, 01 + count = later, 00 +
// count = earlier. 11 ends.
func writeEvents(p *bits.Writer, in *InstanceState, base int64) {
	t := base
	for _, e := range in.Events {
		switch e.Kind {
		case EventTimer:
			if e.Data == 1 {
				p.Bits(0, 2)
			} else {
				p.Bits(1, 2)
				if e.Data == 0 {
					p.Bits(1, 1)
				} else {
					p.Bits(0, 1)
					small(p, uint32(e.Data))
				}
			}
		case EventFinish:
			p.Bits(2, 2)
		default:
			continue
		}
		p.Bits(uint32(e.State), uint(in.Layout.IdxBits))
		switch d := e.Time - t; {
		case d == 0:
			p.Bits(1, 1)
		case d > 0:
			p.Bits(0b10, 2) // 0 then 1
			count(p, uint32(d))
		default:
			p.Bits(0, 2)
			count(p, uint32(-d))
		}
		t = e.Time
	}
	p.Bits(3, 2)
}

func sortedIDs(m map[uint32]heroes.Value) []uint32 {
	ids := make([]uint32, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
