// texscan loads every texture of the install through internal/texture and prints statistics (T063 validation):
//
//	texscan                      load + decode statistics
//	texscan -stats               header / payload statistics
//	texscan -png GUID DIR        mip 0 (with -mips: every mip) of one texture as PNG; -png all:<format name> DIR
//	texscan -ref DIR             DDS + float32 dumps of up to 20 textures per format (compare with another decoder)
package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"owcontent/content"
	"owexplorer/internal/texture"
)

func main() {
	install := flag.String("install", "../game/owbetaclient", "install")
	dump := flag.String("png", "", "write mip 0 (best) of the listed GUIDs (comma list, or 'all:<fmt>') to this dir")
	ref := flag.String("ref", "", "write DDS + decoded float32 of mip 0 surface 0 for up to 20 textures per format to this dir")
	stats := flag.Bool("stats", false, "header statistics only")
	allMips := flag.Bool("mips", false, "with -png: every mip of surface 0")
	flag.Parse()
	s, err := content.Open(*install, nil)
	if err != nil {
		panic(err)
	}
	defer s.Close()
	read := func(g uint64) ([]byte, error) {
		b, err := s.Read(g)
		if errors.Is(err, content.ErrNotFound) {
			return nil, texture.ErrMissing
		}
		return b, err
	}
	cnt := map[string]int{}
	ex := map[string]uint64{}
	add := func(k string, g uint64) {
		cnt[k]++
		if _, ok := ex[k]; !ok {
			ex[k] = g
		}
	}
	refCount := map[string]int{}
	for _, r := range s.ResourcesOfType(texture.TypeTexture) {
		t, err := texture.Load(read, r.GUID)
		if err != nil {
			add("load error: "+err.Error(), r.GUID)
			continue
		}
		fn := texture.FormatName(t.Header.Format)
		if *stats {
			h := t.Header
			add(fmt.Sprintf("flags %04x surf %d inline %v", h.Flags, h.Surfaces, t.Inline), r.GUID)
			add(fmt.Sprintf("flags %04x %s", h.Flags, fn), r.GUID)
			add(fmt.Sprintf("b5 %d flags %04x", h.Unk5, h.Flags), r.GUID)
			add(fmt.Sprintf("unk10 zero %v", h.Unk10 == 0), r.GUID)
			add(fmt.Sprintf("base nonzero %v flags&2000 %v", h.Base != 0, h.Flags&0x2000 != 0), r.GUID)
			add(fmt.Sprintf("variant %x payloads %d", (r.GUID>>32)&0xffff, h.Payloads), r.GUID)
			if h.Flags&texture.FlagDelta != 0 {
				if bb, err := read(h.Base); err == nil {
					bh, _ := texture.ParseHeader(bb)
					add(fmt.Sprintf("delta base flags %04x %s mips %d/%d", bh.Flags, texture.FormatName(bh.Format), bh.Mips, h.Mips), r.GUID)
				}
			}
			if tl, err := texture.Load(read, r.GUID); err == nil {
				for i, p := range tl.Payloads {
					last := p.First + int(p.Header.Count) - 1
					tag := "rest"
					if last == len(tl.Mips)-1 {
						tag = "to smallest"
					}
					add(fmt.Sprintf("np %d payload %d first %d (%s)", h.Payloads, i, p.First, tag), r.GUID)
				}
				if !tl.Inline && tl.Mips[0].Width*tl.Mips[0].Height > 0 {
					add(fmt.Sprintf("np %d max side %d", h.Payloads, max(tl.Mips[0].Width, tl.Mips[0].Height)), r.GUID)
				}
			}
			continue
		}
		switch {
		case t.Format == nil:
			add("no decoder "+fn, r.GUID)
		}
		for _, p := range t.Payloads {
			if !p.Present {
				add("payload missing", r.GUID)
			}
			if p.Err != "" {
				add("payload error: "+p.Err, r.GUID)
			}
		}
		b := t.Best()
		if t.Header.Flags&texture.FlagDelta != 0 {
			add("delta", r.GUID)
		}
		if t.Format != nil {
			if b < 0 {
				add("no mip", r.GUID)
			} else {
				add(fmt.Sprintf("best mip %d", b), r.GUID)
				if _, err := t.Decode(b, 0); err != nil {
					add("decode error", r.GUID)
				} else {
					add("decoded", r.GUID)
				}
			}
			avail := 0
			for m := range t.Mips {
				if t.Available(m) {
					avail++
				}
			}
			if avail < len(t.Mips) {
				add(fmt.Sprintf("partial (%s)", fn), r.GUID)
			}
		}
		if *ref != "" && b == 0 && t.Format != nil && refCount[fn] < 20 && (refCount[fn] == 0 || r.GUID%7 == 0) {
			refCount[fn]++
			writeRef(*ref, t)
		}
		if *dump != "" && *allMips && want(*dump, r.GUID, fn) {
			for m := range t.Mips {
				if img, err := t.Render(m, 0, texture.Display{Auto: true}); err == nil {
					data, _ := texture.PNG(img)
					os.WriteFile(filepath.Join(flag.Arg(0), fmt.Sprintf("%016X_m%d.png", r.GUID, m)), data, 0o644)
				}
			}
		}
		if *dump != "" && b >= 0 {
			if img, err := t.Render(b, 0, texture.Display{Auto: true}); err == nil && want(*dump, r.GUID, fn) {
				data, _ := texture.PNG(img)
				os.WriteFile(filepath.Join(flag.Arg(0), fmt.Sprintf("%016X.png", r.GUID)), data, 0o644)
			}
		}
	}
	ks := make([]string, 0, len(cnt))
	for k := range cnt {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		fmt.Printf("%-60s %6d  e.g. %016X\n", k, cnt[k], ex[k])
	}
}

func want(spec string, g uint64, fn string) bool {
	if len(spec) > 4 && spec[:4] == "all:" {
		return spec[4:] == fn
	}
	v, _ := strconv.ParseUint(spec, 16, 64)
	return v == g
}

func writeRef(dir string, t *texture.Texture) {
	m := t.Mips[0]
	data := m.Data[0]
	h := make([]byte, 4+124+20)
	le := func(o int, v uint32) { h[o], h[o+1], h[o+2], h[o+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24) }
	copy(h, "DDS ")
	le(4, 124)
	le(8, 0x1|0x2|0x4|0x1000)
	le(12, uint32(m.Height))
	le(16, uint32(m.Width))
	le(4+72, 32)
	le(4+76, 4)
	copy(h[4+80:], "DX10")
	le(4+104, 0x1000)
	le(128, uint32(t.Header.Format))
	le(132, 3)
	le(140, 1)
	name := filepath.Join(dir, fmt.Sprintf("%s_%016X", texture.FormatName(t.Header.Format), t.GUID))
	os.WriteFile(name+".dds", append(h, data...), 0o644)
	px, _ := t.Decode(0, 0)
	buf := make([]byte, len(px)*4)
	for i, v := range px {
		u := math.Float32bits(v)
		buf[i*4], buf[i*4+1], buf[i*4+2], buf[i*4+3] = byte(u), byte(u>>8), byte(u>>16), byte(u>>24)
	}
	os.WriteFile(name+".f32", buf, 0o644)
}
