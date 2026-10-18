package replication

import (
	"strings"
	"testing"

	"owserver/inst/bits"
	"owserver/internal/heroes"
)

// bitString renders a writer LSB-first as 0/1 (the order the bits were written).
func bitString(w *bits.Writer) string {
	var sb strings.Builder
	r := bits.NewReader(w.Buf())
	for range w.Len() {
		if r.Bool() {
			sb.WriteByte('1')
		} else {
			sb.WriteByte('0')
		}
	}
	return sb.String()
}

func bitsOf(f func(w *bits.Writer)) string {
	var w bits.Writer
	f(&w)
	return bitString(&w)
}

// deltaPayload strips the StatescriptPacket header (WrapScriptPacket) from a delta.
func deltaPayload(t *testing.T, w *bits.Writer, start, end uint32) string {
	t.Helper()
	s := bitString(w)
	var h bits.Writer
	h.Bits(1, 1)
	h.Bits(0, 1)
	cf(&h, start)
	count(&h, end-start)
	h.Bits(0, 1)
	hs := bitString(&h)
	if !strings.HasPrefix(s, hs) {
		t.Fatalf("header %s, want %s", s[:min(len(s), len(hs))], hs)
	}
	s = s[len(hs):]
	n := bitsOf(func(w *bits.Writer) { count(w, uint32(len(s)-len(bitsOf(func(w *bits.Writer) { count(w, 0) })))) })
	// The payload length prefix: 0 + 8 bits for payloads under 256 bits.
	if !strings.HasPrefix(s, n) {
		t.Fatalf("length prefix %s, want %s", s[:9], n)
	}
	return s[len(n):]
}

func cnt(v uint32) string        { return bitsOf(func(w *bits.Writer) { count(w, v) }) }
func sml(v uint32) string        { return bitsOf(func(w *bits.Writer) { small(w, v) }) }
func nb(v uint32, n uint) string { return bitsOf(func(w *bits.Writer) { w.Bits(v, n) }) }

var testLayout = &GraphLayout{Index: 0x42, SyncSlots: []uint32{0x10, 0, 0x11}, States: ".CS.", IdxBits: 2}

func state(vars map[uint32]heroes.Value, states map[int]StateData, ev ...Event) *ScriptState {
	return &ScriptState{Owner: map[uint32]heroes.Value{0x317: heroes.Float(1)},
		Instances: []InstanceState{{ID: 1, Layout: testLayout, Vars: vars, States: states, Events: ev}}}
}

func TestLocalDeltaUnchanged(t *testing.T) {
	s := state(map[uint32]heroes.Value{0x10: heroes.Int(3)}, map[int]StateData{0: {}})
	w, err := LocalDelta(100, 104, s, s)
	if err != nil {
		t.Fatal(err)
	}
	want := "011" + cnt(104) + // ignored, local, rollback, CF
		sml(1) + "00" + sml(0) + // instance 1 known; end
		cnt(0) + // owner vars: none changed
		"000" + cnt(0) + // sync set (3 slots), no extras
		"0000" + // state set (4 states)
		"1" + "0" + sml(1) + "11" + "0" + sml(0) + "0" + // events: instance 1 none; end; no segment
		"00" // actions: no timed blocks, no untimed records
	if got := deltaPayload(t, w, 100, 104); got != want {
		t.Errorf("payload\n got %s\nwant %s", got, want)
	}
}

func TestLocalDeltaChanges(t *testing.T) {
	prev := state(map[uint32]heroes.Value{0x10: heroes.Int(3)}, map[int]StateData{0: {}})
	cur := state(map[uint32]heroes.Value{0x10: heroes.Int(2), 0x11: heroes.Int(3), 0x55: heroes.Int(1)},
		map[int]StateData{2: {Bits: []byte{1}, N: 1}},
		Event{Kind: EventTimer, State: 3, Data: 1, Time: 104*16 + 5})
	cur.Owner[0x318] = heroes.Float(1)
	w, err := LocalDelta(100, 104, prev, cur)
	if err != nil {
		t.Fatal(err)
	}
	val := func(i uint32) string { return bitsOf(func(w *bits.Writer) { scriptValue(w, heroes.Int(int64(i))) }) }
	fval := bitsOf(func(w *bits.Writer) { scriptValue(w, heroes.Float(1)) })
	want := "011" + cnt(104) +
		sml(1) + "00" + sml(0) +
		cnt(0x318) + fval + cnt(0) + // owner var 0x318 added
		"101" + val(2) + val(3) + cnt(0x55) + val(1) + cnt(0) + // slots 0 and 2 changed, extra 0x55
		"1010" + // state 0 ended, state 2 ('S') began
		"0" + // state 0: inactive
		"1" + "1" + "1" + "0" + // state 2: has-update bit, active, data 1, dropped bit
		"1" + "0" + sml(1) + "00" + nb(3, 2) + "01" + cnt(5) + "11" + // timer ud 1, state 3, +5 ms
		"0" + sml(0) + "0" +
		"00"
	if got := deltaPayload(t, w, 100, 104); got != want {
		t.Errorf("payload\n got %s\nwant %s", got, want)
	}
}

func TestLocalDeltaNewAndDestroyed(t *testing.T) {
	prev := state(nil, nil)
	cur := &ScriptState{Owner: prev.Owner, Instances: []InstanceState{{ID: 2, Layout: testLayout, Parent: 1, Slot: 3}}}
	w, err := LocalDelta(7, 9, prev, cur)
	if err != nil {
		t.Fatal(err)
	}
	s := deltaPayload(t, w, 7, 9)
	list := sml(1) + "1" + // id 1 destroyed
		sml(1) + "01" + nb(0x42, 16) + "0" + "1" + cnt(1) + cnt(3) + // id 2 new, parent 1 slot 3
		sml(0)
	if !strings.HasPrefix(s, "011"+cnt(9)+list) {
		t.Errorf("instance list\n got %s\nwant %s", s, "011"+cnt(9)+list)
	}
	if _, err := LocalDelta(0, 9, prev, cur); err == nil {
		t.Error("start CF 0 accepted")
	}
}

// The end of a "has update" state carries its data: has-update 1, active 0, data, dropped bit.
func TestLocalDeltaHasUpdateEnd(t *testing.T) {
	prev := state(nil, map[int]StateData{2: {Bits: []byte{1}, N: 1, Changes: 1}})
	cur := state(nil, nil)
	cur.Instances[0].Ended = map[int]StateData{2: {Bits: []byte{0b10}, N: 2, Changes: 2}}
	w, err := LocalDelta(100, 104, prev, cur)
	if err != nil {
		t.Fatal(err)
	}
	want := "011" + cnt(104) + sml(1) + "00" + sml(0) + cnt(0) +
		"000" + cnt(0) + // vars: nothing changed
		"0010" + // state 2 ('S') changed
		"1" + "0" + "01" + "0" + // has update, inactive, its 2 data bits, dropped bit
		"1" + "0" + sml(1) + "11" + "0" + sml(0) + "0" + "00"
	if got := deltaPayload(t, w, 100, 104); got != want {
		t.Errorf("payload\n got %s\nwant %s", got, want)
	}
	// Without its data the end can't be sent: left to the client.
	cur.Instances[0].Ended = nil
	w, _ = LocalDelta(100, 104, prev, cur)
	if got := deltaPayload(t, w, 100, 104); !strings.Contains(got, "000"+cnt(0)+"0000") {
		t.Errorf("end without data was sent: %s", got)
	}
}

// Actions (§2.9): a timed block per CF after the start, the first record of a block names its instance.
func TestLocalDeltaActions(t *testing.T) {
	lay := &GraphLayout{Index: 0x42, States: "", IdxBits: 1, NodeBits: 5}
	s := &ScriptState{Owner: map[uint32]heroes.Value{}, Instances: []InstanceState{{ID: 1, Layout: lay}, {ID: 2, Layout: lay}}}
	cur := *s
	cur.Actions = []Action{{CF: 100, Instance: 1, Node: 9}, // not after the start: dropped
		{CF: 102, Instance: 1, Node: 3}, {CF: 102, Instance: 1, Node: 4}, {CF: 102, Instance: 2, Node: 5},
		{CF: 103, Instance: 1, Node: 6}, {CF: 103, Instance: 7, Node: 1}} // unknown instance: dropped
	w, err := LocalDelta(100, 103, s, &cur)
	if err != nil {
		t.Fatal(err)
	}
	cfs := func(v uint32) string { return bitsOf(func(w *bits.Writer) { cf(w, v) }) }
	rec := func(first bool, id uint32, node uint32) string {
		if first {
			return "1" + "1" + sml(id) + nb(node, 5) + "0"
		}
		return "1" + "0" + nb(node, 5) + "0"
	}
	b102 := rec(true, 1, 3) + rec(false, 0, 4) + rec(true, 2, 5)
	b103 := rec(true, 1, 6)
	want := "1" + cfs(102) + cnt(uint32(len(b102))) + b102 + "0" +
		"1" + cfs(103) + cnt(uint32(len(b103))) + b103 + "0" + "0" + "0"
	if got := deltaPayload(t, w, 100, 103); !strings.HasSuffix(got, want) {
		t.Errorf("actions\n got %s\nwant …%s", got, want)
	}
}

// Var writers (§2.7): form 0 for a state of the var's own instance, form 1 (instance + state) otherwise and always
// on an owner var; an unknown writer instance is not named.
func TestLocalDeltaVarUsers(t *testing.T) {
	prev := state(nil, nil)
	cur := state(map[uint32]heroes.Value{0x10: heroes.Int(3), 0x11: heroes.Int(4)}, nil)
	cur.Instances[0].Users = map[uint32]VarUser{0x10: {Instance: 1, State: 2, StateDefined: true, Extra: 0},
		0x11: {Instance: 9, State: 1}} // instance 9 unknown
	cur.Owner = map[uint32]heroes.Value{0x317: heroes.Float(1), 0x320: heroes.Int(1)}
	cur.OwnerUsers = map[uint32]VarUser{0x320: {Instance: 1, State: 3, Priority: true}}
	w, err := LocalDelta(100, 104, prev, cur)
	if err != nil {
		t.Fatal(err)
	}
	tag := func(v int64) string { return bitsOf(func(w *bits.Writer) { tagged(w, heroes.Int(v)) }) }
	want := "011" + cnt(104) + sml(1) + "00" + sml(0) +
		cnt(0x320) + "1" + "1" + cnt(1) + sml(3) + "1" + "0" + tag(1) + cnt(0) + // owner var: form 1, priority
		"101" + "1" + "0" + nb(2, 2) + "0" + "1" + sml(0) + tag(3) + // slot 0: form 0, state-defined, extra 0
		"0" + tag(4) + cnt(0) // slot 2: writer unknown -> none
	if got := deltaPayload(t, w, 100, 104); !strings.HasPrefix(got, want) {
		t.Errorf("\n got %s\nwant %s…", got, want)
	}
}
