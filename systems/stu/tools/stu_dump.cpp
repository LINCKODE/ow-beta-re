// stu_dump: print a STUD v3 file (or an as-loaded client image) as JSON.
//   stu_dump FILE.stud                  raw file (CASC format)
//   stu_dump --loaded BASE FILE.mem     image dumped from the client, BASE = its address (hex)
//   stu_dump --summary FILE.stud        instance table only
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>

#include "stu/reader.h"

int main(int argc, char** argv) {
    stu::Mode mode = stu::Mode::File;
    uint64_t base = 0;
    bool summary = false, handles = false;
    const char* path = nullptr;
    for (int i = 1; i < argc; i++) {
        if (!std::strcmp(argv[i], "--loaded") && i + 1 < argc) {
            mode = stu::Mode::Loaded;
            base = std::strtoull(argv[++i], nullptr, 16);
        } else if (!std::strcmp(argv[i], "--summary")) {
            summary = true;
        } else if (!std::strcmp(argv[i], "--handles")) {
            handles = true;
        } else {
            path = argv[i];
        }
    }
    if (!path) {
        std::fprintf(stderr, "usage: stu_dump [--summary] [--handles] [--loaded BASE] FILE\n");
        return 2;
    }
    auto bytes = stu::ReadFile(path);
    if (bytes.empty()) {
        std::fprintf(stderr, "cannot read %s\n", path);
        return 1;
    }
    const auto& reg = stu::Registry::Client();
    stu::Document doc;
    std::string err;
    if (!stu::Document::Parse(bytes, reg, doc, err, mode, base)) {
        std::fprintf(stderr, "%s: %s\n", path, err.c_str());
        return 1;
    }
    std::fputs(stu::DocumentJson(doc, handles, summary).c_str(), stdout);
    for (const auto& in : doc.instances())
        if (!in.crc_ok) return 3;
    return 0;
}
