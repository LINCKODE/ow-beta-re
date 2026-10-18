package match

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"owserver/inst/clock"
	"owserver/inst/jam"
	"owserver/internal/gamelink"
	"owserver/internal/maps"
)

func TestDebugShapes(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fl := &fakeLink{}
	m := NewManager(log, Config{FirstFrame: 100}, fl)
	defer m.Close()
	now := time.Now()
	tick := func(n int) {
		for range n {
			now = now.Add(clock.FrameDuration)
			m.Tick(now)
		}
	}
	if d, err := m.DebugShapes(0); err != nil || len(d.Shapes) != 0 || len(d.Groups) == 0 {
		t.Fatalf("no match: %+v, %v", d, err)
	}
	if err := m.Assign(gamelink.Assignment{ConnID: 1, Map: maps.Anubis, Name: "A"}); err != nil {
		t.Fatal(err)
	}
	m.Connected(1)
	tick(2)
	var cseq uint32
	m.Data(1, clientPacket(t, &cseq, 0, &jam.MapLoaded{}), now, nil, nil)
	tick(3)

	if _, err := m.DebugShapes(99); err == nil {
		t.Fatal("unknown player: no error")
	}
	d, err := m.DebugShapes(1)
	if err != nil {
		t.Fatal(err)
	}
	anubis, _ := maps.ByData(maps.Anubis)
	rooms := map[int]bool{}
	for _, s := range anubis.Spawns {
		rooms[s.Room] = true
	}
	count := map[string]int{}
	for _, s := range d.Shapes {
		count[s.Group+"/"+s.Kind]++
		if s.Kind == "box" && s.Half == nil || s.Kind == "line" && s.To == nil {
			t.Fatalf("incomplete shape %+v", s)
		}
	}
	if d.Name != anubis.Name || count["spawns/sphere"] != len(anubis.Spawns) || count["spawns/line"] != len(anubis.Spawns) ||
		count["rooms/box"] != len(rooms) {
		t.Fatalf("%s: %v (spawns %d, rooms %d)", d.Name, count, len(anubis.Spawns), len(rooms))
	}
	if len(d.HUD) < 2 || d.HUD[0].Group != "server" {
		t.Fatalf("hud: %+v", d.HUD)
	}
	if d0, _ := m.DebugShapes(0); len(d0.Shapes) != len(d.Shapes) {
		t.Fatalf("first match: %d shapes, want %d", len(d0.Shapes), len(d.Shapes))
	}
	t.Logf("%s: %v", d.Name, count)
}
