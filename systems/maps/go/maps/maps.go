// Package maps wraps the map data reader/writer (systems/maps, C API capi/maps.h) for Go: parse a map data
// blob (resource type 0x002, 0x080…) into its header, records, collision summary and sound-space links, and
// edit it (move a record, change an entity's definition, add an entity). Format: docs/Maps.md §2, §7.
//
// Build the static C API first: cmake -S systems/maps -B systems/maps/build -G Ninja && cmake --build systems/maps/build
//
// A Doc is not safe for concurrent use. Map values are plain Go data (decoded from the library's JSON).
package maps

/*
#cgo CFLAGS: -I${SRCDIR}/../../capi
#cgo LDFLAGS: ${SRCDIR}/../../build/libow_maps_c_static.a ${SRCDIR}/../../build/libow_maps.a -lstdc++ -lm
#include <stdlib.h>
#include "maps.h"
*/
import "C"

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unsafe"
)

// Record types (record +0x13), docs/Maps.md §2.4.
const (
	ModelGroup      = 1
	Model           = 2
	Occluder        = 3
	ReflectionProbe = 4
	TextLabel       = 6
	MapText         = 7
	PlacedModel     = 8
	Light           = 9
	SoundVolume     = 10
	Entity          = 11
	SoundEmitter    = 12
	Effect          = 13
	Volume44        = 14
	Volume45        = 15
	FirstPlacedType = 8
)

// TypeNames are the record type names (index = type).
var TypeNames = [16]string{"type 0", "model group", "model", "occluder", "reflection probe", "type 5", "text label",
	"map text", "placed model", "light", "sound volume", "entity", "sound emitter", "effect", "volume 0x44", "volume 0x45"}

// GUID is a resource GUID; it (un)marshals as "0x%016x".
type GUID uint64

func (g GUID) String() string               { return fmt.Sprintf("0x%016x", uint64(g)) }
func (g GUID) MarshalJSON() ([]byte, error) { return []byte(`"` + g.String() + `"`), nil }
func (g *GUID) UnmarshalJSON(b []byte) error {
	v, err := strconv.ParseUint(strings.TrimPrefix(strings.Trim(string(b), `"`), "0x"), 16, 64)
	*g = GUID(v)
	return err
}

// Type is the GUID's resource type (bitreverse12((guid >> 48) & 0xFFF) + 1).
func (g GUID) Type() uint32 { return uint32(C.maps_guid_type(C.uint64_t(g))) }

// Hex is a number the library writes as "0x…".
type Hex uint64

func (h Hex) MarshalJSON() ([]byte, error) { return []byte(fmt.Sprintf(`"0x%x"`, uint64(h))), nil }
func (h *Hex) UnmarshalJSON(b []byte) error {
	v, err := strconv.ParseUint(strings.TrimPrefix(strings.Trim(string(b), `"`), "0x"), 16, 64)
	*h = Hex(v)
	return err
}

type Transform struct {
	Pos   [3]float32 `json:"pos"`
	Scale [3]float32 `json:"scale"`
	Rot   [4]float32 `json:"rot"` // quaternion x, y, z, w
}

type InstanceData struct {
	Slot   uint32 `json:"slot"`
	Offset Hex    `json:"offset"` // absolute offset of the STUD blob in the map data
	Size   uint32 `json:"size"`
}

type GroupInstance struct {
	Look GUID      `json:"look"`
	Xf   Transform `json:"xf"`
}

type Shape struct {
	Kind string     `json:"kind"` // box, sphere, capsule
	Pos  [3]float32 `json:"pos"`
	Rot  [4]float32 `json:"rot"`
	Half [3]float32 `json:"half"`
}

// Record is one placement record. Fields holds the decoded fields as [name, offset, value] (with fields=true).
type Record struct {
	Offset       Hex             `json:"offset"`
	Size         uint32          `json:"size"`
	Type         int             `json:"type"`
	Flags        int             `json:"flags"`
	Phase        int             `json:"phase"`
	UUID         string          `json:"uuid"`
	Index        int             `json:"index"`
	Placed       int             `json:"placed"` // placed-entry index (-1 for types < 8)
	Primary      GUID            `json:"primary,omitempty"`
	Secondary    GUID            `json:"secondary,omitempty"`
	Xf           *Transform      `json:"xf,omitempty"`
	XfWritable   bool            `json:"xf_writable,omitempty"`
	InstanceData []InstanceData  `json:"instance_data,omitempty"`
	Instances    []GroupInstance `json:"instances,omitempty"`
	Shapes       []Shape         `json:"shapes,omitempty"`
	Fields       [][3]any        `json:"fields,omitempty"`
}

type Section struct {
	Type   int    `json:"type"`
	Name   string `json:"name"`
	Offset Hex    `json:"offset"`
	Count  int    `json:"count"`
	Size   uint32 `json:"size"`
}

type Env struct {
	GamemodeDef GUID       `json:"gamemode_def"`
	Lighting    GUID       `json:"lighting"`
	SkyModel    GUID       `json:"sky_model"`
	SkyLook     GUID       `json:"sky_look"`
	SoundSpace  GUID       `json:"sound_space"`
	Name        GUID       `json:"name"`
	TextureF8   GUID       `json:"texture_f8"`
	Texture108  GUID       `json:"texture_108"`
	Texture128  GUID       `json:"texture_128"`
	Texture130  GUID       `json:"texture_130"`
	Texture180  GUID       `json:"texture_180"`
	Texture188  GUID       `json:"texture_188"`
	Texture190  GUID       `json:"texture_190"`
	SunDir      [3]float32 `json:"sun_dir"`
}

type Bounds struct {
	Min [4]float32 `json:"min"`
	Max [4]float32 `json:"max"`
}

type Header struct {
	Bounds       []Bounds   `json:"bounds"`
	CameraPos    [4]float32 `json:"camera_pos"`
	Env          Env        `json:"env"`
	RecordCount  int        `json:"record_count"`
	Sections     []Section  `json:"sections"`
	Nav          Hex        `json:"nav"`
	NavSize      uint32     `json:"nav_size"`
	Tactical     Hex        `json:"tactical"`
	TacticalSize uint32     `json:"tactical_size"`
	BlobBegin    Hex        `json:"blob_begin"`
	BlobEnd      Hex        `json:"blob_end"`
}

// CollisionMesh is the summary of the map's triangle mesh (docs/Maps.md §2.6); Geometry decodes its contents.
type CollisionMesh struct {
	Tris       int `json:"tris"`
	Verts      int `json:"verts"`
	BVHNodes   int `json:"bvh_nodes"`
	BVHDepth   int `json:"bvh_depth"`
	TriOffset  Hex `json:"tri_offset"`
	VertOffset Hex `json:"vert_offset"`
	BVHOffset  Hex `json:"bvh_offset"`
}

type Collision struct {
	Spheres  int             `json:"spheres"`
	Capsules int             `json:"capsules"`
	Hulls    int             `json:"hulls"`
	Meshes   []CollisionMesh `json:"meshes"`
}

type SoundSpaceLink struct {
	Volume string `json:"volume"`
	Space  GUID   `json:"space"`
}

// Map is a parsed map data blob.
type Map struct {
	Size        int              `json:"size"`
	Header      Header           `json:"header"`
	Placed      int              `json:"placed"`
	Collision   Collision        `json:"collision"`
	SoundSpaces []SoundSpaceLink `json:"sound_spaces"`
	Records     []Record         `json:"records"`
}

// ByPlaced returns the record with a placed-entry index, or nil.
func (m *Map) ByPlaced(i int) *Record {
	for k := range m.Records {
		if m.Records[k].Placed == i {
			return &m.Records[k]
		}
	}
	return nil
}

// Doc is an open map data blob (the library holds a copy of the bytes; edits change the copy).
type Doc struct{ d *C.maps_doc }

var ErrClosed = errors.New("maps: document is closed")

// Open parses a map data blob.
func Open(b []byte) (*Doc, error) {
	if len(b) == 0 {
		return nil, errors.New("maps: empty data")
	}
	var buf [256]C.char
	d := C.maps_open((*C.uint8_t)(unsafe.Pointer(&b[0])), C.size_t(len(b)), &buf[0], C.size_t(len(buf)))
	if d == nil {
		return nil, fmt.Errorf("maps: %s", C.GoString(&buf[0]))
	}
	return &Doc{d}, nil
}

func (d *Doc) Close() {
	if d.d != nil {
		C.maps_close(d.d)
		d.d = nil
	}
}

// Map decodes the document (records: include the record list; fields: include per-record fields).
func (d *Doc) Map(records, fields bool) (*Map, error) {
	if d.d == nil {
		return nil, ErrClosed
	}
	var n C.size_t
	p := C.maps_json(d.d, cbool(records), cbool(fields), &n)
	var m Map
	if err := json.Unmarshal(C.GoBytes(unsafe.Pointer(p), C.int(n)), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// JSON is the library's JSON of the document.
func (d *Doc) JSON(records, fields bool) (string, error) {
	if d.d == nil {
		return "", ErrClosed
	}
	var n C.size_t
	p := C.maps_json(d.d, cbool(records), cbool(fields), &n)
	return C.GoStringN(p, C.int(n)), nil
}

// Bytes is a copy of the document's current bytes.
func (d *Doc) Bytes() []byte {
	if d.d == nil {
		return nil
	}
	var n C.size_t
	p := C.maps_bytes(d.d, &n)
	return C.GoBytes(unsafe.Pointer(p), C.int(n))
}

func (d *Doc) err(rc C.int) error {
	if rc == C.MAPS_OK {
		return nil
	}
	return fmt.Errorf("maps: %s (%d)", C.GoString(C.maps_error(d.d)), int(rc))
}

// SetTransform rewrites the transform of the record at map-data offset off (nil scale = keep).
func (d *Doc) SetTransform(off uint32, pos [3]float32, scale *[3]float32, rot [4]float32) error {
	if d.d == nil {
		return ErrClosed
	}
	var sp *C.float
	if scale != nil {
		sp = (*C.float)(&scale[0])
	}
	return d.err(C.maps_set_transform(d.d, C.uint32_t(off), (*C.float)(&pos[0]), sp, (*C.float)(&rot[0])))
}

// SetDefinition sets an entity record's definition.
func (d *Doc) SetDefinition(off uint32, def GUID) error {
	if d.d == nil {
		return ErrClosed
	}
	return d.err(C.maps_set_definition(d.d, C.uint32_t(off), C.uint64_t(def)))
}

// AddEntity appends a copy of the entity record at src with a new placement GUID (32 hex digits) and transform.
func (d *Doc) AddEntity(src uint32, uuid string, pos [3]float32, scale *[3]float32, rot [4]float32) error {
	if d.d == nil {
		return ErrClosed
	}
	u, err := hex.DecodeString(uuid)
	if err != nil || len(u) != 16 {
		return fmt.Errorf("maps: bad uuid %q", uuid)
	}
	var sp *C.float
	if scale != nil {
		sp = (*C.float)(&scale[0])
	}
	return d.err(C.maps_add_entity(d.d, C.uint32_t(src), (*C.uint8_t)(unsafe.Pointer(&u[0])), (*C.float)(&pos[0]), sp,
		(*C.float)(&rot[0])))
}

func cbool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
