package lobbylogic

import (
	"bytes"
	"testing"
)

func TestPrometheusCallEncoding(t *testing.T) {
	got := encodePrometheusCall(7, "notify", `{"text":"hi"}`)
	want := append([]byte{7, 0, 0, 0}, "notify\x00{\"text\":\"hi\"}\x00"...)
	if !bytes.Equal(got, want) || len(got) != 25 { // docs/JAM.md §13.5 example
		t.Fatalf("got %x", got)
	}
}

func TestPrometheusResultDecoding(t *testing.T) {
	r, err := decodePrometheusResult(append([]byte{9, 0, 0, 0, 1}, "{\"pong\":true}\x00"...))
	if err != nil || r.Seq != 9 || !r.OK || r.Result != `{"pong":true}` {
		t.Fatalf("%+v %v", r, err)
	}
	for _, bad := range [][]byte{{1, 2}, {1, 0, 0, 0, 0, 'x'}, append([]byte{1, 0, 0, 0, 0}, "a\x00b\x00"...)} {
		if _, err := decodePrometheusResult(bad); err == nil {
			t.Errorf("%x: no error", bad)
		}
	}
}
