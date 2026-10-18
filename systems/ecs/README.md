# systems/ecs: entity admins, entities, components, systems, command frames

Clean-room reimplementation of the beta 0.8 (build 24919) client's ECS core and its fixed-step command
frames. It replays every recorded client trace exactly (spawn with dependencies, children and id
allocation, inline entities, system matching and callback order, deferred create/destroy queues, the
tick, delete gating by `CanDelete`, the 16 ms command-frame clock). Findings and semantics:
[docs/ECS.md](../../docs/ECS.md), [docs/CommandFrames.md](../../docs/CommandFrames.md); catalogues
[docs/ECS_Components.md](../../docs/ECS_Components.md), [docs/ECS_Systems.md](../../docs/ECS_Systems.md).

```
cmake -S systems/ecs -B systems/ecs/build -G Ninja && cmake --build systems/ecs/build
ctest --test-dir systems/ecs/build                   # behaviour tests + replay of the client traces
(cd systems/ecs/go && go test -count=1 ./...)        # cgo wrapper (needs the build above)
```

## Layout

| path | what |
|---|---|
| `include/ecs/` | `entity_admin.h` (core), `entity.h`, `component.h`, `system.h`, `loader.h`, `command_frame.h`, `command_frame_systems.h`, `trace.h`, `manifest.h`, `types.h` |
| `src/` | `entity_admin.cpp`, `command_frame_systems.cpp`, `manifest.cpp`, `capi.cpp` |
| `capi/ecs.h` | `extern "C"` API: opaque handles, POD structs, no exceptions across the boundary (cgo, ctypes) |
| `go/` | module `owecs`, package `ecs` (cgo wrapper) + smoke test |
| `generated/` | `manifest_data.h` from `tools/gen_manifest.py` (component types, per-admin systems) |
| `manifest/` | live dumps from the client (Prometheus `ecs_dump`): `ecs_{game,session,lobby}.json`, `components.json`, `systems.json`, `component_storage.json`, `command_frame_listeners.json`; `analysis/` raw per-admin component and system dumps |
| `fixtures/` | client traces (Prometheus `ecs_trace`): map load (Anubis), `/setup` controller spawn, command frames; plus dumps at the start and end of the map load |
| `tests/` | C++ behaviour tests (`test_lifecycle.cpp`, `test_command_frames.cpp`) and `replay_fixture.py` (trace replay through the C API) |
| `tools/` | `live.py` (drive the client: launch / stop / instance / setup / dump / trace / record), `gen_manifest.py`, `gen_docs.py` (the two catalogue docs) |

## Ground truth and how to refresh it

Ids, tick order and subscription lists come from the running client, not from assumptions. To
regenerate: servers running, then `tools/live.py record` (launches the client if needed, records the map
load and the setup traces, about 75 s), `tools/gen_manifest.py`, rebuild, `ctest`. `record-cf` records
the command-frame fixture.

## What is a stand-in

`manifest::DataComponent` and `manifest::ManifestSystem` carry no game logic: they only reproduce
component ids, storage and subscriptions so that lifecycle and ordering match. Real components and
systems replace them one by one (Statescript next: `docs/StatescriptRuntime.md`, `systems/statescript/`
when it exists).

## Fidelity rule

Match the client's behaviour and identifiers (component ids, system identity and tick order, callback
ordering, entity id format, deferred deletion, fixed-step timing), not its memory layout. See
[systems/README.md](../README.md).
