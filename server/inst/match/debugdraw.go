package match

import (
	"fmt"
	"math"
	"sort"

	"owserver/inst/world"
	"owserver/internal/maps"
)

// Debug shapes: the server's view of a match as world-space primitives, drawn in game by Prometheus'
// "Debug Draw" window through the engine's own PrimDraw (docs/DebugDrawing.md §3). Served by the control
// API (GET /debug/draw); positions are the client's world coordinates (metres, y up).

// Shape is one primitive. Kinds and their fields:
//
//	sphere    Pos = centre, Radius
//	box       Pos = centre, Half = half extents, Yaw (radians, about y)
//	capsule   Pos = centre, Radius, Height = distance between the two cap centres (y axis)
//	cylinder  Pos = centre of the base, Radius, Height (y axis)
//	line      Pos → To
type Shape struct {
	Group  string      `json:"group"`
	Kind   string      `json:"kind"`
	Pos    [3]float32  `json:"pos"`
	To     *[3]float32 `json:"to,omitempty"`
	Half   *[3]float32 `json:"half,omitempty"`
	Radius float32     `json:"radius,omitempty"`
	Height float32     `json:"height,omitempty"`
	Yaw    float32     `json:"yaw,omitempty"`
	Color  [4]float32  `json:"color"` // RGBA 0–1; alpha < 1 draws translucent
	Label  string      `json:"label,omitempty"`
}

// HUDLine is a line of screen text, drawn by Prometheus with the engine's debug font under the engine's
// debug overlays (docs/DebugDrawing.md §2.4).
type HUDLine struct {
	Group string     `json:"group"`
	Text  string     `json:"text"`
	Color [4]float32 `json:"color"`
}

// Prim2D is a screen-space primitive in pixels (origin top left): kind "rect" (Rect = x, y, w, h, filled)
// or "lines" (Points, a connected polyline).
type Prim2D struct {
	Group  string       `json:"group"`
	Kind   string       `json:"kind"`
	Rect   *[4]float32  `json:"rect,omitempty"`
	Points [][2]float32 `json:"points,omitempty"`
	Color  [4]float32   `json:"color"`
}

// DebugGroup names a group of shapes for the overlay's legend.
type DebugGroup struct {
	Name  string     `json:"name"`
	Label string     `json:"label"`
	Color [4]float32 `json:"color"`
}

// DebugDraw is the reply of GET /debug/draw.
type DebugDraw struct {
	Map    uint64       `json:"map"`
	Name   string       `json:"name"`
	Frame  uint32       `json:"frame"`
	Groups []DebugGroup `json:"groups"`
	Shapes []Shape      `json:"shapes"`
	HUD    []HUDLine    `json:"hud"`
	Screen []Prim2D     `json:"screen,omitempty"`
}

var (
	colAttack = [4]float32{1, 0.45, 0.1, 1}  // team 1
	colDefend = [4]float32{0.1, 0.7, 1, 1}   // team 0
	colOther  = [4]float32{0.8, 0.8, 0.8, 1} // no team
	colHero   = [4]float32{1, 0.1, 0.9, 1}
	colObj    = [4]float32{1, 1, 0.1, 1}
	colHUD    = [4]float32{0.6, 1, 0.6, 1}
)

func teamColor(team int, alpha float32) [4]float32 {
	c := colOther
	switch team {
	case maps.TeamAttack:
		c = colAttack
	case maps.TeamDefend:
		c = colDefend
	}
	c[3] = alpha
	return c
}

func teamName(team int) string {
	switch team {
	case maps.TeamAttack:
		return "att"
	case maps.TeamDefend:
		return "def"
	}
	return fmt.Sprint("team ", team)
}

// DebugShapes returns the debug shapes of the match of player conn (0: the first match).
func (m *Manager) DebugShapes(conn uint32) (*DebugDraw, error) {
	var mt *Match
	if conn != 0 {
		p := m.players[conn]
		if p == nil {
			return nil, fmt.Errorf("no such player %d", conn)
		}
		mt = p.match
	} else {
		keys := make([]uint64, 0, len(m.matches))
		for k := range m.matches {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		if len(keys) > 0 {
			mt = m.matches[keys[0]]
		}
	}
	out := &DebugDraw{Groups: []DebugGroup{
		{"heroes", "Hero bodies: where the server believes they are", colHero},
		{"spawns", "Spawn points (team colour, line = facing)", colAttack},
		{"rooms", "Spawn rooms (box around each room's 6 points)", colDefend},
		{"objectives", "Objective placements the server copies", colObj},
		{"capture", "Capture zones (server radius)", colObj},
		{"server", "HUD: the server's match and clock state", colHUD},
	}, Shapes: []Shape{}, HUD: []HUDLine{}}
	if mt == nil {
		out.HUD = append(out.HUD, HUDLine{"server", "SERVER: no match", colHUD})
		return out, nil
	}
	w := mt.World
	out.Map, out.Name, out.Frame = mt.Key, w.Map.Name, mt.Frame

	// Screen text: the match, the rules layer, every player's clock.
	hud := func(c [4]float32, f string, a ...any) {
		out.HUD = append(out.HUD, HUDLine{"server", fmt.Sprintf(f, a...), c})
	}
	hud(colHUD, "SERVER %s  frame %d", w.Map.Name, mt.Frame)
	if g := mt.Game; g != nil {
		hud(colHUD, "  phase %s", g.Phase)
		for _, p := range g.Points() {
			hud(teamColor(p.Capturing, 1), "  point %c: owner %d capturing %d %3.0f%%%s", 'A'+p.Order, p.Owner, p.Capturing,
				p.Progress*100, map[bool]string{true: "", false: " (off)"}[p.Enabled])
		}
	}
	for _, p := range m.Players() {
		if p.Map != mt.Key {
			continue
		}
		hud(colHUD, "  conn %d %s: lead %d (min %d) flags %#x resyncs %d", p.ConnID, p.State, p.Lead, p.MinLead, p.Flags, p.Resyncs)
	}

	// Spawn points and rooms.
	rooms := map[int][]maps.Spawn{}
	for _, s := range w.Map.Spawns {
		rooms[s.Room] = append(rooms[s.Room], s)
		c := s.Pos
		c[1] += 0.3
		to := [3]float32{c[0] + float32(math.Sin(float64(s.Yaw))), c[1], c[2] + float32(math.Cos(float64(s.Yaw)))}
		out.Shapes = append(out.Shapes,
			Shape{Group: "spawns", Kind: "sphere", Pos: c, Radius: 0.3, Color: teamColor(s.Team, 1),
				Label: fmt.Sprintf("spawn %d (%s p%d)", s.Index, teamName(s.Team), s.Priority)},
			Shape{Group: "spawns", Kind: "line", Pos: c, To: &to, Color: teamColor(s.Team, 1)})
	}
	for room, ss := range rooms {
		lo, hi := ss[0].Pos, ss[0].Pos
		for _, s := range ss[1:] {
			for i := 0; i < 3; i++ {
				lo[i] = min(lo[i], s.Pos[i])
				hi[i] = max(hi[i], s.Pos[i])
			}
		}
		const pad, height = 1.5, 3.0
		half := [3]float32{(hi[0]-lo[0])/2 + pad, height / 2, (hi[2]-lo[2])/2 + pad}
		centre := [3]float32{(lo[0] + hi[0]) / 2, lo[1] + height/2, (lo[2] + hi[2]) / 2}
		until := "end"
		if ss[0].Until != 0 {
			until = fmt.Sprint(ss[0].Until)
		}
		out.Shapes = append(out.Shapes, Shape{Group: "rooms", Kind: "box", Pos: centre, Half: &half,
			Color: teamColor(ss[0].Team, 0.25),
			Label: fmt.Sprintf("room %d: %s, progress %d–%s, priority %d", room, teamName(ss[0].Team), ss[0].From, until, ss[0].Priority)})
	}

	// World entities: hero bodies (the server's position) and objectives.
	for _, e := range w.Entities() {
		switch e.Kind {
		case world.KindHero:
			c := e.Pos
			c[1] += 0.9
			out.Shapes = append(out.Shapes, Shape{Group: "heroes", Kind: "capsule", Pos: c, Radius: 0.35, Height: 1.1,
				Color: colHero, Label: fmt.Sprintf("hero 0x%08x (conn %d, team %d)", e.ID, e.Owner, e.Team)})
		case world.KindObjective:
			half := [3]float32{0.4, 0.4, 0.4}
			c := e.Pos
			c[1] += 0.4
			out.Shapes = append(out.Shapes, Shape{Group: "objectives", Kind: "box", Pos: c, Half: &half, Yaw: e.Yaw,
				Color: colObj, Label: fmt.Sprintf("objective 0x%08x def 0x%x", e.ID, e.Def)})
		}
	}

	// Capture zones as the rules host sees them.
	if g := mt.Game; g != nil {
		for _, p := range g.Points() {
			e := w.Get(p.ID)
			if e == nil {
				continue
			}
			a := float32(0.2)
			if !p.Enabled {
				a = 0.08
			}
			out.Shapes = append(out.Shapes, Shape{Group: "capture", Kind: "cylinder", Pos: e.Pos, Radius: g.Radius(), Height: 3,
				Color: teamColor(p.Capturing, a),
				Label: fmt.Sprintf("point %c: owner %d, capturing %d, %.0f%%, enabled %v", 'A'+p.Order, p.Owner, p.Capturing, p.Progress*100, p.Enabled)})
		}
	}
	return out, nil
}
