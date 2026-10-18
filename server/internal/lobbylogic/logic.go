// Package lobbylogic is the lobby's game logic. It runs as its own process
// (lobbylogic) connected to the lobby gateway (lobbyserver), which keeps the
// client connection up, so this process can be rebuilt and restarted at will.
//
// Handlers are registered by message ID in handlers.go.
package lobbylogic

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"owserver/inst/jam"
	"owserver/internal/lobby"
)

// Ctx is one received client message.
type Ctx struct {
	Log      *slog.Logger
	Session  *lobby.SessionInfo
	Protocol string
	Msg      uint16
	Payload  []byte
	// State is this client's server-side state (friends, party). It lives in the
	// logic process, so restarting lobbylogic resets it (the client keeps its copy).
	State *SessionState
	logic *Logic
}

// Send sends a message to this message's client.
func (c *Ctx) Send(protocol string, msgID uint16, payload []byte) error {
	return c.logic.Send(c.Session.ID, protocol, msgID, payload)
}

// SendMsg sends a generated JAM message (package jam) to this message's client.
func (c *Ctx) SendMsg(m jam.Message) error {
	p, ok := jam.ProtocolOf(m.ID())
	if !ok {
		return fmt.Errorf("message %d has no protocol", m.ID())
	}
	return c.logic.Send(c.Session.ID, p.Name, m.ID(), jam.Marshal(m))
}

// ClientJamID is the client's JamID from the handshake trailer.
func (c *Ctx) ClientJamID() []byte {
	b, _ := hex.DecodeString(c.Session.ClientJamID)
	return b
}

type Handler func(c *Ctx) error

type Logic struct {
	Log      *slog.Logger
	Handlers map[uint16]Handler
	// Resync runs for every live session when the logic (re)connects to the gateway:
	// the client still holds state from the previous logic process, this one doesn't.
	Resync Handler
	// Instances is the game server link; nil disables game assignment.
	Instances *Instances

	mu       sync.Mutex
	enc      *json.Encoder
	sessions map[uint64]*lobby.SessionInfo
	states   map[uint64]*SessionState
}

// Run connects to the gateway and serves it until ctx is cancelled,
// reconnecting whenever the connection drops.
func (l *Logic) Run(ctx context.Context, addr string) error {
	waiting := false
	for ctx.Err() == nil {
		var d net.Dialer
		nc, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			if !waiting {
				l.Log.Info("waiting for the lobby gateway", "addr", addr)
				waiting = true
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
			continue
		}
		waiting = false
		l.Log.Info("connected to the lobby gateway", "addr", addr)
		go func() {
			<-ctx.Done()
			nc.Close()
		}()
		err = l.serve(nc)
		nc.Close()
		if ctx.Err() == nil {
			l.Log.Warn("lost the lobby gateway", "err", err)
		}
	}
	return nil
}

func (l *Logic) serve(nc net.Conn) error {
	l.mu.Lock()
	l.enc = json.NewEncoder(nc)
	l.sessions = map[uint64]*lobby.SessionInfo{}
	l.mu.Unlock()

	sc := bufio.NewScanner(nc)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() {
		var m lobby.WireMessage
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			return fmt.Errorf("bad message from gateway: %w", err)
		}
		l.handle(m)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return fmt.Errorf("connection closed")
}

func (l *Logic) handle(m lobby.WireMessage) {
	switch m.Type {
	case lobby.WireHello:
		l.mu.Lock()
		for i := range m.Sessions {
			l.sessions[m.Sessions[i].ID] = &m.Sessions[i]
		}
		l.mu.Unlock()
		l.Log.Info("gateway hello", "live_sessions", len(m.Sessions))
		if l.Resync != nil {
			for i := range m.Sessions {
				sess := &m.Sessions[i]
				st := newSessionState()
				l.mu.Lock()
				if l.states == nil {
					l.states = map[uint64]*SessionState{}
				}
				l.states[sess.ID] = st
				l.mu.Unlock()
				c := &Ctx{Log: l.Log.With("session", sess.ID), Session: sess, State: st, logic: l}
				if err := l.Resync(c); err != nil {
					c.Log.Error("resync failed", "err", err)
				}
			}
		}
	case lobby.WireOpen:
		l.mu.Lock()
		l.sessions[m.Session] = m.Info
		l.mu.Unlock()
		l.Log.Info("client link up", "session", m.Session, "cid", m.Info.BnetCID)
	case lobby.WireClose:
		l.mu.Lock()
		delete(l.sessions, m.Session)
		delete(l.states, m.Session)
		l.mu.Unlock()
		l.Log.Info("client gone", "session", m.Session)
		if l.Instances != nil && l.Instances.Viewing(m.Session) {
			l.Instances.SetView(m.Session, false) // live server view (serverview.go)
		}
	case lobby.WireRecv:
		l.mu.Lock()
		sess := l.sessions[m.Session]
		l.mu.Unlock()
		log := l.Log.With("session", m.Session, "msg", m.Msg)
		if sess == nil {
			log.Warn("message for an unknown session")
			return
		}
		h := l.Handlers[m.Msg]
		if h == nil {
			log.Info("unhandled", "protocol", m.Protocol, "bytes", len(m.Payload), "data", hex.EncodeToString(m.Payload))
			return
		}
		log.Info("handling", "protocol", m.Protocol, "bytes", len(m.Payload))
		c := &Ctx{Log: log, Session: sess, Protocol: m.Protocol, Msg: m.Msg, Payload: m.Payload, State: l.state(m.Session), logic: l}
		if err := h(c); err != nil {
			log.Error("handler failed", "err", err)
		}
	default:
		l.Log.Warn("unknown message from gateway", "type", m.Type)
	}
}

// state returns a session's state, created on first use.
func (l *Logic) state(session uint64) *SessionState {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.states == nil {
		l.states = map[uint64]*SessionState{}
	}
	st := l.states[session]
	if st == nil {
		st = newSessionState()
		l.states[session] = st
	}
	return st
}

// ctxFor is a context for sending to a session outside a message handler.
func (l *Logic) ctxFor(sess *lobby.SessionInfo) *Ctx {
	return &Ctx{Log: l.Log.With("session", sess.ID), Session: sess, State: l.state(sess.ID), logic: l}
}

// liveSessions returns the connected clients the logic knows about.
func (l *Logic) liveSessions() []*lobby.SessionInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]*lobby.SessionInfo, 0, len(l.sessions))
	for _, s := range l.sessions {
		out = append(out, s)
	}
	return out
}

// Send asks the gateway to send a message to a client. An empty protocol is
// looked up from the message ID.
func (l *Logic) Send(session uint64, protocol string, msgID uint16, payload []byte) error {
	if protocol == "" {
		p := lobby.ProtocolByMsg(msgID)
		if p == nil {
			return fmt.Errorf("no protocol contains message %d", msgID)
		}
		protocol = p.Name
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.enc == nil {
		return fmt.Errorf("not connected to the gateway")
	}
	return l.enc.Encode(lobby.WireMessage{Type: lobby.WireSend, Session: session, Protocol: protocol, Msg: msgID, Payload: payload})
}
