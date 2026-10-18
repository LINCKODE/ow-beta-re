package lobbylogic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"owserver/internal/gamelink"
)

func TestServerViewEncoding(t *testing.T) {
	got := encodeServerView(&gamelink.ViewFrame{Seq: 3, Frame: 0x1234, SentUS: 0x0102030405060708, Body: json.RawMessage(`{"v":1}`)}, 0x1112131415161718)
	want := []byte{3, 0, 0, 0, 0x34, 0x12, 0, 0}
	want = binary.LittleEndian.AppendUint64(want, 0x0102030405060708)
	want = binary.LittleEndian.AppendUint64(want, 0x1112131415161718)
	want = append(want, "{\"v\":1}\x00"...)
	if !bytes.Equal(got, want) { // docs/JAM.md §13.8
		t.Fatalf("got %x", got)
	}
}

// A fake game server that streams: SetView tells it about the session's connections, a later assignment and a
// reconnect repeat the subscription, and its frames reach OnFrame with the connection's session.
func TestServerViewSubscriptions(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var frames []uint64 // sessions
	in := &Instances{Log: quiet, Active: "instance", OnFrame: func(s uint64, f *gamelink.ViewFrame) {
		mu.Lock()
		frames = append(frames, s)
		mu.Unlock()
	}}
	go in.Serve(ctx, addr)

	type fake struct {
		nc  net.Conn
		enc *json.Encoder
		got chan gamelink.Wire
	}
	dial := func() *fake {
		var nc net.Conn
		waitFor(t, "lobbylogic", func() bool {
			var err error
			nc, err = net.Dial("tcp", addr)
			return err == nil
		})
		f := &fake{nc: nc, enc: json.NewEncoder(nc), got: make(chan gamelink.Wire, 16)}
		f.enc.Encode(gamelink.Wire{Type: gamelink.WireHello, Name: "instance", Addr: "127.0.0.1:1", View: true})
		go func() {
			sc := bufio.NewScanner(nc)
			for sc.Scan() {
				var w gamelink.Wire
				json.Unmarshal(sc.Bytes(), &w)
				f.got <- w
			}
			close(f.got)
		}()
		return f
	}
	next := func(f *fake, typ string) gamelink.Wire {
		t.Helper()
		for {
			select {
			case w, ok := <-f.got:
				if !ok {
					t.Fatalf("link closed waiting for %s", typ)
				}
				if w.Type == typ {
					return w
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("no %s", typ)
			}
		}
	}

	f := dial()
	var a gamelink.Assignment
	waitFor(t, "the game server", func() bool {
		var err error
		a, _, err = in.Assign(5, "p", DefaultMap)
		return err == nil
	})
	next(f, gamelink.WireAssign)
	if told := in.SetView(5, true); len(told) != 1 || told[0] != a.ConnID {
		t.Fatalf("told %v", told)
	}
	if w := next(f, gamelink.WireView); w.Conn.ConnID != a.ConnID || !w.View {
		t.Fatalf("view %+v", w)
	}
	// A new assignment of the session is subscribed at once.
	b, _, err := in.Assign(5, "p", DefaultMap)
	if err != nil {
		t.Fatal(err)
	}
	next(f, gamelink.WireAssign)
	if w := next(f, gamelink.WireView); w.Conn.ConnID != b.ConnID || !w.View {
		t.Fatalf("view after assign %+v", w)
	}
	// Frames go to the session; an unknown connection's frame is dropped.
	f.enc.Encode(gamelink.Wire{Type: gamelink.WireFrame, Frame: &gamelink.ViewFrame{Conn: a.ConnID, Seq: 1, Body: json.RawMessage(`{}`)}})
	f.enc.Encode(gamelink.Wire{Type: gamelink.WireFrame, Frame: &gamelink.ViewFrame{Conn: 999, Seq: 1, Body: json.RawMessage(`{}`)}})
	f.enc.Encode(gamelink.Wire{Type: gamelink.WireFrame, Frame: &gamelink.ViewFrame{Conn: b.ConnID, Seq: 2, Body: json.RawMessage(`{}`)}})
	waitFor(t, "frames", func() bool { mu.Lock(); defer mu.Unlock(); return len(frames) == 2 })
	if frames[0] != 5 || frames[1] != 5 {
		t.Fatalf("frames to sessions %v", frames)
	}
	// A reconnecting game server gets the subscriptions again.
	f.nc.Close()
	f2 := dial()
	seen := map[uint32]bool{}
	for len(seen) < 2 {
		w := next(f2, gamelink.WireView)
		seen[w.Conn.ConnID] = w.View
	}
	if !seen[a.ConnID] || !seen[b.ConnID] {
		t.Fatalf("replayed views %v", seen)
	}
	// Off.
	if told := in.SetView(5, false); len(told) != 2 || in.Viewing(5) {
		t.Fatalf("off told %v", told)
	}
	if w := next(f2, gamelink.WireView); w.View {
		t.Fatalf("off %+v", w)
	}
}
