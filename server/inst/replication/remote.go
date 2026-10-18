package replication

import (
	"fmt"
	"slices"

	"owserver/inst/bits"
	"owserver/internal/heroes"
)

// Remote-format Statescript packets (docs/StatescriptSync.md §2, §6.2): what a client receives for an entity it
// does not control (another player's hero, the gamemode brain, map entities). They carry no CF of their own (the
// client uses the message's P), no events and no rollback: the client applies them at once, with no prediction.
//
//	hdr      bit 0 (ignored), bit 0 (remote)
//	full     instances; owner vars; per instance: a bit (+ value) per eligible m_syncVars slot, then per eligible
//	         m_remoteSyncNodes state (neither client- nor server-only) an "included" bit and, if 1, the state
//	         (plain: 0 | 1 data; has update: 0 | 1 a data); actions            (G:ced430 / cedf30 / cedbe0)
//	delta    instances; owner vars; per listed instance: set(|m_syncVars|) + values, set(|m_remoteSyncNodes|) +
//	         per set entry the state (as local: data then a dropped bit); actions (G:ced600 / cee790 / cee430)
//
// Only state entries may be set in a remote delta's set: G:cee430 reads the active bit of any set entry and, on 1,
// dereferences the entry's state object, which is null for an action (a crash) [G, T053]. State data is the
// remote column of §4.2 (StateData in a remote ScriptState). Time fields in it are relative to the client's clock
// when it applies the packet (§6.1), which the caller models when it takes the snapshot.

// RemoteLayout is what the remote format needs to know about a graph.
type RemoteLayout struct {
	Index     uint16
	SyncSlots []uint32 // var id per m_syncVars slot (0 = no var)
	Eligible  []bool   // per slot: in a full update (a var, mode +0x10 = 0)
	Nodes     []RemoteNode
	NodeBits  int // m_remoteSyncNodesBitCount (graph +0x180): an action's index in the actions section
}

// RemoteNode is an m_remoteSyncNodes entry: its m_nodes index, its m_states index (-1: not a state, e.g. an
// action) and, for states, its kind as in GraphLayout.States ('.', 'S', 'C', 'V').
type RemoteNode struct {
	Node  int
	State int
	Kind  byte
}

func (l *RemoteLayout) eligibleState(n RemoteNode) bool {
	return n.State >= 0 && (n.Kind == '.' || n.Kind == 'S')
}

// RemoteFull encodes a remote full update of s ending at endCF (start 0): it clears the owner bag and stops every
// instance of the entity it doesn't list, client-started ones included (§2.3). layouts gives each instance's
// remote layout; states not in RemoteNodes are left alone ("included" 0), frozen ones too.
func RemoteFull(endCF uint32, s *ScriptState, layouts map[uint16]*RemoteLayout) (*bits.Writer, error) {
	var p bits.Writer
	p.Bits(0, 1) // (ignored)
	p.Bits(0, 1) // remote
	if err := instanceList(&p, nil, s, nil); err != nil {
		return nil, err
	}
	ownerVars(&p, nil, s, nil)
	for i := range s.Instances {
		in := &s.Instances[i]
		l := layouts[in.ID]
		if l == nil {
			return nil, fmt.Errorf("replication: instance %d: no remote layout", in.ID)
		}
		for sl, id := range l.SyncSlots {
			if sl >= len(l.Eligible) || !l.Eligible[sl] {
				continue
			}
			v, ok := in.Vars[id]
			if !ok || !ValueOK(v) {
				p.Bits(0, 1)
				continue
			}
			p.Bits(1, 1)
			userValue(&p, v, in.Users[id], s, in)
		}
		for _, n := range l.Nodes {
			if !l.eligibleState(n) {
				continue
			}
			if in.Layout != nil && in.Layout.Frozen[n.State] {
				p.Bits(0, 1) // not included: left as it is
				continue
			}
			d, on := in.States[n.State]
			p.Bits(1, 1) // included
			if n.Kind == 'S' {
				if !on {
					p.Bits(0, 1) // no update
					continue
				}
				p.Bits(1, 1)
				p.Bits(1, 1) // active
			} else {
				p.Bool(on)
				if !on {
					continue
				}
			}
			if d.N > 0 {
				p.Append(bits.FromBytes(d.Bits, uint(d.N)))
			}
		}
	}
	writeRemoteActions(&p, 0, s, layouts)
	return WrapScriptPacket(0, endCF, &p), nil
}

// RemoteDelta encodes the union [startCF, endCF] from prev (the state the client acknowledged) to cur, union
// marking what changed at some frame between (as LocalGhost does for the local format).
func RemoteDelta(startCF, endCF uint32, prev, cur *ScriptState, union *Touched, layouts map[uint16]*RemoteLayout) (*bits.Writer, error) {
	if startCF == 0 || endCF < startCF {
		return nil, fmt.Errorf("replication: remote delta CF range %d..%d", startCF, endCF)
	}
	if union == nil {
		union = &Touched{}
	}
	var p bits.Writer
	p.Bits(0, 1)
	p.Bits(0, 1)
	if err := instanceList(&p, prev, cur, union); err != nil {
		return nil, err
	}
	ownerVars(&p, prev, cur, union)
	for i := range cur.Instances {
		in := &cur.Instances[i]
		l := layouts[in.ID]
		if l == nil {
			return nil, fmt.Errorf("replication: instance %d: no remote layout", in.ID)
		}
		ut := union.Instances[in.ID]
		was := prev.find(in.ID)
		if was != nil && (!sameRecord(was, in) || (ut != nil && ut.Restate)) {
			was = nil
		}
		set := make([]bool, len(l.SyncSlots))
		for sl, id := range l.SyncSlots {
			v, ok := in.Vars[id]
			if id == 0 || !ok || !ValueOK(v) {
				continue
			}
			var o heroes.Value
			had := false
			if was != nil {
				o, had = was.Vars[id]
			}
			set[sl] = was == nil || !had || !sameValue(o, v) || was.Users[id] != in.Users[id] || (ut != nil && ut.Vars[id])
		}
		changedSet(&p, set)
		for sl, on := range set {
			if on {
				id := l.SyncSlots[sl]
				userValue(&p, in.Vars[id], in.Users[id], cur, in)
			}
		}
		stSet := make([]bool, len(l.Nodes))
		for k, n := range l.Nodes {
			if !l.eligibleState(n) || (in.Layout != nil && in.Layout.Frozen[n.State]) {
				continue // never an action entry (G:cee430 crashes on it)
			}
			d, on := in.States[n.State]
			var od StateData
			wasOn := false
			if was != nil {
				od, wasOn = was.States[n.State]
			}
			touched := ut != nil && ut.States[n.State]
			if n.Kind == 'S' && !on {
				_, have := in.Ended[n.State]
				stSet[k] = (wasOn || touched || was == nil) && have
				continue
			}
			stSet[k] = on != wasOn || touched || (on && (d.Changes != od.Changes || (DataChanges && !d.equal(od))))
			if was == nil && ut != nil && ut.Restate {
				stSet[k] = true
			}
		}
		changedSet(&p, stSet)
		for k, ch := range stSet {
			if !ch {
				continue
			}
			n := l.Nodes[k]
			d, on := in.States[n.State]
			if n.Kind == 'S' {
				p.Bits(1, 1)
				p.Bool(on)
				if !on {
					d = in.Ended[n.State]
				}
				if d.N > 0 {
					p.Append(bits.FromBytes(d.Bits, uint(d.N)))
				}
				p.Bits(0, 1)
				continue
			}
			p.Bool(on)
			if on {
				if d.N > 0 {
					p.Append(bits.FromBytes(d.Bits, uint(d.N)))
				}
				p.Bits(0, 1)
			}
		}
	}
	writeRemoteActions(&p, startCF, cur, layouts)
	return WrapScriptPacket(startCF, endCF, &p), nil
}

// instanceList writes the instance list shared by both formats: cur's instances (new or restated ones with their
// record), and prev's (or union-destroyed) ones cur doesn't have as destroyed. prev nil = a full update's list.
func instanceList(p *bits.Writer, prev, cur *ScriptState, union *Touched) error {
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
	if union != nil {
		for id, it := range union.Instances {
			if it.Restate && cur.find(id) == nil && prev.find(id) == nil {
				list = append(list, item{id: id, destroyed: true})
			}
		}
	}
	slices.SortFunc(list, func(a, b item) int { return int(a.id) - int(b.id) })
	prevID := uint16(0)
	for _, it := range list {
		if it.id <= prevID {
			return fmt.Errorf("replication: instance ids must increase (%d after %d)", it.id, prevID)
		}
		small(p, uint32(it.id-prevID))
		prevID = it.id
		if prev == nil { // full update: every entry is a record
			writeRecord(p, cur.find(it.id))
			continue
		}
		if it.destroyed {
			p.Bits(1, 1)
			continue
		}
		p.Bits(0, 1)
		in := cur.find(it.id)
		ut := union.Instances[it.id]
		if was := prev.find(it.id); was != nil && sameRecord(was, in) && (ut == nil || !ut.Restate) {
			p.Bits(0, 1)
			continue
		}
		p.Bits(1, 1)
		writeRecord(p, in)
	}
	small(p, 0)
	return nil
}

// writeRecord: u16 graph index, owner '0' (the entity itself), parent ('1' count(parent) count(slot) | '0').
func writeRecord(p *bits.Writer, in *InstanceState) {
	p.Bits(uint32(in.Layout.Index), 16)
	p.Bits(0, 1)
	if in.Parent != 0 {
		p.Bits(1, 1)
		count(p, uint32(in.Parent))
		count(p, in.Slot)
	} else {
		p.Bits(0, 1)
	}
}

// ownerVars writes the owner var section: the changed ones (all of cur's for a full update, prev nil).
func ownerVars(p *bits.Writer, prev, cur *ScriptState, union *Touched) {
	for _, id := range sortedIDs(cur.Owner) {
		v := cur.Owner[id]
		if !ValueOK(v) || id == 0 {
			continue
		}
		if prev != nil {
			if o, ok := prev.Owner[id]; ok && sameValue(o, v) && prev.OwnerUsers[id] == cur.OwnerUsers[id] && (union == nil || !union.Owner[id]) {
				continue
			}
		}
		count(p, id)
		userValue(p, v, cur.OwnerUsers[id], cur, nil)
	}
	count(p, 0)
}

// writeRemoteActions writes the actions section with remote node indices (u<m_remoteSyncNodesBitCount>): remote
// clients execute only SYNC_ALL actions, the remote sync nodes (§2.9). Action.Node (m_nodes) is mapped through the
// layout; actions of other nodes are dropped. Timed blocks as in writeActions.
func writeRemoteActions(p *bits.Writer, startCF uint32, cur *ScriptState, layouts map[uint16]*RemoteLayout) {
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
		l := layouts[a.Instance]
		if l == nil || cur.find(a.Instance) == nil || (startCF != 0 && int32(a.CF-startCF) <= 0) {
			continue
		}
		ri := slices.IndexFunc(l.Nodes, func(n RemoteNode) bool { return n.State < 0 && n.Node == a.Node })
		if ri < 0 || l.NodeBits <= 0 || ri >= 1<<l.NodeBits {
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
		block.Bits(uint32(ri), uint(l.NodeBits))
		block.Bits(0, 1) // execute
	}
	flush()
	p.Bits(0, 1)
	p.Bits(0, 1)
}
