package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"owcontent/content"
	"owexplorer/internal/catalog"
	"owexplorer/internal/mapview"
	"owexplorer/internal/model"
	"owexplorer/internal/texture"
)

// Models, materials and looks (T064): the Model tab (App.Model, geometry over the asset server as /model/<guid>.bin),
// the Material tab (App.Material), material textures for the 3D preview (/mtex/<guid>.png), glTF / OBJ export.
// Formats: docs/AssetFormats.md §2–§4.

var (
	modelCache = newLRU[*model.Model](512<<20, func(m *model.Model) int { return m.Size })
	lookCache  = newLRU[[]model.LookMaterial](8<<20, func(l []model.LookMaterial) int { return 64 + 16*len(l) })
	mtexCache  = newLRU[[]byte](96<<20, func(b []byte) int { return len(b) })
)

func (a *App) loadModel(c *catalog.Catalog, g uint64) (*model.Model, error) {
	key := fmt.Sprintf("%s/%016x", c.Describe, g)
	if m, ok := modelCache.get(key); ok {
		return m, nil
	}
	b, err := c.Store.Read(g)
	if err != nil {
		return nil, err
	}
	m, err := model.Parse(b)
	if err != nil {
		return nil, err
	}
	modelCache.put(key, m)
	return m, nil
}

func (a *App) loadLook(c *catalog.Catalog, g uint64) ([]model.LookMaterial, error) {
	key := fmt.Sprintf("%s/%016x", c.Describe, g)
	if l, ok := lookCache.get(key); ok {
		return l, nil
	}
	b, err := c.Store.Read(g)
	if err != nil {
		return nil, err
	}
	l, err := model.ParseLook(b)
	if err != nil {
		return nil, err
	}
	lookCache.put(key, l)
	return l, nil
}

func (a *App) loadMaterial(c *catalog.Catalog, g uint64) (*model.Material, error) {
	b, err := c.Store.Read(g)
	if err != nil {
		return nil, err
	}
	return model.ParseMaterial(b)
}

// keyLooks: material key → looks that have an entry for it (built once per install, for the "keys" fallback).
var keyLooks struct {
	sync.Mutex
	describe string
	m        map[uint64][]uint64
}

func (a *App) looksByKey(c *catalog.Catalog) map[uint64][]uint64 {
	keyLooks.Lock()
	defer keyLooks.Unlock()
	if keyLooks.describe == c.Describe && keyLooks.m != nil {
		return keyLooks.m
	}
	m := map[uint64][]uint64{}
	for _, r := range c.Store.ResourcesOfType(model.TypeLook) {
		l, err := a.loadLook(c, r.GUID)
		if err != nil {
			continue
		}
		for _, e := range l {
			m[e.Key] = append(m[e.Key], r.GUID)
		}
	}
	keyLooks.describe, keyLooks.m = c.Describe, m
	return m
}

// --- views ---------------------------------------------------------------------------------------------------

type ElementView struct {
	Semantic string `json:"semantic"`
	Index    int    `json:"index"`
	Format   string `json:"format"`
	Stream   int    `json:"stream"`
	Offset   int    `json:"offset"`
}

type VBView struct {
	Count    int           `json:"count"`
	Hash     string        `json:"hash"`
	Strides  [2]int        `json:"strides"`
	Elements []ElementView `json:"elements"`
}

type LODView struct {
	Mask      int `json:"mask"`
	Triangles int `json:"triangles"`
	Submeshes int `json:"submeshes"`
}

// LookOption is a look the model can be shown with.
type LookOption struct {
	GUID     string `json:"guid"`
	Name     string `json:"name,omitempty"`
	Source   string `json:"source"`   // "STU" (model component …), "map" (placed together), "keys" (key match only)
	Resolved int    `json:"resolved"` // material keys of the model the look has an entry for
}

// SlotView is one material key of the model, resolved through the chosen look.
type SlotView struct {
	Key       string `json:"key"`
	Material  string `json:"material,omitempty"`
	Shader    string `json:"shader,omitempty"`
	Color     string `json:"color,omitempty"`   // the texture shown as colour (basecolor / color slot, else the first sRGB one)
	Opacity   string `json:"opacity,omitempty"` // the opacity mask (alpha)
	Decal     bool   `json:"decal,omitempty"`   // an opacity mask but no colour texture: not drawn by the preview
	Normal    string `json:"normal,omitempty"`  // the normal map (not used by the preview's shading yet)
	Textures  int    `json:"textures"`
	Submeshes int    `json:"submeshes"`
	Helper    bool   `json:"helper,omitempty"` // the material is a helper / editor material (+0x68 bit 1)
}

type BoneView struct {
	Parent int        `json:"parent"`
	ID     string     `json:"id"`
	Pos    [3]float32 `json:"pos"` // world bind position
}

type SkeletonView struct {
	Hash  string     `json:"hash"`
	Bones []BoneView `json:"bones"`
}

// ModelView is the Model tab.
type ModelView struct {
	GUID      string          `json:"guid"`
	Size      int             `json:"size"`
	Split     [3]uint32       `json:"split"`
	LODs      []LODView       `json:"lods"` // the finest first; one entry of mask 255 if the model has no LODs
	VBs       []VBView        `json:"vbs"`
	IBs       []int           `json:"ibs"` // index counts
	Submeshes []model.Submesh `json:"submeshes"`
	Look      string          `json:"look"` // the look used for the materials ("" = none)
	Looks     []LookOption    `json:"looks"`
	Slots     []SlotView      `json:"slots"`
	Skeleton  *SkeletonView   `json:"skeleton,omitempty"`
	Refs      []string        `json:"refs"`
	Opened    string          `json:"opened"` // the GUID opened (a look opens its model)
}

func hex16(g uint64) string { return fmt.Sprintf("%016x", g) }

// modelLooks lists the looks placed with a model (reference index: model.look, map.look), then, if there are none,
// the looks that resolve the most of its keys.
func (a *App) modelLooks(c *catalog.Catalog, g uint64, m *model.Model) []LookOption {
	var out []LookOption
	resolved := func(lg uint64) int {
		l, err := a.loadLook(c, lg)
		if err != nil {
			return 0
		}
		n := 0
		for _, x := range model.Resolve(m.MaterialKeys, l) {
			if x != 0 {
				n++
			}
		}
		return n
	}
	p, _ := c.Neighbors(catalog.NeighborQuery{GUID: hex16(g), Dir: "out", Kinds: []string{"model.look", "map.look"}, Count: 500})
	for _, r := range p.Rows {
		lg, err := parse(r.GUID)
		if err != nil || content.TypeOf(lg) != model.TypeLook {
			continue
		}
		src := "map"
		for _, k := range r.Kinds {
			if k == "model.look" {
				src = "STU"
			}
		}
		out = append(out, LookOption{GUID: r.GUID, Name: r.Name, Source: src, Resolved: resolved(lg)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Resolved != out[j].Resolved {
			return out[i].Resolved > out[j].Resolved
		}
		return out[i].Source == "STU" && out[j].Source != "STU"
	})
	if len(out) > 0 || len(m.MaterialKeys) == 0 {
		return out
	}
	byKey := a.looksByKey(c)
	cnt := map[uint64]int{}
	for _, k := range m.MaterialKeys {
		for _, l := range byKey[k] {
			cnt[l]++
		}
	}
	for lg := range cnt {
		out = append(out, LookOption{GUID: hex16(lg), Name: c.Name(lg).Text, Source: "keys", Resolved: resolved(lg)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Resolved != out[j].Resolved {
			return out[i].Resolved > out[j].Resolved
		}
		return out[i].GUID < out[j].GUID
	})
	return out[:min(len(out), 12)]
}

// modelFor: a look opens the first model placed with it.
func (a *App) modelFor(c *catalog.Catalog, g uint64) (uint64, uint64, error) {
	if content.TypeOf(g) == model.TypeModel {
		return g, 0, nil
	}
	if content.TypeOf(g) != model.TypeLook {
		return 0, 0, fmt.Errorf("%016x is not a model or a look", g)
	}
	p, _ := c.Neighbors(catalog.NeighborQuery{GUID: hex16(g), Dir: "in", Kinds: []string{"model.look", "map.look"}, Count: 50})
	for _, r := range p.Rows {
		if mg, err := parse(r.GUID); err == nil && content.TypeOf(mg) == model.TypeModel {
			return mg, g, nil
		}
	}
	return 0, 0, fmt.Errorf("no model is placed with look %016x", g)
}

// Model is the Model tab: guid is a model or a look; look chooses the look ("" = the best one).
func (a *App) Model(guid, look string) (ModelView, error) {
	c, err := a.catalog()
	if err != nil {
		return ModelView{}, err
	}
	g0, err := parse(guid)
	if err != nil {
		return ModelView{}, err
	}
	g, lookFrom, err := a.modelFor(c, g0)
	if err != nil {
		return ModelView{}, err
	}
	m, err := a.loadModel(c, g)
	if err != nil {
		return ModelView{}, err
	}
	v := ModelView{GUID: hex16(g), Opened: hex16(g0), Size: m.Size, Split: m.Split, Submeshes: m.Submeshes}
	for _, lod := range m.LODs() {
		n := 0
		for _, s := range m.Submeshes {
			if s.LODMask&lod != 0 {
				n++
			}
		}
		v.LODs = append(v.LODs, LODView{Mask: int(lod), Triangles: m.Triangles(lod), Submeshes: n})
	}
	if len(v.LODs) == 0 {
		v.LODs = []LODView{{Mask: 255, Triangles: m.Triangles(0xFF), Submeshes: len(m.Submeshes)}}
	}
	for _, vb := range m.VBs {
		vv := VBView{Count: int(vb.Count), Hash: fmt.Sprintf("%08x", vb.Hash), Strides: [2]int{int(vb.Strides[0]), int(vb.Strides[1])}}
		for _, e := range vb.Elements {
			vv.Elements = append(vv.Elements, ElementView{model.SemanticName(e.Semantic), int(e.Index), model.FormatName(e.Format), int(e.Stream), int(e.Offset)})
		}
		v.VBs = append(v.VBs, vv)
	}
	for _, ib := range m.IBs {
		v.IBs = append(v.IBs, int(ib.Count))
	}
	for _, r := range m.Refs {
		v.Refs = append(v.Refs, hex16(r))
	}
	v.Looks = a.modelLooks(c, g, m)
	var lg uint64
	switch {
	case look != "":
		lg, _ = parse(look)
	case lookFrom != 0:
		lg = lookFrom
	case len(v.Looks) > 0:
		lg, _ = parse(v.Looks[0].GUID)
	}
	var mats []uint64
	if lg != 0 {
		if l, err := a.loadLook(c, lg); err == nil {
			v.Look = hex16(lg)
			mats = model.Resolve(m.MaterialKeys, l)
		}
	}
	uses := make([]int, len(m.MaterialKeys))
	for _, s := range m.Submeshes {
		uses[s.Material]++
	}
	for i, k := range m.MaterialKeys {
		sv := SlotView{Key: fmt.Sprintf("%016x", k), Submeshes: uses[i]}
		if i < len(mats) && mats[i] != 0 {
			sv.Material = hex16(mats[i])
			if mt, err := a.loadMaterial(c, mats[i]); err == nil {
				sv.Shader = hex16(mt.Shader)
				sv.Textures = len(mt.Textures)
				sv.Helper = mt.Helper()
				if t := mt.ColorTexture(a.srgbTexture(c)); t != 0 {
					sv.Color = hex16(t)
				}
				if t := mt.Texture(model.SlotOpacity); t != 0 {
					sv.Opacity = hex16(t)
					sv.Decal = sv.Color == ""
				}
				if t := mt.Texture(model.SlotNormal); t != 0 {
					sv.Normal = hex16(t)
				}
			}
		}
		v.Slots = append(v.Slots, sv)
	}
	if sk := m.Skeleton; sk != nil {
		s := &SkeletonView{Hash: fmt.Sprintf("%08x", sk.Hash)}
		for _, b := range sk.Bones {
			s.Bones = append(s.Bones, BoneView{Parent: int(b.Parent), ID: fmt.Sprintf("%08x", b.ID), Pos: b.World.Pos})
		}
		v.Skeleton = s
	}
	return v, nil
}

// meshBinary is /model/<guid>.bin?lod=<mask>[&proxy=1] (proxy: with the flag-0x10 proxy submeshes): little-endian "OWMD", version 2 (T071; 1 had no colours / UV1), V, I, G, then G × {first index,
// index count, material slot, submesh} (u32), f32 positions [3V], normals [3V], uvs [2V], u32 indices [I].
func (a *App) meshBinary(c *catalog.Catalog, guid string, lod int, proxies bool) ([]byte, error) {
	g, err := parse(guid)
	if err != nil {
		return nil, err
	}
	m, err := a.loadModel(c, g)
	if err != nil {
		return nil, err
	}
	me, err := m.MeshWith(uint8(lod), proxies)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	le := binary.LittleEndian
	buf.WriteString("OWMD")
	// version 2 (T071): after the indices, RGBA8 vertex colours (semantic 8; white where a submesh has none) and UV set 1
	for _, x := range []uint32{2, uint32(len(me.Positions) / 3), uint32(len(me.Indices)), uint32(len(me.Groups))} {
		binary.Write(&buf, le, x)
	}
	for _, gr := range me.Groups {
		binary.Write(&buf, le, [4]uint32{uint32(gr.Index), uint32(gr.Count), uint32(gr.Material), uint32(gr.Submesh)})
	}
	binary.Write(&buf, le, me.Positions)
	binary.Write(&buf, le, me.Normals)
	binary.Write(&buf, le, me.UVs)
	binary.Write(&buf, le, me.Indices)
	cols := me.Colors
	if cols == nil {
		cols = bytes.Repeat([]byte{255}, len(me.Positions)/3*4)
	}
	buf.Write(cols)
	binary.Write(&buf, le, me.UV1s)
	return buf.Bytes(), nil
}

// materialTexture is /mtex/<guid>.png?max=N: the largest available mip of surface 0 no bigger than N (default 1024),
// as colour (no exposure, no Z from XY).
func (a *App) materialTexture(c *catalog.Catalog, guid string, maxSize int) ([]byte, error) {
	if maxSize <= 0 {
		maxSize = 1024
	}
	key := fmt.Sprintf("%s/%s/%d", c.Describe, guid, maxSize)
	if b, ok := mtexCache.get(key); ok {
		return b, nil
	}
	g, err := parse(guid)
	if err != nil {
		return nil, err
	}
	t, err := a.loadTexture(c, textureOf(g))
	if err != nil {
		return nil, err
	}
	mip := -1
	for i, m := range t.Mips {
		if len(m.Data) > 0 && m.Data[0] != nil {
			mip = i
			if max(m.Width, m.Height) <= maxSize {
				break
			}
		}
	}
	if mip < 0 {
		return nil, errors.New("no mip available")
	}
	img, err := t.Render(mip, 0, texture.Display{Auto: true})
	if err != nil {
		return nil, err
	}
	b, err := texture.PNG(img)
	if err != nil {
		return nil, err
	}
	mtexCache.put(key, b)
	return b, nil
}

// --- materials -------------------------------------------------------------------------------------------------

type MatTextureView struct {
	GUID     string `json:"guid"`
	Slot     string `json:"slot"`
	SlotName string `json:"slotName,omitempty"`
	Flags    uint32 `json:"flags"`
	Format   string `json:"format,omitempty"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Missing  bool   `json:"missing,omitempty"`
}

type MatBufferView struct {
	Hash   string    `json:"hash"`
	Size   int       `json:"size"`
	Floats []float32 `json:"floats"` // the first 64 values
}

// MaterialView is the Material tab.
type MaterialView struct {
	GUID     string           `json:"guid"`
	Shader   string           `json:"shader"`
	Ref      string           `json:"ref,omitempty"`
	Color    [4]float32       `json:"color"`
	Helper   bool             `json:"helper"`
	Flags68  int              `json:"flags68"`
	Textures []MatTextureView `json:"textures"`
	Buffers  []MatBufferView  `json:"buffers"`
	Params   []MatParamView   `json:"params"`
	Preview  model.Preview    `json:"preview"` // T071: family, slot → input mapping (internal/model/preview.go)
}

// MatParamView is one item of the parameter stream (+0x10).
type MatParamView struct {
	Hash   string    `json:"hash"`
	Type   int       `json:"type"`
	Values []float32 `json:"values"`
	Use    string    `json:"use,omitempty"`
}

func (a *App) Material(guid string) (MaterialView, error) {
	c, err := a.catalog()
	if err != nil {
		return MaterialView{}, err
	}
	g, err := parse(guid)
	if err != nil {
		return MaterialView{}, err
	}
	b, err := c.Store.Read(g)
	if err != nil {
		return MaterialView{}, err
	}
	m, err := model.ParseMaterial(b)
	if err != nil {
		return MaterialView{}, err
	}
	// empty lists, not null: 2,983 materials have no constant buffers and 56 no textures (T071)
	v := MaterialView{GUID: hex16(g), Shader: hex16(m.Shader), Color: m.Color, Helper: m.Helper(), Flags68: int(m.Flags68),
		Textures: []MatTextureView{}, Buffers: []MatBufferView{}, Params: []MatParamView{}, Preview: m.Preview(a.texInfo(c))}
	for _, pr := range m.Params {
		pv := MatParamView{Hash: fmt.Sprintf("%08x", pr.Hash), Type: int(pr.Type), Values: pr.Values}
		for _, u := range v.Preview.Params {
			if u.Hash == pv.Hash {
				pv.Use = u.Use + " [" + u.Conf + "]"
			}
		}
		v.Params = append(v.Params, pv)
	}
	if m.Ref != 0 {
		v.Ref = hex16(m.Ref)
	}
	for _, t := range m.Textures {
		tv := MatTextureView{GUID: hex16(t.GUID), Slot: fmt.Sprintf("%08x", t.Slot), SlotName: model.SlotName(t.Slot), Flags: t.Flags}
		if tb, err := c.Store.Read(t.GUID); err == nil {
			if h, err := texture.ParseHeader(tb); err == nil {
				tv.Format, tv.Width, tv.Height = texture.FormatName(h.Format), int(h.Width), int(h.Height)
			}
		} else {
			tv.Missing = true
		}
		v.Textures = append(v.Textures, tv)
	}
	for _, bf := range m.Buffers {
		bv := MatBufferView{Hash: fmt.Sprintf("%08x", bf.Hash), Size: int(bf.Size), Floats: []float32{}}
		for i := 0; i+4 <= int(bf.Size) && i < 256; i += 4 {
			bv.Floats = append(bv.Floats, math.Float32frombits(binary.LittleEndian.Uint32(b[int(bf.Offset)+i:])))
		}
		v.Buffers = append(v.Buffers, bv)
	}
	return v, nil
}

// --- export ------------------------------------------------------------------------------------------------------

// exportMaterials: one per material slot of the model, with its colour texture as PNG.
func (a *App) exportMaterials(c *catalog.Catalog, v ModelView) []model.ExportMaterial {
	out := make([]model.ExportMaterial, len(v.Slots))
	for i, s := range v.Slots {
		out[i].Name = "slot" + strconv.Itoa(i)
		if s.Material != "" {
			out[i].Name = "mat_" + s.Material
		}
		if s.Color != "" {
			if b, err := a.materialTexture(c, s.Color, 2048); err == nil {
				out[i].ColorPNG = b
				out[i].ColorName = s.Color
			}
		}
	}
	return out
}

// ExportModel saves the model at a LOD with the look's colour textures: format "glb" (one file, textures inside)
// or "obj" (.obj + .mtl + PNGs next to it).
func (a *App) ExportModel(guid, look string, lod int, format string) (string, error) {
	c, err := a.catalog()
	if err != nil {
		return "", err
	}
	v, err := a.Model(guid, look)
	if err != nil {
		return "", err
	}
	g, _ := parse(v.GUID)
	m, err := a.loadModel(c, g)
	if err != nil {
		return "", err
	}
	me, err := m.Mesh(uint8(lod))
	if err != nil {
		return "", err
	}
	ext := map[string]string{"glb": "glb", "obj": "obj"}[format]
	if ext == "" {
		return "", fmt.Errorf("unknown export format %q", format)
	}
	name := fmt.Sprintf("%s.%s", v.GUID, ext)
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "Export " + name, DefaultFilename: name,
		Filters: []runtime.FileFilter{{DisplayName: strings.ToUpper(ext), Pattern: "*." + ext}}})
	if err != nil || path == "" {
		return "", err
	}
	return path, writeModel(path, format, me, a.exportMaterials(c, v))
}

func writeModel(path, format string, me *model.Mesh, mats []model.ExportMaterial) error {
	if format == "glb" {
		b, err := model.GLB(me, mats)
		if err != nil {
			return err
		}
		return os.WriteFile(path, b, 0o644)
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	dir := filepath.Dir(path)
	obj, mtl, files := model.OBJ(me, mats, base)
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, base+".mtl"), []byte(mtl), 0o644); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(obj), 0o644)
}

// modelStatus is the Info tab's decoder line for models, materials and looks.
func (a *App) modelStatus(c *catalog.Catalog, g uint64, b []byte) DecoderStatus {
	switch content.TypeOf(g) {
	case model.TypeModel:
		m, err := model.Parse(b)
		if err != nil {
			return DecoderStatus{Name: "model (internal/model)", Status: "error", Detail: err.Error()}
		}
		sk := "no skeleton"
		if m.Skeleton != nil {
			sk = fmt.Sprintf("%d bones", len(m.Skeleton.Bones))
		}
		return DecoderStatus{Name: "model (internal/model)", Status: "ok", Detail: fmt.Sprintf("%d submeshes, %d LODs, %d triangles (finest), %d material keys, %s; see the Model tab",
			len(m.Submeshes), max(1, len(m.LODs())), m.Triangles(0), len(m.MaterialKeys), sk)}
	case model.TypeMaterial:
		m, err := model.ParseMaterial(b)
		if err != nil {
			return DecoderStatus{Name: "material (internal/model)", Status: "error", Detail: err.Error()}
		}
		return DecoderStatus{Name: "material (internal/model)", Status: "ok", Detail: fmt.Sprintf("shader %016x, %d textures, %d constant buffers; see the Material tab",
			m.Shader, len(m.Textures), len(m.Buffers))}
	}
	return DecoderStatus{}
}

// --- map visual geometry (3D map mode, step 2) ---------------------------------------------------------------------

// MapVisualBatch is a model + look placed on a map, with what the 3D view needs to draw it.
type MapVisualBatch struct {
	mapviewBatch
	LOD       int      `json:"lod"`       // LOD mask drawn (the finest)
	Triangles int      `json:"triangles"` // per instance
	Colors    []string `json:"colors"`    // per material slot: the colour texture ("" none)
	Helpers   []bool   `json:"helpers"`   // per material slot: a helper / editor material (+0x68 bit 1)
	Opacity   []string `json:"opacity"`   // per material slot: the opacity mask ("" none)
	Decals    []bool   `json:"decals"`    // per material slot: opacity but no colour: not drawn
	Materials []string `json:"materials"` // per material slot: the material ("" none): the PBR mode (T071)
	Error     string   `json:"error,omitempty"`
}

// MapVisual lists a map's placed models (record types 1, 2, 8) grouped by model + look, with their instances.
func (a *App) MapVisual(guid string) (MapVisualView, error) {
	c, m, err := a.openMap(guid)
	if err != nil {
		return MapVisualView{}, err
	}
	a.mmu.Lock()
	batches := m.Visual()
	a.mmu.Unlock()
	v := MapVisualView{Batches: make([]MapVisualBatch, len(batches))}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, b := range batches {
		v.Batches[i].mapviewBatch = b
		wg.Add(1)
		sem <- struct{}{}
		go func(out *MapVisualBatch) {
			defer func() { <-sem; wg.Done() }()
			mg, _ := parse(out.Model)
			md, err := a.loadModel(c, mg)
			if err != nil {
				out.Error = err.Error()
				return
			}
			lod := uint8(0xFF)
			if l := md.LODs(); len(l) > 0 {
				lod = l[0]
			}
			out.LOD, out.Triangles = int(lod), md.Triangles(lod)
			out.Colors = make([]string, len(md.MaterialKeys))
			out.Helpers = make([]bool, len(md.MaterialKeys))
			out.Opacity = make([]string, len(md.MaterialKeys))
			out.Decals = make([]bool, len(md.MaterialKeys))
			out.Materials = make([]string, len(md.MaterialKeys))
			if out.Look == "" {
				return
			}
			lg, _ := parse(out.Look)
			look, err := a.loadLook(c, lg)
			if err != nil {
				return
			}
			for k, mat := range model.Resolve(md.MaterialKeys, look) {
				if mat == 0 {
					continue
				}
				out.Materials[k] = hex16(mat)
				if mt, err := a.loadMaterial(c, mat); err == nil {
					if t := mt.ColorTexture(a.srgbTexture(c)); t != 0 {
						out.Colors[k] = hex16(t)
					}
					if t := mt.Texture(model.SlotOpacity); t != 0 {
						out.Opacity[k] = hex16(t)
						out.Decals[k] = out.Colors[k] == ""
					}
					out.Helpers[k] = mt.Helper()
				}
			}
		}(&v.Batches[i])
	}
	wg.Wait()
	for _, b := range v.Batches {
		n := len(b.Records)
		v.Instances += n
		v.Triangles += n * b.Triangles
		if b.Error != "" {
			v.Failed++
		}
	}
	return v, nil
}

// MapVisualView is the answer of MapVisual.
type MapVisualView struct {
	Batches   []MapVisualBatch `json:"batches"`
	Instances int              `json:"instances"`
	Triangles int              `json:"triangles"` // over every instance
	Failed    int              `json:"failed"`
}

type mapviewBatch = mapview.VisualBatch

// modelPlacement is the model side of a map record that places a model (types 1, 2, 8): the model, its looks (a model
// group has a look per group) with instance counts, and each look's material slots. For placementBrief (selection
// card, Ask, MCP current_view / placement).
func (a *App) modelPlacement(mapGUID string, rec int) map[string]any {
	_, m, err := a.openMap(mapGUID)
	if err != nil {
		return nil
	}
	a.mmu.Lock()
	if rec < 0 || rec >= len(m.M.Records) {
		a.mmu.Unlock()
		return nil
	}
	r := m.M.Records[rec]
	a.mmu.Unlock()
	if r.Primary.Type() != model.TypeModel {
		return nil
	}
	type lk struct {
		guid  uint64
		count int
	}
	var looks []lk
	addLook := func(g uint64) {
		for i := range looks {
			if looks[i].guid == g {
				looks[i].count++
				return
			}
		}
		looks = append(looks, lk{g, 1})
	}
	instances := 1
	if r.Type == 1 {
		instances = len(r.Instances)
		for _, in := range r.Instances {
			addLook(uint64(in.Look))
		}
	} else {
		addLook(uint64(r.Secondary))
	}
	out := map[string]any{"model": hex16(uint64(r.Primary)), "instances": instances}
	var lv []map[string]any
	for i, l := range looks {
		e := map[string]any{"look": hex16(l.guid), "instances": l.count}
		if i < 4 && l.guid != 0 {
			if v, err := a.Model(hex16(uint64(r.Primary)), hex16(l.guid)); err == nil {
				var slots []map[string]any
				for k, s := range v.Slots {
					slots = append(slots, map[string]any{"slot": k, "material": s.Material, "color": s.Color, "submeshes": s.Submeshes, "helper": s.Helper})
				}
				e["slots"] = slots
				if len(v.LODs) > 0 {
					e["triangles"] = v.LODs[0].Triangles
				}
			}
		}
		lv = append(lv, e)
	}
	out["looks"] = lv
	return out
}

// srgbTexture reports whether a texture's format is an sRGB one (BC1 / BC3 / RGBA8 sRGB): the colour-texture fallback.
func (a *App) srgbTexture(c *catalog.Catalog) func(uint64) bool {
	return func(g uint64) bool {
		b, err := c.Store.Read(g)
		if err != nil {
			return false
		}
		h, err := texture.ParseHeader(b)
		if err != nil {
			return false
		}
		switch h.Format {
		case 29, 72, 78: // R8G8B8A8_UNORM_SRGB, BC1_UNORM_SRGB, BC3_UNORM_SRGB
			return true
		}
		return false
	}
}
