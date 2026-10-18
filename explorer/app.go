package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"owcontent/content"
	"owexplorer/internal/catalog"
	"owexplorer/internal/mapview"
	"owexplorer/internal/model"
	"owexplorer/internal/names"
	"owexplorer/internal/texture"
	"owexplorer/internal/view"
	"owstu/stu"
)

// App is the object bound to the frontend: every exported method is callable from JS
// (frontend/wailsjs/go/main/App.js). GUIDs cross the boundary as 16-digit hex strings.
type App struct {
	ctx  context.Context
	repo string

	mu  sync.RWMutex
	cat *catalog.Catalog
	err string

	bmu   sync.Mutex
	bytes struct { // last resource read (hex paging)
		guid uint64
		b    []byte
	}

	smu   sync.Mutex
	state UIState

	mmu     sync.Mutex
	mapOpen *mapview.Map // last map data opened by the map view (~100 MB: one at a time)

	child   bool   // opened by another window (OpenWindow): --child
	map3d   bool   // --map3d: open the map view in 3D, maximised (the 3D view's "pop out", T066)
	install string // --install=<dir> (a child gets its parent's install)

	mcp *mcpState // MCP server (app_mcp.go)
	ask *askHub   // Ask Claude (app_ask.go)
}

func NewApp() *App {
	wd, _ := os.Getwd()
	a := &App{repo: names.FindRepo(wd)}
	for _, arg := range os.Args[1:] {
		switch {
		case arg == "--child":
			a.child = true
		case arg == "--map3d":
			a.map3d = true
		case strings.HasPrefix(arg, "--install="):
			a.install = strings.TrimPrefix(arg, "--install=")
		}
	}
	if a.repo == "" {
		if exe, err := os.Executable(); err == nil {
			a.repo = names.FindRepo(filepath.Dir(exe))
		}
	}
	a.loadState()
	return a
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if a.child {
		runtime.WindowSetTitle(ctx, "OW Storage Explorer · "+a.StartupLink())
	}
	a.startMCP()
	go a.OpenInstall("")
}

func (a *App) shutdown(context.Context) {
	if a.mcp != nil {
		a.mcp.srv.Close()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cat != nil {
		a.cat.Close()
	}
}

// ---- install --------------------------------------------------------------------------------------

type Status struct {
	Open      bool             `json:"open"`
	Opening   bool             `json:"opening"`
	Install   string           `json:"install"`
	Describe  string           `json:"describe"`
	Resources int              `json:"resources"`
	Packages  int              `json:"packages"`
	Types     int              `json:"types"`
	OpenMs    int64            `json:"openMs"`
	Error     string           `json:"error,omitempty"`
	Index     catalog.Progress `json:"index"`
	Repo      string           `json:"repo"`
	Notes     []string         `json:"notes"`
}

func (a *App) defaultInstall() string {
	if a.install != "" {
		return a.install
	}
	if a.state.Install != "" {
		return a.state.Install
	}
	if env := os.Getenv("OW_INSTALL"); env != "" {
		return env
	}
	if a.repo != "" {
		return filepath.Join(a.repo, "game", "owbetaclient")
	}
	return "game/owbetaclient"
}

var opening sync.Mutex

// OpenInstall opens an install directory ("" = the last one, $OW_INSTALL, or game/owbetaclient).
func (a *App) OpenInstall(dir string) Status {
	opening.Lock()
	defer opening.Unlock()
	if dir == "" {
		dir = a.defaultInstall()
	}
	c, err := catalog.Open(dir, a.repo)
	a.mu.Lock()
	if err != nil {
		a.err = err.Error()
		a.mu.Unlock()
		return a.Status()
	}
	if a.cat != nil {
		a.cat.Close()
	}
	a.cat, a.err = c, ""
	a.mu.Unlock()
	if !a.child { // a window opened by another one keeps the remembered install as it is
		a.update(func(st *UIState) { st.Install = dir })
	}
	c.StartIndex(func() {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "index", c.Progress())
		}
	})
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "opened")
	}
	return a.Status()
}

// StartupLink is the ow:// link given on the command line ("" if none): ow://<guid>[/<tab>[/<instance>]].
func (a *App) StartupLink() string {
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "ow://") {
			return arg
		}
	}
	return ""
}

// OpenWindow opens a resource in a new window: a second explorer process on the deep link, with this window's
// install (Wails v2 has one window per process). The index loads from the disk cache, so it opens in about a second.
func (a *App) OpenWindow(link string) error {
	return a.openWindow(link)
}

// OpenMap3DWindow pops a map's 3D view out into its own window: a child on ow://<map>/map[/<record>] with --map3d,
// which opens straight into the 3D view, maximised.
func (a *App) OpenMap3DWindow(guid string, record int) error {
	link := "ow://" + guid + "/map"
	if record >= 0 {
		link += fmt.Sprintf("/%d", record)
	}
	return a.openWindow(link, "--map3d")
}

// StartupMap3D: this window was opened by OpenMap3DWindow (--map3d).
func (a *App) StartupMap3D() bool { return a.map3d }

func (a *App) openWindow(link string, extra ...string) error {
	if !strings.HasPrefix(link, "ow://") {
		return fmt.Errorf("not an ow:// link: %q", link)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := append([]string{link, "--child"}, extra...)
	if c, err := a.catalog(); err == nil {
		args = append(args, "--install="+c.Install)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir, _ = os.Getwd()
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap it when it closes
	return nil
}

// ChooseInstall asks for a directory and opens it.
func (a *App) ChooseInstall() Status {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "Open an Overwatch 0.8 install",
		DefaultDirectory: a.defaultInstall()})
	if err != nil || dir == "" {
		return a.Status()
	}
	return a.OpenInstall(dir)
}

func (a *App) Status() Status {
	a.mu.RLock()
	defer a.mu.RUnlock()
	s := Status{Error: a.err, Repo: a.repo, Notes: []string{}}
	if !opening.TryLock() {
		s.Opening = true
	} else {
		opening.Unlock()
	}
	if c := a.cat; c != nil {
		s.Open, s.Install, s.Describe = true, c.Install, c.Describe
		s.Resources, s.Packages, s.Types = c.NumResources(), len(c.Packages()), len(c.Types())
		s.OpenMs = c.OpenTime.Milliseconds()
		s.Index = c.Progress()
		s.Notes = c.Book.Notes
	}
	return s
}

var errClosed = errors.New("no install open")

func (a *App) catalog() (*catalog.Catalog, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.cat == nil {
		return nil, errClosed
	}
	return a.cat, nil
}

func parse(guid string) (uint64, error) {
	g, ok := catalog.ParseGUID(guid)
	if !ok {
		return 0, fmt.Errorf("not a GUID: %q", guid)
	}
	return g, nil
}

// ---- browsing -------------------------------------------------------------------------------------

func (a *App) Types() []catalog.TypeRow {
	c, err := a.catalog()
	if err != nil {
		return []catalog.TypeRow{}
	}
	return c.Types()
}

func (a *App) Packages() []catalog.PackageRow {
	c, err := a.catalog()
	if err != nil {
		return []catalog.PackageRow{}
	}
	rows := append([]catalog.PackageRow(nil), c.Packages()...)
	for i := range rows {
		if g, ok := catalog.ParseGUID(rows[i].GUID); ok {
			rows[i].Name = c.Name(g).Text
		}
	}
	return rows
}

func (a *App) List(q catalog.Query) catalog.Page {
	c, err := a.catalog()
	if err != nil {
		return catalog.Page{Rows: []catalog.Row{}}
	}
	return c.List(q)
}

// ---- one resource ---------------------------------------------------------------------------------

type ResourceInfo struct {
	GUID      string     `json:"guid"`
	Type      uint32     `json:"type"`
	TypeLabel string     `json:"typeLabel"`
	Size      uint32     `json:"size"`
	CKey      string     `json:"ckey"`
	Flags     uint32     `json:"flags"`
	Packages  []string   `json:"packages"`
	Name      names.Name `json:"name"`
	Kind      string     `json:"kind"` // stu, text, binary
	Magic     string     `json:"magic"`
	Text      string     `json:"text,omitempty"`
	RefsIn    int        `json:"refsIn"`
	RefsOut   int        `json:"refsOut"`
	Pinned    bool       `json:"pinned"`
	Error     string     `json:"error,omitempty"`
}

func (a *App) read(c *catalog.Catalog, g uint64) ([]byte, error) {
	a.bmu.Lock()
	defer a.bmu.Unlock()
	if a.bytes.b != nil && a.bytes.guid == g {
		return a.bytes.b, nil
	}
	b, err := c.Store.Read(g)
	if err != nil {
		return nil, err
	}
	a.bytes.guid, a.bytes.b = g, b
	return b, nil
}

func (a *App) Resource(guid string) (ResourceInfo, error) {
	c, err := a.catalog()
	if err != nil {
		return ResourceInfo{}, err
	}
	g, err := parse(guid)
	if err != nil {
		return ResourceInfo{}, err
	}
	r, ok := c.Find(g)
	if !ok {
		return ResourceInfo{}, fmt.Errorf("%016x is not in this install", g)
	}
	info := ResourceInfo{GUID: fmt.Sprintf("%016x", g), Type: r.Type, TypeLabel: c.TypeLabel(r.Type), Size: r.Size,
		CKey: r.CKey.String(), Flags: r.Flags, Packages: []string{}, Name: c.Name(g), Pinned: a.pinned(info0(g))}
	if x := c.Index(); x != nil {
		info.RefsOut, info.RefsIn = x.Degree(g)
	}
	for _, p := range c.Store.ResourcePackages(g) {
		info.Packages = append(info.Packages, fmt.Sprintf("%016x", p))
	}
	b, err := a.read(c, g)
	if err != nil {
		info.Kind, info.Error = "binary", err.Error()
		return info, nil
	}
	info.Magic = magic(b)
	switch {
	case r.Type == texture.TypeTexture || r.Type == texture.TypePayload:
		info.Kind = "texture"
	case r.Type == model.TypeModel:
		info.Kind = "model"
	case r.Type == model.TypeMaterial:
		info.Kind = "material"
	case strings.HasPrefix(string(b[:min(4, len(b))]), "DUTS"):
		info.Kind = "stu"
	default:
		if s, ok := view.Text(b); ok {
			info.Kind, info.Text = "text", s
		} else {
			info.Kind = "binary"
		}
	}
	return info, nil
}

func info0(g uint64) string { return fmt.Sprintf("%016x", g) }

func magic(b []byte) string {
	var sb strings.Builder
	for _, x := range b[:min(4, len(b))] {
		if x < 128 && unicode.IsPrint(rune(x)) {
			sb.WriteByte(x)
		} else {
			sb.WriteByte('.')
		}
	}
	return sb.String()
}

type HexPage struct {
	Offset int    `json:"offset"`
	Size   int    `json:"size"`
	Data   []byte `json:"data"` // base64 in JSON
}

// Hex returns length bytes of a resource from offset.
func (a *App) Hex(guid string, offset, length int) (HexPage, error) {
	c, err := a.catalog()
	if err != nil {
		return HexPage{}, err
	}
	g, err := parse(guid)
	if err != nil {
		return HexPage{}, err
	}
	b, err := a.read(c, g)
	if err != nil {
		return HexPage{}, err
	}
	offset = max(0, min(offset, len(b)))
	end := min(len(b), offset+max(0, min(length, 1<<20)))
	return HexPage{Offset: offset, Size: len(b), Data: b[offset:end]}, nil
}

func (a *App) withDoc(guid string, f func(c *catalog.Catalog, d *stu.Document) error) error {
	c, err := a.catalog()
	if err != nil {
		return err
	}
	g, err := parse(guid)
	if err != nil {
		return err
	}
	return c.WithDoc(g, func(d *stu.Document) error { return f(c, d) })
}

func (a *App) Document(guid string) (view.Document, error) {
	var out view.Document
	err := a.withDoc(guid, func(c *catalog.Catalog, d *stu.Document) error { out = view.Describe(d, c); return nil })
	return out, err
}

func (a *App) Instance(guid string, index int) (view.Node, error) {
	var out view.Node
	err := a.withDoc(guid, func(c *catalog.Catalog, d *stu.Document) error {
		if index < 0 || index >= d.NumInstances() {
			return fmt.Errorf("instance %d out of range", index)
		}
		out = view.Tree(d, index, c)
		return nil
	})
	return out, err
}

func (a *App) Graph(guid string) (*view.Graph, error) {
	var out *view.Graph
	err := a.withDoc(guid, func(c *catalog.Catalog, d *stu.Document) (err error) {
		out, err = view.BuildGraph(d, c)
		if err == nil {
			out.Layout = view.Layout(out)
		}
		return err
	})
	return out, err
}

// NodeBrief is one Statescript node in a few fields (the Ask panel's node badges).
type NodeBrief struct {
	Index int    `json:"index"`
	Kind  string `json:"kind"`
	Short string `json:"short"`
	Net   string `json:"net"`
}

// GraphNodes lists a graph's nodes briefly (m_nodes index, kind, short type, network side).
func (a *App) GraphNodes(guid string) ([]NodeBrief, error) {
	var out []NodeBrief
	err := a.withDoc(guid, func(c *catalog.Catalog, d *stu.Document) error {
		g, err := view.BuildGraph(d, c)
		if err != nil {
			return err
		}
		for _, n := range g.Nodes {
			out = append(out, NodeBrief{n.Index, n.Kind, n.Short, n.Net})
		}
		return nil
	})
	return out, err
}

// ---- graph view: node positions the user dragged (view only, never game data) ------------------------------

func graphLayoutPath(guid, mode string) string {
	d, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	g, _ := catalog.ParseGUID(guid)
	if mode != "editor" {
		mode = "layered"
	}
	return filepath.Join(d, "owexplorer", "graph-layout", fmt.Sprintf("%016x-%s.json", g, mode))
}

// GraphPositions are the node positions the user dragged in a graph's view (node position → x, y) for a layout mode
// ("layered" or "editor"); stored in the config directory, per graph.
func (a *App) GraphPositions(guid, mode string) map[string][2]float64 {
	out := map[string][2]float64{}
	if b, err := os.ReadFile(graphLayoutPath(guid, mode)); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// SetGraphPositions saves them (an empty map resets the layout).
func (a *App) SetGraphPositions(guid, mode string, pos map[string][2]float64) error {
	p := graphLayoutPath(guid, mode)
	if p == "" {
		return errors.New("no config directory")
	}
	if len(pos) == 0 {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(pos)
	return os.WriteFile(p, b, 0o644)
}

func (a *App) Entity(guid string) (*view.Entity, error) {
	var out *view.Entity
	err := a.withDoc(guid, func(c *catalog.Catalog, d *stu.Document) (err error) {
		out, err = view.BuildEntity(d, c)
		return err
	})
	return out, err
}

func (a *App) rows(c *catalog.Catalog, gs []uint64) []catalog.Row {
	out := []catalog.Row{}
	for _, g := range gs[:min(len(gs), 5000)] {
		r, ok := c.Find(g)
		n := c.Name(g)
		row := catalog.Row{GUID: fmt.Sprintf("%016x", g), Type: content.TypeOf(g), Name: n.Text, Guess: n.Guess, Note: n.Note}
		if ok {
			row.Size = r.Size
		}
		out = append(out, row)
	}
	return out
}

// RefsFrom lists the resources a resource references (reference index; before it is ready, STU fields
// only); RefsTo those referencing it.
func (a *App) RefsFrom(guid string) ([]catalog.Row, error) {
	c, err := a.catalog()
	if err != nil {
		return nil, err
	}
	g, err := parse(guid)
	if err != nil {
		return nil, err
	}
	if x := c.Index(); x != nil {
		var gs []uint64
		for _, l := range x.Out(g) {
			gs = append(gs, l.GUID)
		}
		return a.rows(c, gs), nil
	}
	var out []catalog.Row
	err = a.withDoc(guid, func(c *catalog.Catalog, d *stu.Document) error { out = a.rows(c, view.Refs(d)); return nil })
	return out, err
}

func (a *App) RefsTo(guid string) ([]catalog.Row, error) {
	c, err := a.catalog()
	if err != nil {
		return nil, err
	}
	g, err := parse(guid)
	if err != nil {
		return nil, err
	}
	return a.rows(c, c.RefsTo(g)), nil
}

// Export saves a resource's bytes ("bytes") or its decoded STU tree as JSON ("json"); returns the path
// ("" if cancelled).
func (a *App) Export(guid, kind string) (string, error) {
	c, err := a.catalog()
	if err != nil {
		return "", err
	}
	g, err := parse(guid)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%016x.bin", g)
	if kind == "json" {
		name = fmt.Sprintf("%016x.json", g)
	} else if b, err := a.read(c, g); err == nil && strings.HasPrefix(string(b[:min(4, len(b))]), "DUTS") {
		name = fmt.Sprintf("%016x.stud", g)
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "Export " + name, DefaultFilename: name})
	if err != nil || path == "" {
		return "", err
	}
	var data []byte
	if kind == "json" {
		err = c.WithDoc(g, func(d *stu.Document) error { data = []byte(d.JSON(false)); return nil })
	} else {
		data, err = a.read(c, g)
	}
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0o644)
}

// ---- pins and recents (os.UserConfigDir()/owexplorer/state.json) ------------------------------------

type UIState struct {
	Install string         `json:"install"`
	Pinned  []string       `json:"pinned"`
	Recent  []string       `json:"recent"`
	Panels  map[string]int `json:"panels"` // panel sizes in px by panel id (splitters)
}

func statePath() string {
	d, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "owexplorer", "state.json")
}

func (a *App) loadState() {
	a.state = UIState{Pinned: []string{}, Recent: []string{}, Panels: map[string]int{}}
	if b, err := os.ReadFile(statePath()); err == nil {
		_ = json.Unmarshal(b, &a.state)
	}
	if a.state.Panels == nil {
		a.state.Panels = map[string]int{}
	}
}

// update changes the saved state: re-read state.json (another window may have written it), apply f, write it back.
func (a *App) update(f func(st *UIState)) {
	a.smu.Lock()
	defer a.smu.Unlock()
	st := UIState{Pinned: []string{}, Recent: []string{}, Panels: map[string]int{}}
	if b, err := os.ReadFile(statePath()); err == nil && json.Unmarshal(b, &st) == nil {
		if st.Panels == nil {
			st.Panels = map[string]int{}
		}
		a.state = st
	}
	f(&a.state)
	b, _ := json.MarshalIndent(a.state, "", "  ")
	if p := statePath(); p != "" {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		tmp := p + fmt.Sprintf(".%d.tmp", os.Getpid())
		if os.WriteFile(tmp, b, 0o644) == nil {
			_ = os.Rename(tmp, p)
		}
	}
}

func (a *App) pinned(guid string) bool {
	a.smu.Lock()
	defer a.smu.Unlock()
	return slices.Contains(a.state.Pinned, guid)
}

func (a *App) UIState() UIState {
	a.smu.Lock()
	defer a.smu.Unlock()
	st := a.state
	st.Panels = maps.Clone(a.state.Panels) // marshalled after the lock is released
	return st
}

func (a *App) SetPinned(guid string, on bool) UIState {
	a.update(func(st *UIState) {
		st.Pinned = slices.DeleteFunc(st.Pinned, func(s string) bool { return s == guid })
		if on {
			st.Pinned = append([]string{guid}, st.Pinned...)
		}
	})
	return a.UIState()
}

// SetPanel remembers a panel's size (px); 0 forgets it (back to the default).
func (a *App) SetPanel(id string, size int) UIState {
	a.update(func(st *UIState) {
		if size <= 0 {
			delete(st.Panels, id)
		} else {
			st.Panels[id] = size
		}
	})
	return a.UIState()
}

// Visit records guid as the most recent resource.
func (a *App) Visit(guid string) UIState {
	a.update(func(st *UIState) {
		st.Recent = slices.DeleteFunc(st.Recent, func(s string) bool { return s == guid })
		st.Recent = append([]string{guid}, st.Recent...)
		if len(st.Recent) > 40 {
			st.Recent = st.Recent[:40]
		}
	})
	return a.UIState()
}

// Describe rows for a list of GUIDs (pins, recents).
func (a *App) Rows(guids []string) []catalog.Row {
	c, err := a.catalog()
	if err != nil {
		return []catalog.Row{}
	}
	var gs []uint64
	for _, s := range guids {
		if g, ok := catalog.ParseGUID(s); ok {
			gs = append(gs, g)
		}
	}
	return a.rows(c, gs)
}

// --- map view (docs/Explorer.md "Map view") ---------------------------------------------------------

// openMap parses a map data blob (type 0x002), or the map data a map header (0x09F) names in m_map.
func (a *App) openMap(guid string) (*catalog.Catalog, *mapview.Map, error) {
	c, err := a.catalog()
	if err != nil {
		return nil, nil, err
	}
	g, err := parse(guid)
	if err != nil {
		return nil, nil, err
	}
	if content.TypeOf(g) == 0x09F {
		var data uint64
		err := c.WithDoc(g, func(d *stu.Document) error {
			var err error
			data, _, err = d.Decode(d.Root()).Field(names.Hash("m_map")).Ref()
			return err
		})
		if err != nil || data == 0 {
			return nil, nil, fmt.Errorf("map header %016x: no m_map (%v)", g, err)
		}
		g = data
	}
	a.mmu.Lock()
	defer a.mmu.Unlock()
	if a.mapOpen != nil && a.mapOpen.GUID == g {
		return c, a.mapOpen, nil
	}
	b, err := c.Store.Read(g)
	if err != nil {
		return nil, nil, err
	}
	m, err := mapview.Open(g, b)
	if err != nil {
		return nil, nil, err
	}
	a.mapOpen = m
	return c, m, nil
}

// MapView is the map view of a map data (or map header) resource.
func (a *App) MapView(guid string) (*mapview.View, error) {
	c, m, err := a.openMap(guid)
	if err != nil {
		return nil, err
	}
	a.mmu.Lock()
	defer a.mmu.Unlock()
	return m.Build(c), nil
}

// MapShapes is a map's collision shapes (spheres, capsules, hulls) and its mesh summary (3D mode; the mesh itself is
// served as /mapgeo/<guid>.bin, see imageHandler).
func (a *App) MapShapes(guid string) (*mapview.Shapes, error) {
	_, m, err := a.openMap(guid)
	if err != nil {
		return nil, err
	}
	a.mmu.Lock()
	defer a.mmu.Unlock()
	return m.Shapes()
}

// MapRay casts a ray on a map's collision mesh (3D mode cursor), on the triangle parts given (mapview.Part*, 0 = all).
func (a *App) MapRay(guid string, origin, dir [3]float32, parts int) (*mapview.RayHit, error) {
	_, m, err := a.openMap(guid)
	if err != nil {
		return nil, err
	}
	a.mmu.Lock()
	defer a.mmu.Unlock()
	return m.Ray(origin, dir, parts)
}

func (a *App) mapMesh(guid string) ([]byte, error) {
	_, m, err := a.openMap(guid)
	if err != nil {
		return nil, err
	}
	a.mmu.Lock()
	defer a.mmu.Unlock()
	return m.MeshBinary()
}

// MapRecord is one record of a map: decoded fields and instance data.
func (a *App) MapRecord(guid string, index int) (*mapview.Detail, error) {
	c, m, err := a.openMap(guid)
	if err != nil {
		return nil, err
	}
	a.mmu.Lock()
	defer a.mmu.Unlock()
	return m.Record(c, index)
}
