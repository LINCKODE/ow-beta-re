package match

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"owserver/inst/bits"
	"owserver/inst/clock"
	"owserver/inst/jam"
	"owserver/inst/wire"
	"owserver/internal/gamelink"
	"owserver/internal/heroes"
	"owserver/internal/maps"
)

type sent struct {
	conn uint32
	h    wire.ServerHeader
}

type fakeLink struct {
	seq     uint32
	packets []sent
	down    bool
}

func (f *fakeLink) SendData(conn uint32, payload []byte) (uint32, error) {
	if f.down {
		return 0, errors.New("down")
	}
	h, err := wire.DecodeServerHeader(payload)
	if err != nil {
		return 0, err
	}
	f.packets = append(f.packets, sent{conn, h})
	f.seq++
	return f.seq - 1, nil
}

func (f *fakeLink) take() []sent { p := f.packets; f.packets = nil; return p }

// clientPacket is a client payload with JAM messages and an input history ending at frame.
func clientPacket(t *testing.T, seq *uint32, frame uint32, msgs ...jam.Message) []byte {
	var w bits.Writer
	var env []jam.Envelope
	for _, m := range msgs {
		env = append(env, jam.Envelope{Reliable: true, Seq: *seq, Msg: m})
		*seq++
	}
	if err := jam.WriteStream(&w, env); err != nil {
		t.Fatal(err)
	}
	if frame == 0 {
		w.Bits(0, 1)
		return w.Buf()
	}
	w.Bits(1, 1)
	w.Bits(16+8+32+80+16, 32)
	w.Bits(620, 16) // timing
	w.Bits(1, 8)
	w.Bits(frame, 32)
	w.Bits(0, 32)
	w.Bits(0, 32)
	w.Bits(0, 16+16) // move, buttons... (first entry: 16 16 8 8 16 8 + 16)
	return w.Buf()
}

func TestLifecycle(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fl := &fakeLink{}
	m := NewManager(log, Config{FirstFrame: 100}, fl)
	defer m.Close()
	now := time.Now()
	tick := func(n int) []sent {
		for range n {
			now = now.Add(clock.FrameDuration)
			m.Tick(now)
		}
		return fl.take()
	}

	if err := m.Assign(gamelink.Assignment{ConnID: 1, Map: maps.Anubis, Name: "A"}); err != nil {
		t.Fatal(err)
	}
	if got := tick(3); len(got) != 0 {
		t.Fatalf("sent %d packets before the handshake", len(got))
	}
	m.Connected(1)
	got := tick(2)
	if len(got) != 2 || got[0].h.Clock != clock.FlagResync || got[1].h.Clock != 0 || got[1].h.Frame != got[0].h.Frame+1 {
		t.Fatalf("after connect: %+v", got)
	}

	// Map loaded: 20301 + the controller in the first packet, then the world one per packet.
	var cseq uint32
	m.Data(1, clientPacket(t, &cseq, 0, &jam.MapLoaded{}, &jam.TimeSyncPing{N: 7, Times: []uint64{5}}), now, nil, nil)
	anubis, _ := maps.ByData(maps.Anubis)
	want := 1 + len(anubis.Networked) + len(anubis.Spawns)
	got = tick(want + 3)
	if len(got[0].h.Jam) != 2 || got[0].h.Entities != 1 {
		t.Fatalf("join packet: %+v", got[0].h)
	}
	le, ok1 := got[0].h.Jam[0].Msg.(*jam.LocalEntity)
	pong, ok2 := got[0].h.Jam[1].Msg.(*jam.TimeSyncPong)
	if !ok1 || !ok2 || pong.N != 7 || len(pong.Times) != 4 || le.Entity != jam.EntityID(0x40000000|uint32(len(anubis.Networked)+1)) {
		t.Fatalf("join JAM: %+v %+v", got[0].h.Jam[0].Msg, got[0].h.Jam[1].Msg)
	}
	ents := 0
	for _, s := range got {
		ents += s.h.Entities
	}
	if ents != want {
		t.Fatalf("world: %d entities sent, want %d", ents, want)
	}

	// Input: acknowledged and steered (lead 35 -> slow down, strongly).
	var input uint32
	for range 20 {
		frame, _ := m.Frame(1)
		input = frame + 35
		m.Data(1, clientPacket(t, &cseq, input), now, nil, nil)
		tick(1)
	}
	got = tick(1)
	if got[0].h.InputAck != input || got[0].h.Clock != clock.FlagSlow|clock.FlagStrong {
		t.Fatalf("input ack %d (sent %d), flags %#x", got[0].h.InputAck, input, got[0].h.Clock)
	}

	// Lock-in: body create, controller update, start-up.
	genji, _ := heroes.ByDef(heroes.All[0].Def)
	m.Data(1, clientPacket(t, &cseq, 0, &jam.HeroLockIn{Hero: genji.Hero}), now, nil, nil)
	got = tick(5)
	ents = 0
	for _, s := range got {
		ents += s.h.Entities
	}
	pi := m.Player(1)
	if ents != 3 || pi.State != Playing || pi.Body == 0 || pi.Scripts != 1 {
		t.Fatalf("lock in: %d entities, %+v", ents, pi)
	}

	// A second player in the same match gets the first one's body too.
	m.Assign(gamelink.Assignment{ConnID: 2, Map: maps.Anubis})
	m.Connected(2)
	var c2 uint32
	m.Data(2, clientPacket(t, &c2, 0, &jam.MapLoaded{}), now, nil, nil)
	got = tick(want + 5)
	ents = 0
	for _, s := range got {
		if s.conn == 2 {
			ents += s.h.Entities
		}
	}
	if ents != want+1 || len(m.Matches(false)) != 1 {
		t.Fatalf("second player: %d entities", ents)
	}

	// Lost packets are resent.
	fl.down = true
	m.SendJam(2, &jam.MapLoaded{}, true) // any reliable message
	tick(3)
	fl.down = false
	if got := tick(1); len(got) != 2 {
		t.Fatalf("after the link came back: %d packets", len(got))
	}

	// Leaving ends the match with its last player.
	m.Closed(1, "test")
	m.Release(2)
	if len(m.Matches(false)) != 0 || len(m.Players()) != 0 {
		t.Fatal("match not ended")
	}
}
