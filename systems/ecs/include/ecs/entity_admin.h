#pragma once
// EntityAdmin: behaviour reference docs/ECS.md. Function names in comments are the client's
// (Ghidra FUN_ addresses) so each piece can be checked against the binary.
#include "command_frame.h"
#include "component.h"
#include "entity.h"
#include "loader.h"
#include "system.h"
#include "trace.h"
#include <array>
#include <functional>
#include <memory>
#include <span>
#include <unordered_map>
#include <vector>

namespace ecs {

class EntityAdmin {
public:
    using Callback = std::function<void(EntityAdmin&)>;

    explicit EntityAdmin(const ComponentRegistry& registry);
    virtual ~EntityAdmin();
    EntityAdmin(const EntityAdmin&) = delete;
    EntityAdmin& operator=(const EntityAdmin&) = delete;

    // --- systems (FUN_7ff6507ef3f0 / FUN_7ff6507ef450) ---------------------------------
    System* AppendSystem(std::unique_ptr<System> system);
    // Inserts before the first system of type T. Like the client, drops the system (returns
    // nullptr) if there is none.
    template <class T> System* InsertSystemBefore(std::unique_ptr<System> system) {
        for (size_t i = 0; i < systems_.size(); i++)
            if (dynamic_cast<T*>(systems_[i].get())) return InsertSystemAt(i, std::move(system));
        return nullptr;
    }
    template <class T> T* FindSystem() const {
        for (auto& s : systems_)
            if (auto p = dynamic_cast<T*>(s.get())) return p;
        return nullptr;
    }
    std::span<const std::unique_ptr<System>> systems() const { return systems_; }
    int SystemIndex(const System* s) const;

    // FUN_7ff6507ef200: OnInitialize every system (fail on the first false), then build the
    // subscriber lists, then OnPostInitialize.
    bool Initialize();
    // FUN_7ff6507ef380
    void Shutdown();
    bool initialized() const { return initialized_; }

    // --- entities ------------------------------------------------------------------------
    // FUN_7ff6507ef7c0. Returns nullptr if an explicit id is already in use.
    Entity* Spawn(const EntityLoader& loader, EntityId requested = kDefaultEntityId);
    // FUN_7ff6507f0030: lightweight entity. One allocation for entity + components (inline
    // types), components constructed in the given order (not sorted, no dependencies), no system
    // callbacks, no created queue, never in iteration lists. id = ++counter | 0x80000000,
    // flags kFlagExternalStorage (| kFlagCustomDeallocator if the last type has an allocator).
    Entity* SpawnInline(std::span<const ComponentId> components, const EntityLoader& loader);

    // FUN_7ff6507eff50: unlink from the parent, delete children, then (FUN_7ff6507f03f0)
    // OnEntityDeleting and queue for destroy if every component CanDelete().
    void RequestDelete(Entity& entity, bool immediate = false);
    // Delete-request queue (EA+0x48), processed at the next FlushDestroy. [?] The client
    // pushes to it from elsewhere (other threads); the immediate flag is taken from kFlagDeleteImmediate.
    void QueueDelete(Entity& entity);

    // [?] Entities also get parented after creation (inline entities are spawned separately, yet
    // delete recursively); the client function is not identified yet. Unlinks from the old parent,
    // prepends to the new one's children (like Spawn). nullptr = detach.
    void SetParent(Entity& child, Entity* parent);

    Entity* Find(EntityId id) const;
    size_t entity_count() const { return entities_.size(); }

    // Per-type iteration list (EA+0xB0), sorted by component order key.
    std::span<Component* const> ComponentsOfType(ComponentId id) const { return iteration_[id]; }

    // --- ticking -------------------------------------------------------------------------
    // FUN_7ff6507f15d0
    void Tick(float dt);
    // FUN_7ff6507f0600. Also called by systems mid-tick (the client's replication system does).
    void FlushCreated();
    // FUN_7ff6507f0d30
    void FlushDestroy();
    uint64_t tick_count() const { return tick_count_; }

    // Mark n component slots of a type as taken (entities that existed before a replayed trace).
    void ReservePoolSlots(ComponentId id, int n) {
        for (int i = 0; i < n; i++) pools_[id].Allocate();
    }

    // Entity id counter (EA+0xC0), e.g. to continue from a client trace's starting point.
    uint32_t id_counter() const { return id_counter_; }
    void set_id_counter(uint32_t v) { id_counter_ = v; }

    // --- command frames ------------------------------------------------------------------
    // The EA's netcode clock (EA+0x218 → +0x50), advanced by GameClockSystem.
    CommandFrameClock& clock() { return clock_; }
    // EA+0xF0: listeners in registration order (the client registers them in the EA builder).
    void AddCommandFrameListener(CommandFrameListener* l) { cf_listeners_.push_back(l); }
    std::span<CommandFrameListener* const> command_frame_listeners() const { return cf_listeners_; }
    // FUN_7ff650fef420: one CF: every listener's OnCommandFrame, then prune the deferred queue.
    void RunCommandFrame(uint32_t cf, uint32_t flags);
    // Every listener's OnPreCommandFrames / OnPostCommandFrames (CommandFrameSystem's tick).
    void RunCommandFramesPre();
    void RunCommandFramesPost();
    std::vector<DeferredCommandFrameEntry>& deferred_command_frame_queue() { return deferred_cf_; }

    // Trace hooks for the clock/CF systems (same events as the Prometheus tracer with cf=true).
    void TraceClockDilate(float dt, uint8_t flags);
    void TraceClockAdvance(int64_t us);
    // Resync the clock (traced like the client's FUN_7ff650ce64a0).
    void ResyncClock(uint32_t cf);
    void TraceCommandFrameRange(uint32_t first, uint32_t last);
    void TraceCommandFrameListener(TraceKind kind, CommandFrameListener* l, uint32_t cf = 0, uint32_t flags = 0);

    // Around a range of CFs (client: EA vfunc 0x70 → vfunc 0x20 object, flag +0x18 set while
    // simulating; FUN_7ff65101fc30 after). Public so CommandFrameSystem can call them.
    virtual void OnCommandFramesBegin() {}
    virtual void OnCommandFramesEnd() {}

    // Process-wide callback lists, as in the client (globals shared by every EA).
    static std::vector<Callback>& GlobalPreTick();      // 0x1893df0
    static std::vector<Callback>& GlobalPostTick();     // 0x1893e08
    static std::vector<Callback>& GlobalFlushDestroy(); // 0x1893e20

    void SetTraceSink(TraceSink* sink) { trace_ = sink; }

    // Runs fn as work from outside the EA's tick thread (the client spawns map entities from a
    // loader thread under the EA lock): trace events inside get phase None.
    template <class F> void RunExternal(F&& fn) {
        PhaseScope ps(*this, TracePhase::None);
        fn();
    }

protected:
    // EA vtable hooks: +0x30, +0x38, +0x40, +0x48, +0x50, +0x58.
    virtual void OnPostInitialize() {}
    virtual void OnPreShutdown() {}
    virtual void OnPreTick() {}
    virtual void OnPreSystemTick(System&) {}
    virtual void OnPostSystemTick(System&) {}
    virtual void OnPostTick() {}

private:
    System* InsertSystemAt(size_t index, std::unique_ptr<System> system);

    Entity* Allocate(const EntityLoader& loader, EntityId requested);            // FUN_7ff6507efd90
    void Finalize(Entity& e, const EntityLoader& loader);                         // FUN_7ff6507ef9d0
    Component* AddComponent(Entity& e, ComponentId id);                           // FUN_7ff650809a30
    void AddDependencies(Entity& e, ComponentId id);                              // FUN_7ff6507ef8d0
    void MatchSystems(const Entity& e, std::vector<System*>& out) const;          // FUN_7ff6507ef680
    void DeleteNow(Entity& e, bool immediate);                                    // FUN_7ff6507f03f0
    void Destroy(Entity& e);                                                      // FUN_7ff6507f0880

    template <class F> void NotifySystems(Entity& e, TraceKind kind, F&& call);
    void Trace(TraceKind kind, const Entity* e, uint64_t a = 0, uint64_t b = 0, int sys = -1,
               bool with_comps = false);

    struct PhaseScope {
        EntityAdmin& ea;
        TracePhase saved_phase;
        int saved_sys;
        uint32_t saved_cf;
        PhaseScope(EntityAdmin& a, TracePhase p, int sys = -1, uint32_t cf = 0)
            : ea(a), saved_phase(a.phase_), saved_sys(a.phase_sys_), saved_cf(a.phase_cf_) {
            ea.phase_ = p;
            ea.phase_sys_ = sys;
            ea.phase_cf_ = cf;
        }
        ~PhaseScope() {
            ea.phase_ = saved_phase;
            ea.phase_sys_ = saved_sys;
            ea.phase_cf_ = saved_cf;
        }
    };

    const ComponentRegistry& registry_;
    std::vector<std::unique_ptr<System>> systems_;
    std::array<std::vector<System*>, kComponentIdLimit> subscribers_;   // EA+0xB8
    std::array<std::vector<Component*>, kComponentIdLimit> iteration_;  // EA+0xB0
    std::array<ComponentPool, kComponentIdLimit> pools_;
    std::unordered_map<EntityId, std::unique_ptr<Entity>> entities_;    // EA+0x10 id table
    std::vector<Entity*> pending_created_;        // EA+0x90
    std::vector<Entity*> delete_requests_;        // EA+0x48
    std::vector<Entity*> pending_destroy_;        // EA+0x70
    uint32_t id_counter_ = 0;                     // EA+0xC0
    uint64_t tick_count_ = 0;                     // EA+0xC8
    bool initialized_ = false;                    // EA+0xD0

    CommandFrameClock clock_;
    std::vector<CommandFrameListener*> cf_listeners_;   // EA+0xF0
    std::vector<DeferredCommandFrameEntry> deferred_cf_; // EA+0x110

    TraceSink* trace_ = nullptr;
    uint64_t trace_seq_ = 0;
    TracePhase phase_ = TracePhase::None;
    int phase_sys_ = -1;
    uint32_t phase_cf_ = 0;
};

} // namespace ecs
