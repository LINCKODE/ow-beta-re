package texture

import "encoding/binary"

// BC6H_UF16 (unsigned half floats), after the D3D11 format specification.

// Endpoint fields: r0 g0 b0 r1 g1 b1 r2 g2 b2 r3 g3 b3 (endpoint*3 + channel).
const (
	r0 = iota
	g0
	b0
	r1
	g1
	b1
	r2
	g2
	b2
	r3
	g3
	b3
)

// seg reads bits of field f in stream order: first bit to position `to`, last to `from` (from > to for the usual
// LSB-first order, from < to for the specification's reversed fields).
type seg struct{ f, from, to uint8 }

type bc6Mode struct {
	modeBits  int
	two       bool // two regions (3-bit indices + partition) vs one (4-bit indices)
	transform bool // endpoints 1.. are deltas from endpoint 0
	epb       int
	delta     [3]int // delta bits per channel
	layout    []seg
}

var bc6Modes = map[uint8]*bc6Mode{
	0x00: {2, true, true, 10, [3]int{5, 5, 5}, []seg{{g2, 4, 4}, {b2, 4, 4}, {b3, 4, 4}, {r0, 9, 0}, {g0, 9, 0}, {b0, 9, 0}, {r1, 4, 0}, {g3, 4, 4}, {g2, 3, 0}, {g1, 4, 0}, {b3, 0, 0}, {g3, 3, 0}, {b1, 4, 0}, {b3, 1, 1}, {b2, 3, 0}, {r2, 4, 0}, {b3, 2, 2}, {r3, 4, 0}, {b3, 3, 3}}},
	0x01: {2, true, true, 7, [3]int{6, 6, 6}, []seg{{g2, 5, 5}, {g3, 4, 4}, {g3, 5, 5}, {r0, 6, 0}, {b3, 0, 0}, {b3, 1, 1}, {b2, 4, 4}, {g0, 6, 0}, {b2, 5, 5}, {b3, 2, 2}, {g2, 4, 4}, {b0, 6, 0}, {b3, 3, 3}, {b3, 5, 5}, {b3, 4, 4}, {r1, 5, 0}, {g2, 3, 0}, {g1, 5, 0}, {g3, 3, 0}, {b1, 5, 0}, {b2, 3, 0}, {r2, 5, 0}, {r3, 5, 0}}},
	0x02: {5, true, true, 11, [3]int{5, 4, 4}, []seg{{r0, 9, 0}, {g0, 9, 0}, {b0, 9, 0}, {r1, 4, 0}, {r0, 10, 10}, {g2, 3, 0}, {g1, 3, 0}, {g0, 10, 10}, {b3, 0, 0}, {g3, 3, 0}, {b1, 3, 0}, {b0, 10, 10}, {b3, 1, 1}, {b2, 3, 0}, {r2, 4, 0}, {b3, 2, 2}, {r3, 4, 0}, {b3, 3, 3}}},
	0x06: {5, true, true, 11, [3]int{4, 5, 4}, []seg{{r0, 9, 0}, {g0, 9, 0}, {b0, 9, 0}, {r1, 3, 0}, {r0, 10, 10}, {g3, 4, 4}, {g2, 3, 0}, {g1, 4, 0}, {g0, 10, 10}, {g3, 3, 0}, {b1, 3, 0}, {b0, 10, 10}, {b3, 1, 1}, {b2, 3, 0}, {r2, 3, 0}, {b3, 0, 0}, {b3, 2, 2}, {r3, 3, 0}, {g2, 4, 4}, {b3, 3, 3}}},
	0x0A: {5, true, true, 11, [3]int{4, 4, 5}, []seg{{r0, 9, 0}, {g0, 9, 0}, {b0, 9, 0}, {r1, 3, 0}, {r0, 10, 10}, {b2, 4, 4}, {g2, 3, 0}, {g1, 3, 0}, {g0, 10, 10}, {b3, 0, 0}, {g3, 3, 0}, {b1, 4, 0}, {b0, 10, 10}, {b2, 3, 0}, {r2, 3, 0}, {b3, 1, 1}, {b3, 2, 2}, {r3, 3, 0}, {b3, 4, 4}, {b3, 3, 3}}},
	0x0E: {5, true, true, 9, [3]int{5, 5, 5}, []seg{{r0, 8, 0}, {b2, 4, 4}, {g0, 8, 0}, {g2, 4, 4}, {b0, 8, 0}, {b3, 4, 4}, {r1, 4, 0}, {g3, 4, 4}, {g2, 3, 0}, {g1, 4, 0}, {b3, 0, 0}, {g3, 3, 0}, {b1, 4, 0}, {b3, 1, 1}, {b2, 3, 0}, {r2, 4, 0}, {b3, 2, 2}, {r3, 4, 0}, {b3, 3, 3}}},
	0x12: {5, true, true, 8, [3]int{6, 5, 5}, []seg{{r0, 7, 0}, {g3, 4, 4}, {b2, 4, 4}, {g0, 7, 0}, {b3, 2, 2}, {g2, 4, 4}, {b0, 7, 0}, {b3, 3, 3}, {b3, 4, 4}, {r1, 5, 0}, {g2, 3, 0}, {g1, 4, 0}, {b3, 0, 0}, {g3, 3, 0}, {b1, 4, 0}, {b3, 1, 1}, {b2, 3, 0}, {r2, 5, 0}, {r3, 5, 0}}},
	0x16: {5, true, true, 8, [3]int{5, 6, 5}, []seg{{r0, 7, 0}, {b3, 0, 0}, {b2, 4, 4}, {g0, 7, 0}, {g2, 5, 5}, {g2, 4, 4}, {b0, 7, 0}, {g3, 5, 5}, {b3, 4, 4}, {r1, 4, 0}, {g3, 4, 4}, {g2, 3, 0}, {g1, 5, 0}, {g3, 3, 0}, {b1, 4, 0}, {b3, 1, 1}, {b2, 3, 0}, {r2, 4, 0}, {b3, 2, 2}, {r3, 4, 0}, {b3, 3, 3}}},
	0x1A: {5, true, true, 8, [3]int{5, 5, 6}, []seg{{r0, 7, 0}, {b3, 1, 1}, {b2, 4, 4}, {g0, 7, 0}, {b2, 5, 5}, {g2, 4, 4}, {b0, 7, 0}, {b3, 5, 5}, {b3, 4, 4}, {r1, 4, 0}, {g3, 4, 4}, {g2, 3, 0}, {g1, 4, 0}, {b3, 0, 0}, {g3, 3, 0}, {b1, 5, 0}, {b2, 3, 0}, {r2, 4, 0}, {b3, 2, 2}, {r3, 4, 0}, {b3, 3, 3}}},
	0x1E: {5, true, false, 6, [3]int{6, 6, 6}, []seg{{r0, 5, 0}, {g3, 4, 4}, {b3, 0, 0}, {b3, 1, 1}, {b2, 4, 4}, {g0, 5, 0}, {g2, 5, 5}, {b2, 5, 5}, {b3, 2, 2}, {g2, 4, 4}, {b0, 5, 0}, {g3, 5, 5}, {b3, 3, 3}, {b3, 5, 5}, {b3, 4, 4}, {r1, 5, 0}, {g2, 3, 0}, {g1, 5, 0}, {g3, 3, 0}, {b1, 5, 0}, {b2, 3, 0}, {r2, 5, 0}, {r3, 5, 0}}},
	0x03: {5, false, false, 10, [3]int{10, 10, 10}, []seg{{r0, 9, 0}, {g0, 9, 0}, {b0, 9, 0}, {r1, 9, 0}, {g1, 9, 0}, {b1, 9, 0}}},
	0x07: {5, false, true, 11, [3]int{9, 9, 9}, []seg{{r0, 9, 0}, {g0, 9, 0}, {b0, 9, 0}, {r1, 8, 0}, {r0, 10, 10}, {g1, 8, 0}, {g0, 10, 10}, {b1, 8, 0}, {b0, 10, 10}}},
	0x0B: {5, false, true, 12, [3]int{8, 8, 8}, []seg{{r0, 9, 0}, {g0, 9, 0}, {b0, 9, 0}, {r1, 7, 0}, {r0, 10, 11}, {g1, 7, 0}, {g0, 10, 11}, {b1, 7, 0}, {b0, 10, 11}}},
	0x0F: {5, false, true, 16, [3]int{4, 4, 4}, []seg{{r0, 9, 0}, {g0, 9, 0}, {b0, 9, 0}, {r1, 3, 0}, {r0, 10, 15}, {g1, 3, 0}, {g0, 10, 15}, {b1, 3, 0}, {b0, 10, 15}}},
}

// Partition shapes for two subsets (bit i = subset of pixel i) and the second subset's anchor pixel.
var bc6Shapes = [32]uint16{
	0xCCCC, 0x8888, 0xEEEE, 0xECC8, 0xC880, 0xFEEC, 0xFEC8, 0xEC80, 0xC800, 0xFFEC, 0xFE80, 0xE800, 0xFFE8, 0xFF00, 0xFFF0, 0xF000,
	0xF710, 0x008E, 0x7100, 0x08CE, 0x008C, 0x7310, 0x3100, 0x8CCE, 0x088C, 0x3110, 0x6666, 0x366C, 0x17E8, 0x0FF0, 0x718E, 0x399C,
}
var bc6Anchor2 = [32]int{
	15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15,
	15, 2, 8, 2, 2, 8, 8, 15, 2, 8, 2, 2, 8, 8, 2, 2,
}

var (
	weights3 = [8]int32{0, 9, 18, 27, 37, 46, 55, 64}
	weights4 = [16]int32{0, 4, 9, 13, 17, 21, 26, 30, 34, 38, 43, 47, 51, 55, 60, 64}
)

type bits128 struct {
	lo, hi uint64
	pos    uint
}

func (b *bits128) read(n uint) uint32 {
	var v uint64
	for i := range n {
		p := b.pos + i
		var bit uint64
		if p < 64 {
			bit = b.lo >> p & 1
		} else {
			bit = b.hi >> (p - 64) & 1
		}
		v |= bit << i
	}
	b.pos += n
	return uint32(v)
}

func bc6Unquantize(v int32, epb int) int32 {
	switch {
	case epb >= 15:
		return v
	case v == 0:
		return 0
	case v == 1<<epb-1:
		return 0xFFFF
	}
	return (v<<16 + 0x8000) >> epb
}

func signExtend(v int32, bits int) int32 {
	s := 32 - bits
	return v << s >> s
}

func bc6Block(blk []byte, px *[16][4]float32) {
	br := bits128{lo: binary.LittleEndian.Uint64(blk), hi: binary.LittleEndian.Uint64(blk[8:])}
	mb := uint8(br.read(2))
	if mb > 1 {
		mb |= uint8(br.read(3)) << 2
	}
	m := bc6Modes[mb]
	if m == nil { // reserved mode: the block decodes to 0
		*px = [16][4]float32{}
		for i := range px {
			px[i][3] = 1
		}
		return
	}
	var e [12]int32
	for _, s := range m.layout {
		if s.from >= s.to {
			for b := s.to; b <= s.from; b++ {
				e[s.f] |= int32(br.read(1)) << b
			}
		} else {
			for b := int(s.to); b >= int(s.from); b-- {
				e[s.f] |= int32(br.read(1)) << b
			}
		}
	}
	n := 2 // endpoints
	if m.two {
		n = 4
	}
	mask := int32(1)<<m.epb - 1
	if m.transform {
		for i := 3; i < n*3; i++ {
			c := i % 3
			e[i] = (e[c] + signExtend(e[i], m.delta[c])) & mask
		}
	}
	for i := range n * 3 {
		e[i] = bc6Unquantize(e[i], m.epb)
	}
	shape, anchor2 := uint16(0), -1
	ibits := uint(4)
	if m.two {
		p := br.read(5)
		shape, anchor2, ibits = bc6Shapes[p], bc6Anchor2[p], 3
		br.pos = 82
	} else {
		br.pos = 65
	}
	for i := range 16 {
		nb := ibits
		if i == 0 || i == anchor2 {
			nb--
		}
		idx := br.read(nb)
		var w int32
		if m.two {
			w = weights3[idx]
		} else {
			w = weights4[idx]
		}
		sub := int(shape >> i & 1)
		for c := range 3 {
			a, b := e[sub*6+c], e[sub*6+3+c]
			v := ((64-w)*a + w*b + 32) >> 6
			px[i][c] = Half(uint16(v * 31 >> 6))
		}
		px[i][3] = 1
	}
}

func decodeBC6H(dst []float32, src []byte, w, h int) { blocks(dst, src, w, h, 16, bc6Block) }
