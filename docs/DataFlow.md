# DataFlow: how gameplay values reach presentation

The client's **DataFlow** system is a typed, per-entity value channel from the simulation (Statescript, components,
effects, the scene) into presentation (effects, animation blend trees, model draw constants, sound RTPCs / switches,
camera shake). A value is named by a **DataFlow asset** (resource type 0x013); an entity's **component 0x03** stores
and resolves the values; **producers** publish them and **consumers** read them by id. Nothing of it is replicated.

Task T068 (2026-09-29). Addresses are RVAs like the other docs (Ghidra address = `0x7ff64ffe0000 + RVA`, so
`0xa08410` is `FUN_7ff6509e8410`); `DAT_…` names are Ghidra's. Tags: **[D]** measured in the game data (install,
`systems/content` + `systems/stu`), **[G]** read in Ghidra, **[L]** live, **[?]** inferred. Everything here is static ([D]/[G]); no live check was run yet (§8).

**Contents**: 1 model · 2 assets · 3 component 0x03 · 4 producers · 5 consumers · 6 networking and the server ·
7 worked examples · 8 open questions · 9 tools and data.

## 1. The model in one page

```
 producers (register on an entity's comp 0x03)            consumers (read by id, from an entity)
 ─────────────────────────────────────────────            ──────────────────────────────────────
 Statescript DataFlowMapping state  ─┐                ┌─ effect params, kind 6          (per effect update)
 (global-value state → comp 0x52)    │   comp 0x03     ├─ model draw constants            (per render submission)
 components 0x10/0x13/0x1c/0x28/…   ├─▶ value store ─▶├─ anim blend-tree parameter banks (per anim update)
 effect "data-flow output" elements  │   id → value16  ├─ sound RTPC / switch / state      (per sound update)
 explicit setters (camera, anim, …)  │   + status      ├─ first-person camera kick/shake  (per frame)
 scene + Wwise globals              ─┘   (inherits    └─ (Statescript never reads, §4.2.2)
                                          from parent scene node)
 nothing publishes → the asset's default (STUDataFlowType m_default)
```

- **Id**: a DataFlow is addressed by the low 32 bits of its GUID (`0x04800000000000FD` → id `0xFD`) [G: `0x89e8c0`
  looks the id up in a hash map and lazily loads the 0x013 resource; D: the id lists in effects hold these values].
- **Pull, with subscription**: the first read of an id resolves it lazily (producers in order, else default) and
  caches it; the next scene update turns the id into a **subscription** that is re-polled once per **render frame**,
  parents before children, while any producer answers "dynamic" [G, §3.4]. No interpolation in the store; animation
  does its own blending (§5.3).
- **Hierarchy**: an entity's comp 0x03 answers every id it has no producer for from its **parent scene node**'s
  comp 0x03 (built-in provider, §3.5). An effect or child model therefore sees its owner's values.
- **Presentation only**: comp 0x03 is not mirrored; no DataFlow state is on the wire; all producers found run on the
  client from state the client already has (Statescript vars, components, the scene). The server's job is to make the
  producers' inputs right (§6).

## 2. DataFlow assets (resource type 0x013)

### 2.1 Format [D, G]

361 assets, GUIDs `0x0480000000000001 … 0x0480000000000585`, each 2 STU instances (`systems/stu` registry offsets):

```
STUDataFlow (0x77d4d730, 24 B)
  +0x08 m_type        → STUDataFlowType_* (inline instance)
  +0x10 0x10756330    enum   = m_animBlendRule   (Prometheus name; 0 keep, 1 lerp, 2 snap, §5.3)   0 ×359, 1 ×2
  +0x14 0x4e66c5e2    u8     = m_onFailureKeepLastValue (Prometheus name; behaviour §3.3)            1 ×39
  +0x15 0xb6956c0d    u8     = m_visibleOnServer (Prometheus name; no reader found, §6.2)            1 ×78
STUDataFlowType (0xdf0668b9) subclasses (type code from 0xee82d0 [G]):
  1 Float    (0x74a4e1e9, 32 B): +8 m_default, +0xc m_min, +0x10 m_max, +0x14 0x0ab37abf f32 (1; 0.01 ×2, 0.1 ×1),
                                 +0x18 0xada61fdb u32 (0; 2 ×2), +0x1c 0xdce7ac5b u8 (always 1)          302 assets
  2 Vec2     (0x1b462083): +8 m_default                                                                   8
  3 Vec3     (0x6c411015): +8 m_default                                                                   8
  4 Vec4     (0xf22585b6): +8 m_default                                                                   1
  5 GUID     (0x9722af42): +8 m_default (u64; always 0)                                                  42
  6 EntityID (0x2a1d1e36): no default (reads give the null entity id, DAT_7ff65183a0c8)                   0
```

- Float defaults: 0 ×253, 1 ×37, 100 ×3, 0.5 ×2, 2/3/4/−48 ×1; ranges [0, 1] ×299 [D].
- **m_min / m_max are not applied** on any read or write path checked (getters, `0x8a0820`, resolve `0xa07de0` /
  `0xa09350`, push `0xa08770`, setter `0xa36210`, the effect evaluator, the anim banks) [G]. Their reader and the
  three unnamed Float fields are open (§8) [?: editor ranges, or quantisation for `m_visibleOnServer` flows].
- No names: the 0.8 data has no DataFlow names; the catalogue `0x046000000000002D` (type 0x063) only lists all 361
  GUIDs [D].

### 2.2 Who references DataFlows [D]

| referrer | how | DataFlows |
|---|---|---|
| Statescript graphs (0x01B) | `STUConfigVarDataFlow` (0x824213f0, +0x10 GUID) in **DataFlowMapping** states (275 nodes in 102 graphs) and **global-value** states `e19fda31` (10, graphs `0x1CB`, `0xB68`) | 114 |
| anim blend trees (0x020) / sets (0x021) | `STUDataFlow` refs in anim nodes (track overrides, weight drivers, leg IK, category data flow, …) | 150 / 54 |
| sound parameter bindings (0x04C, `0x44c43eac` {m_parameter → STUSoundParameter, m_dataFlow}) | Wwise RTPC | 45 |
| sound state / switch conditions (0x05B `0x770008c3`, 0x04E `0x473f605c`: `m_condition[] {m_dataFlow, m_value}`) | set a Wwise state / switch | 19 / 6 |
| effects (0x00D, 0x08F) | **32-bit id** in kind-6 parameters (§5.1; list at header +0x128) | 22 |
| effects (0x00D, 0x08F) | **32-bit id** list at header +0x120 read into model draw constants (§5.2) | 4 (`0x1E`, `0xC9`, `0x34D`, `0x14E`) |
| materials (0x008), models (0x00C), UX (0x05A, 0x05E) | 64-bit GUID inside the binary / STU (meaning not traced) | 5, 3, 2 / 7 |
| client code | hard-coded ids (§3.5, §4.1, §5.4, §5.5) | ~74 ids |
| nothing found | (read or written by id from code paths not scanned, or unused in 0.8) | 89 |

`m_visibleOnServer` is set on 78 assets: 76 of them are used only by anim blend trees / sets (or nothing), 1 by a
graph [D]. That pattern fits "the server evaluates these for its own animation (hitbox poses)" [?].

## 3. Component 0x03 (DataFlow)

A per-entity value store, 0x650 bytes, **dependency of most presentation components** (0x04, 0x0b, 0x0d, 0x1d, 0x23,
0x27, …; ECS_Components.md). Created by `0xa09910` → ctor `0xa07880`; OnCreate `0xa07d20` links it with the entity's
scene transform (comp 0x01 +0x1a0 = comp 0x03, comp 0x03 +0x18 = comp 0x01) [G].

### 3.1 Layout [G]

| off | field |
|---|---|
| +0x000 | vtable `0x158e520` ([0] dtor `0xa07b20`, [2] OnCreate `0xa07d20`, [3] teardown `0xa08600`) |
| +0x008 | Entity* (passed to producers) |
| +0x018 | comp 0x01 (scene node) |
| +0x020 / +0x028 | **producer list**, priority order: the global providers (copied at construction), then component producers in registration order, the built-in provider (+0x50) always last |
| +0x038 / +0x040 | deferred producer ops `{vtable 0x1588698, obj}`: register / unregister a producer on another entity by id (vt[3] `0xa12960`) |
| +0x050 | **built-in provider** (vtable `0x158e570`; +0x60 = parent scene node) (§3.5) |
| +0x070 | write buffer: explicit sets (status 2) |
| +0x170 | resolve cache: lazy reads of this frame, waiting for promotion |
| +0x270 | keep-last cache (status 4) |
| +0x370 / +0x378 | `teList<ValueStack*>` (2 inline) |
| +0x3a0 … +0x637 | IdMap: id → value stack (16 buckets × 8, overflow chunks of 0x298) |
| +0x638 | frame stamp of the last update / poll (anim banks compare it, §5.3) |
| +0x63c | frame stamp of the last producer poll |
| +0x640 | "producers changed": the next update rebuilds |
| +0x641 | "queued" |

Caches (0x100 each; lookup `0xa096c0`, append `0xa097a0`): a spin lock and four parallel arrays (16-byte values,
u32 ids, u8 status, producer*), searched linearly. **ValueStack** (0xc0): 8 slots of {16-byte value, u32 id, u8
status}, +0xa8 owning producer, +0xb0 count of the bottom (dynamic, polled) region, +0xb1 start of the top (static)
region, +0xb2 failed mask, +0xb3 needs-compaction. One slot per id: there is no priority stack of overrides per id;
"bottom/top" only separates polled from static values [G].

### 3.2 Values and status

A value is 16 bytes (float, vec2/3/4, u64 GUID or entity id), plus a status [G]:

| status | meaning |
|---|---|
| 0 | valid, static (never re-polled) |
| 1 | valid, dynamic (re-polled every frame by its producer) |
| 2 | explicitly set (write buffer) |
| 3 | temporarily unavailable: not cached, retried; a polled slot turning 3 is dropped |
| 4 | fallback: keep-last value or the type default |
| 5 | (producer) this id is not mine |
| 6 | (internal) nobody answered |

Consumers test `< 3` ("has a value"); most ignore the status and use the value, which then is the default.

### 3.3 Reading an id [G]

`0x8a0820(comp03, id, out)`: IdMap → the slot in a value stack. If the id is not subscribed yet, `0xa07de0`:
1. write buffer (+0x70) → status 2;
2. this frame's resolve cache (+0x170);
3. **producers** (`0xa09350`): walk the list in order, call `vt[1] GetValue(entity, id, out)`, keep the lowest status,
   stop at the first `< 3`; ties go to the earlier producer;
4. keep-last cache (+0x270), if the asset has `m_onFailureKeepLastValue`;
5. the asset's default (`0x89eee0`, by `m_type`).

The result goes into the resolve cache and the entity is queued; the next update (§3.4) moves the id into a value
stack (`0xa08f40` → `0xa08770`), dynamic (status 1) or static.

**Typed getters** `0x89f110` / `0x89f4a0` (float) / `0x89f6b0` (vec2) / `0x89f8d0` (vec3) / `0x89fae0` (vec4)
`(_, entity, id, out)` go through the entity's comp 0x03 when it has one (entity mask bit 3), else ask the global
providers directly and fall back to the default with status 4. An id without a 0x013 asset logs "failed to get
default for 0x%016llx".

**Keep-last** (`m_onFailureKeepLastValue`, 39 assets): when a producer goes away (its stacks are popped, `0xa08ad0`) or
a poll fails (`0xa089a0`), the last value is kept (in +0x270, status 4) instead of resetting to the default.

### 3.4 When values are evaluated [G]

```
0x7d0260  frame begin: DAT_7ff6517fd8c8++ (global engine frame counter)
EA tick → MapWorldSystem OnTick 0xc43980 (also 0xc35950, 0xc84940, 0xce6520/0xce6ef0, 0xaaa720)
  → 0x9f6770 Scene::Update(world, dt)        (inline 0x9f6150 or job 0x9f60a0; scene time +0x3c0 += dt)
     0x9fb5a0  drain the queued entities → 0xa090b0 per comp 0x03:
               rebuild if producers changed (0xa08ad0), promote the resolve cache into value stacks,
               move explicit sets into stacks, compact (0xa07000)
     0x9f4fe0  dirty scene nodes by depth 0..15, parents first (worker jobs for ≥ 16 nodes):
               if the node has dynamic values and was not polled this frame → 0xa089a0 (poll every stack's
               owner: vt[2] UpdateValueStack, base 0x89ff30 = GetValue per dynamic slot), then the transform
     0x9f61e0 → 0x9f55e0 → 0x9f5450/0x9f5360  (same poll; re-queue the node while it has dynamic values)
```

- **Per render frame**, variable `dt`, at most one poll per component per frame; **not per command frame**. No
  interpolation, no history: presentation values are not rolled back or predicted as such (§6.3).
- **Parents first** matters because children inherit (§3.5).
- A consumer's first read of a new id is synchronous (lazy resolve); after that it reads the polled slot.

### 3.5 The built-in provider and inheritance [G]

Every comp 0x03 ends its producer list with its own provider (`0xa074f0`):
- id **`0xF3`**: from the scene node's render flags (comp 0x01 +0x180: `0x800400` → 0.0, `0x1000800` → 1.0, else 2.0)
  [?: a first-/third-person or visibility mode];
- ids `0x183`, `0x1BF`, `0x10C`, `0x10D`, `0x285`, `0x286`: "not mine" (never inherited);
- **every other id: the nearest ancestor scene node's comp 0x03** (`0xa074a0`; parent set by SetParent `0xa01430` →
  `0xa08e00`). Removing the built-in provider (`0xa083a0`, used by comp 0x20 and `0xcad5e0`) cuts inheritance.

### 3.6 Lifetime [G]

- Producers register with `0xa08410(comp03, producer)` (deduplicated, inserted before the built-in provider; sets
  +0x640 and queues the entity) and unregister with `0xa08580` (pops their stacks). comp 0x03's teardown does not
  notify producers; their owners unregister them.
- Producer objects: base vtable `0x15886b8` ([1] GetValue, [2] UpdateValueStack `0x89ff30`), ref-counted
  (+0x10). A pooled "value list" producer (vtable `0x1588638`, 0xc0 bytes, `0x8a02b0`: list of {value16, id}, answers
  0 or 1 by a flag, 5 if absent) and a forwarding provider (vtable `0x1588678`, `0x8a0ab0`: forwards every id to
  another entity's comp 0x03, `0x8a0990`) are the generic building blocks.
- **Data Flow Manager** (engine manager 31, EngineManagers.md): state `DAT_7ff65180b748` (0 off, 1 init, 2 running,
  3 shutdown), pools of producers (`DAT_7ff651784ac0` 0xc0-byte, `DAT_7ff651784b50` 0x20-byte), the id → asset map
  (`0x89f060`, lookup `0x89e8c0`), the global provider list `0x17a68d0` (add `0x89e850`); `0x89ec50` waits for
  pooled producers at shutdown ("Data Flow Manager is waiting for %d pooled data flow producers to be deleted.").
- **Global providers** (asked for every entity, copied into each comp 0x03 at construction): the **scene provider**
  (`0x17aa9c8`, added inline by scene init `0x9d6e50`): `0x181` = scene time, `0x182` = comp 0x01 +0x130
  (16 bytes); the **Wwise provider** (`0x17ab3b0`, added by `0xa52560` through `0x89e850`): `0xBC`, `0x2E9` (§4.1).

## 4. Producers

### 4.1 Components and systems [G]

Every component that publishes registers a producer on its own entity's comp 0x03 in its OnCreate (vtable slot 2).
On one entity the **first registered producer that answers wins** (§3.3); the built-in provider is always last.
Status 0/1 = value, 3 = known but unavailable now, 5 = not mine.

| registered by | component | provider (vtable) · GetValue | ids | value |
|---|---|---|---|---|
| `0xcc7260` | 0x28 Health | `0x15c5a80` · `0xcc3ea0` | `0x79`–`0x80`, `0x151` | health pools (3 pools: current, current/max clamped, totals; which pool is health / armour / shield not known) |
| `0xccfc00` | 0x13 PlayerIdentity | `0x15c6048` · `0xccfa70` | `0x6B`, `0x1E` (vec, RGBA), `0xF5` | `0x6B` = 1 if hostile to the local pawn [?]; `0x1E` = red (1,0,0,1) / blue (0,0,1,1); `0xF5` from EA+0x208 |
| `0xcd09a0` | 0x10 FilterBits | `0x15c6128` · `0xcd0590` | `0x6B`, `0x1E`, `0xF5` | same, with a same-team shortcut for projectiles owned by the local pawn |
| `0xcf24a0` | 0x33 LocalHitEffects | `0x15c6d30` · `0xcf2210` | `0x34`, `0x3A`, `0x3B`, `0x3C`, `0x45`, `0x46`, `0x121` | hit-reaction fields (`0x3B` = time since the last hit): the inputs of the camera kick (§5.5) |
| `0xca4600` (AnimationSystem) | 0x1C MirroredAnim | comp+0xff0 (`0x15c5eb8`) · `0xccd210` | `0xE6`, `0x2` (GUID), `0x3`, `0x2D`, `0x195`–`0x1A0`, `0x1BE`, `0x1BF`, `0x1C1`, `0x1F9`, `0x1FB`, `0x1FD` | anim layer state (slot GUIDs, weights); others asked of the anim object |
| `0xca4740` (AnimationSystem) | 0x1D LocalAnim | comp+0x28 (`0x15c6fc8`) · `0xcfa570` | all | the anim object |
| `0xccc760` | 0x1F PawnOwnerLink | `0x15c5dc0` · `0xccc360` | all | **forwards** to the owner's comp 0x03 (a hero sees its player's values) |
| `0xcf1fa0` | 0x3C LocalPawnEventRelay | `0x15c6cc0` · `0xcf1db0` | all | the lobby **GlobalVariableStore** (comp 0x52, §4.2.3), else the controlled pawn's comp 0x03 |
| `0xce6ef0` | lobby world entity | `0x15c69e8` · `0xce6a50` | all | comp 0x52 only |
| `0xcca010` | 0x23 Statescript | comp+0x14c0 (`0x15c5d50`) · `0xd71030` | dynamic | **DataFlowMapping answers** (§4.2) |
| `0xd35c20` | 0x27 ProjectileVisual | comp+0x9e8 (`0x15c5b68`) · `0xcc9110` | dynamic | the shot record's data-flow table (weapon projectile data flow, §4.2.4), else forwards to the projectile's owner |
| `0x8a0e40` | effect element | pooled (`0x1588638`) · `0x8a0160` | dynamic | effect data-flow outputs (§4.3) |

**Forwarders** (vtable `0x1588678`: "this entity's values = entity X's") are how spawned things see their
owner [G]: children spawned by effects (`0xa2b460` → the effect's owner), spawned children (`0xa29920` → parent),
ChildEntitySpawnSystem children (`0xcb4c20`), EffectPhysics bodies (`0xcbb040`), projectile visuals (`0xd3eee0` →
the projectile owner), BlinkEffect entities (`0xd683a0` → the script owner), the first-person model (`0xcad5e0`: drops
its scene-parent provider, forwards to the viewed entity), plus generic helpers (`0xd41cd0`, `0xd7d8a0`). Statescript
also registers its own provider on the entities it creates (`0xd563d0`, e.g. cosmetic entities [?]), so mapping answers
reach them (filtered by `m_entity`, §4.2.1). comp 0x20 (`0xccc090`) only cuts scene-parent inheritance.

**Direct sets** (not producers; write buffer, status 2) [G]: `0xa36210` (float), `0xa12ae0` (vec2), `0xcbe140`
(vec3), batch `0xa07f60` (animation outputs, `0x8920e0`): `0x31`, `0x32`, `0xF1` (first-person camera, `0xd71c70`),
`0xFF`, `0x100`, `0x585`, `0x171`, `0x172` (new entities, `0xc40c70`), `0xF2` = 1 (EffectPhysics collision `0xcb93f0`),
`0x8B` (vec3) / `0x8D` (`0xcb9090`, `0xa34e70`, `0xa417c0`, `0xcced70`), `0x582` (`0xcba6d0`), `0x4DD` (`0xa11db0`),
`0x35` (`0xd46060`).

**Global providers**: the scene provider (`0x181` scene time, `0x182`, §3.6) and the Wwise provider (`0xa52560`,
vtable `0x158f838`, GetValue `0xa5da10`): ids **`0xBC`** and **`0x2E9`**, refreshed from Wwise RTPC values
(`0xa5db70`; `0x2E9` normalised from dB, default −48).

### 4.2 Statescript

#### 4.2.1 DataFlowMapping (`85c28a9a`, 275 nodes in 102 graphs) [G, D]

```
STUStatescriptStateDataFlowMapping (State, 0x118)
  +0x100 m_dataFlow  STUConfigVarDataFlow → the DataFlow (its id)
  +0x108 m_answer    any config var: the value (Dynamic 130, Expression 98, Int 25, AnimCategory 7, Bool 7, …)
  +0x110 m_entity    STUStatescriptDataFlowEntity: which entities the answer applies to
```

- **Hooks** (class vtable `0x15dd9c8`): activate `0xd85540` and var-changed `0xd85620` → `0xd858c0` (resolve
  `m_dataFlow`; if the id changed, unregister the old and register the new answer with the Statescript component's
  registry `0xccb370`); deactivate `0xd85550` → unregister `0xccb4d0`. One id per mapping. The mapping is a
  **state**: it publishes exactly while it is active.
- **Registry** (comp 0x23 +0x14c0, vtable `0x15c5d50`): entries `{u32 id, answers[]}` (0x90 bytes); an answer
  slot caches `{answer*, value16, kind}`; kind from the asset's type (1 float, 2 vector, −1 GUID, −2 EntityID).
- **Evaluation**: `0xd71110` runs **once per frame after the Statescript tick** (StatescriptUpdateSystem job
  `0xd087d0` → `0xd08960`) and evaluates every answer into its cache, in the **mapping's own instance** (vars of the
  graph that owns the mapping, not of the reading entity). Reads (`0xd71030`) return the cache: no evaluation at read
  time.
- **Several mappings on one id stack**: the read walks the answers from the last registered to the first and returns
  the first that applies to the reading entity: **the most recently activated mapping wins**. No priority field.
- **`m_entity`** (three subclasses, names unknown): `0x8070a3c9` (207 uses) = **any entity** that reads through this
  Statescript component (its own entity and the entities it forwards to); `0x39f790a9` (54) = **the owner** (or the
  owner's first-person model); `0x4d8a2f7c` (14, `m_handle`) = one **script-created entity** by handle
  (comp 0x23 +0x1460 records).
- Answer types by flow (most used): `0x6C` ×23, `0x37` ×18, `0x48` ×16, `0xF7` ×14, `0x4A` ×10, `0xF8` ×10, `0xBF`,
  `0xC0` ×9, `0xEE` ×8, `0x10C` ×7, `0x14E` ×5; 103 distinct flows [D].

#### 4.2.2 STUConfigVarDataFlow (`824213f0`) [G]

Its evaluator (`0xf69420`) returns the DataFlow's **GUID**; it never reads a data-flow value. It only names a flow
for DataFlowMapping, the global-value state and the weapon projectile data flow. **No config var reads data-flow
values**: Statescript writes into DataFlow, it doesn't read from it.

#### 4.2.3 Global data-flow values (`e19fda31`: `m_values: STUGlobalDataFlowValue[] {m_answer, m_dataFlow}`) [G, D]

A Statescript **state** (10 uses: graph `0x1CB`, and graph `0xB68` on the lobby entity: "global data-flow values by
front-end context `$1a6e`", LobbyScripts.md). While active it writes each answer into the lobby
**GlobalVariableStore** (comp 0x52) keyed by the flow id (activate `0xd031f0`, per update `0xd03510` / `0xd036a0` /
var-changed `0xd034d0` via `0xd03890` by type; deactivate `0xd033e0` removes them). Those values are **not** a comp 0x03
producer; they are read through `0xcea950` by the comp 0x3C relay provider (before the pawn) and the lobby world's
provider. `0xcea950` also answers some ids itself: `0x15A` (local pawn check), `0xF4` (1 when `0xbfce10()` == 8),
`0x153` (voice level), `0x584`.

#### 4.2.4 Weapon projectile data flow (`c6dc0aa1` in `STUStatescriptWeaponProjectileVisual` `m_fixedDataFlow`) [G]

The GDC example (`ProjectileVisual → FixedDataFlow[0] → Answer = VisualScalar`, Statescript.md): the shot record
(EA comp 0x6f registry, `0x1033890`) carries a typed table {id, value, type} at +0x60; the comp 0x27 provider
(`0xcc9110`) answers from it (status 0), and the projectile visual entity gets the same table pushed as direct sets
(`0xd3eee0`) plus a forwarder to the projectile's owner. Where the table is filled from `m_fixedDataFlow` (WeaponVolley
launch) was not found (§8).

### 4.3 Effects publish too [G]

An effect element type (registered by `0x89ea60`, tag `0x10100a` at `0x18313a0`) is a **data-flow output**:
init `0x8a0e10` creates a producer (vtable `0x1588698`), start `0x8a0e40` registers it on the effect's entity
(and, if the element's flag +0xcc bit 0 is set, deferred on the attach parent via `0x8a17c0`), update `0x8a13b0`
evaluates the element's parameters (desc +0xb0 ids, +0xb8 param indices, +0xc0 types 1–4, +0xc8 count) and writes
{value, id} into the producer each effect tick, stop `0x8a1060` unregisters. So effects can drive other effects,
animation and sound through data flows. Which effect files contain such elements was not enumerated (§8).

## 5. Consumers

### 5.1 Effects (parameter kind 6) [G, D]

Effect resources (0x00D, 0x08F; 0x08E never) hold a parameter table: header +0xe0 = table offset, +0x140 = u16 count;
entries of 0x14 bytes `{+0 u32 data offset (relative to the entry), +4 u16 index, +6 i16 input parameter (−1 none),
+8 u8 kind, +9 u8 sub-kind, +0xa u16 flags, +0xc/+0x10 f32 range}` [G `0xa0d010`, D: index = position in all 10,188
effects].

- **Kind 6 = DataFlow**: data `{u32 id}`; sub-kind 1 float (splat to xyzw) / 2 vec2 / 3 vec3 / 4 vec4 → the typed
  getters; the entity is the owner of the effect's scene node (`*(*(inst+0xc0)+8)`); the status is ignored (missing
  → default). Other parameters take it as input (kind 1 linear map, kind 2 curve, …).
- **Kind 0x15 = DataFlow GUID compare** (`0xa0f100`): 1 if the GUID-typed flow equals a constant (one use: effect
  `0x155A` compares flow `0x4DD` with `0x0D800000000017CC`).
- Other kinds (for reference, names [?]): 0 constant, 1 linear map, 2 curve, 3 random, 4 noise, 5 swizzle, 7 lookup,
  8 math, 9 per-instance slot, 10 built-in inputs (age, transforms), 11 bone transform, **12 input by hash** (the
  inputs a Statescript Effect state passes, e.g. `0x6b8e8f77`, `0x137abd2b`), 14 array pick, 15 threshold,
  16 node matrix, 18 timer, 19/20 ?, 21 GUID compare.
- **Evaluated on every element update** (`0xa0d010` is called by ~25 element update callbacks) [G]; per effect tick
  [?]. The header +0x128 list (u16 count +0x156) repeats the kind-6 ids; `0x9ff360` reads them once at creation
  (warming the subscriptions) [G, D].
- Data [D]: 513 kind-6 parameters in 488 effects (249 of type 0x00D, 264 of 0x08F), **22 ids**: `0x150` ×214,
  `0x6B` ×204 (keep-last), `0x8B` ×33 (vec3), `0xF2` ×19, `0x3D9` ×9, `0x125` ×7, `0xF6` ×6, `0x14F` ×3, `0xC9` ×3,
  `0x52D`, `0x14E` ×2, `0x582`, `0x4F1`, `0xE9`, `0xF4`, `0x222`, `0x223`, `0x286`, `0x4`, `0x34D`, `0x339`, `0xB` ×1.

### 5.2 Model draw constants [G, D]

`0x9fdeb0` (render-instance fill, vtable `0x158e420`) copies the values of the id list at the render object's
resource +0x120 (u16 count +0x154) into the draw's constant block, 16 bytes per id (default when the status is > 4):
a shader input. In 0.8 only 13 effect resources have such a list (ids `0x1E`, `0xC9` (×18 in `0x585`), `0x34D`,
`0x14E`) [D]. `0xa3d6c0` (a bone-driven scene element) scales its strength by `clamp01(flow)` [G].

### 5.3 Animation [G]

- The anim STU's data-flow parameters are collected by `0x89d340` and tagged `id | 0x80000000` (the high bit marks a
  data-flow parameter, it is not part of the id).
- Parameter banks (float `0x88dd30`, GUID `0x88e2f0`, a third `0x88e710`) start at `m_default` and are refreshed by
  `0x88d9b0` only when comp 0x03's frame stamp (+0x638) changed: `prev = cur; cur = read(id)`; status `< 3` sets the
  valid bit, else `cur = default`.
- **`m_animBlendRule`** (`0x88c0c0`, `0x88b240`) decides how a flow's value follows blend transitions: 2 = snap,
  1 = lerp by the transition weight (`w·in + (1−w)·cur`), 0 = keep.

### 5.4 Sound [G]

`0xa50620`, per sound instance update (`0xa5c7b0` with dt): generic RTPC bindings (`STUSoundParameter` + Float
DataFlow, optionally from a global or another entity), Wwise **state / switch conditions** (`v > 0`, `v ≤ 0`, `v == c`,
`v > c`, `v ≤ c`; all true → set), plus two hard-coded ids: **`0xF5`** (default 2) → RTPC `0x0740000000000134`, and
**`0x172`** (default 1) → an AkAudioLib scaling call.

### 5.5 First-person camera [G]

`0xdcbed0` (vtable `0x15f2980` slot 4, per frame from `0xd71920`): camera kick / shake from ids `0x35` (vec2),
`0x48`, `0x49`, `0xF7`, `0xF8` (keep-last), `0x3B` (default 100: shake timer), `0x45` (amplitude), `0x34`, `0x3A`
(angles). Its sibling `0xd71c70` reads `0x33` and **sets** `0x31`, `0x32`, `0xF1` (`0xa36210`).

### 5.6 When nothing publishes [G]

The consumer gets `m_type->m_default` (EntityID: the null id), with status 3/4, unless keep-last holds an older value.
No clamping to `m_min`/`m_max`.

## 6. Networking and the server

### 6.1 Not replicated [G, D]

- comp 0x03 has no mirror data and no sync path; no JAM message carries DataFlow ids; DataFlowMapping and the global
  value state are presentation-side Statescript states (StatescriptNodes.md: "presentation").
- Every producer found computes from client-side state: Statescript config vars (vars are replicated by the
  Statescript sync), component state (movement, filter bits, …), the scene, effects.

### 6.2 What the server must do

1. **Nothing DataFlow-specific.** Don't send data-flow values (there is no channel).
2. **Make the producer inputs right**: the Statescript vars the answers read (e.g. `$009a`, `$01b3`), the remote state
   activation of DataFlowMapping states that are remote nodes (payload R11–R13), the components producers read
   (movement state, …). If a mapping's answer reads a var the server doesn't sync, the presentation is wrong, not the
   simulation.
3. **Presentation values are client-side and per frame**: no command-frame alignment, no rollback. When the client
   rolls back Statescript (StatescriptSync.md), mapping states that re-enter re-register; the store just follows.
4. `m_visibleOnServer` (78 flows, mostly animation) suggests the real server evaluated some flows itself (for its own
   animation: hitboxes) [?]; our server does not animate, so nothing follows from it today.

### 6.3 Prediction

No DataFlow state is predicted or rolled back. Values follow their producers each render frame; a mispredicted
Statescript state shows its presentation briefly (as any presentation does) and is corrected when the state is.

## 7. Worked examples

### 7.1 Payload wheels / cart motion: `$01b3` → `0x14F` → effect curves + a sound RTPC [D, G]

```
server-driven cart graph 0x098 (MatchLogic.md §12.4)
  owner var $01b3 = push direction (−1 back, 0 idle, 1 forward)   ← Stacks R2–R4 (remote states)
  Switch 63 on $01b3 → one of DataFlowMapping R11 / R12 / R13 (remote states, nodes 62/64/65)
       m_dataFlow 0x14F (Float, default 1, [0,1])   m_answer Int 0 / Float 0.5 / Int 1   m_entity "any"
       └─ active mapping registers {0x14F → answer} in the cart's comp 0x23 registry (0xccb370)
cart entity comp 0x03: producer list … comp 0x23 provider (registered in 0x23 OnCreate, 0xcca010) …
       └─ read of 0x14F → the most recently activated mapping's cached answer (evaluated after each script tick)
consumers on the cart (each cart definition adds a map-specific visual graph):
  def 0x120: graph 0x286 → Effect 0x458  ┐
  def 0x42E: graph 0x2D9 → Effect 0x682  ├ effect parameter 7 = kind 6, id 0x14F → curves 6 / 8 / 17
  def 0x77F: graph 0xA33 → Effect 0x15D4 ┘   (per effect update, from the cart's comp 0x03)
  sound binding 0x0D200000000001C0: STUSoundParameter 0x0740000000000260 ← 0x14F (Wwise RTPC, per sound update;
      used by 0x04F 0x0720000000000181)
nothing active (before the graph starts, or the switch between states) → default 1
```

Mapping 0 / 0.5 / 1 to back / idle / forward is by the data; what the curves drive (wheel spin, a track texture
scroll) was not looked at [?]. **For the server**: sync `$01b3` and the remote states R2–R4 / R11–R13 as the graph
defines them; nothing else. The cart graph `0xA33` also maps `0x14E` (answer: an expression, `$0b60` := 0.9 on
`0x…2d`), read by effect `0x1717` on the cart's child definition `0x1275`, by two draw lists (`0x16BF`, `0x3A9`),
three blend trees and a sound binding; the child reads it through inheritance from the cart [?].

### 7.2 Capture ring: `$009a` → `0x0FD` → (no reader) [D, G]

```
ring entity (def 0x2ED: model, mirrored anim, graph 0x208; one per capture point)
  Stack 2: $009a := $0702.$009a        ($0702 = its capture point, ENTITY, synced)
  DataFlowMapping 1: m_dataFlow 0x0FD (Float, default 0, [0,1])  m_answer Dynamic $0702.$009a  m_entity "any"
  Effects 3–5 / 8–12 (0x3B1, 0x3E4, 0x3DD, 0x4C5, 0x49B) via BooleanSwitches 6 / 10
```

- **Nothing reads `0x0FD` in 0.8**: it is in no effect parameter table or draw list (all 10,188 effects), in no anim
  blend tree / set, sound binding or condition, material or model, no UX resource references it, and no call site
  passes it as a constant id [D; G: immediate-argument scan of the getters]. The ring's effects take only Statescript
  inputs (kind 12: the Effect state's team comparison, i.e. colours) and built-ins.
- So the mapping publishes the capture progress on the ring's comp 0x03, but no presentation of 0.8 consumes it: the
  progress the player sees comes from the capture point's own graph `0x028` (UIInWorld ring `0x09A…EA` and the HUD
  view models reading the vars directly, InstanceServer.md §12) [?: a consumer could still read it through a code
  path with a computed id; a live read of the ring's comp 0x03 during a capture would show the value, not a reader].
- **For the server**: the ring copy needs `$0702` (and the point's `$009a`) synced for the mapping to have a value;
  nothing visible depends on `0x0FD` today.

### 7.3 Team colour and hostility: comp 0x13 / 0x10 → `0x6B`, `0x1E` → effects [G, D]

```
any entity with comp 0x13 PlayerIdentity or 0x10 FilterBits: producer 0xccfa70 / 0xcd0590 answers
   0x6B = 1.0 if hostile to the local pawn else 0.0      0x1E = red (1,0,0,1) or blue (0,0,1,1)
children (effects' scene nodes, spawned entities, projectile visuals) inherit through the scene parent / forwarders
204 effect parameters read 0x6B (kind 6, keep-last): ability effects switch to enemy / ally looks
```

Everything here comes from the local client's view (who the local pawn is, the replicated filter bits / team), so
the same effect looks red to one player and blue to another. **For the server**: replicate team / filter bits (it
already does); no DataFlow involvement.

### 7.4 First-person camera kick: comp 0x33 → `0x3B`, `0x45`, `0x34`, `0x3A` → `0xdcbed0` [G]

LocalHitEffects (comp 0x33, on the controller) publishes the time since the last hit (`0x3B`), an amplitude (`0x45`)
and angles (`0x34`, `0x3A`); the first-person camera (`0xdcbed0`, per frame) reads them from its entity and kicks /
shakes when the timer is below the default 100. A purely local chain: comp 0x33 is filled from local hit events.

## 8. Open questions

1. **Live confirmation** (not done; the client was down): `read_component 0x03` on a payload cart while it moves
   (expect `0x14F` = 0 / 0.5 / 1 in a value stack) and on a ring copy during a capture (expect `0x0FD` = progress).
2. Who reads `0x0FD` (if anything): the negative result above covers the data and constant ids only.
3. Readers of `m_min` / `m_max`, the three unnamed Float fields and `m_visibleOnServer`.
4. Names of the `STUStatescriptDataFlowEntity` subclasses (`8070a3c9`, `39f790a9`, `4d8a2f7c`) and of the id
   meanings (no DataFlow names in 0.8).
5. Health pool order behind `0x79`–`0x80` (the health getters are anti-tamper stubs): check live on a hero.
6. Where the weapon shot record's data-flow table is filled from `m_fixedDataFlow`.
7. Which effects contain data-flow output elements (§4.3) and what their element descriptor looks like in the file.
8. `0xF3` semantics (first person / local third person / remote [?]), the scene provider's `0x182`, the Wwise ids
   `0xBC`, `0x2E9`.

## 9. Tools and data

- Tables behind the numbers here: `systems/data/dataflow/` (all 361 assets with flags and referrers, every
  DataFlowMapping / global-value state, every kind-6 effect parameter, the draw lists; README there).
- Offline: `systems/content/build/content_dump --install game/owbetaclient --read <GUID> OUT` +
  `systems/stu/build/stu_dump OUT` for STU (DataFlows, graphs, sound bindings); effect parameter tables per §5.1.
- The explorer (docs/Explorer.md) lists the 0x013 resources and their STU referrers (refs in).
- Prometheus: `read_component` 0x03 (Prometheus `Components/Component_3_DataFlow.h` has an older partial layout).
