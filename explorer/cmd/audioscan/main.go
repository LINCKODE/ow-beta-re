// audioscan: audio resources of the install (T065).
//
//	audioscan TYPE…                         magic / WEM header statistics per type (hex types, e.g. 03F 0B2)
//	audioscan -dump GUID                    raw bytes to stdout
//	audioscan -ogg GUID -out F              a WEM rebuilt as Ogg Vorbis
//	audioscan -raw GUID -out F              a WEM decoded to interleaved float32
//	audioscan -all [-ffmpeg DIR -every N]   convert + decode every WEM; compare every N-th with ffmpeg's decode
//	audioscan -banks                        bank chunk / HIRC / Sound / parent / event / action statistics
//	audioscan -groups                       switch groups, switches, parameters, state groups with the FNV-1 check
package main

import (
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"owstu/stu"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"owcontent/content"
	"owexplorer/internal/audio"
)

func main() {
	install := flag.String("install", "../game/owbetaclient", "install")
	dump := flag.String("dump", "", "GUID to write raw to stdout")
	ogg := flag.String("ogg", "", "GUID of a WEM to write as Ogg Vorbis (-out)")
	out := flag.String("out", "", "output file")
	groups := flag.Bool("groups", false, "switch / state groups, switches, parameters (02D–030)")
	banks := flag.Bool("banks", false, "bank statistics")
	all := flag.Bool("all", false, "convert + decode every WEM (03F, 0B2); with -ffmpeg DIR also compare every -every'th with ffmpeg")
	ffdir := flag.String("ffmpeg", "", "with -all: directory for the ffmpeg comparison files")
	every := flag.Int("every", 50, "with -ffmpeg: compare every n-th WEM")
	raw := flag.String("raw", "", "GUID of a WEM to decode to interleaved float32 (-out)")
	flag.Parse()
	s, err := content.Open(*install, nil)
	if err != nil {
		panic(err)
	}
	defer s.Close()
	guid := func(a string) uint64 {
		g, err := strconv.ParseUint(strings.TrimPrefix(a, "0x"), 16, 64)
		if err != nil {
			panic(err)
		}
		return g
	}
	if *dump != "" {
		b, err := s.Read(guid(*dump))
		if err != nil {
			panic(err)
		}
		os.Stdout.Write(b)
		return
	}
	if *groups {
		for _, t := range []uint32{0x02D, 0x02E, 0x02F, 0x030} {
			for _, r := range s.ResourcesOfType(t) {
				b, _ := s.Read(r.GUID)
				d, err := stu.Open(b, stu.File, 0)
				if err != nil {
					fmt.Println(err)
					continue
				}
				var name string
				var id uint32
				var refs []string
				for _, f := range d.Decode(d.Root()).Items() {
					switch f.Type() {
					case stu.String:
						name, _ = f.Str()
					case stu.Primitive:
						if raw, err := f.Raw(); err == nil && len(raw) == 4 {
							id = binary.LittleEndian.Uint32(raw)
						}
					}
					if g, _, err := f.Ref(); err == nil && g != 0 {
						refs = append(refs, fmt.Sprintf("%016x", g))
					}
				}
				fmt.Printf("%03X %016x %-40q id %08x fnv-ok %v %v\n", t, r.GUID, name, id, audio.WwiseID(name) == id, refs)
				d.Close()
			}
		}
		return
	}
	if *banks {
		bankStats(s)
		return
	}
	if *all {
		scanAll(s, *install, *ffdir, *every)
		return
	}
	if *raw != "" {
		lib, err := audio.LoadCodebooks(filepath.Join(*install, "GameClientApp.exe"))
		if err != nil {
			panic(err)
		}
		b, err := s.Read(guid(*raw))
		if err != nil {
			panic(err)
		}
		v, err := audio.Convert(b, lib)
		if err != nil {
			panic(err)
		}
		pcm, err := v.Decode()
		if err != nil {
			fmt.Println("decode error:", err)
		}
		f := make([]byte, 0, len(pcm)*len(pcm[0])*4)
		for i := range pcm[0] {
			for c := range pcm {
				f = binary.LittleEndian.AppendUint32(f, math.Float32bits(pcm[c][i]))
			}
		}
		fmt.Println("samples", len(pcm[0]), "channels", len(pcm))
		if err := os.WriteFile(*out, f, 0o644); err != nil {
			panic(err)
		}
		return
	}
	if *ogg != "" {
		lib, err := audio.LoadCodebooks(filepath.Join(*install, "GameClientApp.exe"))
		if err != nil {
			panic(err)
		}
		b, err := s.Read(guid(*ogg))
		if err != nil {
			panic(err)
		}
		v, err := audio.Convert(b, lib)
		if err != nil {
			panic(err)
		}
		fmt.Printf("%+v\npackets %d\n", *v.Header, len(v.Packets))
		if err := os.WriteFile(*out, v.Ogg(), 0o644); err != nil {
			panic(err)
		}
		return
	}
	for _, t := range flag.Args() {
		tt, _ := strconv.ParseUint(t, 16, 32)
		rs := s.ResourcesOfType(uint32(tt))
		magic := map[string]int{}
		ex := map[string]uint64{}
		var tot uint64
		for _, r := range rs {
			b, err := s.Read(r.GUID)
			if err != nil {
				magic["err "+err.Error()]++
				continue
			}
			tot += uint64(len(b))
			m := hex.EncodeToString(b[:min(4, len(b))])
			if len(b) >= 4 {
				m = fmt.Sprintf("%q", b[:4])
			}
			if len(b) >= 4 && string(b[:4]) == "RIFF" {
				w, err := audio.ParseWEM(b)
				if err != nil {
					m += " error " + err.Error()
				} else {
					var ids []string
					for _, c := range w.Chunks {
						ids = append(ids, c.ID)
					}
					m += fmt.Sprintf(" ch=%d rate=%d bs=%d/%d mod=%v chunks=%s", w.Channels, w.SampleRate, w.Blocksize0,
						w.Blocksize1, w.ModPackets, strings.Join(ids, ","))
				}
			}
			magic[m]++
			if _, ok := ex[m]; !ok {
				ex[m] = r.GUID
			}
		}
		fmt.Printf("type %03X: %d resources, %d bytes\n", tt, len(rs), tot)
		keys := make([]string, 0, len(magic))
		for k := range magic {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return magic[keys[i]] > magic[keys[j]] })
		for _, k := range keys {
			fmt.Printf("   %6d %s  e.g. %016x\n", magic[k], k, ex[k])
		}
	}
}

func scanAll(s *content.Store, install, ffdir string, every int) {
	lib, err := audio.LoadCodebooks(filepath.Join(install, "GameClientApp.exe"))
	if err != nil {
		panic(err)
	}
	var rs []content.Resource
	rs = append(rs, s.ResourcesOfType(audio.TypeWEM)...)
	rs = append(rs, s.ResourcesOfType(audio.TypeWEMStream)...)
	t0 := time.Now()
	type result struct {
		r       content.Resource
		err     string
		samples int
		secs    float64
		maxDiff float64
		cmp     bool
	}
	ch := make(chan int)
	res := make([]result, len(rs))
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				r := rs[i]
				out := result{r: r, maxDiff: -1}
				b, err := s.Read(r.GUID)
				if err == nil {
					var v *audio.Vorbis
					if v, err = audio.Convert(b, lib); err == nil {
						var pcm [][]float32
						pcm, err = v.Decode()
						if err == nil {
							out.samples = len(pcm[0])
							out.secs = v.Header.Seconds()
							if out.samples != v.Header.Samples {
								err = fmt.Errorf("decoded %d samples, header %d", out.samples, v.Header.Samples)
							}
							if err == nil && ffdir != "" && i%every == 0 {
								out.cmp = true
								out.maxDiff, err = compare(ffdir, r.GUID, v, pcm)
							}
						}
					}
				}
				if err != nil {
					out.err = err.Error()
				}
				res[i] = out
			}
		}()
	}
	for i := range rs {
		ch <- i
	}
	close(ch)
	wg.Wait()
	fails := map[string]int{}
	var secs, worst float64
	cmps := 0
	for _, x := range res {
		if x.err != "" {
			fails[x.err]++
			if fails[x.err] <= 3 {
				fmt.Printf("FAIL %016x (type %03X): %s\n", x.r.GUID, x.r.Type, x.err)
			}
		}
		secs += x.secs
		if x.cmp && x.err == "" {
			cmps++
			worst = math.Max(worst, x.maxDiff)
		}
	}
	fmt.Printf("%d WEMs, %d failed (%d kinds), %.0f s of audio (%.1f h), decoded in %.1f s\n", len(rs), len(res)-len(rs)+countFails(fails),
		len(fails), secs, secs/3600, time.Since(t0).Seconds())
	if ffdir != "" {
		fmt.Printf("ffmpeg comparison: %d WEMs, worst max sample difference %.3g\n", cmps, worst)
	}
}

func countFails(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// compare decodes the rebuilt Ogg with ffmpeg and returns the largest sample difference to pcm.
func compare(dir string, g uint64, v *audio.Vorbis, pcm [][]float32) (float64, error) {
	ogg := filepath.Join(dir, fmt.Sprintf("%016x.ogg", g))
	if err := os.WriteFile(ogg, v.Ogg(), 0o644); err != nil {
		return 0, err
	}
	defer os.Remove(ogg)
	b, err := exec.Command("ffmpeg", "-v", "error", "-i", ogg, "-f", "f32le", "-").Output()
	if err != nil {
		return 0, fmt.Errorf("ffmpeg: %w", err)
	}
	ch := len(pcm)
	n := len(b) / 4 / ch
	if n != len(pcm[0]) {
		return 0, fmt.Errorf("ffmpeg decoded %d samples, we %d", n, len(pcm[0]))
	}
	var md float64
	for i := range n {
		for c := range ch {
			f := math.Float32frombits(binary.LittleEndian.Uint32(b[(i*ch+c)*4:]))
			md = math.Max(md, math.Abs(float64(f-pcm[c][i])))
		}
	}
	if md > 1e-4 {
		return md, fmt.Errorf("differs from ffmpeg by %.3g", md)
	}
	return md, nil
}

func bankStats(s *content.Store) {
	cnt := map[string]int{}
	ex := map[string]uint64{}
	add := func(k string, g uint64) {
		cnt[k]++
		if _, ok := ex[k]; !ok {
			ex[k] = g
		}
	}
	for _, r := range s.ResourcesOfType(audio.TypeBank) {
		b, err := s.Read(r.GUID)
		if err != nil {
			add("read error", r.GUID)
			continue
		}
		bk, err := audio.ParseBank(b)
		if err != nil {
			add("error "+err.Error(), r.GUID)
			continue
		}
		var ids []string
		for _, c := range bk.Chunks {
			ids = append(ids, c.ID)
		}
		add(fmt.Sprintf("version %d chunks %s", bk.Version, strings.Join(ids, ",")), r.GUID)
		objs := map[uint32]uint8{}
		for _, o := range bk.Objects {
			objs[o.ID] = o.Type
		}
		for _, o := range bk.Objects {
			add(fmt.Sprintf("hirc type %2d %s", o.Type, audio.HircTypeNames[o.Type]), r.GUID)
			if sd, ok := o.Sound(); ok {
				add(fmt.Sprintf("sound plugin %08x stream %d", sd.Plugin, sd.StreamType), r.GUID)
			}
			if p, ok := o.Parent(); ok {
				t, in := objs[p]
				switch {
				case p == 0:
					add(fmt.Sprintf("parent of %d: none", o.Type), r.GUID)
				case in:
					add(fmt.Sprintf("parent of %d: type %d in bank", o.Type, t), r.GUID)
				default:
					add(fmt.Sprintf("parent of %d: not in bank", o.Type), r.GUID)
				}
			}
			if o.Type == 4 {
				acts, ok := o.Actions()
				add(fmt.Sprintf("event parse ok %v", ok), r.GUID)
				for _, a := range acts {
					add(fmt.Sprintf("event action in bank %v", objs[a] == 3), r.GUID)
				}
			}
			if a, ok := o.Action(); ok {
				t, in := objs[a.Target]
				add(fmt.Sprintf("action type %04x bus %v target type %d in bank %v", a.Type, a.IsBus, t, in), r.GUID)
			}
		}
	}
	keys := make([]string, 0, len(cnt))
	for k := range cnt {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("%7d %s  e.g. %016x\n", cnt[k], k, ex[k])
	}
}
