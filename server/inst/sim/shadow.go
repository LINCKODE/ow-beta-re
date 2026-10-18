package sim

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"owserver/inst/replication"
	"owserver/internal/heroes"
)

// Shadow mode (T052, docs/StatescriptSync.md §7): the runtime simulates the hero as with --sim but its deltas
// are not sent, so the client keeps predicting on its own (and undoes each prediction 40 CF later). The host
// keeps a snapshot of the hero's Statescript per simulated frame, for comparing with the client's own state at
// the same command frame (server/cmd/shadowcmp).

// Shadower is a Host that keeps per-frame snapshots (Runtime with Ring > 0).
type Shadower interface {
	ShadowState(body, cf uint32) (*ShadowSnapshot, error)
	ShadowReset(body uint32) (uint32, error)
}

// ShadowSnapshot is a hero's Statescript at the end of a command frame, in wire terms.
type ShadowSnapshot struct {
	Body      uint32               `json:"body"`
	CF        uint32               `json:"cf"`
	Newest    uint32               `json:"newest"` // the newest simulated frame
	Owner     map[string]ShadowVar `json:"owner"`
	Instances []ShadowInstance     `json:"instances"`
}

type ShadowVar struct {
	Type int    `json:"type"` // heroes.Type*
	Bits uint64 `json:"bits"`
}

type ShadowState struct {
	N   int    `json:"n"`
	Hex string `json:"hex"` // LSB first
	// The same data written with the clock at the end of the step window (CF*16 + 15), as the client's own writer
	// runs when Prometheus ss_serialize_local captures it (docs/StatescriptSync.md §7.1): only when it differs.
	CapN   int    `json:"cap_n,omitempty"`
	CapHex string `json:"cap_hex,omitempty"`
}

type ShadowEvent struct {
	Kind  int   `json:"kind"`
	State int   `json:"state"`
	Data  int32 `json:"data"`
	Time  int64 `json:"time"`
}

type ShadowInstance struct {
	ID     uint16               `json:"id"`
	Graph  uint16               `json:"graph"`
	Parent uint16               `json:"parent"`
	Slot   uint32               `json:"slot"`
	Kinds  string               `json:"kinds"`            // per m_states: '.' synced, 'C' client-only, 'V' server-only, 'S' has update
	Frozen []int                `json:"frozen,omitempty"` // m_states the server doesn't correct (policy)
	Vars   map[string]ShadowVar `json:"vars"`
	States map[int]ShadowState  `json:"states"` // active states and their local-format data
	Events []ShadowEvent        `json:"events"`
}

// shadowFrame is a kept snapshot plus the capture-clock variant of its state data.
type shadowFrame struct {
	s   *replication.ScriptState
	cap map[[2]int]replication.StateData // (instance, state) -> data at CF*16 + 15, where it differs
}

func shadowJSON(body, cf, newest uint32, f shadowFrame) *ShadowSnapshot {
	s := f.s
	vars := func(m map[uint32]heroes.Value) map[string]ShadowVar {
		out := map[string]ShadowVar{}
		for id, v := range m {
			out[fmt.Sprintf("%04x", id&0xffff)] = ShadowVar{Type: int(v.Type), Bits: v.Bits}
		}
		return out
	}
	out := &ShadowSnapshot{Body: body, CF: cf, Newest: newest, Owner: vars(s.Owner)}
	for _, in := range s.Instances {
		si := ShadowInstance{ID: in.ID, Graph: in.Layout.Index, Parent: in.Parent, Slot: in.Slot, Kinds: in.Layout.States,
			Vars: vars(in.Vars), States: map[int]ShadowState{}}
		for st, d := range in.States {
			ss := ShadowState{N: d.N, Hex: hex.EncodeToString(d.Bits)}
			if c, ok := f.cap[[2]int{int(in.ID), st}]; ok {
				ss.CapN, ss.CapHex = c.N, hex.EncodeToString(c.Bits)
			}
			si.States[st] = ss
		}
		for st := range in.Layout.Frozen {
			si.Frozen = append(si.Frozen, st)
		}
		sort.Ints(si.Frozen)
		for _, e := range in.Events {
			si.Events = append(si.Events, ShadowEvent{e.Kind, e.State, e.Data, e.Time})
		}
		out.Instances = append(out.Instances, si)
	}
	sort.Slice(out.Instances, func(i, j int) bool { return out.Instances[i].ID < out.Instances[j].ID })
	return out
}

func (r *Runtime) keepSnapshot(h *hero, f uint32) {
	if r.Ring <= 0 {
		return
	}
	if h.snaps == nil {
		h.snaps = map[uint32]shadowFrame{}
	}
	fr := shadowFrame{s: h.snapshot()}
	// The capture-clock variant: the client's writer runs at CF*16 + 15 (§7.1); only the clock differs.
	t := h.sm.Time()
	h.sm.SetClock(f, int64(f)*16+15)
	for _, in := range fr.s.Instances {
		for st, d := range in.States {
			b, n, err := h.sm.StateDataFormat(int16(in.ID), st, true, false)
			if err == nil && (n != d.N || !(replication.StateData{Bits: b, N: n}).Equal(d)) {
				if fr.cap == nil {
					fr.cap = map[[2]int]replication.StateData{}
				}
				fr.cap[[2]int{int(in.ID), st}] = replication.StateData{Bits: b, N: n}
			}
		}
	}
	h.sm.SetClock(f, t)
	h.snaps[f] = fr
	delete(h.snaps, f-uint32(r.Ring))
}

// ShadowState returns body's snapshot at the end of command frame cf (0: the newest).
func (r *Runtime) ShadowState(body, cf uint32) (*ShadowSnapshot, error) {
	h := r.heroes[body]
	if h == nil {
		return nil, errors.New("no such hero")
	}
	if cf == 0 {
		cf = h.frame
	}
	s, ok := h.snaps[cf]
	if !ok {
		return nil, fmt.Errorf("no snapshot for frame %d (newest %d, ring %d)", cf, h.frame, r.Ring)
	}
	return shadowJSON(body, cf, h.frame, s), nil
}

// ShadowReset re-seeds body from its start-up at its current frame: the state the client returns to once its
// prediction timeout has undone everything it predicted since the lock-in full update (the timeout rolls back to
// the last server state and keeps the clock at the current frame, docs/StatescriptSync.md §5.5).
func (r *Runtime) ShadowReset(body uint32) (uint32, error) {
	h := r.heroes[body]
	if h == nil {
		return 0, errors.New("no such hero")
	}
	st, ok := heroes.Start(h.def)
	if !ok {
		return 0, errors.New("no start-up")
	}
	nh, err := r.seed(body, h.def, h.frame, st)
	if err != nil {
		return 0, err
	}
	nh.last, nh.inputs = h.last, h.inputs
	h.sm.Close()
	r.heroes[body] = nh
	r.keepSnapshot(nh, nh.frame)
	return nh.frame, nil
}
