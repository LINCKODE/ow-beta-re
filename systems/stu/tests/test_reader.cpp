#include <cstring>
#include <functional>
#include <string>

#include "stu/reader.h"
#include "test.h"

using stu::Document;
using stu::Kind;
using stu::Mode;
using stu::Registry;
using stu::Value;

namespace {

std::string Fixture(const char* name) { return std::string(STU_FIXTURES) + "/" + name; }

// Base address of a loaded image, from its .mem.json ("base": N).
uint64_t LoadedBase(const std::string& mem_path) {
    auto j = stu::ReadFile(mem_path + ".json");
    std::string s(j.begin(), j.end());
    auto p = s.find("\"base\":");
    return p == std::string::npos ? 0 : std::strtoull(s.c_str() + p + 7, nullptr, 10);
}

int CountErrors(const Value& v, std::string* first = nullptr) {
    int n = v.type == Value::Type::Error;
    if (n && first && first->empty()) *first = v.str;
    for (const auto& c : v.items) n += CountErrors(c, first);
    return n;
}

struct Loaded {
    std::vector<uint8_t> bytes;
    Document doc;
    bool ok = false;
};

Loaded Load(const char* name, Mode mode) {
    Loaded l;
    std::string path = Fixture(name);
    l.bytes = stu::ReadFile(path);
    std::string err;
    uint64_t base = mode == Mode::Loaded ? LoadedBase(path) : 0;
    l.ok = !l.bytes.empty() && Document::Parse(l.bytes, Registry::Client(), l.doc, err, mode, base);
    if (!l.ok) std::printf("  load %s: %s\n", name, err.c_str());
    return l;
}

// Every instance decodes without errors; returns the number of decoded values.
void CheckAllDecode(const Loaded& l) {
    for (size_t i = 0; i < l.doc.instances().size(); i++) {
        std::string first;
        int errs = CountErrors(l.doc.Decode(static_cast<int>(i)), &first);
        if (errs) std::printf("  instance %zu: %d errors (%s)\n", i, errs, first.c_str());
        CHECK_EQ(errs, 0);
    }
}

}  // namespace

// --- the test blobs (dumped from the client, see fixtures/README.md) ---------------------

TEST(graph_0F_file) {
    auto l = Load("graph_058000000000000F.stud", Mode::File);
    CHECK(l.ok);
    if (!l.ok) return;
    CHECK_EQ(l.doc.instances().size(), size_t(352));
    CHECK(l.doc.AllCrcOk());
    int with_refs = 0;
    for (auto& in : l.doc.instances()) with_refs += in.flags & 1;
    CHECK_EQ(with_refs, 16);
    CheckAllDecode(l);

    const auto& reg = Registry::Client();
    Value root = l.doc.Decode(0);
    CHECK_EQ(root.type_hash, 0x0DF7E24Fu);  // STUStatescriptGraph
    const Value* nodes = root.Find(0x51caba98);  // m_nodes
    const Value* states = root.Find(0x7b5125b3);  // m_states
    const Value* entries = root.Find(0x77b7ff8c);  // m_entries
    CHECK(nodes && nodes->type == Value::Type::List && nodes->items.size() == 49);
    CHECK(states && states->items.size() == 17);
    CHECK(entries && entries->items.size() == 8);
    if (nodes)
        for (auto& n : nodes->items) {
            CHECK(n.type == Value::Type::Object);
            // Node instances derive from the list's element type.
            if (n.type == Value::Type::Object) {
                auto* t = l.doc.instances()[n.instance].type;
                CHECK(t && reg.IsA(t->hash, nodes->type_hash));
            }
        }
    const Value* inl = root.Find(0x45b2bab8);  // m_graph (inline STUGraph)
    CHECK(inl && inl->type == Value::Type::Inline && inl->type_hash == 0xcb52d391);
    // m_consoleCommand: empty teString
    const Value* cc = root.Find(0x682b3b61);
    CHECK(cc && cc->type == Value::Type::String && cc->str.empty());
}

TEST(entitydef_39_file) {
    auto l = Load("entitydef_0400000000000039.stud", Mode::File);
    CHECK(l.ok);
    if (!l.ok) return;
    CHECK_EQ(l.doc.instances().size(), size_t(23));
    CHECK(l.doc.AllCrcOk());
    CheckAllDecode(l);

    Value root = l.doc.Decode(0);
    CHECK_EQ(root.type_hash, 0xec23fffdu);  // STUEntityDefinition
    const Value* comps = root.Find(0xa8f462ea);  // component map (kind 7)
    CHECK(comps && comps->type == Value::Type::Map && comps->items.size() == 3);
    if (comps && comps->items.size() == 3) {
        // Keys are the component type hashes, values the component instances of that type.
        for (auto& e : comps->items) {
            CHECK(e.type == Value::Type::Object);
            if (e.type == Value::Type::Object) {
                auto* t = l.doc.instances()[e.instance].type;
                CHECK(t && t->hash == e.map_key);
            }
        }
        CHECK_EQ(comps->items[0].map_key, 0x528f2f94u);
        CHECK_EQ(comps->items[1].map_key, 0x81ebdcd0u);
        CHECK_EQ(comps->items[2].map_key, 0x9b38211bu);
    }
}

// The raw file (after undoing the fixup) and the as-loaded image decode to the same tree.
static void CheckSameTree(const char* stud, const char* mem) {
    auto f = Load(stud, Mode::File);
    auto m = Load(mem, Mode::Loaded);
    CHECK(f.ok && m.ok);
    if (!f.ok || !m.ok) return;
    CHECK(m.doc.AllCrcOk());
    CheckAllDecode(m);
    CHECK_EQ(f.doc.instances().size(), m.doc.instances().size());
    const auto& reg = Registry::Client();
    for (size_t i = 0; i < f.doc.instances().size() && i < m.doc.instances().size(); i++) {
        std::string a = stu::ToJson(f.doc.Decode(int(i)), reg);
        std::string b = stu::ToJson(m.doc.Decode(int(i)), reg);
        if (a != b) std::printf("  instance %zu differs\n", i);
        CHECK(a == b);
    }
}
TEST(graph_0F_file_equals_loaded) { CheckSameTree("graph_058000000000000F.stud", "graph_058000000000000F.mem"); }
TEST(entitydef_39_file_equals_loaded) {
    CheckSameTree("entitydef_0400000000000039.stud", "entitydef_0400000000000039.mem");
}

// --- synthetic files -----------------------------------------------------------------------

namespace {
struct Builder {
    std::vector<uint8_t> b;
    size_t Alloc(size_t n, size_t align = 16) {
        while (b.size() % align) b.push_back(0);
        size_t o = b.size();
        b.resize(o + n, 0);
        return o;
    }
    template <typename T>
    void Put(size_t off, T v) { std::memcpy(b.data() + off, &v, sizeof v); }
};

// One instance of type 0x0897c6fb (size 24, a teString at +8) holding `text`.
std::vector<uint8_t> StringFile(const std::string& text, uint64_t crc) {
    Builder w;
    w.Alloc(16);
    w.Put<uint32_t>(0, 0x53545544);
    w.Put<uint32_t>(4, 3);
    size_t inst = w.Alloc(24);
    w.Put<uint32_t>(inst, 0x0897c6fb);
    size_t hdr = w.Alloc(16);
    size_t buf = w.Alloc(text.size() + 1);
    std::memcpy(w.b.data() + buf, text.data(), text.size());
    w.Put<int32_t>(hdr, int32_t(text.size()));
    w.Put<uint32_t>(hdr + 4, 0);  // CRC32 of the text (not checked by the reader)
    w.Put<int64_t>(hdr + 8, int64_t(buf));
    w.Put<int64_t>(inst + 8, int64_t(hdr));
    size_t entries = w.Alloc(16);
    w.Put<uint32_t>(entries, uint32_t(inst));
    w.Put<uint64_t>(entries + 8, crc);
    size_t table = w.Alloc(16);
    w.Put<uint32_t>(table, 1);
    w.Put<int64_t>(table + 8, int64_t(entries));
    w.Put<int64_t>(8, int64_t(table));
    return w.b;
}
}  // namespace

// A real file with a non-empty teString (CASC bytes of STUSoundSwitchGroup 0x034000000000001D): the string
// header is {i32 len, u32 crc32(text), i64 buf}.
TEST(file_string_casc) {
    Loaded l = Load("soundswitchgroup_034000000000001D.stud", Mode::File);
    CHECK(l.ok);
    if (!l.ok) return;
    CheckAllDecode(l);
    Value v = l.doc.Decode(0);
    const Value* s = v.Find(0xce0b2c3d);
    CHECK(s && s->type == Value::Type::String && s->str == "WEAPON_TAIL");
}

TEST(synthetic_string) {
    const auto& reg = Registry::Client();
    uint64_t crc = reg.Find(0x0897c6fb)->layout_crc;
    auto bytes = StringFile("Hello, STU", crc);
    Document d;
    std::string err;
    CHECK(Document::Parse(bytes, reg, d, err));
    CHECK(d.AllCrcOk());
    Value v = d.Decode(0);
    const Value* s = v.Find(0xc08c4427);
    CHECK(s && s->type == Value::Type::String && s->str == "Hello, STU");
}

TEST(synthetic_crc_mismatch) {
    auto bytes = StringFile("x", 0x1234);
    Document d;
    std::string err;
    CHECK(Document::Parse(bytes, Registry::Client(), d, err));
    CHECK(!d.AllCrcOk());
    CHECK(d.instances()[0].type != nullptr && !d.instances()[0].crc_ok);
}

TEST(synthetic_malformed) {
    const auto& reg = Registry::Client();
    auto good = StringFile("x", reg.Find(0x0897c6fb)->layout_crc);
    Document d;
    std::string err;
    auto bad_magic = good;
    bad_magic[0] ^= 1;
    CHECK(!Document::Parse(bad_magic, reg, d, err));
    auto bad_version = good;
    bad_version[4] = 2;
    CHECK(!Document::Parse(bad_version, reg, d, err));
    // Table offset past the end.
    auto bad_table = good;
    int64_t far = int64_t(good.size()) + 100;
    std::memcpy(bad_table.data() + 8, &far, 8);
    CHECK(!Document::Parse(bad_table, reg, d, err));
    // Every truncation either fails to parse or decodes without crashing.
    for (size_t n = 0; n < good.size(); n++) {
        std::vector<uint8_t> t(good.begin(), good.begin() + n);
        Document td;
        if (Document::Parse(t, reg, td, err))
            for (size_t i = 0; i < td.instances().size(); i++) (void)td.Decode(int(i));
    }
    // A string whose buffer lies outside the file decodes to an error, not a crash.
    auto bad_str = good;
    int64_t hdr = 0;
    std::memcpy(&hdr, bad_str.data() + 0x10 + 8, 8);
    int64_t junk = 1 << 20;
    std::memcpy(bad_str.data() + hdr + 8, &junk, 8);
    CHECK(Document::Parse(bad_str, reg, d, err));
    CHECK(d.Decode(0).Find(0xc08c4427)->type == Value::Type::Error);
}
