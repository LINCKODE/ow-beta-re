#include "stu/reader.h"

#include <cstdio>
#include <cstring>

namespace stu {

namespace {
constexpr uint32_t kMagic = 0x53545544;  // 'STUD'
constexpr uint32_t kVersion = 3;
constexpr uint32_t kNullObject = 0xFFFFFFFF;
constexpr uint32_t kMaxCount = 1u << 24;  // sanity bound on list / instance counts

Value Error(const FieldInfo* f, const char* what, uint64_t at) {
    Value v;
    v.type = Value::Type::Error;
    if (f) {
        v.field_hash = f->hash;
        v.kind = f->kind;
    }
    char buf[96];
    std::snprintf(buf, sizeof buf, "%s at 0x%llx", what, static_cast<unsigned long long>(at));
    v.str = buf;
    return v;
}
}  // namespace

template <typename T>
T Document::Read(uint64_t off) const {
    T v{};
    if (In(off, sizeof(T))) std::memcpy(&v, bytes_.data() + off, sizeof(T));
    return v;
}

std::vector<uint8_t> ReadFile(const std::string& path) {
    std::vector<uint8_t> out;
    FILE* f = std::fopen(path.c_str(), "rb");
    if (!f) return out;
    std::fseek(f, 0, SEEK_END);
    long n = std::ftell(f);
    std::fseek(f, 0, SEEK_SET);
    if (n > 0) {
        out.resize(static_cast<size_t>(n));
        if (std::fread(out.data(), 1, out.size(), f) != out.size()) out.clear();
    }
    std::fclose(f);
    return out;
}

bool Document::Parse(std::span<const uint8_t> bytes, const Registry& reg, Document& out, std::string& error,
                     Mode mode, uint64_t base) {
    out = Document();
    out.bytes_ = bytes;
    out.reg_ = &reg;
    out.mode_ = mode;
    out.base_ = base;
    if (bytes.size() < 0x10) {
        error = "too short";
        return false;
    }
    if (out.Read<uint32_t>(0) != kMagic) {
        error = "bad magic";
        return false;
    }
    if (out.Read<uint32_t>(4) != kVersion) {
        error = "unsupported version " + std::to_string(out.Read<uint32_t>(4));
        return false;
    }
    int64_t table = out.Ptr(8);
    if (table < 0 || !out.In(table, 16)) {
        error = "instance table offset out of range";
        return false;
    }
    uint32_t count = out.Read<uint32_t>(table);
    int64_t entries = out.Ptr(table + 8);
    if (count > kMaxCount || entries < 0 || !out.In(entries, uint64_t(count) * 16)) {
        error = "instance table entries out of range";
        return false;
    }
    out.instances_.reserve(count);
    for (uint32_t i = 0; i < count; i++) {
        Instance in;
        in.offset = out.Read<uint32_t>(entries + 16 * i);
        in.flags = out.Read<uint32_t>(entries + 16 * i + 4);
        in.layout_crc = out.Read<uint64_t>(entries + 16 * i + 8);
        if (!out.In(in.offset, 8)) {
            error = "instance " + std::to_string(i) + " offset out of range";
            return false;
        }
        if (mode == Mode::File) {
            // The u32 type hash sits where the vtable pointer goes after loading.
            in.type = reg.Find(out.Read<uint32_t>(in.offset));
        } else {
            // Loaded image: the slot holds a vtable pointer; identify the type by its layout CRC
            // (unique per type in the client's registry).
            in.type = reg.FindByLayoutCrc(in.layout_crc);
        }
        in.crc_ok = in.type && in.type->layout_crc == in.layout_crc;
        out.instances_.push_back(in);
    }
    return true;
}

bool Document::AllCrcOk() const {
    for (const auto& i : instances_)
        if (!i.crc_ok) return false;
    return !instances_.empty();
}

int Document::InstanceAt(uint64_t offset) const {
    for (size_t i = 0; i < instances_.size(); i++)
        if (instances_[i].offset == offset) return static_cast<int>(i);
    return -1;
}

int64_t Document::Ptr(uint64_t at) const {
    if (!In(at, 8)) return -1;
    uint64_t v = Read<uint64_t>(at);
    if (v == 0) return -1;
    if (mode_ == Mode::Loaded) {
        if (v < base_ || v - base_ >= bytes_.size()) return -1;  // static sentinel / outside the image
        return static_cast<int64_t>(v - base_);
    }
    if (v >= bytes_.size()) return -1;
    return static_cast<int64_t>(v);
}

Value Document::Decode(int instance) const {
    if (instance < 0 || static_cast<size_t>(instance) >= instances_.size()) return Error(nullptr, "no such instance", 0);
    const Instance& in = instances_[instance];
    if (!in.type) return Error(nullptr, "unknown type", in.offset);
    return DecodeObject(in.offset, *in.type);
}

Value Document::DecodeObject(uint64_t offset, const TypeInfo& type) const {
    Value v;
    v.type = Value::Type::Inline;
    v.kind = Kind::Inline;
    v.type_hash = type.hash;
    if (!In(offset, type.size)) return Error(nullptr, "object out of range", offset);
    for (const FieldInfo* f : reg_->AllFields(type)) {
        Value fv = DecodeField(offset + f->offset, *f);
        fv.field_hash = f->hash;
        fv.kind = f->kind;
        fv.flags = f->flags;
        v.items.push_back(std::move(fv));
    }
    return v;
}

Value Document::DecodeField(uint64_t at, const FieldInfo& f) const {
    switch (f.kind) {
        case Kind::Primitive:
            return f.elem == prim::teString ? DecodeString(at) : DecodePrimitive(at, f.elem);
        case Kind::Enum: {
            Value v = DecodePrimitive(at, prim::u32);
            v.type = Value::Type::Enum;
            v.type_hash = f.elem;
            return v;
        }
        case Kind::Object:
            return DecodeObjectPtr(at, f.elem);
        case Kind::Inline: {
            const TypeInfo* t = reg_->Find(f.elem);
            if (!t) return Error(&f, "unknown inline type", at);
            return DecodeObject(at, *t);
        }
        case Kind::FileRef:
        case Kind::ResourceRef:
            return DecodeRef(at, f.elem);
        case Kind::Map:
            return DecodeMap(at, f);
        default:
            return DecodeList(at, f);
    }
}

Value Document::DecodePrimitive(uint64_t at, uint32_t elem) const {
    uint32_t size = reg_->PrimitiveSize(elem);
    if (size == 0) return Error(nullptr, "unknown primitive", at);
    if (!In(at, size)) return Error(nullptr, "primitive out of range", at);
    Value v;
    v.type = Value::Type::Primitive;
    v.type_hash = elem;
    v.raw.assign(bytes_.begin() + at, bytes_.begin() + at + size);
    return v;
}

Value Document::DecodeString(uint64_t at) const {
    // teString: { ptr -> { i32 len; u32 crc32(text); char* buf }, i64 string_type }. Empty strings point at
    // a static empty string after loading (offset 0 in the file). Measured on the 0.8 install (T065): all
    // 35,800 non-empty strings of the 30,930 STU files have this order, e.g. {11, 0x88eae14e, → "WEAPON_TAIL"}.
    Value v;
    v.type = Value::Type::String;
    v.type_hash = prim::teString;
    if (!In(at, 16)) return Error(nullptr, "string out of range", at);
    int64_t p = Ptr(at);
    if (p < 0) return v;
    if (!In(p, 16)) return Error(nullptr, "string header out of range", p);
    int32_t len = Read<int32_t>(p);
    int64_t buf = Ptr(p + 8);
    if (len < 0 || buf < 0 || !In(buf, len)) return Error(nullptr, "string buffer out of range", p);
    v.str.assign(reinterpret_cast<const char*>(bytes_.data() + buf), len);
    return v;
}

Value Document::DecodeObjectPtr(uint64_t at, uint32_t elem) const {
    Value v;
    v.type_hash = elem;
    if (!In(at, 8)) return Error(nullptr, "object pointer out of range", at);
    int64_t off;
    if (mode_ == Mode::File) {
        uint32_t o = Read<uint32_t>(at);
        if (o == kNullObject) return v;  // Null
        off = o;
    } else {
        off = Ptr(at);
        if (off < 0) return v;
    }
    int idx = InstanceAt(off);
    if (idx < 0) return Error(nullptr, "object pointer is not an instance", off);
    v.type = Value::Type::Object;
    v.instance = idx;
    return v;
}

Value Document::DecodeRef(uint64_t at, uint32_t elem) const {
    Value v;
    v.type = Value::Type::Ref;
    v.type_hash = elem;
    if (!In(at, 16)) return Error(nullptr, "reference out of range", at);
    v.handle = Read<uint64_t>(at);
    v.guid = Read<uint64_t>(at + 8);
    return v;
}

Value Document::DecodeList(uint64_t at, const FieldInfo& f) const {
    // Descriptor { i64 offset; i64 flag } -> header { i32 count; pad; i64 data }.
    Value v;
    v.type = Value::Type::List;
    v.type_hash = f.elem;
    if (!In(at, 16)) return Error(&f, "list descriptor out of range", at);
    int64_t h = Ptr(at);
    if (h < 0) return v;  // empty (static sentinel after loading)
    if (!In(h, 16)) return Error(&f, "list header out of range", h);
    int32_t count = Read<int32_t>(h);
    if (count <= 0) return v;
    if (static_cast<uint32_t>(count) > kMaxCount) return Error(&f, "list count out of range", h);
    int64_t data = Ptr(h + 8);
    if (data < 0) return Error(&f, "list data out of range", h);
    uint64_t stride;
    switch (f.kind) {
        case Kind::PrimitiveList:
            stride = f.elem == prim::teString ? 16 : reg_->PrimitiveSize(f.elem);
            if (stride == 0) return Error(&f, "unknown primitive element", at);
            break;
        case Kind::ObjectList: stride = 8; break;
        case Kind::EnumList: stride = 4; break;
        case Kind::FileRefList:
        case Kind::ResourceRefList: stride = 16; break;
        case Kind::InlineList: {
            const TypeInfo* t = reg_->Find(f.elem);
            if (!t) return Error(&f, "unknown inline element type", at);
            stride = t->size;
            break;
        }
        default:
            return Error(&f, "not a list kind", at);
    }
    if (!In(data, stride * count)) return Error(&f, "list elements out of range", data);
    v.items.reserve(count);
    for (int32_t i = 0; i < count; i++) {
        uint64_t e = data + stride * i;
        Value ev;
        switch (f.kind) {
            case Kind::PrimitiveList:
                ev = f.elem == prim::teString ? DecodeString(e) : DecodePrimitive(e, f.elem);
                break;
            case Kind::ObjectList: ev = DecodeObjectPtr(e, f.elem); break;
            case Kind::EnumList:
                ev = DecodePrimitive(e, prim::u32);
                ev.type = Value::Type::Enum;
                ev.type_hash = f.elem;
                break;
            case Kind::FileRefList:
            case Kind::ResourceRefList: ev = DecodeRef(e, f.elem); break;
            case Kind::InlineList: ev = DecodeObject(e, *reg_->Find(f.elem)); break;
            default: break;
        }
        ev.kind = f.kind;
        v.items.push_back(std::move(ev));
    }
    return v;
}

Value Document::DecodeMap(uint64_t at, const FieldInfo& f) const {
    // Descriptor { i64 offset; i64 flag } -> { i32 buckets; pad; i64 bucket_ends; i64 items };
    // bucket_ends[i] = end index of bucket i (cumulative); items: { u32 key; pad; object pointer }.
    Value v;
    v.type = Value::Type::Map;
    v.type_hash = f.elem;
    if (!In(at, 16)) return Error(&f, "map descriptor out of range", at);
    int64_t h = Ptr(at);
    if (h < 0) return v;
    if (!In(h, 24)) return Error(&f, "map header out of range", h);
    int32_t buckets = Read<int32_t>(h);
    if (buckets <= 0) return v;
    if (static_cast<uint32_t>(buckets) > kMaxCount) return Error(&f, "map bucket count out of range", h);
    int64_t ends = Ptr(h + 8), items = Ptr(h + 16);
    if (ends < 0 || !In(ends, 4ull * buckets)) return Error(&f, "map buckets out of range", h);
    int32_t n = Read<int32_t>(ends + 4 * (buckets - 1));
    if (n <= 0) return v;
    if (items < 0 || static_cast<uint32_t>(n) > kMaxCount || !In(items, 16ull * n))
        return Error(&f, "map items out of range", h);
    for (int32_t i = 0; i < n; i++) {
        Value ev = DecodeObjectPtr(items + 16 * i + 8, f.elem);
        ev.map_key = Read<uint32_t>(items + 16 * i);
        ev.kind = Kind::Object;
        v.items.push_back(std::move(ev));
    }
    return v;
}

}  // namespace stu
