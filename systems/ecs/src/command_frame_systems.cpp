#include "ecs/command_frame_systems.h"

namespace ecs {

void GameClockSystem::OnTick(float dt) {
    EntityAdmin& ea = *admin();
    CommandFrameClock& clock = ea.clock();
    clock.last_dt = dt;
    clock.total_time += dt;
    clock.render_frames++;
    // FUN_7ff650ce63a0: dilation, then (not modelled: smoothing of the lead values at clock+0x80/0x84)
    ea.TraceClockDilate(dt, clock.dilation_flags);
    int64_t us = CommandFrameClock::DilatedMicros(dt, clock.dilation_flags);
    ea.TraceClockAdvance(us);
    clock.Advance(us);
}

CommandFrameClockComponent* CommandFrameSystem::Singleton() const {
    auto list = admin()->ComponentsOfType(kCommandFrameClockComponent);
    return list.empty() ? nullptr : dynamic_cast<CommandFrameClockComponent*>(list.front());
}

void CommandFrameSystem::OnTick(float) {
    EntityAdmin& ea = *admin();
    ea.RunCommandFramesPre();
    CommandFrameClockComponent* state = Singleton();
    if (state) state->phase = 1;
    const auto& pending = ea.clock().pending;
    if (!pending.empty()) RunCommandFrames(pending.front(), pending.back());
    if (state) state->phase = 2;
    ea.RunCommandFramesPost();
}

int CommandFrameSystem::RunCommandFrames(uint32_t first, uint32_t last) {
    EntityAdmin& ea = *admin();
    ea.TraceCommandFrameRange(first, last);
    CommandFrameClockComponent* state = Singleton();
    ea.OnCommandFramesBegin();
    int count = 0;
    for (uint32_t cf = first; cf <= last; cf++, count++) {
        if (state) {
            state->command_frame = cf;
            state->time_ms = int64_t(cf) * kCommandFrameMicros / 1000;
        }
        uint32_t flags = (cf == first ? uint32_t(kFirstCommandFrame) : 0u) | (cf == last ? uint32_t(kLastCommandFrame) : 0u);
        ea.RunCommandFrame(cf, flags);
    }
    ea.OnCommandFramesEnd();
    return count;
}

} // namespace ecs
