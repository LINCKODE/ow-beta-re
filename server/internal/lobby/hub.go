package lobby

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Hub routes lobby messages between client links, Lua handlers and the logic
// backend, and keeps a ring buffer of recent traffic for the control API.
type Hub struct {
	log *slog.Logger
	lua *LuaVM

	mu      sync.Mutex
	nextID  uint64
	links   map[uint64]*link
	backend *backendConn

	traffic trafficRing

	// Quiet lists message ids that are not logged one line per message (high-rate streams such as
	// PrometheusServerView 32002, 62.5/s): they go to the traffic ring as usual, and the log gets
	// one summary line per id every quietEvery. Set before the hub is used.
	Quiet   map[uint16]bool
	quietMu sync.Mutex
	quiet   map[uint16]*quietCount
}

type quietCount struct {
	n, bytes int
	since    time.Time
}

const quietEvery = 10 * time.Second

func NewHub(log *slog.Logger) *Hub {
	h := &Hub{log: log, links: map[uint64]*link{}, quiet: map[uint16]*quietCount{}}
	h.traffic.entries = make([]TrafficEntry, trafficSize)
	return h
}

// logMsg writes the per-message log line, or counts a quiet message and logs a summary every
// quietEvery.
func (h *Hub) logMsg(log *slog.Logger, what, via, protocol string, msgID uint16, payload []byte) {
	if !h.Quiet[msgID] {
		if via != "" {
			log.Info(what, "via", via, "protocol", protocol, "msg", msgID, "bytes", len(payload), "data", hex.EncodeToString(payload))
		} else {
			log.Info(what, "protocol", protocol, "msg", msgID, "bytes", len(payload), "data", hex.EncodeToString(payload))
		}
		return
	}
	h.quietMu.Lock()
	c := h.quiet[msgID]
	now := time.Now()
	if c == nil {
		c = &quietCount{since: now}
		h.quiet[msgID] = c
	}
	c.n++
	c.bytes += len(payload)
	var n, bytes int
	var span time.Duration
	if now.Sub(c.since) >= quietEvery {
		n, bytes, span = c.n, c.bytes, now.Sub(c.since)
		*c = quietCount{since: now}
	}
	h.quietMu.Unlock()
	if n > 0 {
		log.Info(what+" (quiet summary)", "protocol", protocol, "msg", msgID, "messages", n, "bytes", bytes,
			"per_second", fmt.Sprintf("%.1f", float64(n)/span.Seconds()))
	}
}

func (h *Hub) add(l *link) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	l.id = h.nextID
	h.links[l.id] = l
}

func (h *Hub) remove(l *link) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.links, l.id)
	if h.backend != nil && l.info().Negotiated {
		h.backend.enqueue(WireMessage{Type: WireClose, Session: l.id})
	}
}

// linkUp is called once the client's protocol list is accepted.
func (h *Hub) linkUp(l *link) {
	info := l.info()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.backend != nil {
		h.backend.enqueue(WireMessage{Type: WireOpen, Session: l.id, Info: &info})
	} else {
		h.log.Warn("no logic backend connected; start lobbylogic", "session", l.id)
	}
}

// Sessions lists the connected clients, oldest first.
func (h *Hub) Sessions() []SessionInfo {
	h.mu.Lock()
	links := make([]*link, 0, len(h.links))
	for _, l := range h.links {
		links = append(links, l)
	}
	h.mu.Unlock()
	sort.Slice(links, func(i, j int) bool { return links[i].id < links[j].id })
	out := make([]SessionInfo, len(links))
	for i, l := range links {
		out[i] = l.info()
	}
	return out
}

// dispatch handles one client message: Lua handlers first (a handler returning
// true consumes it), then the logic backend.
func (h *Hub) dispatch(l *link, proto *Protocol, msgID uint16, payload []byte) {
	h.traffic.add(TrafficEntry{Session: l.id, Dir: "in", Protocol: proto.Name, Msg: msgID, Payload: payload})
	h.logMsg(l.log, "recv", "", proto.Name, msgID, payload)

	if h.lua != nil && h.lua.handle(l.id, proto.Name, msgID, payload) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.backend == nil {
		l.log.Warn("no logic backend connected; message dropped", "msg", msgID)
		return
	}
	h.backend.enqueue(WireMessage{Type: WireRecv, Session: l.id, Protocol: proto.Name, Msg: msgID, Payload: payload})
}

// Send sends a message to a client. session 0 means the newest negotiated
// session. via names the sender for the traffic log (logic, lua, http).
func (h *Hub) Send(session uint64, protocol string, msgID uint16, payload []byte, via string) error {
	l, err := h.link(session)
	if err != nil {
		return err
	}
	if protocol == "" {
		p := ProtocolByMsg(msgID)
		if p == nil {
			return fmt.Errorf("no protocol contains message %d", msgID)
		}
		protocol = p.Name
	}
	if err := l.send(protocol, msgID, payload); err != nil {
		return err
	}
	h.traffic.add(TrafficEntry{Session: l.id, Dir: "out", Via: via, Protocol: protocol, Msg: msgID, Payload: payload})
	h.logMsg(l.log, "send", via, protocol, msgID, payload)
	return nil
}

// Inject hands a message to the logic backend as if the client had sent it
// (session 0 = newest negotiated session). It bypasses the Lua handlers, so
// it's safe to call from Lua. Use it to drive lobbylogic without the UI, e.g.
// a ClientOutLobbyChat 21700 line "!instance" to send the client into a game.
func (h *Hub) Inject(session uint64, msgID uint16, payload []byte) error {
	l, err := h.link(session)
	if err != nil {
		return err
	}
	proto := ProtocolByMsg(msgID)
	if proto == nil {
		return fmt.Errorf("no protocol contains message %d", msgID)
	}
	h.traffic.add(TrafficEntry{Session: l.id, Dir: "in", Via: "inject", Protocol: proto.Name, Msg: msgID, Payload: payload})
	l.log.Info("inject", "protocol", proto.Name, "msg", msgID, "bytes", len(payload), "data", hex.EncodeToString(payload))
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.backend == nil {
		return errors.New("no logic backend connected")
	}
	h.backend.enqueue(WireMessage{Type: WireRecv, Session: l.id, Protocol: proto.Name, Msg: msgID, Payload: payload})
	return nil
}

// Disconnect closes a client's connection.
func (h *Hub) Disconnect(session uint64) error {
	l, err := h.link(session)
	if err != nil {
		return err
	}
	return l.conn.Close()
}

func (h *Hub) link(session uint64) (*link, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if session != 0 {
		if l := h.links[session]; l != nil {
			return l, nil
		}
		return nil, fmt.Errorf("no session %d", session)
	}
	var newest *link
	for _, l := range h.links {
		if (newest == nil || l.id > newest.id) && l.info().Negotiated {
			newest = l
		}
	}
	if newest == nil {
		return nil, fmt.Errorf("no negotiated session")
	}
	return newest, nil
}

// Traffic returns up to n entries with Seq > since (n <= 0: all), optionally
// only those of one session.
func (h *Hub) Traffic(since uint64, n int, session uint64) []TrafficEntry {
	return h.traffic.since(since, n, session)
}

const trafficSize = 2000

type TrafficEntry struct {
	Seq      uint64    `json:"seq"`
	Time     time.Time `json:"time"`
	Session  uint64    `json:"session"`
	Dir      string    `json:"dir"`           // in = client -> server, out = server -> client
	Via      string    `json:"via,omitempty"` // who sent an out message: logic, lua, http
	Protocol string    `json:"protocol"`
	Msg      uint16    `json:"msg"`
	Payload  HexBytes  `json:"payload"`
}

// HexBytes marshals as a hex string.
type HexBytes []byte

func (b HexBytes) MarshalJSON() ([]byte, error) {
	return []byte(`"` + hex.EncodeToString(b) + `"`), nil
}

type trafficRing struct {
	mu      sync.Mutex
	seq     uint64
	entries []TrafficEntry // entries[seq % len]
}

func (r *trafficRing) add(e TrafficEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	e.Seq, e.Time = r.seq, time.Now()
	r.entries[r.seq%uint64(len(r.entries))] = e
}

func (r *trafficRing) since(since uint64, n int, session uint64) []TrafficEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	oldest := uint64(1)
	if r.seq > uint64(len(r.entries)) {
		oldest = r.seq - uint64(len(r.entries)) + 1
	}
	out := []TrafficEntry{}
	for s := max(since+1, oldest); s <= r.seq; s++ {
		e := r.entries[s%uint64(len(r.entries))]
		if session == 0 || e.Session == session {
			out = append(out, e)
		}
	}
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}
