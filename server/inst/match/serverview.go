package match

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"owstatescript/statescript"

	"owserver/inst/jam"
	"owserver/inst/sim"
	"owserver/inst/world"
	"owserver/internal/gamelink"
	"owserver/internal/heroes"
)

// Live server view (docs/DebugDrawing.md §5, T072): every tick, for each subscribed
// connection, the server's view of its match as one small JSON frame, which lobbylogic
// relays to the client's Prometheus (PrometheusServerView 32002). Off until lobbylogic
// subscribes a connection (!serverview on).

// ViewFullEvery is how often a frame lists every world entity (and the map name);
// the frames in between list only the entities that can move.
const ViewFullEvery = 64

// ViewBodyCap is the largest body sent; a bigger one is replaced by an error body.
const ViewBodyCap = 8 * 1024

type viewSub struct {
	seq   uint32
	count uint32 // frames built
}

// transformRecord is the last PlayerTransform 21601 of a connection and the frame it arrived at.
type transformRecord struct {
	msg   jam.PlayerTransform
	frame uint32
}

// SetView subscribes (on) or unsubscribes a connection. The connection need not have a
// player yet: the subscription waits for it.
func (m *Manager) SetView(conn uint32, on bool) {
	if m.views == nil {
		m.views = map[uint32]*viewSub{}
	}
	if !on {
		delete(m.views, conn)
		m.log.Info("server view off", "conn", conn)
		return
	}
	if m.views[conn] == nil {
		m.views[conn] = &viewSub{}
		m.log.Info("server view on", "conn", conn)
	}
}

// Note sets a free-form debug line that the live server view shows on the client's screen
// (the body's txt lines, under the server's own), for conn's frames (0: every connection's).
// Any server code can call it on the game loop; text "" removes the line. Lines are
// ordered by key and stay until changed.
func (m *Manager) Note(conn uint32, key, text string) {
	if m.notes == nil {
		m.notes = map[uint32]map[string]string{}
	}
	if text == "" {
		delete(m.notes[conn], key)
		return
	}
	if m.notes[conn] == nil {
		m.notes[conn] = map[string]string{}
	}
	m.notes[conn][key] = text
}

// noteLines are the Note lines for conn's frames: the global ones, then conn's own.
func (m *Manager) noteLines(conn uint32) []string {
	var out []string
	for i, c := range []uint32{0, conn} {
		if i == 1 && conn == 0 {
			break
		}
		keys := make([]string, 0, len(m.notes[c]))
		for k := range m.notes[c] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, k+": "+m.notes[c][k])
		}
	}
	return out
}

// ViewStats is the stream's state for the control API.
type ViewStats struct {
	Conns   []uint32 `json:"conns"`
	Frames  uint64   `json:"frames"`
	Dropped uint64   `json:"dropped"` // the sink refused (link down / queue full)
	Bytes   uint64   `json:"bytes"`   // body bytes built
	Last    int      `json:"last_size"`
	Max     int      `json:"max_size"`
}

// ViewStats reports the subscriptions and counters.
func (m *Manager) ViewStats() ViewStats {
	s := m.viewStats
	s.Conns = make([]uint32, 0, len(m.views))
	for c := range m.views {
		s.Conns = append(s.Conns, c)
	}
	return s
}

// ViewTick builds this tick's frame for every subscribed connection that has a player
// and hands it to sink (non-blocking; false = dropped). late is the loop's late ticks.
func (m *Manager) ViewTick(now time.Time, late int, sink func(*gamelink.ViewFrame) bool) {
	for conn, sub := range m.views {
		p := m.players[conn]
		if p == nil || p.match == nil || p.State == Assigned { // no client on this connection (yet, or any more)
			continue
		}
		body := m.viewBody(p, sub, now, late)
		b, err := json.Marshal(body)
		if err != nil || len(b) > ViewBodyCap {
			b, _ = json.Marshal(map[string]any{"v": 1, "error": "too big", "size": len(b)})
		}
		sub.seq++
		sub.count++
		f := &gamelink.ViewFrame{Conn: conn, Seq: sub.seq, Frame: p.match.Frame, SentUS: time.Now().UnixMicro(), Body: b}
		m.viewStats.Frames++
		m.viewStats.Bytes += uint64(len(b))
		m.viewStats.Last = len(b)
		m.viewStats.Max = max(m.viewStats.Max, len(b))
		if sink == nil || !sink(f) {
			m.viewStats.Dropped++
		}
	}
}

// The body (DebugDrawing.md §5.2). Short keys: it goes out 62.5 times a second.
type viewBody struct {
	V    int       `json:"v"`
	MS   int64     `json:"ms"`
	Late int       `json:"late"`
	Clk  viewClock `json:"clk"`
	Game *viewGame `json:"game,omitempty"`
	ECS  int       `json:"ecs"`
	Full bool      `json:"full,omitempty"`
	Map  string    `json:"map,omitempty"`
	Ents []viewEnt `json:"ents"`
	Me   *viewPos  `json:"me,omitempty"`
	XF   *viewXF   `json:"xf,omitempty"`
	// Txt is the server's debug text for the client's screen (Prometheus draws it with the engine's debug
	// font): its own summary lines, then the Note lines.
	Txt []string `json:"txt"`
	// ECS and Statescript facts.
	Events uint64      `json:"ev"`              // ECS events drained so far
	Comps  string      `json:"comps,omitempty"` // the hero's ECS components (hex)
	SS     *viewSS     `json:"ss,omitempty"`    // the hero's Statescript simulation
	Rules  []viewRules `json:"rules,omitempty"` // the rules host's entities (brain, objective copies)
}

// viewSS is a sync manager's summary (+ the hero simulation's counters).
type viewSS struct {
	Inst   int      `json:"inst"`
	Active int      `json:"act"`
	Graphs []string `json:"g"` // "graph:active" of the instances with active states (graph = GUID low 12 bits, hex)
	Frame  uint32   `json:"f,omitempty"`
	Sent   uint32   `json:"sent,omitempty"`
	Acked  uint32   `json:"ack,omitempty"`
	Deltas int      `json:"d,omitempty"`
	Skip   int      `json:"skip,omitempty"`
	InErr  int      `json:"inerr,omitempty"`
	Lost   int      `json:"lost,omitempty"`
}

type viewRules struct {
	ID   uint32 `json:"id"`
	Kind string `json:"k"`
	viewSS
}

// ssSummary reads a sync manager's instances and active states.
func ssSummary(sm *statescript.SyncManager) viewSS {
	var v viewSS
	if sm == nil {
		return v
	}
	for _, i := range sm.Instances() {
		v.Inst++
		n := len(sm.ActiveStates(i))
		v.Active += n
		if n > 0 {
			info, _ := sm.InstanceInfo(i)
			v.Graphs = append(v.Graphs, fmt.Sprintf("%03x:%d", info.Graph&0xfff, n))
		}
	}
	return v
}

type viewClock struct {
	State  PlayerState `json:"st"`
	Input  uint32      `json:"in"`
	Lead   int32       `json:"lead"`
	Min    int32       `json:"min"`
	Flags  uint8       `json:"fl"`
	Resync int         `json:"rs"`
	Timing uint16      `json:"tm"`
}

type viewGame struct {
	Phase  string      `json:"ph"`
	Frames uint32      `json:"pf"`
	Points []viewPoint `json:"pts"`
}

type viewPoint struct {
	Order     int     `json:"o"`
	On        bool    `json:"on"`
	Owner     int     `json:"own"`
	Capturing int     `json:"cap"`
	Contested bool    `json:"ct,omitempty"`
	Progress  float64 `json:"p"`
}

type viewEnt struct {
	ID    uint32     `json:"id"`
	Kind  string     `json:"k"`
	Def   uint32     `json:"d,omitempty"`
	Team  int        `json:"tm"`
	Owner uint32     `json:"o,omitempty"`
	Pos   [3]float64 `json:"p"`
	Yaw   float64    `json:"y"`
}

type viewPos struct {
	ID  uint32     `json:"id"`
	Pos [3]float64 `json:"p"`
	Yaw float64    `json:"y"`
}

type viewXF struct {
	Pos   [3]float64 `json:"p"`
	Rot   [4]float64 `json:"q"`
	Frame uint32     `json:"f"`
}

func cm(v float32) float64   { return math.Round(float64(v)*100) / 100 }
func mrad(v float32) float64 { return math.Round(float64(v)*1000) / 1000 }
func cm3(v [3]float32) [3]float64 {
	return [3]float64{cm(v[0]), cm(v[1]), cm(v[2])}
}

func (m *Manager) viewBody(p *Player, sub *viewSub, now time.Time, late int) *viewBody {
	mt := p.match
	w := mt.World
	b := &viewBody{V: 1, MS: now.Sub(m.started).Milliseconds(), Late: late, ECS: w.ECSEntities(),
		Clk: viewClock{State: p.State, Input: p.InputAck, Lead: p.steer.Lead, Min: p.steer.MinLead, Flags: p.steer.LastFlags(),
			Resync: p.steer.Resyncs, Timing: p.steer.Timing},
		Ents: []viewEnt{}}
	if g := mt.Game; g != nil {
		vg := &viewGame{Phase: string(g.Phase), Frames: mt.Frame - g.PhaseFrame(), Points: []viewPoint{}}
		for _, pt := range g.Points() {
			vg.Points = append(vg.Points, viewPoint{Order: pt.Order, On: pt.Enabled, Owner: pt.Owner, Capturing: pt.Capturing,
				Contested: pt.Contested, Progress: math.Round(float64(pt.Progress)*1000) / 1000})
		}
		b.Game = vg
	}
	b.Full = sub.count%ViewFullEvery == 0
	if b.Full {
		b.Map = w.Map.Name
	}
	for _, e := range w.Entities() {
		if e.Kind == world.KindController || (!b.Full && e.Kind != world.KindHero) {
			continue
		}
		b.Ents = append(b.Ents, viewEnt{ID: e.ID, Kind: e.KindName, Def: e.Def, Team: e.Team, Owner: e.Owner, Pos: cm3(e.Pos), Yaw: mrad(e.Yaw)})
	}
	if p.Body != 0 {
		if e := w.Get(p.Body); e != nil {
			b.Me = &viewPos{ID: e.ID, Pos: cm3(e.Pos), Yaw: mrad(e.Yaw)}
		}
	}
	b.Events = w.Events
	if p.Body != 0 {
		b.Comps = fmt.Sprintf("% x", w.ECSComponents(p.Body))
		if hs, ok := mt.Sim.(interface {
			HeroView(uint32) (sim.HeroView, bool)
		}); ok {
			if h, ok := hs.HeroView(p.Body); ok {
				v := ssSummary(h.SM)
				v.Frame, v.Sent, v.Acked, v.Deltas, v.Skip, v.InErr, v.Lost = h.Frame, h.SentCF, h.AckedCF, h.Deltas, h.Skipped, h.InputErrs, h.Lost
				b.SS = &v
			}
		}
	}
	if g := mt.Game; g != nil {
		for _, e := range g.Entities() {
			b.Rules = append(b.Rules, viewRules{ID: e.ID, Kind: e.Kind.String(), viewSS: ssSummary(g.SyncManager(e.ID))})
		}
	}
	b.Txt = m.viewText(p, b)
	if x := p.xf; x != nil {
		b.XF = &viewXF{Pos: cm3([3]float32{x.msg.Pos.X, x.msg.Pos.Y, x.msg.Pos.Z}),
			Rot:   [4]float64{mrad(x.msg.Rot.X), mrad(x.msg.Rot.Y), mrad(x.msg.Rot.Z), mrad(x.msg.Rot.W)},
			Frame: x.frame}
	}
	return b
}

// viewText is the server's screen text (DebugDrawing.md §5.2 txt): short lines, one topic each.
func (m *Manager) viewText(p *Player, b *viewBody) []string {
	mt := p.match
	w := mt.World
	t := []string{
		fmt.Sprintf("SERVER %s  CF %d  t %.1f s  late ticks %d", w.Map.Name, mt.Frame, float64(b.MS)/1000, b.Late),
		fmt.Sprintf("conn %d %s  lead %d (min %d)  flags %#x  resyncs %d  timing %d", p.ConnID, p.State, b.Clk.Lead, b.Clk.Min,
			b.Clk.Flags, b.Clk.Resync, b.Clk.Timing),
	}
	if b.Me != nil {
		name := "hero"
		if h, ok := heroes.ByHero(p.Hero); ok {
			name = h.Name
		}
		t = append(t, fmt.Sprintf("%s 0x%08x at %.2f %.2f %.2f  yaw %.0f deg  (server's belief)", name, b.Me.ID,
			b.Me.Pos[0], b.Me.Pos[1], b.Me.Pos[2], b.Me.Yaw*180/math.Pi))
	} else {
		t = append(t, "no hero body yet")
	}
	if g := b.Game; g != nil {
		t = append(t, fmt.Sprintf("game %s  %.0f s", g.Phase, float64(g.Frames)*0.016))
		for _, pt := range g.Points {
			s := fmt.Sprintf("  point %c: owner %d  capturing %d  %.0f%%", 'A'+pt.Order, pt.Owner, pt.Capturing, pt.Progress*100)
			if pt.Contested {
				s += "  contested"
			}
			if !pt.On {
				s += "  (off)"
			}
			t = append(t, s)
		}
	}
	t = append(t, fmt.Sprintf("world %d entities  ECS %d entities, %d events  players %d", len(w.Entities()), b.ECS, b.Events, len(mt.players)))
	if b.Comps != "" {
		t = append(t, "ECS hero components: "+b.Comps)
	}
	if s := b.SS; s != nil {
		t = append(t, fmt.Sprintf("SS hero: %d instances, %d active states  sim CF %d (%+d)  delta sent %d acked %d  deltas %d skipped %d lost %d input errs %d",
			s.Inst, s.Active, s.Frame, int32(s.Frame-mt.Frame), s.Sent, s.Acked, s.Deltas, s.Skip, s.Lost, s.InErr))
		t = append(t, graphLines("SS hero graphs: ", s.Graphs)...)
	}
	if len(b.Rules) > 0 {
		inst, act := 0, 0
		var gs []string
		for _, r := range b.Rules {
			inst += r.Inst
			act += r.Active
			for _, g := range r.Graphs {
				gs = append(gs, fmt.Sprintf("%s/%s", r.Kind[:3], g))
			}
		}
		t = append(t, fmt.Sprintf("SS rules host: %d entities, %d instances, %d active states", len(b.Rules), inst, act))
		t = append(t, graphLines("SS rules graphs: ", gs)...)
	}
	if g := mt.Game; g != nil && len(g.Recent) > 0 {
		for _, r := range g.Recent[max(0, len(g.Recent)-3):] {
			t = append(t, "rules: "+r)
		}
	}
	return append(t, m.noteLines(p.ConnID)...)
}

// graphLines wraps "graph:active" items into lines of at most 10 items.
func graphLines(prefix string, items []string) []string {
	var out []string
	for i := 0; i < len(items); i += 10 {
		out = append(out, prefix+strings.Join(items[i:min(i+10, len(items))], " "))
		prefix = "    "
	}
	return out
}
