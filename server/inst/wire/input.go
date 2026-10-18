package wire

import (
	"fmt"

	"owserver/inst/bits"
	"owserver/inst/jam"
)

// The client's stream 3 (FUN_7ff650c05260 / FUN_7ff650c053d0): u16 timing, then the
// input history (FUN_7ff650fef1c0): every command frame the server hasn't
// acknowledged yet (the ack is ServerPayload.InputAck):
//
//	u8 K, then K entries, oldest first:
//	  u32 command frame
//	  first entry: s16 s16 s8 s8 u16 u8 (FUN_7ff650feee90) + u16
//	  others: u8 change mask vs the previous entry, then the changed fields:
//	    0x01 s16, 0x02 s16, 0x04 s8, 0x08 s8, 0x10 u16, 0x20 u8, 0x40 u16
//
// Field meanings are guesses from their shapes [?]: two aim angles, two movement
// axes, buttons, an extra byte and a trailing u16. Timing is (lead x 16 ms +
// safety buffer) x scale (docs/CommandFrames.md §5).

// InputEntry is one command frame of client input.
type InputEntry struct {
	Frame        uint32
	Aim1, Aim2   int16
	Move1, Move2 int8
	Buttons      uint16
	Extra        uint8
	Tail         uint16
}

// Input is stream 3 of a client payload.
type Input struct {
	Timing  uint16
	Entries []InputEntry
}

// Newest returns the newest command frame in the history (the one to acknowledge).
func (in *Input) Newest() (uint32, bool) {
	if in == nil || len(in.Entries) == 0 {
		return 0, false
	}
	return in.Entries[len(in.Entries)-1].Frame, true
}

// ClientPayload is a client -> server data payload.
type ClientPayload struct {
	Jam   []jam.Envelope
	Input *Input // nil when stream 3 is absent
}

// ParseClientPayload decodes a client payload. On a JAM message without a codec the
// messages before it are returned with the error, and the input can't be found.
func ParseClientPayload(b []byte) (ClientPayload, error) {
	r := bits.NewReader(b)
	var p ClientPayload
	env, err := jam.ReadStream(r)
	p.Jam = env
	if err != nil {
		return p, err
	}
	if !r.Bool() {
		return p, nil
	}
	length := r.Bits(32)
	if int(length) > r.Left() {
		return p, fmt.Errorf("wire: input length %d bits but only %d left", length, r.Left())
	}
	in := &Input{Timing: uint16(r.Bits(16))}
	k := int(r.Bits(8))
	var prev InputEntry
	for i := 0; i < k; i++ {
		e := InputEntry{Frame: r.Bits(32)}
		if i == 0 {
			e.Aim1, e.Aim2 = int16(r.Bits(16)), int16(r.Bits(16))
			e.Move1, e.Move2 = int8(r.Bits(8)), int8(r.Bits(8))
			e.Buttons, e.Extra = uint16(r.Bits(16)), uint8(r.Bits(8))
			e.Tail = uint16(r.Bits(16))
		} else {
			frame := e.Frame
			e = prev
			e.Frame = frame
			mask := r.Bits(8)
			if mask&0x01 != 0 {
				e.Aim1 = int16(r.Bits(16))
			}
			if mask&0x02 != 0 {
				e.Aim2 = int16(r.Bits(16))
			}
			if mask&0x04 != 0 {
				e.Move1 = int8(r.Bits(8))
			}
			if mask&0x08 != 0 {
				e.Move2 = int8(r.Bits(8))
			}
			if mask&0x10 != 0 {
				e.Buttons = uint16(r.Bits(16))
			}
			if mask&0x20 != 0 {
				e.Extra = uint8(r.Bits(8))
			}
			if mask&0x40 != 0 {
				e.Tail = uint16(r.Bits(16))
			}
		}
		if r.Err() != nil {
			return p, fmt.Errorf("wire: input history runs past the payload")
		}
		in.Entries = append(in.Entries, e)
		prev = e
	}
	p.Input = in
	return p, r.Err()
}
