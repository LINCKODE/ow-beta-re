// Synthetic map data: parse, edit, insert.
#include <cstring>

#include "maps/mapdata.h"
#include "test.h"

using namespace maps;

namespace {

template <typename T>
void Put(Bytes& d, size_t off, T v) {
    std::memcpy(d.data() + off, &v, sizeof v);
}
template <typename T>
T Get(const Bytes& d, size_t off) {
    T v;
    std::memcpy(&v, d.data() + off, sizeof v);
    return v;
}

// header | entity (type 11, one instance-data pair) | pad | STUD blob (0x20) | light (type 9) | sound table
Bytes Synthetic() {
    Bytes d(0x4d0, 0);
    const uint32_t ent = 0x320, blob = 0x3b0, light = 0x3d0, table = 0x4a0;
    Put<uint32_t>(d, 0x20c, 2);
    Put<uint32_t>(d, 0x210 + 4 * 11, ent);
    Put<uint32_t>(d, 0x250 + 4 * 11, 1);
    Put<uint32_t>(d, 0x290 + 4 * 11, 0x88);
    Put<uint32_t>(d, 0x210 + 4 * 9, light);
    Put<uint32_t>(d, 0x250 + 4 * 9, 1);
    Put<uint32_t>(d, 0x290 + 4 * 9, 0xd0);
    Put<uint64_t>(d, 0x308, 1);
    Put<uint64_t>(d, 0x310, table);
    // entity
    for (int i = 0; i < 16; i++) d[ent + i] = (uint8_t)(0xA0 + i);
    d[ent + 0x10] = 1;
    d[ent + 0x13] = 11;
    Put<uint32_t>(d, ent + 0x14, 0x88);
    Put<uint64_t>(d, ent + 0x18, 0x0400000000000167ull);
    Put<float>(d, ent + 0x20, 1.f);
    Put<float>(d, ent + 0x24, 2.f);
    Put<float>(d, ent + 0x28, 3.f);
    for (int i = 0; i < 3; i++) Put<float>(d, ent + 0x2c + 4 * i, 1.f);
    Put<float>(d, ent + 0x44, 1.f);
    Put<uint32_t>(d, ent + 0x48, 1);
    Put<uint32_t>(d, ent + 0x80, 2);
    Put<uint32_t>(d, ent + 0x84, blob - (ent + 0x80));
    std::memcpy(d.data() + blob, "DUTS", 4);
    Put<uint32_t>(d, blob + 4, 3);
    // light
    d[light + 0x10] = 2;
    d[light + 0x13] = 9;
    Put<uint32_t>(d, light + 0x14, 0xd0);
    Put<float>(d, light + 0x24, 1.f);  // quat w
    Put<float>(d, light + 0x28, 5.f);
    Put<float>(d, light + 0xa0, 7.5f);
    // sound-space link
    for (int i = 0; i < 16; i++) d[table + i] = (uint8_t)i;
    Put<uint64_t>(d, table + 16, 0x02a0000000000016ull);
    return d;
}

}  // namespace

TEST(guid_type) {
    CHECK_EQ(GuidType(0x0400000000000039ull), 0x003u);
    CHECK_EQ(GuidType(0x080000000000005Bull), 0x002u);
    CHECK_EQ(GuidType(0x0d00000000000f81ull), 0x00Cu);
    CHECK_EQ(GuidType(0x02a0000000000016ull), 0x055u);
}

TEST(synthetic_parse) {
    Bytes d = Synthetic();
    Error err;
    auto m = Parse(d, &err);
    if (!m) std::printf("  %s\n", err.message.c_str());
    CHECK(m.has_value());
    if (!m) return;
    CHECK_EQ(m->records.size(), (size_t)2);
    CHECK_EQ(m->placed, 2u);
    const Record& l = m->records[0];
    CHECK_EQ((int)l.type, 9);
    CHECK_EQ(l.placed, 0);
    CHECK(l.has_xf && l.xf.pos.x == 5.f);
    const Record& e = m->records[1];
    CHECK_EQ((int)e.type, 11);
    CHECK_EQ(e.placed, 1);
    CHECK_EQ(e.primary, 0x0400000000000167ull);
    CHECK_EQ(e.instance_data.size(), (size_t)1);
    CHECK_EQ(e.instance_data[0].offset, 0x3b0u);
    CHECK_EQ(e.instance_data[0].size, 0x20u);  // up to the light section
    CHECK_EQ(m->sound_spaces.size(), (size_t)1);
    CHECK(m->ByPlaced(1) == &m->records[1]);
    CHECK(!ToJson(*m, true, true).empty());
}

TEST(synthetic_malformed) {
    Bytes d = Synthetic();
    Put<uint32_t>(d, 0x320 + 0x14, 0x5000);  // entity size past the section
    CHECK(!Parse(d).has_value());
    d = Synthetic();
    Put<uint32_t>(d, 0x20c, 3);  // counts don't sum
    CHECK(!Parse(d).has_value());
    d = Synthetic();
    std::memcpy(d.data() + 0x3b0, "XXXX", 4);  // blob is not STUD
    CHECK(!Parse(d).has_value());
    d.resize(0x100);
    CHECK(!Parse(d).has_value());
}

TEST(synthetic_set_transform) {
    Bytes d = Synthetic();
    auto m = Parse(d);
    CHECK(m.has_value());
    if (!m) return;
    Transform x{{10, 20, 30}, {2, 2, 2}, {0, 0.7071f, 0, 0.7071f}};
    CHECK(SetTransform(d, m->records[1], x));
    CHECK(SetTransform(d, m->records[0], x));  // light: rot + pos only
    auto m2 = Parse(d);
    CHECK(m2.has_value());
    if (!m2) return;
    CHECK(m2->records[1].xf.pos.y == 20.f && m2->records[1].xf.scale.x == 2.f && m2->records[1].xf.rot.y > 0.7f);
    CHECK(m2->records[0].xf.pos.z == 30.f && m2->records[0].xf.rot.w > 0.7f);
    CHECK(SetDefinition(d, m2->records[1], 0x0400000000000013ull));
    CHECK_EQ(Parse(d)->records[1].primary, 0x0400000000000013ull);
    CHECK(!SetDefinition(d, m2->records[0], 1));
}

TEST(synthetic_add_entity) {
    Bytes d = Synthetic();
    auto m = Parse(d);
    CHECK(m.has_value());
    if (!m) return;
    Uuid u;
    u.b.fill(0x55);
    Transform x{{-1, -2, -3}, {1, 1, 1}, {0, 0, 0, 1}};
    Error err;
    CHECK(AddEntity(d, *m, m->records[1], u, x, &err));
    auto m2 = Parse(d, &err);
    if (!m2) std::printf("  %s\n", err.message.c_str());
    CHECK(m2.has_value());
    if (!m2) return;
    CHECK_EQ(m2->records.size(), (size_t)3);
    CHECK_EQ(m2->header.record_count, 3u);
    const Record& a = m2->records[1];
    const Record& b = m2->records[2];
    CHECK_EQ(b.placed, 2);
    CHECK(b.uuid == u);
    CHECK(b.xf.pos.x == -1.f);
    CHECK_EQ(b.primary, a.primary);
    CHECK_EQ(a.instance_data.size(), (size_t)1);
    CHECK_EQ(b.instance_data.size(), (size_t)1);
    CHECK(a.instance_data[0].offset != b.instance_data[0].offset);  // own copy of the blob
    CHECK(std::memcmp(d.data() + a.instance_data[0].offset, d.data() + b.instance_data[0].offset, 0x20) == 0);
    CHECK(m2->records[0].xf.pos.x == 5.f);  // the light moved with its section
    CHECK_EQ(m2->sound_spaces.size(), (size_t)1);
    CHECK_EQ(m2->sound_spaces[0].space, 0x02a0000000000016ull);
    CHECK_EQ(d.size() % 16, (size_t)0);
}
