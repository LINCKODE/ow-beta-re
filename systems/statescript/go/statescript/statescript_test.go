package statescript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Graph images dumped from the client (systems/data/live_anubis/graphs: <guid>.mem + meta.json bases).
var graphDir = filepath.Join("..", "..", "..", "data", "live_anubis", "graphs")

func loadImage(t *testing.T, rt *Runtime, guid uint64) {
	t.Helper()
	var meta map[string]struct{ Base uint64 }
	b, err := os.ReadFile(filepath.Join(graphDir, "meta.json"))
	if err != nil {
		t.Skip("no graph images:", err)
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		t.Fatal(err)
	}
	m, ok := meta[fmt.Sprintf("0x%016x", guid)]
	if !ok {
		t.Fatalf("graph %#x not in meta.json", guid)
	}
	img, err := os.ReadFile(filepath.Join(graphDir, fmt.Sprintf("%016x.mem", guid)))
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.LoadGraph(guid, img, Loaded, m.Base); err != nil {
		t.Fatalf("load %#x: %v", guid, err)
	}
}

const door = 0x0580000000000042

// The door graph as a default graph (the C API test's scenario, through Go).
func TestDefaultGraph(t *testing.T) {
	rt := NewRuntime()
	defer rt.Close()
	loadImage(t, rt, door)
	sm := rt.NewSyncManager(0x40000001, Client)
	defer sm.Close()
	if err := sm.Trace(true); err != nil {
		t.Fatal(err)
	}
	sm.SetClock(100, 100*16+15)
	id, err := sm.CreateInstance(door, nil, 0)
	if err != nil || id != -1 {
		t.Fatalf("CreateInstance = %d, %v", id, err)
	}
	sm.Run(101, 1)
	if got := sm.ActiveStates(id); len(got) != 2 {
		t.Errorf("active states %v, want the closed branch (2 states)", got)
	}
	rs := sm.ActiveRemoteStates(id)
	if len(rs) != 1 || rs[0] != (RemoteState{Remote: 1, State: 1}) {
		t.Errorf("active remote states %v", rs)
	}
	if len(sm.SyncVars(id)) == 0 {
		t.Error("no sync vars")
	}
	v, err := sm.GetVar(id, VarID(0x192))
	if err != nil || v.Type != Float || v.Bits != 0 {
		t.Errorf("$192 = %+v, %v", v, err)
	}
	if ids := sm.Instances(); len(ids) != 1 || ids[0] != id {
		t.Errorf("instances %v", ids)
	}
	info, err := sm.InstanceInfo(id)
	if err != nil || info.Graph != door || info.Parent != 0 {
		t.Errorf("info %+v, %v", info, err)
	}
	tr := sm.DrainTrace()
	if !strings.Contains(tr, `"kind":"begin"`) {
		t.Errorf("trace has no begin event: %.300s", tr)
	}
	if sm.DrainTrace() != "" {
		t.Error("second drain not empty")
	}
}

// Import (replication): the instance exists with the given id, no entries fire, vars and states are what we
// set.
func TestImport(t *testing.T) {
	rt := NewRuntime()
	defer rt.Close()
	loadImage(t, rt, door)
	sm := rt.NewSyncManager(0x40000001, Server)
	defer sm.Close()
	sm.SetClock(200, 200*16)
	id, err := sm.ImportInstance(door, 3, 0, 0)
	if err != nil || id != 3 {
		t.Fatalf("ImportInstance = %d, %v", id, err)
	}
	if _, err := sm.ImportInstance(door, 3, 0, 0); err != ErrExists {
		t.Errorf("duplicate id: %v, want ErrExists", err)
	}
	if err := sm.SetVar(0, VarID(0x317), Value{Type: Float, Bits: 0x3f800000}); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetVar(id, VarID(0x192), Value{Type: Float, Bits: 0x3f800000}); err != nil {
		t.Fatal(err)
	}
	sm.Run(201, 1)
	if got := sm.ActiveStates(id); len(got) != 0 {
		t.Errorf("imported instance began states %v (entries must not fire)", got)
	}
	vs := sm.Vars(0)
	if len(vs) != 1 || vs[0].ID != VarID(0x317) {
		t.Errorf("entity bag %+v", vs)
	}
	if v, _ := sm.GetVar(id, VarID(0x192)); v.Bits != 0x3f800000 {
		t.Errorf("$192 = %+v", v)
	}
	// State 1 (the closed Anim) has no data in the local format we know of; the runtime decides per type.
	if err := sm.ImportState(id, 1, nil, 0); err != nil && err != ErrUnimplemented {
		t.Fatal(err)
	} else if err == nil {
		if got := sm.ActiveStates(id); len(got) != 1 || got[0] != 1 {
			t.Errorf("after ImportState: %v", got)
		}
	}
	_ = sm.Events(id)
	_ = sm.Unmodelled(UnmodelledStates)
}

func TestLayout(t *testing.T) {
	rt := NewRuntime()
	defer rt.Close()
	loadImage(t, rt, door)
	slots, err := rt.GraphSyncSlots(door)
	if err != nil || len(slots) == 0 {
		t.Fatalf("sync slots %v, %v", slots, err)
	}
	states, err := rt.GraphStates(door)
	if err != nil || len(states) == 0 {
		t.Fatalf("states %v, %v", states, err)
	}
	nb, err := rt.GraphIndexBits(door)
	if err != nil || nb < 1 {
		t.Fatalf("index bits %d, %v", nb, err)
	}
	if _, err := rt.GraphStates(0x0580000000000001); err == nil {
		t.Error("unknown graph: no error")
	}
	t.Logf("door: %d sync slots, %d states, %d index bits", len(slots), len(states), nb)
}

// Capabilities and layouts (T053/T054) on the door graph.
func TestCapsAndLayouts(t *testing.T) {
	rt := NewRuntime()
	defer rt.Close()
	loadImage(t, rt, door)
	g := uint64(door)
	caps := rt.TypeCaps()
	if len(caps) == 0 {
		t.Fatal("no type capabilities")
	}
	states := 0
	for _, c := range caps {
		if c.Kind == KindState {
			states++
		}
	}
	if states == 0 {
		t.Error("no state types")
	}
	bc, err := rt.GraphBitCounts(g)
	if err != nil || bc.Nodes <= 0 || bc.States <= 0 {
		t.Fatalf("bit counts %+v %v", bc, err)
	}
	nodes, err := rt.GraphNodeTypes(g)
	if err != nil || len(nodes) == 0 || len(nodes) > 1<<bc.Nodes {
		t.Errorf("%d node types for %d node bits (%v)", len(nodes), bc.Nodes, err)
	}
	rn, err := rt.GraphRemoteNodes(g)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("bits %+v, %d nodes, %d remote nodes", bc, len(nodes), len(rn))
}
