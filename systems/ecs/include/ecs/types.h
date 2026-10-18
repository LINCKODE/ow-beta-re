#pragma once
// Basic ECS types. Behaviour reference: docs/ECS.md (client build 24919).
#include <cstdint>

namespace ecs {

using ComponentId = uint8_t;
using EntityId = uint32_t;

// Entity component mask capacity (3 x u64 at Entity+0x98). The client registers 0x8f ids.
inline constexpr int kComponentIdLimit = 192;

// Subscription index meaning "every entity" (global byte at RVA 0x189229f, 0 in all EAs).
inline constexpr ComponentId kSubscribeAll = 0;

// Spawn with this id (or any id whose low 29 bits are all set) = allocate one:
// id = loader.IdHighBits() | ++counter.
inline constexpr EntityId kDefaultEntityId = 0;
inline constexpr EntityId kEntityIdLowMask = 0x1fffffff;

// Entity+0xB4 lifecycle flags.
enum EntityFlags : uint32_t {
    kFlagCreated = 0x1,             // components created (finalize step 5)
    kFlagPendingCreated = 0x2,      // in the created queue until FlushCreated
    kFlagDeleteRequested = 0x4,
    kFlagDeleteImmediate = 0x8,     // delete requested with immediate = true
    kFlagPendingDestroy = 0x10,     // in the destroy queue until FlushDestroy
    kFlag20 = 0x20,                 // unknown; like 0x10 it makes further delete requests no-ops
    kFlagHasTaggedChild = 0x40,     // a child was spawned from a loader with a tag
    kFlagExternalStorage = 0x80,    // not from the EA pool; Destroy skips OnEntityRemoved
    kFlagCustomDeallocator = 0x100,
};

struct Uuid {
    uint64_t low = 0, high = 0;
    bool operator==(const Uuid&) const = default;
};

} // namespace ecs
