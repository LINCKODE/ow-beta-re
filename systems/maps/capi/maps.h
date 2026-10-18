/* C API over the map data reader/writer (systems/maps), for cgo (systems/maps/go, the explorer, the servers).
 * Format and library: docs/Maps.md §2, §7. No exceptions or STL across the boundary.
 *
 * A maps_doc owns a copy of the bytes it was opened with; edits (maps_set_transform, maps_add_entity)
 * change that copy and re-parse it. Strings returned by maps_json/maps_error stay valid until the next call
 * on the same document or maps_close. A document is not thread-safe. */
#ifndef OW_MAPS_CAPI_H
#define OW_MAPS_CAPI_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct maps_doc maps_doc;

enum { MAPS_OK = 0, MAPS_ERR_ARG = -1, MAPS_ERR_PARSE = -2, MAPS_ERR_EDIT = -3 };

/* Opens (copies + parses) a map data blob. On failure returns NULL and, if err is not NULL, writes a
 * message (up to errlen bytes, NUL-terminated). */
maps_doc* maps_open(const uint8_t* data, size_t len, char* err, size_t errlen);
void maps_close(maps_doc* d);

/* JSON of the map (docs/Maps.md §7): records = include the record list, fields = include per-record fields. */
const char* maps_json(maps_doc* d, int records, int fields, size_t* len);

/* The document's current bytes (after edits). Valid until the next edit or maps_close. */
const uint8_t* maps_bytes(maps_doc* d, size_t* len);

size_t maps_record_count(maps_doc* d);
/* Record by position in the record list (JSON "records" order); -1 if out of range. */
int32_t maps_record_offset(maps_doc* d, size_t i);

/* Rewrites the transform of the record at map-data offset `record_offset`. pos[3], scale[3], rot[4] (x,y,z,w). */
int maps_set_transform(maps_doc* d, uint32_t record_offset, const float* pos, const float* scale, const float* rot);
/* Sets an entity record's definition. */
int maps_set_definition(maps_doc* d, uint32_t record_offset, uint64_t def);
/* Appends a copy of the entity record at `src_offset` with a new placement GUID (16 bytes) and transform. */
int maps_add_entity(maps_doc* d, uint32_t src_offset, const uint8_t* uuid16, const float* pos, const float* scale,
                    const float* rot);

/* Last error message of the document ("" if none). */
const char* maps_error(maps_doc* d);

/* Resource type of a GUID (bitreverse12((guid >> 48) & 0xFFF) + 1). */
uint32_t maps_guid_type(uint64_t guid);

#ifdef __cplusplus
}
#endif
#endif
