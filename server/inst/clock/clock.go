// Package clock is the server side of the command-frame clock
// (docs/CommandFrames.md): the 16 ms tick that numbers the server's frames, and
// the steering of each client's clock with the u4 flags every data packet carries
// in stream 3.
//
// The client runs ahead of the server by its lead: it simulates command frame cf
// while the last server frame it has seen is P. Its input for cf has to reach the
// server before the server simulates cf, with some safety margin. The client
// resyncs itself only when it falls behind P (to P + min(16, RTT frames)); every
// other correction comes from the server: flag 0x1 slows its clock (-200 µs per
// render frame), 0x2 speeds it up (+200 µs), 0x8 makes either ±1000 µs, 0x4 resyncs
// (FUN_7ff650c07560, FUN_7ff650ce63a0). The client keeps the last flags it got.
package clock

import (
	"context"
	"time"
)

// FrameDuration is one command frame (62.5 Hz).
const FrameDuration = 16 * time.Millisecond

// Clock flags (stream 3's u4, stored at the client's clock+0xa8).
const (
	FlagSlow   uint8 = 0x1
	FlagFast   uint8 = 0x2
	FlagResync uint8 = 0x4
	FlagStrong uint8 = 0x8
)

// Ticker calls a function every FrameDuration on a drift-free schedule
// (start + n x 16 ms). When it falls behind it runs the missed ticks back to back,
// up to MaxCatchUp, and otherwise drops them (and moves the schedule).
type Ticker struct {
	MaxCatchUp int // default 4: the client grows its safety buffer when P jumps by more
	Late       int // ticks dropped because the loop fell too far behind (stats)
}

// Run calls tick(n, now) with n = 0, 1, 2... until ctx ends.
func (t *Ticker) Run(ctx context.Context, tick func(n uint64, now time.Time)) {
	maxCatch := t.MaxCatchUp
	if maxCatch == 0 {
		maxCatch = 4
	}
	start := time.Now()
	var n uint64
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		now := time.Now()
		due := uint64(now.Sub(start) / FrameDuration)
		if due >= n+uint64(maxCatch) {
			// Too far behind (e.g. the process was stopped): skip ahead.
			t.Late += int(due - n)
			start = start.Add(time.Duration(due-n) * FrameDuration)
			due = n
		}
		for ; n <= due; n++ {
			tick(n, now)
		}
		timer.Reset(time.Until(start.Add(time.Duration(n) * FrameDuration)))
	}
}

// Policy is how the server steers a client's lead. Lead here is what the server
// sees: the client's newest input frame minus the server's frame when it arrived.
// The zero value takes the defaults.
type Policy struct {
	// Target is the lead to hold (frames): how early input should arrive. Default 4
	// (64 ms, about the client's minimum safety buffer of 52 ms, clock+0x88).
	Target int
	// Band is the dead band around Target (default 1): no correction inside it.
	Band int
	// Strong: beyond Target ± Strong frames the strong variant (0x8) is used (default 8).
	Strong int
	// Resync: beyond Target ± Resync frames the client is resynced (0x4; default 60).
	Resync int
	// Window is how many lead samples the control uses (their minimum: the earliest
	// input has to be in time too; default 16, ~0.25 s of input packets).
	Window int
	// ResyncCooldown is the least time between resyncs (default 1 s): the flag stays
	// set on the client until the next packet, so it goes out in one packet only.
	ResyncCooldown time.Duration
}

func (p Policy) withDefaults() Policy {
	if p.Target == 0 {
		p.Target = 4
	}
	if p.Band == 0 {
		p.Band = 1
	}
	if p.Strong == 0 {
		p.Strong = 8
	}
	if p.Resync == 0 {
		p.Resync = 60
	}
	if p.Window == 0 {
		p.Window = 16
	}
	if p.ResyncCooldown == 0 {
		p.ResyncCooldown = time.Second
	}
	return p
}

// Steer is one client's clock steering. It is not safe for concurrent use.
type Steer struct {
	p          Policy
	samples    []int32
	next       int
	have       bool
	resync     bool // resync in the next packet
	lastResync time.Time
	flags      uint8
	last       uint8

	// Stats (control API).
	Lead      int32 // newest lead sample
	MinLead   int32 // the window's minimum (what the control acts on)
	Resyncs   int
	Timing    uint16 // the client's own report (stream 3 u16, docs/CommandFrames.md §5)
	InputSeen bool
}

func NewSteer(p Policy) *Steer {
	p = p.withDefaults()
	return &Steer{p: p, samples: make([]int32, p.Window)}
}

// Policy is the steering policy in use (with its defaults filled in).
func (s *Steer) Policy() Policy { return s.p }

// SetPolicy changes the policy (control API experiments); the samples are kept.
func (s *Steer) SetPolicy(p Policy) { s.p = p.withDefaults() }

// Reset starts over (a new connection) and resyncs the client in the next packet:
// a client that comes back to a restarted server may be far ahead of its frames.
func (s *Steer) Reset() {
	s.have, s.next, s.flags, s.InputSeen = false, 0, 0, false
	s.resync = true
	s.lastResync = time.Time{}
}

// LastFlags are the flags of the last packet.
func (s *Steer) LastFlags() uint8 { return s.last }

// Observe records a client input packet: its newest input frame, arriving while
// the server is at frame.
func (s *Steer) Observe(input, frame uint32, timing uint16) {
	lead := int32(input - frame)
	s.Lead, s.Timing, s.InputSeen = lead, timing, true
	if !s.have {
		for i := range s.samples {
			s.samples[i] = lead
		}
		s.have = true
	}
	s.samples[s.next] = lead
	s.next = (s.next + 1) % len(s.samples)
}

// Flags returns the u4 for the next packet at now.
func (s *Steer) Flags(now time.Time) uint8 {
	s.last = s.decide(now)
	return s.last
}

func (s *Steer) decide(now time.Time) uint8 {
	if s.resync {
		s.resync = false
		s.lastResync = now
		s.Resyncs++
		s.have = false // the samples are from before the resync
		s.flags = 0
		return FlagResync
	}
	if !s.have {
		return s.flags
	}
	lead := s.samples[0]
	for _, v := range s.samples[1:] {
		lead = min(lead, v)
	}
	s.MinLead = lead
	s.flags = Decide(s.p, int(lead))
	if s.flags&FlagResync != 0 {
		if now.Sub(s.lastResync) < s.p.ResyncCooldown {
			// Waiting for the last resync to show: steer hard meanwhile.
			s.flags = FlagFast | FlagStrong
			if int(lead) > s.p.Target {
				s.flags = FlagSlow | FlagStrong
			}
		} else {
			s.lastResync = now
			s.Resyncs++
			s.have = false
			s.flags = 0
			return FlagResync
		}
	}
	return s.flags
}

// Decide maps a lead to flags under policy p (the pure part of Steer).
func Decide(p Policy, lead int) uint8 {
	p = p.withDefaults()
	d := lead - p.Target
	switch {
	case d > p.Resync || d < -p.Resync:
		return FlagResync
	case d > p.Strong:
		return FlagSlow | FlagStrong
	case d > p.Band:
		return FlagSlow
	case d < -p.Strong:
		return FlagFast | FlagStrong
	case d < -p.Band:
		return FlagFast
	}
	return 0
}
