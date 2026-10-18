#pragma once
// content::Store: read the game's resources by GUID, independent of how they are stored.
//
// The interface hides the storage (CASC/TACT/APM for the 0.8 client). The default implementation,
// `OpenOw08Install`, reads a local Overwatch beta 0.8 (build 24919) install from disk with our own
// CASC/TACT/APM code (docs/ContentAccess.md). Another client version, or an implementation on top of
// an existing library, plugs in by deriving from Store.
#include <array>
#include <cstdint>
#include <memory>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace content {

using Guid = uint64_t;
using Bytes = std::vector<uint8_t>;

// Content key: MD5 of the decoded bytes (TACT CKey).
struct CKey {
    uint8_t b[16] = {};
    bool operator==(const CKey&) const = default;
    std::string Hex() const;
    static std::optional<CKey> FromHex(std::string_view hex);
};

// Resource type as the client dispatches it: bitreverse12((guid >> 48) & 0xFFF) + 1
// (docs/ResourceLoading.md §1). 0x01B = Statescript graph, 0x003 = entity definition, 0x077 = asset package.
uint32_t GuidType(Guid guid);

struct PackageInfo {
    Guid guid = 0;              // asset package GUID (type 0x077, 0x06E…)
    CKey ckey;                  // the package blob's content key
    uint32_t size = 0;          // size of the package blob
    uint32_t record_count = 0;  // resources listed by the package
    uint64_t key_name = 0;      // TACT key name the manifest associates with the package
    std::string manifest;       // manifest the package came from (e.g. "Win_SPWin_RDEV_LenUS_EExt")
    Guid asset = 0;             // the asset the package belongs to (APM item +0x08: map data, hero, …)
};

struct ResourceInfo {
    Guid guid = 0;
    CKey ckey;
    uint32_t size = 0;          // decoded size in bytes
    uint32_t flags = 0;         // record flags (docs/ContentAccess.md)
    Guid package = 0;           // first package that lists the resource (0 for packages themselves)
};

// One record of an asset package: a resource the package lists.
struct PackageRecordInfo {
    Guid guid = 0;
    uint32_t flags = 0;         // record flags (docs/ContentAccess.md §4)
};

struct Error {
    std::string message;
};

class Store {
public:
    virtual ~Store() = default;

    // Short description of the backend and the content version (e.g. "local CASC (ow08 backend), version 0.8.0.0.24919, …").
    virtual std::string Describe() const = 0;

    virtual const std::vector<PackageInfo>& Packages() const = 0;
    // Every resource GUID the store can read (packages included), sorted by GUID.
    virtual const std::vector<ResourceInfo>& Resources() const = 0;
    virtual std::optional<ResourceInfo> Find(Guid guid) const = 0;
    // Package membership (a resource can be listed by several packages). Records of package `index`
    // (Packages() order) in package order; empty if out of range.
    virtual std::vector<PackageRecordInfo> PackageResources(size_t index) const = 0;
    // Indices into Packages() of every package listing `guid` as a record, ascending (manifest order).
    virtual std::vector<uint32_t> ResourcePackages(Guid guid) const = 0;

    // Decoded bytes of a resource. Thread-safe. Returns false (and sets *err) on failure.
    virtual bool Read(Guid guid, Bytes& out, Error* err = nullptr) const = 0;
    // Decoded bytes by content key (any file in the container, e.g. manifests).
    virtual bool ReadCKey(const CKey& ckey, Bytes& out, Error* err = nullptr) const = 0;
};

// Options for the 0.8 local-install backend.
struct Ow08Options {
    // Manifest names are "Win_SPWin_<region>_L<locale>_EExt" (+ "_speech_"), as the client builds them
    // from the registry (docs/ResourceLoading.md §6).
    std::string region = "RDEV";
    std::string locale = "enUS";
    bool speech = true;          // also load the speech manifest
    bool verify_md5 = false;     // check MD5(decoded) == CKey on every read
    // Start from the built-in 0.8 keyring (ow08::Keyring::Builtin); false = only extra_keys.
    bool builtin_keys = true;
    // Extra TACT keys (name → 16-byte key).
    std::vector<std::pair<uint64_t, std::array<uint8_t, 16>>> extra_keys;
};

// Open a local Overwatch beta 0.8 install (directory containing `.build.info` and `data/casc`).
std::unique_ptr<Store> OpenOw08Install(const std::string& install_dir, const Ow08Options& opt = {},
                                       Error* err = nullptr);

} // namespace content
