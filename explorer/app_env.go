package main

// Map lighting for the 3D map mode (T071 round 3, docs/Explorer.md "3D map mode"): the map environment's sun
// (direction, HDR colour), its sky / ambient / reflection cube maps and its colour-grading LUT, read from the map
// data's environment block (Maps.md §2: +0xF0…+0x1EF), and the cube maps as HDR equirectangular panoramas for three.js
// (/envmap/<texture>.bin).

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"

	"owexplorer/internal/catalog"
	"owexplorer/internal/model"
	"owexplorer/internal/texture"
)

// MapEnvironmentView is what the 3D map needs to light a map like the game (as far as known).
type MapEnvironmentView struct {
	SunDir     [3]float32 `json:"sunDir"`     // +0x14C: unit vector, the direction the sunlight travels (down) [D value, ? meaning]
	SunColor   [3]float32 `json:"sunColor"`   // +0x158: HDR colour × intensity (Anubis 3.29, 2.23, 1.61) [D value, ? meaning]
	Sky        string     `json:"sky"`        // +0x180: RGBA16F cube, the sky seen from the map (Anubis 64 px) [D image]
	Ambient    string     `json:"ambient"`    // +0xF8: RGBA16F 8 px cube, sky irradiance [? from the image]
	Reflection string     `json:"reflection"` // +0x130: BC6H cube array (4 cubes), an in-map reflection capture [D image]
	LUT        string     `json:"lut"`        // +0x108: RGBA8 1024×32 = a 32³ colour-grading LUT (32 slices of 32×32) [D image]
	SkyModel   string     `json:"skyModel"`   // +0x110 / +0x118: the sky model + look (Maps.md) [D]
	SkyLook    string     `json:"skyLook"`
	Floats     []float32  `json:"floats"` // +0x140…+0x1EF as floats (the other lighting / fog parameters, meaning unknown)
}

// texGUID reads a u64 at o when it is a texture (0x004) GUID, else "".
func texGUID(b []byte, o int) string {
	g := binary.LittleEndian.Uint64(b[o:])
	if model.GUIDType(g) != 0x004 {
		return ""
	}
	return hex16(g)
}

// MapEnvironment reads a map's environment block.
func (a *App) MapEnvironment(guid string) (MapEnvironmentView, error) {
	_, m, err := a.openMap(guid)
	if err != nil {
		return MapEnvironmentView{}, err
	}
	b := m.Bytes
	if len(b) < 0x1F0 {
		return MapEnvironmentView{}, errors.New("map data too short for an environment block")
	}
	le := binary.LittleEndian
	f := func(o int) float32 { return math.Float32frombits(le.Uint32(b[o:])) }
	v := MapEnvironmentView{
		SunDir:     [3]float32{f(0x14C), f(0x150), f(0x154)},
		SunColor:   [3]float32{f(0x158), f(0x15C), f(0x160)},
		Sky:        texGUID(b, 0x180),
		Ambient:    texGUID(b, 0xF8),
		Reflection: texGUID(b, 0x130),
		LUT:        texGUID(b, 0x108),
	}
	if g := le.Uint64(b[0x110:]); model.GUIDType(g) == model.TypeModel {
		v.SkyModel = hex16(g)
	}
	if g := le.Uint64(b[0x118:]); model.GUIDType(g) == model.TypeLook {
		v.SkyLook = hex16(g)
	}
	for o := 0x140; o < 0x1F0; o += 4 {
		v.Floats = append(v.Floats, f(o))
	}
	return v, nil
}

// cubeDir maps a direction (game axes, Y up) to a cube face and its texel coordinates (u, v in 0..1, v down): the
// D3D / GL cube map selection rule (faces +X, −X, +Y, −Y, +Z, −Z).
func cubeDir(x, y, z float64) (face int, u, v float64) {
	ax, ay, az := math.Abs(x), math.Abs(y), math.Abs(z)
	var sc, tc, ma float64
	switch {
	case ax >= ay && ax >= az:
		ma = ax
		if x > 0 {
			face, sc, tc = 0, -z, -y
		} else {
			face, sc, tc = 1, z, -y
		}
	case ay >= az:
		ma = ay
		if y > 0 {
			face, sc, tc = 2, x, z
		} else {
			face, sc, tc = 3, x, -z
		}
	default:
		ma = az
		if z > 0 {
			face, sc, tc = 4, x, -y
		} else {
			face, sc, tc = 5, -x, -y
		}
	}
	return face, (sc/ma + 1) / 2, (tc/ma + 1) / 2
}

// equirect converts cube map faces (largest available mip, first cube of an array) to a w × w/2 RGBA float32
// panorama in three.js's equirectangular layout: u = atan2(z, x) / 2π + 0.5, v = asin(y) / π + 0.5, row 0 = v 0
// (straight down).
func equirect(t *texture.Texture, w int) ([]float32, int, int, error) {
	return equirectMip(t, w, t.Best())
}

// equirectMip is equirect from one mip (a smaller mip = a blurrier panorama; the probes' mip chains serve as roughness
// levels). An unavailable mip falls back to the best one.
func equirectMip(t *texture.Texture, w, mip int) ([]float32, int, int, error) {
	if t.Format == nil {
		return nil, 0, 0, errors.New("texture: no decoder")
	}
	if t.Header.SurfaceCount() < 6 {
		return nil, 0, 0, fmt.Errorf("texture %016x is not a cube map", t.GUID)
	}
	if mip < 0 || mip >= len(t.Mips) || !t.Available(mip) {
		mip = t.Best()
	}
	if mip < 0 {
		return nil, 0, 0, errors.New("no mip available")
	}
	fw, fh := t.Mips[mip].Width, t.Mips[mip].Height
	var faces [6][]float32
	for s := range 6 {
		px, err := t.Decode(mip, s)
		if err != nil {
			return nil, 0, 0, err
		}
		faces[s] = px
	}
	h := w / 2
	out := make([]float32, w*h*4)
	for j := range h {
		el := ((float64(j)+0.5)/float64(h) - 0.5) * math.Pi
		for i := range w {
			az := ((float64(i)+0.5)/float64(w) - 0.5) * 2 * math.Pi
			x, y, z := math.Cos(az)*math.Cos(el), math.Sin(el), math.Sin(az)*math.Cos(el)
			face, u, v := cubeDir(x, y, z)
			fx, fy := min(int(u*float64(fw)), fw-1), min(int(v*float64(fh)), fh-1)
			copy(out[(j*w+i)*4:(j*w+i)*4+4], faces[face][(fy*fw+fx)*4:(fy*fw+fx)*4+4])
		}
	}
	return out, w, h, nil
}

// envmapBinary is /envmap/<texture>.bin?w=N: "OWEQ", u32 w, u32 h, then w × h × RGBA float32 (little-endian).
func (a *App) envmapBinary(guid string, w int) ([]byte, error) {
	c, err := a.catalog()
	if err != nil {
		return nil, err
	}
	g, err := parse(guid)
	if err != nil {
		return nil, err
	}
	t, err := a.loadTexture(c, textureOf(g))
	if err != nil {
		return nil, err
	}
	if w <= 0 || w > 2048 {
		w = 512
	}
	px, w, h, err := equirect(t, w)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 12+len(px)*4)
	copy(out, "OWEQ")
	binary.LittleEndian.PutUint32(out[4:], uint32(w))
	binary.LittleEndian.PutUint32(out[8:], uint32(h))
	for i, f := range px {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			f = 0
		}
		binary.LittleEndian.PutUint32(out[12+4*i:], math.Float32bits(f))
	}
	return out, nil
}

// --- reflection probes (T071 round 5) ------------------------------------------------------------------------------

// ProbeView is one reflection probe (map record type 4, Maps.md §2) [D layout, T071]: +0x18 position, +0x24 scale,
// +0x30 rotation (quaternion x, y, z, w), +0x40 a local transform (identity on the maps checked, not applied), +0x68
// the box half-extents, +0x78 an sRGB BC1 cube and +0x80 an HDR BC6H cube (256 px, 9 mips). Record -1 is the map-wide
// capture from the environment block (+0x130), used where no probe box contains the point.
type ProbeView struct {
	Record   int        `json:"record"`
	Pos      [3]float32 `json:"pos"`
	Scale    [3]float32 `json:"scale"`
	Rot      [4]float32 `json:"rot"`
	Extents  [3]float32 `json:"extents"`
	HDR      string     `json:"hdr"`
	LDR      string     `json:"ldr"`
	Floats   []float32  `json:"floats"`   // +0x98…+0xAB: [3] (+0xA4) an exponent (mostly 6), [4] tracks the capture's brightness
	Exposure float32    `json:"exposure"` // brightness factor applied to the capture (MapProbes)
}

// MapProbesView lists a map's probes and the atlas layout of /probes/<map>.bin.
type MapProbesView struct {
	Probes []ProbeView `json:"probes"` // the atlas tiles are in this order; the map-wide capture (record −1) is last
	Levels []int       `json:"levels"` // panorama width per blur level (height = width / 2)
	Mips   []int       `json:"mips"`   // the cube mip each level is made from
}

var probeLevels = []struct{ w, mip int }{{128, 2}, {32, 4}, {8, 6}}

// MapProbes reads a map's reflection probes.
func (a *App) MapProbes(guid string) (MapProbesView, error) {
	c, m, err := a.openMap(guid)
	if err != nil {
		return MapProbesView{}, err
	}
	le := binary.LittleEndian
	v := MapProbesView{Probes: []ProbeView{}}
	for _, l := range probeLevels {
		v.Levels = append(v.Levels, l.w)
		v.Mips = append(v.Mips, l.mip)
	}
	a.mmu.Lock()
	recs := m.M.Records
	a.mmu.Unlock()
	for ri, r := range recs {
		if r.Type != 4 || r.Size < 0xB0 {
			continue
		}
		b := m.Bytes[int(r.Offset) : int(r.Offset)+int(r.Size)]
		f := func(o int) float32 { return math.Float32frombits(le.Uint32(b[o:])) }
		p := ProbeView{Record: ri, Pos: [3]float32{f(0x18), f(0x1C), f(0x20)}, Scale: [3]float32{f(0x24), f(0x28), f(0x2C)},
			Rot: [4]float32{f(0x30), f(0x34), f(0x38), f(0x3C)}, Extents: [3]float32{f(0x68), f(0x6C), f(0x70)},
			HDR: texGUID(b, 0x80), LDR: texGUID(b, 0x78)}
		for o := 0x98; o < 0xAC; o += 4 {
			p.Floats = append(p.Floats, f(o))
		}
		if p.HDR == "" {
			continue
		}
		if g, _ := parse(p.HDR); func() bool { _, ok := c.Store.Find(g); return !ok }() {
			continue
		}
		v.Probes = append(v.Probes, p)
	}
	// Exposure [? inferred, T071 round 6]: the captures are stored ~50× darker than the ambient cube. One factor per map,
	// 2^(median of +0xA4) (mostly 6 → ×64), brings a typical probe to the ambient level and keeps the probes' relative
	// brightness. Per-probe 2^(+0xA4) spread them 12,000× apart (0.005 … 60), so it is not a plain per-probe exposure.
	var exps []float64
	for o := range v.Probes {
		exps = append(exps, float64(v.Probes[o].Floats[3]))
	}
	if len(exps) > 0 {
		slices.Sort(exps)
		k := float32(math.Exp2(exps[len(exps)/2]))
		for o := range v.Probes {
			v.Probes[o].Exposure = k
		}
	}
	if len(m.Bytes) >= 0x138 {
		if g := texGUID(m.Bytes, 0x130); g != "" {
			v.Probes = append(v.Probes, ProbeView{Record: -1, HDR: g, LDR: texGUID(m.Bytes, 0x128), Rot: [4]float32{0, 0, 0, 1},
				Scale: [3]float32{1, 1, 1}, Exposure: a.captureScale(c, texGUID(m.Bytes, 0xF8), g)})
		}
	}
	return v, nil
}

// captureScale: the ambient cube's mean luminance / the capture's (the map-wide capture has no exposure field) [?].
func (a *App) captureScale(c *catalog.Catalog, ambient, capture string) float32 {
	mean := func(guid string) float64 {
		g, err := parse(guid)
		if err != nil {
			return 0
		}
		t, err := a.loadTexture(c, textureOf(g))
		if err != nil {
			return 0
		}
		px, w, h, err := equirectMip(t, 32, len(t.Mips)-1)
		if err != nil {
			return 0
		}
		s := 0.0
		for i := range w * h {
			s += float64(0.2126*px[4*i] + 0.7152*px[4*i+1] + 0.0722*px[4*i+2])
		}
		return s / float64(w*h)
	}
	am, cm := mean(ambient), mean(capture)
	if am <= 0 || cm <= 0 {
		return 1
	}
	return float32(am / cm)
}

// probesBinary is /probes/<map>.bin: "OWPR", u32 probes, u32 levels, then per level (width, height = width / 2) a
// width × (height × probes) RGBA half-float atlas, the probes' panoramas stacked vertically in MapProbes' order.
func (a *App) probesBinary(guid string) ([]byte, error) {
	c, err := a.catalog()
	if err != nil {
		return nil, err
	}
	pv, err := a.MapProbes(guid)
	if err != nil {
		return nil, err
	}
	n := len(pv.Probes)
	out := []byte("OWPR")
	out = binary.LittleEndian.AppendUint32(out, uint32(n))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(probeLevels)))
	pans := make([][][]float32, len(pv.Probes)) // probe → level → pixels
	for i, p := range pv.Probes {
		g, _ := parse(p.HDR)
		t, err := a.loadTexture(c, textureOf(g))
		if err != nil {
			return nil, err
		}
		for _, l := range probeLevels {
			px, _, _, err := equirectMip(t, l.w, l.mip)
			if err != nil {
				return nil, err
			}
			for k := range px {
				px[k] *= p.Exposure
			}
			pans[i] = append(pans[i], px)
		}
	}
	for li, l := range probeLevels {
		out = binary.LittleEndian.AppendUint32(out, uint32(l.w))
		for i := range pv.Probes {
			for _, f := range pans[i][li] {
				if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
					f = 0
				}
				out = binary.LittleEndian.AppendUint16(out, half(min(f, 65000)))
			}
		}
	}
	return out, nil
}

// half converts a float32 to IEEE half (round to nearest, no NaN handling needed here).
func half(f float32) uint16 {
	b := math.Float32bits(f)
	sign := uint16(b>>16) & 0x8000
	exp := int((b>>23)&0xff) - 127 + 15
	mant := b & 0x7fffff
	switch {
	case exp <= 0:
		if exp < -10 {
			return sign
		}
		mant |= 0x800000
		return sign | uint16(mant>>uint(14-exp))
	case exp >= 31:
		return sign | 0x7c00
	}
	return sign | uint16(exp)<<10 | uint16(mant>>13)
}
