package lobby

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"net"
	"testing"

	"owserver/internal/session"
)

// TestHandshake plays the 0.8 client's side of the negotiation (FUN_7ff6510c2500)
// against Handshake, then sends one JAM frame the way the client would.
func TestHandshake(t *testing.T) {
	secret := []byte("test-secret")
	cid := uint64(8887162714385818036)
	keys := session.DeriveKeys(secret, cid) // what the login server sent the client
	serverAddr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 3724}

	client, server := net.Pipe()
	defer client.Close()
	result := make(chan *Session, 1)
	go func() {
		sess, err := Handshake(server, secret, serverAddr, [36]byte{})
		if err != nil {
			t.Error(err)
		}
		result <- sess
		// Server side: read one frame.
		if sess != nil {
			frame, err := readFrame(server, sess.Recv)
			if err != nil {
				t.Error(err)
			}
			if !bytes.Equal(frame, []byte{0x01, 0x00, 0xAA}) {
				t.Errorf("server decrypted frame %x", frame)
			}
		}
		server.Close()
	}()

	read := func(n int) []byte {
		t.Helper()
		b := make([]byte, n)
		if _, err := io.ReadFull(client, b); err != nil {
			t.Fatal(err)
		}
		return b
	}

	// state 0
	client.Write([]byte("HELLO PRO CLIENT\x00"))
	if got := read(17); string(got) != "HELLO PRO SERVER\x00" {
		t.Fatalf("server hello %q", got)
	}
	cn := make([]byte, 32)
	rand.Read(cn)
	ident := binary.LittleEndian.AppendUint64(nil, cid)
	client.Write(append(ident, cn...))

	// state 1
	nonce := read(65)
	if nonce[32] != 0 {
		t.Fatalf("difficulty %d, want 0", nonce[32])
	}
	sn := nonce[33:]
	proof := binary.LittleEndian.AppendUint64(nil, 1) // difficulty 0 -> counter 1
	client.Write(append(proof, mac(keys[0][:], cn, sn)...))

	// state 2
	if got := read(32); !hmac.Equal(got, mac(keys[1][:], sn, cn)) {
		t.Fatal("MAC2 does not verify")
	}

	// state 4: 256-byte blob + 36 bytes, JAM-encrypted with HMAC(k2, cn||sn)
	recv := NewJam(cipherKey(keys[2][:], cn, sn))
	final := read(292)
	recv.Crypt(final)
	blob := final[:256]
	h := hmac.New(sha256.New, blobKey) // FUN_7ff65110a400
	h.Write(blob[:0x13])
	h.Write(blob[0x13 : 0x13+0x9c])
	h.Write(blob[0xaf : 0xaf+1])
	h.Write(blob[0xd0 : 0xd0+0x2f])
	h.Write(blob[0xff:])
	if !hmac.Equal(h.Sum(nil), blob[0xb0:0xd0]) {
		t.Fatal("blob HMAC does not validate")
	}
	if blob[0] != 1 || !bytes.Equal(blob[1:5], []byte{127, 0, 0, 1}) || binary.LittleEndian.Uint16(blob[5:7]) != 3724 {
		t.Fatalf("blob address % x", blob[:7])
	}
	if !bytes.Equal(final[256:], make([]byte, 36)) {
		t.Fatalf("trailer % x", final[256:])
	}

	sess := <-result
	if sess == nil || sess.ClientID != cid {
		t.Fatalf("session %+v", sess)
	}

	// JAM: the client encrypts with HMAC(k3, sn||cn).
	send := NewJam(cipherKey(keys[3][:], sn, cn))
	frame := []byte{0, 0, 3, 0x01, 0x00, 0xAA}
	send.Crypt(frame)
	client.Write(frame)
	io.Copy(io.Discard, client) // wait for the server to finish
}

func TestHandshakeRejectsWrongSecret(t *testing.T) {
	cid := uint64(42)
	keys := session.DeriveKeys([]byte("login-secret"), cid)
	client, server := net.Pipe()
	defer client.Close()
	errc := make(chan error, 1)
	go func() {
		_, err := Handshake(server, []byte("other-secret"), &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 3724}, [36]byte{})
		errc <- err
		server.Close()
	}()
	client.Write([]byte("HELLO PRO CLIENT\x00"))
	io.ReadFull(client, make([]byte, 17))
	cn := make([]byte, 32)
	client.Write(append(binary.LittleEndian.AppendUint64(nil, cid), cn...))
	nonce := make([]byte, 65)
	io.ReadFull(client, nonce)
	client.Write(append(binary.LittleEndian.AppendUint64(nil, 1), mac(keys[0][:], cn, nonce[33:])...))
	if err := <-errc; err == nil {
		t.Fatal("handshake with a mismatched secret succeeded")
	}
}
