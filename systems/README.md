# systems/

Clean-room C++ reimplementations of Overwatch beta 0.8 (build 24919) engine
systems, written from reverse engineering of the client. Everything here is
code we own and control.

## Long-term goal

A **game instance server capable of serving real Overwatch matches**. The
client is the specification: each system we rebuild has to *behave* like the
game's, so that the unmodified client accepts what the server simulates.

`systems/` holds those rebuilt engine systems as standalone C++ libraries (`ecs/`, `stu/`, `content/`, `statescript/`, `maps/`, …) (no
Windows or game dependencies), each verified against recordings of the real
client, and exposed through a C API so the Go server (`server/`) can link them
via cgo. `server/` stays the network/lobby/orchestration layer; `systems/` is
the simulation and content layer it drives.

## Roadmap (rewritten 2026-09-27)

### Systems: status

Status: **done** = implemented and replay- or install-verified; **RE'd** = reverse-engineered and documented;
**partial** = some of it built; **known** = located, partly understood.

| # | system | status | where |
|---|---|---|---|
| 1 | **ECS** (`ecs/`): entity admins, entities, components, systems, lifecycle, tick | **done** | docs/ECS.md, ECS_Components.md, ECS_Systems.md |
| 2 | **Command frames** (in `ecs/`): clock, dilation, resync, CF listeners | **done** | docs/CommandFrames.md |
| 3 | **Server tick + clock steering**: 62.5 Hz loop, P / input ack / dilation flags, resync policy | **done** in `instance` (T048): lead 3–4 frames steady, steering measured | docs/CommandFrames.md §4–6.1, InstanceServer.md |
| 4 | **STU reader** (`stu/`): STUD v3 + client type registry, C API + Go | **done** | docs/STU.md |
| 5 | **Content access** (`content/`): CASC/TACT/APM, every resource by GUID, C API + Go | **done** | docs/ContentAccess.md |
| 6 | **Statescript runtime** (`statescript/`): data model, execution, config vars/expressions, 5 replay fixtures | **partial**: runtime done; node coverage from data: **0 unmodelled on server paths** (heroes + brains + objectives, T060); 12/12 Genji press recordings exact (T054); match/objective families and movement/collision/history/time-scale hooks behind `MatchHost` (T055, T060); server-only behaviour is designed [?]; server-side hosts (health, movement, collision, match) missing | docs/StatescriptRuntime.md, StatescriptConfigVars.md, catalogues |
| 7 | **Replication (MirrorSystem)**: creates/updates/destroys, component mirror data, Statescript sync | **partial**: ad-hoc encoders in the Go server (creates, movement snapshots, full updates, hero start-up); general STU-reflected mirror data, deltas, remote actions/events, owner + string vars missing | docs/MatchLogic.md §5, §8–§9, §14; `server/internal/instance/{stream,script,startup}.go` |
| 8 | **Maps** (`maps/`): full map-data format, placements, spawn rooms, load sequence, parser/writer, runtime injection | **done** (RE + tools); server world model from map data missing | docs/Maps.md |
| 9 | **Match / gamemode**: brains, objectives, teams, spawns, tutorial; what the server must drive | RE'd (model complete: MatchLogic.md §10–§15); brain/objective graphs **run offline** in `statescript` behind a `MatchHost` (T055: Anubis A→B capture, Dorado escort, TDM, tutorial step 1→2; capture/payload rates designed [?]); **live on `instance`** (T057: Anubis brain B + server copies of the points, capture seen in game); HUD unlock, texts, ground ring, end of match open | docs/MatchLogic.md, StatescriptNodes.md §6, InstanceServer.md §12 |
| 10 | **Movement**: input, character mover, movement state, reconciliation | **RE'd** (T062: input stream decoded, mover formulas reproduce 13 client recordings, per-CF quantisation, exact-compare reconciliation → a server mover must be bit-exact); library not built | docs/Movement.md, CommandFrames.md |
| 11 | **Combat**: health, projectiles, weapons, hit reporting | known; all server-side nodes missing | docs/ECS_Systems.md, StatescriptNodeCatalogue.md |
| 12 | **Heroes**: definitions, start-up (overrides, weapons, hub, ability state data) | **done** for start-up (`server/internal/heroes`, 21 heroes); abilities need press confirmation (T041) | docs/MatchLogic.md §14 |
| 13 | **JAM messaging**: lobby catalogue complete; instance protocol partly | **partial**: lobby done (LobbyServer.md, TitleScreen.md); in-instance catalogue and a proper JAM layer missing | docs/JAM.md, LobbyServer.md |
| 14 | **UI**: screens, view models, runtime rewiring, injection route | RE'd (UISystem.md); STU writer missing for our own screens | docs/UISystem.md |
| 15 | **Physics / collision**: static collision mesh, character/projectile collision on the server | **RE'd** (engine: Blizzard Domino = a 3D Box2D v2.4 step/solver with Box3D-style data design, T069; character: T062) (T062: Blizzard's own `dm` engine, Box2D-v3-like plane solver, capsule/filter measured); nothing built; `systems/maps` CollisionMesh has tris/bvh swapped (fix pending) | docs/Movement.md, Physics.md, Maps.md §2.6 |

### Phases towards a playable server

The rules of every mode live in server-only Statescript nodes the client doesn't ship (MatchLogic.md
§10–§13, §6 of Maps.md): a real server **is** a Statescript host over a replicated world. The phases build that.

**Phase A: one player in a live world** (the new `instance` server skeleton is built here)
- A1 server tick + clock steering: **done** (T048, `instance`).
- A2 server world model: **partly** (T047/T048: world once per match on `owecs`, networked placements, spawn rooms, teams); all placements/volumes/path data as server entities still to do.
- A3 replication generalised: component mirror data through STU reflection for the component set we use; deltas; remote actions/events; owner and string vars; per-connection state.
- A4 Statescript on the server (T041): the runtime drives hero and brain instances; a minimal server-node set (target sets, ModifyHealth/HealthPool/Invincible as data, Criteria, respawn); presses confirmed by real deltas; hero identity for icons.
- A5 input decoded; movement **client-authoritative for now** (the server records positions).
- Exit: spawn, move, fire and abilities without rollback, HUD, objectives shown but static; all maps and the tutorial's entry.

**Phase B: rules**
- B1 the remaining server-only nodes for combat and objectives; damage and health; projectiles as CF-stepped entities.
- B2 capture from zone occupancy; payload along its path (parametric movement, no physics); doors and spawn rooms from the brain; teams, respawn, match phases, end of match, stats; the tutorial's steps (voice targeting, objective text needs the string-var path).
- Exit: a solo Assault or Escort match, and the tutorial, play to a result.

**Phase C: multiplayer**
- Several clients, interest management, deltas at scale, hit registration with lag compensation, movement reconciliation (server-authoritative movement), lobby orchestration (party → match, teams, picks, join/leave, match lifecycle), in-instance JAM (chat, pings).

**Phase D: physics fidelity**
- Character collision against the map mesh on the server, projectile collision, then a short investigation of the engine's physics before deciding whether to extract or reimplement it.

**Tooling alongside**: explorer (T046: textures, models, audio, dependency graphs), STU writer (our own UI screens, header edits, the explorer as editor), Prometheus `logical_button` (T042), debugger (T039), custom JAM messages via Prometheus (T033).

### Agreed order of work

Phase A first, in a **new server skeleton** (`instance`, Go, properly structured, hot reload) rather than in the
R&D game server, which stays as the reverse-engineering harness. A1–A3 are the frame; A4 is the heart and the
first real consumer of `systems/statescript`. 

## Fidelity rule

**Match behaviour and identifiers, not memory layout.**

Must match the client exactly:
- component ids, system identity and **tick order**
- subscription semantics and callback **ordering** (create / add / remove / delete)
- entity id format, parent/child relations, deferred-deletion behaviour
- fixed-step timing (16 ms command frames) versus variable-rate ticks

Free to differ:
- struct offsets, vtable shapes, MSVC ABI, allocators

Exception: where layout **is** the protocol (mirror data, STU, network
serialization), we reproduce it byte-exactly, but in a dedicated serialization
layer, not in the in-memory ECS types.

## How we work

1. **Ghidra (static):** decompile the EntityAdmin tick, entity create/destroy,
   and the component/system registration paths. Record findings in
   `docs/ECS.md` (same style as `docs/JAM.md`, `docs/Statescript.md`).
2. **Prometheus MCP (live):** dump the real runtime state (systems per admin,
   subscriptions, component registry) into a machine-readable manifest
   (`ecs/manifest/`). This is the ground truth for ids and ordering.
3. **Traces (live):** temporarily hook system callbacks in Prometheus and
   record event sequences (map load, hero spawn, deaths). These become golden
   fixtures that the reimplementation must replay identically.
4. **Implement + test** in the standalone project against those fixtures.
   Nothing counts as "done" until it matches a trace or is confirmed in game.

### Driving the client without the UI

- Start the client: `./build-and-run-with-server.fish` (it logs in automatically and ends on the title screen).
- Send it into a match: `curl -X POST --data 'command("instance")' http://127.0.0.1:3726/eval`. This goes through the lobby gateway's Lua `inject()`, which runs the lobbylogic `!instance` chat command.
- Check the game connection: `GET http://127.0.0.1:3728/conns`.
- Record ECS events: Prometheus MCP `ecs_trace` (`start` / `fetch` / `stop`).

## `ecs/`: status and layout (details: [`ecs/README.md`](ecs/README.md))

The ECS core is implemented and **replays every recorded client trace exactly** (see
`docs/ECS.md` §7): spawn with dependencies, children and id allocation, inline entities, system
matching and callback order (including duplicates), deferred create/destroy queues, the tick,
and delete gating by `CanDelete`.

```
ecs/
  CMakeLists.txt       # C++20, gcc/clang, no dependencies
  include/ecs/         # entity_admin.h (core), entity.h, component.h, system.h, loader.h, trace.h,
                       # manifest.h (client component types + per-EA systems as stand-ins)
  src/                 # entity_admin.cpp, manifest.cpp, capi.cpp
  capi/ecs.h           # extern "C" API (cgo / ctypes)
  generated/           # manifest_data.h, from tools/gen_manifest.py
  manifest/            # live dumps (Prometheus ecs_dump) + component_storage.json
  fixtures/            # client traces (Prometheus ecs_trace): map load, /setup
  tests/               # C++ behaviour tests + replay_fixture.py (trace replay through the C API)
  tools/live.py        # drive the client: launch/stop/instance/setup/dump/trace/record
  tools/gen_manifest.py
  go/                  # cgo wrapper (package ecs) + smoke test
```

Build and test:

```
cmake -S systems/ecs -B systems/ecs/build -G Ninja && cmake --build systems/ecs/build
ctest --test-dir systems/ecs/build            # behaviour tests + fixture replay
(cd systems/ecs/go && go test ./...)          # cgo
```

Refresh the ground truth: `systems/ecs/tools/live.py record` (launches the client if needed; the
servers must be running), then `tools/gen_manifest.py`, rebuild, `ctest`.

Stand-ins: `manifest::DataComponent` and `manifest::ManifestSystem` carry no game logic; real
components and systems replace them one by one (Statescript next).

## Integration with the Go server (`server/`)

Each library exposes a plain C API (opaque handles, POD structs, no exceptions
or STL across the boundary) and a cgo wrapper, so `server/internal/instance` can link it:

| library | C API | Go module / package |
|---|---|---|
| `ecs/` | `ecs/capi/ecs.h` | `owecs` / `ecs` (`ecs/go`) |
| `stu/` | `stu/capi/stu.h` | `owstu` / `stu` (`stu/go`) |
| `content/` | `content/capi/content.h` | `owcontent` / `content` (`content/go`) |

ECS calls are coarse-grained: roughly one `ecs_admin_tick()` per 16 ms command
frame with batched inputs, never per-field access. The instance server's
planned 62.5 Hz tick loop (see netcode notes) is what will drive it. STU documents
are opened once per resource and walked by field hash (docs/STU.md §7). The content store is opened once
per process (~0.2–0.7 s) and read by GUID from any goroutine; its bytes feed `stu.Open(b, stu.File, 0)`.

Each library is its own CMake project with its own build directory; the cgo wrappers link the static
archives from there (`<lib>/build/libow_<lib>_c_static.a` + `libow_<lib>.a`), so build the C++ side first.
Target and symbol names are disjoint (`ow_ecs*`/`ecs_*`/`namespace ecs`, `ow_stu*`/`stu_*`/`namespace stu`,
`ow_content*`/`content_*`/`namespace content`): all three link into one Go binary (checked with a program
importing all three; content also needs `-lz`, which its cgo flags add). Build and test everything:

```
cmake -S systems/ecs -B systems/ecs/build -G Ninja && cmake --build systems/ecs/build && ctest --test-dir systems/ecs/build
cmake -S systems/stu -B systems/stu/build -G Ninja && cmake --build systems/stu/build && ctest --test-dir systems/stu/build
cmake -S systems/content -B systems/content/build -G Ninja && cmake --build systems/content/build && ctest --test-dir systems/content/build
(cd systems/ecs/go && go test -count=1 ./...); (cd systems/stu/go && go test -count=1 ./...)
(cd systems/content/go && go test -count=1 ./...)   # reads game/owbetaclient ($OW_INSTALL), skipped if absent
```

A Go module uses a wrapper through a `replace` directive, e.g. in `server/go.mod`:
`require owstu v0.0.0` + `replace owstu => ../systems/stu/go`, then `import "owstu/stu"`. `owcontent`
requires `owstu` (its tests parse with it), and `replace` only applies in the main module, so a module
importing `owcontent` needs the `replace` lines for both.

## Reverse-engineering reference

- Runtime (Prometheus): game base `0x140000000`, so **RVA = addr - 0x140000000**.
- Ghidra (`ghidra-project`): image base `0x7ff64ffe0000`, so
  **Ghidra addr = 0x7ff64ffe0000 + RVA**. Verified by matching code bytes.
- Prometheus MCP HTTP endpoint: `POST http://127.0.0.1:17888/mcp/tools/call`
  (bridge: `prometheus/bridge_mcp_prometheus.py`).
- Existing RE'd structures: `prometheus/Prometheus-Core/entity_admin.h`,
  `Components/Component.h`, `Systems/SystemBase.h`,
  `UI/windows/entityadmin_systems_window.h` (system vtable RVAs).

## ECS findings so far

Details: [`docs/ECS.md`](../docs/ECS.md); catalogues [`docs/ECS_Components.md`](../docs/ECS_Components.md),
[`docs/ECS_Systems.md`](../docs/ECS_Systems.md) (`ecs/tools/gen_docs.py`); command frames, clock and time
dilation: [`docs/CommandFrames.md`](../docs/CommandFrames.md). Live snapshots: `ecs/manifest/ecs_{game,session,lobby}.json`,
regenerated with the Prometheus MCP tool `ecs_dump`. Key points for the reimplementation:

- Three EAs (game, session, lobby), each ticked once per render frame. Fixed 16 ms CFs run inside systems.
- `Tick`: counter++, destroy flush, created flush, then systems in explicit registration order, with global pre/post hooks.
- Deferred structure changes: spawn fires `OnEntityCreated` immediately, but iteration lists and `OnEntityAdded` wait for the next flush. Deletion is two-phase (`OnEntityDeleting` now, `OnEntityRemoved` at the flush) and gated by component `CanDelete()`.
- Components: ids < 0x8f in this build (mask supports < 192), auto-added dependencies, per-entity list sorted by id with O(1) rank lookup via a bitmask.
- Subscriptions: per-component subscriber lists with no de-duplication; a system is matched once per subscribed component the entity has.
- System names are stripped; systems are identified by vtable RVA.
- Two creation paths: `Spawn` (pool entity, dependencies, callbacks, queues) and `SpawnInline` (entity + inline components in one allocation, no callbacks; map geometry, created from loader threads too).
- Systems act across EAs, components delete entities they own, and the id counter is shared by racing threads.
