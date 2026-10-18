#include "ecs/entity_admin.h"
#include <algorithm>
#include <cassert>
#include <cstring>

namespace ecs {

EntityAdmin::EntityAdmin(const ComponentRegistry& registry) : registry_(registry) {}

EntityAdmin::~EntityAdmin() {
    // Teardown without callbacks: components go before the entity maps and systems.
    entities_.clear();
    systems_.clear();
}

std::vector<EntityAdmin::Callback>& EntityAdmin::GlobalPreTick() {
    static std::vector<Callback> v;
    return v;
}
std::vector<EntityAdmin::Callback>& EntityAdmin::GlobalPostTick() {
    static std::vector<Callback> v;
    return v;
}
std::vector<EntityAdmin::Callback>& EntityAdmin::GlobalFlushDestroy() {
    static std::vector<Callback> v;
    return v;
}

// --- systems -------------------------------------------------------------------------------

System* EntityAdmin::AppendSystem(std::unique_ptr<System> system) {
    return InsertSystemAt(systems_.size(), std::move(system));
}

System* EntityAdmin::InsertSystemAt(size_t index, std::unique_ptr<System> system) {
    system->admin_ = this;
    auto* raw = system.get();
    systems_.insert(systems_.begin() + index, std::move(system));
    return raw;
}

int EntityAdmin::SystemIndex(const System* s) const {
    for (size_t i = 0; i < systems_.size(); i++)
        if (systems_[i].get() == s) return int(i);
    return -1;
}

bool EntityAdmin::Initialize() {
    for (auto& s : systems_)
        if (!s->OnInitialize()) return false;
    // No de-duplication: a system appears once per subscribed id.
    for (auto& s : systems_)
        for (auto id : s->Subscriptions()) subscribers_[id].push_back(s.get());
    OnPostInitialize();
    initialized_ = true;
    return true;
}

void EntityAdmin::Shutdown() {
    OnPreShutdown();
    FlushDestroy();
    for (auto& s : systems_) s->PreDelete();
    FlushDestroy();
    initialized_ = false;
}

// --- tracing -------------------------------------------------------------------------------

void EntityAdmin::Trace(TraceKind kind, const Entity* e, uint64_t a, uint64_t b, int sys, bool with_comps) {
    if (!trace_) return;
    TraceEvent ev;
    ev.seq = ++trace_seq_;
    ev.tick = tick_count_;
    ev.phase = phase_;
    ev.phase_sys = (phase_ == TracePhase::SystemTick || phase_ >= TracePhase::CfListener) ? phase_sys_ : -1;
    ev.phase_cf = phase_ == TracePhase::CfListener ? phase_cf_ : 0;
    ev.kind = kind;
    ev.sys = sys;
    ev.ent = e ? e->id_ : 0;
    ev.a = a;
    ev.b = b;
    if (with_comps && e)
        for (auto* c : e->components_) ev.comps.push_back(c->id_);
    trace_->Record(ev);
}

template <class F> void EntityAdmin::NotifySystems(Entity& e, TraceKind kind, F&& call) {
    std::vector<System*> matched;
    MatchSystems(e, matched);
    for (auto* s : matched) {
        Trace(kind, &e, 0, 0, SystemIndex(s));
        call(*s);
    }
}

// FUN_7ff6507ef680: the "every entity" list, then each component's list in ascending id.
void EntityAdmin::MatchSystems(const Entity& e, std::vector<System*>& out) const {
    out.insert(out.end(), subscribers_[kSubscribeAll].begin(), subscribers_[kSubscribeAll].end());
    for (auto* c : e.components_) {
        auto& subs = subscribers_[c->id_];
        out.insert(out.end(), subs.begin(), subs.end());
    }
}

// --- spawn ---------------------------------------------------------------------------------

Entity* EntityAdmin::Find(EntityId id) const {
    auto it = entities_.find(id);
    return it != entities_.end() ? it->second.get() : nullptr;
}

Entity* EntityAdmin::Allocate(const EntityLoader& loader, EntityId requested) {
    EntityId id = requested;
    if (requested == kDefaultEntityId || (requested & kEntityIdLowMask) == kEntityIdLowMask) {
        id = (++id_counter_) | loader.IdHighBits();
    } else if (Find(requested)) {
        return nullptr;
    }
    auto e = std::make_unique<Entity>();
    e->admin_ = this;
    e->id_ = id;
    e->uuid_ = loader.GetUuid();
    auto* raw = e.get();
    entities_[id] = std::move(e);
    return raw;
}

Entity* EntityAdmin::Spawn(const EntityLoader& loader, EntityId requested) {
    Trace(TraceKind::SpawnBegin, nullptr, requested);
    Entity* e = Allocate(loader, requested);
    if (e) {
        // Children are spawned (and finalized) before their parent is finalized, and prepended.
        for (const EntityLoader* child_loader : loader.Children()) {
            Entity* child = Spawn(*child_loader, kDefaultEntityId);
            if (!child) continue; // the client doesn't handle this (default ids can't collide)
            if (auto tag = child_loader->ChildTag()) {
                e->flags_ |= kFlagHasTaggedChild;
                child->child_tag_ = tag;
            }
            child->parent_ = e;
            child->next_sibling_ = e->first_child_;
            e->first_child_ = child;
        }
        Finalize(*e, loader);
    }
    Trace(TraceKind::SpawnEnd, e, requested, 0, -1, true);
    return e;
}

Entity* EntityAdmin::SpawnInline(std::span<const ComponentId> components, const EntityLoader& loader) {
    Trace(TraceKind::SpawnInlineBegin, nullptr, components.size());
    auto owned = std::make_unique<Entity>();
    Entity& e = *owned;
    e.admin_ = this;
    e.id_ = (++id_counter_) | 0x80000000u;
    e.flags_ = kFlagExternalStorage;
    if (!components.empty())
        if (auto* last = registry_.Get(components.back()); last && last->inline_entity_allocator)
            e.flags_ |= kFlagCustomDeallocator;
    entities_[e.id_] = std::move(owned);
    loader.OnComponentsCreated(e); // vf+0x40, before the components exist
    for (ComponentId id : components) e.mask_[id >> 6] |= uint64_t(1) << (id & 63);
    e.RebuildRank();
    for (ComponentId id : components) {
        const ComponentType* type = registry_.Get(id);
        std::unique_ptr<Component> c = type && type->create ? type->create() : std::make_unique<Component>();
        c->id_ = id;
        c->entity_ = &e;
        c->OnCreate(loader); // the in-place constructor gets the loader
        e.components_.push_back(c.get());
        e.storage_.push_back(std::move(c));
    }
    e.flags_ |= kFlagCreated;
    for (auto* c : e.components_) c->OnPostCreate();
    Trace(TraceKind::SpawnInlineEnd, &e, components.size(), e.flags_, -1, true);
    return &e;
}

Component* EntityAdmin::AddComponent(Entity& e, ComponentId id) {
    const ComponentType* type = registry_.Get(id);
    Component* raw = nullptr;
    if (type && type->create) {
        auto c = type->create();
        c->id_ = id;
        c->entity_ = &e;
        c->order_key_ = pools_[id].Allocate();
        raw = c.get();
        e.storage_.push_back(std::move(c));
        e.components_.push_back(raw);
        e.mask_[id >> 6] |= uint64_t(1) << (id & 63);
        e.RebuildRank();
    }
    Trace(TraceKind::AddComponent, &e, id, raw != nullptr);
    return raw;
}

void EntityAdmin::AddDependencies(Entity& e, ComponentId id) {
    const ComponentType* type = registry_.Get(id);
    if (!type) return;
    for (ComponentId dep : type->dependencies)
        if (!e.Has(dep) && AddComponent(e, dep)) AddDependencies(e, dep);
}

void EntityAdmin::Finalize(Entity& e, const EntityLoader& loader) {
    for (ComponentId id : loader.Components()) AddComponent(e, id);
    for (ComponentId id : loader.Components()) AddDependencies(e, id);
    std::sort(e.components_.begin(), e.components_.end(),
              [](const Component* a, const Component* b) { return a->id_ < b->id_; });

    loader.OnComponentsCreated(e);
    for (auto* c : e.components_) c->OnCreate(loader);
    e.flags_ |= kFlagCreated;
    for (auto* c : e.components_) c->OnPostCreate();
    loader.OnEntityCreated(e);

    NotifySystems(e, TraceKind::OnCreated, [&](System& s) { s.OnEntityCreated(e); });

    e.flags_ |= kFlagPendingCreated;
    pending_created_.push_back(&e);
}

// FUN_7ff6507f0600. The client holds a non-reentrant spinlock here, so callbacks must not spawn;
// we process a snapshot and leave later spawns for the next flush.
void EntityAdmin::FlushCreated() {
    if (!pending_created_.empty()) Trace(TraceKind::FlushCreated, nullptr, pending_created_.size());
    PhaseScope ps(*this, TracePhase::FlushCreated);
    std::vector<Entity*> batch;
    batch.swap(pending_created_);
    for (Entity* e : batch) {
        e->flags_ &= ~kFlagPendingCreated;
        NotifySystems(*e, TraceKind::OnAdded, [&](System& s) { s.OnEntityAdded(*e); });
        for (auto it = e->components_.rbegin(); it != e->components_.rend(); ++it) {
            Component* c = *it;
            const ComponentType* type = registry_.Get(c->id_);
            if (!type || !type->tracked()) continue;
            auto& list = iteration_[c->id_];
            auto pos = std::lower_bound(list.begin(), list.end(), c,
                                        [](const Component* a, const Component* b) { return a->order_key_ < b->order_key_; });
            list.insert(pos, c);
        }
    }
}

// --- delete / destroy ----------------------------------------------------------------------

void EntityAdmin::SetParent(Entity& e, Entity* parent) {
    if (e.parent_ == parent) return;
    if (Entity* p = e.parent_) {
        if (p->first_child_ == &e) {
            p->first_child_ = e.next_sibling_;
        } else {
            Entity* prev = p->first_child_;
            while (prev->next_sibling_ != &e) prev = prev->next_sibling_;
            prev->next_sibling_ = e.next_sibling_;
        }
        e.parent_ = nullptr;
        e.next_sibling_ = nullptr;
    }
    if (parent) {
        e.parent_ = parent;
        e.next_sibling_ = parent->first_child_;
        parent->first_child_ = &e;
    }
}

void EntityAdmin::RequestDelete(Entity& e, bool immediate) {
    Trace(TraceKind::DeleteRequest, &e, immediate, e.flags_);
    SetParent(e, nullptr);
    while (e.first_child_) RequestDelete(*e.first_child_, immediate);
    DeleteNow(e, immediate);
    Trace(TraceKind::DeleteRequestEnd, &e, 0, e.flags_);
}

void EntityAdmin::DeleteNow(Entity& e, bool immediate) {
    if (e.flags_ & (kFlagPendingDestroy | kFlag20)) return;
    e.flags_ |= immediate ? (kFlagDeleteRequested | kFlagDeleteImmediate) : kFlagDeleteRequested;

    NotifySystems(e, TraceKind::OnDeleting, [&](System& s) { s.OnEntityDeleting(e); });

    bool all = true;
    for (auto it = e.components_.rbegin(); it != e.components_.rend(); ++it)
        if (!(*it)->CanDelete()) all = false; // every component is asked
    if (!all) return;
    if (!(e.flags_ & kFlagPendingDestroy)) {
        e.flags_ |= kFlagPendingDestroy;
        pending_destroy_.push_back(&e);
    }
}

void EntityAdmin::QueueDelete(Entity& e) { delete_requests_.push_back(&e); }

void EntityAdmin::FlushDestroy() {
    if (!delete_requests_.empty() || !pending_destroy_.empty())
        Trace(TraceKind::FlushDestroy, nullptr, delete_requests_.size(), pending_destroy_.size());
    PhaseScope ps(*this, TracePhase::FlushDestroy);
    for (auto& cb : GlobalFlushDestroy()) cb(*this);
    while (!delete_requests_.empty()) {
        std::vector<Entity*> batch;
        batch.swap(delete_requests_);
        for (Entity* e : batch) RequestDelete(*e, (e->flags_ & kFlagDeleteImmediate) != 0);
    }
    while (!pending_destroy_.empty()) {
        std::vector<Entity*> batch;
        batch.swap(pending_destroy_);
        for (Entity* e : batch) Destroy(*e);
    }
}

void EntityAdmin::Destroy(Entity& e) {
    Trace(TraceKind::Destroy, &e, 0, e.flags_, -1, true);
    if (e.flags_ & kFlagPendingCreated) {
        auto it = std::find(pending_created_.begin(), pending_created_.end(), &e);
        if (it != pending_created_.end()) { // swap-remove, as the client
            *it = pending_created_.back();
            pending_created_.pop_back();
        }
        e.flags_ &= ~kFlagPendingCreated;
    }
    if (!(e.flags_ & kFlagExternalStorage)) {
        NotifySystems(e, TraceKind::OnRemoved, [&](System& s) { s.OnEntityRemoved(e); });
        for (auto it = e.components_.rbegin(); it != e.components_.rend(); ++it) {
            Component* c = *it;
            const ComponentType* type = registry_.Get(c->id_);
            if (!type || !type->tracked()) continue;
            auto& list = iteration_[c->id_];
            auto pos = std::lower_bound(list.begin(), list.end(), c,
                                        [](const Component* a, const Component* b) { return a->order_key_ < b->order_key_; });
            if (pos != list.end() && *pos == c) list.erase(pos);
        }
    }
    // FUN_7ff650809d00: components are destroyed in reverse list order (highest id first for
    // pool entities); inline ones get OnInlineDestroy first.
    bool inline_storage = e.flags_ & kFlagExternalStorage;
    for (auto it = e.components_.rbegin(); it != e.components_.rend(); ++it) {
        Component* c = *it;
        if (inline_storage) {
            c->OnInlineDestroy();
        } else {
            pools_[c->id_].Free(c->order_key_);
        }
        for (auto s = e.storage_.begin(); s != e.storage_.end(); ++s)
            if (s->get() == c) {
                e.storage_.erase(s);
                break;
            }
    }
    e.components_.clear();
    entities_.erase(e.id_);
}

// --- tick ----------------------------------------------------------------------------------

void EntityAdmin::Tick(float dt) {
    ++tick_count_;
    PhaseScope ps(*this, TracePhase::Tick);
    OnPreTick();
    FlushDestroy();
    FlushCreated();
    for (auto& cb : GlobalPreTick()) cb(*this);
    size_t n = systems_.size();
    for (size_t i = 0; i < n; i++) {
        System& s = *systems_[i];
        OnPreSystemTick(s);
        {
            PhaseScope sys_phase(*this, TracePhase::SystemTick, int(i));
            s.OnTick(dt);
        }
        OnPostSystemTick(s);
    }
    for (auto& cb : GlobalPostTick()) cb(*this);
    OnPostTick();
}

// --- command frames ------------------------------------------------------------------------

void EntityAdmin::RunCommandFrame(uint32_t cf, uint32_t flags) {
    for (size_t i = 0; i < cf_listeners_.size(); i++) {
        auto* l = cf_listeners_[i];
        TraceCommandFrameListener(TraceKind::CfListener, l, cf, flags);
        PhaseScope ps(*this, TracePhase::CfListener, int(i), cf);
        l->OnCommandFrame(cf, flags);
    }
    // Drop deferred entries older than this CF (swap with the last one, re-check the slot).
    for (size_t i = 0; i < deferred_cf_.size();) {
        uint32_t f = deferred_cf_[i].command_frame;
        if (f != 0 && f < cf) {
            deferred_cf_[i] = deferred_cf_.back();
            deferred_cf_.pop_back();
        } else {
            i++;
        }
    }
}

void EntityAdmin::RunCommandFramesPre() {
    for (size_t i = 0; i < cf_listeners_.size(); i++) {
        TraceCommandFrameListener(TraceKind::CfPre, cf_listeners_[i]);
        PhaseScope ps(*this, TracePhase::CfPre, int(i));
        cf_listeners_[i]->OnPreCommandFrames();
    }
}

void EntityAdmin::RunCommandFramesPost() {
    for (size_t i = 0; i < cf_listeners_.size(); i++) {
        TraceCommandFrameListener(TraceKind::CfPost, cf_listeners_[i]);
        PhaseScope ps(*this, TracePhase::CfPost, int(i));
        cf_listeners_[i]->OnPostCommandFrames();
    }
}

void EntityAdmin::TraceClockDilate(float dt, uint8_t flags) {
    uint32_t bits;
    std::memcpy(&bits, &dt, 4);
    Trace(TraceKind::ClockDilate, nullptr, bits, flags);
}

void EntityAdmin::TraceClockAdvance(int64_t us) {
    Trace(TraceKind::ClockAdvance, nullptr, uint64_t(us),
          uint64_t(clock_.command_frame) | (uint64_t(uint32_t(clock_.accumulator_us)) << 32));
}

void EntityAdmin::ResyncClock(uint32_t cf) {
    Trace(TraceKind::ClockResync, nullptr, cf, clock_.command_frame);
    clock_.Resync(cf);
}

void EntityAdmin::TraceCommandFrameRange(uint32_t first, uint32_t last) {
    Trace(TraceKind::CfRange, nullptr, first, last);
}

void EntityAdmin::TraceCommandFrameListener(TraceKind kind, CommandFrameListener* l, uint32_t cf, uint32_t flags) {
    int index = -1;
    for (size_t i = 0; i < cf_listeners_.size(); i++)
        if (cf_listeners_[i] == l) index = int(i);
    Trace(kind, nullptr, cf, flags, index);
}

// --- names ---------------------------------------------------------------------------------

const char* TraceKindName(TraceKind k) {
    switch (k) {
    case TraceKind::SpawnBegin: return "spawn_begin";
    case TraceKind::SpawnEnd: return "spawn_end";
    case TraceKind::AddComponent: return "add_component";
    case TraceKind::OnAdded: return "on_added";
    case TraceKind::OnCreated: return "on_created";
    case TraceKind::OnDeleting: return "on_deleting";
    case TraceKind::OnRemoved: return "on_removed";
    case TraceKind::DeleteRequest: return "delete_request";
    case TraceKind::DeleteRequestEnd: return "delete_request_end";
    case TraceKind::Destroy: return "destroy";
    case TraceKind::FlushCreated: return "flush_created";
    case TraceKind::FlushDestroy: return "flush_destroy";
    case TraceKind::Tick: return "tick";
    case TraceKind::SpawnInlineBegin: return "spawn_inline_begin";
    case TraceKind::SpawnInlineEnd: return "spawn_inline_end";
    case TraceKind::ClockDilate: return "clock_dilate";
    case TraceKind::ClockAdvance: return "clock_advance";
    case TraceKind::CfRange: return "cf_range";
    case TraceKind::CfPre: return "cf_pre";
    case TraceKind::CfListener: return "cf_listener";
    case TraceKind::CfPost: return "cf_post";
    case TraceKind::ClockResync: return "clock_resync";
    }
    return "?";
}

const char* TracePhaseName(TracePhase p) {
    switch (p) {
    case TracePhase::None: return "none";
    case TracePhase::FlushDestroy: return "flush_destroy";
    case TracePhase::FlushCreated: return "flush_created";
    case TracePhase::SystemTick: return "system_tick";
    case TracePhase::Tick: return "tick";
    case TracePhase::CfListener: return "cf_listener";
    case TracePhase::CfPre: return "cf_pre";
    case TracePhase::CfPost: return "cf_post";
    }
    return "?";
}

} // namespace ecs
