// STU type registry: the client's (build 24919) type/field tables with names from OWLib.
// Data: generated/registry_data.h (from systems/data/client_stu_registry.json). See docs/STU.md §3.
#pragma once
#include <cstdint>
#include <span>
#include <string>
#include <vector>

namespace stu {

// Field constraint kind (registry "ctype"; same numbering as OWLib serializationType and
// Prometheus-Atlas STUConstraintType).
enum class Kind : uint8_t {
    Primitive = 0,
    PrimitiveList = 1,
    Object = 2,
    ObjectList = 3,
    Inline = 4,
    InlineList = 5,
    Map = 7,
    Enum = 8,
    EnumList = 9,
    FileRef = 10,
    FileRefList = 11,
    ResourceRef = 12,
    ResourceRefList = 13,
};

const char* KindName(Kind k);
inline bool IsList(Kind k) {
    return k == Kind::PrimitiveList || k == Kind::ObjectList || k == Kind::InlineList || k == Kind::EnumList ||
           k == Kind::FileRefList || k == Kind::ResourceRefList;
}

struct FieldInfo {
    uint32_t hash;    // CRC32 of the field name
    uint32_t offset;  // byte offset in the object
    Kind kind;
    uint32_t elem;    // element type: primitive hash (Primitive/PrimitiveList), STU type (Object*/Inline*/ResourceRef*), 0 otherwise
    uint32_t flags;   // STUArgumentInfo+0x18: 4 = load dependency, 2 = common on object fields, 1 = non-owning
};

struct TypeInfo {
    uint32_t hash;
    uint32_t parent;  // 0 = none
    uint32_t size;    // instance size
    uint64_t layout_crc;
    uint32_t flag58;
    std::span<const FieldInfo> own_fields;  // fields declared by this type (not the parents')
};

// Primitive element hashes (CRC32 of the type name).
namespace prim {
constexpr uint32_t u8 = 0x3b9327d2, s8 = 0x6dc98054, u16 = 0xa49ce182, s16 = 0xa0119d30, u32 = 0x91c74719,
                   s32 = 0x954a3bab, u64 = 0x05d31669, s64 = 0x015e6adb, f32 = 0x8fa75a30, f64 = 0x1bb30b40,
                   teEntityID = 0x0c525b48, teVec2 = 0xf6ec8926, teVec3 = 0x81ebb9b0, teVec3A = 0x2357e676,
                   teVec4 = 0x1f8f2c13, teQuat = 0xda9494c8, teColorRGB = 0x525114ab, teColorRGBA = 0xa9e19537,
                   teStructuredDataDateAndTime = 0xe1b9e763, DBID = 0x20b3d5a9, teUUID = 0x809899f3,
                   teString = 0xbbca304b, teMtx43A = 0x6fb9880b;
}

class Registry {
public:
    // The client's registry (3639 types), built from the generated tables.
    static const Registry& Client();

    const TypeInfo* Find(uint32_t hash) const;
    const TypeInfo* FindByLayoutCrc(uint64_t crc) const;
    size_t TypeCount() const { return types_.size(); }
    std::span<const TypeInfo> Types() const { return types_; }

    // All fields of a type, parents first (the order the client walks them in).
    std::vector<const FieldInfo*> AllFields(const TypeInfo& t) const;
    const FieldInfo* FindField(const TypeInfo& t, uint32_t field_hash) const;
    bool IsA(uint32_t type_hash, uint32_t ancestor_hash) const;

    // Names from OWLib KnownTypes/KnownFields/KnownEnums (+ hashlibrary.json); nullptr if unknown.
    const char* Name(uint32_t hash) const;
    std::string NameOr(uint32_t hash) const;  // name or "0x%08x"

    // Size of a primitive element (0 if unknown).
    uint32_t PrimitiveSize(uint32_t elem) const;

private:
    Registry();
    std::vector<TypeInfo> types_;  // sorted by hash
};

}  // namespace stu
