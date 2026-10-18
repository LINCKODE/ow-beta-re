// Package content wraps the offline content reader (systems/content, C API capi/content.h) for Go: open a
// local Overwatch beta 0.8 install, list its packages and resources, and read resources by GUID or by
// content key. Formats and library: docs/ContentAccess.md.
//
// Build the static C API first: cmake -S systems/content -B systems/content/build -G Ninja && cmake --build systems/content/build
//
// A Store is safe for concurrent use (reads are thread-safe in the library; Close waits for running
// calls). Returned slices are Go copies and stay valid after Close.
package content

/*
#cgo CFLAGS: -I${SRCDIR}/../../capi
#cgo LDFLAGS: ${SRCDIR}/../../build/libow_content_c_static.a ${SRCDIR}/../../build/libow_content.a -lz -lstdc++ -lm -lpthread
#include <stdlib.h>
#include "content.h"
*/
import "C"

import (
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sync"
	"unsafe"
)

var (
	ErrNotFound = errors.New("content: resource not found")
	ErrClosed   = errors.New("content: store is closed")
)

// Resource types (TypeOf), as the client dispatches them (docs/ResourceLoading.md §1).
const (
	TypeEntityDefinition uint32 = 0x003
	TypeStatescriptGraph uint32 = 0x01B
	TypeAssetPackage     uint32 = 0x077
)

// TypeOf is a GUID's resource type: bitreverse12((guid >> 48) & 0xFFF) + 1.
func TypeOf(guid uint64) uint32 { return uint32(C.content_guid_type(C.uint64_t(guid))) }

// CKey is a content key: MD5 of the decoded bytes.
type CKey [16]byte

func (k CKey) String() string { return hex.EncodeToString(k[:]) }

// ParseCKey parses 32 hex digits.
func ParseCKey(s string) (CKey, error) {
	var k CKey
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 {
		return k, fmt.Errorf("content: bad ckey %q", s)
	}
	copy(k[:], b)
	return k, nil
}

// Resource is an entry of the store's resource list.
type Resource struct {
	GUID    uint64
	Package uint64 // first package listing the resource; 0 for packages themselves
	Type    uint32 // TypeOf(GUID)
	Size    uint32 // decoded size in bytes
	Flags   uint32 // record flags (docs/ContentAccess.md §4)
	CKey    CKey
}

// Package is an asset package of the manifests.
type Package struct {
	GUID        uint64
	KeyName     uint64 // TACT key name the manifest associates with the package
	Size        uint32
	RecordCount uint32
	CKey        CKey
	Manifest    string // e.g. "Win_SPWin_RDEV_LenUS_EExt"
	Asset       uint64 // the asset the package belongs to (APM item +0x08: map data, hero, …)
}

// Options for Open. The zero value gives the defaults (RDEV / enUS, speech manifest, built-in keyring,
// no per-read MD5 check).
type Options struct {
	Region        string              // "" = "RDEV"
	Locale        string              // "" = "enUS"
	NoSpeech      bool                // skip the speech manifest
	VerifyMD5     bool                // check MD5(decoded) == CKey on every read
	NoBuiltinKeys bool                // start from an empty keyring instead of the built-in 0.8 keys
	ExtraKeys     map[uint64][16]byte // TACT key name -> key, added to the keyring
}

// Store is an open install. Close it when done.
type Store struct {
	mu sync.RWMutex // Close (write) vs calls (read)
	h  *C.content_store
}

const errSize = 512

// Open opens a local 0.8 install (the directory with .build.info). opt may be nil.
func Open(installDir string, opt *Options) (*Store, error) {
	if opt == nil {
		opt = &Options{}
	}
	var o C.content_options
	C.content_options_init(&o)
	dir := C.CString(installDir)
	defer C.free(unsafe.Pointer(dir))
	if opt.Region != "" {
		o.region = C.CString(opt.Region)
		defer C.free(unsafe.Pointer(o.region))
	}
	if opt.Locale != "" {
		o.locale = C.CString(opt.Locale)
		defer C.free(unsafe.Pointer(o.locale))
	}
	o.speech = cbool(!opt.NoSpeech)
	o.verify_md5 = cbool(opt.VerifyMD5)
	o.builtin_keys = cbool(!opt.NoBuiltinKeys)
	if n := len(opt.ExtraKeys); n > 0 {
		// C memory: the options struct is Go memory and may only hold C pointers.
		keys := (*C.content_key)(C.malloc(C.size_t(n) * C.sizeof_content_key))
		defer C.free(unsafe.Pointer(keys))
		ks := unsafe.Slice(keys, n)
		i := 0
		for name, key := range opt.ExtraKeys {
			ks[i].name = C.uint64_t(name)
			for j := range key {
				ks[i].key[j] = C.uint8_t(key[j])
			}
			i++
		}
		o.extra_keys = keys
		o.extra_key_count = C.size_t(n)
	}
	var err [errSize]C.char
	h := C.content_open_ow08_ex(dir, &o, &err[0], errSize)
	if h == nil {
		return nil, fmt.Errorf("content: open %s: %s", installDir, C.GoString(&err[0]))
	}
	return &Store{h: h}, nil
}

// Close frees the store. Calls after Close return ErrClosed / empty results.
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.h != nil {
		C.content_close(s.h)
		s.h = nil
	}
}

// Describe names the backend and content version, e.g. "local CASC (ow08 backend), version 0.8.0.0.24919, …".
func (s *Store) Describe() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.h == nil {
		return ""
	}
	return C.GoString(C.content_describe(s.h))
}

func (s *Store) NumPackages() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.h == nil {
		return 0
	}
	return int(C.content_package_count(s.h))
}

func (s *Store) NumResources() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.h == nil {
		return 0
	}
	return int(C.content_resource_count(s.h))
}

// Packages returns every package (a fresh copy per call).
func (s *Store) Packages() []Package {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.h == nil {
		return nil
	}
	out := make([]Package, int(C.content_package_count(s.h)))
	for i := range out {
		var p C.content_package
		C.content_package_at(s.h, C.size_t(i), &p)
		out[i] = Package{uint64(p.guid), uint64(p.key_name), uint32(p.size), uint32(p.record_count), ckey(&p.ckey),
			C.GoString(C.content_package_manifest(s.h, C.size_t(i))), uint64(p.asset)}
	}
	return out
}

// Resources returns every resource, sorted by GUID (a fresh copy per call: ~150k entries, cache it).
func (s *Store) Resources() []Resource {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.h == nil {
		return nil
	}
	n := C.content_resource_count(s.h)
	if n == 0 {
		return nil
	}
	buf := make([]C.content_resource, n)
	n = C.content_resources(s.h, 0, &buf[0], n)
	return convert(buf[:n])
}

// ResourcesOfType returns the resources of one type (e.g. TypeStatescriptGraph), sorted by GUID.
func (s *Store) ResourcesOfType(typ uint32) []Resource {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.h == nil {
		return nil
	}
	n := C.content_resources_of_type(s.h, C.uint32_t(typ), nil, 0)
	if n == 0 {
		return nil
	}
	buf := make([]C.content_resource, n)
	n = min(n, C.content_resources_of_type(s.h, C.uint32_t(typ), &buf[0], n))
	return convert(buf[:n])
}

// Find looks a resource up by GUID.
func (s *Store) Find(guid uint64) (Resource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.h == nil {
		return Resource{}, false
	}
	var r C.content_resource
	if C.content_find(s.h, C.uint64_t(guid), &r) == 0 {
		return Resource{}, false
	}
	return resource(&r), true
}

// PackageRecord is a resource listed by a package.
type PackageRecord struct {
	GUID  uint64
	Flags uint32 // record flags (docs/ContentAccess.md §4)
}

// packageIndices returns the indices of the packages with GUID pkg (locked by the caller).
func (s *Store) packageIndices(pkg uint64) []int {
	var out []int
	for i, n := 0, int(C.content_package_count(s.h)); i < n; i++ {
		var p C.content_package
		if C.content_package_at(s.h, C.size_t(i), &p) != 0 && uint64(p.guid) == pkg {
			out = append(out, i)
		}
	}
	return out
}

// PackageResources returns the records of package pkg in package order (nil for an unknown package).
// A resource can be listed by several packages: see ResourcePackages.
func (s *Store) PackageResources(pkg uint64) []PackageRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.h == nil {
		return nil
	}
	var out []PackageRecord
	for _, i := range s.packageIndices(pkg) {
		n := C.content_package_resources(s.h, C.size_t(i), nil, nil, 0)
		if n == 0 {
			continue
		}
		guids := make([]C.uint64_t, n)
		flags := make([]C.uint32_t, n)
		n = min(n, C.content_package_resources(s.h, C.size_t(i), &guids[0], &flags[0], n))
		for k := range int(n) {
			out = append(out, PackageRecord{uint64(guids[k]), uint32(flags[k])})
		}
	}
	return out
}

// ResourcePackages returns the GUIDs of every package listing guid, in manifest order (the first one is
// Resource.Package). Scans all records (~1.4M, about 13 ms).
func (s *Store) ResourcePackages(guid uint64) []uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.h == nil {
		return nil
	}
	n := C.content_resource_packages(s.h, C.uint64_t(guid), nil, 0)
	if n == 0 {
		return nil
	}
	idx := make([]C.uint32_t, n)
	n = min(n, C.content_resource_packages(s.h, C.uint64_t(guid), &idx[0], n))
	out := make([]uint64, 0, n)
	for _, i := range idx[:n] {
		var p C.content_package
		if C.content_package_at(s.h, C.size_t(i), &p) != 0 && !slices.Contains(out, uint64(p.guid)) {
			out = append(out, uint64(p.guid))
		}
	}
	return out
}

// Read returns a resource's decoded bytes. An unknown GUID gives an error wrapping ErrNotFound.
func (s *Store) Read(guid uint64) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.h == nil {
		return nil, ErrClosed
	}
	var data *C.uint8_t
	var size C.size_t
	var err [errSize]C.char
	if C.content_read(s.h, C.uint64_t(guid), &data, &size, &err[0], errSize) == 0 {
		var r C.content_resource
		if C.content_find(s.h, C.uint64_t(guid), &r) == 0 {
			return nil, fmt.Errorf("%w: %016X", ErrNotFound, guid)
		}
		return nil, fmt.Errorf("content: read %016X: %s", guid, C.GoString(&err[0]))
	}
	return take(data, size), nil
}

// ReadCKey returns the decoded bytes of any file in the container by content key (manifests, packages…).
func (s *Store) ReadCKey(k CKey) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.h == nil {
		return nil, ErrClosed
	}
	ck := (*C.uint8_t)(C.CBytes(k[:]))
	defer C.free(unsafe.Pointer(ck))
	var data *C.uint8_t
	var size C.size_t
	var err [errSize]C.char
	if C.content_read_ckey(s.h, ck, &data, &size, &err[0], errSize) == 0 {
		return nil, fmt.Errorf("content: read ckey %s: %s", k, C.GoString(&err[0]))
	}
	return take(data, size), nil
}

func take(data *C.uint8_t, size C.size_t) []byte {
	defer C.content_free(unsafe.Pointer(data))
	return C.GoBytes(unsafe.Pointer(data), C.int(size))
}

func ckey(p *[16]C.uint8_t) CKey {
	var k CKey
	for i := range k {
		k[i] = byte(p[i])
	}
	return k
}

func resource(r *C.content_resource) Resource {
	return Resource{uint64(r.guid), uint64(r._package), uint32(r._type), uint32(r.size), uint32(r.flags), ckey(&r.ckey)}
}

func convert(buf []C.content_resource) []Resource {
	out := make([]Resource, len(buf))
	for i := range buf {
		out[i] = resource(&buf[i])
	}
	return out
}

func cbool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
