package main

// MCP for models and materials (T064, docs/Explorer.md "MCP server"): describe's summaries, the Model tab's selection
// in current_view, and the tool model_image (a software render of a model with its look's colour textures).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"

	"owcontent/content"
	"owexplorer/internal/catalog"
	"owexplorer/internal/mcp"
	"owexplorer/internal/model"
	"owexplorer/internal/texture"
)

// modelSummary is describe's summary of a model (or of a look: its model) and of a material.
func (a *App) modelSummary(c *catalog.Catalog, g uint64) (any, error) {
	if content.TypeOf(g) == model.TypeMaterial {
		v, err := a.Material(hexg(g))
		if err != nil {
			return nil, err
		}
		tex := []map[string]any{}
		for _, t := range v.Textures {
			tex = append(tex, map[string]any{"texture": t.GUID, "slot": t.Slot, "slotName": t.SlotName, "format": t.Format,
				"size": fmt.Sprintf("%d×%d", t.Width, t.Height)})
		}
		return map[string]any{"shader": v.Shader, "ref08": v.Ref, "textures": tex, "constantBuffers": len(v.Buffers),
			"preview": v.Preview}, nil
	}
	v, err := a.Model(hexg(g), "")
	if err != nil {
		return nil, err
	}
	slots := []map[string]any{}
	for i, s := range v.Slots {
		slots = append(slots, map[string]any{"slot": i, "key": s.Key, "material": s.Material, "colorTexture": s.Color,
			"submeshes": s.Submeshes})
	}
	looks := v.Looks
	if len(looks) > 12 {
		looks = looks[:12]
	}
	out := map[string]any{"model": v.GUID, "size": v.Size, "lods": v.LODs, "submeshes": len(v.Submeshes),
		"vertexBuffers": len(v.VBs), "look": v.Look, "looks": looks, "materialSlots": slots, "loaderRefs": v.Refs}
	if v.Skeleton != nil {
		out["bones"] = len(v.Skeleton.Bones)
	}
	return out, nil
}

// modelSelection adds the Model tab's state to current_view's selection.
func (a *App) modelSelection(e map[string]any, sel map[string]any) {
	for _, k := range []string{"look", "lod", "submesh", "shading", "drawn", "camera"} {
		if v, ok := e[k]; ok {
			sel[k] = v
		}
	}
	mg, _ := e["model"].(string)
	sm, ok := e["submesh"].(float64)
	if !ok || sm < 0 || mg == "" {
		return
	}
	look, _ := e["look"].(string)
	v, err := a.Model(mg, look)
	if err != nil || int(sm) >= len(v.Submeshes) {
		return
	}
	s := v.Submeshes[int(sm)]
	d := map[string]any{"index": int(sm), "triangles": s.IndexCount / 3, "vb": s.VB, "ib": s.IB, "lodMask": s.LODMask,
		"materialSlot": s.Material}
	if int(s.Material) < len(v.Slots) {
		d["material"] = v.Slots[s.Material].Material
		d["colorTexture"] = v.Slots[s.Material].Color
	}
	sel["submeshDetail"] = d
}

type modelImageArgs struct {
	GUID    string   `json:"guid"`
	Look    string   `json:"look"`
	LOD     int      `json:"lod"`
	Yaw     *float64 `json:"yaw"`
	Pitch   *float64 `json:"pitch"`
	Size    int      `json:"size"`
	Submesh *int     `json:"submesh"`
	Mirror  bool     `json:"mirror"`
	PBR     bool     `json:"pbr"`
	Save    *bool    `json:"save"`
}

func modelImageTool(a *App) mcp.Tool {
	return tool("model_image", "A model (0x00C; a look 0x01A shows its model) rendered in the explorer (software "+
		"rasteriser: z-buffer, Lambert, the look's colour textures, nearest sampling) and returned as an image, also saved "+
		"to a file (path in the text part). Default: the finest LOD, the look the Model tab would pick, a 3/4 view.",
		obj(map[string]any{"guid": pGUID,
			"look":    pStr("look GUID (default: the best look placed with the model)"),
			"lod":     pInt("LOD mask 1, 2, 4, 8, or 255 for every submesh (default: the finest)"),
			"yaw":     map[string]any{"type": "number", "description": "degrees around Y; 0 = from +Z (default 35)"},
			"pitch":   map[string]any{"type": "number", "description": "degrees, > 0 from above (default 20)"},
			"size":    pInt("image side in pixels, default 512, max 1024"),
			"submesh": pInt("submesh drawn highlighted"),
			"mirror":  map[string]any{"type": "boolean", "description": "mirror X"},
			"pbr": map[string]any{"type": "boolean", "description": "shade with each slot material's PBR inputs (the Material " +
				"tab's mapping: colour, roughness, metalness, specular, AO on UV0, emissive, alpha test; no normal maps) and a " +
				"studio sun + ambient, instead of the colour texture with Lambert"},
			"save": map[string]any{"type": "boolean", "description": "write the PNG file (default true)"}}, "guid"),
		typed(a.toolModelImage))
}

func (a *App) toolModelImage(ctx context.Context, q modelImageArgs) (any, error) {
	c, g, err := a.guidArg(q.GUID)
	if err != nil {
		return nil, err
	}
	if t := content.TypeOf(g); t != model.TypeModel && t != model.TypeLook {
		return nil, fmt.Errorf("%016x is not a model (0x00C) or a look (0x01A)", g)
	}
	v, err := a.Model(hexg(g), q.Look)
	if err != nil {
		return nil, err
	}
	mg, _ := parse(v.GUID)
	m, err := a.loadModel(c, mg)
	if err != nil {
		return nil, err
	}
	me, err := m.Mesh(uint8(q.LOD))
	if err != nil {
		return nil, err
	}
	size := q.Size
	if size <= 0 {
		size = 512
	}
	size = min(size, 1024)
	vw := model.View{Yaw: 35, Pitch: 20, Mirror: q.Mirror, Highlight: -1}
	if q.Yaw != nil {
		vw.Yaw = *q.Yaw
	}
	if q.Pitch != nil {
		vw.Pitch = *q.Pitch
	}
	if q.Submesh != nil {
		vw.Highlight = *q.Submesh
	}
	// the colour texture of each group's slot, decoded once
	imgs := map[string]*image.NRGBA{}
	groupImg := make([]*image.NRGBA, len(me.Groups))
	skip := make([]bool, len(me.Groups))
	textured := 0
	for i, gr := range me.Groups {
		if gr.Material < len(v.Slots) && v.Slots[gr.Material].Decal {
			skip[i] = true // a normal-only decal: not drawn
			continue
		}
		if gr.Material >= len(v.Slots) || v.Slots[gr.Material].Color == "" {
			continue
		}
		tg := v.Slots[gr.Material].Color
		im, ok := imgs[tg]
		if !ok {
			if b, err := a.materialTexture(c, tg, 512); err == nil {
				if d, err := png.Decode(bytes.NewReader(b)); err == nil {
					im = toNRGBA(d)
				}
			}
			imgs[tg] = im
		}
		if im != nil {
			groupImg[i] = im
			textured++
		}
	}
	vw.Skip = skip
	if q.PBR {
		vw.Shade = a.pbrShader(c, v, me, vw.Yaw+40, 35)
	}
	img := model.Render(me, size, size, vw, func(gi int, u, w float32) (color.NRGBA, bool) {
		if im := groupImg[gi]; im != nil {
			return model.SampleImage(im, u, w), true
		}
		return color.NRGBA{}, false
	})
	pngb, err := texture.PNG(img)
	if err != nil {
		return nil, err
	}
	lodMask := q.LOD
	if lodMask == 0 && len(v.LODs) > 0 {
		lodMask = v.LODs[0].Mask
	}
	info := map[string]any{"model": v.GUID, "look": v.Look, "pbr": q.PBR, "lod": lodMask, "triangles": len(me.Indices) / 3,
		"groups": len(me.Groups), "texturedGroups": textured, "yaw": vw.Yaw, "pitch": vw.Pitch, "image": fmt.Sprintf("%d×%d", size, size),
		"bounds": map[string]any{"min": me.Min, "max": me.Max}}
	if q.Save == nil || *q.Save {
		dir := os.Getenv("XDG_RUNTIME_DIR")
		if dir == "" {
			dir = os.TempDir()
		}
		dir = filepath.Join(dir, "owexplorer")
		name := fmt.Sprintf("%s-model-l%d-y%.0f-p%.0f.png", v.GUID, lodMask, vw.Yaw, vw.Pitch)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, pngb, 0o644); err != nil {
			return nil, err
		}
		info["path"] = path
	}
	text, _ := json.Marshal(info)
	return mcp.Content{mcp.Image(pngb, "image/png"), mcp.Text(string(text))}, nil
}

// pbrShader is model_image's PBR mode: per group, the slot material's preview inputs (app_material.go previewImage,
// 512 px) shaded by model.ShadePBR. Normal maps are left out (the mesh has no tangents here).
func (a *App) pbrShader(c *catalog.Catalog, v ModelView, me *model.Mesh, lightYaw, lightPitch float64) func(int, float32, float32, [3]float64, [3]float64) (color.NRGBA, int) {
	type grp struct {
		mode                                            string
		uv                                              [4]float32 // scale, offset
		color, opacity, rough, spec, metal, ao, emissiv *image.NRGBA
		intensity                                       float64
	}
	cache := map[string]*grp{}
	groups := make([]*grp, len(me.Groups))
	for i, gr := range me.Groups {
		if gr.Material >= len(v.Slots) || v.Slots[gr.Material].Material == "" {
			continue
		}
		mg := v.Slots[gr.Material].Material
		if g, ok := cache[mg]; ok {
			groups[i] = g
			continue
		}
		g := &grp{}
		cache[mg] = g
		groups[i] = g
		mgu, _ := parse(mg)
		_, p, err := a.materialPreview(c, mgu)
		if err != nil {
			continue
		}
		g.mode = p.Family.Mode
		g.uv = [4]float32{p.UVScale[0], p.UVScale[1], p.UVOffset[0], p.UVOffset[1]}
		for _, in := range p.Inputs {
			if !in.On {
				continue
			}
			im, err := a.previewImage(c, mg, in.Input, 512, false)
			if err != nil {
				continue
			}
			switch in.Input {
			case model.InColor:
				g.color = im
			case model.InOpacity:
				g.opacity = im
			case model.InRoughness:
				g.rough = im
			case model.InSpecular:
				g.spec = im
			case model.InMetalness:
				g.metal = im
			case model.InAO:
				g.ao = im
			case model.InEmissive:
				g.emissiv = im
				g.intensity = 1
				if in.Intensity != nil {
					g.intensity = float64(*in.Intensity)
				}
			}
		}
	}
	grey := func(im *image.NRGBA, u, w float32, def float64) float64 {
		if im == nil {
			return def
		}
		return float64(model.SampleImage(im, u, w).R) / 255
	}
	return func(gi int, u, w float32, n, view [3]float64) (color.NRGBA, int) {
		g := groups[gi]
		if g == nil || g.mode == "" {
			return color.NRGBA{}, 0
		}
		u, w = u*g.uv[0]+g.uv[2], w*g.uv[1]+g.uv[3]
		if g.mode == "effect" { // additive effects: not drawn on a model render
			return color.NRGBA{}, 2
		}
		if g.opacity != nil && grey(g.opacity, u, w, 1) < 0.5 {
			return color.NRGBA{}, 2
		}
		alb := [3]float64{0.5, 0.5, 0.5}
		if g.color != nil {
			c := model.SampleImage(g.color, u, w)
			alb = [3]float64{float64(c.R) / 255, float64(c.G) / 255, float64(c.B) / 255}
		}
		if g.mode == "unlit" || g.mode == "sky" {
			return color.NRGBA{uint8(alb[0] * 255), uint8(alb[1] * 255), uint8(alb[2] * 255), 255}, 1
		}
		var em [3]float64
		if g.emissiv != nil {
			c := model.SampleImage(g.emissiv, u, w)
			for k, x := range [3]uint8{c.R, c.G, c.B} {
				em[k] = lin(x) * g.intensity
			}
		}
		o := model.ShadePBR(alb, grey(g.rough, u, w, 0.6), grey(g.spec, u, w, 0.5), grey(g.metal, u, w, 0), grey(g.ao, u, w, 1),
			em, n, view, lightYaw, lightPitch)
		return color.NRGBA{o[0], o[1], o[2], 255}, 1
	}
}

func toNRGBA(im image.Image) *image.NRGBA {
	if n, ok := im.(*image.NRGBA); ok {
		return n
	}
	b := im.Bounds()
	n := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			n.Set(x, y, im.At(x, y))
		}
	}
	return n
}
