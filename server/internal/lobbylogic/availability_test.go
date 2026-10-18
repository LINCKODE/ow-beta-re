package lobbylogic

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"owserver/internal/gamelink"
	"owserver/internal/instance"
	"owserver/internal/lobby"
)

type availLog struct {
	mu  sync.Mutex
	got []bool
}

func (a *availLog) on(v bool, _ string) { a.mu.Lock(); a.got = append(a.got, v); a.mu.Unlock() }
func (a *availLog) list() []bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]bool(nil), a.got...)
}

func freeAddr(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// Online is reported at once, a restart shorter than the debounce not at all, a longer absence as offline.
func TestAvailabilityDebounce(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	av := &availLog{}
	in := &Instances{Log: quiet, Debounce: 300 * time.Millisecond, OnAvailable: av.on}
	go in.Serve(ctx, addr)

	// Nothing linked: the first report (offline) comes after the debounce.
	waitFor(t, "the first report", func() bool { return len(av.list()) == 1 })
	if a, known := in.Available(); a || !known || av.list()[0] {
		t.Fatalf("first report %v", av.list())
	}

	run := func() context.CancelFunc {
		gctx, stop := context.WithCancel(ctx)
		go (&instance.Server{Log: quiet}).RunLink(gctx, addr, "127.0.0.1:26500")
		return stop
	}
	stop := run()
	waitFor(t, "online", func() bool { a, _ := in.Available(); return a })
	// A restart quicker than the debounce: no offline report.
	stop()
	stop = run()
	time.Sleep(500 * time.Millisecond)
	if got := av.list(); len(got) != 2 || !got[1] {
		t.Fatalf("after a quick restart: %v", got)
	}
	// Gone for longer than the debounce: offline.
	stop()
	waitFor(t, "offline", func() bool { got := av.list(); return len(got) == 3 && !got[2] })
	// Switching the active server to one that is linked turns it back on.
	stop = run()
	defer stop()
	waitFor(t, "online again", func() bool { got := av.list(); return len(got) == 4 && got[3] })
	in.SetActive("other")
	waitFor(t, "offline: other isn't linked", func() bool { got := av.list(); return len(got) == 5 && !got[4] })
}

// A server whose hello announces pings and then goes silent is dropped; one without pings (old hello) is kept.
func TestHeartbeatTimeout(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := &Instances{Log: quiet, Debounce: time.Hour}
	go in.Serve(ctx, addr)

	dial := func(hello gamelink.Wire) net.Conn {
		var nc net.Conn
		waitFor(t, "lobbylogic", func() bool {
			var err error
			nc, err = net.Dial("tcp", addr)
			return err == nil
		})
		json.NewEncoder(nc).Encode(hello)
		return nc
	}
	connected := func(name string) bool {
		for _, s := range in.Servers() {
			if s.Name == name {
				return s.Connected
			}
		}
		return false
	}
	old := dial(gamelink.Wire{Type: gamelink.WireHello, Addr: "127.0.0.1:1", Name: "old"})
	defer old.Close()
	hung := dial(gamelink.Wire{Type: gamelink.WireHello, Addr: "127.0.0.1:2", Name: "hung", Ping: 50})
	defer hung.Close()
	waitFor(t, "both linked", func() bool { return connected("old") && connected("hung") })

	// hung answers pings with pongs, then stops pinging.
	json.NewEncoder(hung).Encode(gamelink.Wire{Type: gamelink.WirePing})
	buf := make([]byte, 64)
	hung.SetReadDeadline(time.Now().Add(time.Second))
	if n, err := hung.Read(buf); err != nil || string(buf[:n]) != "{\"type\":\"pong\"}\n" {
		t.Fatalf("pong: %q %v", buf[:n], err)
	}
	waitFor(t, "hung dropped", func() bool { return !connected("hung") })
	time.Sleep(200 * time.Millisecond)
	if !connected("old") {
		t.Fatal("a server without pings must not time out")
	}
}

// What the client gets: nothing while the availability is unknown, the empty party while
// offline, the party of one while online, pushed to every session with a chat line.
func TestAvailabilityPartyState(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	var out bytes.Buffer
	in := &Instances{Log: quiet}
	l := &Logic{Log: quiet, Instances: in, enc: json.NewEncoder(&out),
		sessions: map[uint64]*lobby.SessionInfo{7: {ID: 7, ClientJamID: "13326046000000571dfcb3e14d341046"}}}
	sent := func() []lobby.WireMessage {
		var ms []lobby.WireMessage
		dec := json.NewDecoder(&out)
		for {
			var m lobby.WireMessage
			if dec.Decode(&m) != nil {
				return ms
			}
			ms = append(ms, m)
		}
	}
	c := l.ctxFor(l.sessions[7])
	if err := c.sendParty(); err != nil || len(sent()) != 0 {
		t.Fatal("unknown availability: nothing must be sent")
	}
	in.reported = availOff
	c.sendParty()
	if ms := sent(); len(ms) != 1 || ms[0].Msg != 20700 || !bytes.Equal(ms[0].Payload, noParty()) {
		t.Fatalf("offline: %+v", ms)
	}
	in.reported = availOn
	l.AvailabilityChanged(true, "instance")
	ms := sent()
	if len(ms) != 2 || ms[0].Msg != 20700 || ms[0].Session != 7 || len(ms[0].Payload) <= 20 || ms[1].Msg != 20400 {
		t.Fatalf("online push: %+v", ms)
	}
}
