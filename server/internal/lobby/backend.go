package lobby

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
)

// ServeBackend accepts logic backends (lobbylogic) on addr until ctx is
// cancelled. Only one backend is active: a new connection replaces the old one.
func (h *Hub) ServeBackend(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	h.log.Info("listening for the logic backend", "addr", addr)
	for {
		nc, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go h.serveBackend(nc)
	}
}

func (h *Hub) serveBackend(nc net.Conn) {
	b := &backendConn{conn: nc, out: make(chan WireMessage, 1024), done: make(chan struct{})}
	go b.writeLoop()
	defer b.close()

	sessions := h.Sessions()
	h.mu.Lock()
	if h.backend != nil {
		h.log.Warn("a new logic backend connected; dropping the old one")
		h.backend.close()
	}
	h.backend = b
	var live []SessionInfo
	for _, s := range sessions {
		if s.Negotiated {
			live = append(live, s)
		}
	}
	b.enqueue(WireMessage{Type: WireHello, Sessions: live})
	h.mu.Unlock()
	h.log.Info("logic backend connected", "remote", nc.RemoteAddr().String(), "sessions", len(live))

	sc := bufio.NewScanner(nc)
	sc.Buffer(make([]byte, 64*1024), 4*maxFrame)
	for sc.Scan() {
		var m WireMessage
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			h.log.Error("backend: bad message", "err", err)
			continue
		}
		switch m.Type {
		case WireSend:
			if err := h.Send(m.Session, m.Protocol, m.Msg, m.Payload, "logic"); err != nil {
				h.log.Error("backend send", "session", m.Session, "msg", m.Msg, "err", err)
			}
		case WireDisconnect:
			if err := h.Disconnect(m.Session); err != nil {
				h.log.Error("backend disconnect", "session", m.Session, "err", err)
			}
		default:
			h.log.Warn("backend: unknown message type", "type", m.Type)
		}
	}
	h.mu.Lock()
	if h.backend == b {
		h.backend = nil
		h.log.Warn("logic backend disconnected; client links stay up")
	}
	h.mu.Unlock()
}

type backendConn struct {
	conn net.Conn
	out  chan WireMessage
	done chan struct{}
	once sync.Once
}

// enqueue queues a message without blocking; the caller holds Hub.mu, so
// messages keep the order they were produced in.
func (b *backendConn) enqueue(m WireMessage) {
	select {
	case b.out <- m:
	case <-b.done:
	default:
		b.conn.Close() // backend is stuck; drop it rather than block the clients
	}
}

func (b *backendConn) writeLoop() {
	enc := json.NewEncoder(b.conn)
	for {
		select {
		case m := <-b.out:
			if err := enc.Encode(m); err != nil {
				if !errors.Is(err, net.ErrClosed) {
					b.conn.Close()
				}
				return
			}
		case <-b.done:
			return
		}
	}
}

func (b *backendConn) close() {
	b.once.Do(func() { close(b.done) })
	b.conn.Close()
}
