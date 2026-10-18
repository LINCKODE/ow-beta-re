// The in-process match world (include/statescript/match_world.h; T055, docs/StatescriptNodes.md §5.6).
#include "statescript/match_world.h"

#include <algorithm>
#include <cmath>
#include <format>

namespace ss {

MatchWorld::MatchWorld(const EvaluatorRegistry& evals, std::function<const Graph*(uint64_t)> graphs)
    : evals_(evals), graphs_(std::move(graphs)), self_(this, [](MatchHost*) {}) {}

MatchWorld::~MatchWorld() {
    // Instances subscribe through other entities' bags: tear the sync managers down before the entities.
    for (auto& e : entities_) e->sm.reset();
}

void MatchWorld::Log(std::string s) {
    s = std::format("cf {}: {}", cf_, s);
    if (on_log) on_log(s);
    log.push_back(std::move(s));
}

MatchWorld::Effect& MatchWorld::Emit(Fx kind, uint32_t entity) {
    Effect& fx = effects.emplace_back();
    fx.seq = ++effect_seq, fx.cf = cf_, fx.kind = kind, fx.entity = entity;
    return fx;
}

MatchWorld::Entity& MatchWorld::Add(uint32_t id, uint64_t def, bool with_sm) {
    auto e = std::make_unique<Entity>();
    e->id = id;
    e->def = def;
    if (!with_sm) {
        entities_.push_back(std::move(e));
        return *entities_.back();
    }
    e->sm = std::make_unique<SyncManager>(id, evals_, Role::Server);
    e->sm->stack_priorities = stack_priorities;
    // Mid-step (a CreateEntity) the entity runs this frame with the others: its clock ends at the previous frame.
    uint32_t at = in_step_ ? cf_ - 1 : cf_;
    e->sm->SetClock(at, int64_t(at) * SyncManager::kMsPerCf + 15);
    e->sm->graph_provider = graphs_;
    e->sm->entity_resolver = [this](uint32_t x) -> SyncManager* {
        Entity* t = Find(x);
        return t ? t->sm.get() : nullptr;
    };
    e->sm->match_host = self_;
    e->sm->host_event_hook = [this, id](const SyncManager::HostEvent& h) {
        static const char* kinds[] = {"action", "begin", "finished", "aborted"};
        host_events.emplace_back(id, std::format("cf {} entity {:x} inst {} node {} type {:08x} {}", h.cf, id, h.instance,
                                                 h.node, h.type, h.kind >= 0 && h.kind < 4 ? kinds[h.kind] : "?"));
    };
    entities_.push_back(std::move(e));
    return *entities_.back();
}

MatchWorld::Entity* MatchWorld::Find(uint32_t id) {
    for (auto& e : entities_)
        if (e->id == id && e->alive) return e.get();
    return nullptr;
}

bool MatchWorld::Remove(uint32_t id) {
    Entity* e = Find(id);
    if (!e) return false;
    e->alive = false;  // kept (other entities' instances may subscribe to its sync manager); invisible to queries
    return true;
}

Instance* MatchWorld::Start(uint32_t entity, uint64_t graph, std::span<const Override> overrides) {
    Entity* e = Find(entity);
    const Graph* g = graphs_ ? graphs_(graph) : nullptr;
    if (!e || !e->sm || !g) {
        Log(std::format("start {:016x} on {:x}: {}", graph, entity, !e || !e->sm ? "no entity" : "graph not loaded"));
        return nullptr;
    }
    int16_t id = mirrored_ids ? e->sm->AllocateId(true) : 0;
    if (mirrored_ids && id <= 0) {
        Log(std::format("start {:016x} on {:x}: no instance id left", graph, entity));
        return nullptr;
    }
    return e->sm->CreateInstance(*g, overrides, id);
}

void MatchWorld::Send(const GameMessage& msg, uint32_t target) {
    Log(std::format("send {:x} to {}", msg.id & 0xFFFFFFFFFFFF, target ? std::format("{:x}", target) : "all"));
    for (size_t k = 0; k < entities_.size(); k++) {
        Entity* e = entities_[k].get();
        if (!e->alive || !e->sm || (target && e->id != target)) continue;
        e->sm->DeliverGameMessageFrom(msg, true);
    }
}

void MatchWorld::PlayerEvent(ss::PlayerEvent ev, uint32_t player, uint32_t other) {
    static const char* kinds[] = {"joined", "left", "died"};
    Log(std::format("player {:x} {}", player, kinds[int(ev)]));
    for (size_t k = 0; k < entities_.size(); k++)
        if (entities_[k]->alive && entities_[k]->sm) MatchPlayerEvent(*entities_[k]->sm, ev, player, other);
}

void MatchWorld::Step(uint32_t n) {
    for (uint32_t k = 0; k < n; k++) {
        cf_++;
        in_step_ = true;
        for (size_t i = 0; i < entities_.size(); i++)  // entities created during the frame run it too
            if (entities_[i]->alive && entities_[i]->sm) entities_[i]->sm->RunCommandFrames(cf_, 1);
        in_step_ = false;
    }
}

std::vector<uint32_t> MatchWorld::Players() {
    std::vector<uint32_t> out;
    for (auto& e : entities_)
        if (e->alive && e->player) out.push_back(e->id);
    return out;
}

int32_t MatchWorld::Team(uint32_t id) {
    Entity* e = Find(id);
    return e ? e->team : -1;
}

bool MatchWorld::Exists(uint32_t id) { return Find(id) != nullptr; }

uint64_t MatchWorld::FilterBits(uint32_t id) {
    Entity* e = Find(id);
    if (!e) return 0;
    uint64_t bits = e->extra_bits | (e->player ? 1 : 0);
    if (e->team >= 0 && e->team < 8) bits |= 0x40000ull << e->team;
    return bits;
}

bool MatchWorld::PayloadAdvance(uint32_t id, float speed, float dt, PayloadStep& out) {
    Entity* e = Find(id);
    if (!e || e->path.size() < 2) return false;
    std::vector<float> at(e->path.size(), 0.0f);  // distance of each path point from the start
    for (size_t k = 1; k < e->path.size(); k++) {
        const Vec3f &a = e->path[k - 1], &b = e->path[k];
        at[k] = at[k - 1] + std::sqrt((b.x - a.x) * (b.x - a.x) + (b.y - a.y) * (b.y - a.y) + (b.z - a.z) * (b.z - a.z));
    }
    float total = at.back(), before = e->path_distance;
    e->path_distance = std::clamp(before + speed * dt, 0.0f, total);
    for (auto& [k, wp] : e->path_waypoints)  // passed forward
        if (before < at[k] && e->path_distance >= at[k]) out.waypoint = wp;
    size_t k = 1;
    while (k + 1 < at.size() && at[k] < e->path_distance) k++;
    float seg = at[k] - at[k - 1], f = seg > 0 ? (e->path_distance - at[k - 1]) / seg : 0.0f;
    const Vec3f &a = e->path[k - 1], &b = e->path[k];
    e->pos = {a.x + (b.x - a.x) * f, a.y + (b.y - a.y) * f, a.z + (b.z - a.z) * f};
    out.progress = total > 0 ? e->path_distance / total : 0.0f;
    out.at_end = speed > 0 ? e->path_distance >= total : e->path_distance <= 0.0f;
    if (int(before / total * 10) != int(e->path_distance / total * 10) || !IsInvalidEntity(out.waypoint))
        Log(std::format("payload {:x} progress {:.0f}%{}", id, out.progress * 100,
                        IsInvalidEntity(out.waypoint) ? "" : std::format(", waypoint {:x}", out.waypoint)));
    return true;
}

void MatchWorld::PayloadStatus(uint32_t id, int attackers, int defenders, bool moving) {
    Log(std::format("payload {:x}: {} attacker(s), {} defender(s), {}", id, attackers, defenders, moving ? "moving" : "stopped"));
    Effect& fx = Emit(Fx::PayloadStatus, id);
    fx.i0 = attackers, fx.i1 = defenders, fx.f[0] = moving ? 1.0f : 0.0f;
}

bool MatchWorld::Position(uint32_t id, Vec3f& out) {
    Entity* e = Find(id);
    if (e) out = e->pos;
    return e != nullptr;
}

bool MatchWorld::Yaw(uint32_t id, float& deg) {
    Entity* e = Find(id);
    if (e) deg = e->yaw;
    return e != nullptr;
}

uint32_t MatchWorld::PvPEntity() {
    for (size_t k = entities_.size(); k-- > 0;)  // the newest entity with a PvP-game component (MatchLogic.md §10.2)
        if (entities_[k]->alive && entities_[k]->pvp) return entities_[k]->id;
    return kInvalidEntity;
}

std::vector<uint32_t> MatchWorld::FindByTags(std::span<const uint64_t> any, std::span<const uint64_t> all,
                                             std::span<const uint64_t> none) {
    std::vector<uint32_t> out;
    for (auto& e : entities_) {
        if (!e->alive) continue;
        auto has = [&](uint64_t t) { return std::find(e->tags.begin(), e->tags.end(), t) != e->tags.end(); };
        if (!any.empty() && std::none_of(any.begin(), any.end(), has)) continue;
        if (!std::all_of(all.begin(), all.end(), has)) continue;
        if (std::any_of(none.begin(), none.end(), has)) continue;
        out.push_back(e->id);
    }
    return out;
}

int32_t MatchWorld::RandomInt(int32_t lo, int32_t hi) {
    seed ^= seed << 13, seed ^= seed >> 17, seed ^= seed << 5;
    uint32_t span = uint32_t(hi - lo) + 1;
    return span ? lo + int32_t(seed % span) : int32_t(seed);
}

std::vector<uint32_t> MatchWorld::InCaptureArea(uint32_t area) {
    std::vector<uint32_t> out;
    Entity* a = Find(area);
    if (!a || !a->capture_area) return out;
    for (auto& e : entities_) {
        if (!e->alive || !e->player) continue;
        float dx = e->pos.x - a->pos.x, dy = e->pos.y - a->pos.y, dz = e->pos.z - a->pos.z;
        if (dx * dx + dy * dy + dz * dz <= a->capture_radius * a->capture_radius) out.push_back(e->id);
    }
    return out;
}

bool MatchWorld::PointInArea(uint32_t area, const Vec3f& p) {
    Entity* a = Find(area);
    if (!a) return false;
    float dx = p.x - a->pos.x, dy = p.y - a->pos.y, dz = p.z - a->pos.z;
    return dx * dx + dy * dy + dz * dz <= a->capture_radius * a->capture_radius;
}

// A player's controller possesses its hero; a hero without a controller stands for itself (offline worlds).
bool MatchWorld::PossessedEntity(uint32_t player, uint32_t& hero) {
    for (auto& e : entities_)
        if (e->alive && e->player && e->controller == player) return hero = e->id, true;
    Entity* e = Find(player);
    if (!e || !e->player) return false;
    hero = player;
    return true;
}

bool MatchWorld::Health(uint32_t id, int32_t pool, bool max, float& out) {
    Entity* e = Find(id);
    if (!e) return false;
    out = 0.0f;
    for (auto& [p, hv] : e->health)
        if (pool < 0 || p == pool) out += max ? hv.second : hv.first;
    return true;
}

uint64_t MatchWorld::HeroOf(uint32_t id) {
    uint32_t hero = id;
    Entity* e = Find(id);
    if (e && !e->player) PossessedEntity(id, hero);
    Entity* h = Find(hero);
    return h ? h->hero_guid : 0;
}

Vec3f MatchWorld::Throttle(uint32_t id) {
    Entity* e = Find(id);
    return e ? e->throttle : Vec3f{};
}

CaptureAreaRules MatchWorld::CaptureRules(uint32_t area) {
    Entity* a = Find(area);
    return a ? a->rules : CaptureAreaRules{};
}

bool MatchWorld::CaptureEnabled(uint32_t area) {
    Entity* a = Find(area);
    return a && a->capture_enabled;
}

void MatchWorld::Broadcast(const GameMessage& msg) {
    Log(std::format("broadcast {:x} from {:x}", msg.id & 0xFFFFFFFFFFFF, msg.sender));
    Emit(Fx::Broadcast, kInvalidEntity).other = msg.sender;
    effects.back().u = msg.id;
    for (size_t k = 0; k < entities_.size(); k++) {
        Entity* e = entities_[k].get();
        if (e->alive && e->sm) e->sm->DeliverGameMessageFrom(msg, e->id != msg.sender);
    }
}

void MatchWorld::SetTeam(uint32_t id, int32_t team) {
    Log(std::format("team {:x} := {}", id, team));
    if (Entity* e = Find(id)) e->team = team;
    Emit(Fx::Team, id).i0 = team;
}

void MatchWorld::AddTargetTags(uint32_t id, std::span<const uint64_t> tags) {
    Entity* e = Find(id);
    if (!e) return;
    for (uint64_t t : tags)
        if (std::find(e->tags.begin(), e->tags.end(), t) == e->tags.end()) e->tags.push_back(t);
    Log(std::format("tags {:x} += {} tag(s)", id, tags.size()));
    for (uint64_t t : tags) Emit(Fx::TagsAdd, id).u = t;
}

void MatchWorld::RemoveTargetTags(uint32_t id, std::span<const uint64_t> tags) {
    Entity* e = Find(id);
    if (!e) return;
    for (uint64_t t : tags) std::erase(e->tags, t);
    Log(std::format("tags {:x} -= {} tag(s)", id, tags.size()));
    for (uint64_t t : tags) Emit(Fx::TagsRemove, id).u = t;
}

void MatchWorld::Teleport(uint32_t id, const Vec3f& pos, std::optional<float> yaw) {
    Entity* e = Find(id);
    if (!e) return;
    e->pos = pos;
    if (yaw) e->yaw = *yaw;
    Log(std::format("teleport {:x} to ({:.1f}, {:.1f}, {:.1f})", id, pos.x, pos.y, pos.z));
    Effect& fx = Emit(Fx::Teleport, id);
    fx.f[0] = pos.x, fx.f[1] = pos.y, fx.f[2] = pos.z, fx.f[3] = e->yaw, fx.i1 = yaw ? 1 : 0;
}

uint32_t MatchWorld::CreateEntity(uint64_t def, const Vec3f& pos, float yaw, int32_t team, uint32_t creator) {
    if (id_end && next_id >= id_end) {
        Log(std::format("create def {:x}: no entity ids left", def & 0xFFFFFFFFFFFF));
        return kInvalidEntity;
    }
    uint32_t id = next_id++;
    Entity& e = Add(id, def);
    e.pos = pos, e.yaw = yaw, e.team = team;
    Log(std::format("create {:x} def {:x} team {} by {:x}", id, def & 0xFFFFFFFFFFFF, team, creator));
    Effect& fx = Emit(Fx::Create, id);
    fx.other = creator, fx.i0 = team, fx.u = def, fx.f[0] = pos.x, fx.f[1] = pos.y, fx.f[2] = pos.z, fx.f[3] = yaw;
    if (auto it = def_graphs.find(def); it != def_graphs.end())
        for (uint64_t g : it->second) Start(id, g);
    return id;
}

void MatchWorld::DestroyEntity(uint32_t id) {
    Entity* e = Find(id);
    if (!e) return;
    Log(std::format("destroy {:x}", id));
    Emit(Fx::Destroy, id);
    e->alive = false;  // kept (its sync manager may be running this frame); invisible to queries
}

void MatchWorld::Respawn(uint32_t id) {
    Log(std::format("respawn {:x}", id));
    Emit(Fx::Respawn, id);
}
void MatchWorld::AddMatchTime(float s) {
    match_time_added += s;
    Log(std::format("match time += {} s", s));
    Emit(Fx::MatchTime, kInvalidEntity).f[0] = s;
}
void MatchWorld::GameFinished(int32_t team) {
    winner = team;
    Log(std::format("game finished, winner {}", team));
    Emit(Fx::GameFinished, kInvalidEntity).i0 = team;
}
void MatchWorld::EndMatch() {
    ended = true;
    Log("end match");
    Emit(Fx::EndMatch, kInvalidEntity);
}
void MatchWorld::SetCaptureEnabled(uint32_t id, bool on) {
    Entity* e = Find(id);
    if (!e) return;
    if (e->capture_enabled != on) {
        Log(std::format("capture {:x} {}", id, on ? "enabled" : "disabled"));
        Emit(Fx::CaptureEnabled, id).i0 = on;
    }
    e->capture_enabled = on;
}
void MatchWorld::SetCaptureOwner(uint32_t id, int32_t team) {
    Entity* e = Find(id);
    if (!e) return;
    if (e->capture_owner != team) Emit(Fx::CaptureOwner, id).i0 = team;
    e->capture_owner = team;
    Log(std::format("capture {:x} owner team {}", id, team));
}
void MatchWorld::CaptureProgress(uint32_t id, float p, int32_t team, bool contested) {
    Entity* e = Find(id);
    if (!e) return;
    int before = int(e->capture_progress * 10), after = int(p * 10);
    if (p != e->capture_progress || team != e->capture_team || contested != e->capture_contested) {
        Effect& fx = Emit(Fx::CaptureProgress, id);
        fx.f[0] = p, fx.i0 = team, fx.i1 = contested;
    }
    e->capture_progress = p;
    e->capture_team = team, e->capture_contested = contested;
    if (before != after) Log(std::format("capture {:x} progress {:.0f}% (team {}{})", id, p * 100, team, contested ? ", contested" : ""));
}
void MatchWorld::SetSpawnEnabled(uint32_t id, bool on) {
    Entity* e = Find(id);
    if (!e) return;
    if (e->spawn_enabled != on) {
        Log(std::format("spawn {:x} {}", id, on ? "enabled" : "disabled"));
        Emit(Fx::SpawnEnabled, id).i0 = on;
    }
    e->spawn_enabled = on;
}

}  // namespace ss
