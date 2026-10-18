// modelscan: model / material / look scan and export (T064):
//
//	modelscan -scan                                   every model (all LODs), material and look through internal/model
//	modelscan -export MODEL[:LOOK[:LOD]] -out x.glb   export (.glb or .obj, by the extension)
//	modelscan -dump GUID > file, -dumpall TYPE:DIR, -texfmt
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"owcontent/content"
)

func main() {
	install := flag.String("install", "../game/owbetaclient", "install")
	dump := flag.String("dump", "", "write raw bytes of GUID to stdout")
	dumpAll := flag.String("dumpall", "", "type:dir: write every resource of a type (hex) to dir")
	texFmt := flag.Bool("texfmt", false, "print guid format surfaces w h of every texture")
	doScan := flag.Bool("scan", false, "parse every model / material / look (internal/model)")
	doExport := flag.String("export", "", "MODEL[:LOOK[:LOD]]: write -out as .glb or .obj")
	out := flag.String("out", "model.glb", "export path")
	flag.Parse()
	s, err := content.Open(*install, nil)
	if err != nil {
		panic(err)
	}
	defer s.Close()
	if *doExport != "" {
		var mg, lg uint64
		lod := 0
		parts := strings.Split(*doExport, ":")
		mg, _ = strconv.ParseUint(parts[0], 16, 64)
		if len(parts) > 1 {
			lg, _ = strconv.ParseUint(parts[1], 16, 64)
		}
		if len(parts) > 2 {
			lod, _ = strconv.Atoi(parts[2])
		}
		if err := export(s, mg, lg, uint8(lod), *out); err != nil {
			panic(err)
		}
		return
	}
	if *doScan {
		scan(s)
		return
	}
	if *texFmt {
		for _, r := range s.ResourcesOfType(4) {
			b, err := s.Read(r.GUID)
			if err == nil && len(b) >= 32 {
				fmt.Printf("%016x %d %d %d %d %d\n", r.GUID, b[3], b[4], int(b[8])|int(b[9])<<8, int(b[10])|int(b[11])<<8, b[5])
			}
		}
		return
	}
	if *dumpAll != "" {
		var t uint32
		var dir string
		fmt.Sscanf(*dumpAll, "%x:%s", &t, &dir)
		os.MkdirAll(dir, 0o755)
		for _, r := range s.ResourcesOfType(t) {
			b, err := s.Read(r.GUID)
			if err == nil {
				os.WriteFile(fmt.Sprintf("%s/%016x", dir, r.GUID), b, 0o644)
			}
		}
		return
	}
	if *dump != "" {
		g, _ := strconv.ParseUint(*dump, 16, 64)
		b, err := s.Read(g)
		if err != nil {
			panic(err)
		}
		os.Stdout.Write(b)
		return
	}
	for _, t := range []uint32{0x00C, 0x008, 0x007, 0x01A, 0x006} {
		rs := s.ResourcesOfType(t)
		var sizes []int
		for _, r := range rs {
			b, err := s.Read(r.GUID)
			if err != nil {
				continue
			}
			sizes = append(sizes, len(b))
		}
		sort.Ints(sizes)
		if len(sizes) == 0 {
			fmt.Printf("%03X: %d resources, none read\n", t, len(rs))
			continue
		}
		fmt.Printf("%03X: %d resources, %d read, size min %d med %d max %d; first %016x last %016x\n", t, len(rs), len(sizes), sizes[0], sizes[len(sizes)/2], sizes[len(sizes)-1], rs[0].GUID, rs[len(rs)-1].GUID)
	}
}
