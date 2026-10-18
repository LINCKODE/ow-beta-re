package lobbylink

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"owserver/internal/gamelink"
)

type handler struct {
	mu       sync.Mutex
	assigned []uint32
	released []uint32
	views    []string
}

func (h *handler) View(c uint32, on bool) {
	h.mu.Lock()
	h.views = append(h.views, fmt.Sprint(c, on))
	h.mu.Unlock()
}

func (h *handler) Assign(a gamelink.Assignment) {
	h.mu.Lock()
	h.assigned = append(h.assigned, a.ConnID)
	h.mu.Unlock()
}
func (h *handler) Release(c uint32) { h.mu.Lock(); h.released = append(h.released, c); h.mu.Unlock() }
func (h *handler) Assignments() []gamelink.Assignment {
	return []gamelink.Assignment{{ConnID: 9}}
}

func TestHelloAssignReleaseReconnect(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &handler{}
	l := &Link{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), LogicAddr: ln.Addr().String(), Advertise: "127.0.0.1:26600", Name: "instance", Handler: h, Retry: 20 * time.Millisecond}
	go l.Run(ctx)

	for round := range 2 { // the second round: after the link dropped
		nc, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(nc)
		if !sc.Scan() {
			t.Fatal("no hello")
		}
		var hello gamelink.Wire
		json.Unmarshal(sc.Bytes(), &hello)
		if hello.Type != gamelink.WireHello || hello.Addr != "127.0.0.1:26600" || hello.Name != "instance" || len(hello.Conns) != 1 || hello.Conns[0].ConnID != 9 ||
			hello.Ping != int(gamelink.PingInterval/time.Millisecond) || len(hello.Modes) != len(gamelink.AllModes) || len(hello.Maps) == 0 {
			t.Fatalf("hello %+v", hello)
		}
		enc := json.NewEncoder(nc)
		enc.Encode(gamelink.Wire{Type: gamelink.WireAssign, Conn: &gamelink.Assignment{ConnID: uint32(10 + round)}})
		enc.Encode(gamelink.Wire{Type: gamelink.WireRelease, Conn: &gamelink.Assignment{ConnID: uint32(10 + round)}})
		for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			h.mu.Lock()
			n := len(h.released)
			h.mu.Unlock()
			if n == round+1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("no release")
			}
		}
		nc.Close()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.assigned) != 2 || h.assigned[1] != 11 {
		t.Fatalf("assigned %v", h.assigned)
	}
}

// view {conn, on} reaches the handler; frames go out as frame lines, and drop while the link is down.
func TestViewAndFrames(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &handler{}
	l := &Link{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), LogicAddr: ln.Addr().String(), Name: "instance", Handler: h, Retry: 20 * time.Millisecond}
	l.Init()
	if l.Frame(&gamelink.ViewFrame{Conn: 1, Seq: 1}) || l.Dropped() != 1 {
		t.Fatal("a frame before the link is up must drop")
	}
	go l.Run(ctx)
	nc, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	sc := bufio.NewScanner(nc)
	var hello gamelink.Wire
	if !sc.Scan() || json.Unmarshal(sc.Bytes(), &hello) != nil || !hello.View {
		t.Fatalf("hello %+v", hello)
	}
	json.NewEncoder(nc).Encode(gamelink.Wire{Type: gamelink.WireView, Conn: &gamelink.Assignment{ConnID: 7}, View: true})
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		h.mu.Lock()
		n := len(h.views)
		h.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no view")
		}
	}
	if h.views[0] != "7 true" {
		t.Fatalf("views %v", h.views)
	}
	for !l.Frame(&gamelink.ViewFrame{Conn: 7, Seq: 2, Frame: 100, SentUS: 5, Body: json.RawMessage(`{"v":1}`)}) {
		time.Sleep(5 * time.Millisecond) // until serve set the queue up
	}
	nc.SetReadDeadline(time.Now().Add(2 * time.Second))
	for sc.Scan() {
		var m gamelink.Wire
		json.Unmarshal(sc.Bytes(), &m)
		if m.Type != gamelink.WireFrame {
			continue // pings
		}
		if m.Frame == nil || m.Frame.Conn != 7 || m.Frame.Seq != 2 || m.Frame.Frame != 100 || string(m.Frame.Body) != `{"v":1}` {
			t.Fatalf("frame %s", sc.Bytes())
		}
		return
	}
	t.Fatal("no frame")
}
