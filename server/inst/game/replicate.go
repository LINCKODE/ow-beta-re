package game

import (
	"fmt"
	"sort"

	"owserver/inst/replication"
	"owserver/inst/sim"
	"owserver/internal/heroes"

	"owstatescript/statescript"
)

// Remote-format snapshots of the server-run entities (docs/InstanceServer.md §12.6): what their remote full updates
// and deltas carry to every client. The policy is §10.3's, for the remote format: a state whose remote data the
// runtime can't write is frozen (never included), with one exception measured live: a Stack's remote data is one
// "no predecessor" bit (MatchLogic.md §10.3, §10.6 [L]).

const typeStack = 0xf7d6498c

type layouts struct {
	local  *replication.GraphLayout
	remote *replication.RemoteLayout
}

func (g *Game) layout(graph uint64) (*layouts, error) {
	if l := g.lay[graph]; l != nil {
		return l, nil
	}
	rt := g.eng.Runtime()
	slots, err := rt.GraphSyncSlots(graph)
	if err != nil {
		return nil, err
	}
	states, err := rt.GraphStates(graph)
	if err != nil {
		return nil, err
	}
	nodes, err := rt.GraphRemoteNodes(graph)
	if err != nil {
		return nil, err
	}
	bc, err := rt.GraphBitCounts(graph)
	if err != nil {
		return nil, err
	}
	loc := &replication.GraphLayout{Index: uint16(graph), IdxBits: bc.States, NodeBits: bc.Nodes, Frozen: map[int]bool{}}
	rem := &replication.RemoteLayout{Index: uint16(graph), NodeBits: bc.RemoteNodes}
	for _, s := range slots {
		loc.SyncSlots = append(loc.SyncSlots, uint32(s.Var))
		rem.SyncSlots = append(rem.SyncSlots, uint32(s.Var))
		rem.Eligible = append(rem.Eligible, s.Var != 0 && s.Mode == 0)
	}
	kinds := make([]byte, len(states))
	loc.Types = make([]uint32, len(states))
	for i, s := range states {
		kinds[i] = map[statescript.StateKind]byte{statescript.StateClientOnly: 'C', statescript.StateServerOnly: 'V',
			statescript.StateHasUpdate: 'S'}[s.Kind]
		if kinds[i] == 0 {
			kinds[i] = '.'
		}
		loc.Types[i] = s.Type
		if ok, _ := g.eng.Corrects(s.Type, false); !ok && s.Type != typeStack {
			loc.Frozen[i] = true
		}
	}
	loc.States = string(kinds)
	for _, n := range nodes {
		k := byte(0)
		if n.State >= 0 && n.State < len(kinds) {
			k = kinds[n.State]
		}
		rem.Nodes = append(rem.Nodes, replication.RemoteNode{Node: n.Node, State: n.State, Kind: k})
	}
	l := &layouts{loc, rem}
	g.lay[graph] = l
	return l, nil
}

// Snapshot is a server-run entity's Statescript in the remote format, with each instance's remote layout.
func (g *Game) Snapshot(id uint32) (*replication.ScriptState, map[uint16]*replication.RemoteLayout, error) {
	sm := g.m.SyncManager(id)
	if sm == nil {
		return nil, nil, fmt.Errorf("game: %#x runs no graphs", id)
	}
	s := &replication.ScriptState{Owner: map[uint32]heroes.Value{}}
	for _, v := range sm.Vars(0) {
		if hv, ok := sim.HeroValue(v.Value); ok {
			s.Owner[uint32(v.ID)] = hv
		}
	}
	rl := map[uint16]*replication.RemoteLayout{}
	for _, iid := range sm.Instances() {
		if iid <= 0 {
			continue // the server's own
		}
		info, err := sm.InstanceInfo(iid)
		if err != nil {
			return nil, nil, err
		}
		l, err := g.layout(info.Graph)
		if err != nil {
			return nil, nil, fmt.Errorf("game: graph %#x: %w", info.Graph, err)
		}
		in := replication.InstanceState{ID: uint16(iid), Layout: l.local, Parent: uint16(info.Parent), Slot: info.Slot,
			Vars: map[uint32]heroes.Value{}, States: map[int]replication.StateData{}}
		for _, v := range sm.Vars(iid) {
			// An INT64 of 0 is an unset resource / identifier reference (display texts the runtime can't evaluate:
			// brain 0x029's $0738… HUD message vars): sent, the client's HUD prints it as "0X000…" [H]. Left out, the
			// client keeps its own.
			if v.Value.Type == statescript.Int64 && v.Value.Bits == 0 {
				continue
			}
			if hv, ok := sim.HeroValue(v.Value); ok {
				in.Vars[uint32(v.ID)] = hv
			}
		}
		for _, st := range sm.ActiveStates(iid) {
			if l.local.Frozen[st] {
				continue
			}
			changes, _ := sm.StateChanges(iid, st)
			if st < len(l.local.Types) && l.local.Types[st] == typeStack {
				in.States[st] = replication.StateData{Bits: []byte{0}, N: 1, Changes: changes}
				continue
			}
			b, n, err := sm.StateDataFormat(iid, st, false, false)
			if err != nil {
				continue // no remote data from the runtime: left out (the client keeps what it has)
			}
			in.States[st] = replication.StateData{Bits: b, N: n, Changes: changes}
		}
		s.Instances = append(s.Instances, in)
		rl[uint16(iid)] = l.remote
	}
	sort.Slice(s.Instances, func(i, j int) bool { return s.Instances[i].ID < s.Instances[j].ID })
	return s, rl, nil
}
