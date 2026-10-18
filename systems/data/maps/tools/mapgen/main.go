// Command mapgen builds the map catalogue of build 24919 offline (T035, docs/Maps.md).
//
// It reads every STUMapHeader (GUIDs 0x079..., resource type 0x09F) from the install, its map data
// (m_map, 0x080..., type 0x002) and the entity definitions (0x040..., type 0x003) the map places, and
// writes:
//
//	systems/data/maps/maps.json           every map: GUIDs, names, header fields, placement sections,
//	                                      the networked placements and the movers
//	server/internal/maps/maps_gen.go      the same tables for the servers (lobby lists, 20600,
//	                                      /setup's from-map creates, the residency guard)
//
// The global resident set (definitions resident on every map) is read from the measured files in
// systems/data/maps/resident (tools/resident.py; docs/InstanceServer.md §12.15).
//
// Build systems/content and systems/stu first (cmake, see their READMEs), then:
//
//	cd systems/data/maps/tools/mapgen && go run .            # or: go run . -install DIR -check
package main

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	"owcontent/content"
	"owstu/stu"
)

// STU types of entity-definition components that decide how a placement is treated.
const (
	stuSpawnComponent     = 0xe880f3c4 // runtime component 0x2e
	stuSpawnPointInfo     = 0x86ed8f46 // runtime component 0x73 (no OWLib name)
	stuMovementState      = 0xd25da2bc // runtime 0x12
	stuMirroredAnim       = 0x9d8ea225 // runtime 0x1c, depends on 0x12
	stuDynamicPathdataObs = 0x1b54380e // runtime 0x2a, depends on 0x12
)

// Placement record types (record +0x13). Only records of type >= firstPlacedType are in the client's
// placed-entity list (the list the server's from-map creates index); 0x0b = entity.
const (
	firstPlacedType = 8
	typeEntity      = 0x0b
	numSections     = 16 // record types 0..15; header +0x210 offsets, +0x250 counts
)

func main() {
	root := repoRoot()
	install := flag.String("install", filepath.Join(root, "game/owbetaclient"), "0.8 install directory")
	outJSON := flag.String("json", filepath.Join(root, "systems/data/maps/maps.json"), "catalogue output")
	outGo := flag.String("go", filepath.Join(root, "server/internal/maps/maps_gen.go"), "Go tables output")
	strs := flag.String("strings", filepath.Join(root, "systems/data/lobby_scripts/strings_enUS.tsv"), "string table (GUID<TAB>text)")
	resident := flag.String("resident", filepath.Join(root, "systems/data/maps/resident"), "measured resident definition sets (title, anubis, tutorial .json)")
	flag.Parse()

	names, err := loadStrings(*strs)
	if err != nil {
		log.Fatal(err)
	}
	global, err := globalSet(*resident)
	if err != nil {
		log.Fatal(err)
	}
	s, err := content.Open(*install, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	g := &gen{s: s, names: names, defs: map[uint64]*defInfo{}}
	headers := s.ResourcesOfType(content.TypeOf(0x0790000000000000))
	var maps []*Map
	for _, h := range headers {
		m, err := g.mapOf(h.GUID)
		if err != nil {
			log.Fatalf("map header %#016x: %v", h.GUID, err)
		}
		maps = append(maps, m)
		log.Printf("%#016x %-8s %-8s %-24q %6d placements, %3d networked, %3d movers, brain %#x, %3d package defs", m.Data, m.Kind, m.Mode,
			m.Name, m.Placed, len(m.Networked), len(m.Movers), uint64(m.Brain), len(m.Defs))
	}

	b, err := json.MarshalIndent(catalogue{Source: "systems/data/maps/tools/mapgen (T035); docs/Maps.md", Global: global, Maps: maps}, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*outJSON, append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(*outGo), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*outGo, goTables(maps, global), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s and %s (%d maps)", *outJSON, *outGo, len(maps))
}

// repoRoot is new-stuff/, found from this file's location (go run keeps it).
func repoRoot() string {
	_, f, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(f), "../../../../.."))
}

func loadStrings(path string) (map[uint64]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := map[uint64]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "\t")
		if !ok {
			continue
		}
		if g, err := strconv.ParseUint(strings.TrimPrefix(k, "0x"), 16, 64); err == nil {
			m[g] = v
		}
	}
	return m, sc.Err()
}

// --- output -------------------------------------------------------------------------------------

type catalogue struct {
	Source string  `json:"source"`
	Global []hex64 `json:"global_defs"` // resident on every map (measured, docs/InstanceServer.md §12.15)
	Maps   []*Map  `json:"maps"`
}

type hex64 uint64

func (h hex64) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("\"0x%016x\"", uint64(h))), nil
}

type hex32 uint32

func (h hex32) MarshalJSON() ([]byte, error) { return []byte(fmt.Sprintf("\"0x%x\"", uint32(h))), nil }

// Map is one STUMapHeader and what its map data places.
type Map struct {
	Header        hex64             `json:"header"`         // STUMapHeader 0x079...
	Data          hex64             `json:"map"`            // m_map, 0x080...: what 20600 / 23300 carry
	Data2         hex64             `json:"map2,omitempty"` // field 0xccf37178: 0x080001... (another map data variant)
	Data2Present  bool              `json:"map2_in_install"`
	Name          string            `json:"name"`    // m_displayName
	Kind          string            `json:"kind"`    // game, tutorial, lobby (title-screen variant), test
	Mode          string            `json:"mode"`    // game maps: from the placed objectives (assault, escort, hybrid); "tutorial"; "" = none
	Brain         hex64             `json:"brain"`   // the environment's gamemode brain definition (map data +0xF0)
	Defs          []hex64           `json:"defs"`    // the entity definitions of the map's asset package (sorted)
	Strings       map[string]string `json:"strings"` // every string reference of the header: field -> "GUID text"
	MapType       string            `json:"map_type"`
	HeaderFields  map[string]any    `json:"header_fields"` // the header's other non-reference fields, raw
	Refs          map[string]string `json:"refs"`          // the header's other references (textures, sounds)
	DataSize      int               `json:"data_size"`
	Sections      []section         `json:"sections"`   // placement record types in file order
	Placed        int               `json:"placed"`     // records of type >= 8: the client's placed-entity list
	Entities      int               `json:"entities"`   // type 0x0b records
	ObjectiveDefs map[string]int    `json:"objectives"` // placements per gamemode-ish component (hints)
	Networked     []Placement       `json:"networked"`  // entity placements the client expects the server to create
	Spawns        []Spawn           `json:"spawns"`     // the spawn points (networked placements with STUECSpawnInstanceData)
	Movers        []Placement       `json:"movers"`     // entity placements with a movement state (0x12)
	// Objectives: the placements a match server runs itself (docs/InstanceServer.md §12): capture points and the
	// placements whose Statescript overrides reference one (their rings), with the overrides and capture rules.
	Objectives []Objective `json:"objective_placements,omitempty"`
	rooms      int
	uuids      map[string]int // placement record UUID (hex) -> placed index
	candidates []Objective
}

// Objective is a placement a match server copies and runs (T057).
type Objective struct {
	Index    hex32         `json:"index"`
	Def      hex64         `json:"def"`
	Pos      [3]float32    `json:"pos"`
	Yaw      float32       `json:"yaw"`
	Mover    bool          `json:"mover,omitempty"` // has a movement state: the from-map create needs a snapshot
	Graphs   []hex64       `json:"graphs"`          // m_graphOverrides' graphs (the definition's Statescript graphs)
	Override []Override    `json:"overrides,omitempty"`
	Capture  *CaptureRules `json:"capture,omitempty"`
	refUUIDs []string
}

// Override is one Statescript schema override of a placement: var := value (primitive type 1 BYTE, 2 INT, 3 FLT,
// 4 INT64 (resource GUIDs), 11 ENTITY = the placement at Ref).
type Override struct {
	Graph hex64  `json:"graph"`
	Var   hex64  `json:"var"`
	Type  int    `json:"type"`
	Bits  uint64 `json:"bits"`
	Ref   int    `json:"ref,omitempty"` // ENTITY: the referenced placed index (-1 = not found)
	From  string `json:"from"`          // the config var's STU type
	uuid  string
}

// CaptureRules is STUECCaptureAreaInstanceData (names are our reading [?], docs/StatescriptNodes.md §6.3).
type CaptureRules struct {
	DecayTime, F349aaca6, Segments, ExtraPlayer, CaptureTime, F5f080cc5, DecayDelay float32
	F2ca7fc17                                                                       int32
	Fe6e7f301                                                                       float32
	F3a691fdf                                                                       bool
	InitialTeam                                                                     uint32
}

type section struct {
	Type   int   `json:"type"`
	Offset hex32 `json:"offset"`
	Count  int   `json:"count"`
}

// Placement is one entity record (type 0x0b) of the map data.
type Placement struct {
	Index      hex32      `json:"index"` // position in the placed-entity list (from-map create)
	Def        hex64      `json:"def"`   // entity definition 0x040...
	Pos        [3]float32 `json:"pos"`
	Scale      [3]float32 `json:"scale"`
	Rot        [4]float32 `json:"rot"` // quaternion x, y, z, w
	Yaw        float32    `json:"yaw"` // rotation about +Y, what the movement snapshot carries
	Components []string   `json:"components"`
	// InstanceData: the placement's per-component overrides (embedded STUD blobs), by type name.
	InstanceData []string `json:"instance_data,omitempty"`
}

// Spawn is a spawn point: a placement with STUECSpawnInstanceData, grouped into rooms.
type Spawn struct {
	Index    hex32      `json:"index"`
	Team     int        `json:"team"`     // STUECSpawnInstanceData 0xcccec698[0]: 1 = attackers, 0 = defenders [inferred]
	Priority int        `json:"priority"` // m_spawnPriority
	Flag     int        `json:"flag"`     // 0x2b91afbe (1 on forward / later rooms; meaning unknown)
	From     int        `json:"from"`     // graph 0x0D6 var 0x0D8...2B9 (default 0): first objective progress the room is used at [inferred]
	Until    int        `json:"until"`    // var 0x0D8...2BA (default 0 = open end): progress the room stops at [inferred]
	Room     int        `json:"room"`     // rooms numbered per map in placement order: same team, priority, flag, from, until
	Pos      [3]float32 `json:"pos"`
	Yaw      float32    `json:"yaw"`
}

// --- generation ---------------------------------------------------------------------------------

type gen struct {
	s     *content.Store
	names map[uint64]string
	defs  map[uint64]*defInfo
}

type defInfo struct {
	types      []uint32
	components []string
}

func (d *defInfo) has(t uint32) bool {
	return slices.ContainsFunc(d.types, func(x uint32) bool { return stu.IsA(x, t) })
}

func (g *gen) def(guid uint64) (*defInfo, error) {
	if d, ok := g.defs[guid]; ok {
		return d, nil
	}
	b, err := g.s.Read(guid)
	if err != nil {
		return nil, err
	}
	doc, err := stu.Open(b, stu.File, 0)
	if err != nil {
		return nil, err
	}
	defer doc.Close()
	d := &defInfo{}
	for _, in := range doc.Instances() {
		d.types = append(d.types, in.Type)
		if n := stu.NameOr(in.Type); strings.HasSuffix(n, "Component") || strings.HasPrefix(n, "0x") {
			d.components = append(d.components, n)
		}
	}
	g.defs[guid] = d
	return d, nil
}

func (g *gen) mapOf(header uint64) (*Map, error) {
	b, err := g.s.Read(header)
	if err != nil {
		return nil, err
	}
	doc, err := stu.Open(b, stu.File, 0)
	if err != nil {
		return nil, err
	}
	defer doc.Close()
	var fields map[string]json.RawMessage
	var root struct {
		Fields map[string]json.RawMessage `json:"$fields"`
	}
	if err := json.Unmarshal([]byte(doc.Decode(doc.Root()).JSON(false)), &root); err != nil {
		return nil, err
	}
	fields = root.Fields

	m := &Map{Header: hex64(header), Strings: map[string]string{}, HeaderFields: map[string]any{}, Refs: map[string]string{}}
	for k, raw := range fields {
		var ref struct {
			GUID string `json:"$guid"`
		}
		if json.Unmarshal(raw, &ref) == nil && ref.GUID != "" {
			gu, _ := strconv.ParseUint(strings.TrimPrefix(ref.GUID, "0x"), 16, 64)
			switch {
			case k == "m_map":
				m.Data = hex64(gu)
			case k == "0xccf37178":
				m.Data2 = hex64(gu)
			case gu>>48 == 0x0de0:
				m.Strings[k] = fmt.Sprintf("0x%016x %s", gu, g.names[gu])
			case gu != 0:
				m.Refs[k] = fmt.Sprintf("0x%016x", gu)
			}
			continue
		}
		var v any
		_ = json.Unmarshal(raw, &v)
		if k == "m_mapType" {
			m.MapType = fmt.Sprint(v)
			continue
		}
		if e, ok := v.(map[string]any); ok && e["$error"] != nil {
			continue // teStrings (m_mapName, m_description, 0x39824653): the reader can't decode them yet
		}
		m.HeaderFields[k] = v
	}
	if dn, ok := fields["m_displayName"]; ok {
		var ref struct {
			GUID string `json:"$guid"`
		}
		if json.Unmarshal(dn, &ref) == nil {
			gu, _ := strconv.ParseUint(strings.TrimPrefix(ref.GUID, "0x"), 16, 64)
			m.Name = g.names[gu]
		}
	}
	if m.Data2 != 0 {
		_, m.Data2Present = g.s.Find(uint64(m.Data2))
	}
	if m.Data == 0 {
		return nil, errors.New("no m_map")
	}
	data, err := g.s.Read(uint64(m.Data))
	if err != nil {
		return nil, fmt.Errorf("map data %#016x: %w", uint64(m.Data), err)
	}
	m.DataSize = len(data)
	if err := g.placements(m, data); err != nil {
		return nil, err
	}
	m.Brain = hex64(binary.LittleEndian.Uint64(data[0xF0:]))
	// Residency (docs/InstanceServer.md §12.15): the definitions of the package that lists the map data.
	if pkgs := g.s.ResourcePackages(uint64(m.Data)); len(pkgs) > 0 {
		for _, r := range g.s.PackageResources(pkgs[0]) {
			if r.GUID>>48 == 0x0400 {
				m.Defs = append(m.Defs, hex64(r.GUID))
			}
		}
		slices.Sort(m.Defs)
		m.Defs = slices.Compact(m.Defs)
	}
	return m, nil
}

// globalSet is the definitions resident on every map: those resident at the title screen, on Anubis and on the
// tutorial (measured files; the title screen's extra ones come from the title map's package).
func globalSet(dir string) ([]hex64, error) {
	var sets []map[uint64]bool
	for _, name := range []string{"title", "anubis", "tutorial"} {
		b, err := os.ReadFile(filepath.Join(dir, name+".json"))
		if err != nil {
			return nil, err
		}
		var v []string
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		set := map[uint64]bool{}
		for _, s := range v {
			x, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			set[x] = true
		}
		sets = append(sets, set)
	}
	var out []hex64
	for x := range sets[0] {
		if sets[1][x] && sets[2][x] {
			out = append(out, hex64(x))
		}
	}
	slices.Sort(out)
	return out, nil
}

// placements walks the map data's placement sections (docs/Maps.md §2):
//
//	+0x20C u32 total record count
//	+0x210 u32 offset[16]   per record type 0..15 (0 = none)
//	+0x250 u32 count[16]
//	record: UUID[16], u8 flags +0x10, +0x13 u8 type, +0x14 u32 size (the next record follows)
//	entity (0x0b): +0x18 u64 definition, +0x20 vec3 position, +0x2C vec3 scale, +0x38 quat (x,y,z,w)
func (g *gen) placements(m *Map, d []byte) error {
	le := binary.LittleEndian
	if len(d) < 0x300 {
		return errors.New("map data too short")
	}
	total := int(le.Uint32(d[0x20C:]))
	sum := 0
	placed := 0
	m.ObjectiveDefs = map[string]int{}
	for t := 0; t < numSections; t++ {
		off := int(le.Uint32(d[0x210+4*t:]))
		n := int(le.Uint32(d[0x250+4*t:]))
		sum += n
		if n == 0 {
			continue
		}
		m.Sections = append(m.Sections, section{Type: t, Offset: hex32(off), Count: n})
		for j := 0; j < n; j++ {
			if off+0x18 > len(d) {
				return fmt.Errorf("type %d record %d past the end", t, j)
			}
			if rt := int(d[off+0x13]); rt != t {
				return fmt.Errorf("type %d record %d at %#x has type %d", t, j, off, rt)
			}
			size := int(le.Uint32(d[off+0x14:]))
			if size < 0x18 || off+size > len(d) {
				return fmt.Errorf("type %d record %d at %#x: bad size %#x", t, j, off, size)
			}
			index := -1
			if t >= firstPlacedType {
				index = placed
				placed++
			}
			if t == typeEntity {
				m.Entities++
				if err := g.entity(m, d[off:off+size], index); err != nil {
					return fmt.Errorf("entity record %d at %#x: %w", j, off, err)
				}
			}
			off += size
		}
	}
	if sum != total {
		return fmt.Errorf("section counts sum to %d, header says %d", sum, total)
	}
	m.Placed = placed
	m.Kind, m.Mode = classify(m)
	// Objectives: capture areas, and placements with an entity reference to one (resolved by record UUID).
	captures := map[int]bool{}
	for _, o := range m.candidates {
		if o.Capture != nil {
			captures[int(o.Index)] = true
		}
	}
	for _, o := range m.candidates {
		keep := o.Capture != nil
		for i := range o.Override {
			ov := &o.Override[i]
			if ov.Type != 11 {
				continue
			}
			ov.Ref = -1
			if ix, ok := m.uuids[ov.uuid]; ok {
				ov.Ref = ix
				keep = keep || captures[ix]
			}
		}
		if keep {
			m.Objectives = append(m.Objectives, o)
		}
	}
	m.candidates, m.uuids = nil, nil
	return nil
}

// classify: Kind from the GUID blocks (the title screen's maps are 0x664-0x67B, docs/TitleScreen.md
// §2.3 names three of them; the others in the block are the same maps cut down the same way [?]); Mode
// of game maps from the objective components the map places (0.8 has no control maps).
func classify(m *Map) (kind, mode string) {
	switch id := uint64(m.Data) & 0xffffffff; {
	case id == 0x661:
		kind = "tutorial"
	case id >= 0x662 && id <= 0x67b:
		kind = "lobby"
	case m.Name == "":
		kind = "test"
	default:
		kind = "game"
	}
	if kind == "tutorial" {
		return kind, "tutorial" // its own brain (0x128E), not the assault one its capture point suggests
	}
	if kind != "game" {
		return kind, "" // title-screen variants and test maps host no match
	}
	capture, payload := m.ObjectiveDefs["STUCaptureAreaComponent"] > 0, m.ObjectiveDefs["STUPayloadComponent"] > 0
	switch {
	case capture && payload:
		mode = "hybrid"
	case payload:
		mode = "escort"
	case capture:
		mode = "assault"
	}
	return kind, mode
}

func (g *gen) entity(m *Map, r []byte, index int) error {
	le := binary.LittleEndian
	if len(r) < 0x48 {
		return fmt.Errorf("entity record of %#x bytes", len(r))
	}
	f := func(o int) float32 { return math.Float32frombits(le.Uint32(r[o:])) }
	p := Placement{
		Index: hex32(index),
		Def:   hex64(le.Uint64(r[0x18:])),
		Pos:   [3]float32{f(0x20), f(0x24), f(0x28)},
		Scale: [3]float32{f(0x2C), f(0x30), f(0x34)},
		Rot:   [4]float32{f(0x38), f(0x3C), f(0x40), f(0x44)},
	}
	p.Yaw = yaw(p.Rot)
	obj := &Objective{Index: p.Index, Def: p.Def, Pos: p.Pos, Yaw: p.Yaw}
	sp, err := g.instanceData(&p, r, obj)
	if err != nil {
		return err
	}
	di, err := g.def(uint64(p.Def))
	if err != nil {
		return fmt.Errorf("definition %#016x: %w", uint64(p.Def), err)
	}
	if m.uuids == nil {
		m.uuids = map[string]int{}
	}
	m.uuids[hex.EncodeToString(r[:16])] = index
	obj.Mover = di.has(stuMovementState) || di.has(stuMirroredAnim) || di.has(stuDynamicPathdataObs)
	if obj.Capture != nil || len(obj.refUUIDs) > 0 {
		m.candidates = append(m.candidates, *obj)
	}
	p.Components = di.components
	for _, c := range di.components {
		for _, k := range []string{"Capture", "Payload", "Escort", "Checkpoint", "PvPGame", "GameMode", "Spawn"} {
			if strings.Contains(c, k) {
				m.ObjectiveDefs[c]++
			}
		}
	}
	if di.has(stuSpawnComponent) || di.has(stuSpawnPointInfo) {
		m.Networked = append(m.Networked, p)
		if sp != nil {
			sp.Index, sp.Pos, sp.Yaw = p.Index, p.Pos, p.Yaw
			sp.Room = -1
			for _, o := range m.Spawns {
				if o.Team == sp.Team && o.Priority == sp.Priority && o.Flag == sp.Flag && o.From == sp.From && o.Until == sp.Until {
					sp.Room = o.Room
				}
			}
			if sp.Room < 0 {
				sp.Room = m.rooms
				m.rooms++
			}
			m.Spawns = append(m.Spawns, *sp)
		}
	}
	if di.has(stuMovementState) || di.has(stuMirroredAnim) || di.has(stuDynamicPathdataObs) {
		m.Movers = append(m.Movers, p)
	}
	return nil
}

// Spawn configuration: STUECSpawnInstanceData fields and the spawn graph's config vars.
const (
	stuECSpawnInstanceData = "STUECSpawnInstanceData"
	fieldSpawnTeams        = "0xcccec698"
	fieldSpawnFlag         = "0x2b91afbe"
	cvSpawnFrom            = "0x0d800000000002b9"
	cvSpawnUntil           = "0x0d800000000002ba"
)

// instanceData decodes the placement's instance-data references: the record ends with count (+0x48)
// pairs {u32 component, u32 offset}; the offset is relative to the pair and points at a STUD blob
// (+0x08 u32 the offset of its instance table) holding the overrides for that component. Returns the spawn configuration if
// the placement has STUECSpawnInstanceData.
func (g *gen) instanceData(p *Placement, r []byte, obj *Objective) (*Spawn, error) {
	le := binary.LittleEndian
	n := int(le.Uint32(r[0x48:]))
	if 0x4C+8*n > len(r) {
		return nil, fmt.Errorf("%d instance-data refs in a %#x-byte record", n, len(r))
	}
	var sp *Spawn
	vars := map[string]int{}
	full := r[:cap(r)] // r is a slice of the map data; the blobs lie after the placement sections
	for k := range n {
		pos := len(r) - 8*n + 8*k
		start := pos + int(le.Uint32(r[pos+4:]))
		if start+0x10 > len(full) {
			return nil, fmt.Errorf("instance data %d at +%#x past the end", k, start)
		}
		if string(full[start:start+4]) != "DUTS" {
			return nil, fmt.Errorf("instance data %d at +%#x: not a STUD blob", k, start)
		}
		// +0x08 is the instance table's offset; the table follows it. Hand the reader a generous
		// window (blobs are small and packed back to back; it only reads what the table names).
		doc, err := stu.Open(full[start:min(len(full), start+int(le.Uint32(full[start+8:]))+0x1000)], stu.File, 0)
		if err != nil {
			return nil, fmt.Errorf("instance data %d: %w", k, err)
		}
		var js struct {
			Root []struct {
				Type  string `json:"type"`
				Value struct {
					Fields map[string]json.RawMessage `json:"$fields"`
				} `json:"value"`
			} `json:"root"`
		}
		if err := json.Unmarshal([]byte(doc.JSON(false)), &js); err != nil {
			doc.Close()
			return nil, err
		}
		doc.Close()
		entryGraph := map[int]hex64{}
		for i, in := range js.Root {
			if i == 0 {
				p.InstanceData = append(p.InstanceData, in.Type)
			}
			f := in.Value.Fields
			switch in.Type {
			case stuECSpawnInstanceData:
				sp = &Spawn{}
				var teams []string
				_ = json.Unmarshal(f[fieldSpawnTeams], &teams)
				if len(teams) > 0 {
					t, _ := strconv.ParseUint(strings.TrimPrefix(teams[0], "0x"), 16, 32)
					sp.Team = int(t)
				}
				_ = json.Unmarshal(f["m_spawnPriority"], &sp.Priority)
				_ = json.Unmarshal(f[fieldSpawnFlag], &sp.Flag)
			case "STUECCaptureAreaInstanceData":
				c := &CaptureRules{}
				num := func(k string) float32 {
					var v float32
					_ = json.Unmarshal(f[k], &v)
					return v
				}
				c.DecayTime, c.F349aaca6, c.Segments = num("0xa0b01a58"), num("0x349aaca6"), num("0xa3a59991")
				c.ExtraPlayer, c.CaptureTime, c.F5f080cc5 = num("0x95fb5638"), num("0xbef90de8"), num("0x5f080cc5")
				c.DecayDelay, c.F2ca7fc17, c.Fe6e7f301 = num("0xfdfb30ac"), int32(num("0x2ca7fc17")), num("0xe6e7f301")
				c.F3a691fdf = num("0x3a691fdf") != 0
				var team string
				_ = json.Unmarshal(f["m_initialTeam"], &team)
				t, _ := strconv.ParseUint(strings.TrimPrefix(team, "0x"), 16, 32)
				c.InitialTeam = uint32(t)
				obj.Capture = c
			case "STUStatescriptComponentInstanceData":
				// m_graphOverrides: [{m_graph, 0x953d7942: [schema entry instances]}]
				var gos []struct {
					Fields struct {
						Graph struct {
							GUID string `json:"$guid"`
						} `json:"m_graph"`
						Entries []struct {
							Instance int `json:"$instance"`
						} `json:"0x953d7942"`
					} `json:"$fields"`
				}
				_ = json.Unmarshal(f["m_graphOverrides"], &gos)
				for _, g := range gos {
					gu, _ := strconv.ParseUint(strings.TrimPrefix(g.Fields.Graph.GUID, "0x"), 16, 64)
					obj.Graphs = append(obj.Graphs, hex64(gu))
					for _, e := range g.Fields.Entries {
						entryGraph[e.Instance] = hex64(gu)
					}
				}
			case "STUStatescriptSchemaEntry":
				// {0x4f6799b5: config var GUID, m_value: instance of the value}
				var id struct {
					GUID string `json:"$guid"`
				}
				var val struct {
					Instance int `json:"$instance"`
				}
				_ = json.Unmarshal(f["0x4f6799b5"], &id)
				if json.Unmarshal(f["m_value"], &val) == nil && val.Instance < len(js.Root) {
					vf := js.Root[val.Instance].Value.Fields
					var v int
					if json.Unmarshal(vf["m_value"], &v) == nil {
						vars[id.GUID] = v
					}
					vu, _ := strconv.ParseUint(strings.TrimPrefix(id.GUID, "0x"), 16, 64)
					if ov, ok := override(js.Root[val.Instance].Type, vf); ok {
						ov.Graph, ov.Var = entryGraph[i], hex64(vu)
						obj.Override = append(obj.Override, ov)
						if ov.Type == 11 {
							obj.refUUIDs = append(obj.refUUIDs, ov.uuid)
						}
					}
				}
			}
		}
	}
	if sp != nil {
		sp.From, sp.Until = vars[cvSpawnFrom], vars[cvSpawnUntil]
	}
	return sp, nil
}

// override reads a schema override's config var as a primitive: Int, Bool, Float, resource GUIDs (display text,
// 0xa0dd8e27 view model refs: INT64 [?]) and entity references (ENTITY, resolved to a placement later). Other config
// var types (expressions, dynamic vars) are left to the graph's default.
func override(typ string, f map[string]json.RawMessage) (Override, bool) {
	ov := Override{From: typ}
	switch typ {
	case "STUConfigVarInt":
		var v int64
		if json.Unmarshal(f["m_value"], &v) != nil {
			return ov, false
		}
		ov.Type, ov.Bits = 2, uint64(uint32(int32(v)))
	case "STUConfigVarBool":
		var v int
		if json.Unmarshal(f["m_value"], &v) != nil {
			return ov, false
		}
		ov.Type, ov.Bits = 1, uint64(v&1)
	case "STUConfigVarFloat":
		var v float32
		if json.Unmarshal(f["m_value"], &v) != nil {
			return ov, false
		}
		ov.Type, ov.Bits = 3, uint64(math.Float32bits(v))
	case "STUConfigVarEntityReference":
		if json.Unmarshal(f["m_entityRef"], &ov.uuid) != nil || ov.uuid == "" {
			return ov, false
		}
		ov.Type = 11
	default:
		for _, raw := range f { // a resource reference: its GUID
			var r struct {
				GUID string `json:"$guid"`
			}
			if json.Unmarshal(raw, &r) == nil && r.GUID != "" {
				gu, _ := strconv.ParseUint(strings.TrimPrefix(r.GUID, "0x"), 16, 64)
				ov.Type, ov.Bits = 4, gu
				return ov, true
			}
		}
		return ov, false
	}
	return ov, true
}

// yaw is the rotation about +Y of a quaternion (x, y, z, w), in radians.
func yaw(q [4]float32) float32 {
	x, y, z, w := float64(q[0]), float64(q[1]), float64(q[2]), float64(q[3])
	return float32(math.Atan2(2*(w*y+x*z), 1-2*(y*y+x*x)))
}

// --- Go tables ----------------------------------------------------------------------------------

func goTables(maps []*Map, global []hex64) []byte {
	var b strings.Builder
	b.WriteString("// Code generated by systems/data/maps/tools/mapgen; DO NOT EDIT.\n\npackage maps\n\n")
	b.WriteString("// Global is every entity definition resident on every map (measured, docs/InstanceServer.md §12.15), sorted.\n")
	fmt.Fprintf(&b, "var Global = %s\n\n", guids(global, "\t"))
	b.WriteString("// All is every map of build 24919 (one per STUMapHeader), sorted by map data GUID.\n")
	b.WriteString("var All = []Map{\n")
	sorted := slices.Clone(maps)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Data < sorted[j].Data })
	pl := func(ps []Placement) string {
		if len(ps) == 0 {
			return "nil"
		}
		var s strings.Builder
		s.WriteString("[]Placement{\n")
		for _, p := range ps {
			fmt.Fprintf(&s, "\t\t\t{%#x, %#016x, [3]float32{%s}, [4]float32{%s}, %s},\n", uint32(p.Index), uint64(p.Def),
				floats(p.Pos[:]), floats(p.Rot[:]), fl(p.Yaw))
		}
		s.WriteString("\t\t}")
		return s.String()
	}
	for _, m := range sorted {
		fmt.Fprintf(&b, "\t{\n\t\tHeader: %#016x, Data: %#016x, Name: %q, Kind: %q, Mode: %q, Placed: %d,\n",
			uint64(m.Header), uint64(m.Data), m.Name, m.Kind, m.Mode, m.Placed)
		fmt.Fprintf(&b, "\t\tBrain: %#016x,\n", uint64(m.Brain))
		fmt.Fprintf(&b, "\t\tDefs: %s,\n", guids(m.Defs, "\t\t\t"))
		fmt.Fprintf(&b, "\t\tNetworked: %s,\n", pl(m.Networked))
		fmt.Fprintf(&b, "\t\tMovers: %s,\n", pl(m.Movers))
		if len(m.Objectives) > 0 {
			b.WriteString("\t\tObjectives: []Objective{\n")
			for _, o := range m.Objectives {
				fmt.Fprintf(&b, "\t\t\t{Index: %#x, Def: %#016x, Pos: [3]float32{%s}, Yaw: %s, Mover: %v,\n", uint32(o.Index),
					uint64(o.Def), floats(o.Pos[:]), fl(o.Yaw), o.Mover)
				gs := make([]string, len(o.Graphs))
				for i, gr := range o.Graphs {
					gs[i] = fmt.Sprintf("%#016x", uint64(gr))
				}
				fmt.Fprintf(&b, "\t\t\t\tGraphs: []uint64{%s},\n", strings.Join(gs, ", "))
				if len(o.Override) > 0 {
					b.WriteString("\t\t\t\tOverrides: []Override{\n")
					for _, ov := range o.Override {
						fmt.Fprintf(&b, "\t\t\t\t\t{Graph: %#016x, Var: %#016x, Type: %d, Bits: %#x, Ref: %d}, // %s\n",
							uint64(ov.Graph), uint64(ov.Var), ov.Type, ov.Bits, ov.Ref, ov.From)
					}
					b.WriteString("\t\t\t\t},\n")
				}
				if c := o.Capture; c != nil {
					fmt.Fprintf(&b, "\t\t\t\tCapture: &CaptureRules{DecayTime: %s, F349aaca6: %s, Segments: %s, ExtraPlayer: %s, CaptureTime: %s, F5f080cc5: %s, DecayDelay: %s, F2ca7fc17: %d, Fe6e7f301: %s, F3a691fdf: %v, InitialTeam: %d},\n",
						fl(c.DecayTime), fl(c.F349aaca6), fl(c.Segments), fl(c.ExtraPlayer), fl(c.CaptureTime), fl(c.F5f080cc5),
						fl(c.DecayDelay), c.F2ca7fc17, fl(c.Fe6e7f301), c.F3a691fdf, c.InitialTeam)
				}
				b.WriteString("\t\t\t},\n")
			}
			b.WriteString("\t\t},\n")
		}
		if len(m.Spawns) > 0 {
			b.WriteString("\t\tSpawns: []Spawn{\n")
			for _, sp := range m.Spawns {
				fmt.Fprintf(&b, "\t\t\t{%#x, %d, %d, %d, %d, %d, %d, [3]float32{%s}, %s},\n", uint32(sp.Index), sp.Team, sp.Priority,
					sp.Flag, sp.From, sp.Until, sp.Room, floats(sp.Pos[:]), fl(sp.Yaw))
			}
			b.WriteString("\t\t},\n")
		}
		b.WriteString("\t},\n")
	}
	b.WriteString("}\n")
	return []byte(b.String())
}

// guids is a []uint64 literal, 6 per line, the lines indented by ind (the closing brace one tab less).
func guids(v []hex64, ind string) string {
	if len(v) == 0 {
		return "nil"
	}
	var s strings.Builder
	s.WriteString("[]uint64{")
	for i, x := range v {
		if i%6 == 0 {
			s.WriteString("\n" + ind)
		} else {
			s.WriteString(" ")
		}
		fmt.Fprintf(&s, "%#016x,", uint64(x))
	}
	s.WriteString("\n" + ind[:len(ind)-1] + "}")
	return s.String()
}

func fl(f float32) string { return strconv.FormatFloat(float64(f), 'g', -1, 32) }

func floats(fs []float32) string {
	s := make([]string, len(fs))
	for i, f := range fs {
		s[i] = fl(f)
	}
	return strings.Join(s, ", ")
}
