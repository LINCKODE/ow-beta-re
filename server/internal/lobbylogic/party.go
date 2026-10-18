package lobbylogic

import "encoding/binary"

// Party (ClientInLobbyParty 20700-20707).
//
// 20700 is the party state: ID party, [n] member. Its handler (FUN_7ff650c49c20 ->
// FUN_7ff650cba220) stores the party ID on the party manager (FUN_7ff650c27500,
// +0x18/+0x20), diffs the member list (+0xB0, count +0xB8) and raises change flag
// 0x8000. The client finds itself by comparing each member's player ID with the ID
// from ClientInLobbyConnect 20500 (lobby connection +0x60/+0x68).
//
// Confirmed: a party containing just the local player (bool 1, status 5) turns on
// EnablePlayButton, EnableCustomButton, EnablePracticeButton and
// EnableTutorialButton on the title screen. Without a party every button is disabled.
//
// Member (80 bytes in memory):
//
//	cstring name    +0x00  BattleTag; the client strips "#1234" for display
//	bool            +0x10  with status 5-7: 1 = class 3 (probably leader), 0 = class 2
//	i8 status       +0x11  1-4 = class 1, 5-7 = class 2/3 (see above); others ignored
//	u64 icon        +0x20  player icon unlock (class 0x8CDAA871, 0x025...). The title
//	                       screen's name/portrait panel (FUN_7ff650d85700) looks it up
//	                       with FUN_7ff650d52060; without a valid icon it rebuilds the
//	                       panel every frame. 23 are loaded: 0x02500000000002F7, 2F9,
//	                       304-318.
//	u64             +0x28  unknown
//	ID player       +0x30  the member key
//	ID              +0x40  unknown (we send the player ID again)

const (
	partyMemberLeader = 1
	partyStatusMember = 5
)

// partyOfOne encodes ClientInLobbyParty 20700 with the local player as the only member.
func partyOfOne(partyID [16]byte, playerID []byte, name string, icon uint64) []byte {
	b := append([]byte{}, partyID[:]...)
	b = binary.LittleEndian.AppendUint32(b, 1)
	b = append(b, name...)
	b = append(b, 0)
	b = append(b, partyMemberLeader, partyStatusMember)
	b = binary.LittleEndian.AppendUint64(b, icon)
	b = binary.LittleEndian.AppendUint64(b, 0)
	b = append(b, playerID...)
	b = append(b, playerID...)
	return b
}

// soloPartyID is the party ID we give each player's one-member party.
var soloPartyID = [16]byte{1, 0, 0, 0, 0, 0, 0, 0, 2}
