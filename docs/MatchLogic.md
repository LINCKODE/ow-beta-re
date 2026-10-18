# Match logic: where the gamemode and objective logic lives

Is the match logic (gamemode, objectives, their Statescript) already loaded on the client when it joins
a game server, or does the server have to make the client load it? Findings for Temple of Anubis
(Assault) in the Overwatch beta 0.8 client (build 24919).

Related: [StatescriptLoading.md](StatescriptLoading.md) (how graphs load and instances start),
[STU.md](STU.md), [ECS_Components.md](ECS_Components.md), `server/internal/instance` (our game server).

**Status**: [L] measured live, [G] read in code, [?] inferred / to be confirmed.
**Setup**: client joined our game server (`!instance`, Temple of Anubis `0x080000000000005B`), `/setup`
run (controller + the map's networked entities spawned), no hero picked. Snapshot data:
`systems/data/live_anubis/`.

---

## 1. Answer

**The match logic is already loaded on the client.** It waits for the server to create and drive it; the
server does not have to make the client load anything:

- the **objective entities** (capture points) are **local map entities** that already exist and run
  their graph;
- the **gamemode "brain"** is an entity **definition** that is resident together with all its graphs,
  but no entity has been created from it: the server has to spawn it.

Earlier searches found nothing because (a) the logic does not run on the EA singleton, and (b) nothing
references it from the map: following references from the map header finds no gamemode.

## 2. What is resident  [L]

| piece | where it lives | resident | running |
|---|---|---|---|
| **capture points** | 2 local map entities `0x800001cb`, `0x800001cc`, definition `0x0400000000000037` (components: effect `0x0d`, capture area `0x1a`, Statescript `0x23`, target tag `0x40`, `0x68`) | yes | **yes**: each runs graph **`0x0580000000000028`** (217 KB, 2041 STU instances: the largest graph in memory) |
| **gamemode brain A** | definition **`0x0400000000000013`**: STUPvPGameComponent + STUStatescriptComponent | yes | **no entity** |
| **gamemode brain B** | definition **`0x0400000000000039`**: STUVoiceSetComponent (announcer) + STUPvPGameComponent + STUStatescriptComponent | yes | **no entity** |
| the map's 22 networked placed entities | spawn points (definition `0x167` × 18) and target-tag entities (`0x176` × 3, `0x7a4` × 1) | yes | spawned by our server; they have Statescript but run nothing |

The two PvP-game definitions are the only ones among the 466 resident entity definitions with
`STUPvPGameComponent`. The capture-area definition `0x37` is the only one with `STUCaptureAreaComponent`.
No resident definition has `STUGameModeComponent`, `STUPayloadComponent` or `STUEscortComponent` (Anubis has
no payload).

Their graphs (all resident; sizes include sub-graphs reached through SubScript/PlayScript config vars):

| graph | owner | size | sub-graphs |
|---|---|---|---|
| `0x00f` | brain A | 38 KB, 352 instances | – |
| `0x014` | brain A | 38 KB (53 KB total) | `0x011`, `0x012` |
| `0x029` | brain B | 77 KB (98 KB total) | `0x011`, `0x012`, `0x12d` |
| `0x08f` | brain B | ? (header not read) | ? |
| `0x1e1` | brain B | 25 KB | – |
| `0x028` | capture points | 217 KB | – |

(Graph ids are the low bits of `0x0580000000000xxx`.)

## 3. What is *not* resident, and why that doesn't matter  [L, G]

- **No gamemode data assets**: `STUGameMode`, `STUGameModeLegacy`, `STUGameRuleset`, `STUMap`,
  `STUMapCatalog`, `STUGameModeComponent`, `STUMapLayer`: 0 resident each. Only the 19 `STUMapHeader`s are.
- **The map header references no gamemode.** Anubis' header (`0x079000000000005B`) references its map data
  `m_map` = `0x080000000000005B` (type 0x002, loaded), a second map data `0x080001000000005B` (not loaded),
  textures, music tease, announcer welcome and display names; all references are lazy (no auto-load).
- **The client never instantiates `STUGameModeComponent`**: its hash (`0xCAB7E3D2`) is not in the
  STU-component → runtime-id switch (`FUN_7ff650ec7d70`, [ECS.md](ECS.md) §4). Game-mode data is
  presumably server-side.

So there is no "load the gamemode" step for the client to wait for: what the client needs to show a
match is in the entity definitions it already has.

## 4. How it probably fits together  [?]  (superseded by §10, kept for history)

- The server **spawns the gamemode entity** from definition `0x13` or `0x39` by definition id (like the
  controller `0x0e`), not by placed-entity index.
- Server-created (mirrored) entities don't start their default graphs locally
  ([StatescriptLoading.md](StatescriptLoading.md) §4): the server **creates the graph instances** through
  Statescript sync (positive instance ids), as our server already does for hero graphs, and sets their
  state and variables.
- The server also drives the PvP-game component's replicated data (`STUPvPGameComponent`, component
  `0x24`: game state, overtime, round, team; see Prometheus `Component_24_PvPGame`).
- The local capture-point graphs (`0x028`) presumably read that shared state (and their own target-tag
  and Statescript sync data) for contest visuals and UI.
- Which brain Anubis uses is open. `0x39` carries the announcer voice set, which suggests the full PvP
  gamemode; `0x13` may be a generic or other-mode variant.

## 5. Experiments (2026-09-25)  [L, confirmed in game]

1. **Server spawns the brain**: `POST /spawn {"conn": N, "id": 48, "def": 0x39}` → entity `0x40000030` with
   components `0x01 0x03 0x0e 0x10 0x23 0x24 0x46 0x68 0x7a`. The client immediately builds the scoreboard
   (1 `ScoreboardVM`, 12 `ScoreboardPlayerVM`, 12 `ScoreboardStatVM`): Tab shows the 12 empty slots.
   No graphs run (server entity).
2. **Server starts its graphs**: new server control call `POST /scripts {"conn": N, "entity": 48, "graphs":
   [{"index": 0x29, "sync_vars": 9, "remote_nodes": 2}, {"index": 0x8f, …: 0, 0}, {"index": 0x1e1, …: 0, 3}]}`
   (`Server.SendEntityScripts`, a remote full update; layouts from `graph_sync_info`) → instances 1–3 on
   the brain (server ids). `0x029` 64 nodes (gamemode flow: game-message entries, SendGameMessage, sub-scripts,
   cosmetic entity; mostly client-only presentation), `0x08f` 0 nodes, `0x1e1` 26 nodes.
3. **The HUD**: `0x1e1` node 0 is `STUStatescriptStateUXScreen` (UI screen `0x09A00000000000EE`, target the
   controller `0x40000001`), a synced state. Starting it on the client (Prometheus `start_stop_state`) shows
   the match HUD: "TEMPLE OF ANUBIS / TIME 0.0" and the top-centre "0.0 WAITING FOR PLAYERS". (Done from the
   server alone since T001, §9.) The server
   still has to learn to activate remote states itself (remote state encoding in the full update,
   `FUN_7ff650cedbe0`, not yet reversed).
4. **Entities at (0,0,0)**: the server-spawned placed entities (spawns, target-tag entities) have correct
   positions. The ones at the origin are **local** map entities the client creates itself (proven: trace
   attributes their spawns to game system #43 `MapWorldSystem`, not Prometheus): doors/moving props (defs
   `0x5b`, `0x127`, `0x133`: movement state `0x12`, mirrored anim `0x1c`, pathdata obstacle `0x29/0x2a`,
   Statescript running graph `0x042`, which reacts to game messages `0x0240000000000017/071/02c/02d`), `0x2ed`
   (graph `0x208`), `0x173` (graph `0x0dc`) and plain props (`0x5f`, `0x60`, `0x37e`, `0x11f2`, `0x12f4`,
   `0x431`). Without a server (e.g. King's Row) doors and payload also sit at the origin: **the client creates
   them, the server positions and drives them.**
5. **How replicated entity entries name their entity** [G `FUN_7ff650c500a0`]: 1 class bit (0 →
   `0x40000000`, 1 → `0x60000000`) OR'd with the sized id from `FUN_7ff650ee4ff0` (1 + 8 bits, 01 + 16,
   00 + 32), looked up in the EA id hash. `0x6000xxxx` is the range `PredictedEntitySpawnSystem` gives
   client-predicted entities. **There is no class for local `0x8000xxxx` entities**, so the server cannot
   address a client-created map entity directly by replication. Candidates to check next: game messages
   (the door graph `0x042` has four GameMessage entries), a "from map" (placed-index) spawn that replaces or
   links the local entity, or state held by another networked entity (e.g. the brain). Some origin entities
   are props spawned by graphs (Statescript CF listener #5 spawns `01,03,04` / `01,03,0d` entities) that stay
   at the origin until their owner graph places them. **Answered in §8**: movement state 0x12 overrides the
   placement; the server places a copy with a from-map create + movement snapshot.

## 6. Method

- Resident STU by type: Prometheus MCP `list_stu_resources` with OWLib type hashes (`KnownTypes.csv`).
- Map header references: `read_stu_object` (extended to decode references to `{guid, type, handle,
  loaded}` and to expand objects to a given depth).
- Entities per component: `list_entities has_component=…`; running graphs: `entity_statescripts`.
- Definition components: `graph_sync_info` over all 466 resident entity definitions; graph references and
  sizes: `systems/data/live_anubis/def_graphs.json`, `graph_refs.json`, `graph_edges.json`.

## 7. Next steps

Done since this section was first written: the server spawns the brain (§5), places the map movers (§8)
and starts the HUD by activating remote states (§9). Open, in priority order:

1. **Door open/close** from the server: graph `0x042` on the door copies + remote states with data (T010).
2. ~~Read the brain graphs~~ done: §10 (objective model, per-graph tables, brain B for Anubis).
3. **Match setup does it all**: brain + HUD + movers spawned by `/setup` (T008); tilted movers (T009).
4. ~~An Escort map~~ done: §12 (Dorado: brain `0x12e3`, cart, waypoints; cart motion is server-only).
5. ~~Offline CASC / STU reader~~ done (systems/content, systems/stu).
6. **Tutorial** (§13): entry flow works; the steps need the server to run the brain (T040 hero start-up, then
   T041 runtime on the server; remote actions, owner and string vars in the update format).

## 8. Map movers (doors): why they sit at the origin, how the server places them  (T002, 2026-09-25)

Status tags as above; [G] from Ghidra, [L] measured live on Anubis, [H] confirmed in game by the user.

### 8.1 Why they are at (0,0,0)
- The map data **does** place them: e.g. the attacker ship door (def `0x5b`) has placed-entity index `0x1d7`,
  record position (3.27, 2.77, −20.59), quaternion (−0.090, 0.910, 0.285, 0.287) [L]. The spawn routine
  `FUN_7ff650a293d0` copies the record's pos (+0x20), scale (+0x2c) and quaternion (+0x38) into the loader's
  init transform [G]; placed entities without a movement state (capture points) keep it [L].
- Entities with a **movement state (component `0x12`)** lose it: every frame MovementStateSystem
  (`FUN_7ff650c056e0` → `FUN_7ff650ce6600`) interpolates the 0x12 history; with no entry it resets the state to
  defaults (`FUN_7ff65100aa40`) and writes that transform (origin, identity) to scene component 0x01
  (`FUN_7ff6509e45c0`) [G]. Nothing seeds the history from the placement; the only writers of the history
  (`FUN_7ff65100d7c0`) are network paths (create snapshot, queued server snapshots, local-player reconcile,
  character mover) [G]. The doors' 0x12 had never received a frame (frame −1, empty history) [L].
- Movers on Anubis (local ids, all at the origin) [L]:

| def | placed index | position | rotation |
|---|---|---|---|
| `0x5b` ship door (attacker spawn exit) | `0x1d7` | (3.27, 2.77, −20.59) | tilted (quaternion above) |
| `0x133` door | `0x1d8` | (18.74, 0.79, −18.29) | yaw 0.960 |
| `0x133` door | `0x1d9` | (−4.06, 0.79, −34.25) | yaw 0.960 |
| `0x127` door | `0x1db` | (−44.89, 5.48, 27.11) | yaw 2.570 |
| `0x127` door | `0x1dc` | (−48.10, 1.51, 34.46) | yaw 0.999 |
| `0x2ed` (graph `0x208`, at the capture points) | `0x20a`, `0x20c` | (−7.5, 2.48, 111.3), (−45.6, −2.20, 43.5) | none |

  (Placed-entity entries are 0x38 bytes: +0x18 record pointer, +0x28 the spawned entity's global id; record
  +0x10 flags, +0x13 type (0xb = entity), +0x18 definition, +0x20 pos, +0x2c scale, +0x38 quaternion x,y,z,w.
  Found live by searching the entity's UUID (entity +0x18 = placement GUID).)

### 8.2 Placement binding: why every networked placement also has a local copy  (T037, 2026-09-26)
Short answer: **in this build the client spawns every gameplay placement locally, and nothing suppresses,
replaces or deletes that copy when the server creates the same placement.** A from-map create adds a second
entity and moves the placement's binding to it. The local copy stays as an orphan (at the origin if it has a
movement state, §8.1). All three hypotheses (ordering, a record/create flag, binding to the
local entity) are refuted; details below.

**The load loop** (`FUN_7ff650a233f0`, one pass over the placed entries, 0x38 bytes each: +0x18 record,
+0x20 definition resource, +0x28 bound entity global id, +0x2c placed index, +0x30 state bits, +0x32 record
type) [G]:
1. First visit of an entry: it is started only if `(world+0x284 & 1) == 0 || (record+0x10 & 1) != 0`,
   otherwise marked skipped (state 0x8000). `world+0x284` is the world-creation flags word (`FUN_7ff6509d1130`
   copies it from the creation descriptor +0x10) [G]. Every world creator in the binary passes an even
   value (`0x3fe2` in MapWorldSystem's `FUN_7ff650c23540` and `FUN_7ff650cc6ef0`, `0x3fe2`/`0x31c6`, `0x3dc2`,
   `0x3fea` elsewhere) [G]; live Dorado world flags = `0x3fe2` [L]. **So the gate never skips anything on
   this client.**
2. MapWorldSystem installs the loader callbacks (world+0x3a8 → +0x170…+0x198, context = the system) [G, L].
   The start hook `FUN_7ff65102e850` requests the definition for every entity record (type 0xb) and sets
   state bit 8 (`FUN_7ff650a292a0`); the spawn step `FUN_7ff65102e9e0` spawns once the definition is loaded,
   after the system predicate (vtable slot 0x98, `0x7ff650c23a40`) [G]. The predicate only filters records
   with flag bit 1 set and bit 0 clear (presentation props) by the detail setting `DAT_7ff65180b718`; every
   other record passes [G]. The spawn (`FUN_7ff650a293d0`) creates the entity and writes its global id to
   entry +0x28. **No step looks at +0x28, at the GUID map (sys+0x48) or at any network state** [G].
3. Live on Dorado: all 1797 entity entries went through the hook and are spawned (state `0x4005`), none
   skipped [L].

**Record flags (`record+0x10`)** [D, measured on Anubis and Dorado with `systems/data/maps/tools/recflags`]:
`0x01` = gameplay records (spawn points, target tags, spawn-room volumes, waypoints, doors, capture points,
the cart, breakables), `0x02` = model-only props (components 01 03 04), `0x03` = both (some models, 0x4a
entities). The flag is uniform per definition (no definition mixes values). A networked placement has
the same flag (`0x01`) as a local-only gameplay placement, so **no record flag marks "server-owned"**.
Bit 0 reads as "gameplay/simulation record" and the unused world bit 0 as "gameplay-only world" (a
server/headless world that skips the props) [?, strong]; neither can hide a local copy on this client.

**Which placements the client expects from the server** [G, L]: after the load, MirrorSystem (game #8)
clears its list (sys+0xb0/+0xb8) and visits every entity entry (`FUN_7ff650c022f0` →
`FUN_7ff650a24070` → visitor `FUN_7ff650c02180`); an entry whose definition has runtime component `0x73`
(`'s'`) or `0x2e` (`'.'`) appends its placed index. That is exactly Maps.md §3's "networked" list. Its
only purpose is readiness: each from-map create removes its index, and when the list is empty and the local
player entity exists, MirrorSystem runs the one-time local-player init (`FUN_7ff650c19340`, flag sys+200) [G]
(this is why the hero lock-in waits for `/setup`, Maps.md §3).

**The from-map create** (`FUN_7ff650c00d20`, create bit 1 + u32 placed index) [G, L]:
- It reads the loader entry of that index (`EA+0x130` → vfunc 0x28 = world+0x3a8 → +0x168 = the loader),
  copies the record's GUID/transform from entry +0x18, and, when the id is new, spawns through the **same**
  `FUN_7ff650a293d0` as the local pass. So the placement's entry +0x28 is overwritten with the server
  entity: live, entry `0x22a` → `0x40000031` (server cart) and all 40 networked entries → `0x40000003…0x2a`
  [L]. The local copy (`0x80009a6b`, the spawn-point copies) is no longer referenced by the placement table,
  but nothing deletes it [G, L].
- It dereferences entry +0x18 (the record) without a null check [G]. That field is filled when the load loop
  first visits the entry, so a from-map create that arrives **before** the placement pass would read a null
  record (client crash), not pre-empt the local spawn. Sending creates after the load, as `/setup` does, is
  the only safe order. The load loop spawns the local copies during the map load (trace
  `trace_mapload_anubis.json`: 1008 MapWorldSystem spawns in ticks 765–841 of the load) [L], so ordering
  cannot avoid them either.
- Replicated ids have no class for `0x8000xxxx` (§5 item 5) [G]. The id lookup (EA id hash) that would reuse
  an existing entity therefore can never hit a local copy. MapWorldSystem's GUID lookup
  (`FUN_7ff650c24430`) is only reachable through its interface vtables (Statescript/world queries), not from
  MirrorSystem [G, static].
- Side effect: the MapWorldSystem unload hook (`FUN_7ff65102e8b0`, loader +0x180) deletes the entity at entry
  +0x28, which after a from-map create is the **server** entity, while the orphan survives [G]. It only runs
  when a placement is unloaded (detail change for props, map teardown).

**What this means for the server.** On this client, any placement the server creates from the map exists
twice. For the networked list (spawn points, target tags) that is how the client works: the server must
create them (readiness), the local copies have no model component (0x04) [L] and run their own graphs (`0x0d6`), so the
duplicates should be invisible [?]. For visible gameplay placements (doors, cart) the server has two choices,
both with a cost: create a server copy (placeable, movable, §8.3) and live with the orphan at the origin,
or leave the local copy alone (it cannot be positioned or moved: no id class, 0x12 has no network
path to it). Whether the shipping server avoided the visible duplicates some other way (e.g. by
Statescript on the local copy, or because a later client build changed the load path) is not visible in this
binary [?].

### 8.3 What works: from-map create + a movement snapshot  [L, H]
The create's from-map branch (`FUN_7ff650c00d20`) is: bit 1, u32 placed index, **bit: snapshot**, then a
movement snapshot (`FUN_7ff65100aba0`, a delta against the baseline `DAT_7ff651890390`). Definition creates
carry the same snapshot with create flag 0x02. If the snapshot's frame is not −1 it is pushed into the 0x12
history and applied at once; one history entry holds the pose (the interpolation copies the only entry) [G, L].
- Encoding: `server/internal/instance/stream.go` `Mover.writeSnapshot` (bit-by-bit comments). The reader is
  given frame −1 for creates, so the frame must be written explicitly (−1 − d); positions are 1/1024-unit
  signed prefix numbers, yaw q/4096 rad.
- Control API: `POST :3728/spawn {"conn": N, "id": 111, "map_index": 472, "mover": {"pos": [18.738771, 0.7936692, -18.289146], "yaw": 0.95993}}`.
- Result: all five doors and the two `0x2ed` movers appear at their placements with the placement's yaw
  (scene quaternion matches the record for the pure-yaw doors) [L]; the user saw them in place [H].
- **Only yaw survives**: pitch and roll in the snapshot are dropped (movement state angles (0, yaw, 0), scene
  quaternion pure yaw) [L]. The tilted ship door (`0x5b`, "part of a ship, supposed to be at an angle" [H])
  therefore comes out upright. Open: how a real server tilts it (a moving-platform parent, snapshot fields
  +0x244/+0x248? a different rotation encoding?).
- Use the connection the client is on (newest `last_seen` in `/conns`); spawns sent to a stale connection
  are lost.

### 8.4 Game messages (the door graph's open/close)
- Door graph `0x042`: GameMessage entries `0x…17`, `0x…2d` (with a sender filter), `0x…2c`, `0x…71` flip var
  `0x192`, which picks state #2 or #3; each plays an anim alias (`0x0c80000000000011` / `…15`, type 0x014) on
  the door [L]. Executing entry #0 (Prometheus `execute_node`) set `0x192` 0 → 1 [L].
- **No network message carries a game message** [G, JAM handlers and Statescript-sync events checked]. Game
  messages are posted only on the client: by Statescript send nodes (to self, to an entity-id list
  `vtable 0x7ff6515bbcb0`, or broadcast to every Statescript component of the EA `vtable 0x7ff6515bb370`),
  or by UI code (`FUN_7ff650c24dd0`, which only reaches the frontend UI entity) [G]. The record: message id
  +0x20, sender entity +0x210, instigator +0x214; an entry's filter (config var `0xaff3b654`) tests the sender
  and passes when there is none [G]. Statescript-sync events cannot post one (for send nodes they only mark
  "already sent") [G].
- So the server makes doors react **indirectly**: the local capture-point graph `0x028` broadcasts `0x…71`
  (node type `0xece45723`) and listens for `0x…2d` [old graph dumps]; the brain graphs presumably send the
  rest. Remote (server-created) instances run only client-only entries (instance flag, entry +0x75) [G].
- The server's door copies run no graph (server entities don't start default graphs); opening them means
  creating graph `0x042` on them and activating state #2/#3 remotely, i.e. T001's remote state activation.

## 9. Remote state activation: the server starts the HUD  (T001, 2026-09-25)  [G, L, H]

A remote Statescript full update (stream 1, local flag 0; `server/internal/instance/script.go`
`ScriptFullUpdate`) activates states on a server entity's instances.
- Per instance, after the sync vars, `FUN_7ff650cedbe0` walks the graph's **`m_remoteSyncNodes`**
  (graph +0x120). It skips entries that are not states or are client-only (+0x75) or server-only (+0x76),
  and for those it reads no bit. For each remaining node it reads:
  - **included** bit (0 = leave it as it is);
  - for the plain layout (`FUN_7ff650ecfee0` = 0): **active** bit. 0 deactivates (`FUN_7ff650f29df0`). 1
    activates (`FUN_7ff650f295c0`) and reads the **state data** (`FUN_7ff650f289e0`: the state's reflected
    data-store fields, where remote mode skips local-only fields, then vfunc +0x1F8);
  - for types 0x7bf31629, 0x860ee5fc and 0xffbfa88d (`ecfee0` = 1): "has update" bit, then active bit and
    data. We don't send these yet.
  
  After the loop, active states that are not remote sync nodes (+0x6c = −1) are deactivated.
- A state's index in the update is its position among the eligible remote sync nodes. Node +0x68 is the
  state index and +0x6c the remote index [G]. Graph `0x1e1`: `m_remoteSyncNodes` = nodes 0, 8, 25 [L]. Node
  0 (`STUStatescriptStateUXScreen`, the HUD) is remote index 0, and its state data is empty: 0 bits [L,
  the update parsed and the HUD appeared].
- Control API: `POST :3728/scripts {"conn": N, "entity": 48, "graphs": [{"index": 41, "sync_vars": 9,
  "remote_nodes": 2}, {"index": 143, "sync_vars": 0, "remote_nodes": 0}, {"index": 481, "sync_vars": 0,
  "remote_nodes": 3}], "active": {"3": [{"index": 0}]}}` after `/spawn {"id": 48, "def": 57}`.
- Result [H]: without Prometheus the client shows "0.0 WAITING FOR PLAYERS" top centre, **with the two
  objective (lock) icons**, and the Tab scoreboard with "TEMPLE OF ANUBIS / TIME".
- Next: the same mechanism should open and close the server's door copies (§8.4). Create graph `0x042` on
  them and activate its state #2 or #3; their remote indexes come from the graph's +0x120 list. States with
  data (e.g. BooleanSwitch) need their data format per type.

## 10. Objective model: how the server is meant to drive objective entities  (T013, 2026-09-25)

Status tags: [L] measured live, [G] read in code, [H] confirmed in game by the user, [?] inferred.
Graph node tables, sync layouts and the game-message table: `systems/data/live_anubis/objective/`
(`dump_<graph>_<entity>.json` = Prometheus `dump_script`; `an_<graph>.json` = `m_syncVars` /
`m_remoteSyncNodes` with node indexes and client-only flags; `game_messages.txt` = who sends / listens to
each game message among all graphs running on Anubis plus the brain sub-graphs `0x011`, `0x012`).

### 10.1 The model in one paragraph
The graphs are written for a **server-authoritative simulation**. The server runs the whole match. That
means the brain graphs, the capture-point and door graphs, and server-only rules the client doesn't have.
Those rules post the game messages nothing on the client sends and receive the ones nothing on the client
receives (§10.4). Only results reach the client, over three channels:
1. **Statescript sync on server entities**:
   - sync vars (the "changed" bits after the graph list);
   - owner variables (the full update's owner-var section);
   - remote states, activated with their state data.
2. **Mirrored component data**: PvP game `0x24`, capture area `0x1a`, target tag `0x40`, lease `0x68`
   (stream 4 component updates).
3. **Movement snapshots** for movers (§8).

**Game messages never cross the network** (§8.4). On a server-created instance the client runs only
client-only entries and nodes plus the states the server activates. So the brain's message logic
(entries `0x029` #5/#13/#18, all of `0x014`) runs **only on the server** [L: flags, G: T002]. The
objective graphs themselves are built to be synced, e.g. `0x028`:
- 14 syncable vars, among them the owner vars `0x0064` enabled, `0x0063`, `0x008d`, `0x0027` and the
  phase `0x0195`;
- 12 eligible remote states;
- 11 remote actions: announcer banners, effect swaps, capture music.

On this client, though, the capture points and doors are **local** map entities that the server cannot
address.

### 10.2 Entities: who creates, who drives, which channel

| entity (Anubis) | created by | driven by | channel | evidence |
|---|---|---|---|---|
| gamemode brain **B**, def `0x39` (announcer voice set, PvP game, Statescript) | server, by definition (`/spawn def 57`) | server | its graphs' sync vars + remote states (`0x1e1` R0 = HUD), owner vars, `0x24` mirror | [L, H] §5, §9 |
| gamemode brain **A**, def `0x13` (PvP game, Statescript) | server, by definition | server | remote Stack states of `0x00f` write owner var **`0x0195` = phase** (§10.3) | [L] |
| "the gamemode entity" seen by client graphs | — | — | config var type `0xd5384f53` returns the **newest entity with a PvP-game component**: B alone → B; B then A → A; a later non-PvP server entity does not change it | [L] |
| capture points `0x800001cb` (−7.5, 4.7, 111.3), `0x800001cc` (−43.8, 1.7, 45.9), def `0x37` | **client**, from the map (placement type `0xb`, always spawned locally) | their **local** `0x028` instance (id −1) | none from the server in this build: not in the client's list of 22 placements it expects from the server; no network id class for `0x8000xxxx` | [L] system 8 list, [G] `FUN_7ff650a28720` / §8.2 |
| server copy of a capture point (def `0x37` by definition at the placement) | server (tested) | server | remote instance of `0x028` (positive id); its remote states run on the client | [L] |
| doors / movers (defs `0x5b`, `0x127`, `0x133`, `0x2ed`) | client, from the map | local `0x042` / `0x208` | none directly. Their local graph reacts only to game messages (`0x…17` from the brain, `0x…71` from the capture graph, `0x…2c`/`0x…2d` server-only), and on the client those only fire if a local graph posts them; server copies: §8.3 (placement), T010 (states) | [L, G] |
| spawn points (def `0x167` ×18) | client **and** server (the client expects them: networked placements) | local `0x0d6` on the local copy (listens `0x…71`); server copies run nothing | mirror data `0x0e`, `0x68`, `0x73` | [L] |
| target-tag entities (`0x176` ×3, `0x7a4` ×1) | client and server (expected networked placements) | server | mirrored target tag `0x40` (two lists); **no graphs** | [L]; role [?] |

### 10.3 Per-graph: what the server must set or send

Remote index = position among the graph's eligible `m_remoteSyncNodes` (what `/scripts` "active" takes).
C = client-only node (+0x75). State data in the remote format: **Stack states take 1 bit** (0 = no
predecessor in the priority stack; the reader `FUN_7ff650f2b360` then reads an instance id + node
index), states whose local encoding has 0 bits take 0 [L, G].

**`0x00f` (brain A: phase machine)**: 0 eligible sync vars, 11 remote states, no C nodes. The
server's phase changes, as owner-var writes on the brain:

| remote idx | node | writes (owner var) |
|---|---|---|
| 0, 1, 2, 3 | 12, 13, 14, 21 | `0x018d`, `0x0015`, `0x0018`, `0x0016` := true |
| 4, 5, 6, 7 | 23, 24, 25, 26 | **`0x0195` := `0x…018d` / `0x…0016` / `0x…0015` / `0x…0018`** (identifiers) |
| 8 | 27 | type `0xa4f3be65` (no config) |
| 9, 10 | 41, 42 | `0x0197` := true; **`0x0195` := `0x…0197`** (the value OnStart sets: the initial phase) |

Its entries (native triggers `0x…09`, `0x…16`, `0x…1a`; `0x…04`, `0x…05` from `0x014`; `0x…29`, `0x…2a`
from itself) run on the server only. Pairing (entry → message → state): `0x1a` → R3+R5, `0x2a` → R0+R4, `0x05` → R1+R6,
`0x04` → R2+R7. Live [L]: the server activated R9+R10, then R0+R4, R1+R6, R2+R7, R3+R5; the owner var
`0x0195` followed (`0x197` …), with no crash once the Stack data bit was sent.

**`0x014` (brain A: arbiter)**: 0 sync vars, 0 remote states, no C nodes: runs **only on the server**
(`0x…02/03/07/08/0a` in, `0x…04/05` out to the gamemode entity).

**`0x029` (brain B: gamemode flow)**: 9 eligible sync vars, 2 eligible remote states.

| sync slot | var | used by |
|---|---|---|
| 0, 1 | `0x0784`, `0x0785` | C Stack states 32, 33 → owner `0x0783` (priority stack of a `0x0de…` resource: announcement/HUD message [?]) |
| 2 | `0x01ac` | SetVar 14 (on `0x…0b`) |
| 3–6 | `0x0738`–`0x073b` | C Stack states 37, 36, 35, 34 → owner `0x0783` |
| 7 | `0x07dc` | remote state 56 CosmeticEntity (the entity definition to show) |
| 8 | `0x0cdd` | C Stack states 61, 62 → owner `0x0783` |

Remote idx 0 = node 56 (CosmeticEntity), 1 = node 63 (`0x4ab09851`); node 22 (Effect) is a remote
action. Entries `0x…2d`, `0x…0b` (the capture points' tick), `0x…45` and the sends `0x…0d` (capture
enable, broadcast), `0x…17` (door graph `0x042` entry 0, broadcast), `0x…03`/`0x…78` (to itself) are
**server-side**. SubScript `0x011` (plays `0x012` on `0x…03`) and `0x12d` (on `0x…45`).

**`0x1e1` (brain B: HUD)**: 0 eligible sync vars. Remote idx 0 = node 0 UXScreen `0x09A…EE` (the match
HUD, §9) [H]; 1 = node 8 ChaseVar owner `0x0821` → 10000; 2 = node 25. Entries `0x…45`, `0x…1a3`
(sender filter), `0x…3d`, `0x…e3`: server-side.

**`0x08f` (brain B)**: 0 nodes, 2 sync vars (none eligible): nothing to send.

**`0x028` (capture point)**: 14 eligible sync vars and 12 eligible remote states.

Sync var slots (`*` = owner var, sync-var entry +0x14 = 1 [?]):

| slot | var | meaning / use |
|---|---|---|
| 0 | `0x0780` | HUD string states (C 19–26) |
| 1 | `0x0203` | team/instigator: occupancy state and capture messages `0x5a4`/`0x5a5`/`0x190` target |
| 2 | `0x098a` | node 130 |
| 3 | `0x008d`* | per point (point 1cb: 1, 1cc: 0) |
| 4 | `0x0712` | C condition 152 (in-world UI branch) |
| 5 | `0x0195`* | the point's own phase |
| 6 | `0x06d9` | (`0x0de…34b`/`…34c` per point) |
| 7 | `0x06da` | Stack R5 |
| 8 | `0x0260` | occupancy state |
| 9 | `0x0062` | occupancy state |
| 10 | `0x0063`* | occupancy state, Stack R9 |
| 11 | `0x0064`* | **enabled** (C BooleanSwitch 15, enable toggles 36–38/57/71) |
| 12 | `0x0027`* | Stack R2 |
| 13 | `0x0029` | the player named in the "wants to attack/defend the control point!" banners |

Remote states (idx → node):

| idx | node | type / role |
|---|---|---|
| 0–3 | 64–67 | Stacks: owner `0x07e1`, `0x0064`, `0x0027`, `0x008d` |
| 4 | 114 | `0x0119c536`, the **capture-zone occupancy** state |
| 5–8 | 122–125 | Stacks: owner `0x06da`, `0x08a3`, `0x08a4`, `0x08a5` |
| 9 | 157 | Stack: owner `0x0063` |
| 10 | 184 | `0x9f74c07c` (→ C HUD-string branch 35) |
| 11 | 185 | `0x4ab09851` |

Remote actions (not in the full update; sent as events [?]):
- 85, 87, 92–95: effect-state swaps `0x5efae015`;
- 96, 97: Effect;
- 145: capture music `0x…290`;
- 148, 149: banners.

Game messages:
- listens to `0x…0e`, `0x…2d`, `0x…0b`, `0x…0d`, `0x…2b7` (zone occupancy, sender filter), `0x…3a7`,
  `0x…3bb`;
- sends `0x…71` (broadcast: doors `0x042`, spawn `0x0d6`, `0x0ec`, `0x0f6`, `0x1ce`);
- sends `0x…51`/`0x…52` to the gamemode entity: no resident listener;
- sends `0x…0b` (broadcast tick), `0x…120`, `0x…130`, `0x…114`, `0x…190`, `0x…5a4`/`0x…5a5`, `0x…2a3`.

It reads the gamemode entity:
- HUD string states (C) take it as an entity config var (`0xccde96d8`);
- node 181's condition expression contains a gamemode-entity config var and a `0x6324dd9f` reference to
  identifier `0x1a27`.

Its in-world capture ring (C UIInWorld 16/17/28/31, UI `0x09A…EA`) hangs off client-only branches
gated by the synced vars `0x0064` and `0x0712`.

**`0x042` (doors)**: 5 eligible sync vars, 2 remote states (T010: #2, #3). Listens `0x…17` (brain B),
`0x…71` (capture points), `0x…2c`, `0x…2d` (server-only).

**`0x26a` (ridable objective, resident)**: 8 eligible sync vars, 34 eligible remote states, hero-like
states (MovementMod, LimitAim, Camera3P, Invisible, CosmeticEntity `0x0400…0d93`, Stacks on owner vars),
entries `0x…13`, `0x…16c`, `0x…6b`, `0x…592`. The Escort payload graph `0x098` is **not resident** on
Anubis (T004). By the same model, a payload is a server entity (a mover: movement snapshots, §8) running a
synced objective graph [?].

### 10.4 Messages with no client-side sender or listener  [L]
Among all graphs running on Anubis plus `0x011`/`0x012`/`0x26a`:
- **no sender**: `0x…0e`, `0x…2d`, `0x…2c`, `0x…09`, `0x…16`, `0x…1a`, `0x…45`, `0x…2b7`, `0x…3a7`,
  `0x…3bb`, `0x…13`, `0x…16c`, `0x…6b`, `0x…592`, `0x…3d`, `0x…e3`, `0x…1a3`;
- **no listener**: `0x…51`, `0x…52`, `0x…06`, `0x…19`, `0x…78`, `0x…114`, `0x…120`, `0x…130`,
  `0x…190`, `0x…290`, `0x…2a3`, `0x…5a4`, `0x…5a5`, `0x…1a4`, `0x…7a`, `0x…7d`.

No 64-bit immediate of the no-sender ids checked (`0x0e`, `0x2d`, `0x2c`, `0x09`, `0x16`, `0x1a`, `0x45`,
`0x2b7`, `0x3a7`, `0x3bb`) is in the client code (memory search over the module). So they come from and go
to **server-only logic** [?, strong]. That is the game-mode rules layer: `STUGameMode`, `STUGameRuleset` etc. are not
resident, §3. The server has to supply it itself.

### 10.5 Which brain Anubis uses
**Brain B (`0x39`)** [?, strong]:
- it carries the announcer voice set;
- its HUD (`0x1e1`) shows "TEMPLE OF ANUBIS" and the objective icons [H];
- its flow graph is wired to the capture points (listens to their tick `0x…0b`, sends them `0x…0d`) and to
  the doors (`0x…17`).

Brain A (`0x13`: `0x00f` + `0x014`) is a generic phase machine. It is wired to nothing on the map
except through the phase var `0x0195`, which `0x029` and `0x028` also hold. The client resolves "the
gamemode entity" to one entity only (the newest PvP-game entity), so the two are not meant to coexist
as gamemode entities. Whether a real server also spawns A for the phase machine, or sets `0x0195` on B
directly (owner-var section), is open [?].

### 10.6 Live tests (2026-09-25)  [L]
1. With B then A spawned, setting A's phase (`0x00f` R9+R10, then every other pair) changed A's owner
   vars. It did **not** change any active state or variable of either local capture point. Their
   conditions don't re-evaluate without their (server-side) entries firing.
2. The first attempt sent the two Stack states without state data. The client read the next state's
   "included" bit as the Stack's reference bit, resolved a nonexistent instance and **crashed**: AV at
   RVA `0xf4b3cc`, `[null+0x160]` in `FUN_7ff650f2b360`. With one `0` data bit per Stack state it works.
3. The server spawned def `0x37` at the second capture point's placement (`0x4000003c`, components as the
   local one plus `0x0e`, `0x10`, `0x7a`) and created `0x028` on it (instance 1). Activating remote idx
   0–4 and 9–11 (the set the local point has active) gives the same active set on the client (64–67,
   114, 157, 184, 185). **Not yet checked in game**: whether the copy shows anything or conflicts with
   the local point.

### 10.7 What the server is missing to run a capture
(T055: item 1's node types and a designed capture model now run offline, brain B + both Anubis points captured in
order: [StatescriptNodes.md](StatescriptNodes.md) §6.3, §6.9.)
1. A **Statescript runtime** (T014–T016) running the brain, capture and door graphs server-side, plus
   the **rules layer** that posts §10.4's messages (phase triggers, zone occupancy `0x…2b7`, init
   `0x…0e`) and consumes `0x…51`/`0x…52`/`0x…5a4`/… (capture results).
2. **Remote updates with state data per type** (Stack = 1 bit; the rest to catalogue), **sync var
   values** in the remote format (today every var is sent "unchanged"), the **owner-var section** (today
   0 entries), **deltas** over time and **remote-action events** (banners, effect swaps, music).
3. A decision on the **capture-point entity**:
   - (a) server copies of def `0x37` (tested: sync reaches them; duplicates of the local points remain);
   - (b) a mechanism that lets the server reach the local ones (not found; §8.2).
   
   The same question applies to doors (§8, T010).
4. **Mirrored component data**: capture area `0x1a` (type `0x47c72f78`: a list, 2 floats, 5 ints, a
   float, `m_enabled`, a flag; all zero now), PvP game `0x24`, target tag `0x40`.
5. **Players on the point**: movement / occupancy (roadmap #7), and team assignment (filter bits `0x10`).

## 11. Team assignment: filter bits (component 0x10)  (T035, 2026-09-26)  [G, L]

- A player's team is **component 0x10 FilterBits**: the mirrored u64 `pre_filter_bits` (component `+0x90` = mirror
  object `+0x78`, STU `0xfb184d9c` field `0x81ee6c36`); `FUN_7ff650c9f660` copies it (bit 0x10 recomputed) to the
  final bits `+0x98` that the rest of the client reads. **Team t = bit `0x40000 << t`** (0x40000…0x200000; Prometheus
  `Component_10_FilterBits::set_team`). Our server sent nothing, so every networked entity (controller, hero, spawn
  points) had 0 = no team [L].
- **Encoding**: a mirror update of component 0x10 with one changed field. u64 mirror fields are read **compressed**
  (`FUN_7ff650818e90` flag 2 → `FUN_7ff650e29d40`): a byte whose bit i marks byte i as non-zero, then those bytes,
  low first. 8 raw bytes are read back as 0 (their first byte is taken as the mask) [G, L]. Server:
  `Mirror10(TeamBit(t))` (`server/internal/instance/stream.go`), control API `POST /filter {"conn", "id", "bits"}`.
- The game server now gives the controller and the hero team 1 (the attacking team of the map's spawn data,
  [Maps.md](Maps.md) §4) and every spawn point its own team: read back live as `pre = final = 0x80000` on the
  controller/hero and 0x40000 / 0x80000 on the spawn points [L].- What in the game reads or writes teams (brains, spawns, barriers, healing; script vs native vs server-only):
  [Maps.md](Maps.md) §6.

## 12. Escort / payload (Dorado)  (T036, 2026-09-26)

Status tags: [L] measured live, [G] read in code, [H] confirmed in game by the user, [?] inferred.
**Setup**: client in a Private Game on Dorado (map data `0x08000000000002C3`) on our game server, conn 13, `/setup`
done (controller, hero `0x40000002`, the 40 networked placements `0x40000003…0x2a`). Data:
`systems/data/live_dorado/` (`entities.json`, `entity_statescripts.json` = graphs per entity, `def_graphs.json` =
Statescript graphs per resident definition, `graphs/an_<g>.json` = sync layouts (`graph_sync_info`),
`graphs/dump_<g>_<entity>.json` + `sum_<g>_<entity>.txt` = node dumps and one-line-per-node summaries,
`graphs/cp_<entity>.json` = the five waypoint instances, `game_messages.txt`, `trace_local_payload_start.json`;
`tools/` = the scratch scripts that made them).
Graph ids are the low bits of `0x0580000000000xxx`; `$xxxx` = variable `0x0D8…xxxx`; `0x…nn` = game message
`0x02400000000000nn`.

### 12.1 The answer
Escort works like Assault (§10). The logic is resident on the client, the server drives it, and game
messages never cross the network. One thing is new: **the payload's motion and progress are
server-only.**
- The client maps `STUPayloadComponent` → runtime component **`0x19`** and `STUParametricMovementComponent` (the
  path) → **`0x17`** (`FUN_7ff650ec7d70`) [G]. Neither id has a component type in the client's registry: they are
  among the 24 missing ids in `systems/ecs/manifest/components.json` [L]. Neither cart has them [L].
- The payload graph's `StateUpdatePayload` (`0x6c2502e0`, attacker/defender lists → progress outputs) has **no
  client class** (StatescriptNodeCatalogue: runtime empty) [G].
- So the client cannot move a cart: the server simulates it along the path and sends **movement snapshots**
  (component 0x12, §8.3) plus Statescript sync.
- Dorado's brain is a third PvP-game definition, **`0x12e3`**. Anubis' brain B `0x39` is not resident on
  Dorado [L].

### 12.2 What is resident and running  [L]
511 resident entity definitions. The ones with PvP game / payload / escort / capture area:

| definition | components | graphs | role |
|---|---|---|---|
| **`0x12e3`** | VoiceSet `0x528f2f94`, PvPGame `0x81ebdcd0`, Statescript | **`0x10f`**, `0x1e1`, `0x219` | **Dorado's brain** (escort flow + the shared match HUD `0x1e1`); no entity until the server spawns it |
| `0x13` | PvPGame, Statescript | `0x00f`, `0x014` | brain A, the generic phase machine (§10.3), resident here too |
| `0x1232` | VoiceSet, Statescript | `0xb21` (2 sync vars, 2 remote nodes, none eligible) | announcer-like; role not checked |
| **`0x77f`** | STUPayloadComponent `0x1bfe2681`, `0xf6ddfa63` (0x8d), `0x995b544f` (0x41), `0x1b54380e` (0x2a), Statescript, TargetTag, MirroredAnim, Model, **ParametricMovement `0xce81ad50`**, **STUEscortComponent `0x0e2552e2`** (0x8c) | `0x098`, `0xa33` | **the payload cart** (placement `0x22a`, pos (6.01, −0.22, 6.45), yaw π/2) |

No capture area on Dorado. Graph `0x029` (Anubis' brain B flow) and `0x028` (capture point) are not resident.
Graph `0x26a` ("ridable objective", §10.3) is resident but runs nowhere and is not the payload: the payload graph is
`0x098`.

Entities on the client (local ids `0x8000xxxx`; the EA still holds earlier maps' entities, so counts per
definition include leftovers):

| entity | def | graphs (local instance) | what |
|---|---|---|---|
| cart `0x80009a6b` | `0x77f` | `0x098` (id −1, 125 nodes), `0xa33` (id −2) | local payload; components `01 03 04 12 1c 23 29 2a 40 41 68 8c 8d`; **at (0,0,0)** like the doors (§8.1: 0x12 without history) [L]; **icon over it** once its state 123 was active [H] |
| waypoints `0x80009a0b`, `…a22`, `…a24`, `…a25`, `…a36` | `0x15c` | `0x0d2` | STUWaypointComponent `0x1b` + Statescript: the path's **checkpoints** (table §12.4) |
| `0x80009bc4…bc8` | `0x330` | `0x21f` | effect on `0x…71` (one at the end point, four far off the map: fireworks/sky effects [?]) |
| `0x80009afe` | `0x7f2` | `0x532` | client-only effects on `0x…1ef` |
| movers `0x118f`, `0x1190`, `0x1191`, `0x11b6`, `0x120d`, `0x122d` (9) | | `0x042` (the door graph, §8.4) | Dorado's doors, at the origin (§8.1) |
| spawn-room volumes (def `0x29f`) | | `0x1ce` | as on Anubis (Maps.md §4) |

### 12.3 The brain `0x12e3`: live  [L, H]
`/spawn {"conn":13,"id":48,"def":4835}` → `0x40000030` (components `01 03 0e 10 23 24 46 68 7a`), then
`/scripts {"conn":13,"entity":48,"graphs":[{"index":271,"sync_vars":6,"remote_nodes":2},{"index":481,"sync_vars":0,"remote_nodes":3},{"index":537,"sync_vars":0,"remote_nodes":0}],"active":{"2":[{"index":0}]}}`
→ instances 1 `0x10f` (50 nodes), 2 `0x1e1` (26), 3 `0x219` (0 nodes).

**`0x10f` (escort flow; the counterpart of Anubis' `0x029`)**: 6 eligible sync vars, 2 remote states.

| sync slot | var | used by |
|---|---|---|
| 0, 1 | `$0784`, `$0785` | C Stack states 18, 19 → owner `$0783` (announcement/HUD message priority stack, as in `0x029`) |
| 2, 3 | `$0739`, `$073b` | C Stacks 23, 22 → `$0783` |
| 4 | `$07dc` | R0 = node 4 CosmeticEntity (the entity definition to show) |
| 5 | `$0cdd` | C Stacks 46, 47 → `$0783` |

R1 = node 49 (`0x4ab09851`). Entries and sends (server-side, like `0x029`'s):
- entry #39 (plain Entry) → state 35 `0x97315c42` (reads `$000e`, `$002e`, `$002f`, `$0012`, `$0010`: the team
  and player counts [?]) → send `0x…03` to itself, and state 38 → SubScript `0x011` (plays `0x012` on `0x…03`,
  as on Anubis);
- `0x…45` → SubScript `0x12d`;
- **`0x…2d` → condition 32 → broadcast `0x…2cb` (payload #104), and condition 29 → broadcast `0x…66` (payload
  #8) → broadcast `0x…17` (doors `0x042` #0)**. This is the **match start**: it opens the doors and
  starts the payload;
- `0x…ca` (from the payload) → `$074a` := true; `0x…18f` (from the payload, sender filter) → no-op.
- Client-only: entry 15 → UXViewModel 40 (`0x0AC…1F5`), toasts 9/20/21/44.

**`0x1e1`**: the same match HUD graph as on Anubis (§10.3). R0 = UXScreen `0x09A…EE` activated by the server
(0 data bits). **`0x219`**: 0 nodes (like `0x08f`).

Client-only entries do **not** run on their own on these server instances: after the full update, `0x10f`
#15 and `0x1e1` #3/#5 had not started their UX states [L].

### 12.4 The payload  [L, G]
**`0x098` (payload), 125 nodes, 18 eligible sync vars, 25 eligible remote states** (`*` = owner var):

| slot | var | meaning / use |
|---|---|---|
| 0, 1 | `$0780`, `$0781` | HUD view-model inputs (C UXViewModel 35/36 `0x0AC…1F3`) |
| 2 | `$0846` | Wait 53 / Anim 54 timing |
| 3 | `$0bd9`* | Stack R17 |
| 4 | `$06d9` | (`0x0de…07e`) |
| 5, 6 | `$1a9f`*, `$1aa0`* | **attackers / defenders near the cart**: outputs of TrackTargets 0 / 2 (radius from `$08e0` / `$08df` = 7), inputs of UpdatePayload |
| 7, 8 | `$1aa1`*, `$1aa2`* | Stacks R20/R21: their counts (→ ModifyHealth 14 heals attackers on the cart; → switch 91) |
| 9 | `$0029` | the player in the "wants to move / defend the payload!" chat lines (actions 102/103) |
| 10 | `$0270` | **moving** (UpdatePayload output; SetVar 6/7) |
| 11 | `$01f3`* | **checkpoint index** the cart heads for (input of the move states, Watch 38, Stack R15 → owner `$009a`) |
| 12, 13 | `$1933`*, `$1934`* | Stacks R18/R19 (filtered attacker/defender arrays) |
| 14 | `$07b7`* | Stack R5 (set on `0x…18`) |
| 15 | `$02bb`* | Stack R16: **started** (the main state's gate) |
| 16, 17 | `$023d`*, `$023e`* | FilterArray 118/119 outputs |

Owner vars on the cart: `$01b3` = push direction (−1 back, 0 idle, 1 forward; Stacks R2/R3/R4, Switch 63 → the
wheels' DataFlowMapping R11–R13 = 0 / 0.5 / 1; traced to the effects and sound in DataFlow.md §7.1), `$0727`, `$0317…$031a`.

The flow:
1. Entry #56 (start): `$03b9` := 0, `$0781` := true, radii 7, `$0908` := 10, `$0bd8` := 1 → idle Anim 57,
   Switch 63 (wheels 0.5).
2. **Start**: `0x…66` (#8, brain broadcast) or `0x…71` (#84, conditions 85/86) → Stack R16 (`$02bb` := true) →
   **state 3** = TrackTargets 0 (attackers → `$1a9f`) and 2 (defenders → `$1aa0`), **UpdatePayload 5**
   (`$1a9f`, `$1aa0` → `$0270`, `$03b9`; 5.0 = [?]), Wait 52 / 53 (anims, sends `0x…18f` to the gamemode entity,
   `0x…16d` to itself), State 105 (BooleanSwitch 12 on `$0270`, 83 on `$063d`); plus broadcast `0x…121`, target
   tags 112 (`0x0D6…29B` on the cart).
3. **Motion states** (type `0x616f509f`: `m_speed`, `m_destinationRadius`, `7a2added` = `$01f3`, output
   `7fe17913` = `$02af` = the waypoint reached, plugs aborted / finished):
   - 4: pushed forward (speed expression; Stack R3 `$01b3` := 1);
   - 23: speed 2 (R4 `$01b3` := 1);
   - 11: rollback, speed −0.3, after Wait 13 (`$0908` = 10 s) with nobody pushing (R2 `$01b3` := −1).
   
   On finish, SendGameMessage 15/26 send **`0x…70` to `$02af`**, the waypoint entity (the waypoint's `0x0d2` #0).
4. Chat lines 102/103 (remote actions) on `0x…2b7` (zone occupancy, sender filter `$0028`/`$0029`); `0x…2a3`
   to `$0028`; `0x…4e7` conditional.
5. Client-only UI: R22/R23 are `ClientonlyController` states. R22 → C BooleanSwitch 37 → UXViewModel 35/36
   (`0x0AC…1F3`, the HUD payload widget [?]); R23 → UIInWorld 48 (`0x09A…EA`). R1 = UIInWorld 19 (`0x09A…79`,
   offset (0, −2, 0)).

Remote states (idx → node): R0 16 `0x2785fbdd` (colour, on the cart), R1 19 UIInWorld, R2–R4 20/21/24 Stacks
`$01b3`, R5 46 Stack `$07b7`, R6–R10 49/50/54/55/57 Anims (`0x0C8…137/138/13C/137/1`), R11–R13 62/64/65
DataFlowMapping (`0x048…14F` = 0 / 0.5 / 1), R14 66 Anim, R15 79 Stack `$009a` := `$01f3`, R16 87 Stack `$02bb`,
R17 93 Stack `$0bd9`, R18–R21 110/111/120/121 Stacks, R22/R23 122/123 ClientonlyController, R24 124 `0x4ab09851`.
Remote actions: 102, 103 (chat), 109 (conditional message).

**`0xa33`** (cart): 1 sync var `$0b60`*; R0 = Effect `0x030…15D4` on the cart (on `0x…ca` / `0x…16d`), R1 =
DataFlowMapping `0x048…14E` (`$0b60` := 0.9 on `0x…2d`).

**Waypoints `0x0d2`** (def `0x15c`, STUWaypointComponent `0x1b`): each instance has its own data [L]:

| entity | pos | `$02b8` index | `$0c18` checkpoint | `$089f` | `$02b0` time bonus (s) | `$03bb` |
|---|---|---|---|---|---|---|
| `0x80009a0b` | (73.2, 7.1, −11.6) | 1 | **1** | true | 180 | 15 |
| `0x80009a22` | (131.2, 11.1, 26.1) | 2 | **2** | true | 120 | 0 |
| `0x80009a24` | (146.3, 11.0, 30.4) | 3 | 0 | false | 0 | 5 |
| `0x80009a25` | (158.7, 10.9, 33.7) | 4 | 0 | false | 0 | 0 |
| `0x80009a36` | (184.2, 13.9, 40.3) | 5 | **3** (the end) | false | 0 | 0 |

`0x0d2`: on `0x…70` (the cart arrived) → state 3 → action `0x670cf926` (`$02b0` s, capped at 31557600 s: **adds
match time**) and **broadcast `0x…71`** (the objective progress: spawn rooms `0x1ce`, spawn points `0x0d6`, doors
`0x042`, the payload `0x098` #84, effects `0x21f`, `0x0f6`, `0x0ec`), `0x…7c` (payload #108), `0x…130` (if
`$089f`), instance event, Effects R0/R1 (`0x030…41D` / `…41E`). The spawn rooms' From/Until (Maps.md §4:
Dorado 0–2, 1–2, 2–) match checkpoints 1 and 2 [?, strong].

### 12.5 Game messages on Dorado  [L]
(`game_messages.txt`; graphs running on Dorado + the brain `0x10f`/`0x1e1`.)

| message | sender | listeners | |
|---|---|---|---|
| `0x…2d` | **none** (server) | brain `0x10f` #34, payload #30/#90, `0xa33` #3, doors | match start / go (server rules layer) |
| `0x…66` | brain `0x10f` #28 (bcast) | payload #8 | start the payload |
| `0x…2cb` | brain `0x10f` #48 (bcast) | payload #104 | payload armed (state 105) |
| `0x…17` | brain `0x10f` #33 (bcast) | doors `0x042` #0 | open the attackers' doors |
| `0x…70` | payload #15/#26 → the waypoint | waypoint `0x0d2` #0 | cart reached a waypoint |
| `0x…71` | waypoint `0x0d2` #6 (bcast) | spawns, rooms, doors, payload, effects | objective progress |
| `0x…7c` | waypoint #8 (bcast) | payload #108 | → conditions 88 (Wait 45, `$07b7`) |
| `0x…ca` | payload #41 / #76 | brain #25, `0xa33` #1 | |
| `0x…18f` | payload #78 | brain #41 (filtered) | |
| `0x…16d` | payload #77 (to itself) | `0xa33` #2 | |
| `0x…18`, `0x…45`, `0x…2b7`, `0x…3d`, `0x…e3`, `0x…1a3`, `0x…2c` | **none** | payload, brain, HUD, doors | server-only (as §10.4) |
| `0x…118`, `0x…121`, `0x…12a`, `0x…192`, `0x…2a3`, `0x…4e7`, `0x…130`, `0x…1a4`, `0x…125` | payload / waypoint / HUD | **none** | to the server rules layer |

### 12.6 Live tests  [L, H]
1. **Brain**: spawned and started as in §12.3 (HUD R0) [L]. HUD text / scoreboard: not reported yet.
2. **Server cart**: `/spawn {"conn":13,"id":49,"map_index":554,"mover":{"pos":[6.009921,-0.21650034,6.4486103],"yaw":1.5707965}}`
   → `0x40000031`, def `0x77f`, components as the local cart plus `0e 10 7a` (again no `0x17`/`0x19`). Scene at
   (6.01, −0.22, 6.45), yaw π/2 [L]. The user saw it **at the start of the path**; the local cart stays at the
   origin [H].
3. `/scripts` on the server cart (entity 49): graphs `0x098` (18, 25) + `0xa33` (1, 2), active R12, R22, R23, R24
   (0-bit states). The client ran nodes 64, 122, 123, 124. The **"attack" objective icon appeared above the server
   cart** [H]. Client-only children 37/48 were not reported active.
4. **The local cart's graph runs but cannot move it**: Prometheus `execute_node` of the local `0x098` entry #8
   (`0x…66`) started state 3 and its children (trace). The user saw the **icon appear over the local cart at the
   origin** too (node 123 became active) [H]. After 10 s the rollback motion state 11 (speed −0.3) became active.
   The cart stayed at (0,0,0) [L]: nothing in the client moves a cart.
5. Prometheus `start_stop_state` on the server cart node 19 (UIInWorld `0x09A…79`) and the brain's C view models
   (`0x10f` #40, `0x1e1` #4/#6): only 19 shows as active afterwards (0 local data bits). Result [H]: the top-centre
   HUD shows the **payload progress bar** with the "P" marker at the start, and "0.0 / WAITING FOR PLAYERS". Both carts
   carry a red "P ATTACK" icon, and the Tab scoreboard works. The local cart at the origin clips into the
   geometry there (the origin is inside a building).

State data seen locally (upper bound of the remote format, which drops local-only fields): Anim 4 bits,
BooleanSwitch 1, Switch 5, DataFlowMapping / ClientonlyController / UIInWorld / `0x4ab09851` / UXScreen 0 [L].

### 12.7 Which cart, and what the server is missing for a payload push
(T055: the payload's path mover, UpdatePayload and the waypoints run offline with a host path: StatescriptNodes.md
§6.6, §6.9.)
- **The cart**: as with the doors (§8.2), the server cannot address the local cart (`0x8000xxxx`) and nothing in
  the client moves it. A **server cart** (from-map create of placement `0x22a` + movement snapshots) is the one
  that can be shown moving. The local cart is a duplicate at the origin, and it reacts to local game messages
  (icon, [H]). It cannot be removed from the network side (§8.2, T037): entry state bit 8 (`+0x30 & 8`) only
  means "local spawn pending, definition requested", the record flags have no "server-owned" bit, and the
  from-map create never deletes the local copy. Remaining idea: hide it via its own graph [?].
- **Missing on the server**, in order:
  1. **Movement updates for an existing mover.** Today only the create carries a snapshot (§8.3); a moving cart
     needs per-frame 0x12 snapshots or deltas. This needs server code (`/move` or similar).
  2. **The path**: `STUParametricMovementComponentInstanceData` of placement `0x22a`. It is in the map data, and
     the offline readers can decode it (Maps.md §2, instance data). The server also needs its own mover:
     the progress along the path, speed rules (push speed by attacker count, rollback −0.3 after 10 s, contested =
     stop), and the waypoint distances.
  3. **The rules layer** (§10.7 item 1): post `0x…2d` (start), run the brain's `0x10f` (→ `0x…66`/`0x…2cb`/`0x…17`),
     the payload's UpdatePayload (server-only class), the checkpoints (→ `0x…71` progress, time bonus) and the
     end of the match.
  4. **Statescript sync to the server cart**: sync var values (`$1a9f`/`$1aa0` near-cart players, `$0270`
     moving, `$01f3` checkpoint index, `$02bb` started), owner vars (`$01b3` direction, wheels), remote states
     R16 (started), R2–R4 (direction), R6–R10/R14 (anims), R11–R13 (wheel mapping), R22/R23 (HUD, icon). Needs the
     sync-var and owner-var sections of the remote update (§10.7 item 2).
  5. **Progress to the local world**: spawn rooms, doors and waypoint effects are local and listen to `0x…71`,
     which only a local waypoint graph broadcasts, when it receives `0x…70` from a local payload graph. A server
     cart's remote `0x098` instance runs no sends. So the local world advances only through the **local cart's**
     graph (its move states must finish at a waypoint) or not at all. Open: how a real server makes the local
     waypoints fire (§8.4 says no network path posts a game message).

## 13. Tutorial  (T043, 2026-09-26)

Status tags: [L] measured live, [G] read in code, [D] read from the install's data, [H] confirmed in game by the
user, [?] inferred. Map data `0x0800000000000661` (Maps.md §1). Data: `systems/data/live_tutorial/`
(`placements.json` = every entity placement with its definition's components, graphs and instance data, from
`tools/tutmap`; `defs/` = the definitions' decoded STU; `graphs/*.txt` = rendered graphs with the enUS strings
resolved; `sync/` = `graph_sync_info` layouts; `entities.json` = the live entity list; `traces/` = ss_trace /
ss_serialize_local captures; `tools/pm.py` = the direct-HTTP Prometheus helper). Graph ids are the low bits of
`0x0580000000000xxx`; `$xxxx` = variable `0x0D8…xxxx`; `0x…nn` = game message `0x02400000000000nn`.

### 13.1 The answer
- **Getting there works [H]**: TUTORIAL → 24001 type 4 → lobbylogic sends 23303 (game info type 4, the
  tutorial map in the settings) + 23304 + **20600 with `0x…661`**; the main menu (graph `0134`) sets
  IsJoiningTutorial (`$1a9d`, #33) and clears it when the custom-game type is 4 (#36 `0x1a163481(4)`); no page opens
  for type 4 and there is no Start, so the server sends 20600 at once (`customgame.go` `startTutorial`). The game
  server runs `/setup` itself on the tutorial when the client sends **21602** after the map load (the tutorial's
  loading screen waits for the controller + the map's networked spawn point before showing hero select; other maps
  set up on the hero pick 21605). Then the **hero select** appears, the user picks Soldier: 76 (21603) and the
  hero spawns at the tutorial's only spawn point `0x69` (−1, 0, −7), team 0 (the map has no attackers' room; the
  player gets the room's team) [H, L].
- **The tutorial is a server-run script, by design.** Its brain is entity definition **`0x128E`** (VoiceSet +
  PvPGame + `0x6be2f80c` + Statescript `0xB41`, no graph overrides) [D]. Every Athena line is a `VoiceStimulus`
  targeting `0x8f3d1c9d()`, the "all players" target set, which **has no client class** (evaluates empty;
  StatescriptConfigVarCatalogue) [D]. Run on the client, step 1 stops at its first voice line forever (the
  state never finishes) [L]. The brain also uses server-only actions (TeleportTo, BroadcastGameMessage,
  SetTeamIndex, Unkillable, ApplyTargetTags, EndMatch). A server has to run `0xB41`/`0xB55…0xB59` itself and
  replicate their effects.
- **The client does nothing tutorial-specific on its own**: no brain entity exists until the server spawns one,
  and the hero has no weapon (the tutorial graphs grant none; the weapon is hero start-up, T040) [H, D].

### 13.2 Map entities  [D, L]
326 placed records, 112 entity placements (`placements.json`); live, the game EA holds the same set (plus the
controller, hero, and our server's copies) [L]. Networked: only the spawn point `0x69` (def `0x167`).

| placed index | def | what (target tag = how the brain finds it) |
|---|---|---|
| `0x69` | `0x167` | the spawn point (team 0), graph `0x0D6` |
| `0x6a` | `0xf73` | tag `0x0D6…16D`: where the brain creates Tracer (−16, 4, 8) |
| `0x6b`/`0x6d`/`0x6e` | `0xfd8` | tag `…181` (used by the player graph `0xB40`) |
| `0x6c` | `0x1012` | tag `…195` (`0xB40`) |
| `0x6f`, `0x70`, `0x71`, `0x72`, `0x73` | `0x103c`, `0x1026`, `0x104e`, `0x104f`, `0x103e` | tags `…1BE`, `…1A9`, `…1E5`, `…1E6`, `…1BF`: step 1's Tracer spots (weapons bay, launch control, target range … [?]) |
| `0x74`–`0x76` | `0x10ee`–`0x10f0` | tags `…20D`–`…20F` (used by step 2 `0xB56`) |
| `0x77` | `0x12c8` | tag `…2A6`: the player's teleport target (brain `0xB41` #9/#21/#23) |
| `0x78`, `0x7b`, `0x7c` | `0x112d`–`0x112f` | tags `…24A`–`…24C` (used by `0xB57`/`0xB58`) |
| `0x79` + `0x7a` | `0x1300` (graph `0x2BB`) + `0x37` (capture area, graph `0x028`) | step 5's capture point at (−86.9, 3, 24.2), tag `…2A1` |
| `0x7d`, `0x7e`, `0x84` | `0x477` ×2, `0x131d` | movers with the door graph `0x042` |
| `0x7f`–`0x82`, `0x83` | `0x1310`–`0x1313`, `0x1315` | tags `…2A8`–`…2AB` (not referenced by a graph), `…2AC` (`0xB59`) |
| `0x85`, `0x86`, `0xa2` | `0x1332` | Statescript + target tag (no graph, no tag) |
| `0xd4` | `0x131f` | graph `0x0A7` (spawn-room healing, Maps.md §6) |
| 27 × | `0x1314` | breakable props with graph `0xB5C` |

### 13.3 The brain `0xB41` and the player graph `0xB40`  [D, L]
**`0xB41`** (42 nodes; 1 eligible sync var `$1720`, 1 remote state R0 = ChaseVar #3):
- entry #1 on `0x…1db` → **PlayScript `0xB40` on `0x8f3d1c9d()`** (the players). `0x…1db` is sent to
  `GetPvPEntity()` by graph `0x91B` #2 (a player-side graph) [D].
- entry #2 on `0x…1dc` (sent back by `0xB40`'s EntryPlayScript #0 → `GetPvPEntity()`): `$171b…$171f` := true,
  `$0c6b` := the players, `$1721` := 1 (**the step pointer**), ChaseVar #3 (`$1720` up to 9999999), and SubScripts
  #12…#16 = **`0xB55`, `0xB56`, `0xB57`, `0xB58`, `0xB59`, all started at once**; each waits on `$1721 == n`
  (1…5) and sets `$1721 := n+1` at its end (e.g. `0xB55` #103) [D, L: all five instances started together].
- entry #7 → `0x6c4f68a0` (10, server-only) → SubScript `0x011` (respawn countdown, as in the TDM brain).
- `$1721` := 3/4/5 + TeleportTo tag `…2A6` + broadcast `0x…71` (progress) + `0x…420`/`0x…50f`/`0x…433`/`0x…510`/
  `0x…576`/`0x…59c` to the player: the step transitions (#30–#38) [D].

**`0xB40`** (79 nodes, played on each player; 3 eligible sync vars `$1a42` (objective text), `$1a44` (show),
`$1a43`; 14 remote states):
- entry #6: throttle direction Stacks `$091d…$0920` (moved back/forward/left/right: step 2's checks),
  TrackMovement, Invisible, Stack `$0021`, **MovementMod** #14 (locks moves until `0x…420`), target tag `…210`
  on the owner, Stacks `$1880`, `$18e7`, **Unkillable** (server-only); #62 → Stack `$04be` → **SetTeamIndex(owner,
  1)** (server-only).
- **the tutorial HUD**: UXScreen #66 `0x09A…4C0` {`ui091e` = `$1a42` text, `ui091f` = `$1a44`, `ui0920` = `$1a43`},
  filtered by `$1a44`: shown only after **`0x…5bb`** (param `0x0A4…332` = the objective text → `$1a42`, `$1a44` :=
  true); `0x…5bc` hides it after 3 s [D]. Tips: client-only entry #77 on **`0x…5bd`** (param `…333` = tip text) →
  ChaseVar → UXScreen `0x09A…33B` (Tip_Text).
- "Look" checks: TrackTargets #16/#18/#24/#59 (radius 40/10/30/5 around the owner) → `$172e`, `$1742`, `$1790`,
  `$19a8`, which the steps read as `$0004:0c6b:<var>` (the player's var).
- Live [L]: played locally (Prometheus `play_script`) its entry states all begin; #66 begins, invisible
  while `$1a44` is false.

### 13.4 The steps
Each step graph interleaves Athena `VoiceStimulus` states (target `0x8f3d1c9d()`; their `onBegin` pulses an
**activity-log line** action `0xb17638d2`, their `onFinished` goes on), objective messages **`0x…5bb`/`0x…5bc` to
`$0c6b`** (the player's `0xB40`), client-only **tip** actions `0x4027e912` → `0x…5bd` under
`ClientonlyController` states, Waits, and checks on the player's vars or on LogicalButton presses. Full texts:
`graphs/0B5x.txt`.

| step | graph | objectives (`0x…5bb`) | what the player does / what is checked | creates |
|---|---|---|---|---|
| 1 | `0xB55` (179 nodes) | Look up at the dropship · Look at Tracer in the weapons bay / launch control · Look up at Tracer at the target range | look up (`$172e`; "invert your controls?" dialog `0x…587` if they look down); find Tracer 3× (`$1790`) | **Tracer**: CreateEntity def `0x001` at tag `…16D`, team 1 (`$16d4`), moved by TeleportTo between the spots, `0x992`/`0x9d1`/`0x956` played on her |
| 2 | `0xB56` (216) | Move forwards / backwards / left and right · Move around in all directions · Follow Tracer … · Jump up onto the ledge · Enter the highlighted area | throttle Stacks `$091d…$0920`, follow Tracer, jump, reach the area | |
| 3 | `0xB57` (220) | Shoot the target · Melee the target · Shoot all the targets | shoot / melee a target, destroy mobile bots (HUD intro: portrait, health, ammo) | targets def `0x1330`, bots `0x1321` ×7 |
| 4 | `0xB58` (274) | Use Sprint · Activate Biotic Field · Use Biotic Field to heal… · Fire Helix Rockets… · Charge your ultimate… · Destroy the targets using Tactical Visor | ability presses (LogicalButton), damage + ModifyHealth on the player (server-only), ult charge | `0x1321` ×3 |
| 5 | `0xB59` (182) | Move to the objective · Attack Objective A · Move into / Return to the capture area · Activate the hero select screen · Select / Confirm Tracer · View hero details | capture the point `0x7a` (graph `0x028`, progress `0x…71`), open hero select, pick Tracer; ends with **EndMatch** #0 | `0x1330` ×3 |

Per step the server has to: run the graph (voice, waits, checks), send the objective text to the player's `0xB40`
(sync vars `$1a42` text + `$1a44`, or the game message's effect), trigger the remote actions (activity-log lines)
and the tip controllers, create/move Tracer and the targets, apply the server-only effects (teleports, damage,
healing, team, unkillable), and advance `$1721`.

### 13.5 Server-side sync of the brain: live tests  [L]
- `/spawn {"conn":2,"id":48,"def":4750}` → `0x40000030` (components `01 03 0e 10 23 24 46 68 7a`); `/scripts` with
  `0xB41` (1 var, 1 remote) + `0xB55` (1 var, 21 eligible remote states) → both instances exist.
- `0xB41` R0 (ChaseVar #3) activates/deactivates from the server [L].
- `0xB55`'s eligible remote states are Effects R0–R2 (layout with a "has update" bit), Stacks R3–R15 (**1 data
  bit each**), ClientonlyController R16–R19 (#174–#177), `0x4ab09851` R20 (#178, no client class). **Nested
  states activate without their parents** (a Stack under an inactive VoiceStimulus began) [L]. A Stack sent
  without its data bit shifts every later index by one, and many at once crashed the client (AV in the remote
  state reader `FUN_7ff650f2b360`, as in T013) [L]. Send Stacks with `"data":[false]`.
- A remote update deactivates every active state of the instance that is not a remote node
  (`FUN_7ff650cedbe0`, second loop) [G, L: a VoiceStimulus started locally was aborted by the next update].
- **Remote instances don't follow links**: executing `0xB41` #2 on the server's instance links to #11/#12
  but runs neither [L]. Activity-log lines (`0xb17638d2`) and tips (`0x4027e912`) are remote **actions**;
  how a server triggers them (the packet's events section, `FUN_7ff650cee920`) is not decoded (§10.7).

- **The tutorial objective panel from the server [L, H]**: a local full update of the hero (Soldier: 76) with
  `0xB40` as instance 1 (layout `.......................C........CC`, 34 states; sync vars in slot order `$1a42`,
  `$1a44`, `$1a43`), `$1a44` = 1 and state 30 (#66, UXScreen `0x09A…4C0`, 0 data bits) active:
  `POST :3728/scripts {"conn":5,"local":true,"graphs":[{"index":2880,"sync_vars":3,"remote_nodes":14,
  "states":".......................C........CC","sync_var_ids":[6722,6724,6723],"vars":{"6724":1}}],
  "active":{"1":[{"index":30}]}}` → #66 began [L]; **the user sees the objective panel** [H] (2026-09-26 14:31).
  What shows (user screenshot): on the right, an **"OBJECTIVE"** heading with a checkbox icon and **"0"** as the
  objective line: `$1a42` still holds INT 0 and the panel prints it. The real text (`0x…5bb`'s param, e.g. "Look up at
  the dropship.") needs a string value in the sync var; that encoding is unknown. Hero in the hangar next to the
  WEAPON BAY, bare-handed (no weapon: T040) [H].

### 13.6 What the server is missing to run the tutorial
(T055: item 1 runs offline: step 1 advances to step 2 with scripted look inputs, StatescriptNodes.md §6.5, §6.9.)
1. **A Statescript runtime that evaluates the server-only nodes** (`0x8f3d1c9d` target set, VoiceStimulus
   completion, TeleportTo, BroadcastGameMessage, ModifyHealth, SetTeamIndex, Unkillable, CreateEntity for Tracer and
   targets, EndMatch): `systems/statescript` (T041) running `0xB41` + the steps for the connection.
2. **Hero start-up for Soldier: 76** (`0x3cf`: weapon, hub, HUD) = T040; our server has no layout for him.
3. **`0xB40` on the hero**: a local full update with `0xB40` (the brain's PlayScript on the player), its sync
   vars (`$1a42` is text: the string value encoding is not known yet) and states.
4. **Remote actions** (events) for the activity log and tips; **owner vars** (`$0c6b` = the player entity,
   `$1721` = step) in the remote full update: both unimplemented in `script.go`.
5. **Voice**: how a VoiceStimulus on the server reaches the client (VoiceSet component replication?) is unknown.

## 14. Hero start-up  (T040, 2026-09-26)  [G, L, D, H: HUD shown from our server]

What the server's lock-in full update must carry so that a hero it creates starts like one the client
creates itself, generated from content for all 21 heroes. [D] = read from the content (CASC + STU).

### 14.1 The answer
- The client starts an entity's default graphs itself only in `StartDefaultScripts` (`0x103acb0`, entities it
  creates): each `STUStatescriptGraphWithOverrides` of the definition's `STUStatescriptComponent`, with the
  graph's `m_publicSchema` defaults, then the overrides (StatescriptLoading.md §4). A hero body the server
  creates gets none of that: the server's full update has to carry the instances, their values and the
  states the entries begin (T016 report, "Stage 2, parts C/D").
- The server now generates that update from content: `systems/data/heroes/tools/herogen` (reads the
  install offline) → `systems/data/heroes/heroes.json` (readable) and `server/internal/heroes/heroes_gen.go`;
  `heroes.Start(def)` computes the start-up and `instance.HeroStartupFullUpdate` encodes it; `lockIn` sends it
  (replaces the hard-coded `heroScripts`; `heroGameplayEntities` is replaced by the generated
  `STUHero.m_gameplayEntity` too).
- Regenerate: build `systems/content` and `systems/stu` (cmake), then
  `cd systems/data/heroes/tools/herogen && go run .`

### 14.2 What a hero starts with [D]
Per hero (`STUHero` 0x02E… → `m_gameplayEntity` 0x040…):
1. **Instances 1..n**: the `STUStatescriptComponent` graphs in definition order, then the `STUWeaponComponent`'s
   `m_managerScript` (0x015 for every hero), then each `m_weapons[].m_script` (weapon index = position among the
   weapons that have a script, 1-based, as Prometheus' `player_spawner::attach_weapons`). No 0.8 hero uses
   `m_weapons[].m_graph`.
2. **Instance vars**: the graph's `m_publicSchema` defaults (schema order), then the definition's overrides
   (`GraphWithOverrides` field `0x953d7942`: `{0x4f6799b5 var, m_value}`), then for weapon scripts `$01c` =
   weapon index. Every schema/override config var in the 21 heroes is a literal: Int → INT, Bool → BYTE,
   Float → FLT, String → STRING, asset references (UXScreen, Texture, ContactSet, Bone, Effect) → INT64 GUID;
   a null config var → INT 0. Genji's 12 overrides come out exactly as T016 read them live (unit test).
3. **Owner vars** (entity bag): `$317`–`$31a` = FLT 1.0 (StartDefaultScripts), `$0fd` = 1 (player scripts),
   `$01d` = dictionary of the weapon indices (index → index), and whatever the entries' SetVars write there.
4. **Entries**: every plain `STUStatescriptEntry` (exact type, not client-only) is walked through its out
   plug's links: a link to a state's `m_beginPlug` begins that state; a `SetVar` whose source and target are
   literals or plain var paths (`[id]`, `[0x…03, id]`, `[0x…04, id]`) is applied in order and followed; any
   other node (conditions, other actions) needs the runtime (T041) and stops the walk. Examples: hub `0x04b`
   entry #0 copies `$152` (ult cost: Genji 1200, Soldier 1500) to the entity bag; weapon scripts set their
   ammo/cost values (Genji `0x304`: `$0c6` = 24, `$035` = `$0c6`, …).
5. **SubScript children**: a `STUStatescriptStateSubScript` start state plays `m_statescriptGraph` (always a
   literal graph for the heroes); the child is an instance with the parent's id and the SubScript's `m_states`
   index, and gets its own start (recursively; no schema, as for PlayScript children). Genji's hub `0x04b` entry
   #4: 13 start states, 12 SubScripts (children `0x1c7 0x1ca 0x1cf 0x1d8 0x215 0x246 0x2c5 0x2cf 0x352 0x5ad
   0x5ac 0xb3b`) + one `0x4ab09851`. T016's recording had a 13th child, `0xb2a`, behind a `Condition_Bool` on
   owner var `$94c` (the client sets it to true itself [L]): not reached statically.

Sizes: Genji 21 instances (9 + 12 children), 47 states begun, 25 start states left inactive (§14.4); Soldier: 76
22 instances, 33 / 22. Largest update: Bastion, 501 bytes.

### 14.3 Sync layout from content [D, checked against the live client]
- **Sync vars**: `m_syncVars` slots in order with id ≠ 0 and `0x9e2a316c` (entry +0x10) = 0 (the changed bits of
  a full update; FUN_7ff650cedf30).
- **Remote nodes**: `m_remoteSyncNodes` entries that are states (`IsA STUStatescriptState`), neither client- nor
  server-only.
- **Local state kinds** per `m_states`: `C` = `m_clientOnly`, `V` = `m_serverOnly`, `S` = exact type
  `0x7bf31629` / `0x860ee5fc` / `0xffbfa88d` (has-update layout), `.` otherwise.
- All three match the layouts read from the live client for Genji's 7 graphs exactly (the old `heroScripts`),
  sync var order included.

### 14.4 Wire format of the start-up (local full update)  [G]
Instance record (FUN_7ff650ced910): `small(id delta)`, u16 graph index, owner bit (0 = default), **parent bit:
1 → `count(parent instance)`, `count(parent's m_states index)`** (the SubScript's slot: the SubScript begin
`FUN_7ff650f50a10` sets child+0x1A = parent id, child+0x1C = the state's index). The client's own
`ss_serialize_local` writes children the same way, and T016's replay of it stuck.

**Variable value** (FUN_7ff650cef290): state reference (FUN_7ff650cf1520; `0` = none; else form bit, form 0 `u<W>
node` (crashes in the owner list), form 1 `count inst` + `small node`, then priority bit, has-extra bit [+ small]),
u4 tag, for tag 15 a u3 subtag, then (FUN_7ff650cef780):

| tag | body | client value |
|---|---|---|
| 0 | bit | INT 0/1 (the type stays INT: **BYTE cannot be sent**) |
| 1 / 2 / 3 / 4 | u4 / s8 / s16 / u32 | INT |
| 5 / 6 / 7 / 8 | u4 / s8 (as float) / half / f32 | FLT |
| 9 | u16 hi, if hi ≠ 0 `count` lo | INT64 `hi<<48 \| lo`: **bits 32–47 cannot be sent** (every hero GUID fits) |
| 10 / 11 / 12 | 2 × u16 direction / u3 mask + halves / u3 mask + f32s | VEC3 |
| 13 | bit, 1 → u32 | ENTITY |
| 14 | u3, count, small, small | INTVEC4 |
| 15/0, 15/1, 15/2, 15/3 | 4 × u16 / u32 / – / count | FLT16VEC4 / ColorRGBA / INT 0 / SS_INSTANCEID |
| 15/4–7 | array: values or key+value, delta or full | Dictionary |

**Strings cannot be synced** (a STRING reader exists, no tag reaches it). This also means the tutorial's
objective text `$1a42` (§13.6) cannot be sent as a variable. Dictionary, full key+value (15/7):
`small N`, homogeneous bit (send 0), then per entry key and value as tag + body without a state reference.
Only the low 16 bits of a var id are used; network writes are forced (the server's value wins). Owner vars
(FUN_7ff650cee050) clear the entity bag first, then `count(id)` + value until `count(0)`; the per-instance
extra list (local only) has the same format.

**State data** of an activated state (FUN_7ff650f289e0 → the mirror objects' reflected fields, then the
class's vfunc +0x1F8; in a local full update it gets local = 1, full = 1):

| state type | data | source |
|---|---|---|
| State, `0x4ab09851`, UXScreen, UXViewModel, Wait, Watch, LogicalButton, PopulateTips, DataFlowMapping, CosmeticEntity, ModifyHealth, ApplyAura, TrackStatTime, UIInWorld, ClientonlyController, `0x2168e38f`, `0x82505a1b` | none | vtable +0x1F8 and +0x208 = no-op `0x5a0c20` [G]; 0 bits on live states [L] |
| SubScript | `small(child instance)` + 1 bit (node `0xdb3a2efb`) | FUN_7ff650f50f40 [G] |
| BooleanSwitch | 1 bit: the switch value | FUN_7ff650f55560 [G]; 1 bit on 95 live states [L] |
| Invisible, TrackTargets | 1 bit | [L] |
| Switch | one bit per case (list at node +0x100, ≤ 64) | FUN_7ff650f57e80 [G] |
| Ability, Stack, TrackMovement, CameraTransform, BlinkEffect, LedgeClimb | see §14.7 (T044) | [G, L] |
| Anim, ChaseVar, HistoryBuilder | not decoded (Anim: 4 or 23 bits live) | |

The start-up activates only states whose data it knows: the no-data types, SubScripts, and BooleanSwitches
whose `m_condition` is a literal or a plain var (54 of 175; the bit = the condition's truthiness over the
start-up's values, an unset var = false [?]). Other start states are left inactive (listed per instance in
`heroes.Instance.Skipped`): 121 BooleanSwitches with expression conditions, Stack, Ability, TrackMovement,
Switch, CameraTransform, BlinkEffect; `S` states are sent as "no update". Closing that gap needs the runtime on
the server (T041).

### 14.5 Two things the first live run added  [D, L]
- **SubScript init data** (`STUStatescriptStateSubScriptBase` `0x7eb65d0d`: `{m_out_Var, m_value}`): on start
  the child gets child var := parent var. The HUD needs it: hub state 3 hands `$769` (the screen) and `$7ac`
  to child `0x1cf`, whose UXScreen state 2 shows `m_screen` = its own `$769`.
- **Branches through conditions**: `0x1cf` state 2 is reached from entry 0 via `Condition_Bool` (`0x722ea2e4`,
  gamemode entity has a component) → `NoOp`. The generator follows `NoOp`s and takes every condition it cannot
  evaluate as false (14 in all heroes, listed per graph under "assumed" in `heroes.json`) [?].
- State types without a client class of their own (73 in the node catalogue, e.g. HealthPool) run as the
  base `STUStatescriptState`: no state data.

### 14.6 Status
- Offline [D, unit tests]: generation for all 21 heroes; Genji's overrides and layouts equal the live values;
  the encoded update round-trips through an independent reader (`startup_test.go`) for every hero.
- Live, Genji on Hanamura (2026-09-26 15:58) [H, L]: **the HUD shows**: portrait, 200/200 with segments, the
  bar below, the ult meter at 0 %, two ability slots (LSHIFT, E) with their key labels but no icons.
  Wall-climb is limited (was unlimited before the start-up) but has no animation. A centre-screen circle
  (healed indicator) is shown.
- Superseded by §14.7 (T044): weapon, ammo, fire, melee, dash, deflect and double jump now start. The paragraph
  below is the state before it.
- Missing: weapon/ammo UI, primary fire, melee, dash, deflect, double jump, ability icons. These map to the 25
  start states left inactive (Ability states `0x317` #5 / `0x3a3` #1, the weapon's BooleanSwitch `0x304` #0 with
  an expression condition, `0x157`'s expression switches, CameraTransform, BlinkEffect) and to per-press
  confirmation. The **Ability state data** is not a fixed few bits: its reader (`0x10871b0`) reads an optional
  32-bit float (depending on the class of node +0x1E0) and more, after the reflected fields of its mirror object
  at +0xD0 (`0xd60a00`); it needs its own decode (or the runtime on the server, T041).

### 14.7 State data per type, and the start-up as programs  (T044, 2026-09-26)  [G, L, H]

**The answer.** The data formats of the remaining hero state types are decoded (Ghidra: each class's reader
= vtable +0x1F8, writer +0x1F0, mirror list +0x208; checked live against `ss_serialize_local` of client-started
reference heroes spawned with Prometheus). The start-up now also walks what the states it begins run
themselves (their subgraph plugs), evaluates expression conditions, and picks the current weapon. Live on
Genji [H]: weapon in hand, ammo counter, primary + secondary fire, melee, dash, deflect and double jump start
(each rolls back / rubber-bands afterwards: no per-press confirmation yet, T041). Still missing: ability icons,
the deflect afterimage.

**State data, local format** (what a local full update carries after the active bit; reader address = RVA):

| type | reader | data | at start-up | tag |
|---|---|---|---|---|
| Ability `f53c5da8` | `0x10871b0` | mirror object +0xD0's one non-skipped reflected field: **u8 flags** (+0x15C: 0x1 ?, 0x2 holds its stack var, 0x4 on top, 0x8 ?); if the node's `0x92c13ac6` is set and not a `STUConfigVarStackPriority`: u32 priority + bit (never in 0.8 heroes); **cooldown bit**, if 1: f32 cooldown (+0x150), bit, if 1 f32 rate (+0x154, else 1.0), `count` t → start time (+0x148) = (CF+1)·16 − t; **stack predecessor** (`FUN_7ff650f2b360`: bit, if 1 `count` instance + node/state index) | 10 zero bits | [G]; 10 bits `0000` on every idle Ability of two reference heroes, a dash cooldown live = 52 bits (flags 0, cooldown 0.97, rate 1.0, t = 30, no predecessor) [L] |
| Stack `f7d6498c` | `0xf55bf0` | stack predecessor (as above); if `m_priority` is set and not a StackPriority: u32 priority + bit; **on-top bit** (+0xC4, top subgraph fired), **under bit** (+0xC5) | `010` (no predecessor, on top) | [G]; `010` on every Stack of the reference heroes [L] |
| TrackMovement `8de7e50f` | `0xf55560` | 1 bit (+0x120; its mirror +0xA8 has only a skipped field) | 0 | [G, L] |
| CameraTransform `a5f89e3f` | `0xdc4990` | stack predecessor bit (then applies the camera) | 0 | [G] |
| BlinkEffect `429991a7` | `0xd68bc0` | nothing in the local format (remote: u32 + …) | – | [G] |
| LedgeClimb `f6e1e486` | – | nothing (+0x1F0 … +0x208 all the no-op) | – | [G, L] |
| Switch `26367267` | `0xf57e80` | one bit per case | needs `m_switchValue` (runtime) | [G], 4 bits live [L] |

The reflected-field walk (`FUN_7ff650f28a40`) skips fields flagged 0x8, reads 0x10 fields only in the local
format and 0x20 fields only in a full update. The Ability/Stack priority condition tests the config var class
`STUConfigVarStackPriority` (`2adc2e14`; its inheritance object is `0x1788720`, under `STUConfigVar` `0x1791340`).

**The start-up as programs** (`herogen` → `heroes.Graph.Entry` / `Progs`, run by `heroes.Start`). A firing plug
runs its links in order, depth first: begin a state; a SetVar over literals / plain var paths; a
`Condition_Bool` whose condition the server can evaluate (both branches kept, chosen at start-up; one it can't
evaluate is taken as false [?]); NoOps are followed; other actions end the path; client-only nodes are skipped.
Entries are programs, and so are the plugs a state fires when it begins: every state's `m_subgraphPlug`, a
BooleanSwitch's `m_trueSubgraphPlug` / `m_falseSubgraphPlug` by its value, a Stack's `m_topSubgraphPlug`.
Server-only states run (their subgraphs too) without being sent; a synced state whose data the start-up can't
produce stays inactive and its subgraphs don't run. **Why it matters**: the client runs the actions under a
plug of a mirrored instance itself (Genji's `$1f` := 0 appeared live), but it only begins states the server
sends. The weapon manager `0x015` keeps its whole machine in its first switch's false subgraph, and a weapon
script keeps its content in its "current weapon" switch's true subgraph.

Conditions and values the start-up now evaluates:
- **Expressions** (`heroes.Expr.Eval`, docs/StatescriptConfigVars.md §3) over plain vars, constants and literal
  config vars; a missing var reads FLT 0; opcodes that need game state (vectors, `on`, providers, dictionaries,
  random) make it unevaluable.
- `c81f8768` ("this priority may take the stack slot"): true at start-up [?] (Genji `0x157` #33: the jump branch).
- **Weapon selection**: the manager's Watch (#8) OnBegin picks the first valid weapon through an action
  (`0x17c9f80b`) the start-up doesn't model; the start-up sets owner `$1f` = `$1e` = the first weapon index. The
  weapon scripts' switch `$1f == $1c` then begins true.
- **Local-hero flags**: owner `$1a1`, `$656`, `$94c` = true, which the client's own code sets on its local hero
  (no graph writes them) [L]. Genji `0x157` #2 / #7 depend on them; the full update would otherwise clear them.
- **Short Waits settle**: a Wait with a literal timeout ≤ 0.25 s is shown finished (its on-finished program runs)
  [?]: Genji's jump LogicalButton sits behind a 0.1 s Wait.

Result: Genji 103 active states (T040: 51); across the 21 heroes 99 start states stay inactive: 56 BooleanSwitches
whose conditions read game state (throttle, owner, has-component `0x722ea2e4`, `0xc6f01de9`, entity health), 28
Switches, 13 ChaseVar, 1 Anim, 1 HistoryBuilder. Largest update 606 bytes (Bastion). Compared state by state with
the client-started reference heroes: the differences are those conditions and states behind them [L].

**Ability icons are not state data.** Soldier's ability icons are literal textures in his viewmodel; Genji's
(`0x317` #3, `0x3a3` #0, `0x1db` #0) use `STUConfigVarGetHeroLoadoutIcon(button, owner)`, which looks the owner
entity up in an entity → hero registry (the entity-admin singleton `0xd95a20` queries) and returns 0 when it
isn't there. A server-spawned hero body is evidently not registered, hence no icons [G, H]; that needs the hero
identity at spawn (follow-up).

**Update (T048, 2026-09-26) [H]: the icons show now**, on the R&D server and on the instance server alike (Genji on
Anubis and Dorado, user-confirmed). The only lock-in change since T044 common to both servers is T047's PlayerInfo
(0x3F) hero on the controller (§15.4), so that is what registers the hero for the lookup [?: by elimination; the
registry's writer was not traced]. The "hero identity at spawn" follow-up is closed by it.


**Update (T041, 2026-09-27): the rollbacks.** Every press above was undone because nothing confirmed it: the
client replays the press when the server acknowledges its input and drops the prediction 46–78 CF after it
(StatescriptRuntime.md §5.10.1). The library now runs Genji's primary fire, Shift and E exactly as the client
predicts them (§5.10), and the instance server confirms them with deltas (T051; primary fire live: ammo kept, no
rollback [L]). Server inputs this needs beyond the graphs: the StackPriority records (content), and for the dash
the owner vars `$1a1` / `$221` / `$238` / `$656` that client movement code writes (§5.10.6).

### 14.8 The "PLAY OF THE GAME / 0 / AS 0" card  (T056, 2026-09-27)  [D, L]

**The answer.** The card is not a hero-graph screen and nothing our server sends shows it. It is screen
`0x09A…AC` (key table `POTGText` = "PLAY OF THE GAME", `POTGAsText` = " AS "), shown only by graph **`0x3CA`**, and
its two texts are bound to `Name = {$01d0}` and `HeroName = {$048f}`: unset vars render as "0", hence "0 / AS 0"
[D]. In a real match `0x3CA` runs only in step 2 of the client's **post-match sequence**, which starts when the
lobby-entity var `$04aa` (match over) becomes true. On our server `$04aa` is 0 and none of those graphs run [L]. The
card we saw was a **leftover of T054's recording** `play_script 0x3ca` on the hero body `0x40000018` (the call
at 05:56:41, fixture `systems/statescript/fixtures/raw_subscript_3ca.jsonl` written 05:57:02; the user's screenshot
of the card "after a rejoin" at 05:57:25): a client-local instance (ids −1 = `0x3CA`, −2 = `0x3B6`) that nothing
stops, so the card stays until the entity or the client goes away [L: session transcripts + file times; the
recording has no stop]. The client started at 06:49 has no such instance and `$04aa` = 0 on a fresh join and after rejoins, and the user
has not seen the card since [L, H, 2026-09-27 07:10]. **Reproduced** (2026-09-27, fresh client, Reaper on Anubis):
MCP `play_script 0x3ca` on the body → instances −1 `0x3CA` / −2 `0x3B6` and the user sees "PLAY OF THE GAME / 0 /
AS 0" [L, H]; after a rejoin the instances are gone and so is the card [L, H]. So a rejoin *clears* the card, and the
original one was created by the recording right after the user's rejoin, not by it. T054's identification (graph
`0x1CF` s0, screen `0x09A…C4`) was wrong: `0x09A…C4` has no strings at all; with its `CameraTransformX/Y/Scale`
and `HideHeroElements` bindings it is the HUD's hero portrait [D; portrait: inferred].

**Where the card comes from** (graphs rendered from content, `render_graph.py`) [D]:
- Lobby graph `0x41C` (lobby EA, instance −24) starts six SubScripts on match entry: `0x129` (−25), `0x250`,
  `0x1BE`, `0x225`, `0x28C` (in-game UI root), `0xB11` [L: `list_loaded_scripts`].
- `0x129` = the **post-match sequence**. #1 BooleanSwitch `$04aa` (var id `0x0D8…04AA` in bag 4, the lobby
  entity's; Prometheus `dump_script` value 0 live [L]). True: #2 Wait 0.24 s → #5 Switch over a step counter
  `$003b` (set to 0 by #14, advanced by #13 whenever game message `0x…16B` arrives, entry #11): `$003b == 0` →
  SubScript `0x27F` (step time `$0966` = 6 s), `== 1` → `0x281` (`$0967` = 5 s), **`== 2` → `0x282`** (`$0969` = 5 s,
  `$096a` = 11.5 s), `== 3` → `0x283` (`$096b` = 20 s), `== 4` → #44 → `0x284` / `0xB5A` (`$096c` = 21 s);
  `≤ 3` → `0x280`, `3 or 4` → `0xB46`. A ChaseVar counts `$0231` down over the total.
- `0x282` = the POTG step: Entry → #0 Wait (`$0969 + $096a`, then sends `0x…16B`: next step) and #1 Wait `$0969`
  whose OnBegin begins #7 **SubScript `0x3CA`**; `cf08df77` #2 (`-1`, enum 2) also begins #7; after `$0969` a
  white UXFade (`0x09A…B1`).
- `0x3CA`: Entry → SubScript `0x3B6` (init `$04d0` = −0.3, `$0aed` = a target tag, `$1a59`), whose subgraph
  begins #0 `cdb394a5` (UXScreen with a hero model: `m_screen` `0x09A…AC`, `Name`, `HeroName`, out Skin / Offset /
  Yaw / Pitch / Roll / Anim), then CameraTransform + SetLightingLayer `0x051…15`, and PlayScript `0x236` / `0x160`
  (the highlight's animation) by whether `$0401` (the anim) is set.
- **`$04aa` has no writer in any graph**: of the 370 graphs only `0x129`, `0x1CB`, `0x1E5`, `0x250`, `0x278`, `0x28C`
  read it (menu state, `0x28C` #49, …); the client's code sets it (config var `3c640582` reads it through the lobby
  EA, docs/StatescriptConfigVarCatalogue.md) [D]; which code and on which server message was not traced [?].

**What a real server does**: nothing, until the match ends; then whatever makes the client set `$04aa` (with the
game messages `0x…16B` stepping the sequence, sent by the step graphs themselves) and the POTG data behind `$01d0`
/ `$048f` / `$0401` / `$04b6` (name, hero, highlight anim, model). That is match-end work (MatchHost, T057), not a
defect of the join.

**Test tooling note**: `play_script` instances are client-local and are not stopped by the tool; stop them (or
restart the client) after a recording, or they stay on screen.

## 15. Match init and the join / lock-in sequence  (T047, 2026-09-27)  [G, L, H]

### 15.1 The answer
- **The world is built once per match, when the match is created, and each joining client receives it when
  its map has loaded (21602).** `server/internal/instance/world.go`: `Assign` (the lobby's hand-off, before any
  client connects) builds the map's `World` (the networked placements as from-map creates, then the spawn points'
  team updates, Maps.md §3–4) if the match has none; on the client's **21602** the server sends the player's
  controller (+ 20301) and replays the world, one entity per packet. This is the same on every map, tutorial
  included (the old triggers, the hero pick 21605 and the tutorial-only 21602, are gone). A second connection or a
  rejoin gets the same world [L: rejoin on Anubis, conn 25 joined the world conn 24 had left]. The tutorial goes
  through the same code (its join was already on 21602 before, T043), but **no tutorial join has been run on this
  build yet** [?: to check]. Start spawn points are
  handed out per world, so players in one match start on different points. `GET :3728/worlds` lists them.
- A match is keyed by its **map** for now: the lobby's assignment carries no match id. A world ends with its last
  connection (after the release grace); lobbylogic's stale assignments (re-sent in its hello) keep old worlds
  alive, which is harmless while the world is static.
- **The black screen after the lock-in was a 5 s fallback timer [L, H].** Hero select's exit (lobby graph
  **`0x9D0`**, see §15.3) waits until **PlayerInfo (0x3F) +0x98 of the local player = the picked hero**; our server
  never set it, so only the 5 s fallback closed hero select (0.5 s + 5.0 s: the user's "5–6 s of black"). The lock-in
  now sends a PlayerInfo mirror update with the hero on the controller (`Mirror3F`), and hero select closes 0.5 s
  after the confirm (trace: switch #10 true at once) [L]; **the user sees no black screen any more** [H,
  2026-09-27 18:10].

### 15.2 The join timeline  [L: server log + ss_trace/ecs_trace of a join, 2026-09-27 18:09, Anubis, Genji]
Lobby graphs: `0147` loading screen (instance −3), `028C` in-game UI root (−25), `09D0` hero-select flow (−29),
`09CF` hero-select screen (−31, SubScript #42 of `09D0`). t = time after 21602.

| t | client | server | client state |
|---|---|---|---|
| −2.0 s | lobby 24013 (Start) → lobbylogic 20600 {map, addr, conn, key} | game server: connection assigned (the match's world already exists) | `FUN_7ff650c9f980` (map load start) posts `0x…09B`/`0x…117`/`0x…09E`/`0x…23F`: `0147` shows (states 1, 3), `028C` + `09D0` start, `03F2` waits |
| −2.0 s | UDP SYN → data | SYN\|ACK, keepalive ACKs, pongs (20304 for 21608) | map loading (Maps.md §8) |
| −0.03 s | **21601** {vec3, vec4, bool} | – | map world callback `FUN_7ff650c9fb60` (table `0x7ff6515a2630`) |
| 0 | **21602** (empty) + 21605 {0} | **join**: controller `0x…0E` (flags 0x38) + 20301, then the world (22 from-map creates + 18 team updates on Anubis) | map world "finished" callback `FUN_7ff650c9fc20` (table +0x58) posts `0x…09C` and sends 21602 |
| +16 ms | – | – | `0x…40B`, `0x…07B`, `0x…124`, `0x…0E2`: `0147` aborts (**the loading screen hides by itself**, it does not wait for the server), `09CF` hero select starts |
| +0.1 s | command frames (input) | input ack in every data packet | the controller exists: CF loop runs |
| +1…5 s | **21605** {hero, bools 1,0} per hero click (preview) | nothing (see 15.4) | `09CF` preview states |
| +6.0 s | **21605** {0, bools 1,1} + **21603** {hero, skin `0x0A5…`, bool} (confirm: `09D0` #0 `UXSendHeroSelection` behaviour 1) | hero body create (+ 0x12 snapshot at a start spawn, health, team), controller update (0x20 link, 0x10 team, **0x3F hero**), hero Statescript start-up (§14) | `09D0` #11 `$0842` := 1 → #14 Wait 0.5 s (#15 `$0861` := 1: the screen fades) |
| +6.5 s | – | – | `09D0` #10 BooleanSwitch true → #4 `$03d5` := 0 → #47 false: hero select (`09CF`) torn down; #19 true → #18 Wait 1.2 s → #16 Wait 4 s with Stack `$07c3` (tips) → playing |
| (before the fix) +6.5…11.5 s | – | – | #10 false: its subgraph Wait #49 (5 s) → #48 `$03d5`? → #4: the same exit 5 s later (black) |

### 15.3 What hero select waits for  [D, G, L]
`09D0` (67 nodes, rendered from content): #1 `UXHeroSelectScreen`, #42 SubScript `09CF`, #0/#8/#21/#34
`UXSendHeroSelection` (`c43f6a1b`: `m_hero` `$03ac`, `m_skin`, `m_behavior` 0 = preview → 21605 {hero, bool, 0},
2 = 21605 {hero, bool, 1}, 1 = confirm → 21605 {0, 1, 1} + 21603 {hero, skin, bool}, 3 = 21604 {hero};
`FUN_7ff650d574b0`; message vtables `0x7ff6515eff08` 21603, `…ff28` 21602, `…ff68` 21605, `…ffa8` 21604, `…ffc8`
21601).
- #10 BooleanSwitch: `(GetEntityHero(GetLocalPlayer()) == $03ac) ? (0xd48e6dfc(GetLocalPlayerPrimaryPossessable()) ||
  (exists && possessable.$01de)) : (exists(GetLocalPlayerPrimaryPossessable()) && possessable.$01de)`; onTrue → #4
  (close); subgraph → #49 Wait 5 s → #48 → #4 (the fallback).
- `GetEntityHero` = the entity's **PlayerInfo (0x3F) +0x98**; `GetLocalPlayerPrimaryPossessable` = the controller's
  0x20 +0x98 (the lock-in link); `0xd48e6dfc` = !(comp 0x01 +0x180 & 1) (the body is shown).
- #59/#60/#62 `0x722ea2e4()` (the gamemode entity has a certain component) gate `0x8f76a28c` actions that tell the
  PvP entity about the pick (behaviour 0/1/2); false on our server today (no gamemode entity).

### 15.4 PlayerInfo (0x3F): what the server sends  [G, L]
Mirror STU `0xDB2735A9` at component +0x20 (field order = the mirror type's): u64 +0x78 `0x769a0079` **hero**
(component +0x98; `GetEntityHero`, `GetEntityHeroName`), u64 +0x80 `0x252d06e1`, u64 +0x88 `0x28bb6bce`, u64 +0x90
`0x065c8044` **pending hero** (component +0xb0; the hero asset prefetch `FUN_7ff650be5ff0` streams it, and
PlayerInfoEventHookSystem stamps +0xC8 when it changes), i32 +0x98 `0xe7c9adfb`, i32 +0x9c `0xf4ef993c`, u8 +0xa0
`m_slot`, u8 +0xa1 `0x8adc2877`, +0xa2 `0x3dd1f3d1` (the prefetch ignores the recency limit when set), +0xa3
`0x8030d888`. Encoding in an update: u64 compressed (as 0x10), i32 32 raw bits (`FUN_7ff650591a50`), u8 8 raw bits
(`FUN_7ff65066b660`). `Mirror3F` in `stream.go`; the lock-in sends the hero only (read back live: +0x98 = the hero [L]).
Not sent yet [?]: the pending hero on 21605 previews (would prefetch the hero's assets during hero select), the slot
and the flags.
