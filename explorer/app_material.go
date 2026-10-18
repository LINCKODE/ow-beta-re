package main

// Material preview (T071, docs/Explorer.md "Material view"): the resolved preview inputs of a material (in the
// Material tab and in describe), one image per input for the three.js preview (/mprev/<material>/<input>.png), a
// software render for the MCP tool material_image and the material thumbnails (/thumb/<material>.png).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"sync"

	"owcontent/content"
	"owexplorer/internal/catalog"
	"owexplorer/internal/mcp"
	"owexplorer/internal/model"
	"owexplorer/internal/texture"
)

var (
	mprevCache = newLRU[*image.NRGBA](128<<20, func(im *image.NRGBA) int { return len(im.Pix) })
	mprevPNG   = newLRU[[]byte](64<<20, func(b []byte) int { return len(b) })
	texInfos   sync.Map // "<describe>/<guid>" → texInfoEntry
)

type texInfoEntry struct {
	info model.TexInfo
	ok   bool
}

// texInfo reads a texture's header (format, sRGB, HDR, channels); ok = false when it is missing or unusable.
func (a *App) texInfo(c *catalog.Catalog) func(uint64) (model.TexInfo, bool) {
	return func(g uint64) (model.TexInfo, bool) {
		key := fmt.Sprintf("%s/%016x", c.Describe, g)
		if e, ok := texInfos.Load(key); ok {
			return e.(texInfoEntry).info, e.(texInfoEntry).ok
		}
		var e texInfoEntry
		if b, err := c.Store.Read(g); err == nil {
			if h, err := texture.ParseHeader(b); err == nil {
				if f := texture.LookupFormat(h.Format); f != nil {
					e = texInfoEntry{model.TexInfo{Format: f.Name, SRGB: f.SRGB, HDR: f.HDR, Channels: f.Channels}, true}
				}
			}
		}
		texInfos.Store(key, e)
		return e.info, e.ok
	}
}

// materialPreview loads a material and resolves its preview.
func (a *App) materialPreview(c *catalog.Catalog, g uint64) (*model.Material, model.Preview, error) {
	if content.TypeOf(g) != model.TypeMaterial {
		return nil, model.Preview{}, fmt.Errorf("%016x is not a material (0x008)", g)
	}
	m, err := a.loadMaterial(c, g)
	if err != nil {
		return nil, model.Preview{}, err
	}
	return m, m.Preview(a.texInfo(c)), nil
}

// MaterialPreviews resolves the previews of many materials at once (the Model tab's and the 3D map's PBR mode, T071).
// Materials that fail are left out.
func (a *App) MaterialPreviews(guids []string) (map[string]model.Preview, error) {
	c, err := a.catalog()
	if err != nil {
		return nil, err
	}
	out := make(map[string]model.Preview, len(guids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, s := range guids {
		g, err := parse(s)
		if err != nil {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			if _, p, err := a.materialPreview(c, g); err == nil {
				mu.Lock()
				out[hex16(g)] = p
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out, nil
}

// ormImage is the synthetic input "orm": R = AO (255 when the material has none on UV0), G = roughness, B = metalness,
// the layout three.js reads (aoMap .r, roughnessMap .g, metalnessMap .b): one texture instead of three for the 3D map.
func (a *App) ormImage(c *catalog.Catalog, guid string, p model.Preview, maxSize int, small bool) (*image.NRGBA, error) {
	get := func(in string) *image.NRGBA {
		if pi := p.Input(in); pi != nil && (in != model.InAO || pi.On) {
			if im, err := a.previewImage(c, guid, in, maxSize, small); err == nil {
				return im
			}
		}
		return nil
	}
	r, m, ao := get(model.InRoughness), get(model.InMetalness), get(model.InAO)
	base := r
	for _, im := range []*image.NRGBA{m, ao} {
		if base == nil {
			base = im
		}
	}
	if base == nil {
		return nil, fmt.Errorf("material %s has no roughness / metalness / AO input", guid)
	}
	w, h := base.Rect.Dx(), base.Rect.Dy()
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			o := (y*w + x) * 4
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = 255, 153, 0, 255 // AO 1, roughness 0.6, metal 0
			if ao != nil {
				out.Pix[o] = at(ao, x, y, w, h, 1, 1, 0, 0)[0]
			}
			if r != nil {
				out.Pix[o+1] = at(r, x, y, w, h, 1, 1, 0, 0)[0]
			}
			if m != nil {
				out.Pix[o+2] = at(m, x, y, w, h, 1, 1, 0, 0)[0]
			}
		}
	}
	return out, nil
}

// previewImage builds one input's image: the largest available mip of surface 0 no bigger than maxSize, converted for
// the preview. colour and emissive: RGB as stored (sRGB); normal: X, Y from R, G (from the rule's channels), Z rebuilt,
// encoded (v+1)/2; the others: the rule's channel as grey (inverted when the rule says so). Alpha is 255.
func (a *App) previewImage(c *catalog.Catalog, guid, input string, maxSize int, small bool) (*image.NRGBA, error) {
	if maxSize <= 0 {
		maxSize = 1024
	}
	key := fmt.Sprintf("%s/%s/%s/%d/%v", c.Describe, guid, input, maxSize, small)
	if im, ok := mprevCache.get(key); ok {
		return im, nil
	}
	g, err := parse(guid)
	if err != nil {
		return nil, err
	}
	_, p, err := a.materialPreview(c, g)
	if err != nil {
		return nil, err
	}
	if input == "orm" {
		im, err := a.ormImage(c, guid, p, maxSize, small)
		if err == nil {
			mprevCache.put(key, im)
		}
		return im, err
	}
	in := p.Input(input)
	if in == nil {
		return nil, fmt.Errorf("material %s has no %s input", guid, input)
	}
	load := func(tg uint64) (*texture.Texture, error) {
		if small { // thumbnails: payload 0 only, and not through texCache (it would push the viewed textures out)
			return texture.LoadSmall(a.texReader(c), tg)
		}
		return a.loadTexture(c, textureOf(tg))
	}
	t, err := load(in.Texture)
	if err != nil {
		return nil, err
	}
	px, w, h, err := decodeUpTo(t, maxSize)
	if err != nil {
		return nil, err
	}
	// LDR values as stored; HDR scaled to the 99.5th percentile and sRGB-encoded (the texture view's "auto")
	disp := t.Format.ToImage(px, w, h, texture.Display{Auto: true})
	var with []*image.NRGBA
	for _, wg := range in.With {
		wt, err := load(wg)
		if err != nil {
			return nil, err
		}
		wpx, ww, wh, err := decodeUpTo(wt, maxSize)
		if err != nil {
			return nil, err
		}
		with = append(with, wt.Format.ToImage(wpx, ww, wh, texture.Display{Auto: true}))
	}
	m, _, _ := a.materialPreview(c, g)
	im := texture.Downscale(convertInput(disp, px, t.Format, *in, with, m), maxSize) // textures without a small mip
	mprevCache.put(key, im)
	return im, nil
}

// decodeUpTo decodes surface 0 of the largest available mip no bigger than maxSize (else the smallest available).
func decodeUpTo(t *texture.Texture, maxSize int) ([]float32, int, int, error) {
	if t.Format == nil {
		return nil, 0, 0, fmt.Errorf("texture %016x: no decoder", t.GUID)
	}
	mip := -1
	for i := range t.Mips {
		if t.Available(i) {
			mip = i
			if max(t.Mips[i].Width, t.Mips[i].Height) <= maxSize {
				break
			}
		}
	}
	if mip < 0 {
		return nil, 0, 0, errors.New("no mip available")
	}
	px, err := t.Decode(mip, 0)
	return px, t.Mips[mip].Width, t.Mips[mip].Height, err
}

func sat(x float64) float64 { return math.Max(0, math.Min(1, x)) }

func lin(c uint8) float64 {
	v := float64(c) / 255
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func srgb8(v float64) uint8 {
	v = sat(v)
	if v <= 0.0031308 {
		v *= 12.92
	} else {
		v = 1.055*math.Pow(v, 1/2.4) - 0.055
	}
	return uint8(math.Round(v * 255))
}

// at samples an image at the texel centre (x, y) of a w × h image, scaled / offset in UV (wrap, nearest).
func at(im *image.NRGBA, x, y, w, h int, su, sv, ou, ov float64) []uint8 {
	b := im.Rect
	u := (float64(x)+0.5)/float64(w)*su + ou
	v := (float64(y)+0.5)/float64(h)*sv + ov
	u, v = u-math.Floor(u), v-math.Floor(v)
	xx, yy := min(int(u*float64(b.Dx())), b.Dx()-1), min(int(v*float64(b.Dy())), b.Dy()-1)
	o := im.PixOffset(b.Min.X+xx, b.Min.Y+yy)
	return im.Pix[o : o+4]
}

// convertInput turns a decoded texture into the preview's input image (the channel transforms of preview.go's rule).
func convertInput(disp *image.NRGBA, px []float32, f *texture.Format, in model.PreviewInput, with []*image.NRGBA, _ *model.Material) *image.NRGBA {
	w, h := disp.Rect.Dx(), disp.Rect.Dy()
	out := image.NewNRGBA(disp.Rect)
	ch := map[string]int{"r": 0, "g": 1, "b": 2, "a": 3}
	for y := range h {
		for x := range w {
			i := y*w + x
			o := i * 4
			d := disp.Pix[o : o+4]
			r, g, b := float64(d[0])/255, float64(d[1])/255, float64(d[2])/255
			var v uint8
			switch in.Channel {
			case "normal": // inverse stereographic, B flattens (T071 [D])
				s := 1 / (1 + 31.875*float64(px[o+2]))
				nx := (float64(px[o])-128.0/255)*s + 128.0/255
				ny := (float64(px[o+1])-128.0/255)*s + 128.0/255
				nx, ny = nx*2-1, ny*2-1
				dd := 1 + nx*nx + ny*ny
				out.Pix[o], out.Pix[o+1], out.Pix[o+2] = unit8(2*nx/dd), unit8(2*ny/dd), unit8(2/dd-1)
				out.Pix[o+3] = 255
				continue
			case "rgb":
				if f.Channels == 1 {
					out.Pix[o], out.Pix[o+1], out.Pix[o+2] = d[0], d[0], d[0]
				} else {
					out.Pix[o], out.Pix[o+1], out.Pix[o+2] = d[0], d[1], d[2]
				}
				out.Pix[o+3] = 255
				continue
			case "layers", "emissive", "emissivelin":
				var c [3]float64
				switch in.Channel {
				case "layers": // lerp(lerp(layer2, layer1, layer1.a), base, base.a)
					l1, l2 := at(with[0], x, y, w, h, 1, 1, 0, 0), at(with[1], x, y, w, h, 1, 1, 0, 0)
					a1, ab := float64(l1[3])/255, float64(d[3])/255
					for k := range 3 {
						c[k] = (lin(l2[k])*(1-a1)+lin(l1[k])*a1)*(1-ab) + lin(d[k])*ab
					}
				default: // emissive: mask (^2.2) × albedo
					mk := r
					if in.Channel == "emissive" {
						mk = math.Pow(r, 2.2)
					}
					c = [3]float64{mk, mk, mk}
					if len(with) > 0 {
						al := at(with[0], x, y, w, h, 1, 1, 0, 0)
						for k := range 3 {
							c[k] *= lin(al[k])
						}
					}
				}
				out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = srgb8(c[0]), srgb8(c[1]), srgb8(c[2]), 255
				continue
			case "gloss": // roughness = 0.015 + 0.985 (1 − gloss); gloss = (G + B) / 2 when B > 0
				gl := g
				if b > 0 {
					gl = sat((g + b) / 2)
				}
				v = uint8(math.Round((0.015 + 0.985*(1-gl)) * 255))
			case "metal":
				m := sat(2*r - 1)
				v = uint8(math.Round(m * m * 255))
			case "spec": // dielectric F0 × sat(4s), stored as F0 / 0.08
				f0 := (0.02 + 0.06*sat(r-0.25)) * sat(4*r)
				v = uint8(math.Round(sat(f0/0.08) * 255))
			default:
				v = d[ch[in.Channel]]
			}
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = v, v, v, 255
		}
	}
	return out
}

func unit8(v float64) uint8 { return uint8(math.Round(math.Max(0, math.Min(1, (v+1)/2)) * 255)) }

// previewPNG is /mprev/<material>/<input>.png?max=N.
func (a *App) previewPNG(c *catalog.Catalog, guid, input string, maxSize int) ([]byte, error) {
	key := fmt.Sprintf("%s/%s/%s/%d", c.Describe, guid, input, maxSize)
	if b, ok := mprevPNG.get(key); ok {
		return b, nil
	}
	im, err := a.previewImage(c, guid, input, maxSize, false)
	if err != nil {
		return nil, err
	}
	b, err := texture.PNG(im)
	if err != nil {
		return nil, err
	}
	mprevPNG.put(key, b)
	return b, nil
}

// MaterialRenderOpts are the options of a software render (material_image, thumbnails).
type MaterialRenderOpts struct {
	Size       int
	Geometry   string // "" = the family's default
	LightYaw   float64
	LightPitch float64
	FlipGreen  bool
	TexSize    int
	Small      bool
	Inputs     map[string]bool // inputs to leave out (false)
}

// renderMaterial draws the material with the software renderer.
func (a *App) renderMaterial(c *catalog.Catalog, g uint64, o MaterialRenderOpts) (*image.NRGBA, model.Preview, error) {
	_, p, err := a.materialPreview(c, g)
	if err != nil {
		return nil, p, err
	}
	var in model.MatInputs
	guid := fmt.Sprintf("%016x", g)
	for _, pi := range p.Inputs {
		if o.Inputs != nil {
			if on, ok := o.Inputs[pi.Input]; ok && !on {
				continue
			}
		} else if !pi.On { // on UV1+: meaningless on a primitive
			continue
		}
		im, err := a.previewImage(c, guid, pi.Input, o.TexSize, o.Small)
		if err != nil {
			continue // a missing input renders without it
		}
		switch pi.Input {
		case model.InColor:
			in.Color = im
		case model.InOpacity:
			in.Opacity = im
		case model.InNormal:
			in.Normal = im
		case model.InRoughness:
			in.Roughness = im
		case model.InSpecular:
			in.Specular = im
		case model.InMetalness:
			in.Metalness = im
		case model.InAO:
			in.AO = im
		case model.InEmissive:
			in.Emissive = im
			in.EmissiveIntensity = 1
			if pi.Intensity != nil {
				in.EmissiveIntensity = *pi.Intensity
			}
		}
	}
	geom := o.Geometry
	if geom == "" {
		geom = p.Family.Geometry
	}
	vw := model.MatView{Size: o.Size, Geometry: geom, Mode: p.Family.Mode, UVScale: p.UVScale, LightYaw: o.LightYaw,
		LightPitch: o.LightPitch, FlipGreen: o.FlipGreen, Background: [3]uint8{16, 21, 31}}
	return model.RenderMaterial(in, vw), p, nil
}

// materialThumbnail is /thumb/<material>.png: a 64 px sphere (rendered at 128 and box-filtered), cached with the
// texture thumbnails, from the textures' small mips.
func (a *App) materialThumbnail(c *catalog.Catalog, g uint64) ([]byte, error) {
	key := fmt.Sprintf("%s/%016x", c.Describe, g)
	if b, ok := thumbCache.get(key); ok {
		return b, nil
	}
	thumbSem <- struct{}{}
	defer func() { <-thumbSem }()
	im, _, err := a.renderMaterial(c, g, MaterialRenderOpts{Size: 128, LightYaw: 40, LightPitch: 35, TexSize: 128, Small: true})
	if err != nil {
		return nil, err
	}
	b, err := texture.PNG(texture.Downscale(im, 64))
	if err != nil {
		return nil, err
	}
	thumbCache.put(key, b)
	return b, nil
}

// --- MCP --------------------------------------------------------------------------------------------------------

// materialPreviewSummary is describe's "preview" section of a material.
func (a *App) materialPreviewSummary(c *catalog.Catalog, g uint64) any {
	_, p, err := a.materialPreview(c, g)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return p
}

type materialImageArgs struct {
	GUID       string   `json:"guid"`
	Geometry   string   `json:"geometry,omitempty"`
	Size       int      `json:"size,omitempty"`
	LightYaw   *float64 `json:"light_yaw,omitempty"`
	LightPitch *float64 `json:"light_pitch,omitempty"`
	FlipGreen  bool     `json:"flip_green,omitempty"`
	Inputs     []string `json:"inputs,omitempty"`
	Save       *bool    `json:"save,omitempty"`
}

func materialImageTool(a *App) mcp.Tool {
	return tool("material_image", "A material (0x008) previewed on a sphere, plane or cube by a software renderer (GGX "+
		"+ hemisphere ambient, the Material tab's resolved inputs: colour, opacity, normal, roughness, specular, AO, "+
		"emissive) and returned as an image, also saved to a file (path in the text part, with the resolved inputs). "+
		"Default geometry: the shader family's (decals and effects on a plane).",
		obj(map[string]any{"guid": pGUID,
			"geometry":    pStr("sphere, plane or cube (default: the family's)"),
			"size":        pInt("image side in pixels, default 384, max 1024"),
			"light_yaw":   map[string]any{"type": "number", "description": "light direction, degrees around Y from the camera side (default 40)"},
			"light_pitch": map[string]any{"type": "number", "description": "light elevation in degrees (default 35)"},
			"flip_green":  map[string]any{"type": "boolean", "description": "read the normal map's green channel as up (OpenGL convention)"},
			"inputs": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
				"description": "only these inputs (color, opacity, normal, roughness, specular, metalness, ao, emissive); default all"},
			"save": map[string]any{"type": "boolean", "description": "write the PNG file (default true)"}}, "guid"),
		typed(a.toolMaterialImage))
}

func (a *App) toolMaterialImage(ctx context.Context, q materialImageArgs) (any, error) {
	c, g, err := a.guidArg(q.GUID)
	if err != nil {
		return nil, err
	}
	if content.TypeOf(g) != model.TypeMaterial {
		return nil, fmt.Errorf("%016x is not a material (0x008)", g)
	}
	switch q.Geometry {
	case "", "sphere", "plane", "cube":
	default:
		return nil, fmt.Errorf("geometry %q: sphere, plane or cube", q.Geometry)
	}
	o := MaterialRenderOpts{Size: q.Size, Geometry: q.Geometry, LightYaw: 40, LightPitch: 35, FlipGreen: q.FlipGreen, TexSize: 1024}
	if o.Size <= 0 {
		o.Size = 384
	}
	o.Size = min(o.Size, 1024)
	if q.LightYaw != nil {
		o.LightYaw = *q.LightYaw
	}
	if q.LightPitch != nil {
		o.LightPitch = *q.LightPitch
	}
	if len(q.Inputs) > 0 {
		o.Inputs = map[string]bool{}
		for _, in := range model.Inputs {
			o.Inputs[in] = false
		}
		for _, in := range q.Inputs {
			if _, ok := o.Inputs[in]; !ok {
				return nil, fmt.Errorf("unknown input %q", in)
			}
			o.Inputs[in] = true
		}
	}
	im, p, err := a.renderMaterial(c, g, o)
	if err != nil {
		return nil, err
	}
	pngb, err := texture.PNG(im)
	if err != nil {
		return nil, err
	}
	geom := q.Geometry
	if geom == "" {
		geom = p.Family.Geometry
	}
	info := map[string]any{"material": fmt.Sprintf("%016x", g), "geometry": geom, "image": fmt.Sprintf("%d×%d", o.Size, o.Size),
		"preview": p}
	if q.Save == nil || *q.Save {
		dir := os.Getenv("XDG_RUNTIME_DIR")
		if dir == "" {
			dir = os.TempDir()
		}
		dir = filepath.Join(dir, "owexplorer")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		path := filepath.Join(dir, fmt.Sprintf("%016x-material-%s.png", g, geom))
		if err := os.WriteFile(path, pngb, 0o644); err != nil {
			return nil, err
		}
		info["path"] = path
	}
	text, _ := json.Marshal(info)
	return mcp.Content{mcp.Image(pngb, "image/png"), mcp.Text(string(text))}, nil
}
