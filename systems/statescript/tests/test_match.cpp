// Match / objective node families (T055, docs/StatescriptNodes.md §5): offline runs of the gamemode brain and
// objective graphs in a MatchWorld (CASC bytes in fixtures/), with scripted inputs for the server's rules layer.
// SS_MATCH_LOG=1 prints each world's log.
#include <algorithm>
#include <cstdlib>
#include <format>
#include <map>
#include <memory>

#include "statescript/match_host.h"
#include "statescript/match_world.h"
#include "statescript/runtime.h"
#include "test.h"

using namespace ss;

namespace {

constexpr uint64_t VarId(uint32_t low) { return 0x0D80000000000000ull | low; }
constexpr uint64_t Msg(uint32_t low) { return 0x0240000000000000ull | low; }
constexpr uint64_t Param(uint32_t low) { return 0x0A40000000000000ull | low; }
constexpr uint64_t GraphId(uint32_t low) { return 0x0580000000000000ull | low; }
constexpr uint32_t Cf(double seconds) { return uint32_t(seconds * 1000.0 / 16.0 + 0.5); }

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

struct Rig {
    Graphs graphs;
    EvaluatorRegistry evals = EvaluatorRegistry::Builtin();
    std::unique_ptr<MatchWorld> w;
    Rig() {
        w = std::make_unique<MatchWorld>(evals, [this](uint64_t g) { return graphs.Get(g); });
        if (std::getenv("SS_MATCH_LOG")) w->on_log = [](const std::string& s) { std::printf("    %s\n", s.c_str()); };
    }
    ~Rig() {
        if (!std::getenv("SS_MATCH_UNMODELLED")) return;
        std::map<std::string, int> all;
        for (uint32_t id = 0x40000000; id < 0x40000400; id++)
            if (auto* e = w->Find(id)) {
                for (auto& [t, n] : e->sm->unmodelled_nodes) all[std::format("node {:08x}", t)] += n;
                for (auto& [t, n] : e->sm->unmodelled_states) all[std::format("state {:08x}", t)] += n;
                for (auto& [t, n] : e->sm->unmodelled_config_vars) all[std::format("cv {:08x}", t)] += n;
            }
        for (uint32_t id = 0x80000000; id < 0x80000400; id++)
            if (auto* e = w->Find(id)) {
                for (auto& [t, n] : e->sm->unmodelled_nodes) all[std::format("node {:08x}", t)] += n;
                for (auto& [t, n] : e->sm->unmodelled_states) all[std::format("state {:08x}", t)] += n;
                for (auto& [t, n] : e->sm->unmodelled_config_vars) all[std::format("cv {:08x}", t)] += n;
            }
        for (auto& [k, n] : all) std::printf("    unmodelled %s x%d\n", k.c_str(), n);
    }
    bool Logged(const std::string& text) const {
        for (auto& l : w->log)
            if (l.find(text) != std::string::npos) return true;
        return false;
    }
    Primitive Get(uint32_t entity, int16_t inst, uint32_t var) {
        Primitive p;
        MatchWorld::Entity* e = w->Find(entity);
        Instance* i = e ? e->sm->FindInstance(inst) : nullptr;
        uint64_t path[1] = {VarId(var)};
        if (!i || !i->GetVar(path, p)) p = Primitive::Make(PrimType::None, 0);
        return p;
    }
};

GameMessage Message(uint32_t id, std::vector<std::pair<uint64_t, Primitive>> params = {}) {
    GameMessage m;
    m.id = Msg(id);
    m.params = std::move(params);
    return m;
}

// Anubis (map 0x05b): brain B (def 0x39: 0x029, 0x08f, 0x1e1) and the two capture points (def 0x37, graph 0x028)
// with their placements' overrides and capture-area data (Maps.md; systems/data/maps). Point A = placed index 470,
// point B = 469. The capture zones' radius is not read from the shape asset: 6 m [?].
constexpr uint32_t kBrain = 0x40000030, kPointA = 0x800001d6, kPointB = 0x800001d5, kAttacker = 0x40000002,
                   kDefender = 0x40000003;

void SetupAnubis(Rig& r) {
    auto& brain = r.w->Add(kBrain, 0x0400000000000039);
    brain.pvp = true;
    for (uint32_t g : {0x029u, 0x08fu, 0x1e1u}) r.w->Start(kBrain, GraphId(g));

    auto& a = r.w->Add(kPointA, 0x0400000000000037);
    a.pos = {-43.780807f, 1.6629558f, 45.898396f};
    a.capture_area = true, a.capture_radius = 6.0f;
    a.rules = CaptureAreaRules{.decay_time = 10, .segments = 3, .extra_player = 1, .capture_time = 30, .f5f080cc5 = 30,
                               .decay_delay = 5};
    Override oa[] = {{VarId(0x02b8), Primitive::Int(1)}, {VarId(0x0c18), Primitive::Int(1)}};
    r.w->Start(kPointA, GraphId(0x028), oa);

    auto& b = r.w->Add(kPointB, 0x0400000000000037);
    b.pos = {-7.5f, 4.7f, 111.3f};
    b.capture_area = true, b.capture_radius = 6.0f;
    b.rules = CaptureAreaRules{.decay_time = 7, .segments = 3, .extra_player = 0.5f, .capture_time = 18, .f5f080cc5 = 22,
                               .decay_delay = 5, .f2ca7fc17 = 1, .f3a691fdf = true};
    Override ob[] = {{VarId(0x008d), Primitive::Int(1)}, {VarId(0x0090), Primitive::Byte(true)}, {VarId(0x02b8), Primitive::Int(2)},
                     {VarId(0x01a3), Primitive::Int(0)}, {VarId(0x0780), Primitive::Int(1)}, {VarId(0x0c18), Primitive::Int(2)},
                     {VarId(0x089f), Primitive::Byte(false)}};
    r.w->Start(kPointB, GraphId(0x028), ob);

    auto& att = r.w->Add(kAttacker);
    att.player = true, att.team = 1, att.pos = {-13.9f, 0, -161.2f};
    auto& def = r.w->Add(kDefender);
    def.player = true, def.team = 0, def.pos = {0, 0, 200};
}

}  // namespace

// The Assault flow offline: init (0x…0e, owner team 0) → phase 0x…15 (in progress) enables point A; an attacker in
// the zone captures it (progress 0 → 1 over the area's capture time), which disables A and, through the capture tick
// 0x…0b, enables B; then B is captured.
TEST(match_anubis_capture_offline) {
    Rig r;
    SetupAnubis(r);
    r.w->Step(2);
    MatchWorld::Entity* a = r.w->Find(kPointA);
    MatchWorld::Entity* b = r.w->Find(kPointB);
    CHECK(a && b);
    if (!a || !b) return;
    // The points start disabled ($0064 schema default false → 3be5172e).
    CHECK(!a->capture_enabled && !b->capture_enabled);
    // Rules layer: every capture point gets 0x…0e {0x…10 = the owning (defending) team 0}.
    r.w->Send(Message(0x0e, {{Param(0x10), Primitive::Int(0)}}), kPointA);
    r.w->Send(Message(0x0e, {{Param(0x10), Primitive::Int(0)}}), kPointB);
    r.w->Step(2);
    CHECK_EQ(a->capture_owner, 0);
    CHECK_EQ(b->capture_owner, 0);
    // Phase: in progress (0x0D8…15) to everyone: the brain opens the doors (0x…17), point A ($008d 0) enables itself.
    r.w->Send(Message(0x2d, {{Param(0x29), Primitive::Int64(VarId(0x15))}}));
    r.w->Step(2);
    CHECK(r.Logged("broadcast 17 from 40000030"));
    CHECK(a->capture_enabled);
    CHECK(!b->capture_enabled);
    // The attacker walks onto A: 30 s for one capturer.
    r.w->Find(kAttacker)->pos = a->pos;
    r.w->Step(Cf(5));  // 5 s
    float p5 = MatchCaptureProgress(*a->sm);
    CHECK(p5 > 0.14f && p5 < 0.19f);
    CHECK(r.Get(kPointA, -1, 0x06da).Truthy());  // "being captured" (plug 46562068 → SetVar #59)
    r.w->Step(Cf(26));
    CHECK(MatchCaptureProgress(*a->sm) >= 1.0f);
    CHECK(!a->capture_enabled);  // captured → #63 → $0064 := 0 → disabled
    CHECK(r.Logged("broadcast b from 800001d6"));  // the capture tick
    CHECK(b->capture_enabled);   // B: $0027 == $008d − 1 → enabled
    // B: 18 s.
    r.w->Find(kAttacker)->pos = b->pos;
    r.w->Step(Cf(19));
    CHECK(MatchCaptureProgress(*b->sm) >= 1.0f);
    CHECK(!b->capture_enabled);
    CHECK(r.Logged("broadcast b from 800001d5"));
}

// Contest and decay on A: attacker + defender → no progress; attacker alone → progress; attacker leaves → after the
// decay delay the progress falls back to the last locked third.
TEST(match_anubis_capture_contest_decay) {
    Rig r;
    SetupAnubis(r);
    r.w->Step(2);
    MatchWorld::Entity* a = r.w->Find(kPointA);
    r.w->Send(Message(0x0e, {{Param(0x10), Primitive::Int(0)}}), kPointA);
    r.w->Send(Message(0x2d, {{Param(0x29), Primitive::Int64(VarId(0x15))}}));
    r.w->Step(2);
    r.w->Find(kAttacker)->pos = a->pos;
    r.w->Find(kDefender)->pos = a->pos;
    r.w->Step(Cf(3));
    CHECK_EQ(MatchCaptureProgress(*a->sm), 0.0f);
    r.w->Find(kDefender)->pos = {0, 0, 200};
    r.w->Step(Cf(12));  // 12 s of 30: 0.4 (one third locked)
    float p = MatchCaptureProgress(*a->sm);
    CHECK(p > 0.38f && p < 0.42f);
    r.w->Find(kAttacker)->pos = {0, 0, -200};
    r.w->Step(Cf(4));  // within the decay delay: kept
    CHECK_EQ(MatchCaptureProgress(*a->sm), p);
    r.w->Step(Cf(5));
    float q = MatchCaptureProgress(*a->sm);
    CHECK(q > 0.33f && q < 0.34f);  // back to the locked third
}

namespace {

// The tutorial (map 0x661, MatchLogic.md §13): brain def 0x128E (graph 0xB41), the player (Soldier: 76) and the
// tagged spots step 1 uses (placements.json; tags from the definitions: 0x6a → 16D Tracer's spawn, 0x6f…0x73 → 1BE,
// 1A9, 1E5, 1E6, 1BF her spots, 0x77 → 2A6 the player's teleport target).
constexpr uint32_t kTutBrain = 0x40000030, kPlayer = 0x40000002;

void SetupTutorial(Rig& r) {
    auto& brain = r.w->Add(kTutBrain, 0x040000000000128E);
    brain.pvp = true;
    r.w->Start(kTutBrain, GraphId(0xB41));
    auto& p = r.w->Add(kPlayer, 0x0400000000000000);
    p.player = true, p.team = 0, p.pos = {-1, 0, -7};
    struct Spot {
        uint32_t id, tag;
        Vec3f pos;
    } spots[] = {{0x8000006a, 0x16d, {-16, 4, 8}},           {0x8000006f, 0x1be, {-1.5f, 1, 8}},
                 {0x80000070, 0x1a9, {11.61f, -1, 3.0f}},     {0x80000071, 0x1e5, {-1.2f, 1, 8}},
                 {0x80000072, 0x1e6, {-8.57f, 4, -1.51f}},    {0x80000073, 0x1bf, {-1, 0, -2}},
                 {0x80000077, 0x2a6, {-45.06f, 5, 8.95f}}};
    for (auto& s : spots) {
        auto& e = r.w->Add(s.id);
        e.pos = s.pos;
        e.tags = {0x0D60000000000000ull | s.tag};
    }
}

void SetPlayerVar(Rig& r, uint32_t var, bool v) {
    r.w->Find(kPlayer)->sm->entity_bag().Set(VarId(var), Primitive::Byte(v));
}

}  // namespace

// Step 1 of the tutorial offline: the player graph starts (0x…1db → PlayScript 0xB40 → 0x…1dc back), the step pointer
// $1721 = 1 starts 0xB55; Athena's lines play (VoiceStimulus for their length), the objective "Look up at the
// dropship." reaches the player's 0xB40 ($1a42 / $1a44); the player looks up ($172e, scripted: what 0xB40's
// TrackTargets + BooleanSwitch would write); Tracer is created at tag 16D and moved between her spots; each time the
// player finds her ($1790, scripted) the step goes on; at the end $1721 = 2 (step 2).
TEST(match_tutorial_step1_offline) {
    Rig r;
    SetupTutorial(r);
    r.w->Step(2);
    r.w->Send(Message(0x1db), kTutBrain);  // the player's graph 0x91B sends it to GetPvPEntity() [D]
    r.w->Step(2);
    MatchWorld::Entity* player = r.w->Find(kPlayer);
    MatchWorld::Entity* brain = r.w->Find(kTutBrain);
    bool b40 = false;
    for (auto& i : player->sm->instances()) b40 |= i->graph().guid() == GraphId(0xB40);
    CHECK(b40);
    uint64_t step_path[1] = {VarId(0x1721)};
    auto step = [&] {
        const Var* v = brain->sm->entity_bag().Find(step_path[0]);
        return v ? v->value().AsInt() : -1;
    };
    CHECK_EQ(step(), 1);
    // Athena: 2 s wait, "Hello, Soldier: 76. This is Athena." (4 s), "I will be guiding you…" (4 s), "Let's begin. First
    // look UP…" (4.5 s) with a 3 s wait, then the reminder loop (8–10 s) sends the objective "Look up at the dropship."
    // (0x…5bb) to the player's 0xB40: $1a42 = the text, $1a44 = shown.
    auto shown = [&] {
        for (auto& i : player->sm->instances())
            if (i->graph().guid() == GraphId(0xB40)) {
                uint64_t p[1] = {VarId(0x1a44)};
                Primitive v;
                return i->GetVar(p, v) && v.Truthy();
            }
        return false;
    };
    int waited = 0;
    while (!shown() && waited < 40 * 4) r.w->Step(Cf(0.25)), waited++;
    CHECK(shown());
    CHECK(waited > 15 * 4 && waited < 30 * 4);
    // The player looks up at the dropship.
    SetPlayerVar(r, 0x172e, true);
    // Then Tracer: found each time she moves (1 s after a teleport, for 1 s).
    uint32_t tracer = 0;
    size_t seen = r.w->log.size();
    int64_t found_at = -1;
    for (int s = 0; s < 240 * 4 && step() == 1; s++) {
        r.w->Step(Cf(0.25));
        for (; seen < r.w->log.size(); seen++) {
            const std::string& l = r.w->log[seen];
            if (!tracer && l.find("def 1 team 1") != std::string::npos) tracer = r.w->next_id - 1;
            if (tracer && l.find(std::format("teleport {:x}", tracer)) != std::string::npos) found_at = r.w->ms() + 1000;
        }
        if (found_at >= 0 && r.w->ms() >= found_at) {
            SetPlayerVar(r, 0x1790, true);
            r.w->Step(Cf(1));
            SetPlayerVar(r, 0x1790, false);
            found_at = -1;
        }
    }
    CHECK(tracer != 0);
    CHECK_EQ(step(), 2);
    if (std::getenv("SS_MATCH_LOG"))
        for (auto& [e, h] : r.w->host_events) std::printf("    host %s\n", h.c_str());
}

namespace {

// Dorado (map 0x2C3, MatchLogic.md §12): brain def 0x12e3 (0x10f, 0x1e1, 0x219), the cart (placement 0x22a, def
// 0x77f: 0x098 + 0xa33) and the five waypoints (def 0x15c, graph 0x0d2) with their placements' overrides. The path
// is the polyline start → waypoints [?: the waypoints' spline tangents are not used].
constexpr uint32_t kDBrain = 0x40000030, kCart = 0x8000022a, kDAttacker = 0x40000002, kDDefender = 0x40000003;
constexpr uint32_t kWaypoints[5] = {0x800001d9, 0x800001f0, 0x800001f2, 0x800001f3, 0x80000200};

void SetupDorado(Rig& r) {
    auto& brain = r.w->Add(kDBrain, 0x04000000000012e3);
    brain.pvp = true;
    for (uint32_t g : {0x10fu, 0x1e1u, 0x219u}) r.w->Start(kDBrain, GraphId(g));
    struct Wp {
        Vec3f pos;
        std::vector<Override> ov;
    } wps[5] = {
        {{73.15f, 7.09f, -11.6f}, {{VarId(0x02b8), Primitive::Int(1)}, {VarId(0x0c18), Primitive::Int(1)},
                                  {VarId(0x02b0), Primitive::Int(180)}, {VarId(0x03bb), Primitive::Int(15)}}},
        {{131.18f, 11.09f, 26.13f}, {{VarId(0x0c18), Primitive::Int(2)}, {VarId(0x02b0), Primitive::Int(120)},
                                    {VarId(0x02b8), Primitive::Int(2)}, {VarId(0x0df4), Primitive::Byte(false)}}},
        {{146.28f, 11.04f, 30.35f}, {{VarId(0x098e), Primitive::Byte(false)}, {VarId(0x089f), Primitive::Byte(false)},
                                    {VarId(0x0df4), Primitive::Byte(true)}, {VarId(0x02b0), Primitive::Int(0)},
                                    {VarId(0x02b8), Primitive::Int(3)}, {VarId(0x03bb), Primitive::Int(5)}}},
        {{158.71f, 10.89f, 33.7f}, {{VarId(0x089f), Primitive::Byte(false)}, {VarId(0x098e), Primitive::Byte(false)},
                                   {VarId(0x02b0), Primitive::Int(0)}, {VarId(0x02b8), Primitive::Int(4)}}},
        {{184.19f, 13.94f, 40.32f}, {{VarId(0x0c18), Primitive::Int(3)}, {VarId(0x02b0), Primitive::Int(0)},
                                    {VarId(0x098e), Primitive::Byte(false)}, {VarId(0x089f), Primitive::Byte(false)},
                                    {VarId(0x02b8), Primitive::Int(5)}}}};
    auto& cart = r.w->Add(kCart, 0x040000000000077f);
    cart.pos = {6.01f, -0.22f, 6.45f};
    cart.path.push_back(cart.pos);
    for (int k = 0; k < 5; k++) {
        auto& w = r.w->Add(kWaypoints[k], 0x040000000000015c);
        w.pos = wps[k].pos;
        r.w->Find(kCart)->path.push_back(w.pos);
        r.w->Find(kCart)->path_waypoints[k + 1] = kWaypoints[k];
        r.w->Start(kWaypoints[k], GraphId(0x0d2), wps[k].ov);
    }
    Override co[] = {{VarId(0x06ee), Primitive::Byte(false)}};
    r.w->Start(kCart, GraphId(0x098), co);
    r.w->Start(kCart, GraphId(0xa33));
    auto& att = r.w->Add(kDAttacker);
    att.player = true, att.team = 1, att.pos = {0, 0, 0};
    auto& def = r.w->Add(kDDefender);
    def.player = true, def.team = 0, def.pos = {300, 0, 300};
}

}  // namespace

// Escort offline: setup phase (0x…16) arms the cart (0x…2cb), in progress (0x…264) starts it (0x…66); an attacker
// next to the cart pushes it (speed by the attacker count); a defender next to it stops it (contest); waypoint 1 adds
// 180 s and broadcasts the progress 0x…71 {1}; the cart reaches the end.
TEST(match_dorado_payload_offline) {
    Rig r;
    SetupDorado(r);
    r.w->Step(2);
    r.w->Send(Message(0x2d, {{Param(0x29), Primitive::Int64(VarId(0x16))}}));
    r.w->Step(2);
    CHECK(r.Logged("broadcast 2cb from 40000030"));
    r.w->Send(Message(0x2d, {{Param(0x29), Primitive::Int64(VarId(0x264))}}));
    r.w->Step(2);
    CHECK(r.Logged("broadcast 66 from 40000030"));
    CHECK(r.Logged("broadcast 17 from 40000030"));
    MatchWorld::Entity* cart = r.w->Find(kCart);
    auto follow = [&](uint32_t who, int s) {
        for (int k = 0; k < Cf(s); k++) {
            r.w->Find(who)->pos = cart->pos;
            r.w->Step(1);
        }
    };
    // Nobody near: it does not move forward.
    r.w->Step(Cf(2));
    CHECK(cart->path_distance == 0.0f);
    follow(kDAttacker, 10);
    float d10 = cart->path_distance;
    CHECK(d10 > 5.0f);
    // Contest: the defender joins; after the 1 s delay ($0bd8) the cart stops.
    for (int k = 0; k < Cf(4); k++) {
        r.w->Find(kDAttacker)->pos = cart->pos;
        r.w->Find(kDDefender)->pos = cart->pos;
        r.w->Step(1);
    }
    float c0 = cart->path_distance;
    for (int k = 0; k < Cf(3); k++) {
        r.w->Find(kDAttacker)->pos = cart->pos;
        r.w->Find(kDDefender)->pos = cart->pos;
        r.w->Step(1);
    }
    CHECK(cart->path_distance == c0);
    r.w->Find(kDDefender)->pos = {300, 0, 300};
    follow(kDAttacker, 240);
    CHECK(r.Logged("waypoint 800001d9"));
    CHECK(r.w->match_time_added >= 180.0f);
    CHECK(r.Logged("broadcast 71 from 800001d9"));
    CHECK(cart->path_distance > 150.0f);
}

// Brain A (def 0x13: 0x00f phase machine + 0x014 team deathmatch rules) offline: setup (0x…1a), two players join (the
// player-event state) → teams balanced (0 then 1) → both teams present → phase 0x…15 (in progress); 25 kills by team 0
// → winner team 0 → phase 0x…18 → GameFinished 10 s later.
TEST(match_brain_a_tdm_offline) {
    Rig r;
    constexpr uint32_t kA = 0x40000031, kP1 = 0x40000002, kP2 = 0x40000003;
    auto& brain = r.w->Add(kA, 0x0400000000000013);
    brain.pvp = true;
    r.w->Start(kA, GraphId(0x00f));
    r.w->Start(kA, GraphId(0x014));
    for (uint32_t p : {kP1, kP2}) r.w->Add(p).player = true;
    r.w->Step(2);
    auto phase = [&] {
        Primitive v;
        for (auto& i : brain.sm->instances())
            if (i->graph().guid() == GraphId(0x00f)) {
                uint64_t p[2] = {VarId(0x0004), VarId(0x0195)};  // an owner var: the entity bag
                if (i->GetVar(p, v)) return v.bits & 0xFFFF;
            }
        return uint64_t(0);
    };
    CHECK_EQ(phase(), uint64_t(0x197));  // initial
    MatchPlayerEvent(*brain.sm, PlayerEvent::Joined, kP1);
    r.w->Step(2);
    CHECK_EQ(r.w->Team(kP1), 0);
    CHECK_EQ(phase(), uint64_t(0x18d));  // the first player: waiting (0x…2a → 0x…19)
    r.w->Send(Message(0x1a), kA);        // the rules layer: setup begins [?]
    r.w->Step(2);
    CHECK_EQ(phase(), uint64_t(0x16));
    MatchPlayerEvent(*brain.sm, PlayerEvent::Joined, kP2);
    r.w->Step(2);
    CHECK_EQ(r.w->Team(kP2), 1);
    CHECK_EQ(phase(), uint64_t(0x15));  // both teams have a player during setup: in progress (0x…05 → 0x…07)
    Primitive global;  // PublishGlobals (a4f3be65, T060): the phase as global var 0x…195
    CHECK(MatchGlobalVar(*brain.sm, VarId(0x0195), global) && (global.bits & 0xFFFF) == 0x15);
    for (int k = 0; k < 25; k++) {
        MatchPlayerEvent(*brain.sm, PlayerEvent::Died, kP2, kP1);
        r.w->Step(Cf(1));
    }
    CHECK_EQ(phase(), uint64_t(0x18));
    CHECK_EQ(r.w->winner, -2);
    r.w->Step(Cf(11));
    CHECK(r.w->winner != -2);
}

// Objective progress to the spawn rooms (Maps.md §4, §6): a spawn point (0x0d6, From 1) is disabled until the progress
// message 0x…71 {0x…55 = 1}; a spawn-room volume (0x1ce, team 1, From 0 / Until 1) sets its owner's team, carries the
// room tag 0x0D6…35 from the start and drops it at progress 1; the healing area (0x0a7) tracks the players inside it
// and heals them (ModifyHealth: a host event) and applies its aura.
TEST(match_spawn_rooms_and_healing_offline) {
    Rig r;
    constexpr uint32_t kSpawn = 0x800001dd, kVolume = 0x80000300, kHeal = 0x80000301, kP = 0x40000002;
    auto& sp = r.w->Add(kSpawn, 0x0400000000000167);
    Override so[] = {{VarId(0x02b9), Primitive::Int(1)}, {VarId(0x02ba), Primitive::Int(0)}};
    r.w->Start(kSpawn, GraphId(0x0d6), so);
    auto& vol = r.w->Add(kVolume, 0x040000000000029f);
    Override vo[] = {{VarId(0x03f8), Primitive::Int(1)}, {VarId(0x02b9), Primitive::Int(0)}, {VarId(0x02ba), Primitive::Int(1)},
                     {VarId(0x06ee), Primitive::Byte(true)}};
    r.w->Start(kVolume, GraphId(0x1ce), vo);
    auto& heal = r.w->Add(kHeal, 0x040000000000013C);
    heal.pos = {10, 0, 10}, heal.capture_radius = 8;
    r.w->Start(kHeal, GraphId(0x0a7));
    auto& p = r.w->Add(kP);
    p.player = true, p.team = 1, p.pos = {100, 0, 100};
    r.w->Step(2);
    CHECK_EQ(vol.team, 1);
    CHECK(std::find(vol.tags.begin(), vol.tags.end(), 0x0D60000000000035ull) != vol.tags.end());
    sp.spawn_enabled = false;
    r.w->Send(Message(0x71, {{Param(0x55), Primitive::Int(1)}}));
    r.w->Step(2);
    CHECK(sp.spawn_enabled);
    CHECK(std::find(vol.tags.begin(), vol.tags.end(), 0x0D60000000000035ull) == vol.tags.end());
    // Healing: the player walks into the room; TrackTargets (every 12 CF) picks them up → ModifyHealth + ApplyAura begin.
    p.pos = {12, 0, 9};
    r.w->Step(30);
    bool healed = false;
    for (auto& [e, h] : r.w->host_events) healed |= e == kHeal && h.find("619d9e95 begin") != std::string::npos;
    CHECK(healed);
    const Var* tracked = nullptr;
    for (auto& i : heal.sm->instances()) {
        uint64_t path[1] = {VarId(0x0177)};
        tracked = i->FindDictionary(path);
    }
    CHECK(tracked && tracked->dict_size() == 1);
}
