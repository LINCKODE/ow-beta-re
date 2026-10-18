package lobby

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a bytes.Buffer safe for the link's writer and the test's reader.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.b.Bytes()...)
}

// newTestLink returns a negotiated link whose channels are ClientInLobbyConnect
// (1) and ClientOutLobbyConnect (2), registered with a fresh hub.
func newTestLink(t *testing.T) (*Hub, *link, *syncBuffer, [32]byte) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := NewHub(log)
	var key [32]byte
	out := &syncBuffer{}
	l := &link{
		log:        log,
		hub:        hub,
		conn:       io.NopCloser(nil),
		w:          out,
		sess:       &Session{Send: NewJam(key)},
		negotiated: true,
		channels: map[byte]*Protocol{
			0: protocolByCRC(0x11d82194),
			1: ProtocolByName("ClientInLobbyConnect"),
			2: ProtocolByName("ClientOutLobbyConnect"),
		},
	}
	hub.add(l)
	return hub, l, out, key
}

func TestLuaHandlerConsumesAndReplies(t *testing.T) {
	hub, l, out, key := newTestLink(t)
	vm := NewLuaVM(hub.log, hub, "", nil)
	if _, err := vm.Eval(`on(21800, function(m) send("ClientInLobbyConnect", 20501, pack("bz", 7, "hi")); return true end)`); err != nil {
		t.Fatal(err)
	}
	if err := l.handle([]byte{2, 0, 0xAA}); err != nil { // channel 2 index 0 = 21800
		t.Fatal(err)
	}
	reply, err := readFrame(bytes.NewReader(out.Bytes()), NewJam(key))
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{1, 1, 7, 'h', 'i', 0}; !bytes.Equal(reply, want) { // channel 1 index 1 = 20501
		t.Fatalf("reply %x, want %x", reply, want)
	}
	tr := hub.Traffic(0, 0, 0)
	if len(tr) != 2 || tr[0].Dir != "in" || tr[1].Dir != "out" || tr[1].Via != "lua" {
		t.Fatalf("traffic %+v", tr)
	}
}

func TestLuaPackUnpack(t *testing.T) {
	hub, _, _, _ := newTestLink(t)
	vm := NewLuaVM(hub.log, hub, "", nil)
	out, err := vm.Eval(`hex(pack("b h i q ? z s c3 x2", 1, 2, 3, "0x0240000000000096", true, "ab", "xyz", "Q"))`)
	if err != nil {
		t.Fatal(err)
	}
	want := `"01` + `0200` + `03000000` + `9600000000004002` + `01` + `616200` + `0300000078797a` + `510000` + `0000"`
	if strings.TrimSpace(out) != "=> "+want {
		t.Fatalf("got %s want %s", out, want)
	}
	out, err = vm.Eval(`unpack("h q z", pack("h q z", 513, "0x0240000000000096", "yo"))`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `=> 513`) || !strings.Contains(out, `=> "0x0240000000000096"`) || !strings.Contains(out, `=> "yo"`) || !strings.Contains(out, `=> 14`) {
		t.Fatalf("unpack: %s", out)
	}
}

func TestLuaHasNoOSLibrary(t *testing.T) {
	hub, _, _, _ := newTestLink(t)
	vm := NewLuaVM(hub.log, hub, "", nil)
	if out, _ := vm.Eval(`os == nil and io == nil and dofile == nil`); strings.TrimSpace(out) != "=> true" {
		t.Fatalf("got %q", out)
	}
}

// A backend gets hello with the live session, then the client's messages, and
// its sends reach the client.
func TestBackendRoundTrip(t *testing.T) {
	hub, l, out, key := newTestLink(t)
	gw, be := net.Pipe()
	go hub.serveBackend(gw)
	dec := json.NewDecoder(bufio.NewReader(be))
	be.SetDeadline(time.Now().Add(5 * time.Second))

	var hello WireMessage
	if err := dec.Decode(&hello); err != nil {
		t.Fatal(err)
	}
	if hello.Type != WireHello || len(hello.Sessions) != 1 || hello.Sessions[0].ID != l.id || hello.Sessions[0].Channels[1] != "ClientInLobbyConnect" {
		t.Fatalf("hello %+v", hello)
	}

	if err := l.handle([]byte{2, 0, 0xAB, 0xCD}); err != nil {
		t.Fatal(err)
	}
	var recv WireMessage
	if err := dec.Decode(&recv); err != nil {
		t.Fatal(err)
	}
	if recv.Type != WireRecv || recv.Msg != 21800 || recv.Protocol != "ClientOutLobbyConnect" || !bytes.Equal(recv.Payload, []byte{0xAB, 0xCD}) {
		t.Fatalf("recv %+v", recv)
	}

	if err := json.NewEncoder(be).Encode(WireMessage{Type: WireSend, Session: l.id, Msg: 20503, Payload: []byte{9}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(out.Bytes()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	reply, err := readFrame(bytes.NewReader(out.Bytes()), NewJam(key))
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{1, 3, 9}; !bytes.Equal(reply, want) { // protocol looked up from 20503
		t.Fatalf("reply %x, want %x", reply, want)
	}
	be.Close()
}
