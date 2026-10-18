package gamelink

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Client is a game server's end of the link: it dials lobbylogic, says hello,
// pings every PingInterval and hands lobbylogic's messages to Handle. It
// reconnects whenever the link drops (lobbylogic restarted, or stopped answering
// for Timeout once it had answered a ping); either side can restart.
type Client struct {
	Log   *slog.Logger
	Addr  string        // lobbylogic's instance port
	Retry time.Duration // between dial attempts; default 1 s
	// Hello builds the hello for each connection (the assignments change). Its
	// Ping is filled in.
	Hello func() Wire
	// Handle gets every assign, release and view, from the link's goroutine.
	Handle func(Wire)
	// Ping overrides PingInterval (tests).
	Ping time.Duration

	mu      sync.Mutex
	out     chan Wire // the current connection's queue (Send); nil while not connected
	dropped atomic.Uint64
}

// SendQueue is how many messages Send queues per connection before it drops.
const SendQueue = 64

// Send queues a message to lobbylogic without blocking (the game loop calls it). It
// reports false, and counts a drop, when the link is down or the queue is full.
func (c *Client) Send(w Wire) bool {
	c.mu.Lock()
	out := c.out
	c.mu.Unlock()
	if out != nil {
		select {
		case out <- w:
			return true
		default:
		}
	}
	c.dropped.Add(1)
	return false
}

// Dropped is how many Send messages were dropped.
func (c *Client) Dropped() uint64 { return c.dropped.Load() }

// Run serves the link until ctx ends.
func (c *Client) Run(ctx context.Context) error {
	retry := c.Retry
	if retry == 0 {
		retry = time.Second
	}
	waiting := false
	for ctx.Err() == nil {
		var d net.Dialer
		nc, err := d.DialContext(ctx, "tcp", c.Addr)
		if err != nil {
			if !waiting {
				c.Log.Info("waiting for lobbylogic", "addr", c.Addr)
				waiting = true
			}
			select {
			case <-ctx.Done():
			case <-time.After(retry):
			}
			continue
		}
		waiting = false
		c.Log.Info("connected to lobbylogic", "addr", c.Addr)
		err = c.serve(ctx, nc)
		nc.Close()
		if ctx.Err() == nil {
			c.Log.Warn("lost lobbylogic; assigned connections are kept", "err", err)
		}
	}
	return nil
}

func (c *Client) serve(ctx context.Context, nc net.Conn) error {
	ping := c.Ping
	if ping == 0 {
		ping = PingInterval
	}
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	context.AfterFunc(lctx, func() { nc.Close() })

	var mu sync.Mutex // the hello and the pinger write
	enc := json.NewEncoder(nc)
	send := func(w Wire) error {
		mu.Lock()
		defer mu.Unlock()
		nc.SetWriteDeadline(time.Now().Add(3 * ping))
		return enc.Encode(w)
	}
	hello := c.Hello()
	hello.Type = WireHello
	hello.Ping = int(ping / time.Millisecond)
	if err := send(hello); err != nil {
		return err
	}
	out := make(chan Wire, SendQueue)
	c.mu.Lock()
	c.out = out
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.out = nil
		c.mu.Unlock()
	}()
	go func() {
		for {
			select {
			case <-lctx.Done():
				return
			case w := <-out:
				if err := send(w); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	go func() {
		t := time.NewTicker(ping)
		defer t.Stop()
		for {
			select {
			case <-lctx.Done():
				return
			case <-t.C:
				if err := send(Wire{Type: WirePing}); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	sc := bufio.NewScanner(nc)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	answered := false // lobbylogic answers pings: its silence counts from now on
	for sc.Scan() {
		var m Wire
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			return fmt.Errorf("bad message from lobbylogic: %w", err)
		}
		switch {
		case m.Type == WirePong:
			answered = true
		case (m.Type == WireAssign || m.Type == WireRelease || m.Type == WireView) && m.Conn != nil:
			c.Handle(m)
		default:
			c.Log.Warn("unknown message from lobbylogic", "type", m.Type)
		}
		if answered {
			nc.SetReadDeadline(time.Now().Add(3 * ping))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return fmt.Errorf("connection closed")
}
