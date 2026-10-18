package main

import (
	"fmt"
	"math"
	"sort"

	"owcontent/content"
	"owexplorer/internal/model"
)

// scan: every model, material and look through internal/model; prints statistics and failures.
func scan(s *content.Store) {
	cnt := map[string]int{}
	fails := []string{}
	keyLooks := map[uint64][]uint64{}
	nLooks := 0
	for _, r := range s.ResourcesOfType(model.TypeLook) {
		b, err := s.Read(r.GUID)
		if err != nil {
			fails = append(fails, fmt.Sprintf("look %016x: %v", r.GUID, err))
			continue
		}
		lm, err := model.ParseLook(b)
		if err != nil {
			fails = append(fails, fmt.Sprintf("look %016x: %v", r.GUID, err))
			continue
		}
		nLooks++
		cnt[fmt.Sprintf("look materials %d", min(len(lm), 5))]++
		for _, l := range lm {
			keyLooks[l.Key] = append(keyLooks[l.Key], r.GUID)
			if l.Material != 0 && model.GUIDType(l.Material) != model.TypeMaterial {
				fails = append(fails, fmt.Sprintf("look %016x: material %016x type %03X", r.GUID, l.Material, model.GUIDType(l.Material)))
			}
		}
	}
	tris, verts := 0, 0
	var badNormal, nNormal int
	for _, r := range s.ResourcesOfType(model.TypeModel) {
		b, err := s.Read(r.GUID)
		if err != nil {
			fails = append(fails, fmt.Sprintf("model %016x: %v", r.GUID, err))
			continue
		}
		m, err := model.Parse(b)
		if err != nil {
			fails = append(fails, fmt.Sprintf("model %016x: %v", r.GUID, err))
			continue
		}
		lods := m.LODs()
		cnt[fmt.Sprintf("lods %d", len(lods))]++
		if m.Skeleton != nil {
			cnt["skeleton"]++
			for i, bo := range m.Skeleton.Bones {
				if int(bo.Parent) >= i || bo.Parent < -1 {
					fails = append(fails, fmt.Sprintf("model %016x: bone %d parent %d", r.GUID, i, bo.Parent))
					break
				}
			}
		}
		for _, lod := range append([]uint8{0xFF}, lods...) {
			me, err := m.Mesh(lod)
			if err != nil {
				fails = append(fails, fmt.Sprintf("model %016x lod %x: %v", r.GUID, lod, err))
				continue
			}
			if lod == 0xFF {
				tris += len(me.Indices) / 3
				verts += len(me.Positions) / 3
				for i := 0; i < len(me.Normals); i += 3 {
					l := math.Sqrt(float64(me.Normals[i]*me.Normals[i] + me.Normals[i+1]*me.Normals[i+1] + me.Normals[i+2]*me.Normals[i+2]))
					nNormal++
					if l < 0.9 || l > 1.1 {
						badNormal++
					}
				}
			}
		}
		// looks whose keys cover the model's
		cand := map[uint64]int{}
		for _, k := range m.MaterialKeys {
			for _, l := range keyLooks[k] {
				cand[l]++
			}
		}
		full := 0
		for range cand {
			full++
		}
		cnt[fmt.Sprintf("looks sharing a key: %s", bucket(full))]++
		if len(m.Refs) > 0 {
			cnt["models with refs"]++
		}
	}
	for _, r := range s.ResourcesOfType(model.TypeMaterial) {
		b, err := s.Read(r.GUID)
		if err != nil {
			fails = append(fails, fmt.Sprintf("material %016x: %v", r.GUID, err))
			continue
		}
		m, err := model.ParseMaterial(b)
		if err != nil {
			fails = append(fails, fmt.Sprintf("material %016x: %v", r.GUID, err))
			continue
		}
		for _, t := range m.Textures {
			if _, ok := s.Find(t.GUID); !ok {
				cnt["material texture missing"]++
			}
		}
		if m.Texture(model.SlotBaseColor, model.SlotColor) != 0 {
			cnt["material with basecolor/color"]++
		}
	}
	keys := []string{}
	for k := range cnt {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("%-40s %d\n", k, cnt[k])
	}
	fmt.Printf("looks %d, triangles (all LODs) %d, vertices %d, normals off unit %d / %d\n", nLooks, tris, verts, badNormal, nNormal)
	fmt.Printf("failures: %d\n", len(fails))
	for i, f := range fails {
		if i < 30 {
			fmt.Println(" ", f)
		}
	}
}

func bucket(n int) string {
	switch {
	case n == 0:
		return "0"
	case n == 1:
		return "1"
	case n <= 5:
		return "2-5"
	case n <= 50:
		return "6-50"
	}
	return ">50"
}
