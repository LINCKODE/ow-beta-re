package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"owserver/inst/bits"
	"owserver/inst/clock"
	"owserver/inst/jam"
	"owserver/inst/link"
	"owserver/inst/match"
	"owserver/inst/wire"
	"owserver/internal/gamelink"
	"owserver/internal/heroes"
	"owserver/internal/maps"
)

// A fake client against the whole server: lobbylogic assigns, the client connects,
// loads the map, gets the world, locks in, leaves; packets come every 16 ms.
func TestEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	logic, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer logic.Close()
	s := New(slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Addr: "127.0.0.1:0", Advertise: "x", LogicAddr: logic.Addr().String()})
	go s.Run(ctx)

	// lobbylogic: accept the hello, assign conn 5.
	lc, err := logic.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer lc.Close()
	key := bytes.Repeat([]byte{0x42}, 20)
	json.NewEncoder(lc).Encode(gamelink.Wire{Type: gamelink.WireAssign, Conn: &gamelink.Assignment{ConnID: 5, Key: key, Map: maps.Anubis}})
	var addr net.Addr
	for addr == nil {
		addr = s.Link.Addr()
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // the assignment reaches the link

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	var seq, jseq uint32
	send := func(kind uint32, flags byte, payload []byte) {
		h := link.Header{ConnID: 5, Kind: kind, Seq: seq, Flags: flags}
		seq++
		pc.WriteTo(link.Sign(key, append(link.AppendHeader(nil, h), payload...)), addr)
	}
	read := func() (link.Header, []byte) {
		buf := make([]byte, 4096)
		pc.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			t.Fatal(err)
		}
		h, _ := link.ParseHeader(buf[:n])
		return h, buf[link.HeaderSize:n]
	}
	jamPacket := func(msgs ...jam.Message) []byte {
		var w bits.Writer
		var env []jam.Envelope
		for _, m := range msgs {
			env = append(env, jam.Envelope{Reliable: true, Seq: jseq, Msg: m})
			jseq++
		}
		jam.WriteStream(&w, env)
		w.Bits(0, 1) // no input
		return w.Buf()
	}

	send(0, link.FlagSYN, nil)
	if h, _ := read(); h.Flags != link.FlagSYN|link.FlagACK {
		t.Fatalf("handshake %+v", h)
	}
	send(1, link.FlagACK, jamPacket()) // data completes the handshake

	// Tick packets: one per 16 ms, frames consecutive, the first one resyncs the client.
	var frames []uint32
	var flags []uint8
	start := time.Now()
	for len(frames) < 30 {
		h, p := read()
		if !h.Data() {
			continue
		}
		sh, err := wire.DecodeServerHeader(p)
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, sh.Frame)
		flags = append(flags, sh.Clock)
	}
	el := time.Since(start)
	for i := 1; i < len(frames); i++ {
		if frames[i] != frames[i-1]+1 {
			t.Fatalf("frames %v", frames)
		}
	}
	if flags[0] != clock.FlagResync {
		t.Fatalf("first flags %#x", flags[0])
	}
	if el < 29*clock.FrameDuration*8/10 || el > 29*clock.FrameDuration*2 {
		t.Fatalf("30 packets in %v", el)
	}

	// Map loaded -> 20301 + controller, then the world.
	send(1, link.FlagACK, jamPacket(&jam.MapLoaded{}))
	anubis, _ := maps.ByData(maps.Anubis)
	want := 1 + len(anubis.Networked) + len(anubis.Spawns)
	ents, local := 0, false
	for deadline := time.Now().Add(5 * time.Second); ents < want && time.Now().Before(deadline); {
		h, p := read()
		if !h.Data() {
			continue
		}
		sh, err := wire.DecodeServerHeader(p)
		if err != nil {
			t.Fatal(err)
		}
		ents += sh.Entities
		for _, e := range sh.Jam {
			if _, ok := e.Msg.(*jam.LocalEntity); ok {
				local = true
			}
		}
	}
	if ents != want || !local {
		t.Fatalf("world: %d entities (want %d), 20301 %v", ents, want, local)
	}

	// Lock in.
	send(1, link.FlagACK, jamPacket(&jam.HeroLockIn{Hero: heroes.All[0].Hero}))
	ents = 0
	for deadline := time.Now().Add(2 * time.Second); ents < 3 && time.Now().Before(deadline); {
		h, p := read()
		if h.Data() {
			sh, _ := wire.DecodeServerHeader(p)
			ents += sh.Entities
		}
	}
	var ps []match.PlayerInfo
	s.Do(ctx, func(m *match.Manager) error { ps = m.Players(); return nil })
	if ents != 3 || len(ps) != 1 || ps[0].State != match.Playing {
		t.Fatalf("lock in: %d entities, %+v", ents, ps)
	}

	// The lobby releases the player: FIN, then the match ends.
	json.NewEncoder(lc).Encode(gamelink.Wire{Type: gamelink.WireRelease, Conn: &gamelink.Assignment{ConnID: 5}})
	for deadline := time.Now().Add(2 * time.Second); ; {
		h, _ := read()
		if h.Flags&link.FlagFIN != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no FIN")
		}
	}
	var n int
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		s.Do(ctx, func(m *match.Manager) error { n = len(m.Matches(false)); return nil })
		if n == 0 {
			break
		}
	}
	if n != 0 {
		t.Fatal("match not ended")
	}
}
