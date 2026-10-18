package lobbylogic

import (
	"encoding/hex"
	"testing"
	"time"
)

// The 20804 the client accepted: enabled, 1 h remaining, 0 played (profile +0x18 = 1,
// then remaining counted down and played up, measured live).
func TestPlayTimeEncoding(t *testing.T) {
	want := "0180ee3600000000000000000000000000"
	if got := hex.EncodeToString(playTime{enabled: true, remaining: time.Hour}.encode()); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if got := hex.EncodeToString(playTime{}.encode()); got != "0000000000000000000000000000000000" {
		t.Fatalf("off: %s", got)
	}
}
