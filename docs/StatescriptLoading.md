# Statescript loading: what is loaded, when, and how instances start

Which Statescript graphs the Overwatch beta 0.8 client (build 24919) has in memory while running, how
they get there, and how graph instances are created. For how Statescript itself works (nodes, states,
config vars, sync) see [Statescript.md](Statescript.md).

Related: [STU.md](STU.md) (graphs are STU assets), [ResourceLoading.md](ResourceLoading.md),
[ECS_Systems.md](ECS_Systems.md) (StatescriptSystem game #22 / lobby #16, StatescriptUpdateSystem #24,
component `0x23` = `STUStatescriptComponent`).

**Addresses**: RVA (runtime base `0x140000000`; Ghidra = `0x7ff64ffe0000 + RVA`).
**Status**: [H] code + confirmed live, [M] code or live only, [L] inferred.
**Data**: `systems/data/live_anubis/` (snapshot: Temple of Anubis match, local controller spawned, no hero
picked).

---

## 1. Summary

- Graphs are ordinary STU assets (resource type **0x01B**, GUIDs `0x058…`), handled by the "STU" resource
  handler.
- They are **not** all loaded at startup and **not** loaded at hero pick. They load **as dependencies of
  whatever asset is loaded**, recursively, through the STU resolve pass
  ([STU.md](STU.md) §2.2): hero → hero entity definition → its Statescript component's graph list → the
  graphs' sub-graphs.
- Because every hero (`STUHero`) is resident from the lobby on, **every hero's graphs are already in memory
  with no hero picked**. Map-entity graphs arrive with the map's entity definitions.
- A component's default graphs are started **synchronously** from its `OnCreate` (they must already be
  resident); any graph can also be started by GUID through an **asynchronous** path that requests and waits
  for it (what Prometheus' `play_script` uses).
- A server only needs to send graph GUIDs, instance ids and state: the client resolves the graph content
  itself.

## 2. What is loaded now  [H, live]

**11,594** STU resources are resident, among them **296 Statescript graphs**, all **21** `STUHero`,
**466** entity definitions, all **19** map headers, 22 skins, 110 loadouts, 168 hero weapons, 5017 voice
lines, 520 sounds, 361 data flows.

- All **111** graph → graph references found in the resident graphs point to resident graphs.
- The **466** entity definitions contain 205 graph references (63 definitions, 127 distinct graphs); **all
  are resolved**, i.e. dependency-loaded.
- The **21 hero** definitions all list `0x033` (health pools) + `0x04b` (base hero hub) + `0x043`, plus 3–6
  hero-specific graphs (Genji, def `0x0400000000000001`: `001a, 0024, 0034, 0033, 0043, 004b, 003d, 02cf`).
- From these roots (entity definitions + the lobby's running graphs) 237 of the 296 are reachable; **59 are
  not** (probably referenced from weapon components, loadouts, UX or game-mode STU types that were not
  scanned).

**Running instances**:
- **Lobby EA**: 25 instances (UI / front-end) on the lobby singleton entity `0x80000002` (def
  `0x04000000000001f1`), e.g. `0x41a`, `0x41b`, `0x1e5` (112 nodes), `0x2ba` (86), `0x250` (77).
- **Game EA**: the EA singleton runs none. 113 entities carry component `0x23`:
  - 87 local map entities (`0x8000xxxx`) run **90 instances of 11 graphs**: `0x0f6` ×24, `0x0d6` ×18
    (spawn points, def `0x167`), `0x1ce` ×18, `0x046` ×12, `0x042` ×5, `0x0a7` + `0x0ec` ×3, `0x028` ×2,
    `0x208` ×2, `0x2bb` ×2, `0x0dc` ×1;
  - the 22 server-created entities (`0x4000xxxx`) have the component but **run nothing**;
  - 4 local entities have the component and no graphs.
- **All instance ids are negative** (local). Local ids are allocated as `min(existing) − 1` (`0x103b750`,
  into `comp+0xAA`); positive ids would come from the server.

## 3. Loading policy  [H for the mechanism, M for when each root loads]

| content | how its graphs get loaded |
|---|---|
| heroes | `STUHero.0CD16BAA` (dependency) → hero entity definition → `STUStatescriptComponent` → `STUStatescriptGraphWithOverrides.m_graph` (dependency) → sub-graphs via `STUConfigVarStatescriptGraph.m_graph` (SubScript / PlayScript, dependency), transitively |
| map | map entity definitions (spawns, objectives, pickups, doors, …) load with the map data and pull their graphs; `STUMapHeader` references are lazy, so other maps are not pulled in |
| lobby / UI | 25 UI graphs run on the lobby singleton; what starts them was not traced |
| anything else | can be started by GUID: the instance requests the graph asynchronously and waits (§4) |

## 4. Creating an instance  [H]

```
component 0x23 vtable (RVA 0x15c5cb0)
  +0x58 LoadGraph(comp, guid, …) = 0xcca530     next negative id (0x103b750) →
        SyncMgr (comp+0xC0) slot +0x18 = 0xd13810 CreateInstance(guid, id, …)
  +0x60 variant for mirrored entities (extra prediction argument)

0xd13810: allocate 0x290 bytes → StatescriptInstance ctor 0xf4bb60;
          append to SyncMgr+0xF0 (instances) and +0x120 ({instance, id})
0xf4bb60: inst+0x150 = graph GUID, +0x158 = handle (−1), +0x18 = id
          → 0xf4dff0 RequestGraph: AcquireHandle(&inst+0x158, guid) (0x9c8330), asynchronous
            (failure: "Failed to load statescript graph %016llx")
instance poll 0xf4dd20 (every update):
          graph = handle → object; not loaded and failed → log and drop
          loaded and not yet linked → 0xf517f0 LinkGraph; then start
0xf517f0 LinkGraph (once per graph, global lock): walks the graph blob's instance table (through the STUD
          header at root−0x10), binds the runtime evaluator of every config-var-like instance via the
          StatescriptRTTI factory (0xf433a0, fallback 0xf435c0), sets graph+0x190 = 1
```

Other SyncMgr slots: +0x20 `0xd139b0` add an already-built instance, +0x28 `0xd13af0` stop an instance by id.

### Default graphs of a component
`StartDefaultScripts` (`0x103acb0`), from the component's `OnCreate` (`0xcca010`) or a deferred start
(`0xc9ab90`): runs once (`comp+0xBC`) and only when an EA-side state object is null or at state 4 (world
ready?); sends events `{0x40000, 0x317..0x31a}`; for each `GraphWithOverrides` in the Statescript component
(`+0x8`) calls `0x103aeb0`:
- local entity (`0x8000xxxx`): vtable +0x58 (local instance); mirrored entity (`0x4000xxxx`): +0x60;
- resolves the graph **synchronously** (handle → object via `0xa4b3c0`), so it must already be resident;
- links it if needed and applies the per-graph config-var overrides (`GraphWithOverrides+0x10` and the
  graph's `m_publicSchema` at +0x160) with `0x103beb0`. Example: Genji's HP override `00c7 = 200` on `0x033`.

Then the component's schema object (`+0x18`) is applied (`0x103b160`).

Live, the 22 server-created entities with component `0x23` run no instances: mirrored entities don't start
their default graphs locally in this state; the server is expected to create them (positive ids) through
Statescript sync. Which condition blocks them is still open.

## 5. What a server needs

For the match logic specifically (gamemode brain, objectives) see [MatchLogic.md](MatchLogic.md).


- To drive the client: graph GUIDs, instance ids and state (the Statescript sync). The client already has
  the graphs (§1).
- To run or verify Statescript itself: the graph data (nodes, `m_states`, `m_entries`, config vars,
  `m_syncVars` / `m_remoteSyncNodes`) and the entity-definition → graph mapping with overrides
  (`systems/data/live_anubis/def_graphs.json` covers the resident definitions). Both are STU assets: read them
  offline from CASC ([ResourceLoading.md](ResourceLoading.md) §8) and parse them with the registry
  ([STU.md](STU.md) §6), or dump them from the running client (all hero and map graphs are resident without
  playing).

## Open questions
1. Where the 59 unreferenced resident graphs come from (full reverse-reference scan over all resident STU
   objects with the registry).
2. When each group loads (lobby startup vs. map load): timestamp the STU construct (`0xa4c4e0`) against the
   load counter (`0x18a1840`) over a fresh start and a map load.
3. The gate that keeps mirrored entities from running their default graphs, and how server-created
   (positive-id) instances name their graph in the sync packet.
