package main

// Material preview tests (T071): every material through the Material tab's view and the preview resolution, the
// preview images and renders per shader family, the material thumbnails and the MCP tool material_image.

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image"
	stdpng "image/png"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"owexplorer/internal/model"
	"owexplorer/internal/names"
)

func newMaterialApp(t *testing.T) *App {
	repo := names.FindRepo(".")
	install := os.Getenv("OW_INSTALL")
	if install == "" {
		install = filepath.Join(repo, "game", "owbetaclient")
	}
	if _, err := os.Stat(filepath.Join(install, ".build.info")); err != nil {
		t.Skip("no install at", install)
	}
	a := &App{repo: repo, child: true, install: install}
	a.loadState()
	if st := a.OpenInstall(install); !st.Open {
		t.Fatalf("open: %+v", st)
	}
	return a
}

// TestMaterialViews: all 11,150 materials open in the Material tab (no null lists: before T071, 3,039 materials had
// no constant buffers or no textures, the frontend read null.length and the tab stayed empty), every preview input
// names an existing texture, and the inputs cover what the bytecode says the families bind.
func TestMaterialViews(t *testing.T) {
	a := newMaterialApp(t)
	c, _ := a.catalog()
	res := c.Store.ResourcesOfType(model.TypeMaterial)
	if len(res) != 11150 {
		t.Fatalf("%d materials", len(res))
	}
	inputs := map[string]int{}
	fam := map[uint16]int{}
	noInputs := 0
	for _, r := range res {
		v, err := a.Material(hex16(r.GUID))
		if err != nil {
			t.Errorf("%016x: %v", r.GUID, err)
			continue
		}
		j, _ := json.Marshal(v)
		var m map[string]any
		_ = json.Unmarshal(j, &m)
		for _, k := range []string{"textures", "buffers", "params"} {
			if m[k] == nil {
				t.Fatalf("%016x: %s is null", r.GUID, k)
			}
		}
		p := m["preview"].(map[string]any)
		for _, k := range []string{"inputs", "params", "unmapped", "notes"} {
			if p[k] == nil {
				t.Fatalf("%016x: preview.%s is null", r.GUID, k)
			}
		}
		fam[v.Preview.Family.ID]++
		if len(v.Preview.Inputs) == 0 {
			noInputs++
		}
		for _, in := range v.Preview.Inputs {
			inputs[in.Input]++
			if in.Format == "" {
				t.Errorf("%016x: input %s names texture %s with no format", r.GUID, in.Input, in.GUID)
			}
			if in.Conf != "D" && in.Conf != "?" {
				t.Errorf("%016x: input %s confidence %q", r.GUID, in.Input, in.Conf)
			}
		}
	}
	t.Logf("inputs %v; materials without inputs %d; families %d", inputs, noInputs, len(fam))
	// [D] every 0x21 and 0x2C material binds basecolor, basenormal and the packed spec/gloss texture
	// measured 2026-09-29: color 8190, normal 6906, roughness 6463, metalness / specular 6461, ao 5476, opacity 4141,
	// emissive 3326; 56 materials without inputs (no textures: 0x29 × 53, 0x2E × 3)
	if noInputs != 56 {
		t.Errorf("%d materials without preview inputs, want 56 (the ones without textures)", noInputs)
	}
	for in, least := range map[string]int{"color": 8409, "normal": 6906, "roughness": 6463, "metalness": 6461, "specular": 6461,
		"ao": 5476, "opacity": 4141, "emissive": 3326} {
		if inputs[in] < least {
			t.Errorf("input %s on %d materials, want ≥ %d", in, inputs[in], least)
		}
	}
	if fam[0x21] != 1801 || fam[0x2C] != 2398 || fam[0x28] != 2502 {
		t.Errorf("families: %v", fam)
	}
}

// TestMaterialPreviewImages: for two materials of every shader family, every preview input image builds, the software
// renderer draws every geometry, and the material thumbnail is a 64 px PNG.
func TestMaterialPreviewImages(t *testing.T) {
	a := newMaterialApp(t)
	c, _ := a.catalog()
	per := map[uint64]int{}
	n := 0
	for _, r := range c.Store.ResourcesOfType(model.TypeMaterial) {
		m, err := a.loadMaterial(c, r.GUID)
		if err != nil {
			t.Fatal(err)
		}
		if per[m.Shader]++; per[m.Shader] > 2 {
			continue
		}
		g := hex16(r.GUID)
		v, _ := a.Material(g)
		for _, in := range v.Preview.Inputs {
			im, err := a.previewImage(c, g, in.Input, 256, false)
			if err != nil {
				t.Errorf("%s %s: %v", g, in.Input, err)
				continue
			}
			if im.Rect.Dx() > 256 || im.Rect.Dy() > 256 || im.Rect.Dx() == 0 {
				t.Errorf("%s %s: %v", g, in.Input, im.Rect)
			}
			if _, err := a.previewPNG(c, g, in.Input, 128); err != nil {
				t.Errorf("%s %s png: %v", g, in.Input, err)
			}
		}
		for _, geom := range []string{"sphere", "plane", "cube"} {
			im, _, err := a.renderMaterial(c, r.GUID, MaterialRenderOpts{Size: 48, Geometry: geom, LightYaw: 40, LightPitch: 35, TexSize: 64, Small: true})
			if err != nil || im.Rect.Dx() != 48 {
				t.Errorf("%s %s: %v", g, geom, err)
			}
		}
		b, err := a.thumbnail(c, g)
		if err != nil {
			t.Errorf("%s thumbnail: %v", g, err)
			continue
		}
		if cfg, err := stdpng.DecodeConfig(bytes.NewReader(b)); err != nil || cfg.Width != 64 || cfg.Height != 64 {
			t.Errorf("%s thumbnail %v %+v", g, err, cfg)
		}
		n++
	}
	if n < 40 {
		t.Errorf("only %d materials tried", n)
	}
	if _, err := a.previewImage(c, "0e00000000000005", "emissive", 64, false); err == nil {
		t.Error("an input the material doesn't have must fail")
	}
}

// TestPreviewMapping: the bytecode-backed mapping on known materials (docs/Explorer.md "Material view").
func TestPreviewMapping(t *testing.T) {
	a := newMaterialApp(t)
	check := func(g string, want map[string]string) model.Preview {
		t.Helper()
		v, err := a.Material(g)
		if err != nil {
			t.Fatal(err)
		}
		for in, ch := range want {
			pi := v.Preview.Input(in)
			if ch == "" {
				if pi != nil {
					t.Errorf("%s: unexpected %s %+v", g, in, pi)
				}
				continue
			}
			if pi == nil || pi.Slot+"/"+pi.Channel != ch {
				t.Errorf("%s: %s = %+v, want %s", g, in, pi, ch)
			}
		}
		return v.Preview
	}
	// 0x21: basecolor, basenormal, packed spec/gloss, baseao on UV1 (off by default)
	p := check("0e0000000000007a", map[string]string{"color": "ad10f602/rgb", "normal": "169615aa/normal",
		"roughness": "20af06ce/gloss", "metalness": "20af06ce/metal", "specular": "20af06ce/spec", "ao": "c6d17199/r"})
	if ao := p.Input("ao"); ao.UVSet != 1 || ao.On {
		t.Errorf("0x21 baseao: %+v", ao)
	}
	// 0x2C: basecolor.a is AO
	check("0e0000000000007c", map[string]string{"color": "ad10f602/rgb", "ao": "ad10f602/a", "emissive": "bcbe747d/emissive"})
	// 0x22 decal: color + opacity.r
	check("0e00000000000074", map[string]string{"color": "665648e9/rgb", "opacity": "43fd6d66/r"})
	// 0x28 effect: the BC4 texture_N mask, no colour texture (the HDR lifetime curve and the gradient stay unmapped)
	check("0e00000000172add", map[string]string{"opacity": "d74c2428/r", "color": ""})
}

// TestNormalDecode: the inverse stereographic decode (flat → +Z, B flattens).
func TestNormalDecode(t *testing.T) {
	px := []float32{128.0 / 255, 128.0 / 255, 0, 1, 1, 0.5, 0, 1, 1, 0.5, 1, 1}
	disp := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	out := convertInput(disp, px, nil, model.PreviewInput{Channel: "normal"}, nil, nil)
	if p := out.Pix[0:3]; p[0] != 128 || p[1] != 128 || p[2] != 255 {
		t.Errorf("flat: %v", p)
	}
	// x = 1 → n = (2/2, 0, 0): fully tilted; with B = 1 the slope is divided by ~33
	if p := out.Pix[4:7]; p[0] != 255 || p[2] != 128 {
		t.Errorf("tilted: %v", p)
	}
	if p := out.Pix[8:11]; p[0] > 140 || p[2] < 250 { // x = 0.034 → n.x = 0.067
		t.Errorf("flattened: %v", p)
	}
}

// TestMaterialImage: the MCP tool on a hero material, a decal (plane by default) and the error cases.
func TestMaterialImage(t *testing.T) {
	h := newHarness(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	call := func(args map[string]any) ([]byte, map[string]any, string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "material_image", "arguments": args}})
		req, _ := http.NewRequest(http.MethodPost, h.url, strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var resp struct {
			Result struct {
				Content []struct{ Type, Text, Data, MimeType string } `json:"content"`
				IsError bool                                          `json:"isError"`
			} `json:"result"`
		}
		_ = json.NewDecoder(r.Body).Decode(&resp)
		cs := resp.Result.Content
		if resp.Result.IsError {
			return nil, nil, cs[0].Text
		}
		if len(cs) != 2 || cs[0].Type != "image" || cs[1].Type != "text" {
			t.Fatalf("content parts: %+v", cs)
		}
		png, _ := base64.StdEncoding.DecodeString(cs[0].Data)
		var info map[string]any
		_ = json.Unmarshal([]byte(cs[1].Text), &info)
		return png, info, ""
	}
	png, info, e := call(map[string]any{"guid": "0e0000000000007c", "size": 200})
	if e != "" {
		t.Fatal(e)
	}
	cfg, err := stdpng.DecodeConfig(bytes.NewReader(png))
	if err != nil || cfg.Width != 200 || info["geometry"] != "sphere" {
		t.Fatalf("%v %+v", err, info)
	}
	if b, err := os.ReadFile(info["path"].(string)); err != nil || !bytes.Equal(b, png) {
		t.Errorf("saved file differs: %v", err)
	}
	if _, info, e = call(map[string]any{"guid": "0e00000000000074", "inputs": []string{"color", "opacity"}, "save": false}); e != "" ||
		info["geometry"] != "plane" || info["path"] != nil {
		t.Errorf("decal: %s %+v", e, info)
	}
	if _, _, e = call(map[string]any{"guid": "0d000000000011ce"}); !strings.Contains(e, "not a material") {
		t.Errorf("model: %q", e)
	}
	if _, _, e = call(map[string]any{"guid": "0e0000000000007c", "geometry": "torus"}); !strings.Contains(e, "sphere, plane or cube") {
		t.Errorf("geometry: %q", e)
	}
	if _, _, e = call(map[string]any{"guid": "0e0000000000007c", "inputs": []string{"gloss"}}); !strings.Contains(e, "unknown input") {
		t.Errorf("inputs: %q", e)
	}
}

// TestMaterialRound2 (T071 round 2): bulk previews, the packed "orm" image, the 3D map's per-slot materials (Anubis)
// and model_image's PBR mode.
func TestMaterialRound2(t *testing.T) {
	a := newMaterialApp(t)
	c, _ := a.catalog()
	pv, err := a.MaterialPreviews([]string{"0e0000000000007a", "0e0000000000007c", "nonsense", "0d000000000011ce"})
	if err != nil || len(pv) != 2 || pv["0e0000000000007c"].Family.ID != 0x2C {
		t.Fatalf("previews: %v %d", err, len(pv))
	}
	im, err := a.previewImage(c, "0e0000000000007c", "orm", 128, false)
	if err != nil || im.Rect.Dx() == 0 || im.Rect.Dx() > 128 {
		t.Fatalf("orm: %v", err)
	}
	if _, err := a.previewImage(c, "0e00000000172add", "orm", 128, false); err == nil {
		t.Error("an effect material has no orm input")
	}
	v, err := a.MapVisual("080000000000005b") // Anubis
	if err != nil {
		t.Fatal(err)
	}
	slots, withMat := 0, 0
	for _, b := range v.Batches {
		if b.Error != "" {
			continue
		}
		if len(b.Materials) != len(b.Colors) {
			t.Fatalf("batch %s: %d materials, %d slots", b.Model, len(b.Materials), len(b.Colors))
		}
		for k, m := range b.Materials {
			slots++
			if m != "" {
				withMat++
			} else if b.Colors[k] != "" {
				t.Errorf("batch %s slot %d: a colour but no material", b.Model, k)
			}
		}
	}
	t.Logf("Anubis: %d slots, %d with a material", slots, withMat)
	if withMat < slots/2 {
		t.Errorf("only %d of %d slots have a material", withMat, slots)
	}
}

func TestModelImagePBR(t *testing.T) {
	h := newHarness(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "model_image", "arguments": map[string]any{"guid": "0d000000000011ce", "size": 160, "pbr": true, "save": false}}})
	req, _ := http.NewRequest(http.MethodPost, h.url, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var resp struct {
		Result struct {
			Content []struct{ Type, Text, Data string } `json:"content"`
			IsError bool                                `json:"isError"`
		} `json:"result"`
	}
	_ = json.NewDecoder(r.Body).Decode(&resp)
	cs := resp.Result.Content
	if resp.Result.IsError || len(cs) != 2 || !strings.Contains(cs[1].Text, `"pbr":true`) {
		t.Fatalf("%+v", resp.Result)
	}
	png, _ := base64.StdEncoding.DecodeString(cs[0].Data)
	if cfg, err := stdpng.DecodeConfig(bytes.NewReader(png)); err != nil || cfg.Width != 160 {
		t.Errorf("%v %+v", err, cfg)
	}
}

// TestMapEnvironment (T071 round 3): Anubis's environment block (sun, sky, ambient, LUT), the sky panorama's
// orientation (the upper quarter is blue-dominant) and the LUT layout (close to identity with red across a slice,
// green down it, blue across slices; far from it with green flipped).
func TestMapEnvironment(t *testing.T) {
	a := newMaterialApp(t)
	c, _ := a.catalog()
	e, err := a.MapEnvironment("080000000000005b")
	if err != nil {
		t.Fatal(err)
	}
	if e.Sky != "0c00000000000ad1" || e.Ambient != "0c0000000000087f" || e.LUT != "0c000000000008ea" ||
		math.Abs(float64(e.SunDir[1])+0.7477) > 1e-3 || math.Abs(float64(e.SunColor[0])-3.2911) > 1e-3 {
		t.Fatalf("%+v", e)
	}
	b, err := a.envmapBinary(e.Sky, 128)
	if err != nil || string(b[:4]) != "OWEQ" || len(b) != 12+128*64*16 {
		t.Fatalf("envmap: %v %d", err, len(b))
	}
	var r, bl float64
	for j := 48; j < 64; j++ {
		for i := 0; i < 128; i++ {
			o := 12 + ((j*128+i)*4)*4
			r += float64(math.Float32frombits(binary.LittleEndian.Uint32(b[o:])))
			bl += float64(math.Float32frombits(binary.LittleEndian.Uint32(b[o+8:])))
		}
	}
	if bl < 2*r {
		t.Errorf("sky upper quarter not blue: r %.1f b %.1f", r, bl)
	}
	g, _ := parse(e.LUT)
	tx, err := a.loadTexture(c, g)
	if err != nil {
		t.Fatal(err)
	}
	px, _ := tx.Decode(0, 0)
	w := tx.Mips[0].Width
	rms := func(flip bool) float64 {
		var s float64
		for bb := 0; bb < 32; bb++ {
			for y := 0; y < 32; y++ {
				for x := 0; x < 32; x++ {
					o := (y*w + bb*32 + x) * 4
					gg := float64(y) / 31
					if flip {
						gg = 1 - gg
					}
					d0, d1, d2 := float64(px[o])-float64(x)/31, float64(px[o+1])-gg, float64(px[o+2])-float64(bb)/31
					s += d0*d0 + d1*d1 + d2*d2
				}
			}
		}
		return math.Sqrt(s / 32768 / 3)
	}
	if a, b := rms(false), rms(true); a > 0.1 || b < 0.3 {
		t.Errorf("LUT layout: rms %.3f (flipped %.3f)", a, b)
	}
}

// TestMapProbes (T071 round 5): Hanamura's 98 reflection probes + the map-wide capture, their layout values, and the
// atlas binary (3 blur levels, half floats).
func TestMapProbes(t *testing.T) {
	a := newMaterialApp(t)
	v, err := a.MapProbes("0800000000000165")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Probes) != 99 || v.Probes[98].Record != -1 || v.Probes[0].Record != 552 ||
		math.Abs(float64(v.Probes[0].Extents[0])-10.2814) > 1e-3 || v.Probes[0].HDR != "0c00000000001562" {
		t.Fatalf("%d probes, first %+v", len(v.Probes), v.Probes[0])
	}
	for _, p := range v.Probes {
		if p.Exposure <= 1 { // the captures are stored ~50× darker than the ambient cube (MapProbes)
			t.Fatalf("probe %d exposure %v", p.Record, p.Exposure)
		}
	}
	b, err := a.probesBinary("0800000000000165")
	if err != nil || string(b[:4]) != "OWPR" || binary.LittleEndian.Uint32(b[4:]) != 99 {
		t.Fatalf("binary: %v", err)
	}
	want := 12
	for _, w := range v.Levels {
		want += 4 + w*(w/2)*99*8
	}
	if len(b) != want {
		t.Errorf("binary %d bytes, want %d", len(b), want)
	}
}
