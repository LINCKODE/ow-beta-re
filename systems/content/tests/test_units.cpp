// Unit tests on synthetic data: crypto, BLTE, text tables, APM, packages.
#include "content/ow08/ow08.h"
#include "test.h"
#include "util.h"

#include <zlib.h>

using namespace content;
using namespace content::ow08;

namespace {

Bytes Str(const char* s) { return Bytes(s, s + std::strlen(s)); }
void PutBe32(Bytes& b, uint32_t v) {
    for (int i = 3; i >= 0; --i) b.push_back(uint8_t(v >> (8 * i)));
}
void PutLe32(Bytes& b, uint32_t v) {
    for (int i = 0; i < 4; ++i) b.push_back(uint8_t(v >> (8 * i)));
}
void PutLe64(Bytes& b, uint64_t v) {
    for (int i = 0; i < 8; ++i) b.push_back(uint8_t(v >> (8 * i)));
}
Bytes Deflate(const Bytes& in, bool gzip) {
    z_stream z{};
    deflateInit2(&z, 9, Z_DEFLATED, gzip ? 16 + MAX_WBITS : MAX_WBITS, 8, Z_DEFAULT_STRATEGY);
    Bytes out(deflateBound(&z, uLong(in.size())) + 32);
    z.next_in = const_cast<Bytef*>(in.data());
    z.avail_in = uInt(in.size());
    z.next_out = out.data();
    z.avail_out = uInt(out.size());
    deflate(&z, Z_FINISH);
    out.resize(z.total_out);
    deflateEnd(&z);
    return out;
}
// BLTE with a chunk table over the given (already mode-prefixed) chunks.
Bytes Blte(const std::vector<std::pair<Bytes, uint32_t>>& chunks) {
    Bytes b = Str("BLTE");
    PutBe32(b, uint32_t(12 + 24 * chunks.size()));
    b.push_back(0x0F);
    b.push_back(0), b.push_back(0), b.push_back(uint8_t(chunks.size()));
    for (auto& [c, dsize] : chunks) {
        PutBe32(b, uint32_t(c.size()));
        PutBe32(b, dsize);
        CKey m = Md5(c.data(), c.size());
        b.insert(b.end(), m.b, m.b + 16);
    }
    for (auto& [c, dsize] : chunks) b.insert(b.end(), c.begin(), c.end());
    return b;
}

} // namespace

TEST(md5_vectors) {
    CHECK(Md5(nullptr, 0).Hex() == "d41d8cd98f00b204e9800998ecf8427e");
    Bytes abc = Str("abc");
    CHECK(Md5(abc.data(), abc.size()).Hex() == "900150983cd24fb0d6963f7d28e17f72");
    Bytes q = Str("12345678901234567890123456789012345678901234567890123456789012345678901234567890");
    CHECK(Md5(q.data(), q.size()).Hex() == "57edf4a22be3c955ac49da2e2107b67a");
}

TEST(salsa20_ecrypt_set1_vector0) {
    // eSTREAM Salsa20/20, 128-bit key, set 1 vector 0: key 80 00…, IV 0, keystream bytes 0..15.
    uint8_t key[16] = {0x80}, iv[8] = {};
    uint8_t buf[64] = {};
    Salsa20Xor(key, iv, buf, sizeof buf);
    const uint8_t want[16] = {0x4D, 0xFA, 0x5E, 0x48, 0x1D, 0xA2, 0x3E, 0xA0,
                              0x9A, 0x31, 0x02, 0x20, 0x50, 0x85, 0x99, 0x36};
    CHECK(std::memcmp(buf, want, 16) == 0);
    // XOR twice = identity, across a partial block.
    Bytes d = Str("the quick brown fox jumps over the lazy dog, twice over the 64-byte boundary!");
    Bytes c = d;
    Salsa20Xor(key, iv, c.data(), c.size());
    CHECK(c != d);
    Salsa20Xor(key, iv, c.data(), c.size());
    CHECK(c == d);
}

TEST(guid_type) {
    CHECK_EQ(GuidType(0x058000000000041aull), 0x01Bu);
    CHECK_EQ(GuidType(0x0400000000000039ull), 0x003u);
    CHECK_EQ(GuidType(0x06E000000000008Dull), 0x077u);
    CHECK_EQ(GuidType(0x0DE0000000000001ull), 0x07Cu);
}

TEST(ckey_hex) {
    auto k = CKey::FromHex("F3aef5d7cb236ccc95d224c3ba99fcb5");
    CHECK(k.has_value());
    CHECK(k->Hex() == "f3aef5d7cb236ccc95d224c3ba99fcb5");
    CHECK(!CKey::FromHex("f3aef5").has_value());
    CHECK(!CKey::FromHex("g3aef5d7cb236ccc95d224c3ba99fcb5").has_value());
}

TEST(blte_modes) {
    Keyring keys;
    std::array<uint8_t, 16> key{};
    for (int i = 0; i < 16; ++i) key[i] = uint8_t(i * 7 + 1);
    keys.Add(0x1122334455667788ull, key);

    Bytes plain0 = Str("chunk zero, raw. "), plain1 = Str("chunk one, zlib zlib zlib zlib. "),
          plain2 = Str("chunk two, encrypted then zlib.");
    Bytes c0 = Str("N");
    c0.insert(c0.end(), plain0.begin(), plain0.end());
    Bytes c1 = Str("Z"), z1 = Deflate(plain1, false);
    c1.insert(c1.end(), z1.begin(), z1.end());
    // E chunk: key name, IV, 'S', Salsa20(inner chunk) with the chunk index (2) XORed into the IV.
    Bytes inner = Str("Z"), z2 = Deflate(plain2, false);
    inner.insert(inner.end(), z2.begin(), z2.end());
    uint8_t iv[8] = {9, 8, 7, 6, 0, 0, 0, 0}, ivx[8];
    std::memcpy(ivx, iv, 8);
    ivx[0] ^= 2;
    Bytes enc = inner;
    Salsa20Xor(key.data(), ivx, enc.data(), enc.size());
    Bytes c2 = Str("E");
    c2.push_back(8);
    PutLe64(c2, 0x1122334455667788ull);
    c2.push_back(4);
    c2.insert(c2.end(), iv, iv + 4);
    c2.push_back('S');
    c2.insert(c2.end(), enc.begin(), enc.end());

    Bytes blte = Blte({{c0, uint32_t(plain0.size())}, {c1, uint32_t(plain1.size())}, {c2, uint32_t(plain2.size())}});
    Bytes out;
    Error err;
    CHECK(DecodeBlte(blte.data(), blte.size(), keys, out, &err));
    Bytes want = plain0;
    want.insert(want.end(), plain1.begin(), plain1.end());
    want.insert(want.end(), plain2.begin(), plain2.end());
    CHECK(out == want);

    // Missing key → error naming the key.
    CHECK(!DecodeBlte(blte.data(), blte.size(), Keyring(), out, &err));
    CHECK(err.message.find("1122334455667788") != std::string::npos);

    // Header-less BLTE (header size 0): one chunk; and a nested 'F' chunk.
    Bytes single = Str("BLTE");
    PutBe32(single, 0);
    single.insert(single.end(), c0.begin(), c0.end());
    CHECK(DecodeBlte(single.data(), single.size(), keys, out, &err) && out == plain0);
    Bytes f = Str("F");
    f.insert(f.end(), single.begin(), single.end());
    Bytes nested = Blte({{f, uint32_t(plain0.size())}});
    CHECK(DecodeBlte(nested.data(), nested.size(), keys, out, &err) && out == plain0);

    // Wrong decoded size in the table → error.
    Bytes bad = Blte({{c0, uint32_t(plain0.size() + 1)}});
    CHECK(!DecodeBlte(bad.data(), bad.size(), keys, out, &err));
    Bytes junk = Str("BLTX");
    CHECK(!DecodeBlte(junk.data(), junk.size(), keys, out, &err));
}

TEST(text_tables) {
    auto rows = ParseBuildInfo("Branch!STRING:0|Active!DEC:1|Build Key!HEX:16\neu|1|85f2\nus|0|85f3\n");
    CHECK_EQ(rows.size(), size_t(2));
    CHECK(rows[0]["Branch"] == "eu" && rows[0]["Build Key"] == "85f2" && rows[1]["Active"] == "0");
    auto cfg = ParseConfig("# Build Configuration\n\nroot = f3ae\nencoding = aa bb\n");
    CHECK(cfg["root"] == "f3ae" && cfg["encoding"] == "aa bb");
    auto root = ParseRoot("#MD5|CHUNK_ID|FILENAME|INSTALLPATH\n"
                          "C3CA24AD8F160676A6F82F3D2DD4A1D3|0|Manifest/Win_SPWin_RDEV_LenUS_EExt.apm|\n"
                          "FE3AD8A77EEF77B383DF4929AED816FD|0|RetailClient/GameClientApp.exe|GameClientApp.exe\n");
    CHECK_EQ(root.size(), size_t(2));
    CHECK(root[0].filename == "Manifest/Win_SPWin_RDEV_LenUS_EExt.apm" && root[0].install_path.empty());
    CHECK(root[1].ckey.Hex() == "fe3ad8a77eef77b383df4929aed816fd" && root[1].install_path == "GameClientApp.exe");
}

TEST(apm_and_package) {
    Bytes apm;
    PutLe64(apm, 0x133f7);
    PutLe32(apm, 24919);
    PutLe32(apm, 1);           // items
    PutLe32(apm, 2);           // entries
    PutLe32(apm, 0x13636d66);  // "fmc" | 0x13 << 24
    for (uint32_t i = 0; i < 2; ++i) PutLe32(apm, i), PutLe64(apm, 0xAB00 + i);
    size_t item = apm.size();
    apm.resize(item + 0x48);
    auto put64 = [&](size_t o, uint64_t v) { for (int i = 0; i < 8; ++i) apm[item + o + i] = uint8_t(v >> (8 * i)); };
    put64(0x18, 0xDBD3371554F60306ull);
    put64(0x20, 0x06E0000016B79D8Cull);
    put64(0x2C, 10509 | uint64_t(0x82F27) << 32);
    for (int i = 0; i < 16; ++i) apm[item + 0x38 + i] = uint8_t(i);
    Apm a;
    Error err;
    CHECK(ParseApm(apm, a, &err));
    CHECK_EQ(a.build, 24919u);
    CHECK_EQ(a.entries.size(), size_t(2));
    CHECK_EQ(a.items.size(), size_t(1));
    CHECK(a.items[0].package == 0x06E0000016B79D8Cull && a.items[0].key_name == 0xDBD3371554F60306ull);
    CHECK(a.items[0].size == 10509 && a.items[0].records_size == 0x82F27 && a.items[0].ckey.b[15] == 15);
    apm.pop_back();
    CHECK(!ParseApm(apm, a, &err));

    // Package: 0x50 header, gzip(records), u64 references, u32 package dependencies.
    Bytes recs;
    PutLe64(recs, 0x058000000000000Full), PutLe32(recs, 38432), PutLe32(recs, 0), PutLe32(recs, 0);
    for (int i = 0; i < 16; ++i) recs.push_back(uint8_t(0xA0 + i));
    PutLe64(recs, 0x0400000000000039ull), PutLe32(recs, 1472), PutLe32(recs, 0x40000000), PutLe32(recs, 0x40);
    for (int i = 0; i < 16; ++i) recs.push_back(uint8_t(0xB0 + i));
    Bytes gz = Deflate(recs, true);
    Bytes pkg(0x50);
    auto p64 = [&](size_t o, uint64_t v) { for (int i = 0; i < 8; ++i) pkg[o + i] = uint8_t(v >> (8 * i)); };
    size_t refs = 0x50 + gz.size(), deps = refs + 8;
    p64(0, 0x50), p64(8, refs), p64(0x10, deps);
    p64(0x18, 2 | uint64_t(gz.size()) << 32);
    p64(0x20, 1 | uint64_t(1) << 32);  // 1 reference, 1 dependency
    p64(0x2C, 0x0D90000016B79D8Cull);   // bundle GUID
    pkg.insert(pkg.end(), gz.begin(), gz.end());
    PutLe64(pkg, 0x0DE0000000000001ull);
    PutLe32(pkg, 0x16B79D8C);
    Package p;
    CHECK(ParsePackage(pkg, p, &err));
    CHECK_EQ(p.records.size(), size_t(2));
    CHECK(p.records[0].guid == 0x058000000000000Full && p.records[0].size == 38432 && p.records[0].ckey.b[0] == 0xA0);
    CHECK(p.records[1].flags == 0x40000000 && p.records[1].offset == 0x40 && p.records[1].ckey.b[15] == 0xBF);
    CHECK(p.references.size() == 1 && p.references[0] == 0x0DE0000000000001ull);
    CHECK(p.deps.size() == 1 && p.deps[0] == 0x16B79D8C);
    CHECK(p.bundle == 0x0D90000016B79D8Cull && GuidType(p.bundle) == 0x09C);
}
