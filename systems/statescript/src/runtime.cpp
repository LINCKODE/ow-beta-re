#include "statescript/runtime.h"

#include <algorithm>
#include <bit>
#include <format>

namespace ss {

namespace {
constexpr uint32_t kOnBeginPlug = Hash("m_onBeginPlug"), kOnEndPlug = Hash("m_onEndPlug"),
                   kSubgraphPlug = Hash("m_subgraphPlug"), kTransitionPlug = Hash("m_transitionPlug"),
                   kExclusive = 0x065d841c, kSubscribeList0 = 0x652a186c, kSubscribeList1 = 0x3882159e,
                   kEntryOut = Hash("m_outPlug"), kGameMessage = Hash("m_gameMessage"), kParams = Hash("m_params"),
                   kParamKey = 0xc76b6366, kOutVar = Hash("m_out_Var"), kOutSender = Hash("m_out_Sender"),
                   kOutInstigator = Hash("m_out_Instigator"), kFilter = Hash("m_filter"), kOneShot = 0xeb7f6ab1;
constexpr int kRunawayLimit = 1000;
constexpr int kSameTimePassLimit = 20000;

std::string NotifText(const Notification& n) {
    static const char* kLife[] = {"begin", "finish", "abort"};
    std::string s;
    switch (n.type) {
    case NotifType::Lifetime: s = std::format("lifetime {} state {}", kLife[int(n.lifetime)], n.state); break;
    case NotifType::Timer: s = std::format("timer state {} data {}", n.state, n.data); break;
    case NotifType::EvaluateExits: s = std::format("evalexits state {}", n.state); break;
    case NotifType::TraverseFrom: s = std::format("traversefrom node {}", n.node); break;
    case NotifType::SetVar:
        s = std::format("setvar inst {} var {:x} = {}", n.target, n.var & 0xFFFF, ToTraceText(n.value));
        break;
    case NotifType::SetVarArray: s = std::format("setvararray inst {} var {:x}", n.target, n.var & 0xFFFF); break;
    case NotifType::StopInstance: s = "stopinstance"; break;
    case NotifType::LogicalButton: s = std::format("button {} down {}", n.data, n.state); break;
    case NotifType::AttrDependencyChanged: s = std::format("depchanged state {}", n.state); break;
    }
    return s + std::format(" flags {:x}", n.flags);
}

// Dynamic path of a SetVar target as (instance id, var id): [var] -> this instance, [0x…04, var] -> entity bag.
bool SimpleTarget(const std::vector<uint64_t>& path, int16_t self, int16_t& target, uint64_t& var) {
    if (path.size() == 1) {
        target = self;
        var = path[0];
        return true;
    }
    if (path.size() == 2 && path[0] == varid::EntityBag) {
        target = 0;
        var = path[1];
        return true;
    }
    if (path.size() == 2 && path[0] == varid::InstanceBag) {
        target = self;
        var = path[1];
        return true;
    }
    return false;
}
}  // namespace

// ==== State =============================================================================================

State::State(Instance& inst, int state_index) : inst_(inst), index_(state_index) {}
State::~State() = default;

const Node& State::node() const { return inst_.graph().state_node(index_); }
int64_t State::now() const { return inst_.now(); }
EvalContext State::Ctx() const { return EvalContext{&inst_}; }

bool State::Execute(State* src, const Link& link) {
    if (link.abort()) {
        ScheduleLifetime(LifetimeKind::Abort);
    } else if (!HandlesInput(link)) {
        ScheduleLifetime(LifetimeKind::Begin);
        if (src == this) flags_ |= kSelfTransition;
    }
    return true;
}

void State::DoBegin(int64_t time_offset) {
    SyncManager& sm = inst_.sm();
    bool reenter = active();
    changes_++;
    sm.Emit({.kind = reenter ? "reenter" : "begin", .instance = inst_.id(), .a = index_, .b = node().index,
             .c = time_offset});
    if (reenter) {
        OnReenter();
        return;
    }
    serial_++;
    last_tick_ = -1;
    // Exclusive states (node +0x98): aborted now, their notifications of this time dropped (0xf485e0).
    if (auto* ex = node().Field(kExclusive); ex && ex->type == stu::Value::Type::List)
        for (auto& e : ex->items) {
            int si = e.AsI32();
            if (State* s = inst_.state(si)) {
                s->DoAbort();
                inst_.DropStateNotifications(si, true);
            }
        }
    flags_ = (flags_ & ~kExitTaken) | kActive;
    begin_time_ = now() - time_offset;
    Subscribe();
    OnActivate(false);
    OnBeginPre();
    FirePlugField(kOnBeginPlug);
    FirePlugField(kSubgraphPlug);
    OnBegin();
    EvaluateExits();
}

void State::DoFinish() { End(true); }
void State::DoAbort() { End(false); }

void State::End(bool finish) {
    if (!active() || (flags_ & kEnding)) return;
    SyncManager& sm = inst_.sm();
    sm.Emit({.kind = finish ? "finish" : "abort", .instance = inst_.id(), .a = index_, .b = node().index});
    flags_ |= kEnding;
    changes_++;
    inst_.DropStateNotifications(index_);
    if (const OutPlug* sub = node().Plug(0xF0)) AbortStates(sub->subgraph);  // children end first (0xf484e0)
    if (finish) OnFinishHook();
    else OnAbortHook();
    OnEndHook();
    OnDeactivate(finish ? 1 : 0);
    if (finish) TraverseFinished();
    else TraverseAborted();
    FirePlugField(kOnEndPlug);
    Unsubscribe();
    flags_ &= ~(kActive | kEnding | kSelfTransition);
    end_time_ = now();
    SetTicking(false);
    sm.Emit({.kind = finish ? "finish_done" : "abort_done", .instance = inst_.id(), .a = index_});
}

bool State::CheckExits() {
    for (const OutPlug* p : node().PlugList(0x88))
        if (p->field == kTransitionPlug && FirePlugIndexed(p)) return true;
    return false;
}

void State::EvaluateExits() {
    if (flags_ & kExitTaken) return;
    bool taken = CheckExits();
    if (taken && !inst_.stopped() && !(flags_ & kSelfTransition)) {
        flags_ |= kExitTaken;
        Notification n = inst_.Make(NotifType::Lifetime);
        n.lifetime = LifetimeKind::Abort;
        n.state = index_;
        inst_.QueueExitAbort(n);
        inst_.DropLifetime(index_, LifetimeKind::Finish, now());
    }
    flags_ &= ~kSelfTransition;  // [?] where the client clears 0x4 is not identified
}

void State::OnVarDependencyChanged(Var&) {
    if (!active() || inst_.stopped()) return;
    Notification n = inst_.Make(NotifType::EvaluateExits);
    n.state = index_;
    inst_.QueueSorted(n);
}

bool State::FirePlugField(uint32_t field_hash, int index) {
    for (auto& p : node().plugs)
        if (p.field == field_hash && p.index == index) return FirePlug(&p);
    return false;
}

bool State::FirePlug(const OutPlug* plug) { return inst_.FirePlug(this, node(), plug, false); }
bool State::FirePlugIndexed(const OutPlug* plug) { return inst_.FirePlug(this, node(), plug, true); }

void State::AbortStates(std::span<const int32_t> states) {
    for (int32_t si : states)
        if (State* s = inst_.state(si)) s->DoAbort();
}

void State::ScheduleLifetime(LifetimeKind kind, int64_t delay) {
    Notification n = inst_.Make(NotifType::Lifetime);
    n.lifetime = kind;
    n.state = index_;
    n.time = now() + delay;
    inst_.QueueSorted(n);
}

void State::ScheduleTimer(int64_t at, int32_t data, bool at_head) {
    Notification n = inst_.Make(NotifType::Timer);
    n.state = index_;
    n.data = data;
    n.time = at;
    if (at_head) inst_.QueueHead(n);
    else inst_.QueueSorted(n);
}

void State::RemoveTimers(int32_t data) {
    std::erase_if(inst_.queue_, [&](const Notification& n) {
        return n.type == NotifType::Timer && n.state == index_ && n.data == data;
    });
}

void State::SetTicking(bool on) {
    if (on == bool(flags_ & kTicking)) return;
    if (on) flags_ |= kTicking;
    else flags_ &= ~kTicking;
    inst_.SetTicking(*this, on);
}

void State::Subscribe() {
    if (flags_ & kSubscribed) return;
    const Graph& g = inst_.graph();
    const uint32_t lists[2] = {kSubscribeList0, kSubscribeList1};
    for (int l = 0; l < 2; l++) {
        subscribed_[l].clear();
        for (int cv : g.Objects(*node().fields, lists[l])) {
            auto path = g.DynamicPath(cv);
            if (path.empty()) continue;
            inst_.SubscribeVar(path, this, l);
            subscribed_[l].push_back(std::move(path));
        }
    }
    flags_ |= kSubscribed;
}

void State::Unsubscribe() {
    if (!(flags_ & kSubscribed)) return;
    for (int l = 0; l < 2; l++)
        for (auto& p : subscribed_[l]) inst_.UnsubscribeVar(p, this, l);
    flags_ &= ~kSubscribed;
}

// ==== Instance ==========================================================================================

Instance::Instance(SyncManager& sm, const Graph& graph, int16_t id) : sm_(sm), graph_(graph), id_(id) {}
Instance::~Instance() {
    for (auto& s : states_)
        if (s) s->Unsubscribe();
}

int64_t Instance::now() const { return sm_.time(); }

State& Instance::EnsureState(int i) {
    if (i >= (int)states_.size()) states_.resize(graph_.states().size());
    if (!states_[i]) states_[i] = CreateState(*this, i);
    return *states_[i];
}

Notification Instance::Make(NotifType type) const {
    Notification n;
    n.type = type;
    n.time = now();
    n.seq = sm_.NextSeq();
    return n;
}

void Instance::QueueSorted(Notification n) {
    if (n.time < now()) n.time = now();
    auto it = queue_.end();
    while (it != queue_.begin()) {
        auto prev = std::prev(it);
        if (prev->time <= n.time) break;
        it = prev;
    }
    queue_.insert(it, std::move(n));
}

void Instance::QueueHead(Notification n) {
    if (n.time < now()) n.time = now();
    queue_.push_front(std::move(n));
}

void Instance::QueueExitAbort(Notification n) {
    // Head of the queue, or right behind the notifications of this time created earlier in the pass.
    auto pos = queue_.begin();
    for (auto it = queue_.begin(); it != queue_.end() && it->time <= n.time; ++it)
        if (it->seq < sm_.pass_seq_start_) pos = std::next(it);
    queue_.insert(pos, std::move(n));
}

void Instance::DropStateNotifications(int state, bool only_now) {
    std::erase_if(queue_, [&](const Notification& n) {
        bool of_state = (n.type == NotifType::Lifetime || n.type == NotifType::Timer ||
                         n.type == NotifType::EvaluateExits || n.type == NotifType::AttrDependencyChanged) &&
                        n.state == state;
        return of_state && (!only_now || n.time <= now());
    });
}

void Instance::DropLifetime(int state, LifetimeKind kind, int64_t time) {
    std::erase_if(queue_, [&](const Notification& n) {
        return n.type == NotifType::Lifetime && n.state == state && n.lifetime == kind && n.time == time;
    });
}

void Instance::SetTicking(State& s, bool on) {
    if (on) {
        if (std::find(ticking_.begin(), ticking_.end(), &s) == ticking_.end()) ticking_.push_back(&s);
    } else {
        std::erase(ticking_, &s);
    }
}

bool Instance::NodeAllowed(const Node& n) const {
    if (sm_.role() == Role::Client) return !n.server_only;  // vfunc +0x88 for an instance that runs locally
    return !n.client_only;
}

void Instance::Instantiate() {
    sm_.Emit({.kind = "instantiate", .instance = id_, .a = int64_t(graph_.guid()), .b = parent_});
    flags_ |= kInstantiated | kProcessed;
    created_ = now();
    states_.resize(graph_.states().size());
    entry_fired_.assign(graph_.entries().size(), 0);
    size_t fired = 0;
    for (size_t i = 0; i < graph_.entries().size(); i++) {
        const Node& e = graph_.node(graph_.entries()[i]);
        if (e.type != stype::Entry || !NodeAllowed(e)) continue;  // GameMessage / PlayScript entries wait
        entry_fired_[i] = 1;
        fired++;
        Notification n = Make(NotifType::TraverseFrom);
        n.node = e.index;
        QueueSorted(n);
    }
    if (fired == entry_fired_.size()) flags_ |= kAllEntriesFired;
    sm_.Emit({.kind = "instantiate_done", .instance = id_, .a = flags_, .b = int64_t(queue_.size())});
}

void Instance::QueueLogicalButton(int32_t button, bool down, int64_t time) {
    Notification n = Make(NotifType::LogicalButton);
    n.time = time;
    n.data = button;
    n.state = down ? 1 : 0;
    QueueSorted(n);
}

void Instance::InstantiateImported() {
    sm_.Emit({.kind = "instantiate", .instance = id_, .a = int64_t(graph_.guid()), .b = parent_});
    flags_ |= kInstantiated | kProcessed;
    created_ = now();
    states_.resize(graph_.states().size());
    entry_fired_.assign(graph_.entries().size(), 0);
    size_t fired = 0;
    for (size_t i = 0; i < graph_.entries().size(); i++)
        if (graph_.node(graph_.entries()[i]).type == stype::Entry) entry_fired_[i] = 1, fired++;
    if (fired == entry_fired_.size()) flags_ |= kAllEntriesFired;
    sm_.Emit({.kind = "instantiate_done", .instance = id_, .a = flags_, .b = int64_t(queue_.size())});
}

void State::CheckDataChange() {
    BitOut b;
    WriteChangeKey(b);
    if (change_key_bits_ >= 0 && (b.nbits != change_key_bits_ || b.bytes != change_key_)) changes_++;
    change_key_ = std::move(b.bytes);
    change_key_bits_ = b.nbits;
}

void State::Import(const uint8_t* data, int nbits) {
    if (active()) return;
    serial_++;
    last_tick_ = -1;
    flags_ = (flags_ & ~kExitTaken) | kActive;
    begin_time_ = now();
    inst_.sm().Emit({.kind = "import", .instance = inst_.id(), .a = index_, .b = node().index, .c = nbits});
    import_bits_ = data ? nbits : 0;
    import_data_.assign(data, data + (import_bits_ + 7) / 8);
    Subscribe();
    OnImport(data, nbits);
    change_key_bits_ = -1;
    CheckDataChange();  // the imported data is the client's baseline
}

// Stop (0xf500e0) [G]: the stopped flag is set first (so the aborted states' plugs follow no links), every
// active state is aborted, then a SubScript parent state that still owns this child (same serial, same child id)
// forgets it and, unless its node's 916d18ce is set, schedules its abort at now.
void Instance::Stop() {
    if (stopped()) return;
    flags_ |= kStopped;
    gate_arrivals_.clear();
    for (auto& s : states_)
        if (s && s->active()) s->DoAbort();
    queue_.clear();
    ticking_.clear();
    if (parent_ != 0)
        if (Instance* p = sm_.FindInstance(parent_))
            if (State* st = p->state(int(slot_)); st && st->active()) st->ChildStopped(*this);
}

bool Instance::FirePlug(State* src, const Node& from, const OutPlug* plug, bool indexed) {
    if (!plug) return false;
    bool any = false;
    for (const Link& l : plug->links) any |= TraverseLink(src, from, *plug, l, indexed);
    return any;
}

bool Instance::TraverseLink(State* src, const Node& from, const OutPlug& plug, const Link& link, bool indexed) {
    sm_.Emit({.kind = "link", .instance = id_, .a = from.index, .b = plug.offset, .c = link.target,
              .idx = indexed ? plug.index : -1});
    bool ret = false;
    if (!stopped() && link.target >= 0) {
        const Node& t = graph_.node(link.target);
        // The traversal being made (instance +0x108 / +0x110 / +0x118), read by AndGate.
        last_src_ = from.index, last_plug_ = int(plug.offset), last_idx_ = indexed ? plug.index : -1;
        if (NodeAllowed(t)) {
            if (t.kind == NodeKind::State) ret = EnsureState(t.state_index).Execute(src, link);
            else ret = ExecuteNode(src, t);
        }
    }
    sm_.Emit({.kind = "link_done", .instance = id_, .a = link.target, .b = ret});
    return ret;
}

bool Instance::ExecuteEntry(int node) {
    const Node& n = graph_.node(node);
    if (n.kind != NodeKind::Entry) return false;
    return FirePlug(nullptr, n, n.Plug(0x78));
}

int Instance::DeliverMessage(const GameMessage& msg) {
    int result = 1;
    if (!instantiated() || stopped() || (flags_ & kAllEntriesFired)) {
        sm_.Emit({.kind = "message", .instance = id_, .a = int64_t(msg.id), .b = result, .c = msg.sender,
                  .idx = msg.instigator});
        return result;
    }
    EvalContext ctx{this};
    for (size_t i = 0; i < graph_.entries().size(); i++) {
        if (entry_fired_[i]) continue;
        const Node& e = graph_.node(graph_.entries()[i]);
        if (e.type != stype::EntryGameMessage || !NodeAllowed(e)) continue;
        Primitive id;
        if (!Evaluate(ctx, graph_.Object(*e.fields, kGameMessage), id) || id.bits != msg.id) continue;
        if (graph_.Object(*e.fields, kFilter) >= 0) sm_.unmodelled_config_vars[graph_.type_of(graph_.Object(*e.fields, kFilter))]++;
        auto queue_setvar = [&](int out_cv, const Primitive& value) {
            if (out_cv < 0) return;
            int16_t target;
            uint64_t var;
            if (!SimpleTarget(graph_.DynamicPath(out_cv), id_, target, var)) {
                sm_.warnings.push_back("game message out-var with a complex path");
                return;
            }
            Notification n = Make(NotifType::SetVar);
            n.flags = kNotifDefault | kNotifFromMessage;
            n.target = target;
            n.var = var;
            n.value = value;
            QueueSorted(n);
        };
        for (int p : graph_.Objects(*e.fields, kParams)) {
            if (p < 0) continue;
            uint64_t key = Graph::RefGuid(graph_.value(p).Find(kParamKey));
            for (auto& [k, v] : msg.params)
                if (k == key) queue_setvar(graph_.Object(graph_.value(p), kOutVar), v);
        }
        queue_setvar(graph_.Object(*e.fields, kOutSender), Primitive::Entity(msg.sender));
        queue_setvar(graph_.Object(*e.fields, kOutInstigator), Primitive::Entity(msg.instigator));
        if (auto* os = e.Field(kOneShot); os && os->AsU8()) entry_fired_[i] = 1;
        Notification n = Make(NotifType::TraverseFrom);
        n.node = e.index;
        QueueSorted(n);
        result = 2;
    }
    sm_.Emit({.kind = "message", .instance = id_, .a = int64_t(msg.id), .b = result, .c = msg.sender,
              .idx = msg.instigator});
    return result;
}

VarBag* Instance::ResolveBag(std::span<const uint64_t>& path, bool create) const {
    VarBag* bag = const_cast<VarBag*>(&bag_);
    while (path.size() > 1) {
        uint64_t id = path[0];
        if (id == varid::InstanceBag) {
            bag = const_cast<VarBag*>(&bag_);
            path = path.subspan(1);
        } else if (id == varid::EntityBag) {
            bag = &sm_.entity_bag();
            path = path.subspan(1);
        } else if (id == varid::ChildBag) {
            bag = create ? &bag->Child(path[1]) : bag->FindChild(path[1]);
            if (!bag) return nullptr;
            path = path.subspan(2);
        } else {
            break;
        }
    }
    return path.empty() ? nullptr : bag;
}

Var* Instance::FindVar(std::span<const uint64_t> path, bool create, int16_t* owner) {
    VarBag* bag = ResolveBag(path, create);
    if (!bag || path.size() != 1) return nullptr;
    if (owner) {
        *owner = 0;
        for (auto& i : sm_.instances_)
            if (&i->bag() == bag) *owner = i->id();
    }
    return create ? &bag->GetOrCreate(path[0]) : bag->Find(path[0]);
}

const Var* Instance::FindDictionary(std::span<const uint64_t> path) const {
    VarBag* bag = ResolveBag(path, false);
    if (!bag || path.size() != 1) return nullptr;
    const Var* v = bag->Find(path[0]);
    return v && v->kind() == Var::Kind::Dictionary ? v : nullptr;
}

VarBag* Instance::WriteBag(std::span<const uint64_t> path, uint64_t& id) {
    VarBag* bag = ResolveBag(path, true);
    if (!bag || path.size() != 1) return nullptr;
    id = path[0];
    return bag;
}

const Var* SyncManager::FindEntityDictionary(uint32_t entity, std::span<const uint64_t> path) {
    if (path.empty() || IsInvalidEntity(entity)) return nullptr;
    SyncManager* other = entity == owner_ ? this : entity_resolver ? entity_resolver(entity) : nullptr;
    if (!other) return nullptr;
    while (path.size() > 1 && (path[0] == varid::EntityBag || path[0] == varid::InstanceBag)) path = path.subspan(1);
    if (path.size() != 1) return nullptr;
    const Var* v = other->entity_bag().Find(path[0]);
    return v && v->kind() == Var::Kind::Dictionary ? v : nullptr;
}

bool Instance::GetVar(std::span<const uint64_t> path, Primitive& out) const {
    VarBag* bag = ResolveBag(path, false);
    return bag && ReadFrom(sm_, bag, path, out);
}

bool Instance::ReadFrom(SyncManager& sm, VarBag* bag, std::span<const uint64_t> path, Primitive& out) {
    Var* v = bag->Find(path[0]);
    if (!v || v->kind() == Var::Kind::Bag) return false;
    if (path.size() == 1) {
        if (v->kind() == Var::Kind::Dictionary) return false;
        // Owned with the state-defined bit: the owning state supplies the value (+0x228; 0xf5e840) [G].
        if (const VarUser& u = v->user(); u.state_defined && !v->stack_key())
            if (Instance* oi = sm.FindInstance(u.instance))
                if (State* os = oi->state(u.state); os && os->active() && os->StateValue(u.extra, out)) return true;
        out = v->value();
        return true;
    }
    uint64_t sub = path[1];
    if (path.size() == 2 && sub == varid::DictSize) {
        out = Primitive::Int(int32_t(v->dict_size()));
        return true;
    }
    if (path.size() == 2 && (sub == varid::X || sub == varid::Y || sub == varid::Z)) {
        const Primitive& p = v->value();
        if (p.type != PrimType::Vec3) return false;
        uint32_t raw = sub == varid::X ? uint32_t(p.bits) : sub == varid::Y ? uint32_t(p.bits >> 32) : p.extra;
        out = Primitive::Make(PrimType::Float, raw);
        return true;
    }
    if (v->value().type == PrimType::Entity) return sm.ReadEntityVar(v->value().AsU32(), path.subspan(1), out);
    sm.warnings.push_back(std::format("unsupported var path read (len {}, first {:x})", path.size(), path[0]));
    return false;
}

bool Instance::PathDependsOnExternal(std::span<const uint64_t> path) const {
    int plain = 0;
    for (uint64_t id : path)
        if (id < varid::Make(0x3) || id > varid::Make(0xB)) plain++;
    if (plain > 1) return true;
    std::span<const uint64_t> rest = path;
    if (VarBag* bag = ResolveBag(rest, false))
        if (Var* v = bag->Find(rest[0])) return v->user().instance != 0 && v->user().priority != 0;
    return false;
}

SetResult Instance::SetVar(std::span<const uint64_t> path, const Primitive& value, const VarUser& user) {
    VarBag* bag = ResolveBag(path, true);
    if (!bag) return SetResult::Rejected;
    if (path.size() == 1) return bag->Set(path[0], value, user);
    sm_.warnings.push_back(std::format("unsupported var path write (len {}, first {:x})", path.size(), path[0]));
    return SetResult::Rejected;
}

void Instance::SubscribeVar(std::span<const uint64_t> path, VarSubscriber* s, int list) {
    VarBag* bag = ResolveBag(path, true);
    if (!bag) return;
    bag->Subscribe(path[0], s, list);  // the first plain var of the path (0xf43bd0)
    // Through an ENTITY var: also in that entity's Statescript (its entity bag), where changes come from [L].
    if (path.size() > 1)
        if (Var* v = bag->Find(path[0]); v && v->value().type == PrimType::Entity) {
            uint32_t e = v->value().AsU32();
            SyncManager* other = e == sm_.owner() ? &sm_ : sm_.entity_resolver ? sm_.entity_resolver(e) : nullptr;
            if (other && !IsInvalidEntity(e)) other->entity_bag().Subscribe(path[1], s, list);
        }
}

void Instance::UnsubscribeVar(std::span<const uint64_t> path, VarSubscriber* s, int list) {
    VarBag* bag = ResolveBag(path, false);
    if (!bag) return;
    bag->Unsubscribe(path[0], s, list);
    if (path.size() > 1)
        if (Var* v = bag->Find(path[0]); v && v->value().type == PrimType::Entity) {
            uint32_t e = v->value().AsU32();
            SyncManager* other = e == sm_.owner() ? &sm_ : sm_.entity_resolver ? sm_.entity_resolver(e) : nullptr;
            if (other) other->entity_bag().Unsubscribe(path[1], s, list);
        }
}

void Instance::Process(uint32_t world_mask) {
    flags_ &= ~kProcessed;
    if (stopped() || !instantiated()) return;
    if (world_mask)
        for (int si : graph_.world_states())
            if (State* s = state(si); s && s->active() && (s->node().world_flags & world_mask)) s->OnWorldChanged();
    while (!queue_.empty() && queue_.front().time <= now()) {
        Notification n = std::move(queue_.front());
        queue_.pop_front();
        if (n.time == same_time_) {
            if (++same_time_count_ > kRunawayLimit) {
                sm_.warnings.push_back(std::format("stopping runaway script {:016x}", graph_.guid()));
                Stop();
                return;
            }
        } else {
            same_time_ = n.time;
            same_time_count_ = 1;
        }
        Execute(n);
        if (stopped()) return;
    }
    auto ticking = ticking_;
    for (State* s : ticking)
        if (s->active() && s->last_tick_ != now()) {
            int64_t elapsed = s->last_tick_ < 0 ? now() - s->begin_time() : now() - s->last_tick_;
            s->last_tick_ = now();
            s->OnTick(elapsed);
        }
    flags_ |= kProcessed;
    // Auto-stop (0xf4ec70): instances created with flag 0x2000 stop without pending work or active states.
    if ((flags_ & kAutoStop) && queue_.empty() && ActiveStates().empty()) Stop();
}

void Instance::RequestStop() {
    QueueSorted(Make(NotifType::StopInstance));
}

void Instance::DeliverPlayScript(const std::vector<std::pair<uint64_t, Primitive>>& params, uint32_t sender) {
    constexpr uint32_t kEntryPlayScript = 0x46c48944;
    // A PlayScript onto another entity whose sync manager is not executing (no flush instantiated it): instantiate
    // now, so its plain entries are queued before the parameters (T055: cross-entity PlayScript, e.g. the tutorial
    // brain playing 0xB40 on the player) [?: the client's order in this case is not read].
    EnsureInstantiated();
    for (size_t i = 0; i < graph_.entries().size(); i++) {
        if (entry_fired_[i]) continue;
        const Node& e = graph_.node(graph_.entries()[i]);
        if (e.type != kEntryPlayScript || !NodeAllowed(e)) continue;
        auto queue_setvar = [&](int out_cv, const Primitive& value) {
            int16_t target;
            uint64_t var;
            if (out_cv < 0 || !SimpleTarget(graph_.DynamicPath(out_cv), id_, target, var)) return;
            Notification n = Make(NotifType::SetVar);
            n.flags = kNotifDefault | kNotifFromMessage;
            n.target = target;
            n.var = var;
            n.value = value;
            QueueSorted(n);
        };
        for (int p : graph_.Objects(*e.fields, kParams)) {
            if (p < 0) continue;
            uint64_t key = Graph::RefGuid(graph_.value(p).Find(kParamKey));
            for (auto& [k, v] : params)
                if (k == key) queue_setvar(graph_.Object(graph_.value(p), kOutVar), v);
        }
        queue_setvar(graph_.Object(*e.fields, kOutSender), Primitive::Entity(sender));
        if (auto* os = e.Field(0xeb7f6ab1); os && os->AsU8()) entry_fired_[i] = 1;  // [?] field at +0x98 here
        Notification n = Make(NotifType::TraverseFrom);
        n.node = e.index;
        QueueSorted(n);
    }
}

bool Instance::Execute(Notification& n) {
    sm_.Emit({.kind = "notif", .instance = id_, .a = int(n.type), .b = n.time, .text = NotifText(n)});
    switch (n.type) {
    case NotifType::Lifetime: {
        if (n.state < 0 || n.state >= (int)graph_.states().size()) break;
        if (n.lifetime == LifetimeKind::Begin) EnsureState(n.state).DoBegin(0);
        else if (State* s = state(n.state)) {
            if (n.lifetime == LifetimeKind::Finish) s->DoFinish();
            else s->DoAbort();
        }
        break;
    }
    case NotifType::Timer:
        if (State* s = state(n.state); s && s->active()) {
            s->OnTimer(n.data);
            if (s->active()) s->EvaluateExits();
        }
        break;
    case NotifType::EvaluateExits:
        if (State* s = state(n.state); s && s->active()) s->EvaluateExits();
        break;
    case NotifType::TraverseFrom: {
        const Node& node = graph_.node(n.node);
        if (node.kind == NodeKind::Entry) FirePlug(nullptr, node, node.Plug(0x78));
        break;
    }
    case NotifType::SetVar: {
        VarBag* bag = nullptr;
        if (n.target == 0) bag = &sm_.entity_bag();
        else if (Instance* t = sm_.FindInstance(n.target)) bag = &t->bag();
        if (bag) bag->Set(n.var, n.value, n.user);
        break;
    }
    case NotifType::StopInstance: Stop(); break;
    case NotifType::LogicalButton: {
        // SyncMgr held-button list (+0x380) [G]; then the states of this instance see the press (+0x1C8).
        // [?] the client routes it through graph lists +0xE0 (exit re-evaluation) / +0xF0 (states with a
        // button hook); here every active state gets it and filters by button itself.
        // Every state of graph list +0xF0 gets vfunc +0x1C8, active or not (its object is created) [G 0xf4f600].
        // [?] not modelled: the +0xE0 LogicalButton conditions' exit re-evaluation (4 nodes in all graphs).
        auto& held = sm_.held_buttons;
        auto it = std::find(held.begin(), held.end(), n.data);
        if (n.state && it == held.end()) held.push_back(n.data);
        if (!n.state && it != held.end()) held.erase(it);
        sm_.last_button = n.data;
        sm_.last_down = n.state != 0;
        // The LogicalButton conditions of graph list +0xE0 for this button: the states of their c77035be list
        // re-evaluate their exits [?: order and immediacy not read; queued at now].
        for (int ni : graph_.button_conditions()) {
            if (ni < 0 || ni >= int(graph_.nodes().size())) continue;
            const Node& c = graph_.node(ni);
            EvalContext ctx{this};
            if (EvaluateInt(ctx, graph_.Object(*c.fields, Hash("m_logicalButton")), -1) != n.data) continue;
            if (auto* l = c.Field(0xc77035be); l && l->type == stu::Value::Type::List)
                for (auto& e : l->items)
                    if (State* st = state(e.AsI32()); st && st->active()) {
                        Notification x = Make(NotifType::EvaluateExits);
                        x.state = st->index();
                        QueueSorted(x);
                    }
        }
        for (int si : graph_.button_states())
            if (si >= 0 && si < int(states_.size())) EnsureState(si).OnLogicalButton(n.data, n.state != 0);
        break;
    }
    case NotifType::AttrDependencyChanged:
        if (State* s = state(n.state); s && s->active()) s->OnWorldChanged();
        break;
    default: sm_.warnings.push_back(std::format("notification type {} not modelled", int(n.type))); break;
    }
    return true;
}

std::vector<std::pair<uint64_t, Primitive>> Instance::SyncVars() const {
    std::vector<std::pair<uint64_t, Primitive>> out;
    for (auto& sv : graph_.sync_vars()) {
        Primitive p;
        uint64_t path[1] = {sv.var};
        if (GetVar(path, p)) out.emplace_back(sv.var, p);
    }
    return out;
}

std::vector<std::pair<int, int>> Instance::ActiveRemoteStates() const {
    std::vector<std::pair<int, int>> out;
    for (size_t i = 0; i < states_.size(); i++)
        if (states_[i] && states_[i]->active() && states_[i]->node().remote_index >= 0)
            out.emplace_back(states_[i]->node().remote_index, int(i));
    return out;
}

std::vector<int> Instance::ActiveStates() const {
    std::vector<int> out;
    for (size_t i = 0; i < states_.size(); i++)
        if (states_[i] && states_[i]->active()) out.push_back(int(i));
    return out;
}

bool State::TimerQueued(int32_t data) const {
    for (auto& q : inst_.queue())
        if (q.type == NotifType::Timer && q.state == index_ && q.data == data) return true;
    return false;
}

// ==== logical-button input (StatescriptRuntime.md §5.9) =====================================================

namespace {
// Mask bit → logical button in emission order (0x1071e40) [G].
constexpr std::pair<uint16_t, int32_t> kButtonOrder[] = {
    {0x0100, 0x42}, {0x0200, 0x4c}, {0x0001, 0x5d}, {0x0002, 0x65}, {0x0004, 0x73}, {0x0010, 0x77}, {0x0020, 0x78},
    {0x0040, 0x79}, {0x0008, 0x66}, {0x0080, 0x7a}, {0x0400, 0x7b}, {0x0800, 0x7c}, {0x1000, 0x7d}};
constexpr uint32_t kLogicalButtonState = 0xff77458c;
}  // namespace

// Action code → button: table 0x173e680 ({u8 code, i32 button} × 45, code = index) [G]; 13 = reload 0x50,
// 14 = quick_weapon 0x71, read live (T051 [L]: they were crossed here, so R did a quick melee on the server).
// Entries 13 / 14 are stored as {14, 0x71}, {13, 0x50} (checked in the binary, T054).
int32_t ActionButton(uint8_t code) {
    if (code >= 1 && code <= 10) return 0x53 + (code - 1);
    if (code >= 16 && code <= 24) return 0x43 + (code - 16);
    if (code >= 27 && code <= 42) return 0xb2 + (code - 27);
    switch (code) {
    case 11: return 0x5e;
    case 12: return 0x5f;
    case 13: return 0x50;
    case 14: return 0x71;
    case 15: return 0x72;
    case 25: return 0x86;
    case 26: return 0xa3;
    case 43: return 0xab;
    case 44: return 0xb1;
    case 45: return 0x9;
    }
    return -1;
}

// Routing (0xf446e0) [G]: an instance gets the notification when a LogicalButton condition of its graph
// (+0xE0) matches the button, or a state of +0xF0 accepts it: any non-LogicalButton state (Ability) accepts
// every button; a LogicalButton state accepts its m_logicalButton when its down / up subgraph is linked or the
// plug for this edge (onGoingDown / onComingUp) is.
int SyncManager::DispatchButton(int32_t button, bool down, int64_t time) {
    int n = 0;
    for (auto& inst : instances_) {
        if (inst->stopped()) continue;
        const Graph& g = inst->graph();
        EvalContext ctx{inst.get()};
        auto cv_button = [&](const Node& node) {
            return EvaluateInt(ctx, g.Object(*node.fields, Hash("m_logicalButton")), -1);
        };
        bool accept = false;
        for (int ni : g.button_conditions())
            if (ni >= 0 && ni < int(g.nodes().size()) && cv_button(g.node(ni)) == button) accept = true;  // [?] 0xf66640
        for (int si : g.button_states()) {
            if (accept || si < 0 || si >= int(g.states().size())) break;
            const Node& node = g.state_node(si);
            if (!g.IsA(node.instance, kLogicalButtonState)) {
                accept = true;
                break;
            }
            if (cv_button(node) != button) continue;
            for (auto& p : node.plugs) {
                bool edge = p.field == (down ? Hash("m_onGoingDownPlug") : Hash("m_onComingUpPlug"));
                if ((p.field == 0x8496114b || p.field == 0x29909c7c || edge) && !p.links.empty()) accept = true;
            }
        }
        if (!accept) continue;
        inst->QueueLogicalButton(button, down, time);
        n++;
    }
    auto it = std::find(held_buttons.begin(), held_buttons.end(), button);
    if (down && it == held_buttons.end()) held_buttons.push_back(button);  // the SM-level copy (0xf459b0)
    if (!down && it != held_buttons.end()) held_buttons.erase(it);
    return n;
}

void SyncManager::Input(uint32_t cf, uint16_t buttons, uint8_t action) {
    int64_t t = int64_t(cf) * kMsPerCf;
    for (auto [bit, button] : kButtonOrder) {
        if (!(prev_buttons_ & bit) && (buttons & bit)) DispatchButton(button, true, t);
        else if ((prev_buttons_ & bit) && !(buttons & bit)) DispatchButton(button, false, t);
    }
    if (action) {
        int32_t b = ActionButton(action);
        if (b >= 0) {
            DispatchButton(b, true, t);
            DispatchButton(b, false, t);
        }
    }
    prev_buttons_ = buttons;
}

// ==== stacks (StatescriptRuntime.md §5.9) =================================================================

uint32_t StackMember::member_key() const { return uint16_t(instance().id()) | (uint32_t(uint16_t(index())) << 16); }

bool StackMember::ResolveStackVar(std::span<const uint64_t> path) {
    int16_t owner = 0;
    stack_var_ = path.empty() ? nullptr : instance().FindVar(path, true, &owner);
    key_ = stack_var_ ? uint16_t(owner) | (uint32_t(stack_var_->id() & 0xFFFF) << 16) : 0;
    return stack_var_ != nullptr;
}

void StackMember::Push(uint32_t pred, bool has_pred, bool notify) {
    if (stack_var_) instance().sm().StackPush(*this, *stack_var_, key_, pred, has_pred, notify);
}

void StackMember::Unregister(bool notify) { instance().sm().StackRemove(*this, notify); }

const StackMember* StackMember::Above() const {
    auto it = instance().sm().stacks().find(key_);
    if (!key_ || it == instance().sm().stacks().end()) return nullptr;
    auto& v = it->second.members;
    for (size_t i = 0; i + 1 < v.size(); i++)
        if (v[i] == this) return v[i + 1];
    return nullptr;
}

void StackMember::DataRefs(bool, std::vector<int16_t>& ids) const {
    if (const StackMember* a = Above()) ids.push_back(a->instance().id());
}

namespace {
// A state reference in state data (0xf4b230): small(instance id); local: m_states index in the named graph's
// m_statesBitCount (+0x17C) bits, remote: its node's m_remoteSyncNodes index in m_remoteSyncNodesBitCount (+0x180).
void WriteStateRef(BitOut& out, const State& s, bool local) {
    out.Prefix3(uint16_t(s.instance().id()), 4, 8, 16);
    const Graph& g = s.instance().graph();
    int w = g.bit_counts()[local ? 1 : 2];
    if (w < 0) {  // not in the asset (never seen): the width the count needs
        uint32_t n = uint32_t(local ? g.states().size() : g.remote_nodes().size());
        w = n > 1 ? int(std::bit_width(n - 1)) : 1;
    }
    out.Bits(uint32_t(local ? s.index() : s.node().remote_index), w);
}
}  // namespace

void StackMember::WritePredecessor(BitOut& out) const {
    const StackMember* a = Above();
    out.Bit(a != nullptr);
    if (a) WriteStateRef(out, *a, true);
}

TypeListMember::~TypeListMember() {
    auto& lists = instance().sm().type_lists;
    if (auto it = lists.find(node().type); it != lists.end()) std::erase(it->second, this);
}

void TypeListMember::OnActivate(bool server_apply) {
    if (server_apply) return;
    auto& l = instance().sm().type_lists[node().type];
    if (std::find(l.begin(), l.end(), this) == l.end()) l.push_back(this);
}

void TypeListMember::OnDeactivate(int) {
    auto& l = instance().sm().type_lists[node().type];
    std::erase(l, this);
    pending_instance = 0, pending_state = -1;
}

const TypeListMember* TypeListMember::Next() const {
    auto it = instance().sm().type_lists.find(node().type);
    if (it == instance().sm().type_lists.end()) return nullptr;
    auto& l = it->second;
    for (size_t i = 0; i + 1 < l.size(); i++)
        if (l[i] == this) return l[i + 1];
    return nullptr;
}

void TypeListMember::WriteData(BitOut& out, bool) const {
    const TypeListMember* n = Next();
    out.Bit(n != nullptr);
    if (n) WriteStateRef(out, *n, true);
}

bool TypeListMember::WriteRemoteData(BitOut& out, bool) const {
    const TypeListMember* n = Next();
    out.Bit(n != nullptr);
    if (n) WriteStateRef(out, *n, false);
    return true;
}

void TypeListMember::DataRefs(bool, std::vector<int16_t>& ids) const {
    if (const TypeListMember* n = Next()) ids.push_back(n->instance().id());
}

// Import (0xf4af00, local format): unlink, then place so that the named member follows; members that named this one
// move in front of it.
void TypeListMember::OnImport(const uint8_t* data, int nbits) {
    SyncManager& sm = instance().sm();
    auto& l = sm.type_lists[node().type];
    std::erase(l, this);
    pending_instance = 0, pending_state = -1;
    auto bit = [&](int i) { return i < nbits && (data[i / 8] >> (i % 8) & 1) != 0; };
    int pos = 0;
    if (nbits > 0 && bit(pos++)) {
        int w = 4;
        if (bit(pos++)) w = bit(pos++) ? 16 : 8;
        uint32_t id = 0;
        for (int i = 0; i < w; i++) id |= uint32_t(bit(pos++)) << i;
        pending_instance = int16_t(id);
        if (Instance* ni = sm.FindInstance(pending_instance)) {
            int sw = ni->graph().bit_counts()[1];
            if (sw < 0) {
                uint32_t n = uint32_t(ni->graph().states().size());
                sw = n > 1 ? int(std::bit_width(n - 1)) : 1;
            }
            uint32_t si = 0;
            for (int i = 0; i < sw; i++) si |= uint32_t(bit(pos++)) << i;
            pending_state = int32_t(si);
        }
    }
    auto named = [&](const TypeListMember* m, int16_t inst, int32_t st) {
        return m->instance().id() == inst && m->index() == st;
    };
    auto it = std::find_if(l.begin(), l.end(), [&](auto* m) { return named(m, pending_instance, pending_state); });
    if (it != l.end()) l.insert(it, this), pending_instance = 0, pending_state = -1;
    else l.push_back(this);
    for (size_t i = 0; i < l.size(); i++) {  // members waiting for this one
        TypeListMember* m = l[i];
        if (m == this || !named(this, m->pending_instance, m->pending_state)) continue;
        m->pending_instance = 0, m->pending_state = -1;
        std::erase(l, m);
        l.insert(std::find(l.begin(), l.end(), this), m);
        i = 0;
    }
}

SyncManager::StackPriority SyncManager::FindStackPriority(uint64_t guid) const {
    auto it = stack_priorities.find(guid);
    return it == stack_priorities.end() ? StackPriority{} : it->second;
}

bool SyncManager::Held(int32_t button) const {
    return std::find(held_buttons.begin(), held_buttons.end(), button) != held_buttons.end();
}

// Push (0xf596e0) [G]: local placement by priority (higher nearer the top; a newcomer goes below equals unless
// it wins ties); a replicated placement (`has_pred`) goes directly below the member `pred` ({inst, state}),
// pred 0 = at the top. Then the var reads as the fold; the new top and the member directly under it are told.
void SyncManager::StackPush(StackMember& m, Var& var, uint32_t key, uint32_t pred, bool has_pred, bool notify) {
    StackEntry& e = stacks_[key];
    e.var = &var;
    std::erase(e.members, &m);
    auto& v = e.members;  // bottom → top
    size_t at = 0;
    if (!has_pred) {
        at = 0;
        for (size_t i = v.size(); i-- > 0;) {
            StackMember* o = v[i];
            if (o->priority() <= m.priority() && (o->priority() < m.priority() || m.wins_ties())) {
                at = i + 1;
                break;
            }
        }
    } else if (pred == 0) {
        at = v.size();
    } else {
        at = 0;
        for (size_t i = 0; i < v.size(); i++)
            if (v[i]->member_key() == pred) at = i;
    }
    v.insert(v.begin() + at, &m);
    var.SetStackSlot(this, key);
    Recompute(var, notify);
    if (!notify || v.empty()) return;
    StackMember* top = v.back();
    if (v.size() > 1) v[v.size() - 2]->BecomeUnder();
    top->BecomeTop();
}

// Unregister (0xf560b0 → 0xf59c10) [G].
void SyncManager::StackRemove(StackMember& m, bool notify) {
    auto it = stacks_.find(m.stack_key());
    if (it == stacks_.end()) return;
    StackEntry& e = it->second;
    if (std::find(e.members.begin(), e.members.end(), &m) == e.members.end()) return;
    std::erase(e.members, &m);
    if (e.var) Recompute(*e.var, notify);
    if (e.members.empty() && e.var) e.var->ClearStackSlot();
    if (!notify || e.members.empty()) return;
    auto& v = e.members;
    if (v.size() > 1) v[v.size() - 2]->BecomeUnder();
    v.back()->BecomeTop();
}

// Fold (0xf59d40 / 0xf55db0) [G]: from the stored value, each member bottom → top may replace it.
void SyncManager::Recompute(Var& var, bool notify) {
    auto it = stacks_.find(var.stack_key());
    if (var.stack_key() && it != stacks_.end()) {
        Primitive v = var.stored();
        for (StackMember* m : it->second.members) m->Fold(v);
        var.SetFolded(v);
    }
    if (notify) var.Notify();
}

// ==== SyncManager ========================================================================================

SyncManager::SyncManager(uint32_t owner, const EvaluatorRegistry& evaluators, Role role)
    : owner_(owner), evals_(&evaluators), role_(role) {}
SyncManager::~SyncManager() {
    instances_.clear();  // states unregister from the registries below while those still exist
}

void SyncManager::SetClock(uint32_t cf, int64_t time_ms) {
    cf_ = cf;
    time_ = time_ms;
    end_ = time_ms;
}

void SyncManager::Emit(TraceEvent ev) const {
    if (!trace) return;
    ev.cf = cf_;
    ev.ms = time_;
    trace(ev);
}

Instance* SyncManager::FindInstance(int16_t id) const {
    Instance* stopped = nullptr;  // a live instance wins over a stopped one that had its id before
    for (auto& i : instances_)
        if (i->id() == id) {
            if (!i->stopped()) return i.get();
            stopped = i.get();
        }
    return stopped;
}

Instance* SyncManager::CreateInstance(const Graph& graph, std::span<const Override> overrides, int16_t id,
                                      bool apply_schema) {
    if (id == 0) {
        int16_t lowest = 0;
        for (auto& i : instances_) lowest = std::min(lowest, i->id());
        id = int16_t(lowest - 1);
    }
    if (FindInstance(id)) return nullptr;
    auto owned = std::make_unique<Instance>(*this, graph, id);
    Instance* inst = owned.get();
    instances_.push_back(std::move(owned));
    inst->created_ = time_;
    inst->bag().Store(varid::Owner, Primitive::Entity(owner_));
    EvalContext ctx{inst};
    auto queue = [&](uint64_t var, const Primitive& v) {
        Notification n = inst->Make(NotifType::SetVar);
        n.flags = kNotifDefault | kNotifFromMessage;
        n.target = id;
        n.var = var;
        n.value = v;
        inst->QueueSorted(n);
    };
    if (apply_schema)
        for (auto& e : graph.schema()) {
            Primitive v;  // INT 0: queued as is when the config var yields nothing (mover 0x208's $702) [L]
            Evaluate(ctx, e.value, v);
            queue(e.var, v);
        }
    for (auto& o : overrides) queue(o.var, o.value);
    if (executing_) inst->Instantiate();
    else pending_instantiate_.push_back(inst);
    return inst;
}

int16_t SyncManager::AllocateId(bool mirrored) const {
    int16_t hi = 0, lo = 0;
    for (auto& i : instances_)
        if (!i->stopped()) hi = std::max(hi, i->id()), lo = std::min(lo, i->id());
    if (!mirrored) return int16_t(lo - 1);
    if (hi + 1 <= 0x7F) return int16_t(hi + 1);
    for (int16_t id = 1; id < 0x7F; id++)
        if (std::none_of(instances_.begin(), instances_.end(), [&](auto& i) { return !i->stopped() && i->id() == id; }))
            return id;
    return 0;
}

Instance* SyncManager::CreateChild(const Graph& graph, bool mirrored) {
    int16_t id = AllocateId(mirrored);
    if (Instance* old = id ? FindInstance(id) : nullptr; id == 0 || (old && !old->stopped())) return nullptr;
    auto owned = std::make_unique<Instance>(*this, graph, id);
    Instance* inst = owned.get();
    instances_.push_back(std::move(owned));
    inst->created_ = time_;
    inst->bag().Store(varid::Owner, Primitive::Entity(owner_));
    return inst;
}

Instance* SyncManager::ImportInstance(const Graph& graph, int16_t id, int16_t parent, uint32_t slot) {
    if (id == 0 || FindInstance(id)) return nullptr;
    auto owned = std::make_unique<Instance>(*this, graph, id);
    Instance* inst = owned.get();
    instances_.push_back(std::move(owned));
    inst->created_ = time_;
    inst->SetParent(parent, slot);
    inst->bag().Store(varid::Owner, Primitive::Entity(owner_));
    inst->InstantiateImported();
    return inst;
}

void SyncManager::PostEvent(int64_t time, std::function<void()> fn) { events_.emplace(time, std::move(fn)); }

void SyncManager::RecordHostEvent(const Instance& inst, const Node& n, int32_t kind) {
    uint32_t cf = uint32_t(time_ / kMsPerCf);
    if (!host_events_.empty() && cf >= kActionLogCfs && host_events_.front().cf < cf - kActionLogCfs) {
        auto keep = std::find_if(host_events_.begin(), host_events_.end(), [&](const HostEvent& e) { return e.cf >= cf - kActionLogCfs; });
        host_events_.erase(host_events_.begin(), keep);
    }
    HostEvent e{cf, inst.id(), n.index, n.state_index, n.type, kind};
    host_events_.push_back(e);
    if (host_event_hook) host_event_hook(e);
}

void SyncManager::RecordAction(const Instance& inst, int node) {
    if (inst.id() <= 0) return;  // only mirrored (server-created) instances record (0xd14a20) [G]
    uint32_t cf = uint32_t(time_ / kMsPerCf);
    if (!actions_.empty() && cf >= kActionLogCfs && actions_.front().cf < cf - kActionLogCfs) {
        auto keep = std::find_if(actions_.begin(), actions_.end(), [&](const ActionRecord& a) { return a.cf >= cf - kActionLogCfs; });
        actions_.erase(actions_.begin(), keep);
    }
    actions_.push_back({cf, inst.id(), node});
}

bool SyncManager::NextTime(int pass, bool) {
    bool ret = pass == 0 || time_ < end_;
    int64_t t = time_, next = end_;
    auto consider = [&](int64_t c) {
        if (c <= next) {
            ret = true;
            next = c <= t ? t : c;
        }
    };
    consider(((t + 1) / kMsPerCf + 1) * kMsPerCf - 1);
    if (!events_.empty()) consider(events_.begin()->first);
    for (auto& i : instances_)
        if (!i->queue_.empty() && (i->flags_ & (Instance::kProcessed | Instance::kStopped | 0x10)) == Instance::kProcessed)
            consider(i->queue_.front().time);
    time_ = next;
    cf_ = uint32_t(next / kMsPerCf);
    return ret;
}

void SyncManager::RunCommandFrames(uint32_t cf, uint32_t n) {
    TraceEvent start{.kind = "cf", .a = cf, .b = n, .c = int64_t(instances_.size())};
    Emit(start);
    if (cf != cf_ || n) {
        cf_ = cf;
        int64_t s = int64_t(cf) * kMsPerCf;
        time_ = s;
        end_ = s + int64_t(n) * kMsPerCf - 1;
    }
    bool was_running = in_run_, was_executing = executing_;
    int64_t flush_time = time_;
    int64_t saved_end = end_;
    in_run_ = executing_ = true;
    auto pending = std::move(pending_instantiate_);
    pending_instantiate_.clear();
    for (Instance* i : pending) i->Instantiate();
    uint32_t world = world_pending_;
    world_pending_ = 0;
    int pass = 0, same = 0;
    int64_t last = -1;
    while (NextTime(pass, pass == 0)) {
        pass_seq_start_ = seq_;
        while (!events_.empty() && events_.begin()->first <= time_) {
            auto fn = std::move(events_.begin()->second);
            events_.erase(events_.begin());
            fn();
        }
        for (size_t k = 0; k < instances_.size(); k++) instances_[k]->Process(pass == 0 ? world : 0);
        if (after_instances) after_instances(pass);
        if (time_ == last) {
            if (++same >= kSameTimePassLimit) {
                warnings.push_back("runaway sync manager: stopping every instance");
                for (auto& i : instances_) i->Stop();
                break;
            }
        } else {
            same = 0;
            last = time_;
        }
        pass++;
    }
    // n > 0: the clock ends at the window end. n = 0 (a flush) leaves it where it was: the same thing at top
    // level (the clock is at the window end), and the time of the pass for a flush nested in a run [L].
    if (n > 0) time_ = end_;
    else time_ = flush_time;
    cf_ = uint32_t(time_ / kMsPerCf);
    if (!was_running)  // data changes of active states count as changes (ss_sm_state_changes)
        for (auto& i : instances_)
            for (int k = 0, n = int(i->graph().states().size()); k < n; k++)
                if (State* st = i->state(k); st && st->active()) st->CheckDataChange();
    in_run_ = was_running;
    executing_ = was_executing;
    if (was_running) end_ = saved_end;  // a nested flush (PlayScript) inside a running window
    Emit({.kind = "cf_end", .a = cf});
}

Instance* SyncManager::PlayScript(uint64_t guid, const std::vector<std::pair<uint64_t, Primitive>>& params,
                                  uint32_t sender, uint32_t stacking) {
    const Graph* g = graph_provider ? graph_provider(guid) : nullptr;
    if (!g) {
        warnings.push_back(std::format("PlayScript: graph {:016x} not available", guid));
        return nullptr;
    }
    for (auto& i : instances_)
        if (i->graph().guid() == guid && !i->stopped()) {
            if (stacking == 0) return nullptr;
            if (stacking == 3) i->Stop();
        }
    Instance* inst = CreateInstance(*g, {}, 0, /*apply_schema=*/false);
    if (!inst) return nullptr;
    if (executing_) RunCommandFrames(cf_, 0);  // the flush seen live inside the PlayScript action [L]
    inst->DeliverPlayScript(params, sender);
    // Created with LoadGraph's flag 0x2000 (auto-stop) [L]; it must not stop in the flush above, before its
    // parameters arrive (the client's child survives it) [?: the client's exact condition is not read].
    inst->flags_ |= Instance::kAutoStop;
    return inst;
}

bool SyncManager::ReadEntityVar(uint32_t entity, std::span<const uint64_t> path, Primitive& out) {
    if (path.empty() || IsInvalidEntity(entity)) return false;
    SyncManager* other = entity == owner_ ? this : entity_resolver ? entity_resolver(entity) : nullptr;
    if (!other) return false;
    VarBag* bag = &other->entity_bag();
    while (path.size() > 1 && (path[0] == varid::EntityBag || path[0] == varid::InstanceBag)) path = path.subspan(1);
    return Instance::ReadFrom(*other, bag, path, out);
}

int SyncManager::DeliverGameMessageFrom(const GameMessage& msg, bool from_other) {
    int fired = DeliverGameMessage(msg);
    if (fired && from_other) RunCommandFrames(cf_, 0);
    return fired;
}

int SyncManager::DeliverGameMessage(const GameMessage& msg) {
    int fired = 0;
    for (size_t k = 0; k < instances_.size(); k++)
        if (instances_[k]->DeliverMessage(msg) == 2) fired++;
    return fired;
}

}  // namespace ss
