// Package wire is the layout of a data packet's payload (the bytes after the
// 34-byte packet header, see package link) in both directions.
//
// A payload is a bit stream written by the link's five streams in order (client:
// game system 45, FUN_7ff650c52bc0, one packet per 16 ms command frame).
//
// Server -> client (reader FUN_7ff650c500a0):
//
//	varint P                 the server's command frame (Frame; widths 18/24, else 32)
//	stream 0                 JAM messages (jam.WriteStream)
//	stream 3 global          varint A (InputAck, widths 18/24) + u4 clock flags (package clock)
//	stream 4 global          one bit (0 so far)
//	u7 entity count, then per entity:
//	  bit class (0 = 0x40000000) + ID (0 + 8 bits | 1 0 + 16 | 1 1 + 32, FUN_7ff650ee4ff0)
//	  per stream 0..4: bit 1 + that stream's entity data; a 0 ends the entity early
//	    stream 0: none; stream 1: Statescript sync (FUN_7ff650c10140);
//	    stream 2 (FUN_7ff650c14b70): two absent bits; stream 3 (FUN_7ff650c05bb0): one absent bit;
//	    stream 4 (FUN_7ff650bfff40): u2 op, u14 bit length, data (package replication)
//
// Client -> server (writer FUN_7ff650c52bc0), no leading frame number:
//
//	stream 0                 JAM messages
//	stream 3                 bit present | u32 bit length | u16 timing | input history (Input)
//
// Streams 1 and 2 write nothing from the client.
package wire

import (
	"fmt"

	"owserver/inst/bits"
	"owserver/inst/jam"
)

// Varint widths (FUN_7ff65100dff0 with the tables at 0x7ff651716c58 / ...6838).
var (
	FrameWidths  = [2]uint{18, 24}
	EntDefWidths = [2]uint{12, 20}
)

// Varint writes the variable-length number read by FUN_7ff65100dff0: a 1 bit and
// widths[0] bits, or 0 1 and widths[1] bits, or 0 0 and 32 bits.
func Varint(w *bits.Writer, v uint32, widths [2]uint) {
	switch {
	case v < 1<<widths[0]:
		w.Bits(1, 1)
		w.Bits(v, widths[0])
	case v < 1<<widths[1]:
		w.Bits(0b10, 2) // 0 then 1
		w.Bits(v, widths[1])
	default:
		w.Bits(0, 2)
		w.Bits(v, 32)
	}
}

// ReadVarint reads what Varint writes.
func ReadVarint(r *bits.Reader, widths [2]uint) uint32 {
	if r.Bool() {
		return r.Bits(widths[0])
	}
	if r.Bool() {
		return r.Bits(widths[1])
	}
	return r.Bits(32)
}

// Entity is one entity's section of a server payload. The entity is 0x40000000|ID.
type Entity struct {
	ID uint32
	// Scripts is stream 1's data (a Statescript packet, starting with its present bit).
	Scripts *bits.Writer
	// State writes stream 4's data (op, length, blob) in place: its component updates
	// align to bytes relative to the packet, so it can't be built separately.
	State func(w *bits.Writer)
}

func writeEntityID(w *bits.Writer, id uint32) {
	w.Bits(0, 1) // class 0x40000000
	switch {
	case id < 1<<8:
		w.Bits(0, 1)
		w.Bits(id, 8)
	case id < 1<<16:
		w.Bits(0b01, 2) // 1 then 0
		w.Bits(id, 16)
	default:
		w.Bits(0b11, 2)
		w.Bits(id, 32)
	}
}

func (e Entity) write(w *bits.Writer) {
	writeEntityID(w, e.ID)
	w.Bits(1, 1) // stream 0: no entity data
	if e.State == nil {
		w.Bits(1, 1)
		if e.Scripts != nil {
			w.Append(e.Scripts)
		} else {
			w.Bits(0, 1)
		}
		w.Bits(0, 1) // ends the entity
		return
	}
	w.Bits(1, 1)
	if e.Scripts != nil {
		w.Append(e.Scripts)
	} else {
		w.Bits(0, 1) // stream 1: nothing
	}
	w.Bits(1, 1)
	w.Bits(0, 2) // stream 2: neither part
	w.Bits(1, 1)
	w.Bits(0, 1) // stream 3: nothing
	w.Bits(1, 1)
	e.State(w)
}

// ServerPayload is a server -> client data payload.
type ServerPayload struct {
	Frame    uint32         // P
	Jam      []jam.Envelope // stream 0
	InputAck uint32         // A: the newest client command frame received
	Clock    uint8          // the u4 clock flags (package clock)
	Entities []Entity
}

// MaxEntities is the most entities one payload can carry (u7 count).
const MaxEntities = 127

// Encode builds the payload.
func (p *ServerPayload) Encode() ([]byte, error) {
	var w bits.Writer
	if err := p.EncodeTo(&w); err != nil {
		return nil, err
	}
	return w.Buf(), nil
}

// EncodeTo writes the payload at w's position (a fresh writer: alignment is packet-relative).
func (p *ServerPayload) EncodeTo(w *bits.Writer) error {
	Varint(w, p.Frame, FrameWidths)
	if err := jam.WriteStream(w, p.Jam); err != nil {
		return err
	}
	Varint(w, p.InputAck, FrameWidths)
	w.Bits(uint32(p.Clock&0xf), 4)
	w.Bits(0, 1) // stream 4's global bit
	if len(p.Entities) > MaxEntities {
		return fmt.Errorf("wire: at most %d entities per payload", MaxEntities)
	}
	w.Bits(uint32(len(p.Entities)), 7)
	for _, e := range p.Entities {
		e.write(w)
	}
	return nil
}

// ServerHeader is what DecodeServerHeader reads back from a server payload (tests,
// tools): everything before the entity section.
type ServerHeader struct {
	Frame, InputAck uint32
	Clock           uint8
	Jam             []jam.Envelope
	Entities        int
}

// DecodeServerHeader reads a server payload up to the entity count.
func DecodeServerHeader(b []byte) (ServerHeader, error) {
	r := bits.NewReader(b)
	var h ServerHeader
	h.Frame = ReadVarint(r, FrameWidths)
	env, err := jam.ReadStream(r)
	if err != nil {
		return h, err
	}
	h.Jam = env
	h.InputAck = ReadVarint(r, FrameWidths)
	h.Clock = uint8(r.Bits(4))
	r.Bits(1)
	h.Entities = int(r.Bits(7))
	return h, r.Err()
}
