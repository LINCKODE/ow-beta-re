package instance

import (
	"encoding/binary"
	"fmt"
)

// Client -> server data payloads (writer FUN_7ff650c52bc0, streams in order):
//
//	stream 0 (JAM, FUN_7ff651030df0): bit present | u4 reliable | u4 unreliable |
//	         messages from the next byte (see ParseJamStream); without messages
//	         the next stream follows at the next bit
//	stream 3 (input, FUN_7ff650c05260): bit present | u32 bit length | data
//	         data (FUN_7ff650c053d0): u16 timing | input history (FUN_7ff650fef1c0)
//
// The input history is every command frame the server hasn't acknowledged yet
// (the ack is stream 3's value in our packets, see EncodeDataPayload):
//
//	u8 K, then K entries, oldest first:
//	  u32 command frame
//	  first entry: s16 s16 s8 s8 u16 u8 (FUN_7ff650feee90) + u16
//	  others: u8 change mask vs the previous entry, then the changed fields:
//	    0x01 s16, 0x02 s16, 0x04 s8, 0x08 s8, 0x10 u16, 0x20 u8, 0x40 u16
//
// The field meanings are guesses from their shapes: two aim angles, two movement
// axes, buttons, an extra byte and a trailing u16.

// InputEntry is one command frame of client input.
type InputEntry struct {
	Frame        uint32
	Aim1, Aim2   int16 // +8, +0xa
	Move1, Move2 int8  // +0xc, +0xd
	Buttons      uint16
	Extra        uint8
	Tail         uint16 // +0x14
}

// ClientInput is stream 3 of a client data packet.
type ClientInput struct {
	Timing  uint16
	Entries []InputEntry
}

// Newest returns the newest command frame (the one to acknowledge).
func (in *ClientInput) Newest() (uint32, bool) {
	if in == nil || len(in.Entries) == 0 {
		return 0, false
	}
	return in.Entries[len(in.Entries)-1].Frame, true
}

type bitReader struct {
	b   []byte
	pos uint // bit position
}

func (r *bitReader) bits(n uint) uint32 {
	var v uint32
	for i := uint(0); i < n; i++ {
		byteI := r.pos / 8
		if byteI < uint(len(r.b)) && r.b[byteI]>>(r.pos%8)&1 != 0 {
			v |= 1 << i
		}
		r.pos++
	}
	return v
}

func (r *bitReader) align() { r.pos = (r.pos + 7) / 8 * 8 }

func (r *bitReader) left() int { return len(r.b)*8 - int(r.pos) }

// ParseClientPayload splits a client data payload into its JAM messages and its
// input history (nil when stream 3 is absent).
func ParseClientPayload(p []byte) ([]JamMsg, *ClientInput, error) {
	r := &bitReader{b: p}
	var msgs []JamMsg
	if r.bits(1) == 1 {
		nrel, nunrel := int(r.bits(4)), int(r.bits(4))
		if nrel+nunrel > 0 {
			for i := 0; i < nrel+nunrel; i++ {
				// Message headers are read from the next whole byte, also after a
				// previous message's trailing bool bits.
				r.align()
				m := JamMsg{Reliable: i < nrel}
				start := r.pos / 8
				if m.Reliable {
					if int(start)+4 > len(p) {
						return msgs, nil, fmt.Errorf("short reliable header")
					}
					m.Seq = binary.LittleEndian.Uint32(p[start:])
					start += 4
				}
				if int(start)+2 > len(p) {
					return msgs, nil, fmt.Errorf("short message header")
				}
				m.Channel, m.Index = p[start], p[start+1]
				start += 2
				id, _ := m.ID()
				n := clientMsgSize(id, p[start:])
				if n < 0 || int(start)+n > len(p) {
					m.Payload = p[start:]
					msgs = append(msgs, m)
					return msgs, nil, fmt.Errorf("message %d has an unknown length; the input after it can't be found", id)
				}
				m.Payload = p[start : int(start)+n]
				msgs = append(msgs, m)
				// Trailing bools are bits: the packet goes on right after them.
				if b := clientMsgBools(id); b > 0 {
					r.pos = (start+uint(n)-1)*8 + b
				} else {
					r.pos = (start + uint(n)) * 8
				}
			}
		}
	}
	// Streams 1 and 2 write nothing.
	if r.bits(1) == 0 {
		return msgs, nil, nil
	}
	length := r.bits(32)
	if int(length) > r.left() {
		return msgs, nil, fmt.Errorf("input length %d bits but only %d left", length, r.left())
	}
	in := &ClientInput{Timing: uint16(r.bits(16))}
	k := int(r.bits(8))
	var prev InputEntry
	for i := 0; i < k; i++ {
		e := InputEntry{Frame: r.bits(32)}
		if i == 0 {
			e.Aim1, e.Aim2 = int16(r.bits(16)), int16(r.bits(16))
			e.Move1, e.Move2 = int8(r.bits(8)), int8(r.bits(8))
			e.Buttons, e.Extra = uint16(r.bits(16)), uint8(r.bits(8))
			e.Tail = uint16(r.bits(16))
		} else {
			e.Aim1, e.Aim2, e.Move1, e.Move2, e.Buttons, e.Extra, e.Tail = prev.Aim1, prev.Aim2, prev.Move1, prev.Move2, prev.Buttons, prev.Extra, prev.Tail
			mask := r.bits(8)
			if mask&0x01 != 0 {
				e.Aim1 = int16(r.bits(16))
			}
			if mask&0x02 != 0 {
				e.Aim2 = int16(r.bits(16))
			}
			if mask&0x04 != 0 {
				e.Move1 = int8(r.bits(8))
			}
			if mask&0x08 != 0 {
				e.Move2 = int8(r.bits(8))
			}
			if mask&0x10 != 0 {
				e.Buttons = uint16(r.bits(16))
			}
			if mask&0x20 != 0 {
				e.Extra = uint8(r.bits(8))
			}
			if mask&0x40 != 0 {
				e.Tail = uint16(r.bits(16))
			}
		}
		if r.left() < 0 {
			return msgs, in, fmt.Errorf("input history runs past the payload")
		}
		in.Entries = append(in.Entries, e)
		prev = e
	}
	return msgs, in, nil
}

// clientMsgBools is the number of trailing bool bits of a client message (their
// byte is counted in clientMsgSize).
func clientMsgBools(id uint16) uint {
	switch id {
	case 21601, 21603:
		return 1
	case 21605:
		return 2
	}
	return 0
}
