# Command frames, the client clock and time dilation

How the Overwatch beta 0.8 client (build 24919) turns render frames into fixed 16 ms **command frames
(CF)**, how the server steers that clock, and what a server has to send. Companion to
[ECS.md](ECS.md) §6/§8 (tick structure) and the netcode notes in memory ("netcode-clock-findings").

**Addresses**: Ghidra = `0x7ff64ffe0000 + RVA`; runtime = `0x140000000 + RVA`.
**Status**: [G] read in Ghidra, [L] verified against the live client (fixtures in `systems/ecs/fixtures/`),
[?] inferred. Constants were read from the binary.

---

## 1. Two rates

| | rate | driven by |
|---|---|---|
| `EntityAdmin::Tick` → every system's `OnTick(dt)` | once per **render frame** (variable; hundreds of fps on menus) | frame loop `FUN_7ff650bdf460` |
| command frames → every CF listener's `OnCommandFrame(cf, flags)` | **62.5 Hz**, 16 000 µs each, 0..n per render frame | `GameClockSystem` + `CommandFrameSystem` |

Gameplay simulation that must agree with the server (movement, projectiles, weapons, Statescript
updates) runs in command frames. Everything else (rendering, UI, audio, interpolation) runs per render
frame. A render frame with less than 16 ms of accumulated time runs no CF; a long frame runs several.

## 2. The clock  [G, L]

Every game-type EA owns a timing object at `EA+0x218`; the **netcode clock** is at `timing+0x50`
(accessors `FUN_7ff650be3be0` → timing, `FUN_7ff650bfdf50` → `+0x50`).

| offset | type | meaning |
|---|---|---|
| clock+0x00 | i64 | accumulator: µs not yet turned into a CF |
| clock+0x08 | i64 | µs turned into CFs this render frame |
| clock+0x10 | u32 | **current CF** (last one produced) |
| clock+0x20/+0x28 | u32[]/int | **pending CFs** of this render frame (consecutive), count |
| clock+0x80 | f32 | smoothed lead (s) [?] |
| clock+0x84 | f32 | smoothed buffer (s) [?] |
| clock+0x88 | f32 | **safety buffer** (s): 0.052 … 0.104 (0.2 max from jitter) |
| clock+0x94 | u32 | **P**: last server frame received |
| clock+0x98 | u32 | **lead** = max(0, current CF − P) |
| clock+0xa8 | u8 | **dilation flags** (from the server, §4) |
| timing+0x20/+0x28/+0x30/+0x38 | | last dt, total seconds (f64), scaled time, render-frame counter |

### Per render frame: `GameClockSystem` (game EA #1 / session #0, vtable `0x15c4be8`, OnTick `FUN_7ff650c7d4b0`)
1. Store dt, add it to the total, count the frame.
2. **Dilate** (`FUN_7ff650ce63a0`): `µs = (int64)(dt × 1e6f)`, then with the flags at clock+0xa8:
   - bit 0 (`0x1`, slow down): `µs −= 200` (−1000 if bit 3 `0x8` is also set)
   - else bit 1 (`0x2`, speed up): `µs += 200` (+1000 with bit 3)

   It also smooths the lead (`clock+0x98 × 16000 µs`) and the buffer (`+0x88`) into +0x80/+0x84 with a
   rate of 2.5 (`FUN_7ff650c5f420`, `DAT_…160cf88`) [?: used by the input/timing report, see §5].
3. **Advance** (`FUN_7ff65100e950`):
   ```
   pending.clear(); simulated = 0
   step = clamp(µs, 0, 250000)          // at most 250 ms per render frame
   acc += step
   while acc >= 16000: acc -= 16000; simulated += 16000; pending.push(++cf)
   ```
[L] Both formulas reproduce every tick of `trace_command_frames.json` (351 ticks per EA, e.g.
acc 14423 + 81135 µs → CFs 0x2c1..0x2c5, 15558 µs left).

So one real millisecond is one simulated millisecond; dilation adds or removes a fixed 0.2 ms (or 1 ms)
per **render frame**, i.e. its strength depends on the client's frame rate (at 60 fps, ±200 µs/frame =
±1.25 % speed; the strong variant ±6.25 %).

## 3. Running the command frames  [G, L]

`CommandFrameSystem` (game #16 / session #10, vtable `0x15c1b48`, OnTick `FUN_7ff650c167d0`), ticked after
the clock in the same EA tick:
1. every listener's slot 1 `OnPreCommandFrames()` (also when no CF is due)
2. singleton `0x4d`+0x28 = 1
3. if CFs are pending: `FUN_7ff650feea30(first, last)`:
   - EA vfunc 0x70 → vfunc 0x20 object: +0x18 = 1 ("simulating")
   - for cf = first..last: `0x4d` {+0x18 = cf, +0x20 = cf × 16000 / 1000 ms};
     flags = 1 if first, | 2 if last (a single CF gets 3);
     `FUN_7ff650fef420(ea, cf, flags)`: every listener's slot 0 `OnCommandFrame(cf, flags)` in list order,
     then drop entries of the deferred queue `EA+0x110` (stride 0x18, CF at +0x10) whose CF is non-zero
     and older than cf
   - `FUN_7ff65101fc30(obj+0x10)`, flag +0x18 = 0
4. `0x4d`+0x28 = 2
5. every listener's slot 2 `OnPostCommandFrames()`

**Listeners** (`EA+0xF0`/`+0xF8`), registered by the EA builder `FUN_7ff650be0fd0` after all systems, in
this order (game and session EA; the lobby EA has none):

| # | owner system | listener subobject |
|---|---|---|
| 0 | #17 PhysicsAreaRegistrySystem (`0x15c1ac8`) | +0x38 |
| 1 | #18 CollisionProxySyncSystem (`0x15c4d30`) | +0x10 |
| 2 | #19 UnsynchronizedMoverSystem (`0x15c1bb8`) | +0x10 |
| 3 | #20 LocalPlayerMovementReconcileSystem (`0x15c4db8`) | +0x10 |
| 4 | #21 WeaponAttachmentCommandFrameSystem (`0x15c1e68`) | +0x10 |
| 5 | #22 StatescriptSystem (`0x15c2e20`) | +0x38 |
| 6 | #23 ProjectileNetworkReceiveSystem (`0x15c1d98`) | +0x398 |

Because `CommandFrameSystem` (#16) comes before its listeners' own systems, a system's CF work for this
render frame is done before its `OnTick`. Listeners do real ECS work: e.g. the Statescript listener
spawns effect entities inside `OnCommandFrame` [L].

**Singletons** [L]: component `0x4d` CommandFrameClock (+0x18 CF, +0x20 ms, +0x28 phase) advances at
62.5/s; component `0x6b` holds the last CF confirmed by the server (live: 33 CF behind `0x4d`).
Projectile simulation divides by 16000.0 (µs per CF).

## 4. How the server steers the clock  [G]

Each data packet from the game server carries (reader `FUN_7ff650c500a0`, see
`server/internal/instance/stream.go`):
- a leading varint = **P**, the server's command frame (header+0x1c);
- in **stream 3** (`FUN_7ff650c07430`): a varint = **A**, the input ack (last client input frame the
  server has), then a **u4 = the clock flags**.

`FUN_7ff650c07560(net, P, A, flags, reset)` (reset = 0 from packets):
1. `ackedInput = max(ackedInput, A)` (net+0x60); the local player's input history up to A is dropped
   (`FUN_7ff650feef20`).
2. **Safety buffer** (clock+0x88), only while connected (`FUN_7ff650c2c410()+0x2780`) and a previous P
   exists: if `P > lastP + 4` (packets were missed), `buffer += 0.015` for every 3 missing frames, capped
   at 0.104 s; otherwise `buffer -= 0.00066` per packet, not below 0.052 s.
3. `clock+0xa8 = flags` (**dilation flags come from the server**), `clock+0x94 = P`,
   `clock+0x98 = lead = max(0, cf − P)`.
4. **Resync** (`FUN_7ff650ce64a0`) to `P + min(16, RTT / 16 ms)` (RTT from `FUN_7ff650e31d10`) when
   - flags bit 2 (`0x4`) is set (also resets the input-history state), or
   - the client's CF is **behind** P (cf < P).

   Resync sets the CF and renumbers this render frame's pending CFs after it (same count; the
   accumulator is kept); the lead is recomputed. [L] Joining the game server: one resync in the game EA
   during `NetworkEventSystem` (#3), CF 0x362 → 0x66ed4 (the server's frame).
   With `reset` set (other callers, e.g. `FUN_7ff650c07540`), lead = 0, buffer = 0.104 and the CF is set
   to P directly.

`FUN_7ff650c078b0` sets the buffer from link statistics: `jitter (FUN_7ff650ffbd30) + 0.5 × loss-sample`
limited to 0.2 s, not below 0.052 s. `FUN_7ff650c07800` is a resync to `lastP + min(16, RTT frames)`.

| flag bit | value | effect on the client |
|---|---|---|
| 0 | `0x1` | slow the clock down: −200 µs per render frame |
| 1 | `0x2` | speed the clock up: +200 µs per render frame (ignored if bit 0 is set) |
| 2 | `0x4` | resync to P + RTT frames, reset the input state |
| 3 | `0x8` | strong: ±1000 µs instead of ±200 |

The flags are stored as they are received, so they stay in effect until a later packet changes them.

## 5. The client's report back  (netcode notes, [?] partly)

The client's input stream carries a `u16 timing` (`FUN_7ff650c053d0`) = (lead × 16 ms + buffer) × scale;
observed 617–633 ≈ 560 ms lead + 60 ms buffer while our server sent no dilation (see §6). The smoothed
values at clock+0x80/+0x84 are presumably what this reports.

### 5.1 The input stream (T062)  [G]
The rest of stream 3 is the **input history**: every CF's command the server hasn't acknowledged (≤ 128), oldest
first, the first entry in full and the rest as change masks: {u32 CF, s16 yaw, s16 pitch (× 2π/65536 rad), s8 right,
s8 forward (/127), u16 buttons (ORed over the CF), u8 action code, u16 (clock+0xa0 + clock+0xa4) × 1000 [?]}. Built
per render frame by LocalPlayerInputSystem, pushed per pending CF by MovementStateSystem, consumed per CF by the
movement listener (#20 below); a CF without an entry repeats the last command, and after 32 repeats the axes and
buttons go to zero. Details: [Movement.md](Movement.md) §2.

**Correction** (T062): the LocalPlayerMovementReconcile listener's vtable (`0x7ff6515a4e28`) is slot 0
`FUN_7ff650c80960` = OnCommandFrame (**simulates the local pawn for the CF**: command → character mover step →
Statescript input), slot 1 `FUN_7ff650c80800` = OnPreCommandFrames (reconciliation with the server's movement
record), slot 2 `FUN_7ff650c809c0` = OnPostCommandFrames. Movement.md §1.

## 6. What this means for the server

- The client **runs ahead** of the server by its lead: it predicts, the server confirms. The target is a
  lead ≈ RTT + buffer (at most 16 CF of RTT on resync). Measured on localhost with our current server:
  ~35 CF (~560 ms), far too much (`0x4d` − `0x6b` = 33 live). With the tick loop and steering: 3–4 (§6.1).
- The server controls the lead with the **u4 flags** in every packet: `0x1`/`0x9` to slow a client that is
  too far ahead, `0x2`/`0xA` to speed one up that is too close or behind, `0x4` to force a resync.
  The R&D server always sends 0 (`server/internal/instance/stream.go`, `w.bits(0, 4)`), so the client never
  corrects its lead there; the instance server steers (§6.1).
- The server must send **P** (its current CF) in every packet at a steady 62.5 Hz (one CF per packet),
  otherwise the buffer logic sees gaps and the client resyncs; and the **input ack A** so the client can
  drop acknowledged input.
- A server simulation runs the same CF loop without a render rate: `CommandFrameClock::Advance(16000)`
  per server tick gives exactly one CF.

### 6.1 Measured with the instance server  (T048, 2026-09-26)  [L, H]

The instance server (`server/cmd/instance`, [InstanceServer.md](InstanceServer.md) §5) does the above: a drift-free
16 ms tick, one data packet per tick with consecutive P, the input ack and the flags, a resync (`0x4`) in the
first packet after a connect, then steering to a target lead of 4 frames (±1) measured as *newest input frame −
server frame at arrival*, window minimum over 16 input packets. Localhost, Anubis / Dorado / tutorial:

| | R&D game server (no tick loop, flags always 0) | instance server |
|---|---|---|
| packets to the client | on events + an input ack at most every 50 ms | 63/s, consecutive P, 0 late ticks in every run (~15 min in all) |
| lead (input frame − P) | **47–50 frames** (2026-09-26 20:12, conn 27); ~35 on 09-24 | **3–4** on Anubis (5–6 on Dorado), steady |
| client timing report (stream 3 u16, §5) | 617–633 | 100–116 (Dorado 132–148); dips to 52 in a client hitch |
| corrections needed | – | none in steady state; a client hitch (lead −1, 20:08:52) → `0x2` for ~5 s → back to 3 |

- Right after the connect resync the client's input starts **3–6 frames** ahead (≈ its safety buffer, RTT ≈ 0
  on localhost), i.e. already at the target: the client's own resync logic + a steady P is most of the fix.
- **Correction rate** [L]: moving the target to 12 and back to 4 through the control API (`POST /clock`), the weak
  flags (`0x2` / `0x1`) moved the lead ~0.8 frames/s each way (5 → 11 in ~9 s, 11 → 5 in ~9 s), which is
  200 µs × ~62 client render frames/s = 12.5 ms/s, as §2 predicts. The lead settles at the near edge of the dead
  band. The strong flags (`0x8`) and the resync-by-policy (beyond ±60) were not needed in these runs.
- In game [H]: no black screen after the hero confirm, HUD, ability icons and weapon on Anubis and Dorado; the
  rollback on abilities is unchanged (no server confirmation yet, T041).

## 7. Library and tests

- `systems/ecs/include/ecs/command_frame.h`: `CommandFrameClock` (dilation, 250 ms clamp, accumulator,
  pending list, `Resync` = `FUN_7ff650ce64a0`, `Seed` for tests), `CommandFrameListener`, `CommandFrameClockComponent` (`0x4d`),
  `DeferredCommandFrameEntry`.
- `command_frame_systems.h`: `GameClockSystem`, `CommandFrameSystem`; `EntityAdmin::AddCommandFrameListener`,
  `RunCommandFrame`, `RunCommandFramesPre/Post`; `manifest::AddSystems` places them at the client's indexes
  and registers stand-in listeners in the client's order.
- Tests: `tests/test_command_frames.cpp`; `tests/replay_fixture.py` replays `trace_command_frames.json`
  (every clock step, CF range and listener call, per EA) exactly, and drives the clock of the map-load
  replay with the recorded dt, dilation flags and resyncs, so work done inside listeners at specific CFs
  replays too (all fixtures match).
- Tracing: Prometheus `ecs_trace` always records `clock_dilate` / `clock_advance` / `cf_range` /
  `clock_resync` (one per EA tick) and, with `cf=true`,
  `cf_pre` / `cf_listener` / `cf_post`; work done inside a listener gets phase `cf_listener` with the
  listener index and CF. `live.py record-cf` captures a CF fixture; `ecs_trace action=save` writes the
  events to a file from inside the game (a full `live.py record` takes ~75 s after launch).

Not modelled yet: the netcode side (§4: P/ack/flags handling, buffer, when to resync, lead smoothing),
which belongs to the network layer rather than the ECS; the library exposes `EntityAdmin::ResyncClock`
and the dilation flags for it.
