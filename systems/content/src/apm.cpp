// Layer 2: .apm manifests and asset package blobs.
#include "content/ow08/ow08.h"
#include "util.h"

namespace content::ow08 {

bool ParseApm(const Bytes& d, Apm& out, Error* err) {
    out = {};
    if (d.size() < 0x18) return Fail(err, "apm: short");
    out.unk00 = Le64(&d[0]);
    out.build = Le32(&d[8]);
    uint32_t items = Le32(&d[0xC]), entries = Le32(&d[0x10]);
    out.magic = Le32(&d[0x14]);
    if ((out.magic & 0xFFFFFF) != 0x636D66 || out.magic >> 24 != 0x13) return Fail(err, "apm: bad magic");  // "fmc", 0x13
    if (out.build != 24919) return Fail(err, "apm: build " + std::to_string(out.build) + ", expected 24919 (0.8)");
    size_t p = 0x18;
    if (p + size_t(entries) * 12 + size_t(items) * 0x48 != d.size()) return Fail(err, "apm: size does not match counts");
    for (uint32_t i = 0; i < entries; ++i, p += 12) out.entries.emplace_back(Le32(&d[p]), Le64(&d[p + 4]));
    for (uint32_t i = 0; i < items; ++i, p += 0x48) {
        ApmItem it;
        it.lookup = Le64(&d[p]);
        it.asset = Le64(&d[p + 8]);
        it.asset_param = Le64(&d[p + 0x10]);
        it.key_name = Le64(&d[p + 0x18]);
        it.package = Le64(&d[p + 0x20]);
        // +0x28 = 0, +0x2C = blob size.
        it.size = Le32(&d[p + 0x2C]);
        it.records_size = Le32(&d[p + 0x30]);
        std::memcpy(it.ckey.b, &d[p + 0x38], 16);
        out.items.push_back(it);
    }
    return true;
}

bool ParsePackage(const Bytes& d, Package& out, Error* err) {
    out = {};
    if (d.size() < 0x50) return Fail(err, "package: short");
    out.records_offset = Le64(&d[0]);
    out.references_offset = Le64(&d[8]);
    out.deps_offset = Le64(&d[0x10]);
    out.record_count = Le32(&d[0x18]);
    out.records_gz_size = Le32(&d[0x1C]);
    out.reference_count = Le32(&d[0x20]);
    out.dep_count = Le32(&d[0x24]);
    out.records_size = Le32(&d[0x28]);
    out.bundle = Le64(&d[0x2C]);
    out.bundle_size = Le32(&d[0x34]);
    out.unk38 = Le64(&d[0x38]);
    std::memcpy(out.bundle_ckey.b, &d[0x40], 16);
    if (out.records_offset + out.records_gz_size > d.size()) return Fail(err, "package: records out of range");
    if (out.reference_count && out.references_offset + size_t(out.reference_count) * 8 > d.size())
        return Fail(err, "package: references out of range");
    if (out.dep_count && out.deps_offset + size_t(out.dep_count) * 4 > d.size()) return Fail(err, "package: deps out of range");
    if (out.record_count) {
        Bytes r;
        if (!Inflate(&d[out.records_offset], out.records_gz_size, true, r, err)) return false;
        if (r.size() != size_t(out.record_count) * 36) return Fail(err, "package: record count mismatch");
        out.records.resize(out.record_count);
        for (uint32_t i = 0; i < out.record_count; ++i) {
            const uint8_t* q = &r[size_t(i) * 36];
            auto& rec = out.records[i];
            rec.guid = Le64(q);
            rec.size = Le32(q + 8);
            rec.flags = Le32(q + 12);
            rec.offset = Le32(q + 16);
            std::memcpy(rec.ckey.b, q + 20, 16);
        }
    }
    for (uint32_t i = 0; i < out.reference_count; ++i) out.references.push_back(Le64(&d[out.references_offset + i * 8]));
    for (uint32_t i = 0; i < out.dep_count; ++i) out.deps.push_back(Le32(&d[out.deps_offset + i * 4]));
    return true;
}

} // namespace content::ow08
