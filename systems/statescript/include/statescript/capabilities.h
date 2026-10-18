// What this runtime implements, per client node / state / config-var type: the library's own registry, so that a
// host (the instance server) decides from it which states it can simulate and correct instead of keeping lists of
// types itself (T053/T054). Levels and data: docs/StatescriptNodes.md §1.
#pragma once
#include <cstdint>
#include <span>

namespace ss {

class EvaluatorRegistry;

enum class TypeKind : uint8_t { Entry, Condition, Action, Terminator, State, ConfigVar, Other };

// How faithfully the runtime runs a type.
enum class CapLevel : uint8_t {
    Unmodelled = 0,    // no-op (plugs and lifetime only), counted in SyncManager::unmodelled_*
    Presentation = 1,  // client presentation (effect, sound, UI, debug): a no-op is the right server behaviour
    Designed = 2,      // implemented without a client class to follow (server-only logic) or partially [?]
    Exact = 3,         // the client class's behaviour, read in Ghidra
    Verified = 4,      // exact and checked against client recordings (fixture replay / shadow compare)
};

// State data in one format (local = the owner's rollback packets, remote = other clients' updates).
enum class DataCap : uint8_t {
    None = 0,    // the format is empty for this type (T052 spec)
    Writer = 1,  // written from the runtime's state, per the spec
    Echo = 2,    // not produced: only the bits an import supplied can be written back
};

// A client type as the generated table (tools/gen_type_table.py) describes it.
struct ClientType {
    uint32_t type;
    TypeKind kind;
    uint32_t impl;  // STU type whose client class implements it (exact or nearest registered ancestor); 0 = none
    int8_t prio;    // that class's priority layer, -1 = none
    // State data facts (states only): has-update type, local / remote format non-empty, both formats identical.
    uint8_t has_update, local, remote, same;
    uint32_t resident_uses;             // instances in the 296 resident graphs
    uint32_t parent;                    // STU parent type
};

struct TypeCap {
    uint32_t type = 0;
    TypeKind kind = TypeKind::Other;
    CapLevel level = CapLevel::Unmodelled;
    DataCap local = DataCap::None, remote = DataCap::None;
    bool has_update = false;
    bool known = false;  // the type is in the client's catalogue
    bool host = false;   // server-only (no client class): lifecycle here, effects by the host (host events)
};

// Every client node / state / config-var type (719), in type-hash order.
std::span<const ClientType> ClientTypes();
const ClientType* FindClientType(uint32_t type);

// The capability of `type`. Config vars: Exact when `evals` has an evaluator for the exact type (host-registered
// evaluators count: the host vouches for them), else Unmodelled; without `evals`, the built-in set.
TypeCap Capability(uint32_t type, const EvaluatorRegistry* evals = nullptr);
// A node / state type whose effects are the host's (server-only types and host-system client classes): the runtime
// runs its lifecycle and logs host events (docs/StatescriptNodes.md §3.9).
bool IsHostType(uint32_t type);

}  // namespace ss
