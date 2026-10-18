// Entries, conditions and actions: stateless per-type behaviour, run synchronously when a link reaches
// them (StatescriptRuntime.md §3.5). Types not listed here run as explicit no-ops and are counted in
// SyncManager::unmodelled_nodes (presentation actions: effects, sounds, UI; server-only logic).
#include <algorithm>
#include <format>
#include <unordered_map>

#include "statescript/capabilities.h"
#include "statescript/runtime.h"
#include "match.h"

namespace ss {

namespace {

constexpr uint32_t kSetVar = 0x4698e4b6, kConditionBool = 0xb1032daf, kPlayScript = 0xf9ef59a6, kStopScript = 0x85d2552d,
                   kArrayClear = 0x5179b279, kSingleRaycast = 0x757a8a94,
                   kStopAbility = 0xb6853829, kAbilityState = 0xf53c5da8, kNoOp = 0x047f99e4,
                   kSendGameMessage = 0x8b6a3312, kSendGameMessageNetworked = 0x4027e912, kArrayAdd = 0x0cdc2e1a, kArrayRemove = 0xa88eb9de,
                   kArrayContains = 0x5720b660, kCycleIndex = 0x17c9f80b, kTimeout = 0x07722a23,
                   kButtonCondition = 0x4768e4a3, kActionSwitch = 0x321be080, kRepeat = 0xf5a51858;
constexpr uint32_t kOutPlug = Hash("m_outPlug"), kTruePlug = Hash("m_truePlug"), kFalsePlug = Hash("m_falsePlug"),
                   kCondition = Hash("m_condition"), kOutVar = Hash("m_out_Var"), kValue = Hash("m_value"),
                   kIndex = Hash("m_index"), kKey = Hash("m_key");

// Client-presentation actions: nothing to simulate (their out plug still fires). Listed so that the report
// separates them from logic that is genuinely missing.
const std::unordered_map<uint32_t, const char*> kPresentationActions = {
    {0xeb923a6a, "STUStatescriptActionEffect"},
    {0x66b5f581, "STUStatescriptActionGlobalSound"},
    {0xe4d667e6, "STUStatescriptActionDebugText"},
    {0xf8ec9531, "STUStatescriptActionRestartAnim"},  // restarts a playing animation (0x10a4ac0): none on a server
};

const OutPlug* PlugByField(const Node& n, uint32_t field) {
    for (auto& p : n.plugs)
        if (p.field == field && p.index < 0) return &p;
    return nullptr;
}

void CheckArraySize(Instance& inst, int n);

// STUStatescriptActionSetVar (0xf67570) [G]: evaluate m_value, write through m_out_Var with user {0, 0, 1}.
void SetVarAction(Instance& inst, const Node& n) {
    const Graph& g = inst.graph();
    if (g.Object(*n.fields, kIndex) >= 0 || g.Object(*n.fields, kKey) >= 0) {
        inst.sm().warnings.push_back("SetVar with m_index / m_key (array forms) not modelled");
        return;
    }
    EvalContext ctx{&inst};
    int value_cv = g.Object(*n.fields, kValue);
    if (value_cv >= 0 && (g.IsA(value_cv, 0x13ba10f6) || g.IsA(value_cv, 0x8cb6be1c))) {
        // An array / iterable value (DoAction 0xf67570 → vfunc +0x130 = 0xf673c0) [G] (T055): the out var becomes
        // a Dictionary of the iterated {key, value} entries (begin write, clear, append each, commit, size check).
        auto path = g.DynamicPath(g.Object(*n.fields, kOutVar));
        uint64_t id;
        VarBag* bag = path.empty() ? nullptr : inst.WriteBag(path, id);
        Var* var;
        auto* e = bag ? bag->BeginDictionaryWrite(id, VarUser{}, &var) : nullptr;
        if (!e) return;
        auto entries = Iterate(ctx, value_cv);
        e->assign(entries.begin(), entries.end());
        bag->CommitDictionary(*var);
        CheckArraySize(inst, int(entries.size()));
        return;
    }
    Primitive v;
    if (!Evaluate(ctx, value_cv, v)) return;
    auto path = g.DynamicPath(g.Object(*n.fields, kOutVar));
    if (!path.empty()) inst.SetVar(path, v);
}

// STUStatescriptActionPlayScript (client class 0x15dbb80, execute 0xd79c90) [G][L]: m_script = graph GUID,
// m_targets = the entities (iterable config var; only a single entity is modelled), m_params = {key, value};
// each target's sync manager plays the graph (SyncManager::PlayScript). Mirrored instances don't play.
void PlayScriptAction(Instance& inst, const Node& n) {
    const Graph& g = inst.graph();
    if (inst.flags() & Instance::kMirrored) return;
    EvalContext ctx{&inst};
    uint64_t guid = EvaluateGuid(ctx, g.Object(*n.fields, Hash("m_script")));
    if (!guid) return;
    std::vector<std::pair<uint64_t, Primitive>> params;
    for (int p : g.Objects(*n.fields, Hash("m_params"))) {
        if (p < 0) continue;
        Primitive v;
        if (Evaluate(ctx, g.Object(g.value(p), kValue), v))
            params.emplace_back(Graph::RefGuid(g.value(p).Find(0xc76b6366)), v);
    }
    uint32_t target = EvaluateEntity(ctx, g.Object(*n.fields, Hash("m_targets")));
    SyncManager& sm = inst.sm();
    SyncManager* tsm = target == sm.owner() ? &sm : sm.entity_resolver && !IsInvalidEntity(target) ? sm.entity_resolver(target) : nullptr;
    if (!tsm) return;
    uint32_t stacking = 0;
    if (auto* sb = n.Field(Hash("m_stackingBehavior"))) stacking = sb->AsU32();
    tsm->PlayScript(guid, params, sm.owner(), stacking);
}

// STUStatescriptActionSendGameMessage (8b6a3312, base f9f1e339; client class 0x15db930, DoAction 0xd794f0) and the
// networked variant 4027e912 (same base; DoAction 0xd7abb0) [G] (docs/StatescriptNodes.md §3.2). Message =
// m_gameMessage's GUID; targets = the entities m_targets iterates to; sender = the own entity, instigator =
// 694d2b8b if it yields an entity, else the instance's owner var $1b, else the sender; params = m_params {c76b6366
// key, m_value (iterated: one value per entry modelled [?])}.
// - SendGameMessage: nothing on a mirrored instance; the client delivers to its own entity only (synchronously,
//   every instance); other targets are a server's: delivered like the networked variant [?].
// - 4027e912: runs on the client only for a (instance, node) the server's actions section registered (net-sync
//   +0x130, 0xd09f20); it delivers to every target through the target's sync manager (0xf457c0: every instance,
//   then a nested flush when an entry fired on another entity). The server runs it outright; a client-role
//   runtime has no registration and does nothing.
void SendGameMessageAction(Instance& inst, const Node& n, bool networked) {
    SyncManager& sm = inst.sm();
    if (networked ? sm.role() == Role::Client : (inst.flags() & Instance::kMirrored) != 0) return;
    const Graph& g = inst.graph();
    EvalContext ctx{&inst};
    GameMessage msg;
    msg.id = EvaluateGuid(ctx, g.Object(*n.fields, Hash("m_gameMessage")));
    if (!msg.id) return;
    std::vector<uint32_t> targets;
    for (auto& [k, v] : Iterate(ctx, g.Object(*n.fields, Hash("m_targets"))))
        if (v.type == PrimType::Entity && !IsInvalidEntity(v.AsU32())) targets.push_back(v.AsU32());
    if (targets.empty()) return;
    msg.sender = sm.owner();
    uint32_t instigator = kInvalidEntity;
    Primitive owner;
    uint64_t path[1] = {varid::Owner};
    if (inst.GetVar(path, owner) && owner.type == PrimType::Entity) instigator = owner.AsU32();
    if (int cv = g.Object(*n.fields, 0x694d2b8b); cv >= 0) {
        Primitive e;
        if (Evaluate(ctx, cv, e)) instigator = e.type == PrimType::Entity ? e.AsU32() : kInvalidEntity;
    }
    msg.instigator = IsInvalidEntity(instigator) ? msg.sender : instigator;
    for (int p : g.Objects(*n.fields, Hash("m_params"))) {
        if (p < 0) continue;
        uint64_t key = Graph::RefGuid(g.value(p).Find(0xc76b6366));
        if (!key) continue;
        for (auto& [k, v] : Iterate(ctx, g.Object(g.value(p), kValue))) msg.params.emplace_back(key, v);
    }
    bool self = false;
    for (uint32_t t : targets) {
        if (t == sm.owner()) {
            if (networked) sm.DeliverGameMessageFrom(msg, false);
            else self = true;
        } else if (SyncManager* tsm = sm.entity_resolver ? sm.entity_resolver(t) : nullptr) {
            tsm->DeliverGameMessageFrom(msg, true);
        }
    }
    if (self) sm.DeliverGameMessage(msg);
}

// ---- arrays (Dictionary vars; docs/StatescriptNodes.md §3.3) [G] ----

bool NumericKey(const Primitive& k) { return k.type == PrimType::Byte || k.type == PrimType::Int || k.type == PrimType::Float; }

// The out array's path, when m_out_Array is a Dynamic config var.
std::vector<uint64_t> ArrayPath(const Graph& g, const Node& n) {
    int cv = g.Object(*n.fields, Hash("m_out_Array"));
    return cv >= 0 && g.type_of(cv) == 0xf8cc195d ? g.DynamicPath(cv) : std::vector<uint64_t>{};
}

// The elements of m_value, unless m_value is the array being written (the client's iterator stops at the target's
// own entries).
std::vector<DictEntry> Elements(Instance& inst, int value_cv, const Var* target) {
    const Graph& g = inst.graph();
    if (target && g.type_of(value_cv) == 0xf8cc195d && inst.FindDictionary(g.DynamicPath(value_cv)) == target) return {};
    EvalContext ctx{&inst};
    return Iterate(ctx, value_cv);
}

// The array size limit (0xf51590): more than 200 entries is a graph violation (logged; the add still happened).
void CheckArraySize(Instance& inst, int n) {
    if (n > 200) inst.sm().warnings.push_back(std::format("graph {:016x}: array larger than 200", inst.graph().guid()));
}

// Keyed set (bag +0x130 → 0xf60cd0): an entry with an equal non-numeric key is overwritten in place when it sits
// at `index`, else removed; then {key, value} is appended (index < 0 or past the end) or inserted at `index`.
void DictSetKeyed(Instance& inst, std::span<const uint64_t> path, const Primitive& key, const Primitive& value, int index) {
    uint64_t id;
    VarBag* bag = inst.WriteBag(path, id);
    Var* var;
    auto* e = bag ? bag->BeginDictionaryWrite(id, VarUser{}, &var) : nullptr;
    if (!e) return;
    int found = -1;
    if (!NumericKey(key))
        for (size_t i = 0; i < e->size(); i++)
            if (LooselyEqual((*e)[i].first, key)) {
                found = int(i);
                break;
            }
    if (found >= 0 && found == index) {
        (*e)[found].second = value;
    } else {
        if (found >= 0) e->erase(e->begin() + found);
        if (index < 0 || index >= int(e->size())) e->emplace_back(key, value);
        else e->insert(e->begin() + index, {key, value});
    }
    bag->CommitDictionary(*var);
    CheckArraySize(inst, int(var->dict_size()));
}

// STUStatescriptActionArrayAdd (0cdc2e1a; execute 0xf67fb0, add 0xf68200).
void ArrayAddAction(Instance& inst, const Node& n) {
    const Graph& g = inst.graph();
    auto path = ArrayPath(g, n);
    int value_cv = g.Object(*n.fields, kValue), key_cv = g.Object(*n.fields, kKey);
    if (path.empty() || value_cv < 0) return;
    EvalContext ctx{&inst};
    int index = EvaluateInt(ctx, g.Object(*n.fields, kIndex), -1);
    if (index > 200) {
        CheckArraySize(inst, index);
        return;
    }
    if (key_cv >= 0) {
        Primitive key, value;
        if (!Evaluate(ctx, key_cv, key)) key = Primitive::Int(0);
        if (!Evaluate(ctx, value_cv, value)) value = Primitive::Int(0);
        DictSetKeyed(inst, path, key, value, index);
        return;
    }
    uint64_t id;
    VarBag* bag = inst.WriteBag(path, id);
    Var* var;
    auto* e = bag ? bag->BeginDictionaryWrite(id, VarUser{}, &var) : nullptr;
    if (!e) return;
    bool unique = n.Field(0xa7e46ea2) && n.Field(0xa7e46ea2)->AsU8();
    bool insert = index >= 0 && index < int(e->size());
    if (index > int(e->size())) e->resize(size_t(index), {Primitive::Int(0), Primitive::Int(0)});  // 0xcf2010 [?: the pad value]
    for (auto& el : Elements(inst, value_cv, var)) {
        if (NumericKey(el.first)) {
            if (unique && std::any_of(e->begin(), e->end(), [&](auto& x) { return LooselyEqual(x.second, el.second); }))
                continue;
        } else {
            auto it = std::find_if(e->begin(), e->end(), [&](auto& x) { return !NumericKey(x.first) && LooselyEqual(x.first, el.first); });
            if (it != e->end()) {
                it->second = el.second;
                continue;
            }
        }
        if (insert) e->insert(e->begin() + index++, el);
        else e->push_back(el);
    }
    bag->CommitDictionary(*var);
    CheckArraySize(inst, int(var->dict_size()));
}

// STUStatescriptActionArrayRemove (a88eb9de; execute 0xf684e0, remove 0xf68730; keyed: bag +0x138 → 0xf60ee0).
void ArrayRemoveAction(Instance& inst, const Node& n) {
    const Graph& g = inst.graph();
    auto path = ArrayPath(g, n);
    int value_cv = g.Object(*n.fields, kValue), key_cv = g.Object(*n.fields, kKey), index_cv = g.Object(*n.fields, kIndex);
    if (path.empty() || (key_cv < 0 && value_cv < 0 && index_cv < 0)) return;
    EvalContext ctx{&inst};
    uint64_t id;
    VarBag* bag = inst.WriteBag(path, id);
    if (!bag) return;
    Var* var;
    if (key_cv >= 0) {  // an existing Dictionary only: entries with that key (and that value)
        Primitive key, value;
        if (!Evaluate(ctx, key_cv, key)) key = Primitive::Int(0);
        bool by_value = value_cv >= 0;
        if (by_value && !Evaluate(ctx, value_cv, value)) value = Primitive::Int(0);
        auto* e = bag->BeginDictionaryWrite(id, VarUser{}, &var, /*existing_only=*/true);
        if (!e) return;
        std::erase_if(*e, [&](auto& x) { return LooselyEqual(x.first, key) && (!by_value || LooselyEqual(x.second, value)); });
        bag->CommitDictionary(*var);
        return;
    }
    auto* e = bag->BeginDictionaryWrite(id, VarUser{}, &var);
    if (!e) return;
    int index = EvaluateInt(ctx, index_cv, -1);
    if (index < 0) {
        if (value_cv >= 0)
            for (auto& el : Elements(inst, value_cv, var))
                std::erase_if(*e, [&](auto& x) { return LooselyEqual(x.second, el.second); });
    } else if (index < int(e->size())) {
        e->erase(e->begin() + index);
    }
    bag->CommitDictionary(*var);
}

// STUStatescriptCondition_ArrayContains (5720b660; evaluate 0xf701f0): the array edfb51d3 (iterated) has an entry
// with that value (m_value only), that key (m_key only) or both; loose ==; neither → false.
bool ArrayContains(Instance& inst, const Node& n) {
    const Graph& g = inst.graph();
    int array_cv = g.Object(*n.fields, 0xedfb51d3), key_cv = g.Object(*n.fields, kKey), value_cv = g.Object(*n.fields, kValue);
    if (array_cv < 0 || (key_cv < 0 && value_cv < 0)) return false;
    EvalContext ctx{&inst};
    Primitive key, value;
    if (key_cv >= 0 && !Evaluate(ctx, key_cv, key)) return false;
    if (value_cv >= 0 && !Evaluate(ctx, value_cv, value)) return false;
    for (auto& el : Iterate(ctx, array_cv))
        if ((key_cv < 0 || LooselyEqual(el.first, key)) && (value_cv < 0 || LooselyEqual(el.second, value))) return true;
    return false;
}

// STUStatescriptActionCycleIndex (17c9f80b; execute 0xf689e0): count = m_count; 1…200: from the current
// m_out_Index (clamped to [0, count − 1]) step by +1 (or −1 when 188934cd), wrapping, until m_condition holds for
// the candidate, which a value provider answers as 0x0A4…0C while the condition is evaluated; the first such
// candidate becomes m_out_Index; none after a full cycle (or count outside 1…200): aca9b916 (default −1).
void CycleIndexAction(Instance& inst, const Node& n) {
    const Graph& g = inst.graph();
    EvalContext ctx{&inst};
    int out_cv = g.Object(*n.fields, Hash("m_out_Index"));
    auto path = out_cv >= 0 ? g.DynamicPath(out_cv) : std::vector<uint64_t>{};
    auto write = [&](int32_t v) {
        if (!path.empty()) inst.SetVar(path, Primitive::Int(v));
    };
    int count = EvaluateInt(ctx, g.Object(*n.fields, Hash("m_count")), 0);
    int fallback_cv = g.Object(*n.fields, 0xaca9b916);
    if (count < 1 || count > 200) {
        CheckArraySize(inst, count);
        write(EvaluateInt(ctx, fallback_cv, -1));
        return;
    }
    int start = EvaluateInt(ctx, out_cv, 0);
    start = start < 0 ? 0 : std::min(start, count - 1);
    int dir = EvaluateBool(ctx, g.Object(*n.fields, 0x188934cd), false) ? -1 : 1;
    int cond_cv = g.Object(*n.fields, kCondition);
    int cur = start;
    SyncManager& sm = inst.sm();
    sm.value_providers.push_back([&cur](uint64_t id, Primitive& out) {
        if (id != 0x0A4000000000000C) return false;
        out = Primitive::Int(cur);
        return true;
    });
    bool found = false;
    do {
        int next = cur + dir;
        cur = next >= count ? 0 : next < 0 ? count - 1 : next;
        if (cond_cv >= 0) {
            Primitive c;
            if (Evaluate(ctx, cond_cv, c) && c.Truthy()) {
                sm.value_providers.pop_back();
                write(cur);
                found = true;
                break;
            }
        }
    } while (cur != start);
    if (!found) {
        sm.value_providers.pop_back();
        write(EvaluateInt(ctx, fallback_cv, -1));
    }
}

// STUStatescriptCondition_Timeout (07722a23; evaluate 0xf6ff00) [G]: t = c4630712 (s; default the client's
// constant, < 0 → false); d = round(t × 1000) ms. Evaluated for a source state: true once d ms have passed since
// its begin; else its Timer (data 0) is re-armed at begin + d (the timer re-evaluates its exits) → false. No
// source state → false.
bool TimeoutCondition(State* src, Instance& inst, const Node& n) {
    EvalContext ctx{&inst};
    float t = EvaluateFloat(ctx, inst.graph().Object(*n.fields, 0xc4630712), 0.0f);
    if (t < 0.0f || !src) return false;
    float f = t * 1000.0f;
    int32_t d = int32_t(f >= 0.0f ? f + 0.5f : f - 0.5f);
    int64_t elapsed = std::max<int64_t>(0, inst.now() - src->begin_time());
    if (d <= elapsed) return true;
    src->RearmTimer(src->begin_time() + d, 0);
    return false;
}

// STUStatescriptCondition_LogicalButton (4768e4a3; evaluate 0xf70040) [G]: the button (m_logicalButton, −1) is the
// latest notified one and it went down (30c0a41d) / up (86cd8af0) → true; else held (a3e7d1e9) / not held
// (b4015d42) → true.
bool LogicalButtonCondition(Instance& inst, const Node& n) {
    const Graph& g = inst.graph();
    int cv = g.Object(*n.fields, Hash("m_logicalButton"));
    if (cv < 0) return false;
    EvalContext ctx{&inst};
    int32_t b = EvaluateInt(ctx, cv, -1);
    SyncManager& sm = inst.sm();
    auto flag = [&](uint32_t h) { return n.Field(h) && n.Field(h)->AsU8(); };
    if (b == sm.last_button && ((flag(0x30c0a41d) && sm.last_down) || (flag(0x86cd8af0) && !sm.last_down))) return true;
    bool held = sm.Held(b);
    return (flag(0xa3e7d1e9) && held) || (flag(0xb4015d42) && !held);
}

// STUStatescriptActionSwitch (321be080; execute 0xf69070) [G]: every case plug of 7d662124 (+0x88, 57e931d8 plugs
// with a +0x28 m_value) whose value equals m_switchValue (loose ==) is traversed with its index; the first only
// unless b1c64d7f; none → the default plug 8ae7d668 (+0xA8). Then the action's out plug as usual.
bool ActionSwitch(State* src, Instance& inst, const Node& n) {
    const Graph& g = inst.graph();
    EvalContext ctx{&inst};
    bool all = EvaluateBool(ctx, g.Object(*n.fields, 0xb1c64d7f), false);
    Primitive v;
    Evaluate(ctx, g.Object(*n.fields, Hash("m_switchValue")), v);
    bool any = false;
    for (const OutPlug* p : n.PlugList(0x88)) {
        Primitive c;
        if (p->value >= 0) Evaluate(ctx, p->value, c);
        if (!LooselyEqual(c, v)) continue;
        inst.FirePlug(src, n, p, /*indexed=*/true);
        any = true;
        if (!all) break;
    }
    if (!any)
        for (auto& p : n.plugs)
            if (p.field == 0x8ae7d668 && p.index < 0) inst.FirePlug(src, n, &p);
    return any;
}

// STUStatescriptActionRepeat (f5a51858; execute 0xf68de0) [G]: count = m_count; for i in 0…count − 1, with a value
// provider answering 0x0A4…0C = INT i: m_out_Var := m_value, then the body plug 7bac6a4e (+0xA0) is traversed.
// Its Execute replaces the base one: no DoAction, no "action executed" record, no out plug; returns false.
void RepeatAction(State* src, Instance& inst, const Node& n) {
    const Graph& g = inst.graph();
    EvalContext ctx{&inst};
    int count = EvaluateInt(ctx, g.Object(*n.fields, Hash("m_count")), 0);
    int i = 0;
    SyncManager& sm = inst.sm();
    sm.value_providers.push_back([&i](uint64_t id, Primitive& out) {
        if (id != 0x0A4000000000000C) return false;
        out = Primitive::Int(i);
        return true;
    });
    auto path = g.DynamicPath(g.Object(*n.fields, kOutVar));
    const OutPlug* body = nullptr;
    for (auto& p : n.plugs)
        if (p.field == 0x7bac6a4e && p.index < 0) body = &p;
    for (; i < count; i++) {
        Primitive v;
        if (!path.empty() && Evaluate(ctx, g.Object(*n.fields, kValue), v)) inst.SetVar(path, v);
        inst.FirePlug(src, n, body);
    }
    sm.value_providers.pop_back();
}

}  // namespace

// STUStatescriptAndGate (138ccab0; class 0x1604bd8, execute 0xf47430, arrivals 0xf47660) [G]
// (docs/StatescriptNodes.md §3.11): an arrival = {gate, source node, plug offset, index} of the traversal that
// reached it, kept for the current timestamp (instance +0x120, cleared when the time changes and on stop). The same
// arrival again → nothing. The first arrival → the states of c77035be (+0x78) that are active (other than the
// source) re-evaluate their exits at now; when 5e756553 (+0x98) distinct arrivals are in: the gate's arrivals are
// dropped, its out plug (+0x90) fires and, if that traversed something, the listed active states are aborted
// (Lifetime abort at the head of the queue). Returns whether it opened.
bool AndGateExecute(State* src, Instance& inst, const Node& n) {
    int64_t now = inst.now();
    if (now != inst.gate_time_) inst.gate_arrivals_.clear(), inst.gate_time_ = now;
    std::array<int16_t, 4> a{int16_t(n.index), int16_t(inst.last_src_), int16_t(inst.last_plug_), int16_t(inst.last_idx_)};
    auto& l = inst.gate_arrivals_;
    int before = 0;
    for (auto& x : l) {
        if (x[0] != a[0]) continue;
        if (x == a) return false;
        before++;
    }
    l.push_back(a);
    int need = n.Field(0x5e756553) ? n.Field(0x5e756553)->AsI32() : 0;
    std::vector<int> states;
    if (auto* v = n.Field(0xc77035be); v && v->type == stu::Value::Type::List)
        for (auto& e : v->items) states.push_back(e.AsI32());
    if (before == 0) {
        for (int si : states)
            if (State* s = inst.state(si); s && s->active() && s != src) {
                Notification x = inst.Make(NotifType::EvaluateExits);
                x.state = si;
                inst.QueueSorted(x);
            }
        return false;
    }
    if (before != need - 1) return false;
    std::erase_if(l, [&](auto& x) { return x[0] == a[0]; });
    const OutPlug* out = nullptr;
    for (auto& p : n.plugs)
        if (p.field == Hash("m_outPlug") && p.index < 0) out = &p;
    if (!inst.FirePlug(src, n, out)) return false;
    for (int si : states)
        if (State* s = inst.state(si); s && s->active()) {
            Notification x = inst.Make(NotifType::Lifetime);
            x.lifetime = LifetimeKind::Abort;
            x.state = si;
            inst.QueueHead(x);
        }
    return true;
}

bool Instance::ExecuteNode(State* src, const Node& n) {
    switch (n.kind) {
    case NodeKind::Condition: {
        bool result = false;
        if (n.type == kConditionBool) {
            EvalContext ctx{this};
            result = EvaluateBool(ctx, graph_.Object(*n.fields, kCondition), false);
        } else if (n.type == kArrayContains) {
            result = ArrayContains(*this, n);
        } else if (n.type == kTimeout) {
            result = TimeoutCondition(src, *this, n);
        } else if (n.type == kButtonCondition) {
            result = LogicalButtonCondition(*this, n);
        } else if (MatchCondition(src, *this, n, result)) {  // match / objective families (T055)
        } else {
            sm_.unmodelled_nodes[n.type]++;
        }
        const OutPlug* t = PlugByField(n, kTruePlug);
        const OutPlug* f = PlugByField(n, kFalsePlug);
        bool traversed = FirePlug(src, n, result ? t : f);
        bool unlinked = (!t || t->links.empty()) && (!f || f->links.empty());
        return traversed || unlinked;
    }
    case NodeKind::Action:
    case NodeKind::Terminator:
        // Execute (0xf47740): DoAction, then "action executed" (sm vfunc +0x158: a record for the sync's actions
        // section, mirrored instances only) [G], then the out plug.
        if (n.type == kRepeat) {
            RepeatAction(src, *this, n);
            return false;
        }
        if (n.type == kStopScript) {  // 0xf68cb0 replaces Execute: request the stop, return 0, no record [G][L]
            RequestStop();
            return false;
        }
        if (n.type == kSetVar) SetVarAction(*this, n);
        else if (n.type == kPlayScript) PlayScriptAction(*this, n);
        else if (n.type == kSendGameMessage) SendGameMessageAction(*this, n, false);
        else if (n.type == kSendGameMessageNetworked) SendGameMessageAction(*this, n, true);
        else if (n.type == kArrayAdd) ArrayAddAction(*this, n);
        else if (n.type == kArrayRemove) ArrayRemoveAction(*this, n);
        else if (n.type == kCycleIndex) CycleIndexAction(*this, n);
        else if (n.type == kActionSwitch) ActionSwitch(src, *this, n);
        else if (n.type == kStopAbility) {
            // STUStatescriptActionStopAbility (0x1089c50) [G]: a Timer (data 3 = stop now) at now for every
            // existing Ability state object of the instance [L: Genji dash, 0x1db state 1 at CF 9361].
            if (!stopped())
                for (size_t i = 0; i < states_.size(); i++)
                    if (states_[i] && graph_.IsA(graph_.state_node(int(i)).instance, kAbilityState)) {
                        Notification t = Make(NotifType::Timer);
                        t.state = int(i);
                        t.data = 3;
                        QueueSorted(t);
                    }
        }
        else if (n.type == kSingleRaycast) {
            // STUStatescriptActionSingleRaycast (0x107b310) [G]: the cast is the host's (SyncManager::raycast); then
            // m_out_hitSomething, m_out_hitEntity (null on a miss or for static geometry), and on a hit only
            // m_out_HitPointWS / m_out_hitNormal (VEC3), in that order. Without a host every cast misses.
            const Graph& g = graph_;
            auto out = [&](const char* f, const Primitive& v) {
                auto path = g.DynamicPath(g.Object(*n.fields, Hash(f)));
                if (!path.empty()) SetVar(path, v);
            };
            SyncManager::RaycastHit hit;
            bool hits = sm_.raycast && sm_.raycast(id_, n.index, hit);
            out("m_out_hitSomething", Primitive::Byte(hits));
            out("m_out_hitEntity", Primitive::Entity(hits ? hit.entity : 0));
            if (hits) {
                out("m_out_HitPointWS", Primitive::Vec3(hit.point[0], hit.point[1], hit.point[2]));
                out("m_out_hitNormal", Primitive::Vec3(hit.normal[0], hit.normal[1], hit.normal[2]));
            }
            if (!sm_.raycast) sm_.unmodelled_nodes[n.type]++;
        }
        else if (n.type == kArrayClear) {  // 0xf68940 [G][L]: the out var becomes an empty Dictionary
            std::vector<uint64_t> path = graph_.DynamicPath(graph_.Object(*n.fields, Hash("m_out_Array")));
            std::span<const uint64_t> rest = path;
            if (VarBag* bag = path.empty() ? nullptr : ResolveBag(rest, true); bag && rest.size() == 1)
                bag->ClearDictionary(rest[0]);
        }
        else if (MatchAction(src, *this, n)) {}  // match / objective families (T055)
        else if (IsHostType(n.type)) sm_.RecordHostEvent(*this, n, 0);
        else if (n.type != kNoOp) sm_.unmodelled_nodes[n.type]++;  // NoOp: the base action (out plug only)
        sm_.RecordAction(*this, n.index);
        if (n.kind == NodeKind::Action) FirePlug(src, n, PlugByField(n, kOutPlug));
        return true;
    case NodeKind::Entry: FirePlug(src, n, PlugByField(n, kOutPlug)); return true;
    default:
        if (n.type == 0x138ccab0) return AndGateExecute(src, *this, n);
        sm_.unmodelled_nodes[n.type]++;
        return false;
    }
}

bool IsPresentationAction(uint32_t type) { return kPresentationActions.count(type) != 0; }

}  // namespace ss
