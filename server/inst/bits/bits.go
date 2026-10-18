// Package bits is the bit stream the client's network layer reads and writes: bits
// are packed LSB first within each byte (FUN_7ff650e292e0 / FUN_7ff650e29350), and
// byte fields (JAM payload fields, component masks) start at the next whole byte.
// Every inst package that writes or reads the wire uses it.
package bits

import (
	"errors"
	"math"
)

// Writer appends bits to a byte buffer. The zero value is ready to use.
type Writer struct {
	buf  []byte
	nbit uint
}

// Bits writes the low n bits of v (n <= 32), LSB first.
func (w *Writer) Bits(v uint32, n uint) {
	for i := uint(0); i < n; i++ {
		if w.nbit%8 == 0 {
			w.buf = append(w.buf, 0)
		}
		if v>>i&1 != 0 {
			w.buf[len(w.buf)-1] |= 1 << (w.nbit % 8)
		}
		w.nbit++
	}
}

// Bits64 writes the low n bits of v (n <= 64).
func (w *Writer) Bits64(v uint64, n uint) {
	if n > 32 {
		w.Bits(uint32(v), 32)
		w.Bits(uint32(v>>32), n-32)
		return
	}
	w.Bits(uint32(v), n)
}

// Bool writes one bit.
func (w *Writer) Bool(b bool) {
	if b {
		w.Bits(1, 1)
	} else {
		w.Bits(0, 1)
	}
}

// Bools writes one bit per bool.
func (w *Writer) Bools(bs ...bool) {
	for _, b := range bs {
		w.Bool(b)
	}
}

// F32 writes floats as 32 raw bits each.
func (w *Writer) F32(fs ...float32) {
	for _, f := range fs {
		w.Bits(math.Float32bits(f), 32)
	}
}

// Align pads to the next byte boundary.
func (w *Writer) Align() { w.nbit = uint(len(w.buf)) * 8 }

// Bytes pads to a byte boundary and appends b.
func (w *Writer) Bytes(b []byte) {
	w.Align()
	w.buf = append(w.buf, b...)
	w.nbit += uint(len(b)) * 8
}

// Append copies all of o's bits at w's position (no alignment).
func (w *Writer) Append(o *Writer) {
	if o == nil {
		return
	}
	for i := uint(0); i < o.nbit; i++ {
		w.Bits(uint32(o.buf[i/8]>>(i%8)&1), 1)
	}
}

// SetBits overwrites n bits at bit position pos (already written).
func (w *Writer) SetBits(pos uint, v uint32, n uint) {
	for i := uint(0); i < n; i++ {
		byteI, bit := (pos+i)/8, (pos+i)%8
		if v>>i&1 != 0 {
			w.buf[byteI] |= 1 << bit
		} else {
			w.buf[byteI] &^= 1 << bit
		}
	}
}

// Len is the number of bits written.
func (w *Writer) Len() uint { return w.nbit }

// Buf returns the bytes written (the last one zero-padded). It aliases the writer's buffer.
func (w *Writer) Buf() []byte { return w.buf }

// FromBytes is a writer holding the first n bits of b (e.g. a ready-made payload).
func FromBytes(b []byte, n uint) *Writer {
	if n > uint(len(b))*8 {
		n = uint(len(b)) * 8
	}
	w := &Writer{}
	w.Append(&Writer{buf: b, nbit: n})
	return w
}

// ErrOverrun is the Reader's sticky error after reading past the end.
var ErrOverrun = errors.New("bits: read past the end")

// Reader reads a bit stream written like Writer. Reading past the end yields zero
// bits and sets Err.
type Reader struct {
	b   []byte
	pos uint
	err error
}

func NewReader(b []byte) *Reader { return &Reader{b: b} }

// Bits reads n bits (n <= 32).
func (r *Reader) Bits(n uint) uint32 {
	var v uint32
	for i := uint(0); i < n; i++ {
		byteI := r.pos / 8
		if byteI >= uint(len(r.b)) {
			r.err = ErrOverrun
		} else if r.b[byteI]>>(r.pos%8)&1 != 0 {
			v |= 1 << i
		}
		r.pos++
	}
	return v
}

// Bits64 reads n bits (n <= 64).
func (r *Reader) Bits64(n uint) uint64 {
	if n > 32 {
		lo := uint64(r.Bits(32))
		return lo | uint64(r.Bits(n-32))<<32
	}
	return uint64(r.Bits(n))
}

func (r *Reader) Bool() bool { return r.Bits(1) != 0 }

func (r *Reader) F32() float32 { return math.Float32frombits(r.Bits(32)) }

// Align skips to the next byte boundary.
func (r *Reader) Align() { r.pos = (r.pos + 7) / 8 * 8 }

// ReadBytes aligns and reads n whole bytes.
func (r *Reader) ReadBytes(n int) ([]byte, error) {
	r.Align()
	start := r.pos / 8
	if n < 0 || int(start)+n > len(r.b) {
		r.err = ErrOverrun
		r.pos = uint(len(r.b)) * 8
		return nil, ErrOverrun
	}
	r.pos += uint(n) * 8
	return r.b[start : int(start)+n], nil
}

// Rest aligns and returns every byte left (without consuming them).
func (r *Reader) Rest() []byte {
	r.Align()
	if start := r.pos / 8; start < uint(len(r.b)) {
		return r.b[start:]
	}
	return nil
}

// Pos is the bit position; Seek sets it.
func (r *Reader) Pos() uint     { return r.pos }
func (r *Reader) Seek(pos uint) { r.pos = pos }
func (r *Reader) Left() int     { return len(r.b)*8 - int(r.pos) }
func (r *Reader) Err() error    { return r.err }
func (r *Reader) Bytes() []byte { return r.b }
