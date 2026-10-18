// ss_scan: node-feature census of Statescript graph images (to choose graphs for new traces).
//   ss_scan <graphs dir with meta.json + <guid>.mem> [graph guid]   (with a guid: the feature nodes of that graph)
// Prints per graph: nodes, states, Stack states, logical-button states/conditions, sub-script states,
// PlayScript actions/entries, linked transition plugs, remotely synced nodes, prediction behaviour.
#include <cstdio>
#include <fstream>
#include <regex>
#include <string>

#include "statescript/graph.h"

int main(int argc, char** argv) {
    if (argc < 2) {
        std::fprintf(stderr, "usage: ss_scan <graphs dir>\n");
        return 2;
    }
    std::string dir = argv[1];
    std::ifstream f(dir + "/meta.json");
    std::string meta((std::istreambuf_iterator<char>(f)), {});
    std::regex re("\"0x([0-9a-f]{16})\": \\{\"base\": ([0-9]+)");
    std::printf("graph              nodes states stack button subscript playscript transitions remote predict\n");
    for (auto it = std::sregex_iterator(meta.begin(), meta.end(), re); it != std::sregex_iterator(); ++it) {
        uint64_t guid = std::stoull((*it)[1], nullptr, 16), base = std::stoull((*it)[2]);
        if (argc > 2 && guid != std::stoull(argv[2], nullptr, 16)) continue;
        std::string err;
        auto g = ss::Graph::Load(stu::ReadFile(dir + "/" + (*it)[1].str() + ".mem"), guid, err, stu::Mode::Loaded, base);
        if (!g) {
            std::printf("%016llx load failed: %s\n", (unsigned long long)guid, err.c_str());
            continue;
        }
        int stack = 0, button = 0, subscript = 0, playscript = 0, transitions = 0;
        for (auto& n : g->nodes()) {
            switch (n.type) {
            case 0xf7d6498c: stack++; break;
            case 0xff77458c: case 0xd689c470: case 0x4768e4a3: case 0x92f7915f: button++; break;
            case 0x3db862c4: subscript++; break;
            case 0xf9ef59a6: case 0x46c48944: playscript++; break;
            }
            bool tr = false;
            for (auto& p : n.plugs)
                if (p.field == ss::Hash("m_transitionPlug") && !p.links.empty()) transitions++, tr = true;
            if (argc > 2) {
                const char* what = n.type == 0xf7d6498c ? "Stack" : n.type == 0xff77458c ? "LogicalButton state"
                                 : n.type == 0xd689c470 ? "LogicalButtonClientOnly state" : n.type == 0x4768e4a3 ? "LogicalButton condition"
                                 : n.type == 0x3db862c4 ? "SubScript state" : n.type == 0xf9ef59a6 ? "PlayScript action"
                                 : n.type == 0x46c48944 ? "PlayScript entry" : tr ? "state with linked transitions" : nullptr;
                if (what) {
                    std::printf("  node %3d state %3d remote %3d %s", n.index, n.state_index, n.remote_index, what);
                    for (auto& p : n.plugs)
                        if (p.field == ss::Hash("m_transitionPlug"))
                            for (auto& l : p.links) std::printf(" [t%d -> %d]", p.index, l.target);
                    std::printf("\n");
                }
            }
        }
        std::printf("%016llx %6zu %6zu %5d %6d %9d %10d %11d %6zu %7u\n", (unsigned long long)guid, g->nodes().size(),
                    g->states().size(), stack, button, subscript, playscript, transitions, g->remote_nodes().size(),
                    g->prediction_behavior());
    }
}
