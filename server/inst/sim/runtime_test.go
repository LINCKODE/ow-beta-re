package sim

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"owserver/inst/bits"
	"owserver/inst/wire"
	"owserver/internal/heroes"

	"owcontent/content"
)

// The tests read the hero graphs from the local 0.8 install (skipped without it).
const install = "../../../game/owbetaclient"

var (
	storeOnce sync.Once
	store     *content.Store
	storeErr  error
)

func engine(t *testing.T) *Engine {
	t.Helper()
	if _, err := os.Stat(install); err != nil {
		t.Skip("no 0.8 install:", err)
	}
	storeOnce.Do(func() { store, storeErr = content.Open(install, nil) })
	if storeErr != nil {
		t.Skip("content:", storeErr)
	}
	e := NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)), store)
	t.Cleanup(e.Close)
	return e
}

const genjiDef, body = 0x225, 0x40000030

// readPacket checks a StatescriptPacket's header and returns its CF range.
func readPacket(t *testing.T, w *bits.Writer) (start, end uint32) {
	t.Helper()
	r := bits.NewReader(w.Buf())
	prefix := func(w0, w1, w2 uint) uint32 {
		if !r.Bool() {
			return r.Bits(w0)
		}
		if !r.Bool() {
			return r.Bits(w1)
		}
		return r.Bits(w2)
	}
	if !r.Bool() {
		t.Fatal("not present")
	}
	r.Bool()
	start = prefix(16, 24, 32)
	end = start + prefix(8, 16, 32)
	if r.Bool() {
		t.Fatal("optional value set")
	}
	n := prefix(8, 16, 32)
	if int(n) != r.Left()-int(uint(len(w.Buf())*8)-w.Len()) {
		t.Fatalf("payload length %d, %d bits follow", n, int(w.Len())-int(r.Pos()))
	}
	return start, end
}

// Genji's start-up imports into the runtime; with no input nothing changes, and the
// deltas chain from the seed frame.
func TestGenjiSeedAndDelta(t *testing.T) {
	e := engine(t)
	r := e.NewRuntime()
	defer r.Close()
	st, endCF, ok := r.SpawnHero(body, genjiDef, 1000)
	if !ok || endCF != 1000 {
		t.Fatalf("SpawnHero: ok %v endCF %d", ok, endCF)
	}
	h := r.heroes[body]
	if h == nil {
		t.Fatal("hero not simulated")
	}
	if got := len(h.sm.Instances()); got != len(st.Instances) {
		t.Errorf("%d instances, the start-up has %d", got, len(st.Instances))
	}
	// Every start state the runtime could import is active; the seed snapshot has them.
	active := 0
	for _, in := range h.ghost.LastState().Instances {
		active += len(in.States)
	}
	want := 0
	for _, in := range st.Instances {
		want += len(in.Active)
	}
	t.Logf("start states: %d active in the seed (start-up: %d); unencodable %v", active, want, h.Unencodable)
	if d, _ := r.Delta(body); r.Pending(body) || d != nil {
		t.Error("delta before any frame was simulated")
	}
	// Input for 1001..1003 arrives (no buttons); the server simulates them.
	r.Input(body, []wire.InputEntry{{Frame: 1001}, {Frame: 1002}, {Frame: 1003}})
	r.Tick(10)
	if h.frame != 1003 || !r.Pending(body) {
		t.Fatalf("frame %d pending %v", h.frame, r.Pending(body))
	}
	d, end := r.Delta(body)
	if d == nil || end != 1003 {
		t.Fatal("no delta")
	}
	if s, en := readPacket(t, d); s != 1000 || en != 1003 {
		t.Errorf("delta range %d..%d, want 1000..1003", s, en)
	}
	// Old frames are ignored. Unacknowledged, the next delta is a union from the acked base; once acked, the
	// next one starts at the acked end.
	r.Input(body, []wire.InputEntry{{Frame: 1002}, {Frame: 1004}})
	r.Tick(11)
	d, _ = r.Delta(body)
	if s, en := readPacket(t, d); s != 1000 || en != 1004 {
		t.Errorf("union %d..%d, want 1000..1004", s, en)
	}
	r.Acked(body, 1004)
	r.Input(body, []wire.InputEntry{{Frame: 1005}})
	r.Tick(12)
	d, _ = r.Delta(body)
	if s, en := readPacket(t, d); s != 1004 || en != 1005 {
		t.Errorf("after the ack %d..%d, want 1004..1005", s, en)
	}
	// A loss with nothing new: the union goes out again.
	r.Lost(body)
	if !r.Pending(body) {
		t.Error("nothing pending after a loss")
	}
	stats := r.Stats().([]HeroStats)
	if len(stats) != 1 || stats[0].Simulated != 5 || stats[0].Deltas != 3 {
		t.Errorf("stats %+v", stats)
	}
	t.Logf("stats %+v", stats[0])
}

// A frame missing from the input keeps the previous frame's buttons; a client far
// ahead is caught up at most maxCatchUp frames per tick.
func TestCatchUp(t *testing.T) {
	e := engine(t)
	r := e.NewRuntime()
	r.Unsent = true // every delta counts as acknowledged
	defer r.Close()
	if _, _, ok := r.SpawnHero(body, genjiDef, 500); !ok {
		t.Fatal("SpawnHero")
	}
	r.Input(body, []wire.InputEntry{{Frame: 501, Buttons: 1}, {Frame: 700}})
	r.Tick(1)
	h := r.heroes[body]
	if h.frame != 700 || h.Skipped != 200-maxCatchUp {
		t.Errorf("frame %d skipped %d", h.frame, h.Skipped)
	}
	if h.last.Buttons != 0 {
		t.Errorf("last input %+v", h.last)
	}
}

func TestStaticHost(t *testing.T) {
	var h Host = Static{}
	st, cf, ok := h.SpawnHero(body, genjiDef, 5)
	if !ok || cf != 0 || len(st.Instances) == 0 {
		t.Errorf("Static.SpawnHero = %v %d %v", st != nil, cf, ok)
	}
	if _, ok := heroes.ByDef(genjiDef); !ok {
		t.Error("Genji not in the catalogue")
	}
}

// The StackPriority records bf listed (T041): ...01 = 0, ...02 = -10, ...03 = 10, ...3E = -5 wins ties.
func TestStackPriorities(t *testing.T) {
	engine(t)
	want := map[uint64]struct {
		p float32
		t bool
	}{0x0C40000000000001: {0, false}, 0x0C40000000000002: {-10, false}, 0x0C40000000000003: {10, false}, 0x0C4000000000003E: {-5, true}}
	for g, w := range want {
		b, err := store.Read(g)
		if err != nil {
			t.Fatal(err)
		}
		p, ties, err := readStackPriority(b)
		if err != nil || p != w.p || ties != w.t {
			t.Errorf("%#x: %v %v %v, want %v %v", g, p, ties, err, w.p, w.t)
		}
	}
	var all []uint64
	for _, r := range store.Resources() {
		if r.GUID>>52 == 0x0C4 {
			all = append(all, r.GUID)
		}
	}
	e := engine(t)
	if n, err := e.LoadStackPriorities(all); err != nil || n != len(all) || n == 0 {
		t.Errorf("LoadStackPriorities: %d of %d, %v", n, len(all), err)
	}
}

// A primary-fire tap through the host: the server's instances fire like the client's
// prediction (T041: volley ends P+13, ammo $35 24 -> 21), and the deltas carry it.
func TestGenjiFire(t *testing.T) {
	e := engine(t)
	var prios []uint64
	for _, r := range store.Resources() {
		if r.GUID>>52 == 0x0C4 {
			prios = append(prios, r.GUID)
		}
	}
	if _, err := e.LoadStackPriorities(prios); err != nil {
		t.Fatal(err)
	}
	r := e.NewRuntime()
	r.Unsent = true // every delta counts as acknowledged
	defer r.Close()
	const seed, press = 2000, 2010
	if _, cf, ok := r.SpawnHero(body, genjiDef, seed); !ok || cf != seed {
		t.Fatal("SpawnHero")
	}
	h := r.heroes[body]
	ammo := func() int64 {
		v, err := h.sm.GetVar(9, 0x0D80000000000035)
		if err != nil {
			t.Fatal(err)
		}
		return int64(int32(v.Bits))
	}
	if a := ammo(); a != 24 {
		t.Fatalf("ammo at the start %d", a)
	}
	var deltas int
	var sawAmmo bool
	for f := uint32(seed + 1); f <= press+40; f++ {
		var b uint16
		if f >= press && f < press+6 { // held 6 frames (100 ms)
			b = 1
		}
		r.Input(body, []wire.InputEntry{{Frame: f, Buttons: b}})
		r.Tick(f)
		if r.Pending(body) {
			if d, _ := r.Delta(body); d != nil {
				deltas++
				if f == press {
					t.Logf("frame %d: sm clock %d ms (f*16 = %d)", f, h.sm.Time(), int64(f)*16)
				}
				if in := findInstance(h.ghost.LastState(), 9); in != nil && f >= press && f <= press+14 {
					t.Logf("frame %d: $35 %d writer %+v", f, int32(in.Vars[0x35].Bits), in.Users[0x35])
				}
				if n := len(h.ghost.LastState().Actions); n > 0 {
					t.Logf("frame %d: %d SYNC_ALL actions %v", f, n, h.ghost.LastState().Actions)
				}
			}
			if in := findInstance(h.ghost.LastState(), 9); in != nil && int32(in.Vars[0x35].Bits) < 24 {
				sawAmmo = true
			}
		}
	}
	if h.InputErrs != 0 {
		t.Errorf("input errors: %v", h.inputErr)
	}
	acts, _ := h.sm.Actions(0)
	sync := 0
	for _, a := range acts {
		if a.Remote >= 0 {
			sync++
		}
	}
	t.Logf("actions executed: %d (SYNC_ALL %d)", len(acts), sync)
	if a := ammo(); a >= 24 {
		t.Errorf("ammo after the tap %d, want < 24", a)
	}
	if !sawAmmo {
		t.Error("no delta carried the ammo change")
	}
	t.Logf("ammo %d, %d deltas, unencodable %v", ammo(), deltas, h.Unencodable)
}

// Shift (dash), then again after the 8 s cooldown: the second press must start the
// ability again (the adopted cooldown var reads through the Ability and is released
// at 0; T051 [L]: it used to read 8 forever).
func TestGenjiDashTwice(t *testing.T) {
	e := engine(t)
	var prios []uint64
	for _, r := range store.Resources() {
		if r.GUID>>52 == 0x0C4 {
			prios = append(prios, r.GUID)
		}
	}
	if _, err := e.LoadStackPriorities(prios); err != nil {
		t.Fatal(err)
	}
	r := e.NewRuntime()
	r.Unsent = true // every delta counts as acknowledged
	defer r.Close()
	r.SpawnHero(body, genjiDef, 1000)
	h := r.heroes[body]
	h.sm.Trace(true)
	for f := uint32(1001); f <= 1750; f++ {
		var b uint16
		if (f >= 1010 && f < 1016) || (f >= 1700 && f < 1706) {
			b = 0x10 // ability_1
		}
		r.Input(body, []wire.InputEntry{{Frame: f, Buttons: b}})
		r.Tick(f)
		r.Delta(body)
	}
	begins := 0
	for _, l := range strings.Split(h.sm.DrainTrace(), "\n") {
		if strings.Contains(l, `"kind":"begin"`) && strings.Contains(l, `"inst":6,`) && strings.Contains(l, `"a":29,`) {
			begins++
		}
	}
	if begins != 2 {
		t.Errorf("dash state 29 of instance 6 began %d times, want 2 (the second press after the cooldown)", begins)
	}
}

// Fire, then R (action code 13 = reload 0x50, table 0x173e680): the LogicalButton
// state of the weapon graph starts the reload and the ammo is refilled ~1.5 s later
// (T051 [L]: the codes were crossed and the state type missing, so R did nothing).
func TestGenjiReload(t *testing.T) {
	e := engine(t)
	var prios []uint64
	for _, r := range store.Resources() {
		if r.GUID>>52 == 0x0C4 {
			prios = append(prios, r.GUID)
		}
	}
	if _, err := e.LoadStackPriorities(prios); err != nil {
		t.Fatal(err)
	}
	r := e.NewRuntime()
	r.Unsent = true // every delta counts as acknowledged
	defer r.Close()
	r.SpawnHero(body, genjiDef, 1000)
	h := r.heroes[body]
	ammo := func() int64 { v, _ := h.sm.GetVar(9, 0x0D80000000000035); return int64(int32(v.Bits)) }
	for f := uint32(1001); f <= 1250; f++ {
		in := wire.InputEntry{Frame: f}
		if f >= 1010 && f < 1016 {
			in.Buttons = 1
		}
		if f == 1100 {
			in.Extra = 13
		}
		r.Input(body, []wire.InputEntry{in})
		r.Tick(f)
		r.Delta(body)
		if f == 1099 && ammo() != 21 {
			t.Fatalf("ammo before the reload %d, want 21", ammo())
		}
	}
	if a := ammo(); a != 24 {
		t.Errorf("ammo after the reload %d, want 24", a)
	}
}
