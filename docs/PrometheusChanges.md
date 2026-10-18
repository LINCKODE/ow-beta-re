# Prometheus: changes in this fork

This project uses [Prometheus](https://github.com/saturn-xvi/prometheus), the injected DLL and ImGui overlay for
the Overwatch beta 0.8 client (build 24919), as its live reverse-engineering tool. The copy in `prometheus/` is
upstream commit `7fb40f5` ("Merge branch 'artpop2'", 2026-05-15) plus the changes listed here. Upstream is licensed
under the **GNU AGPL-3.0** (`prometheus/LICENSE`), so these changes are AGPL-3.0 as well.

At a glance, the fork adds:

- an **MCP server** inside the DLL (HTTP on `127.0.0.1:17888`, 44 tools) so scripts and AI agents can inspect and
  drive the live client (§2);
- **tracers** for the ECS and for Statescript execution, and **logical-button injection** for repeatable
  recordings (§3);
- **world- and screen-space debug drawing** through the engine's own renderer, and a **live server view** that
  overlays our game server's state on the client's (§4);
- hooks that let the client talk to **our own login / lobby / game servers**, a **custom JAM protocol**
  (`PrometheusLobby`) that lets the lobby server call Prometheus tools, and JAM / protobuf research windows (§5);
- corrected **Statescript / JAM / entity-admin structures** from reverse engineering (§6);
- a **dedicated UI thread** with multi-viewport (pop-out) windows, a reworked **Statescript graph view**, and new
  windows (§7);
- a launcher with a **headless `--launch` mode** and an argument-passing fix, and build changes for the Linux
  llvm-mingw cross build (§1).

Status tags as in the rest of the docs: statements without a tag were read from the code; **[?]** marks what is
inferred. Addresses are RVAs (`gameBase + RVA`; Ghidra address = `0x7ff64ffe0000 + RVA`).

---

## 1. Build and run

### 1.1 What was already upstream

Upstream already ships a MinGW build: the `Release-MinGW` / `Debug-MinGW` presets in `CMakePresets.json`,
`CMake/Platform/MinGW.cmake` (clang flags, `-fms-extensions`, static runtime) and the `vcpkg-mingw/` manifest
(adds `libbacktrace`), plus a CI workflow. This fork uses that path unchanged as the primary build, on Linux.

### 1.2 Changes in the tree

| file | change |
|---|---|
| `vcpkg.json`, `vcpkg-mingw/vcpkg.json` | `cpp-httplib` added (the MCP server, the server bridge, debug-draw polling) |
| `Prometheus-Core/CMakeLists.txt` | the new sources (`MCP/`, `Network/`, `Systems/DebugDraw`, `Systems/ServerView`, `Utility/SafeMemory`); `find_package(httplib)`; links `ws2_32` |
| `MCP/McpServer.cpp` | cpp-httplib needs Windows 8.1+ APIs while the project builds with `WINVER=0x0601`; it raises the target for that file, and declares `GetAddrInfoExCancel`, which llvm-mingw's `ws2tcpip.h` lacks (only used on a client-side async-resolve path the server never hits) |
| `MCP/McpTools.cpp` | `<Windows.h>` → `<windows.h>` (case-sensitive headers on a Linux host) |
| `Systems/SystemBase.h`, `entity_admin.{h,cpp}` | ABI fix for systems Prometheus registers with the game: `get_inheritance` is now virtual (as a non-virtual it took no vtable slot, so every later slot was off by one [?]) and `deallocate` is the deleting-destructor slot `void* deallocate(char)` returning `nullptr`, so the game does not free a system that was allocated with Prometheus' own `new` |

### 1.3 Building on Linux (scripts outside the Prometheus tree)

The top-level `build.fish` / `build.sh` configure once with `cmake --preset <Release|Debug>-MinGW` and build the
`Prometheus-Launcher` target (which pulls in `PrometheusCore.dll`). They source a prerequisites environment
(`prometheus-prerequisites/env.{fish,sh}`, next to the repo) that mirrors the `environment` block of upstream's
`flake.nix` without nix:

- an **llvm-mingw** release (UCRT, x86_64) first on `PATH`, `CC/CXX/RC` = `x86_64-w64-mingw32-clang/clang++/windres`;
  it must come before `/usr/bin`, since vcpkg's mingw toolchain looks up `x86_64-w64-mingw32-gcc` and a
  distribution's GCC mingw-w64 provides the same names;
- `VCPKG_ROOT` = the `external/vcpkg` submodule, metrics off;
- a **no-op `pwsh`** stand-in: vcpkg only uses PowerShell to run `applocal.ps1` (copying a port's DLLs next to its
  tools), which does nothing with the static mingw triplet, but vcpkg refuses to run without it.

### 1.4 Running under Proton

`run.fish` / `run.sh` start `Prometheus.exe` from `output/<config>/bin` through **umu + GE-Proton** (DXVK; plain
Wine's wined3d crashes the client). `WINEPREFIX`, `PROTONPATH` and `UMU_RUN` can be overridden from the
environment. With `-- <game args>` they run the launcher headless (`--launch`, §7.4) and pass the arguments to the
game, e.g. `-- --BNetServer=127.0.0.1:1119 --lobbyServer=127.0.0.1:3724` to point it at our servers.

### 1.5 Pitfall: `__try/__except` and `IsBadReadPtr`

With llvm-mingw (clang targeting `x86_64-w64-mingw32`), `__try/__except` **compiles to nothing**: no handler is
emitted and the `__except` branch is dropped. Under Wine, `IsBadReadPtr` also touches the pages it checks, and a
guard page (e.g. another thread's stack guard) escapes it and kills or hangs the thread. Code that probes game
memory therefore must not rely on either.

`Utility/SafeMemory.{h,cpp}` (new) is the replacement:

- `IsReadable(p, n, &why)` / `ReadableSpan(p, max)`: check pages with `VirtualQuery` (touches nothing): committed,
  readable, not guard / no-access; `why` names the first bad page and its state / protection.
- `Read(dst, src, n, &why)`: checks, then copies with a tiny assembly leaf routine (`prom_probe_copy`,
  `rep movsb`, no stack use). If the page goes away between check and copy, the fault is recovered by
  `RecoverFault`, which the Prometheus vectored exception handler (`Utility/Exception.cpp`) now calls first: it
  redirects the faulting instruction to the routine's failure return.
- `Utility/Exception.cpp` also flushes the core log on a fatal exception (the logger flushes once a second, so the
  crash report used to be lost).

All MCP memory readers go through it (`bad_read` / `read_bytes` in `McpTools.cpp`), so they are safe on any
address and return `{"error": "unreadable: <page, state, protect>"}`. The tools' `seh_guard` wrapper is kept but
catches nothing under llvm-mingw; a fault inside **game code** called from a tool still crashes. Some new windows
(`console_debug.h`, `menu_explorer.h`, `jam_schema_dumper.h`, `statescript_dumper.h`, `jamprotocol_window.h`) still
use `IsBadReadPtr` / `__try` and carry that risk.

---

## 2. The MCP server

### 2.1 Transport

- `MCP/McpServer.{h,cpp}`: a cpp-httplib server on **`127.0.0.1:17888`**, started on the first overlay frame.
  - `POST /mcp/tools/list`: names, descriptions, JSON input schemas;
  - `POST /mcp/tools/call` with `{"name": ..., "arguments": {...}}`;
  - `GET /mcp/health`: running state, counters, whether writes are enabled.
- Threading: HTTP worker threads queue the call and block on a promise (5 s timeout); the UI thread drains the
  queue once per frame (`McpServer::process_pending()`), runs the handler there and fulfils the promise. Handlers
  run on the thread that owns the overlay and are expected to be short.
- `bridge_mcp_prometheus.py` (repo root): a stdio MCP bridge (`pip install mcp requests`). It reads the tool list
  from `/mcp/tools/list` at session start, so the game must be running for the tools to appear; `PROMETHEUS_MCP_URL`
  and `PROMETHEUS_MCP_TIMEOUT` override the defaults.
- Window **Tools › MCP Server** (`UI/windows/mcp_server_window.h`): status, request counters, queue depth, the tool
  list with descriptions, and the **write toggle** (off at startup, `prom_mcp::g_write_enabled`; greyed out while
  writes are compiled out, §2.3).

### 2.2 Tools

44 tools, defined in `MCP/McpTools.cpp` (`all_tools()`). Classification:

- **R**: reads only;
- **G**: a write tool: changes game memory or state, runs game code, injects input or resources, or writes files.
  Refused centrally (§2.3): always in this release, because writes are compiled out;
- **O**: changes only Prometheus' own state or the overlay (in-memory names, debug shapes), not gated;
- **T**: installs hooks for tracing / drawing (recording is otherwise passive);
- **N**: also talks to our game server over HTTP.

| tool | class | purpose |
|---|---|---|
| `read_memory` | R | N bytes (≤ 4096) at `gameBase + rva`, hex |
| `read_memory_abs` | R | N bytes (≤ 4096) at an absolute address |
| `read_string` | R | NUL-terminated string at `gameBase + rva` |
| `read_pointer_chain` | R | walk base / `base_rva` through a list of offsets |
| `resolve_hash` | R | CRC32 → name from the hash library |
| `add_hash` | O | add a hash → name entry to the in-memory hash library (Prometheus state only) |
| `list_loaded_scripts` | R | Statescript instances on the game or lobby entity admin |
| `dump_script` | R | full node dump of a loaded instance: nodes, flags, plugs and links, config vars with live values |
| `lookup_stu_type` | R | STU type by hash: name, fields with offsets |
| `game_state` | R | snapshot: bases, entity-admin presence, script counts |
| `call_function` | G | call `gameBase + rva` with up to 8 u64 arguments |
| `call_function_void` | G | same dispatch, void intent |
| `write_memory` | G | write hex bytes at `gameBase + rva` |
| `inject_resource` | G | create a resource from a file under a GUID (CreateFromMemory), e.g. edited map data before the map loads; keeps a reference |
| `execute_node` | G | execute a Statescript node by index (EA singleton or an entity's own 0x23 component) |
| `start_stop_state` | G | start / stop a state node |
| `play_script` | G | instantiate a graph by resource id on an entity admin |
| `list_entities` | R | entities of an EA (id, definition, components), filter by component, paginated |
| `get_entity` | R | one entity: components, parent |
| `find_entity` | R | special entities (`local_player`) |
| `read_component` | R | raw bytes of a component |
| `list_systems` | R | the EA's systems in tick order (instance, vtable RVA) |
| `ecs_dump` | R | full ECS snapshot: counters, queues, systems and subscriptions, per component type creator / deps / count |
| `ecs_trace` | T; G for `save` | ECS event tracer (§3.1); `save` writes a file at a caller-given path |
| `ss_trace` | T; G for `save` | Statescript execution tracer (§3.2); `save` writes a file at a caller-given path |
| `logical_button` | G for `press`/`release`/`tap` | press / release / tap logical buttons without the keyboard (§3.3) |
| `debug_draw` | O, N | world / screen debug shapes, HUD text, the engine's built-in overlays, polling the server's shapes (§4.1) |
| `entity_statescripts` | R | graphs running on one entity's 0x23 component |
| `list_stu_resources` | R | loaded STU resources, filter by type, paginated |
| `read_stu_object` | R | reflect a loaded STU object by resource id (fields, references, nested objects) |
| `graph_sync_info` | R | for an entity definition or graph: the graphs and their sync layout (sync vars, remote sync nodes) as the client's full-update decoder counts them |
| `ss_serialize_local` | R | serialize an entity's live Statescript state as a full / delta update payload with the client's own writers; optional baseline store / diff (Prometheus state) |
| `execute_node_server` | G, N | run a node / state change, capture the entity a few frames later and send it to the game server as an authoritative full update |
| `hero_attach_weapons` | G | the player spawner's weapon-script setup on an existing hero (e.g. one our server spawned) |
| `server_echo` | G, N | echo mode: the server keeps confirming the entity's Statescript state (full update, then deltas) |
| `custom_jam_status` | R | state of the `PrometheusLobby` protocol (§5.3) |
| `server_view` | R | the live server view snapshot and stats (§4.2); `action=draw/reset` toggles Prometheus state |
| `server_bridge_status` | R | outcome of the last server-bridge capture |
| `search_memory` | R | scan committed readable memory for a byte pattern (stalls the game briefly) |
| `read_stu_at` | R | reflect an STU object at an absolute address |
| `list_viewmodels` | R | Statescript-controlled UI view models, filter, paginated |
| `read_viewmodel` | R | a view model's properties |
| `write_viewmodel` | G | set a view-model property's 64-bit value |
| `construct_component` | G | spawn an entity through the engine create path from a definition (optionally with extra components) or a bare one-component entity; `dry_run` builds without spawning |

The tool descriptions (shown in the MCP Server window and returned by `/mcp/tools/list`) document every argument.

### 2.3 MCP write gating (this release)

**Writes are compiled out.** `MCP/McpTools.h` defines `PROMETHEUS_MCP_NO_WRITES`. With it defined:

- every **G** call above is refused, whoever makes it (the HTTP API, the `PrometheusLobby` channel, or another
  module through `find_tool`). `all_tools()` wraps each handler with `gate_writes()`, which checks
  `is_write_call(tool, args)`, and the per-tool checks use `writes_allowed()`. The refusal is an error result naming
  the define;
- the write toggle in *Tools › MCP Server* is shown **greyed out** ("compiled out") and can't be switched on;
- `GET /mcp/health` reports `"writes_enabled": false, "writes_compiled_out": true`.

What still works: every read tool, the tracers (except `save`; use `fetch`), debug drawing, the live server view,
and `logical_button list/status`. `construct_component` is refused even with `dry_run`.

**Why.** The MCP server listens on `127.0.0.1:17888` with no authentication, and the `PrometheusLobby` JAM channel
(§5.3) lets the lobby server you are connected to call any MCP tool. With writes on, any local process, or the
operator of that lobby server, can run arbitrary code in your game process (`call_function`), rewrite memory and
write files to any path (`ecs_trace`/`ss_trace` `save`).

**Re-enabling writes is not advised.** The reverse-engineering workflow in the docs (button recordings, server
echo, node execution, trace files) needs them, so they can be turned back on. Do it only if you fully understand
and accept the risks above:

1. remove `#define PROMETHEUS_MCP_NO_WRITES` from `Prometheus-Core/MCP/McpTools.h` and rebuild;
2. writes are then still **off at startup**. Turn them on per session with the toggle in *Tools › MCP Server*, and
   off again when you are done;
3. connect only to login / lobby servers you run yourself, and never expose port 17888 (or forward it) to other
   machines.

---

## 3. Tracers and input

### 3.1 ECS tracer (`MCP/EcsTrace.{h,cpp}`, tool `ecs_trace`)

Records entity lifecycle calls (spawn, inline spawn, add component, delete request, destroy, flushes) and every
per-system entity callback (`on_created` / `on_added` / `on_deleting` / `on_removed`) with the tick and phase they
happened in, including which system's `OnTick` caused them. Clock events (dilate, advance, CF range) are always
recorded; options add every EA tick, every command-frame listener call, or parent snapshots around each system
tick. Engine functions are hooked with MinHook on first start (then kept, recording gated by a flag); system
callbacks are traced by giving each system instance a **private copy of its vtable**, so the shared `.rdata`
vtables stay untouched (`original_vtable()` gives the game's one back). `save` writes JSON lines straight to a
file. Used to record the golden fixtures that `systems/ecs` is replayed against (docs/ECS.md).

### 3.2 Statescript tracer (`MCP/SsTrace.{h,cpp}`, tool `ss_trace`)

For selected graphs, entities, or everything: the sync manager's command-frame steps, every notification executed
(type, time, decoded payload), state begin / reenter / finish / abort, link traversals (source node, plug,
target), instantiation, game-message delivery and the variables that changed in each command frame. Every event
carries CF, sync-manager milliseconds, entity and instance. Hooks run on the game thread inside the Statescript
update. See docs/StatescriptRuntime.md §4.

### 3.3 Logical buttons (`MCP/LogicalButton.{h,cpp}`, tool `logical_button`)

Presses logical buttons (`fire`, `ability_1`, `reload`, ... or an id) without the keyboard, for repeatable
recordings (docs/StatescriptRuntime.md §4.1.1):

- `mode=input` (default), the real input path: the "is logical button held" query (`0xcec330`) is replaced and
  answers true for injected buttons, and the local player input system's tick (`0xc9df40`) samples even with the
  game window unfocused. The command's button mask, action code and movement axes go to the server, and the
  hero's Statescript gets its LogicalButton notifications as for a key. The replacement never calls the original:
  the original checks that its caller is inside the game's code and would fault on the detour.
- `mode=notify`: the Fake LogicalButton window's path: queue LogicalButton notifications directly on one entity's
  Statescript instances (local only, any id).

---

## 4. Debug drawing and the live server view

### 4.1 Debug drawing (`Systems/DebugDraw.{h,cpp}`, window *Tools › Debug Draw*, tool `debug_draw`)

Full description: docs/DebugDrawing.md. In short:

- **World space**: spheres, boxes (with yaw), capsules, cylinders and lines in metres (y up), drawn by the engine's
  own immediate-mode renderer **PrimDraw**, depth-tested in the game camera. Shapes are replayed every frame from a
  hook on the game's frame submit (between the render manager's BeginFrame and EndFrame), since PrimDraw shapes
  live one frame. Groups with visibility, TTLs, translucency.
- **Screen space**: HUD text lines in the engine's debug font (`DebugPrintV`) and 2D rects / polylines, drawn
  inside the engine's debug-overlay pass through an overlay object Prometheus adds to the engine's overlay list.
  `DebugPrintV` checks its return address (it divides by zero when called from outside the game's code), so the
  install patches that failure path, as upstream's anti-debug section does for other such checks.
- The engine's 10 built-in overlays (fps, worker threads, time scale, memory, input lag, ...) can be listed and
  switched.
- Sources: local shapes (tool, window) and the game server's shapes, polled from the instance server's control API
  (`GET /debug/draw`, port 3729).

### 4.2 Live server view (`Systems/ServerView.{h,cpp}`, window *Tools › Server View*, tool `server_view`)

The instance server's view of the match, one frame per server tick, relayed by the lobby logic as
`PrometheusServerView` (message 32002 on the `PrometheusLobby` protocol) after `!serverview on` in the lobby chat.
Prometheus keeps the latest frame, a short history and timing stats (rate, latency split instance → lobby logic →
client, drops, coalescing, sizes), compares the server's state with the client's own every UI frame (hero position
error in metres, command frame, entity count) and re-submits the world shapes as debug-draw group `view`.
docs/DebugDrawing.md §5, docs/InstanceServer.md.

---

## 5. Network

### 5.1 Hooks for our own servers (`Network/`, installed from `StartHook`)

| file | target (RVA) | what |
|---|---|---|
| `BnetTlsBypass` | `0x10FFDA0`, the Battle.net SDK's certificate verification | runs the original check, logs the result (with a readable reason: not a hostname, no certificate, CN mismatch, pinning, chain without the Battle.net CA) and returns 0 = trusted, so the client accepts our local login server |
| `LobbyBlobBypass` | `0x1359B10`, the RSA public transform on the lobby state blob | skips the transform so a blob carrying only the HMAC is accepted (its only caller is the lobby handshake) |
| `JamDiagnostics` | `0xF288F0` JamLink::Close, `0xF29510` JamLink notify, `TerminateProcess` / `ExitProcess` | logs every JAM link close and event (state, reason) and every self-termination with the call stack as game RVAs, and flushes the log; also tells `CustomJam` to forget a closed link |

These are only useful against a local server; they could sit behind a "local server mode" setting before going
upstream.

### 5.2 Server bridge (`Network/ServerBridge.{h,cpp}`)

Development bridge to our game server's control API (`127.0.0.1:3728`, `POST /scripts`). The client only predicts
what a Statescript node does and rolls it back when the server does not confirm it; the bridge captures the
entity's state a couple of frames after a local change (`ss_serialize_local`) and has the server send it back as an
authoritative full update, so it sticks. **Echo mode** keeps doing that for one entity (full update, then deltas
stamped with the client's own frame), a stand-in for server-side simulation. Used by the graph view's "+ server"
buttons, `execute_node_server` and `server_echo`.

### 5.3 Custom JAM protocol `PrometheusLobby` (`Network/CustomJam.{h,cpp}`)

After the client registers its stock lobby protocols (hook on `0xC66560`), the DLL registers its own protocol on
the same route config: a pool implemented in Prometheus, one receiver and the route entry. The client then offers
the CRC in its negotiation and the lobby server gets a channel for it.

- name `PrometheusLobby`, CRC `0xF4456D46` (top nibble `F` so it sorts after every stock protocol: channel 33);
  message IDs 32000..32015;
- `32000 PrometheusCall` (server → client) `{u32 seq, str tool, str args}`;
  `32001 PrometheusResult` (client → server) `{u32 seq, bool ok, str result}`;
  `32002 PrometheusServerView` (server view frames, §4.2);
- a call runs on the UI thread: any MCP tool, plus `notify` (text drawn on top of the game), `ping`, `tools`.
  Write calls go through the same gate as the HTTP API (§2.3): in this release the lobby server can't run any
  write tool.

Engine strings in the messages use fixed Prometheus-owned buffers, so the decoder never touches the game heap.
Full layout and the lobby side: docs/JAM.md §13, docs/LobbyServer.md.

### 5.4 JAM research (`jamprotocol_window.h`, `JAM.h`, new windows)

- `JamMessageInfo` field offsets documented; message names are stripped in this build except for a few pools.
- Hooks on the JamLink tick (`0xF28590`) and the network manager tick (`0xF22340`) capture `g_jamLink` and
  `g_networkManager`; if no link was seen yet, the manager's JamLink tree is walked to find one.
- The `associatePool` hook also keeps every receiver object (`poolReceivers`, refcount bumped), and `JAM.h` provides
  a fake handler buffer, so messages can be dispatched into the client's receivers without a link.
- Jam Protocol window: **Dump all to JSON** (`jam_protocol_dump.h` → `Prometheus/jam_protocols.json`: every captured
  protocol with messages and field layouts, encodings as the client's binary decoder reads them). Protocols
  registered before the hook is installed are not captured.
- Message detail window: type labels corrected to the decoder (12 CSTRING, 13 FIXED BLOB, 15 LEN-PREFIXED,
  16 TAGGED ID, ...).
- New windows: *Jam › JAM Message Injector* (build a message from the pool's field tables and dispatch it through
  the pool dispatch `0xF23680`), *Jam › JAM Protocol Analyzer* (schema dump / export).

### 5.5 Protobuf descriptors (`proto_capture.h`, `proto_decoder.h`, *Networking › Proto Viewer*)

A hook on protobuf's `InternalAddGeneratedFile` (`0xE8C8C0`) records every FileDescriptorProto the game registers
at startup (~600 `.proto` files) [?count from the code comment]; a self-contained wire-format decoder turns them
back into messages / enums / services for browsing and export.

---

## 6. Structure fixes

### 6.1 Statescript (`Statescript.h`, `StatescriptVar.h`, `Statescript.cpp`, `statescript_logger.h`)

Reverse-engineered corrections, cross-referenced to docs/StatescriptRuntime.md:

- `StatescriptRTTI`: `record` (points at the inline factory record), `priority_layer` (0..2, highest wins in the
  registry), `source_anchor` (per source file [?]).
- `RID_User` ("user" of a variable): instance id, writing state index, priority bits (0-6 write priority, bit 7
  state-defined value), with `priority()` / `state_defined()`.
- `StatescriptState`: vbtable at +0x08, `state_flags` bits, `serial` (+0x44), `begin_time` (+0x48), `m_uniqueID`,
  `client_only`, and the two virtual-base interface sub-objects (+0xA8 / +0xB0 only in the base class).
- `STUStatescriptBase`: `idx_in_remote_sync_nodes` (+0x6C), `ea_scope` (+0x70) (was the wrong `idx_in_states`).
  `STUStatescriptState`: `m_stateGroup` / `m_transitionPlug` offsets fixed (+0x78 / +0x88), `idx_in_states` (+0xF8).
- `StatescriptInstance` (0x290 bytes): new `StatescriptInstanceFlags` enum for +0x258 (instantiated, mirrored,
  server apply, predicted, runs locally, ...), parent state index / serial, cached parent, depth, timestamps,
  per-state flag words (+0xB8), the notification queue head / tail / count, graph handle, `entry_fired`, the
  embedded hot-reload listener, `creation_kind`. Removed the duplicate `script_flags` / `execution_flags` (both at
  +0x258) and the misplaced `second_inst_varbag`; `Stop()` no longer clears a flag bit itself.
- `StatescriptSyncMgr`: nine list slots (+0x10..+0xE7), and the clock fields named: `current_cf` (+0x2C4),
  `step_window_len`, `time_ms` (+0x2D0), `step_window_end` (+0x2D8), `ms_per_cf` (+0x360, 16). Callers moved from
  the old `cf_timestamp` / `increasing_counter3` names.
- `StatescriptVar`: special type `0x...06` is `INSTANCE_REFERENCE` (dereference an instance-reference variable), not
  "this varbag"; `StatescriptVar_Primitive::value` alias with a note that real global variables live in
  component 0x51.

### 6.2 Entity admin and player spawner (`entity_admin.{h,cpp}`, `player_spawner.{h,cpp}`)

- `PrometheusSystem` keeps **entity ids** instead of pointers for the local controllers and the newly added
  entities and re-resolves them every tick: `OnDeletion` is not reliably called, and a server-spawned local player
  that went away with the map left a dangling pointer that crashed `OnTick`. The health / Statescript lookups on
  the camera-attach entity are null-checked (a server-spawned player can point its camera at an entity without
  them).
- `player_spawner::attach_weapons` and `fixup_player_statescript` split out of `modify_ent` as static functions,
  usable on any hero entity (tool `hero_attach_weapons`).

---

## 7. UI and launcher

### 7.1 Dedicated UI thread and pop-out windows (`dllmain.cpp`, `window_manager.cpp`)

All ImGui work moved from the Present hook to a dedicated UI thread. The Present hook signals a frame and waits
for it, so DX11 immediate-context use stays serialized on one thread at a time (concurrent use made the NVIDIA
driver report "device removed", per the code comments). The game's WndProc only queues messages for the UI thread
and absorbs mouse / keyboard input based on the previous frame's `WantCapture*` flags. Both waits use
`MsgWaitForMultipleObjects` and pump messages, because ImGui's multi-viewport code sends cross-thread messages
(e.g. `WM_NCHITTEST`) that would otherwise deadlock. ImGui **multi-viewport** is enabled (set before the backends
initialize, or the platform callbacks stay null), and every window gets a **Pop out** button that moves it outside
the game into its own OS window. The UI thread also runs, once per frame: the MCP queue, the server bridge, the
custom JAM calls and the server view. DLL detach stops and joins the thread. How well pop-out windows behave under
Proton is not established [?].

### 7.2 Statescript graph view (`statescript_window.{h,cpp}`)

- **Zoom** (mouse wheel around the cursor; imnodes has none, so node positions, style and font are scaled), Alt +
  left drag pans, layouts are saved as at zoom 1.
- **Automatic layout**: a layered (Sugiyama) layout left to right: cycle removal, longest-path layering, dummy nodes
  for long edges, barycenter crossing reduction, placement.
- An **inspector** for the selected node: kind, whether it runs, its config-var arguments with live values and the
  variable paths of dynamic ones, links; Start / Stop state and Execute, each also as "+ server" (through the
  server bridge, §5.2), and the echo-mode checkbox.
- Menu entries for the instance var bag and the extra var bag (+0x280).

### 7.3 Other window changes

- New windows (registered in `window_regs.cpp`): *Tools › MCP Server*, *Tools › Debug Draw*, *Tools › Server View*,
  *Tools › Statescript Dumper* (every unique graph of every entity admin to JSON: per-script files, `_all.json`,
  `_index.json`), *Tools › Menu Explorer* (UI view models and screen-controlling scripts), *Debug › Console Debug*
  (the game's console command table and a command log), *Debug › CRC32 Discovery* (hooks the console-command CRC32
  function `0x7F88E0` and logs every string hashed), *Networking › Proto Viewer*, *Jam › JAM Message Injector*,
  *Jam › JAM Protocol Analyzer*.
- `statescript_info.h`: shows CF / time / step window and the instance flags by their new names.
- `statescript_registry.cpp`: columns for the renamed RTTI fields.
- `CVD_VarbagSelector.h`: uses the +0x280 bag (the old +0x270 field was removed).
- `statescript_fake_logicalbtn.h`: timestamps from `time_ms`.
- Management window: an **Entities** menu shortcut.

### 7.4 Launcher (`Prometheus-Launcher/main.cpp`)

- The command line given to `CreateProcessA` now starts with the quoted executable path. Before, the game took the
  first option as `argv[0]` and silently dropped it (a bug for every user).
- `Prometheus.exe --launch [-- <game args>]`: launch with the saved `launch_config.json` without the window (MD5 and
  DLL checks, exit code 0 / 1); arguments after `--` are quoted as needed and appended to the saved options, not
  persisted. `--help` prints the usage. The full command line is printed on launch; config loading moved into
  `load_config()`. Without arguments the GUI behaves as before.

---

## Appendix: files changed

### Modified

| file | summary |
|---|---|
| `vcpkg.json`, `vcpkg-mingw/vcpkg.json` | add `cpp-httplib` |
| `Prometheus-Core/CMakeLists.txt` | new sources, httplib, `ws2_32` |
| `Prometheus-Core/dllmain.cpp` | dedicated UI thread + multi-viewport, MCP server start, per-frame processing, network hooks, protobuf capture hook, debug-draw hook |
| `Prometheus-Core/JAM.h` | `JamMessageInfo` offsets, receiver capture, fake handler, `g_jamLink` / `g_networkManager` |
| `Prometheus-Core/Statescript.h` | RTTI, `RID_User`, state, STU node, instance (flags enum), sync manager clock fields |
| `Prometheus-Core/Statescript.cpp` | renamed clock fields |
| `Prometheus-Core/StatescriptVar.h` | `INSTANCE_REFERENCE`, `value` alias |
| `Prometheus-Core/statescript_logger.h` | renamed clock fields |
| `Prometheus-Core/Systems/SystemBase.h` | virtual `get_inheritance`, deleting-destructor slot |
| `Prometheus-Core/entity_admin.h`, `entity_admin.cpp` | id-based controller tracking, null checks, destructor slot |
| `Prometheus-Core/player_spawner.h`, `player_spawner.cpp` | `attach_weapons` / `fixup_player_statescript` split out |
| `Prometheus-Core/Utility/Exception.cpp` | SafeMemory fault recovery, log flush on fatal exceptions |
| `Prometheus-Core/UI/window_manager/window_manager.cpp` | Pop out button |
| `Prometheus-Core/UI/window_manager/window_regs.cpp` | registers the new windows |
| `Prometheus-Core/UI/window_manager/management_window.cpp` | Entities shortcut |
| `Prometheus-Core/UI/windows/statescript_window.h`, `.cpp` | zoom, layered layout, inspector, server buttons |
| `Prometheus-Core/UI/windows/statescript_info.h` | new field / flag names |
| `Prometheus-Core/UI/windows/statescript_registry.cpp` | renamed RTTI columns |
| `Prometheus-Core/UI/windows/statescript_fake_logicalbtn.h` | `time_ms` |
| `Prometheus-Core/UI/windows/CVD_VarbagSelector.h` | +0x280 bag |
| `Prometheus-Core/UI/windows/jamprotocol_window.h` | JamLink / manager tick hooks, receiver capture, JSON dump button |
| `Prometheus-Core/UI/windows/jam_messagedetail_window.h` | decoder type labels |
| `Prometheus-Launcher/main.cpp` | argv[0] fix, `--launch`, `--` game arguments |

### New

| path | what |
|---|---|
| `bridge_mcp_prometheus.py` | stdio MCP bridge to the in-DLL HTTP server |
| `Prometheus-Core/MCP/McpServer.{h,cpp}` | HTTP server, UI-thread call queue |
| `Prometheus-Core/MCP/McpTools.{h,cpp}` | the 44 tools, write toggle, write gating (`PROMETHEUS_MCP_NO_WRITES`, §2.3) |
| `Prometheus-Core/MCP/EcsTrace.{h,cpp}` | ECS tracer |
| `Prometheus-Core/MCP/SsTrace.{h,cpp}` | Statescript tracer |
| `Prometheus-Core/MCP/LogicalButton.{h,cpp}` | logical-button injection |
| `Prometheus-Core/Network/BnetTlsBypass.{h,cpp}` | accept the local login server's TLS certificate |
| `Prometheus-Core/Network/LobbyBlobBypass.{h,cpp}` | skip the RSA transform of the lobby state blob |
| `Prometheus-Core/Network/JamDiagnostics.{h,cpp}` | JAM link close / event and process-exit logging |
| `Prometheus-Core/Network/ServerBridge.{h,cpp}` | Statescript captures and echo mode to the game server |
| `Prometheus-Core/Network/CustomJam.{h,cpp}` | `PrometheusLobby` protocol, notify overlay |
| `Prometheus-Core/Systems/DebugDraw.{h,cpp}` | PrimDraw world shapes, screen overlay, built-in overlays, server polling |
| `Prometheus-Core/Systems/ServerView.{h,cpp}` | live server view |
| `Prometheus-Core/Utility/SafeMemory.{h,cpp}` | fault-safe memory reads |
| `Prometheus-Core/proto_capture.h`, `proto_decoder.h` | protobuf descriptor capture and decoder |
| `Prometheus-Core/UI/windows/mcp_server_window.h` | MCP Server window |
| `Prometheus-Core/UI/windows/debug_draw_window.h` | Debug Draw window |
| `Prometheus-Core/UI/windows/server_view_window.h` | Server View window |
| `Prometheus-Core/UI/windows/statescript_dumper.h` | Statescript Dumper window |
| `Prometheus-Core/UI/windows/menu_explorer.h` | Menu Explorer window |
| `Prometheus-Core/UI/windows/console_debug.h` | Console Debug window |
| `Prometheus-Core/UI/windows/crc32_discovery.h` | CRC32 Discovery window |
| `Prometheus-Core/UI/windows/proto_viewer_window.h` | Proto Viewer window |
| `Prometheus-Core/UI/windows/jam_injector_window.h` | JAM Message Injector window |
| `Prometheus-Core/UI/windows/jam_schema_dumper.h` | JAM Protocol Analyzer window |
| `Prometheus-Core/UI/windows/jam_protocol_dump.h` | JAM protocol JSON dump |

Outside the Prometheus tree: `build.{fish,sh}`, `run.{fish,sh}`, `build-and-run*.{fish,sh}` and the
`prometheus-prerequisites/` environment (§1.3, §1.4).
