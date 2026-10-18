// C API over content::Store (capi/content.h).
#include "content.h"
#include "content/store.h"

#include <algorithm>
#include <array>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>

struct content_store {
    std::unique_ptr<content::Store> store;
    std::string describe;
};

namespace {

void SetErr(char* err, size_t n, const std::string& msg) {
    if (!err || !n) return;
    std::snprintf(err, n, "%s", msg.c_str());
}

void Fill(const content::ResourceInfo& r, content_resource* out) {
    out->guid = r.guid;
    out->package = r.package;
    out->type = content::GuidType(r.guid);
    out->size = r.size;
    out->flags = r.flags;
    std::memcpy(out->ckey, r.ckey.b, 16);
}

int Emit(bool ok, content::Bytes& b, const content::Error& e, uint8_t** data, size_t* size, char* err, size_t n) {
    if (!ok) {
        SetErr(err, n, e.message);
        return 0;
    }
    *size = b.size();
    *data = static_cast<uint8_t*>(std::malloc(b.size() ? b.size() : 1));
    if (!*data) {
        SetErr(err, n, "out of memory");
        return 0;
    }
    std::memcpy(*data, b.data(), b.size());
    return 1;
}

} // namespace

extern "C" {

void content_options_init(content_options* opt) {
    if (!opt) return;
    *opt = content_options{};
    content::Ow08Options d;
    opt->speech = d.speech;
    opt->verify_md5 = d.verify_md5;
    opt->builtin_keys = d.builtin_keys;
}

content_store* content_open_ow08(const char* install_dir, const char* region, const char* locale, char* err,
                                 size_t err_size) {
    content_options o;
    content_options_init(&o);
    o.region = region;
    o.locale = locale;
    return content_open_ow08_ex(install_dir, &o, err, err_size);
}

content_store* content_open_ow08_ex(const char* install_dir, const content_options* o, char* err, size_t err_size) {
    if (!install_dir) {
        SetErr(err, err_size, "install_dir is NULL");
        return nullptr;
    }
    content::Ow08Options opt;
    if (o) {
        if (o->region) opt.region = o->region;
        if (o->locale) opt.locale = o->locale;
        opt.speech = o->speech != 0;
        opt.verify_md5 = o->verify_md5 != 0;
        opt.builtin_keys = o->builtin_keys != 0;
        if (o->extra_key_count && !o->extra_keys) {
            SetErr(err, err_size, "extra_keys is NULL");
            return nullptr;
        }
        for (size_t i = 0; i < o->extra_key_count; ++i) {
            std::array<uint8_t, 16> k;
            std::memcpy(k.data(), o->extra_keys[i].key, 16);
            opt.extra_keys.emplace_back(o->extra_keys[i].name, k);
        }
    }
    content::Error e;
    auto st = content::OpenOw08Install(install_dir, opt, &e);
    if (!st) {
        SetErr(err, err_size, e.message);
        return nullptr;
    }
    auto* s = new content_store;
    s->describe = st->Describe();
    s->store = std::move(st);
    return s;
}

void content_close(content_store* s) { delete s; }

uint32_t content_guid_type(uint64_t guid) { return content::GuidType(guid); }

const char* content_describe(const content_store* s) { return s ? s->describe.c_str() : ""; }

size_t content_package_count(const content_store* s) { return s ? s->store->Packages().size() : 0; }

int content_package_at(const content_store* s, size_t index, content_package* out) {
    if (!s || !out || index >= s->store->Packages().size()) return 0;
    auto& p = s->store->Packages()[index];
    out->guid = p.guid;
    out->key_name = p.key_name;
    out->size = p.size;
    out->record_count = p.record_count;
    out->asset = p.asset;
    std::memcpy(out->ckey, p.ckey.b, 16);
    return 1;
}

const char* content_package_manifest(const content_store* s, size_t index) {
    if (!s || index >= s->store->Packages().size()) return "";
    return s->store->Packages()[index].manifest.c_str();
}

size_t content_resource_count(const content_store* s) { return s ? s->store->Resources().size() : 0; }

int content_resource_at(const content_store* s, size_t index, content_resource* out) {
    if (!s || !out || index >= s->store->Resources().size()) return 0;
    Fill(s->store->Resources()[index], out);
    return 1;
}

int content_find(const content_store* s, uint64_t guid, content_resource* out) {
    if (!s || !out) return 0;
    auto r = s->store->Find(guid);
    if (!r) return 0;
    Fill(*r, out);
    return 1;
}

size_t content_resources(const content_store* s, size_t start, content_resource* out, size_t n) {
    if (!s || !out) return 0;
    auto& all = s->store->Resources();
    if (start >= all.size()) return 0;
    n = std::min(n, all.size() - start);
    for (size_t i = 0; i < n; ++i) Fill(all[start + i], out + i);
    return n;
}

size_t content_resources_of_type(const content_store* s, uint32_t type, content_resource* out, size_t cap) {
    if (!s) return 0;
    size_t total = 0;
    for (auto& r : s->store->Resources()) {
        if (content::GuidType(r.guid) != type) continue;
        if (out && total < cap) Fill(r, out + total);
        ++total;
    }
    return total;
}

size_t content_package_resources(const content_store* s, size_t pkg_index, uint64_t* guids, uint32_t* flags,
                                 size_t cap) {
    if (!s) return 0;
    auto recs = s->store->PackageResources(pkg_index);
    for (size_t i = 0; i < recs.size() && i < cap; ++i) {
        if (guids) guids[i] = recs[i].guid;
        if (flags) flags[i] = recs[i].flags;
    }
    return recs.size();
}

size_t content_resource_packages(const content_store* s, uint64_t guid, uint32_t* out, size_t cap) {
    if (!s) return 0;
    auto pkgs = s->store->ResourcePackages(guid);
    if (out) std::copy_n(pkgs.begin(), std::min(cap, pkgs.size()), out);
    return pkgs.size();
}

int content_read(const content_store* s, uint64_t guid, uint8_t** data, size_t* size, char* err, size_t err_size) {
    if (!s || !data || !size) return 0;
    content::Bytes b;
    content::Error e;
    bool ok = s->store->Read(guid, b, &e);
    return Emit(ok, b, e, data, size, err, err_size);
}

int content_read_ckey(const content_store* s, const uint8_t ckey[16], uint8_t** data, size_t* size, char* err,
                      size_t err_size) {
    if (!s || !ckey || !data || !size) return 0;
    content::CKey k;
    std::memcpy(k.b, ckey, 16);
    content::Bytes b;
    content::Error e;
    bool ok = s->store->ReadCKey(k, b, &e);
    return Emit(ok, b, e, data, size, err, err_size);
}

void content_free(void* p) { std::free(p); }

} // extern "C"
