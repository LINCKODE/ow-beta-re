// Package lobbylink is the instance server's end of the link to lobbylogic
// (package gamelink): it dials lobbylogic's instance port, says hello with the UDP
// address clients should connect to and the assignments it already has, and
// receives the connections lobbylogic hands out (assign) and takes back (release).
// It reconnects whenever the link drops; either side can restart. The dialing,
// heartbeat and reconnects are gamelink.Client's.
package lobbylink

import (
	"context"
	"log/slog"
	"time"

	"owserver/internal/gamelink"
)

// Handler receives lobbylogic's hand-outs. Calls come from the link's goroutine.
type Handler interface {
	Assign(a gamelink.Assignment)
	Release(conn uint32)
	// View starts / stops the live server view of a connection (DebugDrawing.md §5).
	View(conn uint32, on bool)
	// Assignments are the connections the server has (for the hello).
	Assignments() []gamelink.Assignment
}

// Link is the lobbylogic link.
type Link struct {
	Log       *slog.Logger
	LogicAddr string // lobbylogic --instance-addr
	Advertise string // the UDP address lobbylogic puts in 20600
	Name      string // the server's name in the hello (lobbylogic --game-server / !gameserver)
	Handler   Handler
	Retry     time.Duration // default 1 s
	// Modes and Maps are announced in the hello; nil = gamelink.AllModes / every catalogue map.
	Modes []string
	Maps  []uint64
	Ping  time.Duration // default gamelink.PingInterval

	client *gamelink.Client
}

// init sets up the link's client (Run; Init for Frame before Run).
func (l *Link) init() {
	if l.client != nil {
		return
	}
	l.client = &gamelink.Client{Log: l.Log, Addr: l.LogicAddr, Retry: l.Retry, Ping: l.Ping,
		Hello: func() gamelink.Wire {
			modes, maps := l.Modes, l.Maps
			if modes == nil {
				modes = gamelink.AllModes
			}
			if maps == nil {
				maps = gamelink.AllMaps()
			}
			return gamelink.Wire{Addr: l.Advertise, Name: l.Name, Conns: l.Handler.Assignments(), Modes: modes, Maps: maps, View: true}
		},
		Handle: func(m gamelink.Wire) {
			switch m.Type {
			case gamelink.WireAssign:
				l.Handler.Assign(*m.Conn)
			case gamelink.WireRelease:
				l.Handler.Release(m.Conn.ConnID)
			case gamelink.WireView:
				l.Handler.View(m.Conn.ConnID, m.View)
			}
		},
	}
}

// Frame queues a live server view frame to lobbylogic without blocking; false = dropped
// (link down or queue full).
func (l *Link) Frame(f *gamelink.ViewFrame) bool {
	if l.client == nil {
		return false
	}
	return l.client.Send(gamelink.Wire{Type: gamelink.WireFrame, Frame: f})
}

// Dropped is how many frames Frame dropped.
func (l *Link) Dropped() uint64 {
	if l.client == nil {
		return 0
	}
	return l.client.Dropped()
}

// Run serves the link until ctx ends.
func (l *Link) Run(ctx context.Context) error {
	l.init()
	return l.client.Run(ctx)
}

// Init sets up the client before Run, so Frame works from the start (it drops until
// the link is up).
func (l *Link) Init() { l.init() }
