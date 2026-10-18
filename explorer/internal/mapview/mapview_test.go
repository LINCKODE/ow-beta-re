package mapview

import (
	"os"
	"path/filepath"
	"testing"

	"owcontent/content"
	"owstu/stu"
)

type res struct{ s *content.Store }

func (r res) GUIDName(uint64) string { return "" }
func (r res) Exists(g uint64) bool   { _, ok := r.s.Find(g); return ok }
func (r res) WithDoc(g uint64, f func(d *stu.Document) error) error {
	b, err := r.s.Read(g)
	if err != nil {
		return err
	}
	d, err := stu.Open(b, stu.File, 0)
	if err != nil {
		return err
	}
	defer d.Close()
	return f(d)
}

func store(t *testing.T) *content.Store {
	dir, _ := filepath.Abs("../../../game/owbetaclient")
	if _, err := os.Stat(filepath.Join(dir, ".build.info")); err != nil {
		t.Skip("no install")
	}
	s, err := content.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Every map builds a view; Anubis' classes match the catalogue (docs/Maps.md §1: 22 networked, 7 movers).
func TestAllMaps(t *testing.T) {
	s := store(t)
	defer s.Close()
	r := res{s}
	for _, rs := range s.ResourcesOfType(0x002) {
		b, err := s.Read(rs.GUID)
		if err != nil {
			t.Fatal(err)
		}
		m, err := Open(rs.GUID, b)
		if err != nil {
			t.Fatalf("%016x: %v", rs.GUID, err)
		}
		v := m.Build(r)
		if len(v.Placements) != len(m.M.Records) || len(v.Assets) == 0 {
			t.Errorf("%016x: %d placements, %d assets", rs.GUID, len(v.Placements), len(v.Assets))
		}
		if rs.GUID == 0x080000000000005B {
			if v.Classes["networked"] != 22 || v.Classes["mover"] != 7 {
				t.Errorf("Anubis classes %v", v.Classes)
			}
			d, err := m.Record(r, m.M.ByPlaced(0x1dd).Index+sectionStart(m, 11))
			if err != nil || len(d.InstanceData) == 0 || d.InstanceData[0].JSON == "" {
				t.Errorf("spawn point detail: %v %+v", err, d)
			}
		}
	}
}

func sectionStart(m *Map, typ int) int {
	for i, r := range m.M.Records {
		if r.Type == typ {
			return i
		}
	}
	return 0
}

// The 3D mode's data for every map: the binary mesh matches the decoded geometry, and on Anubis the networked
// entities stand on the collision mesh: 20 of the 22 (the spawn points) are within 0.1 m above a flat,
// hero-blocking floor (the other two float 3.1 and 9.8 m above it).
func TestGeometry(t *testing.T) {
	s := store(t)
	defer s.Close()
	r := res{s}
	for _, rs := range s.ResourcesOfType(0x002) {
		b, err := s.Read(rs.GUID)
		if err != nil {
			t.Fatal(err)
		}
		m, err := Open(rs.GUID, b)
		if err != nil {
			t.Fatal(err)
		}
		sh, err := m.Shapes()
		if err != nil {
			t.Fatalf("%016x: %v", rs.GUID, err)
		}
		bin, err := m.MeshBinary()
		if err != nil {
			t.Fatal(err)
		}
		if sh.Tris == 0 || len(bin) != (16+12*sh.Verts+18*sh.Tris+3)&^3 || sh.Tris != m.M.Collision.Meshes[0].Tris {
			t.Errorf("%016x: %d tris, %d verts, %d bytes", rs.GUID, sh.Tris, sh.Verts, len(bin))
		}
		if rs.GUID != 0x080000000000005B {
			continue
		}
		v := m.Build(r)
		n, on := 0, 0
		for _, p := range v.Placements {
			if p.Class != "networked" || !p.HasPos {
				continue
			}
			n++
			o := [3]float32{p.Pos[0], p.Pos[1] + 0.5, p.Pos[2]}
			h, _ := m.Ray(o, [3]float32{0, -1, 0}, PartSolid)
			if h.Hit && h.T < 0.6 && h.Normal[1] > 0.9 {
				on++
			}
		}
		if n != 22 || on != 20 {
			t.Errorf("Anubis: %d of %d networked entities over a floor", on, n)
		}
	}
}
