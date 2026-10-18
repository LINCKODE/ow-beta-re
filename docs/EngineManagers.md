# Engine managers

The lowest layer of the Overwatch beta 0.8 client (build 24919): a fixed table of engine "managers"
(memory, threads, storage, resources, rendering, …) created and started before any entity admin. The
Prometheus README summarises it as "They handle things such as Dataflow, CASC, Window management, etc."
This document is what we actually know about them.

Related: [ResourceLoading.md](ResourceLoading.md) (the Resource and Asset Package managers in detail),
[ECS.md](ECS.md) (what runs on top).

**Addresses**: RVA (Prometheus: `gameBase + RVA`, runtime base `0x140000000`); Ghidra = `0x7ff64ffe0000 + RVA`.
**Status**: [H] code + confirmed live, [M] code only, [L] inferred.

---

## 1. The manager table  [H]

| What | RVA |
|---|---|
| `Mgr* g_mgr[41]`: manager instances | `0x181e340` |
| `void (*g_mgrFactory[41])()`: factories | `0x181e490` |
| `IAllocator* g_alloc`: the engine allocator (vfunc +0x30 alloc(size, align), +0x40 free) | `0x181ee50` |
| engine run state (1 running, 5 in game, 9 shutting down, 10 dead) | `0x18582ec` |

`WinMain` (RVA `0x5a4dc0`) fills the factory table with fixed functions and sets two slots directly
(19 and 40). Factory `i` sets `g_mgr[i]` to a static object (heap-allocated for slots 20, 30, 33), so
**slot index = factory index**. Live: 35 of the 41 slots are populated.

## 2. Lifecycle  [H]

1. **EngineInit** (`0x7d8e30`, from WinMain): parses the command line and config, runs factories 0 and 1
   and `RegisterAndStartMgr(i)` (`0x7d8ca0`) for each: vfunc +0x08 `Register()`, then +0x10 `Start()`
   (errors: "Error: Mgr %d failed to register" / "…failed to start").
2. **EngineBoot** (`0x7dac40`) → **StartAllMgrs** (`0x7dabb0`): runs factories 2..40, then
   `RegisterAndStartMgr(i)` for i = 2..40 **in index order**. The start order is the table order.
3. `BroadcastMsg(0, 0)` (vfunc +0x48 on every manager, forward), then the main loop. Each frame managers get
   their update call (+0x20).
4. **EngineShutdown** (`0x7debd0`): state 9; reverse broadcast (+0x48, args (1, 0)); +0x30 pre-shutdown on
   0..40; then in reverse 40..0: +0x40 shutdown and +0x58 `ShouldDelete()` (true → destructor + free via
   `g_alloc`). While shutting down, +0x38 `IsIdle(printReason)` prints the "X Manager is waiting…" lines.

## 3. Manager interface (vtable)  [H up to +0x48, M after]

| offset | meaning |
|---|---|
| +0x00 | scalar deleting destructor (base vtable RVA `0x157f040`) |
| +0x08 | `int Register()` (default returns 0) |
| +0x10 | `int Start()` (typically sets a state 1 → 2) |
| +0x18 | ? |
| +0x20 | per-frame update |
| +0x28 | ? |
| +0x30 | pre-shutdown |
| +0x38 | `bool IsIdle(…, bool printWaitReason)` |
| +0x40 | shutdown |
| +0x48 | `bool OnMessage(int msg, arg)` (broadcasts) |
| +0x50 | ? (render: "loading screen active", used to pace resource unloading) |
| +0x58 | `bool ShouldDelete()` |

## 4. The slots

Identified from their Start/IsIdle strings (confidence per row). Everything else is still unknown.

| # | Manager | Evidence |
|---|---|---|
| 0 | **Memory** | Start creates `g_alloc` [H] |
| 1 | **Thread** | "Thread Manager is still waiting for jobs…" [H] |
| 2 | placeholder (no-op) | [L] |
| 3 | **Storage** (loose files; not the asset path, see ResourceLoading §2) | "Storage Manager is waiting for sources…" [H] |
| 4 | **Log / output** | channels "blackbox", "console", "Tank_%u.log" [M] |
| 6 | **Settings / CVars** | "Settings_v0", walks a registered list [M] |
| 7–9, 17, 37, 39 | empty | [H] |
| 10 | **Network sockets** | winsock init, gethostname/getaddrinfo [M] |
| 11 | **statsd / telemetry** | "statsd" channel, "overwatch" prefix [M] |
| 12 | ? tools/connection ("%s (TANK)", port 13000) | [L] |
| 13 | **Resource** (asset loading via TACT/CASC) | see ResourceLoading.md [H] |
| 14 | **Asset Package (APM)** | loads the `.apm` manifests [H] |
| 16 | ? registers load callbacks for types 0x01B (graphs) and 0x003 (entity defs) | [L] |
| 18 | ? locale / text | [L] |
| 19 | ? registers a load callback for type 0x083 | [L] |
| 20 | **Render** (heap, 0x2ff0 bytes; also the broadcast object at `0x181e3e0`) | "Render Manager is waiting…" [H] |
| 23 | **Animation** | registers `anim_*` cvars [M] |
| 24 | **Font** | FreeType-style library init [L–M] |
| 27 | **Scene** | "Scene Manager is exiting with scenes…" [H] |
| 31 | **Data Flow** | "Data Flow Manager is waiting for %d pooled…" [H] |
| 40 | **Battle.net client** | "Overwatch", "0.8.0.0 Beta", own thread [M] |
| 5, 15, 21, 22, 25, 26, 28–30, 32–36, 38 | unknown (33 is a 0x1798-byte heap object, possibly sound) | – |

The Wwise sound engine, input and window handling are not tied to a slot yet. The Enlighten "update
managers" in the strings belong to the renderer (3rd-party).

## Open questions
- Identities of the unknown slots, in particular Sound (Wwise), Input and Window (check each vtable's
  Start/IsIdle strings and the callers of the `AK::` init functions).
- vtable slots +0x18, +0x28, +0x50.
