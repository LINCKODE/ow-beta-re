// Actions, conditions and config vars of the match / objective families (T055, docs/StatescriptNodes.md §5). Most
// have no client class (server-only logic, designed [?]); the world is the host's (SyncManager::match_host).
#include <algorithm>
#include <cmath>
#include <cstring>

#include "match.h"
#include "statescript/match_host.h"
#include "statescript/runtime.h"

namespace ss {

namespace {

constexpr uint32_t kBroadcastGameMessage = 0xece45723, kTeleportTo = 0xde4f0f88, kSetTeamIndex = 0xa257245a,
                   kAddTargetTags = 0xbd6a3dff, kRemoveTargetTags = 0x78184003, kRespawnPlayer = 0x4f14c91b,
                   kGameFinished = 0xf90c6c08, kEndMatch = 0xf1201eda, kDestroyEntity = 0x5c46d801,
                   kCreateEntityAction = 0x0e3689f3, kCaptureEnable = 0x3be5172e, kCaptureOwner = 0x350e207d,
                   kAddMatchTime = 0x670cf926, kSpawnEnable = 0x12213f7e, kCriteria = 0x4e83bc01,
                   kInstanceEvent = 0xeaee2743;

MatchHost* Host(const Instance& inst) { return inst.sm().match_host.get(); }
int Cv(const Graph& g, const Node& n, uint32_t field) { return g.Object(*n.fields, field); }
int Cv(const Graph& g, int cv, uint32_t field) { return g.Object(g.value(cv), field); }

// Every valid entity a config var iterates to.
std::vector<uint32_t> Entities(EvalContext& ctx, int cv) {
    std::vector<uint32_t> out;
    for (auto& [k, v] : Iterate(ctx, cv))
        if (v.type == PrimType::Entity && !IsInvalidEntity(v.AsU32())) out.push_back(v.AsU32());
    return out;
}
std::vector<uint64_t> Guids(EvalContext& ctx, int cv) {
    std::vector<uint64_t> out;
    if (cv < 0) return out;
    for (auto& [k, v] : Iterate(ctx, cv))
        if (v.type == PrimType::Int64 && v.bits && std::find(out.begin(), out.end(), v.bits) == out.end())
            out.push_back(v.bits);
    return out;
}
Vec3f ToVec(const Primitive& p) {
    uint32_t raw[3] = {uint32_t(p.bits), uint32_t(p.bits >> 32), p.extra};
    Vec3f v;
    std::memcpy(&v.x, &raw[0], 4), std::memcpy(&v.y, &raw[1], 4), std::memcpy(&v.z, &raw[2], 4);
    return v;
}
// A position operand: an ENTITY (the host's position of it) or a VEC3.
bool PositionOf(MatchHost& h, const Primitive& p, Vec3f& out) {
    if (p.type == PrimType::Vec3) return out = ToVec(p), true;
    if (p.type == PrimType::Entity && !IsInvalidEntity(p.AsU32())) return h.Position(p.AsU32(), out);
    return false;
}

// BroadcastGameMessage (ece45723, base f9f1e339 as SendGameMessage; no client class) [?]: the message built as
// SendGameMessage builds it (m_gameMessage, m_params, instigator 694d2b8b / owner var / sender), delivered by the host
// to every entity's Statescript (docs §5.2).
void Broadcast(Instance& inst, const Node& n) {
    MatchHost* h = Host(inst);
    if (!h) return;
    const Graph& g = inst.graph();
    EvalContext ctx{&inst};
    GameMessage msg;
    msg.id = EvaluateGuid(ctx, Cv(g, n, Hash("m_gameMessage")));
    if (!msg.id) return;
    msg.sender = inst.sm().owner();
    uint32_t instigator = kInvalidEntity;
    Primitive owner;
    uint64_t path[1] = {varid::Owner};
    if (inst.GetVar(path, owner) && owner.type == PrimType::Entity) instigator = owner.AsU32();
    if (int cv = Cv(g, n, 0x694d2b8b); cv >= 0) {
        Primitive e;
        if (Evaluate(ctx, cv, e)) instigator = e.type == PrimType::Entity ? e.AsU32() : kInvalidEntity;
    }
    msg.instigator = IsInvalidEntity(instigator) ? msg.sender : instigator;
    for (int p : g.Objects(*n.fields, Hash("m_params"))) {
        if (p < 0) continue;
        uint64_t key = Graph::RefGuid(g.value(p).Find(0xc76b6366));
        if (!key) continue;
        for (auto& [k, v] : Iterate(ctx, g.Object(g.value(p), Hash("m_value")))) msg.params.emplace_back(key, v);
    }
    h->Broadcast(msg);
}

// TeleportTo (de4f0f88; no client class) [?]: every entity m_target iterates to goes to 91255cca (an entity's position
// or a VEC3), facing 0b4169ac: a VEC3 of angles (663394d9 output: x = yaw degrees) or a number (yaw degrees; 0 in
// the tutorial brain's teleports of the player).
void Teleport(Instance& inst, const Node& n) {
    MatchHost* h = Host(inst);
    if (!h) return;
    const Graph& g = inst.graph();
    EvalContext ctx{&inst};
    Primitive dest;
    Vec3f pos;
    if (!Evaluate(ctx, Cv(g, n, 0x91255cca), dest) || !PositionOf(*h, dest, pos)) return;
    std::optional<float> yaw;
    Primitive f;
    if (Evaluate(ctx, Cv(g, n, 0x0b4169ac), f)) {
        if (f.type == PrimType::Vec3) yaw = ToVec(f).x;
        else if (f.type == PrimType::Int || f.type == PrimType::Float) yaw = f.ToFloat();
    }
    for (uint32_t e : Entities(ctx, Cv(g, n, Hash("m_target")))) h->Teleport(e, pos, yaw);
}

bool Filter_(Instance& inst, uint32_t entity, int filter_cv);

}  // namespace

// CreateEntity (action 0e3689f3 / state e7c6aaf6) [?]: m_entityDef (GUID), m_position (entity or VEC3), yaw 2108984c
// (degrees), team f01b5f96 (-1 when unset); the new entity is written to m_out_Entity.
uint32_t MatchCreateEntity(Instance& inst, const Node& n) {
    MatchHost* h = Host(inst);
    if (!h) return kInvalidEntity;
    const Graph& g = inst.graph();
    EvalContext ctx{&inst};
    uint64_t def = EvaluateGuid(ctx, Cv(g, n, Hash("m_entityDef")), 0);
    if (!def) return kInvalidEntity;
    Vec3f pos;
    Primitive p;
    if (Evaluate(ctx, Cv(g, n, Hash("m_position")), p)) PositionOf(*h, p, pos);
    float yaw = EvaluateFloat(ctx, Cv(g, n, 0x2108984c), 0.0f);
    int32_t team = EvaluateInt(ctx, Cv(g, n, 0xf01b5f96), -1);
    uint32_t e = h->CreateEntity(def, pos, yaw, team, inst.sm().owner());
    if (int out = Cv(g, n, Hash("m_out_Entity")); out >= 0 && !IsInvalidEntity(e)) {
        auto path = g.DynamicPath(out);
        if (!path.empty()) inst.SetVar(path, Primitive::Entity(e));
    }
    return e;
}

// The filter test (SyncMgr +0x168 = 0x1073d80) [G]: no filter → pass; the entity must exist; the u64 masks d33fcc8f…
// 3f374297 (+0x10…+0x38) against its component 0x10 (not modelled [?]: masks other than none / 0xffffff pass); then
// m_condition (+0x40, 0xf25880) evaluated with a value provider answering 0x0A40000000000008 with the entity
// (default true); then the target-tag lists (0x1053b50) through the host.
bool MatchPassesFilter(Instance& inst, uint32_t entity, int filter_cv) { return Filter_(inst, entity, filter_cv); }

namespace {

// The mask test (0x102f3e0) [G]: `bits` = the entity's final filter bits; any-of d33fcc8f (0xffffff = any), all-of
// 1f8d859e, none-of dbcfe34a; relations to the reference entity (the sync manager's owner): 1 friendly (not hostile,
// or itself), 2 hostile, 4 itself: any-of 11661069 passes the any-of test, all-of 0b03a6d2 (a missing one fails),
// none-of 3f374297. Hostile = both have a team and they differ [?: 0xffd2b0 not read].
bool MaskTest(MatchHost& h, uint32_t entity, uint32_t ref, const uint64_t m[6]) {
    uint64_t bits = h.FilterBits(entity);
    bool any = (m[0] & bits) != 0 || m[0] == 0xffffff;
    bool all = m[1] == 0 || (bits & m[1]) == m[1];
    bool none = (m[2] & bits) != 0;
    if (!IsInvalidEntity(ref)) {
        bool self = ref == entity;
        int32_t a = h.Team(ref), b = h.Team(entity);
        bool hostile = !self && a >= 0 && b >= 0 && a != b;
        bool friendly = !hostile;
        if (((m[3] & 1) && friendly) || ((m[3] & 2) && hostile) || ((m[3] & 4) && self)) any = true;
        if (((m[4] & 1) && hostile) || ((m[4] & 2) && friendly) || ((m[4] & 4) && !self)) all = false;
        if (((m[5] & 1) && friendly) || ((m[5] & 2) && hostile) || ((m[5] & 4) && self)) none = true;
    }
    return any && all && !none;
}

bool Filter_(Instance& inst, uint32_t entity, int filter_cv) {
    if (filter_cv < 0) return true;
    SyncManager& sm = inst.sm();
    MatchHost* h = Host(inst);
    if (h && !h->Exists(entity)) return false;
    const Graph& g = inst.graph();
    if (h) {
        static constexpr uint32_t kMasks[6] = {0xd33fcc8f, 0x1f8d859e, 0xdbcfe34a, 0x11661069, 0x0b03a6d2, 0x3f374297};
        uint64_t m[6];
        bool zero = true;
        for (int k = 0; k < 6; k++) {
            auto* f = g.value(filter_cv).Find(kMasks[k]);
            m[k] = f ? f->AsU64() : 0;
            zero &= m[k] == 0;
        }
        // [G] without masks the test is skipped; the client's extra gate (the entity has component 0x7a unless
        // any-of is 0xffffff) is taken as met [?].
        if (!zero && !MaskTest(*h, entity, sm.owner(), m)) return false;
    }
    if (int cond = Cv(g, filter_cv, Hash("m_condition")); cond >= 0) {
        sm.value_providers.push_back([entity](uint64_t id, Primitive& out) {
            if (id != 0x0A40000000000008ull) return false;
            out = Primitive::Entity(entity);
            return true;
        });
        EvalContext ctx{&inst};
        bool ok = EvaluateBool(ctx, cond, true);
        sm.value_providers.pop_back();
        if (!ok) return false;
    }
    EvalContext ctx{&inst};
    auto any = Guids(ctx, Cv(g, filter_cv, 0xbc189043)), all = Guids(ctx, Cv(g, filter_cv, 0xa8dcabcd)),
         none = Guids(ctx, Cv(g, filter_cv, 0x84dd0e91));
    if (h && (!any.empty() || !all.empty() || !none.empty())) {
        auto found = h->FindByTags(any, all, none);
        return std::find(found.begin(), found.end(), entity) != found.end();
    }
    return true;
}

// ---- config vars -----------------------------------------------------------------------------------------------

class HostEvaluator : public Evaluator {
public:
    using Fn = bool (*)(const Graph&, int, EvalContext&, MatchHost&, Primitive&);
    HostEvaluator(Fn fn, bool external = true) : fn_(fn), external_(external) {}
    bool Get(const Graph& g, int cv, EvalContext& ctx, Primitive& out) const override {
        MatchHost* h = ctx.instance ? Host(*ctx.instance) : nullptr;
        return h && fn_(g, cv, ctx, *h, out);
    }
    bool DependsOnExternal(const Graph&, int, EvalContext&) const override { return external_; }

private:
    Fn fn_;
    bool external_;
};

// 8f3d1c9d (no client class): the "all players" target set [?] (docs §5.1). Iterates the host's players; as a single
// value (PlayScript's one target, SetVar's scalar path) the first player.
class PlayersEvaluator : public Evaluator {
public:
    bool Get(const Graph&, int, EvalContext& ctx, Primitive& out) const override {
        MatchHost* h = ctx.instance ? Host(*ctx.instance) : nullptr;
        if (!h) return false;
        auto p = h->Players();
        if (p.empty()) return false;
        out = Primitive::Entity(p.front());
        return true;
    }
    bool DependsOnExternal(const Graph&, int, EvalContext&) const override { return true; }
    bool IterateValues(const Graph&, int, EvalContext& ctx, std::vector<std::pair<Primitive, Primitive>>& out) const override {
        MatchHost* h = ctx.instance ? Host(*ctx.instance) : nullptr;
        if (!h) return false;
        for (uint32_t e : h->Players()) out.emplace_back(Primitive::Byte(false), Primitive::Entity(e));
        return true;
    }
};

uint32_t EntityField(const Graph& g, int cv, EvalContext& ctx, uint32_t field) {
    return EvaluateEntity(ctx, g.Object(g.value(cv), field));
}

template <bool kPool, bool kMax>
bool HealthOf(const Graph& g, int cv, EvalContext& ctx, MatchHost& h, Primitive& out) {
    uint32_t e = EntityField(g, cv, ctx, Hash("m_entity"));
    if (IsInvalidEntity(e) || !h.Exists(e)) return false;
    float v = 0.0f;
    if (!h.Health(e, kPool ? EvaluateInt(ctx, Cv(g, cv, 0x36967fde), 0) : -1, kMax, v)) v = 0.0f;
    return out = Primitive::Float(v), true;
}

uint32_t PossessorOf(MatchHost& h, uint32_t hero) {
    uint32_t possessed;
    if (!IsInvalidEntity(hero))
        for (uint32_t p : h.Players())
            if (h.PossessedEntity(p, possessed) && possessed == hero) return p;
    return kInvalidEntity;
}

// STUConfigVarArrayContains (4c31363c; client 0x1607228) [G]: pure, as the ArrayContains condition.
class ArrayContainsEvaluator : public Evaluator {
public:
    bool Get(const Graph& g, int cv, EvalContext& ctx, Primitive& out) const override {
        out = Primitive::Byte(false);
        int arr = Cv(g, cv, 0xedfb51d3), key = Cv(g, cv, Hash("m_key")), val = Cv(g, cv, Hash("m_value"));
        if (arr < 0 || (key < 0 && val < 0)) return true;
        Primitive k, v;
        if ((key >= 0 && !Evaluate(ctx, key, k)) || (val >= 0 && !Evaluate(ctx, val, v))) return true;
        for (auto& [ek, ev] : Iterate(ctx, arr))
            if ((key < 0 || LooselyEqual(ek, k)) && (val < 0 || LooselyEqual(ev, v))) {
                out = Primitive::Byte(true);
                break;
            }
        return true;
    }
    bool DependsOnExternal(const Graph& g, int cv, EvalContext& ctx) const override {
        return ss::DependsOnExternal(ctx, Cv(g, cv, 0xedfb51d3));
    }
};

// 663394d9 (client 0x161d618) [G, catalogue]: direction → angles in degrees: x = yaw = atan2(x, z) (0 when x and z
// are 0), y = pitch = asin(clamp(−y, −1, 1)), the input normalized; z = 0 [?: the client keeps the previous z].
class DirectionToAnglesEvaluator : public Evaluator {
public:
    bool Get(const Graph& g, int cv, EvalContext& ctx, Primitive& out) const override {
        Primitive d;
        if (!Evaluate(ctx, Cv(g, cv, 0x02fb5a83), d) || d.type != PrimType::Vec3) {
            out = Primitive::Vec3(0, 0, 0);
            return true;
        }
        Vec3f v = ToVec(d);
        float len = std::sqrt(v.x * v.x + v.y * v.y + v.z * v.z);
        if (len > 0 && std::fabs(len - 1.0f) > 1e-5f) v.x /= len, v.y /= len, v.z /= len;
        constexpr float kDeg = 57.29578f;
        float yaw = (v.x == 0 && v.z == 0) ? 0.0f : std::atan2(v.x, v.z) * kDeg;
        float pitch = std::asin(std::clamp(-v.y, -1.0f, 1.0f)) * kDeg;
        out = Primitive::Vec3(yaw, pitch, 0.0f);
        return true;
    }
};

}  // namespace

void RegisterMatchEvaluators(EvaluatorRegistry& r) {
    r.Register(0x8f3d1c9d, std::make_shared<PlayersEvaluator>());
    r.Register(0x4c31363c, std::make_shared<ArrayContainsEvaluator>());
    r.Register(0x663394d9, std::make_shared<DirectionToAnglesEvaluator>());
    // GetPvPEntity (d5384f53) [G: X vfunc +0x260].
    r.Register(0xd5384f53, std::make_shared<HostEvaluator>([](const Graph&, int, EvalContext&, MatchHost& h, Primitive& out) {
        uint32_t e = h.PvPEntity();
        if (IsInvalidEntity(e)) return false;
        return out = Primitive::Entity(e), true;
    }));
    // GetGlobalEntity (4f205faf) [G]: always true, invalid when there is none.
    r.Register(0x4f205faf, std::make_shared<HostEvaluator>([](const Graph&, int, EvalContext&, MatchHost& h, Primitive& out) {
        return out = Primitive::Entity(h.GlobalEntity()), true;
    }));
    // GetTeam (d1d51f19) [G]: the team of m_target (component 0x10), -1 without one; always true.
    r.Register(0xd1d51f19, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext& ctx, MatchHost& h, Primitive& out) {
        uint32_t e = EntityField(g, cv, ctx, Hash("m_target"));
        return out = Primitive::Int(IsInvalidEntity(e) ? -1 : h.Team(e)), true;
    }));
    // GetEntityExists (1b6632cd) [G].
    r.Register(0x1b6632cd, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext& ctx, MatchHost& h, Primitive& out) {
        uint32_t e = EntityField(g, cv, ctx, Hash("m_entity"));
        return out = Primitive::Byte(!IsInvalidEntity(e) && h.Exists(e)), true;
    }));
    // GetEntityPosition (b0bbdf5a) [G]: false when the entity is unknown.
    r.Register(0xb0bbdf5a, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext& ctx, MatchHost& h, Primitive& out) {
        uint32_t e = EntityField(g, cv, ctx, Hash("m_entity"));
        Vec3f p;
        if (IsInvalidEntity(e) || !h.Position(e, p)) return false;
        return out = Primitive::Vec3(p.x, p.y, p.z), true;
    }));
    // GetEntityYaw (0badb906) [G]: degrees.
    r.Register(0x0badb906, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext& ctx, MatchHost& h, Primitive& out) {
        uint32_t e = EntityField(g, cv, ctx, Hash("m_entity"));
        float y;
        if (IsInvalidEntity(e) || !h.Yaw(e, y)) return false;
        return out = Primitive::Float(y), true;
    }));
    // FindEntity (decb4aff) [G]: the first entity whose target tags match (any / all / none) and that passes m_filter;
    // invalid when none.
    r.Register(0xdecb4aff, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext& ctx, MatchHost& h, Primitive& out) {
        auto any = Guids(ctx, Cv(g, cv, 0xbc189043)), all = Guids(ctx, Cv(g, cv, 0xa8dcabcd)), none = Guids(ctx, Cv(g, cv, 0x84dd0e91));
        uint32_t found = kInvalidEntity;
        int filter = Cv(g, cv, Hash("m_filter"));
        for (uint32_t e : h.FindByTags(any, all, none))
            if (Filter_(*ctx.instance, e, filter)) {
                found = e;
                break;
            }
        return out = Primitive::Entity(found), true;
    }));
    // GetEntityPassesFilter (c6f01de9) [G].
    r.Register(0xc6f01de9, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext& ctx, MatchHost&, Primitive& out) {
        uint32_t e = EntityField(g, cv, ctx, Hash("m_entity"));
        return out = Primitive::Byte(!IsInvalidEntity(e) && Filter_(*ctx.instance, e, Cv(g, cv, Hash("m_filter")))), true;
    }));
    // IntRandom (6774a48a) [G]: uniform in [min, max]; the host's generator (the client's is local).
    r.Register(0x6774a48a, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext&, MatchHost& h, Primitive& out) {
        auto* a = g.value(cv).Find(Hash("m_min"));
        auto* b = g.value(cv).Find(Hash("m_max"));
        int32_t lo = a ? a->AsI32() : 0, hi = b ? b->AsI32() : 0;
        if (lo > hi) std::swap(lo, hi);
        return out = Primitive::Int(h.RandomInt(lo, hi)), true;
    }, false));
    // Overtime (7705e5e6 remaining, dc0a8d4e max) [G: the match-state object]: seconds.
    r.Register(0x7705e5e6, std::make_shared<HostEvaluator>([](const Graph&, int, EvalContext&, MatchHost& h, Primitive& out) {
        return out = Primitive::Float(h.OvertimeRemaining()), true;
    }));
    r.Register(0xdc0a8d4e, std::make_shared<HostEvaluator>([](const Graph&, int, EvalContext&, MatchHost& h, Primitive& out) {
        return out = Primitive::Float(h.MaxOvertime()), true;
    }));
    // The local player's entities (5dd4ed21 GetLocalPlayer, dcdf6679 GetLocalPlayerPossessedView, 8e85bff5 primary
    // possessable) [G]: a server has no local player: invalid (dcdf6679 / 5dd4ed21 are always true, 8e85bff5 false).
    for (uint32_t t : {0x5dd4ed21u, 0xdcdf6679u})
        r.RegisterConstant(t, Primitive::Entity(kInvalidEntity));
    // 4b5d2f3f (no client class) [?, strong]: the number of players on team f26a2fc7 (brain A's 0x014 gives a joining
    // player the team with fewer players: count(1) >= count(0) → team 0).
    r.Register(0x4b5d2f3f, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext& ctx, MatchHost& h, Primitive& out) {
        int32_t team = EvaluateInt(ctx, Cv(g, cv, 0xf26a2fc7), -1);
        int n = 0;
        for (uint32_t p : h.Players()) n += h.Team(p) == team;
        return out = Primitive::Int(n), true;
    }));
    // IsPointInArea (ae45e451) [?: the client reads the area's shape]: the point fb524c40 (an entity's position or a
    // VEC3) in the area of m_entity (the host's shape).
    r.Register(0xae45e451, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext& ctx, MatchHost& h, Primitive& out) {
        uint32_t area = EntityField(g, cv, ctx, Hash("m_entity"));
        Primitive p;
        Vec3f pos;
        bool in = !IsInvalidEntity(area) && Evaluate(ctx, Cv(g, cv, 0xfb524c40), p) && PositionOf(h, p, pos) &&
                  h.PointInArea(area, pos);
        return out = Primitive::Byte(in), true;
    }));
    // GetPrimaryPossessedEntity (8b31201d) [G]: the hero of the player m_entity (component 0x20 +0x98); false without.
    r.Register(0x8b31201d, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext& ctx, MatchHost& h, Primitive& out) {
        uint32_t p = EntityField(g, cv, ctx, Hash("m_entity")), hero;
        if (IsInvalidEntity(p) || !h.PossessedEntity(p, hero)) return false;
        return out = Primitive::Entity(hero), true;
    }));
    // GetThrottle (cbd4f4f2) [G]: the movement input axes of m_entity (x, y); always true.
    r.Register(0xcbd4f4f2, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext& ctx, MatchHost& h, Primitive& out) {
        uint32_t e = EntityField(g, cv, ctx, Hash("m_entity"));
        Vec3f t = IsInvalidEntity(e) ? Vec3f{} : h.Throttle(e);
        return out = Primitive::Vec3(t.x, t.y, 0.0f), true;
    }));
    // CVFilter as a value (7ead98e5) [G]: the filter test of the sync manager's owner entity.
    r.Register(0x7ead98e5, std::make_shared<HostEvaluator>([](const Graph&, int cv, EvalContext& ctx, MatchHost&, Primitive& out) {
        return out = Primitive::Byte(Filter_(*ctx.instance, ctx.instance->sm().owner(), cv)), true;
    }));
    // Health (component 0x28) [G]: GetEntityCombinedHealth 381d5387 (every pool), GetEntityHealth 6723776e / MaxHealth
    // b164e7c6 (pool 36967fde, default 0); 0 for an entity without health, false for an unknown one.
    r.Register(0x381d5387, std::make_shared<HostEvaluator>(&HealthOf<false, false>));
    r.Register(0x6723776e, std::make_shared<HostEvaluator>(&HealthOf<true, false>));
    r.Register(0xb164e7c6, std::make_shared<HostEvaluator>(&HealthOf<true, true>));
    // GetEntityHero (74bcf7ab) [G]: the selected hero's GUID (INT64), 0 without one; false for an unknown entity.
    r.Register(0x74bcf7ab, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext& ctx, MatchHost& h, Primitive& out) {
        uint32_t e = EntityField(g, cv, ctx, Hash("m_entity"));
        if (IsInvalidEntity(e) || !h.Exists(e)) return false;
        return out = Primitive::Int64(h.HeroOf(e)), true;
    }));
    // GetEntityController (d466d90f, m_controlledEntity; false without one) / GetPrimaryPossessorEntity (1eff9521,
    // m_entity; invalid without one) [G]: the player possessing a hero (PossessedEntity reversed over the players).
    r.Register(0xd466d90f, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext& ctx, MatchHost& h, Primitive& out) {
        uint32_t p = PossessorOf(h, EntityField(g, cv, ctx, Hash("m_controlledEntity")));
        if (IsInvalidEntity(p)) return false;
        return out = Primitive::Entity(p), true;
    }));
    r.Register(0x1eff9521, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext& ctx, MatchHost& h, Primitive& out) {
        return out = Primitive::Entity(PossessorOf(h, EntityField(g, cv, ctx, Hash("m_entity")))), true;
    }));
    // A server has no local player and no UI [G: these read the local player / the lobby UI]: the local player's
    // primary possessable (8e85bff5) has no value; spectator (6f53d515) and hero select (b5e9c66e) are false.
    r.Register(0x8e85bff5, std::make_shared<FunctionEvaluator>([](const Graph&, int, EvalContext&, Primitive&) { return false; }));
    r.RegisterConstant(0x6f53d515, Primitive::Byte(false));
    r.RegisterConstant(0xb5e9c66e, Primitive::Byte(false));
    // IsModeReplay (0eef80e2) [G]: not a replay.
    r.RegisterConstant(0x0eef80e2, Primitive::Byte(false));

    // ---- T060 (docs §3.15) ----
    // GetEntityVisualPosition (9ec82686; 0x10c21d0) [G]: component 0x01's world position (parent transform applied);
    // the client's rendered transform, the server's authoritative position; unknown entity → no value.
    r.Register(0x9ec82686, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext& ctx, MatchHost& h, Primitive& out) {
        uint32_t e = EntityField(g, cv, ctx, Hash("m_entity"));
        Vec3f p;
        if (IsInvalidEntity(e) || !h.Position(e, p)) return false;
        return out = Primitive::Vec3(p.x, p.y, p.z), true;
    }));
    // e4a99cc5 IsEntityInCaptureArea (0x10ca330) [G]: m_entity among the capture area e5e4595a's occupants
    // (component 0x1A +0x98); no value when either is invalid or the area unknown.
    r.Register(0xe4a99cc5, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext& ctx, MatchHost& h, Primitive& out) {
        uint32_t e = EntityField(g, cv, ctx, Hash("m_entity")), area = EntityField(g, cv, ctx, 0xe5e4595a);
        if (IsInvalidEntity(e) || IsInvalidEntity(area) || !h.Exists(area)) return false;
        auto in = h.InCaptureArea(area);
        return out = Primitive::Byte(std::find(in.begin(), in.end(), e) != in.end()), true;
    }));
    // 9521c2b9 AngleBetweenPoints2D (0xd8d830) [G]: d = A (a3fe58ce) − B (4d832c7b) (entities → positions);
    // atan2(d.y, d.x) in degrees when both |d.x| and |d.y| > 0, else 0.
    r.Register(0x9521c2b9, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext& ctx, MatchHost& h, Primitive& out) {
        Primitive pa, pb;
        Vec3f a, b;
        if (!Evaluate(ctx, Cv(g, cv, 0xa3fe58ce), pa) || !PositionOf(h, pa, a)) a = {};
        if (!Evaluate(ctx, Cv(g, cv, 0x4d832c7b), pb) || !PositionOf(h, pb, b)) b = {};
        float dx = a.x - b.x, dy = a.y - b.y;
        return out = Primitive::Float(std::fabs(dx) > 0 && std::fabs(dy) > 0 ? std::atan2(dy, dx) * 57.29578f : 0.0f), true;
    }));
    // 9161fb46 IsScopeGameEntityAdmin (0xd8e480) [G]: the node's scope EA is the game EA: always so on a server (the
    // condition of 5bea9b4b in 0x028 / 0x098).
    r.RegisterConstant(0x9161fb46, Primitive::Byte(true));
    // 3f836f1a (0x10c18a0) [G]: the entity's controller is a spectator (FilterBits 0x400000) without a team: a server
    // has no spectators.
    r.RegisterConstant(0x3f836f1a, Primitive::Byte(false));
    // Local player / screen / lobby UI [G]: false on a server (their readers are client-only or UX nodes):
    // adfc9542 in viewport, 56a2012b on screen and not occluded, adc6d1a0 lobby UI $734, 5e7fbc32 recently hit by the
    // local player (health +0x485), c29cf678 / e2c530ff highlight replay (post-match flag off / on), 9f762d9f is the
    // local player, b9af22db in the local player's party.
    for (uint32_t t : {0xadfc9542u, 0x56a2012bu, 0xadc6d1a0u, 0x5e7fbc32u, 0xc29cf678u, 0xe2c530ffu, 0x9f762d9fu, 0xb9af22dbu})
        r.RegisterConstant(t, Primitive::Byte(false));
    // No value on a server [G]: 51870208 the off-screen arrow angle (camera), 827387ee the hard-point position (model
    // pose), d07ca4ab GetStringLength (a localized tip's length; client-only ChaseVar in 0xb40).
    for (uint32_t t : {0x51870208u, 0x827387eeu, 0xd07ca4abu})
        r.Register(t, std::make_shared<FunctionEvaluator>([](const Graph&, int, EvalContext&, Primitive&) { return false; }));
    // fafb9471 HeroHasIdentifier (0x10ca180) [G]: the identifier 1506e063 is in the hero m_hero's STU list 99efbfd6
    // (asset data: SyncManager::hero_has_identifier; unset → false, as the client without the hero's STU).
    r.Register(0xfafb9471, std::make_shared<FunctionEvaluator>([](const Graph& g, int cv, EvalContext& ctx, Primitive& out) {
        uint64_t hero = EvaluateGuid(ctx, Cv(g, cv, Hash("m_hero")), 0);
        const stu::Value* ref = g.value(cv).Find(0x1506e063);
        uint64_t id = Graph::RefGuid(ref);
        if (ref && ref->type == stu::Value::Type::Inline)  // an inline struct holding the reference
            for (auto& f : ref->items)
                if (f.type == stu::Value::Type::Ref) id = f.guid;
        SyncManager& sm = ctx.instance->sm();
        return out = Primitive::Byte(hero && sm.hero_has_identifier && sm.hero_has_identifier(hero, id)), true;
    }, false));
    // 31993d91 (no client class) [?]: server-defined; the round-end reel's time t (0x12d: offset = t > 6 ? t − 6 : t).
    r.Register(0x31993d91, std::make_shared<HostEvaluator>([](const Graph& g, int cv, EvalContext& ctx, MatchHost& h, Primitive& out) {
        auto t = h.ReelTime(EntityField(g, cv, ctx, Hash("m_entity")));
        return t && (out = Primitive::Float(*t), true);
    }));
}

bool MatchCondition(State*, Instance&, const Node&, bool&) { return false; }

bool MatchAction(State*, Instance& inst, const Node& n) {
    MatchHost* h = Host(inst);
    const Graph& g = inst.graph();
    EvalContext ctx{&inst};
    switch (n.type) {
    case kBroadcastGameMessage: Broadcast(inst, n); return true;
    case kTeleportTo: Teleport(inst, n); return true;
    case kCreateEntityAction: MatchCreateEntity(inst, n); return true;
    case kSetTeamIndex:  // a257245a [?]: every entity of m_targets gets m_teamIndex (a Team config var: INT).
        if (h) {
            int32_t team = EvaluateInt(ctx, Cv(g, n, Hash("m_teamIndex")), -1);
            for (uint32_t e : Entities(ctx, Cv(g, n, Hash("m_targets")))) h->SetTeam(e, team);
        }
        return true;
    case kAddTargetTags:
    case kRemoveTargetTags:  // bd6a3dff / 78184003 [?]: entity 5fb6d29d, m_targetTags (GUIDs).
        if (h) {
            auto tags = Guids(ctx, Cv(g, n, Hash("m_targetTags")));
            for (uint32_t e : Entities(ctx, Cv(g, n, 0x5fb6d29d)))
                n.type == kAddTargetTags ? h->AddTargetTags(e, tags) : h->RemoveTargetTags(e, tags);
        }
        return true;
    case kRespawnPlayer:  // 4f14c91b [?]: the player 18394ef1.
        if (h)
            for (uint32_t e : Entities(ctx, Cv(g, n, 0x18394ef1))) h->Respawn(e);
        return true;
    case kGameFinished:  // f90c6c08 [?]: the winning team, read from 41090ce8 (a Dynamic var: brain A's $0014).
        if (h) h->GameFinished(EvaluateInt(ctx, Cv(g, n, 0x41090ce8), -1));
        return true;
    case kEndMatch:  // f1201eda [?]: no fields (the tutorial's last node).
        if (h) h->EndMatch();
        return true;
    case kDestroyEntity:  // 5c46d801 [?]: m_entity.
        if (h)
            for (uint32_t e : Entities(ctx, Cv(g, n, Hash("m_entity")))) h->DestroyEntity(e);
        return true;
    case kCaptureEnable:  // 3be5172e [?]: the capture area eefeec53 enabled := 0ab6f7cd (the mirror's m_enabled field).
        if (h) {
            uint32_t e = EvaluateEntity(ctx, Cv(g, n, 0xeefeec53));
            if (!IsInvalidEntity(e)) h->SetCaptureEnabled(e, EvaluateBool(ctx, Cv(g, n, 0x0ab6f7cd), false));
        }
        return true;
    case kCaptureOwner:  // 350e207d [?]: the capture area eefeec53 is owned by the team in b5477441.
        if (h) {
            uint32_t e = EvaluateEntity(ctx, Cv(g, n, 0xeefeec53));
            if (!IsInvalidEntity(e)) h->SetCaptureOwner(e, EvaluateInt(ctx, Cv(g, n, 0xb5477441), -1));
        }
        return true;
    case kAddMatchTime:  // 670cf926 [?]: add f13ba264 seconds, capped at 7f0ddbb5.
        if (h) {
            float s = EvaluateFloat(ctx, Cv(g, n, 0xf13ba264), 0.0f);
            float cap = EvaluateFloat(ctx, Cv(g, n, 0x7f0ddbb5), s);
            if (s > 0) h->AddMatchTime(std::min(s, cap));
        }
        return true;
    case kSpawnEnable:  // 12213f7e (spawn points 0x0d6) [?]: the spawn point 5dc4ca76 (the owner) enabled := 0ab6f7cd.
        if (h) {
            uint32_t e = EvaluateEntity(ctx, Cv(g, n, 0x5dc4ca76));
            h->SetSpawnEnabled(IsInvalidEntity(e) ? inst.sm().owner() : e, EvaluateBool(ctx, Cv(g, n, 0x0ab6f7cd), false));
        }
        return true;
    case kCriteria:  // 4e83bc01 [?]: voice / achievement bookkeeping, no graph-visible effect.
        return true;
    case kInstanceEvent:  // eaee2743 [?]: an analytics / instance event; logged for the host.
        inst.sm().RecordHostEvent(inst, n, 0);
        return true;
    case 0x9f8f5a55: {  // CreateReel [?] (docs §3.14): e5dc4915 := the host's clip id (INT, −1 without a host)
        int32_t secs = EvaluateInt(ctx, Cv(g, n, 0x981fbaec), 0);
        float offset = EvaluateFloat(ctx, Cv(g, n, 0x852c7046), 0.0f);
        int32_t id = h ? h->CreateReel(inst.sm().owner(), secs, offset) : -1;
        int cv = Cv(g, n, 0xe5dc4915);
        auto path = cv >= 0 ? g.DynamicPath(cv) : std::vector<uint64_t>{};
        if (!path.empty()) inst.SetVar(path, Primitive::Int(id));
        inst.sm().RecordHostEvent(inst, n, 0);
        return true;
    }
    default: return false;
    }
}

}  // namespace ss
