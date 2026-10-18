/* C API smoke test: an instance of the door graph on an ECS entity, stepped, with its sync state. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "statescript.h"

static int failures = 0;
#define CHECK(c) do { if (!(c)) { printf("  FAIL %s:%d: %s\n", __FILE__, __LINE__, #c); failures++; } } while (0)

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

int main(void) {
    size_t len = 0;
    unsigned char* bytes = read_file(SS_DATA "/0580000000000042.mem", &len);
    CHECK(bytes != NULL);
    if (!bytes) return 1;
    ss_runtime* rt = ss_runtime_create();
    CHECK(ss_runtime_load_graph(rt, 0x0580000000000042ull, bytes, len, SS_MODE_LOADED, 1463812096ull) == SS_OK);
    free(bytes);

    ss_world* w = ss_world_create(rt, SS_ROLE_CLIENT);
    unsigned char comps[] = {0x23};
    uint32_t ent = ss_world_spawn(w, comps, 1);
    CHECK(ent != 0);
    ss_sm* sm = ss_world_sm(w, ent);
    CHECK(sm != NULL);
    if (!sm) return 1;
    ss_sm_set_clock(sm, 100, 100 * 16 + 15);
    int32_t id = ss_sm_create_instance(sm, 0x0580000000000042ull, NULL, NULL, 0, 0);
    CHECK(id == -1);
    ss_sm_run(sm, 101, 1);
    int32_t states[8];
    int n = ss_sm_active_states(sm, (int16_t)id, states, 8);
    /* closed branch: BooleanSwitch (state 2) + closed Anim (state 1) */
    CHECK(n == 2);
    int32_t remote[8], st[8];
    int nr = ss_sm_active_remote_states(sm, (int16_t)id, remote, st, 8);
    CHECK(nr == 1 && st[0] == 1 && remote[0] == 1);
    uint64_t vars[16];
    ss_value vals[16];
    int nv = ss_sm_sync_vars(sm, (int16_t)id, vars, vals, 16);
    CHECK(nv > 0);
    ss_value v;
    CHECK(ss_sm_get_var(sm, (int16_t)id, 0x0D80000000000192ull, &v) == SS_OK && v.type == 3 && v.bits == 0);
    /* door states: 1 Anim (data: local writer), 2 BooleanSwitch (value bit, local = remote format) */
    unsigned char bits[8];
    CHECK(ss_sm_state_data2(sm, (int16_t)id, 2, 1, 0, bits, 64) == 1);
    CHECK(ss_sm_state_data2(sm, (int16_t)id, 2, 0, 0, bits, 64) == 1);
    int16_t refs[4];
    CHECK(ss_sm_state_data_refs(sm, (int16_t)id, 2, 1, refs, 4) == 0);
    CHECK(ss_sm_actions(sm, 0, NULL, 0) == 0); /* local instance: no action records */

    /* capabilities */
    int nt = ss_runtime_type_caps(rt, NULL, 0);
    CHECK(nt == 719);
    ss_type_cap c;
    CHECK(ss_runtime_type_cap(rt, 0x65f158da, &c) == SS_OK && c.kind == SS_KIND_STATE && c.level == SS_LEVEL_VERIFIED &&
          c.data_local == SS_DATA_NONE);                                           /* Wait */
    CHECK(ss_runtime_type_cap(rt, 0xf7d6498c, &c) == SS_OK && c.data_local == SS_DATA_WRITER &&
          c.data_remote == SS_DATA_ECHO);                                          /* Stack: remote format differs */
    CHECK(ss_runtime_type_cap(rt, 0x619d9e95, &c) == SS_OK && c.level == SS_LEVEL_DESIGNED && c.host); /* ModifyHealth */
    CHECK(ss_runtime_type_cap(rt, 0xffbfa88d, &c) == SS_OK && c.has_update && c.data_local == SS_DATA_WRITER); /* Effect: u2 serial */
    CHECK(ss_runtime_type_cap(rt, 0x4698e4b6, &c) == SS_OK && c.kind == SS_KIND_NODE);          /* SetVar */
    CHECK(ss_runtime_type_cap(rt, 0x7b13a0d4, &c) == SS_OK && c.kind == SS_KIND_CONFIG_VAR && c.level == SS_LEVEL_EXACT);
    CHECK(ss_runtime_type_cap(rt, 0x12345678, &c) == SS_ERR_NOT_FOUND);
    uint32_t types[64];
    CHECK(ss_runtime_graph_node_types(rt, 0x0580000000000042ull, types, 64) == 28);

    CHECK(ss_sm_host_events(sm, 0, NULL, 0) == 0);
    CHECK(ss_sm_end_state(sm, (int16_t)id, 1, 1) == SS_OK); /* the door's Anim finishes */
    ss_sm_run(sm, 102, 1);
    int32_t states2[8];
    CHECK(ss_sm_active_states(sm, (int16_t)id, states2, 8) >= 1 && ss_sm_state_changes(sm, (int16_t)id, 1) >= 2);
    int32_t bc[4];
    CHECK(ss_runtime_graph_bit_counts(rt, 0x0580000000000042ull, bc) == SS_OK && bc[0] == 5 && bc[1] == 3);
    ss_remote_node rn[8];
    int nrn = ss_runtime_graph_remote_nodes(rt, 0x0580000000000042ull, rn, 8);
    CHECK(nrn >= 2 && rn[1].state == 1); /* the door's Anim is remote index 1 */

    ss_world_destroy(w);
    ss_runtime_destroy(rt);
    printf("%s capi (%d sync vars)\n", failures ? "FAIL" : "ok  ", nv);
    return failures ? 1 : 0;
}
