// Behaviour tests of the runtime's building blocks (values, var bags, graph loading, ECS component).
#include "ecs/entity_admin.h"
#include "ecs/loader.h"
#include "ecs/manifest.h"
#include "statescript/ecs_component.h"
#include "statescript/runtime.h"
#include "test.h"

#include <format>

using namespace ss;

TEST(primitive_equality_and_truthiness) {
    CHECK(Primitive::Float(1e-6f).Truthy() == false);  // |x| <= 1.19e-5
    CHECK(Primitive::Float(1e-4f).Truthy());
    CHECK(LooselyEqual(Primitive::Int(2), Primitive::Float(2.0f)));
    CHECK(LooselyEqual(Primitive::Float(1.0f), Primitive::Byte(true)));  // BYTE compares by truthiness
    CHECK(!LooselyEqual(Primitive::Int64(5), Primitive::Int(5)));
    CHECK(!Identical(Primitive::Int(0), Primitive::Float(0)));
    CHECK(ToTraceText(Primitive::Float(2.2f)) == std::string("FLT 2.2"));
    CHECK(ToTraceText(Primitive::Int64(0x30000000000014f)) == std::string("INT64 0x30000000000014f"));
    CHECK(ToTraceText(Primitive::Byte(true)) == std::string("BYTE 1"));
}

namespace {
struct Counter : VarSubscriber {
    int changed = 0, dep = 0;
    void OnVarChanged(Var&) override { changed++; }
    void OnVarDependencyChanged(Var&) override { dep++; }
};
}  // namespace

TEST(var_bag_writes_and_subscriptions) {
    VarBag bag;
    Counter a, b;
    bag.Subscribe(0x0D80000000000192, &a, 0);  // creates INT 0
    bag.Subscribe(0x0D80000000000192, &b, 1);
    CHECK(bag.Find(0x0D80000000000192));
    CHECK(bag.Find(0x0D80000000000192)->value().type == PrimType::Int);
    CHECK(bag.Set(0x0D80000000000192, Primitive::Int(0)) == SetResult::Unchanged);  // same user, identical
    CHECK(bag.Set(0x0D80000000000192, Primitive::Float(0)) == SetResult::Changed);  // type differs
    CHECK_EQ(a.changed, 1);
    CHECK_EQ(b.dep, 1);
    CHECK(bag.Set(0x0D80000000000192, Primitive::Float(1), VarUser{.priority = 5}) == SetResult::Changed);
    CHECK(bag.Set(0x0D80000000000192, Primitive::Float(2)) == SetResult::Rejected);  // lower priority
}

TEST(statescript_component_on_an_ecs_entity) {
    ecs::ComponentRegistry reg;
    ecs::manifest::RegisterComponents(reg);
    EvaluatorRegistry evals = EvaluatorRegistry::Builtin();
    RegisterStatescriptComponent(reg, evals);
    ecs::EntityAdmin ea(reg);
    ea.Initialize();
    ecs::BasicLoader l({0x23});
    ecs::Entity* e = ea.Spawn(l);
    CHECK(e);
    if (!e) return;
    SyncManager* sm = SyncManagerOf(*e);
    CHECK(sm);
    if (sm) CHECK_EQ(sm->owner(), e->id());
}

namespace {
std::string Shape(const Graph& g) {
    std::string s;
    for (auto& n : g.nodes()) {
        s += std::format("n{} t{:x} k{} s{} r{} u{}:", n.index, n.type, int(n.kind), n.state_index, n.remote_index,
                         n.unique_id);
        for (auto& p : n.plugs) {
            s += std::format(" p{:x}/{}[", p.offset, p.index);
            for (auto& l : p.links) s += std::format("{}{} ", l.target, l.abort() ? "a" : "");
            for (int32_t st : p.subgraph) s += std::format("s{} ", st);
            s += "]";
        }
        s += "\n";
    }
    for (auto& e : g.schema()) s += std::format("schema {:x} {:x}\n", e.var, g.type_of(e.value));
    for (auto& v : g.sync_vars()) s += std::format("sync {:x} {} {}\n", v.var, v.mode, v.flag);
    return s;
}
}  // namespace

// The door graph from the CASC bytes (content reader, fixtures/graph_0x042.stud) and from the image dumped
// from the client: the same graph for the runtime.
TEST(graph_from_casc_equals_client_image) {
    std::string err;
    auto file = Graph::Load(stu::ReadFile(std::string(SS_FIXTURES) + "/graph_0x042.stud"), 0x0580000000000042, err);
    CHECK(file);
    auto mem = Graph::Load(stu::ReadFile(std::string(SS_DATA) + "/0580000000000042.mem"), 0x0580000000000042, err,
                           stu::Mode::Loaded, 1463812096);
    CHECK(mem);
    if (!file || !mem) return;
    CHECK_EQ(file->nodes().size(), size_t(28));
    CHECK_EQ(file->states().size(), size_t(5));
    CHECK_EQ(file->entries().size(), size_t(5));
    CHECK(Shape(*file) == Shape(*mem));
    // Plug offsets as ss_trace records them: BooleanSwitch (node 6) false subgraph at 0x120 -> Wait node 26.
    const OutPlug* p = file->node(6).Plug(0x120);
    CHECK(p && p->links.size() == 1 && p->links[0].target == 26);
    CHECK(p && (p->subgraph == std::vector<int32_t>{4, 1}));
}

// T038's worked examples on the door graph: node 5 m_value `($813 ? 1 : 0)` -> FLT 0 (constants are FLT);
// Anim node 3 field 0x4d475080 `($838 ? 0.01 : $7f3)` -> INT 2 once $838 exists as INT 0.
TEST(door_expressions_match_t038) {
    std::string err;
    auto g = Graph::Load(stu::ReadFile(std::string(SS_FIXTURES) + "/graph_0x042.stud"), 0x0580000000000042, err);
    CHECK(g);
    if (!g) return;
    EvaluatorRegistry evals = EvaluatorRegistry::Builtin();
    SyncManager sm(0x800001cd, evals);
    Instance* inst = sm.CreateInstance(*g, {}, -1);
    inst->bag().Store(varid::Make(0x813), Primitive::Byte(false));
    inst->bag().Store(varid::Make(0x838), Primitive::Int(0));
    inst->bag().Store(varid::Make(0x7f3), Primitive::Int(2));
    EvalContext ctx{inst};
    Primitive v;
    CHECK(Evaluate(ctx, g->Object(*g->node(5).fields, Hash("m_value")), v));
    CHECK(v.type == PrimType::Float && v.AsFloat() == 0.0f);
    CHECK(Evaluate(ctx, g->Object(*g->node(3).fields, 0x4d475080), v));
    CHECK(v.type == PrimType::Int && v.AsInt() == 2);
}

// T038 worked example: 0x028 node 181, `GetPvPEntity().$195 == Identifier(0x0D8…1A27)` (typed, op 35).
TEST(op35_reads_in_the_gamemode_entity) {
    std::string meta;
    auto g = Graph::Load(stu::ReadFile(std::string(SS_DATA) + "/0580000000000028.mem"), 0x0580000000000028, meta,
                         stu::Mode::Loaded, 1541996544);
    CHECK(g);
    if (!g) return;
    const uint64_t k1a27 = varid::Make(0x1a27), k195 = varid::Make(0x195);
    int cv = g->Object(*g->node(181).fields, Hash("m_condition"));
    CHECK(cv >= 0);
    uint32_t pvp = kInvalidEntity;
    EvaluatorRegistry evals = EvaluatorRegistry::Builtin();
    evals.Register(0xd5384f53, std::make_shared<FunctionEvaluator>([&](const Graph&, int, EvalContext&, Primitive& out) {
                       out = Primitive::Entity(pvp);  // GetPvPEntity: succeeds with the invalid id when there is none
                       return true;
                   }));
    SyncManager capture(0x80000100, evals), gamemode(0x40000030, evals);
    capture.entity_resolver = [&](uint32_t e) { return e == gamemode.owner() ? &gamemode : nullptr; };
    Instance* inst = capture.CreateInstance(*g, {}, -1);
    EvalContext ctx{inst};
    // No gamemode entity: the capture instance's own $195 is read.
    inst->bag().Store(k195, Primitive::Int64(k1a27));
    CHECK(EvaluateBool(ctx, cv));
    inst->bag().Store(k195, Primitive::Int64(varid::Make(0x15)));
    CHECK(!EvaluateBool(ctx, cv));
    // Gamemode entity present: its $195 decides.
    pvp = gamemode.owner();
    gamemode.entity_bag().Store(k195, Primitive::Int64(k1a27));
    CHECK(EvaluateBool(ctx, cv));
    gamemode.entity_bag().Store(k195, Primitive::Int64(varid::Make(0x16)));
    CHECK(!EvaluateBool(ctx, cv));
    // The expression contains op 35: the BooleanSwitch polls it.
    CHECK(DependsOnExternal(ctx, cv));
}
