# Statescript catalogue tools (T014)

Scratch-quality scripts that produced `../statescript_node_catalogue.json` and
`docs/StatescriptNodeCatalogue.md` (see docs/StatescriptRuntime.md §1.8, §2). Kept for reproducibility;
they read relative paths from the working directory, so run them from a work dir that contains `graphs/`
(a copy or symlink of `systems/data/live_anubis/graphs/`) and a Python venv with `capstone pefile requests`.

| step | script | input → output |
|---|---|---|
| 1 | (Ghidra) `GET /xrefs_to?address=0x7ff6518d8e88` | → `rtti_xrefs.txt` (xrefs to the StatescriptRTTI list head) |
| 2 | `rtti.py` | binary (`bin.py`: `RE-binary/…treated.exe` + capstone) + xrefs → `rtti_recs.json` (factory, inheritance, priority per record). Note: 4 initializers adjacent to other functions are missed by its start-of-function scan; they were added by hand (records at `0x50bd00`, `0x53a7e0`, `0x53d0a0`, `0x5475d0`) |
| 3 | `inhmap.py` | binary → `inhmap.json` (STU inheritance object → type hash, via the static `STUInfo` objects) |
| 4 | `brute.py` | registry + OWLib names → `names_brute.json` (CRC32 name recovery) |
| 5 | `dumpall.py` (needs the running client, read-only MCP) | → `graphs/*.mem` + `meta.json` |
| 6 | `usage.py` | graphs → `usage.json` (type use counts) |
| 7 | `cat2.py`, `cat3.py` | → `cat2.json` (records + vtable/size from `fact.py`), `cat3.json` (per STU type) |
| 8 | `gen.py` | → `statescript_node_catalogue.json`, `StatescriptNodeCatalogue.md` |

`img.py` reads a dumped (as-loaded) graph image: `Img('0x0580000000000042').build()`, `lst(addr)` for
object lists, `lst32` for i32 lists, `bytype[addr]` = STU type hash. `fields.py <hash…>` prints a type's
registry layout with names.

## Names (T019)

`../statescript_names.csv` is the source of truth for type names: one row per named type with `confidence`
(`exact` = OWLib table, `high`, `medium`, `low`), `method`, `applied` (`low` rows are candidates only and are
not shown in the catalogue), use counts, `source` and `evidence` (why the name fits the type's fields,
parent or siblings). Every name matches its hash (CRC32 of the lower-cased name); casing is a guess.

| step | script | input → output |
|---|---|---|
| 9 | `apply_names.py` | names CSV + existing `statescript_node_catalogue.json` → the JSON (adds `name_confidence`, `name_method`) and `docs/StatescriptNodeCatalogue.md`; `gen.py` also reads the CSV (applied rows win) |
| – | `crc_mitm.c` | meet-in-the-middle CRC32 search: `crc_mitm prefixes.txt vocabF.txt depthF vocabB.txt targets.txt` tries every prefix + up to `depthF` words from vocabF + 0/1 word from vocabB against the target hashes (all lower-case) |
| – | `crc_guess.py` | `crc_guess.py HASH "Prefix1 Prefix2" Word…`: prefix (+ optional `_`) + 1–3 words of a hand list |

Vocabularies used (rebuild, not committed): identifiers from the full OWLib git history (every blob),
strings of the client binary, `/usr/share/dict/cracklib-small`, and a hand-curated list of ~760 game words
(plus the tokens of already-named Statescript types and field names). How to read a hit: the chance that a
random string of the searched space matches one given hash is space/2³²; a 3-word search over 4000 words
yields ~15 random matches per hash, so such hits were kept only when the name fits the fields/family.

## Config vars and expressions (T038)

docs/StatescriptConfigVars.md (interface, bytecode) and docs/StatescriptConfigVarCatalogue.md (per type).

| script | input → output |
|---|---|
| `expr.py graph.json [--ops]` | a `stu_dump` of a graph → every `STUConfigVarExpression(Typed)` decoded to text; `decode(ops, consts, dyn, cvs)` is importable |
| `configvar_catalogue.py GRAPH_JSON_DIR [--md]` | node catalogue + binary (evaluator vtable `+0xD0`/`+0xD8`) + graph usage + `../configvar_semantics.json` → `../configvar_catalogue.json` (and the markdown table of the catalogue doc with `--md`); needs `pefile`/`capstone` (`bin.py`) |

All graphs of the build as `stu_dump` JSON (GRAPH_JSON_DIR):
```
B=systems/content/build
for g in $($B/content_dump --list 1B | awk '{print $1}'); do
  $B/content_dump --read 0x$g $D/$g.stud && systems/stu/build/stu_dump $D/$g.stud > $D/$g.json; done
```
`../configvar_semantics.json` is the source of truth for what each evaluator computes (read from the decompiled
`GetValue` functions; edit it, then rerun the generator).
