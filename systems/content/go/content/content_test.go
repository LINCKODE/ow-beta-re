package content

import (
	"bytes"
	"crypto/md5"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"owstu/stu"
)

// The install (game/owbetaclient, or $OW_INSTALL); tests skip when it is absent.
func installDir() string {
	if d := os.Getenv("OW_INSTALL"); d != "" {
		return d
	}
	return filepath.Join("..", "..", "..", "..", "game", "owbetaclient")
}

func open(t *testing.T, opt *Options) *Store {
	t.Helper()
	dir := installDir()
	if _, err := os.Stat(filepath.Join(dir, ".build.info")); err != nil {
		t.Skipf("no install at %s", dir)
	}
	s, err := Open(dir, opt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

const (
	guidGraph     = 0x058000000000000F // Statescript graph (the STU fixture)
	guidEntityDef = 0x0400000000000039 // gamemode brain entity definition (the STU fixture)
)

// One shared store for the read-only tests (opening takes ~0.2 s warm).
var (
	sharedOnce sync.Once
	shared     *Store
	sharedErr  error
)

func store(t *testing.T) *Store {
	t.Helper()
	dir := installDir()
	if _, err := os.Stat(filepath.Join(dir, ".build.info")); err != nil {
		t.Skipf("no install at %s", dir)
	}
	sharedOnce.Do(func() { shared, sharedErr = Open(dir, nil) })
	if sharedErr != nil {
		t.Fatal(sharedErr)
	}
	return shared
}

func TestMain(m *testing.M) {
	code := m.Run()
	if shared != nil {
		shared.Close()
	}
	os.Exit(code)
}

func TestCounts(t *testing.T) {
	s := store(t)
	if d := s.Describe(); !strings.Contains(d, "24919") {
		t.Errorf("describe %q", d)
	}
	if n := s.NumPackages(); n != 1332 {
		t.Errorf("packages %d, want 1332", n)
	}
	pk := s.Packages()
	if len(pk) != 1332 || pk[0].Manifest == "" || pk[0].RecordCount == 0 || TypeOf(pk[0].GUID) != TypeAssetPackage {
		t.Errorf("packages[0] %+v", pk[0])
	}
	all := s.Resources()
	if len(all) != s.NumResources() || len(all) < 150000 {
		t.Fatalf("resources %d / %d", len(all), s.NumResources())
	}
	byType := map[uint32]int{}
	for i, r := range all {
		if i > 0 && r.GUID <= all[i-1].GUID {
			t.Fatalf("not sorted at %d", i)
		}
		if r.Type != TypeOf(r.GUID) {
			t.Fatalf("%016X type %#x vs %#x", r.GUID, r.Type, TypeOf(r.GUID))
		}
		byType[r.Type]++
	}
	if byType[TypeStatescriptGraph] != 370 || byType[TypeEntityDefinition] != 967 || byType[TypeAssetPackage] == 0 {
		t.Errorf("graphs %d, entity definitions %d, packages %d", byType[TypeStatescriptGraph],
			byType[TypeEntityDefinition], byType[TypeAssetPackage])
	}
	for typ, want := range byType {
		got := s.ResourcesOfType(typ)
		if len(got) != want || got[0].Type != typ {
			t.Errorf("ResourcesOfType(%#x): %d, want %d", typ, len(got), want)
		}
	}
	if s.ResourcesOfType(0xFFF) != nil {
		t.Error("unused type has resources")
	}
	if TypeOf(guidGraph) != TypeStatescriptGraph || TypeOf(guidEntityDef) != TypeEntityDefinition {
		t.Error("TypeOf")
	}
}

// read reads a resource, checks it against its entry (size, MD5 = CKey, ReadCKey) and parses it with
// the STU reader: every instance type known with its layout CRC, every instance decodes.
func TestPackageMembership(t *testing.T) {
	s := store(t)
	r, ok := s.Find(guidGraph)
	if !ok {
		t.Fatal("graph not found")
	}
	pkgs := s.ResourcePackages(guidGraph)
	if len(pkgs) == 0 || pkgs[0] != r.Package {
		t.Fatalf("ResourcePackages %x, first package %x", pkgs, r.Package)
	}
	for _, p := range pkgs {
		if !slices.ContainsFunc(s.PackageResources(p), func(x PackageRecord) bool { return x.GUID == guidGraph }) {
			t.Errorf("package %016X does not list the graph", p)
		}
	}
	// Every package's records match its header count, and every resource except packages and bundles
	// is listed somewhere (its first package lists it).
	listed := map[uint64]bool{}
	total := 0
	for _, p := range s.Packages() {
		recs := s.PackageResources(p.GUID)
		total += len(recs)
		for _, x := range recs {
			listed[x.GUID] = true
		}
	}
	want := 0
	for _, p := range s.Packages() {
		want += int(p.RecordCount)
	}
	if total < want { // packages sharing a GUID are both counted by PackageResources
		t.Errorf("records %d, header counts %d", total, want)
	}
	for _, x := range s.Resources() {
		if x.Package != 0 && x.Type != 0x09C && !listed[x.GUID] {
			t.Fatalf("%016X not listed by any package", x.GUID)
		}
	}
	if s.PackageResources(0x1234) != nil || s.ResourcePackages(0x0580000000FFFFFF) != nil {
		t.Error("unknown package / resource")
	}
}

func read(t *testing.T, s *Store, guid uint64) *stu.Document {
	t.Helper()
	r, ok := s.Find(guid)
	if !ok {
		t.Fatalf("%016X not found", guid)
	}
	b, err := s.Read(guid)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != int(r.Size) || CKey(md5.Sum(b)) != r.CKey || !bytes.HasPrefix(b, []byte("DUTS")) {
		t.Fatalf("%016X: size %d/%d, md5 %x, ckey %s", guid, len(b), r.Size, md5.Sum(b), r.CKey)
	}
	b2, err := s.ReadCKey(r.CKey)
	if err != nil || !bytes.Equal(b, b2) {
		t.Fatalf("ReadCKey: %v", err)
	}
	d, err := stu.Open(b, stu.File, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	if !d.AllCRCOK() {
		t.Errorf("%016X: layout CRC mismatch", guid)
	}
	for i := 0; i < d.NumInstances(); i++ {
		if n := d.Decode(i).ErrorCount(); n != 0 {
			t.Errorf("%016X instance %d: %d decode errors", guid, i, n)
		}
	}
	return d
}

// The decoded tree equals the one of the STU fixture dumped from the live client (fixup undone).
func sameAsFixture(t *testing.T, d *stu.Document, fixture string) {
	t.Helper()
	f, err := stu.OpenFile(filepath.Join("..", "..", "..", "stu", "fixtures", fixture), stu.File, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if j := d.JSON(false); len(j) < 1000 || j != f.JSON(false) {
		t.Errorf("decoded tree differs from fixture %s", fixture)
	}
}

func TestReadGraph(t *testing.T) {
	s := store(t)
	d := read(t, s, guidGraph)
	root := d.Decode(d.Root())
	if d.NumInstances() != 352 || stu.Name(root.TypeHash()) != "STUStatescriptGraph" {
		t.Fatalf("instances %d root %s", d.NumInstances(), stu.NameOr(root.TypeHash()))
	}
	sameAsFixture(t, d, "graph_058000000000000F.stud")
}

func TestReadEntityDefinition(t *testing.T) {
	s := store(t)
	d := read(t, s, guidEntityDef)
	root := d.Decode(d.Root())
	if d.NumInstances() != 23 || stu.Name(root.TypeHash()) != "STUEntityDefinition" {
		t.Fatalf("instances %d root %s", d.NumInstances(), stu.NameOr(root.TypeHash()))
	}
	sameAsFixture(t, d, "entitydef_0400000000000039.stud")
}

// check reads one resource and parses it; returns its size and "" or what is wrong.
func check(s *Store, r Resource) (int, string) {
	b, err := s.Read(r.GUID)
	if err != nil {
		return 0, err.Error()
	}
	if CKey(md5.Sum(b)) != r.CKey {
		return len(b), "md5"
	}
	d, err := stu.Open(b, stu.File, 0)
	if err != nil {
		return len(b), err.Error()
	}
	defer d.Close()
	if !d.AllCRCOK() || d.NumInstances() == 0 {
		return len(b), "crc"
	}
	return len(b), ""
}

// Every graph and entity definition, read on 8 goroutines (reads are thread-safe), parses with all CRCs ok.
func TestAllGraphsAndEntityDefinitions(t *testing.T) {
	s := store(t)
	res := append(s.ResourcesOfType(TypeStatescriptGraph), s.ResourcesOfType(TypeEntityDefinition)...)
	jobs := make(chan Resource)
	var mu sync.Mutex
	var bad []string
	total := 0
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range jobs {
				n, msg := check(s, r)
				mu.Lock()
				total += n
				if msg != "" {
					bad = append(bad, fmt.Sprintf("%016X %s", r.GUID, msg))
				}
				mu.Unlock()
			}
		}()
	}
	for _, r := range res {
		jobs <- r
	}
	close(jobs)
	wg.Wait()
	t.Logf("%d resources, %d bytes", len(res), total)
	if len(res) != 370+967 || len(bad) != 0 {
		t.Errorf("%d resources, %d bad: %v", len(res), len(bad), bad)
	}
}

func TestErrors(t *testing.T) {
	s := store(t)
	if _, ok := s.Find(0x0580000000FFFFFF); ok {
		t.Error("unknown GUID found")
	}
	if _, err := s.Read(0x0580000000FFFFFF); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown GUID: %v", err)
	}
	if _, err := s.ReadCKey(CKey{1, 2, 3}); err == nil {
		t.Error("unknown ckey read")
	}
	if _, err := Open(filepath.Join(os.TempDir(), "no-such-install"), nil); err == nil ||
		!strings.Contains(err.Error(), "no-such-install") {
		t.Errorf("bad install dir: %v", err)
	}
	if k, err := ParseCKey(CKey{0xab, 0xcd}.String()); err != nil || k != (CKey{0xab, 0xcd}) {
		t.Errorf("ParseCKey round trip: %v", err)
	}
	if _, err := ParseCKey("abc"); err == nil {
		t.Error("ParseCKey accepted a short key")
	}
}

func TestOptionsAndClose(t *testing.T) {
	s := open(t, &Options{VerifyMD5: true, ExtraKeys: map[uint64][16]byte{0x1122334455667788: {1, 2, 3}}})
	read(t, s, guidEntityDef)
	b, err := s.Read(guidGraph)
	s.Close()
	if err != nil || len(b) != 38432 {
		t.Fatalf("read before close: %v", err)
	}
	if b[0] != 'D' { // the bytes are a Go copy and outlive the store
		t.Error("bytes after close")
	}
	if _, err := s.Read(guidGraph); !errors.Is(err, ErrClosed) {
		t.Errorf("read after close: %v", err)
	}
	if s.NumResources() != 0 || s.Resources() != nil || s.Describe() != "" {
		t.Error("closed store not empty")
	}
	s.Close() // idempotent
}
