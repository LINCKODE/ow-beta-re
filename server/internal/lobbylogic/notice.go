package lobbylogic

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

// Server notices: the three lobby messages that open the client's generic popup (UI
// 0x0AC00000000005D5, the one chat's "/list" also uses). All three handlers resolve a
// localized string (FUN_7ff650832ee0) and open the popup through the chat manager
// (FUN_7ff650c26e90 -> FUN_7ff650cc07e0 / cc0e10):
//
//	ClientInLobbyInstance   20601 {u64 string}  FUN_7ff650c461f0: shows nothing when the
//	                                            string resolves empty
//	ClientInLobbyPlayer     20803 {u8 index}    FUN_7ff650c5bdb0: string = notice table
//	                                            DAT_7ff6515a3fb0[index], 52 entries, NOT
//	                                            bounds-checked by the client
//	ClientInLobbyCustomGame 23307 {u64 string}  FUN_7ff650bef8b0: same popup, no empty check
//
// String IDs are STU string GUIDs (0x0DE...; systems/data/lobby_scripts/strings_enUS.tsv).
// The notice table is systems/data/lobby_scripts/notice_table.tsv.

// Notice is an index into the client's notice table (ClientInLobbyPlayer 20803).
type Notice uint8

// A few of the 52 notices (the full table is in notice_table.tsv).
const (
	NoticePlayerNotFound         Notice = 0
	NoticeUnableToJoinQueue      Notice = 14 // "Unable to join queue at this time"
	NoticeUnableToJoinPractice   Notice = 15 // "Unable to join practice mode"
	NoticeMatchmakingServerDown  Notice = 17 // "Matchmaking server is down"
	NoticePrivateGameServerDown  Notice = 19 // "Private game server is down."
	NoticeUnableToCreatePrivate  Notice = 35 // "Unable to create private game."
	NoticeCustomServerNotLoaded  Notice = 39 // "Custom server not loaded."
	NoticeUnableToAssignInstance Notice = 51 // "Unable to be assigned to instance, removing."
	noticeCount                         = 52
)

// noticeMessage encodes 20803. Indexes past the table would make the client read a
// string ID out of bounds, so they are refused.
func noticeMessage(n Notice) ([]byte, error) {
	if int(n) >= noticeCount {
		return nil, fmt.Errorf("notice %d out of range (0-%d)", n, noticeCount-1)
	}
	return []byte{byte(n)}, nil
}

// stringMessage encodes 20601 / 23307: one u64 string ID.
func stringMessage(stringID uint64) []byte {
	return binary.LittleEndian.AppendUint64(nil, stringID)
}

// Notice shows one of the client's built-in notices as a popup.
func (c *Ctx) Notice(n Notice) error {
	b, err := noticeMessage(n)
	if err != nil {
		return err
	}
	return c.Send("ClientInLobbyPlayer", 20803, b)
}

// Popup shows a localized string (0x0DE... GUID) as a popup (20601).
func (c *Ctx) Popup(stringID uint64) error {
	return c.Send("ClientInLobbyInstance", 20601, stringMessage(stringID))
}

// CustomGamePopup shows a localized string as a popup via the custom-game channel (23307).
func (c *Ctx) CustomGamePopup(stringID uint64) error {
	return c.Send("ClientInLobbyCustomGame", 23307, stringMessage(stringID))
}

// parseStringID reads a hex string ID; short forms (no type byte) get the string
// type 0x0DE: "1a14" -> 0x0DE0000000001A14.
func parseStringID(s string) (uint64, error) {
	v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(s), "0x"), 16, 64)
	if err != nil {
		return 0, fmt.Errorf("string id: %w", err)
	}
	if v>>48 == 0 {
		v |= 0x0DE0000000000000
	}
	return v, nil
}

func init() {
	commands["notice"] = command{"!notice <0-51> - popup from the client's notice table (20803)", func(c *Ctx, a []string) error {
		if len(a) < 1 {
			return fmt.Errorf("usage: !notice <0-%d>", noticeCount-1)
		}
		n, err := strconv.ParseUint(a[0], 10, 8)
		if err != nil {
			return fmt.Errorf("index: %w", err)
		}
		return c.Notice(Notice(n))
	}}
	commands["popup"] = command{"!popup <string id> [custom] - popup with a localized string (20601, or 23307 with 'custom')", func(c *Ctx, a []string) error {
		if len(a) < 1 {
			return fmt.Errorf("usage: !popup <string id, e.g. 0x0DE0000000000C99 or c99> [custom]")
		}
		id, err := parseStringID(a[0])
		if err != nil {
			return err
		}
		if len(a) > 1 && strings.EqualFold(a[1], "custom") {
			return c.CustomGamePopup(id)
		}
		return c.Popup(id)
	}}
}
