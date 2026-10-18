package refindex

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"sync"

	"owcontent/content"
	"owexplorer/internal/names"
	"owexplorer/internal/view"
	"owmaps/maps"
	"owstu/stu"
)

// Builtin is the default source list, in the order they run (names of earlier sources are visible to later
// ones: strings first, so STU name fields resolve).
func Builtin() []Source { return []Source{Strings{}, STU{}, &Maps{}, &Packages{}} }

// ---- strings: string assets (0x07C, 0x0A9) name themselves ------------------------------------------------

type Strings struct{}

func (Strings) ID() string        { return "strings" }
func (Strings) Version() int      { return 1 }
func (Strings) Kinds() []KindInfo { return nil }
func (Strings) Workers() int      { return 8 }
func (Strings) Select(_ *Ctx, r content.Resource) bool {
	return r.Type == 0x07C || r.Type == 0x0A9
}
func (Strings) Scan(c *Ctx, r content.Resource, e *Emitter) error {
	b, err := c.Store.Read(r.GUID)
	if err != nil {
		return err
	}
	if s, ok := view.Text(b); ok {
		e.Name(r.GUID, s, "string asset", false)
		return nil
	}
	return fmt.Errorf("not a string asset")
}

// ---- stu: every reference field of every STUD resource ---------------------------------------------------

type STU struct{}

var (
	hName = names.Hash("m_name")
	hMap  = names.Hash("m_map")
)

func (STU) ID() string   { return "stu" }
func (STU) Version() int { return 2 } // 2: teStrings decode (systems/stu fix, T065), so name fields name resources
func (STU) Kinds() []KindInfo {
	return []KindInfo{{Name: "stu.field", Label: "STU field"}}
}
func (STU) Workers() int                           { return 8 }
func (STU) Select(c *Ctx, r content.Resource) bool { return c.IsSTU(r.Type) }
func (STU) Scan(c *Ctx, r content.Resource, e *Emitter) error {
	b, err := c.Store.Read(r.GUID)
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(b, []byte("DUTS")) {
		return fmt.Errorf("not a STUD file")
	}
	d, err := stu.Open(b, stu.File, 0)
	if err != nil {
		return err
	}
	defer d.Close()
	for _, g := range view.Refs(d) {
		e.Ref(r.GUID, g, 0, 0)
	}
	// A root field naming a string (m_name, m_displayName, …) names the resource; a map header also names
	// its map data (m_map).
	root := d.Decode(d.Root())
	name, via := "", ""
	var mapData uint64
	for _, f := range root.Items() {
		fi := f.Info()
		if fi.Type != stu.Ref {
			continue
		}
		if fi.FieldHash == hMap {
			mapData = fi.GUID
		}
		if name != "" || content.TypeOf(fi.GUID) != 0x07C {
			continue
		}
		fn, _ := c.TypeName(fi.FieldHash)
		if fi.FieldHash == hName || strings.Contains(strings.ToLower(fn), "name") {
			name, via = c.Name(fi.GUID), fn
		}
	}
	if name != "" {
		e.Name(r.GUID, name, "name field "+via+" (string asset)", false)
		if mapData != 0 {
			e.Name(mapData, name, "map header "+fmt.Sprintf("%016x", r.GUID), false)
		}
	}
	return nil
}

// ---- maps: placements, environment and instance data of the map data (0x002) ------------------------------

// Maps reads every map data blob through systems/maps (docs/Maps.md §2). Aux = record index + 1.
type Maps struct{}

const (
	mapPlacement = iota // record's primary GUID: definition / model / effect / sound / string
	mapAsset            // secondary GUID (look, font, skin), model-group instance looks, GUIDs in record fields
	mapEnv              // environment: gamemode brain, lighting, sky, sound space, textures
	mapInstance         // references inside entity instance data (STU blobs)
	mapLook             // model → look placed together (record types 1, 2, 8, the sky): from = the model (T064)
)

func (*Maps) ID() string   { return "maps" }
func (*Maps) Version() int { return 2 }
func (*Maps) Kinds() []KindInfo {
	return []KindInfo{
		{Name: "map.placement", Label: "placement"},
		{Name: "map.asset", Label: "placement asset"},
		{Name: "map.env", Label: "map environment"},
		{Name: "map.instance", Label: "instance data"},
		{Name: "map.look", Label: "model look (map)"},
	}
}
func (*Maps) Workers() int                           { return 2 } // map data is up to 119 MB
func (*Maps) Select(_ *Ctx, r content.Resource) bool { return r.Type == 0x002 }

func mapGUID(g uint64) bool { return g != 0 && g != math.MaxUint64 && g>>60 == 0 && g>>48 != 0 }

func (*Maps) Scan(c *Ctx, r content.Resource, e *Emitter) error {
	b, err := c.Store.Read(r.GUID)
	if err != nil {
		return err
	}
	d, err := maps.Open(b)
	if err != nil {
		return err
	}
	m, err := d.Map(true, true)
	d.Close()
	if err != nil {
		return err
	}
	env := m.Header.Env
	for _, g := range []maps.GUID{env.GamemodeDef, env.Lighting, env.SkyModel, env.SkyLook, env.SoundSpace, env.Name,
		env.TextureF8, env.Texture108, env.Texture128, env.Texture130, env.Texture180, env.Texture188, env.Texture190} {
		if mapGUID(uint64(g)) {
			e.Ref(r.GUID, uint64(g), mapEnv, 0)
		}
	}
	if mapGUID(uint64(env.SkyModel)) && mapGUID(uint64(env.SkyLook)) {
		e.Ref(uint64(env.SkyModel), uint64(env.SkyLook), mapLook, 0)
	}
	for _, sp := range m.SoundSpaces {
		if mapGUID(uint64(sp.Space)) {
			e.Ref(r.GUID, uint64(sp.Space), mapEnv, 0)
		}
	}
	for i := range m.Records {
		rec := &m.Records[i]
		aux := uint32(i + 1)
		if mapGUID(uint64(rec.Primary)) {
			e.Ref(r.GUID, uint64(rec.Primary), mapPlacement, aux)
		}
		if mapGUID(uint64(rec.Secondary)) {
			e.Ref(r.GUID, uint64(rec.Secondary), mapAsset, aux)
		}
		for _, in := range rec.Instances {
			if mapGUID(uint64(in.Look)) {
				e.Ref(r.GUID, uint64(in.Look), mapAsset, aux)
				if rec.Primary.Type() == 0x00C {
					e.Ref(uint64(rec.Primary), uint64(in.Look), mapLook, 0)
				}
			}
		}
		if rec.Primary.Type() == 0x00C && rec.Secondary.Type() == 0x01A {
			e.Ref(uint64(rec.Primary), uint64(rec.Secondary), mapLook, 0)
		}
		for _, f := range rec.Fields {
			if s, ok := f[2].(string); ok && len(s) == 18 && strings.HasPrefix(s, "0x") {
				var g uint64
				if _, err := fmt.Sscanf(s[2:], "%x", &g); err == nil && mapGUID(g) {
					e.Ref(r.GUID, g, mapAsset, aux)
				}
			}
		}
		for _, id := range rec.InstanceData {
			end := uint64(id.Offset) + uint64(id.Size)
			if end > uint64(len(b)) {
				continue
			}
			doc, err := stu.Open(b[id.Offset:end], stu.File, 0)
			if err != nil {
				continue
			}
			for _, g := range view.Refs(doc) {
				e.Ref(r.GUID, g, mapInstance, aux)
			}
			doc.Close()
		}
	}
	return nil
}

// ---- packages: asset package members, dependencies, bundles and owners ------------------------------------

// Packages reads the package list (APM: owner asset) and each package blob's header (docs/ContentAccess.md §4).
type Packages struct {
	once  sync.Once
	owner map[uint64]uint64 // package → asset it belongs to
}

const (
	pkgMember = iota // package → resource it lists
	pkgDep           // package → package it depends on
	pkgBundle        // package → its bundle (0x09C)
	pkgOwner         // asset → its package (APM)
)

func (*Packages) ID() string   { return "packages" }
func (*Packages) Version() int { return 2 }
func (*Packages) Kinds() []KindInfo {
	return []KindInfo{
		{Name: "package.member", Label: "package member"},
		{Name: "package.dep", Label: "package dependency"},
		{Name: "package.bundle", Label: "package bundle"},
		{Name: "package.owner", Label: "load package (APM)"},
	}
}
func (*Packages) Workers() int                           { return 8 }
func (*Packages) Select(_ *Ctx, r content.Resource) bool { return r.Type == content.TypeAssetPackage }

func (p *Packages) Prepare(c *Ctx) error {
	p.owner = map[uint64]uint64{}
	for _, pk := range c.Store.Packages() {
		p.owner[pk.GUID] = pk.Asset
	}
	return nil
}

func (p *Packages) Scan(c *Ctx, r content.Resource, e *Emitter) error {
	owner := ""
	if a := p.owner[r.GUID]; a != 0 {
		e.Ref(a, r.GUID, pkgOwner, 0)
		if n := c.Name(a); n != "" {
			owner = n
			e.Name(r.GUID, "Package of "+n, fmt.Sprintf("APM owner %016x", a), false)
		}
	}
	for _, rec := range c.Store.PackageResources(r.GUID) {
		e.Ref(r.GUID, rec.GUID, pkgMember, 0)
	}
	b, err := c.Store.Read(r.GUID)
	if err != nil {
		return err
	}
	if len(b) < 0x50 {
		return fmt.Errorf("package blob too small")
	}
	le := binary.LittleEndian
	depOff, deps := le.Uint64(b[0x10:]), le.Uint32(b[0x24:])
	if depOff != 0 && depOff+4*uint64(deps) <= uint64(len(b)) {
		for k := range uint64(deps) {
			idx := le.Uint32(b[depOff+4*k:])
			e.Ref(r.GUID, 0x06E0000000000000|uint64(idx), pkgDep, 0) // typebits(0x077) | index
		}
	}
	if bg := le.Uint64(b[0x2C:]); bg != 0 {
		e.Ref(r.GUID, bg, pkgBundle, 0)
		if owner != "" {
			e.Name(bg, "Bundle of "+owner, fmt.Sprintf("bundle of package %016x", r.GUID), false)
		}
	}
	return nil
}
