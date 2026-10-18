package model

import (
	"image"
	"math"
)

// Software material preview (T071): a sphere, plane or cube ray-cast analytically and shaded with a simple
// metal/roughness GGX model plus a hemisphere ambient, for the MCP tool material_image and the material thumbnails.
// It mirrors the Material tab's three.js preview closely enough to recognise a material; it is not the game's shader.

// MatInputs are the prepared input images (app_material.go previewImage): colour as stored (sRGB), normal encoded
// (x, y, z) → (x+1)/2 …, the scalar inputs grey. nil = the input is absent.
type MatInputs struct {
	Color, Opacity, Normal, Roughness, Specular, Metalness, AO, Emissive *image.NRGBA
	EmissiveIntensity                                                    float32 // the emissive image (already × albedo) × this
}

// MatView sets up a render.
type MatView struct {
	Size       int
	Geometry   string // sphere (default), plane, cube
	Mode       string // Family.Mode
	UVScale    [2]float32
	LightYaw   float64 // degrees around Y from +Z (the camera side)
	LightPitch float64 // degrees above the horizon
	FlipGreen  bool    // OpenGL normal-map convention (green = up in the image)
	Background [3]uint8
}

type v3 [3]float64

func (a v3) add(b v3) v3      { return v3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func (a v3) sub(b v3) v3      { return v3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func (a v3) mul(s float64) v3 { return v3{a[0] * s, a[1] * s, a[2] * s} }
func (a v3) dot(b v3) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func (a v3) norm() v3 {
	l := math.Sqrt(a.dot(a))
	if l == 0 {
		return a
	}
	return a.mul(1 / l)
}
func (a v3) hadamard(b v3) v3     { return v3{a[0] * b[0], a[1] * b[1], a[2] * b[2]} }
func lerp3(a, b v3, t float64) v3 { return a.mul(1 - t).add(b.mul(t)) }

// rotation of the plane (tilted back) and the cube (yaw + pitch), as rows
type mat3 [3]v3

func rotYX(yaw, pitch float64) mat3 {
	cy, sy := math.Cos(yaw), math.Sin(yaw)
	cp, sp := math.Cos(pitch), math.Sin(pitch)
	// R = Rx(pitch) · Ry(yaw)
	return mat3{{cy, 0, sy}, {sp * sy, cp, -sp * cy}, {-cp * sy, sp, cp * cy}}
}
func (r mat3) apply(v v3) v3  { return v3{r[0].dot(v), r[1].dot(v), r[2].dot(v)} }
func (r mat3) applyT(v v3) v3 { return r[0].mul(v[0]).add(r[1].mul(v[1])).add(r[2].mul(v[2])) }

// hit: position, geometric normal, tangent (+u), bitangent (+v, image down), uv
type hit struct {
	n, t, b v3
	u, v    float64
}

func intersect(geom string, ro, rd v3) (hit, bool) {
	switch geom {
	case "plane":
		r := rotYX(0, 35*math.Pi/180)    // tilted back: seen from above
		o, d := r.apply(ro), r.apply(rd) // into plane space (plane z = 0, normal +z)
		if d[2] >= 0 {
			return hit{}, false
		}
		t := -o[2] / d[2]
		p := o.add(d.mul(t))
		const s = 1.0
		if math.Abs(p[0]) > s || math.Abs(p[1]) > s {
			return hit{}, false
		}
		return hit{n: r.applyT(v3{0, 0, 1}), t: r.applyT(v3{1, 0, 0}), b: r.applyT(v3{0, -1, 0}),
			u: (p[0] + s) / (2 * s), v: (s - p[1]) / (2 * s)}, true
	case "cube":
		r := rotYX(35*math.Pi/180, 25*math.Pi/180)
		o, d := r.apply(ro), r.apply(rd)
		const s = 0.62
		tmin, tmax := -math.MaxFloat64, math.MaxFloat64
		axis := 0
		for k := range 3 {
			if math.Abs(d[k]) < 1e-12 {
				if math.Abs(o[k]) > s {
					return hit{}, false
				}
				continue
			}
			t1, t2 := (-s-o[k])/d[k], (s-o[k])/d[k]
			if t1 > t2 {
				t1, t2 = t2, t1
			}
			if t1 > tmin {
				tmin, axis = t1, k
			}
			tmax = math.Min(tmax, t2)
		}
		if tmin > tmax || tmax < 0 {
			return hit{}, false
		}
		p := o.add(d.mul(tmin))
		var n, t, b v3
		sg := math.Copysign(1, p[axis])
		n[axis] = sg
		// face axes: u along t, v (image down) along b
		switch axis {
		case 0:
			t, b = v3{0, 0, -sg}, v3{0, -1, 0}
		case 1:
			t, b = v3{1, 0, 0}, v3{0, 0, sg}
		case 2:
			t, b = v3{sg, 0, 0}, v3{0, -1, 0}
		}
		u := (t.dot(p)/s + 1) / 2
		v := (b.dot(p)/s + 1) / 2
		return hit{n: r.applyT(n), t: r.applyT(t), b: r.applyT(b), u: u, v: v}, true
	default: // unit sphere
		bq := ro.dot(rd)
		c := ro.dot(ro) - 1
		disc := bq*bq - c
		if disc < 0 {
			return hit{}, false
		}
		t := -bq - math.Sqrt(disc)
		p := ro.add(rd.mul(t))
		theta := math.Acos(math.Max(-1, math.Min(1, p[1])))
		phi := math.Atan2(p[2], -p[0])
		if phi < 0 {
			phi += 2 * math.Pi
		}
		st := math.Sin(theta)
		tan := v3{math.Sin(phi), 0, math.Cos(phi)}
		bit := v3{-math.Cos(phi) * math.Cos(theta), -st, math.Sin(phi) * math.Cos(theta)}
		return hit{n: p, t: tan, b: bit.norm(), u: phi / (2 * math.Pi), v: theta / math.Pi}, true
	}
}

// bilinear sample with wrap addressing, channels 0..1
func sample(img *image.NRGBA, u, v float64) [4]float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	x := (u-math.Floor(u))*float64(w) - 0.5
	y := (v-math.Floor(v))*float64(h) - 0.5
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	fx, fy := x-float64(x0), y-float64(y0)
	var out [4]float64
	for j := range 2 {
		for i := range 2 {
			xx, yy := ((x0+i)%w+w)%w, ((y0+j)%h+h)%h
			wt := (1 - math.Abs(float64(i)-fx)) * (1 - math.Abs(float64(j)-fy))
			o := img.PixOffset(b.Min.X+xx, b.Min.Y+yy)
			for c := range 4 {
				out[c] += wt * float64(img.Pix[o+c]) / 255
			}
		}
	}
	return out
}

func toLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func toSRGB(c float64) float64 {
	if c <= 0.0031308 {
		return c * 12.92
	}
	return 1.055*math.Pow(c, 1/2.4) - 0.055
}

// aces: Narkowicz's fit
func aces(x float64) float64 {
	return math.Max(0, math.Min(1, (x*(2.51*x+0.03))/(x*(2.43*x+0.59)+0.14)))
}

// Surface is one shaded point's material (linear colours).
type Surface struct {
	Albedo                 v3
	Rough, Spec, Metal, AO float64 // spec: dielectric F0 / 0.08
	Emissive               v3
}

// lit: GGX sun + hemisphere ambient; returns the linear colour and the Fresnel term.
func lit(s Surface, N, V, L, sky, ground v3) (v3, v3) {
	rough := math.Max(0.04, s.Rough)
	f0 := lerp3(v3{0.08 * s.Spec, 0.08 * s.Spec, 0.08 * s.Spec}, s.Albedo, s.Metal)
	diff := s.Albedo.mul(1 - s.Metal)
	NdV := math.Max(1e-4, N.dot(V))
	NdL := math.Max(0, N.dot(L))
	H := L.add(V).norm()
	NdH := math.Max(0, N.dot(H))
	a := rough * rough
	a2 := a * a
	dd := NdH*NdH*(a2-1) + 1
	D := a2 / (math.Pi * dd * dd)
	k := (rough + 1) * (rough + 1) / 8
	G := (NdV / (NdV*(1-k) + k)) * (NdL / (NdL*(1-k) + k))
	fres := math.Pow(1-math.Max(0, H.dot(V)), 5)
	F := f0.add(v3{1, 1, 1}.sub(f0).mul(fres))
	specBRDF := F.mul(D * G / (4 * NdV * math.Max(NdL, 1e-4)))
	const light = 2.6
	direct := diff.mul(1 / math.Pi).add(specBRDF).mul(light * NdL)
	hemi := lerp3(ground, sky, N[1]*0.5+0.5)
	R := N.mul(2 * N.dot(V)).sub(V)
	env := lerp3(ground, sky, R[1]*0.5+0.5)
	fa := f0.add(v3{1, 1, 1}.sub(f0).mul(math.Pow(1-NdV, 5) * (1 - rough)))
	ambient := diff.hadamard(hemi).add(fa.hadamard(env).mul(1 - 0.7*rough)).mul(s.AO)
	return direct.add(ambient).add(s.Emissive), F
}

// ShadePBR is the material renderer's lighting for another rasteriser (model_image's PBR mode): the sun from
// lightYaw / lightPitch (degrees), N and V unit vectors in the same space (Y up). Returns 8-bit sRGB.
func ShadePBR(albedoSRGB [3]float64, rough, spec, metal, ao float64, emissiveLinear [3]float64, N, V [3]float64, lightYaw, lightPitch float64) [3]uint8 {
	ly, lp := lightYaw*math.Pi/180, lightPitch*math.Pi/180
	L := v3{math.Sin(ly) * math.Cos(lp), math.Sin(lp), math.Cos(ly) * math.Cos(lp)}.norm()
	s := Surface{Albedo: v3{toLinear(albedoSRGB[0]), toLinear(albedoSRGB[1]), toLinear(albedoSRGB[2])}, Rough: rough,
		Spec: spec, Metal: metal, AO: ao, Emissive: v3(emissiveLinear)}
	c, _ := lit(s, v3(N), v3(V), L, v3{0.55, 0.6, 0.68}, v3{0.16, 0.14, 0.12})
	var out [3]uint8
	for k := range 3 {
		out[k] = uint8(math.Round(toSRGB(aces(c[k])) * 255))
	}
	return out
}

// RenderMaterial draws the material preview.
func RenderMaterial(in MatInputs, vw MatView) *image.NRGBA {
	n := max(vw.Size, 8)
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	us, vs := float64(vw.UVScale[0]), float64(vw.UVScale[1])
	if us == 0 {
		us = 1
	}
	if vs == 0 {
		vs = 1
	}
	ly, lp := vw.LightYaw*math.Pi/180, vw.LightPitch*math.Pi/180
	L := v3{math.Sin(ly) * math.Cos(lp), math.Sin(lp), math.Cos(ly) * math.Cos(lp)}.norm()
	eye := v3{0, 0, 4.3}
	fov := math.Tan(30 * math.Pi / 180 / 2)
	sky, ground := v3{0.55, 0.6, 0.68}, v3{0.16, 0.14, 0.12}
	bg := v3{float64(vw.Background[0]) / 255, float64(vw.Background[1]) / 255, float64(vw.Background[2]) / 255}
	checker := vw.Mode == "effect" || vw.Mode == "decal" || vw.Mode == "alphatest" || vw.Mode == "glass"
	for py := range n {
		for px := range n {
			sx := (2*(float64(px)+0.5)/float64(n) - 1) * fov
			sy := (1 - 2*(float64(py)+0.5)/float64(n)) * fov
			rd := v3{sx, sy, -1}.norm()
			back := bg
			if checker && ((px*8/n)+(py*8/n))%2 == 0 {
				back = bg.mul(1.6).add(v3{0.02, 0.02, 0.02})
			}
			col := back
			if h, ok := intersect(vw.Geometry, eye, rd); ok {
				col = shade(in, vw, h, L, rd.mul(-1), us, vs, sky, ground, back)
			}
			o := img.PixOffset(px, py)
			for c := range 3 {
				img.Pix[o+c] = uint8(math.Round(math.Max(0, math.Min(1, col[c])) * 255))
			}
			img.Pix[o+3] = 255
		}
	}
	return img
}

// shade returns the display (sRGB) colour of a hit.
func shade(in MatInputs, vw MatView, h hit, L, V v3, us, vs float64, sky, ground, back v3) v3 {
	u, v := h.u*us, h.v*vs
	albedo := v3{0.5, 0.5, 0.5}
	alpha := 1.0
	if in.Color != nil {
		s := sample(in.Color, u, v)
		albedo = v3{toLinear(s[0]), toLinear(s[1]), toLinear(s[2])}
	}
	if in.Opacity != nil {
		alpha = sample(in.Opacity, u, v)[0]
	}
	switch vw.Mode {
	case "effect": // additive: colour × alpha over the background
		return lerp3(back, back.add(albedo.mul(alpha)), 1)
	case "unlit", "sky":
		c := v3{toSRGB(albedo[0]), toSRGB(albedo[1]), toSRGB(albedo[2])}
		return lerp3(back, c, alpha)
	case "alphatest":
		if alpha < 0.5 {
			return back
		}
		alpha = 1
	}
	N := h.n
	decal := vw.Mode == "decal"
	if decal && in.Color == nil {
		albedo = v3{0.5, 0.5, 0.5}
	}
	if in.Normal != nil {
		s := sample(in.Normal, u, v)
		x, y, z := s[0]*2-1, s[1]*2-1, s[2]*2-1
		if vw.FlipGreen {
			y = -y
		}
		pn := h.t.mul(x).add(h.b.mul(y)).add(h.n.mul(z)).norm()
		if decal {
			pn = lerp3(h.n, pn, alpha).norm()
		}
		N = pn
	}
	rough, spec, metal, ao := 0.6, 0.5, 0.0, 1.0
	if in.Roughness != nil {
		rough = sample(in.Roughness, u, v)[0]
	}
	if in.Specular != nil {
		spec = sample(in.Specular, u, v)[0]
	}
	if in.Metalness != nil {
		metal = sample(in.Metalness, u, v)[0]
	}
	if in.AO != nil {
		ao = sample(in.AO, u, v)[0]
	}
	if decal { // the decal over a grey base: colour blended by its opacity
		albedo = lerp3(v3{0.45, 0.45, 0.45}, albedo, alpha)
		alpha = 1
	}
	var em v3
	if in.Emissive != nil {
		s := sample(in.Emissive, u, v)
		em = v3{toLinear(s[0]), toLinear(s[1]), toLinear(s[2])}.mul(float64(in.EmissiveIntensity))
	}
	c, F := lit(Surface{Albedo: albedo, Rough: rough, Spec: spec, Metal: metal, AO: ao, Emissive: em}, N, V, L, sky, ground)
	out := v3{toSRGB(aces(c[0])), toSRGB(aces(c[1])), toSRGB(aces(c[2]))}
	if vw.Mode == "glass" {
		alpha = 0.35 + 0.65*F[0]
	}
	return lerp3(back, out, alpha)
}
