package match

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"owserver/inst/clock"
	"owserver/inst/game"
	"owserver/inst/jam"
	"owserver/inst/replication"
	"owserver/inst/sim"
	"owserver/inst/world"

	"owstatescript/statescript"
)

// JamLogEntry is a JAM message received from a client.
type JamLogEntry struct {
	Time     time.Time `json:"time"`
	Msg      uint16    `json:"msg"`
	Name     string    `json:"name"`
	Reliable bool      `json:"reliable"`
	Seq      uint32    `json:"seq"`
	Body     string    `json:"body"`
}

const jamLogSize = 500

func (p *Player) logJam(e jam.Envelope, at time.Time) {
	id := e.Msg.ID()
	p.jlog = append(p.jlog, JamLogEntry{Time: at, Msg: id, Name: jam.Name(id), Reliable: e.Reliable, Seq: e.Seq, Body: fmt.Sprintf("%+v", e.Msg)})
	if len(p.jlog) > jamLogSize {
		p.jlog = p.jlog[len(p.jlog)-jamLogSize:]
	}
}

// JamLog returns a player's received JAM messages, oldest first; skipPing leaves out
// the time-sync pings.
func (m *Manager) JamLog(conn uint32, skipPing bool) []JamLogEntry {
	p := m.players[conn]
	if p == nil {
		return nil
	}
	out := []JamLogEntry{}
	for _, e := range p.jlog {
		if !skipPing || e.Msg != jam.IDTimeSyncPing {
			out = append(out, e)
		}
	}
	return out
}

// PlayerInfo is a player as the control API shows it.
type PlayerInfo struct {
	ConnID     uint32      `json:"conn_id"`
	Name       string      `json:"name,omitempty"`
	Session    uint64      `json:"session"`
	Map        uint64      `json:"map"`
	MapName    string      `json:"map_name"`
	State      PlayerState `json:"state"`
	Controller uint32      `json:"controller,omitempty"`
	Body       uint32      `json:"body,omitempty"`
	Hero       uint64      `json:"hero,omitempty"`
	// The clock: our frame P, the newest client input frame, the lead the steering
	// acts on (the window minimum of input frame - our frame at arrival), the flags
	// sent, the client's timing report.
	Frame      uint32 `json:"frame"`
	InputFrame uint32 `json:"input_frame"`
	Lead       int32  `json:"lead"`
	MinLead    int32  `json:"min_lead"`
	Flags      uint8  `json:"clock_flags"`
	Resyncs    int    `json:"resyncs"`
	Timing     uint16 `json:"timing"`
	Sent       int    `json:"sent"`
	Received   int    `json:"received"`
	SendErrors int    `json:"send_errors"`
	// Replication and JAM.
	Queued      int `json:"queued"`
	Creates     int `json:"creates"`
	Updates     int `json:"updates"`
	Destroys    int `json:"destroys"`
	Scripts     int `json:"scripts"`
	Resent      int `json:"resent"`
	Refused     int `json:"refused"` // entries the residency guard kept from the client (docs/InstanceServer.md §12.15)
	JamResends  int `json:"jam_resends"`
	JamRecvSkip int `json:"jam_recv_skips"`
}

func (p *Player) info() PlayerInfo {
	r := p.rep
	return PlayerInfo{ConnID: p.ConnID, Name: p.Name, Session: p.Session, Map: p.Map, MapName: p.match.World.Map.Name, State: p.State,
		Controller: p.Controller, Body: p.Body, Hero: p.Hero,
		Frame: p.LastFrame, InputFrame: p.InputAck, Lead: p.steer.Lead, MinLead: p.steer.MinLead, Flags: p.steer.LastFlags(), Resyncs: p.steer.Resyncs,
		Timing: p.steer.Timing, Sent: p.Sent, Received: p.Received, SendErrors: p.SendErrs,
		Queued: r.Pending(), Creates: r.Creates, Updates: r.Updates, Destroys: r.Destroys, Scripts: r.Scripts, Resent: r.Resent,
		Refused: r.Refused, JamResends: p.ch.Resends, JamRecvSkip: p.ch.Skips}
}

// Players lists the players.
func (m *Manager) Players() []PlayerInfo {
	out := make([]PlayerInfo, 0, len(m.players))
	for _, p := range m.players {
		out = append(out, p.info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ConnID < out[j].ConnID })
	return out
}

// MatchInfo is a match as the control API shows it.
type MatchInfo struct {
	Map         uint64          `json:"map"`
	Name        string          `json:"name"`
	Created     time.Time       `json:"created"`
	Frame       uint32          `json:"frame"`
	Players     []uint32        `json:"players"`
	Placements  int             `json:"placements"`
	Teamed      int             `json:"teamed"`
	ECSEntities int             `json:"ecs_entities"`
	ECSEvents   uint64          `json:"ecs_events"`
	Entities    []*world.Entity `json:"entities,omitempty"`
	Sim         any             `json:"sim,omitempty"` // the simulation host's stats (sim.Runtime: per hero)
	Game        *GameInfo       `json:"game,omitempty"`
}

// GameInfo is the rules host's state (docs/InstanceServer.md §12).
type GameInfo struct {
	Phase     game.Phase     `json:"phase"`
	Points    []*game.Point  `json:"points"`
	Effects   map[string]int `json:"effects"`
	Unhandled map[string]int `json:"unhandled,omitempty"`
	Recent    []string       `json:"recent,omitempty"`
}

// Matches lists the matches (with their entities if full).
func (m *Manager) Matches(full bool) []MatchInfo {
	out := []MatchInfo{}
	for _, mt := range m.matches {
		mi := MatchInfo{Map: mt.Key, Name: mt.World.Map.Name, Created: mt.Created, Frame: mt.Frame, Placements: mt.World.Placed,
			Teamed: mt.World.Teamed, ECSEntities: mt.World.ECSEntities(), ECSEvents: mt.World.Events, Sim: mt.Sim.Stats()}
		for id := range mt.players {
			mi.Players = append(mi.Players, id)
		}
		sort.Slice(mi.Players, func(i, j int) bool { return mi.Players[i] < mi.Players[j] })
		if g := mt.Game; g != nil {
			mi.Game = &GameInfo{Phase: g.Phase, Points: g.Points(), Effects: g.Effects, Unhandled: g.Unhandled}
			if full {
				mi.Game.Recent = g.Recent
			}
		}
		if full {
			mi.Entities = mt.World.Entities()
		}
		out = append(out, mi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Map < out[j].Map })
	return out
}

// Queue sends replication entries to a player (control API experiments).
func (m *Manager) Queue(conn uint32, es ...replication.Entry) error {
	p := m.players[conn]
	if p == nil {
		return errors.New("no such player")
	}
	return p.rep.Queue(es...)
}

// SendJam queues a JAM message to a player (control API).
func (m *Manager) SendJam(conn uint32, msg jam.Message, reliable bool) error {
	p := m.players[conn]
	if p == nil {
		return errors.New("no such player")
	}
	p.ch.Send(msg, reliable)
	return nil
}

// Player returns a player's state for the control API (nil if unknown).
func (m *Manager) Player(conn uint32) *PlayerInfo {
	p := m.players[conn]
	if p == nil {
		return nil
	}
	pi := p.info()
	return &pi
}

// Frame is a player's match frame (for control API Statescript updates).
func (m *Manager) Frame(conn uint32) (uint32, bool) {
	p := m.players[conn]
	if p == nil {
		return 0, false
	}
	return p.match.Frame, true
}

// SetPolicy changes the clock steering of every player, now and for new ones.
func (m *Manager) SetPolicy(p clock.Policy) clock.Policy {
	m.cfg.Policy = p
	for _, pl := range m.players {
		pl.steer.SetPolicy(p)
	}
	return clock.NewSteer(p).Policy()
}

// SimTrace turns the runtime trace of a hero body's server-side Statescript on or
// off (on == nil: leave it) and returns what it buffered since the last call (JSON
// lines, the ss_trace vocabulary).
func (m *Manager) SimTrace(body uint32, on *bool) (string, error) {
	for _, mt := range m.matches {
		t, ok := mt.Sim.(sim.Tracer)
		if !ok {
			continue
		}
		if on != nil {
			if err := t.Trace(body, *on); err != nil {
				continue
			}
		}
		if out, err := t.DrainTrace(body); err == nil {
			return out, nil
		}
	}
	return "", errors.New("no simulated hero with that body")
}

// SimDeltas returns the last Statescript deltas encoded for a hero body.
func (m *Manager) SimDeltas(body uint32) ([]sim.DeltaRecord, error) {
	for _, mt := range m.matches {
		if t, ok := mt.Sim.(sim.Tracer); ok {
			if out, err := t.RecentDeltas(body); err == nil {
				return out, nil
			}
		}
	}
	return nil, errors.New("no simulated hero with that body")
}

// ShadowState returns a hero body's server-side Statescript at the end of command frame cf (shadow mode).
func (m *Manager) ShadowState(body, cf uint32) (*sim.ShadowSnapshot, error) {
	for _, mt := range m.matches {
		if t, ok := mt.Sim.(sim.Shadower); ok {
			if out, err := t.ShadowState(body, cf); err == nil || err.Error() != "no such hero" {
				return out, err
			}
		}
	}
	return nil, errors.New("no simulated hero with that body (shadow mode needs --shadow)")
}

// ShadowReset re-seeds a hero body's server-side Statescript from its start-up at its current frame.
func (m *Manager) ShadowReset(body uint32) (uint32, error) {
	for _, mt := range m.matches {
		if t, ok := mt.Sim.(sim.Shadower); ok {
			if f, err := t.ShadowReset(body); err == nil || err.Error() != "no such hero" {
				return f, err
			}
		}
	}
	return 0, errors.New("no simulated hero with that body (shadow mode needs --shadow)")
}

// SetPosition sets where the server believes a hero body is (the position source until the server models movement,
// docs/InstanceServer.md §12.9: the Prometheus bridge or a test posts it).
func (m *Manager) SetPosition(body uint32, pos [3]float32, yaw *float32) error {
	for _, mt := range m.matches {
		if e := mt.World.Get(body); e != nil && e.Kind == world.KindHero {
			e.Pos = pos
			if yaw != nil {
				e.Yaw = *yaw
			}
			return nil
		}
	}
	return errors.New("no such hero body")
}

// GameSend sends a raw rules-layer game message in the match of a player (target 0 = every entity).
func (m *Manager) GameSend(conn, target uint32, msg uint64, params map[uint64]statescript.Value) error {
	p := m.players[conn]
	if p == nil || p.match.Game == nil {
		return errors.New("no such player, or its match has no rules host")
	}
	return p.match.Game.Send(target, msg, params)
}
