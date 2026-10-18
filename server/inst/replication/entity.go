package replication

import (
	"fmt"
	"math"

	"owserver/inst/bits"
	"owserver/inst/wire"
)

// Op is a stream 4 entity operation (FUN_7ff650bfff40: u2 op, u14 bit length, data).
type Op uint8

const (
	OpCreate  Op = 1 // FUN_7ff650c00d20
	OpUpdate  Op = 2 // mirrored component updates only
	OpDestroy Op = 3 // FUN_7ff650c00c80: no length, no data
)

// Create is how an entity comes into being on the client: from the map's placed
// entity MapIndex (create blob bit 1, u32 index into the placed-entity list,
// without component data; system 8 holds the local player back until the map's
// networked ones exist), or from an entity definition (0x0400000000000000 | Def).
type Create struct {
	FromMap  bool
	MapIndex uint32
	Def      uint32
	// Flags is the definition create's u7 (FUN_7ff650c018f0): 0x08 position +
	// rotation (+ scale), 0x04 the scale only, 0x02 a movement snapshot, 0x01 an
	// extra resource (0x40: of another type), 0x10 the player loader (adds 0x2F/0x33
	// on the local entity), 0x20 also 0x3F PlayerInfo. 0 means 0x08.
	Flags uint8
	Pos   [3]float32
	Rot   [4]float32 // quaternion x, y, z, w
	Scale [3]float32
	// Mover sends a movement snapshot with the create. Entities with a movement state
	// (0x12: doors, hero bodies) need one, or MovementStateSystem puts them at the origin.
	Mover *Mover
}

// Mover is a movement snapshot (FUN_7ff65100aba0) holding only a command frame, a
// position and a yaw; every other field stays at the reader's baseline
// (DAT_7ff651890390). One history entry holds the entity there.
type Mover struct {
	Frame uint32     `json:"frame"` // history frame; 0 = the packet's frame
	Pos   [3]float32 `json:"pos"`
	Yaw   float32    `json:"yaw"` // radians about +Y; 0 faces +Z
}

// writeSnapshot writes m as a delta against the create baseline. The create passes
// the reader a frame of -1, so the frame is sent explicitly (-1 - d).
func (m Mover) writeSnapshot(w *bits.Writer, frame uint32) {
	if m.Frame != 0 {
		frame = m.Frame
	}
	w.Bits(0, 1)                           // +0x198 unchanged (FUN_7ff651008130)
	w.Bits(0, 1)                           // +0x19c = 0: positions in 1/1024 units
	w.Bits(0, 1)                           // +0x1a0 explicit: -1 - d
	wire.Varint(w, ^frame, [2]uint{4, 10}) // d (FUN_7ff65100dff0, table 0x7ff651716cd8)
	w.Bits(0, 1)                           // +0x1a4 unchanged (FUN_7ff651007f00)
	w.Bits(1, 1)                           // +0x240 unchanged (FUN_7ff65100eff0)
	w.Bits(1, 1)                           // +0x1a8 = baseline
	w.Bits(1, 1)                           // +0x1ac unchanged (FUN_7ff65100ef60)
	w.Bits(1, 1)                           // +0x1b4 = +0x1a0
	w.Bits(0, 2)                           // +0x1b0, +0x1b1 unchanged (FUN_7ff65100c8a0)
	// Rotation (FUN_7ff65100ce60): yaw, pitch, roll; 1 = unchanged, else q/4096 rad.
	if q := int32(math.Round(float64(m.Yaw) * 4096)); q == 0 {
		w.Bits(1, 1)
	} else {
		w.Bits(0, 1)
		prefixSigned(w, q, [3]uint{8, 12, 16}, 16) // table 0x7ff65171e808
	}
	w.Bits(1, 1)              // pitch unchanged
	w.Bits(1, 1)              // roll unchanged
	for _, p := range m.Pos { // +0x220 (FUN_7ff651008200 -> FUN_7ff65100ef60)
		w.Bits(0, 1)
		prefixSigned(w, int32(math.Round(float64(p)*1024)), [3]uint{8, 10, 24}, 32) // table 0x7ff65171e7f8
	}
	w.Bits(1, 1) // +0x230 = baseline
	w.Bits(0, 3) // FUN_7ff65100d0c0 / d160 / d270: absent
	w.Bits(0, 4) // optional blocks +0x70, +0xd0, +0x90, +0xd8: absent
	w.Bits(0, 1) // +0x244/+0x248 (moving platform) = baseline
	w.Bits(1, 1) // +0x1b8 unchanged
	w.Bits(1, 1) // +0x1bc unchanged
	w.Bits(0, 1) // +0x120 mirrored anim data: none (FUN_7ff6510092f0)
	w.Bits(0, 1) // +0x250 = baseline (FUN_7ff651007f80)
}

// prefixSigned writes the movement snapshot's signed numbers (FUN_7ff65100d360): i
// 0 bits and a 1, then widths[i] bits, for the first width that fits; otherwise
// three 0 bits and raw bits.
func prefixSigned(w *bits.Writer, q int32, widths [3]uint, raw uint) {
	for i, n := range widths {
		if q >= -(1<<(n-1)) && q < 1<<(n-1) {
			w.Bits(1<<i, uint(i)+1)
			w.Bits(uint32(q)&(1<<n-1), n)
			return
		}
	}
	w.Bits(0, 3)
	w.Bits(uint32(q)&uint32(uint64(1)<<raw-1), raw)
}

func (c Create) validate() error {
	if c.FromMap {
		return nil
	}
	flags := c.Flags
	if c.Mover != nil {
		flags |= 0x02
	}
	if flags&^0x3e != 0 {
		return fmt.Errorf("replication: create flags %#x: 0x01 and 0x40 need data we don't write yet", flags)
	}
	if flags&0x02 != 0 && c.Mover == nil {
		return fmt.Errorf("replication: create flag 0x02 needs a Mover")
	}
	return nil
}

func (c Create) write(w *bits.Writer, frame uint32, snap SnapshotSerializer) {
	if c.FromMap {
		w.Bits(1, 1)
		w.Bits(c.MapIndex, 32)
		if c.Mover != nil {
			w.Bits(1, 1)
			snap.WriteSnapshot(w, *c.Mover, frame)
		} else {
			w.Bits(0, 1)
		}
		return
	}
	w.Bits(0, 1)
	wire.Varint(w, c.Def, wire.EntDefWidths)
	flags := uint32(c.Flags)
	if flags == 0 {
		flags = 0x08
	}
	if c.Mover != nil {
		flags |= 0x02
	}
	w.Bits(flags, 7)
	if flags&0x08 != 0 {
		w.F32(c.Pos[:]...)
		w.F32(c.Rot[:]...)
	}
	if flags&0x0c != 0 {
		w.F32(c.Scale[:]...)
	}
	if flags&0x02 != 0 {
		snap.WriteSnapshot(w, *c.Mover, frame)
	}
}

// Entry is one entity's part of a packet: a stream 4 operation (create with its
// initial component updates, update, destroy) and/or Statescript sync data
// (stream 1).
type Entry struct {
	Entity     uint32 // 0x40000000 | id
	Op         Op     // 0 = none (Statescript only)
	Create     Create
	Components []ComponentUpdate
	Scripts    *bits.Writer // a Statescript packet (script.go)
	// ScriptsAt builds the Statescript packet for the frame of the packet that carries
	// it (a full update's end CF), when Scripts is nil.
	ScriptsAt func(frame uint32) *bits.Writer
	// Acked / Lost report the fate of the packet that carried the entry (transport acks). A lost entry with
	// Lost set is not sent again: its owner sends something newer instead (a Statescript union, LocalGhost).
	Acked, Lost func()
}

// ID is the entry's entity number (without the class bit).
func (e Entry) ID() uint32 { return e.Entity &^ 0x40000000 }

// wire turns the entry into the payload's entity section for frame.
func (e Entry) wire(frame uint32, snap SnapshotSerializer) wire.Entity {
	we := wire.Entity{ID: e.ID(), Scripts: e.Scripts}
	if we.Scripts == nil && e.ScriptsAt != nil {
		we.Scripts = e.ScriptsAt(frame)
	}
	if e.Op == 0 {
		return we
	}
	we.State = func(w *bits.Writer) {
		w.Bits(uint32(e.Op), 2)
		if e.Op == OpDestroy {
			return
		}
		lenPos := w.Len()
		w.Bits(0, 14)
		start := w.Len()
		if e.Op == OpCreate {
			e.Create.write(w, frame, snap)
		}
		writeComponentUpdates(w, e.Components)
		w.SetBits(lenPos, uint32(w.Len()-start), 14)
	}
	return we
}

// EntityID is the client's ID of networked entity n.
func EntityID(n uint32) uint32 { return 0x40000000 | n }

// YawQuat is the quaternion (x, y, z, w) of a rotation by yaw radians about +Y.
func YawQuat(yaw float32) [4]float32 {
	s, c := math.Sincos(float64(yaw) / 2)
	return [4]float32{0, float32(s), 0, float32(c)}
}
