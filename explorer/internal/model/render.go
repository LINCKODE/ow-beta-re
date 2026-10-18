package model

import (
	"image"
	"image/color"
	"math"
)

// Sampler returns the colour of a group's texture at (u, v) (D3D coordinates, wrapped), ok = false for none.
type Sampler func(group int, u, v float32) (color.NRGBA, bool)

// View of a render: the camera orbits the mesh's bounding box centre.
type View struct {
	Yaw, Pitch float64 // degrees; yaw 0 looks along −Z from +Z (the model's front if it faces +Z), pitch > 0 from above
	Mirror     bool    // mirror X
	Highlight  int     // submesh index drawn tinted (−1 none)
	Skip       []bool  // groups not drawn (e.g. normal-only decals)
	// Shade, when set, colours a pixel itself (the PBR mode, T071): group, UV, the interpolated unit normal facing the
	// camera and the unit vector towards the camera (Y up, the model's axes). Result: 0 = not handled (the sampler /
	// Lambert path), 1 = col, 2 = discard (nothing drawn, no depth written).
	Shade func(group int, u, v float32, n, view [3]float64) (col color.NRGBA, res int)
}

// Render draws the mesh with a z-buffer: textured where the sampler has a colour, otherwise a colour per material
// slot; Lambert shading from a light at the camera plus ambient. Background dark grey. For the MCP tool
// model_image (docs/Explorer.md "MCP server").
func Render(me *Mesh, w, h int, vw View, sample Sampler) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	bg := color.NRGBA{16, 21, 31, 255}
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = bg.R, bg.G, bg.B, 255
	}
	if len(me.Positions) == 0 {
		return img
	}
	var c [3]float64
	r := 0.0
	for k := range 3 {
		c[k] = float64(me.Min[k]+me.Max[k]) / 2
		d := float64(me.Max[k] - me.Min[k])
		r += d * d
	}
	r = math.Max(math.Sqrt(r)/2, 1e-3)
	yaw, pitch := vw.Yaw*math.Pi/180, vw.Pitch*math.Pi/180
	// camera basis: forward f (from the eye to the centre), right x, up y
	f := [3]float64{-math.Sin(yaw) * math.Cos(pitch), -math.Sin(pitch), -math.Cos(yaw) * math.Cos(pitch)}
	x := norm(cross(f, [3]float64{0, 1, 0}))
	y := cross(x, f)
	dist := r * 2.6
	eye := [3]float64{c[0] - f[0]*dist, c[1] - f[1]*dist, c[2] - f[2]*dist}
	fov := 40 * math.Pi / 180
	sc := float64(h) / 2 / math.Tan(fov/2)
	nv := len(me.Positions) / 3
	type sv struct{ x, y, z float64 }
	scr := make([]sv, nv)
	shade := make([]float64, nv)
	nrm := make([][3]float64, nv)
	for i := range nv {
		p := [3]float64{float64(me.Positions[3*i]), float64(me.Positions[3*i+1]), float64(me.Positions[3*i+2])}
		n := [3]float64{float64(me.Normals[3*i]), float64(me.Normals[3*i+1]), float64(me.Normals[3*i+2])}
		if vw.Mirror {
			p[0], n[0] = 2*c[0]-p[0], -n[0]
		}
		d := [3]float64{p[0] - eye[0], p[1] - eye[1], p[2] - eye[2]}
		cz := dot(d, f)
		if cz < 1e-4 {
			cz = 1e-4
		}
		scr[i] = sv{float64(w)/2 + dot(d, x)/cz*sc, float64(h)/2 - dot(d, y)/cz*sc, cz}
		nrm[i] = n
		l := math.Abs(-dot(n, f)) // two-sided
		shade[i] = 0.35 + 0.65*l
	}
	zb := make([]float64, w*h)
	for i := range zb {
		zb[i] = math.Inf(1)
	}
	for gi, g := range me.Groups {
		if gi < len(vw.Skip) && vw.Skip[gi] {
			continue
		}
		base := slotColour(g.Material)
		hl := g.Submesh == vw.Highlight
		for k := g.Index; k+2 < g.Index+g.Count; k += 3 {
			ia, ib, ic := me.Indices[k], me.Indices[k+1], me.Indices[k+2]
			a, b, cc := scr[ia], scr[ib], scr[ic]
			minX := int(math.Max(0, math.Floor(math.Min(a.x, math.Min(b.x, cc.x)))))
			maxX := int(math.Min(float64(w-1), math.Ceil(math.Max(a.x, math.Max(b.x, cc.x)))))
			minY := int(math.Max(0, math.Floor(math.Min(a.y, math.Min(b.y, cc.y)))))
			maxY := int(math.Min(float64(h-1), math.Ceil(math.Max(a.y, math.Max(b.y, cc.y)))))
			area := (b.x-a.x)*(cc.y-a.y) - (b.y-a.y)*(cc.x-a.x)
			if area == 0 || minX > maxX || minY > maxY {
				continue
			}
			for py := minY; py <= maxY; py++ {
				for px := minX; px <= maxX; px++ {
					qx, qy := float64(px)+0.5, float64(py)+0.5
					w0 := ((b.x-qx)*(cc.y-qy) - (b.y-qy)*(cc.x-qx)) / area
					w1 := ((cc.x-qx)*(a.y-qy) - (cc.y-qy)*(a.x-qx)) / area
					w2 := 1 - w0 - w1
					if w0 < 0 || w1 < 0 || w2 < 0 {
						continue
					}
					// perspective-correct depth and attributes
					iz := w0/a.z + w1/b.z + w2/cc.z
					z := 1 / iz
					o := py*w + px
					if z >= zb[o] {
						continue
					}
					p0, p1, p2 := w0/a.z*z, w1/b.z*z, w2/cc.z*z
					if vw.Shade != nil {
						u := float32(p0)*me.UVs[2*ia] + float32(p1)*me.UVs[2*ib] + float32(p2)*me.UVs[2*ic]
						v := float32(p0)*me.UVs[2*ia+1] + float32(p1)*me.UVs[2*ib+1] + float32(p2)*me.UVs[2*ic+1]
						var n [3]float64
						for k := range 3 {
							n[k] = p0*nrm[ia][k] + p1*nrm[ib][k] + p2*nrm[ic][k]
						}
						n = norm(n)
						vd := [3]float64{-f[0], -f[1], -f[2]}
						if dot(n, vd) < 0 { // two-sided
							n = [3]float64{-n[0], -n[1], -n[2]}
						}
						if col, res := vw.Shade(gi, u, v, n, vd); res == 2 {
							continue
						} else if res == 1 {
							zb[o] = z
							if hl {
								col.R, col.G, col.B = clamp8(float64(col.R)*0.6+40), clamp8(float64(col.G)*0.6+80), clamp8(float64(col.B)*0.6+150)
							}
							q := o * 4
							img.Pix[q], img.Pix[q+1], img.Pix[q+2], img.Pix[q+3] = col.R, col.G, col.B, 255
							continue
						}
					}
					zb[o] = z
					col := base
					if sample != nil {
						u := float32(p0)*me.UVs[2*ia] + float32(p1)*me.UVs[2*ib] + float32(p2)*me.UVs[2*ic]
						v := float32(p0)*me.UVs[2*ia+1] + float32(p1)*me.UVs[2*ib+1] + float32(p2)*me.UVs[2*ic+1]
						if t, ok := sample(gi, u, v); ok {
							col = t
						}
					}
					s := p0*shade[ia] + p1*shade[ib] + p2*shade[ic]
					rr, gg, bb := float64(col.R)*s, float64(col.G)*s, float64(col.B)*s
					if hl {
						rr, gg, bb = rr*0.6+40, gg*0.6+80, bb*0.6+150
					}
					q := o * 4
					img.Pix[q], img.Pix[q+1], img.Pix[q+2], img.Pix[q+3] = clamp8(rr), clamp8(gg), clamp8(bb), 255
				}
			}
		}
	}
	return img
}

func slotColour(i int) color.NRGBA {
	h := math.Mod(float64(i)*0.618, 1)
	// HSL(h, 0.45, 0.55) → RGB
	q := 0.55 + 0.45 - 0.55*0.45
	p := 2*0.55 - q
	ch := func(t float64) uint8 {
		t = math.Mod(t+1, 1)
		var v float64
		switch {
		case t < 1.0/6:
			v = p + (q-p)*6*t
		case t < 0.5:
			v = q
		case t < 2.0/3:
			v = p + (q-p)*(2.0/3-t)*6
		default:
			v = p
		}
		return uint8(v * 255)
	}
	return color.NRGBA{ch(h + 1.0/3), ch(h), ch(h - 1.0/3), 255}
}

func clamp8(v float64) uint8 { return uint8(math.Max(0, math.Min(255, v))) }
func dot(a, b [3]float64) float64 {
	return a[0]*b[0] + a[1]*b[1] + a[2]*b[2]
}
func cross(a, b [3]float64) [3]float64 {
	return [3]float64{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}
func norm(a [3]float64) [3]float64 {
	l := math.Sqrt(dot(a, a))
	if l == 0 {
		return [3]float64{1, 0, 0}
	}
	return [3]float64{a[0] / l, a[1] / l, a[2] / l}
}

// SampleImage samples an image at wrapped D3D texture coordinates (nearest).
func SampleImage(img *image.NRGBA, u, v float32) color.NRGBA {
	return SampleImageMode(img, u, v, false)
}

// SampleImageMode samples with wrap or mirror addressing.
func SampleImageMode(img *image.NRGBA, u, v float32, mirror bool) color.NRGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	fu := float64(u) - math.Floor(float64(u))
	fv := float64(v) - math.Floor(float64(v))
	if mirror {
		fu, fv = mirrorCoord(float64(u)), mirrorCoord(float64(v))
	}
	x := min(int(fu*float64(w)), w-1)
	y := min(int(fv*float64(h)), h-1)
	o := img.PixOffset(b.Min.X+x, b.Min.Y+y)
	return color.NRGBA{img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3]}
}

// mirrorCoord: D3D mirror addressing (… 1→0 | 0→1 | 1→0 …).
func mirrorCoord(x float64) float64 {
	t := math.Mod(math.Abs(x), 2)
	if t > 1 {
		t = 2 - t
	}
	return t
}
