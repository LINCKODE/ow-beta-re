package instance

import (
	"fmt"
	"slices"

	"owserver/internal/heroes"
)

// Hero start-up (T040, docs/MatchLogic.md "Hero start-up"): the lock-in full update of a hero body
// carries what the client's StartDefaultScripts does for entities it creates itself: the definition's
// graphs with schema defaults and overrides, the weapon scripts, the entries' start (heroes.Start,
// generated from content).
//
// Variable values (FUN_7ff650cef290): a state reference (FUN_7ff650cf1520, 0 = none), a u4 tag, for
// tag 15 a u3 subtag, then the body (FUN_7ff650cef780; tag table in docs/MatchLogic.md "Hero
// start-up"). Tags written here: 1-4 INT (u4, s8, s16, u32), 8 FLT (f32), 9 INT64 (u16 bits 48-63,
// then if non-zero a 7e0 of bits 0-31: bits 32-47 cannot be sent), 15/7 a full dictionary. BYTE has no
// tag (tag 0 stores an INT), so bools arrive as INT 0/1; strings cannot be synced at all.

// scriptValueOK reports whether scriptValue can carry v.
func scriptValueOK(v heroes.Value) bool {
	switch v.Type {
	case heroes.TypeByte, heroes.TypeInt, heroes.TypeFloat:
		return true
	case heroes.TypeInt64:
		return v.Bits>>32&0xffff == 0
	}
	return false
}

// tagged writes a value's u4 tag and body, without the state reference (dictionary elements).
func (w *bitWriter) tagged(v heroes.Value) {
	switch v.Type {
	case heroes.TypeFloat:
		w.bits(8, 4)
		w.bits(uint32(v.Bits), 32)
	case heroes.TypeInt64:
		w.bits(9, 4)
		hi := uint32(v.Bits >> 48)
		w.bits(hi, 16)
		if hi != 0 {
			w.count(uint32(v.Bits))
		}
	default: // INT, BYTE
		i := int32(v.Bits)
		switch {
		case i >= 0 && i < 16:
			w.bits(1, 4)
			w.bits(uint32(i), 4)
		case i >= -128 && i < 128:
			w.bits(2, 4)
			w.bits(uint32(i)&0xff, 8)
		case i >= -32768 && i < 32768:
			w.bits(3, 4)
			w.bits(uint32(i)&0xffff, 16)
		default:
			w.bits(4, 4)
			w.bits(uint32(i), 32)
		}
	}
}

// scriptValue writes a variable value: no state reference, then the tagged value.
func (w *bitWriter) scriptValue(v heroes.Value) {
	w.bits(0, 1)
	w.tagged(v)
}

// scriptDict writes a dictionary variable (tag 15, subtag 7: a full key/value list, FUN_7ff650cf1070):
// small(N), a "homogeneous" bit (0: every element tagged), then key and value per entry.
func (w *bitWriter) scriptDict(keys, values []heroes.Value) {
	w.bits(0, 1) // no state reference
	w.bits(15, 4)
	w.bits(7, 3)
	w.small(uint32(len(keys)))
	w.bits(0, 1)
	for i := range keys {
		w.tagged(keys[i])
		w.tagged(values[i])
	}
}

// HeroStartupFullUpdate is a local-format Statescript full update (see ScriptLocalFullUpdate) that
// creates a hero's start-up instances (with SubScript children: parent bit, 7e0 parent instance, 7e0
// the parent's m_states index), sets the owner vars (the entity bag, cleared by a full update) and
// each instance's vars (eligible sync vars as changed bits in slot order, the rest in the extra list)
// and activates the start states with their state data.
func HeroStartupFullUpdate(endCF uint32, st *heroes.Startup) (*bitWriter, error) {
	var p bitWriter
	p.bits(0, 1) // (ignored)
	p.bits(1, 1) // local
	p.bits(0, 1) // +0x51
	p.count(0)   // frame offset
	prev := uint16(0)
	for _, in := range st.Instances {
		if in.ID <= prev {
			return nil, fmt.Errorf("instance ids must increase (%d after %d)", in.ID, prev)
		}
		p.small(uint32(in.ID - prev))
		prev = in.ID
		p.bits(in.Graph.Index&0xffff, 16)
		p.bits(0, 1) // default owner value
		if in.Parent != 0 {
			p.bits(1, 1)
			p.count(uint32(in.Parent))
			p.count(uint32(in.Slot))
		} else {
			p.bits(0, 1)
		}
	}
	p.small(0)
	// Owner vars (FUN_7ff650cee050): 7e0 id + value, 7e0(0) ends.
	for _, v := range heroes.Final(st.Owner) {
		if scriptValueOK(v.Value) {
			p.count(v.ID)
			p.scriptValue(v.Value)
		}
	}
	if ks, vs := st.WeaponDict(); len(ks) > 0 {
		// The valid weapon indices, as Prometheus' player spawner fills them (a filler 0 -> 0, then index -> index).
		p.count(heroes.VarWeaponList)
		p.scriptDict(ks, vs)
	}
	p.count(0)
	for _, in := range st.Instances {
		vars := heroes.Final(in.Vars)
		// Sync vars (FUN_7ff650cedf30): a changed bit per eligible m_syncVars slot, then the extra list.
		for _, id := range in.Graph.SyncVars {
			v, ok := heroes.Lookup(vars, id)
			if !ok || !scriptValueOK(v) {
				p.bits(0, 1)
				continue
			}
			p.bits(1, 1)
			p.scriptValue(v)
		}
		for _, v := range vars {
			if slices.Contains(in.Graph.SyncVars, v.ID) || !scriptValueOK(v.Value) {
				continue
			}
			p.count(v.ID)
			p.scriptValue(v.Value)
		}
		p.count(0)
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
				p.bits(0, 1) // no update
				continue
			}
			a, ok := on[si]
			if !ok {
				p.bits(0, 1)
				continue
			}
			p.bits(1, 1)
			switch a.Data {
			case heroes.DataSubScript:
				p.small(uint32(a.Child))
				p.bools([]bool{a.Flag})
			case heroes.DataSwitch:
				p.bools([]bool{a.Flag})
			case heroes.DataBits: // Ability, Stack, TrackMovement, CameraTransform (T044)
				if a.NBits > 0 {
					p.bits(a.Bits, uint(a.NBits))
				}
			}
		}
		p.bits(3, 2) // events: none (kind 3 ends)
	}
	p.bits(0, 1) // FUN_7ff650cee920: nothing
	return wrapScriptPacket(0, endCF, &p), nil
}
