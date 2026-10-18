package model

import (
	"bytes"

	"owcontent/content"
	"owexplorer/internal/refindex"
	"owstu/stu"
)

// Source adds model / material references to the reference index (docs/Explorer.md "Plugging in a format"):
//   - model → the 0x08E resources its loader requests (model.effect);
//   - material → its textures (material.texture, aux = binding index) and its shader (material.shader, +0x08: material.ref);
//   - model → look, for every STU instance that names both (STUModelComponent m_model + m_look, …): model.look.
//
// Look → material is already a stu.field edge; map placements pair models and looks through the maps source
// (map.look).
type Source struct{}

const (
	kindEffect = iota
	kindTexture
	kindShader
	kindRef
	kindLook
)

func (Source) ID() string   { return "model" }
func (Source) Version() int { return 1 }
func (Source) Kinds() []refindex.KindInfo {
	return []refindex.KindInfo{
		{Name: "model.effect", Label: "model effect"},
		{Name: "material.texture", Label: "material texture"},
		{Name: "material.shader", Label: "material shader"},
		{Name: "material.ref", Label: "material ref"},
		{Name: "model.look", Label: "model look (STU)"},
	}
}
func (Source) Workers() int { return 8 }

func (Source) Select(c *refindex.Ctx, r content.Resource) bool {
	return r.Type == TypeModel || r.Type == TypeMaterial || (c.IsSTU(r.Type) && r.Type != TypeLook)
}

func (Source) Scan(c *refindex.Ctx, r content.Resource, e *refindex.Emitter) error {
	switch r.Type {
	case TypeModel:
		b, err := c.Store.Read(r.GUID)
		if err != nil {
			return err
		}
		m, err := Parse(b)
		if err != nil {
			return err
		}
		for _, g := range m.Refs {
			e.Ref(r.GUID, g, kindEffect, 0)
		}
		return nil
	case TypeMaterial:
		b, err := c.Store.Read(r.GUID)
		if err != nil {
			return err
		}
		m, err := ParseMaterial(b)
		if err != nil {
			return err
		}
		e.Ref(r.GUID, m.Shader, kindShader, 0)
		e.Ref(r.GUID, m.Ref, kindRef, 0)
		for i, t := range m.Textures {
			e.Ref(r.GUID, t.GUID, kindTexture, uint32(i))
		}
		return nil
	}
	b, err := c.Store.Read(r.GUID)
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(b, []byte("DUTS")) || !bytes.Contains(b, lookTag[:]) {
		return nil
	}
	d, err := stu.Open(b, stu.File, 0)
	if err != nil {
		return nil // the stu source reports broken documents
	}
	defer d.Close()
	for i := range d.NumInstances() {
		ModelLookPairs(d.Decode(i), func(model, look uint64) { e.Ref(model, look, kindLook, 0) })
	}
	return nil
}

// lookTag: a document naming a look contains a GUID whose bytes 6–7 are those of type 0x01A (0x0980…): a cheap
// filter before parsing.
var lookTag = [2]byte{0x80, 0x09}

// ModelLookPairs calls f for every (model, look) pair named by the fields of one object and of the inline
// objects under it (depth-first).
func ModelLookPairs(v stu.Value, f func(model, look uint64)) {
	if !v.Valid() {
		return
	}
	var models, looks []uint64
	for _, it := range v.Items() {
		info := it.Info()
		switch info.Type {
		case stu.Ref:
			switch GUIDType(info.GUID) {
			case TypeModel:
				models = append(models, info.GUID)
			case TypeLook:
				looks = append(looks, info.GUID)
			}
		case stu.Inline:
			ModelLookPairs(it, f)
		}
	}
	for _, m := range models {
		for _, l := range looks {
			f(m, l)
		}
	}
}
