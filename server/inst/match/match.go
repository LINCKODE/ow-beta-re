// Package match is the matches' lifecycle and the players in them:
//
//	assign   lobbylogic hands a connection to the server (gamelink); the match for
//	         its map is created with its world if it has none (docs/MatchLogic.md §15)
//	connect  the client completes the UDP handshake (package link)
//	join     21602 "map loaded": the player's controller (+ 20301 naming it the local
//	         entity), then the match's world, one entity per packet
//	lock-in  21603: the hero body at a start spawn point, the controller linked to it
//	         (0x20, team, PlayerInfo hero: closes hero select, §15.3), the hero's
//	         Statescript start-up (sim.Host, §14)
//	leave    the lobby releases the connection (21801), the client FINs or times out
//
// Every tick (package clock) each match advances its frame, ticks its world and
// simulation and sends every connected player one data packet: the frame P, the
// player's queued JAM messages, the input ack, the clock flags and the next
// replication entries. A Manager is not safe for concurrent use: the server's game
// loop owns it and calls it for every event.
package match

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"owserver/inst/bits"
	"owserver/inst/clock"
	"owserver/inst/game"
	"owserver/inst/jam"
	"owserver/inst/link"
	"owserver/inst/replication"
	"owserver/inst/sim"
	"owserver/inst/wire"
	"owserver/inst/world"
	"owserver/internal/gamelink"
	"owserver/internal/heroes"
)

// Sender sends a data packet's payload to a connection (package link).
type Sender interface {
	SendData(conn uint32, payload []byte) (uint32, error)
}

// Config tunes the matches; the zero value works.
type Config struct {
	Policy clock.Policy
	// Batch lets updates, destroys and Statescript entries share a packet (a create
	// still ends one). Off: one entry per packet, as the R&D server does.
	Batch bool
	// NewSim makes a match's simulation host (default: sim.Static).
	// Shadow: simulate and encode the hero deltas but never send them (T052 shadow compare): the client keeps
	// predicting on its own, so a delta the client would misread can't wedge it.
	Shadow bool
	NewSim func(*world.World) sim.Host
	// NewGame makes a match's rules host (inst/game: the brain and objective graphs on the server) at the match's
	// first frame; nil, or a nil game, runs no rules.
	NewGame func(w *world.World, frame uint32) *game.Game
	// GameEvery is the interval of the rules host's replication (remote deltas, capture mirror) in ticks (default
	// 4): one entry goes out per packet, and the hero's deltas need most of them.
	GameEvery uint32
	// FirstFrame is a new match's first frame number (default 1).
	FirstFrame uint32
	// Serializers produce the client-layout payloads (default: replication.Default).
	Serializers *replication.Serializers
	// DeltaEvery is the interval of a hero's Statescript deltas in ticks (0/1: every
	// tick). The client rolls back and replays on every delta, so fewer deltas mean
	// fewer replays; the client drops unconfirmed predictions 40 CF after them.
	DeltaEvery uint32
}

// PlayerState is where a player is in the lifecycle.
type PlayerState string

const (
	Assigned  PlayerState = "assigned"
	Connected PlayerState = "connected"
	Joined    PlayerState = "joined"  // map loaded: controller + world sent
	Playing   PlayerState = "playing" // locked in a hero
)

// Player is one connection in a match.
type Player struct {
	gamelink.Assignment
	State      PlayerState
	Controller uint32
	Body       uint32
	Hero       uint64

	InputAck  uint32 // newest client command frame received (sent back in stream 3)
	LastFrame uint32 // P of our last packet
	Sent      int    // data packets sent
	Received  int    // data packets received
	SendErrs  int

	match *Match
	rep   *replication.Replicator
	ch    *link.Channel
	steer *clock.Steer
	seen  map[uint16]bool
	jlog  []JamLogEntry

	view *game.View // what the client has of the rules host's entities (brain, objective copies)

	deltaQueued bool   // a Statescript delta for the body waits in the replication queue
	deltaAt     uint32 // the match frame the last one was queued at

	xf *transformRecord // the last PlayerTransform 21601 (live server view)
}

// Match is one match: its world, simulation, frame counter and players.
type Match struct {
	Key     uint64 // the map for now: the lobby's assignment carries no match id
	World   *world.World
	Sim     sim.Host
	Game    *game.Game // nil: no rules host
	Frame   uint32
	Created time.Time
	players map[uint32]*Player
}

// Manager holds the matches.
type Manager struct {
	log     *slog.Logger
	cfg     Config
	out     Sender
	started time.Time
	matches map[uint64]*Match
	players map[uint32]*Player

	views     map[uint32]*viewSub          // live server view subscriptions (serverview.go)
	notes     map[uint32]map[string]string // live server view debug lines per connection (0: all)
	viewStats ViewStats
}

func NewManager(log *slog.Logger, cfg Config, out Sender) *Manager {
	if cfg.NewSim == nil {
		cfg.NewSim = func(*world.World) sim.Host { return sim.Static{} }
	}
	if cfg.FirstFrame == 0 {
		cfg.FirstFrame = 1
	}
	if cfg.GameEvery == 0 {
		cfg.GameEvery = 4
	}
	if cfg.Serializers == nil {
		cfg.Serializers = &replication.Default
	}
	return &Manager{log: log, cfg: cfg, out: out, started: time.Now(), matches: map[uint64]*Match{}, players: map[uint32]*Player{}}
}

// Assign adds a connection lobbylogic handed out, creating its match if needed.
func (m *Manager) Assign(a gamelink.Assignment) error {
	if old := m.players[a.ConnID]; old != nil {
		m.leave(old, "reassigned")
	}
	mt := m.matches[a.Map]
	if mt == nil {
		w, err := world.New(a.Map, m.log)
		if err != nil {
			return fmt.Errorf("match %#016x: %w", a.Map, err)
		}
		mt = &Match{Key: a.Map, World: w, Sim: m.cfg.NewSim(w), Frame: m.cfg.FirstFrame, Created: time.Now(), players: map[uint32]*Player{}}
		if m.cfg.NewGame != nil {
			mt.Game = m.cfg.NewGame(w, mt.Frame)
		}
		m.matches[a.Map] = mt
		m.log.Info("match: created", "map", fmt.Sprintf("%#016x", a.Map), "name", w.Map.Name)
	}
	p := &Player{Assignment: a, State: Assigned, match: mt}
	p.reset(m.cfg)
	mt.players[a.ConnID] = p
	m.players[a.ConnID] = p
	m.log.Info("player assigned", "conn", a.ConnID, "map", w(mt), "name", a.Name, "session", a.Session)
	return nil
}

func (m *Manager) ser() *replication.Serializers { return m.cfg.Serializers }

func w(mt *Match) string { return mt.World.Map.Name }

func (p *Player) reset(cfg Config) {
	p.rep = replication.NewReplicator()
	// The residency guard: no create of a definition the client hasn't loaded on this map (a client assert,
	// docs/InstanceServer.md §12.14, §12.15).
	mp := p.match.World.Map
	p.rep.Residency = &replication.Residency{Resident: mp.Resident, Placed: uint32(mp.Placed)}
	p.rep.OnePerPacket = !cfg.Batch
	p.rep.Snapshots = cfg.Serializers.Snapshot
	p.ch = link.NewChannel()
	p.steer = clock.NewSteer(cfg.Policy)
	p.seen = map[uint16]bool{}
	p.InputAck = 0
	p.view = nil
}

// Release removes a player (the lobby released the connection).
func (m *Manager) Release(conn uint32) {
	if p := m.players[conn]; p != nil {
		m.leave(p, "released")
	}
}

// Connected: the client completed the handshake. A reconnect (a new SYN) starts
// the player over: the client has nothing.
func (m *Manager) Connected(conn uint32) {
	p := m.players[conn]
	if p == nil {
		return
	}
	if p.State != Assigned {
		m.log.Info("player reconnected: starting over", "conn", conn, "was", p.State)
		m.dropEntities(p)
	}
	p.reset(m.cfg)
	p.steer.Reset()
	p.State = Connected
	m.log.Info("player connected", "conn", conn, "map", w(p.match))
}

// Closed: the connection ended (FIN, timeout, release).
func (m *Manager) Closed(conn uint32, why string) {
	if p := m.players[conn]; p != nil {
		m.leave(p, why)
	}
}

func (m *Manager) leave(p *Player, why string) {
	mt := p.match
	m.dropEntities(p)
	delete(mt.players, p.ConnID)
	delete(m.players, p.ConnID)
	m.log.Info("player left", "conn", p.ConnID, "why", why, "map", w(mt))
	if len(mt.players) == 0 {
		if mt.Game != nil {
			mt.Game.Close()
		}
		mt.Sim.Close()
		mt.World.Close()
		delete(m.matches, mt.Key)
		m.log.Info("match: ended (no players left)", "map", w(mt))
	}
}

// dropEntities removes a player's controller and body from the world (and from the
// other players' clients).
func (m *Manager) dropEntities(p *Player) {
	if p.Body != 0 {
		p.match.Sim.RemoveHero(p.Body)
		if p.match.Game != nil {
			p.match.Game.HeroRemoved(p.Body)
		}
	}
	p.deltaQueued = false
	for _, id := range []uint32{p.Body, p.Controller} {
		if id != 0 {
			m.destroy(p.match, id)
		}
	}
	p.Body, p.Controller, p.Hero = 0, 0, 0
}

func (m *Manager) destroy(mt *Match, id uint32) {
	mt.World.Remove(id)
	for _, o := range mt.players {
		if o.rep.Has(id) {
			m.queue(o, "destroy", replication.Entry{Entity: id, Op: replication.OpDestroy})
		}
	}
}

// queue queues entries to a player, logging what the replicator refused (the residency guard leaves out
// creates of definitions the client hasn't loaded, and their entities' entries; the rest is queued).
func (m *Manager) queue(p *Player, what string, es ...replication.Entry) {
	if err := p.rep.Queue(es...); err != nil {
		m.log.Error(what+": entries not queued", "conn", p.ConnID, "map", w(p.match), "err", err)
	}
}

// Data handles a client data packet received at at; acked / lost are the link's
// report on our packets.
func (m *Manager) Data(conn uint32, payload []byte, at time.Time, acked, lost []uint32) {
	p := m.players[conn]
	if p == nil || p.State == Assigned {
		return
	}
	p.Received++
	p.ch.Acked(acked)
	p.ch.Lost(lost)
	p.rep.Acked(acked)
	p.rep.Lost(lost)
	cp, err := wire.ParseClientPayload(payload)
	if err != nil {
		m.log.Warn("client payload", "conn", conn, "err", err)
	}
	if newest, ok := cp.Input.Newest(); ok {
		if !p.steer.InputSeen {
			m.log.Info("client input started", "conn", conn, "input_frame", newest, "frame", p.match.Frame, "lead", int32(newest-p.match.Frame))
		}
		if int32(newest-p.InputAck) > 0 {
			p.InputAck = newest
		}
		p.steer.Observe(newest, p.match.Frame, cp.Input.Timing)
		if p.Body != 0 {
			p.match.Sim.Input(p.Body, cp.Input.Entries)
		}
	}
	for _, e := range p.ch.Receive(cp.Jam) {
		m.handle(p, e, at)
	}
}

func (m *Manager) handle(p *Player, e jam.Envelope, at time.Time) {
	id := e.Msg.ID()
	p.logJam(e, at)
	log := m.log.With("conn", p.ConnID)
	first := !p.seen[id]
	p.seen[id] = true
	attrs := []any{"msg", id, "name", jam.Name(id), "reliable", e.Reliable, "jam_seq", e.Seq, "body", fmt.Sprintf("%+v", e.Msg)}
	if first {
		log.Info("JAM message (first of its kind; more at debug)", attrs...)
	} else {
		log.Debug("JAM message", attrs...)
	}
	switch msg := e.Msg.(type) {
	case *jam.TimeSyncPing:
		p.ch.Send(&pong{ping: *msg, recv: at, started: m.started}, true)
	case *jam.MapLoaded:
		m.join(p, "map loaded (21602)")
	case *jam.HeroLockIn:
		m.lockIn(p, msg.Hero)
	case *jam.PlayerTransform:
		p.xf = &transformRecord{msg: *msg, frame: p.match.Frame}
	}
}

// pong answers a time-sync ping (21608) with 20304: the client's times plus our
// receive time, our clock and our send time (stamped when the packet is encoded).
// The client (FUN_7ff650c45fd0) appends its receive time t5 and computes RTT =
// (t5-t1) - (t4-t2). Times are ms since the server started.
type pong struct {
	ping    jam.TimeSyncPing
	recv    time.Time
	started time.Time
}

func (p *pong) ID() uint16 { return jam.IDTimeSyncPong }
func (p *pong) Encode(w *bits.Writer) {
	now := uint64(time.Since(p.started).Milliseconds())
	times := append(append([]uint64{}, p.ping.Times...), uint64(p.recv.Sub(p.started).Milliseconds()), now, now)
	(&jam.TimeSyncPong{N: p.ping.N, Times: times}).Encode(w)
}
func (p *pong) Decode(*bits.Reader) error { return errors.New("pong: encode only") }

// Join sends a player whose map has loaded its controller and the world (control
// API: also without 21602; the map must be loaded).
func (m *Manager) Join(conn uint32) error {
	p := m.players[conn]
	if p == nil {
		return errors.New("no such player")
	}
	if p.State == Assigned {
		return errors.New("not connected")
	}
	m.join(p, "control API")
	return nil
}

func (m *Manager) join(p *Player, why string) {
	if p.State != Connected {
		return
	}
	mt := p.match
	ctrl, err := mt.World.Add(world.Entity{Kind: world.KindController, Def: 0x0e, Owner: p.ConnID, Team: mt.World.PlayerTeam()})
	if err != nil {
		m.log.Error("join: controller", "conn", p.ConnID, "err", err)
		return
	}
	p.Controller, p.State = ctrl.ID, Joined
	// The controller (definition 0x0E, flags 0x38 = transform + player loader +
	// PlayerInfo) and 20301 naming it the local entity, which system 8 waits for.
	p.ch.Send(&jam.LocalEntity{Entity: jam.EntityID(ctrl.ID)}, true)
	q := []replication.Entry{{Entity: ctrl.ID, Op: replication.OpCreate,
		Create: replication.Create{Def: 0x0e, Flags: 0x38, Rot: [4]float32{0, 0, 0, 1}, Scale: [3]float32{1, 1, 1}}}}
	// The world: the map's placements, then their teams; the other players' bodies.
	var teams []replication.Entry
	for _, e := range mt.World.Entities() {
		switch {
		case e.Kind == world.KindPlacement:
			q = append(q, replication.Entry{Entity: e.ID, Op: replication.OpCreate, Create: replication.Create{FromMap: true, MapIndex: e.MapIndex}})
			if e.Team != world.NoTeam {
				teams = append(teams, replication.Entry{Entity: e.ID, Op: replication.OpUpdate,
					Components: []replication.ComponentUpdate{m.ser().Mirror.FilterBits(replication.TeamBit(e.Team))}})
			}
		case e.Kind == world.KindHero && e.Owner != p.ConnID:
			q = append(q, m.bodyCreate(e))
		}
	}
	q = append(q, teams...)
	if mt.Game != nil {
		// The rules host's entities: the brain and the objective copies, with their Statescript (remote format).
		p.view = game.NewView()
		q = append(q, mt.Game.Join(p.view)...)
	}
	m.queue(p, "join", q...)
	m.log.Info("join: controller and world queued", "conn", p.ConnID, "trigger", why, "controller", fmt.Sprintf("%#x", ctrl.ID),
		"map", w(mt), "entries", len(q))
}

// bodyCreate is a hero body's create: its definition at its spawn point with a
// movement snapshot (0x12 overrides the create transform, docs/MatchLogic.md §8.1),
// health and team.
func (m *Manager) bodyCreate(e *world.Entity) replication.Entry {
	ser := m.ser().Mirror
	return replication.Entry{Entity: e.ID, Op: replication.OpCreate,
		Create: replication.Create{Def: e.Def, Flags: 0x08, Pos: e.Pos, Rot: replication.YawQuat(e.Yaw), Scale: [3]float32{1, 1, 1},
			Mover: &replication.Mover{Pos: e.Pos, Yaw: e.Yaw}},
		Components: []replication.ComponentUpdate{
			// Health rides in the create (Prometheus's player spawner's values), then the team.
			ser.Health(replication.HealthPart{Enabled: 1, Max: e.Health, Current: e.Health, Flag10: 1, Flag11: 1}),
			ser.FilterBits(replication.TeamBit(e.Team))}}
}

// heroHealth is every hero's health for now.
const heroHealth = 200

// LockIn plays a hero (control API, or the client's 21603).
func (m *Manager) LockIn(conn uint32, hero uint64) error {
	p := m.players[conn]
	if p == nil {
		return errors.New("no such player")
	}
	return m.lockIn(p, hero)
}

func (m *Manager) lockIn(p *Player, hero uint64) error {
	log := m.log.With("conn", p.ConnID, "hero", fmt.Sprintf("%#016x", hero))
	h, ok := heroes.ByHero(hero)
	if !ok {
		log.Warn("lock in: unknown hero (not in the generated hero catalogue)")
		return errors.New("unknown hero")
	}
	if p.State != Joined && p.State != Playing {
		log.Warn("lock in before the join: ignored", "state", p.State)
		return errors.New("not joined")
	}
	if p.Body != 0 && p.Hero == hero {
		log.Info("lock in: already playing this hero")
		return nil
	}
	mt := p.match
	if p.Body != 0 {
		mt.Sim.RemoveHero(p.Body)
		if mt.Game != nil {
			mt.Game.HeroRemoved(p.Body)
		}
		m.destroy(mt, p.Body) // hero change: the previous body goes first
		p.deltaQueued = false
	}
	e := world.Entity{Kind: world.KindHero, Def: h.Def, Owner: p.ConnID, Team: mt.World.PlayerTeam(), Hero: hero, Health: heroHealth}
	if sp, ok := mt.World.NextStart(); ok {
		e.Pos, e.Yaw = sp.Pos, sp.Yaw
		log.Info("lock in: hero at the start room", "team", sp.Team, "spawn", fmt.Sprintf("%#x", sp.Index), "room", sp.Room, "pos", sp.Pos, "yaw", sp.Yaw)
	}
	body, err := mt.World.Add(e)
	if err != nil {
		return err
	}
	p.Body, p.Hero, p.State = body.ID, hero, Playing
	if mt.Game != nil {
		mt.Game.HeroSpawned(body.ID, p.Controller, hero)
	}
	for _, o := range mt.players {
		if o.State == Joined || o.State == Playing {
			m.queue(o, "lock in: body", m.bodyCreate(body))
		}
	}
	// The controller's link to the body (aim, camera, movement attach), its team, and
	// PlayerInfo's hero, which hero select's exit waits for (§15.3).
	q := []replication.Entry{{Entity: p.Controller, Op: replication.OpUpdate, Components: []replication.ComponentUpdate{
		m.ser().Mirror.Possession(body.ID, body.ID, body.ID), m.ser().Mirror.FilterBits(replication.TeamBit(e.Team)),
		m.ser().Mirror.PlayerInfo(replication.PlayerInfo{Hero: &hero})}}}
	// The hero's Statescript start-up, in the local format: the controller link has
	// made the body the local entity (the client drops a remote-format update's
	// instances on its local entity).
	// The simulation starts from the same start-up, as of the client's newest input
	// frame; the full update ends there and the simulation's deltas follow on.
	if st, endCF, ok := mt.Sim.SpawnHero(body.ID, h.Def, p.InputAck); ok {
		q = append(q, replication.Entry{Entity: body.ID, ScriptsAt: func(frame uint32) *bits.Writer {
			if endCF != 0 {
				frame = endCF
			}
			sw, err := m.ser().Script.HeroStartup(frame, st)
			if err != nil {
				m.log.Error("lock in: hero start-up", "conn", p.ConnID, "err", err)
				return nil
			}
			return sw
		}})
	}
	m.queue(p, "lock in", q...)
	log.Info("lock in: hero body, controller link and start-up queued", "hero_name", h.Name, "body", fmt.Sprintf("%#x", body.ID),
		"def", fmt.Sprintf("%#016x", 0x0400000000000000|uint64(h.Def)))
	return nil
}

// Tick advances every match by one command frame and sends each connected player
// its packet.
func (m *Manager) Tick(now time.Time) {
	for _, mt := range m.matches {
		mt.Frame++
		mt.World.Tick()
		mt.Sim.Tick(mt.Frame)
		if mt.Game != nil {
			mt.Game.Tick(mt.Frame)
		}
		for _, p := range mt.players {
			m.queueDelta(p)
			if mt.Game != nil && p.view != nil && mt.Frame%m.cfg.GameEvery == 0 {
				if es := mt.Game.Update(p.view, mt.Frame); len(es) > 0 {
					m.queue(p, "game update", es...)
				}
			}
			if p.State != Assigned {
				m.send(p, now)
			}
		}
	}
}

func (m *Manager) send(p *Player, now time.Time) {
	frame := p.match.Frame
	env := p.ch.Take()
	entries, ents := p.rep.Take(frame)
	pl := wire.ServerPayload{Frame: frame, Jam: env, InputAck: p.InputAck, Clock: p.steer.Flags(now), Entities: ents}
	b, err := pl.Encode()
	if err == nil {
		var seq uint32
		if seq, err = m.out.SendData(p.ConnID, b); err == nil {
			p.ch.Sent(seq, env)
			p.rep.Sent(seq, entries)
			p.LastFrame = frame
			p.Sent++
			return
		}
	}
	p.ch.Unsent(env)
	p.rep.Unsent(entries)
	p.SendErrs++
	if p.SendErrs == 1 || p.SendErrs%1000 == 0 {
		m.log.Warn("send", "conn", p.ConnID, "err", err, "errors", p.SendErrs)
	}
}

// queueDelta queues one Statescript delta for the player's body when its simulation
// has something the client hasn't been sent: one entry at a time, encoded when the
// packet is built, so it covers everything simulated up to then (and the
// replication queue never fills with deltas). The delta is a union from the last
// acknowledged one (sim.Host, replication.LocalGhost): the transport's acks and losses
// go back to the host, and a lost delta is not resent; the next union covers it
// (docs/StatescriptSync.md §6.1).
func (m *Manager) queueDelta(p *Player) {
	if p.Body == 0 || p.State != Playing || p.deltaQueued || !p.match.Sim.Pending(p.Body) {
		return
	}
	if every := m.cfg.DeltaEvery; every > 1 && p.match.Frame-p.deltaAt < every {
		return
	}
	p.deltaAt = p.match.Frame
	body, mt := p.Body, p.match
	if m.cfg.Shadow {
		mt.Sim.Delta(body) // encoded (GET /simdeltas shows it), not sent
		return
	}
	var enc *bits.Writer
	var end uint32
	done := false
	p.deltaQueued = true
	p.rep.Queue(replication.Entry{Entity: body, ScriptsAt: func(uint32) *bits.Writer {
		if !done {
			done = true
			if p.Body == body {
				p.deltaQueued = false
			}
			enc, end = mt.Sim.Delta(body)
		}
		return enc
	},
		Acked: func() {
			if end != 0 {
				mt.Sim.Acked(body, end)
			}
		},
		Lost: func() {
			if end != 0 {
				mt.Sim.Lost(body)
			}
		}})
}

// Close ends every match.
func (m *Manager) Close() {
	for _, mt := range m.matches {
		if mt.Game != nil {
			mt.Game.Close()
		}
		mt.Sim.Close()
		mt.World.Close()
	}
	m.matches, m.players = map[uint64]*Match{}, map[uint32]*Player{}
}

// Assignments are the current players' assignments (for the hello to lobbylogic).
func (m *Manager) Assignments() []gamelink.Assignment {
	out := make([]gamelink.Assignment, 0, len(m.players))
	for _, p := range m.players {
		out = append(out, p.Assignment)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ConnID < out[j].ConnID })
	return out
}
