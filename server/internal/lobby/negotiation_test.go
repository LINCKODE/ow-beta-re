package lobby

import (
	"bytes"
	"encoding/binary"
	"io"
	"log/slog"
	"testing"
)

func TestNewJamIDServerType(t *testing.T) {
	id := NewJamID(ServerTypeLobby)
	if typ := binary.LittleEndian.Uint64(id[:8]) >> 40 & 0xFFFF; typ != ServerTypeLobby {
		t.Fatalf("server type %d, want %d", typ, ServerTypeLobby)
	}
}

// The server must accept the client's CRC list with an empty negotiation
// message 3 on the same channel, and map channels 1..n to the listed protocols.
func TestNegotiationAcceptsList(t *testing.T) {
	var key [32]byte
	var out bytes.Buffer
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	l := &link{
		log:      log,
		hub:      NewHub(log),
		w:        &out,
		sess:     &Session{Send: NewJam(key)},
		channels: map[byte]*Protocol{0: protocolByCRC(0x11d82194)},
	}
	payload := []byte{2, 0, 0, 0, 0xaf, 0x8d, 0x95, 0x4e, 0xdd, 0x57, 0xd4, 0xd7} // [ClientInLobbyConnect, ClientOutLobbyConnect]
	if err := l.handle(append([]byte{1, 0}, payload...)); err != nil {
		t.Fatal(err)
	}
	if l.channels[1].Name != "ClientInLobbyConnect" || l.channels[2].Name != "ClientOutLobbyConnect" || !l.negotiated {
		t.Fatalf("channels %v", l.channels)
	}
	reply, err := readFrame(&out, NewJam(key))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reply, []byte{1, 2}) { // channel 1, index 2 = message 3, no payload
		t.Fatalf("reply %x", reply)
	}
}
