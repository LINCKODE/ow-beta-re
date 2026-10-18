package lobbylogic

import (
	"bytes"
	"encoding/binary"
	"path/filepath"
	"testing"

	"owserver/inst/jam"
)

func TestPlayerProgression(t *testing.T) {
	b := playerProgression(true, 0x0250000000000306, []uint64{0x02500000000002F7, 0x0250000000000306})
	// A: bool, u64 icon, count 2, 2 x {bool, i32, u64, u64}; B: count 0.
	if len(b) != 1+8+4+2*21+4 {
		t.Fatalf("len %d", len(b))
	}
	if b[0] != 1 || binary.LittleEndian.Uint64(b[1:]) != 0x0250000000000306 || binary.LittleEndian.Uint32(b[9:]) != 2 {
		t.Fatalf("header %x", b[:13])
	}
	if got := binary.LittleEndian.Uint64(b[13+5:]); got != 0x02500000000002F7 {
		t.Fatalf("first unlock %#x", got)
	}
	if got := binary.LittleEndian.Uint64(b[13+21+5:]); got != 0x0250000000000306 {
		t.Fatalf("second unlock %#x", got)
	}
	if binary.LittleEndian.Uint32(b[len(b)-4:]) != 0 {
		t.Fatal("B list not empty")
	}
	// Tutorial not done, no unlocks: the old 17-byte form.
	if b := playerProgression(false, 0, nil); !bytes.Equal(b, make([]byte, 17)) {
		t.Fatalf("empty form %x", b)
	}
}

func TestConnectAccepted(t *testing.T) {
	self := bytes.Repeat([]byte{0xAB}, 16)
	b := jam.Marshal(connectAccepted(self, "Tracr#1234"))
	if !bytes.Equal(b[:16], self) || string(b[16:]) != "Tracr#1234\x00" {
		t.Fatalf("%x", b)
	}
}

// The generated codecs send the connect sequence byte for byte as the hand-written
// encoders did (T049): 20501 empty, 20503 two empty arrays.
func TestConnectSequenceBytes(t *testing.T) {
	if b := jam.Marshal(&jam.ConnectReady{}); len(b) != 0 {
		t.Fatalf("20501: %x", b)
	}
	if b := jam.Marshal(&jam.TactKeys{}); !bytes.Equal(b, make([]byte, 8)) {
		t.Fatalf("20503: %x", b)
	}
}

func TestFindIcon(t *testing.T) {
	for in, want := range map[string]uint64{"tracer": 0x0250000000000306, "306": 0x0250000000000306, "0x02500000000002F7": 0x02500000000002F7, "Soldier: 76": 0x0250000000000312} {
		if got, err := findIcon(in); err != nil || got != want {
			t.Errorf("%s: %#x, %v", in, got, err)
		}
	}
	if _, err := findIcon("2f8"); err == nil {
		t.Error("spray 0x025...2F8 accepted as an icon")
	}
	if len(iconNames) != 23 {
		t.Errorf("%d icons", len(iconNames))
	}
}

func TestIdentityPersists(t *testing.T) {
	t.Setenv("LOGIC_IDENTITY", filepath.Join(t.TempDir(), "id.json"))
	identityMu.Lock()
	identityLoaded = false
	identityMu.Unlock()
	if got := currentIdentity(); got != defaultIdentity {
		t.Fatalf("default %+v", got)
	}
	want := Identity{Name: "Tracr#4321", Icon: 0x0250000000000318}
	if err := setIdentity(want); err != nil {
		t.Fatal(err)
	}
	identityMu.Lock()
	identityLoaded = false
	identityMu.Unlock()
	if got := currentIdentity(); got != want {
		t.Fatalf("reloaded %+v", got)
	}
	if withTag("Tracr") != "Tracr#1234" || withTag("A#1") != "A#1" {
		t.Error("withTag")
	}
	if validName("#1234") == nil || validName("Tracr#1") != nil {
		t.Error("validName")
	}
}
