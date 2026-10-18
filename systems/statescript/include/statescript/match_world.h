// An in-process match world (T055, docs/StatescriptNodes.md §5.6): entities with a Statescript sync manager each,
// positions, teams, target tags, capture areas and players, implementing MatchHost. Offline runs of the gamemode and
// objective graphs use it (tests, tools/ss_match); a server can use it as the reference for its own host.
#pragma once
#include <cstdint>
#include <functional>
#include <map>
#include <memory>
#include <string>
#include <vector>

#include "statescript/match_host.h"
#include "statescript/runtime.h"

namespace ss {

class MatchWorld : public MatchHost {
public:
    struct Entity {
        uint32_t id = 0;
        uint64_t def = 0;
        Vec3f pos;
        float yaw = 0;
        int32_t team = -1;
        bool player = false, pvp = false, spawn_enabled = true, alive = true;
        std::vector<uint64_t> tags;
        // Capture area (component 0x1a): radius of the zone around pos [?: the shape asset 0x493b0749 is not read],
        // rules from the placement, enabled flag and owner team as the capture actions set them.
        bool capture_area = false, capture_enabled = false;
        float capture_radius = 0.0f;  // also the area of IsPointInArea (spawn rooms) [?]
        CaptureAreaRules rules;
        int32_t capture_owner = -1;
        float capture_progress = 0.0f;
        int32_t capture_team = -1;  // the capturing team the capture state last reported
        bool capture_contested = false;
        uint64_t extra_bits = 0;
        Vec3f throttle;  // movement input (x, y)
        // A player's hero (T057): its controller (GetEntityController / GetPrimaryPossessedEntity), the selected hero
        // GUID (component 0x3F) and health per pool (component 0x28: current, max).
        uint32_t controller = kInvalidEntity;
        uint64_t hero_guid = 0;
        std::map<int32_t, std::pair<float, float>> health;
        // A payload cart: its path (positions, the first = the start) and the waypoint entity at path points.
        std::vector<Vec3f> path;
        std::map<size_t, uint32_t> path_waypoints;
        float path_distance = 0.0f;
        std::unique_ptr<SyncManager> sm;
    };

    // `graphs`: graph GUID → loaded graph (PlayScript, SubScript and the definitions' graphs).
    MatchWorld(const EvaluatorRegistry& evals, std::function<const Graph*(uint64_t)> graphs);
    ~MatchWorld() override;

    // A new entity with an (empty) sync manager, clocked at the world's current command frame. with_sm = false: a
    // fact-only entity (a player's hero whose Statescript runs elsewhere): queries see it, messages don't reach it.
    Entity& Add(uint32_t id, uint64_t def = 0, bool with_sm = true);
    // A live entity (fact-only ones included).
    Entity* Find(uint32_t id);
    // Removes an entity at once (the host's world lost it); DestroyEntity is the graphs' effect.
    bool Remove(uint32_t id);
    // Starts a graph on an entity (default script: schema defaults, then `overrides`).
    Instance* Start(uint32_t entity, uint64_t graph, std::span<const Override> overrides = {});
    // Graphs a definition starts when the host creates an entity of it (CreateEntity).
    std::map<uint64_t, std::vector<uint64_t>> def_graphs;
    // Delivers a game message (as the server's rules layer would) to one entity, or to every entity (target 0).
    void Send(const GameMessage& msg, uint32_t target = 0);
    // Runs command frames cf() + 1 … cf() + n on every entity in order.
    void Step(uint32_t n = 1);
    // Sets the world's clock before any entity exists (a server starts it at the match's frame).
    void SetFrame(uint32_t cf) { cf_ = cf; }
    // A player event (joined / left / died) on every entity's player-event states.
    void PlayerEvent(ss::PlayerEvent ev, uint32_t player, uint32_t other = kInvalidEntity);
    uint32_t cf() const { return cf_; }
    int64_t ms() const { return int64_t(cf_) * SyncManager::kMsPerCf + 15; }
    uint32_t next_id = 0x40000100;  // ids for host-created entities: next_id … id_end − 1 (id_end 0 = unbounded)
    uint32_t id_end = 0;
    uint32_t global_entity = kInvalidEntity;  // GetGlobalEntity
    // Started instances get positive (mirrored) ids, as a server's must to be replicated (T057); offline worlds
    // keep the local negative ids of LoadGraph.
    bool mirrored_ids = false;
    std::map<uint64_t, SyncManager::StackPriority> stack_priorities;  // copied to every new sync manager

    // Effects in order (T057): what the graphs did to the world, for a server to apply and replicate. Also logged.
    enum class Fx {
        Team, TagsAdd, TagsRemove, Teleport, Create, Destroy, Respawn, MatchTime, GameFinished, EndMatch,
        CaptureEnabled, CaptureOwner, CaptureProgress, SpawnEnabled, PayloadStatus, Broadcast
    };
    struct Effect {
        uint32_t seq = 0, cf = 0;
        Fx kind = Fx::Team;
        uint32_t entity = kInvalidEntity, other = kInvalidEntity;  // subject; creator / sender
        int32_t i0 = 0, i1 = 0;                                    // team / flag / counts
        float f[4] = {0, 0, 0, 0};                                 // position + yaw, progress, seconds
        uint64_t u = 0;                                            // definition, message id, first tag
    };
    std::vector<Effect> effects;  // appended; a host drains it
    uint32_t effect_seq = 0;
    uint32_t seed = 12345;          // RandomInt's generator (xorshift)
    std::vector<std::string> log;   // effects and events, "cf <n>: <text>"
    std::function<void(const std::string&)> on_log;

    // MatchHost
    std::vector<uint32_t> Players() override;
    int32_t Team(uint32_t entity) override;
    uint64_t FilterBits(uint32_t entity) override;
    bool PayloadAdvance(uint32_t cart, float speed, float dt, PayloadStep& out) override;
    void PayloadStatus(uint32_t cart, int attackers, int defenders, bool moving) override;
    bool Exists(uint32_t entity) override;
    bool Position(uint32_t entity, Vec3f& out) override;
    bool Yaw(uint32_t entity, float& degrees) override;
    uint32_t PvPEntity() override;
    std::vector<uint32_t> FindByTags(std::span<const uint64_t> any, std::span<const uint64_t> all,
                                     std::span<const uint64_t> none) override;
    int32_t RandomInt(int32_t lo, int32_t hi) override;
    std::vector<uint32_t> InCaptureArea(uint32_t area) override;
    CaptureAreaRules CaptureRules(uint32_t area) override;
    bool CaptureEnabled(uint32_t area) override;
    bool PointInArea(uint32_t area, const Vec3f& point) override;
    bool PossessedEntity(uint32_t player, uint32_t& hero) override;
    uint32_t GlobalEntity() override { return global_entity; }
    bool Health(uint32_t entity, int32_t pool, bool max, float& out) override;
    uint64_t HeroOf(uint32_t entity) override;
    Vec3f Throttle(uint32_t entity) override;
    void Broadcast(const GameMessage& msg) override;
    void SetTeam(uint32_t entity, int32_t team) override;
    void AddTargetTags(uint32_t entity, std::span<const uint64_t> tags) override;
    void RemoveTargetTags(uint32_t entity, std::span<const uint64_t> tags) override;
    void Teleport(uint32_t entity, const Vec3f& pos, std::optional<float> yaw) override;
    uint32_t CreateEntity(uint64_t def, const Vec3f& pos, float yaw, int32_t team, uint32_t creator) override;
    void DestroyEntity(uint32_t entity) override;
    void Respawn(uint32_t entity) override;
    void AddMatchTime(float seconds) override;
    void GameFinished(int32_t winning_team) override;
    void EndMatch() override;
    void SetCaptureEnabled(uint32_t area, bool on) override;
    void SetCaptureOwner(uint32_t area, int32_t team) override;
    void CaptureProgress(uint32_t area, float progress, int32_t capturing_team, bool contested) override;
    void SetSpawnEnabled(uint32_t entity, bool on) override;

    float match_time_added = 0;
    int32_t winner = -2;  // GameFinished's team (-2 = not finished)
    bool ended = false;
    std::vector<std::pair<uint32_t, std::string>> host_events;  // server-only node events of every entity

private:
    void Log(std::string s);
    Effect& Emit(Fx kind, uint32_t entity);
    const EvaluatorRegistry& evals_;
    std::function<const Graph*(uint64_t)> graphs_;
    std::shared_ptr<MatchHost> self_;
    std::vector<std::unique_ptr<Entity>> entities_;  // creation order (the entity admin's order)
    uint32_t cf_ = 100;
    bool in_step_ = false;
};

}  // namespace ss
