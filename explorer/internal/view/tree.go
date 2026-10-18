// Package view turns decoded STU documents into plain, JSON-friendly trees for the UI: instance tables,
// field trees with names resolved, references, Statescript graphs and entity definitions.
//
// Every node carries a stable Path (instance, then field hashes / list indices / map keys) so a future
// editor can address a value without holding decoder pointers.
package view

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"owexplorer/internal/names"
	"owstu/stu"
)

// Resolver names things the tree refers to; the catalog implements it.
type Resolver interface {
	TypeName(hash uint32) (string, bool)
	GUIDName(guid uint64) string
	Exists(guid uint64) bool
}

// Node is one value of a decoded tree.
type Node struct {
	Path      string `json:"path"`
	Label     string `json:"label"`               // field name, "[i]" or map key
	FieldHash uint32 `json:"fieldHash,omitempty"` // fields of inline objects
	Kind      string `json:"kind"`                // null prim string enum object inline list map ref error
	Type      string `json:"type,omitempty"`      // STU type / primitive / enum name
	TypeHash  uint32 `json:"typeHash,omitempty"`
	Named     bool   `json:"named"`           // Label / Type come from a name table (not a raw hash)
	Value     string `json:"value,omitempty"` // display text
	Alt       string `json:"alt,omitempty"`   // secondary display (hex of an integer, …)
	GUID      string `json:"guid,omitempty"`  // ref target, 16 hex digits
	RefName   string `json:"refName,omitempty"`
	Missing   bool   `json:"missing,omitempty"`  // ref target not in the install
	Instance  int    `json:"instance"`           // object target (-1 = none)
	Count     int    `json:"count,omitempty"`    // items of lists / maps / inline objects
	Children  []Node `json:"children,omitempty"` // inline fields, list elements, map entries
}

// InstanceRow is an entry of a document's instance table.
type InstanceRow struct {
	Index    int    `json:"index"`
	Offset   uint32 `json:"offset"`
	Flags    uint32 `json:"flags"`
	CRC      string `json:"crc"`
	Type     string `json:"type"`
	TypeHash uint32 `json:"typeHash"`
	Named    bool   `json:"named"`
	CRCOK    bool   `json:"crcOk"`
	Root     bool   `json:"root"`
	Errors   int    `json:"errors"`
	Summary  string `json:"summary,omitempty"` // a short hint (display name, first string, …)
}

// Document is the instance table of a parsed STU file.
type Document struct {
	Size      int           `json:"size"`
	Root      int           `json:"root"`
	AllCRCOK  bool          `json:"allCrcOk"`
	Instances []InstanceRow `json:"instances"`
}

var primNames = map[uint32]string{
	stu.PrimU8: "u8", stu.PrimS8: "s8", stu.PrimU16: "u16", stu.PrimS16: "s16", stu.PrimU32: "u32",
	stu.PrimS32: "s32", stu.PrimU64: "u64", stu.PrimS64: "s64", stu.PrimF32: "f32", stu.PrimF64: "f64",
	stu.PrimEntityID: "teEntityID", stu.PrimVec2: "teVec2", stu.PrimVec3: "teVec3", stu.PrimVec3A: "teVec3A",
	stu.PrimVec4: "teVec4", stu.PrimQuat: "teQuat", stu.PrimColorRGB: "teColorRGB",
	stu.PrimColorRGBA: "teColorRGBA", stu.PrimDateTime: "teDateTime", stu.PrimDBID: "DBID",
	stu.PrimUUID: "teUUID", stu.PrimString: "teString", stu.PrimMtx43A: "teMtx43A",
}

// Describe builds the instance table. Decoding every instance also counts its errors.
func Describe(d *stu.Document, r Resolver) Document {
	out := Document{Size: d.Size(), Root: d.Root(), AllCRCOK: d.AllCRCOK()}
	for _, in := range d.Instances() {
		name, named := r.TypeName(in.Type)
		v := d.Decode(in.Index)
		out.Instances = append(out.Instances, InstanceRow{
			Index: in.Index, Offset: in.Offset, Flags: in.Flags, CRC: fmt.Sprintf("%016x", in.LayoutCRC),
			Type: name, TypeHash: in.Type, Named: named, CRCOK: in.CRCOK, Root: in.Index == out.Root,
			Errors: v.ErrorCount(), Summary: summary(v),
		})
	}
	return out
}

var (
	hDisplayName = names.Hash("m_displayName")
	hComment     = names.Hash("m_comment")
	hDebugText   = names.Hash("m_debugText")
	hBodyText    = names.Hash("m_bodyText")
)

// summary picks a short hint for an instance row: a display name / comment / first non-empty string.
func summary(v stu.Value) string {
	for _, h := range []uint32{hDisplayName, hBodyText, hComment, hDebugText} {
		if s, err := v.Field(h).Str(); err == nil && s != "" {
			return clip(s, 80)
		}
	}
	for _, it := range v.Items() {
		if it.Type() == stu.String {
			if s, err := it.Str(); err == nil && s != "" {
				return clip(s, 80)
			}
		}
	}
	return ""
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// Tree converts instance i into a Node tree.
func Tree(d *stu.Document, i int, r Resolver) Node {
	v := d.Decode(i)
	n := convert(v, r, "i"+strconv.Itoa(i))
	n.Label = "instance " + strconv.Itoa(i)
	return n
}

func convert(v stu.Value, r Resolver, path string) Node {
	info := v.Info()
	n := Node{Path: path, Instance: -1, TypeHash: info.TypeHash}
	switch info.Type {
	case stu.Null:
		n.Kind = "null"
		n.Value = "null"
		if info.Kind == stu.KindObject || info.Kind == stu.KindObjectList {
			n.Type, n.Named = r.TypeName(info.TypeHash)
		}
	case stu.Primitive:
		n.Kind = "prim"
		n.Type = primNames[info.TypeHash]
		if n.Type == "" {
			n.Type, _ = r.TypeName(info.TypeHash)
		}
		n.Named = true
		n.Value, n.Alt = primitive(v, info.TypeHash)
	case stu.String:
		n.Kind = "string"
		n.Type, n.Named = "teString", true
		n.Value, _ = v.Str()
	case stu.Enum:
		n.Kind = "enum"
		n.Type, n.Named = r.TypeName(info.TypeHash)
		if x, err := v.Int(); err == nil {
			n.Value = strconv.FormatInt(x, 10)
			n.Alt = fmt.Sprintf("0x%x", uint64(x)&mask(info.RawSize))
		}
	case stu.Object:
		n.Kind = "object"
		n.Type, n.Named = r.TypeName(info.TypeHash)
		n.Instance = info.Instance
		n.Value = "→ instance " + strconv.Itoa(info.Instance)
	case stu.Inline:
		n.Kind = "inline"
		n.Type, n.Named = r.TypeName(info.TypeHash)
		n.Count = info.Count
		for _, it := range v.Items() {
			fh := it.Info().FieldHash
			c := convert(it, r, path+".f"+hex32(fh))
			c.FieldHash = fh
			c.Label, c.Named = r.TypeName(fh)
			n.Children = append(n.Children, c)
		}
	case stu.List:
		n.Kind = "list"
		n.Type, _ = r.TypeName(info.TypeHash)
		if p := primNames[info.TypeHash]; p != "" {
			n.Type = p
		}
		n.Count = info.Count
		for k, it := range v.Items() {
			c := convert(it, r, path+".["+strconv.Itoa(k)+"]")
			c.Label = "[" + strconv.Itoa(k) + "]"
			n.Children = append(n.Children, c)
		}
	case stu.Map:
		n.Kind = "map"
		n.Type, _ = r.TypeName(info.TypeHash)
		n.Count = info.Count
		for _, it := range v.Items() {
			key := it.Info().MapKey
			c := convert(it, r, path+".k"+hex32(key))
			c.Label, _ = r.TypeName(key)
			n.Children = append(n.Children, c)
		}
	case stu.Ref:
		n.Kind = "ref"
		n.Type, n.Named = r.TypeName(info.TypeHash)
		n.GUID = fmt.Sprintf("%016x", info.GUID)
		if info.GUID == 0 {
			n.Value = "none"
		} else {
			n.RefName = r.GUIDName(info.GUID)
			n.Missing = !r.Exists(info.GUID)
		}
	case stu.Error:
		n.Kind = "error"
		n.Value = v.Err()
	default:
		n.Kind = "error"
		n.Value = "invalid value"
	}
	return n
}

func mask(size int) uint64 {
	if size <= 0 || size >= 8 {
		return math.MaxUint64
	}
	return 1<<(8*size) - 1
}

func hex32(h uint32) string { return fmt.Sprintf("%08x", h) }

func primitive(v stu.Value, t uint32) (string, string) {
	switch t {
	case stu.PrimU8, stu.PrimU16, stu.PrimU32:
		x, _ := v.Int()
		return strconv.FormatInt(x, 10), fmt.Sprintf("0x%x", x)
	case stu.PrimS8, stu.PrimS16, stu.PrimS32, stu.PrimS64:
		x, _ := v.Int()
		return strconv.FormatInt(x, 10), ""
	case stu.PrimU64:
		x, _ := v.Int()
		return strconv.FormatUint(uint64(x), 10), fmt.Sprintf("0x%016x", uint64(x))
	case stu.PrimEntityID:
		x, _ := v.Int()
		return fmt.Sprintf("0x%x", uint64(x)), ""
	case stu.PrimF32, stu.PrimF64:
		f, _ := v.Float()
		return strconv.FormatFloat(f, 'g', 7, 64), ""
	case stu.PrimVec2, stu.PrimVec3, stu.PrimVec3A, stu.PrimVec4, stu.PrimQuat, stu.PrimColorRGB,
		stu.PrimColorRGBA, stu.PrimMtx43A:
		fs, err := v.Floats()
		if err != nil {
			break
		}
		parts := make([]string, len(fs))
		for i, f := range fs {
			parts[i] = strconv.FormatFloat(float64(f), 'g', 6, 32)
		}
		return "(" + strings.Join(parts, ", ") + ")", ""
	}
	if b, err := v.Raw(); err == nil {
		return fmt.Sprintf("%x", b), ""
	}
	return "?", ""
}

// Refs lists every resource GUID the document references (deduplicated, in first-seen order).
func Refs(d *stu.Document) []uint64 {
	seen := map[uint64]bool{}
	var out []uint64
	var walk func(v stu.Value)
	walk = func(v stu.Value) {
		info := v.Info()
		switch info.Type {
		case stu.Ref:
			if info.GUID != 0 && !seen[info.GUID] {
				seen[info.GUID] = true
				out = append(out, info.GUID)
			}
		case stu.Inline, stu.List, stu.Map:
			if info.Kind == stu.KindPrimitiveList || info.Kind == stu.KindEnumList {
				return
			}
			for i := range info.Count {
				walk(v.Index(i))
			}
		}
	}
	for i := range d.NumInstances() {
		walk(d.Decode(i))
	}
	return out
}
