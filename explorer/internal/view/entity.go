package view

import (
	"errors"
	"fmt"

	"owexplorer/internal/names"
	"owstu/stu"
)

var (
	tEntityDef          = names.Hash("STUEntityDefinition")
	tStatescriptComp    = names.Hash("STUStatescriptComponent")
	hComponentMap       = names.Hash("m_componentMap")
	hGraphsWithOverride = uint32(0x0175ad85) // STUStatescriptComponent: STUStatescriptGraphWithOverrides[]
	hOverrides          = uint32(0x953d7942) // STUStatescriptGraphWithOverrides: schema entries
)

var ErrNotEntity = errors.New("not an entity definition")

type EntityRef struct {
	Path string `json:"path"` // field path inside the component, readable
	GUID string `json:"guid"`
	Name string `json:"name,omitempty"`
}

type EntityGraph struct {
	GUID      string `json:"guid"`
	Name      string `json:"name,omitempty"`
	Overrides int    `json:"overrides"`
}

type Component struct {
	Key      string        `json:"key"` // m_componentMap key (a type hash, named where known)
	KeyHash  uint32        `json:"keyHash"`
	Type     string        `json:"type"`
	Instance int           `json:"instance"`
	Graphs   []EntityGraph `json:"graphs"`
	Refs     []EntityRef   `json:"refs"`
}

type Entity struct {
	Components []Component `json:"components"`
	Refs       []EntityRef `json:"refs"` // references on the definition itself (children, …)
}

// BuildEntity lists an entity definition's components, their Statescript graphs and references.
func BuildEntity(d *stu.Document, r Resolver) (*Entity, error) {
	root := d.Decode(d.Root())
	if !stu.IsA(root.TypeHash(), tEntityDef) {
		return nil, ErrNotEntity
	}
	e := &Entity{Components: []Component{}, Refs: []EntityRef{}}
	for _, f := range root.Items() {
		if f.Info().FieldHash != hComponentMap {
			collectRefs(d, f, fieldLabel(r, f.Info().FieldHash), r, &e.Refs, 0)
		}
	}
	for _, it := range root.Field(hComponentMap).Items() {
		key := it.Info().MapKey
		ci := objIndex(it)
		kname, _ := r.TypeName(key)
		c := Component{Key: kname, KeyHash: key, Instance: ci, Graphs: []EntityGraph{}, Refs: []EntityRef{}}
		if ci >= 0 {
			t := instType(d, ci)
			c.Type, _ = r.TypeName(t)
			cv := d.Decode(ci)
			if stu.IsA(t, tStatescriptComp) {
				for _, gw := range cv.Field(hGraphsWithOverride).Items() {
					guid, _, err := gw.Field(hGraph).Ref()
					if err != nil {
						continue
					}
					c.Graphs = append(c.Graphs, EntityGraph{GUID: fmt.Sprintf("%016x", guid), Name: r.GUIDName(guid),
						Overrides: gw.Field(hOverrides).Len()})
				}
			}
			collectRefs(d, cv, "", r, &c.Refs, 0)
		}
		e.Components = append(e.Components, c)
	}
	return e, nil
}

func fieldLabel(r Resolver, h uint32) string { n, _ := r.TypeName(h); return n }

// collectRefs appends every resource reference under v (following object pointers a few levels).
func collectRefs(d *stu.Document, v stu.Value, path string, r Resolver, out *[]EntityRef, depth int) {
	if depth > 6 || len(*out) > 500 {
		return
	}
	info := v.Info()
	switch info.Type {
	case stu.Ref:
		if info.GUID != 0 {
			*out = append(*out, EntityRef{Path: path, GUID: fmt.Sprintf("%016x", info.GUID), Name: r.GUIDName(info.GUID)})
		}
	case stu.Inline:
		for _, f := range v.Items() {
			p := fieldLabel(r, f.Info().FieldHash)
			if path != "" {
				p = path + "." + p
			}
			collectRefs(d, f, p, r, out, depth+1)
		}
	case stu.List, stu.Map:
		if info.Kind == stu.KindPrimitiveList || info.Kind == stu.KindEnumList {
			return
		}
		for k, it := range v.Items() {
			collectRefs(d, it, fmt.Sprintf("%s[%d]", path, k), r, out, depth+1)
		}
	case stu.Object:
		collectRefs(d, d.Decode(info.Instance), path, r, out, depth+1)
	}
}
