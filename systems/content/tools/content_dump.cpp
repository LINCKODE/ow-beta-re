// content_dump: inspect and extract resources from a local 0.8 install through content::Store.
#include "content/ow08/ow08.h"

#include <array>
#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <fstream>
#include <map>
#include <string>

using namespace content;

static int Usage() {
    std::fprintf(stderr,
                 "usage: content_dump [--install DIR] [--region R] [--locale L] [--verify] [--no-builtin-keys] [--key NAME=KEY]… COMMAND\n"
                 "  --info                  backend, build, index sizes, package/resource counts\n"
                 "  --root                  TACT root rows (CKey, file name)\n"
                 "  --list-packages         package GUID, record count, size, key name, CKey, manifest\n"
                 "  --list-types            resource count and bytes per type\n"
                 "  --list [TYPE]           resources (optionally of one type, hex, e.g. 1B)\n"
                 "  --check [TYPE]          read every resource, verify MD5 == CKey, count STUD blobs\n"
                 "  --read GUID OUT         write a resource's bytes to OUT\n"
                 "  --read-ckey CKEY OUT    write a file by content key to OUT\n"
                 "  --read-file NAME OUT    write a TACT root file (e.g. Manifest/…apm) to OUT\n"
                 "DIR defaults to $OW_INSTALL, else game/owbetaclient.\n");
    return 2;
}

static bool Write(const char* path, const Bytes& b) {
    std::ofstream f(path, std::ios::binary);
    return f && f.write(reinterpret_cast<const char*>(b.data()), std::streamsize(b.size()));
}

int main(int argc, char** argv) {
    std::string install = std::getenv("OW_INSTALL") ? std::getenv("OW_INSTALL") : "game/owbetaclient";
    Ow08Options opt;
    int i = 1;
    for (; i < argc; ++i) {
        if (!std::strcmp(argv[i], "--install") && i + 1 < argc) install = argv[++i];
        else if (!std::strcmp(argv[i], "--region") && i + 1 < argc) opt.region = argv[++i];
        else if (!std::strcmp(argv[i], "--locale") && i + 1 < argc) opt.locale = argv[++i];
        else if (!std::strcmp(argv[i], "--verify")) opt.verify_md5 = true;
        else if (!std::strcmp(argv[i], "--no-builtin-keys")) opt.builtin_keys = false;
        else if (!std::strcmp(argv[i], "--key") && i + 1 < argc) {
            // NAME=KEY (16 hex digits = 32 hex digits)
            std::string kv = argv[++i];
            auto eq = kv.find('=');
            auto key = eq == std::string::npos ? std::nullopt : CKey::FromHex(kv.substr(eq + 1));
            if (!key) return Usage();
            std::array<uint8_t, 16> k;
            std::memcpy(k.data(), key->b, 16);
            opt.extra_keys.emplace_back(std::strtoull(kv.substr(0, eq).c_str(), nullptr, 16), k);
        }
        else break;
    }
    if (i >= argc) return Usage();
    std::string cmd = argv[i++];

    auto t0 = std::chrono::steady_clock::now();
    Error err;
    auto store = OpenOw08Install(install, opt, &err);
    if (!store) {
        std::fprintf(stderr, "open %s: %s\n", install.c_str(), err.message.c_str());
        return 1;
    }
    double open_s = std::chrono::duration<double>(std::chrono::steady_clock::now() - t0).count();
    auto* ow = dynamic_cast<ow08::Ow08Store*>(store.get());

    if (cmd == "--info") {
        std::printf("%s\nopen: %.2f s\n", store->Describe().c_str(), open_s);
        if (ow) {
            auto& t = ow->GetTact();
            std::printf("idx entries: %zu, encoding entries: %zu, root rows: %zu\n", t.Casc().size(), t.Encoding().size(),
                        t.Root().size());
            for (auto& m : ow->Manifests()) std::printf("manifest: %s\n", m.c_str());
            auto& s = ow->GetStats();
            std::printf("package records: %zu, duplicates: %zu (different CKey: %zu)\n", s.records, s.duplicate_records,
                        s.duplicate_ckey_diff);
            std::printf("bundles: %zu, GUID references: %zu, package dependencies: %zu\nrecord flags:", s.bundles,
                        s.references, s.deps);
            for (auto& [f, n] : s.record_flags) std::printf(" %08X x %zu", f, n);
            std::printf("\n");
        }
    } else if (cmd == "--root") {
        if (!ow) return 1;
        for (auto& r : ow->GetTact().Root()) std::printf("%s %s\n", r.ckey.Hex().c_str(), r.filename.c_str());
    } else if (cmd == "--list-packages") {
        for (auto& p : store->Packages())
            std::printf("%016llX %6u %9u %016llX %s %s\n", (unsigned long long)p.guid, p.record_count, p.size,
                        (unsigned long long)p.key_name, p.ckey.Hex().c_str(), p.manifest.c_str());
    } else if (cmd == "--list-types") {
        std::map<uint32_t, std::pair<size_t, uint64_t>> types;
        for (auto& r : store->Resources()) {
            auto& t = types[GuidType(r.guid)];
            ++t.first, t.second += r.size;
        }
        std::printf("type  count        bytes\n");
        for (auto& [t, v] : types) std::printf("%03X %7zu %12llu\n", t, v.first, (unsigned long long)v.second);
    } else if (cmd == "--list") {
        long type = i < argc ? std::strtol(argv[i], nullptr, 16) : -1;
        for (auto& r : store->Resources())
            if (type < 0 || GuidType(r.guid) == uint32_t(type))
                std::printf("%016llX %03X %9u %08X %s %016llX\n", (unsigned long long)r.guid, GuidType(r.guid), r.size,
                            r.flags, r.ckey.Hex().c_str(), (unsigned long long)r.package);
    } else if (cmd == "--check") {
        // Read every resource (optionally of one type), verify MD5 == CKey, count STUD blobs per type.
        long type = i < argc ? std::strtol(argv[i], nullptr, 16) : -1;
        std::map<uint32_t, std::array<size_t, 3>> per;  // type → {ok, stud, failed}
        std::map<std::string, size_t> errors;
        uint64_t bytes = 0;
        for (auto& r : store->Resources()) {
            uint32_t t = GuidType(r.guid);
            if (type >= 0 && t != uint32_t(type)) continue;
            Bytes b;
            Error e;
            auto& c = per[t];
            if (store->Read(r.guid, b, &e) && ow08::Md5(b.data(), b.size()) == r.ckey) {
                ++c[0], bytes += b.size();
                if (b.size() >= 8 && !std::memcmp(b.data(), "DUTS", 4)) ++c[1];
            } else {
                ++c[2];
                std::string m = e.message.empty() ? "MD5 mismatch" : e.message;
                if (m.starts_with("CKey ")) m = "CKey …" + m.substr(m.find(':') == std::string::npos ? 37 : m.find(':'));
                if (m.starts_with("resource ")) m = "resource …" + m.substr(m.find(':'));
                ++errors[m];
            }
        }
        std::printf("type      ok   STUD failed\n");
        size_t ok = 0, bad = 0;
        for (auto& [t, c] : per) {
            std::printf("%03X %7zu %7zu %6zu\n", t, c[0], c[1], c[2]);
            ok += c[0], bad += c[2];
        }
        std::printf("total: %zu ok (%llu bytes), %zu failed\n", ok, (unsigned long long)bytes, bad);
        for (auto& [m, n] : errors) std::printf("  %6zu x %s\n", n, m.c_str());
    } else if (cmd == "--read" ||cmd == "--read-ckey" || cmd == "--read-file") {
        if (i + 1 >= argc) return Usage();
        Bytes b;
        bool ok;
        if (cmd == "--read") {
            ok = store->Read(std::strtoull(argv[i], nullptr, 16), b, &err);
        } else if (cmd == "--read-ckey") {
            auto ck = CKey::FromHex(argv[i]);
            if (!ck) return Usage();
            ok = store->ReadCKey(*ck, b, &err);
        } else {
            const ow08::RootRow* row = ow ? ow->GetTact().FindRoot(argv[i]) : nullptr;
            ok = row && store->ReadCKey(row->ckey, b, &err);
            if (!row) err.message = "not in the TACT root";
        }
        if (!ok) {
            std::fprintf(stderr, "%s: %s\n", argv[i], err.message.c_str());
            return 1;
        }
        if (!Write(argv[i + 1], b)) {
            std::fprintf(stderr, "cannot write %s\n", argv[i + 1]);
            return 1;
        }
        std::printf("%s: %zu bytes -> %s\n", argv[i], b.size(), argv[i + 1]);
    } else {
        return Usage();
    }
    return 0;
}
