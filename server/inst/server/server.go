// Package server wires the instance server together: the UDP link, the lobbylogic
// link, the matches and the control API around one game loop.
//
// Concurrency: the game loop is the only goroutine that touches match state
// (matches, worlds, players, replication, clock steering). It runs on the 16 ms
// tick (clock.Ticker); every tick it first runs the events that arrived since the
// last one (link and lobbylogic events, control API calls, in arrival order), then
// advances the matches and sends each connected player its packet. The link and
// lobbylogic goroutines only post events; the control API posts calls and waits.
package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"owserver/inst/clock"
	"owserver/inst/link"
	"owserver/inst/lobbylink"
	"owserver/inst/match"
	"owserver/internal/gamelink"
)

// Name is the instance server's name on the lobbylogic link (lobbylogic --game-server instance).
const Name = "instance"

// Config is the instance server's configuration.
type Config struct {
	Addr      string // UDP listen address
	Advertise string // the address clients are told to connect to (20600)
	LogicAddr string // lobbylogic's instance port
	Link      link.Config
	Match     match.Config
}

// Server is the instance server.
type Server struct {
	log    *slog.Logger
	cfg    Config
	Link   *link.Link
	mgr    *match.Manager
	events chan func()
	ticker clock.Ticker
	ticks  uint64
	loopUp chan struct{}
	lobby  *lobbylink.Link
}

func New(log *slog.Logger, cfg Config) *Server {
	s := &Server{log: log, cfg: cfg, events: make(chan func(), 4096), loopUp: make(chan struct{})}
	s.Link = link.New(log.With("part", "link"), linkEvents{s}, cfg.Link)
	s.mgr = match.NewManager(log.With("part", "match"), cfg.Match, s.Link)
	return s
}

// Run serves until ctx ends.
func (s *Server) Run(ctx context.Context) error {
	if err := s.Link.Listen(s.cfg.Addr); err != nil {
		return err
	}
	errs := make(chan error, 2)
	go func() { errs <- s.Link.Serve(ctx) }()
	s.lobby = &lobbylink.Link{Log: s.log.With("part", "lobbylink"), LogicAddr: s.cfg.LogicAddr, Advertise: s.cfg.Advertise, Name: Name, Handler: lobbyEvents{s}}
	s.lobby.Init()
	go func() { errs <- s.lobby.Run(ctx) }()
	close(s.loopUp)
	s.ticker.Run(ctx, s.tick)
	s.mgr.Close()
	for range 2 {
		if err := <-errs; err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) tick(n uint64, now time.Time) {
	// What arrived before this tick; later events wait for the next one.
	for range len(s.events) {
		(<-s.events)()
	}
	s.ticks = n
	s.mgr.Tick(now)
	s.mgr.ViewTick(now, s.ticker.Late, s.frame)
}

// frame hands a live server view frame to the lobbylogic link (non-blocking).
func (s *Server) frame(f *gamelink.ViewFrame) bool {
	if s.lobby == nil {
		return false
	}
	return s.lobby.Frame(f)
}

// post runs f on the game loop (before the next tick).
func (s *Server) post(f func()) { s.events <- f }

// ErrStopped is Do's error when the loop doesn't run.
var ErrStopped = errors.New("server: game loop not running")

// Do runs f on the game loop and waits for it (control API).
func (s *Server) Do(ctx context.Context, f func(m *match.Manager) error) error {
	select {
	case <-s.loopUp:
	case <-ctx.Done():
		return ctx.Err()
	}
	done := make(chan error, 1)
	select {
	case s.events <- func() { done <- f(s.mgr) }:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return ErrStopped
	}
}

// Stats is the loop's state for the control API.
type Stats struct {
	Ticks      uint64 `json:"ticks"`
	LateTicks  int    `json:"late_ticks"`
	QueuedEvts int    `json:"queued_events"`
	// The live server view (DebugDrawing.md §5), read on the loop.
	View *match.ViewStats `json:"view,omitempty"`
}

func (s *Server) Stats() Stats {
	st := Stats{Ticks: s.ticks, LateTicks: s.ticker.Late, QueuedEvts: len(s.events)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.Do(ctx, func(m *match.Manager) error { v := m.ViewStats(); st.View = &v; return nil })
	return st
}

// linkEvents posts the UDP link's events to the loop.
type linkEvents struct{ s *Server }

func (e linkEvents) Connected(id uint32) { e.s.post(func() { e.s.mgr.Connected(id) }) }
func (e linkEvents) Data(id uint32, p []byte, at time.Time, acked, lost []uint32) {
	e.s.post(func() { e.s.mgr.Data(id, p, at, acked, lost) })
}
func (e linkEvents) Closed(id uint32, why string) { e.s.post(func() { e.s.mgr.Closed(id, why) }) }

// lobbyEvents posts lobbylogic's hand-outs to the loop.
type lobbyEvents struct{ s *Server }

func (e lobbyEvents) Assign(a gamelink.Assignment) {
	e.s.Link.Assign(a.ConnID, a.Key)
	e.s.post(func() {
		if err := e.s.mgr.Assign(a); err != nil {
			e.s.log.Error("assign", "conn", a.ConnID, "err", err)
		}
	})
}

func (e lobbyEvents) Release(conn uint32) {
	e.s.Link.Release(conn) // FIN; its Closed event removes the player
	e.s.post(func() { e.s.mgr.Release(conn) })
}

func (e lobbyEvents) View(conn uint32, on bool) {
	e.s.post(func() { e.s.mgr.SetView(conn, on) })
}

func (e lobbyEvents) Assignments() []gamelink.Assignment {
	var out []gamelink.Assignment
	e.s.Do(context.Background(), func(m *match.Manager) error { out = m.Assignments(); return nil })
	return out
}
