// The world as the match / objective node types see it (T055, docs/StatescriptNodes.md §5): what a gamemode brain,
// a capture point, a payload or the tutorial needs from the game (players, teams, positions, target tags, capture
// areas, entities, match time) and the effects it has on the game. The runtime asks the host through this
// interface; it never keeps world state of its own. One host serves every sync manager of a match
// (SyncManager::match_host).
//
// Every query has a neutral default (no players, no team, not found), so a sync manager without a host runs these
// graphs as before (lifecycle only). Effects default to no-ops. Designs are marked [?] in the doc.
#pragma once
#include <cstdint>
#include <map>
#include <optional>
#include <span>
#include <vector>

#include "statescript/config_var.h"
#include "statescript/value.h"

namespace ss {

struct GameMessage;

struct Vec3f {
    float x = 0, y = 0, z = 0;
};

// Capture-area tuning from the placement's STUECCaptureAreaInstanceData (field offsets in the STU; the names are
// our reading of the values [?], docs/StatescriptNodes.md §5.3).
struct CaptureAreaRules {
    float decay_time = 10.0f;     // 0xa0b01a58 (+0x38): seconds to lose the whole unlocked progress [?]
    float f349aaca6 = 0.0f;       // 0x349aaca6 (+0x3c): 0 on every map [?]
    float segments = 3.0f;        // 0xa3a59991 (+0x40): progress locks per 1/segments [?]
    float extra_player = 1.0f;    // 0x95fb5638 (+0x44): rate added per extra capturer, up to max_players [?]
    float capture_time = 18.0f;   // 0xbef90de8 (+0x48): seconds for one capturer [?]
    float f5f080cc5 = 18.0f;      // 0x5f080cc5 (+0x4c): (≥ capture_time) not used [?]
    float decay_delay = 5.0f;     // 0xfdfb30ac (+0x50): seconds without capturers before the progress decays [?]
    int32_t f2ca7fc17 = 0;        // 0x2ca7fc17 (+0x58) [?]
    float fe6e7f301 = 1.0f;       // 0xe6e7f301 (+0x5c) [?]
    bool f3a691fdf = false;       // 0x3a691fdf (+0x60) [?]
    int32_t max_players = 3;      // how many capturers speed it up [?: Overwatch's rule, = segments on every map]
};

class MatchHost {
public:
    virtual ~MatchHost() = default;

    // ---- queries (config vars and states read the world) ----
    // The "all players" target set (config var 8f3d1c9d, no client class) [?]: the players' controlled heroes.
    virtual std::vector<uint32_t> Players() { return {}; }
    // Team of an entity (component 0x10 FilterBits; GetTeam d1d51f19): -1 = none.
    virtual int32_t Team(uint32_t /*entity*/) { return -1; }
    // The entity's final filter bits (component 0x10 +0x98): team t = 0x40000 << t; filters test other bits too
    // (0x1 on players' heroes, 0x10 excluded by every "live players" filter [?: dead]). Default: the team bit.
    virtual uint64_t FilterBits(uint32_t entity) {
        int32_t t = Team(entity);
        return t >= 0 && t < 8 ? 0x40000ull << t : 0;
    }
    virtual bool Exists(uint32_t /*entity*/) { return false; }
    virtual bool Position(uint32_t /*entity*/, Vec3f& /*out*/) { return false; }
    virtual bool Yaw(uint32_t /*entity*/, float& /*degrees*/) { return false; }
    // The gamemode entity (GetPvPEntity d5384f53: the newest entity with a PvP-game component).
    virtual uint32_t PvPEntity() { return kInvalidEntity; }
    // GetGlobalEntity (4f205faf): the game entity admin's global entity (UI / announcer queue holder).
    virtual uint32_t GlobalEntity() { return kInvalidEntity; }
    // Entities whose target tags match: any of `any` (ignored when empty), all of `all`, none of `none`; in the
    // entity admin's order (FindEntity decb4aff takes the first).
    virtual std::vector<uint32_t> FindByTags(std::span<const uint64_t> /*any*/, std::span<const uint64_t> /*all*/,
                                             std::span<const uint64_t> /*none*/) {
        return {};
    }
    // Uniform integer in [lo, hi] (IntRandom 6774a48a; the client's RNG is local): a host can make runs repeatable.
    virtual int32_t RandomInt(int32_t lo, int32_t /*hi*/) { return lo; }
    // Capture area of an entity (component 0x1a): the entities standing in it (with a team), its rules.
    virtual std::vector<uint32_t> InCaptureArea(uint32_t /*area*/) { return {}; }
    virtual CaptureAreaRules CaptureRules(uint32_t /*area*/) { return {}; }
    // Whether the capture area is enabled (what 3be5172e last set; the mirrored 0ab6f7cd).
    virtual bool CaptureEnabled(uint32_t /*area*/) { return false; }
    // The hero a player (controller) possesses (component 0x20 +0x98; GetPrimaryPossessedEntity 8b31201d); false = none.
    virtual bool PossessedEntity(uint32_t /*player*/, uint32_t& /*hero*/) { return false; }
    // Health (component 0x28): of pool `pool` (GetEntityHealth / MaxHealth, 36967fde) or of every pool (combined,
    // pool -1); false = no such entity (the client gives 0 for an entity without health).
    virtual bool Health(uint32_t /*entity*/, int32_t /*pool*/, bool /*max*/, float& /*out*/) { return false; }
    // The hero GUID a player selected (component 0x3F PlayerInfo +0x98; GetEntityHero 74bcf7ab), 0 = none.
    virtual uint64_t HeroOf(uint32_t /*entity*/) { return 0; }
    // An entity's movement throttle (GetThrottle cbd4f4f2): x, y in [-1, 1] (its input record, component 0x12).
    virtual Vec3f Throttle(uint32_t /*entity*/) { return {}; }
    // Whether a point lies in an entity's area shape (IsPointInArea ae45e451; spawn-room healing 0x0a7).
    virtual bool PointInArea(uint32_t /*area*/, const Vec3f& /*point*/) { return false; }
    // How long a voice line plays (VoiceStimulus), ms; nullopt = the node's own estimate (e341b9c4) [?].
    virtual std::optional<int64_t> VoiceLineMs(uint64_t /*stimulus*/) { return std::nullopt; }
    // Overtime (GetOvertimeRemaining 7705e5e6 / GetMaxOvertimeLength dc0a8d4e), seconds.
    virtual float OvertimeRemaining() { return 0.0f; }
    virtual float MaxOvertime() { return 0.0f; }

    // ---- effects ----
    // BroadcastGameMessage (ece45723): the message to every entity's Statescript, the sender's included [?].
    virtual void Broadcast(const GameMessage& /*msg*/) {}
    // SetTeamIndex (a257245a).
    virtual void SetTeam(uint32_t /*entity*/, int32_t /*team*/) {}
    // Add / RemoveTargetTags (bd6a3dff / 78184003) and the scoped ApplyTargetTags state (1ab3b4d7).
    virtual void AddTargetTags(uint32_t /*entity*/, std::span<const uint64_t> /*tags*/) {}
    virtual void RemoveTargetTags(uint32_t /*entity*/, std::span<const uint64_t> /*tags*/) {}
    // TeleportTo (de4f0f88): to `pos` (and facing `yaw` degrees when set).
    virtual void Teleport(uint32_t /*entity*/, const Vec3f& /*pos*/, std::optional<float> /*yaw*/) {}
    // CreateEntity (state e7c6aaf6 / action 0e3689f3): a new entity of `def`; returns its id (kInvalidEntity = none).
    virtual uint32_t CreateEntity(uint64_t /*def*/, const Vec3f& /*pos*/, float /*yaw*/, int32_t /*team*/,
                                  uint32_t /*creator*/) {
        return kInvalidEntity;
    }
    virtual void DestroyEntity(uint32_t /*entity*/) {}
    // RespawnPlayer (4f14c91b).
    virtual void Respawn(uint32_t /*entity*/) {}
    // 670cf926: add match time (s).
    virtual void AddMatchTime(float /*seconds*/) {}
    // GameFinished (f90c6c08) / EndMatch (f1201eda).
    virtual void GameFinished(int32_t /*winning_team*/) {}
    virtual void EndMatch() {}
    // Capture area control: 3be5172e (enable), 350e207d (owning team); the capture state reports its progress.
    virtual void SetCaptureEnabled(uint32_t /*area*/, bool /*on*/) {}
    virtual void SetCaptureOwner(uint32_t /*area*/, int32_t /*team*/) {}
    virtual void CaptureProgress(uint32_t /*area*/, float /*progress*/, int32_t /*capturing_team*/, bool /*contested*/) {}
    // The payload (graph 0x098): the path mover 616f509f moves the cart along its path at `speed` (m/s, negative =
    // back) for `dt` s; the host owns the path (component 0x17 data) and reports the progress (0…1), a waypoint
    // entity passed on the way (kInvalidEntity = none) and whether the end (forward) / start (back) was reached.
    struct PayloadStep {
        float progress = 0.0f;
        uint32_t waypoint = kInvalidEntity;
        bool at_end = false;
    };
    virtual bool PayloadAdvance(uint32_t /*cart*/, float /*speed*/, float /*dt*/, PayloadStep& /*out*/) { return false; }
    // UpdatePayload (6c2502e0): the players near the cart and whether it moves, for the payload component 0x19 [?].
    virtual void PayloadStatus(uint32_t /*cart*/, int /*attackers*/, int /*defenders*/, bool /*moving*/) {}
    // 12213f7e (spawn points): enable / disable a spawn point [?].
    virtual void SetSpawnEnabled(uint32_t /*entity*/, bool /*on*/) {}

    // CreateReel (9f8f5a55, round end 0x12d / death cam 0x1cb) [?]: a replay clip of `seconds` (7 kill cam, 12
    // highlight) ending `offset` s before now; returns its id (the graphs' $55e, which the client's reel request
    // cf08df77 plays), −1 = none (the client then shows no kill cam). PublishReel cc7b6828, 6dbdfe7c and d5c8d941 are
    // host events (SyncManager::host_events).
    virtual int32_t CreateReel(uint32_t /*owner*/, int32_t /*seconds*/, float /*offset*/) { return -1; }
    // 31993d91 (no client class, 0x12d): the reel's time t for `entity` (invalid = the match) [?]; nullopt = no value
    // (as the client's base evaluator: the expression then reads 0).
    virtual std::optional<float> ReelTime(uint32_t /*entity*/) { return std::nullopt; }

    // ---- movement, collision, position history, time scale (T060, docs/StatescriptNodes.md §3.16) ----
    // The states of these systems run their graph side in the runtime (timing, plugs, var claims, state data); what
    // the engine does is asked here. Defaults: nothing moves, nothing is hit (each state's "no host" behaviour).
    // A cast in the owner's collision world from `from` to `to` (filter = the node's m_castFilter / 19ab05f7 config
    // var, evaluated by the host with ss_sm_eval_field if it needs it; the owner is ignored).
    struct CastHit {
        float fraction = 1.0f;  // of from → to
        Vec3f point;
        uint32_t entity = kInvalidEntity;  // the entity hit (invalid for static geometry)
    };
    virtual std::optional<CastHit> Cast(uint32_t /*owner*/, const Vec3f& /*from*/, const Vec3f& /*to*/) { return std::nullopt; }
    // Aim of an entity for a hardpoint (Extender 26cb90c0, 0x1096080): the source point and the unit direction.
    virtual bool Aim(uint32_t /*entity*/, uint64_t /*hardpoint*/, Vec3f& /*source*/, Vec3f& /*dir*/) { return false; }
    // Mover controllers on the entity's CharacterMover (component 0x15, 0x10ab1f0): kind 6 Accelerate {accel xyz,
    // terminal velocity}, 12 c8c71bbc climb {from xyz, to xyz, time}, 13 f536a60f curve {weight, bf6d8f58, curve}.
    // key = instance id << 48 | node index << 32 | state serial; active from start_cf until MoverEnd's end_cf.
    struct MoverController {
        uint64_t key = 0;
        int kind = 0;
        float priority = 0.0f;
        uint32_t start_cf = 0;
        float p[7] = {0, 0, 0, 0, 0, 0, 0};
        uint64_t guid = 0;  // kind 13: the curve
    };
    virtual void MoverAdd(uint32_t /*entity*/, const MoverController& /*c*/) {}
    virtual void MoverEnd(uint32_t /*entity*/, uint64_t /*key*/, uint32_t /*end_cf*/) {}
    // Kind 12: the climb is done (the owner's movement record +0x198 & 0x20 and +0xCC == 2); nullopt = the host
    // does not model it (the state then finishes after its m_climbTime [?]).
    virtual std::optional<bool> MoverDone(uint32_t /*entity*/, uint64_t /*key*/) { return std::nullopt; }
    // TrackSlamming 76391dd3: the owner's forward progress d ∈ [−1, 1] of a mover sweep over `lookahead` s (1 = free).
    virtual std::optional<float> ForwardProgress(uint32_t /*entity*/, float /*lookahead*/) { return std::nullopt; }
    // Shockwave 8d50485a: the entities the ring of `radius` (arc `arc` degrees around yaw `yaw`) at `centre` reaches,
    // passing the node's filters (the host evaluates m_filter / 08f9cb86 / the sizes, ss_sm_eval_field).
    virtual std::vector<uint32_t> ShockwaveHits(uint32_t /*owner*/, const Vec3f& /*centre*/, float /*yaw*/,
                                                float /*arc*/, float /*radius*/) {
        return {};
    }
    // Position history (HistoryBuilder 029bf346 / HistoryRewinder 7211cf70, Tracer's Recall). `handle` = the
    // builder's {instance id, state index}.
    virtual void HistoryRecord(uint32_t /*entity*/, uint32_t /*handle*/, float /*length_s*/, float /*rate_hz*/,
                               bool /*transforms*/) {}
    virtual void HistoryStop(uint32_t /*entity*/, uint32_t /*handle*/) {}
    struct Rewind {
        float span = 0, duration = 0, blend_in = 0, blend_out = 0, smoothing = 0;
        int32_t easing = 0;
    };
    virtual bool HistoryRewind(uint32_t /*entity*/, uint32_t /*handle*/, const Rewind& /*r*/) { return false; }
    virtual void HistoryRewindEnd(uint32_t /*entity*/, uint32_t /*handle*/) {}
    // ScaleTime 7bf31629: a time scale of the scope from start_cf (ramp to `scale` until ramp_end_cf); stop_cf /
    // stop_end_cf = 0 while it runs.
    virtual void TimeScale(uint32_t /*owner*/, uint64_t /*key*/, float /*scale*/, uint32_t /*start_cf*/,
                           uint32_t /*ramp_end_cf*/, uint32_t /*stop_cf*/, uint32_t /*stop_end_cf*/) {}

    // ---- global vars (component 0x51 StatescriptGlobalVarBag, docs/StatescriptNodes.md §3.14) ----
    // A registry {0x0D8 id → the publishing state}: the PublishGlobals state (a4f3be65, brain A's phase $0195)
    // publishes its m_entries while active; reading an id evaluates the entry's m_value in the publishing instance
    // (Get 0xceb2b0). MatchGlobalVar reads one. Overridable (a host that mirrors them), the default keeps the map.
    struct GlobalRef {
        uint32_t entity = kInvalidEntity;
        int16_t instance = 0;
        int32_t state = -1;
    };
    virtual void PublishGlobal(uint64_t key, const GlobalRef& ref) { globals_[key] = ref; }
    virtual void UnpublishGlobal(uint64_t key) { globals_.erase(key); }
    const GlobalRef* FindGlobal(uint64_t key) const {
        auto it = globals_.find(key);
        return it == globals_.end() ? nullptr : &it->second;
    }
    const std::map<uint64_t, GlobalRef>& globals() const { return globals_; }

private:
    std::map<uint64_t, GlobalRef> globals_;
};

// Game events the host reports to a sync manager's player-event states (97315c42: brains A / B, Dorado).
enum class PlayerEvent { Joined, Left, Died };
class SyncManager;
// Fires the event on every active player-event state of `sm` (`other` = the killer for Died).
void MatchPlayerEvent(SyncManager& sm, PlayerEvent ev, uint32_t player, uint32_t other = kInvalidEntity);
// The capture progress (0…1) of the first active capture-point state of `sm`, -1 if none (tools / tests).
float MatchCaptureProgress(const SyncManager& sm);
// The value of global var `key` (component 0x51 Get 0xceb2b0) [G]: the publishing state's entry m_value evaluated in
// its instance; `sm` = any sync manager of the match (its host and entity resolver find the publisher). false = not
// published / the publisher is gone.
bool MatchGlobalVar(SyncManager& sm, uint64_t key, Primitive& out);

}  // namespace ss
