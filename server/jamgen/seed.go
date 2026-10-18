package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// The Prometheus export of the client's registered protocols
// (game/owbetaclient/Prometheus/jam_protocols.json, Prometheus jam_protocol_dump.h).
type dumpProtocol struct {
	CRC      int64         `json:"crc"`
	Min      int           `json:"min_id"`
	Max      int           `json:"max_id"`
	Name     string        `json:"name"` // pool class, e.g. JAM469a6c4dProtocolPool
	Pool     string        `json:"pool"` // protocol name
	Messages []dumpMessage `json:"messages"`
}

type dumpMessage struct {
	CRC    int64       `json:"crc"`
	ID     int         `json:"id"`
	Index  int         `json:"index"`
	Name   string      `json:"name"`
	Fields []dumpField `json:"fields"`
}

type dumpField struct {
	Name   string      `json:"name"`
	Type   int         `json:"type"`
	Offset int         `json:"offset"`
	Size   int         `json:"size"`
	Array  any         `json:"array"` // absent, "dynamic" or a count
	Fields []dumpField `json:"fields"`
}

// The instance stream's protocol list (client stream +0x38, R&D server stream.go).
var instanceChannels = []string{
	"ClientInInstance", "ClientOutInstance", "ClientInCombat", "ClientInVoice",
	"ClientInStats", "ClientOutCheats", "ClientOutPvP", "ClientOutStats",
}

// Named structs, recognised by their field names and types.
var sharedTypes = []*Type{
	{Name: "JamID", Size: 16, Notes: "JAM's 128-bit address {seq, src}; used for players, parties, games, channels (every 16-byte ID in the lobby).",
		Fields: []*Field{{Name: "seq", Type: "u64"}, {Name: "src", Type: "u64", Offset: 8}}},
	{Name: "EntityID", Size: 4, Notes: "A client entity ID (0x40000000 | n for server entities).",
		Fields: []*Field{{Name: "id", Type: "u32"}}},
	{Name: "Vec3", Size: 12, Fields: []*Field{{Name: "x", Type: "f32"}, {Name: "y", Type: "f32", Offset: 4}, {Name: "z", Type: "f32", Offset: 8}}},
	{Name: "Vec4", Size: 16, Fields: []*Field{{Name: "x", Type: "f32"}, {Name: "y", Type: "f32", Offset: 4}, {Name: "z", Type: "f32", Offset: 8}, {Name: "w", Type: "f32", Offset: 12}}},
}

// negotiation is JAM's link negotiation protocol. It is registered before the
// Prometheus hook runs, so it isn't in the dump; the layouts are from the static
// message tables at 0x7ff65170cac8..0x7ff65170cc40 (docs/JAM.md §12.5).
func negotiation() *Protocol {
	return &Protocol{
		Name: "Negotiation", CRC: 0x11d82194, Base: 1, Max: 5, Direction: "both", Transport: "lobby",
		Notes: "Channel 0 on every lobby link. Registered by FUN_7ff650f0b3e0 (not in the dump: layouts from the .data tables). The client (link flag +0x82 = 0) sends 1, waits for 3, then sends 4 every 10 s.",
		Messages: []*Message{
			{ID: 1, Name: "NegotiateProtocols", Tags: "G L", Use: "recv",
				Notes:  "The CRCs of the protocols registered for the peer's server type, sorted; channels 1..n are created in this order. Don't echo it (the client's handler requires state 2).",
				Fields: []*Field{{Name: "crcs", Type: "u32", Array: "[]"}}},
			{ID: 2, Name: "NegotiateDescriptors", Tags: "G",
				Notes: "The other link mode's descriptor list (flag +0x82 != 0; JSON fallback on CRC mismatch). Not used by the game client.",
				Fields: []*Field{
					{Name: "peer", Type: "JamID"},
					{Name: "protocols", Type: "struct", Array: "[]", Fields: []*Field{
						{Name: "m_protocolCRC", Type: "u32"}, {Name: "m_messageBase", Type: "u32"}, {Name: "m_messageMax", Type: "u32"},
						{Name: "m_protocolFlags", Type: "u32"}, {Name: "m_untrustedServer", Type: "u32"}, {Name: "m_protocolName", Type: "str"}}},
				}},
			{ID: 3, Name: "NegotiateAccept", Tags: "G L", Use: "send", Notes: "Accepts the client's list: link state 3 -> 5 (up).", Fields: []*Field{}},
			{ID: 4, Name: "NegotiateKeepalive", Tags: "L", Use: "recv", Notes: "Sent by the client every 10 s once the link is up; no reply.", Fields: []*Field{}},
		},
	}
}

func convertFields(fs []dumpField) ([]*Field, error) {
	out := make([]*Field, 0, len(fs))
	for _, f := range fs {
		if f.Type < 0 || f.Type >= len(jamTypes) {
			return nil, fmt.Errorf("unknown JAM type %d", f.Type)
		}
		o := &Field{Name: f.Name, Type: jamTypes[f.Type], Offset: f.Offset}
		switch a := f.Array.(type) {
		case nil:
		case string:
			if a != "dynamic" {
				return nil, fmt.Errorf("array %q", a)
			}
			o.Array = "[]"
		case float64:
			o.Array = strconv.Itoa(int(a))
		}
		switch o.Type {
		case "blob":
			o.Size = f.Size
		case "struct":
			sub, err := convertFields(f.Fields)
			if err != nil {
				return nil, err
			}
			o.Fields = sub
			if t := matchShared(sub); t != nil {
				o.Type, o.Fields = t.Name, nil
			}
		}
		out = append(out, o)
	}
	return out, nil
}

func matchShared(fs []*Field) *Type {
	for _, t := range sharedTypes {
		if len(t.Fields) != len(fs) {
			continue
		}
		ok := true
		for i := range fs {
			ok = ok && fs[i].Name == t.Fields[i].Name && fs[i].Type == t.Fields[i].Type && fs[i].Array == ""
		}
		if ok {
			return t
		}
	}
	return nil
}

// seed builds the catalogue from the dump, keeping everything curated in prev
// (names, notes, handlers, tags) where the layout still matches.
func seed(dumpPath string, prev *Catalogue) (*Catalogue, error) {
	b, err := os.ReadFile(dumpPath)
	if err != nil {
		return nil, err
	}
	var dump []dumpProtocol
	if err := json.Unmarshal(b, &dump); err != nil {
		return nil, err
	}
	c := &Catalogue{
		Comment: []string{
			"JAM message catalogue of the Overwatch 0.8 beta client (build 24919). Source of server/inst/jam (go generate).",
			"Layouts: the client's own field tables (Prometheus jam_protocol_dump -> game/owbetaclient/Prometheus/jam_protocols.json), reseeded with `go run ./jamgen seed`; curated fields (names, notes, handlers, tags) are kept.",
			"Types: bool (bit-packed), char, u8, i8, u16, i16, u32, i32, u64, i64, f32, f64, str (NUL-terminated), blob (fixed size), bytes (u32 length + bytes), tid (tagged ID), struct, or a named type below. array: [] = u32 count + elements, N = fixed.",
			"RVAs are relative to the image base (Ghidra 0x7ff64ffe0000). Tags: G Ghidra, L live capture, K early notes (docs/early-notes, docs/JAM.md §12.5), H confirmed in game.",
		},
		Types: sharedTypes,
	}
	c.Protocols = append(c.Protocols, negotiation())
	sort.Slice(dump, func(i, j int) bool { return dump[i].Pool < dump[j].Pool })
	for _, d := range dump {
		p := &Protocol{Name: d.Pool, CRC: Hex32(uint32(d.CRC)), Base: d.Min, Max: d.Max, Pool: d.Name}
		switch {
		case strings.HasPrefix(p.Name, "ClientIn"):
			p.Direction = "in"
		case strings.HasPrefix(p.Name, "ClientOut"):
			p.Direction = "out"
		}
		if strings.HasPrefix(p.Name, "ClientInLobby") || strings.HasPrefix(p.Name, "ClientOutLobby") {
			p.ServerType, p.Transport = 13, "lobby"
		}
		for i, n := range instanceChannels {
			if n == p.Name {
				ch := i
				p.InstanceChannel, p.Transport = &ch, "instance"
			}
		}
		sort.Slice(d.Messages, func(i, j int) bool { return d.Messages[i].ID < d.Messages[j].ID })
		for _, dm := range d.Messages {
			fs, err := convertFields(dm.Fields)
			if err != nil {
				return nil, fmt.Errorf("%s %d: %w", p.Name, dm.ID, err)
			}
			p.Messages = append(p.Messages, &Message{ID: dm.ID, Name: dm.Name, Fields: fs})
		}
		c.Protocols = append(c.Protocols, p)
	}
	if prev != nil {
		merge(c, prev)
	}
	return c, c.validate()
}

// merge copies curated attributes from prev into c.
func merge(c, prev *Catalogue) {
	for _, t := range c.Types {
		if pt := prev.typeByName(t.Name); pt != nil {
			t.Go, t.Notes = pt.Go, pt.Notes
		}
	}
	// Curated named types (the dump has only anonymous structs for them).
	for _, pt := range prev.Types {
		if c.typeByName(pt.Name) == nil {
			c.Types = append(c.Types, pt)
		}
	}
	for _, p := range c.Protocols {
		var pp *Protocol
		for _, x := range prev.Protocols {
			if x.Name == p.Name {
				pp = x
			}
		}
		if pp == nil {
			continue
		}
		p.Receiver, p.Dispatcher, p.Notes = pp.Receiver, pp.Dispatcher, pp.Notes
		if pp.Direction != "" {
			p.Direction = pp.Direction
		}
		if pp.Transport != "" && p.Transport == "" {
			p.Transport = pp.Transport
		}
		for _, m := range p.Messages {
			for _, pm := range pp.Messages {
				if pm.ID != m.ID {
					continue
				}
				if pm.Name != "" {
					m.Name = pm.Name
				}
				m.Label, m.Handler, m.Use, m.Tags, m.Notes = pm.Label, pm.Handler, pm.Use, pm.Tags, pm.Notes
				if !mergeFields(c, m.Fields, pm.Fields) {
					fmt.Fprintf(os.Stderr, "warning: %s %d: layout changed, field names dropped\n", p.Name, m.ID)
				}
			}
		}
	}
}

func mergeFields(c *Catalogue, fs, prev []*Field) bool {
	if len(fs) != len(prev) {
		return false
	}
	for i, f := range fs {
		pf := prev[i]
		// A curated named type replaces an anonymous struct of the same shape.
		if t := c.typeByName(pf.Type); t != nil && f.Type == "struct" && pf.Array == f.Array && sameShape(c, t.Fields, f.Fields) {
			f.Type, f.Fields = pf.Type, nil
		}
		if pf.Type != f.Type || pf.Array != f.Array || pf.Size != f.Size {
			return false
		}
		if f.Name == "" {
			f.Name = pf.Name
		}
		f.Notes = pf.Notes
		if f.Type == "struct" && !mergeFields(c, f.Fields, pf.Fields) {
			return false
		}
	}
	return true
}

// sameShape reports whether two field lists have the same wire layout (names
// aside), resolving named types on either side.
func sameShape(c *Catalogue, a, b []*Field) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Array != y.Array || x.Size != y.Size {
			return false
		}
		xf, yf := x.Fields, y.Fields
		xt, yt := x.Type, y.Type
		if t := c.typeByName(xt); t != nil {
			xt, xf = "struct", t.Fields
		}
		if t := c.typeByName(yt); t != nil {
			yt, yf = "struct", t.Fields
		}
		if xt != yt || (xt == "struct" && !sameShape(c, xf, yf)) {
			return false
		}
	}
	return true
}
