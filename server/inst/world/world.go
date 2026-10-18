// Package world is a match's world on the server: the entities that exist for
// everyone in the match, built once when the match is created (docs/MatchLogic.md
// §15) from the map's data (server/internal/maps, generated from content by
// systems/maps), plus the players' entities (controllers, hero bodies) as they
// join. The entities live in a server-side entity admin of the C++ ECS
// (systems/ecs via owecs), ticked once per command frame; the World keeps the
// replication-facing facts about each entity (what it is made from, team, owner,
// transform) next to it.
//
// A World is not safe for concurrent use: the game loop owns it.
package world

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"owecs/ecs"
	"owserver/internal/maps"
)

// Kind is what an entity is.
type Kind uint8

const (
	KindPlacement  Kind = iota + 1 // one of the map's networked placements (from-map create)
	KindController                 // a player's controller (definition 0x0E)
	KindHero                       // a player's hero body (the hero's gameplay entity)
	KindBrain                      // the gamemode brain (definition create; its graphs run on the server, inst/game)
	KindObjective                  // a server copy of an objective placement (from-map create; inst/game)
)

func (k Kind) String() string {
	switch k {
	case KindPlacement:
		return "placement"
	case KindController:
		return "controller"
	case KindHero:
		return "hero"
	case KindBrain:
		return "brain"
	case KindObjective:
		return "objective"
	}
	return fmt.Sprint(uint8(k))
}

// NoTeam is Entity.Team for entities without a team.
const NoTeam = -1

// Entity is one networked entity of the world.
type Entity struct {
	ID        uint32          `json:"id"` // the client's ID (0x40000000 | n)
	Kind      Kind            `json:"-"`
	KindName  string          `json:"kind"`
	Placement *maps.Placement `json:"-"`
	MapIndex  uint32          `json:"map_index,omitempty"`
	Def       uint32          `json:"def,omitempty"`   // entity definition index (0x0400000000000000 | Def)
	Owner     uint32          `json:"owner,omitempty"` // the player's connection (controller, hero)
	Team      int             `json:"team"`
	Hero      uint64          `json:"hero,omitempty"`
	Pos       [3]float32      `json:"pos"`
	Yaw       float32         `json:"yaw"`
	Health    float32         `json:"health,omitempty"`
	Mover     bool            `json:"mover,omitempty"` // has a movement state (0x12)
}

// Server-side component sets per kind [?]: which components the server's ECS gives
// an entity is the server's own model (the client builds its entities from the
// definitions); these are the components the server writes or reads for it.
var kindComponents = map[Kind][]byte{
	KindPlacement:  {0x01},                         // scene (transform)
	KindController: {0x10, 0x20, 0x3f},             // filter bits, possession links, PlayerInfo
	KindHero:       {0x01, 0x10, 0x12, 0x23, 0x28}, // scene, filter bits, movement, Statescript, health
	KindBrain:      {0x23},                         // Statescript (the graphs run in inst/game's match world)
	KindObjective:  {0x01, 0x23},                   // scene, Statescript
}

// World is one match's world.
type World struct {
	MapID   uint64
	Map     *maps.Map
	Created time.Time

	ecs    *ecs.Admin
	ents   map[uint32]*Entity
	order  []uint32
	next   uint32 // last entity number handed out
	starts int    // start spawn points handed out
	Events uint64 // ECS trace events drained (stats)
	Placed int    // map placements created
	Teamed int    // placements with a team (spawn points)
	Ticks  uint64
	log    *slog.Logger
}

// New builds a map's world: the map's networked placements (the client's system 8
// list: placements whose definition has a spawn component; the client finishes the
// local player only when all of them exist), with the spawn points' teams (from the
// map's STUECSpawnInstanceData, docs/Maps.md §4).
func New(mapID uint64, log *slog.Logger) (*World, error) {
	admin, err := ecs.NewAdmin("game")
	if err != nil {
		return nil, err
	}
	w := &World{MapID: mapID, Created: time.Now(), ecs: admin, ents: map[uint32]*Entity{}, log: log}
	m, ok := maps.ByData(mapID)
	if !ok {
		log.Warn("world: map not in the catalogue, no map entities", "map", fmt.Sprintf("%#016x", mapID))
		m = &maps.Map{Data: mapID, Name: fmt.Sprintf("%#016x", mapID)}
	}
	w.Map = m
	teams := map[uint32]int{}
	for _, sp := range m.Spawns {
		teams[sp.Index] = sp.Team
	}
	for i := range m.Networked {
		p := &m.Networked[i]
		team, ok := teams[p.Index]
		if !ok {
			team = NoTeam
		} else {
			w.Teamed++
		}
		if _, err := w.Add(Entity{Kind: KindPlacement, Placement: p, MapIndex: p.Index, Def: uint32(p.Def), Team: team, Pos: p.Pos, Yaw: p.Yaw}); err != nil {
			admin.Close()
			return nil, err
		}
		w.Placed++
	}
	log.Info("world: created", "map", fmt.Sprintf("%#016x", mapID), "name", m.Name, "placements", w.Placed, "teamed", w.Teamed, "movers_not_placed", len(m.Movers))
	return w, nil
}

// Close frees the world's entity admin.
func (w *World) Close() {
	if w.ecs != nil {
		w.ecs.Close()
		w.ecs = nil
	}
}

// Add adds an entity with the next free ID (IDs are never reused within a world, so
// a new entity never takes the ID of one the client is still destroying).
func (w *World) Add(e Entity) (*Entity, error) {
	w.next++
	e.ID = 0x40000000 | w.next
	e.KindName = e.Kind.String()
	if got := w.ecs.Spawn(e.ID, kindComponents[e.Kind], 0); got != e.ID {
		return nil, fmt.Errorf("world: ECS spawn of %#x (%s) failed", e.ID, e.Kind)
	}
	p := &e
	w.ents[e.ID] = p
	w.order = append(w.order, e.ID)
	return p, nil
}

// Remove deletes an entity.
func (w *World) Remove(id uint32) error {
	if _, ok := w.ents[id]; !ok {
		return errors.New("world: no such entity")
	}
	delete(w.ents, id)
	for i, o := range w.order {
		if o == id {
			w.order = append(w.order[:i], w.order[i+1:]...)
			break
		}
	}
	if !w.ecs.RequestDelete(id, true) {
		w.log.Warn("world: ECS delete refused", "entity", fmt.Sprintf("%#x", id))
	}
	return nil
}

// Get returns an entity (nil if there is none).
func (w *World) Get(id uint32) *Entity { return w.ents[id] }

// Entities returns the entities in creation order.
func (w *World) Entities() []*Entity {
	out := make([]*Entity, 0, len(w.order))
	for _, id := range w.order {
		out = append(out, w.ents[id])
	}
	return out
}

// Placements returns the map's entities (what every joining client is sent).
func (w *World) Placements() []*Entity {
	var out []*Entity
	for _, id := range w.order {
		if e := w.ents[id]; e.Kind == KindPlacement {
			out = append(out, e)
		}
	}
	return out
}

// ECSEntities is the entity count of the server's entity admin.
func (w *World) ECSEntities() int { return w.ecs.EntityCount() }

// ECSComponents is an entity's component list in the entity admin.
func (w *World) ECSComponents(id uint32) []byte { return w.ecs.Components(id) }

// Tick runs the entity admin for one command frame and drains its trace.
func (w *World) Tick() {
	w.ecs.Tick(float32(16.0 / 1000))
	w.Events += uint64(len(w.ecs.TakeEvents()))
	w.Ticks++
}

// playerTeam is the team every player gets for now (the spawn data's attacking team).
const playerTeam = maps.TeamAttack

// PlayerTeam is the team players get: the attackers, or on a map without an
// attackers' start room (the tutorial: one spawn point, team 0) the team of the room
// they spawn in.
func (w *World) PlayerTeam() int {
	if len(w.Map.StartSpawns(playerTeam)) == 0 {
		if room := w.Map.StartSpawns(maps.TeamDefend); len(room) > 0 {
			return room[0].Team
		}
	}
	return playerTeam
}

// NextStart hands out the next start spawn point of the players' team (players in one
// match start on different points; docs/Maps.md "Spawn rooms").
func (w *World) NextStart() (maps.Spawn, bool) {
	room := w.Map.StartSpawns(w.PlayerTeam())
	if len(room) == 0 {
		return maps.Spawn{}, false
	}
	sp := room[w.starts%len(room)]
	w.starts++
	return sp, true
}
