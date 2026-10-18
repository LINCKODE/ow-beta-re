// Map data writer: in-place record edits and entity insertion with relocation (docs/Maps.md §5).
#include <algorithm>
#include <cstring>

#include "internal.h"
#include "maps/mapdata.h"

namespace maps {

namespace {

bool Fail(Error* err, const char* msg) {
    if (err) err->message = msg;
    return false;
}

template <typename T>
void Put(Bytes& d, uint64_t off, const T& v) {
    std::memcpy(d.data() + off, &v, sizeof(T));
}
template <typename T>
T Get(const Bytes& d, uint64_t off) {
    T v;
    std::memcpy(&v, d.data() + off, sizeof(T));
    return v;
}

uint64_t Align16(uint64_t v) { return (v + 15) & ~15ull; }

void WriteXf(Bytes& d, uint64_t base, const XfLayout& l, const Transform& xf) {
    Put(d, base + l.pos, xf.pos);
    if (l.scale) Put(d, base + l.scale, xf.scale);
    if (l.rot) Put(d, base + l.rot, xf.rot);
    if (l.uniform_scale) Put(d, base + l.uniform_scale, xf.scale.x);
}

}  // namespace

bool SetTransform(Bytes& data, const Record& r, const Transform& xf, Error* err) {
    XfLayout l = LayoutOf(r);
    if (!l.pos) return Fail(err, "record has no writable transform");
    if ((uint64_t)r.offset + r.size > data.size()) return Fail(err, "record outside the data");
    WriteXf(data, r.offset, l, xf);
    return true;
}

bool SetDefinition(Bytes& data, const Record& r, Guid def, Error* err) {
    if (r.type != kEntity) return Fail(err, "not an entity record");
    if ((uint64_t)r.offset + r.size > data.size()) return Fail(err, "record outside the data");
    Put(data, r.offset + 0x18, def);
    return true;
}

bool AddEntity(Bytes& data, const MapData& m, const Record& src, const Uuid& uuid, const Transform& xf,
               Error* err) {
    const Header& h = m.header;
    if (src.type != kEntity) return Fail(err, "source is not an entity record");
    if (data.size() != m.size) return Fail(err, "data does not match the parsed map");
    const uint64_t p1 = (uint64_t)h.offsets[kEntity] + h.sizes[kEntity];  // end of the entity section
    const uint64_t p2 = m.blob_end;                                      // end of the blob region
    bool blobs = !src.instance_data.empty();
    if (blobs && (m.blob_begin < p1 || p2 < m.blob_begin)) return Fail(err, "unexpected blob region layout");
    for (int t = 0; t < kNumRecordTypes; t++)  // no section may start inside the blob region
        if (h.counts[t] && h.offsets[t] > m.blob_begin && h.offsets[t] < p2) return Fail(err, "section inside the blob region");

    // New record: a copy of src (its pairs are rewritten below), a new GUID and transform.
    Bytes rec(data.begin() + src.offset, data.begin() + src.offset + src.size);
    std::memcpy(rec.data(), uuid.b.data(), 16);
    Bytes tmp = rec;  // WriteXf works on a Bytes at an offset
    WriteXf(tmp, 0, LayoutOf(src), xf);
    rec = tmp;

    uint64_t blob_total = 0;
    for (auto& id : src.instance_data) blob_total += id.size;
    const uint64_t d1 = Align16(rec.size());
    const uint64_t d2 = blobs ? Align16(blob_total) : 0;
    auto f = [&](uint64_t x) { return x + (x >= p1 ? d1 : 0) + (x >= p2 ? d2 : 0); };

    Bytes out;
    out.reserve(data.size() + d1 + d2);
    out.insert(out.end(), data.begin(), data.begin() + p1);
    out.insert(out.end(), rec.begin(), rec.end());
    out.resize(p1 + d1, 0);
    out.insert(out.end(), data.begin() + p1, data.begin() + p2);
    const uint64_t new_blobs = out.size();  // = f(p2) - d2 = p2 + d1
    for (auto& id : src.instance_data) out.insert(out.end(), data.begin() + id.offset, data.begin() + id.offset + id.size);
    out.resize(new_blobs + d2, 0);
    out.insert(out.end(), data.begin() + p2, data.end());

    // Header: counts, sizes, offsets and the 64-bit table pointers.
    Put(out, 0x20c, h.record_count + 1);
    Put(out, 0x250 + 4 * kEntity, h.counts[kEntity] + 1);
    Put(out, 0x290 + 4 * kEntity, h.sizes[kEntity] + (uint32_t)rec.size());
    for (int t = 0; t < kNumRecordTypes; t++)
        if (h.counts[t] && t != kEntity) Put(out, 0x210 + 4 * t, (uint32_t)f(h.offsets[t]));
    for (int i = 0; i < 4; i++)
        if (h.collision_tables[i]) Put(out, 0x2d8 + 8 * i, f(h.collision_tables[i]));
    if (h.nav) Put(out, 0x2f8, f(h.nav));
    if (h.tactical) Put(out, 0x300, f(h.tactical));
    if (h.sound_space_table) Put(out, 0x310, f(h.sound_space_table));
    for (auto& c : m.collision.hulls)
        for (int k = 0; k < 4; k++) Put(out, f(c.at) + 8 * k, f(c.ptr[k]));
    for (auto& c : m.collision.meshes) {
        Put(out, f(c.at), f(c.bvh));
        Put(out, f(c.at) + 8, f(c.verts));
        Put(out, f(c.at) + 16, f(c.tris));
    }
    // Instance-data pairs: offsets are relative to the pair.
    for (auto& r : m.records) {
        if (r.type != kEntity) continue;
        for (size_t k = 0; k < r.instance_data.size(); k++) {
            uint64_t pair = f(r.offset + 0x80 + 8 * k);
            Put(out, pair + 4, (uint32_t)(f(r.instance_data[k].offset) - pair));
        }
    }
    uint64_t at = new_blobs;
    for (size_t k = 0; k < src.instance_data.size(); k++) {
        uint64_t pair = p1 + 0x80 + 8 * k;
        Put(out, pair + 4, (uint32_t)(at - pair));
        at += src.instance_data[k].size;
    }
    data.swap(out);
    return true;
}

}  // namespace maps
