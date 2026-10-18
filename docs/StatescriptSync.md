# Statescript sync protocol (client build 24919): specification

What the unmodified client expects from a server for Statescript, read from the client's own code (T052).
It covers where the traffic arrives (§1), the packet grammar (§2), how packets are queued and applied (§3), the
state data of every state type (§4), the client's prediction / rollback / roll-forth model (§5), and what a server
must send, with the past live symptoms explained (§6). Verification tool: §7.

**Tags.** [G] read in the client binary (Ghidra), [L] measured on the live client, [?] inferred. A claim
carried over from an earlier doc names that doc.

**Addresses.** `G:xxxxxxx` = Ghidra address `0x7ff650000000 + xxxxxxx` (so `G:cec290` = `FUN_7ff650cec290`,
`G:1050590` = `FUN_7ff651050590`). RVA = Ghidra − `0x7ff64ffe0000` (Prometheus uses RVAs).

**Objects.** `comp` = Statescript component 0x23; `sm` = its sync manager (`comp+0xC0`, StatescriptRuntime.md
§1.6); `so` = the net-sync object (`sm+0x500` = `comp+0x5C0`); `pkt` = a received StatescriptPacket (0x60 B);
`g` = a graph (`STUStatescriptGraph`, StatescriptRuntime.md §1.2).

Background: the GDC model (Statescript.md §6–9) maps onto this code almost 1:1; §5 lists where the binary
differs.

---

## 1. Where Statescript traffic arrives

- **Stream 1 of the entity-data packet**, one record per entity (system 22, StatescriptSystem). The entity
  reader `G:c10110` passes the message header (`+0` entity, `+0x1C` the header's frame P, `+0x24` entity id)
  to `G:c10140` [G]. A second entry `G:c104a0` looks the entity up by id in a hash map first and then does the
  same. Which message uses it is not traced [?].
- `G:c10140`: **present bit** (0 = nothing for this entity), one more bit (read, ignored), then a StatescriptPacket
  (§2.1) [G].
  - If the entity, its component 0x23, or `comp+0xF70` (component ready) is missing, or the entity admin's
    `+0x178 == 1` [?: meaning unknown], the packet is parsed into a standalone record and **buffered for
    8000 ms** of game time (system list at `+0x50`). Every tick `G:c0f940` hands buffered packets to their
    entity once it is ready (`G:cec130`), and drops them after the 8 s [G].
  - Otherwise it is parsed straight into one of the sync object's packet slots (4 inline + heap,
    `G:cf1e10`) and queued (`G:cec130`, §3.1) [G].
  - For a non-local packet `pkt+0x4C` (its CF) is the P of the message header [G].
- **Nothing else carries Statescript state.** Game messages are never sent over the network (MatchLogic.md
  §8.4, StatescriptRuntime.md §3.10) [G].
- **The client sends no Statescript acknowledgement.** Outside the apply path nothing reads the last applied
  CF (`so+0x984`) [G, from the absence of other readers]. A server knows what arrived only from the transport's
  packet acks (InstanceServer.md §4.1).

## 2. Wire grammar

Bits are LSB first, as everywhere in the entity-data packet. Number encodings [G]:

| name | reader | encoding |
|---|---|---|
| `bit` | `G:813380` | 1 bit |
| `u<n>` | `G:e29520` | n raw bits |
| `cf` | `G:1050590` | `0`+u16 \| `1 0`+u24 \| `1 1`+u32 |
| `count` | `G:10507e0` | `0`+u8 \| `1 0`+u16 \| `1 1`+u32 |
| `small` | `G:1050a30` | `0`+u4 \| `1 0`+u8 \| `1 1`+u16 (result u16) |
| `set(n)` | `G:1050c90` | changed set of n bits: n ≤ 8 → n bits; else one "present" bit per 8-entry chunk (`(n−1)/8+1` bits), then the 8 bits (fewer for the last chunk) of each present chunk |

(The first prefix bit selects the short form: `0` = short. That is the opposite of the frame varint in the
packet header, CommandFrames.md.)

### 2.1 StatescriptPacket (`G:cf6770`)
```
packet   := cf(start) count(range) frag count(len) payload[len bits]      end = start + range
frag     := '0'                                   (not fragmented: pkt+8 = −1)
          | '1' small(index) bit(last)            (fragment `index`; `last` on the final one)
```
- **start = 0 is a full update**, anything else a delta [G] (Statescript.md §7.3).
- Fragments: once the fragment with `last` = 1 and index n has arrived, and all indices 0…n with the same
  [start, end] are queued, `G:cec440` concatenates their payloads in index order into one packet (frag = −1)
  and drops the pieces [G]. A fragment is never applied on its own.
- `so+0x798` keeps the largest end seen [G].

### 2.2 Payload
```
payload   := hdr subframe
hdr       := bit(ignored) bit(local) [local: bit(rollback)]            read once per packet (G:ce89f0)
subframe  := [local: count(cfDelta)] instances body
```
- `local` (`pkt+0x50`): the payload is in the **local format**. That is the format for the one entity the
  recipient controls (Statescript.md §6.2). `rollback` (`pkt+0x51`): the packet confirms predictions (§5).
- Local only: every sub-frame starts with `count`, **added** to the packet's CF (`pkt+0x4C`, 0 at the start).
  So the first sub-frame gives the absolute CF, and later ones give offsets [G]. A non-local packet's CF is
  the message header's P (§1) [G].
- The reader remembers the position and CF at the start of each sub-frame (`pkt+0x54`, `+0x58`). A sub-frame
  whose instance list fails (§2.3) is rewound there, and the packet stays queued to resume at that sub-frame [G].
- `body` is §2.4 (start = 0) or §2.5 (delta).

### 2.3 Instance list (`G:cec990`)
Before the list, the loader (`so+0x40`, `G:1051020`) refreshes its pending graph loads. If a graph failed to
load, it logs "Failed to load statescript graph", removes the entry and **fails the sub-frame** [G].

```
full:   instances := { small(idDelta≠0) record }* small(0)                  (G:cecab0)
delta:  instances := { small(idDelta≠0) entry }* small(0)                   (G:cecba0)
        entry     := '1'                     destroy the known instance with this id
                   | '0' '0'                 known instance, unchanged record (G:cee0e0)
                   | '0' '1' record          new (or re-stated) instance
record  := u16(graphIndex) owner parent                                     (G:ced910 / G:cee0e0)
owner   := '0'                               the entity itself (EA+0x450's id)
         | '1' u32(entityId)
parent  := '0'                               a top-level instance
         | '1' count(parentId) count(slot)   a SubScript child: parent instance id + the parent's m_states index
```
- id = running sum of the deltas (i16). Graph GUID = `0x0580000000000000 | graphIndex` [G].
- The known-instance table (`so+0x58` = loader `+0x18`, 0x28-byte entries `{+0 owner, +4 id, +6 parent,
  +8 slot, +0x10 graph GUID, +0x18 resource handle, +0x20 loaded graph}`) holds every instance the server has
  created [G].
- `record`: `G:1051210` adds the entry and requests the graph resource. If the id is already known, the
  existing entry must match graph, parent and slot, otherwise the sub-frame fails. **A record whose graph
  is not loaded yet also fails the sub-frame.** The packet then stays queued and is retried every CF until the
  asynchronous load completes [G].
- `'0' '0'` for an id that is not known fails the sub-frame [G].
- `'1'` (destroy) for an id that is not known is a no-op: `G:cecba0` leaves the entry's id 0 and `G:ced2f0` skips
  it [G, T053]. So a server may destroy an instance the client may never have received (a union resend).
- After the list, every new entry with a parent must name a known parent, otherwise the sub-frame fails [G].
- **Destroy** (`G:ced2f0`, run before the body): destroys child entries of this id first, calls the instance's
  `Stop()` (vfunc +0x1B0 = `G:f300e0`), drops the loader entry and zeroes the entry's id. **A destroyed instance
  has no section in the body** [G].
- **Create** (`G:ced030`, run before the body for new entries): parents first. Top-level: sm vfunc +0x18
  CreateInstance(graph, id, kind 2, mirrored, owner). Child: reuses a cached child of the parent (`G:f2c710`)
  or creates one, then writes the child id into the parent's state object at `slot` (`state+0xA8`: the
  SubScript) and child `+0x1A` = parent, `+0x1C` = slot [G].
- **A delta need not list every instance**: instances it does not name keep their state, and only listed
  ones have a body section [G]. A full update names every instance, and **every instance of the sync manager
  it does not name is stopped** (`G:ced430`: all of `sm+0xF0`, including the client's own unsynchronized
  instances with negative ids [G; not tested live]).

### 2.4 Full update body (`G:ced430`)
```
full   := ownerVars { ivars states [local: events] }* actions               one section per listed instance, list order
```
1. First every prediction record is freed (`G:cec050`), and the entity (owner) var bag is **cleared**
   (bag vfunc +0xD0) [G].
2. `ownerVars := { count(id≠0) value }* count(0)` (`G:cee050`): var `0x0D80000000000000 | id` in the entity bag [G].
3. Per listed instance, in list order:
   - `ivars` (`G:cedf30`): one `bit` per `m_syncVars` slot whose var id ≠ 0 and whose `+0x10` (`9e2a316c`) = 0.
     If the bit is 1, a `value` follows. Local only: then `{ count(id≠0) value }* count(0)` for other vars of the instance [G].
   - `states` (`G:cedbe0`), §2.6 (full form).
   - local only: `events` (§2.8).
4. `G:cf1b90` (runs the sync object's pending mirrored-data callbacks), then `actions` (§2.9) [G].
5. Instances of the sync manager that were not listed are stopped (§2.3) [G].

### 2.5 Delta body (`G:ced600`)
```
delta  := ownerVars { ivarsD statesD }* [local: eventsTail more] [not more: actions]
```
- `ownerVars` as in §2.4, but the owner bag is **not** cleared (a delta cannot remove a var) [G].
- Per listed, non-destroyed instance, in list order:
  - `ivarsD` (`G:cee790`): `set(|m_syncVars|)` over **all** slots, then the values of the set slots in slot
    order. Local only: then `{ count(id≠0) value }* count(0)` [G].
  - `statesD` (`G:cee430`), §2.6 (delta form).
- Local only:
  `eventsTail := '0' | '1' { '0'|'1' small(Δ≠0) events }* small(0)`: a signed running instance id. The sign
  bit comes first (1 = negative), then `small`, and a `small(0)` ends it. Each named instance's `events` (§2.8)
  replace its pending ones [G].
  `more := bit`: 1 = another sub-frame follows (§2.2), applied in the same pass (§3.2) [G].
- `actions` (§2.9) only when `more` = 0 (so only in the last sub-frame), and always for a non-local packet [G].

### 2.6 States
Which list the state indices run over [G]:

| format | full update (`G:cedbe0`) | delta (`G:cee430`) |
|---|---|---|
| local | every `m_states` entry (`g+0x60`) that is neither client-only (`+0x75`) nor server-only (`+0x76`); no bit for the others | `set(|m_states|)` over **all** entries |
| remote | every `m_remoteSyncNodes` entry (`g+0x120`) that is a state (IsA `STUStatescriptState`) and neither client- nor server-only; then an `included` bit, 0 = leave it alone | `set(|m_remoteSyncNodes|)` over **all** entries (actions included) |

Per state that is present (full) or set (delta), with `S` = one of the three **"has update"** types
`7bf31629`, `860ee5fc` (WeaponVolley), `ffbfa88d` (`G:ecfee0`; every other type is plain) [G]:

```
full,  plain:  '0'                        deactivate if active
             | '1' data                   activate if inactive (re-entering an active one does not happen here), then data
full,  S:      '0'                        no update
             | '1' '0' data               deactivate if active, then data
             | '1' '1' data               activate if inactive, then data
delta, plain:  '0'                        deactivate if active
             | '1' data bit               activate if inactive, data, one bit read and dropped
delta, S:      '0'                        no update
             | '1' '0' data bit           deactivate if active, data, dropped bit
             | '1' '1' data bit           activate if inactive, data, dropped bit
```
- `data` = the state's **state data** (§4): the reflected mirror fields, then the class's own reader
  (vfunc +0x1F8), with `local` = the packet's local flag and `full` = (start = 0) [G]. The full update reads it
  through `G:f289e0`, the delta through `G:f28a40` + vfunc +0x1F8 with `full` = 0. `full` changes the format (§4).
- **The dropped bit** exists only in deltas and only after data (active plain states and every S state) [G:
  its value is never tested at `G:cee6ca`]. Leaving it out shifts the rest of the packet (T051 wedges).
- A state may read its own data **during its activation**: the reader stores the packet in `so+0x988` and the
  state object in `so+0x990`; a state that calls the instance's vfunc +0xB0 (`G:d540e0`) reads its data right
  there, and the reader then skips its own read. The bits are the same either way [G].
- Remote full update only: afterwards every active state whose node is not a remote sync node (`+0x6C` = −1)
  and whose object `+0x80` is 0 is deactivated [G] (MatchLogic.md §9).
- The node's state index is `node+0x68`, and for remote lists its remote index is `node+0x6C` [G].
- **Remote delta, action entries**: the set runs over every `m_remoteSyncNodes` entry, but `G:cee430` reads the
  active bit of any set entry and on `1` takes the entry's state object, which is null for an action, and
  dereferences it (crash). `0` is harmless (the deactivate path checks the object's type). A server sets only state
  entries [G, T053].

### 2.7 Values (`G:cef290`)
```
value := stateRef u4(tag) [tag = 15: u3(sub)] body
stateRef := '0'                                                       no writer (null user)
          | '1' ( '0' u<g+0x17C>(stateIndex)                          form 0: a state of this instance (instance vars only)
                | '1' count(instanceId) small(stateIndex) )           form 1
            bit(priority) ( '0' | '1' small(extra) )                  priority → user bits 0–6 (0 or 1); '1' → bit 7 "state-defined" + extra in bits 8+
```
The "user" is the var's writer (StatescriptRuntime.md §1.7). Form 0 on an owner var dereferences a null
instance and crashes the client (MatchLogic.md §14.4) [G].

Bodies (`G:cef780`, `G:cf0730`; T040's table in MatchLogic.md §14.4, re-checked here):

| tag | body | client value |
|---|---|---|
| 0 | bit | INT 0/1: writes the byte but not the type, which stays INT (`G:cf076c`), so **BYTE cannot be sent** [G] |
| 1 / 2 / 3 / 4 | u4 / s8 / s16 / u32 | INT [G] |
| 5 / 6 / 7 / 8 | u4 as float / s8 as float / half / f32 | FLT [G] |
| 9 | u16 hi, if hi ≠ 0 `count` lo | INT64 `hi<<48 \| lo` (bits 32–47 cannot be sent) |
| 10 / 11 / 12 | 2 × u16 direction / u3 mask + halves / u3 mask + f32s | VEC3 |
| 13 | bit, 1 → u32 | ENTITY |
| 14 | u3, count, small, small | INTVEC4 |
| 15/0, 15/1, 15/2, 15/3 | 4 × u16 / u32 / nothing / count | FLT16VEC4 / ColorRGBA / (no write) / SS_INSTANCEID |
| 15/4–7 | dictionary (below) | Dictionary var |

- The write is a **forced network write** of the var `0x0D80000000000000 | (id & 0xFFFF)` in the owner bag (no
  instance) or the instance's bag (`G:f449c0`, last argument 1) [G]. Only the low 16 bits of an id travel.
- **Strings cannot be synced.** A STRING body reader exists (`G:cf0e5d`, kind 4), but no tag reaches it [G].
- **Dictionary** (`G:cf1070`): 15/4 and 15/5 = range update `small(newSize) small(first) small(tail)`: the
  array is resized to newSize, its last `tail` elements are kept at the end, and entries first … newSize−tail−1
  follow. 15/6 and 15/7 = full `small(size)` (first = tail = 0). Size 0 clears it. Then `bit(homogeneous)`.
  15/4 and 15/6 carry values only (key = index), 15/5 and 15/7 key + value. Each element is `u4 tag [u3 sub] body`
  with no stateRef. With homogeneous = 1, every element after the first uses the first element's type with no
  tag. Send homogeneous = 0 [G].

### 2.8 Events (`G:cf1620`, local only)
The instance's pending Timer / Lifetime-finish notifications (StatescriptRuntime.md §3.4) are **replaced**.
Notifications marked `+0x28 & 4`, or aimed at a state whose object `+0x80` is set, are kept and merged back by
time [G].
```
events := { kind stateIdx time }* '11'
kind   := '00'                            Timer, data 1          (kind is a u2 value: 0, 1, 2; 3 ends [G, T053])
        | '01' ( '1' | '0' small(data) )  Timer, data 0 | data
        | '10'                            Lifetime finish (type 0 notification, +0x30 = 1)
stateIdx := u<g+0x17C>                    m_states index (m_statesBitCount)
time   := '1'                             same as the previous event
        | '0' '1' count(Δ)                later by Δ ms
        | '0' '0' count(Δ)                earlier by Δ ms
```
The time base for the first event is **the sub-frame's CF × 16 ms** (`so+0x980` × `sm+0x360`) [G].

### 2.9 Actions (`G:cee920`)
Actions the server executed, for the client to execute too [G]. This section was not documented before T052.
```
actions := { '1' cf(CF) count(L) records '0' }* '0' { '1' record }* '0'
records := { '1' record }*                          L = the bit length of `records`
record  := ( '1' small(instanceId) | '0' )          '0' = the previous record's instance; no instance → stop reading
           nodeIdx
           ( '0'                                     execute: sm+0x3D0 = node+0x70 (EA scope), vfunc +0x100(obj, 0, instance, node)
           | '1' )                                   vfunc +0xE8(obj, stream, instance): the default no-op for every action class
nodeIdx := local:  u<g+0x178>                       m_nodes index (m_nodesBitCount)
         | remote: u<g+0x180>                       m_remoteSyncNodes index (m_remoteSyncNodesBitCount) → its node +0x68
```
- **Timed blocks** run only if `CF > so+0x984` (newer than the last applied packet's end). Otherwise the reader
  skips L bits and the terminating `'0'`. This is how overlapping (union) resends avoid running an action twice [G].
- **Untimed records** always run [G].
- A record's first bit is `'1' small(instance)` for the first record of each timed block: the "previous instance"
  is reset at every block, and `'0'` without one ends the whole section [G, T053].
- The widths `m_nodesBitCount` (+0x178), `m_statesBitCount` (+0x17C), `m_remoteSyncNodesBitCount` (+0x180) are STU
  fields of the graph asset (T054). On 17 lobby graphs +0x178 = max(1, bit length of (nodes − 1)) [L, T053].
- No action class overrides vfunc +0xE8 (all 87 action vtables in the node catalogue hold the no-op `G:580c20`),
  so `'1'` = "don't execute" [G]. Only UX states override that slot [G].
- An empty section is `'0' '0'`. At the very end of the payload the second `'0'` may be left out, because
  reading past the end returns 0 [G].
- For a remote entity, `nodeIdx` goes through `m_remoteSyncNodes`: remote clients execute only SYNC_ALL nodes
  (Statescript.md §9.1) [G + GDC].

### 2.10 Worked example: the local rollback delta T051 sends
`hdr` = `0 1 1`, `count(CF)`; instances (all known: `small(Δ) 0 0` each, `small(0)`); `count(0)` (no owner
vars); per instance `set(|m_syncVars|)` + values, `count(0)`, `set(|m_states|)` + per changed state (§2.6
delta); `eventsTail` = `1` + per instance `0 small(Δ) events` + `0 small(0)`; `more` = 0; actions `0 0`.
`server/inst/replication/delta.go` writes exactly this grammar (checked against it field by field, T052).

## 3. Queueing and applying

### 3.1 Queue (`G:cec130`)
On arrival, a packet whose end ≤ `so+0x984` (last applied end) is dropped. Otherwise every queued packet
inside its range (start ≥ its start and end ≤ its end; with an identical range only when the fragment index
matches) is dropped, and the packet is queued (`so+0x7A0` list, `+0x7A8` count) and marks the sync manager
for processing (`sm+0x359`) [G]. This is the talk's union rule (Statescript.md §7.4): a union resend
replaces the lone packets it covers.

### 3.2 Apply loop (`G:cec290`, called by `G:ce9470` every Statescript step of the entity)
- It waits while any instance of the sync manager has no graph yet (`inst+0x160` = 0) [G].
- Fragments are reassembled first (`G:cec440`, §2.1) [G].
- Then, repeated until nothing changes:
  ```
  so+0x960 == 0               → drop (sync broken; never set in this build, §3.4)
  pkt.end <= so+0x984         → drop (already covered)
  pkt unfragmented and pkt.start <= so+0x984 + 1:
      if sm+0x4D0: G:cea6b0(so); G:f248d0(sm)
      apply (G:cec7b0); applied → drop
  else                        → stays queued (a gap waits for a packet that covers it, with no timeout)
  ```
  [G] (T051 report §1).
- **The client's own CF is never compared**: a packet ahead of the client is applied at once [G].
- `G:cec7b0` refuses (the packet stays queued) while roll forth is running (`so+0x793`) [G].

### 3.3 One packet (`G:cec7b0`)
```
save so+0xC4 and the input state so+0x18..0x27
while the instance list parses (G:cec990):
    prev = so+0x980; so+0x980 = pkt CF (local with CF 0: the client's current CF)
    if no prediction records (so+0xA0 == 0) and not local:          plain replication
        begin apply; G:cecd90; end apply; restore; applied; break
    if not in apply: begin apply (sm vfunc +0xF8); if rollback: ROLLBACK (G:ceaf90)
    elif local: G:cf1d50(so, so+0x980 − prev)                       shift active states' +0x68 by the CF gap × 16 ms
    G:cecd90 (apply the body)
    if not more: applied; break
if in apply: end apply (+0x100); if rollback: restore so+0xC4/input; ROLL FORTH (G:ceb420)
if applied: so+0x984 = pkt.end; return 1
```
[G] (T051 report §2).
- `+0x984` becomes the **header's end CF**, while the clock goes to the **payload CF** (`+0x980`) [G].
- If a later sub-frame's instance list fails, the earlier sub-frames stay applied, the roll forth still runs,
  and the packet stays queued. It resumes at the failed sub-frame next time [G].

### 3.4 The body (`G:cecd90`)
- `so+0x960` = 0 → nothing (sync broken) [G].
- Local + rollback: before the body, the clock and the input state go back to the sub-frame CF: `so+0xC4` =
  CF, `so+0xB0` = CF × 16, the recorded input at CF (`G:ceae50`), `sm+0x2C4` = CF, `sm+0x2D0` = CF × 16. The
  client clock object is set too. Each mirrored-data object gets `G:f28e50(obj, (CF+1)×16, id)` [G].
- Creates (§2.3), then destroys, then the full (`G:ced430`) or delta (`G:ced600`) body [G].
- A body that fails would set `so+0x960` = 0 and drop every later packet for the entity (only the constructor
  sets it to 1, and only here is it cleared). **But in this build both body readers always return 1**, so this
  never happens [G]. A misparse is never detected as such. The reader goes on through garbage, and the damage
  shows up later [G]:
  - a wrong `more` bit (local deltas) makes the client parse the rest as another sub-frame. Its instance list
    then almost always fails (an unknown `0 0` id, a new record with an unloaded or bogus graph), so the
    packet stays queued with that sub-frame rewound, **forever**. `+0x984` never advances, and every later
    delta waits behind the gap (§3.2). This is T051's "waits behind it forever" / "13,000 pending" wedge [G, L].
  - garbage that reaches a reader which dereferences it, e.g. a form-0 state reference on an owner var or an
    out-of-range index, crashes the client (T051's crash in `G:cf1520`) [G, L].
- Local + rollback, after the body: `so+0xC4` = CF + 1, the clock = (CF + 1) × 16, the input state at CF + 1,
  then `G:f25680(sm)` (rebuild the active list) [G].

## 4. State data  (stage 2)

The bits after a state's active bit (§2.6) are the **state data**. They are read by `G:f28a40` + the class
reader, both called with `local` = the packet's local flag and `full` = (start = 0) [G]:

1. **Reflected mirror fields.** vfunc +0x208 appends the state's mirror objects (STU `mirror` types,
   Statescript.md §8.6). For each object the walk reads the fields of its own type info (`+0x28` array of
   0xA0-byte entries, count `+0x54`; parent types are not walked). Fields flagged **0x8 are never read, 0x10 only
   in the local format, 0x20 only in a full update**. Each field's type reads its value with vfunc +0xB0 [G].
2. **The class reader**, vfunc +0x1F8 `(state, stream, local, full)` [G].

Generated table: **`systems/data/statescript/state_data_formats.json`**, generator
`systems/data/statescript/tools/gen_state_data_formats.py`. The generator works mechanically for every state type:
- the effective client class: the type's highest-priority registration, else its nearest registered ancestor
  (StatescriptRuntime.md §1.8);
- its vtable slots +0x1F0 / +0x1F8 / +0x208 from the binary;
- mirror fields and flags from the client STU registry;
- plus a hand-read grammar for each of the 26 distinct reader functions and the 3 distinct mirror lists.

The grammar is a small DSL (`bit`, `u<n>`, `small`, `count`, `scount`, `pred`, conditionals). For each type
the table gives `local` and `remote` formats, minimum lengths, whether the length is fixed, the reader/writer
addresses, and notes.

### 4.1 What the generator found [G]
- **259 state types. 226 have no data at all**: their class has neither a reader nor a mirror list
  (+0x1F8 and +0x208 are the no-op `G:580c20`). That includes every type without a client class of its own
  (73 in the catalogue, e.g. HealthPool, ModifyHealth, ApplyAura) and presentation types such as UXScreen,
  LogicalButton, Watch and DataFlowMapping.
- **Only one reflected field is ever read**: the Ability mirror (`efd4b39b` at state +0xD0) has one field without
  0x8, the flags byte at state +0x15C (u8). The other four mirror types flag every field 0x8:
  TrackMovement `9a97a6c3` and BooleanSwitch `ff61af77` at +0xA8, ChaseVar `28c44860` and `c6366130` `4d83830d` at
  +0xB0. No mirror field has 0x10 or 0x20.
- **`full` never changes a format in this build**: no field is flagged 0x20, and the readers that receive `full`
  use it only to decide how to apply (WeaponVolley, Effect), not for which bits to read.
- **The local and remote formats differ** for Stack (remote: predecessor only), Ability (remote: no priority part),
  WeaponVolley (remote: no begin offset), SubScript (remote: nothing), Extender (remote: nothing) and
  BlinkEffect (**local: nothing**, remote: u32).
- **Reader / writer asymmetry**: the client classes of Effect (207 uses), UXFade, `3f67b31b` and HistoryRewinder
  have a reader but a no-op writer (+0x1F0). The client never writes their data, so **a serialization made with the
  client's own writers (Prometheus `ss_serialize_local`) produces 0 bits for them while the reader expects 2 / 1 /
  9 / 16 bits** [G]. Conversely UXScoreboardScreen and `26b2c9f4` have something in the +0x1F0 slot but read
  nothing [G; probably a different vfunc in those UI classes, not checked].

### 4.2 Formats (every type with data; `pred` = stack predecessor, §4.3)

| type | uses | local format | remote |
|---|---|---|---|
| Stack `f7d6498c` | 823 | `pred`, [node `m_priority` set and not a StackPriority: u32 priority, bit], bit on-top, bit under | `pred` |
| BooleanSwitch `e811ecbb`, TrackMovement `8de7e50f` | 782, 12 | bit value (the shared reader `G:f35560`) | same |
| ChaseVar `970ec198` | 263 | bit isVec, bit hasDuration, bit reached; isVec ? 3 × f32 : f32; [hasDuration: bit wide, wide ? u32 : u16 remaining ms]; bit closed, [closed: count((CF+1)·16 − last tick)] | same |
| Anim `7fb4bf64` | 259 | u3 serial, bit has-anim, [bit has-time, [count time ms]] | same |
| **Effect `ffbfa88d`** (has update) | 207 | **u2 serial** (low 2 bits of the state serial +0x44) | same |
| TrackTargets `498b421a` | 109 | bit has, [small n, n × (u32 entity id, count age ms)] | same |
| SubScript `3db862c4` | 77 | small child instance id, bit | **nothing** |
| WeaponVolley `860ee5fc` (has update) | 65 | scount(CF·16 − begin), bit, [small volley counter] (else 1), u6 serial | without the begin offset |
| Ability `f53c5da8` | 63 | u8 flags (mirror field); [node `0x92c13ac6` set and not a StackPriority: u32 priority, bit]; bit cooldown, [f32 cooldown, bit, [f32 rate], count((CF+1)·16 − start)]; `pred` | no priority part |
| **Switch `26367267`** | 35 | one bit per `m_cases` entry (≤ 64), **then one more bit** (+0xB0) | same |
| LimitAim, PhaseOut, TimeScaleAnimation, AimSpeed, WeaponAim, ImpactEffectOverride (`G:f2af00`) | 34, 13, 2, 2, 0, 0 | `pred` | same |
| Invisible `7331cb24`, CameraTransform `a5f89e3f`, CameraChase, `2785fbdd` | 20, 20, 1, 1 | `pred` (then the class applies it) | same |
| UXFade `01419d1c` | 6 | bit | same |
| `3f67b31b` | 3 | bit, u8 | same |
| Extender `26cb90c0` | 2 | scount(CF·16 − begin) | nothing |
| BlinkEffect `429991a7` | 2 | **nothing** | u32 |
| HistoryRewinder `7211cf70` | 1 | u16 | same |
| ScaleTime `7bf31629` (has update) | 1 | bit, u32 | same |
| SprayEffect `1787a82d` | 1 | bit has, [2 × u16 direction, 3 × u32, 2 × u16 direction, bit, [bit, count entity, bit, [count entity]], bit] | same |
| Counter, Sequencer | 0 | count, bit / small n, bit, bit b, then n (if b) + 1 indices of bit-length(n−1) bits | same |
| TrackRay, UXHeroHelp, c6366130 | 17, 1, 2 | nothing (TrackRay's reader only prunes old entries) | same |

`uses` = nodes of that type in the 296 graphs resident on Anubis (node catalogue). Encodings: `small` 0+4 | 10+8 |
11+16, `count` 0+8 | 10+16 | 11+32, `scount` = sign bit + `count`; f32 = 32 raw bits [G].

### 4.3 The stack predecessor (`G:f2b360`)
`bit` (0 = none); if 1: `small(instance id)`, then the predecessor's state index: local `u<its graph's
m_statesBitCount>`, remote `u<m_remoteSyncNodesBitCount>` as an index into that graph's `m_remoteSyncNodes`
(→ node +0xF8). **The width comes from the named instance's graph. An instance id the client doesn't know
dereferences a null graph and crashes the client** [G]. A replicated predecessor places the member directly
above it in the stack registry (StatescriptRuntime.md §5.10.3).

### 4.4 Checked against the live client
- The generator replays **every active state of the four live `ss_serialize_local` snapshots** (Genji,
  `systems/statescript/fixtures/snap_genji_{fire2,dash,deflect}.json`, `hero_serialize_local.json`; 328 states of
  22 types: Stack, Ability, Anim, BooleanSwitch, SubScript, TrackMovement, CameraTransform, BlinkEffect, LedgeClimb,
  UXScreen, …) through the model. **Each consumes exactly its recorded bits** [L + G]. Those snapshots were
  written by the client's own writers, so they check the formats of types whose writer matches their reader (the
  four asymmetric types are listed, not replayed).
- The earlier live-verified formats agree: Stack idle `010`, Ability idle 10 bits and a dash cooldown 52 bits
  (MatchLogic.md §14.7), WeaponVolley 17, ChaseVar 45 and Anim 39 bits (T041 / T051), TrackMovement,
  BooleanSwitch, CameraTransform, TrackTargets idle and Invisible 1 bit, BlinkEffect, LedgeClimb and UXScreen 0 bits.

### 4.5 What this explains, and what it changes
- **Switch**: the client reads the case bits **and one more bit**. The writer `G:f37dc0` writes the same. Our
  runtime (`systems/statescript/src/states.cpp`, Switch `WriteData`) and T044's table write only the case bits,
  so a delta carrying a Switch would shift the client's parse by one bit (§3.4: wedge) [G]. It must be fixed
  before Switches are sent.
- **TrackTargets idle is 1 bit, not 0**, and **Effect (a has-update type) carries 2 bits**. T051 wrote both as 0
  bits for the quick-melee states (TargetEffect / TrackTargets / StateEffect end), which was the third wedge [G, L].
- **The client's own serializer can't be the reference for the asymmetric types** (Effect, UXFade, `3f67b31b`,
  HistoryRewinder). For them the reference is the reader (this table). Stage 4's shadow compare has to use the
  table there, not the client's bits.
- **Remote formats**: a server driving server-owned entities (brain, HUD, doors: MatchLogic.md §9–10) must use the
  remote column. SubScript remote data is empty. The child id is not carried in the remote format, so remote
  children are named by the instance record's parent/slot (§2.3).

## 5. The prediction model  (stage 3)

Only the entity the client controls predicts. Its sync manager's step (`G:ce9470`, called by
RunCommandFrames before its passes) runs when `G:cf3700` (locally controlled) holds. Every other entity only
applies packets (`G:cec290`) [G].

### 5.1 One command frame of the local entity [G]
```
RunCommandFrames(cf): sm.cf = cf, sm.time = cf*16, window end = cf*16+15
  G:ce9470 (sm vfunc +0x40):
    so+0xB0 = sm.time
    apply pending packets            G:cec290 (§3): rollback / apply / roll forth happen here
    prediction timeout               G:cea070 (§5.5)
    open a frame record              G:cebe60 → so+0xA8
    record the input if it changed   G:ceace0: {ms, CF, previous buttons, new buttons} → so+0xC8 list
    emit button edges                G:1051e40 → LogicalButton notifications at cf*16 (StatescriptRuntime §5.10.2)
  pass loop: NextTime walks from sm.time to the window end, running notifications, states, links
             (after a roll forth it starts at the rewound time, so this loop is the re-simulation, §5.4)
  G:ce95b0 (sm vfunc +0x50): end a roll forth (hooks +0x18), commit the frame record (G:ce9fc0)
```

### 5.2 What is recorded (the undo list) [G]
A frame record (pool `DAT_7ff65188e608`) holds 16-byte entries (`rec+8`, count `+0x10`) plus side tables for
variables. On commit it gets the time (`+0x90`), the CF (`+0x98`) and the input state (`+0x9C`). Records form a list
(head `so+0x90` = oldest, tail `so+0x98`, count `so+0xA0`). An entry is recorded only when a record is open
(`so+0xA8`), `so+0xC0` < 1, and the instance is **mirrored** (flag 0x8, i.e. created by the server). A new time
(`sm.time`) commits the open record and opens another (`G:cebdc0`), so **there is one record per distinct pass
time**, not strictly per CF.

| kind (`+0xE`) | recorded by | content | undone by |
|---|---|---|---|
| 0 | sm vfunc +0x120 state begin (`+0`=0), +0x128 re-enter (`+0`=1), +0x158 action executed | instance id, node index | `G:ceb9c0` (end the state / the action's rollback) |
| 1 | +0x138 state end (`G:ce97c0`) | instance, state index, elapsed ms, list-membership flags | `G:f2a210`: re-activate with the old begin time |
| 2, 3 | variable about to change (`G:ce98e0`, from `G:cf4f20`) | the old value and writer (dictionary: all entries) | `G:cebaf0`: a forced write of the old value |
| 4 | instance flag 0x40 set outside apply (`G:d53fb0`) | instance | clear the instance flags `&~0x444` |
| 5 | `G:ce9af0` | instance + an entity-keyed side entry (`rec+0x50`) | `G:cebca0` [? not read] |

Variables whose id is in the sync manager's list at `sm+0x4D8` are not recorded unless a state owns them [G; what
fills that list was not found (?)]. Mirrored-data objects (`so+0x100`, the STU mirror types of §4) roll back
through their own hooks: +0 begin rollback, +8 end, +0x10 begin roll forth, +0x18 end [G].

**Nothing is recorded while a packet is applied.** The previous CF's record was committed at the end of that CF,
and the new one is opened only after `G:cec290` returns. During the roll forth (the pass loop after it) records
*are* written again: those are the new predictions (Statescript.md §8.1) [G].

### 5.3 Rollback (`G:ceaf90`, local packet with the rollback bit, first sub-frame) [G]
`so+0x792` = 1; mirrored-data "begin rollback". Then **every record, newest to oldest**: the clock is set to the
record's CF/time and the input state to the record's, and the entries are undone in reverse. Then the clock goes to
the packet's CF (`so+0x980`) and the input state to the one recorded at that CF. Each mirrored-data object gets
`G:f28e50(obj, (CF+1)·16, serial)` and "end rollback". **Every record is undone, confirmed or not: nothing compares
predictions with the packet** (T051 report §4).

### 5.4 Apply and roll forth [G]
- Inside "begin apply" (sm vfunc +0xF8: every mirrored instance gets flag 0x1000, and a state's activation hook
  receives it as "replicating"), the body is applied at the packet's CF (§3.4). For local + rollback the clock
  ends at CF + 1 [G].
- **Roll forth** (`G:ceb420`): `so+0x793` = 1; the input entries with CF ≤ packet CF are dropped; **every frame
  record is freed** (their latest time → `so+0xB8`, records at or before it are never committed again); the
  held-button set is restored from the last input at or before CF. **Every remaining stored input is re-queued as
  button edges at its original ms time** [G].
- There is no separate re-simulation loop: the same RunCommandFrames' pass loop walks from (CF+1)·16 to the
  current window end and runs the re-queued edges at their times. Only Statescript is re-simulated, not movement [G].
- A local packet **without** the rollback bit, or a remote packet, is applied on top of whatever was predicted,
  with no undo and no replay [G].

### 5.5 The prediction timeout (`G:cea070`, every CF of the local entity) [G]
`lim` = the client's current CF − **40** (0x28). If the oldest record's CF ≤ lim: begin apply, **roll back
everything** to the last server state (§5.3), drop the inputs with CF ≤ lim, free the records with CF ≤ lim, end
apply, then roll forth the inputs newer than max(last server CF, lim). Unlike a rollback packet, **the timeout
leaves the clock at the current CF**: the state is the last server state, with no re-simulation from its CF;
events that were pending in it fire at the next pass, and the replayed inputs run at their (past) times at the
next pass [G: `G:cea070` restores `so+0xC4` / the sm clock to the saved current CF before `G:ceb420`]. So a client
with no confirmations returns to exactly the last server state (the lock-in full update) once it is idle. **An unconfirmed prediction is undone once
its record is 40 CF old, and its input is dropped. Newer inputs are replayed.** A server that sends a local
rollback packet for a CF at or after a prediction before that frees every record (§5.4), so the timeout never fires.

### 5.6 The binary vs the GDC model (Statescript.md §6–8)

| GDC | binary |
|---|---|
| client ACKs each packet | no Statescript ack: transport packet acks only (§1) |
| redundant / out-of-order packets are ignored | end ≤ last applied: dropped; a union replaces the queued packets it covers (§3.1); a gap waits with no timeout (§3.2) |
| local packet → rollback all → replicate → roll forth | the same, but only when the packet's **rollback bit** is set; without it a local packet is applied on top (§5.4) |
| predictions stored as an undo list | yes: per-pass-time records of state begin/re-enter/end, actions, var old values, instance flags (§5.2) |
| "once it knows confirmation won't arrive" | a fixed 40-CF timeout (§5.5) |
| one delta per changed CF | a local packet may carry several sub-frames (each with a CF offset), applied in one pass, with active states' times shifted by the gap (`G:cf1d50`) (§2.2, §3.3) |
| actions: executed-action indices in the delta | a separate actions section; timed blocks are skipped when their CF ≤ last applied (§2.9) |
| replication doesn't follow links | states are activated through the normal begin path with a "replicating" flag. T044 saw the client run actions under a mirrored state's plug itself (Genji `$1f` := 0) [L] |
| deltas kept until every client acked their CF; packet = union of deltas | server side, not in the client. The client only requires start ≤ last applied end + 1 < end (§3.2) |

## 6. What a server must send  [G, from §2–5]

### 6.1 The controlled entity (local format)
- **Every tick**, after simulating the client's inputs up to its newest input frame F: one stream-1 packet,
  header `[start, end = F]` with start ≤ (the last end the client is known to have applied) + 1, payload
  `hdr = 0 1 1`, `count(F)` (one sub-frame), the state at the **end of F**. The client undoes its predictions,
  applies it at F, and replays its inputs after F. Correct predictions are simply made again (§5.3–5.4).
  - "Known applied" can only come from transport acks (§1). Start may be lower (overlap is harmless: values
    are absolute and timed actions are deduplicated by CF), but never higher.
  - It must arrive **within 40 client CFs** of each prediction, or the client undoes it (§5.5).
- **Loss**: resend the union from the first unacknowledged start. The client drops a covered lone packet and
  waits on a gap forever (§3.1–3.2).
- **The first packet** of an entity is a full update (start 0): it frees the predictions, **clears the owner bag**,
  sets everything, and **stops every instance it doesn't list** (§2.3–2.4).
- **Instances**: a new one needs a `0 1 record`. Its graph must be loadable by the client (the packet waits
  until it is loaded). A destroyed one is `1` (§2.3).
- **Events**: naming an instance in the events tail **replaces** its pending Timer / Lifetime-finish events, so
  send its complete pending list or leave it out (§2.8). The time base is the sub-frame CF × 16.
- **Has-update states** (ScaleTime, Effect, WeaponVolley): send `1 a data 0` when they begin, end (a = 0: the end
  carries data) or their data changes. `0` = no update (§2.6).
- **Time-relative state data** (Ability start, WeaponVolley / Extender begin, ChaseVar last tick) is relative to the
  sm clock *at apply*. For a local rollback packet that is the packet's CF (§3.4). For every other packet it is the
  client's current CF at apply time, not the packet's [G]. A server that encodes remote times relative to its own
  P is off by the client's lead [?: visible as a few-frame offset].
- **Everything must be bit-exact.** Nothing detects a misparse: the client reads on, and the packet either stays
  queued forever (wedge) or crashes the client (§3.4). Send only states whose data format (§4) the encoder writes
  exactly. Never send a form-0 state reference on an owner var, an unknown predecessor instance, or a BYTE / STRING
  expecting it to arrive as such.

### 6.2 Other entities (remote format)
- Packets with `local` = 0 carry no CF of their own (the client uses the message's P) and no events, and are applied
  at once with no prediction. State lists run over `m_remoteSyncNodes` (full: the eligible states + an included
  bit; delta: a set over all entries). Data uses the remote column of §4. Remote clients execute only SYNC_ALL
  actions (§2.9).
- A remote full update also clears the owner bag and stops unlisted instances, including client-started ones
  (§2.3 [G, untested]).

### 6.3 The live symptoms, explained

| symptom | explanation |
|---|---|
| T016: Anim states begin/abort every ~5 CF under `server_echo` [L] | the echo sent local packets **without** the rollback bit, a step behind. They are applied on top of the predictions with no undo or replay (§5.4), so each one ended a state the client had just predicted, and the client predicted it again |
| T041: a press is replayed, then undone 46–78 CF later [L] | nothing confirmed. The timeout (§5.5) fires when the oldest record is 40 CF old: it rolls everything back, drops inputs ≤ lim and replays the newer ones (the "replay"); the press's own input is dropped by a later timeout (the "undo"). The extra 6–38 CF are the time until an older record triggers it, plus records being per pass time |
| T051 first wedge: WeaponVolley/ChaseVar/Anim data ~25 bits too long [L] | a data length error shifts the delta. The client reads a wrong `more` bit, parses garbage as the next sub-frame, its instance list fails, and the packet stays queued forever; `+0x984` never moves, and every later delta waits behind it (§3.4) |
| T051 second wedge: a StateEffect end [L] | Effect is a has-update type with a 2-bit serial (§4.2); the encoder wrote 0 bits → shift → wedge |
| T051 third wedge: quick melee (TargetEffect / TrackTargets) [L] | TrackTargets data is at least 1 bit (§4.2); written as 0 → shift → wedge |
| T051 crash: access violation reading 0x18 in `G:cf1520` (state reference) [L] | a shifted bit read as a var's state reference with form 0 on an owner var dereferences the null instance at +0x18 (§2.7) |
| T051: invisible shurikens (volley re-entered) [L, H] | the end of a has-update state was not sent (`1 0 data 0`), so the server copy stayed active. Every rollback restored it, and the next press re-entered it instead of beginning it (§5.3, §6.1) |
| T051: cooldown radial / per-shot ammo restarted while data was re-sent every tick [H] | every local rollback packet re-applies the data through the reader, which fires the state's change callbacks whenever a value differs from the predicted one (§4, `G:10671b0`/`G:1058570`). A time field that isn't bit-identical to the client's prediction restarts what it animates [? mechanism from the readers; which field differed was not measured] |
| T051: dash cooldown never ended [L] | runtime bug (the adopted cooldown var), not a protocol issue (StatescriptRuntime §5.10.3) |
| T051: movement owner vars (`$1a1`, `$221`, `$238`, `$656`) reset by every rollback [L] | owner vars written by client movement code are part of the server state a rollback packet applies. Unless the server sends the same values, each packet overwrites the client's. They may or may not be recorded as predictions (the `sm+0x4D8` list, §5.2 [?]) |

## 7. Verification: the shadow compare  (stage 4)

- **`instance --shadow`** (server/cmd/instance): the Statescript sim runs as with `--sim`, and each tick's local
  delta is encoded (`GET /simdeltas`) but **never sent**. So the client predicts on its own and undoes each
  prediction 40 CF later (§5.5); a delta it would misread can't wedge it. The host keeps a snapshot of the hero's
  state for each of its last 1024 simulated frames: `GET :3729/shadow?body=&cf=` (instances, vars, active states
  with local-format data, pending events). `POST /shadow/reset?body=` re-seeds the hero from its start-up at the
  current frame: the state an idle client returns to after its timeouts (§5.5).
- **`server/cmd/shadowcmp`**: for each action (default fire, reload, ability_1, ability_2, jump) it waits until the
  client is idle again, resets the server, compares once as a baseline, taps the button with Prometheus
  `logical_button` (the real input path, so the server gets the same input), captures the client with
  `ss_serialize_local` repeatedly for 35 CF, and compares every capture with the server's snapshot of the same CF:
  - instances present on both sides;
  - var values (floats to the 4 decimals the client prints);
  - activity of every synced state (`m_states` minus client- and server-only);
  - state-data bits, exact, except for the types whose client writer doesn't match the reader (§4.1), which are
    compared by activity only.
  Output: a per-type table (activity / data checks and mismatches) and the mismatch list; `-out` writes JSON.
- Build: `go build -o bin/shadowcmp ./cmd/shadowcmp`. Run: `make run INSTANCE_ARGS=--shadow LOGIC_ARGS=--game-server=instance`,
  the client in a match with a hero locked in, then `bin/shadowcmp` (from `server/`).
### 7.1 Results  (2026-09-27, Anubis, Genji, the unmodified client, `instance --shadow`)  [L]
Runs: fire, reload, Shift, E, jump, 3 repeats each (the user moved Genji away from walls for the jump), then Shift ×3
and fire after the fixes below. 49 + 13 compared frames. **A client prediction timeout (§5.5) fires every ~40 CF even
at idle** (something is always being predicted), so only the captures before the next timeout after the press are
comparable. The tool detects the timeout from the client's oldest prediction record (§5.2) and drops later captures.

| type | data checks (match / all) | activity | note |
|---|---|---|---|
| Ability | 288 / 288 | 294 / 294 | |
| SubScript | 637 / 637 | 686 / 686 | |
| WeaponVolley | 6 / 6 | 98 / 98 | |
| ChaseVar | 9 / 9 | 523 / 539 | activity: movement (below) |
| Stack | 404 / 406 | 3134 / 3136 | Shift: one Stack placed with a predecessor on the server, none on the client |
| Switch, Effect, SprayEffect, 3f67b31b | – | all match | never active with data in these runs |
| TrackTargets | 0 / 12 → **12 / 12** | 441 / 441 | the runtime wrote 0 bits; fixed (the server tracks no targets: `0`) |
| Anim | 0 / 30 raw | 1176 / 1176 | 27 differ only by the capture offset (below); 3 by a real 3 ms (state 54, below) |
| BooleanSwitch | 1238 / 1524 | 2977 / 2989 | the 8 idle switches that depend on the movement flags (baseline) |
| TrackMovement | 33 / 49 | 98 / 98 | movement (the server has none) |
| all no-data types (UXScreen, LogicalButton, Wait, CosmeticEntity, …) | match | match except movement / E presentation | |

Findings:
- **Reload: 0 differences. Fire: 0 except Anim timing.** The encoder's state data agrees bit for bit with the
  client for every type T041/T051 send (Ability, Stack, WeaponVolley, ChaseVar, SubScript, BooleanSwitch values
  aside).
- ~~Capture artifact~~ **Correction (T053)**: Anim's time field was 15 ms larger on the server because the *server*
  wrote it with the runtime's clock at the end of the step window (f·16 + 15 after `Run(f, 1)` [L]); the client
  writes and reads at CF·16 (§3.4). The instance server now writes state data at CF·16, and the fields match [L].
- **Runtime timing difference**: Genji weapon Anim state 54 (after the volley) begins 18 ms (about 1 CF) earlier on the
  server [L]. Not investigated.
- **Baseline differences (every test, before any press)**: owner vars `$1a1`, `$221`, `$656` (client movement code),
  `$06c4`/`$06c9` (client FLT 200 vs server INT 0; client code, not a graph), and `$195a..c` (the Ability HUD vars:
  the runtime writes 1 when not cooling, the client has 0). Eight BooleanSwitches of `0x157` / `0x1c7` / `0x304` /
  `0x5ad` depend on them.
- **Movement-driven** (jump, Shift): TrackMovement data, the dash's LogicalButton / BooleanSwitch branch on instance 4,
  and DataFlowMapping / ChaseVar on instance 10 begin on the client but not on the server. They need the client's
  movement owner vars (§6.3) or a movement model.
- **E**: one CosmeticEntity (presentation) the server doesn't begin.
- Fixed from the compare, per §4: Switch (the default-taken bit) and TrackTargets (`0`) writers in `systems/statescript`.

