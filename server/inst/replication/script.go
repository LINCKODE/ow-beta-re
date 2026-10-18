package replication

import (
	"fmt"
	"slices"

	"owserver/inst/bits"
	"owserver/internal/heroes"
)

// Statescript sync (stream 1; docs/Statescript.md §7-8, docs/MatchLogic.md §14).
// The client reads an entity's stream 1 data with FUN_7ff650c10140: bit present,
// one more bit (ignored), then a StatescriptPacket (FUN_7ff650cf6770):
//
//	start CF   (cf: 0+16 | 10+24 | 11+32 bits)
//	range      (count: 0+8 | 10+16 | 11+32), end CF = start + range
//	bit: 1 -> small + bool (0 here)
//	payload bit length (count) + payload
//
// Start CF 0 is a full update ("the current value of everything",
// FUN_7ff650ced430); anything else a delta (FUN_7ff650ced600). The payload starts
// with a bit (ignored) and the local flag (FUN_7ff650ce89f0); the local format
// (the entity the client controls) adds a bit (+0x51) and a count frame offset.
//
// Stopgap (T049 boundary rule): the Statescript runtime (systems/statescript, T041)
// will produce these payloads behind its C API; these are the R&D server's
// encoders, moved.

// prefix3 writes the Statescript sync numbers: 0 + w0 bits, 1 0 + w1, 1 1 + w2.
func prefix3(w *bits.Writer, v uint32, w0, w1, w2 uint) {
	switch {
	case v < 1<<w0:
		w.Bits(0, 1)
		w.Bits(v, w0)
	case v < 1<<w1:
		w.Bits(0b01, 2) // 1 then 0
		w.Bits(v, w1)
	default:
		w.Bits(0b11, 2)
		w.Bits(v, w2)
	}
}

func cf(w *bits.Writer, v uint32)    { prefix3(w, v, 16, 24, 32) } // FUN_7ff651050590
func count(w *bits.Writer, v uint32) { prefix3(w, v, 8, 16, 32) }  // FUN_7ff6510507e0
func small(w *bits.Writer, v uint32) { prefix3(w, v, 4, 8, 16) }   // FUN_7ff651050a30

// WrapScriptPacket puts a payload into a StatescriptPacket covering CFs startCF to
// endCF (start 0 = a full update).
func WrapScriptPacket(startCF, endCF uint32, p *bits.Writer) *bits.Writer {
	var w bits.Writer
	w.Bits(1, 1) // present
	w.Bits(0, 1) // (ignored)
	cf(&w, startCF)
	count(&w, endCF-startCF)
	w.Bits(0, 1) // no optional value
	count(&w, uint32(p.Len()))
	w.Append(p)
	return &w
}

// Variable values (FUN_7ff650cef290): a state reference (FUN_7ff650cf1520, 0 =
// none), a u4 tag, for tag 15 a u3 subtag, then the body (FUN_7ff650cef780). Tags
// written here: 1-4 INT (u4, s8, s16, u32), 8 FLT (f32), 9 INT64 (u16 bits 48-63,
// then if non-zero a count of bits 0-31: bits 32-47 cannot be sent), 15/7 a full
// dictionary. BYTE has no tag (tag 0 stores an INT), so bools go as INT 0/1;
// strings cannot be synced.

// ValueOK reports whether a variable value can be synced.
func ValueOK(v heroes.Value) bool {
	switch v.Type {
	case heroes.TypeByte, heroes.TypeInt, heroes.TypeFloat:
		return true
	case heroes.TypeInt64:
		return v.Bits>>32&0xffff == 0
	}
	return false
}

func tagged(w *bits.Writer, v heroes.Value) {
	switch v.Type {
	case heroes.TypeFloat:
		w.Bits(8, 4)
		w.Bits(uint32(v.Bits), 32)
	case heroes.TypeInt64:
		w.Bits(9, 4)
		hi := uint32(v.Bits >> 48)
		w.Bits(hi, 16)
		if hi != 0 {
			count(w, uint32(v.Bits))
		}
	default: // INT, BYTE
		i := int32(v.Bits)
		switch {
		case i >= 0 && i < 16:
			w.Bits(1, 4)
			w.Bits(uint32(i), 4)
		case i >= -128 && i < 128:
			w.Bits(2, 4)
			w.Bits(uint32(i)&0xff, 8)
		case i >= -32768 && i < 32768:
			w.Bits(3, 4)
			w.Bits(uint32(i)&0xffff, 16)
		default:
			w.Bits(4, 4)
			w.Bits(uint32(i), 32)
		}
	}
}

func scriptValue(w *bits.Writer, v heroes.Value) {
	w.Bits(0, 1) // no state reference
	tagged(w, v)
}

// scriptDict writes a dictionary (tag 15, subtag 7: a full key/value list,
// FUN_7ff650cf1070): small(N), a "homogeneous" bit (0: every element tagged), then
// key and value per entry.
func scriptDict(w *bits.Writer, keys, values []heroes.Value) {
	w.Bits(0, 1)
	w.Bits(15, 4)
	w.Bits(7, 3)
	small(w, uint32(len(keys)))
	w.Bits(0, 1)
	for i := range keys {
		tagged(w, keys[i])
		tagged(w, values[i])
	}
}

// HeroStartup is a local-format full update that creates a hero's start-up
// instances (heroes.Start: definition graphs with schema defaults and overrides,
// weapon scripts, SubScript children), sets the owner vars and each instance's
// vars, and activates the start states with their state data (docs/MatchLogic.md
// §14.4).
func HeroStartup(endCF uint32, st *heroes.Startup) (*bits.Writer, error) {
	var p bits.Writer
	p.Bits(0, 1) // (ignored)
	p.Bits(1, 1) // local
	p.Bits(0, 1) // +0x51
	count(&p, 0) // frame offset
	prev := uint16(0)
	for _, in := range st.Instances {
		if in.ID <= prev {
			return nil, fmt.Errorf("replication: instance ids must increase (%d after %d)", in.ID, prev)
		}
		small(&p, uint32(in.ID-prev))
		prev = in.ID
		p.Bits(in.Graph.Index&0xffff, 16)
		p.Bits(0, 1) // default owner value
		if in.Parent != 0 {
			p.Bits(1, 1)
			count(&p, uint32(in.Parent))
			count(&p, uint32(in.Slot))
		} else {
			p.Bits(0, 1)
		}
	}
	small(&p, 0)
	// Owner vars (FUN_7ff650cee050): count id + value, count(0) ends.
	for _, v := range heroes.Final(st.Owner) {
		if ValueOK(v.Value) {
			count(&p, v.ID)
			scriptValue(&p, v.Value)
		}
	}
	if ks, vs := st.WeaponDict(); len(ks) > 0 {
		// The valid weapon indices, as Prometheus' player spawner fills them (a filler 0 -> 0, then index -> index).
		count(&p, heroes.VarWeaponList)
		scriptDict(&p, ks, vs)
	}
	count(&p, 0)
	for _, in := range st.Instances {
		vars := heroes.Final(in.Vars)
		// Sync vars (FUN_7ff650cedf30): a changed bit per eligible m_syncVars slot, then the extra list.
		for _, id := range in.Graph.SyncVars {
			v, ok := heroes.Lookup(vars, id)
			if !ok || !ValueOK(v) {
				p.Bits(0, 1)
				continue
			}
			p.Bits(1, 1)
			scriptValue(&p, v)
		}
		for _, v := range vars {
			if slices.Contains(in.Graph.SyncVars, v.ID) || !ValueOK(v.Value) {
				continue
			}
			count(&p, v.ID)
			scriptValue(&p, v.Value)
		}
		count(&p, 0)
		// States (FUN_7ff650cedbe0, local): an active bit per synced m_states entry, then its data.
		on := map[int]heroes.Active{}
		for _, a := range in.Active {
			on[a.State] = a
		}
		for si := range len(in.Graph.States) {
			switch in.Graph.States[si] {
			case 'C', 'V':
				continue
			case 'S':
				p.Bits(0, 1) // no update
				continue
			}
			a, ok := on[si]
			if !ok {
				p.Bits(0, 1)
				continue
			}
			p.Bits(1, 1)
			switch a.Data {
			case heroes.DataSubScript:
				small(&p, uint32(a.Child))
				p.Bool(a.Flag)
			case heroes.DataSwitch:
				p.Bool(a.Flag)
			case heroes.DataBits: // Ability, Stack, TrackMovement, CameraTransform (T044)
				if a.NBits > 0 {
					p.Bits(a.Bits, uint(a.NBits))
				}
			}
		}
		p.Bits(3, 2) // events: none (kind 3 ends)
	}
	p.Bits(0, 1) // FUN_7ff650cee920: nothing
	return WrapScriptPacket(0, endCF, &p), nil
}

// Graph is a Statescript graph and its sync layout (control API experiments).
type Graph struct {
	Index       uint16 `json:"index"`        // graph 0x058...|Index
	SyncVars    int    `json:"sync_vars"`    // eligible sync vars
	RemoteNodes int    `json:"remote_nodes"` // eligible remote sync nodes (m_remoteSyncNodes)
}

// State is a state to activate in a remote full update: its position among the
// graph's eligible remote sync nodes and its state data bits.
type State struct {
	Index int    `json:"index"`
	Data  []bool `json:"data"`
	Off   bool   `json:"off"` // deactivate instead
}

// RemoteFullUpdate creates instances 1..n of graphs on a (non-local) entity as of
// frame endCF, sync vars unchanged, with the given states per instance (1-based)
// activated. For server-created entities, which don't start their default graphs
// themselves (docs/MatchLogic.md §9).
func RemoteFullUpdate(endCF uint32, graphs []Graph, active map[int][]State) *bits.Writer {
	var p bits.Writer
	p.Bits(0, 1)
	p.Bits(0, 1) // remote
	for _, g := range graphs {
		small(&p, 1)
		p.Bits(uint32(g.Index), 16)
		p.Bits(0, 1) // default owner value
		p.Bits(0, 1) // no parent
	}
	small(&p, 0)
	count(&p, 0) // no owner vars
	for i, g := range graphs {
		for range g.SyncVars {
			p.Bits(0, 1)
		}
		on := map[int]State{}
		for _, st := range active[i+1] {
			on[st.Index] = st
		}
		for n := range g.RemoteNodes {
			st, ok := on[n]
			if !ok {
				p.Bits(0, 1) // not included: left as it is
				continue
			}
			p.Bits(1, 1) // included
			if st.Off {
				p.Bits(0, 1)
				continue
			}
			p.Bits(1, 1)
			p.Bools(st.Data...)
		}
	}
	p.Bits(0, 1) // no events
	return WrapScriptPacket(0, endCF, &p)
}

// ActiveData is the local-format state data HeroStartup writes for a start state.
func ActiveData(a heroes.Active) StateData {
	var w bits.Writer
	switch a.Data {
	case heroes.DataSubScript:
		small(&w, uint32(a.Child))
		w.Bool(a.Flag)
	case heroes.DataSwitch:
		w.Bool(a.Flag)
	case heroes.DataBits:
		if a.NBits > 0 {
			w.Bits(a.Bits, uint(a.NBits))
		}
	}
	return StateData{Bits: w.Buf(), N: int(w.Len())}
}
