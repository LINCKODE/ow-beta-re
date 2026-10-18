# Statescript runtime (client build 24919)

How the Overwatch beta 0.8 client represents Statescript at runtime: the graph asset, its nodes, plugs and
links, instances, the per-entity component `0x23` with its sync manager, variable storage, and the registry
that maps STU node types to runtime classes. Section 2 is a catalogue of every node type. It is meant as the
specification for a clean-room reimplementation (`systems/statescript`, T016).

Concepts (GDC talk: kinds, plugs, states, var bags): [Statescript.md](Statescript.md). Which graphs are
resident and how an instance starts: [StatescriptLoading.md](StatescriptLoading.md). STU format and type
registry: [STU.md](STU.md). Execution semantics (tick, plug propagation, activation): §3; live traces and their format: §4 (T015).
The C++ runtime that implements it (`systems/statescript`) and what it adds: §5 (T016).

**Addresses:** `RVA = runtime - 0x140000000`, `Ghidra = 0x7ff64ffe0000 + RVA`. Offsets are from the start of
the object.
**Status tags:** [G] read in Ghidra, [L] verified live (Prometheus MCP read-only reads of the running client,
or the graph images dumped from it), [?] inferred, not verified.
**Data:** `systems/data/live_anubis/graphs/*.mem` (the 296 resident graphs as loaded images, dumped
2026-09-25 from a Temple of Anubis match; `meta.json` = base address per graph),
`systems/data/statescript/` (node catalogue JSON + the scripts that produced it).

---

## 1. Data model

### 1.1 Overview

```
Entity ── component 0x23 (STUStatescriptComponent runtime, 0x14d0 B)
            └─ +0xC0 StatescriptSyncMgr ── owner var bag (entity-wide variables)
                  └─ instances[] ── StatescriptInstance (0x290 B), one per running graph
                        ├─ graph  → STUStatescriptGraph (immutable STU asset, shared by all instances)
                        ├─ state objects[m_states.count]   (created lazily, one per STU state node)
                        ├─ action cache[m_nodes.count]     (shared per-type singletons)
                        ├─ instance var bag
                        └─ notification queue (timers, lifetime events, set-var, …)
```

- The **graph is immutable data** (an STU asset, loaded in place, [STU.md](STU.md)). Everything that changes
  at runtime lives in the instance, its state objects and var bags.
- **Only states have per-instance objects.** Entries, conditions, actions and config vars are evaluated by a
  per-type **singleton** that holds no data (§1.8) [G][L].
- Node identity inside a graph is by index: `m_nodes` index (node `+0x68`), `m_states` index (state
  `+0xF8`), `m_remoteSyncNodes` index (node `+0x6C`). These indices are compiled into the asset [L] and are
  what the runtime (and the network sync) uses.

### 1.2 The graph: `STUStatescriptGraph` (type `0df7e24f`, 0x198 B)

Asset GUID type 0x058, resource type 0x01B. Fields from the client STU registry; names from OWLib
KnownFields (hash where unknown). Checked against all 296 resident graphs [L].

| off | field | type | meaning |
|---|---|---|---|
| +0x08 | `m_graph` | inline `STUGraph` (0x48) | editor graph: `+0x08` extents (inline `STUGraphExtents`, 4 × f32), `+0x20` `m_items` (all items: nodes + containers), `+0x30` `m_links` (every link), `+0x40` u32 |
| +0x50 | `m_nodes` | `STUStatescriptBase*[]` | every node. `node+0x68` == its index here (11,275 / 11,275) [L] |
| +0x60 | `m_states` | `STUStatescriptState*[]` | the state nodes, a subset of `m_nodes`. `state+0xF8` == its index here (5,436 / 5,436) [L] |
| +0x70 | `m_entries` | `STUStatescriptEntry*[]` | entry nodes (subset of `m_nodes`; 1,065 in total) [L] |
| +0x80 | `m_consoleCommand` | string | |
| +0x90…+0xC0 | 4 × `STUIdentifier` ref lists | | `ce243eee`, `a258a936`, `fe8f3a77`, `2aefde3f` (meaning unknown) |
| +0xD0 | `e22f7341` | inline list of `{STUIdentifier; STUIdentifier[]}` (0x20) | unknown |
| +0xE0…+0x100 | 3 × i32 lists | | `e03b7f7b`, `d9b17c50`, `229d2b04` |
| +0x110 | `3efc52de` | `STUIdentifier[]` | |
| +0x120 | `m_remoteSyncNodes` | `STUStatescriptBase*[]` | nodes synced to remote clients; `node+0x6C` == index here, −1 otherwise [L] |
| +0x130 | `m_syncVars` | inline `STUStatescriptSyncVar[]` (0x18: `+0` `STUIdentifier` var id, `+0x10` enum, `+0x14` u8) | variables in the sync protocol |
| +0x140, +0x150 | `6b840897`, `0ef4c042` | inline `STUStatescriptOperationPattern[]` (`m_operations`, i32) | |
| +0x160 | `m_publicSchema` | `STUStatescriptSchema*` (`m_entries`: `{STUIdentifier var; STUConfigVar* m_value}`) | default values that `STUStatescriptGraphWithOverrides` entries override (see [StatescriptLoading.md](StatescriptLoading.md) §4) |
| +0x168 | `m_playScriptHotReloadBehavior` | enum | |
| +0x16C, +0x170 | u32 `0bce4cb5`, `36391723` | | |
| +0x174 | `m_predictionBehavior` | enum | |
| +0x178…+0x184 | `m_nodesBitCount`, `m_statesBitCount`, `m_remoteSyncNodesBitCount`, `m_syncVarsBitCount` | i32 | index widths in sync packets |
| +0x188, +0x18C | i32 `47e36e2c`, `ac1c3244` | | |
| +0x190 | u8 `01169d71` | | **runtime "linked" flag**: set to 1 by `LinkGraph` (§1.2.1). 48 of the 296 resident graphs had it set (those that had been instantiated) [G][L] |
| +0x191, +0x192 | u8 `fe10f816`, `cd875ae9` | | |

Totals over the 296 resident graphs [L]: 11,275 nodes, 5,436 states, 1,065 entries, 10,923 links, 12,434
graph items (nodes + 1,159 containers).

#### 1.2.1 Runtime fix-up (`LinkGraph`, `0xf517f0`) [G]

Runs once per graph when the first instance starts (under a global spin lock, `0x188fcd4`). It walks the
graph's STUD instance table (through the header kept at `root-0x10`) and, for every object that is a
`STUConfigVar` (inheritance `0x1791340`), writes the config var's **evaluator singleton** into
`configvar+0x08`: exact type lookup (`0xf433a0`), otherwise the `STUConfigVar` base evaluator
(`0xf435c0`). Then it sets `graph+0x190 = 1`. Nothing else in the graph is modified: node/state indices
are already in the file.

### 1.3 Nodes

All nodes derive from `STUGraphItem` → `STUGraphNode` → `STUStatescriptBase`. Kinds are determined by
the STU inheritance chain (Prometheus `STUStatescriptBase::get_type`). Layouts from the registry; the
index fields were checked on all resident graphs [L].

**`STUGraphItem` (`11130c58`, 0x38)**: `+0x08` `m_displayName` (string), `+0x18` `m_comment` (string),
`+0x28` `m_pos` (vec2 f32), `+0x30` `m_uniqueID` (u32, copied into the state object `+0x74` [L]).
`STUGraphNode` (`8b77c103`) adds nothing.

**`STUStatescriptBase` (`a97578e7`, 0x78)**

| off | field | type | meaning |
|---|---|---|---|
| +0x38 | `m_debugText` | string | |
| +0x48 | `f8f89f89` | inline `STUStatescriptRemoteSyncVar[]` (`{STUIdentifier var; enum}`) | vars this node syncs to remote clients [?] |
| +0x58 | `a56dc772` | inline `STUStatescriptRemoteSyncVar[]` | second list (other direction or state data) [?] |
| +0x68 | `53bfb779` | i32 | **index in `m_nodes`** [L]; the per-instance action cache is indexed by it [G] |
| +0x6C | `4b062861` | i32 | **index in `m_remoteSyncNodes`**, −1 if not remotely synced (3,093 synced / 8,182 not) [L] |
| +0x70 | `efd56e99` | enum | **entity-admin scope** the node runs in: copied to SyncMgr `+0x3D0` for the running state (0 owner entity's EA, 1 game EA, 2 lobby EA, 3/4 others, §1.6) [G]. 0 on 10,842 nodes, 1: 399, 2: 2, 3: 4, 4: 28 [L]. The container type `26c93342` carries the same enum |
| +0x74 | `m_isState` | u8 | 1 exactly for the nodes in `m_states` [L] |
| +0x75 | `m_clientOnly` | u8 | copied into the state object `+0x80` [G] |
| +0x76 | `m_serverOnly` | u8 | |

Distribution of (`m_isState`, `m_clientOnly`, `m_serverOnly`) over all nodes [L]: 000 4,995; 001 501;
010 343; 100 4,780; 101 135; 110 521.

**Kinds** (the base types; per-type fields in §2):

| STU base | hash | size | adds | plugs |
|---|---|---|---|---|
| `STUStatescriptEntry` | `2f937ae3` | 0x80 | `+0x78 m_outPlug` | out |
| `STUStatescriptCondition` | `6f10080f` | 0x90 | `+0x78 m_inPlug`, `+0x80 m_truePlug`, `+0x88 m_falsePlug` | in → true / false |
| `STUStatescriptActionTerminator` | `15b44727` | 0x80 | `+0x78 m_inPlug` | in only (e.g. Stop Script) |
| `STUStatescriptAction` | `2ecefc8b` | 0x88 | (terminator) + `+0x80 m_outPlug` | in → out |
| `STUStatescriptState` | `a7213568` | 0x100 | see below | begin/abort in; onBegin/onEnd/subgraph/transitions out, plus per-type plugs (`m_onFinishedPlug`, …) |
| `138ccab0` (unnamed, direct `STUStatescriptBase` child) | | 0xA0 | `+0x78` i32[], `+0x88 m_inPlug`, `+0x90 m_outPlug`, `+0x98` i32 | in → out; 5 uses, has its own class |

**`STUStatescriptState` (`a7213568`, 0x100)**

| off | field | type | meaning |
|---|---|---|---|
| +0x78 | `m_stateGroup` | ref `STUStatescriptStateGroup` | |
| +0x88 | `m_transitionPlug` | `8f6c88d9*[]` (transition output plugs) | "Transition [n]" |
| +0x98 | `065d841c` | i32[] | |
| +0xA8, +0xB8 | `652a186c`, `3882159e` | `STUConfigVarDynamic*[]` | variables the state reads/writes (dependency lists) [?] |
| +0xC8 | `a2eca5de` | `STUStatescriptSubGraph*` | the red container the state belongs to [?] |
| +0xD0 | `m_beginPlug` | `STUStatescriptInputPlug*` | Begin |
| +0xD8 | `m_abortPlug` | `STUStatescriptInputPlugAbort*` (`e51f3e8d`) | Abort |
| +0xE0 | `m_onBeginPlug` | `f1f0a36d*` (output) | OnBegin |
| +0xE8 | `m_onEndPlug` | `STUStatescriptOutputPlug*` | OnEnd |
| +0xF0 | `m_subgraphPlug` | `24ab1f0b*` (subgraph output plug) | Subgraph |
| +0xF8 | `63d0b555` | i32 | **index in `m_states`** [L]; copied to the state object `+0x70` [G] |
| +0xFC | `36391723` | u32 | |

Containers (`STUGraphContainer` `5dbbdd70`, 0x78: `+0x38` child items, `+0x48 m_bodyText`, colours) are
graph items but not nodes; subclasses `STUStatescriptSubGraph` (`b8f35b20`, `+0x78` parent subgraph),
`STUStatescriptSubgraphClientonly` (`5732771b`), `STUStatescriptSubgraphServerOnly` (`4e5feee5`, name by CRC
match) and `26c93342` (`+0x80` the same enum as node `+0x70`). Resident: 795 plain containers, 164
subgraphs, 100 client-only, 53 server-only, 47 `26c93342` [L].

### 1.4 Plugs and links

**`STUGraphPlug` (`84201dca`, 0x28)**: `+0x08` `m_links` (`STUGraphLink*[]`), `+0x18` `m_parentNode`,
`+0x20` u8 (0 on every resident plug). **`STUGraphLink` (`38a4b0b7`, 0x18)**: `+0x08` `m_outputPlug`,
`+0x10` `m_inputPlug`.

- Links are **one-directional in the data**: every link is in its output plug's `m_links` (10,923 / 10,923);
  input plugs' `m_links` are always empty [L]. To find what drives an input plug, scan `STUGraph.m_links`.
- `m_parentNode` of every input plug and of all but 4 output plugs is a node of the same graph [L].

Plug types (`STUStatescriptInputPlug` `11d7f052` / `STUStatescriptOutputPlug` `3e490486` and subclasses;
link-endpoint counts over the resident graphs [L]):

| type | parent | extra fields | role | links |
|---|---|---|---|---|
| `3e490486` `STUStatescriptOutputPlug` | plug | | plain output (Out, True/False, OnEnd, OnFinished, …) | 6,799 |
| `24ab1f0b` | output | `+0x28` i32[] | **subgraph plug**: the list holds the `m_states` indices of the states in the subgraph (valid on all 1,181 non-empty lists; equal to the directly linked states on 1,082, a superset on 1,119 — it includes states nested in the red container) [L] | 3,251 |
| `136270d8` | `24ab1f0b` | `+0x38 m_value` (`STUConfigVar*`) | subgraph plug with a value (switch/case-like) | 171 |
| `f1f0a36d` | output | | OnBegin | 495 |
| `05db2226` → `8f6c88d9` | output | | transition plug (`m_transitionPlug`) | 91 |
| `af12fa9c` | output | `+0x28 m_command` (ref `STUUXLink`) | UX command output | 94 |
| `57e931d8` | output | `+0x28 m_value` (`STUConfigVar*`) | output with a value | 19 |
| `f26f968d`, `8e15543a` | output | | | 3, 0 |
| `11d7f052` `STUStatescriptInputPlug` | plug | | In, Begin | 10,611 |
| `e51f3e8d` `STUStatescriptInputPlugAbort` (name by CRC) | input | | Abort | 271 |
| `8dcb62ea`, `e2eb6a5d`, `af42564f`, `2309c11b`, `7426d7ef` | input | | per-node extra inputs | 20, 10, 7, 2, 2 |

### 1.5 Instance: `StatescriptInstance` (0x290 B)

Three class levels, as for the sync manager: engine base (ctor `0xf4bb60`, vtable `0x1604df0`), game
level (inlined, vtable `0x15da668`), client (ctor `0xd739d0`, vtable **`0x15da3c8`**, 0x54 slots). Created by
`SyncMgr::CreateInstance` (`0xd13810`: alloc 0x290, ctor, append to SyncMgr `+0xF0` and `+0x120`) [G].
Live values are from the door instance (§1.9).

| off | size | type | meaning | tag |
|---|---|---|---|---|
| +0x00 | 8 | vtable | client `0x15da3c8` | [G][L] |
| +0x10 | 8 | SyncMgr* | owning sync manager (= component + 0xC0) | [G][L] |
| +0x18 | 2 | i16 | **instance id**: < 0 local (allocated by the client), > 0 from the server | [G][L] (−1) |
| +0x1A | 2 | i16 | parent instance id (0 = root) | [G][L] (0) |
| +0x1C | 4 | i32 | `m_states` index of the parent's state that spawned this child | [G] `0xf70a10` |
| +0x20 | 2 | i16 | that state's serial (state `+0x44`), checked on stop | [G] |
| +0x28 | 8 | Instance* | cached parent instance (resolved from `+0x1A` through SyncMgr `+0x120`) | [G][L] (null) |
| +0x30 | 1 | u8 | depends on parent (the parent's graph must be loaded too) | [G] |
| +0x31 | 1 | u8 | nesting depth (parent + 1); teardown destroys the deepest first | [G][L] (0) |
| +0x38 | 8 | i64 | sync-manager time (ms) when the graph was instantiated | [G][L] |
| +0x40 | 1 | u8 | debug logging enabled | [G] |
| +0x50 | 0x10 | list<Instance*> (inline capacity 4 at `+0x60`) | `m_apCachedSubScriptInstances`: detached child instances kept for reuse, by graph GUID | [G] (string) |
| +0x90 | 2 | i16 | creator (parent) id, survives detach | [G] |
| +0x98 | 8 | VarBag* | **instance var bag** (§1.7) | [G][L] |
| +0xA0 | 0x10 | list<StatescriptState*> | **state objects, one slot per `m_states` entry**, null until first use | [G][L] (5 slots, 3 created) |
| +0xB8 | 0x10 | list<u32> | per-state flag words (same length) | [G][L] (all 0) |
| +0xD0 | 0x10 | list<impl*> | **per-node impl cache**, one slot per `m_nodes` entry (entry/condition/action singletons, §1.8) | [G][L] (28 slots) |
| +0xE8 / +0xF0 / +0xF8 | 8/8/4 | | **notification queue** head / tail / count (one doubly linked queue; node `+0x8` prev, `+0x10` next) | [G][L] (count 0) |
| +0x100 | 4 | u32 | OR of the `+0xB8` words (tested against SyncMgr `+0x368`); `+0x104` u8 dirty | [G] |
| +0x120 | 0x10 | list (16-byte elements, inline capacity 2 at `+0x130`) | per-timestamp scratch, reset when the notification time changes and on stop | [G] |
| +0x150 | 8 | GUID | graph GUID | [G][L] |
| +0x158 | 8 | handle | graph resource handle (−1 = none) | [G][L] |
| +0x160 | 8 | STUStatescriptGraph* | the graph, set by the poll (`0xf4dd20`) | [G][L] (= resident root) |
| +0x1F8 / +0x200 / +0x208 | 8/8/4 | | queue of pending var-set / override records (0x70-byte entries), processed by `0xf50770` | [G] |
| +0x210 | 0x10 | list<i32> | per-entry "fired" flags (one per `m_entries`; all set → flag 0x2) | [G] |
| +0x228 | 0x28 | embedded listener (vtable `0x1604dd8`) | graph hot-reload listener; `+0x248` = back-pointer to this instance | [G][L] |
| +0x250 | 8 | record* | one more pending record | [G] |
| +0x258 | 4 | u32 | **flags** (below) | [G][L] (`0x408001`) |
| +0x260 | 8 | STUStatescriptGraph* | cached graph pointer (cleared on hot reload) | [G][L] |
| +0x270, +0x278 | 8, 4 | | client fields, zeroed; no other use found | [G] |
| +0x280 | 8 | VarBag* | third var bag (parent = SyncMgr `+0xEA0`), only for some instances (§1.7) | [G][L] (null) |
| +0x288 | 4 | i32 | creation kind: 0 root (LoadGraph), 1 child (sub-graph / PlayScript), 2 ? | [G][L] (0) |

**Owner entity**: not stored in the instance. `CreateInstance` stores it as a sync-manager variable keyed
`{instance id, 0x1b}` of type ENTITY (default: the component's entity, `entity+0x94`); client vfunc `+0x1C8`
(`0xd747e0`) reads it back [G].

**Flags `+0x258`** [G]: 0x1 graph instantiated (with 0x8000) · 0x2 all entries fired · 0x4 stopped ·
0x8 mirrored instance (created for a server-owned entity, component vfunc `+0x60`) · 0x10 re-run
instantiate step · 0x40 set by client vfunc `+0x1C0` · 0x80 notification processed this pass · 0x100 owner
torn down · 0x200 graph reload pending · 0x400 stop requested · 0x800 hot-reload restart · **0x1000
server-apply mode** (set on mirrored instances while the sync manager applies server state, SyncMgr vfunc
`+0xF8`/`+0x100`; sub-graph states don't spawn children locally meanwhile) · 0x2000 the `bit13` argument of
LoadGraph · 0x4000 has parent · 0x10000/0x20000 shutdown in progress/done · 0x100000 runaway-script
logging (after 980 notifications in one pass) · **0x200000 predicted** (mirrored, owner entity locally
controlled, and parent predicted or `graph.m_predictionBehavior < 2`) · **0x400000 runs locally** (not
mirrored, or predicted and allowed) · 0x800000 graph has client-only nodes. The door instance
had `0x408001` = runs locally | 0x8000 | instantiated [L].

Important vfuncs (client vtable) [G]: `+0x10` re-init with id `0xf4c5c0`, `+0x20` time (SyncMgr `+0x2D0`),
`+0xC0` id, `+0xF8` ensure graph `0xf4df10`, `+0x100` request graph `0xf4dff0`, `+0x128` instantiate graph
(`0xf4e370`, client `0xd73c70`), `+0x148` enqueue notification `0xf4f420`, `+0x158` execute notification
`0xf4f5b0`, `+0x190` runs locally `0xd73be0`, `+0x1B0` stop (`0x107f140` → `0xf500e0`), `+0x1C8` owner
entity id `0xd747e0`, `+0x228` poll graph `0xf4dd20`.

**Parent / child** [G]: a sub-graph (PlayScript-like) state (client class vtable `0x1621978`; stores the
child id at state `+0xA8`) creates its child in `0x10ccc00`: reuse a cached child with the same GUID from
the parent's `+0x50` (component vfunc `+0x78` → SyncMgr `AddInstance`) or create one (component vfunc
`+0x70` → `CreateInstance`, kind 1); the owner is the parent's owner. On state begin (`0xf70a10`) it sets
the child's `+0x1A/+0x1C/+0x20/+0x31/+0x90` and flag 0x4000, pushes the input values as SetVar
notifications, instantiates and starts it. On state end (`0xf70e10`) it detaches the child and requests a
stop at the current time (unless keep-alive, state `+0xAA`). A child also publishes its parent id as a
sync-manager variable (`{id, 3, 7}`, type SS_INSTANCEID).

### 1.6 Component 0x23 and the sync manager

**Component `0x23`** (0x14D0 B, ctor `0xcc9cd0`, vtable `0x15c5cb0`) [G]:

| off | meaning | tag |
|---|---|---|
| +0x08 | owner entity (entity `+0x28` EA, `+0x94` id, `+0x98` flags; bit 19 = locally controlled) | [G][L] |
| +0x10…+0x77 | ComponentBase | [G] |
| +0xA8 | i16 **next positive (mirrored) instance id**, init 0x7F; vfunc `+0x68` picks the smallest unused id ≥ 1 | [G][L] (0x7F) |
| +0xAA | i16 **next local instance id**, init 0x8000; `0x103b750` = min(existing, 0) − 1 | [G][L] (−1) |
| +0xBC | u8 default graphs started | [G][L] (1) |
| +0xC0 | **StatescriptSyncMgr** (0xEB0 B) | [G][L] |
| +0xF70…+0x14C0 | lists / holders not identified; `+0x14B8/+0x14C0` hold a 0x4C0-byte object (vtable `0x15c5d50`) registered with the entity in `OnCreate` | [G] |

Component vfuncs: `+0x10` OnCreate `0xcca010` (starts default graphs), `+0x38` current CF `0xcca330`,
`+0x48` sync manager, `+0x58` LoadGraph `0xcca530` (local id), `+0x60` LoadGraphMirrored `0xcca5b0`
(positive id, flag 0x8), `+0x68` allocate positive id `0xcca250`, `+0x70` create child `0xcca630`, `+0x78`
add cached child `0xcca6e0` [G].

**StatescriptSyncMgr** (0xEB0 B at component `+0xC0`; ctors: base `0xf43c70` → game `0x10733b0` → client
`0xd12bd0`; client vtable `0x15ca9d0`). Despite the name it is the per-entity **Statescript runtime**: it
owns the instances, the entity var bag, the clock and the ticking-state list; the network part is the
embedded object at `+0x500`.

| off | size | meaning | tag |
|---|---|---|---|
| +0x00 | 8 | vtable `0x15ca9d0` | [G] |
| +0x10…+0xE7 | 9 × 0x18 | nine list slots (Prometheus: 7 Stack-state priority lists; contents unverified) | [G] ctor / [?] |
| +0xF0 | 0x10 | **list<Instance*>**: all instances | [G][L] (1 on the door) |
| +0x108 | 0x10 | list, use unknown | [G] |
| +0x120 | 0x10 | list<{Instance*, i16 id}>: **id → instance** (lookup `0xf45390`) | [G] |
| +0x2C0 | 1 | cleared by instance ctor/teardown, tested in the CF loop | [G] |
| +0x2C4 | 4 | **current command frame** | [G][L] (40150) |
| +0x2C8 | 8 | step window length (n × ms/CF) | [G] |
| +0x2D0 | 8 | **current time in ms** (CF-based; the instance "time" vfunc returns it) | [G][L] (642415 = 40150×16 + 15) |
| +0x2D8 | 8 | step window end = start + n×16 − 1 | [G][L] (642415) |
| +0x2E0 | 8 | **entity (owner) var bag** (§1.7) | [G][L] |
| +0x308 / +0x310 / +0x318 | | sync-manager-level notification queue head / tail / count | [G] |
| +0x358 | 1 | torn down | [G] |
| +0x359 | 1 | needs processing | [G] |
| +0x35A | 1 | inside RunCommandFrames | [G] |
| +0x360 | 8 | **ms per command frame = 16** | [G][L] |
| +0x368 | 4 | state-flag mask of this pass (vs instance `+0x100`) | [G] |
| +0x374 | 1 | shutting down | [G] |
| +0x375 | 1 | executing (instances created now are instantiated immediately) | [G] |
| +0x376 | 1 | has client-only scripts | [G] |
| +0x377, +0x378, +0x380 | 1, 4, 0x10 | Prometheus: latest logical button (down flag, id) and logical button list; `+0x378` initialised to 0xC3 | [G] ctor [L] (0xC3) / [?] meaning |
| +0x3C8 | 4 | hot reload pending | [G] |
| +0x3D0 | 4 | **EA scope** for the running state (from node `+0x70`!): 0 owner entity's EA, 1 game EA (`0x1817f90`), 2 lobby EA (`0x1833e18`), 3 derived from 2, 4 `*(0x1817fc0)+0xB8` (vfunc `+0x230` `0xd135d0`) | [G][L] (0) |
| +0x3E0 | 0x10 | **list of ticking states** (each gets vfunc `+0x130` per update) | [G] |
| +0x410 | 0x10 | list, meaning unknown (Prometheus: config-var-dynamic callbacks) | [G] ctor |
| +0x448 | 4 | owner entity locally controlled | [G] |
| +0x450 | 8 | component back-reference | [G][L] |
| +0x4D0 | 1 | post-update dirty | [G] |
| +0x4D1 | 1 | **in server-apply scope** (vfunc `+0xF8` set / `+0x100` clear) | [G] |
| +0x4D2 / +0x4D3 | 1 / 1 | cached result of `0xc3efa0(…, owner)` / cached flag | [G] |
| +0x500 | 0x9A0 | embedded **net-sync / prediction object** (vtable `0x15ca990`, ctor `0xd08f20`); mirrored instances are registered here | [G] |
| +0xEA0 | 8 | parent var bag for instance `+0x280` | [G] |

The node's enum at `+0x70` therefore selects **which entity admin** a state works in (0: the owner's EA,
1: game, 2: lobby, 3/4: others) [G]; 10,842 of the 11,275 resident nodes use 0 [L].

Sync-manager vfuncs [G]: `+0x18` CreateInstance `0xd13810` (guid, id, kind, mirrored, flag 0x2000, owner),
`+0x20` AddInstance `0xd139b0`, `+0x28` StopById `0xd13af0`, `+0x68` reset entity var bag `0xf44640`,
`+0xA0/+0xA8` allocate/free notification (`0xd13f40`/`0xd14200`), `+0xF8/+0x100` begin/end server apply,
`+0x1C0` update(dt) `0xd13e20` (called by StatescriptUpdateSystem with dt in ms), `+0x230` entity admin for a
scope `0xd135d0`, `+0x258` net-sync object. The command-frame loop is `0xf44ef0(this, cf, n)`.

### 1.7 Variable storage

**`StatescriptPrimitive`** (16 B: `+0x0` payload, `+0x8` extra payload (VEC3.z), `+0xC` type) [G]
(clone `0xeea470`, truthiness `0xee97b0`, to-string `0xeea070`):

| type | name | payload | truthy |
|---|---|---|---|
| 1 | BYTE (bool) | u8 | != 0 |
| 2 | INT | i32 | != 0 |
| 3 | FLT | f32 | \|x\| > ε (`0x162c6e8`) |
| 4 | INT64 (GUIDs) | u64 | != 0 |
| 5 | STRING | owned `char*` from the allocator at `0x17b9a20`, copied with a 256-char cap; deep-copied on clone, freed on overwrite | non-empty |
| 6 | VEC3 | 3 × f32 at +0, +4, +8 | any != 0 |
| 7 | INTVEC4 | 4 × i16 | != 0 |
| 8 | SS_INSTANCEID | 2 × i16 (lo, hi) | hi != 0 |
| 9 | FLT16VEC4 | 4 × 16-bit | any != 0 |
| 10 | ColorRGBA | 4 × u8 | != 0 |
| 11 | ENTITY | u32 entity id | valid id |

**Var objects.** All four var classes are STU-registered classes (they have `STUInfo`s; names are
Prometheus', the hashes are not in KnownTypes) [G]:

| class | vtable | STU hash | size | ctor | payload at +0xA0 |
|---|---|---|---|---|---|
| VarBag | `0x1607a90` | `1fa9a0d7` | 0x3D0 | `0xf61a20` | 16 buckets × 0x30 `{ptr, i32 count, i32 cap, inline}` of `{u64 id, Var*}`; `+0x3A0` bucket count (16); `+0x3B0` entity bag, `+0x3B8` instance, `+0x3C0` sync manager |
| Primitive | `0x1608248` | `0e540279` | 0xB0 | `0xf5ef40` | `StatescriptPrimitive` (default INT 0) |
| InstanceReference | `0x1607fb8` | `9da4baac` | 0xB0 | `0xf5f930` | SS_INSTANCEID (i16 pair) |
| Dictionary (array var) | `0x1607d20` | `0af10f60` | 0xD0 | `0xf60370` | `{Primitive key, Primitive value}[]` (+0xA0 ptr, +0xA8 count, +0xAC cap), `+0xB8` i32 (−1) |

Common base (0xA0) [G]: `+0x10` list of subscribed states (notified through state vfunc `+0x170`), `+0x50`
second listener list (vfunc `+0x178`), `+0x80` parent bag, `+0x88` **user** (8 B, below), `+0x90` stack-state
slot (0 = none; value then comes from a sync-manager Stack list), `+0x94` flags (init 6; bits 1–2 set on every
change = dirty; bit 0 inherited from the parent; bit 3 replaced an older var), `+0x98` **var id**.

**User** (`+0x88`, who wrote the value) [G]: `+0` i16 instance id, `+2` i16 **state index** (`m_states`
index of the writing state), `+4` u32: bits 0–6 **write priority** (a lower-priority write than the current
one is rejected, `0xf5eb00`; null user = priority 1; the parent-instance setup writes with 7), bit 7
**state-defined value** (the value is supplied by the writing state's vfunc `+0x228`, `0xf5e840`). This is
how Chase Var / Stack style states own a variable. The sync manager maps a user to its state with
`0xf453c0` (`+0x120` → instance → `+0xA0[state index]`).

**Var ids** are STU identifier GUIDs of type 0x0D8 (`0x0D80000000000xxx`), used as the full 64-bit key;
bucket = `id & 0xF`, linear search [G][L].

**Where the bags are** [G][L]:

| bag | where | id | parent / links |
|---|---|---|---|
| entity (owner) bag | SyncMgr `+0x2E0` | `0x0D8…04` | `+0x3C0` sync manager |
| instance bag | instance `+0x98` | `0x0D8…03` | `+0x3B0` entity bag, `+0x3B8` instance, `+0x3C0` sync manager |
| child bags | inside a bag, created on first write by path id `0x0D8…05` | the child id | `+0x80` parent |
| second entity bag / instance bag | SyncMgr `+0xEA0` / instance `+0x280` | | only when predicate `0xd13700` holds (a deep copy of the entity bag); purpose unknown [?] |

**Variable paths** (`STUConfigVarDynamic`, type `f8cc195d`: `+0x10` list of `STUIdentifier`s) are walked
element by element (`0xf61ec0` get, `0xf620b0` set) [G]. Special ids:

| id | meaning |
|---|---|
| `0x0D8…03` | continue in the instance bag |
| `0x0D8…04` | continue in the entity bag |
| `0x0D8…05` | child bag: the next element is the child bag's id (created on write) |
| `0x0D8…06` | dereference an InstanceReference var (next element = its id) and continue in that instance's bag (id 0: the entity bag). Prometheus' "THIS_VARBAG" is wrong |
| `0x0D8…08/09/0A` | x / y / z of a VEC3 (get → FLT; set converts the var to VEC3) |
| `0x0D8…0B` | dictionary size |
| any other | the var with that id; if it holds an ENTITY and the path continues, the walk continues in **that entity's** Statescript (SyncMgr vfunc `+0xD0`) |

A **get never creates** anything; the first **set creates** the var: InstanceReference if the value type is
8, otherwise Primitive; a var of the wrong class is replaced (subscribers carried over). Dictionaries are
created only by the array actions (var vfuncs `+0x130`/`+0x170`) [G].

**Instance id and owner are variables too** [G][L]: the owner entity is the instance-bag var
`0x0D8000000000001B` (ENTITY), written by `CreateInstance`; a child publishes its parent id as an
SS_INSTANCEID var.

Live instance bag of the door instance (§1.9): 14 Primitive vars, e.g. `…1b` = ENTITY `0x800001cd`,
`…261` = INT64 `0x030000000000014f`, `…186` = BYTE 1; entity bag: `…804` = BYTE 1 and `…317`…`…31a` =
FLT 1.0 (the four ids `StartDefaultScripts` sends, [StatescriptLoading.md](StatescriptLoading.md) §4). All
users were 0 (default writes), flags 6, buckets matched `id & 0xF` [L].

**Global variables.** Component `0x51` (StatescriptGlobalVarBag, vtable `0x15c6b38`) is **not** a var bag
but a registry `{0x0D8 id → (entity, instance id, state index)}`: states register the ids of their STU list at
`+0x100` (`0xd02f60`, unregister `0xd03140`), and `Get` (`+0x38`, `0xceb2b0`) evaluates the owning state's
config var for that id on demand. The registering state type is `a4f3be65` (PublishGlobals; brain A publishes the
match phase `0x…195`, StatescriptNodes.md §3.14). Component `0x52`
(GlobalVariableStore) is keyed by u32 and is not Statescript storage [G].

### 1.8 Node runtime classes and the StatescriptRTTI registry

**Registration** [G]: every runtime node class registers itself with a static initializer (593 of them,
e.g. `0x50be00`) that pushes a list node onto the head at `0x18f8e88`. List node `{next, record*}`;
record:

| off | field |
|---|---|
| +0x00 | factory function (allocates the runtime object and runs its constructor) |
| +0x08 | STU inheritance object of the STU type it implements (`{self, parent}`; the type's `STUInfo` follows at `+0x10`, type hash at `STUInfo+0x50`) |
| +0x10 | i32 **priority layer** (0, 1, 2) |
| +0x18 | pointer to a per-source-file anchor (records group by it: probably one per `.cpp`) [?] |

**Registry build** (`0xf43210`, at startup; result at `*(0x1854d18)`) [G]: a hash map keyed by the STU
inheritance object. For each type the record with the **highest priority wins** (ties: the later one). Then
each winner's factory is called **once** to create a **shared singleton**, except for types whose
inheritance chain contains `STUStatescriptState` (`0x1766e40`): states get no singleton.

- 593 records, **565 distinct STU types**; 28 types are registered twice or three times in different
  priority layers (e.g. `STUStatescriptStateChaseVar`: p0 `0xf51ce0`, p1 `0x106e710`, p2 `0xd00e50`). The
  layers look like code modules: p0 functions around `0xf4…–0xf5…` (the Statescript core), p1 around
  `0x10…`, p2 around `0xd0…–0xda…` (game-specific) [?].
- The object size is visible in each factory (allocator call): every entry, condition, action and config
  var class is **8 bytes (a vtable only)** except two 16-byte config vars; state classes are 0xB8 (the base)
  or larger [G].

**Lookups** [G]:

| what | function | rule |
|---|---|---|
| config var evaluator | `0xf433a0` / fallback `0xf435c0` (from `LinkGraph`) | exact STU type, else the `STUConfigVar` base evaluator; stored in `configvar+0x08` |
| action/entry/condition impl | `0xf4e1a0 (inst, node)` | cached in `inst+0xD0[node+0x68]`; exact type, else (if the node is a `STUStatescriptAction`, inheritance `0x1766cd0`) the base action singleton |
| state object | `0xf43450` (factory) from `0xf4e230 (inst, stu_state)` | exact type, else **nearest registered ancestor**; if none, a plain `StatescriptState` (0xB8, ctor `0xf47920`) |

`0xf4e230` (create state object) then: `state+0x70 = stu_state+0xF8` (index in `m_states`),
`state+0x74 = stu+0x30` (`m_uniqueID`), `state+0x80 = stu+0x75` (`m_clientOnly`), stores it in
`inst+0xA0[index]`, clears `inst+0xB8[index]`, calls vfunc `+0x1A8` if instance flag 0x8 is set (debug
names), registers the state with the sync manager (vfunc `+0x190` / `+0x1A0`) when its two virtual-base
interfaces are present, and calls state vfunc `+0x100` (init) [G]. State objects are created **on first
use**: in the live door instance below, states 0 and 3 had never been reached and were still null [L].

**Consequence for a reimplementation:** 35 state types and 25 action types that resident graphs use have
**no client class** and fall back to the base class, i.e. do nothing on the client. They are the
server-authoritative nodes: `STUStatescriptStateModifyHealth` (81 uses), `HealthPool` (20), `ApplyAura`
(34), `Invincible`, `Unkillable`, `Assist`, `CombatModFilter`, `SuppressCapture`, `Pet`, `PossessControl/View`,
`STUStatescriptActionCriteria` (377), `TrackStat` (83), `InstanceEvent` (58), `DestroyEntity` (47),
`SpawnPet`, `Resurrect`, `TeleportTo`, `Hack`, … (full list: §2, rows with `→`). Their behaviour is not in
the client at all and has to come from the GDC talk, the data and observation.

**State object base (`StatescriptState`)** [G] (ctor `0xf47920`, base vtable `0x15ca730`, 0xB8 B). It uses
virtual inheritance: `+0x08` is a vbtable pointer (`0x15e6de8`) and the two interface sub-objects (vtable
`0x15f2270`) sit at `+0xA8`/`+0xB0` in the base and **move to the end** in derived classes (e.g. `+0xF8/+0x100`
in the 0x108-byte Stack class). Those interfaces decide whether the state is registered with the sync manager
(vfunc `+0x190`/`+0x1A0` on creation).

| off | meaning | tag |
|---|---|---|
| +0x00 | vtable (per node type, §2) | [G][L] |
| +0x08 | vbtable pointer | [G] |
| +0x10, +0x20, +0x30 | intrusive links in three instance lists (`+0x168/+0x180`, `+0x198/+0x1B0`, `+0x1C8/+0x1E0`; which of each pair depends on `+0x80`); state flags 0x80 / 0x100 / 0x400 mark membership | [G] |
| +0x40 | u32 flags: 0x4 exit evaluation running, 0x8 subscribed to its variables, 0x10 exit taken, 0x20/0x40 cached STU properties (vfunc `+0x1A8`), 0x80/0x100/0x400 list membership | [G] |
| +0x44 | i16 serial (checked by child instances) | [G] |
| +0x48 | i64 **begin time** (sync-manager ms); elapsed = SyncMgr `+0x2D0` − this (vfunc `+0x190`) | [G] |
| +0x50, +0x58, +0x60 | i64, init −1 (timestamps?) | [G] / [?] |
| +0x70 | i32 index in `m_states` (= instance `+0xA0` index) | [G][L] |
| +0x74 | i32 node `m_uniqueID` | [G][L] |
| +0x78 | StatescriptInstance* | [G][L] |
| +0x80 | u8 copy of node `m_clientOnly` (selects the client-only list set) | [G][L] |
| +0x88 | array of 0x38-byte handle entries (delegate vtable `0x15fb308` at `+0x10`, i32 handle at `+0x20`) | [G] |

State vfuncs confirmed [G]: `+0xD0` check out-plugs, `+0xF8` execute (→ `+0x1D0/+0x1D8`), `+0x100` init,
`+0x130` tick (ticking list), `+0x170` variable changed, `+0x180` lost ownership of a variable, `+0x190`
elapsed, `+0x1A8` cache STU flags, `+0x1D0/+0x1D8` schedule begin / abort (a notification at now + delay,
`0xf4a9c0`), `+0x1E8` evaluate exits, `+0x218/+0x220` subscribe / unsubscribe the variables of STU lists
`+0xA8` / `+0xB8` in the instance bag, `+0x228` state-defined value. Prometheus' slots `+0x100…+0x170` in the
base vtable are return-0 stubs. Semantics: §3 (T015).

### 1.9 Live cross-check (door graph `0x0580000000000042`) [L]

Entity `0x800001cd` (map door, definition `0x040000000000005b`), component 0x23 at `0x73c66830`, read with
`read_component` / `read_memory_abs` while in the Anubis match:

| check | result |
|---|---|
| SyncMgr `+0xF0` instance list | 1 instance; instance `+0x10` = component + 0xC0 |
| instance `+0x18` / `+0x1A` / `+0x288` | −1 (local) / 0 / 0 (root) |
| instance `+0x150` / `+0x160` / `+0x260` | GUID `0x0580000000000042` / graph root `0x57400010` = the resident graph (read independently with `read_stu_object`) / same |
| instance `+0x258` flags | `0x408001` (instantiated, runs locally) |
| instance `+0x228` / `+0x248` | listener vtable `0x1604dd8` / the instance itself |
| `+0xA0` state list | 5 slots = `m_states.count`; slots 0 and 3 null (never reached), 1 `STUStatescriptStateAnim` (vtable `0x15e59a0`, p2 class), 2 `…BooleanSwitch` (`0x1605058`), 4 `…Wait` (`0x16052b0`): each vtable is the catalogue's winning class for the STU type of `m_states[i]` |
| state `+0x70` / `+0x74` / `+0x78` / `+0x80` | 1, 2, 4 / `m_uniqueID` 0x11, 0x14, 0x3a / the instance / 0 |
| `+0xB8` / `+0xD0` | 5 zero words / 28 slots = `m_nodes.count` |
| SyncMgr `+0x2C4` / `+0x2D0` / `+0x2D8` / `+0x360` | CF 40150 / 642415 / 642415 / 16 |
| component `+0xA8` / `+0xAA` / `+0xBC` | 0x7F / −1 / 1 |
| var bags | see §1.7 |

(The client reloaded the map between the first and the var-bag reads; the second read was on the new
component at `0x5e1c6830`, same entity id and graph.)

---

### 1.10 Differences from Prometheus' `Statescript.h` / `StatescriptVar.h`

For whoever maintains Prometheus (all [G]). **Fixed in Prometheus (T020, 2026-09-25)**: items 1–7, 9
and 10 are corrected in `Statescript.h` / `StatescriptVar.h` (new names below). Their users were updated too
(statescript info/window, CVD selector, RTTI registry window, `ss_serialize_local`). Item 8 needed no code
change because Prometheus has no 0x51 type; `StatescriptVar_Primitive::global_var` got a `value` alias and a
comment. Also fixed while there: `STUStatescriptState` `m_stateGroup` / `m_transitionPlug` (declared at +0x8,
really +0x78 / +0x88), the false `STUStatescriptBase +0x70 idx_in_states` (now `ea_scope`; the state index
is `STUStatescriptState::idx_in_states` at +0xF8) and `+0x6C` (`idx_in_remote_sync_nodes`).
New names: instance `parent_instance` (+0x28), `notif_head/tail/count` (+0xE8/+0xF0/+0xF8), one `flags`
field (+0x258) with the `StatescriptInstanceFlags` enum, `third_inst_varbag` (+0x280; the bogus +0x270 bag
was removed), `graph_handle`, `parent_state_index`, `parent_state_serial`, `depth`, `creator_id`,
`cached_graph`, `creation_kind`; SyncMgr `current_cf`, `time_ms`, `step_window_end`, `ms_per_cf`, 9 list
slots at +0x10; `RID_User {ss_instance_id, state_index, priority_bits}`; path id `…06` =
`StatescriptVar_INSTANCE_REFERENCE`; state `base_interface_1/2`, `serial`, `begin_time`, vfunc
`CacheSTUFlags`; `StatescriptRTTI {next, record, factory, inheritance, priority_layer, source_anchor}`.
The record sits inline behind the list node (`record == node + 0x10`, read live).
1. Instance `+0x28` is the cached **parent** instance, not a self reference; the self pointer at `+0x248`
   belongs to the reload listener at `+0x228`.
2. Instance `+0xE8`/`+0xF0` are head/tail of **one** notification queue (count `+0xF8`).
3. Instance `+0x258` is one flags field (declared twice as `script_flags`/`execution_flags`); 0x100000 is
   runaway-script logging, not "wants delete"; 0x200000 is "predicted" (see §1.5).
4. Instance `+0x270` is not a var bag; the extra bag is `+0x280`. Missing fields: `+0x158` handle, `+0x1C`,
   `+0x20`, `+0x31`, `+0x90`, `+0x260`, `+0x288`.
5. SyncMgr `+0x2C4` is the current command frame and `+0x2D0` time in ms (not a CF); `+0x10` holds 9 list slots
   of 0x18 bytes, not 7 stack states.
6. `RID_User`: `+2` is the writer's **state index**, `+4` holds the write priority (bits 0–6) and the
   state-defined flag (bit 7).
7. Special path id `0x0D8…06` dereferences an InstanceReference var ("THIS_VARBAG" is wrong); `…07` is unused.
8. Component `0x51` is a global-var registry evaluated from state config vars, not a var bag.
9. State `+0xA8`/`+0xB0` are virtual-base interface sub-objects (vbptr at `+0x08`); state vfunc `+0x1A8`
   caches STU flags (not "GetStateNameStruct").
10. `StatescriptRTTI` records are `{factory, inheritance, priority, anchor}`; the "implementation_type" is the
    priority layer.

---

## 2. Node catalogue

**[StatescriptNodeCatalogue.md](StatescriptNodeCatalogue.md)** lists all **719** STU types that are nodes
or config vars (`STUStatescriptBase` and `STUConfigVar` subclasses in the client's STU registry), per kind,
with STU hash, name, parent, STU size, own fields, the client runtime class (vtable RVA, object size,
priority layer; or the ancestor it falls back to) and how often the 296 resident graphs use it.

| kind | STU types | used in resident graphs | with own client class | resident uses |
|---|---|---|---|---|
| Entry | 3 | 3 | 3 | 1,065 |
| Condition | 5 | 4 | 4 | 991 |
| Action (incl. terminators) | 122 | 101 | 87 | 3,778 |
| State | 259 | 192 | 185 | 5,440 (4 are not in any `m_nodes`) |
| Config var | 328 | 243 | 285 | 37,876 |
| other `STUStatescriptBase` child | 2 | 1 | 1 | 5 |
| **total** | **719** | **544** | **565** | |

How it was produced (scripts in `systems/data/statescript/tools/`):
1. **Runtime classes** [G]: the 593 registration initializers were located by the xrefs to the list head
   `0x18f8e88` and decoded offline from `RE-binary/GameClientApp_dump_SCY.exetreated.exe` (capstone):
   factory, inheritance, priority. The inheritance object gives the STU type hash (`STUInfo` at `+0x10`,
   hash at `+0x50`; all 593 map to a registry type). Object size and vtable come from the factory and its
   constructor (the last vtable store to offset 0).
2. **Names**: OWLib `DataPreHashChange/KnownTypes.csv` (701 registry types). The type hash is CRC32 of the
   lower-cased name, so a further 229 were recovered by matching `prefix + one or two words` (words taken
   from KnownTypes/KnownFields/KnownEnums and the GDC notes) against the registry. Such names are
   *italic* in the catalogue: most are clearly right (`STUStatescriptStateUXDialogBox`,
   `STUConfigVarGetLocalPlayer`), but a CRC32 match on 2-word guesses can be a coincidence; 9 obvious
   collisions (kind prefix contradicts the inheritance) were dropped.
3. **Usage** [L]: the instance tables of the 296 dumped graphs (every instance carries its type's layout
   CRC, which identifies the type uniquely in the registry).

Most used node types (instances in resident graphs): SetVar 1,813; Condition `b1032daf` (the Bool
condition, one `m_condition` config var) 940; Wait 708; Entry 549; GameMessage entry 488; Criteria action
377; see the catalogue for the rest.

---

## 3. Execution (T015)

How the client runs an instance: who drives it and when, the notification queue, what a link does,
state activation, timing, variables, sub-scripts and game messages. All [G] (Ghidra, build 24919) unless
tagged; the live traces that check it are §4. RVAs as in the rest of this document; functions named in
Ghidra as `FUN_7ff6…` are `Ghidra − 0x7ff64ffe0000`.

### 3.1 The model in one paragraph

Each entity's sync manager (component `0x23` `+0xC0`) owns a **clock in milliseconds** (`+0x2D0`) that
advances in **command-frame windows of 16 ms**. Inside a window the runtime is **event driven**: every
instance has a time-ordered **notification queue** (instance `+0xE8/+0xF0/+0xF8`), and the sync manager
repeatedly jumps its clock to the earliest due notification of any of its instances and executes all
notifications due at that time, until the window end. Executing a notification may run node code:
**entries, conditions and actions run synchronously and recursively** as links are followed
(depth-first, in link order), while **reaching a state only schedules** its begin or abort as a new
notification at the current time. State begin, finish and abort then run hooks, fire the state's
output plugs (again synchronously) and schedule further work (timers, exit evaluation). Variables are
written immediately; a change wakes the states that depend on the variable through an **EvaluateExits**
notification.

### 3.2 Who drives the runtime and when

**Game and session EAs: command frames.** `StatescriptSystem` (game #22 / session #16) has a CF-listener
sub-object at `+0x38` (listener vtable `0x15c2d90`; slot 0 `OnCommandFrame(cf, flags)` = `0xc2fd80`,
slot 1 `OnPreCommandFrames` = `0xc2f940`). It is listener #5 of `CommandFrameSystem` (CommandFrames.md §3),
so Statescript runs **after** physics-area, collision-proxy, unsynchronized-mover, local-movement
reconcile and weapon-attachment CF work and **before** projectile receive, for every CF. `0xc2fd80`:

```
OnCommandFrame(cf, flags):                      // flags: 1 first CF of the render frame, 2 last
  if flags & 2: refresh local-player / world-change bits (0xc306a0)
  for comp in EA components 0x23:
    every_cf = SyncMgr::RunsEveryCF(comp+0xC0)  // 0xd13700, cached in +0x4D2/+0x4D3: true when the
                                                // owner's EA is not the game EA type (e.g. session EA);
                                                // in the game EA = 0xc3efa0(EA, owner) [?: probably
                                                // "owned/controlled by the local player"]
    if every_cf or (flags & 2 and (comp.needs_processing(+0x419 = SyncMgr+0x359)
                                   or comp.deferred(+0x504) or comp.flags(+0x42C) & world_bits)):
        queue {comp, budget = every_cf ? 16000 µs : cf_count_this_frame × 16000 µs,
               priority = every_cf or deferred ? 101 : −distance to camera (+ view-direction bonus)}
  sort the queue (0xc10970, by priority)
  run them in order: 0xce84a0(comp, cf) → SyncMgr vfunc +0x48 RunCommandFrames(cf, 1)
  after the first, a component with priority < 101 is skipped once 1.2 ms (0x162c744) of wall time
  has been spent this call: the rest are marked deferred (+0x504 = 1, +0x500 |= world bits) and run
  on the next CF with priority 101.
```

So **entities that must be exact** (priority 101) step **every CF**; all other entities with pending
work are stepped **once per render frame, on the last CF of that frame**, directly to that CF (their
clock jumps over the skipped CFs; notifications that came due in between are executed at the new
window's start time, see 3.3). A reimplementation on the server should step every instance every CF;
the per-frame batching and the time budget are client optimisations [G] (the "every CF" predicate
itself is only partly understood [?]).

**Per render frame (all EAs with component 0x23).** `StatescriptUpdateSystem` (game #24 / session #18,
`0xc2ddf0` → `0xd08590`) calls SyncMgr vfunc `+0x1C0(dt_ms)` = `0xd13e20` → `0xf45b40` for every
component with `+0x419` set: if the sync manager has **frame-ticking states** (list `+0x3E0`, count
`+0x3E8`), every instance's vfunc `+0xF8(1)` (ensure graph), then every ticking state's vfunc **`+0x130(dt
ms)`** (EA scope set from the node), then vfunc `+0x230` of each instance with a graph (client: no-op).
This is the **render-rate tick** for presentation states; it does not advance the Statescript clock.
Then `0xd08680` runs components with pending `+0x14C0` work on worker threads [?: not the
notification processing; content not identified].

**Lobby EA.** The lobby `StatescriptSystem` (`0x15c4a08`, method `0xc7ac50`) calls SyncMgr vfunc
`+0x1C8(round(time × 1000))` = `0xd13e50` → `0xf45c90` per render frame for components with `+0x419`:
every instance `+0xF8(1)`, then `0xf4eaa0(inst, t)` (per-instance processing to time t), then `+0x230`
[G]. Nevertheless the traced lobby-EA music instance (graph `0x1e5` on `0x80000002`) was stepped by
`RunCommandFrames` **once per CF** with its own CF counter (CF 1121… at ms = CF×16+15) [L]; which caller
does that for the lobby was not identified [?].

### 3.3 One command frame of one sync manager: `RunCommandFrames(cf, n)` (`0xf44ef0`)

```
RunCommandFrames(sm, cf, n):                        // client vtable +0x48 = 0xd13c20 wraps it:
  if !vfunc+0x30(): return                          //   sets "locally controlled" (+0x448, vfunc +0x220)
  sm.scope(+0x3D0) = 0                               //   from the owner entity flag bit 19, then
  sm.needs_processing(+0x359) = frame_ticking_count > 0  //   0xd13700, then this
  if hot reload pending (+0x3C8): 0xf45f10
  if cf != sm.cf(+0x2C4) or n: sm.cf = cf; start = cf × 16; end = start + n×16 − 1
                               sm.time(+0x2D0) = start; sm.end(+0x2D8) = end; first = 1
  sm.in_run(+0x35A) = 1; sm.executing(+0x375) = 1
  vfunc+0xF0: world mask +0x368 = (+0x440 | EA world-change bits) & +0x36C  // which state-flag words to
  vfunc+0x38 (local-player clock hook), 0xf460f0 (build the active-instance list +0x108: instances with  //   wake
      pending notifications, ticking states, pending records or mask hits; quiet ones are skipped),
  vfunc+0x40(n×16) (net-sync object +0x500 prepares prediction / mirrored data)
  pass = 0
  while NextTime(sm, pass, first):                   // 0xf45230, below
      if pass == 1 and list not built: 0xf45680 (build the active list)
      vfunc+0xE0(0)                                  // local-player clock bookkeeping
      0xf45e40: run the sync-manager-level queue (+0x308…) items due now (vfunc +0x218, e.g. replicated
                events), freeing each
      for inst in active list: inst vfunc +0x138(0) = 0xd73ef0 → ProcessInstance (0xf4e7f0)
      sm.mask(+0x368) = 0
      if the clock did not advance: if ++same_time_passes ≥ 20000: stop every instance (runaway), break
      pass++
  vfunc+0xE0(1); sm.time = sm.end; restore mask; vfunc+0x50(n×16); 0xf46270 (post: per-instance vfunc
  +0x230, clear instance flag 0x80, recompute +0x36C = OR of instances' state-flag words); vfunc+0x58
  sm.in_run = sm.executing = 0
```

**`NextTime` (`0xf45230`)** chooses the time of the next pass: `t = sm.time`; candidates are the end of
the CF that contains `t+1` (`((t+1)/16 + 1) × 16 − 1`), the head of the sync-manager queue, and the
head (earliest) notification of every instance that is instantiated, not stopped and not flagged 0x10;
it takes the earliest candidate ≤ window end, **clamped to ≥ t** (overdue notifications run "now"), sets
`sm.time` to it, `sm.cf = time / 16` and `sm.elapsed(+0x2C8) = time − t (+1 on the first pass)`, and
returns true while `pass == 0 or t < end`. So within a CF window the passes run at the distinct
notification times in ascending order and always once at the window's last millisecond (`cf×16+15`).
**Timing is millisecond-exact inside a CF**, not rounded to CFs: for an entity stepped every CF, a Wait of
0.1 s begun at ms 642400 fires in the pass at ms 642500 of CF 40156's window [G]. Live, timers of the
music graph ran in a pass at the first millisecond of a window (ms 21904 = CF 1369 × 16), before its
end-of-window pass [L]; a pass with nothing due runs at the window end (ms = CF × 16 + 15) [L].

**`RunCommandFrames(cf, 0)`** (same CF, n = 0) keeps the clock and runs one or more passes at the current
time: it is how work queued between CFs is flushed. Seen live right after game-message deliveries [L]
(caller not identified [?]).

**`ProcessInstance` (`0xf4e7f0`)**: clear flag 0x8000; if not stopped (0x4), instantiated (0x1) and with a
graph: if the world mask is non-zero, `0xf4d440` (for each state in graph list `+0x100` whose flag word
`node+0xFC` hits the mask and that is active (or vfunc `+0x210`): vfunc `+0x170(0)`, the "external
dependency changed" hook); then **`ExecuteDue` (`0xf4ef60`)**; if that finished, **`TickStates`
(`0xf4e880`)** and `0xf4ec70` (auto-stop), then set 0x8000.

**`ExecuteDue` (`0xf4ef60`)**: `while head && head.time ≤ sm.time`: unlink the head, execute it
(vfunc `+0x158` = `0xf4f5b0`: `+0x160` dispatch `0xf4f600`, then free unless re-queued); if the
instance got stopped meanwhile, return. **New notifications created while executing are visible to
the same loop** (a notification for "now" is inserted behind the ones already due at this time, see
3.4), so a chain of begin/finish/traverse at one timestamp completes in one pass. Guard: at 980
notifications with the same timestamp the instance logs "Logging runaway script" (flag 0x100000); above
1000 it logs "Stopping runaway script %016llx" and stops the instance. With argument 1 (client-only
mode) only notifications of client-only nodes (flag 4 or the state's `+0x80`) run.

**`TickStates` (`0xf4e880`)**: every state in the instance's **per-pass ticking list** (`+0x168`, or
`+0x180` for client-only states) that is active and was not yet ticked at this time gets vfunc
**`+0x120(elapsed ms + carried)`** (scope set from the node), then `+0x58 = now`. A state enters/leaves
this list with `0xf4ed00(inst, state, on)` (e.g. Wait with its "polled" flag, below). This is the GDC
`OnFrameTick`, called at every pass time (so at least once per CF while the instance is processed).

**Auto-stop (`0xf4ec70`)**: an instance created with flag 0x2000 (LoadGraph's `bit13`) stops itself
when it has no pending notification and no active state (vfunc `+0x1B0`).

### 3.4 Notifications

Allocated by SyncMgr vfunc `+0xA0(type)` (`0xd13f40`), freed by `+0xA8`. Common header (Prometheus
`StatescriptNotification_Base`): `+0x08` prev (towards the head), `+0x10` next, **`+0x18` i64 time (ms)**,
`+0x20` i16 instance id, `+0x22` u8 type, `+0x24` u32 sequence (SyncMgr counter `+0x320`), `+0x28` u8
flags (0x4 client-only node, 0x8 from a game message). Type-specific fields from `+0x30`:

| type | name | fields | executed by `0xf4f600` as |
|---|---|---|---|
| 0 | Lifetime | `+0x30` u16 kind (0 begin, 1 finish, 2 abort), `+0x32` u16 state | 0: create the state object if needed (`0xf4e230`), **DoBeginState**(state, 0); 1: **DoFinishState**; 2: **DoAbortState** |
| 1 | Timer | `+0x30` i32 user data, `+0x34` u16 state | if the state is active: state vfunc `+0x140(data)` (timer hook), then `+0x1E8` **evaluate exits** |
| 2 | EvaluateExits | `+0x30` u16 state | if active: `+0x1E8` |
| 3 | TraverseFrom | `+0x30` u16 node (`m_nodes` index) | the node's impl (state object for states) vfunc `+0xD0(inst, …)`: for entries = **take entry** (`0xf47800`: fire `m_outPlug`) |
| 4 | SetVar | `+0x30` i16 target instance id (0 = entity bag), `+0x34` u16 var id (low bits of the 0x0D8 GUID), `+0x40` StatescriptPrimitive value | `0xf647e0`: find/create the var in that bag, apply (var vfunc `+0x280`) |
| 5 | SetVarArray | as 4 with a `{key, value}` list at `+0x40` | `0xf64e60` |
| 6 | StopInstance | – | instance vfunc `+0x1B0` (stop) |
| 7 | LogicalButton | `+0x30` i32 button, `+0x34` u8 down | updates SyncMgr `+0x377/+0x378/+0x380`; nodes in graph list `+0xE0` that match (`0xf66640`) re-evaluate their states' exits (`+0x90` lists); states in graph list `+0xF0` get vfunc `+0x1C8(notification)` |
| 8 | AttrDependencyChanged | `+0x30` u16 state | if active: vfunc `+0x170(0)` |

**Insertion** (instance vfuncs) — all first check the instance filter `+0x178` (below) and clamp the
time to ≥ now:
- `+0x140` **sorted insert** (`0xf4f310`): walk from the tail towards the head and insert after the last
  notification with time ≤ its time: **FIFO among equal times**. Used by Lifetime (`0xf4a9c0`), Timer
  (`0xf47a50`), EvaluateExits (`0xf47b10`), TraverseFrom and the SetVars of game messages.
- `+0x148` **push at the head** (`0xf4f420`): runs before everything already due. Used by the exit abort
  (3.6) when nothing else is queued at this time.
- `+0x150` insert after a given notification. Used by the exit abort to go behind same-time
  notifications created earlier in this pass (sequence < SyncMgr `+0x320`).
- A notification refused by `+0x178` is freed at once (SyncMgr `+0xA8`).

**Instance filters** (client instance vtable `0x15da3c8`):

| vfunc | used for | returns true when |
|---|---|---|
| `+0x178` (`0xd73df0`) | queueing any notification | the instance runs locally (flag 0x400000, not in server-apply 0x1000), or is not yet instantiated, or `0xf50070`, or the notification's node is client-only |
| `+0x170` (`0xd73e90`) | scheduling a state's lifetime | same rule for the state's node |
| `+0x88` (`0xd739a0`) | following a link to a node | never for server-only nodes (`+0x76`); runs locally and not in server-apply; or the node is client-only (`+0x75`) |
| `+0x130` (`0xd73d10`) | firing a plain entry on start | client-only entry, or runs locally and creation kind ≠ 2 |
| `+0x190` (`0xd73be0`) | "runs locally" (flag 0x400000) | not mirrored (flag 0x8), or mirrored + predicted (0x200000) + `RunsEveryCF` |

So a **remote (mirrored, not predicted) instance** executes only client-only nodes; states, variables
and everything else come from the server (MatchLogic.md §9), exactly the GDC rule "remote instances don't
follow links" (Statescript.md §8.2).

### 3.5 Links: plug propagation

**`FirePlug(src, inst, node, plug, plug_offset)` (`0xf46f80`)**: for each link in `plug.m_links`, **in
array order**, `TraverseLink(src, inst, node, plug_offset, link.m_inputPlug, −1)`; returns true if any
traversal returned true. **`TraverseLink` (`0xf471f0`)**:

```
target = in_plug.m_parentNode
if inst stopped or !inst.vfunc+0x88(target): return false        // filter above
sm.scope(+0x3D0) = target+0x70
inst.last_src_node(+0x108) = node+0x68; inst.last_plug(+0x110) = plug_offset; inst(+0x118) = idx
if target is not a state:
    impl = GetImpl(inst, target)                                  // 0xf4e1a0, per-node singleton cache
    (client-only node: enter client-only scope, inst vfunc +0x198 … +0x1A0)
    return impl.vfunc+0xF8(src, inst, target, in_plug)            // runs NOW, recursively
else:
    state = inst.states[target+0xF8] or CreateState (0xf4e230)
    return state.vfunc+0xF8(src, inst, target, in_plug)           // 0xf48700: schedules only
```

Consequences for a reimplementation:
- **Actions and conditions run immediately and depth-first.** An action's execute (`0xf47740`) calls its
  `DoAction` (impl vfunc `+0x100`; SetVar: `+0x128` first = `0xf67570`), notifies the sync manager
  (vfunc `+0x158`, `0xd14a20`: records "action executed" for prediction on mirrored instances), then, if
  the node has an out plug (`STUStatescriptAction`, not a terminator), **fires `m_outPlug` (+0x80) before
  returning**. A chain SetVar → SetVar → Condition runs completely inside the traversal that reached
  its first node, before the next link of the originating plug.
- **Conditions** (`0xf47350`): evaluate (impl vfunc `+0xF0`); true → fire `m_truePlug` (+0x80), false →
  `m_falsePlug` (+0x88). The return value (used by transitions) is "something was traversed", or true
  when both plugs are unlinked.
- **States are never entered synchronously.** `State::Execute` (`0xf48700`): if the input plug is the
  **Abort** plug (`STUStatescriptInputPlugAbort`) → schedule **abort** (`+0x1D8(0)` → Lifetime 2 at now);
  else, unless the type handles the plug itself (vfunc `+0x240`), schedule **begin** (`+0x1D0(0)` →
  Lifetime 0 at now) and, if `src == this state`, set state flag 0x4 (a self-transition: skip this
  exit evaluation). Returns true. A state reached twice in the same pass gets two begin notifications;
  the second one runs the **re-enter** path (3.6).
- There is no re-entrancy guard on actions: a cycle of actions recurses until the stack ends (graphs
  avoid it; cycles through states are fine because states defer).

### 3.6 States: begin, re-enter, finish, abort, exits

State object fields as in §1.8; `+0x40` flags: **0x1 active**, 0x2 ending (inside finish/abort), 0x4
self-transition, 0x8 subscribed, 0x10 exit taken, 0x80/0x100/0x400 list membership.

**DoBeginState(state, time_offset)** (`0xf495c0`; debug text "%s: DoBeginState (%d:%d)"):
```
sm.scope = node+0x70; if node client-only: enter client-only scope
inst.state_flag_word(+0xB8)[i] = node+0xFC; inst(+0x100) |= it          // world-change dependencies
if not active:
    serial(+0x44)++ ; +0x58 = +0x60 = −1
    sm.vfunc+0x120(inst, state)             // 0xd146a0: mirrored-data begin, prediction record "begin"
    AbortExclusive(state)                   // 0xf485e0: every state index in node+0x98 (i32 list) that
                                            //   has an object is aborted NOW (DoAbortState), and their
                                            //   pending notifications at this time are dropped (0xf4fd00)
    flags = flags & ~0x10 | 0x1 ; +0x68 = 0 ; begin_time(+0x48) = now − time_offset
    vfunc+0x218                             // subscribe to the vars of node lists +0xA8 / +0xB8
    vfunc+0x160(server_apply)               // activation hook; argument = instance flag 0x1000 [?: GDC OnActivate(reason)]
    vfunc+0x108
    FirePlug(m_onBeginPlug +0xE0)           // OnBegin, synchronously
    FirePlug(m_subgraphPlug +0xF0)          // Subgraph: its links start the child states (scheduled)
    vfunc+0x110                             // type-specific begin (e.g. Wait arms its timer)
    vfunc+0x1E8                             // evaluate exits
else ("DoBeginState: Reentering"):
    sm.vfunc+0x128(inst, state)             // prediction record "re-enter"
    vfunc+0x118                             // type-specific re-enter (Wait: optional restart)
```

**DoFinishState** (`0xf499d0`, "DoFinishState (time in state %d)") and **DoAbortState** (`0xf49df0`,
"DoAbortState …") are identical except for three slots; both do nothing unless active and not ending:
```
flags |= 0x2 (ending); inst.state_flag_word[i] = 0; inst.dirty(+0x104) = 1; scope; client-only scope
sm.vfunc+0x138(inst, state)                 // 0xd14850: mirrored-data end, prediction record "end"
inst.vfunc+0x168(i, all=1)                  // 0xf4fc20: drop every queued notification of this state
AbortSubgraph(state)                        // 0xf484e0: unless in server-apply (and not client-only):
                                            //   every state index in the subgraph plug's +0x28 list
                                            //   (§1.4) that has an object is aborted NOW, recursively
vfunc +0x148 (finish) / +0x150 (abort)      // type hooks
vfunc +0x158
vfunc +0x168(reason)                        // OnDeactivate: finish 1, abort 0, in server-apply 6
vfunc +0x250 (finish) / +0x248 (abort)      // TRAVERSE_FINISHED / TRAVERSE_ABORT: the type's
                                            //   m_onFinishedPlug / m_onAbortPlug, e.g. Wait +0x110/+0x108
FirePlug(m_onEndPlug +0xE8)                 // OnEnd (after the finished/aborted plug)
if !vfunc+0x210: vfunc+0x220                // unsubscribe vars
flags &= ~(0x1|0x2); end_time(+0x50) = now
leave the instance lists (0x168/0x198/0x1C8 sets) unless the interface sub-object keeps it
```
Ordering rule: **children end before their parent's end plugs fire**, and **end plugs fire while the
state is still marked active** (flag 0x1 is cleared last).

**Evaluate exits** (vfunc `+0x1E8`, base `0xf48870`): unless an exit was already taken (0x10): run the
type's exit check (vfunc `+0xD0`; base `0xf48790`: for each **transition plug** `m_transitionPlug[i]`
(+0x88) **in order**, `FirePlug`-like traversal with index i (`0xf470b0`); the first one that traverses
something wins). If an exit was taken, the instance is running and the state is not in a
self-transition (0x4): set 0x10 and queue **Lifetime abort** for this state **at now**, at the head of
the queue (or right behind the notifications of this time created earlier in the pass), and drop a
pending Lifetime *finish* of the same state at this time (`0xf4fe30`). So a transition = "traverse the
transition plug (which schedules the target state's begin), then abort myself". Exits are evaluated on
begin, after every timer, on EvaluateExits (variable dependency changed) and on logical-button changes.

**Scheduling helpers**: `0xf4a9c0(state, kind, delay)` = Lifetime(kind) at now + delay (vfunc `+0x1D0`
begin / `+0x1D8` abort use it with delay 0; "finish after" uses kind 1); `0xf47a50(state, time, data)` =
Timer at an absolute time; `0xf47b10(state, delay)` = EvaluateExits (vfunc `+0x178`, the var-dependency
callback, calls it with 0 when the state is active and the instance not torn down).

**Example: Wait** (vtable `0x16052b0`; STU `+0x100 m_timeout`, `+0x108 m_onAbortPlug`, `+0x110
m_onFinishedPlug`, `+0x118 1bff3867` (restart on re-enter), `+0x119 c5a427fa` (polled)):
- begin (`+0x110`, `0xf52f00`): `t = round(timeout × 1000)` ms; if `t ≤ 0`: schedule finish now; else if
  not polled: **Timer at now + t** (user data 1); if polled: join the per-pass ticking list;
- tick (`+0x120`, `0xf53120`, polled only): `if now − begin_time ≥ t: schedule finish now`;
- re-enter (`+0x118`, `0xf53030`): if restart: remove its pending Timer (type 1, data 1) and begin again;
- timer (`+0x140`): finish; finished → `m_onFinishedPlug`, aborted → `m_onAbortPlug`, then OnEnd.

### 3.7 Instance start: entries

`InstantiateGraph` (`0xf4e370`, client wrapper `0xd73c70` first registers mirrored instances with the
net-sync object): flags |= 0x8001, `+0x38` = now; destroy old state objects (reason 2), size the state
and impl lists to the graph; set "runs locally"; if it has a parent, write the parent id var `{id, 3, 7}`
(SS_INSTANCEID). Then, **for each entry in `m_entries` in array order** that passes filter `+0x130` and
whose impl is the **plain `STUStatescriptEntry`** class (GameMessage and PlayScript entries wait for
their trigger): mark it fired (`+0x210[i] = 1`; flag 0x2 when all are) and queue **TraverseFrom(entry)
at now** (sorted, so FIFO in entry order). Nothing runs inside `InstantiateGraph`: the entries fire in
the instance's next processing pass (the same CF if the sync manager is executing, `+0x375`, otherwise
the next CF in which it is stepped).

### 3.8 Variables

- **SetVar** (`0xf67570`) writes **immediately** through the instance bag (`+0x98` vfunc `+0xE0`) with
  user `{0, 0, priority 1}`; indexed/keyed forms go through `0xf67a00` / `0xf676d0` / vfunc `+0x130`.
  Reads (config vars, `0xf61ec0`) never create vars (§1.7).
- **Write priority** (`0xf5eb00`): a write with a lower priority than the var's current user is
  rejected; if the owner changes, the previous owning state (if active) gets vfunc `+0x180(var, user)`
  ("lost ownership").
- **Dependencies**: on begin a state subscribes to the var paths in node lists `+0xA8` and `+0xB8`
  (VarBag vfunc `+0x1B8`, `0xf63950`); a change calls the subscribed state's vfunc `+0x170`/`+0x178`;
  base `+0x178` (`0xf49520`) queues **EvaluateExits at now** if the state is active. So conditions on
  transitions are re-checked in the same pass after the write, never by polling.
- **External dependencies** (health, team, model, …): `StatescriptSystem`'s component watchers set
  world-change bits; `RunCommandFrames` intersects them with the per-state flag words (`node+0xFC`,
  graph list `+0x100`) and calls vfunc `+0x170(0)` on the affected active states (3.3).
- **SetVar notifications** (type 4/5) are used for values that must arrive at a given time: PlayScript
  inputs, game-message parameters and out-vars, replication.
- **Inheritance to sub-scripts**: a child instance's bag chain is instance bag → entity bag (§1.7); the
  parent's inputs are pushed as SetVar notifications on the child at begin (§1.5).

### 3.9 Sub-scripts (PlayScript)

As §1.5: the sub-graph state (class `0x1621978`) creates or reuses the child instance at begin
(`0xf70a10`), pushes the inputs as SetVars, instantiates and starts it (its plain entries then run in
the next pass, 3.7); at end (`0xf70e10`) it detaches the child and requests its stop at the current
time. Children are separate instances in the same sync manager, processed in the sync manager's
instance order (`+0xF0`), i.e. usually **after** their parent within a pass.

### 3.10 Game messages

A message is a record (`+0x20` message GUID, `+0x30/+0x38` parameters `{key, value}` list of 0x70-byte
entries, `+0x210` sender entity, `+0x214` instigator) posted by SendGameMessage (`0xd794f0`: to self =
every instance of this sync manager; to other entities through the target list) or by UI code.
Delivery to one instance (`0xf50770`):
```
if all entries fired: return
if the graph is not loaded yet: keep the record (0xf51480) for later
for each entry i in m_entries order, not fired (+0x210[i] == 0):
    skip unless the entry is a GameMessage entry (be49a4c2) with m_gameMessage == record.id,
         (mirrored non-owned instance: client-only entries only),
         and its m_filter (+0xB0) accepts the sender (SyncMgr vfunc +0x168)
    FireMessageEntry (instance vfunc +0x1E8 → 0xf50aa0) at time = now:
        queue SetVar(s) for the matching parameters (m_params), m_out_Sender (+0x98) = sender,
              m_out_Instigator (+0xA0) = instigator, +0xA8 = a derived value   (sorted, FIFO)
        0xf4c980: if the entry's +0xB8 flag (one-shot) → mark fired; plain non-GameMessage entries are
              always marked; queue TraverseFrom(entry) behind the SetVars
```
So the out-vars are set **before** the entry's out plug runs, in the same pass. Several matching
entries fire in `m_entries` order. A GameMessage entry without the one-shot flag stays armed.
No network message carries a game message (MatchLogic.md §8.4).

### 3.11 Where synchronized instances record changes

On the client the "sync" hooks record **predictions** for mirrored instances (flag 0x8), via the
net-sync object at SyncMgr `+0x500`: state begin (`+0x120`, record kind 0), re-enter (`+0x128`, kind 1),
end (`+0x138` → `0xce97c0`), action executed (`+0x158`, kind 0 with the action's node index); records
go into the list at `+0x5A8` with the instance id, node index and time (`0xcebdc0`), only when
`+0x5C0 < 1` (not replaying). Mirrored-data state types also register begin/end with `0xce9d50`.
Variables mark themselves dirty (var flags bits 1–2, §1.7). The server side of the sync protocol (what a
server marks and sends) is not in the client; for its wire format see MatchLogic.md §9.

### 3.12 Summary of ordering rules (for the implementation and the replay tests)

1. Per CF: Statescript runs as CF listener #5 (after movement, before projectiles); components in
   priority order; per component `RunCommandFrames(cf, 1)`.
2. Within a CF window: passes at each distinct due time (ascending), plus one at `cf×16+15`; per pass
   the sync-manager queue, then instances in sync-manager order (`+0xF0`, creation order), each
   executing all due notifications then ticking its per-pass ticking states.
3. Within an instance: notifications by time, FIFO among equal times, except exit aborts which jump
   ahead; notifications created while executing join the same pass if due.
4. Links: plug links in array order; actions/conditions run depth-first immediately; states are
   scheduled at now (FIFO).
5. Begin order: exclusive aborts → OnBegin plug → Subgraph plug → type begin → exits.
   End order: drop the state's notifications → abort subgraph children → hooks → finished/aborted plug →
   OnEnd plug → inactive.
6. Start: plain entries in `m_entries` order, each as a TraverseFrom at the start time.
7. Game messages: parameter SetVars, then the entry's TraverseFrom, all at the post time.

---

## 4. Traces (T015)

### 4.1 Recording: Prometheus MCP tool `ss_trace`

`prometheus/Prometheus-Core/MCP/SsTrace.cpp` hooks (MinHook, installed on the first `start`, recording
gated by a flag): `RunCommandFrames` `0xf44ef0`, notification execution `0xf4f5b0`, `DoBeginState`
`0xf495c0`, `DoFinishState` `0xf499d0`, `DoAbortState` `0xf49df0`, `TraverseLink` `0xf471f0`,
`InstantiateGraph` `0xf4e370`, game-message delivery `0xf50770`; after each `RunCommandFrames` (and after
instantiate) it diffs the instance bag and the entity bag of every traced instance (Primitive,
InstanceReference, Dictionary size; child bags one level deep).

```
live.py mcp ss_trace '{"action":"start","graphs":["0x0580000000000042"],"entities":[...],"all":false}'
# ... do something in game ...
ss_trace {"action":"save","path":"Z:\\...\\raw.jsonl","since":0}      # JSON lines, written by the game
ss_trace {"action":"stop"} | {"action":"status"} | {"action":"fetch","since":N,"limit":200}
systems/statescript/tools/ss_fixture.py raw.jsonl <entity> fixture.jsonl --graph <guid> --note "..."
```
A raw line: `{seq, kind, cf, ms, ent, inst, a, b, c, s}` (cf/ms = the owner's sync-manager clock at the
event). `ss_fixture.py` keeps one entity and decodes the fields (its docstring is the reference):

| kind | fields | meaning |
|---|---|---|
| `cf` / `cf_end` | `cf`, `n`, `instances` | `RunCommandFrames(cf, n)` of the sync manager starts / ends (`n = 0`: flush at the current time) |
| `instantiate` / `_done` | `guid`, `parent`, `flags` / `flags`, `queued` | graph instantiated (3.7); `queued` = notifications pending after it |
| `notif` | `type`, `time`, `text` | a notification executes (§3.4); `time` = its scheduled ms, `ms` = when it ran |
| `begin`, `reenter`, `finish`, `abort` (+`_done`) | `state`, `node` | `m_states` / `m_nodes` index |
| `link` / `link_done` | `src`, `plug`, `idx`, `dst` / `dst`, `ret` | `TraverseLink`: source node, **plug = byte offset of the plug field in the source STU node** (0x78 entry out, 0x80 action out / condition true, 0x88 condition false, 0xE0 OnBegin, 0xE8 OnEnd, 0xF0 Subgraph, type fields beyond 0x100), transition index or −1, target node; `ret` = the target's return value |
| `message` | `id`, `result`, `sender`, `instigator` | game message delivered to the instance (`result` 1 no entry, 2 entry fired) |
| `var` | `bag`, `id`, `value` | value changed during the CF (`id` = low 48 bits of the 0x0D8 GUID; `value` "TYPE payload" or null when removed) |

Graph structure for decoding node indices: `graph_<guid>_dump.json` (Prometheus `dump_script`: nodes,
types, plugs and links) and the graph images in `systems/data/live_anubis/graphs/`.

#### 4.1.1 Driving button input: MCP tool `logical_button` (T042)

`prometheus/Prometheus-Core/MCP/LogicalButton.cpp`. Presses logical buttons without the keyboard and without
the game window in the foreground, so hero recordings are repeatable:

```
live.py mcp logical_button '{"action":"list"}'                    # 195 named ids: command_bit / action_code / axis
live.py mcp logical_button '{"action":"tap","button":"fire","hold_ms":200}'     # name, id or "0x5d"
live.py mcp logical_button '{"action":"press","button":"jump"}'   # held until release
live.py mcp logical_button '{"action":"release","button":"jump"}'
live.py mcp logical_button '{"action":"status"}'                  # held buttons + input-tick diagnostics
live.py mcp logical_button '{"action":"tap","button":"reload","mode":"notify","entity_id":"0x40000018"}'
```

**How a key becomes a LogicalButton notification** [G, L]:
1. Comp 0x50 (lobby singleton, input) holds per-button bytes: `[tick*0xC3 + 0x148 + id]` held,
   `[tick*0xC3 + 0x2CE + id]` went down this frame (`tick` = `+0x40`, 0/1).
2. `LocalPlayerInputSystem` tick `0xc9df40` (per entity admin that has it; `sys+8` = the EA) fills the
   local player's comp 0x2f command, unless the input manager (`*0x18a6688`) `+0x95a` says input is
   captured (game window unfocused, UI; recomputed inside the tick by `0xc9ece0`) and comp 0x50 `+0x5e5`
   ("sample anyway") is 0:
   - `0xc9e580` → **button mask** `+0x147a` (u16) from the is-held query `0xcec330`: fire 0x5d → 0x1,
     secondary_fire 0x65 → 0x2, hold_weapon_view 0x73 → 0x4, interact 0x66 → 0x8, ability_1..3 0x77..0x79 →
     0x10/0x20/0x40, use_item 0x7a → 0x80, jump 0x42 → 0x100, crouch 0x4c → 0x200 (also a toggle via
     `+0x14a0`), ping_self/world/social 0x7b..0x7d → 0x400/0x800/0x1000, 0x8000 = gamepad.
   - `0xc9e0e0` → **action code** `+0x147c` (u8): the first entry of the table at `0x173e680` (45 × {u8 code,
     i32 button}) whose button *went down* this frame: equip_weapon_0..9 (1–10), next/previous_weapon
     (11/12), reload 0x50 (13), quick_weapon 0x71 (14), weapon_view (15), emote..emote9 (16–24), spray (25),
     open_map (26), spectate_* (27–42), hero_select (43), help (44).
   - `0xc9e6f0` → movement axes `+0x1478/+0x1479` from move_forward 0x3d / mouse_forward 0x3e /
     move_backward 0x3f / move_left 0x40 / move_right 0x41.
   - Copied / ORed into the accumulated command `+0x1464..+0x146c`: the wire command
     (`server/inst/wire/input.go`: aim ×2, move ×2, u16 buttons = mask, u8 "extra" = action code, u16).
3. The hero's Statescript gets **LogicalButton notifications** (type 7, flags byte 1, time = the start of the
   command frame, `CF × 16`) on a **per-button subset of its instances** (Genji, instance ids of the
   start-up: fire → 3 5 6 7 9 11; secondary/abilities/reload/interact → 3 5 6 7 9; jump → 3 4 5 6 7 9;
   next_weapon down → 3 5 6 7 8 9, up → 3 5 6 7 9; ping_self → 3 5 6 7 9 20) [L]. Mask buttons: down at the
   first CF, up at the CF the bit clears. Action-code buttons: **down and up in the same CF** (a one-shot
   press). Which component produces them and how it picks instances is not found yet (the allocator
   `0xd13f40` is only called through the sync-manager vtable) [?].

**Mode `input` (default)** replaces `0xcec330` (without calling the original: its integrity check faults on a
return address outside the game's code) so injected buttons read as held, and hooks the input tick: while a
button is injected it clears `+0x95a` and sets comp 0x50 `+0x5e5` for the duration of the tick (unfocused
window works), and on the first tick that runs for the local player's comp 0x2f it sets the button's
went-down byte (one edge per press, so action codes fire once). Everything downstream is the client's own
path, including the command sent to the server. Verified [L] (2026-09-26, Genji on the instance server,
Temple of Anubis, `ss_trace` on the body): fire, secondary_fire, reload, ability_1/2/3, jump (tap and
press/release), interact, ping_self, next_weapon, quick_weapon produce notifications whose instance sets,
flags, CF-aligned times and same-CF pairs match the keyboard recordings of T016
(`fixtures/raw_hero_*.jsonl`); hold times map to CFs (200 ms → 12–13 CF, 1 s → 66 CF).

**The is-held replacement is switched on only while a button is injected** (T051): kept installed for good it
broke the hero-select screen's hero preview (no model until the client restarted) [H]; the input tick hook now
turns it off at the first tick with nothing injected.

**Mode `notify`** is the Fake LogicalButton window's path (`statescript_fake_logicalbtn.h`): it queues the
notification (flags 1, CF-aligned time; tap = down now + up after `hold_ms`, sorted insert `+0x140`) on
**every** instance of the entity's Statescript component (default: the local controller's hero via comp
0x20). Local only (no command, the server never sees it), any button id, but a superset of the real
recipients (22 instances instead of 5–6).

**Seen with mode `input` on the instance server, not in the T016 keyboard recordings (R&D server)** [L]: every
press is followed 20–40 CF later by a second copy of its transitions (a same-CF down+up pair, or during a
long hold a second down / second up). **Explained (T041)**: it is the client's **prediction replay** after the
server acknowledges input (`0xd0b420` re-emits the button edges of every history entry after the acked frame
[G]), and ~46–78 CF after the press the unconfirmed prediction is dropped: vars and states return to the last
server state (the ammo `$35` 21 → 24) [L]. A server that confirms the press (§5.10, T051) removes both. The
producer and the routing are §5.10.2. Fixtures cut the comparison before the replay (`tools/hero_fixture.py`).
**Explained (T051, §5.9) [G]:** it is the client's prediction timeout (`cea070`): 40 CF after its oldest
unconfirmed prediction it rolls back to the last server state and replays the newer stored input, which
re-runs part of the press; a server that sends rollback deltas every tick replaces this.

### 4.2 Fixtures (`systems/statescript/fixtures/`, 2026-09-25, Temple of Anubis via our game server) [L]

| file | what | events |
|---|---|---|
| `trace_door_042_start.jsonl` | door `0x800001cd`, graph `0x042`: instance start during the map load | 63 |
| `trace_door_042_entry0.jsonl` | same door: GameMessage entry node 0 run with Prometheus `execute_node` (open the door) | 29 |
| `trace_music_1e5.jsonl` | lobby-EA entity `0x80000002`, graph `0x1e5` (music controller), map load + ~70 s: game messages, timers, state changes, every CF step | 11,622 |
| `raw_mapload.jsonl`, `raw_door_entry0.jsonl` | the raw saves (all five door instances + music) | |
| `trace_playscript_216_046.jsonl` | entity `0x80000366` (def `0x0fc2`), map load 2026-09-26 (T016): graph `0x216` (instance −1) PlayScripts `0x046` onto its own entity (instance −2), then StopScript | 57 |
| `trace_mover_208_start.jsonl` | mover `0x80000200` (def `0x2ed`), same map load: graph `0x208`, a Stack state and a BooleanSwitch subscribed through `$702` (the capture point) | 3,145 |
| `raw_stage2_mapload.jsonl` | the raw save of that map load (entities `0x80000366`, `0x80000200` and the capture point `0x800001cb`, graph `0x028`) | |
| `graph_0x042_dump.json`, `graph_0x1e5_dump.json` | graph structure from `dump_script` | |

Door graph `0x042` (states: 0 = node 2 Anim "open", 1 = node 3 Anim "closed", 2 = node 6 BooleanSwitch on
var `0x192`, 3 = node 25 Wait (true branch), 4 = node 26 Wait (false branch); node 4 = plain entry, 0 =
GameMessage entry).

**Start** (`trace_door_042_start`, CF 238013), exactly as §3.7/3.5/3.6 predict:
1. `instantiate` → 13 notifications queued: the 12 override SetVars queued by LoadGraph at ms 3807935
   (flags 9 = client-only | from-message) and the entry's TraverseFrom at ms 3808208.
2. The SetVars run first (overdue, executed at 3808208), then `traversefrom node 4`.
3. Entry 4's out plug (0x78) has links → 5 and → 15: **5 (SetVar) runs, and its out plug → 6 is followed
   before the entry's second link** (depth-first); reaching 6 (a state) only queues `lifetime begin state 2`.
4. `begin state 2` (BooleanSwitch): condition false → **false-subgraph plug (0x120)** → Wait 26: queued
   begin; Wait with timeout 0 → `lifetime finish state 4` queued at the same ms and run in the same pass;
   finish → `m_onFinishedPlug` (0x110) → node 3 → `begin state 1` → OnBegin (0xE0) → 23 (Condition,
   `ret 0`: nothing traversed) and 27 (Effect).
5. `var` diffs at the end of the CF: `0x192 = FLT 0`, override values, entity-bag values `0x317…0x31a`.
All of it happened at one timestamp (ms 3808208) in one CF.

**Open** (`trace_door_042_entry0`): `execute_node` ran the entry chain 0 → 1 (condition true) → 7 (SetVar
`0x192 = 1`) → 9 synchronously, stamped with the door's **stale clock** (CF 238018: the door had not been
stepped since; quiet components are not stepped, §3.2). The var change queued a **Timer (data 1) on the
BooleanSwitch** at that time; at the door's next step (CF 241910, the current frame) it ran overdue:
`abort state 1` (the closed Anim, a child of the false branch, aborted synchronously) → true-subgraph plug
(0x118) → Wait 25 begin/finish at the same ms → `begin state 0` (open Anim) → OnBegin → 8 (Effect).

**Music** (`trace_music_1e5`): stepped every CF; game messages at map load fire GameMessage entries
(`message` result 2 → SetVar out-var at the post time → TraverseFrom entry, §3.10); a Timer on state 11
traverses a type-specific plug (0x100) of node 57, which reaches an abort of state 15 and a begin of
state 10 in the same pass; later timers finish state 10 (ms 51887). Timers ran at window-start
milliseconds (ms 21904) as well as window ends (§3.3).

### 4.3 What a replay has to reproduce

For each fixture: load the graph (image or dump), create the instance(s) with the recorded overrides,
drive the sync manager with the recorded `cf`/`n` steps (and inject `execute_node` / game messages at the
recorded points), and compare the sequence of `notif` (type, time), `begin`/`finish`/`abort`, `link`
(src, plug, dst) events and the per-CF `var` values. Behaviour of node types without a client class (§1.8)
is not in these traces; the traced graphs only use client-implemented types (SetVar, Condition, Wait,
BooleanSwitch, Anim, Effect, GameMessage entry, plus `0xe4d667e6`).

Not covered yet (follow-ups): a predicted/mirrored instance (hero ability, rollback), a transition-plug
exit, logical buttons, SubScript states (child instances of a state) and the Stack state's var ownership.
PlayScript (action) and a Stack state's begin are covered by the two stage-2 fixtures (§5.6).

---

## 5. The runtime library (`systems/statescript`, T016)

A clean-room C++20 implementation of §1–3, verified by replaying the five §4 fixtures **exactly** (every
notification with its scheduled and execution time and text, every begin/finish/abort, every link with plug
offset, index and return value, every game-message result and every per-CF variable diff, 14,916 events).
Build and layout: [systems/statescript/README.md](../systems/statescript/README.md). Config vars and the two
expression interpreters: [StatescriptConfigVars.md](StatescriptConfigVars.md) (T038). Stage 1 (offline) is
done; stage 2 (more fixtures from the game) and stage 3 (sync encoders) are open (§5.8).

### 5.1 Model

| client (§1) | library | notes |
|---|---|---|
| `STUStatescriptGraph` | `ss::Graph` | loaded from CASC bytes (`stu::Mode::File`) or a client image (`Mode::Loaded`); both give the same graph (test `graph_from_casc_equals_client_image`). Nodes by `m_nodes` index with kind (entry / condition / action / terminator / state by STU inheritance), `m_states`, `m_entries`, `m_remoteSyncNodes`, `m_syncVars`, `m_publicSchema`, the world-dependency state list `0x229d2b04`. Every output plug is kept with its **field offset** and list index (what `ss_trace` records), its links in array order, and for subgraph plugs the `0xeaece4c0` state list |
| SyncMgr (component 0x23 +0xC0) | `ss::SyncManager` | owner entity, entity bag, instances in creation order, clock (CF, ms, window end), sync-manager queue (`PostEvent`), world-change bits, trace sink |
| `StatescriptInstance` | `ss::Instance` | id (local < 0 / server > 0), flags, instance bag, state objects (lazily, one per `m_states` entry), entry-fired flags, notification queue, per-pass ticking list |
| state objects | `ss::State` + types | virtual hooks named after the client vfuncs (`OnBegin` +0x110, `OnTimer` +0x140, `VarChanged` +0x170, …); `CreateState` picks the nearest modelled ancestor |
| entry / condition / action singletons | `Instance::ExecuteNode` | stateless per-type code (`nodes.cpp`) |
| var bags / vars | `ss::VarBag` / `ss::Var` | writer ("user") with priority, two subscriber lists, child bags |
| config-var evaluators | `ss::Evaluator` in an `EvaluatorRegistry` | pluggable: the host registers game-state types (§5.4) |
| component 0x23 | `ss::StatescriptComponent` | `ecs::Component` owning the sync manager of its entity (`RegisterStatescriptComponent`) |

Fidelity rule (systems/README.md): ids, indices, orders and timing are the client's; memory layout is not.
`Role::Client` applies the client's link filter (server-only nodes are never followed, as on a client that
runs the instance locally, §3.4); `Role::Server` inverts it (client-only nodes are skipped).

### 5.2 Execution as implemented

§3 as written, plus these details, read in Ghidra for the implementation [G] and confirmed by the replays [L]:

- **`NextTime` (`0xf45230`)**, exactly: `ret = pass == 0 || time < end`; `next = end`; each candidate `c`
  (the end of the CF containing `time + 1`, the sync-manager queue head, and the queue head of every instance
  with flags `& 0x8014 == 0x8000`) with `c <= next` sets `ret = 1` and `next = max(c, time)`; then
  `time = next`, `cf = time / 16`. Consequences: the first pass of a window runs at the **window start only
  if something is due by then** (overdue work, a sync-manager event), otherwise at the window end; passes
  follow at each due time; an `n = 0` flush runs one pass at the current time plus one more for every event
  queued at that time during the pass. Because every instance of the sync manager feeds the candidates, the
  pass times of one instance depend on the others (the music trace needed this, §5.6). `InstantiateGraph`
  sets `0x8001`, so a fresh instance's head counts in the same run [G][L].
- **Subscribing creates variables** (`0xf43bd0`): a state's begin subscribes the paths of node lists `+0xA8`
  (list 0) and `+0xB8` (list 1) and creates every missing var as a Primitive **INT 0** [G]. That is where the
  door's `0x2bb = INT 0` and `0x838 = INT 0` come from (the Wait and Anim states' dependency lists) [L].
- **Writes** (`0xf3f270` path set, `0xf3f650` SetVar notification): same writer and an identical value
  (`0xec9bb0`: same type and payload) change nothing and notify nobody; otherwise the value is stored,
  marked dirty, then **list-0 subscribers get vfunc `+0x170` (in subscription order), then list-1 subscribers
  get `+0x178`** (`0xf3e980`) [G]. A lower write priority than the current writer's is rejected.
- **Equality**: `0xec99c0` is the loose `==` of expressions and Switch cases (BYTE against anything by
  truthiness, INT/FLT across types within ε = 1.1920929e-05, others by payload); `0xec9bb0` the strict one [G].
- **Wait** (`0xf52f00`): `t = (int)(timeout × 1000 ± 0.5)` (half away from zero); `t × 0.001 > 0` → a Timer at
  `now + t` (or polling when `+0x119`), else a finish at now; the timer schedules a Lifetime finish at now [G][L].
- **BooleanSwitch** (`0xf55170` begin, `0xf55270` timer, `0xf55420` var changed, `0xf55640` apply) [G][L]:
  begin evaluates `m_condition` (truthiness), stores it (`+0x120`, kept across activations), then fires
  `m_onTruePlug`/`m_onFalsePlug` (0x108/0x110) and then the true/false **subgraph plug** (0x118/0x120). A change
  of a list-0 variable (or a world-change hit) queues a **Timer (data 1) at now, sorted**. The timer
  re-evaluates; if the value changed it **aborts the old branch's subgraph states synchronously** (no lifetime
  notification: the door-open trace's `abort state 1`), stores the new value and applies it.
- **Switch** (`0xf57a10` begin, `0xf57a90` timer, `0xf57bc0` var changed, `0xf58010` compute, `0xf581d0`
  apply) [G][L]: compute = evaluate `m_switchValue`; if it has a value, compare it (loose `==`) with each case
  plug's `m_value` (list `0x3fd0156c`, ≤ 64 cases) and set bit *i* for a match, stopping at the first unless
  `0xcdfceefc` (match all) is true. Apply: for each case, newly set → traverse the case plug **with its index**
  (`0xf470b0`: the trace's `plug 256 idx 4`), newly cleared → abort its subgraph states; then the default plug
  `0xf3c7685f` (0x120) fires when no case matches (once) and its states are aborted when one matches again.
  A var change queues a Timer (data 1) **at the head of the queue** (instance vfunc `+0x148`), unlike the
  BooleanSwitch.
- **Game messages**: delivered by the sync-manager queue at the start of a pass, or directly by another
  instance's action during the pass; both are modelled (`PostEvent`, `DeliverGameMessage`).
- **Cross-entity variables** [G][L]: a path through an ENTITY var (`[$702, $7e1]`) reads in that entity's
  **entity bag** (SyncMgr vfunc `+0xD0` → component `0x23` vfunc `+0x90` → `0x101bfb0` → `0xf44bb0` with
  instance id 0 = the entity bag). A state subscribing to such a path subscribes to the ENTITY var in its own
  bag and to the var in the other entity's bag; that entity's changes reach the state (mover fixture: three
  Timers on the BooleanSwitch at the start of the next CF, through the sync-manager queue [?]).
- **`RunCommandFrames(cf, 0)`** leaves the clock where it was: at the window end for a top-level flush, at the
  pass time for a flush **nested** in a running window (the PlayScript action flushes its target sync manager
  while the caller is still executing) [L].
- **Trace vocabulary**: the runtime emits the `ss_trace` events (§4.1) through `SyncManager::trace`, with the
  same field meaning, so a replay compares streams directly. The notification flag `0x1` is set on every
  notification seen live and so on every notification here; its meaning is unknown [?].

### 5.3 Instances, entries, messages

- `CreateInstance(graph, overrides, id)` = LoadGraph: a local id (`min(ids, 0) − 1`) unless given; writes the
  owner var `0x1b` (ENTITY); queues **the graph's `m_publicSchema` defaults in schema order, then the
  overrides**, as SetVar notifications at the current time with flags 9 (the door trace: 10 schema values,
  then `0x261` from the entity definition and `0x186` from the map placement) [L]; instantiates at the next
  `RunCommandFrames` (at once while the sync manager is executing).
- `Instantiate`: TraverseFrom for every **plain** `STUStatescriptEntry` in `m_entries` order at now.
- `DeliverGameMessage(msg)`: every instance in order; each unfired `STUStatescriptEntryGameMessage` whose
  `m_gameMessage` equals the id queues SetVars for its matching `m_params` (key `0xc76b6366`), `m_out_Sender`,
  `m_out_Instigator`, then its TraverseFrom; one-shot entries (`0xeb7f6ab1`) are marked fired. Result 1/2 as
  in the fixtures.
- `Instance::ExecuteEntry(node)`: Prometheus `execute_node` (fires an entry synchronously, outside the queue).
- **Schema defaults belong to the default-graph start** (StartDefaultScripts → `0x103beb0`), not to instance
  creation: PlayScript children get none [L]. A schema value whose config var yields nothing is still queued,
  as the pre-initialised **INT 0** (mover `$702`, an `STUConfigVarEntityReference`) [L].
- **PlayScript action** (`STUStatescriptActionPlayScript`, client `0xd79c90`) [G][L]: `m_script` → graph GUID,
  `m_targets` → entities (the own entity or others via `entity_resolver`), `m_params` = `{key 0x0A4…, value}`.
  On a target sync manager (`0xcaa820`: stacking `m_stackingBehavior` 0 = not while that graph runs, 3 =
  restart) it **creates a local instance at once** (next negative id, LoadGraph flag **0x2000 = auto-stop**,
  no schema, instantiated immediately since the caller is executing: `instantiate_done queued 0`), **flushes
  the target (`RunCommandFrames(cf, 0)`, nested)**, then queues the parameters as SetVars (flags 9) for the
  child's `STUStatescriptEntryPlayScript` entry (keys matched against its `m_params`, like a game message) and
  its TraverseFrom; then the action's out plug fires. Mirrored instances don't play. `SyncManager::graph_provider`
  supplies graphs by GUID. [?]: the child must survive the flush with an empty queue, so the auto-stop is armed
  after the parameters (the client's exact condition is not read); only one target entity is modelled.
- **StopScript** (terminator, `0xf68cb0`): queues a StopInstance notification at now and **returns 0**
  (`link_done ret 0`) [G][L]. **ArrayClear** (`0xf68940`): the out var becomes an empty Dictionary (instance
  bag vfunc `+0x140`, user priority 1) [G][L].

### 5.4 Config vars: the evaluator interface

Follows [StatescriptConfigVars.md](StatescriptConfigVars.md) §1 (T038). `Evaluator::Get(graph, cv, ctx, out) →
bool` is the client's `GetValue` (vtable `+0xD0`, context = the instance), `DependsOnExternal` its `+0xD8`
(BooleanSwitch / Switch states re-evaluate every pass when it is true): Dynamic = the var belongs to a state
or the path crosses into another bag/entity (`0xf5c160`); expressions = op 35 in the bytecode or any operand
answers true (`0xf6beb0`). Typed getters as in §1.3: `EvaluateBool` (truthiness), `EvaluateInt` (BYTE 0/1,
INT, FLT rounded), `EvaluateFloat` (BYTE 0/1, INT, FLT; other types → default), `EvaluateGuid` (INT64 only),
`EvaluateEntity` (ENTITY, else invalid).

`EvaluatorRegistry::Builtin()`: the **54 literal types** of T038's catalogue (`reads: literal`: GUID
references, enum/int fields, constants, Bool/Float/String/Vec2/Vec3/Color; table generated by
`tools/gen_evaluators.py` from `systems/data/statescript/configvar_catalogue.json`), `GetOwner` (ENTITY
owner), `Dynamic` (path read, §1.7; a path through an ENTITY var continues in that entity), `Expression`
(float interpreter) and `ExpressionTyped` (typed interpreter) with opcodes 0–5, 30, 31, **35** (entity
context: the next op 31 reads in that entity's Statescript through `SyncManager::entity_resolver`; an invalid
id = no entity = the instance's own read), 70, 100–106, 120, 140–145, 161–166, 200, 201, 203–205 on scalars.
Anything else yields **no value** and is counted in `SyncManager::unmodelled_config_vars` (an opcode outside
the list yields FLT 0 like the client's unknown-opcode path, with a warning). Game-state types are the host's:
`Register(type, evaluator)` or `RegisterConstant(type, value)`. T038's worked examples are unit tests: the door
expressions (FLT 0, INT 2) and `0x028` #181 `GetPvPEntity().$195 == Identifier(…1A27)` with and without a
gamemode entity. The invalid entity id: `0xFFFFFFFF` or any id whose low 29 bits are all set (the typed
interpreter's test) [?: the client's global `0x183a0c8` is set at runtime]. The other entity's read starts
in its entity bag [G] (§5.2).

### 5.5 Sync state and the C API

`Instance::SyncVars()` = the graph's `m_syncVars` that have a value in the instance; `ActiveRemoteStates()` =
active states whose node has an `m_remoteSyncNodes` index (`{remote index, state index}`); `ActiveStates()`.
C API (`capi/statescript.h`): `ss_runtime` (graphs + evaluators), `ss_sm` (a sync manager: clock,
`create_instance`, `run`, `deliver_message`, vars, `sync_vars`, `active_remote_states`), and `ss_world` (an
`ecs::EntityAdmin` whose component 0x23 is `StatescriptComponent`: `ss_world_spawn` + `ss_world_sm` give the
sync manager of an ECS entity). `tests/test_capi.c` creates the door graph's instance on an ECS entity, steps
it and reads its sync vars and active remote states (the closed Anim, remote index 1). The encoders of
`server/internal/instance/script.go` are not ported yet (stage 3).

### 5.6 Replaying the fixtures: inputs the traces do not contain

`tests/test_replay.cpp` walks each fixture, calls `RunCommandFrames(cf, n)` at every `cf` event, delivers
messages where they appear, and compares the whole stream. What it supplies itself, and why:

| fixture | input | source / status |
|---|---|---|
| door start | clock 3807935 before CF 238013 (the door had not been stepped for 18 CFs, §3.2) | trace `cf` event [L] |
| door start | entity bag `0x317…0x31a` = FLT 1 | `StartDefaultScripts` events (StatescriptLoading.md §4) [G]; the trace shows them in its first diff [L] |
| door start | overrides `0x261` = effect `0x030000000000014f`, `0x186` = BYTE 1 | `0x261`: entity definition `0x040000000000005b` (read from CASC) [L]; **`0x186`: not in the definition, presumably the map placement's overrides (roadmap #8)** [?] |
| door open | `execute_node(0)` before the first event | how the trace was made (§4.2) [L] |
| music | a **prologue** (not compared): entity bag seeded with the trace's first diff, instance −6 started at CF 1, message `0x116` at CF 100 (sets `0x8ae` = 1), stepped to CF 1120; it ends with states 0 3 4 7 11 16 active and the instance bag equal to the trace's first diff | reconstruction: the instance was running before the trace [?]; consistent with all 11,622 events |
| music | every entity-bag write is external (applied at the CF end where the trace shows it) | graph `0x1e5` writes only instance vars in this window [L] |
| music | messages `0x117` and `0xe2` carry parameter `0x0a40000000000097` = INT64 `0x080000000000005b` | the SetVar notifications they cause [L] |
| music | `STUConfigVarGetMapTease` (`5852c346`) = `0x030000000000032f` | the value written to `0xe08` [L]; game state (maps) |
| music | CF 1369: world change `0x2000` + one sync-manager event at the window start | the graph's world flags are `0x2000` on states 7 and 16, both got a Timer at ms 21904 = the window start with no variable written [L]; the pass at the window start was caused by another of the lobby entity's 17–28 instances [?] |
| music | `0x0e00b16e`, `0x63c25b10`, `0x7705e5e6` (GetOvertimeRemaining) evaluate to nothing (FLT 0 in expressions) | unmodelled game state; consistent with the trace (no branch changed) [?] |
| playscript | clock 316287 before CF 19780; entity bag `0x317` before the CF, `0x318…0x31a` written at the CF end | trace (StartDefaultScripts' values arrive over the CF: `0x317` is in the flush's diff, the others at the CF end) [L] |
| playscript | graph `0x046` available to PlayScript (`graph_provider`) | resident graph |
| mover | clock 316207; entity bag `0x317…0x31a`; override `$702` = ENTITY `0x800001cb` (after the schema's INT 0) | trace [L]; the override is map-placement data (roadmap #8) |
| mover | a sync manager for the capture point `0x800001cb` (`entity_resolver`) and **three change notifications** on BooleanSwitch state 5 at the first pass of CF 19780 | [?] the capture point's graph `0x028` changes vars the mover subscribed through `$702`; which ones is not in the trace (the capture point's own events are in `raw_stage2_mapload.jsonl`, 178 events: a candidate fixture) |

### 5.7 Explicit no-ops

Reported per run in `SyncManager::unmodelled_nodes / unmodelled_states / unmodelled_config_vars` (by STU type).
In the fixtures: `STUStatescriptActionEffect`, `STUStatescriptActionGlobalSound` (`66b5f581`),
`STUStatescriptActionDebugText` (`e4d667e6`) (presentation actions, `IsPresentationAction`),
`STUStatescriptStateAnim` and `STUStatescriptStateGlobalSound` (`f533c587`) (presentation states: plugs and
lifetime only, they never finish by themselves), and the three config vars above.

In the stage-2 fixtures also: `STUStatescriptStateStack` (begin only; its var ownership is not modelled, below),
`StateDataFlowMapping`, `StateUIInWorld`, `StateCosmeticEntity`, `StateEffect`, `STUConfigVarEntityReference`.

Since T041 implemented (§5.10): the **Stack** state and the sync manager's stack registry, **logical buttons**
(input, routing, notification), Ability, WeaponVolley, ChaseVar, Anim, BlinkEffect's trigger plug, StopAbility,
SingleRaycast (no hit) and the may-take-slot condition. (The registry insert is `0xf596e0`; `0xf56290`, cited
here before as "0xf36290", aborts the top / under subgraphs.)

Not implemented yet: the **state-defined reads** of owned vars (user bit 7, `+0x228`: a WeaponVolley's ammo, an
Ability's cooldown var, a ChaseVar's var read their stored value), SubScript states (child instances of a state,
§1.5; spec in §5.10), Watch, the LogicalButton state's own hooks, transition exits (implemented per §3.6, not
trace-verified, the exit-abort placement and where flag 0x4 is cleared are [?]), SetVarArray / array SetVar
(`m_index`/`m_key`), dictionaries (only their size), InstanceReference and entity-var paths (`0x…06`, a path
through an ENTITY var), vector opcodes and ops 32–40, message filters (`m_filter`), messages to instances whose
graph is not instantiated yet, auto-stop (flag 0x2000), mirrored / predicted instances and the server-apply
mode, the render-rate tick (`+0x1C0`), the BooleanSwitch/Switch polled ticks and abort hooks (`0xf55620`,
`0xf553c0`, `0xf57b50`, `0xf57b00`: implemented as re-evaluate / abort the active branch, not read) [?],
every node type without a client class (§1.8: the server-only logic, 60 types) and every presentation type
beyond the ones above.

### 5.8 Next

- **Stage 2** (needs the game): parts A (PlayScript, `0x216`/`0x046`) and B (Stack begin + cross-entity
  subscription, `0x208`) are recorded and replay (§5.6); the hero parts are done for Genji's primary fire, Shift
  and E (§5.10, T041). Open: other heroes and abilities, SubScript start-up. Candidate: a capture-point fixture
  (`0x028` on `0x800001cb`, already in `raw_stage2_mapload.jsonl`).
- **Stage 3**: the server's sync (sync vars, active remote states → full/delta updates): port
  `server/internal/instance/script.go`'s encoders; a Go (cgo) wrapper like `systems/stu/go`.
- Map-placement overrides (door `0x186`), `StartDefaultScripts` (default graphs of component 0x23 from the
  entity definition) and the game-state evaluators (entities, teams, map, match timers) belong to the
  server's map loading (#8) and match (#9) work.

### 5.9 Server integration (T051)

The instance server runs this library through `systems/statescript/go` (cgo, package `statescript`) in
`server/inst/sim` (docs/InstanceServer.md §9): per hero body a server-role sync manager, seeded by *importing*
the lock-in start-up (C API "server hosting": `ss_sm_import_instance` / `ss_sm_set_var` / `ss_sm_import_state`,
the client's replication path), stepped per client input frame (`ss_sm_input` → `ss_sm_run(F, 1)`), and read
back per frame (`ss_sm_instances`, `ss_sm_vars`, `ss_sm_active_states`, `ss_sm_state_data`, `ss_sm_events`,
graph layouts) into local **rollback deltas**. How the client takes them (Ghidra) [G]:
- Pending packets are applied at the start of the client's Statescript step (`ce9470` → `cec290`, called from
  RunCommandFrames' vfunc +0x40 before the pass loop): when `start <= last applied (+0x984) + 1` and
  `end > last applied`; a gap waits; the client's CF is not compared; a failed apply sets `+0x960` = 0 and all
  later packets are dropped.
- Local header (`ce89f0`): bit, local bit, **rollback bit** (`pkt+0x51`), `count(CF)` (`pkt+0x4c`; 0 = the
  client's own CF). With rollback set (`cec7b0`): every prediction record is undone newest first (`ceaf90`),
  the clock is set to CF, the packet applied (`cecd90` → `ced430` / `ced600`), then the clock is CF + 1, the
  stored input up to CF is dropped and the rest re-queued as LogicalButton events at their original times
  (`ceb420`); the step's own pass loop re-simulates to the present. No comparison with the predictions.
- **Prediction timeout** (`cea070`, every CF, locally controlled entities): when the oldest prediction record
  is 40 CF (0x28) behind the client's CF, everything is rolled back to the last server state and its input
  dropped. This is the "press undone ~46 CF later" of T041 stage 0.
- `+0x984` = the packet's end CF, `+0x980` = its payload CF (the events' time base).
Measured with the server's deltas [L]: applied every tick, `+0x960` stays 1, `+0x980` = `+0x984` = the server's
CF (docs/InstanceServer.md §9.4).

### 5.10 Hero node types, logical-button input, stacks (T041, 2026-09-27)

What a server needs to run a hero's graphs so that it arrives where the client's own prediction of a press
arrives. Read in Ghidra from the shared (prio 0/1) classes, the ones a server would run; the prio-2 client
subclasses only add presentation. Implemented in `src/states_hero.cpp` (states, the may-take-slot evaluator),
`src/runtime.cpp` (input, registry) and `src/nodes.cpp` (actions); checked by replaying the client's prediction
of real presses (§5.10.8). Tags: [G] read in Ghidra (RVA), [L] live trace, [C] content, [?] inferred.

#### 5.10.1 Why the client undoes a press

A press on a hero whose server does not confirm it: the client predicts it at once (states, vars), then
**replays** it after the server acknowledges the input frame (`0xd0b420`: the button edges of every history
entry after the acked frame are emitted again, times clamped to now) [G], and **drops** it 46–78 CF after the
press: the vars and states return to the last server state [L] (Genji fire: ammo `$35` 24 → 21 → 24). A server
that runs the same graphs from the same state and sends the result for the press's CF (T051) removes both.

#### 5.10.2 Logical-button input: producer, routing, notification

- **Producer** `EmitButtonEdges` (`0x1071e40`) [G], called by the net-sync object before the passes of every
  `RunCommandFrames` of a predicted sync manager, time = CF × 16: for each bit of the command's u16 mask
  (`server/inst/wire` `InputEntry.Buttons`) that changed against the previous command, in this order: 0x100
  jump 0x42, 0x200 crouch 0x4c, 0x1 fire 0x5d, 0x2 secondary 0x65, 0x4 0x73, 0x10/0x20/0x40 ability 1–3
  0x77–0x79, 0x8 interact 0x66, 0x80 use_item 0x7a, 0x400/0x800/0x1000 pings 0x7b–0x7d, a down or up
  notification; then, for a non-zero action code (`Extra`), a down + up pair of `table[code]` (table
  `0x173e680`, {u8 code, i32 button} per entry, by code: 1–10 → 0x53–0x5c, 11 0x5e, 12 0x5f, **13 → 0x50 (reload),
  14 → 0x71 (quick_weapon)** (the table stores those two entries in the order {14, 0x71}, {13, 0x50}; checked in the
  binary, T054), 15 0x72, 16–24 0x43–0x4b, 25 0x86, 26 0xa3, 27–42 0xb2–0xc1, 43 0xab, 44
  0xb1). Library: `SyncManager::Input(cf, buttons, action)` = C API `ss_sm_input`.
- **Routing** (`0xf446e0`) [G]: an instance gets the notification when its graph's list `e03b7f7b` (graph
  +0xE0: the `STUStatescriptCondition_LogicalButton` nodes) has a condition for that button, or its list
  `d9b17c50` (+0xF0: every LogicalButton and Ability state) has a state that accepts it: an Ability accepts every
  button; a LogicalButton state accepts its `m_logicalButton` when its down / up subgraph plug or the plug of
  this edge (`m_onGoingDownPlug` / `m_onComingUpPlug`) has links. Both lists are graph data (loader:
  `Graph::button_conditions()` / `button_states()`). Genji: fire reaches instances 3 5 6 7 9 11 [L, and the
  replay from the raw mask reproduces exactly that set].
- **Execution** (type 7, `0xf4f600`) [G]: the sync manager's held-button set (+0x380: down adds, up removes)
  is updated, then **every state of +0xF0 gets vfunc +0x1C8**, active or not (its object is created).
  Not modelled [?]: the +0xE0 conditions' exit re-evaluation (4 nodes in all resident graphs).

#### 5.10.3 The stack registry, Stack and Ability

- **Registry** (SyncMgr +0x10) [G]: per key {instance id owning the bag (0 = entity bag), var id & 0xFFFF}, the
  members bottom → top. Placement (`0xf596e0`): higher priority nearer the top; a newcomer goes **below**
  equals unless it wins ties; a replicated placement (state data / import) goes directly below its
  predecessor. After a push or pop only the top (**BecomeTop**, +0x260) and the member directly under it
  (**BecomeUnder**, +0x268) are told. A var with a slot **reads as the fold**: from its stored value, each member
  bottom → top may replace it (Stack: `m_value` if set; Ability: BYTE true); the stored value stays what the
  sync serializers send. Priorities: StackPriority resources (GUID type 0x0C4, content type 024, STU `98b439ca`:
  `m_priority` f32, `0xa6518307` wins ties), all 19 in `systems/data/statescript/stack_priorities.json` [C];
  host: `ss_runtime_set_stack_priority`. A missing record = priority 0, and an Ability then never takes its
  slot.
- **Stack** (`f7d6498c`, p0 `0x15c9ed8`) [G]: activate (+0x160, before the OnBegin plug) resolves the priority,
  pushes onto `m_out_Var`; BecomeTop fires `m_topSubgraphPlug` (aborting an active under-subgraph), BecomeUnder
  `m_underSubgraphPlug`; deactivate aborts both and pops (the new top fires its top subgraph); tick / changed
  `m_value` dependency re-fold.
- **Ability** (`f53c5da8`, p1 `0x15d81b0`; its constructor installs the Stack vtable: an Ability is a stack
  member) [G]: button downs of its start / hold / stop buttons go into a **press buffer** (window `0338e8e4`,
  0.15 s on Genji) and queue Timer(now, 1) (as does a var change); Timer 1 / 2 run **Update**: refresh the
  cooldown (remaining = cd − elapsed × rate, rate = 1 + owner `$e44`; the HUD vars `$195a..c` of ability slots
  1–3 := 2 while cooling, 1 when not), adopt a positive `m_out_CooldownVar` written by someone else (claims the
  var, keeps its stored value; **reads of the adopted var then go through the Ability: the remaining cooldown**,
  as on the client where an owned var's value comes from its owner state; T051: without it the var read its stored
  8 forever and the ability never started again) or release it at 0 (write 0, drop the fold), then **CanStart** (a buffered or held start button;
  cooldown var 0; `m_startCondition` / `84c50e4b` true; owner alive (assumed); `MayTakeSlot` for `3df852d3` /
  own priority) → stack mode: push onto `m_out_StackVar`, and on becoming top **StartAbility** (flag 1, fire the
  ability subgraph `42a043f1`, the started plug, VarToSet list `e018f16a`) — or, running, **KeepRunning**
  (hold / stop buttons, conditions, `752471c2` priority) else release. Losing the top or deactivation stops it
  (aborts the ability subgraph, stopped plug, list `b2ff48fa`). Timer 3 = stop now (queued by the
  **StopAbility** action, `0x1089c50`, on every Ability object of the instance); StopNow tests its flags as they
  were on entry, so a running top member fires its stopped plug twice [L]. A link through the **trigger input
  plug** (type `8dcb62ea`, vfunc +0x240) stops it without a begin. It never finishes by itself.
- **May-take-slot** condition (`c81f8768`: a67f0501 slot var, `m_stackPriority`): the record exists and the
  slot's holder does not outrank it [G]; evaluated live on the registry.

#### 5.10.4 WeaponVolley, ChaseVar, Anim, BlinkEffect, actions

- **WeaponVolley** (`860ee5fc`, weapon-state base `0x1077xxx`) [G]: shot *k* at begin + round(*k* / rate ×
  1000) ms (rate ramp `8bc9d90b` optional), shot 0 at begin; Timer 1 → `m_onMinShotsPlug` at shot (min − 1)
  when min < ammo / per-shot; Timer 2 at the final shot (min(max, ammo / per-shot) − 1) fires it and finishes.
  Ammo (`m_out_Ammo`), rate and pattern vars are owned while active and **released on finish / abort** with
  their final values (ammo − per-shot × (ShotIndexAt(t + 1 ms) + 1)); `2ccaffb8` := null entity per shot.
  Genji primary: 9/s, min = max = 3 → finish at +222 ms, `$35` 24 → 21 [G, L]. Aim, projectiles, effects:
  client / projectile system.
- **ChaseVar** (`970ec198`) [G]: moves `m_out_Var` from its start towards `m_destination` at `41430518` units/s
  (default 1) or over `924cebfa` s; constant scalar destination and rate → value computed on read + one Timer
  (data 0) at arrival, else ticked every pass; finish on arrival unless `9f6b675b`; finish / abort write the
  value reached back (bake). World transforms of bones / objects: host data [?].
- **Anim** (`7fb4bf64`, p1 `0x15e54d0`) [G]: ends only through the Lifetime finish its activation queues at
  begin + round(`4d475080` × 1000) ms, unless looping (`6d640a54`) or hold (`a7fbdeef`); duration 0 = the
  animation's length × speed (asset data, not modelled: finishes at begin) [L: 300 ms and 450 ms exact].
- **BlinkEffect** (`429991a7`): presentation, but a link through its trigger input plug is handled by
  +0x240 (`0xd48de0`) without a begin [G, L].
- **SingleRaycast** (`757a8a94`): world collision is the host's; modelled as "no hit" (`m_out_hitSomething` =
  false, `m_out_hitEntity` = null) [L: the dash's no-hit writes].
- **BooleanSwitch import**: the local state data (1 bit) is the switch's stored value; the client keeps the
  value it was sent even while the other branch runs, and re-applies on the next re-evaluation [L: 0x157 #33
  re-enters #2 on the fire press].

#### 5.10.5 State data (local format; `ss_sm_state_data`)

Writers modelled [G]; every other type writes back the bits it was imported with. All 108 imported Genji states
round-trip bit-exact; T051's deltas with them are accepted live [L].

| type | bits |
|---|---|
| Ability | u8 flags (0x1 running, 0x2 in stack, 0x4 top, 0x8 latched); bit cooldown [f32 cd, bit [f32 rate], count((CF + 1) × 16 − start)]; stack predecessor |
| Stack | predecessor (bit; instance id 0+4 / 10+8 / 11+16; state index in max(1, bit length(states − 1)) bits); on-top bit; under bit |
| BooleanSwitch | the stored value |
| Switch | one bit per case |
| WeaponVolley | begin as CF × 16 − begin (sign bit, then 0+8 / 10+16 / 11+32); volley counter (0 = 1, else 1 + 0+4 / 10+8 / 11+16); u6 serial |
| Anim | u3 serial; bit has-animation; [bit b, [b: time since begin, 0+8 / 10+16 / 11+32]] |
| ChaseVar | bits isVec, hasDuration, reached; f32 x [, y, z]; [bit wide, u16 / u32 remaining ms]; bit closed form [count((CF + 1) × 16 − last tick)] |

The Anim time is the `0xee4f80` varint (8/16/32), not the 4/8/16 one: getting it wrong shifts the client's parse
of the rest of the packet (T051's first live run) [G, L].

#### 5.10.6 Inputs a server has to supply

- The client's command per CF (buttons, action code) → `ss_sm_input`, then `ss_sm_run(cf, 1)`.
- StackPriority records (content).
- **Owner vars written by client code, not graphs**: at the Genji dash `$1a1` = BYTE 1, `$221` = BYTE 0, `$238` =
  FLT 0, `$656` = BYTE 1 in the window's last pass of P+1, `$656` 0 / 1 at P+19 / P+20 [L]; the dash's follow-on
  states (0x157 #0 #5, 0x1c7 #5 #6, 0x1db #24 on `$1a1`) depend on them. Presumably the movement system [?]: a
  server movement model has to write them.
- Owner health (Ability's alive checks), world collision (raycasts), entity / bone transforms (ChaseVar).

#### 5.10.7 Not modelled yet

State-defined reads of owned vars (§5.7); SubScript (spec: child created in +0x108 before the OnBegin plug, id =
smallest unused positive id for mirrored instances, init data as SetVars "flags 9", run at once; stopped at the
state's end unless keep-alive `db3a2efb`; a child that stops aborts its state unless `916d18ce`) and Watch
(activate stores the watched list; a change queues Timer 1, which writes the previous value to `b0f08764` and
fires `a415be52`); the LogicalButton state's hooks (+0x1C8 down: press time, fire onGoingDown + down subgraph
and abort the up one while active; begin with a buffered press; Timer: released → up side); DataFlowMapping is
presentation (register a data-flow answer; a server only sends it active); the Ability's owner comp 0x10 flag
0x400 branch and its server-only lists `2d76e984` / `37f2b53c` / `40f679b6`; WeaponVolley's var-changed restart.

#### 5.10.8 Fixtures and tests

`tools/hero_fixture.py` turns an `ss_serialize_local` snapshot of the idle hero + the `ss_trace` of one press into
`fixtures/hero_*.fixture` (instances, vars, active states with their data, the button notifications, the
expected transitions and var changes up to the client's replay; `--from` for a trace buffer holding earlier
events, `--extern` for vars client code writes). `tests/test_hero.cpp` imports it into a server-role sync
manager and replays the press, from the recorded recipients or from the raw input mask
(`hero_genji_fire_input`, `_dash`, `_deflect`):

| fixture | press | result |
|---|---|---|
| `hero_genji_fire` | fire 93, 100 ms, CF 29402 | 18 / 18 transitions + var changes, 0 extra; routing = recorded recipients |
| `hero_genji_dash` | ability_1 0x77 (Shift), CF 9342 | 46 / 46 (4 movement owner vars as extern inputs) |
| `hero_genji_deflect` | ability_2 0x78 (E), CF 14177 | 17 / 17 |

Raw traces and snapshots: `fixtures/raw_genji_{fire2,dash,deflect}.jsonl`, `fixtures/snap_genji_*.json`; graph
dumps `fixtures/graph_0x{15,157,1db,304,317,3a3}_dump.json`.

### 5.11 Sync-state accessors added for the server (T051)
- `ss_sm_state_changes(sm, inst, state)`: a state's lifetime changes (begin, re-enter, end). The server's local
  delta marks a state changed when this moved (or its data changed), like the client's sync records (§3.11).
- `ss_sm_state_data` also answers for an ended state (its object keeps its fields): the end of a "has update" type
  (WeaponVolley `860ee5fc`, `7bf31629`, `ffbfa88d`) carries its data (`FUN_7ff650cee430`: has-update bit, active
  bit, data read active or not, a dropped bit) [G]. Not sending that end left the client's volley active, and the
  next press re-entered it without throwing projectiles [L].
- **LogicalButton state** (`ff77458c`, T051): a down edge of its `m_logicalButton` fires `m_onGoingDownPlug` and
  swaps the up subgraph (`0x29909c7c`) for the down one (`0x8496114b`); an up edge fires `m_onComingUpPlug` and swaps
  back; on begin the subgraph of the button's current state starts. Genji's reload (weapon `0x304` node 4) needs it
  [L]; the client class was not read [?].
- **Action codes** (`ss_sm_input`): table `0x173e680` read live: code = index, 13 = reload `0x50`, 14 = quick_weapon
  `0x71` (they were crossed before T051).
