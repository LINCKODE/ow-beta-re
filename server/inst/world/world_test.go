package world

import (
	"io"
	"log/slog"
	"testing"

	"owserver/internal/maps"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// Every map in the catalogue builds, with one entity per networked placement in the
// server's ECS and the spawn points' teams.
func TestBuildEveryMap(t *testing.T) {
	for i := range maps.All {
		m := &maps.All[i]
		w, err := New(m.Data, quiet)
		if err != nil {
			t.Fatalf("%s: %v", m.Label(), err)
		}
		if w.Placed != len(m.Networked) || len(w.Placements()) != len(m.Networked) || w.ECSEntities() < w.Placed {
			t.Errorf("%s: %d placements, %d networked, %d in the ECS", m.Label(), w.Placed, len(m.Networked), w.ECSEntities())
		}
		teams := 0
		for _, e := range w.Placements() {
			if e.Team != NoTeam {
				teams++
			}
		}
		if teams != len(m.Spawns) {
			t.Errorf("%s: %d teamed placements, %d spawns", m.Label(), teams, len(m.Spawns))
		}
		if m.Kind == "game" || m.Kind == "tutorial" {
			if _, ok := w.NextStart(); !ok {
				t.Errorf("%s: no start spawn", m.Label())
			}
		}
		w.Tick()
		w.Close()
	}
}

func TestPlayers(t *testing.T) {
	w, err := New(maps.Anubis, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	n := w.ECSEntities()
	ctrl, err := w.Add(Entity{Kind: KindController, Owner: 7, Team: w.PlayerTeam()})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := w.Add(Entity{Kind: KindHero, Owner: 7})
	if ctrl.ID != 0x40000000|uint32(w.Placed+1) || body.ID != ctrl.ID+1 {
		t.Fatalf("ids %#x %#x", ctrl.ID, body.ID)
	}
	if got := w.ECSComponents(body.ID); len(got) < len(kindComponents[KindHero]) {
		t.Fatalf("hero components %x", got)
	}
	if err := w.Remove(body.ID); err != nil {
		t.Fatal(err)
	}
	w.Tick()
	again, _ := w.Add(Entity{Kind: KindHero, Owner: 7})
	if again.ID == body.ID {
		t.Fatal("ID reused")
	}
	if w.ECSEntities() != n+2 {
		t.Fatalf("ECS entities %d, want %d", w.ECSEntities(), n+2)
	}
	// Start spawns go round the room.
	a, _ := w.NextStart()
	b, _ := w.NextStart()
	if a.Index == b.Index || a.Team != maps.TeamAttack {
		t.Fatalf("spawns %+v %+v", a, b)
	}
	// The tutorial has no attackers' room: players take the room's team.
	tw, err := New(maps.Tutorial, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer tw.Close()
	if sp, ok := tw.NextStart(); !ok || sp.Team != tw.PlayerTeam() {
		t.Fatalf("tutorial start %+v %v", sp, ok)
	}
}
