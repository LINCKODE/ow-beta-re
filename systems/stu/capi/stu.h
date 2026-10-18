/* C API over the STUD v3 reader (systems/stu), for cgo (server/, systems/stu/go) and the Statescript
 * runtime. Opaque handles, POD structs, return codes; no exceptions or STL across the boundary.
 * Format and library: docs/STU.md §1, §7.
 *
 * Lifetimes: a document copies the bytes it is opened with. Instances are decoded on first access and
 * cached in the document; every stu_value pointer and string it hands out stays valid until
 * stu_doc_close. A document is not thread-safe (decoding fills the cache); the registry functions are. */
#ifndef OW_STU_CAPI_H
#define OW_STU_CAPI_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct stu_doc stu_doc;
typedef struct stu_value stu_value; /* a node of a decoded tree, owned by its document */

/* Return codes (0 = ok). */
enum {
    STU_OK = 0,
    STU_ERR_ARG = -1,       /* NULL handle / bad argument */
    STU_ERR_PARSE = -2,     /* malformed header or instance table */
    STU_ERR_NOT_FOUND = -3, /* no such instance / field / item / key / type */
    STU_ERR_TYPE = -4,      /* the value is not of the requested kind */
};

/* How pointers in the image are encoded (stu::Mode). */
enum {
    STU_MODE_FILE = 0,   /* raw STUD file (CASC bytes): offsets from the start of the file */
    STU_MODE_LOADED = 1, /* image dumped from the client's memory: absolute pointers, base = its address */
};

/* Decoded value types (stu::Value::Type). */
enum {
    STU_VALUE_NULL = 0,      /* null object pointer / empty descriptor */
    STU_VALUE_PRIMITIVE = 1, /* raw bytes of a primitive; type_hash = primitive hash (STU_PRIM_*) */
    STU_VALUE_STRING = 2,    /* teString */
    STU_VALUE_ENUM = 3,      /* u32; type_hash = the enum */
    STU_VALUE_OBJECT = 4,    /* pointer to another instance of the document (instance index) */
    STU_VALUE_INLINE = 5,    /* inline object or decoded instance: items = its fields, parents first */
    STU_VALUE_LIST = 6,      /* items = elements */
    STU_VALUE_MAP = 7,       /* items = entries (map_key + an Object/Null value) */
    STU_VALUE_REF = 8,       /* file / resource reference (guid, handle) */
    STU_VALUE_ERROR = 9,     /* could not decode (stu_value_error) */
};

/* Field constraint kinds (stu::Kind, registry "ctype"). */
enum {
    STU_KIND_PRIMITIVE = 0,
    STU_KIND_PRIMITIVE_LIST = 1,
    STU_KIND_OBJECT = 2,
    STU_KIND_OBJECT_LIST = 3,
    STU_KIND_INLINE = 4,
    STU_KIND_INLINE_LIST = 5,
    STU_KIND_MAP = 7,
    STU_KIND_ENUM = 8,
    STU_KIND_ENUM_LIST = 9,
    STU_KIND_FILE_REF = 10,
    STU_KIND_FILE_REF_LIST = 11,
    STU_KIND_RESOURCE_REF = 12,
    STU_KIND_RESOURCE_REF_LIST = 13,
};

/* Primitive element hashes (CRC32 of the type name; stu::prim). */
#define STU_PRIM_U8 0x3b9327d2u
#define STU_PRIM_S8 0x6dc98054u
#define STU_PRIM_U16 0xa49ce182u
#define STU_PRIM_S16 0xa0119d30u
#define STU_PRIM_U32 0x91c74719u
#define STU_PRIM_S32 0x954a3babu
#define STU_PRIM_U64 0x05d31669u
#define STU_PRIM_S64 0x015e6adbu
#define STU_PRIM_F32 0x8fa75a30u
#define STU_PRIM_F64 0x1bb30b40u
#define STU_PRIM_ENTITY_ID 0x0c525b48u
#define STU_PRIM_VEC2 0xf6ec8926u
#define STU_PRIM_VEC3 0x81ebb9b0u
#define STU_PRIM_VEC3A 0x2357e676u
#define STU_PRIM_VEC4 0x1f8f2c13u
#define STU_PRIM_QUAT 0xda9494c8u
#define STU_PRIM_COLOR_RGB 0x525114abu
#define STU_PRIM_COLOR_RGBA 0xa9e19537u
#define STU_PRIM_DATE_TIME 0xe1b9e763u
#define STU_PRIM_DBID 0x20b3d5a9u
#define STU_PRIM_UUID 0x809899f3u
#define STU_PRIM_STRING 0xbbca304bu
#define STU_PRIM_MTX43A 0x6fb9880bu

typedef struct stu_instance_info {
    uint32_t offset;     /* from the start of the image */
    uint32_t flags;      /* bit 0: has references to other resources */
    uint64_t layout_crc; /* from the instance table */
    uint32_t type_hash;  /* 0 if the type is unknown */
    int32_t crc_ok;      /* type known and its layout CRC matches the client's registry */
} stu_instance_info;

typedef struct stu_value_info {
    int32_t type;        /* STU_VALUE_* */
    int32_t kind;        /* STU_KIND_* of the field this value came from */
    uint32_t field_hash; /* fields of an Inline value; 0 otherwise */
    uint32_t type_hash;  /* primitive / enum / STU type of an Inline, Object, Ref target, list element */
    uint32_t map_key;    /* map entries */
    uint32_t flags;      /* field flags (registry): 4 load dependency, 2 common, 1 non-owning */
    int32_t count;       /* items: fields (Inline), elements (List), entries (Map); 0 otherwise */
    int32_t instance;    /* Object: instance index; -1 otherwise */
    uint64_t guid;       /* Ref */
    uint64_t handle;     /* Ref (file: 0xFFFFFFFFFFFFFFFF; loaded: the client's resource handle) */
    uint32_t raw_size;   /* Primitive / Enum: byte size */
} stu_value_info;

typedef struct stu_type_info {
    uint32_t hash;
    uint32_t parent; /* 0 = none */
    uint32_t size;   /* instance size */
    uint32_t field_count; /* all fields, parents first */
    uint64_t layout_crc;
} stu_type_info;

typedef struct stu_field_info {
    uint32_t hash;
    uint32_t offset;
    int32_t kind; /* STU_KIND_* */
    uint32_t elem;
    uint32_t flags;
} stu_field_info;

/* --- documents ----------------------------------------------------------------------------- */

/* Parses the header and instance table (copies `bytes`). mode: STU_MODE_*; base: address of a loaded
 * image (ignored for files). Instances with an unknown type or a layout-CRC mismatch are kept
 * (stu_doc_all_crc_ok tells whether the client would load the file). On failure returns STU_ERR_*,
 * *out = NULL and writes the reason to err (if err_cap > 0, NUL-terminated). */
int stu_doc_open(const uint8_t* bytes, size_t len, int mode, uint64_t base, stu_doc** out, char* err,
                 size_t err_cap);
/* Reads a file and opens it. */
int stu_doc_open_file(const char* path, int mode, uint64_t base, stu_doc** out, char* err, size_t err_cap);
void stu_doc_close(stu_doc* doc);

int stu_doc_mode(const stu_doc* doc);
size_t stu_doc_size(const stu_doc* doc);
int stu_doc_instance_count(const stu_doc* doc);
/* 1 if every instance's type is known and its layout CRC matches (and there is at least one). */
int stu_doc_all_crc_ok(const stu_doc* doc);
/* Root instance index (0), -1 if the document is empty. */
int stu_doc_root(const stu_doc* doc);
/* Instance index starting at `offset`, -1 if none. */
int stu_doc_instance_at(const stu_doc* doc, uint64_t offset);
int stu_doc_instance(const stu_doc* doc, int index, stu_instance_info* out);

/* Decoded instance (an Inline value, or an Error value if its type is unknown); decoded once and
 * cached. NULL if the index is out of range. */
const stu_value* stu_doc_decode(stu_doc* doc, int index);

/* --- values -------------------------------------------------------------------------------- */

int stu_value_type(const stu_value* v); /* STU_VALUE_*, -1 for NULL */
int stu_value_get_info(const stu_value* v, stu_value_info* out);
/* Items of an Inline / List / Map value (0 for other types). */
int stu_value_count(const stu_value* v);
/* i-th field (Inline, registry order), element (List) or entry (Map); NULL if out of range. */
const stu_value* stu_value_item(const stu_value* v, int i);
/* Field of an Inline value by field hash; NULL if absent. */
const stu_value* stu_value_field(const stu_value* v, uint32_t field_hash);
/* Map entry by key; NULL if absent. */
const stu_value* stu_value_map_get(const stu_value* v, uint32_t key);

/* Typed getters: STU_OK, STU_ERR_ARG or STU_ERR_TYPE. */
/* Integer primitives (u8..s64, teEntityID) and enums; signed types are sign-extended, u64 is returned
 * as its bit pattern. */
int stu_value_int(const stu_value* v, int64_t* out);
/* f32 / f64. */
int stu_value_float(const stu_value* v, double* out);
/* Float-vector primitives (teVec2/3/3A/4, teQuat, teColorRGB(A), teMtx43A): writes up to cap floats,
 * *count = the element's float count. */
int stu_value_floats(const stu_value* v, float* out, int cap, int* count);
/* Raw bytes of a Primitive / Enum (pointer into the tree). */
int stu_value_raw(const stu_value* v, const uint8_t** data, size_t* len);
/* teString: not NUL-safe (use len); *data is never NULL on STU_OK. */
int stu_value_string(const stu_value* v, const char** data, size_t* len);
/* Ref: guid (and handle, may be NULL). */
int stu_value_ref(const stu_value* v, uint64_t* guid, uint64_t* handle);
/* Object: the pointed-to instance index. A Null value gives STU_ERR_NOT_FOUND. */
int stu_value_object(const stu_value* v, int* instance);
/* Error message of an Error value; NULL otherwise. */
const char* stu_value_error(const stu_value* v);
/* Number of Error values in the subtree. */
int stu_value_error_count(const stu_value* v);

/* --- JSON (stu::ToJson; names resolved through the registry) ------------------------------- */

/* JSON of a value tree; malloc'd, free with stu_free. NULL for a NULL value. */
char* stu_value_json(const stu_value* v, int with_handles);
/* The whole document in stu_dump's format: {"instances", "crc_mismatch", "root": [...]}. */
char* stu_doc_json(stu_doc* doc, int with_handles);
void stu_free(void* p);

/* --- registry (the client's, build 24919) -------------------------------------------------- */

/* Name of a type / field / enum hash, NULL if unknown. Static storage. */
const char* stu_name(uint32_t hash);
int stu_type(uint32_t type_hash, stu_type_info* out);
/* i-th field of the type, parents first (the order Inline values list them in). */
int stu_type_field(uint32_t type_hash, int i, stu_field_info* out);
int stu_type_is_a(uint32_t type_hash, uint32_t ancestor_hash);
int stu_type_count(void);
const char* stu_kind_name(int kind);
const char* stu_value_type_name(int type);

#ifdef __cplusplus
}
#endif
#endif
