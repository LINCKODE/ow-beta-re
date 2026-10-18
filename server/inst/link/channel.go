package link

import (
	"owserver/inst/jam"
)

// Channel is one connection's JAM message stream (stream 0 of every data packet)
// with its reliability: reliable messages are numbered 0, 1, 2... per direction;
// the client delivers them in order and holds back everything after a gap, so a
// reliable message in a lost packet must be sent again with its number. It is not
// safe for concurrent use: the game loop owns it.
type Channel struct {
	nextOut  uint32
	resend   []jam.Envelope            // lost, to go out first
	queue    []jam.Envelope            // new
	inFlight map[uint32][]jam.Envelope // our packet seq -> the reliable messages in it

	nextIn  uint32                  // next reliable seq to deliver
	held    map[uint32]jam.Envelope // received ahead of a gap
	Resends int                     // reliable messages sent again (stats)
	Skips   int                     // receive gaps given up on (stats)
}

// MaxPerPacket is the most reliable (and unreliable) messages one packet carries.
const MaxPerPacket = 15

// maxHeld is how many reliable messages Receive holds back behind a gap.
const maxHeld = 64

func NewChannel() *Channel {
	return &Channel{inFlight: map[uint32][]jam.Envelope{}, held: map[uint32]jam.Envelope{}}
}

// Send queues a message.
func (c *Channel) Send(m jam.Message, reliable bool) {
	c.queue = append(c.queue, jam.Envelope{Reliable: reliable, Msg: m})
}

// Pending reports whether messages are waiting to go out.
func (c *Channel) Pending() bool { return len(c.resend)+len(c.queue) > 0 }

// Take returns the messages for the next packet: resends first, then new messages in
// order, at most MaxPerPacket of each kind. Reliable ones get their numbers here.
func (c *Channel) Take() []jam.Envelope {
	var out []jam.Envelope
	rel, unrel := 0, 0
	for len(c.resend) > 0 && rel < MaxPerPacket {
		out = append(out, c.resend[0])
		c.resend = c.resend[1:]
		rel++
		c.Resends++
	}
	keep := c.queue[:0]
	blocked := false // keep reliable order: once one waits, later ones wait too
	for _, e := range c.queue {
		switch {
		case e.Reliable && !blocked && rel < MaxPerPacket:
			e.Seq = c.nextOut
			c.nextOut++
			rel++
			out = append(out, e)
		case !e.Reliable && unrel < MaxPerPacket:
			unrel++
			out = append(out, e)
		default:
			if e.Reliable {
				blocked = true
			}
			keep = append(keep, e)
		}
	}
	c.queue = keep
	return out
}

// Sent records which reliable messages went out in packet seq.
func (c *Channel) Sent(seq uint32, env []jam.Envelope) {
	var rel []jam.Envelope
	for _, e := range env {
		if e.Reliable {
			rel = append(rel, e)
		}
	}
	if len(rel) > 0 {
		c.inFlight[seq] = rel
	}
}

// Unsent puts the reliable messages of a packet that could not be sent back in front.
func (c *Channel) Unsent(env []jam.Envelope) {
	var rel []jam.Envelope
	for _, e := range env {
		if e.Reliable {
			rel = append(rel, e)
		}
	}
	c.resend = append(rel, c.resend...)
}

// Acked and Lost take the link's packet reports.
func (c *Channel) Acked(seqs []uint32) {
	for _, s := range seqs {
		delete(c.inFlight, s)
	}
}

func (c *Channel) Lost(seqs []uint32) {
	for _, s := range seqs {
		if env, ok := c.inFlight[s]; ok {
			c.resend = append(c.resend, env...)
			delete(c.inFlight, s)
		}
	}
}

// Receive takes the messages of one client packet and returns the ones to handle
// now, in order: unreliable ones at once, reliable ones in sequence (duplicates
// dropped, ones after a gap held back until it fills).
func (c *Channel) Receive(env []jam.Envelope) []jam.Envelope {
	var out []jam.Envelope
	for _, e := range env {
		if !e.Reliable {
			out = append(out, e)
			continue
		}
		if int32(e.Seq-c.nextIn) < 0 {
			continue // duplicate
		}
		c.held[e.Seq] = e
		if len(c.held) > maxHeld {
			// A gap that doesn't fill (the client numbers from 0 per connection, and a new
			// Channel is made per SYN, so this should not happen): skip it.
			c.nextIn = e.Seq
			for s := range c.held {
				if int32(s-c.nextIn) < 0 {
					c.nextIn = s
				}
			}
			c.Skips++
		}
		for {
			h, ok := c.held[c.nextIn]
			if !ok {
				break
			}
			delete(c.held, c.nextIn)
			c.nextIn++
			out = append(out, h)
		}
	}
	return out
}
