package model

import (
	"fmt"
	"hash/crc32"
	"slices"
)

// Material preview (T071, docs/Explorer.md "Material view"): which of a material's texture bindings feed which input
// of a simple PBR preview (colour, opacity, normal, roughness, specular, metalness, AO, emissive), and how the
// material's shader family is shown (lit / decal / effect / unlit, on a sphere or a plane).
//
// Every rule carries its confidence: "D" = from the data (slot names recovered by CRC32, the shader bytecode, formats
// bound across the install), "?" = inferred. The table below is the reference for docs/Explorer.md.

// Family describes a material shader source (0x0E1…XX, Rendering.md §9.5).
type Family struct {
	ID       uint16 `json:"id"`
	Name     string `json:"name"`
	Mode     string `json:"mode"`     // lit, decal, effect, unlit, helper, glass, alphatest, sky
	Geometry string `json:"geometry"` // sphere, plane, cube: the default preview shape
	Conf     string `json:"conf"`     // confidence of the purpose: D or ?
	Note     string `json:"note,omitempty"`
}

var families = map[uint16]Family{
	0x21: {Name: "standard opaque PBR (world, props)", Mode: "lit", Geometry: "sphere", Conf: "D"},
	0x2C: {Name: "hero / character PBR", Mode: "lit", Geometry: "sphere", Conf: "D",
		Note: "2,078 of 2,398 ship no G-buffer permutation in their own group (T071)"},
	0x2B: {Name: "skin (subsurface colour 0xB317E4FD, basethickness)", Mode: "lit", Geometry: "sphere", Conf: "D"},
	0x33: {Name: "hair (forward; flowmap, two specular lobes)", Mode: "alphatest", Geometry: "sphere", Conf: "D"},
	0x32: {Name: "entity / weapon material (colour 0xE870032E, gradient LUT)", Mode: "lit", Geometry: "sphere", Conf: "?"},
	0x22: {Name: "decal", Mode: "decal", Geometry: "plane", Conf: "D"},
	0x28: {Name: "effect / particle", Mode: "effect", Geometry: "plane", Conf: "D",
		Note: "texture_N .r is an alpha mask; the colour is a constant in the material's constant buffer (not resolved: white here)"},
	0x2A: {Name: "overlay / screen effect look", Mode: "effect", Geometry: "plane", Conf: "?"},
	0x29: {Name: "effect variant (no textures)", Mode: "effect", Geometry: "plane", Conf: "?"},
	0x30: {Name: "effect variant", Mode: "effect", Geometry: "plane", Conf: "?"},
	0x26: {Name: "layered material (diffusemap, normalmap × normalstrengthmap, emissivemap, tintmap)", Mode: "lit", Geometry: "sphere", Conf: "?"},
	0x24: {Name: "world: basecolor × baseoverlay × 2 (detail), detail normal", Mode: "lit", Geometry: "plane", Conf: "D"},
	0x25: {Name: "world: two layers height-blended by vertex colour (layer A shown)", Mode: "lit", Geometry: "plane", Conf: "D"},
	0x36: {Name: "glass / water / refractive", Mode: "glass", Geometry: "sphere", Conf: "?"},
	0x37: {Name: "layered world material (layer1 / layer2 colour, parallax)", Mode: "lit", Geometry: "sphere", Conf: "D"},
	0x2F: {Name: "forward-lit world material (probe cube maps)", Mode: "lit", Geometry: "sphere", Conf: "D"},
	0x27: {Name: "masked PBR (basemask alpha test)", Mode: "alphatest", Geometry: "sphere", Conf: "D"},
	0x34: {Name: "cloth-type PBR (roughness 0.9–1)", Mode: "lit", Geometry: "sphere", Conf: "?"},
	0x35: {Name: "alpha-tested foliage (transmission colour)", Mode: "alphatest", Geometry: "plane", Conf: "D"},
	0x2E: {Name: "sky / clouds (cloudcolor, starfield)", Mode: "sky", Geometry: "sphere", Conf: "?"},
	0x23: {Name: "helper (colorsource)", Mode: "unlit", Geometry: "cube", Conf: "D"},
	0x38: {Name: "facing-ratio glow: emissivecolor × pow(N·V)", Mode: "effect", Geometry: "sphere", Conf: "D"},
	0x65: {Name: "water (foamcolor, flowmap)", Mode: "lit", Geometry: "plane", Conf: "?"},
	0x2D: {Name: "special (one RGBA8 basecolor)", Mode: "unlit", Geometry: "sphere", Conf: "?"},
}

// FamilyOf returns the family of a shader GUID (a generic "unknown" family for sources outside the table).
func FamilyOf(shader uint64) Family {
	id := uint16(shader & 0xfff)
	f, ok := families[id]
	if !ok {
		f = Family{Name: "unknown shader", Mode: "lit", Geometry: "sphere", Conf: "?", Note: "colour only"}
	}
	f.ID = id
	return f
}

// Slot names recovered by CRC32 of the lower-cased name (T071: dictionary search; the short dictionary runs have an
// expected false-hit count far below one). Names in slotNames (material.go) come first.
var recoveredSlots = []string{
	"baseao", "baseemissive", "basemask", "basenormal", "baseoverlay", "baseshadow", "basethickness", "bakedshadow",
	"basenormaloverlay", "baseroughnessspecular", "roughnessspecular", "roughness", "emissive", "emissivecolor",
	"emissivemap", "diffusecolor", "diffusemap", "diffuseprobe", "specularprobe", "colorsource", "tintmap",
	"layer1color", "layer2color", "secnormal", "secheightmap", "overlay", "overlayao", "overlaynormal", "flowmap",
	"normalstrengthmap", "cloudcolor", "cloudthickness", "foamcolor", "starmask",
}

func init() {
	for _, n := range recoveredSlots {
		slotNames[crc32.ChecksumIEEE([]byte(n))] = n
	}
	for i := range 64 { // effect materials (0x28, 0x2A, 0x30) bind texture_0 … texture_63
		n := fmt.Sprintf("texture_%d", i)
		slotNames[crc32.ChecksumIEEE([]byte(n))] = n
	}
	// named by their use in the shader bytecode (T071), not by CRC
	for h, n := range map[uint32]string{
		0x20af06ce: "spec/gloss (packed)", 0x312060c8: "spec/gloss B (packed)", 0x2d82d9d6: "spec/gloss (packed)",
		0xb96fb351: "spec/gloss (packed)", 0x7614968d: "spec/gloss (packed)", 0x49e57f4e: "spec/gloss (packed)",
		0x2ce679ff: "gradient LUT", 0x12bbc7d3: "lifetime curve (VS)", 0xb317e4fd: "subsurface colour",
		0xb067e858: "colour B", 0xe870032e: "colour", 0xef1dc737: "normal", 0xb85ea8cf: "colour",
		0x4296f04a: "hair shift", 0x067e6202: "AO / emissive factor",
	} {
		slotNames[h] = n + " ~"
	}
}

func slot(n string) uint32 { return crc32.ChecksumIEEE([]byte(n)) }

// Preview inputs.
const (
	InColor     = "color"
	InOpacity   = "opacity"
	InNormal    = "normal"
	InRoughness = "roughness"
	InSpecular  = "specular"
	InMetalness = "metalness"
	InAO        = "ao"
	InEmissive  = "emissive"
	// layer inputs (T071 round 4), blended in the viewer's shader: 0x25's layer B colour and blend height map, 0x24's
	// overlay (albedo × overlay × 2 on its own UV set)
	InColorB  = "colorB"
	InHeight  = "height"
	InOverlay = "overlay"
)

// Inputs lists the preview inputs in display order.
var Inputs = []string{InColor, InOpacity, InNormal, InRoughness, InSpecular, InMetalness, InAO, InEmissive, InColorB, InHeight, InOverlay}

// rule: an input read from the first bound slot of a list through a channel transform:
//
//	rgb       colour as stored (sRGB)
//	r g b a   one channel as grey
//	normal    inverse stereographic decode of R, G with B as a flattening factor (every family, T071 [D])
//	gloss     G (or (G+B)/2 when B > 0) = gloss → roughness 0.015 + 0.985 (1 − gloss)
//	metal     R = s → sat(2s − 1)²
//	spec      R = s → dielectric F0 (0.02 + 0.06 sat(s − 0.25)) · sat(4s), stored as F0 / 0.08
//	emissive  R^2.2 × the colour texture (emissive = intensity × mask × albedo); emissivelin without the pow
//	layers    lerp(lerp(layer2, layer1, layer1.a), base, base.a) (0x37). 0x24's overlay and 0x25's layer B are blended
//	          in the viewer's shader (utils/owmat.ts patchLayers): they need the mesh's UV1 / vertex colours
type rule struct {
	input     string
	slots     []uint32
	channel   string
	conf      string
	note      string
	only      []uint16 // families the rule is limited to (nil = all)
	uvSet     int      // 0 = UV0; 1 = UV1 (the preview's primitives have one UV set: off by default)
	uvParam   uint32   // an int parameter choosing the UV set (1 = UV0, 2 = UV1, 3 = UV2)
	intensity uint32   // a float parameter scaling the input
}

var (
	sPacked  = []uint32{0x20af06ce, slot("baseroughnessspecular"), slot("roughnessspecular"), 0x2d82d9d6, 0xb96fb351, 0x7614968d, 0x49e57f4e}
	sTexture = func() (l []uint32) { // texture_0 … texture_63 (a function: previewRules is built before init runs)
		for i := range 64 {
			l = append(l, slot(fmt.Sprintf("texture_%d", i)))
		}
		return l
	}()
)

// Parameter hashes (T071, bytecode [D]).
const (
	ParamUVScale      = uint32(0x704029ea) // float2: uv0 × scale + offset (0x21 family)
	ParamUVOffset     = uint32(0x811d50fa) // float2
	ParamUVScroll     = uint32(0x3bc81c35) // float2 × time
	ParamEmissive21   = uint32(0x91d6c98f) // float: emissive intensity (0x21, 0x2F)
	ParamEmissive2C   = uint32(0xc1be91cc) // float: emissive intensity (0x2C, 0x22, 0x37, 0x38)
	ParamAOUV2C       = uint32(0x8ca81224) // int: UV set of basecolor.a AO (0x2C)
	ParamEmissiveUV2C = uint32(0xc7526e90) // int: UV set of baseemissive (0x2C)
	ParamOverlayAOUV  = uint32(0xe98cd32e) // int: UV set of overlayao (0x25)
	ParamOverlayScale = uint32(0x656f1d9a) // float2: baseoverlay UV scale (0x24)
	ParamOverlayOfs   = uint32(0x9432648a) // float2: baseoverlay UV offset (0x24)
	ParamOverlayUV    = uint32(0x859fbb9d) // int: 1 = baseoverlay on uv0 (else uv 0) (0x24)
	ParamDecalColor   = uint32(0x21b43c8e) // float: decal colour multiplier; 2 = multiply decal (0x22)
)

var previewRules = []rule{
	// colour
	{input: InColor, slots: []uint32{slot("basecolor")}, channel: "layers", conf: "D", only: []uint16{0x37},
		note: "lerp(lerp(layer2color, layer1color, layer1color.a), basecolor, basecolor.a) (parallax offsets ignored)"},
	{input: InColor, slots: []uint32{slot("basecolor")}, channel: "rgb", conf: "D", only: []uint16{0x24},
		note: "× baseoverlay × 2 in the viewer's shader (the overlay has its own UV set and transform)"},
	{input: InOverlay, slots: []uint32{slot("baseoverlay")}, channel: "rgb", conf: "D", only: []uint16{0x24},
		note: "UV = (859FBB9D: 1 uv0, 2 uv1, else 0) × 656F1D9A + 9432648A"},
	{input: InColorB, slots: []uint32{0xb067e858}, channel: "rgb", conf: "D", only: []uint16{0x25},
		note: "layer B colour × D44A3918, on uv0 × C251F501 + 811D50FA"},
	{input: InHeight, slots: []uint32{slot("secheightmap")}, channel: "r", conf: "D", only: []uint16{0x25},
		note: "blend = sat((h − (1 − vertex.a)) / clamp(vertex.g, 0.01, 1) + vertex.a)"},
	{input: InColor, slots: []uint32{0xe870032e}, channel: "rgb", conf: "D", only: []uint16{0x32}, note: "× the gradient LUT in game"},
	{input: InColor, slots: []uint32{0xb85ea8cf}, channel: "rgb", conf: "D", only: []uint16{0x35}},
	{input: InColor, slots: []uint32{slot("emissivecolor")}, channel: "rgb", conf: "D", only: []uint16{0x38},
		note: "× pow(N·V, 1EE1B719) × C1BE91CC in game (a facing-ratio glow)"},
	{input: InColor, slots: []uint32{slot("basecolor")}, channel: "rgb", conf: "D", only: []uint16{0x25},
		note: "layer A; the viewer blends layer B (colorB) in by vertex colour and the height map (its spec/gloss 0x312060C8 and secnormal are not blended)"},
	{input: InColor, slots: []uint32{slot("basecolor"), slot("color"), slot("diffusecolor")}, channel: "rgb", conf: "D"},
	{input: InColor, slots: []uint32{slot("diffusemap"), slot("colorsource"), slot("cloudcolor"), slot("foamcolor")},
		channel: "rgb", conf: "?", note: "slot name recovered, use not read in the bytecode"},
	{input: InColor, slots: []uint32{slot("texture_0"), slot("texture_1")}, channel: "rgb", conf: "?", only: []uint16{0x2A},
		note: "BC3 sRGB on most 0x2A materials"},
	// opacity
	{input: InOpacity, slots: []uint32{slot("opacity")}, channel: "r", conf: "D"},
	{input: InOpacity, slots: []uint32{slot("basemask")}, channel: "r", conf: "D", only: []uint16{0x27}, note: "alpha test at 0.5"},
	{input: InOpacity, slots: []uint32{slot("diffusecolor")}, channel: "a", conf: "D", only: []uint16{0x33}, note: "coverage (discard at 0)"},
	{input: InOpacity, slots: sTexture, channel: "r", conf: "D", only: []uint16{0x28}, note: "alpha mask × the lifetime curve"},
	// normal
	{input: InNormal, slots: []uint32{slot("basenormal"), slot("normal"), slot("normalmap"), 0xef1dc737}, channel: "normal", conf: "D"},
	// packed spec / gloss
	{input: InRoughness, slots: sPacked, channel: "gloss", conf: "D", note: "G (B) = gloss"},
	{input: InMetalness, slots: sPacked, channel: "metal", conf: "D", note: "R = s: metal = sat(2s − 1)²"},
	{input: InSpecular, slots: sPacked, channel: "spec", conf: "D", note: "R = s: F0 = 0.02 + 0.06 sat(s − 0.25), × sat(4s)"},
	{input: InRoughness, slots: []uint32{slot("roughness")}, channel: "r", conf: "?"},
	// AO
	{input: InAO, slots: []uint32{slot("basecolor")}, channel: "a", conf: "D", only: []uint16{0x2C, 0x2B}, uvParam: ParamAOUV2C,
		note: "basecolor.a is AO on hero materials"},
	{input: InAO, slots: []uint32{slot("baseao")}, channel: "r", conf: "D", only: []uint16{0x21, 0x2F}, uvSet: 1},
	{input: InAO, slots: []uint32{slot("ao")}, channel: "r", conf: "D", only: []uint16{0x35}, uvSet: 1},
	{input: InAO, slots: []uint32{slot("overlayao")}, channel: "r", conf: "D", only: []uint16{0x25}, uvParam: ParamOverlayAOUV},
	{input: InAO, slots: []uint32{slot("baseao"), slot("ao")}, channel: "r", conf: "D",
		only: []uint16{0x27, 0x37, 0x22, 0x33}},
	{input: InAO, slots: []uint32{slot("baseao"), slot("ao")}, channel: "r", conf: "?"},
	// emissive
	{input: InEmissive, slots: []uint32{slot("baseemissive")}, channel: "emissive", conf: "D", only: []uint16{0x21, 0x2F},
		intensity: ParamEmissive21},
	{input: InEmissive, slots: []uint32{slot("baseemissive")}, channel: "emissive", conf: "D", only: []uint16{0x2C},
		intensity: ParamEmissive2C, uvParam: ParamEmissiveUV2C},
	{input: InEmissive, slots: []uint32{slot("emissive")}, channel: "emissivelin", conf: "D", only: []uint16{0x37},
		intensity: ParamEmissive2C},
	{input: InEmissive, slots: []uint32{slot("emissivecolor")}, channel: "emissive", conf: "D", only: []uint16{0x22},
		intensity: ParamEmissive2C, note: "decal: emissivecolor .r only (a mask)"},
	{input: InEmissive, slots: []uint32{slot("baseemissive"), slot("emissive"), slot("emissivemap")}, channel: "emissive", conf: "?"},
}

// TexInfo is what the preview needs to know about a bound texture.
type TexInfo struct {
	Format   string
	SRGB     bool
	HDR      bool
	Channels int
}

// PreviewInput is one resolved input.
type PreviewInput struct {
	Input     string   `json:"input"`
	Texture   uint64   `json:"-"`
	GUID      string   `json:"texture"`
	Slot      string   `json:"slot"`
	SlotName  string   `json:"slotName,omitempty"`
	Channel   string   `json:"channel"`
	Format    string   `json:"format,omitempty"`
	UVSet     int      `json:"uvSet"`               // 0 = UV0, 1 = UV1, 2 = UV2
	On        bool     `json:"on"`                  // shown by default (off for inputs on UV1+: a primitive has one UV set)
	Intensity *float32 `json:"intensity,omitempty"` // from a parameter (emissive)
	Conf      string   `json:"conf"`
	Note      string   `json:"note,omitempty"`
	// textures a composed channel also reads (overlay, layers, emissive × colour)
	With []uint64 `json:"-"`
}

// PreviewParam is a parameter the preview uses, with its confidence.
type PreviewParam struct {
	Hash   string    `json:"hash"`
	Use    string    `json:"use"`
	Values []float32 `json:"values"`
	Conf   string    `json:"conf"`
}

// Preview is the resolved preview of a material.
type Preview struct {
	Family   Family         `json:"family"`
	Inputs   []PreviewInput `json:"inputs"`
	Params   []PreviewParam `json:"params"`
	Unmapped []PreviewInput `json:"unmapped"` // bindings no input reads (shown in the side panel)
	UVScale  [2]float32     `json:"uvScale"`
	UVOffset [2]float32     `json:"uvOffset"`
	Notes    []string       `json:"notes"`
}

// Input returns the resolved input (nil if the material has none).
func (p *Preview) Input(in string) *PreviewInput {
	for i := range p.Inputs {
		if p.Inputs[i].Input == in {
			return &p.Inputs[i]
		}
	}
	return nil
}

var paramUses = map[uint32]string{
	ParamUVScale: "uv scale", ParamUVOffset: "uv offset", ParamUVScroll: "uv scroll (× time)",
	ParamEmissive21: "emissive intensity", ParamEmissive2C: "emissive intensity", ParamAOUV2C: "AO uv set",
	ParamEmissiveUV2C: "emissive uv set", ParamOverlayAOUV: "overlay AO uv set", ParamOverlayScale: "overlay uv scale",
	ParamOverlayOfs: "overlay uv offset", ParamOverlayUV: "overlay on uv0 (1)", ParamDecalColor: "decal colour × (2 = multiply decal)",
	0x72955e70: "character flag (G-buffer RT3 0x200000 when > 0)", 0x56dfd011: "emissive / RT1.x constant (0x34)",
	0xc251f501: "layer B uv scale", 0xd44a3918: "layer B colour ×", 0x62081fbd: "overlay normal strength",
	0x3c90e14b: "decal normal alpha from opacity .r (≠ 0) or .g", 0xeb5dc7ce: "decal spec channel select",
	0x1ee1b719: "glow exponent (pow(N·V))", 0x31273623: "thickness lerp toward 1", 0xc48a324b: "uv scroll (× time)",
}

// Preview resolves the material's bindings to preview inputs. info reports a texture's format (ok = false when the
// texture is missing).
func (m *Material) Preview(info func(uint64) (TexInfo, bool)) Preview {
	f := FamilyOf(m.Shader)
	p := Preview{Family: f, Inputs: []PreviewInput{}, Params: []PreviewParam{}, Unmapped: []PreviewInput{},
		UVScale: [2]float32{1, 1}, Notes: []string{}}
	param := func(h uint32) []float32 {
		for _, pr := range m.Params {
			if pr.Hash == h {
				return pr.Values
			}
		}
		return nil
	}
	used := map[int]bool{}
	bind := func(i int, r rule) PreviewInput {
		t := m.Textures[i]
		in := PreviewInput{Input: r.input, Texture: t.GUID, GUID: fmt.Sprintf("%016x", t.GUID),
			Slot: fmt.Sprintf("%08x", t.Slot), SlotName: SlotName(t.Slot), Channel: r.channel, Conf: r.conf, Note: r.note,
			UVSet: r.uvSet}
		if ti, ok := info(t.GUID); ok {
			in.Format = ti.Format
		}
		if r.uvParam != 0 {
			if v := param(r.uvParam); len(v) == 1 && v[0] >= 1 {
				in.UVSet = int(v[0]) - 1
			}
		}
		in.On = in.UVSet == 0
		if r.intensity != 0 {
			if v := param(r.intensity); len(v) == 1 {
				x := v[0]
				in.Intensity = &x
			}
		}
		return in
	}
	find := func(s uint32, skipHDR bool) int {
		for i, t := range m.Textures {
			if t.Slot == s {
				if ti, ok := info(t.GUID); ok && !(skipHDR && ti.HDR) {
					return i
				}
			}
		}
		return -1
	}
	byName := func(n string) uint64 {
		if i := find(slot(n), false); i >= 0 {
			return m.Textures[i].GUID
		}
		return 0
	}
	has := map[string]bool{}
	for _, r := range previewRules {
		if has[r.input] || (r.only != nil && !contains(r.only, f.ID)) {
			continue
		}
		for _, s := range r.slots {
			i := find(s, false)
			if r.input == InOpacity && f.ID == 0x28 { // effect masks: a one-channel texture_N (not the HDR curves)
				i = findMask(m, info)
			}
			if i < 0 {
				continue
			}
			in := bind(i, r)
			switch r.channel {
			case "layers":
				l1, l2 := byName("layer1color"), byName("layer2color")
				if l1 != 0 && l2 != 0 {
					in.With = []uint64{l1, l2}
				} else {
					in.Channel = "rgb"
				}
			}
			p.Inputs = append(p.Inputs, in)
			used[i], has[r.input] = true, true
			break
		}
	}
	// colour fallback: the first sRGB texture no rule took (AssetFormats.md §3.4)
	if !has[InColor] && f.ID != 0x28 {
		for i, t := range m.Textures {
			if ti, ok := info(t.GUID); ok && ti.SRGB && !used[i] && t.Slot != SlotOpacity {
				p.Inputs = append(p.Inputs, bind(i, rule{input: InColor, channel: "rgb", conf: "?",
					note: "no colour slot: the first sRGB texture"}))
				used[i], has[InColor] = true, true
				break
			}
		}
	}
	// last resort (a material whose textures sit only in unknown slots: 0x2A, 0x30, 0x32): its first LDR texture as
	// colour, so every material with a texture shows something [?]
	if len(p.Inputs) == 0 {
		for i, t := range m.Textures {
			if ti, ok := info(t.GUID); ok && !ti.HDR {
				p.Inputs = append(p.Inputs, bind(i, rule{input: InColor, channel: "rgb", conf: "?",
					note: "no known slot: the first texture shown as colour"}))
				used[i], has[InColor] = true, true
				break
			}
		}
	}
	// emissive = mask × albedo: the composed image also reads the colour texture
	if e := p.Input(InEmissive); e != nil && (e.Channel == "emissive" || e.Channel == "emissivelin") {
		if c := p.Input(InColor); c != nil && f.ID != 0x22 {
			e.With = []uint64{c.Texture}
		}
	}
	// opacity from the colour's alpha where there is no opacity slot and the family blends [?]
	if c := p.Input(InColor); c != nil && !has[InOpacity] && hasAlpha(c.Format) &&
		(f.Mode == "effect" || f.Mode == "alphatest" || f.Mode == "unlit") {
		o := *c
		o.Input, o.Channel, o.Conf, o.Note, o.With = InOpacity, "a", "?", "the colour texture's alpha (no opacity slot)", nil
		p.Inputs = append(p.Inputs, o)
		has[InOpacity] = true
	}
	for i, t := range m.Textures {
		if used[i] {
			continue
		}
		u := PreviewInput{Texture: t.GUID, GUID: fmt.Sprintf("%016x", t.GUID), Slot: fmt.Sprintf("%08x", t.Slot),
			SlotName: SlotName(t.Slot)}
		if ti, ok := info(t.GUID); ok {
			u.Format = ti.Format
		} else {
			u.Note = "missing"
		}
		p.Unmapped = append(p.Unmapped, u)
	}
	for _, pr := range m.Params {
		use, ok := paramUses[pr.Hash]
		if !ok {
			continue
		}
		p.Params = append(p.Params, PreviewParam{Hash: fmt.Sprintf("%08x", pr.Hash), Use: use, Values: pr.Values, Conf: "D"})
		switch {
		case pr.Hash == ParamUVScale && len(pr.Values) == 2:
			p.UVScale = [2]float32{pr.Values[0], pr.Values[1]}
		case pr.Hash == ParamUVOffset && len(pr.Values) == 2:
			p.UVOffset = [2]float32{pr.Values[0], pr.Values[1]}
		}
	}
	for _, in := range p.Inputs {
		if !in.On {
			p.Notes = append(p.Notes, fmt.Sprintf("%s is on UV%d: off by default (the preview shapes have one UV set)", in.Input, in.UVSet))
		}
		if in.Intensity != nil && *in.Intensity == 0 {
			p.Notes = append(p.Notes, "emissive intensity parameter is 0: no emission in game")
		}
	}
	if v := param(ParamDecalColor); f.ID == 0x22 && len(v) == 1 && v[0] == 2 {
		p.Notes = append(p.Notes, "multiply decal (colour × 2 multiplies what is under it)")
	}
	switch {
	case len(m.Textures) == 0:
		p.Notes = append(p.Notes, "no textures: the preview shows a plain surface")
	case f.ID == 0x28:
		p.Notes = append(p.Notes, "effect: mask only; the colour constant lives in the material's constant buffer (layout per shader instance, not resolved)")
	case !has[InColor] && has[InOpacity] && f.Mode == "decal":
		p.Notes = append(p.Notes, "decal without colour: it changes the normals (and masks) under it")
	case !has[InColor]:
		p.Notes = append(p.Notes, "no colour texture identified: grey base colour")
	}
	if m.Helper() {
		p.Notes = append(p.Notes, "helper / editor material (+0x68 bit 1)")
	}
	return p
}

// findMask: the first texture_N in a one-channel LDR format (BC4 / R8), else the first LDR texture_N.
func findMask(m *Material, info func(uint64) (TexInfo, bool)) int {
	best := -1
	for i, t := range m.Textures {
		ti, ok := info(t.GUID)
		if !ok || ti.HDR || !slices.Contains(sTexture, t.Slot) {
			continue
		}
		if ti.Channels == 1 {
			return i
		}
		if best < 0 {
			best = i
		}
	}
	return best
}

func hasAlpha(format string) bool {
	switch format {
	case "BC3_UNORM_SRGB", "BC3_UNORM", "R8G8B8A8_UNORM_SRGB", "R8G8B8A8_UNORM", "R16G16B16A16_FLOAT":
		return true
	}
	return false
}

func contains(l []uint16, v uint16) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}
