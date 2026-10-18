package lobbylogic

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

// Social: friends (ClientInLobbyFriends 27100-27117 / ClientOutLobbyFriends 27000-27008)
// and party membership (ClientInLobbyParty 20700 / ClientOutLobbyParty 22100-22110).
// Everything here was confirmed in game (2026-09-24); see docs/early-notes/04-lobby-and-instance-notes.md.
//
// Friends are three things on the client:
//   - a friends-list entry: 27101 {u32, u64, ID account} (27100 list 1),
//   - an account presence record and a game-account presence record: 27109
//     {[attr], ID self, ID parent} (27100 list 3). The parent of the account record
//     is null; the game account's parent is the account. A record that is its own
//     parent makes the client loop forever.
//   - presence attributes {bytes FieldKey, bytes Variant, u32 op (0 set, 1 remove)}:
//     BN/1/4 = BattleTag (shown without "#1234"), BN/2/1 = online. BN/1/1 would be a
//     "real name" shown instead. BN/2/8 is rich presence (a template) - don't send junk.
//
// Incoming friend requests are 27100 list 2 / 27103 {str tag, str name, str, u32, u64,
// ID, ID} and 27104 {ID} removes one. The client answers with 27001 {u64 cookie, ID, u32}.

// SessionState is one client's social state, kept by the logic process.
type SessionState struct {
	mu       sync.Mutex
	nextID   uint64
	friends  []*friend
	requests map[[16]byte]*friend // incoming friend requests by account ID
	party    []*friend            // other party members (the local player is added when encoding)
	leader   *friend              // party leader; nil = the local player
}

type friend struct {
	name        string // BattleTag
	account     [16]byte
	gameAccount [16]byte
	icon        uint64
}

// nextIDSeed makes fake-player IDs unique across logic restarts: the client keeps
// presence records for IDs it has seen (27100 doesn't clear them), and re-using an ID
// leaves the new friend without a game account ("Player Not Found" on invite).
var nextIDSeed = uint64(time.Now().Unix()) << 20

func newSessionState() *SessionState {
	return &SessionState{nextID: nextIDSeed, requests: map[[16]byte]*friend{}}
}

// newFriend makes a friend with fresh IDs (account n, game account n+1).
func (s *SessionState) newFriend(name string) *friend {
	s.nextID += 2
	return &friend{name: name, account: idOf(s.nextID), gameAccount: idOf(s.nextID + 1), icon: 0x02500000000002F9}
}

func (s *SessionState) friendByID(id [16]byte) *friend {
	for _, f := range s.friends {
		if f.account == id {
			return f
		}
	}
	return nil
}

// idOf builds the 16-byte ID {n, n} used for fake players.
func idOf(n uint64) [16]byte {
	var id [16]byte
	binary.LittleEndian.PutUint64(id[:8], n)
	binary.LittleEndian.PutUint64(id[8:], n)
	return id
}

// --- encoders -------------------------------------------------------------

const presenceProgramBN = 0x424E // "BN"

func appendVarint(b []byte, v uint64) []byte { return binary.AppendUvarint(b, v) }

// fieldKey is a serialized bgs.protocol.presence.v1.FieldKey {1 program, 2 group, 3 field}.
func fieldKey(group, field uint64) []byte {
	b := appendVarint([]byte{0x08}, presenceProgramBN)
	b = appendVarint(append(b, 0x10), group)
	return appendVarint(append(b, 0x18), field)
}

// Serialized bgs.protocol.Variant values.
func variantBool(v bool) []byte {
	if v {
		return []byte{0x10, 1}
	}
	return []byte{0x10, 0}
}

func variantString(s string) []byte {
	return append(appendVarint([]byte{0x2A}, uint64(len(s))), s...)
}

func appendBytes(b, v []byte) []byte {
	b = binary.LittleEndian.AppendUint32(b, uint32(len(v)))
	return append(b, v...)
}

func appendCString(b []byte, s string) []byte { return append(append(b, s...), 0) }

// presenceRecord encodes {[ {bytes key, bytes value, u32 op} ], ID self, ID parent}.
func presenceRecord(self, parent [16]byte, attrs ...[2][]byte) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(len(attrs)))
	for _, a := range attrs {
		b = appendBytes(b, a[0])
		b = appendBytes(b, a[1])
		b = binary.LittleEndian.AppendUint32(b, 0)
	}
	b = append(b, self[:]...)
	return append(b, parent[:]...)
}

// friendPresence is the account + game-account records for an online friend.
func friendPresence(f *friend) []byte {
	b := binary.LittleEndian.AppendUint32(nil, 2)
	b = append(b, presenceRecord(f.account, [16]byte{}, [2][]byte{fieldKey(1, 4), variantString(f.name)})...)
	return append(b, presenceRecord(f.gameAccount, f.account, [2][]byte{fieldKey(2, 1), variantBool(true)})...)
}

// friendRequest encodes a 27100 list-2 / 27103 element.
func friendRequest(f *friend) []byte {
	b := appendCString(nil, f.name)
	b = appendCString(b, f.name)
	b = appendCString(b, "")
	b = binary.LittleEndian.AppendUint32(b, 1)
	b = binary.LittleEndian.AppendUint64(b, 0)
	b = append(b, f.account[:]...)
	return append(b, f.account[:]...)
}

// friendsState encodes 27100 with only incoming requests (lists 1, 3, 4 empty).
func friendsState(requests []*friend) []byte {
	b := binary.LittleEndian.AppendUint32(nil, 0)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(requests)))
	for _, r := range requests {
		b = append(b, friendRequest(r)...)
	}
	b = binary.LittleEndian.AppendUint32(b, 0)
	return binary.LittleEndian.AppendUint32(b, 0)
}

// partyMember encodes one 20700 member (see party.go for the layout).
func partyMember(name string, leader bool, icon uint64, id []byte) []byte {
	b := appendCString(nil, name)
	if leader {
		b = append(b, 1)
	} else {
		b = append(b, 0)
	}
	b = append(b, partyStatusMember)
	b = binary.LittleEndian.AppendUint64(b, icon)
	b = binary.LittleEndian.AppendUint64(b, 0)
	b = append(b, id...)
	return append(b, id...)
}

// --- sending --------------------------------------------------------------

// addFriend makes f an online friend on the client.
func (c *Ctx) addFriend(f *friend) error {
	c.State.mu.Lock()
	c.State.friends = append(c.State.friends, f)
	c.State.mu.Unlock()
	entry := binary.LittleEndian.AppendUint32(nil, 1)
	entry = binary.LittleEndian.AppendUint64(entry, 0)
	entry = append(entry, f.account[:]...)
	if err := c.Send("ClientInLobbyFriends", 27101, entry); err != nil {
		return err
	}
	return c.Send("ClientInLobbyFriends", 27109, friendPresence(f))
}

// sendParty sends the party state: the local player (leader) plus c.State.party; with
// no game server linked the empty party instead (availability.go), and nothing while
// that isn't known yet (the availability report sends it).
func (c *Ctx) sendParty() error {
	available, known := c.logic.gameAvailable()
	if !known {
		return nil
	}
	if !available {
		return c.Send("ClientInLobbyParty", 20700, noParty())
	}
	c.State.mu.Lock()
	members := append([]*friend(nil), c.State.party...)
	c.State.mu.Unlock()
	b := append([]byte{}, soloPartyID[:]...)
	c.State.mu.Lock()
	leader := c.State.leader
	c.State.mu.Unlock()
	b = binary.LittleEndian.AppendUint32(b, uint32(1+len(members)))
	b = append(b, partyMember(playerName(), leader == nil, playerIcon(), c.ClientJamID())...)
	for _, m := range members {
		b = append(b, partyMember(m.name, leader == m, m.icon, m.account[:])...)
	}
	return c.Send("ClientInLobbyParty", 20700, b)
}

// chatFrom shows text in chat as said by f.
func (c *Ctx) chatFrom(f *friend, text string) error {
	return c.Send("ClientInLobbyChat", 20400, chatMessage(1, f.name, binary.LittleEndian.Uint64(f.account[:8]), text))
}

// --- handlers -------------------------------------------------------------

// sendSocialOnConnect sends the friends state after login: the game server choice
// requests (serverchoice.go).
func sendSocialOnConnect(c *Ctx) error {
	return c.Send("ClientInLobbyFriends", 27100, friendsState(serverRequestFriends()))
}

// ResyncSession resets a client that outlived the previous logic process: a friends
// state with only the server choice requests (27100 clears the client's friends
// manager) and a party of one.
func ResyncSession(c *Ctx) error {
	c.Log.Info("resyncing client: friends and party reset")
	if err := c.Send("ClientInLobbyFriends", 27100, friendsState(serverRequestFriends())); err != nil {
		return err
	}
	if err := c.sendParty(); err != nil {
		return err
	}
	if err := sendPlayerProgression(c); err != nil {
		return err
	}
	if err := sendHeroProgression(c, 1); err != nil {
		return err
	}
	return joinDefaultChannels(c)
}

// onFriendRequestAnswer handles 27001 {u64 cookie, ID, u32 action}; action 0 = accept.
func onFriendRequestAnswer(c *Ctx) error {
	if len(c.Payload) < 28 {
		return fmt.Errorf("27001 too short: %x", c.Payload)
	}
	var id [16]byte
	copy(id[:], c.Payload[8:24])
	action := binary.LittleEndian.Uint32(c.Payload[24:])
	if r := serverRequestByID(id); r != nil {
		if err := c.Send("ClientInLobbyFriends", 27104, id[:]); err != nil {
			return err
		}
		return answerServerRequest(c, r, action)
	}
	c.State.mu.Lock()
	f := c.State.requests[id]
	delete(c.State.requests, id)
	c.State.mu.Unlock()
	if err := c.Send("ClientInLobbyFriends", 27104, id[:]); err != nil {
		return err
	}
	if f == nil || action != 0 {
		c.Log.Info("friend request declined or unknown", "action", action)
		return nil
	}
	if err := c.addFriend(f); err != nil {
		return err
	}
	return c.chatFrom(f, "Cheers, love! Thanks for the add.")
}

// onAddFriend handles 27000 {u64 cookie, u32 kind (1 BattleTag, 2 e-mail), str target,
// str message}: the target becomes an online friend right away.
func onAddFriend(c *Ctx) error {
	if len(c.Payload) < 13 {
		return fmt.Errorf("27000 too short: %x", c.Payload)
	}
	kind := binary.LittleEndian.Uint32(c.Payload[8:])
	target, _, _ := bytes.Cut(c.Payload[12:], []byte{0})
	name := string(target)
	if kind == 2 { // e-mail: use the local part as the name
		local, _, _ := bytes.Cut(target, []byte("@"))
		name = string(local) + "#0000"
	}
	c.State.mu.Lock()
	f := c.State.newFriend(name)
	c.State.mu.Unlock()
	if err := c.addFriend(f); err != nil {
		return err
	}
	return c.SystemChat(name + " is now your friend.")
}

// maxPartySize is Overwatch's party limit.
const maxPartySize = 6

// onPartyInvite handles ClientOutLobbyParty 22101 {ID, bool}: the friend accepts at once.
func onPartyInvite(c *Ctx) error {
	if len(c.Payload) < 16 {
		return fmt.Errorf("22101 too short: %x", c.Payload)
	}
	var id [16]byte
	copy(id[:], c.Payload[:16])
	c.State.mu.Lock()
	f := c.State.friendByID(id)
	joined := false
	if f != nil && len(c.State.party)+1 < maxPartySize {
		joined = true
		for _, m := range c.State.party {
			if m == f {
				joined = false
			}
		}
		if joined {
			c.State.party = append(c.State.party, f)
		}
	}
	c.State.mu.Unlock()
	if !joined {
		c.Log.Info("party invite ignored (unknown friend, already in party or party full)")
		return nil
	}
	if err := c.sendParty(); err != nil {
		return err
	}
	return c.chatFrom(f, "Joined your party!")
}

// onPartyKick handles ClientOutLobbyParty 22102 {ID}. The client prints "X left the
// party" itself when the member disappears from the next party state.
func onPartyKick(c *Ctx) error {
	if len(c.Payload) < 16 {
		return fmt.Errorf("22102 too short: %x", c.Payload)
	}
	var id [16]byte
	copy(id[:], c.Payload[:16])
	c.State.mu.Lock()
	kept := c.State.party[:0]
	for _, m := range c.State.party {
		if m.account != id {
			kept = append(kept, m)
		}
	}
	c.State.party = kept
	if c.State.leader != nil && c.State.leader.account == id {
		c.State.leader = nil
	}
	c.State.mu.Unlock()
	return c.sendParty()
}

// onPartyPromote handles ClientOutLobbyParty 22103 {ID}: that member becomes leader.
// The local player then no longer leads (and can't queue).
func onPartyPromote(c *Ctx) error {
	if len(c.Payload) < 16 {
		return fmt.Errorf("22103 too short: %x", c.Payload)
	}
	var id [16]byte
	copy(id[:], c.Payload[:16])
	c.State.mu.Lock()
	for _, m := range c.State.party {
		if m.account == id {
			c.State.leader = m
		}
	}
	c.State.mu.Unlock()
	return c.sendParty()
}

// onPartyLeave handles ClientOutLobbyParty 22104 (empty): the local player leaves,
// i.e. ends up alone in a new party of one.
func onPartyLeave(c *Ctx) error {
	c.State.mu.Lock()
	c.State.party = nil
	c.State.leader = nil
	c.State.mu.Unlock()
	return c.sendParty()
}

// whisperChannel is the chat channel type used for whisper replies (unconfirmed;
// channel types 0-4 render in different colours).
const whisperChannel = 3

// onWhisper handles ClientOutLobbyFriends 27004 {u64 cookie, ID account, ID game
// account, cstring text}: the friend answers.
func onWhisper(c *Ctx) error {
	if len(c.Payload) < 41 {
		return fmt.Errorf("27004 too short: %x", c.Payload)
	}
	var id [16]byte
	copy(id[:], c.Payload[8:24])
	text, _, _ := bytes.Cut(c.Payload[40:], []byte{0})
	c.State.mu.Lock()
	f := c.State.friendByID(id)
	c.State.mu.Unlock()
	if f == nil {
		return c.SystemChat("Whisper target is unknown to the server (was the lobby logic restarted?).")
	}
	reply := fmt.Sprintf("You whispered \"%s\". I'm a bot, but thanks!", text)
	return c.Send("ClientInLobbyChat", 20400, chatMessage(whisperChannel, f.name, binary.LittleEndian.Uint64(f.account[:8]), reply))
}

// onFriendFlags handles ClientOutLobbyFriends 27005 {u64 cookie, ID, u64 flags}
// (1 = favourite): the answer is 27105 {u32, u64 flags, ID}, which updates the
// friend-list entry's fields (the ones 27101 creates; handler case 0x69E1).
func onFriendFlags(c *Ctx) error {
	if len(c.Payload) < 32 {
		return fmt.Errorf("27005 too short: %x", c.Payload)
	}
	b := binary.LittleEndian.AppendUint32(nil, 1)
	b = append(b, c.Payload[24:32]...) // flags
	b = append(b, c.Payload[8:24]...)  // ID
	return c.Send("ClientInLobbyFriends", 27105, b)
}
