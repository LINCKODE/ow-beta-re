// Command movergen dumps the per-entity movement parameters of build 24919 offline.
//
// It walks every entity definition (0x040...) of the install and, for each one that has a
// STUCharacterMoverComponent (0xc6ec49e5, runtime component 0x15) or a CharacterCollisionProxy
// component (0x1ab05d0f), writes the fields of those components plus the other movement-related
// components on the same definition (STUFirstPersonComponent, STUFallingDamageComponent,
// STUAIMovementComponent). Definitions are labelled with the STUHero that owns them (m_gameplayEntity
// or another reference from the hero's top-level fields).
//
//	systems/data/movement/hero_movers.json
//
// It also prints, for the heroes' movers, which fields vary and which are constant (markdown table).
//
// Build systems/content and systems/stu first (cmake, see their READMEs), then:
//
//	cd systems/data/movement/tools/movergen && go run .        # or: go run . -install DIR
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"hash/crc32"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"owcontent/content"
	"owstu/stu"
)

const (
	tMover       = 0xc6ec49e5 // STUCharacterMoverComponent
	tProxy       = 0x1ab05d0f // CharacterCollisionProxy (m_radius, m_height, ...)
	tFirstPerson = 0xdaf7a654 // STUFirstPersonComponent
	tFalling     = 0x4c58173e // STUFallingDamageComponent
	tAIMovement  = 0x565ca5c3 // STUAIMovementComponent
)

// Components dumped besides the mover / proxy, when present on a definition that has one of them.
var extraTypes = []uint32{tFirstPerson, tFalling, tAIMovement}

// Primitive element types of the registry (systems/stu/include/stu/registry.h).
var primNames = map[string]string{
	"3b9327d2": "u8", "6dc98054": "s8", "a49ce182": "u16", "a0119d30": "s16", "91c74719": "u32",
	"954a3bab": "s32", "05d31669": "u64", "015e6adb": "s64", "8fa75a30": "f32", "1bb30b40": "f64",
}

func hash(name string) uint32 { return crc32.ChecksumIEEE([]byte(strings.ToLower(name))) }

func main() {
	root := repoRoot()
	install := flag.String("install", filepath.Join(root, "game/owbetaclient"), "0.8 install directory")
	out := flag.String("json", filepath.Join(root, "systems/data/movement/hero_movers.json"), "output")
	reg := flag.String("registry", filepath.Join(root, "systems/data/client_stu_registry.json"), "client STU registry (field offsets / types)")
	strs := flag.String("strings", filepath.Join(root, "systems/data/lobby_scripts/strings_enUS.tsv"), "string table (GUID<TAB>text)")
	flag.Parse()

	names, err := loadStrings(*strs)
	if err != nil {
		log.Fatal(err)
	}
	layouts, err := loadRegistry(*reg)
	if err != nil {
		log.Fatal(err)
	}
	s, err := content.Open(*install, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	// Heroes and the definitions they reference.
	owners := map[uint64][]string{} // definition GUID -> "Hero" / "Hero.field"
	heroOf := map[uint64]heroRef{}
	for _, r := range s.ResourcesOfType(content.TypeOf(0x02E0000000000000)) {
		d, err := load(s, r.GUID)
		if err != nil {
			log.Fatalf("hero %#016x: %v", r.GUID, err)
		}
		f := fields(d.vals[0])
		def := guidOf(f["m_gameplayEntity"])
		name := ""
		for _, k := range sortedKeys(f) {
			if n, ok := names[guidOf(f[k])]; ok && n != "" {
				name = n
				break
			}
		}
		if name == "" {
			name = fmt.Sprintf("hero %#x", r.GUID)
		}
		if def != 0 {
			heroOf[def] = heroRef{GUID: r.GUID, Name: name}
			owners[def] = append(owners[def], name)
		}
		for _, k := range sortedKeys(f) {
			g := guidOf(f[k])
			if g != 0 && g != def && content.TypeOf(g) == content.TypeOf(0x0400000000000000) {
				owners[g] = append(owners[g], name+"."+k)
			}
		}
	}

	cat := catalogue{
		Source: "systems/data/movement/tools/movergen (offline, from the 0.8 install); systems/data/movement/README.md",
		Layout: map[string][]layoutField{},
	}
	for _, t := range append([]uint32{tMover, tProxy}, extraTypes...) {
		cat.Layout[typeLabel(t)] = layouts[fmt.Sprintf("%08x", t)]
	}
	defs := s.ResourcesOfType(content.TypeOf(0x0400000000000000))
	cat.Scanned = len(defs)
	for _, r := range defs {
		d, err := load(s, r.GUID)
		if err != nil {
			log.Fatalf("definition %#016x: %v", r.GUID, err)
		}
		cm, _ := fields(d.vals[0])["m_componentMap"].(map[string]any)
		comps := map[uint32]map[string]any{}
		for _, c := range cm {
			v, t := d.deref(c)
			comps[t] = fields(v)
		}
		if comps[tMover] == nil && comps[tProxy] == nil {
			continue
		}
		e := &Entity{Def: hex64(r.GUID), Owners: owners[r.GUID], Components: map[string][]Field{}}
		if h, ok := heroOf[r.GUID]; ok {
			e.Hero = h.Name
			e.HeroGUID = hex64(h.GUID)
		}
		if m := comps[0xbc1233e0]; m != nil {
			e.Model = hex64(guidOf(m["m_model"]))
		}
		for _, t := range append([]uint32{tMover, tProxy}, extraTypes...) {
			if f := comps[t]; f != nil {
				e.Components[typeLabel(t)] = dumpFields(f, layouts[fmt.Sprintf("%08x", t)])
			}
		}
		if comps[tMover] != nil {
			cat.MoverCount++
		}
		if comps[tProxy] != nil {
			cat.ProxyCount++
		}
		cat.Entities = append(cat.Entities, e)
	}
	sort.SliceStable(cat.Entities, func(i, j int) bool {
		a, b := cat.Entities[i], cat.Entities[j]
		if (a.Hero != "") != (b.Hero != "") {
			return a.Hero != ""
		}
		if a.Hero != b.Hero {
			return a.Hero < b.Hero
		}
		return a.Def < b.Def
	})
	// Non-hero definitions: the hero whose gameplay definition uses the same model, as a hint.
	heroModel := map[hex64]string{}
	for _, e := range cat.Entities {
		if e.Hero != "" && e.Model != 0 {
			heroModel[e.Model] = e.Hero
		}
	}
	for _, e := range cat.Entities {
		if e.Hero == "" && e.Model != 0 {
			e.SameModelAs = heroModel[e.Model]
		}
	}
	cat.Summary = summarize(cat.Entities)

	b, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	printTables(cat)
	log.Printf("wrote %s: %d definitions scanned, %d with a mover, %d with a collision proxy", *out, cat.Scanned, cat.MoverCount, cat.ProxyCount)
}

// --- output -------------------------------------------------------------------------------------

type hex64 uint64

func (h hex64) MarshalJSON() ([]byte, error) { return fmt.Appendf(nil, "\"0x%016x\"", uint64(h)), nil }

type heroRef struct {
	GUID uint64
	Name string
}

type catalogue struct {
	Source     string                   `json:"source"`
	Scanned    int                      `json:"definitions_scanned"`
	MoverCount int                      `json:"definitions_with_mover"`
	ProxyCount int                      `json:"definitions_with_collision_proxy"`
	Layout     map[string][]layoutField `json:"layout"` // client registry layouts (runtime offsets)
	Summary    []FieldSummary           `json:"hero_mover_summary"`
	Entities   []*Entity                `json:"entities"`
}

type layoutField struct {
	Hash   string `json:"hash"`
	Name   string `json:"name,omitempty"`
	Offset int    `json:"offset"`
	Type   string `json:"type"`
}

// Entity is one entity definition with a mover or collision proxy.
type Entity struct {
	Def      hex64    `json:"def"`
	Hero     string   `json:"hero,omitempty"` // the STUHero whose m_gameplayEntity this is
	HeroGUID hex64    `json:"hero_guid,omitempty"`
	Owners   []string `json:"referenced_by,omitempty"` // STUHero fields referencing the definition
	Model    hex64    `json:"model,omitempty"`
	// SameModelAs: non-hero definitions, the hero whose gameplay definition has the same m_model.
	SameModelAs string             `json:"same_model_as,omitempty"`
	Components  map[string][]Field `json:"components"`
}

// Field is one field value (numbers as decoded; u32 hex strings as numbers; GUIDs as strings).
type Field struct {
	Hash   string `json:"hash"`
	Name   string `json:"name,omitempty"`
	Offset int    `json:"offset,omitempty"`
	Type   string `json:"type,omitempty"`
	Value  any    `json:"value"`
}

// FieldSummary is one mover field over the heroes' gameplay definitions.
type FieldSummary struct {
	Hash     string             `json:"hash"`
	Name     string             `json:"name,omitempty"`
	Offset   int                `json:"offset"`
	Min      float64            `json:"min"`
	Max      float64            `json:"max"`
	Common   float64            `json:"common"` // most frequent value
	Constant bool               `json:"constant"`
	Differ   map[string]float64 `json:"differ,omitempty"` // hero -> value, heroes not at the common value
}

func typeLabel(t uint32) string {
	switch t {
	case tProxy:
		return "CharacterCollisionProxy"
	}
	return stu.NameOr(t)
}

func dumpFields(f map[string]any, layout []layoutField) []Field {
	byHash := map[string]layoutField{}
	for _, l := range layout {
		byHash[l.Hash] = l
	}
	out := []Field{}
	for k, v := range f {
		h := strings.TrimPrefix(k, "0x")
		name := ""
		if !strings.HasPrefix(k, "0x") {
			h = fmt.Sprintf("%08x", hash(k))
			name = k
		}
		fl := Field{Hash: h, Name: name, Value: value(v)}
		if l, ok := byHash[h]; ok {
			fl.Offset, fl.Type = l.Offset, l.Type
			if fl.Name == "" {
				fl.Name = l.Name
			}
		}
		out = append(out, fl)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Offset != out[j].Offset {
			return out[i].Offset < out[j].Offset
		}
		return out[i].Hash < out[j].Hash
	})
	return out
}

func value(v any) any {
	switch x := v.(type) {
	case string:
		if n, err := strconv.ParseUint(x, 0, 64); err == nil {
			return n
		}
		return x
	case map[string]any:
		if g, ok := x["$guid"].(string); ok {
			return g
		}
	case float64:
		// f32 in the data: shortest float32 representation (0.45, not 0.449999988)
		r, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'g', -1, 32), 64)
		return r
	}
	return v
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case uint64:
		return float64(x), true
	}
	return 0, false
}

func summarize(es []*Entity) []FieldSummary {
	var heroes []*Entity
	for _, e := range es {
		if e.Hero != "" && e.Components["STUCharacterMoverComponent"] != nil {
			heroes = append(heroes, e)
		}
	}
	if len(heroes) == 0 {
		return nil
	}
	var out []FieldSummary
	for i, f0 := range heroes[0].Components["STUCharacterMoverComponent"] {
		s := FieldSummary{Hash: f0.Hash, Name: f0.Name, Offset: f0.Offset, Min: math.Inf(1), Max: math.Inf(-1)}
		freq := map[float64]int{}
		vals := map[string]float64{}
		for _, h := range heroes {
			f := h.Components["STUCharacterMoverComponent"][i]
			v, _ := num(f.Value)
			vals[h.Hero] = v
			freq[v]++
			s.Min, s.Max = min(s.Min, v), max(s.Max, v)
		}
		best := -1
		for v, n := range freq {
			if n > best || (n == best && v < s.Common) {
				s.Common, best = v, n
			}
		}
		s.Constant = len(freq) == 1
		for h, v := range vals {
			if v != s.Common {
				if s.Differ == nil {
					s.Differ = map[string]float64{}
				}
				s.Differ[h] = v
			}
		}
		out = append(out, s)
	}
	return out
}

func fmtNum(v float64) string { return strconv.FormatFloat(float64(float32(v)), 'g', 6, 32) }

func printTables(cat catalogue) {
	fmt.Println("## Mover fields over the heroes' gameplay definitions")
	fmt.Println()
	fmt.Println("| off | hash | name | min | max | common | heroes not at common |")
	fmt.Println("|---|---|---|---|---|---|---|")
	for _, s := range cat.Summary {
		var d []string
		for _, h := range sortedKeys(s.Differ) {
			d = append(d, fmt.Sprintf("%s %s", h, fmtNum(s.Differ[h])))
		}
		diff := "constant"
		if !s.Constant {
			diff = strings.Join(d, ", ")
		}
		fmt.Printf("| %d | %s | %s | %s | %s | %s | %s |\n", s.Offset, s.Hash, s.Name, fmtNum(s.Min), fmtNum(s.Max), fmtNum(s.Common), diff)
	}
	fmt.Println()
	fmt.Println("## Every definition with a mover (varying hero fields only)")
	fmt.Println()
	var vary []FieldSummary
	for _, s := range cat.Summary {
		if !s.Constant {
			vary = append(vary, s)
		}
	}
	hdr := "| def | hero / referenced by |"
	sep := "|---|---|"
	for _, s := range vary {
		n := s.Name
		if n == "" {
			n = s.Hash
		}
		hdr += " " + n + " |"
		sep += "---|"
	}
	fmt.Println(hdr)
	fmt.Println(sep)
	for _, e := range cat.Entities {
		m := e.Components["STUCharacterMoverComponent"]
		if m == nil {
			continue
		}
		who := e.Hero
		if who == "" {
			who = strings.Join(e.Owners, ", ")
		}
		if who == "" && e.SameModelAs != "" {
			who = "model of " + e.SameModelAs
		}
		if e.Components["STUAIMovementComponent"] != nil {
			who += " (AI movement)"
		}
		if who == "" {
			who = "-"
		}
		row := fmt.Sprintf("| %#x | %s |", uint64(e.Def)&0xffffffffffff, who)
		for _, s := range vary {
			for _, f := range m {
				if f.Hash == s.Hash {
					v, _ := num(f.Value)
					row += " " + fmtNum(v) + " |"
				}
			}
		}
		fmt.Println(row)
	}
	fmt.Println()
	fmt.Println("## Collision proxies")
	fmt.Println()
	for _, e := range cat.Entities {
		p := e.Components["CharacterCollisionProxy"]
		if p == nil {
			continue
		}
		var parts []string
		for _, f := range p {
			n := f.Name
			if n == "" {
				n = f.Hash
			}
			parts = append(parts, fmt.Sprintf("%s=%v", n, f.Value))
		}
		fmt.Printf("- %#x (%s): %s\n", uint64(e.Def)&0xffffffffffff, strings.Join(e.Owners, ", "), strings.Join(parts, " "))
	}
}

// --- inputs -------------------------------------------------------------------------------------

func repoRoot() string {
	_, f, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(f), "../../../../.."))
}

func loadStrings(path string) (map[uint64]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := map[uint64]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "\t")
		if !ok {
			continue
		}
		if g, err := strconv.ParseUint(strings.TrimPrefix(k, "0x"), 16, 64); err == nil {
			m[g] = v
		}
	}
	return m, sc.Err()
}

// loadRegistry reads the client's STU registry: type hash -> field layout (runtime offsets).
func loadRegistry(path string) (map[string][]layoutField, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]*struct {
		Fields []struct {
			Hash   string  `json:"hash"`
			Offset int     `json:"offset"`
			CType  int     `json:"ctype"`
			Elem   *string `json:"elem"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := map[string][]layoutField{}
	for t, v := range raw {
		if v == nil {
			continue
		}
		for _, f := range v.Fields {
			l := layoutField{Hash: f.Hash, Offset: f.Offset}
			h, _ := strconv.ParseUint(f.Hash, 16, 32)
			if n := stu.Name(uint32(h)); n != "" {
				l.Name = n
			}
			switch {
			case f.Elem != nil && primNames[*f.Elem] != "":
				l.Type = primNames[*f.Elem]
			case f.Elem != nil:
				l.Type = "0x" + *f.Elem
			default:
				l.Type = fmt.Sprintf("ctype %d", f.CType)
			}
			out[t] = append(out[t], l)
		}
	}
	return out, nil
}

// doc is a decoded STU document: instance values (JSON) and types.
type doc struct {
	vals  []map[string]any
	types []uint32
}

func load(s *content.Store, guid uint64) (*doc, error) {
	b, err := s.Read(guid)
	if err != nil {
		return nil, err
	}
	d, err := stu.Open(b, stu.File, 0)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	var raw struct {
		Root []struct {
			Value map[string]any `json:"value"`
		} `json:"root"`
	}
	if err := json.Unmarshal([]byte(d.JSON(false)), &raw); err != nil {
		return nil, err
	}
	out := &doc{}
	for i, in := range d.Instances() {
		out.types = append(out.types, in.Type)
		var v map[string]any
		if i < len(raw.Root) {
			v = raw.Root[i].Value
		}
		out.vals = append(out.vals, v)
	}
	return out, nil
}

func (d *doc) deref(v any) (any, uint32) {
	m, ok := v.(map[string]any)
	if !ok {
		return v, 0
	}
	n, ok := m["$instance"].(float64)
	if i := int(n); ok && i >= 0 && i < len(d.vals) {
		return d.vals[i], d.types[i]
	}
	return v, 0
}

func fields(v any) map[string]any {
	m, _ := v.(map[string]any)
	f, _ := m["$fields"].(map[string]any)
	return f
}

func guidOf(v any) uint64 {
	m, _ := v.(map[string]any)
	s, _ := m["$guid"].(string)
	g, _ := strconv.ParseUint(s, 0, 64)
	return g
}

func sortedKeys[V any](m map[string]V) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
