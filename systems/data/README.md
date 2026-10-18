# systems/data

Research data for the 0.8 client (build 24919). Two kinds of material belong here, and only one of them is
in the repository.

## Published: our own analysis

These files are results of the reverse engineering: names, classifications, addresses, formats and curated
layouts. The game data they describe is not included.

| file | what | used by / docs |
|---|---|---|
| `statescript/statescript_names.csv` | names for Statescript node / config-var types with provenance (OWLib tables, CRC guessing) and confidence | explorer, `docs/StatescriptNodeCatalogue.md` |
| `statescript/statescript_node_catalogue.json` | every node type: kind, parent, fields, client runtime class (factory / vtable / size), usage counts | explorer, `docs/StatescriptNodeCatalogue.md` |
| `statescript/configvar_catalogue.json` | every config-var type: fields, evaluator vtable, GetValue, usage | `docs/StatescriptConfigVarCatalogue.md` |
| `statescript/configvar_semantics.json` | what each config-var evaluator computes (from the decompiled evaluators) | `docs/StatescriptConfigVars.md` |
| `statescript/state_data_formats.json` | the bit formats of every state's sync data (local / remote) | `server/cmd/shadowcmp`, `docs/StatescriptSync.md` §4 |
| `jam/catalogue.json` | the JAM message catalogue: every protocol and message with field layouts, curated names, notes, handlers | `server/jamgen` → `server/inst/jam`, `docs/JAM.md` |
| `shadow/genji_anubis.json` | shadow-compare evidence (server simulation vs client) for Genji on Anubis | `instance --evidence`, `server/cmd/shadowcmp` |
| `ui/vmtab.txt`, `ui/vmctors.txt` | UI view-model classes: RTTI addresses, vtables, constructors (Ghidra) | raw tables for the UI RE |
| `../ecs/manifest/analysis/` | Ghidra analysis of the ECS components and systems (our names and interpretation) | `systems/ecs/tools/gen_docs.py`, `docs/ECS_*.md` |

## Not published: dumps of the client or its data

Regenerate these locally from your own install (`game/owbetaclient`) or a running client with Prometheus. The
tools are in `*/tools/`:

- `client_stu_registry.json` (the client's STU type registry): Prometheus MCP dump, `docs/STU.md` §3.
- `maps/maps.json`, `maps/resident/`: `maps/tools/mapgen`, `maps/tools/resident.py`.
- `heroes/heroes.json`: `heroes/tools/herogen`.
- `movement/*.json` (mover values, client movement recordings): `movement/tools/movergen`,
  `movement/tools/record_movement.py`.
- `lobby_scripts/*.tsv` (string table, unlocks, skins, notices), lobby graphs: `lobby_scripts/tools/`.
- `live_anubis/`, `live_dorado/`, `live_tutorial/` (live snapshots, graph dumps, traces): `statescript/tools/`,
  `live_*/tools/`, the Prometheus MCP.
- `dataflow/*.tsv`: `content_dump` + `stu_dump` (`docs/DataFlow.md`).
- `statescript/stack_priorities.json` (values read from content).
