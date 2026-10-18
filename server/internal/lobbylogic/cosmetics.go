package lobbylogic

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Hero cosmetics: the featured title-screen hero and what each hero wears (T026,
// docs/LobbyServer.md "Title hero and cosmetics").
//
// Featured hero: profile +0x230, read by ConfigVar 0x29969708 (FUN_7ff650d78680). It is
// written only by the profile constructor (FUN_7ff650cc77e0, = 0) and by the picker
// FUN_7ff650cc93b0, which runs while it is 0: a random hero among the 24900 records with
// record +0x10 != 0 or level > 1 (else among all records). 24900 doesn't reset it, nor
// does the lobby-disconnect reset FUN_7ff650cc7d60, so the pick holds for the client
// process. We steer it: with a featured hero set, only that record has +0x10 != 0 and
// level > 1 may only be its level, so the next pick (next client launch) is that hero. [G]
//
// Equipped cosmetics: hero record +0x28..+0x60 (see equipSlot) and +0x68 []{ctx, theme}.
// The title hero's look is FUN_7ff650cc8d20(profile, hero, skin): the theme of the +0x68
// entry whose ctx == the equipped skin (+0x20), its slot (unlock +0x38, STU field
// 0xbd085e06; default 1), then the skin's STUSkinTheme variant with that m_slot. Record
// +0x28 (theme with ctx 0) is not read there. 24903 {hero, ctx, unlock} equips live
// (FUN_7ff650cc8540), but only unlocks loaded in profile +0x188: every unlock list the
// client receives loads there, so each 24900 record owns (+0x80) all its hero's unlocks. [G]

type unlockClass uint8

const (
	classSkinTheme unlockClass = iota + 1
	classPOTG
	classSpray
	classAvatarPortrait
	classPortraitFrame
)

var classNames = map[unlockClass]string{
	classSkinTheme: "theme", classPOTG: "intro", classSpray: "spray",
	classAvatarPortrait: "icon", classPortraitFrame: "frame",
}

type heroInfo struct {
	guid     uint64
	name     string
	skin     uint64    // STUHero.m_skins[0] (every 0.8 hero has one skin)
	unlocks  []uint64  // STUProgressionUnlocks, in progression order
	variants [4]string // the skin's STUSkinTheme names, m_slot 1-4
}

type unlockInfo struct {
	class unlockClass
	name  string
	slot  uint32 // SkinTheme: the skin variant (m_slot) it selects
}

// equipSlot is the hero-record field (index into heroLoadout.Equipped, record
// +0x28 + 8*i) an unlock class is equipped into (FUN_7ff650cc8540). HeroMod (slots
// 1-3), class 0x5F38A9D5 (6) and Trophy (7) have no unlocks in 0.8; AvatarPortrait
// goes to profile +0x150 (24300) and PortraitFrame is ignored.
var equipSlot = map[unlockClass]int{classSkinTheme: 0, classPOTG: 4, classSpray: 5}

// heroLoadout is what one hero wears; 0 = the client's default.
type heroLoadout struct {
	Equipped [8]uint64 // record +0x28..+0x60
}

type cosmeticsState struct {
	Featured uint64 // 0 = let the client pick at random
	Heroes   map[uint64]*heroLoadout
}

// cosmeticsJSON is the file form (LOGIC_COSMETICS, default lobby-cosmetics.json):
// {"featured": "Mei", "heroes": {"Mei": {"theme": "0x025000000000011e"}}}, keys =
// hero names, one entry per equipped class ("theme", "intro", "spray").
type cosmeticsJSON struct {
	Featured string                       `json:"featured,omitempty"`
	Heroes   map[string]map[string]string `json:"heroes,omitempty"`
}

func (s *cosmeticsState) file() cosmeticsJSON {
	f := cosmeticsJSON{Heroes: map[string]map[string]string{}}
	if h := heroByGUID(s.Featured); h != nil {
		f.Featured = h.name
	}
	for guid, l := range s.Heroes {
		h := heroByGUID(guid)
		if h == nil {
			continue
		}
		for cls, i := range equipSlot {
			if l.Equipped[i] != 0 {
				if f.Heroes[h.name] == nil {
					f.Heroes[h.name] = map[string]string{}
				}
				f.Heroes[h.name][classNames[cls]] = fmt.Sprintf("%#016x", l.Equipped[i])
			}
		}
	}
	return f
}

// state converts the file form; unknown heroes, classes and unlocks are dropped.
func (f cosmeticsJSON) state() cosmeticsState {
	s := cosmeticsState{Heroes: map[uint64]*heroLoadout{}}
	if h, err := findHero(f.Featured); err == nil {
		s.Featured = h.guid
	}
	for name, slots := range f.Heroes {
		h, err := findHero(name)
		if err != nil {
			continue
		}
		l := &heroLoadout{}
		for cls, i := range equipSlot {
			if u, err := findUnlock(h, slots[classNames[cls]]); err == nil && unlockInfos[u].class == cls {
				l.Equipped[i] = u
			}
		}
		s.Heroes[h.guid] = l
	}
	return s
}

var (
	cosmeticsMu     sync.Mutex
	cosmetics       cosmeticsState
	cosmeticsLoaded bool
)

func cosmeticsFile() string {
	if p := os.Getenv("LOGIC_COSMETICS"); p != "" {
		return p
	}
	return "lobby-cosmetics.json"
}

// withCosmetics runs f on the state (loaded from the file on first use).
func withCosmetics(f func(s *cosmeticsState)) {
	cosmeticsMu.Lock()
	defer cosmeticsMu.Unlock()
	if !cosmeticsLoaded {
		cosmeticsLoaded = true
		cosmetics = cosmeticsState{Heroes: map[uint64]*heroLoadout{}}
		if b, err := os.ReadFile(cosmeticsFile()); err == nil {
			var f cosmeticsJSON
			if json.Unmarshal(b, &f) == nil {
				cosmetics = f.state()
			}
		}
	}
	f(&cosmetics)
}

func saveCosmetics() error {
	var b []byte
	withCosmetics(func(s *cosmeticsState) { b, _ = json.MarshalIndent(s.file(), "", "  ") })
	return os.WriteFile(cosmeticsFile(), append(b, '\n'), 0o644)
}

func loadoutOf(hero uint64) heroLoadout {
	var l heroLoadout
	withCosmetics(func(s *cosmeticsState) {
		if p := s.Heroes[hero]; p != nil {
			l = *p
		}
	})
	return l
}

func featuredHero() uint64 {
	var h uint64
	withCosmetics(func(s *cosmeticsState) { h = s.Featured })
	return h
}

func heroByGUID(guid uint64) *heroInfo {
	for i := range heroInfos {
		if heroInfos[i].guid == guid {
			return &heroInfos[i]
		}
	}
	return nil
}

// foldName lower-cases and drops accents and non-letters: "Lúcio" -> "lucio",
// "Soldier: 76" -> "soldier76".
func foldName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch r {
		case 'ú':
			r = 'u'
		case 'ö':
			r = 'o'
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// findHero resolves a hero by name (any case/accents, a unique prefix is enough) or GUID.
func findHero(s string) (*heroInfo, error) {
	want := foldName(s)
	var prefix []*heroInfo
	for i := range heroInfos {
		h := &heroInfos[i]
		n := foldName(h.name)
		if n == want {
			return h, nil
		}
		if want != "" && strings.HasPrefix(n, want) {
			prefix = append(prefix, h)
		}
	}
	if len(prefix) == 1 {
		return prefix[0], nil
	}
	if v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(s), "0x"), 16, 64); err == nil {
		if v>>48 == 0 {
			v |= 0x02E0000000000000
		}
		if h := heroByGUID(v); h != nil {
			return h, nil
		}
	}
	return nil, fmt.Errorf("unknown hero %q (!hero list)", s)
}

// findUnlock resolves one of the hero's unlocks by GUID (full or short hex) or by name,
// optionally "class:name" (theme/intro/spray), e.g. "spray:icon".
func findUnlock(h *heroInfo, s string) (uint64, error) {
	cls, name, typed := strings.Cut(s, ":")
	if !typed {
		name, cls = s, ""
	}
	var hits []uint64
	for _, u := range h.unlocks {
		info := unlockInfos[u]
		if cls != "" && classNames[info.class] != strings.ToLower(cls) {
			continue
		}
		if foldName(info.name) == foldName(name) {
			hits = append(hits, u)
		}
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	if len(hits) > 1 {
		return 0, fmt.Errorf("%q is ambiguous for %s: prefix the class (theme:, intro:, spray:) or use the GUID", s, h.name)
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(name), "0x"), 16, 64)
	if err != nil {
		return 0, fmt.Errorf("%s has no unlock %q (!unlocks %s)", h.name, s, foldName(h.name))
	}
	if v>>48 == 0 {
		v |= 0x0250000000000000
	}
	for _, u := range h.unlocks {
		if u == v {
			return v, nil
		}
	}
	return 0, fmt.Errorf("%#016x is not one of %s's unlocks (!unlocks %s)", v, h.name, foldName(h.name))
}

// themeForSlot finds the hero's SkinTheme unlock that selects variant slot n.
func themeForSlot(h *heroInfo, n uint32) (uint64, bool) {
	for _, u := range h.unlocks {
		if info := unlockInfos[u]; info.class == classSkinTheme && info.slot == n {
			return u, true
		}
	}
	return 0, false
}

// equipMessage encodes 24903 {u64 hero, u64 ctx, u64 unlock}.
func equipMessage(hero, ctx, unlock uint64) []byte {
	b := binary.LittleEndian.AppendUint64(nil, hero)
	b = binary.LittleEndian.AppendUint64(b, ctx)
	return binary.LittleEndian.AppendUint64(b, unlock)
}

// skinMessage encodes 24902 {u64 hero, u64 skin}.
func skinMessage(hero, skin uint64) []byte {
	return binary.LittleEndian.AppendUint64(binary.LittleEndian.AppendUint64(nil, hero), skin)
}

// equip stores the unlock in the hero's loadout and sends 24903. A SkinTheme goes
// with ctx = the hero's skin (the +0x68 entry the renderer reads) and ctx 0 (+0x28).
func (c *Ctx) equip(h *heroInfo, unlock uint64) error {
	info := unlockInfos[unlock]
	i, ok := equipSlot[info.class]
	if !ok {
		return fmt.Errorf("%s unlocks aren't equipped per hero", classNames[info.class])
	}
	withCosmetics(func(s *cosmeticsState) {
		if s.Heroes[h.guid] == nil {
			s.Heroes[h.guid] = &heroLoadout{}
		}
		s.Heroes[h.guid].Equipped[i] = unlock
	})
	if err := saveCosmetics(); err != nil {
		return err
	}
	if info.class == classSkinTheme {
		if err := c.Send("ClientInLobbyHeroProgression", 24903, equipMessage(h.guid, h.skin, unlock)); err != nil {
			return err
		}
	}
	if err := c.Send("ClientInLobbyHeroProgression", 24903, equipMessage(h.guid, 0, unlock)); err != nil {
		return err
	}
	if info.class == classSkinTheme {
		return c.respawnHero(h)
	}
	return nil
}

// respawnHero makes the title scene rebuild the hero with its current theme. The
// CosmeticEntity state reads the skin variant only when it is built; graph 0x058…B4A
// gates it on IsLoaded(EquippedSkin(F)), so 24902 {hero, 0} removes the hero and
// 24902 {hero, skin} builds it again (confirmed in game: Lúcio went away and came back
// orange). The pause lets the graph see the empty skin for a few frames (0.5 s; shorter untested).
func (c *Ctx) respawnHero(h *heroInfo) error {
	if err := c.Send("ClientInLobbyHeroProgression", 24902, skinMessage(h.guid, 0)); err != nil {
		return err
	}
	time.Sleep(respawnPause)
	return c.Send("ClientInLobbyHeroProgression", 24902, skinMessage(h.guid, h.skin))
}

var respawnPause = 500 * time.Millisecond

func init() {
	commands["hero"] = command{"!hero [name|random|list] - featured title hero (the client keeps its pick until it restarts: takes effect at the next launch)", func(c *Ctx, a []string) error {
		if len(a) == 0 {
			if h := heroByGUID(featuredHero()); h != nil {
				return c.SystemChat("featured hero: " + h.name + " (from the next client launch)")
			}
			return c.SystemChat("featured hero: random (client's pick)")
		}
		arg := strings.Join(a, " ")
		var guid uint64
		switch strings.ToLower(arg) {
		case "list":
			var names []string
			for _, h := range heroInfos {
				names = append(names, h.name)
			}
			return c.SystemChat("heroes: " + strings.Join(names, ", "))
		case "random":
		default:
			h, err := findHero(arg)
			if err != nil {
				return err
			}
			guid = h.guid
		}
		withCosmetics(func(s *cosmeticsState) { s.Featured = guid })
		if err := saveCosmetics(); err != nil {
			return err
		}
		if err := sendHeroProgression(c, 1); err != nil {
			return err
		}
		return c.SystemChat("featured hero set; the title screen keeps its current hero until the client restarts")
	}}
	commands["skin"] = command{"!skin <hero> <1|2|variant name> - skin variant (SkinTheme) of a hero, e.g. !skin mei pink", func(c *Ctx, a []string) error {
		if len(a) < 1 {
			return fmt.Errorf("usage: !skin <hero> <1|2|variant>")
		}
		h, err := findHero(a[0])
		if err != nil {
			return err
		}
		if len(a) < 2 {
			var have []string
			for n := uint32(1); n <= 4; n++ {
				if _, ok := themeForSlot(h, n); ok {
					have = append(have, fmt.Sprintf("%d %s", n, h.variants[n-1]))
				}
			}
			return c.SystemChat(fmt.Sprintf("%s variants: %s (unlockable: %s)", h.name, strings.Join(h.variants[:], ", "), strings.Join(have, ", ")))
		}
		var n uint32
		if v, err := strconv.ParseUint(a[1], 10, 32); err == nil {
			n = uint32(v)
		} else {
			for i, v := range h.variants {
				if foldName(v) == foldName(a[1]) {
					n = uint32(i + 1)
				}
			}
		}
		theme, ok := themeForSlot(h, n)
		if !ok {
			return fmt.Errorf("%s has no unlock for variant %q (0.8 ships unlocks for slots 1 and 2 only)", h.name, a[1])
		}
		if err := c.equip(h, theme); err != nil {
			return err
		}
		return c.SystemChat(fmt.Sprintf("%s: variant %d %s (%#016x)", h.name, n, h.variants[n-1], theme))
	}}
	commands["equip"] = command{"!equip <hero> <unlock> - equip an intro / spray / theme by name or GUID (class:name if ambiguous)", func(c *Ctx, a []string) error {
		if len(a) < 2 {
			return fmt.Errorf("usage: !equip <hero> <unlock>")
		}
		h, err := findHero(a[0])
		if err != nil {
			return err
		}
		u, err := findUnlock(h, strings.Join(a[1:], " "))
		if err != nil {
			return err
		}
		if err := c.equip(h, u); err != nil {
			return err
		}
		return c.SystemChat(fmt.Sprintf("%s: equipped %s %q (%#016x)", h.name, classNames[unlockInfos[u].class], unlockInfos[u].name, u))
	}}
	commands["unlocks"] = command{"!unlocks <hero> - list a hero's unlocks and what is equipped", func(c *Ctx, a []string) error {
		if len(a) < 1 {
			return fmt.Errorf("usage: !unlocks <hero>")
		}
		h, err := findHero(strings.Join(a, " "))
		if err != nil {
			return err
		}
		l := loadoutOf(h.guid)
		byClass := map[unlockClass][]string{}
		for _, u := range h.unlocks {
			info := unlockInfos[u]
			s := fmt.Sprintf("%x %s", u&0xFFFF, info.name)
			if i, ok := equipSlot[info.class]; ok && l.Equipped[i] == u {
				s += "*"
			}
			byClass[info.class] = append(byClass[info.class], s)
		}
		for _, cls := range []unlockClass{classSkinTheme, classPOTG, classSpray, classAvatarPortrait, classPortraitFrame} {
			if len(byClass[cls]) > 0 {
				if err := c.SystemChat(fmt.Sprintf("%s %s: %s", h.name, classNames[cls], strings.Join(byClass[cls], ", "))); err != nil {
					return err
				}
			}
		}
		return nil
	}}
}
