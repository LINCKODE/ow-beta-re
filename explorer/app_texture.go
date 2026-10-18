package main

import (
	"container/list"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"owcontent/content"
	"owexplorer/internal/catalog"
	"owexplorer/internal/model"
	"owexplorer/internal/texture"
)

// Textures (T063): the Texture tab's data (App.Texture), images served over the asset server's handler
// (/tex/…, /thumb/…: the webview loads them as <img>, no base64 through the bindings), PNG export.

// lru is a small least-recently-used cache.
type lru[V any] struct {
	mu    sync.Mutex
	max   int
	cost  func(V) int
	total int
	ll    *list.List
	m     map[string]*list.Element
}

type lruEntry[V any] struct {
	k string
	v V
}

func newLRU[V any](max int, cost func(V) int) *lru[V] {
	return &lru[V]{max: max, cost: cost, ll: list.New(), m: map[string]*list.Element{}}
}

func (c *lru[V]) get(k string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[k]; ok {
		c.ll.MoveToFront(e)
		return e.Value.(*lruEntry[V]).v, true
	}
	var z V
	return z, false
}

func (c *lru[V]) put(k string, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[k]; ok {
		c.total -= c.cost(e.Value.(*lruEntry[V]).v)
		c.ll.Remove(e)
	}
	c.m[k] = c.ll.PushFront(&lruEntry[V]{k, v})
	c.total += c.cost(v)
	for c.total > c.max && c.ll.Len() > 1 {
		e := c.ll.Back()
		c.total -= c.cost(e.Value.(*lruEntry[V]).v)
		delete(c.m, e.Value.(*lruEntry[V]).k)
		c.ll.Remove(e)
	}
}

func textureBytes(t *texture.Texture) int {
	n := 1
	for _, m := range t.Mips {
		for _, s := range m.Data {
			n += len(s)
		}
	}
	return n
}

var (
	texCache   = newLRU[*texture.Texture](256<<20, textureBytes)              // loaded textures (payload bytes)
	thumbCache = newLRU[[]byte](48<<20, func(b []byte) int { return len(b) }) // thumbnail PNGs
	thumbSem   = make(chan struct{}, 6)                                       // concurrent thumbnail decodes
)

func (a *App) texReader(c *catalog.Catalog) texture.Reader {
	return func(g uint64) ([]byte, error) {
		b, err := c.Store.Read(g)
		if errors.Is(err, content.ErrNotFound) {
			return nil, texture.ErrMissing
		}
		return b, err
	}
}

// textureOf is the texture a GUID shows: a texture itself, or for a payload (04D) the texture with the same index.
func textureOf(g uint64) uint64 {
	if content.TypeOf(g) == texture.TypePayload {
		return 0x0C00000000000000 | g&0xFFFFFFFF
	}
	return g
}

func (a *App) loadTexture(c *catalog.Catalog, g uint64) (*texture.Texture, error) {
	key := fmt.Sprintf("%s/%016x", c.Describe, g)
	if t, ok := texCache.get(key); ok {
		return t, nil
	}
	t, err := texture.Load(a.texReader(c), g)
	if err != nil {
		return nil, err
	}
	texCache.put(key, t)
	return t, nil
}

// TextureView is the Texture tab.
type TextureView struct {
	GUID      string       `json:"guid"`    // the texture shown
	Payload   string       `json:"payload"` // set when a payload (04D) was opened: its GUID
	Format    string       `json:"format"`
	DXGI      int          `json:"dxgi"`
	Decoder   bool         `json:"decoder"`
	HDR       bool         `json:"hdr"`
	Channels  int          `json:"channels"`
	SRGB      bool         `json:"srgb"`
	Width     int          `json:"width"`
	Height    int          `json:"height"`
	Surfaces  int          `json:"surfaces"`
	Cube      bool         `json:"cube"`
	Flags     int          `json:"flags"`
	FlagNames []string     `json:"flagNames"`
	Unk5      int          `json:"unk5"`
	Unk10     string       `json:"unk10"`
	Base      string       `json:"base,omitempty"` // delta textures: the base texture
	Inline    bool         `json:"inline"`
	DataSize  int          `json:"dataSize"`
	Mips      []TexMip     `json:"mips"`
	Payloads  []TexPayload `json:"payloads"`
	Variants  []string     `json:"variants"` // other textures sharing the index (e.g. the low-detail 0x000F variant)
	Error     string       `json:"error,omitempty"`
	Header    TexHeaderHex `json:"header"`
}

type TexMip struct {
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Bytes     int    `json:"bytes"` // one surface
	Available bool   `json:"available"`
	Source    string `json:"source"` // payload GUID, "" = inline
}

type TexPayload struct {
	GUID    string `json:"guid"`
	Present bool   `json:"present"`
	First   int    `json:"first"`
	Count   int    `json:"count"`
	Top     int    `json:"top"`
	Size    int    `json:"size"`
	Error   string `json:"error,omitempty"`
}

type TexHeaderHex struct {
	Bytes string `json:"bytes"` // the 32 header bytes, hex
}

var flagNames = []struct {
	bit  uint16
	name string
}{{0x0001, "0x0001 ?"}, {0x0002, "2D"}, {0x0008, "cube"}, {0x0010, "0x0010 ?"}, {0x0020, "sRGB"}, {0x0040, "array"},
	{0x0100, "0x0100 ?"}, {0x0400, "0x0400 ?"}, {0x1000, "split blocks"}, {0x2000, "delta"}}

// Texture describes a texture (or, for a payload, the texture it belongs to) for the Texture tab.
func (a *App) Texture(guid string) (TextureView, error) {
	c, err := a.catalog()
	if err != nil {
		return TextureView{}, err
	}
	g, err := parse(guid)
	if err != nil {
		return TextureView{}, err
	}
	tg := textureOf(g)
	v := TextureView{GUID: fmt.Sprintf("%016x", tg), Mips: []TexMip{}, Payloads: []TexPayload{}, Variants: []string{}}
	if tg != g {
		v.Payload = fmt.Sprintf("%016x", g)
	}
	for _, r := range c.Store.ResourcesOfType(texture.TypeTexture) {
		if r.GUID&0xFFFFFFFF == tg&0xFFFFFFFF && r.GUID != tg {
			v.Variants = append(v.Variants, fmt.Sprintf("%016x", r.GUID))
		}
	}
	t, err := a.loadTexture(c, tg)
	if err != nil {
		v.Error = err.Error()
		return v, nil
	}
	if b, err := c.Store.Read(tg); err == nil {
		v.Header.Bytes = fmt.Sprintf("%x", b[:min(len(b), texture.HeaderSize)])
	}
	h := t.Header
	v.Format, v.DXGI, v.Decoder = texture.FormatName(h.Format), int(h.Format), t.Format != nil
	if f := t.Format; f != nil {
		v.HDR, v.Channels, v.SRGB = f.HDR, f.Channels, f.SRGB
	}
	v.Width, v.Height, v.Surfaces = int(h.Width), int(h.Height), h.SurfaceCount()
	v.Cube = h.Flags&texture.FlagCube != 0
	v.Flags, v.Unk5, v.Unk10 = int(h.Flags), int(h.Unk5), fmt.Sprintf("%016x", h.Unk10)
	for _, f := range flagNames {
		if h.Flags&f.bit != 0 {
			v.FlagNames = append(v.FlagNames, f.name)
		}
	}
	if h.Flags&texture.FlagDelta != 0 {
		v.Base = fmt.Sprintf("%016x", h.Base)
	}
	v.Inline, v.DataSize = t.Inline, int(h.DataSize)
	for _, m := range t.Mips {
		tm := TexMip{Width: m.Width, Height: m.Height, Available: m.Data != nil}
		if t.Format != nil {
			tm.Bytes = t.Format.MipBytes(m.Width, m.Height)
		}
		if m.Source != 0 {
			tm.Source = fmt.Sprintf("%016x", m.Source)
		}
		v.Mips = append(v.Mips, tm)
	}
	for _, p := range t.Payloads {
		v.Payloads = append(v.Payloads, TexPayload{GUID: fmt.Sprintf("%016x", p.GUID), Present: p.Present, First: p.First,
			Count: int(p.Header.Count), Top: int(p.Header.Top), Size: int(p.Header.Size), Error: p.Err})
	}
	return v, nil
}

// texDisplay reads the display options of an image request.
func texDisplay(q map[string][]string) texture.Display {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	exp, _ := strconv.ParseFloat(get("exp"), 64)
	return texture.Display{Exposure: exp, Auto: get("auto") != "0", ZFromXY: get("z") == "1"}
}

// imageHandler serves the asset server's requests that aren't files of the frontend:
//
//	/tex/<guid>/<mip>/<surface>.png?exp=<stops>&auto=0|1&z=0|1   one surface of one mip
//	/thumb/<guid>.png                                           a 64 px thumbnail (textures and payloads)
//	/mapgeo/<guid>.bin                                          a map's collision mesh (3D map mode, T066)
//	/model/<guid>.bin?lod=<mask>                                a model's mesh (Model tab, T064)
//	/mtex/<guid>.png?max=<px>                                   a material texture for the 3D preview (T064)
//	/mprev/<material>/<input>.png?max=<px>                      one input of a material's preview (T071)
//	/thumb/<material>.png                                       a 64 px material preview sphere (T071)
//	/probes/<map>.bin                                           a map's reflection probe panoramas, 3 blur levels (T071)
//	/envmap/<texture>.bin?w=<px>                                a cube map as an HDR equirectangular panorama (T071)
type imageHandler struct{ a *App }

func (h imageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a := h.a
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	c, err := a.catalog()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if len(parts) == 2 && parts[0] == "mapgeo" { // the 3D map mode's collision mesh (mapview.MeshBinary)
		b, err := a.mapMesh(strings.TrimSuffix(parts[1], ".bin"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(b)
		return
	}
	if len(parts) == 2 && parts[0] == "audio" { // a WEM as WAV / Ogg (app_audio.go, T065)
		a.serveAudio(w, r, c, parts[1])
		return
	}
	if len(parts) == 2 && parts[0] == "probes" { // a map's reflection probe atlases (app_env.go, T071)
		b, err := a.probesBinary(strings.TrimSuffix(parts[1], ".bin"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(b)
		return
	}
	if len(parts) == 2 && parts[0] == "envmap" { // a cube map as an HDR panorama (app_env.go, T071)
		size, _ := strconv.Atoi(r.URL.Query().Get("w"))
		b, err := a.envmapBinary(strings.TrimSuffix(parts[1], ".bin"), size)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(b)
		return
	}
	if len(parts) == 2 && parts[0] == "model" { // a model's mesh at a LOD (app_model.go meshBinary, T064)
		lod, _ := strconv.Atoi(r.URL.Query().Get("lod"))
		b, err := a.meshBinary(c, strings.TrimSuffix(parts[1], ".bin"), lod, r.URL.Query().Get("proxy") == "1")
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(b)
		return
	}
	var png []byte
	switch {
	case len(parts) == 3 && parts[0] == "mprev": // a material preview input (app_material.go, T071)
		mx, _ := strconv.Atoi(r.URL.Query().Get("max"))
		png, err = a.previewPNG(c, parts[1], strings.TrimSuffix(parts[2], ".png"), mx)
	case len(parts) == 2 && parts[0] == "mtex":
		mx, _ := strconv.Atoi(r.URL.Query().Get("max"))
		png, err = a.materialTexture(c, strings.TrimSuffix(parts[1], ".png"), mx)
	case len(parts) == 2 && parts[0] == "thumb":
		png, err = a.thumbnail(c, strings.TrimSuffix(parts[1], ".png"))
	case len(parts) == 4 && parts[0] == "tex":
		png, err = a.textureImage(c, parts[1], parts[2], strings.TrimSuffix(parts[3], ".png"), texDisplay(r.URL.Query()))
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "max-age=3600")
	w.Write(png)
}

func (a *App) thumbnail(c *catalog.Catalog, guid string) ([]byte, error) {
	g, err := parse(guid)
	if err != nil {
		return nil, err
	}
	if content.TypeOf(g) == model.TypeMaterial { // a rendered sphere (app_material.go, T071)
		return a.materialThumbnail(c, g)
	}
	g = textureOf(g)
	key := fmt.Sprintf("%s/%016x", c.Describe, g)
	if b, ok := thumbCache.get(key); ok {
		return b, nil
	}
	thumbSem <- struct{}{}
	defer func() { <-thumbSem }()
	// Thumbnails don't go through texCache: they would push the viewed textures out.
	t, err := texture.LoadSmall(a.texReader(c), g)
	if err != nil {
		return nil, err
	}
	img, err := t.Thumbnail(64)
	if err != nil {
		return nil, err
	}
	b, err := texture.PNG(img)
	if err != nil {
		return nil, err
	}
	thumbCache.put(key, b)
	return b, nil
}

func (a *App) renderTexture(c *catalog.Catalog, guid string, mip, surface int, d texture.Display) (*texture.Texture, []byte, error) {
	g, err := parse(guid)
	if err != nil {
		return nil, nil, err
	}
	t, err := a.loadTexture(c, textureOf(g))
	if err != nil {
		return nil, nil, err
	}
	img, err := t.Render(mip, surface, d)
	if err != nil {
		return t, nil, err
	}
	b, err := texture.PNG(img)
	return t, b, err
}

func (a *App) textureImage(c *catalog.Catalog, guid, mip, surface string, d texture.Display) ([]byte, error) {
	m, err1 := strconv.Atoi(mip)
	s, err2 := strconv.Atoi(surface)
	if err1 != nil || err2 != nil {
		return nil, fmt.Errorf("bad mip / surface")
	}
	_, b, err := a.renderTexture(c, guid, m, s, d)
	return b, err
}

// TexDisplay is the display state the viewer passes to exports.
type TexDisplay struct {
	Exposure float64 `json:"exposure"`
	Auto     bool    `json:"auto"`
	ZFromXY  bool    `json:"zFromXY"`
}

func (d TexDisplay) display() texture.Display {
	return texture.Display{Exposure: d.Exposure, Auto: d.Auto, ZFromXY: d.ZFromXY}
}

// ExportTexturePNG saves one surface of one mip as PNG (as displayed: HDR exposure, Z from XY).
func (a *App) ExportTexturePNG(guid string, mip, surface int, d TexDisplay) (string, error) {
	c, err := a.catalog()
	if err != nil {
		return "", err
	}
	g, err := parse(guid)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%016x.png", textureOf(g))
	if mip > 0 || surface > 0 {
		name = fmt.Sprintf("%016x_m%d_s%d.png", textureOf(g), mip, surface)
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "Export " + name, DefaultFilename: name,
		Filters: []runtime.FileFilter{{DisplayName: "PNG", Pattern: "*.png"}}})
	if err != nil || path == "" {
		return "", err
	}
	_, b, err := a.renderTexture(c, guid, mip, surface, d.display())
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, b, 0o644)
}

// ExportTextureAll saves every available mip of every surface as PNG into a chosen directory.
func (a *App) ExportTextureAll(guid string, d TexDisplay) (string, error) {
	c, err := a.catalog()
	if err != nil {
		return "", err
	}
	g, err := parse(guid)
	if err != nil {
		return "", err
	}
	g = textureOf(g)
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "Export every mip and surface of " +
		fmt.Sprintf("%016x", g), CanCreateDirectories: true})
	if err != nil || dir == "" {
		return "", err
	}
	t, err := a.loadTexture(c, g)
	if err != nil {
		return "", err
	}
	n := 0
	for m := range t.Mips {
		if !t.Available(m) {
			continue
		}
		for s := range t.Header.SurfaceCount() {
			img, err := t.Render(m, s, d.display())
			if err != nil {
				return "", err
			}
			b, err := texture.PNG(img)
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%016x_m%d_s%d.png", g, m, s)), b, 0o644); err != nil {
				return "", err
			}
			n++
		}
	}
	return fmt.Sprintf("%d PNGs in %s", n, dir), nil
}

// textureStatus is the Info tab's decoder line for textures and payloads.
func (a *App) textureStatus(c *catalog.Catalog, g uint64) DecoderStatus {
	st := DecoderStatus{Name: "texture (docs/AssetFormats.md §1)"}
	t, err := a.loadTexture(c, textureOf(g))
	if err != nil {
		st.Status, st.Detail = "error", err.Error()
		return st
	}
	h := t.Header
	var bad []string
	for _, p := range t.Payloads {
		if !p.Present {
			bad = append(bad, fmt.Sprintf("payload %016x missing", p.GUID))
		} else if p.Err != "" {
			bad = append(bad, p.Err)
		}
	}
	where := "inline"
	if !t.Inline {
		where = fmt.Sprintf("%d payloads", len(t.Payloads))
	}
	st.Detail = fmt.Sprintf("%s %d×%d, %d mips, %d surfaces, %s", texture.FormatName(h.Format), h.Width, h.Height,
		len(t.Mips), h.SurfaceCount(), where)
	if textureOf(g) != g {
		st.Detail = fmt.Sprintf("payload of %016x: ", textureOf(g)) + st.Detail
	}
	switch {
	case t.Format == nil:
		st.Status = "error"
		st.Detail += "; no decoder for the format"
	case len(bad) > 0:
		st.Status = "error"
		st.Detail += "; " + strings.Join(bad, "; ")
	default:
		st.Status = "ok"
		st.Detail += "; see the Texture tab"
	}
	return st
}
