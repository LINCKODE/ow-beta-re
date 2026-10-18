# Asset formats: textures, models, materials, audio

The binary asset formats of the 0.8 client (build 24919) that are not STU: how they are stored in the install and how
the client reads them. Shared by the explorer tasks T063 (textures, §1), T064 (models / materials) and T065 (audio).
The readers live in `explorer/internal/<format>` (Go, no third-party code).

Related: [ResourceLoading.md](ResourceLoading.md) §1 (GUIDs), §3 (resource handlers), [ContentAccess.md](ContentAccess.md)
(reading resources offline), [Explorer.md](Explorer.md) "Texture view".

**Addresses**: RVA (runtime base `0x140000000`; Ghidra = `0x7ff64ffe0000 + RVA`).
**Status tags**: [G] read in the client's code (Ghidra), [D] measured on the install's data (all resources unless noted),
[?] inferred / not verified.

---

## 1. Textures (0x004) and texture payloads (0x04D)

### 1.1 Summary

```
texture 0x004 (32-byte header) ──payloads == 0──▶ inline data at +0x80
        │
        └──payloads 1–3──▶ payload i = 0x04D GUID 0x0320_000i_<index> (16-byte header + data)
                              flag 0x1000: the blocks are stored as planes (§1.5)
                              flag 0x2000: the payload patches the base texture's payload i (§1.6)
data of one chunk (inline, or one payload) = for each surface: its mips, largest first (§1.4)
```

| | count [D] | bytes [D] |
|---|---|---|
| textures (0x004) | 21,696: 13,630 with GUID bits 32..47 = 0, 8,066 with 0x000F (§1.7) | 755 MB |
| inline textures (no payloads) | 5,513 | |
| textures with 1 / 2 payloads | 8,228 / 7,955 | |
| payloads (0x04D) | 16,072 | 2.0 GB |
| delta textures (flag 0x2000) | 272 | |

Every texture decodes: all 21,696 load with every payload present and every mip placed, and mip 0 of every surface
decodes (`explorer/internal/texture` `TestAllTextures`, 15 s). Failure list: empty.

### 1.2 Loading in the client  [G]

- The resource handlers `TXTR` (type 0x004) and `TXTR_PAYLOAD` (0x04D) are registered by `0x84eb80` (with
  `TXTR_COMPRESS`, which has no construct function, and `ASSET_THUMBNAIL` 0x034).
- **TXTR construct** `0x860150` checks the header (the checks in §1.3), requests the payloads (§1.4), and builds a
  0x78-byte texture object `0x868710`. The object copies the header fields, keeps the inline data pointer (header
  + 0x80) and the data size (+0x0C) for the texture memory counters.
- **TXTR_PAYLOAD construct** `0x860870` only keeps the loaded bytes.
- **Upload**: the renderer installs the ready callback `0x97a660` (from `0x936190`). A texture without payloads is created
  from its inline data (`0x97b940`). A texture with payloads goes through `0x97acd0`, which reassembles split blocks (§1.5),
  applies deltas (§1.6), and hands each chunk to `0x961e60`, which lays out the D3D subresources (§1.4).

### 1.3 Header (32 bytes)

| offset | type | field | notes |
|---|---|---|---|
| +0x00 | u16 | flags | table below |
| +0x02 | u8 | mip count | < 17, else rejected [G] |
| +0x03 | u8 | format | DXGI_FORMAT, < 100 ("Texture format out of range: %u") [G] |
| +0x04 | u8 | surfaces | array slices; cubes count 6 per cube (a cube array of n cubes: 6n) [D] |
| +0x05 | u8 | ? | 0–13, copied into the texture object [G]; meaning unknown [?] |
| +0x06 | u8 | payload count | 0 = inline; payload handles for up to 3 [G]; 0.8 uses 0–2 [D] |
| +0x07 | u8 | 0 | always 0 [D] |
| +0x08 | u16 | width | 0 rejected ("Invalid texture width (0)") [G] |
| +0x0A | u16 | height | 0 rejected [G] |
| +0x0C | u32 | inline data size | 0 with payloads; the file is exactly 0x80 + size for all 5,513 inline textures [D] |
| +0x10 | u64 | ? | not read by the loader [G]; non-zero on every texture with payloads, 0 on inline ones [D] |
| +0x18 | u64 | base texture GUID | read only with flag 0x2000 (§1.6) [G] |
| +0x20 | | padding to +0x80 | inline textures only; the data starts at +0x80 [G] |

**Flags** (0.8 has 16 combinations):

| bit | meaning | evidence |
|---|---|---|
| 0x0001 | ? | 3 textures (R8, R16G16B16A16F), no other flag |
| 0x0002 | 2D texture | every non-cube texture except those 3 [D] |
| 0x0008 | cube | always with 6 × n surfaces [D]; the GPU description then has 6 faces per array slice [G: `0x961e60`] |
| 0x0010 | ? | only on the 272 delta textures (0x3032) [D] |
| 0x0020 | sRGB | only on *_SRGB formats, but not on every one (cubes with BC1_SRGB lack it) [D] |
| 0x0040 | array | on the 2D arrays (2–50 surfaces) and cube arrays (0x0048) [D] |
| 0x0100, 0x0400 | ? | 21 HDR cubes (0x0508) [D] |
| 0x0800 | the upload skips creating the subresources | tested in `0x97acd0` [G]; not in 0.8's data [D] |
| 0x1000 | payload blocks stored as planes | §1.5 [G] |
| 0x2000 | delta texture | §1.6 [G] |

**Formats** [D] (every texture's +0x03):

| DXGI | format | textures | | DXGI | format | textures |
|---|---|---|---|---|---|---|
| 2 | R32G32B32A32_FLOAT | 2 | | 61 | R8_UNORM | 86 |
| 10 | R16G16B16A16_FLOAT | 1,704 | | 71 | BC1_UNORM | 3,034 |
| 28 | R8G8B8A8_UNORM | 17 | | 72 | BC1_UNORM_SRGB | 5,218 |
| 29 | R8G8B8A8_UNORM_SRGB | 237 | | 77 | BC3_UNORM | 776 |
| 34 | R16G16_FLOAT | 320 | | 78 | BC3_UNORM_SRGB | 1,738 |
| 49 | R8G8_UNORM | 39 | | 80 | BC4_UNORM | 4,489 |
| 54 | R16_FLOAT | 445 | | 83 | BC5_UNORM | 2,506 |
| | | | | 95 | BC6H_UF16 | 1,085 |

No BC2, BC7 or signed formats. BC6H is used by 1,070 cubes (reflection probes) and 15 cube arrays. BC5 holds normal maps
(red = X, green = Y) [?: from how they look].

### 1.4 Payloads and data layout

- **Payload GUID** [G: `0x860150`]: payload *i* of texture *g* is
  `0x0320000000000000 | (i & 0xF) << 32 | (g & 0xFFFFFFFF)`, that is, type 0x04D, the payload index in bits 32..35, and the
  texture's index. The texture's own bits 32..47 are dropped, so the 0x000F variant shares its payloads with the
  0x0000 one (§1.7).
- **Payload header** (16 bytes) [D, all 16,072]:

  | offset | field |
  |---|---|
  | +0x00 | `top`: the index of the payload's largest mip, counted from the smallest mip |
  | +0x04 | `count`: the number of mips in the payload |
  | +0x08 | data size |
  | +0x0C | header size = 0x10 |

  The payload holds mips `first = mips − 1 − top` to `first + count − 1`. The client computes the same end,
  `count + mips − top − 1`, for the subresource range [G: `0x97acd0`].
- **Which payload holds what** [D]:
  - With one payload, payload 0 holds every mip.
  - With two payloads, payload 1 holds mip 0 and payload 0 holds mips 1 to the smallest. The client can load the small
    mips first and stream the largest.
  - Textures with 2 payloads are 8 to 4096 px; 4096 appears 22 times.
- **Order inside a chunk** (inline data or one payload): surface by surface, and within each surface its mips, largest
  first. This is D3D11's subresource order [G: `0x961e60` loops over surfaces, then mips, advancing one pointer].
  - [D] Comparing each mip with the box-filtered mip above it tells this order from the mip-major one: every one of the
    2,194 inline multi-surface textures with several mips fits it better.
  - [D] Unfolded, the cube faces join without seams (screenshot `explorer/screenshots/texture-cube.png`).
- **Mip size**:
  - Uncompressed: `w × h × bytes per pixel`.
  - Block-compressed: `⌈w/4⌉ × ⌈h/4⌉ × block bytes` (8 for BC1 / BC4, 16 for BC3 / BC5 / BC6H), with `w = max(1, W >> m)`.
  - The client rounds the pitch up to the 4-pixel block [G: `0x961e60`].
  - Non-power-of-two sizes occur (e.g. 300, 472, 764) [D].

### 1.5 Split blocks (flag 0x1000)  [G: `0x97acd0`, D]

A payload of a texture with flag 0x1000 stores its BC blocks **as planes**. The whole payload is one run: every mip and
every surface together, `n = max(1, size / block bytes)` blocks. Each plane holds one part of every block, in block
order:

| formats (DXGI) | planes, in file order (bytes per block) |
|---|---|
| BC1 (71, 72) | colour endpoints (4), colour indices (4) |
| BC3 (77, 78) | alpha endpoints (2), alpha indices (6), colour endpoints (4), colour indices (4) |
| BC4 (80, 81) | endpoints (2), indices (6) |
| BC5 (83, 84) | endpoints (4: red 2, green 2), indices (12: red 6, green 6) |
| others (BC6H, uncompressed) | stored plainly: the flag has no effect |

- The client rebuilds each 8- or 16-byte block from these planes before the upload.
- **Inline data is never split** [D]: the flag occurs only on textures with payloads, 15,845 of them.
- Decoding the planes as plain blocks shows the image squeezed into the top half of mip 0 and noise below it. That is how
  the layout was noticed [D].

### 1.6 Delta textures (flag 0x2000)  [G: `0x97acd0`, D]

272 textures (all BC3_UNORM_SRGB, flags 0x3032) are recolours of another texture: skins that differ from a base only in
colour. Header +0x18 names the **base texture**. Payload *i* of the delta is built from payload *i* of the base:

1. Read the base's payload *i* (`PayloadGUID(base, i)`) and rebuild its blocks with the BC3 plane order of §1.5. The code
   does this unconditionally; every base in 0.8 is a BC3_UNORM_SRGB texture with flags 0x1022 [D].
2. For each mip of the delta payload, from its first mip on:
   - a bit mask of `max(1, ⌈blocks / 8⌉)` bytes, where `blocks = max(1, w >> 2) × max(1, h >> 2)` and bit *b* stands for
     block *b*, read as u32 words, little-endian;
   - one u32 for each set bit, which replaces the block's colour endpoints (+8);
   - one u32 for each set bit, which replaces its colour indices (+12).
   - The base buffer then advances by `blocks × 16`.
3. The patched buffer is uploaded with the delta payload's own `top` / `count` (which equal the base payload's [D]).

- Alpha stays the base's.
- [D] Every delta payload is consumed exactly: 0 bytes left over.
- Example: `0x0C00000000002E66` (a green Tracer skin) over the base `0x0C0000000000032C` (orange); screenshot
  `explorer/screenshots/texture.png`.

### 1.7 The 0x000F variants  [D]

- 8,066 textures have `0x000F` in GUID bits 32..47, next to a texture with the same index and bits 0.
- Their package records carry variant mask 1; those of the 0x0000 texture carry mask 6 (ContentAccess.md §4, record
  flags & 7).
- In the examples checked, the variant is the same texture one mip smaller. For example, `0x0C00000F0000007E` is
  512² with 8 mips, and `0x0C0000000000007E` is 1024² with 9 mips.
- The variant has one payload, payload 0 of the shared index, which holds exactly the smaller chain.
- This looks like a low texture-quality setting that never loads the largest mip [?: the setting that selects it was not
  traced].

### 1.8 Validation

- **Whole install** (`explorer/internal/texture`, `make test`):
  - all 21,696 textures load, with every payload present and every mip placed;
  - mip 0 of every surface decodes;
  - a thumbnail builds from payload 0 alone.
- **Mip consistency**: on every 8th texture, the smallest mip of at least 16 px is compared with the 2×2 box average of the
  mip above it, surface by surface.
  - 4,079 checks, all under 0.2 mean absolute error (0..1 scale). The worst is 0.156, on a high-contrast BC3 atlas whose
    mips were made with a better filter than a box.
  - A wrong plane order or surface order gives about 0.3 on most textures, so the check catches both.
- **Decoders against another implementation**:
  - Pillow 12.3's BCn decoder, on up to 20 textures per format (DDS files written by `cmd/texscan -ref`).
  - BC1 / BC3 / BC4 / BC5 match within 1.4 / 255, which is rounding.
  - BC6H_UF16 matches within 1.12 / 255 over 40 textures. Pillow truncates to 8 bit and we compare floats.
  - Pillow has no sRGB BC variants; they share the UNORM block decoders.
- **By eye** (explorer): atlases, the Tracer base and the delta skin, the Anubis reflection-probe cube as a cross, and
  arrays. The user's confirmation of the explorer views is pending.

### 1.9 Open questions

- Header +0x05 (values 0–13) and +0x10 (a hash or id? non-zero on every texture with payloads).
- Flags 0x0001, 0x0010, 0x0100 and 0x0400.
- Which setting selects the 0x000F variant (§1.7), and whether the client ever loads a single payload of a 2-payload
  texture (streaming).
- The `TXTR_COMPRESS` handler (no construct function; it may belong to compression that 0.8's data does not use).
- Which resources reference textures: materials (0x008) and model looks, left for T064.

---

## 2. Models (0x00C)

### 2.1 Summary

```
model 0x00C: one file, header 0x1D0 bytes; every "pointer" in it is a file offset (the loader adds the base)
  +0x050 skeleton block ─▶ hierarchy, bind matrices, world / local TRS, bone ids, remaps     (§2.6)
  +0x160 u64 material keys [u16 +0x144] ◀── submesh +0x43 indexes them; a look maps key → material (§4)
  +0x168 submeshes [u8 +0x153] × 0x48 ─▶ draw: VB, IB, index range, base vertex, material, LOD mask (§2.5)
  +0x170 vertex buffers [u8 +0x150] × 0x38 ─▶ elements (8 bytes each), 2 interleaved streams     (§2.4)
  +0x178 index buffers [u8 +0x151] × 0x18 ─▶ u16 triangle lists
  +0x1A8 / +0x1B0 / +0x1B8 tables of GUIDs the loader requests (all 0x08E in 0.8)             (§2.7)
```

| | count [D] |
|---|---|
| models (0x00C) | 2,972, 464 B to 14.3 MB (median 42 KB), 568 MB |
| submeshes | 8,784; 8.5 M triangles, 12.9 M vertices over every LOD |
| with LODs (submesh masks 1 / 2 / 4 / 8) | 715 (540 with one LOD bit, 74 / 27 / 74 with 2 / 3 / 4); 2,257 draw every submesh (mask 0xFF) |
| with a skeleton | 444 |
| skeleton resources (0x007) | 23, same construct as models [G] (not decoded further) |

Every model parses, every LOD decodes with every index inside its vertex buffer, and every skeleton's parents come
before their bones (`explorer/internal/model` `TestAllModels`, 2 s). Failure list: empty.

### 2.2 Loading in the client  [G]

- Handlers are registered by `0x84da60`: **MODEL** (type 0x00C) and **SKELETON** (0x007) share construct `0x84df00` and
  destroy `0x84e5f0`; **MATERIAL** (0x008) has construct `0x84dd80` (§3.2). No ready callback.
- **Construct** `0x84df00`:
  - Header +0x40 holds three u32: the size of the CPU part for two quality settings and the file size (= the file's
    length on all 2,972 [D]). With the global setting at `0x17a1838` = 1 or 2, only the first +0x40 / +0x44 bytes are
    copied into a new buffer and +0x1C8 keeps the distance to the full file, so offsets past the CPU part still resolve
    against the whole file [G]. In the data, +0x40 is where the vertex data begins [D, e.g. 0x1D: 0x710].
  - `0x85b1a0` turns the offsets into pointers (the fields listed in §2.3 are exactly the ones it touches); `0x85aa90`
    does the skeleton block, `0x8684c0` the 0xD8-byte records.
  - It then requests the GUIDs of §2.7 as dependencies (`0x848590`).

### 2.3 Header (0x1D0 bytes)

| offset | type | field | evidence |
|---|---|---|---|
| +0x00 | f32 × 16? | bounds / unknown | [?] |
| +0x40 | u32 × 3 | CPU part size (quality 1, quality 2), file size | [G, D] |
| +0x50 | 0x60 bytes | skeleton block (§2.6) | [G: `0x85aa90`] |
| +0xCB | u8 | count of 0x60-byte records at +0x158 | [G] |
| +0xCC | i32 | count of 0xD8-byte records at +0x1B8 (cloth? [?]) | [G] |
| +0x144 | u16 | material key count (+0x146 is equal on 2,964 of 2,972) | [D] |
| +0x14C / +0x14E | u16 | counts of 0x90-byte records at +0x198 / 0x58-byte records at +0x1A0 | [G] |
| +0x150 | u8 | vertex buffer count | [G, D] |
| +0x151 | u8 | index buffer count | [G, D] |
| +0x152 | u8 | union of the submeshes' LOD masks (0xFF, 1, 3, 7, 0xF) | [D] |
| +0x153 | u8 | submesh count | [D] |
| +0x158 | offset | 0x60-byte records (+0x50 is an offset) | [G] |
| +0x160 | offset | u64 material keys | [G, D] |
| +0x168 | offset | submeshes | [G, D] |
| +0x170 | offset | vertex buffer descriptors | [G, D] |
| +0x178 | offset | index buffer descriptors | [G, D] |
| +0x180, +0x188, +0x190 | offset | unknown | [G] |
| +0x198, +0x1A0 | offset | 0x90- / 0x58-byte records (4 / 3 offsets each) | [G] |
| +0x1A8, +0x1B0, +0x1B8 | offset | dependency tables (§2.7) | [G] |
| +0x1C0 | offset | 2 offsets | [G] |
| +0x1C8 | i64 | 0 in the file; the loader's base delta | [G] |

### 2.4 Vertex and index buffers

**Vertex buffer descriptor** (0x38 bytes) [G: the loader fixes +0x20 / +0x28 / +0x30; D: all 3,811]:

| offset | field |
|---|---|
| +0x00 | u32 vertex count |
| +0x04 | u32 hash (27 values) [?: a layout id] |
| +0x08, +0x09 | u8 stride of stream 0, stream 1 |
| +0x0A | u8 element count |
| +0x0B | u8 0, 2 or 6 [?] |
| +0x10 | 16 bytes of 0xFF |
| +0x20 | offset of the elements (8 bytes each) |
| +0x28, +0x30 | offsets of stream 0 and stream 1 (count × stride bytes each) |

**Element** (8 bytes): u8 semantic, u8 index, u8 format, u8 stream, u16 0, u16 offset in the stream.

| semantic | format | stream | elements [D] |
|---|---|---|---|
| 0 position | 2 = float3 | 0, offset 0 | 3,811 (every buffer) |
| 4 bone indices | 6 = u8 × 4 | 0, offset 12 | 608 |
| 5 bone weights | 8 = unorm8 × 4 | 0, offset 16 | 608 |
| 1 normal | 9 = snorm8 × 4 | 1 | 3,798 |
| 3 tangent (w = sign) | 9 | 1 | 3,798 |
| 8 colour (index 0, 1) | 8 | 1 | 1,770 |
| 9 texture coordinates (index 0–2) | 4 = half2 | 1 | 5,502 |
| 16 id | 12 = u32 | 1 | 3,811 |

- Semantic and format numbers: [D] from the byte sizes and the values. The normals decoded as snorm8 are unit length
  on all but 498 of 12.9 M vertices; UVs as half floats fall on the textures. The client's table that turns them into a
  D3D input layout was not read [?]. The same numbers are OWLib's for later builds.
- Stream 0 is 12 bytes (position) or 20 (position + skinning); stream 1 is 8–28 bytes.

**Index buffer descriptor** (0x18 bytes): u32 index count, u32 0, 8 × 0xFF, offset of the u16 indices. Triangle lists.

### 2.5 Submeshes (0x48 bytes)  [D]

| offset | field |
|---|---|
| +0x00 | u64 −1 |
| +0x08 | 11 × f32 [?: bounds of the submesh] |
| +0x34 | u32 base vertex: the indices are relative to it |
| +0x38 | u16 first index in the index buffer |
| +0x3A | u16 index count |
| +0x3C | u32 [?: a vertex count; 7,725 of 8,784 equal max index + 1, the rest do not] |
| +0x40 | u8 flags: 0x0C (4,799), 0x10 (2,072), 0x2E (1,157), 0x0E (685), 0x08 (67), … Bit 0x10 = **proxy** (below) [D]; the other bits [?] |
| +0x41 | u8 vertex buffer |
| +0x42 | u8 index buffer |
| +0x43 | u8 material: index into the material keys (+0x160) |
| +0x44 | u8 LOD mask: 1 = the finest LOD … 8 = the coarsest; 0xFF = drawn at every LOD |

- The draw is `DrawIndexed(count, first, base vertex)`: on all 8,784 submeshes, base vertex + the largest index is
  inside the vertex buffer, and the smallest index is 0 [D].
- **Proxy submeshes** (flag 0x10) [D]: 2,072 submeshes on 1,400 models, always next to the real submeshes and
  always drawn at every LOD (mask 0xFF), with a median 6 % of the model's other triangles. No model consists only of
  them. They are a coarse copy of the model: drawn over the real mesh (with other UVs), they made the Anubis statue
  `0x0D…07BD` look patchy and asymmetric (the user's in-game screenshots show it without them). The explorer leaves
  them out of every view and export ("proxy" in the Model tab shows them). Their use in the client (shadow casting,
  occlusion) was not traced [?].
- LOD masks [D]: 1 = 1,259, 2 = 395, 4 = 271, 8 = 206 submeshes, 0xFF = 6,653. The LOD1 submesh of a model has fewer
  triangles than its LOD0 one (e.g. 0xBC0: 570 → 11). What picks the LOD (distance, the `ModelQuality` /
  `ModelLODPush` settings) was not traced.

### 2.6 Skeleton (+0x50, 0x60 bytes)  [G: the fix-up `0x85aa90`; D]

| offset (in the block) | field |
|---|---|
| +0x00 | hierarchy: 6 bytes per bone: u16, u16 [?], **i16 parent** (−1 for the root) |
| +0x08 | 4 × 4 f32 per bone [?: local matrix, only equal to the local TRS on unrotated bones] |
| +0x10 | 64 bytes per bone [?: an inverse bind pose in a packed form; row 3 = −world position] |
| +0x18 | **world bind TRS**, 48 bytes per bone: quaternion x y z w, scale x y z + pad, position x y z + pad |
| +0x20 | **local TRS** (relative to the parent), same layout |
| +0x28 | 32 bytes per bone [?: zeros in the samples] |
| +0x30 | u32 bone id per bone |
| +0x38 | offsets per bone, to ~14-byte records [?] (the fix-up resolves them) |
| +0x40 | u16 per bone [?] |
| +0x48 | u16 × counts[3] [?: a remap] |
| +0x50 | u16 per bone, −1 = none [?: an inverse remap] |
| +0x58 | u32 hash |
| +0x5C | i16 × 6: bone count (the fix-up loops over it), bone count again, 0, a count (218, 137), bone count, a count |

- On all 444 skeletons the parent comes before its bone, and `local ∘ parent's world = world` holds to 1e-3 for every
  bone but one (model 0x0D07 bone 1: the translation matches, the rotation does not) [D]. Matrices use the row-vector
  convention (translation in row 3).
- Bone indices in the vertices (semantic 4) are not checked against these bones; skinning and animation are out of scope.

### 2.7 References

- The construct requests these GUIDs [G `0x84df00`]:
  - +0x1A8: a table of five arrays (offsets at +0x00…+0x20, counts at +0x28…+0x38, strides 0x50, 0x38, 0x50, 0x58, 0x60);
    element +0x00 is a GUID. The first array's GUIDs are loaded as dependencies; the others are recorded with
    their +0x10 value.
  - +0x1B0: an array of 0x38-byte records (GUIDs at +0x00 and +0x10, count at +0x18) and one of 0x20 bytes (GUID
    at +0x00, count at +0x20).
  - +0x1B8: the 0xD8-byte records hold GUIDs at +0xA0 and +0xB8. The same slots also hold non-GUID values
    (e.g. 0x0010000000000001) [D].
- In 0.8 every one of them is a **0x08E** resource ("effect-like", T046) [D: 112 models, 144 distinct pairs].
- **A model names no material, texture or look**: which materials it wears comes from a look (§4).

## 3. Materials (0x008)

### 3.1 Summary

```
material 0x008 (144–1,088 bytes): shader GUID + 6 offsets + counts
  textures [u8 +0x60] × 16 bytes at offset +0x20: texture GUID (0x004), slot hash, flags
  constant buffers [u8 +0x61] × 16 bytes at offset +0x38: self-relative data offset, size, hash
```

11,150 materials, all parse; every texture binding names an existing texture (`TestAllMaterialsAndLooks`) [D].

### 3.2 Loading in the client  [G]

- MATERIAL is registered by `0x84da60` with a type id from `0x7f8540(8, 0x4e, "Material", …)`.
- **Construct** `0x84dd80` calls the fix-up `0x84d460` (the six offsets at +0x10…+0x38 become pointers; each of the
  +0x61 records at +0x38 has a self-relative offset in its first 8 bytes). It then checks +0x00: the material is
  accepted only if it is `0x0E10000000000041`, `0x0E10000000000065` or `0x0E10000000000020` … `0x0E10000000000038`,
  otherwise the load fails. These are type 0x088 GUIDs (SHADER SOURCE handler, ResourceLoading.md §3), a fixed set of
  shaders.

### 3.3 Layout

| offset | field | evidence |
|---|---|---|
| +0x00 | u64 shader GUID (0x088) | [G, D: 24 of the 27 allowed occur] |
| +0x08 | u64 0 or a 0x03A GUID (5,243 materials) | [D; meaning ?] |
| +0x10…+0x38 | 6 offsets (0 = absent) | [G] |
| +0x10 | → **parameter stream**, u16 count at +0x56 (T071, below) | [G, D] |
| +0x20 | → texture bindings | [D] |
| +0x28 | → u32 list [?: parameter hashes] | [D] |
| +0x38 | → constant buffer records | [G, D] |
| +0x40 | f32 × 4 [?: a colour] | [D] |
| +0x60 | u8 texture count | [D] |
| +0x61 | u8 constant buffer count | [G] |
| +0x68 | u8 flags: bit 1 = **helper / editor material** (§3.5); 0x40 (3,856) and 0xC0 (1,163) common, meaning unknown | [D] |

- **Texture binding** (16 bytes): u64 texture GUID, u32 slot hash, u32 flags (0, 2 or 3) [D].
- **Constant buffer record** (16 bytes): i64 offset of the data from the record, u8, u8, u16 size, u32 hash [G, D].
- **Slots**: the hash is CRC32 of the lower-cased slot name. Recovered by guessing names: `basecolor` (0xAD10F602,
  5,305 bindings, BC1/BC3 sRGB), `normal`, `normalmap`, `opacity`, `color`, `ao`. Inferred from the formats bound to
  them [?]: 0x169615AA (BC5 on 4,847 of 5,215: a normal map), 0x2CE679FF / 0x12BBC7D3 (half-float: lighting),
  0x20AF06CE (BC1 linear, packed), 0xBCBE747D / 0xC6D17199 (BC4 masks). 252 distinct slot hashes.
- **Slots (T071)**:
  - More names were recovered by CRC32 dictionary search; the short dictionaries had an expected false-hit count far
    below one:
    - `baseao` 0xC6D17199, `baseemissive` 0xBCBE747D, `basenormal` 0x169615AA, `basemask` 0x5862A480;
    - `baseoverlay`, `basenormaloverlay`, `baseroughnessspecular`, `roughnessspecular`, `roughness`;
    - `emissive`, `emissivecolor` 0xEDCD5483, `emissivemap`, `diffusecolor`, `diffusemap`, `tintmap`;
    - `layer1color`, `layer2color`, `secnormal`, `secheightmap`, `overlayao`, `flowmap`, `basethickness`,
      `normalstrengthmap`, `cloudcolor`, `foamcolor`, `colorsource`;
    - `texture_0` … `texture_63` (effects).
  - The shader bytecode gives the meaning of others:
    - 0x20AF06CE is a **packed spec/gloss** texture (R = spec/metal scalar, G (B) = gloss);
    - 0x2CE679FF is a gradient LUT, not lighting;
    - 0x12BBC7D3 is the effects' over-lifetime curve, read in the VS.
  - The channel use per shader family is in [Explorer.md](Explorer.md) "Material view".
- **Parameter stream** (+0x10, T071) [D]:
  - Items are {u32 hash, u8 type, u8 count, u16 0, data}. Types: 2 / 3 = u32, 6 = float, 8 = float2, 10 = float4,
    11 = float4 (colours). This parses cleanly on all 11,150 materials.
  - The hashes are the material constant buffer's variable hashes, not CRCs of lower-case names. Known uses: UV scale
    0x704029EA / offset 0x811D50FA, and the emissive intensities 0x91D6C98F / 0xC1BE91CC (Explorer.md "Material view").

### 3.4 Which texture is the colour, and opacity  [D]

- Most materials bind their colour texture to `basecolor` (5,305 bindings) or `color` (658).
- Others use another slot name for it. *Correction (T071, bytecode)*: the decal shader `0x0E10000000000022`'s colour
  is `color` (0x665648E9). 0xEDCD5483 (`emissivecolor`) is the decal's emissive mask (.r); it is a colour only on the
  glow shader 0x38. On hero materials (0x2C / 0x2B) `basecolor`.a is AO, not opacity. So the explorer takes the first texture in an sRGB format (BC1 / BC3 / RGBA8 sRGB) when
  there is no `basecolor` / `color`. The sRGB formats hold colour and the linear ones masks, normals and lighting
  (§1.3 table) [?: a rule inferred from the formats, not read in the shaders].
- `opacity` (0x43FD6D66, BC4 on 649 of 695) is an alpha mask. Decals with an opacity mask and **no colour texture**
  (e.g. `0x0E…0237`: opacity + normal only) change only the normals of what is under them; the preview doesn't draw
  them. Before this rule they showed as solid strips on the model's edges.
- Texture addressing: wrap. Negative and >1 U values are ordinary wrapping. The Anubis statue (`0x0D…07BD`) first
  looked like it needed mirror addressing; its real problem was the proxy submesh (§2.5).
- **Correction (T070, Rendering.md §9.5)** [D]: `0x0E10000000000028` is the **effect / particle shader**, not an
  environment shader: its 2,502 materials are referenced only by effect resources (0x00D, 0x04A, 0x08E, 0x08F).
- *T071*: which textures use UV 1 is now read per family from the bytecode (e.g. 0x21's `baseao`, the 0x2C AO /
  emissive UV set parameters; Explorer.md "Material view"). The earlier open point follows.
- Still unexplained: the environment shader `0x0E10000000000028` (2,502 materials) binds only half-float textures
  (0x12BBC7D3: RGBA16F / RG16F / R16F) and BC4 masks. Its materials have high indices (e.g. `0x0E00000000172ADD`), and
  none of the ones checked has a colour texture. Models with two UV sets (semantic 9 index 1) exist, and which
  textures use UV 1 is not known: the binding flags (0 / 2 / 3) are a candidate [?].

### 3.5 Helper / editor materials  [D]

Material +0x68 **bit 1** is set on exactly 9 of 11,150 materials:
- the six "Player Only" / "Player Only · No Walk · No Grapple" materials (`0x0E…0794`, `…0796`, `…0797`, `…07A0`,
  `…07A1`, `…07A2`, all on shader 0x…21, textures `0x0C…1B8D` / `0x0C…1BA4`);
- `0x0E…07C0` (shader 0x…23, the same helper look's slot 0);
- the orange / blue checker placeholder `0x0E…0005` (texture `0x0C…0011`, used by 718 looks) and `0x0E…1CF0`.

The helper boxes are ordinary model-group records: record flags 3 (like other model groups), a unit-cube model
(`0x0D…078C`, 12 triangles) placed 150 times on Anubis with two looks. The client's static world
(`0x7ff650a04e60`, types 1–7) creates a render instance for every model-group instance and checks no flag there [G].
Whatever hides them in game (the renderer testing this material flag, or a debug setting) was not traced [?]. The
explorer treats these submeshes as **helper geometry**: the 3D map's "helpers" layer, off by default. Anubis has 868
placements with helper parts.

## 4. Model looks (0x01A)

A look is an STU resource (`STUModelLook`, 3,687): `m_materials` is a list of `STUModelMaterial {m_material → 0x008,
0x07E44BAA: u64 key}` (plus animation permutations and material effects).

**Model + look → materials** [D]: submesh +0x43 indexes the model's material keys; the look's entry with that key names
the material. Model 0x23 with look 0xC0: all keys resolve. Model 0x11CE with look 0x14B0 (the pair from entity
definition 0x0D93's `STUModelComponent`): 11 of 14 keys resolve, the other 3 have no entry. How the client draws a
submesh without an entry was not read [?].

**Which look goes with a model** comes from where they are placed together:
- `STUModelComponent` (and any STU object) with `m_model` and `m_look`;
- map records: model + look (types 2, 8), model groups (a look per group), the sky (Maps.md §1, §2).

The key alone doesn't pick a look: keys are shared (most models share a key with more than 50 looks) [D].

## 5. Audio (T065)

### 5.1 Summary

The client links Audiokinetic **Wwise** (SDK strings: `…\3rdPartyArchive\Audiokinetic\Code_Lvl_3_Win\SDK\…`) [G]. Sounds
are STU resources that name a Wwise bank and the WEM files its sounds play; banks carry no media.

```
STUSound 0x02C ──m_versionedBankData──▶ STUSoundBankDataVersion { m_soundBank → bank 0x043,
      m_wwiseEventIDPlay / Stop (Wwise event ids)      m_soundWEMFiles[i] → WEM 0x03F  with id m_wwiseWEMFileIDs[i],
                                                       m_soundWEMStreams[i] → WEM 0x03F with id m_wwiseWEMStreamIDs[i],
                                                       0x40ac0372 = the bank's BKHD id }
bank 0x043 (BKHD v112 + HIRC): Event ─▶ Actions ─▶ containers ─▶ Sound objects { source id = a WEM id above }
voice line STU 0x059 ──0xfa1f65e6──▶ voice WEM 0x0B2 (played through an External Source sound, §5.5)
switch group 0x02D, switch 0x02E (→ its group), parameter 0x02F, state group 0x030: { name, Wwise id = FNV-1(name) }
```

| type | handler [G] | what | count [D] | bytes [D] |
|---|---|---|---|---|
| 0x02C | STU | `STUSound` | 3,821 | 2.6 MB |
| 0x02D | STU | `STUSoundSwitchGroup` | 5 | |
| 0x02E | STU | `STUSoundSwitch` | 9 | |
| 0x02F | STU | `STUSoundParameter` (Wwise game parameter / RTPC) | 67 | |
| 0x030 | STU | `STUSoundStateGroup` | 6 | |
| 0x03F | `SOUNDWEMFILE` | WEM (RIFF, Wwise Vorbis) | 11,703 | 252 MB |
| 0x043 | `SOUNDBANK` | Wwise bank, BKHD + HIRC | 3,821 | 8.8 MB |
| 0x0B2 | `VOICEWEMFILE` | voice WEM (same format) | 6,904 | 121 MB |

- **Handlers** [G]: `0xa4e0e0` registers `SOUNDBANK` (0x043), `VOICEWEMFILE` (0x0B2) and `SOUNDWEMFILE` (0x03F). Their
  construct functions only allocate a buffer for the bytes (`0xa4dee0`; the voice one, `0xa4df20`, 4 bytes more) and
  hand them to Wwise. Missing media logs "%s not loaded - WEM not available: 0x%016llX".
- Every 0x03F WEM is listed by 1–3 sounds (4,148 / 3,491 / 4,064); no sound lists a 0x0B2 WEM [D]. 169 WEM entries
  are under `m_soundWEMStreams`, all 0x03F [D].
- One sound, `0x0D40000000000F49`, has no bank data and no events [D].
- `STUSound`'s three teString fields (`0xc602d346`, `0x81386f9f`, `0x32ef0298`) are empty in every sound [D]. So the
  names of sounds, events and banks are stripped.

### 5.2 WEM (RIFF) header  [D: all 18,607 WEMs]

A RIFF/WAVE file. Chunks: `fmt ` (always 0x42 bytes), `data`; some add `cue ` (2,030), `smpl` (loop points, 113),
`LIST` (8), `JUNK` + `akd ` (3). Every WEM is Wwise Vorbis (format tag 0xFFFF). 0x03F: 1, 2 or 4 channels (4: 16
WEMs), 48,000 Hz (11,537), 44,100 Hz (154) or 22,050 Hz (12, block sizes 512 / 1024). 0x0B2: mono or stereo at 48 kHz.
Otherwise the block sizes are 256 / 2048.

| fmt offset | type | field |
|---|---|---|
| +0x00 | u16 | format 0xFFFF |
| +0x02 | u16 | channels |
| +0x04 | u32 | sample rate |
| +0x08 | u32 | average bytes / s (written as the Vorbis nominal bitrate × 1/8) |
| +0x10 | u16 | extra size 0x30 |
| +0x14 | u32 | channel mask |
| +0x18 | u32 | **samples** per channel (the decoded length) |
| +0x1C | u32 | "mod signal": 0xD9 / 0xCB / … in 0.8. The values 0x4A, 0x4B, 0x69, 0x70 would mean plain packets (ww2ogg's rule); none occurs in 0.8, so every WEM has the modified packets below |
| +0x28 | u32 | setup packet offset (in `data`) |
| +0x2C | u32 | first audio packet offset (in `data`) |
| +0x3C | u32 | uid (used as the Ogg serial number) |
| +0x40 | u8, u8 | log2 of the short / long block size |

`data` is a sequence of packets `{u16 size, bytes}`: the setup packet, then the audio packets.

### 5.3 Wwise Vorbis → Vorbis I  [D; G for the codebook library]

The packets are Vorbis I packets (https://xiph.org/vorbis/doc/Vorbis_I_spec.html) with everything redundant removed:

- **No identification or comment header.** Both are rebuilt from the fmt fields.
- **Setup packet** (the Vorbis setup header without its type byte and "vorbis" magic): codebook count − 1 (8 bits),
  then per codebook a **10-bit id into a codebook library** instead of the codebook. The time-domain section is missing
  (it has one legal value). Floor, residue and mapping types lose their 16-bit type fields (2 bits for the residue
  type; floors are always type 1, mappings type 0). Each mode's window type and transform type are missing (16 + 16
  bits). The rest is bit-identical to Vorbis I. In every WEM the setup packet is used to its last byte (a check of the
  converter).
- **Codebook library**: 598 codebooks, 71,991 bytes, in the executable's .rdata at RVA `0x142b7b0` (file offset
  `0x142a3b0` in the install's `GameClientApp.exe`, readable without decryption), with a table of 599 pointers at RVA
  `0x1717870` (the last pointer is the end) [G]. It is byte-identical to the "packed_codebooks_aoTuV_603.bin" that
  other Wwise tools ship [D] (sha256 `de31ec8d…2914b038`). A codebook starts on a byte boundary. Its size is
  floor(bits / 8) + 1: the packer always ends one with at least one pad bit [D: all 598]. The **packed codebook**
  form: dimensions 4 bits, entries 14 bits, ordered 1 bit.
  - Ordered: initial length 5 bits, then run counts of ilog(entries − current) bits (as in Vorbis).
  - Not ordered: codeword-length width 3 bits (1–5), sparse 1 bit, then per entry [present 1 bit] length (width
    bits).
  - Lookup type 1 bit. Type 1: minimum 32 bits, delta 32 bits, value bits − 1 4 bits, sequence 1 bit, then the
    multiplicands.

  Rebuilt, the codebook gets the 24-bit sync pattern, 16 / 24-bit dimensions / entries, 5-bit lengths and a 4-bit
  lookup type.
- **Audio packets** lack the packet-type bit (0). For a long block they also lack the previous / next window flags,
  which are recomputed from the previous packet's mode and the next packet's mode (its first mode bits).
- **Granule positions**: each packet after the first adds a quarter of the previous and a quarter of the current block
  size. The decoded length is cut to the fmt's sample count, which is shorter than the last granule. In an Ogg file the
  last page carries it (end trimming). The first audio packet gets a page of its own; otherwise a short sound's only
  audio page is both first and last, and decoders (ffmpeg) trim the wrong end.

**Tool chain** (all in `explorer/internal/audio`, Go, no third-party code, no external tools): `ParseWEM` → `Convert`
(the ww2ogg step: rebuild the three headers and the audio packets, with the codebook library read from the install's
executable) → `Vorbis.Ogg()` (an Ogg Vorbis file) and `Vorbis.Decode()` (a Vorbis I decoder: floor 1, residues 0–2,
mapping 0, FFT-based IMDCT) → WAV. The steps follow the public ww2ogg / revorb tools' understanding of the format;
the code is written from the Vorbis I specification and the data.

**Validation** [D, `cmd/audioscan -all -ffmpeg DIR -every 1`, `TestAllWEMs`]: all 18,607 WEMs (7.8 h of audio)
convert and decode, 12 s on 16 cores. Each decodes to exactly its sample count. Every rebuilt Ogg decodes in ffmpeg
(libvorbis-compatible) to the same samples (largest difference 5.4e-7, float rounding) and the same length. Failure
list: empty.

### 5.4 Banks (BKHD + HIRC)  [D: all 3,821]

Chunks `{char[4] id, u32 size, data}`: `BKHD` + `HIRC` (3,820 banks). One bank also has `STMG` and `ENVS`:
`0x0420000000002E1E` (the init bank: all 71 buses and 112 aux buses; no sound uses it). No bank has `DIDX` / `DATA` (media). BKHD: version
**112**, bank id (= the sound's `0x40ac0372`), language.

HIRC = `u32 count`, then objects `{u8 type, u32 size, u32 id, body}`. The objects fill the chunk exactly in every bank.
Counts by type: Sound (2) 52,670, actor-mixer (7) 14,871, random / sequence container (5) 6,738, Action (3) 5,658,
Event (4) 4,382, Attenuation (14) 4,358, blend container (9) 2,620, custom effect (19) 766. Fewer than 200 each: State
(1), switch container (6), bus (8), music segment / track / switch / playlist (10–13), effect (18), aux bus (20), LFO
(21), envelope (22).

What the explorer reads (the rest of each body is kept as bytes):
- **Sound** (2): plugin id u32, stream type u8, source id u32, file id u32 (0; for streamed sources the source id
  again), file offset u32 (only when not streamed: stream type ≠ 2), in-memory size u32, source bits u8. A source
  plugin (plugin type = low nibble 2) follows with u32 size + parameters. Plugins: 0x00040001 Vorbis (52,030; stream
  type 0, or 2 for 93), 0x00650002 silence (631), 0x00080001 (8, all in the voice bank, §5.5), 0x00940002 (1). The field
  meanings come from Wwise's names; the lengths are what makes every parent below resolve [?].
- **Audio node parent** (Sound after its source, containers 5 / 6 / 9, actor-mixer 7): effect override u8, effect count
  u8, [bypass bits u8 + count × 7 bytes], a u8 (0), output bus id u32, **parent id** u32. All 76,953 parents are 0
  (3,768 top-level actor-mixers and one blend container) or an object of the same bank.
- **Event** (4): u32 count, action ids. Every action is an Action object of the same bank.
- **Action** (3): action type u16, target id u32, is-bus u8. Play (0x0403) 4,714, stop (0x0102 / 0x0103) 727, others (0x0803, 0x1204, 0x1302, 0x1402, 0x1901, 0x1C03, 0x1E03, 0x1E05: 217).
  Most targets are containers or Sounds in the same bank; some are 0 or elsewhere.

**Sound → WEM** [D]: each Vorbis Sound object's source id is the id of one of its STUSound's WEMs (`m_wwiseWEMFileIDs` /
`m_wwiseWEMStreamIDs`). All 52,030 match (index kind `audio.source`, bank → WEM).

### 5.5 Voice lines

The 6,904 voice WEMs (0x0B2) are referenced by voice-line STUs (type 0x059, root `0x07a46d78`, field `0xfa1f65e6`)
next to a `STUVoiceWEMFile` GUID of type 0x057 that isn't in the install [D]. The voice lines' shared sound
`0x0D400000000016F3` (`STUVoiceLine.m_externalSound`, used 5,144 times) has a bank whose Sound objects use plugin
0x00080001. That is Wwise's codec id 8, *External Source* [?, Wwise's codec numbering]. So the voice WEM is most likely
supplied at play time as the external source.

### 5.6 Switch groups, switches, parameters, state groups  [D]

Tiny STU documents with one teString (the name) and one u32 (the Wwise id); a switch also references its group.
**Every named one's id is FNV-1 (32-bit) of its lower-cased name** (`WwiseID`), e.g. "WEAPON_TAIL" → 0xD237B686.
11 of 87 have empty (stripped) names. For example, switch group WEAPON_TAIL has switches INDOOR / OUTDOOR, and ONGROUND
has INAIR / ONGROUND. Parameters include MASTER_VOLUME, PVP_CAPTUREPOINT_PROGRESS, PAYLOAD_PROGRESS and GLOBAL_TIMESCALE.
State groups: INDOOR, PLAYER_HIT, PLAYER_SHOOTS, INGAME_MIX_STATE, MENU_STATE. Which bank uses them: the index kind
`audio.group` links a bank to every group / switch / parameter whose id occurs as a u32 in its HIRC object bodies
(5,895 edges). This is a byte match, because the node parameters that hold the ids (state groups, RTPCs, the switch
container's group) aren't parsed [?].

(The names only decode since T065: `systems/stu` read the teString header as `{hash, len}`; it is `{len, crc32}`,
docs/STU.md §1.)

### 5.7 Open questions
- The rest of NodeBaseParams (props, positioning, aux, state groups, RTPCs) and the container bodies (children,
  playlists, switch assignments) aren't parsed. The tree is recovered from the parent ids instead.
- Plugin 0x00940002 (1 Sound object) is unidentified. The 0x057 `STUVoiceWEMFile` GUIDs aren't in the install.
- How the game picks an event (`m_wwiseEventIDPlay`) and sets switches, states and RTPCs at run time (Statescript
  sound nodes) was not traced.
