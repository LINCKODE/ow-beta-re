package instance

import (
	"encoding/binary"
	"fmt"
	"math"
)

// A data packet's payload is a bitstream written by the link's streams in order
// (client: game system 45, FUN_7ff650c52bc0, one packet per 16 ms command frame).
// Bits are packed LSB first (FUN_7ff650e292e0 / FUN_7ff650e29350).
//
// Server -> client payloads (reader FUN_7ff650c500a0, header flag 1) start with a
// variable-length number (FUN_7ff65100dff0, widths at 0x7ff651716c58): 1 + 18
// bits, 01 + 24 bits or 00 + 32 bits, probably the server's command frame. Then
// the streams read in order (+0x30), then u7 entity count and per entity a flag
// bit, the entity ID and per stream a present bit + entity data (+0x38). The
// client's own payloads have no leading number. Leaving it out made the client
// read our stream 0 two bytes late and crash on a bogus channel.
//
// Stream 0 is the JAM message stream (writer FUN_7ff651030df0, reader
// FUN_7ff6510304c0), the same in both directions:
//
//	bit present | u4 reliable count | u4 unreliable count | pad to byte
//	reliable:   u32 seq | u8 channel | u8 index | JAM payload   (seq 0, 1, 2... per direction)
//	unreliable: u8 channel | u8 index | JAM payload
//
// A channel is an index into the stream's protocol list (see Channels) and the
// message ID is the protocol's first ID + index. Payloads use the normal JAM
// binary encoding, as in the lobby. The client delivers reliable messages in seq
// order and holds back anything after a gap.

// Channels is the JAM stream's protocol list, read from the client (stream +0x38).
var Channels = []struct {
	Name        string
	First, Last uint16 // Last is inclusive
}{
	{"ClientInInstance", 20300, 20307},
	{"ClientOutInstance", 21600, 21615},
	{"ClientInCombat", 25000, 25010},
	{"ClientInVoice", 27500, 27501},
	{"ClientInStats", 25800, 25808},
	{"ClientOutCheats", 30700, 30739},
	{"ClientOutPvP", 22300, 22308},
	{"ClientOutStats", 25700, 25705},
}

// ChannelOf returns the channel and index of a message ID.
func ChannelOf(msg uint16) (channel, index byte, ok bool) {
	for i, c := range Channels {
		if msg >= c.First && msg <= c.Last {
			return byte(i), byte(msg - c.First), true
		}
	}
	return 0, 0, false
}

// MessageID returns the message ID of a channel and index.
func MessageID(channel, index byte) (uint16, string) {
	if int(channel) >= len(Channels) {
		return 0, fmt.Sprintf("channel %d", channel)
	}
	c := Channels[channel]
	return c.First + uint16(index), c.Name
}

// JamMsg is one message of the JAM stream.
type JamMsg struct {
	Reliable bool
	Seq      uint32 // reliable only
	Channel  byte
	Index    byte
	Payload  []byte // the JAM payload's byte fields (up to the next message is unknown: see ParseJamStream)
	// Bools are the payload's trailing bool fields. The stream's JAM decoder reads
	// byte fields from the next whole byte but bools as single bits of the bit
	// reader (consecutive bools share a byte), and after the last message the
	// packet continues from that exact bit. Writing a bool as a byte misaligns
	// everything after it.
	Bools []bool
}

func (m JamMsg) ID() (uint16, string) { return MessageID(m.Channel, m.Index) }

type bitWriter struct {
	buf  []byte
	nbit uint
}

func (w *bitWriter) bits(v uint32, n uint) {
	for i := uint(0); i < n; i++ {
		if w.nbit%8 == 0 {
			w.buf = append(w.buf, 0)
		}
		if v>>i&1 != 0 {
			w.buf[len(w.buf)-1] |= 1 << (w.nbit % 8)
		}
		w.nbit++
	}
}

func (w *bitWriter) bools(bs []bool) {
	for _, b := range bs {
		if b {
			w.bits(1, 1)
		} else {
			w.bits(0, 1)
		}
	}
}

// bytes pads to a byte boundary and appends b.
func (w *bitWriter) bytes(b []byte) {
	w.nbit = uint(len(w.buf)) * 8
	w.buf = append(w.buf, b...)
	w.nbit += uint(len(b)) * 8
}

// varint writes the variable-length number read by FUN_7ff65100dff0: a 1 bit and
// widths[0] bits, or 0 1 and widths[1] bits, or 0 0 and 32 bits.
func (w *bitWriter) varint(v uint32, widths [2]uint) {
	switch {
	case v < 1<<widths[0]:
		w.bits(1, 1)
		w.bits(v, widths[0])
	case v < 1<<widths[1]:
		w.bits(0b10, 2) // bits 0 then 1 (LSB first)
		w.bits(v, widths[1])
	default:
		w.bits(0, 2)
		w.bits(v, 32)
	}
}

var (
	frameWidths  = [2]uint{18, 24} // 0x7ff651716c58
	entDefWidths = [2]uint{12, 20} // 0x7ff651716838
)

func (w *bitWriter) f32s(fs ...float32) {
	for _, f := range fs {
		w.bits(math.Float32bits(f), 32)
	}
}

// Spawn creates a networked entity from an entity definition (STUEntityDefinition
// 0x0400000000000000 | Def). The entity's ID is 0x40000000 | ID (class bit 0).
type Spawn struct {
	ID  uint8 // 8-bit form of the entity ID (FUN_7ff650ee4ff0)
	Def uint32
	// Flags is the create blob's u7 (FUN_7ff650c018f0, kept in the create function's
	// local_3398): 0x08 position + rotation (+ vec3), 0x04 the vec3 only, 0x02
	// snapshot data, 0x01 an extra resource (0x40: of another type), 0x10 the
	// player loader branch (loader vtable 0x7ff6515a2978; adds components
	// 0x2F/0x33 when the entity is the local one), 0x20 also 0x3F PlayerInfo.
	// 0 means 0x08.
	Flags uint8
	// FromMap spawns the map's placed entity MapIndex instead of a definition (create
	// blob bit 1, u32 index into the map's placed-entity list, entries of 0x38 bytes
	// with their transform), without component data. System 8 keeps the indexes of
	// the map's networked entities it expects (+0xb0, count +0xb8: 22 on Anubis) and
	// holds back the local player until they exist.
	FromMap  bool
	MapIndex uint32
	// Components are mirrored component updates applied with the create (or, with
	// Update, to an existing entity: stream 4 op 2).
	Components []ComponentUpdate
	Update     bool
	// Destroy removes the entity (stream 4 op 3, FUN_7ff650c00c80: no data).
	Destroy bool
	// Scripts, when set, makes this entry a Statescript sync entry instead: stream 1
	// data (see ScriptFullUpdate), and the entity's streams end after it.
	Scripts *bitWriter
	Pos     [3]float32
	Rot     [4]float32 // quaternion x, y, z, w
	Scale   [3]float32
	// Mover, when set, sends a movement snapshot with the create (from map: the bit
	// after the index; from a definition: flag 0x02). Entities with a movement state
	// (component 0x12, e.g. doors) need one: MovementStateSystem writes the scene
	// transform from the movement history every frame, and without an entry that is
	// the origin, whatever the placement or create transform says.
	Mover *Mover
}

// Mover is a movement snapshot (FUN_7ff65100aba0) holding only a command frame, a
// position and a yaw; every other field is left at the reader's baseline
// (DAT_7ff651890390). One history entry holds the entity there: the interpolation
// (FUN_7ff65100dbb0) copies the only entry, without extrapolating.
type Mover struct {
	Frame uint32     `json:"frame"` // history frame; 0 = the packet's frame. Later snapshots need larger frames.
	Pos   [3]float32 `json:"pos"`
	Yaw   float32    `json:"yaw"` // radians about +Y; 0 faces +Z
	Pitch float32    `json:"pitch"`
	Roll  float32    `json:"roll"`
}

// writeSnapshot writes m as a delta against the create baseline. The create passes
// the reader a frame of -1, so the frame is sent explicitly (-1 - d): a frame of -1
// would make the client drop the snapshot (no history push).
func (m Mover) writeSnapshot(w *bitWriter, frame uint32) {
	if m.Frame != 0 {
		frame = m.Frame
	}
	w.bits(0, 1)                     // +0x198 unchanged (FUN_7ff651008130)
	w.bits(0, 1)                     // +0x19c = 0: positions in 1/1024 units
	w.bits(0, 1)                     // +0x1a0 explicit: -1 - d
	w.varint(^frame, [2]uint{4, 10}) // d (FUN_7ff65100dff0, table 0x7ff651716cd8)
	w.bits(0, 1)                     // +0x1a4 unchanged (FUN_7ff651007f00)
	w.bits(1, 1)                     // +0x240 unchanged (FUN_7ff65100eff0)
	w.bits(1, 1)                     // +0x1a8 = baseline
	w.bits(1, 1)                     // +0x1ac unchanged (FUN_7ff65100ef60)
	w.bits(1, 1)                     // +0x1b4 = +0x1a0
	w.bits(0, 2)                     // +0x1b0, +0x1b1 unchanged (FUN_7ff65100c8a0)
	// Rotation (FUN_7ff65100ce60): yaw, pitch, roll; 1 = unchanged, else q/4096 rad.
	if q := int32(math.Round(float64(m.Yaw) * 4096)); q == 0 {
		w.bits(1, 1)
	} else {
		w.bits(0, 1)
		w.prefixSigned(q, [3]uint{8, 12, 16}, 16) // table 0x7ff65171e808
	}
	w.bits(1, 1)              // pitch unchanged
	w.bits(1, 1)              // roll unchanged
	for _, p := range m.Pos { // +0x220 (FUN_7ff651008200 -> FUN_7ff65100ef60)
		w.bits(0, 1)
		w.prefixSigned(int32(math.Round(float64(p)*1024)), [3]uint{8, 10, 24}, 32) // table 0x7ff65171e7f8
	}
	w.bits(1, 1) // +0x230 = baseline
	w.bits(0, 3) // FUN_7ff65100d0c0 / d160 / d270: absent
	w.bits(0, 4) // optional blocks +0x70, +0xd0, +0x90, +0xd8: absent
	w.bits(0, 1) // +0x244/+0x248 (moving platform) = baseline
	w.bits(1, 1) // +0x1b8 unchanged
	w.bits(1, 1) // +0x1bc unchanged
	w.bits(0, 1) // +0x120 mirrored anim data: none (FUN_7ff6510092f0)
	w.bits(0, 1) // +0x250 = baseline (FUN_7ff651007f80)
}

// prefixSigned writes the movement snapshot's signed numbers (FUN_7ff65100d360):
// i 0 bits and a 1, then widths[i] bits (sign-extended by the reader), for the
// first width that fits; otherwise three 0 bits and raw bits.
func (w *bitWriter) prefixSigned(q int32, widths [3]uint, raw uint) {
	for i, n := range widths {
		if q >= -(1<<(n-1)) && q < 1<<(n-1) {
			w.bits(1<<i, uint(i)+1)
			w.bits(uint32(q)&(1<<n-1), n)
			return
		}
	}
	w.bits(0, 3)
	w.bits(uint32(q)&uint32(uint64(1)<<raw-1), raw)
}

// EntityID is the client-side entity ID.
func (s Spawn) EntityID() uint32 { return 0x40000000 | uint32(s.ID) }

// ComponentUpdate is one mirrored component's data in a stream 4 create or update.
// FUN_7ff650c00560 reads u8 N, then N x (u8 component ID, component data); the
// component data (update visitor FUN_7ff650c00310 + FUN_7ff6508147d0) is, from the
// next byte, u8 L and L bytes holding one changed bit per field of the
// component's STU mirror type (own fields in order, then the parent type's),
// followed in the stream by the changed fields' values (each field type's reader,
// vfunc +0xb0, reads from the stream, not from the L bytes).
type ComponentUpdate struct {
	ID     byte
	Mask   []byte     // changed bits, one per field
	Values *bitWriter // the changed fields' values
}

// Mirror20 is component 0x20's update: its mirror has 3 entity ID fields (mirror
// +0x78/+0x7c/+0x80 = component +0x90 aim, +0x94 camera, +0x98 movement attach);
// an entity ID value is 32 raw bits (FUN_7ff650591a50).
func Mirror20(aim, camera, movement uint32) ComponentUpdate {
	v := &bitWriter{}
	for _, id := range []uint32{aim, camera, movement} {
		v.bits(id, 32)
	}
	return ComponentUpdate{ID: 0x20, Mask: []byte{0b111}, Values: v}
}

// Mirror10 is component 0x10's (FilterBits) update: its mirror (STU 0xfb184d9c) has one
// u64 field, pre_filter_bits (component +0x90). Mirror updates read u64s compressed
// (FUN_7ff650818e90 with flag 2 -> FUN_7ff650e29d40): a byte whose bit i says byte i is
// non-zero, then those bytes, low first (8 raw bytes read back as 0: their first byte was
// taken as the mask). FUN_7ff650c9f660 copies the value (bit 0x10 recomputed) to the
// final bits +0x98 that team checks use. Team t is bit 0x40000 << t (Prometheus
// Component_10_FilterBits::set_team).
func Mirror10(preFilterBits uint64) ComponentUpdate {
	return ComponentUpdate{ID: 0x10, Mask: []byte{0b1}, Values: compressedU64(preFilterBits)}
}

// PlayerInfo is component 0x3F's mirror (STU 0xDB2735A9, component +0x20; field order =
// the mirror type's, docs/MatchLogic.md "Match init and the join / lock-in sequence"):
// u64 +0x78 the confirmed hero (0x769a0079), u64 +0x80 / +0x88 (0x252d06e1, 0x28bb6bce,
// unknown), u64 +0x90 the pending hero (0x065c8044: the hero-select preview; the hero
// asset prefetch FUN_7ff650be5ff0 streams it, and PlayerInfoEventHookSystem stamps
// +0xC8 when it changes), i32 +0x98 / +0x9c (unknown), u8 +0xa0 slot, u8 +0xa1 / +0xa2 /
// +0xa3 (hero-select flags [?]; +0xa2 set = the prefetch keeps the pending hero without a
// recency limit). Only the fields set are sent.
type PlayerInfo struct {
	Hero, Pending                            *uint64
	Slot, HeroSelected, WasSelected, Enabled *uint8
}

// Mirror3F is component 0x3F's (PlayerInfo) update. u64 fields are compressed like
// Mirror10's, i32 fields 32 raw bits, u8 fields 8 raw bits (FUN_7ff65066b660).
func Mirror3F(p PlayerInfo) ComponentUpdate {
	v := &bitWriter{}
	mask := uint32(0)
	u64 := func(bit uint, x *uint64) {
		if x != nil {
			mask |= 1 << bit
			c := compressedU64(*x)
			for i := uint(0); i < c.nbit; i++ {
				v.bits(uint32(c.buf[i/8]>>(i%8)&1), 1)
			}
		}
	}
	u8 := func(bit uint, x *uint8) {
		if x != nil {
			mask |= 1 << bit
			v.bits(uint32(*x), 8)
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

// compressedU64 writes a u64 the way FUN_7ff650e29d40 reads it.
func compressedU64(x uint64) *bitWriter {
	v := &bitWriter{}
	var mask uint32
	for i := range 8 {
		if byte(x>>(8*i)) != 0 {
			mask |= 1 << i
		}
	}
	v.bits(mask, 8)
	for i := range 8 {
		if b := byte(x >> (8 * i)); b != 0 {
			v.bits(uint32(b), 8)
		}
	}
	return v
}

// TeamBit is the filter bit of team t (0-3).
func TeamBit(t int) uint64 { return 0x40000 << uint(t) }

// HealthPart is one entry of component 0x28's health arrays (an STU struct of 7
// fields, written without changed bits by FUN_7ff650593430; every field here is
// read as 32 or 8 raw bits).
type HealthPart struct {
	Enabled      uint32
	Max, Current float32
	Unknown      uint32 // +0xc
	Flag10       uint8  // Prometheus's player spawner sets 1
	Flag11       uint8  // "some_other_flag", 1 in Prometheus's spawner
	Flag12       uint8
}

func (h HealthPart) write(w *bitWriter) {
	w.bits(h.Enabled, 32)
	w.f32s(h.Max, h.Current)
	w.bits(h.Unknown, 32)
	w.bits(uint32(h.Flag10), 8)
	w.bits(uint32(h.Flag11), 8)
	w.bits(uint32(h.Flag12), 8)
}

// Mirror28 is component 0x28's (health) update. Its mirror (type
// 0x7ff65174d470, at component +0x20) has 52 fields: 16 normal health parts
// (component +0x98), 16 armour (+0x1d8), 16 shield (+0x318), two u32 and two
// bytes. This sets normal health part 0 only.
func Mirror28(normal HealthPart) ComponentUpdate {
	v := &bitWriter{}
	normal.write(v)
	mask := make([]byte, 7) // 52 fields
	mask[0] = 0x01
	return ComponentUpdate{ID: 0x28, Mask: mask, Values: v}
}

// PlayerHealth is what Prometheus's player spawner gives a hero (a full 200).
func PlayerHealth(max float32) ComponentUpdate {
	return Mirror28(HealthPart{Enabled: 1, Max: max, Current: max, Flag10: 1, Flag11: 1})
}

// writeComponentUpdates writes the update list that ends every create and is all
// of an update. The client keeps reading it after a create, so it must be there
// (N = 0) even without updates.
func (w *bitWriter) writeComponentUpdates(us []ComponentUpdate) {
	w.bits(uint32(len(us)), 8)
	for _, u := range us {
		w.bits(uint32(u.ID), 8)
		w.bytes(append([]byte{byte(len(u.Mask))}, u.Mask...)) // from the next byte
		if u.Values != nil {
			for i := uint(0); i < u.Values.nbit; i++ {
				w.bits(uint32(u.Values.buf[i/8]>>(i%8)&1), 1)
			}
		}
	}
}

// writeEntityData writes stream 4's data for s at w's position (op, u14 bit
// length, blob). The blob is written in place because the component updates'
// byte alignment is relative to the packet.
func (s Spawn) writeEntityData(w *bitWriter, frame uint32) {
	if s.Destroy {
		w.bits(3, 2) // op 3 = destroy: no length, no data
		return
	}
	if s.Update {
		w.bits(2, 2) // op 2 = update
	} else {
		w.bits(1, 2) // op 1 = create (FUN_7ff650c00d20)
	}
	lenPos := w.nbit
	w.bits(0, 14)
	start := w.nbit
	if !s.Update {
		s.writeCreate(w, frame)
	}
	w.writeComponentUpdates(s.Components)
	w.setBits(lenPos, uint32(w.nbit-start), 14)
}

func (s Spawn) writeCreate(b *bitWriter, frame uint32) {
	if s.FromMap {
		b.bits(1, 1)
		b.bits(s.MapIndex, 32)
		if s.Mover != nil {
			b.bits(1, 1)
			s.Mover.writeSnapshot(b, frame)
		} else {
			b.bits(0, 1) // no snapshot
		}
		return
	}
	b.bits(0, 1)
	b.varint(s.Def, entDefWidths)
	flags := uint32(s.Flags)
	if flags == 0 {
		flags = 0x08
	}
	if s.Mover != nil {
		flags |= 0x02
	}
	if flags&^0x3e != 0 {
		panic("spawn flags 0x01 and 0x40 need data we don't write yet")
	}
	b.bits(flags, 7)
	if flags&0x08 != 0 {
		b.f32s(s.Pos[:]...)
		b.f32s(s.Rot[:]...)
	}
	if flags&0x0c != 0 {
		b.f32s(s.Scale[:]...)
	}
	if flags&0x02 != 0 {
		if s.Mover == nil {
			panic("spawn flag 0x02 needs a Mover")
		}
		s.Mover.writeSnapshot(b, frame)
	}
}

// setBits overwrites n bits at bit position pos.
func (w *bitWriter) setBits(pos uint, v uint32, n uint) {
	for i := uint(0); i < n; i++ {
		byteI, bit := (pos+i)/8, (pos+i)%8
		if v>>i&1 != 0 {
			w.buf[byteI] |= 1 << bit
		} else {
			w.buf[byteI] &^= 1 << bit
		}
	}
}

// EncodeDataPayload builds a server data payload (reader FUN_7ff650c500a0): the
// frame number, stream 0 with msgs, the global parts of streams 1-4, then the
// entity section with spawns (creates or component updates).
//
// ack is stream 3's value: the newest client command frame received (the client
// drops its input history up to it, FUN_7ff650c07560 -> FUN_7ff650feef20).
func EncodeDataPayload(frame, ack uint32, msgs []JamMsg, spawns ...Spawn) ([]byte, error) {
	var w bitWriter
	w.varint(frame, frameWidths)
	if _, err := encodeJamStream(&w, msgs); err != nil {
		return nil, err
	}
	// Streams 1 and 2 have no global part. Stream 3 (FUN_7ff650c07430): a varint
	// (widths 18/24) = the input ack, and a u4 (flag 4 resets the client's input
	// state), passed on with the frame to FUN_7ff650c07560. Stream 4
	// (FUN_7ff650c02100): one bit (0 so far).
	w.varint(ack, frameWidths)
	w.bits(0, 4)
	w.bits(0, 1)
	if len(spawns) > 127 {
		return nil, fmt.Errorf("at most 127 entities per packet")
	}
	w.bits(uint32(len(spawns)), 7)
	for _, s := range spawns {
		w.bits(0, 1) // class 0x40000000
		w.bits(0, 1) // 8-bit entity ID
		w.bits(uint32(s.ID), 8)
		// Per stream a 1 bit then its entity data; a 0 ends the entity.
		if s.Scripts != nil {
			w.bits(1, 1) // stream 0: no entity data
			w.bits(1, 1) // stream 1 (FUN_7ff650c10140): Statescript sync
			for i := uint(0); i < s.Scripts.nbit; i++ {
				w.bits(uint32(s.Scripts.buf[i/8]>>(i%8)&1), 1)
			}
			w.bits(0, 1) // no more streams for this entity
			continue
		}
		w.bits(1, 1) // stream 0: no entity data
		w.bits(1, 1)
		w.bits(0, 1) // stream 1 (FUN_7ff650c10140): nothing
		w.bits(1, 1)
		w.bits(0, 2) // stream 2 (FUN_7ff650c14b70): neither part
		w.bits(1, 1)
		w.bits(0, 1) // stream 3 (FUN_7ff650c05bb0): nothing
		w.bits(1, 1) // stream 4 (FUN_7ff650bfff40): u2 op, u14 bit length, data
		s.writeEntityData(&w, frame)
	}
	return w.buf, nil
}

// EncodeJamStream builds a payload that carries only stream 0 (the client's
// direction: no frame number).
func EncodeJamStream(msgs []JamMsg) ([]byte, error) {
	return encodeJamStream(&bitWriter{}, msgs)
}

func encodeJamStream(w *bitWriter, msgs []JamMsg) ([]byte, error) {
	var rel, unrel []JamMsg
	for _, m := range msgs {
		if m.Reliable {
			rel = append(rel, m)
		} else {
			unrel = append(unrel, m)
		}
	}
	if len(rel) > 15 || len(unrel) > 15 {
		return nil, fmt.Errorf("at most 15 reliable and 15 unreliable messages per packet")
	}
	w.bits(1, 1)
	w.bits(uint32(len(rel)), 4)
	w.bits(uint32(len(unrel)), 4)
	for _, m := range rel {
		b := binary.LittleEndian.AppendUint32(nil, m.Seq)
		b = append(b, m.Channel, m.Index)
		w.bytes(append(b, m.Payload...))
		w.bools(m.Bools)
	}
	for _, m := range unrel {
		w.bytes(append([]byte{m.Channel, m.Index}, m.Payload...))
		w.bools(m.Bools)
	}
	return w.buf, nil
}

// JamStreamHeader reads stream 0's header from a data payload.
func JamStreamHeader(p []byte) (present bool, reliable, unreliable int) {
	if len(p) < 2 {
		return false, 0, 0
	}
	v := uint16(p[0]) | uint16(p[1])<<8
	return v&1 != 0, int(v >> 1 & 0xf), int(v >> 5 & 0xf)
}

// ParseJamStream splits the client's stream 0. JAM payloads aren't
// self-delimiting, so a message's length comes from sizeOf (nil or -1 = unknown:
// the rest of the payload is returned as that message's payload and parsing stops).
func ParseJamStream(p []byte, sizeOf func(id uint16, payload []byte) int) ([]JamMsg, error) {
	present, nrel, nunrel := JamStreamHeader(p)
	if !present {
		return nil, nil
	}
	b := p[2:]
	var out []JamMsg
	for i := 0; i < nrel+nunrel; i++ {
		m := JamMsg{Reliable: i < nrel}
		if m.Reliable {
			if len(b) < 6 {
				return out, fmt.Errorf("short reliable message header")
			}
			m.Seq = binary.LittleEndian.Uint32(b)
			b = b[4:]
		} else if len(b) < 2 {
			return out, fmt.Errorf("short unreliable message header")
		}
		m.Channel, m.Index = b[0], b[1]
		b = b[2:]
		id, _ := m.ID()
		n := -1
		if sizeOf != nil {
			n = sizeOf(id, b)
		}
		if n < 0 || n > len(b) {
			m.Payload = b
			return append(out, m), nil
		}
		m.Payload, b = b[:n], b[n:]
		out = append(out, m)
	}
	return out, nil
}

// Known payload sizes of client messages.
func clientMsgSize(id uint16, b []byte) int {
	// Trailing bools are bits; the next message's header starts at the next byte,
	// so they count as one byte here.
	switch id {
	case 21601: // {vec3, vec4, bool}
		return 12 + 16 + 1
	case 21602: // {}
		return 0
	case 21605: // {u64 hero, bool, bool}: hero select click
		return 8 + 1
	case 21603: // {u64 hero, u64, bool}: lock in (OK)
		return 8 + 8 + 1
	case 21608: // ClientOutInstance 21608 {u32 n, u64[] times}
		if len(b) < 8 {
			return -1
		}
		return 8 + 8*int(binary.LittleEndian.Uint32(b[4:]))
	}
	return -1
}

// LocalEntityMsg is ClientInInstance 20301 {u64, ID {u64, u64}, {u32 entity}, bool}:
// the handler (case 0x4f4d in ClientInInstance_Dispatch) stores the entity at
// entityAdmin+0x220, the local entity that system 8 waits for.
func LocalEntityMsg(entity uint32) JamMsg {
	b := make([]byte, 8+16)
	b = binary.LittleEndian.AppendUint32(b, entity)
	return JamMsg{Reliable: true, Channel: 0, Index: 1, Payload: b, Bools: []bool{false}}
}
