/* C API over content::Store, for cgo (server/) and ctypes. Opaque handle, POD structs, no exceptions
 * across the boundary. Functions returning int: 1 = ok, 0 = failure (message in err, if given). */
#ifndef OW_CONTENT_CAPI_H
#define OW_CONTENT_CAPI_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct content_store content_store;

typedef struct content_resource {
    uint64_t guid;
    uint64_t package; /* first package listing the resource; 0 for packages themselves */
    uint32_t type;    /* bitreverse12((guid >> 48) & 0xFFF) + 1, e.g. 0x1B = Statescript graph */
    uint32_t size;
    uint32_t flags;
    uint8_t ckey[16];
} content_resource;

typedef struct content_package {
    uint64_t guid;
    uint64_t key_name;
    uint32_t size;
    uint32_t record_count;
    uint8_t ckey[16];
    uint64_t asset; /* the asset the package belongs to (APM item +0x08) */
} content_package;

/* Open a local Overwatch beta 0.8 install (the directory with .build.info). region/locale may be NULL
 * (defaults "RDEV" / "enUS"). Returns NULL on failure. Reads are thread-safe. */
content_store* content_open_ow08(const char* install_dir, const char* region, const char* locale, char* err,
                                 size_t err_size);

/* Options for content_open_ow08_ex; content_options_init sets the defaults (same as content_open_ow08). */
typedef struct content_key {
    uint64_t name; /* TACT key name */
    uint8_t key[16];
} content_key;

typedef struct content_options {
    const char* region;       /* NULL = "RDEV" */
    const char* locale;       /* NULL = "enUS" */
    int speech;               /* also load the speech manifest (default 1) */
    int verify_md5;           /* check MD5(decoded) == CKey on every read (default 0) */
    int builtin_keys;         /* start from the built-in 0.8 keyring (default 1) */
    const content_key* extra_keys; /* added to the keyring (copied at open) */
    size_t extra_key_count;
} content_options;

void content_options_init(content_options* opt);
/* opt may be NULL (defaults). Returns NULL on failure. */
content_store* content_open_ow08_ex(const char* install_dir, const content_options* opt, char* err, size_t err_size);
void content_close(content_store* s);

/* Resource type of a GUID: bitreverse12((guid >> 48) & 0xFFF) + 1. */
uint32_t content_guid_type(uint64_t guid);

/* Backend and content version; valid until content_close. */
const char* content_describe(const content_store* s);

size_t content_package_count(const content_store* s);
int content_package_at(const content_store* s, size_t index, content_package* out);
/* Manifest the package came from (e.g. "Win_SPWin_RDEV_LenUS_EExt"); valid until content_close, "" if out
 * of range. */
const char* content_package_manifest(const content_store* s, size_t index);
/* Resources sorted by GUID. */
size_t content_resource_count(const content_store* s);
int content_resource_at(const content_store* s, size_t index, content_resource* out);
int content_find(const content_store* s, uint64_t guid, content_resource* out);
/* Bulk copy: resources [start, start + n) into out (clamped to the count). Returns the number copied. */
size_t content_resources(const content_store* s, size_t start, content_resource* out, size_t n);
/* Resources of one type, sorted by GUID: copies up to cap into out (out may be NULL when cap is 0) and
 * returns how many there are in total (call with cap 0 first to size the buffer). */
size_t content_resources_of_type(const content_store* s, uint32_t type, content_resource* out, size_t cap);

/* Package membership (a resource can be listed by several packages).
 * Records of package pkg_index (content_package_at order), in package order: copies up to cap GUIDs into
 * guids and their record flags into flags (either may be NULL) and returns the record count (call with
 * cap 0 first to size the buffers; 0 if pkg_index is out of range). */
size_t content_package_resources(const content_store* s, size_t pkg_index, uint64_t* guids, uint32_t* flags,
                                 size_t cap);
/* Indices (content_package_at order, ascending) of every package listing guid as a record: copies up to cap
 * into out (may be NULL when cap is 0) and returns the total. Scans all records (~1.4M, about 13 ms). */
size_t content_resource_packages(const content_store* s, uint64_t guid, uint32_t* out, size_t cap);

/* The bytes are malloc'd; release with content_free. */
int content_read(const content_store* s, uint64_t guid, uint8_t** data, size_t* size, char* err, size_t err_size);
int content_read_ckey(const content_store* s, const uint8_t ckey[16], uint8_t** data, size_t* size, char* err,
                      size_t err_size);
void content_free(void* p);

#ifdef __cplusplus
}
#endif

#endif
