package lobbylogic

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"owserver/internal/lobby"
)

// The layout the client accepted (status 1 -> "Searching").
func TestQueueStatusEncoding(t *testing.T) {
	want := "0100000000000000000000000100000001000000010000000000000001000000000000000100000000000000000000000000000000000000"
	if got := hex.EncodeToString(queueStatus(mmStatusSearching, nil)); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestSearchStats(t *testing.T) {
	if s := searchStats(0, mmSearchTime); s.found != 1 || s.needed != mmPlayers || s.fill != 0 {
		t.Fatalf("start: %+v", s)
	}
	if s := searchStats(mmSearchTime*2, mmSearchTime); s.found != mmPlayers || s.fill/4 != 6 {
		t.Fatalf("end: %+v", s)
	}
}

// mmLogic is a Logic whose gateway is a pipe: statuses() returns the 25600 statuses sent.
func mmLogic(t *testing.T) (*Ctx, func() []uint32) {
	t.Helper()
	r, w := net.Pipe()
	l := &Logic{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), enc: json.NewEncoder(w)}
	var mu sync.Mutex
	var got []uint32
	go func() {
		dec := json.NewDecoder(r)
		for {
			var m lobby.WireMessage
			if dec.Decode(&m) != nil {
				return
			}
			if m.Msg == 25600 {
				mu.Lock()
				got = append(got, binary.LittleEndian.Uint32(m.Payload))
				mu.Unlock()
			}
		}
	}()
	t.Cleanup(func() { w.Close(); r.Close() })
	c := &Ctx{Log: l.Log, Session: &lobby.SessionInfo{ID: 1}, logic: l}
	return c, func() []uint32 {
		mu.Lock()
		defer mu.Unlock()
		return append([]uint32(nil), got...)
	}
}

func setMMTimes(t *testing.T, search, found, enter time.Duration) {
	s, f, e := mmSearchTime, mmFoundTime, mmEnterTime
	mmSearchTime, mmFoundTime, mmEnterTime = search, found, enter
	t.Cleanup(func() { mmSearchTime, mmFoundTime, mmEnterTime = s, f, e })
}

// Play -> searching updates -> found, found (Entering Match) -> no game server: the
// ticket is removed.
func TestQueueFlow(t *testing.T) {
	setMMTimes(t, 30*time.Millisecond, 10*time.Millisecond, 10*time.Millisecond)
	c, statuses := mmLogic(t)
	if err := onMatchmakingStart(c); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	got := statuses()
	n := len(got)
	if n < 4 || got[0] != mmStatusSearching || got[n-3] != mmStatusFound || got[n-2] != mmStatusFound || got[n-1] != mmStatusCancelled {
		t.Fatalf("statuses %v", got)
	}
	if stopQueue(1) {
		t.Fatal("queue still registered")
	}
}

// Cancel during the search stops it: nothing after the cancel's status 6.
func TestQueueCancel(t *testing.T) {
	setMMTimes(t, time.Hour, time.Millisecond, time.Millisecond)
	c, statuses := mmLogic(t)
	if err := onMatchmakingStart(c); err != nil {
		t.Fatal(err)
	}
	if err := onMatchmakingCancel(c); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := statuses(); len(got) != 2 || got[0] != mmStatusSearching || got[1] != mmStatusCancelled {
		t.Fatalf("statuses %v", got)
	}
}
