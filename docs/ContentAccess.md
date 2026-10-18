# Content access: reading the 0.8 install offline

How to get the bytes of any resource (by GUID) from the Overwatch beta 0.8 (build 24919) install on disk,
without a running client: the storage formats as this client uses them, and the library that reads them
(`systems/content/`).

Related: [ResourceLoading.md](ResourceLoading.md) (GUIDs §1; how the client loads, §6),
[STU.md](STU.md) (what most resources are). Library: [systems/content/README.md](../systems/content/README.md).

**Addresses**: RVA (runtime base `0x140000000`; Ghidra = `0x7ff64ffe0000 + RVA`).
**Status tags**: [D] measured on the install's data (every file / every resource unless noted),
[G] read in the client's code, [D+G] both, [?] inferred.

---

## 1. Summary

```
.build.info ──Build Key──▶ build config ──encoding EKey──▶ encoding table (CKey → EKey)
                                   └──root CKey──▶ TACT root (62 rows: file name → CKey)
                                                     └──▶ Manifest/Win_SPWin_RDEV_LenUS_EExt.apm (+ speech)
                                                            └──▶ 1163 + 169 packages (GUID, CKey)
                                                                   └──▶ records: resource GUID → CKey, size
read(CKey) = encoding → EKey → data/*.idx → data.NNN record → BLTE (N/Z/E/F; Salsa20 with the keyring)
```

Measured on `game/owbetaclient` [D]: 146,389 idx entries, 341,442 encoding entries, 62 root rows,
1,332 packages, 1,397,709 package records → **150,898 distinct resource GUIDs + 1,160 bundles**. Every one
of the 152,058 resources reads, and MD5(bytes) = its CKey for all of them (`content_dump --check`, ~30 s,
6.6 GB + 1.4 GB of bundles). The index is built in ~0.2 s (warm page cache).

**The "CKey problem" (ResourceLoading.md §6) offline**: the client has no global GUID → CKey index because
it only reads the packages it needs. Offline we read all 1,332 package blobs at open and build that index
(GUID → first package record). A GUID listed by several packages always has the same CKey (1,248,143
duplicate records, 0 with a different CKey) [D].

## 2. Layer 0: local CASC (`data/casc/data`)

**Index version per bucket**: 16 buckets; file `<bucket:02x><version:08x>.idx`. The current version of
each bucket is in `shmem`: `u32 versions[16]` at `+0x110` (shmem header u32 = 4 or 5) [D+G: `0xf7a710`,
file name `0xf81de0`]. The install has two versions per bucket; shmem names the newer one. (The running
client rewrites `shmem`; the reader only reads it.)

**`.idx`** [D+G: bind `0xf83a90`, "KeyMappingTable"]:
```
u32 header size (0x10), u32 header hash (lookup3)
header: u16 version = 7, u8 bucket, u8 extra = 0, u8 size bytes = 4, u8 offset bytes = 5, u8 key bytes = 9,
        u8 archive-offset bits = 30, u64 max archive size
at (header size + 0x17) & ~0xF: u32 entries size, u32 entries hash (lookup3)
entries, 18 bytes: EKey[0..9], u40 big-endian (archive << 30 | offset), u32 LE size
(after 0x1000 alignment: an update section of 0x200-byte pages; empty/unused here)
bucket(EKey) = x ^ (x >> 4) & 0xF with x = XOR of the 9 key bytes  [G: 0xf7c540]
```
The 30-bit split is the container's default parameter (`0xf78e90`) and fits the data; the split itself
was not found in code [D, G partial].

**`data.NNN` record** [D+G: builder `0xbd7030`, reader skips 0x1E at `0xb8c370`]: 0x1E-byte header
`{EKey[16] byte-reversed, u32 LE record size (= idx size), u8 flags, u8 0, u32 checksum A, u32 checksum B}`,
then the BLTE stream. The reader checks the EKey and size, not the checksums.

**BLTE** [D+G: `0xbed110`, E chunk `0xbf1060`, Salsa20 `0xbf0b30`]:
```
"BLTE", u32 BE header size (0 = one chunk, no table)
table: u8 flags = 0x0F, u24 BE chunk count, per chunk {u32 BE encoded size, u32 BE decoded size, MD5[16] of the encoded chunk}
chunk = mode byte + payload:
  'N' raw   'Z' zlib   'F' nested BLTE
  'E' u8 key-name size (8), u64 LE key name, u8 IV size (<= 16), IV, u8 'S' (Salsa20; the client
      also accepts 'A'), ciphertext → decrypts to a chunk again
Salsa20/20, 16-byte key ("expand 16-byte k"), nonce = first min(IV size, 8) IV bytes XOR the chunk index
(u64 LE)
```
Seen in the install: N, Z, E-with-S; no 'A' [D]. A wrong key does not fail at decryption: the plaintext's
mode byte comes out as garbage ("unknown chunk mode … wrong key?").

**Keyring** [D]: all nine 0.8 keys (`casc_keys` in `prometheus/Prometheus-Core/dllmain.cpp`) are needed.
Resources that fail to read without each key (`content_dump --no-builtin-keys --key …`):

| key name | built into | resources without it |
|---|---|---|
| `FB680CB6A8BF81F3` | client (ContainerInit `0x9ce9d0`) | 8,654 |
| `402CD9D8D6BFED98` | "hardcoded" per Prometheus | every package blob (nothing opens) |
| `DBD3371554F60306` | streamed by the live server | 19,128 |
| `11A9203C9881710A` | streamed | 72,269 |
| `A19C4F859F6EFA54` | streamed | 2,484 |
| `87AEBBC9C4E6B601` | streamed | 14,232 |
| `DEE3A0521EFF6F03` | streamed | 3,472 |
| `8C9106108AA84F07` | streamed | 10,286 |
| `49166D358A34D815` | streamed | 19,041 |

## 3. Layer 1: TACT

- **`.build.info`**: `Name!TYPE:len|…` header + rows (eu active, us inactive; same keys). Build Key
  `85f2a2c9…`, CDN Key `6677e5d1…`, version `0.8.0.0.24919` [D].
- **Build config** (`data/casc/config/85/f2/85f2a2c9a8e18462aed1769e883e13e5`): `root = f3aef5d7…`,
  `encoding = <CKey> <EKey>` (`13389924…` `ba6923df…`), `build-name = prometheus-0_8_0_0-24919` [D].
  The CDN config lists the archives of `data/casc/indices/*.index` (CDN indices: not needed locally).
- **Encoding table** (read by its EKey, 36 MB) [D+G: parser `0xbbd950`]:
  ```
  "EN", u8 version 1, u8 CKey size 16, u8 EKey size 16, u16 BE CKey page KB (4), u16 BE EKey page KB (4),
  u32 BE CKey page count (3192), u32 BE EKey page count (2095), u8 0, u32 BE ESpec size
  ESpec strings; CKey page index {first CKey, MD5}; CKey pages; EKey page index; EKey pages
  CKey page entry: u8 EKey count, u40 BE decoded size, CKey, EKeys…  (a 0 count ends the page)
  ```
  CKey → EKey needs the CKey pages only; the client also uses the EKey pages/ESpec (E-size queries).
- **Root** (by CKey, text): `#MD5|CHUNK_ID|FILENAME|INSTALLPATH`, 62 rows: 14 client files
  (`RetailClient/…`), 24 main manifests and 24 speech manifests `Manifest/Win_SPWin[_speech]_{RCN,RDEV}_L<locale>_EExt.apm`
  for 12 locales. RCN and RDEV rows have the same CKeys [D].

## 4. Layer 2: APM manifests and asset packages

**`.apm`** (main 85,932 bytes, speech 14,364) [D+G: parser `0x9c4bf0`, Start `0x9c3500`]:
```
+0x00 u64 0x133F7 (not read by the client)
+0x08 u32 build = 24919 (checked: other values → error 6)
+0x0C u32 package count (1163 / 169)
+0x10 u32 entry count (181)
+0x14 "fmc" + version byte 0x13 (checked)
+0x18 entry count × {u32 index, u64 value}      (no reader found)
then package count × 0x48-byte items, sorted by +0x00:
  +0x00 u64 lookup = typebits(asset) | crc32({u64 asset, u64 asset_param})  (binary search 0x9c4cf0; holds for all items)
  +0x08 u64 asset GUID the package belongs to     +0x10 u64 asset_param (0)
  +0x18 u64 a TACT key name (not used on the load path; the package blob itself needs 402CD9D8D6BFED98)
  +0x20 u64 package GUID (type 0x077; speech packages have bit 40 set)
  +0x28 u32 0   +0x2C u32 package blob size   +0x30 u32 sum of its record sizes   +0x38 CKey[16]
```

**Package blob** (resource type 0x077 "ASSETPACKAGE"; construct `0x9c4190`, records `0x9d08f0`,
LoadRecordFromPackage `0x9cc410`) [D+G]:
```
+0x00 u64 offset of the records (0x50)   +0x08 u64 offset of GUID references (0 = none)
+0x10 u64 offset of package dependencies (0 = none)
+0x18 u32 record count   +0x1C u32 gzip size of the records   +0x20 u32 reference count   +0x24 u32 dependency count
+0x28 u32 sum of record sizes
+0x2C u64 bundle GUID (type 0x09C "BINARY_PACKAGE_DATA"; 0 = no bundle)   +0x34 u32 bundle size
+0x38 u64 ?   +0x40 CKey[16] of the bundle
records: gzip stream (inflateInit2 with 31) of 36-byte records
  {u64 GUID, u32 size, u32 flags, u32 offset in the bundle, CKey[16]}
references: u64 GUIDs, requested without a CKey [G]    (0 in the whole install [D])
dependencies: u32 package indices → package GUID typebits(0x077) | index [G]; 3,552 in the install [D]
```
**Record flags** [G, counts D]: `0x40000000` = the record is also stored in the package's **bundle**
at `offset` (the client loads it from there); `flags & 7` = variant mask (used if 0 or bit `localeIdx` set);
`0x80000000` = kept in a side list (meaning unknown). Values in the install: 0 ×123,041, 7 ×5,296,
0x20000000 ×212, 0x40000000 ×1,088,356, 0x40000001/0x40000006 ×45,059 each, 0x40000007 ×861,
0x80000006 ×40,885, 0x80000007 ×35,291, 0xC0000006 ×3,136, 0xC0000007 ×10,513.

**Bundles** [D]: a bundle is exactly the concatenation of its package's flagged records (checked for
package `06E0000016B79D8C`: 482/482 records equal their slice). Every flagged record also has its own CKey
in the encoding table, so offline we read every record directly by CKey; bundles are listed as resources
of their own (1,160, type 0x09C).

## 5. STU blobs from CASC vs the live-dumped fixtures  [D]

`0x058000000000000F` (38,432 bytes) and `0x0400000000000039` (1,472 bytes) come out of the install with
the same size as `systems/stu/fixtures/*.stud`; `stu_dump` decodes both to **identical trees** with all
layout CRCs ok. **All 370 Statescript graphs and all 967 entity definitions** in the install parse with
every CRC matching (ctest `install_all_graphs_and_entity_defs_parse`). The bytes differ from the
fixtures only where the loader writes:
- **Instance header**: the upper u32 of each instance's type-hash slot holds the **offset of the next
  instance** (0xFFFFFFFF for the last), in all 1,337 files checked (the fixtures have 0 there).
- **Descriptor flag**: list/map/string descriptors have **flag = 0** in the file (the loader writes 2);
  164 such descriptors in the graph fixture still carry 2.
- **Graph root +0x08 / +0x10**: two u32s the engine fills after loading (0 in the file).
- Resource-reference handles are −1 in the file too (no difference).

## 6. The library (`systems/content/`)  [D]

- **Interface** `content::Store` (`include/content/store.h`): `Packages()`, `Resources()` (sorted GUID
  list with type/size/CKey/package), `Find(guid)`, `Read(guid)`, `ReadCKey(ckey)`, `GuidType(guid)`. Nothing
  about CASC/TACT/APM. Another version or a library-based backend derives from `Store`.
- **Default backend** `OpenOw08Install(dir, Ow08Options)` → `ow08::Ow08Store`, built from §2–4: own idx /
  BLTE / Salsa20 / MD5 / encoding / root / APM / package code; zlib for inflate; no CascLib/TACTLib.
  Options: region/locale (`RDEV`/`enUS`), speech manifest, MD5 check per read, keyring. Reads are
  thread-safe; the package blobs are read on up to 16 threads at open.
- **C API** (`capi/content.h`): `content_open_ow08`, `content_open_ow08_ex` (`content_options`: region,
  locale, speech manifest, MD5 per read, built-in keyring on/off, extra TACT keys), `content_find`,
  `content_read`, `content_read_ckey`, package/resource iteration, bulk copy (`content_resources`),
  resources of one type (`content_resources_of_type`), `content_package_manifest`, `content_guid_type`,
  package membership (T034): `content_package_resources` (a package's records: GUID + flags, package order)
  and `content_resource_packages` (every package listing a GUID; a linear scan of the ~1.4M records, ~13 ms).
  The store keeps all package records after open for this (12 bytes per record, ~17 MB for 0.8). Shared and
  static library for cgo.
- **Package owner** (T046): `PackageInfo.asset` / `content_package.asset` / Go `Package.Asset` = the APM item's
  `+0x08`, the asset the package belongs to (e.g. a map data or a hero); the explorer's reference index uses it.
- **Go wrapper** (`go/`, module `owcontent`, package `content`, T021): `Open(dir, *Options)`, `Describe`,
  `Packages`, `Resources`, `ResourcesOfType`, `Find`, `Read` (unknown GUID → `ErrNotFound`), `ReadCKey`,
  `TypeOf`, `PackageResources(pkg)` / `ResourcePackages(guid)` (package membership, T034); returns Go copies;
  safe for concurrent use; explicit `Close` (later calls → `ErrClosed`).
  Its tests read the install and parse what they read with the STU wrapper (`owstu/stu`): graph
  `0x058000000000000F` and entity definition `0x0400000000000039` decode to the same trees as the live
  fixtures, and all 370 graphs + 967 entity definitions (16.4 MB, read on 8 goroutines) match their CKeys
  and parse with every layout CRC. ECS, STU and content link into one Go binary.
- **CLI** `content_dump`: `--info`, `--root`, `--list-packages`, `--list-types`, `--list [TYPE]`,
  `--check [TYPE]`, `--read GUID OUT`, `--read-ckey`, `--read-file`.
- **Tests** (`ctest`): synthetic unit tests (MD5/Salsa20 vectors, BLTE modes incl. encrypted and nested,
  text tables, APM, package) and install tests (layer counts vs the client's live counts, the fixture
  comparison, all graphs/entity definitions, MD5 sample), C API test (plain C99).

## Open questions
- APM 12-byte entries (181) and header `+0x00`: no reader found.
- Record flag `0x80000000`, `0x20000000` and the variant mask's locale index in 0.8; package header `+0x38`.
- Which resources the GUID-only references would resolve (none in 0.8).
- BLTE 'A' encryption (ARC4?) and the E-chunk decode dispatch (not found; not used by the data).
