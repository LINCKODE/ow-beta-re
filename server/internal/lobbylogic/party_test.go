package lobbylogic

import (
	"encoding/hex"
	"testing"
)

// The bytes that turned the title screen buttons on (sent live from Lua).
func TestPartyOfOneEncoding(t *testing.T) {
	id, _ := hex.DecodeString("ab92425600000026859e26b2f8608aa4")
	want := "0100000000000000020000000000000001000000506c61796572233132333400010500000000000000000000000000000000ab92425600000026859e26b2f8608aa4ab92425600000026859e26b2f8608aa4"
	if got := hex.EncodeToString(partyOfOne(soloPartyID, id, "Player#1234", 0)); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
