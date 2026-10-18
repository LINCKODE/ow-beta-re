// Package model reads the 0.8 client's models (0x00C), materials (0x008) and model looks (0x01A, STU):
// docs/AssetFormats.md §2–§4. Pure Go, no third-party code.
package model

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Resource types.
const (
	TypeModel    = 0x00C
	TypeMaterial = 0x008
	TypeLook     = 0x01A
	TypeSkeleton = 0x007 // SKELETON handler: the same construct as MODEL (AssetFormats.md §2.1)
)

var ErrMissing = errors.New("resource missing")

// Header offsets (AssetFormats.md §2.2). All offsets inside a model are file offsets.
const (
	offSplit        = 0x40  // u32 × 3: CPU part size (quality 1), (quality 2), file size
	offSkeleton     = 0x50  // skeleton block (0x60 bytes)
	offCloth        = 0xCC  // i32 count of 0xD8-byte records at +0x1B8
	offMaterialKeys = 0x144 // u16 count of u64 material keys at +0x160
	offCounts       = 0x150 // u8 vertex buffers, u8 index buffers, u8 LOD mask union, u8 submeshes
	offKeysPtr      = 0x160
	offSubmeshPtr   = 0x168
	offVBPtr        = 0x170
	offIBPtr        = 0x178
	offRefsA        = 0x1A8
	offRefsB        = 0x1B0
	headerSize      = 0x1D0
)

// Vertex element semantics and formats (AssetFormats.md §2.4).
const (
	SemPosition   = 0
	SemNormal     = 1
	SemTangent    = 3
	SemBoneIndex  = 4
	SemBoneWeight = 5
	SemColor      = 8
	SemUV         = 9
	SemID         = 16

	FmtFloat3     = 2  // R32G32B32_FLOAT
	FmtHalf2      = 4  // R16G16_FLOAT
	FmtUByte4     = 6  // R8G8B8A8_UINT
	FmtUByte4Norm = 8  // R8G8B8A8_UNORM
	FmtByte4Norm  = 9  // R8G8B8A8_SNORM
	FmtUInt       = 12 // R32_UINT
)

var semNames = map[uint8]string{SemPosition: "position", SemNormal: "normal", SemTangent: "tangent",
	SemBoneIndex: "bone index", SemBoneWeight: "bone weight", SemColor: "color", SemUV: "uv", SemID: "id"}
var fmtNames = map[uint8]string{FmtFloat3: "float3", FmtHalf2: "half2", FmtUByte4: "ubyte4", FmtUByte4Norm: "unorm8x4",
	FmtByte4Norm: "snorm8x4", FmtUInt: "u32"}
var fmtSize = map[uint8]int{FmtFloat3: 12, FmtHalf2: 4, FmtUByte4: 4, FmtUByte4Norm: 4, FmtByte4Norm: 4, FmtUInt: 4}

func SemanticName(s uint8) string {
	if n, ok := semNames[s]; ok {
		return n
	}
	return fmt.Sprintf("semantic %d", s)
}
func FormatName(f uint8) string {
	if n, ok := fmtNames[f]; ok {
		return n
	}
	return fmt.Sprintf("format %d", f)
}

// Element is one vertex attribute: 8 bytes.
type Element struct {
	Semantic uint8  `json:"semantic"`
	Index    uint8  `json:"index"`
	Format   uint8  `json:"format"`
	Stream   uint8  `json:"stream"`
	Unk      uint16 `json:"unk"`
	Offset   uint16 `json:"offset"`
}

// VertexBuffer is a 0x38-byte descriptor: two streams of interleaved vertices.
type VertexBuffer struct {
	Count    uint32    `json:"count"`
	Hash     uint32    `json:"hash"` // +0x04, a layout id? [?]
	Strides  [2]uint8  `json:"strides"`
	Flags    uint8     `json:"flags"` // +0x0B [?]
	Elements []Element `json:"elements"`
	Data     [2]uint64 `json:"data"` // file offsets of stream 0 and 1
}

// IndexBuffer is a 0x18-byte descriptor of u16 indices.
type IndexBuffer struct {
	Count uint32 `json:"count"`
	Data  uint64 `json:"data"`
}

// Submesh is a 0x48-byte record: one draw call.
type Submesh struct {
	Bounds      [11]float32 `json:"bounds"` // +0x08..+0x33 [?: bounding data]
	VertexStart uint32      `json:"vertexStart"`
	IndexStart  uint16      `json:"indexStart"`
	IndexCount  uint16      `json:"indexCount"`
	VertexCount uint32      `json:"vertexCount"` // +0x3C: not the range the indices cover [?]
	Flags       uint8       `json:"flags"`
	VB          uint8       `json:"vb"`
	IB          uint8       `json:"ib"`
	Material    uint8       `json:"material"` // index into the model's material keys
	LODMask     uint8       `json:"lod"`      // 1 = LOD0 (finest) … 8 = LOD3, 0xFF = every LOD
}

// Proxy: submesh flag 0x10 marks a coarse proxy of the model, not drawn in the visual pass [D]: on all 1,400 models that
// have such submeshes they sit next to the real ones, at every LOD (mask 0xFF), with a median 6 % of their triangles;
// no model has only proxies. Drawn over the real mesh they made e.g. the Anubis statue 0x0D…07BD look patchy
// (AssetFormats.md §2.5). What the client uses them for (shadows, occlusion) was not traced [?].
func (s Submesh) Proxy() bool { return s.Flags&0x10 != 0 }

// Bone of the skeleton (AssetFormats.md §2.6).
type Bone struct {
	Parent int16  `json:"parent"`
	A, B   uint16 `json:"-"`
	ID     uint32 `json:"id"`
	World  TRS    `json:"world"` // bind pose, model space
	Local  TRS    `json:"local"` // relative to the parent
}

// TRS: rotation quaternion (x, y, z, w), scale, translation.
type TRS struct {
	Rot   [4]float32 `json:"rot"`
	Scale [3]float32 `json:"scale"`
	Pos   [3]float32 `json:"pos"`
}

type Skeleton struct {
	Hash   uint32   `json:"hash"`
	Counts [6]int16 `json:"counts"` // +0x5C..: bones, bones, ?, ?, ?, ?
	Bones  []Bone   `json:"bones"`
}

// Model is a parsed model file.
type Model struct {
	Size         int            `json:"size"`
	Split        [3]uint32      `json:"split"`
	LODUnion     uint8          `json:"lodUnion"`
	VBs          []VertexBuffer `json:"vbs"`
	IBs          []IndexBuffer  `json:"ibs"`
	Submeshes    []Submesh      `json:"submeshes"`
	MaterialKeys []uint64       `json:"materialKeys"`
	Skeleton     *Skeleton      `json:"skeleton,omitempty"`
	ClothCount   int            `json:"clothCount"`
	Refs         []uint64       `json:"refs"` // GUIDs the loader requests (all 0x08E in 0.8)

	b []byte
}

type reader struct {
	b   []byte
	err error
}

func (r *reader) ok(off uint64, n int) bool {
	if r.err != nil {
		return false
	}
	if off > uint64(len(r.b)) || uint64(len(r.b))-off < uint64(n) {
		r.err = fmt.Errorf("offset 0x%x+%d past the end (0x%x)", off, n, len(r.b))
		return false
	}
	return true
}
func (r *reader) u8(o uint64) uint8 {
	if !r.ok(o, 1) {
		return 0
	}
	return r.b[o]
}
func (r *reader) u16(o uint64) uint16 {
	if !r.ok(o, 2) {
		return 0
	}
	return binary.LittleEndian.Uint16(r.b[o:])
}
func (r *reader) u32(o uint64) uint32 {
	if !r.ok(o, 4) {
		return 0
	}
	return binary.LittleEndian.Uint32(r.b[o:])
}
func (r *reader) u64(o uint64) uint64 {
	if !r.ok(o, 8) {
		return 0
	}
	return binary.LittleEndian.Uint64(r.b[o:])
}
func (r *reader) f32(o uint64) float32 { return math.Float32frombits(r.u32(o)) }

// Parse reads a model's tables (not its vertices: see Mesh).
func Parse(b []byte) (*Model, error) {
	if len(b) < headerSize {
		return nil, fmt.Errorf("model: %d bytes, shorter than the header", len(b))
	}
	r := &reader{b: b}
	m := &Model{Size: len(b), b: b}
	for i := range 3 {
		m.Split[i] = r.u32(uint64(offSplit + 4*i))
	}
	if int(m.Split[2]) != len(b) {
		return nil, fmt.Errorf("model: size field 0x%x != file size 0x%x", m.Split[2], len(b))
	}
	nvb, nib, nsm := int(b[offCounts]), int(b[offCounts+1]), int(b[offCounts+3])
	m.LODUnion = b[offCounts+2]
	vbp, ibp, smp := r.u64(offVBPtr), r.u64(offIBPtr), r.u64(offSubmeshPtr)
	for i := range nvb {
		o := vbp + uint64(i)*0x38
		v := VertexBuffer{Count: r.u32(o), Hash: r.u32(o + 4), Strides: [2]uint8{r.u8(o + 8), r.u8(o + 9)}, Flags: r.u8(o + 11)}
		ne := int(r.u8(o + 10))
		ep := r.u64(o + 0x20)
		v.Data = [2]uint64{r.u64(o + 0x28), r.u64(o + 0x30)}
		for k := range ne {
			eo := ep + uint64(k)*8
			v.Elements = append(v.Elements, Element{r.u8(eo), r.u8(eo + 1), r.u8(eo + 2), r.u8(eo + 3), r.u16(eo + 4), r.u16(eo + 6)})
		}
		m.VBs = append(m.VBs, v)
	}
	for i := range nib {
		o := ibp + uint64(i)*0x18
		m.IBs = append(m.IBs, IndexBuffer{Count: r.u32(o), Data: r.u64(o + 0x10)})
	}
	for i := range nsm {
		o := smp + uint64(i)*0x48
		s := Submesh{VertexStart: r.u32(o + 0x34), IndexStart: r.u16(o + 0x38), IndexCount: r.u16(o + 0x3A),
			VertexCount: r.u32(o + 0x3C), Flags: r.u8(o + 0x40), VB: r.u8(o + 0x41), IB: r.u8(o + 0x42),
			Material: r.u8(o + 0x43), LODMask: r.u8(o + 0x44)}
		for k := range 11 {
			s.Bounds[k] = r.f32(o + 8 + uint64(4*k))
		}
		m.Submeshes = append(m.Submeshes, s)
	}
	nkeys := int(r.u16(offMaterialKeys))
	kp := r.u64(offKeysPtr)
	for i := range nkeys {
		m.MaterialKeys = append(m.MaterialKeys, r.u64(kp+uint64(8*i)))
	}
	m.ClothCount = int(int32(r.u32(offCloth)))
	m.parseSkeleton(r)
	m.parseRefs(r)
	if r.err != nil {
		return nil, fmt.Errorf("model: %w", r.err)
	}
	return m, m.validate()
}

func (m *Model) validate() error {
	for i, v := range m.VBs {
		for _, e := range v.Elements {
			if e.Stream > 1 {
				return fmt.Errorf("model: vb %d: element stream %d", i, e.Stream)
			}
			sz, ok := fmtSize[e.Format]
			if !ok {
				return fmt.Errorf("model: vb %d: unknown vertex format %d", i, e.Format)
			}
			if int(e.Offset)+sz > int(v.Strides[e.Stream]) {
				return fmt.Errorf("model: vb %d: element at %d past the stride %d", i, e.Offset, v.Strides[e.Stream])
			}
		}
		for s := range 2 {
			if v.Strides[s] > 0 && v.Data[s]+uint64(v.Count)*uint64(v.Strides[s]) > uint64(len(m.b)) {
				return fmt.Errorf("model: vb %d stream %d past the end", i, s)
			}
		}
	}
	for i, ib := range m.IBs {
		if ib.Data+uint64(ib.Count)*2 > uint64(len(m.b)) {
			return fmt.Errorf("model: ib %d past the end", i)
		}
	}
	for i, s := range m.Submeshes {
		if int(s.VB) >= len(m.VBs) || int(s.IB) >= len(m.IBs) {
			return fmt.Errorf("model: submesh %d: vb %d / ib %d out of range", i, s.VB, s.IB)
		}
		if int(s.IndexStart)+int(s.IndexCount) > int(m.IBs[s.IB].Count) {
			return fmt.Errorf("model: submesh %d: indices past the buffer", i)
		}
		if int(s.Material) >= len(m.MaterialKeys) {
			return fmt.Errorf("model: submesh %d: material %d of %d keys", i, s.Material, len(m.MaterialKeys))
		}
	}
	return nil
}

func (m *Model) parseSkeleton(r *reader) {
	n := int(int16(r.u16(offSkeleton + 0x5C)))
	if n <= 0 {
		return
	}
	var p [11]uint64
	for i := range p {
		p[i] = r.u64(offSkeleton + uint64(8*i))
	}
	s := &Skeleton{Hash: r.u32(offSkeleton + 0x58)}
	for i := range s.Counts {
		s.Counts[i] = int16(r.u16(offSkeleton + 0x5C + uint64(2*i)))
	}
	trs := func(o uint64) TRS {
		var t TRS
		for k := range 4 {
			t.Rot[k] = r.f32(o + uint64(4*k))
		}
		for k := range 3 {
			t.Scale[k] = r.f32(o + 16 + uint64(4*k))
			t.Pos[k] = r.f32(o + 32 + uint64(4*k))
		}
		return t
	}
	for i := range n {
		bo := Bone{A: r.u16(p[0] + uint64(6*i)), B: r.u16(p[0] + uint64(6*i) + 2), Parent: int16(r.u16(p[0] + uint64(6*i) + 4))}
		if p[6] != 0 {
			bo.ID = r.u32(p[6] + uint64(4*i))
		}
		if p[3] != 0 {
			bo.World = trs(p[3] + uint64(48*i))
		}
		if p[4] != 0 {
			bo.Local = trs(p[4] + uint64(48*i))
		}
		s.Bones = append(s.Bones, bo)
	}
	m.Skeleton = s
}

// parseRefs collects the GUIDs the loader requests (construct 0x84df00, RVA): the tables at +0x1A8 and +0x1B0 and the
// 0xD8-byte records at +0x1B8.
func (m *Model) parseRefs(r *reader) {
	seen := map[uint64]bool{}
	add := func(g uint64) {
		if g != 0 && g != math.MaxUint64 && !seen[g] {
			seen[g] = true
			m.Refs = append(m.Refs, g)
		}
	}
	if t := r.u64(offRefsA); t != 0 {
		strides := []uint64{0x50, 0x38, 0x50, 0x58, 0x60}
		for a, st := range strides {
			base := r.u64(t + uint64(8*a))
			n := int(int32(r.u32(t + 0x28 + uint64(4*a))))
			if base == 0 {
				continue
			}
			for k := range n {
				add(r.u64(t + base + uint64(k)*st))
			}
		}
	}
	if t := r.u64(offRefsB); t != 0 {
		b0, b2 := r.u64(t), r.u64(t+0x10)
		for k := range int(int32(r.u32(t + 0x18))) {
			add(r.u64(t + b0 + uint64(k)*0x38))
			add(r.u64(t + b0 + uint64(k)*0x38 + 0x10))
		}
		for k := range int(int32(r.u32(t + 0x20))) {
			add(r.u64(t + b2 + uint64(k)*0x20))
		}
	}
	if t := r.u64(0x1B8); t != 0 {
		for k := range m.ClothCount {
			o := t + uint64(k)*0xD8
			for _, f := range []uint64{0xA0, 0xB8} {
				if g := r.u64(o + f); GUIDType(g) == 0x08E {
					add(g)
				}
			}
		}
	}
}

// GUIDType is a GUID's resource type (ResourceLoading.md §1: bits 48..59 reversed, + 1). The 0xD8 records
// also hold non-GUID keys in the GUID slots (e.g. 0x0010000000000001), so only 0x08E is taken from them.
func GUIDType(g uint64) uint32 {
	t := uint32(g>>48) & 0xFFF
	var r uint32
	for i := range 12 {
		if t&(1<<i) != 0 {
			r |= 1 << (11 - i)
		}
	}
	return r + 1
}

// LODs lists the LOD bits used by the submeshes (1, 2, 4, 8), finest first; nil if every submesh is 0xFF.
func (m *Model) LODs() []uint8 {
	var have [8]bool
	for _, s := range m.Submeshes {
		if s.LODMask != 0xFF {
			for b := range 8 {
				if s.LODMask&(1<<b) != 0 {
					have[b] = true
				}
			}
		}
	}
	var out []uint8
	for b := range 8 {
		if have[b] {
			out = append(out, 1<<b)
		}
	}
	return out
}

// Triangles counts the triangles drawn at a LOD bit (0: the finest one present).
func (m *Model) Triangles(lod uint8) int {
	if lod == 0 {
		if l := m.LODs(); len(l) > 0 {
			lod = l[0]
		} else {
			lod = 0xFF
		}
	}
	n := 0
	for _, s := range m.Submeshes {
		if s.LODMask&lod != 0 && !s.Proxy() {
			n += int(s.IndexCount) / 3
		}
	}
	return n
}
