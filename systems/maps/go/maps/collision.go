package maps

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Collision geometry of a map data blob (docs/Maps.md §2.6), decoded in Go from the bytes the C library parses:
// header +0x1FC four counts, +0x2D8 four table pointers (spheres 0x60, capsules 0x68, hulls 0x90, meshes 0x58).
// Layouts: systems/maps/include/maps/mapdata.h (CollisionMesh, CollisionHull), checked on every map by the install
// tests (tests/test_install.cpp, TestGeometry).

// Filter: every item becomes a fixture of the map's static body (FUN_7ff650a239a0): category 0x0001, or 0x0800 when
// its pass-through byte is set (in no mover's mask: characters walk through it), and the item's mask (the categories
// it collides with; heroes are 0x08 / 0x10 by team, docs/Movement.md §4.3, §4.6).
type Filter struct {
	Mask        uint16 `json:"mask"`
	PassThrough bool   `json:"passThrough"`
}

// BlocksHeroes: a hero's capsule (category 0x08 or 0x10) collides with it.
func (f Filter) BlocksHeroes() bool { return !f.PassThrough && f.Mask&0x18 != 0 }

// Sphere is a collision sphere (item +0x08 centre, +0x14 radius, +0x50 mask, +0x5D pass-through).
type Sphere struct {
	Center [3]float32 `json:"center"`
	Radius float32    `json:"radius"`
	Filter
}

// Capsule is a collision capsule: the segment P0–P1 swept by Radius (item +0x08, +0x14, +0x20; +0x50 mask,
// +0x5C pass-through).
type Capsule struct {
	P0     [3]float32 `json:"p0"`
	P1     [3]float32 `json:"p1"`
	Radius float32    `json:"radius"`
	Filter
}

// Hull is a convex hull: its vertices and its faces as vertex loops (counter-clockwise seen from outside, the order
// of the half-edge `next` links), with their planes (normal xyz, d: n·x = d). Item +0x78 mask, +0x84 pass-through.
type Hull struct {
	Filter
	Center [3]float32   `json:"center"`
	Verts  [][3]float32 `json:"verts"`
	Faces  [][]int      `json:"faces"`
	Planes [][4]float32 `json:"planes"`
}

// BVHNode is one node of the mesh's bounding volume hierarchy (32 bytes, pre-order, root = 0). Internal nodes
// (Count 0): left child = this + 1, right child = this + Index. Leaves: triangles Index … Index+Count-1.
type BVHNode struct {
	Min, Max [3]float32
	Index    int32
	Mask     uint16 // OR of the subtree's triangle masks (a query enters a node only if mask & its category)
	Kind     uint8  // +0x1E: 0 / 1 / 2 on internal nodes [? split axis], 3 on leaves
	Count    uint8
}

// Mesh is the map's static triangle mesh (the level geometry's collision).
type Mesh struct {
	Verts       []float32 // x, y, z per vertex
	Tris        []uint32  // 3 vertex indices per triangle
	Wings       []int32   // 3 per triangle: the opposite vertex across edge v0v1 / v1v2 / v2v0, -1 = open edge
	TriMaterial []uint16  // per triangle (+0x18)
	TriFlags    []uint16  // per triangle surface flags (+0x1A; bit 0 needed by the ground probe [? walkable])
	TriMask     []uint16  // per triangle collision mask (+0x1C)
	Nodes       []BVHNode // BVH
	Depth       int
	Filter      // item +0x40 mask, +0x52 pass-through (the triangles' own masks filter further)
}

// Geometry is every static collision shape of a map.
type Geometry struct {
	Spheres  []Sphere
	Capsules []Capsule
	Hulls    []Hull
	Meshes   []Mesh // one per map in 0.8
}

type rd []byte

func (b rd) u8(o uint64) uint8   { return b[o] }
func (b rd) u16(o uint64) uint16 { return binary.LittleEndian.Uint16(b[o:]) }
func (b rd) u32(o uint64) uint32 { return binary.LittleEndian.Uint32(b[o:]) }
func (b rd) u64(o uint64) uint64 { return binary.LittleEndian.Uint64(b[o:]) }
func (b rd) f32(o uint64) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(b[o:]))
}
func (b rd) v3(o uint64) [3]float32 { return [3]float32{b.f32(o), b.f32(o + 4), b.f32(o + 8)} }

// DecodeGeometry reads the collision shapes of a map data blob (the bytes given to Open, or Doc.Bytes()).
func DecodeGeometry(data []byte) (*Geometry, error) {
	b := rd(data)
	n := uint64(len(b))
	if n < 0x320 {
		return nil, errors.New("map data too short")
	}
	var cnt [4]uint64
	var tab [4]uint64
	strides := [4]uint64{0x60, 0x68, 0x90, 0x58}
	for k := 0; k < 4; k++ {
		cnt[k] = uint64(b.u32(0x1fc + 4*uint64(k)))
		tab[k] = b.u64(0x2d8 + 8*uint64(k))
		if cnt[k] > 0 && (tab[k] < 0x320 || tab[k]+cnt[k]*strides[k] > n) {
			return nil, fmt.Errorf("collision table %d outside the file", k)
		}
	}
	g := &Geometry{}
	for i := uint64(0); i < cnt[0]; i++ {
		o := tab[0] + i*0x60
		g.Spheres = append(g.Spheres, Sphere{b.v3(o + 8), b.f32(o + 0x14), Filter{b.u16(o + 0x50), b.u8(o+0x5d) != 0}})
	}
	for i := uint64(0); i < cnt[1]; i++ {
		o := tab[1] + i*0x68
		g.Capsules = append(g.Capsules, Capsule{b.v3(o + 8), b.v3(o + 0x14), b.f32(o + 0x20),
			Filter{b.u16(o + 0x50), b.u8(o+0x5c) != 0}})
	}
	for i := uint64(0); i < cnt[2]; i++ {
		h, err := decodeHull(b, tab[2]+i*0x90)
		if err != nil {
			return nil, fmt.Errorf("hull %d: %w", i, err)
		}
		g.Hulls = append(g.Hulls, *h)
	}
	for i := uint64(0); i < cnt[3]; i++ {
		m, err := decodeMesh(b, tab[3]+i*0x58)
		if err != nil {
			return nil, fmt.Errorf("mesh %d: %w", i, err)
		}
		g.Meshes = append(g.Meshes, *m)
	}
	return g, nil
}

func decodeHull(b rd, o uint64) (*Hull, error) {
	n := uint64(len(b))
	pv, pp, pe, pf := b.u64(o), b.u64(o+8), b.u64(o+16), b.u64(o+24)
	nv, nf, ne := uint64(b.u32(o+0x2c)), uint64(b.u32(o+0x30)), uint64(b.u32(o+0x34))
	if nv > 255 || nf > 255 || ne > 255 || pv+12*nv > n || pp+16*nf > n || pe+4*ne > n || pf+nf > n {
		return nil, errors.New("arrays outside the file")
	}
	h := &Hull{Center: b.v3(o + 0x20), Filter: Filter{b.u16(o + 0x78), b.u8(o+0x84) != 0}}
	for i := uint64(0); i < nv; i++ {
		h.Verts = append(h.Verts, b.v3(pv+12*i))
	}
	for f := uint64(0); f < nf; f++ {
		q := pp + 16*f
		h.Planes = append(h.Planes, [4]float32{b.f32(q), b.f32(q + 4), b.f32(q + 8), b.f32(q + 12)})
		first := uint64(b.u8(pf + f))
		var loop []int
		for e := first; ; {
			if e >= ne || uint64(b.u8(pe+4*e+2)) != f || uint64(len(loop)) > ne {
				return nil, fmt.Errorf("face %d: broken half-edge loop", f)
			}
			v := int(b.u8(pe + 4*e + 1))
			if uint64(v) >= nv {
				return nil, fmt.Errorf("face %d: vertex %d out of range", f, v)
			}
			loop = append(loop, v)
			e = uint64(b.u8(pe + 4*e + 3))
			if e == first {
				break
			}
		}
		h.Faces = append(h.Faces, loop)
	}
	return h, nil
}

func decodeMesh(b rd, o uint64) (*Mesh, error) {
	n := uint64(len(b))
	pn, pv, pt := b.u64(o), b.u64(o+8), b.u64(o+16)
	nn, nv, nt := uint64(b.u32(o+0x18)), uint64(b.u32(o+0x1c)), uint64(b.u32(o+0x20))
	if pn+32*nn > n || pv+16*nv > n || pt+32*nt > n {
		return nil, errors.New("arrays outside the file")
	}
	m := &Mesh{
		Verts: make([]float32, 3*nv), Tris: make([]uint32, 3*nt), Wings: make([]int32, 3*nt),
		TriMaterial: make([]uint16, nt), TriFlags: make([]uint16, nt), TriMask: make([]uint16, nt),
		Nodes: make([]BVHNode, nn), Depth: int(b.u32(o + 0x24)), Filter: Filter{b.u16(o + 0x40), b.u8(o+0x52) != 0},
	}
	for i := uint64(0); i < nv; i++ {
		v := b.v3(pv + 16*i)
		copy(m.Verts[3*i:], v[:])
	}
	for i := uint64(0); i < nt; i++ {
		q := pt + 32*i
		for k := uint64(0); k < 3; k++ {
			v := b.u32(q + 4*k)
			if uint64(v) >= nv {
				return nil, fmt.Errorf("triangle %d: vertex %d out of range", i, v)
			}
			m.Tris[3*i+k] = v
			m.Wings[3*i+k] = int32(b.u32(q + 12 + 4*k))
		}
		m.TriMaterial[i], m.TriFlags[i], m.TriMask[i] = b.u16(q+0x18), b.u16(q+0x1a), b.u16(q+0x1c)
	}
	for i := uint64(0); i < nn; i++ {
		q := pn + 32*i
		m.Nodes[i] = BVHNode{b.v3(q), b.v3(q + 12), int32(b.u32(q + 0x18)), b.u16(q + 0x1c), b.u8(q + 0x1e), b.u8(q + 0x1f)}
	}
	return m, nil
}

// Hit is a ray hit on the mesh.
type Hit struct {
	T   float32    // distance along the (normalised) ray
	Pos [3]float32 // hit point
	Tri int        // triangle index
}

// Raycast returns the closest triangle hit by the ray (dir need not be normalised; T is in units of |dir|) within
// maxT, walking the BVH like the client does (only into nodes whose mask has a bit of `mask`; 0xffff = everything).
// Triangles are hit from both sides.
func (m *Mesh) Raycast(origin, dir [3]float32, maxT float32, mask uint16) (Hit, bool) {
	return m.RaycastFunc(origin, dir, maxT, mask, nil)
}

// RaycastFunc is Raycast with a triangle filter (nil = every triangle whose mask passes).
func (m *Mesh) RaycastFunc(origin, dir [3]float32, maxT float32, mask uint16, accept func(tri int) bool) (Hit, bool) {
	best := Hit{T: maxT, Tri: -1}
	if len(m.Nodes) == 0 {
		return best, false
	}
	var inv [3]float32
	for k := 0; k < 3; k++ {
		inv[k] = 1 / dir[k] // ±Inf for 0 is what the slab test wants
	}
	stack := make([]int32, 0, 64)
	stack = append(stack, 0)
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		nd := &m.Nodes[i]
		if nd.Mask&mask == 0 || !slab(origin, inv, nd.Min, nd.Max, best.T) {
			continue
		}
		if nd.Count == 0 {
			stack = append(stack, i+nd.Index, i+1)
			continue
		}
		for t := int(nd.Index); t < int(nd.Index)+int(nd.Count); t++ {
			if m.TriMask[t]&mask == 0 || (accept != nil && !accept(t)) {
				continue
			}
			if d, ok := m.rayTri(origin, dir, t); ok && d < best.T {
				best.T, best.Tri = d, t
			}
		}
	}
	if best.Tri < 0 {
		return best, false
	}
	for k := 0; k < 3; k++ {
		best.Pos[k] = origin[k] + dir[k]*best.T
	}
	return best, true
}

func slab(o, inv, mn, mx [3]float32, maxT float32) bool {
	t0, t1 := float32(0), maxT
	for k := 0; k < 3; k++ {
		a, b := (mn[k]-o[k])*inv[k], (mx[k]-o[k])*inv[k]
		if a > b {
			a, b = b, a
		}
		if a != a || b != b { // NaN: origin on the slab plane with a zero direction component
			continue
		}
		if a > t0 {
			t0 = a
		}
		if b < t1 {
			t1 = b
		}
		if t0 > t1 {
			return false
		}
	}
	return true
}

// rayTri is Möller–Trumbore, two-sided.
func (m *Mesh) rayTri(o, d [3]float32, t int) (float32, bool) {
	v := func(i uint32) [3]float32 { return [3]float32{m.Verts[3*i], m.Verts[3*i+1], m.Verts[3*i+2]} }
	a, b, c := v(m.Tris[3*t]), v(m.Tris[3*t+1]), v(m.Tris[3*t+2])
	e1 := sub3(b, a)
	e2 := sub3(c, a)
	p := cross3(d, e2)
	det := dot3(e1, p)
	if det > -1e-12 && det < 1e-12 {
		return 0, false
	}
	inv := 1 / det
	s := sub3(o, a)
	u := dot3(s, p) * inv
	if u < 0 || u > 1 {
		return 0, false
	}
	q := cross3(s, e1)
	w := dot3(d, q) * inv
	if w < 0 || u+w > 1 {
		return 0, false
	}
	tt := dot3(e2, q) * inv
	return tt, tt >= 0
}

func sub3(a, b [3]float32) [3]float32 { return [3]float32{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func dot3(a, b [3]float32) float32    { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func cross3(a, b [3]float32) [3]float32 {
	return [3]float32{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}
