package gamelink

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

func accept(t *testing.T, ln net.Listener) (net.Conn, *bufio.Scanner, Wire) {
	t.Helper()
	ln.(*net.TCPListener).SetDeadline(time.Now().Add(3 * time.Second))
	nc, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(nc)
	if !sc.Scan() {
		t.Fatal("no hello")
	}
	var hello Wire
	if err := json.Unmarshal(sc.Bytes(), &hello); err != nil {
		t.Fatal(err)
	}
	return nc, sc, hello
}

// The client pings; once lobbylogic has answered a ping, its silence drops the link and the client redials.
func TestClientHeartbeat(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan Wire, 4)
	c := &Client{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Addr: ln.Addr().String(), Retry: 10 * time.Millisecond,
		Ping:   30 * time.Millisecond,
		Hello:  func() Wire { return Wire{Addr: "127.0.0.1:1", Name: "x", Modes: AllModes, Maps: AllMaps()} },
		Handle: func(w Wire) { got <- w }}
	go c.Run(ctx)

	nc, sc, hello := accept(t, ln)
	if hello.Type != WireHello || hello.Ping != 30 || hello.Name != "x" || len(hello.Modes) != 4 || len(hello.Maps) == 0 {
		t.Fatalf("hello %+v", hello)
	}
	if !hello.ServesMode(ModeTutorial) || !hello.ServesMap(hello.Maps[0]) || hello.ServesMap(1) {
		t.Fatal("Serves*")
	}
	enc := json.NewEncoder(nc)
	enc.Encode(Wire{Type: WireAssign, Conn: &Assignment{ConnID: 5}})
	if w := <-got; w.Conn.ConnID != 5 {
		t.Fatalf("handled %+v", w)
	}
	// No pong yet: silence is fine (old lobbylogic).
	start := time.Now()
	for time.Since(start) < 150*time.Millisecond {
		if !sc.Scan() {
			t.Fatal("link dropped without a pong")
		}
		var m Wire
		json.Unmarshal(sc.Bytes(), &m)
		if m.Type != WirePing {
			t.Fatalf("expected pings, got %+v", m)
		}
	}
	// One pong, then silence: the client drops the link within 3 intervals and redials.
	enc.Encode(Wire{Type: WirePong})
	dropped := time.Now()
	nc2, _, _ := accept(t, ln)
	defer nc2.Close()
	if d := time.Since(dropped); d > 500*time.Millisecond {
		t.Fatalf("redial after %v", d)
	}
	nc.Close()
}

// An empty hello list serves everything (old servers).
func TestServesEmpty(t *testing.T) {
	var w Wire
	if !w.ServesMode(ModePrivate) || !w.ServesMap(123) {
		t.Fatal("empty lists must serve all")
	}
}
