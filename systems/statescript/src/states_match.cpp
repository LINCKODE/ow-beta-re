// State types of the match / objective families (T055, docs/StatescriptNodes.md §5): voice lines, the capture point,
// target tracking, array filters, player events, entity creation, target tags. The world is the host's
// (SyncManager::match_host, include/statescript/match_host.h); without one these states run their lifecycle only.
#include <algorithm>
#include <cmath>
#include <cstring>
#include <map>

#include "match.h"
#include "statescript/match_host.h"
#include "statescript/runtime.h"

namespace ss {

namespace {

constexpr uint32_t kVoiceStimulus = 0x6e3b8ce5, kCapturePointEvents = 0x0119c536, kTrackTargets = 0x498b421a,
                   kFilterArray = 0xc5dcbfa1, kPlayerEvents = 0x97315c42, kCreateEntityState = 0xe7c6aaf6,
                   kApplyTargetTags = 0x1ab3b4d7, kReplicatedVars = 0x4ab09851, kPathMove = 0x616f509f,
                   kUpdatePayload = 0x6c2502e0, kPublishGlobals = 0xa4f3be65;

MatchHost* Host(const Instance& inst) { return inst.sm().match_host.get(); }

int Cv(const State& s, uint32_t field) { return s.instance().graph().Object(*s.node().fields, field); }
std::vector<uint64_t> PathOf(const State& s, uint32_t field) {
    int cv = Cv(s, field);
    return cv >= 0 ? s.instance().graph().DynamicPath(cv) : std::vector<uint64_t>{};
}
const OutPlug* PlugOf(const Node& n, uint32_t field) {
    for (auto& p : n.plugs)
        if (p.field == field && p.index < 0) return &p;
    return nullptr;
}
// Writes a Dictionary of entities {INT 0 key [?], ENTITY} (null user).
void WriteEntityArray(Instance& inst, std::span<const uint64_t> path, std::span<const uint32_t> ents) {
    if (path.empty()) return;
    uint64_t id;
    VarBag* bag = inst.WriteBag(path, id);
    Var* var;
    auto* e = bag ? bag->BeginDictionaryWrite(id, VarUser{}, &var) : nullptr;
    if (!e) return;
    e->clear();
    for (uint32_t x : ents) e->emplace_back(Primitive::Int(0), Primitive::Entity(x));
    bag->CommitDictionary(*var);
}
std::vector<uint64_t> Guids(EvalContext& ctx, int cv) {
    std::vector<uint64_t> out;
    for (auto& [k, v] : Iterate(ctx, cv))
        if (v.type == PrimType::Int64 && v.bits && std::find(out.begin(), out.end(), v.bits) == out.end())
            out.push_back(v.bits);
    return out;
}

// ---- VoiceStimulus (6e3b8ce5; client class vtable 0x15c81c8) --------------------------------------------------
// Client [G]: the class overrides only its finished traversal (0xd02a40: m_onFinishedPlug +0x128); nothing on the
// client finishes it. Server [?]: the line plays for the host's length (MatchHost::VoiceLineMs), else for the node's
// e341b9c4 seconds (+0x120; values 1–5.5 s that follow the line lengths in the tutorial: "Over here!" 1, "Hello,
// Soldier: 76. This is Athena." 4), then the state finishes. Without a host it stays active (host-ended, T054).
class VoiceStimulusState : public State {
public:
    using State::State;
    void OnBegin() override {
        SyncManager& sm = instance().sm();
        sm.RecordHostEvent(instance(), node(), 1);
        MatchHost* h = Host(instance());
        if (!h) return;
        EvalContext ctx = Ctx();
        uint64_t guid = EvaluateGuid(ctx, Cv(*this, Hash("m_stimulus")), 0);
        std::optional<int64_t> ms = h->VoiceLineMs(guid);
        if (!ms) {
            float s = EvaluateFloat(ctx, Cv(*this, 0xe341b9c4), 0.0f);
            ms = int64_t(std::lround(s * 1000.0f));
        }
        ScheduleTimer(now() + std::max<int64_t>(*ms, 0), 1);
    }
    void OnTimer(int32_t data) override {
        if (data == 1) ScheduleLifetime(LifetimeKind::Finish);
    }
    void OnFinishHook() override { instance().sm().RecordHostEvent(instance(), node(), 2); }
    void OnAbortHook() override { instance().sm().RecordHostEvent(instance(), node(), 3); }
    void TraverseFinished() override { FirePlugField(Hash("m_onFinishedPlug")); }
};

// ---- CapturePointEvents (0119c536; client class vtable 0x161b520) ---------------------------------------------
// Client [G]: activation (0x10bcbb0 → 0xf47f10) owns the progress var 84e42246 (+0x168) with the state-defined bit
// (user {instance, state, priority 1 | 0x80, extra 0}); its value (+0x228 = 0x10bcc50) is the float at component
// 0x1a +0xb4 (the mirrored capture area's 794d7691); deactivation (0xf48240) writes it back with priority 0. Nothing
// on the client fires its plugs or writes its other outputs: the capture system is the server's.
// Server [?] (designed, docs §5.3): per pass, the capture model of the area (the state's owner entity) from the
// host's occupants and the placement's rules; outputs be70c5df capturing team, 2410322f the owning team (read),
// 66c0c2dd occupants (entities), 87ab9dd1 "being captured", progress; plugs below.
class CapturePointEventsState : public State {
public:
    using State::State;
    // Plugs (+0x100…+0x120) and subgraph plugs (+0x128…+0x140) by their consumers in graph 0x028 [?].
    static constexpr uint32_t kStarted = 0x46562068, kOccupants = 0x18e32949, kCaptured = 0xbdcd5456,
                              kReset = 0x4c53a4e6, kStopped = 0x186bc8f8, kWhileCapturing = 0x03fc2892,
                              kWhileContested = 0x01c2fd48, kWhileDefended = 0x337c6af2, kWhileDecaying = 0x404fea29;
    static constexpr uint32_t kCapturingTeam = 0xbe70c5df, kOwnerTeam = 0x2410322f, kOccupantsVar = 0x66c0c2dd,
                              kBeingCaptured = 0x87ab9dd1, kProgress = 0x84e42246;

    void OnActivate(bool) override {
        auto p = PathOf(*this, kProgress);
        if (!p.empty())
            instance().SetVar(p, Primitive::Float(progress_), VarUser{instance().id(), int16_t(index()), 1, true, 0});
        last_ = now();
        SetTicking(true);
    }
    void OnDeactivate(int) override {
        auto p = PathOf(*this, kProgress);
        if (!p.empty()) instance().SetVar(p, Primitive::Float(progress_), VarUser{instance().id(), int16_t(index()), 0});
        SetTicking(false);
        for (uint32_t f : {kWhileCapturing, kWhileContested, kWhileDefended, kWhileDecaying}) SetWhile(f, false);
    }
    bool StateValue(uint8_t extra, Primitive& out) const override {
        if (extra) return false;
        out = Primitive::Float(progress_);
        return true;
    }
    void OnTick(int64_t) override { Step(); }
    float progress() const { return progress_; }

private:
    void Step() {
        MatchHost* h = Host(instance());
        int64_t t = now();
        float dt = float(t - last_) * 0.001f;
        last_ = t;
        if (!h) return;
        uint32_t area = instance().sm().owner();
        EvalContext ctx = Ctx();
        int32_t owner_team = EvaluateInt(ctx, Cv(*this, kOwnerTeam), -1);
        std::vector<uint32_t> occ = h->InCaptureArea(area);
        int attackers = 0, defenders = 0, attacking_team = -1;
        for (uint32_t e : occ) {
            int32_t team = h->Team(e);
            if (team < 0) continue;
            if (team == owner_team) defenders++;
            else attackers++, attacking_team = team;
        }
        if (occ != occupants_) {
            occupants_ = occ;
            WriteEntityArray(instance(), PathOf(*this, kOccupantsVar), occ);
            FirePlugField(kOccupants);
        }
        CaptureAreaRules r = h->CaptureRules(area);
        bool enabled = h->CaptureEnabled(area);
        bool contested = attackers > 0 && defenders > 0;
        bool capturing = enabled && attackers > 0 && !contested && progress_ < 1.0f;
        float before = progress_;
        if (capturing) {
            int n = std::min(attackers, std::max(1, r.max_players));
            float rate = (1.0f + float(n - 1) * r.extra_player) / std::max(0.001f, r.capture_time);
            progress_ = std::min(1.0f, progress_ + rate * dt);
            idle_ = 0;
        } else if (enabled && attackers == 0 && progress_ > 0.0f && progress_ < 1.0f) {
            idle_ += dt;
            float seg = r.segments > 0 ? 1.0f / r.segments : 0.0f;
            float floor = seg > 0 ? std::floor(progress_ / seg + 1e-4f) * seg : 0.0f;
            if (idle_ >= r.decay_delay && progress_ > floor) {
                progress_ = std::max(floor, progress_ - dt / std::max(0.001f, r.decay_time));
                decaying_ = progress_ > floor;
            } else {
                decaying_ = false;
            }
        } else {
            decaying_ = false;
        }
        if (capturing && !capturing_) {
            WriteInt(kCapturingTeam, attacking_team);
            WriteBool(kBeingCaptured, true);
            FirePlugField(kStarted);
        } else if (!capturing && capturing_ && progress_ < 1.0f) {
            WriteBool(kBeingCaptured, false);
            FirePlugField(kStopped);
        }
        capturing_ = capturing;
        SetWhile(kWhileCapturing, capturing);
        SetWhile(kWhileContested, contested && enabled);
        SetWhile(kWhileDefended, enabled && defenders > 0 && attackers == 0);
        SetWhile(kWhileDecaying, decaying_);
        if (progress_ != before) {
            h->CaptureProgress(area, progress_, capturing ? attacking_team : -1, contested);
            // The var's subscribers see the state-defined value change.
            auto p = PathOf(*this, kProgress);
            if (!p.empty())
                instance().SetVar(p, Primitive::Float(progress_), VarUser{instance().id(), int16_t(index()), 1, true, 0});
            if (progress_ >= 1.0f && before < 1.0f) {
                WriteBool(kBeingCaptured, false);
                FirePlugField(kCaptured);
            } else if (progress_ <= 0.0f && before > 0.0f) {
                FirePlugField(kReset);
            }
        }
    }
    void WriteInt(uint32_t f, int32_t v) {
        auto p = PathOf(*this, f);
        if (!p.empty()) instance().SetVar(p, Primitive::Int(v));
    }
    void WriteBool(uint32_t f, bool v) {
        auto p = PathOf(*this, f);
        if (!p.empty()) instance().SetVar(p, Primitive::Byte(v));
    }
    // A subgraph plug that is active while a condition holds: begin its states when it starts, abort them when it ends.
    void SetWhile(uint32_t f, bool on) {
        bool& cur = while_[f];
        if (cur == on) return;
        cur = on;
        const OutPlug* p = PlugOf(node(), f);
        if (!p) return;
        if (on) FirePlug(p);
        else AbortStates(p->subgraph);
    }
    float progress_ = 0.0f, idle_ = 0.0f;
    bool capturing_ = false, decaying_ = false;
    int64_t last_ = 0;
    std::vector<uint32_t> occupants_;
    std::map<uint32_t, bool> while_;
};

// ---- TrackTargets (498b421a; client class vtable 0x15d4d50, 704 bytes) -------------------------------------------
// Client [G]: tick 0x108ea60 updates when due (0x1090c90: the update interval by 0x1a4 enum 8004b1bd: 1 → 3, 2 → 6,
// 3 → 12, 4 → 30 command frames, phase-shifted by the node's m_nodes index; else every pass), else 0x108ed70 (the
// world query: m_posEntity (+0x120) + m_posOffset, m_radius, the cone 9d812041, m_filter, the id lists at +0x178 /
// +0x180, …). State data (writer 0x108eaf0, reader 0x108ec30): bit has; small(n) (0x0ee4c90); per target u32 entity,
// its age in ms (sm time − begin − the target's entry time, ≥ 0) as 0 + 8 | 1 0 + 16 | 1 1 + 32.
// Server [?] (designed): candidates = the host's players; kept when within m_radius of m_posEntity (+ m_posOffset)
// and passing m_filter (the cone, line of sight and the id lists are not modelled); m_out_Targets = the tracked
// entities (Dictionary). Without a host nothing is tracked (a single 0 data bit, as T054 wrote).
class TrackTargetsState : public State {
public:
    using State::State;
    void OnActivate(bool) override {
        targets_.clear();
        SetTicking(true);
    }
    void OnDeactivate(int) override { SetTicking(false); }
    void OnTick(int64_t) override {
        MatchHost* h = Host(instance());
        if (!h) return;
        int period = PeriodCf();
        uint32_t cf = instance().sm().cf();
        if (period > 1 && last_cf_ != UINT32_MAX && cf < last_cf_ + uint32_t(period) &&
            (cf + uint32_t(node().index)) % uint32_t(period) != 0)
            return;
        if (cf == last_cf_) return;
        last_cf_ = cf;
        Update(*h);
    }
    void WriteData(BitOut& out, bool) const override {
        if (targets_.empty()) {
            out.Bit(false);
            return;
        }
        out.Bit(true);
        out.Prefix3(uint32_t(targets_.size()), 4, 8, 16);  // small n [?: 0xee4c90 widths as the instance-id small]
        for (auto& [e, since] : targets_) {
            out.Bits(e, 32);
            uint32_t age = uint32_t(std::max<int64_t>(0, now() - since));
            if (age < 0x100) out.Bit(false), out.Bits(age, 8);
            else if (age < 0x10000) out.Bit(true), out.Bit(false), out.Bits(age, 16);
            else out.Bit(true), out.Bit(true), out.Bits(age, 32);
        }
    }

private:
    int PeriodCf() const {
        auto* f = node().Field(0x8004b1bd);
        switch (f ? f->AsU32() : 0) {
        case 1: return 3;
        case 2: return 6;
        case 3: return 12;
        case 4: return 30;
        default: return 1;
        }
    }
    void Update(MatchHost& h) {
        EvalContext ctx = Ctx();
        uint32_t center_e = EvaluateEntity(ctx, Cv(*this, Hash("m_posEntity")));
        Vec3f c;
        if (IsInvalidEntity(center_e) || !h.Position(center_e, c)) return;
        Primitive off;
        if (Evaluate(ctx, Cv(*this, Hash("m_posOffset")), off) && off.type == PrimType::Vec3) {
            float xyz[3];
            uint32_t raw[3] = {uint32_t(off.bits), uint32_t(off.bits >> 32), off.extra};
            for (int i = 0; i < 3; i++) std::memcpy(&xyz[i], &raw[i], 4);
            c.x += xyz[0], c.y += xyz[1], c.z += xyz[2];
        }
        float radius = EvaluateFloat(ctx, Cv(*this, Hash("m_radius")), 0.0f);
        std::vector<std::pair<uint32_t, int64_t>> next;
        for (uint32_t e : h.Players()) {
            Vec3f p;
            if (e == center_e || !h.Position(e, p)) continue;
            float dx = p.x - c.x, dy = p.y - c.y, dz = p.z - c.z;
            if (dx * dx + dy * dy + dz * dz > radius * radius) continue;
            if (!MatchPassesFilter(instance(), e, Cv(*this, Hash("m_filter")))) continue;
            int64_t since = now();
            for (auto& [o, s] : targets_)
                if (o == e) since = s;
            next.emplace_back(e, since);
        }
        bool changed = next.size() != targets_.size();
        for (size_t i = 0; !changed && i < next.size(); i++) changed = next[i].first != targets_[i].first;
        targets_ = std::move(next);
        if (!changed) return;
        NoteDataChange();
        std::vector<uint32_t> ents;
        for (auto& [e, s] : targets_) ents.push_back(e);
        WriteEntityArray(instance(), PathOf(*this, Hash("m_out_Targets")), ents);
    }
    std::vector<std::pair<uint32_t, int64_t>> targets_;
    uint32_t last_cf_ = UINT32_MAX;
};

// ---- FilterArray (c5dcbfa1; client class vtable 0x1605e68) --------------------------------------------------------
// Client [G]: activation (0xf38ff0) and every change of a subscribed var other than its own out var (0xf39040) run
// the filter (0xf391a0): needs edfb51d3 (+0x108, the array) and m_out_Array (+0x110); the valid ENTITY values of the
// array that pass m_filter (+0x100; SyncMgr +0x168 = 0x1073d80) in order; when they differ from the out Dictionary's
// values (count, or an entry by ==) the out var is rewritten (begin write, resize, each entry's value := the entity,
// commit) and a Timer (data 1) is queued at now at the head; Timer 1 (0xf39140) fires a415be52 (+0x118).
class FilterArrayState : public State {
public:
    using State::State;
    void OnActivate(bool) override { Run(); }
    void VarChanged(Var* v) override {
        if (v) {
            auto out = PathOf(*this, Hash("m_out_Array"));
            int16_t owner = 0;
            if (!out.empty() && instance().FindVar(out, false, &owner) == v) return;
        }
        Run();
    }
    void OnTimer(int32_t data) override {
        if (data == 1) FirePlugField(0xa415be52);
    }

private:
    void Run() {
        int arr = Cv(*this, 0xedfb51d3);
        auto out = PathOf(*this, Hash("m_out_Array"));
        if (arr < 0 || out.empty()) return;
        EvalContext ctx = Ctx();
        std::vector<uint32_t> ents;
        for (auto& [k, v] : Iterate(ctx, arr))
            if (v.type == PrimType::Entity && !IsInvalidEntity(v.AsU32()) &&
                MatchPassesFilter(instance(), v.AsU32(), Cv(*this, Hash("m_filter"))))
                ents.push_back(v.AsU32());
        if (const Var* cur = instance().FindDictionary(out); cur && cur->dict_size() == ents.size()) {
            bool same = true;
            for (size_t i = 0; same && i < ents.size(); i++)
                same = LooselyEqual(cur->dict()[i].second, Primitive::Entity(ents[i]));
            if (same) return;
        }
        uint64_t id;
        VarBag* bag = instance().WriteBag(out, id);
        Var* var;
        auto* e = bag ? bag->BeginDictionaryWrite(id, VarUser{}, &var) : nullptr;
        if (!e) return;
        e->resize(ents.size());
        for (size_t i = 0; i < ents.size(); i++) (*e)[i].second = Primitive::Entity(ents[i]);
        bag->CommitDictionary(*var);
        ScheduleTimer(now(), 1, true);
    }
};

// ---- player events (97315c42, no client class) -------------------------------------------------------------------
// Server-only [?] (designed from brains A 0x00f and B 0x029 / Dorado 0x10f): the game's player events. Fields:
// plugs d400f59a joined (+0x100), 91b4d548 left (+0x108), 638541a5 died (+0x110); outputs 4333383d the joining
// player ($000e, which 0x00f forwards as 0x…02 {0x…02 player}), e38db4a1 the leaving player ($002e), 1eb7273b ($002f,
// not read by any graph), 6da858cf the victim ($0012) and 767c4956 the killer ($0010) (forwarded as 0x…03 {0x…03
// victim, 0x…04 killer}, which brain A's 0x014 counts for the team deathmatch score). The host reports the events
// with MatchPlayerEvent (match_host.h).
class PlayerEventsState : public State {
public:
    using State::State;
    void Fire(PlayerEvent ev, uint32_t player, uint32_t other) {
        auto write = [&](uint32_t f, uint32_t e) {
            auto p = PathOf(*this, f);
            if (!p.empty()) instance().SetVar(p, Primitive::Entity(e));
        };
        switch (ev) {
        case PlayerEvent::Joined: write(0x4333383d, player), FirePlugField(0xd400f59a); break;
        case PlayerEvent::Left: write(0xe38db4a1, player), FirePlugField(0x91b4d548); break;
        case PlayerEvent::Died: write(0x6da858cf, player), write(0x767c4956, other), FirePlugField(0x638541a5); break;
        }
    }
};

// ---- CreateEntity state (e7c6aaf6; client class vtable 0x15d9df0) ------------------------------------------------
// Client [G: class hooks]: only class-specific slots (+0x258…+0x288): its effect is the server's. Server [?]: begin
// asks the host for an entity of m_entityDef at m_position (an entity's position or a VEC3), yaw 2108984c (degrees),
// team f01b5f96, and writes it to m_out_Entity; the entity lives as long as the state (end → destroyed [?]).
class CreateEntityState : public State {
public:
    using State::State;
    void OnBegin() override {
        instance().sm().RecordHostEvent(instance(), node(), 1);
        MatchHost* h = Host(instance());
        if (!h) return;
        created_ = MatchCreateEntity(instance(), node());
    }
    void OnDeactivate(int) override {
        if (MatchHost* h = Host(instance()); h && !IsInvalidEntity(created_)) h->DestroyEntity(created_);
        created_ = kInvalidEntity;
    }

private:
    uint32_t created_ = kInvalidEntity;
};

// ---- ApplyTargetTags (1ab3b4d7, no client class) -----------------------------------------------------------------
// Server-only [?]: while active, the entity 5fb6d29d carries m_targetTags (added on begin, removed on end).
class ApplyTargetTagsState : public State {
public:
    using State::State;
    void OnBegin() override {
        MatchHost* h = Host(instance());
        if (!h) return;
        EvalContext ctx = Ctx();
        entity_ = EvaluateEntity(ctx, Cv(*this, 0x5fb6d29d));
        tags_ = Guids(ctx, Cv(*this, Hash("m_targetTags")));
        if (!IsInvalidEntity(entity_)) h->AddTargetTags(entity_, tags_);
    }
    void OnDeactivate(int) override {
        if (MatchHost* h = Host(instance()); h && !IsInvalidEntity(entity_)) h->RemoveTargetTags(entity_, tags_);
        entity_ = kInvalidEntity;
    }

private:
    uint32_t entity_ = kInvalidEntity;
    std::vector<uint64_t> tags_;
};

// ---- 4ab09851 (no client class): a list of the graph's vars (5cd2d4aa, CVDynamic[]) -----------------------------
// Every brain / objective graph has one, begun from its entries, remotely synced, naming (mostly) its synced vars.
// Designed [?]: replication bookkeeping on the server (which vars the sync sends); no graph-visible effect.
class ReplicatedVarsState : public State {
public:
    using State::State;
};

// ---- the payload's path mover (616f509f; client class vtable 0x1618bc0, 184 bytes) ------------------------------
// Client [G: class hooks]: only its class-specific slots (+0x270…+0x288): the motion is the server's. Server [?]
// (designed from graph 0x098, MatchLogic.md §12.4): while active, every pass the host moves the cart at m_speed (re-
// evaluated: 0x098's push speed depends on the attacker count; −0.3 = the rollback) along its path; the progress
// (0…1) is written to 7a2added ($01f3, which 0x098 shows as the HUD progress and watches for 0.85); a waypoint passed
// is written to 7fe17913 ($02af) and 1a985a27 fires (→ SendGameMessage 0x…70 to it: the waypoint's graph 0x0d2); the
// path's end (forward) or start (back) finishes the state. m_destinationRadius, 6cbdabbb, ac9d3ea1 not modelled.
class PathMoveState : public State {
public:
    using State::State;
    void OnActivate(bool) override {
        last_ = now();
        SetTicking(true);
    }
    void OnDeactivate(int) override { SetTicking(false); }
    void OnTick(int64_t) override {
        MatchHost* h = Host(instance());
        int64_t t = now();
        float dt = float(t - last_) * 0.001f;
        last_ = t;
        if (!h || dt <= 0.0f) return;
        EvalContext ctx = Ctx();
        float speed = EvaluateFloat(ctx, Cv(*this, Hash("m_speed")), 0.0f);
        MatchHost::PayloadStep st;
        if (!h->PayloadAdvance(instance().sm().owner(), speed, dt, st)) return;
        if (auto p = PathOf(*this, 0x7a2added); !p.empty()) instance().SetVar(p, Primitive::Float(st.progress));
        if (!IsInvalidEntity(st.waypoint)) {
            if (auto p = PathOf(*this, 0x7fe17913); !p.empty()) instance().SetVar(p, Primitive::Entity(st.waypoint));
            FirePlugField(0x1a985a27);
        }
        if (st.at_end) ScheduleLifetime(LifetimeKind::Finish);
    }
    void TraverseFinished() override { FirePlugField(Hash("m_onFinishedPlug")); }
    void TraverseAborted() override { FirePlugField(Hash("m_onAbortPlug")); }

private:
    int64_t last_ = 0;
};

// ---- UpdatePayload (6c2502e0, no client class) ------------------------------------------------------------------
// Server-only [?]: fields a8f9dafa ($1a9f: the attackers near the cart, TrackTargets 0 with the team-1 filter),
// 995cb516 ($1aa0: the defenders), a0e922d6 ($0270: moving), bab53ff9 ($03b9), ce6de719 (5.0). None is written by it
// in the graph's logic (the others are the graph's own vars), so it is taken as the report of the cart's state to the
// payload component 0x19 (replicated: contest / pushing for the HUD): the host gets it every pass.
class UpdatePayloadState : public State {
public:
    using State::State;
    void OnActivate(bool) override { SetTicking(true); }
    void OnDeactivate(int) override { SetTicking(false); }
    void OnTick(int64_t) override {
        MatchHost* h = Host(instance());
        if (!h) return;
        EvalContext ctx = Ctx();
        auto count = [&](uint32_t f) {
            const Var* v = instance().FindDictionary(PathOf(*this, f));
            return v ? int(v->dict_size()) : 0;
        };
        int a = count(0xa8f9dafa), d = count(0x995cb516);
        bool moving = EvaluateBool(ctx, Cv(*this, 0xa0e922d6), false);
        if (a != a_ || d != d_ || moving != moving_) h->PayloadStatus(instance().sm().owner(), a, d, moving);
        a_ = a, d_ = d, moving_ = moving;
    }

private:
    int a_ = -1, d_ = -1;
    bool moving_ = false;
};

// ---- PublishGlobals (a4f3be65; class vtable 0x15c97c0, 208 B; docs/StatescriptNodes.md §3.14) --------------------
// Client [G]: activation (0xd02f60) registers every m_entries item (STUPublishEntry {m_key 0x0D8 id, m_value CV}) with
// the game EA's global-var registry (component 0x51, vfunc +0x30(key, owner entity, {instance id, state index})) and
// keeps the list at +0xA8; deactivation (0xd03140) unregisters each key (+0x40). Reading an id (Get 0xceb2b0) finds
// the publisher's entry and evaluates its m_value there. Brain A (0x00f node 27) publishes 0x…195 := owner $0195,
// the match phase; no Statescript node reads the registry, native code reads 0x…198 / 0x…984 (0xc7b7b7, 0xd03d25).
class PublishGlobalsState : public State {
public:
    using State::State;
    void OnActivate(bool) override {
        MatchHost* h = Host(instance());
        keys_.clear();
        auto* list = node().Field(Hash("m_entries"));
        if (!list || list->type != stu::Value::Type::List) return;
        for (auto& e : list->items) {
            uint64_t key = Graph::RefGuid(e.Find(Hash("m_key")));
            if (!key) continue;
            keys_.push_back(key);
            if (h) h->PublishGlobal(key, {instance().sm().owner(), instance().id(), index()});
        }
    }
    void OnDeactivate(int) override {
        if (MatchHost* h = Host(instance()))
            for (uint64_t k : keys_) h->UnpublishGlobal(k);
        keys_.clear();
    }

private:
    std::vector<uint64_t> keys_;
};

}  // namespace

std::unique_ptr<State> CreateMatchState(Instance& inst, int state_index, uint32_t type) {
    switch (type) {
    case kVoiceStimulus: return std::make_unique<VoiceStimulusState>(inst, state_index);
    case kCapturePointEvents: return std::make_unique<CapturePointEventsState>(inst, state_index);
    case kTrackTargets: return std::make_unique<TrackTargetsState>(inst, state_index);
    case kFilterArray: return std::make_unique<FilterArrayState>(inst, state_index);
    case kPlayerEvents: return std::make_unique<PlayerEventsState>(inst, state_index);
    case kCreateEntityState: return std::make_unique<CreateEntityState>(inst, state_index);
    case kApplyTargetTags: return std::make_unique<ApplyTargetTagsState>(inst, state_index);
    case kReplicatedVars: return std::make_unique<ReplicatedVarsState>(inst, state_index);
    case kPathMove: return std::make_unique<PathMoveState>(inst, state_index);
    case kUpdatePayload: return std::make_unique<UpdatePayloadState>(inst, state_index);
    case kPublishGlobals: return std::make_unique<PublishGlobalsState>(inst, state_index);
    default: return nullptr;
    }
}

void MatchPlayerEvent(SyncManager& sm, PlayerEvent ev, uint32_t player, uint32_t other) {
    for (auto& inst : sm.instances()) {
        if (inst->stopped()) continue;
        const Graph& g = inst->graph();
        for (size_t i = 0; i < g.states().size(); i++)
            if (State* s = inst->state(int(i)); s && s->active() && g.state_node(int(i)).type == kPlayerEvents)
                static_cast<PlayerEventsState*>(s)->Fire(ev, player, other);
    }
}

float MatchCaptureProgress(const SyncManager& sm) {
    for (auto& inst : sm.instances()) {
        const Graph& g = inst->graph();
        for (size_t i = 0; i < g.states().size(); i++)
            if (State* s = inst->state(int(i)); s && s->active() && g.state_node(int(i)).type == kCapturePointEvents)
                return static_cast<CapturePointEventsState*>(s)->progress();
    }
    return -1.0f;
}

bool MatchGlobalVar(SyncManager& sm, uint64_t key, Primitive& out) {
    MatchHost* h = sm.match_host.get();
    const MatchHost::GlobalRef* ref = h ? h->FindGlobal(key) : nullptr;
    if (!ref) return false;
    SyncManager* owner = ref->entity == sm.owner() ? &sm : sm.entity_resolver ? sm.entity_resolver(ref->entity) : nullptr;
    Instance* inst = owner ? owner->FindInstance(ref->instance) : nullptr;
    State* st = inst ? inst->state(ref->state) : nullptr;
    if (!st || !st->active()) return false;
    auto* list = st->node().Field(Hash("m_entries"));
    if (!list || list->type != stu::Value::Type::List) return false;
    for (auto& e : list->items)
        if (Graph::RefGuid(e.Find(Hash("m_key"))) == key) {
            int cv = Graph::ObjectOf(e.Find(Hash("m_value")));
            EvalContext ctx{inst};
            return cv >= 0 && Evaluate(ctx, cv, out);
        }
    return false;
}

}  // namespace ss
