#pragma once
// Building blocks of the 0.8 local-install backend (docs/ContentAccess.md). Layered:
//   crypto  Salsa20, MD5, the TACT keyring
//   0 CASC  data/casc/data: *.idx (EKey → archive, offset, size), data.NNN records, BLTE
//   1 TACT  .build.info → build config → encoding table (CKey → EKey) → root (names → CKeys)
//   2 APM   .apm manifests → asset packages → package records (GUID → CKey, size)
// Users normally go through content::Store (store.h); these are public for tests and tools.
#include "content/store.h"

#include <list>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <unordered_map>
#include <vector>

namespace content::ow08 {

// ---- crypto ------------------------------------------------------------------------------------

// Salsa20/20 with a 16-byte key ("expand 16-byte k") and an 8-byte nonce, counter from 0; XORs in place.
void Salsa20Xor(const uint8_t key[16], const uint8_t nonce[8], uint8_t* data, size_t size);
CKey Md5(const uint8_t* data, size_t size);

class Keyring {
public:
    // The 0.8 keys: FB680CB6A8BF81F3 (built into the client) and the ones the server streamed
    // (prometheus/Prometheus-Core/dllmain.cpp `casc_keys`).
    static Keyring Builtin();
    void Add(uint64_t name, const std::array<uint8_t, 16>& key) { keys_[name] = key; }
    const uint8_t* Find(uint64_t name) const {
        auto it = keys_.find(name);
        return it == keys_.end() ? nullptr : it->second.data();
    }
    size_t size() const { return keys_.size(); }

private:
    std::unordered_map<uint64_t, std::array<uint8_t, 16>> keys_;
};

// ---- layer 0: CASC ------------------------------------------------------------------------------

// BLTE: "BLTE", u32be header size, then (header size != 0) u8 flags 0x0F, u24be chunk count, per chunk
// {u32be compressed size, u32be decoded size, md5[16]}; chunks start with a mode byte:
// 'N' raw, 'Z' zlib, 'F' nested BLTE, 'E' encrypted (key name, IV, 'S' = Salsa20) then a chunk again.
bool DecodeBlte(const uint8_t* data, size_t size, const Keyring& keys, Bytes& out, Error* err = nullptr);

struct IdxEntry {
    uint32_t archive = 0;  // data.NNN
    uint32_t offset = 0;   // record offset inside the archive
    uint32_t size = 0;     // record size (0x1E header + BLTE)
};

// The first 9 bytes of an EKey, as stored in the .idx files.
struct EKey9 {
    uint8_t b[9];
    bool operator==(const EKey9&) const = default;
};
struct EKey9Hash {
    size_t operator()(const EKey9& k) const noexcept;
};

class LocalCasc {
public:
    // data_dir = <install>/data/casc/data
    bool Open(const std::string& data_dir, Error* err = nullptr);
    const IdxEntry* Find(const uint8_t* ekey) const;  // ekey: at least 9 bytes
    size_t size() const { return index_.size(); }
    const std::vector<uint32_t>& IdxVersions() const { return versions_; }
    // BLTE bytes of a record (the 0x1E-byte record header checked and stripped).
    bool ReadRecord(const uint8_t* ekey, Bytes& out, Error* err = nullptr) const;
    bool ReadEKey(const uint8_t* ekey, const Keyring& keys, Bytes& out, Error* err = nullptr) const;

private:
    std::string dir_;
    std::vector<uint32_t> versions_;  // per bucket 0..15
    std::unordered_map<EKey9, IdxEntry, EKey9Hash> index_;
};

// ---- layer 1: TACT ------------------------------------------------------------------------------

// "Name!TYPE:len|…" header + rows; returns the rows as name → value maps.
std::vector<std::map<std::string, std::string>> ParseBuildInfo(const std::string& text);
// "key = value" lines (build / CDN config).
std::map<std::string, std::string> ParseConfig(const std::string& text);

class EncodingTable {
public:
    bool Parse(const Bytes& data, Error* err = nullptr);
    struct Entry {
        uint64_t size = 0;
        std::vector<std::array<uint8_t, 16>> ekeys;
    };
    const Entry* Find(const CKey& ckey) const;
    size_t size() const { return map_.size(); }

private:
    struct CKeyHash {
        size_t operator()(const CKey& k) const noexcept;
    };
    std::unordered_map<CKey, Entry, CKeyHash> map_;
};

struct RootRow {
    CKey ckey;
    std::string filename;     // e.g. "Manifest/Win_SPWin_RDEV_LenUS_EExt.apm"
    std::string install_path;
};
std::vector<RootRow> ParseRoot(const std::string& text);

class Tact {
public:
    bool Open(const std::string& install_dir, const Keyring& keys, Error* err = nullptr);
    bool ReadCKey(const CKey& ckey, Bytes& out, Error* err = nullptr) const;
    const std::vector<RootRow>& Root() const { return root_; }
    const RootRow* FindRoot(const std::string& filename) const;
    const std::map<std::string, std::string>& BuildInfo() const { return build_info_; }
    const std::map<std::string, std::string>& BuildConfig() const { return build_config_; }
    const LocalCasc& Casc() const { return casc_; }
    const EncodingTable& Encoding() const { return encoding_; }
    const Keyring& Keys() const { return keys_; }

private:
    Keyring keys_;
    LocalCasc casc_;
    EncodingTable encoding_;
    std::map<std::string, std::string> build_info_, build_config_;
    std::vector<RootRow> root_;
};

// ---- layer 2: APM + asset packages --------------------------------------------------------------

// .apm manifest (parser 0x9c4bf0): header 0x18 bytes {u64 ? (not read by the client), u32 build (24919),
// u32 package count, u32 entry count, u32 'fmc' | 0x13 << 24}, entry count × 12-byte entries {u32, u64} (not
// read on the load path), package count × 0x48-byte items sorted by `lookup` (binary search in 0x9c4cf0).
struct ApmItem {
    uint64_t lookup = 0;       // +0x00 typebits(asset) | crc32({asset, asset_param})
    Guid asset = 0;            // +0x08 the asset the package belongs to
    uint64_t asset_param = 0;  // +0x10 (0 in 0.8)
    uint64_t key_name = 0;     // +0x18 a TACT key name (not used on the load path)
    Guid package = 0;          // +0x20 package GUID (type 0x077; speech packages have bit 40 set)
    uint32_t size = 0;         // +0x2C package blob size
    uint32_t records_size = 0; // +0x30 sum of the package's record sizes
    CKey ckey;                 // +0x38 package blob CKey
};
struct Apm {
    uint64_t unk00 = 0;
    uint32_t build = 0;
    uint32_t magic = 0;
    std::vector<std::pair<uint32_t, uint64_t>> entries;
    std::vector<ApmItem> items;
};
bool ParseApm(const Bytes& data, Apm& out, Error* err = nullptr);

// Asset package blob (resource type 0x077; construct 0x9c4190, records 0x9d08f0): 0x50-byte header, then
// a gzip stream of 36-byte records, u64 GUID references, u32 package indices.
struct PackageRecord {
    Guid guid = 0;
    uint32_t size = 0;
    uint32_t flags = 0;   // 0x40000000 = also stored in the package's bundle at `offset`; & 7 = variant mask;
                          // 0x80000000 = ? (the client keeps a side list)
    uint32_t offset = 0;  // offset inside the bundle (flag 0x40000000)
    CKey ckey;
};
struct Package {
    uint64_t records_offset = 0;    // +0x00
    uint64_t references_offset = 0; // +0x08 (0 = none)
    uint64_t deps_offset = 0;       // +0x10 (0 = none)
    uint32_t record_count = 0;      // +0x18
    uint32_t records_gz_size = 0;   // +0x1C
    uint32_t reference_count = 0;   // +0x20
    uint32_t dep_count = 0;         // +0x24
    uint32_t records_size = 0;      // +0x28 sum of record sizes
    Guid bundle = 0;                // +0x2C bundle GUID (type 0x09C "BINARY_PACKAGE_DATA"), 0 = no bundle
    uint32_t bundle_size = 0;       // +0x34
    uint64_t unk38 = 0;             // +0x38
    CKey bundle_ckey;               // +0x40
    std::vector<PackageRecord> records;
    std::vector<Guid> references;   // resources requested by GUID alone (no CKey)
    std::vector<uint32_t> deps;     // other packages: GUID = typebits(0x077) | index
};
bool ParsePackage(const Bytes& data, Package& out, Error* err = nullptr);

// ---- the Store --------------------------------------------------------------------------------------

// content::Store over a local 0.8 install: TACT → the region/locale manifests → every package's records.
// Resource GUID → first package (manifest order) that lists it.
class Ow08Store : public Store {
public:
    bool Open(const std::string& install_dir, const Ow08Options& opt, Error* err);

    std::string Describe() const override;
    const std::vector<PackageInfo>& Packages() const override { return packages_; }
    const std::vector<ResourceInfo>& Resources() const override { return resources_; }
    std::optional<ResourceInfo> Find(Guid guid) const override;
    std::vector<PackageRecordInfo> PackageResources(size_t index) const override;
    std::vector<uint32_t> ResourcePackages(Guid guid) const override;  // scans all records (~1.4M, ~13 ms)
    bool Read(Guid guid, Bytes& out, Error* err = nullptr) const override;
    bool ReadCKey(const CKey& ckey, Bytes& out, Error* err = nullptr) const override;

    const Tact& GetTact() const { return tact_; }
    const std::vector<std::string>& Manifests() const { return manifests_; }
    struct Stats {
        size_t records = 0;             // package records over all packages
        size_t duplicate_records = 0;   // records whose GUID an earlier package already listed
        size_t duplicate_ckey_diff = 0; // … with a different CKey than the first
        size_t bundles = 0;             // packages with a bundle (added as resources, type 0x09C)
        size_t references = 0, deps = 0;
        std::map<uint32_t, size_t> record_flags;  // flags value → record count
    };
    const Stats& GetStats() const { return stats_; }

private:
    Ow08Options opt_;
    Tact tact_;
    std::vector<std::string> manifests_;
    std::vector<PackageInfo> packages_;
    std::vector<ResourceInfo> resources_;  // sorted by GUID
    // Package membership, flat: records of package i are [package_first_[i], package_first_[i + 1]).
    // 12 bytes per record (1.4M records in 0.8: ~17 MB).
    std::vector<Guid> record_guids_;
    std::vector<uint32_t> record_flags_;
    std::vector<size_t> package_first_;
    Stats stats_;
    // Bundle-flagged records with no encoding entry of their own are read from their package's bundle.
    struct BundleSlice {
        CKey bundle;
        uint32_t offset = 0;
    };
    std::unordered_map<Guid, BundleSlice> bundle_only_;
    bool ReadFromBundle(const BundleSlice& s, uint32_t size, Bytes& out, Error* err) const;
    // Decoded bundles, least recently used evicted beyond kBundleCacheBytes (reads in GUID order hop between bundles).
    static constexpr size_t kBundleCacheBytes = size_t(1) << 30;
    struct CachedBundle {
        CKey key;
        std::shared_ptr<const Bytes> data;
    };
    mutable std::mutex bundle_cache_mutex_;
    mutable std::list<CachedBundle> bundle_cache_;  // front = most recent
    mutable size_t bundle_cache_bytes_ = 0;
};

} // namespace content::ow08
