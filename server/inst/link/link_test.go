package link

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"owserver/inst/jam"
)

// A connect packet the client sent after 20600 with conn 1 and key 20 x 0x11.
const capturedSYN = "c20181dc78f80df1a2f4bd1a100000000000000000000000000000000000000001ad"

func TestCapturedSYN(t *testing.T) {
	b, _ := hex.DecodeString(capturedSYN)
	h, err := ParseHeader(b)
	if err != nil || h.ConnID != 1 || h.Data() || h.Flags != FlagSYN {
		t.Fatalf("%+v %v", h, err)
	}
	key := bytes.Repeat([]byte{0x11}, 20)
	if !Verify(key, b) || Verify(bytes.Repeat([]byte{0x22}, 20), b) {
		t.Fatal("MAC")
	}
	if !bytes.Equal(Sign(key, AppendHeader(nil, h)), b) {
		t.Fatal("header + Sign don't reproduce the packet")
	}
}

func TestWindow(t *testing.T) {
	var w window
	for _, s := range []uint32{10, 11, 13} {
		w.add(s)
	}
	if w.ack != 13 || w.mask != 0b110 {
		t.Fatalf("ack %d mask %b", w.ack, w.mask)
	}
	w.add(12)
	if w.mask != 0b111 || !acked(w.ack, w.mask, 10) || acked(w.ack, w.mask, 9) {
		t.Fatalf("mask %b", w.mask)
	}
}

type events struct {
	mu        sync.Mutex
	connected []uint32
	data      [][]byte
	acked     []uint32
	lost      []uint32
	closed    []string
}

func (e *events) Connected(id uint32) {
	e.mu.Lock()
	e.connected = append(e.connected, id)
	e.mu.Unlock()
}
func (e *events) Data(id uint32, p []byte, at time.Time, a, l []uint32) {
	e.mu.Lock()
	e.data = append(e.data, p)
	e.acked = append(e.acked, a...)
	e.lost = append(e.lost, l...)
	e.mu.Unlock()
}
func (e *events) Closed(id uint32, why string) {
	e.mu.Lock()
	e.closed = append(e.closed, why)
	e.mu.Unlock()
}

// client is a fake game client: it signs packets and reads the server's answers.
type client struct {
	t    *testing.T
	pc   net.PacketConn
	srv  net.Addr
	key  []byte
	id   uint32
	seq  uint32
	recv window
}

func (c *client) send(kind uint32, flags byte, payload []byte) {
	h := Header{ConnID: c.id, Kind: kind, Seq: c.seq, Ack: c.recv.ack, Mask: c.recv.mask, Flags: flags}
	c.seq++
	if _, err := c.pc.WriteTo(Sign(c.key, append(AppendHeader(nil, h), payload...)), c.srv); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) read() (Header, []byte) {
	buf := make([]byte, 2048)
	c.pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := c.pc.ReadFrom(buf)
	if err != nil {
		c.t.Fatal(err)
	}
	if !Verify(c.key, buf[:n]) {
		c.t.Fatal("server packet MAC")
	}
	h, err := ParseHeader(buf[:n])
	if err != nil {
		c.t.Fatal(err)
	}
	c.recv.add(h.Seq)
	return h, buf[HeaderSize:n]
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("timed out")
}

func TestHandshakeDataFIN(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ev := &events{}
	l := New(slog.New(slog.NewTextHandler(io.Discard, nil)), ev, Config{Keepalive: time.Hour})
	if err := l.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	go l.Serve(ctx)
	key := bytes.Repeat([]byte{0x11}, 20)
	l.Assign(7, key)
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	c := &client{t: t, pc: pc, srv: l.Addr(), key: key, id: 7}

	c.send(0, FlagSYN, nil)
	if h, _ := c.read(); h.Flags != FlagSYN|FlagACK || h.Ack != 0 || h.Seq != 0 {
		t.Fatalf("SYN answer %+v", h)
	}
	c.send(0, FlagACK, nil)
	if h, _ := c.read(); h.Flags != FlagACK {
		t.Fatalf("ACK answer %+v", h)
	}
	waitFor(t, func() bool { return l.Connected(7) })

	// Our data packets: the client acks the first, the second is lost (3 behind).
	s1, err := l.SendData(7, []byte{0xaa})
	if err != nil {
		t.Fatal(err)
	}
	if h, p := c.read(); !h.Data() || !bytes.Equal(p, []byte{0xaa}) || h.Seq != s1 {
		t.Fatalf("data %+v %x", h, p)
	}
	s2, _ := l.SendData(7, []byte{0xbb})
	c.read()
	c.recv = window{} // forget s2
	c.recv.add(s1)
	c.send(kindData, FlagACK, []byte{1, 2})
	waitFor(t, func() bool { ev.mu.Lock(); defer ev.mu.Unlock(); return len(ev.data) == 1 })
	ev.mu.Lock()
	if len(ev.acked) != 1 || ev.acked[0] != s1 || len(ev.lost) != 0 {
		t.Fatalf("acked %v lost %v", ev.acked, ev.lost)
	}
	ev.mu.Unlock()
	for range 3 {
		l.SendData(7, nil)
	}
	c.recv = window{}
	c.recv.add(s1)
	c.recv.add(s2 + 3)
	c.send(kindData, FlagACK, []byte{3})
	waitFor(t, func() bool { ev.mu.Lock(); defer ev.mu.Unlock(); return len(ev.data) == 2 })
	ev.mu.Lock()
	if len(ev.lost) != 1 || ev.lost[0] != s2 {
		t.Fatalf("lost %v (s2 %d)", ev.lost, s2)
	}
	ev.mu.Unlock()

	// The client leaves.
	c.pc.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	for {
		if _, _, err := c.pc.ReadFrom(make([]byte, 2048)); err != nil {
			break
		}
	}
	c.send(0, FlagFIN, nil)
	if h, _ := c.read(); h.Flags != FlagFIN|FlagACK {
		t.Fatalf("FIN answer %+v", h)
	}
	waitFor(t, func() bool { ev.mu.Lock(); defer ev.mu.Unlock(); return len(ev.closed) == 1 })
	if _, err := l.SendData(7, nil); err != ErrNotConnected {
		t.Fatalf("send after close: %v", err)
	}
}

func TestStaleConnectionEnded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ev := &events{}
	l := New(slog.New(slog.NewTextHandler(io.Discard, nil)), ev, Config{Keepalive: time.Hour})
	l.Listen("127.0.0.1:0")
	go l.Serve(ctx)
	key := bytes.Repeat([]byte{0x33}, 20)
	l.Assign(2, key)
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	defer pc.Close()
	c := &client{t: t, pc: pc, srv: l.Addr(), key: key, id: 2}
	c.send(kindData, FlagACK, []byte{0}) // data without a SYN: a client from before a restart
	if h, _ := c.read(); h.Flags != FlagFIN {
		t.Fatalf("want FIN, got %+v", h)
	}
	c.send(kindData, FlagACK, []byte{0})
	time.Sleep(50 * time.Millisecond)
	ev.mu.Lock()
	defer ev.mu.Unlock()
	if len(ev.closed) != 1 || len(ev.data) != 0 {
		t.Fatalf("closed %v data %d", ev.closed, len(ev.data))
	}
}

func TestChannelResendAndOrder(t *testing.T) {
	c := NewChannel()
	for range 20 {
		c.Send(&jam.MapLoaded{}, true)
	}
	c.Send(&jam.MapLoaded{}, false)
	p1 := c.Take()
	if len(p1) != 16 || p1[14].Seq != 14 || p1[15].Reliable {
		t.Fatalf("first packet %d msgs", len(p1))
	}
	c.Sent(100, p1)
	p2 := c.Take()
	if len(p2) != 5 || p2[0].Seq != 15 {
		t.Fatalf("second packet %+v", p2)
	}
	c.Sent(101, p2)
	c.Lost([]uint32{100})
	c.Acked([]uint32{101})
	p3 := c.Take()
	if len(p3) != 15 || p3[0].Seq != 0 || c.Resends != 15 {
		t.Fatalf("resend %d msgs, first seq %d", len(p3), p3[0].Seq)
	}
	if c.Pending() {
		t.Fatal("still pending")
	}

	// Receive: in order, duplicates dropped, gaps held back.
	r := NewChannel()
	env := func(seqs ...uint32) []jam.Envelope {
		var out []jam.Envelope
		for _, s := range seqs {
			out = append(out, jam.Envelope{Reliable: true, Seq: s, Msg: &jam.MapLoaded{}})
		}
		return out
	}
	if got := r.Receive(env(0, 2)); len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	if got := r.Receive(env(1, 0)); len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 2 {
		t.Fatalf("got %+v", got)
	}
}
