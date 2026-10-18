// Tests against the real install (game/owbetaclient); skipped when it is absent.
#include "content/ow08/ow08.h"
#include "stu/reader.h"
#include "stu/registry.h"
#include "stu/value.h"
#include "test.h"
#include "util.h"

#include <filesystem>
#include <map>

using namespace content;

namespace {

ow08::Ow08Store* Install() {
    static std::unique_ptr<Store> store;
    static bool tried = false;
    if (!tried) {
        tried = true;
        if (!std::filesystem::exists(std::string(OW_INSTALL) + "/.build.info")) {
            std::printf("  (no install at %s: skipped)\n", OW_INSTALL);
            return nullptr;
        }
        Error err;
        store = OpenOw08Install(OW_INSTALL, {}, &err);
        if (!store) std::printf("  open failed: %s\n", err.message.c_str());
        CHECK(store != nullptr);
    }
    return static_cast<ow08::Ow08Store*>(store.get());
}

bool ParseStu(const Bytes& b, stu::Document& doc) {
    std::string e;
    return stu::Document::Parse(std::span<const uint8_t>(b.data(), b.size()), stu::Registry::Client(), doc, e);
}

} // namespace

TEST(install_layers) {
    auto* s = Install();
    if (!s) return;
    auto& t = s->GetTact();
    CHECK(t.BuildInfo().at("Version") == "0.8.0.0.24919");
    CHECK(t.BuildConfig().at("build-name") == "prometheus-0_8_0_0-24919");
    CHECK_EQ(t.Root().size(), size_t(62));  // same as the client's root row count (docs/ResourceLoading.md §6)
    CHECK(t.FindRoot("Manifest/Win_SPWin_RDEV_LenUS_EExt.apm") != nullptr);
    CHECK(t.Casc().size() > 100000);
    CHECK(t.Encoding().size() > 300000);
    // 1163 + 169 packages, as the client's Asset Package manager holds live.
    size_t main = 0, speech = 0;
    for (auto& p : s->Packages()) (p.manifest.find("speech") != std::string::npos ? speech : main)++;
    CHECK_EQ(main, size_t(1163));
    CHECK_EQ(speech, size_t(169));
    CHECK(s->Resources().size() > 149000);
    // The two asset packages the client requests at startup are packages of the manifest.
    CHECK(s->Find(0x06E000000000008Dull).has_value());
    CHECK(s->Find(0x06E00000000000B5ull).has_value());
}

TEST(install_test_resources_match_fixtures) {
    auto* s = Install();
    if (!s) return;
    const std::pair<Guid, const char*> cases[] = {{0x058000000000000Full, "graph_058000000000000F.stud"},
                                                  {0x0400000000000039ull, "entitydef_0400000000000039.stud"}};
    for (auto& [guid, name] : cases) {
        Bytes b, fx;
        Error err;
        CHECK(s->Read(guid, b, &err));
        CHECK(ReadFile(std::string(STU_FIXTURES) + "/" + name, fx));
        CHECK_EQ(b.size(), fx.size());
        CHECK(ow08::Md5(b.data(), b.size()) == s->Find(guid)->ckey);
        stu::Document d1, d2;
        CHECK(ParseStu(b, d1) && d1.AllCrcOk());
        CHECK(ParseStu(fx, d2) && d2.AllCrcOk());
        CHECK_EQ(d1.instances().size(), d2.instances().size());
        // Decoded trees equal the ones of the fixture (dumped from the live client, fixup undone).
        bool same = d1.instances().size() == d2.instances().size();
        for (size_t i = 0; same && i < d1.instances().size(); ++i)
            same = stu::ToJson(d1.Decode(int(i)), stu::Registry::Client(), true) ==
                   stu::ToJson(d2.Decode(int(i)), stu::Registry::Client(), true);
        CHECK(same);
    }
}

// Every Statescript graph and entity definition in the install parses with all layout CRCs matching, and
// the upper half of each instance's type-hash slot holds the next instance's offset (-1 for the last).
TEST(install_all_graphs_and_entity_defs_parse) {
    auto* s = Install();
    if (!s) return;
    std::map<uint32_t, int> total, ok;
    int next_ok = 0, next_total = 0;
    for (auto& r : s->Resources()) {
        uint32_t type = GuidType(r.guid);
        if (type != 0x01B && type != 0x003) continue;
        ++total[type];
        Bytes b;
        stu::Document d;
        if (!s->Read(r.guid, b) || !ParseStu(b, d) || !d.AllCrcOk()) continue;
        ++ok[type];
        auto& in = d.instances();
        bool chain = true;
        for (size_t i = 0; i < in.size(); ++i)
            chain &= Le32(&b[in[i].offset + 4]) == (i + 1 < in.size() ? in[i + 1].offset : 0xFFFFFFFFu);
        next_ok += chain, ++next_total;
    }
    std::printf("  graphs %d/%d, entity definitions %d/%d parse with all CRCs; next-instance chain %d/%d\n",
                ok[0x01B], total[0x01B], ok[0x003], total[0x003], next_ok, next_total);
    CHECK(total[0x01B] > 0 && ok[0x01B] == total[0x01B]);
    CHECK(total[0x003] > 0 && ok[0x003] == total[0x003]);
    CHECK_EQ(next_ok, next_total);
}

// CKey = MD5 of the decoded bytes, for a sample across all types and packages.
TEST(install_md5_sample) {
    auto* s = Install();
    if (!s) return;
    int n = 0, good = 0;
    auto& rs = s->Resources();
    for (size_t i = 0; i < rs.size(); i += 997) {
        Bytes b;
        Error err;
        ++n;
        if (s->Read(rs[i].guid, b, &err) && ow08::Md5(b.data(), b.size()) == rs[i].ckey) ++good;
        else std::printf("  %016llX: %s\n", (unsigned long long)rs[i].guid, err.message.c_str());
    }
    std::printf("  md5 sample %d/%d\n", good, n);
    CHECK_EQ(good, n);
}
