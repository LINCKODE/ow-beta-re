package replication

import (
	"bytes"
	"testing"

	"owserver/inst/wire"
	"owserver/internal/heroes"
	rnd "owserver/internal/instance"
)

// The encoders were moved from the R&D server: every one must produce its bytes.
// (Delete the rnd comparisons if the R&D server goes away.)

func payload(t *testing.T, frame uint32, es ...Entry) []byte {
	t.Helper()
	p := wire.ServerPayload{Frame: frame}
	for _, e := range es {
		p.Entities = append(p.Entities, e.wire(frame, Stopgap{}))
	}
	b, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func rndPayload(t *testing.T, frame uint32, sp ...rnd.Spawn) []byte {
	t.Helper()
	b, err := rnd.EncodeDataPayload(frame, 0, nil, sp...)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func same(t *testing.T, name string, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Errorf("%s:\n got %x\nwant %x", name, got, want)
	}
}

func TestSameBytesAsRnD(t *testing.T) {
	rot, scale := [4]float32{0, 0, 0, 1}, [3]float32{1, 1, 1}
	hero := uint64(0x02E0000000000029)
	// The controller.
	same(t, "controller",
		payload(t, 7, Entry{Entity: EntityID(1), Op: OpCreate, Create: Create{Def: 0x0e, Flags: 0x38, Rot: rot, Scale: scale}}),
		rndPayload(t, 7, rnd.Spawn{ID: 1, Def: 0x0e, Flags: 0x38, Rot: rot, Scale: scale}))
	// A map placement with a movement snapshot, and one without.
	same(t, "from map + mover",
		payload(t, 9, Entry{Entity: EntityID(5), Op: OpCreate, Create: Create{FromMap: true, MapIndex: 0x123, Mover: &Mover{Pos: [3]float32{1.5, -2, 300}, Yaw: 1.2}}}),
		rndPayload(t, 9, rnd.Spawn{ID: 5, FromMap: true, MapIndex: 0x123, Mover: &rnd.Mover{Pos: [3]float32{1.5, -2, 300}, Yaw: 1.2}}))
	same(t, "from map",
		payload(t, 9, Entry{Entity: EntityID(6), Op: OpCreate, Create: Create{FromMap: true, MapIndex: 77}}),
		rndPayload(t, 9, rnd.Spawn{ID: 6, FromMap: true, MapIndex: 77}))
	// A hero body: definition, snapshot, health + team.
	pos := [3]float32{-10.25, 5, 44}
	same(t, "hero body",
		payload(t, 11, Entry{Entity: EntityID(2), Op: OpCreate,
			Create:     Create{Def: 0x2cd, Flags: 0x08, Pos: pos, Rot: YawQuat(0.5), Scale: scale, Mover: &Mover{Pos: pos, Yaw: 0.5}},
			Components: []ComponentUpdate{PlayerHealth(200), Mirror10(TeamBit(1))}}),
		rndPayload(t, 11, rnd.Spawn{ID: 2, Def: 0x2cd, Flags: 0x08, Pos: pos, Rot: YawQuat(0.5), Scale: scale, Mover: &rnd.Mover{Pos: pos, Yaw: 0.5},
			Components: []rnd.ComponentUpdate{rnd.PlayerHealth(200), rnd.Mirror10(rnd.TeamBit(1))}}))
	// The controller link on lock-in.
	same(t, "controller update",
		payload(t, 12, Entry{Entity: EntityID(1), Op: OpUpdate, Components: []ComponentUpdate{Mirror20(0x40000002, 0x40000002, 0x40000002),
			Mirror10(TeamBit(1)), Mirror3F(PlayerInfo{Hero: &hero})}}),
		rndPayload(t, 12, rnd.Spawn{ID: 1, Update: true, Components: []rnd.ComponentUpdate{rnd.Mirror20(0x40000002, 0x40000002, 0x40000002),
			rnd.Mirror10(rnd.TeamBit(1)), rnd.Mirror3F(rnd.PlayerInfo{Hero: &hero})}}))
	same(t, "destroy",
		payload(t, 13, Entry{Entity: EntityID(2), Op: OpDestroy}),
		rndPayload(t, 13, rnd.Spawn{ID: 2, Destroy: true}))
}

func TestHeroStartupSameBytesAsRnD(t *testing.T) {
	n := 0
	for _, h := range heroes.All {
		st, ok := heroes.Start(h.Def)
		if !ok {
			continue
		}
		mine, err := HeroStartup(1000, st)
		if err != nil {
			t.Fatal(err)
		}
		theirs, err := rnd.HeroStartupFullUpdate(1000, st)
		if err != nil {
			t.Fatal(err)
		}
		// Compare through a payload (the R&D writer's fields are unexported).
		got := payload(t, 1000, Entry{Entity: EntityID(2), Scripts: mine})
		want := rndPayload(t, 1000, rnd.Spawn{ID: 2, Scripts: theirs})
		same(t, h.Name, got, want)
		n++
	}
	if n == 0 {
		t.Fatal("no hero start-ups")
	}
}

func TestRemoteFullUpdateSameBytesAsRnD(t *testing.T) {
	graphs := []Graph{{Index: 0x29, SyncVars: 9, RemoteNodes: 2}, {Index: 0x42, SyncVars: 0, RemoteNodes: 3}}
	active := map[int][]State{1: {{Index: 1, Data: []bool{true}}}, 2: {{Index: 0, Off: true}}}
	var rg []rnd.ScriptGraph
	for _, g := range graphs {
		rg = append(rg, rnd.ScriptGraph{Index: g.Index, SyncVars: g.SyncVars, RemoteNodes: g.RemoteNodes})
	}
	ra := map[int][]rnd.ScriptState{1: {{Index: 1, Data: []bool{true}}}, 2: {{Index: 0, Off: true}}}
	same(t, "remote full update",
		payload(t, 50, Entry{Entity: EntityID(48), Scripts: RemoteFullUpdate(50, graphs, active)}),
		rndPayload(t, 50, rnd.Spawn{ID: 48, Scripts: rnd.ScriptFullUpdate(50, rg, ra)}))
}

func TestReplicatorPacking(t *testing.T) {
	r := NewReplicator()
	create := func(id uint32) Entry {
		return Entry{Entity: EntityID(id), Op: OpCreate, Create: Create{FromMap: true, MapIndex: id}}
	}
	update := func(id uint32) Entry {
		return Entry{Entity: EntityID(id), Op: OpUpdate, Components: []ComponentUpdate{Mirror10(TeamBit(0))}}
	}
	if err := r.Queue(create(3), create(4), update(3), update(4), create(5)); err != nil {
		t.Fatal(err)
	}
	if !r.Has(EntityID(4)) || r.Has(EntityID(9)) {
		t.Fatal("Has")
	}
	for i := range 5 {
		es, _ := r.Take(uint32(i))
		if len(es) != 1 {
			t.Fatalf("one per packet: %d", len(es))
		}
		r.Sent(uint32(i), es)
	}
	// Batched: updates share a packet, a create ends it.
	r.OnePerPacket = false
	r.Lost([]uint32{1, 2, 3}) // create 4, update 3, update 4 again, in order
	if r.Pending() != 3 || r.Resent != 3 {
		t.Fatalf("pending %d", r.Pending())
	}
	es, _ := r.Take(10)
	if len(es) != 1 || es[0].Op != OpCreate || es[0].Entity != EntityID(4) {
		t.Fatalf("%+v", es)
	}
	r.Queue(Entry{Entity: EntityID(3), Op: OpDestroy}, create(6))
	es, _ = r.Take(11)
	if len(es) != 4 || es[3].Op != OpCreate || es[2].Op != OpDestroy {
		t.Fatalf("batch %d", len(es))
	}
	if r.Has(EntityID(3)) || r.Pending() != 0 {
		t.Fatal("destroy / pending")
	}
	if err := r.Queue(Entry{Entity: 1, Op: OpCreate, Create: Create{Flags: 0x01}}); err == nil {
		t.Fatal("flag 0x01 accepted")
	}
}
