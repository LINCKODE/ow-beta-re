# Rendering: how the 0.8 client produces a frame

How the Overwatch beta 0.8 client (build 24919) turns its scene into D3D11 work: threads and frame pacing, the render
world, visibility, draw items and passes, the frame's pass list (deferred G-buffer, lighting, shadows, GI, fog, post),
materials and shaders, effects, UI, and GPU resources.

Task T070 (2026-09-29). Addresses are RVAs like the other docs (Ghidra address = `0x7ff64ffe0000 + RVA`, so
`0x98d100` is `FUN_7ff65096d100`); `DAT_…` names are Ghidra's. Tags: **[G]** read in Ghidra, **[D]** read in the game
data offline (`systems/content`), **[L]** live, **[?]** inferred. Everything here is static ([G]/[D]); no live check
was run (§12 lists the ones worth doing). Where the code is too large to explain in full, the section gives the
structure and the entry points.

**Contents**: 1 summary · 2 threads and the frame · 3 the device · 4 the render world · 5 visibility and LOD ·
6 draw items, passes, sorting, instancing · 7 the frame's passes · 8 lighting and shadows · 9 materials and shaders ·
10 effects · 11 UI · 12 GPU resources · 13 entry points · 14 open questions.

Related: [EngineManagers.md](EngineManagers.md) (the render manager is slot 20), [ResourceLoading.md](ResourceLoading.md)
(handlers), [AssetFormats.md](AssetFormats.md) (textures, models, materials, looks), [Maps.md](Maps.md) §2.4 (the map
records the static world draws), [DataFlow.md](DataFlow.md) §5 (draw constants, effect parameters),
[UISystem.md](UISystem.md) (screens, view models).

---

## 1. Summary

```
 MAIN THREAD                                         RENDER THREAD                          GPU
 ───────────                                         ─────────────                          ───
 App_Frame                                           wakes on "frame submitted"
  pace: wait for a free render buffer (≤2)           ExecuteFrame(N)
  managers update (input pump, …)                     └ frame task graph (cull/prep jobs)
  game tick: EA systems, command frames,                └ FrameEndJob:
    teScene::Update → dirty nodes → render world           Present(N-1)  ───────────────────▶ scan-out
  BeginFrame: open render buffer N                         replay packets of N on the
  RenderSubmit: per view a 0x1040-byte view packet         immediate context  ───────────────▶ draws
    (3 cameras + pointers into the render world),          End(N): event query  ◀──────────── done
    then the UI canvas                                     buffer N free
  EndFrame: kick frame graph, wake render thread
```

- **Two threads + jobs** [G]: the main thread simulates and *records* render commands into one of **two render
  buffers**; a dedicated **render thread** replays them on the **D3D11 immediate context** and presents. The main
  thread can be at most two frames ahead. **Present is deferred by one frame** (frame N is presented at the start of
  frame N+1's replay). Culling and draw-list preparation run as jobs of a per-frame task graph.
- **D3D11.0 only** [G, D]: feature level 11_0 or 10_1, no deferred contexts or command lists, no D3D11.1 / DXGI 1.2.
  Swap chain R8G8B8A8_UNORM, DISCARD, 2 or 3 buffers.
- **The render world is persistent** [G]: components create render objects once; the scene update propagates dirty
  transforms into them. Per frame only a small **view packet** is snapshotted.
- **Visibility** [G]: no BVH or portals. Static map geometry sits in four flat size-class arrays culled by SIMD
  kernels on jobs into bitsets; a **256×128 CPU occlusion buffer** rasterises the map's occluder quads; small far
  objects are dropped by projected area; there are also HW occlusion queries. LOD is by distance with hysteresis.
- **Deferred shading** [G]: depth pre-pass bucket → **6-target G-buffer** (RGBA8 sRGB, RG16F, RGBA16F, RGBA16 UINT,
  R11G11B10F, R16F + D24S8) → deferred decals → HBAO+ → screen-space sun-shadow mask → deferred lighting (ambient,
  sun, **light volumes** for local lights, SSLR) into an **RGBA16F HDR** target → forward translucency → HDR→LDR →
  LDR post (FXAA-like [?]) → outlines and screen particles → upscale → colour-blind filter at present. UI (a vector
  canvas) is drawn last.
- **Global illumination: Enlighten 3.00** [G] (realtime radiosity, CPU solver on worker threads): lightmaps for static
  geometry, SH probe volumes for dynamic objects; per-map precompute in LIGHTINGDATA (0x00E). Plus reflection probes
  in an atlas, SSLR, HBAO+, froxel local fog.
- **Shaders are precompiled DXBC** [D]: 0x087 SHADER CODE = gzip'd DXBC with stripped reflection; 0x086 SHADER
  INSTANCE = one permutation's bindings; 0x085 SHADER GROUP. No shader source ships (0x088 is only a key).
- **Effects are CPU-simulated** [G]: 26 element kinds (emitters, particle renderers, ribbons, lights, sounds, child
  effects, …) updated as jobs with distance-based frame skipping; particle attributes reach the GPU through per-frame
  structured buffers (vertex pulling).

---

## 2. Threads and the frame

### 2.1 The main loop [G]
`App_Frame` `0x7d0260`, called in a loop by `App_Run` `0x7ced10`:
1. `g_frame` (`0x181d8c8`) += 1; **dormant** = window inactive and not in game and `app.IgnoreDormantMode` off.
2. Frame timer and **limiter** (`FrameLimiter_Wait` `0x7e7660`): if enabled, a QPC spin to 1000/fps ms
   (fixed fps, a static 60, or a dormant rate). Whether it is enabled by default was not established [?]; menus are
   known to run at hundreds of fps (CommandFrames.md), so probably not.
3. Render message 10, **PaceBeforeFrame** `0x938750`: waits for a free render buffer *before* input and the
   simulation, using the average BeginFrame wait of the last 15 frames minus 5 ms. The point is to take the
   unavoidable wait for the render thread before sampling input, not after it [? purpose; G mechanics].
4. **UpdateAllMgrs** `0x7def80`: every manager's per-frame update (vfunc +0x20); the input manager's message pump
   runs here [?]. Outside a game (engine state ≠ 5) the render manager draws loading/menu frames here (message 0xe).
5. In a game, a two-node task graph:
   - **GameTick** `0x7d0190` → `0xbff460`: the entity-admin ticks (ECS.md §6; command frames run inside;
     systems call `teScene::Update`);
   - then **RenderFrameTick** `0x7df170`: skipped when the window is inactive and less than 1/480 s has passed;
     **BeginFrame** (message 0xb, `0x938970`, waits up to 100 ms when `app.WaitForRenderFrame` = 1, the default);
     if no buffer frees up the frame is **not rendered** but the simulation has already advanced; **RenderSubmit**
     `Game_SubmitFrame` `0xbff7b0` (§4.3); every manager's vfunc +0x28; **EndFrame** (message 0xc,
     `0x938d80`).
6. Run one posted main-thread callback.

### 2.2 Render buffers: recorded command packets [G]
- `render+0xd20 + i·0x8e0`, **count 2** (a static default, `0x17a1140`; Start fails above 2). State 0 free,
  1 recording, 2 submitted, 3 executing. A semaphore counts the free buffers.
- A buffer is a paged linear allocator of **packets** `{callback, size, payload}`; `RenderBuffer_AllocPacket`
  `0x9826a0` is how every render-side operation is recorded, `RenderBuffer_Execute` `0x982920` replays
  them in order on the render thread. The debug overlay's "RB MEM" is the byte size of the last executed buffer.
- Each buffer owns a **task graph** (`RenderFrame_Init` `0x9ad890`, 7 nodes A–G); culling and pass-preparation
  jobs are attached to it while recording [? which node runs what]. EndFrame kicks it (`RenderFrame_Kick`
  `0x9adfc0`); its last node G = **FrameEndJob** `0x938fe0` runs on the render thread.

### 2.3 The render thread [G]
`RenderThreadMain` `0x938000`, created by the render manager's Start (`0x936190`; stack 64 KB):
- creates the DXGI factory, the device and the swap chain **on this thread** (`0x93ab20` →
  `teVidDevice::InitializeWindow` `0x9128c0`), then loops: wait for the wake semaphore (40 ms timeout) →
  `RenderThread_Tick` `0x938150`: posted functors (resize, stop), re-copy the display settings, a
  resource/streaming service call (`0x851a00`), and if a buffer is submitted **ExecuteFrame**
  `0x939310`: run the frame graph's final node inline, then free the buffer.
- **FrameEndJob** `0x938fe0`: (a) `teVidDevice::Present` `0x9142a0` for the **previous** frame;
  (b) replay this frame's packets; (c) `teVidDevice::End` `0x913f70`: an EVENT query per frame (ring of 16).

### 2.4 Input-lag breakdown [G]
The debug display "INPUT LAG: SUB RST RND GPU" shows, per input, milliseconds from the input to: **SUB** the main
thread submitted the frame that consumed it (EndFrame); **RST** the render thread started replaying it (first
packet `0x938360`); **RND** replay finished (`teVidDevice::End`); **GPU** the frame's event query was seen
complete. The tracker is manager slot 33 (it also runs the Windows message pump: the input/window manager [?]), a ring
of 120 records.

Latency chain [? assembled from the above]: input → sim(N) → record(N) → (≤ 2 buffers) → replay(N) → Present(N) at
the start of frame N+1's replay → DXGI queue (`MaxFrameLatency`) → scan-out.

---

## 3. The device [G unless tagged]

- **Device** (`teVidDevice::InitializeWindow` `0x9128c0`, call at `0x912a92`):
  `D3D11CreateDevice(adapter, D3D_DRIVER_TYPE_UNKNOWN, NULL, flags 0, {11_0, 10_1}, 2, SDK 7)`: not single-threaded,
  no debug layer. `IDXGIDevice1::SetMaximumFrameLatency(MaxFrameLatency)` when that setting is ≥ 0 (default not read).
  Below FL 11_0 the compute passes (HBAO+, compute fog, UAV targets) are skipped.
- **Immediate context only**: a scan of the whole binary finds no `CreateDeferredContext`, `FinishCommandList` or
  `ExecuteCommandList` call; the IIDs of ID3D11Device1 / Context1 / IDXGIFactory2 / IDXGISwapChain1 are not in the
  image [G, D]. Multithreading is done by recording engine-level packets on the main thread and jobs, not D3D command
  lists.
- **Swap chain** (`__CreateDeviceWindow` `0x91cb40`): R8G8B8A8_UNORM (not sRGB), usage shader-input +
  render-target, **2 buffers, or 3 with TripleBufferingEnabled**, windowed with ALLOW_MODE_SWITCH, **DISCARD** swap
  effect; an SRV and an RTV on the back buffer. Fullscreen goes through `SetFullscreenState` / `ResizeBuffers` later.
- **Present**: `Present(VerticalSyncEnabled ? 1 : 0, 0)`; `LimitTo30` busy-waits to 1/31 s after each present;
  E_OUTOFMEMORY / DEVICE_REMOVED / DEVICE_RESET → device-lost handling.
- **State cache**: the context wrapper (`teVidDevice+0x10`, 0x13e0 bytes) caches IA / blend / depth / raster state
  with dirty bits and commits just before a draw (`GfxContext_CommitState` `0x922d40`).
- The engine's format enum **is DXGI_FORMAT** (identity table) [G].

---

## 4. The render world vs the ECS

### 4.1 Persistent render objects [G]
- Components own render-side objects created once and updated in place: a model component's render instance
  (fill `0xa1deb0`, which also copies DataFlow draw constants, DataFlow.md §5.2), a light's render interfacer
  (comp 0x05), effect instances (§10). They hang off the entity's **scene node** (comp 0x01, +0x1a8 interface,
  +0x1b8 render flags; ECS_Components.md).
- The render world is `scene+0x3a8` (sub-systems at +0x1a8…+0x1c0); render objects are 0x160+ bytes with flags at
  +0x60, kind/LOD bits at +0x78, local bounds at +0x80/+0x90.
- **Static map geometry is not entities** (Maps.md §7): the static world `0xa24e60` turns map records 1–7 into
  render objects and cull entries (§5.1); baked map text is turned into a runtime model (§11.3).

### 4.2 What the scene update does [G]
`teScene::Update` `0x9f6770` → `teScene::Tick` `0x9f6150` runs, among others: **FlushDirtyNodes**
`0x9f4310` (the double-buffered dirty list from the comp 0x01 setters; parents before children, as jobs when
≥ 16 nodes; `dmWorld_Step` for cosmetic physics before it), the render-world update (`0xa44b80` on
`scene+0x3a8`), the sound listener, per-node jobs; it stamps `scene+0x410 = frame#` and calls render vfunc +0x4f8
(lighting / probe update, Enlighten [?]). **It does not submit draws**: it brings the persistent render world up to
date.

### 4.3 What is snapshotted per frame [G]
- `Game_SubmitFrame` `0xbff7b0`: ticks the UX runtime, then renders the view of the active entity admin by game
  state (5 in game → `0xce6da0`; 6 lobby → `0xc43e60`; 7/8 replay), then extra UI views, then **submits
  the UI canvas last** (`teCanvas_Submit(canvas, clear = !worldDrawn)`).
- `teView::Render` `0x97f3a0` → `Submit` `0x97f7c0` → **`SubmitScene`** `0x980310`: allocates a
  **0x1040-byte view packet** in the render buffer (callback `0x98b970`): three camera copies of 0x250 bytes
  (main, **cull**, **LOD**: separable, which is what `render.LockCull` uses), render flags (+0x1020), scene bounds,
  pointers into the render world, light/probe parameters, the frame number. A view is rendered only if its scene was
  ticked in the same frame.
- The per-frame dynamic data that the GPU reads comes from per-frame ring buffers filled during recording/replay:
  "Matrix Buffer" (a pool of 3×4 matrices per render buffer), "Model Dynamic Inst Data", "Model Inst VB",
  "DataFlow Buffer", the effect pools, the canvas buffers (§6.4, §10, §11) [G names; layouts partly ?].

---

## 5. Visibility and LOD

### 5.1 The static world [G `0xa24e60`]
- Per model-group instance (Maps.md §2.4 type 1): world AABB, size = its largest extent; **size class** 0 (< 8 m),
  1 (< 20 m), 2 (larger), 3 (record flag bit 3, "large"). Each instance gets a **0x40-byte cull entry**
  `{AABB min, max, …, u16 view mask 0xf, u16 cost, u16 group index, u8 class, → instance}` in its class list, plus
  SoA bounds (8 AABBs per 0xC0 bytes, for the SIMD kernel), an oriented box (occlusion test input) and a **u16 parent
  index** (visible only if the parent's bit is visible; 0xFFFE = always hidden).
- Type-2 models and type-7 map text get their own render-object list; type-3 **occluders** go to an occluder list
  (and a GPU copy for drawing them); type-4 reflection probes are kept by the static world (§8.3).
- **No BVH, octree or portals** were found: static culling is brute force over four flat arrays, 8 boxes per SIMD
  step, output = visibility bitsets [G; the absence of a tree is [?]: nothing else reads these lists].

### 5.2 Cull views [G]
A cull context (0x410 bytes; `CullContext_Init` `0x9a7310`, `SetFrustum` `0x9a75b0`) per view;
`SceneCuller_SetupViews` `0x9b3ce0` / `CullAll` `0x9b6350` create per frame:
- type 0 **main camera** (with software occlusion), types 1 and 2 (lighting-object views [? which]), types 3–5 up to 3
  and 6–8 up to 3 (flagged; the **sun shadow cascades**, dynamic and static [?]), type 9 **one per shadowed local
  light**. No reflection-probe view was seen [?]: probes are baked.
- Per view: set frustum → rasterise occluders (main view) → **cull** (`Cull` `0x9a8360`) → collect decal /
  projector volumes (main view) → emit draw items (§6) → queue the view for rendering.

### 5.3 Cull execution [G `0x9a8360`]
- **Jobs**: one job per static size class, one for single models, one "finalize" job per object category; submitted
  to the job system (inline without one).
- Static classes (`CullStaticClass` `0x9aa0d0`): a SIMD kernel (function pointer in the context) tests the SoA
  boxes against the frustum planes → bitset, ANDed with the entries' view masks. A shared **work budget** with a
  resume index lets some views spread static culling over several frames (**time-sliced culling**).
- **Dynamic objects** (`CullDynamic` `0x9a8df0` → `0x9a8cb0`): distance² from the camera and the projected
  size (`ProjectedSize` `0x9aaa70`, × a per-object LOD scale); culled when the size is ~0, or **beyond 50 m**
  (dist² > 2500) when below a per-object-class threshold (16-entry table, from 2.5e-5 to 0.25 of the screen) =
  **detail culling by screen area**. In the main view it stores the distance and size on the object for LOD / fade.
  Visible objects are appended to 5 category lists (0 models, 1, 2, 3, 4).

### 5.4 Occlusion [G]
- **Software occlusion buffer** (CPU): **256×128** for the main camera (512×512 for view type 1). Occluders = the map's
  type-3 records (oriented boxes flattened to quads, Maps.md §2.4), selected, sorted and rasterised on the CPU
  (`Occlusion_RasterizeOccluders` `0x9a7c30`, rasteriser around `0x9c0390`). Entries with a screen size
  ≥ 8 are box-tested (`Occlusion_TestBox` `0x981b30`); **temporal**: an entry visible last frame is re-tested
  only on 1 frame in 16 (by index). Cvars `render.occlusionEnable`, `render.occlusionDisplay` (0 off, 1 occluders,
  2 buffer, 3 occluded).
- **HW occlusion queries**: `teVidDevice::CreateOcclusionQuery` `0x91d000`; boxes drawn with queries from a
  record list after the deferred decals (`HWOcclusion_IssueQueries` `0x9299c0`); `render.UseHWOcclusion`
  (default 1), `render.occlusionQueryMinDist`. Which objects get queries (lights, flares, large objects) is open [?].

### 5.5 LOD [G `Model_SelectLOD` `0x8587d0`]
- Per object: forced LOD (bits 12–15 of +0x78) or automatic (mode 4).
- Automatic: the camera position in object space clamped to the object's local AABB → closest-point distance²; a
  per-model **LOD distance step** (model +0xd4), normalised for FOV/resolution (min 5.0), times the object's LOD scale;
  LOD = the largest i with `k·(i·step)² < dist²`, **k = 1.1 / 0.9 hysteresis** around the previous LOD.
- Global bias: +1 when `ModelLODPush` is set, +1 at the lowest `ModelQuality` [?]; clamped to the model's LOD count.
- Applied **per submesh**: a submesh carries a LOD bitmask byte (AssetFormats.md §2, "LODs"), a view mask and a
  part-visibility mask (the look's hidden parts).
- Static map instances use their size class instead; their per-instance LOD path (`0x9abb90` and neighbours)
  was not read.
- Effects have their own distance LOD (update every 1/2/4/8 frames, §10.2).

---

## 6. Draw items, passes, sorting, instancing

### 6.1 From a visible object to draw items [G]
`RenderView_EmitVisible` `0x9740c0` → by render-object kind (`EmitObject` `0x974660`); models:
`EmitModel` `0x9af210` → `EmitModelMesh` `0x9aede0` (LOD pick) → **batch** by model + LOD + look
(FNV-1a hash into 512 buckets, `0x9ae920`) → per submesh (LOD / view / part masks, material;
`0x975070`) → instanced (`0x9afbe0`) or single (`0x9af660`) → **draw item** (0xa0 bytes,
`DrawItem_Create` `0x9b0840`) → the first matching pass.

Draw item (0xa0): `+0x08` material binding, `+0x10` **pass mask**, `+0x14` **kind** (the draw function), `+0x15`
**category**, `+0x16` flags (instanced 0x40, non-instanced 2, depth-range override 1, …), `+0x18` material flags,
`+0x1c` **distance²**, `+0x20` view-space centre, `+0x30` radius, `+0x40` submesh, `+0x58` instance members,
`+0x98` first instance, `+0x9c` instance count.
- **category**: 1 opaque, 5 translucent (material flags & 0x3c), 4/5 mesh decals, 0 fog particles.
- **kind**: 0–2 models, 3 submesh flag 0x10 (proxy submeshes, AssetFormats.md §2.5), 4 sky, 5 particles, 6 decal
  box, 7 mesh decal, 8 ribbons, 10 material `0x0E10000000000033` [names of 4/5/6/8 inferred from the passes that
  accept them].
- **pass mask**: 1 normal (| 4 for an override submesh), 0x800 (object flag bit 17), **0x1000 "After Blur"** (object
  flag bit 18), 0x200 screen particles, 0x400 fog-density particles, outline bits 0x40–0x100 from per-object overrides.

### 6.2 Passes ("buckets") [G `RenderView_CreatePasses` `0x9725d0`]
Each view owns 20 pass objects run in **11 stages**. An item goes to the **first** pass in the chain for its
(category, kind) whose pass mask, required and excluded material bits match (registration order). Pass vtable:
Init, BeginFrame, Submit(item), PreExecute/sort, Execute(stage).

| stage | passes | pass mask | notes |
|---|---|---|---|
| 0 | Fog Density Particles | 0x400 | particles written into the froxel fog volume [?] |
| 1 | Depth Prepass (mask 8, requires material bit 1), **Standard Opaque**, Opaque With Ambient, WireFrame (0x8000) | 0x4801 | G-buffer fill |
| 2 | Opaque Particle, Background Opaque (special views), Opaque Ribbons | | G-buffer fill, second half |
| 3 | SkyModels, Transparent Decals, Opaque Decals | | pre-decal work |
| 4 | Opaque Decals | 0x805 | deferred decals into the G-buffer |
| 5 | Transparent Decals, SkyModels, Unsorted Translucency (0x10), **Translucency** | | forward, into the HDR target |
| 6–9 | After Blur Standard Opaque / Opaque Particle / Opaque Ribbons / Opaque Decals / Translucency | 0x1000 | §7.3 |
| 10 | **Outline**, **Screen Particles** (0x200) | | on the LDR image, after post |

### 6.3 Sorting [G]
- **Opaque passes do not sort by depth.** Items are grouped in a 3-level hash (128 buckets each, `0x9b13a0`)
  keyed by the material's shader ids (technique table `mat+0x40`), one root per material layer: state-sorted by
  construction (shader → material), front-to-back not attempted; the depth pre-pass and HW early-Z do that work [?].
- **Translucency** sorts back to front: introsort (`0x9a5100`) on distance² descending, then a flag, then the
  submission sequence. Items with material bit 0x10 go to a separate list that triggers the refraction scene copy [?].

### 6.4 Instancing [G]
- `render.UseInstancing` (default 1). An object batch is instanceable when not skinned, not fading, with pass mask 1
  or 0x4000 and no per-object override.
- Instances of the same submesh key (1024-bucket hash at view+0x1218) become **one draw item** with an instance count.
  `RenderView_FillInstanceBuffers` `0x975a20` writes **8 bytes per instance** into the per-frame **"Model Inst
  VB"** ring: `u32` index into **"Model Dynamic Inst Data"** (the per-object transforms and constants store [?]),
  `u16` constant index, `u16` extra. The draw binds it as vertex stream 2 (stride 8) and calls
  `DrawIndexedInstanced`. So an instance is an **index record, not a matrix**; the vertex shader fetches the world
  matrix (`MtxInstWS`, semantic `INSTANCEDATA` [? shader side]) from the dynamic instance data.
- Map model groups use the same path through the static world's render objects.

### 6.5 Submission [G]
Execute per item: bind shaders and material (`GfxContext_BindMaterial` `0x991050`), resolve blend / depth /
raster state from the material's blend mode (`mat+0x5c`, 1–9; `0x991310`), optional depth-range override,
then the draw function by kind (table `0x146ba50`): mesh `0x9798f0` (`DrawIndexed` /
`DrawIndexedInstanced`), particles `0x979f90`, decal box `0x979e50` (`DrawIndexedInstanced(36, n)`: a unit
cube per decal), mesh decal `0x979c20`, ribbons/grids `0x97a380`. State is committed lazily
(`0x922d40`).

---

## 7. The frame's passes

### 7.1 Screen targets [G `Renderer_CreateScreenTargets` `0x8d7540`]
At render resolution (window × `RenderScale`, or `EffectiveWidth/Height` with `UseGPUScale`):

| slot | format | role |
|---|---|---|
| G-buffer RT0 | R8G8B8A8_UNORM_SRGB | `GBUF_RT0`: albedo [?] |
| RT1 | R16G16_FLOAT | `GBUF_RT1`: encoded normal [?] |
| RT2 | R16G16B16A16_FLOAT | `GBUF_RT2` [? contents] |
| RT3 | R16G16B16A16_UINT | `GBUF_RT3`: packed material data / ids [?]; read by the shadow composite and the skin blur |
| RT4 | R11G11B10_FLOAT | 5th MRT: emissive + baked/indirect lighting [?] ("Opaque With Ambient") |
| RT5 | R16_FLOAT | 6th MRT [? linear depth / velocity / AO] |
| depth | D24_UNORM_S8_UINT | scene depth + stencil |
| HDR | R16G16B16A16_FLOAT | scene colour; translucency drawn forward into it |
| extra | R11G11B10F ×2, RGBA16F | pre-decal copies, refraction / blur sources |
| half-res | D24S8, R32F depth copy, RGBA16F (+ UAVs on FL11: R16G16F ×2, R16F, R32F) | HBAO+ chain, half-res lighting |
| froxel fog | RGBA16F Texture2DArray, 64 slices + 2 RGBA16F | local fog volume (§8.5) |

**Correction (T071, material shader bytecode)**
[D]: the material G-buffer fill writes **4 targets**:
- RT0 = albedo + the spec/metal scalar in alpha;
- RT1 (RG16F) = emissive intensity, AO;
- RT2 (RGBA16F) = the spheremap-encoded normal, view depth and roughness;
- RT3 (RGBA16_UINT) = flags and a lightmap UV or SSS colour.

The lighting shaders read them that way. The roles of RT4 / RT5 above remain [?].

RT0–RT3 are bound as global textures 9–12 (`Renderer_BindGBufferTextures` `0x8d63b0`). All targets have one
sample: **no MSAA** [G for the targets seen].

### 7.2 Pass list of one view [G `Renderer_RenderView` `0x98d100`]
Before it, `Renderer_RenderFrame` `0x98bc90` renders the view's **sub-views**: shadow maps (depth-only,
`0x98bd90`: stages 1, 2, 5) and coloured (translucent) shadow maps (`0x98ca50`), then shadow-map
post-processing (ESM conversion / blur [?], `0x8d82e0`).

| # | pass | entry | notes |
|---|---|---|---|
| 1 | view constants; bind G-buffer as textures 9–12 | `0x9394e0`, `0x8d63b0` | |
| 2 | fog prep | `0x8dc260` | FogEnabled, ComputeFogEnabled |
| 3 | **stage 0** fog-density particles | `Scene_DrawPhase` `0x975b80` | into the froxel volume [?] |
| 4 | clear depth/stencil and targets | `0x98cdc0` | |
| 5 | bind 6 MRT + depth; **stage 1**: depth pre-pass bucket, then opaque G-buffer fill; a full-screen constant pass | `0x975b80`, `0x8e2ab0` | the map's occluder quads are also drawn at stage 1 start (§5.4) |
| 6 | depth downsample to half res | `0x8d3630` | |
| 7 | **HBAO+** (SSAOEnabled; FL ≥ 11; compute at half res, bilateral blur) | `0x8e7f00` | `SSAO.HBAO_*` cvars (NVIDIA HBAO+ parameters) |
| 8 | **stage 2** (opaque particles, ribbons, background), **stage 3** (sky/decal prep) | | into the G-buffer |
| 9 | pre-decal G-buffer copy | `0x8e2590` | decals read the copy |
| 10 | **stage 4 deferred decals** | | G-buffer modified in place |
| 11 | full-screen pass over the copy; HW occlusion queries | `0x8e29a0`, `0x9299c0` | |
| 12 | **sun shadow mask**: stencil marking, then a screen-space shadow composite into RGBA8 from depth, RT2, RT3 and the cascades | `0x8da3a0`, `0x8da870` | §8.2 |
| 13 | **deferred lighting** → HDR | `Lighting_RenderDeferred` `0x8d3950` | §8.1 |
| 14 | **stage 5** forward translucency, sky, transparent decals into HDR | `0x976920`, `0x975b80` | back-to-front |
| 15 | skin subsurface blur (2-pass separable filter using RT3) | `0x8d4750` | [? "SKIN_SCATTERING"] |
| 16 | After-Blur path (special views only, §7.3) | `0x8d4a20` | |
| 17 | **HDR → LDR** through the screen-effect helper | `0x98e250` → `0x8f5a90` | where tonemap/exposure/bloom sit: [?] |
| 18 | LDR post, 2 passes (LDR ↔ HDR target) | `0x8d5bf0` | **FXAA-like AA** [?] |
| 19 | **stage 10**: outlines composited onto the LDR image, screen particles | `0x9b1cc0` | §7.4 |
| 20 | upscale to the window when render scale ≠ 100 % | `0x94aff0` | |
| 21 | (later) **Present**: colour-blind filter pass (LUT per mode) or plain copy to the back buffer | `0x9142a0` → `0x937d80` | |

UI is recorded after all views (§11) and so drawn on top of the LDR image.

### 7.3 "After Blur" [G]
When a view has its own render target (off-screen / UI 3D views: hero gallery, menus [? which]) and `GFXPresetLevel`
> 1, the scene is drawn, the HDR image is blurred (`PostFx_BackgroundBlur` `0x8d4a20`, or the helper's blur
mode), then stages 6–9 draw the objects flagged "after blur" (object flag bit 18 → pass mask 0x1000) **sharp on top of
the blurred backdrop**, with their own lighting pass. With a low preset they are drawn inline with their normal stages.

### 7.4 Outlines [G `0x9b1cc0`, `0x9b1f60`]
The Outline pass keeps **4 outline groups**. For each non-empty group: allocate and clear a temporary full-view
RGBA8 target, draw the group's objects into it depth-tested against the scene (stencil ref −5), with a per-object
colour, then composite onto the LDR image with the screen-effect helper's **outline mode** (6; edge detect / dilate
[?]). This is after post-processing. Cvars `camera.combatOutlines`, `camera.uxOutlines`; setting OutlineDetail.

### 7.5 Screen-effect helper [G]
Manager **slot 29** (object `0x189e020`), vfunc +0x88 `ScreenEffect_Apply(mode, src, dst, aux, depth)`
`0x8f5a90`: 9 full-screen modes (0 copy, 2 blur, 6 outline composite, others not identified).

### 7.6 First-person viewmodel [G strings; ?]
`camera.FirstPersonNearZ` ("near clipping plane override for objects rendered in first person mode") and the
draw item's depth-range override flag (§6.5) suggest the viewmodel gets its own projection / depth range; no separate
viewmodel pass or depth clear was found in RenderView [?].

### 7.7 Settings → renderer [G]
`ApplyGraphicsSettings` `0x85be20` runs the GPU autodetect (vendor, name heuristics → preset) and copies each
setting (object: name at +0x10, value at +0x38) into renderer globals: DirectionalShadowDetail, SimpleDirectionalShadows,
TranslucentShadowDetail, LocalFogDetail, SinglePointAmbient, DynamicAmbient, TextureDetail, EffectsQuality,
ModelQuality, …; ComputeFogEnabled is forced on for non-Intel GPUs at FL ≥ 11. Gamma / brightness / contrast /
colour-blind mode are copied into the render manager each render-thread tick; the UI applies brightness/contrast to
element colours on the CPU, the 3D scene through a `BRIGHTNESS_CONTRAST_GAMMA` constant [? string].

---

## 8. Lighting and shadows

### 8.1 Deferred lighting [G structure `0x8d3950`; ? sub-pass roles]
- A half-res pre-pass using RT1 (`0x8e39c0`); the **main pass** `0x8e3ef0`: G-buffer + half-res AO as
  textures; light setup `0x8db300`; **full-screen ambient / indirect** (`0x8e5270`, many shader variants,
  also a half-res variant); **sun** (`0x8e59c0`); **local lights** (`0x8e6b30`).
- **Local lights = light volumes** [G for the binning, ? for the volume draw]: `Lighting_SetupLocalLight`
  `0x8e0a00` computes each light's volume, whether the camera is inside it, shadowed / translucent-shadowed, the
  dynamic shadow distance, and appends a 0x160-byte record into one of **16 bins** (light type × shadowed × inside /
  outside). No tiled or clustered light-culling pass exists. A `FORWARD_LIGHT_BUFFER` / `LIGHT_COUNTS` serves
  forward (translucent) shading [strings].
- **SSLR** (screen-space local reflections, `SSLRDetailLevel` 1 = half res … 4; `0x8e9290`), then a final
  composite (`0x8eb190`) and a bilateral upsample of the half-res result.
- Map lights come from map record type 9 (point / spot, a detail level) as inline `{01 05}` entities (Maps.md §2.4);
  comp 0x05 installs a render interfacer on the scene node; the path from there (renderer vfunc +0x510 on teardown)
  to the light records was not traced [?].

### 8.2 Shadows [G]
- **Sun**: cascaded shadow maps (`SunShadow_Configure` `0x9bc540`, `CreateTargets` `0x9bc600`):
  1–3 cascades at 1024 or 2048 depending on `DirectionalShadowDetail` (`SimpleDirectionalShadows` → none), in up to 3
  target groups (D32 or D24S8) — the dynamic set and a static set [?]; a **static shadow map, 2048, R32** plus an R16F
  companion (ESM [?]); a translucent sun shadow (R11G11B10F) when `TranslucentShadowDetail` > 0.
  `ShadowMaxStaticDist` / `ShadowMaxDynamicDist` split static and dynamic casters by distance. The sun shadow reaches
  lighting as a **screen-space shadow mask** (§7.2 #12).
- **Local lights**: a **shadow atlas** as a Texture2DArray of D32 slices, slice size 256 / 512 / 1024
  (`DirectionalShadowDetail`), in 3 tiers of slots; with `TranslucentShadowDetail` 2–4 an RGBA8 sRGB translucent-shadow
  array and moment/ESM arrays [?] (`0x9be6f0`). Strings `SHADOW_ATLAS`, `LOCALSHADOW_ARRAY`, `FRUSTUM_COOKIES`
  (projected light textures).

### 8.3 Global illumination and reflections
- **Enlighten 3.00** (Geomerics; SDK path strings, `teEnlighten*` wrappers) [G]: the realtime-radiosity CPU solver runs
  on worker threads ("Enlighten Solvers", "Enlighten Input Lighting", a 2.5 ms time limiter). Outputs: **lightmaps**
  for static geometry (`LIGHTMAP_TEX`, `LIGHTMAPLIGHTDIR_TEX` directional irradiance, UV transform / system index) and
  **SH L1 probe volumes** for dynamic objects (`PROBEVOLUME_R/G/B`, "ProbeLitSystemVolumeTexture"); baked AO
  (`BAKEDLIGHTMAP_AO_TEX`). Two GPU "Irradiance Buffer"s are created at start [G strings; wiring ?].
- **LIGHTINGDATA 0x00E** [D]: 19 resources (one per map, 1.4 GB total, e.g. Anubis 85 MB in its own package), magic
  `EEDC`, then 0x40-byte records keyed by 16-byte GUIDs (Enlighten system ids [?]): the per-map Enlighten precompute.
- **Reflection probes**: map record type 4 (1,678 in the install; each with two textures, Maps.md §2.4) →
  `AREA_REFLECTION_ATLAS_TEX`, `REFLECTION_TRANSFORMS/SIZE/FALLOFF` (box-projected cubemaps in an atlas [?]),
  `ENVIRONMENT_BRDF` (split-sum image-based lighting [?]); `LocalReflections` setting; plus SSLR.
- `DynamicAmbient` / `SinglePointAmbient` select the ambient model for dynamic objects [G mapping; effect ?].

### 8.4 Ambient occlusion [G]
HBAO+ at half resolution (compute, FL ≥ 11), bilateral blur, fed to the lighting's ambient term; cvars
`SSAO.HBAO_Radius`, `…_Strength`, `…_PowerExponent`, `…_BlurRadius`, … (the HBAO+ SDK parameter set).

### 8.5 Fog
Global height / distance fog (`FOG_DATA`, `FOGCOLOR`, `FOGDISTRIB`, `FOGCUBE`; `Render.FogMin/FogMax`) and **local fog
volumes** in a **froxel grid** (`LocalFogDetail` 1–4 → 32/64/128/256 cells on the long axis, 64 depth slices,
RGBA16F), filled by compute (`ComputeFogEnabled`) and by "Fog Density Particles" [G targets, ? exact use].

---

## 9. Materials and shaders

### 9.1 The chain: material → group → instance → code [G, D]
```
material 0x008 ── +0x00 shader source GUID 0x0E1…(type 0x088: not shipped, only a key)
   │               +0x28 switches: (u32 name hash, u32 value hash) pairs   [count u16 +0x5e]
   │               +0x30 fragments: (u32 hash, u32 0, u64 0x0E1… GUID)       [count u16 +0x64]
   ▼  CRC32 over (source, switches, fragments)   ShaderGroup_GuidFromKey 0xf3dfa0
0x085 SHADER GROUP  GUID 0x0210000000000000 | crc    N × (instance id, permutation key) + hash → engine-slot records
   ▼
0x086 SHADER INSTANCE  gzip'd reflection: 5 CB slots (b0–b4) + defaults, CB layouts, textures, samplers
   ▼
0x087 SHADER CODE  gzip'd DXBC, one stage  →  teVidDevice::CreateShader (deduplicated by code id)
```
- **No shader source ships** [D]: the install has 3,296 groups (0x085), 12,032 instances (0x086), 9,725 code blobs
  (0x087), one 4-byte group cache (0x00F) and **no 0x088**. A material's shader "GUID" is a key: the group GUID is a raw
  CRC32 of the source GUID, the material's switch list and its fragment list. Recomputed offline for **all 11,150
  materials: every one hits an existing group** (3,238 distinct), and the remaining **58 groups** are `crc(source)`
  alone, the engine's own shaders (sources 0x01–0x0F, 0x10, 0x19–0x1B, 0x1E–0x38, 0x3D–0x3F, 0x41, 0x42, 0x44, 0x45,
  0x51, 0x66, 0x8D, 0x8E, 0xA2): 3,238 + 58 = 3,296 [D, `matgrp.py` in the T070 report].
- The 0x088 handler is a **hot-reload channel** [G]: loading a source flags every runtime group made from it and
  requeues it under a `0x02101…` GUID (compiled from source; none shipped). `%s\Cache\Shaders\` is a disk cache whose
  user was not traced [?].
- Handlers (all registered by the render manager's Register, `0x935ce0`): SHADER SOURCE 0x088, SHADER GROUP
  0x085 (acquires its instances as dependencies), SHADER INSTANCE 0x086 (acquires its code), SHADER CODE 0x087,
  SHADER GROUP CACHE 0x00F. Child GUID type bits come from a 64-bit bit reverse of the type; the low 32 bits are the ids
  stored in the parent [G].
- Groups are built on the shader-system tick (`0x92e1b0`) with a time budget (≤ 30 per call) [G]:
  decompress the instance, create its constant buffers and samplers, find or create the D3D shader
  (`CreateVertexShader` / `CreatePixelShader` / `CreateComputeShader` / `CreateGeometryShader[WithStreamOutput]`).

### 9.2 Formats [D all resources; G handlers]
- **0x087 SHADER CODE**: header 0x28 {+0x00 → gzip stream, +0x08 → stream-out declaration, +0x10 code id, +0x14 SO
  count, +0x18 SO stride, +0x1c uncompressed size, +0x20 compressed size (0 = raw), +0x24 stage flags (VS 0x01/0x80,
  PS 0x02, GS 0x04, GS+SO 0x10, CS 0x100)}. The payload is **DXBC with three chunks, ISGN / OSGN / SHDR (SHEX for CS)**:
  the reflection (RDEF) and statistics are stripped; bindings come from the instance. Compiler: "Microsoft (R) HLSL
  Shader Compiler 6.3.9600.16384".
- Stages: **6,972 ps_4_1, 2,704 vs_4_1, 38 cs_5_0, 11 gs_4_0** (one with stream-out). **Draw shaders target shader
  model 4.1 (feature level 10.1)**; compute (SM 5.0) is optional and used by HBAO+ / fog on FL 11.
- **0x086 SHADER INSTANCE** (gzip'd): 5 CB slots b0–b4 {variables (size, offset, name hash), default data, size}:
  b1/b2 are always empty in the instance (engine-bound buffers), b3 is 48 bytes / 10 variables in 6,532 instances,
  **b4 = the material CB** (16–176 bytes); 22 distinct CB layouts (e.g. hashes `0xB564F9D1` 252 bytes and
  `0x1DD3BC0A` 100 bytes in 8,618 instances: engine per-frame / per-object blocks [?]); **texture records** {default
  texture, slot hash, dimension, t-register, engine texture id or 0xFFFF = from the material}; sampler records (engine
  sampler id or an inline desc); a stage mask and a CB register mask.
- **0x085 SHADER GROUP**: N instance ids + N **permutation keys**, M records mapping hashes to engine input slots, a
  feature/pass mask [?].

### 9.3 Permutations [D; ? for names]
- Two levels: **static** per material (the group: switches and generated material-function fragments, 113 distinct
  switch pairs, 5–17 per material) and **dynamic** per group (instances keyed by a u32 permutation key).
- Typical groups: 9 instances (1,987 groups: 2 VS + 7 PS), 24/25 (699 groups: 12 VS + 12–13 PS, the character
  shaders), 22 (8 VS + 14 PS), up to 66. 369 distinct keys, each always the same stage and recurring across shaders
  (e.g. VS `0x02B5689B` in 3,209 groups, PS `0x71AF1111` in 3,169): the keys are **named passes × vertex variants**
  (depth-only, G-buffer, forward, shadow, outline, …; static / skinned / instanced) [? names not recovered: no exe
  string CRCs to them].
- What picks the key at draw time was not traced; the draw item's pass and the technique table `mat+0x40` (pass type,
  3 shader indices) are the path [?].
- A quality level 0–2 (TextureDetail) is part of the material render-object key but not of the group key: it changes
  textures, not shaders [G].

### 9.4 Binding a material [G `Material_GetRenderObject` `0x977c50`, `Material_BuildInstanceBindings` `0x9785b0`]
- A **material render object** (0x80 bytes) per (material, source, quality): it gets its group through render vfunc
  +0x2b0 (`GetOrCreateShaderGroup`), keeps the material flags (+0x68 bit 1 = **helper material** → flag 0x400, so the
  renderer can drop "Player Only" helper geometry [? reader not traced], AssetFormats.md §3.5) and a **shader category**
  from the source id (0x1F/0x20 → 1, 0x21/0x23 → 2, 0x2C → 5, 0x24/0x25 → 6, 0x2B/0x33/0x34 → 7, 0x2F/0x35 → 8,
  else 3).
- **Textures** are matched **by slot hash** (CRC32 of the slot name, AssetFormats.md §3.3) against each instance's
  material-supplied texture records; unmatched records keep their default; a global override table can replace any.
  37,382 of 51,792 material texture bindings are read by some shipped permutation; the rest are not read at all [D].
- **Texture detail**: a binding with flags 3 swaps in the low-resolution variant GUID `0x0C00000F_xxxxxxxx` at detail
  level 0 (8,066 such variants exist) [G, D]. That is the texture-quality mechanism, not mip streaming.
- **Parameters**: the material's parameter stream is written into a copy of the instance's b4 defaults by variable
  hash → one **immutable** D3D11 buffer per material instance; the material's own CB records (AssetFormats.md §3.3,
  mostly PS b4) likewise.
- **Engine buffers** (render start `0x9397f0`): "Matrix Buffer" (3×4 matrices), "DataFlow Buffer" (16-byte
  values: the per-draw DataFlow constants of DataFlow.md §5.2), "Irradiance Buffer" ×2, "Model Inst VB", "Model
  Dynamic Inst Data" (128-byte per-object entries), each a double-buffered CPU ring per frame; how they reach the
  shaders (CB b1/b2 vs SRV) was not traced [?].

### 9.5 The material shaders in use [D counts; purposes ?]
24 of the 27 allowed sources occur. By material count and who references the materials (model looks 0x01A, effect
looks 0x0A8, effects 0x00D/0x04A/0x08E/0x08F):

| source | materials | used by | purpose |
|---|---|---|---|
| 0x21 | 1,801 | model looks (world, props, entities) | **standard opaque PBR** (basecolor, normal, packed masks); also the "Player Only" helpers |
| 0x2C | 2,398 | model looks (2,813 entity refs), 12 VS permutations | **hero / character PBR** |
| 0x28 | 2,502 | **effects only** | **effect / particle shader** (half-float and BC4 textures). *Correction*: AssetFormats.md §3.4 called it the environment shader |
| 0x32 | 882 | model looks | entity / weapon material with generated function fragments [?] |
| 0x22 | 677 | model looks | **decal** (colour 0xEDCD5483, opacity, normal) |
| 0x2A | 494 | effect looks (0x0A8), effects | overlay / screen effect look [?] |
| 0x26 | 468 | model looks | layered / detail material [?] |
| 0x33 | 418 | hero looks | skin / cloth-type hero material [?] |
| 0x24, 0x25 | 316, 230 | map looks | multi-layer world blend (terrain-like; 8–10 textures on 0x25) [?] |
| 0x2B | 303 | model looks | character variant [?] |
| 0x30, 0x29 | 139, 53 | effects | effect variants (distortion? no-texture) [?] |
| 0x36 | 134 | model looks | glass / water / refractive [?] |
| 0x37 | 89 | map looks | emissive world [?] |
| 0x2F, 0x27, 0x34, 0x35, 0x2E, 0x23, 0x38, 0x65, 0x2D | ≤ 69 each | | special cases (0x35 alpha-tested hair/foliage [?], 0x23 helper, 0x38 unlit colour) |
| 0x20, 0x31, 0x41 | 0 | | allowed, unused (0x20 is requested by the engine at start) |

---

## 10. Effects

### 10.1 Resources and element kinds [G, D]
- The EFFECT handler (`0x9d86b0`) loads types **0x00D, 0x08E, 0x08F, 0x04A**. An effect resource is an element
  list (`kind | index << 8` at +0x10), one array per kind, the DataFlow parameter table (DataFlow.md §5.1), duration
  (+0x130) and flags (+0x13c).
- Runtime **element-kind table** at `0x1830d20`: 26 kinds × 0x98 bytes of callbacks (start, update(dt), stop,
  create state, **render gather**, load, unload). The editor names in the binary (`STUEffectTrackParticleSystem`,
  `…TrackRibbon`, `…TrackDecal`, `…TrackExplosion`, `STUEffectShader_Base`, …) are tool-side and do not map one to one.

| kind | what [D counts over 11,819 effect files; G callbacks] |
|---|---|
| 1 | **particle emitter** (CPU simulation, 0x3f0-byte state per instance) |
| 2 | **particle renderer**: a model (00C, the sprite / mesh) + a material, paired with 1 |
| 4 | data companion of 1/2 [?] |
| 3 | animated model instance (model + look + animation) |
| 5 / 19 | **sound** (in 7,625 of the 8,264 0x00D files: most 0x00D "effects" are animation sound events) |
| 6 | **child effect** |
| 7 | **material overlay** on the owner's model (status looks, common in 0x04A) |
| 8 | **ribbon / beam** between two attach points (model + material + 2 sockets) [?] |
| 9 | **light** (optional projected texture) [?] |
| 10 | DataFlow output (DataFlow.md §4) |
| 17 | spawn entity; 18 STUMaterialEffect; 20 play animation [?]; 22 voice stimulus |
| 11–16, 21, 23–25 | attachment helpers and rare kinds [?]; the decal track is one of 11/16 [?] |

### 10.2 Simulation [G]
- Effects live in a **scene** (the world scene or a UI effects scene) and are created by `teScene::AddEffect`
  `0x9f33f0`. Per instance: local time, an element state machine (start / update / stop time windows,
  `0x9edb10`), DataFlow parameters evaluated on each element update.
- **CPU, on jobs, on the game tick**: the scene update walks 16 buckets of instances and runs them as thread jobs
  (`EffectScene_UpdateAll` `0x9d8dd0`, worker `0x9d92c0`). **Distance LOD skips frames**: an instance
  updates every 1, 2, 4 or 8 frames depending on the camera distance chosen in the previous render visit
  (`0x8fe060`).
- **No GPU simulation** was found: stream-out geometry shaders exist only as a generic shader-creation option.

### 10.3 Rendering [G]
- The render visit (`0x8fe060`) calls each render element's **gather** callback, which builds a 0x60-byte draw
  item with a pass mask (1, 0x200 screen, 0x400 fog density, 0x1000 after blur) and a distance² sort key, and writes
  the per-particle attributes (`EffectElement_WriteAttributes` `0x8c1a60`: sprite alignment modes × quad / mesh
  [? names]).
- Attributes go to three per-frame, double-buffered pools, **"Effect Instance"**, **"Element Attribute"**, **"Element
  Index"**, uploaded each frame into dynamic GPU buffers and read by the vertex shaders (**vertex pulling** with
  instancing) (`EffectRender_InitBuffers` `0x8fe5e0`, upload in the frame setup `0x938360`).
- Passes: one particle pass class serves Opaque / After Blur Particle, Opaque / After Blur Ribbons and Fog Density
  Particles; **opaque particles go into the G-buffer** (stage 2); translucent effects go through Translucency /
  Unsorted Translucency; Screen Particles draw after post.
- **Decals**: *mesh decals* are decal submeshes in models (decal shader `0x0E10000000000022`, AssetFormats.md §3.4) routed
  to the decal passes; *projected decals* (impacts into comp 0x57, sprays, effect decal tracks) are drawn as **unit
  cubes** (kind 6) into the G-buffer (Opaque Decals, merged by material and instanced) or forward (Transparent
  Decals, which bind a copy of the target at texture slot 11: screen-space projection from depth [?]).

---

## 11. UI

### 11.1 teCanvas [G]
- The UX tree (UISystem.md) is drawn by **teCanvas, a vector canvas**: filled and stroked paths are **tessellated to
  triangles on the CPU** and drawn as **instanced indexed draws** that pull per-element data from an instance-data
  buffer. Buffers "teCanvas vertex buffer" (16-byte vertices), "instance ID buffer", "instance data buffer", "index
  buffer", double-buffered and uploaded per frame (`0xa6d680`, `0xa69a60`). Cvars `canvas.FillsEnabled`,
  `StrokesEnabled`, `ClipGeoEnabled` (clip masks), `CompositingEnabled` / `CompositeMargin` / `CompositePadding`
  (layers rendered into **composite render targets**), `SplitInstances`, `CullingEnabled`.
- Submission `teCanvas_Submit` `0xa697a0` records render-buffer packets: begin, then one per layer; a layer
  replays batches (draw geometry, composite a sub-layer, clip masks). Viewport = the full back buffer.
- **UI is drawn last**, after post-processing, and clears the target itself when no world view was drawn
  (`Game_SubmitFrame` `0xbff7b0`) [G]. Whether it writes to an sRGB view is unknown [?].

### 11.2 3D inside UI [G]
The `STUUXTankEffect` element (`0xb0c640`) creates an engine effect in a dedicated **UI effects scene** chosen by
its DrawMode; hero-gallery and menu 3D views are separate views with their own targets (the After-Blur path, §7.3).

### 11.3 Text [G partly]
Fonts (0x050) are TrueType; FreeType is linked. **Baked map text** (`0xa48120`) lays out the string, **builds a
mesh from the glyph outlines** and registers it as a runtime model (GUID type 0x00C via the runtime-resource path,
UISystem.md §4). No glyph-atlas or SDF strings exist; whether UI TextBlocks tessellate glyphs into canvas fills the same
way is likely but not traced [?].

### 11.4 In-world UI and debug drawing
- No separate world-space UI renderer was found; world icons are either view-model driven canvas elements projected
  per frame or effects [?].
- **PrimDraw** (immediate-mode debug primitives: render-manager vfuncs +0x448…+0x498, common add `0x94f2e0`,
  material `0x0E1000000000001E`, "PrimDraw Instance" / "PrimDraw Inst VB", flushed in the frame setup) serves debug
  displays [?].

---

## 12. GPU resources

- **Creation** [G]: teVidDevice wraps every D3D11 create (textures 1D/2D/3D/cube, render targets 2D/2D-array/3D/cube,
  depth targets, views, vertex formats, shaders, queries); every call's HRESULT goes to `HandleDeviceLost`
  `0x9149b0` (E_OUTOFMEMORY / DEVICE_REMOVED / DEVICE_RESET → device-lost state, a "renderDeviceLost" event).
  The device is free-threaded; resource creation happens from loader threads while the render thread owns the
  immediate context [? threads not measured].
- **Textures are not streamed by mip** [G]: the TXTR handler requests **all** payloads (0x04D) of a texture as load
  dependencies (AssetFormats.md §1); quality is chosen by texture **variant GUID** (§9.4). Uploads happen in the render
  thread's service call [?]. The resource manager's cache-age unloading (ResourceLoading.md) is the only eviction
  found; no explicit GPU memory budget [?].
- **Per-frame data** lives in double-buffered CPU rings (engine buffers §9.4, effect pools §10.3, canvas buffers
  §11.1, PrimDraw) uploaded with `Map(WRITE_DISCARD)` into dynamic buffers each frame [G for effects/canvas].
- **Screen targets** are recreated on resize (`CreateScreenTargets` `0x8d7540`, §7.1); shadow maps, the shadow
  atlas and the fog volume by their settings (§8).
- Debug displays [G]: "TEX MEM Sys/Dvc" (texture bytes in system memory / on the device), "GPU USAGE Tex/Buf", "GPU
  ALLOC RT/Buf" (buffer count and bytes from the device), "RB MEM" (render-buffer bytes), "JOBS Std/Low" (two job
  queues), "INPUT LAG" (§2.4); `render.LockCull` freezes the cull camera ("CULLING CAMERA LOCKED").

---

## 13. Entry points

All names are proposals (not applied in Ghidra). Ghidra address = `0x7ff64ffe0000 + RVA`.

| area | RVA | proposed name |
|---|---|---|
| Threads and the frame | `0x7d0260` | App_Frame (main loop body) |
| Threads and the frame | `0x7df170` | RenderFrameTick (BeginFrame → RenderSubmit → EndFrame) |
| Threads and the frame | `0x938750` | teRender::PaceBeforeFrame (message 10) |
| Threads and the frame | `0x938970` | teRender::BeginFrame (message 0xb) |
| Threads and the frame | `0x938d80` | teRender::EndFrame (message 0xc) |
| Threads and the frame | `0x936190` | teRender::Start (render buffers, teVidDevice, render thread, render cvars) |
| Threads and the frame | `0x938000` | RenderThreadMain |
| Threads and the frame | `0x938150` | RenderThread_Tick |
| Threads and the frame | `0x939310` | RenderThread_ExecuteFrame |
| Threads and the frame | `0x938fe0` | RenderFrame_EndJob (present previous, replay, End) |
| Threads and the frame | `0x938360` | RenderFrame_FirstPacket (frame setup, uploads) |
| Threads and the frame | `0x9826a0` | RenderBuffer_AllocPacket |
| Threads and the frame | `0x982920` | RenderBuffer_Execute |
| Threads and the frame | `0x9ad890` | RenderFrame_Init (per-buffer task graph) |
| Device | `0x9128c0` | teVidDevice::InitializeWindow (factory, D3D11CreateDevice, frame latency) |
| Device | `0x91cb40` | teVidDevice_P::__CreateDeviceWindow (swap chain) |
| Device | `0x9142a0` | teVidDevice::Present |
| Device | `0x913f70` | teVidDevice::End (frame event query) |
| Device | `0x9149b0` | teVidDevice::HandleDeviceLost |
| Device | `0x922d40` | GfxContext_CommitState |
| Render world and submission | `0x9f6770` | teScene::Update |
| Render world and submission | `0x9f4310` | teScene::FlushDirtyNodes |
| Render world and submission | `0xbff7b0` | Game_SubmitFrame (UX tick, views, UI canvas last) |
| Render world and submission | `0x980310` | teView::SubmitScene (0x1040-byte view packet) |
| Render world and submission | `0x98b970` | ViewPacket_Execute (render thread) |
| Visibility and LOD | `0xa24e60` | StaticWorld_Create (map records → render objects, cull entries) |
| Visibility and LOD | `0x9b6350` | SceneCuller_CullAll (per-view: frustum, occluders, cull, emit) |
| Visibility and LOD | `0x9a8360` | CullContext_Cull (jobs) |
| Visibility and LOD | `0x9aa0d0` | CullStaticClass (SIMD kernel, bitsets, budget) |
| Visibility and LOD | `0x9a8cb0` | CullDistanceScreenSize (dynamic objects, detail culling) |
| Visibility and LOD | `0x9a7c30` | Occlusion_RasterizeOccluders |
| Visibility and LOD | `0x981b30` | Occlusion_TestBox |
| Visibility and LOD | `0x9299c0` | HWOcclusion_IssueQueries |
| Visibility and LOD | `0x8587d0` | Model_SelectLOD |
| Draw items and passes | `0x9740c0` | RenderView_EmitVisible |
| Draw items and passes | `0x9aede0` | EmitModelMesh |
| Draw items and passes | `0x9b0840` | DrawItem_Create (pass selection) |
| Draw items and passes | `0x9725d0` | RenderView_CreatePasses (the 20 named passes) |
| Draw items and passes | `0x975a20` | RenderView_FillInstanceBuffers ("Model Inst VB") |
| Draw items and passes | `0x975b80` | Scene_DrawPhase (execute one stage) |
| Draw items and passes | `0x9a3cd0` | TranslucencyPass_Sort |
| Draw items and passes | `0x9603b0` | DrawItem_Draw (dispatch by kind) |
| Draw items and passes | `0x9798f0` | DrawMesh |
| Frame passes, lighting, shadows, post | `0x98bc90` | Renderer_RenderFrame (sub-views: shadow maps) |
| Frame passes, lighting, shadows, post | `0x98d100` | Renderer_RenderView (the pass list) |
| Frame passes, lighting, shadows, post | `0x8d7540` | Renderer_CreateScreenTargets |
| Frame passes, lighting, shadows, post | `0x8d63b0` | Renderer_BindGBufferTextures |
| Frame passes, lighting, shadows, post | `0x8e7f00` | SSAO_HBAO_Render |
| Frame passes, lighting, shadows, post | `0x8da870` | Shadow_CompositeSunShadowMask |
| Frame passes, lighting, shadows, post | `0x8d3950` | Lighting_RenderDeferred |
| Frame passes, lighting, shadows, post | `0x8e0a00` | Lighting_SetupLocalLight (16 bins) |
| Frame passes, lighting, shadows, post | `0x8e9290` | SSLR_Render |
| Frame passes, lighting, shadows, post | `0x8d4a20` | PostFx_BackgroundBlur (After Blur) |
| Frame passes, lighting, shadows, post | `0x98e250` | RenderView_ResolveToLDR |
| Frame passes, lighting, shadows, post | `0x8d5bf0` | PostFx_LdrAA (FXAA? ) |
| Frame passes, lighting, shadows, post | `0x9b1cc0` | OutlinePass_Execute |
| Frame passes, lighting, shadows, post | `0x8f5a90` | ScreenEffect_Apply(mode) (manager slot 29, vfunc +0x88) |
| Frame passes, lighting, shadows, post | `0x937d80` | Present_ColorblindFilter |
| Frame passes, lighting, shadows, post | `0x9bc540` | SunShadow_Configure |
| Frame passes, lighting, shadows, post | `0x9be6f0` | LocalShadowAtlas_CreateTargets |
| Frame passes, lighting, shadows, post | `0x85be20` | ApplyGraphicsSettings |
| Shaders and materials | `0x935ce0` | Render_RegisterShaderHandlers |
| Shaders and materials | `0xf3dfa0` | ShaderGroup_GuidFromKey |
| Shaders and materials | `0x9314a0` | ShaderSys_GetOrCreateGroup (render vfunc +0x2b0) |
| Shaders and materials | `0x930470` | ShaderGroup_BuildRuntime |
| Shaders and materials | `0x91af90` | teVidDevice::CreateShader |
| Shaders and materials | `0x977c50` | Material_GetRenderObject |
| Shaders and materials | `0x9785b0` | Material_BuildInstanceBindings |
| Shaders and materials | `0x9397f0` | Render_InitDeviceResources (engine buffers, engine shader groups) |
| Effects | `0x9d86b0` | EffectHandler_Register |
| Effects | `0x1830d20` | g_effectElementKinds[26] (data) |
| Effects | `0x9f33f0` | teScene::AddEffect |
| Effects | `0x9d8dd0` | EffectScene_UpdateAll (jobs, LOD frame skipping) |
| Effects | `0x8fe060` | EffectInstance_RenderVisit (LOD, gather) |
| Effects | `0x8c1a60` | EffectElement_WriteAttributes |
| Effects | `0x8fe5e0` | EffectRender_InitBuffers |
| UI | `0xa697a0` | teCanvas_Submit |
| UI | `0xa6d680` | teCanvas_InitBuffers |
| UI | `0xa48120` | BakedMapText_Create (glyph mesh → runtime model) |
| UI | `0xb0c640` | UXTankEffect_CreateEffect |
| UI | `0x94f2e0` | PrimDraw_AddInstance |

---

## 14. Open questions

1. **Tonemap, exposure and bloom**: where they run was not found. `TONEMAP_EXPOSURE` exists; candidates are the
   HDR → LDR helper shader (§7.2 #17) and the lighting composite (`0x8eb190`). Identify the helper-mode shaders.
2. **Anti-aliasing**: `AADetail` is only copied into a post-parameter block; the 2-pass LDR post (`0x8d5bf0`) is
   FXAA-like [?]. No MSAA targets.
3. **G-buffer contents** of RT1–RT5 (normal encoding, what RT2 / RT3 / RT5 hold): read the G-buffer pixel shaders'
   output signatures and code (DXBC SHDR of a hero / standard group's G-buffer permutation) or capture a frame.
4. **Permutation keys**: which key a draw uses (pass × vertex variant) and the names behind the 369 keys and the
   texture-slot / CB hashes (try the OWLib hash lists).
5. **Map lights**: comp 0x05's render interfacer → the light records binned by `0x8e0a00`; which object types
   get HW occlusion queries; cull view types 1, 2 and 6–8.
6. **First-person viewmodel**: projection / depth handling (readers of `camera.FirstPersonNearZ`).
7. **Engine buffers → shaders**: how the Matrix / DataFlow / Irradiance / dynamic-instance rings are bound (CB b1/b2
   or SRVs), and the layouts of the 22 engine CB layouts.
8. **Effects**: which element kind is the decal track and how effect decals project; whether kind 9 is a light.
9. **UI text**: do TextBlocks tessellate glyph outlines like baked map text? UI target gamma (the back buffer is
   UNORM, not sRGB).
10. **Live checks worth doing** (short, game lock): enumerate the live render targets (sizes, formats) through
    Prometheus' D3D11 hook to confirm §7.1; count draw calls / state changes per frame; read the defaults of
    `MaxFrameLatency` and the frame limiter; capture one frame with a graphics debugger (RenderDoc under Proton) to
    settle 1–3 in one go.
11. Proposed Ghidra renames for all entry points in §13 (not applied: no Ghidra-writes lock taken).
