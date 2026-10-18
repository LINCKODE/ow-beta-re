// Capability rows of the match / objective families (T055; the registry is capabilities.cpp, the per-type record
// docs/StatescriptNodes.md §5). Searched before T054's rows: a type listed here is this file's.
#include "match.h"

namespace ss {

namespace {
using L = CapLevel;
constexpr CapRow kRows[] = {
    // states (states_match.cpp)
    {0x6e3b8ce5, L::Designed, false, false},        // VoiceStimulus: finishes after the line (host / e341b9c4) [?]
    {0x0119c536, L::Designed, false, false},        // CapturePointEvents: progress var exact, capture model [?]
    {0x498b421a, L::Designed, true, false},         // TrackTargets: radius + filter over the host's players; data exact
    {0xc5dcbfa1, L::Exact, false, false},           // FilterArray (m_filter: masks + condition + tags, docs §5.4)
    {0x97315c42, L::Designed, false, false},        // player events (host: MatchPlayerEvent)
    {0xe7c6aaf6, L::Designed, false, false, false, true},  // CreateEntity state (host creates)
    {0x1ab3b4d7, L::Designed, false, false},        // ApplyTargetTags
    {0x4ab09851, L::Designed, false, false},        // the graph's replicated-var list: no effect [?]
    {0x616f509f, L::Designed, false, false},        // payload path mover (host path)
    {0x6c2502e0, L::Designed, false, false},        // UpdatePayload (report to the host)
    {0xa4f3be65, L::Exact, false, false},           // PublishGlobals: global-var registry (component 0x51) (T060)
    // actions (nodes_match.cpp)
    {0xece45723, L::Designed, false, false},        // BroadcastGameMessage (host delivers)
    {0xde4f0f88, L::Designed, false, false},        // TeleportTo
    {0x0e3689f3, L::Designed, false, false, false, true},  // CreateEntity action
    {0xa257245a, L::Designed, false, false},        // SetTeamIndex
    {0xbd6a3dff, L::Designed, false, false},        // AddTargetTags
    {0x78184003, L::Designed, false, false},        // RemoveTargetTags
    {0x4f14c91b, L::Designed, false, false},        // RespawnPlayer
    {0xf90c6c08, L::Designed, false, false},        // GameFinished
    {0xf1201eda, L::Designed, false, false},        // EndMatch
    {0x5c46d801, L::Designed, false, false},        // DestroyEntity
    {0x3be5172e, L::Designed, false, false},        // capture area enable
    {0x350e207d, L::Designed, false, false},        // capture area owning team
    {0x670cf926, L::Designed, false, false},        // add match time
    {0x12213f7e, L::Designed, false, false},        // spawn point enable
    {0x4e83bc01, L::Designed, false, false},        // Criteria: no graph-visible effect
    {0xeaee2743, L::Designed, false, false, false, true},  // InstanceEvent: host event only
    {0x9f8f5a55, L::Designed, false, false, false, true},  // CreateReel: out var := host clip id (T060)
    {0xcc7b6828, L::Designed, false, false, false, true},  // PublishReel (type, id): host event (T060)
    {0x6dbdfe7c, L::Designed, false, false, false, true},  // highlight length: host event (T060)
    {0xd5c8d941, L::Designed, false, false, false, true},  // round-end slow motion over: host event (T060)
    // config vars (nodes_match.cpp; registered evaluators make them Exact for pure / client-class ones)
    {0x8f3d1c9d, L::Designed, false, false},        // "all players" target set (host) [?]
    {0x4b5d2f3f, L::Designed, false, false},        // players on a team [?]
    {0xae45e451, L::Designed, false, false},        // IsPointInArea (host shape)
    {0x8b31201d, L::Exact, false, false},           // GetPrimaryPossessedEntity (host: component 0x20)
    {0xcbd4f4f2, L::Exact, false, false},           // GetThrottle (host: the input record)
    {0x7ead98e5, L::Exact, false, false},           // CVFilter (as a value: the owner's filter test; masks, condition, tags)
    // T060 (docs §3.15): exact through the host / asset data, designed, or no value on a server (presentation)
    {0x9ec82686, L::Exact, false, false},           // GetEntityVisualPosition (host Position)
    {0xe4a99cc5, L::Exact, false, false},           // IsEntityInCaptureArea (host InCaptureArea)
    {0x9521c2b9, L::Exact, false, false},           // AngleBetweenPoints2D (pure)
    {0x9161fb46, L::Exact, false, false},           // IsScopeGameEntityAdmin: true on a server
    {0xfafb9471, L::Exact, false, false},           // HeroHasIdentifier (hero STU list 99efbfd6)
    {0x3f836f1a, L::Designed, false, false},        // teamless spectator: false (no spectators on the server)
    {0x31993d91, L::Designed, false, false},        // the reel time (host ReelTime) [?]
    {0xadfc9542, L::Presentation, false, false},    // in viewport
    {0x56a2012b, L::Presentation, false, false},    // on screen, not occluded
    {0xadc6d1a0, L::Presentation, false, false},    // lobby UI var $734
    {0x5e7fbc32, L::Presentation, false, false},    // recently hit by the local player
    {0xc29cf678, L::Presentation, false, false},    // highlight replay (post-match flag off)
    {0xe2c530ff, L::Presentation, false, false},    // highlight replay (post-match flag on)
    {0x9f762d9f, L::Presentation, false, false},    // is the local player
    {0xb9af22db, L::Presentation, false, false},    // in the local player's party
    {0x51870208, L::Presentation, false, false},    // off-screen arrow angle (camera)
    {0x827387ee, L::Presentation, false, false},    // GetEntityVisualHardPointPosition (model pose)
    {0xd07ca4ab, L::Presentation, false, false},    // GetStringLength (a localized tip)
    // UI text (catalogue [G]: localized formatting, colour tags, times, names, key-binding labels): read only by UI
    // view models, toasts, chat lines and the activity log, which are client presentation
    {0xee046439, L::Presentation, false, false},    // FormatString
    {0x56ad73f3, L::Presentation, false, false},    // GetRichTextString
    {0xe0d68734, L::Presentation, false, false},    // StringTime
    {0x7b51e72b, L::Presentation, false, false},    // GetEntityName
    {0x08f6ed3d, L::Presentation, false, false},    // key-binding label of an input action
    {0xb4dd308c, L::Presentation, false, false},    // string list (m_strings)
};
}  // namespace

std::span<const CapRow> MatchCapRows() { return kRows; }

}  // namespace ss
