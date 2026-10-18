package game

import (
	"owserver/inst/bits"
	"owserver/inst/replication"
	"owserver/inst/world"
)

// View is what one player's client has of the game's entities (docs/InstanceServer.md §12.6–12.7): per server-run
// entity the Statescript state it acknowledged (a remote delta is a diff from it) and one packet in flight at a
// time, and per capture point the mirror values it was sent. The caller (package match) queues the entries Join and
// Update return on the player's replicator.
type View struct {
	ents map[uint32]*viewEnt
}

type viewEnt struct {
	joined   bool                     // create + full update queued
	base     *replication.ScriptState // acknowledged
	baseCF   uint32                   // the acknowledged packet's end CF
	inflight bool
	// capture mirror
	mirSent     bool
	mirInflight bool
	mirProgress float32
	mirEnabled  bool
}

func NewView() *View { return &View{ents: map[uint32]*viewEnt{}} }

// Join returns the creates of the game's entities and their remote full updates, for a player whose map has
// loaded (after the world's placements).
func (g *Game) Join(v *View) []replication.Entry {
	var q, scripts []replication.Entry
	for _, e := range g.ents {
		ve := &viewEnt{joined: true}
		v.ents[e.ID] = ve
		c := replication.Entry{Entity: e.ID, Op: replication.OpCreate}
		switch e.Kind {
		case world.KindBrain:
			c.Create = replication.Create{Def: uint32(e.Def), Flags: 0x08, Rot: [4]float32{0, 0, 0, 1}, Scale: [3]float32{1, 1, 1}}
		default:
			c.Create = replication.Create{FromMap: true, MapIndex: e.Obj.Index}
			if e.Obj.Mover {
				c.Create.Mover = &replication.Mover{Pos: e.Obj.Pos, Yaw: e.Obj.Yaw}
			}
		}
		q = append(q, c)
		scripts = append(scripts, g.fullEntry(e.ID, ve))
	}
	return append(q, scripts...)
}

// fullEntry is an entity's remote full update, encoded when the packet is built; its acknowledgement makes it the
// base of the deltas.
func (g *Game) fullEntry(id uint32, ve *viewEnt) replication.Entry {
	var snap *replication.ScriptState
	var end uint32
	ve.inflight = true
	return replication.Entry{Entity: id, ScriptsAt: func(frame uint32) *bits.Writer {
		s, lay, err := g.Snapshot(id)
		if err != nil {
			g.log.Warn("game: snapshot", "entity", id, "err", err)
			return nil
		}
		w, err := replication.RemoteFull(frame, s, lay)
		if err != nil {
			g.log.Warn("game: remote full update", "entity", id, "err", err)
			return nil
		}
		snap, end = s, frame
		return w
	}, Acked: func() {
		ve.inflight = false
		if snap != nil {
			ve.base, ve.baseCF = snap, end
		}
	}, Lost: func() {
		ve.inflight = false // the next Update sends a full update again (no base yet)
	}}
}

// Update returns what a joined player needs this tick: a remote delta per entity whose Statescript changed since
// its acknowledged base (one in flight per entity; a lost one is covered by the next, from the same base), and the
// capture points' mirror values that changed (one in flight per point).
func (g *Game) Update(v *View, frame uint32) []replication.Entry {
	var q []replication.Entry
	for _, e := range g.ents {
		ve := v.ents[e.ID]
		if ve == nil || !ve.joined {
			continue
		}
		if pt := g.points[e.ID]; pt != nil && !ve.mirInflight && (!ve.mirSent || pt.Progress != ve.mirProgress || pt.Enabled != ve.mirEnabled) {
			q = append(q, g.mirrorEntry(pt, ve))
		}
		if ve.inflight {
			continue
		}
		if ve.base == nil {
			q = append(q, g.fullEntry(e.ID, ve))
			continue
		}
		cur, lay, err := g.Snapshot(e.ID)
		if err != nil {
			continue
		}
		if !changed(ve.base, cur) {
			continue
		}
		base, start := ve.base, ve.baseCF+1
		ve.inflight = true
		var end uint32
		q = append(q, replication.Entry{Entity: e.ID, ScriptsAt: func(fr uint32) *bits.Writer {
			if fr < start {
				fr = start
			}
			w, err := replication.RemoteDelta(start, fr, base, cur, nil, lay)
			if err != nil {
				g.log.Warn("game: remote delta", "entity", e.ID, "err", err)
				return nil
			}
			end = fr
			return w
		}, Acked: func() {
			ve.inflight = false
			if end != 0 {
				ve.base, ve.baseCF = cur, end
			}
		}, Lost: func() { ve.inflight = false }})
	}
	return q
}

func (g *Game) mirrorEntry(pt *Point, ve *viewEnt) replication.Entry {
	p, en := pt.Progress, pt.Enabled
	ve.mirInflight = true
	return replication.Entry{Entity: pt.ID, Op: replication.OpUpdate,
		Components: []replication.ComponentUpdate{replication.Mirror1A(replication.CaptureArea{Progress: &p, Enabled: &en})},
		Acked: func() {
			ve.mirInflight, ve.mirSent, ve.mirProgress, ve.mirEnabled = false, true, p, en
		},
		Lost: func() { ve.mirInflight = false }}
}

// changed: anything a remote delta would carry differs (instances, owner vars, instance vars, active states or
// their change counts / data).
func changed(a, b *replication.ScriptState) bool {
	if len(a.Instances) != len(b.Instances) || len(a.Owner) != len(b.Owner) {
		return true
	}
	for k, v := range b.Owner {
		if o, ok := a.Owner[k]; !ok || o != v {
			return true
		}
	}
	for i := range b.Instances {
		x, y := &a.Instances[i], &b.Instances[i]
		if x.ID != y.ID || len(x.Vars) != len(y.Vars) || len(x.States) != len(y.States) {
			return true
		}
		for k, v := range y.Vars {
			if o, ok := x.Vars[k]; !ok || o != v {
				return true
			}
		}
		for k, d := range y.States {
			if o, ok := x.States[k]; !ok || o.Changes != d.Changes || !o.Equal(d) {
				return true
			}
		}
	}
	return false
}
