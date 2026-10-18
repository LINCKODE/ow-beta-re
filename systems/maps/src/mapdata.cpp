// Map data parser (docs/Maps.md §2). Every read goes through Reader, which checks bounds.
#include "maps/mapdata.h"
#include "internal.h"

#include <algorithm>
#include <cstdio>
#include <cstring>
#include <map>

namespace maps {

namespace {

struct Reader {
    std::span<const uint8_t> d;
    bool ok = true;
    template <typename T>
    T get(uint64_t off) {
        T v{};
        if (off + sizeof(T) > d.size() || off + sizeof(T) < off) {
            ok = false;
            return v;
        }
        std::memcpy(&v, d.data() + off, sizeof(T));
        return v;
    }
    uint8_t u8(uint64_t o) { return get<uint8_t>(o); }
    uint16_t u16(uint64_t o) { return get<uint16_t>(o); }
    uint32_t u32(uint64_t o) { return get<uint32_t>(o); }
    int32_t i32(uint64_t o) { return get<int32_t>(o); }
    uint64_t u64(uint64_t o) { return get<uint64_t>(o); }
    float f32(uint64_t o) { return get<float>(o); }
    Vec3 v3(uint64_t o) { return {f32(o), f32(o + 4), f32(o + 8)}; }
    Vec4 v4(uint64_t o) { return {f32(o), f32(o + 4), f32(o + 8), f32(o + 12)}; }
    Quat q(uint64_t o) { return {f32(o), f32(o + 4), f32(o + 8), f32(o + 12)}; }
    Uuid uuid(uint64_t o) {
        Uuid u;
        if (o + 16 > d.size()) {
            ok = false;
            return u;
        }
        std::memcpy(u.b.data(), d.data() + o, 16);
        return u;
    }
};

bool Fail(Error* err, const char* fmt, uint64_t a = 0, uint64_t b = 0, uint64_t c = 0) {
    if (err) {
        char buf[256];
        std::snprintf(buf, sizeof buf, fmt, (unsigned long long)a, (unsigned long long)b, (unsigned long long)c);
        err->message = buf;
    }
    return false;
}

// Record builder: appends decoded fields and fills the typed members.
struct Dec {
    Reader& rd;
    Record& r;
    uint64_t base;  // record offset
    void u32(const char* n, uint32_t at) { r.fields.push_back({Field::U32, n, at, rd.u32(base + at)}); }
    void i32(const char* n, uint32_t at) {
        r.fields.push_back({Field::I32, n, at, (uint64_t)(int64_t)rd.i32(base + at)});
    }
    void hex(const char* n, uint32_t at) { r.fields.push_back({Field::Hex32, n, at, rd.u32(base + at)}); }
    void f32(const char* n, uint32_t at) {
        Field f{Field::F32, n, at};
        f.f[0] = rd.f32(base + at);
        r.fields.push_back(f);
    }
    void v3(const char* n, uint32_t at) {
        Field f{Field::Vec3f, n, at};
        Vec3 v = rd.v3(base + at);
        f.f = {v.x, v.y, v.z, 0};
        r.fields.push_back(f);
    }
    void v4(const char* n, uint32_t at) {
        Field f{Field::Vec4f, n, at};
        Vec4 v = rd.v4(base + at);
        f.f = {v.x, v.y, v.z, v.w};
        r.fields.push_back(f);
    }
    Guid guid(const char* n, uint32_t at) {
        Guid g = rd.u64(base + at);
        r.fields.push_back({Field::Guid64, n, at, g});
        return g;
    }
    void u64(const char* n, uint32_t at) { r.fields.push_back({Field::U64, n, at, rd.u64(base + at)}); }
    // Transform layouts: pos/scale/rot at the given record offsets (0 = absent).
    void xf(uint32_t pos, uint32_t scale, uint32_t rot, uint8_t layout) {
        r.has_xf = true;
        r.xf_at = pos;
        r.xf_layout = layout;
        r.xf.pos = rd.v3(base + pos);
        if (scale) r.xf.scale = rd.v3(base + scale);
        if (rot) r.xf.rot = rd.q(base + rot);
        v3("pos", pos);
        if (scale) v3("scale", scale);
        if (rot) v4("rot", rot);
    }
};

}  // namespace

bool Uuid::IsZero() const {
    for (auto x : b)
        if (x) return false;
    return true;
}

std::string Uuid::Hex() const {
    static const char* h = "0123456789abcdef";
    std::string s;
    for (auto x : b) {
        s += h[x >> 4];
        s += h[x & 15];
    }
    return s;
}

uint32_t GuidType(Guid g) {
    uint32_t v = (g >> 48) & 0xfff, r = 0;
    for (int i = 0; i < 12; i++)
        if (v & (1u << i)) r |= 1u << (11 - i);
    return r + 1;
}

const char* RecordTypeName(uint8_t t) {
    switch (t) {
    case kModelGroup: return "model group";
    case kModel: return "model";
    case kOccluder: return "occluder";
    case kReflectionProbe: return "reflection probe";
    case kType5: return "type 5";
    case kTextLabel: return "text label";
    case kMapText: return "map text";
    case kPlacedModel: return "placed model";
    case kLight: return "light";
    case kSoundVolume: return "sound volume";
    case kEntity: return "entity";
    case kSoundEmitter: return "sound emitter";
    case kEffect: return "effect";
    case kVolume44: return "volume 0x44";
    case kVolume45: return "volume 0x45";
    default: return "unknown";
    }
}

const Record* MapData::ByPlaced(uint32_t p) const {
    for (const auto& r : records)
        if (r.placed == (int32_t)p) return &r;
    return nullptr;
}

const Record* MapData::ByUuid(const Uuid& u) const {
    for (const auto& r : records)
        if (r.uuid == u) return &r;
    return nullptr;
}

// Decodes the type-specific part of a record (offsets relative to the record; client functions in
// docs/Maps.md §2.4).
static bool DecodeRecord(Reader& rd, Record& r, Error* err) {
    Dec d{rd, r, r.offset};
    switch (r.type) {
    case kModelGroup: {  // static world FUN_7ff650a04e60
        r.primary = d.guid("model", 0x18);
        d.u32("groups", 0x20);
        uint32_t ng = rd.u32(r.offset + 0x20);
        uint64_t g = r.offset + 0x38;
        for (uint32_t gi = 0; gi < ng; gi++) {
            Guid look = rd.u64(g);
            int32_t next = rd.i32(g + 0x10);
            uint32_t ni = rd.u32(g + 0x14);
            if (g + 0x18 + (uint64_t)ni * 0x58 > (uint64_t)r.offset + r.size)
                return Fail(err, "model group at %#llx: %llu instances past the record", r.offset, ni);
            for (uint32_t k = 0; k < ni; k++) {
                uint64_t o = g + 0x18 + (uint64_t)k * 0x58;
                GroupInstance in;
                in.look = look;
                in.at = (uint32_t)o;
                in.xf = {rd.v3(o), rd.v3(o + 12), rd.q(o + 24)};
                in.uuid = rd.uuid(o + 0x28);
                r.instances.push_back(in);
            }
            if (gi + 1 < ng) {
                if (next <= 0) return Fail(err, "model group at %#llx: bad group link", r.offset);
                g += next;
            } else if (g + 0x18 + (uint64_t)ni * 0x58 != (uint64_t)r.offset + r.size) {
                return Fail(err, "model group at %#llx: groups don't fill the record", r.offset);
            }
        }
        if (!r.instances.empty()) {
            r.secondary = r.instances[0].look;
            r.has_xf = true;  // the first instance, for plots (not writable through SetTransform)
            r.xf = r.instances[0].xf;
        }
        break;
    }
    case kModel:  // static world: +0x18 model, +0x20 look, +0x28 pos/scale/rot, +0x58 flags, +0x5C u8
    case kPlacedModel:  // spawner FUN_7ff650a28870: same layout; +0x58 bit0/1/2/3 + bits 4.. scene flags
        r.primary = d.guid("model", 0x18);
        r.secondary = d.guid("look", 0x20);
        d.xf(0x28, 0x34, 0x40, 1);
        d.hex("u50", 0x50);
        d.hex("u54", 0x54);
        d.hex("flags58", 0x58);
        d.hex("u5c", 0x5c);
        break;
    case kOccluder:  // static world: rot +0x18, pos +0x28, extents +0x34 (the thinnest axis is dropped)
        d.xf(0x28, 0, 0x18, 2);
        d.v3("extents", 0x34);
        break;
    case kReflectionProbe:  // static world keeps {record+0x18, uuid, +0xB8}
        d.xf(0x18, 0x24, 0x30, 1);
        d.v3("local_pos", 0x40);
        d.v3("local_scale", 0x4c);
        d.v4("local_rot", 0x58);
        d.v3("extents", 0x68);
        r.primary = d.guid("texture0", 0x78);
        r.secondary = d.guid("texture1", 0x80);
        for (uint32_t o = 0x88; o < 0xb8; o += 4) d.hex("u", o);
        d.hex("ub8", 0xb8);
        d.hex("ubc", 0xbc);
        break;
    case kTextLabel: {  // static world (+0x1E0 list, AABB FUN_7ff650efb460)
        std::string s;
        for (uint32_t o = 0x18; o < 0x38; o++) {
            char c = (char)rd.u8(r.offset + o);
            if (!c) break;
            s += c;
        }
        Field f{Field::Text, "text", 0x18};
        f.text = s;
        r.fields.push_back(f);
        d.xf(0x48, 0, 0x38, 2);
        d.f32("u54", 0x54);
        d.f32("size", 0x58);
        d.hex("color", 0x5c);
        d.u32("u60", 0x60);
        d.hex("u64", 0x64);
        break;
    }
    case kMapText:  // static world FUN_7ff650a06d90: rot +0x18, pos +0x28, scale +0x50
        d.xf(0x28, 0, 0x18, 3);
        r.primary = d.guid("string", 0x38);
        r.secondary = d.guid("font", 0x40);
        d.guid("material", 0x48);
        d.f32("scale", 0x50);
        d.hex("u54", 0x54);
        {
            float s = rd.f32(r.offset + 0x50);
            r.xf.scale = {s, s, s};
        }
        break;
    case kLight: {  // light component OnCreate FUN_7ff6509fac30 (record+0x18 = its data)
        d.xf(0x28, 0, 0x18, 2);
        d.guid("guid38", 0x38);
        d.u32("light_type", 0x40);  // 0 point, 1 spot
        d.hex("flags", 0x44);
        d.u32("u48", 0x48);
        d.v3("color", 0x4c);
        d.v3("color2", 0x58);
        d.hex("u64", 0x64);
        d.v4("params68", 0x68);
        d.v3("v78", 0x78);
        d.v3("v84", 0x84);
        d.f32("f90", 0x90);
        d.f32("f94", 0x94);
        d.f32("f98", 0x98);
        d.f32("f9c", 0x9c);
        d.f32("radius", 0xa0);
        d.hex("ua4", 0xa4);
        d.f32("fa8", 0xa8);
        d.f32("fac", 0xac);
        d.f32("fb0", 0xb0);
        d.u64("u64b8", 0xb8);
        d.u64("u64c0", 0xc0);
        d.u32("detail", 0xc8);  // not spawned above the light detail setting (FUN_7ff650a28cc0)
        d.hex("ucc", 0xcc);
        break;
    }
    case kSoundVolume: {  // spawner FUN_7ff650a29890 (component 0x06)
        uint16_t nb = rd.u16(r.offset + 0x18), ns = rd.u16(r.offset + 0x1a), nc = rd.u16(r.offset + 0x1c);
        uint32_t ob = rd.u32(r.offset + 0x20), os = rd.u32(r.offset + 0x24), oc = rd.u32(r.offset + 0x28);
        d.u32("boxes", 0x18);
        d.hex("u1c", 0x1c);
        auto in = [&](uint32_t off, uint32_t n, uint32_t stride) {
            return r.offset + 0x18 + (uint64_t)off + (uint64_t)n * stride <= (uint64_t)r.offset + r.size;
        };
        if (!in(ob, nb, 0x28) || !in(os, ns, 0x10) || !in(oc, nc, 0x28))
            return Fail(err, "sound volume at %#llx: shapes past the record", r.offset);
        for (uint32_t i = 0; i < nb; i++) {
            uint64_t o = r.offset + 0x18 + ob + i * 0x28;
            r.shapes.push_back({Shape::Box, rd.q(o), rd.v3(o + 0x10), rd.v3(o + 0x1c)});
        }
        for (uint32_t i = 0; i < ns; i++) {
            uint64_t o = r.offset + 0x18 + os + i * 0x10;
            r.shapes.push_back({Shape::Sphere, Quat{}, rd.v3(o), {rd.f32(o + 12), 0, 0}});
        }
        for (uint32_t i = 0; i < nc; i++) {
            uint64_t o = r.offset + 0x18 + oc + i * 0x28;
            r.shapes.push_back({Shape::Capsule, rd.q(o), rd.v3(o + 0x10), {rd.f32(o + 0x1c), rd.f32(o + 0x20), 0}});
        }
        if (!r.shapes.empty()) {
            r.has_xf = true;  // first shape, for plots
            r.xf.pos = r.shapes[0].pos;
            r.xf.rot = r.shapes[0].rot;
        }
        break;
    }
    case kEntity: {  // spawn FUN_7ff650a293d0; blobs loaded by the MAPDATA constructor FUN_7ff650ef9130
        r.primary = d.guid("definition", 0x18);
        d.xf(0x20, 0x2c, 0x38, 1);
        d.u32("instance_data", 0x48);
        d.hex("u4c", 0x4c);
        d.hex("u50", 0x50);  // +0x52 u16 / +0x54: passed to the model component (FUN_7ff6509c1820)
        d.hex("u54", 0x54);
        d.hex("u58", 0x58);
        d.u32("scene_flag", 0x68);  // != 0: scene flag 0x10000 set
        r.secondary = d.guid("skin", 0x70);  // STUSkin (0x076); bits 32..47 = variant, bit 31 = flag
        d.u32("u78", 0x78);
        uint32_t n = rd.u32(r.offset + 0x48);
        if (r.size != 0x80 + 8ull * n)
            return Fail(err, "entity at %#llx: size %#llx != 0x80 + 8*%llu", r.offset, r.size, n);
        for (uint32_t k = 0; k < n; k++) {
            uint64_t p = r.offset + 0x80 + 8ull * k;
            InstanceData id;
            id.slot = rd.u32(p);
            id.offset = (uint32_t)(p + rd.u32(p + 4));
            r.instance_data.push_back(id);
        }
        break;
    }
    case kSoundEmitter:  // spawner FUN_7ff650a2a7f0: sound system vfunc 0x78
        r.primary = d.guid("sound", 0x18);
        d.xf(0x20, 0x2c, 0x38, 1);
        break;
    case kEffect:  // spawner FUN_7ff650a29db0: inline {01 03 0d}
        r.primary = d.guid("effect", 0x18);
        d.xf(0x20, 0x2c, 0x38, 1);
        d.u32("u48", 0x48);  // → effect component +0xC8
        d.u32("detail", 0x4c);  // not spawned above the effect detail setting (0 = 2)
        d.hex("u50", 0x50);
        d.hex("u54", 0x54);
        break;
    case kVolume44:  // spawner FUN_7ff650a2a070: component 0x44
        d.xf(0x18, 0x24, 0x30, 1);
        d.v3("extents", 0x40);
        d.v4("color", 0x4c);
        d.v3("v5c", 0x5c);
        r.primary = d.guid("texture", 0x68);
        for (uint32_t o = 0x70; o < 0x88; o += 4) d.f32("f", o);
        break;
    case kVolume45:  // spawner FUN_7ff650a2a4c0: component 0x45 (FUN_7ff650a2b150 sets its bounds)
        d.xf(0x18, 0x24, 0x30, 1);
        d.v3("extents", 0x40);
        d.hex("u4c", 0x4c);
        break;
    default:
        break;
    }
    if (!rd.ok) return Fail(err, "record at %#llx: read past the end", r.offset);
    return true;
}

Vec3 CollisionVertex(std::span<const uint8_t> data, const CollisionMesh& c, uint32_t i) {
    return Reader{data}.v3(c.verts + 16ull * i);
}

CollisionNode CollisionBvhNode(std::span<const uint8_t> data, const CollisionMesh& c, uint32_t i) {
    Reader rd{data};
    uint64_t o = c.bvh + 32ull * i;
    return {rd.v3(o), rd.v3(o + 12), rd.i32(o + 0x18), rd.u16(o + 0x1c), rd.u8(o + 0x1e), rd.u8(o + 0x1f)};
}

CollisionTriangle CollisionTri(std::span<const uint8_t> data, const CollisionMesh& c, uint32_t i) {
    Reader rd{data};
    uint64_t o = c.tris + 32ull * i;
    return {{rd.i32(o), rd.i32(o + 4), rd.i32(o + 8)}, {rd.i32(o + 12), rd.i32(o + 16), rd.i32(o + 20)},
            rd.u16(o + 0x18), rd.u16(o + 0x1a), rd.u16(o + 0x1c)};
}

std::optional<MapData> Parse(std::span<const uint8_t> data, Error* err) {
    Reader rd{data};
    MapData m;
    m.size = (uint32_t)data.size();
    if (data.size() < 0x320 || data.size() > 0xffffffffull) {
        Fail(err, "map data of %llu bytes", data.size());
        return std::nullopt;
    }
    Header& h = m.header;
    for (int i = 0; i < 5; i++) h.bounds[i] = {rd.v4(0x20 * i), rd.v4(0x20 * i + 0x10)};
    h.camera_pos = rd.v4(0xa0);
    for (int i = 0; i < 16; i++) h.camera_rot[i] = rd.f32(0xb0 + 4 * i);
    Environment& e = h.env;
    e.gamemode_def = rd.u64(0xf0);
    e.texture_f8 = rd.u64(0xf8);
    e.lighting = rd.u64(0x100);
    e.texture_108 = rd.u64(0x108);
    e.sky_model = rd.u64(0x110);
    e.sky_look = rd.u64(0x118);
    e.sound_space = rd.u64(0x120);
    e.texture_128 = rd.u64(0x128);
    e.texture_130 = rd.u64(0x130);
    e.name = rd.u64(0x138);
    e.sun_dir = rd.v3(0x14c);
    for (int i = 0; i < 3; i++) e.textures_180[i] = rd.u64(0x180 + 8 * i);
    for (int i = 0; i < 44; i++) e.raw[i] = rd.f32(0x140 + 4 * i);
    h.u1f0 = rd.u32(0x1f0);
    h.u1f4 = rd.u32(0x1f4);
    h.last_phase = rd.u16(0x1f8);
    h.u1fa = rd.u16(0x1fa);
    for (int i = 0; i < 4; i++) h.collision_counts[i] = rd.u32(0x1fc + 4 * i);
    h.record_count = rd.u32(0x20c);
    for (int t = 0; t < kNumRecordTypes; t++) {
        h.offsets[t] = rd.u32(0x210 + 4 * t);
        h.counts[t] = rd.u32(0x250 + 4 * t);
        h.sizes[t] = rd.u32(0x290 + 4 * t);
    }
    h.u2d0 = rd.u32(0x2d0);
    h.u2d4 = rd.u32(0x2d4);
    for (int i = 0; i < 4; i++) h.collision_tables[i] = rd.u64(0x2d8 + 8 * i);
    h.nav = rd.u64(0x2f8);
    h.tactical = rd.u64(0x300);
    h.sound_space_count = rd.u64(0x308);
    h.sound_space_table = rd.u64(0x310);
    h.u318 = rd.u64(0x318);

    // Records, section by section (the client walks a section by record sizes until offset + size).
    uint64_t sum = 0;
    int32_t placed = 0;
    for (int t = 0; t < kNumRecordTypes; t++) {
        uint32_t n = h.counts[t];
        sum += n;
        if (!n) continue;
        uint64_t off = h.offsets[t], end = (uint64_t)h.offsets[t] + h.sizes[t];
        if (off < 0x320 || end > data.size()) {
            Fail(err, "type %llu section %#llx+%#llx outside the file", t, off, h.sizes[t]);
            return std::nullopt;
        }
        for (uint32_t j = 0; j < n; j++) {
            Record r;
            r.offset = (uint32_t)off;
            r.uuid = rd.uuid(off);
            r.flags = rd.u8(off + 0x10);
            r.b11 = rd.u8(off + 0x11);
            r.phase = rd.u8(off + 0x12);
            r.type = rd.u8(off + 0x13);
            r.size = rd.u32(off + 0x14);
            r.index = (int32_t)j;
            if (r.type != t || r.size < 0x18 || off + r.size > end) {
                Fail(err, "type %llu record %llu at %#llx: bad type/size", t, j, off);
                return std::nullopt;
            }
            if (t >= kFirstPlacedType) r.placed = placed++;
            if (!DecodeRecord(rd, r, err)) return std::nullopt;
            m.records.push_back(std::move(r));
            off += m.records.back().size;
        }
        if (off != end) {
            Fail(err, "type %llu section: records end at %#llx, size says %#llx", t, off, end);
            return std::nullopt;
        }
    }
    if (sum != h.record_count) {
        Fail(err, "section counts sum to %llu, header says %llu", sum, h.record_count);
        return std::nullopt;
    }
    m.placed = (uint32_t)placed;

    // Instance-data blobs: sizes from the sorted blob starts; the region ends at the first section or
    // table after the last blob.
    std::vector<uint32_t> starts;
    for (auto& r : m.records)
        for (auto& id : r.instance_data) {
            if (id.offset + 16ull > data.size() || std::memcmp(data.data() + id.offset, "DUTS", 4) != 0) {
                Fail(err, "entity at %#llx: instance data at %#llx is not a STUD blob", r.offset, id.offset);
                return std::nullopt;
            }
            starts.push_back(id.offset);
        }
    std::sort(starts.begin(), starts.end());
    starts.erase(std::unique(starts.begin(), starts.end()), starts.end());
    if (!starts.empty()) {
        m.blob_begin = starts.front();
        uint64_t limit = data.size();
        for (int t = 0; t < kNumRecordTypes; t++)
            if (h.counts[t] && h.offsets[t] > starts.back()) limit = std::min<uint64_t>(limit, h.offsets[t]);
        for (uint64_t p : {h.collision_tables[0], h.collision_tables[1], h.collision_tables[2], h.collision_tables[3],
                           h.nav, h.tactical, h.sound_space_table})
            if (p > starts.back()) limit = std::min<uint64_t>(limit, p);
        // the collision mesh data (vertices, triangles, BVH) sits before its table
        m.blob_end = (uint32_t)limit;
        for (auto& r : m.records)
            for (auto& id : r.instance_data) {
                auto it = std::upper_bound(starts.begin(), starts.end(), id.offset);
                id.size = (it == starts.end() ? m.blob_end : *it) - id.offset;
            }
    }

    // Collision tables.
    auto table = [&](int k, uint32_t stride) -> bool {
        uint64_t p = h.collision_tables[k], n = h.collision_counts[k];
        if (!n) return true;
        return p >= 0x320 && p + n * stride <= data.size();
    };
    if (!table(0, 0x60) || !table(1, 0x68) || !table(2, 0x90) || !table(3, 0x58)) {
        Fail(err, "collision table outside the file");
        return std::nullopt;
    }
    for (uint32_t i = 0; i < h.collision_counts[0]; i++) {
        uint64_t o = h.collision_tables[0] + i * 0x60ull;
        m.collision.spheres.push_back({(uint32_t)o, rd.v3(o + 8), rd.f32(o + 0x14)});
    }
    for (uint32_t i = 0; i < h.collision_counts[1]; i++) {
        uint64_t o = h.collision_tables[1] + i * 0x68ull;
        m.collision.capsules.push_back({(uint32_t)o, rd.v3(o + 8), rd.v3(o + 0x14), rd.f32(o + 0x20)});
    }
    for (uint32_t i = 0; i < h.collision_counts[2]; i++) {
        uint64_t o = h.collision_tables[2] + i * 0x90ull;
        CollisionHull c{(uint32_t)o, rd.v3(o + 0x20), rd.u32(o + 0x2c), rd.u32(o + 0x30), rd.u32(o + 0x34), {}};
        for (int k = 0; k < 4; k++) {
            c.ptr[k] = rd.u64(o + 8 * k);
            if (c.ptr[k] >= data.size()) {
                Fail(err, "hull %llu: pointer %llu outside the file", i, k);
                return std::nullopt;
            }
        }
        m.collision.hulls.push_back(c);
    }
    for (uint32_t i = 0; i < h.collision_counts[3]; i++) {
        uint64_t o = h.collision_tables[3] + i * 0x58ull;
        CollisionMesh c{(uint32_t)o, rd.u64(o), rd.u64(o + 8), rd.u64(o + 16), rd.u32(o + 0x18), rd.u32(o + 0x1c),
                        rd.u32(o + 0x20), rd.u32(o + 0x24), rd.u32(o + 0x28)};  // bvh, verts, tris, nodes, verts, tris
        if (c.tris + 32ull * c.tri_count > data.size() || c.verts + 16ull * c.vert_count > data.size() ||
            c.bvh + 32ull * c.node_count > data.size()) {
            Fail(err, "collision mesh %llu outside the file", i);
            return std::nullopt;
        }
        m.collision.meshes.push_back(c);
    }

    // AI blobs and the sound-space table.
    auto stud = [&](uint64_t p) { return p && p + 16 <= data.size() && std::memcmp(data.data() + p, "DUTS", 4) == 0; };
    if ((h.nav && !stud(h.nav)) || (h.tactical && !stud(h.tactical))) {
        Fail(err, "nav/tactical blob is not a STUD file");
        return std::nullopt;
    }
    if (h.nav) m.nav_size = (uint32_t)((h.tactical > h.nav ? h.tactical : data.size()) - h.nav);
    if (h.tactical) m.tactical_size = (uint32_t)((h.sound_space_table > h.tactical ? h.sound_space_table : data.size()) - h.tactical);
    if (h.sound_space_count) {
        if (h.sound_space_table + h.sound_space_count * 24 > data.size()) {
            Fail(err, "sound-space table outside the file");
            return std::nullopt;
        }
        for (uint64_t i = 0; i < h.sound_space_count; i++) {
            uint64_t o = h.sound_space_table + 24 * i;
            m.sound_spaces.push_back({rd.uuid(o), rd.u64(o + 16)});
        }
    }
    if (!rd.ok) {
        Fail(err, "read past the end");
        return std::nullopt;
    }
    return m;
}

}  // namespace maps
