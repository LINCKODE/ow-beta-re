#include "stu/registry.h"

#include <algorithm>
#include <cstdio>

#include "registry_data.h"

namespace stu {

const char* KindName(Kind k) {
    switch (k) {
        case Kind::Primitive: return "primitive";
        case Kind::PrimitiveList: return "primitive_list";
        case Kind::Object: return "object";
        case Kind::ObjectList: return "object_list";
        case Kind::Inline: return "inline";
        case Kind::InlineList: return "inline_list";
        case Kind::Map: return "map";
        case Kind::Enum: return "enum";
        case Kind::EnumList: return "enum_list";
        case Kind::FileRef: return "file_ref";
        case Kind::FileRefList: return "file_ref_list";
        case Kind::ResourceRef: return "resource_ref";
        case Kind::ResourceRefList: return "resource_ref_list";
    }
    return "?";
}

namespace {
// The generated field rows are grouped per type; view them as FieldInfo without copying.
static_assert(sizeof(generated::FieldRow) >= sizeof(FieldInfo));
std::vector<FieldInfo> BuildFields() {
    std::vector<FieldInfo> v;
    v.reserve(generated::kFieldCount);
    for (const auto& r : generated::kFields)
        v.push_back(FieldInfo{r.hash, r.offset, static_cast<Kind>(r.kind), r.elem, r.flags});
    return v;
}
const std::vector<FieldInfo>& Fields() {
    static const std::vector<FieldInfo> f = BuildFields();
    return f;
}
}  // namespace

Registry::Registry() {
    const auto& fields = Fields();
    types_.reserve(generated::kTypeCount);
    for (const auto& r : generated::kTypes) {
        TypeInfo t;
        t.hash = r.hash;
        t.parent = r.parent;
        t.size = r.size;
        t.layout_crc = r.layout_crc;
        t.flag58 = r.flag58;
        t.own_fields = std::span<const FieldInfo>(fields.data() + r.first_field, r.field_count);
        types_.push_back(t);
    }
    std::sort(types_.begin(), types_.end(), [](const TypeInfo& a, const TypeInfo& b) { return a.hash < b.hash; });
}

const Registry& Registry::Client() {
    static const Registry r;
    return r;
}

const TypeInfo* Registry::Find(uint32_t hash) const {
    auto it = std::lower_bound(types_.begin(), types_.end(), hash,
                               [](const TypeInfo& t, uint32_t h) { return t.hash < h; });
    return (it != types_.end() && it->hash == hash) ? &*it : nullptr;
}

const TypeInfo* Registry::FindByLayoutCrc(uint64_t crc) const {
    for (const auto& t : types_)
        if (t.layout_crc == crc) return &t;
    return nullptr;
}

std::vector<const FieldInfo*> Registry::AllFields(const TypeInfo& t) const {
    std::vector<const TypeInfo*> chain;
    for (const TypeInfo* p = &t; p; p = p->parent ? Find(p->parent) : nullptr) {
        if (chain.size() > 64) break;  // corrupt parent chain
        chain.push_back(p);
    }
    std::vector<const FieldInfo*> out;
    for (auto it = chain.rbegin(); it != chain.rend(); ++it)
        for (const auto& f : (*it)->own_fields) out.push_back(&f);
    return out;
}

const FieldInfo* Registry::FindField(const TypeInfo& t, uint32_t field_hash) const {
    for (const TypeInfo* p = &t; p; p = p->parent ? Find(p->parent) : nullptr)
        for (const auto& f : p->own_fields)
            if (f.hash == field_hash) return &f;
    return nullptr;
}

bool Registry::IsA(uint32_t type_hash, uint32_t ancestor) const {
    int guard = 0;
    for (const TypeInfo* p = Find(type_hash); p && guard < 64; p = p->parent ? Find(p->parent) : nullptr, guard++)
        if (p->hash == ancestor) return true;
    return false;
}

const char* Registry::Name(uint32_t hash) const {
    auto it = std::lower_bound(std::begin(generated::kNames), std::end(generated::kNames), hash,
                               [](const generated::NameRow& n, uint32_t h) { return n.hash < h; });
    return (it != std::end(generated::kNames) && it->hash == hash) ? it->name : nullptr;
}

std::string Registry::NameOr(uint32_t hash) const {
    if (const char* n = Name(hash)) return n;
    char buf[16];
    std::snprintf(buf, sizeof buf, "0x%08x", hash);
    return buf;
}

uint32_t Registry::PrimitiveSize(uint32_t elem) const {
    for (const auto& p : generated::kPrimitives)
        if (p.hash == elem) return p.size;
    return 0;
}

}  // namespace stu
