package statescript

/*
#include "statescript.h"
*/
import "C"

import "unsafe"

// Capabilities (T053/T054): what the runtime implements per client type. The instance server decides from these
// which states it corrects; it keeps no per-type lists of its own (docs/InstanceServer.md §10.3).

// TypeKind is what a client Statescript type is.
type TypeKind uint8

const (
	KindNode      TypeKind = C.SS_KIND_NODE // entry / condition / action / terminator
	KindState     TypeKind = C.SS_KIND_STATE
	KindConfigVar TypeKind = C.SS_KIND_CONFIG_VAR
)

// Level is how faithfully the runtime implements a type.
type Level uint8

const (
	LevelUnmodelled   Level = C.SS_LEVEL_UNMODELLED   // no-op: plugs and lifetime only
	LevelPresentation Level = C.SS_LEVEL_PRESENTATION // client presentation: a no-op is right on a server
	LevelDesigned     Level = C.SS_LEVEL_DESIGNED     // no client class to follow, or partial [?]
	LevelExact        Level = C.SS_LEVEL_EXACT        // the client class's behaviour (Ghidra)
	LevelVerified     Level = C.SS_LEVEL_VERIFIED     // exact and checked against client recordings
)

// DataCap is whether the runtime produces a state type's data in one format.
type DataCap uint8

const (
	DataNone   DataCap = C.SS_DATA_NONE   // the format is empty for the type
	DataWriter DataCap = C.SS_DATA_WRITER // produced from the runtime's state per the spec
	DataEcho   DataCap = C.SS_DATA_ECHO   // not produced: only an import's own bits are written back
)

// TypeCap is one type's capability.
type TypeCap struct {
	Type       uint32
	Kind       TypeKind
	Level      Level
	DataLocal  DataCap
	DataRemote DataCap
	HasUpdate  bool // ScaleTime / Effect / WeaponVolley: the end carries data
	Host       bool // server-only type: the library runs its lifecycle, the host's game systems its effects
}

func capFromC(c C.ss_type_cap) TypeCap {
	return TypeCap{Type: uint32(c._type), Kind: TypeKind(c.kind), Level: Level(c.level), DataLocal: DataCap(c.data_local),
		DataRemote: DataCap(c.data_remote), HasUpdate: c.has_update != 0, Host: c.host != 0}
}

// TypeCaps lists every client type's capability.
func (rt *Runtime) TypeCaps() []TypeCap {
	n := int(C.ss_runtime_type_caps(rt.h, nil, 0))
	if n <= 0 {
		return nil
	}
	buf := make([]C.ss_type_cap, n)
	n = min(int(C.ss_runtime_type_caps(rt.h, &buf[0], C.int(n))), n)
	out := make([]TypeCap, n)
	for i := range out {
		out[i] = capFromC(buf[i])
	}
	return out
}

// TypeCap is one type's capability (ErrNotFound: not a client Statescript type).
func (rt *Runtime) TypeCap(typ uint32) (TypeCap, error) {
	var c C.ss_type_cap
	if rc := C.ss_runtime_type_cap(rt.h, C.uint32_t(typ), &c); rc < 0 {
		return TypeCap{}, codeErr(rc)
	}
	return capFromC(c), nil
}

// GraphNodeTypes is the STU type of every node of a loaded graph, in m_nodes order.
func (rt *Runtime) GraphNodeTypes(graph uint64) ([]uint32, error) {
	n := int(C.ss_runtime_graph_node_types(rt.h, C.uint64_t(graph), nil, 0))
	if n < 0 {
		return nil, codeErr(C.int(n))
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]C.uint32_t, n)
	n = min(int(C.ss_runtime_graph_node_types(rt.h, C.uint64_t(graph), &buf[0], C.int(n))), n)
	out := make([]uint32, n)
	for i := range out {
		out[i] = uint32(buf[i])
	}
	return out, nil
}

// StateDataFormat is a state's data in the local (owner's) or the remote format: the bits, LSB first, and their
// count. ErrUnimplemented: the runtime can't produce that format for the state; never guess it.
func (sm *SyncManager) StateDataFormat(instance int16, state int, local, full bool) ([]byte, int, error) {
	buf := make([]byte, 64)
	call := func() C.int {
		return C.ss_sm_state_data2(sm.h, C.int16_t(instance), C.int32_t(state), cbool(local), cbool(full),
			(*C.uint8_t)(unsafe.Pointer(&buf[0])), C.int(len(buf)*8))
	}
	n := call()
	if n < 0 {
		return nil, 0, codeErr(n)
	}
	if int(n) > len(buf)*8 {
		buf = make([]byte, (int(n)+7)/8)
		if n = call(); n < 0 {
			return nil, 0, codeErr(n)
		}
	}
	return buf[:(int(n)+7)/8], int(n), nil
}

// StateDataRefs are the instance ids a state's data names in a format (a stack predecessor's instance): the
// client crashes on one it doesn't know.
func (sm *SyncManager) StateDataRefs(instance int16, state int, local bool) ([]int16, error) {
	n := int(C.ss_sm_state_data_refs(sm.h, C.int16_t(instance), C.int32_t(state), cbool(local), nil, 0))
	if n < 0 {
		return nil, codeErr(C.int(n))
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]C.int16_t, n)
	n = min(int(C.ss_sm_state_data_refs(sm.h, C.int16_t(instance), C.int32_t(state), cbool(local), &buf[0], C.int(n))), n)
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(buf[i])
	}
	return out, nil
}

// Action is an executed action node (sync spec §2.9): its command frame, instance, m_nodes index and
// m_remoteSyncNodes index (-1: not SYNC_ALL, never transmitted).
type Action struct {
	CF       uint32
	Instance int16
	Node     int
	Remote   int
}

// Actions lists the actions executed on mirrored instances at command frames after sinceCF, in order.
func (sm *SyncManager) Actions(sinceCF uint32) ([]Action, error) {
	n := int(C.ss_sm_actions(sm.h, C.uint32_t(sinceCF), nil, 0))
	if n < 0 {
		return nil, codeErr(C.int(n))
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]C.ss_action, n)
	n = min(int(C.ss_sm_actions(sm.h, C.uint32_t(sinceCF), &buf[0], C.int(n))), n)
	out := make([]Action, n)
	for i := range out {
		out[i] = Action{uint32(buf[i].cf), int16(buf[i].instance), int(buf[i].node), int(buf[i].remote_index)}
	}
	return out, nil
}

// RemoteNode is an m_remoteSyncNodes entry (graph +0x120): its m_nodes index, m_states index (-1: not a state,
// e.g. an action) and, for states, its kind (-1 otherwise).
type RemoteNode struct {
	Node, State int
	Kind        StateKind
}

// GraphRemoteNodes lists every m_remoteSyncNodes entry of a loaded graph in order.
func (rt *Runtime) GraphRemoteNodes(graph uint64) ([]RemoteNode, error) {
	n := int(C.ss_runtime_graph_remote_nodes(rt.h, C.uint64_t(graph), nil, 0))
	if n < 0 {
		return nil, codeErr(C.int(n))
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]C.ss_remote_node, n)
	n = min(int(C.ss_runtime_graph_remote_nodes(rt.h, C.uint64_t(graph), &buf[0], C.int(n))), n)
	out := make([]RemoteNode, n)
	for i := range out {
		out[i] = RemoteNode{int(buf[i].node), int(buf[i].state), StateKind(buf[i].kind)}
	}
	return out, nil
}

// BitCounts are a graph's sync index widths as stored in the asset.
type BitCounts struct {
	Nodes, States, RemoteNodes, SyncVars int // +0x178, +0x17C, +0x180, +0x184
}

// GraphBitCounts reads a loaded graph's index widths.
func (rt *Runtime) GraphBitCounts(graph uint64) (BitCounts, error) {
	var out [4]C.int32_t
	if rc := C.ss_runtime_graph_bit_counts(rt.h, C.uint64_t(graph), &out[0]); rc < 0 {
		return BitCounts{}, codeErr(rc)
	}
	return BitCounts{int(out[0]), int(out[1]), int(out[2]), int(out[3])}, nil
}

// HostEventKind is what happened to a server-only node.
type HostEventKind int32

const (
	HostActionExecuted HostEventKind = 0
	HostStateBegin     HostEventKind = 1
	HostStateFinished  HostEventKind = 2
	HostStateAborted   HostEventKind = 3
)

// HostEvent is a server-only node's execution (Host types), for the host's game systems to apply.
type HostEvent struct {
	CF       uint32
	Instance int16
	Node     int
	State    int // -1 for an action
	Type     uint32
	Kind     HostEventKind
}

// HostEvents lists the host events at command frames after sinceCF, in order.
func (sm *SyncManager) HostEvents(sinceCF uint32) ([]HostEvent, error) {
	n := int(C.ss_sm_host_events(sm.h, C.uint32_t(sinceCF), nil, 0))
	if n < 0 {
		return nil, codeErr(C.int(n))
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]C.ss_host_event, n)
	n = min(int(C.ss_sm_host_events(sm.h, C.uint32_t(sinceCF), &buf[0], C.int(n))), n)
	out := make([]HostEvent, n)
	for i, e := range buf[:n] {
		out[i] = HostEvent{uint32(e.cf), int16(e.instance), int(e.node), int(e.state), uint32(e._type), HostEventKind(e.kind)}
	}
	return out, nil
}

// EvalField evaluates a node's config-var field (by field hash) in an instance; ok = false when it yields no value.
func (sm *SyncManager) EvalField(instance int16, node int, field uint32) (Value, bool, error) {
	var v C.ss_value
	rc := C.ss_sm_eval_field(sm.h, C.int16_t(instance), C.int32_t(node), C.uint32_t(field), &v)
	if rc < 0 {
		return Value{}, false, codeErr(rc)
	}
	return fromC(v), rc == 0, nil
}

// IterateField returns the {key, value} entries an iterable field yields (m_targets …).
func (sm *SyncManager) IterateField(instance int16, node int, field uint32) (keys, values []Value, err error) {
	n := int(C.ss_sm_iterate_field(sm.h, C.int16_t(instance), C.int32_t(node), C.uint32_t(field), nil, nil, 0))
	if n < 0 {
		return nil, nil, codeErr(C.int(n))
	}
	if n == 0 {
		return nil, nil, nil
	}
	ks := make([]C.ss_value, n)
	vs := make([]C.ss_value, n)
	n = min(int(C.ss_sm_iterate_field(sm.h, C.int16_t(instance), C.int32_t(node), C.uint32_t(field), &ks[0], &vs[0], C.int(n))), n)
	for i := 0; i < n; i++ {
		keys, values = append(keys, fromC(ks[i])), append(values, fromC(vs[i]))
	}
	return keys, values, nil
}

// EndState ends an active state (finish: its finished plug; else abort) at the sync manager's current time; the
// next Run applies it through the lifetime path.
func (sm *SyncManager) EndState(instance int16, state int, finish bool) error {
	return codeErr(C.ss_sm_end_state(sm.h, C.int16_t(instance), C.int32_t(state), cbool(finish)))
}

// VarUser is a var's writer (var +0x88): the writing state, its write priority, and whether readers see the
// state's own value (state-defined, with Extra = which of the state's values, e.g. a WeaponVolley's per-shot
// ammo). The var sync's state reference carries it (sync spec §2.7). Instance 0 = no writer.
type VarUser struct {
	Instance, State int16
	Priority        uint8
	StateDefined    bool
	Extra           uint8
}

// VarUsers returns the writer of each var Vars lists, keyed by var id.
func (sm *SyncManager) VarUsers(instance int16) map[uint64]VarUser {
	n := int(C.ss_sm_var_users(sm.h, C.int16_t(instance), nil, nil, 0))
	if n <= 0 {
		return nil
	}
	ids := make([]C.uint64_t, n)
	us := make([]C.ss_var_user, n)
	n = min(int(C.ss_sm_var_users(sm.h, C.int16_t(instance), &ids[0], &us[0], C.int(n))), n)
	out := make(map[uint64]VarUser, n)
	for i := 0; i < n; i++ {
		u := us[i]
		out[uint64(ids[i])] = VarUser{int16(u.instance), int16(u.state), uint8(u.priority), u.state_defined != 0, uint8(u.extra)}
	}
	return out
}
