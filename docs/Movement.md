# Movement: how the client moves a character

How the Overwatch beta 0.8 client (build 24919) turns per-command-frame input into character movement: the input
stream, the character mover (component `0x15`), the movement state and its records (component `0x12`), prediction
and reconciliation with the server, collision, and what a server must replicate. Written as the spec for a future
library (`systems/movement`) that replays client recordings exactly (T062).

**Addresses**: Ghidra = `0x7ff64ffe0000 + RVA`; runtime = `0x140000000 + RVA`. **Tags**: [G] read in Ghidra,
[L] measured live, [D] read from data, [?] inferred. Companions: [CommandFrames.md](CommandFrames.md) (CF loop,
clock), [StatescriptRuntime.md](StatescriptRuntime.md) §4.1.1 / §5.10.2 (buttons → LogicalButton notifications),
[Maps.md](Maps.md) §2.6 (static collision), [StatescriptNodes.md](StatescriptNodes.md) §3.16 (movement hooks).

Status: §1–§6 written from Ghidra, checked against recordings where marked [L].

---

## 1. The pipeline per command frame  [G]

### 1.1 Objects

| object | where | holds |
|---|---|---|
| **LocalPlayer** `0x2f` (on the controller entity) | `+0x1460…+0x14a0` | the input **sample** being accumulated this CF (§2.1) |
| | `+0x7e0` | the **input history** object (§2.2): vtable, last command `+0x7f0`, ring `+0x810` |
| | `+0x60`, `+0x200`, `+0x2d0`, `+0x2e0` | reconciliation: the server's movement record, its CF, the pending flag, the server mover state (§1.4) |
| | `+0x20`, `+0x40`, `+0x44` | visual error offset and its decay (§1.4) |
| | `+0x50` | reconciliation state (0/1/2) |
| **CharacterMover** `0x15` (on the hero body, the "pawn") | `+0x70` | this CF's command (16 bytes) |
| | `+0x90` / `+0x98` | Statescript mover controllers (stride 0x30; §3) |
| | `+0x7e0` / `+0xa50` | the current / previous **movement record** (0x270 bytes, §5.1) |
| **MovementState** `0x12` (on every moving entity) | `+0x20` | **history**: {f64 time, u32 CF} index ring (cap `+0x420`, index `+0x430`, count `+0x434`) and a record ring at `+0x440`, stride 0x270 (cap `+0xa040`, index `+0xa050`, count `+0xa054`); insert `FUN_7ff65100d7c0`, newest `FUN_7ff650c03580` |
| | `+0xa300` | a second history of the same shape: **the local pawn's own records, one per CF** [L] (the `+0x20` history then holds only the create snapshot) |
| | `+0xa060` | the interpolated render record → scene `0x01` |

The pawn is found from the controller through component `0x20` (PlayerPawnLink, `FUN_7ff650cd4320` /
`FUN_7ff651004640`).

### 1.2 Order within one render frame

`EntityAdmin::Tick` runs the systems in order; `CommandFrameSystem` (#16) runs all due CFs before the later systems'
own ticks (CommandFrames.md §3).

1. **LocalPlayerInputSystem** OnTick `FUN_7ff650c7df40`: samples keys / mouse / gamepad into the sample (§2.1).
2. **MovementStateSystem** (#15) OnTick `FUN_7ff650c042a0`:
   1. `c078b0` safety buffer from link statistics; `c060a0` smoothing;
   2. `c069f0`: applies the **queued movement snapshots** received from the server (`sys+0x1b0`) with
      `FUN_7ff650c06ce0` once the map is loaded (§5.2);
   3. `c05530`: **render-time prediction** of the local pawn: `0x12` vfunc `+0x30` at time
      (CF × 16000 + accumulator) µs × 1e-6 − clock`+0x80` − clock`+0x84`, with the sample `0x2f+0x560`;
   4. `c056e0`: interpolates every `0x12` at render time and writes the scene transform `0x01` (`FUN_7ff650ce6600`);
   5. `c04540`: for every pending CF of this render frame, pushes the sample into the input history
      (`FUN_7ff650fef040(0x2f+0x810, 0x2f+0x1460, cf, ms)`, §2.2);
   6. `c05eb0`: drops history older than 30 s.
3. **CommandFrameSystem** (#16): for each CF listener, `OnPreCommandFrames`, then per CF `OnCommandFrame(cf)` in
   listener order, then `OnPostCommandFrames`. The movement listener is #3,
   **LocalPlayerMovementReconcileSystem** (#20), listener vtable `0x7ff6515a4e28`:

   | slot | function | does |
   |---|---|---|
   | 0 OnCommandFrame | `FUN_7ff650c80960` → per `0x2f`: `FUN_7ff650c80a90(sys, 0x2f, cf)` | **simulates the local pawn for this CF** (§1.3) |
   | 1 OnPreCommandFrames | `FUN_7ff650c80800` | per `0x2f`: `+0x1510 = +0x1500`, clear `+0x7d1`, sync the mover (`FUN_7ff651037d30(mover, mover+0xa50)`), run the **reconciliation state machine** `FUN_7ff650c81540` (§1.4) |
   | 2 OnPostCommandFrames | `FUN_7ff650c809c0` | per `0x2f`: `c81370`, `c80bf0` (render record, visual error decay), `c80f70`, `c81160` at render time |

   ECS_Systems.md described slot 1 as "the interface method"; the vtable shows it is the pre-CF slot and that slot 0
   drives the per-CF simulation.
4. The other CF listeners run in the same CF after it: #21 weapon attachments (reads the owner's `0x12` at the CF),
   #22 Statescript, #23 projectiles.

Remote entities are **not simulated**: their `0x12` history is filled by server snapshots (§5.2) and interpolated
in step 2.4. Only the local player's pawn runs the mover.

### 1.3 One CF of the local pawn

`FUN_7ff650c80a90(sys, lp, cf)` (if `lp+0x7d0` is set):
- `FUN_7ff65102ecd0(lp+0x7e0, controller, cf, cf, flags)`, flags = 3 (mover + Statescript), or 2 (Statescript
  only) when the pawn's component at `FUN_7ff650cd8300` (+0x1c0, count `+0x10` > 0) is set [?: attached to / carried
  by something]; in that case the pawn's record is taken from outside and written into the `0x12` history
  (`FUN_7ff65100d7c0(ms+0xa300, …)`).
- `FUN_7ff65102ecd0(hist, controller, from, to, flags)` for each cf in from..to (one CF here; the replay uses a
  range, §1.4): sets the EA's "current CF" (EA vfunc `+0x70` → `+0x20` object `+0`), then
  `FUN_7ff65102f160(hist, cf, 16000, mover, pawnLink, &statescripts)` and copies the new record to the previous
  (`FUN_7ff650c035b0(mover+0xa50, mover+0x7e0)`). Before the loop, if the pawn has no parent and `cf ≠ 0`, the mover is
  rewound to cf − 1 (`FUN_7ff651034790`); if `mover+0x1078` it resets its sub-state (`FUN_7ff651035a80`).
- `FUN_7ff65102f160`: picks the command for cf from the history (`FUN_7ff650feefc0`: the entry with that CF, else
  the next newer one); **with no input for this CF it repeats the last command** (`hist+0x10`), counting repeats
  at `hist+0x28`; after **32** repeats it zeroes the axes and buttons and the action byte. Then
  1. `FUN_7ff6510339b0(mover+0x48, cmd)`: clamps the command's aim to the mover's yaw / pitch limits
     (`+0x50…+0x5c`, wrapping yaw by 2π), rewriting the s16 angles when clamped;
  2. **`FUN_7ff651037310(mover, cmd, cf, 16000)`: the mover step** (§3);
  3. history vfunc `+0x08(cmd, cf, dt, mover, pawnLink)` and `+0x10(cmd, statescripts)`: the Statescript side
     (button edges → LogicalButton notifications, StatescriptRuntime.md §5.10.2).

### 1.4 Reconciliation with the server  [G]

The client predicts; the server's movement record for the local pawn arrives in a snapshot (§5.2). When it is
applied, the snapshot code copies the record to `lp+0x60` (its CF lands at `lp+0x200`) and sets
`lp+0x2d0` = packet field `+0x144` + 1 [?: the input ack + 1] if that is not older than the pending one.

`FUN_7ff650c81540` runs a 3-state machine (`lp+0x50`, table at `0x7ff6518d8e20`), up to 10001 steps per render frame:

| state | function | does |
|---|---|---|
| 0 check | `FUN_7ff650c81710` | if something is pending: when the pawn is attached (component bit 40), the record is re-based on the parent's transform. Otherwise read the **local record for the server's CF** from the mover's history (mover`+0x10c0` vfunc `+0x40(cf)`) and compare it with the server's (`FUN_7ff65100b390`). **Equal → prediction confirmed**: trim the mover's history (`FUN_7ff651034790(mover, pending)`, capped against the Statescript component's `+0xf40`, at most 30 CFs back), clear pending, done. Different → state 1. |
| 1 restore | `FUN_7ff650c81a80` | replay range start = end = pending (`+0x550/+0x554`), elapsed `+0x558` = 0; copy the server state (`lp+0x2e0`) over `lp+0x60`; rewind the mover to the start CF; if the mover's reset counter (`mover+0x984`) differs from the server's (`lp+0x484`), **hard reset** the mover to the server state (`FUN_7ff651037d30`), flag `lp+0x7d1`, back to 0; else state 2. |
| 2 replay | `FUN_7ff650c81c40` | load the server state into the mover (`mover+0x10c8` = 1 during it), then **re-simulate the stored inputs** from the start CF (`FUN_7ff65102ecd0(…, flags 1)`: mover only, no Statescript) up to a target that grows with time: target = start + ⌈(current − start) × min(1, elapsed × 20.833)⌉, i.e. the whole replay is spread over at most 48 ms of render time. When the target reaches the current input CF (`lp+0x800`): compute the **visual error** (old predicted vs new, `FUN_7ff65100cb50` / `c9b0` into `lp+0x20`) and return to state 0. |

The visual error decays in `FUN_7ff650c80bf0`: every 1/60 s the magnitude `lp+0x40` is multiplied by **0.85**.

**The compare is exact** (`FUN_7ff65100b390`): every compared field of the two records must be bit-equal as floats
(flags `+0x198`, `+0x19c`, `+0x1a4`, `+0x1ac`, bytes `+0x1b0…+0x1b3`, `+0x1b4…+0x1bc`, rotation `+0x200`, position
`+0x220`, `+0x230`, `+0x240…+0x258`, and the optional blocks when their flag is set: `0x400` → `+0x00…+0x20`, `0x800`
→ `+0x30…+0x38`, `0x10` → `+0x40…+0x60`, `0x80` → `+0x70…+0x88`, `0x20` → `+0x90` (`FUN_7ff651008a90`), `0x40` →
`+0xd0`, and `+0xd8` (`FUN_7ff651008f90`)). Consequence for the server: **a server mover that is not bit-exact makes
the client rewind and replay on every snapshot** (and blend a visual error); a bit-exact one is invisible. Whether the
record is quantised before the compare (the wire carries positions in 1/1024 units) is part of §3 / §5.1.

---

## 2. The input stream  [G, L]

### 2.1 Sampling (per render frame)

`LocalPlayerInputSystem` (`FUN_7ff650c7df40`) per `0x2f`, unless input is captured (window unfocused / UI):
- **buttons** `+0x147a` (`FUN_7ff650c7e580`), **action code** `+0x147c` (`FUN_7ff650c7e0e0`), **movement axes**
  `+0x1478/+0x1479` (`FUN_7ff650c7e6f0`): see StatescriptRuntime.md §4.1.1 for the logical-button ids. Axes: each
  s8 is +127 / −127 / 0 from the keys (both keys → 0), plus the gamepad stick × 127, clamped to [−127, 127]:
  `+0x1478` = right (move_right 0x41 +, move_left 0x40 −), `+0x1479` = forward (move_forward 0x3d or mouse_forward
  0x3e +, move_backward 0x3f −). The cvar `player_GenerateRandomMovement` substitutes random axes (and jump).
- **aim** (`FUN_7ff650c7f320`, `FUN_7ff650c7efd0`): the mouse deltas (`+0x1490/+0x1494`, scaled by the zoom factor
  of `FUN_7ff650cd8300`) rotate the view direction `+0x14d0`; the command carries the **absolute** view angles
  quantised to s16: yaw = atan2 of the direction × 65536 / 2π, pitch = asin(−y) clamped to ±1.5533 rad (±89°) ×
  65536 / 2π. The mover's own aim limits are applied again per CF (§1.3).
- The sample is folded into the command being built for the current CF (`+0x1464…`): angles and axes overwrite,
  **buttons are ORed** (a tap shorter than a CF still reaches the CF), a non-zero action code overwrites.

### 2.2 The input history  [G]

`FUN_7ff650fef040(ring = lp+0x810, sample, cf, ms)`: an entry per CF, 0x18 bytes:

| entry | type | meaning |
|---|---|---|
| +0x00 | u32 | command frame |
| +0x04 | u32 | sequence number (increments per new entry; not sent) |
| +0x08 | s16 | yaw, × 2π / 65536 rad |
| +0x0a | s16 | pitch, clamped to ±0x3fff (±90°), × 2π / 65536 rad |
| +0x0c | s8 | right axis (/127) |
| +0x0d | s8 | forward axis (/127) |
| +0x0e | u16 | buttons (ORed while the CF is open) |
| +0x10 | u8 | action code (one-shot: the mover side zeroes it when the same entry is used again) |
| +0x14 | u16 | (clock`+0xa0` + clock`+0xa4`) × 1000 [?: the client's interpolation / view delay in ms, for lag compensation] |

Ring capacity at `+0xc20` (read live), count up to 128. The **command** a CF runs with is the 16 bytes from `+0x04`
{seq, yaw, pitch, right, forward, buttons, action, …}.

### 2.3 On the wire

Stream 3 of every client data packet (`FUN_7ff650c05260` / `FUN_7ff650c053d0`): u16 timing (CommandFrames.md §5),
then `FUN_7ff650fef1c0`: u8 K = the entries the server hasn't acknowledged (up to 128, oldest first), each u32 CF,
the first one in full (`FUN_7ff650feee90`: s16 yaw, s16 pitch, s8 right, s8 forward, u16 buttons, u8 action) + u16
tail, the others as a u8 change mask against the previous entry + the changed fields (0x01 yaw, 0x02 pitch, 0x04
right, 0x08 forward, 0x10 buttons, 0x20 action, 0x40 tail). Decoder: `server/inst/wire/input.go` (field names
there: Aim1 = yaw, Aim2 = pitch, Move1 = right, Move2 = forward, Extra = action, Tail). The server acknowledges
with A in its stream 3 (CommandFrames.md §4), which drops entries ≤ A.

So **the server receives every CF's command, redundantly, until acknowledged**: the same data the client's mover
ran with. A server running the same mover on the same commands from the same state reproduces the client's records.

---

## 3. The character mover  [G, L where marked]

Component `0x15` (`STUCharacterMoverComponent`), class vtable `0x7ff6515a5f90`, created by `FUN_7ff650caf980`;
on add (slot 2, `FUN_7ff650caecb0`) it links the entity's `0x12` (`M+0x10c0`) and initialises from the STU
(`FUN_7ff651034140`: parameters, the physics capsule, filter, `M+0x1078` = 1). "M+x" = mover offset, "R+x" = movement
record offset (R = `M+0x7e0`, §5.1), "E+x" = effective parameter = `M+0x2c8 + x` with x the STU offset (§3.5).
dt = 16000 µs = 0.016 s. Y is up (world up `DAT_7ff65188f720`, runtime-initialised) [G for the x/z ring, ? for the value].

**The mover's state is the record R.** Every step reads R, writes R, and quantises R at the end (§3.4), so the
next CF starts from quantised values.

### 3.1 Mover layout (the fields the step uses)

| M+ | type | meaning |
|---|---|---|
| 0x08 | ptr | entity |
| 0x48…0x5c | f32 | aim clamp for the command (`FUN_7ff6510339b0`): +0x48 yaw reference, +0x50/+0x58 yaw min/max, +0x54/+0x5c pitch min/max (ctor ±1e10) |
| 0x60 | vec4 | up (gravity up), initially world up; `+0x200` = its copy at step start |
| 0x70 | 16 B | this CF's command {u32 seq, s16 yaw, s16 pitch, s8 right, s8 forward, u16 buttons, u8 action} |
| 0x80, 0x88 | u32, s64 | CF, dt (µs) |
| 0x90 / 0x98 | array | **active controllers** this CF (stride 0x30, rebuilt from +0x380 each CF, cleared at the end) |
| 0x1f0 | ptr | physics world (queries; `+0x3a8` gravity-state object, `+0x3a0` filter table) |
| 0x218 | 0x90 B | base parameters = STU bytes 8…0x8f (`FUN_7ff651034650`) |
| 0x2c8 | 0xb0 B | **effective parameters** E (base + modifiers, recomputed each CF) |
| 0x358 / 0x35c / 0x364 | u8 / f32 / f32 | collision on; speed scale; movement time scale (0…100, scales the move's dt only) |
| 0x368…0x374 | f32 | yaw / pitch limit window (rad) |
| 0x380 / 0x388 | array | **controller sources** (stride 0x30, Statescript MoverAdd) |
| 0x460 / 0x468 | array | second controller list (stride 0x40), called after each stage, result ignored [? filler] |
| 0x580 / 0x588 | array | **scalar modifiers** (stride 0x38, MovementMod) |
| 0x680 / 0x688 | array | **velocity ops** (stride 0x40, MovementMod vector ops: knockback) |
| 0x7e0 / 0xa50 | 0x270 B | current / previous record |
| 0xcc0 | ptr | the character proxy (physics capsule) |
| 0xcd0, 0xce0, 0xcf0 | | min contact·forward of the last move; ground normal (parent-local / world) |
| 0x1061, 0x1070, 0x1076 | u8, int, u8 | touched geometry; team index (component 0x10); group-4 flag (0x10 bit 22) |
| 0x1077 / 0x1078 / 0x1079 / 0x107a / 0x107b / 0x107c / 0x107e | u8 | jumped this CF / initialised / parent cached / collision allowed / alt filter / ghost / landed this CF |
| 0x1080 | mat4 | last parent world matrix |
| 0x10c0, 0x10c8 | ptr, int | the entity's `0x12`; mode (0/1/2, slot 18) [? meaning; 1 during the reconcile replay] |

### 3.2 The step, in order

`FUN_7ff651037310(M, cmd, cf, 16000)`, only if `M+0x1078` and `M+0x1079`:
```
M+0x107e = 0; M+0x70 = cmd; FUN_7ff65103cef0 (component 0x10 status 0x10: zero axes and buttons [? stun])
M+0x80 = cf; M+0x88 = dt; M+0x200 = M+0x60; upPrev = world up of the body (R+0x200 via the parent)
037ee0  modifiers → effective parameters (§3.6)
6510386a0  controller bookkeeping → active list (§3.7)
M+0x107b = !(R.flags bit13) && E+0x88;  038890 proxy collision filter (§4)
dts = dt × 1e-6
stage 0x30 orient    default 038970 (§3.3.1)
stage 0x38 ground    default 038e10 probe, 039870, 039990 parent change, 039a70 land / jump / crouch (§3.3.2)
                     then, if grounded (flags & 0xc == 0): vel −= (vel·n) n
stage 0x40 context   axes = (right / 127, forward / 127); default 039e90 (§3.3.3)
stage 0x48 step      default 03a2b0 (§3.3.4)
stage 0x50 velocity  default: 03a440 (airborne: air friction + drag); grounded: 03a630 ground friction;
                     03a950 accelerate; 03ab00 speed clamp; airborne: gravity (§3.3.5)
stage 0x58           controllers only
stage 0x60 vel-ops   default 03ae90 (§3.3.6)
03b320 up-alignment;  03b7a0 input record + MOVE (§3.3.7);  if M+0x1077: vfunc 0x70
03bc10 fall-velocity bookkeeping / landing callback
65100afc0(R) QUANTISE (§3.4);  03bed0 place the proxy at pos + up·(stepH + R+0x1b8);  036700 clear the active list
M+0x70 = cmd; M+0xce0 / 0xcf0 = ground normal
```
A stage's controllers are called in list order with "overridden so far"; the default runs only if none returned true.

### 3.3 The stages

#### 3.3.1 Orient (`FUN_7ff651038970`)
yaw, pitch = s16 × 2π/65536 → view quaternion (half angles) → R+0x200 and eulers R+0x210 pitch / +0x214 yaw /
+0x218 roll; then the world yaw is clamped to [M+0x368, M+0x36c] (with 2π wrap) and pitch to [M+0x370, M+0x374];
the **body** quaternion = euler(yaw, 0, roll) → R+0x200, the aim pitch is kept in **R+0x240**.

#### 3.3.2 Ground: probe, land, jump, crouch
Probe (`FUN_7ff651038e10`):
- slopeMax = clamp(E+0x30 × π/180, 0, 1.5707844), cosMax = cos(slopeMax);
- probe length d = 0 when airborne; grounded: d = max(tan(slopeMax) × dts × min(|vel|, maxSpeed) × 1.1, E+0x34),
  maxSpeed = max of the three standing (or crouched) speeds;
- query `FUN_7ff6509d76c0(world, &hit, &q)` from pos + up·(h + r) down to pos − up·d (h = proxy height above the
  feet, r = capsule radius), filter 0x1081 | group << 16 (§4); accepted if hit, normal·up > cosMax and hit flags
  bit 0x10000;
- else, if E+0x24 = N > 0: N probes on a ring of radius E+0x20 in x/z (angle step 2π/N from (1, 0)); the accepted hit
  with the smallest |(pos − hit)·up| wins, shifted back by its ring offset;
- gravity scale E+0x78 ≤ 0 or no hit → no ground. The result G {point, normal, platform offset, entity id, bone,
  valid} is in the ground entity's frame when it moves (components 0x12 / 0x1c / 0x11).

`FUN_7ff651039870`: on the same parent, the platform's displacement since the proxy was placed → added to the move.
`FUN_7ff651039990`: a new ground entity/bone → the state is converted to world and then into the new parent's frame,
R+0x244/+0x248 = new id / bone, vel −= the platform's velocity.

Land / jump / crouch (`FUN_7ff651039a70(M, n, valid, jump, crouch)`):
```
if grounded: if !valid { flags = flags & ~4 | 8; R+0x1a8 = 0 }                 // walked off: falling
else if valid && vel·n < 1.19e-5 { flags &= ~0xc; M+0x1077 = 0; M+0x107e = 1; R+0x1a8 = 0 }   // landed
else R+0x1a8++                                                                  // airborne CF counter
j = jump (buttons 0x100) through modifier kind 0xc
if j > 0.5: if grounded && !(flags & 1): JUMP; flags |= 1                       // bit 0 = jump latch
else if grounded: flags &= ~1                                                   // the latch clears only on the ground
c = crouch (0x200) && grounded, modifier kind 0xd → FUN_7ff651039d50(M, c > 0.5)
```
JUMP (`FUN_7ff651039bb0`): u = (vel + platform velocity)·up; vel += (E+0x74 − u)·up (vertical speed := jump speed);
flags = flags & ~8 | 0x204; vfunc 0x68; M+0x1077 = 1. **The default mover has no double jump** (Genji's comes
from Statescript, §3.7). Crouch (`FUN_7ff651039d50`): on change, flag bit 1 and a new capsule: r = max(E+0x18 or
E+0x0c, 0.02), length = max(0.1 r, height − (2r + stepH)), segment (0, r) → (0, r + length).

#### 3.3.3 Move context (`FUN_7ff651039e90`)
ctx (0xa0 bytes): vel; n = ground normal if grounded and valid, else up; vn = (vel·n) n; vt = vel − vn; fwd = body
matrix row 2; side = −normalize(row1 × row2); G point and platform offset; axes after `FUN_7ff65103a1a0` (modifiers 9 /
10 / 11 scale forward / backward / strafe input; x, y clamped to [−1, 1]); vel·n; body yaw; cf; 20 (move iterations).

#### 3.3.4 Ground snap and step spring (`FUN_7ff65103a2b0`)
```
if valid && grounded: R+0x220 = G.point                                  // feet on the ground
x = (proxyPos − pos)·up − stepH                                          // stepH = E+0x10 (crouched E+0x1c)
w = E+0x28 × 2π; z = E+0x2c
R+0x1bc = (R+0x1bc − w²·dts·x) / ((2z + w·dts)·w·dts + 1)                 // implicit damped spring
R+0x1b8 = R+0x1bc·dts + x
```
The feet snap; the capsule follows through the spring, so steps are climbed instantly at the feet and smoothed for
the body. [L] `walk_forward`: y rises 1.871 → 2.558 in one CF at a step.

#### 3.3.5 Velocity: friction, accelerate, clamp, gravity, drag
- **friction** `FUN_7ff65103a630(base, thr, coef)` on the tangential velocity v (the whole velocity when flying without
  gravity): dec = min(dts × (base + coef × max(|v| − thr, 0)) × E+0x38, |v|); vel −= dec × v/|v|. Ground: E+0x5c /
  +0x60 / +0x64; air (airborne only, in `03a440`): E+0x68 / +0x6c / +0x70.
- **accelerate** `FUN_7ff65103a950`: wish = x·strafe·side + y·(y > 0 ? forward : backward)·fwd; wishSpeed = |wish|
  capped by dirMax(d) = strafe + (fwdOrBack − strafe)·|d·fwd| (`FUN_7ff6510355f0`), d = wish/|wish|;
  add = wishSpeed − (vel − vn)·d; accel = E+0x50 grounded, E+0x58 airborne while touching geometry (flag 0x20000),
  else E+0x54; if add > 0: vel += min(add, accel × E+0x38 × dts) × d.
- **speed clamp** `FUN_7ff65103ab00`: the tangential speed is capped at max(dirMax, |vt before the stage|) when it
  differs by more than 0.004 (never faster than the maximum, but momentum above it is kept).
- **gravity** (airborne only): a = g × E+0x78 × up × dts, g = −10 unless the world's gravity-state object disables it
  (`+0x3a8` null, `+0x160 == 1`, or `+0x168 → +0x24 & ~2 == 0`) [? which states]; vel += a; R+0x250 += a.
- **drag** (airborne, `03a440`): vt_term = E+0x7c when falling (vel·n ≤ 0), E+0x80 when rising; if > 1.19e-5:
  dv = −|g·E+0x78 / vt_term² · (vel·n)| · dts · vn, limited to |vel|. At |vn| = vt_term drag equals gravity, so the
  terminal velocity is reached asymptotically; there is no clamp.
- Units: accelerations and friction are **multiples of the forward speed E+0x38 per second**.

[L] checked against the recordings (Genji: forward speed 6, ground accel 35, ground friction 15 / 6.2 / 2, jump 6.2,
gravity scale 1.75, terminal 30 falling / 0 rising):
- `walk_forward`: speeds 3.359, 5.279, 6.0 = min(6, 35·6·0.016 = 3.36); 3.36 − 0.016·6·15 = 1.92 + 3.36 = 5.28; then
  capped at 6 (values quantised to 1/1024).
- `jump`: first CF 6.2 − 17.5 × 0.016 = 5.92, y += 5.92 × 0.016; rising −0.28 per CF (no rising drag); falling
  −0.271 at −5.3 m/s (drag 17.5·(5.3/30)² ≈ 0.55 m/s²); landing friction 0.016·6·15 = 1.44 per CF.

#### 3.3.6 Velocity ops: knockback (`FUN_7ff65103ae90`)
Only with modifiers or vector ops present: the world velocity goes through the limit modifiers (kind 0x17 total speed,
0x18 horizontal, 0x19 vertical, 0x1a vertical / jump speed), then the vector ops of `M+0x680` (add / multiply / set a
vec4) — **the knockback and impulse entry point** (MovementMod vector ops). If the vertical speed rose by more than
0.004 the mover becomes airborne (flags = flags & ~4 | 8).

#### 3.3.7 Input record and move (`FUN_7ff65103b7a0`)
R+0x1b0 / +0x1b1 = the s8 axes × 127 of this CF; if non-zero also R+0x1b2 / +0x1b3 and R+0x1b4 = cf. A controller's
slot `+0x70` may take over the position (teleports, attach). Default: vfunc `0x78` (`FUN_7ff650caed70` →
`FUN_7ff651034d70`):
```
flags &= ~0x20000
if collision disabled (M+0x107a, M+0x358, every controller's +0x68): proxy += v·dtEff
else: disp = (R+0x1bc·up + v)·dtEff + platform offset
      FUN_7ff651331c90(proxy, &res, {disp, dtEff, skin 0.005, 20 iterations})    // sweep and slide (§4)
      proxyPos = res+0x210; v = FUN_7ff651331af0(proxy, v)                      // velocity clipped by the contacts
      contacts (res+0x20c > 0): flags |= 0x20000; M+0x1061 = 1; M+0xcd0 = min(contact normal · fwd)
pos = proxyPos − up·(stepH + R+0x1b8); vel = v   (both back into the parent frame); R+0x1a0 = cf
```
`FUN_7ff65103bc10` then keeps R+0x250 (the gravity-accumulated fall velocity) consistent and, on landing, calls the
landing callback (slot 17, with R+0x250) and zeroes it.

### 3.4 Quantisation (`FUN_7ff65100afc0`)
At the end of every step, in place, truncating toward zero (`cvttss2si`):

| field | to |
|---|---|
| R+0x220 position, R+0x230 velocity (x, y, z) | (float)(int)(v × 1024) / 1024 |
| R+0x1b8 / R+0x1bc spring offset / velocity | 1/1024 |
| R+0x210 / +0x214 / +0x218 pitch / yaw / roll | (float)(short)(int)(a × 4096) / 4096, then the quaternion R+0x200 is rebuilt from them |
| R+0x240 aim pitch | s16 / 4096 |
| R+0x70…+0x78 (WallMove block, flag 0x80) | 1/1024 |

Not quantised: R+0x250 (fall velocity, still compared exactly), the w lanes, counters. [L] every recorded position
and velocity is a multiple of 1/1024.

**Bit-exactness**: lengths are computed with `rsqrtss` / `rsqrtps` plus one Newton step (constants at
`0x7ff651437970`/`80`, `…c10`/`20`, `…d40`/`50`), not `sqrtss`. A reimplementation must use the same sequence (and SSE
single precision, the same operation order) to produce equal records; the quantisation absorbs most, not all,
differences (a value near a 1/1024 boundary still flips).

### 3.5 Parameters: the STU and per-hero values
`STUCharacterMoverComponent` (0xc6ec49e5, 0x90 bytes; E+x = M+0x2c8+x). Values from the install (all 21 heroes,
`systems/data/movement/hero_movers.json`, T062) [D]; meanings [G]:

| STU | hash | meaning | common value | differs |
|---|---|---|---|---|
| 0x08 | 4213f69e | standing height | 2 | Reinhardt / Bastion / Roadhog 2.3, Winston 2.5, Torbjörn / Zenyatta 1.5 |
| 0x0c | ee162e1c | standing radius (≥ 0.02) | 0.5 | Reinhardt / Winston 1 |
| 0x10 | 429d00f9 | standing step height (capsule bottom above the feet) | 0.5 | |
| 0x14 / 0x18 / 0x1c | 26b1e89c / 262f155f / 28c23d22 | crouched height / radius / step height | 1.25 / 0.5 / 0.25 | radius: Reinhardt / Winston 1 |
| 0x20 | 0137b364 | ground ring-probe radius | 0.45 | Tracer 0.5 |
| 0x24 | 70e76efc (s32) | ground ring-probe count | 8 | |
| 0x28 / 0x2c | cdeb173e / 1650a1e6 | step spring frequency (Hz) / damping ratio | 10 / 1 | |
| 0x30 | 8a8bde3c | max walkable slope (°) | 45 | |
| 0x34 | c6746ba1 | minimum ground-snap distance | 0.5 | Torbjörn / Zenyatta 0.3 |
| 0x38 / 0x3c / 0x40 | a77dafce / 2ac3a673 / 85f126df | max forward / backward / strafe speed | 5.5 / 4.95 / 5.5 | Genji, Tracer 6 / 5.4 / 6 |
| 0x44 / 0x48 / 0x4c | dfc6730d / 28d68ea5 / 39e44797 | crouched forward / backward / strafe | 3 / 3 / 3 | |
| 0x50 | 0400d8b0 | ground acceleration (× fwd speed / s) | 35 | Tracer 22, Lúcio 15 |
| 0x54 | 210433d7 | air acceleration | 3.5 | |
| 0x58 | d6e4f419 | air acceleration while touching geometry | 0.5 | |
| 0x5c / 0x60 / 0x64 | 9274e557 / 743b7433 / 14be84e0 | ground friction base / threshold / coefficient | 15 / 6.2 / 2 | Tracer base 6.5, Lúcio 2 / – / 0.5 |
| 0x68 / 0x6c / 0x70 | 05094c81 / 20c44f9b / 35d20516 | air friction base / threshold / coefficient | 0 / 5.5 / 0.35 | Lúcio threshold 10 |
| 0x74 | bd620ad7 `m_jumpSpeed` | jump speed | 6 | Genji, Tracer 6.2 |
| 0x78 | 3b349362 | gravity scale (× 10 m/s²) | 1.75 | |
| 0x7c / 0x80 | 2f0f310d `m_terminalVelocity` / a7e4bf08 | terminal velocity falling / rising (0 = no drag) | 30 / 0 | |
| 0x84 | 9684fdae | up-alignment rate (1/s) | 4.5 | |
| 0x88 | c1426cd3 (u8) | alternate collision mask enable (unless record flag bit 13) | 1 | |

(The full per-hero table is in `systems/data/movement/README.md`; 12 non-hero definitions also carry a mover.)

### 3.6 Modifiers (MovementMod)
`STUStatescriptStateMovementMod` (`02e4a0ab`, activate `FUN_7ff651088940`, add `FUN_7ff651088fd0`) is not a controller:
each op adds a scalar modifier to `M+0x580` {key, start CF = CF + 1, end CF, kind, combine, value, value2} or a
vector op to `M+0x680` (value vec4). Combine: add / multiply / set / min / max / clamp (scalar), add / multiply / set
(vector). With its bool config var `142abd7e` the op lasts one CF. `FUN_7ff651037ee0` applies the scalar modifiers
whose [start, end) contains the CF, from the base values:

| kind | effect |
|---|---|
| 0 | speed scale: the six speeds and both friction thresholds × s |
| 1 / 2 | ground / air acceleration × v |
| 3 / 4 | ground / air friction base and coefficient × v |
| 5 / 6 / 7 / 8 | jump speed := v / gravity scale × v / terminal falling := v / rising := v |
| 9 / 10 / 11 | forward / backward / strafe input scale |
| 0xc / 0xd | jump / crouch button value |
| 0xe / 0xf / 0x10 | alt filter bool / collision on / ghost |
| 0x11 / 0x12 | ? (M+0x360) / movement time scale |
| 0x13…0x16 | yaw / pitch limits (degrees) |
| 0x17…0x1a | velocity limits (total, horizontal, vertical, vertical / jump speed; applied in §3.3.6) |

R+0x1ac = the effective gravity scale.

### 3.7 Controllers (MoverAdd)
Mover-controller nodes (activate: node vfunc slot 44, generic `FUN_7ff65108b1f0`; deactivate slot 45
`FUN_7ff65108b420`; kind = node vfunc slot 75; factory `FUN_7ff65108f740(M, kind)`) add a **source** to `M+0x380`:
{type 1 = Statescript, key ((graph << 16 | node) << 32 | serial), `m_priority`, start CF = CF + 1, end CF
(0xffffffff until deactivated), controller}. Each CF (`FUN_7ff6510386a0`) the sources with start ≤ cf < end are
begun (claiming their record block: the record flag bit the controller owns), ended ones release it, and the active
list is rebuilt in array order. Controller vtable: +0x08 begin CF, +0x10 ended, +0x18 / +0x20 claim / release a
record block, +0x28 record-flag mask, **+0x30…+0x60 the stage overrides**, +0x68 collision enabled, +0x70 move override.

| kind | node | record block (flag) | overrides |
|---|---|---|---|
| 1 | MoveTo `c1035274` | 0x10 | ground, step, velocity, vel-ops |
| 2 | LedgeClimb `f6e1e486` | 0x20 | velocity (`FUN_7ff651091390`) |
| 3 | WallMove `f45a1956` | 0x80 | ground, velocity |
| 6 | Accelerate `fea1e74b` | – | velocity (`FUN_7ff651095ad0`) |
| 7 | Steer `c2eeee16` | 0x40 | orient |
| 8 | Attach `971645fe` | – | ground; step / velocity / vel-ops suppressed; move override |
| 10 | `ed329693` | – | stage 0x58 |
| 12 | climb `c8c71bbc` | 0x20 | velocity (same function as LedgeClimb) |
| 13 | curve `f536a60f` | – | begin / end only |
| 0, 4, 5, 9, 11, 14 | no node found in the catalogue | | [?] |

[L] Genji's Statescript movement (recordings with instance `--shadow`, where the client's graphs run unopposed): a
jump tap (jump held ~4 CFs) gives the normal jump (flags 0x225, vy 5.92) and on the **next** CF flags 0x2a9 (the
WallMove block 0x80 claimed) with **vy := 9.3**; about 36 CFs later vy := 9.3 again without input, with x / z fixed,
then a push-off (flag 0x20000) near walls; this happens in open floor too (`jump`, `double_jump`), so it is Genji's
graph (double jump / wall climb) acting through a mover controller, not the default mover. Which graph states own
it is a follow-up. With the server's Statescript on (normal instance mode) the first jump after the lock-in still gets the 9.3
(`jump_sim`), later jumps don't (`double_jump_sim`: vy ≤ 5.92, no 0x80 block): something in the server's state never
re-arms it (InstanceServer.md §10.4 lists Genji's movement-owner vars among the mismatching states).

## 4. Collision  [G]

### 4.1 The engine
**Blizzard's own physics library, prefix `dm`**, not Havok / PhysX / Bullet. A debug writer that dumps a world as
C++ test code (`FUN_7ff651191890` bodies, `FUN_7ff651195970` / `FUN_7ff651195ef0` fixtures) names it:
`dmWorld_CreateBody`, `dmBody_CreateFixture`, `dmSphereDef` / `dmCapsuleDef` / `dmPolytopeDef` / `dmTreeMeshDef` /
`dmHeightFieldDef`, `m_filter.m_categoryBits / m_includeMask / m_groupIndex`, joints, `dmWorld_LoadMaterials`. The
design is a 3D engine in the style of Box2D v3 (handle ids {u16 world, u16 revision, u32 index}, dynamic AABB trees,
contact manifolds, GJK + SAT, a plane-solver character mover; the closest public reference is Box2D v3's
`b2World_CastMover` / `b2SolvePlanes` / `b2ClipVector`, but the constants here rule). Code ~`0x7ff651190000…0x7ff6511e0000`;
the game's character-controller wrapper `0x7ff651331010…0x7ff651331d28`; SIMD constants at `0x7ff6517f88b0…` (filled at
start-up; 0.005 linear slop at `…8910`).

**Who and what** [talk, not the binary]: in the BlizzCon 2017 Overwatch development retrospective, Jeff Kaplan
credits Erin Catto (author of Box2D) with "getting the Blizzard physics engine working within the Overwatch engine for
the first time so you can have things like the crates and vases on Temple of Anubis breaking and moving" (quoted by the
user, 2026-09-29). The engine's name is reportedly **Domino** [? user recollection; consistent with the `dm` prefix]; it
is Blizzard's shared engine (also associated with Diablo III [?]). This matches the binary: the talk's example, the
breakable props, are the cosmetic bodies stepped per render frame (§4.7), while hero movement is a gameplay mover on
top of the engine's queries (§3–§4.4). The engine itself and a comparison with Erin Catto's public engines are in [Physics.md](Physics.md).

### 4.2 The world
`DAT_7ff6517f8a20` = 256 world pointers (not a body table); the game's world id = scene`+0x3a0` + 0x4c0 (u16).

| world+ | array (stride) | notes |
|---|---|---|
| 0x008 | 4 broadphase AABB trees × 0x28 | static → tree 0, kinematic → 1, dynamic → 2 |
| 0x2f0 | bodies (0x1b0) | +4 type (0 dynamic, 1 kinematic, 2 static), +0x10 quat, +0x20 pos, +0x30 / +0x40 linear / angular velocity |
| 0x310 | **characters** (0x50) | kinematic character proxies: +0x00 quat, +0x10 position, +0x24 first fixture, +0x38 s16 group (team), +0x3a s16 priority |
| 0x330 | fixtures (0x90) | +0x28 shape, +0x64 AABB margin (0.1 characters), +0x68 push limit, +0x6c filter {u16 category, u16 mask, s16 group}, +0x7e material, +0x80 surface flags, +0x88 owner kind (1 body, 2 character) |
| 0x350 | contacts (0x60) | manifolds of 0x100 (4 points × 0x30, normal +0xc0, point count +0xf6; mesh points carry the triangle index +0x1c and material +0x20) |

Shapes: 0 sphere, 1 capsule {radius, p1, p2}, 2 convex hull (radius **0.01**), 3 tree mesh, 4 heightfield (unused
by the maps seen). A mesh triangle is handled as a temporary triangle shape with radius **0.01**.

**The map** (`FUN_7ff650a239a0`, world flag 0x200) is one static body with a fixture per Maps.md §2.6 item
(`FUN_7ff651193ee0`): category 0x0001 (0x0800 when the item's flag byte is set), the item's mask, restitution 0,
push limit FLT_MAX, group 0; the triangle mesh with friction 0.9, material 1, flags 3. The mesh item's layout is
corrected in Maps.md §2.6 (BVH nodes first; node and triangle formats). Queries walk the BVH only into nodes whose
u16 mask has the querying fixture's category (`FUN_7ff6511ae280`).

### 4.3 The character's capsule
Created by the mover's init (`FUN_7ff651034140` → CC object `FUN_7ff651331010` at `M+0xcc0`: +0x04 character id,
+0x0c fixture id, +0x20 contact planes): a **character** (group = team `M+0x1070`, priority 0) with one capsule fixture
(friction 0.6, push limit FLT_MAX, margin 0.1):
- r = max(STU+0x0c, 0.02), s = STU+0x10 (step height), H = STU+0x08, L = max(0.1 r, H − 2r − s);
- **p1 = (0, r, 0), p2 = (0, r + L, 0)**, relative to the character position = feet + up·(s + R+0x1b8).

So the capsule covers [s, H] above the feet (standard hero: 0.5…2.0 m, r 0.5); the band below it is covered by the
ground probe (§3.3.2), which is how steps up to s are climbed. Crouching swaps the capsule (`FUN_7ff651331ae0`).

Filter (`FUN_7ff651038890`, via `FUN_7ff651331ca0`): category 8 (team 0) / 0x10 (team 1) / 0x18 (other, or ghost),
4 with FilterBits bit 22; mask 0 when collision is off, 0x1081 normally, **0x1499** (0x1481 with the bit-22 flag) when
`M+0x107b` = !(record flag bit 13) && STU+0x88 (1 on heroes) [G]. Team from FilterBits (component 0x10) bits 18…21
(`FUN_7ff650c89f20`).

[L] Genji (team 1) on Anubis: capsule radius 0.5, p1 (0, 0.5, 0), p2 (0, 1.0, 0); character position = feet + 0.5
(`M+0xcc0` → CC {+0 world 1, +0x0c fixture} → world `+0x330` fixture: category 0x10, **mask 0x1499**, group 0, margin
0.1, owner kind 2; the character's group 1 = team, priority 0); up `M+0x60` = (0, 1, 0); `M+0x107b` = 1 with flag
bit 13 clear. So heroes collide with both teams' characters (0x1499 ⊇ 8, 0x10).

### 4.4 The move: contact planes, plane solver, swept move (`FUN_7ff651331c90`)
Input from the mover (§3.3.7): displacement, dt', skin **0.005**, **20** outer iterations. Per iteration:
1. refresh every contact manifold of the capsule at the current position (`FUN_7ff6511baef0` → convex
   `FUN_7ff6511d2d10` / mesh `FUN_7ff6511c15b0`);
2. one **plane** per contact: n = the manifold normal pointing at the capsule, **w = n·pos − min(point
   separations)**, push limit = the other fixture's (FLT_MAX for the map); another character is skipped if our
   priority is higher (never: all 0); a **teammate** (same group) gets push limit = dt' (a soft push apart) and no
   velocity clip;
3. **plane solver** (≤ 200 iterations): target t = start + displacement; per plane an accumulated push
   p ∈ [0, limit]: sep = n·t − w; p' = clamp(p − (sep + 0.005), 0, limit); t += (p' − p) n; stop when
   max |p' − p| < 0.005;
4. **swept move** `FUN_7ff6511bb1d0` → `FUN_7ff6511baa10`: swept AABBs, pair update (new contacts), time of impact per
   contact (conservative advancement on the GJK distance, `FUN_7ff6511d1310` convex / `FUN_7ff6511c0450` mesh; ≤ 20
   iterations; teammates skipped), position = old + (t − old)·min TOI; stop when it moved less than 0.005.

Output: final position (res+0x210), the planes with push > 0 (res+0x200, count res+0x20c).
**Velocity clip** `FUN_7ff651331af0`: for each such plane with v·n < 0: v −= (v·n) n.

### 4.5 Narrowphase
Convex pairs via GJK (`FUN_7ff6511b3f70`) + SAT (dispatch `FUN_7ff6511d2170`): sphere–sphere `FUN_7ff6511d9870`,
capsule–sphere `…d99e0`, capsule–capsule `…d9c60`, hull–sphere `…d9e20`, hull–capsule `…dae70`, hull–hull (SAT, cached)
`…dd060`. Mesh pairs (`FUN_7ff6511c15b0`): a cached query box (the shape's AABB fattened by its margin) re-queries
the BVH only when the shape leaves it; per candidate triangle, **capsule–triangle** `FUN_7ff6511d3350`:
- GJK distance d between the triangle (radius 0.01) and the segment (radius r); d > rT + rC → no contact;
- shallow: if the GJK normal is within 5° of the triangle normal (cos 0.99619) → face contact (`FUN_7ff6511d4020`,
  segment clipped to the triangle, ≤ 2 points); else one point from the witnesses; **internal-edge rejection** with
  the triangle's wing vertices (drop a vertex / edge contact pointing into an existing neighbour);
- deep: SAT, an edge beats the face only if sep_edge > 0.9·sep_face + 0.0025, edges whose dihedral is within
  ±0.0349 rad (2°) are skipped; face → `FUN_7ff6511d4020`, edge → `FUN_7ff6511d3da0` (1 point).
Manifolds are reduced by `FUN_7ff6511bfa70`. Formulas of the deep paths are not transcribed; they are best validated
by replaying the recordings (§6).

### 4.6 Filtering
Pair filter `FUN_7ff651197640`: collide iff `(B.cat & A.mask) && (A.cat & B.mask)`; if both groups are non-zero:
same group → only if > 0; **different non-zero groups never collide** (unlike Box2D). Categories seen: 0x0001 map and
default model collision, 0x0004 / 0x0008 / 0x0010 / 0x0018 movers by team, 0x0100 / 0x1000 model collision variants
(`FUN_7ff6509c1d00`), 0x0800 flagged map items (in no mover mask: characters pass through them), 0x0080 / 0x0400 [?].
Ray / sphere casts (`FUN_7ff6511a1260`; game wrapper `FUN_7ff6509d76c0`, closest hit) use a one-sided or symmetric
test. **Ground probe rays**: mask 0x1091 (team 0) / 0x1089 (team 1) / 0x1081: a hero can stand on enemies, not
teammates; a hit counts only with surface-flags bit 0 [? "walkable"].

Characters vs characters: teammates share the group → soft push, no TOI clamp; enemies → hard planes and TOI (if
their masks include each other's category, §4.3). CollisionProxy (component 0x16, `FUN_7ff650cae430`) creates a
character with a sphere or capsule, filter {cat by team, mask 0x1099 turret / 0x1081}; CollisionProxySyncSystem
teleports it (`FUN_7ff6511bb2f0`) to the movement-state position; only turrets block movers.

### 4.7 Dynamic bodies, platforms, gravity areas
Model collision (`FUN_7ff6509c1d00`) creates bodies (category 1 / 0x100 / 0x1000) that the world steps **per render
frame** (`FUN_7ff6509d51f0` → `FUN_7ff6511a17c0`, dt ≤ 1/15, 8 / 2 iterations). The mover refreshes its own contacts
every CF, so it sees kinematic bodies at the pose of the last render-frame step [?]. Riding platforms is done in the
mover's frame (ground entity id R+0x244, `FUN_7ff6510356c0`: the entity's scene transform, optionally at a history
time). Gravity / dampening areas (components 0x09 / 0x0a): `PhysicsAreaRegistrySystem` only keeps an area → entity map;
no mover path reading it was found [?]; the mover's gravity is −10 × E+0x78 along M+0x60.

### 4.8 For a server
- Load the map's items exactly as the client (§4.2) with our map parser, build the same BVH queries.
- Run the same move (planes + solver + swept TOI) and the same capsule–triangle contacts; the results are
  quantised per CF (§3.4), which helps, but contacts near a 1/1024 boundary flip, so the narrowphase must match
  the client's operation order to stay bit-exact.
- Other players' capsules at their CF positions; doors / movers at their CF poses (UnsynchronizedMover is
  deterministic from the clock).

## 5. Replication

### 5.1 The movement record (0x270 bytes)  [G]
The unit of simulation (§3: the mover's state is its current record), prediction, history, interpolation and
replication. Sources: the step (§3), the snapshot reader `FUN_7ff65100aba0`, the compare `FUN_7ff65100b390`, the
server writer `server/inst/replication/entity.go`. "cmp" = compared exactly by reconciliation (§1.4).

| R+ | meaning | wire (reader) | cmp |
|---|---|---|---|
| 0x00…0x20 | block, flag 0x400 (+0x10: a position carried with the parent when 0x400 and 0x800) [?] | optional | if flag |
| 0x30…0x38 | block, flag 0x800 [?] | optional | if flag |
| 0x40…0x60 | MoveTo block (flag 0x10) | – | if flag |
| 0x70…0x8c | WallMove block (flag 0x80; quantised 1/1024) | optional (`+0x70`) | if flag |
| 0x90… | climb / LedgeClimb block (flag 0x20), compared with 0.004 tolerance (`FUN_7ff651008a90`) | optional (`+0x90`) | if flag |
| 0xd0 | Steer block (flag 0x40) | optional (`+0xd0`) | if flag |
| 0xd8… | list block (`FUN_7ff651008f90`) | optional (`+0xd8`) | yes |
| 0x120 | mirrored animation data | `FUN_7ff6510092f0` | – |
| 0x198 | **flags**: bit 0 jump latch, 1 crouched, 2 rising from a jump, 3 falling (2 \| 3 = airborne), 4 / 5 / 6 / 7 controller blocks MoveTo / climb / Steer / WallMove, 9 set by a jump, 10 / 11 optional blocks, 13 disables the alternate collision mask (§4.3), 15 cleared on a ground-entity change, 17 touched geometry in the last move | `FUN_7ff651008130` | yes |
| 0x19c | u32; bit 0 = 0: floats sent quantised (1/1024) | 1 bit | yes |
| 0x1a0 | CF of this state | varint delta vs the packet frame | – (the key) |
| 0x1a4 | sequence (reset counter): reconciliation hard-resets when it differs; the history insert rejects a smaller one | `FUN_7ff651007f00` | yes |
| 0x1a8 | airborne CF counter | prefix delta | yes |
| 0x1ac | gravity scale (the effective E+0x78) | `FUN_7ff65100ef60` | yes |
| 0x1b0 / 0x1b1 | this CF's axes right / forward (s8 × 127) | `FUN_7ff65100c8a0` | yes |
| 0x1b2 / 0x1b3 / 0x1b4 | the last non-zero axes and their CF | bytes / = +0x1a0 unless sent | yes |
| 0x1b8 / 0x1bc | step-spring offset / velocity (1/1024) | `FUN_7ff65100ef60` | yes |
| 0x1c0…0x1ff | cached parent world matrix | – | no |
| 0x200 | body rotation quaternion (yaw + roll, parent-local) | `FUN_7ff65100ce60`: yaw, pitch, roll as q / 4096 rad | yes |
| 0x210 / 0x214 / 0x218 / 0x21c | pitch / yaw / roll (1/4096), valid | (from the quaternion) | – |
| 0x220 | **position** (feet, parent-local, 1/1024) | `FUN_7ff651008200` | yes |
| 0x230 | **velocity** (parent-local, 1/1024) | – | yes |
| 0x240 | aim pitch (1/4096) | `FUN_7ff65100eff0` | yes |
| 0x244 / 0x248 | ground / parent entity id, bone | 1 bit + two values | yes |
| 0x250 | accumulated fall velocity (not quantised) | `FUN_7ff651007f80` | yes |
| 0x260 | EA pointer for the parent lookup | – | no |
| 0x268 | byte: provisional (history) | – | no |

[L] Recorded Genji records: position / velocity multiples of 1/1024; +0x1ac = 1.75; +0x1a8 counting airborne CFs
from 1; +0x19c = 0.

### 5.2 Snapshots  [G]
Entity snapshots arrive with the entity updates and are queued on MovementStateSystem (`sys+0x1b0`); applied at the
start of its tick by `FUN_7ff650c06ce0`: read the record as a delta against the entity's record at (packet frame −
d) or the baseline `DAT_7ff651890390`, insert it into the entity's `0x12` history at its CF time; for the **local
pawn** also hand it to reconciliation (§1.4). The server today sends one snapshot at create (`Mover{Pos, Yaw}`).

## 6. Verification plan and recordings

### 6.1 Recorder
No Prometheus change needed: the state the mover produces per CF stays in memory long enough to be read in bulk.
`systems/data/movement/tools/record_movement.py` (T062) polls every 0.3 s, through MCP `read_memory_abs`:
- the controller's input history (`0x2f+0x810`: ring of 0x18-byte entries) → the command of every CF;
- the pawn's `0x12` second history (`+0xa300`: records at `+0xa720`, 64 × 0x270, [L] the local pawn's records) → the
  record of every CF, read in whole records per MCP call (a record split over two calls can be torn across frames);
  a changed record for a CF already seen (a rewind / replay) is kept under `replaced`;
- once per recording: the mover component `0x15` (0x10e0 bytes) and the pawn's `0x12` header,
and writes per CF {cf, command, record bytes (+ decoded position / rotation / flags)}. Inputs are driven with
`logical_button` (press / release by name) and view angles are left fixed (or set via the view direction).

### 6.2 The set, and what each must show
| recording | inputs | expected (to confirm) |
|---|---|---|
| `idle_flat` | none, on flat ground | position constant, records every CF with consecutive CFs; command repeats; confirms the recorder and which history (+0x20 or +0xa300) holds the local records |
| `walk_forward` | forward held 2 s, release | speed ramps to the max ground speed (§3 constants) with the stage-C/D acceleration, then decelerates by the friction constant; per-CF displacement = speed × 0.016 |
| `strafe_diagonal` | forward + right | diagonal speed normalised (not √2 faster) [?] |
| `jump` | jump tap on flat ground | vertical velocity = `m_jumpSpeed`, gravity per CF from §3, landing CF; flag bits of the airborne state |
| `double_jump` (Genji) | jump, jump in the air | the second jump's impulse from Genji's Statescript (a mover controller or a velocity set) |
| `stairs`, `slope` | forward into stairs / up a ramp on Anubis | step-up height, slope handling, ground normal fields |
| `wall` | forward into a wall at an angle | slide along the wall (velocity minus normal component) |
| `dash` (Genji Shift) | ability_1 | the Accelerate / MovementMod controller taking over stages |
| `wall_climb` (Genji) | forward into a wall held | the climb controller (kind 12) |

Fixtures go to `systems/data/movement/` (JSON per recording, with the hero, map, spawn position and the mover
component bytes so a library can start from the same state).

### 6.3 Recorded (2026-09-27, Genji, Temple of Anubis attacker spawn room, instance server)  [L]
`systems/data/movement/*.json`, one per recording: {name, note, controller / pawn ids, steps and their MCP results,
start / end mover (0x10e0 bytes) and `0x12` head, frames [{cf, input (§2.2 entry), record (decoded), raw record hex,
replaced}], gaps}. Every recording is gap-free (consecutive CFs) and none had a rewritten record (no reconciliation:
the server sends no movement snapshots after the create). Unless marked `_sim`, the instance ran with `--shadow`
(the client's Statescript unopposed); each starts from a fresh body (hero switch Tracer → Genji; the three spawn
points rotate). `tools/summarize.py` prints a compact per-CF view.

| recording | CFs | what it shows | spec check |
|---|---|---|---|
| `idle_flat` | 196 | nothing moves; command repeats | recorder |
| `walk_forward` | 296 | 0 → 3.359 → 5.279 → 6.0 m/s, a step-up (y +0.69 in one CF), sliding into walls (flag 0x20000) | §3.3.5 accelerate / friction exact; §3.3.4 |
| `walk_backward` | 253 | steady 5.399 (= 5.4 truncated to 1/1024) | backward speed |
| `strafe_diagonal` | 253 | forward + right: 6.0, not 8.5 | dirMax cap |
| `crouch_walk` | 253 | flag 0x2, steady 3.0 | crouched speeds |
| `jump` | 218 | 5.92, −0.28 / CF rising, drag while falling; Genji's 9.3 boosts (§3.7) | §3.3.2 jump, gravity, drag |
| `jump_forward` | 253 | jump while running into the stairs / wall | air control, contacts |
| `double_jump` | 240 | two taps; Genji's controller boosts | §3.7 |
| `dash` | 240 | Swift Strike: ~50 m/s for ~0.3 s, flags 0x38 (MoveTo block 0x10) | MoveTo controller |
| `wall_climb` | 328 | forward into the wall + jump held: climb blocks 0x20 / 0x80 | climb / WallMove |
| `long_walk` | 383 | forward 4 s across the room: steps, walls | collision (§4) |
| `jump_sim`, `double_jump_sim` | 230 each | normal instance mode (server Statescript): first jump boosted, later ones plain | §3.7 note |

Not recorded: a slope (none found near the spawn), knockback, two characters colliding (needs a second client).
Measured values of the capsule and filter: §4.3.

