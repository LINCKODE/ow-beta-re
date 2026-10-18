// Package instance is the game (instance) server: the UDP server the client
// connects to after the lobby sends ClientInLobbyInstance 20600 ("game found").
// It hangs off lobbylogic (see package gamelink).
package instance

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"sync"
	"time"

	"owserver/internal/gamelink"
	"owserver/internal/heroes"
	"owserver/internal/maps"
)

// Server is the UDP game server: it answers the handshake, keeps the connection
// alive, reads the client's JAM stream and answers its time-sync pings. SendJam
// and Send let experiments send anything else.
type Server struct {
	Log *slog.Logger

	mu      sync.Mutex
	pc      net.PacketConn
	conns   map[uint32]*Conn
	traffic []TrafficEntry
	warned  map[string]bool
	worlds  map[uint64]*World // the matches' worlds, by map (world.go)
}

// Conn is an assigned connection and, once the client showed up, its address.
type Conn struct {
	gamelink.Assignment
	Addr     string    `json:"addr,omitempty"`
	LastSeen time.Time `json:"last_seen,omitzero"`
	Packets  int       `json:"packets"`
	LastSeq  uint32    `json:"last_seq"`
	// State is the server's view of the handshake: "", "syn-received" (answered
	// the client's SYN), "connected" (got its ACK), "closed".
	State string `json:"state,omitempty"`
	// Frame is the frame number of our last data packet; InputFrame the newest
	// client command frame received.
	Frame      uint32          `json:"frame"`
	InputFrame uint32          `json:"input_frame"`
	seq        uint32          // our packet sequence number (control and data packets)
	seen       map[string]bool // JAM messages already logged at info level

	// synced is set when this process saw the client's SYN. A connection carried
	// over from before a restart (its assignment comes back in lobbylogic's hello)
	// has none: no frame numbers, hero body or acks the client expects.
	synced bool

	jamIn      uint32 // next reliable JAM seq expected from the client
	jamLog     []JamLogEntry
	lastFrame  uint32 // frame number of the last data packet we sent
	model      uint32 // the hero entity spawned on lock-in (0 = none)
	hero       uint64 // its hero (STUHero)
	heroSpawns int    // hero bodies spawned on this connection (for fresh IDs)

	inputAck    uint32 // newest client command frame received (acknowledged in stream 3)
	lastAckSent time.Time
	seenInput   bool
	setup       bool   // the client got its controller and the world (join)
	world       *World // the match's world
	jamOut      uint32 // next reliable JAM seq we send

	lastKeepalive time.Time
	recv          ackWindow // client packet seqs received, for our ack + bitmask
	addr          net.Addr
}

type TrafficEntry struct {
	Time   time.Time `json:"time"`
	Dir    string    `json:"dir"` // "in" or "out"
	Addr   string    `json:"addr"`
	Conn   uint32    `json:"conn"`
	Valid  bool      `json:"valid"` // MAC checked out
	Header uint32    `json:"header"`
	Seq    uint32    `json:"seq"`
	Data   string    `json:"data"` // the whole datagram, hex
}

const maxTraffic = 2000

func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.pc = pc
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		pc.Close()
	}()
	s.Log.Info("listening for clients (UDP)", "addr", pc.LocalAddr().String())
	buf := make([]byte, 64*1024)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		s.handle(append([]byte(nil), buf[:n]...), from)
	}
}

func (s *Server) handle(b []byte, from net.Addr) {
	p, err := ParsePacket(b)
	if err != nil {
		s.Log.Warn("bad packet", "from", from.String(), "err", err, "data", hex.EncodeToString(b))
		return
	}
	s.mu.Lock()
	c := s.conns[p.ConnID()]
	valid := c != nil && Verify(c.Key, b)
	s.record(TrafficEntry{Time: time.Now(), Dir: "in", Addr: from.String(), Conn: p.ConnID(), Valid: valid, Header: p.Header, Seq: p.Seq, Data: hex.EncodeToString(b)})
	if c == nil {
		first := !s.warned[from.String()]
		if s.warned == nil {
			s.warned = map[string]bool{}
		}
		s.warned[from.String()] = true
		s.mu.Unlock()
		if first {
			s.Log.Warn("packet for an unassigned connection (further ones from this address are only recorded)", "from", from.String(), "conn", p.ConnID(), "data", hex.EncodeToString(b))
		}
		return
	}
	if !valid {
		s.mu.Unlock()
		s.Log.Warn("bad MAC", "from", from.String(), "conn", p.ConnID(), "data", hex.EncodeToString(b))
		return
	}
	newClient := c.addr == nil || c.addr.String() != from.String()
	c.addr, c.Addr, c.LastSeen, c.LastSeq = from, from.String(), time.Now(), p.Seq
	c.recv.add(p.Seq)
	c.Packets++
	s.mu.Unlock()
	log := s.Log.With("conn", p.ConnID(), "seq", p.Seq)
	if newClient {
		log.Info("client connecting", "from", from.String(), "map", fmt.Sprintf("%#016x", c.Map), "session", c.Session)
	}
	if ctl, ok := ParseControl(b); ok {
		s.onControl(c, ctl, log)
		return
	}
	s.mu.Lock()
	stale := !c.synced && c.State != "closing" && c.State != "closed"
	s.mu.Unlock()
	if stale {
		// A real server that restarts drops its connections; do the same, so the
		// client goes back to the lobby and reconnects (adopting our frame numbers)
		// instead of talking to a server that has lost its session.
		log.Warn("data on a connection from before a restart: ending it", "from", from.String())
		s.Release(c.ConnID)
		return
	}
	s.keepalive(c, log)
	if len(b) > ControlSize { // the client drops datagrams under 34 bytes too
		s.onData(c, b[ControlSize:], log)
	}
}

// keepaliveInterval matches the client's own control resend interval
// (UdpConn_TickControl). The client drops a connected connection after 10 s
// without hearing from the server.
const keepaliveInterval = 250 * time.Millisecond

// keepalive answers the client's data packets with an ACK control packet at
// most every keepaliveInterval, so the client doesn't time the connection out.
func (s *Server) keepalive(c *Conn, log *slog.Logger) {
	s.mu.Lock()
	if time.Since(c.lastKeepalive) < keepaliveInterval {
		s.mu.Unlock()
		return
	}
	if c.State == "closing" || c.State == "closed" {
		s.mu.Unlock()
		return
	}
	c.lastKeepalive = time.Now()
	justConnected := c.State != "connected"
	if justConnected {
		log.Info("handshake", "got", "data", "state", "connected")
		c.State = "connected"
	}
	out := ControlMsg{Seq: c.seq, Ack: c.recv.ack, X: c.recv.mask, Flags: FlagACK}
	c.seq++
	s.mu.Unlock()
	if err := s.Send(c.ConnID, ControlPacket(c.Key, c.ConnID, out), false); err != nil {
		log.Error("keepalive", "err", err)
	}
	// Nothing is created yet: the map is still loading when the link comes up, and a
	// from-map create then crashes the client (game+0xC20E2D, null placed-entity
	// record). The join waits for 21602, the client's "map loaded".
}

// onControl answers the handshake (see UdpConn_HandleControlFlags in packet.go).
func (s *Server) onControl(c *Conn, in ControlMsg, log *slog.Logger) {
	log.Debug("control", "flags", FlagNames(in.Flags), "ack", in.Ack, "x", in.X)
	var reply byte
	var state string
	switch {
	case in.Flags&FlagFIN != 0:
		reply, state = FlagFIN|FlagACK, "closed"
		if in.Flags&FlagACK != 0 {
			reply = FlagACK
		}
	case in.Flags&FlagSYN != 0:
		reply, state = FlagSYN|FlagACK, "syn-received"
		// A SYN starts a new connection with fresh sequence numbers (the client
		// reuses the connection ID when it gets the same 20600 again).
		s.mu.Lock()
		c.seq, c.recv, c.lastKeepalive, c.seen, c.jamIn, c.jamOut, c.setup, c.model = 0, ackWindow{}, time.Time{}, nil, 0, 0, false, 0
		c.inputAck, c.seenInput, c.hero, c.heroSpawns = 0, false, 0, 0
		c.synced = true
		c.recv.add(in.Seq)
		s.mu.Unlock()
	case in.Flags&FlagACK != 0:
		reply, state = FlagACK, "connected"
	default:
		log.Warn("control packet without flags", "flags", in.Flags)
		return
	}
	s.mu.Lock()
	changed := c.State != state
	c.State = state
	out := ControlMsg{Seq: c.seq, Ack: c.recv.ack, X: c.recv.mask, Flags: reply}
	c.seq++
	s.mu.Unlock()
	if changed {
		log.Info("handshake", "got", FlagNames(in.Flags), "state", state, "reply", FlagNames(reply))
	}
	if err := s.Send(c.ConnID, ControlPacket(c.Key, c.ConnID, out), false); err != nil {
		log.Error("control reply", "err", err)
	}
}

// Assign adds or replaces a connection.
func (s *Server) Assign(a gamelink.Assignment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conns == nil {
		s.conns = map[uint32]*Conn{}
	}
	if old := s.conns[a.ConnID]; old != nil {
		s.leaveWorld(old)
	}
	c := &Conn{Assignment: a}
	s.conns[a.ConnID] = c
	s.Log.Info("connection assigned", "conn", a.ConnID, "map", fmt.Sprintf("%#016x", a.Map), "session", a.Session, "name", a.Name)
	// The match's world exists from its first assignment on, before any client loads.
	s.joinWorld(c)
}

// Release ends a connection (the player left the game, lobbylogic's 21801): the
// client gets a FIN (its state 3 -> 8: it answers FIN|ACK, which onControl ACKs,
// and returns to the lobby), and the connection is forgotten after
// releaseGrace.
func (s *Server) Release(connID uint32) {
	s.mu.Lock()
	c := s.conns[connID]
	if c == nil {
		s.mu.Unlock()
		return
	}
	live := c.addr != nil && c.State != "closing" && c.State != "closed"
	c.State = "closing"
	out := ControlMsg{Seq: c.seq, Ack: c.recv.ack, X: c.recv.mask, Flags: FlagFIN}
	c.seq++
	s.mu.Unlock()
	log := s.Log.With("conn", connID)
	if live {
		if err := s.Send(connID, ControlPacket(c.Key, connID, out), false); err != nil {
			log.Error("release: FIN", "err", err)
		}
		log.Info("released: sent FIN")
	}
	time.AfterFunc(releaseGrace, func() {
		s.mu.Lock()
		if s.conns[connID] == c {
			delete(s.conns, connID)
			s.leaveWorld(c)
		}
		s.mu.Unlock()
	})
}

// releaseGrace keeps a released connection around to finish the FIN exchange.
const releaseGrace = 15 * time.Second

// Assignments returns the current assignments (for the hello to lobbylogic).
func (s *Server) Assignments() []gamelink.Assignment {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]gamelink.Assignment, 0, len(s.conns))
	for _, c := range s.conns {
		out = append(out, c.Assignment)
	}
	return out
}

func (s *Server) Conns() []Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Conn, 0, len(s.conns))
	for _, c := range s.conns {
		out = append(out, *c)
	}
	return out
}

// Send sends a datagram to a connection's client. With sign, data is the packet
// after the MAC (header, seq, body) and the MAC is prepended; otherwise data is
// sent as is.
func (s *Server) Send(connID uint32, data []byte, sign bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.conns[connID]
	if c == nil {
		return errors.New("no such connection")
	}
	if c.addr == nil {
		return errors.New("the client hasn't sent anything yet")
	}
	if s.pc == nil {
		return errors.New("not listening")
	}
	if sign {
		data = Sign(c.Key, data)
	}
	p, _ := ParsePacket(data)
	s.record(TrafficEntry{Time: time.Now(), Dir: "out", Addr: c.Addr, Conn: connID, Valid: Verify(c.Key, data), Header: p.Header, Seq: p.Seq, Data: hex.EncodeToString(data)})
	_, err := s.pc.WriteTo(data, c.addr)
	return err
}

// record appends to the traffic ring; the caller holds s.mu.
func (s *Server) record(e TrafficEntry) {
	s.traffic = append(s.traffic, e)
	if len(s.traffic) > maxTraffic {
		s.traffic = s.traffic[len(s.traffic)-maxTraffic:]
	}
}

// Traffic returns the last n entries (all if n <= 0).
func (s *Server) Traffic(n int) []TrafficEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.traffic
	if n > 0 && n < len(t) {
		t = t[len(t)-n:]
	}
	return append([]TrafficEntry{}, t...)
}

// ackWindow tracks the peer's packet sequence numbers the way the client's ack
// processing reads them (FUN_7ff650e34a20): ack is the newest seq received, and
// bit i of mask means seq ack-1-i was received too. Packets the mask doesn't
// cover are reported lost to the layer above, which then resends its messages.
type ackWindow struct {
	ack  uint32
	mask uint64
	any  bool
}

func (w *ackWindow) add(seq uint32) {
	switch d := int32(seq - w.ack); {
	case !w.any:
		w.ack, w.mask, w.any = seq, 0, true
	case d > 0:
		if d >= 64 {
			w.mask = 0
		} else {
			w.mask = w.mask<<d | 1<<(d-1)
		}
		w.ack = seq
	case d < 0 && d >= -64:
		w.mask |= 1 << (-d - 1)
	}
}

var started = time.Now()

// onData handles a data packet's payload (after the 34-byte header).
func (s *Server) onData(c *Conn, payload []byte, log *slog.Logger) {
	msgs, input, err := ParseClientPayload(payload)
	if err != nil {
		log.Warn("client payload", "err", err, "payload", hex.EncodeToString(payload))
	}
	if newest, ok := input.Newest(); ok {
		s.mu.Lock()
		first := !c.seenInput
		c.seenInput = true
		if newest > c.inputAck {
			c.inputAck = newest
			c.InputFrame = newest
		}
		due := time.Since(c.lastAckSent) >= ackInterval
		s.mu.Unlock()
		if first {
			log.Info("client input started", "frame", newest, "history", len(input.Entries))
		}
		if due {
			// An otherwise empty data packet: its stream 3 carries the ack.
			if err := s.SendPacket(c.ConnID, nil, nil); err != nil {
				log.Error("input ack", "err", err)
			}
		}
	}
	for _, m := range msgs {
		id, proto := m.ID()
		s.mu.Lock()
		dup := m.Reliable && m.Seq < c.jamIn
		if m.Reliable && m.Seq >= c.jamIn {
			c.jamIn = m.Seq + 1
		}
		key := fmt.Sprint(id)
		first := !c.seen[key]
		if c.seen == nil {
			c.seen = map[string]bool{}
		}
		c.seen[key] = true
		s.mu.Unlock()
		if dup {
			continue
		}
		s.mu.Lock()
		c.jamLog = append(c.jamLog, JamLogEntry{Time: time.Now(), Msg: id, Protocol: proto, Reliable: m.Reliable, Seq: m.Seq, Payload: hex.EncodeToString(m.Payload)})
		if len(c.jamLog) > 500 {
			c.jamLog = c.jamLog[len(c.jamLog)-500:]
		}
		s.mu.Unlock()
		attrs := []any{"msg", id, "protocol", proto, "reliable", m.Reliable, "jam_seq", m.Seq, "payload", hex.EncodeToString(m.Payload)}
		if first {
			log.Info("JAM message (first of its kind; more at debug)", attrs...)
		} else {
			log.Debug("JAM message", attrs...)
		}
		if id == 21608 {
			s.pong(c, m.Payload, log)
		} else if id == 21603 && len(m.Payload) >= 8 {
			go s.lockIn(c, binary.LittleEndian.Uint64(m.Payload), log)
		} else if id == 21602 {
			// "Map loaded" (the map world's finish callback FUN_7ff650c9fc20, after the
			// placement pass): the client can take from-map creates now. The tutorial's
			// loading screen stays up until the controller and the map's networked entity
			// exist; on the other maps hero select is already open.
			s.setupOnce(c, log, "map loaded (21602)")
		}
	}
}

// pong answers a time-sync ping, ClientOutInstance 21608 {u32 n, u64[] times},
// with ClientInInstance 20304 {u32 n, u64[] times}: the client's times plus our
// receive time, our clock and our send time. The client (FUN_7ff650c45fd0)
// appends its receive time t5 and computes RTT = (t5-t1) - (t4-t2); t3 goes
// along with it (probably the server time).
func (s *Server) pong(c *Conn, ping []byte, log *slog.Logger) {
	if len(ping) < 8 {
		return
	}
	n := binary.LittleEndian.Uint32(ping)
	count := binary.LittleEndian.Uint32(ping[4:])
	if len(ping) < 8+8*int(count) {
		return
	}
	times := make([]uint64, 0, count+3)
	for i := range int(count) {
		times = append(times, binary.LittleEndian.Uint64(ping[8+8*i:]))
	}
	now := uint64(time.Since(started).Milliseconds())
	times = append(times, now, now, now)
	b := binary.LittleEndian.AppendUint32(nil, n)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(times)))
	for _, t := range times {
		b = binary.LittleEndian.AppendUint64(b, t)
	}
	if err := s.SendJam(c.ConnID, JamMsg{Reliable: true, Channel: 0, Index: 4, Payload: b}); err != nil {
		log.Error("pong", "err", err)
	}
}

// SendJam sends JAM messages to a client in one data packet. Reliable messages
// get the connection's next JAM sequence numbers.
func (s *Server) SendJam(connID uint32, msgs ...JamMsg) error {
	return s.SendPacket(connID, msgs, nil)
}

// SendPacket sends JAM messages and entity spawns in one data packet.
func (s *Server) SendPacket(connID uint32, msgs []JamMsg, spawns []Spawn) error {
	s.mu.Lock()
	c := s.conns[connID]
	if c == nil {
		s.mu.Unlock()
		return errors.New("no such connection")
	}
	for i := range msgs {
		if msgs[i].Reliable {
			msgs[i].Seq = c.jamOut
			c.jamOut++
		}
	}
	s.mu.Unlock()
	// One frame number per packet, like a server running one tick per packet.
	s.mu.Lock()
	frame := uint32(time.Since(started) / (16 * time.Millisecond))
	if frame <= c.lastFrame {
		frame = c.lastFrame + 1
	}
	c.lastFrame, c.Frame = frame, frame
	s.mu.Unlock()
	s.mu.Lock()
	ack := c.inputAck
	c.lastAckSent = time.Now()
	s.mu.Unlock()
	payload, err := EncodeDataPayload(frame, ack, msgs, spawns...)
	if err != nil {
		return err
	}
	return s.sendData(c, payload)
}

// sendData sends a data packet: the control header with flag 1 in the header
// word and ACK set, then the payload.
func (s *Server) sendData(c *Conn, payload []byte) error {
	s.mu.Lock()
	b := binary.LittleEndian.AppendUint32(nil, c.ConnID<<4|1)
	b = binary.LittleEndian.AppendUint32(b, c.seq)
	b = binary.LittleEndian.AppendUint32(b, c.recv.ack)
	b = binary.LittleEndian.AppendUint64(b, c.recv.mask)
	b = append(b, FlagACK, ControlMarker)
	c.seq++
	s.mu.Unlock()
	return s.Send(c.ConnID, append(b, payload...), true)
}

// JamLogEntry is a JAM message received from a client.
type JamLogEntry struct {
	Time     time.Time `json:"time"`
	Msg      uint16    `json:"msg"`
	Protocol string    `json:"protocol"`
	Reliable bool      `json:"reliable"`
	Seq      uint32    `json:"seq"`
	Payload  string    `json:"payload"`
}

// JamLog returns the JAM messages received on a connection, oldest first; with
// skipPing the time-sync pings (21608) are left out.
func (s *Server) JamLog(connID uint32, skipPing bool) []JamLogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.conns[connID]
	if c == nil {
		return nil
	}
	out := []JamLogEntry{}
	for _, e := range c.jamLog {
		if !skipPing || e.Msg != 21608 {
			out = append(out, e)
		}
	}
	return out
}

// Entity IDs used by setupWorld and lockIn: the controller, the hero, then the
// map's networked entities.
const (
	controllerEntity = 1
	heroEntity       = 2
	firstMapEntity   = 3
)

// heroDef returns a hero's gameplay entity definition index (STUHero.m_gameplayEntity; entity
// definitions are 0x0400000000000000 | index), from the generated hero catalogue (server/internal/heroes).
func heroDef(hero uint64) (uint32, bool) {
	h, ok := heroes.ByHero(hero)
	if !ok {
		return 0, false
	}
	return h.Def, true
}

// lockIn answers ClientOutInstance 21603 (hero select OK): it spawns the hero's
// gameplay entity and makes it the controller's body with a component 0x20
// update (aim, camera and movement attach).
func (s *Server) lockIn(c *Conn, hero uint64, log *slog.Logger) {
	def, ok := heroDef(hero)
	if !ok {
		log.Warn("lock in: unknown hero (not in the generated hero catalogue)", "hero", fmt.Sprintf("%#016x", hero))
		return
	}
	s.mu.Lock()
	if c.model != 0 && c.hero == hero {
		s.mu.Unlock()
		log.Info("lock in: already playing this hero", "hero", fmt.Sprintf("%#016x", hero))
		return
	}
	old := c.model
	// A fresh ID per body, so a new body never reuses the one being destroyed:
	// 2 for the first, then 200-249.
	id := uint8(heroEntity)
	if c.heroSpawns > 0 {
		id = uint8(200 + (c.heroSpawns-1)%50)
	}
	c.heroSpawns++
	body := Spawn{ID: id, Def: def, Flags: 0x08, Rot: [4]float32{0, 0, 0, 1}, Scale: [3]float32{1, 1, 1},
		// Health rides in the create's component list (all heroes get 200 for now).
		Components: []ComponentUpdate{PlayerHealth(200), Mirror10(TeamBit(mapPlayerTeam(c.Map)))}}
	n := c.heroSpawns - 1
	if c.world != nil {
		n = c.world.takeSpawn()
	}
	if sp, ok := startSpawn(c.Map, n); ok {
		// The movement state (0x12) overrides the create transform (docs/MatchLogic.md §8.1):
		// the snapshot is what puts the body there.
		body.Pos, body.Rot = sp.Pos, yawQuat(sp.Yaw)
		body.Mover = &Mover{Pos: sp.Pos, Yaw: sp.Yaw}
		log.Info("lock in: hero at the start room", "team", sp.Team, "spawn", fmt.Sprintf("%#x", sp.Index), "room", sp.Room, "pos", sp.Pos, "yaw", sp.Yaw)
	}
	c.model, c.hero = body.EntityID(), hero
	s.mu.Unlock()
	if old != 0 {
		// Hero change: remove the previous body first.
		if err := s.SendPacket(c.ConnID, nil, []Spawn{{ID: uint8(old & 0xff), Destroy: true}}); err != nil {
			log.Error("lock in: destroy the previous hero", "err", err)
			return
		}
		log.Info("lock in: destroyed the previous hero entity", "entity", fmt.Sprintf("%#x", old))
	}
	if err := s.SendPacket(c.ConnID, nil, []Spawn{body}); err != nil {
		log.Error("lock in: spawn hero", "err", err)
		return
	}
	// PlayerInfo's hero (0x3F +0x98) is what the hero-select exit waits for: graph 0x9D0's
	// BooleanSwitch #10 closes hero select once GetEntityHero(GetLocalPlayer()) is the picked
	// hero and the possessed body is visible; without it only its 5 s fallback Wait (#49)
	// closes it (the black screen after the confirm, docs/MatchLogic.md "Match init and the
	// join / lock-in sequence").
	link := Spawn{ID: controllerEntity, Update: true, Components: []ComponentUpdate{Mirror20(body.EntityID(), body.EntityID(), body.EntityID()),
		Mirror10(TeamBit(mapPlayerTeam(c.Map))), Mirror3F(PlayerInfo{Hero: &hero})}}
	if err := s.SendPacket(c.ConnID, nil, []Spawn{link}); err != nil {
		log.Error("lock in: link controller", "err", err)
		return
	}
	if st, ok := heroes.Start(def); ok {
		s.mu.Lock()
		frame := c.lastFrame + 1
		s.mu.Unlock()
		// Local format: the controller link above has made the body the local entity,
		// and the client drops a remote-format update's instances on its local entity.
		w, err := HeroStartupFullUpdate(frame, st)
		if err != nil {
			log.Error("lock in: hero start-up", "err", err)
			return
		}
		if err := s.SendPacket(c.ConnID, nil, []Spawn{{ID: id, Scripts: w}}); err != nil {
			log.Error("lock in: hero scripts", "err", err)
			return
		}
		active, skipped := 0, 0
		for _, in := range st.Instances {
			active += len(in.Active)
			skipped += len(in.Skipped)
		}
		log.Info("lock in: sent the hero's Statescript start-up (full update)", "hero_name", st.Hero.Name,
			"instances", len(st.Instances), "active_states", active, "inactive_start_states", skipped, "bits", w.nbit)
	}
	log.Info("lock in: spawned the hero and linked the controller to it", "hero", fmt.Sprintf("%#016x", hero), "entity", fmt.Sprintf("%#x", body.EntityID()), "def", fmt.Sprintf("%#016x", 0x0400000000000000|uint64(def)))
}

// SendScriptPayload sends a ready-made Statescript payload (e.g. from Prometheus's
// ss_serialize_local) for the hero body, as a packet covering startCF to endCF
// (start 0 = a full update, else a delta).
func (s *Server) SendScriptPayload(connID uint32, startCF, endCF uint32, payload []byte, nbits uint) error {
	s.mu.Lock()
	c := s.conns[connID]
	if c == nil {
		s.mu.Unlock()
		return errors.New("no such connection")
	}
	model := c.model
	s.mu.Unlock()
	if model == 0 {
		return errors.New("no hero body yet")
	}
	if startCF != 0 && startCF >= endCF {
		return fmt.Errorf("start CF %d must be below end CF %d", startCF, endCF)
	}
	if nbits > uint(len(payload))*8 {
		return fmt.Errorf("%d bits but only %d bytes", nbits, len(payload))
	}
	return s.SendPacket(connID, nil, []Spawn{{ID: uint8(model & 0xff), Scripts: wrapScriptPacket(startCF, endCF, &bitWriter{buf: payload, nbit: nbits})}})
}

// SendEntityScripts sends a remote Statescript full update for any networked
// entity (0x40000000|entity): instances 1..n of the given graphs (layouts from
// Prometheus's graph_sync_info), nothing else set. For server-created entities,
// which don't start their default graphs themselves (docs/MatchLogic.md).
func (s *Server) SendEntityScripts(connID uint32, entity uint8, graphs []ScriptGraph, active map[int][]ScriptState) error {
	s.mu.Lock()
	c := s.conns[connID]
	if c == nil {
		s.mu.Unlock()
		return errors.New("no such connection")
	}
	frame := c.lastFrame + 1
	s.mu.Unlock()
	return s.SendPacket(connID, nil, []Spawn{{ID: entity, Scripts: ScriptFullUpdate(frame, graphs, active)}})
}

// SendHeroGraphs sends a local Statescript full update for the connection's hero body
// with the given graphs instead of the hero's own (control API; e.g. the tutorial's
// player graph 0xB40, which the tutorial brain plays on the player).
func (s *Server) SendHeroGraphs(connID uint32, graphs []ScriptGraph, active map[int][]ScriptState) error {
	s.mu.Lock()
	c := s.conns[connID]
	if c == nil {
		s.mu.Unlock()
		return errors.New("no such connection")
	}
	model, frame := c.model, c.lastFrame+1
	s.mu.Unlock()
	if model == 0 {
		return errors.New("no hero body yet")
	}
	return s.SendPacket(connID, nil, []Spawn{{ID: uint8(model & 0xff), Scripts: ScriptLocalFullUpdate(frame, graphs, active)}})
}

// SendScripts sends a Statescript full update for the connection's hero body: its
// start-up (heroes.Start) in the local format, or its graphs in the remote one (no vars;
// active = the states to activate per instance).
func (s *Server) SendScripts(connID uint32, local bool, active map[int][]ScriptState) error {
	s.mu.Lock()
	c := s.conns[connID]
	if c == nil {
		s.mu.Unlock()
		return errors.New("no such connection")
	}
	model, hero, frame := c.model, c.hero, c.lastFrame+1
	s.mu.Unlock()
	if model == 0 {
		return errors.New("no hero body yet")
	}
	def, _ := heroDef(hero)
	st, ok := heroes.Start(def)
	if !ok {
		return fmt.Errorf("no start-up for hero %#016x", hero)
	}
	var w *bitWriter
	if local {
		if len(active) > 0 {
			return errors.New("local: the start-up sets the states; send \"graphs\" to activate others")
		}
		var err error
		if w, err = HeroStartupFullUpdate(frame, st); err != nil {
			return err
		}
	} else {
		var graphs []ScriptGraph
		for _, in := range st.Instances {
			if in.Parent == 0 {
				graphs = append(graphs, ScriptGraph{Index: uint16(in.Graph.Index), SyncVars: len(in.Graph.SyncVars), RemoteNodes: len(in.Graph.RemoteStates)})
			}
		}
		w = ScriptFullUpdate(frame, graphs, active)
	}
	return s.SendPacket(connID, nil, []Spawn{{ID: uint8(model & 0xff), Scripts: w}})
}

// playerTeam is the filter-bit team every player gets for now (experiment: the spawn
// data's attacking team is 1).
const playerTeam = 1

// mapPlayerTeam is the team players get on a map: the attackers (playerTeam), or on a map
// without an attackers' start room (the tutorial: one spawn point, team 0) the team of
// the room they spawn in.
func mapPlayerTeam(mapID uint64) int {
	if m, ok := maps.ByData(mapID); ok && len(m.StartSpawns(playerTeam)) == 0 {
		if room := m.StartSpawns(maps.TeamDefend); len(room) > 0 {
			return room[0].Team
		}
	}
	return playerTeam
}

// startSpawn picks the n-th spawn point (mod the room size) of the start room of the players'
// team on the map (docs/Maps.md "Spawn rooms"): all players are on one team for now.
func startSpawn(mapID uint64, n int) (maps.Spawn, bool) {
	m, ok := maps.ByData(mapID)
	if !ok {
		return maps.Spawn{}, false
	}
	room := m.StartSpawns(mapPlayerTeam(mapID))
	if len(room) == 0 {
		return maps.Spawn{}, false
	}
	return room[max(n, 0)%len(room)], true
}

// yawQuat is the quaternion (x, y, z, w) of a rotation by yaw radians about +Y.
func yawQuat(yaw float32) [4]float32 {
	s, c := math.Sincos(float64(yaw) / 2)
	return [4]float32{0, float32(s), 0, float32(c)}
}

// ackInterval is the most often an otherwise empty data packet is sent to
// acknowledge client input (it rides along in every data packet anyway).
const ackInterval = 50 * time.Millisecond

// Setup joins a connection to its world now (control API), unless it already has.
// The client's map must be loaded.
func (s *Server) Setup(connID uint32) error {
	s.mu.Lock()
	c := s.conns[connID]
	s.mu.Unlock()
	if c == nil {
		return errors.New("no such connection")
	}
	s.setupOnce(c, s.Log.With("conn", connID), "control API")
	return nil
}

func (s *Server) setupOnce(c *Conn, log *slog.Logger, why string) {
	s.mu.Lock()
	if c.setup || c.State != "connected" {
		s.mu.Unlock()
		return
	}
	c.setup = true
	if c.world == nil {
		s.joinWorld(c)
	}
	w := c.world
	s.mu.Unlock()
	log.Info("setup", "trigger", why)
	s.join(c, w, log)
}

// join gives a client whose map has loaded its player controller (definition
// 0x040000000000000E, spawn flags 0x38 = transform + player loader + PlayerInfo) as
// the local entity (20301), then the match's world.
func (s *Server) join(c *Conn, w *World, log *slog.Logger) {
	ctrl := Spawn{ID: controllerEntity, Def: 0x0e, Flags: 0x38, Rot: [4]float32{0, 0, 0, 1}, Scale: [3]float32{1, 1, 1}}
	if err := s.SendPacket(c.ConnID, []JamMsg{LocalEntityMsg(ctrl.EntityID())}, []Spawn{ctrl}); err != nil {
		log.Error("setup: controller", "err", err)
		return
	}
	if err := s.replicate(c, w, log); err != nil {
		log.Error("setup: world", "err", err)
		return
	}
	log.Info("setup: spawned the local controller and replicated the world", "controller", fmt.Sprintf("%#x", ctrl.EntityID()), "map", w.Name)
}
