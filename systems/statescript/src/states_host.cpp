// State types whose effect lives in an engine system the server owns (movement, collision, position history, time
// scale) (T060, docs/StatescriptNodes.md §3.16): the runtime runs their graph side as the client classes do (timing,
// plugs, var claims and state values, state data) and asks MatchHost (include/statescript/match_host.h, "movement,
// collision, …") for the engine's part. Without a host each has a defined fallback (noted per class). They also log
// host events (begin / finish / abort) like every host type.
#include <algorithm>
#include <cmath>

#include "match.h"
#include "statescript/match_host.h"
#include "statescript/runtime.h"

namespace ss {

namespace {

constexpr uint32_t kHistoryBuilder = 0x029bf346, kHistoryRewinder = 0x7211cf70, kAccelerate = 0xfea1e74b,
                   kClimb = 0xc8c71bbc, kCurveMove = 0xf536a60f, kTrackSlamming = 0x76391dd3, kShockwave = 0x8d50485a,
                   kValidateWidth = 0x12aa4f0f, kExtender = 0x26cb90c0, kScaleTime = 0x7bf31629;
constexpr uint32_t kOnFinished = Hash("m_onFinishedPlug"), kOnAbort = Hash("m_onAbortPlug");

Vec3f V(const Primitive& p) {
    if (p.type != PrimType::Vec3) return {};
    return {std::bit_cast<float>(uint32_t(p.bits)), std::bit_cast<float>(uint32_t(p.bits >> 32)),
            std::bit_cast<float>(p.extra)};
}
Vec3f Add(Vec3f a, Vec3f b, float k = 1.0f) { return {a.x + b.x * k, a.y + b.y * k, a.z + b.z * k}; }
Primitive P(const Vec3f& v) { return Primitive::Vec3(v.x, v.y, v.z); }
int64_t RoundMs(float s) {
    float ms = s * 1000.0f;
    return int64_t(ms >= 0.0f ? ms + 0.5f : ms - 0.5f);
}

// The common part: fields, host, host events, plugs.
class HostSystemState : public State {
public:
    using State::State;
    void OnBegin() override { instance().sm().RecordHostEvent(instance(), node(), 1); }
    void OnFinishHook() override { instance().sm().RecordHostEvent(instance(), node(), 2); }
    void OnAbortHook() override { instance().sm().RecordHostEvent(instance(), node(), 3); }

protected:
    MatchHost* Host() const { return instance().sm().match_host.get(); }
    uint32_t Owner() const { return instance().sm().owner(); }
    int Cv(uint32_t f) const { return instance().graph().Object(*node().fields, f); }
    float F(uint32_t f, float def = 0.0f) const {
        EvalContext ctx = Ctx();
        return EvaluateFloat(ctx, Cv(f), def);
    }
    // A position operand (VEC3, or an entity's position through the host), as the client's 0xd55240.
    Vec3f Pos(uint32_t f) const {
        EvalContext ctx = Ctx();
        Primitive p;
        if (!Evaluate(ctx, Cv(f), p)) return {};
        if (p.type == PrimType::Vec3) return V(p);
        Vec3f out;
        if (p.type == PrimType::Entity && Host() && Host()->Position(uint32_t(p.bits), out)) return out;
        return {};
    }
    Vec3f OwnerPos() const {
        Vec3f p;
        if (Host()) Host()->Position(Owner(), p);
        return p;
    }
    std::vector<uint64_t> PathOf(uint32_t f) const {
        int cv = Cv(f);
        return cv >= 0 ? instance().graph().DynamicPath(cv) : std::vector<uint64_t>{};
    }
    void Write(uint32_t f, const Primitive& v, VarUser u = {}) {
        if (auto p = PathOf(f); !p.empty()) instance().SetVar(p, v, u);
    }
    VarUser Claim(uint8_t extra) const { return {instance().id(), int16_t(index()), 1, true, extra}; }
    VarUser Release() const { return {instance().id(), int16_t(index()), 0, false}; }
    int32_t Enum(uint32_t f) const {
        auto* v = node().Field(f);
        return v ? v->As<int32_t>() : 0;
    }
    uint32_t HandleKey() const { return uint32_t(uint16_t(instance().id())) | uint32_t(uint16_t(index())) << 16; }
    uint64_t MoverKey() const {
        return uint64_t(uint16_t(instance().id())) << 48 | uint64_t(uint32_t(node().index)) << 32 | uint16_t(serial());
    }
};

// HistoryBuilder (029bf346; activate 0x1089e90, tick 0x108a160) [G]: activation (not a replicated one) writes
// m_out_Handle := INTVEC4 {2, instance id, state index, 0} (null user) and starts sampling the owner's movement records
// (efa4e44f s at 3f20d7d2 samples / s, transforms when cf5c0f0c) for the Rewinder and lag compensation: the host's.
class HistoryBuilderState : public HostSystemState {
public:
    using HostSystemState::HostSystemState;
    void OnActivate(bool server_apply) override {
        if (server_apply) return;
        uint64_t v = 2 | uint64_t(uint16_t(instance().id())) << 16 | uint64_t(uint16_t(index())) << 32;
        Write(Hash("m_out_Handle"), Primitive::Make(PrimType::IntVec4, v));
        if (MatchHost* h = Host()) h->HistoryRecord(Owner(), HandleKey(), F(0xefa4e44f, 1.0f), F(0x3f20d7d2, 10.0f), Enum(0xcf5c0f0c) != 0);
    }
    void OnDeactivate(int) override {
        if (MatchHost* h = Host()) h->HistoryStop(Owner(), HandleKey());
    }
};

// HistoryRewinder (7211cf70; activate 0xd66250, frame tick 0xd667a0, deactivate 0xd66620) [G]: moves the owner back
// along the builder's history (m_handle) over 85f47ed7 s; no client hook ends it, yet 0x01a waits on its finished plug
// (the Blink refill). Designed [?]: the runtime finishes it at begin + 85f47ed7 (where the client's frame tick clamps);
// the motion is the host's (HistoryRewind at begin, HistoryRewindEnd at the end). State data: u16 in both formats
// (reader 0xd673d0: +0x5C0 = u16 / 62.5 s; the client never writes it): 0 [?].
class HistoryRewinderState : public HostSystemState {
public:
    using HostSystemState::HostSystemState;
    void OnBegin() override {
        HostSystemState::OnBegin();
        EvalContext ctx = Ctx();
        Primitive hp;
        handle_ = 0;
        if (Evaluate(ctx, Cv(Hash("m_handle")), hp) && hp.type == PrimType::IntVec4 && (hp.bits & 0xffff) == 2)
            handle_ = uint32_t(hp.bits >> 16);
        MatchHost::Rewind r{F(0xa9157cbb), F(0x85f47ed7), F(0x53222441), F(0x05aaa790), F(0xd970b2d6), Enum(0xc5a32914)};
        if (MatchHost* h = Host()) h->HistoryRewind(Owner(), handle_, r);
        ScheduleLifetime(LifetimeKind::Finish, std::max<int64_t>(0, RoundMs(r.duration)));
    }
    void OnDeactivate(int) override {
        if (MatchHost* h = Host()) h->HistoryRewindEnd(Owner(), handle_);
    }
    void TraverseFinished() override { FirePlugField(kOnFinished); }
    void TraverseAborted() override { FirePlugField(kOnAbort); }
    void WriteData(BitOut& out, bool full) const override {
        if (imported_data()) State::WriteData(out, full);
        else out.Bits(0, 16);
    }

private:
    uint32_t handle_ = 0;
};

// Mover controllers (parent f1c8d756: m_priority; activate 0x10ab1f0, deactivate 0x10ab420) [G]: a controller of the
// class's kind on the owner's CharacterMover (key {instance, node, serial}, from the next CF), ended at deactivation.
// Accelerate fea1e74b kind 6 {c5c517e5 acceleration, m_terminalVelocity}, c8c71bbc kind 12 (climb {owner position,
// 6e1a2648 target, m_climbTime}; tick 0x10aca60 finishes it when the movement record says the climb is done),
// f536a60f kind 13 {dfa07e24 weight ∈ [0, 1], bf6d8f58, 8e3d03b5 curve}. Only the climb is graph-visible (its
// finished plug); the host reports it done (MoverDone), else it finishes after m_climbTime [?].
class MoverControllerState : public HostSystemState {
public:
    MoverControllerState(Instance& inst, int si, int kind) : HostSystemState(inst, si), kind_(kind) {}
    void OnActivate(bool server_apply) override {
        if (server_apply) return;
        MatchHost::MoverController c;
        c.key = MoverKey();
        c.kind = kind_;
        c.priority = F(Hash("m_priority"));
        c.start_cf = instance().sm().cf() + 1;
        EvalContext ctx = Ctx();
        Primitive p;
        if (kind_ == 6) {
            Vec3f a = Evaluate(ctx, Cv(0xc5c517e5), p) ? V(p) : Vec3f{};
            c.p[0] = a.x, c.p[1] = a.y, c.p[2] = a.z, c.p[3] = F(Hash("m_terminalVelocity"), 9999999.0f);
        } else if (kind_ == 12) {
            Vec3f from = OwnerPos(), to = Pos(0x6e1a2648);
            c.p[0] = from.x, c.p[1] = from.y, c.p[2] = from.z, c.p[3] = to.x, c.p[4] = to.y, c.p[5] = to.z;
            c.p[6] = F(Hash("m_climbTime"));
            SetTicking(true);
        } else {
            c.p[0] = std::clamp(F(0xdfa07e24), 0.0f, 1.0f), c.p[1] = F(0xbf6d8f58);
            c.guid = EvaluateGuid(ctx, Cv(0x8e3d03b5), 0);
        }
        key_ = c.key;
        if (MatchHost* h = Host()) h->MoverAdd(Owner(), c);
        if (kind_ == 12 && !(Host() && Host()->MoverDone(Owner(), key_)))
            ScheduleLifetime(LifetimeKind::Finish, std::max<int64_t>(0, RoundMs(c.p[6])));
    }
    void OnTick(int64_t) override {
        if (kind_ != 12) return;
        if (MatchHost* h = Host(); h && h->MoverDone(Owner(), key_).value_or(false)) ScheduleLifetime(LifetimeKind::Finish);
    }
    void OnDeactivate(int reason) override {
        if (reason == 6) return;
        if (MatchHost* h = Host()) h->MoverEnd(Owner(), key_, instance().sm().cf());
    }
    void TraverseFinished() override {
        if (kind_ == 12) FirePlugField(kOnFinished);
    }

private:
    int kind_;
    uint64_t key_ = 0;
};

// TrackSlamming (76391dd3; begin 0x10ad690, tick 0x10ad7c0) [G]: begin (when the instance starts now) writes
// m_out_Slammed := false and ticks; each tick, d = the owner's forward progress over a 1.5 s mover sweep ∈ [−1, 1]
// (the host's), and asin(d) < 84cb6a46 degrees writes m_out_Slammed := true (0x07d: ends Reinhardt's charge). Without
// a host it never slams.
class TrackSlammingState : public HostSystemState {
public:
    using HostSystemState::HostSystemState;
    void OnBegin() override {
        HostSystemState::OnBegin();
        Write(Hash("m_out_Slammed"), Primitive::Byte(false));
        SetTicking(true);
    }
    void OnTick(int64_t) override {
        MatchHost* h = Host();
        auto d = h ? h->ForwardProgress(Owner(), 1.5f) : std::nullopt;
        if (!d) return;
        if (std::asin(std::clamp(*d, -1.0f, 1.0f)) < F(0x84cb6a46, 0.001f) * 3.14159265f / 180.0f)
            Write(Hash("m_out_Slammed"), Primitive::Byte(true));
    }
};

// Shockwave (8d50485a; activate 0x10910f0, tick 0x1091190, deactivate 0x1091180) [G]: a ring growing from the centre
// (m_posEntity + m_posOffset) at m_speed up to m_distance; finishes at the first pass with elapsed ≥ distance / speed
// (0x0be: 500 ms). Each scan appends the new entities the ring reaches (the host's: ShockwaveHits, arc 9d812041
// around yaw f6b861b5, the filters) to m_out_Targets (entities, deduplicated). Plugs: aborted +0x188, finished +0x190.
class ShockwaveState : public HostSystemState {
public:
    using HostSystemState::HostSystemState;
    void OnActivate(bool server_apply) override {
        radius_ = 0.0f;
        SetTicking(true);
        if (!server_apply) Scan();
    }
    void OnTick(int64_t) override {
        float dist = F(Hash("m_distance")), dur = dist / std::max(F(Hash("m_speed")), 1e-5f);
        float e = float(now() - begin_time()) * 0.001f;
        radius_ = dur <= 1e-4f ? dist : std::clamp(e / dur, 0.0f, 1.0f) * dist;
        if (e < dur) Scan();
        else ScheduleLifetime(LifetimeKind::Finish);
    }
    void OnDeactivate(int reason) override {
        if (reason == 0 || reason == 1) Scan();
    }
    void TraverseFinished() override { FirePlugField(kOnFinished); }
    void TraverseAborted() override { FirePlugField(kOnAbort); }
    float radius() const { return radius_; }

private:
    void Scan() {
        MatchHost* h = Host();
        if (!h) return;
        EvalContext ctx = Ctx();
        Vec3f centre = Add(Pos(Hash("m_posEntity")), Pos(Hash("m_posOffset")));
        auto hits = h->ShockwaveHits(Owner(), centre, F(0xf6b861b5), F(0x9d812041, 360.0f), radius_);
        auto path = PathOf(Hash("m_out_Targets"));
        if (hits.empty() || path.empty()) return;
        uint64_t id;
        VarBag* bag = instance().WriteBag(path, id);
        Var* var;
        auto* entries = bag ? bag->BeginDictionaryWrite(id, VarUser{}, &var) : nullptr;
        if (!entries) return;
        for (uint32_t e : hits) {
            bool have = std::any_of(entries->begin(), entries->end(), [&](auto& kv) {
                return kv.second.type == PrimType::Entity && uint32_t(kv.second.bits) == e;
            });
            if (!have) entries->emplace_back(Primitive::Int(int32_t(entries->size())), Primitive::Entity(e));
        }
        bag->CommitDictionary(*var);
    }
    float radius_ = 0.0f;
};

// ValidateWidth (12aa4f0f; activate 0xd6dfc0, tick 0xd6dff0, state value 0xd6e030, deactivate 0xd6dfe0) [G]: claims
// its four out vars state-defined (130ac01b extra 0, b43e4062 1, dccef747 2, f5b00a5f 3) and answers them once per CF
// (0x1099fa0): f92d850c rays per side at heights 45561961 → 92bdab20 above m_position, of length b73d0304 along
// ±m_direction; per side the smallest and the largest clear distance (a hit at fraction f = f × length). Released
// with their last values (priority 0) at the end. The casts are the host's (Cast); without one nothing is hit.
class ValidateWidthState : public HostSystemState {
public:
    using HostSystemState::HostSystemState;
    static constexpr uint32_t kOut[4] = {0x130ac01b, 0xb43e4062, 0xdccef747, 0xf5b00a5f};
    void OnActivate(bool) override {
        cf_ = -1;
        for (uint8_t i = 0; i < 4; i++) {
            Primitive v;
            StateValue(i, v);
            Write(kOut[i], v, Claim(i));
        }
        SetTicking(true);
    }
    void OnTick(int64_t) override { Recompute(); }
    void OnDeactivate(int reason) override {
        if (reason == 6) return;
        for (uint8_t i = 0; i < 4; i++) {
            Primitive v;
            StateValue(i, v);
            Write(kOut[i], v, Release());
        }
    }
    bool StateValue(uint8_t extra, Primitive& out) const override {
        if (extra > 3) return false;
        Recompute();
        out = Primitive::Float(v_[extra]);
        return true;
    }

private:
    void Recompute() const {
        int64_t cf = instance().sm().cf();
        if (cf == cf_) return;
        cf_ = cf;
        int n = int(F(0xf92d850c));
        float len = F(0xb73d0304), h0 = F(0x45561961), h1 = F(0x92bdab20);
        Vec3f p = Pos(Hash("m_position")), d = Pos(Hash("m_direction"));
        MatchHost* h = Host();
        for (int side = 0; side < 2; side++) {
            float mn = len, mx = 0.0f, k = side ? -1.0f : 1.0f;
            for (int i = 0; i < n; i++) {
                float ht = h0 + float(i) / float(n) * (h1 - h0);
                Vec3f from{p.x, p.y + ht, p.z};
                Vec3f to = Add(from, d, len * k);
                auto hit = h ? h->Cast(Owner(), from, to) : std::nullopt;
                float dist = hit ? hit->fraction * len : len;
                mn = std::min(mn, dist), mx = std::max(mx, dist);
            }
            if (side == 0) v_[0] = mn, v_[1] = std::max(mx, mn);
            else v_[2] = mn, v_[3] = mx;
        }
    }
    mutable int64_t cf_ = -1;
    mutable float v_[4] = {0, 0, 0, 0};
};

// Extender (26cb90c0; activate 0x1094d10, tick 0x10957f0, state value 0x1095e00, deactivate 0x1095420, data
// 0x1095d90 / 0x1095dc0) [G]: a probe (Doomfist-style hook) from the aim source S along the aim direction A at m_speed,
// up to 9feaa98b m (27cae126 when aim assist found a target): activation claims m_out_Pos (extra 0) and ebba30ab
// (extra 1) state-defined, writes the direction (5dbaf065, cbcb0fb0), the assist point 7edb811b and flag e865d77a;
// the values are S + s × A (s = speed × elapsed); each pass casts S → S + L × A (a hit shortens L) and aborts itself
// once s ≥ L. Deactivation releases the two positions with their last values and writes the hit (40da3d88, the point
// d41af241, the entity ec477088 / 76609896). Local data: the begin offset CF × 16 − begin (0xf4b460), remote: none.
// Aim and casts are the host's (Aim, Cast); without a host: S = the owner's position, A = +z [?], nothing is hit.
// Aim assist (6523759e cone) is not modelled [?].
class ExtenderState : public HostSystemState {
public:
    using HostSystemState::HostSystemState;
    void OnActivate(bool server_apply) override {
        SetTicking(true);
        if (server_apply) return;
        hit_ = false;
        maxlen_ = 0.0f;
        Write(0xec477088, Primitive::Entity(kInvalidEntity));
        src_ = OwnerPos(), dir_ = {0, 0, 1};
        EvalContext ctx = Ctx();
        if (MatchHost* h = Host()) h->Aim(Owner(), EvaluateGuid(ctx, Cv(Hash("m_sourceHardpoint")), 0), src_, dir_);
        Write(Hash("m_out_Pos"), P(src_), Claim(0));
        Write(0xebba30ab, P(src_), Claim(1));
        Write(0x5dbaf065, P(dir_));
        Write(0xcbcb0fb0, P(dir_));
        Write(0x7edb811b, P(src_));
        Write(0xe865d77a, Primitive::Byte(false));
        OnTick(0);
    }
    void OnTick(int64_t) override {
        float len = F(0x9feaa98b);
        if (!hit_)
            if (MatchHost* h = Host())
                if (auto c = h->Cast(Owner(), src_, Add(src_, dir_, len))) hit_ = true, hit_at_ = *c, hitlen_ = c->fraction * len;
        if (hit_) len = hitlen_;
        maxlen_ = std::max(maxlen_, len);
        if (Travel() >= len) ScheduleLifetime(LifetimeKind::Abort);
    }
    bool StateValue(uint8_t extra, Primitive& out) const override {
        if (extra > 1) return false;
        out = P(Add(src_, dir_, std::min(Travel(), maxlen_ > 0 ? maxlen_ : Travel())));
        return true;
    }
    void OnDeactivate(int reason) override {
        if (reason == 6) return;
        Primitive v;
        StateValue(0, v);
        Write(Hash("m_out_Pos"), v, Release());
        Write(0xebba30ab, v, Release());
        Write(0x40da3d88, Primitive::Byte(hit_));
        if (hit_) {
            Write(0xd41af241, P(hit_at_.point));
            Write(0xec477088, Primitive::Entity(hit_at_.entity));
            Write(0x76609896, Primitive::Entity(hit_at_.entity));
        }
    }
    void WriteData(BitOut& out, bool) const override {
        int32_t d = int32_t(int64_t(instance().sm().cf()) * 16 - begin_time());
        out.Bit(d < 0);
        out.Prefix3(uint32_t(d < 0 ? -d : d), 8, 16, 32);
    }
    void WriteChangeKey(BitOut&) const override {}  // the offset moves with the clock

private:
    float Travel() const { return F(Hash("m_speed")) * float(now() - begin_time()) * 0.001f; }
    Vec3f src_, dir_;
    bool hit_ = false;
    MatchHost::CastHit hit_at_;
    float hitlen_ = 0.0f, maxlen_ = 0.0f;
};

// ScaleTime (7bf31629; activate 0x109ba60, deactivate 0x109bcb0, data 0x109be20 / 0x109be80) [G]: a time scale
// m_timeScalar of the scope from CF + 4ad5330b (ramping in over b041c0e8 CFs), stopped at deactivation's CF +
// 4ad5330b (ramping out over 53e591a4). Data (both formats): bit active; u32 the start CF when active, else the stop
// CF. What the scale does is the host's (TimeScale). The client registers it only when the scope EA is of the
// RTTI 0x17caf70 [?: not identified]; the bookkeeping here runs regardless.
class ScaleTimeState : public HostSystemState {
public:
    using HostSystemState::HostSystemState;
    void OnActivate(bool server_apply) override {
        active_ = true;
        if (!server_apply) start_ = instance().sm().cf() + uint32_t(std::max(0, Int(0x4ad5330b)));
        stop_ = 0;
        Report();
    }
    void OnDeactivate(int reason) override {
        active_ = false;
        if (reason != 6) stop_ = instance().sm().cf() + uint32_t(std::max(0, Int(0x4ad5330b)));
        Report();
    }
    void WriteData(BitOut& out, bool) const override {
        out.Bit(active_);
        out.Bits(active_ ? start_ : stop_, 32);
    }
    void OnImport(const uint8_t* data, int nbits) override {
        if (nbits >= 33) {
            uint32_t v = 0;
            for (int i = 0; i < 32; i++) v |= uint32_t(data[(i + 1) / 8] >> ((i + 1) % 8) & 1) << i;
            (data[0] & 1 ? start_ : stop_) = v;
        }
        OnActivate(true);
    }

private:
    int32_t Int(uint32_t f) const {
        EvalContext ctx = Ctx();
        return EvaluateInt(ctx, Cv(f), 0);
    }
    void Report() {
        MatchHost* h = Host();
        if (!h) return;
        uint64_t key = uint64_t(uint16_t(instance().id())) << 16 | uint16_t(index());
        uint32_t in = uint32_t(std::max(0, Int(0xb041c0e8))), out = uint32_t(std::max(0, Int(0x53e591a4)));
        h->TimeScale(Owner(), key, F(Hash("m_timeScalar"), 1.0f), start_, start_ + in, stop_, stop_ ? stop_ + out : 0);
    }
    bool active_ = false;
    uint32_t start_ = 0, stop_ = 0;
};

}  // namespace

std::unique_ptr<State> CreateHostSystemState(Instance& inst, int state_index, uint32_t type) {
    switch (type) {
    case kHistoryBuilder: return std::make_unique<HistoryBuilderState>(inst, state_index);
    case kHistoryRewinder: return std::make_unique<HistoryRewinderState>(inst, state_index);
    case kAccelerate: return std::make_unique<MoverControllerState>(inst, state_index, 6);
    case kClimb: return std::make_unique<MoverControllerState>(inst, state_index, 12);
    case kCurveMove: return std::make_unique<MoverControllerState>(inst, state_index, 13);
    case kTrackSlamming: return std::make_unique<TrackSlammingState>(inst, state_index);
    case kShockwave: return std::make_unique<ShockwaveState>(inst, state_index);
    case kValidateWidth: return std::make_unique<ValidateWidthState>(inst, state_index);
    case kExtender: return std::make_unique<ExtenderState>(inst, state_index);
    case kScaleTime: return std::make_unique<ScaleTimeState>(inst, state_index);
    default: return nullptr;
    }
}

}  // namespace ss
