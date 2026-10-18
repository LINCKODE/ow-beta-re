// ss_census: node-type census of Statescript graphs read from CASC bytes (content_dump --read), as JSON lines:
//   ss_census <dir of <GUID16>.stud files>
// One line per graph: {"graph", "refs": [graph GUIDs it references], "nodes": [[type, kind, flags], …] (m_nodes
// order; flags 1 client-only, 2 server-only, 4 remotely synced), "cvs": {config-var type: count}}.
//   ss_census --caps: the runtime's capability of every client type (JSON).
// Input for tools/node_priorities.py (docs/StatescriptNodes.md §2).
#include <algorithm>
#include <cstdio>
#include <filesystem>
#include <map>
#include <string>

#include "statescript/capabilities.h"
#include "statescript/graph.h"

namespace {
void CollectRefs(const stu::Value& v, std::vector<uint64_t>& out) {
    if (v.type == stu::Value::Type::Ref && (v.guid >> 52) == 0x058 && v.guid)
        if (std::find(out.begin(), out.end(), v.guid) == out.end()) out.push_back(v.guid);
    for (auto& c : v.items) CollectRefs(c, out);
}
}  // namespace

int main(int argc, char** argv) {
    if (argc < 2) {
        std::fprintf(stderr, "usage: ss_census <dir of .stud graphs> | ss_census --caps\n");
        return 2;
    }
    if (std::string(argv[1]) == "--caps") {  // the runtime's capability of every client type, as JSON
        std::printf("[\n");
        bool first = true;
        for (auto& t : ss::ClientTypes()) {
            ss::TypeCap c = ss::Capability(t.type);
            std::printf("%s{\"type\":\"%08x\",\"level\":%d,\"local\":%d,\"remote\":%d}", first ? "" : ",\n", t.type,
                        int(c.level), int(c.local), int(c.remote));
            first = false;
        }
        std::printf("\n]\n");
        return 0;
    }
    std::vector<std::filesystem::path> files;
    for (auto& e : std::filesystem::directory_iterator(argv[1]))
        if (e.path().extension() == ".stud") files.push_back(e.path());
    std::sort(files.begin(), files.end());
    for (auto& f : files) {
        uint64_t guid = std::stoull(f.stem().string(), nullptr, 16);
        std::string err;
        auto g = ss::Graph::Load(stu::ReadFile(f.string()), guid, err, stu::Mode::File);
        if (!g) {
            std::fprintf(stderr, "%016llx: %s\n", (unsigned long long)guid, err.c_str());
            continue;
        }
        std::vector<uint64_t> refs;
        for (int i = 0; i < g->instance_count(); i++) CollectRefs(g->value(i), refs);
        std::erase(refs, guid);
        std::map<uint32_t, int> cvs;
        for (int i = 0; i < g->instance_count(); i++) {
            const ss::ClientType* ct = ss::FindClientType(g->type_of(i));
            if (ct && ct->kind == ss::TypeKind::ConfigVar) cvs[ct->type]++;
        }
        std::printf("{\"graph\":\"0x%016llx\",\"refs\":[", (unsigned long long)guid);
        for (size_t k = 0; k < refs.size(); k++) std::printf("%s\"0x%016llx\"", k ? "," : "", (unsigned long long)refs[k]);
        std::printf("],\"nodes\":[");
        bool first = true;
        for (auto& n : g->nodes()) {
            int flags = (n.client_only ? 1 : 0) | (n.server_only ? 2 : 0) | (n.remote_index >= 0 ? 4 : 0);
            std::printf("%s[\"%08x\",%d,%d]", first ? "" : ",", n.type, int(n.kind), flags);
            first = false;
        }
        std::printf("],\"cvs\":{");
        first = true;
        for (auto& [t, c] : cvs) std::printf("%s\"%08x\":%d", first ? "" : ",", t, c), first = false;
        std::printf("}}\n");
    }
}
