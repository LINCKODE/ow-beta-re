/* C API smoke test against the install (skipped when absent). Plain C (C99), so the header is checked as C. */
#include "content.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>

int main(void) {
    struct stat st;
    if (stat(OW_INSTALL "/.build.info", &st) != 0) {
        printf("no install at %s: skipped\n", OW_INSTALL);
        return 0;
    }
    char err[256] = {0};
    content_store* s = content_open_ow08(OW_INSTALL, NULL, NULL, err, sizeof err);
    if (!s) {
        printf("FAIL open: %s\n", err);
        return 1;
    }
    int fails = 0;
    printf("%s\n", content_describe(s));
    if (content_package_count(s) != 1332) printf("FAIL package count %zu\n", content_package_count(s)), ++fails;
    content_resource r;
    if (!content_find(s, 0x058000000000000FULL, &r) || r.type != 0x1B || r.size != 38432) printf("FAIL find graph\n"), ++fails;
    uint8_t* data = NULL;
    size_t size = 0;
    if (!content_read(s, 0x058000000000000FULL, &data, &size, err, sizeof err) || size != 38432 ||
        memcmp(data, "DUTS", 4) != 0)
        printf("FAIL read graph: %s\n", err), ++fails;
    content_free(data);
    if (!content_read_ckey(s, r.ckey, &data, &size, err, sizeof err) || size != 38432) printf("FAIL read ckey\n"), ++fails;
    content_free(data);
    if (content_read(s, 0x0580000000FFFFFFULL, &data, &size, err, sizeof err) || !err[0]) printf("FAIL unknown guid\n"), ++fails;
    content_resource first;
    if (!content_resource_at(s, 0, &first) || content_resource_at(s, content_resource_count(s), &first)) printf("FAIL resource_at\n"), ++fails;

    /* Bulk copy == resource_at; of_type == filtered list. */
    size_t n = content_resource_count(s);
    content_resource* all = (content_resource*)malloc(n * sizeof *all);
    if (content_resources(s, 0, all, n + 10) != n || content_resources(s, n, all, 1) != 0) printf("FAIL bulk count\n"), ++fails;
    content_resource mid;
    content_resource_at(s, n / 2, &mid);
    const content_resource* b = &all[n / 2]; /* field by field: the struct has tail padding */
    if (mid.guid != b->guid || mid.package != b->package || mid.type != b->type || mid.size != b->size ||
        mid.flags != b->flags || memcmp(mid.ckey, b->ckey, 16) != 0)
        printf("FAIL bulk == resource_at\n"), ++fails;
    size_t graphs = 0, defs = 0;
    for (size_t i = 0; i < n; ++i) {
        if (all[i].type != content_guid_type(all[i].guid)) printf("FAIL guid_type %016llx\n", (unsigned long long)all[i].guid), ++fails;
        graphs += all[i].type == 0x1B;
        defs += all[i].type == 0x03;
    }
    if (graphs != 370 || defs != 967) printf("FAIL type counts %zu %zu\n", graphs, defs), ++fails;
    if (content_resources_of_type(s, 0x1B, NULL, 0) != graphs) printf("FAIL of_type count\n"), ++fails;
    content_resource g[4];
    if (content_resources_of_type(s, 0x1B, g, 4) != graphs || g[0].type != 0x1B || g[3].guid <= g[2].guid) printf("FAIL of_type fill\n"), ++fails;
    if (content_guid_type(0x058000000000000FULL) != 0x1B || content_guid_type(0x0400000000000039ULL) != 0x03) printf("FAIL guid_type\n"), ++fails;
    free(all);
    /* Package membership: the graph's first package lists it, every package listing it has it as a record,
     * and the record counts add up to the package headers'. */
    {
        size_t np = content_package_count(s), total = 0, recs = 0;
        uint32_t pk[64];
        size_t npk = content_resource_packages(s, 0x058000000000000FULL, pk, 64);
        content_package first_pkg;
        if (npk == 0 || npk > 64 || !content_package_at(s, pk[0], &first_pkg) || first_pkg.guid != r.package)
            printf("FAIL resource_packages %zu\n", npk), ++fails;
        for (size_t i = 0; i < npk && i < 64; ++i) {
            size_t m = content_package_resources(s, pk[i], NULL, NULL, 0);
            uint64_t* gs = (uint64_t*)malloc((m ? m : 1) * sizeof *gs);
            uint32_t* fs = (uint32_t*)malloc((m ? m : 1) * sizeof *fs);
            int found = 0;
            if (content_package_resources(s, pk[i], gs, fs, m) != m) printf("FAIL package_resources count\n"), ++fails;
            for (size_t k = 0; k < m; ++k) found |= gs[k] == 0x058000000000000FULL;
            if (!found) printf("FAIL package %u does not list the graph\n", pk[i]), ++fails;
            free(gs);
            free(fs);
        }
        for (size_t i = 0; i < np; ++i) {
            content_package p;
            content_package_at(s, i, &p);
            recs += p.record_count;
            total += content_package_resources(s, i, NULL, NULL, 0);
        }
        if (total != recs || total < n) printf("FAIL package records %zu vs %zu\n", total, recs), ++fails;
        if (content_package_resources(s, np, NULL, NULL, 0) != 0) printf("FAIL package_resources out of range\n"), ++fails;
        if (content_resource_packages(s, 0x0580000000FFFFFFULL, NULL, 0) != 0) printf("FAIL resource_packages unknown\n"), ++fails;
    }
    if (!content_package_manifest(s, 0)[0] || content_package_manifest(s, content_package_count(s))[0]) printf("FAIL manifest\n"), ++fails;
    content_close(s);

    /* Options: defaults, MD5-verified reads, extra keys. */
    content_options o;
    content_options_init(&o);
    if (!o.speech || o.verify_md5 || !o.builtin_keys || o.region || o.extra_key_count) printf("FAIL options_init\n"), ++fails;
    content_key k = {0x1122334455667788ULL, {1, 2, 3}};
    o.verify_md5 = 1;
    o.extra_keys = &k;
    o.extra_key_count = 1;
    s = content_open_ow08_ex(OW_INSTALL, &o, err, sizeof err);
    if (!s) {
        printf("FAIL open_ex: %s\n", err);
        return 1;
    }
    if (!content_read(s, 0x0400000000000039ULL, &data, &size, err, sizeof err) || memcmp(data, "DUTS", 4) != 0) printf("FAIL md5 read: %s\n", err), ++fails;
    content_free(data);
    content_close(s);
    o.extra_keys = NULL;
    err[0] = 0;
    if (content_open_ow08_ex(OW_INSTALL, &o, err, sizeof err) || !err[0]) printf("FAIL NULL extra_keys accepted\n"), ++fails;
    if (content_open_ow08_ex(NULL, NULL, err, sizeof err)) printf("FAIL NULL install\n"), ++fails;

    printf(fails ? "FAILED\n" : "ok\n");
    return fails ? 1 : 0;
}
