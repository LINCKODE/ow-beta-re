package lobbylogic

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"owserver/internal/gamelink"
)

// Live server view (docs/DebugDrawing.md §5, T072): the game server streams its view
// of a subscribed connection's match every tick over the game server link (gamelink
// frame); lobbylogic relays each frame to the connection's lobby session as
// PrometheusServerView 32002 (docs/JAM.md §13.8) for the client's Prometheus.
const msgPrometheusServerView = 32002 // {u32 seq, u32 frame, u64 sent_us, u64 relay_us, str body}

// encodeServerView encodes 32002; relayUS is lobbylogic's wall clock (Unix µs).
func encodeServerView(f *gamelink.ViewFrame, relayUS int64) []byte {
	b := make([]byte, 0, 24+len(f.Body)+1)
	b = binary.LittleEndian.AppendUint32(b, f.Seq)
	b = binary.LittleEndian.AppendUint32(b, f.Frame)
	b = binary.LittleEndian.AppendUint64(b, uint64(f.SentUS))
	b = binary.LittleEndian.AppendUint64(b, uint64(relayUS))
	b = append(b, f.Body...)
	return append(b, 0)
}

// viewCounters are the relay's counters, logged every viewLogEvery.
type viewCounters struct {
	frames, bytes, errs atomic.Uint64
	orphans             atomic.Uint64 // frames for a session lobbylogic doesn't have
	lastLog             atomic.Int64  // Unix ns
	loggedFrames        atomic.Uint64
}

var viewStats viewCounters

const viewLogEvery = 10 * time.Second

// ServerViewFrame relays a game server's view frame to the lobby session (Instances.OnFrame).
func (l *Logic) ServerViewFrame(session uint64, f *gamelink.ViewFrame) {
	l.mu.Lock()
	live := l.sessions[session] != nil
	l.mu.Unlock()
	if !live { // the client is gone (its subscription ended with it) or not linked yet
		viewStats.orphans.Add(1)
		return
	}
	p := encodeServerView(f, time.Now().UnixMicro())
	if err := l.Send(session, protoPrometheus, msgPrometheusServerView, p); err != nil {
		if viewStats.errs.Add(1) == 1 {
			l.Log.Warn("server view: relay failed", "session", session, "conn", f.Conn, "err", err)
		}
	} else {
		viewStats.frames.Add(1)
		viewStats.bytes.Add(uint64(len(p)))
	}
	now := time.Now().UnixNano()
	if last := viewStats.lastLog.Load(); now-last >= int64(viewLogEvery) && viewStats.lastLog.CompareAndSwap(last, now) {
		frames := viewStats.frames.Load()
		n := frames - viewStats.loggedFrames.Swap(frames)
		secs := float64(now-last) / 1e9
		if last == 0 {
			secs = 0
		}
		l.Log.Info("server view relay", "session", session, "conn", f.Conn, "seq", f.Seq, "frame", f.Frame,
			"frames", frames, "rate", fmt.Sprintf("%.1f/s", float64(n)/max(secs, 1)), "bytes", viewStats.bytes.Load(),
			"last_size", len(p), "errors", viewStats.errs.Load(), "orphans", viewStats.orphans.Load())
	}
}

// Handshake: !serverview on first calls the Prometheus tool server_view {"action":"hello"}; only a DLL
// that knows 32002 answers ok with "message": 32002 (an older one would drop the lobby link on 32002).
var viewHandshakes sync.Map // PrometheusCall seq -> lobby session

// onServerViewHello handles the answer to a handshake call; false = r was not one.
func onServerViewHello(c *Ctx, r prometheusResult) (bool, error) {
	v, ok := viewHandshakes.LoadAndDelete(r.Seq)
	if !ok {
		return false, nil
	}
	session := v.(uint64)
	var res struct {
		Message int `json:"message"`
	}
	json.Unmarshal([]byte(r.Result), &res)
	if !r.OK || res.Message != msgPrometheusServerView {
		c.Log.Warn("server view: Prometheus can't take it", "ok", r.OK, "result", r.Result)
		return true, c.SystemChat("server view: this Prometheus has no server view (rebuild it); nothing is streamed")
	}
	in := c.logic.Instances
	if in == nil {
		return true, c.SystemChat("server view: no game server link")
	}
	told := in.SetView(session, true)
	c.Log.Info("server view on", "session", session, "conns", told)
	return true, c.SystemChat(fmt.Sprintf("server view on (connections now: %v; later matches follow)", told))
}

func init() {
	commands["serverview"] = command{`!serverview on|off|status - stream the game server's view of your match every tick to Prometheus (docs/DebugDrawing.md §5)`, func(c *Ctx, a []string) error {
		in := c.logic.Instances
		if in == nil {
			return fmt.Errorf("no game server link")
		}
		arg := "status"
		if len(a) > 0 {
			arg = a[0]
		}
		switch arg {
		case "on":
			seq, err := c.PrometheusCall("server_view", map[string]any{"action": "hello"})
			if err != nil {
				return err
			}
			viewHandshakes.Store(seq, c.Session.ID)
			return c.SystemChat(fmt.Sprintf("server view: asking Prometheus (#%d)", seq))
		case "off":
			told := in.SetView(c.Session.ID, false)
			c.Log.Info("server view off", "session", c.Session.ID, "conns", told)
			return c.SystemChat(fmt.Sprintf("server view off (connections told: %v)", told))
		case "status":
			return c.SystemChat(fmt.Sprintf("server view: %v; relayed %d frames, %d bytes, %d errors", map[bool]string{true: "on", false: "off"}[in.Viewing(c.Session.ID)],
				viewStats.frames.Load(), viewStats.bytes.Load(), viewStats.errs.Load()))
		}
		return fmt.Errorf("usage: !serverview on|off|status")
	}}
}
