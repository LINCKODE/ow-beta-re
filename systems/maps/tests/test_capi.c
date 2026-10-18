/* C API test in plain C99: a synthetic map data with one light record. */
#include <stdio.h>
#include <string.h>

#include "maps.h"

static int failures = 0;
#define CHECK(c) do { if (!(c)) { printf("  FAIL %s:%d: %s\n", __FILE__, __LINE__, #c); failures++; } } while (0)

static void put32(unsigned char* d, size_t o, unsigned v) { memcpy(d + o, &v, 4); }

int main(void) {
    static unsigned char d[0x3f0];
    char err[128];
    float one = 1.f, five = 5.f;
    put32(d, 0x20c, 1);
    put32(d, 0x210 + 4 * 9, 0x320);
    put32(d, 0x250 + 4 * 9, 1);
    put32(d, 0x290 + 4 * 9, 0xd0);
    d[0x320 + 0x13] = 9;
    put32(d, 0x320 + 0x14, 0xd0);
    memcpy(d + 0x320 + 0x24, &one, 4);
    memcpy(d + 0x320 + 0x28, &five, 4);

    maps_doc* m = maps_open(d, sizeof d, err, sizeof err);
    CHECK(m != NULL);
    if (!m) { printf("  %s\n", err); return 1; }
    CHECK(maps_record_count(m) == 1);
    CHECK(maps_record_offset(m, 0) == 0x320);
    size_t n = 0;
    const char* js = maps_json(m, 1, 1, &n);
    CHECK(js && n > 0 && strstr(js, "\"light_type\"") != NULL);
    {
        float pos[3] = {1, 2, 3}, rot[4] = {0, 0, 0, 1};
        CHECK(maps_set_transform(m, 0x320, pos, NULL, rot) == MAPS_OK);
        CHECK(strstr(maps_json(m, 1, 0, NULL), "\"pos\":[1,2,3]") != NULL);
        CHECK(maps_set_transform(m, 0x999, pos, NULL, rot) == MAPS_ERR_ARG);
        CHECK(maps_set_definition(m, 0x320, 1) == MAPS_ERR_EDIT);
    }
    CHECK(maps_guid_type(0x0400000000000039ull) == 3);
    CHECK(maps_open(d, 0x100, err, sizeof err) == NULL);
    maps_close(m);
    printf("maps_capi_test: %d failed checks\n", failures);
    return failures ? 1 : 0;
}
