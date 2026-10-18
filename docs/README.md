# Documentation

Reverse-engineering notes for the Overwatch beta 0.8 client (build 24919) and the reimplementation in
`systems/` (plan: [`systems/README.md`](../systems/README.md)).

**Conventions**: addresses are RVAs (runtime base `0x140000000`, Prometheus `gameBase + RVA`); Ghidra
address = `0x7ff64ffe0000 + RVA`. Status tags: [G]/[M] read in code, [L]/[H] confirmed live, [?] inferred.

| document | about |
|---|---|
| [EngineManagers.md](EngineManagers.md) | the engine's 41 manager slots (memory, threads, storage, resources, render, …), start/shutdown |
| [ResourceLoading.md](ResourceLoading.md) | resource GUIDs, the Resource Manager load pipeline, TACT/CASC, asset packages, loading data ourselves |
| [ContentAccess.md](ContentAccess.md) | reading the game's content offline: local CASC, TACT, APM manifests and asset packages, keys; the reader library `systems/content/` |
| [STU.md](STU.md) | STU: the STUD v3 asset format, parser, type registry, mirror data, offline parsing; the reader library `systems/stu/` (§7) |
| [ECS.md](ECS.md) | entity admins, entities, components, systems, lifecycle, tick; replay-verified |
| [ECS_Components.md](ECS_Components.md) | catalogue of all 118 component types (generated) |
| [ECS_Systems.md](ECS_Systems.md) | catalogue of all 71 systems (generated) |
| [InstanceServer.md](InstanceServer.md) | the `instance` game server (`server/cmd/instance`, `server/inst/`): packages, match lifecycle, replication, the 16 ms tick and clock steering, control API, live results |
| [CommandFrames.md](CommandFrames.md) | fixed 16 ms command frames, the client clock, time dilation, what the server controls |
| [Movement.md](Movement.md) | character movement: the per-CF pipeline, the input stream, the character mover (stages, formulas, per-hero parameters, controllers, modifiers), the `dm` collision engine (capsule, plane solver, BVH mesh, filters), the movement record and reconciliation (exact compare), recordings for a future library |
| [Physics.md](Physics.md) | the physics engine (Blizzard "Domino", `dm`): ids and API, world / body / fixture / contact layouts, broadphase, step, island solver, contact solver, CCD, joints, events, threading; how Overwatch uses it (map, props, ragdolls, characters, clocks); comparison with Box2D v2.4 / v3.2 and Box3D |
| [Statescript.md](Statescript.md) | Statescript as described in the GDC 2017 talk (Dan Reed) |
| [StatescriptLoading.md](StatescriptLoading.md) | which graphs are loaded when, how instances start |
| [StatescriptRuntime.md](StatescriptRuntime.md) | the client's Statescript runtime: data model (graph, nodes, instance, sync manager, vars, RTTI), execution semantics (drivers, clock, notifications, links, states, messages), trace fixtures |
| [StatescriptSync.md](StatescriptSync.md) | the Statescript sync protocol as the client implements it: wire grammar (full/delta, instances, vars, remote states, actions), state-data format of every type, the prediction/rollback model, what a server must send per tick; shadow-compare results |
| [StatescriptConfigVars.md](StatescriptConfigVars.md) | how config-var values are computed: the evaluator interface, the two expression interpreters (opcodes, typing, equality), worked examples |
| [StatescriptConfigVarCatalogue.md](StatescriptConfigVarCatalogue.md) | per-type table of the 278 used config-var types (generated): inputs, output type, what it computes, what it reads, client class yes/no |
| [StatescriptNodeCatalogue.md](StatescriptNodeCatalogue.md) | catalogue of all 719 node / config-var types (generated; 399 named, names + provenance in `systems/data/statescript/statescript_names.csv`), which have a client class, which are server-only |
| [StatescriptNodes.md](StatescriptNodes.md) | node-type behaviour as implemented in `systems/statescript` (T054/T055): per-type spec with tags, press fidelity (§3), match and objective families (§6: MatchHost boundary, capture, filters, brains, payload, offline runs, coverage) |
| [Maps.md](Maps.md) | the map reference: catalogue, the full map-data format (all record types, collision, AI, sound), which placements the server creates, spawn rooms and teams, choosing a map, composition, the load sequence, tools (`systems/maps`, explorer Map tab), editing maps (writer + runtime injection, proven in game) |
| [MatchLogic.md](MatchLogic.md) | where the gamemode and objective logic lives (resident on the client, driven by the server) |
| [TitleScreen.md](TitleScreen.md) | the lobby side: title-screen scene (component 0x54, LobbyWorld state, title maps), the featured-hero graph and what 24900 must carry, 20503/20504, catalogue of every `ClientInLobby*` message |
| [LobbyScripts.md](LobbyScripts.md) | every Statescript graph on the lobby entity: instance tree, the page switch, per-graph states / triggers / gates (JAM message at the root), unreachable pages and why, the "custom server" strings |
| [DataFlow.md](DataFlow.md) | the DataFlow system: assets (0x013), component 0x03 (value store, evaluation per render frame, inheritance), producers (components, Statescript DataFlowMapping, effects), consumers (effects, draw constants, animation, sound, camera), networking (not replicated) and worked examples (T068) |
| [UISystem.md](UISystem.md) | the client's UI: screen assets, UX states and view models, screen build and hosts, the runtime-resource path, the three routes to our own UI and a runtime-rewired button prototype |
| [Explorer.md](Explorer.md) | the storage explorer desktop app (`explorer/`): architecture, views, how to add a view |
| [AssetFormats.md](AssetFormats.md) | non-STU asset formats: textures + payloads (header, formats, payload layout, split blocks, delta skins; §1, T063); models (buffers, submeshes, LODs, skeleton; §2), materials (§3) and model looks (§4) (T064); audio: WEM → Wwise-Vorbis → Vorbis, sounds, banks, switch / state groups (§5, T065) |
| [Rendering.md](Rendering.md) | the renderer (T070): render thread + 2 recorded render buffers, D3D11.0 immediate context, persistent render world + per-frame view packet, culling (size-class SIMD, CPU occlusion buffer, HW queries, LOD), draw items / 20 passes / sorting / instancing, the deferred frame (6-RT G-buffer, HBAO+, light volumes, CSM + shadow atlas, Enlighten GI, SSLR, froxel fog, outlines, post), shaders (material → group → instance → DXBC), effects, UI canvas, GPU resources |
| [DebugDrawing.md](DebugDrawing.md) | in-game debug drawing: the engine's surviving debug renderers (PrimDraw world shapes, debug-overlay text / 2D, 10 built-in overlays), Prometheus Debug Draw (window + MCP `debug_draw`), the server → Prometheus channel (`GET /debug/draw`) (T050) |
| [JAM.md](JAM.md) | JAM serialization and routing (GDC 2013 talk) and our implementation: the message catalogue (`systems/data/jam`), the generated Go codecs (`server/inst/jam`), the serialization boundary |
| [PrometheusChanges.md](PrometheusChanges.md) | what this project changed and added in Prometheus (the injected RE tool in `prometheus/`) |
| [gdc/README.md](gdc/README.md) | the GDC talks the Statescript and JAM work is based on |
| [early-notes/README.md](early-notes/README.md) | the first notes, trimmed to what the documents above don't cover: Battle.net login, the lobby handshake and stream cipher, static-analysis tips, early lobby and instance-link details (21800, social/chat UI messages, feature flags, kiosk mode, UDP states, mirrored component updates) |

Data dumped from the client lives in `systems/data/` (STU registry, live snapshots, `live_anubis/graphs/`: all 296 resident graph images, `statescript/`: the node catalogue) and
`systems/ecs/manifest/` (ECS). These dumps are not published (only the generators in `systems/data/*/tools/`);
reproduce ECS data with `systems/ecs/tools/live.py`.
