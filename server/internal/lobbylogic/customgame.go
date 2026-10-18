package lobbylogic

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"owserver/internal/maps"
)

// Custom games (ClientOutLobbyCustomGame 24000-24019 / ClientInLobbyCustomGame 23300-23312).
// Flow and message layouts: docs/LobbyServer.md "Practice vs AI and Private Game".
//
// The title screen's Custom, Dev custom, Practice vs AI and Tutorial buttons all send
// 24001 = {i8 type, cstring, cstring} (type 1 custom, 2 dev custom, 3 practice vs AI,
// 4 tutorial; both strings empty); their Cancel sends 24007 (empty).
//
// The replies are dispatched by FUN_7ff650c48c20 onto the custom-game object
// (FUN_7ff650c29670, "cg" below):
//
//	23300  []u64, []u64  -> cg +0x40 / +0x58: the maps the MAP picker offers for types
//	                        1-2 / type 3 (FUN_7ff650d8d9e0; 0x08...D4 is skipped)
//	23303  game info -> +0x70 (type +0x90, game +0x98), settings -> +0xB8 (server copy)
//	       and +0x210 (the copy the UI edits), clears +0x208: "you are in this game".
//	       Type 3 opens the practice setup page (graph 0134 #12), types 1/2 the private
//	       game lobby (page 10, graph 08CA) - only with a non-null game id.
//	23304  lobby state -> +0x180; its ID (+0x1A8) is what Start sends back
//	23305  settings -> +0xB8 and +0x210
//	23308  (u64[2])  -> resets the whole object (FUN_7ff650cb42c0): leave / request over
//
// Start (practice page: action 0x4f961c19 then 0x2515c992; page 10: 0x2515c992) edits
// the +0x210 settings (map, bots), sends them if they differ from +0xB8
// (FUN_7ff650beee60) as 24008, then asks to enter the game with 24013 {u8 1, 23304 ID}
// (FUN_7ff650bef430).
// We answer by sending the client to the game server like !instance does.

const (
	customGameCustom   = 1
	customGameDev      = 2
	customGamePractice = 3
	customGameTutorial = 4
)

// Maps offered by the custom and practice MAP pickers (23300): every playable map of the
// catalogue (docs/Maps.md §5), Anubis first, without King's Row: the picker
// (FUN_7ff650d8d9e0) hides 0x08...D4, and with it in the list the row picked below it
// came back as King's Row in 24008 (2026-09-26: Hanamura picked, King's Row loaded).
// !instance still loads King's Row.
var (
	customGameMaps   = pickerMaps()
	practiceGameMaps = pickerMaps()
)

const kingsRow = 0x08000000000000D4

func pickerMaps() []uint64 {
	var out []uint64
	for _, id := range maps.PlayableData() {
		if id != kingsRow {
			out = append(out, id)
		}
	}
	return out
}

// customGame is the custom game a client is in (one per lobby session).
type customGame struct {
	kind byte
	id   [2]uint64 // game id (gameinfo +0x28, lobby state +0x28)
	set  cgSettings
}

var (
	customGamesMu sync.Mutex
	customGames   = map[uint64]*customGame{}
	nextGameID    = nextIDSeed // unique across logic restarts, like the fake player IDs
)

func (c *Ctx) customGame() *customGame {
	customGamesMu.Lock()
	defer customGamesMu.Unlock()
	return customGames[c.Session.ID]
}

func (c *Ctx) setCustomGame(g *customGame) {
	customGamesMu.Lock()
	defer customGamesMu.Unlock()
	if g == nil {
		delete(customGames, c.Session.ID)
	} else {
		customGames[c.Session.ID] = g
	}
}

// cgBot is one AI player of the settings' bot list (FUN_7ff650beed50 appends them).
type cgBot struct {
	difficulty byte
	n          int32 // always 1 from the client
	hero       uint64
}

// cgSettings is the settings struct of 23303 / 23305 / 24008 (0xC8 bytes in memory).
// Only the fields whose use we know have names; the rest round-trips.
//
//	+0x00 {{bool start, bool}, {{bool, bool}, {bool, bool}}}  start: FUN_7ff650beebf0
//	+0x08 {i32}
//	+0x0C {bool, i32, i32}
//	+0x18 {i32, i32 botCount, i32, i32, i32}                   FUN_7ff650beeb40
//	+0x30 u64[2]
//	+0x40 {i32, i32, u64[]}
//	+0x60 {[]{u8 difficulty, i32, u64 hero}}                   bots, FUN_7ff650beed50
//	+0x78 {i32 mapMode, i32, i32, i32, u64[] maps}             FUN_7ff650bedf90 (mode 1)
//	+0xA0 {4 bools, i32, []ID}
type cgSettings struct {
	bools byte // 6 bools, bit 0 = start
	i08   int32
	b0C   bool
	i0C   [2]int32
	i18   [5]int32 // [1] = bot count
	u30   [2]uint64
	i40   [2]int32
	u40   []uint64
	bots  []cgBot
	i78   [4]int32 // [0] = map mode (1 once a map is set)
	maps  []uint64
	bA0   byte // 4 bools
	iA0   int32
	ids   [][2]uint64
}

func appendBool(b []byte, v bool) []byte {
	if v {
		return append(b, 1)
	}
	return append(b, 0)
}

func appendU64s(b []byte, v []uint64) []byte {
	b = binary.LittleEndian.AppendUint32(b, uint32(len(v)))
	for _, x := range v {
		b = binary.LittleEndian.AppendUint64(b, x)
	}
	return b
}

func (s *cgSettings) encode() []byte {
	le := binary.LittleEndian
	b := []byte{s.bools}
	b = le.AppendUint32(b, uint32(s.i08))
	b = appendBool(b, s.b0C)
	for _, v := range s.i0C {
		b = le.AppendUint32(b, uint32(v))
	}
	for _, v := range s.i18 {
		b = le.AppendUint32(b, uint32(v))
	}
	b = le.AppendUint64(b, s.u30[0])
	b = le.AppendUint64(b, s.u30[1])
	b = le.AppendUint32(b, uint32(s.i40[0]))
	b = le.AppendUint32(b, uint32(s.i40[1]))
	b = appendU64s(b, s.u40)
	b = le.AppendUint32(b, uint32(len(s.bots)))
	for _, bot := range s.bots {
		b = append(b, bot.difficulty)
		b = le.AppendUint32(b, uint32(bot.n))
		b = le.AppendUint64(b, bot.hero)
	}
	for _, v := range s.i78 {
		b = le.AppendUint32(b, uint32(v))
	}
	b = appendU64s(b, s.maps)
	b = append(b, s.bA0)
	b = le.AppendUint32(b, uint32(s.iA0))
	b = le.AppendUint32(b, uint32(len(s.ids)))
	for _, id := range s.ids {
		b = le.AppendUint64(b, id[0])
		b = le.AppendUint64(b, id[1])
	}
	return b
}

// reader decodes little-endian fields and remembers the first error.
type reader struct {
	b   []byte
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return make([]byte, n)
	}
	if len(r.b) < n {
		r.err = errors.New("message too short")
		return make([]byte, n)
	}
	v := r.b[:n]
	r.b = r.b[n:]
	return v
}
func (r *reader) u8() byte    { return r.take(1)[0] }
func (r *reader) i32() int32  { return int32(binary.LittleEndian.Uint32(r.take(4))) }
func (r *reader) u64() uint64 { return binary.LittleEndian.Uint64(r.take(8)) }

// count reads an array count, refusing counts the rest of the message can't hold.
func (r *reader) count(elem int) int {
	n := int(uint32(r.i32()))
	if r.err == nil && n*elem > len(r.b) {
		r.err = fmt.Errorf("array of %d x %d bytes past the end", n, elem)
		return 0
	}
	return n
}

func (r *reader) u64s() []uint64 {
	n := r.count(8)
	v := make([]uint64, n)
	for i := range v {
		v[i] = r.u64()
	}
	return v
}

// decodeSettings reads a settings struct (24008 / 23305 layout).
func decodeSettings(r *reader) cgSettings {
	var s cgSettings
	s.bools = r.u8()
	s.i08 = r.i32()
	s.b0C = r.u8() != 0
	s.i0C = [2]int32{r.i32(), r.i32()}
	for i := range s.i18 {
		s.i18[i] = r.i32()
	}
	s.u30 = [2]uint64{r.u64(), r.u64()}
	s.i40 = [2]int32{r.i32(), r.i32()}
	s.u40 = r.u64s()
	s.bots = make([]cgBot, r.count(13))
	for i := range s.bots {
		s.bots[i] = cgBot{r.u8(), r.i32(), r.u64()}
	}
	for i := range s.i78 {
		s.i78[i] = r.i32()
	}
	s.maps = r.u64s()
	s.bA0 = r.u8()
	s.iA0 = r.i32()
	s.ids = make([][2]uint64, r.count(16))
	for i := range s.ids {
		s.ids[i] = [2]uint64{r.u64(), r.u64()}
	}
	return s
}

// defaultSettings: the first map of the type's list selected.
func defaultSettings(kind byte) cgSettings {
	maps := customGameMaps
	if kind == customGamePractice {
		maps = practiceGameMaps
	}
	return cgSettings{i78: [4]int32{1}, maps: []uint64{maps[0]}}
}

// customGameInfo encodes the game-info struct {cstring name, cstring, u8 type,
// u64[2] game, ID owner} (cg +0x70; FUN_7ff650f9bf90 copies it). u64[2] is a fixed
// array (no count on the wire). A null game id makes the client's type checks
// (FUN_7ff650d747b0) false, so no page opens.
func customGameInfo(name string, kind byte, game [2]uint64, owner []byte) []byte {
	b := appendCString(nil, name)
	b = appendCString(b, "")
	b = append(b, kind)
	b = binary.LittleEndian.AppendUint64(b, game[0])
	b = binary.LittleEndian.AppendUint64(b, game[1])
	var o [16]byte
	copy(o[:], owner)
	return append(b, o[:]...)
}

// lobbyState encodes 23304 (cg +0x180, copied by FUN_7ff650f9c550):
//
//	{{bool, bool}, {bool, bool}}  one byte
//	u8
//	{5 x i32}
//	u64
//	ID game                        +0x28: sent back by Start (FUN_7ff650bef430); while
//	                               it is null Start shows notice 0x1F "Not in a private game."
//	{i32, i32, u64[]}
//	{[]{u8, i32, u64}}
//	[]{cstring name, bool, u8 kind, u8, i32 team, u64 bot, ID player}   the slots
//
// Slot entries (0x38 bytes; page 10 filler FUN_7ff650d935d0): bool +0x10 picks the
// entry's status image (0x0AC...90D if set, else ...90E); kind +0x11 = 1 puts a human
// in a team column, other kinds go to the spectator column; team +0x18 = 0 / 1 (2 =
// spectator); bot +0x20 != 0 marks an AI player; ID +0x28 is matched against the
// party (the INVITE dialog, FUN_7ff650d8ee30, lists party members not in a slot).
func lobbyState(game [2]uint64, name string, player []byte) []byte {
	le := binary.LittleEndian
	b := []byte{0, 0}
	b = append(b, make([]byte, 20)...)
	b = le.AppendUint64(b, 0)
	b = le.AppendUint64(b, game[0])
	b = le.AppendUint64(b, game[1])
	b = append(b, make([]byte, 12)...) // i32, i32, u64[] count
	b = le.AppendUint32(b, 0)          // [] count
	b = le.AppendUint32(b, 1)          // players
	b = appendCString(b, name)
	b = append(b, 0, 1, 0)    // bool, kind 1 = player, u8
	b = le.AppendUint32(b, 0) // team 0
	b = le.AppendUint64(b, 0) // human
	var p [16]byte
	copy(p[:], player)
	return append(b, p[:]...)
}

// mapLists encodes 23300: the MAP picker's options for custom and practice games.
func mapLists() []byte {
	return appendU64s(appendU64s(nil, customGameMaps), practiceGameMaps)
}

func onCustomGameRequest(c *Ctx) error {
	if len(c.Payload) < 1 {
		return fmt.Errorf("24001 too short: %x", c.Payload)
	}
	switch kind := c.Payload[0]; kind {
	case customGameCustom, customGameDev, customGamePractice:
		return c.createCustomGame(kind)
	case customGameTutorial:
		return c.startTutorial()
	default:
		return fmt.Errorf("unknown custom game type %d", kind)
	}
}

// createCustomGame puts the client in a new custom game of the given type, which
// opens the practice setup page (type 3) or the private game lobby (types 1/2).
func (c *Ctx) createCustomGame(kind byte) error {
	customGamesMu.Lock()
	nextGameID++
	g := &customGame{kind: kind, id: [2]uint64{7, nextGameID}, set: defaultSettings(kind)}
	customGamesMu.Unlock()
	c.setCustomGame(g)

	name := "Private Game"
	if kind == customGamePractice {
		name = "Practice vs AI"
	}
	if err := c.Send("ClientInLobbyCustomGame", 23300, mapLists()); err != nil {
		return err
	}
	info := customGameInfo(name, kind, g.id, c.ClientJamID())
	if err := c.Send("ClientInLobbyCustomGame", 23303, append(info, g.set.encode()...)); err != nil {
		return err
	}
	c.Log.Info("custom game created", "type", kind, "game", fmt.Sprintf("%x", g.id))
	return c.Send("ClientInLobbyCustomGame", 23304, lobbyState(g.id, playerName(), c.ClientJamID()))
}

// startTutorial answers TUTORIAL (24001 type 4). The main menu (graph 0134) sets
// IsJoiningTutorial ($1a9d, #33) and clears it once the custom game's type is 4
// (#36 0x1a163481(4) reads cg +0x90 from 23303); no page opens for type 4 and nothing
// sends a Start, so the server puts the client in the tutorial game (23303 + 23304) and
// sends it to the game server on the tutorial map at once.
func (c *Ctx) startTutorial() error {
	customGamesMu.Lock()
	nextGameID++
	g := &customGame{kind: customGameTutorial, id: [2]uint64{7, nextGameID},
		set: cgSettings{i78: [4]int32{1}, maps: []uint64{maps.Tutorial}}}
	customGamesMu.Unlock()
	c.setCustomGame(g)
	info := customGameInfo("Tutorial", customGameTutorial, g.id, c.ClientJamID())
	if err := c.Send("ClientInLobbyCustomGame", 23303, append(info, g.set.encode()...)); err != nil {
		return err
	}
	if err := c.Send("ClientInLobbyCustomGame", 23304, lobbyState(g.id, playerName(), c.ClientJamID())); err != nil {
		return err
	}
	conn, addr, err := c.sendToGame(maps.Tutorial)
	if err != nil {
		if nerr := c.Notice(NoticeUnableToJoinPractice); nerr != nil {
			return nerr
		}
		return c.SystemChat("Tutorial: " + err.Error())
	}
	c.Log.Info("tutorial started", "map", mapLabel(maps.Tutorial), "addr", addr, "conn", conn.ConnID, "game", fmt.Sprintf("%x", g.id))
	return nil
}

// onCustomGameSettings handles 24008 (settings; sent by Start when the player changed
// them): keep them and confirm with 23305, so the client's two copies agree.
func onCustomGameSettings(c *Ctx) error {
	r := &reader{b: c.Payload}
	s := decodeSettings(r)
	if r.err != nil {
		return fmt.Errorf("24008: %w (%x)", r.err, c.Payload)
	}
	c.Log.Info("custom game settings", "maps", mapLabels(s.maps), "bots", len(s.bots), "start", s.bools&1)
	g := c.customGame()
	if g == nil {
		return c.SystemChat("Not in a custom game.")
	}
	customGamesMu.Lock()
	g.set = s
	customGamesMu.Unlock()
	return c.Send("ClientInLobbyCustomGame", 23305, s.encode())
}

// onCustomGameStart handles 24013 {u8, ID game} (Start: FUN_7ff650bef430 sends u8 1
// and the 23304 game id, right after 24008): send the client to the game server on
// the chosen map. Without server state (lobbylogic restarted) the default map is used.
func onCustomGameStart(c *Ctx) error {
	mapID := uint64(DefaultMap)
	if g := c.customGame(); g != nil {
		customGamesMu.Lock()
		if len(g.set.maps) > 0 && g.set.maps[0] != 0 {
			mapID = g.set.maps[0]
		}
		customGamesMu.Unlock()
	}
	conn, addr, err := c.sendToGame(mapID)
	if err != nil {
		if nerr := c.Notice(NoticePrivateGameServerDown); nerr != nil {
			return nerr
		}
		return c.SystemChat("Custom game: " + err.Error())
	}
	c.Log.Info("custom game started", "map", mapLabel(mapID), "addr", addr, "conn", conn.ConnID)
	return nil
}

// onCustomGameCancel answers 24007 by resetting the client's custom-game state.
func onCustomGameCancel(c *Ctx) error {
	return c.leaveCustomGame()
}

// leaveCustomGame drops the client's custom game (23308 resets the client's object).
func (c *Ctx) leaveCustomGame() error {
	g := c.customGame()
	c.setCustomGame(nil)
	var id [2]uint64
	if g != nil {
		id = g.id
	}
	b := binary.LittleEndian.AppendUint64(nil, id[0])
	return c.Send("ClientInLobbyCustomGame", 23308, binary.LittleEndian.AppendUint64(b, id[1]))
}

// mapLabel names a map GUID for logs and chat ("TEMPLE OF ANUBIS (0x80000000000005b)").
func mapLabel(id uint64) string {
	if m, ok := maps.ByData(id); ok {
		return m.Label()
	}
	return fmt.Sprintf("unknown map %#016x", id)
}

func mapLabels(ids []uint64) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = mapLabel(id)
	}
	return out
}
