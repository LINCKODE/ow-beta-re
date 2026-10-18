// Package mapview turns a map data blob (resource type 0x002) into the explorer's map view: placements with
// their definition/model names, a top-down plot, the referenced assets, and per-record details (decoded
// fields and the entities' instance data). Parsing: systems/maps (owmaps/maps); format: docs/Maps.md §2.
package mapview

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"owmaps/maps"
	"owstu/stu"
)

// Resolver names GUIDs and gives access to entity definitions (the catalog implements it).
type Resolver interface {
	GUIDName(guid uint64) string
	Exists(guid uint64) bool
	WithDoc(guid uint64, f func(d *stu.Document) error) error
}

// Placement is one record as the map view lists and plots it.
type Placement struct {
	I       int        `json:"i"` // record index (MapRecord)
	Type    int        `json:"type"`
	Placed  int        `json:"placed"` // placed-entry index (-1 below type 8)
	Flags   int        `json:"flags"`
	GUID    string     `json:"guid,omitempty"` // primary: definition / model / effect / sound / string
	Name    string     `json:"name,omitempty"`
	Look    string     `json:"look,omitempty"` // secondary: look / font / skin
	Pos     [3]float32 `json:"pos"`
	Yaw     float32    `json:"yaw"`
	HasPos  bool       `json:"hasPos"`
	Class   string     `json:"class"`             // networked, mover, gameplay, prop, static, … (see classify)
	Group   int        `json:"group,omitempty"`   // model group: instance count
	Overrides []string `json:"overrides,omitempty"` // entity: instance-data STU types
	Offset  uint32     `json:"offset"`
}

// Point is an extra plot point (model group instances), compact.
type Point struct {
	I int        `json:"i"` // owning record
	X float32    `json:"x"`
	Y float32    `json:"y"` // 3D mode
	Z float32    `json:"z"`
}

type Asset struct {
	GUID  string `json:"guid"`
	Type  uint32 `json:"type"`
	Count int    `json:"count"`
	Name  string `json:"name,omitempty"`
	Missing bool `json:"missing,omitempty"`
}

type EnvRow struct {
	Label string `json:"label"`
	GUID  string `json:"guid"`
	Name  string `json:"name,omitempty"`
}

type View struct {
	GUID       string          `json:"guid"`
	Size       int             `json:"size"`
	Placed     int             `json:"placed"`
	Sections   []maps.Section  `json:"sections"`
	Collision  maps.Collision  `json:"collision"`
	Env        []EnvRow        `json:"env"`
	SunDir     [3]float32      `json:"sunDir"`
	Camera     [4]float32      `json:"camera"`
	Bounds     []maps.Bounds   `json:"bounds"`
	NavSize    uint32          `json:"navSize"`
	TactSize   uint32          `json:"tacticalSize"`
	SoundLinks int             `json:"soundLinks"`
	Placements []Placement     `json:"placements"`
	Points     []Point         `json:"points"`
	Assets     []Asset         `json:"assets"`
	Classes    map[string]int  `json:"classes"`
}

// Detail is one record: its decoded fields and instance data.
type Detail struct {
	Placement
	Size         int           `json:"size"`
	UUID         string        `json:"uuid"`
	Fields       []FieldRow    `json:"fields"`
	InstanceData []BlobView    `json:"instanceData"`
	Shapes       []maps.Shape  `json:"shapes,omitempty"`
	Instances    int           `json:"instances"`
}

type FieldRow struct {
	Name  string `json:"name"`
	At    string `json:"at"`
	Value string `json:"value"`
	GUID  string `json:"guid,omitempty"`
	RefName string `json:"refName,omitempty"`
}

type BlobView struct {
	Slot   uint32 `json:"slot"`
	Offset string `json:"offset"`
	Size   uint32 `json:"size"`
	Types  []string `json:"types"`
	JSON   string `json:"json"`
}

// STU types of entity-definition components that classify placements (docs/Maps.md §3/§4).
const (
	stuSpawnComponent     = 0xe880f3c4 // runtime 0x2e
	stuSpawnPointInfo     = 0x86ed8f46 // runtime 0x73
	stuMovementState      = 0xd25da2bc // runtime 0x12
	stuMirroredAnim       = 0x9d8ea225
	stuDynamicPathdataObs = 0x1b54380e
)

// Map is an opened map: the parsed data plus caches for the view.
type Map struct {
	GUID  uint64
	Bytes []byte
	M     *maps.Map // with fields
	defs  map[uint64]string
	geo   *maps.Geometry // collision geometry, decoded on first use (geometry.go)
}

// Open parses a map data blob.
func Open(guid uint64, b []byte) (*Map, error) {
	d, err := maps.Open(b)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	m, err := d.Map(true, true)
	if err != nil {
		return nil, err
	}
	return &Map{GUID: guid, Bytes: b, M: m, defs: map[uint64]string{}}, nil
}

func hex16(g uint64) string { return fmt.Sprintf("%016x", g) }

func yaw(q [4]float32) float32 {
	x, y, z, w := float64(q[0]), float64(q[1]), float64(q[2]), float64(q[3])
	return float32(math.Atan2(2*(w*y+x*z), 1-2*(y*y+x*x)))
}

// defClass classifies an entity definition: networked (the server creates it), mover (has a movement
// state), or gameplay.
func (m *Map) defClass(r Resolver, def uint64) string {
	if c, ok := m.defs[def]; ok {
		return c
	}
	c := "entity"
	_ = r.WithDoc(def, func(d *stu.Document) error {
		var net, mover bool
		for _, in := range d.Instances() {
			switch {
			case stu.IsA(in.Type, stuSpawnComponent) || stu.IsA(in.Type, stuSpawnPointInfo):
				net = true
			case stu.IsA(in.Type, stuMovementState) || stu.IsA(in.Type, stuMirroredAnim) || stu.IsA(in.Type, stuDynamicPathdataObs):
				mover = true
			}
		}
		switch {
		case net:
			c = "networked"
		case mover:
			c = "mover"
		}
		return nil
	})
	m.defs[def] = c
	return c
}

func (m *Map) placement(r Resolver, i int, rec *maps.Record) Placement {
	p := Placement{I: i, Type: rec.Type, Placed: rec.Placed, Flags: rec.Flags, Offset: uint32(rec.Offset)}
	if rec.Primary != 0 {
		p.GUID = hex16(uint64(rec.Primary))
		p.Name = r.GUIDName(uint64(rec.Primary))
	}
	if rec.Secondary != 0 && uint64(rec.Secondary) != math.MaxUint64 {
		p.Look = hex16(uint64(rec.Secondary))
	}
	if rec.Xf != nil {
		p.Pos, p.Yaw, p.HasPos = rec.Xf.Pos, yaw(rec.Xf.Rot), true
	}
	p.Group = len(rec.Instances)
	for _, id := range rec.InstanceData {
		p.Overrides = append(p.Overrides, m.blobTypes(id)...)
	}
	switch rec.Type {
	case maps.Entity:
		p.Class = m.defClass(r, uint64(rec.Primary))
		if p.Class == "entity" {
			if rec.Flags&1 != 0 {
				p.Class = "gameplay"
			} else {
				p.Class = "prop"
			}
		}
	case maps.ModelGroup, maps.Model:
		p.Class = "static"
	default:
		p.Class = strings.ReplaceAll(maps.TypeNames[rec.Type], " ", "_")
	}
	return p
}

func (m *Map) blob(id maps.InstanceData) []byte {
	end := uint64(id.Offset) + uint64(id.Size)
	if end > uint64(len(m.Bytes)) {
		return nil
	}
	return m.Bytes[id.Offset:end]
}

func (m *Map) blobTypes(id maps.InstanceData) []string {
	doc, err := stu.Open(m.blob(id), stu.File, 0)
	if err != nil {
		return nil
	}
	defer doc.Close()
	ins := doc.Instances()
	if len(ins) == 0 {
		return nil
	}
	return []string{stu.NameOr(ins[0].Type)}
}

// Build is the whole map view.
func (m *Map) Build(r Resolver) *View {
	h := m.M.Header
	v := &View{GUID: hex16(m.GUID), Size: m.M.Size, Placed: m.M.Placed, Sections: h.Sections, Collision: m.M.Collision,
		SunDir: h.Env.SunDir, Camera: h.CameraPos, Bounds: h.Bounds, NavSize: h.NavSize, TactSize: h.TacticalSize,
		SoundLinks: len(m.M.SoundSpaces), Classes: map[string]int{}, Placements: []Placement{}, Points: []Point{}}
	e := h.Env
	for _, row := range []struct {
		l string
		g maps.GUID
	}{{"gamemode brain (definition)", e.GamemodeDef}, {"lighting data", e.Lighting}, {"sky model", e.SkyModel},
		{"sky look", e.SkyLook}, {"default sound space", e.SoundSpace}, {"name", e.Name}, {"texture +0xF8", e.TextureF8},
		{"texture +0x108", e.Texture108}, {"texture +0x128", e.Texture128}, {"texture +0x130", e.Texture130},
		{"texture +0x180", e.Texture180}, {"texture +0x188", e.Texture188}, {"texture +0x190", e.Texture190}} {
		if row.g != 0 {
			v.Env = append(v.Env, EnvRow{row.l, hex16(uint64(row.g)), r.GUIDName(uint64(row.g))})
		}
	}
	assets := map[uint64]int{}
	for i := range m.M.Records {
		rec := &m.M.Records[i]
		p := m.placement(r, i, rec)
		v.Placements = append(v.Placements, p)
		v.Classes[p.Class]++
		for k, in := range rec.Instances {
			if k > 0 {
				v.Points = append(v.Points, Point{i, in.Xf.Pos[0], in.Xf.Pos[1], in.Xf.Pos[2]})
			}
			assets[uint64(in.Look)]++
		}
		for _, f := range rec.Fields {
			if s, ok := f[2].(string); ok && len(s) == 18 && strings.HasPrefix(s, "0x") {
				var g uint64
				fmt.Sscanf(s[2:], "%x", &g)
				if g != 0 && g != math.MaxUint64 && g>>60 == 0 && g>>48 != 0 {
					assets[g]++
				}
			}
		}
	}
	for g, n := range assets {
		v.Assets = append(v.Assets, Asset{GUID: hex16(g), Type: maps.GUID(g).Type(), Count: n, Name: r.GUIDName(g), Missing: !r.Exists(g)})
	}
	sort.Slice(v.Assets, func(i, j int) bool {
		if v.Assets[i].Type != v.Assets[j].Type {
			return v.Assets[i].Type < v.Assets[j].Type
		}
		return v.Assets[i].Count > v.Assets[j].Count
	})
	return v
}

// Record is the detail of record i.
func (m *Map) Record(r Resolver, i int) (*Detail, error) {
	if i < 0 || i >= len(m.M.Records) {
		return nil, fmt.Errorf("record %d out of range", i)
	}
	rec := &m.M.Records[i]
	d := &Detail{Placement: m.placement(r, i, rec), Size: int(rec.Size), UUID: rec.UUID, Shapes: rec.Shapes,
		Instances: len(rec.Instances), Fields: []FieldRow{}, InstanceData: []BlobView{}}
	for _, f := range rec.Fields {
		name, _ := f[0].(string)
		at, _ := f[1].(string)
		row := FieldRow{Name: name, At: at}
		switch x := f[2].(type) {
		case string:
			row.Value = x
			if len(x) == 18 && strings.HasPrefix(x, "0x") {
				var g uint64
				fmt.Sscanf(x[2:], "%x", &g)
				if g != 0 && g != math.MaxUint64 && g>>48 != 0 {
					row.GUID = hex16(g)
					row.RefName = r.GUIDName(g)
				}
			}
		case []any:
			parts := make([]string, len(x))
			for k, y := range x {
				parts[k] = fmt.Sprintf("%.4g", y)
			}
			row.Value = "(" + strings.Join(parts, ", ") + ")"
		default:
			row.Value = fmt.Sprint(x)
		}
		d.Fields = append(d.Fields, row)
	}
	for _, id := range rec.InstanceData {
		bv := BlobView{Slot: id.Slot, Offset: fmt.Sprintf("0x%x", uint64(id.Offset)), Size: id.Size, Types: []string{}}
		if doc, err := stu.Open(m.blob(id), stu.File, 0); err == nil {
			for _, in := range doc.Instances() {
				bv.Types = append(bv.Types, stu.NameOr(in.Type))
			}
			bv.JSON = doc.JSON(false)
			doc.Close()
		}
		d.InstanceData = append(d.InstanceData, bv)
	}
	return d, nil
}
