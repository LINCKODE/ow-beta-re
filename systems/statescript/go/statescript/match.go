package statescript

/*
#include <stdlib.h>
#include "statescript.h"
*/
import "C"

import (
	"unsafe"
)

// Match is a match world (C API ss_match_*, docs/InstanceServer.md §12.4): the server-run entities of a match (the
// gamemode brain, objective copies) with a server-role sync manager each, plus fact-only entities (players' heroes).
// The world answers the match / objective node types' queries from facts the host pushes in (Set*), and queues the
// graphs' effects for the host (Effects). A Runtime must outlive its matches.
type Match struct {
	h   *C.ss_match
	sms map[uint32]*SyncManager
}

// NewMatch creates a match world whose clock starts at command frame cf; seed drives IntRandom.
func (rt *Runtime) NewMatch(cf, seed uint32) *Match {
	h := C.ss_match_create(rt.h, C.uint32_t(cf), C.uint32_t(seed))
	if h == nil {
		return nil
	}
	return &Match{h: h, sms: map[uint32]*SyncManager{}}
}

func (m *Match) Close() {
	if m.h != nil {
		for _, sm := range m.sms {
			sm.h = nil // borrowed: owned by the match
		}
		C.ss_match_destroy(m.h)
		m.h = nil
	}
}

// EntityFlags say what a match entity is.
type EntityFlags uint32

const (
	WithSyncManager EntityFlags = C.SS_ME_SM     // runs graphs (brain, objectives)
	PlayerHero      EntityFlags = C.SS_ME_PLAYER // a player's hero (fact-only unless WithSyncManager)
	PvPGame         EntityFlags = C.SS_ME_PVP    // has a PvP-game component: the gamemode entity
	GlobalEntity    EntityFlags = C.SS_ME_GLOBAL // GetGlobalEntity
)

func (m *Match) Add(id uint32, def uint64, flags EntityFlags) error {
	return codeErr(C.ss_match_add(m.h, C.uint32_t(id), C.uint64_t(def), C.uint32_t(flags)))
}

func (m *Match) Remove(id uint32) error {
	if sm := m.sms[id]; sm != nil {
		sm.h = nil
		delete(m.sms, id)
	}
	return codeErr(C.ss_match_remove(m.h, C.uint32_t(id)))
}

// SyncManager is the entity's sync manager (owned by the match: don't Close it), nil for fact-only entities. Its
// readers (Instances, SyncVars, ActiveStates, StateData …) feed the remote encoders.
func (m *Match) SyncManager(id uint32) *SyncManager {
	if sm := m.sms[id]; sm != nil {
		return sm
	}
	h := C.ss_match_sm(m.h, C.uint32_t(id))
	if h == nil {
		return nil
	}
	sm := &SyncManager{h}
	m.sms[id] = sm
	return sm
}

func (m *Match) SetTransform(id uint32, pos [3]float32, yawDeg float32) error {
	return codeErr(C.ss_match_set_transform(m.h, C.uint32_t(id), C.float(pos[0]), C.float(pos[1]), C.float(pos[2]), C.float(yawDeg)))
}

func (m *Match) SetTeam(id uint32, team int) error {
	return codeErr(C.ss_match_set_team(m.h, C.uint32_t(id), C.int32_t(team)))
}

func (m *Match) SetFilterBits(id uint32, extra uint64) error {
	return codeErr(C.ss_match_set_filter_bits(m.h, C.uint32_t(id), C.uint64_t(extra)))
}

func (m *Match) SetAlive(id uint32, alive bool) error {
	return codeErr(C.ss_match_set_alive(m.h, C.uint32_t(id), cbool(alive)))
}

func (m *Match) SetTags(id uint32, tags []uint64) error {
	var p *C.uint64_t
	if len(tags) > 0 {
		p = (*C.uint64_t)(unsafe.Pointer(&tags[0]))
	}
	return codeErr(C.ss_match_set_tags(m.h, C.uint32_t(id), p, C.int(len(tags))))
}

// SetPlayer marks hero as a player's hero possessed by controller, with the selected hero GUID.
func (m *Match) SetPlayer(hero, controller uint32, heroGUID uint64) error {
	return codeErr(C.ss_match_set_player(m.h, C.uint32_t(hero), C.uint32_t(controller), C.uint64_t(heroGUID)))
}

func (m *Match) SetHealth(id uint32, pool int, current, max float32) error {
	return codeErr(C.ss_match_set_health(m.h, C.uint32_t(id), C.int32_t(pool), C.float(current), C.float(max)))
}

func (m *Match) SetThrottle(id uint32, x, y float32) error {
	return codeErr(C.ss_match_set_throttle(m.h, C.uint32_t(id), C.float(x), C.float(y)))
}

// CaptureRules is a capture area's STUECCaptureAreaInstanceData (our reading of the fields [?],
// docs/StatescriptNodes.md §6.3) and the zone's radius [?].
type CaptureRules struct {
	DecayTime, F349aaca6, Segments, ExtraPlayer, CaptureTime, F5f080cc5, DecayDelay float32
	F2ca7fc17                                                                       int32
	Fe6e7f301                                                                       float32
	F3a691fdf                                                                       bool
	MaxPlayers                                                                      int32
	Radius                                                                          float32
}

func (m *Match) SetCaptureArea(id uint32, r CaptureRules) error {
	c := C.ss_capture_rules{
		decay_time: C.float(r.DecayTime), f349aaca6: C.float(r.F349aaca6), segments: C.float(r.Segments),
		extra_player: C.float(r.ExtraPlayer), capture_time: C.float(r.CaptureTime), f5f080cc5: C.float(r.F5f080cc5),
		decay_delay: C.float(r.DecayDelay), f2ca7fc17: C.int32_t(r.F2ca7fc17), fe6e7f301: C.float(r.Fe6e7f301),
		f3a691fdf: C.int32_t(cbool(r.F3a691fdf)), max_players: C.int32_t(r.MaxPlayers), radius: C.float(r.Radius),
	}
	return codeErr(C.ss_match_set_capture_area(m.h, C.uint32_t(id), &c))
}

// ReserveIDs gives the world entity ids first … first+count−1 for the entities its graphs create.
func (m *Match) ReserveIDs(first, count uint32) error {
	return codeErr(C.ss_match_reserve_ids(m.h, C.uint32_t(first), C.uint32_t(count)))
}

func keyValues(params map[uint64]Value) ([]C.uint64_t, []C.ss_value, *C.uint64_t, *C.ss_value) {
	var ks []C.uint64_t
	var vs []C.ss_value
	for k, v := range params {
		ks = append(ks, C.uint64_t(k))
		vs = append(vs, v.c())
	}
	if len(ks) == 0 {
		return nil, nil, nil, nil
	}
	return ks, vs, &ks[0], &vs[0]
}

// Start runs graph on an entity with a sync manager (schema defaults, then the overrides); returns the instance id
// (positive: server instances are replicated).
func (m *Match) Start(id uint32, graph uint64, overrides map[uint64]Value) (int16, error) {
	ks, _, kp, vp := keyValues(overrides)
	return instanceResult(C.ss_match_start(m.h, C.uint32_t(id), C.uint64_t(graph), kp, vp, C.int(len(ks))))
}

// Send delivers a rules-layer game message to one entity, or to every entity (target 0).
func (m *Match) Send(target uint32, msg uint64, params map[uint64]Value) error {
	ks, _, kp, vp := keyValues(params)
	return codeErr(C.ss_match_send(m.h, C.uint32_t(target), C.uint64_t(msg), kp, vp, C.int(len(ks))))
}

// PlayerEventKind is a game event of a player (the player-event states 97315c42).
type PlayerEventKind int

const (
	PlayerJoined PlayerEventKind = 0
	PlayerLeft   PlayerEventKind = 1
	PlayerDied   PlayerEventKind = 2
)

func (m *Match) PlayerEvent(kind PlayerEventKind, player, other uint32) error {
	return codeErr(C.ss_match_player_event(m.h, C.int(kind), C.uint32_t(player), C.uint32_t(other)))
}

// Step runs every command frame up to and including cf.
func (m *Match) Step(cf uint32) { C.ss_match_step(m.h, C.uint32_t(cf)) }
func (m *Match) Frame() uint32  { return uint32(C.ss_match_frame(m.h)) }

// EffectKind is what a graph did to the world.
type EffectKind int32

const (
	FxTeam            EffectKind = C.SS_FX_TEAM
	FxTagsAdd         EffectKind = C.SS_FX_TAGS_ADD
	FxTagsRemove      EffectKind = C.SS_FX_TAGS_REMOVE
	FxTeleport        EffectKind = C.SS_FX_TELEPORT
	FxCreate          EffectKind = C.SS_FX_CREATE
	FxDestroy         EffectKind = C.SS_FX_DESTROY
	FxRespawn         EffectKind = C.SS_FX_RESPAWN
	FxMatchTime       EffectKind = C.SS_FX_MATCH_TIME
	FxGameFinished    EffectKind = C.SS_FX_GAME_FINISHED
	FxEndMatch        EffectKind = C.SS_FX_END_MATCH
	FxCaptureEnabled  EffectKind = C.SS_FX_CAPTURE_ENABLED
	FxCaptureOwner    EffectKind = C.SS_FX_CAPTURE_OWNER
	FxCaptureProgress EffectKind = C.SS_FX_CAPTURE_PROGRESS
	FxSpawnEnabled    EffectKind = C.SS_FX_SPAWN_ENABLED
	FxPayloadStatus   EffectKind = C.SS_FX_PAYLOAD_STATUS
	FxBroadcast       EffectKind = C.SS_FX_BROADCAST
)

var effectNames = [...]string{"team", "tags+", "tags-", "teleport", "create", "destroy", "respawn", "match time",
	"game finished", "end match", "capture enabled", "capture owner", "capture progress", "spawn enabled",
	"payload status", "broadcast"}

func (k EffectKind) String() string {
	if k >= 0 && int(k) < len(effectNames) {
		return effectNames[k]
	}
	return "?"
}

// Effect is one effect (fields by kind: I0 = team / on / attackers, I1 = contested / yaw given / defenders, F =
// position + yaw / progress / seconds, U = definition / message / tag; Other = creator / sender).
type Effect struct {
	Seq, CF       uint32
	Kind          EffectKind
	Entity, Other uint32
	I0, I1        int32
	F             [4]float32
	U             uint64
}

// Effects drains the effects queued since the last call, oldest first.
func (m *Match) Effects() []Effect {
	var out []Effect
	buf := make([]C.ss_match_effect, 128)
	for {
		n := int(C.ss_match_effects(m.h, &buf[0], C.int(len(buf))))
		for _, e := range buf[:max(n, 0)] {
			out = append(out, Effect{Seq: uint32(e.seq), CF: uint32(e.cf), Kind: EffectKind(e.kind),
				Entity: uint32(e.entity), Other: uint32(e.other), I0: int32(e.i0), I1: int32(e.i1),
				F: [4]float32{float32(e.f[0]), float32(e.f[1]), float32(e.f[2]), float32(e.f[3])}, U: uint64(e.u)})
		}
		if n < len(buf) {
			return out
		}
	}
}

// CaptureState is a capture area's state as the capture graph left it.
type CaptureState struct {
	Enabled       bool
	Owner         int
	CapturingTeam int
	Contested     bool
	Progress      float32
}

func (m *Match) Capture(id uint32) (CaptureState, error) {
	var c C.ss_capture_state
	if err := codeErr(C.ss_match_capture(m.h, C.uint32_t(id), &c)); err != nil {
		return CaptureState{}, err
	}
	return CaptureState{Enabled: c.enabled != 0, Owner: int(c.owner), CapturingTeam: int(c.capturing_team),
		Contested: c.contested != 0, Progress: float32(c.progress)}, nil
}
