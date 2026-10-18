// Package maps is the map catalogue of build 24919, generated offline from the install by
// systems/data/maps/tools/mapgen (maps_gen.go; the full catalogue is systems/data/maps/maps.json).
// Format and meaning: docs/Maps.md.
//
// The lobby uses it for the MAP pickers (23300) and 20600; the game server for the entities it
// creates from the map (from-map creates of the networked placements, movers) and for the residency
// guard (Resident: which entity definitions the client has loaded on the map, docs/InstanceServer.md §12.15).
package maps

import (
	"slices"
	"strconv"
	"strings"
)

// Map is one map: its STUMapHeader and what its map data places.
type Map struct {
	Header uint64 // STUMapHeader, 0x079...
	Data   uint64 // m_map, 0x080...: the GUID 20600 and 23300 carry
	Name   string // m_displayName (enUS)
	Kind   string // "game", "tutorial", "lobby" (title-screen variant), "test"
	Mode   string // game maps: from the placed objectives ("assault", "escort", "hybrid"); "tutorial"; "" = none
	Placed int    // size of the client's placed-entity list (records of type >= 8)
	// Brain is the gamemode brain definition the map's environment names (map data +0xF0): the entity the
	// server is expected to create for the match (Anubis 0x39, tutorial 0x128E, Dorado 0x12E3).
	Brain uint64
	// Defs: the entity definitions of the map's asset package, sorted. With Global, what the client has
	// loaded on the map (a slight over-approximation: a few package definitions stay unloaded).
	Defs []uint64

	// Networked: the entity placements whose definition has a spawn component (STUSpawnComponent,
	// runtime 0x2e, or 0x86ed8f46, runtime 0x73). The client does not create them itself and
	// finishes the local player only once the server has created all of them (system 8's list).
	Networked []Placement
	// Movers: entity placements with a movement state (0x12, also pulled in by STUMirroredAnim 0x1c
	// and 0x1b54380e): the client creates them at the origin until a server places them
	// (docs/MatchLogic.md §8).
	Movers []Placement
	// Spawns: the spawn points (networked placements with STUECSpawnInstanceData), in rooms of 6.
	Spawns []Spawn
	// Objectives: the placements a match server copies and runs (docs/InstanceServer.md §12): capture points and
	// the placements whose Statescript overrides reference one (their rings).
	Objectives []Objective
}

// Resident reports whether the client has entity definition def (0x040...) loaded on this map: it is in
// Global or in the map's package (docs/InstanceServer.md §12.15). Creating an entity from a definition that is
// not loaded is a client assert (the create handler FUN_7ff650c00d20, §12.14).
func (m *Map) Resident(def uint64) bool {
	if _, ok := slices.BinarySearch(Global, def); ok {
		return true
	}
	_, ok := slices.BinarySearch(m.Defs, def)
	return ok
}

// Objective is an objective placement with the placement's Statescript overrides and capture rules.
type Objective struct {
	Index     uint32
	Def       uint64
	Pos       [3]float32
	Yaw       float32
	Mover     bool     // has a movement state: its from-map create needs a movement snapshot
	Graphs    []uint64 // the placement's Statescript graphs (m_graphOverrides)
	Overrides []Override
	Capture   *CaptureRules
}

// Override is a Statescript schema override: Var := a primitive (Type 1 BYTE, 2 INT, 3 FLT, 4 INT64 (resource
// GUIDs [?]), 11 ENTITY = the placement with placed index Ref, -1 if not found) in the instance of Graph.
type Override struct {
	Graph uint64
	Var   uint64
	Type  int
	Bits  uint64
	Ref   int
}

// CaptureRules is the placement's STUECCaptureAreaInstanceData (names are our reading [?],
// docs/StatescriptNodes.md §6.3).
type CaptureRules struct {
	DecayTime, F349aaca6, Segments, ExtraPlayer, CaptureTime, F5f080cc5, DecayDelay float32
	F2ca7fc17                                                                       int32
	Fe6e7f301                                                                       float32
	F3a691fdf                                                                       bool
	InitialTeam                                                                     uint32
}

// Objective returns the objective placement with this placed index.
func (m *Map) Objective(index uint32) (*Objective, bool) {
	for i := range m.Objectives {
		if m.Objectives[i].Index == index {
			return &m.Objectives[i], true
		}
	}
	return nil, false
}

// Spawn is a spawn point (docs/Maps.md "Spawn rooms"). Team, From and Until are read from the map
// data; what they mean is inferred from where the rooms are.
type Spawn struct {
	Index    uint32 // placed index (also in Networked)
	Team     int    // TeamAttack or TeamDefend
	Priority int    // m_spawnPriority
	Flag     int    // 0x2b91afbe (1 on forward rooms)
	From     int    // first objective progress the room is used at
	Until    int    // progress it stops at (0 = to the end)
	Room     int    // room number within the map
	Pos      [3]float32
	Yaw      float32
}

// Spawn teams (STUECSpawnInstanceData 0xcccec698): team 1's start room is where the attackers
// begin on every map (behind the first objective), team 0's behind the last one [inferred].
const (
	TeamDefend = 0
	TeamAttack = 1
)

// StartSpawns is the team's start room: its spawns active at objective progress 0 (From 0), the
// highest priority first. Nil if the map has none.
func (m *Map) StartSpawns(team int) []Spawn {
	best := -1
	for i, s := range m.Spawns {
		if s.Team == team && s.From == 0 && (best < 0 || s.Priority > m.Spawns[best].Priority) {
			best = i
		}
	}
	if best < 0 {
		return nil
	}
	var out []Spawn
	for _, s := range m.Spawns {
		if s.Room == m.Spawns[best].Room {
			out = append(out, s)
		}
	}
	return out
}

// Placement is one entity record of the map data.
type Placement struct {
	Index uint32     // index in the placed-entity list: what a from-map create sends
	Def   uint64     // entity definition 0x040...
	Pos   [3]float32 // position
	Rot   [4]float32 // quaternion x, y, z, w
	Yaw   float32    // rotation about +Y (radians), what a movement snapshot carries
}

// Anubis is Temple of Anubis, the default map.
const Anubis = 0x080000000000005B

// Tutorial is the tutorial map (kind "tutorial"; 24001 type 4 loads it, docs/MatchLogic.md "Tutorial").
const Tutorial = 0x0800000000000661

// ByData returns the map with this map data GUID (m_map).
func ByData(data uint64) (*Map, bool) {
	for i := range All {
		if All[i].Data == data {
			return &All[i], true
		}
	}
	return nil, false
}

// Playable is every map of kind "game", in catalogue order.
func Playable() []*Map {
	var out []*Map
	for i := range All {
		if All[i].Kind == "game" {
			out = append(out, &All[i])
		}
	}
	return out
}

// PlayableData is Playable's map data GUIDs, Anubis first (the pickers' default).
func PlayableData() []uint64 {
	out := []uint64{Anubis}
	for _, m := range Playable() {
		if m.Data != Anubis {
			out = append(out, m.Data)
		}
	}
	return out
}

// Find resolves a map argument: a GUID (0x080..., 0x079... header, or the short index like 0x5b /
// 5b), or a name (case and punctuation ignored, a unique prefix of a word sequence is enough:
// "anubis", "kings row", "gibraltar"). Playable maps win over lobby variants of the same name.
func Find(arg string) (*Map, bool) {
	a := strings.TrimSpace(arg)
	if a == "" {
		return nil, false
	}
	if v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(a), "0x"), 16, 64); err == nil {
		switch {
		case v>>52 == 0x079:
			v = v&0xffffffff | 0x0800000000000000
		case v < 1<<32:
			v |= 0x0800000000000000
		}
		if m, ok := ByData(v); ok {
			return m, true
		}
	}
	want := norm(a)
	var best *Map
	for i := range All {
		m := &All[i]
		n := norm(m.Name)
		if n == "" || !(n == want || strings.Contains(" "+n, " "+want)) {
			continue
		}
		if best == nil || (best.Kind != "game" && m.Kind == "game") {
			best = m
		}
	}
	return best, best != nil
}

// norm lowercases and keeps letters/digits, words separated by one space.
func norm(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		case r == '\'':
		default:
			space = true
		}
	}
	return b.String()
}

// Label is "NAME (0x080...)".
func (m *Map) Label() string {
	return m.Name + " (0x" + strconv.FormatUint(m.Data, 16) + ")"
}
