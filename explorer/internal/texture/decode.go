package texture

import (
	"encoding/binary"
	"math"
)

// Uncompressed formats.

func decodeRGBA8(dst []float32, src []byte, w, h int) {
	for i := range w * h * 4 {
		dst[i] = float32(src[i]) / 255
	}
}

func decodeRG8(dst []float32, src []byte, w, h int) {
	for i := range w * h {
		dst[i*4], dst[i*4+1], dst[i*4+3] = float32(src[i*2])/255, float32(src[i*2+1])/255, 1
	}
}

func decodeR8(dst []float32, src []byte, w, h int) {
	for i := range w * h {
		dst[i*4], dst[i*4+3] = float32(src[i])/255, 1
	}
}

func decodeRGBA32F(dst []float32, src []byte, w, h int) {
	for i := range w * h * 4 {
		dst[i] = math.Float32frombits(binary.LittleEndian.Uint32(src[i*4:]))
	}
}

func decodeRGBA16F(dst []float32, src []byte, w, h int) {
	for i := range w * h * 4 {
		dst[i] = Half(binary.LittleEndian.Uint16(src[i*2:]))
	}
}

func decodeRG16F(dst []float32, src []byte, w, h int) {
	for i := range w * h {
		dst[i*4] = Half(binary.LittleEndian.Uint16(src[i*4:]))
		dst[i*4+1] = Half(binary.LittleEndian.Uint16(src[i*4+2:]))
		dst[i*4+3] = 1
	}
}

func decodeR16F(dst []float32, src []byte, w, h int) {
	for i := range w * h {
		dst[i*4], dst[i*4+3] = Half(binary.LittleEndian.Uint16(src[i*2:])), 1
	}
}

// Half converts an IEEE 754 binary16 to float32.
func Half(v uint16) float32 {
	sign := uint32(v>>15) << 31
	exp := uint32(v>>10) & 0x1F
	man := uint32(v) & 0x3FF
	switch {
	case exp == 0 && man == 0:
		return math.Float32frombits(sign)
	case exp == 0: // subnormal
		f := float32(man) / 1024 / 16384
		if sign != 0 {
			return -f
		}
		return f
	case exp == 31:
		return math.Float32frombits(sign | 0x7F800000 | man<<13)
	}
	return math.Float32frombits(sign | (exp+112)<<23 | man<<13)
}

// Block-compressed formats: the decoders walk 4×4 blocks and clip at the mip's edge.

func blocks(dst []float32, src []byte, w, h, size int, block func(b []byte, px *[16][4]float32)) {
	var px [16][4]float32
	bw, bh := (w+3)/4, (h+3)/4
	for by := range bh {
		for bx := range bw {
			o := (by*bw + bx) * size
			block(src[o:o+size], &px)
			for y := range 4 {
				yy := by*4 + y
				if yy >= h {
					break
				}
				for x := range 4 {
					xx := bx*4 + x
					if xx >= w {
						break
					}
					copy(dst[(yy*w+xx)*4:], px[y*4+x][:])
				}
			}
		}
	}
}

func rgb565(c uint16) [3]float32 {
	return [3]float32{float32(c>>11&31) / 31, float32(c>>5&63) / 63, float32(c&31) / 31}
}

// bc1Color fills the colours of a BC1 (or, with alpha1 = false, the colour part of a BC2/3) block.
func bc1Color(b []byte, px *[16][4]float32, punchThrough bool) {
	c0, c1 := binary.LittleEndian.Uint16(b), binary.LittleEndian.Uint16(b[2:])
	a, bb := rgb565(c0), rgb565(c1)
	var pal [4][4]float32
	pal[0] = [4]float32{a[0], a[1], a[2], 1}
	pal[1] = [4]float32{bb[0], bb[1], bb[2], 1}
	if c0 > c1 || !punchThrough {
		for i := range 3 {
			pal[2][i] = (2*a[i] + bb[i]) / 3
			pal[3][i] = (a[i] + 2*bb[i]) / 3
		}
		pal[2][3], pal[3][3] = 1, 1
	} else {
		for i := range 3 {
			pal[2][i] = (a[i] + bb[i]) / 2
		}
		pal[2][3] = 1
		pal[3] = [4]float32{0, 0, 0, 0}
	}
	idx := binary.LittleEndian.Uint32(b[4:])
	for i := range 16 {
		px[i] = pal[idx>>(2*i)&3]
	}
}

// bc4Channel decodes a BC4 block (also BC3's alpha, BC5's channels) into channel ch.
func bc4Channel(b []byte, px *[16][4]float32, ch int) {
	e0, e1 := float32(b[0])/255, float32(b[1])/255
	var pal [8]float32
	pal[0], pal[1] = e0, e1
	if b[0] > b[1] {
		for i := 1; i < 7; i++ {
			pal[i+1] = (float32(7-i)*e0 + float32(i)*e1) / 7
		}
	} else {
		for i := 1; i < 5; i++ {
			pal[i+1] = (float32(5-i)*e0 + float32(i)*e1) / 5
		}
		pal[6], pal[7] = 0, 1
	}
	var bits uint64
	for i := range 6 {
		bits |= uint64(b[2+i]) << (8 * i)
	}
	for i := range 16 {
		px[i][ch] = pal[bits>>(3*i)&7]
	}
}

func decodeBC1(dst []float32, src []byte, w, h int) {
	blocks(dst, src, w, h, 8, func(b []byte, px *[16][4]float32) { bc1Color(b, px, true) })
}

func decodeBC3(dst []float32, src []byte, w, h int) {
	blocks(dst, src, w, h, 16, func(b []byte, px *[16][4]float32) {
		bc1Color(b[8:], px, false)
		bc4Channel(b, px, 3)
	})
}

func decodeBC4(dst []float32, src []byte, w, h int) {
	blocks(dst, src, w, h, 8, func(b []byte, px *[16][4]float32) {
		*px = [16][4]float32{}
		bc4Channel(b, px, 0)
		for i := range px {
			px[i][3] = 1
		}
	})
}

func decodeBC5(dst []float32, src []byte, w, h int) {
	blocks(dst, src, w, h, 16, func(b []byte, px *[16][4]float32) {
		*px = [16][4]float32{}
		bc4Channel(b, px, 0)
		bc4Channel(b[8:], px, 1)
		for i := range px {
			px[i][3] = 1
		}
	})
}
