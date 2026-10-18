// Replays the client's Statescript traces (fixtures/, docs/StatescriptRuntime.md §4.3) through the runtime and
// compares the event streams: notifications (type, scheduled time, text), state begin/finish/abort, links
// (source, plug offset, index, target, return value), game messages and the per-CF variable diffs, each with
// the sync manager's clock. What the fixtures cannot contain (other instances of the same sync manager,
// graph overrides from map data, the music instance's history before the trace) is supplied by a small
// per-fixture script; every such input is listed in docs/StatescriptRuntime.md §5.6.
#include <cstdio>
#include <format>
#include <functional>
#include <map>
#include <set>
#include <string>
#include <vector>

#include "json.h"
#include "statescript/runtime.h"
#include "test.h"

using namespace ss;

namespace {

uint64_t Hex(const std::string& s) { return std::strtoull(s.c_str(), nullptr, 16); }

std::unique_ptr<Graph> LoadImage(uint64_t guid) {
    std::string meta;
    {
        std::ifstream f(std::string(SS_DATA) + "/meta.json");
        meta.assign(std::istreambuf_iterator<char>(f), {});
    }
    std::string key = std::format("\"0x{:016x}\": {{\"base\": ", guid);
    size_t at = meta.find(key);
    if (at == std::string::npos) return nullptr;
    uint64_t base = std::strtoull(meta.c_str() + at + key.size(), nullptr, 10);
    auto bytes = stu::ReadFile(std::format("{}/{:016x}.mem", SS_DATA, guid));
    std::string err;
    auto g = Graph::Load(std::move(bytes), guid, err, stu::Mode::Loaded, base);
    if (!g) std::printf("  graph %016x: %s\n", (unsigned long long)guid, err.c_str());
    return g;
}

// "TYPE payload" (ss_trace text) -> value.
Primitive ParsePrim(const std::string& s) {
    auto sp = s.find(' ');
    std::string t = s.substr(0, sp), rest = sp == std::string::npos ? "" : s.substr(sp + 1);
    if (t == "BYTE") return Primitive::Byte(std::stoi(rest) != 0);
    if (t == "INT") return Primitive::Int(std::stoi(rest));
    if (t == "FLT") return Primitive::Float(std::stof(rest));
    if (t == "INT64") return Primitive::Int64(Hex(rest));
    if (t == "ENTITY") return Primitive::Entity(uint32_t(Hex(rest)));
    if (t.size() > 1 && t[0] == 'T') {
        auto sp2 = rest.find(' ');
        return Primitive::Make(PrimType(std::stoi(t.substr(1))), Hex(rest.substr(0, sp2)),
                               uint32_t(Hex(rest.substr(sp2 + 1))));
    }
    return Primitive::Int(0);
}

void Seed(VarBag& bag, const std::string& id, const std::string& value) {
    uint64_t var = varid::Make(Hex(id));
    if (value.rfind("DICT n=", 0) == 0) bag.SetDictionarySize(var, std::stoul(value.substr(7)));
    else bag.Store(var, ParsePrim(value));
}

std::string Norm(const fx::Object& e) {
    std::string k = fx::S(e, "kind");
    std::string h = std::format("{} {} {} inst={}", k, k == "cf" || k == "cf_end" ? fx::I(e, "cf") : fx::I(e, "cf"),
                                fx::I(e, "ms"), fx::I(e, "inst"));
    if (k == "cf") return h + std::format(" n={}", fx::I(e, "n"));
    if (k == "cf_end") return h;
    if (k == "instantiate") return h + " guid=" + fx::S(e, "guid");
    if (k == "instantiate_done") return h + std::format(" queued={}", fx::I(e, "queued"));
    if (k == "notif") return h + std::format(" type={} time={} {}", fx::I(e, "type"), fx::I(e, "time"), fx::S(e, "text"));
    if (k == "begin" || k == "reenter")
        return h + std::format(" state={} node={} offset={}", fx::I(e, "state"), fx::I(e, "node"), fx::I(e, "offset"));
    if (k == "finish" || k == "abort") return h + std::format(" state={} node={}", fx::I(e, "state"), fx::I(e, "node"));
    if (k == "finish_done" || k == "abort_done") return h + std::format(" state={}", fx::I(e, "state"));
    if (k == "link")
        return h + std::format(" src={} plug={} idx={} dst={}", fx::I(e, "src"), fx::I(e, "plug"), fx::I(e, "idx"),
                               fx::I(e, "dst"));
    if (k == "link_done") return h + std::format(" dst={} ret={}", fx::I(e, "dst"), fx::I(e, "ret"));
    if (k == "message")
        return h + std::format(" id={} result={} sender={} instigator={}", fx::S(e, "id"), fx::I(e, "result"),
                               fx::S(e, "sender"), fx::S(e, "instigator"));
    if (k == "var") return h + " " + fx::S(e, "bag") + ":" + fx::S(e, "id") + "=" + fx::S(e, "value");
    return h + " ?";
}

std::string Norm(const TraceEvent& e) {
    std::string h = std::format("{} {} {} inst={}", e.kind, e.kind == "cf" || e.kind == "cf_end" ? uint32_t(e.a) : e.cf,
                                e.ms, e.instance);
    const std::string& k = e.kind;
    if (k == "cf") return h + std::format(" n={}", e.b);
    if (k == "cf_end") return h;
    if (k == "instantiate") return h + std::format(" guid=0x{:016x}", uint64_t(e.a));
    if (k == "instantiate_done") return h + std::format(" queued={}", e.b);
    if (k == "notif") return h + std::format(" type={} time={} {}", e.a, e.b, e.text);
    if (k == "begin" || k == "reenter") return h + std::format(" state={} node={} offset={}", e.a, e.b, e.c);
    if (k == "finish" || k == "abort") return h + std::format(" state={} node={}", e.a, e.b);
    if (k == "finish_done" || k == "abort_done") return h + std::format(" state={}", e.a);
    if (k == "link") return h + std::format(" src={} plug={} idx={} dst={}", e.a, e.b, e.idx, e.c);
    if (k == "link_done") return h + std::format(" dst={} ret={}", e.a, e.b);
    if (k == "message")
        return h + std::format(" id=0x{:016x} result={} sender=0x{:x} instigator=0x{:x}", uint64_t(e.a), e.b,
                               uint32_t(e.c), uint32_t(e.idx));
    if (k == "var") return h + " " + e.text;
    return h + " ?";
}

// The replay: runs a sync manager, records its trace in the fixture's vocabulary (var diffs as ss_trace
// makes them) and walks the fixture.
struct Replay {
    std::vector<fx::Object> fixture;  // events (header dropped)
    EvaluatorRegistry evals = EvaluatorRegistry::Builtin();
    std::unique_ptr<SyncManager> sm;
    std::vector<std::string> got;
    bool recording = true;
    std::map<const void*, std::map<std::string, std::string>> base;
    std::vector<std::pair<std::string, std::string>> external;  // ent writes applied at the next outer cf_end
    int depth = 0;
    // Script.
    std::map<uint64_t, std::vector<std::pair<uint64_t, Primitive>>> message_params;
    std::map<int64_t, std::function<void()>> before_seq;      // run before the fixture event with this seq
    std::map<uint32_t, std::function<void()>> before_cf;      // run before RunCommandFrames(cf, 1)
    bool external_ent_writes = false;  // every ent-bag var event is a write by another instance

    std::string name;
    explicit Replay(const std::string& file, uint32_t owner) : name(file) {
        fixture = fx::ReadLines(std::string(SS_FIXTURES) + "/" + file);
        if (!fixture.empty()) fixture.erase(fixture.begin());
        sm = std::make_unique<SyncManager>(owner, evals, Role::Client);
        sm->trace = [this](const TraceEvent& e) { OnEvent(e); };
    }

    void Diff(const VarBag& bag, const std::string& role, int16_t inst) {
        std::map<std::string, std::string> now;
        bag.ForEach([&](std::span<const uint64_t> path, const Var& v) {
            std::string key = role;
            for (uint64_t id : path) key += std::format(":{:x}", id & 0xFFFFFFFFFFFFull);
            now[key] = v.kind() == Var::Kind::Dictionary ? std::format("DICT n={}", v.dict_size())
                                                         : ToTraceText(v.value());
        });
        auto& before = base[&bag];
        for (auto& [k, v] : now) {
            auto it = before.find(k);
            if (it == before.end() || it->second != v) Record({.kind = "var", .instance = inst, .text = k + "=" + v});
        }
        for (auto& [k, v] : before)
            if (!now.count(k)) Record({.kind = "var", .instance = inst, .text = k + " removed"});
        before = std::move(now);
    }
    void Record(TraceEvent e) {
        e.cf = sm->cf();
        e.ms = sm->time();
        if (recording) got.push_back(Norm(e));
    }
    void OnEvent(const TraceEvent& e) {
        if (recording) got.push_back(Norm(e));
        if (e.kind == "instantiate_done") {
            Instance* i = sm->FindInstance(e.instance);
            base.erase(&i->bag());
            Diff(i->bag(), "inst", i->id());
        } else if (e.kind == "cf") {
            depth++;
        } else if (e.kind == "cf_end") {
            if (--depth == 0) {
                for (auto& [id, value] : external) Seed(sm->entity_bag(), id, value);
                external.clear();
            }
            for (auto& i : sm->instances()) Diff(i->bag(), "inst", i->id());
            Diff(sm->entity_bag(), "ent", 0);
        }
    }
    void ResetBaselines() {
        base.clear();
        for (auto& i : sm->instances()) base[&i->bag()];
    }

    GameMessage Message(const fx::Object& e) {
        GameMessage m;
        m.id = Hex(fx::S(e, "id"));
        m.sender = uint32_t(Hex(fx::S(e, "sender")));
        m.instigator = uint32_t(Hex(fx::S(e, "instigator")));
        if (auto it = message_params.find(m.id); it != message_params.end()) m.params = it->second;
        return m;
    }

    // Walks the fixture: drives RunCommandFrames / message deliveries / scripted inputs at their positions.
    void Run() {
        for (size_t i = 0; i < fixture.size(); i++) {
            const fx::Object& e = fixture[i];
            if (auto it = before_seq.find(fx::I(e, "seq")); it != before_seq.end()) it->second();
            std::string k = fx::S(e, "kind");
            if (k == "message") {
                sm->DeliverGameMessage(Message(e));
            } else if (k == "cf") {
                uint32_t cf = uint32_t(fx::I(e, "cf"));
                uint32_t n = uint32_t(fx::I(e, "n"));
                // Messages inside the block: delivered by the sync-manager queue at the start of the first
                // pass when nothing of this instance happened before them, else after the instances of the
                // pass (sent by the lobby's other instances).
                std::vector<GameMessage> pre, post;
                bool seen = false;
                size_t j = i + 1;
                for (int depth = 0; j < fixture.size(); j++) {  // the matching cf_end (flushes nest, §5.3)
                    std::string kj = fx::S(fixture[j], "kind");
                    if (kj == "cf") depth++;
                    else if (kj == "cf_end" && depth-- == 0) break;
                    else if (kj == "message") (seen ? post : pre).push_back(Message(fixture[j]));
                    else seen = true;
                }
                if (external_ent_writes)
                    for (size_t v = j + 1; v < fixture.size() && fx::S(fixture[v], "kind") == "var"; v++)
                        if (fx::S(fixture[v], "bag") == "ent")
                            external.emplace_back(fx::S(fixture[v], "id"), fx::S(fixture[v], "value"));
                for (auto& m : pre) sm->PostEvent(sm->time(), [this, m] { sm->DeliverGameMessage(m); });
                sm->after_instances = [this, post](int pass) {
                    if (pass == 0)
                        for (auto& m : post) sm->DeliverGameMessage(m);
                };
                if (n == 1)
                    if (auto it = before_cf.find(cf); it != before_cf.end()) it->second();
                sm->RunCommandFrames(cf, n);
                sm->after_instances = nullptr;
                // Skip the fixture's in-block messages: already delivered; continue after the block.
                for (size_t q = i + 1; q < j; q++)
                    if (fx::S(fixture[q], "kind") == "message") fixture[q]["kind"].s = "message_done";
                i = j;
            }
        }
    }

    // Compares the recorded stream with the fixture (message_done = delivered inside a block).
    bool Compare() {
        if (const char* dir = std::getenv("SS_REPLAY_DUMP")) {  // debugging: write both streams
            std::ofstream g(std::format("{}/{}.got", dir, name)), w(std::format("{}/{}.want", dir, name));
            for (auto& s : got) g << s << "\n";
            for (auto& e : fixture) {
                fx::Object c = e;
                if (fx::S(c, "kind") == "message_done") c["kind"].s = "message";
                w << Norm(c) << "\n";
            }
        }
        std::vector<std::string> want;
        for (auto& e : fixture) {
            fx::Object c = e;
            if (fx::S(c, "kind") == "message_done") c["kind"].s = "message";
            want.push_back(Norm(c));
        }
        size_t n = std::min(want.size(), got.size());
        for (size_t i = 0; i < n; i++)
            if (want[i] != got[i]) {
                std::printf("  first difference at event %zu (fixture seq %lld):\n", i, (long long)fx::I(fixture[i], "seq"));
                for (size_t k = i >= 3 ? i - 3 : 0; k < std::min(n, i + 4); k++)
                    std::printf("   %s want %s\n   %s got  %s\n", k == i ? ">" : " ", want[k].c_str(), k == i ? ">" : " ",
                                got[k].c_str());
                return false;
            }
        if (want.size() != got.size()) {
            std::printf("  length differs: want %zu got %zu\n", want.size(), got.size());
            for (size_t k = n; k < std::min(n + 5, std::max(want.size(), got.size())); k++)
                std::printf("   extra %s\n", k < want.size() ? want[k].c_str() : got[k].c_str());
            return false;
        }
        return true;
    }

    void Report(const char* name) {
        std::printf("  %s: %zu events compared; unmodelled nodes %zu, states %zu, config vars %zu, warnings %zu\n",
                    name, got.size(), sm->unmodelled_nodes.size(), sm->unmodelled_states.size(),
                    sm->unmodelled_config_vars.size(), sm->warnings.size());
        const stu::Registry& reg = stu::Registry::Client();
        for (auto* m : {&sm->unmodelled_nodes, &sm->unmodelled_states, &sm->unmodelled_config_vars})
            for (auto& [t, n] : *m)
                std::printf("    unmodelled %s %s x%d%s\n", m == &sm->unmodelled_nodes ? "node" : m == &sm->unmodelled_states ? "state" : "config var",
                            reg.NameOr(t).c_str(), n, IsPresentationAction(t) ? " (presentation)" : "");
        for (auto& w : std::set<std::string>(sm->warnings.begin(), sm->warnings.end()))
            std::printf("    warning: %s\n", w.c_str());
    }
};

}  // namespace

// Door 0x800001cd, graph 0x042 (Temple of Anubis): map-load start, then the GameMessage entry run by
// Prometheus execute_node and the BooleanSwitch flipping at the next step. The second fixture continues the
// same sync manager.
TEST(replay_door_042) {
    auto g = LoadImage(0x0580000000000042);
    CHECK(g);
    if (!g) return;
    Replay start("trace_door_042_start.jsonl", 0x800001cd);
    SyncManager& sm = *start.sm;
    sm.SetClock(uint32_t(3807935 / 16), 3807935);  // the door's clock before CF 238013 (stale, §3.2)
    // StartDefaultScripts sends events 0x317..0x31a = FLT 1 to the entity bag (StatescriptLoading.md §4).
    for (uint64_t v : {0x317, 0x318, 0x319, 0x31a}) sm.entity_bag().Store(varid::Make(v), Primitive::Float(1));
    // Overrides: 0x261 from the entity definition 0x040000000000005b (STUStatescriptGraphWithOverrides);
    // 0x186 = BYTE 1 from the map placement (not modelled yet, docs §5.6).
    Override ov[] = {{varid::Make(0x261), Primitive::Int64(0x030000000000014f)}, {varid::Make(0x186), Primitive::Byte(true)}};
    Instance* inst = sm.CreateInstance(*g, ov, -1);
    CHECK(inst);
    start.Run();
    bool ok = start.Compare();
    CHECK(ok);
    start.Report("door start");

    // Continue: execute_node(node 0) at the stale clock (CF 238018), then the next step at CF 241910.
    Replay open("trace_door_042_entry0.jsonl", 0x800001cd);
    open.sm.swap(start.sm);
    open.sm->trace = [&open](const TraceEvent& e) { open.OnEvent(e); };
    open.base = start.base;
    open.before_seq[8976] = [&open] { open.sm->FindInstance(-1)->ExecuteEntry(0); };
    open.Run();
    ok = open.Compare();
    CHECK(ok);
    open.Report("door open");
}

// Lobby-EA music controller 0x80000002, graph 0x1e5: map load + ~70 s, every CF.
TEST(replay_music_1e5) {
    auto g = LoadImage(0x05800000000001e5);
    CHECK(g);
    if (!g) return;
    Replay r("trace_music_1e5.jsonl", 0x80000002);
    SyncManager& sm = *r.sm;
    // --- Prologue (not compared): the instance was running long before the trace started. Seed the entity
    // bag with the values of the trace's first diff, start the graph at CF 1, and send the message that set
    // 0x8ae = 1 (entries 15/19 -> SetVar 58), so the states match the trace's start.
    for (auto& e : r.fixture) {
        if (fx::S(e, "kind") == "var" && fx::S(e, "bag") == "ent") Seed(sm.entity_bag(), fx::S(e, "id"), fx::S(e, "value"));
        if (fx::S(e, "kind") == "cf" && fx::I(e, "cf") > 1121) break;
    }
    r.recording = false;
    sm.SetClock(0, 15);
    Instance* inst = sm.CreateInstance(*g, {}, -6);
    CHECK(inst);
    inst->bag().Store(varid::Make(0x7), Primitive::Make(PrimType::None, 0x3ffff));  // as seen live (var 0x007)
    for (uint32_t cf = 1; cf <= 1120; cf++) {
        if (cf == 100) sm.DeliverGameMessage({.id = 0x0240000000000116});
        sm.RunCommandFrames(cf, 1);
    }
    std::printf("  prologue: active states");
    for (int s : inst->ActiveStates()) std::printf(" %d", s);
    std::printf("\n");
    r.ResetBaselines();
    r.recording = true;
    // --- Inputs the trace implies but does not contain.
    r.external_ent_writes = true;  // graph 0x1e5 writes only instance vars in this window
    // 0x117 and 0xe2 carry parameter 0x0a40000000000097 (their SetVar notifications show the value).
    r.message_params[0x0240000000000117] = {{0x0a40000000000097, Primitive::Int64(0x080000000000005b)}};
    r.message_params[0x02400000000000e2] = {{0x0a40000000000097, Primitive::Int64(0x080000000000005b)}};
    // GetMapTease (5852c346) reads the map: the value the trace wrote to 0xe08.
    r.evals.RegisterConstant(0x5852c346, Primitive::Int64(0x030000000000032f));
    // CF 1369: a world change (bit 0x2000, the graph's world flags) re-evaluates BooleanSwitch states 7 and 16;
    // another instance of the lobby sync manager had work at the window start, so the first pass ran there.
    r.before_cf[1369] = [&sm] {
        sm.AddWorldChange(0x2000);
        sm.PostEvent(1369 * 16, [] {});
    };
    r.Run();
    bool ok = r.Compare();
    CHECK(ok);
    r.Report("music");
}

// Entity 0x80000366 (def 0x0fc2, Anubis map load): graph 0x216 (instance -1) runs its entry -> PlayScript action
// (plays 0x046 on its own entity: instance -2, auto-stop flag, nested flush, parameters -> 0x046's PlayScript
// entry) -> StopScript (returns 0; StopInstance for -1). 0x046's node 0 is server-only (link returns 0).
TEST(replay_playscript_216_046) {
    auto parent = LoadImage(0x0580000000000216), child = LoadImage(0x0580000000000046);
    CHECK(parent && child);
    if (!parent || !child) return;
    Replay r("trace_playscript_216_046.jsonl", 0x80000366);
    SyncManager& sm = *r.sm;
    sm.graph_provider = [&](uint64_t guid) -> const Graph* { return guid == child->guid() ? child.get() : nullptr; };
    sm.SetClock(uint32_t(316287 / 16), 316287);
    // StartDefaultScripts: 0x317 is in the entity bag at the flush inside the CF, 0x318..0x31a at its end [L].
    sm.entity_bag().Store(varid::Make(0x317), Primitive::Float(1));
    r.external_ent_writes = true;
    CHECK(sm.CreateInstance(*parent, {}, -1));
    r.Run();
    bool ok = r.Compare();
    CHECK(ok);
    r.Report("playscript");
}

// Mover 0x80000200 (def 0x2ed, Anubis map load): graph 0x208. Stack state 1 begins and starts states 0 and 5
// through its subgraph plug; BooleanSwitch 5 depends on [$702, $7e1] / [$702, $64] where $702 = the capture
// point 0x800001cb, whose sync manager sends three change notifications at the start of the next CF.
TEST(replay_mover_208) {
    auto g = LoadImage(0x0580000000000208);
    CHECK(g);
    if (!g) return;
    // The capture point (owner of $702) must outlive the mover: the mover unsubscribes through it.
    EvaluatorRegistry capture_evals = EvaluatorRegistry::Builtin();
    SyncManager capture(0x800001cb, capture_evals);
    Replay r("trace_mover_208_start.jsonl", 0x80000200);
    SyncManager& sm = *r.sm;
    sm.entity_resolver = [&](uint32_t e) { return e == capture.owner() ? &capture : nullptr; };
    sm.SetClock(uint32_t(316207 / 16), 316207);
    for (uint64_t v : {0x317, 0x318, 0x319, 0x31a}) sm.entity_bag().Store(varid::Make(v), Primitive::Float(1));
    // 0x702: schema default INT 0, then the placement override = the capture point entity (map data) [L].
    Override ov[] = {{varid::Make(0x702), Primitive::Entity(0x800001cb)}};
    Instance* inst = sm.CreateInstance(*g, ov, -1);
    CHECK(inst);
    // [?] The capture point's three change notifications (which of $7e1 / $64 changed is not in the trace):
    // delivered at the first pass of CF 19780.
    r.before_cf[19780] = [&sm, inst] {
        sm.PostEvent(19780 * 16, [inst] {
            for (int k = 0; k < 3; k++)
                if (State* s = inst->state(5)) s->OnWorldChanged();
        });
    };
    r.Run();
    bool ok = r.Compare();
    CHECK(ok);
    r.Report("mover");
}
