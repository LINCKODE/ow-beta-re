# GDC talks: background reading

Blizzard engineers have described several of the systems we reverse-engineer in public conference talks.
They give us the vocabulary and the intended design: Statescript instances, deltas and ghosts, JAM protocols
and channels, ECS systems and singletons, command frames. The beta 0.8 client (build 24919) is still the
specification, though. Every talk-derived claim in our docs is checked against the binary (Ghidra) or the live
client (Prometheus), and where the shipped code differs from a talk, the docs say so (for example
[StatescriptSync.md §5.6](../StatescriptSync.md)). This page lists the talks we rely on, links to the official
recordings and slides, and gives our own summary of the parts that matter for understanding the client.

> **Copyright.** The slides, recordings and transcripts of these talks are © their authors, Blizzard
> Entertainment and/or GDC (Informa). They are **linked, not included**: this repository holds no copy of
> any slide deck, video or transcript. The summaries below are our own paraphrase. Some GDC Vault content
> needs a (free or paid) Vault account.

---

## 1. Networking Scripted Weapons and Abilities in 'Overwatch'

- **Speaker:** Dan Reed (gameplay engineer, Blizzard Entertainment; designed the Statescript runtime and its sync)
- **Event:** GDC 2017, Programming track
- **Official links:**
  - GDC Vault: <https://gdcvault.com/play/1024041/Networking-Scripted-Weapons-and-Abilities>
    (the same session is also listed as <https://gdcvault.com/play/1024653/Networking-Scripted-Weapons-and-Abilities>)
  - No official GDC YouTube upload found (2026-10-18).

**Summary**

- Overwatch writes nearly all high-level logic in **Statescript**, a visual scripting language: hero weapons
  and abilities, game modes, map objects (doors, health packs), UI and music. Every script is a graph of nodes.
- Playing a graph creates an **instance**, which belongs to an entity (through its Statescript component).
  Instances are added and removed at runtime, and one entity can run several instances of the same graph.
- There are four node kinds: **entries** (where execution starts, sometimes on a message), **conditions**
  (branch on an expression), **actions** (one immediate C++ call, e.g. Set Var) and **states** (work that
  lasts over time, with Begin/Abort inputs and OnBegin/OnEnd/OnFinished/Subgraph outputs). Only states
  hold runtime data.
- Variables live in **var bags**: instance variables (private to one instance) and owner variables
  (shared by every instance on the entity). A variable is a primitive or an array of primitives. Some are
  "state-defined", meaning a state supplies their value (Chase Var is the common case).
- Node properties are **ConfigVars**: literals, variable reads, C++ utility getters, or expressions, and
  they can nest. Each ConfigVar type is one C++ function.
- **Subgraphs** hang off a state and are active only while that state is active. Coloured containers mark
  client-only and server-only parts of a graph. States clean up after themselves (effects, animations,
  modified variables, child subgraphs), so the lifetimes nest from state up to instance, entity and game.
- Graphs and node properties are **STU** structured-data assets. The same graph runs on server and client,
  and splitting behaviour between them is opt-in.
- **Network model:** instances are either synchronized (server and client converge on the same logical
  instance: weapons, abilities, modes, map entities) or unsynchronized (local only: menus, music). On each
  client at most one entity is *local* (the controlled hero); every other networked entity is *remote*.
- The server is authoritative. Only button input and aim go from client to server, and the client sends
  input for a **future command frame** (current server frame plus round-trip time) so it arrives in time.
- **Server side:** each entity's sync manager records a **delta** for every command frame on which something
  changed (changed instances, variables with array index ranges, state and action indices). It keeps one
  **ghost** per client (last acknowledged frame, packets in flight) and builds cached **packets** that cover
  a frame range by taking the union of deltas and sending current values. A range starting at 0 is a full update.
- Lost packets are recovered by resending the **union** of all unacknowledged ranges rather than each
  packet. The client drops a packet that would skip an unapplied frame.
- **Client side:** remote packets are simply replicated. A local packet triggers **rollback** of every
  prediction, then replication of the server state, then **roll forth**: re-simulation up to the present
  with the recorded input, all inside one frame. Predictions are kept as an undo list rather than snapshots.
- During re-simulation, local variables and states, input and aim, and all entity positions and poses are
  historically accurate. Remote entities and other components' data (for example health) use the latest value.
- Remote entities do not run full logic: they tick registered states and rely on the server for the rest.
  A **SYNC_ALL** node attribute decides which states and actions are sent to remote clients. The graph
  compiler stores the remote-relevant nodes and variables, and the bit widths needed to index them, in the
  graph asset, which amounts to a custom wire protocol per graph.
- The talk gives bandwidth figures (roughly 1 kb/s for a local Tracer emptying a clip, much less for a
  remote one) and names the main limitation: sync is **state-based, not event-based** (end-of-frame values
  are sent, not a history), which sometimes needs workaround variables in scripts.

**Where this shows up in our docs**

- [../Statescript.md](../Statescript.md): a detailed section-by-section reference of the talk, with our hypotheses on how it maps to the binary
- [../StatescriptSync.md](../StatescriptSync.md): the sync protocol as the client actually implements it; §5.6 compares it with the talk's model
- [../StatescriptRuntime.md](../StatescriptRuntime.md): the client's runtime (graph, instance, var bags, states, notifications)
- [../StatescriptLoading.md](../StatescriptLoading.md), [../StatescriptNodes.md](../StatescriptNodes.md), [../StatescriptConfigVars.md](../StatescriptConfigVars.md): loading, node behaviour, ConfigVar evaluation
- [../DataFlow.md](../DataFlow.md): the talk's projectile-visual workaround example, followed through the client
- [../STU.md](../STU.md): the structured-data format graphs are stored in (mirror data)

---

## 2. Network Serialization and Routing in World of Warcraft

- **Speaker:** Joe Rumsey (principal software engineer, Blizzard Entertainment; former lead server
  engineer on World of Warcraft, at the time working on Blizzard's next MMO, the project that became Overwatch)
- **Event:** GDC 2013, Programming track
- **Official links:**
  - GDC Vault: <https://gdcvault.com/play/1017733/Network-Serialization-and-Routing-in>
    (also listed as <https://gdcvault.com/play/1018849/Network-Serialization-and-Routing-in>)
  - No official GDC YouTube upload found (2026-10-18).

**Summary**

- **JAM** ("Joe's Automated Messages") is Blizzard's application-level serialization and message-routing
  library, in use since 2001. It started between World of Warcraft servers and later spread to tools and,
  in newer projects, to **client-server** traffic. The talk itself covers the server-to-server case.
- The goal is no hand-written serialization: messages are plain structs declared in `.jam` files, and a
  code generator produces the message classes, serializers, dispatchers and handler prototypes.
- A **protocol** is one `.jam` file: an ordered list of messages. Each message's ID is its position in the
  file, and the generator computes a **CRC per protocol** over the messages and their fields.
- Messages support nested structs, arrays and other dynamic types, and the generator emits encoders for
  them (binary, and a named-field form used for JSON).
- Sending targets a service, a connection or an object. Request/response pairs ("registered messages")
  carry a sequence number so replies can be matched to requests. Object messages (with a 128-bit object ID)
  are only used between internal servers.
- Receiving is configured per service with a **route config**: which protocols it receives and sends, and
  which handler and dispatch policy each one uses.
- Threading options: raw concurrent dispatch from network threads, a **message queue** drained at a known
  point on one thread (the model the game client uses), a global lock, or a custom lock policy per message.
- **Connection handshake:** each side sends a hello that lists its protocols (name, CRC, direction).
  Matched protocols get a **one-byte channel ID** in a deterministic order, and a connection can be set to
  refuse peers whose protocols don't match (used for the game client).
- **Wire framing:** a variable-length size, the channel ID, a message type byte, a 16-bit message ID, the
  object ID for object messages, then the payload. Fields are written in declaration order with no tags,
  and (as of 2013) no varints or bit packing.
- If a protocol's CRC differs between the two peers, that protocol falls back to a **JSON** encoding
  (missing fields default, unknown fields are ignored). This is internal only, never on public-facing
  protocols.
- The talk compares JAM with protobuf, Thrift and Avro: JAM gives up cross-version flexibility for speed,
  and the speaker reports it serializing several times faster than default protobuf.

**Where this shows up in our docs**

- [../JAM.md](../JAM.md): §1–10 summarise the talk in detail, §11 maps it to the client (our hypotheses), §12–13 describe our message catalogue, generated Go codecs and custom protocols
- [../LobbyServer.md](../LobbyServer.md), [../InstanceServer.md](../InstanceServer.md): our servers, which speak JAM to the client
- [../TitleScreen.md](../TitleScreen.md), [../LobbyScripts.md](../LobbyScripts.md): the lobby-side JAM messages and the scripts they trigger

---

## 3. 'Overwatch' Gameplay Architecture and Netcode

- **Speaker:** Timothy Ford (lead gameplay engineer, Blizzard Entertainment)
- **Event:** GDC 2017, Programming track
- **Official links:**
  - GDC Vault: <https://gdcvault.com/play/1024001/-Overwatch-Gameplay-Architecture-and>
  - YouTube (official GDC channel): <https://www.youtube.com/watch?v=W3aieHjyNvw>

We have no local copy of this talk. The summary below is from the public recording and is less detailed
than the two above. [../Statescript.md](../Statescript.md) refers to it for the ECS background.

**Summary**

- Overwatch gameplay is built on an **Entity Component System**. An entity is an ID, a component is data
  with little or no behaviour, and a system is behaviour that keeps no state of its own. Systems run in a
  fixed order each tick over the components they care about.
- All of this lives in an **EntityAdmin** (the "world"), which owns the systems and the entities.
- State that exists once per world (input, camera and the like) goes in **singleton components** rather
  than in system members, which keeps systems stateless.
- Behaviour that several systems share, but that has few call sites, goes in **utility functions** rather
  than in new systems. Side effects that would be costly or awkward to do immediately (e.g. impact
  effects) are **deferred** and handled later in the frame by the system that owns them.
- The simulation is split into fixed **command frames** (16 ms at the time) so that client and server
  step the same deterministic simulation.
- The client **runs ahead** of the server by about half the round-trip time plus a small buffer. It
  predicts the local player's movement and abilities immediately and sends its input for each command frame.
- The server keeps a small **input buffer**. When the buffer runs dry because of packet loss, the server
  tells the client to change its tick rate slightly (**time dilation**), so the client runs a bit faster to
  build the buffer back up, or slower when it is too far ahead.
- To survive loss, the client re-sends every input the server has not yet acknowledged, so a gap is filled
  by the next packet that gets through.
- When the server's authoritative result disagrees with a prediction, the client **rolls back** to the
  server state and **re-simulates** the stored inputs up to the present. Movement state is designed to be
  cheap to snapshot and replay for this.
- Hit registration is **lag-compensated**: the server rewinds other entities to the positions the shooter
  saw. Statescript-driven abilities use the same prediction scheme (covered in depth in Reed's talk above).

**Where this shows up in our docs**

- [../ECS.md](../ECS.md), [../ECS_Systems.md](../ECS_Systems.md), [../ECS_Components.md](../ECS_Components.md): entity admins, entities, systems, components and singletons as found in the client
- [../CommandFrames.md](../CommandFrames.md): the 16 ms command frames, the client clock, and the dilation flags the server sends
- [../Movement.md](../Movement.md): the per-frame movement pipeline, the input stream and reconciliation
- [../InstanceServer.md](../InstanceServer.md): our server's 16 ms tick and clock steering
