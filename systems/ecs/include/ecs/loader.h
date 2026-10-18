#pragma once
#include "types.h"
#include <span>
#include <vector>

namespace ecs {

class Entity;

// Describes what to spawn. Mirrors the client EntityLoader vtable (docs/ECS.md §5).
class EntityLoader {
public:
    virtual ~EntityLoader() = default;

    // vf[0x00]/vf[0x08] iteration: components requested by the loader, in loader order.
    virtual std::span<const ComponentId> Components() const = 0;
    // vf[0x18]/vf[0x20] iteration: child loaders, spawned before this entity is finalized.
    virtual std::span<const EntityLoader* const> Children() const { return {}; }
    // vf[0x38]: tag given to this entity when it is spawned as a child (0 = none).
    virtual uint32_t ChildTag() const { return 0; }
    // vf[0x30]: entity UUID.
    virtual Uuid GetUuid() const { return {}; }
    // vf[0x50]: high bits of an allocated entity id.
    virtual EntityId IdHighBits() const { return 0; }
    // vf[0x40]: after all components exist and are sorted, before any Component::OnCreate.
    virtual void OnComponentsCreated(Entity&) const {}
    // vf[0x48]: after Component::OnPostCreate, before the systems' OnEntityCreated.
    virtual void OnEntityCreated(Entity&) const {}
};

// Plain data loader.
class BasicLoader : public EntityLoader {
public:
    std::vector<ComponentId> components;
    std::vector<const EntityLoader*> children;
    uint32_t child_tag = 0;
    EntityId id_high_bits = 0;
    Uuid uuid{};

    BasicLoader() = default;
    explicit BasicLoader(std::vector<ComponentId> c, EntityId high_bits = 0)
        : components(std::move(c)), id_high_bits(high_bits) {}

    std::span<const ComponentId> Components() const override { return components; }
    std::span<const EntityLoader* const> Children() const override { return children; }
    uint32_t ChildTag() const override { return child_tag; }
    Uuid GetUuid() const override { return uuid; }
    EntityId IdHighBits() const override { return id_high_bits; }
};

} // namespace ecs
