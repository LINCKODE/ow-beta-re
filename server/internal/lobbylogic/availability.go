package lobbylogic

import "fmt"

// Game server availability on the title screen (docs/LobbyServer.md "Game server availability").
//
// The four match buttons (PLAY, PRIVATE GAME, PRACTICE VS AI, TUTORIAL) are enabled by
// the party state 20700: a party with the local player as leader turns them on, no party
// turns them off, and the client re-evaluates them whenever a new 20700 arrives [U, T058
// stage 0]. So lobbylogic sends the party only while the active game server is linked
// (Instances.Available) and the empty party (null ID, no members) otherwise; the
// empty party also hides the player's name and icon at the top right [U].

// noParty encodes 20700 with no party: null party ID, no members.
func noParty() []byte {
	return make([]byte, 16+4)
}

// gameAvailable reports whether new matches have a game server, and whether that is
// known yet (a restarted lobbylogic waits for the servers to redial). Without a game
// server link at all the buttons stay on, as before.
func (l *Logic) gameAvailable() (available, known bool) {
	if l.Instances == nil {
		return true, true
	}
	return l.Instances.Available()
}

// AvailabilityChanged is Instances.OnAvailable: every connected client gets the party
// state that matches (buttons on / off) and a system chat line.
func (l *Logic) AvailabilityChanged(available bool, server string) {
	text := fmt.Sprintf("Game server %q is offline: PLAY, PRIVATE GAME, PRACTICE VS AI and TUTORIAL are disabled until it is back.", server)
	if available {
		text = fmt.Sprintf("Game server %q is online.", server)
	}
	for _, sess := range l.liveSessions() {
		c := l.ctxFor(sess)
		if err := c.sendParty(); err != nil {
			c.Log.Warn("availability: party state not sent", "err", err)
			continue
		}
		if err := c.SystemChat(text); err != nil {
			c.Log.Warn("availability: chat not sent", "err", err)
		}
	}
}
