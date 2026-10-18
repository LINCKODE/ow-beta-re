package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"owcontent/content"
	"owexplorer/internal/model"
	"owexplorer/internal/texture"
)

// export writes model (with look, 0 = none) at a LOD as .glb or .obj (by the path's extension), with the colour
// textures (mip ≤ 1024 px): the same code path as the explorer's Export buttons (writeModel in app_model.go).
func export(s *content.Store, mg, lg uint64, lod uint8, path string) error {
	b, err := s.Read(mg)
	if err != nil {
		return err
	}
	m, err := model.Parse(b)
	if err != nil {
		return err
	}
	me, err := m.Mesh(lod)
	if err != nil {
		return err
	}
	mats := make([]model.ExportMaterial, len(m.MaterialKeys))
	if lg != 0 {
		lb, err := s.Read(lg)
		if err != nil {
			return err
		}
		look, err := model.ParseLook(lb)
		if err != nil {
			return err
		}
		read := func(g uint64) ([]byte, error) {
			b, err := s.Read(g)
			if errors.Is(err, content.ErrNotFound) {
				return nil, texture.ErrMissing
			}
			return b, err
		}
		for i, mat := range model.Resolve(m.MaterialKeys, look) {
			mats[i].Name = fmt.Sprintf("slot%d", i)
			if mat == 0 {
				continue
			}
			mats[i].Name = fmt.Sprintf("mat_%016x", mat)
			mb, _ := s.Read(mat)
			mt, err := model.ParseMaterial(mb)
			if err != nil {
				continue
			}
			tg := mt.Texture(model.SlotBaseColor, model.SlotColor)
			if tg == 0 {
				continue
			}
			t, err := texture.Load(read, tg)
			if err != nil {
				continue
			}
			mip := 0
			for mip < len(t.Mips)-1 && (max(t.Mips[mip].Width, t.Mips[mip].Height) > 1024 || !t.Available(mip)) {
				mip++
			}
			img, err := t.Render(mip, 0, texture.Display{Auto: true})
			if err != nil {
				continue
			}
			if p, err := texture.PNG(img); err == nil {
				mats[i].ColorPNG, mats[i].ColorName = p, fmt.Sprintf("%016x", tg)
			}
		}
	}
	if strings.HasSuffix(path, ".glb") {
		g, err := model.GLB(me, mats)
		if err != nil {
			return err
		}
		return os.WriteFile(path, g, 0o644)
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	obj, mtl, files := model.OBJ(me, mats, base)
	for n, b := range files {
		os.WriteFile(filepath.Join(filepath.Dir(path), n), b, 0o644)
	}
	os.WriteFile(filepath.Join(filepath.Dir(path), base+".mtl"), []byte(mtl), 0o644)
	return os.WriteFile(path, []byte(obj), 0o644)
}
