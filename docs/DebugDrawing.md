# Debug drawing: what the engine can draw, and a server → Prometheus debug channel

T050 (2026-09-29). Build 24919. Addresses are **Ghidra addresses**; RVA = Ghidra − `0x7ff64ffe0000` (Prometheus uses
RVAs: e.g. Ghidra `0x7ff6508ad940` = RVA `0x8cd940`). Careful: subtracting `0x7ff650000000` instead gives an RVA that
is off by `0x20000` (made once during T050; the byte guards caught it).
Tags: **[G]** read in Ghidra, **[L]** measured live, **[H]** confirmed by the user in game, **[?]** inferred.
Related: [Rendering.md](Rendering.md) (render manager, frame, views), [Physics.md](Physics.md), [Maps.md](Maps.md) §4
(spawn rooms), [InstanceServer.md](InstanceServer.md) (control API).

## 0. Summary

- The engine's debug renderers survive in this release build, **unused**: nothing in the shipped code calls them
  or turns them on, but none is stubbed.
  - **PrimDraw**: world-space solid shapes and lines (render-manager vtable +0x440…+0x498). Depth-tested and lit.
  - **Debug-overlay pass**: screen-space text in the engine's debug font (`DebugPrintV`), a filled rectangle (+0x4b0)
    and a polyline (+0x4d8), plus **10 built-in overlays** (fps, jobs, time scale, memory, input lag, …) that are
    registered but never enabled.
- **Prometheus is the glue** (window *Tools › Debug Draw*, MCP tool `debug_draw`):
  - it feeds PrimDraw every frame from a hook on `Game_SubmitFrame`;
  - it adds its own overlay object to the engine's overlay list, which draws HUD text and 2D primitives;
  - it toggles the 10 built-in overlays.
  Everything is live-verified [L] and user-confirmed [H].
- **Server → Prometheus:** the instance server serves `GET /debug/draw`, and Prometheus polls it every 250 ms. The
  reply carries world shapes, HUD text lines and 2D primitives: the server's hero position, spawn points and rooms,
  objectives, capture zones, and match / clock state. The schema is transport-independent, so it can move to a
  custom JAM message once T033 exists.
- **Live server view (T072, §5):** the instance server pushes its state every tick over our custom JAM message
  (PrometheusLobby 32002, relayed by lobbylogic); Prometheus prints it as text on the game screen (clock, the
  server's hero, match, ECS, Statescript, free-form `Note` lines) next to the client's own values. `!serverview on`.

## 1. Inventory of the engine's debug facilities

| facility | where | state | used by Prometheus |
|---|---|---|---|
| **PrimDraw** world primitives | render vtable +0x440…+0x498 → `PrimDraw_AddInstance` `0x7ff65092f2e0` | live, no callers [G, L] | yes (§2.1) |
| **Debug text** `DebugPrintV` | `0x7ff6508ad940` (`DebugPrintf` `0x7ff6508ac910` wraps it) | live; return-address checked [G, L] | yes, check patched (§2.2) |
| **2D rect / polyline** | render vtable +0x4b0, +0x4d8 | live [G, L] | yes (§2.2) |
| **10 built-in overlays** | list at render manager +0x8F0 | registered, never enabled [G, L] | toggles (§2.3) |
| FPS / RTT line | render manager +0x950 bit 7 | drawn in the overlay pass [G, L] | toggle |
| textured 2D quads | render vtable +0x4a0 / +0x4a8 / +0x4b8 | live; +0x4a0/+0x4a8 return-address checked (/0) [G] | no |
| cvars ~135, registry + set-by-name | `0x7ff651839ed0`, set `0x7ff6507ccf30` | most debug cvars have **no reader** [G] | no (§1.3) |
| in-game console | dispatcher `0x7ff6507c64c0`, `--tank_Console=1` | no command is ever registered: "Unknown command" [G] | no |
| Statescript `StateDrawDebugSphere` | client class, vtable `0x7ff6515a9098` | ticks, draws through game-EA `+0x1C8` object's vf0/vf+0x20 (unidentified) [G] | no |
| Statescript `StateDrawDebugLine` | no client class | server-only / stripped [G] | – |
| Statescript `ActionDebugText` | vtable `0x7ff6515bb240` | DoAction = `return true`: no-op on the client [G] | – |
| physics (Domino), navigation, AI, sound debug draw | – | only text dump writers (`DominoDump`); no draw code found [G] | – |

### 1.1 PrimDraw [G, L]

**Setup.** `PrimDraw_Init` `0x7ff65092e6f0` (from `Render_InitDeviceResources`): 14 shape pools, a material from
shader group `0x0E1000000000001E`, per-frame buffers "PrimDraw Instance" / "PrimDraw Inst VB".

**API: the render manager's vtable** (object pointer at RVA `0x181e3e0`). Each function is
`void f(RenderMgr* rm, …, const float rgba[4], uint32_t flags)`: colour ×255 → bytes, then an inner builder makes a
4×4 instance matrix on the current render buffer and calls `PrimDraw_AddInstance(rb, type, m[16], rgba8, flags)`.
Matrices are **row-major: rows 0–2 = local x / y / z axes, row 3 = translation** (w = 1). Float arguments are
ordinary float parameters (XMM2/XMM3), the colour pointer and flags follow.

| slot | wrapper → builder (Ghidra) | arguments after `rm` | type | shape [L: seen in game] |
|---|---|---|---|---|
| +0x440 | `…91f660` → `…9305d0` | `pos[4]`, `r` | 1 | **sphere** at a point, radius r |
| +0x448 | `…91f0b0` → `…92fea0` | `m[16]`, `r` | 1 | sphere, oriented |
| +0x450 | `…91fc10` → `…930970` | `m[16]`, `s` | 3 | **hemisphere** (dome up) |
| +0x458 | `…920040` → `…930e90` | `m[16]`, `r`, `h` | 4 | **cylinder**, centred on the origin, height h |
| +0x460 | `…920780` → `…931410` | `m[16]`, `r`, `h` | 5 | **cone**, apex up, centred |
| +0x468 | `…920c30` → `…9317e0` | `m[16]`, `r`, `h` | 7 + 2×8 | **capsule**: body height h + caps at ±h/2 (total h + 2r) |
| +0x470 | `…9210a0` → `…92fa70` | `m[16]`, `scale[3]` | 0 | **box**, mesh spans −1…1 → scale = half extents |
| +0x478 | `…921730` → `…931e70` | `p0[4]`, `p1[4]` | 9 | **line** p0 → p1 (thin) |
| +0x480 | `…921ee0` → `…9326e0` | `m[16]`, `s` | 10 | **circle** outline (horizontal) |
| +0x488 | `…9227c0` → `…932e60` | `m[16]`, `s` | 11 | **arc** / half circle outline |
| +0x490 | `…922ae0` → `…933570` | `m[16]`, `s` | 12 | **star** of radial lines (a point marker) |
| +0x498 | `…9232f0` → `…933ca0` | `m[16]`, `scale[3]` | 13 | a wire figure (bow-tie / frustum-like) [? exact] |

(`…` = `0x7ff650`.) Solid shapes are **lit and one-sided**: from inside a box you see nothing (the spawn-room box
around the player is invisible) [L].

**Flags** (`PrimDraw_AddInstance`): alpha < 0xFE sets **0x10** (translucent, works [L]); **0x20** = **unlit**, the
exact colour (bright, flat) [L]; **0x100** = per-view sorted path (one packet per primitive) [G].

**Lifetime: one frame.** `teRender_BeginFrame` resets the batch lists; re-add every frame [G].

**Submission.** Views push / pop themselves on the render manager (vf +0x178 / +0x180, from `teView_*` and
`teRender_EndFrame`); push and pop hand the pending primitives to a view, so primitives added before the first view
renders end up in the world view [G, L].

**Threading.** Pools use a spin lock, the batch lists don't: add on the **main thread between BeginFrame and
EndFrame** [G].

**Callers.** No indirect call through +0x440…+0x498 exists in `.text` (two independent scans) [G].

### 1.2 The debug-overlay pass [G, L]

`teRender_EndFrame` → `0x7ff6509426e0` (runs in app states 4/5):
1. **Gate.** The pass runs if any overlay in the list is enabled, or `rm+0x950 & 0x70`, or `rm+0x950` bit 7 (the
   "FPS: … / RTT: … ID: …" lines), or the screenshot banner.
2. **Background.** It pushes a 2D view (vf +0x178) and draws one translucent rectangle behind the enabled overlays
   (vf +0x4b0, colour `0x7F000000`). Size = the widest overlay × the sum of their heights (+ spacing).
3. **Overlays.** It calls each enabled overlay's `Draw(self, float x, float y)`, stacking them downwards.
4. **Callbacks.** It calls the callbacks in the list at `rm+0x8D0` (`{fn(x, y, ctx) → new y, ctx, next}`, empty in
   the stock client), then the screenshot banner, then pops the view.

Anchor: `0x7ff651781144/48` (x, y), `0x7ff65178114c/4d` (right / bottom alignment).

**Overlay object**:

| offset | field |
|---|---|
| `+0x00` | vtable (6 slots) |
| `+0x10` | width (float) |
| `+0x14` | height (float) |
| `+0x18` | enabled (u8) |
| `+0x20` | next |
| `+0x28` | mode (i32) |

Vtable slots:
- 0: scalar deleting destructor (flag 1 frees through the game allocator)
- 1: delete
- 2: `Draw(self, float x, float y)`
- 3: `HandleToggle(code)` (no caller)
- 4: `GetHelp`
- 5: GetName-if-enabled `0x7ff650998e30` (shared)

**The 10 built-in overlays** [L: all present, all off at start]:

| code | help | vtable RVA | w × h (px) | draw |
|---|---|---|---|---|
| fps (modes: fps / sfps / mspf) | Frames per second | `0x158cea8` | 300 × 75 | `0x7ff6509663c0` |
| wt | Worker Thread Status ("JOBS") | `0x158cde8` | 175 × 15 | `0x7ff650967020` |
| ts | Time Scale | `0x158ce48` | 135 × 15 | `0x7ff650967370` |
| rb | Render Buffer Memory | `0x158ce18` | 135 × 15 | `0x7ff6509671b0` |
| tm | Texture Memory ("TEX MEM", "GPU USAGE", "GPU ALLOC") | `0x158ce78` | 275 × 45 | `0x7ff6509674f0` |
| mma | Memory Manager Allocations | `0x158cd88` | 250 × 30 | `0x7ff6509678a0` |
| ci | CPU Idle | `0x158cdb8` | 100 × 15 | `0x7ff650967b10` |
| rm | Resource Memory | `0x158cd28` | 200 × 0 | `0x7ff650967ce0` |
| il (mode 1: ild) | Input Lag (Detailed) | `0x158cd58` | 245 × 30 | `0x7ff650968000` |
| gt | GPU Temp | `0x158ccf8` | 200 × 15 | `0x7ff6509685e0` |

Enabling = writing `+0x18 = 1` (and `+0x28` for fps / il). FPS, JOBS and TEX MEM were shown this way [L].

**Text.** `DebugPrintV` `0x7ff6508ad940` (RVA `0x8cd940`):
- signature: `(float x, float y, const float rgba[4], const float shadow_rgba[4] or null, unused, const char* fmt, va_list)`;
- formats into 512 bytes and draws with the debug font (`0x7ff65180b7a0`, loaded in game [L]);
- **return-address check**: when the caller is outside the game's code sections, the failure path divides by zero.
  In the live code the check loop ends at RVA `0x8cdc86` (`cmp r9d, eax; jl`), followed by 12 anti-disassembly junk
  bytes at `0x8cdc8f` (the treated dump shows NOPs there), the `/0` block from `0x8cdc9b` and the success label
  `0x8cdd39`.
- `DebugPrintf` `0x7ff6508ac910` is the variadic front end, with its own check.

**2D primitives.** Render vtable:
- **+0x4b0** `FillRect(rm, const float r[4] = {w, h, x, y}, uint32 colour)`, colour `0xAABBGGRR`;
- **+0x4d8** `Polyline(rm, const struct {float2* pts; u32 count; u32 cap;}*, uint32 colour)`, a **connected strip** [L].

Both use the pushed 2D view's transform (pixels, origin top left): they only draw inside the overlay pass.
- +0x4a0 / +0x4a8: textured quads with a return-address `/0` check.
- +0x4b8 (textured), +0x4c8, +0x4d0, +0x4e0: not decoded.
- +0x4e8 / +0x4f0: add / remove an overlay.

### 1.3 Cvars and the console [G]

- **Registry and access.** Registry `0x7ff651839ed0`; find by name `0x7ff6507cce60`; set by name `0x7ff6507ccf30`
  (a placeholder is created if the cvar isn't registered yet). Each cvar has a fixed storage global.
- **Dead (no reader):** `render.occlusionDisplay`, `enlighten.probedebug`, `sound.DrawSound`, `anim.ShowAnimNames`,
  `anim.Debug*`, `firstPerson.ShowFrameFlag`, `firstPerson.DrawThirdPersonModel`, `input.MouseAccelerationDebugFlag`.
- **Live:**
  - `render.LockCull` (`rm+0x107`)
  - `canvas.CompositeBoundsVisible`, `canvas.DebugNormalsEnabled`, `canvas.ClipGeoVisible`, `canvas.SplitInstances`
  - `input.GamePadRenderDebug` (2D rect + polyline)
  - `anim.ShowBindPose`, `firstPerson.DetachModelFlag`
  - `enlighten.dusterdebug`: it has a reader, but that reader is an update, not a draw [?]
- **No cvars exist** for navigation, AI, physics, collision, net or wireframe.
- **Setting cvars.** `Settings_v0.ini` sections on a whitelist (GPU, Render, Sound, ux, Console, showmode) are applied
  through set-by-name (e.g. `[Render.13] LockCull="1"` [? untested]).
- **Console.** The dispatcher exists (`--tank_Console=1`), but nothing registers a command, so every line gets
  "Unknown command". A JAM handler `0x7ff6508833c0` also feeds it (remote console? [?]).


## 2. What Prometheus draws

Code: `prometheus/Prometheus-Core/Systems/DebugDraw.{h,cpp}`, window `UI/windows/debug_draw_window.h`, MCP tool
`debug_draw`.

### 2.1 World space: hook + PrimDraw [L, H]

**Hook.** Prometheus hooks **`Game_SubmitFrame`** `0x7ff650bdf7b0` (RVA `0xbff7b0`, `void (App*, float dt)`, the
app's vfunc +0x38; `RenderFrameTick` calls it between BeginFrame and EndFrame, on the main thread). Before the
original runs, it replays its shape list through the render vtable. The engine projects, depth-tests and lights the
shapes, so no camera matrices are needed.

**Shape model** (shared with the server):

| kind | fields | PrimDraw |
|---|---|---|
| `sphere` | `pos` = centre, `radius` | +0x440 |
| `box` | `pos` = centre, `half`, `yaw` (rad about y) | +0x470, scale = half (`box_scale` 1) |
| `capsule` | `pos` = centre, `radius`, `height` = cap-centre distance | +0x468 |
| `cylinder` | `pos` = base centre, `radius`, `height` | +0x458 (centred mesh: pos + h/2) |
| `line` | `pos` → `to` | +0x478 |
| `slot` | raw slot 0x440…0x498 with `pos`, `radius`, `height`, `half` | tests |

All kinds also take:
- `color`: RGBA 0–1; alpha < 1 draws translucent;
- `flags`: `0x20` unlit, `0x100` sorted;
- `group`: legend entry and toggle;
- `label`: shown when the window's "Labels" option projects labels with the game camera (`Camera::WorldToScreen`,
  ImGui).

### 2.2 Screen space: a Prometheus overlay in the engine's pass [L, H]

**The overlay object.** Prometheus links its own overlay object into the engine's list (main thread, once) and sets
it up like this:
- **Destructor slots:** its own no-op ones, since the object lives in Prometheus' memory.
- **Slot 5:** the engine's GetName.
- **Size:** width and height set every frame from the HUD lines (15 px per line, about 7.5 px per character).
- **Enabled:** only while there is HUD text or a 2D primitive to draw.

So the engine's own background box sizes itself around our block, and the block stacks under any built-in overlay.

**Draw.** Its `Draw(x, y)` does two things:
- prints each HUD line with `DebugPrintV` (line colour, black shadow);
- records the 2D primitives through +0x4b0 / +0x4d8.

**The patch.** Prometheus patches `DebugPrintV`'s failure path, the same approach as the "Anti-Anti-Debug" patches in
`dllmain.cpp`:
- **Patch:** `jmp 0x8cdd39` (`E9 A5 00 00 00`) at RVA `0x8cdc8f`.
- **Guard:** it is applied only when the live bytes around the site match (the loop end, the `/0` block and the
  target).
- **Timing:** it is applied lazily on the first frame.

The MCP result's `text_patch` reports it.

HUD lines and 2D primitives come from the server payload (§3) or locally (MCP `hud` / `screen`). They are grouped and
toggled like the world shapes.

### 2.3 Built-in overlays

The window's "Screen" section and MCP `overlays` action control:
- the 10 overlays (checkbox each; mode for fps and il);
- the FPS / RTT line (`rm+0x950` bit 7);
- the Prometheus HUD on / off.

### 2.4 MCP `debug_draw`

| action | does |
|---|---|
| `status` | counters, poll state, groups; `shapes=true` also lists the shapes |
| `add` | local world shapes (`ttl` in seconds; 0 = until cleared) |
| `clear` | drops local world shapes |
| `poll` | `on`, `host`, `port`, `conn`, `interval_ms` |
| `enable` | master switch |
| `groups` | `visible = {name: bool}` |
| `settings` | `box_scale`, `cylinder_slot`, `cylinder_centred` |
| `hud` | adds `text`, or `lines = [{text, color, group}]`; `clear`, `ttl`, `on` |
| `screen` | `prims = [{kind: rect, rect: [x, y, w, h]} or {kind: lines, points: [[x, y], …]}, color, group]` |
| `overlays` | `set = {code: bool or {on, mode}}`, `fps_line` |

The tool is not write-gated: it adds drawing only and doesn't change game state. `overlays` writes the overlay enable
bytes, which only switch on debug displays.

## 3. Server → Prometheus channel

### 3.1 Design
- **Now (dev):** Prometheus polls **`GET /debug/draw[?conn=N]`** on the instance server's loopback control API
  (`127.0.0.1:3729`) every 250 ms on a background thread. Each reply replaces the "server" set.
  - Pros: no protocol work; works with the stock client plus Prometheus; independent of the match's traffic.
  - Cons: loopback only; up to 250 ms stale.
- **Later:** the same fields in a custom JAM message on the game link, once T033's mechanism exists (T033 is scoped to
  the lobby, so a game-link variant would be a follow-up). That would be frame-accurate and could go over the network.

### 3.2 Frame, units, lifetime
- **World space:** client world coordinates, metres, **y up**. These are the same numbers as `world.Entity.Pos`,
  `maps.Spawn.Pos` and the movement snapshot. Yaw is in radians about +y.
- **Screen space:** pixels, origin top left. HUD lines are stacked by the overlay pass.
- **Lifetime:** each reply is a complete snapshot, kept until the next one. It is cleared after 2 s without a reply.

### 3.3 Payload
```json
{
  "map": 576460752303423579, "name": "TEMPLE OF ANUBIS", "frame": 36278,
  "groups": [{"name": "heroes", "label": "Hero bodies: where the server believes they are", "color": [1,0.1,0.9,1]}],
  "shapes": [
    {"group": "heroes", "kind": "capsule", "pos": [x,y,z], "radius": 0.35, "height": 1.1, "color": [1,0.1,0.9,1],
     "label": "hero 0x4000001f (conn 1, team 1)"},
    {"group": "rooms", "kind": "box", "pos": [..], "half": [..], "color": [1,0.45,0.1,0.25], "label": "room 1: att, progress 0–end, priority 1"},
    {"group": "spawns", "kind": "line", "pos": [..], "to": [..], "color": [..]}
  ],
  "hud": [{"group": "server", "text": "SERVER TEMPLE OF ANUBIS  frame 36278", "color": [0.6,1,0.6,1]}],
  "screen": [{"group": "g", "kind": "rect", "rect": [x,y,w,h], "color": [..]},
             {"group": "g", "kind": "lines", "points": [[x,y], ...], "color": [..]}]
}
```
What the instance server sends (`server/inst/match/debugdraw.go`, `Manager.DebugShapes`):

| group | kind | what |
|---|---|---|
| `heroes` | capsule, centred 0.9 m up | The server's hero position. It moves only when the server moves it, so it drifts away from the client's hero as the player walks. That gap is the debug information. The window's "Client hero" option draws the client's own hero next to it (a green translucent capsule). |
| `spawns` | sphere + facing line | Every spawn point, in team colour. |
| `rooms` | translucent box | Around each room's six points, padded 1.5 m, 3 m high. An approximation, not the trigger volume. |
| `objectives` | box | The objective placements the server copies. |
| `capture` | cylinder | Each capture point's zone with the rules host's radius (6 m; see §4). |
| `server` | HUD lines | Map and frame, rules phase, each capture point (owner, capturing team, progress), each connection's clock (state, lead, min lead, flags, resyncs). |

## 4. Status and open points

**Confirmed:**
- [L, H] World shapes from the server in game on Anubis. The user confirmed it "appears to be working as expected".
- [L] Every PrimDraw slot identified visually (table §1.1); box unit size; flags 0x10 / 0x20.
- [L] HUD text and 2D rect / polyline from the server and locally; engine overlays toggled.

**Open:**
- [?] Capture zone radius (server default 6 m) against the in-game ring: not checked yet.
- **Screen text in world space**: it currently uses ImGui labels. Engine text at a projected position would need the
  world → screen transform; the camera function Prometheus uses is `0xa1fa70` (RVA).
- **`StateDrawDebugSphere`**: identify the game-EA `+0x1C8` object it draws through. It may be a second debug-draw
  interface, or nothing in the release build.
- **2D slots** +0x4b8 / +0x4c8 / +0x4d0 / +0x4e0 are not decoded. The textured quads +0x4a0 / +0x4a8 would need
  their `/0` checks patched.
- **Stale players:** `/conns` keeps players of dropped connections in state "assigned" (they show up in the HUD's clock
  lines). That is an instance-server issue, noted as a follow-up.

## 5. Live server view: the server's state every tick (T072)

*Spec written before the code, then updated to what was built (2026-09-29); tags as above, **[D]** = our design
choice.* The instance server pushes its view of the match **once per server tick** (62.5 Hz, one frame per command
frame) to the client over our custom JAM protocol (PrometheusLobby 32002), and Prometheus shows it **as text on the
game screen** (the engine's debug font, §2.2) next to the client's own state. It tests that the custom-protocol path
(T033) carries real-time updates, and it is the general debug channel for server work: any server code can add a
line (`Manager.Note`). No HTTP polling is involved; `GET /debug/draw` (§3) stays for the static map shapes (spawns,
rooms, zones).

### 5.1 Path [D, L]
```
instance (game loop, per tick)                       game server link (server/internal/gamelink, JSON lines, TCP)
  └─ gamelink "frame" {conn, seq, frame, sent_us, body} ──▶ lobbylogic
        └─ PrometheusServerView 32002 (docs/JAM.md §13.8) to the conn's lobby session ──▶ gateway ──▶ client
              └─ Prometheus CustomJam receiver (network thread: arrival stamp) ──▶ ServerView (UI thread)
```
- **Off by default.** `!serverview on|off|status` (chat, or `curl -X POST --data 'command("serverview on")'
  http://127.0.0.1:3726/eval`) subscribes the lobby session that sends it, after a handshake: lobbylogic first calls
  the Prometheus tool `server_view {"action":"hello"}` and subscribes only on an `ok` answer naming message 32002
  (a DLL without the message would drop the lobby link on it, JAM.md §13.8).
- lobbylogic keeps the subscription per session **in memory**: it tells the game server holding the session's
  assignment (gamelink `view {conn, on}`), again after every new assignment of that session (a new match follows
  automatically) and when the game server (re)connects. It ends when the lobby session closes. A **lobbylogic
  restart forgets it** (send `!serverview on` again). Only game servers whose hello says `view: true` get it (the R&D
  server doesn't).
- **Instance** keeps a set of subscribed connections. Every tick, after the matches ticked and the players' packets
  went out, it builds one frame per subscribed connection whose player has a client (not in state `assigned`).
  Frames go to a bounded queue (64) drained by the link's writer goroutine; the game loop never blocks on TCP. A full
  queue or a down link drops the frame (`/stats` `view.dropped`); the client sees the gap in `seq`.
- **lobbylogic** maps `conn` → the session of that assignment and sends 32002, stamping `relay_us`; frames for a
  session it doesn't have are dropped (`orphans`). No per-frame log: one `server view relay` line every 10 s (rate,
  bytes, errors, orphans).
- **Gateway**: unchanged (it maps channel 33 already). It logs **every send at info level with the payload in hex**,
  which is the one real cost of the stream (§5.5). A quiet list for 32002 is a gateway change that needs a lobby
  group restart: follow-up, the user's call.

### 5.2 The frame [D]
Header fields (binary JAM, JAM.md §13.8): `seq` (per subscription, from 1; a gap = a dropped frame), `frame` (the
match's command frame P), `sent_us` (instance wall clock, Unix µs, when the frame was built), `relay_us`
(lobbylogic's wall clock when it forwarded it). Everything runs on one machine, so client arrival − `sent_us` is the
one-way latency.

`body` is compact JSON (positions rounded to 1 cm, angles to 0.001 rad; `server/inst/match/serverview.go`):

| key | when | what |
|---|---|---|
| `v` | always | 1 (body version) |
| `txt` | always | **the server's screen text**: its summary lines (below), then the `Note` lines |
| `ms` | always | server time: ms since the instance started (the clock of the 20304 pongs) |
| `late` | always | the loop's late ticks so far (`/stats`) |
| `clk` | always | the connection's clock: `st` player state, `in` newest input CF, `lead`, `min` (window minimum), `fl` clock flags sent, `rs` resyncs, `tm` client timing report (CommandFrames.md §4–6, InstanceServer.md §5) |
| `game` | rules host running | `ph` phase, `pf` frames in the phase, `pts` capture points `[{o order, on, own owner, cap capturing team, ct contested, p progress 0–1}]` |
| `ecs`, `ev` | always | the match world's ECS entity count and ECS events drained so far |
| `comps` | the player has a body | the hero's ECS components (hex) |
| `ss` | the hero is simulated | the hero's Statescript: `inst` instances, `act` active states, `g` `["graph:active", …]` (graph = GUID low 12 bits), `f` simulated CF, `sent`/`ack` delta end CFs, `d` deltas, `skip`, `lost`, `inerr` input errors |
| `rules` | rules host running | per server-run entity (brain, objective copies): `{id, k kind, inst, act, g}` |
| `full` | every 64th frame and the first | true: `ents` lists every world entity, `map` is the map name |
| `ents` | always | full frames: every entity; otherwise only entities that can move (heroes). Each `{id, k kind, d def, tm team, o owner conn, p [x,y,z], y yaw}` (the server's belief) |
| `me` | the player has a body | `{id body, p, y}` the server's position of this connection's hero |
| `xf` | a 21601 was received | the last `PlayerTransform` 21601 `{p, q, f the frame it arrived at}`. Measured on Anubis: `p = [15.98, 0, 15.98]`, `q = [0, 0, 0, 0]` at every join [L]: not a world position, so the message's meaning stays open (MatchLogic.md §15.2) |

The server's summary lines (`txt`), one topic each:
```
SERVER TEMPLE OF ANUBIS  CF 8933  t 143.1 s  late ticks 10
conn 3 playing  lead 5 (min 5)  flags 0x0  resyncs 1  timing 148
Genji 0x4000001f at -1.24 1.81 -44.91  yaw 32 deg  (server's belief)
game in progress  110 s
  point A: owner 0  capturing -1  0%
  point B: owner 0  capturing -1  0%  (off)
world 31 entities  ECS 31 entities, 214517 events  players 2
ECS hero components: 01 03 10 12 23 28 68 7a
SS hero: 22 instances, 113 active states  sim CF 8937 (+4)  delta sent 8936 acked 8936  deltas 4650 skipped 0 lost 0 input errs 0
SS hero graphs: 033:2 04b:14 043:2 157:18 317:5 1db:12 3a3:6 015:14 304:15 1c7:12
    1cf:7 2cf:2 352:1 5ad:2 b3b:1
SS rules host: 7 entities, 10 instances, 58 active states
SS rules graphs: bra/029:6 bra/1e1:4 bra/011:1 obj/028:13 obj/028:14 obj/208:3 obj/208:8 obj/2bb:5 obj/2bb:4
rules: cf 2048 capture enabled 0x40000019 i0=1 i1=0 u=0x0        (the rules host's last 3 effects)
```
**Adding a line from server code**: `Manager.Note(conn, key, text)` on the game loop (conn 0 = every connection;
text "" removes it; lines sorted by key, shown as `key: text` under the summary). The body is capped at 8 KiB (a
bigger one is replaced by `{"v":1,"error":"too big","size":n}`, shown in orange).

### 5.3 Prometheus (`Systems/ServerView.{h,cpp}`, window *Tools › Server View*, MCP `server_view`)
- **Receive.** CustomJam's receiver stamps the arrival (Unix µs, `GetSystemTimePreciseAsFileTime`) on the network
  thread and queues the message; ServerView takes it on the UI thread. It keeps the **latest** frame, a history of
  the last 256 headers (seq, frame, sent, relay, arrival, size), and the merged entity table (full frames replace it,
  partial ones update it).
- **Screen text (default on)** [H]: every rendered frame, through the Prometheus overlay in the engine's debug
  overlay pass (§2.2, group `view`, drawn before any other HUD line): the server's `txt` lines in green, then three
  client lines in blue: `CLIENT hero at x y z  yaw … (server off by … m)`, `CLIENT CF … (±n vs server)  lead  flags
  buffer  entities`, `STREAM …/s  latency … ms (p95 …)  drops  size`. The client values come from the scene
  component 0x01 of the controller's camera-attach entity and the netcode clock (`*(EA+0x218)+0x50`,
  CommandFrames.md §2). Nothing is drawn 2 s after the last frame.
- **World markers (opt-in)**: a marker per server entity (heroes magenta capsules, objectives yellow, placements
  grey), the client's hero capsule, a line between the server's and the client's hero labelled with the error.
- **Stats.** Latency (arrival − `sent_us`): last / average / p95 / max over the history; its split instance →
  lobbylogic (`relay_us − sent_us`) and lobbylogic → client; frames in the last second; drops (seq gaps); restarts
  (seq went back: new subscription or game server restart); **coalesced** frames (more than one frame taken in one
  UI frame: only the newest is shown); body size.
- **Panel** (*Tools › Server View*): toggles (screen text, world markers, reset stats), the stream stats, a server |
  client table (CF, lead, clock flags, P / input CF, resyncs / buffer, hero position and error, 21601, entities,
  server time) and the match state.
- **MCP `server_view`**: `{}` → the latest frame (header + parsed body), the client's state and the stats;
  `history: n` adds the last n headers; `action: text | draw {on}`; `action: reset`; `action: hello` (the handshake).

### 5.4 Stage 2 (not in T072)
The same message on the instance link (custom JAM protocol in the instance data stream, JAM.md §13.4 "game link")
for lower latency tied to command frames. The header fields and body don't depend on the lobby path, so the message
moves unchanged.

### 5.5 Measured (2026-09-29, localhost, Anubis, Genji, the unmodified client + Prometheus)  [L, H]
| | |
|---|---|
| rate | 61–62.8 frames/s received (instance: 62.5/s built), **0 drops** over >16 000 frames |
| latency (arrival − sent) | avg **0.22–0.29 ms**, p95 0.31–0.56 ms, max 2.5 ms; instance → lobbylogic avg 0.06–0.08 ms, lobbylogic → client avg 0.16–0.18 ms |
| coalesced | 3–5 % of frames (the game renders slower than 62.5 Hz at times; only the newest is drawn) |
| size | first version (no text): 466–505 B per frame, full frames 2.9 KB; with `txt` + ECS/Statescript: **~2.2 KB avg**, max 4.6 KB (full frames) |
| instance loop | late ticks unchanged (10 before and after enabling); `view.dropped` 0 |
| lobbylogic / gateway CPU | < 1 % each |
| **gateway log** | **~290 KB/s (≈ 1 GB/h)** while streaming: the gateway's info line per send with the payload in hex. The stream's only real cost; fix = gateway quiet list for 32002 (lobby group restart, the user's call) |
| in game [H] | the user saw the server and client lines on screen, updating live while walking (2026-09-29) |

Seen along the way: a stale connection stuck in `assigned` (§4 "Stale players") doubled the traffic
to a dead lobby session (125 frames/s, one gateway ERROR per frame) until the instance skipped players without a
client and lobbylogic dropped frames for unknown sessions. The server has no movement model (roadmap #10), so its
idea of the hero stays at the spawn point; the `(server off by … m)` on the CLIENT line shows the gap.
