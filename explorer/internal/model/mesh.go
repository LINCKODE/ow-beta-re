package model

import (
	"encoding/binary"
	"fmt"
	"math"

	"owexplorer/internal/texture"
)

// Mesh is a model's geometry at one LOD, flattened for display and export: every submesh's vertex range
// copied out (indices stay relative to the group's first vertex + Group.Vertex).
type Mesh struct {
	Positions []float32 // xyz
	Normals   []float32 // xyz (zero if the buffer has none)
	UVs       []float32 // uv set 0 (zero if none)
	UV1s      []float32 // uv set 1 (zero if none; T071: layer / overlay / AO textures read on UV1)
	Colors    []uint8   // rgba of semantic 8 index 0 (nil if no submesh has one)
	Indices   []uint32  // absolute into the arrays above
	Groups    []Group
	Min, Max  [3]float32
}

// Group is one submesh in the Mesh.
type Group struct {
	Submesh  int    `json:"submesh"`
	Vertex   int    `json:"vertex"` // first vertex in the Mesh
	Vertices int    `json:"vertices"`
	Index    int    `json:"index"` // first index in the Mesh
	Count    int    `json:"count"`
	Material int    `json:"material"` // index into Model.MaterialKeys
	Key      uint64 `json:"-"`
	LODMask  uint8  `json:"lod"`
}

func snorm8(b byte) float32 { return max(float32(int8(b))/127, -1) }

// Mesh decodes the submeshes whose LOD mask has a bit of lod (0: the finest LOD; 0xFF: every submesh), without the
// proxy submeshes (flag 0x10, see Submesh.Proxy).
func (m *Model) Mesh(lod uint8) (*Mesh, error) { return m.MeshWith(lod, false) }

// MeshWith is Mesh, optionally with the proxy submeshes.
func (m *Model) MeshWith(lod uint8, proxies bool) (*Mesh, error) {
	if lod == 0 {
		if l := m.LODs(); len(l) > 0 {
			lod = l[0]
		} else {
			lod = 0xFF
		}
	}
	out := &Mesh{Min: [3]float32{math.MaxFloat32, math.MaxFloat32, math.MaxFloat32},
		Max: [3]float32{-math.MaxFloat32, -math.MaxFloat32, -math.MaxFloat32}}
	le := binary.LittleEndian
	for si, s := range m.Submeshes {
		if s.LODMask&lod == 0 || s.IndexCount == 0 || (s.Proxy() && !proxies) {
			continue
		}
		vb, ib := m.VBs[s.VB], m.IBs[s.IB]
		idx := m.b[ib.Data+uint64(s.IndexStart)*2:]
		maxI := 0
		for k := range int(s.IndexCount) {
			maxI = max(maxI, int(le.Uint16(idx[2*k:])))
		}
		nv := maxI + 1
		if int(s.VertexStart)+nv > int(vb.Count) {
			return nil, fmt.Errorf("submesh %d: vertices %d+%d past the buffer (%d)", si, s.VertexStart, nv, vb.Count)
		}
		g := Group{Submesh: si, Vertex: len(out.Positions) / 3, Vertices: nv, Index: len(out.Indices), Count: int(s.IndexCount),
			Material: int(s.Material), Key: m.MaterialKeys[s.Material], LODMask: s.LODMask}
		base := g.Vertex
		for k := range int(s.IndexCount) {
			out.Indices = append(out.Indices, uint32(base+int(le.Uint16(idx[2*k:]))))
		}
		var ePos, eNrm, eUV, eUV1, eCol *Element
		for i := range vb.Elements {
			e := &vb.Elements[i]
			switch {
			case e.Semantic == SemPosition && e.Format == FmtFloat3 && ePos == nil:
				ePos = e
			case e.Semantic == SemNormal && e.Format == FmtByte4Norm && eNrm == nil:
				eNrm = e
			case e.Semantic == SemUV && e.Index == 0 && e.Format == FmtHalf2 && eUV == nil:
				eUV = e
			case e.Semantic == SemUV && e.Index == 1 && e.Format == FmtHalf2 && eUV1 == nil:
				eUV1 = e
			case e.Semantic == SemColor && e.Index == 0 && e.Format == FmtUByte4Norm && eCol == nil:
				eCol = e
			}
		}
		if ePos == nil {
			return nil, fmt.Errorf("submesh %d: vertex buffer %d has no float3 position", si, s.VB)
		}
		if eCol != nil && out.Colors == nil {
			out.Colors = make([]uint8, len(out.Positions)/3*4)
			for i := range out.Colors {
				out.Colors[i] = 255
			}
		}
		at := func(e *Element, v int) []byte {
			st := uint64(vb.Strides[e.Stream])
			o := vb.Data[e.Stream] + (uint64(s.VertexStart)+uint64(v))*st + uint64(e.Offset)
			return m.b[o : o+uint64(fmtSize[e.Format])]
		}
		for v := range nv {
			p := at(ePos, v)
			for k := range 3 {
				f := math.Float32frombits(le.Uint32(p[4*k:]))
				out.Positions = append(out.Positions, f)
				out.Min[k] = min(out.Min[k], f)
				out.Max[k] = max(out.Max[k], f)
			}
			if eNrm != nil {
				n := at(eNrm, v)
				out.Normals = append(out.Normals, snorm8(n[0]), snorm8(n[1]), snorm8(n[2]))
			} else {
				out.Normals = append(out.Normals, 0, 0, 0)
			}
			if eUV != nil {
				u := at(eUV, v)
				out.UVs = append(out.UVs, texture.Half(le.Uint16(u)), texture.Half(le.Uint16(u[2:])))
			} else {
				out.UVs = append(out.UVs, 0, 0)
			}
			if eUV1 != nil {
				u := at(eUV1, v)
				out.UV1s = append(out.UV1s, texture.Half(le.Uint16(u)), texture.Half(le.Uint16(u[2:])))
			} else {
				out.UV1s = append(out.UV1s, 0, 0)
			}
			if out.Colors != nil {
				if eCol != nil {
					out.Colors = append(out.Colors, at(eCol, v)...)
				} else {
					out.Colors = append(out.Colors, 255, 255, 255, 255)
				}
			}
		}
		out.Groups = append(out.Groups, g)
	}
	if len(out.Groups) == 0 {
		out.Min, out.Max = [3]float32{}, [3]float32{}
	}
	return out, nil
}
