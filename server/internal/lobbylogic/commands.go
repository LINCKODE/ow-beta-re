package lobbylogic

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"owserver/internal/maps"
)

// onPlayerChat handles ClientOutLobbyChat 21700 = {channel {i32 type, ID}, cstring text}.
// Lines starting with "!" are server commands; General is relayed to every other
// connected client; Party gets an answer from a bot party member. The client already
// shows the player's own line.
func onPlayerChat(c *Ctx) error {
	if len(c.Payload) < 21 {
		return fmt.Errorf("21700 too short: %x", c.Payload)
	}
	ctype := binary.LittleEndian.Uint32(c.Payload)
	var channel [16]byte
	copy(channel[:], c.Payload[4:20])
	raw, _, _ := bytes.Cut(c.Payload[20:], []byte{0})
	text := string(raw)
	c.Log.Info("chat", "channel", ctype, "text", text)

	if strings.HasPrefix(text, "!") {
		return runCommand(c, text[1:])
	}
	switch ctype {
	case chanGeneral:
		for _, s := range c.logic.liveSessions() {
			if s.ID == c.Session.ID {
				continue
			}
			sender, _ := hex.DecodeString(c.Session.ClientJamID)
			if err := c.logic.Send(s.ID, "ClientInLobbyChat", 20400, chatMessageFrom(ctype, channel, playerName(), sender, text)); err != nil {
				c.Log.Warn("relay failed", "to", s.ID, "err", err)
			}
		}
	case chanParty:
		c.State.mu.Lock()
		var bot *friend
		if len(c.State.party) > 0 {
			bot = c.State.party[0]
		}
		c.State.mu.Unlock()
		if bot != nil {
			return c.Send("ClientInLobbyChat", 20400, chatMessageFrom(chanParty, channel, bot.name, bot.account[:], "Roger that: "+text))
		}
	}
	return nil
}

type command struct {
	usage string
	run   func(c *Ctx, args []string) error
}

var commands map[string]command

func init() {
	commands = map[string]command{
		"help": {"!help - list commands", cmdHelp},
		"friend": {"!friend <name> - add an online friend", func(c *Ctx, a []string) error {
			if len(a) < 1 {
				return fmt.Errorf("usage: !friend <name>")
			}
			f := c.newNamedFriend(a[0])
			if err := c.addFriend(f); err != nil {
				return err
			}
			return c.SystemChat(f.name + " is now your friend.")
		}},
		"request": {"!request <name> - receive a friend request", func(c *Ctx, a []string) error {
			if len(a) < 1 {
				return fmt.Errorf("usage: !request <name>")
			}
			f := c.newNamedFriend(a[0])
			c.State.mu.Lock()
			c.State.requests[f.account] = f
			c.State.mu.Unlock()
			return c.Send("ClientInLobbyFriends", 27103, friendRequest(f))
		}},
		"invite": {"!invite <name> - that friend (created if needed) joins your party", func(c *Ctx, a []string) error {
			if len(a) < 1 {
				return fmt.Errorf("usage: !invite <name>")
			}
			f := c.friendByName(a[0])
			if f == nil {
				f = c.newNamedFriend(a[0])
				if err := c.addFriend(f); err != nil {
					return err
				}
			}
			var id [16]byte = f.account
			c.Payload = append(id[:], 0)
			return onPartyInvite(c)
		}},
		"friends": {"!friends - list the server's view of your friends and party", func(c *Ctx, a []string) error {
			c.State.mu.Lock()
			var names, party []string
			for _, f := range c.State.friends {
				names = append(names, f.name)
			}
			for _, f := range c.State.party {
				party = append(party, f.name)
			}
			c.State.mu.Unlock()
			return c.SystemChat(fmt.Sprintf("friends: %s | party: %s", strings.Join(names, ", "), strings.Join(party, ", ")))
		}},
		"queue": {"!queue - enter the matchmaking queue", func(c *Ctx, a []string) error {
			return onMatchmakingStart(c)
		}},
		"found": {"!found <n> [needed] - update the Searching panel's player count", func(c *Ctx, a []string) error {
			if len(a) < 1 {
				return fmt.Errorf("usage: !found <n> [needed]")
			}
			var found, needed int32 = 0, 12
			fmt.Sscan(a[0], &found)
			if len(a) > 1 {
				fmt.Sscan(a[1], &needed)
			}
			return c.Send("ClientInLobbyMatchmaking", 25600, queueStatus(mmStatusSearching, &queueStats{good: true, found: found, needed: needed, fill: found * 4 * 6 / max(needed, 1)}))
		}},
		"instance": {"!instance [map] - send 20600 (game found): load a map (name or GUID, default Temple of Anubis; !maps lists them) and connect to the game server", func(c *Ctx, a []string) error {
			mapID := uint64(DefaultMap)
			if len(a) > 0 {
				m, ok := maps.Find(strings.Join(a, " "))
				if !ok {
					return fmt.Errorf("unknown map %q (!maps lists them)", strings.Join(a, " "))
				}
				mapID = m.Data
			}
			conn, addr, err := c.sendToGame(mapID)
			if err != nil {
				return err
			}
			return c.SystemChat(fmt.Sprintf("sent 20600: %s, connect to %s as conn %d", mapLabel(mapID), addr, conn.ConnID))
		}},
		"gameserver": {"!gameserver [name] - list the game servers, or send new matches to that one (gameserver = R&D, instance = the new server)", func(c *Ctx, a []string) error {
			in := c.logic.Instances
			if in == nil {
				return errors.New("lobbylogic runs without a game server link")
			}
			if len(a) > 0 {
				in.SetActive(a[0])
			}
			for _, s := range in.Servers() {
				line := fmt.Sprintf("%s: %s", s.Name, map[bool]string{true: "connected " + s.Addr, false: "not connected"}[s.Connected])
				if s.Connected {
					modes, maps := "all modes", "all maps"
					if len(s.Modes) > 0 {
						modes = strings.Join(s.Modes, "/")
					}
					if s.Maps > 0 {
						maps = fmt.Sprintf("%d maps", s.Maps)
					}
					line += fmt.Sprintf(" (%s, %s, heartbeat %v)", modes, maps, s.Heartbeat)
				}
				if s.Active {
					line += ", gets new matches"
				}
				if err := c.SystemChat(fmt.Sprintf("%s, %d assigned", line, s.Conns)); err != nil {
					return err
				}
			}
			return nil
		}},
		"maps": {"!maps - list the maps !instance and the MAP pickers know", func(c *Ctx, a []string) error {
			for _, m := range maps.All {
				line := fmt.Sprintf("%#x %s: %s %s, %d networked", m.Data&0xffffffff, m.Name, m.Kind, m.Mode, len(m.Networked))
				if err := c.SystemChat(line); err != nil {
					return err
				}
			}
			return nil
		}},
		"unqueue": {"!unqueue - leave the matchmaking queue", func(c *Ctx, a []string) error {
			return onMatchmakingCancel(c)
		}},
		"sessions": {"!sessions - list connected clients", func(c *Ctx, a []string) error {
			var ids []string
			for _, s := range c.logic.liveSessions() {
				ids = append(ids, fmt.Sprintf("%d (cid %d)", s.ID, s.BnetCID))
			}
			return c.SystemChat("sessions: " + strings.Join(ids, ", "))
		}},
	}
}

func cmdHelp(c *Ctx, _ []string) error {
	var names []string
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := c.SystemChat(commands[name].usage); err != nil {
			return err
		}
	}
	return nil
}

func runCommand(c *Ctx, line string) error {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return cmdHelp(c, nil)
	}
	cmd, ok := commands[strings.ToLower(fields[0])]
	if !ok {
		return c.SystemChat("Unknown command !" + fields[0] + " - try !help")
	}
	if err := cmd.run(c, fields[1:]); err != nil {
		return c.SystemChat("!" + fields[0] + ": " + err.Error())
	}
	return nil
}

// newNamedFriend creates a friend object; names without "#" get "#0000".
func (c *Ctx) newNamedFriend(name string) *friend {
	if !strings.Contains(name, "#") {
		name += "#0000"
	}
	c.State.mu.Lock()
	defer c.State.mu.Unlock()
	return c.State.newFriend(name)
}

// friendByName finds a friend by BattleTag or by the name before "#" (case-insensitive).
func (c *Ctx) friendByName(name string) *friend {
	c.State.mu.Lock()
	defer c.State.mu.Unlock()
	for _, f := range c.State.friends {
		short, _, _ := strings.Cut(f.name, "#")
		if strings.EqualFold(f.name, name) || strings.EqualFold(short, name) {
			return f
		}
	}
	return nil
}

// onChannelList handles ClientOutLobbyChat 21701 {channel {i32 type, ID}} (/list): the
// answer is 20401 {channel, [ {cstring name, ID} ]} (handler FUN_7ff650c5c040 shows
// the names). General lists every connected client; Party the party.
func onChannelList(c *Ctx) error {
	if len(c.Payload) < 20 {
		return fmt.Errorf("21701 too short: %x", c.Payload)
	}
	ctype := binary.LittleEndian.Uint32(c.Payload)
	type member struct {
		name string
		id   []byte
	}
	me, _ := hex.DecodeString(c.Session.ClientJamID)
	members := []member{{playerName(), me}}
	switch ctype {
	case chanGeneral:
		for _, s := range c.logic.liveSessions() {
			if s.ID != c.Session.ID {
				id, _ := hex.DecodeString(s.ClientJamID)
				members = append(members, member{playerName(), id})
			}
		}
	case chanParty:
		c.State.mu.Lock()
		for _, f := range c.State.party {
			members = append(members, member{f.name, append([]byte(nil), f.account[:]...)})
		}
		c.State.mu.Unlock()
	}
	b := append([]byte{}, c.Payload[:20]...)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(members)))
	for _, m := range members {
		b = appendCString(b, m.name)
		b = append(b, m.id...)
	}
	return c.Send("ClientInLobbyChat", 20401, b)
}

// onRemoveFriend handles ClientOutLobbyFriends 27002 {u64 cookie, ID} (/removefriend
// and the social menu): drop the friend (27102 {ID}) and take them out of the party.
func onRemoveFriend(c *Ctx) error {
	if len(c.Payload) < 24 {
		return fmt.Errorf("27002 too short: %x", c.Payload)
	}
	var id [16]byte
	copy(id[:], c.Payload[8:24])
	c.State.mu.Lock()
	kept := c.State.friends[:0]
	for _, f := range c.State.friends {
		if f.account != id {
			kept = append(kept, f)
		}
	}
	c.State.friends = kept
	inParty := false
	party := c.State.party[:0]
	for _, m := range c.State.party {
		if m.account == id {
			inParty = true
			continue
		}
		party = append(party, m)
	}
	c.State.party = party
	c.State.mu.Unlock()
	if err := c.Send("ClientInLobbyFriends", 27102, id[:]); err != nil {
		return err
	}
	if inParty {
		return c.sendParty()
	}
	return nil
}
