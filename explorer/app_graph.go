package main

import (
	"encoding/binary"
	"fmt"
	"strings"

	"owcontent/content"
	"owexplorer/internal/audio"
	"owexplorer/internal/catalog"
	"owexplorer/internal/model"
	"owexplorer/internal/refindex"
	"owexplorer/internal/texture"
	"owexplorer/internal/view"
	"owstu/stu"
)

// ---- reference index: dependency graph, info view, reveal (docs/Explorer.md "Reference index") ----------

// Kinds lists the reference index's edge kinds.
func (a *App) Kinds() []refindex.KindInfo {
	c, err := a.catalog()
	if err != nil {
		return []refindex.KindInfo{}
	}
	return c.KindRows()
}

// Neighbors is one side of a resource in the reference index (what it uses / what uses it), filtered and paged.
func (a *App) Neighbors(q catalog.NeighborQuery) (catalog.NeighborPage, error) {
	c, err := a.catalog()
	if err != nil {
		return catalog.NeighborPage{}, err
	}
	return c.Neighbors(q)
}

// Node describes one resource for the dependency graph.
func (a *App) Node(guid string) (catalog.NodeRow, error) {
	c, err := a.catalog()
	if err != nil {
		return catalog.NodeRow{}, err
	}
	g, err := parse(guid)
	if err != nil {
		return catalog.NodeRow{}, err
	}
	return c.Node(g), nil
}

// IndexOf is the row of guid in the resource list q selects (-1 if absent): "reveal in list".
func (a *App) IndexOf(q catalog.Query, guid string) int {
	c, err := a.catalog()
	if err != nil {
		return -1
	}
	g, err := parse(guid)
	if err != nil {
		return -1
	}
	return c.IndexOf(q, g)
}

// RefDetail says where "from" references "to": STU field paths, or the map record.
type RefDetail struct {
	Kinds []string `json:"kinds"`
	Paths []string `json:"paths"`
	Aux   int      `json:"aux,omitempty"`
}

func (a *App) RefDetail(from, to string) (RefDetail, error) {
	out := RefDetail{Kinds: []string{}, Paths: []string{}}
	c, err := a.catalog()
	if err != nil {
		return out, err
	}
	f, err := parse(from)
	if err != nil {
		return out, err
	}
	t, err := parse(to)
	if err != nil {
		return out, err
	}
	if x := c.Index(); x != nil {
		for _, l := range x.Out(f) {
			if l.GUID == t {
				out.Kinds, out.Aux = x.KindNames(l.Mask), int(l.Aux)
			}
		}
	}
	_ = c.WithDoc(f, func(d *stu.Document) error { out.Paths = view.RefPaths(d, t, c, 40); return nil })
	return out, nil
}

// DecoderStatus says how far the explorer understands a resource.
type DecoderStatus struct {
	Name    string `json:"name"`   // "STU", "map data", "string asset", "asset package", ""
	Status  string `json:"status"` // ok, error, none
	Detail  string `json:"detail"`
	Planned string `json:"planned,omitempty"` // task that adds a decoder
}

type PackageDetail struct {
	Records  int    `json:"records"`
	Deps     int    `json:"deps"`
	Bundle   string `json:"bundle,omitempty"`
	Owner    string `json:"owner,omitempty"`
	Manifest string `json:"manifest"`
}

// InfoView is the info tab: every resource has one.
type InfoView struct {
	Stats   catalog.TypeStats    `json:"stats"`
	Decoder DecoderStatus        `json:"decoder"`
	Ready   bool                 `json:"ready"` // reference index loaded
	Out     catalog.NeighborPage `json:"out"`   // counts only
	In      catalog.NeighborPage `json:"in"`
	Package *PackageDetail       `json:"package,omitempty"`
}

var planned = map[uint32]string{}

func (a *App) Info(guid string) (InfoView, error) {
	c, err := a.catalog()
	if err != nil {
		return InfoView{}, err
	}
	g, err := parse(guid)
	if err != nil {
		return InfoView{}, err
	}
	t := content.TypeOf(g)
	v := InfoView{Stats: c.TypeStats(t)}
	v.Out, _ = c.Neighbors(catalog.NeighborQuery{GUID: guid, Dir: "out", Count: -1})
	v.In, _ = c.Neighbors(catalog.NeighborQuery{GUID: guid, Dir: "in", Count: -1})
	v.Ready = v.Out.Ready
	b, err := a.read(c, g)
	switch {
	case err != nil:
		v.Decoder = DecoderStatus{Status: "error", Detail: err.Error()}
	case t == 0x002:
		v.Decoder = DecoderStatus{Name: "map data (systems/maps)", Status: "ok", Detail: "see the Map tab"}
	case t == texture.TypeTexture || t == texture.TypePayload:
		v.Decoder = a.textureStatus(c, g)
	case t == model.TypeModel || t == model.TypeMaterial:
		v.Decoder = a.modelStatus(c, g, b)
	case isWEM(t) || t == audio.TypeBank:
		v.Decoder = audioStatus(c, g, b)
	case strings.HasPrefix(string(b[:min(4, len(b))]), "DUTS"):
		v.Decoder = DecoderStatus{Name: "STU", Status: "ok"}
		err := c.WithDoc(g, func(d *stu.Document) error {
			doc := view.Describe(d, c)
			errs := 0
			for _, in := range doc.Instances {
				errs += in.Errors
			}
			v.Decoder.Detail = fmt.Sprintf("%d instances, layout CRCs %s, %d field errors", len(doc.Instances),
				map[bool]string{true: "all match", false: "differ"}[doc.AllCRCOK], errs)
			if !doc.AllCRCOK || errs > 0 {
				v.Decoder.Status = "error"
			}
			return nil
		})
		if err != nil {
			v.Decoder = DecoderStatus{Name: "STU", Status: "error", Detail: err.Error()}
		}
	case t == content.TypeAssetPackage:
		v.Decoder = DecoderStatus{Name: "asset package", Status: "ok", Detail: "header, records (docs/ContentAccess.md §4)"}
		pd := &PackageDetail{Records: len(c.Store.PackageResources(g))}
		if len(b) >= 0x50 {
			pd.Deps = int(binary.LittleEndian.Uint32(b[0x24:]))
			if bg := binary.LittleEndian.Uint64(b[0x2C:]); bg != 0 {
				pd.Bundle = fmt.Sprintf("%016x", bg)
			}
		}
		for _, p := range c.Store.Packages() {
			if p.GUID == g {
				pd.Manifest = p.Manifest
				if p.Asset != 0 {
					pd.Owner = fmt.Sprintf("%016x", p.Asset)
				}
			}
		}
		v.Package = pd
	default:
		if s, ok := view.Text(b); ok {
			v.Decoder = DecoderStatus{Name: "string asset", Status: "ok", Detail: fmt.Sprintf("%d characters", len([]rune(s)))}
		} else {
			v.Decoder = DecoderStatus{Status: "none", Detail: "unknown format: no decoder yet (Hex shows the bytes)", Planned: planned[t]}
		}
	}
	return v, nil
}
