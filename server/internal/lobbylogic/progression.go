package lobbylogic

import (
	"encoding/binary"
	"fmt"
)

// Hero progression (ClientInLobbyHeroProgression 24900-24908; dispatch FUN_7ff650c49340).
//
// 24900 = {i32 cap, [] hero record, [] {u64 hero, [] unlock}}. The handler stores cap
// at profile+0xF0 and the lists in two maps (+0xF8 records, +0x120 unlocks) keyed by
// hero ID. The title screen features one hero (profile+0x230, chosen by the client via
// FUN_7ff650cc93b0 when unset) and FUN_7ff650d87210 shows its name and progression on
// LobbyMainMenuVM: level (record u32 @+4) -> 0xD18C2192, level+1 -> 0xBA6FA775,
// record u64 @+16 -> 0x4ACDC2D3, u64 @+24 -> 0x9CA2621, level >= cap -> 0xAD2AB0C7.
// The hero must be in both maps. Confirmed: the title screen then shows "MEI".
//
// Hero record: {i8, u32 level, u64 hero, u64 @+16, u64 @+24, u64 skin @+32, 8 x u64,
// []{u64 skin, u64}, []unlock}.
//
// The level indicator below the cap shows the NEXT REWARD: FUN_7ff650d870c0 picks the
// first entry {bool, i32 level, u64 asset, u64} of the hero's unlock list (second list)
// with level >= next level that the hero record doesn't already own, and the panel
// shows that asset's name/icon. With no rewards the indicator hides; at the cap it
// shows "max level".

// playerIcons are the 23 loaded player-icon unlocks (class 0x8CDAA871), used as rewards.
var playerIcons = []uint64{
	0x02500000000002F7, 0x02500000000002F9, 0x0250000000000304, 0x0250000000000305,
	0x0250000000000306, 0x0250000000000307, 0x0250000000000308, 0x0250000000000309,
	0x025000000000030A, 0x025000000000030B, 0x025000000000030C, 0x025000000000030D,
	0x025000000000030E, 0x025000000000030F, 0x0250000000000310, 0x0250000000000311,
	0x0250000000000312, 0x0250000000000313, 0x0250000000000314, 0x0250000000000315,
	0x0250000000000316, 0x0250000000000317, 0x0250000000000318,
}

// heroes are the 21 hero definitions (0x02E...) loaded in 0.8.
var heroes = []uint64{
	0x02E0000000000002, 0x02E0000000000003, 0x02E0000000000004, 0x02E0000000000005,
	0x02E0000000000006, 0x02E0000000000007, 0x02E0000000000008, 0x02E0000000000009,
	0x02E000000000000A, 0x02E0000000000015, 0x02E0000000000016, 0x02E0000000000020,
	0x02E0000000000029, 0x02E0000000000040, 0x02E0000000000042, 0x02E0000000000065,
	0x02E0000000000068, 0x02E000000000006E, 0x02E0000000000079, 0x02E000000000007A,
	0x02E00000000000DD,
}

// The hero record's first "9 x u64" field (record +0x20) is the equipped skin
// (heroInfo.skin, STUHero.m_skins; every 0.8 hero has exactly one): 24902 {hero, u64}
// overwrites it, ConfigVar 0xae8e9502 reads it, and the title map's graph
// 0x0580000000000B4A only spawns the featured hero (CosmeticEntity) when that skin is a
// loaded resource. With 0 the title screen shows no hero. The other 8 fields, the
// +0x68 theme list and the owned unlocks come from the hero's loadout (cosmetics.go).

const heroLevelCap = 25

// heroRecord is one 24900 hero record (wire = in-memory layout from +0, packed).
type heroRecord struct {
	level    uint32
	hero     uint64
	xp       uint64 // +0x10
	xpNext   uint64 // +0x18
	skin     uint64 // +0x20
	equipped [8]uint64
	themes   [][2]uint64 // +0x68 {ctx = skin, SkinTheme unlock}
	owned    []uint64    // +0x80
}

// heroRecords builds every hero's record. With a featured hero set, only its record
// has xp != 0 and a level above 1, which makes it the client's only candidate when it
// picks the featured hero (FUN_7ff650cc93b0, cosmetics.go).
func heroRecords(level uint32, xp, xpNext uint64) []heroRecord {
	featured := featuredHero()
	recs := make([]heroRecord, 0, len(heroInfos))
	for _, h := range heroInfos {
		r := heroRecord{level: level, hero: h.guid, xp: xp, xpNext: xpNext, skin: h.skin, owned: h.unlocks}
		if featured != 0 && h.guid != featured {
			r.level, r.xp = 1, 0
		}
		r.equipped = loadoutOf(h.guid).Equipped
		if t := r.equipped[equipSlot[classSkinTheme]]; t != 0 {
			r.themes = [][2]uint64{{h.skin, t}}
		}
		recs = append(recs, r)
	}
	return recs
}

func appendUnlock(b []byte, level int32, asset uint64) []byte {
	b = append(b, 0)
	b = binary.LittleEndian.AppendUint32(b, uint32(level))
	b = binary.LittleEndian.AppendUint64(b, asset)
	return binary.LittleEndian.AppendUint64(b, 0)
}

// heroProgression encodes 24900 {i32 cap, []record, []{u64 hero, []unlock rewards}}.
func heroProgression(recs []heroRecord) []byte {
	b := binary.LittleEndian.AppendUint32(nil, heroLevelCap)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(recs)))
	for _, r := range recs {
		b = append(b, 0)
		b = binary.LittleEndian.AppendUint32(b, r.level)
		b = binary.LittleEndian.AppendUint64(b, r.hero)
		b = binary.LittleEndian.AppendUint64(b, r.xp)
		b = binary.LittleEndian.AppendUint64(b, r.xpNext)
		b = binary.LittleEndian.AppendUint64(b, r.skin)
		for _, u := range r.equipped {
			b = binary.LittleEndian.AppendUint64(b, u)
		}
		b = binary.LittleEndian.AppendUint32(b, uint32(len(r.themes)))
		for _, t := range r.themes {
			b = binary.LittleEndian.AppendUint64(b, t[0])
			b = binary.LittleEndian.AppendUint64(b, t[1])
		}
		b = binary.LittleEndian.AppendUint32(b, uint32(len(r.owned)))
		for _, u := range r.owned {
			b = appendUnlock(b, 0, u)
		}
	}
	b = binary.LittleEndian.AppendUint32(b, uint32(len(recs)))
	for _, r := range recs {
		b = binary.LittleEndian.AppendUint64(b, r.hero)
		// One reward per level 2..cap: {bool, i32 level, u64 asset, u64}.
		b = binary.LittleEndian.AppendUint32(b, heroLevelCap-1)
		for lvl := uint32(2); lvl <= heroLevelCap; lvl++ {
			b = appendUnlock(b, int32(lvl), playerIcons[int(lvl)%len(playerIcons)])
		}
	}
	return b
}

func sendHeroProgression(c *Ctx, level uint32) error {
	return c.Send("ClientInLobbyHeroProgression", 24900, heroProgression(heroRecords(level, 500, 1000)))
}

func init() {
	commands["level"] = command{"!level <n> - set every hero's level (only the featured hero's with !hero set)", func(c *Ctx, a []string) error {
		var level uint32 = 1
		if len(a) > 0 {
			fmt.Sscan(a[0], &level)
		}
		return sendHeroProgression(c, level)
	}}
}
