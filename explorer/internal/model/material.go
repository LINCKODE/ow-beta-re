package model

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"

	"owstu/stu"
)

// Material is a parsed material (0x008): AssetFormats.md §3.
type Material struct {
	Shader   uint64       `json:"shader"` // +0x00, a 0x088 GUID from a fixed set (checked by the construct)
	Ref      uint64       `json:"ref"`    // +0x08, 0 or a 0x03A GUID [?]
	Offsets  [6]uint64    `json:"offsets"`
	Color    [4]float32   `json:"color"`   // +0x40 [?: a tint; mostly white-ish]
	Flags68  uint8        `json:"flags68"` // +0x68: bit 1 = helper / editor material (AssetFormats.md §3.3) [D]
	Textures []TextureRef `json:"textures"`
	Buffers  []Buffer     `json:"buffers"`
	Params   []Param      `json:"params"` // +0x10 parameter stream, u16 count at +0x56 (T071)
}

// Param is one item of the material's parameter stream (+0x10, u16 count +0x56): {u32 hash, u8 type, u8 count,
// u16 0, data}. The client writes it into the shader instance's material constant buffer by variable hash
// (Rendering.md §9.4). Types [D: sizes fit on all 11,150 materials]: 2 / 3 u32, 6 float, 8 float2, 10 float4, 11 float4 (colours).
type Param struct {
	Hash   uint32    `json:"hash"`
	Type   uint8     `json:"type"`
	Values []float32 `json:"values"` // floats, or the u32 values converted (types 2, 3)
}

var paramSize = map[uint8]int{2: 4, 3: 4, 6: 4, 8: 8, 10: 16, 11: 16}

// TextureRef is one 16-byte texture binding.
type TextureRef struct {
	GUID  uint64 `json:"guid"`
	Slot  uint32 `json:"slot"` // CRC32 of the lower-cased slot name
	Flags uint32 `json:"flags"`
}

// Buffer is a constant buffer: a 16-byte record pointing at its data.
type Buffer struct {
	Hash   uint32    `json:"hash"`
	A, B   uint8     `json:"-"`
	Size   uint16    `json:"size"`
	Offset uint64    `json:"offset"`
	Floats []float32 `json:"-"` // the data as floats
}

// Slot names: recovered (the CRC32 of the lower-cased name matches) or inferred from the textures bound to them
// (AssetFormats.md §3.3). Inferred names end in "?".
var slotNames = map[uint32]string{}

func init() {
	for _, n := range []string{"basecolor", "normal", "normalmap", "opacity", "color", "ao"} {
		slotNames[crc32.ChecksumIEEE([]byte(n))] = n
	}
	// T071 recovered more by CRC (preview.go recoveredSlots): 0x169615AA basenormal, 0xBCBE747D baseemissive,
	// 0xC6D17199 baseao; 0x20AF06CE is still inferred (preview.go).
	for h, n := range map[uint32]string{
		0x2ce679ff: "lighting (HDR)?", // RGBA16F / RG16F / R16F
		0x12bbc7d3: "lighting 2 (HDR)?",
	} {
		slotNames[h] = n
	}
}

// SlotName names a slot hash ("" if unknown).
func SlotName(h uint32) string { return slotNames[h] }

// Albedo is the CRC of "basecolor", the colour slot of most materials.
var (
	SlotBaseColor = crc32.ChecksumIEEE([]byte("basecolor"))
	SlotColor     = crc32.ChecksumIEEE([]byte("color"))
	SlotNormal    = crc32.ChecksumIEEE([]byte("basenormal"))
)

// ParseMaterial reads a material.
func ParseMaterial(b []byte) (*Material, error) {
	if len(b) < 0x70 {
		return nil, fmt.Errorf("material: %d bytes", len(b))
	}
	le := binary.LittleEndian
	m := &Material{Shader: le.Uint64(b), Ref: le.Uint64(b[8:])}
	for i := range 6 {
		m.Offsets[i] = le.Uint64(b[0x10+8*i:])
	}
	for i := range 4 {
		m.Color[i] = math.Float32frombits(le.Uint32(b[0x40+4*i:]))
	}
	m.Flags68 = b[0x68]
	nt, nb := int(b[0x60]), int(b[0x61])
	if nt > 0 {
		o := m.Offsets[2]
		if o+uint64(nt)*16 > uint64(len(b)) {
			return nil, fmt.Errorf("material: %d textures at 0x%x past the end", nt, o)
		}
		for i := range nt {
			p := b[o+uint64(16*i):]
			m.Textures = append(m.Textures, TextureRef{le.Uint64(p), le.Uint32(p[8:]), le.Uint32(p[12:])})
		}
	}
	if np := int(le.Uint16(b[0x56:])); np > 0 && m.Offsets[0] != 0 {
		o := int(m.Offsets[0])
		for range np {
			if o+8 > len(b) {
				return nil, fmt.Errorf("material: parameter stream past the end")
			}
			p := Param{Hash: le.Uint32(b[o:]), Type: b[o+4]}
			sz := paramSize[p.Type] * int(b[o+5])
			if sz == 0 || o+8+sz > len(b) {
				return nil, fmt.Errorf("material: parameter %08x type %d at 0x%x", p.Hash, p.Type, o)
			}
			for k := 0; k < sz; k += 4 {
				u := le.Uint32(b[o+8+k:])
				if p.Type == 2 || p.Type == 3 {
					p.Values = append(p.Values, float32(u))
				} else {
					p.Values = append(p.Values, math.Float32frombits(u))
				}
			}
			m.Params = append(m.Params, p)
			o += 8 + sz
		}
	}
	if nb > 0 {
		o := m.Offsets[5]
		if o+uint64(nb)*16 > uint64(len(b)) {
			return nil, fmt.Errorf("material: %d buffers at 0x%x past the end", nb, o)
		}
		for i := range nb {
			ro := o + uint64(16*i)
			p := b[ro:]
			bf := Buffer{Offset: ro + le.Uint64(p), A: p[8], B: p[9], Size: le.Uint16(p[10:]), Hash: le.Uint32(p[12:])}
			if bf.Offset+uint64(bf.Size) > uint64(len(b)) {
				return nil, fmt.Errorf("material: buffer %d past the end", i)
			}
			for k := uint64(0); k+4 <= uint64(bf.Size); k += 4 {
				bf.Floats = append(bf.Floats, math.Float32frombits(le.Uint32(b[bf.Offset+k:])))
			}
			m.Buffers = append(m.Buffers, bf)
		}
	}
	return m, nil
}

// Helper: +0x68 bit 1, set on exactly 9 of 11,150 materials: the "Player Only / No Walk / No Grapple" helper
// materials and the orange / blue checker placeholder (0x0E…0005, 0x0E…1CF0). Treated as not drawn in game [D, ?].
func (m *Material) Helper() bool { return m.Flags68&2 != 0 }

// SlotOpacity is CRC32("opacity"): a BC4 alpha mask (695 bindings; decals of shader 0x…22 among them).
var SlotOpacity = crc32.ChecksumIEEE([]byte("opacity"))

// ColorTexture picks the texture the preview shows as colour: the basecolor / color slot, else the first texture in an
// sRGB format (srgb reports that from the texture's header), e.g. the decal shader's colour slot 0xEDCD5483
// (AssetFormats.md §3.4). Returns 0 when the material has no colour texture.
func (m *Material) ColorTexture(srgb func(tex uint64) bool) uint64 {
	if t := m.Texture(SlotBaseColor, SlotColor); t != 0 {
		return t
	}
	for _, t := range m.Textures {
		if t.Slot != SlotOpacity && srgb != nil && srgb(t.GUID) {
			return t.GUID
		}
	}
	return 0
}

// Texture picks the texture to show for a slot preference list (0 if none).
func (m *Material) Texture(slots ...uint32) uint64 {
	for _, s := range slots {
		for _, t := range m.Textures {
			if t.Slot == s {
				return t.GUID
			}
		}
	}
	return 0
}

// LookMaterial is one entry of STUModelLook.m_materials.
type LookMaterial struct {
	Key      uint64 `json:"key"`
	Material uint64 `json:"material"`
}

var (
	hMaterials = crc32.ChecksumIEEE([]byte("m_materials"))
	hMaterial  = crc32.ChecksumIEEE([]byte("m_material"))
	hKey       = uint32(0x07e44baa) // STUModelMaterial: the model's material key (name unknown)
)

// ParseLook reads a model look's material table (STU, AssetFormats.md §4).
func ParseLook(b []byte) ([]LookMaterial, error) {
	d, err := stu.Open(b, stu.File, 0)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	root := d.Decode(d.Root())
	if !root.Valid() {
		return nil, fmt.Errorf("look: no root instance")
	}
	var out []LookMaterial
	for _, it := range root.Field(hMaterials).Items() {
		if o := it.Deref(); o.Valid() {
			it = o
		}
		g, _, _ := it.Field(hMaterial).Ref()
		k, _ := it.Field(hKey).Int()
		out = append(out, LookMaterial{Key: uint64(k), Material: g})
	}
	return out, nil
}

// Resolve maps a model's material keys to materials through a look (0 where the look has no entry).
func Resolve(keys []uint64, look []LookMaterial) []uint64 {
	out := make([]uint64, len(keys))
	for i, k := range keys {
		for _, l := range look {
			if l.Key == k {
				out[i] = l.Material
				break
			}
		}
	}
	return out
}
