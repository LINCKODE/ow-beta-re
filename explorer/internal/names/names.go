// Package names collects human-readable names for STU hashes and resource GUIDs from the repository's
// data files (systems/data, systems/stu/names). Every name carries its source so the UI can say how sure
// it is. All files are optional: a missing file just means fewer names.
package names

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"owstu/stu"
)

// Hash is the STU name hash: CRC32 (IEEE) of the lower-cased name (types, fields and enums alike).
func Hash(name string) uint32 { return crc32.ChecksumIEEE([]byte(strings.ToLower(name))) }

// Name of a resource GUID or STU hash, with where it came from.
type Name struct {
	Text   string `json:"text"`
	Source string `json:"source"`         // e.g. "string asset", "ui_variables.tsv", "catalogue (medium)"
	Guess  bool   `json:"guess"`          // recovered by CRC match of a guessed name / heuristic
	Note   string `json:"note,omitempty"` // what it is, e.g. "assault map" / "hero definition"
}

// Book holds the names loaded from a repository checkout.
type Book struct {
	Repo  string
	GUIDs map[uint64]Name   // resource names from the TSVs
	Types map[uint32]Name   // extra STU type names (Statescript catalogue) the registry lacks
	Kinds map[uint32]string // Statescript node kind per type hash (catalogue)
	// Statescript node types by whether the client has a class for them (statescript_node_catalogue.json `runtime`;
	// false = no client class: the node falls back to the base class and does nothing on the client).
	Client map[uint32]bool
	Notes  []string // what was loaded (for the about panel)
}

// FindRepo walks up from dir looking for the new-stuff checkout (the directory with systems/data).
func FindRepo(dir string) string {
	if env := os.Getenv("OW_REPO"); env != "" {
		return env
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	for d := dir; ; d = filepath.Dir(d) {
		if st, err := os.Stat(filepath.Join(d, "systems", "data")); err == nil && st.IsDir() {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// Load reads every name source under repo ("" = none).
func Load(repo string) *Book {
	b := &Book{Repo: repo, GUIDs: map[uint64]Name{}, Types: map[uint32]Name{}, Kinds: map[uint32]string{}}
	if repo == "" {
		b.Notes = append(b.Notes, "repository not found: only registry names")
		return b
	}
	data := filepath.Join(repo, "systems", "data")
	b.tsv(filepath.Join(data, "lobby_scripts", "ui_variables.tsv"), false, func(r []string) {
		if g, ok := parseGUID(r[0]); ok && len(r) > 1 {
			b.GUIDs[g] = Name{r[1], "ui_variables.tsv", false, ""}
		}
	})
	b.tsv(filepath.Join(data, "lobby_scripts", "unlocks.tsv"), true, func(r []string) {
		if g, ok := parseGUID(r[0]); ok && len(r) > 2 && r[2] != "" {
			label := r[2]
			if len(r) > 3 && r[3] != "" && r[3] != "all" {
				label = r[3] + ": " + label
			}
			b.GUIDs[g] = Name{label, "unlocks.tsv", false, ""}
		}
	})
	b.tsv(filepath.Join(data, "lobby_scripts", "hero_skins.tsv"), true, func(r []string) {
		if len(r) < 6 {
			return
		}
		if g, ok := parseGUID(r[1]); ok {
			b.GUIDs[g] = Name{r[0], "hero_skins.tsv", false, ""}
		}
		if g, ok := parseGUID(r[4]); ok {
			b.GUIDs[g] = Name{r[0] + ": " + r[5], "hero_skins.tsv", false, ""}
		}
	})
	b.catalogue(filepath.Join(data, "statescript", "statescript_names.csv"))
	b.clientClasses(filepath.Join(data, "statescript", "statescript_node_catalogue.json"))
	b.maps(filepath.Join(data, "maps", "maps.json"))
	b.heroes(filepath.Join(data, "heroes", "heroes.json"))
	return b
}

// maps names map data, map headers and gamemode brains from the map catalogue (docs/Maps.md §1).
func (b *Book) maps(path string) {
	var f struct {
		Maps []struct {
			Header, Map, Brain, Name, Kind, Mode string
		} `json:"maps"`
	}
	if !readJSON(path, &f) {
		return
	}
	for _, m := range f.Maps {
		name := m.Name
		if name == "" {
			name = "(unnamed map)"
		}
		note := strings.TrimSpace(m.Mode + " " + m.Kind + " map")
		if h, ok := parseGUID(m.Header); ok {
			b.GUIDs[h] = Name{Text: name, Source: "maps.json", Note: "map header · " + note}
		}
		if g, ok := parseGUID(m.Map); ok {
			b.GUIDs[g] = Name{Text: name, Source: "maps.json", Note: "map data · " + note}
		}
		if g, ok := parseGUID(m.Brain); ok && m.Kind != "lobby" {
			if old, ok := b.GUIDs[g]; ok && strings.HasPrefix(old.Note, "gamemode brain") {
				old.Text += ", " + name
				b.GUIDs[g] = old
			} else if !ok {
				b.GUIDs[g] = Name{Text: "Gamemode brain: " + name, Source: "maps.json (map environment +0xF0)",
					Note: "gamemode brain definition"}
			}
		}
	}
	b.Notes = append(b.Notes, "maps.json: "+strconv.Itoa(len(f.Maps))+" maps")
}

// heroes names heroes and their entity definitions (systems/data/heroes/heroes.json, T040).
func (b *Book) heroes(path string) {
	var f struct {
		Heroes []struct{ Hero, Name, Def string } `json:"heroes"`
	}
	if !readJSON(path, &f) {
		return
	}
	for _, h := range f.Heroes {
		if g, ok := parseGUID(h.Hero); ok {
			b.GUIDs[g] = Name{Text: h.Name, Source: "heroes.json", Note: "hero"}
		}
		if d, ok := parseGUID(h.Def); ok {
			if d < 1<<32 { // short form: an entity definition index
				d |= 0x0400000000000000
			}
			b.GUIDs[d] = Name{Text: h.Name, Source: "heroes.json", Note: "hero entity definition"}
		}
	}
	b.Notes = append(b.Notes, "heroes.json: "+strconv.Itoa(len(f.Heroes))+" heroes")
}

func readJSON(path string, v any) bool {
	data, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(data, v) == nil
}

func (b *Book) tsv(path string, header bool, row func([]string)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n := 0
	for first := true; sc.Scan(); first = false {
		line := sc.Text()
		if (first && header) || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		row(strings.Split(line, "\t"))
		n++
	}
	b.Notes = append(b.Notes, filepath.Base(path)+": "+strconv.Itoa(n)+" rows")
}

func (b *Book) catalogue(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil || len(rows) < 2 {
		return
	}
	n := 0
	for _, r := range rows[1:] { // hash,name,kind,confidence,method,...
		if len(r) < 4 {
			continue
		}
		h, err := strconv.ParseUint(r[0], 16, 32)
		if err != nil {
			continue
		}
		b.Kinds[uint32(h)] = r[2]
		if r[1] == "" || stu.Name(uint32(h)) != "" {
			continue
		}
		b.Types[uint32(h)] = Name{r[1], "Statescript catalogue (" + r[3] + ")", r[3] != "exact", ""}
		n++
	}
	b.Notes = append(b.Notes, "statescript_names.csv: "+strconv.Itoa(n)+" extra type names")
}

// clientClasses reads which Statescript node types have a client class (docs/StatescriptRuntime.md §1.5 "no client
// class").
func (b *Book) clientClasses(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var rows []struct {
		Hash    string            `json:"hash"`
		Kind    string            `json:"kind"`
		Runtime []json.RawMessage `json:"runtime"`
	}
	if json.Unmarshal(raw, &rows) != nil {
		return
	}
	b.Client = map[uint32]bool{}
	for _, r := range rows {
		if h, err := strconv.ParseUint(r.Hash, 16, 32); err == nil && r.Kind != "ConfigVar" {
			b.Client[uint32(h)] = len(r.Runtime) > 0
		}
	}
	b.Notes = append(b.Notes, fmt.Sprintf("%d Statescript node types with / without a client class (statescript_node_catalogue.json)", len(b.Client)))
}

// ClientClass: whether the client has a class for a Statescript node type (known = the type is in the catalogue).
func (b *Book) ClientClass(h uint32) (has, known bool) {
	has, known = b.Client[h]
	return
}

// TypeName names an STU type / field / enum hash: the registry (OWLib names), then the catalogue,
// else "0x%08x".
func (b *Book) TypeName(h uint32) (string, bool) {
	if n := stu.Name(h); n != "" {
		return n, true
	}
	if n, ok := b.Types[h]; ok {
		return n.Text, true
	}
	return "0x" + strconv.FormatUint(uint64(h)|1<<32, 16)[1:], false
}

func parseGUID(s string) (uint64, bool) {
	s = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "0x"), "0X")
	g, err := strconv.ParseUint(s, 16, 64)
	return g, err == nil && s != ""
}
