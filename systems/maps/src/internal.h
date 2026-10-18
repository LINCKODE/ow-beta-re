#pragma once
// Shared between the parser and the writer.
#include "maps/mapdata.h"

namespace maps {

// Where a record's pos, scale and rot sit (record offsets; 0 = absent). Record::xf_layout:
// 1 = pos, scale, rot contiguous (0x28 bytes); 2 = rot then pos (lights, occluders, text labels);
// 3 = rot, pos, then a uniform scale float at pos + 0x28 (map text).
struct XfLayout {
    uint32_t pos = 0, scale = 0, rot = 0, uniform_scale = 0;
};
inline XfLayout LayoutOf(const Record& r) {
    switch (r.xf_layout) {
    case 1: return {r.xf_at, r.xf_at + 12, r.xf_at + 24, 0};
    case 2: return {r.xf_at, 0, r.xf_at - 16, 0};
    case 3: return {r.xf_at, 0, r.xf_at - 16, r.xf_at + 0x28};
    default: return {};
    }
}

}  // namespace maps
