package replication

import (
	"owserver/inst/bits"
)

// ComponentUpdate is one mirrored component's data in a stream 4 create or update.
// FUN_7ff650c00560 reads u8 N, then N x (u8 component ID, component data); the
// component data (update visitor FUN_7ff650c00310 + FUN_7ff6508147d0) is, from the
// next byte, u8 L and L bytes holding one changed bit per field of the component's
// STU mirror type (own fields in order, then the parent type's), followed in the
// stream by the changed fields' values (each field type's reader, vfunc +0xb0,
// reads from the stream, not from the L bytes).
//
// Stopgap (T049 boundary rule): these hand-written mirrors are to be produced by
// the C++ STU reflection behind a C API later.
type ComponentUpdate struct {
	ID     byte
	Mask   []byte       // changed bits, one per field
	Values *bits.Writer // the changed fields' values
}

// writeComponentUpdates writes the update list that ends every create and is all
// of an update. The client keeps reading it after a create, so it must be there
// (N = 0) even without updates.
func writeComponentUpdates(w *bits.Writer, us []ComponentUpdate) {
	w.Bits(uint32(len(us)), 8)
	for _, u := range us {
		w.Bits(uint32(u.ID), 8)
		w.Bytes(append([]byte{byte(len(u.Mask))}, u.Mask...)) // from the next byte
		w.Append(u.Values)
	}
}

// Mirror20 is component 0x20's update: its mirror has 3 entity ID fields (mirror
// +0x78/+0x7c/+0x80 = component +0x90 aim, +0x94 camera, +0x98 movement attach);
// an entity ID value is 32 raw bits (FUN_7ff650591a50).
func Mirror20(aim, camera, movement uint32) ComponentUpdate {
	v := &bits.Writer{}
	for _, id := range []uint32{aim, camera, movement} {
		v.Bits(id, 32)
	}
	return ComponentUpdate{ID: 0x20, Mask: []byte{0b111}, Values: v}
}

// Mirror10 is component 0x10's (FilterBits) update: its mirror (STU 0xfb184d9c) has
// one u64 field, pre_filter_bits (component +0x90), compressed (compressedU64).
// FUN_7ff650c9f660 copies it (bit 0x10 recomputed) to the final bits +0x98 that
// team checks use. Team t is bit 0x40000 << t (TeamBit).
func Mirror10(preFilterBits uint64) ComponentUpdate {
	return ComponentUpdate{ID: 0x10, Mask: []byte{0b1}, Values: compressedU64(preFilterBits)}
}

// TeamBit is the filter bit of team t (0-3).
func TeamBit(t int) uint64 { return 0x40000 << uint(t) }

// PlayerInfo is component 0x3F's mirror (STU 0xDB2735A9; docs/MatchLogic.md §15.4):
// u64 +0x78 the confirmed hero, +0x80 / +0x88 unknown, +0x90 the pending hero
// (hero-select preview), i32 +0x98 / +0x9c unknown, u8 +0xa0 slot, u8 +0xa1..+0xa3
// hero-select flags [?]. Only the fields set are sent.
type PlayerInfo struct {
	Hero, Pending                            *uint64
	Slot, HeroSelected, WasSelected, Enabled *uint8
}

// Mirror3F is component 0x3F's (PlayerInfo) update: u64 fields compressed, u8
// fields 8 raw bits (FUN_7ff65066b660).
func Mirror3F(p PlayerInfo) ComponentUpdate {
	v := &bits.Writer{}
	mask := uint32(0)
	u64 := func(bit uint, x *uint64) {
		if x != nil {
			mask |= 1 << bit
			v.Append(compressedU64(*x))
		}
	}
	u8 := func(bit uint, x *uint8) {
		if x != nil {
			mask |= 1 << bit
			v.Bits(uint32(*x), 8)
		}
	}
	u64(0, p.Hero)
	u64(3, p.Pending)
	u8(6, p.Slot)
	u8(7, p.HeroSelected)
	u8(8, p.WasSelected)
	u8(9, p.Enabled)
	return ComponentUpdate{ID: 0x3f, Mask: []byte{byte(mask), byte(mask >> 8)}, Values: v}
}

// compressedU64 writes a u64 the way FUN_7ff650e29d40 reads mirror u64s: a byte
// whose bit i says byte i is non-zero, then those bytes, low first.
func compressedU64(x uint64) *bits.Writer {
	v := &bits.Writer{}
	var mask uint32
	for i := range 8 {
		if byte(x>>(8*i)) != 0 {
			mask |= 1 << i
		}
	}
	v.Bits(mask, 8)
	for i := range 8 {
		if b := byte(x >> (8 * i)); b != 0 {
			v.Bits(uint32(b), 8)
		}
	}
	return v
}

// HealthPart is one entry of component 0x28's health arrays (an STU struct of 7
// fields written without changed bits, FUN_7ff650593430).
type HealthPart struct {
	Enabled      uint32
	Max, Current float32
	Unknown      uint32 // +0xc
	Flag10       uint8  // Prometheus's player spawner sets 1
	Flag11       uint8  // "some_other_flag", 1 in Prometheus's spawner
	Flag12       uint8
}

func (h HealthPart) write(w *bits.Writer) {
	w.Bits(h.Enabled, 32)
	w.F32(h.Max, h.Current)
	w.Bits(h.Unknown, 32)
	w.Bits(uint32(h.Flag10), 8)
	w.Bits(uint32(h.Flag11), 8)
	w.Bits(uint32(h.Flag12), 8)
}

// Mirror28 is component 0x28's (health) update. Its mirror (type 0x7ff65174d470)
// has 52 fields: 16 normal health parts (component +0x98), 16 armour (+0x1d8), 16
// shield (+0x318), two u32 and two bytes. This sets normal health part 0 only.
func Mirror28(normal HealthPart) ComponentUpdate {
	v := &bits.Writer{}
	normal.write(v)
	mask := make([]byte, 7) // 52 fields
	mask[0] = 0x01
	return ComponentUpdate{ID: 0x28, Mask: mask, Values: v}
}

// PlayerHealth is what Prometheus's player spawner gives a hero (a full pool).
func PlayerHealth(max float32) ComponentUpdate {
	return Mirror28(HealthPart{Enabled: 1, Max: max, Current: max, Flag10: 1, Flag11: 1})
}

// CaptureArea is component 0x1a's mirror (STU 0x47c72f78, 12 fields, no parent; docs/InstanceServer.md §12.7):
// field 6 `794d7691` f32 is the capture progress (component +0xb4, what CapturePointEvents makes $009a read on the
// client [G]), field 10 `0ab6f7cd` bool the enabled flag. Only the fields set are sent.
type CaptureArea struct {
	Progress *float32
	Enabled  *bool
}

// Mirror1A is component 0x1a's update. f32 = 32 raw bits and bool = 8 raw bits (as u8, FUN_7ff65066b660) [?: the
// f32 / bool field readers were not identified in Ghidra; checked live against component +0xb4].
func Mirror1A(c CaptureArea) ComponentUpdate {
	v := &bits.Writer{}
	mask := uint32(0)
	if c.Progress != nil {
		mask |= 1 << 6
		v.F32(*c.Progress)
	}
	if c.Enabled != nil {
		mask |= 1 << 10
		b := uint32(0)
		if *c.Enabled {
			b = 1
		}
		v.Bits(b, 8)
	}
	return ComponentUpdate{ID: 0x1a, Mask: []byte{byte(mask), byte(mask >> 8)}, Values: v}
}
