# The instance server (`server/cmd/instance`, `server/inst/`)

The game instance server that is to serve real matches (Phase A of [systems/README.md](../systems/README.md)
"Phases"). It replaces the R&D game server (`server/cmd/gameserver`, `server/internal/instance`) as the server the
client plays on; the R&D server stays as the reverse-engineering harness. It is built from what the R&D server
found (moved behind clean interfaces, byte-for-byte the same wire output) plus a 62.5 Hz tick with clock steering.

Status tags as in [README.md](README.md): [L] measured live, [H] confirmed by the user in game, [G] read in
Ghidra, [?] inferred. First written for T048 (2026-09-26).

---

## 1. Running it

```
cd server
make libs                 # the C++ libraries the cgo wrappers link (systems/ecs; cmake + ninja, incremental)
make run                  # login, lobby gateway, lobbylogic, R&D gameserver, instance (the last three hot-reload)
```

- `instance` reads the Statescript graphs from the 0.8 install's CASC data (`--content`, default
  `../game/owbetaclient`; opening it takes ~0.7 s). The server **simulates** hero Statescript by default (§9, §10); `--no-sim` (deprecated, to be removed) sends the
  start-up only, `--shadow` simulates without sending (the shadow compare, StatescriptSync.md §7), `--evidence FILE`
  freezes the states a shadow compare found mismatches for (§10.4).
- `instance` listens on UDP `:26600` (clients), control API `127.0.0.1:3729`, and dials lobbylogic's instance
  port `127.0.0.1:3727` like the R&D server (which uses UDP `:26500`, control `:3728`). Flags / `INSTANCE_*`
  environment: `--addr`, `--advertise` (the address put in 20600), `--logic-addr`, `--control-addr`, `--debug`
  (every JAM message), `--batch` (§4.4), `--lead-target` / `--lead-band` (§5). `make run` passes `INSTANCE_ARGS`.
- **Which server gets the matches**: lobbylogic keeps every linked game server by the name in its hello
  (`instance`, the default; `gameserver` = R&D) and sends new matches to the active one: `LOGIC_ARGS=--game-server=gameserver`,
  or at runtime the lobby chat command `!gameserver [name]` (also
  `curl -X POST --data 'command("gameserver instance")' http://127.0.0.1:3726/eval`), which lists the servers when
  given no name. Releases (Leave Game) go to the server holding the assignment. Then `!instance [map]` (or
  `live.py instance`, which waits on the R&D control port) sends the client in.
- **Process groups** (T058): `make lobby` (login + gateway + lobbylogic) holds the client's connection and stays up;
  `make games` (`GAMES`, default `instance gameserver`) runs the game servers and can be stopped, started and
  rebuilt on its own; `make run` runs both (`make -j2 lobby games`). Start order doesn't matter (the links retry).
  Stopping the games greys out the title screen's match buttons (LobbyServer.md "Game server availability").
- Hot reload: `scripts/watch.sh instance` (part of `make games`; alone: `make instance-watch`) rebuilds and restarts
  on `.go` changes in its packages (including the local cgo wrapper modules under `../systems`), on changes to the
  C++ archives it links (`../systems/*/build/*.a`: run `make libs` or `cmake --build`, the watcher relinks), and on
  changes to its args file **`server/instance.args`** (extra flags, read on every start; `#` comments, shell
  quoting; applied after `INSTANCE_ARGS`). If the binary exits on its own the watcher restarts it (backing off to
  30 s while it keeps dying). A restart drops the match: the client's next packet hits a connection the new
  process never saw (no SYN), so it gets a FIN and returns to the lobby; lobbylogic has replayed the assignment in
  the meantime. The new process links ~1 s after the old one stops, inside lobbylogic's 2 s offline debounce, so
  the title screen doesn't flicker [L, 2026-09-27].
- Tests: `go vet ./...` and `go test ./...` in `server/` need the libraries built (`make vet`, `make test` build
  them first). `inst/server`'s end-to-end test runs the whole server against a fake lobbylogic and a fake client.

## 2. Packages

One concept each; the arrows are imports.

| package | what | tested by |
|---|---|---|
| `inst/bits` | the client's bit stream (LSB first, byte fields aligned): Writer / Reader | round trips |
| `inst/jam` (T049) | JAM messages: catalogue-generated codecs, the instance stream's framing (stream 0), channels | T049 |
| `inst/wire` | a data payload's layout both ways: server (frame P, JAM, input ack + clock flags, entity section), client (JAM, input history) | captured client packets, the R&D server's bytes |
| `inst/link` | UDP transport: packet header + HMAC-MD5, handshake (SYN, SYN\|ACK, ACK, FIN), ack windows, loss detection, keepalive, timeouts; `Channel` = reliable JAM (numbering, resend on loss, in-order receive) | handshake / data / FIN over a real socket, loss, resend, ordering |
| `inst/clock` | the 16 ms `Ticker` (drift-free, bounded catch-up) and per-client clock `Steer`ing (§5) | the policy table, the steering sequence, tick timing |
| `inst/world` | a match's world: the map's networked placements with their teams, players' controllers and bodies, start spawns; entities live in a server-side entity admin of `systems/ecs` (owecs) | every map of the catalogue builds; IDs, removal, spawns |
| `inst/replication` | entity creates / updates / destroys, mirrored component data, movement snapshots, Statescript sync packets (full update, local rollback delta `delta.go`); `Replicator` = one connection's state (what the client has, queue, in-flight, resend of lost entries); `serializer.go` = the boundary to the C++ serializers (§4.3) | same bytes as the R&D encoders for every entity kind and every hero's start-up; packing |
| `inst/sim` | the Statescript host (§9): `Runtime` runs each hero body's instances in `systems/statescript` (owstatescript) in lockstep with the client and produces its local deltas; the correction policy from the runtime's capabilities (§10.3); `Static` (`--no-sim`) only hands out the start-up | Genji seed, input frames, delta chain (needs the 0.8 install) |
| `inst/match` | the lifecycle (§3), players, per-tick packet assembly | a whole lifecycle with a fake link |
| `inst/lobbylink` | the link to lobbylogic (`internal/gamelink.Client`): hello (name `instance`, ping interval, modes, maps), assign, release, heartbeat, reconnect | a fake lobbylogic |
| `inst/server` | the game loop around all of it (§6) | end-to-end |
| `inst/control` | the HTTP control API (§7) | – |
| `cmd/instance` | configuration, wiring, signals | – |

Reused from the R&D side unchanged: `internal/gamelink` (the lobbylogic link protocol), `internal/maps` (the map
catalogue generated from content), `internal/heroes` (the hero start-ups generated from content, T040/T044),
`internal/logging`.

## 3. A match's lifecycle

| step | trigger | the server |
|---|---|---|
| assign | lobbylogic's `assign` (the player pressed Start / `!instance`) | creates the match for the map if it has none, with its **world**: every networked placement of the map + the spawn points' teams (built once per match, T047); the link learns the connection's key |
| connect | the client's SYN, then ACK or data | SYN\|ACK; the player's replication, JAM channel and clock steering start over; the first packet carries the **resync** flag |
| tick | every 16 ms | one data packet per connected player: P, JAM, input ack, clock flags, the next replication entries |
| join | **21602** (map loaded) | the player's controller (definition 0x0E, flags 0x38) + **20301** naming it the local entity, then the world: 22 from-map creates + 18 team updates on Anubis, and the other players' bodies |
| lock-in | **21603** {hero, skin} | the body (hero gameplay entity at the next start spawn point, movement snapshot, health, team) to every joined player; to the owner the controller update (0x20 link, 0x10 team, 0x3F PlayerInfo hero: closes hero select, MatchLogic.md §15.3) and the hero's Statescript start-up (local full update from `sim.Host`, MatchLogic.md §14); a hero change destroys the old body first |
| ping | **21608** | 20304 with the client's times, our receive time and our send time (stamped when the packet is encoded) |
| leave | lobbylogic's `release` (Leave Game), the client's FIN, or 10 s of silence | FIN; the player's entities are destroyed for the others; the match ends with its last player |

Entity IDs: the map's placements get 1..N (0x40000001…), then controllers and bodies in order; IDs are never reused
within a match (a new body never takes the ID of one the client is still destroying). The entity section encodes
IDs up to 8 bits in 10 bits, up to 16 in 19, else 35 (`FUN_7ff650ee4ff0`) [G].

## 4. Replication

### 4.1 Per connection
A `Replicator` holds what the client has (created and not destroyed), the queue of entries to send and the entries
in flight per packet sequence number. The link reports which of our packets the client acknowledged (its ack +
64-bit mask in every packet header) and which count as lost (not covered and ≥ 3 behind the newest ack); entries of
lost packets go back to the front of the queue. [?] Resending an update after a newer one for the same component
would roll the client back; the skeleton accepts that (no loss on the links tested).

### 4.2 Reliable JAM
`link.Channel`: reliable messages are numbered 0, 1, 2… per direction; a lost packet's reliable messages are sent
again with their numbers. Received reliable messages are delivered in order, duplicates dropped, messages after a
gap held back (up to 64, then the gap is skipped and counted).

### 4.3 The serializer boundary (with T049)
Payloads in the client's memory layout (component mirror data, Statescript sync packets, movement snapshots) are
produced only through `replication.Serializers` (`MirrorSerializer`, `ScriptSerializer`, `SnapshotSerializer`).
Today `Stopgap` implements them with the R&D server's hand-written encoders; the C++ libraries (STU reflection,
the Statescript runtime) replace them behind their C APIs. Component values and snapshots are written in place
(stream 4 aligns bytes relative to the packet), so a C++ implementation takes the writer's bit phase and returns
bits to append. See [JAM.md](JAM.md) §12.4.

### 4.4 Packing
The client goes on reading after a create's blob, so a create must end its packet (with 8 creates per packet it
asserted, MatchLogic.md §15). The default sends one entry per packet, like the R&D server: Anubis' world takes 41
ticks (0.66 s) after 21602. `--batch` lets updates, destroys and Statescript entries share a packet with at most one
create at the end [?: not yet run against the client].

## 5. The clock: tick and steering

The server's frame **P** advances by one per tick and every packet carries it, so the client sees no gaps (its
safety buffer only grows when P jumps by more than 4, CommandFrames.md §4). Each match counts its own frames.

The **lead** the server steers is what it can measure: the client's newest input frame minus the server's frame
when that input arrived. Its input has to arrive before the server simulates the frame, with a margin; the
client's own safety buffer is 52–104 ms (clock+0x88), so the default **target is 4 frames** (64 ms), dead band ±1.
`clock.Steer` takes the minimum over the last 16 input packets (the earliest input must be in time too) and sends:

| lead − target | flags |
|---|---|
| within ±1 | 0 |
| +2 … +8 | 0x1 slow (−200 µs per client render frame) |
| beyond +8 | 0x9 slow, strong (−1000 µs) |
| −2 … −8 | 0x2 fast |
| beyond −8 | 0xA fast, strong |
| beyond ±60 | 0x4 resync (at most once per second; strong steering meanwhile) |

Every (re)connect sends one resync in the first packet: a client that comes back to a restarted server may be far
ahead of its frames. The client keeps the last flags it received, so they go in every packet.

Measured: §8.

## 6. The game loop

One goroutine owns all match state (matches, worlds, players, replicators, JAM channels, steering), so none of it
needs locks. It runs on `clock.Ticker`: every 16 ms it first runs the events that arrived since the last tick, in
order (the UDP link's connected / data / closed, lobbylogic's assign / release, control API calls), then ticks every
match (frame + 1, the world's entity admin, the simulation) and sends each connected player its packet. Client
input is therefore handled at tick granularity, like a real server; the ping's receive time is stamped when the
datagram arrives. The link (its read goroutine and a housekeeping ticker for keepalives and timeouts) and the
lobbylogic link only post events. If the loop falls more than 4 ticks behind (e.g. the process was stopped) it
skips ahead instead of bursting (`/stats` late_ticks).

## 7. Control API (`127.0.0.1:3729`)

| call | what |
|---|---|
| `GET /conns` | players: lifecycle state, controller / body / hero, the clock (`frame` P, `input_frame`, `lead`, `min_lead`, `clock_flags`, `resyncs`, the client's `timing`), packet and replication counters, and the link's view (address, handshake state) |
| `GET /matches` (`/worlds`), `?full=1` | matches: map, frame, players, placements, ECS entity / event counts, `sim` (per hero: frame simulated, delta end CF, frames, deltas, input errors, states whose data the runtime can't encode, unmodelled node/state types); full = every entity |
| `GET /simdeltas?body=` | the last 128 Statescript deltas encoded for a hero body: CF range, bits, hex (LSB first), what changed |
| `POST /simtrace {"body", "on"}`, `GET /simtrace?body=` | switch / drain the runtime trace of a hero body's server-side Statescript (JSON lines, Prometheus `ss_trace`'s vocabulary) |
| `GET /stats` | the loop: ticks, late ticks, queued events; `view`: the live server view's subscribed connections, frames, drops, sizes (DebugDrawing.md §5) |
| `POST /clock {"Target": 12, "Band": 1, …}` | the clock steering policy for every player (`clock.Policy`; zero fields = defaults); answers the policy in use |
| `GET /traffic?n=50` | recent datagrams, both directions (hex) |
| `GET /jamlog?conn=N&ping=0` | JAM messages received, decoded |
| `POST /setup?conn=N` | join now (as on 21602) |
| `POST /lockin {"conn", "hero"}` | lock in a hero (as on 21603) |
| `POST /jam {"conn", "msg", "payload", "unreliable"}` | queue a raw JAM message |
| `POST /spawn {"conn", "id", "def", "pos", "rot", "scale", "flags", "map_index", "mover"}` | create 0x40000000\|id on that client only |
| `POST /filter {"conn", "id", "bits"}` | a component 0x10 update |
| `POST /scripts {"conn", "entity", "graphs", "active"}` | a remote Statescript full update (layouts from Prometheus `graph_sync_info`) |

Numbers may be JSON strings in hex (`"hero": "0x02E0000000000029"`).

## 8. Live results  (T048, 2026-09-26, localhost, the unmodified client)

Run beside the normal group (`bin/instance` + lobbylogic `!gameserver instance`), then switched back.

| test | result |
|---|---|
| Anubis: connect → 21602 join → Genji lock-in | handshake, controller + 20301, 22 placements + 18 team updates (41 ticks); body, controller update, start-up: the client created the hero's **22 Statescript instances** [L]. **No black screen, HUD, ability icons, weapon, abilities work** [H]; rollback on abilities as on the R&D server (no confirmation yet, T041) |
| clock | 63 packets/s, consecutive P, 0 late ticks; **lead 3–4 frames** steady without corrections (R&D server the same hour: 47–50); client timing report 100–116 (R&D: 617–633); a client hitch (lead −1) corrected by `0x2` in ~5 s; deliberate target 12 → 4: ~0.8 frames/s with the weak flags [L] (CommandFrames.md §6.1) |
| Leave Game | 21801 → release → FIN; client FIN\|ACK 0.44 s later; match ended; the user was back in the lobby at once [L, H] |
| second map: Dorado | 40 placements + 36 team updates; HUD, icons, weapon fine [H]; lead 5–6 (inside the band) |
| hot reload while in a match | restart → lobbylogic replays the assignment → the client's next packet has no SYN in this process → FIN → client FIN\|ACK the same second; the user was kicked cleanly to a working lobby [L, H] |
| tutorial (TUTORIAL button) | same path: 1 placement + its team, Genji at the tutorial's spawn point (team 0); loading screen gone, hero select shown [L, H] (the first run of the tutorial through the 21602 join, open since T047) |

The ability icons also show on the R&D server now [H]: they come from T047's PlayerInfo hero on lock-in, not from
anything specific to this server (MatchLogic.md §14.7 update).

## 9. Statescript on the server (`inst/sim`, T051)

The client **predicts** its own hero's Statescript: it runs the graphs on its button input at its own command
frame (3–6 frames ahead of the server's P) and records what each frame changed. The server runs the **same
instances** on the **same input** at the **same frames** and sends what they came to; the client rolls back to
that and replays its newer input. When the two agree nothing is visible; when they don't, the server wins.

### 9.1 Pieces
- **`systems/statescript/go`** (module `owstatescript`, package `statescript`): cgo over
  `systems/statescript/capi/statescript.h`: runtime (graphs, graph closure `GraphRefs`, layouts
  `GraphSyncSlots` / `GraphStates` / `GraphIndexBits`), sync managers (`ImportInstance`, `ImportState`,
  `SetVar`, `Input`, `Run`, `Instances` / `InstanceInfo` / `Vars` / `ActiveStates` / `StateData` / `Events`,
  trace, unmodelled report). The C API's "server hosting" section is the contract between the library and this server.
- **`sim.Engine`** (one per process): the runtime with every graph loaded so far, read from CASC by GUID
  (`owcontent`), each with the graphs it references; the local-format layout per graph.
- **`sim.Runtime`** (one per match, `sim.Host`): a server-role sync manager per hero body.
- **`replication.LocalDelta`** (`delta.go`): the local delta encoder over two `ScriptState` snapshots.

### 9.2 One hero's life
1. **Lock-in** (`match.lockIn` → `Host.SpawnHero(body, def, cf)`): `heroes.Start(def)` as before (the client's
   start-up); the host **imports** it into a new sync manager as the client does on receiving it: instances
   with their ids and SubScript parent/slot (`ImportInstance`: no schema, no entries fired), then owner vars
   and instance vars (`SetVar`), then the start states with their state data (`ImportState`). `cf` = the
   client's newest input frame; the lock-in full update now **ends at that CF** (it used to end at the packet's
   P), so the first delta continues from it. Not importable yet: the weapon list `$1d` (a dictionary; the C API
   sets primitives only).
2. **Input** (`match.Data` → `Host.Input`): the entries of the client's input history (`wire.InputEntry`: button
   mask, action code) for frames the hero hasn't simulated.
3. **Tick** (`Host.Tick`, every 16 ms): per hero, every frame whose input has arrived: `Input(F, buttons, action)`
   (the runtime turns edges into LogicalButton notifications for the instances the client sends them to),
   then `Run(F, 1)`. A frame missing from the history repeats the previous input; a client more than 64
   frames ahead is caught up 64 frames per tick (`skipped`).
4. **Delta** (`match.queueDelta`): when a hero has simulated frames no delta covers and none is queued, one
   replication entry is queued whose packet is **encoded when the packet is built** (so it covers every frame
   simulated until then): `LocalDelta(sentCF, frame, sent, now)`, then `sent = now`. One delta in the queue at
   a time; a resend repeats its bytes (the client drops a packet whose end CF it has applied). In practice
   one delta per tick, each covering the frames that arrived since the last.
5. **Leave / hero change**: `RemoveHero`.

### 9.3 The local delta and how the client takes it
Format (`delta.go`, from Prometheus `ss_serialize_local` + Ghidra): header bit 0, local 1, **rollback 1**,
`count(CF)`; the instance list (known / new with its record / destroyed [?]); changed owner vars; per instance
a changed set over every `m_syncVars` slot + values, the other changed vars by id, a changed set over every
`m_states` entry + per changed state [has-update bit] active bit, data, one dropped bit; then every instance's
pending Timer / Lifetime-finish events (time base CF × 16 ms). State data comes from the runtime
(`ss_sm_state_data`); while a type's data is not implemented the snapshot keeps the start-up's bits for start
states, and other states are counted as `unencodable` (reported, not sent as changed).

The client's side (all [G]):
- It applies pending packets at the start of its next Statescript step (`FUN_7ff650cec290`): a packet is applied
  when `start <= last applied end + 1` and `end > last applied`; a gap waits (no timeout) for a packet that
  fills it; the client's own CF is not compared. A failed apply sets sync-object `+0x960` = 0 and **every
  later packet is dropped**.
- **Rollback bit set**: it undoes every recorded prediction, sets its clock to the packet's CF, applies the
  server state, drops its stored input up to that CF and replays the newer input as LogicalButton events at
  their original times; the following passes of the same step re-simulate up to the present. Nothing compares
  predictions with the packet: a correct prediction is simply made again.
- **Without it** (what `server_echo` sent, with CF 0 = the client's own) the packet is applied on top of the
  predictions, and the client's **prediction timeout** (`FUN_7ff650cea070`, every CF) rolls everything back
  once its oldest prediction record is **40 CF** old. That is the "undone 46 CF after the press" of T041.
- So a hero's predictions stay only if a rollback packet for a CF at or after them arrives within 40 client
  frames; the host sends one every tick.

### 9.4 Status
Unit-tested: the wrapper (door graph, import, layouts, trace), the encoder (bit-exact expectations for
unchanged / changed / new / destroyed), the host (Genji: 22 instances and 108 start states imported, input
frames, the delta chain, catch-up). What reacts to input depends on the node types the runtime implements
(T041).

Live (2026-09-27, Anubis, Genji body `0x40000018` locked in via `/lockin`, the unmodified client) [L]:
- Seeding: 22 instances, 108 start states imported; the client's active states equal the start-up's (108/108).
- **The client applies every delta**: sync object (component 0x23 + 0x5C0) `+0x960` = 1 (not broken),
  `+0x980` (server CF) = `+0x984` (last applied) = our delta's CF, 1–2 CF behind the client's CF
  (component `+0x384`); 0–1 packets pending, ≤ 1 prediction record. ~1 delta per tick (239 deltas over 257
  frames).
- A primary-fire tap (`logical_button tap fire 100 ms`): the LogicalButton notifications arrive (each twice:
  prediction and replay after a rollback) but **no state begins** and ammo `$35` stays 24: the server's state
  has no press (`ss_sm_input` not implemented yet), and its rollback deltas are authoritative. Same tap with
  `--no-sim`: weapon instance 9 begins states 4 (WeaponVolley), 5 (ChaseVar), 11 (Anim), `$35` 24 → 23, and
  the client drops it again ~38 CF later. So the delta path works end to end; confirming presses waits for
  the runtime's input handling and the fire-path node types + their state data (T041).
- Later the same day, with T041's fire path (`ss_sm_input`, fire nodes, state data, StackPriority records from
  CASC) [L]: the **server fires exactly like the client** (WeaponVolley begins at the press CF, finishes
  +13, Ability 59/60 abort). But the client stopped applying at the delta for press+7…+9: it read that delta's
  "more sub-frames" bit as 1 because the runtime's delta-form **state data for WeaponVolley / ChaseVar / Anim
  is ~25 bits shorter than the client's** (42 / 70 / 39 bits vs 17 / 45 / 10–23). After a misparsed delta
  the client waits behind it forever, and its prediction stays: Genji kept firing [H]. That was
  T041's Anim time field (`0+8 | 10+16 | 11+32`, written as 4/8/16).
- After that fix [L]: **Genji's primary fire is confirmed.** 10 consecutive `logical_button` taps; ammo 24 → 21 →
  … → 3 → 24 (auto-reload, simulated by the server as well) → 21 → 18; each value stays, nothing is undone at
  +40 CF, the Ability cooldown states come back at +60 as on the client; client sync 1–2 CF behind, 0–1
  pending. One run of 12 got stuck at press+14 and was not reproduced; `GET /simdeltas?body=` keeps the last
  128 deltas (hex + change summary) to diagnose the next one.
- User's look, then fixes (2026-09-27) [L, H]:
  - **Shurikens were invisible**: our deltas never sent the end of a "has update" state (WeaponVolley), so the
    client kept a stale active volley and the next press re-entered it (no projectiles). An end is `1 0 <data> 0`
    (`FUN_7ff650cee430` reads the data active or not); now sent, and primary fire shows shurikens every time [H].
  - **Dash cooldown never ended**: the runtime's Ability read its adopted cooldown var's stored value forever;
    the var now reads through the Ability (remaining cooldown) and is released at 0 (`TestGenjiDashTwice`).
  - **Secondary fire** is still mostly invisible [H], though its projectile entities are created as without the
    sim [L]: follow-up.
  - A delta marks a state changed on begin / re-enter / end (`ss_sm_state_changes`, which also counts an Ability's
    semantic changes: flags, cooldown start / end), or when the data of a state that doesn't move with time changed
    (BooleanSwitch, Stack …). Ability, WeaponVolley, ChaseVar and Anim data move with time and are not re-sent:
    re-applying them every tick restarted the cooldown radial and the volley's per-shot ammo [H].
  - "Has update" types are sent only with a verified data writer (WeaponVolley): a StateEffect end with unverified
    data shifted a delta, and the client waited behind it for good (13,000 pending) [L].
  - Vars go out with their stored value (the client's), never a read through an owner state.
  - Still broken [H]: double jump and wall climb. They depend on owner vars the client's movement code writes
    (`$1a1/$221/$238/$656`), which every rollback resets to the server's values, and on LedgeClimb / TrackMovement,
    which the server doesn't simulate: they need a movement model.
  - Then a third wedge (quick melee: TargetEffect / TrackTargets data we write as 0 bits) and a **client crash**
    (a misread delta reached the client's state-reference reader `0xcf1520`). Since then only states with verified
    data formats are sent, and the sim was **opt-in (`--sim`)** until a verification harness had compared our
    deltas with the client's own serialization (T052's shadow compare). Since T053 it is on by default, gated by
    the runtime's capabilities and the shadow evidence (§10).

## 10. Conformance to the sync spec  (T053)

Every obligation of [StatescriptSync.md](StatescriptSync.md) §6 (and the §2 grammar rules it relies on), where
`server/inst` meets it, and its status. Audited 2026-09-27 against the code as T051/T052 left it ("was"), then closed
by T053. ✓ implemented · ◐ partial (what is missing) · ⊘ out of scope (reason).

### 10.1 Controlled entity (local format, §6.1)

| # | obligation (spec) | was (T051/T052) | now (where) | status |
|---|---|---|---|---|
| L1 | one local packet per tick after simulating to the newest input frame F: `0 1 1`, `count(F)`, one sub-frame, the state at the end of F | as specified | unchanged (`match.queueDelta`, `sim.Runtime.Delta`) | ✓ |
| L2 | start ≤ the last end the client is **known** to have applied + 1, known only from transport acks (§1) | start = the previous *encoded* end | start = the end of the newest **acknowledged** delta (`replication.LocalGhost`; `Entry.Acked/Lost` from `Replicator`) | ✓ |
| L3 | loss: send the **union** from the first unacknowledged start (§3.1–3.2) | the lost delta's old bytes were resent; later deltas waited behind the gap | every delta is a union from the acked base: it names everything that changed in *any* frame since (a var changed and changed back is sent again, a state begun and ended is sent inactive), with the values at the end. A lost delta is never resent; after a loss a union goes out even without new frames. The client drops the lone packets the union covers (§3.1) | ✓ |
| L4 | within 40 client CFs of each prediction (§5.5) | every tick | every tick; a loss costs one tick | ✓ |
| L5 | first packet = full update (start 0) | `HeroStartup` | unchanged: the content-generated start-up (MatchLogic.md §14). Its state data comes from `internal/heroes` (T040/T044), and the sim imports the same bits | ◐ the library can't read dictionary vars back (`$1d`), so the full update isn't encoded from the sim's snapshot yet |
| L6 | new instance `0 1 record`; destroyed `1` (§2.3) | ✓ | ✓. A destroy of an id the client doesn't know is a no-op (`G:cecba0` leaves the entry id 0, `G:ced2f0` skips it) [G, T053], so a union may destroy an instance the client never got | ✓ |
| L7 | a known id's record must match graph, parent **and** slot, or the sub-frame fails (wedge) | an id reused with another graph went out as `0 1 record` (wedge); parent / slot not compared | `LocalGhost.holdBack`: an instance whose record differs from one the client may hold (acked base or any packet since) is destroyed first and created only once no such packet is outstanding; its children wait with it. An instance destroyed and re-created in the union window is **restated**: new record, every var, every state bit | ✓ |
| L8 | events tail replaces an instance's pending Timer / Lifetime list; time base = sub-frame CF × 16 (§2.8) | every instance named with the runtime's queue | unchanged. Encoding checked against `G:cf1620`: kind is a u2 *value* (0 Timer data 1, 1 Timer + `1` (data 0) or `0 small(data)`, 2 Lifetime finish, 3 end), time bit 1 = same, then `0 1` + count later / `0 0` + count earlier [G, T053] | ◐ events of frozen states follow the server (§10.3) |
| L9 | actions section (§2.9): SYNC_ALL actions the server executed, timed per CF | always empty | `writeActions`: the runtime's executed-action log (`ss_sm_actions`), SYNC_ALL only (a remote sync node: Statescript.md §9.1), one timed block per CF after the start; index width = the graph's `m_nodesBitCount` (+0x178, read from the asset; `max(1, bitlen(nodes−1))` on 17 lobby graphs live [L]). Genji's fire / reload / abilities execute no SYNC_ALL action, so the section is empty in practice [L] | ✓ |
| L10 | has-update types: `1 a data 0` on begin / end / data change | WeaponVolley only (Go list) | every has-update type with a runtime data writer (capability) | ✓ |
| L11 | time-relative data at the sm clock at apply = the packet CF × 16 for a local rollback packet (§3.4) | assumed ✓ | **was 15 ms off**: the runtime's clock after `Run(f, 1)` is at the end of the step window (f·16 + 15) [L], and the snapshot wrote data there. Now the snapshot writes state data with the clock at f·16 (`hero.snapshot`). This was also the "+15 ms capture artifact" of StatescriptSync.md §7.1 | ✓ |
| L12 | bit-exact data: send only what the encoder writes exactly | Go lists `verifiedData`, `timeDataTypes`, `heroes.NoStateData` | the runtime's capability per type (`ss_runtime_type_cap`) + shadow evidence (§10.3–10.4); the runtime counts every client-visible data change in `ss_sm_state_changes` (time-only movement excluded), and the server sends a state's data only when that count moves (`DataChanges` = false). Comparing bits re-sent WeaponVolley / ChaseVar / Ability data every tick, which restarts the per-shot ammo and the cooldown radial (first play test [H]) | ✓ |
| L13 | never a form-0 state reference on an owner var | ✓ | ✓ (owner vars only ever use form 1) | ✓ |
| L13b | a var's writer travels as its state reference (§2.7, `G:cf1520`: form, priority bit, state-defined + small(extra)) | never written: every correction left the var with no writer | `ss_sm_var_users` → `userValue`: form 0 for a state of the var's own instance, form 1 (instance + state) otherwise; a writer instance the client won't know is not named. A running WeaponVolley owns `$35` state-defined, so the client reads the per-shot ammo through it (T054, `G:1077e40`); without the writer every correction showed the stored value (the choppy ammo of the play test [H]) | ✓ |
| L14 | never an unknown stack predecessor instance (crash, §4.3) | not checked | `ss_sm_state_data_refs`: a state whose data names an instance the packet doesn't leave the client with (or a server-local id ≤ 0) is held back as last sent (`LocalGhost.holdStates`) | ✓ |
| L15 | BYTE arrives as INT, STRING can't be synced | ✓ | ✓ (BYTE vars go out as INT: the client can't hold a synced BYTE either way) | ✓ |
| L16 | INT64 bits 32–47 can't be sent | ✓ | ✓ (skipped) | ✓ |

### 10.2 Other entities (remote format, §6.2)

| # | obligation | was | now | status |
|---|---|---|---|---|
| R1 | `local` = 0, no CF, no events, applied at once | `/scripts` full update from caller bits | `replication.RemoteFull` / `RemoteDelta` over a `ScriptState` with remote-format data | ✓ encoder |
| R2 | full: eligible remote states + `included` bit; delta: `set` over **all** `m_remoteSyncNodes` | full only | both. Only state entries are ever set in a remote delta: `G:cee430` reads the active bit of any set entry and on `1` dereferences the entry's state object, null for an action (crash) [G, T053] | ✓ encoder |
| R3 | remote column of §4 | local only | `ss_sm_state_data2(local = 0)`; types without a remote writer are frozen for remote packets | ✓ |
| R4 | remote time fields relative to the client's clock at apply | – | the caller takes the remote snapshot with the sm clock at the viewer's newest input frame [?: the client's lead over that estimate is not measured] | ◐ |
| R5 | a remote full update clears the owner bag and stops unlisted instances | – | `RemoteFull` lists every instance of the snapshot | ✓ |
| R6 | remote clients execute only SYNC_ALL actions (remote index, `m_remoteSyncNodesBitCount`) | – | `writeRemoteActions` maps the m_nodes index through `m_remoteSyncNodes` | ✓ encoder |
| R7 | consumers: other players' hero bodies, the gamemode brain, map entities | none | ⊘ not wired: other players' bodies need a second client to verify anything (T029); brain / map entities are not simulated on the server yet (MatchLogic.md §9–10, roadmap). The encoders are unit-tested | ⊘ |

### 10.3 Policy for what the runtime can't produce

A consequence of §5.3–5.5 decides it: **a client prediction the server does not confirm cannot last.** Every rollback
packet undoes *all* recorded predictions (confirmed or not) and re-sets only what it carries; without packets the
40-CF timeout does the same. So "leave a state to the client" inside a corrected entity means: it lives until the next
confirmation (≤ 1 tick) — and a state the client begins **from time** (a timer, a volley's end), not from input, is
worse off: the client applies packets ~3 CF behind its own frame, so while the begin lies after the packet's CF the roll
forth runs it again (animation restart, sound twice), and once the packet passes it the state is gone (the secondary
fire's post-throw Anim `0x304` s55 in the re-test [H]; mechanism from §5.3–5.4, not measured per CF). A state the server can't express exactly has two options: send it exactly, or **freeze** it
at what the client was last sent (`GraphLayout.Frozen`; `LocalGhost.holdStates` keeps it as last sent, so the ghost
records what the client really got). Per state type, from the runtime (`sim.Engine.Corrects`):

| the runtime says (`ss_runtime_type_cap`) | server |
|---|---|
| no state data in the packet's format (`SS_DATA_NONE`; 226 of 259 types) | **activity only**: begin / end sent (a state's lifetime runs through links, which the runtime models for every type) |
| data written from the runtime's state (`SS_DATA_WRITER`) | sent |
| data only echoed from an import (`SS_DATA_ECHO`) | frozen |
| the shadow compare found mismatches for the state (§10.4) | frozen |
| level below `Policy.MinLevel` (0 by default) | frozen |

The server has no per-type list left: adding a node type (or a writer) to the runtime changes what it corrects with no
server change. `GET /matches` shows per hero `Corrected` and `Frozen` ("type (reason)" → count).

### 10.4 The evidence gate (shadow compare)

- `bin/shadowcmp … -evidence FILE [-gate]` records per state (`0x<graph>:<m_states index>`) the comparisons and
  mismatches of a run (baseline and captures), with whether the server corrected it; `-gate` exits 1 if a corrected
  state mismatched. The server's shadow snapshots also carry each state's data written at the capture clock
  (CF·16 + 15, `cap_hex`), so the client-writer artifact of StatescriptSync.md §7.1 no longer counts as a mismatch.
- `instance --evidence FILE` freezes every state with mismatches in it. Per state rather than per type: the movement
  owner vars make 8 idle BooleanSwitches differ (§7.1), and freezing BooleanSwitch as a type would freeze 782 others.
- Shadow snapshots mask a leading `serial` field (Anim u3, Effect u2): the client's readers overwrite the state
  serial with the received one (Anim `G:10a4c80` (T054), Effect `G:d5de40` when active / full [G]), so in shadow mode,
  where the client never receives our data, the counters run independently.
- Current evidence: `systems/data/shadow/genji_anubis.json` (2026-09-27 10:35, T061, library `ac79b51a1ab9` = T060's
  build, Genji on Anubis, fire / secondary / reload / Shift / E / jump × 3, 18 tests): **23 states frozen** (the
  instance logs `frozen_states=23`) = 20 mismatching in this run + `0x157` s14 / s15 / s16 (Stack / Anim / Wait)
  carried over from T057's run: 0 / 73 here and in the 06:48 run, so intermittent, kept frozen [L]. No new mismatch
  vs T057. Mismatching corrected states: the movement-driven BooleanSwitches of `0x157` / `0x15` / `0x1c7` / `0x304`
  and Shift's Wait / LogicalButton / LedgeClimb / BooleanSwitch (`0x157` s0 / s1 / s5 / s6) / DataFlowMapping /
  ChaseVar / MoveTo (movement owner vars), a Stack predecessor on Shift (`0x15` s13, `0x1db` s24 / s25, T054 batch 3),
  E's CosmeticEntity `0x304` s43; `0x1c7` s13 TrackMovement mismatches too but is frozen anyway (no writer) [L].
  Secondary fire shows no Statescript difference at all.

### 10.5 Live checks  (2026-09-27, Anubis, Genji, the unmodified client)

First run [L]:

`make run INSTANCE_ARGS="--evidence ../systems/data/shadow/genji_anubis.json"` (simulation on by default), one tap
each of fire, reload, Shift, E, jump via `logical_button`: **no wedge, no crash**. The client's sync object stayed
healthy in every sample (`+0x960` = 1, 0–1 packets pending, last applied = the server's CF, 2–4 CF behind the
client's own CF); the ghost's acked base advanced every tick, 0 losses. Fire × 3: ammo `$35` 24 → 21 → 18 → 15, each
value still there 1.5 s later (past the client's 40-CF timeout: confirmed, not predicted). 363 synced states of Genji
corrected, 27 frozen by evidence, 16 frozen for echo-only data (T054 is adding the writers).

The user's play test of that build [H] and what it showed:
- primary: shurikens fly, the hand doesn't animate, ammo steps back mid-volley (12-11-12-9) → data re-sent every
  tick (L12), no var writer (L13b), and the throw Anims frozen by evidence, whose 15 ms time error was ours (L11);
- dash / deflect cooldown: no countdown, no radial → data re-sent every tick (L12);
- secondary: hand anim and ammo fine, no projectiles (as before T053; no Statescript difference in the shadow
  compare); deflect: viewmodel shurikens stay visible (CosmeticEntity s43, runtime); reload, melee, jump fine; double
  jump, wall climb, dash distance: movement (not T053).
All three server causes were fixed and re-verified in shadow (library `9477731add07`), and the group left sim-on for a
re-test.

Re-test [H]: primary "crisp" (hand animation, ammo per shuriken), dash / deflect cooldown countdown and radial
correct. Secondary: the hand animation looks rolled back and the throw sound plays twice: the frozen post-throw Anim
s55 (§10.3). The scroll wheel (next / previous weapon) removed the shurikens for good: the valid-weapon dictionary
`$1d` lacked the filler entry 0 → 0 the client's own spawner writes (Prometheus 92f7d9a); with it, weapon cycling
keeps the weapon [L].

## 11. Not done yet / next

- The tutorial shows hero select; the retail tutorial forced Soldier: 76 without a picker (the user's memory) [?]:
  a forced hero the lobby or the tutorial brain expects is not sent.
- Leave Game sometimes needs two presses (tutorial, 2026-09-26; T028 saw it once on the R&D server): the first
  press sends no 21801 at all (lobbylogic received one), so it is client-side state [?].
- `--batch` packing has not been run against the client.

- Statescript on the server: the host is in (§9); what it simulates grows with the runtime's node types (T041).
  Input decoding beyond buttons / action code (aim, movement), movement, combat.
- Other players' controllers are not replicated to each other (only their bodies); N players are structurally
  supported, only one was tested.
- Delta replication by acknowledged state instead of resending lost entries.

## 12. The match host: brain and objectives on the server  (T057, design; spec stage)

Status: design accepted; stages 0–3 implemented and tested offline (§12.12), stage 4 (live) pending. Written
2026-09-27 for T057. Tags as
above; [D] = read from the install's data. The offline reference is `ss::MatchWorld` (StatescriptNodes.md §6.8).

### 12.1 What it does
The rules of a mode are server-only Statescript (MatchLogic.md §10–§13). The `instance` server gets a **match host**
that, per match:
1. creates the mode's **brain** (Anubis: def `0x39`, graphs `0x029` / `0x08f` / `0x1e1`) and a **server copy of every
   objective placement** (Anubis: the capture points, def `0x37`, graph `0x028`), and runs their graphs in
   server-role sync managers, with the placements' Statescript overrides;
2. sends the **rules layer's** game messages to them (init, phases) and reports player events;
3. answers the graphs' **queries** (players, teams, positions, capture areas …) from the match's world;
4. applies their **effects** to the world and **replicates** them: Statescript remote updates of the brain and the
   copies, the capture area's mirror (component 0x1a), teams (0x10), created / destroyed entities.

### 12.2 The local-entity question: server copies  [G, L; the visible result is checked in stage 0]
The capture points (def `0x37`), doors and the cart are **local map entities** on this client: the load loop spawns
every gameplay placement locally, and def `0x37` is not in the client's networked list (MatchLogic.md §8.2) [G, L].
From the client code there is **no way for the server to reach a local entity**:
- replicated entity ids have no class for `0x8000xxxx`, so no create / update / Statescript packet can name one
  (§8.2) [G];
- game messages never cross the network (§8.4) [G], and the local `0x028` instance reacts only to game-message
  entries (`0x…0e`, `0x…2d`, `0x…0b`, `0x…0d`, `0x…2b7`, `0x…3a7`, `0x…3bb`), which only server-only logic sends
  (§10.4) [L, G];
- its CapturePointEvents reads the progress from component 0x1a `+0xb4` of **its own** entity (StatescriptNodes.md
  §6.3) [G], a mirror the server can only update on an entity it created.

So **the server runs `0x028` on its own copy** of each capture point, created with a **from-map create** of the
point's placed index (Anubis A = 470 `0x1d6`, B = 469 `0x1d5`; no movement snapshot: def `0x37` has no 0x12). The
from-map create moves the placement's binding (entry `+0x28`) to the copy (§8.2) [G, L]. The copy gets the brain's
and the points' Statescript as remote updates (§12.6) and the capture-area mirror (§12.7).

What the client then shows, from graph `0x028` (node flags from `systems/data/live_anubis/objective/dump_028_*`):
- The capture ring (UIInWorld 16 / 17 / 28 / 31, UI `0x09A…EA`), the progress effect (DataFlowMapping 6 / 138 on
  `$009a`) and the HUD view models (`0xc2a01675` 32 / 42 on `0x0AC…1F4`) hang **only** off the client-only OnStart
  entries **34 and 41**; the HUD strings 19–27 also off remote state 184 (`0x9f74c07c`, R10) [D].
- **Client-only OnStart entries do fire on a replicated instance** [L, stage 0]: tracing a fresh server copy
  (`0x400000cb`, remote full update of `0x028`) showed `traversefrom node 34` / `node 41` right after
  `instantiate`, then 118, 167, 119, 133, 29, 178 (aborted), and via R10 184 → 35 → 27 → 26 (view model `0x0AC…1F3`)
  beginning, all at the instantiate CF. That agrees with Ghidra (`+0x130` passes client-only entries); MatchLogic.md
  §12.3's brain observation must have had another cause (`0x1e1` #3's target has a constant-false condition).
  With the point locked (`$0064` / `$0712` false) the branches that show the in-world icon (15 → 16 / 17, 30 → 28)
  are not taken, so nothing shows in the world yet: expected.
- The **orphan** (the local point) keeps running its own `0x028` with the placement's overrides; its `$0064` stays
  false forever, so it stays locked and shows no in-world icon either (same branches). Nothing on the network side can
  remove it (§8.2).

**The capture ring is a separate entity** [L, D]: def `0x2ed` (model, mirrored anim, Statescript graph `0x208`),
one per point (Anubis placements `0x20a` = B at (−7.5, 2.48, 111.3), `0x20c` = A at (−45.6, −2.20, 43.5)). It is a
**mover** (component 0x12), so on this client it sits at the **origin** until a server places it (§8.1): these are the
"rings at the origin, wrongly rotated" the user saw (with the doors). Graph `0x208` (15 nodes): Stack 2 writes `$009a`
from `$0702.$009a`, DataFlowMapping 1 maps `$009a` onto the ring's fill (`0x048…FD`; nothing in 0.8 reads that flow, DataFlow.md §7.2), Effects 3–5 / 8–12 by
BooleanSwitches 6 / 10 (team colours, states). `$0702` (ENTITY, synced) = its capture point; on the local ring it was
resolved at load to the **local** point (`AE:{800001CB}` for `0x20a`) [L], so a local ring can never follow a server
copy. The server therefore also creates **ring copies** (from-map create + a movement snapshot, as the doors, §8.3
[H]) and runs `0x208` on them with `$0702` = its capture-point copy. Tested: from-map create of `0x20c` with a
snapshot puts `0x400000cc` at (−45.6, −2.2, 43.5) [L]; it runs no graph until the server sends `0x208`. The local
rings stay at the origin (already the case today).

**Stage 0 result** (2026-09-27, Anubis, Genji, brain B `0x400000c8` + copies A `0x400000c9` / B `0x400000ca` from
placements 470 / 469) [H, L]:
- The copies sit exactly on the local points' transforms (scene component `+0x60`: (−43.78, 1.66, 45.9) and
  (−7.5, 4.7, 111.3)) [L].
- The copies take the same active remote states as the local points (64–67, 114, 157, 184, 185) [L].
- Top-centre HUD: **one** padlock icon, "0.0", "WAITING FOR PLAYERS" [H]. At A and B: nothing but geometry, no ring or
  icon [H]. Rings at the origin (the ring movers above) [H].
- Why one icon [?]: the copies were created **without the placements' overrides** (the stopgap full update sends vars
  unchanged), so both copies have `$0780` = 0 = slot "A" (the local B has `$0780` = 1, `$008d` = 1 [L]). Their view
  models (26 on `0x0AC…1F3`, indexed by `$0780`) both write slot A. The local points' view models began at map load,
  before any gamemode entity existed (their `0xccde96d8` is a gamemode-entity config var), which may be why they add
  nothing [?]. To be checked in stage 3, when the copies carry their overrides: expected two icons, A and B.

Consequences for the design: send the placements' overrides as the copies' vars in the full update (`$0780`, `$008d`,
`$02b8`, `$0c18`, `$0090`, `$089f`, `$01a3` on Anubis: the owner / sync vars of §10.3), add the ring copies (def `0x2ed`,
graph `0x208`) to the objective set, and expect the in-world icon and ring only once the point is enabled
(`$0064` true) and the ring is placed.

Doors (`0x042`) and the cart stay out of scope. On Anubis the local doors sit at the origin (movers without a 0x12
history, §8.1), so the attackers' spawn exit is open and point A is reachable without them [L, H: Genji played on
Anubis]. The brain's `0x…17` (open the doors) goes nowhere on the client until T010's door copies exist.

### 12.3 Where the graphs run and who hosts them
- **One `ss_match` per match** (§12.4): a C++ match world holding every server-run entity of the match (brain,
  objective copies, and later spawn points, waypoints, volumes). It is `MatchWorld`, extended; it implements
  `MatchHost` itself, so every query is answered **inside C++** from state the Go side pushes in, and every effect is
  applied to that state and appended to an **effect queue** the Go side drains after each step.
- Why not C → Go callbacks per query (T055's first idea): queries run inside `ss_sm_run` (hundreds per frame in
  `TrackTargets` / filters); a cgo callback per query re-enters Go from C on the game loop, needs exported Go
  functions with handle tables, and makes the offline tests depend on Go. The push model keeps the runtime
  deterministic and the reference world and the server's host **the same code**. The price: the Go side pushes
  facts every frame (a few entities: cheap).
- **Players** are fact-only entities in the match world (no sync manager there): hero body id, controller id, team,
  position, yaw, alive, filter bits, selected hero, health. Their Statescript stays in `sim.Runtime` (hero graphs,
  local format). A broadcast therefore does not reach hero graphs yet (follow-up: attach `sim.Runtime`'s sync
  managers to the match world so `BroadcastGameMessage` and PlayScript onto a hero work).
- Entity ids: the Go `world` hands out ids (never reused). `CreateEntity` effects take ids from a block the Go side
  reserves (`ss_match_reserve_ids`); the Go side adds the world entity with that id when it drains the effect.

### 12.4 C API additions (`capi/statescript.h`, "match hosting")
```c
typedef struct ss_match ss_match;
ss_match* ss_match_create(ss_runtime* rt, uint32_t cf, uint32_t seed);   /* clock at cf; RandomInt seed */
void      ss_match_destroy(ss_match* m);
/* Entities. flags: SS_ME_SM (has a server-role sync manager), SS_ME_PLAYER (a player's hero body), SS_ME_PVP
 * (PvP-game component: GetPvPEntity), SS_ME_GLOBAL. */
int  ss_match_add(ss_match* m, uint32_t id, uint64_t def, uint32_t flags);
int  ss_match_remove(ss_match* m, uint32_t id);
ss_sm* ss_match_sm(ss_match* m, uint32_t id);                           /* borrowed; the existing ss_sm_* readers
                                                                          * (sync vars, state data2, instances …)
                                                                          * encode its remote updates */
/* Facts (pushed by the host before each step). */
int  ss_match_set_transform(ss_match* m, uint32_t id, float x, float y, float z, float yaw_deg);
int  ss_match_set_team(ss_match* m, uint32_t id, int32_t team);
int  ss_match_set_filter_bits(ss_match* m, uint32_t id, uint64_t extra); /* besides team + player bits */
int  ss_match_set_alive(ss_match* m, uint32_t id, int alive);
int  ss_match_set_tags(ss_match* m, uint32_t id, const uint64_t* tags, int n);
int  ss_match_set_player(ss_match* m, uint32_t hero, uint32_t controller, uint64_t hero_guid);
int  ss_match_set_health(ss_match* m, uint32_t id, int32_t pool, float cur, float max);
int  ss_match_set_throttle(ss_match* m, uint32_t id, float x, float y);
typedef struct ss_capture_rules { float decay_time, f349aaca6, segments, extra_player, capture_time, f5f080cc5,
                                  decay_delay; int32_t f2ca7fc17; float fe6e7f301; int32_t f3a691fdf, max_players;
                                  float radius; } ss_capture_rules;
int  ss_match_set_capture_area(ss_match* m, uint32_t id, const ss_capture_rules* r);
int  ss_match_reserve_ids(ss_match* m, uint32_t first, uint32_t count);
/* Graphs and the rules layer. */
int32_t ss_match_start(ss_match* m, uint32_t id, uint64_t graph, const uint64_t* override_vars,
                       const ss_value* override_values, int count);                 /* instance id */
int  ss_match_send(ss_match* m, uint32_t target /*0 = all*/, uint64_t message, const uint64_t* keys,
                   const ss_value* values, int count);
int  ss_match_player_event(ss_match* m, int kind /*0 joined 1 left 2 died*/, uint32_t player, uint32_t other);
void ss_match_step(ss_match* m, uint32_t cf);                                      /* runs cf on every entity */
/* Effects since the last drain, in order. */
typedef struct ss_match_effect {
    uint32_t seq, cf; int32_t kind;          /* SS_FX_TEAM, TAGS_ADD, TAGS_REMOVE, TELEPORT, CREATE, DESTROY, RESPAWN,
                                              * MATCH_TIME, GAME_FINISHED, END_MATCH, CAPTURE_ENABLED, CAPTURE_OWNER,
                                              * CAPTURE_PROGRESS, SPAWN_ENABLED, PAYLOAD_STATUS, BROADCAST */
    uint32_t entity, other;                  /* subject; creator / sender */
    int32_t i0, i1;                          /* team, flags (contested, on/off, moving), counts */
    float f[4];                              /* pos + yaw, progress, seconds */
    uint64_t u;                              /* def, message id, tag */
} ss_match_effect;
int  ss_match_effects(ss_match* m, ss_match_effect* out, int cap);                   /* drains; returns count */
typedef struct ss_capture_state { int32_t enabled, owner, capturing_team, contested; float progress; } ss_capture_state;
int  ss_match_capture(ss_match* m, uint32_t id, ss_capture_state* out);
```
Library changes behind it: `MatchWorld` gets fact-only entities (no sync manager), a controller ↔ hero map
(`PossessedEntity`, GetEntityController), `Health`, `HeroOf`, `GlobalEntity`, the id block, and records every effect
in a queue besides its log; `ss_match` wraps it. The T055 scenario tests are ported to run through the C API as well
(one ctest), so the reference and the server share one path.

### 12.5 The Go host (`inst/game`, new package)
One `game.Match` per match, owned by `inst/match` (game loop goroutine, no locks), next to `sim.Runtime`:

| step | what |
|---|---|
| match create | picks the brain for the map's mode (`maps.Map.Mode`: assault → def `0x39` [?, strong: MatchLogic.md §10.5], escort → `0x12e3`, later); adds to the Go `world` a **brain** entity (definition create) and an **objective** entity per objective placement (from-map create), new kinds `KindBrain` / `KindObjective`; adds them to the `ss_match` with their graphs (the definition's Statescript graphs, from content) and the placement's overrides (sent as the copy's vars in its first full update: stage 0 showed copies without them all act as point A); each capture point's **ring copy** (def `0x2ed`, from-map create + movement snapshot, graph `0x208` with `$0702` = the point's copy, §12.2); capture points get their `STUECCaptureAreaInstanceData` as `ss_capture_rules` (radius 6 m [?: the shape asset is not read]) |
| data | `internal/maps` (generated by `mapgen`) gains `Objectives []Objective{Index, Def, Pos, Yaw, Graphs, Overrides, Capture *CaptureRules}` (mapgen already reads per-component instance data; the overrides are the placement's Statescript override blob, as `tutmap` reads them) |
| each tick, frame P | push facts of every player's hero (position §12.9, yaw, team, alive), then `ss_match_step(P)`, then drain effects (below), then queue replication (§12.6, §12.7) |
| effects | TEAM → world team + `Mirror10` to every joined player; CAPTURE_ENABLED / CAPTURE_PROGRESS / CAPTURE_OWNER → the copy's 0x1a mirror state (§12.7); CREATE / DESTROY → world entities + creates / destroys; MATCH_TIME, GAME_FINISHED, END_MATCH → recorded (`/matches`), the end of the match is a follow-up; SPAWN_ENABLED → the spawn choice for (re)spawns; BROADCAST → log |
| rules layer | §12.8 |
| control API | `GET /matches` shows per match: phase, brain / objective entities, capture state per point, effects counters, unmodelled node types; `POST /game/send {"target", "msg", "params"}` (raw rules-layer message), `POST /game/pos {"body", "pos"}` (§12.9); `GET /game/log` (the effect log) |

### 12.6 Statescript replication of server-run entities (remote format)
- Every joined player gets, for the brain and each objective copy: after the entity's create, a **remote full
  update** (`replication.RemoteFull`, §10.2 R1–R5) from a remote-format snapshot of the entity's sync manager
  (`ss_match_sm` + the existing snapshot code of `sim`), then **remote deltas** (`RemoteDelta`) when the snapshot
  changed, as a union from the last acknowledged state (the local ghost's rule, L2 / L3, applied per entity and
  connection: a `RemoteGhost`). Remote SYNC_ALL actions go in the actions section (R6): banners, effect swaps,
  capture music.
- **Policy**: as §10.3, per state type and format (`ss_runtime_type_cap(…).DataRemote`): states whose remote data the
  runtime can't write are frozen at what was last sent. For `0x028` the remote states are Stacks (1 bit: no
  predecessor [L]), CapturePointEvents (`0119c536`, R12 in the current layout), `0x9f74c07c`, `0x4ab09851`,
  effect swaps: their remote data formats are checked against StatescriptSync.md §4.2 before stage 3 [G, open for
  `0119c536` and `0x9f74c07c`].
- Time fields in remote data: the snapshot is taken at the viewer's newest input frame (R4 as today).
- Order on join: world creates (placements, brain, copies), then the full updates, then the hero lock-in as today.

### 12.7 The capture area's mirror (component 0x1a)
The client's CapturePointEvents (active as remote state on the copy) makes `$009a` read the copy's component 0x1a
`+0xb4` [G], so the **progress travels as mirror data**, not as a Statescript var. (Live, the sent progress lands at
component `+0xc4` [L, §12.13]: the mirror STU starts at `+0x28`; which of the two offsets `0x10bcc50` reads is to be
rechecked, the arc filled either way [H].)
- Mirror type `47c72f78` (176 B, 12 fields, no parent) [D: client_stu_registry]: 0 `aca1ca5c` list, 1 `319337e2`,
  2 `1d75c1a4` i32, 3 `41c241af` i32, 4 `48d74852` f32, 5 `51fdea67` f32, **6 `794d7691` f32 progress** (`+0x9c`, component
  `+0xb4`), 7 `8b0c4e7d` f32, 8 `9d98248a` f32, 9 `42ceb92c` i32, **10 `0ab6f7cd` bool enabled**, 11 `8cb5cafe` bool. A
  component update is the 2-byte changed mask + the changed fields' values (§4 `ComponentUpdate`). The f32 and bool
  field readers (vfunc `+0xb0` of elem types `8fa75a30` / `3b9327d2`) are to be read in Ghidra in stage 3 [open];
  the other fields' meanings (capturing team, occupant counts?) too, as far as the HUD needs them.
- New encoder `Mirror1A(fields)` (stopgap, T049 boundary: later STU reflection).
- **Rate and loss**: a coalesced entry per (connection, entity, component) that encodes the **current** value when the
  packet is built (like the local delta, §9.2 step 4), queued when the value changed; at most one in flight per
  entity, re-queued on loss. This avoids §4.1's "older update after a newer one" roll-back. Progress changes every
  frame while capturing (1/30 s⁻¹ on A), so one update per tick (16 ms) is the ceiling; stage 3 measures whether every
  4th tick is enough for a smooth ring.

### 12.8 The rules layer (Anubis, assault)  [? designed; the messages and params are the graphs' own [D]]
| when | message | to |
|---|---|---|
| match create, after the copies are started | `0x…0e` {`0x…10` = 0 (defenders own the points)} | each capture point |
| a player's hero joins (lock-in) | player event Joined (the brain's `97315c42` states, if any run) | all |
| first lock-in | `0x…2d` {`0x…29` = `0x0D8…16`} (setup) | all |
| setup time later (flag `--setup`, default 10 s for testing [?: retail attack setup was ~60 s from the user's memory]) | `0x…2d` {`0x…29` = `0x0D8…15`} (assault in progress: the brain broadcasts `0x…17`, point A enables itself) | all |
| a point's CAPTURE_PROGRESS reaches 1 on the last point | `0x…2d` {`0x…29` = `0x0D8…18`} (ended), then after 10 s END_MATCH handling (follow-up) | all |
| hero death / leave | player events Died / Left | all |

Consumed (recorded, no listener on the client): `0x…51` / `0x…52` (capture stopped / started, to the gamemode),
`0x…114`, `0x…120`, `0x…130`, `0x…190`, `0x…5a4` / `0x…5a5`. Not sent in this task: `0x…2b7` (zone occupancy for the
attack / defend chat banners), `0x…3a7`, `0x…3bb`, `0x…45`, the HUD's `0x…1a3` / `0x…3d` / `0x…e3`.

### 12.9 Player positions: the missing input  [open: a decision for the user]
Capture needs the attacker's position in the zone. **The server has none**: the client's input stream carries aim and
movement axes, not positions (§9, `wire.InputEntry`), and there is no server movement model yet (roadmap #10; phase
A5 says "client-authoritative for now, the server records positions", which needs a position source). Options:
1. **Prometheus position bridge (proposed for the live stage)**: `live.py` polls the local hero's scene position
   (MCP `read_component` 0x01 of the hero, ~10 Hz) and posts it to `POST /game/pos`. Measured positions, no model;
   test scaffolding only (the unmodified client alone would not capture), clearly logged as such.
2. **Dead reckoning** from the movement axes, aim yaw and a run speed, without collision: drifts from the real
   position within seconds (walls, slopes, jumps) [?].
3. **Server movement** (roadmap #10): the right answer, its own task.
4. For tests: `POST /game/pos` by hand (teleport the server's idea of the hero onto the point).

The host takes positions through one interface (`game.PositionSource`), so 1 → 3 is a swap. Recommendation: 1 + 4
now, 3 as a follow-up task.

### 12.10 Stages and tests
| stage | what | test | needs |
|---|---|---|---|
| 0 | live look at server copies (§12.2) with today's control API: **done** 2026-09-27 (entries 34 / 41 fire on copies; the ring is the `0x2ed` mover) | the user's description + ss_trace | game lock |
| 1 | C API `ss_match_*` + MatchWorld extensions | ctest: T055's Anubis capture and contest scenarios through the C API; effect queue contents | `systems/statescript` lock |
| 2 | `inst/game` + map objective data + rules layer, offline | go test: a match on Anubis with a fake position source: setup → in progress → A enabled → attacker on A → progress, A captured, B enabled; effects → world (teams, capture state) | `server/inst` lock |
| 3 | replication: brain + copy creates, remote full / deltas, `Mirror1A`, team updates | go test: bytes (remote encoders over the match's snapshots), coalesced mirror entries; Ghidra: the 0x1a field readers | – |
| 4 | live on Anubis, attack team, position bridge | the user: capture progress on the ring / HUD, A captured, B unlocks | game lock |

### 12.12 Implementation (stages 1–3, 2026-09-27)
- **Library** (`systems/statescript`): `MatchWorld` gained fact-only entities (`Add(id, def, with_sm = false)`),
  controller ↔ hero (`PossessedEntity`, `HeroOf`), health pools, `GlobalEntity`, an id block for CreateEntity
  (`next_id` / `id_end`), positive (mirrored) instance ids (`mirrored_ids`), `PlayerEvent`, `SetFrame` and an **effect
  queue** (`effects`, one record per effect, as logged). C API `ss_match_*` (§12.4) over it; Go wrapper
  `statescript.Match` (`go/statescript/match.go`). Tests: `statescript_capi_match_tests` (the Anubis flow through the C
  API: init, phase, A captured in 30 s, B enabled, match time +300 s), `TestMatchAnubisCaptureA` (Go); ASan / UBSan clean.
- **Map data**: `mapgen` writes each map's `Objectives` (`internal/maps`: placed index, definition, transform, mover
  flag, graphs, typed overrides with ENTITY references resolved from placement UUIDs, capture rules). Anubis: A `0x1d6`,
  B `0x1d5`, their rings `0x20c` / `0x20a` (graph `0x208`, `$0702` → the point), and two more placements `0x20d` /
  `0x20e` (defs `0x471` / `0x47a`, graph `0x2bb`) that reference the points the same way.
- **Server** (`inst/game`): `game.New` (from `match.Config.NewGame`, when the map's mode has a brain: assault → `0x39`)
  adds the brain (`KindBrain`) and every objective copy (`KindObjective`) to the world and the match world, starts
  their graphs with the overrides (ENTITY → the referenced copy), sends init `0x…0e` {owner 0}; `Tick` pushes every hero's
  position / team, runs the phases (first lock-in → setup, `--setup` s later → in progress, last point captured →
  ended), steps and applies the effects (teams, capture state). `View` (per player): on join the creates (brain:
  definition create; copies: from-map, movers with a snapshot) and remote full updates; every 4th tick
  (`Config.GameEvery`) remote deltas from the acknowledged base (one in flight per entity) and the capture mirror
  (`replication.Mirror1A`: progress + enabled; one in flight per point). Remote state policy: §10.3's for the remote
  format, Stack = one `0` bit [L]. Control API: `POST /game/pos`, `POST /game/send`; `GET /matches` shows `game`
  (phase, points, effect counts). Flags `--no-game`, `--setup`. Position bridge: `server/scripts/posbridge.py`.
- **Tests**: `inst/game` `TestAnubisAssault` (setup → A enabled → nobody on A: no progress → attacker on A: 0.14–0.19 at
  5 s, captured at 30 s, B enabled → B captured → ended) and `TestViewReplication` (7 creates + 7 full updates encode;
  deltas and mirror updates as A is captured).
- Not done yet: target tags (component 0x40: the points add `0x0D6…29B` while enabled), filter bits on the copies,
  remote SYNC_ALL actions (banners, effect swaps, music), a union for remote deltas (a value changed and changed back
  between two acknowledgements is not re-sent), CreateEntity effects adopted by the world, the f32 / bool field readers
  of the 0x1a mirror (to be confirmed live).

### 12.13 Live (stage 4, 2026-09-27, Anubis, Genji, the unmodified client)
Setup: `make run INSTANCE_ARGS="--evidence ../systems/data/shadow/genji_anubis.json"`, position bridge
(`scripts/posbridge.py`) on. Measured [L] / seen by the user [H]:
- The client creates the brain and the 6 objective copies (30 creates on join, 7 remote full updates, queue drained);
  copies A / B carry their overrides (`$0780` 0 / 1, `$008d` 0 / 1) and the local points' active set; both ring copies
  sit at their placements [L].
- HUD: **two objective icons** (A, B) [H]. After setup (10 s) the phase goes to in progress and A enables: the in-world
  **"A ATTACK" marker** appears [H].
- Standing in A: the server's progress rises at 1/30 s⁻¹, decays when left, and the **progress arc around the A marker
  fills** [H]; the client's capture-area component holds the server's value (0.56 vs 0.54 at the read) at component
  **`+0xc4`** (the mirror STU sits at component `+0x28`, not `+0x18`: §12.7's offset corrected), so the f32 field is 32 raw
  bits [L]. **A captured** at 30 s, A disabled, **B enabled** ("B ATTACK" marker) [H, L]; B captured at 18 s and the
  phase went to ended [L].
- Not working yet [H]:
  - the **top-centre objective icons never unlock** (A's stayed locked while A was enabled and capturable, B's after A);
    the client's B copy had `$0064` = 1 and `$0195` = `0x…15` [L], so the icon follows something else: candidates are
    the owner vars the Stack states fold (`owner.$0064 := $0064`: the server sends stored values, 0) and brain B's HUD
    message vars (below) [?];
  - the **HUD / announcement texts**: brain `0x029`'s display-text vars (`$0738…$073b`, `$0784/5`, `$07dc`) are INT64 0
    on the server (the runtime doesn't evaluate UXDisplayText-type defaults); sent, the HUD printed "0X000…"; now left
    out (`game.Snapshot`), it prints "0";
  - **no ring on the ground** at A, although the ring copy (def `0x2ed`, graph `0x208`) sits at its placement with
    states 1, 2, 4, 5, 11, 12 active; its `$009a` stays 0 on the client [L] (its Stack reads `$0702.$009a` across entities,
    not modelled for a remote copy) and the ring may need its mirrored animation (component 0x1c) [?];
  - after B nothing happens: the end of the match (GameFinished, end screen) is not implemented.
- Shadow evidence refreshed first (library changed): 21 mismatching states, 7 new vs 06:48 (`0x157:14/15/16`,
  `0x15:12`, `0x304:0/43/56`); merged with the old file (`0x157:5/6`, secondary fire not re-run): 23 frozen.

### 12.11 Follow-ups (out of scope)
Payload movement and the cart (Dorado, §12.4 of MatchLogic.md), doors (T010), the tutorial's steps, spawn points /
volumes / healing as server-run entities (their graphs listen to `0x…71`), the end of a match (GameFinished → the
client's end screen), match time in the PvP-game mirror 0x24, hero graphs receiving broadcasts, server movement
(§12.9), multiplayer, zone shapes (spheres now), the attack / defend chat banners (`0x…2b7`).

### 12.14 Crash: the assault rules host on the TUTORIAL map  (2026-09-27)  [L, G]
**Fixed (T059, 2026-09-27)**: the tutorial has its own mode and no rules host, and a residency guard keeps every
create of a non-loaded definition from the client (§12.15). TUTORIAL from the menu loads without a crash [H].
Hanamura (brain `0x1FA`) and Volskaya (`0xEDC`) had the same bug in waiting: "assault" by their objectives, they
would have been sent `0x39` too, which only Anubis loads [D].

**What happened** [L]: the user clicked TUTORIAL (24001 type 4) at 08:27:59; lobbylogic sent 20600 for map
`0x0800000000000661` to `instance`. The server logged `game: set up mode=assault brain=0x40000002 objectives=2
capture_points=1` for TUTORIAL, the client loaded the map (21602 at 08:28:01), the server queued the controller and
the world (9 entries, including the brain create), and the client died at 08:28:03 without a Prometheus log entry.

**Where the client died** [L: `~/steam-default.log`, `PROTON_LOG=1 WINEDEBUG=+seh`]: `EXCEPTION_BREAKPOINT`
(int3, an assert) at `0x140C214C4` (RVA `0xC214C4`, Ghidra `0x7ff650c014c4`), preceded by a
`DBG_PRINTEXCEPTION_C` (the assert text; not captured, the debug-string channel is off). `rbx =
0x0400000000000039` = the **assault brain definition**.

**The assert** [G]: it sits in `FUN_7ff650c00d20`, the replication create-entity handler (ECS_Systems.md: op 1).
The handler looks the definition GUID (`[rbp+0x38]`) up with `FUN_7ff6509a6470`; if the lookup returns null, or the
resource's type check (vtable `+0x98`) fails, it calls the assert reporter `FUN_7ff650806fd0` (source line `0x255`)
and hits int3. So **the client asserts when the server creates an entity whose definition is not loaded**.

**Why** [code]: `server/internal/maps/maps_gen.go` gives TUTORIAL `Mode: "assault"` (the mode is derived from the
placed objectives, and the tutorial has a capture point for its step 5); `inst/game` then spawns
`brains["assault"] = 0x0400000000000039`. That definition is resident on Anubis [L, MatchLogic.md §2] but, by this
crash, not on the tutorial map [inferred, strong]. The tutorial's own brain is `0x128E` (MatchLogic.md §13).

**Fix (proposed)**:
1. mapgen: `Kind: "tutorial"` maps get mode `tutorial` (no assault host); the tutorial brain comes with the
   tutorial work.
2. A server-side guard: never create an entity whose definition is not loaded on that map (per-map resident
   definition set from content, or at least a per-map brain allowlist). A wrong create is a hard client crash.
3. Debugging aid: add `+debugstr` to `WINEDEBUG` in `build-and-run-with-server.fish` so assert texts are logged.

### 12.15 The residency guard: which definitions a map has loaded  (T059, 2026-09-27)  [L, D]
**The rule** [L, D]: on a map, the client has exactly these entity definitions resident (up to a small
over-approximation, below):

  **resident(map) = global ∪ defs(package(map))**

- **package(map)** is the asset package that lists the map data (`content.ResourcePackages(map data)[0]`,
  ContentAccess.md §4); **defs** = its type-0x003 resources (`0x040…`). Anubis 106, tutorial 87, Dorado 150.
- **global** is what the client keeps on every map: the 361 definitions resident at the title screen *and* on
  Anubis *and* on the tutorial (the title screen adds 21 more, all from the title map's package, which unload
  when a map loads). It holds the controller `0x0E`, the lobby's `0x13` (brain A), all 21 heroes, … Every
  global definition is also held by some non-map package; which packages the client loads at start was not
  traced, so the set is **measured**, not derived [L].
- The **gamemode brain** a map expects is its environment's `+0xF0` (map data `+0xF0`, Maps.md §7), and it is in
  the map's own package on every map checked [D]: Anubis `0x39`, tutorial **`0x128E`**, Dorado `0x12E3`, King's
  Row `0x12C` (all maps: `maps.Map.Brain`). The assault brain `0x39` is in Anubis' package only among these, so
  it is **not** resident on the tutorial or Dorado [D, L] — the §12.14 crash.

**Measurements** (`systems/data/maps/resident/*.json`, tool `systems/data/maps/tools/resident.py`: Prometheus
`list_stu_resources`, every `0x040…` resource) [L]:

| where | resident | predicted | resident ∖ predicted | predicted ∖ resident |
|---|---|---|---|---|
| title screen (fresh client) | 382 | – | – | – |
| Anubis (match, Genji) | 466 | 466 | 0 | 0 |
| tutorial (instance `--no-game`, Roadhog) | 416 | 448 | `0x5C3` (not in any map package; hero-dependent? [?]) | `0x409`, `0x4C9` |
| Dorado (instance `--no-game`) | 504 | 511 | 0 | `0x2E6`, `0x336`, `0x33F`, `0x4C9`, `0x574`, `0x58F`, `0xCB6` |

So a package is not loaded as a whole: 0–7 of its definitions stay unloaded (which ones is decided by what the
map's placements and their definitions pull in, not modelled). The guard accepts that over-approximation; every
definition the server creates today is in the exact part (controller, heroes, the map's brain, from-map
placements).

**Data** (`mapgen` → `internal/maps`): `Map.Brain` (map data `+0xF0`), `Map.Defs` (sorted package definitions),
`maps.Global` (sorted: title ∩ Anubis ∩ tutorial from the measured files), `(*Map).Resident(def)`. `Mode` is set
for `game` maps only (from the placed objectives), `tutorial` for the tutorial, `""` for title-screen and test
maps. Refreshing `global` needs a new title / Anubis / tutorial measurement (a new build only).

**The guard** (`replication.Replicator`, the one place every create passes): a connection's replicator gets its
map's residency (`Resident func(def) bool`, `Placed int`). `Queue` refuses (does not send, logs the entity and
the definition) a **definition create** whose `0x0400000000000000 | Def` is not resident, and a **from-map
create** whose placed index is ≥ `Placed` (the client reads that entry's record without a check, MatchLogic.md
§8.2); every later entry for a refused entity (updates, Statescript, destroy) is dropped as well; the rest of the
batch goes on. `Refused` counts them (control API `/conns`).

**Mode → host** (`inst/game`): the host keeps its brain per mode (`assault` → `0x39`) and runs only if the
map's own brain (`Map.Brain`) is that definition and it is resident; otherwise no game (logged). The tutorial
(mode `tutorial`, brain `0x128E`) gets none until the tutorial work adds its host; so do Hanamura (`0x1FA`) and
Volskaya (`0xEDC`), whose brains are other definitions than Anubis' (whether they run the same graphs is not
checked: follow-up).

**Tests**: `internal/maps` `TestResidency` (the global set, the crash case, every map's brain / networked /
mover / objective placements resident); `inst/replication` `TestResidencyGuard` (tutorial: `0x39` and a placed
index past the list refused with their later entries, controller / hero / `0x128E` / the spawn point sent);
`inst/game` `TestNoHostWithoutTheMapsBrain` (tutorial, Hanamura, Volskaya, Dorado: no host) and
`TestViewReplication` (Anubis' 7 host creates pass the guard).

**Live** (2026-09-27, the unmodified client): TUTORIAL from the menu: `game: no rules host for this map's mode
… mode=tutorial map_brain=0x040000000000128e`, 3 creates, 0 refused, the map loads and the hero plays, **no
crash** [L, H]. Anubis: the host set up as before (6 objective copies, 55 join entries, nothing refused), the two
objective icons as before [L, H].

**Debugging aid**: `build-and-run-with-server.fish` (and so `live.py launch`) runs with
`WINEDEBUG=…,+debugstr`: the client's `OutputDebugString` texts, assert messages included, land in
`~/steam-default.log` (`:debugstr:` lines) [L].


## Lobbylogic link: hello and heartbeat (T058)

Both game servers use `internal/gamelink.Client` (the R&D server through `internal/instance/link.go`, `instance`
through `inst/lobbylink`). One JSON object per line over TCP to lobbylogic's `:3727`:

| direction | message | fields |
|---|---|---|
| game → logic | `hello` | `addr` (UDP address for 20600), `name` (empty = `gameserver`), `conns` (its assignments), `ping` (interval ms; 0 = sends no pings), `modes` (`matchmaking`, `private`, `practice`, `tutorial`; empty = all), `maps` (map data GUIDs; empty = all) |
| game → logic | `ping` | every 2 s |
| logic → game | `pong` | the answer to each ping |
| logic → game | `assign` / `release` | `conn` (unchanged) |
| logic → game | `view` | `conn`, `view` (on/off): the live server view of a connection (T072; only to a server whose hello says `view: true`) |
| game → logic | `frame` | `frame {conn, seq, frame, sent_us, body}`: one live server view frame per tick (DebugDrawing.md §5); sent through a 64-deep queue that drops when full |

- lobbylogic drops a server whose hello announced pings after 3 intervals (6 s) without a message: a hung
  process counts as gone. The game server drops and redials the link after 6 s without a message, once
  lobbylogic has answered at least one ping. Peers without pings or pongs (older builds) keep working without the
  timeout.
- Both servers currently announce every mode and every catalogue map (`gamelink.AllModes`, `gamelink.AllMaps`).
  lobbylogic refuses to assign a map that the active server doesn't list, and `!gameserver` shows the modes, the
  number of maps and whether the server sends a heartbeat. Gating single buttons by mode is not done yet.
