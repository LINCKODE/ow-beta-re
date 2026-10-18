// Package statescript wraps the Statescript runtime (systems/statescript, C API capi/statescript.h) for Go:
// load graphs, host sync managers (one per entity), import a start-up snapshot, feed client input per command
// frame, step, and read the sync state (instances, vars, active states, state data, pending events) for the
// full / delta encoders. Semantics: docs/StatescriptRuntime.md §5.
//
// Build the static C API first: cmake -S systems/statescript -B systems/statescript/build -G Ninja &&
// cmake --build systems/statescript/build
//
// Nothing here is safe for concurrent use: one goroutine owns a Runtime and its sync managers (the instance
// server's game loop). A Runtime must outlive its sync managers.
package statescript

/*
#cgo CFLAGS: -I${SRCDIR}/../../capi
#cgo LDFLAGS: ${SRCDIR}/../../build/libow_statescript_c_static.a ${SRCDIR}/../../build/libow_statescript_ecs.a ${SRCDIR}/../../build/libow_statescript.a ${SRCDIR}/../../build/libss_dep_ecs.a ${SRCDIR}/../../build/libss_dep_stu.a -lstdc++ -lm
#include <stdlib.h>
#include "statescript.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

var (
	ErrArg           = errors.New("statescript: bad argument")
	ErrParse         = errors.New("statescript: not a Statescript graph")
	ErrNotFound      = errors.New("statescript: not found")
	ErrExists        = errors.New("statescript: instance id in use")
	ErrUnimplemented = errors.New("statescript: not implemented in the runtime yet")
)

func codeErr(rc C.int) error {
	switch rc {
	case C.SS_OK:
		return nil
	case C.SS_ERR_ARG:
		return ErrArg
	case C.SS_ERR_PARSE:
		return ErrParse
	case C.SS_ERR_NOT_FOUND:
		return ErrNotFound
	case C.SS_ERR_EXISTS:
		return ErrExists
	case C.SS_ERR_UNIMPLEMENTED:
		return ErrUnimplemented
	}
	if rc < 0 {
		return fmt.Errorf("statescript: error %d", int(rc))
	}
	return nil
}

// Mode says how pointers in a graph image are encoded.
type Mode int

const (
	File   Mode = C.SS_MODE_FILE   // raw STUD bytes (CASC)
	Loaded Mode = C.SS_MODE_LOADED // image dumped from the client (absolute pointers, base = its address)
)

// Role selects the sync manager's node filters.
type Role int

const (
	Client Role = C.SS_ROLE_CLIENT // server-only nodes are skipped
	Server Role = C.SS_ROLE_SERVER // client-only nodes are skipped
)

// Type is a StatescriptPrimitive type.
type Type uint32

const (
	Byte       Type = 1
	Int        Type = 2
	Float      Type = 3
	Int64      Type = 4
	Vec3       Type = 6
	IntVec4    Type = 7
	InstanceID Type = 8
	Flt16Vec4  Type = 9
	Color      Type = 10
	Entity     Type = 11
)

// Value is a StatescriptPrimitive: payload bytes 0..7 in Bits, 8..11 in Extra.
type Value struct {
	Type  Type
	Bits  uint64
	Extra uint32
}

func (v Value) c() C.ss_value {
	return C.ss_value{_type: C.uint32_t(v.Type), bits: C.uint64_t(v.Bits), extra: C.uint32_t(v.Extra)}
}
func fromC(v C.ss_value) Value { return Value{Type(v._type), uint64(v.bits), uint32(v.extra)} }

// VarID makes a variable identifier (0x0D8 GUID) from its low bits.
func VarID(low uint64) uint64 { return 0x0D80000000000000 | low&0xFFFFFFFFFFFF }

// GraphGUID makes a graph GUID (type 0x058) from its index.
func GraphGUID(index uint32) uint64 { return 0x0580000000000000 | uint64(index) }

// Runtime owns the loaded graphs and the config-var evaluators.
type Runtime struct{ h *C.ss_runtime }

func NewRuntime() *Runtime { return &Runtime{C.ss_runtime_create()} }

func (rt *Runtime) Close() {
	if rt.h != nil {
		C.ss_runtime_destroy(rt.h)
		rt.h = nil
	}
}

// LoadGraph loads a graph from STUD bytes (copied).
func (rt *Runtime) LoadGraph(guid uint64, b []byte, mode Mode, base uint64) error {
	if len(b) == 0 {
		return ErrArg
	}
	return codeErr(C.ss_runtime_load_graph(rt.h, C.uint64_t(guid), (*C.uint8_t)(unsafe.Pointer(&b[0])),
		C.size_t(len(b)), C.int(mode), C.uint64_t(base)))
}

// GraphRefs lists the graphs a loaded graph references (SubScript children, PlayScript literals).
func (rt *Runtime) GraphRefs(guid uint64) []uint64 {
	n := int(C.ss_runtime_graph_refs(rt.h, C.uint64_t(guid), nil, 0))
	if n <= 0 {
		return nil
	}
	out := make([]uint64, n)
	n = int(C.ss_runtime_graph_refs(rt.h, C.uint64_t(guid), (*C.uint64_t)(unsafe.Pointer(&out[0])), C.int(n)))
	return out[:min(n, len(out))]
}

// SetConstant makes every config var of STU type cvType evaluate to v.
func (rt *Runtime) SetConstant(cvType uint32, v Value) error {
	return codeErr(C.ss_runtime_set_constant(rt.h, C.uint32_t(cvType), v.c()))
}

// SyncManager is one entity's Statescript (component 0x23's sync manager).
type SyncManager struct{ h *C.ss_sm }

// NewSyncManager creates a standalone sync manager for entity.
func (rt *Runtime) NewSyncManager(entity uint32, role Role) *SyncManager {
	h := C.ss_sm_create(rt.h, C.uint32_t(entity), C.int(role))
	if h == nil {
		return nil
	}
	return &SyncManager{h}
}

func (sm *SyncManager) Close() {
	if sm.h != nil {
		C.ss_sm_destroy(sm.h)
		sm.h = nil
	}
}

func (sm *SyncManager) SetClock(cf uint32, ms int64) { C.ss_sm_set_clock(sm.h, C.uint32_t(cf), C.int64_t(ms)) }
func (sm *SyncManager) CommandFrame() uint32        { return uint32(C.ss_sm_command_frame(sm.h)) }
func (sm *SyncManager) Time() int64                  { return int64(C.ss_sm_time(sm.h)) }

// Run runs n command frames starting at cf (n = 0 flushes at the current time).
func (sm *SyncManager) Run(cf, n uint32) { C.ss_sm_run(sm.h, C.uint32_t(cf), C.uint32_t(n)) }

// instanceResult decodes the int32 id-or-error of the instance constructors.
func instanceResult(r C.int32_t) (int16, error) {
	if r <= -0x10000 {
		return 0, codeErr(C.int(r / 0x10000))
	}
	return int16(r), nil
}

// CreateInstance loads graph as a default graph (schema defaults, then overrides; entries fire at the next
// Run). id 0 allocates a local (negative) id.
func (sm *SyncManager) CreateInstance(graph uint64, overrides map[uint64]Value, id int16) (int16, error) {
	var ks []C.uint64_t
	var vs []C.ss_value
	for k, v := range overrides {
		ks = append(ks, C.uint64_t(k))
		vs = append(vs, v.c())
	}
	var kp *C.uint64_t
	var vp *C.ss_value
	if len(ks) > 0 {
		kp, vp = &ks[0], &vs[0]
	}
	return instanceResult(C.ss_sm_create_instance(sm.h, C.uint64_t(graph), kp, vp, C.int(len(ks)), C.int16_t(id)))
}

// ImportInstance creates a replicated instance (no schema, no overrides, entries not fired); parent / slot
// = the SubScript parent and its m_states index (0 / 0 for top-level).
func (sm *SyncManager) ImportInstance(graph uint64, id, parent int16, slot uint32) (int16, error) {
	return instanceResult(C.ss_sm_import_instance(sm.h, C.uint64_t(graph), C.int16_t(id), C.int16_t(parent), C.uint32_t(slot)))
}

// ImportState activates state (m_states index) of an imported instance with its local-format state data
// (nbits bits, LSB first in data).
func (sm *SyncManager) ImportState(instance int16, state int, data []byte, nbits int) error {
	var p *C.uint8_t
	if len(data) > 0 {
		p = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}
	return codeErr(C.ss_sm_import_state(sm.h, C.int16_t(instance), C.int32_t(state), p, C.int(nbits)))
}

// Input feeds the client's command for frame cf (button mask, action code); call before Run(cf, 1).
func (sm *SyncManager) Input(cf uint32, buttons uint16, action uint8) error {
	return codeErr(C.ss_sm_input(sm.h, C.uint32_t(cf), C.uint16_t(buttons), C.uint8_t(action)))
}

// DeliverMessage sends a game message to the entity; returns the number of instances whose entry fired.
func (sm *SyncManager) DeliverMessage(msg uint64, params map[uint64]Value, sender, instigator uint32) int {
	var ks []C.uint64_t
	var vs []C.ss_value
	for k, v := range params {
		ks = append(ks, C.uint64_t(k))
		vs = append(vs, v.c())
	}
	var kp *C.uint64_t
	var vp *C.ss_value
	if len(ks) > 0 {
		kp, vp = &ks[0], &vs[0]
	}
	return int(C.ss_sm_deliver_message(sm.h, C.uint64_t(msg), kp, vp, C.int(len(ks)), C.uint32_t(sender), C.uint32_t(instigator)))
}

// SetVar writes a var of an instance's bag (instance 0 = the entity bag).
func (sm *SyncManager) SetVar(instance int16, id uint64, v Value) error {
	return codeErr(C.ss_sm_set_var(sm.h, C.int16_t(instance), C.uint64_t(id), v.c()))
}

func (sm *SyncManager) GetVar(instance int16, id uint64) (Value, error) {
	var out C.ss_value
	if err := codeErr(C.ss_sm_get_var(sm.h, C.int16_t(instance), C.uint64_t(id), &out)); err != nil {
		return Value{}, err
	}
	return fromC(out), nil
}

// Var is a variable and its value.
type Var struct {
	ID    uint64
	Value Value
}

type varLister func(ids *C.uint64_t, vals *C.ss_value, cap C.int) C.int

func listVars(f varLister) []Var {
	n := int(f(nil, nil, 0))
	for n > 0 {
		ids := make([]C.uint64_t, n)
		vals := make([]C.ss_value, n)
		m := int(f(&ids[0], &vals[0], C.int(n)))
		if m > n { // grew in between (can't, single-threaded; be safe)
			n = m
			continue
		}
		out := make([]Var, m)
		for i := range out {
			out[i] = Var{uint64(ids[i]), fromC(vals[i])}
		}
		return out
	}
	return nil
}

// Vars lists every primitive var of a bag (instance 0 = the entity bag), ordered by id.
func (sm *SyncManager) Vars(instance int16) []Var {
	return listVars(func(ids *C.uint64_t, vals *C.ss_value, cap C.int) C.int {
		return C.ss_sm_vars(sm.h, C.int16_t(instance), ids, vals, cap)
	})
}

// SyncVars lists the graph's m_syncVars that have a value in the instance.
func (sm *SyncManager) SyncVars(instance int16) []Var {
	return listVars(func(ids *C.uint64_t, vals *C.ss_value, cap C.int) C.int {
		return C.ss_sm_sync_vars(sm.h, C.int16_t(instance), ids, vals, cap)
	})
}

// Instances lists the instance ids in creation order (stopped ones excluded).
func (sm *SyncManager) Instances() []int16 {
	n := int(C.ss_sm_instances(sm.h, nil, 0))
	if n <= 0 {
		return nil
	}
	out := make([]int16, n)
	n = int(C.ss_sm_instances(sm.h, (*C.int16_t)(unsafe.Pointer(&out[0])), C.int(n)))
	return out[:min(n, len(out))]
}

// InstanceInfo is an instance's graph and SubScript parent.
type InstanceInfo struct {
	Graph  uint64
	Parent int16
	Slot   uint32
	Flags  uint32
}

func (sm *SyncManager) InstanceInfo(instance int16) (InstanceInfo, error) {
	var o C.ss_instance_info
	if err := codeErr(C.ss_sm_instance_info(sm.h, C.int16_t(instance), &o)); err != nil {
		return InstanceInfo{}, err
	}
	return InstanceInfo{uint64(o.graph), int16(o.parent), uint32(o.slot), uint32(o.flags)}, nil
}

func (sm *SyncManager) intList(f func(p *C.int32_t, cap C.int) C.int) []int {
	n := int(f(nil, 0))
	if n <= 0 {
		return nil
	}
	buf := make([]C.int32_t, n)
	n = min(int(f(&buf[0], C.int(n))), len(buf))
	out := make([]int, n)
	for i := range out {
		out[i] = int(buf[i])
	}
	return out
}

// ActiveStates lists the m_states indices of the instance's active states.
func (sm *SyncManager) ActiveStates(instance int16) []int {
	return sm.intList(func(p *C.int32_t, cap C.int) C.int {
		return C.ss_sm_active_states(sm.h, C.int16_t(instance), p, cap)
	})
}

// RemoteState is an active remotely synced state: its m_remoteSyncNodes and m_states indices.
type RemoteState struct{ Remote, State int }

func (sm *SyncManager) ActiveRemoteStates(instance int16) []RemoteState {
	n := int(C.ss_sm_active_remote_states(sm.h, C.int16_t(instance), nil, nil, 0))
	if n <= 0 {
		return nil
	}
	r := make([]C.int32_t, n)
	s := make([]C.int32_t, n)
	n = min(int(C.ss_sm_active_remote_states(sm.h, C.int16_t(instance), &r[0], &s[0], C.int(n))), len(r))
	out := make([]RemoteState, n)
	for i := range out {
		out[i] = RemoteState{int(r[i]), int(s[i])}
	}
	return out
}

// StateData returns an active state's local-format state data (full = a full update's fields): the bits,
// LSB first, and their count.
func (sm *SyncManager) StateData(instance int16, state int, full bool) ([]byte, int, error) {
	f := 0
	if full {
		f = 1
	}
	buf := make([]byte, 64)
	n := C.ss_sm_state_data(sm.h, C.int16_t(instance), C.int32_t(state), C.int(f), (*C.uint8_t)(unsafe.Pointer(&buf[0])), C.int(len(buf)*8))
	if n < 0 {
		return nil, 0, codeErr(n)
	}
	if int(n) > len(buf)*8 {
		buf = make([]byte, (int(n)+7)/8)
		n = C.ss_sm_state_data(sm.h, C.int16_t(instance), C.int32_t(state), C.int(f), (*C.uint8_t)(unsafe.Pointer(&buf[0])), C.int(len(buf)*8))
		if n < 0 {
			return nil, 0, codeErr(n)
		}
	}
	return buf[:(int(n)+7)/8], int(n), nil
}

// EventKind is a pending notification kind a local update carries.
type EventKind int32

const (
	EventTimer  EventKind = 1 // Data = the timer's user data
	EventFinish EventKind = 2 // Lifetime finish
)

// Event is a pending Timer / Lifetime-finish notification of an instance.
type Event struct {
	Kind  EventKind
	State int
	Data  int32
	Time  int64 // ms
}

// Events lists an instance's pending Timer and Lifetime-finish notifications in queue order.
func (sm *SyncManager) Events(instance int16) []Event {
	n := int(C.ss_sm_events(sm.h, C.int16_t(instance), nil, 0))
	if n <= 0 {
		return nil
	}
	buf := make([]C.ss_event, n)
	n = min(int(C.ss_sm_events(sm.h, C.int16_t(instance), &buf[0], C.int(n))), len(buf))
	out := make([]Event, n)
	for i := range out {
		out[i] = Event{EventKind(buf[i].kind), int(buf[i].state), int32(buf[i].data), int64(buf[i].time)}
	}
	return out
}

// Trace enables or disables the sync manager's trace buffer (the ss_trace vocabulary).
func (sm *SyncManager) Trace(on bool) error {
	o := 0
	if on {
		o = 1
	}
	return codeErr(C.ss_sm_trace_enable(sm.h, C.int(o)))
}

// DrainTrace returns the buffered trace as JSON lines and empties the buffer.
func (sm *SyncManager) DrainTrace() string {
	buf := make([]byte, 64<<10)
	for {
		n := int(C.ss_sm_trace_drain(sm.h, (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf))))
		if n >= 0 {
			return string(buf[:n])
		}
		buf = make([]byte, -n)
	}
}

// Unmodelled kinds.
const (
	UnmodelledNodes      = 0
	UnmodelledStates     = 1
	UnmodelledConfigVars = 2
)

// Unmodelled returns the explicit no-ops so far (STU type hash → count) of one kind.
func (sm *SyncManager) Unmodelled(kind int) map[uint32]int {
	n := int(C.ss_sm_unmodelled(sm.h, C.int(kind), nil, nil, 0))
	if n <= 0 {
		return nil
	}
	ts := make([]C.uint32_t, n)
	cs := make([]C.int32_t, n)
	n = min(int(C.ss_sm_unmodelled(sm.h, C.int(kind), &ts[0], &cs[0], C.int(n))), n)
	out := make(map[uint32]int, n)
	for i := 0; i < n; i++ {
		out[uint32(ts[i])] = int(cs[i])
	}
	return out
}

// SyncSlot is an m_syncVars entry: its var id (0 = empty slot), mode (+0x10; 0 = eligible for a full
// update) and flag (+0x14).
type SyncSlot struct {
	Var  uint64
	Mode uint32
	Flag uint8
}

// GraphSyncSlots lists every m_syncVars slot of a loaded graph in order.
func (rt *Runtime) GraphSyncSlots(graph uint64) ([]SyncSlot, error) {
	n := int(C.ss_runtime_graph_sync_slots(rt.h, C.uint64_t(graph), nil, 0))
	if n < 0 {
		return nil, codeErr(C.int(n))
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]C.ss_sync_slot, n)
	n = min(int(C.ss_runtime_graph_sync_slots(rt.h, C.uint64_t(graph), &buf[0], C.int(n))), n)
	out := make([]SyncSlot, n)
	for i := range out {
		out[i] = SyncSlot{uint64(buf[i]._var), uint32(buf[i].mode), uint8(buf[i].flag)}
	}
	return out, nil
}

// StateKind is how the local format treats an m_states entry.
type StateKind int32

const (
	StatePlain      StateKind = C.SS_STATE_PLAIN
	StateClientOnly StateKind = C.SS_STATE_CLIENT_ONLY
	StateServerOnly StateKind = C.SS_STATE_SERVER_ONLY
	StateHasUpdate  StateKind = C.SS_STATE_HAS_UPDATE
)

// StateLayout is an m_states entry: its STU type, kind, m_remoteSyncNodes index (-1 = none) and m_nodes index.
type StateLayout struct {
	Type   uint32
	Kind   StateKind
	Remote int
	Node   int
}

// GraphStates lists every m_states entry of a loaded graph in order.
func (rt *Runtime) GraphStates(graph uint64) ([]StateLayout, error) {
	n := int(C.ss_runtime_graph_states(rt.h, C.uint64_t(graph), nil, 0))
	if n < 0 {
		return nil, codeErr(C.int(n))
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]C.ss_state_layout, n)
	n = min(int(C.ss_runtime_graph_states(rt.h, C.uint64_t(graph), &buf[0], C.int(n))), n)
	out := make([]StateLayout, n)
	for i := range out {
		out[i] = StateLayout{uint32(buf[i]._type), StateKind(buf[i].kind), int(buf[i].remote_index), int(buf[i].node)}
	}
	return out, nil
}

// GraphIndexBits is the width of a state index in the local events tail (graph +0x17C).
func (rt *Runtime) GraphIndexBits(graph uint64) (int, error) {
	n := int(C.ss_runtime_graph_index_bits(rt.h, C.uint64_t(graph)))
	if n < 0 {
		return 0, codeErr(C.int(n))
	}
	return n, nil
}

// SetStackPriority registers a StackPriority record (GUID type 0x0C4, STU 98b439ca) for the sync managers
// created afterwards; Stack and Ability states need theirs (an Ability without it never starts).
func (rt *Runtime) SetStackPriority(guid uint64, priority float32, winsTies bool) error {
	return codeErr(C.ss_runtime_set_stack_priority(rt.h, C.uint64_t(guid), C.float(priority), cbool(winsTies)))
}

func cbool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

// SetDict writes a dictionary var (instance 0 = the entity bag) with keys[i] -> values[i]; no notification.
func (sm *SyncManager) SetDict(instance int16, id uint64, keys, values []Value) error {
	if len(keys) != len(values) {
		return ErrArg
	}
	ks := make([]C.ss_value, len(keys))
	vs := make([]C.ss_value, len(values))
	for i := range keys {
		ks[i], vs[i] = keys[i].c(), values[i].c()
	}
	var kp, vp *C.ss_value
	if len(ks) > 0 {
		kp, vp = &ks[0], &vs[0]
	}
	return codeErr(C.ss_sm_set_dict(sm.h, C.int16_t(instance), C.uint64_t(id), kp, vp, C.int(len(ks))))
}

// StateChanges is a state's lifetime change count (begin, re-enter, end).
func (sm *SyncManager) StateChanges(instance int16, state int) (uint32, error) {
	n := C.ss_sm_state_changes(sm.h, C.int16_t(instance), C.int32_t(state))
	if n < 0 {
		return 0, codeErr(n)
	}
	return uint32(n), nil
}
