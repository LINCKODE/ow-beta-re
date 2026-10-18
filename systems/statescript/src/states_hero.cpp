// Hero state types (T041): the shared (server + client) logic of the states a hero's ability path runs.
// Specs: docs/StatescriptRuntime.md §5.9 (Ghidra reading of the prio-0/1 classes, checked against
// fixtures/hero_genji_fire.fixture).
#include <algorithm>
#include <bit>
#include <cmath>
#include <optional>

#include "statescript/runtime.h"
#include "states_hero.h"

namespace ss {

namespace {

constexpr uint32_t kAnim = 0x7fb4bf64, kChaseVar = 0x970ec198, kWeaponVolley = 0x860ee5fc;
constexpr uint32_t kCvInt = 0x3ae7427e, kCvFloat = 0x7b13a0d4;
constexpr uint32_t kStack = 0xf7d6498c, kAbility = 0xf53c5da8, kBlinkEffect = 0x429991a7;
// The "trigger" input plug class (type info RVA 0x17665f0) that states take through vfunc +0x240 [G].
constexpr uint32_t kTriggerInputPlug = 0x8dcb62ea;

// Local-format state data, LSB-first (the full update's bit stream).
struct BitIn {
    const uint8_t* d;
    int n, pos = 0;
    bool ok() const { return pos <= n; }
    uint32_t Bits(int k) {
        uint32_t v = 0;
        for (int i = 0; i < k; i++, pos++)
            if (pos < n && d && (d[pos / 8] >> (pos % 8) & 1)) v |= 1u << i;
        return v;
    }
    bool Bit() { return Bits(1) != 0; }
    // 0 + w0 | 1 0 + w1 | 1 1 + w2 (FUN_7ff651050590 family).
    uint32_t Prefix3(int w0, int w1, int w2) {
        if (!Bit()) return Bits(w0);
        if (!Bit()) return Bits(w1);
        return Bits(w2);
    }
};

int IndexBits(const Graph& g) {
    uint32_t n = uint32_t(g.states().size());
    return n > 1 ? int(std::bit_width(n - 1)) : 1;
}

// Stack predecessor (0xf4b360, local): bit present; instance id (2-bit size prefix 4 / 8 / 16); state index.
bool ReadPredecessor(BitIn& in, const Graph& g, uint32_t& key) {
    if (!in.Bit()) return false;
    uint32_t inst = in.Prefix3(4, 8, 16);
    uint32_t st = in.Bits(IndexBits(g));
    key = uint16_t(inst) | (uint32_t(uint16_t(st)) << 16);
    return true;
}

int64_t RoundMs(float seconds) {  // (int)(x * 1000 ± 0.5), half away from zero
    float ms = seconds * 1000.0f;
    return int64_t(ms >= 0.0f ? ms + 0.5f : ms - 0.5f);
}

// STUStatescriptStateAnim (shared class 0x15e54d0) [G]: the only thing ending an Anim by itself is the
// Lifetime finish queued by its activation hook (+0x160 = 0x10a4930) at begin + round(duration × 1000),
// unless looping (6d640a54) or hold (a7fbdeef). Duration = 4d475080 (s); 0 → the animation's length ×
// m_speedScalar from the owner's animation component (asset data, not modelled: finishes at begin) [G].
// Playing the animation is client presentation.
class AnimState : public State {
public:
    using State::State;
    static constexpr uint32_t kLooping = 0x6d640a54, kDuration = 0x4d475080, kHold = 0xa7fbdeef,
                              kOnFinished = Hash("m_onFinishedPlug");

    void OnActivate(bool server_apply) override {
        if (server_apply) return;  // [?] a replicated Anim is ended by the server's updates (0xf4a9c0 gate)
        const Graph& g = instance().graph();
        EvalContext ctx = Ctx();
        bool hold = EvaluateBool(ctx, g.Object(*node().fields, kHold), false);
        if (EvaluateBool(ctx, g.Object(*node().fields, kLooping), false)) return;
        float duration = EvaluateFloat(ctx, g.Object(*node().fields, kDuration), 0.0f);
        if (duration <= 1.1920929e-4f) instance().sm().warnings.push_back("Anim with duration 0: animation length not modelled");
        int64_t remaining = RoundMs(duration) - std::max<int64_t>(0, now() - begin_time());
        if (hold) return;
        ScheduleLifetime(LifetimeKind::Finish, std::max<int64_t>(0, remaining));
    }
    void TraverseFinished() override { FirePlugField(kOnFinished); }
    // The reader (0x10a4c80) overwrites the state serial (+0x44) with the u3 it receives: the client counts on
    // from the server's value, so an import does the same (T053 shadow compare: client 0 vs server 3 before) [G].
    void OnImport(const uint8_t* data, int nbits) override {
        if (nbits >= 3) SetSerial(int16_t(data[0] & 7));
        // The time the data carries (bits: u3 serial, has-anim, bit b, [b: 0xee4f80 time]) = since the play start.
        auto bit = [&](int i) { return i < nbits && (data[i / 8] >> (i % 8) & 1) != 0; };
        int64_t t = 0;
        if (bit(3) && bit(4)) {
            int pos = 5, w = 8;
            if (bit(pos++)) w = bit(pos++) ? 32 : 16;
            for (int i = 0; i < w; i++) t |= int64_t(bit(pos++)) << i;
        }
        play_start_ = now() - t;
        OnActivate(true);
    }
    // The client starts the animation in the state's +0x128 hook (0xd99c60 → +0x258 = 0x10a4f20), which runs from
    // the post-run list at the end of the command-frame window it began in, not at the activation (instance +0x188
    // = 1 there): +0xE0 = that delay, and the data's time counts from the play start [G] (T053: Anim s54 / s55,
    // begun at ms 12 of their CF, were 12 ms "late" against the client).
    void OnBegin() override {
        play_start_ = begin_time() / 16 * 16 + 15;
        WriteHandle();
    }
    // The play hook (0x10a4f20, x258) writes the out var 5c5addbe (+0x138) := INTVEC4 {3, instance id, state index,
    // 0}, null user, with or without an animation component: the handle RestartAnim (f8ec9531) reads (0x176 $793,
    // 0x2f3 $168f) [G, T060]. The client plays at the end of the begin's CF window; written at the begin here [?].
    void WriteHandle() {
        int cv = instance().graph().Object(*node().fields, 0x5c5addbe);
        auto path = cv >= 0 ? instance().graph().DynamicPath(cv) : std::vector<uint64_t>{};
        if (path.empty()) return;
        uint64_t v = 3 | uint64_t(uint16_t(instance().id())) << 16 | uint64_t(uint16_t(index())) << 32;
        instance().SetVar(path, Primitive::Make(PrimType::IntVec4, v));
    }
    // State data (0x10a4bd0) [G]: u3 serial & 7, bit hasAnim, [hasAnim: bit b, [b: animTimeMs as 0xee4f80 =
    // 0 + 8 | 1 0 + 16 | 1 1 + 32]] with the animation time since the (server-side, unplayed) start = now − begin [?].
    void WriteData(BitOut& out, bool) const override {
        out.Bits(uint32_t(serial()) & 7, 3);
        EvalContext ctx = Ctx();
        bool has_anim = EvaluateGuid(ctx, instance().graph().Object(*node().fields, 0xf9045e7a), 0) != 0;
        out.Bit(has_anim);
        if (!has_anim) return;
        int64_t t = now() - play_start_;
        out.Bit(t > 0);
        if (t > 0) out.Prefix3(uint32_t(t), 8, 16, 32);
    }
    void WriteChangeKey(BitOut& out) const override {  // the time since begin moves with time
        BitOut all;
        WriteData(all, false);
        for (int i = 0; i < 4 && i < all.nbits; i++) out.Bit(all.bytes[0] >> i & 1);
    }

private:
    int64_t play_start_ = 0;
};

// STUStatescriptStateChaseVar (p0 class 0x15c9a18; p1/p2 add world transforms to ResolveTarget) [G].
// Moves m_out_Var from a start value towards m_destination at 41430518 units/s (default 1) or over 924cebfa
// seconds. The state owns the var while active (user priority 1, state-defined); with a constant scalar
// destination and rate the value is computed on read and one Timer (data 0) ends the chase, otherwise it moves
// every pass. On finish / abort the current value is written back as a plain value (bake).
class ChaseVarState : public State {
public:
    using State::State;
    static constexpr uint32_t kDest = Hash("m_destination"), kDestBone = Hash("m_destinationBone"),
                              kDestObject = Hash("m_destinationObject"), kStartBone = 0x3746cf3c,
                              kStartObject = 0xeb254e1e, kStart = 0xfc7bc05c, kOutVar = Hash("m_out_Var"),
                              kRate = 0x41430518, kDurationF = 0x924cebfa, kOnAbort = Hash("m_onAbortPlug"),
                              kOnFinished = Hash("m_onFinishedPlug"), kKeepFollowing = 0x9f6b675b, kRestart = 0xb36f935f;

    int Cv(uint32_t f) const { return instance().graph().Object(*node().fields, f); }
    bool Flag(uint32_t f) const {
        auto* v = node().Field(f);
        return v && v->AsU8();
    }
    std::vector<uint64_t> OutPath() const { return instance().graph().DynamicPath(Cv(kOutVar)); }

    struct V4 {
        float x = 0, y = 0, z = 0, w = 0;
    };
    // ResolveTarget (0xf54f90) [G]: a VEC3 value; else a scalar when no bone / object; else (0,0,0) as a vector
    // (p1/p2 would transform by the entity / bone: world data, not modelled [?]).
    V4 Resolve(int value_cv, int bone_cv, int object_cv, bool& is_vec) {
        EvalContext ctx = Ctx();
        Primitive p;
        bool has = value_cv >= 0 && Evaluate(ctx, value_cv, p);
        if (has && p.type == PrimType::Vec3) {
            is_vec = true;
            return {std::bit_cast<float>(uint32_t(p.bits)), std::bit_cast<float>(uint32_t(p.bits >> 32)),
                    std::bit_cast<float>(p.extra), 0};
        }
        if (bone_cv < 0 && object_cv < 0) {
            is_vec = false;
            return {has ? Scalar(p) : 0.0f, 0, 0, 0};
        }
        is_vec = true;
        return {};
    }
    static float Scalar(const Primitive& p) {
        switch (p.type) {
        case PrimType::Byte: return p.bits ? 1.0f : 0.0f;
        case PrimType::Int: return float(int32_t(p.bits));
        case PrimType::Float: return p.AsFloat();
        default: return 0.0f;
        }
    }
    Primitive AsPrim(const V4& v) const { return is_vec_ ? Primitive::Vec3(v.x, v.y, v.z) : Primitive::Float(v.x); }
    void Write(const Primitive& p, uint8_t priority, bool state_defined) {
        auto path = OutPath();
        if (path.empty()) return;
        instance().SetVar(path, p, VarUser{instance().id(), int16_t(index()), priority, state_defined});
    }
    static float Len(const V4& d) {
        float l2 = d.x * d.x + d.y * d.y + d.z * d.z;
        return l2 < 1.1754944e-38f ? -1.0f : std::sqrt(l2);
    }

    // Begin (0xf54320).
    void OnBegin() override {
        closed_form_ = false;
        if (Cv(kOutVar) < 0) return;
        if (Cv(kStart) < 0 && Cv(kStartBone) < 0 && Cv(kStartObject) < 0) {
            Primitive v;
            auto path = OutPath();
            if (instance().GetVar(path, v) && v.type == PrimType::Vec3) {
                cur_ = {std::bit_cast<float>(uint32_t(v.bits)), std::bit_cast<float>(uint32_t(v.bits >> 32)),
                        std::bit_cast<float>(v.extra), 0};
                is_vec_ = true;
            } else {
                cur_ = {instance().GetVar(path, v) ? Scalar(v) : 0.0f, 0, 0, 0};
                is_vec_ = false;
            }
        } else {
            cur_ = Resolve(Cv(kStart), Cv(kStartBone), Cv(kStartObject), is_vec_);
        }
        Write(AsPrim(cur_), 1, true);
        EvalContext ctx = Ctx();
        if (Cv(kDurationF) < 0) {
            remaining_ms_ = 0, has_duration_ = false;
        } else {
            remaining_ms_ = int32_t(EvaluateFloat(ctx, Cv(kDurationF), 0.0f) * 1000.0f), has_duration_ = true;
        }
        reached_ = false;
        last_tick_ = now();
        ScheduleMotion();
    }
    // 0xf54bc0 [G].
    void ScheduleMotion() {
        const Graph& g = instance().graph();
        EvalContext ctx = Ctx();
        float rate = EvaluateFloat(ctx, Cv(kRate), 1.0f);
        auto literal = [&](int cv) { return cv < 0 || g.IsA(cv, kCvInt) || g.IsA(cv, kCvFloat); };
        closed_form_ = rate > 1.1920929e-5f && Cv(kDestBone) < 0 && Cv(kDestObject) < 0 && literal(Cv(kRate)) &&
                       literal(Cv(kDest));
        RemoveTimers(0);
        if (!closed_form_) {
            SetTicking(true);
            return;
        }
        int64_t t;
        if (!has_duration_) {
            bool v;
            V4 dest = Resolve(Cv(kDest), Cv(kDestBone), Cv(kDestObject), v);
            V4 d{dest.x - cur_.x, dest.y - cur_.y, dest.z - cur_.z, 0};
            float dist = std::max(0.0f, Len(d));
            t = RoundMs(dist / rate);
        } else {
            t = remaining_ms_;
        }
        ScheduleTimer(now() + t, 0);
        SetTicking(false);
    }
    // Per-pass tick, ticking mode (0xf535d0) [G].
    void OnTick(int64_t) override {
        int64_t dt = now() - last_tick_;
        if (dt < 1) return;
        bool v;
        V4 dest = Resolve(Cv(kDest), Cv(kDestBone), Cv(kDestObject), v);
        EvalContext ctx = Ctx();
        if (!has_duration_) {
            float rate = EvaluateFloat(ctx, Cv(kRate), 1.0f);
            if (rate <= 0.0f) return;
            V4 d{dest.x - cur_.x, dest.y - cur_.y, dest.z - cur_.z, dest.w - cur_.w};
            float len = Len(d);
            float step = float(dt) * 0.001f * rate;
            if (len <= step + 1.1920929e-5f) {
                cur_ = dest;
                Arrive();
            } else {
                cur_ = {cur_.x + step * d.x / len, cur_.y + step * d.y / len, cur_.z + step * d.z / len,
                        cur_.w + step * d.w / len};
                reached_ = false;
            }
        } else {
            if (reached_) return;
            int32_t r = remaining_ms_;
            if (dt < r) {
                float a = float(1.0 - double(r - dt) / r);
                cur_ = {cur_.x + a * (dest.x - cur_.x), cur_.y + a * (dest.y - cur_.y), cur_.z + a * (dest.z - cur_.z),
                        cur_.w + a * (dest.w - cur_.w)};
                remaining_ms_ = int32_t(r - dt);
            } else {
                cur_ = dest;
                remaining_ms_ = 0;
                Arrive();
            }
        }
        last_tick_ = now();
    }
    void Arrive() {
        reached_ = true;
        if (!Flag(kKeepFollowing)) ScheduleLifetime(LifetimeKind::Finish);
    }
    // Timer (0xf532c0) [G]: the closed-form chase arrived.
    void OnTimer(int32_t data) override {
        if (data != 0) return;
        bool v;
        cur_ = Resolve(Cv(kDest), Cv(kDestBone), Cv(kDestObject), v);
        remaining_ms_ = 0;
        reached_ = true;
        if (!Flag(kKeepFollowing)) ScheduleLifetime(LifetimeKind::Finish);
        last_tick_ = now();
    }
    // The value readers get (+0x228 0xf534e0 / ValueAt 0xf54940) [G].
    V4 ValueAt(int64_t t) {
        if (!closed_form_) return cur_;
        int64_t dt = t - last_tick_;
        if (dt <= 0) return cur_;
        bool v;
        V4 dest = Resolve(Cv(kDest), Cv(kDestBone), Cv(kDestObject), v);
        if (has_duration_) {
            if (reached_ || dt >= remaining_ms_) return dest;
            float a = float(dt) / float(remaining_ms_);
            return {cur_.x + a * (dest.x - cur_.x), cur_.y + a * (dest.y - cur_.y), cur_.z + a * (dest.z - cur_.z), 0};
        }
        EvalContext ctx = Ctx();
        float rate = EvaluateFloat(ctx, Cv(kRate), 1.0f);
        if (rate <= 0.0f) return cur_;
        V4 d{dest.x - cur_.x, dest.y - cur_.y, dest.z - cur_.z, 0};
        float len = Len(d);
        float step = float(dt) * 0.001f * rate;
        if (step + 1.1920929e-5f < len) return {cur_.x + step * d.x / len, cur_.y + step * d.y / len, cur_.z + step * d.z / len, 0};
        return dest;
    }
    // Re-enter (0xf53200) [G]: restart when b36f935f is set (bake, then begin again).
    void OnReenter() override {
        if (!Flag(kRestart) || Cv(kOutVar) < 0) return;
        Write(AsPrim(ValueAt(now())), 0, false);
        OnBegin();
    }
    // End hook +0x158 (0xf53270) [G], finish and abort: bake the value reached.
    void Bake() {
        if (Cv(kOutVar) >= 0) Write(AsPrim(ValueAt(now())), 0, false);
    }
    void OnFinishHook() override { Bake(); }
    void OnAbortHook() override { Bake(); }
    void TraverseFinished() override { FirePlugField(kOnFinished); }
    void TraverseAborted() override { FirePlugField(kOnAbort); }
    // State data (0xf53b10) [G]; [?] whether the mirror's reflected fields precede it is not checked.
    void WriteData(BitOut& out, bool) const override {
        out.Bit(is_vec_), out.Bit(has_duration_), out.Bit(reached_);
        out.Bits(std::bit_cast<uint32_t>(cur_.x), 32);
        if (is_vec_) out.Bits(std::bit_cast<uint32_t>(cur_.y), 32), out.Bits(std::bit_cast<uint32_t>(cur_.z), 32);
        if (has_duration_) {
            bool wide = uint32_t(remaining_ms_) >= 0x10000;
            out.Bit(wide);
            out.Bits(uint32_t(remaining_ms_), wide ? 32 : 16);
        }
        out.Bit(closed_form_);
        if (closed_form_) out.Prefix3(uint32_t((int64_t(instance().sm().cf()) + 1) * 16 - last_tick_), 8, 16, 32);
    }
    void WriteChangeKey(BitOut& out) const override {  // the value and the remaining time move with time
        out.Bit(is_vec_), out.Bit(has_duration_), out.Bit(reached_), out.Bit(closed_form_);
    }

private:
    V4 cur_;
    bool is_vec_ = false, has_duration_ = false, reached_ = false, closed_form_ = false;
    int32_t remaining_ms_ = 0;
    int64_t last_tick_ = 0;
};

// STUStatescriptStateWeaponVolley (class 0x15d2450 on the shared weapon-state base 0x1077xxx) [G]: shot k at
// begin + ShotTime(k) (shot 0 at begin); Timer 1 → m_onMinShotsPlug at ShotTime(minShots − 1) when minShots <
// ammo / ammoPerShot; Timer 2 at ShotTime(final) fires the last shot and finishes. The ammo / rate / pattern
// vars are owned (state-defined) while active and released with their final values at finish / abort.
// Server no-ops: aim, spread, projectile spawning, effects, de-dupe history, the volley records.
// Not modelled yet [?]: state-defined reads of the owned vars while active (readers see the stored begin
// value), the var-changed restart (0x1077db0), the WeaponComponent / mirrored precondition (assumed true).
class WeaponVolleyState : public State {
public:
    using State::State;
    static constexpr uint32_t kProjectilesPerShot = Hash("m_numProjectilesPerShot"),
                              kShotsPerSecond = Hash("m_numShotsPerSecond"), kMinShots = 0x64389377,
                              kMaxShots = 0xa835e878, kRamp = 0x8bc9d90b, kAmmoPerShot = 0xa4e081c9,
                              kOutAmmo = Hash("m_out_Ammo"), kRateVar = 0xa2a6d6a4, kPatternVar = 0x663ce7e5,
                              kShotEntityVar = 0x2ccaffb8, kOnMinShots = Hash("m_onMinShotsPlug"),
                              kOnAbort = Hash("m_onAbortPlug"), kOnFinished = Hash("m_onFinishedPlug");

    int Cv(uint32_t f) const { return instance().graph().Object(*node().fields, f); }
    std::vector<uint64_t> Path(uint32_t f) const { return instance().graph().DynamicPath(Cv(f)); }

    void OnActivate(bool) override { SetTicking(true); }  // 0xd44b60: joins the per-pass ticking list

    void OnBegin() override {  // 0x1077b30
        origin_ = 0;
        counter_ = uint8_t(counter_ + 1);  // volley id counter8 (0x1078a10)
        EvalContext ctx = Ctx();
        min_shots_ = EvaluateInt(ctx, Cv(kMinShots), 1);
        max_shots_ = EvaluateInt(ctx, Cv(kMaxShots), 1);
        float per_second = EvaluateFloat(ctx, Cv(kShotsPerSecond), 1.0f);
        LoadDynamicValues(per_second);
        ScheduleTimers(ctx);
        Tick();
    }
    // 0x1078ea0.
    void LoadDynamicValues(float per_second) {
        Primitive v;
        auto ammo_path = Path(kOutAmmo), rate_path = Path(kRateVar), pat_path = Path(kPatternVar);
        ammo_ = std::max(0, !ammo_path.empty() && instance().GetVar(ammo_path, v) ? ToInt(v) : max_shots_);
        rate0_ = std::max(1e-5f, !rate_path.empty() && instance().GetVar(rate_path, v) ? v.ToFloat() : per_second);
        x_ = std::clamp(!pat_path.empty() && instance().GetVar(pat_path, v) ? v.ToFloat() : 0.0f, 0.0f, 1.0f);
        EvalContext ctx = Ctx();
        per_shot_ = std::max(1, EvaluateInt(ctx, Cv(kAmmoPerShot), 1));
        if (!ammo_path.empty()) Own(ammo_path, Primitive::Int(ammo_), 0);
        if (!rate_path.empty()) Own(rate_path, Primitive::Float(rate0_), 1);
        if (!pat_path.empty()) Own(pat_path, Primitive::Float(x_), 2);
    }
    static int32_t ToInt(const Primitive& p) {
        switch (p.type) {
        case PrimType::Byte: return p.bits ? 1 : 0;
        case PrimType::Int: return int32_t(p.bits);
        case PrimType::Float: return int32_t(std::lround(p.AsFloat()));
        default: return 0;
        }
    }
    void Own(const std::vector<uint64_t>& path, const Primitive& value, int which) {
        instance().SetVar(path, value, VarUser{instance().id(), int16_t(index()), 1, true, uint8_t(which)});
    }
    // State-defined values (+0x228 = 0x1077e40) [G]: 0 ammo = start − per shot × (shot index now + 1) (≥ 0), 1 the
    // rate now (FLT), 2 the pattern value [?: 0x10788b0 not modelled: x]. The client's HUD reads the ammo per shot
    // through this while the volley runs.
    bool StateValue(uint8_t which, Primitive& out) const override {
        int64_t t = Elapsed() - origin_;
        switch (which) {
        case 0: out = Primitive::Int(std::max(0, ammo_ - per_shot_ * (ShotIndexAt(t) + 1))); return true;
        case 1: out = Primitive::Float(RateAt(t)); return true;
        case 2: out = Primitive::Float(x_); return true;
        default: return false;
        }
    }
    // 0x1079570 (+ the ramp set-up).
    void ScheduleTimers(EvalContext& ctx) {
        rate1_ = rate0_, accel_ = 0, ramp_t_ = 0, ramp_n_ = 0;
        if (int ramp = Cv(kRamp); ramp >= 0) {
            const Graph& g = instance().graph();
            const stu::Value& r = g.value(ramp);
            // Three float config vars at sub-STU +8 / +0x10 / +0x18: target rate, accel, duration [G].
            std::vector<int> cvs;
            for (auto& f : r.items)
                if (Graph::ObjectOf(&f) >= 0) cvs.push_back(Graph::ObjectOf(&f));
            if (cvs.size() >= 3) {
                rate1_ = EvaluateFloat(ctx, cvs[0], rate0_);
                float acc = EvaluateFloat(ctx, cvs[1], 0.0f), dur = EvaluateFloat(ctx, cvs[2], 0.0f);
                if (acc <= 0.0f) accel_ = (rate1_ - rate0_) / std::max(dur, 1e-5f);
                else accel_ = rate0_ < rate1_ ? std::fabs(acc) : rate0_ > rate1_ ? -std::fabs(acc) : 0.0f;
                ramp_t_ = accel_ != 0.0f ? (rate1_ - rate0_) / accel_ : 0.0f;
                ramp_n_ = 0.5f * accel_ * ramp_t_ * ramp_t_ + rate0_ * ramp_t_;
            } else {
                instance().sm().warnings.push_back("WeaponVolley rate ramp: config vars not found");
            }
        }
        fired_ = 0;
        next_shot_ = 0;
        int64_t base = begin_time() + origin_;
        int n_shots = per_shot_ ? ammo_ / per_shot_ : 0;
        if (min_shots_ < n_shots) ScheduleTimer(base + ShotTime(std::max(min_shots_ - 1, 0)), 1);
        final_ = std::max(std::min(max_shots_, n_shots) - 1, 0);
        ScheduleTimer(base + ShotTime(final_), 2);
    }
    // 0x1078220.
    int64_t ShotTime(int n) const {
        float t;
        if (accel_ == 0.0f || n < 0) t = float(n) / rate0_;
        else if (float(n) < ramp_n_) t = (std::sqrt(2.0f * accel_ * float(n) + rate0_ * rate0_) - rate0_) / accel_;
        else t = (float(n) - ramp_n_) / rate1_ + ramp_t_;
        return RoundMs(t);
    }
    // 0x1078170.
    int ShotIndexAt(int64_t ms) const {
        float s = float(ms) * 0.001f;
        if (s < 0.0f) return -1;
        if (accel_ == 0.0f) return int(std::floor(s * rate0_));
        if (s < ramp_t_) return int(std::floor(0.5f * accel_ * s * s + rate0_ * s));
        return int(std::floor(0.5f * accel_ * ramp_t_ * ramp_t_ + rate0_ * ramp_t_ + (s - ramp_t_) * rate1_));
    }
    float RateAt(int64_t ms) const {
        float s = float(ms) * 0.001f;
        if (accel_ != 0.0f && s >= 0.0f) return s < ramp_t_ ? accel_ * s + rate0_ : rate1_;
        return rate0_;
    }
    int64_t Elapsed() const { return std::max<int64_t>(0, now() - begin_time()); }
    // Tick (0xd44d60): fire the due shots.
    void Tick() {
        int64_t t = Elapsed() - origin_;
        while (next_shot_ <= t && fired_ < ammo_) FireShot();
    }
    void OnTick(int64_t) override { Tick(); }
    // 0xd456c0: the Statescript-visible part of a shot.
    void FireShot() {
        auto path = Path(kShotEntityVar);
        if (!path.empty()) instance().SetVar(path, Primitive::Entity(0));  // *0x185a0c8 = 0 [G][L]
        fired_++;
        next_shot_ = ShotTime(fired_);
    }
    void OnTimer(int32_t data) override {  // 0x1077d00
        if (data == 1) FirePlugField(kOnMinShots);
        else if (data == 2) {
            Tick();
            ScheduleLifetime(LifetimeKind::Finish);
        }
    }
    // +0x158 (0x10792c0), finish and abort: the owned vars get their final values.
    void Release() {
        int64_t t = Elapsed() - origin_;
        int i = ShotIndexAt(t + 1);
        auto write = [&](uint32_t f, const Primitive& p) {
            auto path = Path(f);
            if (!path.empty()) instance().SetVar(path, p, VarUser{instance().id(), int16_t(index()), 0, false});
        };
        write(kOutAmmo, Primitive::Int(std::max(0, ammo_ - per_shot_ * (i + 1))));
        write(kRateVar, Primitive::Float(RateAt(t)));
        write(kPatternVar, Primitive::Float(x_));  // [?] the launch pattern value: x without a pattern
    }
    void OnFinishHook() override { Release(); }
    void OnAbortHook() override { Release(); }
    void TraverseFinished() override { FirePlugField(kOnFinished); }
    void TraverseAborted() override { FirePlugField(kOnAbort); }
    // State data (0x10784e0) [G]: begin time as CF × 16 − begin (sign bit + 8 / 16 / 32, 0xf4b460), the volley
    // counter (bit 0 when 1, else bit 1 + small 4 / 8 / 16), the 6-bit state serial.
    void WriteData(BitOut& out, bool) const override {
        int32_t d = int32_t(int64_t(instance().sm().cf()) * 16 - begin_time());
        out.Bit(d < 0);
        out.Prefix3(uint32_t(d < 0 ? -d : d), 8, 16, 32);
        if (counter_ == 1) out.Bit(false);
        else out.Bit(true), out.Prefix3(counter_, 4, 8, 16);
        out.Bits(uint32_t(serial()) & 0x3F, 6);
    }
    void WriteChangeKey(BitOut& out) const override {  // the begin offset moves with the clock
        out.Bits(counter_, 8), out.Bits(uint32_t(serial()) & 0x3F, 6);
    }

private:
    uint8_t counter_ = 0;
    int64_t origin_ = 0, next_shot_ = 0;
    int ammo_ = 0, per_shot_ = 1, min_shots_ = 1, max_shots_ = 1, final_ = 0, fired_ = 0;
    float rate0_ = 1, rate1_ = 1, accel_ = 0, ramp_t_ = 0, ramp_n_ = 0, x_ = 0;
};

// STUStatescriptStateStack (p0 class 0x15c9ed8) [G]: pushes onto the stack of m_out_Var at its m_priority
// (StackPriority record); the top member fires m_topSubgraphPlug, the one directly under it
// m_underSubgraphPlug; the var reads as the fold of the members' m_value (topmost with a value wins).
class StackState : public StackMember {
public:
    using StackMember::StackMember;
    static constexpr uint32_t kOutVar = Hash("m_out_Var"), kPriority = Hash("m_priority"), kValue = Hash("m_value"),
                              kTop = Hash("m_topSubgraphPlug"), kUnder = Hash("m_underSubgraphPlug");
    int Cv(uint32_t f) const { return instance().graph().Object(*node().fields, f); }
    const OutPlug* PlugOf(uint32_t field) const {
        for (auto& p : node().plugs)
            if (p.field == field) return &p;
        return nullptr;
    }
    void Setup() {  // activate (0xf55770): priority, the var, ticking
        on_top_ = under_ = false;
        EvalContext ctx = Ctx();
        auto rec = instance().sm().FindStackPriority(EvaluateGuid(ctx, Cv(kPriority)));
        priority_ = rec.priority, wins_ties_ = rec.wins_ties;
        ResolveStackVar(instance().graph().DynamicPath(Cv(kOutVar)));
        SetTicking(Cv(kValue) >= 0 && DependsOnExternal(ctx, Cv(kValue)));
        last_tick_ = -1;
    }
    void OnActivate(bool server_apply) override {
        Setup();
        if (!server_apply) Push();
    }
    // Import = the reader (0xf55bf0) + the replicated push: placement from the predecessor, bits as sent,
    // no plugs.
    void OnImport(const uint8_t* data, int nbits) override {
        Setup();
        BitIn in{data, nbits};
        uint32_t pred = 0;
        bool has = ReadPredecessor(in, instance().graph(), pred);
        bool top = in.Bit(), under = in.Bit();
        Push(has ? pred : 0, true, false);
        on_top_ = top, under_ = under;
    }
    void Fold(Primitive& v) override {
        if (Cv(kValue) < 0) return;
        EvalContext ctx = Ctx();
        Primitive out;
        if (Evaluate(ctx, Cv(kValue), out)) v = out;
    }
    void AbortBoth() {  // 0xf56290
        if (on_top_)
            if (auto* p = PlugOf(kTop)) AbortStates(p->subgraph);
        on_top_ = false;
        if (under_)
            if (auto* p = PlugOf(kUnder)) AbortStates(p->subgraph);
        under_ = false;
    }
    void BecomeTop() override {  // 0xf55e70
        if (on_top_) return;
        if (under_) AbortBoth();
        FirePlugField(kTop);
        on_top_ = true;
        if (!active()) AbortBoth();
    }
    void BecomeUnder() override {  // 0xf55f20
        if (under_) return;
        if (on_top_) AbortBoth();
        FirePlugField(kUnder);
        under_ = true;
        if (!active()) AbortBoth();
    }
    void OnTick(int64_t) override {  // 0xf55890
        if (last_tick_ == now()) return;
        if (key_ && stack_var_) instance().sm().Recompute(*stack_var_, true);
        last_tick_ = now();
    }
    void VarChanged(Var* var) override {  // 0xf559d0
        if (key_ && stack_var_ && var != stack_var_) instance().sm().Recompute(*stack_var_, true);
        EvalContext ctx = Ctx();
        SetTicking(Cv(kValue) >= 0 && DependsOnExternal(ctx, Cv(kValue)));
    }
    // State data (0xf55b10) [G]: predecessor (the member above), on-top bit, under bit.
    void WriteData(BitOut& out, bool) const override {
        WritePredecessor(out);
        out.Bit(on_top_), out.Bit(under_);
    }
    void OnDeactivate(int reason) override {  // p0 0xf55970
        if (!key_ || reason == 2 || reason == 5) return;
        AbortBoth();
        Unregister();
    }
};

// STUStatescriptStateAbility (p1 class 0x15d81b0; a stack member, ctor installs the Stack vtable) [G]:
// buffers presses of its buttons, starts on a press (or while held) when its conditions, cooldown and stack
// slot allow, fires its ability subgraph (+0x1F0) while running; never finishes by itself.
// Not modelled [?]: owner death (assumed alive), owner comp 0x10 flag 0x400, the cooldown var's
// state-defined reads (readers see the stored value), Timer data 3's producer, the +0x240 stop input plug,
// server-only lists +0x120..+0x140.
class AbilityState : public StackMember, public VarStackHost {
public:
    using StackMember::StackMember;
    ~AbilityState() override {
        if (cd_var_) cd_var_->ClearStackSlot();
    }
    // The adopted cooldown var reads through its owner (user state-defined): the remaining cooldown, while its
    // stored value (the synced one) stays as adopted [L: $3d INT 8 stays 8]. Without this the var reads 8
    // forever and the ability never starts again (T051 [L]: second dash refused on the server).
    void OnStoredChanged(Var&) override {}
    static constexpr uint32_t kStartList = 0xe018f16a, kStopList = 0xb2ff48fa, kWindow = 0x0338e8e4,
                              kHoldButton = 0x14fd5806, kStartButton = Hash("m_startButton"), kStopButton = 0xf838418b,
                              kCdPolicy = 0x25198346, kCooldownVar = Hash("m_out_CooldownVar"), kCond2 = 0x84c50e4b,
                              kStartCondition = Hash("m_startCondition"), kKeepWhenDead = 0x543a9538,
                              kStartWhenDead = 0xdb1971cb, kStackVar = Hash("m_out_StackVar"), kKeepPrio = 0x752471c2,
                              kStartPrio = 0x3df852d3, kPriority = 0x92c13ac6, kSubgraph = 0x42a043f1,
                              kStarted = 0xa6e2adb2, kStopped = 0x211de955;
    static constexpr uint8_t kRunning = 0x1, kInStack = 0x2, kTop = 0x4, kLatched = 0x8;

    int Cv(uint32_t f) const { return instance().graph().Object(*node().fields, f); }
    bool StackMode() const { return Cv(kStackVar) >= 0 && Cv(kPriority) >= 0; }
    int32_t CvInt(uint32_t f) {
        EvalContext ctx = Ctx();
        return EvaluateInt(ctx, Cv(f), 0);
    }

    void Setup() {  // activate (0x1086a00)
        priority_ = 0, wins_ties_ = false;
        EvalContext ctx = Ctx();
        if (StackMode()) {
            auto rec = instance().sm().FindStackPriority(EvaluateGuid(ctx, Cv(kPriority)));
            priority_ = rec.priority, wins_ties_ = rec.wins_ties;
            ResolveStackVar(instance().graph().DynamicPath(Cv(kStackVar)));
        }
    }
    void OnActivate(bool) override {
        Setup();
        SetTicking(true);
    }
    // Import (reader 0x10871b0, MatchLogic.md §14.7): flags, cooldown, predecessor; a member in the stack
    // (flag 2) is placed by its predecessor without plugs.
    void OnImport(const uint8_t* data, int nbits) override {
        Setup();
        BitIn in{data, nbits};
        flags_ = uint8_t(in.Bits(8));
        if (in.Bit()) {
            cd_ = std::bit_cast<float>(in.Bits(32));
            rate_ = in.Bit() ? std::bit_cast<float>(in.Bits(32)) : 1.0f;
            uint32_t t = in.Prefix3(8, 16, 32);
            start_ = (int64_t(instance().sm().cf()) + 1) * 16 - t;
        }
        uint32_t pred = 0;
        bool has = ReadPredecessor(in, instance().graph(), pred);
        if ((flags_ & kInStack) && StackMode()) Push(has ? pred : 0, true, false);
        on_top_ = flags_ & kTop;
        if (instance().sm().import_resumes) SetTicking(SteadyTicking());
        else SetTicking(true);  // activate (0x1086a00) joins the ticking list; the first update settles it
    }
    void Fold(Primitive& v) override { v = Primitive::Byte(true); }  // +0x258 0x1087810
    // State data (writer 0x1087000, reader 0x10871b0; MatchLogic.md §14.7): u8 flags; cooldown bit [f32 cd, bit
    // [f32 rate], count((CF + 1) × 16 − start)]; stack predecessor. [?] the writer's cooldown / rate conditions
    // are inferred from the reader (cd > 0, rate != 1).
    void WriteData(BitOut& out, bool) const override {
        out.Bits(flags_, 8);
        out.Bit(cd_ > 0.0f);
        if (cd_ > 0.0f) {
            out.Bits(std::bit_cast<uint32_t>(cd_), 32);
            out.Bit(rate_ != 1.0f);
            if (rate_ != 1.0f) out.Bits(std::bit_cast<uint32_t>(rate_), 32);
            out.Prefix3(uint32_t((int64_t(instance().sm().cf()) + 1) * 16 - start_), 8, 16, 32);
        }
        WritePredecessor(out);
    }
    // The cooldown values move with time: the client needs the data again only when the flags change, a cooldown
    // starts / ends or the stack position changes (T051 [H]: re-sending it every refresh restarted the HUD's
    // cooldown radial).
    void WriteChangeKey(BitOut& out) const override {
        out.Bits(flags_, 8), out.Bit(cd_ > 0.0f), out.Bits(cd_epoch_, 32);
        WritePredecessor(out);
    }

    // Button down (+0x1C8, 0x1086e50).
    void OnLogicalButton(int32_t button, bool down) override {
        if (!down) return;
        bool match = false;
        for (uint32_t f : {kHoldButton, kStartButton, kStopButton})
            if (Cv(f) >= 0 && CvInt(f) == button) match = true;
        if (!match) return;
        presses_.push_back({now(), button, serial(), false});
        if (!TimerQueued(1)) ScheduleTimer(now(), 1);
    }
    // +0x240 (0x1086fc0) [G]: a link entering through the trigger input plug stops the ability now, no begin.
    bool HandlesInput(const Link& link) override {
        if (!instance().graph().IsA(link.input_instance, kTriggerInputPlug)) return false;
        StopNow();
        return true;
    }
    void VarChanged(Var*) override {  // 0x1086d80
        SetTicking(true);
        if (!TimerQueued(1)) ScheduleTimer(now(), 1);
    }
    void OnTick(int64_t) override { Update(); }
    void OnTimer(int32_t data) override {  // 0x1086c60
        if (data == 1 || data == 2) Update();
        else if (data == 3) StopNow();
        else if (data == 4 && Cv(kCooldownVar) >= 0) {
            EvalContext ctx = Ctx();
            cd_ = EvaluateFloat(ctx, Cv(kCooldownVar), 0.0f);
            cd_epoch_++;
            SetCooldownUI(cd_ > 0.0f);
        }
    }
    void OnDeactivate(int reason) override {  // 0x1086b30
        if (reason == 5) return;
        if (flags_ & kInStack) Release();
        flags_ &= ~kLatched;
        if (flags_ & kRunning) StopAbility();
    }
    void BecomeTop() override {  // 0x1087880
        flags_ |= kTop;
        on_top_ = true;
        if (!(flags_ & kRunning)) StartAbility();
    }
    void BecomeUnder() override {  // 0x1087990: no longer on top
        flags_ &= ~kTop;
        on_top_ = false;
        if (flags_ & kInStack) Release();
        if (flags_ & kRunning) StopAbility();
    }

private:
    struct Press {
        int64_t time;
        int32_t button;
        int16_t serial;
        bool reported;
    };

    void StartAbility() {  // 0x1089310
        flags_ |= kRunning;
        FirePlugField(kSubgraph);
        FirePlugField(kStarted);
        Apply(kStartList);
    }
    void StopAbility() {  // 0x10893c0
        flags_ &= ~kRunning;
        for (auto& p : node().plugs)
            if (p.field == kSubgraph) AbortStates(p.subgraph);
        FirePlugField(kStopped);
        Apply(kStopList);
    }
    // 0x1087ab0: the flags are tested as they were on entry, so a running ability on top of its stack is
    // stopped twice (the release's lost-top path, then here): the stopped plug fires twice [L: Genji dash].
    void StopNow() {
        uint8_t f = flags_;
        if (f & kInStack) Release();
        if (f & kRunning) StopAbility();
    }
    void Apply(uint32_t list) {  // 0x1089510: {target path, value} in order, null user
        const Graph& g = instance().graph();
        for (int e : g.Objects(*node().fields, list)) {
            if (e < 0) continue;
            std::vector<int> cvs;
            for (auto& f : g.value(e).items)
                if (Graph::ObjectOf(&f) >= 0) cvs.push_back(Graph::ObjectOf(&f));
            if (cvs.size() < 2) continue;
            EvalContext ctx = Ctx();
            Primitive v;
            if (Evaluate(ctx, cvs[1], v)) instance().SetVar(g.DynamicPath(cvs[0]), v);
        }
    }
    void PushSelf() {  // +0x270 0x1088460
        flags_ |= kInStack;
        Push();
    }
    void Release() {  // +0x278 0x1088650
        flags_ &= ~kInStack;
        bool was_top = flags_ & kTop;
        Unregister();
        if (was_top) BecomeUnder();
    }
    float Rate() {  // 0x1088280: 1 + entity var 0xE44
        const Var* v = instance().sm().entity_bag().Find(varid::Make(0xE44));
        return 1.0f + (v ? v->value().ToFloat() : 0.0f);
    }
    void SetCooldownUI(bool on) {  // 0x1088340: HUD vars of ability slots 1–3
        if (Cv(kStartButton) < 0) return;
        int32_t b = CvInt(kStartButton);
        if (b < 0x77 || b > 0x79) return;
        uint64_t id = varid::Make(0x195A + (b - 0x77));
        Primitive v = Primitive::Int(on ? 2 : 1);
        const Var* cur = instance().sm().entity_bag().Find(id);
        if (cur && cur->stored().type == v.type && cur->stored().bits == v.bits) return;
        instance().sm().entity_bag().Set(id, v);
    }
    void ScheduleCooldownTimer() {
        RemoveTimers(2);
        if (cd_ > 0.0f && rate_ > 1.1920929e-5f)
            ScheduleTimer(now() + std::max<int64_t>(1, RoundMs(cd_ / rate_)), 2);
    }
    void RefreshCooldown() {  // 0x1087fa0
        if (cd_ <= 0.0f) {
            SetCooldownUI(false);
            FoldCooldown();
            return;
        }
        cd_ = std::max(0.0f, cd_ - float(now() - start_) * 0.001f * rate_);
        start_ = now();
        rate_ = Rate();
        SetCooldownUI(cd_ > 0.0f);
        ScheduleCooldownTimer();
        FoldCooldown();
    }
    void FoldCooldown() {  // the adopted var's reads = the remaining cooldown (0 once run out: Update releases it)
        if (!cd_var_) return;
        cd_var_->SetStackSlot(this, kCooldownFold);
        cd_var_->SetFolded(Primitive::Float(std::max(cd_, 0.0f)));
    }
    std::optional<Press> Buffered(uint32_t field) {  // 0x1089630
        if (Cv(field) < 0) return std::nullopt;
        int32_t button = CvInt(field);
        EvalContext ctx = Ctx();
        int64_t w = RoundMs(EvaluateFloat(ctx, Cv(kWindow), 0.0f));
        for (size_t i = 0; i < presses_.size(); i++) {
            Press p = presses_[i];
            if (now() - w <= p.time && p.time <= now() && p.button == button && p.serial <= serial()) {
                presses_[i] = presses_.back();
                presses_.pop_back();
                return p;
            }
        }
        return std::nullopt;
    }
    bool Held(uint32_t field) { return Cv(field) >= 0 && instance().sm().Held(CvInt(field)); }
    bool CondFalse(uint32_t field) {
        if (Cv(field) < 0) return false;
        EvalContext ctx = Ctx();
        return !EvaluateBool(ctx, Cv(field), false);
    }
    // MayTakeSlot (0xf4d1d0): the record must exist; the current holder (top) must not outrank it.
    bool MayTakeSlot(uint32_t guid_field) {
        EvalContext ctx = Ctx();
        uint64_t guid = EvaluateGuid(ctx, Cv(guid_field));
        auto& table = instance().sm().stack_priorities;
        auto rec = table.find(guid);
        if (rec == table.end()) return false;
        auto& stacks = instance().sm().stacks();
        auto it = stacks.find(key_);
        if (!key_ || it == stacks.end() || it->second.members.empty()) return true;
        StackMember* holder = it->second.members.back();
        if (holder == this) return true;
        return !(rec->second.priority < holder->priority() ||
                 (rec->second.priority == holder->priority() && !rec->second.wins_ties));
    }
    bool CanStart(bool& retry) {  // 0x1088810
        std::vector<Press> consumed;
        bool by_button = false;
        if (Cv(kHoldButton) >= 0) {
            if (!Held(kHoldButton)) return false;
            auto p = Buffered(kHoldButton);
            if (!p) p = Buffered(kStartButton);
            if (p) consumed.push_back(*p), by_button = true;
        } else if (Cv(kStartButton) >= 0) {
            if (auto p = Buffered(kStartButton)) {
                consumed.push_back(*p);
                by_button = true;
            } else if (Held(kStartButton)) {
                if (flags_ & kLatched) return false;
                by_button = true;
            } else {
                flags_ &= ~kLatched;
                return false;
            }
        }
        EvalContext ctx = Ctx();
        bool blocked = (Cv(kCooldownVar) >= 0 && EvaluateFloat(ctx, Cv(kCooldownVar), 0.0f) > 0.0f) ||
                       CondFalse(kStartCondition) || CondFalse(kCond2) ||
                       (Cv(kStackVar) >= 0 && Cv(kStartPrio) >= 0 && EvaluateGuid(ctx, Cv(kStartPrio)) && !MayTakeSlot(kStartPrio)) ||
                       (Cv(kStackVar) >= 0 && Cv(kPriority) >= 0 && EvaluateGuid(ctx, Cv(kPriority)) && !MayTakeSlot(kPriority));
        if (blocked) {
            for (auto& p : consumed) {
                if (!p.reported) retry = true;
                p.reported = true;
                presses_.push_back(p);
            }
            return false;
        }
        if (by_button) flags_ |= kLatched;
        return true;
    }
    bool KeepRunning() {  // 0x1088d80
        if ((flags_ & kLatched) && !Held(kStartButton)) flags_ &= ~kLatched;
        std::optional<Press> stop;
        if (Cv(kHoldButton) >= 0) {
            if (!Held(kHoldButton)) return false;
        } else if (Cv(kStopButton) >= 0 && (stop = Buffered(kStopButton))) {
            if (CvInt(kStopButton) == CvInt(kStartButton)) flags_ |= kLatched;
            return false;
        }
        EvalContext ctx = Ctx();
        bool halt = CondFalse(kCond2) ||
                    (Cv(kStackVar) >= 0 && Cv(kKeepPrio) >= 0 && EvaluateGuid(ctx, Cv(kKeepPrio)) && !MayTakeSlot(kKeepPrio));
        if (halt && stop) presses_.push_back(*stop);
        return !halt;
    }
    void Update() {  // p1 +0x288 0x1087b70 (the instance runs locally)
        UpdateInner();
    }
    void UpdateInner() {
        RefreshCooldown();
        bool force_stop = false;
        if (Cv(kCooldownVar) >= 0) {
            auto path = instance().graph().DynamicPath(Cv(kCooldownVar));
            if (Var* var = path.empty() ? nullptr : instance().FindVar(path, false)) {
                float v = var->value().ToFloat();
                const VarUser& u = var->user();
                bool mine = u.instance == instance().id() && u.state == index();
                if (u.state_defined || v <= 0.0f) {
                    if (mine && v <= 0.0f) {  // ReleaseCooldown (0xf48050)
                        if (cd_var_ == var) {
                            cd_var_->ClearStackSlot();
                            cd_var_ = nullptr;
                        }
                        instance().SetVar(path, Primitive::Float(0.0f), VarUser{instance().id(), int16_t(index()), 0, false});
                    }
                } else {  // AdoptCooldown (0x1089730): claim the var (user only; the stored value stays) [L: $3d INT 8]
                    cd_ = v, rate_ = Rate(), start_ = now();
                    cd_epoch_++;
                    instance().SetVar(path, var->stored(), VarUser{instance().id(), int16_t(index()), 1, true});
                    ScheduleCooldownTimer();
                    cd_var_ = var;
                    FoldCooldown();
                }
            }
        }
        bool retry = false;
        if (!StackMode()) {
            if (!(flags_ & kRunning)) {
                if (CanStart(retry)) StartAbility();
            } else if (!KeepRunning() || force_stop) {
                StopAbility();
            }
        } else {
            if (!(flags_ & kInStack)) {
                if (CanStart(retry)) PushSelf();
            } else if (!KeepRunning() || force_stop) {
                Release();
            }
        }
        SetTicking(SteadyTicking());
    }
    // The update's tail (0x1087b70): stays on the ticking list while presses are buffered, or while no cooldown runs
    // and a start condition depends on something outside the graph (the client instance's +0x180 is always 0).
    bool SteadyTicking() {
        EvalContext ctx = Ctx();
        bool dyn = (Cv(kStartCondition) >= 0 && DependsOnExternal(ctx, Cv(kStartCondition))) ||
                   (Cv(kCond2) >= 0 && DependsOnExternal(ctx, Cv(kCond2)));
        return !presses_.empty() || (cd_ <= 1.1920929e-5f && dyn);
    }

    uint8_t flags_ = 0;
    float cd_ = 0.0f, rate_ = 1.0f;
    int64_t start_ = 0;
    uint32_t cd_epoch_ = 0;
    Var* cd_var_ = nullptr;                          // the adopted cooldown var, while it reads through us
    static constexpr uint32_t kCooldownFold = 0xC0DE; // stack key marking our fold
    std::vector<Press> presses_;
};

// STUConfigVar c81f8768 (CVBoolBase; +0x10 a67f0501 the slot's dynamic var, +0x18 m_stackPriority) [G]
// (StatescriptConfigVarCatalogue.md): true when a member of that priority may take the slot: the priority
// record exists and the slot has no holder, or its priority beats the holder's (equal: wins ties).
class MayTakeSlotEvaluator : public Evaluator {
public:
    bool Get(const Graph& g, int cv, EvalContext& ctx, Primitive& out) const override {
        if (!ctx.instance) return false;
        const stu::Value& v = g.value(cv);
        int slot = g.Object(v, 0xa67f0501), prio = g.Object(v, Hash("m_stackPriority"));
        if (slot < 0 || prio < 0) {
            out = Primitive::Byte(false);
            return true;
        }
        Instance& inst = *ctx.instance;
        auto rec = inst.sm().stack_priorities.find(EvaluateGuid(ctx, prio));
        bool ok = false;
        if (rec != inst.sm().stack_priorities.end()) {
            int16_t owner = 0;
            Var* var = inst.FindVar(g.DynamicPath(slot), false, &owner);
            uint32_t key = var ? uint16_t(owner) | (uint32_t(var->id() & 0xFFFF) << 16) : 0;
            auto it = inst.sm().stacks().find(key);
            if (!var || it == inst.sm().stacks().end() || it->second.members.empty()) ok = true;
            else {
                const StackMember* holder = it->second.members.back();
                ok = rec->second.priority > holder->priority() ||
                     (rec->second.priority == holder->priority() && rec->second.wins_ties);
            }
        }
        out = Primitive::Byte(ok);
        return true;
    }
};

// STUStatescriptStateBlinkEffect (client class 0x15d4fc8): presentation, except that a link entering through its
// trigger input plug (+0x100 5094068c) is handled by +0x240 (0xd48de0: plays the blink, no begin) [G][L: Genji
// dash, 0x1db #13 is not re-entered].
class BlinkEffectState : public State {
public:
    using State::State;
    bool HandlesInput(const Link& link) override { return instance().graph().IsA(link.input_instance, kTriggerInputPlug); }
};

// STUStatescriptStateLogicalButton (ff77458c): reacts to its m_logicalButton. A down edge fires m_onGoingDownPlug
// and swaps the up subgraph (0x29909c7c) for the down one (0x8496114b); an up edge fires m_onComingUpPlug and
// swaps back; on begin the subgraph of the button's current state starts. Without it no graph saw reload (Genji
// 0x304 node 4, button 0x50 → node 8) [L: client trace T051]; the client class was not read [?: exact order].
// STUStatescriptStateLogicalButton (ff77458c; p2 class vtable 0x15c7f70) [G] (docs/StatescriptNodes.md §3.4):
// begin 0xf58370, timer 0xf585e0, end hook 0xf58550, button 0xf586d0. Fields +0x100 m_logicalButton, +0x108
// 0338e8e4 press window (s), +0x110 8cbe9822 (presses count even while the owner's input is blocked), +0x120
// m_onGoingDownPlug, +0x128 m_onComingUpPlug, +0x130 down subgraph 8496114b, +0x138 up subgraph 29909c7c.
class LogicalButtonState : public State {
public:
    using State::State;
    static constexpr uint32_t kButton = Hash("m_logicalButton"), kWindow = 0x0338e8e4, kIgnoreBlock = 0x8cbe9822,
                              kDown = Hash("m_onGoingDownPlug"), kUp = Hash("m_onComingUpPlug"), kDownSub = 0x8496114b,
                              kUpSub = 0x29909c7c;
    // Begin: a press buffered for this activation (its serial) within the window fires the going-down plug; then
    // the button held → the down subgraph; else a buffered press → the down subgraph and a Timer (data 1) at
    // now + 1 ms (the release); else the up subgraph.
    void OnBegin() override {
        if (Cv(kButton) < 0) return;
        int32_t button = Button();
        EvalContext ctx = Ctx();
        bool buffered = press_serial_ == serial() &&
                        float(now() - press_time_) * 0.001f <= EvaluateFloat(ctx, Cv(kWindow), 0.0f);
        if (buffered) FirePlugField(kDown);
        if (instance().sm().Held(button)) {
            FirePlugField(kDownSub);
        } else if (buffered) {
            FirePlugField(kDownSub);
            if (!instance().stopped()) ScheduleTimer(now() + 1, 1);
        } else {
            FirePlugField(kUpSub);
        }
    }
    // Timer 1: still not held → coming-up plug, up subgraph, the down subgraph's states aborted.
    void OnTimer(int32_t data) override {
        if (data != 1 || Cv(kButton) < 0 || instance().sm().Held(Button())) return;
        FirePlugField(kUp);
        FirePlugField(kUpSub);
        AbortSub(kDownSub);
    }
    // Every notification of the state's button, active or not: a down edge records the press (time, and the
    // serial of the activation it belongs to); while active, a down fires going-down + the down subgraph and
    // aborts the up subgraph's states, an up the reverse. With the owner's input blocked (instance +0x220: a flag
    // of the owner's component 0x10, host state) downs are ignored unless 8cbe9822.
    void OnLogicalButton(int32_t button, bool down) override {
        if (Cv(kButton) < 0) return;
        EvalContext ctx = Ctx();
        if (down && !EvaluateBool(ctx, Cv(kIgnoreBlock), false) && instance().sm().input_blocked &&
            instance().sm().input_blocked(instance().sm().owner()))
            return;
        if (button != Button()) return;
        if (down) {
            press_time_ = now();
            press_serial_ = int16_t(serial() + (active() ? 0 : 1));
        }
        if (!active()) return;
        FirePlugField(down ? kDown : kUp);
        FirePlugField(down ? kDownSub : kUpSub);
        AbortSub(down ? kUpSub : kDownSub);
    }
    void OnEndHook() override {  // 0xf58550: both subgraphs' states aborted
        if (Cv(kButton) < 0) return;
        AbortSub(kDownSub);
        AbortSub(kUpSub);
    }

private:
    int Cv(uint32_t f) const { return instance().graph().Object(*node().fields, f); }
    int32_t Button() {
        EvalContext ctx = Ctx();
        return EvaluateInt(ctx, Cv(kButton), -1);
    }
    void AbortSub(uint32_t field) {
        for (auto& p : node().plugs)
            if (p.field == field) AbortStates(p.subgraph);
    }
    int64_t press_time_ = 0;
    int16_t press_serial_ = -1;
};

}  // namespace

std::unique_ptr<State> CreateHeroState(Instance& inst, int state_index, uint32_t type) {
    switch (type) {
    case kAnim: return std::make_unique<AnimState>(inst, state_index);
    case kChaseVar: return std::make_unique<ChaseVarState>(inst, state_index);
    case kWeaponVolley: return std::make_unique<WeaponVolleyState>(inst, state_index);
    case kStack: return std::make_unique<StackState>(inst, state_index);
    case kAbility: return std::make_unique<AbilityState>(inst, state_index);
    case kBlinkEffect: return std::make_unique<BlinkEffectState>(inst, state_index);
    case 0xff77458c: return std::make_unique<LogicalButtonState>(inst, state_index);
    }
    return nullptr;
}

}  // namespace ss

namespace ss {
void RegisterHeroEvaluators(EvaluatorRegistry& r) { r.Register(0xc81f8768, std::make_shared<MayTakeSlotEvaluator>()); }
}  // namespace ss
