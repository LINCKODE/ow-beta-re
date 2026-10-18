package lobbylogic

import (
	"encoding/hex"
	"testing"
)

// The presence records that showed Tracer online as "Tracer" (sent live from Lua).
func TestFriendPresenceEncoding(t *testing.T) {
	f := &friend{name: "Tracer#1111", account: idOf(6), gameAccount: idOf(7)}
	got := hex.EncodeToString(friendPresence(f))
	want := "02000000" +
		"01000000" + "08000000" + "08ce840110011804" + "0d000000" + "2a0b547261636572233131313100000000" +
		"06000000000000000600000000000000" + "00000000000000000000000000000000" +
		"01000000" + "08000000" + "08ce840110021801" + "02000000" + "1001" + "00000000" +
		"07000000000000000700000000000000" + "06000000000000000600000000000000"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// The friend request that showed "Tracer#1111 - FRIEND REQUEST PENDING".
func TestFriendsStateEncoding(t *testing.T) {
	want := "0000000001000000547261636572233131313100547261636572233131313100000100000000000000000000000600000000000000060000000000000006000000000000000600000000000000" + "0000000000000000"
	f := &friend{name: "Tracer#1111", account: idOf(6)}
	if got := hex.EncodeToString(friendsState([]*friend{f})); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// partyOfOne and the general party encoder agree.
func TestPartyMemberMatchesPartyOfOne(t *testing.T) {
	id := idOf(9)
	one := partyOfOne(soloPartyID, id[:], "Player#1234", 0x0250000000000306)
	gen := append(append(append([]byte{}, soloPartyID[:]...), 1, 0, 0, 0), partyMember("Player#1234", true, 0x0250000000000306, id[:])...)
	if hex.EncodeToString(one) != hex.EncodeToString(gen) {
		t.Fatalf("%x\n%x", one, gen)
	}
}
