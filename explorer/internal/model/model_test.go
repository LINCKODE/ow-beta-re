package model

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"owcontent/content"
	"owexplorer/internal/names"
	"owstu/stu"
)

func openInstall(t testing.TB) *content.Store {
	install := os.Getenv("OW_INSTALL")
	if install == "" {
		install = filepath.Join(names.FindRepo("."), "game", "owbetaclient")
	}
	if _, err := os.Stat(filepath.Join(install, ".build.info")); err != nil {
		t.Skip("no install at", install)
	}
	s, err := content.Open(install, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestGUIDType(t *testing.T) {
	for g, want := range map[uint64]uint32{0x0d00000000000bc0: 0x00C, 0x0e00000000000005: 0x008, 0x09800000000000c0: 0x01A,
		0x0b10000000000040: 0x08E, 0x0c00000000002e66: 0x004, 0x0580000000000041: 0x01B} {
		if got := GUIDType(g); got != want {
			t.Errorf("GUIDType(%016x) = %03X, want %03X", g, got, want)
		}
		if got := content.TypeOf(g); got != want {
			t.Errorf("content.TypeOf(%016x) = %03X", g, got)
		}
	}
}

// quatMat: row-vector convention (p' = p·M), as the skeleton's matrices.
func trsMat(r TRS) [4][4]float64 {
	x, y, z, w := float64(r.Rot[0]), float64(r.Rot[1]), float64(r.Rot[2]), float64(r.Rot[3])
	m := [4][4]float64{
		{1 - 2*(y*y+z*z), 2 * (x*y + z*w), 2 * (x*z - y*w), 0},
		{2 * (x*y - z*w), 1 - 2*(x*x+z*z), 2 * (y*z + x*w), 0},
		{2 * (x*z + y*w), 2 * (y*z - x*w), 1 - 2*(x*x+y*y), 0},
		{float64(r.Pos[0]), float64(r.Pos[1]), float64(r.Pos[2]), 1},
	}
	for i := range 3 {
		for j := range 3 {
			m[i][j] *= float64(r.Scale[i])
		}
	}
	return m
}

func mul(a, b [4][4]float64) (c [4][4]float64) {
	for i := range 4 {
		for j := range 4 {
			for k := range 4 {
				c[i][j] += a[i][k] * b[k][j]
			}
		}
	}
	return
}

// TestAllModels: every model parses, every LOD decodes (indices inside their vertex buffer), every skeleton's
// parents come before their bones and local ∘ parent world = world (AssetFormats.md §2.6).
func TestAllModels(t *testing.T) {
	s := openInstall(t)
	defer s.Close()
	res := s.ResourcesOfType(TypeModel)
	if len(res) != 2972 {
		t.Errorf("%d models, want 2,972", len(res))
	}
	var mu sync.Mutex
	var tris, skels, poseBad, normals, normalsOff int
	jobs := make(chan content.Resource)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for r := range jobs {
				b, err := s.Read(r.GUID)
				if err != nil {
					t.Errorf("%016x: %v", r.GUID, err)
					continue
				}
				m, err := Parse(b)
				if err != nil {
					t.Errorf("%016x: %v", r.GUID, err)
					continue
				}
				nt, nn, no, bad := 0, 0, 0, 0
				for _, lod := range append([]uint8{0xFF}, m.LODs()...) {
					me, err := m.Mesh(lod)
					if err != nil {
						t.Errorf("%016x lod %x: %v", r.GUID, lod, err)
						continue
					}
					if lod != 0xFF {
						continue
					}
					nt = len(me.Indices) / 3
					for i := 0; i < len(me.Normals); i += 3 {
						l := math.Sqrt(float64(me.Normals[i]*me.Normals[i] + me.Normals[i+1]*me.Normals[i+1] + me.Normals[i+2]*me.Normals[i+2]))
						nn++
						if l < 0.9 || l > 1.1 {
							no++
						}
					}
				}
				if sk := m.Skeleton; sk != nil {
					world := make([][4][4]float64, len(sk.Bones))
					for i, bo := range sk.Bones {
						if int(bo.Parent) >= i || bo.Parent < -1 {
							t.Errorf("%016x: bone %d parent %d", r.GUID, i, bo.Parent)
							break
						}
						world[i] = trsMat(bo.World)
						if bo.Parent < 0 {
							continue
						}
						got := mul(trsMat(bo.Local), world[bo.Parent])
					cmp:
						for a := range 4 {
							for c := range 3 {
								if math.Abs(got[a][c]-world[i][a][c]) > 1e-3 {
									bad++
									t.Logf("%016x bone %d: local∘parent %v, world %v", r.GUID, i, got[3], world[i][3])
									break cmp
								}
							}
						}
					}
				}
				mu.Lock()
				tris += nt
				normals += nn
				normalsOff += no
				poseBad += bad
				if m.Skeleton != nil {
					skels++
				}
				mu.Unlock()
			}
		})
	}
	for _, r := range res {
		jobs <- r
	}
	close(jobs)
	wg.Wait()
	t.Logf("%d models, %d triangles (every submesh), %d skeletons, %d bones off the pose check, normals off unit: %d / %d",
		len(res), tris, skels, poseBad, normalsOff, normals)
	// One bone fails: 0D07 bone 1 (its translation matches, its rotation does not: AssetFormats.md §2.6).
	if skels != 444 || poseBad > 1 {
		t.Errorf("skeletons %d (want 444), pose mismatches %d", skels, poseBad)
	}
	if normalsOff*1000 > normals {
		t.Errorf("%d of %d normals are not unit length", normalsOff, normals)
	}
}

func TestAllMaterialsAndLooks(t *testing.T) {
	s := openInstall(t)
	defer s.Close()
	mats := s.ResourcesOfType(TypeMaterial)
	if len(mats) != 11150 {
		t.Errorf("%d materials, want 11,150", len(mats))
	}
	withColor := 0
	for _, r := range mats {
		b, err := s.Read(r.GUID)
		if err != nil {
			t.Fatal(err)
		}
		m, err := ParseMaterial(b)
		if err != nil {
			t.Errorf("%016x: %v", r.GUID, err)
			continue
		}
		if GUIDType(m.Shader) != 0x088 {
			t.Errorf("%016x: shader %016x", r.GUID, m.Shader)
		}
		for _, tx := range m.Textures {
			if _, ok := s.Find(tx.GUID); !ok || GUIDType(tx.GUID) != 0x004 {
				t.Errorf("%016x: texture %016x missing or not a texture", r.GUID, tx.GUID)
			}
		}
		if m.Texture(SlotBaseColor, SlotColor) != 0 {
			withColor++
		}
	}
	t.Logf("%d materials, %d with a basecolor / color texture", len(mats), withColor)
	looks := s.ResourcesOfType(TypeLook)
	n := 0
	for _, r := range looks {
		b, _ := s.Read(r.GUID)
		lm, err := ParseLook(b)
		if err != nil {
			t.Errorf("look %016x: %v", r.GUID, err)
			continue
		}
		for _, l := range lm {
			if l.Material != 0 && GUIDType(l.Material) != TypeMaterial {
				t.Errorf("look %016x: material %016x", r.GUID, l.Material)
			}
			n++
		}
	}
	t.Logf("%d looks, %d material entries", len(looks), n)
	if len(looks) != 3687 {
		t.Errorf("%d looks, want 3,687", len(looks))
	}
}

// TestKnown: an entity definition's model component pairs model 11CE with look 14B0, which resolves 11 of the
// model's 14 material keys (the other 3 have no entry: AssetFormats.md §4); model 0x23's keys all resolve
// through look 0xC0.
func TestKnown(t *testing.T) {
	s := openInstall(t)
	defer s.Close()
	b, _ := s.Read(0x0400000000000d93)
	d, err := stu.Open(b, stu.File, 0)
	if err != nil {
		t.Fatal(err)
	}
	var pairs [][2]uint64
	for i := range d.NumInstances() {
		ModelLookPairs(d.Decode(i), func(m, l uint64) { pairs = append(pairs, [2]uint64{m, l}) })
	}
	d.Close()
	if len(pairs) != 1 || pairs[0] != [2]uint64{0x0d000000000011ce, 0x09800000000014b0} {
		t.Errorf("pairs %x", pairs)
	}
	for _, c := range [][2]uint64{{0x0d000000000011ce, 0x09800000000014b0}, {0x0d00000000000023, 0x09800000000000c0}} {
		mb, _ := s.Read(c[0])
		m, err := Parse(mb)
		if err != nil {
			t.Fatal(err)
		}
		lb, _ := s.Read(c[1])
		lm, err := ParseLook(lb)
		if err != nil {
			t.Fatal(err)
		}
		missing := 0
		for _, g := range Resolve(m.MaterialKeys, lm) {
			if g == 0 {
				missing++
			}
		}
		if want := map[uint64]int{0x0d000000000011ce: 3}[c[0]]; missing != want {
			t.Errorf("%016x through %016x: %d keys unresolved, want %d", c[0], c[1], missing, want)
		}
	}
}

// TestExport: the GLB of model 0x23 (4 submeshes) is a valid container whose accessors fit its buffer; the OBJ has
// every vertex and triangle.
func TestExport(t *testing.T) {
	s := openInstall(t)
	defer s.Close()
	b, _ := s.Read(0x0d00000000000023)
	m, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	me, err := m.Mesh(0xFF)
	if err != nil {
		t.Fatal(err)
	}
	mats := []ExportMaterial{{Name: "a", ColorName: "t", ColorPNG: []byte("\x89PNG fake")}, {Name: "b"}, {Name: "c"}}
	glb, err := GLB(me, mats)
	if err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	if le.Uint32(glb) != 0x46546C67 || int(le.Uint32(glb[8:])) != len(glb) || le.Uint32(glb[16:]) != 0x4E4F534A {
		t.Fatalf("GLB header %x", glb[:20])
	}
	jl := int(le.Uint32(glb[12:]))
	var doc struct {
		Buffers     []struct{ ByteLength int }
		BufferViews []struct{ ByteOffset, ByteLength int }
		Accessors   []struct {
			BufferView, ComponentType, Count int
			Type                             string
		}
		Meshes []struct{ Primitives []struct{ Material *int } }
		Images []struct{ BufferView int }
	}
	if err := json.Unmarshal(glb[20:20+jl], &doc); err != nil {
		t.Fatal(err)
	}
	bin := glb[20+jl:]
	if int(le.Uint32(bin)) != doc.Buffers[0].ByteLength || le.Uint32(bin[4:]) != 0x004E4942 || len(bin)-8 != doc.Buffers[0].ByteLength {
		t.Fatalf("BIN chunk %d vs %d", le.Uint32(bin), doc.Buffers[0].ByteLength)
	}
	size := map[string]int{"VEC3": 12, "VEC2": 8, "SCALAR": 4}
	for i, a := range doc.Accessors {
		v := doc.BufferViews[a.BufferView]
		if a.Count*size[a.Type] != v.ByteLength || v.ByteOffset+v.ByteLength > doc.Buffers[0].ByteLength {
			t.Errorf("accessor %d: %d × %s in view %+v", i, a.Count, a.Type, v)
		}
	}
	if len(doc.Meshes[0].Primitives) != len(me.Groups) || len(doc.Images) != 1 {
		t.Errorf("%d primitives for %d groups, %d images", len(doc.Meshes[0].Primitives), len(me.Groups), len(doc.Images))
	}
	obj, mtl, files := OBJ(me, mats, "x")
	if n := strings.Count(obj, "\nv "); n != len(me.Positions)/3 {
		t.Errorf("OBJ: %d vertices, want %d", n, len(me.Positions)/3)
	}
	if n := strings.Count(obj, "\nf "); n != len(me.Indices)/3 {
		t.Errorf("OBJ: %d faces, want %d", n, len(me.Indices)/3)
	}
	if !strings.Contains(mtl, "map_Kd x_t.png") || len(files) != 1 {
		t.Errorf("MTL %q, files %d", mtl, len(files))
	}
}

// TestHelperMaterials: +0x68 bit 1 marks exactly the helper / placeholder materials (AssetFormats.md §3.3).
func TestHelperMaterials(t *testing.T) {
	s := openInstall(t)
	defer s.Close()
	var got []uint64
	for _, r := range s.ResourcesOfType(TypeMaterial) {
		b, _ := s.Read(r.GUID)
		if m, err := ParseMaterial(b); err == nil && m.Helper() {
			got = append(got, r.GUID)
		}
	}
	want := []uint64{0x0e00000000000005, 0x0e00000000000794, 0x0e00000000000796, 0x0e00000000000797, 0x0e000000000007a0,
		0x0e000000000007a1, 0x0e000000000007a2, 0x0e000000000007c0, 0x0e00000000001cf0}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("helper materials %x, want %x", got, want)
	}
}
