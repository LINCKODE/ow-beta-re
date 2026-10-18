// Package session derives the lobby session keys shared by the login and lobby servers.
//
// The login server hands the client four 64-byte keys (k0-k3) and a client ID
// (cid) in the GameUtilities referral. The lobby handshake starts with the client
// sending that cid, so deriving the keys from (secret, cid) lets the lobby server
// recompute them without any state shared with the login server.
//
// Roles (from the 1.74 research, same attribute names in 0.8):
//
//	MAC1 = HMAC-SHA256(k0, cn || sn)    MAC2 = HMAC-SHA256(k1, sn || cn)
//	s2c cipher key = HMAC-SHA256(k2, cn || sn)
//	c2s cipher key = HMAC-SHA256(k3, sn || cn)
package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
)

// Keys are k0-k3, in order.
type Keys [4][64]byte

// DeriveKeys returns k_i = HMAC-SHA512(secret, "k<i>" || cid as u64 little-endian).
func DeriveKeys(secret []byte, cid uint64) Keys {
	var keys Keys
	var cidBytes [8]byte
	binary.LittleEndian.PutUint64(cidBytes[:], cid)
	for i := range keys {
		mac := hmac.New(sha512.New, secret)
		fmt.Fprintf(mac, "k%d", i)
		mac.Write(cidBytes[:])
		copy(keys[i][:], mac.Sum(nil))
	}
	return keys
}

// NewClientID returns a random non-zero client ID.
func NewClientID() uint64 {
	var b [8]byte
	for {
		rand.Read(b[:])
		if id := binary.LittleEndian.Uint64(b[:]) >> 1; id != 0 {
			return id
		}
	}
}
