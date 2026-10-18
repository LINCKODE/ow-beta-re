package lobby

import (
	"encoding/hex"
	"testing"
)

// Known-answer test from the 1.74 research (confirmed by independent implementations).
func TestJamKnownAnswer(t *testing.T) {
	var key [32]byte
	for i := range key {
		key[i] = byte(i)
	}
	stream := make([]byte, 64)
	NewJam(key).Crypt(stream)
	want := "f07e5df1c8d51c0d754da6ddde34cca01db0ed26334eb94ae277135a0764c6a6" +
		"794d7ab0cc0978ef43de30766a7ee1d7c90e159df239d4016d450fa3d8207842"
	if got := hex.EncodeToString(stream); got != want {
		t.Fatalf("keystream mismatch\n got %s\nwant %s", got, want)
	}
}

// The keystream must not depend on how the data is split into Crypt calls.
func TestJamChunking(t *testing.T) {
	var key [32]byte
	key[0] = 0x42
	whole := make([]byte, 1000)
	NewJam(key).Crypt(whole)
	parts := make([]byte, 1000)
	j := NewJam(key)
	for off, n := 0, 1; off < len(parts); off, n = off+n, n+7 {
		end := min(off+n, len(parts))
		j.Crypt(parts[off:end])
	}
	if hex.EncodeToString(whole) != hex.EncodeToString(parts) {
		t.Fatal("chunked keystream differs")
	}
}
