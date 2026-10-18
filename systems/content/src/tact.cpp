// Layer 1: TACT (.build.info, build config, encoding table, root) over the local CASC.
#include "content/ow08/ow08.h"
#include "util.h"

#include <algorithm>
#include <filesystem>
#include <sstream>
#include <zlib.h>

namespace content {

bool Inflate(const uint8_t* data, size_t size, bool gzip, Bytes& out, Error* err, size_t* consumed) {
    z_stream z{};
    if (inflateInit2(&z, gzip ? 16 + MAX_WBITS : MAX_WBITS) != Z_OK) return Fail(err, "inflateInit failed");
    z.next_in = const_cast<Bytef*>(data);
    z.avail_in = uInt(size);
    uint8_t buf[65536];
    int rc;
    do {
        z.next_out = buf;
        z.avail_out = sizeof buf;
        rc = inflate(&z, Z_NO_FLUSH);
        if (rc != Z_OK && rc != Z_STREAM_END) {
            inflateEnd(&z);
            return Fail(err, std::string("inflate: ") + (z.msg ? z.msg : "error"));
        }
        out.insert(out.end(), buf, buf + (sizeof buf - z.avail_out));
    } while (rc != Z_STREAM_END && (z.avail_in > 0 || z.avail_out == 0));
    if (consumed) *consumed = size - z.avail_in;
    inflateEnd(&z);
    return rc == Z_STREAM_END ? true : Fail(err, "inflate: truncated stream");
}

std::string CKey::Hex() const {
    static const char* d = "0123456789abcdef";
    std::string s(32, '0');
    for (int i = 0; i < 16; ++i) s[2 * i] = d[b[i] >> 4], s[2 * i + 1] = d[b[i] & 15];
    return s;
}

std::optional<CKey> CKey::FromHex(std::string_view hex) {
    if (hex.size() != 32) return std::nullopt;
    CKey k;
    for (int i = 0; i < 32; ++i) {
        char c = hex[i];
        int v = c >= '0' && c <= '9' ? c - '0' : c >= 'a' && c <= 'f' ? c - 'a' + 10 : c >= 'A' && c <= 'F' ? c - 'A' + 10 : -1;
        if (v < 0) return std::nullopt;
        k.b[i / 2] |= uint8_t(i % 2 ? v : v << 4);
    }
    return k;
}

uint32_t GuidType(Guid guid) {
    uint32_t v = uint32_t(guid >> 48) & 0xFFF, r = 0;
    for (int i = 0; i < 12; ++i) r |= ((v >> i) & 1) << (11 - i);
    return r + 1;
}

} // namespace content

namespace content::ow08 {

namespace fs = std::filesystem;

namespace {
std::string Trim(const std::string& s) {
    size_t a = s.find_first_not_of(" \t\r\n"), b = s.find_last_not_of(" \t\r\n");
    return a == std::string::npos ? "" : s.substr(a, b - a + 1);
}
std::vector<std::string> Split(const std::string& s, char sep) {
    std::vector<std::string> r;
    size_t a = 0;
    for (size_t b; (b = s.find(sep, a)) != std::string::npos; a = b + 1) r.push_back(s.substr(a, b - a));
    r.push_back(s.substr(a));
    return r;
}
std::string ToString(const Bytes& b) { return std::string(b.begin(), b.end()); }
} // namespace

std::vector<std::map<std::string, std::string>> ParseBuildInfo(const std::string& text) {
    std::vector<std::map<std::string, std::string>> rows;
    std::istringstream in(text);
    std::string line;
    std::vector<std::string> cols;
    while (std::getline(in, line)) {
        line = Trim(line);
        if (line.empty() || line[0] == '#') continue;
        auto f = Split(line, '|');
        if (cols.empty()) {
            for (auto& c : f) cols.push_back(c.substr(0, c.find('!')));
            continue;
        }
        std::map<std::string, std::string> row;
        for (size_t i = 0; i < cols.size() && i < f.size(); ++i) row[cols[i]] = f[i];
        rows.push_back(std::move(row));
    }
    return rows;
}

std::map<std::string, std::string> ParseConfig(const std::string& text) {
    std::map<std::string, std::string> r;
    std::istringstream in(text);
    std::string line;
    while (std::getline(in, line)) {
        line = Trim(line);
        if (line.empty() || line[0] == '#') continue;
        auto eq = line.find('=');
        if (eq != std::string::npos) r[Trim(line.substr(0, eq))] = Trim(line.substr(eq + 1));
    }
    return r;
}

size_t EncodingTable::CKeyHash::operator()(const CKey& k) const noexcept { return size_t(Le64(k.b)); }

// "EN", u8 version 1, u8 CKey size, u8 EKey size, u16be CKey page KB, u16be EKey page KB,
// u32be CKey page count, u32be EKey page count, u8 ?, u32be ESpec block size; ESpec strings;
// CKey page index {first CKey, page MD5}; CKey pages {u8 EKey count, u40be size, CKey, EKeys…} (0-padded).
// Only the CKey pages are needed to go CKey → EKey.
bool EncodingTable::Parse(const Bytes& d, Error* err) {
    map_.clear();
    if (d.size() < 22 || d[0] != 'E' || d[1] != 'N' || d[2] != 1) return Fail(err, "encoding: bad header");
    uint32_t ch = d[3], eh = d[4];
    if (ch != 16 || eh != 16) return Fail(err, "encoding: unexpected key sizes");
    size_t cpage = size_t(uint32_t(d[5]) << 8 | d[6]) * 1024;
    uint32_t cpages = Be32(&d[9]);
    uint32_t espec = Be32(&d[18]);
    size_t p = 22 + size_t(espec) + size_t(cpages) * (ch + 16);
    if (p + size_t(cpages) * cpage > d.size()) return Fail(err, "encoding: CKey pages out of range");
    map_.reserve(size_t(cpages) * 16);
    for (uint32_t pg = 0; pg < cpages; ++pg) {
        size_t q = p + pg * cpage, end = q + cpage;
        while (q + 6 + ch <= end) {
            uint32_t n = d[q];
            if (n == 0) break;
            if (q + 6 + ch + size_t(n) * eh > end) return Fail(err, "encoding: entry crosses page");
            Entry e;
            for (int i = 0; i < 5; ++i) e.size = e.size << 8 | d[q + 1 + i];
            CKey ck;
            std::memcpy(ck.b, &d[q + 6], 16);
            for (uint32_t i = 0; i < n; ++i) {
                std::array<uint8_t, 16> ek;
                std::memcpy(ek.data(), &d[q + 6 + ch + i * eh], 16);
                e.ekeys.push_back(ek);
            }
            map_.emplace(ck, std::move(e));
            q += 6 + ch + size_t(n) * eh;
        }
    }
    return true;
}

const EncodingTable::Entry* EncodingTable::Find(const CKey& ckey) const {
    auto it = map_.find(ckey);
    return it == map_.end() ? nullptr : &it->second;
}

// Root: text, "#MD5|CHUNK_ID|FILENAME|INSTALLPATH" (the header names the columns).
std::vector<RootRow> ParseRoot(const std::string& text) {
    std::vector<RootRow> rows;
    std::istringstream in(text);
    std::string line;
    int c_md5 = 0, c_name = 2, c_path = 3;
    while (std::getline(in, line)) {
        line = Trim(line);
        if (line.empty()) continue;
        auto f = Split(line[0] == '#' ? line.substr(1) : line, '|');
        if (line[0] == '#') {
            for (int i = 0; i < int(f.size()); ++i) {
                if (f[i] == "MD5") c_md5 = i;
                else if (f[i] == "FILENAME") c_name = i;
                else if (f[i] == "INSTALLPATH") c_path = i;
            }
            continue;
        }
        if (int(f.size()) <= std::max({c_md5, c_name, c_path})) continue;
        auto ck = CKey::FromHex(f[c_md5]);
        if (!ck) continue;
        rows.push_back({*ck, f[c_name], f[c_path]});
    }
    return rows;
}

bool Tact::Open(const std::string& install_dir, const Keyring& keys, Error* err) {
    keys_ = keys;
    fs::path root(install_dir);
    Bytes b;
    if (!ReadFile((root / ".build.info").string(), b)) return Fail(err, "cannot read .build.info");
    auto rows = ParseBuildInfo(ToString(b));
    if (rows.empty()) return Fail(err, ".build.info: no rows");
    build_info_ = rows[0];
    for (auto& r : rows)
        if (r["Active"] == "1") {
            build_info_ = r;
            break;
        }
    const std::string& bk = build_info_["Build Key"];
    if (bk.size() != 32) return Fail(err, ".build.info: bad Build Key");
    auto cfg = root / "data/casc/config" / bk.substr(0, 2) / bk.substr(2, 2) / bk;
    if (!ReadFile(cfg.string(), b)) return Fail(err, "cannot read build config " + cfg.string());
    build_config_ = ParseConfig(ToString(b));

    if (!casc_.Open((root / "data/casc/data").string(), err)) return false;

    // encoding = <CKey> <EKey>: the encoding file is read by its EKey.
    auto enc = Split(build_config_["encoding"], ' ');
    auto enc_ekey = enc.size() == 2 ? CKey::FromHex(enc[1]) : std::nullopt;
    if (!enc_ekey) return Fail(err, "build config: bad encoding line");
    Bytes encoding;
    Error e;
    if (!casc_.ReadEKey(enc_ekey->b, keys_, encoding, &e)) return Fail(err, "encoding: " + e.message);
    if (!encoding_.Parse(encoding, err)) return false;

    auto root_ckey = CKey::FromHex(build_config_["root"]);
    if (!root_ckey) return Fail(err, "build config: bad root");
    Bytes rootb;
    if (!ReadCKey(*root_ckey, rootb, &e)) return Fail(err, "root: " + e.message);
    root_ = ParseRoot(ToString(rootb));
    return true;
}

bool Tact::ReadCKey(const CKey& ckey, Bytes& out, Error* err) const {
    const EncodingTable::Entry* e = encoding_.Find(ckey);
    if (!e) return Fail(err, "CKey " + ckey.Hex() + " not in the encoding table");
    for (auto& ek : e->ekeys)
        if (casc_.Find(ek.data())) {
            if (!casc_.ReadEKey(ek.data(), keys_, out, err)) return false;
            if (out.size() != e->size) return Fail(err, "CKey " + ckey.Hex() + ": decoded size mismatch");
            return true;
        }
    return Fail(err, "CKey " + ckey.Hex() + ": no EKey in the local storage");
}

const RootRow* Tact::FindRoot(const std::string& filename) const {
    for (auto& r : root_)
        if (r.filename == filename) return &r;
    return nullptr;
}

} // namespace content::ow08
