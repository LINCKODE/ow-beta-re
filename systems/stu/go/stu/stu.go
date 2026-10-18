// Package stu wraps the STUD v3 reader (systems/stu, C API capi/stu.h) for Go: open a raw STU file or an
// image dumped from the client, check layout CRCs, walk instances and read fields by hash.
// Format and library: docs/STU.md §1, §7.
//
// Build the static C API first: cmake -S systems/stu -B systems/stu/build -G Ninja && cmake --build systems/stu/build
//
// A Document is not safe for concurrent use (instances are decoded on first access). Values point into
// the document and are invalid (Valid() == false) once it is closed.
package stu

/*
#cgo CFLAGS: -I${SRCDIR}/../../capi
#cgo LDFLAGS: ${SRCDIR}/../../build/libow_stu_c_static.a ${SRCDIR}/../../build/libow_stu.a -lstdc++ -lm
#include <stdlib.h>
#include "stu.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

var (
	ErrType     = errors.New("stu: value has another type")
	ErrNotFound = errors.New("stu: not found")
	ErrInvalid  = errors.New("stu: invalid value or closed document")
)

// Mode says how pointers in the image are encoded.
type Mode int

const (
	File   Mode = C.STU_MODE_FILE   // raw STUD file (CASC bytes): offsets from the start of the file
	Loaded Mode = C.STU_MODE_LOADED // image dumped from the client: absolute pointers, base = its address
)

// ValueType is the type of a decoded value.
type ValueType int

const (
	Invalid   ValueType = -1
	Null      ValueType = C.STU_VALUE_NULL
	Primitive ValueType = C.STU_VALUE_PRIMITIVE
	String    ValueType = C.STU_VALUE_STRING
	Enum      ValueType = C.STU_VALUE_ENUM
	Object    ValueType = C.STU_VALUE_OBJECT // pointer to another instance
	Inline    ValueType = C.STU_VALUE_INLINE // inline object / decoded instance: items = fields
	List      ValueType = C.STU_VALUE_LIST
	Map       ValueType = C.STU_VALUE_MAP
	Ref       ValueType = C.STU_VALUE_REF // file / resource reference
	Error     ValueType = C.STU_VALUE_ERROR
)

func (t ValueType) String() string {
	if t == Invalid {
		return "invalid"
	}
	return C.GoString(C.stu_value_type_name(C.int(t)))
}

// Kind is a field's constraint kind (registry "ctype").
type Kind int

const (
	KindPrimitive       Kind = C.STU_KIND_PRIMITIVE
	KindPrimitiveList   Kind = C.STU_KIND_PRIMITIVE_LIST
	KindObject          Kind = C.STU_KIND_OBJECT
	KindObjectList      Kind = C.STU_KIND_OBJECT_LIST
	KindInline          Kind = C.STU_KIND_INLINE
	KindInlineList      Kind = C.STU_KIND_INLINE_LIST
	KindMap             Kind = C.STU_KIND_MAP
	KindEnum            Kind = C.STU_KIND_ENUM
	KindEnumList        Kind = C.STU_KIND_ENUM_LIST
	KindFileRef         Kind = C.STU_KIND_FILE_REF
	KindFileRefList     Kind = C.STU_KIND_FILE_REF_LIST
	KindResourceRef     Kind = C.STU_KIND_RESOURCE_REF
	KindResourceRefList Kind = C.STU_KIND_RESOURCE_REF_LIST
)

func (k Kind) String() string { return C.GoString(C.stu_kind_name(C.int(k))) }

// Primitive element hashes (Value.TypeHash of a Primitive).
const (
	PrimU8        uint32 = C.STU_PRIM_U8
	PrimS8        uint32 = C.STU_PRIM_S8
	PrimU16       uint32 = C.STU_PRIM_U16
	PrimS16       uint32 = C.STU_PRIM_S16
	PrimU32       uint32 = C.STU_PRIM_U32
	PrimS32       uint32 = C.STU_PRIM_S32
	PrimU64       uint32 = C.STU_PRIM_U64
	PrimS64       uint32 = C.STU_PRIM_S64
	PrimF32       uint32 = C.STU_PRIM_F32
	PrimF64       uint32 = C.STU_PRIM_F64
	PrimEntityID  uint32 = C.STU_PRIM_ENTITY_ID
	PrimVec2      uint32 = C.STU_PRIM_VEC2
	PrimVec3      uint32 = C.STU_PRIM_VEC3
	PrimVec3A     uint32 = C.STU_PRIM_VEC3A
	PrimVec4      uint32 = C.STU_PRIM_VEC4
	PrimQuat      uint32 = C.STU_PRIM_QUAT
	PrimColorRGB  uint32 = C.STU_PRIM_COLOR_RGB
	PrimColorRGBA uint32 = C.STU_PRIM_COLOR_RGBA
	PrimDateTime  uint32 = C.STU_PRIM_DATE_TIME
	PrimDBID      uint32 = C.STU_PRIM_DBID
	PrimUUID      uint32 = C.STU_PRIM_UUID
	PrimString    uint32 = C.STU_PRIM_STRING
	PrimMtx43A    uint32 = C.STU_PRIM_MTX43A
)

func rcErr(rc C.int) error {
	switch rc {
	case C.STU_OK:
		return nil
	case C.STU_ERR_TYPE:
		return ErrType
	case C.STU_ERR_NOT_FOUND:
		return ErrNotFound
	default:
		return ErrInvalid
	}
}

// --- documents ---------------------------------------------------------------------------------

// Document is a parsed STU image (it keeps its own copy of the bytes).
type Document struct{ h *C.stu_doc }

// Instance is an entry of the instance table.
type Instance struct {
	Index     int
	Offset    uint32 // from the start of the image
	Flags     uint32 // bit 0: has references to other resources
	LayoutCRC uint64
	Type      uint32 // 0 if unknown
	CRCOK     bool   // type known and its layout CRC matches the client's registry
}

// Open parses an image. base is the address of a Loaded image (ignored for File). Instances whose type
// is unknown or whose layout CRC mismatches are kept: see AllCRCOK.
func Open(data []byte, mode Mode, base uint64) (*Document, error) {
	var p *C.uint8_t
	if len(data) > 0 {
		p = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}
	var h *C.stu_doc
	var err [256]C.char
	if rc := C.stu_doc_open(p, C.size_t(len(data)), C.int(mode), C.uint64_t(base), &h, &err[0], C.size_t(len(err))); rc != C.STU_OK {
		return nil, fmt.Errorf("stu: %s", C.GoString(&err[0]))
	}
	return &Document{h}, nil
}

// OpenFile reads and parses a file.
func OpenFile(path string, mode Mode, base uint64) (*Document, error) {
	cs := C.CString(path)
	defer C.free(unsafe.Pointer(cs))
	var h *C.stu_doc
	var err [256]C.char
	if rc := C.stu_doc_open_file(cs, C.int(mode), C.uint64_t(base), &h, &err[0], C.size_t(len(err))); rc != C.STU_OK {
		return nil, fmt.Errorf("stu: %s", C.GoString(&err[0]))
	}
	return &Document{h}, nil
}

// Close frees the document; its Values become invalid.
func (d *Document) Close() {
	if d.h != nil {
		C.stu_doc_close(d.h)
		d.h = nil
	}
}

func (d *Document) Mode() Mode        { return Mode(C.stu_doc_mode(d.h)) }
func (d *Document) Size() int         { return int(C.stu_doc_size(d.h)) }
func (d *Document) NumInstances() int { return int(C.stu_doc_instance_count(d.h)) }
func (d *Document) Root() int         { return int(C.stu_doc_root(d.h)) }
func (d *Document) AllCRCOK() bool    { return C.stu_doc_all_crc_ok(d.h) != 0 }
func (d *Document) InstanceAt(offset uint64) int {
	return int(C.stu_doc_instance_at(d.h, C.uint64_t(offset)))
}

func (d *Document) Instance(i int) (Instance, error) {
	var ii C.stu_instance_info
	if err := rcErr(C.stu_doc_instance(d.h, C.int(i), &ii)); err != nil {
		return Instance{}, err
	}
	return Instance{i, uint32(ii.offset), uint32(ii.flags), uint64(ii.layout_crc), uint32(ii.type_hash), ii.crc_ok != 0}, nil
}

func (d *Document) Instances() []Instance {
	out := make([]Instance, d.NumInstances())
	for i := range out {
		out[i], _ = d.Instance(i)
	}
	return out
}

// Decode returns instance i as an Inline value (an Error value if its type is unknown; invalid if
// out of range). Decoded once and cached.
func (d *Document) Decode(i int) Value {
	if d.h == nil {
		return Value{}
	}
	return Value{d, C.stu_doc_decode(d.h, C.int(i))}
}

// JSON of the whole document (stu_dump's format).
func (d *Document) JSON(withHandles bool) string {
	if d.h == nil {
		return ""
	}
	return takeString(C.stu_doc_json(d.h, cbool(withHandles)))
}

func cbool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

func takeString(p *C.char) string {
	if p == nil {
		return ""
	}
	defer C.stu_free(unsafe.Pointer(p))
	return C.GoString(p)
}

// --- values ------------------------------------------------------------------------------------

// Value is a node of a decoded tree. The zero Value is invalid; lookups on an invalid Value return
// invalid Values, so chains like doc.Decode(0).Field(a).Index(2).Field(b) need one check at the end.
type Value struct {
	d *Document
	v *C.stu_value
}

// ValueInfo describes a value without further calls.
type ValueInfo struct {
	Type      ValueType
	Kind      Kind   // constraint kind of the field the value came from
	FieldHash uint32 // fields of an Inline value
	TypeHash  uint32 // primitive / enum / STU type of an Inline, Object, Ref target, list element
	MapKey    uint32 // map entries
	Flags     uint32 // field flags (registry)
	Count     int    // fields / elements / entries
	Instance  int    // Object: instance index; -1 otherwise
	GUID      uint64 // Ref
	Handle    uint64 // Ref
	RawSize   int    // Primitive / Enum
}

func (v Value) ok() bool { return v.v != nil && v.d != nil && v.d.h != nil }

// Valid is false for the zero Value, missing fields/items, and values of a closed document.
func (v Value) Valid() bool { return v.ok() }

func (v Value) wrap(p *C.stu_value) Value {
	if p == nil {
		return Value{}
	}
	return Value{v.d, p}
}

func (v Value) Type() ValueType {
	if !v.ok() {
		return Invalid
	}
	return ValueType(C.stu_value_type(v.v))
}

func (v Value) Info() ValueInfo {
	if !v.ok() {
		return ValueInfo{Type: Invalid, Instance: -1}
	}
	var i C.stu_value_info
	C.stu_value_get_info(v.v, &i)
	return ValueInfo{ValueType(i._type), Kind(i.kind), uint32(i.field_hash), uint32(i.type_hash), uint32(i.map_key),
		uint32(i.flags), int(i.count), int(i.instance), uint64(i.guid), uint64(i.handle), int(i.raw_size)}
}

func (v Value) FieldHash() uint32 { return v.Info().FieldHash }
func (v Value) TypeHash() uint32  { return v.Info().TypeHash }
func (v Value) MapKey() uint32    { return v.Info().MapKey }
func (v Value) Kind() Kind        { return v.Info().Kind }

// Len is the number of fields (Inline), elements (List) or entries (Map).
func (v Value) Len() int {
	if !v.ok() {
		return 0
	}
	return int(C.stu_value_count(v.v))
}

// Index returns the i-th field (Inline, registry order, parents first), element (List) or entry (Map).
func (v Value) Index(i int) Value {
	if !v.ok() {
		return Value{}
	}
	return v.wrap(C.stu_value_item(v.v, C.int(i)))
}

// Items returns all fields / elements / entries.
func (v Value) Items() []Value {
	out := make([]Value, v.Len())
	for i := range out {
		out[i] = v.Index(i)
	}
	return out
}

// Field looks up a field of an Inline value by its hash.
func (v Value) Field(hash uint32) Value {
	if !v.ok() {
		return Value{}
	}
	return v.wrap(C.stu_value_field(v.v, C.uint32_t(hash)))
}

// Path follows a chain of field hashes.
func (v Value) Path(hashes ...uint32) Value {
	for _, h := range hashes {
		v = v.Field(h)
	}
	return v
}

// Get looks up a Map entry by key.
func (v Value) Get(key uint32) Value {
	if !v.ok() {
		return Value{}
	}
	return v.wrap(C.stu_value_map_get(v.v, C.uint32_t(key)))
}

// Int reads an integer primitive (u8..s64, teEntityID) or an enum. u64 comes back as its bit pattern.
func (v Value) Int() (int64, error) {
	if !v.ok() {
		return 0, ErrInvalid
	}
	var x C.int64_t
	return int64(x), rcErr(C.stu_value_int(v.v, &x))
}

// Float reads an f32 / f64.
func (v Value) Float() (float64, error) {
	if !v.ok() {
		return 0, ErrInvalid
	}
	var x C.double
	return float64(x), rcErr(C.stu_value_float(v.v, &x))
}

// Floats reads a float-vector primitive (teVec2/3/3A/4, teQuat, teColorRGB(A), teMtx43A).
func (v Value) Floats() ([]float32, error) {
	if !v.ok() {
		return nil, ErrInvalid
	}
	var buf [16]C.float
	var n C.int
	if err := rcErr(C.stu_value_floats(v.v, &buf[0], C.int(len(buf)), &n)); err != nil {
		return nil, err
	}
	out := make([]float32, int(n))
	for i := range out {
		out[i] = float32(buf[i])
	}
	return out, nil
}

// Raw returns a copy of the bytes of a Primitive / Enum.
func (v Value) Raw() ([]byte, error) {
	if !v.ok() {
		return nil, ErrInvalid
	}
	var p *C.uint8_t
	var n C.size_t
	if err := rcErr(C.stu_value_raw(v.v, &p, &n)); err != nil {
		return nil, err
	}
	return C.GoBytes(unsafe.Pointer(p), C.int(n)), nil
}

// Str reads a teString.
func (v Value) Str() (string, error) {
	if !v.ok() {
		return "", ErrInvalid
	}
	var p *C.char
	var n C.size_t
	if err := rcErr(C.stu_value_string(v.v, &p, &n)); err != nil {
		return "", err
	}
	return C.GoStringN(p, C.int(n)), nil
}

// Ref reads a file / resource reference: its GUID and handle (-1 in a raw file).
func (v Value) Ref() (guid, handle uint64, err error) {
	if !v.ok() {
		return 0, 0, ErrInvalid
	}
	var g, h C.uint64_t
	err = rcErr(C.stu_value_ref(v.v, &g, &h))
	return uint64(g), uint64(h), err
}

// Object returns the instance index an Object value points at (ErrNotFound for a null pointer).
func (v Value) Object() (int, error) {
	if !v.ok() {
		return -1, ErrInvalid
	}
	var i C.int
	if err := rcErr(C.stu_value_object(v.v, &i)); err != nil {
		return -1, err
	}
	return int(i), nil
}

// Deref decodes the instance an Object value points at (invalid for null / non-objects).
func (v Value) Deref() Value {
	i, err := v.Object()
	if err != nil {
		return Value{}
	}
	return v.d.Decode(i)
}

// Err is the message of an Error value, "" otherwise.
func (v Value) Err() string {
	if !v.ok() {
		return ""
	}
	if p := C.stu_value_error(v.v); p != nil {
		return C.GoString(p)
	}
	return ""
}

// ErrorCount is the number of Error values in the subtree.
func (v Value) ErrorCount() int {
	if !v.ok() {
		return 0
	}
	return int(C.stu_value_error_count(v.v))
}

// JSON of the subtree (names resolved through the registry).
func (v Value) JSON(withHandles bool) string {
	if !v.ok() {
		return ""
	}
	return takeString(C.stu_value_json(v.v, cbool(withHandles)))
}

// --- registry (the client's, build 24919) ------------------------------------------------------

// Name of a type / field / enum hash, "" if unknown.
func Name(hash uint32) string {
	if p := C.stu_name(C.uint32_t(hash)); p != nil {
		return C.GoString(p)
	}
	return ""
}

// NameOr returns the name or "0x%08x".
func NameOr(hash uint32) string {
	if n := Name(hash); n != "" {
		return n
	}
	return fmt.Sprintf("0x%08x", hash)
}

type TypeInfo struct {
	Hash      uint32
	Parent    uint32 // 0 = none
	Size      uint32
	LayoutCRC uint64
	Fields    []FieldInfo // all fields, parents first (the order of an Inline value's items)
}

type FieldInfo struct {
	Hash   uint32
	Offset uint32
	Kind   Kind
	Elem   uint32 // element type: primitive hash or STU type
	Flags  uint32
}

// LookupType returns a type of the client's registry.
func LookupType(hash uint32) (TypeInfo, bool) {
	var t C.stu_type_info
	if C.stu_type(C.uint32_t(hash), &t) != C.STU_OK {
		return TypeInfo{}, false
	}
	ti := TypeInfo{uint32(t.hash), uint32(t.parent), uint32(t.size), uint64(t.layout_crc), make([]FieldInfo, int(t.field_count))}
	for i := range ti.Fields {
		var f C.stu_field_info
		C.stu_type_field(C.uint32_t(hash), C.int(i), &f)
		ti.Fields[i] = FieldInfo{uint32(f.hash), uint32(f.offset), Kind(f.kind), uint32(f.elem), uint32(f.flags)}
	}
	return ti, true
}

// IsA tells whether typ is ancestor or derives from it.
func IsA(typ, ancestor uint32) bool {
	return C.stu_type_is_a(C.uint32_t(typ), C.uint32_t(ancestor)) != 0
}

// TypeCount is the number of types in the client's registry.
func TypeCount() int { return int(C.stu_type_count()) }
