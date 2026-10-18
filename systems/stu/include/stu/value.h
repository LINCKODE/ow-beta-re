// Generic decoded STU value tree.
#pragma once
#include <cstdint>
#include <cstring>
#include <string>
#include <vector>

#include "stu/registry.h"

namespace stu {

struct Value {
    enum class Type : uint8_t {
        Null,       // null object pointer / empty descriptor
        Primitive,  // raw bytes of a primitive (type_hash = primitive hash)
        String,     // teString (str)
        Enum,       // u32 (raw)
        Object,     // pointer to another instance (instance index)
        Inline,     // inline object: items = its fields (type_hash = the type)
        List,       // items = elements
        Map,        // items = entries (map_key + the value)
        Ref,        // file / resource reference (guid, handle)
        Error,      // could not decode (error)
    };

    Type type = Type::Null;
    Kind kind = Kind::Primitive;  // constraint kind of the field this value came from
    uint32_t field_hash = 0;      // set on fields of an Inline / instance
    uint32_t type_hash = 0;       // primitive / enum element / STU type of Inline, Object, Ref target
    uint32_t map_key = 0;         // Map entries
    uint32_t flags = 0;           // field flags (registry)

    std::vector<uint8_t> raw;  // Primitive / Enum bytes
    std::string str;           // String / Error
    int instance = -1;         // Object: instance index in the document
    uint64_t guid = 0;         // Ref
    uint64_t handle = 0;       // Ref (file: 0xFFFFFFFFFFFFFFFF; loaded: the resource handle)
    std::vector<Value> items;  // Inline fields / List elements / Map entries

    // Field lookup (Inline / decoded instance).
    const Value* Find(uint32_t field_hash) const {
        for (auto& v : items)
            if (v.field_hash == field_hash) return &v;
        return nullptr;
    }

    template <typename T>
    T As() const {
        T out{};
        if (raw.size() >= sizeof(T)) std::memcpy(&out, raw.data(), sizeof(T));
        return out;
    }
    uint32_t AsU32() const { return As<uint32_t>(); }
    int32_t AsI32() const { return As<int32_t>(); }
    uint64_t AsU64() const { return As<uint64_t>(); }
    float AsF32() const { return As<float>(); }
    double AsF64() const { return As<double>(); }
    uint8_t AsU8() const { return As<uint8_t>(); }
};

// JSON text of a value tree (names resolved through the registry). For debugging and tests.
// `with_handles`: print Ref handles (they differ between a file and a loaded image).
std::string ToJson(const Value& v, const Registry& reg, bool with_handles = false, int indent = 0);

}  // namespace stu
