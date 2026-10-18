package main

import (
	"encoding/binary"
	"testing"
)

func TestWAVHeader(t *testing.T) {
	pcm := [][]float32{{0, 0.5, -1}, {1, -0.5, 0}}
	b := wav(48000, pcm)
	le := binary.LittleEndian
	if string(b[0:4]) != "RIFF" || string(b[8:16]) != "WAVEfmt " || string(b[36:40]) != "data" {
		t.Fatalf("header %q", b[:44])
	}
	if le.Uint32(b[4:]) != uint32(len(b)-8) || le.Uint32(b[16:]) != 16 || le.Uint16(b[20:]) != 1 || le.Uint16(b[22:]) != 2 ||
		le.Uint32(b[24:]) != 48000 || le.Uint32(b[28:]) != 48000*4 || le.Uint16(b[32:]) != 4 || le.Uint16(b[34:]) != 16 ||
		le.Uint32(b[40:]) != 12 || len(b) != 44+12 {
		t.Fatalf("header fields %x", b[:44])
	}
	// interleaved L R: 0, 1.0, 0.5, -0.5, -1, 0
	want := []int16{0, 32767, 16384, -16384, -32767, 0}
	for i, w := range want {
		if got := int16(le.Uint16(b[44+2*i:])); got != w {
			t.Errorf("sample %d = %d, want %d", i, got, w)
		}
	}
	p := pcm16(22050, pcm)
	if string(p[:4]) != "OWPC" || le.Uint32(p[4:]) != 2 || le.Uint32(p[8:]) != 22050 || le.Uint32(p[12:]) != 3 || len(p) != 16+12 {
		t.Fatalf("pcm16 header %x", p[:16])
	}
	if int16(le.Uint16(p[16+2*3:])) != 32767 { // planar: channel 1 starts after channel 0's 3 samples
		t.Error("pcm16 not planar")
	}
}
