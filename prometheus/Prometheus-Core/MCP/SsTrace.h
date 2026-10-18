#pragma once
#include <cstdint>
#include <nlohmann/json.hpp>

// Statescript execution tracer (see docs/StatescriptRuntime.md §4). Records, for the instances of
// selected graphs / entities: the command-frame steps of their sync manager, every notification the
// instance executes, state begin / finish / abort, every link traversal (plug -> node), game-message
// deliveries, instance starts, and the variable values that changed during each command frame.
//
// Engine functions are hooked with MinHook (installed on first start, then kept; recording is gated
// by a flag). Hooks run on the game thread inside the Statescript update.
namespace ss_trace {

// action: start (graphs=[guid...], entities=[id...], all=bool, cap) | stop | clear | status |
//         fetch (since, limit) | save (path, since)
nlohmann::json control(const nlohmann::json& args);

} // namespace ss_trace
