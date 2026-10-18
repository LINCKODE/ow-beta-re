// Command mapcomp reports what a map is made of (docs/Maps.md §3): the asset package that lists its map
// data and that package's contents by resource type, the resources the map data references directly
// (records, environment, entity instance data) by type, and which of those the map's package holds.
//
//	cd systems/maps/tools/mapcomp && go run . 0x080000000000005B 0x08000000000002C3
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"owcontent/content"
	"owmaps/maps"
	"owstu/stu"
)

type typeStat struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
	Bytes int64  `json:"bytes"`
	InPkg int    `json:"in_map_package,omitempty"`
}

type report struct {
	Map         string     `json:"map"`
	Size        int        `json:"size"`
	Package     string     `json:"package"`
	PackageRecs int        `json:"package_records"`
	PackageSize int64      `json:"package_bytes"`
	Contents    []typeStat `json:"package_contents"`
	Refs        []typeStat `json:"map_references"`
	Missing     []string   `json:"references_not_in_install,omitempty"`
}

func main() {
	_, f, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(f), "../../../.."))
	s, err := content.Open(filepath.Join(root, "game/owbetaclient"), nil)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	var out []report
	for _, a := range os.Args[1:] {
		g, err := strconv.ParseUint(strings.TrimPrefix(a, "0x"), 16, 64)
		if err != nil {
			log.Fatal(err)
		}
		out = append(out, one(s, g))
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
}

func stats(m map[uint32]*typeStat) []typeStat {
	var v []typeStat
	for _, t := range m {
		v = append(v, *t)
	}
	sort.Slice(v, func(i, j int) bool { return v[i].Bytes > v[j].Bytes })
	return v
}

func one(s *content.Store, g uint64) report {
	b, err := s.Read(g)
	if err != nil {
		log.Fatal(err)
	}
	d, err := maps.Open(b)
	if err != nil {
		log.Fatal(err)
	}
	defer d.Close()
	m, err := d.Map(true, true)
	if err != nil {
		log.Fatal(err)
	}
	r := report{Map: fmt.Sprintf("0x%016x", g), Size: len(b)}
	pkgs := s.ResourcePackages(g)
	inPkg := map[uint64]bool{}
	if len(pkgs) > 0 {
		r.Package = fmt.Sprintf("0x%016x", pkgs[0])
		cont := map[uint32]*typeStat{}
		for _, pr := range s.PackageResources(pkgs[0]) {
			inPkg[pr.GUID] = true
			t := content.TypeOf(pr.GUID)
			if cont[t] == nil {
				cont[t] = &typeStat{Type: fmt.Sprintf("%03X", t)}
			}
			cont[t].Count++
			if res, ok := s.Find(pr.GUID); ok {
				cont[t].Bytes += int64(res.Size)
				r.PackageSize += int64(res.Size)
			}
			r.PackageRecs++
		}
		r.Contents = stats(cont)
	}
	// Direct references: record GUID fields, the environment, and every reference in the instance data.
	refs := map[uint64]bool{}
	add := func(x uint64) {
		if x != 0 && x != ^uint64(0) && x>>60 == 0 && x>>48 != 0 {
			refs[x] = true
		}
	}
	e := m.Header.Env
	for _, x := range []maps.GUID{e.GamemodeDef, e.Lighting, e.SkyModel, e.SkyLook, e.SoundSpace, e.Name, e.TextureF8,
		e.Texture108, e.Texture128, e.Texture130, e.Texture180, e.Texture188, e.Texture190} {
		add(uint64(x))
	}
	for _, sp := range m.SoundSpaces {
		add(uint64(sp.Space))
	}
	for _, rec := range m.Records {
		for _, fl := range rec.Fields {
			if sv, ok := fl[2].(string); ok && len(sv) == 18 && strings.HasPrefix(sv, "0x") {
				x, _ := strconv.ParseUint(sv[2:], 16, 64)
				add(x)
			}
		}
		for _, in := range rec.Instances {
			add(uint64(in.Look))
		}
		for _, id := range rec.InstanceData {
			doc, err := stu.Open(b[id.Offset:uint32(id.Offset)+id.Size], stu.File, 0)
			if err != nil {
				continue
			}
			collectRefs(doc.JSON(false), add)
			doc.Close()
		}
	}
	rs := map[uint32]*typeStat{}
	for x := range refs {
		t := content.TypeOf(x)
		if rs[t] == nil {
			rs[t] = &typeStat{Type: fmt.Sprintf("%03X", t)}
		}
		rs[t].Count++
		if res, ok := s.Find(x); ok {
			rs[t].Bytes += int64(res.Size)
		} else {
			r.Missing = append(r.Missing, fmt.Sprintf("0x%016x", x))
		}
		if inPkg[x] {
			rs[t].InPkg++
		}
	}
	r.Refs = stats(rs)
	sort.Strings(r.Missing)
	return r
}

// collectRefs finds every "$guid" in a stu_dump-format JSON document.
func collectRefs(js string, add func(uint64)) {
	for {
		i := strings.Index(js, `"$guid": "0x`)
		if i < 0 {
			return
		}
		js = js[i+12:]
		if len(js) >= 16 {
			x, err := strconv.ParseUint(js[:16], 16, 64)
			if err == nil {
				add(x)
			}
		}
	}
}
