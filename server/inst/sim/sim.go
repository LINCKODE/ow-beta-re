// Package sim is the match's simulation host: where the server runs Statescript
// (hero and brain graphs, the server-only nodes) over the world.
//
// Two hosts: Static hands out each hero's content-generated start-up
// (server/internal/heroes, T040/T044) and simulates nothing; Runtime (runtime.go,
// T051) runs the hero's instances in systems/statescript in lockstep with the
// client's prediction: seeded from the same start-up the client gets, stepped at
// the client's input command frames with its buttons, and encoded as local deltas
// that confirm (or correct) what the client predicted.
package sim

import (
	"owserver/inst/bits"
	"owserver/inst/wire"
	"owserver/internal/heroes"
)

// Host is one match's simulation. The game loop calls it; it is not called
// concurrently.
type Host interface {
	// SpawnHero starts the Statescript of hero body (definition index def of the
	// hero's gameplay entity). It returns the start-up the lock-in full update
	// carries and the command frame that update must end at (0: the frame of the
	// packet that carries it).
	SpawnHero(body, def uint32, cf uint32) (st *heroes.Startup, endCF uint32, ok bool)
	// RemoveHero ends a body's Statescript.
	RemoveHero(body uint32)
	// Input hands over the client's input history for body (the entries of one
	// client packet, oldest first; already seen frames are ignored).
	Input(body uint32, in []wire.InputEntry)
	// Tick runs command frame frame (the server's frame P).
	Tick(frame uint32)
	// Pending reports whether body has simulated frames no delta covers yet.
	Pending(body uint32) bool
	// Delta encodes a local-format delta covering what body simulated since the last
	// acknowledged one (a union: replication.LocalGhost), and its end CF; nil when there
	// is nothing to send. The caller reports its fate with Acked / Lost.
	Delta(body uint32) (*bits.Writer, uint32)
	// Acked: the packet carrying body's delta ending at end arrived (transport ack).
	Acked(body, end uint32)
	// Lost: a packet carrying one of body's deltas was lost.
	Lost(body uint32)
	// Stats describes the simulation (control API).
	Stats() any
	// Close ends the match's simulation.
	Close()
}

// Tracer is a Host that can trace a body's Statescript (Runtime).
type Tracer interface {
	Trace(body uint32, on bool) error
	DrainTrace(body uint32) (string, error)
	RecentDeltas(body uint32) ([]DeltaRecord, error)
}

// Static is the placeholder Host: the start-up only.
type Static struct{}

func (Static) SpawnHero(_, def, _ uint32) (*heroes.Startup, uint32, bool) {
	st, ok := heroes.Start(def)
	return st, 0, ok
}
func (Static) RemoveHero(uint32)                   {}
func (Static) Input(uint32, []wire.InputEntry)     {}
func (Static) Tick(uint32)                         {}
func (Static) Pending(uint32) bool                 { return false }
func (Static) Delta(uint32) (*bits.Writer, uint32) { return nil, 0 }
func (Static) Acked(uint32, uint32)                {}
func (Static) Lost(uint32)                         {}
func (Static) Stats() any                          { return nil }
func (Static) Close()                              {}
