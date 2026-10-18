package texture

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"owcontent/content"
	"owexplorer/internal/names"
)

func TestHalf(t *testing.T) {
	for _, c := range []struct {
		h uint16
		f float32
	}{{0x0000, 0}, {0x3C00, 1}, {0xC000, -2}, {0x7BFF, 65504}, {0x0001, 5.9604645e-8}, {0x3555, 0.33325195}} {
		if got := Half(c.h); got != c.f {
			t.Errorf("Half(%04x) = %g, want %g", c.h, got, c.f)
		}
	}
	if !math.IsInf(float64(Half(0x7C00)), 1) {
		t.Error("Half(7c00) not +Inf")
	}
}

func TestBC1Block(t *testing.T) {
	// c0 = white, c1 = black (c0 > c1: 4-colour mode); indices 0,1,2,3 in the first row.
	blk := []byte{0xFF, 0xFF, 0x00, 0x00, 0xE4, 0, 0, 0}
	px, err := LookupFormat(71).Decode(blk, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []float32{1, 0, 2.0 / 3, 1.0 / 3}
	for i, w := range want {
		if d := px[i*4] - w; d > 1e-6 || d < -1e-6 || px[i*4+3] != 1 {
			t.Errorf("pixel %d = %v, want %g", i, px[i*4:i*4+4], w)
		}
	}
}

func TestUnsplit(t *testing.T) {
	// Three BC3 blocks whose bytes are their block number × 16 + byte offset, split into planes 2 / 6 / 4 / 4.
	const n = 3
	var plain, split []byte
	for b := range n {
		for i := range 16 {
			plain = append(plain, byte(b*16+i))
		}
	}
	off := 0
	for _, p := range []int{2, 6, 4, 4} {
		for b := range n {
			split = append(split, plain[b*16+off:b*16+off+p]...)
		}
		off += p
	}
	if got := Unsplit(77, split); string(got) != string(plain) {
		t.Errorf("BC3 unsplit\n got %x\nwant %x", got, plain)
	}
	// BC5: endpoints plane (R, G per block), then indices plane (R, G).
	split = split[:0]
	for b := range n {
		split = append(split, plain[b*16:b*16+2]...)
		split = append(split, plain[b*16+8:b*16+10]...)
	}
	for b := range n {
		split = append(split, plain[b*16+2:b*16+8]...)
		split = append(split, plain[b*16+10:b*16+16]...)
	}
	if got := Unsplit(83, split); string(got) != string(plain) {
		t.Errorf("BC5 unsplit\n got %x\nwant %x", got, plain)
	}
	if got := Unsplit(95, split); &got[0] != &split[0] {
		t.Error("BC6H data must not be unsplit")
	}
}

func TestPayloadGUID(t *testing.T) {
	if g := PayloadGUID(0x0C00000F0000007E, 1); g != 0x032000010000007E {
		t.Errorf("PayloadGUID = %016X", g)
	}
	if TypeOf := content.TypeOf(PayloadGUID(0x0C00000000000001, 0)); TypeOf != TypePayload {
		t.Errorf("payload type %03X", TypeOf)
	}
}

func openInstall(t testing.TB) (*content.Store, Reader) {
	install := os.Getenv("OW_INSTALL")
	if install == "" {
		install = filepath.Join(names.FindRepo("."), "game", "owbetaclient")
	}
	if _, err := os.Stat(filepath.Join(install, ".build.info")); err != nil {
		t.Skip("no install at", install)
	}
	s, err := content.Open(install, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, func(g uint64) ([]byte, error) {
		b, err := s.Read(g)
		if errors.Is(err, content.ErrNotFound) {
			return nil, ErrMissing
		}
		return b, err
	}
}

// mipError is the mean absolute difference between the 2×2 box average of mip m and mip m+1 (surface s, RGBA 0..1;
// HDR values clamped).
func mipError(t *Texture, m, s int) (float64, error) {
	a, err := t.Decode(m, s)
	if err != nil {
		return 0, err
	}
	b, err := t.Decode(m+1, s)
	if err != nil {
		return 0, err
	}
	w0, h0 := t.Mips[m].Width, t.Mips[m].Height
	w1, h1 := t.Mips[m+1].Width, t.Mips[m+1].Height
	cl := func(v float32) float64 { return math.Min(1, math.Max(0, float64(v))) }
	var e float64
	for y := range h1 {
		for x := range w1 {
			for c := range 4 {
				var avg float64
				for dy := range 2 {
					for dx := range 2 {
						xx, yy := min(x*2+dx, w0-1), min(y*2+dy, h0-1)
						avg += cl(a[(yy*w0+xx)*4+c]) / 4
					}
				}
				e += math.Abs(avg - cl(b[(y*w1+x)*4+c]))
			}
		}
	}
	return e / float64(w1*h1*4), nil
}

// Every texture of the install loads with all its payloads and every mip; mip 0 of every surface decodes; a
// thumbnail comes from payload 0 alone; each mip
// matches the box-filtered mip above it (the check that places planes, surfaces and deltas right).
func TestAllTextures(t *testing.T) {
	s, read := openInstall(t)
	defer s.Close()
	res := s.ResourcesOfType(TypeTexture)
	if len(res) != 21696 {
		t.Errorf("%d textures, want 21,696", len(res))
	}
	t0 := time.Now()
	var mu sync.Mutex
	var failed []string
	fail := func(g uint64, err string) {
		mu.Lock()
		defer mu.Unlock()
		if len(failed) < 20 {
			failed = append(failed, err)
		}
		t.Errorf("%016X: %s", g, err)
	}
	var formats sync.Map
	var checked, bad, deltas atomic.Int64
	var worst sync.Map // format → worst mip error
	jobs := make(chan Resource, 64)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range jobs {
				tx, err := Load(read, r.GUID)
				if err != nil {
					fail(r.GUID, err.Error())
					continue
				}
				if tx.Format == nil {
					fail(r.GUID, "no decoder for "+FormatName(tx.Header.Format))
					continue
				}
				c, _ := formats.LoadOrStore(tx.Format.Name, new(atomic.Int64))
				c.(*atomic.Int64).Add(1)
				if tx.Header.Flags&FlagDelta != 0 {
					deltas.Add(1)
				}
				for _, p := range tx.Payloads {
					if !p.Present || p.Err != "" {
						fail(r.GUID, "payload "+p.Err)
					}
				}
				for m := range tx.Mips {
					if !tx.Available(m) {
						fail(r.GUID, "mip missing")
					}
				}
				for sf := range tx.Header.SurfaceCount() {
					if _, err := tx.Decode(0, sf); err != nil {
						fail(r.GUID, err.Error())
					}
				}
				if small, err := LoadSmall(read, r.GUID); err != nil {
					fail(r.GUID, "LoadSmall: "+err.Error())
				} else if img, err := small.Thumbnail(64); err != nil || img.Rect.Dx() > 64 || img.Rect.Dy() > 64 {
					fail(r.GUID, fmt.Sprintf("thumbnail: %v", err))
				}
				// Mip consistency on every 8th texture with 2+ mips: the smallest mip ≥ 16 px and the one below.
				if r.GUID%8 != 0 {
					continue
				}
				for m := len(tx.Mips) - 2; m >= 0; m-- {
					if min(tx.Mips[m+1].Width, tx.Mips[m+1].Height) < 16 {
						continue
					}
					for sf := range tx.Header.SurfaceCount() {
						e, err := mipError(tx, m, sf)
						if err != nil {
							fail(r.GUID, err.Error())
							break
						}
						checked.Add(1)
						if e > 0.2 {
							bad.Add(1)
							t.Logf("%016X %s flags %04x mip %d surface %d: error %.3f", r.GUID, tx.Format.Name, tx.Header.Flags, m, sf, e)
						}
						for {
							old, _ := worst.LoadOrStore(tx.Format.Name, e)
							if old.(float64) >= e || worst.CompareAndSwap(tx.Format.Name, old, e) {
								break
							}
						}
					}
					break
				}
			}
		}()
	}
	for _, r := range res {
		jobs <- Resource{r.GUID}
	}
	close(jobs)
	wg.Wait()
	var fs []string
	formats.Range(func(k, v any) bool {
		w := "-"
		if e, ok := worst.Load(k); ok {
			w = fmt.Sprintf("%.3f", e)
		}
		fs = append(fs, fmt.Sprintf("%-20s %6d  worst mip error %s", k, v.(*atomic.Int64).Load(), w))
		return true
	})
	sort.Strings(fs)
	t.Logf("%d textures (%d deltas) in %v; mip checks %d, above 0.2: %d", len(res), deltas.Load(), time.Since(t0), checked.Load(), bad.Load())
	for _, f := range fs {
		t.Log(f)
	}
	// Real mips differ from a box filter on high-contrast atlases (worst 0.156 on 0.8); a wrong plane or surface
	// order gives ~0.3 on most textures.
	if bad.Load()*100 > checked.Load() {
		t.Errorf("%d of %d mip checks above 0.2", bad.Load(), checked.Load())
	}
	if deltas.Load() != 272 {
		t.Errorf("%d delta textures, want 272", deltas.Load())
	}
}

// Resource is the part of content.Resource the test needs.
type Resource struct{ GUID uint64 }

// Viewer cost: load (payloads, delta) + decode + PNG of a 2048² BC3 delta texture's mip 0, and a thumbnail.
func BenchmarkViewer(b *testing.B) {
	s, read := openInstall(b)
	defer s.Close()
	const g = 0x0C00000000002E66
	b.Run("load", func(b *testing.B) {
		for b.Loop() {
			if _, err := Load(read, g); err != nil {
				b.Fatal(err)
			}
		}
	})
	t, _ := Load(read, g)
	b.Run("render+png 2048", func(b *testing.B) {
		for b.Loop() {
			img, err := t.Render(0, 0, DefaultDisplay(t.Format))
			if err != nil {
				b.Fatal(err)
			}
			PNG(img)
		}
	})
	b.Run("thumbnail", func(b *testing.B) {
		for b.Loop() {
			t, _ := LoadSmall(read, g)
			img, _ := t.Thumbnail(64)
			PNG(img)
		}
	})
}
