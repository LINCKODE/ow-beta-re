package lobbylogic

import "encoding/binary"

// Chat (ClientInLobbyChat 20400-20407 / ClientOutLobbyChat 21700-21706).
//
// 20400 = [channel {i32 type, u64, u64}] [sender {cstring name, u64, u64}] [cstring text]
// (handler FUN_7ff650c48990 -> FUN_7ff650c5b700). A sender with an empty name or a
// zero ID shows as a system message; a sender ID equal to our own (20500) is dropped
// as an echo. The client strips "#1234" from the name. Channel types 0-4 render in
// different colours (confirmed; see the chan* constants). The client shows the
// player's own lines itself, so the server only relays to other clients.

// chatMessage encodes ClientInLobbyChat 20400.
func chatMessage(channelType uint32, sender string, senderID uint64, text string) []byte {
	b := binary.LittleEndian.AppendUint32(nil, channelType)
	b = append(b, make([]byte, 16)...) // channel ID
	b = append(b, sender...)
	b = append(b, 0)
	b = binary.LittleEndian.AppendUint64(b, senderID)
	b = binary.LittleEndian.AppendUint64(b, senderID)
	b = append(b, text...)
	return append(b, 0)
}

// Chat channel types, from the client's chat channel definitions asset (colours
// confirmed in game). 11 and 12 (purple) are unknown.
const (
	chanError        = 1
	chanSystem       = 2
	chanWhisper      = 3
	chanParty        = 4
	chanTeam         = 5
	chanMatch        = 6
	chanGuild        = 7
	chanGuildOfficer = 8
	chanGeneral      = 9
)

// SystemChat shows text as a (yellow) system message in the client's chat.
func (c *Ctx) SystemChat(text string) error {
	return c.Send("ClientInLobbyChat", 20400, chatMessage(chanSystem, "", 0, text))
}

// chatMessageFrom encodes 20400 with full 16-byte channel and sender IDs.
func chatMessageFrom(channelType uint32, channel [16]byte, sender string, senderID []byte, text string) []byte {
	b := binary.LittleEndian.AppendUint32(nil, channelType)
	b = append(b, channel[:]...)
	b = append(b, sender...)
	b = append(b, 0)
	b = append(b, senderID...)
	b = append(b, text...)
	return append(b, 0)
}

// Joined chat channels (ClientInLobbyChat 20403 = [n] {u8 flags, i32 type, ID}).
// The flags are the channel's disabled bits (20404 sets bits 0-1, 20405 bit 2); a
// current channel with bit 0 or 2 set is switched away from. Joined channels are
// what TAB cycles through, together with whisper targets. 20406 {type, ID} leaves.
var (
	generalChannelID = idOf(9)
	partyChannelID   = soloPartyID
)

func joinedChannels(channels ...struct {
	typ uint32
	id  [16]byte
}) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(len(channels)))
	for _, ch := range channels {
		b = append(b, 0)
		b = binary.LittleEndian.AppendUint32(b, ch.typ)
		b = append(b, ch.id[:]...)
	}
	return b
}

// joinDefaultChannels joins General and Party.
func joinDefaultChannels(c *Ctx) error {
	return c.Send("ClientInLobbyChat", 20403, joinedChannels(
		struct {
			typ uint32
			id  [16]byte
		}{chanGeneral, generalChannelID},
		struct {
			typ uint32
			id  [16]byte
		}{chanParty, partyChannelID},
	))
}
