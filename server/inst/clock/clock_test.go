package clock

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestDecide(t *testing.T) {
	p := Policy{} // target 4, band 1, strong 8, resync 60
	for _, c := range []struct {
		lead int
		want uint8
	}{
		{4, 0}, {5, 0}, {3, 0},
		{6, FlagSlow}, {12, FlagSlow}, {13, FlagSlow | FlagStrong},
		{2, FlagFast}, {-4, FlagFast}, {-5, FlagFast | FlagStrong},
		{64, FlagSlow | FlagStrong}, {65, FlagResync}, {-57, FlagResync},
	} {
		if got := Decide(p, c.lead); got != c.want {
			t.Errorf("lead %d: flags %#x, want %#x", c.lead, got, c.want)
		}
	}
}

func TestSteer(t *testing.T) {
	now := time.Unix(1000, 0)
	s := NewSteer(Policy{Window: 4})
	s.Reset()
	if f := s.Flags(now); f != FlagResync {
		t.Fatalf("first packet after a (re)connect: %#x", f)
	}
	if f := s.Flags(now); f != 0 {
		t.Fatalf("no input yet: %#x", f)
	}
	// The measured case from the R&D server: ~35 frames ahead -> slow down, strongly.
	for i := range 4 {
		s.Observe(uint32(1035+i), uint32(1000+i), 620)
	}
	if f := s.Flags(now); f != FlagSlow|FlagStrong || s.MinLead != 35 {
		t.Fatalf("lead 35: %#x (min %d)", f, s.MinLead)
	}
	// One early sample doesn't hide a late one: the window minimum decides.
	s.Observe(1100, 1050, 0)
	s.Observe(1052, 1050, 0)
	s.Observe(1110, 1050, 0)
	s.Observe(1110, 1050, 0)
	if f := s.Flags(now); f != FlagFast || s.MinLead != 2 {
		t.Fatalf("window min: %#x (min %d)", f, s.MinLead)
	}
	// Far ahead: resync, then not again within the cooldown.
	for range 4 {
		s.Observe(2000, 1000, 0)
	}
	now = now.Add(2 * time.Second) // past the connect resync's cooldown
	if f := s.Flags(now); f != FlagResync {
		t.Fatalf("far ahead: %#x", f)
	}
	for range 4 {
		s.Observe(2000, 1000, 0)
	}
	if f := s.Flags(now.Add(100 * time.Millisecond)); f != FlagSlow|FlagStrong {
		t.Fatalf("cooldown: %#x", f)
	}
	if f := s.Flags(now.Add(2 * time.Second)); f != FlagResync || s.Resyncs != 3 {
		t.Fatalf("after the cooldown: %#x (%d resyncs)", f, s.Resyncs)
	}
}

func TestTicker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var n atomic.Uint64
	var last uint64
	start := time.Now()
	(&Ticker{}).Run(ctx, func(i uint64, _ time.Time) {
		if i != n.Load() {
			t.Errorf("tick %d after %d", i, last)
		}
		last = i
		n.Add(1)
	})
	el := time.Since(start)
	want := uint64(el/FrameDuration) + 1
	if got := n.Load(); got+1 < want || got > want {
		t.Fatalf("%d ticks in %v, want ~%d", got, el, want)
	}
}
