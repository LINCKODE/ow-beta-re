package replication

import (
	"errors"
	"testing"

	"owserver/internal/maps"
)

// The residency guard (docs/InstanceServer.md §12.15): on the tutorial, the assault brain's create (the T059
// crash) and a from-map create past the placed list are refused, with every later entry for those entities;
// the controller, a hero, the tutorial's own brain and a real placement go through.
func TestResidencyGuard(t *testing.T) {
	tut, ok := maps.ByData(maps.Tutorial)
	if !ok {
		t.Fatal("no tutorial map")
	}
	r := NewReplicator()
	r.Residency = &Residency{Resident: tut.Resident, Placed: uint32(tut.Placed)}
	const ctrl, brain, own, spawn, bad, hero = 0x40000002, 0x40000003, 0x40000004, 0x40000005, 0x40000006, 0x40000007
	err := r.Queue(
		Entry{Entity: ctrl, Op: OpCreate, Create: Create{Def: 0x0e, Flags: 0x38}},
		Entry{Entity: brain, Op: OpCreate, Create: Create{Def: 0x39}}, // assault brain: not on the tutorial
		Entry{Entity: brain}, // its Statescript (entry without an op)
		Entry{Entity: own, Op: OpCreate, Create: Create{Def: 0x128e}}, // the tutorial's brain
		Entry{Entity: spawn, Op: OpCreate, Create: Create{FromMap: true, MapIndex: 0x69}},
		Entry{Entity: bad, Op: OpCreate, Create: Create{FromMap: true, MapIndex: uint32(tut.Placed)}},
		Entry{Entity: bad, Op: OpUpdate},
		Entry{Entity: hero, Op: OpCreate, Create: Create{Def: 0x3cf, Mover: &Mover{}}}, // Soldier: 76
		Entry{Entity: brain, Op: OpDestroy},
	)
	if !errors.Is(err, ErrNotResident) {
		t.Fatalf("Queue error = %v, want ErrNotResident", err)
	}
	t.Log(err)
	if r.Refused != 5 {
		t.Errorf("Refused = %d, want 5 (2 creates, 3 entries of theirs)", r.Refused)
	}
	var got []uint32
	for r.Pending() > 0 {
		es, _ := r.Take(1)
		for _, e := range es {
			got = append(got, e.Entity)
		}
	}
	want := []uint32{ctrl, own, spawn, hero}
	if len(got) != len(want) {
		t.Fatalf("sent %#x, want %#x", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sent %#x, want %#x", got, want)
		}
	}
	if r.Has(brain) || r.Has(bad) || !r.Has(own) {
		t.Errorf("known: brain %v bad %v own %v", r.Has(brain), r.Has(bad), r.Has(own))
	}

	// On Anubis the assault brain is resident; without a guard nothing is refused.
	anubis, _ := maps.ByData(maps.Anubis)
	r2 := NewReplicator()
	r2.Residency = &Residency{Resident: anubis.Resident, Placed: uint32(anubis.Placed)}
	if err := r2.Queue(Entry{Entity: brain, Op: OpCreate, Create: Create{Def: 0x39}}); err != nil {
		t.Errorf("Anubis brain: %v", err)
	}
	if err := NewReplicator().Queue(Entry{Entity: brain, Op: OpCreate, Create: Create{Def: 0x39}}); err != nil {
		t.Errorf("no guard: %v", err)
	}
}
