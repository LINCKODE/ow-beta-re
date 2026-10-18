// Config-var evaluation: the pluggable evaluator interface. Each STU config-var type has an evaluator
// (the client binds a per-type singleton at LinkGraph, docs/StatescriptRuntime.md §1.2.1 / §1.8;
// interface: docs/StatescriptConfigVars.md §1, catalogue §4–5). Built-in evaluators cover the literal types
// (generated from T038's catalogue), variable paths, the owner and both expression interpreters; types that
// read game state (entities, teams, maps, UI…) are supplied by the host through EvaluatorRegistry::Register.
#pragma once
#include <cstdint>
#include <functional>
#include <memory>
#include <unordered_map>
#include <vector>

#include "statescript/value.h"

namespace ss {

class Graph;
class Instance;

struct EvalContext {
    Instance* instance = nullptr;
};

class Evaluator {
public:
    virtual ~Evaluator() = default;
    // GetValue (evaluator vtable +0xD0): false = no value (the caller then uses its default). `out` is
    // INT 0 on entry, as the client's callers initialise it.
    virtual bool Get(const Graph& g, int cv, EvalContext& ctx, Primitive& out) const = 0;
    // +0xD8 DependsOnExternal: the value can change without a variable-change notification on this instance
    // (BooleanSwitch / Switch states then re-evaluate every pass). Base: false.
    virtual bool DependsOnExternal(const Graph&, int, EvalContext&) const { return false; }
    // STUConfigVarIterable types: the evaluator's iterator (+0xE0 / +0xE8), appending {key, value} entries; false =
    // not modelled (Iterate then yields nothing). Base: false.
    virtual bool IterateValues(const Graph&, int, EvalContext&, std::vector<std::pair<Primitive, Primitive>>&) const {
        return false;
    }
};

// Evaluator from a function (host-side game-state config vars, test stubs).
class FunctionEvaluator : public Evaluator {
public:
    using Fn = std::function<bool(const Graph&, int, EvalContext&, Primitive&)>;
    explicit FunctionEvaluator(Fn fn, bool external = false) : fn_(std::move(fn)), external_(external) {}
    bool Get(const Graph& g, int cv, EvalContext& ctx, Primitive& out) const override { return fn_(g, cv, ctx, out); }
    bool DependsOnExternal(const Graph&, int, EvalContext&) const override { return external_; }

private:
    Fn fn_;
    bool external_;
};

class EvaluatorRegistry {
public:
    // The built-in evaluators (config_vars.cpp).
    static EvaluatorRegistry Builtin();

    void Register(uint32_t type_hash, std::shared_ptr<const Evaluator> e) { map_[type_hash] = std::move(e); }
    // Convenience: a constant value for every config var of this type.
    void RegisterConstant(uint32_t type_hash, const Primitive& value);
    // Exact type only (the client falls back to the STUConfigVar base evaluator, which yields nothing).
    const Evaluator* Find(uint32_t type_hash) const {
        auto it = map_.find(type_hash);
        return it == map_.end() ? nullptr : it->second.get();
    }

private:
    std::unordered_map<uint32_t, std::shared_ptr<const Evaluator>> map_;
};

// Evaluates config var instance `cv` of the instance's graph (null / unmodelled type -> false; unmodelled
// types are counted in the sync manager's report).
bool Evaluate(EvalContext& ctx, int cv, Primitive& out);
// Typed getters (docs/StatescriptConfigVars.md §1.3): null cv, no value or a type they don't take -> `dflt`.
bool EvaluateBool(EvalContext& ctx, int cv, bool dflt = false);        // 0xcffa40: truthiness
int32_t EvaluateInt(EvalContext& ctx, int cv, int32_t dflt = 0);       // 0xcffac0: BYTE 0/1, INT, FLT rounded
float EvaluateFloat(EvalContext& ctx, int cv, float dflt = 0.0f);      // 0xcffbc0: BYTE 0/1, INT, FLT
uint64_t EvaluateGuid(EvalContext& ctx, int cv, uint64_t dflt = 0);    // 0xcffca0: INT64 only
uint32_t EvaluateEntity(EvalContext& ctx, int cv);                     // 0xcffd30: ENTITY, else kInvalidEntity
bool DependsOnExternal(EvalContext& ctx, int cv);

// Iteration over a config var (SS_CVIterator 0xf5a740 / 0xf5a7f0) [G] (docs/StatescriptNodes.md §3.3): an
// STUConfigVarArray yields its elements in order (nested arrays flattened, up to 4 levels); an element (or the
// config var itself) that is a Dynamic path to a Dictionary var yields that var's {key, value} entries; an
// STUConfigVarIterable subclass would use its evaluator's iterator (not modelled: nothing, counted); anything
// else yields one entry {BYTE 0, its value} (value BYTE 0 when it has none: 0xf4d330).
using DictEntry = std::pair<Primitive, Primitive>;
std::vector<DictEntry> Iterate(EvalContext& ctx, int cv);

// The invalid entity id: the client keeps it in a global (0x183a0c8, set at runtime); the typed expression
// interpreter also treats any id whose low 29 bits are all set as "no entity" [G]. We use 0xFFFFFFFF [?].
inline constexpr uint32_t kInvalidEntity = 0xFFFFFFFFu;
inline bool IsInvalidEntity(uint32_t id) { return id == kInvalidEntity || (id & 0x1FFFFFFFu) == 0x1FFFFFFFu; }

}  // namespace ss
