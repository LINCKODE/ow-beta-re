# Resource loading: GUIDs, TACT/CASC, asset packages

How the Overwatch beta 0.8 client (build 24919) finds and loads its game data: resource GUIDs, the
Resource Manager and its load pipeline, the TACT/CASC container and the asset-package (APM) manifests,
and how we can load data ourselves (in the client and offline).

Related: [EngineManagers.md](EngineManagers.md) (slot 13 Resource, 14 Asset Package, 3 Storage),
[STU.md](STU.md) (what the "STU" handler does with a loaded blob),
[StatescriptLoading.md](StatescriptLoading.md).

**Addresses**: RVA (runtime base `0x140000000`; Ghidra = `0x7ff64ffe0000 + RVA`).
**Status**: [H] code + confirmed live, [M] code only, [L] inferred.

---

## 1. Resource GUIDs  [H]

Every asset is a 64-bit GUID:

```
bits  0..31  index
bits 32..47  locale / region / platform / reserved   (unused by type dispatch)
bits 48..59  type, stored bit-reversed (12 bits)
bits 60..63  ignored by type dispatch

type = bitreverse12((guid >> 48) & 0xFFF) + 1
```

Example: `0x058000000000041a` → `0x580` → reversed `0x01A` → +1 = type **0x01B** (Statescript graph).
This is the same convention as TACTLib `teResourceGUID.Type` / OWLib's 3-digit type names.

| GUID prefix | type | what |
|---|---|---|
| `0x040…` | 0x003 | STUEntityDefinition |
| `0x058…` | 0x01B | STUStatescriptGraph |
| `0x06E…` | 0x077 | asset package |
| `0x0DE…` | 0x07C | display text (Prometheus comments say "7B": they omit the +1) |
| `0x0C0…` | 0x004 | texture |
| `0x07C…` | 0x03F | sound WEM |

Resource types → handler: a 180-entry int table at RVA `0x146c5f0` maps type → handler index (§3).

## 2. Two I/O layers  [H]

- **Storage Manager** (engine slot 3): a generic file-stream layer (`teSimpleFileStream`) over a list of
  *sources* (head RVA `0x1820b30`). Live it has exactly one source, **"Working Directory"**: loose files
  (settings, logs). **Game data does not go through it.**
- **Resource Manager** (slot 13) + **TACT/CASC container**: all GUID assets. The rest of this document.

## 3. Resource handlers  [H]

34 slots at RVA `0x182c940` (26 used), one per asset kind, registered with `RegisterHandler`
(`0x9c7c70`):

TXTR, MATERIAL, ANIM, CURSOR, FONT, TXTR_COMPRESS, SOUNDBANK, **STU**, SHADER SOURCE, EFFECT, VOICEWEMFILE,
MAPDATA, SOUNDWEMFILE, MODEL, LIGHTINGDATA, SKELETON, ASSET_THUMBNAIL, LIGHTINGDEBUGDATA, SHADER GROUP
CACHE, DISPLAYTEXT, ASSETPACKAGE, SHADER GROUP / INSTANCE / CODE, TXTR_PAYLOAD, BINARY PACKAGE DATA.

| handler | resource types |
|---|---|
| TXTR / TXTR_PAYLOAD | 004 / 04D |
| MATERIAL, ANIM, SKELETON, MODEL | 008, 006, 007, 00C |
| EFFECT | 00D, 04A, 08E, 08F |
| LIGHTINGDATA / LIGHTINGDEBUGDATA | 00E / 03E |
| SHADER GROUP CACHE; SHADER GROUP / INSTANCE / CODE / SOURCE | 00F; 085 / 086 / 087 / 088 |
| DISPLAYTEXT | 010, 07C, 0A9 |
| MAPDATA | 002 |
| CURSOR, ASSET_THUMBNAIL, FONT | 027, 034, 050 |
| SOUNDWEMFILE, SOUNDBANK, VOICEWEMFILE | 03F, 043, 057/0B2 |
| ASSETPACKAGE | 077 |
| BINARY PACKAGE DATA | 09C |
| **STU** | everything else: 001, 003 (entity defs), 01B (graphs), 013–015, 017, 018, 01A, 01F–021, 024, 025, 02A–033, … (~90 types) |

Handler struct (`resource_handler` in Prometheus `ResourceManager.h`):

| offset | field |
|---|---|
| +0x00 | `char* name` |
| +0x08 | handler id |
| +0x1c | flags: bits 2..5 priority clamp, 6..9 max cache age, bit 0 "log" |
| +0x20 / +0x28 | Alloc(size) / Free(ptr): the read buffer |
| +0x30 / +0x38 | alloc / free for inline data |
| +0x40 | **Construct(ResourceLoadEntry*)** (state 9 → 10) |
| +0x48 | OnReady (state 17) |
| +0x50 | OnCancel |
| +0x58 | OnFailure |
| +0x60 | Destroy (states 18/19) |
| +0x68 | atomic bytes-loaded counter |

Examples: STU (#12) construct `0xa4c4e0` / ready `0xa4c5b0` / destroy `0xa4c620`; ASSETPACKAGE (#28)
construct `0x9c4190`.

## 4. Data structures

### Resource entry  [H]
One per known GUID (`MisalignedResourceLoadEntry` in `game.h`): 0x40 bytes, 64-byte aligned, pooled (0x1000
per block). Kept in a GUID hash table (RVA `0x18a19a4` lock, `0x18a19a8` buckets, `0x18a19b8` bucket count,
`0x18a19bc` entry count). Live: 16384 buckets, **24,053 entries**.

| offset | field |
|---|---|
| +0x00 | `void* data`: the constructed object |
| +0x08 | flags: 0–5 generation, 6–11 handler index, 12 created, 15 failed, 16 loaded, 18 queued for unload, 22–25 cache age, 26–28 unload delay |
| +0x0C | refcount |
| +0x10 | GUID |
| +0x18 | hash chain |
| +0x20 | `ResourceLoadEntry*` while loading |
| +0x28 | asset-package back reference |
| +0x34 | spin RW lock |

**Handle** = `entry | generation` (low 6 bits); valid iff the generation still matches; `-1` = none.

### ResourceLoadEntry (a load in progress)  [H]
0x80 bytes (Prometheus `ResourceLoadEntry`): +0x00/+0x08 prev/next, +0x10 handler index, +0x14 **state**,
+0x18 cancel, +0x1c **priority**, +0x20 flags, +0x28/+0x30 dependency handles/count, +0x40 data buffer,
+0x50 GUID, +0x58 **16-byte content key (CKey)**, +0x68 resource entry, +0x74 request size/flags, +0x78 data
size, +0x7c state-change counter.

### Load states  [H: names read live from RVA `0x17a9970`]
0 None · 1 Awaiting Placement · 2 CAS Awaiting HTTP · 3 CAS HTTP · 4 CAS Awaiting CASC · 5 CAS CASC ·
6 TACT WAIT LOCAL · 7 TACT WAIT REMOTE · 8 TACT LOAD · 9 Awaiting Creation · 10 In Creation ·
11 Creation Error · 12 Creation Success · 13 Checking Dependencies · 14 Dependency Error ·
15 Dependency Success · 16 Failure · 17 Ready · 18 Awaiting Destruction · 19 In Destruction ·
20 Destruction Failed · 21 Destroyed.

This build goes **6 → 8 → 9 → 10 → 12 → 13 → 15 → 17**; the CAS states (2–5) look like a legacy/dev path.

### LoadRequest  [M]
0x38 bytes: +0x00 vtable (RVA `0x157f1a8`), +0x08 optional ref-counted callback, +0x10 GUID, +0x18 −1,
+0x20 `const u8 (*ckey)[16]`, +0x28 priority (engine uses 1; 6 = lowest), +0x2c size/flags,
+0x30 optional inline data (skips I/O, goes straight to state 9).

## 5. The load pipeline  [H unless noted]

```
game thread
  AcquireHandle(&h, guid)          0x9c8330  type → handler (table 0x146c5f0), find/create entry, ref++
                                              → 2 ok · −15 shutting down · −12 no handler · −9 mismatch
  RequestLoad(h, &LoadRequest)     0x9c88c0  loaded → 0 · loading → re-prioritise → 2 ·
                                              else new ResourceLoadEntry: inline data → state 9,
                                              CKey → state 6, no CKey → "Resource GUID not found in
                                              manifest" → 16; enqueue + wake the loader
resource loader thread             0x9d22e0  (started by the Resource Manager's Start 0x9c6bb0)
  merge the incoming queue into priority lists 1..6 (list 0 = active work list)
  issue reads, most urgent first, ≤ 8 in flight: state 8, QueueTACTRead(CKey) 0x9d1f60
  state machine over the work list:
     9 → 10  handler->Construct(entry)        (construction runs on this thread)
     12/13   wait for dependency handles; 14 on failure
     15 → 17 Ready (16 Failure → OnFailure)
     finish (0x9c8170): OnReady, publish into the resource entry, fire callbacks,
                        ++completed counter (RVA 0x18a1840, Prometheus' "resource load frame")
TACT I/O thread 0x9d4220 + 1..8 decode workers (live: 1)
  ReadOne 0x9d46a0: container->Execute(size query by CKey); buf = handler->Alloc(size);
                    container->Execute(read); → complete 0x9d18a0 (size checks, optional decrypt) → state 9
game thread, per frame (Resource Manager update 0x9c5ab0)
  unload refcount-0 entries after they age (≤ 400 per tick), destruction states, queued "loaded" callbacks
```

Priorities: request +0x28 → entry +0x1c (0..6, lower = more urgent), clamped by the handler's flags.
Re-requesting a queued resource bumps its priority.

Other entry points:

| function | RVA | notes |
|---|---|---|
| **GetLoadedData(guid)** | `0x9c6470` | `entry->data` or 0; takes and drops a ref (so none is held) [H] |
| Release(entry) | `0x9cad30` | ref−−; at 0 it is queued for unload [H] |
| IsLoaded / IsFailed(entry) | `0x9c6680` / `0x9c6620` | [H] |
| CreateFromMemory | `0x9c9130` | "resource creation from runtime", straight to state 9 [M] |
| RegisterHandler | `0x9c7c70` | [M] |
| RegisterLoadedCallback(type, fn, ctx) | `0x9c7f20` | per-type "a resource of this type became ready" [M] |
| LoadPackage / LoadRecordFromPackage | `0x9cbee0` / `0x9cc410` | asset-package paths (where CKeys come from) [M] |

## 6. Where the data comes from: TACT, CASC, APM  [H unless noted]

**Resource Manager Start** (`0x9c6bb0`):
1. reads REGION / LOCALE / LOCALE_AUDIO from the Blizzard registry key, builds the manifest base name
   (live: `Win_SPWin_RDEV_LenUS_EExt`), starts the TACT threads;
2. allocates the content container object `g_tact` (pointer at RVA `0x182ca50`; Prometheus calls it
   `storageHandler`) and runs **ContainerInit** (`0x9ce9d0`):
   - parses `<install>\.build.info`;
   - opens the local TACT container over CASC (`0x9cf110`), with a key service using
     `%APPDATA%\Battle.net\Armadillo` for `.ak` key files (an alternative, probably remote, path exists at
     `0x9cf630`);
   - reads the TACT **root** by CKey (`0x9cf790`): a text table (`#` header, columns incl. FILENAME and
     INSTALLPATH); live **62 rows** (the `.apm` manifests and similar files);
   - adds the built-in TACT key **`FB680CB6A8BF81F3` = `62D90EFA7F36D71C398AE2F1FE37BDB9`** to the keyring
     (emplace `0x9d0470`; Prometheus uses the same function to inject more keys).

`g_tact` layout: +0x00 root CKey, +0x10 path, +0x30 build info, +0x38/+0x40 root rows/count,
**+0x50 TACT container** (vfunc +8 `Execute(result, request)`: type 8 = size by key, type 4 = read by key),
+0x58 keyring.

**Asset Package manager** (slot 14, Start `0x9c3500`): loads the `.apm` manifests (main + speech) into
package tables: live **1163 + 169 packages**, each 0x48 bytes (`AssetPackListItem`: +0x18 TACT key name,
+0x20 package GUID, +0x2c size, +0x38 **CKey**). Then it requests two asset-package resources
(`0x06E000000000008D`, `0x06E00000000000B5`, type 0x077). Package lookup by GUID `0x9c4e40` (linear;
"Reference to unknown Asset Package").

**The CKey problem**: the client has **no global GUID → CKey index**. A resource's CKey comes from the
asset-package records: the package item (`+0x38`) or a 36-byte record inside a loaded ASSETPACKAGE resource
(CKey at record `+0x14`, size `+0x08`; the package header's `+0x40`/`+0x34` are the CKey/size of the package's
bundle, ContentAccess.md §4). A request without a CKey fails with "Resource GUID not found in manifest".

**Below the container** [M; formats measured and checked in code: [ContentAccess.md](ContentAccess.md)]: an embedded Blizzard CASC library ("CASCHandler", "failed to bind CASC index
at '%s'", `casc::Error`; code around RVA `0xba0000–0xbbffff`) and a "CASContainer" TACT layer (around
`0xf78000–0xf86000`): the standard CKey → encoding → EKey → `data/casc/data/*.idx` → `data.00N` → BLTE
(+ Salsa20 with the keyring). The encoding/index internals were not decompiled.

**On disk** (`game/owbetaclient`): `.build.info` (version 0.8.0.0.24919, enUS),
`data/casc/{config, indices, data/*.idx, data.000–003 (~3.6 GB), shmem}`, and a second cache CASC in
`cache/casc/`.

## 7. Threads

- **Game thread**: `AcquireHandle` / `RequestLoad`, the per-frame update (unloads, callbacks).
- **Resource loader thread**: queue merge, issuing reads, **construction**, dependencies, finishing.
- **TACT I/O thread + 1–8 decode workers** (count at RVA `0x17a0f00`, live 1): size, read, decrypt.

## 8. Loading data ourselves

### In the client (Prometheus)
1. **Already loaded**: `void* GetLoadedData(u64 guid)` (`0x9c6470`), on the game thread. Used by Prometheus
   today. [H]
2. **Force-load a GUID** [M, untested]: on the game thread,
   ```c
   int  AcquireHandle(u64* outHandle, u64 guid);             // 0x9c8330: 2 ok, < 0 error
   int  RequestLoad(u64 handle, LoadRequest* req);           // 0x9c88c0: 2 queued, 0 already loaded
   bool IsLoaded(void* entry); bool IsFailed(void* entry);    // 0x9c6680 / 0x9c6620  (entry = handle & ~0x3f)
   int  Release(void* unused, void* entry);                  // 0x9cad30
   ```
   with a `LoadRequest` built like `LoadPackage` (`0x9cbee0`) does: vtable `gameBase + 0x157f1a8`, GUID,
   −1, pointer to the **CKey**, priority 1, size/flags from the package record. Then poll `IsLoaded` and read
   `entry->data`. The hard part is the CKey (see §6); for resources in already-loaded packages,
   `LoadRecordFromPackage` (`0x9cc410`) is the easier path (its structures are only partly known).
   Caveats: construction runs on the loader thread; dependencies load only if acquired; refs must be
   released; during map transitions `AcquireHandle` returns −15.
3. **Raw bytes by CKey** [L–M]: `container = *(g_tact + 0x50)`, `container->vfunc[1](container, &result,
   &request)` with type 8 (size) then 4 (read): synchronous, already decrypted; request layout only partly
   mapped (see `0x9d46a0`); not synchronised with the TACT thread.

### Offline (for a server): done, see [ContentAccess.md](ContentAccess.md)
`systems/content/` reads the install from disk with our own CASC/TACT/APM code (no CascLib/TACTLib):
`.build.info` → build config → encoding → idx/data → BLTE (+ Salsa20, all nine keys of `casc_keys` are
needed) → TACT root → `.apm` manifests → 1,332 package blobs → a GUID → CKey index over every package
(150,898 resources + 1,160 bundles; every one reads and matches its CKey). This answers the CKey problem
offline. Formats as this client uses them, with code references: ContentAccess.md §2–4.

The in-client `GetLoadedData` remains useful to cross-check an offline reader against live objects.

## Open questions
- TACT container `Execute` request/result layouts (`0x9d46a0`, `0x9cf790`); CASC encoding/index internals;
  the remote path (states 2–5, 7; `0x9cf630`).
- ~~ASSETPACKAGE record format~~: see ContentAccess.md §4 (36-byte gzip'd records, bundles, dependencies).
  Whether a fast GUID → record lookup exists in the client (`0x9cd5b0` "Package Query").
- The two sorted GUID lists at RVA `0x1830c88/c90` searched by `0x9d0dd0` (look like a "failed to build"
  diagnostic list, not a CKey index).
