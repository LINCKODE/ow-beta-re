// The match / objective node families (T055, docs/StatescriptNodes.md §5): gamemode brains, capture points, payload,
// spawn rooms, the tutorial. Their states, actions, conditions and config vars, and their capability rows; the world
// they read and change is the host's (include/statescript/match_host.h).
#pragma once
#include <cstdint>
#include <memory>
#include <span>

#include "statescript/capabilities.h"

namespace ss {
class Instance;
class State;
class EvaluatorRegistry;
struct Node;

// A capability row (the registry's record of one implemented type; capabilities.cpp keeps T054's, this file T055's).
struct CapRow {
    uint32_t type;  // the client class's STU type (ClientType::impl), or the type itself for server-only types
    CapLevel level;
    bool local_writer, remote_writer;  // state data writers (states with data only)
    bool subtree = false;  // also every descendant type
    bool host = false;     // server-driven: host events / ss_sm_end_state
};
std::span<const CapRow> MatchCapRows();

// The state object for `type` if it belongs to these families, else nullptr (CreateState's first question).
std::unique_ptr<State> CreateMatchState(Instance& inst, int state_index, uint32_t type);
// The host-system states (movement, collision, position history, time scale; states_host.cpp, T060), else nullptr.
std::unique_ptr<State> CreateHostSystemState(Instance& inst, int state_index, uint32_t type);
// DoAction of an action of these families; false = not one of them.
bool MatchAction(State* src, Instance& inst, const Node& n);
// A condition of these families: true = handled, `result` set.
bool MatchCondition(State* src, Instance& inst, const Node& n, bool& result);
// Config vars that read the world through the match host (registered by EvaluatorRegistry::Builtin).
void RegisterMatchEvaluators(EvaluatorRegistry& r);
// Whether `entity` passes the CVFilter config var `filter_cv` of the instance's graph (SyncMgr +0x168 = 0x1073d80;
// -1 = no filter: passes).
bool MatchPassesFilter(Instance& inst, uint32_t entity, int filter_cv);
// CreateEntity (action 0e3689f3 / state e7c6aaf6): asks the host, writes m_out_Entity; returns the entity.
uint32_t MatchCreateEntity(Instance& inst, const Node& n);
}  // namespace ss
