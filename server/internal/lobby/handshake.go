package lobby

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync/atomic"

	"owserver/internal/session"
)

// The negotiation before JAM, as implemented by the 0.8 client's
// LobbyClientPacketPipe (FUN_7ff6510c2500, state at +0x60):
//
//	C->S "HELLO PRO CLIENT\0"                          (17)
//	S->C "HELLO PRO SERVER\0"                          (17)  client: state 0 -> 1
//	C->S [u64 cid][32-byte cn]                         (40)
//	S->C [32-byte challenge][difficulty][32-byte sn]   (65)  client: state 1 -> 2
//	C->S [u64 proof-of-work counter][MAC1]             (40)
//	S->C MAC2                                          (32)  client: state 2 -> 4
//	S->C JAM( blob[256] | 2 x JamID | u32 )            (292) client: state 4 -> 6, hand-off to JAM
//
//	MAC1 = HMAC-SHA256(k0, cn||sn)     MAC2 = HMAC-SHA256(k1, sn||cn)
//	S->C cipher key = HMAC-SHA256(k2, cn||sn)
//	C->S cipher key = HMAC-SHA256(k3, sn||cn)                 (FUN_7ff6510c30f0)
//
// k0-k3 are the keys the login server sent in the lobby referral; they are
// recomputed here from the cid (see package session).

// NoReferralLogins counts handshakes that used empty keys (show-mode logins without
// a Battle.net referral).
var NoReferralLogins atomic.Int64

var (
	helloClient = []byte("HELLO PRO CLIENT\x00")
	helloServer = []byte("HELLO PRO SERVER\x00")
)

// difficulty is the proof-of-work difficulty in bits (FUN_7ff6510c2fa0: the client
// searches a counter whose hash of challenge||counter has this many zero bits).
// 0 means the client sends counter 1 without searching.
const difficulty = 0

// blobKey is the HMAC key the client validates the state blob with
// (FUN_7ff65110a400, key at 0x7ff6514bcb80). Identical to the 1.74 key.
var blobKey = []byte{
	0x35, 0x86, 0xF3, 0x62, 0x8A, 0x63, 0x1B, 0x70, 0x57, 0x12, 0x40, 0x5B, 0x8A, 0xCC, 0x71, 0xD4,
	0x0F, 0xD1, 0x67, 0x0C, 0xC1, 0xB0, 0x3E, 0xA3, 0x84, 0x97, 0x4A, 0x6F, 0xB1, 0xA7, 0x61, 0x96,
	0xB1, 0x42, 0xF0, 0xB7, 0x23, 0x10, 0xEA, 0x81, 0x16, 0xD0, 0x0A, 0x4C, 0x35, 0x2F, 0x09, 0xAC,
	0xDB, 0xFB, 0x50, 0xA6, 0x3E, 0xC5, 0x15, 0x3E, 0x62, 0xE4, 0xD6, 0x7F, 0xE0, 0x9B, 0xEE, 0xCC,
}

// Session is an established lobby connection's crypto state.
type Session struct {
	ClientID uint64
	Send     *Jam // server -> client
	Recv     *Jam // client -> server
}

// Handshake runs the server side of the negotiation on rw. addr is the server
// address the client connected to; it is embedded in the state blob.
// trailer is the 36 bytes after the blob: 4 x u64 (two 128-bit JamIDs, passed to the
// client's JAM hand-off) and a u32.
func Handshake(rw io.ReadWriter, secret []byte, addr *net.TCPAddr, trailer [36]byte) (*Session, error) {
	hello := make([]byte, len(helloClient))
	if _, err := io.ReadFull(rw, hello); err != nil {
		return nil, fmt.Errorf("read hello: %w", err)
	}
	if !bytes.Equal(hello, helloClient) {
		return nil, fmt.Errorf("unexpected hello %x", hello)
	}
	if _, err := rw.Write(helloServer); err != nil {
		return nil, err
	}

	var ident [40]byte
	if _, err := io.ReadFull(rw, ident[:]); err != nil {
		return nil, fmt.Errorf("read client id: %w", err)
	}
	cid := binary.LittleEndian.Uint64(ident[:8])
	cn := ident[8:]
	keys := session.DeriveKeys(secret, cid)

	challenge, sn := make([]byte, 32), make([]byte, 32)
	rand.Read(challenge)
	rand.Read(sn)
	if _, err := rw.Write(concat(challenge, []byte{difficulty}, sn)); err != nil {
		return nil, err
	}

	var proof [40]byte
	if _, err := io.ReadFull(rw, proof[:]); err != nil {
		return nil, fmt.Errorf("read MAC1: %w", err)
	}
	if !hmac.Equal(proof[8:], mac(keys[0][:], cn, sn)) {
		// EXPERIMENT: show-mode ("kiosk") logins skip Battle.net, so there's no
		// referral and the client announces cid 0. Try empty keys (HMAC with an
		// empty key equals HMAC with 64 zero bytes).
		var empty session.Keys
		if !hmac.Equal(proof[8:], mac(empty[0][:], cn, sn)) {
			return nil, fmt.Errorf("MAC1 mismatch for cid %d: the session secret differs from the login server's, or the cid is not from our referral (empty keys don't match either)", cid)
		}
		keys = empty
		NoReferralLogins.Add(1)
	}

	send := NewJam(cipherKey(keys[2][:], cn, sn))
	recv := NewJam(cipherKey(keys[3][:], sn, cn))

	final := append(stateBlob(addr), trailer[:]...)
	send.Crypt(final)
	if _, err := rw.Write(concat(mac(keys[1][:], sn, cn), final)); err != nil {
		return nil, err
	}
	return &Session{ClientID: cid, Send: send, Recv: recv}, nil
}

// stateBlob builds the 256-byte blob the client validates in state 4.
//
//	[0]        address type: 1 = IPv4 (4 bytes), 2 = IPv6 (16 bytes), 3 = hostname
//	[1:5]      IPv4 address
//	[5:7]      port (u16)
//	[0xb0:0xd0] HMAC-SHA256(blobKey, blob[0:0xb0] || blob[0xd0:0x100])
//
// The client rejects the blob only if both the address and the port differ from
// its connection's. NOTE: the port's byte order is assumed to be little-endian.
//
// The real client first applies an RSA public-key operation to the blob
// (FUN_7ff651339b10), which needs Blizzard's private key to satisfy; Prometheus
// hooks that step out (Network/LobbyBlobBypass), so the blob is sent as is.
func stateBlob(addr *net.TCPAddr) []byte {
	blob := make([]byte, 256)
	blob[0] = 1
	if ip4 := addr.IP.To4(); ip4 != nil {
		copy(blob[1:5], ip4)
	}
	binary.LittleEndian.PutUint16(blob[5:7], uint16(addr.Port))
	h := hmac.New(sha256.New, blobKey)
	h.Write(blob[:0xb0])
	h.Write(blob[0xd0:])
	copy(blob[0xb0:0xd0], h.Sum(nil))
	return blob
}

func mac(key []byte, parts ...[]byte) []byte {
	h := hmac.New(sha256.New, key)
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

func cipherKey(key []byte, a, b []byte) [32]byte {
	var k [32]byte
	copy(k[:], mac(key, a, b))
	return k
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
