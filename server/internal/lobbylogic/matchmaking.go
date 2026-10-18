package lobbylogic

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"owserver/internal/gamelink"
	"owserver/internal/maps"
)

// Matchmaking (ClientOutLobbyMatchmaking 22000-22011 / ClientInLobbyMatchmaking 25600-25607).
//
// The client's matchmaking object ([DAT_7ff651813e18+0x1B8]) keeps a list of queue
// tickets at +0x18 (count at +0x20; "IsMatchmaking" = count != 0 or a request
// pending at +0x33). ClientInLobbyMatchmaking 25600 (handler FUN_7ff650c49a00 ->
// FUN_7ff650d33b90) adds or updates the ticket keyed by the message's ticket block;
// statuses 6-8 remove it. 0 -> 1 tickets fires game message 0x0240000000000096
// ("entered queue") and clears the pending flag; 1 -> 0 fires 0x0240000000000097.
// Confirmed: status 1 shows "Searching" at the top of the title screen.
// See docs/LobbyServer.md "Matchmaking".
//
// Every 25600 refreshes the matchmaking view model (FUN_7ff650c67930; VM +0x20 = the
// panel state, shown by screen 0x09A..18D of graph 02DA; filler FUN_7ff650d01dc0):
//
//	ticket status 0-4                  -> 1 "Searching" (+ the ticket's queue stats)
//	ticket status 5, no game info      -> 2 "Match Found", or 3 "Entering Match" when
//	                                      the refresh comes >= 1700 ms after the
//	                                      first status-5 refresh
//	ticket status 5, game info[0] +0x14 >= 0 -> 6 "Reinstancing"; +0x18 >= 0 -> 5 "Rebalancing"
//	(game info[0] byte +4 == 6 forces 1; any other status -> 0, no panel)
//
// The panel has no clock of its own: the server drives it by re-sending 25600.

const (
	mmStatusSearching = 1
	mmStatusFound     = 5 // "Match Found", then "Entering Match" on a refresh >= 1.7 s later
	mmStatusCancelled = 6 // 6, 7 and 8 all remove the ticket; which one means what is unknown
)

// Matchmaking timing (the queue is simulated: one player, bots "fill" the match).
var (
	mmSearchTime = 8 * time.Second // Searching until the match is found
	mmFoundTime  = 2 * time.Second // Match Found, then Entering Match
	mmEnterTime  = 2 * time.Second // Entering Match, then 20600 (game found)
	mmPlayers    = int32(12)
)

// mmTicket is the ticket key: {u32, u32, u64, {u64, u64}}. One fixed ticket per
// client is enough while there is a single queue.
var mmTicket = [4]uint64{1<<32 | 1, 1, 1, 1}

// queueStats is the first element of the ticket's stats array, shown by the Searching
// panel (MatchmakingVM, filler FUN_7ff650d01dc0) - confirmed in game:
//
//	i8  good    1 = "FINDING A GOOD MATCH" (0x0AC...724 MatchmakingSearchBucketTypeNewbie),
//	            else 0x0AC...725 (...Normal)
//	i32 found   "<found> OF <needed> PLAYERS FOUND"
//	i32 needed
//	i32 fill    /4 picks one of 7 hexagon images (0x0AC...715-71C
//	            MatchmakingSearchExpansionLevel0-6)
//	f32, f32    unknown (not read by the filler)
type queueStats struct {
	good          bool
	found, needed int32
	fill          int32
}

// queueStatus encodes ClientInLobbyMatchmaking 25600:
//
//	u32 status, i32
//	[u32 n] {i8, i32, i32, i32, f32, f32}   queue stats (see queueStats)
//	ticket {u32, u32, u64, {u64, u64}}
//	[u32 n] ticket, [u32 n] ticket
//	[u32 n] {3 bools, i8, i8, u32, 4 x i32, u32[], u32[], u32, u32, {u64, u64}}
func queueStatus(status uint32, stats *queueStats) []byte {
	b := binary.LittleEndian.AppendUint32(nil, status)
	b = binary.LittleEndian.AppendUint32(b, 0)
	if stats == nil {
		b = binary.LittleEndian.AppendUint32(b, 0)
	} else {
		b = binary.LittleEndian.AppendUint32(b, 1)
		if stats.good {
			b = append(b, 1)
		} else {
			b = append(b, 0)
		}
		b = binary.LittleEndian.AppendUint32(b, uint32(stats.found))
		b = binary.LittleEndian.AppendUint32(b, uint32(stats.needed))
		b = binary.LittleEndian.AppendUint32(b, uint32(stats.fill))
		b = append(b, make([]byte, 8)...) // f32, f32
	}
	for _, q := range mmTicket {
		b = binary.LittleEndian.AppendUint64(b, q)
	}
	for range 3 {
		b = binary.LittleEndian.AppendUint32(b, 0)
	}
	return b
}

// onMatchmakingStart answers the Play button (ClientOutLobbyMatchmaking 22000 =
// {{u64 seq, u64 src}, bool}, all zero so far): the client is put in queue and a
// match is "found" after mmSearchTime (see runQueue).
func onMatchmakingStart(c *Ctx) error {
	stopQueue(c.Session.ID)
	q := &queue{stop: make(chan struct{}), search: mmSearchTime, found: mmFoundTime, enter: mmEnterTime}
	queuesMu.Lock()
	queues[c.Session.ID] = q
	queuesMu.Unlock()
	if err := c.Send("ClientInLobbyMatchmaking", 25600, queueStatus(mmStatusSearching, searchStats(0, q.search))); err != nil {
		stopQueue(c.Session.ID)
		return err
	}
	c.Log.Info("matchmaking: searching", "search", q.search)
	go c.runQueue(q)
	return nil
}

// onMatchmakingCancel answers Cancel (ClientOutLobbyMatchmaking 22001, empty) by
// stopping the search and removing the ticket.
func onMatchmakingCancel(c *Ctx) error {
	if stopQueue(c.Session.ID) {
		c.Log.Info("matchmaking: cancelled")
	}
	return c.Send("ClientInLobbyMatchmaking", 25600, queueStatus(mmStatusCancelled, nil))
}

// queue is one client's running search.
type queue struct {
	stop                 chan struct{}
	once                 sync.Once
	search, found, enter time.Duration // mmSearchTime, mmFoundTime, mmEnterTime at the start
}

var (
	queuesMu sync.Mutex
	queues   = map[uint64]*queue{}
)

// stopQueue ends a session's search; false if it had none.
func stopQueue(session uint64) bool {
	queuesMu.Lock()
	q := queues[session]
	delete(queues, session)
	queuesMu.Unlock()
	if q == nil {
		return false
	}
	q.once.Do(func() { close(q.stop) })
	return true
}

// searchStats is the Searching panel after elapsed of a search that takes total: the
// player count fills up and the search expands (level 0 -> 6).
func searchStats(elapsed, total time.Duration) *queueStats {
	f := min(float64(elapsed)/float64(total), 1)
	return &queueStats{
		good:   true,
		found:  1 + int32(f*float64(mmPlayers-1)),
		needed: mmPlayers,
		fill:   int32(f * 6 * 4),
	}
}

// runQueue drives the panel: Searching (updated every second) -> Match Found ->
// Entering Match -> 20600 (the hand-off !instance and custom-game Start use), then
// removes the ticket. A cancel stops it at any step before the hand-off.
func (c *Ctx) runQueue(q *queue) {
	start := time.Now()
	wait := func(d time.Duration) bool {
		select {
		case <-q.stop:
			return false
		case <-time.After(d):
			return true
		}
	}
	send := func(status uint32, stats *queueStats) bool {
		if err := c.Send("ClientInLobbyMatchmaking", 25600, queueStatus(status, stats)); err != nil {
			c.Log.Warn("matchmaking: send failed", "err", err)
			stopQueue(c.Session.ID)
			return false
		}
		return true
	}
	for time.Since(start) < q.search {
		if !wait(min(time.Second, q.search-time.Since(start))) || !send(mmStatusSearching, searchStats(time.Since(start), q.search)) {
			return
		}
	}
	c.Log.Info("matchmaking: match found")
	// The second status 5 is the refresh that turns "Match Found" into "Entering Match".
	if !send(mmStatusFound, nil) || !wait(q.found) || !send(mmStatusFound, nil) || !wait(q.enter) {
		return
	}
	// The search is over: a Cancel from here on only removes the ticket.
	if !stopQueue(c.Session.ID) {
		return
	}
	conn, addr, err := c.sendToGame(DefaultMap)
	if err != nil {
		c.Log.Error("matchmaking: no game", "err", err)
		send(mmStatusCancelled, nil)
		if err := c.SystemChat("Matchmaking: " + err.Error()); err != nil {
			c.Log.Warn("matchmaking: chat failed", "err", err)
		}
		return
	}
	c.Log.Info("matchmaking: sent to the game server", "map", fmt.Sprintf("%#x", uint64(DefaultMap)), "addr", addr, "conn", conn.ConnID)
	send(mmStatusCancelled, nil)
}

// instanceJoin encodes ClientInLobbyInstance 20600, the "game found" message (handler
// FUN_7ff650c5d580: "You should connect to %s:%d"):
//
//	u64 map, cstring host, u16 port, u32 connID, i8[20] key (fixed, no count), ID {u64, u64}
//
// The map (STUMapHeader.m_map) goes to the WorldEngineSystem's wanted map
// (FUN_7ff650c18a10; ClientInInstance 20300 sets it the same way). The client shows
// the loading screen, loads the map and goes to hero select while it connects to
// host:port over UDP (see package instance). Confirmed with Temple of Anubis.
func instanceJoin(a gamelink.Assignment, host string, port uint16) []byte {
	b := binary.LittleEndian.AppendUint64(nil, a.Map)
	b = appendCString(b, host)
	b = binary.LittleEndian.AppendUint16(b, port)
	b = binary.LittleEndian.AppendUint32(b, a.ConnID)
	var key [20]byte
	copy(key[:], a.Key)
	b = append(b, key[:]...)
	b = binary.LittleEndian.AppendUint64(b, 7)
	return binary.LittleEndian.AppendUint64(b, 1)
}

// DefaultMap is Temple of Anubis (the map Prometheus's own map test uses).
const DefaultMap = maps.Anubis

// sendToGame assigns the client a connection on the game server and sends 20600.
func (c *Ctx) sendToGame(mapID uint64) (gamelink.Assignment, string, error) {
	if c.logic.Instances == nil {
		return gamelink.Assignment{}, "", errors.New("lobbylogic runs without a game server link")
	}
	a, addr, err := c.logic.Instances.Assign(c.Session.ID, playerName(), mapID)
	if err != nil {
		return a, "", err
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return a, "", err
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return a, "", err
	}
	return a, addr, c.Send("ClientInLobbyInstance", 20600, instanceJoin(a, host, uint16(port)))
}

// onLeaveGame handles ClientOutLobbyConnect 21801 (empty), sent by "Leave Game" in
// the in-game menu: the game server closes the player's connections with a FIN.
// (The client doesn't return to the title screen on its own.)
func onLeaveGame(c *Ctx) error {
	if c.logic.Instances == nil {
		return errors.New("lobbylogic runs without a game server link")
	}
	n, err := c.logic.Instances.ReleaseSession(c.Session.ID)
	c.Log.Info("leave game", "released_connections", n)
	if err != nil {
		return err
	}
	// A practice / private game ends with the match: without the reset the client
	// would come back to the practice page / private game lobby. Always sent, since a
	// restarted lobbylogic doesn't know the game (resetting an empty object is harmless).
	return c.leaveCustomGame()
}
