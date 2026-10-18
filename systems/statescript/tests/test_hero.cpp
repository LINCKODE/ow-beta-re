// Hero press replays (T041): a client snapshot of an idle hero is imported into a server-role sync manager
// (ss_sm_import_* semantics), the recorded LogicalButton notifications are queued at their CFs, and the
// runtime's state transitions and per-CF var changes are compared with the client's prediction of the press
// (fixtures/hero_*.fixture, made by tools/hero_fixture.py; docs/StatescriptRuntime.md §5.9).
//
// The client runs client-only nodes the server skips: expectations on client-only states (and on instance vars only
// client-only nodes name) are dropped, and
// var changes are compared per (bag, var). A mismatch report lists what is missing / extra per CF; the test
// fails on any difference of a state the server runs or of a var.
#include <cstdio>
#include <cstdlib>
#include <format>
#include <fstream>
#include <map>
#include <optional>
#include <set>
#include <sstream>
#include <string>
#include <vector>

#include "statescript/runtime.h"
#include "test.h"

using namespace ss;

namespace {

// The var ids a graph's nodes name in their config vars' dynamic paths, split by whether only client-only nodes
// name them (their writes are the client's business, like the client-only states).
std::set<uint64_t> ClientOnlyVars(const Graph& g) {
    std::set<int> node_insts;
    for (auto& n : g.nodes()) node_insts.insert(n.instance);
    std::set<uint64_t> co, other;
    for (auto& n : g.nodes()) {
        std::set<int> seen;
        auto walk = [&](auto& self, const stu::Value& v) -> void {
            if (v.type == stu::Value::Type::Object && v.instance >= 0) {
                if (node_insts.count(v.instance) || !seen.insert(v.instance).second) return;
                for (uint64_t id : g.DynamicPath(v.instance)) (n.client_only ? co : other).insert(id & 0xFFFFFFFFFFFFull);
                for (auto& f : g.value(v.instance).items) self(self, f);
                return;
            }
            for (auto& f : v.items) self(self, f);
        };
        if (n.fields)
            for (auto& f : n.fields->items) walk(walk, f);
    }
    for (uint64_t id : other) co.erase(id);
    return co;
}

std::unique_ptr<Graph> LoadImageGraph(uint64_t guid) {
    std::string meta;
    {
        std::ifstream f(std::string(SS_DATA) + "/meta.json");
        meta.assign(std::istreambuf_iterator<char>(f), {});
    }
    std::string key = std::format("\"0x{:016x}\": {{\"base\": ", guid);
    size_t at = meta.find(key);
    if (at == std::string::npos) return nullptr;
    uint64_t base = std::strtoull(meta.c_str() + at + key.size(), nullptr, 10);
    std::string err;
    auto g = Graph::Load(stu::ReadFile(std::format("{}/{:016x}.mem", SS_DATA, guid)), guid, err, stu::Mode::Loaded, base);
    if (!g) std::printf("  graph %016llx: %s\n", (unsigned long long)guid, err.c_str());
    return g;
}

// "INT 200", "FLT 0.25", "BYTE 1", "INT64 0x…", "ENTITY 0x…" (ss_trace text); others are not imported.
bool ParseValue(const std::string& type, const std::string& payload, Primitive& out) {
    if (type == "INT") out = Primitive::Int(int32_t(std::strtol(payload.c_str(), nullptr, 10)));
    else if (type == "FLT") out = Primitive::Float(std::strtof(payload.c_str(), nullptr));
    else if (type == "BYTE") out = Primitive::Byte(std::strtol(payload.c_str(), nullptr, 10) != 0);
    else if (type == "INT64") out = Primitive::Int64(std::strtoull(payload.c_str(), nullptr, 16));
    else if (type == "ENTITY") out = Primitive::Entity(uint32_t(std::strtoul(payload.c_str(), nullptr, 16)));
    else return false;
    return true;
}

std::vector<uint8_t> HexBytes(const std::string& h) {
    std::vector<uint8_t> out;
    for (size_t i = 0; h != "-" && i + 1 < h.size(); i += 2) out.push_back(uint8_t(std::strtoul(h.substr(i, 2).c_str(), nullptr, 16)));
    return out;
}

struct HeroReplay {
    EvaluatorRegistry evals = EvaluatorRegistry::Builtin();
    std::map<uint64_t, std::unique_ptr<Graph>> graphs;
    std::unique_ptr<SyncManager> sm;
    struct Button {
        uint32_t cf;
        int32_t button;
        bool down;
        std::vector<int16_t> insts;
    };
    std::vector<Button> buttons;
    struct Extern {
        uint32_t cf;
        int16_t inst;
        uint64_t var;
        Primitive value;
        int offset;  // ms into the CF at which the graphs see the write
    };
    std::vector<Extern> externs;  // writes by client code (movement system …), applied during their CF
    // When in its CF client code writes (fixture line `extern_at <ms>`, for the externs after it): by default the
    // CF's last ms (T041 [L]: the graphs react in the window's last pass; Genji's dash); a jump's movement writes
    // land before the CF's passes (T054: fitted to the recorded finish CF, not measured).
    int extern_offset = 15;
    // Host answers the client's game systems gave during the recording (collision, movement).
    std::optional<SyncManager::RaycastHit> raycast_hit;
    struct HostEnd {
        uint32_t cf;
        int offset, inst, state, finish;
    };
    std::vector<HostEnd> host_ends;
    uint32_t clock = 0, until = 0;
    std::multiset<std::string> expect, got;
    bool use_input = false;                          // drive through SyncManager::Input (the client's command)
    std::map<std::string, std::vector<int16_t>> routed;  // "cf button down" -> instances that got it
    std::map<std::string, std::string> base;  // bag:var -> value text, for per-CF diffs
    int imported_states = 0, skipped_vars = 0, roundtrip_failures = 0;

    bool Load(const std::string& file) {
        std::ifstream f(std::string(SS_FIXTURES) + "/" + file);
        if (!f) return false;
        std::string line;
        uint32_t entity = 0;
        struct St {
            int16_t inst;
            int state, nbits;
            std::string hex;
        };
        std::vector<St> states;
        std::vector<std::tuple<int16_t, uint64_t, Primitive>> vars;
        std::vector<std::tuple<int16_t, uint64_t, std::vector<std::pair<Primitive, Primitive>>>> dicts;
        std::vector<std::tuple<int16_t, uint64_t, int16_t>> insts;
        while (std::getline(f, line)) {
            if (line.empty() || line[0] == '#') continue;
            std::istringstream in(line);
            std::string k;
            in >> k;
            if (k == "entity") {
                std::string e;
                in >> e;
                entity = uint32_t(std::strtoul(e.c_str(), nullptr, 16));
            } else if (k == "graph") {
                std::string g;
                in >> g;
                uint64_t guid = std::strtoull(g.c_str(), nullptr, 16);
                if (auto gr = LoadImageGraph(guid)) graphs[guid] = std::move(gr);
            } else if (k == "instance") {
                int id, parent;
                std::string g;
                in >> id >> g >> parent;
                insts.emplace_back(int16_t(id), std::strtoull(g.c_str(), nullptr, 16), int16_t(parent));
            } else if (k == "var") {
                int inst;
                std::string id, type, payload;
                in >> inst >> id >> type >> payload;
                Primitive p;
                if (ParseValue(type, payload, p)) vars.emplace_back(int16_t(inst), varid::Make(std::strtoull(id.c_str(), nullptr, 16)), p);
                else skipped_vars++;
            } else if (k == "state") {
                St s;
                int inst;
                in >> inst >> s.state >> s.nbits >> s.hex;
                s.inst = int16_t(inst);
                states.push_back(s);
            } else if (k == "dict") {  // dict <inst> <var> k:v,k:v (INT entries; the trace records only the count)
                int inst;
                std::string id, list;
                in >> inst >> id >> list;
                std::vector<std::pair<Primitive, Primitive>> entries;
                std::stringstream ls(list);
                for (std::string t; std::getline(ls, t, ',');) {
                    auto colon = t.find(':');
                    entries.emplace_back(Primitive::Int(std::atoi(t.substr(0, colon).c_str())),
                                         Primitive::Int(std::atoi(t.substr(colon + 1).c_str())));
                }
                dicts.emplace_back(int16_t(inst), varid::Make(std::strtoull(id.c_str(), nullptr, 16)), std::move(entries));
            } else if (k == "clock") {
                in >> clock;
            } else if (k == "button") {
                Button b;
                int down;
                std::string list;
                in >> b.cf >> b.button >> down >> list;
                b.down = down != 0;
                std::stringstream ls(list);
                for (std::string t; std::getline(ls, t, ',');) b.insts.push_back(int16_t(std::atoi(t.c_str())));
                buttons.push_back(b);
            } else if (k == "extern") {
                uint32_t cf;
                int inst;
                std::string id, type, payload;
                in >> cf >> inst >> id >> type >> payload;
                Primitive p;
                if (ParseValue(type, payload, p))
                    externs.push_back({cf, int16_t(inst), varid::Make(std::strtoull(id.c_str(), nullptr, 16)), p, extern_offset});
            } else if (k == "host_raycast") {  // host_raycast <entity hex> <px py pz> <nx ny nz>: every cast hits that
                std::string e;
                in >> e;
                raycast_hit = SyncManager::RaycastHit{};
                raycast_hit->entity = uint32_t(std::strtoul(e.c_str(), nullptr, 16));
                for (float& v : raycast_hit->point) in >> v;
                for (float& v : raycast_hit->normal) in >> v;
            } else if (k == "host_end") {  // host_end <cf> <ms offset> <inst> <state> <finish 0|1>: a host ends a state
                HostEnd h;
                in >> h.cf >> h.offset >> h.inst >> h.state >> h.finish;
                host_ends.push_back(h);
            } else if (k == "extern_at") {
                in >> extern_offset;
            } else if (k == "until") {
                in >> until;
            } else if (k == "expect") {
                uint32_t cf;
                int inst;
                std::string kind, rest;
                in >> cf >> inst >> kind;
                std::getline(in, rest);
                if (!rest.empty() && rest[0] == ' ') rest.erase(0, 1);
                expect.insert(std::format("{} {} {} {}", cf, inst, kind, rest));
            }
        }
        sm = std::make_unique<SyncManager>(entity, evals, Role::Server);
        sm->import_resumes = true;  // the snapshot's states were active before it
        if (raycast_hit)
            sm->raycast = [this](int16_t, int, SyncManager::RaycastHit& h) {
                h = *raycast_hit;
                return true;
            };
        sm->graph_provider = [this](uint64_t g) -> const Graph* {
            auto it = graphs.find(g);
            return it == graphs.end() ? nullptr : it->second.get();
        };
        // StackPriority records read from content (systems/data/statescript/stack_priorities.json, §5.9).
        {
            std::ifstream pf(std::string(SS_DATA) + "/../../statescript/stack_priorities.json");
            std::string js((std::istreambuf_iterator<char>(pf)), {});
            for (size_t at = js.find("\"guid\""); at != std::string::npos; at = js.find("\"guid\"", at + 1)) {
                size_t q = js.find("0x", at);
                uint64_t guid = std::strtoull(js.c_str() + q, nullptr, 16);
                size_t p = js.find("\"priority\":", at) + 11;
                float prio = std::strtof(js.c_str() + p, nullptr);
                size_t t = js.find("\"wins_ties\":", at) + 12;
                while (js[t] == ' ') t++;
                sm->stack_priorities[guid] = {prio, js.compare(t, 4, "true") == 0};
            }
            if (sm->stack_priorities.size() < 19) std::printf("  stack priorities: only %zu read\n", sm->stack_priorities.size());
        }
        sm->SetClock(clock, int64_t(clock) * 16 + 15);
        for (auto& [id, g, parent] : insts) {
            auto it = graphs.find(g);
            if (it == graphs.end() || !sm->ImportInstance(*it->second, id, parent, 0)) {
                std::printf("  import instance %d (graph %llx) failed\n", id, (unsigned long long)g);
                return false;
            }
        }
        for (auto& [inst, id, p] : vars) {
            VarBag* bag = inst == 0 ? &sm->entity_bag() : &sm->FindInstance(inst)->bag();
            bag->Store(id, p);
        }
        for (auto& [inst, id, entries] : dicts) {
            VarBag* bag = inst == 0 ? &sm->entity_bag() : &sm->FindInstance(inst)->bag();
            bag->SetDictionary(id, entries);
            skipped_vars--;  // its `var … DICT n=` line
        }
        // Expectations on client-only states, and on instance vars only client-only nodes name, are the client's
        // business.
        std::map<int16_t, std::set<uint64_t>> co_vars;
        for (auto it = expect.begin(); it != expect.end();) {
            uint32_t cf;
            int inst, st;
            char kind[16];
            unsigned long long var;
            if (std::sscanf(it->c_str(), "%u %d var inst:%llx=", &cf, &inst, &var) == 3) {
                Instance* i = sm->FindInstance(int16_t(inst));
                if (i && !co_vars.count(i->id())) co_vars[i->id()] = ClientOnlyVars(i->graph());
                if (i && co_vars[i->id()].count(var)) {
                    it = expect.erase(it);
                    continue;
                }
            } else if (std::sscanf(it->c_str(), "%u %d %15s %d", &cf, &inst, kind, &st) == 4 && std::string(kind) != "var") {
                Instance* i = sm->FindInstance(int16_t(inst));
                if (i && st >= 0 && st < int(i->graph().states().size()) && i->graph().state_node(st).client_only) {
                    it = expect.erase(it);
                    continue;
                }
            }
            ++it;
        }
        for (auto& s : states) {
            Instance* i = sm->FindInstance(s.inst);
            auto data = HexBytes(s.hex);
            if (!i || s.state >= int(i->graph().states().size())) continue;
            i->EnsureState(s.state).Import(data.data(), s.nbits);
            imported_states++;
        }
        // Round trip: every imported state writes back the bits the client had (full update format).
        for (auto& s : states) {
            Instance* i = sm->FindInstance(s.inst);
            State* st = i ? i->state(s.state) : nullptr;
            if (!st) continue;
            BitOut b;
            st->WriteData(b, true);
            auto want = HexBytes(s.hex);
            bool same = b.nbits == s.nbits;
            for (int k = 0; same && k < s.nbits; k++) same = (b.bytes[k / 8] >> (k % 8) & 1) == (want[k / 8] >> (k % 8) & 1);
            if (!same) {
                roundtrip_failures++;
                std::printf("    state data round trip: inst %d state %d: %d bits, want %d (%s)\n", s.inst, s.state, b.nbits,
                            s.nbits, s.hex.c_str());
            }
        }
        Snapshot(false);
        // "cf", "cf:inst" or "cf-cf2[:inst]": print the runtime's events of those command frames
        static const char* dump = std::getenv("SS_HERO_TRACE");
        sm->trace = [this](const TraceEvent& e) {
            if (dump) {
                uint32_t dcf = 0, dcf2 = 0;
                int dinst = -1;
                if (std::sscanf(dump, "%u-%u:%d", &dcf, &dcf2, &dinst) < 2) {
                    dinst = -1;
                    std::sscanf(dump, "%u:%d", &dcf, &dinst);
                    dcf2 = dcf;
                }
                if (sm->cf() >= dcf && sm->cf() <= dcf2 && (dinst < 0 || dinst == e.instance))
                    std::printf("      [%u %lld] inst %d %s a=%lld b=%lld c=%lld %s\n", sm->cf(), (long long)sm->time(), e.instance,
                                e.kind.c_str(), (long long)e.a, (long long)e.b, (long long)e.c, e.text.c_str());
            }
            if (e.kind == "begin" || e.kind == "finish" || e.kind == "abort" || e.kind == "reenter")
                got.insert(std::format("{} {} {} {}", sm->cf(), e.instance, e.kind, e.a));
            // SS_HERO_TYPES=1: the node types this replay executes (link targets and state begins), on stderr
            static const bool types = std::getenv("SS_HERO_TYPES") != nullptr;
            if (types && (e.kind == "link" || e.kind == "begin"))
                if (Instance* ti = sm->FindInstance(e.instance)) {
                    int ni = e.kind == "link" ? int(e.c) : int(e.b);
                    if (ni >= 0 && ni < int(ti->graph().nodes().size()))
                        std::fprintf(stderr, "TYPE %08x\n", ti->graph().node(ni).type);
                }
            if (e.kind == "notif" && e.a == int(NotifType::LogicalButton)) {
                int b = 0, d = 0;
                std::sscanf(e.text.c_str(), "button %d down %d", &b, &d);
                routed[std::format("{} {} {}", sm->cf(), b, d)].push_back(e.instance);
            }
        };
        return true;
    }

    // Per-CF var diffs (primitives and dictionaries, both bags), recorded like the trace's `var` events (a dictionary
    // as its entry count, the trace's text: a rewrite with the same count is not seen).
    void Snapshot(bool record) {
        std::map<std::string, std::string> now;
        auto add = [&](const VarBag& bag, const std::string& role, int16_t inst) {
            for (auto& [id, v] : bag.vars()) {
                auto key = std::format("{}|{}:{:x}", inst, role, id & 0xFFFFFFFFFFFFull);
                if (v->kind() == Var::Kind::Primitive) now[key] = ToTraceText(v->stored());
                else if (v->kind() == Var::Kind::Dictionary) now[key] = std::format("DICT n={}", v->dict_size());
            }
        };
        add(sm->entity_bag(), "ent", 0);
        for (auto& i : sm->instances()) add(i->bag(), "inst", i->id());
        if (record)
            for (auto& [k, v] : now) {
                auto it = base.find(k);
                if (it != base.end() && it->second == v) continue;
                bool input = false;  // the extern inputs are not results
                for (auto& x : externs)
                    input |= k == std::format("{}|{}:{:x}", x.inst, x.inst ? "inst" : "ent", x.var & 0xFFFFFFFFFFFFull);
                if (input) continue;
                auto bar = k.find('|');
                got.insert(std::format("{} {} var {}={}", sm->cf(), k.substr(0, bar), k.substr(bar + 1), v));
            }
        base = std::move(now);
    }

    void Run() {
        // Mask bit of a logical button (the client's command, EmitButtonEdges order table).
        auto bit = [](int32_t b) -> uint16_t {
            switch (b) {
            case 0x42: return 0x100; case 0x4c: return 0x200; case 0x5d: return 0x1; case 0x65: return 0x2;
            case 0x73: return 0x4; case 0x77: return 0x10; case 0x78: return 0x20; case 0x79: return 0x40;
            case 0x66: return 0x8; case 0x7a: return 0x80; case 0x7b: return 0x400; case 0x7c: return 0x800;
            case 0x7d: return 0x1000;
            }
            return 0;
        };
        // The other buttons travel as the command's action code (a down + up pair in one CF, §5.10.2).
        auto action_code = [](int32_t b) -> uint8_t {
            for (int c = 1; c <= 44; c++)
                if (ActionButton(uint8_t(c)) == b) return uint8_t(c);
            return 0;
        };
        uint16_t mask = 0;
        for (uint32_t cf = clock + 1; cf <= until; cf++) {
            uint8_t action = 0;
            for (auto& b : buttons)
                if (b.cf == cf) {
                    if (use_input && bit(b.button)) mask = b.down ? mask | bit(b.button) : mask & ~bit(b.button);
                    else if (use_input) action = action_code(b.button);
                    else
                        for (int16_t id : b.insts)
                            if (Instance* i = sm->FindInstance(id)) i->QueueLogicalButton(b.button, b.down, int64_t(cf) * 16);
                }
            if (use_input) sm->Input(cf, mask, action);
            // Client code writes them during the CF; the graphs see them at ms cf×16 + offset (extern_at).
            for (auto& x : externs)
                if (x.cf == cf)
                    sm->PostEvent(int64_t(cf) * 16 + x.offset, [this, x] {
                        VarBag* bag = x.inst == 0 ? &sm->entity_bag() : sm->FindInstance(x.inst) ? &sm->FindInstance(x.inst)->bag() : nullptr;
                        if (bag) bag->Set(x.var, x.value);
                    });
            for (auto& h : host_ends)
                if (h.cf == cf)
                    sm->PostEvent(int64_t(cf) * 16 + h.offset, [this, h] {
                        Instance* i = sm->FindInstance(int16_t(h.inst));
                        if (State* st = i ? i->state(h.state) : nullptr; st && st->active()) st->RequestEnd(h.finish != 0);
                    });
            sm->RunCommandFrames(cf, 1);
            Snapshot(true);
        }
    }

    // Returns the number of differences; prints them.
    int Compare() {
        std::vector<std::string> missing, extra;
        for (auto& e : expect)
            if (!got.count(e)) missing.push_back(e);
        for (auto& g : got)
            if (!expect.count(g)) extra.push_back(g);
        for (auto& m : missing) std::printf("    missing: %s\n", m.c_str());
        for (auto& x : extra) std::printf("    extra:   %s\n", x.c_str());
        std::printf("  %zu expected, %zu got, %zu missing, %zu extra (%d states imported, %d vars not importable)\n",
                    expect.size(), got.size(), missing.size(), extra.size(), imported_states, skipped_vars);
        for (auto& [t, n] : sm->unmodelled_states) std::printf("    unmodelled state %08x x%d\n", t, n);
        for (auto& [t, n] : sm->unmodelled_nodes) std::printf("    unmodelled node %08x x%d\n", t, n);
        for (auto& w : sm->warnings) std::printf("    warning: %s\n", w.c_str());
        return int(missing.size() + extra.size());
    }
};

}  // namespace

// The same press driven by the client's command (mask bit 0x1 held for 6 CFs): the runtime routes the
// notifications itself (graph lists +0xE0 / +0xF0) to the recorded recipients 3 5 6 7 9 11.
TEST(hero_genji_fire_input) {
    HeroReplay r;
    r.use_input = true;
    CHECK(r.Load("hero_genji_fire.fixture"));
    r.Run();
    CHECK_EQ(r.Compare(), 0);
    for (auto& b : r.buttons) {
        auto& got = r.routed[std::format("{} {} {}", b.cf, b.button, b.down ? 1 : 0)];
        CHECK(got == b.insts);
    }
}

TEST(hero_genji_fire) {
    HeroReplay r;
    CHECK(r.Load("hero_genji_fire.fixture"));
    CHECK_EQ(r.roundtrip_failures, 0);
    r.Run();
    int diffs = r.Compare();
    CHECK_EQ(diffs, 0);
}

// Shift (dash, ability_1) and E (deflect, ability_2), driven by the client's command.
TEST(hero_genji_dash) {
    HeroReplay r;
    r.use_input = true;
    CHECK(r.Load("hero_genji_dash.fixture"));
    CHECK_EQ(r.roundtrip_failures, 0);
    r.Run();
    CHECK_EQ(r.Compare(), 0);
}

TEST(hero_genji_deflect) {
    HeroReplay r;
    r.use_input = true;
    CHECK(r.Load("hero_genji_deflect.fixture"));
    CHECK_EQ(r.roundtrip_failures, 0);
    r.Run();
    CHECK_EQ(r.Compare(), 0);
}

// T054 recordings (2026-09-27, instance --shadow, Anubis, body 0x40000019): every Genji press, driven by the
// client's command (mask bits, or the action code for reload / emote / spray / quick_weapon / next_weapon). Each
// fixture replays with no difference, given the host answers the fixtures declare (extern writes, the dash's
// raycast hit and MoveTo end; docs/StatescriptNodes.md §3.12).
TEST(hero_genji_presses_t054) {
    for (const char* b : {"fire", "secondary_fire", "reload", "ability_1", "ability_2", "jump", "interact", "spray",
                          "quick_weapon", "ping_self", "emote", "next_weapon"}) {
        HeroReplay r;
        r.use_input = true;
        std::printf("  %s:\n", b);
        CHECK(r.Load(std::format("hero_genji2_{}.fixture", b)));
        CHECK_EQ(r.roundtrip_failures, 0);
        r.Run();
        CHECK_EQ(r.Compare(), 0);
    }
}
