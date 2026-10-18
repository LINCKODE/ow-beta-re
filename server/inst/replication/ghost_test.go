package replication

import (
	"strings"
	"testing"

	"owserver/inst/bits"
	"owserver/internal/heroes"
)

func val(i int64) string { return bitsOf(func(w *bits.Writer) { scriptValue(w, heroes.Int(i)) }) }

// A union from the acked base: start = BaseCF, and a var that changed and changed back since the base is still
// sent (the client may hold the intermediate value from the packet whose ack is missing).
func TestGhostUnionAfterLoss(t *testing.T) {
	s0 := state(map[uint32]heroes.Value{0x10: heroes.Int(3)}, nil)
	g := NewLocalGhost(100, s0)
	s1 := state(map[uint32]heroes.Value{0x10: heroes.Int(4)}, nil)
	if _, err := g.Next(101, s1); err != nil { // lost (never acked)
		t.Fatal(err)
	}
	s2 := state(map[uint32]heroes.Value{0x10: heroes.Int(3)}, nil) // back to the base value
	w, err := g.Next(102, s2)
	if err != nil {
		t.Fatal(err)
	}
	got := deltaPayload(t, w, 100, 102)
	want := "011" + cnt(102) + sml(1) + "00" + sml(0) + cnt(0) +
		"100" + val(3) + cnt(0) + // slot 0 sent again with its end value
		"0000"
	if !strings.HasPrefix(got, want) {
		t.Errorf("union\n got %s\nwant %s…", got, want)
	}
	// Once 102 is acked, the next packet starts there and carries only new changes.
	g.Acked(102)
	if g.BaseCF != 102 || g.Unacked() {
		t.Fatalf("base %d unacked %v", g.BaseCF, g.Unacked())
	}
	w, _ = g.Next(103, s2)
	if got := deltaPayload(t, w, 102, 103); !strings.HasPrefix(got, "011"+cnt(103)+sml(1)+"00"+sml(0)+cnt(0)+"000"+cnt(0)+"0000") {
		t.Errorf("after ack: %s", got)
	}
}

// A state that began and ended between the base and now is sent as inactive ('0' deactivates if the client got
// the begin).
func TestGhostStateBeganAndEnded(t *testing.T) {
	s0 := state(nil, nil)
	g := NewLocalGhost(10, s0)
	g.Next(11, state(nil, map[int]StateData{0: {Changes: 1}}))
	w, _ := g.Next(12, state(nil, nil))
	got := deltaPayload(t, w, 10, 12)
	want := "011" + cnt(12) + sml(1) + "00" + sml(0) + cnt(0) + "000" + cnt(0) +
		"1000" + "0" // state 0 set: inactive
	if !strings.HasPrefix(got, want) {
		t.Errorf("\n got %s\nwant %s…", got, want)
	}
}

// An id whose record changes is destroyed first, then created once no packet the client may hold names the old
// record; its children wait with it.
func TestGhostRecordChangeHeldBack(t *testing.T) {
	other := &GraphLayout{Index: 0x43, SyncSlots: nil, States: "", IdxBits: 1}
	s0 := state(nil, nil)
	g := NewLocalGhost(10, s0)
	cur := &ScriptState{Owner: s0.Owner, Instances: []InstanceState{
		{ID: 1, Layout: other}, {ID: 2, Layout: other, Parent: 1, Slot: 0}}}
	w, _ := g.Next(11, cur)
	got := deltaPayload(t, w, 10, 11)
	if want := "011" + cnt(11) + sml(1) + "1" + sml(0); !strings.HasPrefix(got, want) {
		t.Fatalf("first: destroy only\n got %s\nwant %s…", got, want)
	}
	// Still unacked: the old record may be on the client, so it stays held back.
	w, _ = g.Next(12, cur)
	if got := deltaPayload(t, w, 10, 12); !strings.HasPrefix(got, "011"+cnt(12)+sml(1)+"1"+sml(0)) {
		t.Fatalf("second: %s", got)
	}
	g.Acked(12)
	w, _ = g.Next(13, cur)
	got = deltaPayload(t, w, 12, 13)
	want := "011" + cnt(13) + sml(1) + "01" + nb(0x43, 16) + "00" + // 1 new, top level
		sml(1) + "01" + nb(0x43, 16) + "0" + "1" + cnt(1) + cnt(0) + // 2 new, child of 1
		sml(0)
	if !strings.HasPrefix(got, want) {
		t.Errorf("after ack\n got %s\nwant %s…", got, want)
	}
}

// An instance destroyed and created again (same record) since the base is restated: new record, every var,
// every state bit, since the client may or may not have applied the destroy.
func TestGhostRestate(t *testing.T) {
	s0 := state(map[uint32]heroes.Value{0x10: heroes.Int(3)}, map[int]StateData{3: {Changes: 1}})
	g := NewLocalGhost(10, s0)
	g.Next(11, &ScriptState{Owner: s0.Owner})
	w, _ := g.Next(12, state(map[uint32]heroes.Value{0x10: heroes.Int(3)}, nil))
	got := deltaPayload(t, w, 10, 12)
	want := "011" + cnt(12) + sml(1) + "01" + nb(0x42, 16) + "00" + sml(0) + cnt(0) +
		"100" + val(3) + cnt(0) + // every var
		"1001" + "0" + "0" // states 0 and 3 inactive ('S' 2 has no ended data: not set)
	if !strings.HasPrefix(got, want) {
		t.Errorf("\n got %s\nwant %s…", got, want)
	}
}

// Frozen states stay as last sent (not corrected); a state whose data names an instance the client won't know
// is held back until it does.
func TestGhostHoldStates(t *testing.T) {
	lay := &GraphLayout{Index: 0x42, SyncSlots: []uint32{0x10, 0, 0x11}, States: ".CS.", IdxBits: 2, Frozen: map[int]bool{3: true}}
	mk := func(states map[int]StateData) *ScriptState {
		return &ScriptState{Owner: map[uint32]heroes.Value{}, Instances: []InstanceState{{ID: 1, Layout: lay, States: states}}}
	}
	g := NewLocalGhost(10, mk(map[int]StateData{3: {Changes: 1}}))
	// Server: frozen state 3 ended, state 0 began with data naming instance 5 (unknown).
	w, _ := g.Next(11, mk(map[int]StateData{0: {Changes: 1, Bits: []byte{1}, N: 1, Refs: []int16{5}}}))
	got := deltaPayload(t, w, 10, 11)
	if want := "011" + cnt(11) + sml(1) + "00" + sml(0) + cnt(0) + "000" + cnt(0) + "0000"; !strings.HasPrefix(got, want) {
		t.Errorf("held\n got %s\nwant %s…", got, want)
	}
	if st := g.LastState().Instances[0].States; len(st) != 1 || st[3].Changes != 1 {
		t.Errorf("recorded %v, want the frozen state 3 only", st)
	}
	// A predecessor in the server's local instance range is never sent either.
	w, _ = g.Next(12, mk(map[int]StateData{0: {Changes: 1, Bits: []byte{1}, N: 1, Refs: []int16{-3}}}))
	if got := deltaPayload(t, w, 10, 12); !strings.Contains(got, "000"+cnt(0)+"0000") {
		t.Errorf("local ref sent: %s", got)
	}
}

// Data that only moves with time (same change count) is not re-sent; a counted change is.
func TestGhostTimeDataNotResent(t *testing.T) {
	g := NewLocalGhost(10, state(nil, map[int]StateData{0: {Changes: 1, Bits: []byte{1}, N: 2}}))
	w, _ := g.Next(11, state(nil, map[int]StateData{0: {Changes: 1, Bits: []byte{2}, N: 2}}))
	if got := deltaPayload(t, w, 10, 11); !strings.Contains(got, "000"+cnt(0)+"0000") {
		t.Errorf("time-moving data re-sent: %s", got)
	}
	g.Acked(11)
	w, _ = g.Next(12, state(nil, map[int]StateData{0: {Changes: 2, Bits: []byte{3}, N: 2}}))
	if got := deltaPayload(t, w, 11, 12); !strings.Contains(got, "000"+cnt(0)+"1000"+"1"+"11"+"0") {
		t.Errorf("counted change not sent: %s", got)
	}
}
