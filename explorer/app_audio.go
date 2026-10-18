package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"owcontent/content"
	"owexplorer/internal/audio"
	"owexplorer/internal/catalog"
	"owstu/stu"
)

// Audio (T065): the Audio tab's data (App.Audio) for WEMs (03F, 0B2), sounds (02C), banks (043) and switch / state
// groups, switches and parameters (02D–030); decoded sound served over the asset server's handler (/audio/<guid>.pcm:
// the player's samples, played with WebAudio; .wav, .ogg); waveform peaks; export. docs/AssetFormats.md §5.

type decodedWEM struct {
	v   *audio.Vorbis
	pcm [][]float32
}

var (
	audioCache = newLRU[*decodedWEM](384<<20, func(d *decodedWEM) int { return 1 + 4*len(d.pcm)*len(d.pcm[0]) })

	libMu     sync.Mutex
	audioLibs = map[string]*audio.Codebooks{} // per install
	libErrs   = map[string]error{}
)

// codebooks reads the Wwise codebook library from the install's executable (once per install).
func codebooks(c *catalog.Catalog) (*audio.Codebooks, error) {
	libMu.Lock()
	defer libMu.Unlock()
	if l, ok := audioLibs[c.Install]; ok {
		return l, nil
	}
	if err, ok := libErrs[c.Install]; ok {
		return nil, err
	}
	l, err := audio.LoadCodebooks(filepath.Join(c.Install, "GameClientApp.exe"))
	if err != nil {
		libErrs[c.Install] = err
		return nil, err
	}
	audioLibs[c.Install] = l
	return l, nil
}

func isWEM(t uint32) bool { return t == audio.TypeWEM || t == audio.TypeWEMStream }

func isAudio(t uint32) bool {
	return isWEM(t) || t == audio.TypeSound || t == audio.TypeBank || (t >= audio.TypeSwitchGroup && t <= audio.TypeStateGroup)
}

// convertWEM rebuilds a WEM's Vorbis packets (no decoding).
func convertWEM(c *catalog.Catalog, g uint64) (*audio.Vorbis, error) {
	if !isWEM(content.TypeOf(g)) {
		return nil, fmt.Errorf("%016x is not a WEM", g)
	}
	lib, err := codebooks(c)
	if err != nil {
		return nil, err
	}
	b, err := c.Store.Read(g)
	if err != nil {
		return nil, err
	}
	return audio.Convert(b, lib)
}

// decodeWEM converts and decodes a WEM (cached).
func decodeWEM(c *catalog.Catalog, g uint64) (*decodedWEM, error) {
	key := fmt.Sprintf("%s/%016x", c.Describe, g)
	if d, ok := audioCache.get(key); ok {
		return d, nil
	}
	v, err := convertWEM(c, g)
	if err != nil {
		return nil, err
	}
	pcm, err := v.Decode()
	if err != nil {
		return nil, err
	}
	d := &decodedWEM{v, pcm}
	audioCache.put(key, d)
	return d, nil
}

// wav encodes 16-bit PCM (channel order as Vorbis: L R, or FL FR RL RR).
func wav(rate int, pcm [][]float32) []byte {
	ch, n := len(pcm), 0
	if ch > 0 {
		n = len(pcm[0])
	}
	data := n * ch * 2
	var b bytes.Buffer
	le := binary.LittleEndian
	b.WriteString("RIFF")
	binary.Write(&b, le, uint32(36+data))
	b.WriteString("WAVEfmt ")
	for _, f := range []any{uint32(16), uint16(1), uint16(ch), uint32(rate), uint32(rate * ch * 2), uint16(ch * 2), uint16(16)} {
		binary.Write(&b, le, f)
	}
	b.WriteString("data")
	binary.Write(&b, le, uint32(data))
	out := make([]byte, data)
	k := 0
	for i := range n {
		for c := range ch {
			x := math.Round(float64(pcm[c][i]) * 32767)
			le.PutUint16(out[k:], uint16(int16(max(-32768, min(32767, x)))))
			k += 2
		}
	}
	b.Write(out)
	return b.Bytes()
}

// pcm16 is the player's format: WebAudio plays it from an AudioBuffer, so the webview decodes nothing (WebKitGTK's
// media element can't load from the asset server's scheme: FormatError).
func pcm16(rate int, pcm [][]float32) []byte {
	n := 0
	if len(pcm) > 0 {
		n = len(pcm[0])
	}
	out := make([]byte, 16+2*n*len(pcm))
	copy(out, "OWPC")
	le := binary.LittleEndian
	le.PutUint32(out[4:], uint32(len(pcm)))
	le.PutUint32(out[8:], uint32(rate))
	le.PutUint32(out[12:], uint32(n))
	k := 16
	for _, ch := range pcm {
		for _, x := range ch {
			le.PutUint16(out[k:], uint16(int16(max(-32768, min(32767, math.Round(float64(x)*32767))))))
			k += 2
		}
	}
	return out
}

// serveAudio answers /audio/<guid>.pcm (the player), .wav and .ogg (imageHandler).
func (a *App) serveAudio(w http.ResponseWriter, r *http.Request, c *catalog.Catalog, file string) {
	name, ext, _ := strings.Cut(file, ".")
	g, err := parse(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var b []byte
	switch ext {
	case "wav":
		var d *decodedWEM
		if d, err = decodeWEM(c, g); err == nil {
			b = wav(d.v.Header.SampleRate, d.pcm)
		}
		w.Header().Set("Content-Type", "audio/wav")
	case "pcm": // the player's input: "OWPC", u32 channels, rate, frames, then s16 planar (little-endian)
		var d *decodedWEM
		if d, err = decodeWEM(c, g); err == nil {
			b = pcm16(d.v.Header.SampleRate, d.pcm)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
	case "ogg":
		var v *audio.Vorbis
		if v, err = convertWEM(c, g); err == nil {
			b = v.Ogg()
		}
		w.Header().Set("Content-Type", "audio/ogg")
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "max-age=3600")
	http.ServeContent(w, r, file, time.Time{}, bytes.NewReader(b)) // ranges: the media element seeks with them
}

// ---- the Audio tab ------------------------------------------------------------------------------------------------

type AudioView struct {
	Kind  string     `json:"kind"` // wem, sound, bank, named
	WEM   *WEMView   `json:"wem,omitempty"`
	Sound *SoundView `json:"sound,omitempty"`
	Bank  *BankView  `json:"bank,omitempty"`
	Named *NamedView `json:"named,omitempty"`
	Ready bool       `json:"ready"` // reference index loaded (lists below come from it)
}

type WEMView struct {
	GUID    string     `json:"guid"`
	Header  *audio.WEM `json:"header,omitempty"`
	Seconds float64    `json:"seconds"`
	Packets int        `json:"packets"`
	Voice   bool       `json:"voice"` // 0x0B2
	Error   string     `json:"error,omitempty"`
	Sounds  []string   `json:"sounds"`       // STUSounds listing it
	Banks   []string   `json:"banks"`        // banks whose Sound objects play it (audio.source)
	Users   []string   `json:"users"`        // other STU users (voice lines)
	ID      string     `json:"id,omitempty"` // Wwise source id (from a sound listing it)
}

type WEMRow struct {
	GUID     string  `json:"guid"`
	ID       string  `json:"id"`
	Stream   bool    `json:"stream"`
	Seconds  float64 `json:"seconds"`
	Channels int     `json:"channels"`
	Rate     int     `json:"rate"`
	Error    string  `json:"error,omitempty"`
}

// HircNode is a bank object in a tree: events → actions → targets → children (by parent id) → sounds → WEMs.
type HircNode struct {
	ID       string     `json:"id"`
	Type     uint8      `json:"type"`
	TypeName string     `json:"typeName"`
	Detail   string     `json:"detail,omitempty"`
	WEM      string     `json:"wem,omitempty"`
	Seconds  float64    `json:"seconds,omitempty"`
	Missing  bool       `json:"missing,omitempty"` // referenced id not in the bank
	Children []HircNode `json:"children,omitempty"`
	More     int        `json:"more,omitempty"` // children left out
}

type SoundView struct {
	EventPlay string     `json:"eventPlay"`
	EventStop string     `json:"eventStop"`
	Bank      string     `json:"bank,omitempty"`
	BankID    string     `json:"bankId"`
	Files     []WEMRow   `json:"files"`
	Events    []HircNode `json:"events"` // the play (and stop) event trees
	Error     string     `json:"error,omitempty"`
}

type HircRow struct {
	ID       string `json:"id"`
	Type     uint8  `json:"type"`
	TypeName string `json:"typeName"`
	Size     int    `json:"size"`
	Offset   int    `json:"offset"`
	Parent   string `json:"parent,omitempty"`
	Detail   string `json:"detail,omitempty"`
	WEM      string `json:"wem,omitempty"`
}

type BankView struct {
	Version uint32         `json:"version"`
	ID      string         `json:"id"`
	Chunks  []audio.Chunk  `json:"chunks"`
	Counts  map[string]int `json:"counts"`
	Objects []HircRow      `json:"objects"`
	Events  []HircNode     `json:"events"`
	Sounds  []string       `json:"sounds"` // STUSounds using the bank
	Groups  []NamedRow     `json:"groups"` // audio.group
	Error   string         `json:"error,omitempty"`
}

type NamedRow struct {
	GUID string `json:"guid"`
	Type uint32 `json:"type"`
	Name string `json:"name"`
	ID   string `json:"id"`
}

type NamedView struct {
	Label    string     `json:"label"` // switch group, switch, parameter, state group
	Name     string     `json:"name"`
	ID       string     `json:"id"`
	HashOK   bool       `json:"hashOk"` // ID = FNV-1(lower-case name)
	Group    *NamedRow  `json:"group,omitempty"`
	Switches []NamedRow `json:"switches"` // a group's switches
	Same     []NamedRow `json:"same"`     // other resources with the same Wwise id
	Banks    []string   `json:"banks"`    // audio.group
	Error    string     `json:"error,omitempty"`
}

var namedLabel = map[uint32]string{audio.TypeSwitchGroup: "switch group", audio.TypeSwitch: "switch",
	audio.TypeParameter: "game parameter", audio.TypeStateGroup: "state group"}

// neighbours lists one side of a resource from the reference index (all pages), filtered by kinds and types.
func neighbours(c *catalog.Catalog, g uint64, dir string, kinds []string, types ...int) ([]string, bool) {
	q := catalog.NeighborQuery{GUID: fmt.Sprintf("%016x", g), Dir: dir, Kinds: kinds, Types: types, NoMissing: true, Count: 500}
	var out []string
	for {
		p, err := c.Neighbors(q)
		if err != nil || !p.Ready {
			return out, false
		}
		for _, r := range p.Rows {
			out = append(out, r.GUID)
		}
		q.Start += len(p.Rows)
		if len(p.Rows) == 0 || q.Start >= p.Total {
			return out, true
		}
	}
}

func (a *App) Audio(guid string) (AudioView, error) {
	c, err := a.catalog()
	if err != nil {
		return AudioView{}, err
	}
	g, err := parse(guid)
	if err != nil {
		return AudioView{}, err
	}
	t := content.TypeOf(g)
	v := AudioView{Ready: c.Progress().Phase == "done"}
	switch {
	case isWEM(t):
		v.Kind, v.WEM = "wem", a.wemView(c, g)
	case t == audio.TypeSound:
		v.Kind, v.Sound = "sound", a.soundView(c, g)
	case t == audio.TypeBank:
		v.Kind, v.Bank = "bank", a.bankView(c, g)
	case t >= audio.TypeSwitchGroup && t <= audio.TypeStateGroup:
		v.Kind, v.Named = "named", a.namedView(c, g)
	default:
		return v, fmt.Errorf("%016x is not an audio resource", g)
	}
	return v, nil
}

func (a *App) wemView(c *catalog.Catalog, g uint64) *WEMView {
	w := &WEMView{GUID: fmt.Sprintf("%016x", g), Voice: content.TypeOf(g) == audio.TypeWEMStream,
		Sounds: []string{}, Banks: []string{}, Users: []string{}}
	b, err := c.Store.Read(g)
	if err == nil {
		w.Header, err = audio.ParseWEM(b)
		if w.Header != nil {
			w.Seconds = w.Header.Seconds()
		}
	}
	if err == nil {
		var v *audio.Vorbis
		if v, err = convertWEM(c, g); err == nil {
			w.Packets = len(v.Packets)
		}
	}
	if err != nil {
		w.Error = err.Error()
	}
	w.Sounds, _ = neighbours(c, g, "in", []string{"stu.field"}, audio.TypeSound)
	w.Banks, _ = neighbours(c, g, "in", []string{"audio.source"})
	all, _ := neighbours(c, g, "in", []string{"stu.field"})
	for _, u := range all {
		if h, ok := catalog.ParseGUID(u); ok && content.TypeOf(h) != audio.TypeSound {
			w.Users = append(w.Users, u)
		}
	}
	if len(w.Sounds) > 0 {
		if h, ok := catalog.ParseGUID(w.Sounds[0]); ok {
			if si, err := audio.OpenSound(c.Store.Read, h); err == nil {
				for _, f := range si.Files {
					if f.GUID == g {
						w.ID = fmt.Sprintf("%08x", f.ID)
					}
				}
			}
		}
	}
	return w
}

func wemRow(c *catalog.Catalog, f audio.WEMFile) WEMRow {
	r := WEMRow{GUID: fmt.Sprintf("%016x", f.GUID), ID: fmt.Sprintf("%08x", f.ID), Stream: f.Stream}
	b, err := c.Store.Read(f.GUID)
	if err == nil {
		var h *audio.WEM
		if h, err = audio.ParseWEM(b); err == nil {
			r.Seconds, r.Channels, r.Rate = h.Seconds(), h.Channels, h.SampleRate
		}
	}
	if err != nil {
		r.Error = err.Error()
	}
	return r
}

// bankTree builds HircNode trees over one bank.
type bankTree struct {
	c        *catalog.Catalog
	objs     map[uint32]*audio.HircObject
	children map[uint32][]uint32
	wems     map[uint32]uint64 // source id → WEM
	secs     map[uint64]float64
}

func newBankTree(c *catalog.Catalog, bk *audio.Bank, files []audio.WEMFile) *bankTree {
	t := &bankTree{c: c, objs: map[uint32]*audio.HircObject{}, children: map[uint32][]uint32{},
		wems: map[uint32]uint64{}, secs: map[uint64]float64{}}
	for i := range bk.Objects {
		o := &bk.Objects[i]
		t.objs[o.ID] = o
		if p, ok := o.Parent(); ok && p != 0 {
			t.children[p] = append(t.children[p], o.ID)
		}
	}
	for _, f := range files {
		t.wems[f.ID] = f.GUID
	}
	return t
}

func (t *bankTree) seconds(g uint64) float64 {
	if s, ok := t.secs[g]; ok {
		return s
	}
	var s float64
	if b, err := t.c.Store.Read(g); err == nil {
		if h, err := audio.ParseWEM(b); err == nil {
			s = h.Seconds()
		}
	}
	t.secs[g] = s
	return s
}

// actionNames: the action type's high byte (Wwise's AkActionType numbering; the low byte is the scope) [?].
var actionNames = map[uint16]string{0x01: "Stop", 0x02: "Pause", 0x03: "Resume", 0x04: "Play", 0x05: "Play and continue",
	0x06: "Mute", 0x07: "Unmute", 0x08: "Set pitch", 0x09: "Reset pitch", 0x0A: "Set volume", 0x0B: "Reset volume",
	0x0C: "Set bus volume", 0x0D: "Reset bus volume", 0x0E: "Set LPF", 0x0F: "Reset LPF", 0x10: "Use state",
	0x11: "Unuse state", 0x12: "Set state", 0x13: "Set game parameter", 0x14: "Reset game parameter",
	0x19: "Set switch", 0x1A: "Toggle bypass", 0x1B: "Reset bypass", 0x1C: "Break", 0x1D: "Trigger", 0x1E: "Seek"}

func pluginName(p uint32) string {
	switch p {
	case 0x00040001:
		return "Vorbis"
	case 0x00080001:
		return "external source"
	case 0x00650002:
		return "silence"
	}
	return fmt.Sprintf("plugin %08x", p)
}

// node describes object id with its subtree (depth-limited; seen guards against cycles).
func (t *bankTree) node(id uint32, depth int, seen map[uint32]bool) HircNode {
	o, ok := t.objs[id]
	if !ok {
		return HircNode{ID: fmt.Sprintf("%08x", id), TypeName: "not in this bank", Missing: true}
	}
	n := HircNode{ID: fmt.Sprintf("%08x", id), Type: o.Type, TypeName: audio.HircTypeNames[o.Type]}
	if n.TypeName == "" {
		n.TypeName = fmt.Sprintf("type %d", o.Type)
	}
	if seen[id] || depth > 12 {
		n.Detail = "…"
		return n
	}
	seen[id] = true
	defer delete(seen, id)
	var kids []uint32
	switch o.Type {
	case 2:
		if s, ok := o.Sound(); ok {
			n.Detail = fmt.Sprintf("%s · source %08x", pluginName(s.Plugin), s.SourceID)
			if s.StreamType == 2 {
				n.Detail += " · streamed"
			}
			if g, ok := t.wems[s.SourceID]; ok {
				n.WEM = fmt.Sprintf("%016x", g)
				n.Seconds = t.seconds(g)
			}
		}
	case 3:
		if a, ok := o.Action(); ok {
			name := actionNames[a.Type>>8]
			if name == "" {
				name = "?"
			}
			n.Detail = fmt.Sprintf("%s (0x%04x) → %08x", name, a.Type, a.Target)
			if _, in := t.objs[a.Target]; in && a.Target != 0 {
				kids = []uint32{a.Target}
			}
		}
	case 4:
		kids, _ = o.Actions()
		n.Detail = fmt.Sprintf("%d actions", len(kids))
	default:
		kids = t.children[id]
		if len(kids) > 0 {
			n.Detail = fmt.Sprintf("%d children", len(kids))
		}
	}
	const maxKids = 200
	for i, k := range kids {
		if i == maxKids {
			n.More = len(kids) - maxKids
			break
		}
		n.Children = append(n.Children, t.node(k, depth+1, seen))
	}
	return n
}

func (a *App) soundView(c *catalog.Catalog, g uint64) *SoundView {
	v := &SoundView{Files: []WEMRow{}, Events: []HircNode{}}
	si, err := audio.OpenSound(c.Store.Read, g)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	v.EventPlay, v.EventStop, v.BankID = fmt.Sprintf("%08x", si.EventPlay), fmt.Sprintf("%08x", si.EventStop),
		fmt.Sprintf("%08x", si.BankID)
	for _, f := range si.Files {
		v.Files = append(v.Files, wemRow(c, f))
	}
	if si.Bank == 0 {
		return v
	}
	v.Bank = fmt.Sprintf("%016x", si.Bank)
	b, err := c.Store.Read(si.Bank)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	bk, err := audio.ParseBank(b)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	t := newBankTree(c, bk, si.Files)
	for _, e := range []uint32{si.EventPlay, si.EventStop} {
		if e != 0 {
			v.Events = append(v.Events, t.node(e, 0, map[uint32]bool{}))
		}
	}
	return v
}

func (a *App) bankView(c *catalog.Catalog, g uint64) *BankView {
	v := &BankView{Counts: map[string]int{}, Objects: []HircRow{}, Events: []HircNode{}, Sounds: []string{},
		Groups: []NamedRow{}, Chunks: []audio.Chunk{}}
	b, err := c.Store.Read(g)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	bk, err := audio.ParseBank(b)
	if err != nil {
		v.Error = err.Error()
	}
	if bk == nil {
		return v
	}
	v.Version, v.ID, v.Chunks = bk.Version, fmt.Sprintf("%08x", bk.ID), bk.Chunks
	v.Sounds, _ = neighbours(c, g, "in", []string{"stu.field"}, audio.TypeSound)
	var files []audio.WEMFile
	for _, s := range v.Sounds {
		if h, ok := catalog.ParseGUID(s); ok {
			if si, err := audio.OpenSound(c.Store.Read, h); err == nil {
				files = append(files, si.Files...)
			}
		}
	}
	t := newBankTree(c, bk, files)
	for i := range bk.Objects {
		o := &bk.Objects[i]
		name := audio.HircTypeNames[o.Type]
		if name == "" {
			name = fmt.Sprintf("type %d", o.Type)
		}
		v.Counts[name]++
		row := HircRow{ID: fmt.Sprintf("%08x", o.ID), Type: o.Type, TypeName: name, Size: o.Size, Offset: o.Offset}
		if p, ok := o.Parent(); ok && p != 0 {
			row.Parent = fmt.Sprintf("%08x", p)
		}
		n := t.node(o.ID, 12, map[uint32]bool{}) // depth 12: the detail only
		row.Detail, row.WEM = n.Detail, n.WEM
		v.Objects = append(v.Objects, row)
		if o.Type == 4 {
			v.Events = append(v.Events, t.node(o.ID, 0, map[uint32]bool{}))
		}
	}
	groups, _ := neighbours(c, g, "out", []string{"audio.group"})
	for _, s := range groups {
		if h, ok := catalog.ParseGUID(s); ok {
			v.Groups = append(v.Groups, namedRow(c, h))
		}
	}
	return v
}

func namedRow(c *catalog.Catalog, g uint64) NamedRow {
	r := NamedRow{GUID: fmt.Sprintf("%016x", g), Type: content.TypeOf(g)}
	if b, err := c.Store.Read(g); err == nil {
		if d, err := stuOpen(b); err == nil {
			n := audio.ReadNamed(d)
			d.Close()
			r.Name, r.ID = n.Name, fmt.Sprintf("%08x", n.ID)
		}
	}
	return r
}

func (a *App) namedView(c *catalog.Catalog, g uint64) *NamedView {
	t := content.TypeOf(g)
	v := &NamedView{Label: namedLabel[t], Switches: []NamedRow{}, Same: []NamedRow{}, Banks: []string{}}
	b, err := c.Store.Read(g)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	d, err := stuOpen(b)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	n := audio.ReadNamed(d)
	d.Close()
	v.Name, v.ID, v.HashOK = n.Name, fmt.Sprintf("%08x", n.ID), n.Name != "" && audio.WwiseID(n.Name) == n.ID
	if n.Group != 0 {
		r := namedRow(c, n.Group)
		v.Group = &r
	}
	for _, tt := range []uint32{audio.TypeSwitchGroup, audio.TypeSwitch, audio.TypeParameter, audio.TypeStateGroup} {
		for _, r := range c.Store.ResourcesOfType(tt) {
			if r.GUID == g {
				continue
			}
			row := namedRow(c, r.GUID)
			if row.ID == v.ID {
				v.Same = append(v.Same, row)
			}
			if t == audio.TypeSwitchGroup && tt == audio.TypeSwitch {
				if b, err := c.Store.Read(r.GUID); err == nil {
					if d, err := stuOpen(b); err == nil {
						if audio.ReadNamed(d).Group == g {
							v.Switches = append(v.Switches, row)
						}
						d.Close()
					}
				}
			}
		}
	}
	v.Banks, _ = neighbours(c, g, "in", []string{"audio.group"})
	return v
}

// AudioPeaks is the waveform: per channel, n buckets of (min, max).
type AudioPeaks struct {
	Channels [][]float32 `json:"channels"` // 2n values per channel: min, max, …
	Seconds  float64     `json:"seconds"`
	Rate     int         `json:"rate"`
	Peak     float32     `json:"peak"`
	RMS      float32     `json:"rms"`
}

func (a *App) AudioPeaks(guid string, n int) (AudioPeaks, error) {
	c, err := a.catalog()
	if err != nil {
		return AudioPeaks{}, err
	}
	g, err := parse(guid)
	if err != nil {
		return AudioPeaks{}, err
	}
	d, err := decodeWEM(c, g)
	if err != nil {
		return AudioPeaks{}, err
	}
	n = max(16, min(n, 8192))
	p := AudioPeaks{Rate: d.v.Header.SampleRate, Seconds: d.v.Header.Seconds()}
	var sum float64
	total := 0
	for _, ch := range d.pcm {
		out := make([]float32, 2*n)
		for i := range n {
			lo, hi := i*len(ch)/n, (i+1)*len(ch)/n
			mn, mx := float32(0), float32(0)
			for _, x := range ch[lo:hi] {
				mn, mx = min(mn, x), max(mx, x)
			}
			out[2*i], out[2*i+1] = mn, mx
			p.Peak = max(p.Peak, -mn, mx)
		}
		for _, x := range ch {
			sum += float64(x) * float64(x)
		}
		total += len(ch)
		p.Channels = append(p.Channels, out)
	}
	if total > 0 {
		p.RMS = float32(math.Sqrt(sum / float64(total)))
	}
	return p, nil
}

// audioFile renders a WEM as "wav" or "ogg".
func audioFile(c *catalog.Catalog, g uint64, format string) ([]byte, error) {
	switch format {
	case "wav":
		d, err := decodeWEM(c, g)
		if err != nil {
			return nil, err
		}
		return wav(d.v.Header.SampleRate, d.pcm), nil
	case "ogg":
		v, err := convertWEM(c, g)
		if err != nil {
			return nil, err
		}
		return v.Ogg(), nil
	}
	return nil, fmt.Errorf("format %q: wav or ogg", format)
}

// ExportAudio saves a WEM as WAV or Ogg Vorbis.
func (a *App) ExportAudio(guid, format string) (string, error) {
	c, err := a.catalog()
	if err != nil {
		return "", err
	}
	g, err := parse(guid)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%016x.%s", g, format)
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "Export " + name, DefaultFilename: name,
		Filters: []runtime.FileFilter{{DisplayName: strings.ToUpper(format), Pattern: "*." + format}}})
	if err != nil || path == "" {
		return "", err
	}
	b, err := audioFile(c, g, format)
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, b, 0o644)
}

// ExportSoundAll saves every WEM of a sound (or the WEMs a bank plays) into a chosen directory.
func (a *App) ExportSoundAll(guid, format string) (string, error) {
	c, err := a.catalog()
	if err != nil {
		return "", err
	}
	g, err := parse(guid)
	if err != nil {
		return "", err
	}
	wems, err := soundWEMs(c, g)
	if err != nil {
		return "", err
	}
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: fmt.Sprintf("Export %d WEMs of %016x", len(wems), g),
		CanCreateDirectories: true})
	if err != nil || dir == "" {
		return "", err
	}
	for _, w := range wems {
		b, err := audioFile(c, w, format)
		if err != nil {
			return dir, err
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%016x.%s", w, format)), b, 0o644); err != nil {
			return dir, err
		}
	}
	return dir, nil
}

// soundWEMs: a sound's WEMs, a bank's (through its sounds), or the WEM itself.
func soundWEMs(c *catalog.Catalog, g uint64) ([]uint64, error) {
	t := content.TypeOf(g)
	var sounds []uint64
	switch {
	case isWEM(t):
		return []uint64{g}, nil
	case t == audio.TypeSound:
		sounds = []uint64{g}
	case t == audio.TypeBank:
		ss, _ := neighbours(c, g, "in", []string{"stu.field"}, audio.TypeSound)
		for _, s := range ss {
			if h, ok := catalog.ParseGUID(s); ok {
				sounds = append(sounds, h)
			}
		}
	default:
		return nil, fmt.Errorf("%016x has no WEMs", g)
	}
	seen := map[uint64]bool{}
	var out []uint64
	for _, s := range sounds {
		si, err := audio.OpenSound(c.Store.Read, s)
		if err != nil {
			return nil, err
		}
		for _, f := range si.Files {
			if !seen[f.GUID] {
				seen[f.GUID] = true
				out = append(out, f.GUID)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// audioStatus is the Info tab's decoder line.
func audioStatus(c *catalog.Catalog, g uint64, b []byte) DecoderStatus {
	t := content.TypeOf(g)
	switch {
	case isWEM(t):
		h, err := audio.ParseWEM(b)
		if err != nil {
			return DecoderStatus{Name: "WEM (internal/audio)", Status: "error", Detail: err.Error()}
		}
		return DecoderStatus{Name: "WEM (internal/audio)", Status: "ok", Detail: fmt.Sprintf("Wwise Vorbis, %d ch, %d Hz, %.2f s: see the Audio tab",
			h.Channels, h.SampleRate, h.Seconds())}
	case t == audio.TypeBank:
		bk, err := audio.ParseBank(b)
		if err != nil {
			return DecoderStatus{Name: "Wwise bank (internal/audio)", Status: "error", Detail: err.Error()}
		}
		return DecoderStatus{Name: "Wwise bank (internal/audio)", Status: "ok", Detail: fmt.Sprintf("version %d, %d HIRC objects: see the Audio tab",
			bk.Version, len(bk.Objects))}
	}
	return DecoderStatus{}
}

func stuOpen(b []byte) (*stu.Document, error) { return stu.Open(b, stu.File, 0) }
