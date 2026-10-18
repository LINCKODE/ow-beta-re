// Package link is the instance server's UDP transport: the connections lobbylogic
// assigns (ID + key, from 20600), the handshake (SYN, SYN|ACK, ACK; FIN), packet
// sequence numbers and acknowledgements, keepalive and timeouts. It hands data
// payloads (package wire) up through Handler and reports which of our packets the
// client acknowledged or lost, so the layer above can resend reliable JAM messages
// (Channel).
//
// The handshake works like TCP. The client sends SYN while connecting (every
// 250 ms, giving up after 10 s), answers SYN|ACK by going "connected" (then ACK
// every 250 ms as a keepalive), and FIN|ACK when closing; a FIN from us sends it
// back to the lobby (its state 3 -> 8).
package link

import (
	"context"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"slices"
	"sync"
	"time"
)

// State is a connection's handshake state.
type State string

const (
	StateAssigned  State = "assigned"     // waiting for the client's SYN
	StateSynRecv   State = "syn-received" // answered SYN with SYN|ACK
	StateConnected State = "connected"    // got its ACK or data
	StateClosing   State = "closing"      // sent FIN
	StateClosed    State = "closed"
)

// Handler receives the link's events. Calls come from the link's goroutines; a
// handler hands them to its own loop.
type Handler interface {
	// Connected: the client completed the handshake (also after a reconnect: a SYN
	// on a known connection starts it afresh, with new sequence numbers).
	Connected(id uint32)
	// Data: a data packet's payload, received at at. acked / lost are our packet
	// seqs the peer's ack newly covers, or that it will not get any more.
	Data(id uint32, payload []byte, at time.Time, acked, lost []uint32)
	// Closed: the connection ended (FIN exchange, timeout, or Release).
	Closed(id uint32, why string)
}

// Config tunes the link; the zero value takes the client's own timings.
type Config struct {
	Keepalive time.Duration // idle send interval (default 250 ms, the client's)
	Timeout   time.Duration // drop a silent connection (default 10 s, the client's)
	Grace     time.Duration // keep a closed connection for late packets (default 15 s)
	Traffic   int           // datagrams kept for the control API (default 2000)
}

func (c *Config) defaults() {
	if c.Keepalive == 0 {
		c.Keepalive = 250 * time.Millisecond
	}
	if c.Timeout == 0 {
		c.Timeout = 10 * time.Second
	}
	if c.Grace == 0 {
		c.Grace = 15 * time.Second
	}
	if c.Traffic == 0 {
		c.Traffic = 2000
	}
}

// Link is the UDP socket and its connections.
type Link struct {
	log *slog.Logger
	cfg Config
	h   Handler

	mu      sync.Mutex
	pc      net.PacketConn
	conns   map[uint32]*conn
	traffic []Datagram
	warned  map[string]bool
}

type conn struct {
	id     uint32
	key    []byte
	addr   net.Addr
	state  State
	synced bool // this process saw the client's SYN (else: from before a restart)

	seq      uint32 // our next packet seq
	recv     window // the peer's seqs
	lastIn   time.Time
	lastOut  time.Time
	packets  int
	closedAt time.Time

	peerAck  uint32
	peerMask uint64
	inFlight map[uint32]bool // our data packets not acked or lost yet
}

// Datagram is a recorded datagram (control API).
type Datagram struct {
	Time  time.Time `json:"time"`
	Dir   string    `json:"dir"` // "in" or "out"
	Addr  string    `json:"addr"`
	Conn  uint32    `json:"conn"`
	Valid bool      `json:"valid"`
	Seq   uint32    `json:"seq"`
	Data  string    `json:"data"` // hex
}

func New(log *slog.Logger, h Handler, cfg Config) *Link {
	cfg.defaults()
	return &Link{log: log, cfg: cfg, h: h, conns: map[uint32]*conn{}, warned: map[string]bool{}}
}

// Listen opens the UDP socket. Serve reads from it.
func (l *Link) Listen(addr string) error {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.pc = pc
	l.mu.Unlock()
	l.log.Info("listening for clients (UDP)", "addr", pc.LocalAddr().String())
	return nil
}

// Addr is the socket's local address.
func (l *Link) Addr() net.Addr {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pc == nil {
		return nil
	}
	return l.pc.LocalAddr()
}

// Serve reads datagrams and runs keepalives and timeouts until ctx ends.
func (l *Link) Serve(ctx context.Context) error {
	l.mu.Lock()
	pc := l.pc
	l.mu.Unlock()
	if pc == nil {
		return errors.New("link: Listen first")
	}
	context.AfterFunc(ctx, func() { pc.Close() })
	go l.housekeeping(ctx)
	buf := make([]byte, 64*1024)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		l.handle(append([]byte(nil), buf[:n]...), from, time.Now())
	}
}

// Assign adds (or replaces) a connection the client will open with id and key.
func (l *Link) Assign(id uint32, key []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.conns[id] = &conn{id: id, key: append([]byte(nil), key...), state: StateAssigned}
}

// Release ends a connection: a FIN if the client is there, then it is forgotten
// after the grace period.
func (l *Link) Release(id uint32) {
	l.mu.Lock()
	c := l.conns[id]
	if c == nil {
		l.mu.Unlock()
		return
	}
	live := c.addr != nil && c.state != StateClosing && c.state != StateClosed
	c.state, c.closedAt = StateClosing, time.Now()
	l.mu.Unlock()
	if live {
		l.sendControl(c, FlagFIN)
		l.log.Info("released: sent FIN", "conn", id)
	}
	l.h.Closed(id, "released")
}

// ConnInfo is a connection as the control API shows it.
type ConnInfo struct {
	ID       uint32    `json:"conn_id"`
	Addr     string    `json:"addr,omitempty"`
	State    State     `json:"state"`
	Packets  int       `json:"packets"`
	LastSeen time.Time `json:"last_seen,omitzero"`
	InFlight int       `json:"in_flight"`
}

func (l *Link) Conns() []ConnInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]ConnInfo, 0, len(l.conns))
	for _, c := range l.conns {
		ci := ConnInfo{ID: c.id, State: c.state, Packets: c.packets, LastSeen: c.lastIn, InFlight: len(c.inFlight)}
		if c.addr != nil {
			ci.Addr = c.addr.String()
		}
		out = append(out, ci)
	}
	return out
}

// Connected reports whether id has completed the handshake.
func (l *Link) Connected(id uint32) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.conns[id]
	return c != nil && c.state == StateConnected
}

// Traffic returns the last n recorded datagrams (all if n <= 0).
func (l *Link) Traffic(n int) []Datagram {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.traffic
	if n > 0 && n < len(t) {
		t = t[len(t)-n:]
	}
	return append([]Datagram{}, t...)
}

func (l *Link) record(d Datagram) {
	l.traffic = append(l.traffic, d)
	if len(l.traffic) > l.cfg.Traffic {
		l.traffic = l.traffic[len(l.traffic)-l.cfg.Traffic:]
	}
}

func (l *Link) handle(b []byte, from net.Addr, at time.Time) {
	id, _ := ConnIDOf(b)
	h, perr := ParseHeader(b)
	l.mu.Lock()
	c := l.conns[id]
	valid := c != nil && Verify(c.key, b)
	l.record(Datagram{Time: at, Dir: "in", Addr: from.String(), Conn: id, Valid: valid, Seq: h.Seq, Data: hex.EncodeToString(b)})
	if c == nil {
		first := !l.warned[from.String()]
		l.warned[from.String()] = true
		l.mu.Unlock()
		if first {
			l.log.Warn("datagram for an unassigned connection (more from this address are only recorded)", "from", from.String(), "conn", id)
		}
		return
	}
	if perr != nil || !valid {
		l.mu.Unlock()
		l.log.Warn("bad datagram", "from", from.String(), "conn", id, "err", perr, "mac_ok", valid)
		return
	}
	if c.addr == nil || c.addr.String() != from.String() {
		l.log.Info("client connecting", "conn", id, "from", from.String())
	}
	c.addr, c.lastIn = from, at
	c.packets++
	log := l.log.With("conn", id, "seq", h.Seq)

	if !h.Data() {
		l.mu.Unlock()
		l.onControl(c, h, log)
		return
	}
	if c.state == StateClosing || c.state == StateClosed {
		l.mu.Unlock()
		return
	}
	if !c.synced {
		// A connection from before a restart: end it, so the client goes back to the
		// lobby and reconnects with fresh state instead of talking to a server that
		// lost its session.
		l.mu.Unlock()
		log.Warn("data on a connection from before a restart: ending it")
		l.Release(id)
		return
	}
	c.recv.add(h.Seq)
	justConnected := c.state != StateConnected
	c.state = StateConnected
	ack, lost := c.peerAcks(h.Ack, h.Mask)
	l.mu.Unlock()
	if justConnected {
		log.Info("handshake", "got", "data", "state", StateConnected)
		l.h.Connected(id)
	}
	l.h.Data(id, b[HeaderSize:], at, ack, lost)
}

// reorderSlack: a packet this far behind the peer's newest ack and not in its mask
// counts as lost.
const reorderSlack = 3

// peerAcks updates the peer's ack of our packets and returns our in-flight data
// packets it newly acknowledged or that count as lost; the caller holds l.mu.
func (c *conn) peerAcks(ack uint32, mask uint64) (acks, lost []uint32) {
	if int32(ack-c.peerAck) < 0 {
		return nil, nil // an older packet: nothing new
	}
	c.peerAck, c.peerMask = ack, mask
	for s := range c.inFlight {
		switch {
		case acked(ack, mask, s):
			acks = append(acks, s)
			delete(c.inFlight, s)
		case int32(ack-s) >= reorderSlack:
			lost = append(lost, s)
			delete(c.inFlight, s)
		}
	}
	slices.Sort(acks)
	slices.Sort(lost)
	return acks, lost
}

func (l *Link) onControl(c *conn, h Header, log *slog.Logger) {
	var reply byte
	var state State
	l.mu.Lock()
	switch {
	case h.Flags&FlagFIN != 0:
		reply, state = FlagFIN|FlagACK, StateClosed
		if h.Flags&FlagACK != 0 {
			reply = FlagACK
		}
		c.recv.add(h.Seq)
	case h.Flags&FlagSYN != 0:
		// A SYN starts the connection afresh (the client reuses the connection ID when
		// it gets the same 20600 again).
		reply, state = FlagSYN|FlagACK, StateSynRecv
		c.seq, c.recv, c.peerAck, c.peerMask, c.inFlight = 0, window{}, 0, 0, map[uint32]bool{}
		c.synced = true
		c.recv.add(h.Seq)
	case h.Flags&FlagACK != 0:
		reply, state = FlagACK, StateConnected
		c.recv.add(h.Seq)
		if c.state == StateClosing || c.state == StateClosed {
			state = c.state // the ACK of our FIN
			reply = 0
		}
	default:
		l.mu.Unlock()
		log.Warn("control packet without flags", "flags", h.Flags)
		return
	}
	old := c.state
	c.state = state
	if state == StateClosed && c.closedAt.IsZero() {
		c.closedAt = time.Now()
	}
	l.mu.Unlock()
	if old != state {
		log.Info("handshake", "got", FlagNames(h.Flags), "state", state, "reply", FlagNames(reply))
	}
	if reply != 0 {
		l.sendControl(c, reply)
	}
	switch {
	case state == StateConnected && old != StateConnected:
		l.h.Connected(c.id)
	case state == StateClosed && old != StateClosed && old != StateClosing:
		l.h.Closed(c.id, "client FIN")
	}
}

func (l *Link) sendControl(c *conn, flags byte) {
	l.mu.Lock()
	h := Header{ConnID: c.id, Seq: c.seq, Ack: c.recv.ack, Mask: c.recv.mask, Flags: flags}
	c.seq++
	l.mu.Unlock()
	if err := l.write(c, AppendHeader(nil, h), h.Seq); err != nil {
		l.log.Error("control packet", "conn", c.id, "err", err)
	}
}

// ErrNotConnected is SendData's error for a connection without a client.
var ErrNotConnected = errors.New("link: not connected")

// SendData sends a data packet with payload and returns its seq (for the acked /
// lost reports in Handler.Data).
func (l *Link) SendData(id uint32, payload []byte) (uint32, error) {
	l.mu.Lock()
	c := l.conns[id]
	if c == nil || c.addr == nil || c.state != StateConnected {
		l.mu.Unlock()
		return 0, ErrNotConnected
	}
	h := Header{ConnID: id, Kind: kindData, Seq: c.seq, Ack: c.recv.ack, Mask: c.recv.mask, Flags: FlagACK}
	c.seq++
	c.inFlight[h.Seq] = true
	l.mu.Unlock()
	return h.Seq, l.write(c, append(AppendHeader(nil, h), payload...), h.Seq)
}

func (l *Link) write(c *conn, unsigned []byte, seq uint32) error {
	b := Sign(c.key, unsigned)
	l.mu.Lock()
	pc, addr := l.pc, c.addr
	c.lastOut = time.Now()
	if addr != nil {
		l.record(Datagram{Time: c.lastOut, Dir: "out", Addr: addr.String(), Conn: c.id, Valid: true, Seq: seq, Data: hex.EncodeToString(b)})
	}
	l.mu.Unlock()
	if pc == nil || addr == nil {
		return ErrNotConnected
	}
	_, err := pc.WriteTo(b, addr)
	return err
}

// housekeeping sends keepalive ACKs on idle connections, drops silent ones and
// forgets closed ones.
func (l *Link) housekeeping(ctx context.Context) {
	t := time.NewTicker(l.cfg.Keepalive / 2)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			var idle []*conn
			var dead []uint32
			l.mu.Lock()
			for id, c := range l.conns {
				switch {
				case (c.state == StateClosing || c.state == StateClosed) && now.Sub(c.closedAt) > l.cfg.Grace:
					delete(l.conns, id)
				case c.state == StateConnected && now.Sub(c.lastIn) > l.cfg.Timeout:
					c.state, c.closedAt = StateClosed, now
					dead = append(dead, id)
				case c.state == StateConnected && now.Sub(c.lastOut) >= l.cfg.Keepalive:
					idle = append(idle, c)
				}
			}
			l.mu.Unlock()
			for _, c := range idle {
				l.sendControl(c, FlagACK)
			}
			for _, id := range dead {
				l.log.Warn("connection timed out", "conn", id)
				l.h.Closed(id, "timeout")
			}
		}
	}
}
