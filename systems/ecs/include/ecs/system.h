#pragma once
#include "types.h"
#include <span>

namespace ecs {

class Entity;
class EntityAdmin;

// Base system. Virtuals mirror the client system vtable slots (docs/ECS.md §3).
class System {
public:
    virtual ~System() = default;

    // Slot 5: EA initialization; any false fails EntityAdmin::Initialize.
    virtual bool OnInitialize() { return true; }
    // Slot 7: EA shutdown.
    virtual void PreDelete() {}
    // Slot 8: component ids this system is matched on (kSubscribeAll = every entity). Read once
    // at Initialize. An entity with n of these components gets each entity callback n times.
    virtual std::span<const ComponentId> Subscriptions() const { return {}; }

    // Slot 9: in FlushCreated, before the entity's components enter the iteration lists.
    virtual void OnEntityAdded(Entity&) {}
    // Slot 10: during spawn, right after the entity is finalized.
    virtual void OnEntityCreated(Entity&) {}
    // Slot 11: on a delete request.
    virtual void OnEntityDeleting(Entity&) {}
    // Slot 12: at destroy (skipped for kFlagExternalStorage entities).
    virtual void OnEntityRemoved(Entity&) {}
    // Slot 13: once per EntityAdmin::Tick, in registration order.
    virtual void OnTick(float /*dt*/) {}

    EntityAdmin* admin() const { return admin_; }

private:
    friend class EntityAdmin;
    EntityAdmin* admin_ = nullptr;
};

} // namespace ecs
