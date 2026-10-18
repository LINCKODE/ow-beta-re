#pragma once
#include <cstdint>
#include <nlohmann/json.hpp>

// ECS event tracer (see docs/ECS.md). Records entity lifecycle calls and per-system entity
// callbacks with the tick/phase they happened in, as golden fixtures for systems/ecs.
//
// Engine functions are hooked with MinHook (installed on first start, then kept; recording is
// gated by a flag). System callbacks (vtable slots 9-13) are traced by giving each system
// instance a private copy of its vtable, so the shared .rdata vtables stay untouched.
namespace ecs_trace {

// action: start | stop | clear | status | fetch (since = seq, limit)
nlohmann::json control(const nlohmann::json& args);

// The system's original (game) vtable if the tracer gave it a private copy, else its current one.
uintptr_t* original_vtable(void* system);

} // namespace ecs_trace
