package view

import (
	"bytes"
	"encoding/binary"
	"unicode/utf8"
)

// Text decodes a string asset (types 0x07C, 0x0A9): a u64 offset (0x10) to NUL-terminated UTF-8 after a
// 16-byte header. ok is false for anything else.
func Text(b []byte) (string, bool) {
	if len(b) < 17 || binary.LittleEndian.Uint64(b) != 0x10 {
		return "", false
	}
	s := b[16:]
	if i := bytes.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	if !utf8.Valid(s) {
		return "", false
	}
	return string(s), true
}
