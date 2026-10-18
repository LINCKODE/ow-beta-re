package jam

import (
	"errors"
	"fmt"

	"owserver/inst/bits"
)

// Stream 0 of an instance data payload is the JAM message stream (client writer
// FUN_7ff651030df0, reader FUN_7ff6510304c0), the same in both directions:
//
//	bit present | u4 reliable count | u4 unreliable count
//	reliable:   (align) u32 seq | u8 channel | u8 index | payload
//	unreliable: (align) u8 channel | u8 index | payload
//
// The channel indexes the stream's protocol list (InstanceChannelOf); a payload is
// the message's JAM encoding, its trailing bools being bits of the stream (the
// next message header aligns again). After the last message the data payload
// continues at that exact bit with the next stream.
//
// The client delivers reliable messages in seq order and holds back anything
// after a gap; sequencing and resends belong to the link (inst/link).

// Envelope is one message of the instance stream.
type Envelope struct {
	Reliable bool
	Seq      uint32 // reliable only
	Msg      Message
}

// MaxPerKind is the most reliable (and unreliable) messages one packet can carry.
const MaxPerKind = 15

// instanceChannels is the instance stream's protocol list, by channel.
var instanceChannels = func() []*Protocol {
	var out []*Protocol
	for i := range protocols {
		if ch := protocols[i].InstanceChannel; ch >= 0 {
			for len(out) <= ch {
				out = append(out, nil)
			}
			out[ch] = &protocols[i]
		}
	}
	return out
}()

// InstanceChannelOf returns the instance stream channel and index of a message ID.
func InstanceChannelOf(id uint16) (ch, idx uint8, ok bool) {
	for i, p := range instanceChannels {
		if id >= p.Base && id < p.Max {
			return uint8(i), uint8(id - p.Base), true
		}
	}
	return 0, 0, false
}

// InstanceMessageID returns the message ID of an instance stream channel and index.
func InstanceMessageID(ch, idx uint8) (uint16, bool) {
	if int(ch) >= len(instanceChannels) {
		return 0, false
	}
	p := instanceChannels[ch]
	id := p.Base + uint16(idx)
	return id, id < p.Max
}

// WriteStream writes stream 0 carrying env at w's position (reliable messages
// first, in the given order).
func WriteStream(w *bits.Writer, env []Envelope) error {
	var rel, unrel []Envelope
	for _, e := range env {
		if e.Reliable {
			rel = append(rel, e)
		} else {
			unrel = append(unrel, e)
		}
	}
	if len(rel) > MaxPerKind || len(unrel) > MaxPerKind {
		return fmt.Errorf("jam: at most %d reliable and %d unreliable messages per packet (%d, %d)", MaxPerKind, MaxPerKind, len(rel), len(unrel))
	}
	for _, e := range env {
		if _, _, ok := InstanceChannelOf(e.Msg.ID()); !ok {
			return fmt.Errorf("jam: message %d isn't on the instance stream", e.Msg.ID())
		}
	}
	w.Bits(1, 1)
	w.Bits(uint32(len(rel)), 4)
	w.Bits(uint32(len(unrel)), 4)
	for _, e := range append(rel, unrel...) {
		ch, idx, _ := InstanceChannelOf(e.Msg.ID())
		if e.Reliable {
			putU32(w, e.Seq)
		}
		w.Bytes([]byte{ch, idx})
		e.Msg.Encode(w)
	}
	return nil
}

// WriteEmptyStream writes stream 0 without messages (the present bit 0).
func WriteEmptyStream(w *bits.Writer) { w.Bits(0, 1) }

// ErrUndelimited is returned by ReadStream when a message has no catalogued
// layout: its payload (a *Raw with the rest of the input) can't be delimited, so
// the messages after it and the following streams are lost.
var ErrUndelimited = errors.New("jam: message without a layout: rest of the stream undelimited")

// ReadStream reads stream 0 from r's position and leaves r right after the last
// message's bits.
func ReadStream(r *bits.Reader) ([]Envelope, error) {
	if !r.Bool() {
		return nil, r.Err()
	}
	nrel, nunrel := int(r.Bits(4)), int(r.Bits(4))
	if err := r.Err(); err != nil {
		return nil, err
	}
	out := make([]Envelope, 0, nrel+nunrel)
	for i := 0; i < nrel+nunrel; i++ {
		e := Envelope{Reliable: i < nrel}
		d := decoder{r: r}
		if e.Reliable {
			e.Seq = d.getU32()
		}
		ch, idx := d.getU8(), d.getU8()
		if err := d.err(); err != nil {
			return out, fmt.Errorf("jam: stream message %d header: %w", i, err)
		}
		id, ok := InstanceMessageID(ch, idx)
		if !ok {
			return out, fmt.Errorf("jam: stream message %d: channel %d index %d", i, ch, idx)
		}
		m, known := New(id)
		if !known {
			raw := &Raw{Id: id}
			raw.Decode(r)
			e.Msg = raw
			return append(out, e), fmt.Errorf("%w (message %d)", ErrUndelimited, id)
		}
		if err := m.Decode(r); err != nil {
			return out, fmt.Errorf("jam: stream message %s (%d): %w", Name(id), id, err)
		}
		e.Msg = m
		out = append(out, e)
	}
	return out, nil
}
