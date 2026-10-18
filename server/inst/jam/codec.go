package jam

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"owserver/inst/bits"
)

// The JAM binary encoding (the client's decoder FUN_7ff650f0c7d0 walks a message's
// field table, FUN_7ff650f0ce00):
//
//   - fields in table order, little-endian, no tags or names;
//   - bool: one bit, LSB first; consecutive bools share a byte (also across nested
//     struct boundaries); every other field starts at the next whole byte;
//   - str: NUL-terminated; bytes: u32 length + bytes; blob: a fixed number of bytes;
//   - tid (tagged ID): u8 kind 1..5, then 1/2/4/8/16 bytes;
//   - arrays: dynamic = u32 count + elements, fixed = elements only.
//
// That is exactly a bit stream where non-bool fields align first, so one codec
// serves both transports: lobby frames (a message = its bits, padded to a byte) and
// the instance data stream (messages follow each other in one bit stream, a
// message's trailing bools are bits of it).

func putU8(w *bits.Writer, v uint8)   { w.Bytes([]byte{v}) }
func putI8(w *bits.Writer, v int8)    { putU8(w, uint8(v)) }
func putChar(w *bits.Writer, v byte)  { putU8(w, v) }
func putU16(w *bits.Writer, v uint16) { w.Bytes(binary.LittleEndian.AppendUint16(nil, v)) }
func putI16(w *bits.Writer, v int16)  { putU16(w, uint16(v)) }
func putU32(w *bits.Writer, v uint32) { w.Bytes(binary.LittleEndian.AppendUint32(nil, v)) }
func putI32(w *bits.Writer, v int32)  { putU32(w, uint32(v)) }
func putU64(w *bits.Writer, v uint64) { w.Bytes(binary.LittleEndian.AppendUint64(nil, v)) }
func putI64(w *bits.Writer, v int64)  { putU64(w, uint64(v)) }
func putF32(w *bits.Writer, v float32) {
	putU32(w, math.Float32bits(v))
}
func putF64(w *bits.Writer, v float64) { putU64(w, math.Float64bits(v)) }
func putCount(w *bits.Writer, n int)   { putU32(w, uint32(n)) }

// putStr writes a NUL-terminated string. A string containing NUL is cut there
// (the client would stop reading at it).
func putStr(w *bits.Writer, s string) {
	b := []byte(s)
	for i, c := range b {
		if c == 0 {
			b = b[:i]
			break
		}
	}
	w.Bytes(append(b, 0))
}

func putBytes(w *bits.Writer, b []byte) {
	putCount(w, len(b))
	w.Bytes(b)
}

// decoder wraps the reader with an error for failures the reader doesn't track
// (a string without NUL, an impossible count, a bad tagged-ID kind).
type decoder struct {
	r    *bits.Reader
	fail error
}

func (d *decoder) err() error {
	if d.fail != nil {
		return d.fail
	}
	if err := d.r.Err(); err != nil {
		return err
	}
	return nil
}

func (d *decoder) setErr(err error) {
	if d.fail == nil {
		d.fail = err
	}
	d.r.Seek(uint(len(d.r.Bytes())) * 8) // stop reading
}

func (d *decoder) bytes(n int) []byte {
	b, err := d.r.ReadBytes(n)
	if err != nil {
		return make([]byte, n)
	}
	return b
}

func (d *decoder) getU8() uint8    { return d.bytes(1)[0] }
func (d *decoder) getI8() int8     { return int8(d.getU8()) }
func (d *decoder) getChar() byte   { return d.getU8() }
func (d *decoder) getU16() uint16  { return binary.LittleEndian.Uint16(d.bytes(2)) }
func (d *decoder) getI16() int16   { return int16(d.getU16()) }
func (d *decoder) getU32() uint32  { return binary.LittleEndian.Uint32(d.bytes(4)) }
func (d *decoder) getI32() int32   { return int32(d.getU32()) }
func (d *decoder) getU64() uint64  { return binary.LittleEndian.Uint64(d.bytes(8)) }
func (d *decoder) getI64() int64   { return int64(d.getU64()) }
func (d *decoder) getF32() float32 { return math.Float32frombits(d.getU32()) }
func (d *decoder) getF64() float64 { return math.Float64frombits(d.getU64()) }
func (d *decoder) blob(dst []byte) { copy(dst, d.bytes(len(dst))) }

// ErrBadCount is returned for an array count larger than the bits left.
var ErrBadCount = errors.New("jam: array count exceeds the payload")

// count reads a dynamic array's u32 count; minBits is the smallest element size.
func (d *decoder) count(minBits int) int {
	n := d.getU32()
	if d.err() != nil {
		return 0
	}
	limit := uint64(d.r.Left())
	if minBits > 0 {
		limit /= uint64(minBits)
	} else if limit < 1<<16 {
		limit = 1 << 16 // zero-size elements: any sane count
	}
	if uint64(n) > limit {
		d.setErr(fmt.Errorf("%w (%d)", ErrBadCount, n))
		return 0
	}
	return int(n)
}

func (d *decoder) getStr() string {
	d.r.Align()
	start := d.r.Pos() / 8
	b := d.r.Bytes()
	for i := start; i < uint(len(b)); i++ {
		if b[i] == 0 {
			d.r.Seek((i + 1) * 8)
			return string(b[start:i])
		}
	}
	d.setErr(errors.New("jam: string without NUL"))
	return ""
}

func (d *decoder) getBytes() []byte {
	n := d.getU32()
	if d.err() != nil {
		return nil
	}
	if uint64(n)*8 > uint64(d.r.Left()) {
		d.setErr(fmt.Errorf("%w (%d bytes)", ErrBadCount, n))
		return nil
	}
	return append([]byte(nil), d.bytes(int(n))...)
}

// JamID is JAM's 128-bit address {seq, src}: players, parties, games, chat
// channels (every 16-byte ID of the lobby protocols). The wire form is the 16
// bytes seq (LE) || src (LE), i.e. the handshake trailer's byte order.
type JamID struct{ Seq, Src uint64 }

// JamIDFromBytes reads a 16-byte ID (as the handshake trailer / lobbylogic carry it).
func JamIDFromBytes(b []byte) JamID {
	var id JamID
	if len(b) >= 16 {
		id.Seq = binary.LittleEndian.Uint64(b)
		id.Src = binary.LittleEndian.Uint64(b[8:])
	}
	return id
}

// Bytes is the 16-byte wire form.
func (id JamID) Bytes() []byte {
	return binary.LittleEndian.AppendUint64(binary.LittleEndian.AppendUint64(nil, id.Seq), id.Src)
}

func putJamID(w *bits.Writer, v JamID) { w.Bytes(v.Bytes()) }
func (d *decoder) getJamID() JamID     { return JamIDFromBytes(d.bytes(16)) }

// EntityID is a client entity ID ({u32 id} on the wire).
type EntityID uint32

func putEntityID(w *bits.Writer, v EntityID) { putU32(w, uint32(v)) }
func (d *decoder) getEntityID() EntityID     { return EntityID(d.getU32()) }

type Vec3 struct{ X, Y, Z float32 }
type Vec4 struct{ X, Y, Z, W float32 }

func putVec3(w *bits.Writer, v Vec3) { putF32(w, v.X); putF32(w, v.Y); putF32(w, v.Z) }
func putVec4(w *bits.Writer, v Vec4) { putF32(w, v.X); putF32(w, v.Y); putF32(w, v.Z); putF32(w, v.W) }
func (d *decoder) getVec3() Vec3     { return Vec3{d.getF32(), d.getF32(), d.getF32()} }
func (d *decoder) getVec4() Vec4     { return Vec4{d.getF32(), d.getF32(), d.getF32(), d.getF32()} }

// TaggedID is JAM type 16: a u8 kind 1..5 then 1/2/4/8/16 bytes (the client's
// decoder FUN_7ff650f0c7d0 case 0x10). No 0.8 message uses it; kept for completeness.
type TaggedID struct {
	Kind  uint8
	Value [16]byte // the low 1/2/4/8/16 bytes are used
}

var tidSizes = [...]int{1: 1, 2: 2, 3: 4, 4: 8, 5: 16}

func putTaggedID(w *bits.Writer, v TaggedID) {
	putU8(w, v.Kind)
	if v.Kind >= 1 && v.Kind <= 5 {
		w.Bytes(v.Value[:tidSizes[v.Kind]])
	}
}

func (d *decoder) getTaggedID() TaggedID {
	v := TaggedID{Kind: d.getU8()}
	if v.Kind < 1 || v.Kind > 5 {
		d.setErr(fmt.Errorf("jam: tagged ID kind %d", v.Kind))
		return v
	}
	copy(v.Value[:], d.bytes(tidSizes[v.Kind]))
	return v
}
