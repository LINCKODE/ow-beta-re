# Physics: Blizzard's "Domino" engine (`dm`) in the 0.8 client

How rigid-body physics works in the Overwatch beta 0.8 client (build 24919): the engine, its data structures,
its step, its solver, and how the game uses it. It closes with a module-by-module comparison with Erin Catto's
public engines (Box2D v2.4, Box2D v3.2, Box3D 0.1). Written for T069 (2026-09-29). The character mover that runs on
top of the engine is in [Movement.md](Movement.md) §4 and is only cross-referenced here.

Tags: **[G]** read in Ghidra, **[D]** checked over data files, **[src]** public source code (Box2D / Box3D, read
2026-09-29), **[pub]** public statement by Blizzard or Erin Catto, **[?]** inferred. Nothing here was measured live:
the game lock was held for a play session during this task (§14).

Addresses are Ghidra addresses (RVA = address − `0x7ff64ffe0000`). The functions named here were renamed in the
Ghidra project during T069.

---

## 0. Summary

- **Name and origin.** The engine is **Domino**, Blizzard's shared in-house physics engine. The binary names its
  dump files `DominoDump.txt` and `DominoTreeDump.txt` [G]. Erin Catto, the author of Box2D, was its lead at
  Blizzard; it also ships in Diablo III, StarCraft II, World of Warcraft and Heroes of the Storm [pub].
- **Shape.** It is a **3D Box2D v2.x**: a C-style API over handles, a `dmWorld_Step(world, dt, velocityIterations,
  positionIterations, …)` in the exact order of `b2World::Step`, islands rebuilt by depth-first search every step,
  a sequential-impulse solver with warm starting, Baumgarte position iterations (β 0.2, slop 0.005),
  `b2_maxTranslation` 2.0, `b2_timeToSleep` 0.5, `b2_maxTOIContacts` 32, 20 TOI position iterations, v2's block and
  stack allocators, and v2's destruction listener [G, src].
- **Ahead of v2.** Several features of Box2D v3 (2024) and Box3D (2025–26) already ship here in 2016: generational
  handle ids `{index, revision, world}`, one broadphase tree per body type with the proxy key `index << 2 | type`,
  contacts that carry several 4-point manifolds (mesh contacts), central friction with rolling resistance, a
  per-shape explosion factor, soft weld joints (linear/angular frequency and damping ratio), and v3's constants
  (max rotation π/4, sleep threshold 0.05 m/s) [G, src]. The **character plane solver** is the same algorithm as
  Box3D's `b3SolvePlanes` / `b3ClipVector`, and the **baked mesh BVH** node is the same 32-byte layout as Box3D's
  `b3MeshNode` [G, D, src].
- **Not v3.** No soft step, no substepping, no relax or restitution passes, no graph colouring, no solver sets,
  no persistent islands. Contacts are rigid by default and become soft (10 Hz, damping ratio 0.7) only against
  kinematic bodies or at mass ratios above 10 [G].
- **Threading.** The narrowphase runs as up to 64 parallel tasks, and every island is solved as its own task, through
  a task interface the game plugs in [G]. Box2D v2 is single-threaded; v3 uses graph colouring instead of per-island
  tasks.
- **How Overwatch uses it.** One `dm` world per scene. The map is one static body. Props and breakables are model
  collision bodies, and ragdolls are bodies plus shoulder / revolute / spherical / weld joints. All of these are
  stepped once per **render frame** (dt ≤ 1/15 s, 8 velocity and 2 position iterations), so they are cosmetic. Heroes
  are not bodies: they are **characters**, which are world-level kinematic proxies moved by the gameplay mover per
  command frame. Gameplay reads the world through queries (46 call sites of `Scene_RayCast`). Weapon hits do not go
  through physics contacts [G, talk].

---

## 1. Identity and history

| fact | source |
|---|---|
| The dump writers name their files `DominoDump.txt` (world as C++ test code) and `DominoTreeDump.txt` (broadphase trees); the emitted code creates a `Testbed/Data/…` scene, i.e. a Box2D-style testbed | [G] strings at `0x7ff6513dc478`, `0x7ff6513dcd78`, `0x7ff6513dc7d0` |
| "MoP has the Domino physics engine, same as D3 and SC2" | [pub] Erin Catto on X, 2013 ([link](https://x.com/erin_catto/status/286957855679082496)) |
| Catto's GDC 2019 speaker bio: working on the Domino physics engine used by Diablo 3, StarCraft, Overwatch, World of Warcraft, Heroes of the Storm and Call of Duty; physics and animation lead on Blizzard's shared engine | [pub] GDC 2019 schedule (as quoted by a web search on 2026-09-29; the page itself did not resolve) |
| Diablo III first used a ragdoll system on Havok, then switched to the in-house Domino, which handles its destruction and ragdolls | [pub] Catto, *Diablo 3 Ragdolls*, GDC 2012 ([slides](https://box2d.org/files/ErinCatto_Ragdolls_GDC2012.pdf)) |
| Soft constraints (frequency + damping ratio), which Domino's weld joint and soft contacts use | [pub] Catto, *Soft Constraints*, GDC 2011 ([slides](https://box2d.org/files/ErinCatto_SoftConstraints_GDC2011.pdf)) |
| A Blizzard character controller built on a swept collision + iterative (Lagrange-multiplier) solver | [pub] Catto, *Understanding Constraints*, GDC 2014 ([summary](https://www.gamedeveloper.com/programming/video-the-physics-behind-blizzard-s-character-controller)) |
| Kaplan credits Catto with getting the Blizzard physics engine working in Overwatch "so you can have things like the crates and vases on Temple of Anubis breaking and moving" | [talk] BlizzCon 2017 retrospective (quoted by the user; Movement.md §4.1) |

**Timeline.** Box2D v2.x (2007–2020) → Domino (at Blizzard from ~2011, in Overwatch 0.8 in 2016) → Box2D v3.0 (2024),
v3.1 (2025: character mover `b2World_CastMover` / `b2SolvePlanes`) → Box3D 0.1 (2026, public repo
`erincatto/box3d`). Domino sits between v2 and v3: it keeps v2's solver and step but already has the data design
(handles, per-type trees, manifolds) and several tools (plane solver, mesh BVH, rolling resistance, explosions) that
appear publicly only in v3 and Box3D. [?] The simplest reading is that v3 and Box3D are Catto's public rewrite of what
he built at Blizzard, with a new solver.

---

## 2. Architecture at a glance

```
            game (scene 0x3a0 + 0x4c0 = u16 world id)                    [G]
               │  create/destroy bodies, fixtures, characters, joints; queries; step
               ▼
  DAT_7ff6517f8a20[256]  ──►  dmWorld (0x5a0 bytes)
      ├─ +0x008 broadphase: 4 dynamic AABB trees (static, kinematic, dynamic, characters) + move buffer + pair buffer
      ├─ +0x0e0 block allocator      +0x168 stack allocator       (Box2D v2 b2BlockAllocator / b2StackAllocator)
      ├─ +0x2f0 bodies      (pool, 0x1b0)      +0x310 characters (pool, 0x50)     +0x330 fixtures (pool, 0x90)
      ├─ +0x350 contacts    (pool, 0x60; each owns n manifolds of 0x100)
      ├─ +0x3b0 joints      (pool, 0xd0)  + one data pool per joint type at +0x3d0 … +0x490
      ├─ +0x4b0 awake bodies   +0x4c0 awake contacts   +0x540 TOI bodies
      ├─ +0x510 gravity (vec4) +0x500 inv_dt0
      ├─ +0x520 contact listener  +0x528 destruction listener  +0x530 task system  +0x538 body-wake callback
      └─ +0x58c locked flag   +0x590 flags (warm starting, continuous, …)
```

Every value is a 16-byte SSE vector (`dm_vec4`); even scalars in the step context are broadcast to 4 lanes [G].

---

## 3. Handles and API

### 3.1 Ids [G]
All objects are addressed by a 64-bit id: **low 32 bits = pool index, bits 32…47 = revision (u16), bits 48…63 = world
id (u16)**. The null id is `{index −1, revision 0, world 0xffff}` (`DAT_7ff6513dc258`, `DAT_7ff6513dd110`). An API call
checks `world < 256`, the world pointer, `index < count`, the slot's stored index (`== index`, i.e. the slot is live)
and the revision (body `+0x190`, fixture `+0x7c`, character `+0x3c`, joint `+0xc0`).

Same idea as Box2D v3 / Box3D (`b3BodyId {int32 index1; uint16 world0; uint16 generation}`) [src]; Box3D stores
index + 1 so that zero means null, Domino stores the index and uses −1.

### 3.2 API style [G]
Free functions over ids, the Box2D v3 style: `dmWorld_CreateBody(worldId, &def)`, `dmBody_CreateFixture(bodyId,
&def)`, `dmWorld_CreateJoint(worldId, &def)`, `dmWorld_CreateCharacter`, `dmWorld_Step`, `dmWorld_CastRay`,
`dmWorld_QueryAABB`, `dmCharacter_MoveSwept`, `dmCharacter_Teleport`, getters and setters per object (about 84 entry
points go through the world table). Creation functions validate the def (finite position, quaternion length > ε)
and refuse while the world is **locked** (`+0x58c`, set for the duration of a step) [G]. Defs are C++ structs with
`m_` members (`dmBodyDef`, `dmSphereDef`, `dmCapsuleDef`, `dmPolytopeDef`, `dmTreeMeshDef`, `dmHeightFieldDef`,
`dm…JointDef`), the Box2D v2 naming [G, strings].

### 3.3 World creation (`dmWorld_Create` `0x7ff6511a0940`) [G]
Finds a free slot of 256, allocates 0x5a0 bytes and sizes **fixed pools from the def**: def[0..3] gravity, [4] bodies,
[5] characters, [6] fixtures, [7]+[8] contacts (two kinds, pools `+0x370` / `+0x390`), [9…15] one count per joint type,
[0x10] stack-allocator bytes. Initial flags `0x2e` (warm starting, continuous, …). Unlike v2 (block allocator per
object) and v3 (growable arrays), capacity is chosen up front; the pools still grow when full (`FUN_7ff65119af10`
doubles) [G]. The def the game passes is supplied by the scene's creator and was not read (see §15).

---

## 4. Bodies [G]

Body record, 0x1b0 bytes (field names from the dump writer `dmDump_Body` `0x7ff651191890` where it prints them):

| offset | field |
|---|---|
| +0x00 | own index (slot live check) |
| +0x04 | type in the low 3 bits: **0 dynamic, 1 kinematic, 2 static** (`m_bodyType`) |
| +0x08 | flags: bit 0 auto-sleep allowed, bit 1 awake, bit 4 selected for `dmWorld_StepBodies`, bit 5 needs TOI |
| +0x10 / +0x20 | rotation (quat) / origin (`m_quaternion`, `m_position`) |
| +0x30 / +0x40 | linear / angular velocity |
| +0x50 / +0x60 | sweep: centre of mass, rotation (current); +0x80 / +0x90 the same at the start of the step |
| +0xf0 … +0x12c | local inverse inertia (4×vec4) |
| +0x130 / +0x140 | force / torque accumulators |
| +0x158 | first fixture; +0x160 first joint edge (`2·joint + side`) |
| +0x164 / +0x168 | inverse mass / mass |
| +0x16c | `m_gravityScale` |
| +0x170 / +0x174 | `m_linearDamping` / `m_angularDamping` |
| +0x178 | `m_inertiaScale` |
| +0x17c | sleep time |
| +0x188 | island epoch; +0x18c index in the awake list (−1 = not listed) |
| +0x190 | revision (u16) |

Types in the broadphase use a different numbering: tree 0 static, 1 kinematic, 2 dynamic (§6).

---

## 5. Fixtures, shapes, materials, filters

### 5.1 Fixture record, 0x90 bytes [G]
| offset | field |
|---|---|
| +0x00 | world AABB (2 × vec4) |
| +0x28 | shape pointer (C++ object with a vtable: `+8` compute AABB, `+0x10` …; type at shape `+8`) |
| +0x38 | first contact edge (`2·contact + side`); +0x3c contact count |
| +0x40 | owner index; +0x44 next fixture of the owner |
| +0x48 | `m_density` |
| +0x54 / +0x58 | `m_friction` / `m_restitution` |
| +0x5c | `m_rollingResistance` |
| +0x60 | `m_explosionFactor` |
| +0x64 | AABB margin (fat-AABB extension, per fixture; 0.1 on characters, Movement.md §4.2) |
| +0x68 | push limit (character plane solver) |
| +0x6c | `m_filter` {u16 `m_categoryBits`, u16 `m_includeMask`, s16 `m_groupIndex`} |
| +0x78 | broadphase proxy key |
| +0x7c | revision; +0x7e material index; +0x80 surface flags; +0x82 u16 |
| +0x84 | `m_isSensor` |
| +0x88 | owner kind: 1 body, 2 character |

A fixture is owned either by a body or by a character (§7); contacts and queries treat both alike.

### 5.2 Shapes [G, D]
0 sphere {`m_localPosition`, `m_radius`}, 1 capsule {`m_p1`, `m_p2`, `m_radius`}, 2 polytope (convex hull, built with
`dmPolytopeData::SetAsHull(vertices, n, 0.0f, 0)`, skin radius **0.01**), 3 tree mesh (`dmTreeMeshDef` + `dmMeshData`,
`m_scale`), 4 height field (`dmHeightFieldDef` + `dmHeightData`; not used by any map seen). Shapes are polymorphic
C++ objects (virtual AABB / mass / ray functions), the Box2D v2 design, not v3's tagged unions [G].

The hull skin 0.01 equals Box2D v2's `b2_polygonRadius = 2 · linearSlop` [src]. A mesh triangle is collided as a
temporary triangle shape with the same radius (Movement.md §4.2).

**Tree mesh** (the map's baked collision; Maps.md §2.6) [D]: 32-byte BVH nodes in pre-order, internal nodes store the
offset of the second child (first child = next node), leaves store the first triangle and a triangle count; byte +0x1e
is 0/1/2 on internal nodes and 3 on leaves. Box3D's `b3MeshNode` is the same 32-byte layout: `axis : 2` (0, 1, 2 =
split axis; 3 = leaf) packed with `childOffset : 30` / `triangleCount : 30`, and `triangleOffset` [src]. So byte +0x1e
is the split axis. Domino adds a u16 **category mask per node** (OR of its triangles) and per triangle, which lets a
query prune whole subtrees by filter; Box3D has no per-node mask [src]. Triangles store their three **wing vertices**
(opposite vertex across each edge) for internal-edge rejection; Box3D precomputes concave-edge flags per triangle
instead [src].

### 5.3 Materials [G]
`dmWorld_LoadMaterials(worldId, dmHeightMaterial[] {friction, restitution}, n)` loads a world material table; mesh
triangles and fixtures carry a material index (+0x7e) and surface flags (+0x80; bit 0 = walkable for the ground probe
[?], Movement.md §4.6). Contact reports carry both (§11). Box3D's equivalent is `b3SurfaceMaterial {friction,
restitution, rollingResistance, tangentVelocity, userMaterialId, customColor}` with per-triangle material indices
[src].

### 5.4 Filtering [G] (Movement.md §4.6)
`dm_ShouldCollide`: `(A.category & B.mask) && (B.category & A.mask)`; if both group indices are non-zero, the same
group collides only if positive, and **different non-zero groups never collide** (unlike Box2D, where different
groups fall through to the mask test). Masks and categories are 16-bit (Box3D: 64-bit) [src].

---

## 6. Broadphase [G]

`dmBroadPhase` (world `+0x008`, init `0x7ff6511b7670`): **four dynamic AABB trees** of 0x28 bytes each at +0x00,
+0x28, +0x50, +0x78, then a move buffer (+0xa0) and a pair buffer (+0xb0). A proxy key is **`nodeIndex << 2 |
tree`** with tree 0 static, 1 kinematic, 2 dynamic, 3 characters. This is Box2D v3's `B2_PROXY_KEY(ID, TYPE) = (ID << 2)
| TYPE` with `trees[b2_bodyTypeCount]`, one tree per body type [src]; Domino adds a fourth tree for characters.

**Tree** (`dmDynamicTree_*`): nodes of 0x40 bytes, 64-byte aligned: fat AABB (2 × vec4), user data (u64) +0x20, parent /
free-list next +0x28, child1 +0x2c, child2 +0x30, height +0x34 (−1 = free). Initial capacity 32, doubling.
`CreateProxy` / `MoveProxy` fatten the AABB by the fixture's **margin** (+0x64) and skip the reinsert while the new
AABB is still inside the fat one; there is no velocity-based prediction (v2 had `b2_aabbMultiplier`; v3 and Box3D
also use a margin only) [src]. `dmDynamicTree_Rebalance(tree, n)` re-optimises n leaves per step round-robin (10 per
step on the static tree, 4 on the others), the incremental rebalance of early Box2D v2 [G].

**Pair update** (`dmBroadPhase_UpdatePairs` `0x7ff651199f50`, run at the start of the step when new fixtures exist and
after every solve): for each moved proxy, a kinematic proxy queries the dynamic and character trees, a dynamic or
character proxy queries all four; static proxies never query. The pairs go into one buffer, are **sorted and
deduplicated** (v2's `b2BroadPhase::UpdatePairs`) and handed to `dmContactManager_AddPair` [G]. v3 does the same
queries in parallel with a hash set instead of a sorted buffer [src].

`AddPair` (`0x7ff65119aae0`): skips fixtures of the same owner, applies the filter, looks for an existing contact by
walking the **shorter** of the two fixtures' contact lists, and for two body fixtures also asks the body-level test
(joint `collideConnected`, both non-dynamic) [G].

---

## 7. Characters: kinematic proxies in the world [G]

Characters are a first-class world object (pool `+0x310`, 0x50 bytes: rotation +0x00, position +0x10, first fixture
+0x24, group s16 +0x38, priority s16 +0x3a, revision +0x3c), with their own broadphase tree and persistent contacts
like any fixture. They have no mass and are never integrated by the solver. The API is `dmWorld_CreateCharacter`,
`dmCharacter_Teleport` (`0x7ff6511bb2f0`), `dmCharacter_MoveSwept` (`0x7ff6511bb1d0`: swept AABBs, pair update, time
of impact per contact by conservative advancement), a contact refresh (`0x7ff6511baef0`) and group / priority
setters. The game's controller (`dmCharacter_CreateController` `0x7ff651331010`, `dmCharacter_MovePlanes`,
`dmCharacter_ClipVelocity`) builds collision planes from the contacts and runs the plane solver: Movement.md §4.3–§4.4.

The plane solver is Box3D's `b3SolvePlanes` [src] with other constants:

| | Domino (Movement.md §4.4) [G] | Box3D `mover.c` [src] |
|---|---|---|
| unknown | accumulated push per plane, clamped to [0, pushLimit] | same |
| separation | n·target − w + 0.005 | `b3PlaneSeparation` + `B3_LINEAR_SLOP` (0.005) |
| stop | max \|Δpush\| < 0.005, ≤ 200 iterations | Σ\|Δpush\| < slop, ≤ 20 iterations |
| velocity clip | `v −= min(0, v·n) n` for planes with push > 0, skipped for teammates | `b3ClipVector`: same, skipped when `clipVelocity == false` |
| soft push | a teammate's plane gets pushLimit = dt' | `pushLimit` field per plane |

The difference is where the character lives: Box3D's mover is a set of **stateless queries** (`b3World_CastMover`,
`b3World_CollideMover` against a capsule the caller owns) [src], while Domino keeps characters in the world with
persistent contacts, groups and priorities.

---

## 8. Contacts and narrowphase [G]

**Contact** (pool `+0x350`, 0x60 bytes): fixture A +0x04 / B +0x14, owners +0x08 / +0x18, list links per side
(+0x10 / +0x20), manifold array +0x28 and manifold count +0x30, island index +0x44 / +0x48, island epoch +0x4c, awake
list index +0x50, flags +0x5a (bit 0 enabled, bit 1 touching). **A contact owns several manifolds** (a convex shape
against a mesh gets one per triangle group), each 0x100 bytes: 4 points × 0x30 (point, separation, feature, triangle
index, material), normal +0xc0, point count s16 +0xf6. Box3D's `b3Contact` also has `b3Manifold* manifolds;
int manifoldCount` with 4-point manifolds; Box2D v2 had one 2-point manifold per contact [src].

**Collide** (`dmWorld_Collide` `0x7ff65119ca10`): the awake contact list is cut into up to **64 chunks** (≥ 32 contacts
each) that run as tasks on the task system (§12), guarded by one critical section for shared updates [G]. Narrowphase
functions (dispatch, GJK, SAT, capsule–triangle, manifold reduction) are in Movement.md §4.5.

---

## 9. The step [G]

`dmWorld_Step(worldId, dt, velocityIterations, positionIterations, flag)` (`0x7ff6511a17c0`) locks the world and calls
`dmWorld_StepImpl` (`0x7ff65119fdc0`):

1. If new fixtures were added (flag bit 0): `dmBroadPhase_UpdatePairs`.
2. Step context: dt (vec4), inv_dt = 1/dt (0 when |dt| is tiny), **dtRatio = dt · inv_dt0** (warm-start scaling),
   warm-starting flag (world flag bit 2), iteration counts.
3. `dmWorld_Collide` (timed: profile at world +0x1a4).
4. If world flag bit 5 is set (set when a TOI pass completes; the counterpart of v2's `m_stepComplete` [?]) and
   dt > 0: `dmWorld_Solve`. Otherwise,
   if a contact listener is set, report the current manifolds of body–body contacts to it.
5. If continuous is enabled (flag bit 3) and dt > 0: `dmWorld_SolveTOI`.
6. inv_dt0 = inv_dt when dt > 0; pool maintenance; high-water marks and a profile (step, collide, solve, TOI times).

This is `b2World::Step` of Box2D v2.4 line for line (`if (m_newContacts) FindNewContacts; dtRatio = inv_dt0 · dt;
Collide; if (stepComplete && dt > 0) Solve; if (continuousPhysics && dt > 0) SolveTOI; inv_dt0 = inv_dt`) [src].

### 9.1 Islands (`dmWorld_Solve` `0x7ff65119d820`)
Islands are **rebuilt every step** by depth-first search from each awake, not-yet-visited body over touching contact
edges and joint edges; static bodies end the search (they join an island but do not conduct). Visited marks use an
epoch counter instead of clearing flags. A body reached by the search that was asleep calls the **body-wake callback**
(+0x538). Each island is copied into an island task (0xe0 bytes from the block allocator) and **submitted to the task
system**; the solve waits for all, then finalises each island (sync fixtures, re-add to the awake list, flag fast
bodies for TOI), and runs `dmBroadPhase_UpdatePairs` [G]. This is v2's `b2World::Solve` (DFS + `b2Island`) with the
islands run in parallel. v3 / Box3D keep **persistent islands** (union on contact begin, split when needed) and solve
the awake set with graph colouring instead [src].

### 9.2 Island solve (`dmIsland_Solve` `0x7ff6511c2920`)
Per island, in the order of `b2Island::Solve` (v2) [G]:
1. **Integrate velocities**: world inverse inertia from the rotation (`R · I⁻¹local · Rᵀ`), `v += dt · (gravityScale ·
   gravity · … + invMass · force)`, `w += dt · I⁻¹ · torque` with a gyroscopic step (`FUN_7ff6511c3c20`), then damping
   **`v *= clamp(1 − dt · linearDamping, 0, 1)`** (the older Box2D form [?]; v2.4.1, v3 and Box3D use
   `1 / (1 + dt · damping)` [src]). Forces are cleared after (island flag).
2. Joint `InitVelocityConstraints`, contact solver init + warm start (`dmContactSolver_InitVelocityConstraints`).
3. **velocityIterations** × (joints `SolveVelocityConstraints`, contacts `SolveVelocityConstraints`).
4. Store impulses.
5. **Integrate positions** with the v2 limits: translation per step ≤ **2.0** (`b2_maxTranslation`), rotation per step
   ≤ **π/4** (v2: π/2; v3 and Box3D: π/4).
6. **positionIterations** × (joints `SolvePositionConstraints`, contacts `SolvePositionConstraints`): nonlinear
   Gauss–Seidel with **β = 0.2** (`b2_baumgarte`) on `C = sep + 0.005` clamped to ≤ 0. The contact position solve
   returns "min separation ≥ −1.5 · slop", but the loop ignores it and **always runs every position iteration** (v2
   exits early) [G].
7. Write back, synchronise transforms.
8. **Sleep** (if allowed): per body with auto-sleep, reset the sleep timer when |v|² > **0.05²** or |w|² > **(5°/s)²**,
   otherwise accumulate; when the island's minimum reaches **0.5 s** (`b2_timeToSleep`), zero every body's velocity and
   force and put the island to sleep. v2 uses 0.01 m/s and 2°/s; v3 / Box3D use a single 0.05 m/s threshold [src].

### 9.3 Contact solver (`dmContactSolver_*` `0x7ff6511d73a0` … `0x7ff6511d9750`) [G]
- One velocity constraint per **manifold** (0x1f0 bytes) plus one record per point (0x60); position constraints 0xb0
  + 0x30.
- **Rigid or soft per contact.** A contact is **soft** when either body is kinematic, or both are dynamic and one
  inverse mass exceeds 10× the other; otherwise it is rigid. Soft contacts use the GDC 2011 soft-constraint formula
  with **f = 10 Hz, ζ = 0.7**: ω = 2πf, d = 2mζω, k = mω², γ = 1/(h(d + hk)), bias = C·hkγ, with C clamped to ≥ −0.25.
  Rigid contacts get no velocity bias; their overlap is fixed by the position iterations.
- **Restitution** as a velocity bias when the approach speed exceeds **2 m/s** (Box2D v2/v3 and Box3D default to 1 m/s)
  [G, src].
- **Friction is central**: per manifold, a 2D tangent impulse clamped to a circle of radius μ·Σλn (the sum of the
  points' accumulated normal impulses), a twist impulse about the normal, and a rolling-resistance impulse clamped
  by length; then the normal impulses per point (`λ' = max(λ − m(vn + bias + γλ), 0)`). Box3D solves the same three
  central friction terms (`frictionImpulse`, `twistImpulse`, `rollingImpulse`), but after the normals [src]. The exact
  friction formulas were not transcribed.

### 9.4 Continuous collision (`dmWorld_SolveTOI` `0x7ff65119e5e0`) [G]
Island finalisation puts a body into the TOI list (+0x540) when its **continuous flag** (body flags bit 3) is set and
it is **fast for its size** (`FUN_7ff651191e80`): (|ω| · maxExtent + |v at the centre|) · dt > 0.25 · minExtent-term
(body +0x184 / +0x180). That is the Box2D v3 / Box3D test (`safetyFactor` 0.5 × extent, `b3BodyDef.safetyFactor = 0.5`)
[G, src]; v2 has no fast-body test and runs TOI for every dynamic body against static ones. A loop computes each one's time of impact (`FUN_7ff65119cba0`), then for each builds a mini-island of the body
and its touching contacts **with static bodies only** (≤ 32 contacts = `b2_maxTOIContacts`), and re-solves it over
the remaining (1 − t) · dt with 20 position iterations (v2 `SolveTOI` uses 20) as an island task. No bullet flag was
found: dynamic–dynamic tunnelling is not prevented [?]. v3 / Box3D do continuous per fast body against static geometry
too, plus bullets, and add speculative contacts [src].

### 9.5 Stepping a subset (`dmWorld_StepBodies` `0x7ff6511a1890`) [G]
A second entry point marks a list of bodies (flag bit 4) and runs N consecutive solves of only their islands
(`FUN_7ff65119e0c0`). The game calls it (`FUN_7ff650a14870`) with dt 1/60, 8 / 2 iterations, N = min(frames, 120) and
up to 32 bodies taken from an object whose records are 0x50 bytes: fast-forwarding one object, probably a ragdoll that
starts late [?].

---

## 10. Joints [G]

Seven types, each with its own data pool (`dmJoint_Init` `0x7ff6511a2250` switches on `def.type − 1`); defs use local
frames (`m_localFrameA/B` = quat + position) except the distance joint (`m_localAnchorA/B`):

| type | name | data | def fields (dump strings) | used by the game |
|---|---|---|---|---|
| 1 | distance | 0xd0 | `m_localAnchorA/B`, `m_distance`, `m_collideConnected` | no |
| 2 | mouse | 0x120 | target, max force, frequency, damping ratio (soft, γ term) | no |
| 3 | prismatic | 0x1c0 | `m_localFrameA/B`, `m_enableLimit`, `m_lowerLimit`, `m_upperLimit`, `m_motorMode` (`dmMotorMode`), `m_maxMotorForce` | no |
| 4 | revolute | 0x2c0 | frames, `m_lowerAngle`, `m_upperAngle`, `m_motorMode`, `m_maxMotorTorque` | ragdolls |
| 5 | shoulder | 0x2e0 | frames, `m_lowerTwistAngle`, `m_upperTwistAngle`, `m_coneAngle`, `m_enableTwistLimit`, motor | ragdolls |
| 6 | spherical | 0x180 | frames, `m_frictionTorque` | ragdolls |
| 7 | weld | 0x1e0 | frames, `m_linearFrequencyHz`, `m_angularFrequencyHz`, `m_linearDampingRatio`, `m_angularDampingRatio` | ragdolls |

The mouse joint (type 2) has no entry in the dump writer, which is exactly Box2D v2's `b2MouseJoint::Dump` ("Dump is not
supported for this joint type"). Box3D's joint set is parallel, distance, filter, motor, prismatic, revolute,
**spherical** (= Domino's shoulder: cone + twist limits + motor torque), weld (**the same four soft parameters**,
named `linearHertz` etc.) and wheel [src]. The game creates types 4–7 only, from two ragdoll builders
(`FUN_7ff650a10a80`, `FUN_7ff650a16830`) [G].

---

## 11. Events and callbacks [G]

| world slot | set by | called |
|---|---|---|
| +0x520 contact listener | `dmWorld_SetContactListener` | after each island solve, `dmIsland_ReportContacts` sends **batches of up to 16 manifolds** (normal, points, separations, normal impulses, fixtures, materials, triangle index, owner kinds) through vfunc +8, filtered by body type and a separation threshold; also used for body–body manifolds when solving is disabled |
| +0x528 destruction listener | `dmWorld_SetDestructionListener` | when a body is destroyed: vfunc +8 per fixture, +0x10 per joint (Box2D v2 `b2DestructionListener::SayGoodbye`) |
| +0x530 task system | `dmWorld_SetTaskSystem` | vfunc +0x10 add narrowphase chunk, +0x20 add island, +0x30 wait all; a default serial implementation exists (`PTR_PTR_7ff6516eafb0`) |
| +0x538 body-wake callback | `dmWorld_SetBodyCallback` | with the body id when an island search wakes a sleeping body |

No per-contact begin / end callback was found; contacts leave the engine as buffered reports drained once per step
(the game's drain is `FUN_7ff650a217c0`, ECS_Components.md §0x01). Box2D v3 / Box3D return begin / end / hit events
as arrays after the step [src]; Domino's batches pushed through a listener are close to that. Sensors exist as a
fixture flag (`m_isSensor`); their event path was not traced [?].

---

## 12. Threading, SIMD, memory, determinism

- **Threading** [G]: parallel narrowphase (≤ 64 tasks), one task per island, through the pluggable task system. The
  scene sets its job system as the task system just before `dmWorld_Step` and resets it after (`Scene_StepPhysics`).
  Box2D v3 / Box3D take `enqueueTask` / `finishTask` callbacks in the world def [src]; same idea.
- **SIMD** [G]: all math on `__m128` (`dm_vec4`, quaternion `dmMulQQ`); reciprocals and square roots through `rcpps`
  / `rsqrtps` + one Newton step. Constant table at `0x7ff6517f88b0…0x7ff6517f89e0` (copied from `.rdata` at start-up):
  1/3, 0.5, 1, 2, 3, FLT_MAX, **0.005** (slop), FLT_EPSILON, 1e-35 (division guard), unit axes, π, 2π, ±∞ masks.
- **Memory** [G]: `dmBlockAllocator` (size-class free lists, chunk ~32 KB (`0x7f00`), blocks up to 1280 bytes, chunk
  table grows by 128: Box2D v2's `b2BlockAllocator` with bigger numbers) for islands and small objects; a
  `dmStackAllocator` (fixed buffer sized by the world def, heap fallback when full: v2's `b2StackAllocator`) for
  per-step arrays; fixed-capacity pools with free lists and revisions for the objects.
- **Determinism** [?]: not a design goal visible in the code. `rcpps` / `rsqrtps` results differ between CPU vendors,
  so bit-identical results across machines are not guaranteed; contact report order depends on task completion.
  Within one machine the per-island work is independent. Box3D lists cross-platform determinism as a feature [src].
  None of this matters to Overwatch because the bodies are cosmetic (§13).

---

## 13. How Overwatch uses it

### 13.1 Who creates what [G]
| creator | object | notes |
|---|---|---|
| scene creation (`FUN_7ff6509d1130`, world flag 0x200) | the **world** (id at scene `+0x3a0`→`+0x4c0`) | also sets the destruction listener (scene +0x4b0), the wake callback `FUN_7ff6509d10f0` (marks the body's scene node active) and the contact listener (scene physics +0x30) |
| map load (`FUN_7ff650a239a0`) | **one static body** with a fixture per baked item: spheres, capsules, hulls, the triangle mesh | Maps.md §2.6, Movement.md §4.2 |
| model collision (`FUN_7ff6509c1d00`) | one body per model instance; **type from the model data** (max over its collision shapes of a mode byte: 1 dynamic, 2 kinematic, 3 static); category 0x1 / 0x100 / 0x1000 | props, breakables (the Anubis crates and vases) |
| ragdolls (`Ragdoll_Create` `0x7ff650a0f040`, builders `FUN_7ff650a10a80` / `FUN_7ff650a16830`) | bodies per bone + revolute / shoulder / spherical / weld joints | log "Partial ragdoll found with intermediate bone on entity: 0x%x" |
| character mover (`dmCharacter_CreateController`) | a **character** with a capsule fixture, group = team | Movement.md §4.3 |
| CollisionProxy 0x16 (`FUN_7ff650cae430`) | a character with a sphere or capsule, teleported each CF to the movement state | turrets; ECS_Systems.md CollisionProxySyncSystem |

EffectPhysics (component 0x57) is not among the 76 game-side callers of the `dm` API, so its job-parallel simulation
looks separate (ECS_Components.md) [? its functions were not enumerated].

### 13.2 Clocks [G]
- **Render frame**: `Scene_StepPhysics` (`0x7ff6509d51f0`, from the scene world update, ECS_Components.md §0x01)
  calls `dmWorld_Step(world, min(frame dt, 1/15), 8, 2, 1)`, or dt 0 while paused; then drains contact reports into
  impact effects. So every rigid body (props, debris, ragdolls) is **client-side and cosmetic**, stepped at the
  render rate with a variable dt.
- **Command frame** (16 ms): the character mover moves its character through queries and `dmCharacter_MoveSwept`
  (Movement.md §3–§4); CollisionProxySyncSystem teleports proxies. Characters see kinematic bodies where the last
  render-frame step left them [?].
- `dmWorld_StepBodies`: fixed 1/60 sub-steps for one object on demand (§9.5).

### 13.3 Queries from gameplay [G]
`Scene_RayCast` (`0x7ff6509d76c0`, closest hit over `dmWorld_CastRay`) has **46 call sites** (impact effects and decals
`FUN_7ff650c8aa10` / `FUN_7ff650c8aef0`, the scene layer, gameplay code at `0x7ff650d49800`…), and AABB overlap
queries (`dmWorld_QueryAABB`) have 6 wrappers (`FUN_7ff6509d9910` and others). Not catalogued per system (§15).
Weapon hit detection runs against historical poses, not physics contacts (Statescript.md §8.7, talk).

### 13.4 What a server needs
- Nothing of the rigid-body side: props, breakables and ragdolls are client-side presentation, stepped on the render
  clock with no replication.
- The static world and the characters: the map's static body (our `systems/maps` parser already decodes it), the
  characters' capsules, the plane solver and the swept move, the ground probe rays and the filters (Movement.md
  §4.8). Kinematic map movers (doors, platforms) are deterministic from the clock.
- Queries: ray / shape casts against the map mesh with the same category pruning, for line-of-sight and similar
  gameplay checks.

---

## 14. Comparison with Box2D v2.4, Box2D v3.2 and Box3D 0.1

Sources read 2026-09-29: `erincatto/box2d` tag v2.4.1, `erincatto/box2d` main (v3.2.0, last commit 2026-09-24),
`erincatto/box3d` main (v0.1.0, last commit 2026-09-27). "Same" means the same algorithm and constants, "similar" the
same idea with different details.

| module | Domino (OW 0.8, 2016) | Box2D v2.4 | Box2D v3.2 | Box3D 0.1 | Domino vs Box3D |
|---|---|---|---|---|---|
| API shape | C functions over 64-bit ids {index, revision u16, world u16}; defs with `m_` members | C++ classes, pointers | C functions over ids {index1, world0, generation} | same as v3 | **similar** (ids same idea; v2-style defs) |
| world storage | fixed-capacity pools per object type from the world def | block allocator per object | growable arrays, solver sets | same as v3 | different |
| body types | dynamic 0, kinematic 1, static 2 | static, kinematic, dynamic | same | same | same set |
| shapes | sphere, capsule, hull (skin 0.01), tree mesh, height field; virtual C++ shapes | circle, edge, polygon (skin 0.01), chain | circle, capsule, polygon, segment, chain | sphere, capsule, hull, mesh, height field, compound | **similar** (no compound in Domino) |
| mesh | baked BVH, 32-byte nodes (axis/leaf, child offset), per-node category mask, wing vertices | – | – | 32-byte `b3MeshNode` (axis/leaf, child offset), concave-edge flags, materials per triangle | **same node layout**; masks vs flags differ |
| broadphase | 4 trees (static, kinematic, dynamic, characters), key `index<<2\|tree`, margin, incremental rebalance, sorted pair buffer | 1 tree, displacement prediction, sorted pair buffer | 3 trees, same key, margin, parallel queries + hash set | same as v3 | **similar** (trees and key same; pair management v2) |
| contact | several 4-point manifolds per contact | one 2-point manifold | one 2-point manifold | several 4-point manifolds per contact | **same** |
| narrowphase | GJK + SAT, cached hull SAT, capsule–triangle with internal-edge rejection | 2D SAT / clipping | 2D SAT, speculative | GJK + SAT, triangle manifolds, speculative | similar (no speculative contacts in Domino) |
| solver | sequential impulse, warm start with dtRatio, 8 velocity + 2 position (NGS, β 0.2) iterations, no early exit | same (early exit) | soft step: substeps (4), relax, restitution pass, graph colouring (24 colours), SIMD wide | same as v3 | **different** (v2) |
| soft contacts | only kinematic or mass ratio > 10 (10 Hz, ζ 0.7) | none | always (30 Hz, ζ 10, push ≤ 3 m/s) | always (same) | different |
| friction | central: 2D tangent + twist + rolling, per manifold | per point | per point + rolling | central: tangent + twist + rolling | **same idea** |
| restitution threshold | 2 m/s | 1 m/s | 1 m/s | 1 m/s | different constant |
| islands | DFS every step, solved as parallel tasks | DFS every step, serial | persistent, union/split | persistent | different |
| sleep | 0.05 m/s, 5°/s, 0.5 s | 0.01 m/s, 2°/s, 0.5 s | 0.05 m/s, 0.5 s | 0.05 m/s, 0.5 s | **similar** |
| position limits | max translation 2.0, max rotation π/4 | 2.0, π/2 | π/4 | π/4 | similar |
| CCD | per fast body (extent test) vs static, mini-island ≤ 32 contacts, 20 position iterations | global TOI events for every dynamic body, ≤ 32 contacts, 20 iterations, bullets | per fast body (extent test) vs static + bullets, speculative | same as v3 | **similar** (same fast-body test) |
| joints | distance, mouse, prismatic, revolute, shoulder, spherical, weld (soft) | distance, friction, gear, motor, mouse, prismatic, pulley, revolute, rope, weld, wheel | distance, filter, motor, mover, pogo, prismatic, revolute, weld, wheel | distance, filter, motor, parallel, prismatic, revolute, spherical (cone + twist), weld, wheel | **similar** (shoulder ≈ spherical; weld same) |
| character | characters in the world (own tree, contacts, group, priority), plane solver ≤ 200 iterations, clip velocity | – | `b2World_CastMover` / `CollideMover` + `b2SolvePlanes` (20) | `b3World_CastMover` / `CollideMover` + `b3SolvePlanes` (20), `b3ClipVector` | **same algorithm**, different ownership |
| events | batched manifold reports through a listener; destruction listener; wake callback | per-contact listener callbacks, destruction listener | event arrays after the step | event arrays | similar |
| explosions | `m_explosionFactor` per fixture | – | `b2World_Explode` | `b3World_Explode`, shape `explosionScale` | **similar** (engine-side apply not found in Domino) |
| threading | parallel narrowphase + per-island tasks | none | task callbacks, parallel everything | same | similar |
| SIMD | everything in `__m128` | none | wide solver (SSE2 / AVX2 / NEON) | same | different use |
| determinism | not guaranteed (`rcpps`, task order) | – | cross-platform | cross-platform, recording / replay | different |
| constants | slop 0.005, hull skin 0.01 | slop 0.005, polygon radius 0.01 | slop 0.005, speculative 0.02, margin 0.1 | slop 0.005, speculative 0.02 | **same slop** |

**What Domino has that Box3D lacks:** characters as world objects with groups, priorities and soft pushing between
teammates; per-node category masks in the mesh BVH; a fourth broadphase tree; the shoulder joint as its own type;
stepping a subset of bodies; world-level material tables for meshes; the destruction listener.

**What Box3D has that Domino lacks:** soft step with substepping, relax and restitution passes; graph colouring and
wide SIMD solving; persistent islands and solver sets; speculative contacts; bullets; compound shapes; motor, wheel,
parallel and filter joints; contact recycling; large-world double positions; cross-platform determinism; recording
and replay.

**Reading.** Domino is the bridge between the two public generations. Its step and solver are Box2D v2 in 3D. Its data
design (ids, per-type trees, multi-manifold contacts, central friction) and its tools (plane solver, baked mesh BVH,
explosion factor, soft weld) are what Box2D v3 and Box3D later published. Box3D is therefore the closest public
relative of Overwatch's physics, with a newer solver.

---

## 15. Open questions
- The world def the game passes (pool capacities, stack size): read it live (the world's pool capacities) or from the
  scene creator's caller.
- The sensor event path, and what `m_explosionFactor` drives (no engine-side explode function was found; the game may
  apply impulses itself).
- The exact friction, twist and rolling formulas and the restitution bias (§9.3), and the narrowphase deep paths
  (Movement.md §4.5); best checked by replay against the client.
- Which gameplay systems call `Scene_RayCast` / `dmWorld_QueryAABB` (46 + 6 sites), and whether any gameplay query hits
  dynamic bodies.
- What `dmWorld_StepBodies` fast-forwards (§9.5), and the game-side meaning of the model-collision mode byte and the
  global `DAT_7ff651781838 == 2` gate in `FUN_7ff6509c1d00`.
- Live check not done: read a world's body / contact counts and a prop body's state through Prometheus (`read_memory`
  on `DAT_7ff6517f8a20` → world → pools).
