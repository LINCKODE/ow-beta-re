package lobbylogic

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Game server choice in the social panel: the client always shows two incoming
// friend requests, one per game server. Accepting one sends new matches to that
// server from now on (Instances.SetActive, saved to Instances.SaveFile); either
// answer re-sends the request, so both stay in the list.

// serverRequest is a fake friend request standing for a game server.
type serverRequest struct {
	friend *friend
	server string // gamelink server name
	label  string // shown in chat
}

// serverRequests are the two choices. Their account IDs are fixed (the client keeps
// no presence records for requests, so re-use is fine; see nextIDSeed for friends).
var serverRequests = []serverRequest{
	{friend: &friend{name: "R&D#0001", account: idOf(0x5E5E0001), gameAccount: idOf(0x5E5E0002)}, server: "gameserver", label: "R&D (gameserver)"},
	{friend: &friend{name: "GAME#0001", account: idOf(0x5E5E0003), gameAccount: idOf(0x5E5E0004)}, server: "instance", label: "GAME (instance)"},
}

func serverRequestByID(id [16]byte) *serverRequest {
	for i := range serverRequests {
		if serverRequests[i].friend.account == id {
			return &serverRequests[i]
		}
	}
	return nil
}

func serverRequestFriends() []*friend {
	fs := make([]*friend, len(serverRequests))
	for i, r := range serverRequests {
		fs[i] = r.friend
	}
	return fs
}

// answerServerRequest handles 27001 for a server request: accept (action 0) makes
// that server the active one; the request is re-sent either way.
func answerServerRequest(c *Ctx, r *serverRequest, action uint32) error {
	if action == 0 {
		in := c.logic.Instances
		if in == nil {
			return errors.New("lobbylogic runs without a game server link")
		}
		in.SetActive(r.server)
		if err := c.SystemChat("Game server for new matches: " + r.label + " (saved)."); err != nil {
			return err
		}
	}
	return c.Send("ClientInLobbyFriends", 27103, friendRequest(r.friend))
}

// savedServer is the file form of the saved choice.
type savedServer struct {
	GameServer string `json:"game_server"`
}

// LoadActiveServer reads the saved game server choice; "" when there is none.
func LoadActiveServer(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var s savedServer
	if err := json.Unmarshal(b, &s); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return s.GameServer, nil
}

// saveActiveServer writes the choice atomically (temp file + rename).
func saveActiveServer(path, name string) error {
	b, err := json.MarshalIndent(savedServer{GameServer: name}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".game-server-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
