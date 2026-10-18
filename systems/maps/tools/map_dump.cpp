// map_dump: parse a map data blob (a file, or a GUID read from the install) and print JSON / a summary.
//   map_dump [--install DIR] [--records] [--fields] [--summary] (FILE | GUID)
#include <cstdio>
#include <cstring>
#include <fstream>
#include <iterator>
#include <map>
#include <string>

#include "content/store.h"
#include "maps/mapdata.h"

int main(int argc, char** argv) {
    std::string install = getenv("OW_INSTALL") ? getenv("OW_INSTALL") : "game/owbetaclient", what;
    bool records = false, fields = false, summary = false;
    for (int i = 1; i < argc; i++) {
        if (!std::strcmp(argv[i], "--install") && i + 1 < argc) install = argv[++i];
        else if (!std::strcmp(argv[i], "--records")) records = true;
        else if (!std::strcmp(argv[i], "--fields")) records = fields = true;
        else if (!std::strcmp(argv[i], "--summary")) summary = true;
        else what = argv[i];
    }
    if (what.empty()) {
        std::fprintf(stderr, "usage: map_dump [--install DIR] [--records] [--fields] [--summary] (FILE | GUID)\n");
        return 2;
    }
    maps::Bytes b;
    std::ifstream f(what, std::ios::binary);
    if (f) {
        b.assign(std::istreambuf_iterator<char>(f), {});
    } else {
        content::Error err;
        auto s = content::OpenOw08Install(install, {}, &err);
        if (!s) {
            std::fprintf(stderr, "open %s: %s\n", install.c_str(), err.message.c_str());
            return 1;
        }
        if (!s->Read(std::stoull(what, nullptr, 16), b, &err)) {
            std::fprintf(stderr, "read %s: %s\n", what.c_str(), err.message.c_str());
            return 1;
        }
    }
    maps::Error err;
    auto m = maps::Parse(b, &err);
    if (!m) {
        std::fprintf(stderr, "parse: %s\n", err.message.c_str());
        return 1;
    }
    if (summary) {
        std::printf("%u bytes, %u records, %u placed, gamemode def %#018llx, lighting %#018llx\n", m->size,
                    m->header.record_count, m->placed, (unsigned long long)m->header.env.gamemode_def,
                    (unsigned long long)m->header.env.lighting);
        for (int t = 0; t < maps::kNumRecordTypes; t++)
            if (m->header.counts[t])
                std::printf("  type %2d %-17s %5u records at %#x (%u bytes)\n", t, maps::RecordTypeName(t),
                            m->header.counts[t], m->header.offsets[t], m->header.sizes[t]);
        std::printf("  collision: %zu spheres, %zu capsules, %zu hulls, %zu meshes", m->collision.spheres.size(),
                    m->collision.capsules.size(), m->collision.hulls.size(), m->collision.meshes.size());
        for (auto& c : m->collision.meshes) std::printf(" (%u tris, %u verts)", c.tri_count, c.vert_count);
        std::printf("\n  nav %u bytes, tactical %u bytes, %zu sound-space links, blobs %#x..%#x\n", m->nav_size,
                    m->tactical_size, m->sound_spaces.size(), m->blob_begin, m->blob_end);
        return 0;
    }
    std::puts(maps::ToJson(*m, records, fields).c_str());
    return 0;
}
