// Layer 0: local CASC (data/casc/data: shmem, *.idx, data.NNN) and BLTE.
#include "content/ow08/ow08.h"
#include "util.h"

#include <cstdio>
#include <cstring>
#include <filesystem>

namespace content::ow08 {

namespace fs = std::filesystem;

size_t EKey9Hash::operator()(const EKey9& k) const noexcept {
    uint64_t v;
    std::memcpy(&v, k.b, 8);
    return size_t(v * 0x9E3779B97F4A7C15ull ^ k.b[8]);
}

// ---- BLTE -------------------------------------------------------------------------------------------

namespace {

bool DecodeChunk(const uint8_t* p, size_t n, uint32_t index, const Keyring& keys, Bytes& out, Error* err,
                 int depth);

bool DecodeBlteImpl(const uint8_t* p, size_t n, const Keyring& keys, Bytes& out, Error* err, int depth) {
    if (depth > 4) return Fail(err, "BLTE: nesting too deep");
    if (n < 8 || std::memcmp(p, "BLTE", 4) != 0) return Fail(err, "BLTE: bad magic");
    uint32_t header = Be32(p + 4);
    if (header == 0) return DecodeChunk(p + 8, n - 8, 0, keys, out, err, depth);
    if (header < 12 || header > n) return Fail(err, "BLTE: bad header size");
    uint8_t flags = p[8];
    uint32_t count = Be24(p + 9);
    if (flags != 0x0F || 12 + size_t(count) * 24 != header) return Fail(err, "BLTE: bad chunk table");
    size_t total = 0, pos = header;
    for (uint32_t i = 0; i < count; ++i) total += Be32(p + 12 + i * 24 + 4);
    out.reserve(out.size() + total);
    for (uint32_t i = 0; i < count; ++i) {
        const uint8_t* e = p + 12 + i * 24;
        uint32_t csize = Be32(e), dsize = Be32(e + 4);
        if (pos + csize > n) return Fail(err, "BLTE: chunk out of range");
        size_t before = out.size();
        if (!DecodeChunk(p + pos, csize, i, keys, out, err, depth)) return false;
        if (out.size() - before != dsize) return Fail(err, "BLTE: chunk " + std::to_string(i) + " decoded size mismatch");
        pos += csize;
    }
    return true;
}

bool DecodeChunk(const uint8_t* p, size_t n, uint32_t index, const Keyring& keys, Bytes& out, Error* err,
                 int depth) {
    if (n < 1) return Fail(err, "BLTE: empty chunk");
    switch (p[0]) {
    case 'N':
        out.insert(out.end(), p + 1, p + n);
        return true;
    case 'Z':
        return Inflate(p + 1, n - 1, false, out, err);
    case 'F':
        return DecodeBlteImpl(p + 1, n - 1, keys, out, err, depth + 1);
    case 'E': {
        // u8 key name size, key name (8 bytes: u64 LE), u8 IV size (<= 16), IV, u8 type ('S' Salsa20; the client
        // also accepts 'A'), payload [G: 0xbf1060].
        size_t q = 1;
        if (q >= n) return Fail(err, "BLTE: truncated E chunk");
        uint8_t kn = p[q++];
        if (kn != 8 || q + kn + 1 > n) return Fail(err, "BLTE: unsupported E key name size");
        uint64_t name = Le64(p + q);
        q += kn;
        uint8_t ivn = p[q++];
        if (ivn > 16 || q + ivn + 1 > n) return Fail(err, "BLTE: bad E IV");
        // Nonce = first min(IV size, 8) bytes of the IV, XOR the chunk index as a u64 LE [G: 0xbf0b30].
        uint8_t iv[8] = {};
        std::memcpy(iv, p + q, ivn < 8 ? ivn : 8);
        for (int i = 0; i < 8; ++i) iv[i] ^= uint8_t(uint64_t(index) >> (8 * i));
        q += ivn;
        uint8_t type = p[q++];
        if (type != 'S') return Fail(err, std::string("BLTE: unsupported encryption type '") + char(type) + "'");
        const uint8_t* key = keys.Find(name);
        if (!key) return Fail(err, "BLTE: missing TACT key " + Hex64(name));
        Bytes plain(p + q, p + n);
        Salsa20Xor(key, iv, plain.data(), plain.size());
        if (!DecodeChunk(plain.data(), plain.size(), index, keys, out, err, depth + 1))
            return Fail(err, err ? err->message + " (after decrypting with key " + Hex64(name) + ": wrong key?)" : "");
        return true;
    }
    default:
        return Fail(err, std::string("BLTE: unknown chunk mode 0x") + Hex64(p[0]).substr(14));
    }
}

} // namespace

bool DecodeBlte(const uint8_t* data, size_t size, const Keyring& keys, Bytes& out, Error* err) {
    out.clear();
    return DecodeBlteImpl(data, size, keys, out, err, 0);
}

// ---- idx / data --------------------------------------------------------------------------------------

bool LocalCasc::Open(const std::string& data_dir, Error* err) {
    dir_ = data_dir;
    index_.clear();
    versions_.assign(16, 0);
    // The client keeps the current version of each bucket's .idx in `shmem`: u32 at +0x110 + 4 * bucket.
    // Without shmem, take the highest version on disk.
    Bytes shmem;
    bool have_shmem = ReadFile((fs::path(data_dir) / "shmem").string(), shmem) && shmem.size() >= 0x150 &&
                      (Le32(shmem.data()) == 4 || Le32(shmem.data()) == 5);  // shmem version [G: 0xf7a710]
    for (uint32_t b = 0; b < 16; ++b) {
        if (have_shmem) {
            versions_[b] = Le32(shmem.data() + 0x110 + 4 * b);
        } else {
            std::error_code ec;
            for (auto& e : fs::directory_iterator(data_dir, ec)) {
                auto name = e.path().filename().string();
                unsigned bucket, ver;
                if (name.size() == 14 && name.ends_with(".idx") && std::sscanf(name.c_str(), "%2x%8x", &bucket, &ver) == 2 &&
                    bucket == b && ver > versions_[b])
                    versions_[b] = ver;
            }
        }
        char name[32];
        std::snprintf(name, sizeof name, "%02x%08x.idx", b, versions_[b]);
        Bytes d;
        if (!ReadFile((fs::path(data_dir) / name).string(), d)) return Fail(err, std::string("cannot read ") + name);
        // u32 header size, u32 hash, header {u16 version 7, u8 bucket, u8 ?, u8 size bytes 4, u8 offset bytes 5,
        // u8 key bytes 9, u8 archive-offset bits 30, u64 max archive size}; pad to 16;
        // u32 entries size, u32 hash, entries {key[9], u40be archive << 30 | offset, u32le size}.
        if (d.size() < 0x18) return Fail(err, std::string("short ") + name);
        uint32_t hs = Le32(d.data());
        const uint8_t* h = d.data() + 8;
        if (Le16(h) != 7 || h[2] != b) return Fail(err, std::string("unexpected idx header in ") + name);
        uint32_t sl = h[4], ol = h[5], kl = h[6], bits = h[7];
        if (sl != 4 || ol != 5 || kl != 9 || bits != 30) return Fail(err, std::string("unexpected idx key/offset spec in ") + name);
        size_t p = (8 + size_t(hs) + 15) & ~size_t(15);
        if (p + 8 > d.size()) return Fail(err, std::string("short ") + name);
        uint32_t es = Le32(d.data() + p);
        p += 8;
        if (p + es > d.size()) return Fail(err, std::string("idx entries out of range in ") + name);
        for (size_t q = p; q + 18 <= p + es; q += 18) {
            EKey9 k;
            std::memcpy(k.b, d.data() + q, 9);
            uint64_t off = 0;
            for (int i = 0; i < 5; ++i) off = off << 8 | d[q + 9 + i];
            IdxEntry e{uint32_t(off >> 30), uint32_t(off & ((1u << 30) - 1)), Le32(d.data() + q + 14)};
            index_.emplace(k, e);  // first entry wins
        }
    }
    return true;
}

const IdxEntry* LocalCasc::Find(const uint8_t* ekey) const {
    EKey9 k;
    std::memcpy(k.b, ekey, 9);
    auto it = index_.find(k);
    return it == index_.end() ? nullptr : &it->second;
}

bool LocalCasc::ReadRecord(const uint8_t* ekey, Bytes& out, Error* err) const {
    const IdxEntry* e = Find(ekey);
    if (!e) return Fail(err, "EKey not in the local index");
    char name[16];
    std::snprintf(name, sizeof name, "data.%03u", e->archive);
    Bytes rec;
    if (!ReadAt((fs::path(dir_) / name).string(), e->offset, e->size, rec) || rec.size() != e->size)
        return Fail(err, std::string("cannot read record from ") + name);
    // Record header (0x1E): EKey[16] byte-reversed, u32le record size, 10 more bytes (flags, checksums).
    if (e->size < 0x1E) return Fail(err, "record too small");
    for (int i = 0; i < 9; ++i)
        if (rec[15 - i] != ekey[i]) return Fail(err, "record header EKey mismatch");
    if (Le32(rec.data() + 16) != e->size) return Fail(err, "record header size mismatch");
    out.assign(rec.begin() + 0x1E, rec.end());
    return true;
}

bool LocalCasc::ReadEKey(const uint8_t* ekey, const Keyring& keys, Bytes& out, Error* err) const {
    Bytes blte;
    if (!ReadRecord(ekey, blte, err)) return false;
    return DecodeBlte(blte.data(), blte.size(), keys, out, err);
}

} // namespace content::ow08
