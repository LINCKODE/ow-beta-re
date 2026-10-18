#pragma once
#include "types.h"
#include <array>
#include <functional>
#include <memory>
#include <optional>
#include <span>
#include <vector>

namespace ecs {

class Entity;
class EntityLoader;

// Base component. Virtuals mirror the client component vtable (docs/ECS.md §4).
class Component {
public:
    virtual ~Component() = default;

    // vf+0x10: called during spawn finalize, ascending component id, after the loader's
    // OnComponentsCreated.
    virtual void OnCreate(const EntityLoader&) {}
    // vf+0x20: called after every component's OnCreate and after kFlagCreated is set.
    virtual void OnPostCreate() {}
    // vf+0x28: asked (reverse id order, every component, no short-circuit) on a delete request;
    // the entity is queued for destroy only if all return true.
    virtual bool CanDelete() { return true; }
    // vf+0x18: called on inline components right before they are destroyed (pool components are
    // just destroyed by their creator).
    virtual void OnInlineDestroy() {}

    ComponentId id() const { return id_; }
    Entity* entity() const { return entity_; }
    // Position in the EA per-type iteration list. The client sorts these lists by component
    // address; we use the pool slot as the address stand-in (see ComponentPool).
    uint64_t order_key() const { return order_key_; }

private:
    friend class EntityAdmin;
    ComponentId id_ = 0;
    Entity* entity_ = nullptr;
    uint64_t order_key_ = 0;
};

struct ComponentType {
    ComponentId id = 0;
    std::function<std::unique_ptr<Component>()> create;
    // Added automatically (depth-first, only if missing) after the loader's own components.
    std::vector<ComponentId> dependencies;
    // Non-zero: the type can be stored inline in a lightweight entity (EntityAdmin::SpawnInline);
    // bytes, from the global per-type table at RVA 0x1828eb0 (+0x18). Such types never enter the
    // EA iteration lists (the flushes check this same value).
    uint32_t inline_size = 0;
    // [?] Global table +0x08: an entity allocator used when this type is the last component of an
    // inline entity (gives kFlagCustomDeallocator). Assumed present when +0x10 (handler) is.
    bool inline_entity_allocator = false;

    bool tracked() const { return inline_size == 0; }
};

// Per-id component types (client: EA+0x38 creators + creation info dependency lists).
class ComponentRegistry {
public:
    void Register(ComponentType type) {
        auto id = type.id;
        types_[id] = std::move(type);
    }
    const ComponentType* Get(ComponentId id) const { return types_[id] ? &*types_[id] : nullptr; }

private:
    std::array<std::optional<ComponentType>, kComponentIdLimit> types_;
};

// Per-type component allocator: a model of the client's page allocator (vtable RVA 0x157f0f0,
// alloc FUN_7ff6507f31f0, free FUN_7ff6507f3350), which decides component addresses and so the
// EA iteration order (lists sorted by address). Verified live: 256 slots per page; fresh slots
// are handed out top-down (descending addresses) before the page's LIFO free list is used.
// [?] New pages are assumed to get higher addresses than older ones, and to become current.
class ComponentPool {
public:
    static constexpr uint16_t kPageSlots = 256;

    // Returns the component's order key (sorts like its address).
    uint64_t Allocate() {
        Page* p = current_;
        if (p) {
            // Look for room starting at the current page (the client checks at most 16 pages).
            for (int tries = 16; p->used >= kPageSlots;) {
                p = p->next;
                if (p == current_ || --tries == 0) {
                    p = nullptr;
                    break;
                }
            }
        }
        if (!p) p = NewPage();
        uint16_t slot;
        if (p->bump < kPageSlots) {
            slot = p->bump++;
        } else {
            slot = p->free.back();
            p->free.pop_back();
        }
        p->used++;
        current_ = p;
        return Key(*p, slot);
    }

    void Free(uint64_t key) {
        uint32_t rank = uint32_t(key >> 16);
        uint16_t slot = uint16_t(kPageSlots - 1 - (key & 0xffff));
        Page* p = nullptr;
        for (auto& pg : pages_)
            if (pg->rank == rank) p = pg.get();
        if (!p) return;
        p->free.push_back(slot);
        p->used--;
        if (p->used == 0) {
            ReleasePage(p);
        } else if (current_ && p->used < current_->used) {
            current_ = p;
        }
    }

private:
    struct Page {
        uint32_t rank = 0;
        uint16_t bump = 0; // next fresh slot (slot 0 = highest address)
        uint16_t used = 0;
        std::vector<uint16_t> free;
        Page* next = nullptr;
    };

    static uint64_t Key(const Page& p, uint16_t slot) {
        return (uint64_t(p.rank) << 16) | uint64_t(kPageSlots - 1 - slot);
    }

    Page* NewPage() {
        auto page = std::make_unique<Page>();
        page->rank = next_rank_++;
        Page* raw = page.get();
        if (pages_.empty()) {
            raw->next = raw;
        } else { // [?] insertion position in the ring
            raw->next = pages_.back()->next;
            pages_.back()->next = raw;
        }
        pages_.push_back(std::move(page));
        return raw;
    }

    void ReleasePage(Page* p) { // [?] which page becomes current afterwards
        if (pages_.size() == 1) {
            pages_.clear();
            current_ = nullptr;
            return;
        }
        Page* prev = p;
        while (prev->next != p) prev = prev->next;
        prev->next = p->next;
        if (current_ == p) current_ = p->next;
        for (auto it = pages_.begin(); it != pages_.end(); ++it)
            if (it->get() == p) {
                pages_.erase(it);
                break;
            }
    }

    std::vector<std::unique_ptr<Page>> pages_;
    Page* current_ = nullptr;
    uint32_t next_rank_ = 0;
};

} // namespace ecs
