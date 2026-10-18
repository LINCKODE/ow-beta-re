package lobbylogic

import (
	"bytes"
	"testing"
)

func TestNoticeMessage(t *testing.T) {
	b, err := noticeMessage(NoticeCustomServerNotLoaded)
	if err != nil || !bytes.Equal(b, []byte{39}) {
		t.Fatalf("notice 39 = %x, %v", b, err)
	}
	if _, err := noticeMessage(noticeCount); err == nil {
		t.Fatal("index 52 must be refused (the client doesn't bounds-check)")
	}
}

func TestStringMessage(t *testing.T) {
	want := []byte{0x99, 0x0c, 0, 0, 0, 0, 0xe0, 0x0d}
	if b := stringMessage(0x0DE0000000000C99); !bytes.Equal(b, want) {
		t.Fatalf("got %x want %x", b, want)
	}
}

func TestParseStringID(t *testing.T) {
	for in, want := range map[string]uint64{
		"c99":                0x0DE0000000000C99,
		"0x0DE0000000001A14": 0x0DE0000000001A14,
		"0x0DE0000000000C99": 0x0DE0000000000C99,
	} {
		if got, err := parseStringID(in); err != nil || got != want {
			t.Errorf("%s: %#x, %v", in, got, err)
		}
	}
	if _, err := parseStringID("zz"); err == nil {
		t.Error("bad hex accepted")
	}
}
