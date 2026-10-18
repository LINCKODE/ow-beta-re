package replication

import (
	"testing"

	"owserver/inst/bits"
	"owserver/internal/heroes"
)

// A remote layout: slots 0x10 (eligible), 0 (empty), 0x11 (not eligible); remote nodes: state 0 ('.'), an action
// (node 5), state 2 ('S'), state 1 client-only.
var remoteLayout = &RemoteLayout{Index: 0x42, SyncSlots: []uint32{0x10, 0, 0x11}, Eligible: []bool{true, false, false},
	Nodes:    []RemoteNode{{Node: 1, State: 0, Kind: '.'}, {Node: 5, State: -1}, {Node: 3, State: 2, Kind: 'S'}, {Node: 2, State: 1, Kind: 'C'}},
	NodeBits: 2}

func remoteState(vars map[uint32]heroes.Value, states map[int]StateData) *ScriptState {
	return &ScriptState{Owner: map[uint32]heroes.Value{}, Instances: []InstanceState{{ID: 1, Layout: testLayout, Vars: vars, States: states}}}
}

func TestRemoteFull(t *testing.T) {
	s := remoteState(map[uint32]heroes.Value{0x10: heroes.Int(3), 0x11: heroes.Int(4)},
		map[int]StateData{0: {Bits: []byte{0b01}, N: 2}, 2: {Bits: []byte{1}, N: 1}})
	w, err := RemoteFull(500, s, map[uint16]*RemoteLayout{1: remoteLayout})
	if err != nil {
		t.Fatal(err)
	}
	got := deltaPayload(t, w, 0, 500)
	want := "00" + // ignored, remote: no rollback bit, no CF
		sml(1) + nb(0x42, 16) + "00" + sml(0) + // instance 1: graph, owner, top level
		cnt(0) + // no owner vars
		"1" + val(3) + // the one eligible slot
		"1" + "1" + "10" + // state 0: included, active, 2 data bits
		"1" + "1" + "1" + "1" + // state 2 ('S'): included, has update, active, data
		"00" // no actions
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

// A remote delta never sets an action entry (G:cee430 would dereference a null state object).
func TestRemoteDelta(t *testing.T) {
	prev := remoteState(map[uint32]heroes.Value{0x10: heroes.Int(3)}, map[int]StateData{0: {Changes: 1}})
	cur := remoteState(map[uint32]heroes.Value{0x10: heroes.Int(5)}, map[int]StateData{})
	cur.Actions = []Action{{CF: 11, Instance: 1, Node: 5}, {CF: 12, Instance: 1, Node: 1}} // node 1 is a state: dropped
	w, err := RemoteDelta(10, 12, prev, cur, nil, map[uint16]*RemoteLayout{1: remoteLayout})
	if err != nil {
		t.Fatal(err)
	}
	got := deltaPayload(t, w, 10, 12)
	block := "1" + "1" + sml(1) + nb(1, 2) + "0" // the action: remote index 1
	want := "00" + sml(1) + "00" + sml(0) + cnt(0) +
		"100" + val(5) + // sync set over all 3 slots
		"1000" + "0" + // remote set: only state 0 (ended)
		"1" + bitsOf(func(w *bits.Writer) { cf(w, 11) }) + cnt(uint32(len(block))) + block + "0" + "00"
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}
