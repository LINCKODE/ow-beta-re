// The Statescript runtime: a per-entity sync manager (component 0x23 +0xC0) owning instances of graphs,
// their notification queues, state objects and var bags, stepped in 16 ms command-frame windows.
// Behaviour follows docs/StatescriptRuntime.md §3 (ordering rules §3.12); the library documentation is §5.
#pragma once
#include <array>
#include <cstdint>
#include <functional>
#include <list>
#include <map>
#include <memory>
#include <span>
#include <string>
#include <vector>

#include "statescript/config_var.h"
#include "statescript/graph.h"
#include "statescript/value.h"
#include "statescript/vars.h"

namespace ss {

class Instance;
class State;
class SyncManager;
class MatchHost;

// ---- notifications (§3.4) ---------------------------------------------------------------------------

enum class NotifType : uint8_t {
    Lifetime = 0,
    Timer = 1,
    EvaluateExits = 2,
    TraverseFrom = 3,
    SetVar = 4,
    SetVarArray = 5,
    StopInstance = 6,
    LogicalButton = 7,
    AttrDependencyChanged = 8,
};
enum class LifetimeKind : uint16_t { Begin = 0, Finish = 1, Abort = 2 };

// Notification flags (+0x28): 0x1 set on every notification seen live [?], 0x4 client-only node,
// 0x8 value from a game message / LoadGraph override.
inline constexpr uint8_t kNotifDefault = 0x1, kNotifClientOnly = 0x4, kNotifFromMessage = 0x8;

struct Notification {
    int64_t time = 0;
    NotifType type = NotifType::Lifetime;
    uint32_t seq = 0;
    uint8_t flags = kNotifDefault;
    LifetimeKind lifetime = LifetimeKind::Begin;
    int state = -1;       // m_states index (Lifetime / Timer / EvaluateExits / AttrDependencyChanged)
    int node = -1;        // m_nodes index (TraverseFrom)
    int32_t data = 0;     // Timer user data
    int16_t target = 0;   // SetVar: target instance id (0 = entity bag)
    uint64_t var = 0;     // SetVar: var id
    Primitive value;      // SetVar
    VarUser user;         // SetVar
};

// ---- trace (the ss_trace fixture vocabulary, §4.1) ----------------------------------------------------

struct TraceEvent {
    std::string kind;  // cf, cf_end, instantiate, instantiate_done, notif, begin, reenter, finish, finish_done,
                       // abort, abort_done, link, link_done, message
    uint32_t cf = 0;
    int64_t ms = 0;
    int16_t instance = 0;
    // kind-specific (same meaning as the fixture fields):
    //   cf: a=cf b=n c=instances · instantiate: a=guid b=parent · instantiate_done: b=queued
    //   notif: a=type b=scheduled time, text · begin/reenter: a=state b=node c=offset · finish/abort: a=state
    //   b=node · *_done: a=state · link: a=src b=plug offset c=dst, idx · link_done: a=dst b=ret
    //   message: a=id b=result c=sender, idx=instigator
    int64_t a = 0, b = 0, c = 0;
    int64_t idx = -1;
    std::string text;
};
using TraceSink = std::function<void(const TraceEvent&)>;

// ---- game messages (§3.10) -----------------------------------------------------------------------------

struct GameMessage {
    uint64_t id = 0;  // 0x024… message GUID
    std::vector<std::pair<uint64_t, Primitive>> params;  // 0x0A4… parameter key -> value
    uint32_t sender = 0, instigator = 0;
};

// ---- states (§1.8, §3.6) ------------------------------------------------------------------------------

// State data bits, LSB first (the sync packets' bit order).
struct BitOut {
    std::vector<uint8_t> bytes;
    int nbits = 0;
    void Bits(uint64_t v, int k) {
        for (int i = 0; i < k; i++, nbits++) {
            if (nbits / 8 >= int(bytes.size())) bytes.push_back(0);
            if (v >> i & 1) bytes[nbits / 8] |= uint8_t(1u << (nbits % 8));
        }
    }
    void Bit(bool b) { Bits(b ? 1 : 0, 1); }
    // 0 + w0 | 1 0 + w1 | 1 1 + w2.
    void Prefix3(uint32_t v, int w0, int w1, int w2) {
        if (v < (1ull << w0)) Bit(false), Bits(v, w0);
        else if (v < (1ull << w1)) Bit(true), Bit(false), Bits(v, w1);
        else Bit(true), Bit(true), Bits(v, w2);
    }
};

class State : public VarSubscriber {
public:
    // State flags (+0x40).
    static constexpr uint32_t kActive = 0x1, kEnding = 0x2, kSelfTransition = 0x4, kSubscribed = 0x8,
                              kExitTaken = 0x10, kTicking = 0x80;

    State(Instance& inst, int state_index);
    ~State() override;

    Instance& instance() const { return inst_; }
    int index() const { return index_; }
    const Node& node() const;
    bool active() const { return flags_ & kActive; }
    uint32_t flags() const { return flags_; }
    int16_t serial() const { return serial_; }
    // Lifetime changes (begin, re-enter, end) so far: what a server delta marks as a changed state (the
    // client's sync hooks record begin / re-enter / end, §3.11), unlike data that moves with time.
    uint32_t changes() const { return changes_; }
    // A change of the state's data that the client must be told about (not one that only moves with time):
    // counted like a lifetime change (the local delta sends the state again).
    void NoteDataChange() { changes_++; }
    int64_t begin_time() const { return begin_time_; }
    int64_t now() const;

    // Reached through a link (0xf48700): schedules begin / abort, never runs synchronously.
    bool Execute(State* src, const Link& link);
    // The lifetime runners (DoBeginState 0xf495c0, DoFinishState 0xf499d0, DoAbortState 0xf49df0).
    void DoBegin(int64_t time_offset = 0);
    void DoFinish();
    void DoAbort();
    // Evaluate exits (vfunc +0x1E8, base 0xf48870).
    void EvaluateExits();
    // World-change dependency hit (vfunc +0x170 with no var).
    void OnWorldChanged() { VarChanged(nullptr); }
    // Replicated activation (server start-up import, the client's server apply): active from now, subscribed,
    // no links followed, no plugs fired; `data` = the type's local-format state data (LSB-first bits).
    void Import(const uint8_t* data, int nbits);

    // Type hooks (client state vfuncs). Base: no-ops.
    virtual void OnActivate(bool /*server_apply*/) {}  // +0x160
    virtual void OnBeginPre() {}                       // +0x108: after activation, before the OnBegin plug
    virtual void OnBegin() {}                          // +0x110 type-specific begin
    virtual void OnReenter() {}                        // +0x118
    virtual void OnTick(int64_t /*elapsed_ms*/) {}     // +0x120 (per-pass ticking list)
    virtual void OnTimer(int32_t /*data*/) {}          // +0x140
    virtual void OnFinishHook() {}                     // +0x148
    virtual void OnAbortHook() {}                      // +0x150
    virtual void OnEndHook() {}                        // +0x158: after the finish / abort hook
    virtual void OnDeactivate(int /*reason*/) {}       // +0x168: finish 1, abort 0, server apply 6
    virtual void TraverseFinished() {}                 // +0x250
    virtual void TraverseAborted() {}                  // +0x248
    virtual bool HandlesInput(const Link&) { return false; }  // +0x240
    virtual bool CheckExits();                         // +0xD0: transition plugs in order
    virtual void VarChanged(Var* /*var*/) {}           // +0x170 (list-0 subscriptions, world changes)
    // State-defined value (+0x228): the value of a var this state owns with the state-defined bit (user bit 7),
    // `extra` = which one. Readers of such a var see this instead of its stored value. false = none.
    virtual bool StateValue(uint8_t /*extra*/, Primitive& /*out*/) const { return false; }
    // A child instance whose parent slot is this state stopped (0xf500e0; SubScript states).
    virtual void ChildStopped(Instance& /*child*/) {}
    // LogicalButton notification delivered to this state (+0x1C8).
    virtual void OnLogicalButton(int32_t /*button*/, bool /*down*/) {}
    // Whether a Timer with `data` is queued for this state.
    bool TimerQueued(int32_t data) const;
    // Import: restore the type's internal state (timers, stored values, state data) without firing plugs.
    virtual void OnImport(const uint8_t* /*data*/, int /*nbits*/) { OnActivate(true); }
    // The local-format state data (writer +0x1F0; full = full update). Base: the imported bits, if any
    // (types whose writer is not modelled keep what the start-up sent).
    virtual void WriteData(BitOut& out, bool /*full*/) const {
        for (int i = 0; i < import_bits_; i++) out.Bit(import_data_[i / 8] >> (i % 8) & 1);
    }
    // The part of the state data whose change the client must be told about (not what moves with time): a
    // change of it between two runs counts as a change (changes()). Base: all of the local data; types whose
    // data moves with time (Ability, WeaponVolley, ChaseVar, Anim) write only their event fields.
    virtual void WriteChangeKey(BitOut& out) const { WriteData(out, false); }
    // Compare the change key with the last one (SyncManager, after every run of an active state).
    void CheckDataChange();
    // The remote-format state data of types whose remote format differs from the local one; false = not
    // produced (capabilities.cpp lists the types that have it).
    virtual bool WriteRemoteData(BitOut& /*out*/, bool /*full*/) const { return false; }
    // The instance ids the state data names (stack predecessor, …) in the local / remote format.
    virtual void DataRefs(bool /*local*/, std::vector<int16_t>& /*ids*/) const {}
    // Whether an import supplied state data (what WriteData echoes for types without a writer).
    bool imported_data() const { return import_bits_ > 0; }

    // VarSubscriber
    void OnVarChanged(Var& v) final { VarChanged(&v); }
    void OnVarDependencyChanged(Var& v) final;  // +0x178: EvaluateExits at now when active

protected:
    // Helpers for state types.
    bool FirePlugField(uint32_t field_hash, int index = -1);  // FirePlug(src = this)
    bool FirePlug(const OutPlug* plug);
    bool FirePlugIndexed(const OutPlug* plug);                 // 0xf470b0: traversal with the plug's index
    void AbortStates(std::span<const int32_t> state_indices);  // 0xf2a8b0: DoAbort on each existing object
    void ScheduleLifetime(LifetimeKind kind, int64_t delay = 0);
    void ScheduleTimer(int64_t at, int32_t data, bool at_head = false);
    void RemoveTimers(int32_t data);
public:
    // A host ending the state (ss_sm_end_state): Lifetime finish / abort at now.
    void RequestEnd(bool finish) { ScheduleLifetime(finish ? LifetimeKind::Finish : LifetimeKind::Abort); }
    // Tooling for condition nodes that time a state (Condition_Timeout): drop this state's Timers with `data`, then
    // queue one at `at`.
    void RearmTimer(int64_t at, int32_t data) {
        RemoveTimers(data);
        ScheduleTimer(at, data);
    }

protected:
    void SetTicking(bool on);
    void SetFlag(uint32_t f, bool on) { flags_ = on ? flags_ | f : flags_ & ~f; }
    void SetSerial(int16_t s) { serial_ = s; }  // an import that carries (part of) the serial
    EvalContext Ctx() const;

private:
    friend class Instance;
    void Subscribe();
    void Unsubscribe();
    void End(bool finish);

    Instance& inst_;
    int index_;
    uint32_t flags_ = 0;
    int16_t serial_ = 0;
    uint32_t changes_ = 0;
    int64_t begin_time_ = 0, end_time_ = -1, last_tick_ = -1;
    std::vector<uint8_t> import_data_;
    int import_bits_ = 0;
    std::vector<uint8_t> change_key_;
    int change_key_bits_ = -1;  // -1: no baseline yet
    std::vector<std::vector<uint64_t>> subscribed_[2];  // var paths subscribed at begin
};

// A member of a var stack (STUStatescriptStateStack p0 class 0x15c9ed8; the Ability state derives from it):
// the sync manager's registry keeps, per (bag, var), the members ordered bottom → top by priority; the var then
// reads as the fold of the members' values over its stored value (StatescriptRuntime.md §5.9).
class StackMember : public State {
public:
    using State::State;
    float priority() const { return priority_; }
    bool wins_ties() const { return wins_ties_; }
    bool on_top() const { return on_top_; }
    bool under() const { return under_; }
    uint32_t stack_key() const { return key_; }
    // {instance id, state index}: how state data names a stack predecessor.
    uint32_t member_key() const;
    // The member directly above this one in its stack (null = on top / not in a stack).
    const StackMember* Above() const;
    // Stack predecessor in state data (0xf4b230): bit present; instance id (4 / 8 / 16); state index.
    void WritePredecessor(BitOut& out) const;
    void DataRefs(bool local, std::vector<int16_t>& ids) const override;  // the predecessor's instance

    // Fold (+0x258): the value this member contributes (default: pass through).
    virtual void Fold(Primitive& /*v*/) {}
    // Registry callbacks (+0x260 became top, +0x268 directly under the top / lost the top).
    virtual void BecomeTop() { on_top_ = true; }
    virtual void BecomeUnder() { under_ = true; }

protected:
    friend class SyncManager;
    // Resolve (creating it) the stack var at `path`; false if it can't be resolved.
    bool ResolveStackVar(std::span<const uint64_t> path);
    void Push(uint32_t pred = 0, bool has_pred = false, bool notify = true);
    void Unregister(bool notify = true);
    Var* stack_var_ = nullptr;
    uint32_t key_ = 0;
    float priority_ = 0.0f;
    bool wins_ties_ = false, on_top_ = false, under_ = false;
    int64_t last_tick_ = -1;
};

// A member of its sync manager's per-type list (0xf4ade0 activate / 0xf4ae60 deactivate, list per STU type 0xf4b1c0)
// [G] (docs/StatescriptNodes.md §3.7): LimitAim, Invisible, CameraTransform, PhaseOut, TimeScaleAnimation, AimSpeed,
// CameraChase, 2785fbdd, WeaponAim, ImpactEffectOverride. Activation appends it (not in server-apply mode),
// deactivation unlinks it. State data (writer 0xf4aed0 → 0xf4b230): the member appended after it (bit; small
// instance id 4 / 8 / 16; local: its m_states index in m_statesBitCount bits of its graph, remote: its node's
// m_remoteSyncNodes index in m_remoteSyncNodesBitCount bits); import (0xf4af00) re-links the list to match.
class TypeListMember : public State {
public:
    using State::State;
    ~TypeListMember() override;
    void OnActivate(bool server_apply) override;
    void OnDeactivate(int reason) override;
    void OnImport(const uint8_t* data, int nbits) override;
    void WriteData(BitOut& out, bool full) const override;
    bool WriteRemoteData(BitOut& out, bool full) const override;
    void DataRefs(bool local, std::vector<int16_t>& ids) const override;
    const TypeListMember* Next() const;
    // An imported reference not resolved yet: {instance id, state index}.
    int16_t pending_instance = 0;
    int32_t pending_state = -1;
};

// Creates the state object for a node type (nearest registered ancestor, else the base State).
std::unique_ptr<State> CreateState(Instance& inst, int state_index);
// Actions that only drive client presentation (effects, sounds, debug text): no-ops by design.
bool IsPresentationAction(uint32_t type);

// ---- instances (§1.5, §3.3–3.10) -----------------------------------------------------------------------

class Instance {
public:
    // Instance flags (+0x258), the ones this library uses.
    static constexpr uint32_t kInstantiated = 0x1, kAllEntriesFired = 0x2, kStopped = 0x4, kMirrored = 0x8,
                              kProcessedPass = 0x80, kAutoStop = 0x2000, kRunsLocally = 0x400000, kProcessed = 0x8000;

    Instance(SyncManager& sm, const Graph& graph, int16_t id);
    ~Instance();

    SyncManager& sm() const { return sm_; }
    const Graph& graph() const { return graph_; }
    int16_t id() const { return id_; }
    uint32_t flags() const { return flags_; }
    bool stopped() const { return flags_ & kStopped; }
    bool instantiated() const { return flags_ & kInstantiated; }
    // SubScript child: parent instance id and slot (instance +0x1A / +0x1C); 0 for top-level instances.
    int16_t parent() const { return parent_; }
    uint32_t slot() const { return slot_; }
    // The serial of the parent's state when it created this child (instance +0x20; -1 = not known, an import).
    int16_t parent_serial() const { return parent_serial_; }
    void SetParent(int16_t parent, uint32_t slot, int16_t serial = -1) {
        parent_ = parent, slot_ = slot, parent_serial_ = serial;
    }
    int64_t now() const;
    VarBag& bag() { return bag_; }
    const VarBag& bag() const { return bag_; }
    State* state(int i) const { return i >= 0 && i < (int)states_.size() ? states_[i].get() : nullptr; }
    State& EnsureState(int i);
    const std::list<Notification>& queue() const { return queue_; }

    // InstantiateGraph (0xf4e370): size the state lists, queue TraverseFrom for the plain entries.
    void Instantiate();
    // A replicated / imported instance: instantiated with its plain entries counted as fired (its states and
    // vars come from the import, ss_sm_import_*).
    void InstantiateImported();
    void Stop();
    // Instantiate now if not yet instantiated (the relevant part of 0xf4caa0).
    void EnsureInstantiated() {
        if (!instantiated()) Instantiate();
    }

    // Queueing (instance vfuncs +0x140 sorted / +0x148 head / exit-abort placement).
    void QueueSorted(Notification n);
    void QueueHead(Notification n);
    void QueueExitAbort(Notification n);
    void DropStateNotifications(int state, bool only_now = false);
    void DropLifetime(int state, LifetimeKind kind, int64_t time);
    Notification Make(NotifType type) const;
    // A LogicalButton notification (type 7: data = button id, state = down) at `time`, sorted (§3.4, §4.1.1).
    void QueueLogicalButton(int32_t button, bool down, int64_t time);

    // Links (§3.5).
    bool FirePlug(State* src, const Node& from, const OutPlug* plug, bool indexed = false);
    bool TraverseLink(State* src, const Node& from, const OutPlug& plug, const Link& link, bool indexed);
    // Tooling (Prometheus execute_node): take an entry synchronously, outside the notification queue.
    bool ExecuteEntry(int node);

    // Game messages: 1 = no entry fired, 2 = an entry fired (the fixture's `result`).
    int DeliverMessage(const GameMessage& msg);
    // A PlayScript request to this (freshly created) instance: parameter SetVars for its unfired
    // STUStatescriptEntryPlayScript entries, then their TraverseFrom (like a game message).
    void DeliverPlayScript(const std::vector<std::pair<uint64_t, Primitive>>& params, uint32_t sender);
    // Queues a StopInstance notification at now (0xf2fff0, StopScript).
    void RequestStop();

    // Variables through a path (§1.7) starting in this instance's bag. A path through an ENTITY var continues
    // in that entity's Statescript (SyncManager::ReadEntityVar).
    bool GetVar(std::span<const uint64_t> path, Primitive& out) const;
    // Whether a Dynamic path depends on more than this instance's notifications (evaluator +0xD8, 0xf5c160).
    bool PathDependsOnExternal(std::span<const uint64_t> path) const;
    // The Dictionary var at `path` (no creation), null if the path does not end at one.
    const Var* FindDictionary(std::span<const uint64_t> path) const;
    // The bag and var id a path writes to (created on the way, as a write's walk does); null if not resolvable.
    VarBag* WriteBag(std::span<const uint64_t> path, uint64_t& id);
    SetResult SetVar(std::span<const uint64_t> path, const Primitive& value, const VarUser& user = {});
    // The var at `path` (created as INT 0 if `create`), and the instance id owning its bag (0 = entity bag).
    Var* FindVar(std::span<const uint64_t> path, bool create, int16_t* owner = nullptr);
    void SubscribeVar(std::span<const uint64_t> path, VarSubscriber* s, int list);
    void UnsubscribeVar(std::span<const uint64_t> path, VarSubscriber* s, int list);

    // One processing pass (ProcessInstance 0xf4e7f0): world-change hooks, due notifications, ticking states.
    void Process(uint32_t world_mask);
    bool Execute(Notification& n);

    // Sync state (§5.5): graph m_syncVars values and the active remotely synced states.
    std::vector<std::pair<uint64_t, Primitive>> SyncVars() const;
    std::vector<std::pair<int, int>> ActiveRemoteStates() const;  // {m_remoteSyncNodes index, m_states index}
    std::vector<int> ActiveStates() const;

    void SetTicking(State& s, bool on);

private:
    friend class SyncManager;
    friend class State;
    VarBag* ResolveBag(std::span<const uint64_t>& path, bool create) const;
    static bool ReadFrom(SyncManager& sm, VarBag* bag, std::span<const uint64_t> path, Primitive& out);
    bool NodeAllowed(const Node& n) const;
    bool ExecuteNode(State* src, const Node& n);

    SyncManager& sm_;
    const Graph& graph_;
    int16_t id_;
    int16_t parent_ = 0;
    uint32_t slot_ = 0;
    int16_t parent_serial_ = -1;
    // The traversal in progress and the AndGate arrivals of the current timestamp (instance +0x120).
    int last_src_ = -1, last_plug_ = -1, last_idx_ = -1;
    std::vector<std::array<int16_t, 4>> gate_arrivals_;
    int64_t gate_time_ = -1;
    friend bool AndGateExecute(State* src, Instance& inst, const Node& n);
    uint32_t flags_ = kRunsLocally;
    int64_t created_ = 0;
    VarBag bag_{varid::InstanceBag};
    std::vector<std::unique_ptr<State>> states_;
    std::vector<uint8_t> entry_fired_;
    std::list<Notification> queue_;
    std::vector<State*> ticking_;
    int same_time_count_ = 0;
    int64_t same_time_ = -1;
};

// ---- the sync manager (§1.6, §3.2–3.3) ---------------------------------------------------------------

enum class Role : uint8_t {
    Client,  // the client's filters: server-only nodes are never followed (replaying client traces)
    Server,  // server: server-only nodes run, client-only nodes are skipped
};

struct Override {
    uint64_t var = 0;
    Primitive value;
};

class SyncManager : public VarStackHost {
public:
    static constexpr int64_t kMsPerCf = 16;

    SyncManager(uint32_t owner_entity, const EvaluatorRegistry& evaluators, Role role = Role::Client);
    ~SyncManager();

    uint32_t owner() const { return owner_; }
    Role role() const { return role_; }
    const EvaluatorRegistry& evaluators() const { return *evals_; }
    VarBag& entity_bag() { return entity_bag_; }
    uint32_t cf() const { return cf_; }
    int64_t time() const { return time_; }
    bool executing() const { return executing_; }
    void SetClock(uint32_t cf, int64_t time_ms);

    // A default graph of the entity (StartDefaultScripts → LoadGraph, component vfunc +0x58 → CreateInstance
    // 0xd13810, then 0x103beb0): allocates a local (negative) id if `id` is 0, writes the owner var, queues the
    // graph's m_publicSchema defaults (unless `apply_schema` is false: PlayScript children get none [L]) and
    // then `overrides` as SetVar notifications at the current time; the graph is instantiated at the next
    // RunCommandFrames (at once while the sync manager is executing).
    Instance* CreateInstance(const Graph& graph, std::span<const Override> overrides = {}, int16_t id = 0,
                             bool apply_schema = true);
    Instance* FindInstance(int16_t id) const;
    // A child instance (component vfunc +0x70 → CreateInstance with kind 1, 0xd13810): an id from the parent's
    // side (mirrored: positive, AllocateId), the owner var, no schema; not instantiated (kind 1 never is at
    // creation): the creator queues its inputs, then instantiates it.
    Instance* CreateChild(const Graph& graph, bool mirrored);
    // Instance ids (component +0xA8 / +0xAA) [G]: mirrored = max(ids, 0) + 1 if <= 0x7F, else the smallest
    // unused id in [1, 0x7F) (0xcca250 / 0x103b6a0); local = min(ids, 0) − 1 (0x103b750). Stopped instances
    // don't count [?: when the client frees a stopped instance was not read]. 0 = none free.
    int16_t AllocateId(bool mirrored) const;
    // An imported instance (server start-up, replication): no schema, no overrides, entries not fired.
    Instance* ImportInstance(const Graph& graph, int16_t id, int16_t parent, uint32_t slot);
    std::span<const std::unique_ptr<Instance>> instances() const { return instances_; }

    // RunCommandFrames (0xf44ef0): n = 0 flushes at the current time.
    void RunCommandFrames(uint32_t cf, uint32_t n = 1);
    // SendGameMessage to this entity: every instance, in order. Returns the number of instances with an entry fired.
    int DeliverGameMessage(const GameMessage& msg);
    // 0xf457c0: deliver to every instance; `from_other` (sent by another entity's script) and an entry fired →
    // a nested flush RunCommandFrames(cf, 0).
    int DeliverGameMessageFrom(const GameMessage& msg, bool from_other);
    // Other entities' Statescript (SyncMgr vfunc +0xD0 / +0x248): the host resolves entity ids. Paths through
    // an ENTITY var subscribe in the other entity's bag, so a resolved sync manager must outlive the
    // instances that subscribed through it (or they must be destroyed first).
    std::function<SyncManager*(uint32_t entity)> entity_resolver;
    // Graphs by GUID for PlayScript (the client's resource handles; resident graphs only).
    std::function<const Graph*(uint64_t guid)> graph_provider;
    // PlayScript on this entity (component 0x23, 0xcaa820): stacking behaviour 0 = not while an instance of
    // the graph runs, 3 = stop it and play again. Creates a local instance with the auto-stop flag,
    // instantiates it at once when executing, flushes (RunCommandFrames(cf, 0), nested) and delivers the
    // parameters. Returns the instance or nullptr.
    Instance* PlayScript(uint64_t graph, const std::vector<std::pair<uint64_t, Primitive>>& params, uint32_t sender,
                         uint32_t stacking);
    // Reads `path` in entity `entity`'s Statescript, starting in its entity bag [?: start bag not read].
    bool ReadEntityVar(uint32_t entity, std::span<const uint64_t> path, Primitive& out);
    // StackPriority records (GUID type 0x0C4, STU 98b439ca: m_priority f32, 0xa6518307 wins ties) [G][C]: the
    // host supplies them from content; unknown → priority 0, no tie win.
    struct StackPriority {
        float priority = 0.0f;
        bool wins_ties = false;
    };
    std::map<uint64_t, StackPriority> stack_priorities;
    // What an imported state stands for. false (default, the server's seed at lock-in): a state activated with the
    // imported data, like the client's full-update apply (activate, then the reader). true (replays of a client
    // snapshot taken mid-life): a state active for a while, left as its last update left it (e.g. an idle Ability is
    // off the ticking list: the reader 0x10871b0 doesn't tick, only the activate 0x1086a00 does).
    bool import_resumes = false;
    StackPriority FindStackPriority(uint64_t guid) const;
    // The stack registry (SyncMgr +0x10) [G]: members bottom → top per key {bag instance id or 0, var id}.
    struct StackEntry {
        std::vector<StackMember*> members;
        Var* var = nullptr;
    };
    const std::map<uint32_t, StackEntry>& stacks() const { return stacks_; }
    void StackPush(StackMember& m, Var& var, uint32_t key, uint32_t pred, bool has_pred, bool notify);
    void StackRemove(StackMember& m, bool notify);
    void OnStoredChanged(Var& var) override { Recompute(var, true); }
    // Recompute the fold of a stacked var (0xf59d40), then notify its subscribers.
    void Recompute(Var& var, bool notify);
    // A client command for command frame `cf` (EmitButtonEdges 0x1071e40): edges of the button mask against the
    // previous command in the fixed order, then a down + up pair for a non-zero action code, all at cf × 16,
    // to the instances whose graphs route that button (0xf446e0). Call before RunCommandFrames(cf, 1).
    void Input(uint32_t cf, uint16_t buttons, uint8_t action);
    // Queue one LogicalButton notification to the instances that route it; returns how many got it.
    int DispatchButton(int32_t button, bool down, int64_t time);
    // Host: whether an entity's input is blocked (instance vfunc +0x220 = 0x107f3b0: a flag of its component 0x10);
    // LogicalButton states then ignore downs unless configured not to. Unset = never blocked.
    std::function<bool(uint32_t entity)> input_blocked;
    // Host: an f32 field of a loaded resource (the client's global resource lookup 0x9c6470 + offset), e.g. a
    // vibration asset's length. Unset = no asset data.
    std::function<bool(uint64_t guid, uint32_t offset, float& out)> resource_float;
    // Host: the engine's global clock in ms (0xe51d10) that some states publish (cd396790); unset = this clock.
    std::function<double()> global_clock_ms;
    // Host: the match world for the match / objective node types (T055; include/statescript/match_host.h).
    // Unset = no world: those types run their lifecycle only.
    std::shared_ptr<MatchHost> match_host;
    // Asset data for fafb9471 HeroHasIdentifier (T060): whether hero `hero`'s STU list 99efbfd6 holds `identifier`.
    std::function<bool(uint64_t hero, uint64_t identifier)> hero_has_identifier;
    // Host: the collision cast of a SingleRaycast action (757a8a94, client 0x107b310: the entity's collision world
    // with m_ignoreEntities / m_startPosWS / m_dirWS / m_castLength / m_castFilter; the host reads those fields of
    // `node` itself, ss_sm_eval_field). Return false for no hit. Unset = never hits.
    struct RaycastHit {
        uint32_t entity = 0;  // the entity hit, 0 for static geometry
        float point[3] = {}, normal[3] = {};
    };
    std::function<bool(int16_t instance, int node, RaycastHit& hit)> raycast;
    // Held logical buttons (SyncMgr +0x380), maintained by the LogicalButton notifications.
    std::vector<int32_t> held_buttons;
    // The latest LogicalButton notification executed (SyncMgr +0x378 button, init 0xC3; +0x377 down).
    int32_t last_button = 0xC3;
    bool last_down = false;
    bool Held(int32_t button) const;
    // World-change bits (component watchers, §3.8) applied in the first pass of the next RunCommandFrames.
    void AddWorldChange(uint32_t bits) { world_pending_ |= bits; }
    // Sync-manager-level queue (+0x308): run at the start of the pass at `time`; takes part in NextTime.
    void PostEvent(int64_t time, std::function<void()> fn);

    // Executed actions of mirrored instances (id > 0), the client's "action executed" records (sm vfunc +0x158,
    // sync spec §2.9): {cf, instance, m_nodes index}, the last kActionLogCfs command frames.
    struct ActionRecord {
        uint32_t cf;
        int16_t instance;
        int32_t node;
    };
    static constexpr uint32_t kActionLogCfs = 256;
    // Server-only node types (no client class: ModifyHealth, HealthPool, Criteria, …; docs/StatescriptNodes.md
    // §3.9): the runtime runs their lifecycle and logs them for the host, whose game systems produce the effects.
    // kind 0 action executed, 1 state begin, 2 state finished, 3 state aborted.
    struct HostEvent {
        uint32_t cf;
        int16_t instance;
        int32_t node, state;
        uint32_t type;
        int32_t kind;
    };
    const std::vector<HostEvent>& host_events() const { return host_events_; }
    void RecordHostEvent(const Instance& inst, const Node& n, int32_t kind);
    // Optional synchronous hook for the same events (C++ hosts, tests).
    std::function<void(const HostEvent&)> host_event_hook;
    const std::vector<ActionRecord>& actions() const { return actions_; }
    void RecordAction(const Instance& inst, int node);

    // Value providers (SyncMgr +0x410, walked last to first by the expressions' op 36, instance vfunc +0xC8
    // 0xf4d580): given the last id of a Dynamic path, supply its value (e.g. CycleIndex's candidate index).
    std::vector<std::function<bool(uint64_t id, Primitive& out)>> value_providers;
    bool ProvideValue(uint64_t id, Primitive& out) const {
        for (size_t k = value_providers.size(); k-- > 0;)
            if (value_providers[k](id, out)) return true;
        return false;
    }
    // A Dictionary var in another entity's entity bag (expression ops 32–34 after op 35).
    const Var* FindEntityDictionary(uint32_t entity, std::span<const uint64_t> path);

    // Tooling / tests.
    TraceSink trace;
    std::function<void(int pass)> after_instances;  // called after the instances of every pass
    void Emit(TraceEvent ev) const;
    uint32_t NextSeq() { return ++seq_; }

    // Report of what the runtime did not model (explicit no-ops), by STU type hash.
    std::map<uint32_t, int> unmodelled_nodes, unmodelled_states, unmodelled_config_vars;
    std::vector<std::string> warnings;

private:
    friend class Instance;
    bool NextTime(int pass, bool first);

    uint32_t owner_;
    const EvaluatorRegistry* evals_;
    Role role_;
    VarBag entity_bag_{varid::EntityBag};
    std::vector<std::unique_ptr<Instance>> instances_;
    std::vector<Instance*> pending_instantiate_;
    uint32_t cf_ = 0;
    int64_t time_ = 0, end_ = 0;
    bool executing_ = false, in_run_ = false;
    uint32_t seq_ = 0, pass_seq_start_ = 0;
    uint32_t world_pending_ = 0;
    std::multimap<int64_t, std::function<void()>> events_;
    std::map<uint32_t, StackEntry> stacks_;
    uint16_t prev_buttons_ = 0;
    std::vector<ActionRecord> actions_;
    std::vector<HostEvent> host_events_;

public:
    // Per-type member lists (TypeListMember), by STU type, in activation order.
    std::map<uint32_t, std::vector<TypeListMember*>> type_lists;

private:
};

// The logical button of a command action code (table 0x173e680), -1 if none (§5.10.2).
int32_t ActionButton(uint8_t code);

}  // namespace ss
