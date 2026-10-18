package lobbylogic

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
)

func TestServerChoiceSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "game-server.json")
	if got, err := LoadActiveServer(path); err != nil || got != "" {
		t.Fatalf("missing file: got %q, %v", got, err)
	}
	in := &Instances{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), SaveFile: path}
	in.SetActive("gameserver")
	if got, err := LoadActiveServer(path); err != nil || got != "gameserver" {
		t.Fatalf("after SetActive: got %q, %v", got, err)
	}
	in.SetActive("instance")
	if got, _ := LoadActiveServer(path); got != "instance" {
		t.Fatalf("after second SetActive: got %q", got)
	}
}

func TestServerRequests(t *testing.T) {
	seen := map[[16]byte]bool{}
	for _, r := range serverRequests {
		if seen[r.friend.account] {
			t.Fatalf("duplicate account ID for %s", r.friend.name)
		}
		seen[r.friend.account] = true
		if got := serverRequestByID(r.friend.account); got == nil || got.server != r.server {
			t.Fatalf("lookup of %s: %+v", r.friend.name, got)
		}
	}
	if serverRequestByID(idOf(6)) != nil {
		t.Fatal("an ordinary request matched a server request")
	}
	if got := serverRequestByID(serverRequests[1].friend.account); got.server != "instance" {
		t.Fatalf("GAME maps to %q", got.server)
	}
}
