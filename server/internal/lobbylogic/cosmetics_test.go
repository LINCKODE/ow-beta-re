package lobbylogic

import (
	"encoding/binary"
	"path/filepath"
	"testing"
)

func useTempCosmetics(t *testing.T) {
	t.Setenv("LOGIC_COSMETICS", filepath.Join(t.TempDir(), "cosmetics.json"))
	cosmeticsMu.Lock()
	cosmetics, cosmeticsLoaded = cosmeticsState{}, false
	cosmeticsMu.Unlock()
}

func TestHeroTables(t *testing.T) {
	if len(heroInfos) != len(heroes) {
		t.Fatalf("%d hero infos, %d heroes", len(heroInfos), len(heroes))
	}
	for i, h := range heroInfos {
		if h.guid != heroes[i] {
			t.Errorf("hero %d: %#x != %#x", i, h.guid, heroes[i])
		}
		themes := 0
		for _, u := range h.unlocks {
			if _, ok := unlockInfos[u]; !ok {
				t.Errorf("%s: unlock %#x not in unlockInfos", h.name, u)
			}
			if unlockInfos[u].class == classSkinTheme {
				themes++
			}
		}
		if _, ok := themeForSlot(&heroInfos[i], 2); !ok || themes != 2 {
			t.Errorf("%s: %d themes", h.name, themes)
		}
	}
}

func TestFindHeroAndUnlock(t *testing.T) {
	for in, want := range map[string]string{"lucio": "Lúcio", "Soldier76": "Soldier: 76", "torb": "Torbjörn", "dva": "D.Va", "dd": "Mei", "MEI": "Mei"} {
		h, err := findHero(in)
		if err != nil || h.name != want {
			t.Errorf("findHero(%q) = %v, %v; want %s", in, h, err, want)
		}
	}
	if _, err := findHero("r"); err == nil {
		t.Error("ambiguous prefix accepted")
	}
	mei, _ := findHero("mei")
	for in, want := range map[string]uint64{"ice wall": 0x0250000000000156, "156": 0x0250000000000156, "theme:slot 1": 0x025000000000011e} {
		if u, err := findUnlock(mei, in); err != nil || u != want {
			t.Errorf("findUnlock(mei, %q) = %#x, %v", in, u, err)
		}
	}
	if _, err := findUnlock(mei, "0x0250000000000053"); err == nil {
		t.Error("Tracer's unlock accepted for Mei")
	}
}

// recordAt decodes the fields of the n-th 24900 record that the tests check.
func recordAt(t *testing.T, b []byte, n int) (level uint32, hero, xp uint64, equipped [8]uint64, themes [][2]uint64, owned []uint64) {
	off := 8
	for i := 0; ; i++ {
		level = binary.LittleEndian.Uint32(b[off+1:])
		hero = binary.LittleEndian.Uint64(b[off+5:])
		xp = binary.LittleEndian.Uint64(b[off+13:])
		for j := range equipped {
			equipped[j] = binary.LittleEndian.Uint64(b[off+37+8*j:])
		}
		off += 37 + 64
		themes = nil
		for k := binary.LittleEndian.Uint32(b[off:]); k > 0; k-- {
			themes = append(themes, [2]uint64{binary.LittleEndian.Uint64(b[off+4:]), binary.LittleEndian.Uint64(b[off+12:])})
			off += 16
		}
		off += 4
		owned = nil
		for k := binary.LittleEndian.Uint32(b[off:]); k > 0; k-- {
			owned = append(owned, binary.LittleEndian.Uint64(b[off+4+5:]))
			off += 21
		}
		off += 4
		if i == n {
			return
		}
	}
}

func TestHeroProgressionFeaturedAndLoadout(t *testing.T) {
	useTempCosmetics(t)
	mei, _ := findHero("mei")
	withCosmetics(func(s *cosmeticsState) {
		s.Featured = mei.guid
		s.Heroes[mei.guid] = &heroLoadout{}
		s.Heroes[mei.guid].Equipped[equipSlot[classSkinTheme]] = 0x025000000000011e
		s.Heroes[mei.guid].Equipped[equipSlot[classPOTG]] = 0x0250000000000156
	})
	b := heroProgression(heroRecords(7, 500, 1000))
	if got := binary.LittleEndian.Uint32(b); got != heroLevelCap {
		t.Fatalf("cap %d", got)
	}
	candidates := 0
	for i := range heroInfos {
		level, hero, xp, equipped, themes, owned := recordAt(t, b, i)
		if xp != 0 || level > 1 {
			candidates++
			if hero != mei.guid {
				t.Errorf("hero %#x is a featured candidate", hero)
			}
		}
		if len(owned) != len(heroInfos[i].unlocks) {
			t.Errorf("%#x owns %d unlocks", hero, len(owned))
		}
		if hero == mei.guid {
			if level != 7 || equipped[0] != 0x025000000000011e || equipped[4] != 0x0250000000000156 {
				t.Errorf("mei record: level %d equipped %x", level, equipped)
			}
			if len(themes) != 1 || themes[0] != [2]uint64{mei.skin, 0x025000000000011e} {
				t.Errorf("mei themes %x", themes)
			}
		}
	}
	if candidates != 1 {
		t.Fatalf("%d featured candidates", candidates)
	}
}

func TestEquipMessages(t *testing.T) {
	b := equipMessage(0x02E00000000000DD, 0x0AE00000000003DD, 0x025000000000011e)
	if len(b) != 24 || binary.LittleEndian.Uint64(b[8:]) != 0x0AE00000000003DD || binary.LittleEndian.Uint64(b[16:]) != 0x025000000000011e {
		t.Fatalf("24903 %x", b)
	}
	if b := skinMessage(1, 2); len(b) != 16 || b[8] != 2 {
		t.Fatalf("24902 %x", b)
	}
}

func TestCosmeticsFileRoundTrip(t *testing.T) {
	mei, _ := findHero("mei")
	s := cosmeticsState{Featured: mei.guid, Heroes: map[uint64]*heroLoadout{mei.guid: {}}}
	s.Heroes[mei.guid].Equipped[equipSlot[classSkinTheme]] = 0x025000000000011e
	s.Heroes[mei.guid].Equipped[equipSlot[classSpray]] = 0x0250000000000181 // Reaper's: dropped on load
	f := s.file()
	if f.Featured != "Mei" || f.Heroes["Mei"]["theme"] != "0x025000000000011e" {
		t.Fatalf("file form %+v", f)
	}
	back := f.state()
	if back.Featured != mei.guid || back.Heroes[mei.guid].Equipped != [8]uint64{0x025000000000011e} {
		t.Fatalf("state %+v", back.Heroes[mei.guid])
	}
}
