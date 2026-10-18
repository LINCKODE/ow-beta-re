package lobbylogic

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"owserver/internal/gamelink"
	"owserver/internal/instance"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestInstancesAssignAndReplay(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := &Instances{Log: quiet}
	go in.Serve(ctx, addr)

	gsCtx, gsStop := context.WithCancel(ctx)
	gs := &instance.Server{Log: quiet}
	go gs.RunLink(gsCtx, addr, "127.0.0.1:26500")

	var a1 struct{ conn uint32 }
	waitFor(t, "the game server", func() bool {
		a, udp, err := in.Assign(1, "p", DefaultMap)
		if err != nil {
			return false
		}
		if udp != "127.0.0.1:26500" || len(a.Key) != 20 {
			t.Fatalf("assignment %+v at %s", a, udp)
		}
		a1.conn = a.ConnID
		return true
	})
	waitFor(t, "the assignment", func() bool { return len(gs.Assignments()) == 1 })

	// A restarted game server gets the assignment back.
	gsStop()
	gs2 := &instance.Server{Log: quiet}
	go gs2.RunLink(ctx, addr, "127.0.0.1:26500")
	waitFor(t, "the replay", func() bool {
		c := gs2.Assignments()
		return len(c) == 1 && c[0].ConnID == a1.conn
	})
}

func TestReleaseSession(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := &Instances{Log: quiet}
	go in.Serve(ctx, addr)
	gs := &instance.Server{Log: quiet}
	go gs.RunLink(ctx, addr, "127.0.0.1:26500")

	var conn uint32
	waitFor(t, "the game server", func() bool {
		a, _, err := in.Assign(7, "p", DefaultMap)
		conn = a.ConnID
		return err == nil
	})
	in.Assign(8, "other", DefaultMap)
	waitFor(t, "the assignments", func() bool { return len(gs.Conns()) == 2 })
	n, err := in.ReleaseSession(7)
	if err != nil || n != 1 {
		t.Fatalf("released %d, %v", n, err)
	}
	// The game server marks it closing (and forgets it after the grace period).
	waitFor(t, "the release", func() bool {
		for _, c := range gs.Conns() {
			if c.ConnID == conn {
				return c.State == "closing"
			}
		}
		return false
	})
}

// Two game servers linked at once (the R&D server, whose hello names none, and the
// instance server): new matches go to the active one, releases to the holder.
func TestNamedGameServers(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := &Instances{Log: quiet}
	go in.Serve(ctx, addr)

	rnd := &instance.Server{Log: quiet}
	go rnd.RunLink(ctx, addr, "127.0.0.1:26500")
	// The instance server, by hand: hello with its name, then read its assignments.
	var nc net.Conn
	waitFor(t, "lobbylogic", func() bool {
		var err error
		nc, err = net.Dial("tcp", addr)
		return err == nil
	})
	defer nc.Close()
	json.NewEncoder(nc).Encode(gamelink.Wire{Type: gamelink.WireHello, Addr: "127.0.0.1:26600", Name: "instance"})
	got := make(chan gamelink.Wire, 8)
	go func() {
		sc := bufio.NewScanner(nc)
		for sc.Scan() {
			var m gamelink.Wire
			json.Unmarshal(sc.Bytes(), &m)
			got <- m
		}
	}()

	waitFor(t, "both servers", func() bool { return len(in.Servers()) == 2 && in.Servers()[0].Connected && in.Servers()[1].Connected })
	a, udp, err := in.Assign(1, "p", DefaultMap)
	if err != nil || udp != "127.0.0.1:26500" {
		t.Fatalf("default goes to the R&D server: %s %v", udp, err)
	}
	waitFor(t, "the R&D assignment", func() bool { return len(rnd.Assignments()) == 1 && rnd.Assignments()[0].ConnID == a.ConnID })

	in.SetActive("instance")
	b, udp, err := in.Assign(2, "p", DefaultMap)
	if err != nil || udp != "127.0.0.1:26600" || b.ConnID == a.ConnID {
		t.Fatalf("instance: %s %v (conn %d after %d)", udp, err, b.ConnID, a.ConnID)
	}
	if m := <-got; m.Type != gamelink.WireAssign || m.Conn.ConnID != b.ConnID {
		t.Fatalf("instance got %+v", m)
	}
	if n, err := in.ReleaseSession(2); n != 1 || err != nil {
		t.Fatalf("release: %d %v", n, err)
	}
	if m := <-got; m.Type != gamelink.WireRelease || m.Conn.ConnID != b.ConnID {
		t.Fatalf("instance got %+v", m)
	}
	if len(rnd.Assignments()) != 1 {
		t.Fatal("the R&D server lost its assignment")
	}
	in.SetActive("nobody")
	if _, _, err := in.Assign(3, "p", DefaultMap); err == nil {
		t.Fatal("assigned to a server that isn't connected")
	}
}
