/* Match hosting through the C API (T057, docs/InstanceServer.md §12.4): the Anubis assault flow of
 * tests/test_match.cpp (brain B, capture points A and B with their placements' overrides and area data), driven the way
 * the server drives it: facts pushed in, rules-layer messages, steps, effects drained. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "statescript.h"

static int failures = 0;
#define CHECK(c) do { if (!(c)) { printf("  FAIL %s:%d: %s\n", __FILE__, __LINE__, #c); failures++; } } while (0)

#define VAR(x) (0x0D80000000000000ull | (x))
#define MSG(x) (0x0240000000000000ull | (x))
#define PARAM(x) (0x0A40000000000000ull | (x))
#define GRAPH(x) (0x0580000000000000ull | (x))
#define CF(s) ((uint32_t)((s) * 1000.0 / 16.0 + 0.5))

enum { BRAIN = 0x40000030, POINT_A = 0x40000031, POINT_B = 0x40000032, HERO = 0x40000002, CONTROLLER = 0x40000001 };

static unsigned char* read_file(const char* path, size_t* len) {
    FILE* f = fopen(path, "rb");
    if (!f) return NULL;
    fseek(f, 0, SEEK_END);
    *len = (size_t)ftell(f);
    fseek(f, 0, SEEK_SET);
    unsigned char* b = (unsigned char*)malloc(*len);
    if (fread(b, 1, *len, f) != *len) { free(b); b = NULL; }
    fclose(f);
    return b;
}

/* Loads a graph from fixtures/ and, recursively, the graphs it references that have a fixture. */
static void load(ss_runtime* rt, uint64_t guid) {
    char path[512];
    snprintf(path, sizeof path, "%s/graph_0x%03x.stud", SS_FIXTURES, (unsigned)(guid & 0xFFF));
    size_t len = 0;
    unsigned char* b = read_file(path, &len);
    if (!b) return;
    int rc = ss_runtime_load_graph(rt, guid, b, len, SS_MODE_FILE, 0);
    free(b);
    if (rc != SS_OK) return;
    uint64_t refs[64];
    int n = ss_runtime_graph_refs(rt, guid, refs, 64);
    for (int k = 0; k < n && k < 64; k++) {
        uint64_t probe;
        if (ss_runtime_graph_refs(rt, refs[k], &probe, 0) < 0) load(rt, refs[k]); /* not loaded yet */
    }
}

static ss_value int_value(int64_t v) { ss_value x = {2, (uint64_t)v, 0}; return x; }
static ss_value byte_value(int v) { ss_value x = {1, (uint64_t)(v != 0), 0}; return x; }
static ss_value int64_value(uint64_t v) { ss_value x = {4, v, 0}; return x; }

/* Drains the effects; returns how many of `kind` for `entity` (0 = any) were seen, the last one in *last. */
static int drain(ss_match* m, int kind, uint32_t entity, ss_match_effect* last) {
    ss_match_effect fx[256];
    int seen = 0, n;
    while ((n = ss_match_effects(m, fx, 256)) > 0)
        for (int k = 0; k < n; k++)
            if (fx[k].kind == kind && (!entity || fx[k].entity == entity)) {
                seen++;
                if (last) *last = fx[k];
            }
    return seen;
}

int main(void) {
    ss_runtime* rt = ss_runtime_create();
    load(rt, GRAPH(0x029));
    load(rt, GRAPH(0x08f));
    load(rt, GRAPH(0x1e1));
    load(rt, GRAPH(0x028));
    uint64_t probe;
    CHECK(ss_runtime_graph_refs(rt, GRAPH(0x028), &probe, 0) >= 0);

    ss_match* m = ss_match_create(rt, 1000, 7);
    CHECK(m != NULL);
    CHECK(ss_match_frame(m) == 1000);

    /* Brain B (def 0x39): the gamemode entity. */
    CHECK(ss_match_add(m, BRAIN, 0x0400000000000039ull, SS_ME_SM | SS_ME_PVP) == SS_OK);
    CHECK(ss_match_add(m, BRAIN, 0, SS_ME_SM) == SS_ERR_EXISTS);
    CHECK(ss_match_start(m, BRAIN, GRAPH(0x029), NULL, NULL, 0) > 0);
    CHECK(ss_match_start(m, BRAIN, GRAPH(0x08f), NULL, NULL, 0) > 0);
    CHECK(ss_match_start(m, BRAIN, GRAPH(0x1e1), NULL, NULL, 0) > 0);

    /* Capture points with the placements' overrides and area data (Anubis A = placed 470, B = 469). */
    ss_capture_rules ra = {10, 0, 3, 1, 30, 30, 5, 0, 1, 0, 3, 6};
    ss_capture_rules rb = {7, 0, 3, 0.5f, 18, 22, 5, 1, 1, 1, 3, 6};
    CHECK(ss_match_add(m, POINT_A, 0x0400000000000037ull, SS_ME_SM) == SS_OK);
    CHECK(ss_match_set_transform(m, POINT_A, -43.780807f, 1.6629558f, 45.898396f, 0) == SS_OK);
    CHECK(ss_match_set_capture_area(m, POINT_A, &ra) == SS_OK);
    uint64_t va[] = {VAR(0x02b8), VAR(0x0c18)};
    ss_value xa[] = {int_value(1), int_value(1)};
    CHECK(ss_match_start(m, POINT_A, GRAPH(0x028), va, xa, 2) > 0);
    CHECK(ss_match_add(m, POINT_B, 0x0400000000000037ull, SS_ME_SM) == SS_OK);
    CHECK(ss_match_set_transform(m, POINT_B, -7.5f, 4.7f, 111.3f, 0) == SS_OK);
    CHECK(ss_match_set_capture_area(m, POINT_B, &rb) == SS_OK);
    uint64_t vb[] = {VAR(0x008d), VAR(0x0090), VAR(0x02b8), VAR(0x01a3), VAR(0x0780), VAR(0x0c18), VAR(0x089f)};
    ss_value xb[] = {int_value(1), byte_value(1), int_value(2), int_value(0), int_value(1), int_value(2), byte_value(0)};
    CHECK(ss_match_start(m, POINT_B, GRAPH(0x028), vb, xb, 7) > 0);
    CHECK(ss_match_start(m, POINT_B, GRAPH(0xfff), NULL, NULL, 0) < -0xffff); /* not loaded */

    /* The player's hero: fact-only (its Statescript runs in the hero host), team 1 (attack), at the spawn. */
    CHECK(ss_match_add(m, HERO, 0x0400000000000225ull, SS_ME_PLAYER) == SS_OK);
    CHECK(ss_match_sm(m, HERO) == NULL);
    CHECK(ss_match_sm(m, POINT_A) != NULL);
    CHECK(ss_match_set_player(m, HERO, CONTROLLER, 0x02E0000000000029ull) == SS_OK);
    CHECK(ss_match_set_team(m, HERO, 1) == SS_OK);
    CHECK(ss_match_set_transform(m, HERO, -13.9f, 0, -161.2f, 0) == SS_OK);
    CHECK(ss_match_reserve_ids(m, 0x40000100, 64) == SS_OK);

    ss_match_step(m, 1002);
    ss_capture_state cs;
    CHECK(ss_match_capture(m, POINT_A, &cs) == SS_OK && !cs.enabled);
    CHECK(ss_match_capture(m, HERO, &cs) == SS_ERR_NOT_FOUND);
    drain(m, -1, 0, NULL);

    /* Rules layer: init (owner = defenders, team 0) to each point, then "assault in progress". */
    uint64_t k10 = PARAM(0x10), k29 = PARAM(0x29);
    ss_value owner = int_value(0), phase = int64_value(VAR(0x15));
    CHECK(ss_match_send(m, POINT_A, MSG(0x0e), &k10, &owner, 1) == SS_OK);
    CHECK(ss_match_send(m, POINT_B, MSG(0x0e), &k10, &owner, 1) == SS_OK);
    ss_match_step(m, 1004);
    CHECK(ss_match_capture(m, POINT_A, &cs) == SS_OK && cs.owner == 0);
    CHECK(ss_match_send(m, 0, MSG(0x2d), &k29, &phase, 1) == SS_OK);
    ss_match_step(m, 1006);
    ss_match_effect fx;
    int enabled = drain(m, SS_FX_CAPTURE_ENABLED, POINT_A, &fx);
    CHECK(enabled == 1 && fx.i0 == 1);
    CHECK(ss_match_capture(m, POINT_A, &cs) == SS_OK && cs.enabled);
    CHECK(ss_match_capture(m, POINT_B, &cs) == SS_OK && !cs.enabled);

    /* The attacker walks onto A: 30 s for one capturer (designed rates [?]). */
    CHECK(ss_match_set_transform(m, HERO, -43.78f, 1.66f, 45.9f, 0) == SS_OK);
    ss_match_step(m, 1006 + CF(5));
    CHECK(ss_match_capture(m, POINT_A, &cs) == SS_OK && cs.progress > 0.14f && cs.progress < 0.19f && cs.capturing_team == 1);
    CHECK(drain(m, SS_FX_CAPTURE_PROGRESS, POINT_A, &fx) > 10 && fx.f[0] > 0.14f);
    ss_match_step(m, 1006 + CF(31));
    CHECK(ss_match_capture(m, POINT_A, &cs) == SS_OK && cs.progress >= 1.0f && !cs.enabled);
    CHECK(ss_match_capture(m, POINT_B, &cs) == SS_OK && cs.enabled);
    ss_match_effect mt;
    CHECK(drain(m, SS_FX_MATCH_TIME, 0, &mt) == 1 && mt.f[0] > 299 && mt.f[0] < 301); /* A's time bonus $01a3 */

    /* Player events reach the brain's player-event states (none on brain B: no effect, no crash). */
    CHECK(ss_match_player_event(m, 0, HERO, 0) == SS_OK);
    CHECK(ss_match_player_event(m, 3, HERO, 0) == SS_ERR_ARG);
    CHECK(ss_match_remove(m, HERO) == SS_OK);
    CHECK(ss_match_set_team(m, HERO, 0) == SS_ERR_NOT_FOUND);
    ss_match_step(m, ss_match_frame(m) + 10);

    ss_match_destroy(m);
    ss_runtime_destroy(rt);
    printf("%s capi match\n", failures ? "FAIL" : "ok  ");
    return failures ? 1 : 0;
}
