#include <cstring>
#include <string>

#include "maps.h"
#include "maps/mapdata.h"

struct maps_doc {
    maps::Bytes bytes;
    maps::MapData map;
    std::string json, error;
};

namespace {

void Copy(char* dst, size_t n, const std::string& s) {
    if (!dst || !n) return;
    size_t k = std::min(n - 1, s.size());
    std::memcpy(dst, s.data(), k);
    dst[k] = 0;
}

const maps::Record* Find(maps_doc* d, uint32_t off) {
    for (auto& r : d->map.records)
        if (r.offset == off) return &r;
    return nullptr;
}

int Reparse(maps_doc* d) {
    maps::Error err;
    auto m = maps::Parse(d->bytes, &err);
    if (!m) {
        d->error = "re-parse after edit: " + err.message;
        return MAPS_ERR_PARSE;
    }
    d->map = std::move(*m);
    return MAPS_OK;
}

maps::Transform Xf(const float* pos, const float* scale, const float* rot) {
    maps::Transform x;
    x.pos = {pos[0], pos[1], pos[2]};
    if (scale) x.scale = {scale[0], scale[1], scale[2]};
    x.rot = {rot[0], rot[1], rot[2], rot[3]};
    return x;
}

}  // namespace

extern "C" {

maps_doc* maps_open(const uint8_t* data, size_t len, char* err, size_t errlen) {
    if (!data) {
        Copy(err, errlen, "null data");
        return nullptr;
    }
    auto* d = new maps_doc;
    d->bytes.assign(data, data + len);
    maps::Error e;
    auto m = maps::Parse(d->bytes, &e);
    if (!m) {
        Copy(err, errlen, e.message);
        delete d;
        return nullptr;
    }
    d->map = std::move(*m);
    return d;
}

void maps_close(maps_doc* d) { delete d; }

const char* maps_json(maps_doc* d, int records, int fields, size_t* len) {
    if (!d) return nullptr;
    d->json = maps::ToJson(d->map, records != 0, fields != 0);
    if (len) *len = d->json.size();
    return d->json.c_str();
}

const uint8_t* maps_bytes(maps_doc* d, size_t* len) {
    if (!d) return nullptr;
    if (len) *len = d->bytes.size();
    return d->bytes.data();
}

size_t maps_record_count(maps_doc* d) { return d ? d->map.records.size() : 0; }

int32_t maps_record_offset(maps_doc* d, size_t i) {
    if (!d || i >= d->map.records.size()) return -1;
    return (int32_t)d->map.records[i].offset;
}

int maps_set_transform(maps_doc* d, uint32_t off, const float* pos, const float* scale, const float* rot) {
    if (!d || !pos || !rot) return MAPS_ERR_ARG;
    const maps::Record* r = Find(d, off);
    if (!r) {
        d->error = "no record at that offset";
        return MAPS_ERR_ARG;
    }
    maps::Error e;
    maps::Transform x = Xf(pos, scale, rot);
    if (!scale) x.scale = r->xf.scale;
    if (!maps::SetTransform(d->bytes, *r, x, &e)) {
        d->error = e.message;
        return MAPS_ERR_EDIT;
    }
    return Reparse(d);
}

int maps_set_definition(maps_doc* d, uint32_t off, uint64_t def) {
    if (!d) return MAPS_ERR_ARG;
    const maps::Record* r = Find(d, off);
    if (!r) {
        d->error = "no record at that offset";
        return MAPS_ERR_ARG;
    }
    maps::Error e;
    if (!maps::SetDefinition(d->bytes, *r, def, &e)) {
        d->error = e.message;
        return MAPS_ERR_EDIT;
    }
    return Reparse(d);
}

int maps_add_entity(maps_doc* d, uint32_t src, const uint8_t* uuid16, const float* pos, const float* scale,
                    const float* rot) {
    if (!d || !uuid16 || !pos || !rot) return MAPS_ERR_ARG;
    const maps::Record* r = Find(d, src);
    if (!r) {
        d->error = "no record at that offset";
        return MAPS_ERR_ARG;
    }
    maps::Uuid u;
    std::memcpy(u.b.data(), uuid16, 16);
    maps::Transform x = Xf(pos, scale, rot);
    if (!scale) x.scale = r->xf.scale;
    maps::Error e;
    maps::Record copy = *r;  // AddEntity reallocates the bytes
    if (!maps::AddEntity(d->bytes, d->map, copy, u, x, &e)) {
        d->error = e.message;
        return MAPS_ERR_EDIT;
    }
    return Reparse(d);
}

const char* maps_error(maps_doc* d) { return d ? d->error.c_str() : ""; }

uint32_t maps_guid_type(uint64_t g) { return maps::GuidType(g); }

}  // extern "C"
