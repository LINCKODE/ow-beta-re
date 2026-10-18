// The host-system state types and the last server-path types (T060, docs/StatescriptNodes.md §3.14–3.16), on their
// real graphs (CASC bytes in fixtures/) in a MatchWorld whose movement / collision answers are scripted here.
#include <cmath>
#include <format>
#include <memory>

#include "statescript/capabilities.h"
#include "statescript/match_world.h"
#include "statescript/runtime.h"
#include "test.h"

using namespace ss;

namespace {

constexpr uint64_t GraphId(uint32_t low) { return 0x0580000000000000ull | low; }
constexpr uint64_t VarId(uint32_t low) { return 0x0D80000000000000ull | low; }

struct Graphs {
    std::vector<std::unique_ptr<Graph>> list;
    const Graph* Get(uint64_t guid) {
        for (auto& g : list)
            if (g->guid() == guid) return g.get();
        std::string err;
        auto g = Graph::Load(stu::ReadFile(std::format("{}/graph_0x{:03x}.stud", SS_FIXTURES, guid & 0xFFF)), guid, err);
        if (!g) return nullptr;
        list.push_back(std::move(g));
        return list.back().get();
    }
};

// A world whose engine answers are scripted per test.
struct World : MatchWorld {
    using MatchWorld::MatchWorld;
    std::function<std::optional<CastHit>(const Vec3f&, const Vec3f&)> cast;
    std::function<std::vector<uint32_t>(float radius)> shock;
    std::vector<MoverController> movers;
    std::vector<uint64_t> mover_ends;
    std::vector<std::array<uint32_t, 4>> scales;  // start, ramp end, stop, stop end
    int32_t reel_id = -1;
    std::vector<std::pair<int32_t, float>> reels;
    std::optional<CastHit> Cast(uint32_t, const Vec3f& a, const Vec3f& b) override { return cast ? cast(a, b) : std::nullopt; }
    std::vector<uint32_t> ShockwaveHits(uint32_t, const Vec3f&, float, float, float r) override {
        return shock ? shock(r) : std::vector<uint32_t>{};
    }
    void MoverAdd(uint32_t, const MoverController& c) override { movers.push_back(c); }
    void MoverEnd(uint32_t, uint64_t key, uint32_t) override { mover_ends.push_back(key); }
    void TimeScale(uint32_t, uint64_t, float, uint32_t a, uint32_t b, uint32_t c, uint32_t d) override {
        scales.push_back({a, b, c, d});
    }
    int32_t CreateReel(uint32_t, int32_t secs, float offset) override {
        reels.push_back({secs, offset});
        return reel_id;
    }
};

struct Rig {
    Graphs graphs;
    EvaluatorRegistry evals = EvaluatorRegistry::Builtin();
    std::shared_ptr<World> w;
    static constexpr uint32_t kHero = 0x40000031;
    Rig() {
        w = std::make_shared<World>(evals, [this](uint64_t g) { return graphs.Get(g); });
        w->SetFrame(1000);
        w->Add(kHero).player = true;
    }
    SyncManager& sm() { return *w->Find(kHero)->sm; }
    // Starts `graph` on the hero and begins its first state of `type` directly; returns it.
    State* Begin(uint32_t graph, uint32_t type, Instance** out = nullptr) {
        Instance* i = w->Start(kHero, GraphId(graph));
        if (!i) return nullptr;
        if (out) *out = i;
        for (auto& n : i->graph().nodes())
            if (n.type == type && n.state_index >= 0) {
                State& s = i->EnsureState(n.state_index);
                if (!s.active()) s.DoBegin();
                return &s;
            }
        return nullptr;
    }
    Primitive Var(Instance* i, int cv) {
        Primitive p;
        auto path = i->graph().DynamicPath(cv);
        if (path.empty() || !i->GetVar(path, p)) p = Primitive::Make(PrimType::None, 0);
        return p;
    }
    int Cv(Instance* i, State* s, uint32_t field) { return i->graph().Object(*s->node().fields, field); }
};

}  // namespace

// Shockwave (0x0be node 1: speed 40, distance 20): finishes at 500 ms; the ring's hits are appended once each.
TEST(host_shockwave_timing_and_targets) {
    Rig r;
    r.w->shock = [](float radius) { return radius > 10.0f ? std::vector<uint32_t>{0x40000050, 0x40000051} : std::vector<uint32_t>{}; };
    Instance* i;
    State* s = r.Begin(0x0be, 0x8d50485a, &i);
    CHECK(s && s->active());
    if (!s) return;
    int64_t begin = s->begin_time();
    int64_t ended = -1;
    for (int k = 0; k < 40 && ended < 0; k++) {
        r.w->Step();
        if (!s->active()) ended = r.sm().time();
    }
    CHECK(ended >= begin + 500 && ended < begin + 500 + 16);
    const Var* t = i->FindDictionary(i->graph().DynamicPath(r.Cv(i, s, Hash("m_out_Targets"))));
    CHECK(t && t->dict_size() == 2);
}

// ValidateWidth (0x622): the four out vars read through the state, per side min / max of the clear distances.
TEST(host_validate_width_state_values) {
    Rig r;
    // Each recompute casts f92d850c (4 in 0x622) rays along +direction, then as many along −direction: the + side
    // hits at 40 % of the ray.
    int calls = 0;
    r.w->cast = [&](const Vec3f&, const Vec3f& b) -> std::optional<MatchHost::CastHit> {
        if (calls++ / 4 % 2 == 0) return MatchHost::CastHit{0.4f, b, kInvalidEntity};
        return std::nullopt;
    };
    Instance* i;
    State* s = r.Begin(0x622, 0x12aa4f0f, &i);
    CHECK(s && s->active());
    if (!s) return;
    r.w->Step();
    EvalContext ctx{i};
    float len = EvaluateFloat(ctx, r.Cv(i, s, 0xb73d0304), 0.0f);
    // 0x622 links the widest clear distance per side: b43e4062 (+, extra 1) = $48c, f5b00a5f (−, extra 3) = $490.
    Primitive a = r.Var(i, r.Cv(i, s, 0xb43e4062)), b = r.Var(i, r.Cv(i, s, 0xf5b00a5f));
    CHECK(a.type == PrimType::Float && b.type == PrimType::Float);
    CHECK(len > 0.0f && std::abs(a.AsFloat() - 0.4f * len) < 1e-3f);
    CHECK(std::abs(b.AsFloat() - len) < 1e-3f);
    s->RequestEnd(false);
    r.w->Step();
    CHECK(!s->active());
    Primitive after = r.Var(i, r.Cv(i, s, 0xb43e4062));
    CHECK(after.type == PrimType::Float && after.AsFloat() == a.AsFloat());  // released with its last value
}

// Extender (0x05a: speed 40, max 20): without hits it aborts itself at 20 / 40 s and reports no hit; with a hit at
// 25 % it aborts at 125 ms and writes the hit.
TEST(host_extender_aborts_at_length_or_hit) {
    for (int with_hit = 0; with_hit < 2; with_hit++) {
        Rig r;
        if (with_hit) r.w->cast = [](const Vec3f&, const Vec3f& b) -> std::optional<MatchHost::CastHit> {
            return MatchHost::CastHit{0.25f, b, 0x40000060};
        };
        Instance* i;
        State* s = r.Begin(0x05a, 0x26cb90c0, &i);
        CHECK(s && s->active());
        if (!s) return;
        int64_t begin = s->begin_time(), ended = -1;
        for (int k = 0; k < 60 && ended < 0; k++) {
            r.w->Step();
            if (!s->active()) ended = r.sm().time();
        }
        int64_t want = with_hit ? 125 : 500;
        CHECK(ended >= begin + want && ended < begin + want + 16);
        Primitive hit = r.Var(i, r.Cv(i, s, 0x40da3d88));
        CHECK(hit.type == PrimType::Byte && (hit.bits != 0) == (with_hit != 0));
        if (with_hit) {
            Primitive e = r.Var(i, r.Cv(i, s, 0xec477088));
            CHECK(e.type == PrimType::Entity && uint32_t(e.bits) == 0x40000060);
        }
    }
}

// HistoryBuilder / HistoryRewinder (Tracer 0x01a): the builder's handle, the rewinder finishing at its duration.
TEST(host_history_builder_and_rewinder) {
    Rig r;
    Instance* i;
    State* b = r.Begin(0x01a, 0x029bf346, &i);
    CHECK(b && b->active());
    if (!b) return;
    Primitive h = r.Var(i, r.Cv(i, b, Hash("m_out_Handle")));
    CHECK(h.type == PrimType::IntVec4 && (h.bits & 0xFFFF) == 2 && uint16_t(h.bits >> 32) == uint16_t(b->index()));
    State* rw = nullptr;
    for (auto& n : i->graph().nodes())
        if (n.type == 0x7211cf70) rw = &i->EnsureState(n.state_index);
    CHECK(rw != nullptr);
    if (!rw) return;
    auto dur = i->graph().DynamicPath(r.Cv(i, rw, 0x85f47ed7));
    if (!dur.empty()) i->SetVar(dur, Primitive::Float(0.9f));
    rw->DoBegin();
    int64_t begin = rw->begin_time(), ended = -1;
    for (int k = 0; k < 80 && ended < 0; k++) {
        r.w->Step();
        if (!rw->active()) ended = r.sm().time();
    }
    CHECK(ended >= begin + 900 && ended < begin + 916);
    BitOut d;
    rw->WriteData(d, false);
    CHECK_EQ(d.nbits, 16);
}

// The round end 0x12d (brain B / Dorado, message 0x…45): the reel actions (CreateReel writes the host's id into
// $55e) and ScaleTime (start = CF + 15, data = bit active + u32 start).
TEST(host_round_end_reel_and_scale_time) {
    Rig r;
    r.w->reel_id = 77;
    Instance* i = r.w->Start(Rig::kHero, GraphId(0x12d));
    CHECK(i != nullptr);
    if (!i) return;
    GameMessage m;
    m.id = 0x0240000000000045;
    r.w->Send(m, Rig::kHero);
    r.w->Step(2);
    CHECK_EQ(r.w->reels.size(), size_t(1));
    if (!r.w->reels.empty()) CHECK_EQ(r.w->reels[0].first, 12);
    uint64_t p55e[1] = {VarId(0x055e)};
    Primitive v;
    CHECK(i->GetVar(p55e, v) && v.type == PrimType::Int && int32_t(v.bits) == 77);
    CHECK(!r.w->scales.empty());
    State* st = nullptr;
    for (auto& n : i->graph().nodes())
        if (n.type == 0x7bf31629) st = i->state(n.state_index);
    CHECK(st && st->active());
    if (st && !r.w->scales.empty()) {
        BitOut d;
        st->WriteData(d, false);
        CHECK_EQ(d.nbits, 33);
        uint32_t start = 0;
        for (int k = 0; k < 32; k++) start |= uint32_t(d.bytes[(k + 1) / 8] >> ((k + 1) % 8) & 1) << k;
        CHECK((d.bytes[0] & 1) && start == r.w->scales[0][0] && r.w->scales[0][1] == start + 30);
    }
    int reel_events = 0;
    for (auto& e : r.sm().host_events()) reel_events += e.type == 0xcc7b6828 || e.type == 0x6dbdfe7c;
    CHECK_EQ(reel_events, 2);
}

// Mover controllers: Accelerate (0x025, kind 6) registers with the host and ends with its state.
TEST(host_mover_controller_accelerate) {
    Rig r;
    Instance* i;
    State* s = r.Begin(0x025, 0xfea1e74b, &i);
    CHECK(s && s->active());
    if (!s) return;
    CHECK_EQ(r.w->movers.size(), size_t(1));
    if (r.w->movers.empty()) return;
    auto& c = r.w->movers[0];
    CHECK(c.kind == 6 && c.p[1] == 50.0f && c.p[3] == 2.0f && c.start_cf == r.sm().cf() + 1);
    s->RequestEnd(false);
    r.w->Step();
    CHECK(r.w->mover_ends.size() == 1 && r.w->mover_ends[0] == c.key);
}

// Coverage: no type of the T060 worklist is unmodelled any more.
TEST(t060_worklist_levels) {
    for (uint32_t t : {0x3f67b31bu, 0xcf08df77u, 0xba8e336eu, 0x1787a82du, 0xc0a88bb0u, 0x26cb90c0u, 0x7bf31629u,
                       0x1a81cfb7u, 0xf8ec9531u, 0x7211cf70u, 0x029bf346u, 0xfea1e74bu, 0xc8c71bbcu, 0x76391dd3u,
                       0x8d50485au, 0x12db64dau, 0xf536a60fu, 0x82505a1bu, 0x12aa4f0fu, 0xa4f3be65u, 0xadfc9542u,
                       0x56a2012bu, 0x3f836f1au, 0x827387eeu, 0x9ec82686u, 0x51870208u, 0xadc6d1a0u, 0x5e7fbc32u,
                       0xe4a99cc5u, 0xc29cf678u, 0xe2c530ffu, 0x31993d91u, 0xfafb9471u, 0x9f762d9fu, 0x9161fb46u,
                       0x9521c2b9u, 0xd07ca4abu, 0xb9af22dbu, 0x9f8f5a55u, 0xcc7b6828u, 0xd5c8d941u, 0x6dbdfe7cu}) {
        TypeCap c = Capability(t);
        if (c.level == CapLevel::Unmodelled) std::printf("  unmodelled %08x\n", t);
        CHECK(c.level != CapLevel::Unmodelled);
    }
}
