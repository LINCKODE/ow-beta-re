#pragma once
// The two systems that turn render frames into command frames (docs/ECS.md §8).
#include "entity_admin.h"

namespace ecs {

// Client game EA #1 / session #0 (vtable 0x15c4be8, OnTick FUN_7ff650c7d4b0): advances the EA
// clock by the render frame's dt (with time dilation), producing this frame's pending CFs.
class GameClockSystem : public System {
public:
    void OnTick(float dt) override;
};

// Client game EA #16 / session #10 (vtable 0x15c1b48, OnTick FUN_7ff650c167d0): runs the clock's
// pending CFs through the EA's command-frame listeners.
class CommandFrameSystem : public System {
public:
    void OnTick(float dt) override;
    // FUN_7ff650feea30: CFs first..last (inclusive); returns how many ran.
    int RunCommandFrames(uint32_t first, uint32_t last);

private:
    CommandFrameClockComponent* Singleton() const;
};

} // namespace ecs
