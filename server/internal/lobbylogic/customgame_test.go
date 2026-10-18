package lobbylogic

import (
	"encoding/hex"
	"testing"
)

// The 23303 the client accepted (sent live from Lua, type 0, null game): 141 bytes.
func TestCustomGameJoinedEncoding(t *testing.T) {
	var empty cgSettings
	msg := append(customGameInfo("Server Game", 0, [2]uint64{0, 7}, []byte{7, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}), empty.encode()...)
	if len(msg) != 141 {
		t.Fatalf("len %d, want 141", len(msg))
	}
	if got := hex.EncodeToString(msg[:14]); got != "5365727665722047616d65000000" {
		t.Fatalf("prefix %s", got)
	}
}

// Settings survive encode -> decode (24008 is decoded with the 23303 layout).
func TestSettingsRoundTrip(t *testing.T) {
	s := defaultSettings(customGamePractice)
	s.bools = 1
	s.i18[1] = 2
	s.bots = []cgBot{{2, 1, 0x0500000000000079}, {2, 1, 0x0500000000000065}}
	s.ids = [][2]uint64{{1, 2}}
	r := &reader{b: s.encode()}
	got := decodeSettings(r)
	if r.err != nil || len(r.b) != 0 {
		t.Fatalf("err %v, %d bytes left", r.err, len(r.b))
	}
	if hex.EncodeToString(got.encode()) != hex.EncodeToString(s.encode()) {
		t.Fatalf("round trip differs:\n%x\n%x", got.encode(), s.encode())
	}
	if got.maps[0] != DefaultMap || got.i78[0] != 1 || len(got.bots) != 2 {
		t.Fatalf("fields: %+v", got)
	}
	short := &reader{b: s.encode()[:40]}
	if decodeSettings(short); short.err == nil {
		t.Fatal("short settings decoded without error")
	}
}

// 23304: the game ID sits after 1+1+20+8 bytes (cg +0x1A8, read back by Start).
func TestLobbyStateEncoding(t *testing.T) {
	b := lobbyState([2]uint64{7, 9}, "Tracr#1234", make([]byte, 16))
	if got := hex.EncodeToString(b[30:46]); got != "07000000000000000900000000000000" {
		t.Fatalf("game id %s", got)
	}
	want := 30 + 16 + 12 + 4 + 4 + len("Tracr#1234") + 1 + 3 + 4 + 8 + 16
	if len(b) != want {
		t.Fatalf("len %d, want %d", len(b), want)
	}
}

// A system chat line as sent live (confirmed displayed).
func TestSystemChatEncoding(t *testing.T) {
	want := "00000000" + "00000000000000000000000000000000" + "00" + "00000000000000000000000000000000" + hex.EncodeToString([]byte("hi")) + "00"
	if got := hex.EncodeToString(chatMessage(0, "", 0, "hi")); got != want {
		t.Fatalf("got %s", got)
	}
}
