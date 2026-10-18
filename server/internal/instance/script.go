package instance

import "slices"

// Statescript sync (stream 1; docs/Statescript.md §7-8). The client reads an
// entity's stream 1 data with FUN_7ff650c10140: bit present, one more bit
// (ignored), then a StatescriptPacket (FUN_7ff650cf6770):
//
//	start CF   (FUN_7ff651050590: 0+16 | 10+24 | 11+32 bits)
//	range      (FUN_7ff6510507e0: 0+8  | 10+16 | 11+32), end CF = start + range
//	bit: 1 -> u (FUN_7ff651050a30: 0+4 | 10+8 | 11+16) + bool
//	payload bit length (7e0) + payload
//
// Note the polarity: a 0 prefix selects the short form here. The payload starts
// with a bit (ignored) and the local flag (FUN_7ff650ce89f0). The client queues
// packets (FUN_7ff650cec130; only if end CF > the last applied) and replicates
// them on its tick (FUN_7ff650cecd90). Start CF 0 is a full update
// (FUN_7ff650ced430, "the current value of everything"):
//
//	bit (ignored), bit local (0 here; 1 adds a bit and a 7e0 frame offset)
//	instance list (FUN_7ff650cecab0): per instance a30(ID delta), then
//	  FUN_7ff650ced910: u16 graph index (graph 0x058...|index), bit (0: default
//	  owner u32; 1: u32), bit (0: no parent; 1: 7e0 parent + 7e0); a30(0) ends
//	owner vars (FUN_7ff650cee050): 7e0(var id) + value ..., 7e0(0) ends
//	per instance: instance vars (FUN_7ff650cedf30): one changed bit per eligible
//	  sync var (m_syncVars), each set bit followed by the value; states
//	  (FUN_7ff650cedbe0, remote entity): one included bit per eligible remote sync
//	  node (m_remoteSyncNodes)
//	events (FUN_7ff650cee920): bit (0 = none)
//
// Instances missing from a full update are destroyed. The eligible counts come
// from the graph asset (Prometheus MCP tool graph_sync_info).
//
// A variable value (FUN_7ff650cef290): a state reference (FUN_7ff650cf1520; bit 0
// = none, for plain variables), a u4 tag and the value (FUN_7ff650cef780). Integer
// tags: 1 u4, 2 s8, 3 s16, 4 s32; floats: 5 from u4, 6 8 bits, 7 half, 8 f32;
// 10 two quantized u16, 11/12 a 3-bit mask of up to three components, 15 a u3
// subtag (4-7 arrays / state-defined values).

// ScriptGraph is a Statescript graph and its sync layout.
type ScriptGraph struct {
	Index       uint16 // graph 0x058...|Index
	SyncVars    int    // eligible sync vars
	RemoteNodes int    // eligible remote sync nodes
	// States is the graph's m_states list (graph +0x60) as the local state format
	// sees it, one character per state: '.' synced, 'C' client-only (+0x75) and 'V'
	// server-only (+0x76), both skipped, 'S' a type FUN_7ff650ecfee0 accepts (an
	// extra "has update" bit first). Read from the client by calling ecfee0 on each
	// state node.
	States string
	// SyncVarIDs are the eligible m_syncVars (graph +0x130, a slot table: GUID
	// 0x0D8...|id at +8, eligible when +0x10 is 0) in slot order, the order of the
	// changed bits. Without them every var is sent unchanged.
	SyncVarIDs []uint16
	// Vars are the instance variables a full update sets: the hero's per-graph
	// overrides (entity definition, Statescript component 0x9b38211b field
	// 0x0175ad85) and what the server's own start-up would have set. Local updates
	// send ones missing from SyncVarIDs in the extra variable list; remote ones
	// drop them.
	Vars        map[uint16]int32
	Description string
}

// scriptInt writes an integer variable value: no state reference, then the
// smallest integer tag that holds v.
func (w *bitWriter) scriptInt(v int32) {
	w.bits(0, 1)
	switch {
	case v >= 0 && v < 16:
		w.bits(1, 4)
		w.bits(uint32(v), 4)
	case v >= -128 && v < 128:
		w.bits(2, 4)
		w.bits(uint32(v)&0xff, 8)
	case v >= -32768 && v < 32768:
		w.bits(3, 4)
		w.bits(uint32(v)&0xffff, 16)
	default:
		w.bits(4, 4)
		w.bits(uint32(v), 32)
	}
}

// syncVars writes an instance's sync variables (FUN_7ff650cedf30): a changed bit
// per eligible var with the value of the ones in g.Vars, then for a local update
// the extra variables (7e0 id + value, 7e0(0) ends).
func (w *bitWriter) syncVars(g ScriptGraph, local bool) {
	synced := map[uint16]bool{}
	for i := range g.SyncVars {
		if i >= len(g.SyncVarIDs) {
			w.bits(0, 1)
			continue
		}
		id := g.SyncVarIDs[i]
		synced[id] = true
		v, ok := g.Vars[id]
		if !ok {
			w.bits(0, 1)
			continue
		}
		w.bits(1, 1)
		w.scriptInt(v)
	}
	if !local {
		return
	}
	extra := make([]uint16, 0, len(g.Vars))
	for id := range g.Vars {
		if !synced[id] {
			extra = append(extra, id)
		}
	}
	slices.Sort(extra)
	for _, id := range extra {
		w.count(uint32(id))
		w.scriptInt(g.Vars[id])
	}
	w.count(0)
}

// prefix3 writes the Statescript sync numbers: a 0 bit and w0 bits, 1 0 and w1
// bits, or 1 1 and w2 bits.
func (w *bitWriter) prefix3(v uint32, w0, w1, w2 uint) {
	switch {
	case v < 1<<w0:
		w.bits(0, 1)
		w.bits(v, w0)
	case v < 1<<w1:
		w.bits(0b01, 2) // 1 then 0
		w.bits(v, w1)
	default:
		w.bits(0b11, 2)
		w.bits(v, w2)
	}
}

func (w *bitWriter) cf(v uint32)    { w.prefix3(v, 16, 24, 32) } // FUN_7ff651050590
func (w *bitWriter) count(v uint32) { w.prefix3(v, 8, 16, 32) }  // FUN_7ff6510507e0
func (w *bitWriter) small(v uint32) { w.prefix3(v, 4, 8, 16) }   // FUN_7ff651050a30

// ScriptFullUpdate is a stream 1 entity entry that creates instances 1..n of the
// given graphs as of frame endCF, in the remote format. active maps an instance
// (1-based) to the states to activate; there a ScriptState's Index is the state's
// position among the graph's eligible remote sync nodes (m_remoteSyncNodes, graph
// +0x120, skipping non-states and client-/server-only ones) and Data its state
// data. FUN_7ff650cedbe0 reads per eligible node an "included" bit, then (for the
// plain layout, FUN_7ff650ecfee0 = 0) an active bit: 0 deactivates, 1 activates
// (FUN_7ff650f295c0) and reads the state data (FUN_7ff650f289e0: reflected
// bsDataStore fields, then vfunc +0x1F8). The three types with FUN_7ff650ecfee0 = 1
// (0x7bf31629, 0x860ee5fc, 0xffbfa88d) read "has update", active, data instead;
// we only send those as not included.
func ScriptFullUpdate(endCF uint32, graphs []ScriptGraph, active map[int][]ScriptState) *bitWriter {
	var p bitWriter
	// Payload header (FUN_7ff650ce89f0): a bit (ignored) and the local flag. A local
	// packet (1) adds a bit (+0x51) and a 7e0 frame offset and changes the instance
	// var and state formats; we send remote packets.
	p.bits(0, 1)
	p.bits(0, 1)
	for _, g := range graphs {
		p.small(1)
		p.bits(uint32(g.Index), 16)
		p.bits(0, 1) // default owner value
		p.bits(0, 1) // no parent
	}
	p.small(0)
	p.count(0) // no owner vars
	for i, g := range graphs {
		p.syncVars(g, false)
		on := map[int]ScriptState{}
		for _, st := range active[i+1] {
			on[st.Index] = st
		}
		for n := range g.RemoteNodes {
			st, ok := on[n]
			if !ok {
				p.bits(0, 1) // not included: left as it is
				continue
			}
			p.bits(1, 1) // included
			if st.Off {
				p.bits(0, 1) // deactivate
				continue
			}
			p.bits(1, 1) // active
			p.bools(st.Data)
		}
	}
	p.bits(0, 1) // no events
	return wrapScriptPacket(0, endCF, &p)
}

// ScriptState is a state to report active in a local full update: its index in
// m_states and its state data (the type's bsDataStore reader, vfunc +0x1F8;
// BooleanSwitch: 1 bit, the switch value; Wait: nothing).
type ScriptState struct {
	Index int
	Data  []bool
	Off   bool // remote updates: deactivate the state instead (included, active bit 0)
}

// ScriptLocalFullUpdate is ScriptFullUpdate in the local format, for the entity
// the client controls: the payload header's local flag is set, followed by a bit
// (+0x51, 0 here) and a 7e0 frame offset (0). Per instance, the sync var bits are
// followed by a 7e0 list of extra variable IDs (0 ends), then every synced
// m_states entry gets an active bit (0 = deactivate, 1 = activate (f295c0) +
// state data); 'S' states get a "has update" bit before it (0 here: leave the
// state alone). Then the events (FUN_7ff650cf1620): u2 kinds until 3. active
// maps an instance (1-based, the graphs' order) to the states to activate.
func ScriptLocalFullUpdate(endCF uint32, graphs []ScriptGraph, active map[int][]ScriptState) *bitWriter {
	var p bitWriter
	p.bits(0, 1) // (ignored)
	p.bits(1, 1) // local
	p.bits(0, 1) // +0x51
	p.count(0)   // frame offset
	for _, g := range graphs {
		p.small(1)
		p.bits(uint32(g.Index), 16)
		p.bits(0, 1) // default owner value
		p.bits(0, 1) // no parent
	}
	p.small(0)
	p.count(0) // no owner vars
	for i, g := range graphs {
		p.syncVars(g, true)
		on := map[int][]bool{}
		for _, st := range active[i+1] {
			on[st.Index] = st.Data
		}
		for si, kind := range g.States {
			switch kind {
			case 'C', 'V':
				continue
			case 'S':
				p.bits(0, 1) // no update
				continue
			}
			data, ok := on[si]
			if !ok {
				p.bits(0, 1)
				continue
			}
			p.bits(1, 1)
			p.bools(data)
		}
		p.bits(3, 2) // events: none (kind 3 ends)
	}
	p.bits(0, 1) // FUN_7ff650cee920: nothing
	return wrapScriptPacket(0, endCF, &p)
}

// wrapScriptPacket puts a payload into a StatescriptPacket covering CFs startCF
// to endCF. Start CF 0 is a full update; anything else a delta (FUN_7ff650ced600),
// which the client applies on top of what it has.
func wrapScriptPacket(startCF, endCF uint32, p *bitWriter) *bitWriter {
	var w bitWriter
	w.bits(1, 1) // present
	w.bits(0, 1) // (ignored)
	w.cf(startCF)
	w.count(endCF - startCF)
	w.bits(0, 1) // no optional value
	w.count(uint32(p.nbit))
	for i := uint(0); i < p.nbit; i++ {
		w.bits(uint32(p.buf[i/8]>>(i%8)&1), 1)
	}
	return &w
}
