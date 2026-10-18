package stu

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Fixtures dumped from the client (systems/stu/fixtures/README.md).
func fixture(name string) string { return filepath.Join("..", "..", "fixtures", name) }

// The fixtures are client data and not distributed; without them the tests are skipped.
func TestMain(m *testing.M) {
	if _, err := os.Stat(fixture("")); err != nil {
		os.Stdout.WriteString("owstu/stu: no fixtures/ directory, tests skipped\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func open(t *testing.T, name string, mode Mode) *Document {
	t.Helper()
	var base uint64
	if mode == Loaded {
		raw, err := os.ReadFile(fixture(name) + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var meta struct{ Base uint64 }
		if err := json.Unmarshal(raw, &meta); err != nil {
			t.Fatal(err)
		}
		base = meta.Base
	}
	d, err := OpenFile(fixture(name), mode, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d
}

func checkAllDecode(t *testing.T, d *Document) {
	t.Helper()
	for i := 0; i < d.NumInstances(); i++ {
		if n := d.Decode(i).ErrorCount(); n != 0 {
			t.Errorf("instance %d: %d decode errors", i, n)
		}
	}
}

const (
	typeGraph     = 0x0df7e24f // STUStatescriptGraph
	typeEntityDef = 0xec23fffd // STUEntityDefinition
	fNodes        = 0x51caba98 // m_nodes
	fStates       = 0x7b5125b3 // m_states
	fEntries      = 0x77b7ff8c // m_entries
	fGraph        = 0x45b2bab8 // m_graph
	fNodesBits    = 0xf7eae932 // m_nodesBitCount
	fConsoleCmd   = 0x682b3b61 // m_consoleCommand
	fComponentMap = 0xa8f462ea // m_componentMap
	fVoiceDef     = 0x6c7b8ce4 // m_voiceDefinition
)

func TestGraph(t *testing.T) {
	d := open(t, "graph_058000000000000F.stud", File)
	if d.NumInstances() != 352 || !d.AllCRCOK() || d.Root() != 0 {
		t.Fatalf("instances %d crc %v root %d", d.NumInstances(), d.AllCRCOK(), d.Root())
	}
	checkAllDecode(t, d)
	refs := 0
	for _, in := range d.Instances() {
		refs += int(in.Flags & 1)
	}
	if refs != 16 {
		t.Errorf("instances with references: %d, want 16", refs)
	}

	root := d.Decode(d.Root())
	if root.Type() != Inline || root.TypeHash() != typeGraph || Name(root.TypeHash()) != "STUStatescriptGraph" {
		t.Fatalf("root %v %#x", root.Type(), root.TypeHash())
	}
	nodes := root.Field(fNodes)
	if nodes.Type() != List || nodes.Len() != 49 {
		t.Fatalf("m_nodes %v len %d", nodes.Type(), nodes.Len())
	}
	for i, n := range nodes.Items() {
		node := n.Deref()
		if !node.Valid() || !IsA(node.TypeHash(), nodes.TypeHash()) {
			t.Errorf("node %d: type %#x not a %#x", i, node.TypeHash(), nodes.TypeHash())
		}
	}
	if root.Field(fStates).Len() != 17 || root.Field(fEntries).Len() != 8 {
		t.Errorf("states %d entries %d", root.Field(fStates).Len(), root.Field(fEntries).Len())
	}
	if n, err := root.Field(fNodesBits).Int(); err != nil || n != 6 {
		t.Errorf("m_nodesBitCount %d %v", n, err)
	}
	g := root.Field(fGraph)
	if g.Type() != Inline || g.TypeHash() != 0xcb52d391 || g.Kind() != KindInline {
		t.Errorf("m_graph %v %#x %v", g.Type(), g.TypeHash(), g.Kind())
	}
	x, err := g.Path(0x396044b9, 0xe4c8dd49).Float()
	if err != nil || x > -277.2179 || x < -277.2181 {
		t.Errorf("extent %v %v", x, err)
	}
	if s, err := root.Field(fConsoleCmd).Str(); err != nil || s != "" {
		t.Errorf("m_consoleCommand %q %v", s, err)
	}
	if _, err := nodes.Str(); !errors.Is(err, ErrType) {
		t.Errorf("Str on a list: %v", err)
	}
	if v := root.Field(0x12345678).Index(3).Field(1); v.Valid() || v.Type() != Invalid {
		t.Error("missing field chain must stay invalid")
	}

	// Registry: an Inline value's items follow the type's fields.
	ti, ok := LookupType(typeGraph)
	if !ok || len(ti.Fields) != root.Len() {
		t.Fatalf("LookupType: %v, %d fields vs %d items", ok, len(ti.Fields), root.Len())
	}
	for i, f := range ti.Fields {
		if it := root.Index(i); it.FieldHash() != f.Hash || it.Kind() != f.Kind {
			t.Errorf("field %d: %#x/%v vs %#x/%v", i, it.FieldHash(), it.Kind(), f.Hash, f.Kind)
		}
	}
	if TypeCount() != 3639 || NameOr(0x12345678) != "0x12345678" {
		t.Errorf("TypeCount %d", TypeCount())
	}

	var tree map[string]any
	if err := json.Unmarshal([]byte(root.JSON(false)), &tree); err != nil || tree["$type"] != "STUStatescriptGraph" {
		t.Errorf("JSON: %v %v", err, tree["$type"])
	}

	d.Close()
	if root.Valid() || root.Len() != 0 || d.Decode(0).Valid() {
		t.Error("values must be invalid after Close")
	}
}

func TestEntityDefinition(t *testing.T) {
	d := open(t, "entitydef_0400000000000039.stud", File)
	if d.NumInstances() != 23 || !d.AllCRCOK() {
		t.Fatalf("instances %d crc %v", d.NumInstances(), d.AllCRCOK())
	}
	checkAllDecode(t, d)
	root := d.Decode(0)
	if root.TypeHash() != typeEntityDef {
		t.Fatalf("root type %#x", root.TypeHash())
	}
	comps := root.Field(fComponentMap)
	if comps.Type() != Map || comps.Len() != 3 {
		t.Fatalf("component map %v len %d", comps.Type(), comps.Len())
	}
	// Keys are component type hashes; values the component instances of that type.
	for _, e := range comps.Items() {
		if c := e.Deref(); c.TypeHash() != e.MapKey() {
			t.Errorf("entry %#x -> type %#x", e.MapKey(), c.TypeHash())
		}
	}
	if i, err := comps.Get(0x81ebdcd0).Object(); err != nil || i != 21 {
		t.Errorf("Get: %d %v", i, err)
	}
	if comps.Get(1).Valid() {
		t.Error("missing key must be invalid")
	}
	// The Statescript component's graphs (resource references, handle -1 in a raw file).
	graphs := comps.Get(0x9b38211b).Deref().Field(0x0175ad85)
	want := []uint64{0x0580000000000029, 0x058000000000008f, 0x05800000000001e1}
	if graphs.Len() != len(want) {
		t.Fatalf("graphs: %d", graphs.Len())
	}
	for i, w := range want {
		guid, handle, err := graphs.Index(i).Field(fGraph).Ref()
		if err != nil || guid != w || handle != ^uint64(0) {
			t.Errorf("graph %d: %#x %#x %v", i, guid, handle, err)
		}
	}
	if guid, _, err := d.Decode(1).Field(fVoiceDef).Ref(); err != nil || guid != 0x07a000000000001d {
		t.Errorf("voice definition %#x %v", guid, err)
	}
}

// The raw file and the as-loaded image decode to the same document.
func TestFileEqualsLoaded(t *testing.T) {
	for _, name := range []string{"graph_058000000000000F", "entitydef_0400000000000039"} {
		f := open(t, name+".stud", File)
		m := open(t, name+".mem", Loaded)
		if m.Mode() != Loaded || !m.AllCRCOK() {
			t.Errorf("%s: loaded mode %v crc %v", name, m.Mode(), m.AllCRCOK())
		}
		checkAllDecode(t, m)
		if a, b := f.JSON(false), m.JSON(false); a == "" || a != b {
			t.Errorf("%s: file and loaded JSON differ", name)
		}
	}
}

func TestOpenErrors(t *testing.T) {
	raw, err := os.ReadFile(fixture("entitydef_0400000000000039.stud"))
	if err != nil {
		t.Fatal(err)
	}
	bad := append([]byte(nil), raw...)
	bad[0] ^= 1
	if _, err := Open(bad, File, 0); err == nil || err.Error() != "stu: bad magic" {
		t.Errorf("bad magic: %v", err)
	}
	if _, err := Open(nil, File, 0); err == nil {
		t.Error("empty input must fail")
	}
	if _, err := OpenFile("/nonexistent.stud", File, 0); err == nil {
		t.Error("missing file must fail")
	}
	d, err := Open(raw, File, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	raw[0] = 0 // the document keeps its own copy
	if d.Decode(0).TypeHash() != typeEntityDef {
		t.Error("document must not alias the input")
	}
}
