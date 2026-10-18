/* C API tests (plain C: proves capi/stu.h is usable without C++). Fixtures: fixtures/README.md. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "stu.h"

static int failures = 0;
#define CHECK(cond)                                                          \
    do {                                                                     \
        if (!(cond)) {                                                       \
            printf("  FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);         \
            failures++;                                                      \
        }                                                                    \
    } while (0)

static void fixture(char* out, size_t cap, const char* name) { snprintf(out, cap, "%s/%s", STU_FIXTURES, name); }

/* Base address of a loaded image, from its .mem.json ("base": N). */
static uint64_t loaded_base(const char* mem_path) {
    char path[1024], buf[4096];
    snprintf(path, sizeof path, "%s.json", mem_path);
    FILE* f = fopen(path, "rb");
    if (!f) return 0;
    size_t n = fread(buf, 1, sizeof buf - 1, f);
    fclose(f);
    buf[n] = 0;
    const char* p = strstr(buf, "\"base\":");
    return p ? strtoull(p + 7, NULL, 10) : 0;
}

static stu_doc* open_fixture(const char* name, int mode) {
    char path[1024], err[256];
    fixture(path, sizeof path, name);
    stu_doc* d = NULL;
    uint64_t base = mode == STU_MODE_LOADED ? loaded_base(path) : 0;
    int rc = stu_doc_open_file(path, mode, base, &d, err, sizeof err);
    if (rc != STU_OK) printf("  open %s: %d %s\n", name, rc, err);
    return d;
}

static int total_errors(stu_doc* d) {
    int n = 0;
    for (int i = 0; i < stu_doc_instance_count(d); i++) n += stu_value_error_count(stu_doc_decode(d, i));
    return n;
}

/* Instance the Object value points at, or -1. */
static int deref(const stu_value* v) {
    int idx = -1;
    return stu_value_object(v, &idx) == STU_OK ? idx : -1;
}

static void test_graph(void) {
    printf("graph_0F\n");
    stu_doc* d = open_fixture("graph_058000000000000F.stud", STU_MODE_FILE);
    CHECK(d != NULL);
    if (!d) return;
    CHECK(stu_doc_mode(d) == STU_MODE_FILE);
    CHECK(stu_doc_instance_count(d) == 352);
    CHECK(stu_doc_all_crc_ok(d));
    CHECK(stu_doc_root(d) == 0);
    CHECK(total_errors(d) == 0);

    stu_instance_info ii;
    CHECK(stu_doc_instance(d, 0, &ii) == STU_OK);
    CHECK(ii.type_hash == 0x0df7e24fu && ii.crc_ok); /* STUStatescriptGraph */
    CHECK(stu_doc_instance_at(d, ii.offset) == 0);
    CHECK(stu_doc_instance(d, 352, &ii) == STU_ERR_NOT_FOUND);
    int with_refs = 0;
    for (int i = 0; i < 352; i++) {
        stu_doc_instance(d, i, &ii);
        with_refs += ii.flags & 1;
    }
    CHECK(with_refs == 16);

    const stu_value* root = stu_doc_decode(d, 0);
    CHECK(root == stu_doc_decode(d, 0)); /* cached */
    CHECK(stu_value_type(root) == STU_VALUE_INLINE);
    stu_value_info vi;
    stu_value_get_info(root, &vi);
    CHECK(vi.type_hash == 0x0df7e24fu);

    /* Fields are listed in registry order, parents first. */
    stu_type_info ti;
    CHECK(stu_type(0x0df7e24fu, &ti) == STU_OK);
    CHECK((int)ti.field_count == stu_value_count(root));
    for (int i = 0; i < stu_value_count(root); i++) {
        stu_field_info fi;
        stu_type_field(0x0df7e24fu, i, &fi);
        stu_value_get_info(stu_value_item(root, i), &vi);
        CHECK(fi.hash == vi.field_hash && fi.kind == vi.kind);
    }

    const stu_value* nodes = stu_value_field(root, 0x51caba98); /* m_nodes */
    CHECK(stu_value_type(nodes) == STU_VALUE_LIST && stu_value_count(nodes) == 49);
    stu_value_get_info(nodes, &vi);
    for (int i = 0; i < stu_value_count(nodes); i++) {
        int idx = deref(stu_value_item(nodes, i));
        CHECK(idx > 0);
        CHECK(stu_doc_instance(d, idx, &ii) == STU_OK && stu_type_is_a(ii.type_hash, vi.type_hash));
    }
    CHECK(stu_value_count(stu_value_field(root, 0x7b5125b3)) == 17); /* m_states */
    CHECK(stu_value_count(stu_value_field(root, 0x77b7ff8c)) == 8);  /* m_entries */

    int64_t n = 0;
    CHECK(stu_value_int(stu_value_field(root, 0xf7eae932), &n) == STU_OK && n == 6); /* m_nodesBitCount */

    const stu_value* graph = stu_value_field(root, 0x45b2bab8); /* m_graph (inline STUGraph) */
    stu_value_get_info(graph, &vi);
    CHECK(vi.type == STU_VALUE_INLINE && vi.type_hash == 0xcb52d391u && vi.kind == STU_KIND_INLINE);
    double x = 0;
    const stu_value* extent = stu_value_field(stu_value_field(graph, 0x396044b9), 0xe4c8dd49);
    CHECK(stu_value_float(extent, &x) == STU_OK && x > -277.2181 && x < -277.2179);
    CHECK(stu_value_int(extent, &n) == STU_ERR_TYPE);

    const char* s = NULL;
    size_t len = 99;
    const stu_value* cc = stu_value_field(root, 0x682b3b61); /* m_consoleCommand */
    CHECK(stu_value_string(cc, &s, &len) == STU_OK && s != NULL && len == 0);
    CHECK(stu_value_string(nodes, &s, &len) == STU_ERR_TYPE);
    CHECK(stu_value_field(root, 0x12345678) == NULL);
    CHECK(stu_value_item(nodes, 49) == NULL);
    CHECK(stu_value_error(root) == NULL);

    char* json = stu_value_json(root, 0);
    CHECK(json && strncmp(json, "{\"$type\": \"STUStatescriptGraph\"", 31) == 0);
    stu_free(json);
    CHECK(strcmp(stu_name(0x51caba98), "m_nodes") == 0);
    CHECK(stu_name(0x12345678) == NULL);
    CHECK(stu_type_count() == 3639);
    CHECK(strcmp(stu_kind_name(STU_KIND_MAP), "map") == 0);
    stu_doc_close(d);
}

static void test_entitydef(void) {
    printf("entitydef_39\n");
    stu_doc* d = open_fixture("entitydef_0400000000000039.stud", STU_MODE_FILE);
    CHECK(d != NULL);
    if (!d) return;
    CHECK(stu_doc_instance_count(d) == 23 && stu_doc_all_crc_ok(d) && total_errors(d) == 0);
    const stu_value* root = stu_doc_decode(d, 0);
    stu_value_info vi;
    stu_value_get_info(root, &vi);
    CHECK(vi.type_hash == 0xec23fffdu); /* STUEntityDefinition */

    const stu_value* comps = stu_value_field(root, 0xa8f462ea); /* m_componentMap */
    CHECK(stu_value_type(comps) == STU_VALUE_MAP && stu_value_count(comps) == 3);
    for (int i = 0; i < stu_value_count(comps); i++) {
        const stu_value* e = stu_value_item(comps, i);
        stu_instance_info ii;
        stu_value_get_info(e, &vi);
        CHECK(stu_doc_instance(d, deref(e), &ii) == STU_OK && ii.type_hash == vi.map_key);
    }
    CHECK(deref(stu_value_map_get(comps, 0x81ebdcd0)) == 21);
    CHECK(stu_value_map_get(comps, 0x1) == NULL);

    /* The Statescript component's three graphs. */
    const stu_value* ssc = stu_doc_decode(d, deref(stu_value_map_get(comps, 0x9b38211b)));
    const stu_value* graphs = stu_value_field(ssc, 0x0175ad85);
    CHECK(stu_value_count(graphs) == 3);
    const uint64_t want[3] = {0x0580000000000029ull, 0x058000000000008full, 0x05800000000001e1ull};
    for (int i = 0; i < 3; i++) {
        uint64_t guid = 0, handle = 0;
        CHECK(stu_value_ref(stu_value_field(stu_value_item(graphs, i), 0x45b2bab8), &guid, &handle) == STU_OK);
        CHECK(guid == want[i] && handle == ~0ull);
    }
    /* Voice set component: resource reference. */
    uint64_t guid = 0;
    CHECK(stu_value_ref(stu_value_field(stu_doc_decode(d, 1), 0x6c7b8ce4), &guid, NULL) == STU_OK &&
          guid == 0x07a000000000001dull);
    stu_doc_close(d);
}

/* The raw file and the loaded image give the same document JSON; handles differ (-1 vs resolved). */
static void test_file_equals_loaded(const char* stud, const char* mem) {
    printf("file == loaded: %s\n", stud);
    stu_doc* f = open_fixture(stud, STU_MODE_FILE);
    stu_doc* m = open_fixture(mem, STU_MODE_LOADED);
    CHECK(f && m);
    if (f && m) {
        CHECK(stu_doc_mode(m) == STU_MODE_LOADED && stu_doc_all_crc_ok(m) && total_errors(m) == 0);
        char* a = stu_doc_json(f, 0);
        char* b = stu_doc_json(m, 0);
        CHECK(a && b && strcmp(a, b) == 0);
        stu_free(a);
        stu_free(b);
    }
    stu_doc_close(f);
    stu_doc_close(m);
}

static void test_errors(void) {
    printf("errors\n");
    char path[1024], err[128];
    fixture(path, sizeof path, "entitydef_0400000000000039.stud");
    FILE* fp = fopen(path, "rb");
    CHECK(fp != NULL);
    if (!fp) return;
    uint8_t buf[4096];
    size_t n = fread(buf, 1, sizeof buf, fp);
    fclose(fp);

    stu_doc* d = (stu_doc*)1;
    CHECK(stu_doc_open(buf, n, 7, 0, &d, err, sizeof err) == STU_ERR_ARG && d == NULL);
    CHECK(stu_doc_open(buf, n, STU_MODE_FILE, 0, NULL, err, sizeof err) == STU_ERR_ARG);
    buf[0] ^= 1;
    CHECK(stu_doc_open(buf, n, STU_MODE_FILE, 0, &d, err, sizeof err) == STU_ERR_PARSE && d == NULL);
    CHECK(strcmp(err, "bad magic") == 0);
    buf[0] ^= 1;
    CHECK(stu_doc_open(buf, 8, STU_MODE_FILE, 0, &d, err, 4) == STU_ERR_PARSE && strcmp(err, "too") == 0);
    CHECK(stu_doc_open_file("/nonexistent.stud", STU_MODE_FILE, 0, &d, err, sizeof err) == STU_ERR_NOT_FOUND);
    /* Every truncation fails to open or decodes without crashing. */
    for (size_t k = 0; k < n; k += 7) {
        if (stu_doc_open(buf, k, STU_MODE_FILE, 0, &d, NULL, 0) != STU_OK) continue;
        total_errors(d);
        stu_doc_close(d);
    }
    /* NULL handles are rejected, not dereferenced. */
    CHECK(stu_doc_instance_count(NULL) == 0 && stu_doc_decode(NULL, 0) == NULL && stu_value_type(NULL) == -1);
    CHECK(stu_value_field(NULL, 1) == NULL && stu_value_json(NULL, 0) == NULL && stu_doc_json(NULL, 0) == NULL);
    int64_t x;
    CHECK(stu_value_int(NULL, &x) == STU_ERR_ARG);
    stu_doc_close(NULL);
}

int main(void) {
    test_graph();
    test_entitydef();
    test_file_equals_loaded("graph_058000000000000F.stud", "graph_058000000000000F.mem");
    test_file_equals_loaded("entitydef_0400000000000039.stud", "entitydef_0400000000000039.mem");
    test_errors();
    printf(failures ? "%d FAILED\n" : "all passed\n", failures);
    return failures ? 1 : 0;
}
