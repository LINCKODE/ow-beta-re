package mapview

import (
	"encoding/binary"
	"math"

	"owmaps/maps"
)

// The 3D map mode (T066): the map's static collision (docs/Maps.md §2.6) as a binary mesh for the webview plus the
// shapes as JSON, and ray picks on the mesh through its own BVH.

// Shapes is the collision besides the triangle mesh, plus the mesh's summary (for the 3D view's layers and camera).
type Shapes struct {
	Spheres  []maps.Sphere  `json:"spheres"`
	Capsules []maps.Capsule `json:"capsules"`
	Hulls    []maps.Hull    `json:"hulls"`
	Tris     int            `json:"tris"`
	Verts    int            `json:"verts"`
	Nodes    int            `json:"nodes"`
	Min      [3]float32     `json:"min"` // mesh bounds (BVH root)
	Max      [3]float32     `json:"max"`
	Filter   maps.Filter    `json:"filter"` // the mesh item's
}

// Geometry decodes (once) the map's collision geometry.
func (m *Map) Geometry() (*maps.Geometry, error) {
	if m.geo == nil {
		g, err := maps.DecodeGeometry(m.Bytes)
		if err != nil {
			return nil, err
		}
		m.geo = g
	}
	return m.geo, nil
}

func (m *Map) mesh() (*maps.Mesh, error) {
	g, err := m.Geometry()
	if err != nil || len(g.Meshes) == 0 {
		return nil, err
	}
	return &g.Meshes[0], nil
}

// Shapes is the collision shapes and the mesh summary.
func (m *Map) Shapes() (*Shapes, error) {
	g, err := m.Geometry()
	if err != nil {
		return nil, err
	}
	s := &Shapes{Spheres: g.Spheres, Capsules: g.Capsules, Hulls: g.Hulls}
	if s.Spheres == nil {
		s.Spheres = []maps.Sphere{}
	}
	if s.Capsules == nil {
		s.Capsules = []maps.Capsule{}
	}
	if s.Hulls == nil {
		s.Hulls = []maps.Hull{}
	}
	if me, _ := m.mesh(); me != nil {
		s.Tris, s.Verts, s.Nodes, s.Filter = len(me.Tris)/3, len(me.Verts)/3, len(me.Nodes), me.Filter
		if len(me.Nodes) > 0 {
			s.Min, s.Max = me.Nodes[0].Min, me.Nodes[0].Max
		}
	}
	return s, nil
}

// MeshMagic starts the binary mesh ("OWCM").
const MeshMagic = 0x4d43574f

// MeshBinary is the triangle mesh for the webview, little-endian:
//
//	u32 magic "OWCM", u32 version 1, u32 vertex count V, u32 triangle count T
//	f32[3V] vertices, u32[3T] triangle vertex indices, u16[T] masks, u16[T] surface flags, u16[T] materials
//	(u16 padding to 4 bytes)
func (m *Map) MeshBinary() ([]byte, error) {
	me, err := m.mesh()
	if err != nil {
		return nil, err
	}
	if me == nil {
		me = &maps.Mesh{}
	}
	nv, nt := len(me.Verts)/3, len(me.Tris)/3
	size := 16 + 12*nv + 12*nt + 6*nt
	size = (size + 3) &^ 3
	b := make([]byte, size)
	le := binary.LittleEndian
	le.PutUint32(b[0:], MeshMagic)
	le.PutUint32(b[4:], 1)
	le.PutUint32(b[8:], uint32(nv))
	le.PutUint32(b[12:], uint32(nt))
	o := 16
	for _, f := range me.Verts {
		le.PutUint32(b[o:], math.Float32bits(f))
		o += 4
	}
	for _, i := range me.Tris {
		le.PutUint32(b[o:], i)
		o += 4
	}
	for _, a := range [][]uint16{me.TriMask, me.TriFlags, me.TriMaterial} {
		for _, x := range a {
			le.PutUint16(b[o:], x)
			o += 2
		}
	}
	return b, nil
}

// RayHit is the closest mesh triangle under a ray (the 3D view's cursor).
type RayHit struct {
	Hit      bool       `json:"hit"`
	T        float32    `json:"t"`
	Pos      [3]float32 `json:"pos"`
	Normal   [3]float32 `json:"normal"` // unit, facing the ray origin
	Tri      int        `json:"tri"`
	Mask     uint16     `json:"mask"`
	Flags    uint16     `json:"flags"`
	Material uint16     `json:"material"`
}

// Triangle parts of the 3D view, by mask (heroes are categories 0x08 / 0x10, docs/Movement.md §4.3):
const (
	PartSolid = 1 << iota // blocks heroes and other categories
	PartClip              // blocks heroes only: mask ⊆ 0x001A (Anubis: 300 invisible boxes up to y 40)
	PartOther             // no hero category (e.g. masks 0x0440, 0x0040)
)

// TriPart is a triangle mask's part (the frontend's Map3D.vue partOf is the same rule).
func TriPart(mask uint16) int {
	switch {
	case mask&0x18 == 0:
		return PartOther
	case mask&^0x1a == 0:
		return PartClip
	}
	return PartSolid
}

// Ray casts on the mesh, on the triangles of the given parts (bits Part*; 0 = all).
func (m *Map) Ray(origin, dir [3]float32, parts int) (*RayHit, error) {
	me, err := m.mesh()
	if err != nil || me == nil {
		return &RayHit{Tri: -1}, err
	}
	var accept func(int) bool
	if parts != 0 && parts != PartSolid|PartClip|PartOther {
		accept = func(t int) bool { return TriPart(me.TriMask[t])&parts != 0 }
	}
	h, ok := me.RaycastFunc(origin, dir, 1e5, 0xffff, accept)
	if !ok {
		return &RayHit{Tri: -1}, nil
	}
	r := &RayHit{Hit: true, T: h.T, Pos: h.Pos, Tri: h.Tri, Mask: me.TriMask[h.Tri], Flags: me.TriFlags[h.Tri],
		Material: me.TriMaterial[h.Tri]}
	v := func(k int) [3]float64 {
		i := me.Tris[3*h.Tri+k]
		return [3]float64{float64(me.Verts[3*i]), float64(me.Verts[3*i+1]), float64(me.Verts[3*i+2])}
	}
	a, b, c := v(0), v(1), v(2)
	e1 := [3]float64{b[0] - a[0], b[1] - a[1], b[2] - a[2]}
	e2 := [3]float64{c[0] - a[0], c[1] - a[1], c[2] - a[2]}
	n := [3]float64{e1[1]*e2[2] - e1[2]*e2[1], e1[2]*e2[0] - e1[0]*e2[2], e1[0]*e2[1] - e1[1]*e2[0]}
	l := math.Sqrt(n[0]*n[0] + n[1]*n[1] + n[2]*n[2])
	if l > 0 {
		if n[0]*float64(dir[0])+n[1]*float64(dir[1])+n[2]*float64(dir[2]) > 0 {
			l = -l
		}
		r.Normal = [3]float32{float32(n[0] / l), float32(n[1] / l), float32(n[2] / l)}
	}
	return r, nil
}
