// Package catalog is the explorer's model of an install: the content store, per-type and per-package
// indices, resource names, search, a cache of parsed STU documents and a background index of
// references between resources.
package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"owcontent/content"
	"owexplorer/internal/names"
	"owexplorer/internal/refindex"
	"owexplorer/internal/view"
	"owstu/stu"
)

// Type labels for resource types that are not STU files. Measured = magic bytes / project docs;
// the rest follow OWLib's names for these type ids and are marked as such.
var fixedTypes = map[uint32][2]string{
	0x002: {"Map data", "docs/Maps.md"},
	0x004: {"Texture", "docs/AssetFormats.md §1"},
	0x006: {"Animation", "OWLib"},
	0x007: {"Skeleton (model format)", "docs/AssetFormats.md §2.2"},
	0x008: {"Material", "docs/AssetFormats.md §3"},
	0x00C: {"Model", "docs/AssetFormats.md §2"},
	0x00D: {"Effect", "OWLib"},
	0x00E: {"Lighting data", "ResourceLoading.md"},
	0x03F: {"WEM (Wwise Vorbis)", "docs/AssetFormats.md §5"},
	0x043: {"Sound bank (Wwise)", "docs/AssetFormats.md §5.4"},
	0x077: {"Asset package", "ContentAccess.md"},
	0x07C: {"String", "format"},
	0x09C: {"Package bundle", "ContentAccess.md"},
	0x0A9: {"String (0x0A9)", "format"},
	0x0B2: {"Voice WEM (Wwise Vorbis)", "docs/AssetFormats.md §5.5"},
	0x04D: {"Texture payload", "docs/AssetFormats.md §1"},
	0x085: {"Shader (group)", "docs/Maps.md §7; OWLib name, unverified"},
	0x086: {"Shader (instance)", "docs/Maps.md §7; OWLib name, unverified"},
	0x087: {"Shader (code)", "docs/Maps.md §7; OWLib name, unverified"},
	0x04A: {"Effect-like (00D header)", "header shape: same 432-byte chunk header as 00D effects"},
	0x08E: {"Effect-like (00D header)", "header shape: same 432-byte chunk header as 00D effects"},
	0x08F: {"Effect-like (00D header)", "header shape: same 432-byte chunk header as 00D effects"},
}

// TypeRow is one resource type of the install.
type TypeRow struct {
	Type   uint32 `json:"type"`
	Label  string `json:"label"`
	Source string `json:"source"` // where the label comes from: "stu root", "OWLib", "magic", …
	Count  int    `json:"count"`
	Bytes  uint64 `json:"bytes"`
	STU    bool   `json:"stu"` // resources are STUD files
}

// PackageRow is an asset package.
type PackageRow struct {
	GUID     string `json:"guid"`
	Index    int    `json:"index"`
	Manifest string `json:"manifest"`
	Records  int    `json:"records"`
	Size     uint32 `json:"size"`
	Name     string `json:"name,omitempty"` // filled by App.Packages (index names: "Package of …")
}

// Catalog is an open install.
type Catalog struct {
	Install  string
	Describe string
	Store    *content.Store
	Book     *names.Book
	OpenTime time.Duration
	// IndexCache overrides the reference index's cache file (default refindex.CachePath; tests).
	IndexCache string

	res      []content.Resource // sorted by GUID
	hexes    []string           // lower-case 16-digit GUIDs, parallel to res
	types    []TypeRow
	typeIdx  map[uint32]int
	packages []PackageRow

	mu       sync.RWMutex
	derived  map[uint64]names.Name // names found in the content (strings, STU name fields), once indexed
	idx      *refindex.Index       // reference index (nil until loaded / built)
	building *refindex.Builder     // while building (progress, names found so far)
	index    Progress              // final progress

	qmu   sync.Mutex
	query struct {
		key string
		ids []int32
	}

	docs *docCache
}

// Open opens an install and builds the in-memory indices (the reference index runs in the background:
// see StartIndex).
func Open(install, repo string) (*Catalog, error) {
	t0 := time.Now()
	s, err := content.Open(install, nil)
	if err != nil {
		return nil, err
	}
	c := &Catalog{Install: install, Describe: s.Describe(), Store: s, Book: names.Load(repo),
		derived: map[uint64]names.Name{}, typeIdx: map[uint32]int{}, docs: newDocCache(12)}
	c.res = s.Resources()
	c.hexes = make([]string, len(c.res))
	for i, r := range c.res {
		c.hexes[i] = fmt.Sprintf("%016x", r.GUID)
		ti, ok := c.typeIdx[r.Type]
		if !ok {
			ti = len(c.types)
			c.typeIdx[r.Type] = ti
			c.types = append(c.types, TypeRow{Type: r.Type})
		}
		c.types[ti].Count++
		c.types[ti].Bytes += uint64(r.Size)
	}
	sort.Slice(c.types, func(i, j int) bool { return c.types[i].Type < c.types[j].Type })
	for i, t := range c.types {
		c.typeIdx[t.Type] = i
	}
	c.labelTypes()
	for i, p := range s.Packages() {
		c.packages = append(c.packages, PackageRow{GUID: fmt.Sprintf("%016x", p.GUID), Index: i, Manifest: p.Manifest,
			Records: int(p.RecordCount), Size: p.Size})
	}
	c.OpenTime = time.Since(t0)
	return c, nil
}

func (c *Catalog) Close() { c.Store.Close() }

// labelTypes names every type: STU files by their root type (read from the smallest resource of the
// type), the rest from fixedTypes.
func (c *Catalog) labelTypes() {
	smallest := map[uint32]content.Resource{}
	for _, r := range c.res {
		if s, ok := smallest[r.Type]; !ok || (r.Size > 16 && (r.Size < s.Size || s.Size <= 16)) {
			smallest[r.Type] = r
		}
	}
	var wg sync.WaitGroup
	for i := range c.types {
		t := &c.types[i]
		if f, ok := fixedTypes[t.Type]; ok {
			t.Label, t.Source = f[0], f[1]
		}
		r := smallest[t.Type]
		if r.Size > 4<<20 {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, err := c.Store.Read(r.GUID)
			if err != nil || !bytes.HasPrefix(b, []byte("DUTS")) {
				return
			}
			t.STU = true
			d, err := stu.Open(b, stu.File, 0)
			if err != nil {
				return
			}
			defer d.Close()
			if in, err := d.Instance(d.Root()); err == nil {
				name, named := c.Book.TypeName(in.Type)
				t.Label, t.Source = strings.TrimPrefix(name, "STU"), "STU root type"
				if !named {
					t.Label, t.Source = "STU "+name, "STU root type (unnamed)"
				}
			}
		}()
	}
	wg.Wait()
	for i := range c.types {
		if c.types[i].Label == "" {
			c.types[i].Label, c.types[i].Source = "unknown", ""
		}
	}
}

func (c *Catalog) Types() []TypeRow       { return c.types }
func (c *Catalog) Packages() []PackageRow { return c.packages }
func (c *Catalog) NumResources() int      { return len(c.res) }

func (c *Catalog) TypeLabel(t uint32) string {
	if i, ok := c.typeIdx[t]; ok {
		return c.types[i].Label
	}
	return "unknown"
}

func (c *Catalog) find(guid uint64) (int, bool) {
	i := sort.Search(len(c.res), func(i int) bool { return c.res[i].GUID >= guid })
	return i, i < len(c.res) && c.res[i].GUID == guid
}

// Find returns a resource by GUID.
func (c *Catalog) Find(guid uint64) (content.Resource, bool) {
	i, ok := c.find(guid)
	if !ok {
		return content.Resource{}, false
	}
	return c.res[i], true
}

// view.Resolver

func (c *Catalog) TypeName(h uint32) (string, bool)  { return c.Book.TypeName(h) }
func (c *Catalog) ClientClass(h uint32) (bool, bool) { return c.Book.ClientClass(h) }
func (c *Catalog) Exists(guid uint64) bool           { _, ok := c.find(guid); return ok }

// Name returns a resource's name and its source (zero Name if none).
func (c *Catalog) Name(guid uint64) names.Name {
	if n, ok := c.Book.GUIDs[guid]; ok {
		return n
	}
	c.mu.RLock()
	n, ok := c.derived[guid]
	b := c.building
	c.mu.RUnlock()
	if !ok && b != nil {
		if r, found := b.Name(guid); found {
			return names.Name{Text: r.Text, Source: r.Source, Guess: r.Guess}
		}
	}
	return n
}

func (c *Catalog) GUIDName(guid uint64) string { return c.Name(guid).Text }

// ---- listing and search ---------------------------------------------------------------------------

// Row is one line of a resource list.
type Row struct {
	GUID  string `json:"guid"`
	Type  uint32 `json:"type"`
	Size  uint32 `json:"size"`
	Name  string `json:"name,omitempty"`
	Guess bool   `json:"guess,omitempty"`
	Note  string `json:"note,omitempty"`
}

// Query selects resources: a type (-1 = all), a package GUID ("" = all), a search text.
type Query struct {
	Type    int    `json:"type"`
	Package string `json:"package"`
	Search  string `json:"search"`
	Start   int    `json:"start"`
	Count   int    `json:"count"`
}

type Page struct {
	Total int   `json:"total"`
	Start int   `json:"start"`
	Rows  []Row `json:"rows"`
}

// ParseGUID accepts "0x…", "ow://…" and bare hex.
func ParseGUID(s string) (uint64, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimPrefix(s, "ow://")
	s = strings.TrimPrefix(s, "0x")
	if s == "" || len(s) > 16 {
		return 0, false
	}
	g, err := strconv.ParseUint(s, 16, 64)
	return g, err == nil
}

func isHex(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return s != ""
}

func (c *Catalog) row(i int) Row {
	r := c.res[i]
	n := c.Name(r.GUID)
	return Row{GUID: c.hexes[i], Type: r.Type, Size: r.Size, Name: n.Text, Guess: n.Guess, Note: n.Note}
}

// List returns one page of the resources matching q. The filtered index of the last query is cached,
// so paging through a result is cheap.
func (c *Catalog) List(q Query) Page {
	ids := c.match(q)
	p := Page{Total: len(ids), Start: q.Start, Rows: []Row{}}
	for k := q.Start; k < len(ids) && k < q.Start+q.Count; k++ {
		p.Rows = append(p.Rows, c.row(int(ids[k])))
	}
	return p
}

func (c *Catalog) match(q Query) []int32 {
	search := strings.ToLower(strings.TrimSpace(q.Search))
	search = strings.TrimPrefix(search, "ow://")
	key := fmt.Sprintf("%d|%s|%s", q.Type, q.Package, search)
	c.qmu.Lock()
	defer c.qmu.Unlock()
	if c.query.key == key && c.query.ids != nil {
		return c.query.ids
	}
	var cand []int32
	if q.Package != "" {
		pg, _ := ParseGUID(q.Package)
		for _, rec := range c.Store.PackageResources(pg) {
			if i, ok := c.find(rec.GUID); ok {
				cand = append(cand, int32(i))
			}
		}
		sort.Slice(cand, func(a, b int) bool { return cand[a] < cand[b] })
	}
	hexq := strings.TrimPrefix(search, "0x")
	hexOnly := isHex(hexq)
	test := func(i int) bool {
		if q.Type >= 0 && c.res[i].Type != uint32(q.Type) {
			return false
		}
		if search == "" {
			return true
		}
		if hexOnly && strings.Contains(c.hexes[i], hexq) {
			return true
		}
		n := c.Name(c.res[i].GUID)
		return (n.Text != "" && strings.Contains(strings.ToLower(n.Text), search)) ||
			(n.Note != "" && strings.Contains(strings.ToLower(n.Note), search))
	}
	ids := []int32{}
	if cand != nil {
		for _, i := range cand {
			if test(int(i)) {
				ids = append(ids, i)
			}
		}
	} else {
		for i := range c.res {
			if test(i) {
				ids = append(ids, int32(i))
			}
		}
	}
	c.query.key, c.query.ids = key, ids
	return ids
}

// Invalidate drops the cached query (names changed).
func (c *Catalog) invalidate() {
	c.qmu.Lock()
	c.query.ids = nil
	c.qmu.Unlock()
}

// ---- documents ------------------------------------------------------------------------------------

// ErrNotSTU: the resource is not a STUD file.
var ErrNotSTU = errors.New("not an STU file")

// WithDoc parses (or takes from the cache) a resource's STU document and runs f with it locked.
func (c *Catalog) WithDoc(guid uint64, f func(d *stu.Document) error) error {
	e, err := c.docs.get(guid, func() (*stu.Document, error) {
		b, err := c.Store.Read(guid)
		if err != nil {
			return nil, err
		}
		if !bytes.HasPrefix(b, []byte("DUTS")) {
			return nil, ErrNotSTU
		}
		return stu.Open(b, stu.File, 0)
	})
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return f(e.doc)
}

// RefsTo returns the resources referencing guid (from the reference index; empty until it is ready).
func (c *Catalog) RefsTo(guid uint64) []uint64 {
	x := c.Index()
	if x == nil {
		return nil
	}
	var out []uint64
	for _, l := range x.In(guid) {
		out = append(out, l.GUID)
	}
	return out
}

func (c *Catalog) Progress() Progress {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.building != nil {
		return c.building.Progress()
	}
	return c.index
}

var _ view.Resolver = (*Catalog)(nil)
