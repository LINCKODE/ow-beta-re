package statescript

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Graphs from CASC (systems/statescript/fixtures/graph_0x<index>.stud, T055).
var fixtureDir = filepath.Join("..", "..", "fixtures")

func loadFixture(t *testing.T, rt *Runtime, guid uint64) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureDir, fmt.Sprintf("graph_0x%03x.stud", guid&0xFFF)))
	if err != nil {
		t.Skip("no fixture:", err)
	}
	if err := rt.LoadGraph(guid, b, File, 0); err != nil {
		t.Fatalf("load %#x: %v", guid, err)
	}
	for _, r := range rt.GraphRefs(guid) {
		if _, err := os.Stat(filepath.Join(fixtureDir, fmt.Sprintf("graph_0x%03x.stud", r&0xFFF))); err == nil && rt.GraphRefs(r) == nil {
			loadFixture(t, rt, r)
		}
	}
}

// Anubis: brain B and capture point A through the Go wrapper (the C test's scenario, shortened).
func TestMatchAnubisCaptureA(t *testing.T) {
	rt := NewRuntime()
	defer rt.Close()
	for _, g := range []uint32{0x029, 0x08f, 0x1e1, 0x028} {
		loadFixture(t, rt, GraphGUID(g))
	}
	m := rt.NewMatch(1000, 7)
	defer m.Close()
	const brain, pointA, hero = 0x40000030, 0x40000031, 0x40000002
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(m.Add(brain, 0x0400000000000039, WithSyncManager|PvPGame))
	for _, g := range []uint32{0x029, 0x08f, 0x1e1} {
		if id, err := m.Start(brain, GraphGUID(g), nil); err != nil || id <= 0 {
			t.Fatalf("start %#x: %d %v", g, id, err)
		}
	}
	must(m.Add(pointA, 0x0400000000000037, WithSyncManager))
	must(m.SetTransform(pointA, [3]float32{-43.78, 1.66, 45.9}, 0))
	must(m.SetCaptureArea(pointA, CaptureRules{DecayTime: 10, Segments: 3, ExtraPlayer: 1, CaptureTime: 30, F5f080cc5: 30,
		DecayDelay: 5, Fe6e7f301: 1, MaxPlayers: 3, Radius: 6}))
	if _, err := m.Start(pointA, GraphGUID(0x028), map[uint64]Value{VarID(0x02b8): {Type: Int, Bits: 1}, VarID(0x0c18): {Type: Int, Bits: 1}}); err != nil {
		t.Fatal(err)
	}
	must(m.Add(hero, 0x0400000000000225, PlayerHero))
	must(m.SetPlayer(hero, 0x40000001, 0x02E0000000000029))
	must(m.SetTeam(hero, 1))
	if m.SyncManager(hero) != nil || m.SyncManager(pointA) == nil {
		t.Fatal("sync managers: fact-only hero must have none, the point one")
	}
	m.Step(1002)
	must(m.Send(pointA, 0x024000000000000e, map[uint64]Value{0x0A40000000000010: {Type: Int, Bits: 0}}))
	must(m.Send(0, 0x024000000000002d, map[uint64]Value{0x0A40000000000029: {Type: Int64, Bits: VarID(0x15)}}))
	m.Step(1006)
	fx := m.Effects()
	enabled := false
	for _, e := range fx {
		if e.Kind == FxCaptureEnabled && e.Entity == pointA && e.I0 == 1 {
			enabled = true
		}
	}
	if !enabled {
		t.Fatalf("A not enabled; effects %v", fx)
	}
	must(m.SetTransform(hero, [3]float32{-43.78, 1.66, 45.9}, 0))
	m.Step(1006 + 313) // 5 s
	c, err := m.Capture(pointA)
	if err != nil || c.Progress < 0.14 || c.Progress > 0.19 || c.CapturingTeam != 1 {
		t.Fatalf("after 5 s: %+v %v", c, err)
	}
	// The point's sync state is readable through the borrowed sync manager (for the remote encoders).
	sm := m.SyncManager(pointA)
	if ids := sm.Instances(); len(ids) != 1 || ids[0] <= 0 {
		t.Fatalf("point instances %v", ids)
	}
	if len(m.Effects()) == 0 {
		t.Fatal("no progress effects")
	}
}
