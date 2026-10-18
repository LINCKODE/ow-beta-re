package wire

import (
	"encoding/binary"
	"encoding/hex"
	"testing"

	"owserver/inst/bits"
	"owserver/inst/jam"
)

// A captured client ping: ClientOutInstance 21608 on channel 1, index 8, then no input.
const capturedPing = "03002600000001084e00000002000000da680c0400000000da680c040000000000"

func TestParseCapturedPing(t *testing.T) {
	b, _ := hex.DecodeString(capturedPing)
	p, err := ParseClientPayload(b)
	if err != nil || len(p.Jam) != 1 {
		t.Fatalf("%v %+v", err, p)
	}
	e := p.Jam[0]
	ping, ok := e.Msg.(*jam.TimeSyncPing)
	if !ok || !e.Reliable || e.Seq != 0x26 || ping.N != 0x4e || len(ping.Times) != 2 || ping.Times[0] != 0x40c68da {
		t.Fatalf("%+v %+v", e, e.Msg)
	}
	var w bits.Writer
	if err := jam.WriteStream(&w, p.Jam); err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(w.Buf()); got != capturedPing[:len(got)] {
		t.Fatalf("re-encoded %s", got)
	}
}

// Same bytes as the R&D server's TestDataPayloadFramePrefix (server/internal/instance).
func TestServerPayloadHeader(t *testing.T) {
	// The R&D test sends a raw 2-byte payload on 20304; do the same.
	raw := &jam.Raw{Id: jam.IDTimeSyncPong, Payload: []byte{0x12, 0x14}}
	p := ServerPayload{Frame: 5, Jam: []jam.Envelope{{Reliable: true, Seq: 0x18, Msg: raw}}}
	b, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	want := "0b00" + "1800" + "18000000" + "0004" + "1214" + "01000000"
	if hex.EncodeToString(b) != want {
		t.Fatalf("got %x want %s", b, want)
	}
}

func TestServerPayloadRoundTrip(t *testing.T) {
	p := ServerPayload{Frame: 300000, InputAck: 299990, Clock: 0x9,
		Jam: []jam.Envelope{{Reliable: true, Seq: 3, Msg: &jam.LocalEntity{Entity: 0x40000001}}}}
	b, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	h, err := DecodeServerHeader(b)
	if err != nil {
		t.Fatal(err)
	}
	if h.Frame != 300000 || h.InputAck != 299990 || h.Clock != 9 || len(h.Jam) != 1 || h.Entities != 0 {
		t.Fatalf("%+v", h)
	}
	if le := h.Jam[0].Msg.(*jam.LocalEntity); le.Entity != 0x40000001 || h.Jam[0].Seq != 3 {
		t.Fatalf("%+v", le)
	}
	// 20301's trailing bool is a bit: the stream 3 varint starts right after it
	// (R&D TestJamBoolsAreBits: frame 1 -> the bool at bit 0 of byte 38, varint prefix at bit 1).
	p = ServerPayload{Frame: 1, Jam: []jam.Envelope{{Reliable: true, Msg: &jam.LocalEntity{Entity: 0x40000001}}}}
	b, _ = p.Encode()
	if len(b) < 39 || b[38]&1 != 0 || b[38]>>1&1 != 1 {
		t.Fatalf("payload %x", b)
	}
}

func TestEntityIDWidths(t *testing.T) {
	for _, c := range []struct {
		id   uint32
		bits uint
	}{{5, 10}, {300, 19}, {70000, 35}} {
		var w bits.Writer
		writeEntityID(&w, c.id)
		if w.Len() != c.bits {
			t.Errorf("id %d: %d bits, want %d", c.id, w.Len(), c.bits)
		}
	}
}

func TestParseClientPayload(t *testing.T) {
	var w bits.Writer
	w.Bits(1, 1)
	w.Bits(2, 4)
	w.Bits(0, 4)
	hero := binary.LittleEndian.AppendUint64(nil, 0x02E0000000000029)
	w.Bytes(append([]byte{9, 0, 0, 0, 1, 5}, hero...))
	w.Bits(1, 1)
	w.Bits(0, 1)
	w.Bytes(append(append([]byte{10, 0, 0, 0, 1, 3}, hero...), make([]byte, 8)...))
	w.Bits(1, 1)
	w.Bits(1, 1)
	w.Bits(16+8+32+80+32+8+16, 32)
	w.Bits(654, 16)
	w.Bits(2, 8)
	w.Bits(100, 32)
	w.Bits(5, 16)
	w.Bits(6, 16)
	w.Bits(1, 8)
	w.Bits(0xff, 8)
	w.Bits(0x10, 16)
	w.Bits(0, 8)
	w.Bits(654, 16)
	w.Bits(101, 32)
	w.Bits(0x10, 8)
	w.Bits(0x30, 16)

	p, err := ParseClientPayload(w.Buf())
	if err != nil || len(p.Jam) != 2 {
		t.Fatalf("%+v %v", p, err)
	}
	if s := p.Jam[0].Msg.(*jam.HeroSelect); s.Hero != 0x02E0000000000029 || !s.B1 || s.B2 {
		t.Fatalf("%+v", s)
	}
	if l := p.Jam[1].Msg.(*jam.HeroLockIn); l.Hero != 0x02E0000000000029 || !l.Flag {
		t.Fatalf("%+v", l)
	}
	in := p.Input
	if in == nil || len(in.Entries) != 2 || in.Timing != 654 {
		t.Fatalf("input %+v", in)
	}
	e := in.Entries[1]
	if e.Frame != 101 || e.Aim1 != 5 || e.Move2 != -1 || e.Buttons != 0x30 || e.Tail != 654 {
		t.Fatalf("entry %+v", e)
	}
}
