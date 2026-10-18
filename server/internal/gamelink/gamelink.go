// Package gamelink is the link between lobbylogic and the game server
// (package gamelink). It is its own package so that changes to the game server
// don't rebuild and restart lobbylogic (scripts/watch.sh follows dependencies).
//
// The game server hangs off lobbylogic the way lobbylogic hangs off the lobby
// gateway: it dials lobbylogic's instance port and receives the connections
// lobbylogic hands out (connection ID, key), one JSON Wire message per line.
// Either side can restart: lobbylogic replays its assignments when the game
// server says hello, and the hello carries the game server's own assignments so
// a restarted lobbylogic learns them back.
//
// Several game servers can be linked at once (the R&D server and the instance
// server): each names itself in its hello, and lobbylogic sends new matches to
// the active one (lobbylogic --game-server, !gameserver).
//
// game server -> lobbylogic:
//
//	hello  {addr, name, conns,   the UDP address the client should connect to, the
//	        ping, modes, maps}   server's name (DefaultServer when empty), known assignments,
//	                             its ping interval in ms (0: it sends no pings) and the modes
//	                             and maps it serves (empty: everything)
//	ping   {}                    every hello.ping ms (Client: PingInterval)
//	frame  {view}                a live server view frame for a subscribed connection
//	                             (hello.view: the server streams them; DebugDrawing.md §5)
//
// lobbylogic -> game server:
//
//	assign  {conn}         a client will connect with this connection ID and key
//	release {conn}         forget a connection
//	pong    {}             the answer to each ping
//	view    {conn, on}     start / stop the live server view of a connection (only to a
//	                       game server whose hello says view: true)
//
// Heartbeat: a side that has seen the other's heartbeat drops the link after Timeout
// of silence (lobbylogic: when the hello announced pings; the game server: once a
// pong came back), so a hung process counts as gone, not only a closed socket. Old
// peers that send no pings / pongs keep working without the timeout.
package gamelink

import (
	"encoding/json"
	"time"

	"owserver/internal/maps"
)

const (
	WireHello   = "hello"
	WireAssign  = "assign"
	WireRelease = "release"
	WirePing    = "ping"
	WirePong    = "pong"
	WireView    = "view"  // lobbylogic -> game server: {conn, on}
	WireFrame   = "frame" // game server -> lobbylogic: {view}
)

// PingInterval is how often Client pings lobbylogic; Timeout is the silence after
// which either side drops the link.
const (
	PingInterval = 2 * time.Second
	Timeout      = 3 * PingInterval
)

// The modes a game server can serve (hello.modes): the title screen's buttons.
const (
	ModeMatchmaking = "matchmaking" // PLAY (22000)
	ModePrivate     = "private"     // PRIVATE GAME (24001 type 1)
	ModePractice    = "practice"    // PRACTICE VS AI (24001 type 3)
	ModeTutorial    = "tutorial"    // TUTORIAL (24001 type 4)
)

// AllModes are every mode, for a hello.
var AllModes = []string{ModeMatchmaking, ModePrivate, ModePractice, ModeTutorial}

// AllMaps are the data GUIDs of every map in the catalogue (package maps), for a hello.
func AllMaps() []uint64 {
	out := make([]uint64, len(maps.All))
	for i, m := range maps.All {
		out[i] = m.Data
	}
	return out
}

// DefaultServer is the name of a game server whose hello names none: the R&D
// game server (cmd/gameserver).
const DefaultServer = "gameserver"

type Wire struct {
	Type  string       `json:"type"`
	Addr  string       `json:"addr,omitempty"`
	Name  string       `json:"name,omitempty"` // hello: the game server's name
	Conn  *Assignment  `json:"conn,omitempty"`
	Conns []Assignment `json:"conns,omitempty"`
	Ping  int          `json:"ping,omitempty"`  // hello: ping interval in ms; 0 = no pings (no heartbeat timeout)
	Modes []string     `json:"modes,omitempty"` // hello: the modes served (Mode*); empty = all
	Maps  []uint64     `json:"maps,omitempty"`  // hello: the maps served (data GUIDs); empty = all
	// Live server view (DebugDrawing.md §5).
	View  bool       `json:"view,omitempty"`  // hello: the server streams view frames; view: on / off
	Frame *ViewFrame `json:"frame,omitempty"` // frame
}

// ViewFrame is one tick of a game server's view of a connection's match, relayed by
// lobbylogic to the client's Prometheus as PrometheusServerView 32002 (docs/JAM.md §13.8).
type ViewFrame struct {
	Conn   uint32          `json:"conn"`
	Seq    uint32          `json:"seq"`     // per subscription, from 1; a gap = a dropped frame
	Frame  uint32          `json:"frame"`   // the match's command frame
	SentUS int64           `json:"sent_us"` // the game server's wall clock (Unix µs) when it built the frame
	Body   json.RawMessage `json:"body"`    // compact JSON (DebugDrawing.md §5.2)
}

// ServesMode reports whether a hello's server serves mode (an empty list serves all).
func (w *Wire) ServesMode(mode string) bool {
	if len(w.Modes) == 0 {
		return true
	}
	for _, m := range w.Modes {
		if m == mode {
			return true
		}
	}
	return false
}

// ServesMap reports whether a hello's server serves the map (an empty list serves all).
func (w *Wire) ServesMap(data uint64) bool {
	if len(w.Maps) == 0 {
		return true
	}
	for _, m := range w.Maps {
		if m == data {
			return true
		}
	}
	return false
}

// Assignment is one client's ticket to the game server, as sent in 20600.
type Assignment struct {
	ConnID  uint32 `json:"conn_id"` // 20600's u32; the packet header carries it as ConnID<<4
	Key     []byte `json:"key"`     // 20600's 20-byte key: HMAC-MD5 key of every packet
	Map     uint64 `json:"map"`     // 20600's first u64: the map to load (STUMapHeader.m_map, e.g. 0x080000000000005B)
	Session uint64 `json:"session"` // the lobby session it was handed to
	Name    string `json:"name,omitempty"`
}
