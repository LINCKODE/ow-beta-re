// Node-type behaviour tests (T054, docs/StatescriptNodes.md §3): each type's spec entry, checked on real graphs
// (CASC bytes in fixtures/).
#include <format>
#include <memory>

#include "statescript/capabilities.h"
#include "statescript/match_host.h"
#include "statescript/runtime.h"
#include "json.h"
#include "test.h"

using namespace ss;

namespace {

std::unique_ptr<Graph> LoadFixture(const char* name, uint64_t guid) {
    std::string err;
    auto g = Graph::Load(stu::ReadFile(std::string(SS_FIXTURES) + "/" + name), guid, err);
    if (!g) std::printf("  load %s: %s\n", name, err.c_str());
    return g;
}

struct Rig {
    std::vector<std::unique_ptr<Graph>> graphs;
    EvaluatorRegistry evals = EvaluatorRegistry::Builtin();
    std::unique_ptr<SyncManager> sm;
    std::vector<TraceEvent> events;
    explicit Rig(Role role = Role::Server) {
        sm = std::make_unique<SyncManager>(0x40000030, evals, role);
        sm->graph_provider = [this](uint64_t guid) -> const Graph* {
            for (auto& g : graphs)
                if (g->guid() == guid) return g.get();
            return nullptr;
        };
        sm->trace = [this](const TraceEvent& e) { events.push_back(e); };
        sm->SetClock(100, 100 * 16 + 15);
    }
    const Graph* Add(const char* file, uint64_t guid) {
        graphs.push_back(LoadFixture(file, guid));
        return graphs.back().get();
    }
    void Run(uint32_t from, uint32_t to) {
        for (uint32_t cf = from; cf <= to; cf++) sm->RunCommandFrames(cf, 1);
    }
    int Index(const char* kind, int16_t inst, int64_t a = -1, const char* text = nullptr) const {
        for (size_t k = 0; k < events.size(); k++) {
            auto& e = events[k];
            if (e.kind == kind && e.instance == inst && (a < 0 || e.a == a) &&
                (!text || e.text.find(text) != std::string::npos))
                return int(k);
        }
        return -1;
    }
};

bool Bits(const State& s, std::vector<int> want) {
    BitOut b;
    s.WriteData(b, false);
    if (b.nbits != int(want.size())) return false;
    for (int i = 0; i < b.nbits; i++)
        if ((b.bytes[i / 8] >> (i % 8) & 1) != want[i]) return false;
    return true;
}

}  // namespace

// Graph 0x3ca: entry → SubScript (state 1, node 6) running 0x3b6 with three init values
// ($4d0 = FLT −0.3, $aed = target tag 0x0d6…7d, $1a59 = the parent's $1a59).
TEST(subscript_child_ids_inputs_and_data) {
    Rig r;
    const Graph* parent = r.Add("graph_0x3ca.stud", 0x05800000000003ca);
    const Graph* child = r.Add("graph_0x3b6.stud", 0x05800000000003b6);
    CHECK(parent && child);
    if (!parent || !child) return;
    Instance* p = r.sm->CreateInstance(*parent, {}, 5);  // a mirrored (positive) parent
    r.Run(101, 101);
    Instance* c = r.sm->FindInstance(6);  // max(ids) + 1
    CHECK(c);
    if (!c) return;
    CHECK_EQ(c->parent(), 5);
    CHECK_EQ(c->slot(), 1u);
    CHECK(c->graph().guid() == 0x05800000000003b6);
    Primitive v;
    CHECK(c->bag().Find(0x0D800000000004d0) && c->bag().Find(0x0D800000000004d0)->value().type == PrimType::Float);
    CHECK(c->bag().Find(0x0D80000000000aed) && c->bag().Find(0x0D80000000000aed)->value().type == PrimType::Int64);
    // Inputs are queued before the child's instantiation: its SetVars run before its entry.
    int set = r.Index("notif", 6, -1, "setvar inst 6 var 4d0"), entry = r.Index("notif", 6, -1, "traversefrom");
    CHECK(set >= 0 && entry > set);
    // The child is processed inside the parent's begin, before the parent's OnBegin / Subgraph plugs.
    int child_begin = r.Index("begin", 6), parent_link = r.Index("link", 5, 6);
    CHECK(child_begin >= 0 && parent_link > child_begin);
    State* s = p->state(1);
    CHECK(s && s->active());
    if (!s) return;
    CHECK(Bits(*s, {0, 0, 1, 1, 0, 0}));  // small(6) = 0 + 0110, keep-alive 0
    std::vector<int16_t> refs;
    s->DataRefs(true, refs);
    CHECK(refs.size() == 1 && refs[0] == 6);
    CHECK(Capability(0x3db862c4).local == DataCap::Writer);
    CHECK(Capability(0x3db862c4).remote == DataCap::None);

    // The child stopping aborts the SubScript state (916d18ce = 0).
    c->RequestStop();
    r.Run(102, 102);
    CHECK(c->stopped());
    CHECK(!s->active());
    CHECK(Bits(*s, {0, 0, 0, 0, 0, 0}));
}

TEST(subscript_end_stops_the_child_and_local_ids) {
    Rig r(Role::Client);
    const Graph* parent = r.Add("graph_0x3ca.stud", 0x05800000000003ca);
    r.Add("graph_0x3b6.stud", 0x05800000000003b6);
    if (!parent) return;
    Instance* p = r.sm->CreateInstance(*parent);  // local: id −1
    r.Run(101, 101);
    Instance* c = r.sm->FindInstance(-2);  // min(ids) − 1
    CHECK(c && c->parent() == p->id());
    if (!c) return;
    // Aborting the state (not keep-alive) unlinks the child and stops it at now.
    p->state(1)->DoAbort();
    CHECK(c->parent() == 0);
    r.Run(102, 102);
    CHECK(c->stopped());
    CHECK(!p->state(1)->active());
}

// Graph 0x037: GameMessage entry (0x…0f, m_out_Sender → $28) → SendGameMessage 0x…12f to $28: a reply.
TEST(send_game_message_to_self_and_other_entities) {
    Rig r;
    const Graph* g = r.Add("graph_0x037.stud", 0x0580000000000037);
    if (!g) return;
    SyncManager other(0x40000031, r.evals, Role::Server);
    std::vector<TraceEvent> other_events;
    other.trace = [&](const TraceEvent& e) { other_events.push_back(e); };
    r.sm->entity_resolver = [&](uint32_t e) { return e == 0x40000031 ? &other : nullptr; };
    other.CreateInstance(*g);  // a receiver (the "message" event is per instance)
    other.RunCommandFrames(101, 1);
    r.sm->CreateInstance(*g);
    r.Run(101, 101);
    auto sent = [&](const std::vector<TraceEvent>& ev) {
        int n = 0;
        for (auto& e : ev)
            if (e.kind == "message" && uint64_t(e.a) == 0x024000000000012f) {
                CHECK_EQ(e.c, 0x40000030);  // sender = the sending entity
                n++;
            }
        return n;
    };
    // To self: every instance of the own sync manager gets it, synchronously inside the action.
    r.sm->DeliverGameMessage({.id = 0x024000000000000f, .sender = 0x40000030});
    r.Run(102, 102);
    CHECK_EQ(sent(r.events), 1);
    // To another entity: through the entity resolver (a server's job; the client delivers to itself only).
    r.sm->DeliverGameMessage({.id = 0x024000000000000f, .sender = 0x40000031});
    r.Run(103, 103);
    CHECK_EQ(sent(other_events), 1);
    CHECK_EQ(sent(r.events), 1);
    CHECK(Capability(0x8b6a3312).level == CapLevel::Exact);
}

// Graph 0x158 (a value-history recorder): entry → ArrayClear ×3 → BooleanSwitch; on every change of the
// entity vars $4f1 / $540 / $67e, ArrayAdd appends the previous values ($515 / $542 / $67f) to $514 / $541 /
// $680, then SetVars copy the new values. Scalars are added as {BYTE 0, value}.
TEST(array_add_appends_scalars) {
    Rig r;
    const Graph* g = r.Add("graph_0x158.stud", 0x0580000000000158);
    if (!g) return;
    VarBag& eb = r.sm->entity_bag();
    eb.Store(varid::Make(0x4f1), Primitive::Float(1));
    eb.Store(varid::Make(0x540), Primitive::Int(2));
    eb.Store(varid::Make(0x67e), Primitive::Int(3));
    r.sm->CreateInstance(*g, {}, 5);
    r.Run(101, 101);
    eb.Set(varid::Make(0x4f1), Primitive::Float(7));
    r.Run(102, 102);
    auto dict = [&](uint64_t low) { return eb.Find(varid::Make(low)) ? eb.Find(varid::Make(low))->dict() : std::vector<DictEntry>{}; };
    auto d514 = dict(0x514), d541 = dict(0x541);
    CHECK(eb.Find(varid::Make(0x514)) && eb.Find(varid::Make(0x514))->kind() == Var::Kind::Dictionary);
    CHECK_EQ(d514.size(), size_t(2));
    CHECK(d514.size() == 2 && d514[0].first.type == PrimType::Byte && d514[0].second.type == PrimType::Int &&
          d514[1].second.type == PrimType::Float && d514[1].second.AsFloat() == 1.0f);
    CHECK(d541.size() == 2 && d541[1].second.type == PrimType::Int && d541[1].second.AsInt() == 2);
    CHECK(eb.Find(varid::Make(0x515))->value().AsFloat() == 7.0f);
}

// Expression op 32 on a Dictionary (graph 0x015, the hero weapon-slot BooleanSwitches: float expressions `$1d[k]`
// on the owner's weapon list, k = 1 / 2 / 3): numeric key = position, the entry's value as a float; missing → 0.
TEST(expression_dictionary_read) {
    Rig r;
    const Graph* g = r.Add("graph_0x015.stud", 0x0580000000000015);
    if (!g) return;
    Instance* i = r.sm->CreateInstance(*g, {}, 5);
    std::vector<int> exprs;  // typed expressions whose bytecode is `const a; var[] 0` ([70, a, 32, 0, 0])
    for (int k = 0; k < g->instance_count(); k++)
        if (g->type_of(k) == 0xcc3a08f4) {  // STUConfigVarExpression (the float interpreter)
            auto* e = g->value(k).Find(Hash("m_expression"));
            auto* o = e ? e->Find(Hash("m_opcodes")) : nullptr;
            if (o && o->items.size() >= 3 && o->items[0].AsU8() == 70 && o->items[2].AsU8() == 32) exprs.push_back(k);
        }
    CHECK_EQ(exprs.size(), size_t(3));
    EvalContext ctx{i};
    Primitive v;
    for (int cv : exprs) CHECK(Evaluate(ctx, cv, v) && v.type == PrimType::Float && v.AsFloat() == 0.0f);  // no list yet
    // Values are read as floats: INT / FLT / BYTE count, an INT64 reads 0.
    r.sm->entity_bag().SetDictionary(varid::Make(0x1d), {{Primitive::Byte(false), Primitive::Int(5)},
                                                          {Primitive::Byte(false), Primitive::Int(6)},
                                                          {Primitive::Byte(false), Primitive::Int64(7)}});
    int truthy = 0;
    for (int cv : exprs) {
        CHECK(Evaluate(ctx, cv, v));
        if (v.Truthy()) truthy++;
    }
    CHECK_EQ(truthy, 1);  // positions 1, 2, 3 of a 3-entry list: 1 = INT 6, 2 = INT64 (reads 0), 3 missing
}

// Graph 0x2cf (spray): BooleanSwitch on $3d; false branch = LogicalButton 0x86 whose going-down plug checks
// may-take-slot (StackPriority 0x0c4…03) and sets $3d = 1; true branch = Wait 1 s → $3d = 0.
TEST(logical_button_state_press_and_rearm) {
    Rig r;
    const Graph* g = r.Add("graph_0x2cf.stud", 0x05800000000002cf);
    if (!g) return;
    r.sm->stack_priorities[0x0c40000000000003] = {1.0f, false};
    Instance* i = r.sm->CreateInstance(*g, {}, 5);
    r.Run(101, 102);
    State* button = i->state(2);  // node 5
    auto var3d = [&] {
        Primitive v;
        return i->GetVar(std::vector<uint64_t>{0x0D8000000000003d}, v) && v.Truthy();
    };
    CHECK(button && button->active());
    CHECK(!var3d());
    r.sm->DispatchButton(0x86, true, 103 * 16);
    r.Run(103, 103);
    CHECK(var3d());                       // going-down → may-take-slot → $3d = 1
    CHECK(!button->active());             // the switch flipped: the false branch (the button state) ended
    r.sm->DispatchButton(0x86, false, 104 * 16);
    r.Run(104, 104 + 70);                 // Wait 1 s
    CHECK(!var3d());
    CHECK(button->active());              // re-armed
}

// Graph 0x015, node 14: Watch on the owner's weapon list $1d (a Dictionary); a415be52 → condition node 15.
TEST(watch_fires_on_a_changed_dictionary) {
    Rig r;
    const Graph* g = r.Add("graph_0x015.stud", 0x0580000000000015);
    if (!g) return;
    Instance* i = r.sm->ImportInstance(*g, 5, 0, 0);
    int st = g->node(14).state_index;
    CHECK(st >= 0);
    VarBag& eb = r.sm->entity_bag();
    eb.SetDictionary(varid::Make(0x1d), {{Primitive::Byte(false), Primitive::Int(1)}});
    r.sm->RunCommandFrames(101, 1);
    i->EnsureState(st).Import(nullptr, 0);
    auto write = [&](std::vector<DictEntry> entries) {
        Var* v;
        auto* e = eb.BeginDictionaryWrite(varid::Make(0x1d), VarUser{}, &v);
        *e = std::move(entries);
        eb.CommitDictionary(*v);
    };
    auto fired = [&] {
        int n = 0;
        for (auto& e : r.events)
            if (e.kind == "link" && e.instance == 5 && e.a == 14 && e.c == 15) n++;
        return n;
    };
    write({{Primitive::Byte(false), Primitive::Int(1)}});  // the same entries: nothing
    r.sm->RunCommandFrames(102, 1);
    CHECK_EQ(fired(), 0);
    write({{Primitive::Byte(false), Primitive::Int(1)}, {Primitive::Byte(false), Primitive::Int(2)}});
    r.sm->RunCommandFrames(103, 1);
    CHECK_EQ(fired(), 1);
}

// Graph 0xb4c: entry → ModifyHealth (server-only: no client class). The library runs its lifecycle and logs begin /
// end for the host; the host ends it.
TEST(server_only_state_host_events) {
    Rig r;
    const Graph* g = r.Add("graph_0xb4c.stud", 0x0580000000000b4c);
    if (!g) return;
    std::vector<SyncManager::HostEvent> seen;
    r.sm->host_event_hook = [&](const SyncManager::HostEvent& e) { seen.push_back(e); };
    Instance* i = r.sm->CreateInstance(*g, {}, 5);
    r.Run(101, 101);
    CHECK(seen.size() == 1 && seen[0].kind == 1 && seen[0].type == 0x619d9e95 && seen[0].instance == 5);
    CHECK(Capability(0x619d9e95).host && Capability(0x619d9e95).level == CapLevel::Designed);
    int st = seen.empty() ? 0 : seen[0].state;
    State* s = i->state(st);
    CHECK(s && s->active());
    if (!s) return;
    s->RequestEnd(true);
    r.Run(102, 102);
    CHECK(!s->active());
    CHECK(seen.size() == 2 && seen[1].kind == 2);
    CHECK_EQ(r.sm->host_events().size(), size_t(2));
}

// Client recording (2026-09-27, Anubis, Prometheus play_script 0x3ca on the game EA, raw_subscript_3ca.jsonl): the
// SubScript begin in the client's order: child −2 under local parent −1, its three inputs as SetVars (flags 9)
// queued before its entry, the child's whole start inside the parent's begin, then the parent's subgraph links.
TEST(subscript_replays_client_trace) {
    std::vector<std::string> want;
    std::ifstream f(std::string(SS_FIXTURES) + "/raw_subscript_3ca.jsonl");
    std::string line;
    auto key = [](const std::string& kind, int64_t inst, int64_t a, int64_t c, const std::string& text) {
        if (kind == "notif") return std::format("{} {} {}", kind, inst, text);
        if (kind == "link") return std::format("{} {} {}->{}", kind, inst, a, c);
        return std::format("{} {} {}", kind, inst, a);
    };
    auto keep = [](const std::string& k) { return k == "notif" || k == "begin" || k == "link" || k == "instantiate" || k == "abort" || k == "finish"; };
    while (std::getline(f, line)) {
        fx::Object o;
        if (!fx::ParseObject(line, o) || o["cf"].i != 241 || !keep(o["kind"].s)) continue;
        want.push_back(key(o["kind"].s, o["inst"].i, o["a"].i, o["c"].i, o["s"].s));
    }
    CHECK(want.size() > 30);
    Rig r(Role::Client);
    const Graph* g = r.Add("graph_0x3ca.stud", 0x05800000000003ca);
    const Graph* child = r.Add("graph_0x3b6.stud", 0x05800000000003b6);
    if (!g || !child) return;
    // Host game state the child reads, as the client's world answered: node 11's target-tag search (decb4aff)
    // found an entity; GetEntityExists is true for a valid entity ($054f) and false for $1a59 (INT 0).
    const Node& n11 = child->node(11);
    r.evals.RegisterConstant(child->type_of(child->Object(*n11.fields, Hash("m_value"))), Primitive::Entity(0x40000099));
    const Node& n12 = child->node(12);
    r.evals.Register(child->type_of(child->Object(*n12.fields, Hash("m_condition"))),
                     std::make_shared<FunctionEvaluator>([](const Graph& gg, int cv, EvalContext& ctx, Primitive& out) {
                         uint32_t e = EvaluateEntity(ctx, gg.Object(gg.value(cv), Hash("m_entity")));
                         out = Primitive::Byte(!IsInvalidEntity(e) && e != 0);
                         return true;
                     }));
    r.sm->SetClock(240, 3855);
    r.sm->CreateInstance(*g, {}, 0, /*apply_schema=*/false);
    r.sm->RunCommandFrames(241, 1);
    std::vector<std::string> got;
    for (auto& e : r.events)
        if (keep(e.kind)) got.push_back(key(e.kind, e.instance, e.a, e.c, e.text));
    size_t n = std::min(want.size(), got.size()), first = n;
    for (size_t i = 0; i < n && first == n; i++)
        if (want[i] != got[i]) first = i;
    if (first < n || want.size() != got.size())
        std::printf("    first difference at %zu: want '%s' got '%s' (%zu vs %zu)\n", first,
                    first < want.size() ? want[first].c_str() : "-", first < got.size() ? got[first].c_str() : "-",
                    want.size(), got.size());
    CHECK(first == n && want.size() == got.size());
}

// TimeInArray (27984fc9; graph 0x04d node 6, array $177): m_out_Time reads m_defaultTime until m_value shows up in the
// array, then the seconds since; an abort freezes that value into a plain FLT (docs/StatescriptNodes.md §3.13).
TEST(time_in_array_state_value) {
    Rig r;
    const Graph* g = r.Add("graph_0x04d.stud", 0x058000000000004d);
    if (!g) return;
    Instance* i = r.sm->ImportInstance(*g, 5, 0, 0);
    const Node& n = g->node(6);
    CHECK(n.type == 0x27984fc9 && n.state_index >= 0);
    auto out = g->DynamicPath(g->Object(*n.fields, Hash("m_out_Time")));
    auto arr = g->DynamicPath(g->Object(*n.fields, 0xedfb51d3));
    CHECK(!out.empty() && arr.size() == 1);
    EvalContext ctx{i};
    float def = EvaluateFloat(ctx, g->Object(*n.fields, Hash("m_defaultTime")), 0.0f);
    i->bag().SetDictionary(arr[0], {});
    r.sm->RunCommandFrames(101, 1);
    State& s = i->EnsureState(n.state_index);
    s.Import(nullptr, 0);
    Primitive v;
    CHECK(i->GetVar(out, v) && v.type == PrimType::Float && v.AsFloat() == def);
    Primitive want;
    CHECK(Evaluate(ctx, g->Object(*n.fields, Hash("m_value")), want));
    Var* dv;
    auto* e = i->bag().BeginDictionaryWrite(arr[0], VarUser{}, &dv);
    *e = {{Primitive::Int(0), want}};
    i->bag().CommitDictionary(*dv);
    r.Run(102, 112);  // found in CF 102's pass; 10 CFs later
    CHECK(i->GetVar(out, v) && std::abs(v.AsFloat() - 0.16f) < 0.02f);
    float before = v.AsFloat();
    s.RequestEnd(false);
    r.Run(113, 113);
    const Var* ov = i->bag().Find(out.back());
    CHECK(ov && !ov->user().state_defined && ov->value().type == PrimType::Float);
    CHECK(ov && ov->value().AsFloat() >= before);
    r.Run(114, 120);
    CHECK(ov && i->GetVar(out, v) && v.AsFloat() == ov->value().AsFloat());  // frozen
}

namespace {
int NodeOfType(const Graph& g, uint32_t type) {
    for (auto& n : g.nodes())
        if (n.type == type) return n.index;
    return -1;
}
}  // namespace

// The networked pulse 3f67b31b (docs §3.14), graph 0x5ad: one pulse per begin / re-entry, at most one count per CF
// once pulsed; the data = bit (pulsed in this CF), u8 fired count from -1.
TEST(pulse_counts_entries_once_per_cf) {
    Rig r;
    const Graph* g = r.Add("graph_0x5ad.stud", 0x05800000000005ad);
    if (!g) return;
    int ni = NodeOfType(*g, 0x3f67b31b);
    CHECK(ni >= 0);
    if (ni < 0) return;
    r.sm->SetClock(1000, 1000 * 16 + 15);  // the CF list keeps the last 600 CFs (before CF 600 it keeps none)
    Instance* i = r.sm->ImportInstance(*g, 5, 0, 0);
    r.sm->RunCommandFrames(1001, 1);
    State& s = i->EnsureState(g->node(ni).state_index);
    auto count = [&] {
        BitOut b;
        s.WriteData(b, false);
        return b.nbits == 9 ? int((b.bytes[0] >> 1 | b.bytes[1] << 7) & 0xff) : -2;
    };
    CHECK_EQ(count(), 0xff);  // -1
    s.DoBegin();              // begin + re-enter before the window's pulse: two pulses
    s.DoBegin();
    CHECK(s.active());
    r.Run(1002, 1002);
    CHECK_EQ(count(), 1);
    CHECK(Bits(s, {1, 1, 0, 0, 0, 0, 0, 0, 0}));  // pulsed in this CF
    s.DoBegin();  // this CF already pulsed: no count
    r.Run(1003, 1003);
    CHECK_EQ(count(), 1);
    CHECK(Bits(s, {0, 1, 0, 0, 0, 0, 0, 0, 0}));
    r.Run(1004, 1004);
    s.DoBegin();  // a later CF: counts
    r.Run(1005, 1005);
    CHECK_EQ(count(), 2);
    CHECK(Capability(0x3f67b31b).level == CapLevel::Exact && Capability(0x3f67b31b).local == DataCap::Writer);
}

// PublishGlobals a4f3be65 (docs §3.14), brain A 0x00f node 27: while active, global var 0x…195 reads the owner's
// $0195 (the match phase) through the registry; after the end it is gone.
TEST(publish_globals_registry) {
    Rig r;
    const Graph* g = r.Add("graph_0x00f.stud", 0x058000000000000f);
    if (!g) return;
    r.sm->match_host = std::make_shared<MatchHost>();
    int ni = NodeOfType(*g, 0xa4f3be65);
    CHECK(ni >= 0);
    if (ni < 0) return;
    Instance* i = r.sm->ImportInstance(*g, 5, 0, 0);
    r.sm->RunCommandFrames(101, 1);
    constexpr uint64_t kKey = 0x0d80000000000195;
    auto* entries = g->node(ni).Field(Hash("m_entries"));
    CHECK(entries && entries->items.size() == 1);
    if (!entries || entries->items.empty()) return;
    auto path = g->DynamicPath(Graph::ObjectOf(entries->items[0].Find(Hash("m_value"))));
    CHECK(path.size() == 2);  // owner bag 0x…004, $0195
    i->SetVar(path, Primitive::Make(PrimType::Int64, 0x0d80000000000197));
    Primitive v;
    CHECK(!MatchGlobalVar(*r.sm, kKey, v));
    State& s = i->EnsureState(g->node(ni).state_index);
    s.DoBegin();
    CHECK(r.sm->match_host->FindGlobal(kKey) != nullptr);
    CHECK(MatchGlobalVar(*r.sm, kKey, v) && v.type == PrimType::Int64 && v.bits == 0x0d80000000000197);
    s.RequestEnd(false);
    r.Run(102, 102);
    CHECK(!MatchGlobalVar(*r.sm, kKey, v));
    CHECK(Capability(0xa4f3be65).level == CapLevel::Exact);
}

// Anim's play hook writes its handle 5c5addbe := INTVEC4 {3, instance id, state index, 0} (graph 0x176 $793, read by
// RestartAnim f8ec9531; docs §3.14).
TEST(anim_writes_restart_handle) {
    Rig r;
    const Graph* g = r.Add("graph_0x176.stud", 0x0580000000000176);
    if (!g) return;
    int ni = -1;
    for (auto& n : g->nodes())
        if (n.type == 0x7fb4bf64 && g->Object(*n.fields, 0x5c5addbe) >= 0) ni = n.index;
    CHECK(ni >= 0);
    if (ni < 0) return;
    Instance* i = r.sm->ImportInstance(*g, 5, 0, 0);
    r.sm->RunCommandFrames(101, 1);
    State& s = i->EnsureState(g->node(ni).state_index);
    s.DoBegin();
    Primitive v;
    auto path = g->DynamicPath(g->Object(*g->node(ni).fields, 0x5c5addbe));
    CHECK(i->GetVar(path, v) && v.type == PrimType::IntVec4 &&
          v.bits == (3 | uint64_t(5) << 16 | uint64_t(uint16_t(s.index())) << 32));
}
