package audio

import "errors"

// errBits: a read ran past the end of the data.
var errBits = errors.New("audio: bitstream ends early")

// bitReader reads Vorbis-order bits: least significant bit of each byte first.
type bitReader struct {
	b   []byte
	pos int // in bits
	err error
}

func (r *bitReader) left() int { return len(r.b)*8 - r.pos }

// read returns n ≤ 32 bits. Past the end it returns 0 and sets err.
func (r *bitReader) read(n int) uint32 {
	if n == 0 {
		return 0
	}
	if r.pos+n > len(r.b)*8 {
		r.err = errBits
		r.pos = len(r.b) * 8
		return 0
	}
	var v uint64
	byteIdx, shift := r.pos>>3, uint(r.pos&7)
	for i := 0; i < 5 && byteIdx+i < len(r.b); i++ {
		v |= uint64(r.b[byteIdx+i]) << (8 * uint(i))
	}
	r.pos += n
	return uint32((v >> shift) & (1<<uint(n) - 1))
}

func (r *bitReader) bit() bool { return r.read(1) != 0 }

// bitWriter writes Vorbis-order bits.
type bitWriter struct {
	b    []byte
	nbit int
}

func (w *bitWriter) write(v uint32, n int) {
	for i := 0; i < n; i++ {
		if w.nbit&7 == 0 {
			w.b = append(w.b, 0)
		}
		if v>>uint(i)&1 != 0 {
			w.b[len(w.b)-1] |= 1 << uint(w.nbit&7)
		}
		w.nbit++
	}
}

func (w *bitWriter) flag(b bool) {
	if b {
		w.write(1, 1)
	} else {
		w.write(0, 1)
	}
}

// ilog is the number of bits needed to hold v (Vorbis I §9.2.1).
func ilog(v uint32) int {
	n := 0
	for v != 0 {
		n++
		v >>= 1
	}
	return n
}

// peek returns the next n bits without consuming them (zeros past the end).
func (r *bitReader) peek(n int) uint32 {
	var v uint64
	byteIdx, shift := r.pos>>3, uint(r.pos&7)
	for i := 0; i < 5 && byteIdx+i < len(r.b); i++ {
		v |= uint64(r.b[byteIdx+i]) << (8 * uint(i))
	}
	return uint32((v >> shift) & (1<<uint(n) - 1))
}
