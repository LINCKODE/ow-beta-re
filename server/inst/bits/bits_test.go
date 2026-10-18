package bits

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	var w Writer
	w.Bits(1, 1)
	w.Bits(0xabc, 12)
	w.Bytes([]byte{0xde, 0xad})
	w.Bools(true, false, true)
	w.Bits64(0x123456789a, 40)
	w.F32(1.5)
	r := NewReader(w.Buf())
	if r.Bits(1) != 1 || r.Bits(12) != 0xabc {
		t.Fatal("bits")
	}
	if b, err := r.ReadBytes(2); err != nil || !bytes.Equal(b, []byte{0xde, 0xad}) {
		t.Fatal("bytes", b, err)
	}
	if !r.Bool() || r.Bool() || !r.Bool() {
		t.Fatal("bools")
	}
	if r.Bits64(40) != 0x123456789a || r.F32() != 1.5 {
		t.Fatal("bits64/f32")
	}
	if r.Err() != nil || r.Pos() != w.Len() {
		t.Fatal("pos", r.Pos(), w.Len(), r.Err())
	}
	r.Bits(8)
	if r.Err() == nil {
		t.Fatal("overrun not reported")
	}
}

func TestLSBFirst(t *testing.T) {
	var w Writer
	w.Bits(1, 1)
	w.Bits(0b10, 2)
	if w.Buf()[0] != 0b101 {
		t.Fatalf("%08b", w.Buf()[0])
	}
	w.SetBits(1, 1, 1)
	if w.Buf()[0] != 0b111 {
		t.Fatalf("%08b", w.Buf()[0])
	}
	o := FromBytes([]byte{0xff}, 3)
	if o.Len() != 3 || o.Buf()[0] != 0b111 {
		t.Fatal("FromBytes")
	}
}
