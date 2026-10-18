package ecs

import "testing"

// The local controller as the game server spawns it (see fixtures/trace_setup_controller.json).
func TestControllerSpawn(t *testing.T) {
	a, err := NewAdmin("game")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	loader := []byte{0x0e, 0x13, 0x2f, 0x33, 0x3e, 0x3f} // client add order; dependencies follow
	if id := a.Spawn(0x40000001, loader, 0); id != 0x40000001 {
		t.Fatalf("spawn: %#x", id)
	}
	want := []byte{0x01, 0x03, 0x0e, 0x10, 0x13, 0x20, 0x21, 0x2f, 0x33, 0x34, 0x35, 0x3e, 0x3f, 0x79, 0x7a}
	if got := a.Components(0x40000001); string(got) != string(want) {
		t.Fatalf("components %x, want %x", got, want)
	}
	var created []int
	for _, e := range a.TakeEvents() {
		if e.Kind == "on_created" && e.Sys != 0 { // system 0 is Prometheus's own
			created = append(created, e.Sys)
		}
	}
	if want := []int{22, 43, 49, 13, 29}; len(created) != len(want) || created[0] != 22 || created[4] != 29 {
		t.Fatalf("on_created systems %v, want %v", created, want)
	}
	a.Tick(0.016)
	if a.TickCount() != 1 || a.EntityCount() != 1 {
		t.Fatalf("tick %d entities %d", a.TickCount(), a.EntityCount())
	}
	if a.Spawn(0x40000001, loader, 0) != 0 {
		t.Fatal("duplicate explicit id must fail")
	}
}

// Render-frame ticks drive 16 ms command frames through the game EA's 7 listeners.
func TestCommandFrames(t *testing.T) {
	a, err := NewAdmin("game")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if n := a.CommandFrameListenerCount(); n != 7 {
		t.Fatalf("listeners %d, want 7", n)
	}
	a.ClockResync(1000, 0)
	a.TakeEvents()
	a.Tick(0.050) // 50 ms: 3 CFs
	if cf := a.CommandFrame(); cf != 1003 {
		t.Fatalf("cf %d, want 1003", cf)
	}
	calls := 0
	for _, e := range a.TakeEvents() {
		if e.Kind == "cf_listener" {
			calls++
		}
	}
	if calls != 3*7 {
		t.Fatalf("listener calls %d, want 21", calls)
	}
}
