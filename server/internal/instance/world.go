package instance

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"owserver/internal/maps"
)

// World is a match's world on the server: the entities that exist for everyone in
// the match. It is built once, when the match gets its first connection (Assign),
// and each client that joins receives it once its map has loaded (21602,
// docs/MatchLogic.md "Match init and the join / lock-in sequence"). Per-player
// entities (the controller, the hero body) are not part of it.
//
// A match is keyed by its map for now: the lobby's assignment carries no match id,
// and every connection on a map shares that map's world until the last one leaves.
type World struct {
	Map     uint64    `json:"map"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	// Entities are replicated in this order, one per packet: after a create the client
	// goes on reading past the blob, which is harmless zeros at the end of a packet but
	// the next entity's bits in a batch ("Failed to deserialize component update ... 0
	// of 16" assert in FUN_7ff650c00560, int3 at game+0xC20BDE, with 8 map entities per
	// packet).
	Entities []Spawn `json:"-"`
	Creates  int     `json:"creates"`
	Updates  int     `json:"updates"`

	conns     map[uint32]bool
	spawnsOut int // start spawn points handed out to hero bodies
}

// newWorld builds a map's world: the map's networked placements (the client's system
// 8 list: placements whose definition has a spawn component, from the offline
// catalogue, docs/Maps.md; the client finishes the local player only when all of them
// exist), then the spawn points' teams in their filter bits, like players (team from
// the map's STUECSpawnInstanceData, docs/Maps.md §4).
func newWorld(mapID uint64, log *slog.Logger) *World {
	w := &World{Map: mapID, Created: time.Now(), conns: map[uint32]bool{}}
	m, ok := maps.ByData(mapID)
	if !ok {
		log.Warn("world: map not in the catalogue, no map entities", "map", fmt.Sprintf("%#016x", mapID))
		m = &maps.Map{}
	}
	w.Name = m.Name
	ids := map[uint32]uint8{}
	for i, p := range m.Networked {
		id := uint8(firstMapEntity + i)
		ids[p.Index] = id
		w.Entities = append(w.Entities, Spawn{ID: id, FromMap: true, MapIndex: p.Index})
		w.Creates++
	}
	for _, sp := range m.Spawns {
		w.Entities = append(w.Entities, Spawn{ID: ids[sp.Index], Update: true, Components: []ComponentUpdate{Mirror10(TeamBit(sp.Team))}})
		w.Updates++
	}
	log.Info("world: created", "map", fmt.Sprintf("%#016x", mapID), "name", w.Name, "creates", w.Creates, "updates", w.Updates,
		"movers_not_placed", len(m.Movers))
	return w
}

// takeSpawn returns the index of the next start spawn point for a hero body, so that
// players in one match start on different spawn points.
func (w *World) takeSpawn() int {
	n := w.spawnsOut
	w.spawnsOut++
	return n
}

// replicate sends the world to a connection whose client has loaded the map.
func (s *Server) replicate(c *Conn, w *World, log *slog.Logger) error {
	for _, e := range w.Entities {
		if err := s.SendPacket(c.ConnID, nil, []Spawn{e}); err != nil {
			return err
		}
	}
	log.Info("setup: replicated the world", "map", w.Name, "creates", w.Creates, "updates", w.Updates)
	return nil
}

// joinWorld adds a connection to its map's world, building the world if the
// connection is the match's first; the caller holds s.mu.
func (s *Server) joinWorld(c *Conn) {
	if s.worlds == nil {
		s.worlds = map[uint64]*World{}
	}
	w := s.worlds[c.Map]
	if w == nil {
		w = newWorld(c.Map, s.Log)
		s.worlds[c.Map] = w
	}
	w.conns[c.ConnID] = true
	c.world = w
}

// leaveWorld removes a connection from its world and ends the world with its last
// connection; the caller holds s.mu.
func (s *Server) leaveWorld(c *Conn) {
	w := c.world
	if w == nil {
		return
	}
	delete(w.conns, c.ConnID)
	if len(w.conns) == 0 && s.worlds[w.Map] == w {
		delete(s.worlds, w.Map)
		s.Log.Info("world: ended (no connections left)", "map", fmt.Sprintf("%#016x", w.Map), "name", w.Name)
	}
}

// WorldInfo is a world as the control API lists it.
type WorldInfo struct {
	World
	Conns []uint32 `json:"conns"`
}

// Worlds lists the matches' worlds.
func (s *Server) Worlds() []WorldInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []WorldInfo{}
	for _, w := range s.worlds {
		wi := WorldInfo{World: *w}
		for id := range w.conns {
			wi.Conns = append(wi.Conns, id)
		}
		sort.Slice(wi.Conns, func(i, j int) bool { return wi.Conns[i] < wi.Conns[j] })
		out = append(out, wi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Map < out[j].Map })
	return out
}
