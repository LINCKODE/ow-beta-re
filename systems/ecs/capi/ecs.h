/* C API over the ECS reimplementation, for cgo (server/) and ctypes (tests/replay_fixture.py).
 * Opaque handles, POD structs, no exceptions across the boundary. Admins are built from the
 * live-client manifest (component types + that EA's systems as subscription-only stand-ins). */
#ifndef OW_ECS_CAPI_H
#define OW_ECS_CAPI_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct ecs_admin ecs_admin;
/* A loader tree: components + children (spawned before the parent is finalized). */
typedef struct ecs_loader ecs_loader;

/* Trace event: same kinds/phases as Prometheus `ecs_trace` (ecs_trace_kind_name/phase_name). */
typedef struct ecs_event {
    uint64_t seq;
    uint64_t tick;
    int32_t phase;
    int32_t phase_sys;
    int32_t kind;
    int32_t sys;
    uint32_t ent;
    uint32_t phase_cf;
    uint32_t comp_count;
    uint64_t a, b;
    uint8_t comps[64]; /* first comp_count component ids (spawn_end, destroy) */
} ecs_event;

/* name: "game" | "session" | "lobby". NULL if unknown. Initialize is done. */
ecs_admin* ecs_admin_create(const char* name);
void ecs_admin_destroy(ecs_admin* ea);

int ecs_system_count(const ecs_admin* ea);
uint32_t ecs_system_vtable_rva(const ecs_admin* ea, int index);

/* Spawn now. id 0 = allocate (id_high_bits | ++counter). Returns the id, 0 on failure. */
uint32_t ecs_spawn(ecs_admin* ea, uint32_t id, const uint8_t* comps, int count, uint32_t id_high_bits);
ecs_loader* ecs_loader_create(const uint8_t* comps, int count, uint32_t id_high_bits, uint32_t child_tag);
/* The parent takes ownership of the child. */
void ecs_loader_add_child(ecs_loader* parent, ecs_loader* child);
void ecs_loader_destroy(ecs_loader* loader);
/* Spawn from a loader tree (not consumed). Returns the root id, 0 on failure. */
uint32_t ecs_spawn_loader(ecs_admin* ea, uint32_t id, const ecs_loader* loader);
/* Lightweight entity (components inline, no callbacks). Returns the id. */
uint32_t ecs_spawn_inline(ecs_admin* ea, const uint8_t* comps, int count);

/* What every component of the entity answers to CanDelete (manifest stand-ins only). */
int ecs_set_can_delete(ecs_admin* ea, uint32_t id, int allow);
/* CanDelete answer for entity `id`, applied right before the next scheduled action runs. */
void ecs_schedule_can_delete(ecs_admin* ea, uint32_t id, int allow);
/* When entity `ent` is next asked CanDelete (its last component, asked first), it requests the
 * deletion of `target` (client components delete entities they own). Applied before the next
 * scheduled action. */
void ecs_schedule_can_delete_side_effect(ecs_admin* ea, uint32_t ent, uint32_t target, int immediate);
/* Parent link (parent 0 = detach), now or right before the next scheduled action. */
int ecs_set_parent(ecs_admin* ea, uint32_t child, uint32_t parent);
void ecs_schedule_set_parent(ecs_admin* ea, uint32_t child, uint32_t parent);
/* Set the id counter right before the next scheduled action runs (replaying racy allocations). */
void ecs_schedule_id_counter(ecs_admin* ea, uint32_t value);
/* Following schedule/spawn calls run as external (other-thread) work until set back to 0. */
void ecs_set_external(ecs_admin* ea, int external);

uint32_t ecs_id_counter(const ecs_admin* ea);
void ecs_set_id_counter(ecs_admin* ea, uint32_t value);

/* Returns 0 if no such entity. */
int ecs_request_delete(ecs_admin* ea, uint32_t id, int immediate);
/* Push to the delete-request queue (processed at the next flush_destroy). 0 if no such entity. */
int ecs_queue_delete(ecs_admin* ea, uint32_t id);
void ecs_tick(ecs_admin* ea, float dt);
void ecs_flush_created(ecs_admin* ea);
void ecs_flush_destroy(ecs_admin* ea);

/* Queue work for system `sys` to do inside its next OnTick, in queue order. */
void ecs_schedule_spawn(ecs_admin* ea, int sys, uint32_t id, const uint8_t* comps, int count, uint32_t id_high_bits);
void ecs_schedule_delete(ecs_admin* ea, int sys, uint32_t id, int immediate);
void ecs_schedule_flush_created(ecs_admin* ea, int sys);
/* Route the next scheduled action to a command-frame listener instead of a system tick:
 * phase 0 = inside OnCommandFrame(cf) of listener `listener` (cf must match), 1 = its
 * OnPreCommandFrames, 2 = its OnPostCommandFrames (cf ignored). */
void ecs_schedule_target_listener(ecs_admin* ea, int phase, int listener, uint32_t cf);
/* Clock: seed CF + accumulator (start state), resync (client FUN_7ff650ce64a0, now or scheduled
 * into system `sys`'s next tick), dilation flags; read the current CF. */
void ecs_clock_seed(ecs_admin* ea, uint32_t cf, int64_t accumulator_us);
void ecs_clock_resync(ecs_admin* ea, uint32_t cf);
void ecs_schedule_clock_resync(ecs_admin* ea, int sys, uint32_t cf);
void ecs_clock_set_dilation(ecs_admin* ea, uint8_t flags);
uint32_t ecs_clock_command_frame(const ecs_admin* ea);
int ecs_command_frame_listener_count(const ecs_admin* ea);
/* Takes ownership of the loader. */
void ecs_schedule_spawn_loader(ecs_admin* ea, int sys, uint32_t id, ecs_loader* loader);
void ecs_schedule_spawn_inline(ecs_admin* ea, int sys, const uint8_t* comps, int count);

uint64_t ecs_tick_count(const ecs_admin* ea);
int ecs_entity_count(const ecs_admin* ea);
/* Writes up to cap component ids of the entity (sorted); returns the count, -1 if not found. */
int ecs_entity_components(const ecs_admin* ea, uint32_t id, uint8_t* out, int cap);
uint32_t ecs_entity_flags(const ecs_admin* ea, uint32_t id);

/* Entity ids of the type's iteration list (EA+0xB0 order); returns the count. */
int ecs_iteration(const ecs_admin* ea, uint8_t comp, uint32_t* out, int cap);
/* Mark n slots of the type's allocator as used (pre-existing entities of a replayed trace). */
void ecs_reserve_slots(ecs_admin* ea, uint8_t comp, int n);

/* Moves up to cap recorded events into out; returns how many. */
int ecs_trace_take(ecs_admin* ea, ecs_event* out, int cap);
const char* ecs_trace_kind_name(int32_t kind);
const char* ecs_trace_phase_name(int32_t phase);

#ifdef __cplusplus
}
#endif
#endif
