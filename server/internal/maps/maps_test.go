package maps

import (
	"math"
	"slices"
	"testing"
)

// The hand list read from the client (system 8's pending list on Anubis without a server,
// 2026-09-24) that server.go used before the catalogue.
var anubisHandList = []uint32{
	0x1dd, 0x1de, 0x1df, 0x1e0, 0x1e1, 0x1e2, 0x1e3, 0x1e4, 0x1e5, 0x1e6,
	0x1e7, 0x1e8, 0x1e9, 0x1ea, 0x1eb, 0x1ec, 0x1ed, 0x1ee, 0x1ef, 0x1f0,
	0x41d, 0x41e,
}

func TestAnubisNetworkedEqualsHandList(t *testing.T) {
	m, ok := ByData(Anubis)
	if !ok {
		t.Fatal("no Anubis")
	}
	var got []uint32
	for _, p := range m.Networked {
		got = append(got, p.Index)
	}
	if !slices.Equal(got, anubisHandList) {
		t.Fatalf("networked = %#x, want %#x", got, anubisHandList)
	}
}

// Anubis' movers as measured live (docs/MatchLogic.md §8.1).
func TestAnubisMovers(t *testing.T) {
	m, _ := ByData(Anubis)
	want := map[uint32]struct {
		def uint64
		pos [3]float32
		yaw float32
	}{
		0x1d8: {0x0400000000000133, [3]float32{18.738771, 0.7936692, -18.289146}, 0.95993},
		0x1d9: {0x0400000000000133, [3]float32{-4.06, 0.79, -34.25}, 0.960},
		0x1db: {0x0400000000000127, [3]float32{-44.89, 5.48, 27.11}, 2.570},
		0x1dc: {0x0400000000000127, [3]float32{-48.10, 1.51, 34.46}, 0.999},
		0x1d7: {0x040000000000005b, [3]float32{3.27, 2.77, -20.59}, float32(math.NaN())},
		0x20a: {0x04000000000002ed, [3]float32{-7.5, 2.48, 111.3}, float32(math.NaN())},
		0x20c: {0x04000000000002ed, [3]float32{-45.6, -2.20, 43.5}, float32(math.NaN())},
	}
	if len(m.Movers) != len(want) {
		t.Fatalf("%d movers, want %d", len(m.Movers), len(want))
	}
	near := func(a, b, eps float32) bool { return math.Abs(float64(a-b)) <= float64(eps) }
	for _, p := range m.Movers {
		w, ok := want[p.Index]
		if !ok {
			t.Errorf("unexpected mover %#x", p.Index)
			continue
		}
		if p.Def != w.def {
			t.Errorf("%#x: def %#x, want %#x", p.Index, p.Def, w.def)
		}
		for i := range 3 {
			if !near(p.Pos[i], w.pos[i], 0.01) {
				t.Errorf("%#x: pos %v, want %v", p.Index, p.Pos, w.pos)
				break
			}
		}
		if !math.IsNaN(float64(w.yaw)) && !near(p.Yaw, w.yaw, 0.001) {
			t.Errorf("%#x: yaw %v, want %v", p.Index, p.Yaw, w.yaw)
		}
	}
}

func TestFind(t *testing.T) {
	for arg, want := range map[string]uint64{
		"anubis":             Anubis,
		"Temple of Anubis":   Anubis,
		"0x080000000000005B": Anubis,
		"0x079000000000005B": Anubis,
		"5b":                 Anubis,
		"kings row":          0x08000000000000D4,
		"king's row":         0x08000000000000D4,
		"gibraltar":          0x0800000000000184,
		"hollywood":          0x08000000000002AF,
		"tutorial":           0x0800000000000661,
		"0x677":              0x0800000000000677,
	} {
		m, ok := Find(arg)
		if !ok || m.Data != want {
			t.Errorf("Find(%q) = %v, want %#x", arg, m, want)
		}
	}
	for _, arg := range []string{"", "nowhere", "0x1234"} {
		if m, ok := Find(arg); ok {
			t.Errorf("Find(%q) = %s", arg, m.Label())
		}
	}
}

func TestPlayable(t *testing.T) {
	d := PlayableData()
	if len(d) != 9 || d[0] != Anubis {
		t.Fatalf("PlayableData = %#x", d)
	}
	for _, m := range Playable() {
		if m.Name == "" || len(m.Networked) == 0 {
			t.Errorf("%s: no name or no networked entities", m.Label())
		}
		if len(m.Networked) > 250 {
			t.Errorf("%s: %d networked entities do not fit the u8 entity ids", m.Label(), len(m.Networked))
		}
	}
}

// Every playable map has a start room of 6 for each team; Anubis' attackers start behind the ship.
func TestStartSpawns(t *testing.T) {
	for _, m := range Playable() {
		for _, team := range []int{TeamAttack, TeamDefend} {
			if got := m.StartSpawns(team); len(got) != 6 {
				t.Errorf("%s team %d: %d start spawns", m.Label(), team, len(got))
			}
		}
	}
	m, _ := ByData(Anubis)
	a := m.StartSpawns(TeamAttack)
	if a[0].Index != 0x1e3 || a[0].Pos[2] > -40 {
		t.Errorf("Anubis attack start %#x at %v", a[0].Index, a[0].Pos)
	}
}

// The residency tables (docs/InstanceServer.md §12.15): the generated data agrees with the live measurements.
func TestResidency(t *testing.T) {
	if !slices.IsSorted(Global) || len(Global) != 361 {
		t.Fatalf("Global: %d definitions, sorted %v (want 361, sorted)", len(Global), slices.IsSorted(Global))
	}
	anubis, _ := ByData(Anubis)
	tut, _ := ByData(Tutorial)
	cases := []struct {
		m    *Map
		def  uint64
		want bool
	}{
		{anubis, 0x0400000000000039, true},  // the assault brain: Anubis' own
		{tut, 0x0400000000000039, false},    // not on the tutorial: the T059 crash
		{tut, 0x040000000000128E, true},     // the tutorial's brain
		{tut, 0x040000000000000E, true},     // the controller (global)
		{tut, 0x04000000000003CF, true},     // Soldier: 76 (heroes are global)
		{anubis, 0x04000000000012E3, false}, // Dorado's brain
	}
	for _, c := range cases {
		if got := c.m.Resident(c.def); got != c.want {
			t.Errorf("%s: Resident(%#x) = %v, want %v", c.m.Name, c.def, got, c.want)
		}
	}
	for i := range All {
		m := &All[i]
		if !slices.IsSorted(m.Defs) {
			t.Errorf("%s: Defs not sorted", m.Label())
		}
		// Every map's own brain and every placement the server creates from the map must be resident.
		if m.Brain != 0 && !m.Resident(m.Brain) {
			t.Errorf("%s: brain %#x not resident", m.Label(), m.Brain)
		}
		for _, ps := range [][]Placement{m.Networked, m.Movers} {
			for _, p := range ps {
				if !m.Resident(p.Def) {
					t.Errorf("%s: placement %#x def %#x not resident", m.Label(), p.Index, p.Def)
				}
			}
		}
		for _, o := range m.Objectives {
			if !m.Resident(o.Def) {
				t.Errorf("%s: objective %#x def %#x not resident", m.Label(), o.Index, o.Def)
			}
		}
	}
	if tut.Mode != "tutorial" || tut.Brain != 0x040000000000128E {
		t.Errorf("tutorial: mode %q brain %#x, want tutorial / 0x128E", tut.Mode, tut.Brain)
	}
}
