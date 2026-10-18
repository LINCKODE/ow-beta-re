#pragma once
#include "component.h"
#include "types.h"
#include <bit>
#include <memory>
#include <span>
#include <vector>

namespace ecs {

class EntityAdmin;

class Entity {
public:
    EntityId id() const { return id_; }
    uint32_t flags() const { return flags_; }
    EntityAdmin* admin() const { return admin_; }
    const Uuid& uuid() const { return uuid_; }
    uint32_t child_tag() const { return child_tag_; }

    Entity* parent() const { return parent_; }
    Entity* first_child() const { return first_child_; }
    Entity* next_sibling() const { return next_sibling_; }

    bool Has(ComponentId id) const { return (mask_[id >> 6] >> (id & 63)) & 1; }

    // O(1) rank lookup into the id-sorted component list (client FUN_7ff650809b00 keeps the
    // prefix popcounts up to date).
    Component* Get(ComponentId id) const {
        if (!Has(id)) return nullptr;
        uint64_t below = mask_[id >> 6] & ((uint64_t(1) << (id & 63)) - 1);
        return components_[prefix_[id >> 6] + std::popcount(below)];
    }
    template <class T> T* Get(ComponentId id) const { return static_cast<T*>(Get(id)); }

    // Sorted by component id once the entity is finalized.
    std::span<Component* const> components() const { return components_; }

private:
    friend class EntityAdmin;

    void RebuildRank() {
        prefix_[0] = 0;
        prefix_[1] = uint8_t(std::popcount(mask_[0]));
        prefix_[2] = uint8_t(prefix_[1] + std::popcount(mask_[1]));
    }

    EntityAdmin* admin_ = nullptr;
    EntityId id_ = 0;
    uint32_t flags_ = 0;
    uint32_t child_tag_ = 0;
    Uuid uuid_{};
    Entity* parent_ = nullptr;
    Entity* first_child_ = nullptr;
    Entity* next_sibling_ = nullptr;
    std::vector<Component*> components_;          // owned; see EntityAdmin::DestroyComponents
    std::vector<std::unique_ptr<Component>> storage_;
    uint64_t mask_[3] = {};
    uint8_t prefix_[3] = {};
};

} // namespace ecs
