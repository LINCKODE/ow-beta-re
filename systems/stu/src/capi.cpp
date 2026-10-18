// C API over the reader (capi/stu.h). stu_value is never defined: the pointers are stu::Value nodes of
// the trees the document caches.
#include "stu.h"

#include <cstdlib>
#include <cstring>
#include <memory>
#include <new>

#include "stu/reader.h"

using stu::Value;

struct stu_doc {
    std::vector<uint8_t> bytes;
    stu::Document doc;
    std::vector<std::unique_ptr<Value>> decoded;  // per instance, filled on first access
};

static_assert(int(Value::Type::Null) == STU_VALUE_NULL && int(Value::Type::Primitive) == STU_VALUE_PRIMITIVE &&
              int(Value::Type::String) == STU_VALUE_STRING && int(Value::Type::Enum) == STU_VALUE_ENUM &&
              int(Value::Type::Object) == STU_VALUE_OBJECT && int(Value::Type::Inline) == STU_VALUE_INLINE &&
              int(Value::Type::List) == STU_VALUE_LIST && int(Value::Type::Map) == STU_VALUE_MAP &&
              int(Value::Type::Ref) == STU_VALUE_REF && int(Value::Type::Error) == STU_VALUE_ERROR);
static_assert(int(stu::Kind::Map) == STU_KIND_MAP && int(stu::Kind::ResourceRefList) == STU_KIND_RESOURCE_REF_LIST);
static_assert(stu::prim::teString == STU_PRIM_STRING && stu::prim::teMtx43A == STU_PRIM_MTX43A &&
              stu::prim::s8 == STU_PRIM_S8 && stu::prim::teEntityID == STU_PRIM_ENTITY_ID);

namespace {

const Value* V(const stu_value* v) { return reinterpret_cast<const Value*>(v); }
const stu_value* H(const Value* v) { return reinterpret_cast<const stu_value*>(v); }

void SetError(char* err, size_t cap, const std::string& msg) {
    if (!err || cap == 0) return;
    size_t n = msg.size() < cap - 1 ? msg.size() : cap - 1;
    std::memcpy(err, msg.data(), n);
    err[n] = 0;
}

char* Dup(const std::string& s) {
    char* p = static_cast<char*>(std::malloc(s.size() + 1));
    if (p) std::memcpy(p, s.c_str(), s.size() + 1);
    return p;
}

int FloatCount(uint32_t prim) {
    switch (prim) {
        case STU_PRIM_VEC2: return 2;
        case STU_PRIM_VEC3:
        case STU_PRIM_VEC3A:
        case STU_PRIM_COLOR_RGB: return 3;
        case STU_PRIM_VEC4:
        case STU_PRIM_QUAT:
        case STU_PRIM_COLOR_RGBA: return 4;
        case STU_PRIM_MTX43A: return 16;
        default: return 0;
    }
}

int CountErrors(const Value& v) {
    int n = v.type == Value::Type::Error;
    for (const auto& c : v.items) n += CountErrors(c);
    return n;
}

}  // namespace

extern "C" {

int stu_doc_open(const uint8_t* bytes, size_t len, int mode, uint64_t base, stu_doc** out, char* err,
                 size_t err_cap) {
    if (!out) return STU_ERR_ARG;
    *out = nullptr;
    if ((!bytes && len) || (mode != STU_MODE_FILE && mode != STU_MODE_LOADED)) {
        SetError(err, err_cap, "bad argument");
        return STU_ERR_ARG;
    }
    auto* d = new (std::nothrow) stu_doc;
    if (!d) return STU_ERR_ARG;
    try {
        d->bytes.assign(bytes, bytes + len);
        std::string e;
        if (!stu::Document::Parse(d->bytes, stu::Registry::Client(), d->doc, e,
                                  mode == STU_MODE_LOADED ? stu::Mode::Loaded : stu::Mode::File, base)) {
            SetError(err, err_cap, e);
            delete d;
            return STU_ERR_PARSE;
        }
        d->decoded.resize(d->doc.instances().size());
    } catch (const std::exception& ex) {
        SetError(err, err_cap, ex.what());
        delete d;
        return STU_ERR_PARSE;
    }
    SetError(err, err_cap, "");
    *out = d;
    return STU_OK;
}

int stu_doc_open_file(const char* path, int mode, uint64_t base, stu_doc** out, char* err, size_t err_cap) {
    if (!out) return STU_ERR_ARG;
    *out = nullptr;
    if (!path) return STU_ERR_ARG;
    std::vector<uint8_t> bytes;
    try {
        bytes = stu::ReadFile(path);
    } catch (const std::exception&) {
    }
    if (bytes.empty()) {
        SetError(err, err_cap, std::string("cannot read ") + path);
        return STU_ERR_NOT_FOUND;
    }
    return stu_doc_open(bytes.data(), bytes.size(), mode, base, out, err, err_cap);
}

void stu_doc_close(stu_doc* doc) { delete doc; }

int stu_doc_mode(const stu_doc* doc) {
    if (!doc) return STU_ERR_ARG;
    return doc->doc.mode() == stu::Mode::Loaded ? STU_MODE_LOADED : STU_MODE_FILE;
}

size_t stu_doc_size(const stu_doc* doc) { return doc ? doc->doc.size() : 0; }

int stu_doc_instance_count(const stu_doc* doc) {
    return doc ? static_cast<int>(doc->doc.instances().size()) : 0;
}

int stu_doc_all_crc_ok(const stu_doc* doc) { return doc && doc->doc.AllCrcOk(); }

int stu_doc_root(const stu_doc* doc) { return doc ? doc->doc.RootInstance() : -1; }

int stu_doc_instance_at(const stu_doc* doc, uint64_t offset) { return doc ? doc->doc.InstanceAt(offset) : -1; }

int stu_doc_instance(const stu_doc* doc, int index, stu_instance_info* out) {
    if (!doc || !out) return STU_ERR_ARG;
    const auto& ins = doc->doc.instances();
    if (index < 0 || static_cast<size_t>(index) >= ins.size()) return STU_ERR_NOT_FOUND;
    const auto& in = ins[index];
    out->offset = in.offset;
    out->flags = in.flags;
    out->layout_crc = in.layout_crc;
    out->type_hash = in.type ? in.type->hash : 0;
    out->crc_ok = in.crc_ok;
    return STU_OK;
}

const stu_value* stu_doc_decode(stu_doc* doc, int index) {
    if (!doc || index < 0 || static_cast<size_t>(index) >= doc->decoded.size()) return nullptr;
    auto& slot = doc->decoded[index];
    if (!slot) {
        try {
            slot = std::make_unique<Value>(doc->doc.Decode(index));
        } catch (const std::exception&) {
            return nullptr;
        }
    }
    return H(slot.get());
}

// --- values ---------------------------------------------------------------------------------

int stu_value_type(const stu_value* v) { return v ? static_cast<int>(V(v)->type) : -1; }

int stu_value_get_info(const stu_value* v, stu_value_info* out) {
    if (!v || !out) return STU_ERR_ARG;
    const Value& x = *V(v);
    out->type = static_cast<int32_t>(x.type);
    out->kind = static_cast<int32_t>(x.kind);
    out->field_hash = x.field_hash;
    out->type_hash = x.type_hash;
    out->map_key = x.map_key;
    out->flags = x.flags;
    out->count = static_cast<int32_t>(x.items.size());
    out->instance = x.type == Value::Type::Object ? x.instance : -1;
    out->guid = x.guid;
    out->handle = x.handle;
    out->raw_size = static_cast<uint32_t>(x.raw.size());
    return STU_OK;
}

int stu_value_count(const stu_value* v) { return v ? static_cast<int>(V(v)->items.size()) : 0; }

const stu_value* stu_value_item(const stu_value* v, int i) {
    if (!v || i < 0 || static_cast<size_t>(i) >= V(v)->items.size()) return nullptr;
    return H(&V(v)->items[i]);
}

const stu_value* stu_value_field(const stu_value* v, uint32_t field_hash) {
    if (!v || V(v)->type != Value::Type::Inline) return nullptr;
    return H(V(v)->Find(field_hash));
}

const stu_value* stu_value_map_get(const stu_value* v, uint32_t key) {
    if (!v || V(v)->type != Value::Type::Map) return nullptr;
    for (const auto& e : V(v)->items)
        if (e.map_key == key) return H(&e);
    return nullptr;
}

int stu_value_int(const stu_value* v, int64_t* out) {
    if (!v || !out) return STU_ERR_ARG;
    const Value& x = *V(v);
    if (x.type == Value::Type::Enum) {
        *out = x.AsU32();
        return STU_OK;
    }
    if (x.type != Value::Type::Primitive) return STU_ERR_TYPE;
    switch (x.type_hash) {
        case STU_PRIM_U8: *out = x.As<uint8_t>(); break;
        case STU_PRIM_S8: *out = x.As<int8_t>(); break;
        case STU_PRIM_U16: *out = x.As<uint16_t>(); break;
        case STU_PRIM_S16: *out = x.As<int16_t>(); break;
        case STU_PRIM_U32:
        case STU_PRIM_ENTITY_ID: *out = x.As<uint32_t>(); break;
        case STU_PRIM_S32: *out = x.As<int32_t>(); break;
        case STU_PRIM_U64: *out = static_cast<int64_t>(x.As<uint64_t>()); break;
        case STU_PRIM_S64: *out = x.As<int64_t>(); break;
        default: return STU_ERR_TYPE;
    }
    return STU_OK;
}

int stu_value_float(const stu_value* v, double* out) {
    if (!v || !out) return STU_ERR_ARG;
    const Value& x = *V(v);
    if (x.type != Value::Type::Primitive) return STU_ERR_TYPE;
    if (x.type_hash == STU_PRIM_F32)
        *out = x.AsF32();
    else if (x.type_hash == STU_PRIM_F64)
        *out = x.AsF64();
    else
        return STU_ERR_TYPE;
    return STU_OK;
}

int stu_value_floats(const stu_value* v, float* out, int cap, int* count) {
    if (!v || (!out && cap > 0)) return STU_ERR_ARG;
    const Value& x = *V(v);
    int n = x.type == Value::Type::Primitive ? FloatCount(x.type_hash) : 0;
    if (n == 0 || x.raw.size() < 4u * n) return STU_ERR_TYPE;
    if (count) *count = n;
    for (int i = 0; i < n && i < cap; i++) std::memcpy(&out[i], x.raw.data() + 4 * i, 4);
    return STU_OK;
}

int stu_value_raw(const stu_value* v, const uint8_t** data, size_t* len) {
    if (!v || !data || !len) return STU_ERR_ARG;
    const Value& x = *V(v);
    if (x.type != Value::Type::Primitive && x.type != Value::Type::Enum) return STU_ERR_TYPE;
    *data = x.raw.data();
    *len = x.raw.size();
    return STU_OK;
}

int stu_value_string(const stu_value* v, const char** data, size_t* len) {
    if (!v || !data || !len) return STU_ERR_ARG;
    const Value& x = *V(v);
    if (x.type != Value::Type::String) return STU_ERR_TYPE;
    *data = x.str.c_str();
    *len = x.str.size();
    return STU_OK;
}

int stu_value_ref(const stu_value* v, uint64_t* guid, uint64_t* handle) {
    if (!v || !guid) return STU_ERR_ARG;
    const Value& x = *V(v);
    if (x.type != Value::Type::Ref) return STU_ERR_TYPE;
    *guid = x.guid;
    if (handle) *handle = x.handle;
    return STU_OK;
}

int stu_value_object(const stu_value* v, int* instance) {
    if (!v || !instance) return STU_ERR_ARG;
    const Value& x = *V(v);
    if (x.type == Value::Type::Null) return STU_ERR_NOT_FOUND;
    if (x.type != Value::Type::Object) return STU_ERR_TYPE;
    *instance = x.instance;
    return STU_OK;
}

const char* stu_value_error(const stu_value* v) {
    return v && V(v)->type == Value::Type::Error ? V(v)->str.c_str() : nullptr;
}

int stu_value_error_count(const stu_value* v) { return v ? CountErrors(*V(v)) : 0; }

// --- JSON -----------------------------------------------------------------------------------

char* stu_value_json(const stu_value* v, int with_handles) {
    if (!v) return nullptr;
    try {
        return Dup(stu::ToJson(*V(v), stu::Registry::Client(), with_handles != 0));
    } catch (const std::exception&) {
        return nullptr;
    }
}

char* stu_doc_json(stu_doc* doc, int with_handles) {
    if (!doc) return nullptr;
    try {
        return Dup(stu::DocumentJson(doc->doc, with_handles != 0));
    } catch (const std::exception&) {
        return nullptr;
    }
}

void stu_free(void* p) { std::free(p); }

// --- registry -------------------------------------------------------------------------------

const char* stu_name(uint32_t hash) { return stu::Registry::Client().Name(hash); }

int stu_type(uint32_t type_hash, stu_type_info* out) {
    if (!out) return STU_ERR_ARG;
    const auto& reg = stu::Registry::Client();
    const stu::TypeInfo* t = reg.Find(type_hash);
    if (!t) return STU_ERR_NOT_FOUND;
    out->hash = t->hash;
    out->parent = t->parent;
    out->size = t->size;
    out->field_count = static_cast<uint32_t>(reg.AllFields(*t).size());
    out->layout_crc = t->layout_crc;
    return STU_OK;
}

int stu_type_field(uint32_t type_hash, int i, stu_field_info* out) {
    if (!out) return STU_ERR_ARG;
    const auto& reg = stu::Registry::Client();
    const stu::TypeInfo* t = reg.Find(type_hash);
    if (!t) return STU_ERR_NOT_FOUND;
    auto fields = reg.AllFields(*t);
    if (i < 0 || static_cast<size_t>(i) >= fields.size()) return STU_ERR_NOT_FOUND;
    const stu::FieldInfo& f = *fields[i];
    out->hash = f.hash;
    out->offset = f.offset;
    out->kind = static_cast<int32_t>(f.kind);
    out->elem = f.elem;
    out->flags = f.flags;
    return STU_OK;
}

int stu_type_is_a(uint32_t type_hash, uint32_t ancestor_hash) {
    return stu::Registry::Client().IsA(type_hash, ancestor_hash);
}

int stu_type_count(void) { return static_cast<int>(stu::Registry::Client().TypeCount()); }

const char* stu_kind_name(int kind) { return stu::KindName(static_cast<stu::Kind>(kind)); }

const char* stu_value_type_name(int type) {
    static const char* const names[] = {"null", "primitive", "string", "enum", "object",
                                        "inline", "list", "map", "ref", "error"};
    return type >= 0 && type < 10 ? names[type] : "?";
}

}  // extern "C"
