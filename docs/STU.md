# STU: the structured data format

STU is Overwatch's structured data: entity definitions, Statescript graphs, heroes, maps, voice lines,
settings, … In the beta 0.8 client (build 24919) it is two things:
1. an **immutable asset format** (STUD v3 blobs in CASC, loaded in place by the "STU" resource handler);
2. a **reflection layer** (type registry with fields) that is also used to replicate component state
   ("mirror data") over the network.

Related: [ResourceLoading.md](ResourceLoading.md) (how a blob gets loaded),
[StatescriptLoading.md](StatescriptLoading.md), Prometheus `STU.h` / `STU_Editable.h` / `Prometheus-Atlas`
(RTTI structs).

**Addresses**: RVA (runtime base `0x140000000`; Ghidra = `0x7ff64ffe0000 + RVA`).
**Status**: [H] code + confirmed live, [M] code or live only, [L] inferred.
**Data**: `systems/data/client_stu_registry.json` (the full type registry, see §3). **Code**: `systems/stu/` (reader library, §7).

---

## 1. On disk: STUD v3, a memory image  [H]

A 0.8 STU file is a flat image of C++ objects; offsets are relative to the start of the file. It is
**not** the later "STU v2" format (field hashes in the file) that OWLib's current reader expects, so OWLib
cannot read these files as they are.

```
+0x00 u32 magic   = 0x53545544 ('STUD')
+0x04 u32 version = 3                      (anything else: load fails)
+0x08 i64 offset of the instance table     (at the end of the blob)
+0x10 …   instance data; instance 0 (the root object) starts at +0x10

instance table: { u32 count; u32 pad; i64 offset of entries }
entry (16 B):   { u32 instanceOffset; u32 flags; u64 layoutCRC }
   flags bit 0 = the instance has references to other resources (gets a resolve pass, §2.2)
   layoutCRC   = must equal the type's registered CRC (STUInfo+0x38): the per-type layout/version check
each instance starts with its u32 type hash, in the slot the vtable pointer takes after loading
```

Fields sit at **fixed offsets** given by the registry; there are no per-field hashes in the file. Field
kinds (the registry's constraint kind; same numbers as OWLib `serializationType` / Atlas
`STUConstraintType`):

| kind | meaning | encoding |
|---|---|---|
| 0 | primitive (u8…u64, f32, vectors, teString, …) | raw value; **teString** is 16 bytes `{i64 offset, i64 flag}` → `{i32 len; u32 crc32(text); i64 bufOffset}` (offset 0 = empty; after loading `flag = 2`, empty strings point at a static empty string) [G: fixup `0x839a90` relocates the header and its `+0x08` buffer only; field order D: T065, all 30,930 STU files of the install: 35,800 non-empty strings decode with this order (0 errors; with the swapped order all of them fail); the u32 is CRC32 of the text as written in the one checked by hand, `{11, 0x88eae14e, → "WEAPON_TAIL"}`; the reader had the two u32 swapped until T065, so every non-empty string failed to decode] |
| 1/3/5/9/11/13 | list of primitive / object / inline object / enum / resource ref | 16-byte descriptor `{i64 offset, i64 flag}` → `{i32 count; pad; i64 dataOffset}`; object lists hold u32 instance offsets (`0xFFFFFFFF` = null); after loading `flag = 2`, offsets are pointers, empty lists point to a static per-type sentinel (`STUBullshitListFull` in STU.h). Element stride: primitive size, 8 (object: u32 offset + pad), instance size of the element type (inline), 4 (enum), 16 (refs) [G, L] |
| 2 | object | u64 slot holding a u32 instance offset (`0xFFFFFFFF` = null) → pointer (0 = null) [L] |
| 4 | inline object | struct embedded at the field offset, fixed up recursively |
| 7 | map | descriptor like a list → `{i32 bucketCount; pad; i64 bucketEndsOffset; i64 itemsOffset}`; `bucketEnds[i]` = cumulative end index of bucket i (last = item count); items 16 B `{u32 key; pad; u32 instance offset (0xFFFFFFFF = null) + pad}` [L: entity def `m_componentMap`, key = component type hash] |
| 8 | enum | u32 |
| 10 / 12 | file reference / resource reference | 16 bytes `{u64 handle, u64 GUID}`; the handle starts at −1 and is filled by the resolve pass (§2.2) |

Registry distribution (9436 fields): primitive 3412, object 2834, resource ref 1058, inline 962, enum 302,
object list 247, inline list 214, primitive list 157, file ref 135, resource-ref list 98.

Example (live): graph `0x058000000000000F` has its instance table at +0x8010 with 352 instances (16 with
references). The 294 graphs scanned total 12.3 MB (1.6–215 KB each) and 119,058 instances.

## 2. Loading  [H]

### 2.1 Parser
The **STU resource handler** (handler 12, `"STU"`) construct slot `0xa4c4e0`:
1. `0xa4c3f0` (Prometheus hooks it as `STUD_ConstructFromResource`) takes the blob from `entry+0x40` and runs
   the **deserializer `0x833ce0`**; on failure "Failed to load immutable STU (GUID …)", state 11.
2. `0x833fe0`: the **resolve pass** (§2.2).
3. marks the entry loaded, keeps the root in `entry+0x40`, calls `root->vfunc+0x28` (per-type OnLoaded).
4. an optional per-type validator (hash table RVA `0x17aaab8`, keyed by type hash) decides ready (12) vs
   error (11).

The deserializer checks magic and version, turns the header offsets into pointers, and for each instance:
looks up its type by hash (`0x830e20`), **rejects it unless the layout CRC matches**, placement-constructs
the object over the file bytes (writes the vtable(s) over the type hash, zeroes runtime-only fields), then
walks the parent chain and every field and lets the field's constraint turn offsets into pointers. It
returns the root. **The loaded object is the file buffer** (no copy): the STUD header stays at `root-0x10`,
and the engine reads it later (Statescript graph linking walks the instance table through it).

### 2.2 Resolve pass: dependencies
`0x833fe0` visits every instance whose table flag bit 0 is set and lets each reference field's constraint
run: for file/resource references (`0x5b5c70`), **if the field's flags have bit 2 (value 4)**, `0x848620`:
- releases the old handle;
- consults per-asset-type flags (table RVA `0x1457350`, by GUID type): skipped if bit 10 is set and the
  process mode (RVA `0x17a1838`) is 2, or bit 11 is set and the mode is not 2 (live mode: 1);
- **requests the referenced asset by GUID** through the Resource Manager and stores the handle in the
  field; registers it with the parent load (so the parent waits for it [M]).

Only **145 of 1296** reference fields have that flag: those auto-load their target, the rest stay lazy and
are requested by gameplay code. Relevant ones: `STUStatescriptGraphWithOverrides.m_graph`,
`STUConfigVarStatescriptGraph.m_graph` (SubScript / PlayScript nodes), `STUHero.0CD16BAA` (the hero's
entity definition). `STUMapHeader` references are all lazy.

Mode 2 skips textures, effects, sounds, fonts, UX screens, display text, shaders, …: it looks like a
**headless / server asset profile** [L]. Types 077 and 090 (`STUResourceKey`) are skipped in every mode
except 2.

## 3. Type registry  [H]

- Linked list head at RVA `0x18F74E0` (`{next, STUInfo*}` nodes): **3639 types**. Hash lookup table at
  `0x17a3480` (0x1000 buckets). Enum registry at `0x18f7500`.
- **Names are stripped** (all point to an empty string). 2715 of the 3639 types are in OWLib's
  RegisteredSTUTypes and 701 have a name in OWLib KnownTypes; field names come from OWLib KnownFields.
- Full dump: `systems/data/client_stu_registry.json` (per type: hash, parent, size, layout CRC, fields with
  hash, offset, kind, element type, flags).

`STUInfo` (0x60 bytes, Atlas `STUInfo.h`):

| offset | field |
|---|---|
| +0x00 | name (empty) |
| +0x08 | RTTI inheritance info |
| +0x10 / +0x18 / +0x20 | first child / sibling / parent |
| +0x28 | field array (`STUArgumentInfo`, 0xA0 each) |
| +0x30 | ? |
| +0x38 | **u64 layout CRC** (checked against the file) |
| +0x40 | `CreateInstance()` (heap) |
| +0x48 | `PlacementConstruct(buf)` (on the file buffer) |
| +0x50 | u32 type hash (CRC32 of the name) |
| +0x54 | field count |
| +0x58 | flag (1 on 80 types, mostly components and asset roots; 2 `STUMapCatalog`; 3 `STUHero`, `STUMap`; meaning unknown) |
| +0x5C | instance size |

`STUArgumentInfo` (0xA0): +0x08 field hash, +0x0C offset, +0x10 constraint (vfunc +0x30 →
`(kind << 32) | elementHash`), +0x18 flags: **4 = load dependency**, 2 = common on object/list fields,
1 = on graph `m_nodes`/`m_states`/`m_entries` (probably non-owning).

Example: `STUStatescriptGraph` (hash `0x0DF7E24F`), size 0x198, 32 fields, 3 vtables: `m_graph` @0x8
(inline STUGraph), `m_nodes` @0x50, `m_states` @0x60, `m_entries` @0x70, `m_consoleCommand` @0x80,
`m_remoteSyncNodes` @0x120, `m_syncVars` @0x130, `m_publicSchema` @0x160, u8 `0x01169d71` @0x190 (also used
at runtime as the "linked" flag).

## 4. Objects in memory  [H]

`obj+0` = vtable (`STUBase_vt+0x10` = `GetSTUInfo()`), fields at `obj + offset`. Types without a vtable
only appear inline or as list elements (e.g. `STUStatescriptGraphWithOverrides`, 0x20 bytes:
`{u64 handle, u64 graphGUID}` + a list of overrides at +0x10). A resource reference's handle resolves to
the loaded object through `0xa4b3c0` ("handle → object, or 0 if stale").

## 5. Mirror data: STU as replication  [M]

Networked components embed an STU object as their replicated state, usually at `comp+0x18` (Statescript
component 0x23: `comp+0x20`, type `0xdcec70e5`, no fields in 0.8). Replication uses the same registry:
each field constraint has `SerializeToBitstream` / `DeserializeFromBitStream` (Atlas `STUConstraint` vfuncs
19 and 22), driven by `MirrorSystem` (game system #8) from packets. [ECS_Components.md](ECS_Components.md)
lists the mirrored components. (Statescript's own sync, `StatescriptSyncMgr` deltas, is separate: it uses
the graph's `m_syncVars` / `m_remoteSyncNodes`.)

## 6. Reading STU offline

Because a file is a memory image, parsing is simple:
1. read the header and instance table;
2. per instance, read its u32 type hash and look it up in `client_stu_registry.json`; the entry's layout
   CRC must match the table's (proves the registry fits the file);
3. decode each field at its fixed offset using the encodings in §1.

What's missing for a server is only the bytes: either an offline CASC reader
([ResourceLoading.md](ResourceLoading.md) §8), or a dump from the client (hook `0xa4c3f0` / `0x833ce0`
**before** fixup and copy `entry+0x40` with its size `entry+0x78`, or undo the fixup on a resident object).

## 7. The reader library (`systems/stu/`)  [H]

C++20 static library `ow_stu` (no dependencies) + `stu_dump` CLI; built and tested with CMake/ctest
(`systems/stu/README.md`).

- **Registry** (`stu::Registry::Client()`): the 3639 types / 9436 fields of
  `systems/data/client_stu_registry.json`, compiled in (`generated/registry_data.h`, from
  `tools/gen_registry.py`). Names: OWLib `TankLibHelper/DataPreHashChange` KnownTypes/KnownFields (the
  0.8-era hashes; 699 types and 968 of 5879 field hashes named) plus `hashlibrary.json`. Primitive element
  hashes and sizes: 23 primitives (`u8` … `teMtx43A`, `s8 = 0x6dc98054`), names joined from OWLib
  RegisteredSTUTypes by field hash.
- **Reader** (`stu::Document::Parse` + `Decode`): checks magic/version, reads the instance table, looks up
  every instance's type and **checks its layout CRC** (`AllCrcOk()` = the client would load the file), and
  decodes all field kinds (§1) into a `stu::Value` tree: primitives (raw bytes + typed accessors), strings,
  enums, object pointers (as instance indices), inline objects, lists, maps and file/resource references
  (GUID + handle). Bounds-checked: malformed input yields `Error` values, never out-of-range reads.
- Two input modes: **File** (raw STUD, offsets from the file start: CASC bytes) and **Loaded** (an image
  dumped from the client's memory: absolute pointers; types found by layout CRC, which is unique per type).

**C API and Go wrapper** (`capi/stu.h`, `go/`): the boundary for the Go server and the Statescript
runtime. `stu_doc_open` (bytes + mode + base; the document copies the bytes) / `stu_doc_open_file`,
instance table with CRC status (`stu_doc_instance`, `stu_doc_all_crc_ok`), `stu_doc_decode(i)` (decoded
once, cached; `stu_value` pointers stay valid until `stu_doc_close`), navigation by field hash / index /
map key, typed getters (integers and enums, floats, float vectors, raw bytes, strings, references, object
→ instance index) with return codes (`STU_ERR_TYPE`, `STU_ERR_NOT_FOUND`, ...), JSON of a value or the
whole document (the `stu_dump` format), and registry lookups (names, type/field tables, `is_a`). Go:
module `owstu`, package `stu`: `OpenFile`/`Open` → `Document` (`Decode`, `Instances`, `AllCRCOK`, `JSON`),
`Value` (`Field`, `Path`, `Index`, `Get`, `Deref`, `Int`/`Float`/`Floats`/`Str`/`Ref`/`Object`, `JSON`;
lookups on a missing value return an invalid `Value`, so chains need one check), `LookupType`, `Name`,
`IsA`. Tests: `tests/test_capi.c` (plain C) and `go/stu/stu_test.go` on both fixtures, raw and loaded.
A document is not thread-safe (decode cache); the registry is.

**Test data**: no raw CASC blobs yet (no offline CASC reader, roadmap #5). `systems/stu/fixtures/` holds
two resident resources dumped from the live client (`tools/dump_live.py`: graph `0x058000000000000F`, 352
instances; entity definition `0x0400000000000039`, 23 instances) and the same images with the fixup undone
(`tools/unfix_image.py`, the inverse of the per-kind fixups below). Both parse with every layout CRC
matching and every field decoded; the raw and loaded forms decode to identical trees. Decoded values match
Prometheus' live reflection (`read_stu_object`) where compared: STUGraph item/link counts 49/44, node
addresses and types, `m_nodesBitCount` 6 for 49 nodes, the entity definition's three graphs
(`live_anubis/def_graphs.json`) [L].

**Fixup routines** (constraint vfunc 26, `+0xd0`, called by the field walker `0x835710` for every field of
every instance, parents first) [G]: object lists `0x5b5aa0`; primitive/enum lists `0x5b4ac0`; inline
object `0x5b3520` (recurses into `0x835710` with the inline type's STUInfo); inline lists: one small
function per element type calling a shared loop with the element stride (e.g. `0x5929f0`, stride 0x38);
resource-reference lists `0x845570`; teString `0x839a90`; map value slot `0x83c100` (`0xFFFFFFFF` → null);
primitives and enums: no-op `0x5a0c20`. Constraint vtables are in `.rdata` (301 found by their shared
slots 7/8 = `0x5b62e0`/`0x5b6300`); STUInfo objects and field arrays are built at runtime (not in the
dumped binary), so the registry has to come from the live client.

**Verified against real CASC bytes** (T018, [ContentAccess.md](ContentAccess.md) §5): 370 graphs and 967 entity
definitions read from the install parse with every layout CRC matching and decode to the same trees as the
live-dumped fixtures. In the file the descriptor flag is 0 (the loader writes 2), and the upper u32 of each
instance's type-hash slot holds the offset of the next instance (0xFFFFFFFF for the last).

Non-empty teStrings are verified on the whole install (T065, §1 table; regression fixture
`fixtures/soundswitchgroup_034000000000001D.stud`). What is not verified yet: lists of teString, file-reference lists (kind 11, 3 fields), the raw file's descriptor `flag` value,
and anything only a CASC blob can prove (§6: bytes the loader rewrites).

## Open questions
- `STUInfo+0x30` and `+0x58` semantics.
- What process mode 2 (`0x17a1838`, written in WinMain around `0x5a583a`) is: a server/headless profile?
- Whether `0x848590` makes the parent load wait for its dependencies.
