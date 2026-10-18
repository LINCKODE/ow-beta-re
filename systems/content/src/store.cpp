// Ow08Store: content::Store over a local Overwatch beta 0.8 install.
#include "content/ow08/ow08.h"
#include "util.h"

#include <algorithm>
#include <atomic>
#include <mutex>
#include <thread>
#include <unordered_map>

namespace content::ow08 {

bool Ow08Store::Open(const std::string& install_dir, const Ow08Options& opt, Error* err) {
    opt_ = opt;
    Keyring keys = opt.builtin_keys ? Keyring::Builtin() : Keyring();
    for (auto& [name, key] : opt.extra_keys) keys.Add(name, key);
    if (!tact_.Open(install_dir, keys, err)) return false;

    // Manifests as the client names them (Resource Manager Start 0x9c6bb0): main, then speech.
    manifests_ = {"Win_SPWin_" + opt.region + "_L" + opt.locale + "_EExt"};
    if (opt.speech) manifests_.push_back("Win_SPWin_speech_" + opt.region + "_L" + opt.locale + "_EExt");

    struct Job {
        size_t package;
        Package parsed;
        Error err;
        bool ok = false;
    };
    for (auto& m : manifests_) {
        const RootRow* row = tact_.FindRoot("Manifest/" + m + ".apm");
        if (!row) return Fail(err, "manifest " + m + " not in the TACT root");
        Bytes b;
        Error e;
        if (!tact_.ReadCKey(row->ckey, b, &e)) return Fail(err, m + ": " + e.message);
        Apm apm;
        if (!ParseApm(b, apm, &e)) return Fail(err, m + ": " + e.message);
        for (auto& it : apm.items) {
            PackageInfo p;
            p.guid = it.package;
            p.ckey = it.ckey;
            p.size = it.size;
            p.key_name = it.key_name;
            p.asset = it.asset;
            p.manifest = m;
            packages_.push_back(std::move(p));
        }
    }

    // Read and parse every package (independent reads: spread over threads).
    std::vector<Job> jobs(packages_.size());
    std::atomic<size_t> next{0};
    auto worker = [&] {
        for (size_t i; (i = next++) < jobs.size();) {
            Bytes b;
            jobs[i].package = i;
            jobs[i].ok = tact_.ReadCKey(packages_[i].ckey, b, &jobs[i].err) && ParsePackage(b, jobs[i].parsed, &jobs[i].err);
        }
    };
    unsigned n = std::max(1u, std::min(16u, std::thread::hardware_concurrency()));
    std::vector<std::thread> threads;
    for (unsigned t = 0; t < n; ++t) threads.emplace_back(worker);
    for (auto& t : threads) t.join();

    std::unordered_map<Guid, size_t> seen;
    for (auto& p : packages_) {
        if (seen.emplace(p.guid, resources_.size()).second)
            resources_.push_back({p.guid, p.ckey, p.size, 0, 0});
    }
    size_t total_records = 0;
    for (auto& j : jobs) total_records += j.parsed.records.size();
    record_guids_.reserve(total_records);
    record_flags_.reserve(total_records);
    package_first_.reserve(jobs.size() + 1);
    for (auto& j : jobs) {
        auto& pkg = packages_[j.package];
        if (!j.ok) return Fail(err, "package " + Hex64(pkg.guid) + ": " + j.err.message);
        pkg.record_count = uint32_t(j.parsed.records.size());
        package_first_.push_back(record_guids_.size());
        for (auto& r : j.parsed.records) {
            record_guids_.push_back(r.guid);
            record_flags_.push_back(r.flags);
        }
        stats_.references += j.parsed.references.size();
        stats_.deps += j.parsed.deps.size();
        // The package's bundle (type 0x09C) is a resource of its own: its CKey and size are in the header.
        if (j.parsed.bundle && seen.emplace(j.parsed.bundle, resources_.size()).second) {
            ++stats_.bundles;
            resources_.push_back({j.parsed.bundle, j.parsed.bundle_ckey, j.parsed.bundle_size, 0, pkg.guid});
        }
        for (auto& r : j.parsed.records) {
            ++stats_.records;
            ++stats_.record_flags[r.flags];
            auto [it, fresh] = seen.emplace(r.guid, resources_.size());
            if (fresh) {
                resources_.push_back({r.guid, r.ckey, r.size, r.flags, pkg.guid});
                // A bundled record without an encoding entry of its own is read from inside its package's bundle.
                if ((r.flags & 0x40000000) && j.parsed.bundle && !tact_.Encoding().Find(r.ckey))
                    bundle_only_.emplace(r.guid, BundleSlice{j.parsed.bundle_ckey, r.offset});
            } else {
                ++stats_.duplicate_records;
                if (!(resources_[it->second].ckey == r.ckey)) ++stats_.duplicate_ckey_diff;
            }
        }
    }
    package_first_.push_back(record_guids_.size());
    std::sort(resources_.begin(), resources_.end(), [](auto& a, auto& b) { return a.guid < b.guid; });
    return true;
}

std::string Ow08Store::Describe() const {
    auto bi = tact_.BuildInfo();
    return "local CASC (ow08 backend), version " + bi["Version"] + ", build " + tact_.BuildConfig().at("build-name") + ", " +
           std::to_string(packages_.size()) + " packages, " + std::to_string(resources_.size()) + " resources";
}

std::optional<ResourceInfo> Ow08Store::Find(Guid guid) const {
    auto it = std::lower_bound(resources_.begin(), resources_.end(), guid, [](auto& r, Guid g) { return r.guid < g; });
    if (it == resources_.end() || it->guid != guid) return std::nullopt;
    return *it;
}

std::vector<PackageRecordInfo> Ow08Store::PackageResources(size_t index) const {
    std::vector<PackageRecordInfo> out;
    if (index + 1 >= package_first_.size()) return out;
    out.reserve(package_first_[index + 1] - package_first_[index]);
    for (size_t i = package_first_[index]; i < package_first_[index + 1]; ++i)
        out.push_back({record_guids_[i], record_flags_[i]});
    return out;
}

std::vector<uint32_t> Ow08Store::ResourcePackages(Guid guid) const {
    std::vector<uint32_t> out;
    for (size_t p = 0; p + 1 < package_first_.size(); ++p)
        for (size_t i = package_first_[p]; i < package_first_[p + 1]; ++i)
            if (record_guids_[i] == guid) {
                out.push_back(uint32_t(p));
                break;
            }
    return out;
}

bool Ow08Store::Read(Guid guid, Bytes& out, Error* err) const {
    auto r = Find(guid);
    if (!r) return Fail(err, "resource " + Hex64(guid) + " not in any package");
    if (auto b = bundle_only_.find(guid); b != bundle_only_.end()) {
        if (!ReadFromBundle(b->second, r->size, out, err)) return false;
        if (opt_.verify_md5 && !(Md5(out.data(), out.size()) == r->ckey))
            return Fail(err, "resource " + Hex64(guid) + ": MD5 mismatch (from its bundle)");
        return true;
    }
    if (!ReadCKey(r->ckey, out, err)) return false;
    if (out.size() != r->size) return Fail(err, "resource " + Hex64(guid) + ": size differs from its package record");
    return true;
}

bool Ow08Store::ReadFromBundle(const BundleSlice& s, uint32_t size, Bytes& out, Error* err) const {
    // Bundles are read whole and kept in a small LRU cache.
    std::shared_ptr<const Bytes> data;
    {
        std::lock_guard<std::mutex> lock(bundle_cache_mutex_);
        for (auto it = bundle_cache_.begin(); it != bundle_cache_.end(); ++it)
            if (it->key == s.bundle) {
                bundle_cache_.splice(bundle_cache_.begin(), bundle_cache_, it);
                data = it->data;
                break;
            }
    }
    if (!data) {
        auto b = std::make_shared<Bytes>();
        if (!ReadCKey(s.bundle, *b, err)) return false;
        data = b;
        std::lock_guard<std::mutex> lock(bundle_cache_mutex_);
        bundle_cache_.push_front({s.bundle, data});
        bundle_cache_bytes_ += data->size();
        while (bundle_cache_bytes_ > kBundleCacheBytes && bundle_cache_.size() > 1) {
            bundle_cache_bytes_ -= bundle_cache_.back().data->size();
            bundle_cache_.pop_back();
        }
    }
    if (size_t(s.offset) + size > data->size()) return Fail(err, "bundle slice out of range");
    out.assign(data->begin() + s.offset, data->begin() + s.offset + size);
    return true;
}

bool Ow08Store::ReadCKey(const CKey& ckey, Bytes& out, Error* err) const {
    if (!tact_.ReadCKey(ckey, out, err)) return false;
    if (opt_.verify_md5 && !(Md5(out.data(), out.size()) == ckey)) return Fail(err, "CKey " + ckey.Hex() + ": MD5 mismatch");
    return true;
}

} // namespace content::ow08

namespace content {

std::unique_ptr<Store> OpenOw08Install(const std::string& install_dir, const Ow08Options& opt, Error* err) {
    auto s = std::make_unique<ow08::Ow08Store>();
    if (!s->Open(install_dir, opt, err)) return nullptr;
    return s;
}

} // namespace content
