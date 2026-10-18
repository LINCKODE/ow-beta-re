// Package replication turns the server's entities into what a client receives:
// stream 4 entity operations (creates with their mirrored component data, updates,
// destroys) and stream 1 Statescript sync, per connection. A Replicator holds one
// connection's state: which entities the client has, what is queued for it and
// which entries are in flight, so that entries in lost packets are sent again.
//
// The encoders (mirror.go, entity.go, script.go) are the R&D server's
// (server/internal/instance), moved behind these types. The ones producing the
// client's memory layouts (component mirrors, Statescript payloads, movement
// snapshots) are stopgaps to be replaced by the C++ libraries' C APIs (T049).
package replication

import (
	"errors"
	"fmt"

	"owserver/inst/wire"
)

// ErrNotResident is Queue's error for a create the client cannot build on its map: a definition it has not
// loaded, or a placed index past the map's list (docs/InstanceServer.md §12.14, §12.15). Such a create is a
// hard client crash (an assert in the create handler FUN_7ff650c00d20), so it is never sent.
var ErrNotResident = errors.New("not loaded on the client's map")

// Residency is what the client has loaded on the connection's map (maps.Map.Resident, maps.Map.Placed).
type Residency struct {
	Resident func(def uint64) bool // entity definition 0x040... (a definition create's 0x0400000000000000 | Def)
	Placed   uint32                // size of the placed-entity list: a from-map create's index must be below it
}

// check reports why the client could not build c, or nil.
func (res *Residency) check(c Create) error {
	switch {
	case res == nil:
		return nil
	case c.FromMap && c.MapIndex >= res.Placed:
		return fmt.Errorf("%w: placed index %#x (the map places %d)", ErrNotResident, c.MapIndex, res.Placed)
	case !c.FromMap && res.Resident != nil && !res.Resident(0x0400000000000000|uint64(c.Def)):
		return fmt.Errorf("%w: definition %#016x", ErrNotResident, 0x0400000000000000|uint64(c.Def))
	}
	return nil
}

// Replicator is one connection's replication state. It is not safe for concurrent
// use: the game loop owns it.
type Replicator struct {
	// OnePerPacket sends one entry per packet. The client goes on reading past a
	// create's blob (harmless zeros at the end of a packet, the next entity's bits in
	// a batch: "Failed to deserialize component update ... 0 of 16" assert in
	// FUN_7ff650c00560 with 8 map entities per packet, docs/MatchLogic.md §15), so
	// with it off a create still ends its packet, and only updates, destroys and
	// Statescript entries share one.
	OnePerPacket bool
	// Snapshots writes the creates' movement snapshots (default: Stopgap).
	Snapshots SnapshotSerializer
	// Residency guards the creates (nil: no guard). A refused create is not sent, and neither is any later
	// entry for its entity (updates, Statescript, destroy) until a create of it passes.
	Residency *Residency

	known    map[uint32]bool    // entities the client has (created, not destroyed)
	queue    []Entry            // to send, in order
	inFlight map[uint32][]Entry // packet seq -> its entries
	refused  map[uint32]bool    // entities whose create the residency guard refused

	// Stats (control API). Refused counts the entries the residency guard dropped.
	Creates, Updates, Destroys, Scripts, Resent, Refused int
}

func NewReplicator() *Replicator {
	return &Replicator{OnePerPacket: true, Snapshots: Stopgap{}, known: map[uint32]bool{}, inFlight: map[uint32][]Entry{},
		refused: map[uint32]bool{}}
}

// Reset forgets everything (a new connection: the client starts empty).
func (r *Replicator) Reset() {
	r.known, r.queue, r.inFlight, r.refused = map[uint32]bool{}, nil, map[uint32][]Entry{}, map[uint32]bool{}
}

// Has reports whether the client has (or is being sent) entity.
func (r *Replicator) Has(entity uint32) bool { return r.known[entity] }

// Queue adds entries to send after the ones already queued. Creates the residency guard refuses, and the
// entries for their entities, are left out (ErrNotResident, one error per refused create); the others are queued.
func (r *Replicator) Queue(es ...Entry) error {
	var refused []error
	for _, e := range es {
		if e.Op == OpCreate {
			if err := e.Create.validate(); err != nil {
				return errors.Join(append(refused, fmt.Errorf("entity %#x: %w", e.Entity, err))...)
			}
			if err := r.Residency.check(e.Create); err != nil {
				r.refused[e.Entity] = true
				r.Refused++
				refused = append(refused, fmt.Errorf("entity %#x: %w", e.Entity, err))
				continue
			}
			delete(r.refused, e.Entity)
		} else if r.refused[e.Entity] {
			r.Refused++
			continue
		}
		switch e.Op {
		case OpCreate:
			r.known[e.Entity] = true
		case OpDestroy:
			delete(r.known, e.Entity)
		}
		r.queue = append(r.queue, e)
	}
	return errors.Join(refused...)
}

// Pending is the number of entries waiting.
func (r *Replicator) Pending() int { return len(r.queue) }

// Take removes the entries for the next packet at frame and returns them with
// their wire form.
func (r *Replicator) Take(frame uint32) ([]Entry, []wire.Entity) {
	var es []Entry
	for len(r.queue) > 0 && len(es) < wire.MaxEntities {
		e := r.queue[0]
		if len(es) > 0 && r.OnePerPacket {
			break
		}
		r.queue = r.queue[1:]
		es = append(es, e)
		if e.Op == OpCreate {
			break // a create ends the packet
		}
	}
	out := make([]wire.Entity, len(es))
	for i, e := range es {
		out[i] = e.wire(frame, r.Snapshots)
		switch {
		case e.Op == OpCreate:
			r.Creates++
		case e.Op == OpUpdate:
			r.Updates++
		case e.Op == OpDestroy:
			r.Destroys++
		}
		if out[i].Scripts != nil {
			r.Scripts++
		}
	}
	return es, out
}

// Sent records the entries that went out in packet seq; Unsent puts back the
// entries of a packet that could not be sent.
func (r *Replicator) Sent(seq uint32, es []Entry) {
	if len(es) > 0 {
		r.inFlight[seq] = es
	}
}

func (r *Replicator) Unsent(es []Entry) { r.queue = append(append([]Entry{}, es...), r.queue...) }

// Acked and Lost take the link's packet reports. Entries in lost packets go to the
// front of the queue, in their order.
//
// [?] Resending an update after a newer one for the same component would roll the
// client back; the skeleton accepts that (loss is rare on the links we test).
func (r *Replicator) Acked(seqs []uint32) {
	for _, s := range seqs {
		for _, e := range r.inFlight[s] {
			if e.Acked != nil {
				e.Acked()
			}
		}
		delete(r.inFlight, s)
	}
}

func (r *Replicator) Lost(seqs []uint32) {
	var again []Entry
	for _, s := range seqs {
		if es, ok := r.inFlight[s]; ok {
			for _, e := range es {
				if e.Lost != nil {
					e.Lost() // superseded, not resent
					continue
				}
				again = append(again, e)
			}
			delete(r.inFlight, s)
		}
	}
	if len(again) > 0 {
		r.Resent += len(again)
		r.queue = append(again, r.queue...)
	}
}
