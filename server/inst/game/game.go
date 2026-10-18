// Package game is a match's rules host (docs/InstanceServer.md §12): the gamemode brain and the server copies of
// the map's objective placements (capture points and their rings) run their Statescript graphs in a match world of
// the Statescript runtime (statescript.Match, C API ss_match_*); the host pushes the world's facts in (players'
// heroes: position, team), sends the rules layer's game messages (init, phases), steps it with the match's frame and
// applies the graphs' effects (teams, capture state) to the match's world. What the client is sent is the caller's
// (replication, stage 3).
//
// A Game is not safe for concurrent use: the game loop owns it (package match).
package game

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"

	"owserver/inst/sim"
	"owserver/inst/world"
	"owserver/internal/maps"

	"owstatescript/statescript"
	"owstu/stu"
)

// Engine is the process's Statescript runtime with graph loading (sim.Engine).
type Engine interface {
	Runtime() *statescript.Runtime
	Load(guid uint64) error
	Source() sim.GraphSource
	// Corrects: whether the server sends states of type t in a format (docs/InstanceServer.md §10.3).
	Corrects(t uint32, local bool) (bool, string)
}

// Config tunes the rules layer; the zero value works.
type Config struct {
	// SetupFrames is how long the setup phase lasts before the assault starts (default 10 s [?: retail setup was
	// longer, docs/InstanceServer.md §12.8]).
	SetupFrames uint32
	// Radius of the capture zones in metres (default 6 [?: the zone shape asset is not read]).
	Radius float32
	// Seed of the world's random numbers (IntRandom).
	Seed uint32
}

// Game messages and parameters of the rules layer (docs/InstanceServer.md §12.8; the graphs' own ids [D]).
const (
	msgInit      = 0x024000000000000e // to each capture point: {0x…10 owning team}
	msgPhase     = 0x024000000000002d // to all: {0x…29 phase identifier}
	paramOwner   = 0x0A40000000000010
	paramPhase   = 0x0A40000000000029
	phaseSetup   = 0x0D80000000000016
	phaseAssault = 0x0D80000000000015
	phaseEnded   = 0x0D80000000000018
	varPointNo   = 0x0D8000000000008d // a capture point's order (0 = A)
)

// Phase is where the rules layer is.
type Phase string

const (
	Waiting    Phase = "waiting"     // no player has locked in yet
	Setup      Phase = "setup"       // 0x…2d {0x…16}
	InProgress Phase = "in progress" // 0x…2d {0x…15}
	Ended      Phase = "ended"       // 0x…2d {0x…18}
)

// brains: the gamemode brain definition per mode whose rules this host runs (MatchLogic.md §10.5, §12.1)
// [?, strong for assault]. A map is hosted only if its own brain (maps.Map.Brain, the environment's +0xF0) is
// that definition: other assault maps name their own (Hanamura 0x1FA, Volskaya 0xEDC), which the client has
// loaded instead of 0x39, and the tutorial (mode "tutorial") names 0x128E (docs/InstanceServer.md §12.15).
var brains = map[string]uint64{"assault": 0x0400000000000039}

// Point is a capture point copy's state as the capture graph left it (for replication: component 0x1a).
type Point struct {
	ID        uint32  `json:"id"`
	Index     uint32  `json:"map_index"`
	Order     int     `json:"order"` // $008d: 0 = A
	Enabled   bool    `json:"enabled"`
	Owner     int     `json:"owner"`
	Capturing int     `json:"capturing_team"`
	Contested bool    `json:"contested"`
	Progress  float32 `json:"progress"`
	Dirty     bool    `json:"-"` // changed since the replication last took it
}

// Entity is a server-run entity of the game (brain or objective copy).
type Entity struct {
	ID     uint32
	Kind   world.Kind
	Def    uint64
	Graphs []uint64
	Obj    *maps.Objective // objective copies: the placement
}

// Game is one match's rules host.
type Game struct {
	log     *slog.Logger
	eng     Engine
	lay     map[uint64]*layouts
	cfg     Config
	w       *world.World
	m       *statescript.Match
	Phase   Phase
	phaseAt uint32
	ents    []*Entity
	points  map[uint32]*Point
	heroes  map[uint32]bool
	// Counters and the last effects (control API).
	Effects   map[string]int
	Recent    []string
	Unhandled map[string]int
}

// New sets up the game for a match's world at frame: the brain of the map's mode and a copy of every objective
// placement, their graphs started with the placements' overrides, and the init message to every capture point.
// A map without a known mode brain, or whose own brain is another definition, gets no game (nil, nil).
func New(log *slog.Logger, eng Engine, w *world.World, frame uint32, cfg Config) (*Game, error) {
	brainDef, ok := brains[w.Map.Mode]
	if !ok {
		log.Info("game: no rules host for this map's mode", "map", w.Map.Name, "mode", w.Map.Mode,
			"map_brain", fmt.Sprintf("%#016x", w.Map.Brain))
		return nil, nil
	}
	if w.Map.Brain != brainDef || !w.Map.Resident(brainDef) {
		log.Warn("game: the map names another brain than this mode's host runs: no rules host", "map", w.Map.Name,
			"mode", w.Map.Mode, "map_brain", fmt.Sprintf("%#016x", w.Map.Brain), "host_brain", fmt.Sprintf("%#016x", brainDef),
			"resident", w.Map.Resident(brainDef))
		return nil, nil
	}
	if cfg.SetupFrames == 0 {
		cfg.SetupFrames = 625 // 10 s
	}
	if cfg.Radius == 0 {
		cfg.Radius = 6
	}
	rt := eng.Runtime()
	// The graphs instantiate in the first steps; the world runs 2 frames ahead of the match until the match catches up
	// (Tick steps only frames ahead of the world's clock).
	g := &Game{log: log, eng: eng, lay: map[uint64]*layouts{}, cfg: cfg, w: w, m: rt.NewMatch(frame, cfg.Seed), Phase: Waiting, points: map[uint32]*Point{},
		heroes: map[uint32]bool{}, Effects: map[string]int{}, Unhandled: map[string]int{}}
	if g.m == nil {
		return nil, errors.New("game: no match world")
	}
	fail := func(err error) (*Game, error) {
		g.Close()
		return nil, err
	}
	// Ids for entities the graphs create: a block far above the world's own (world ids are sequential) [?: the
	// world adopting these ids is a follow-up].
	if err := g.m.ReserveIDs(0x40001000, 0x1000); err != nil {
		return fail(err)
	}

	// The brain: the gamemode entity (PvP game).
	graphs, err := defGraphs(eng, brainDef)
	if err != nil {
		return fail(fmt.Errorf("game: brain %#x: %w", brainDef, err))
	}
	be, err := w.Add(world.Entity{Kind: world.KindBrain, Def: uint32(brainDef), Team: world.NoTeam})
	if err != nil {
		return fail(err)
	}
	g.ents = append(g.ents, &Entity{ID: be.ID, Kind: world.KindBrain, Def: brainDef, Graphs: graphs})
	if err := g.m.Add(be.ID, brainDef, statescript.WithSyncManager|statescript.PvPGame); err != nil {
		return fail(err)
	}

	// The objective copies: world entities first (overrides reference each other by placed index).
	byIndex := map[uint32]uint32{}
	for i := range w.Map.Objectives {
		o := &w.Map.Objectives[i]
		oe, err := w.Add(world.Entity{Kind: world.KindObjective, MapIndex: o.Index, Def: uint32(o.Def), Team: world.NoTeam,
			Pos: o.Pos, Yaw: o.Yaw, Mover: o.Mover})
		if err != nil {
			return fail(err)
		}
		byIndex[o.Index] = oe.ID
		g.ents = append(g.ents, &Entity{ID: oe.ID, Kind: world.KindObjective, Def: o.Def, Graphs: o.Graphs, Obj: o})
		if err := g.m.Add(oe.ID, o.Def, statescript.WithSyncManager); err != nil {
			return fail(err)
		}
		if err := g.m.SetTransform(oe.ID, o.Pos, deg(o.Yaw)); err != nil {
			return fail(err)
		}
		if c := o.Capture; c != nil {
			err := g.m.SetCaptureArea(oe.ID, statescript.CaptureRules{DecayTime: c.DecayTime, F349aaca6: c.F349aaca6,
				Segments: c.Segments, ExtraPlayer: c.ExtraPlayer, CaptureTime: c.CaptureTime, F5f080cc5: c.F5f080cc5,
				DecayDelay: c.DecayDelay, F2ca7fc17: c.F2ca7fc17, Fe6e7f301: c.Fe6e7f301, F3a691fdf: c.F3a691fdf,
				MaxPlayers: 3, Radius: cfg.Radius})
			if err != nil {
				return fail(err)
			}
			pt := &Point{ID: oe.ID, Index: o.Index, Owner: -1, Capturing: -1}
			for _, ov := range o.Overrides {
				if ov.Var == varPointNo && ov.Type == 2 {
					pt.Order = int(int32(uint32(ov.Bits)))
				}
			}
			g.points[oe.ID] = pt
		}
	}

	// Graphs: the brain's, then each copy's with its overrides.
	for _, e := range g.ents {
		for _, gr := range e.Graphs {
			if err := eng.Load(gr); err != nil {
				return fail(fmt.Errorf("game: %s %#x: %w", e.Kind, e.ID, err))
			}
			ov := map[uint64]statescript.Value{}
			if e.Obj != nil {
				for _, o := range e.Obj.Overrides {
					if o.Graph != gr && o.Graph != 0 {
						continue
					}
					if v, ok := overrideValue(o, byIndex); ok {
						ov[o.Var] = v
					} else {
						log.Warn("game: override not applied", "entity", fmt.Sprintf("%#x", e.ID), "var", fmt.Sprintf("%#x", o.Var), "ref", o.Ref)
					}
				}
			}
			if _, err := g.m.Start(e.ID, gr, ov); err != nil {
				return fail(fmt.Errorf("game: start %#x on %#x: %w", gr, e.ID, err))
			}
		}
	}
	g.m.Step(frame + 2)
	// Init: the defenders (team 0) own every capture point.
	for _, pt := range g.points {
		if err := g.m.Send(pt.ID, msgInit, map[uint64]statescript.Value{paramOwner: {Type: statescript.Int, Bits: 0}}); err != nil {
			return fail(err)
		}
	}
	g.drain()
	log.Info("game: set up", "mode", w.Map.Mode, "brain", fmt.Sprintf("%#x", be.ID), "objectives", len(w.Map.Objectives),
		"capture_points", len(g.points))
	return g, nil
}

// overrideValue is a placement override as a primitive; ENTITY references name another objective placement, whose
// server copy they become.
func overrideValue(o maps.Override, byIndex map[uint32]uint32) (statescript.Value, bool) {
	if o.Type == 11 {
		id, ok := byIndex[uint32(o.Ref)]
		if o.Ref < 0 || !ok {
			return statescript.Value{}, false
		}
		return statescript.Value{Type: statescript.Entity, Bits: uint64(id)}, true
	}
	return statescript.Value{Type: statescript.Type(o.Type), Bits: o.Bits}, true
}

// defGraphs is every Statescript graph an entity definition references (its components' graph lists).
func defGraphs(eng Engine, def uint64) ([]uint64, error) {
	b, err := eng.Source().Read(def)
	if err != nil {
		return nil, err
	}
	doc, err := stu.Open(b, stu.File, 0)
	if err != nil {
		return nil, err
	}
	defer doc.Close()
	js := doc.JSON(false)
	var out []uint64
	seen := map[uint64]bool{}
	for i := 0; ; {
		k := strings.Index(js[i:], "\"0x058")
		if k < 0 || i+k+19 > len(js) {
			break
		}
		var g uint64
		if _, err := fmt.Sscanf(js[i+k+1:i+k+19], "0x%x", &g); err == nil && !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
		i += k + 19
	}
	if len(out) == 0 {
		return nil, errors.New("no Statescript graphs")
	}
	return out, nil
}

func deg(rad float32) float32 { return rad * 180 / math.Pi }

// Close frees the match world.
func (g *Game) Close() {
	if g.m != nil {
		g.m.Close()
		g.m = nil
	}
}

// Entities are the brain and the objective copies, in creation order.
func (g *Game) Entities() []*Entity { return g.ents }

// Radius is the capture zones' radius in metres (Config.Radius).
func (g *Game) Radius() float32 { return g.cfg.Radius }

// PhaseFrame is the match frame the current phase started at.
func (g *Game) PhaseFrame() uint32 { return g.phaseAt }

// Points are the capture points, A first.
func (g *Game) Points() []*Point {
	out := make([]*Point, 0, len(g.points))
	for _, p := range g.points {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// SyncManager is a server-run entity's sync manager (its snapshot feeds the remote updates), nil for others.
func (g *Game) SyncManager(id uint32) *statescript.SyncManager { return g.m.SyncManager(id) }

// Match is the match world (tests, control API).
func (g *Game) Match() *statescript.Match { return g.m }

// HeroSpawned: a player's hero body is in the world (lock-in): the graphs see it from now on.
func (g *Game) HeroSpawned(body, controller uint32, hero uint64) {
	e := g.w.Get(body)
	if e == nil || g.heroes[body] {
		return
	}
	if err := g.m.Add(body, 0x0400000000000000|uint64(e.Def), statescript.PlayerHero); err != nil {
		g.log.Warn("game: hero", "body", fmt.Sprintf("%#x", body), "err", err)
		return
	}
	g.m.SetPlayer(body, controller, hero)
	g.m.SetHealth(body, 0, e.Health, e.Health)
	g.heroes[body] = true
	g.push(body, e)
	g.m.PlayerEvent(statescript.PlayerJoined, body, 0)
}

// HeroRemoved: the body left the world.
func (g *Game) HeroRemoved(body uint32) {
	if !g.heroes[body] {
		return
	}
	g.m.PlayerEvent(statescript.PlayerLeft, body, 0)
	g.m.Remove(body)
	delete(g.heroes, body)
}

func (g *Game) push(id uint32, e *world.Entity) {
	g.m.SetTransform(id, e.Pos, deg(e.Yaw))
	g.m.SetTeam(id, e.Team)
}

// Send delivers a raw rules-layer message (control API): to one entity, or to all (target 0).
func (g *Game) Send(target uint32, msg uint64, params map[uint64]statescript.Value) error {
	return g.m.Send(target, msg, params)
}

// Tick runs frame: facts in (every hero's position and team from the world), the rules layer, one step, effects out.
func (g *Game) Tick(frame uint32) {
	for id := range g.heroes {
		if e := g.w.Get(id); e != nil {
			g.push(id, e)
		}
	}
	switch {
	case g.Phase == Waiting && len(g.heroes) > 0:
		g.phase(Setup, phaseSetup, frame)
	case g.Phase == Setup && frame-g.phaseAt >= g.cfg.SetupFrames:
		g.phase(InProgress, phaseAssault, frame)
	case g.Phase == InProgress && g.allCaptured():
		g.phase(Ended, phaseEnded, frame)
	}
	g.m.Step(frame)
	g.drain()
}

func (g *Game) phase(p Phase, id uint64, frame uint32) {
	g.Phase, g.phaseAt = p, frame
	g.log.Info("game: phase", "phase", p, "frame", frame)
	if err := g.m.Send(0, msgPhase, map[uint64]statescript.Value{paramPhase: {Type: statescript.Int64, Bits: id}}); err != nil {
		g.log.Warn("game: phase message", "err", err)
	}
}

// allCaptured: the last point (highest order) is captured.
func (g *Game) allCaptured() bool {
	ps := g.Points()
	return len(ps) > 0 && ps[len(ps)-1].Progress >= 1
}

// drain applies the graphs' effects to the world and the points.
func (g *Game) drain() {
	for _, fx := range g.m.Effects() {
		g.Effects[fx.Kind.String()]++
		if fx.Kind != statescript.FxCaptureProgress {
			g.note(fmt.Sprintf("cf %d %s %#x i0=%d i1=%d u=%#x", fx.CF, fx.Kind, fx.Entity, fx.I0, fx.I1, fx.U))
		}
		pt := g.points[fx.Entity]
		switch fx.Kind {
		case statescript.FxTeam:
			if e := g.w.Get(fx.Entity); e != nil {
				e.Team = int(fx.I0)
			}
		case statescript.FxCaptureEnabled:
			if pt != nil {
				pt.Enabled, pt.Dirty = fx.I0 != 0, true
			}
		case statescript.FxCaptureOwner:
			if pt != nil {
				pt.Owner, pt.Dirty = int(fx.I0), true
			}
		case statescript.FxCaptureProgress:
			if pt != nil {
				pt.Progress, pt.Capturing, pt.Contested, pt.Dirty = fx.F[0], int(fx.I0), fx.I1 != 0, true
			}
		case statescript.FxBroadcast, statescript.FxMatchTime:
		default:
			g.Unhandled[fx.Kind.String()]++
		}
	}
}

func (g *Game) note(s string) {
	g.Recent = append(g.Recent, s)
	if len(g.Recent) > 64 {
		g.Recent = g.Recent[len(g.Recent)-64:]
	}
}
