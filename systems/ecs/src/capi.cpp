#include "ecs.h"
#include "ecs/command_frame_systems.h"
#include "ecs/manifest.h"
#include <algorithm>
#include <cstring>
#include <deque>
#include <map>
#include <tuple>

using namespace ecs;

struct ecs_loader : BasicLoader {
    std::vector<std::unique_ptr<ecs_loader>> owned_children;
};

namespace {

struct Action {
    enum Kind { Spawn, Delete, FlushCreated, SpawnLoader, SpawnInline, ClockResync } kind;
    uint32_t id;
    std::vector<ComponentId> comps;
    uint32_t high_bits;
    bool immediate;
    std::shared_ptr<ecs_loader> loader;
    bool external = false;
    std::vector<std::pair<uint32_t, bool>> can_delete; // applied right before the action
    int64_t id_counter = -1;                            // likewise, if >= 0
    std::vector<std::pair<uint32_t, uint32_t>> parents; // likewise (child, parent)
    struct SideEffect { uint32_t ent, target; bool immediate; };
    std::vector<SideEffect> side_effects;               // likewise
};

void RunActionImpl(EntityAdmin& ea, const Action& a);

void RunAction(EntityAdmin& ea, const Action& a) {
    if (a.id_counter >= 0) ea.set_id_counter(uint32_t(a.id_counter));
    for (auto [child, parent] : a.parents)
        if (Entity* c = ea.Find(child)) ea.SetParent(*c, parent ? ea.Find(parent) : nullptr);
    for (auto se : a.side_effects) {
        Entity* e = ea.Find(se.ent);
        if (!e || e->components().empty()) continue;
        auto* last = static_cast<manifest::DataComponent*>(e->components().back());
        last->on_can_delete = [&ea, se] {
            if (Entity* t = ea.Find(se.target)) ea.RequestDelete(*t, se.immediate);
        };
    }
    for (auto [id, allow] : a.can_delete)
        if (Entity* e = ea.Find(id))
            for (auto* c : e->components()) static_cast<manifest::DataComponent*>(c)->allow_delete = allow;
    if (a.external)
        ea.RunExternal([&] { RunActionImpl(ea, a); });
    else
        RunActionImpl(ea, a);
}

void RunActionImpl(EntityAdmin& ea, const Action& a) {
    switch (a.kind) {
    case Action::Spawn: {
        BasicLoader loader(a.comps, a.high_bits);
        ea.Spawn(loader, a.id);
        break;
    }
    case Action::Delete:
        if (Entity* e = ea.Find(a.id)) ea.RequestDelete(*e, a.immediate);
        break;
    case Action::FlushCreated:
        ea.FlushCreated();
        break;
    case Action::SpawnLoader:
        ea.Spawn(*a.loader, a.id);
        break;
    case Action::ClockResync:
        ea.ResyncClock(a.id);
        break;
    case Action::SpawnInline: {
        BasicLoader loader;
        ea.SpawnInline(a.comps, loader);
        break;
    }
    }
}

} // namespace

struct ecs_admin {
    ComponentRegistry registry;
    EntityAdmin ea{registry};
    VectorTraceSink trace;
    std::vector<std::deque<Action>> scheduled;
    // (phase, listener index, cf) -> actions run inside that listener call
    std::map<std::tuple<int, int, uint32_t>, std::deque<Action>> listener_scheduled;
    bool target_listener = false;
    std::tuple<int, int, uint32_t> listener_target;
    size_t trace_read = 0;
    bool external = false;
    std::vector<std::pair<uint32_t, bool>> pending_can_delete;
    int64_t pending_id_counter = -1;
    std::vector<std::pair<uint32_t, uint32_t>> pending_parents;
    std::vector<Action::SideEffect> pending_side_effects;

    void push(int sys, Action a) {
        a.external = external;
        a.can_delete.swap(pending_can_delete);
        a.id_counter = pending_id_counter;
        a.parents.swap(pending_parents);
        a.side_effects.swap(pending_side_effects);
        if (target_listener) {
            target_listener = false;
            listener_scheduled[listener_target].push_back(std::move(a));
            return;
        }
        pending_id_counter = -1;
        scheduled[sys].push_back(std::move(a));
    }
};

extern "C" {

ecs_admin* ecs_admin_create(const char* name) {
    auto* h = new ecs_admin;
    manifest::RegisterComponents(h->registry);
    if (!manifest::AddSystems(h->ea, name)) {
        delete h;
        return nullptr;
    }
    h->scheduled.resize(h->ea.systems().size());
    for (auto& s : h->ea.systems()) {
        auto* ms = dynamic_cast<manifest::ManifestSystem*>(s.get());
        if (!ms) continue; // real library systems (clock, command frames)
        ms->on_tick = [h](manifest::ManifestSystem& self, float) {
            auto& q = h->scheduled[self.index()];
            while (!q.empty()) {
                Action a = std::move(q.front());
                q.pop_front();
                RunAction(h->ea, a);
            }
        };
    }
    auto run_queue = [h](std::tuple<int, int, uint32_t> key) {
        auto it = h->listener_scheduled.find(key);
        if (it == h->listener_scheduled.end()) return;
        while (!it->second.empty()) {
            Action a = std::move(it->second.front());
            it->second.pop_front();
            RunAction(h->ea, a);
        }
        h->listener_scheduled.erase(key);
    };
    auto listeners = h->ea.command_frame_listeners();
    for (size_t i = 0; i < listeners.size(); i++) {
        auto* ml = dynamic_cast<manifest::ManifestListener*>(listeners[i]);
        if (!ml) continue;
        int idx = int(i);
        ml->on_command_frame = [run_queue, idx](uint32_t cf, uint32_t) { run_queue({0, idx, cf}); };
        ml->on_pre = [run_queue, idx] { run_queue({1, idx, 0}); };
        ml->on_post = [run_queue, idx] { run_queue({2, idx, 0}); };
    }
    h->ea.SetTraceSink(&h->trace);
    h->ea.Initialize();
    return h;
}

void ecs_admin_destroy(ecs_admin* h) { delete h; }

int ecs_system_count(const ecs_admin* h) { return int(h->ea.systems().size()); }

uint32_t ecs_system_vtable_rva(const ecs_admin* h, int i) {
    auto s = h->ea.systems();
    if (i < 0 || size_t(i) >= s.size()) return 0;
    if (auto* ms = dynamic_cast<const manifest::ManifestSystem*>(s[i].get())) return ms->vtable_rva();
    if (dynamic_cast<const GameClockSystem*>(s[i].get())) return 0x15c4be8;
    if (dynamic_cast<const CommandFrameSystem*>(s[i].get())) return 0x15c1b48;
    return 0;
}

uint32_t ecs_spawn(ecs_admin* h, uint32_t id, const uint8_t* comps, int n, uint32_t high) {
    BasicLoader loader(std::vector<ComponentId>(comps, comps + std::max(n, 0)), high);
    Entity* e = h->ea.Spawn(loader, id);
    return e ? e->id() : 0;
}

int ecs_request_delete(ecs_admin* h, uint32_t id, int immediate) {
    Entity* e = h->ea.Find(id);
    if (!e) return 0;
    h->ea.RequestDelete(*e, immediate != 0);
    return 1;
}

int ecs_queue_delete(ecs_admin* h, uint32_t id) {
    Entity* e = h->ea.Find(id);
    if (!e) return 0;
    h->ea.QueueDelete(*e);
    return 1;
}

void ecs_tick(ecs_admin* h, float dt) { h->ea.Tick(dt); }
void ecs_flush_created(ecs_admin* h) { h->ea.FlushCreated(); }
void ecs_flush_destroy(ecs_admin* h) { h->ea.FlushDestroy(); }

static bool valid_sys(const ecs_admin* h, int sys) { return sys >= 0 && size_t(sys) < h->scheduled.size(); }

void ecs_schedule_spawn(ecs_admin* h, int sys, uint32_t id, const uint8_t* comps, int n, uint32_t high) {
    if (!valid_sys(h, sys)) return;
    h->push(sys, {Action::Spawn, id, std::vector<ComponentId>(comps, comps + std::max(n, 0)), high, false, {}});
}

void ecs_schedule_delete(ecs_admin* h, int sys, uint32_t id, int immediate) {
    if (!valid_sys(h, sys)) return;
    h->push(sys, {Action::Delete, id, {}, 0, immediate != 0, {}});
}

void ecs_schedule_flush_created(ecs_admin* h, int sys) {
    if (!valid_sys(h, sys)) return;
    h->push(sys, {Action::FlushCreated, 0, {}, 0, false, {}});
}

ecs_loader* ecs_loader_create(const uint8_t* comps, int n, uint32_t high, uint32_t tag) {
    auto* l = new ecs_loader;
    l->components.assign(comps, comps + std::max(n, 0));
    l->id_high_bits = high;
    l->child_tag = tag;
    return l;
}

void ecs_loader_add_child(ecs_loader* parent, ecs_loader* child) {
    parent->children.push_back(child);
    parent->owned_children.emplace_back(child);
}

void ecs_loader_destroy(ecs_loader* l) { delete l; }

uint32_t ecs_spawn_loader(ecs_admin* h, uint32_t id, const ecs_loader* l) {
    Entity* e = nullptr;
    auto spawn = [&] { e = h->ea.Spawn(*l, id); };
    if (h->external) h->ea.RunExternal(spawn); else spawn();
    return e ? e->id() : 0;
}

uint32_t ecs_spawn_inline(ecs_admin* h, const uint8_t* comps, int n) {
    BasicLoader loader;
    Entity* e = nullptr;
    auto spawn = [&] { e = h->ea.SpawnInline(std::span<const ComponentId>(comps, std::max(n, 0)), loader); };
    if (h->external) h->ea.RunExternal(spawn); else spawn();
    return e->id();
}

int ecs_set_can_delete(ecs_admin* h, uint32_t id, int allow) {
    Entity* e = h->ea.Find(id);
    if (!e) return 0;
    for (auto* c : e->components()) static_cast<manifest::DataComponent*>(c)->allow_delete = allow != 0;
    return 1;
}

void ecs_schedule_can_delete(ecs_admin* h, uint32_t id, int allow) {
    h->pending_can_delete.emplace_back(id, allow != 0);
}

int ecs_set_parent(ecs_admin* h, uint32_t child, uint32_t parent) {
    Entity* c = h->ea.Find(child);
    Entity* p = parent ? h->ea.Find(parent) : nullptr;
    if (!c || (parent && !p)) return 0;
    h->ea.SetParent(*c, p);
    return 1;
}

void ecs_schedule_can_delete_side_effect(ecs_admin* h, uint32_t ent, uint32_t target, int immediate) {
    h->pending_side_effects.push_back({ent, target, immediate != 0});
}

void ecs_schedule_set_parent(ecs_admin* h, uint32_t child, uint32_t parent) { h->pending_parents.emplace_back(child, parent); }

void ecs_schedule_target_listener(ecs_admin* h, int phase, int listener, uint32_t cf) {
    h->target_listener = true;
    h->listener_target = {phase, listener, phase == 0 ? cf : 0};
}

void ecs_clock_seed(ecs_admin* h, uint32_t cf, int64_t acc) { h->ea.clock().Seed(cf, acc); }
void ecs_clock_resync(ecs_admin* h, uint32_t cf) { h->ea.ResyncClock(cf); }
void ecs_schedule_clock_resync(ecs_admin* h, int sys, uint32_t cf) {
    if (!valid_sys(h, sys)) return;
    h->push(sys, {Action::ClockResync, cf, {}, 0, false, {}});
}
void ecs_clock_set_dilation(ecs_admin* h, uint8_t flags) { h->ea.clock().dilation_flags = flags; }
uint32_t ecs_clock_command_frame(const ecs_admin* h) { return const_cast<ecs_admin*>(h)->ea.clock().command_frame; }
int ecs_command_frame_listener_count(const ecs_admin* h) { return int(h->ea.command_frame_listeners().size()); }

void ecs_schedule_id_counter(ecs_admin* h, uint32_t v) { h->pending_id_counter = v; }

void ecs_set_external(ecs_admin* h, int external) { h->external = external != 0; }

uint32_t ecs_id_counter(const ecs_admin* h) { return h->ea.id_counter(); }
void ecs_set_id_counter(ecs_admin* h, uint32_t v) { h->ea.set_id_counter(v); }

void ecs_schedule_spawn_loader(ecs_admin* h, int sys, uint32_t id, ecs_loader* l) {
    std::shared_ptr<ecs_loader> owned(l);
    if (!valid_sys(h, sys)) return;
    h->push(sys, {Action::SpawnLoader, id, {}, 0, false, owned});
}

void ecs_schedule_spawn_inline(ecs_admin* h, int sys, const uint8_t* comps, int n) {
    if (!valid_sys(h, sys)) return;
    h->push(sys, {Action::SpawnInline, 0, std::vector<ComponentId>(comps, comps + std::max(n, 0)), 0, false, {}});
}

uint64_t ecs_tick_count(const ecs_admin* h) { return h->ea.tick_count(); }
int ecs_entity_count(const ecs_admin* h) { return int(h->ea.entity_count()); }

int ecs_entity_components(const ecs_admin* h, uint32_t id, uint8_t* out, int cap) {
    Entity* e = h->ea.Find(id);
    if (!e) return -1;
    int n = 0;
    for (auto* c : e->components())
        if (n < cap) out[n++] = c->id();
    return n;
}

uint32_t ecs_entity_flags(const ecs_admin* h, uint32_t id) {
    Entity* e = h->ea.Find(id);
    return e ? e->flags() : 0;
}

int ecs_iteration(const ecs_admin* h, uint8_t comp, uint32_t* out, int cap) {
    int n = 0;
    for (auto* c : h->ea.ComponentsOfType(comp))
        if (n < cap) out[n++] = c->entity()->id();
    return n;
}

void ecs_reserve_slots(ecs_admin* h, uint8_t comp, int n) { h->ea.ReservePoolSlots(comp, n); }

int ecs_trace_take(ecs_admin* h, ecs_event* out, int cap) {
    int n = 0;
    auto& ev = h->trace.events;
    while (n < cap && h->trace_read < ev.size()) {
        const TraceEvent& e = ev[h->trace_read++];
        ecs_event& o = out[n++];
        std::memset(&o, 0, sizeof o);
        o.seq = e.seq;
        o.tick = e.tick;
        o.phase = int32_t(e.phase);
        o.phase_sys = e.phase_sys;
        o.kind = int32_t(e.kind);
        o.sys = e.sys;
        o.ent = e.ent;
        o.phase_cf = e.phase_cf;
        o.a = e.a;
        o.b = e.b;
        o.comp_count = uint32_t(std::min<size_t>(e.comps.size(), sizeof o.comps));
        std::memcpy(o.comps, e.comps.data(), o.comp_count);
    }
    if (h->trace_read == ev.size()) {
        ev.clear();
        h->trace_read = 0;
    }
    return n;
}

const char* ecs_trace_kind_name(int32_t k) { return TraceKindName(TraceKind(k)); }
const char* ecs_trace_phase_name(int32_t p) { return TracePhaseName(TracePhase(p)); }

} // extern "C"
