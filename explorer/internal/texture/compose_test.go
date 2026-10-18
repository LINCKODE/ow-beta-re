package texture

import (
	"image"
	"testing"
)

func TestMaskAndCross(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	copy(img.Pix, []uint8{10, 20, 30, 40, 50, 60, 70, 80})
	for ch, want := range map[string][]uint8{
		"rgba": {10, 20, 30, 40, 50, 60, 70, 80}, "rgb": {10, 20, 30, 255, 50, 60, 70, 255},
		"g": {20, 20, 20, 255, 60, 60, 60, 255}, "a": {40, 40, 40, 255, 80, 80, 80, 255},
	} {
		m, err := Mask(img, ch)
		if err != nil || string(m.Pix) != string(want) {
			t.Fatalf("%s: %v %v", ch, m.Pix, err)
		}
	}
	if _, err := Mask(img, "x"); err == nil {
		t.Fatal("bad channels accepted")
	}
	var faces []*image.NRGBA
	for k := range 6 {
		f := image.NewNRGBA(image.Rect(0, 0, 2, 2))
		for i := range f.Pix {
			f.Pix[i] = uint8(k + 1)
		}
		faces = append(faces, f)
	}
	c, err := Cross(faces)
	if err != nil || c.Rect.Dx() != 8 || c.Rect.Dy() != 6 {
		t.Fatalf("cross: %v %v", c.Rect, err)
	}
	at := func(x, y int) uint8 { return c.Pix[y*c.Stride+x*4] }
	// +Y (face 2 → value 3) at column 1 row 0; −Z (face 5 → 6) at column 3 row 1; empty corner
	if at(2, 0) != 3 || at(6, 2) != 6 || at(4, 2) != 1 || at(0, 0) != 0 {
		t.Fatalf("cross layout: %d %d %d %d", at(2, 0), at(6, 2), at(4, 2), at(0, 0))
	}
}
