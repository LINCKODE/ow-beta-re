#pragma once
#include "types.h"
#include <cstdint>
#include <vector>

namespace ecs {

// Same event kinds and phases as the Prometheus `ecs_trace` tool, so a run of this library can
// be compared event by event with a client trace (systems/ecs/fixtures).
enum class TraceKind : int32_t {
    SpawnBegin, SpawnEnd, AddComponent,
    OnAdded, OnCreated, OnDeleting, OnRemoved,
    DeleteRequest, DeleteRequestEnd, Destroy,
    FlushCreated, FlushDestroy, Tick,
    SpawnInlineBegin, SpawnInlineEnd,
    ClockDilate, ClockAdvance, CfRange, CfPre, CfListener, CfPost, ClockResync,
};

enum class TracePhase : int32_t { None, FlushDestroy, FlushCreated, SystemTick, Tick, CfListener, CfPre, CfPost };

const char* TraceKindName(TraceKind);
const char* TracePhaseName(TracePhase);

struct TraceEvent {
    uint64_t seq = 0;
    uint64_t tick = 0;       // EntityAdmin tick counter at the time of the event
    TracePhase phase = TracePhase::None;
    int32_t phase_sys = -1;  // system index (SystemTick) or listener index (Cf*)
    uint32_t phase_cf = 0;   // command frame when phase == CfListener
    TraceKind kind = TraceKind::Tick;
    int32_t sys = -1;        // system index for On* callbacks
    EntityId ent = 0;
    uint64_t a = 0, b = 0;   // kind-specific, as in ecs_trace (e.g. requested id, flags)
    std::vector<ComponentId> comps; // SpawnEnd / Destroy
};

class TraceSink {
public:
    virtual ~TraceSink() = default;
    virtual void Record(const TraceEvent&) = 0;
};

class VectorTraceSink : public TraceSink {
public:
    std::vector<TraceEvent> events;
    void Record(const TraceEvent& e) override { events.push_back(e); }
};

} // namespace ecs
