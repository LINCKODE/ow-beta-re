package texture

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"slices"
)

// Display controls how decoded values become 8-bit pixels.
type Display struct {
	// Exposure in stops for HDR formats; with Auto the scale is chosen so the 99.5th percentile of the brightest
	// channel maps to 1 first.
	Exposure float64
	Auto     bool
	// ZFromXY fills blue with sqrt(1 - x² - y²) of the red / green channels read as a normal (two-channel formats).
	ZFromXY bool
}

// ToImage converts decoded RGBA floats to an 8-bit image. LDR formats are written as stored (sRGB formats hold sRGB
// values, UNORM formats their raw values); HDR formats are scaled, clamped and sRGB-encoded. One-channel formats
// are drawn grey.
func (f *Format) ToImage(px []float32, w, h int, d Display) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	scale := float32(1)
	if f.HDR {
		if d.Auto {
			if p := percentile(px, 0.995); p > 0 {
				scale = 1 / p
			}
		}
		scale *= float32(math.Exp2(d.Exposure))
	}
	for i := range w * h {
		r, g, b, a := px[i*4], px[i*4+1], px[i*4+2], px[i*4+3]
		if d.ZFromXY && f.Channels == 2 {
			x, y := r*2-1, g*2-1
			b = (float32(math.Sqrt(float64(max(0, 1-x*x-y*y)))) + 1) / 2
		}
		if f.Channels == 1 { // one channel: grey
			g, b = r, r
		}
		if f.HDR {
			r, g, b = srgb(r*scale), srgb(g*scale), srgb(b*scale)
			if f.Channels < 4 {
				a = 1
			}
		}
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = u8(r), u8(g), u8(b), u8(a)
	}
	return img
}

// percentile of the per-pixel maximum of r, g, b (finite values).
func percentile(px []float32, q float64) float32 {
	n := len(px) / 4
	step := max(1, n/65536)
	v := make([]float32, 0, n/step+1)
	for i := 0; i < n; i += step {
		m := max(px[i*4], px[i*4+1], px[i*4+2])
		if !math.IsNaN(float64(m)) && !math.IsInf(float64(m), 0) {
			v = append(v, m)
		}
	}
	if len(v) == 0 {
		return 0
	}
	slices.Sort(v)
	return v[min(len(v)-1, int(q*float64(len(v))))]
}

func srgb(v float32) float32 {
	if !(v > 0) {
		return 0
	}
	if v <= 0.0031308 {
		return v * 12.92
	}
	return float32(1.055*math.Pow(float64(v), 1/2.4) - 0.055)
}

func u8(v float32) uint8 {
	if !(v > 0) {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(v*255 + 0.5)
}

// DefaultDisplay: auto exposure; two-channel UNORM formats (BC5, R8G8: normal maps) with Z from XY.
func DefaultDisplay(f *Format) Display {
	return Display{Auto: true, ZFromXY: f != nil && f.Channels == 2 && !f.HDR}
}

// Downscale shrinks img so that neither side exceeds size (box filter); smaller images are returned as they are.
func Downscale(img *image.NRGBA, size int) *image.NRGBA {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if w <= size && h <= size {
		return img
	}
	f := max((w+size-1)/size, (h+size-1)/size)
	nw, nh := max(1, w/f), max(1, h/f)
	out := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	for y := range nh {
		for x := range nw {
			var s [4]int
			n := 0
			for yy := y * f; yy < min((y+1)*f, h); yy++ {
				for xx := x * f; xx < min((x+1)*f, w); xx++ {
					o := yy*img.Stride + xx*4
					for c := range 4 {
						s[c] += int(img.Pix[o+c])
					}
					n++
				}
			}
			for c := range 4 {
				out.Pix[y*out.Stride+x*4+c] = uint8(s[c] / n)
			}
		}
	}
	return out
}

// PNG encodes an image (fast compression: the explorer encodes for display and export).
func PNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Render decodes surface s of mip m, converts it for display and returns the image.
func (t *Texture) Render(m, s int, d Display) (*image.NRGBA, error) {
	px, err := t.Decode(m, s)
	if err != nil {
		return nil, err
	}
	mp := t.Mips[m]
	return t.Format.ToImage(px, mp.Width, mp.Height, d), nil
}

// Thumbnail renders the smallest available mip that is at least size on its longer side (else the largest
// available), surface 0, scaled to fit size.
func (t *Texture) Thumbnail(size int) (*image.NRGBA, error) {
	m := t.Best()
	for i := len(t.Mips) - 1; i >= 0; i-- {
		if t.Available(i) && max(t.Mips[i].Width, t.Mips[i].Height) >= size {
			m = i
			break
		}
	}
	if m < 0 {
		return nil, errNoData
	}
	img, err := t.Render(m, 0, DefaultDisplay(t.Format))
	if err != nil {
		return nil, err
	}
	return Downscale(img, size), nil
}

var errNoData = errorString("texture: no mip available")

type errorString string

func (e errorString) Error() string { return string(e) }
