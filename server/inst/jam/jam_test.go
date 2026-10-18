package jam

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"owserver/inst/bits"
)

// fill sets v (a pointer's element) to pseudo-random values; strings avoid NUL.
func fill(rng *rand.Rand, v reflect.Value, depth int) {
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(rng.Intn(2) == 1)
	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(rng.Uint64())
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(rng.Uint64()))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(float64(float32(rng.NormFloat64() * 1000)))
	case reflect.String:
		b := make([]byte, rng.Intn(12))
		for i := range b {
			b[i] = byte('a' + rng.Intn(26))
		}
		v.SetString(string(b))
	case reflect.Slice:
		n := rng.Intn(4)
		if depth > 3 {
			n = 0
		}
		s := reflect.MakeSlice(v.Type(), n, n)
		for i := 0; i < n; i++ {
			fill(rng, s.Index(i), depth+1)
		}
		v.Set(s)
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			fill(rng, v.Index(i), depth+1)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			fill(rng, v.Field(i), depth+1)
		}
	}
}

func allIDs() []uint16 {
	var ids []uint16
	for _, p := range protocols {
		for id := p.Base; id < p.Max; id++ {
			ids = append(ids, id)
		}
	}
	return ids
}

// Every catalogued message survives encode -> decode, alone (lobby payload) and
// packed several to a bit stream (instance layout, trailing bools as bits).
func TestRoundTripAll(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, id := range allIDs() {
		for trial := 0; trial < 5; trial++ {
			m, ok := New(id)
			if !ok {
				t.Fatalf("New(%d) failed", id)
			}
			if m.ID() != id {
				t.Fatalf("%T.ID() = %d, want %d", m, m.ID(), id)
			}
			fill(rng, reflect.ValueOf(m).Elem(), 0)
			b := Marshal(m)
			got, err := Unmarshal(id, b)
			if err != nil {
				t.Fatalf("%s: %v (payload %x)", Name(id), err, b)
			}
			if !reflect.DeepEqual(normalize(got), normalize(m)) {
				t.Fatalf("%s: round trip\n got %+v\nwant %+v", Name(id), got, m)
			}
		}
	}
}

// normalize maps nil and empty slices to the same value for DeepEqual.
func normalize(m Message) any {
	v := reflect.New(reflect.TypeOf(m).Elem()).Elem()
	v.Set(reflect.ValueOf(m).Elem())
	normalizeValue(v)
	return v.Interface()
}

func normalizeValue(v reflect.Value) {
	switch v.Kind() {
	case reflect.Slice:
		if v.Len() == 0 {
			v.Set(reflect.Zero(v.Type()))
			return
		}
		for i := 0; i < v.Len(); i++ {
			normalizeValue(v.Index(i))
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			normalizeValue(v.Index(i))
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			normalizeValue(v.Field(i))
		}
	}
}

func TestStreamRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	var ids []uint16
	for _, id := range allIDs() {
		if _, _, ok := InstanceChannelOf(id); ok {
			ids = append(ids, id)
		}
	}
	for trial := 0; trial < 200; trial++ {
		var env []Envelope
		for i := 0; i < 1+rng.Intn(8); i++ {
			m, _ := New(ids[rng.Intn(len(ids))])
			fill(rng, reflect.ValueOf(m).Elem(), 0)
			e := Envelope{Reliable: rng.Intn(2) == 0, Msg: m}
			if e.Reliable {
				e.Seq = rng.Uint32()
			}
			env = append(env, e)
		}
		var w bits.Writer
		w.Bits(5, 3) // the stream doesn't start byte-aligned in a real payload
		if err := WriteStream(&w, env); err != nil {
			t.Fatal(err)
		}
		w.Bits(0x2a, 7) // the next stream's bits follow the last message
		end := w.Len()
		r := bits.NewReader(w.Buf())
		r.Bits(3)
		got, err := ReadStream(r)
		if err != nil {
			t.Fatal(err)
		}
		if r.Bits(7) != 0x2a || r.Pos() != end {
			t.Fatalf("trial %d: reader not left after the stream (pos %d, end %d)", trial, r.Pos(), end)
		}
		// Reliable first on the wire.
		var want []Envelope
		for _, e := range env {
			if e.Reliable {
				want = append(want, e)
			}
		}
		for _, e := range env {
			if !e.Reliable {
				want = append(want, e)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("got %d messages, want %d", len(got), len(want))
		}
		for i := range got {
			if got[i].Reliable != want[i].Reliable || got[i].Seq != want[i].Seq ||
				!reflect.DeepEqual(normalize(got[i].Msg), normalize(want[i].Msg)) {
				t.Fatalf("trial %d msg %d: got %+v want %+v", trial, i, got[i], want[i])
			}
		}
	}
}

// Bools pack across fields and nested structs, other fields align (the client's
// decoder FUN_7ff650f0c7d0): ClientInLobbyChat 20402 = {{bool,bool},{bool},{i32,ID}}.
func TestBoolPacking(t *testing.T) {
	var w bits.Writer
	w.Bool(true)
	w.Bool(false)
	w.Bool(true)
	putU32(&w, 7)
	w.Bool(true)
	w.Align()
	want := []byte{0b101, 7, 0, 0, 0, 1}
	if !reflect.DeepEqual(w.Buf(), want) {
		t.Fatalf("got %x want %x", w.Buf(), want)
	}
}

func TestLobbyChannels(t *testing.T) {
	ch := LobbyChannels()
	// docs/JAM.md §12.5: 32 lobby protocols, channel 10 = ClientInLobbyConnect,
	// 29 = ClientOutLobbyConnect.
	if len(ch) != 33 || ch[0].Name != "Negotiation" || ch[10].Name != "ClientInLobbyConnect" || ch[29].Name != "ClientOutLobbyConnect" {
		t.Fatalf("channels: %d, 10=%s 29=%s", len(ch), ch[10].Name, ch[29].Name)
	}
	l := DefaultLobbyLink()
	c, idx, ok := l.ChannelOf(IDConnectAccepted)
	if !ok || c != 10 || idx != 0 {
		t.Fatalf("ConnectAccepted on %d/%d", c, idx)
	}
	f, err := l.Frame(&ConnectAccepted{Player: JamID{1, 2}, BattleTag: "A"})
	if err != nil {
		t.Fatal(err)
	}
	c, idx, p, rest, err := SplitLobbyFrame(f)
	if err != nil || len(rest) != 0 || c != 10 || idx != 0 || len(p) != 18 {
		t.Fatalf("split: %v %d %d %x %x", err, c, idx, p, rest)
	}
}

func TestInstanceChannels(t *testing.T) {
	// server/internal/instance/stream.go Channels.
	for _, c := range []struct {
		id      uint16
		ch, idx uint8
	}{{20301, 0, 1}, {21603, 1, 3}, {25000, 2, 0}, {27501, 3, 1}, {25808, 4, 8}, {30739, 5, 39}, {22308, 6, 8}, {25705, 7, 5}} {
		ch, idx, ok := InstanceChannelOf(c.id)
		if !ok || ch != c.ch || idx != c.idx {
			t.Errorf("%d: %d/%d %v, want %d/%d", c.id, ch, idx, ok, c.ch, c.idx)
		}
		if id, ok := InstanceMessageID(c.ch, c.idx); !ok || id != c.id {
			t.Errorf("%d/%d -> %d", c.ch, c.idx, id)
		}
	}
	if _, _, ok := InstanceChannelOf(20500); ok {
		t.Error("lobby message on the instance stream")
	}
}

func TestDecodeErrors(t *testing.T) {
	// A string without NUL, a huge array count, a short payload.
	if _, err := Unmarshal(IDConnectAccepted, append(make([]byte, 16), 'x')); err == nil {
		t.Error("string without NUL accepted")
	}
	if _, err := Unmarshal(IDNegotiateProtocols, []byte{0xff, 0xff, 0xff, 0x7f}); !errors.Is(err, ErrBadCount) {
		t.Errorf("huge count: %v", err)
	}
	if _, err := Unmarshal(IDHeroLockIn, []byte{1, 2, 3}); !errors.Is(err, bits.ErrOverrun) {
		t.Errorf("short: %v", err)
	}
	if _, err := Unmarshal(IDMapLoaded, []byte{1}); !errors.Is(err, ErrTrailing) {
		t.Errorf("trailing: %v", err)
	}
}

type record struct {
	dir     string
	id      uint16
	payload []byte
}

func readTraffic(t *testing.T, path string) []record {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []record
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fs := strings.Fields(line)
		id, _ := strconv.Atoi(fs[1])
		var p []byte
		if len(fs) > 2 {
			if p, err = hex.DecodeString(fs[2]); err != nil {
				t.Fatal(err)
			}
		}
		out = append(out, record{fs[0], uint16(id), p})
	}
	return out
}

// Every lobby payload the R&D servers sent or received decodes with no bytes
// left over, and re-encodes to the same bytes.
func TestReplayLobbyTraffic(t *testing.T) {
	recs := readTraffic(t, "testdata/lobby_traffic.txt")
	if len(recs) < 100 {
		t.Fatalf("only %d lobby records", len(recs))
	}
	for _, rec := range recs {
		m, err := Unmarshal(rec.id, rec.payload)
		if err != nil {
			t.Errorf("%s %d: %v", rec.dir, rec.id, err)
			continue
		}
		if _, raw := m.(*Raw); raw {
			t.Errorf("%d: not catalogued", rec.id)
			continue
		}
		if b := Marshal(m); !reflect.DeepEqual(b, rec.payload) {
			t.Errorf("%s %d: re-encoded\n %x\nwant %x", rec.dir, rec.id, b, rec.payload)
		}
	}
}

// Every instance stream message the client sent decodes and ends inside the
// logged bytes (the R&D server logged a trailing bool byte whole: its other
// bits belong to the next stream).
func TestReplayInstanceTraffic(t *testing.T) {
	recs := readTraffic(t, "testdata/instance_traffic.txt")
	if len(recs) < 40 {
		t.Fatalf("only %d instance records", len(recs))
	}
	for _, rec := range recs {
		m, ok := New(rec.id)
		if !ok {
			t.Fatalf("%d not catalogued", rec.id)
		}
		r := bits.NewReader(rec.payload)
		if err := m.Decode(r); err != nil {
			t.Errorf("%d: %v", rec.id, err)
			continue
		}
		if left := r.Left(); left < 0 || left >= 8 {
			t.Errorf("%s (%d): %d bits left of %x", Name(rec.id), rec.id, left, rec.payload)
		}
	}
}

func ExampleUnmarshal() {
	b, _ := hex.DecodeString("b1df482200000082e6e7f9e5a30507745472616372233132333400")
	m, err := Unmarshal(IDConnectAccepted, b)
	fmt.Printf("%+v %v\n", m, err)
	// Output: &{Player:{Seq:9367487225505832881 Src:8360657434732783590} BattleTag:Tracr#1234} <nil>
}
