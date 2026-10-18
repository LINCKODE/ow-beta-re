// Command mapedit writes an edited copy of a map data blob (docs/Maps.md §5): move/rescale placements, add
// copies of entity placements. The output can be delivered to the client at runtime with the Prometheus MCP
// tool inject_resource (before the map loads).
//
//	cd systems/maps/tools/mapedit
//	go run . -map 0x080000000000005B -out ../../edits/anubis.bin \
//	    -move 0x12c:+0,+2,+0:3        # placed 0x12c: pos += (0,2,0), scale ×3
//	    -add  0x12c:-2.5,1.8,-43.5:2  # new copy of placed 0x12c at (-2.5,1.8,-43.5), scale ×2
//
// Coordinates with a sign prefix on every component are relative to the placement's position.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"owcontent/content"
	"owmaps/maps"
)

type list []string

func (l *list) String() string     { return strings.Join(*l, " ") }
func (l *list) Set(s string) error { *l = append(*l, s); return nil }

type edit struct {
	placed int
	pos    [3]float32
	rel    bool
	scale  float32
}

func parseEdit(s string) (edit, error) {
	var e edit
	parts := strings.Split(s, ":")
	if len(parts) < 2 {
		return e, fmt.Errorf("want placed:x,y,z[:scale], got %q", s)
	}
	p, err := strconv.ParseInt(strings.TrimPrefix(parts[0], "0x"), 16, 32)
	if err != nil {
		return e, err
	}
	e.placed = int(p)
	xyz := strings.Split(parts[1], ",")
	if len(xyz) != 3 {
		return e, fmt.Errorf("want x,y,z in %q", s)
	}
	for i, c := range xyz {
		f, err := strconv.ParseFloat(c, 32)
		if err != nil {
			return e, err
		}
		e.pos[i] = float32(f)
	}
	e.rel = strings.HasPrefix(xyz[0], "+") // relative when the first component has an explicit '+'
	e.scale = 1
	if len(parts) > 2 {
		f, err := strconv.ParseFloat(parts[2], 32)
		if err != nil {
			return e, err
		}
		e.scale = float32(f)
	}
	return e, nil
}

func main() {
	var moves, adds list
	mapGUID := flag.String("map", "0x080000000000005B", "map data GUID")
	out := flag.String("out", "", "output file")
	flag.Var(&moves, "move", "placed:x,y,z[:scale]  (first component with '+' = relative)")
	flag.Var(&adds, "add", "srcplaced:x,y,z[:scale]  new copy of an entity placement")
	flag.Parse()
	if *out == "" {
		log.Fatal("-out required")
	}
	_, f, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(f), "../../../.."))
	s, err := content.Open(filepath.Join(root, "game/owbetaclient"), nil)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	g, err := strconv.ParseUint(strings.TrimPrefix(*mapGUID, "0x"), 16, 64)
	if err != nil {
		log.Fatal(err)
	}
	b, err := s.Read(g)
	if err != nil {
		log.Fatal(err)
	}
	d, err := maps.Open(b)
	if err != nil {
		log.Fatal(err)
	}
	defer d.Close()
	m, err := d.Map(true, false)
	if err != nil {
		log.Fatal(err)
	}
	orig := m // transforms are computed from the unedited map
	target := func(e edit) (*maps.Record, [3]float32, [3]float32) {
		r := orig.ByPlaced(e.placed)
		if r == nil || r.Xf == nil || !r.XfWritable {
			log.Fatalf("placed %#x: no writable transform", e.placed)
		}
		pos := e.pos
		if e.rel {
			for i := range pos {
				pos[i] += r.Xf.Pos[i]
			}
		}
		sc := [3]float32{r.Xf.Scale[0] * e.scale, r.Xf.Scale[1] * e.scale, r.Xf.Scale[2] * e.scale}
		return r, pos, sc
	}
	for _, mv := range moves {
		e, err := parseEdit(mv)
		if err != nil {
			log.Fatal(err)
		}
		r, pos, sc := target(e)
		if err := d.SetTransform(uint32(r.Offset), pos, &sc, r.Xf.Rot); err != nil {
			log.Fatal(err)
		}
		log.Printf("moved placed %#x (%s) %v -> %v, scale %v", e.placed, r.Primary, r.Xf.Pos, pos, sc)
	}
	// Adds last: they shift the records after the entity section (placed indexes of types 12-15).
	for _, ad := range adds {
		e, err := parseEdit(ad)
		if err != nil {
			log.Fatal(err)
		}
		r, pos, sc := target(e)
		cur, _ := d.Map(true, false)
		r = cur.ByPlaced(e.placed) // same record, current offset (adds only shift records after the entity section)
		if r.Type != maps.Entity {
			log.Fatalf("placed %#x is not an entity", e.placed)
		}
		var u [16]byte
		rand.Read(u[:])
		if err := d.AddEntity(uint32(r.Offset), hex.EncodeToString(u[:]), pos, &sc, r.Xf.Rot); err != nil {
			log.Fatal(err)
		}
		log.Printf("added a copy of placed %#x (%s) at %v scale %v, placement GUID %x", e.placed, r.Primary, pos, sc, u)
	}
	nb := d.Bytes()
	chk, err := maps.Open(nb) // the result must parse
	if err != nil {
		log.Fatalf("edited map does not parse: %v", err)
	}
	cm, _ := chk.Map(false, false)
	chk.Close()
	if err := os.WriteFile(*out, nb, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s: %d bytes (was %d), %d records, %d placed", *out, len(nb), len(b), cm.Header.RecordCount, cm.Placed)
}
