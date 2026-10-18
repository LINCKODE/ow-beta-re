package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Catalogue is systems/data/jam/catalogue.json: every JAM protocol the 0.8 client
// registers, with the wire layout of each message (from the client's own field
// tables) and what we know about it. It is the source for the generated codecs.
type Catalogue struct {
	Comment   []string    `json:"comment"`
	Types     []*Type     `json:"types"`
	Protocols []*Protocol `json:"protocols"`
}

// Type is a named struct that many messages share (JamID, EntityID, vectors).
type Type struct {
	Name   string   `json:"name"`
	Go     string   `json:"go,omitempty"` // Go spelling if not Name
	Size   int      `json:"size"`         // size of the client's in-memory struct
	Fields []*Field `json:"fields"`
	Notes  string   `json:"notes,omitempty"`
}

// Protocol is one JAM protocol (one .jam file on Blizzard's side).
type Protocol struct {
	Name string `json:"name"`
	CRC  Hex32  `json:"crc"`
	// Base..Max-1 are the message IDs; on the wire the message index is ID - Base.
	Base int `json:"base"`
	Max  int `json:"max"`
	// Direction from the client's point of view: "in" = server -> client
	// (ClientIn*), "out" = client -> server (ClientOut*), "both" / "" = tools.
	Direction string `json:"direction"`
	// Transport: "lobby" (TCP, negotiated channels), "instance" (UDP data payload
	// stream 0), "" = not used by our servers (dev tools).
	Transport string `json:"transport"`
	// ServerType is the peer server type the client registers the protocol for
	// (13 = lobby; lobby channels are the type-13 protocols sorted by CRC).
	ServerType int `json:"server_type,omitempty"`
	// InstanceChannel is the channel on the instance stream (the client's stream
	// protocol list, stream +0x38).
	InstanceChannel *int   `json:"instance_channel,omitempty"`
	Pool            string `json:"pool,omitempty"`       // the client's pool class name
	Receiver        string `json:"receiver,omitempty"`   // client RVA
	Dispatcher      string `json:"dispatcher,omitempty"` // client RVA
	Notes           string `json:"notes,omitempty"`
	Messages        []*Message `json:"messages"`
}

// Message is one message: its ID and field layout.
type Message struct {
	ID    int    `json:"id"`
	Name  string `json:"name,omitempty"` // Go type name; "" = <Protocol><ID>
	Label string `json:"label,omitempty"`
	// Handler is the client RVA that handles it (in messages) or sends it (out).
	Handler string `json:"handler,omitempty"`
	// Use says what our servers do with it: "send", "recv" or "".
	Use    string   `json:"use,omitempty"`
	Tags   string   `json:"tags,omitempty"` // provenance: G Ghidra, L live, K early notes (docs/early-notes), H user-confirmed
	Notes  string   `json:"notes,omitempty"`
	Fields []*Field `json:"fields"`
}

// Field is one field of a message or struct, in wire order.
//
// Type is a JAM scalar (bool char u8 i8 u16 i16 u32 i32 u64 i64 f32 f64 str blob
// bytes tid), a named type from Types, or "struct" with Fields.
type Field struct {
	Name   string   `json:"name,omitempty"`
	Type   string   `json:"type"`
	Array  string   `json:"array,omitempty"` // "" single, "[]" dynamic (u32 count), "N" fixed
	Size   int      `json:"size,omitempty"`  // blob: byte count
	Offset int      `json:"off"`             // offset in the client's message object
	Notes  string   `json:"notes,omitempty"`
	Fields []*Field `json:"fields,omitempty"`
}

// Hex32 marshals as "0x%08x".
type Hex32 uint32

func (h Hex32) MarshalJSON() ([]byte, error) { return []byte(fmt.Sprintf(`"0x%08x"`, uint32(h))), nil }
func (h *Hex32) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 32)
	*h = Hex32(v)
	return err
}

// JAM wire types by the client's type number (JamMessageItemInfo +0x8). Signedness
// is from the client's JSON encoder (FUN_7ff650f0ce90's jump table: %c, %u, %d, ...).
var jamTypes = []string{
	0: "bool", 1: "char", 2: "u8", 3: "i8", 4: "u16", 5: "i16", 6: "u32", 7: "i32",
	8: "u64", 9: "i64", 10: "f32", 11: "f64", 12: "str", 13: "blob", 14: "struct",
	15: "bytes", 16: "tid",
}

func scalarSize(t string) int {
	switch t {
	case "bool", "char", "u8", "i8":
		return 1
	case "u16", "i16":
		return 2
	case "u32", "i32", "f32":
		return 4
	case "u64", "i64", "f64":
		return 8
	}
	return 0
}

func loadCatalogue(path string) (*Catalogue, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Catalogue
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, c.validate()
}

func (c *Catalogue) save(path string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func (c *Catalogue) typeByName(n string) *Type {
	for _, t := range c.Types {
		if t.Name == n {
			return t
		}
	}
	return nil
}

// GoName is the message's Go type name.
func (m *Message) GoName(p *Protocol) string {
	if m.Name != "" {
		return m.Name
	}
	return fmt.Sprintf("%s%d", p.Name, m.ID)
}

func (c *Catalogue) validate() error {
	names := map[string]string{}
	for _, p := range c.Protocols {
		if len(p.Messages) != p.Max-p.Base {
			return fmt.Errorf("%s: %d messages for IDs %d..%d", p.Name, len(p.Messages), p.Base, p.Max-1)
		}
		for i, m := range p.Messages {
			if m.ID != p.Base+i {
				return fmt.Errorf("%s: message %d has ID %d", p.Name, i, m.ID)
			}
			n := m.GoName(p)
			if prev, ok := names[n]; ok {
				return fmt.Errorf("Go name %s used by %s and %s %d", n, prev, p.Name, m.ID)
			}
			names[n] = fmt.Sprintf("%s %d", p.Name, m.ID)
			if err := c.validateFields(m.Fields, fmt.Sprintf("%s %d", p.Name, m.ID)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Catalogue) validateFields(fs []*Field, where string) error {
	seen := map[string]bool{}
	for i, f := range fs {
		w := fmt.Sprintf("%s field %d", where, i)
		if f.Name != "" {
			if seen[f.Name] {
				return fmt.Errorf("%s: duplicate name %q", w, f.Name)
			}
			seen[f.Name] = true
		}
		switch {
		case f.Type == "struct":
			if err := c.validateFields(f.Fields, w); err != nil {
				return err
			}
		case c.typeByName(f.Type) != nil:
		case f.Type == "blob":
			if f.Size <= 0 {
				return fmt.Errorf("%s: blob without size", w)
			}
		default:
			known := false
			for _, t := range jamTypes {
				known = known || (t == f.Type && t != "struct")
			}
			if !known {
				return fmt.Errorf("%s: unknown type %q", w, f.Type)
			}
		}
		if f.Array != "" && f.Array != "[]" {
			if n, err := strconv.Atoi(f.Array); err != nil || n <= 0 {
				return fmt.Errorf("%s: bad array %q", w, f.Array)
			}
		}
	}
	return nil
}
