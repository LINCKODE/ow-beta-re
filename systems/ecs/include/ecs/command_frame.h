#pragma once
// Fixed-step command frames (docs/ECS.md §6, §8). EntityAdmin::Tick runs at render rate; the
// 16 ms command frames (CF) are produced by the EA clock and dispatched by CommandFrameSystem to
// the EA's command-frame listeners.
#include "component.h"
#include "types.h"
#include <cstdint>
#include <vector>

namespace ecs {

inline constexpr int64_t kCommandFrameMicros = 16000;   // 62.5 Hz
inline constexpr int64_t kMaxClockStepMicros = 250000;  // at most 250 ms simulated per render frame

// OnCommandFrame flags: first / last CF of this render frame's range (a single CF gets both).
enum CommandFrameFlags : uint32_t { kFirstCommandFrame = 1, kLastCommandFrame = 2 };

// Time dilation flags (clock+0xa8), set by the netcode to pull the client's CF lead in or out.
enum ClockDilationFlags : uint8_t {
    kClockSlowDown = 0x1, // -200 µs per render frame
    kClockSpeedUp = 0x2,  // +200 µs per render frame (ignored if kClockSlowDown is set)
    kClockStrong = 0x8,   // ±1000 µs instead of ±200
};

// The EA's netcode clock (client: EA timing object +0x218, clock at timing+0x50).
struct CommandFrameClock {
    int64_t accumulator_us = 0;     // +0x00: time not yet turned into a CF
    int64_t simulated_us = 0;       // +0x08: µs turned into CFs this render frame
    uint32_t command_frame = 0;     // +0x10: last CF produced
    std::vector<uint32_t> pending;  // +0x20: CFs produced this render frame (consecutive)
    uint8_t dilation_flags = 0;     // +0xa8

    // Render-frame timing kept by GameClockSystem (timing +0x20/+0x28/+0x38).
    float last_dt = 0;
    double total_time = 0;
    uint32_t render_frames = 0;

    // FUN_7ff650ce63a0 (first part): render-frame dt (s) -> µs, with dilation.
    static int64_t DilatedMicros(float dt, uint8_t flags) {
        auto us = int64_t(dt * 1000000.0f);
        if (flags & kClockSlowDown) return us - ((flags & kClockStrong) ? 1000 : 200);
        if (flags & kClockSpeedUp) return us + ((flags & kClockStrong) ? 1000 : 200);
        return us;
    }

    // FUN_7ff65100e950: clamp to [0, 250 ms], accumulate, emit one CF per 16000 µs.
    void Advance(int64_t us) {
        pending.clear();
        simulated_us = 0;
        int64_t step = us < 0 ? 0 : (us <= kMaxClockStepMicros ? us : kMaxClockStepMicros);
        accumulator_us += step;
        while (accumulator_us >= kCommandFrameMicros) {
            simulated_us += kCommandFrameMicros;
            accumulator_us -= kCommandFrameMicros;
            pending.push_back(++command_frame);
        }
    }

    // FUN_7ff650ce64a0: jump to `cf` (the netcode resyncs to the server's frame + RTT frames; see
    // docs/CommandFrames.md §4). This render frame's pending CFs keep their count and are
    // renumbered after the new CF; the accumulator is kept.
    void Resync(uint32_t cf) {
        size_t n = pending.size();
        pending.clear();
        command_frame = cf;
        for (size_t i = 0; i < n; i++) pending.push_back(++command_frame);
    }

    // Set the state directly (tests, replaying a trace from its starting point).
    void Seed(uint32_t cf, int64_t accumulator) {
        command_frame = cf;
        accumulator_us = accumulator;
    }
};

// A command-frame listener (client: objects in EA+0xF0, usually a system's subobject at +0x10).
class CommandFrameListener {
public:
    virtual ~CommandFrameListener() = default;
    // slot 0: once per CF of the render frame's range, in order, flags = kFirst/kLastCommandFrame.
    virtual void OnCommandFrame(uint32_t cf, uint32_t flags) = 0;
    // slot 1 / slot 2: every render frame (also when no CF is due), before / after the range.
    virtual void OnPreCommandFrames() {}
    virtual void OnPostCommandFrames() {}
};

// Singleton component 0x4d: the CF being simulated (read by CF-driven code).
inline constexpr ComponentId kCommandFrameClockComponent = 0x4d;
class CommandFrameClockComponent : public Component {
public:
    uint32_t command_frame = 0; // +0x18
    int64_t time_ms = 0;        // +0x20: command_frame * 16
    int32_t phase = 0;          // +0x28: 1 while running this render frame's CFs, 2 after
};

// Entries of the EA's deferred per-CF queue (EA+0x110, stride 0x18). [?] Producer and payload
// unknown; each CF drops entries whose frame (+0x10) is non-zero and older than the CF.
struct DeferredCommandFrameEntry {
    uint64_t a = 0, b = 0;
    uint32_t command_frame = 0;
};

} // namespace ecs
