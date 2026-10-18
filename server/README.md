# server/: the Go servers

This directory holds every server the **unmodified Overwatch beta 0.8 client (build 24919)** talks to, written in
Go: a Battle.net-style **login server**, a **lobby gateway** and the **lobby logic** behind it, an **R&D game
server** used as a reverse-engineering harness, and **`instance`**, the game server meant to serve real matches.
`instance` links the C++ reference implementations in [`../systems/`](../systems/README.md) (ECS, STU, content
access, the Statescript runtime) through cgo.

The client is the specification. Every message, bit layout and timing rule these servers produce was read from
the client in Ghidra or measured on a live client through Prometheus, and is written up in [`../docs/`](../docs/README.md).
This README covers how the servers fit together, how to build and run them, what each binary does, and where the
details are documented.

> Not affiliated with or endorsed by Blizzard Entertainment. This directory contains no game client, no game data
> and no Blizzard assets. Some code generators and the `instance` server need your own copy of the 0.8 client.

---

## Contents

1. [The big picture](#1-the-big-picture)
2. [Prerequisites](#2-prerequisites)
3. [Building](#3-building)
4. [Running](#4-running)
5. [Connecting the client](#5-connecting-the-client)
6. [The binaries in depth](#6-the-binaries-in-depth)
7. [Package map](#7-package-map)
8. [Protocols](#8-protocols)
9. [Generated code](#9-generated-code)
10. [Testing](#10-testing)
11. [Debugging and logs](#11-debugging-and-logs)
12. [Status: what works and what doesn't](#12-status-what-works-and-what-doesnt)
13. [Open work and ground rules](#13-open-work-and-ground-rules)

---

## 1. The big picture

The 0.8 client goes through three servers in turn. First it logs in to Battle.net (BGS RPC over TLS). Battle.net
refers it to a lobby server (an encrypted TCP stream of JAM messages), which runs the title screen, party, social
panel, matchmaking and custom games. When a match is found, the lobby sends it **20600 "game found"** with a game
server's UDP address and a connection key, and the client connects to that game server.

We split the lobby and the game side into separate processes so each can be rebuilt and restarted while the client
stays connected:

```
                           ┌──────────────────────────────── lobby group (make lobby) ───────────────────────────────┐
                           │                                                                                         │
 GameClientApp.exe ──TLS──▶│ loginserver  :1119  BGS RPC (Connect, Logon, web challenge, referral)                   │
 (+ Prometheus)    ──HTTP─▶│              :8081  web login form (skipped with --web-auto-login, the default)        │
        │                  │                                                                                         │
        │ ─────TCP/JAM────▶│ lobbyserver  :3724  handshake, JAM stream cipher, protocol negotiation, keepalives      │
        │                  │   (gateway)  127.0.0.1:3726  control API: /sessions /traffic /send /eval (Lua) …       │
        │                  │                 │                                                                       │
        │                  │                 │ JSON lines, 127.0.0.1:3725 (lobbylogic dials in; restart freely)      │
        │                  │                 ▼                                                                       │
        │                  │ lobbylogic       lobby handlers by message ID, chat commands, identity, game server     │
        │                  │                  choice; 127.0.0.1:3727 = the game server link (game servers dial in)   │
        │                  └─────────────────────────────────────────┬───────────────────────────────────────────────┘
        │                                                            │ JSON lines (hello / ping / assign / release / view / frame)
        │                  ┌──────────────── games group (make games)┼───────────────────────────────────────────────┐
        │                  │                                         ▼                                               │
        │ ──UDP──────────▶ │ instance    :26600  the real game server: 16 ms tick, clock steering, world,          │
        │   (after 20600)  │             127.0.0.1:3729 control API   replication, server-side Statescript, rules   │
        │ ──UDP──────────▶ │ gameserver  :26500  the R&D server: RE harness, hand-driven                           │
        │                  │             127.0.0.1:3728 control API                                                │
        │                  └─────────────────────────────────────────────────────────────────────────────────────────┘
        │
        └── Prometheus (injected DLL) MCP server on 127.0.0.1:17888 (used by some tools here; not needed to play)
```

Ports, all verified in the `cmd/*/main.go` defaults:

| port | proto | binary | bound to | what |
|---|---|---|---|---|
| 1119 | TCP + TLS | loginserver | `0.0.0.0` | Battle.net (BGS) RPC, the client's `--BNetServer` |
| 8081 | HTTP | loginserver | `0.0.0.0` | web login endpoint (`/bnetserver/login/`) |
| 3724 | TCP | lobbyserver | `0.0.0.0` | the lobby link, the client's `--lobbyServer` and the login referral's `hostv4` |
| 3725 | TCP | lobbyserver | `127.0.0.1` | backend port: lobbylogic connects here |
| 3726 | HTTP | lobbyserver | `127.0.0.1` | gateway control API, including an unauthenticated Lua `/eval` (loopback enforced) |
| 3727 | TCP | lobbylogic | `127.0.0.1` | game server link: `gameserver` and `instance` connect here |
| 3728 | HTTP | gameserver | `127.0.0.1` | R&D control API (loopback enforced) |
| 3729 | HTTP | instance | `127.0.0.1` | instance control API (loopback enforced) |
| 26500 | UDP | gameserver | `0.0.0.0` | R&D game server, advertised as `127.0.0.1:26500` in 20600 |
| 26600 | UDP | instance | `0.0.0.0` | instance server, advertised as `127.0.0.1:26600` in 20600 |

Why two groups:

- The **lobby group** (login, gateway, lobbylogic) holds the client's connection. The client copes badly with a
  vanished lobby: it shows no disconnect screen and does not log in again, so restarting the gateway or the login
  server while a client is connected means restarting the client too. The gateway therefore does only link-level
  work, and all lobby behaviour lives in **lobbylogic**, which may be rebuilt and restarted at any time: the gateway
  keeps the client's link up, and a new lobbylogic gets a `hello` with the live sessions.
- The **games group** (`instance`, `gameserver`) can be stopped, started and rebuilt freely. Each game server dials
  lobbylogic (and retries), so start order doesn't matter. While the active game server is unlinked, lobbylogic
  greys out the title screen's PLAY / PRIVATE GAME / PRACTICE VS AI / TUTORIAL buttons, and they come back when it
  relinks. Restarting a game server does drop a match in progress: the client gets a FIN and returns to the lobby,
  and you rejoin.

---

## 2. Prerequisites

| what | why | notes |
|---|---|---|
| **Go** (`go.mod`: `go 1.27.1`) | everything | `protoc-gen-go` is pinned as a Go tool in `go.mod` (`go tool protoc-gen-go`) |
| **CMake ≥ 3.25, Ninja, a C++20 compiler** (gcc or clang) | the C++ libraries in `../systems/` that `instance` links | `make libs` checks for `cmake` and `ninja` |
| **zlib** (development library) | `systems/content` links `-lz` | |
| **buf** | only to regenerate `gen/` from `proto/` | `make generate`, `make lint` |
| **bash, curl, md5sum, stat, find** | `scripts/watch.sh`, examples below | GNU coreutils / findutils (`stat -c`, `find -printf`) |
| **Python 3** | only `scripts/posbridge.py` | |
| **the 0.8 client install** at `../game/owbetaclient` | `instance` reads Statescript graphs and resources from its CASC data (`--content`); the code generators read it | not distributed; see the top-level [README](../README.md) |
| **Prometheus** (injected DLL) | logging the stock client into our servers needs its lobby blob bypass (§8.2); also its MCP server, the custom JAM channel and some tools | [`../prometheus/README.md`](../prometheus/README.md) |

Only `instance` (and the `inst/` packages that import the cgo wrappers) needs the C++ libraries. `loginserver`,
`lobbyserver`, `lobbylogic` and `gameserver` are plain Go.

### The C++ libraries

`go.mod` uses four local cgo wrapper modules through `replace` directives:

| module | path | links |
|---|---|---|
| `owecs` | `../systems/ecs/go` | `../systems/ecs/build/*.a` |
| `owstatescript` | `../systems/statescript/go` | `../systems/statescript/build/*.a` |
| `owcontent` | `../systems/content/go` | `../systems/content/build/*.a` |
| `owstu` | `../systems/stu/go` | `../systems/stu/build/*.a` |

`make libs` configures and builds `systems/ecs`, `systems/statescript` and `systems/content` with CMake + Ninja.
That step is incremental and cheap when nothing changed, and `make build`, `make instance`, `make games`,
`make vet` and `make test` all run it first. **`systems/stu` is not in `make libs`'s list**, but `inst/sim` and
`inst/game` import `owstu/stu`, which links `../systems/stu/build`. Build it once yourself, or build all five
libraries as the top-level README does:

```sh
# from the repository root
for m in ecs stu content statescript maps; do
  cmake -S systems/$m -B systems/$m/build -G Ninja && cmake --build systems/$m/build
done
```

---

## 3. Building

```sh
cd server
make help          # every target with its one-line description
make build         # make libs + all five binaries into bin/
make loginserver   # or lobbyserver, lobbylogic, gameserver, instance: one binary
```

| target | what |
|---|---|
| `help` | list the targets |
| `libs` | build the C++ libraries the cgo wrappers link (`../systems/{ecs,statescript,content}`, incremental) |
| `build` | `libs` + every server into `bin/` |
| `loginserver`, `lobbyserver`, `lobbylogic`, `gameserver` | build one binary |
| `instance` | `libs` + the instance server. A hash of the linked C++ archives is stamped in with `-ldflags "-X main.libs=…"` (see below) |
| `lobby` | run the lobby group (§4) |
| `games` | run the game servers in `GAMES` (§4) |
| `run` | `build`, then both groups in one terminal |
| `run-login`, `run-lobby`, `run-logic`, `run-game`, `run-instance` | build and run one binary once, flags in `ARGS="…"` |
| `logic-watch`, `game-watch`, `instance-watch` | run one binary under `scripts/watch.sh` (rebuild + restart on change), flags in `ARGS` |
| `generate` | `buf generate`: regenerate `gen/` from `proto/` |
| `lint` | `buf lint` the protos |
| `fmt`, `vet`, `test`, `tidy`, `clean` | `go fmt`, `go vet` (after `libs`), `go test ./...` (after `libs`), `go mod tidy`, remove `bin/` |

Why the archive hash: Go does not track the contents of the static archives a cgo package links, so rebuilding a
C++ library alone would not relink `instance`. The Makefile and `scripts/watch.sh` stamp an md5 of the archives
into `main.libs`, which changes the link and so forces a relink. `instance` logs the hash at start (`libs=…`).

---

## 4. Running

### The two groups

```sh
cd server
make lobby      # login :1119 + gateway :3724 + lobbylogic (watched): start once and leave it running
make games      # instance + gameserver (both watched): stop / restart whenever you like
# or both in one terminal (Ctrl-C stops everything):
make run
```

- `make lobby` runs `bin/loginserver` and `bin/lobbyserver` directly and `lobbylogic` under `scripts/watch.sh`.
  Extra flags: `LOGIN_ARGS`, `LOBBY_ARGS`, `LOGIC_ARGS`, e.g.
  `make lobby LOGIC_ARGS="--name Tracr --icon Mei"`.
- `make games` runs every server named in `GAMES` (default `instance gameserver`) under `scripts/watch.sh`.
  Extra flags: `INSTANCE_ARGS`, `GAME_ARGS`, e.g. `make games GAMES=instance INSTANCE_ARGS="--debug"`.
- Ctrl-C stops the whole group (`wait -n` + a trap that kills the jobs). If one member exits, the group stops.
- Every binary also reads its flags from the environment (`LOGIN_*`, `LOBBY_*`, `LOGIC_*`, `GAME_*`,
  `INSTANCE_*`, from `ardanlabs/conf`), and prints them with `--help`.

**Don't restart the lobby group while a client is connected.** A lobbylogic rebuild is fine (the gateway holds the
connection). Restarting the gateway or login server is not: the client will need a restart as well.

### Auto-rebuild and restart: `scripts/watch.sh` and `<name>.args`

`scripts/watch.sh <name> [args…]` runs `bin/<name>` (built from `./cmd/<name>`) and:

- **rebuilds and restarts** it when a `.go` file changes in one of *its own* packages. It lists the binary's
  dependencies with `go list -deps`, covering the main module and the local `replace`d cgo wrapper modules, so an
  edit to the game server never restarts lobbylogic (lobbylogic only imports `internal/gamelink`, not the game
  servers);
- **relinks** it when a C++ archive it links changes (`../systems/*/build/*.a`). This only applies to binaries
  with cgo dependencies. Run `make libs` (or `cmake --build`) in another terminal and the watcher picks it up;
- **restarts** it when its **args file** `server/<name>.args` changes. The file holds extra flags, re-read on every
  start; `#` starts a comment and bash quoting works. They are passed *after* the command-line args, so the file
  can override them. Two are checked in: `instance.args` and `lobbylogic.args` (both only comments now);
- **restarts** it when it exits on its own (a crash, a bad flag), with a backoff that doubles up to 30 s while it
  keeps dying;
- keeps the old binary running if a build fails.

So changing a flag or a library never needs a group restart: edit `instance.args`, or rebuild the library.

### Choosing the game server

lobbylogic can have both game servers linked at once (each names itself in its `hello`) and sends **new matches to
the active one**. Releases (Leave Game) go to whichever server holds the assignment.

- **In game**: the social panel always shows two incoming friend requests, **R&D#0001** (→ `gameserver`) and
  **GAME#0001** (→ `instance`). Accepting one makes that server active and confirms with a chat line. Both
  requests are re-sent after either answer, so they stay in the list.
- **Chat**: `!gameserver` lists the linked servers (address, modes, maps, heartbeat, assignments, which one is
  active); `!gameserver instance` / `!gameserver gameserver` switches.
- The choice is saved to **`server/game-server.json`** (`--game-server-file`). A saved choice overrides
  `--game-server`, whose default is **`instance`**.
- Availability follows the active server: switching to a server that isn't linked greys the menu buttons out after
  `--offline-after` (2 s).

### Player identity

The local player's BattleTag and icon are kept in `server/lobby-identity.json` (path overridable with the
`LOGIC_IDENTITY` environment variable), so lobbylogic restarts keep them. `--name` / `--icon` (env `LOGIC_NAME`,
`LOGIC_ICON`) override the file for one process without writing it. `#1234` is appended to a name without a tag.
The icon is one of the 23 portrait names 0.8 ships (`!icon list`) or a GUID. An unknown name is a startup error.
In-game changes: `!name`, `!icon` (these persist). The default identity is `Player#1234` with the Tracer icon.

```sh
make lobby LOGIC_ARGS="--name Tracr --icon Mei"
```

### Chat commands

Type them in the lobby chat (a line starting with `!` is a command, not a message), or run them from a shell
through the gateway (§6.2):

```sh
curl -X POST --data 'command("instance")' http://127.0.0.1:3726/eval
curl -X POST --data 'command("gameserver instance")' http://127.0.0.1:3726/eval
```

The reply appears in the client's chat. The full list, from `internal/lobbylogic` (`!help` prints it in game):

| command | what |
|---|---|
| `!help` | list commands |
| `!instance [map]` | send 20600 "game found": load a map (name or GUID, default Temple of Anubis) and connect to the active game server |
| `!maps` | list the maps `!instance` and the MAP pickers know (from `internal/maps`) |
| `!gameserver [name]` | list the game servers, or send new matches to `gameserver` (R&D) / `instance` |
| `!queue` / `!unqueue` | enter / leave the matchmaking queue (the Searching panel) |
| `!found <n> [needed]` | update the Searching panel's player count |
| `!friend <name>` | add an online friend |
| `!request <name>` | receive a friend request |
| `!invite <name>` | that friend (created if needed) joins your party |
| `!friends` | the server's view of your friends and party |
| `!sessions` | connected clients |
| `!name <BattleTag>` | set your name (party panel, social panel, chat) |
| `!icon <name\|guid\|list>` | set your player icon |
| `!hero [name\|random\|list]` | featured title-screen hero (the client keeps its pick until it restarts) |
| `!skin <hero> <1\|2\|variant name>` | a hero's skin variant, e.g. `!skin mei pink` |
| `!equip <hero> <unlock>` | equip an intro / spray / theme by name or GUID |
| `!unlocks <hero>` | a hero's unlocks and what is equipped |
| `!level <n>` | set every hero's level |
| `!products [none]` | send the owned-product set (all heroes, or empty) |
| `!notice <0-51>` | a notice from the client's notice table (20803) |
| `!popup <string id> [custom]` | a localized string as a notice (20601, or 23307 with `custom`) |
| `!playtime on [played h] [remaining h] \| off \| warn [seconds]` | the play-time tracker (20804) |
| `!msg <id> tokens…` | send any `ClientInLobby*` message built from typed tokens (a malformed one drops the link) |
| `!custom <tool> [json]` | run a Prometheus tool in the client over the `PrometheusLobby` JAM protocol (`!custom tools`) |
| `!serverview on\|off\|status` | stream the game server's view of your match to Prometheus every tick |

What each of these sends and what the user saw is in [`../docs/LobbyServer.md`](../docs/LobbyServer.md).

### The `/eval` Lua endpoint

The gateway embeds a Lua VM (gopher-lua) with a persistent state, reachable at `POST http://127.0.0.1:3726/eval`.
It is the main tool for live experiments on the lobby link: send any message to the client, inject a message as if
the client had sent it, hook incoming messages, read the traffic ring.

```sh
curl -s http://127.0.0.1:3726/                                   # control API + Lua API help
scripts/lua.sh 'sessions()'                                      # the same as curl … /eval
scripts/lua.sh 'dump(20)'                                        # last 20 messages (lua/init.lua helper)
scripts/lua.sh 'chat(2, "", 0, "hello from the server")'         # a system chat line (lua/init.lua helper)
scripts/lua.sh 'command("instance")'                             # run a lobbylogic chat command
curl -X POST --data 'send(nil, 20700, unhex("00000000000000000000000000000000 00000000"))' \
     http://127.0.0.1:3726/eval                                  # an empty party: the menu buttons grey out
```

Functions: `sessions()`, `send(protocol|nil, msg, payload [, session])`, `inject(msg, payload [, session])`,
`on(key, fn)` / `off(key)` / `handlers()` (key = message ID, protocol name or `"*"`; return `true` from `fn` to
keep the message from lobbylogic), `recent([n [, session]])`, `protocols()` / `protocol(name)`,
`pack(fmt, …)` / `unpack(fmt, s [, pos])`, `hex` / `unhex`, `disconnect([session])`, `print` / `log`, `help()`.
`lua/init.lua` runs whenever the state is created or reset (`POST /lua/reset`) and defines `ids()`, `dump(n)`,
`chat(…)` and `command(line)`. The VM has no `os` / `io` / `dofile` / `loadfile`, every eval or handler has a 5 s
limit, and the control API refuses to listen on anything but a loopback address.

---

## 5. Connecting the client

The client takes the server addresses on its command line:

```
--BNetServer=127.0.0.1:1119 --lobbyServer=127.0.0.1:3724
```

On Linux the repository's top-level scripts start Prometheus (which injects itself into the game) through
umu-launcher + GE-Proton:

```sh
# from the repository root, servers already running
./build.sh                                                     # Prometheus launcher + DLL (once / after changes)
./run.sh -- --BNetServer=127.0.0.1:1119 --lobbyServer=127.0.0.1:3724
# or, with Proton / Wine debug logging (+seh, +debugstr: client assert texts) and the in-game console:
./build-and-run-with-server.fish
```

With `--web-auto-login` (the default) the login server completes the logon as `--web-default-account` straight away,
so the client goes from the splash screen to the title screen without a login form. To send it into a match,
press PLAY, PRACTICE VS AI, PRIVATE GAME or TUTORIAL, or from a shell:

```sh
curl -X POST --data 'command("instance")' http://127.0.0.1:3726/eval            # Temple of Anubis
curl -X POST --data 'command("instance Dorado")' http://127.0.0.1:3726/eval     # another map (!maps lists them)
```

`../systems/ecs/tools/live.py` automates the loop (`launch`, `instance`, `setup`, `stop`, `mcp <tool> <json>`).

**Other machines**: the defaults point the client at `127.0.0.1` everywhere. For a client on another host you would
change the login server's `--lobby-host` (the referral) and `--web-url`, and each game server's `--advertise`
(the address put in 20600). Only localhost has been tested.

---

## 6. The binaries in depth

There are six commands under `cmd/`. Flags are shown as the flag name; the matching environment variable is the
prefix + the upper-cased name (`--lead-target` → `INSTANCE_LEAD_TARGET`).

### 6.1 `loginserver`: Battle.net (BGS) emulation

Emulates the Battle.net services the 0.8 client uses to log in, over TLS on `:1119`. Packages: `internal/bgs`
(transport), `internal/login` (services), `internal/session` (lobby keys), `gen/` (protobuf types).

The login, step by step (`internal/login/service.go`):

1. `ConnectionService.Connect`
2. `AuthenticationService.Logon` (web client verification)
3. server → `ChallengeListener.OnExternalChallenge`, payload = the web login URL
4. the client GETs the URL and draws a login form from the JSON (`web.go`)
5. the client POSTs the credentials and gets a login ticket
6. `AuthenticationService.VerifyWebCredentials(ticket)`
7. server → `AuthenticationListener.OnLogonComplete`
8. `GameUtilitiesService.ProcessClientRequest` → the **lobby referral**: `hostv4`, a client ID `cid` and four
   64-byte keys `k0`–`k3`, derived from (`--lobby-session-secret`, `cid`) so the lobby can recompute them without
   shared state

Any account name and password are accepted. With `--web-auto-login` (default) steps 3–6 are skipped. Calls without
a handler are decoded, logged and left unanswered. A self-signed certificate is generated at start unless
`--tls-cert-file` / `--tls-key-file` are given, and TLS 1.0 is allowed, since the client's SChannel is from 2015.

| flag | default | what |
|---|---|---|
| `--addr` | `0.0.0.0:1119` | BGS listen address |
| `--tls-disable` | false | plain TCP instead of TLS (the 0.8 client always uses TLS) |
| `--tls-cert-file`, `--tls-key-file` | empty | PEM certificate + key; empty = self-signed |
| `--lobby-host` | `127.0.0.1:3724` | the lobby address in the referral |
| `--lobby-session-secret` | `owserver-dev-secret` (masked) | must match `lobbyserver --session-secret` |
| `--web-addr` | `0.0.0.0:8081` | web login listen address |
| `--web-url` | `http://127.0.0.1:8081/bnetserver/login/` | URL sent in the external challenge; must be reachable from the game |
| `--web-default-account` | `player@local` | account used by auto login and for empty form fields; empty = an account name is required |
| `--web-auto-login` | true | log in as the default account immediately, no form |
| `--log-keepalive` | false | log `ConnectionService.KeepAlive` calls (quiet by default) |
| `--debug` | false | debug logging |

Docs: [`../docs/early-notes/02-battlenet-login.md`](../docs/early-notes/02-battlenet-login.md).

### 6.2 `lobbyserver`: the lobby gateway

Owns the client's lobby connection and does only link-level work. It runs the pre-JAM handshake (`handshake.go`),
the JAM stream cipher (`jam.go`), framing and protocol negotiation (`jam_link.go`, `server.go`), and keepalives.
Every other message goes to the **hub** (`hub.go`). The hub records it in a 2000-entry traffic ring, runs any Lua
handlers registered for it, and forwards it to lobbylogic over the backend port, unless a Lua handler consumed it.
With no lobbylogic connected, messages are logged and dropped and the link stays up.

| flag | default | what |
|---|---|---|
| `--addr` | `0.0.0.0:3724` | lobby listen address |
| `--session-secret` | `owserver-dev-secret` (masked) | must match `loginserver --lobby-session-secret` |
| `--client-id-first` | true | client JamID before server JamID in the handshake trailer (the confirmed order) |
| `--backend-addr` | `127.0.0.1:3725` | where lobbylogic connects |
| `--control-addr` | `127.0.0.1:3726` | control API (must be loopback) |
| `--lua-init` | `lua/init.lua` | Lua run when the state is created or reset |
| `--quiet-msgs` | `32002` | message IDs logged only as a 10 s summary instead of one hex line each (high-rate streams) |
| `--protocol-dump` | `../game/owbetaclient/Prometheus/jam_protocols.json` | Prometheus' JAM protocol dump, for the field tables in `/protocols/{name}`; optional (a warning if missing) |
| `--log-keepalive` | false | log the client's keepalives |
| `--debug` | false | debug logging |

**Control API** (`127.0.0.1:3726`, `GET /` prints the help):

| call | what |
|---|---|
| `GET /sessions` | connected clients (id, cid, JamIDs, channels) |
| `GET /protocols` | all JAM protocols (name, CRC, message ID range) |
| `GET /protocols/{name}` | one protocol with its messages' field tables (from the dump) |
| `GET /traffic?since=SEQ&n=N&session=S` | recent messages, payload hex; poll with `since` = the last seq |
| `POST /send {"session","protocol","msg","hex"}` | send a message to the client; empty protocol = looked up by message ID; session 0 = newest |
| `POST /eval` (Lua source) | run Lua in the persistent state (§4) |
| `POST /lua/reset` | a fresh Lua state, `--lua-init` re-run |

```sh
curl -s 'http://127.0.0.1:3726/traffic?n=20'
curl -s -X POST http://127.0.0.1:3726/send -d '{"msg": 20501, "hex": ""}'
```

**Gateway ↔ lobbylogic** (`internal/lobby/wire.go`): one JSON object per line. The gateway sends `hello {sessions}`
(once, on connect), `open`, `recv {session, protocol, msg, payload}` and `close`. lobbylogic sends
`send {session, protocol, msg, payload}` and `disconnect`. A new backend connection replaces the old one.

### 6.3 `lobbylogic`: the lobby's game logic

Everything the lobby *does*: the title screen (connect sequence, identity, featured hero, cosmetics, progression,
store), the social panel and party, matchmaking (PLAY → Searching → Match Found), practice and private games, the
tutorial, notices, play-time warnings, chat and chat commands, the custom `PrometheusLobby` channel, and the game
server link (assign / release connections, availability, the live server view relay). Handlers are registered by
message ID in `internal/lobbylogic/handlers.go`. It connects to the gateway (retrying every second), so it can be
rebuilt and restarted at any time without the client noticing.

| flag | default | what |
|---|---|---|
| `--gateway-addr` | `127.0.0.1:3725` | the gateway's backend address |
| `--instance-addr` | `127.0.0.1:3727` | where the game servers connect (`--logic-addr` on their side) |
| `--game-server` | `instance` | where new matches go when no choice is saved: `instance` or `gameserver` |
| `--game-server-file` | `game-server.json` | where the choice is saved; a saved choice overrides `--game-server` |
| `--offline-after` | `2s` | how long the active game server may be unlinked before the match buttons grey out (debounces rebuilds) |
| `--name` | empty | player name (BattleTag); overrides `lobby-identity.json` |
| `--icon` | empty | player icon (name like `Mei`, or a GUID); overrides `lobby-identity.json` |
| `--debug` | false | debug logging |

When a match starts (PLAY's match found, START in a practice or private game, TUTORIAL, or `!instance`),
lobbylogic picks a connection ID, a random 20-byte key and a game ID, sends `assign` to the active game server, and
sends the client **20600** with the game server's advertised address. lobbylogic refuses to assign a map that the
active server doesn't list in its hello. Docs: [`../docs/LobbyServer.md`](../docs/LobbyServer.md),
[`../docs/TitleScreen.md`](../docs/TitleScreen.md) (the lobby message catalogue),
[`../docs/LobbyScripts.md`](../docs/LobbyScripts.md) (which screen each message reaches).

### 6.4 `gameserver`: the R&D game server

The first game server, kept as the **reverse-engineering harness**: a UDP server (`internal/instance`) with
hand-written encoders for entity creates, mirrored component data, movement snapshots, Statescript full updates
and the hero start-up, all driven by hand through its control API. It follows the client's map loaded (21602) and
lock-in (21603) messages too, but without a real tick loop or clock steering. Its encoders were moved behind clean
interfaces into `inst/replication` and produce byte-for-byte the same output there. Plain Go, no C++ libraries.

| flag | default | what |
|---|---|---|
| `--addr` | `0.0.0.0:26500` | UDP listen address |
| `--advertise` | `127.0.0.1:26500` | address put in 20600 |
| `--logic-addr` | `127.0.0.1:3727` | lobbylogic's game server port |
| `--control-addr` | `127.0.0.1:3728` | control API (must be loopback) |
| `--debug` | false | log every packet |

**Control API** (`127.0.0.1:3728`, see the comment in `internal/instance/control.go` for every body field):

| call | what |
|---|---|
| `GET /conns` | assigned connections (the newest `conn_id` is the current client) |
| `GET /worlds` | the matches' worlds and their connections |
| `GET /traffic?n=50` | recent datagrams, both directions, hex |
| `GET /jamlog?conn=N&ping=0` | JAM messages received (`ping=0` leaves out the 21608 pings) |
| `POST /setup?conn=N` | join now: the local controller + the match's world (normally on 21602) |
| `POST /send {"conn","data","raw"}` | send a datagram: header + seq + body, MAC prepended (`raw` = as is) |
| `POST /jam {"conn","msg","payload","unreliable"}` | one JAM message on the JAM stream |
| `POST /spawn {"conn","id","def","pos","rot","scale","local","flags","map_index","mover"}` | create entity `0x40000000\|id` from definition `0x04000000_00000000\|def` (or the map's placed entity `map_index`) |
| `POST /scripts {…}` | Statescript: resend the hero start-up, a remote full update of chosen graphs on any entity, or a ready-made payload |
| `POST /filter {"conn","id","bits"}` | a component 0x10 (filter bits / team) update |

```sh
curl -s http://127.0.0.1:3728/conns
curl -s -X POST 'http://127.0.0.1:3728/setup?conn=1'
curl -s -X POST http://127.0.0.1:3728/spawn -d '{"conn": 1, "id": 48, "def": 57}'          # 0x40000030 from def 0x…39
curl -s -X POST http://127.0.0.1:3728/scripts \
     -d '{"conn": 1, "entity": 48, "graphs": [{"index": 41, "sync_vars": 9, "remote_nodes": 2}]}'
```

Graph sync layouts (`sync_vars`, `remote_nodes`) come from the Prometheus MCP tool `graph_sync_info`.

**Never create an entity whose definition isn't loaded on the map**: the client asserts in its create handler and
dies. `instance` guards against this (§6.5), but the R&D server doesn't.

### 6.5 `instance`: the game instance server

The server meant to serve real matches. It is built from what the R&D server found, moved behind clean
interfaces, plus a real game loop:

- a **16 ms tick** (62.5 Hz, drift-free with bounded catch-up). One goroutine owns all match state and runs the
  events that arrived since the last tick in order (link, lobbylogic, control API), then ticks every match and
  sends each connected player one packet;
- **clock steering** of each client's command-frame clock with the stream-3 flags (§8.4);
- a **world** per match built from the map's data (networked placements, spawn points, teams), held in a
  server-side entity admin of the C++ ECS;
- **replication** per connection: creates, updates and destroys, mirrored component data, movement snapshots,
  Statescript sync, with resend of entries in lost packets, reliable JAM, and the **residency guard**, which never
  sends a create of a definition the client doesn't have loaded on that map;
- **server-side Statescript**: each locked-in hero's graphs run in `systems/statescript` in lockstep with the
  client's prediction, on the client's input, at the client's command frames. The resulting local deltas confirm
  (or correct) what the client predicted;
- a **rules host** (`inst/game`): the gamemode brain and server copies of the map's objective placements run their
  graphs on the server (assault on Temple of Anubis today);
- a **lobbylogic link** with hello, heartbeat and reconnect, and the **live server view** stream.

The match lifecycle (`inst/match`):

| step | trigger | the server |
|---|---|---|
| assign | lobbylogic's `assign` | creates the match for the map if it has none, with its world |
| connect | the client's SYN | SYN\|ACK; replication, the JAM channel and clock steering start; the first packet carries a resync |
| join | **21602** (map loaded) | the player's controller + **20301** naming it the local entity, then the world, one entity per packet |
| lock-in | **21603** {hero, skin} | the hero body at a start spawn point; the controller update (closes hero select); the hero's Statescript start-up |
| ping | **21608** | 20304 with the client's times and ours |
| leave | `release` (Leave Game), the client's FIN, or 10 s of silence | FIN; the player's entities are destroyed for the others; the match ends with its last player |

| flag | default | what |
|---|---|---|
| `--addr` | `0.0.0.0:26600` | UDP listen address |
| `--advertise` | `127.0.0.1:26600` | address put in 20600 |
| `--logic-addr` | `127.0.0.1:3727` | lobbylogic's game server port |
| `--control-addr` | `127.0.0.1:3729` | control API (must be loopback) |
| `--content` | `../game/owbetaclient` | the 0.8 install whose CASC data the Statescript graphs (and stack priorities) are read from |
| `--lead-target` | `4` | client lead to steer to, in frames (how early its input arrives) |
| `--lead-band` | `1` | dead band around the target, in frames |
| `--batch` | false | let updates / destroys / Statescript entries share a packet (a create still ends one); not yet run against the client |
| `--delta-every` | `1` | Statescript delta interval in ticks |
| `--setup` | `10` | setup phase length in seconds before the assault starts |
| `--no-game` | false | don't run the gamemode brain and objective graphs |
| `--shadow` | false | simulate without sending the deltas, keeping per-frame snapshots for the shadow compare (`GET /shadow`) |
| `--evidence FILE` | empty | shadow-compare evidence (from `cmd/shadowcmp`): states with mismatches are not corrected |
| `--no-sim` | false | **deprecated**: don't simulate hero Statescript, send the start-up only |
| `--sim` | false | **deprecated**: simulating is the default |
| `--debug` | false | log every JAM message |

The install is opened at start (under a second). Without it `instance` exits (`content (--content): …`), unless
`--no-sim` is given without `--shadow`.

**Control API** (`127.0.0.1:3729`; the full field reference is the comment at the top of `inst/control/control.go`).
Numbers in JSON bodies may be hex strings (`"hero": "0x02E0000000000029"`).

| call | what |
|---|---|
| `GET /conns` | players: lifecycle state, entities, the clock (frame P, input frame, lead, flags, resyncs), replication and JAM counters, refused creates, the link's view |
| `GET /matches[?full=1]` (alias `/worlds`) | matches: map, frame, players, world and ECS counts, per-hero simulation stats, the rules host (`game`); `full` = every entity |
| `GET /stats` | the game loop: ticks, late ticks, queued events; the live server view's subscriptions |
| `POST /clock {"Target", "Band", …}` | the clock steering policy for every player (zero fields = defaults) |
| `GET /traffic?n=50` | recent datagrams, both directions, hex |
| `GET /jamlog?conn=N&ping=0` | JAM messages received, decoded |
| `POST /setup?conn=N` | join now (as on 21602) |
| `POST /lockin {"conn", "hero"}` | lock in a hero (as on 21603) |
| `POST /jam {"conn", "msg", "payload", "unreliable"}` | queue a raw JAM message |
| `POST /spawn {"conn", "id", "def", "pos", "rot", "scale", "flags", "map_index", "mover"}` | create `0x40000000\|id` on that client only (not in the world) |
| `POST /filter {"conn", "id", "bits"}` | a component 0x10 update |
| `POST /scripts {"conn", "entity", "graphs", "active"}` | a remote Statescript full update |
| `GET /simdeltas?body=` | the last 128 Statescript deltas encoded for a hero body: CF range, bits, hex, what changed |
| `POST /simtrace {"body", "on"}`, `GET /simtrace?body=` | switch on / drain the trace of a hero body's server-side Statescript (JSON lines) |
| `GET /shadow?body=&cf=`, `POST /shadow/reset?body=` | shadow mode: the server's snapshot of a hero at a command frame; re-seed from the start-up |
| `GET /debug/draw[?conn=N]` | the match as world-space debug shapes for Prometheus' Debug Draw window |
| `POST /game/pos {"body", "pos", "yaw"}` | where the server believes a hero is (the rules host's position source) |
| `POST /game/send {"conn", "target", "msg", "params"}` | a raw rules-layer game message |

```sh
curl -s http://127.0.0.1:3729/conns
curl -s 'http://127.0.0.1:3729/matches' | head -50
curl -s http://127.0.0.1:3729/stats
curl -s -X POST http://127.0.0.1:3729/lockin -d '{"conn": 1, "hero": "0x02E0000000000029"}'
curl -s -X POST http://127.0.0.1:3729/clock  -d '{"Target": 6, "Band": 1}'
curl -s 'http://127.0.0.1:3729/simdeltas?body=0x40000018'
```

**Helpers around `instance`**:

- `scripts/posbridge.py [--hz 10] [--control …] [--prometheus …]`: test scaffolding until the server models
  movement. It reads the local hero's position from the client through Prometheus (MCP `read_component` 0x01) and
  posts it to `POST /game/pos` about 10 times a second, so a capture point can see you standing on it.
- `cmd/shadowcmp`: the **shadow compare**. With `instance --shadow` and a hero locked in, it presses buttons
  through Prometheus' `logical_button`, captures the client's own Statescript state (`ss_serialize_local`) and
  compares it state by state and var by var with the server's snapshot of the same command frame. It writes
  per-state evidence (`-evidence`) that `instance --evidence` uses to stop correcting states that don't match yet,
  and can act as a gate (`-gate`). It reads `../systems/data/heroes/heroes.json` and
  `../systems/data/statescript/state_data_formats.json`, which are generated locally (§9) and not distributed.

Docs: [`../docs/InstanceServer.md`](../docs/InstanceServer.md) is the full reference (packages, lifecycle,
replication, clock, game loop, Statescript on the server, conformance, the rules host, the residency guard, the
lobbylogic link).

### 6.6 `shadowcmp`

See above. Flags: `-mcp` (Prometheus MCP endpoint, default `http://127.0.0.1:17888/mcp/tools/call`), `-control`
(default `http://127.0.0.1:3729`), `-entity`, `-actions` (default `fire,reload,ability_1,ability_2,jump`), `-hold`,
`-settle`, `-window`, `-repeat`, `-root` (default `..`), `-out`, `-evidence`, `-gate`. Background:
[`../docs/StatescriptSync.md`](../docs/StatescriptSync.md) §7.

---

## 7. Package map

### `internal/`: login, lobby, the R&D server, shared tables

| package | what |
|---|---|
| `internal/bgs` | the Battle.net RPC transport: frame header + protobuf, service lookup by wire hash or proto-name hash, method ID tables, dispatch to handlers, client listener calls, self-signed TLS certificate, log formatting of requests |
| `internal/login` | the login services: `Connect`, `KeepAlive`, `Logon`, `VerifyWebCredentials`, `ProcessClientRequest` (the lobby referral), the web login form / ticket endpoint |
| `internal/session` | the lobby session keys shared by login and lobby: `k0`–`k3` derived from (secret, `cid`), so neither server needs state from the other |
| `internal/lobby` | the gateway: the pre-JAM handshake, the JAM stream cipher, lobby framing, protocol negotiation (generated protocol table `protocols_gen.go` + custom protocols), the hub (routing, Lua handlers, traffic ring, quiet list), the backend link, the Lua VM, the control API |
| `internal/lobbylogic` | the lobby's behaviour: handlers by message ID, chat and chat commands, identity, title hero and cosmetics (`herounlocks_gen.go`), progression, store, social / friends / party, matchmaking, practice / private games, notices, play-time, game server link and choice (`instances.go`, `serverchoice.go`), availability, the `PrometheusLobby` channel and the server view relay |
| `internal/gamelink` | the lobbylogic ↔ game server wire types and the game server side's dialing / heartbeat / reconnect client (`gamelink.Client`). Its own package so game server edits don't rebuild lobbylogic |
| `internal/instance` | the **R&D game server**: UDP packets (HMAC-MD5 MAC), connections, the instance JAM stream, client input, the world, Statescript sync and hero start-up encoders, the lobbylogic link, the control API |
| `internal/heroes` | every hero's Statescript start-up (graphs, schema defaults, definition overrides, weapon scripts, start states), generated from content (`heroes_gen.go`), plus a small expression evaluator |
| `internal/maps` | the map catalogue (`maps_gen.go`): names, kinds, modes, networked placements, movers, objectives, brain definition, the per-map resident definition set (`Resident`) |
| `internal/logging` | the compact coloured `slog` handler used by every binary |

### `inst/`: the instance server

| package | what |
|---|---|
| `inst/bits` | the client's bit stream: bits LSB first within a byte, byte fields aligned; `Writer` / `Reader` |
| `inst/jam` | JAM messages: typed codecs generated from the catalogue (`messages_gen.go`), the binary codec, the protocol / channel tables, a typed dispatcher, lobby framing (`lobby.go`) and stream 0 of the instance payload (`stream.go`) |
| `inst/wire` | a data packet's payload layout in both directions: frame P, JAM, input ack + clock flags, the entity section (server → client); JAM and the input history (client → server) |
| `inst/link` | the UDP transport: 34-byte header + HMAC-MD5, the SYN / SYN\|ACK / ACK / FIN handshake, ack windows, loss detection, keepalive, timeouts; `Channel` = reliable JAM (numbering, resend, in-order delivery) |
| `inst/clock` | the 16 ms `Ticker` and the per-client clock `Steer`ing policy |
| `inst/world` | a match's world: the map's networked placements with teams, spawn points, players' controllers and bodies, held in a server-side entity admin of `systems/ecs` (owecs) |
| `inst/replication` | entity creates / updates / destroys, mirrored component data, movement snapshots, Statescript sync (full updates, local and remote deltas), the per-connection `Replicator` (what the client has, queue, in flight, resends), the residency guard, the serializer boundary to the C++ libraries |
| `inst/sim` | the Statescript host: `Engine` (graphs from CASC via owcontent, one per process), `Runtime` (one sync manager per hero body in `systems/statescript`, stepped on the client's input), the correction policy, shadow snapshots |
| `inst/game` | the rules host: the gamemode brain and server copies of objective placements running in a Statescript match world; phases, positions in, effects (teams, capture state) out, and their replication |
| `inst/match` | the match lifecycle and players, per-tick packet assembly, debug draw shapes, the live server view frames |
| `inst/lobbylink` | the instance server's end of the lobbylogic link (hello as `instance`, assign, release, view, heartbeat, reconnect) |
| `inst/server` | the game loop that ties the link, the lobbylogic link, the matches and the control API together |
| `inst/control` | the HTTP control API |

### Everything else

| path | what |
|---|---|
| `cmd/` | one `main.go` per binary: configuration (`ardanlabs/conf`), wiring, signals |
| `jamgen/` | the JAM catalogue tool and code generator (`seed`, `check`, `gen`), §9 |
| `proto/` | the Battle.net `.proto` files the login server uses, one directory per package (`bgs/protocol/...`) |
| `gen/` | Go generated from `proto/` by buf (`owserver/gen/...`) |
| `buf.yaml`, `buf.gen.yaml` | buf module (`proto/`, MINIMAL lint) and generation (managed `go_package_prefix owserver/gen`, local `go tool protoc-gen-go`) |
| `lua/init.lua` | Lua helpers loaded into the gateway |
| `scripts/watch.sh` | rebuild / restart supervisor (§4); `watch-logic.sh` = `watch.sh lobbylogic` |
| `scripts/lua.sh` | run Lua in the gateway (`LOBBY_CONTROL` overrides the address) |
| `scripts/posbridge.py` | the position bridge (§6.5) |
| `*.args` | extra flags per watched binary (§4) |

Runtime files written into `server/` (not checked in): `bin/`, `game-server.json`, `lobby-identity.json`.

---

## 8. Protocols

Short summaries. The documents linked from each one carry the full detail.

### 8.1 Battle.net RPC over TLS

The client speaks Battle.net's "bgs" RPC on `:1119` over TLS: a framed header + protobuf body, services identified
by hash, methods by ID, with responses on service `0xFE`. The `.proto` definitions were reconstructed from the 0.8
client. The full extracted set is in [`../protos/`](../protos/), and `proto/` holds the packages the login server
uses (account, attribute, authentication, challenge, channel, config, connection, content handle, entity,
game_utilities, invitation, presence, rpc types and the option protos), laid out one directory per package for
buf. Docs: [`../docs/early-notes/02-battlenet-login.md`](../docs/early-notes/02-battlenet-login.md).

### 8.2 The lobby link: handshake, stream cipher, JAM

After the referral the client opens TCP to `hostv4` and runs a fixed negotiation before any JAM traffic
(`internal/lobby/handshake.go`):

```
C→S  "HELLO PRO CLIENT\0"
S→C  "HELLO PRO SERVER\0"
C→S  [u64 cid][32-byte client nonce cn]
S→C  [32-byte challenge][u8 difficulty][32-byte server nonce sn]   (difficulty 0: no proof of work)
C→S  [u64 proof-of-work counter][MAC1 = HMAC-SHA256(k0, cn‖sn)]
S→C  MAC2 = HMAC-SHA256(k1, sn‖cn)
S→C  encrypted 292-byte state message: blob[256] (address, port, HMAC) ‖ client JamID ‖ server JamID (type 13) ‖ u32
```

From then on both directions are encrypted with a continuous 32-byte-key stream cipher, keyed with
`HMAC-SHA256(k2, cn‖sn)` (server → client) and `HMAC-SHA256(k3, sn‖cn)` (client → server). The cipher is
implemented in `internal/lobby/jam.go` and has a known-answer test. A real server's blob is RSA-signed with a
private key nobody outside Blizzard has; **Prometheus hooks the client's RSA step** so that the blob only has to
pass its HMAC. That makes Prometheus a requirement for logging the stock client into this lobby.

Inside the cipher the stream carries **JAM frames** `[u24 BE length][u8 channel][u8 message index][payload]`.
Channel 0 is the negotiation protocol. The client offers the CRCs of the protocols registered for server type 13
(lobby), sorted by CRC, and the server accepts, which maps channels 1..n. A JAM payload is a bit stream in which
every non-bool field starts on a byte. Docs: [`../docs/early-notes/03-lobby-handshake.md`](../docs/early-notes/03-lobby-handshake.md),
[`../docs/JAM.md`](../docs/JAM.md) (JAM from Blizzard's GDC talk, then §12 "Our implementation"),
[`../docs/TitleScreen.md`](../docs/TitleScreen.md) §5 (every `ClientInLobby*` message and its handler).

### 8.3 The JAM message catalogue and `jamgen`

Every JAM protocol the client registers (46 protocols, about 350 messages, lobby and instance) is described in one
catalogue, `../systems/data/jam/catalogue.json`. The layouts come from the client's own field tables (exported by
Prometheus' JAM protocol dump), and the names, handlers and notes were curated by hand. `jamgen gen` turns it into
`inst/jam/messages_gen.go`: one struct per message with `Encode(*bits.Writer)` / `Decode(*bits.Reader)`, ID
constants and the protocol table. The same codec reads a message in a lobby frame and inside the instance payload's
stream 0. Docs: [`../docs/JAM.md`](../docs/JAM.md) §12. Regeneration: §9.

### 8.4 The game link: UDP, command frames, the 62.5 Hz tick, clock steering

After 20600 the client connects over UDP with the connection ID and the 20-byte key from the message. Every
datagram starts with a 34-byte header: a 12-byte truncated HMAC-MD5, `connID<<4 | kind`, sequence number, ack
and a 64-bit ack mask, flags (SYN / ACK / FIN). The handshake works like TCP's (`inst/link`). A data packet's
payload is a bit stream of five streams. From the server it carries the frame number P, stream 0 = JAM messages
(reliable ones numbered and resent), stream 3 = input ack + **u4 clock flags**, then the entity section (per
entity: Statescript sync in stream 1, entity operations in stream 4). From the client it carries JAM and its input
history (`inst/wire`).

The game simulates in fixed **16 ms command frames** (62.5 Hz) and the client runs *ahead* of the server by its
lead, so its input for frame F arrives before the server simulates F. The server measures the lead (newest input
frame minus server frame on arrival, minimum over the last 16 input packets) and steers the client's clock with the
flags it keeps sending in every packet: `0x1` slow (−200 µs per render frame), `0x2` fast, `0x8` stronger
(±1000 µs), `0x4` resync. With the default target of 4 frames ±1, the measured lead holds steady at 3–4 frames on
localhost. Docs: [`../docs/CommandFrames.md`](../docs/CommandFrames.md), [`../docs/InstanceServer.md`](../docs/InstanceServer.md) §5.

### 8.5 Replication and Statescript sync

The client mirrors server entities through stream 4 operations: a create (from an entity definition, or "from map"
by placed index, carrying mirrored component data), an update (component data by mask) and a destroy. A create
must end its packet. Component data that uses the client's memory layout goes through a serializer boundary
(`inst/replication/serializer.go`). Hand-written "stopgap" encoders implement it today, and the C++ libraries are
meant to replace them. **The client asserts when it is told to create an entity whose definition is not loaded on
the current map**, so every create passes the residency guard (`resident(map) = global ∪ definitions of the map's
package`, generated into `internal/maps`).

Statescript, the game's gameplay scripting, is synchronised in stream 1. Full updates create a hero's instances
with their variables and active states. For the controlled hero, the client **predicts** its own graphs and records
an undo list. The server runs the same instances on the same input at the same frames and sends **local deltas with
the rollback bit**: the client rolls back to the server's state and replays its newer input. When the two agree
nothing is visible, and when they don't the server wins. If no confirming delta arrives within 40 client frames,
the client undoes its own predictions. Other entities (the brain, objective copies) use the remote format.
Docs: [`../docs/StatescriptSync.md`](../docs/StatescriptSync.md) (the wire grammar and the prediction model),
[`../docs/MatchLogic.md`](../docs/MatchLogic.md) (§8–§15: movers, remote states, the objective model, hero
start-up, the join / lock-in sequence), [`../docs/InstanceServer.md`](../docs/InstanceServer.md) §4, §9–§12.

### 8.6 The game server link (lobbylogic ↔ game servers)

TCP to lobbylogic's `:3727`, one JSON object per line (`internal/gamelink`):

| direction | message | fields |
|---|---|---|
| game → logic | `hello` | `addr` (UDP address for 20600), `name` (`gameserver` when empty, `instance`), `conns` (its assignments), `ping` (interval in ms), `modes`, `maps` (empty = all), `view` |
| game → logic | `ping` | every 2 s |
| logic → game | `pong` | the answer to each ping |
| logic → game | `assign` / `release` | `conn` |
| logic → game | `view` | `conn`, on / off: the live server view of a connection |
| game → logic | `frame` | one server view frame per tick |

Either side can restart. lobbylogic replays its assignments when a game server says hello, and the hello carries
the game server's assignments so a restarted lobbylogic learns them back. A server that announced pings is dropped
after 6 s of silence. Docs: [`../docs/InstanceServer.md`](../docs/InstanceServer.md) "Lobbylogic link".

### 8.7 `PrometheusLobby`: our own JAM protocol, and the live server view

Prometheus registers an extra lobby JAM protocol inside the stock client: **`PrometheusLobby`**, CRC
`0xF4456D46`, message IDs 32000–32015. Its CRC sorts after every stock protocol, so it becomes channel 33 and
leaves channels 1..32 unchanged. The gateway knows it from `internal/lobby/custom_protocols.go`. It carries:

- **32000 `PrometheusCall`** (server → client) `{u32 seq, str tool, str args}`: run a Prometheus tool (any MCP
  tool, plus channel-only ones like `notify`). Write-type tools stay behind Prometheus' own write toggle.
- **32001 `PrometheusResult`** (client → server) `{u32 seq, bool ok, str result}`.
- **32002 `PrometheusServerView`** (server → client) `{u32 seq, u32 frame, u64 sent_us, u64 relay_us, str body}`:
  the instance server's view of the match, every tick, relayed by lobbylogic (`!serverview on`). lobbylogic first
  checks that the DLL knows the message, because an unknown message ID makes the client drop the lobby link.

A client without the DLL simply doesn't offer the CRC. Docs: [`../docs/JAM.md`](../docs/JAM.md) §13,
[`../docs/DebugDrawing.md`](../docs/DebugDrawing.md) §5.

---

## 9. Generated code

| file | generated from | by | how to regenerate |
|---|---|---|---|
| `gen/bgs/protocol/**` | `proto/**.proto` | buf + `protoc-gen-go` | `make generate` (needs `buf`) |
| `inst/jam/messages_gen.go` | `../systems/data/jam/catalogue.json` | `jamgen gen` | `go generate ./inst/jam` |
| `internal/heroes/heroes_gen.go` | the 0.8 install (STUHero, gameplay entity definitions, graphs) | `../systems/data/heroes/tools/herogen` | `cd ../systems/data/heroes/tools/herogen && go run .` |
| `internal/maps/maps_gen.go` | the 0.8 install (map headers, map data, definitions) + measured resident sets | `../systems/data/maps/tools/mapgen` | `cd ../systems/data/maps/tools/mapgen && go run .` |
| `internal/lobbylogic/herounlocks_gen.go` | the 0.8 install (unlocks, hero progression, skin themes) | `../systems/data/lobby_scripts/tools/hero_unlocks.py` | `python3 ../systems/data/lobby_scripts/tools/hero_unlocks.py` |
| `internal/lobby/protocols_gen.go` | Prometheus' `jam_protocols.json` | (generator not in this directory) | edit by hand if needed; the table is small |

All of the generated Go is checked in, so **building and running needs none of the inputs**. Regenerating does:

- The content generators read a local 0.8 install at **`../game/owbetaclient`** (`-install DIR` overrides it for
  `herogen` / `mapgen`) through `systems/content` and `systems/stu`, so build those libraries first (§2). They
  also write a readable JSON next to themselves (`systems/data/heroes/heroes.json`, `systems/data/maps/maps.json`,
  TSVs for the unlocks). Other inputs they read, the English string table
  (`systems/data/lobby_scripts/strings_enUS.tsv`) and, for `mapgen`, the measured resident definition sets
  (`systems/data/maps/resident/*.json`, captured live with `systems/data/maps/tools/resident.py`), are **dumped
  data and not distributed**. You produce them from your own install and client.
- The JAM catalogue is not distributed either. `go run ./jamgen seed -dump <jam_protocols.json> -catalogue <file>`
  rebuilds it from a Prometheus protocol dump (by default `../game/owbetaclient/Prometheus/jam_protocols.json`)
  and keeps every curated attribute wherever the layout still matches. A fresh seed has no curated names, so the
  generated Go names would differ from the checked-in ones. `go run ./jamgen check` validates a catalogue.
- `jamgen` usage:

  ```
  jamgen seed  [-dump jam_protocols.json] [-catalogue catalogue.json]   (re)build the catalogue from the client dump
  jamgen check [-catalogue catalogue.json]                              validate the catalogue
  jamgen gen   [-catalogue catalogue.json] [-out messages_gen.go]       generate the codecs
  ```

---

## 10. Testing

```sh
make test          # make libs, then go test ./...
make vet           # make libs, then go vet ./...
go test ./internal/... ./inst/jam ./inst/bits ./inst/wire ./inst/clock ./inst/link   # the pure-Go packages, no C++ needed
go test -race ./internal/...
go test -run TestAnubisAssault -v ./inst/game
```

What the tests cover, roughly:

| area | tests |
|---|---|
| `internal/bgs`, `internal/login` | service hashes, frame round trips, dispatch, method IDs; a fake client runs the full web login (Connect → Logon → challenge → form → ticket → `OnLogonComplete` → referral with keys matching the derivation) |
| `internal/lobby` | cipher known-answer test and chunking, the handshake as the 0.8 client does it, wrong secret rejected, JamID type bits, negotiation; the gateway (Lua handlers, pack / unpack, no `os`/`io` in Lua, the backend round trip) |
| `internal/lobbylogic` | identity, cosmetics, party, social, matchmaking, custom games, notices, play-time, `!msg`, game server choice, availability, instances, the server view |
| `internal/instance` | the R&D encoders, the hero start-up, the world |
| `internal/heroes`, `internal/maps` | the generated tables, residency (the global set, the tutorial crash case, every map's brain / placements resident) |
| `inst/jam` | every catalogued message round-trips with random values (alone and packed at unaligned starts); every lobby payload in `testdata/lobby_traffic.txt` decodes and re-encodes to the same bytes; every client message in `testdata/instance_traffic.txt` decodes |
| `inst/bits`, `inst/wire`, `inst/link`, `inst/clock` | bit I/O, payload layouts against captured client packets, the UDP handshake / data / FIN over a real socket with loss, resend and ordering, the steering policy and tick timing |
| `inst/replication`, `inst/world`, `inst/match` | the same bytes as the R&D encoders for every entity kind and every hero's start-up, packing, the residency guard, deltas, every catalogue map building a world, a whole lifecycle with a fake link |
| `inst/sim`, `inst/game` | the Statescript host (seeding, input frames, the delta chain), the assault rules host on Anubis (setup → A captured at 30 s → B → ended), replication of the host's entities |
| `inst/server` | end to end: the whole server against a fake lobbylogic and a fake client |

What tests need that isn't in this directory:

- **The C++ libraries**: every package that imports a cgo wrapper (`inst/world`, `inst/replication` and up,
  `inst/sim`, `inst/game`, `inst/match`, `inst/server`, `inst/control`, `cmd/instance`) needs `../systems/*/build`,
  including `systems/stu` (§2).
- **The 0.8 install**: the `inst/sim` and `inst/game` tests open `../game/owbetaclient` and **skip** without it
  (`no 0.8 install`).
- Everything else uses checked-in data (generated tables, `inst/jam/testdata/`).

The C++ libraries have their own tests (`ctest --test-dir ../systems/<lib>/build`), including replays of
recordings of the real client. See [`../systems/README.md`](../systems/README.md).

---

## 11. Debugging and logs

**Logs.** Every binary logs to stderr through `internal/logging`: one compact line per event,
`15:04:05 INFO  [LOGIN]: client connected remote=…`, with a coloured tag per process (`LOGIN`, `LOBBY`, `LOGIC`,
`GAME`, `INSTANCE`). Colours are used on a terminal unless `NO_COLOR` is set. At start each binary logs its
whole configuration (secrets masked). `--debug` raises the level: every packet (`gameserver`), every JAM message
(`instance`), the TLS client hello (`loginserver`). `scripts/watch.sh` adds `watch: …` lines when it rebuilds,
restarts or keeps an old binary after a failed build.

**The gateway logs every lobby message** it forwards (`recv` / `send` / `inject`, payload as hex). That is the
quickest way to see what the client sent and what we answered. High-rate message IDs in `--quiet-msgs` get one
summary line per 10 s instead.

**Inspecting live state**:

```sh
curl -s 'http://127.0.0.1:3726/traffic?n=30'            # lobby messages, both directions
curl -s  http://127.0.0.1:3729/conns                    # instance: clock, lead, flags, replication, refused creates
curl -s 'http://127.0.0.1:3729/jamlog?conn=1&ping=0'    # instance: JAM messages from the client, decoded
curl -s 'http://127.0.0.1:3729/traffic?n=20'            # instance: raw datagrams
curl -s  http://127.0.0.1:3729/stats                    # instance: late ticks (the loop fell behind)
```

**Tips**:

- **A hard client crash with no Prometheus log** usually means a client assert. Run the client with
  `PROTON_LOG=1 WINEDEBUG=+seh,+debugstr` (`build-and-run-with-server.fish` does) and look for
  `EXCEPTION_BREAKPOINT` and the `:debugstr:` assert text in the Proton log (`~/steam-<appid>.log`, e.g.
  `steam-default.log`). Example: creating an entity whose definition isn't resident on the map
  ([`../docs/InstanceServer.md`](../docs/InstanceServer.md) §12.14).
- **The client waits forever behind a misparsed Statescript packet**. If a delta's bit length is off, the client
  stops applying everything after it and keeps its own prediction (a hero who keeps firing). `GET /simdeltas`
  shows the last 128 deltas; the shadow compare (`--shadow` + `cmd/shadowcmp`) finds states whose data format
  doesn't match the client's.
- **Match buttons greyed out on the title screen** = the active game server isn't linked to lobbylogic.
  `!gameserver` shows which one is active and whether it is connected.
- **After a game server rebuild the match is gone**: expected. The client gets a FIN and returns to the lobby.
  Start a new match.
- **`!msg` and `/send` can drop the lobby link**: a malformed payload makes the client close the connection. The
  client then asks Battle.net for a new referral (about every 5 s).
- **A changed C++ library isn't picked up**: run `make libs`. The watcher relinks on archive changes, but only for
  the archives in `../systems/*/build`.

---

## 12. Status: what works and what doesn't

Measured against the unmodified client on localhost. Details and dates are in the linked docs.

**Works**

- **Login and lobby**: login through our BGS server (auto login), the title screen with identity (name, icon),
  featured hero and cosmetics, progression, notices, chat and commands, friends and party, the social panel's game
  server choice. Every map is listed in the Practice / Private Game pickers. PLAY (matchmaking), PRACTICE VS AI,
  PRIVATE GAME and TUTORIAL all lead into a match on our game server, and LEAVE GAME brings you back.
- **The instance server**: the 62.5 Hz tick with consecutive frames and no late ticks; clock steering holding a
  3–4 frame lead; the world built at match init (e.g. 22 placements + team updates on Anubis, 40 on Dorado); hero
  select and lock-in for every hero, with the start-up generated from content (all 21 heroes); HUD, ability icons,
  weapon; clean leave and clean recovery from a server restart.
- **Server-side Statescript**: hero graphs run on the server in lockstep with the client. Genji's primary fire,
  ammo and auto-reload, and ability cooldowns are confirmed by server deltas and nothing is undone. States the
  shadow compare hasn't verified are left uncorrected instead of guessed.
- **Rules on the server (Temple of Anubis, assault)**: the gamemode brain and server copies of the capture points
  run on the server. The HUD shows the two objective icons, "A ATTACK" appears after the setup phase, standing on A
  (with the position bridge) fills the progress arc, A is captured, B enables and can be captured.
- **No-crash guarantees**: the residency guard keeps non-loaded definitions from the client (the tutorial loads
  without the assault host).

**Doesn't work yet / known limitations**

- **Movement is client-side only**: the server has no movement model and doesn't know where heroes are. The rules
  host's positions come from `scripts/posbridge.py`, which reads them out of the client through Prometheus. That is
  test scaffolding, and the unmodified client alone does not capture. Double jump and wall climb break under
  server deltas because they depend on vars the client's movement code writes.
- **Combat**: no health, damage, projectiles or hit registration on the server. Some abilities (e.g. Genji's
  secondary fire) are not right yet. Server-side coverage grows with the Statescript runtime's node types.
- **Match flow**: the top-centre objective icons never unlock, HUD / announcement texts are missing, there is no
  ring on the ground at a capture point, and nothing happens after the last point (no end of match, no end
  screen). Only assault on Anubis has a rules host; Hanamura, Volskaya, Dorado (escort, payload), King's Row and the
  tutorial's steps have none yet.
- **Multiplayer**: several players are structurally supported, but only one has been tested. Other players'
  controllers aren't replicated to each other.
- **Replication** is still a set of hand-written encoders. General mirror data through STU reflection, acknowledged
  delta state instead of resending lost entries, remote actions / events and string vars are missing. `--batch` has
  not been run against the client.
- **The tutorial** shows hero select (the retail tutorial forced a hero). Leave Game sometimes needs two presses (a
  client-side state issue).
- **The client's own limits**: many features need a live service that never existed for this build, and unloading
  a map crashes the client (restart it instead).
- **Platform**: developed and tested on Linux (the client under Proton). The servers themselves are plain Go plus
  CMake libraries, but the scripts assume bash and GNU tools.

The roadmap and the agreed order of work (Phase A: one player in a live world → B: rules → C: multiplayer →
D: physics fidelity) are in [`../systems/README.md`](../systems/README.md).

---

## 13. Open work and ground rules

The method used throughout: **find out what the client does, write it down, then build it**. Read the client
(Ghidra) and watch it live (Prometheus MCP), write the spec in `docs/`, implement it, and check the result against
the client: a recording, a byte-for-byte comparison, or what you see in game. Say which claims were measured and
which were inferred.

Where the open work is:

- **Read first**: [`../docs/InstanceServer.md`](../docs/InstanceServer.md), then
  [`../docs/StatescriptSync.md`](../docs/StatescriptSync.md) and [`../docs/MatchLogic.md`](../docs/MatchLogic.md).
  [`../systems/README.md`](../systems/README.md) has the roadmap.
- **Lobby features** (`internal/lobbylogic`) are the gentlest start. The message catalogue in
  [`../docs/TitleScreen.md`](../docs/TitleScreen.md) §5 lists messages nobody sends yet. A handler is a function
  registered by message ID, lobbylogic hot-reloads without dropping the client, and `/eval` lets you try a payload
  by hand before writing code.
- **The match flow on Anubis** (`inst/game`): the objective icons, HUD texts, the ground ring and the end of a
  match are open, with candidates written up in [`../docs/InstanceServer.md`](../docs/InstanceServer.md) §12.13.
- **Other modes**: rules hosts for Dorado (escort) and the tutorial. The models are in
  [`../docs/MatchLogic.md`](../docs/MatchLogic.md) §12–§13.
- **Movement** on the server: reverse-engineered in [`../docs/Movement.md`](../docs/Movement.md) (the mover
  formulas reproduce client recordings, and a server mover must be bit-exact). It would replace the position bridge
  and fix double jump / wall climb.
- **Replication**: replace the stopgap encoders behind `replication.Serializers` with C++-backed ones (STU
  reflection, the Statescript runtime's C API).
- **JAM**: name more messages and fields in the catalogue (`jamgen check`, `go generate ./inst/jam`), especially
  the in-instance protocols.
- **Tests**: anything you find in a live session can usually become a test with captured bytes (see
  `inst/jam/testdata/` and the replication tests).

Ground rules that keep the client alive:

- Never create an entity whose definition isn't loaded on the map. Every create goes through the `Replicator`.
- A Statescript packet with a wrong bit length wedges the client for the rest of the match. Only send state data
  whose format has been verified against the client.
- Keep the lobby group up while a client is connected. Put behaviour in lobbylogic, not the gateway.
- Control APIs are loopback-only by design. Don't expose them.
