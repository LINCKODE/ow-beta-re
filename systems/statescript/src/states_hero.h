// Hero state types (T041), created by CreateState before the generic fallback.
#pragma once
#include <cstdint>
#include <memory>

namespace ss {
class Instance;
class State;
// The state object for `type` if it is one of the hero state types, else nullptr.
std::unique_ptr<State> CreateHeroState(Instance& inst, int state_index, uint32_t type);
class EvaluatorRegistry;
// Config vars that read the runtime's own state (stack registry), registered by EvaluatorRegistry::Builtin.
void RegisterHeroEvaluators(EvaluatorRegistry& r);
}  // namespace ss
