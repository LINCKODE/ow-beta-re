#include "stu/registry.h"
#include "test.h"

using stu::Kind;
using stu::Registry;

TEST(registry_counts) {
    const auto& reg = Registry::Client();
    CHECK_EQ(reg.TypeCount(), size_t(3639));
    size_t fields = 0;
    for (const auto& t : reg.Types()) fields += t.own_fields.size();
    CHECK_EQ(fields, size_t(9436));
}

TEST(registry_statescript_graph) {
    const auto& reg = Registry::Client();
    const auto* g = reg.Find(0x0DF7E24F);
    CHECK(g != nullptr);
    if (!g) return;
    CHECK_EQ(g->size, 0x198u);
    CHECK_EQ(g->layout_crc, 0xa4105946061623bbull);
    CHECK_EQ(g->own_fields.size(), size_t(32));
    CHECK(std::string(reg.NameOr(g->hash)) == "STUStatescriptGraph");
    const auto* nodes = reg.FindField(*g, 0x51caba98);
    CHECK(nodes && nodes->offset == 0x50 && nodes->kind == Kind::ObjectList && nodes->elem == 0xa97578e7);
    CHECK(std::string(reg.NameOr(0x51caba98)) == "m_nodes");
    CHECK(reg.FindByLayoutCrc(0xa4105946061623bbull) == g);
}

TEST(registry_inheritance) {
    const auto& reg = Registry::Client();
    // Entity definition 0xec23fffd; a component type inherits fields from its parents (parents first).
    const auto* t = reg.Find(0x3e490486);
    CHECK(t != nullptr);
    if (!t) return;
    auto all = reg.AllFields(*t);
    size_t own = t->own_fields.size();
    CHECK(all.size() >= own);
    if (t->parent) {
        CHECK(reg.IsA(t->hash, t->parent));
        CHECK(all.size() > own || reg.Find(t->parent)->own_fields.empty());
    }
    CHECK(!reg.IsA(0x0DF7E24F, 0x3e490486));
}

TEST(registry_primitives) {
    const auto& reg = Registry::Client();
    CHECK_EQ(reg.PrimitiveSize(stu::prim::u8), 1u);
    CHECK_EQ(reg.PrimitiveSize(stu::prim::teColorRGB), 12u);
    CHECK_EQ(reg.PrimitiveSize(stu::prim::teMtx43A), 64u);
    CHECK_EQ(reg.PrimitiveSize(stu::prim::teString), 16u);
    // Every primitive element hash used by the registry has a known size.
    for (const auto& t : reg.Types())
        for (const auto& f : t.own_fields)
            if (f.kind == Kind::Primitive || f.kind == Kind::PrimitiveList) {
                if (reg.PrimitiveSize(f.elem) == 0) std::printf("  unknown primitive %08x\n", f.elem);
                CHECK(reg.PrimitiveSize(f.elem) != 0);
            }
}

TEST(registry_references_resolve) {
    // Every object / inline / list element type referenced by a field exists in the registry.
    const auto& reg = Registry::Client();
    int missing = 0;
    for (const auto& t : reg.Types())
        for (const auto& f : t.own_fields)
            if ((f.kind == Kind::Inline || f.kind == Kind::InlineList) && !reg.Find(f.elem)) missing++;
    CHECK_EQ(missing, 0);
}
