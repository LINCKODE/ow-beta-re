// Tests against the real install (game/owbetaclient): all 19 map data blobs; skipped when it is absent.
#include <algorithm>
#include <cmath>
#include <cstring>
#include <filesystem>
#include <map>
#include <memory>
#include <set>

#include "content/store.h"
#include "maps/mapdata.h"
#include "stu/reader.h"
#include "stu/registry.h"
#include "test.h"

using namespace maps;

namespace {

content::Store* Install() {
    static std::unique_ptr<content::Store> store;
    static bool tried = false;
    if (!tried) {
        tried = true;
        if (!std::filesystem::exists(std::string(OW_INSTALL) + "/.build.info")) {
            std::printf("  (no install at %s: skipped)\n", OW_INSTALL);
            return nullptr;
        }
        content::Error err;
        store = content::OpenOw08Install(OW_INSTALL, {}, &err);
        if (!store) std::printf("  open failed: %s\n", err.message.c_str());
        CHECK(store != nullptr);
    }
    return store.get();
}

std::vector<Guid> MapGuids(content::Store* s) {
    std::vector<Guid> v;
    for (auto& r : s->Resources())
        if (content::GuidType(r.guid) == 0x002) v.push_back(r.guid);
    return v;
}

bool StuOk(std::span<const uint8_t> b, size_t* instances = nullptr) {
    stu::Document doc;
    std::string e;
    if (!stu::Document::Parse(b, stu::Registry::Client(), doc, e)) return false;
    if (instances) *instances = doc.instances().size();
    return doc.AllCrcOk();
}

const Record* Placed(const MapData& m, uint32_t idx) { return m.ByPlaced(idx); }

bool Near(float a, float b) { return std::fabs(a - b) < 0.01f; }

}  // namespace

// Every map parses; every record of every type decodes; every entity's instance data and the two AI blobs
// load with the client's STU registry (all layout CRCs match); sound-space links name sound volumes.
TEST(install_all_maps_parse) {
    auto* s = Install();
    if (!s) return;
    auto guids = MapGuids(s);
    CHECK_EQ(guids.size(), (size_t)19);
    std::map<int, size_t> per_type;
    size_t blobs = 0, blob_fail = 0, recs = 0, stale = 0;
    for (Guid g : guids) {
        Bytes b;
        CHECK(s->Read(g, b));
        Error err;
        auto m = Parse(b, &err);
        if (!m) std::printf("  %016llx: %s\n", (unsigned long long)g, err.message.c_str());
        CHECK(m.has_value());
        if (!m) continue;
        recs += m->records.size();
        for (auto& r : m->records) {
            per_type[r.type]++;
            for (auto& id : r.instance_data) {
                blobs++;
                if (!StuOk(std::span<const uint8_t>(b.data() + id.offset, id.size))) blob_fail++;
            }
        }
        CHECK(m->header.nav != 0 && m->header.tactical != 0);
        CHECK(StuOk(std::span<const uint8_t>(b.data() + m->header.nav, m->nav_size)));
        CHECK(StuOk(std::span<const uint8_t>(b.data() + m->header.tactical, m->tactical_size)));
        // links name sound volumes of the map or of none (stale entries: 0223, 0165, 01D4, 0664, 067A) and
        // a STUSoundSpace or 0
        for (auto& l : m->sound_spaces) {
            const Record* v = m->ByUuid(l.volume);
            if (v) CHECK(v->type == kSoundVolume);
            else stale++;
            if (l.space) CHECK_EQ(GuidType(l.space), 0x055u);
        }
        CHECK_EQ(m->collision.meshes.size(), (size_t)1);  // one static triangle mesh per map
        for (auto& r : m->records) {
            if (r.type == kEntity) CHECK_EQ(GuidType(r.primary), 0x003u);
            if (r.type == kModel || r.type == kPlacedModel || r.type == kModelGroup) CHECK_EQ(GuidType(r.primary), 0x00Cu);
            if (r.type == kEffect) CHECK_EQ(GuidType(r.primary), 0x00Du);
            if (r.type == kSoundEmitter) CHECK_EQ(GuidType(r.primary), 0x02Cu);
            if (r.type == kMapText) CHECK_EQ(GuidType(r.secondary), 0x0AAu);
        }
        if (m->header.env.lighting) CHECK_EQ(GuidType(m->header.env.lighting), 0x00Eu);
        CHECK_EQ(GuidType(m->header.env.sound_space), 0x055u);
    }
    std::printf("  %zu records, %zu instance-data blobs (%zu failed);", recs, blobs, blob_fail);
    for (auto& [t, n] : per_type) std::printf(" %d:%zu", t, n);
    std::printf("\n");
    CHECK_EQ(blob_fail, (size_t)0);
    CHECK_EQ(per_type.size(), (size_t)14);  // 1-4, 6-15 (0 and 5 never occur)
    CHECK_EQ(recs, (size_t)31970);
    CHECK_EQ(stale, (size_t)34);
}

// The collision mesh of every map, checked as the physics world reads it (docs/Maps.md §2.6, T062): +0x00 BVH
// nodes, +0x10 triangles. Every triangle index and wing vertex is in range; the BVH is a pre-order tree (left = next
// node, right = this + the +0x18 offset) whose leaves cover every triangle exactly once, whose boxes contain their triangles and whose
// masks are the OR of their triangles' masks.
TEST(install_collision_meshes) {
    auto* s = Install();
    if (!s) return;
    size_t checked = 0;
    for (Guid g : MapGuids(s)) {
        Bytes b;
        CHECK(s->Read(g, b));
        auto m = Parse(b);
        CHECK(m.has_value());
        if (!m || m->collision.meshes.empty()) continue;
        const CollisionMesh& c = m->collision.meshes[0];
        CHECK(c.tri_count > c.vert_count && c.tri_count > c.node_count);
        size_t bad_tri = 0;
        for (uint32_t i = 0; i < c.tri_count; i++) {
            CollisionTriangle t = CollisionTri(b, c, i);
            for (int k = 0; k < 3; k++) {
                if (t.v[k] < 0 || (uint32_t)t.v[k] >= c.vert_count) bad_tri++;
                if (t.wing[k] < -1 || t.wing[k] >= (int32_t)c.vert_count) bad_tri++;
            }
        }
        CHECK_EQ(bad_tri, (size_t)0);
        std::vector<uint8_t> seen(c.tri_count);
        size_t bad_node = 0, bad_box = 0, bad_mask = 0, dup = 0;
        uint32_t max_depth = 0;
        struct Item { uint32_t i, depth; };
        std::vector<Item> stack{{0, 1}};
        size_t visited = 0;
        while (!stack.empty() && visited <= c.node_count) {
            Item it = stack.back();
            stack.pop_back();
            visited++;
            max_depth = std::max(max_depth, it.depth);
            CollisionNode n = CollisionBvhNode(b, c, it.i);
            if (n.tri_count == 0) {
                uint64_t right = (uint64_t)it.i + n.index;
                if (n.index < 2 || right >= c.node_count) {
                    bad_node++;
                    continue;
                }
                CollisionNode l = CollisionBvhNode(b, c, it.i + 1), r = CollisionBvhNode(b, c, (uint32_t)right);
                if ((l.mask | r.mask) != n.mask) bad_mask++;
                stack.push_back({it.i + 1, it.depth + 1});
                stack.push_back({(uint32_t)right, it.depth + 1});
                continue;
            }
            if (n.index < 0 || (uint64_t)n.index + n.tri_count > c.tri_count) {
                bad_node++;
                continue;
            }
            uint16_t mask = 0;
            for (uint32_t k = 0; k < n.tri_count; k++) {
                CollisionTriangle t = CollisionTri(b, c, n.index + k);
                if (seen[n.index + k]++) dup++;
                mask |= t.mask;
                for (int j = 0; j < 3; j++) {
                    Vec3 v = CollisionVertex(b, c, t.v[j]);
                    const float e = 0.01f;
                    if (v.x < n.min.x - e || v.y < n.min.y - e || v.z < n.min.z - e || v.x > n.max.x + e ||
                        v.y > n.max.y + e || v.z > n.max.z + e)
                        bad_box++;
                }
            }
            if (mask != n.mask) bad_mask++;
        }
        size_t missing = 0;
        for (uint8_t x : seen) missing += x == 0;
        if (bad_node || bad_box || bad_mask || dup || missing || visited != c.node_count)
            std::printf("  %016llx: nodes %u visited %zu, bad node %zu box %zu mask %zu, dup %zu missing %zu\n",
                        (unsigned long long)g, c.node_count, visited, bad_node, bad_box, bad_mask, dup, missing);
        CHECK_EQ(visited, (size_t)c.node_count);
        CHECK_EQ(bad_node + bad_box + bad_mask + dup + missing, (size_t)0);
        CHECK_EQ(max_depth, c.depth);
        checked++;
    }
    CHECK_EQ(checked, (size_t)19);
}

// Every convex hull's topology (layout in mapdata.h): twins pair up, each face's loop closes through `next` on its own
// face, the vertices lie on (inside) the face planes, and V - E + F = 2.
TEST(install_collision_hulls) {
    auto* s = Install();
    if (!s) return;
    size_t hulls = 0, bad = 0;
    for (Guid g : MapGuids(s)) {
        Bytes b;
        CHECK(s->Read(g, b));
        auto m = Parse(b);
        if (!m) continue;
        for (auto& h : m->collision.hulls) {
            hulls++;
            auto u8 = [&](uint64_t o) { return b[o]; };
            auto f32 = [&](uint64_t o) { float f; std::memcpy(&f, b.data() + o, 4); return f; };
            bool ok = h.verts < 256 && h.faces < 256 && h.half_edges < 256 && h.verts - h.half_edges / 2 + h.faces == 2 &&
                      h.ptr[0] + 12ull * h.verts <= b.size() && h.ptr[1] + 16ull * h.faces <= b.size() &&
                      h.ptr[2] + 4ull * h.half_edges <= b.size() && h.ptr[3] + h.faces <= b.size();
            for (uint32_t e = 0; ok && e < h.half_edges; e++) {
                uint64_t o = h.ptr[2] + 4 * e;
                int twin = (int)e + (int8_t)u8(o);
                ok = twin >= 0 && twin < (int)h.half_edges && (int)(twin + (int8_t)u8(h.ptr[2] + 4 * twin)) == (int)e &&
                     u8(o + 1) < h.verts && u8(o + 2) < h.faces && u8(o + 3) < h.half_edges;
            }
            size_t visited = 0;
            for (uint32_t f = 0; ok && f < h.faces; f++) {
                uint32_t first = u8(h.ptr[3] + f), e = first, n = 0;
                float nx = f32(h.ptr[1] + 16 * f), ny = f32(h.ptr[1] + 16 * f + 4), nz = f32(h.ptr[1] + 16 * f + 8),
                      d = f32(h.ptr[1] + 16 * f + 12);
                do {
                    uint64_t o = h.ptr[2] + 4 * e;
                    if (u8(o + 2) != f || n++ > h.half_edges) { ok = false; break; }
                    uint64_t v = h.ptr[0] + 12ull * u8(o + 1);
                    if (std::fabs(nx * f32(v) + ny * f32(v + 4) + nz * f32(v + 8) - d) > 0.01f) ok = false;
                    e = u8(o + 3);
                } while (e != first);
                visited += n;
            }
            if (visited != h.half_edges) ok = false;
            if (!ok) {
                bad++;
                std::printf("  %016llx: hull at %#x bad\n", (unsigned long long)g, h.at);
            }
        }
    }
    std::printf("  %zu hulls\n", hulls);
    CHECK(hulls > 0);
    CHECK_EQ(bad, (size_t)0);
}

// Anubis against the placed indexes measured live (docs/MatchLogic.md §8.1) and its header.
TEST(install_anubis) {
    auto* s = Install();
    if (!s) return;
    Bytes b;
    CHECK(s->Read(0x080000000000005Bull, b));
    auto m = Parse(b);
    CHECK(m.has_value());
    if (!m) return;
    CHECK_EQ(m->placed, 1345u);
    const Record* door = Placed(*m, 0x1d7);
    CHECK(door && door->type == kEntity && door->primary == 0x040000000000005Bull);
    if (door) CHECK(Near(door->xf.pos.x, 3.27f) && Near(door->xf.pos.y, 2.77f) && Near(door->xf.pos.z, -20.59f));
    const Record* d2 = Placed(*m, 0x1d8);
    CHECK(d2 && d2->primary == 0x0400000000000133ull && Near(d2->xf.pos.x, 18.74f));
    CHECK_EQ(m->header.env.gamemode_def, 0x0400000000000039ull);  // brain B (docs/MatchLogic.md §10.5)
    CHECK_EQ(m->header.env.lighting, 0x0B00000000000062ull);
    CHECK_EQ(m->collision.capsules.size(), (size_t)0x76);
    CHECK_EQ(m->collision.hulls.size(), (size_t)0x3b);
    CHECK_EQ(m->collision.meshes[0].tri_count, 522648u);  // docs/Maps.md §2.6 (T062)
    CHECK_EQ(m->collision.meshes[0].node_count, 349549u);
    CHECK_EQ(m->collision.meshes[0].vert_count, 297924u);
    CHECK_EQ(m->sound_spaces.size(), (size_t)22);
    size_t lights = 0;
    for (auto& r : m->records) lights += r.type == kLight;
    CHECK_EQ(lights, (size_t)57);  // = the 57 inline {01 05} entities of the map-load trace
}

// The writer on a real map: move the Anubis ship door, then add a copy of it; everything else must be
// byte-identical (records, blobs, collision, AI blobs) at its relocated position.
TEST(install_anubis_edit) {
    auto* s = Install();
    if (!s) return;
    Bytes b;
    CHECK(s->Read(0x080000000000005Bull, b));
    auto m = Parse(b);
    if (!m) return;
    const Record door = *Placed(*m, 0x1d7);
    Bytes e = b;
    Transform x = door.xf;
    x.pos.y += 10;
    CHECK(SetTransform(e, door, x));
    auto m1 = Parse(e);
    CHECK(m1 && Near(m1->ByPlaced(0x1d7)->xf.pos.y, door.xf.pos.y + 10));

    Uuid u;
    u.b.fill(0x42);
    Error err;
    Bytes a = b;
    CHECK(AddEntity(a, *m, door, u, x, &err));
    auto m2 = Parse(a, &err);
    if (!m2) std::printf("  %s\n", err.message.c_str());
    CHECK(m2.has_value());
    if (!m2) return;
    CHECK_EQ(m2->records.size(), m->records.size() + 1);
    CHECK_EQ(m2->placed, m->placed + 1);
    // every old record keeps its contents (entity pairs aside) and its placed index up to the entity section
    size_t same = 0, diff = 0;
    for (size_t i = 0, j = 0; i < m->records.size(); i++, j++) {
        const Record& r = m->records[i];
        if (r.type > kEntity && j == i) j++;  // the new record sits at the end of the entity section
        const Record& q = m2->records[j];
        size_t n = r.type == kEntity ? 0x80 : r.size;
        if (r.size == q.size && std::memcmp(b.data() + r.offset, a.data() + q.offset, n) == 0) same++;
        else diff++;
        for (size_t k = 0; k < r.instance_data.size(); k++)
            if (std::memcmp(b.data() + r.instance_data[k].offset, a.data() + q.instance_data[k].offset,
                            r.instance_data[k].size) != 0)
                diff++;
    }
    CHECK_EQ(diff, (size_t)0);
    CHECK_EQ(same, m->records.size());
    const Record* added = m2->ByUuid(u);
    CHECK(added && added->type == kEntity && added->primary == door.primary && Near(added->xf.pos.y, x.pos.y));
    CHECK(added && added->placed == (int32_t)(m->header.counts[8] + m->header.counts[9] + m->header.counts[10] +
                                               m->header.counts[11]));
    CHECK(std::memcmp(b.data() + m->header.nav, a.data() + m2->header.nav, m->nav_size) == 0);
    CHECK(std::memcmp(b.data() + m->collision.meshes[0].verts, a.data() + m2->collision.meshes[0].verts,
                      16ull * m->collision.meshes[0].vert_count) == 0);
    CHECK_EQ(m2->sound_spaces.size(), m->sound_spaces.size());
}
