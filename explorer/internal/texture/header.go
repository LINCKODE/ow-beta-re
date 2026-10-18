// Package texture reads the 0.8 client's textures (resource type 0x004) and their payloads (0x04D) and decodes
// them to images. Format: docs/AssetFormats.md §1.
package texture

import (
	"encoding/binary"
	"fmt"
)

// Resource types.
const (
	TypeTexture = 0x004
	TypePayload = 0x04D
)

// HeaderSize is the size of the texture header; inline data starts at InlineOffset.
const (
	HeaderSize   = 0x20
	InlineOffset = 0x80
	PayloadHdr   = 0x10
	MaxPayloads  = 3 // the loader keeps 3 payload handles per set
)

// Header flags (docs/AssetFormats.md §1.2).
const (
	FlagCube  = 0x0008 // 6 surfaces per cube (counts: [D])
	FlagSplit = 0x1000 // payload blocks are stored as planes (Unsplit) [G: 0x97acd0]
	FlagDelta = 0x2000 // payloads patch the payloads of the texture named by Header.Base [G: 0x97acd0]
)

// Header is the 32-byte texture header [G: construct 0x860150, texture object 0x868710].
type Header struct {
	Flags    uint16 // +0x00
	Mips     uint8  // +0x02, < 17
	Format   uint8  // +0x03, DXGI_FORMAT, < 100
	Surfaces uint8  // +0x04, array slices / cube faces
	Unk5     uint8  // +0x05
	Payloads uint8  // +0x06, 0 = the data is inline at +0x80
	Unk7     uint8  // +0x07
	Width    uint16 // +0x08
	Height   uint16 // +0x0A
	DataSize uint32 // +0x0C, inline data size (0 with payloads)
	Unk10    uint64 // +0x10, not read by the loader
	Base     uint64 // +0x18, a texture GUID when Flags & FlagDelta
}

// ParseHeader decodes and checks a texture header the way the loader does.
func ParseHeader(b []byte) (Header, error) {
	if len(b) < HeaderSize {
		return Header{}, fmt.Errorf("texture: %d bytes, header needs %d", len(b), HeaderSize)
	}
	h := Header{
		Flags: binary.LittleEndian.Uint16(b), Mips: b[2], Format: b[3], Surfaces: b[4], Unk5: b[5],
		Payloads: b[6], Unk7: b[7], Width: binary.LittleEndian.Uint16(b[8:]), Height: binary.LittleEndian.Uint16(b[10:]),
		DataSize: binary.LittleEndian.Uint32(b[12:]), Unk10: binary.LittleEndian.Uint64(b[16:]),
		Base: binary.LittleEndian.Uint64(b[24:]),
	}
	switch {
	case h.Format >= 100:
		return h, fmt.Errorf("texture: format out of range: %d", h.Format)
	case h.Mips > 16:
		return h, fmt.Errorf("texture: %d mips", h.Mips)
	case h.Width == 0:
		return h, fmt.Errorf("texture: invalid width (0)")
	case h.Height == 0:
		return h, fmt.Errorf("texture: invalid height (0)")
	case h.Payloads > MaxPayloads:
		return h, fmt.Errorf("texture: %d payloads", h.Payloads)
	}
	return h, nil
}

// MipCount is the number of mips (a header value of 0 is treated as 1).
func (h Header) MipCount() int { return max(int(h.Mips), 1) }

// SurfaceCount is the number of surfaces (0 → 1).
func (h Header) SurfaceCount() int { return max(int(h.Surfaces), 1) }

// MipDim is the size of mip m.
func (h Header) MipDim(m int) (int, int) {
	return max(int(h.Width)>>m, 1), max(int(h.Height)>>m, 1)
}

// PayloadGUID is the GUID of payload i of texture guid: type 0x04D, payload index in bits 32..35, the
// texture's index (low 32 bits). The texture's bits 32..47 (0x000F on the low-detail variants) are dropped [G: 0x860150].
func PayloadGUID(guid uint64, i int) uint64 {
	return 0x0320000000000000 | uint64(i&0xF)<<32 | guid&0xFFFFFFFF
}

// PayloadHeader is the 16-byte header of a payload (0x04D) [D].
type PayloadHeader struct {
	Top   uint32 // index, counted from the smallest mip, of the largest mip in the payload
	Count uint32 // mips in the payload
	Size  uint32 // data size after the header
	HSize uint32 // header size (0x10)
}

// ParsePayloadHeader decodes a payload's header.
func ParsePayloadHeader(b []byte) (PayloadHeader, error) {
	if len(b) < PayloadHdr {
		return PayloadHeader{}, fmt.Errorf("payload: %d bytes", len(b))
	}
	p := PayloadHeader{binary.LittleEndian.Uint32(b), binary.LittleEndian.Uint32(b[4:]),
		binary.LittleEndian.Uint32(b[8:]), binary.LittleEndian.Uint32(b[12:])}
	if p.HSize != PayloadHdr || uint64(p.HSize)+uint64(p.Size) != uint64(len(b)) {
		return p, fmt.Errorf("payload: header size %d, data size %d, %d bytes", p.HSize, p.Size, len(b))
	}
	return p, nil
}

// FirstMip is the largest mip (0 = full size) a payload holds for a texture with mips mips.
func (p PayloadHeader) FirstMip(mips int) int { return mips - 1 - int(p.Top) }
