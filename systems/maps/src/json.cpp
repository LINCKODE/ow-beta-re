// JSON of a parsed map data (map_dump, the C API, the Go wrapper). GUIDs are "0x%016x" strings.
#include <cmath>
#include <cstdio>

#include "maps/mapdata.h"

namespace maps {

namespace {

struct Out {
    std::string s;
    void raw(const char* t) { s += t; }
    void str(const std::string& t) {
        s += '"';
        for (char c : t) {
            if (c == '"' || c == '\\') {
                s += '\\';
                s += c;
            } else if ((unsigned char)c < 0x20) {
                char b[8];
                std::snprintf(b, sizeof b, "\\u%04x", c);
                s += b;
            } else {
                s += c;
            }
        }
        s += '"';
    }
    void key(const char* k) {
        str(k);
        s += ':';
    }
    void num(double v) {
        if (!std::isfinite(v)) {
            s += "null";
            return;
        }
        char b[32];
        std::snprintf(b, sizeof b, "%.9g", v);
        s += b;
    }
    void u(uint64_t v) { s += std::to_string(v); }
    void guid(uint64_t g) {
        char b[24];
        std::snprintf(b, sizeof b, "\"0x%016llx\"", (unsigned long long)g);
        s += b;
    }
    void hex(uint64_t v) {
        char b[24];
        std::snprintf(b, sizeof b, "\"0x%llx\"", (unsigned long long)v);
        s += b;
    }
    void vec(const float* f, int n) {
        s += '[';
        for (int i = 0; i < n; i++) {
            if (i) s += ',';
            num(f[i]);
        }
        s += ']';
    }
    void v3(const Vec3& v) { vec(&v.x, 3); }
    void v4(const Vec4& v) { vec(&v.x, 4); }
    void q(const Quat& v) { vec(&v.x, 4); }
};

void Xf(Out& o, const Transform& x) {
    o.raw("{");
    o.key("pos");
    o.v3(x.pos);
    o.raw(",");
    o.key("scale");
    o.v3(x.scale);
    o.raw(",");
    o.key("rot");
    o.q(x.rot);
    o.raw("}");
}

void FieldJson(Out& o, const Field& f) {
    switch (f.kind) {
    case Field::U32: o.u(f.u); break;
    case Field::I32: o.s += std::to_string((int64_t)f.u); break;
    case Field::F32: o.num(f.f[0]); break;
    case Field::Hex32:
    case Field::U64: o.hex(f.u); break;
    case Field::Guid64: o.guid(f.u); break;
    case Field::Vec3f: o.vec(f.f.data(), 3); break;
    case Field::Vec4f: o.vec(f.f.data(), 4); break;
    case Field::Text: o.str(f.text); break;
    }
}

}  // namespace

std::string ToJson(const MapData& m, bool records, bool fields) {
    Out o;
    const Header& h = m.header;
    o.raw("{");
    o.key("size");
    o.u(m.size);
    o.raw(",");
    o.key("header");
    o.raw("{");
    o.key("bounds");
    o.raw("[");
    for (int i = 0; i < 5; i++) {
        if (i) o.raw(",");
        o.raw("{");
        o.key("min");
        o.v4(h.bounds[i].min);
        o.raw(",");
        o.key("max");
        o.v4(h.bounds[i].max);
        o.raw("}");
    }
    o.raw("],");
    o.key("camera_pos");
    o.v4(h.camera_pos);
    o.raw(",");
    o.key("camera_rot");
    o.vec(h.camera_rot.data(), 16);
    o.raw(",");
    const Environment& e = h.env;
    o.key("env");
    o.raw("{");
    struct {
        const char* k;
        Guid g;
    } gs[] = {{"gamemode_def", e.gamemode_def}, {"texture_f8", e.texture_f8}, {"lighting", e.lighting},
              {"texture_108", e.texture_108}, {"sky_model", e.sky_model},    {"sky_look", e.sky_look},
              {"sound_space", e.sound_space}, {"texture_128", e.texture_128}, {"texture_130", e.texture_130},
              {"name", e.name},               {"texture_180", e.textures_180[0]}, {"texture_188", e.textures_180[1]},
              {"texture_190", e.textures_180[2]}};
    for (auto& g : gs) {
        o.key(g.k);
        o.guid(g.g);
        o.raw(",");
    }
    o.key("sun_dir");
    o.v3(e.sun_dir);
    o.raw(",");
    o.key("raw_140");
    o.vec(e.raw.data(), 44);
    o.raw("},");
    o.key("u1f0");
    o.hex(h.u1f0);
    o.raw(",");
    o.key("u1f4");
    o.hex(h.u1f4);
    o.raw(",");
    o.key("last_phase");
    o.u(h.last_phase);
    o.raw(",");
    o.key("record_count");
    o.u(h.record_count);
    o.raw(",");
    o.key("u2d0");
    o.hex(h.u2d0);
    o.raw(",");
    o.key("sections");
    o.raw("[");
    bool first = true;
    for (int t = 0; t < kNumRecordTypes; t++) {
        if (!h.counts[t]) continue;
        if (!first) o.raw(",");
        first = false;
        o.raw("{");
        o.key("type");
        o.u(t);
        o.raw(",");
        o.key("name");
        o.str(RecordTypeName(t));
        o.raw(",");
        o.key("offset");
        o.hex(h.offsets[t]);
        o.raw(",");
        o.key("count");
        o.u(h.counts[t]);
        o.raw(",");
        o.key("size");
        o.u(h.sizes[t]);
        o.raw("}");
    }
    o.raw("],");
    o.key("nav");
    o.hex(h.nav);
    o.raw(",");
    o.key("nav_size");
    o.u(m.nav_size);
    o.raw(",");
    o.key("tactical");
    o.hex(h.tactical);
    o.raw(",");
    o.key("tactical_size");
    o.u(m.tactical_size);
    o.raw(",");
    o.key("blob_begin");
    o.hex(m.blob_begin);
    o.raw(",");
    o.key("blob_end");
    o.hex(m.blob_end);
    o.raw("},");
    o.key("placed");
    o.u(m.placed);
    o.raw(",");
    // collision summary
    o.key("collision");
    o.raw("{");
    o.key("spheres");
    o.u(m.collision.spheres.size());
    o.raw(",");
    o.key("capsules");
    o.u(m.collision.capsules.size());
    o.raw(",");
    o.key("hulls");
    o.u(m.collision.hulls.size());
    o.raw(",");
    o.key("meshes");
    o.raw("[");
    for (size_t i = 0; i < m.collision.meshes.size(); i++) {
        auto& c = m.collision.meshes[i];
        if (i) o.raw(",");
        o.raw("{");
        o.key("tris");
        o.u(c.tri_count);
        o.raw(",");
        o.key("verts");
        o.u(c.vert_count);
        o.raw(",");
        o.key("bvh_nodes");
        o.u(c.node_count);
        o.raw(",");
        o.key("bvh_depth");
        o.u(c.depth);
        o.raw(",");
        o.key("tri_offset");
        o.hex(c.tris);
        o.raw(",");
        o.key("vert_offset");
        o.hex(c.verts);
        o.raw(",");
        o.key("bvh_offset");
        o.hex(c.bvh);
        o.raw("}");
    }
    o.raw("]},");
    o.key("sound_spaces");
    o.raw("[");
    for (size_t i = 0; i < m.sound_spaces.size(); i++) {
        if (i) o.raw(",");
        o.raw("{");
        o.key("volume");
        o.str(m.sound_spaces[i].volume.Hex());
        o.raw(",");
        o.key("space");
        o.guid(m.sound_spaces[i].space);
        o.raw("}");
    }
    o.raw("]");
    if (records) {
        o.raw(",");
        o.key("records");
        o.raw("[");
        for (size_t i = 0; i < m.records.size(); i++) {
            const Record& r = m.records[i];
            if (i) o.raw(",");
            o.raw("{");
            o.key("offset");
            o.hex(r.offset);
            o.raw(",");
            o.key("size");
            o.u(r.size);
            o.raw(",");
            o.key("type");
            o.u(r.type);
            o.raw(",");
            o.key("flags");
            o.u(r.flags);
            o.raw(",");
            o.key("phase");
            o.u(r.phase);
            o.raw(",");
            o.key("uuid");
            o.str(r.uuid.Hex());
            o.raw(",");
            o.key("index");
            o.u(r.index);
            o.raw(",");
            o.key("placed");
            o.s += std::to_string(r.placed);
            if (r.primary) {
                o.raw(",");
                o.key("primary");
                o.guid(r.primary);
            }
            if (r.secondary) {
                o.raw(",");
                o.key("secondary");
                o.guid(r.secondary);
            }
            if (r.has_xf) {
                o.raw(",");
                o.key("xf");
                Xf(o, r.xf);
                o.raw(",");
                o.key("xf_writable");
                o.raw(r.xf_layout ? "true" : "false");
            }
            if (!r.instance_data.empty()) {
                o.raw(",");
                o.key("instance_data");
                o.raw("[");
                for (size_t k = 0; k < r.instance_data.size(); k++) {
                    if (k) o.raw(",");
                    o.raw("{");
                    o.key("slot");
                    o.u(r.instance_data[k].slot);
                    o.raw(",");
                    o.key("offset");
                    o.hex(r.instance_data[k].offset);
                    o.raw(",");
                    o.key("size");
                    o.u(r.instance_data[k].size);
                    o.raw("}");
                }
                o.raw("]");
            }
            if (!r.instances.empty()) {
                o.raw(",");
                o.key("instances");
                o.raw("[");
                for (size_t k = 0; k < r.instances.size(); k++) {
                    if (k) o.raw(",");
                    o.raw("{");
                    o.key("look");
                    o.guid(r.instances[k].look);
                    o.raw(",");
                    o.key("xf");
                    Xf(o, r.instances[k].xf);
                    o.raw("}");
                }
                o.raw("]");
            }
            if (!r.shapes.empty()) {
                o.raw(",");
                o.key("shapes");
                o.raw("[");
                for (size_t k = 0; k < r.shapes.size(); k++) {
                    const Shape& sh = r.shapes[k];
                    if (k) o.raw(",");
                    o.raw("{");
                    o.key("kind");
                    o.str(sh.kind == Shape::Box ? "box" : sh.kind == Shape::Sphere ? "sphere" : "capsule");
                    o.raw(",");
                    o.key("pos");
                    o.v3(sh.pos);
                    o.raw(",");
                    o.key("rot");
                    o.q(sh.rot);
                    o.raw(",");
                    o.key("half");
                    o.v3(sh.half);
                    o.raw("}");
                }
                o.raw("]");
            }
            if (fields) {
                o.raw(",");
                o.key("fields");
                o.raw("[");
                for (size_t k = 0; k < r.fields.size(); k++) {
                    if (k) o.raw(",");
                    o.raw("[");
                    o.str(r.fields[k].name);
                    o.raw(",");
                    o.hex(r.fields[k].at);
                    o.raw(",");
                    FieldJson(o, r.fields[k]);
                    o.raw("]");
                }
                o.raw("]");
            }
            o.raw("}");
        }
        o.raw("]");
    }
    o.raw("}");
    return o.s;
}

}  // namespace maps
