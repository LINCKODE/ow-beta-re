package match

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"owserver/inst/clock"
	"owserver/inst/jam"
	"owserver/internal/gamelink"
	"owserver/internal/maps"
)

// A subscribed connection gets one frame per tick: seq counts up, a full frame lists every entity, the others only
// heroes; the 21601 transform is kept; off stops the stream; sizes stay small.
func TestServerView(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fl := &fakeLink{}
	m := NewManager(log, Config{FirstFrame: 100}, fl)
	defer m.Close()
	var got []*gamelink.ViewFrame
	sink := func(f *gamelink.ViewFrame) bool { got = append(got, f); return true }
	now := time.Now()
	tick := func(n int) {
		for range n {
			now = now.Add(clock.FrameDuration)
			m.Tick(now)
			m.ViewTick(now, 0, sink)
		}
	}
	m.SetView(1, true) // before the player exists: waits
	tick(1)
	if len(got) != 0 {
		t.Fatal("a frame without a player")
	}
	if err := m.Assign(gamelink.Assignment{ConnID: 1, Map: maps.Anubis, Name: "A"}); err != nil {
		t.Fatal(err)
	}
	m.Connected(1)
	var cseq uint32
	m.Data(1, clientPacket(t, &cseq, 0, &jam.PlayerTransform{Pos: jam.Vec3{X: 1.234, Y: 2, Z: -3.456}, Rot: jam.Vec4{W: 1}}, &jam.MapLoaded{}), now, nil, nil)
	tick(ViewFullEvery + 2)
	if len(got) != ViewFullEvery+2 {
		t.Fatalf("%d frames, want %d", len(got), ViewFullEvery+2)
	}
	type body struct {
		V    int    `json:"v"`
		Full bool   `json:"full"`
		Map  string `json:"map"`
		Ents []struct {
			ID uint32 `json:"id"`
			K  string `json:"k"`
		} `json:"ents"`
		Clk struct {
			St string `json:"st"`
		} `json:"clk"`
		XF *struct {
			P [3]float64 `json:"p"`
		} `json:"xf"`
	}
	var maxFull, maxPart int
	for i, f := range got {
		var b body
		if err := json.Unmarshal(f.Body, &b); err != nil {
			t.Fatalf("frame %d: %v: %s", i, err, f.Body)
		}
		if f.Seq != uint32(i+1) || f.Conn != 1 || f.SentUS == 0 || b.V != 1 || b.Clk.St == "" {
			t.Fatalf("frame %d: %+v %s", i, f, f.Body)
		}
		if i > 0 && f.Frame != got[i-1].Frame+1 {
			t.Fatalf("frame %d: CF %d after %d", i, f.Frame, got[i-1].Frame)
		}
		if b.XF == nil || b.XF.P != [3]float64{1.23, 2, -3.46} {
			t.Fatalf("frame %d: xf %+v", i, b.XF)
		}
		wantFull := i%ViewFullEvery == 0
		if b.Full != wantFull || (b.Map != "") != wantFull {
			t.Fatalf("frame %d: full %v map %q", i, b.Full, b.Map)
		}
		if b.Full {
			if len(b.Ents) < 10 {
				t.Fatalf("full frame with %d entities", len(b.Ents))
			}
			maxFull = max(maxFull, len(f.Body))
		} else {
			for _, e := range b.Ents {
				if e.K != "hero" {
					t.Fatalf("partial frame lists a %s", e.K)
				}
			}
			maxPart = max(maxPart, len(f.Body))
		}
	}
	t.Logf("Anubis: full frame %d bytes, partial %d bytes; %s", maxFull, maxPart, got[0].Body)
	if maxFull > 4096 || maxPart > 1024 {
		t.Fatalf("frames too big: full %d, partial %d", maxFull, maxPart)
	}
	// Server text: its own lines first, then the notes (global, then the connection's).
	m.Note(0, "zz", "global")
	m.Note(1, "aa", "mine")
	m.Note(2, "x", "someone else's")
	tick(1)
	var last struct {
		Txt []string `json:"txt"`
	}
	json.Unmarshal(got[len(got)-1].Body, &last)
	if len(last.Txt) < 4 || !strings.HasPrefix(last.Txt[0], "SERVER TEMPLE OF ANUBIS  CF ") ||
		last.Txt[len(last.Txt)-2] != "zz: global" || last.Txt[len(last.Txt)-1] != "aa: mine" {
		t.Fatalf("txt %q", last.Txt)
	}
	m.Note(1, "aa", "")
	tick(1)
	json.Unmarshal(got[len(got)-1].Body, &last)
	if last.Txt[len(last.Txt)-1] != "zz: global" {
		t.Fatalf("txt after removing a note %q", last.Txt)
	}
	t.Logf("txt: %q", last.Txt)
	m.SetView(1, false)
	n := len(got)
	tick(3)
	if len(got) != n {
		t.Fatal("frames after off")
	}
	if s := m.ViewStats(); s.Frames != uint64(n) || s.Dropped != 0 || len(s.Conns) != 0 {
		t.Fatalf("stats %+v", s)
	}
}
