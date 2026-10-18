package lobbylogic

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"sync"
	"time"

	"owserver/internal/gamelink"
)

// Instances is lobbylogic's end of the game server link (see package gamelink):
// game servers dial in, and lobbylogic assigns them the clients it sends to a
// game. Several game servers can be connected at once, told apart by the name in
// their hello ("gameserver", the R&D server, when empty; "instance", the new
// server); new matches go to the active one (Active), releases to the server that
// holds the assignment. A new connection under a name replaces the old one.
//
// Availability: the active game server is "available" while it is linked.
// OnAvailable hears every change, debounced: going online is reported at once,
// going offline only after Debounce without the server (a rebuild restarts it
// quicker), and the first report comes Debounce after Serve starts unless a server
// links earlier (a restarted lobbylogic waits for the servers to redial).
type Instances struct {
	Log *slog.Logger
	// Active is the game server that gets new matches (default gamelink.DefaultServer).
	Active string
	// SaveFile, if set, is where SetActive saves the choice (LoadActiveServer reads it).
	SaveFile string
	// OnAvailable is called (on its own goroutine, one call at a time) when the
	// active game server's availability changes; server is its name.
	OnAvailable func(available bool, server string)
	// Debounce defaults to 2 s.
	Debounce time.Duration
	// OnFrame gets every live server view frame (DebugDrawing.md §5) with the lobby
	// session of its connection, from the game server's link goroutine.
	OnFrame func(session uint64, f *gamelink.ViewFrame)

	mu       sync.Mutex
	servers  map[string]*gameServer
	assigned map[uint32]assignment
	nextConn uint32

	views map[uint64]bool // lobby sessions subscribed to the live server view

	reported  int // availability last reported: availUnknown / availOff / availOn
	timer     *time.Timer
	notifyMu  sync.Mutex // one OnAvailable call at a time, in order
	notifyGen uint64
}

const (
	availUnknown = iota
	availOff
	availOn
)

type gameServer struct {
	name  string
	nc    net.Conn
	enc   *json.Encoder
	addr  string // its advertised UDP address
	hello gamelink.Wire
}

type assignment struct {
	gamelink.Assignment
	server string
}

// Serve accepts game servers on addr until ctx is cancelled.
func (in *Instances) Serve(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	context.AfterFunc(ctx, func() { ln.Close() })
	in.Log.Info("listening for game servers", "addr", addr, "active", in.active())
	in.mu.Lock()
	if in.reported == availUnknown && in.timer == nil {
		in.timer = time.AfterFunc(in.debounce(), in.settle)
	}
	in.mu.Unlock()
	for {
		nc, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go in.serve(nc)
	}
}

func (in *Instances) active() string {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.activeLocked()
}

func (in *Instances) activeLocked() string {
	if in.Active == "" {
		return gamelink.DefaultServer
	}
	return in.Active
}

func (in *Instances) serve(nc net.Conn) {
	defer nc.Close()
	sc := bufio.NewScanner(nc)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	if !sc.Scan() {
		return
	}
	var hello gamelink.Wire
	if err := json.Unmarshal(sc.Bytes(), &hello); err != nil || hello.Type != gamelink.WireHello {
		in.Log.Error("game server: expected hello", "err", err, "type", hello.Type)
		return
	}
	name := hello.Name
	if name == "" {
		name = gamelink.DefaultServer
	}
	gs := &gameServer{name: name, nc: nc, enc: json.NewEncoder(nc), addr: hello.Addr, hello: hello}

	in.mu.Lock()
	if in.servers == nil {
		in.servers = map[string]*gameServer{}
	}
	if in.assigned == nil {
		in.assigned = map[uint32]assignment{}
	}
	if old := in.servers[name]; old != nil {
		in.Log.Warn("game server reconnected; dropping its old link", "server", name)
		old.nc.Close()
	}
	in.servers[name] = gs
	// Learn the game server's assignments (lobbylogic restarted) and replay ours
	// (the game server restarted).
	known := map[uint32]bool{}
	for _, a := range hello.Conns {
		known[a.ConnID] = true
		if _, ok := in.assigned[a.ConnID]; !ok {
			in.assigned[a.ConnID] = assignment{a, name}
		}
		in.nextConn = max(in.nextConn, a.ConnID)
	}
	for id, a := range in.assigned {
		if a.server == name && !known[id] {
			gs.enc.Encode(gamelink.Wire{Type: gamelink.WireAssign, Conn: &a.Assignment})
		}
		if a.server == name && in.views[a.Session] && hello.View {
			gs.enc.Encode(gamelink.Wire{Type: gamelink.WireView, Conn: &gamelink.Assignment{ConnID: id}, View: true})
		}
	}
	active := in.activeLocked()
	in.changedLocked()
	in.mu.Unlock()
	in.Log.Info("game server connected", "server", name, "addr", hello.Addr, "its_conns", len(hello.Conns), "active", name == active,
		"ping_ms", hello.Ping, "modes", len(hello.Modes), "maps", len(hello.Maps))

	// A server that pings is dropped after 3 intervals of silence (hung, not only gone).
	timeout := time.Duration(hello.Ping) * time.Millisecond * 3
	why := "link closed"
	for {
		if timeout > 0 {
			nc.SetReadDeadline(time.Now().Add(timeout))
		}
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				why = err.Error()
			}
			break
		}
		var m gamelink.Wire
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			in.Log.Warn("game server: bad message", "server", name, "err", err)
			continue
		}
		switch m.Type {
		case gamelink.WirePing:
			in.mu.Lock()
			gs.enc.Encode(gamelink.Wire{Type: gamelink.WirePong})
			in.mu.Unlock()
		case gamelink.WireFrame:
			if m.Frame == nil {
				continue
			}
			in.mu.Lock()
			a, ok := in.assigned[m.Frame.Conn]
			in.mu.Unlock()
			if ok && in.OnFrame != nil {
				in.OnFrame(a.Session, m.Frame)
			}
		default:
			in.Log.Warn("game server: unknown message", "server", name, "type", m.Type)
		}
	}
	in.mu.Lock()
	if in.servers[name] == gs {
		delete(in.servers, name)
		in.Log.Warn("game server disconnected", "server", name, "why", why)
		in.changedLocked()
	}
	in.mu.Unlock()
}

func (in *Instances) debounce() time.Duration {
	if in.Debounce > 0 {
		return in.Debounce
	}
	return 2 * time.Second
}

// Available reports whether the active game server is linked, as last reported to
// OnAvailable; known is false until the first report.
func (in *Instances) Available() (available, known bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.reported == availOn, in.reported != availUnknown
}

// changedLocked re-evaluates the availability after a link or the active server
// changed: online is reported at once, offline after the debounce.
func (in *Instances) changedLocked() {
	linked := in.servers[in.activeLocked()] != nil
	switch {
	case linked && in.reported != availOn:
		if in.timer != nil {
			in.timer.Stop()
			in.timer = nil
		}
		in.reportLocked(true)
	case linked:
		// Still on: a pending offline report is void.
		if in.timer != nil {
			in.timer.Stop()
			in.timer = nil
		}
	case in.reported == availOn && in.timer == nil:
		in.timer = time.AfterFunc(in.debounce(), in.settle)
	}
}

// settle runs when a debounce ends: it reports the availability if it changed.
func (in *Instances) settle() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.timer = nil
	linked := in.servers[in.activeLocked()] != nil
	if want := map[bool]int{true: availOn, false: availOff}[linked]; want != in.reported {
		in.reportLocked(linked)
	}
}

func (in *Instances) reportLocked(on bool) {
	in.reported = map[bool]int{true: availOn, false: availOff}[on]
	server := in.activeLocked()
	in.notifyGen++
	gen := in.notifyGen
	in.Log.Info("game server availability", "server", server, "available", on)
	if in.OnAvailable == nil {
		return
	}
	go func() {
		in.notifyMu.Lock()
		defer in.notifyMu.Unlock()
		in.mu.Lock()
		stale := gen != in.notifyGen
		in.mu.Unlock()
		if !stale { // a newer report supersedes this one
			in.OnAvailable(on, server)
		}
	}()
}

// Assign hands a new connection to the active game server and returns it with the
// UDP address to put in 20600.
func (in *Instances) Assign(session uint64, name string, mapID uint64) (gamelink.Assignment, string, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	server := in.activeLocked()
	gs := in.servers[server]
	if gs == nil {
		return gamelink.Assignment{}, "", fmt.Errorf("game server %q is not connected (make games starts it; !gameserver switches)", server)
	}
	if !gs.hello.ServesMap(mapID) {
		return gamelink.Assignment{}, "", fmt.Errorf("game server %q does not serve map %#x", server, mapID)
	}
	in.nextConn++
	a := gamelink.Assignment{ConnID: in.nextConn, Key: make([]byte, 20), Session: session, Name: name, Map: mapID}
	rand.Read(a.Key)
	if err := gs.enc.Encode(gamelink.Wire{Type: gamelink.WireAssign, Conn: &a}); err != nil {
		return gamelink.Assignment{}, "", err
	}
	in.assigned[a.ConnID] = assignment{a, server}
	if in.views[session] && gs.hello.View {
		gs.enc.Encode(gamelink.Wire{Type: gamelink.WireView, Conn: &gamelink.Assignment{ConnID: a.ConnID}, View: true})
	}
	return a, gs.addr, nil
}

// SetView subscribes a lobby session to the live server view of its game server
// connections (on), or ends it. The subscription outlives matches: every later
// assignment of the session and every game server reconnect gets it again. It
// returns the connections told now (the ones on a linked game server that streams).
func (in *Instances) SetView(session uint64, on bool) []uint32 {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.views == nil {
		in.views = map[uint64]bool{}
	}
	if on {
		in.views[session] = true
	} else {
		delete(in.views, session)
	}
	var told []uint32
	for id, a := range in.assigned {
		if a.Session != session {
			continue
		}
		if gs := in.servers[a.server]; gs != nil && gs.hello.View {
			if err := gs.enc.Encode(gamelink.Wire{Type: gamelink.WireView, Conn: &gamelink.Assignment{ConnID: id}, View: on}); err == nil {
				told = append(told, id)
			}
		}
	}
	sort.Slice(told, func(i, j int) bool { return told[i] < told[j] })
	return told
}

// Viewing reports whether a lobby session is subscribed to the live server view.
func (in *Instances) Viewing(session uint64) bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.views[session]
}

// ReleaseSession ends every game server connection handed to a lobby session
// (the player left the game): the game server holding it closes it with a FIN. It
// returns how many there were.
func (in *Instances) ReleaseSession(session uint64) (int, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	n := 0
	var errs []error
	for id, a := range in.assigned {
		if a.Session != session {
			continue
		}
		delete(in.assigned, id)
		n++
		if gs := in.servers[a.server]; gs != nil {
			if err := gs.enc.Encode(gamelink.Wire{Type: gamelink.WireRelease, Conn: &a.Assignment}); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return n, errors.Join(errs...)
}

// SetActive picks the game server that gets new matches. It need not be
// connected yet.
func (in *Instances) SetActive(name string) {
	in.mu.Lock()
	in.Active = name
	in.changedLocked()
	in.mu.Unlock()
	in.Log.Info("active game server", "server", name)
	if in.SaveFile != "" {
		if err := saveActiveServer(in.SaveFile, name); err != nil {
			in.Log.Error("saving the active game server", "file", in.SaveFile, "err", err)
		}
	}
}

// ServerStatus is a game server as !gameserver lists it.
type ServerStatus struct {
	Name      string
	Addr      string
	Connected bool
	Active    bool
	Conns     int
	Modes     []string // from its hello (empty: all)
	Maps      int      // maps in its hello (0: all)
	Heartbeat bool     // it pings
}

// Servers lists the connected game servers and the active one.
func (in *Instances) Servers() []ServerStatus {
	in.mu.Lock()
	defer in.mu.Unlock()
	active := in.activeLocked()
	byName := map[string]*ServerStatus{}
	for name, gs := range in.servers {
		byName[name] = &ServerStatus{Name: name, Addr: gs.addr, Connected: true, Modes: gs.hello.Modes, Maps: len(gs.hello.Maps), Heartbeat: gs.hello.Ping > 0}
	}
	if byName[active] == nil {
		byName[active] = &ServerStatus{Name: active}
	}
	byName[active].Active = true
	for _, a := range in.assigned {
		if s := byName[a.server]; s != nil {
			s.Conns++
		}
	}
	out := make([]ServerStatus, 0, len(byName))
	for _, s := range byName {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
