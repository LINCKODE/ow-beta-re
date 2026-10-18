// The runtime's capability registry (include/statescript/capabilities.h): the client types from the generated
// table, and what this library implements for each. Every node / state type the runtime implements is listed in
// kImplemented below with its level and data writers; keep it in step with nodes.cpp / states*.cpp
// (docs/StatescriptNodes.md is the per-type record).
#include "statescript/capabilities.h"

#include <algorithm>

#include "statescript/config_var.h"
#include "match.h"

namespace ss {

namespace {

#include "type_table.h"

using Impl = CapRow;  // (src/match.h) the match / objective families' rows are in capabilities_match.cpp (T055)

using L = CapLevel;
constexpr Impl kImplemented[] = {
    // entries, conditions, actions (nodes.cpp)
    {0x2f937ae3, L::Verified, false, false},  // STUStatescriptEntry (door, music fixtures)
    {0xbe49a4c2, L::Verified, false, false},  // STUStatescriptEntryGameMessage (music)
    {0x46c48944, L::Verified, false, false},  // STUStatescriptEntryPlayScript (playscript)
    {0xb1032daf, L::Verified, false, false},  // Condition (bool m_condition)
    {0x4698e4b6, L::Verified, false, false},  // STUStatescriptActionSetVar (scalar form; m_index / m_key forms: warning)
    {0xf9ef59a6, L::Verified, false, false},  // STUStatescriptActionPlayScript (one target)
    {0x85d2552d, L::Verified, false, false},  // STUStatescriptActionStopScript
    {0x5179b279, L::Verified, false, false},  // STUStatescriptActionArrayClear
    {0xb6853829, L::Verified, false, false},  // STUStatescriptActionStopAbility (Genji dash)
    {0x8b6a3312, L::Exact, false, false},     // STUStatescriptActionSendGameMessage (other targets: [?])
    {0x4027e912, L::Exact, false, false},     // the networked SendGameMessage (server runs it; clients when told)
    {0x0cdc2e1a, L::Exact, false, false},     // STUStatescriptActionArrayAdd
    {0xa88eb9de, L::Exact, false, false},     // STUStatescriptActionArrayRemove
    {0x5720b660, L::Exact, false, false},     // STUStatescriptCondition_ArrayContains
    {0x17c9f80b, L::Verified, false, false},  // STUStatescriptActionCycleIndex (Genji next_weapon, T054)
    {0x07722a23, L::Exact, false, false},     // STUStatescriptCondition_Timeout
    {0x4768e4a3, L::Exact, false, false},     // STUStatescriptCondition_LogicalButton
    {0x321be080, L::Exact, false, false},     // STUStatescriptActionSwitch
    {0xf5a51858, L::Exact, false, false},     // STUStatescriptActionRepeat
    {0x138ccab0, L::Exact, false, false},     // STUStatescriptAndGate
    {0x757a8a94, L::Verified, false, false},  // STUStatescriptActionSingleRaycast: the cast is the host's (SyncManager::raycast; unset = miss), Genji dash
    {0xeb923a6a, L::Presentation, false, false},  // STUStatescriptActionEffect
    {0x66b5f581, L::Presentation, false, false},  // STUStatescriptActionGlobalSound
    {0xe4d667e6, L::Presentation, false, false},  // STUStatescriptActionDebugText
    {0x047f99e4, L::Exact, false, false},     // STUStatescriptActionNoOp (base action: out plug only)
    // states (states.cpp, states_hero.cpp)
    {0xa7213568, L::Verified, false, false},  // STUStatescriptState (plugs, lifetime, exits: every fixture)
    {0x3db862c4, L::Verified, true, false},   // SubScript (client trace raw_subscript_3ca.jsonl)
    {0xe67578c0, L::Exact, false, false},     // Watch
    {0x27984fc9, L::Exact, false, false},     // TimeInArray (state-defined m_out_Time)
    {0xffbfa88d, L::Presentation, true, false},  // Effect (u2 serial)
    {0x8ba08edd, L::Presentation, false, false, true},  // UXBase and every UX state (handle var) [?: subclasses' UI hooks]
    {0x6e81f6d4, L::Presentation, false, false},  // CosmeticEntity (handle var)
    // host systems (movement, collision, entities, AI): client classes whose effects are the server's systems;
    // lifecycle + host events here (docs §3.9) [?]
    {0x02e4a0ab, L::Designed, false, false, false, true},  // MovementMod state
    {0x916290ad, L::Designed, false, false, false, true},  // MovementMod action
    {0x8de7e50f, L::Designed, false, false, false, true},  // TrackMovement (bool data from movement: echo)
    {0xf6e1e486, L::Designed, false, false, false, true},  // LedgeClimb
    {0xf45a1956, L::Designed, false, false, false, true},  // WallMove
    {0xc1035274, L::Designed, false, false, false, true},  // MoveTo
    {0x72509d3b, L::Designed, false, false, false, true},  // FindGround
    {0x22fe9543, L::Designed, false, false, false, true},  // RayCast
    {0x323aabcf, L::Designed, false, false, false, true},  // TrackRay
    {0x971645fe, L::Designed, false, false, false, true},  // Attach
    {0xb5741c58, L::Designed, false, false, false, true},  // SetCollisionPriority
    {0xd285a1bb, L::Designed, false, false, false, true},  // FindEntities
    {0x160c4f1d, L::Designed, false, false, false, true},  // ValidatePlacement
    {0xc2eeee16, L::Designed, false, false, false, true},  // Steer
    {0x06fcca5d, L::Designed, false, false, false, true},  // SuppressMovementPrediction
    {0xa8880f27, L::Designed, false, false, false, true},  // Blink action
    {0x225ae956, L::Designed, false, false, false, true},  // threat area (AI)
    {0x67471c66, L::Designed, false, false, false, true},  // DeflectProjectiles (projectiles: the host's)
    {0x614c3a0a, L::Designed, false, false, false, true},  // a filtered cast (collision)
    {0xf16bff1b, L::Designed, false, false, false, true},  // AutoInput (bots / tutorial)
    {0x1ffbc4df, L::Designed, false, false, false, true},  // AutoFacing
    {0x5efae015, L::Presentation, false, false},  // plays one of two assets by the local player's team
    {0xc370abc0, L::Presentation, false, false},  // PauseAnim action
    {0x44e7f474, L::Designed, false, false},  // GamePadVibration (timing exact; the asset length is the host's)
    // client-side effects the server triggers through the actions section (the client runs them only then):
    // the server's part is the action record (ss_sm_actions)
    {0xb17638d2, L::Presentation, false, false},  // queue items onto a state of m_queueEntity (UI / announcer)
    {0x5bea9b4b, L::Presentation, false, false},  // message to a global entity admin (UI)
    // classes that override no hook of the base state: the base behaviour is theirs
    {0x7aff7efe, L::Exact, false, false},
    {0x241069ef, L::Exact, false, false},
    // presentation, confirmed from the class hooks (no lifetime, plug, timer or var effects on the graph) [G]
    {0x85c28a9a, L::Presentation, false, false},  // DataFlowMapping (registers a data-flow answer, 0xd858c0)
    {0xa97b875f, L::Presentation, false, false},  // PopulateTips
    {0x9f74c07c, L::Presentation, false, false},  // ClientonlyController (no hooks)
    {0x6bfa64af, L::Presentation, false, false},  // UXReticleOverride
    {0xd7ca9c01, L::Presentation, false, false},  // TargetEffect
    {0x28d0a23b, L::Presentation, false, false},  // Camera3P
    {0x7b3ed271, L::Presentation, false, false},  // CameraLookAt (no hooks)
    {0xf533c587, L::Presentation, false, false},  // GlobalSound state
    {0x4fbcd9de, L::Presentation, false, false},  // UXToast action
    {0x47db120f, L::Presentation, false, false},  // BroadcastChat action
    // type-list members: list + data exact; the effects are the host's (aim, camera, visibility) [?]
    {0x01d6952a, L::Designed, true, true},    // LimitAim (its state-defined var value not modelled)
    {0x7331cb24, L::Designed, true, true},    // Invisible
    {0xa5f89e3f, L::Presentation, true, true},  // CameraTransform
    {0xb1001dcf, L::Designed, true, true},    // PhaseOut
    {0xbf733a32, L::Presentation, true, true},  // TimeScaleAnimation
    {0xed81c7fb, L::Designed, true, true},    // AimSpeed
    {0x10fc71ba, L::Presentation, true, true},  // CameraChase
    {0x2785fbdd, L::Designed, true, true},
    {0x85d43f96, L::Designed, true, true},    // WeaponAim
    {0xaa488c28, L::Presentation, true, true},  // ImpactEffectOverride
    {0xcd396790, L::Designed, false, false},  // unnamed timed state (logic exact; the global clock is the host's)      // SubScript (child instances; no client trace of a begin yet)
    {0x65f158da, L::Verified, false, false},  // Wait
    {0xe811ecbb, L::Verified, true, false},   // BooleanSwitch
    {0x26367267, L::Verified, true, false},   // Switch
    {0x7fb4bf64, L::Verified, true, false},   // Anim (duration from the node; asset lengths not modelled)
    {0x970ec198, L::Verified, true, false},   // ChaseVar
    {0x860ee5fc, L::Verified, true, false},   // WeaponVolley
    {0xf7d6498c, L::Verified, true, false},   // Stack
    {0xf53c5da8, L::Verified, true, false},   // Ability
    {0x429991a7, L::Verified, false, false},  // BlinkEffect (presentation + trigger plug)
    {0xff77458c, L::Verified, false, false},  // LogicalButton (Genji spray press: 0x2cf state 2, T054)
    // config vars (evaluators; the literal types come from the registry)
    {0xc81f8768, L::Exact, false, false},  // may-take-slot
    // T060 (docs §3.14): the last server-path types
    {0x3f67b31b, L::Exact, true, false},         // networked pulse: plug per entry, u8 fired count (states.cpp)
    {0x12db64da, L::Presentation, false, false},  // voice-set override on the target's sound component 0x06
    {0xcf08df77, L::Presentation, false, false},  // messages 0x…14b / 0x…14c to the local player's camera graph 0x250
    {0xba8e336e, L::Presentation, false, false},  // resets the owner's view override (component 0x1F +0x170)
    {0x82505a1b, L::Designed, false, false, false, true},  // character mover (0x15) +0x7D0 := 2daaa3ef while active
    {0xc0a88bb0, L::Presentation, false, false},  // BeamEffect (reads its CVs into the effect; no graph effect)
    {0x1787a82d, L::Presentation, false, false},  // SprayEffect: camera raycast + decal; data echo only: the reader
                                                  // (0xd838c0) first removes the client's decal, so a server that
                                                  // places no spray must not send it
    {0x1a81cfb7, L::Presentation, false, false},  // AnimatedCamera (local camera animation)
    {0xf8ec9531, L::Presentation, false, false},  // RestartAnim action (needs a live animation: none on a server)
    // host systems with the graph side run here (states_host.cpp; docs §3.16): engine part via MatchHost
    {0x029bf346, L::Exact, false, false, false, true},    // HistoryBuilder: handle write; sampling = host
    {0x7211cf70, L::Designed, true, false, false, true},  // HistoryRewinder: finish at its duration [?]; u16 data 0
    {0xfea1e74b, L::Exact, false, false, false, true},    // Accelerate: mover controller kind 6
    {0xc8c71bbc, L::Designed, false, false, false, true}, // climb controller kind 12: done by the host, else m_climbTime [?]
    {0xf536a60f, L::Exact, false, false, false, true},    // curve controller kind 13
    {0x76391dd3, L::Exact, false, false, false, true},    // TrackSlamming: writes; forward progress = host
    {0x8d50485a, L::Exact, false, false, false, true},    // Shockwave: timing, radius, targets; hits = host
    {0x12aa4f0f, L::Exact, false, false, false, true},    // ValidateWidth: claims + state values; casts = host
    {0x26cb90c0, L::Designed, true, false, false, true},  // Extender: timing, claims, end writes; aim / casts = host, no aim assist
    {0x7bf31629, L::Exact, true, false, false, true},     // ScaleTime: start / stop CF + data; the scale = host
};

const Impl* FindImpl(uint32_t type) {
    for (auto& i : MatchCapRows())  // T055's rows first: they take over some of the rows below
        if (i.type == type) return &i;
    for (auto& i : kImplemented)
        if (i.type == type) return &i;
    return nullptr;
}

}  // namespace

std::span<const ClientType> ClientTypes() { return kClientTypes; }

const ClientType* FindClientType(uint32_t type) {
    auto it = std::lower_bound(std::begin(kClientTypes), std::end(kClientTypes), type,
                               [](const ClientType& c, uint32_t t) { return c.type < t; });
    return it != std::end(kClientTypes) && it->type == type ? &*it : nullptr;
}

bool IsHostType(uint32_t type) {
    const ClientType* ct = FindClientType(type);
    if (!ct || ct->kind == TypeKind::ConfigVar || ct->kind == TypeKind::Other) return false;
    if (ct->impl == 0) return !FindImpl(type) || FindImpl(type)->host;
    const Impl* i = FindImpl(ct->impl);
    return i && i->host;
}

TypeCap Capability(uint32_t type, const EvaluatorRegistry* evals) {
    TypeCap cap;
    cap.type = type;
    const ClientType* ct = FindClientType(type);
    if (!ct) return cap;
    cap.known = true;
    cap.kind = ct->kind;
    cap.has_update = ct->has_update;
    if (ct->kind == TypeKind::ConfigVar) {
        static const EvaluatorRegistry builtin = EvaluatorRegistry::Builtin();
        const EvaluatorRegistry& r = evals ? *evals : builtin;
        cap.level = r.Find(type) ? CapLevel::Exact : CapLevel::Unmodelled;
        // A row states the level of an implemented config var (designed / presentation evaluators are registered too).
        if (const Impl* i = FindImpl(type); i && cap.level != CapLevel::Unmodelled) cap.level = i->level;
        else if (i) cap.level = std::max(cap.level, i->level);
        return cap;
    }
    if (ct->impl == 0 && ct->kind != TypeKind::ConfigVar && ct->kind != TypeKind::Other) {
        cap.host = true;
        if (!FindImpl(type)) {  // lifecycle + host events (docs §3.9) [?]
            cap.level = CapLevel::Designed;
            cap.local = ct->local ? DataCap::Echo : DataCap::None;
            cap.remote = ct->remote ? DataCap::Echo : DataCap::None;
            return cap;
        }
    }
    const Impl* impl = FindImpl(ct->impl ? ct->impl : type);
    if (impl && impl->host) cap.host = true;
    for (const ClientType* p = FindClientType(ct->parent); !impl && p; p = FindClientType(p->parent))
        if (const Impl* i = FindImpl(p->type); i && i->subtree) impl = i;
    if (impl) cap.level = impl->level;
    auto data = [&](bool has, bool writer) {
        return !has ? DataCap::None : impl && writer ? DataCap::Writer : DataCap::Echo;
    };
    cap.local = data(ct->local, impl && impl->local_writer);
    // Identical formats: the local writer serves both.
    cap.remote = data(ct->remote, impl && (impl->remote_writer || (ct->same && impl->local_writer)));
    return cap;
}

}  // namespace ss
