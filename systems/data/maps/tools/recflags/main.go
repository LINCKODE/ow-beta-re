// Command recflags prints every placement record of one or more map data resources (docs/Maps.md §2) with its
// flags byte (+0x10), the bytes after it, its placed index and, for entity records, the definition (T037,
// docs/MatchLogic.md §8.2). Build systems/content first, then:
//
//	cd systems/data/maps/tools/recflags && go run . 0x08000000000002C3 | grep type=11
package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"owcontent/content"
)

// install is new-stuff/game/owbetaclient, found from this file's location (go run keeps it).
func install() string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "../../../../../game/owbetaclient")
}

func main() {
	s, err := content.Open(install(), nil)
	if err != nil {
		log.Fatal(err)
	}
	for _, a := range os.Args[1:] {
		g, _ := strconv.ParseUint(a, 0, 64)
		d, err := s.Read(g)
		if err != nil {
			log.Fatal(err)
		}
		le := binary.LittleEndian
		placed := 0
		for t := 0; t < 16; t++ {
			off := int(le.Uint32(d[0x210+4*t:]))
			n := int(le.Uint32(d[0x250+4*t:]))
			for j := 0; j < n; j++ {
				size := int(le.Uint32(d[off+0x14:]))
				idx := -1
				if t >= 8 {
					idx = placed
					placed++
				}
				var def uint64
				if t == 0xb {
					def = le.Uint64(d[off+0x18:])
				}
				fmt.Printf("%#x type=%d idx=%#x flags=%#02x b11=%#02x b12=%#02x def=%#x uuid=%x\n", g, t, idx, d[off+0x10], d[off+0x11], d[off+0x12], def, d[off:off+16])
				off += size
			}
		}
	}
}
