package main

// MCP for audio (T065, docs/Explorer.md "MCP server"): describe's audio summary and the audio_export tool, which
// writes WEMs as WAV / Ogg files a client can play or analyse.

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"owcontent/content"
	"owexplorer/internal/catalog"
	"owexplorer/internal/mcp"
)

// audioSummary is describe's summary for audio resources: the Audio tab's data, trimmed.
func (a *App) audioSummary(c *catalog.Catalog, g uint64) (any, error) {
	v, err := a.Audio(hexg(g))
	if err != nil {
		return nil, err
	}
	switch v.Kind {
	case "sound":
		s := v.Sound
		return map[string]any{"eventPlay": s.EventPlay, "eventStop": s.EventStop, "bank": s.Bank, "bankId": s.BankID,
			"wems": s.Files, "events": trimTree(s.Events, 4, 12), "error": s.Error}, nil
	case "bank":
		b := v.Bank
		return map[string]any{"version": b.Version, "id": b.ID, "chunks": b.Chunks, "objects": b.Counts,
			"events": len(b.Events), "eventTrees": trimTree(b.Events[:min(len(b.Events), 8)], 4, 12), "sounds": b.Sounds,
			"switchStateParameterIds": b.Groups, "note": "the switch / state / parameter ids are byte matches in the HIRC objects [?]",
			"error": b.Error}, nil
	}
	return v, nil
}

// trimTree cuts HIRC trees at depth and at n children per node.
func trimTree(ns []HircNode, depth, n int) []HircNode {
	out := make([]HircNode, 0, len(ns))
	for _, x := range ns {
		if len(x.Children) > n {
			x.More += len(x.Children) - n
			x.Children = x.Children[:n]
		}
		if depth <= 1 {
			x.More += len(x.Children)
			x.Children = nil
		} else {
			x.Children = trimTree(x.Children, depth-1, n)
		}
		out = append(out, x)
	}
	return out
}

type audioExportArgs struct {
	GUID   string `json:"guid"`
	Format string `json:"format"`
	Limit  int    `json:"limit"`
}

func audioExportTool(a *App) mcp.Tool {
	return tool("audio_export", "Decode audio to files a client can play or analyse: a WEM (0x03F, voice 0x0B2), every WEM "+
		"of a sound (0x02C), or the WEMs a bank's (0x043) sounds play. Each is written as WAV (decoded, 16-bit PCM) or Ogg "+
		"Vorbis (rebuilt, not re-encoded) to $XDG_RUNTIME_DIR/owexplorer/<wem>.<format>; the result lists the paths with "+
		"length, channels, rate, peak and RMS level.",
		obj(map[string]any{"guid": pGUID, "format": pEnum("default wav", "wav", "ogg"),
			"limit": pInt("at most this many WEMs (default 20, max 200)")}, "guid"),
		typed(a.toolAudioExport))
}

func (a *App) toolAudioExport(ctx context.Context, q audioExportArgs) (any, error) {
	c, g, err := a.guidArg(q.GUID)
	if err != nil {
		return nil, err
	}
	format := q.Format
	if format == "" {
		format = "wav"
	}
	if format != "wav" && format != "ogg" {
		return nil, fmt.Errorf("format: wav or ogg")
	}
	limit := cmp0(q.Limit, 20)
	if limit > 200 {
		return nil, fmt.Errorf("limit: at most 200")
	}
	wems, err := soundWEMs(c, g)
	if err != nil {
		return nil, err
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "owexplorer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var files []map[string]any
	for _, w := range wems[:min(len(wems), limit)] {
		d, err := decodeWEM(c, w)
		if err != nil {
			files = append(files, map[string]any{"wem": hexg(w), "error": err.Error()})
			continue
		}
		b, err := audioFile(c, w, format)
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, fmt.Sprintf("%016x.%s", w, format))
		if err := os.WriteFile(path, b, 0o644); err != nil {
			return nil, err
		}
		var peak, sum float64
		n := 0
		for _, ch := range d.pcm {
			for _, x := range ch {
				peak = math.Max(peak, math.Abs(float64(x)))
				sum += float64(x) * float64(x)
			}
			n += len(ch)
		}
		h := d.v.Header
		files = append(files, map[string]any{"wem": hexg(w), "type": fmt.Sprintf("0x%03X", content.TypeOf(w)), "path": path,
			"seconds": math.Round(h.Seconds()*1000) / 1000, "channels": h.Channels, "rate": h.SampleRate,
			"peakDB": dB(peak), "rmsDB": dB(math.Sqrt(sum / float64(max(1, n))))})
	}
	return map[string]any{"format": format, "total": len(wems), "written": len(files), "files": files}, nil
}

func dB(x float64) any {
	if x <= 0 {
		return "-inf"
	}
	return math.Round(20*math.Log10(x)*10) / 10
}
