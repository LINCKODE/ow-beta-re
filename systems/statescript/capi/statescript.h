/* C API over the Statescript runtime (systems/statescript), for cgo / ctypes. Opaque handles, POD structs,
 * return codes; no exceptions or STL across the boundary. Semantics: docs/StatescriptRuntime.md §5.
 *
 * Lifetimes: a runtime owns the loaded graphs and the config-var evaluators; it must outlive every sync
 * manager and world created from it. A world owns an ECS entity admin whose entities' component 0x23 each
 * own a sync manager (ss_world_sm hands out a borrowed handle, valid while the entity exists). Nothing is
 * thread-safe: use one runtime per thread or lock around it. */
#ifndef OW_STATESCRIPT_CAPI_H
#define OW_STATESCRIPT_CAPI_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct ss_runtime ss_runtime;
typedef struct ss_sm ss_sm;
typedef struct ss_world ss_world;

enum {
    SS_OK = 0,
    SS_ERR_ARG = -1,       /* NULL handle / bad argument */
    SS_ERR_PARSE = -2,     /* the bytes are not a Statescript graph */
    SS_ERR_NOT_FOUND = -3, /* no such graph / instance / var / entity */
    SS_ERR_EXISTS = -4,    /* instance id in use */
};

/* STU image encodings (same values as STU_MODE_*). */
enum { SS_MODE_FILE = 0, SS_MODE_LOADED = 1 };
/* Filters of the sync manager: client (server-only nodes are skipped) or server (client-only nodes are). */
enum { SS_ROLE_CLIENT = 0, SS_ROLE_SERVER = 1 };

/* StatescriptPrimitive: type (1 BYTE, 2 INT, 3 FLT, 4 INT64, 6 VEC3, 7 INTVEC4, 8 INSTANCEID, 9 FLT16VEC4,
 * 10 COLOR, 11 ENTITY; strings are not passed through the C API), payload bytes 0..7 and 8..11. */
typedef struct ss_value {
    uint32_t type;
    uint64_t bits;
    uint32_t extra;
} ss_value;

/* ---- runtime: graphs and evaluators ---- */
ss_runtime* ss_runtime_create(void);
void ss_runtime_destroy(ss_runtime* rt);
/* Loads a graph (GUID type 0x058) from STUD bytes; the bytes are copied. */
int ss_runtime_load_graph(ss_runtime* rt, uint64_t guid, const uint8_t* bytes, size_t len, int mode, uint64_t base);
/* A StackPriority record (GUID type 0x0C4, STU 98b439ca: m_priority f32, 0xa6518307 wins ties; read them from
 * content). Needed by Stack and Ability states: an Ability whose priority record is missing never takes its
 * stack slot (never starts). Applies to sync managers created afterwards. */
int ss_runtime_set_stack_priority(ss_runtime* rt, uint64_t guid, float priority, int wins_ties);
/* Every config var of STU type `cv_type` evaluates to `value` (host-provided game state). */
int ss_runtime_set_constant(ss_runtime* rt, uint32_t cv_type, ss_value value);

/* ---- sync managers ---- */
ss_sm* ss_sm_create(ss_runtime* rt, uint32_t entity, int role);
void ss_sm_destroy(ss_sm* sm); /* only for ss_sm_create handles */
void ss_sm_set_clock(ss_sm* sm, uint32_t cf, int64_t ms);
/* LoadGraph: id 0 = allocate a local (negative) id. Returns the instance id (> 0 or < 0) or an error code
 * shifted out of the int16 range (<= -0x10000). Overrides are applied after the graph's schema defaults. */
int32_t ss_sm_create_instance(ss_sm* sm, uint64_t graph, const uint64_t* override_vars,
                              const ss_value* override_values, int count, int16_t id);
/* RunCommandFrames(cf, n); n = 0 flushes at the current time. */
void ss_sm_run(ss_sm* sm, uint32_t cf, uint32_t n);
/* SendGameMessage to the entity: returns the number of instances whose entry fired. */
int ss_sm_deliver_message(ss_sm* sm, uint64_t message, const uint64_t* keys, const ss_value* values, int count,
                          uint32_t sender, uint32_t instigator);
/* Vars by id (0x0D8… identifier); instance 0 = the entity bag. Writes notify subscribed states. */
int ss_sm_set_var(ss_sm* sm, int16_t instance, uint64_t var, ss_value value);
int ss_sm_get_var(ss_sm* sm, int16_t instance, uint64_t var, ss_value* out);
/* A Dictionary var with `count` {key, value} entries (start-up import, e.g. the owner's weapon list $1d); no
 * notification. Dictionary reads (expression ops 32–34) are not modelled yet: the entries are kept. */
int ss_sm_set_dict(ss_sm* sm, int16_t instance, uint64_t var, const ss_value* keys, const ss_value* values, int count);
/* Sync state of an instance: the graph's m_syncVars that have a value, and the active states whose node is
 * remotely synced ({m_remoteSyncNodes index, m_states index}). Return the count (<= cap copied). */
int ss_sm_sync_vars(ss_sm* sm, int16_t instance, uint64_t* vars, ss_value* values, int cap);
int ss_sm_active_remote_states(ss_sm* sm, int16_t instance, int32_t* remote_index, int32_t* state_index, int cap);
int ss_sm_active_states(ss_sm* sm, int16_t instance, int32_t* states, int cap);
uint32_t ss_sm_command_frame(const ss_sm* sm);
int64_t ss_sm_time(const ss_sm* sm);

/* ---- server hosting (T041 / T051): graph closure, start-up import, input, sync state out ----
 * The host (server/inst/sim) loads a hero's graphs, imports the start-up snapshot it sends the client
 * (instances, vars, active states with their data), then per client command frame: ss_sm_input, ss_sm_run(cf, 1),
 * and reads the sync state to encode full / delta updates. Functions marked [T041] are being implemented with
 * the node types; until then they return SS_ERR_UNIMPLEMENTED. */
enum { SS_ERR_UNIMPLEMENTED = -5 };

/* Graph GUIDs a loaded graph references (SubScript m_statescriptGraph, PlayScript m_script literals): load
 * them too, recursively, before creating instances. Returns the count (<= cap copied). Sync managers created
 * from the runtime find every loaded graph (PlayScript / SubScript). */
int ss_runtime_graph_refs(ss_runtime* rt, uint64_t graph, uint64_t* out, int cap);

/* Creates an instance as a replicated (imported) one: no schema defaults, no overrides, entries not fired;
 * `parent` / `slot` = the SubScript parent instance id and slot (0 / 0 for top-level). Returns the id or an
 * error code shifted out of the int16 range, like ss_sm_create_instance. */
int32_t ss_sm_import_instance(ss_sm* sm, uint64_t graph, int16_t id, int16_t parent, uint32_t slot);
/* Activates state `state` (m_states index) of an imported instance as the client's replication does: no
 * links followed, no plugs fired; `data` = the state's local-format state data (LSB-first bits, as in the full
 * update), nbits = its length (0 for types without data). [T041] per type. */
int ss_sm_import_state(ss_sm* sm, int16_t instance, int32_t state, const uint8_t* data, int nbits);

/* The client's input for command frame `cf` (the command's u16 button mask and u8 action code,
 * server/inst/wire InputEntry.Buttons / Extra): the sync manager turns edges against the previous frame's
 * input into LogicalButton notifications at the start of the frame for the instances the client sends them
 * to. Call once per frame, before ss_sm_run(sm, cf, 1). [T041] */
int ss_sm_input(ss_sm* sm, uint32_t cf, uint16_t buttons, uint8_t action);

/* Instances in creation order (ids; stopped ones excluded). Returns the count (<= cap copied). */
int ss_sm_instances(ss_sm* sm, int16_t* ids, int cap);
typedef struct ss_instance_info {
    uint64_t graph;
    int16_t parent;
    uint32_t slot;
    uint32_t flags;
} ss_instance_info;
int ss_sm_instance_info(ss_sm* sm, int16_t instance, ss_instance_info* out);
/* Every primitive var of a bag (instance 0 = the entity / owner bag), ordered by id, with its stored value (what
 * the sync sends; not a read through an owner state); dictionaries and child
 * bags are not listed. Returns the count (<= cap copied). */
int ss_sm_vars(ss_sm* sm, int16_t instance, uint64_t* vars, ss_value* values, int cap);
/* The writer ("user", var +0x88) of each var ss_sm_vars lists, in the same order: the writing state (instance id,
 * m_states index; 0 / 0 = none), write priority (bits 0–6), state-defined (bit 7: readers see the state's own value,
 * e.g. a running WeaponVolley's ammo per shot) and extra (bits 40+: which of the state's values). The var sync's
 * state reference carries these. Returns the count (<= cap copied). */
typedef struct ss_var_user {
    int16_t instance;
    int16_t state;
    uint8_t priority;
    uint8_t state_defined;
    uint8_t extra;
} ss_var_user;
int ss_sm_var_users(ss_sm* sm, int16_t instance, uint64_t* vars, ss_var_user* users, int cap);
/* The local-format state data of a state (active, or ended: its object keeps the fields, which the end of a
 * "has update" type carries) (bits LSB first into `out`, cap in bits); full = 1 for a
 * full update (fields flagged 0x20 included). Returns the bit count, 0 for types without data. [T041] */
int ss_sm_state_data(ss_sm* sm, int16_t instance, int32_t state, int full, uint8_t* out, int cap_bits);
/* Pending notifications a local update carries (Timer and Lifetime finish; the client re-schedules them):
 * kind 1 Timer (data = user data), 2 Lifetime finish; state = m_states index (the client's u16 at
 * notification +0x34 / +0x32); time in ms. Returns the count (<= cap copied). */
typedef struct ss_event {
    int32_t kind;
    int32_t state;
    int32_t data;
    int64_t time;
} ss_event;
int ss_sm_events(ss_sm* sm, int16_t instance, ss_event* out, int cap);
/* A state's changes so far: lifetime (begin, re-enter, end) and every change of its data the client must be
 * told about (a switch's value, a stack member's position, …), never data that only moves with time; an import
 * counts none. A delta sends a state when this moved. >= 0, or an error. */
int ss_sm_state_changes(ss_sm* sm, int16_t instance, int32_t state);

/* Graph sync layout (for the local full / delta encoders; docs/MatchLogic.md §14.3–14.4):
 * - every m_syncVars slot in order, empty (var 0) and non-eligible (mode != 0) ones included;
 * - per m_states index: STU type, kind (the local format skips client-only / server-only states; "has update"
 *   types, FUN 0xeefee0: IsA 0x7bf31629 / 0x860ee5fc WeaponVolley / 0xffbfa88d Effect, write an extra bit),
 *   m_remoteSyncNodes index (-1 none) and m_nodes index;
 * - the index width of the local events tail: max(1, bit length of (m_states count − 1)) (graph +0x17C; matches
 *   all 22 Genji graphs live [L], the LinkGraph code was not read [?]). Returns the count / width, or an error. */
typedef struct ss_sync_slot {
    uint64_t var;
    uint32_t mode;
    uint8_t flag;
} ss_sync_slot;
enum { SS_STATE_PLAIN = 0, SS_STATE_CLIENT_ONLY = 1, SS_STATE_SERVER_ONLY = 2, SS_STATE_HAS_UPDATE = 3 };
typedef struct ss_state_layout {
    uint32_t type;
    int32_t kind;
    int32_t remote_index;
    int32_t node;
} ss_state_layout;
int ss_runtime_graph_sync_slots(ss_runtime* rt, uint64_t graph, ss_sync_slot* out, int cap);
int ss_runtime_graph_states(ss_runtime* rt, uint64_t graph, ss_state_layout* out, int cap);
int ss_runtime_graph_index_bits(ss_runtime* rt, uint64_t graph);
/* Every m_remoteSyncNodes entry (graph +0x120) in order: its m_nodes index, m_states index (-1: not a state,
 * e.g. an action) and, for states, the SS_STATE_* kind as in ss_state_layout (-1 otherwise). Returns the count. */
typedef struct ss_remote_node {
    int32_t node;
    int32_t state;
    int32_t kind;
} ss_remote_node;
int ss_runtime_graph_remote_nodes(ss_runtime* rt, uint64_t graph, ss_remote_node* out, int cap);
/* The graph's sync index widths as stored in the asset: [0] m_nodesBitCount (+0x178), [1] m_statesBitCount
 * (+0x17C), [2] m_remoteSyncNodesBitCount (+0x180), [3] m_syncVarsBitCount (+0x184). */
int ss_runtime_graph_bit_counts(ss_runtime* rt, uint64_t graph, int32_t out[4]);

/* Trace (the ss_trace fixture vocabulary, docs/StatescriptRuntime.md §4.1) buffered in the sync manager:
 * enable, then drain as JSON lines {"kind","cf","ms","inst","a","b","c","idx","s"} into buf (NUL-terminated;
 * returns the bytes written, or the size needed + 1 as a negative number when buf is too small: nothing is
 * drained then). */
int ss_sm_trace_enable(ss_sm* sm, int on);
int ss_sm_trace_drain(ss_sm* sm, char* buf, int cap);
/* The explicit no-ops so far (docs §5.7), by STU type: kind 0 nodes, 1 states, 2 config vars. Returns the
 * count of types (<= cap copied). */
int ss_sm_unmodelled(ss_sm* sm, int kind, uint32_t* types, int32_t* counts, int cap);

/* ---- capabilities (T053/T054): what the runtime implements, per client type (docs/StatescriptNodes.md §1) ----
 * The server decides from these which states it simulates and corrects; it keeps no per-type lists itself. */
enum { SS_KIND_NODE = 0, SS_KIND_STATE = 1, SS_KIND_CONFIG_VAR = 2 }; /* node = entry / condition / action / terminator */
enum {
    SS_LEVEL_UNMODELLED = 0,   /* no-op: plugs and lifetime only */
    SS_LEVEL_PRESENTATION = 1, /* client presentation (effect, sound, UI): a no-op is the right server behaviour */
    SS_LEVEL_DESIGNED = 2,     /* no client class to follow (server-only logic), or partial [?] */
    SS_LEVEL_EXACT = 3,        /* the client class's behaviour, read in Ghidra */
    SS_LEVEL_VERIFIED = 4,     /* exact and checked against client recordings */
};
/* State data per format (docs/StatescriptSync.md §4): none = the format is empty for the type, writer = produced
 * from the runtime's state per the spec, echo = not produced (only an import's own bits can be written back). */
enum { SS_DATA_NONE = 0, SS_DATA_WRITER = 1, SS_DATA_ECHO = 2 };
typedef struct ss_type_cap {
    uint32_t type;
    uint8_t kind;        /* SS_KIND_* */
    uint8_t level;       /* SS_LEVEL_* */
    uint8_t data_local;  /* SS_DATA_* (states; 0 otherwise) */
    uint8_t data_remote; /* SS_DATA_* */
    uint8_t has_update;  /* ScaleTime / Effect / WeaponVolley: the end carries data */
    uint8_t host;        /* server-only (no client class): lifecycle in the library, effects by the host (host events) */
} ss_type_cap;
/* Every client type (719: nodes, states, config vars), in type-hash order. Config-var levels count the
 * runtime's evaluators, host-registered ones included. Returns the count (<= cap copied). */
int ss_runtime_type_caps(ss_runtime* rt, ss_type_cap* out, int cap);
/* One type; SS_ERR_NOT_FOUND if it is not a client Statescript type. */
int ss_runtime_type_cap(ss_runtime* rt, uint32_t type, ss_type_cap* out);
/* The STU type of every node of a graph, in m_nodes order. Returns the count (<= cap copied). */
int ss_runtime_graph_node_types(ss_runtime* rt, uint64_t graph, uint32_t* types, int cap);

/* State data in either format: local = 1 the owner's (local) format, 0 the remote format; full as in
 * ss_sm_state_data (which is local = 1). Returns the bit count (0 for an empty format), or SS_ERR_UNIMPLEMENTED
 * when the runtime cannot produce that format for the state (echo type without imported bits, or the remote
 * format of a type whose remote writer is missing): the server must not send guessed bits. */
int ss_sm_state_data2(ss_sm* sm, int16_t instance, int32_t state, int local, int full, uint8_t* out, int cap_bits);
/* The instance ids a state's data names (a stack predecessor's instance, …) in that format: the client crashes
 * on an unknown one, so hold the state back until they are known to it. Returns the count (<= cap copied). */
int ss_sm_state_data_refs(ss_sm* sm, int16_t instance, int32_t state, int local, int16_t* ids, int cap);

/* Executed actions (sync spec §2.9, the client's "action executed" records, sm vfunc +0x158): every action node
 * run on a mirrored instance (id > 0), in execution order; not the executions of an import. The sync manager
 * keeps the last 256 command frames. node = m_nodes index, remote_index = its m_remoteSyncNodes index or -1.
 * Returns the count with cf > since_cf (<= cap copied). */
typedef struct ss_action {
    uint32_t cf;
    int16_t instance;
    int32_t node;
    int32_t remote_index;
} ss_action;
int ss_sm_actions(ss_sm* sm, uint32_t since_cf, ss_action* out, int cap);

/* Server-only node types (ss_type_cap.host; docs/StatescriptNodes.md §3.9): the library runs their lifecycle
 * (actions fire their out plug; states stay active until aborted or ended by the host) and logs them; the host's
 * game systems (health, auras, stats, entities) apply the effects. kind: 0 action executed, 1 state begin,
 * 2 state finished, 3 state aborted. Only executions of runs (not imports); retention and since_cf as
 * ss_sm_actions. */
typedef struct ss_host_event {
    uint32_t cf;
    int16_t instance;
    int32_t node;  /* m_nodes index */
    int32_t state; /* m_states index, -1 for an action */
    uint32_t type; /* STU type */
    int32_t kind;
} ss_host_event;
int ss_sm_host_events(ss_sm* sm, uint32_t since_cf, ss_host_event* out, int cap);
/* A node's config-var field (by field hash, e.g. m_amount) evaluated in the instance: SS_OK, SS_ERR_NOT_FOUND (no
 * such node / field), or 1 when the config var yields no value (out untouched). */
int ss_sm_eval_field(ss_sm* sm, int16_t instance, int32_t node, uint32_t field_hash, ss_value* out);
/* The {key, value} entries an iterable field yields (m_targets …; docs §3.3 iteration). Returns the count. */
int ss_sm_iterate_field(ss_sm* sm, int16_t instance, int32_t node, uint32_t field_hash, ss_value* keys,
                        ss_value* values, int cap);
/* End an active state: finish = 1 (its finished plug) or 0 (abort): a Lifetime notification at the sync manager's
 * current time, run by the next ss_sm_run (lifetime path: state changes, has-update end data). */
int ss_sm_end_state(ss_sm* sm, int16_t instance, int32_t state, int finish);

/* ---- ECS world: sync managers as component 0x23 of systems/ecs entities ---- */
ss_world* ss_world_create(ss_runtime* rt, int role);
void ss_world_destroy(ss_world* w);
/* Spawns an entity with the given component ids (dependencies are added); returns its id, 0 on failure. */
uint32_t ss_world_spawn(ss_world* w, const uint8_t* components, int count);
/* The entity's sync manager (component 0x23), NULL if it has none. Borrowed: do not ss_sm_destroy it. */
ss_sm* ss_world_sm(ss_world* w, uint32_t entity);

/* ---- match hosting (T057, docs/InstanceServer.md §12): brain and objective graphs in a match world ----
 * A match world (the library's MatchWorld) holds the server-run entities of a match, each with a server-role sync
 * manager, plus fact-only entities (players' heroes, whose Statescript runs elsewhere). It answers every query of the
 * match / objective node types itself, from facts the host pushes in before each step; the graphs' effects are applied
 * to those facts and queued for the host (ss_match_effects). No callbacks into the host. */
typedef struct ss_match ss_match;
enum { SS_ME_SM = 1, SS_ME_PLAYER = 2, SS_ME_PVP = 4, SS_ME_GLOBAL = 8 };
/* The world's clock starts at command frame `cf`; `seed` drives IntRandom. */
ss_match* ss_match_create(ss_runtime* rt, uint32_t cf, uint32_t seed);
void ss_match_destroy(ss_match* m);
/* flags: SS_ME_SM (give it a sync manager), SS_ME_PLAYER (a player's hero), SS_ME_PVP (a PvP-game component: the
 * gamemode entity), SS_ME_GLOBAL (GetGlobalEntity). SS_ERR_EXISTS if the id is live. */
int ss_match_add(ss_match* m, uint32_t id, uint64_t def, uint32_t flags);
int ss_match_remove(ss_match* m, uint32_t id);
/* The entity's sync manager (borrowed, valid while the match lives), NULL for fact-only entities. */
ss_sm* ss_match_sm(ss_match* m, uint32_t id);
/* Facts. */
int ss_match_set_transform(ss_match* m, uint32_t id, float x, float y, float z, float yaw_deg);
int ss_match_set_team(ss_match* m, uint32_t id, int32_t team);
int ss_match_set_filter_bits(ss_match* m, uint32_t id, uint64_t extra); /* besides the team and player bits */
int ss_match_set_alive(ss_match* m, uint32_t id, int alive);
int ss_match_set_tags(ss_match* m, uint32_t id, const uint64_t* tags, int count);
/* A player's hero: its controller entity and the selected hero GUID. */
int ss_match_set_player(ss_match* m, uint32_t hero, uint32_t controller, uint64_t hero_guid);
int ss_match_set_health(ss_match* m, uint32_t id, int32_t pool, float current, float max);
int ss_match_set_throttle(ss_match* m, uint32_t id, float x, float y);
/* Capture area (component 0x1a) rules from the placement's STUECCaptureAreaInstanceData; radius of the zone [?]. */
typedef struct ss_capture_rules {
    float decay_time, f349aaca6, segments, extra_player, capture_time, f5f080cc5, decay_delay;
    int32_t f2ca7fc17;
    float fe6e7f301;
    int32_t f3a691fdf, max_players;
    float radius;
} ss_capture_rules;
int ss_match_set_capture_area(ss_match* m, uint32_t id, const ss_capture_rules* rules);
/* Ids for entities the graphs create (CreateEntity): first … first + count − 1. */
int ss_match_reserve_ids(ss_match* m, uint32_t first, uint32_t count);
/* Starts a graph on an entity with a sync manager (schema defaults, then the overrides); returns the instance id or
 * an error code shifted out of the int16 range (as ss_sm_create_instance). */
int32_t ss_match_start(ss_match* m, uint32_t id, uint64_t graph, const uint64_t* override_vars,
                       const ss_value* override_values, int count);
/* The rules layer: a game message to one entity (target) or to every entity (target 0). */
int ss_match_send(ss_match* m, uint32_t target, uint64_t message, const uint64_t* keys, const ss_value* values,
                  int count);
/* Player events on every entity's player-event states: kind 0 joined, 1 left, 2 died (other = the killer). */
int ss_match_player_event(ss_match* m, int kind, uint32_t player, uint32_t other);
/* Runs command frames up to and including `cf` on every entity (no-op if cf is not ahead of the clock). */
void ss_match_step(ss_match* m, uint32_t cf);
uint32_t ss_match_frame(const ss_match* m);
/* Effects since the last drain, oldest first; copies up to cap and drops them (returns the count copied). */
enum {
    SS_FX_TEAM, SS_FX_TAGS_ADD, SS_FX_TAGS_REMOVE, SS_FX_TELEPORT, SS_FX_CREATE, SS_FX_DESTROY, SS_FX_RESPAWN,
    SS_FX_MATCH_TIME, SS_FX_GAME_FINISHED, SS_FX_END_MATCH, SS_FX_CAPTURE_ENABLED, SS_FX_CAPTURE_OWNER,
    SS_FX_CAPTURE_PROGRESS, SS_FX_SPAWN_ENABLED, SS_FX_PAYLOAD_STATUS, SS_FX_BROADCAST
};
typedef struct ss_match_effect {
    uint32_t seq, cf;
    int32_t kind;
    uint32_t entity, other; /* subject; creator / sender */
    int32_t i0, i1;         /* team / on / attackers; contested / yaw given / defenders */
    float f[4];             /* x y z yaw (teleport, create); progress (capture); seconds (match time); moving */
    uint64_t u;             /* definition (create), message (broadcast), tag */
} ss_match_effect;
int ss_match_effects(ss_match* m, ss_match_effect* out, int cap);
typedef struct ss_capture_state {
    int32_t enabled, owner, capturing_team, contested;
    float progress;
} ss_capture_state;
int ss_match_capture(ss_match* m, uint32_t id, ss_capture_state* out);

#ifdef __cplusplus
}
#endif
#endif
