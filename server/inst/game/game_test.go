package game

import (
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"

	"owserver/inst/replication"
	"owserver/inst/sim"
	"owserver/inst/world"
	"owserver/internal/maps"

	"owcontent/content"
)

// The tests read graphs and definitions from the local 0.8 install (skipped without it).
const install = "../../../game/owbetaclient"

var (
	storeOnce sync.Once
	store     *content.Store
	storeErr  error
)

func engine(t *testing.T) *sim.Engine {
	t.Helper()
	if _, err := os.Stat(install); err != nil {
		t.Skip("no 0.8 install:", err)
	}
	storeOnce.Do(func() { store, storeErr = content.Open(install, nil) })
	if storeErr != nil {
		t.Skip("content:", storeErr)
	}
	e := sim.NewEngine(quiet(), store)
	t.Cleanup(e.Close)
	return e
}

func quiet() *slog.Logger {
	if os.Getenv("GAME_LOG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// secs is n seconds in frames.
func secs(n float64) int { return int(n*62.5 + 0.5) }

// Anubis assault on the server: setup → in progress → A enabled; an attacker on A captures it (30 s), which unlocks
// B; B captured (18 s) → ended. Positions come from the world (the position source), as the live server does.
func TestAnubisAssault(t *testing.T) {
	eng := engine(t)
	w, err := world.New(maps.Anubis, quiet())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	frame := uint32(1)
	g, err := New(quiet(), eng, w, frame, Config{SetupFrames: uint32(secs(2))})
	if err != nil || g == nil {
		t.Fatalf("new: %v", err)
	}
	defer g.Close()
	ps := g.Points()
	if len(ps) != 2 || ps[0].Order != 0 || ps[1].Order != 1 || ps[0].Index != 0x1d6 || ps[1].Index != 0x1d5 {
		t.Fatalf("points %+v", ps)
	}
	a, b := ps[0], ps[1]
	if a.Owner != 0 || b.Owner != 0 {
		t.Fatalf("init: owners %d %d", a.Owner, b.Owner)
	}
	kinds := map[world.Kind]int{}
	for _, e := range g.Entities() {
		kinds[e.Kind]++
	}
	if kinds[world.KindBrain] != 1 || kinds[world.KindObjective] != 6 {
		t.Fatalf("entities %v", kinds)
	}
	tick := func(n int) {
		for range n {
			frame++
			g.Tick(frame)
		}
	}
	tick(10)
	if g.Phase != Waiting {
		t.Fatalf("phase %s without players", g.Phase)
	}
	// A player locks in: an attacker at the start room.
	ctrl, _ := w.Add(world.Entity{Kind: world.KindController, Def: 0x0e, Team: 1})
	sp, _ := w.NextStart()
	body, _ := w.Add(world.Entity{Kind: world.KindHero, Def: 0x225, Team: 1, Pos: sp.Pos, Yaw: sp.Yaw, Health: 200})
	g.HeroSpawned(body.ID, ctrl.ID, 0x02E0000000000029)
	tick(1)
	if g.Phase != Setup {
		t.Fatalf("phase %s after the first hero", g.Phase)
	}
	tick(secs(2) + 2)
	if g.Phase != InProgress || !a.Enabled || b.Enabled {
		t.Fatalf("after setup: phase %s, A %+v, B %+v", g.Phase, a, b)
	}
	// Nobody on A: no progress.
	tick(secs(3))
	if a.Progress != 0 {
		t.Fatalf("A progress %v with nobody on it", a.Progress)
	}
	// The attacker walks onto A (the position source moves the world entity).
	body.Pos = a0(w, a)
	tick(secs(5))
	if a.Progress < 0.14 || a.Progress > 0.19 || a.Capturing != 1 || !a.Dirty {
		t.Fatalf("A after 5 s: %+v", a)
	}
	tick(secs(26))
	if a.Progress < 1 || a.Enabled || !b.Enabled {
		t.Fatalf("A after 31 s: %+v, B %+v", a, b)
	}
	body.Pos = a0(w, b)
	tick(secs(19))
	tick(2)
	if b.Progress < 1 || g.Phase != Ended {
		t.Fatalf("B after 19 s: %+v, phase %s", b, g.Phase)
	}
	if len(g.Unhandled) != 0 {
		t.Logf("unhandled effects: %v", g.Unhandled)
	}
	t.Logf("effects %v", g.Effects)
}

// a0 is a point's position (its world entity).
func a0(w *world.World, p *Point) [3]float32 { return w.Get(p.ID).Pos }

// The per-player view: creates + remote full updates on join, then deltas and capture mirror updates as the point
// is captured; every Statescript entry encodes.
func TestViewReplication(t *testing.T) {
	eng := engine(t)
	w, err := world.New(maps.Anubis, quiet())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	frame := uint32(1)
	g, err := New(quiet(), eng, w, frame, Config{SetupFrames: 10})
	if err != nil || g == nil {
		t.Fatalf("new: %v", err)
	}
	defer g.Close()
	v := NewView()
	join := g.Join(v)
	creates, scripts := 0, 0
	for _, e := range join {
		if e.Op == replication.OpCreate {
			creates++
		}
		if e.ScriptsAt != nil {
			if e.ScriptsAt(frame) == nil {
				t.Fatalf("full update of %#x did not encode", e.Entity)
			}
			scripts++
			e.Acked()
		}
	}
	if creates != 7 || scripts != 7 {
		t.Fatalf("join: %d creates, %d full updates", creates, scripts)
	}
	// Every create passes the residency guard (docs/InstanceServer.md §12.15).
	guarded := replication.NewReplicator()
	guarded.Residency = &replication.Residency{Resident: w.Map.Resident, Placed: uint32(w.Map.Placed)}
	if err := guarded.Queue(join...); err != nil || guarded.Refused != 0 {
		t.Fatalf("join refused by the residency guard: %v", err)
	}
	ctrl, _ := w.Add(world.Entity{Kind: world.KindController, Def: 0x0e, Team: 1})
	body, _ := w.Add(world.Entity{Kind: world.KindHero, Def: 0x225, Team: 1, Health: 200})
	g.HeroSpawned(body.ID, ctrl.ID, 0x02E0000000000029)
	a := g.Points()[0]
	body.Pos = w.Get(a.ID).Pos
	deltas, mirrors := 0, 0
	for range secs(8) {
		frame++
		g.Tick(frame)
		for _, e := range g.Update(v, frame) {
			if e.ScriptsAt != nil {
				if e.ScriptsAt(frame) == nil {
					t.Fatalf("delta of %#x did not encode", e.Entity)
				}
				deltas++
			}
			if len(e.Components) > 0 {
				mirrors++
			}
			e.Acked()
		}
	}
	if deltas == 0 || mirrors < 100 || a.Progress < 0.2 {
		t.Fatalf("%d deltas, %d mirror updates, progress %v", deltas, mirrors, a.Progress)
	}
	t.Logf("%d deltas, %d mirror updates over 8 s", deltas, mirrors)
}

// Only a map whose own brain is the mode's host brain gets a game: the tutorial (T059's crash: the assault brain
// 0x39 is not loaded there), the other assault maps (their own brains) and escort maps get none.
func TestNoHostWithoutTheMapsBrain(t *testing.T) {
	eng := engine(t)
	for _, data := range []uint64{maps.Tutorial, 0x0800000000000165, 0x08000000000001DB, 0x08000000000002C3} {
		w, err := world.New(data, quiet())
		if err != nil {
			t.Fatal(err)
		}
		g, err := New(quiet(), eng, w, 1, Config{})
		if err != nil || g != nil {
			t.Errorf("%s (mode %q, brain %#x): game %v, err %v; want none", w.Map.Name, w.Map.Mode, w.Map.Brain, g != nil, err)
		}
		if g != nil {
			g.Close()
		}
		w.Close()
	}
}
