package instance

import (
	"math"
	"slices"
	"testing"

	"owserver/internal/heroes"
)

// A reader for the local full update as the client parses it (docs/MatchLogic.md "Hero start-up"),
// independent of the writer: it must consume the payload exactly and return what was meant.

func (r *bitReader) prefix3(w0, w1, w2 uint) uint32 {
	if r.bits(1) == 0 {
		return r.bits(w0)
	}
	if r.bits(1) == 0 {
		return r.bits(w1)
	}
	return r.bits(w2)
}
func (r *bitReader) count() uint32 { return r.prefix3(8, 16, 32) }
func (r *bitReader) small() uint32 { return r.prefix3(4, 8, 16) }

// value reads cef290 without a state reference; it returns the client-side type and bits.
func (r *bitReader) value(t *testing.T) (heroes.Type, uint64) {
	if r.bits(1) != 0 {
		t.Fatal("state reference")
	}
	return r.taggedValue(t)
}

func (r *bitReader) taggedValue(t *testing.T) (heroes.Type, uint64) {
	switch tag := r.bits(4); tag {
	case 1:
		return heroes.TypeInt, uint64(r.bits(4))
	case 2:
		return heroes.TypeInt, uint64(int64(int8(r.bits(8))))
	case 3:
		return heroes.TypeInt, uint64(int64(int16(r.bits(16))))
	case 4:
		return heroes.TypeInt, uint64(int64(int32(r.bits(32))))
	case 8:
		return heroes.TypeFloat, uint64(r.bits(32))
	case 9:
		hi := uint64(r.bits(16))
		if hi == 0 {
			return heroes.TypeInt64, 0
		}
		return heroes.TypeInt64, hi<<48 | uint64(r.count())
	default:
		t.Fatalf("tag %d at bit %d", tag, r.pos)
	}
	return 0, 0
}

type decodedInstance struct {
	id, graph, parent, slot uint32
	vars                    map[uint32]uint64
	active                  map[int][]uint32 // state -> data fields
}

func decodeStartup(t *testing.T, w *bitWriter, graphs map[uint32]*heroes.Graph) (owner map[uint32]uint64, dict []uint32, insts []decodedInstance) {
	r := &bitReader{b: w.buf}
	if r.bits(1) != 1 || r.bits(1) != 0 {
		t.Fatal("present")
	}
	if start := r.prefix3(16, 24, 32); start != 0 {
		t.Fatal("not a full update")
	}
	r.count() // range
	if r.bits(1) != 0 {
		t.Fatal("optional value")
	}
	n := r.count()
	end := r.pos + uint(n)
	if r.bits(1) != 0 || r.bits(1) != 1 || r.bits(1) != 0 || r.count() != 0 {
		t.Fatal("local header")
	}
	id := uint32(0)
	for {
		d := r.small()
		if d == 0 {
			break
		}
		id += d
		in := decodedInstance{id: id, graph: r.bits(16), vars: map[uint32]uint64{}, active: map[int][]uint32{}}
		if r.bits(1) != 0 {
			t.Fatal("owner value")
		}
		if r.bits(1) == 1 {
			in.parent, in.slot = r.count(), r.count()
		}
		insts = append(insts, in)
	}
	owner = map[uint32]uint64{}
	for {
		vid := r.count()
		if vid == 0 {
			break
		}
		if vid == heroes.VarWeaponList {
			if r.bits(1) != 0 || r.bits(4) != 15 || r.bits(3) != 7 {
				t.Fatal("dictionary tag")
			}
			n := r.small()
			if r.bits(1) != 0 {
				t.Fatal("homogeneous")
			}
			for range n {
				_, k := r.taggedValue(t)
				_, v := r.taggedValue(t)
				if k != v {
					t.Fatal("weapon list key != value")
				}
				dict = append(dict, uint32(k))
			}
			continue
		}
		_, owner[vid] = r.value(t)
	}
	for i := range insts {
		in := &insts[i]
		g := graphs[in.graph]
		for _, sv := range g.SyncVars {
			if r.bits(1) == 1 {
				_, in.vars[sv] = r.value(t)
			}
		}
		for {
			vid := r.count()
			if vid == 0 {
				break
			}
			_, in.vars[vid] = r.value(t)
		}
		for si, kind := range g.States {
			switch kind {
			case 'C', 'V':
				continue
			case 'S':
				if r.bits(1) != 0 {
					t.Fatal("has-update state sent")
				}
				continue
			}
			if r.bits(1) == 0 {
				continue
			}
			switch g.StateTypes[si] {
			case 0x3db862c4: // SubScript
				in.active[si] = []uint32{r.small(), r.bits(1)}
			case 0xe811ecbb: // BooleanSwitch
				in.active[si] = []uint32{r.bits(1)}
			// T044 (docs/MatchLogic.md §14.7), local format: Ability u8 flags + cooldown bit + stack
			// predecessor bit; Stack predecessor bit + on-top + under; TrackMovement / CameraTransform
			// one bit; BlinkEffect nothing.
			case 0xf53c5da8:
				in.active[si] = []uint32{r.bits(8), r.bits(1), r.bits(1)}
			case 0xf7d6498c:
				in.active[si] = []uint32{r.bits(1), r.bits(1), r.bits(1)}
			case 0x8de7e50f, 0xa5f89e3f:
				in.active[si] = []uint32{r.bits(1)}
			default:
				in.active[si] = nil
			}
		}
		if r.bits(2) != 3 {
			t.Fatal("events")
		}
	}
	if r.bits(1) != 0 {
		t.Fatal("cee920")
	}
	if r.pos != end {
		t.Fatalf("payload: read %d bits, length says %d", r.pos-(end-uint(n)), n)
	}
	return owner, dict, insts
}

func TestHeroStartupFullUpdate(t *testing.T) {
	for _, h := range heroes.All {
		st, _ := heroes.Start(h.Def)
		w, err := HeroStartupFullUpdate(1000, st)
		if err != nil {
			t.Fatal(err)
		}
		owner, dict, insts := decodeStartup(t, w, heroes.Graphs)
		if len(insts) != len(st.Instances) || len(dict) != len(st.Weapons)+1 { // + the filler entry 0 -> 0 (heroes.WeaponDict)
			t.Fatalf("%s: %d instances, %d weapons", h.Name, len(insts), len(dict))
		}
		for _, v := range heroes.Final(st.Owner) {
			if got, ok := owner[v.ID]; !ok || !sameBits(v.Value, got) {
				t.Errorf("%s: owner $%x = %#x, want %+v", h.Name, v.ID, got, v.Value)
			}
		}
		for i, in := range st.Instances {
			d := insts[i]
			if d.id != uint32(in.ID) || d.graph != in.Graph.Index || d.parent != uint32(in.Parent) || (in.Parent != 0 && d.slot != uint32(in.Slot)) {
				t.Fatalf("%s: instance %+v, want %d %#x %d/%d", h.Name, d, in.ID, in.Graph.Index, in.Parent, in.Slot)
			}
			for _, v := range heroes.Final(in.Vars) {
				got, ok := d.vars[v.ID]
				if !scriptValueOK(v.Value) {
					if ok {
						t.Errorf("%s: unsendable $%x sent", h.Name, v.ID)
					}
					continue
				}
				if !ok || !sameBits(v.Value, got) {
					t.Errorf("%s: instance %d $%x = %#x, want %+v", h.Name, in.ID, v.ID, got, v.Value)
				}
			}
			if len(d.active) != len(in.Active) {
				t.Errorf("%s: instance %d: %d active, want %d", h.Name, in.ID, len(d.active), len(in.Active))
			}
			for _, a := range in.Active {
				data, ok := d.active[a.State]
				if !ok {
					t.Errorf("%s: instance %d state %d not active", h.Name, in.ID, a.State)
					continue
				}
				if a.Data == heroes.DataSubScript && (data[0] != uint32(a.Child) || (data[1] == 1) != a.Flag) {
					t.Errorf("%s: SubScript data %v", h.Name, data)
				}
				if typ := in.Graph.StateTypes[a.State]; typ == 0xf7d6498c && !slices.Equal(data, []uint32{0, 1, 0}) ||
					typ == 0xf53c5da8 && !slices.Equal(data, []uint32{0, 0, 0}) {
					t.Errorf("%s: instance %d state %d (%#x) data %v", h.Name, in.ID, a.State, typ, data)
				}
			}
		}
	}
}

// sameBits compares a value with what the client stores: INT (BYTE too) sign-extended, the rest as is.
func sameBits(v heroes.Value, got uint64) bool {
	switch v.Type {
	case heroes.TypeInt, heroes.TypeByte:
		return int64(int32(v.Bits)) == int64(got)
	case heroes.TypeFloat:
		return math.Float32frombits(uint32(got)) == v.Float()
	}
	return v.Bits == got
}

// The HUD values (T016): $769 = UX screen 0x09a00000000000fe on the hub: tag 9, u16 0x09a0, 7e0 0xfe.
func TestScriptValueGUID(t *testing.T) {
	var w bitWriter
	w.scriptValue(heroes.Int64(0x09a00000000000fe))
	r := &bitReader{b: w.buf}
	if r.bits(1) != 0 || r.bits(4) != 9 || r.bits(16) != 0x09a0 || r.bits(1) != 0 || r.bits(8) != 0xfe || r.pos != w.nbit {
		t.Fatal("GUID encoding")
	}
	if scriptValueOK(heroes.Int64(0x0000123400000000)) || scriptValueOK(heroes.String("x")) {
		t.Fatal("unsendable values accepted")
	}
}
