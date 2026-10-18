package lobbylogic

import "owserver/inst/jam"

// DefaultHandlers maps client (ClientOut*) message IDs to their handlers.
func DefaultHandlers() map[uint16]Handler {
	return map[uint16]Handler{
		21800: onLobbyConnect,
		21801: onLeaveGame,
		24200: onPlayerProgression,
		22000: onMatchmakingStart,
		22001: onMatchmakingCancel,
		24001: onCustomGameRequest,
		24007: onCustomGameCancel,
		24008: onCustomGameSettings,
		24013: onCustomGameStart,
		27000: onAddFriend,
		27001: onFriendRequestAnswer,
		22101: onPartyInvite,
		22102: onPartyKick,
		22103: onPartyPromote,
		22104: onPartyLeave,
		27004: onWhisper,
		21700: onPlayerChat,
		21701: onChannelList,
		27002: onRemoveFriend,
		27005: onFriendFlags,
		32001: onPrometheusResult, // PrometheusLobby (custom.go)
	}
}

// onLobbyConnect answers ClientOutLobbyConnect 21800 with ClientInLobbyConnect
// 20500, 20501 and 20503, which takes the client to the title screen.
//
// 20500 = {u64, u64} + cstring. Its handler (FUN_7ff650c5d0e0) stores the 16 bytes
// at +0x60/+0x68 and the string at +0x70 of the lobby connection object, sets flag
// 0x10000 and notifies. EXPERIMENT: the 16 bytes are assumed to be the player's
// JamID (we send the client JamID from the handshake); the string (profile +0x70) is
// the player's BattleTag since T025, its reader is unknown (see identity.go).
func onLobbyConnect(c *Ctx) error {
	if err := c.SendMsg(connectAccepted(c.ClientJamID(), playerName())); err != nil {
		return err
	}
	// 20501 (no fields) sets +0x23c = 1 on the lobby connection object
	// (FUN_7ff650c495a0 case 1). Confirmed: it takes the client past "Authenticating"
	// (to a black screen, waiting for data).
	if err := c.SendMsg(&jam.ConnectReady{}); err != nil {
		return err
	}
	// 20503 = TACT key delivery (handler FUN_7ff650c5f090): u64[] key names + u8[] keys,
	// then the lobby data package loads. Prometheus's "DP Load" button calls this handler
	// with an empty message; we send the same: two empty dynamic arrays.
	if err := c.SendMsg(&jam.TactKeys{}); err != nil {
		return err
	}
	// Player progression up front (the client only asks with 24200 later, after the main
	// menu has already checked the tutorial flag).
	if err := onPlayerProgression(c); err != nil {
		return err
	}
	// A party with just this player enables the title screen buttons (see party.go) while a
	// game server is linked; otherwise the empty party keeps them off (availability.go).
	if err := c.sendParty(); err != nil {
		return err
	}
	if err := sendSocialOnConnect(c); err != nil {
		return err
	}
	if err := sendHeroProgression(c, 1); err != nil {
		return err
	}
	// Every hero owned: the hero page opens hero details instead of the Unlock sheet (store.go).
	if err := sendOwnedProducts(c, heroes); err != nil {
		return err
	}
	return joinDefaultChannels(c)
}

// onPlayerProgression answers ClientOutLobbyPlayerProgression 24200 (empty).
//
// ClientInLobbyPlayerProgression 24300 = struct A {bool, u64, Entry[]}, struct B {Entry[]}.
// A.bool -> profile +0x148 = "tutorial done": while it is 0 the main menu (graph
// 0x0580000000000134, ConfigVar 0x7e8dcef9) opens the "PLAY THE TUTORIAL?" dialog every
// time it appears. A.u64 -> profile +0x150 (equipped player icon), A.list -> owned
// account unlocks (identity.go). See docs/LobbyScripts.md.
func onPlayerProgression(c *Ctx) error {
	return sendPlayerProgression(c)
}
