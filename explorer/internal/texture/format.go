package texture

import "fmt"

// Format describes a DXGI format the textures use (docs/AssetFormats.md §1.3).
type Format struct {
	DXGI     uint8
	Name     string
	Block    int  // 4 for block-compressed formats, else 1
	Bytes    int  // bytes per block (BC) or per pixel
	Channels int  // 1..4: what the format stores
	HDR      bool // float data: decoded to float, tone-mapped for display
	SRGB     bool
	decode   func(dst []float32, src []byte, w, h int) // RGBA float32 per pixel, row-major
}

// formats: the DXGI formats found in the 0.8 install (every texture's +0x03, [D]).
var formats = map[uint8]*Format{
	2:  {2, "R32G32B32A32_FLOAT", 1, 16, 4, true, false, decodeRGBA32F},
	10: {10, "R16G16B16A16_FLOAT", 1, 8, 4, true, false, decodeRGBA16F},
	28: {28, "R8G8B8A8_UNORM", 1, 4, 4, false, false, decodeRGBA8},
	29: {29, "R8G8B8A8_UNORM_SRGB", 1, 4, 4, false, true, decodeRGBA8},
	34: {34, "R16G16_FLOAT", 1, 4, 2, true, false, decodeRG16F},
	49: {49, "R8G8_UNORM", 1, 2, 2, false, false, decodeRG8},
	54: {54, "R16_FLOAT", 1, 2, 1, true, false, decodeR16F},
	61: {61, "R8_UNORM", 1, 1, 1, false, false, decodeR8},
	71: {71, "BC1_UNORM", 4, 8, 4, false, false, decodeBC1},
	72: {72, "BC1_UNORM_SRGB", 4, 8, 4, false, true, decodeBC1},
	77: {77, "BC3_UNORM", 4, 16, 4, false, false, decodeBC3},
	78: {78, "BC3_UNORM_SRGB", 4, 16, 4, false, true, decodeBC3},
	80: {80, "BC4_UNORM", 4, 8, 1, false, false, decodeBC4},
	83: {83, "BC5_UNORM", 4, 16, 2, false, false, decodeBC5},
	95: {95, "BC6H_UF16", 4, 16, 3, true, false, decodeBC6H},
}

// LookupFormat returns the format for a DXGI value, or nil if there is no decoder.
func LookupFormat(dxgi uint8) *Format { return formats[dxgi] }

// FormatName names a DXGI value, also one without a decoder.
func FormatName(dxgi uint8) string {
	if f := formats[dxgi]; f != nil {
		return f.Name
	}
	return fmt.Sprintf("DXGI %d", dxgi)
}

// MipBytes is the size of one surface of a w × h mip.
func (f *Format) MipBytes(w, h int) int {
	if f.Block == 1 {
		return w * h * f.Bytes
	}
	return (w + 3) / 4 * ((h + 3) / 4) * f.Bytes
}

// Decode decodes one surface of a w × h mip to RGBA float32 (LDR formats in 0..1; single- and two-channel formats
// leave the missing channels at 0 and alpha at 1).
func (f *Format) Decode(src []byte, w, h int) ([]float32, error) {
	if n := f.MipBytes(w, h); len(src) < n {
		return nil, fmt.Errorf("texture: %s %dx%d needs %d bytes, have %d", f.Name, w, h, n, len(src))
	}
	dst := make([]float32, w*h*4)
	f.decode(dst, src, w, h)
	return dst, nil
}
