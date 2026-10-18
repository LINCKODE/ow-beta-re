package texture

import (
	"encoding/binary"
	"fmt"
)

// splitParts is how a payload with FlagSplit stores its blocks [G: 0x97acd0]: the data is one run of planes,
// each holding one part of every block, in block order. Other formats (BC6H, uncompressed) are stored plainly.
var splitParts = map[uint8][]int{
	71: {4, 4}, 72: {4, 4}, // BC1: colour endpoints, colour indices
	77: {2, 6, 4, 4}, 78: {2, 6, 4, 4}, // BC3: alpha endpoints, alpha indices, colour endpoints, colour indices
	80: {2, 6}, 81: {2, 6}, // BC4: endpoints, indices
	83: {4, 12}, 84: {4, 12}, // BC5: endpoints (red, green), indices (red, green)
}

// Unsplit reassembles the blocks of a split payload (a copy); data of other formats is returned as is.
func Unsplit(dxgi uint8, data []byte) []byte {
	parts := splitParts[dxgi]
	if parts == nil {
		return data
	}
	if dxgi == 83 || dxgi == 84 { // BC5 planes hold both channels: block = R ep, R idx, G ep, G idx
		return unsplitBC5(data)
	}
	return unsplit(data, parts)
}

func unsplit(data []byte, parts []int) []byte {
	bs := 0
	for _, p := range parts {
		bs += p
	}
	n := max(1, len(data)/bs) // the client: max(1, size >> 3 or 4)
	out := make([]byte, n*bs)
	src := 0
	off := 0
	for _, p := range parts {
		for i := range n {
			if src+i*p+p <= len(data) {
				copy(out[i*bs+off:i*bs+off+p], data[src+i*p:])
			}
		}
		src += n * p
		off += p
	}
	return out
}

func unsplitBC5(data []byte) []byte {
	n := max(1, len(data)/16)
	out := make([]byte, n*16)
	if len(data) < n*16 {
		return out
	}
	ep, idx := data[:n*4], data[n*4:n*16]
	for i := range n {
		b := out[i*16:]
		copy(b[0:2], ep[i*4:])
		copy(b[2:8], idx[i*12:i*12+6])
		copy(b[8:10], ep[i*4+2:i*4+4])
		copy(b[10:16], idx[i*12+6:i*12+12])
	}
	return out
}

// applyDelta builds the data of payload i of a delta texture: payload i of the base texture (Header.Base), unsplit as
// BC3, with the colour half of some blocks replaced. Per mip from the payload's first: a bit mask of
// max(1, ⌈blocks / 8⌉) bytes (bit b = block b), then one u32 per set bit for the block's colour endpoints (+8), then
// one u32 per set bit for its colour indices (+12) [G: 0x97acd0].
func (t *Texture) applyDelta(read Reader, i int, p Payload, delta []byte) ([]byte, error) {
	bg := PayloadGUID(t.Header.Base, i)
	bb, err := read(bg)
	if err != nil {
		return nil, fmt.Errorf("base payload %016X: %w", bg, err)
	}
	bh, err := ParsePayloadHeader(bb)
	if err != nil {
		return nil, fmt.Errorf("base payload %016X: %w", bg, err)
	}
	if bh.Top != p.Header.Top || bh.Count != p.Header.Count {
		return nil, fmt.Errorf("base payload %016X holds mips %d+%d, the delta %d+%d", bg,
			bh.FirstMip(len(t.Mips)), bh.Count, p.First, p.Header.Count)
	}
	base := unsplit(bb[PayloadHdr:], splitParts[77])
	o, d := 0, 0 // offsets in base (blocks of 16 bytes) and delta
	for m := p.First; m < p.First+int(p.Header.Count); m++ {
		w, h := t.Header.MipDim(m)
		blocks := max(1, w>>2) * max(1, h>>2)
		mb := max(1, (blocks+7)>>3)
		if d+mb > len(delta) {
			return nil, fmt.Errorf("delta ends in mip %d's mask", m)
		}
		mask := delta[d : d+mb]
		d += mb
		for pass := range 2 {
			for b := range blocks {
				if mask[b>>3]>>(b&7)&1 == 0 {
					continue
				}
				if d+4 > len(delta) || o+b*16+16 > len(base) {
					return nil, fmt.Errorf("delta ends in mip %d", m)
				}
				binary.LittleEndian.PutUint32(base[o+b*16+8+pass*4:], binary.LittleEndian.Uint32(delta[d:]))
				d += 4
			}
		}
		o += blocks * 16
	}
	if d != len(delta) {
		return nil, fmt.Errorf("%d delta bytes left", len(delta)-d)
	}
	return base, nil
}
