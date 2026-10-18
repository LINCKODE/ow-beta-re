// State types with client behaviour that the runtime models. Everything else falls back to the nearest
// modelled ancestor (the plain STUStatescriptState: plugs and lifetime only) and is reported as unmodelled.
#include <algorithm>
#include <cmath>
#include <format>

#include "statescript/capabilities.h"
#include "statescript/runtime.h"
#include "states_hero.h"
#include "match.h"

namespace ss {

namespace {

constexpr uint32_t kWait = 0x65f158da, kBooleanSwitch = 0xe811ecbb, kSwitch = 0x26367267,
                   kSubScript = 0x3db862c4, kWatch = 0xe67578c0, kTimedPublish = 0xcd396790, kEffect = 0xffbfa88d,
                   kUXBase = 0x8ba08edd, kCosmeticEntity = 0x6e81f6d4, kGamePadVibration = 0x44e7f474,
                   kPulse = 0x3f67b31b;

// STUStatescriptStateWait (vtable 0x16052b0) [G]: StatescriptRuntime.md §3.6.
class WaitState : public State {
public:
    using State::State;
    static constexpr uint32_t kTimeout = Hash("m_timeout"), kOnAbort = Hash("m_onAbortPlug"),
                              kOnFinished = Hash("m_onFinishedPlug"), kRestart = 0x1bff3867, kPolled = 0xc5a427fa;

    bool Flag(uint32_t f) const {
        auto* v = node().Field(f);
        return v && v->AsU8();
    }
    // 0xf52f00: t = round(timeout * 1000) ms (half away from zero); t <= 0 -> finish now; else a Timer at
    // now + t, or per-pass polling when the node is "polled".
    void OnBegin() override {
        EvalContext ctx = Ctx();
        float ms = EvaluateFloat(ctx, instance().graph().Object(*node().fields, kTimeout), 0.0f) * 1000.0f;
        ms = ms >= 0.0f ? ms + 0.5f : ms - 0.5f;
        timeout_ = int32_t(ms);
        bool polled = Flag(kPolled);
        if (float(timeout_) * 0.001f > 0.0f) {
            if (!polled) ScheduleTimer(now() + timeout_, 1);
        } else {
            ScheduleLifetime(LifetimeKind::Finish);
        }
        SetTicking(polled);
    }
    void OnImport(const uint8_t*, int) override { OnBegin(); }  // restart the wait at the import time [?]
    void OnTick(int64_t) override {  // 0xf53120
        if (now() - begin_time() >= timeout_) ScheduleLifetime(LifetimeKind::Finish);
    }
    void OnReenter() override {  // 0xf53030
        if (!Flag(kRestart)) return;
        RemoveTimers(1);
        OnBegin();
    }
    void OnTimer(int32_t data) override {
        if (data == 1) ScheduleLifetime(LifetimeKind::Finish);
    }
    void TraverseFinished() override { FirePlugField(kOnFinished); }
    void TraverseAborted() override { FirePlugField(kOnAbort); }

private:
    int32_t timeout_ = 0;
};

// STUStatescriptStateBooleanSwitch (vtable 0x1605058) [G]: begin 0xf55170, timer 0xf55270, var changed
// 0xf55420, apply 0xf55640.
class BooleanSwitchState : public State {
public:
    using State::State;
    static constexpr uint32_t kCondition = Hash("m_condition"), kOnTrue = Hash("m_onTruePlug"),
                              kOnFalse = Hash("m_onFalsePlug"), kTrueSub = Hash("m_trueSubgraphPlug"),
                              kFalseSub = Hash("m_falseSubgraphPlug");

    int Cv() const { return instance().graph().Object(*node().fields, kCondition); }
    bool Eval() {
        EvalContext ctx = Ctx();
        return EvaluateBool(ctx, Cv(), false);
    }
    void Apply() {
        FirePlugField(value_ ? kOnTrue : kOnFalse);
        FirePlugField(value_ ? kTrueSub : kFalseSub);
    }
    const OutPlug* Branch(bool v) const {
        for (auto& p : node().plugs)
            if (p.field == (v ? kTrueSub : kFalseSub)) return &p;
        return nullptr;
    }
    void OnActivate(bool) override {  // 0xf550d0
        EvalContext ctx = Ctx();
        SetTicking(DependsOnExternal(ctx, Cv()));
    }
    void OnBegin() override {
        value_ = Eval();  // +0x120 keeps the last value across activations
        Apply();
    }
    // Import: the local state data is the stored value (1 bit): the client keeps what the server sent even
    // when it differs from the branch that runs (a later re-evaluation then re-applies it) [L: Genji 0x157 #33].
    void OnImport(const uint8_t* data, int nbits) override {
        OnActivate(true);
        value_ = nbits >= 1 && data ? (data[0] & 1) != 0 : Eval();
    }
    void VarChanged(Var*) override {
        EvalContext ctx = Ctx();
        SetTicking(DependsOnExternal(ctx, Cv()));
        // [?] the client also requires instance vfunc +0x90 false (or a client-only node).
        if (!instance().stopped()) ScheduleTimer(now(), 1);
    }
    void OnTimer(int32_t data) override {
        if (data == 1) Reevaluate();
    }
    void WriteData(BitOut& out, bool) const override { out.Bit(value_); }  // the stored value
    void OnTick(int64_t) override { Reevaluate(); }  // [?] 0xf55620 not read; assumed = timer path
    // [?] abort / finish hook (vfunc +0x150 = 0xf553c0, not decompiled): end the active branch's states.
    void OnAbortHook() override { EndBranch(); }
    void OnFinishHook() override { EndBranch(); }

private:
    void Reevaluate() {
        bool v = Eval();
        if (v == value_) return;
        if (const OutPlug* old = Branch(value_)) AbortStates(old->subgraph);
        value_ = v;
        Apply();
    }
    void EndBranch() {
        if (const OutPlug* b = Branch(value_)) AbortStates(b->subgraph);
    }
    bool value_ = false;
};

// STUStatescriptStateSwitch (vtable 0x16059b8) [G]: begin 0xf57a10, timer 0xf57a90, var changed 0xf57bc0,
// compute 0xf58010, apply 0xf581d0.
class SwitchState : public State {
public:
    using State::State;
    static constexpr uint32_t kCases = 0x3fd0156c, kMatchAll = 0xcdfceefc, kValue = Hash("m_switchValue"),
                              kDefault = 0xf3c7685f;

    std::vector<const OutPlug*> Cases() const {
        std::vector<const OutPlug*> out;
        for (auto& p : node().plugs)
            if (p.field == kCases) out.push_back(&p);
        if (out.size() > 64) out.resize(64);
        return out;
    }
    const OutPlug* Default() const {
        for (auto& p : node().plugs)
            if (p.field == kDefault) return &p;
        return nullptr;
    }
    uint64_t Compute() {
        const Graph& g = instance().graph();
        EvalContext ctx = Ctx();
        Primitive value;
        uint64_t mask = 0;
        if (!Evaluate(ctx, g.Object(*node().fields, kValue), value)) return 0;
        bool all = EvaluateBool(ctx, g.Object(*node().fields, kMatchAll), false);
        auto cases = Cases();
        for (size_t i = 0; i < cases.size(); i++) {
            Primitive c;
            if (cases[i]->value < 0 || !Evaluate(ctx, cases[i]->value, c)) continue;
            if (!LooselyEqual(value, c)) continue;
            mask |= uint64_t(1) << i;
            if (!all) break;
        }
        return mask;
    }
    void Apply(uint64_t next) {
        uint64_t old = mask_;
        mask_ = next;
        auto cases = Cases();
        for (size_t i = 0; (next || old) && i < cases.size(); i++, next >>= 1, old >>= 1) {
            if ((next & 1) && !(old & 1)) FirePlugIndexed(cases[i]);
            else if (!(next & 1) && (old & 1)) AbortStates(cases[i]->subgraph);
        }
        if (!default_active_) {
            if (mask_ != 0) return;
            default_active_ = true;
            FirePlugField(kDefault);
        } else if (mask_ != 0) {
            default_active_ = false;
            if (const OutPlug* d = Default()) AbortStates(d->subgraph);
        }
    }
    bool AnyExternal() {
        EvalContext ctx = Ctx();
        for (const OutPlug* c : Cases())
            if (c->value >= 0 && DependsOnExternal(ctx, c->value)) return true;
        return false;
    }
    void OnBegin() override {
        mask_ = 0;
        default_active_ = false;
        Apply(Compute());
    }
    // Import: the state data is one bit per case (at most 64), then the default-taken bit (+0xB0): reader
    // 0xf57e80, writer 0xf57dc0 (docs/StatescriptSync.md §4.2). Without the extra bit (an old start-up) the
    // default is derived from the mask.
    void OnImport(const uint8_t* data, int nbits) override {
        mask_ = 0;
        int cases = int(std::min<size_t>(Cases().size(), 64));
        auto n = std::min<int>(nbits, cases);
        for (int i = 0; i < n && data; i++)
            if (data[i / 8] >> (i % 8) & 1) mask_ |= uint64_t(1) << i;
        if (!data) mask_ = Compute();
        if (data && nbits > cases) default_active_ = data[cases / 8] >> (cases % 8) & 1;
        else default_active_ = mask_ == 0 && Default();
    }
    void VarChanged(Var*) override {
        if (instance().stopped()) return;
        ScheduleTimer(now(), 1, /*at_head=*/true);  // instance vfunc +0x148: push at the head
        if (AnyExternal()) SetTicking(true);
    }
    void OnTimer(int32_t data) override {
        if (data == 1) Apply(Compute());
    }
    // One bit per case (at most 64), then the default-taken bit (+0xB0; writer 0xf57dc0, reader 0xf57e80):
    // docs/StatescriptSync.md §4.2. Without it a Switch shifts the client's parse of the rest of the delta.
    void WriteData(BitOut& out, bool) const override {
        auto n = std::min<size_t>(Cases().size(), 64);
        for (size_t i = 0; i < n; i++) out.Bit(mask_ >> i & 1);
        out.Bit(default_active_);
    }
    void OnTick(int64_t) override { Apply(Compute()); }  // [?] 0xf57b50 not read
    // [?] abort hook (+0x150 = 0xf57b00, not decompiled): end the matched cases' and the default's states.
    void OnAbortHook() override { EndAll(); }
    void OnFinishHook() override { EndAll(); }

private:
    void EndAll() {
        auto cases = Cases();
        for (size_t i = 0; i < cases.size(); i++)
            if (mask_ & (uint64_t(1) << i)) AbortStates(cases[i]->subgraph);
        if (default_active_)
            if (const OutPlug* d = Default()) AbortStates(d->subgraph);
    }
    uint64_t mask_ = 0;
    bool default_active_ = false;
};

// STUStatescriptStateSubScriptBase (06bcf0df) / SubScript (3db862c4; p1 class vtable 0x1621978) [G]
// (docs/StatescriptNodes.md §3.1). Begin (+0x108, 0xf70a10): the child graph (+0x258, 0xf71020: m_statescriptGraph
// evaluated in the instance) becomes a child instance (+0x260, 0x10ccc00; component +0x70: kind 1, the parent's
// mirrored flag, the parent's owner), linked to this state (parent id, slot = state index, this state's serial);
// each init-data entry (7eb65d0d: {ec9f34a6 child var path, 5180e750 value evaluated here}) is queued as a SetVar
// (flags 8) at now on the instance owning the var (the entity bag's through the sync-manager queue); then the
// child is instantiated (its plain entries after the SetVars) and processed at once, with this state's
// exit-taken flag held. No child (no graph) → finish now unless 916d18ce. The child stopping aborts this state
// unless 916d18ce (Instance::Stop). Deactivate (+0x168, 0xf70e10, reason != 5): unless keep-alive (db3a2efb, +0xAA)
// the child is unlinked and asked to stop at now. Plugs: finished → +0x118, aborted → +0x110. Data (0xf70ea0,
// local only): small(child id: 0+4 / 10+8 / 11+16) + bit keep-alive; remote: none.
class SubScriptState : public State {
public:
    using State::State;
    static constexpr uint32_t kGraph = Hash("m_statescriptGraph"), kInit = 0x7eb65d0d,
                              kChildVar = 0xec9f34a6, kInitValue = 0x5180e750, kKeepAlive = 0xdb3a2efb,
                              kNoFinish = 0x916d18ce, kOnAbort = 0x41ba609a, kOnFinished = 0xfe841c79;

    bool Flag(uint32_t f) const {
        auto* v = node().Field(f);
        return v && v->AsU8();
    }
    int16_t child() const { return child_; }

    void OnBeginPre() override {
        child_ = 0;
        EvalContext ctx = Ctx();
        uint64_t guid = EvaluateGuid(ctx, GraphCv(), 0);
        SyncManager& sm = instance().sm();
        const Graph* cg = guid && sm.graph_provider ? sm.graph_provider(guid) : nullptr;
        if (guid && !cg) sm.warnings.push_back(std::format("SubScript: graph {:016x} not available", guid));
        Instance* c = cg ? sm.CreateChild(*cg, instance().id() > 0 || (instance().flags() & Instance::kMirrored)) : nullptr;
        if (c) {
            child_ = c->id();
            keep_alive_ = Flag(kKeepAlive);
            c->SetParent(instance().id(), uint32_t(index()), serial());
            if (auto* init = node().Field(kInit); init && init->type == stu::Value::Type::List)
                for (auto& e : init->items) QueueInit(*c, e);
            bool held = flags() & kExitTaken;
            SetFlag(kExitTaken, true);
            c->EnsureInstantiated();
            c->Process(0);
            SetFlag(kExitTaken, held);
            return;
        }
        if (!Flag(kNoFinish)) ScheduleLifetime(LifetimeKind::Finish);
    }
    void OnDeactivate(int reason) override {  // 0xf70e10
        if (child_ == 0 || reason == 5) return;
        if (!keep_alive_)
            if (Instance* c = instance().sm().FindInstance(child_); c && !c->stopped()) {
                c->SetParent(0, 0, -1);
                c->RequestStop();
            }
        child_ = 0;
    }
    void ChildStopped(Instance& c) override {  // from Instance::Stop (0xf500e0)
        if (child_ != c.id() || (c.parent_serial() >= 0 && c.parent_serial() != serial())) return;
        child_ = 0;
        if (!Flag(kNoFinish)) ScheduleLifetime(LifetimeKind::Abort);
    }
    void TraverseFinished() override { FirePlugField(kOnFinished); }
    void TraverseAborted() override { FirePlugField(kOnAbort); }
    void OnImport(const uint8_t* data, int nbits) override {  // reader 0xf70f40
        if (nbits < 1) return;
        auto bit = [&](int i) { return (data[i / 8] >> (i % 8) & 1) != 0; };
        int pos = 0, w = 4;
        if (bit(pos++)) w = bit(pos++) ? 16 : 8;
        uint32_t id = 0;
        for (int i = 0; i < w && pos < nbits; i++) id |= uint32_t(bit(pos++)) << i;
        child_ = int16_t(id);
        keep_alive_ = pos < nbits && bit(pos);
    }
    void WriteData(BitOut& out, bool) const override {
        out.Prefix3(uint16_t(child_), 4, 8, 16);
        out.Bit(keep_alive_);
    }
    void DataRefs(bool local, std::vector<int16_t>& ids) const override {
        if (local && child_) ids.push_back(child_);
    }

private:
    int GraphCv() const { return instance().graph().Object(*node().fields, kGraph); }  // +0x128
    void QueueInit(Instance& c, const stu::Value& e) {
        const Graph& g = instance().graph();
        int var_cv = Graph::ObjectOf(e.Find(kChildVar)), val_cv = Graph::ObjectOf(e.Find(kInitValue));
        if (var_cv < 0 || val_cv < 0) return;
        Primitive v;
        EvalContext ctx = Ctx();
        Evaluate(ctx, val_cv, v);  // in the parent (0xf5dac0 on the parent's bag); no value: INT 0 as initialised
        auto path = g.DynamicPath(var_cv);
        int16_t target = c.id();
        uint64_t var = 0;
        if (path.size() == 1) var = path[0];
        else if (path.size() == 2 && path[0] == varid::InstanceBag) var = path[1];
        else if (path.size() == 2 && path[0] == varid::EntityBag) target = 0, var = path[1];
        else {
            instance().sm().warnings.push_back("SubScript: init-data path not modelled");
            return;
        }
        Notification n = c.Make(NotifType::SetVar);
        n.flags |= kNotifFromMessage;
        n.target = target;
        n.var = var;
        n.value = v;
        if (target == 0) {  // the entity bag's var: through the sync-manager queue (0xf45d90)
            SyncManager& sm = instance().sm();
            sm.PostEvent(now(), [&sm, var, v] { sm.entity_bag().Set(var, v, VarUser{}); });
        } else {
            c.QueueSorted(n);
        }
    }

    int16_t child_ = 0;
    bool keep_alive_ = false;
};

// STUStatescriptStateWatch (e67578c0; p0 class vtable 0x1605508) [G] (docs/StatescriptNodes.md §3.5): activate
// 0xf56da0 stores the entries of m_watchedVal (+0x100, iterated); a var change (0xf56f00; not a write of its own
// out var b0f08764) re-reads them and, when they differ (count, or any key / value by loose ==), queues Timer 1 at
// now at the head of the queue; Timer 1 (0xf57160) writes the stored (previous) entries as a Dictionary to
// b0f08764 (+0x108), fires a415be52 (+0x110) and stores the current entries.
class WatchState : public State {
public:
    using State::State;
    static constexpr uint32_t kWatched = Hash("m_watchedVal"), kPrevious = 0xb0f08764, kChanged = 0xa415be52;
    void OnActivate(bool) override { stored_ = Read(); }
    void OnImport(const uint8_t*, int) override { stored_ = Read(); }
    void VarChanged(Var* var) override {
        int prev_cv = Cv(kPrevious);
        if (prev_cv >= 0 && var) {
            auto path = instance().graph().DynamicPath(prev_cv);
            if (!path.empty() && instance().FindVar(path, false) == var) return;
        }
        auto cur = Read();
        bool same = cur.size() == stored_.size();
        for (size_t i = 0; same && i < cur.size(); i++)
            same = LooselyEqual(cur[i].first, stored_[i].first) && LooselyEqual(cur[i].second, stored_[i].second);
        if (!same) ScheduleTimer(now(), 1, /*at_head=*/true);
    }
    void OnTimer(int32_t data) override {
        if (data != 1) return;
        if (int prev_cv = Cv(kPrevious); prev_cv >= 0) {
            uint64_t id;
            if (VarBag* bag = instance().WriteBag(instance().graph().DynamicPath(prev_cv), id)) {
                Var* v;
                if (auto* e = bag->BeginDictionaryWrite(id, VarUser{}, &v)) {
                    *e = stored_;
                    bag->CommitDictionary(*v);
                }
            }
        }
        FirePlugField(kChanged);
        stored_ = Read();
    }

private:
    int Cv(uint32_t f) const { return instance().graph().Object(*node().fields, f); }
    std::vector<DictEntry> Read() {
        EvalContext ctx = Ctx();
        return Iterate(ctx, Cv(kWatched));
    }
    std::vector<DictEntry> stored_;
};

// TimeInArray (27984fc9, class 0x16060c0) [G] (docs/StatescriptNodes.md §3.13): records when m_value first shows up in
// the array edfb51d3 (update 0xf58e00: while not found, the first element whose value equals m_value (0xec99c0) sets
// found = now − begin ms). m_out_Time is a state-defined var (user {instance, state, 0x81}): it reads the seconds since
// then, or m_defaultTime until found (+0x228 0xf58d00); deactivation freezes that value into a plain FLT (0xf58ab0).
// Activate 0xf58960 / var changed 0xf58c40 / tick 0xf58c30: update, then tick while the array or the value depends on
// something outside the graph.
class TimeInArrayState : public State {
public:
    using State::State;
    static constexpr uint32_t kArray = 0xedfb51d3, kValue = Hash("m_value"), kOut = Hash("m_out_Time"),
                              kDefault = Hash("m_defaultTime");
    void OnActivate(bool server_apply) override {
        if (!server_apply) found_ = -1;
        Update();
        if (auto path = OutPath(); !path.empty())
            instance().SetVar(path, Primitive::Float(0.0f), VarUser{instance().id(), int16_t(index()), 1, true});
    }
    void OnImport(const uint8_t*, int) override { OnActivate(false); }  // no state data: the reader is the base's
    void VarChanged(Var*) override { Update(); }
    void OnTick(int64_t) override { Update(); }
    void OnDeactivate(int reason) override {
        if (reason == 5) return;
        if (reason == 7) {
            found_ = -1;
            return;
        }
        Primitive v;
        StateValue(0, v);
        if (auto path = OutPath(); !path.empty())
            instance().SetVar(path, v, VarUser{instance().id(), int16_t(index()), 0, false});
    }
    bool StateValue(uint8_t, Primitive& out) const override {
        if (found_ != -1) {
            int64_t elapsed = std::max<int64_t>(0, now() - begin_time());
            out = Primitive::Float(std::max(0.0f, float(elapsed) * 0.001f - float(found_) * 0.001f));
        } else {
            EvalContext ctx = Ctx();
            out = Primitive::Float(EvaluateFloat(ctx, Cv(kDefault), 0.0f));
        }
        return true;
    }

private:
    int Cv(uint32_t f) const { return instance().graph().Object(*node().fields, f); }
    std::vector<uint64_t> OutPath() const { return instance().graph().DynamicPath(Cv(kOut)); }
    void Update() {
        EvalContext ctx = Ctx();
        if (found_ == -1 && Cv(kArray) >= 0) {
            Primitive v;
            if (Cv(kValue) < 0 || !Evaluate(ctx, Cv(kValue), v)) v = Primitive::Int(0);
            for (auto& e : Iterate(ctx, Cv(kArray)))
                if (LooselyEqual(v, e.second)) {
                    found_ = std::max<int64_t>(0, now() - begin_time());
                    break;
                }
        }
        SetTicking((Cv(kArray) >= 0 && DependsOnExternal(ctx, Cv(kArray))) ||
                   (Cv(kValue) >= 0 && DependsOnExternal(ctx, Cv(kValue))));
    }
    int64_t found_ = -1;  // +0xA8: ms after begin when m_value was found, −1 = not (yet)
};

// cd396790 (unnamed; p2 class vtable 0x15d4840) [G] (docs/StatescriptNodes.md §3.6): a timed state that publishes
// its timing. Begin 0xd67920: d = round(6b7c7d86 × 1000) ms; with prediction records suppressed (net-sync +0x5C0)
// it writes FLT vars 9d9d030f = a global clock (0xe51d10, s), 69afa3be = d / 1000, 3ecdfc9f = d / max(1, d); fires
// the subgraph plug 47e1781c, then Timer 1 at now + d. Timer 1 (0xd67bf0): finish now if b4fe98d8. Deactivate
// (0xd67b90): the subgraph's states aborted. Plugs: aborted +0x128, finished +0x130. The global clock is the
// host's (SyncManager::global_clock_ms; default: the sync manager's clock) [?].
class TimedPublishState : public State {
public:
    using State::State;
    static constexpr uint32_t kDuration = 0x6b7c7d86, kFinish = 0xb4fe98d8, kStartVar = 0x9d9d030f, kDurVar = 0x69afa3be,
                              kRateVar = 0x3ecdfc9f, kSub = 0x47e1781c, kOnAbort = Hash("m_onAbortPlug"),
                              kOnFinished = Hash("m_onFinishedPlug");
    void OnBegin() override {
        EvalContext ctx = Ctx();
        float f = EvaluateFloat(ctx, Cv(kDuration), 0.0f) * 1000.0f;
        int32_t d = int32_t(f >= 0.0f ? f + 0.5f : f - 0.5f);
        SyncManager& sm = instance().sm();
        float clock = float(sm.global_clock_ms ? sm.global_clock_ms() : double(sm.time()));
        Write(kStartVar, clock * 0.001f);
        Write(kDurVar, float(d) * 0.001f);
        Write(kRateVar, float(d) / float(std::max(1, d)));
        FirePlugField(kSub);
        ScheduleTimer(now() + d, 1);
    }
    void OnTimer(int32_t data) override {
        EvalContext ctx = Ctx();
        if (data == 1 && EvaluateBool(ctx, Cv(kFinish), false)) ScheduleLifetime(LifetimeKind::Finish);
    }
    void OnDeactivate(int) override {
        for (auto& p : node().plugs)
            if (p.field == kSub) AbortStates(p.subgraph);
    }
    void TraverseFinished() override { FirePlugField(kOnFinished); }
    void TraverseAborted() override { FirePlugField(kOnAbort); }

private:
    int Cv(uint32_t f) const { return instance().graph().Object(*node().fields, f); }
    void Write(uint32_t field, float v) {
        int cv = Cv(field);
        if (cv < 0) return;
        auto path = instance().graph().DynamicPath(cv);
        if (!path.empty()) instance().SetVar(path, Primitive::Float(v));
    }
};

// 3f67b31b, the networked pulse (class vtable 0x15c77f0, 0x1B0 B; docs/StatescriptNodes.md §3.14) [G]: every begin
// or re-entry (begin 0xd03e50 = re-enter 0xd03e60; not in server-apply mode) counts once, unless this command frame
// already pulsed (the CF list +0x140, pruned to the last 600 CFs), and puts the state on the instance's second list;
// its +0x128 hook (0xd04160) fires the plug 6697832a once per count and adds the CF to the list. The fired count
// (+0x180, u8, from -1) is the state data (both formats: bit b, u8; reader 0xd03f40, no client writer): a client
// that receives a higher count fires the plug until it has caught up (its own predicted pulses of a listed CF are
// not repeated), a first receipt fires once when b or 29ff2457 is set. The server's graphs link the plug into
// client-only nodes (0x3a3 node 27 Wait, 0x5ad): the count is what makes remote clients replay the pulse.
// The +0x128 hook runs at the end of the command-frame window (as Anim's, §3.8) [?]: here a Timer at CF × 16 + 15.
class PulseState : public State {
public:
    using State::State;
    static constexpr uint32_t kPlug = 0x6697832a;
    static constexpr int32_t kTimerPulse = 0x3f67;
    void OnBegin() override { OnReenter(); }
    void OnReenter() override {
        uint32_t cf = instance().sm().cf();
        if (std::find(cfs_.begin(), cfs_.end(), cf) != cfs_.end()) return;
        pending_++;
        if (!TimerQueued(kTimerPulse)) ScheduleTimer(std::max(now(), int64_t(cf) * SyncManager::kMsPerCf + 15), kTimerPulse);
    }
    void OnTimer(int32_t data) override {
        if (data != kTimerPulse) return;
        uint32_t cf = instance().sm().cf();
        if (pending_ > 0) {
            for (; pending_ > 0; pending_--) {
                FirePlugField(kPlug);
                fired_ = fired_ + 1 > 0xff ? 0 : fired_ + 1;
            }
            last_pulse_cf_ = cf;
            cfs_.push_back(cf);
        }
        // Pruned as the client does (unsigned: before CF 600 every entry goes).
        std::erase_if(cfs_, [&](uint32_t c) { return c < cf - 600u; });
    }
    // b = pulsed in the frame being sent [?: the client never writes this data; b makes a client that first sees
    // the state at its pulse replay it], u8 = the fired count.
    void WriteData(BitOut& out, bool) const override {
        out.Bit(last_pulse_cf_ == int64_t(instance().sm().cf()));
        out.Bits(uint32_t(fired_) & 0xff, 8);
    }
    void WriteChangeKey(BitOut& out) const override { out.Bits(uint32_t(fired_) & 0xff, 8); }
    // A server's restore: the count as sent, nothing fired.
    void OnImport(const uint8_t* data, int nbits) override {
        if (nbits >= 9) fired_ = int32_t((data[0] >> 1 | data[1] << 7) & 0xff);
    }
    int32_t fired() const { return fired_; }

private:
    int32_t fired_ = -1, pending_ = 0;
    int64_t last_pulse_cf_ = -1;
    std::vector<uint32_t> cfs_;
};

// STUStatescriptStateEffect (ffbfa88d; p2 class vtable 0x15dc8b8) [G]: presentation (the client plays the effect);
// lifecycle and plugs are the base state's. A has-update type. State data (reader 0xd7de40, no client writer): u2 =
// the state serial & 3 (the reader compares it with its own serial to replay a missed begin / end, +0x200 =
// 0xd7df70), the same in both formats.
class EffectState : public State {
public:
    using State::State;
    void WriteData(BitOut& out, bool) const override { out.Bits(uint32_t(serial()) & 3, 2); }
    void OnImport(const uint8_t* data, int nbits) override {
        if (nbits >= 2) SetSerial(int16_t((serial() & ~3) | (data[0] & 3)));
    }
};

// STUStatescriptStateUXBase (8ba08edd) and every UX state on its class (UXScreen, UXViewModel, UIInWorld, the menus,
// …; activate 0x1074f00) [G] (docs/StatescriptNodes.md §3.8): presentation, but activation writes a handle to
// this state into m_out_Handle (+0x138): INTVEC4 {4, instance id, state index, 0}, null user. UI commands (+0x128)
// and view-model updates (+0x1C0) come from the client's UI: nothing on a server.
class UXBaseState : public State {
public:
    using State::State;
    void OnActivate(bool) override {
        int cv = instance().graph().Object(*node().fields, Hash("m_out_Handle"));
        auto path = cv >= 0 ? instance().graph().DynamicPath(cv) : std::vector<uint64_t>{};
        if (path.empty()) return;
        uint64_t v = 4 | uint64_t(uint16_t(instance().id())) << 16 | uint64_t(uint16_t(index())) << 32;
        instance().SetVar(path, Primitive::Make(PrimType::IntVec4, v));
    }
};

// STUStatescriptStateCosmeticEntity (6e81f6d4; activate 0xd85c00, handle 0x10a45a0) [G]: presentation (the client
// creates a cosmetic entity); activation (not a replicated one) writes the handle 6173dc42 (+0x1C0) := INTVEC4 {1,
// instance id, state index, 0} as this state (user {instance, state, priority 1}); a rejected write (the var is
// held at a higher priority) finishes the state at once.
class CosmeticEntityState : public State {
public:
    using State::State;
    void OnActivate(bool server_apply) override {
        if (server_apply) return;
        int cv = instance().graph().Object(*node().fields, 0x6173dc42);
        auto path = cv >= 0 ? instance().graph().DynamicPath(cv) : std::vector<uint64_t>{};
        if (path.empty()) return;
        uint64_t v = 1 | uint64_t(uint16_t(instance().id())) << 16 | uint64_t(uint16_t(index())) << 32;
        VarUser user{.instance = instance().id(), .state = int16_t(index()), .priority = 1};
        if (instance().SetVar(path, Primitive::Make(PrimType::IntVec4, v), user) == SetResult::Rejected)
            ScheduleLifetime(LifetimeKind::Finish);
    }
};

// A server-only state type (no client class anywhere in its chain; docs/StatescriptNodes.md §3.9): the base state's
// lifecycle; begin and end are logged for the host (SyncManager::host_events), which applies the effect and may end
// the state (ss_sm_end_state) [?: when the real server ended them is not known].
// Their m_onFinishedPlug / m_onAbortPlug (when the type has them) fire on finish / abort [?]. Also used for
// VoiceStimulus (6e3b8ce5): its client class only fires m_onFinishedPlug when finished and nothing finishes it on
// the client (the tutorial brain waits on it forever there, MatchLogic.md): the server ends it after the line.
class HostState : public State {
public:
    using State::State;
    void OnBegin() override { instance().sm().RecordHostEvent(instance(), node(), 1); }
    void OnFinishHook() override { instance().sm().RecordHostEvent(instance(), node(), 2); }
    void OnAbortHook() override { instance().sm().RecordHostEvent(instance(), node(), 3); }
    void TraverseFinished() override { FirePlugField(Hash("m_onFinishedPlug")); }
    void TraverseAborted() override { FirePlugField(Hash("m_onAbortPlug")); }
};

// STUStatescriptStateGamePadVibration (44e7f474; activate 0x109c020, Timer = Wait's 0xf531e0) [G]: duration = the
// vibration asset's length (resource +0x18, f32) / m_timeScalar (default 1; ≤ 0.004 or no asset → abort now);
// looping (6d640a54) → no timer; else Timer 1 at now + round(duration × 1000) → finish. Plugs: aborted +0x128,
// finished +0x130. The asset data is the host's (SyncManager::resource_float); without it the state stays active
// (the client, which has the asset, would time it) [?].
class GamePadVibrationState : public State {
public:
    using State::State;
    void OnActivate(bool) override {
        const Graph& g = instance().graph();
        EvalContext ctx = Ctx();
        uint64_t guid = EvaluateGuid(ctx, g.Object(*node().fields, Hash("m_vibration")), 0);
        SyncManager& sm = instance().sm();
        if (!sm.resource_float) return;
        float length = 0.0f, scalar = EvaluateFloat(ctx, g.Object(*node().fields, Hash("m_timeScalar")), 1.0f);
        if (!guid || !sm.resource_float(guid, 0x18, length) || scalar <= 0.004f || length / scalar <= 0.0f) {
            ScheduleLifetime(LifetimeKind::Abort);
            return;
        }
        if (EvaluateBool(ctx, g.Object(*node().fields, 0x6d640a54), false)) return;
        float ms = length / scalar * 1000.0f;
        ScheduleTimer(now() + int64_t(ms >= 0.0f ? ms + 0.5f : ms - 0.5f), 1);
    }
    void OnTimer(int32_t data) override {
        if (data == 1) ScheduleLifetime(LifetimeKind::Finish);
    }
    void TraverseFinished() override { FirePlugField(Hash("m_onFinishedPlug")); }
    void TraverseAborted() override { FirePlugField(Hash("m_onAbortPlug")); }
};

}  // namespace

std::unique_ptr<State> CreateState(Instance& inst, int state_index) {
    const Graph& g = inst.graph();
    uint32_t type = g.state_node(state_index).type;
    if (auto m = CreateMatchState(inst, state_index, type)) return m;  // match / objective families (T055)
    if (auto m = CreateHostSystemState(inst, state_index, type)) return m;  // movement / collision / history (T060)
    if (IsHostType(type)) return std::make_unique<HostState>(inst, state_index);
    const stu::Registry& reg = g.registry();
    for (uint32_t t = type; t; ) {
        if (auto hero = CreateHeroState(inst, state_index, t)) return hero;
        switch (t) {
        case kWait: return std::make_unique<WaitState>(inst, state_index);
        case kBooleanSwitch: return std::make_unique<BooleanSwitchState>(inst, state_index);
        case kSwitch: return std::make_unique<SwitchState>(inst, state_index);
        case kSubScript: return std::make_unique<SubScriptState>(inst, state_index);
        case kWatch: return std::make_unique<WatchState>(inst, state_index);
        case 0x27984fc9: return std::make_unique<TimeInArrayState>(inst, state_index);
        case kTimedPublish: return std::make_unique<TimedPublishState>(inst, state_index);
        case kEffect: return std::make_unique<EffectState>(inst, state_index);
        case kUXBase: return std::make_unique<UXBaseState>(inst, state_index);
        case kCosmeticEntity: return std::make_unique<CosmeticEntityState>(inst, state_index);
        case kGamePadVibration: return std::make_unique<GamePadVibrationState>(inst, state_index);
        case kPulse: return std::make_unique<PulseState>(inst, state_index);
        case 0x01d6952a: case 0x7331cb24: case 0xa5f89e3f: case 0xb1001dcf: case 0xbf733a32: case 0xed81c7fb:
        case 0x10fc71ba: case 0x2785fbdd: case 0x85d43f96: case 0xaa488c28:
            // The type-list members (writer 0xf4aed0): list membership and data; their own effects (aim, camera,
            // visibility, animation speed) are the host's / presentation.
            return std::make_unique<TypeListMember>(inst, state_index);
        case stype::State:
            if (type != stype::State) inst.sm().unmodelled_states[type]++;
            return std::make_unique<State>(inst, state_index);
        }
        const stu::TypeInfo* ti = reg.Find(t);
        t = ti ? ti->parent : 0;
    }
    inst.sm().unmodelled_states[type]++;
    return std::make_unique<State>(inst, state_index);
}

}  // namespace ss
