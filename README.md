# Overwatch beta 0.8: client reverse engineering and a private server

A research project that takes the **Overwatch closed beta 0.8 client (build 24919, 2015)** apart and rebuilds
its systems in code we own, with one long-term goal: **a game server that can serve real matches to the
unmodified client.**

The client is the specification. Every system here was reverse-engineered from the client (static analysis in
Ghidra, and live inspection of a running client through the injected tool Prometheus), written down in `docs/`,
reimplemented, and checked against recordings of what the real client does.

> **Project status:** this is a snapshot of ongoing personal research. Further work and updates will come
> when, and if, I have time for it: I'm currently busy with other private projects.

> **Not affiliated with or endorsed by Blizzard Entertainment.** Overwatch is a trademark of Blizzard
> Entertainment, Inc. This repository contains no game client, no game data and no Blizzard assets. To use it
> you need your own copy of the 0.8 beta client. It targets a long-dead beta build with no live service and
> is meant for preservation, education and research.

---

## What's in here

| path | what |
|---|---|
| [`docs/`](docs/README.md) | **The reverse-engineering documentation**: about 30 documents covering ECS, command frames, STU, content archives, Statescript (data model, runtime, sync protocol, node and config-var catalogues), JAM messaging, lobby, title screen, UI system, maps, match logic, movement, physics, renderer, data flow, debug drawing, the instance server. Start at [`docs/README.md`](docs/README.md). |
| [`docs/gdc/`](docs/gdc/README.md) | The Blizzard GDC talks this work builds on (Statescript, JAM, ECS/netcode): links and our own summaries |
| [`docs/early-notes/`](docs/early-notes/README.md) | The first notes, trimmed to what the main docs don't cover: Battle.net login, the lobby handshake and cipher, static-analysis tips, early lobby and instance-link details |
| [`docs/PrometheusChanges.md`](docs/PrometheusChanges.md) | What this project changed and added in Prometheus |
| [`systems/`](systems/README.md) | **C++ reference implementations** of the client's systems, standalone (no Windows or game dependencies), each with a C API and a Go (cgo) wrapper: `ecs` (entity admins, components, systems, command frames), `stu` (the STU structured-data format and the client's type registry), `content` (offline CASC/TACT/APM reader), `statescript` (the gameplay scripting runtime), `maps` (map data parser/writer). The roadmap and fidelity rules are in [`systems/README.md`](systems/README.md). |
| [`server/`](server/README.md) | **The Go servers**: Battle.net-style login (TLS, BGS RPC), lobby gateway + lobby logic (JAM), the R&D game server used as an RE harness, and `instance`, the real game server (62.5 Hz tick, clock steering, replication, server-side Statescript, match host) |
| [`protos/`](protos/) | Battle.net (BGS) protobuf definitions extracted from the client |
| [`explorer/`](explorer/README.md) | **Storage explorer**, a desktop app (Wails + Nuxt/Vue) over the readers: browse every resource of an install by GUID, STU views, Statescript graphs, maps in 3D over the collision mesh, textures, models + materials, audio, a reference index and dependency graphs. It has an MCP server so an AI assistant can see what you're looking at. |
| [`prometheus/`](prometheus/README.md) | **Prometheus**, the injected DLL + launcher (ImGui overlay, entity/STU/Statescript inspectors), forked from upstream ([saturn-xvi/prometheus](https://github.com/saturn-xvi/prometheus), AGPL-3.0). This fork adds a Linux cross-build and an **MCP server** that exposes the live client to tools (memory readers, ECS dumps and traces, Statescript inspection and tracing, logical-button input, debug drawing, a custom JAM channel). See [`docs/PrometheusChanges.md`](docs/PrometheusChanges.md). |
| `prometheus-prerequisites/` | Toolchain environment for building Prometheus on Linux (llvm-mingw; `fetch-llvm-mingw.sh` downloads it) |
| `build.sh` / `run.sh` (+ `.fish`) | Build Prometheus; run it (and the game) under Proton through umu |
| `scripts/setup-submodules.sh` | Adds Prometheus' third-party submodules (vcpkg, imnodes) at the pinned commits |

## Where things stand

What works today, against the unmodified client:

- **Login and lobby**: the client logs into our login server, reaches the title screen with a hero and cosmetics,
  notices and player identity, and every map is listed in the Private Game / Practice pickers. PLAY, PRACTICE VS
  AI, PRIVATE GAME and TUTORIAL all lead into a match on our game server.
- **In a match** (`instance` server): the 62.5 Hz server tick with clock steering, the world built at match
  init, hero select and lock-in, hero start-up generated from content (all 21 heroes), and server-side Statescript
  simulation of hero graphs. Abilities are confirmed by the server, and the gamemode brain and objective graphs
  run on the server (capture on Temple of Anubis seen in game).
- **Libraries**: ECS, STU, content access and maps are done. The Statescript runtime replays the client's
  recordings exactly and models every node type on the server paths.
- **Reverse-engineered but not built**: movement (the mover formulas reproduce client recordings), physics (the
  "Domino" engine), the renderer, the UI system, data flow.
- **Missing**: combat and health on the server, server-authoritative movement, general replication (deltas,
  mirror data through STU reflection), multiplayer. The phases are in [`systems/README.md`](systems/README.md).

The client also has limits of its own: many features need a live service that never existed for this build,
and unloading a map crashes it (restart instead).

## Getting started

### 1. The client

Get the Overwatch beta 0.8 client (build 24919) and **verify that `GameClientApp.exe` carries a valid Blizzard
signature** before running anything. Put the install at `game/owbetaclient/`. That is the default path for the
explorer and the data generators, and `.gitignore` keeps it out of git.

### 2. Clone with submodules

```sh
git clone --recursive <this repo>
# or, in a fresh copy without submodules:
scripts/setup-submodules.sh
```

### 3. The C++ libraries

Needs CMake, Ninja and a C++20 compiler.

```sh
for m in ecs stu content statescript maps; do
  cmake -S systems/$m -B systems/$m/build -G Ninja && cmake --build systems/$m/build
done
ctest --test-dir systems/content/build        # unit tests (see "Tests" for the replay tests)
```

The Go wrappers (`systems/*/go`) link the static archives from those `build/` directories.

### 4. Build Prometheus

**Prometheus is required.** The unmodified client can't log into our servers by itself. It is started through
the Prometheus launcher, which injects `PrometheusCore.dll` into the client. Among other things, the DLL:

- **skips the client's TLS certificate check** for the Battle.net login connection, so the client accepts our
  login server's self-signed certificate (`Network/BnetTlsBypass.cpp`);
- **skips the RSA step on the lobby state blob**, which only Blizzard's real servers could produce
  (`Network/LobbyBlobBypass.cpp`);
- decrypts the client at start-up, gives you the overlay and the MCP server, and carries the custom JAM channel
  some server features use.

Without it the client stops at login. On Linux:

```sh
prometheus-prerequisites/fetch-llvm-mingw.sh     # once: the llvm-mingw toolchain
./build.sh                                       # Prometheus launcher + DLL (llvm-mingw, vcpkg); output in prometheus/output/
```

On Windows, build it with Visual Studio as described in [`prometheus/README.md`](prometheus/README.md).

## Running it

You need three things running, in this order: the **lobby group**, the **game servers**, then the **client
through Prometheus**. Each server group goes in its own terminal. Everything listens on `127.0.0.1`. The full
server documentation is in [`server/README.md`](server/README.md).

**Terminal 1: the lobby group** (needs Go, see `server/go.mod`):

```sh
cd server
make lobby
```

This builds and starts:
- the **login server** on `:1119` (TLS, Battle.net RPC; generates a self-signed certificate on first start);
- the **lobby gateway** on `:3724` (the client's lobby connection), with its control/eval port on `:3726`;
- the **lobby logic** behind the gateway (title screen, menus, matchmaking, chat commands).

Start it once and leave it running. Stopping it drops the client's lobby connection, and the client then has to
log in again. The lobby logic rebuilds and restarts on its own when you edit it, without dropping the client,
because the gateway holds the connection.

**Terminal 2: the game servers** (needs the C++ libraries; `make games` builds them first):

```sh
cd server
make games
```

This starts:
- **`instance`**, the real game server (control API on `:3729`);
- the **R&D game server** (control API on `:3728`).

Both rebuild and restart on their own when their Go code, a linked C++ library or their `<name>.args` flags file
changes. This group can be stopped and restarted at any time. While no game server is linked, the title screen
greys out PLAY / PRIVATE GAME / PRACTICE / TUTORIAL, and the buttons come back when one relinks. Restarting a game
server drops a match that is in progress. New matches go to `instance` by default: switch with the **R&D** /
**GAME** friend requests in the social panel or the chat command `!gameserver <name>`.

`make run` runs both groups in one terminal instead.

**Terminal 3: the client:**

```sh
./run.sh -- --BNetServer=127.0.0.1:1119 --lobbyServer=127.0.0.1:3724
```

`run.sh` starts the Prometheus launcher through [umu-launcher](https://github.com/Open-Wine-Components/umu-launcher)
with GE-Proton (DXVK; plain Wine's wined3d crashes). Override `WINEPREFIX`, `PROTONPATH` or `UMU_RUN` as needed.
On the first start, select `GameClientApp.exe` in the launcher. With `--` the launcher starts the game straight
away and passes the arguments through. `./build-and-run-with-server.fish` does build + run with these arguments
and Proton logging.

The client logs in automatically and lands on the title screen. From there PLAY, PRACTICE VS AI, PRIVATE GAME
and TUTORIAL lead into a match on the game server. To send it into a match from a script:

```sh
curl -X POST --data 'command("instance")' http://127.0.0.1:3726/eval
```

`systems/ecs/tools/live.py` automates the whole loop with the subcommands `launch`, `instance`, `setup`, `stop`,
`mcp <tool> <json>` and `record`.

### 6. The explorer

```sh
cd explorer && make libs && make build     # Wails v2, pnpm; see explorer/README.md
```

## Tests

The library tests come in two kinds:

- **Unit tests** run anywhere.
- **Replay tests** compare our implementations with **recordings of the real client** (ECS traces, Statescript
  traces, STU blobs, graph dumps).

The recordings are game data, so they are **not distributed**. Without them, CMake builds the replay tests but
doesn't register them, and the STU Go tests skip themselves. To record your own, run the client with Prometheus:

- `systems/ecs/tools/live.py record` records ECS trace fixtures, and `record-cf` a command-frame fixture.
- The Prometheus MCP tools `ecs_dump`, `ss_trace` and `read_stu_at` dump the rest.
- `systems/data/*/tools/` regenerates the catalogues (maps, heroes, movers, Statescript names, lobby scripts)
  from your install or a live client.

Each library's README says which fixtures it expects.

## How the reverse engineering was done

- **Static analysis**: Ghidra on a decrypted dump of the 0.8 client. Addresses in the docs are given as runtime
  addresses (base `0x140000000`) or RVAs; Prometheus works with `gameBase + RVA`.
- **Live**: Prometheus inside the running client. Its MCP server (`POST http://127.0.0.1:17888/mcp/tools/call`)
  serves memory reads, ECS dumps and traces, Statescript graph and sync info, button injection and debug drawing.
  Tools that write are gated behind a toggle in Prometheus' MCP Server window.
- **Conventions in the docs**: claims are marked as *measured* (live or Ghidra) or *inferred*. Visual results
  were confirmed in game.
- **Fidelity rule**: match the client's behaviour and identifiers (component ids, tick order, callback order,
  entity ids, 16 ms command frames), not its memory layout. Where the layout *is* the protocol (mirror data, STU,
  network serialization), it is reproduced byte-exactly.

## Security notes

Prometheus is a research tool with full control over the game process. Treat it that way.

- **MCP write tools are compiled out of this release.** Prometheus' MCP server listens on `127.0.0.1:17888` with
  **no authentication**. The custom `PrometheusLobby` JAM channel also lets **the lobby server you connect to call
  any MCP tool**. With writes enabled, that means arbitrary code execution in the game (`call_function`), memory
  writes and file writes. So `PROMETHEUS_MCP_NO_WRITES` (in `prometheus/Prometheus-Core/MCP/McpTools.h`) refuses
  every write call centrally, and the write toggle in the MCP Server window is greyed out. Read tools, tracing,
  debug drawing and the live server view still work.
- **Re-enabling writes is not advised.** Some of the reverse-engineering workflows in the docs need them: button
  recordings, server echo, node execution, trace files. Remove the define only if you fully understand and accept
  the risks. Then switch writes on per session only, connect only to servers you run yourself, and never expose
  the port. Details: [`docs/PrometheusChanges.md`](docs/PrometheusChanges.md) §2.3.
- Prometheus makes the client accept any TLS certificate for login and skips the RSA step on the lobby state blob,
  so it can talk to our servers. The login server's certificate is self-signed, and the lobby session secret has a
  development default (`owserver-dev-secret`). None of this is meant for an exposed network.

## Credits

- **Prometheus**: [saturn-xvi/prometheus](https://github.com/saturn-xvi/prometheus) and its contributors
  ([`prometheus/CONTRIBUTORS.md`](prometheus/CONTRIBUTORS.md)); the community wiki [owdev.wiki](https://owdev.wiki).
- **[overtools / OWLib](https://github.com/overtools/OWLib)**: type and field names for STU, and the CRC string lists.
- The Blizzard engineers whose GDC talks explained the architecture ([`docs/gdc/`](docs/gdc/README.md)).

## License

[AGPL-3.0](LICENSE), the same license as Prometheus. Third-party code keeps its own license
(`prometheus/external/*` submodules, vcpkg ports, the explorer's npm dependencies).
