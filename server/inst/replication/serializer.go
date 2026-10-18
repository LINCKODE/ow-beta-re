package replication

import (
	"owserver/inst/bits"
	"owserver/internal/heroes"
)

// The serialization boundary (T049, docs/JAM.md "Our implementation"): payloads
// whose layout is the client's memory layout (component mirror data, Statescript
// sync, movement snapshots) are to be produced by the verified C++ libraries behind
// their C APIs (STU reflection in systems/stu, the runtime in systems/statescript,
// a systems/replication later). The server only ever produces them through these
// interfaces; Stopgap implements them with the R&D server's hand-written encoders
// (mirror.go, script.go, entity.go) until then.
//
// Positions: component values and snapshots are written in place into the packet
// (stream 4's byte alignment is relative to the packet), so a C++ implementation
// takes the writer's bit phase (w.Len() % 8) and returns bits the Go side appends
// with w.Append. Statescript packets are position-independent.

// MirrorSerializer encodes mirrored component updates (the changed-field mask and
// the values).
type MirrorSerializer interface {
	FilterBits(pre uint64) ComponentUpdate                   // 0x10 (STU 0xfb184d9c)
	Possession(aim, camera, movement uint32) ComponentUpdate // 0x20
	Health(normal HealthPart) ComponentUpdate                // 0x28
	PlayerInfo(p PlayerInfo) ComponentUpdate                 // 0x3F (STU 0xDB2735A9)
}

// ScriptSerializer encodes Statescript sync packets (stream 1).
type ScriptSerializer interface {
	// HeroStartup is a hero body's start-up as a local-format full update ending at endCF.
	HeroStartup(endCF uint32, st *heroes.Startup) (*bits.Writer, error)
	// RemoteFullUpdate creates graphs on a non-local entity (control API experiments).
	RemoteFullUpdate(endCF uint32, graphs []Graph, active map[int][]State) *bits.Writer
}

// SnapshotSerializer writes a movement snapshot into a create at w's position.
type SnapshotSerializer interface {
	WriteSnapshot(w *bits.Writer, m Mover, frame uint32)
}

// Serializers is the set the server uses.
type Serializers struct {
	Mirror   MirrorSerializer
	Script   ScriptSerializer
	Snapshot SnapshotSerializer
}

// Stopgap implements every serializer with the moved R&D encoders.
type Stopgap struct{}

// Default is Stopgap everywhere.
var Default = Serializers{Mirror: Stopgap{}, Script: Stopgap{}, Snapshot: Stopgap{}}

func (Stopgap) FilterBits(pre uint64) ComponentUpdate { return Mirror10(pre) }
func (Stopgap) Possession(aim, camera, movement uint32) ComponentUpdate {
	return Mirror20(aim, camera, movement)
}
func (Stopgap) Health(normal HealthPart) ComponentUpdate { return Mirror28(normal) }
func (Stopgap) PlayerInfo(p PlayerInfo) ComponentUpdate  { return Mirror3F(p) }
func (Stopgap) HeroStartup(endCF uint32, st *heroes.Startup) (*bits.Writer, error) {
	return HeroStartup(endCF, st)
}
func (Stopgap) RemoteFullUpdate(endCF uint32, graphs []Graph, active map[int][]State) *bits.Writer {
	return RemoteFullUpdate(endCF, graphs, active)
}
func (Stopgap) WriteSnapshot(w *bits.Writer, m Mover, frame uint32) { m.writeSnapshot(w, frame) }
