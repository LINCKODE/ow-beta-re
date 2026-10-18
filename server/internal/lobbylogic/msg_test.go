package lobbylogic

import (
	"bytes"
	"testing"
)

func TestEncodeMsgTokens(t *testing.T) {
	got, err := encodeMsgTokens([]string{"id:1,0x2", "u8:1", "i16:-2", "n:3", "u64:0x0DE0000000000C99", "f32:1", `str:Bob#1\s2`, "beef"})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{1, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 1, 0xfe, 0xff, 3, 0, 0, 0,
		0x99, 0x0c, 0, 0, 0, 0, 0xe0, 0x0d, 0, 0, 0x80, 0x3f}
	want = append(want, "Bob#1 2\x00"...)
	want = append(want, 0xbe, 0xef)
	if !bytes.Equal(got, want) {
		t.Fatalf("got  %x\nwant %x", got, want)
	}
	for _, bad := range []string{"u8:256", "zz", "x:1", "id:1"} {
		if _, err := encodeMsgTokens([]string{bad}); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}
