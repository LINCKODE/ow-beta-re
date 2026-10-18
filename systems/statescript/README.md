# systems/statescript: Statescript runtime

Clean-room C++20 runtime for the beta 0.8 (build 24919) Statescript system: graphs loaded from STU
(`systems/stu`), instances on per-entity sync managers (component 0x23 on `systems/ecs` entities), the
notification queue and command-frame stepping, links, states, variables and game messages, with the client's
ordering. It replays the client's traces exactly. Documentation: [docs/StatescriptRuntime.md](../../docs/StatescriptRuntime.md)
§5 (this library; §1–4 are the reverse engineering it implements), config vars:
[docs/StatescriptConfigVars.md](../../docs/StatescriptConfigVars.md).

```
cmake -S systems/statescript -B systems/statescript/build -G Ninja && cmake --build systems/statescript/build
ctest --test-dir systems/statescript/build      # unit tests, fixture replays, C API test
SS_REPLAY_DUMP=/some/dir systems/statescript/build/statescript_tests   # write replayed/expected streams
```

| path | what |
|---|---|
| `include/statescript/value.h` | `Primitive` (StatescriptPrimitive), truthiness, loose/strict equality, trace text |
| `include/statescript/graph.h` | `Graph::Load` (CASC bytes or client image): nodes, states, entries, plugs (field offsets), links, schema, sync vars |
| `include/statescript/vars.h` | `VarBag` / `Var`: writers, priorities, subscriptions |
| `include/statescript/config_var.h` | the pluggable evaluator interface (`Evaluator`, `EvaluatorRegistry`), typed getters |
| `include/statescript/runtime.h` | `SyncManager`, `Instance`, `State`, notifications, trace events, game messages |
| `include/statescript/ecs_component.h` | `StatescriptComponent` (component 0x23 owning a sync manager) for `systems/ecs` |
| `src/` | `runtime.cpp` (instances, queue, RunCommandFrames, logical-button input, stack registry), `states.cpp` (Wait, BooleanSwitch, Switch), `states_hero.cpp` (Anim, ChaseVar, WeaponVolley, Stack, Ability, BlinkEffect, the may-take-slot condition; T041, docs §5.10), `nodes.cpp` (entries, conditions, actions), `config_vars.cpp` (evaluators, both expression interpreters) |
| `include/statescript/match_host.h`, `match_world.h`; `src/states_match.cpp`, `nodes_match.cpp`, `capabilities_match.cpp`, `match_world.cpp` | the match / objective node families (T055, docs/StatescriptNodes.md §6): the host boundary (`MatchHost`), their states / actions / config vars, and an in-process world for offline runs of the brain and objective graphs (`tests/test_match.cpp`) |
| `src/states_host.cpp` | the host-system states (T060, docs/StatescriptNodes.md §3.16): Shockwave, ValidateWidth, Extender, TrackSlamming, HistoryBuilder / Rewinder, the mover controllers, ScaleTime; their graph side here, the engine's part through `MatchHost` (movement, collision, history, time scale) |
| `capi/statescript.h`, `src/capi.cpp` | C API (`libow_statescript_c.so`, `libow_statescript_c_static.a`) |
| `fixtures/` | client traces (Prometheus `ss_trace`, T015) + graph dumps + `graph_0x042.stud` (CASC bytes of the door graph) |
| `tests/` | `test_runtime.cpp` (building blocks), `test_replay.cpp` (fixture replays), `test_hero.cpp` (hero press replays: Genji fire / Shift / E, §5.10.8), `test_capi.c` |
| `tools/ss_fixture.py` | raw `ss_trace` save → per-entity fixture |
| `tools/hero_fixture.py` | `ss_serialize_local` snapshot + raw `ss_trace` of a press → `fixtures/hero_*.fixture` |

The STU reader and the ECS core are compiled from their source trees into this build (targets `ss_dep_stu`,
`ss_dep_ecs`). Graph images used by the tests: `systems/data/live_anubis/graphs`.
