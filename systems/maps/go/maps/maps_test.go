package maps

import (
	"os"
	"path/filepath"
	"testing"

	"owcontent/content"
)

func install(t *testing.T) *content.Store {
	dir, _ := filepath.Abs("../../../../game/owbetaclient")
	if _, err := os.Stat(filepath.Join(dir, ".build.info")); err != nil {
		t.Skip("no install")
	}
	s, err := content.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAllMaps(t *testing.T) {
	s := install(t)
	defer s.Close()
	n := 0
	for _, r := range s.ResourcesOfType(0x002) {
		b, err := s.Read(r.GUID)
		if err != nil {
			t.Fatal(err)
		}
		d, err := Open(b)
		if err != nil {
			t.Fatalf("%#016x: %v", r.GUID, err)
		}
		m, err := d.Map(true, true)
		d.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Records) != m.Header.RecordCount {
			t.Errorf("%#016x: %d records, header %d", r.GUID, len(m.Records), m.Header.RecordCount)
		}
		n += len(m.Records)
	}
	if n != 31970 {
		t.Errorf("%d records in the install, want 31970", n)
	}
}

func TestAnubisEdit(t *testing.T) {
	s := install(t)
	defer s.Close()
	b, err := s.Read(0x080000000000005B)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	m, _ := d.Map(true, false)
	door := m.ByPlaced(0x1d7)
	if door == nil || door.Primary != 0x040000000000005B || door.Xf == nil {
		t.Fatalf("ship door: %+v", door)
	}
	pos := door.Xf.Pos
	pos[1] += 10
	if err := d.SetTransform(uint32(door.Offset), pos, nil, door.Xf.Rot); err != nil {
		t.Fatal(err)
	}
	if err := d.AddEntity(uint32(door.Offset), "42424242424242424242424242424242", pos, nil, door.Xf.Rot); err != nil {
		t.Fatal(err)
	}
	m2, _ := d.Map(true, false)
	if got := m2.ByPlaced(0x1d7).Xf.Pos[1]; got != pos[1] {
		t.Errorf("moved door y = %v, want %v", got, pos[1])
	}
	if len(m2.Records) != len(m.Records)+1 || m2.Placed != m.Placed+1 {
		t.Errorf("records %d -> %d, placed %d -> %d", len(m.Records), len(m2.Records), m.Placed, m2.Placed)
	}
	if len(d.Bytes()) <= len(b) {
		t.Errorf("size did not grow")
	}
	if m.Header.Env.GamemodeDef.Type() != 0x003 {
		t.Errorf("gamemode def type %#x", m.Header.Env.GamemodeDef.Type())
	}
}

// Every map's collision geometry decodes and matches the library's summary; the BVH ray cast agrees with a brute
// force over every triangle.
func TestGeometry(t *testing.T) {
	s := install(t)
	defer s.Close()
	n := 0
	for _, r := range s.ResourcesOfType(0x002) {
		b, err := s.Read(r.GUID)
		if err != nil {
			t.Fatal(err)
		}
		d, err := Open(b)
		if err != nil {
			t.Fatal(err)
		}
		m, err := d.Map(false, false)
		d.Close()
		if err != nil {
			t.Fatal(err)
		}
		g, err := DecodeGeometry(b)
		if err != nil {
			t.Fatalf("%#016x: %v", r.GUID, err)
		}
		c := m.Collision
		if len(g.Spheres) != c.Spheres || len(g.Capsules) != c.Capsules || len(g.Hulls) != c.Hulls ||
			len(g.Meshes) != 1 || len(c.Meshes) != 1 {
			t.Fatalf("%#016x: shape counts differ from the summary", r.GUID)
		}
		me := g.Meshes[0]
		if len(me.Tris)/3 != c.Meshes[0].Tris || len(me.Verts)/3 != c.Meshes[0].Verts ||
			len(me.Nodes) != c.Meshes[0].BVHNodes || me.Depth != c.Meshes[0].BVHDepth {
			t.Errorf("%#016x: mesh counts differ from the summary", r.GUID)
		}
		if r.GUID == 0x080000000000005B && (len(me.Tris)/3 != 522648 || len(g.Hulls) != 59 || len(g.Capsules) != 118) {
			t.Errorf("Anubis: %d triangles, %d hulls, %d capsules", len(me.Tris)/3, len(g.Hulls), len(g.Capsules))
		}
		// rays from above the root box's centre area, downwards and slanted
		root := me.Nodes[0]
		for k := 0; k < 8; k++ {
			f := float32(k) / 8
			o := [3]float32{root.Min[0] + (root.Max[0]-root.Min[0])*(0.2+0.6*f), root.Max[1] + 1,
				root.Min[2] + (root.Max[2]-root.Min[2])*(0.7-0.5*f)}
			dir := [3]float32{0.3 - 0.1*float32(k), -1, 0.05 * float32(k)}
			h, ok := me.Raycast(o, dir, 1e6, 0xffff)
			bt, bi := float32(1e6), -1
			for i := 0; i < len(me.Tris)/3; i++ {
				if d, hit := me.rayTri(o, dir, i); hit && d < bt {
					bt, bi = d, i
				}
			}
			if ok != (bi >= 0) || (ok && (h.T-bt > 1e-4 || bt-h.T > 1e-4)) {
				t.Errorf("%#016x ray %d: bvh %v %v %d, brute %v %d", r.GUID, k, ok, h.T, h.Tri, bt, bi)
			}
		}
		n++
	}
	if n != 19 {
		t.Errorf("%d maps", n)
	}
}
