package main

// MCP tool texture_image (docs/Explorer.md "MCP server"): a texture decoded by T063's decoder (internal/texture),
// returned as MCP image content (PNG) and saved to a file whose path is in a text part, for clients that don't show
// MCP images.

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"

	"owcontent/content"
	"owexplorer/internal/mcp"
	"owexplorer/internal/texture"
)

type textureImageArgs struct {
	GUID     string   `json:"guid"`
	Mip      *int     `json:"mip"`
	Surface  *int     `json:"surface"`
	Cross    bool     `json:"cross"`
	Channels string   `json:"channels"`
	MaxSize  int      `json:"max_size"`
	Exposure *float64 `json:"exposure"`
	ZFromXY  *bool    `json:"z_from_xy"`
	Save     *bool    `json:"save"`
}

func textureImageTool(a *App) mcp.Tool {
	return tool("texture_image", "A texture (0x004; a payload 0x04D shows its texture) decoded to PNG and returned as an "+
		"image, and saved to a file (path in the text part) for clients that don't show MCP images. Delta textures "+
		"come out composed with their base. Default: the smallest available mip that fills max_size, surface 0.",
		obj(map[string]any{"guid": pGUID,
			"mip":       pInt("mip (0 = largest); default: the smallest available mip ≥ max_size"),
			"surface":   pInt("surface / cube face (D3D order +X −X +Y −Y +Z −Z; cube n: 6n + face), default 0"),
			"cross":     map[string]any{"type": "boolean", "description": "cube textures: the six faces of the surface's cube unfolded as a cross"},
			"channels":  pEnum("default rgba; one channel is shown as grey", "rgba", "rgb", "r", "g", "b", "a"),
			"max_size":  pInt("longest side of the returned image, default 512 (downscaled in the explorer)"),
			"exposure":  map[string]any{"type": "number", "description": "HDR formats: exposure in stops (default: auto, the 99.5th percentile maps to 1)"},
			"z_from_xy": map[string]any{"type": "boolean", "description": "two-channel normal maps: blue = sqrt(1 − x² − y²) (default on for them)"},
			"save":      map[string]any{"type": "boolean", "description": "write the PNG file (default true)"}}, "guid"),
		typed(a.toolTextureImage))
}

func (a *App) toolTextureImage(ctx context.Context, q textureImageArgs) (any, error) {
	c, g, err := a.guidArg(q.GUID)
	if err != nil {
		return nil, err
	}
	tg := textureOf(g)
	if content.TypeOf(tg) != texture.TypeTexture || !c.Exists(tg) {
		return nil, fmt.Errorf("%016x is not a texture (0x004) or texture payload (0x04D) of this install", g)
	}
	t, err := a.loadTexture(c, tg)
	if err != nil {
		return nil, err
	}
	if t.Format == nil {
		return nil, fmt.Errorf("texture %016x: format %s has no decoder", tg, texture.FormatName(t.Header.Format))
	}
	maxSize := cmp0(q.MaxSize, 512)
	if maxSize > 4096 {
		return nil, fmt.Errorf("max_size: at most 4096")
	}
	mip := -1
	if q.Mip != nil {
		mip = *q.Mip
		if !t.Available(mip) {
			return nil, fmt.Errorf("mip %d is not available (%d mips; available: %v)", mip, len(t.Mips), availableMips(t))
		}
	} else {
		mip = t.Best()
		for i := len(t.Mips) - 1; i >= 0; i-- {
			if t.Available(i) && max(t.Mips[i].Width, t.Mips[i].Height) >= maxSize {
				mip = i
				break
			}
		}
		if mip < 0 {
			return nil, fmt.Errorf("texture %016x: no mip available", tg)
		}
	}
	surfaces := t.Header.SurfaceCount()
	surface := 0
	if q.Surface != nil {
		surface = *q.Surface
	}
	if surface < 0 || surface >= surfaces {
		return nil, fmt.Errorf("surface %d out of range (%d surfaces)", surface, surfaces)
	}
	cube := t.Header.Flags&texture.FlagCube != 0
	if q.Cross && (!cube || surfaces < 6) {
		return nil, fmt.Errorf("cross: not a cube texture")
	}
	d := texture.DefaultDisplay(t.Format)
	if q.Exposure != nil {
		d.Auto, d.Exposure = false, *q.Exposure
	}
	if q.ZFromXY != nil {
		d.ZFromXY = *q.ZFromXY
	}
	var img *image.NRGBA
	if q.Cross {
		first := surface / 6 * 6
		var faces []*image.NRGBA
		for f := first; f < first+6; f++ {
			fi, err := t.Render(mip, f, d)
			if err != nil {
				return nil, fmt.Errorf("face %d: %w", f, err)
			}
			faces = append(faces, fi)
		}
		if img, err = texture.Cross(faces); err != nil {
			return nil, err
		}
	} else if img, err = t.Render(mip, surface, d); err != nil {
		return nil, err
	}
	if img, err = texture.Mask(img, q.Channels); err != nil {
		return nil, err
	}
	img = texture.Downscale(img, maxSize)
	png, err := texture.PNG(img)
	if err != nil {
		return nil, err
	}
	info := map[string]any{"texture": hexg(tg), "format": t.Format.Name, "mip": mip, "mipSize": fmt.Sprintf("%d×%d", t.Mips[mip].Width, t.Mips[mip].Height),
		"image": fmt.Sprintf("%d×%d", img.Rect.Dx(), img.Rect.Dy()), "mips": len(t.Mips), "surfaces": surfaces, "cube": cube,
		"hdr": t.Format.HDR, "channels": map[bool]string{true: q.Channels, false: "rgba"}[q.Channels != ""], "zFromXY": d.ZFromXY}
	if q.Cross {
		info["cross"] = fmt.Sprintf("faces %d–%d: +Y above, −X +Z +X −Z across, −Y below", surface/6*6, surface/6*6+5)
	} else {
		info["surface"] = surface
	}
	if t.Format.HDR {
		info["exposure"] = map[bool]any{true: "auto", false: d.Exposure}[d.Auto]
	}
	if tg != g {
		info["payload"] = hexg(g)
	}
	if t.Header.Flags&texture.FlagDelta != 0 {
		info["deltaOf"] = fmt.Sprintf("%016x (composed)", t.Header.Base)
	}
	if q.Save == nil || *q.Save {
		dir := os.Getenv("XDG_RUNTIME_DIR")
		if dir == "" {
			dir = os.TempDir()
		}
		dir = filepath.Join(dir, "owexplorer")
		name := fmt.Sprintf("%016x-m%d", tg, mip)
		if q.Cross {
			name += fmt.Sprintf("-cross%d", surface/6)
		} else if surfaces > 1 {
			name += fmt.Sprintf("-s%d", surface)
		}
		if q.Channels != "" && q.Channels != "rgba" {
			name += "-" + q.Channels
		}
		path := filepath.Join(dir, name+".png")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, png, 0o644); err != nil {
			return nil, err
		}
		info["path"] = path
	}
	text, _ := json.Marshal(info)
	return mcp.Content{mcp.Image(png, "image/png"), mcp.Text(string(text))}, nil
}

func availableMips(t *texture.Texture) []int {
	var out []int
	for i := range t.Mips {
		if t.Available(i) {
			out = append(out, i)
		}
	}
	return out
}
