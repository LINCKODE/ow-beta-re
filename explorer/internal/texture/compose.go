package texture

import (
	"fmt"
	"image"
)

// Mask keeps the channels of an image the viewer's channel buttons show: "rgba" (as is), "rgb" (opaque), or one
// channel "r", "g", "b", "a" as grey (opaque).
func Mask(img *image.NRGBA, channels string) (*image.NRGBA, error) {
	ch := map[string]int{"r": 0, "g": 1, "b": 2, "a": 3}
	switch channels {
	case "", "rgba":
		return img, nil
	case "rgb", "r", "g", "b", "a":
	default:
		return nil, fmt.Errorf("channels: rgba, rgb, r, g, b or a")
	}
	out := image.NewNRGBA(img.Rect)
	for i := 0; i+3 < len(img.Pix); i += 4 {
		if channels == "rgb" {
			copy(out.Pix[i:i+3], img.Pix[i:i+3])
		} else {
			v := img.Pix[i+ch[channels]]
			out.Pix[i], out.Pix[i+1], out.Pix[i+2] = v, v, v
		}
		out.Pix[i+3] = 255
	}
	return out, nil
}

// CrossLayout places the six faces of a cube (D3D order +X −X +Y −Y +Z −Z) on a 4 × 3 grid: column, row. The same
// cross as the texture viewer (TextureView.vue CROSS): +Y above, −X +Z +X −Z across, −Y below.
var CrossLayout = [6][2]int{{2, 1}, {0, 1}, {1, 0}, {1, 2}, {1, 1}, {3, 1}}

// Cross unfolds six equally sized faces into one image (transparent outside the faces).
func Cross(faces []*image.NRGBA) (*image.NRGBA, error) {
	if len(faces) != 6 {
		return nil, fmt.Errorf("cross: %d faces, want 6", len(faces))
	}
	w, h := faces[0].Rect.Dx(), faces[0].Rect.Dy()
	out := image.NewNRGBA(image.Rect(0, 0, 4*w, 3*h))
	for k, f := range faces {
		if f.Rect.Dx() != w || f.Rect.Dy() != h {
			return nil, fmt.Errorf("cross: face %d is %dx%d, face 0 %dx%d", k, f.Rect.Dx(), f.Rect.Dy(), w, h)
		}
		x0, y0 := CrossLayout[k][0]*w, CrossLayout[k][1]*h
		for y := range h {
			copy(out.Pix[(y0+y)*out.Stride+x0*4:(y0+y)*out.Stride+(x0+w)*4], f.Pix[y*f.Stride:y*f.Stride+w*4])
		}
	}
	return out, nil
}
