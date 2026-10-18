package instance

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"io"
	"log/slog"
	"math"
	"net"
	"testing"
	"time"

	"owserver/internal/gamelink"
)

// A connect packet the client sent after 20600 with conn 1 and key 20 x 0x11.
const capturedConnect = "c20181dc78f80df1a2f4bd1a100000000000000000000000000000000000000001ad"

func TestParseAndVerifyCapturedPacket(t *testing.T) {
	b, _ := hex.DecodeString(capturedConnect)
	p, err := ParsePacket(b)
	if err != nil {
		t.Fatal(err)
	}
	if p.ConnID() != 1 || p.Flags() != 0 || p.Seq != 0 {
		t.Errorf("conn %d flags %d seq %d", p.ConnID(), p.Flags(), p.Seq)
	}
	key := bytes.Repeat([]byte{0x11}, 20)
	if !Verify(key, b) {
		t.Error("MAC doesn't verify")
	}
	if Verify(bytes.Repeat([]byte{0x22}, 20), b) {
		t.Error("MAC verifies with the wrong key")
	}
	if !bytes.Equal(Sign(key, b[MACSize:]), b) {
		t.Error("Sign doesn't reproduce the packet")
	}
}

func TestServerAcceptsSignedPacketAndAnswers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	pc.Close()
	go s.ListenAndServe(ctx, addr)
	time.Sleep(50 * time.Millisecond)

	key := bytes.Repeat([]byte{0x42}, 20)
	s.Assign(gamelink.Assignment{ConnID: 5, Key: key})
	client, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	// Handshake first: data on a connection without a SYN seen by this process is
	// treated as a leftover from before a restart and ended.
	client.Write(ControlPacket(key, 5, ControlMsg{Flags: FlagSYN}))
	synack := make([]byte, 2048)
	client.SetReadDeadline(time.Now().Add(time.Second))
	if n, err := client.Read(synack); err != nil {
		t.Fatal(err)
	} else if c, ok := ParseControl(synack[:n]); !ok || c.Flags != FlagSYN|FlagACK {
		t.Fatalf("SYN answer: %x", synack[:n])
	}
	rest := binary.LittleEndian.AppendUint32(nil, 5<<4)
	rest = binary.LittleEndian.AppendUint32(rest, 0)
	client.Write(Sign(key, rest))
	client.Write(append(make([]byte, MACSize), rest...)) // bad MAC

	var in []TrafficEntry
	deadline := time.Now().Add(time.Second)
	for len(in) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		in = in[:0]
		for _, e := range s.Traffic(0) {
			if e.Dir == "in" {
				in = append(in, e)
			}
		}
	}
	if len(in) != 3 || !in[1].Valid || in[2].Valid {
		t.Fatalf("traffic: %+v", s.Traffic(0))
	}

	// The valid data packet was answered with a keepalive ACK.
	buf := make([]byte, 2048)
	client.SetReadDeadline(time.Now().Add(time.Second))
	n, err := client.Read(buf)
	if c, ok := ParseControl(buf[:n]); err != nil || !ok || c.Flags != FlagACK || !Verify(key, buf[:n]) {
		t.Fatalf("keepalive: %x %v", buf[:n], err)
	}

	if err := s.Send(5, rest, true); err != nil {
		t.Fatal(err)
	}
	// The first data packet also starts setupWorld, whose spawn packets may arrive
	// first: skip data packets until the 20-byte reply.
	for {
		n, err = client.Read(buf)
		if err != nil {
			t.Fatalf("reply: %v", err)
		}
		if n == len(rest)+MACSize {
			break
		}
	}
	if !Verify(key, buf[:n]) {
		t.Fatalf("reply: %x", buf[:n])
	}
}

func TestControlPacketReproducesCapturedSYN(t *testing.T) {
	want, _ := hex.DecodeString(capturedConnect)
	got := ControlPacket(bytes.Repeat([]byte{0x11}, 20), 1, ControlMsg{Flags: FlagSYN})
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x\nwant %x", got, want)
	}
	c, ok := ParseControl(want)
	if !ok || c.Flags != FlagSYN || c.Seq != 0 {
		t.Fatalf("parse: %+v %v", c, ok)
	}
}

func TestAckWindow(t *testing.T) {
	var w ackWindow
	for _, s := range []uint32{10, 11, 13, 12, 9} {
		w.add(s)
	}
	// ack 13; 12 (bit 0), 11 (bit 1), 10 (bit 2), 9 (bit 3) received.
	if w.ack != 13 || w.mask != 0b1111 {
		t.Fatalf("ack %d mask %b", w.ack, w.mask)
	}
	w.add(15) // 14 missing
	if w.ack != 15 || w.mask != 0b111110 {
		t.Fatalf("ack %d mask %b", w.ack, w.mask)
	}
}

func TestJamStreamRoundTrip(t *testing.T) {
	// A captured client ping: ClientOutInstance 21608 on channel 1, index 8.
	ping, _ := hex.DecodeString("03002600000001084e00000002000000da680c0400000000da680c040000000000")
	msgs, err := ParseJamStream(ping, clientMsgSize)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("parse: %v %+v", err, msgs)
	}
	m := msgs[0]
	if id, _ := m.ID(); id != 21608 || !m.Reliable || m.Seq != 0x26 || len(m.Payload) != 24 {
		t.Fatalf("got %+v", m)
	}
	out, err := EncodeJamStream(msgs)
	if err != nil || !bytes.Equal(out, ping[:len(out)]) {
		t.Fatalf("encode: %x %v", out, err)
	}
	if ch, idx, ok := ChannelOf(20304); !ok || ch != 0 || idx != 4 {
		t.Fatalf("ChannelOf(20304) = %d %d %v", ch, idx, ok)
	}
}

func TestDataPayloadFramePrefix(t *testing.T) {
	msg := JamMsg{Reliable: true, Seq: 0x18, Channel: 0, Index: 4, Payload: []byte{0x12, 0x14}}
	p, err := EncodeDataPayload(5, 0, []JamMsg{msg})
	if err != nil {
		t.Fatal(err)
	}
	// 1 + 18-bit frame (5), then stream 0: present, 1 reliable, 0 unreliable = 28 bits,
	// so the messages start at byte 4.
	// ... then stream 3 (varint 0 = 1 + 18 bits, u4), stream 4 (1 bit) and the entity
	// count (u7): 31 bits, starting at a byte boundary after the message.
	want := "0b00" + "1800" + "18000000" + "0004" + "1214" + "01000000"
	if hex.EncodeToString(p) != want {
		t.Fatalf("got %x want %s", p, want)
	}
}

func TestSpawnEncoding(t *testing.T) {
	sp := Spawn{ID: 1, Def: 0x0e, Rot: [4]float32{0, 0, 0, 1}, Scale: [3]float32{1, 1, 1}}
	if sp.EntityID() != 0x40000001 {
		t.Fatalf("entity id %#x", sp.EntityID())
	}
	p, err := EncodeDataPayload(1, 0, nil, sp)
	if err != nil {
		t.Fatal(err)
	}
	// frame 19 + stream 0 header 9 + stream 3 19+4 + stream 4 1 + count 7 + entity ID
	// 10 + stream bits (1, 1+1, 1+2, 1+1, 1) + op 2 + length 14 + create 341 + N 8.
	blob := 1 + 13 + 7 + 320 + 8
	want := 19 + 9 + 19 + 4 + 1 + 7 + 10 + (1 + 2 + 3 + 2 + 1) + 2 + 14 + blob
	if len(p) != (want+7)/8 {
		t.Fatalf("payload %d bytes, want %d bits", len(p), want)
	}
	// The u14 length holds the blob size.
	lenPos := uint(want - blob - 14)
	var got uint32
	for i := uint(0); i < 14; i++ {
		got |= uint32(p[(lenPos+i)/8]>>((lenPos+i)%8)&1) << i
	}
	if got != uint32(blob) {
		t.Fatalf("u14 length %d, want %d", got, blob)
	}
}

func TestComponentUpdateAlignment(t *testing.T) {
	up := Spawn{ID: 1, Update: true, Components: []ComponentUpdate{Mirror20(0x40000002, 0x40000002, 0x40000002)}}
	var w bitWriter
	w.bits(0, 3) // start mid-byte
	up.writeEntityData(&w, 0)
	// op 2 bits, u14, u8 N=1, u8 id 0x20 -> bit 3+2+14+8+8 = 35 -> length byte at byte 5,
	// mask at byte 6, then the three entity IDs from byte 7.
	if w.buf[5] != 1 || w.buf[6] != 0b111 {
		t.Fatalf("length/mask %x", w.buf)
	}
	if binary.LittleEndian.Uint32(w.buf[7:]) != 0x40000002 || len(w.buf) != 7+12 {
		t.Fatalf("values %x", w.buf[7:])
	}
}

func TestJamBoolsAreBits(t *testing.T) {
	// 20301 = 28 bytes + a bool bit; the stream 3 varint must start at the next bit.
	p, err := EncodeDataPayload(1, 0, []JamMsg{LocalEntityMsg(0x40000001)})
	if err != nil {
		t.Fatal(err)
	}
	// header bytes 0-3 (frame 19 bits + 9 stream bits = 28 bits), message at byte 4:
	// 4 seq + 2 + 28 payload = bytes 4..37, bool at bit 0 of byte 38, then the stream 3
	// varint's 1 prefix at bit 1.
	if len(p) < 39 || p[38]&1 != 0 || p[38]>>1&1 != 1 {
		t.Fatalf("payload %x", p)
	}
}

func TestParseClientPayload(t *testing.T) {
	var w bitWriter
	// stream 0: one reliable 21605 {u64 hero, bool, bool}: seq, ch 1, idx 5, 8 bytes, 2 bits
	w.bits(1, 1)
	w.bits(2, 4)
	w.bits(0, 4)
	hero := binary.LittleEndian.AppendUint64(nil, 0x02E0000000000029)
	w.bytes(append([]byte{9, 0, 0, 0, 1, 5}, hero...))
	w.bits(1, 1)
	w.bits(0, 1)
	// and a 21603 {u64 hero, u64, bool}, which starts at the next byte
	w.bytes(append(append([]byte{10, 0, 0, 0, 1, 3}, hero...), make([]byte, 8)...))
	w.bits(1, 1)
	// stream 3: present, u32 length, u16 timing, u8 K = 2
	w.bits(1, 1)
	w.bits(16+8+32+80+32+8+16, 32)
	w.bits(654, 16)
	w.bits(2, 8)
	w.bits(100, 32) // frame
	w.bits(5, 16)
	w.bits(6, 16)
	w.bits(1, 8)
	w.bits(0xff, 8) // aim1 aim2 move1 move2(-1)
	w.bits(0x10, 16)
	w.bits(0, 8)
	w.bits(654, 16) // buttons extra tail
	w.bits(101, 32) // frame
	w.bits(0x10, 8) // only buttons changed
	w.bits(0x30, 16)

	msgs, in, err := ParseClientPayload(w.buf)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("msgs %+v err %v", msgs, err)
	}
	if id, _ := msgs[1].ID(); id != 21603 || binary.LittleEndian.Uint64(msgs[1].Payload) != 0x02E0000000000029 {
		t.Fatalf("second msg %+v", msgs[1])
	}
	if id, _ := msgs[0].ID(); id != 21605 || binary.LittleEndian.Uint64(msgs[0].Payload) != 0x02E0000000000029 {
		t.Fatalf("msg %+v", msgs[0])
	}
	if in == nil || len(in.Entries) != 2 || in.Timing != 654 {
		t.Fatalf("input %+v", in)
	}
	e := in.Entries[1]
	if e.Frame != 101 || e.Aim1 != 5 || e.Move2 != -1 || e.Buttons != 0x30 || e.Tail != 654 {
		t.Fatalf("entry %+v", e)
	}
	if f, _ := in.Newest(); f != 101 {
		t.Fatalf("newest %d", f)
	}
}

func TestDestroyEncoding(t *testing.T) {
	var w bitWriter
	Spawn{ID: 2, Destroy: true}.writeEntityData(&w, 0)
	if w.nbit != 2 || w.buf[0] != 3 {
		t.Fatalf("destroy = %d bits %x", w.nbit, w.buf)
	}
}

func TestHealthUpdate(t *testing.T) {
	u := PlayerHealth(200)
	if u.ID != 0x28 || len(u.Mask) != 7 || u.Mask[0] != 1 || u.Values.nbit != 19*8 {
		t.Fatalf("%+v bits %d", u, u.Values.nbit)
	}
	var w bitWriter
	w.writeComponentUpdates([]ComponentUpdate{u})
	// u8 N, u8 id, then from byte 2: L=7, mask, then the part: enabled 1, max 200.0
	if w.buf[2] != 7 || w.buf[3] != 1 || binary.LittleEndian.Uint32(w.buf[10:]) != 1 ||
		math.Float32frombits(binary.LittleEndian.Uint32(w.buf[14:])) != 200 {
		t.Fatalf("%x", w.buf)
	}
}

func TestScriptFullUpdate(t *testing.T) {
	w := ScriptFullUpdate(1000, []ScriptGraph{{Index: 0x33, SyncVars: 4, RemoteNodes: 5}}, nil)
	r := &bitReader{b: w.buf}
	if r.bits(1) != 1 || r.bits(1) != 0 {
		t.Fatal("present bits")
	}
	if r.bits(1) != 0 || r.bits(16) != 0 { // start CF 0 (short form)
		t.Fatal("start CF")
	}
	if r.bits(1) != 1 || r.bits(1) != 0 || r.bits(16) != 1000 { // range: 1 0 + 16 bits
		t.Fatal("range")
	}
	if r.bits(1) != 0 {
		t.Fatal("optional")
	}
	if r.bits(1) != 0 {
		t.Fatal("payload length form")
	}
	n := r.bits(8)
	// payload: 2 header bits, a30(1)=5, u16, 2 bits, a30(0)=5, 7e0(0)=9, 4+5 zero bits, event bit 1
	if n != 2+5+16+2+5+9+9+1 {
		t.Fatalf("payload bits %d", n)
	}
	if r.bits(2) != 0 {
		t.Fatal("payload header (local flag)")
	}
	if r.bits(1) != 0 || r.bits(4) != 1 || r.bits(16) != 0x33 {
		t.Fatal("instance entry")
	}
}

func TestScriptLocalFullUpdate(t *testing.T) {
	graphs := []ScriptGraph{{Index: 0x043, SyncVars: 2, States: ".CS."}}
	w := ScriptLocalFullUpdate(5, graphs, map[int][]ScriptState{1: {{Index: 3, Data: []bool{true}}}})
	r := &bitReader{b: w.buf}
	if r.bits(1) != 1 || r.bits(1) != 0 || r.bits(1) != 0 || r.bits(16) != 0 {
		t.Fatal("packet header / start CF")
	}
	if r.bits(1) != 0 || r.bits(8) != 5 || r.bits(1) != 0 || r.bits(1) != 0 {
		t.Fatal("range / optional value")
	}
	n := r.bits(8)
	// header 3 bits + offset 9; instance 1+4, 16, 2; end 5; owner vars 9;
	// sync vars 2 + extra 9; states: '.' 1, 'C' 0, 'S' 1, '.' 1+1; events 2; final 1
	want := uint32(3 + 9 + 5 + 16 + 2 + 5 + 9 + 2 + 9 + 1 + 1 + 2 + 2 + 1)
	if n != want {
		t.Fatalf("payload %d bits, want %d", n, want)
	}
	p := &bitReader{b: w.buf, pos: r.pos}
	if p.bits(3) != 0b010 {
		t.Fatal("payload header: want ignored 0, local 1, +0x51 0")
	}
	p.pos = r.pos + uint(n) - 1 - 2 - 2
	if p.bits(2) != 0b11 || p.bits(2) != 3 || p.bits(1) != 0 {
		t.Fatal("last state (active + data), events end, cee920")
	}
}

func TestScriptSyncVars(t *testing.T) {
	g := ScriptGraph{SyncVars: 3, SyncVarIDs: []uint16{0xc7, 0x6c8, 0xdd},
		Vars: map[uint16]int32{0xc7: 200, 0xdd: 0, 0x78c: -1}}
	var w bitWriter
	w.syncVars(g, true)
	r := &bitReader{b: w.buf}
	// 00c7: changed, no state ref, tag 3 (s16), 200
	if r.bits(1) != 1 || r.bits(1) != 0 || r.bits(4) != 3 || r.bits(16) != 200 {
		t.Fatal("00c7")
	}
	if r.bits(1) != 0 {
		t.Fatal("06c8 unchanged")
	}
	// 00dd: changed, no state ref, tag 1 (u4), 0
	if r.bits(1) != 1 || r.bits(1) != 0 || r.bits(4) != 1 || r.bits(4) != 0 {
		t.Fatal("00dd")
	}
	// extra list: 7e0(0x78c) = 1 0 + 16 bits, value tag 2 (s8) 0xff, then 7e0(0)
	if r.bits(2) != 0b01 || r.bits(16) != 0x78c || r.bits(1) != 0 || r.bits(4) != 2 || r.bits(8) != 0xff {
		t.Fatal("extra 078c")
	}
	if r.bits(1) != 0 || r.bits(8) != 0 || r.pos != w.nbit {
		t.Fatal("extra list end")
	}
	var remote bitWriter
	remote.syncVars(g, false)
	if remote.nbit != 1+1+4+16+1+1+1+4+4 {
		t.Fatalf("remote: %d bits (no extra list)", remote.nbit)
	}
}

func TestServerEndsConnectionWithoutSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	pc.Close()
	go s.ListenAndServe(ctx, addr)
	time.Sleep(50 * time.Millisecond)

	// As after a restart: the assignment is known, but this process never saw the
	// client's SYN. Its data gets a FIN.
	key := bytes.Repeat([]byte{0x42}, 20)
	s.Assign(gamelink.Assignment{ConnID: 6, Key: key})
	client, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	rest := binary.LittleEndian.AppendUint32(nil, 6<<4|1)
	rest = binary.LittleEndian.AppendUint32(rest, 7)
	rest = append(rest, make([]byte, 30)...)
	client.Write(Sign(key, rest))
	buf := make([]byte, 2048)
	client.SetReadDeadline(time.Now().Add(time.Second))
	n, err := client.Read(buf)
	if c, ok := ParseControl(buf[:n]); err != nil || !ok || c.Flags != FlagFIN {
		t.Fatalf("want a FIN, got %x %v", buf[:n], err)
	}
}

// A u64 mirror field: byte mask of the non-zero bytes, then those bytes (FUN_7ff650e29d40).
func TestCompressedU64(t *testing.T) {
	for x, want := range map[uint64][]byte{0: {0}, 0x80000: {0b100, 0x08}, 0x400001: {0b101, 0x01, 0x40}} {
		if got := compressedU64(x).buf; !bytes.Equal(got, want) {
			t.Errorf("%#x: %x, want %x", x, got, want)
		}
	}
}
