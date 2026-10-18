package instance

import (
	"io"
	"log/slog"
	"testing"

	"owserver/internal/gamelink"
	"owserver/internal/maps"
)

const anubis = 0x080000000000005b

// Two connections on one map share one world, built at the first assignment; it
// ends with its last connection.
func TestWorldPerMatch(t *testing.T) {
	s := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.Assign(gamelink.Assignment{ConnID: 1, Map: anubis})
	s.Assign(gamelink.Assignment{ConnID: 2, Map: anubis})
	ws := s.Worlds()
	if len(ws) != 1 || len(ws[0].Conns) != 2 {
		t.Fatalf("worlds: %+v", ws)
	}
	w := s.conns[1].world
	if w == nil || s.conns[2].world != w {
		t.Fatal("the two connections don't share a world")
	}
	m, _ := maps.ByData(anubis)
	if w.Creates != len(m.Networked) || w.Updates != len(m.Spawns) || len(w.Entities) != w.Creates+w.Updates {
		t.Fatalf("world: %d creates, %d updates, %d entities; map: %d networked, %d spawns", w.Creates, w.Updates, len(w.Entities), len(m.Networked), len(m.Spawns))
	}
	for i, e := range w.Entities[:w.Creates] {
		if !e.FromMap || e.MapIndex != m.Networked[i].Index || int(e.ID) != firstMapEntity+i {
			t.Fatalf("entity %d: %+v", i, e)
		}
	}
	// Start spawn points are handed out per world, not per connection.
	if a, b := w.takeSpawn(), w.takeSpawn(); a == b {
		t.Fatalf("spawn indexes %d, %d", a, b)
	}

	s.mu.Lock()
	s.leaveWorld(s.conns[1])
	s.mu.Unlock()
	if ws := s.Worlds(); len(ws) != 1 || len(ws[0].Conns) != 1 {
		t.Fatalf("after one leaves: %+v", ws)
	}
	s.mu.Lock()
	s.leaveWorld(s.conns[2])
	s.mu.Unlock()
	if ws := s.Worlds(); len(ws) != 0 {
		t.Fatalf("after both leave: %+v", ws)
	}
	// A new match on the map gets a fresh world.
	s.Assign(gamelink.Assignment{ConnID: 3, Map: anubis})
	if s.conns[3].world == w {
		t.Fatal("the ended world was reused")
	}
}

// Re-assigning a connection (the lobby's hello after a reconnect) keeps one
// membership.
func TestWorldReassign(t *testing.T) {
	s := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.Assign(gamelink.Assignment{ConnID: 1, Map: anubis})
	s.Assign(gamelink.Assignment{ConnID: 1, Map: anubis})
	if ws := s.Worlds(); len(ws) != 1 || len(ws[0].Conns) != 1 {
		t.Fatalf("worlds: %+v", ws)
	}
}

// PlayerInfo's hero: field 0 of 10 (two mask bytes), a compressed u64.
func TestMirror3F(t *testing.T) {
	hero := uint64(0x02e0000000000029)
	u := Mirror3F(PlayerInfo{Hero: &hero})
	if u.ID != 0x3f || len(u.Mask) != 2 || u.Mask[0] != 1 || u.Mask[1] != 0 {
		t.Fatalf("mask %x", u.Mask)
	}
	if got := u.Values.buf[:u.Values.nbit/8]; string(got) != string([]byte{0xc1, 0x29, 0xe0, 0x02}) || u.Values.nbit != 32 {
		t.Fatalf("values %x (%d bits)", got, u.Values.nbit)
	}
	one := uint8(1)
	u = Mirror3F(PlayerInfo{Pending: &hero, Enabled: &one})
	if u.Mask[0] != 1<<3 || u.Mask[1] != 1<<1 || u.Values.nbit != 40 {
		t.Fatalf("mask %x, %d bits", u.Mask, u.Values.nbit)
	}
}
