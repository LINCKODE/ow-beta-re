package audio

import (
	"encoding/binary"
	"sync"

	"owcontent/content"
	"owexplorer/internal/refindex"
	"owstu/stu"
)

// Source adds audio references to the reference index (docs/Explorer.md "Plugging in a format"). The STU source
// already has sound → bank and sound → WEM (fields); this adds what only the bank says:
//   - audio.source: bank → the WEMs its Sound objects play (HIRC source id → the owning STUSound's WEM with that id);
//   - audio.group: bank → the switch groups, switches, state groups and parameters (0x02D–0x030) whose Wwise id
//     occurs in its HIRC objects (a byte match: the node parameters that hold them aren't parsed) [?];
//
// and names the 0x02D–0x030 resources ("WEAPON_TAIL").
type Source struct {
	mu    sync.Mutex
	ids   map[uint32][]uint64 // Wwise id → 0x02D–0x030 resources
	Stats SourceStats
}

// SourceStats are the last build's counts (tests, docs).
type SourceStats struct {
	Sounds, Banks, SoundObjects, Matched, NoWEM, Unmatched, GroupEdges int
}

const (
	kindSource = iota
	kindGroup
)

func (*Source) ID() string   { return "audio" }
func (*Source) Version() int { return 1 }
func (*Source) Kinds() []refindex.KindInfo {
	return []refindex.KindInfo{{Name: "audio.source", Label: "plays WEM"},
		{Name: "audio.group", Label: "switch / state / parameter id in bank"}}
}
func (*Source) Workers() int { return 8 }

func isNamed(t uint32) bool { return t >= TypeSwitchGroup && t <= TypeStateGroup }

func (*Source) Select(_ *refindex.Ctx, r content.Resource) bool {
	return r.Type == TypeSound || isNamed(r.Type)
}

// Prepare reads the 0x02D–0x030 ids.
func (s *Source) Prepare(c *refindex.Ctx) error {
	s.ids = map[uint32][]uint64{}
	s.Stats = SourceStats{}
	for _, t := range []uint32{TypeSwitchGroup, TypeSwitch, TypeParameter, TypeStateGroup} {
		for _, r := range c.Store.ResourcesOfType(t) {
			n, err := readNamedResource(c.Store.Read, r.GUID)
			if err == nil && n.ID != 0 {
				s.ids[n.ID] = append(s.ids[n.ID], r.GUID)
			}
		}
	}
	return nil
}

func readNamedResource(read Reader, g uint64) (Named, error) {
	b, err := read(g)
	if err != nil {
		return Named{}, err
	}
	d, err := stu.Open(b, stu.File, 0)
	if err != nil {
		return Named{}, err
	}
	defer d.Close()
	return ReadNamed(d), nil
}

func (s *Source) Scan(c *refindex.Ctx, r content.Resource, e *refindex.Emitter) error {
	if isNamed(r.Type) {
		n, err := readNamedResource(c.Store.Read, r.GUID)
		if err != nil {
			return err
		}
		e.Name(r.GUID, n.Name, "audio", false)
		return nil
	}
	si, err := OpenSound(c.Store.Read, r.GUID)
	if err != nil {
		return err
	}
	st := SourceStats{Sounds: 1}
	defer func() {
		s.mu.Lock()
		s.Stats.add(st)
		s.mu.Unlock()
	}()
	if si.Bank == 0 {
		return nil
	}
	b, err := c.Store.Read(si.Bank)
	if err != nil {
		return err
	}
	bk, err := ParseBank(b)
	if err != nil {
		return err
	}
	st.Banks = 1
	wems := map[uint32]uint64{}
	for _, f := range si.Files {
		wems[f.ID] = f.GUID
	}
	seen := map[uint64]bool{}
	for i := range bk.Objects {
		o := &bk.Objects[i]
		if sd, ok := o.Sound(); ok {
			st.SoundObjects++
			switch g, ok := wems[sd.SourceID]; {
			case ok:
				st.Matched++
				e.Ref(si.Bank, g, kindSource, 0)
			case sd.Plugin&0x0F == 2 || sd.Plugin == 0x00080001:
				st.NoWEM++ // source plugins (silence …) and 0x00080001 have no WEM
			default:
				st.Unmatched++
			}
		}
		body := o.Body()
		for k := 0; k+4 <= len(body); k++ {
			for _, g := range s.ids[binary.LittleEndian.Uint32(body[k:])] {
				if !seen[g] {
					seen[g] = true
					st.GroupEdges++
					e.Ref(si.Bank, g, kindGroup, 0)
				}
			}
		}
	}
	return nil
}

func (a *SourceStats) add(b SourceStats) {
	a.Sounds += b.Sounds
	a.Banks += b.Banks
	a.SoundObjects += b.SoundObjects
	a.Matched += b.Matched
	a.NoWEM += b.NoWEM
	a.Unmatched += b.Unmatched
	a.GroupEdges += b.GroupEdges
}
