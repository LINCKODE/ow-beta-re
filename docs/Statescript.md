# Statescript — Overwatch's Networked Visual Scripting System

> Which graphs are loaded when and how instances start: [StatescriptLoading.md](StatescriptLoading.md).

Reference compiled from the GDC 2017 talk **"Networking Scripted Weapons and Abilities in Overwatch"** by Dan Reed (Senior Gameplay Engineer, Blizzard). Reed designed the Statescript runtime and its synchronization.

- Sources (where to find them: [gdc/README.md](gdc/README.md)):
  - `Reed_Dan_NetworkingScriptedWeapons.pdf` (82 slides, no speaker notes)
  - `networking-scripted-weapons-and-abilities_GENERATED_TRANSCRIPT.txt` (machine-generated transcript, including Q&A)
- `[S##]` cites a slide number. `[T]` means the fact comes only from the spoken transcript. `[Q&A]` means it comes from the audience question session.
- Screenshots in the slides (editor, debugger overlay, node graphs) were read and transcribed. Details from them are marked `[S##, img]`.
- Sections 1–11 come from the talk. Section 12 connects it to Prometheus and the 0.8 binary. Those links are **our hypotheses**, not statements from the talk.

> **Spelling:** The slides always write "Statescript". The auto-generated transcript also produces "StateScript", "StageScript" and "stage group". These all mean the same thing.

---

## 1. Why Statescript exists [S3]

- **Non-programmers need to write high-level logic.** Overwatch needed a lot of content and didn't want an engineer to hand-code every feature.
- **Scripts define game state, not just react to it.** A common approach is an opaque simulation that scripts nudge through events and function calls, "the results of which disappear back into the black box". Statescript instead gives the scripter explicit control of states and state transitions [T].
- **Modular code.** Build reusable building blocks instead of one vertical stack per feature request [T].
- **Painless synchronized state machines.** Writing them by hand is slow and error-prone, "so make the computer do it for you" [T].
- **Engine integration.** They looked at third-party scripting solutions and chose a language built tightly into the gameplay and engine layers [T].
- **History [Q&A]:** Some form of Statescript has existed since **2011**, before Overwatch began, and it was used on **Titan**. Early prototypes showed that writing synchronized state machines by hand "was awful". **Paul Keat** and Reed came up with the state machine system, and Reed built the synchronization. Lead **Tim Ford** backed the project through engineering pushback. Overwatch itself "spun up in late **2013**" [T].

## 2. Core concepts

### 2.1 Scripts and instances [S4–S6]

- Statescript is a **visual scripting language**. Each script is a **graph of nodes** that describes one chunk of gameplay.
- Examples: Tracer's Recall, the Lúcio speed buff (received when a friendly Lúcio is nearby), and common UI elements that every hero has.
- Playing a script creates a **Statescript Instance**:
  - Every instance is **owned by an Entity**, for example a hero.
  - Instances are **added and removed dynamically**. Example: when McCree's flashbang stuns you, a script that stops movement and aiming is played on you for a moment and then removed [T].
  - **Several instances of the same script** can run on one entity.
- Slide 5–6 screenshot [img]: a large graph with red subgraph containers titled *"Sub Graph - 1 = Transform from Recon Mode to Sentry Mode"* and *"Sub Graph - 2 = Sentry Mode"*. This is most likely Bastion's configuration script.

### 2.2 Node kinds [S7]

| Kind | Editor colour | Pins (from screenshots) | Meaning |
|---|---|---|---|
| **Entry** | blue | `Out` | Where script execution starts. The basic entry pulses as soon as the script begins. Other entry types wait for a specific message before they pulse [T]. Example [S12, img]: `Game Message Entry` with `Game Message: [GameMessage] (Combat - Kill)` |
| **Condition** | green | `In` → `True` / `False` | Changes the flow of execution. The `Bool` condition pulses `True` or `False` depending on an expression, for example `Condition: Foo > 3` |
| **Action** | red | `In` → `Out` | Essentially a **C++ function call** that does immediate work and then pulses its output. **`Set Var` is "by far the most common"** node, for example `Foo = 2` |
| **State** | brown/tan | in: `Begin`, `Abort`; out: `OnAbort`, `OnFinished`, `Transition [0]`, `OnBegin`, `OnEnd`, `Subgraph` | **Ongoing work over time.** Inactive until `Begin` is pulsed. Stays active until it deactivates itself or something external deactivates it. Example: `Wait` with `Timeout: 3` |

- Each State type is a C++ class with virtual functions for activate, deactivate, tick, dependency change and so on [T].
- Actions, Conditions and ConfigVars are "much simpler versions of states". Each has one function that gets invoked. Their **runtime objects hold no data**, so nothing is instantiated when execution reaches them [T]. (For rollback, see §8.2.)
- Green text above a node in the editor summarizes its configured properties, for example `Timeout: 3` or `Condition: Foo> 3` [img].

### 2.3 Variables [S8, S26]

- Statescript provides **bags of variables** (`StatescriptVarBag`):
  - **Instance Variables**: private to one script instance.
  - **Owner Variables**: one shared bag on the owning entity that every instance on that entity can see.
- Example [S8, img], Tracer's *Automatic Pistols* script:
  - Instance vars: `AmmoClipMax: 40`, `AmmoInClip: 40`, `BlockFiring`, `IsFiring`, `RecoveryDue`, `ReloadAmmoAtAnimPercent: 0.7000`, `ReloadRequested`, `ReloadTime: 1`, `SpreadMax: 3.5000`, `SpreadMin`, `SpreadProgress`, `SpreadRecovery`, `WeaponEntityLH1: (1, 10, 0, 0)`, `WeaponEntityRH1: (1, 10, 1, 0)`.
  - Owner vars: `AbilityLock: 0`, `BaseHealth: 150`, `BeingBuffed`, `BeingHealed`, `CanResurrect`, `ClearDoTs`, `ClientReticleEnabled`, `ConnectedPlayer`, `CurWeaponIndex: 1`, `DesiredWeaponIndex: 1`, …
  - `AbilityLock` is shared by every instance on the Tracer entity [T].
- A variable is either a **single primitive** or an **array of primitives**. Reed said they sometimes wished for nested structs or bags: "Perhaps we'll get to it someday" [T].
- Variables can be **state-defined**. The value is then supplied by a Statescript State ("ask this state") through `GetStateDefinedValue` [S8, T]. The most common user is **`Chase Var`**, which moves a variable toward a target over time. Example: `MatchTime = 0 at 1 per second` [S26, img], `SpeedScalar = 0 at 0.2 per second` [S14, img].

### 2.4 Properties and ConfigVars [S9–S10]

- Node behaviour is set through **properties**. For each property the scripter picks a **ConfigVar** type from a dropdown.
- ConfigVars can contain **nested properties**. Example [S9, img]: `Position = Get Entity Head Position { Entity = Get Owner }`, `Radius = Integer 100`, `Direction = Get Entity { … }`, `Angle 60`, `Threat { FixedThreat 200, ThreatPerSecond }`.
- **Each ConfigVar type is implemented by one C++ function** that returns a value to the script.
- Types of ConfigVar:
  - **Literals**: Bool, Int, Float, Vec, String, …
  - **Variables**: reads of instance or owner variables. In the editor, the type is shown as `Dynamic`.
  - **Utilities**: C++ accessors such as `Is Hero Select Active` and `Get Entity Position`.
  - **Expressions**: for example `Foo > 3`, or `dist({0}, {1}) > 3` with a nested ConfigVar list (`{0} = Get Entity Position(Entity = Dynamic Source)`, `{1} = Get Entity Position(Entity = Dynamic Target)`). In the editor this shows as `Expression (All Types)` with a `ConfigVars: STUConfigVar[2]` list [S10, img].
- Utility names shown in the dropdown [S9, img]: `Get Entity At Slot`, `Get Entity Base Position`, `Get Entity Bounds Center (Screen Space)`, `Get Entity Bounds Dimensions (Screen Space)`, `Get Entity Collision Radius`, `Get Entity Current Possessable`, `Get Entity Definition`, `Get Entity Facing Vector`, `Get Entity Forward Vector`, `Get Entity Hard Point Position`, `Get Entity Head Position`, `Get Entity Left Vector`, `Get Entity Pet Master`, …
- Other names seen in screenshots: `GetEntityExists`, `GetEntityPassesFilter`, `ArrayContains`, `IsInTutorialMode`, `IsHeroSelectActive` [S12, img].

### 2.5 Subgraphs and containers [S11–S12]

- **Subgraphs**: every State has a `Subgraph` output plug that pulses when the state activates. When the state deactivates, every state in its subgraph is deactivated too.
- Some states have other subgraph plugs that turn subgraphs on and off at specific times:
  - `Boolean Switch`: `TrueSubgraph` / `FalseSubgraph`, active while the condition is or isn't met. This is the most common one.
  - `Logical Button State`: `DownSubgraph` / `UpSubgraph`, plus `OnGoingDown` / `OnComingUp` pulses.
  - `Stack`: `TopSubgraph` / `UnderSubgraph`, with properties `Out_Var`, `Priority: [StackPriority] (High)` and `Value: 2.5` [S14, img].
- Subgraph example [S11, img]: `Boolean Switch (Foo > 3)` drives three `Effect State`s. Each is annotated with its activation rule, for example *"Active if and only if Boolean Switch state is active and Foo > 3"*.
- **Containers** (border colour):
  - **Grey**: organizational only, no effect on behaviour. Example: `Check Entity`.
  - **Red**: defines which states belong to a subgraph. Without one, **a subgraph is assumed to go only one state deep**. Example: `Sub Graph -`.
  - **Blue**: **Client Only**. Example: `Client Only - Hero Select`, containing `Boolean Switch [IsHeroSelectActive]` → `UX - Screen [UXScreen] (In Game - Hero Select)`.
  - **Purple**: **Server Only**. Example: `Server Only - Successful Melee Kill`: `Game Message Entry (Combat - Kill)` → `Bool "Hostile Players Only" [GetEntityPassesFilter]` → `Bool "Is Melee Kill?" [ArrayContains]` → `Wait 1` → `Bool [IsInTutorialMode]` → `Criteria (event) [Identifier] (Criteria_Enemy_Killed_Melee)`.
- Why client-only and server-only exist [Q&A]:
  - **Client-only** is used mostly for **UI**, where the script needs to reason about "the local player", which the server can't do. Scripts store the local player or the team index they care about in a variable.
  - **Server-only** subgraphs were added mostly as **optimizations**, to stop the client predicting logic that needs extra ticking.
  - Some graphs contain both kinds.
- Other nodes in these screenshots: `Play Script` ("Play [StatescriptGraph] (Hero - DJ - Crossfade Results) on Entity". "DJ" is Lúcio's internal name), `Effect State`, `Effect Action` (`[Effect] (MetalSpark)`, `(ArcadeLoopingSound)`, `(SandWind)`, `(CandleFlame)`).

### 2.6 Themes [S13–S14]

**Lifetime guarantees.** States clean up after themselves. When a state deactivates, it:

- stops its animations
- clears the effects it owns
- resets the variables it modified
- shuts off the subgraphs it started

The same guarantee applies up the chain. Removing an instance deactivates all of its states. Destroying an entity removes all of its instances. Shutting down the game deletes all entities. As a result, scripters never have to handle this whole class of bugs [T]. Slide code (a spray state):

```cpp
virtual void OnDeactivate(DeactivateReason reason) override
{
    Super::OnDeactivate(reason);
    if (m_sprayAdmin)
    {
        u64 key = __BuildKey();
        m_sprayAdmin->GetSingletonContact()->StopMaintainingContact
            (ECSingletonContact_C::Contact::MAINTAINED_USER_STATESCRIPT, key);
        Entity* sprayEntity = m_sprayAdmin->GetEntity(m_sprayHandle);
        if (sprayEntity)
            sprayEntity->ScheduleDestruction(false);
    }
}
```

The code shows engine naming conventions: `ECSingletonContact_C` (an EC-prefixed singleton component), a separate "spray admin" (EntityAdmin) with entity handles, and `ScheduleDestruction(bool)`.

**Logic style:**

- **Imperative**: "do this, then check this, then do this". Example: `Wait 3` → `Bool Foo>3` → `Effect Action MetalSpark` → `Set Var Foo = 0`.
- **Declarative**: "whenever this is the case, then this should be the case". Example: `Boolean Switch Foo>3` → TrueSubgraph `Stack(SpeedScalar, High, 2.5)`, FalseSubgraph `Logical Button State (Weapon - Primary Fire)` → DownSubgraph `Chase Var SpeedScalar = 0 at 0.2 per second`.
- Declarative is "the most sane choice" for large features, partly because of the lifetime guarantees. Imperative logic is used "towards the leaves of these declarative trees" [T].

## 3. Worked examples

### 3.1 Reaper "Super Jump" (built live) [S15–S18, S55]

Behaviour: hold secondary fire for 1 s and the camera goes to third person (charged). Release to launch into the air. Holding for less than 1 s does nothing.

```
Entry ─► Logical Button State (Secondary Fire)
            DownSubgraph ─► Wait (1s) ─OnFinished─► Set Var ReadyToLaunch = true ─► Camera 3P (state)
            OnComingUp   ─► Bool (ReadyToLaunch) ─True─► Movement Mod (action, launch) ─► Set Var ReadyToLaunch = false
```

- Editor: **TED**. Title bar: `TED - [Build Local][Branch:1.5.0.0][Locale:Development][Platform:Development][Region:Development]`. The asset tab is `Hero - Reaper - Super Jump`. Properties show `GUID 0x0580000000000D5B`, `Name Gameplay\Scripts\Hero - Reaper - Super Jump…` and `PublicSchema Null` [S17, img].
- In-game **Statescript debugger overlay** [S18, img]: header `View: Server`, `LOD: Instance Summary`, `Entity ID: SE:{40000014} (yourself)`, `Entity Def: Hero - Reaper - Gameplay`, `Instance: Hero - Reaper - Super Jump (ID: 26)`, an instance-variable panel (`ReadyToLaunch: false`), a vertical timeline on the right, and the `CF:` counter.

### 3.2 Tracer Pulse Pistols [S19, S59, S67]

- Instance `Hero - Tracer - Automatic Pistols (ID: 10)`. A large graph that mixes declarative and imperative logic.
- Nodes seen in the debugger: `Stack`, `Effect`, `GamePadVibration`, `WeaponVolley`, `Bool`, `SetVar`, `ChaseVar`. Also the variables `Owner.PrimaryAction`, `Owner.WeaponSpreadAngle`, `IsFiring` and `SpreadRecovery: 0.2000`. The debugger has LOD modes `Instance Summary` and `Instance Detail`, and prints `CF`, `ICF`, a time in ms and a `Ticking` indicator [S59, img].

## 4. C++ runtime [S20–S29]

### 4.1 Timing

- All timing uses integer **Command Frames (CF)**. **1 CF = 16 ms**, so just over 60 CF/s [T].
- An entity must have a **`StatescriptComponent`** to play scripts. Overwatch uses ECS (Tim Ford's talk).

### 4.2 Data layout (slide simplification) [S22]

```cpp
// Entity ─► StatescriptComponent ─► StatescriptInstances
struct StatescriptComponent {
    int                         m_internalCommandFrame; // may differ from sim CF on client while reconciling
    Array<StatescriptInstance*> m_instances;
    StatescriptVarBag           m_ownerVars;
    StatescriptSyncMgr          m_syncMgr;
};

struct StatescriptInstance {
    int                          m_instanceID;   // important for network serialization
    const STUStatescriptGraph*   m_stuGraph;     // static asset, never modified at runtime
    Array<StatescriptState*>     m_states;       // polymorphic, created by factory on first encounter,
                                                 // live until the instance is destroyed
    List<StatescriptEvent*>      m_futureEvents; // scheduled work on states or the instance;
                                                 // often dispatched the same CF, sometimes later (waits)
    StatescriptVarBag            m_instanceVars;
};
```

Reed called this "a gross simplification". The real structures also hold several flags and cache lists for performance [T].

### 4.3 `StatescriptState` base class [S23–S25, S56]

- **Utility functions**: `GetBool`, `GetInt`, `GetFloat`, `GetVec`, `GetEntityID` (property/ConfigVar evaluation), `EnqueueTimerEvent`, `EnqueueAbortStateEvent`, `EnqueueFinishStateEvent`, `SetFrameTickingEnabled`.
- **Virtual functions**:
  - `OnActivate(ActivationReason)` and `OnDeactivate(DeactivationReason)`
  - `OnTimerEvent`
  - `OnFrameTick`: called once per CF, only if the state registered for ticking
  - `GetStateDefinedValue`: supplies an on-demand value for a variable
  - `OnInternalDependencyChanged`
  - `OnBecameActiveThisTick`, `OnBecameInactiveThisTick` and `OnActivationChangedThisTick`: hide networking turbulence (see §8)
- **Dependency tracking** [S25]:
  - Every state has a `StatescriptDependencyListener`, which is an array of pointers to `StatescriptDependencyProvider`s. Each provider points back to its listeners, so the relationship is **many-to-many**.
  - Providers live on `StatescriptVar`s **and on external objects**. Example: when a property reads an entity's health, the listener gets a pointer to a provider on the entity's health component.
  - The listener is **populated lazily** as properties are evaluated.
  - When a provider changes, it calls `OnInternalDependencyChanged` on its listening states. States therefore never poll.

### 4.4 Variables in C++ [S26]

- A `StatescriptVarBag` holds a table of pointers to `StatescriptVar`s, allocated when first referenced.
- The key is a **16-bit identifier** that maps to a registered asset in the asset system.
- `StatescriptVar` comes in two kinds: `StatescriptVarPrimitive` and `StatescriptVarPrimitiveArray`.
- Primitives are stored in a **128-bit union** that can hold integers, vectors, pointers to strings, and so on.
- `StatescriptVar`s are **dependency providers**.
- A var can optionally **reference a `StatescriptState`**. Reading it then calls that state's `GetStateDefinedValue`.

### 4.5 Structured Data (STU) [S27]

- Assets are defined in **Structured Data** (`.stu`). A compile step generates code for the **editor, the asset compiler and the runtime**.
- STU supports **attributes** and **runtime reflection** on types and members. Both the Statescript editor and the Statescript asset compiler use them.

### 4.6 Anatomy of a node: the `Wait` state [S28–S29]

`.stu` schema (STU markup, not C++):

```csharp
class STUStatescriptStateWait : STUStatescriptState
{
    [Constraint(typeof(STUConfigVarNumeric))]   // editor restricts dropdown to numeric ConfigVars
    embed<STUConfigVar> m_timeout;

    embed<STUStatescriptOutputPlug> m_onAbortPlug;
    embed<STUStatescriptOutputPlug> m_onFinishedPlug;
};
```

Runtime `.cpp`:

```cpp
class StatescriptStateWait : public StatescriptState
{
    DECLARE_STATESCRIPT_RTTI(StatescriptStateWait, StatescriptState)
public:
    virtual void OnActivate(ActivateReason) override
    {
        const STUStatescriptStateWait* stu =
            GetSTUStatescriptStateT<STUStatescriptStateWait>();
        EnqueueFinishStateEvent(GetFloat(stu->m_timeout));   // seconds
    }
    STATESCRIPT_IMPLEMENT_TRAVERSE_ABORT(STUStatescriptStateWait, m_onAbortPlug)
    STATESCRIPT_IMPLEMENT_TRAVERSE_FINISHED(STUStatescriptStateWait, m_onFinishedPlug)
};

IMPLEMENT_STATESCRIPT_FACTORY_AND_RTTI(StatescriptStateWait, STUStatescriptStateWait)
```

- `DECLARE_STATESCRIPT_RTTI` sets up runtime type information.
- `GetSTUStatescriptStateT<T>()` returns the editor-authored static data.
- The `TRAVERSE_*` macros make the plugs fire at the right times.
- `IMPLEMENT_STATESCRIPT_FACTORY_AND_RTTI` **links the runtime class to the STU class**, so the system knows which class to create when execution reaches the node.

## 5. Networking requirements [S30–S36]

| Requirement | Meaning |
|---|---|
| **Usability** | Hide networking from the scripter. **The same script runs on server and client.** They decided early against separate server and client scripts. Splitting behaviour is opt-in and should be rare [S31–S32, T] |
| **Responsiveness** | Player actions take effect immediately on the client, whatever the latency |
| **Security** | Hackers must not be able to influence server behaviour with malicious data |
| **Efficiency** | Must work on poor networks with high latency and packet loss |
| **Seamlessness** | Minimize visible networking artifacts. This started as "fix it as you go" and was later formalized because of pops and stutters on some abilities |

## 6. Networking model [S37–S42]

### 6.1 Synchronized and unsynchronized instances [S38–S39]

- **Synchronized**: server and client describe the **same logical instance**. The system is **eventually consistent**. Whatever the client predicts and whatever the network does, the two converge again.
  - Used for: weapons, abilities, emotes, game modes, map entities (doors, health packs, …).
- **Unsynchronized**: independent internal logic. These instances can still **receive messages from** and **read variables of** synchronized instances.
  - Used for: menus, the hero gallery, the end-of-match flow, music.
  - Example: game-mode scripts tell the music script to change tracks [T].

### 6.2 Local and remote [S40]

- On each client, at most **one networked entity is controlled by the player**. That entity and its instances are **local**. Every other networked entity is **remote**.
- Sometimes there is **no** local entity, for example in a kill cam or Play of the Game [T].
- The **server tracks which entity is local to which client**.

### 6.3 Server authority and input [S41–S42]

- Statescript is **server-authoritative**. Traffic is mostly server → client. **Only button input and aim go client → server.**
- Input timing: the server sends its latest simulated CF (say 100). The client can't send input for CF 100 because it's already past on the server. Instead it sends input for **CF 100 + RTT** (here 105) so the input arrives in time.
  - The server measures how early or late input frames arrive and reports the RTT back to the client (a feedback loop).
  - In practice the client aims a bit further ahead when RTT jitters.
- Diagram constant: **RTT = 5 CF (80 ms)**.

## 7. Server synchronization [S43–S50]

Loop: **gather client input → simulate (record changes in `StatescriptDeltas`) → send the deltas to clients**.

Everything lives in the component's **`StatescriptSyncMgr`**:

```
Entity
 └ StatescriptComponent
    └ StatescriptSyncMgr
       ├ StatescriptDeltas   (what changed, per CF)
       ├ StatescriptGhosts   (what each client knows)
       └ StatescriptPackets  (reusable serialized payloads)
```

### 7.1 `StatescriptDelta` [S44–S45]

A delta is created **only on CFs where something changed**, which is a small percentage of frames. It records what changed between the start and end of that CF:

- Command Frame
- An array of every **synchronized instance** that changed, each with:
  - Instance ID
  - Creation and destruction flags
  - An array of every **instance variable** that changed: its identifier and, **for arrays, the affected index range** (so the whole array doesn't have to be sent)
  - Indices of **states that changed**
  - Indices of **actions that executed**
- An array of every **owner variable** that changed

A delta is **kept until every client has acknowledged its CF**.

### 7.2 `StatescriptGhost` [S46]

One per client per entity. It records what that client knows:

- Client ID
- **Last acknowledged CF**: the client is known to have everything up to and including this frame
- Pointers to **outstanding packets**: sent, but not yet ACKed or NACKed

The lower network layer tells systems when a packet is **ACKed**, or assumes a **NACK** after a timeout. Either result removes the packet from the outstanding list. A ghost lives until its client disconnects.

### 7.3 `StatescriptPacket` [S47–S48]

- **Local/Remote flag**: the payload format depends on whether the entity is local for the recipient.
- **CF range** (start and end).
- **Payload**: take the **union (set union) of all deltas in the range**, then serialize the **current values** of the objects it references.
  - If the range **starts at 0** (a newly connected client), send the current value of everything. This is a **full update**, and deltas aren't used.
- Packets are **cached and reused** for any request with the same CF range and Local/Remote flag.
- A packet is kept until every client has ACKed its **end** CF.

### 7.4 Loss recovery example [S50]

1. CF 100: a delta is created and packet [100] is sent.
2. CF 103: a new delta. Packet [100] is still in flight, so only [103] is sent.
3. CF 106: the server concludes [100] is lost. It resends the **union [100 + 103]**, in case [103] is lost as well.
4. The client **drops the lone [103]** packet. Applying 103 without 100 would create a simulation state that never existed on the server, and the union packet will cover it anyway.
5. The ACK for [103] alone doesn't help, because 100 is still outstanding. The ACK for the union confirms the client has both.

Redundancy is accepted because it recovers faster from bursts of loss.

## 8. Client synchronization [S51–S60]

### 8.1 The algorithm [S51]

The local entity **stores its input and predictions**. It runs *ahead* of incoming server data (§6.3). When a `StatescriptPacket` arrives:

1. **Send an ACK.**
2. **Ignore** the packet if it is redundant or out of order.
3. **Remote** packet → **Replicate**.
4. **Local** packet → **Rollback** all predictions → **Replicate** → **Simulate** back to the present with stored input ("**roll forth**"). New predictions are made during the roll forth, but with the originally recorded button input and aim.

This all happens **within one frame**, so the player never sees it.

Diagram colours: green = simulation, magenta = rollback, cyan = replication.

### 8.2 Remote entities [S52]

- Remote instances mostly **do not follow pulse links or process events**. They only tick states that registered for ticking, and they rely on the server for everything else.
- **Exception: client-only subgraphs** run a full simulation while the synchronized state that owns them is active.

### 8.3 Predicted packet timeline [S53]

`ICF` = **Internal Command Frame**, the frame the Statescript system is currently simulating. It can differ from the entity's `CF` while the client reconciles.

| Client CF | What happens | ICF |
|---|---|---|
| 100 | predicts activity | 100 |
| 103 | predicts activity | 103 |
| 105 | server packet for CF 100 arrives: roll back 103 and 100, replicate 100, re-simulate 101–105 (predicting 103 again) | 103, 100 (rollback) → 100 (replicate) → 101–105 |
| 108 | packet for 103 arrives: roll back 103, replicate 103, re-simulate 104–108 | 103 → 103 → 104–108 |

### 8.4 Mispredicted packet timeline [S54]

- An **unpredicted server event** at CF 95 (for example a stun or death) arrives at client CF 100. There is nothing to roll back, so the client replicates at ICF 95 and re-simulates 96–100.
- The client predicted activity at 103 that **the server never performed**. Once it knows confirmation won't arrive, it rolls back 103 and re-simulates 104–108.

### 8.5 Debugger walkthrough of the Reaper jump [S55, T]

- Server view: stepping the release frame shows the `Down` subgraph turning off (including `Camera 3P`), then `Bool ReadyToLaunch` → true → `MovementMod` → `SetVar ReadyToLaunch = false`.
- Client view: a green (predicted) region where CF == ICF. Then a **magenta rollback region** where ICF jumps back to the prediction frame. Then a **cyan replication region**.
- **Replication doesn't follow links.** It sets variables and updates states, and nothing more. Neither action is replicated:
  - `SetVar` is redundant, because variables are replicated directly.
  - `MovementMod` is redundant, because the **movement system replicates itself**.
- The roll forth recorded nothing because there was no work to do. Afterwards CF == ICF again.

### 8.6 Helper APIs for node authors [S56–S58]

C++ authors must avoid restarting animations, playing sounds twice or spawning extra particles because of a rollback. "The answer is yes, it is harder" [T].

**`StatescriptState`:**

```cpp
OnActivate(ActivationReason reason)       // reason: link traversal, rollback, replication, entity destroyed, …
OnDeactivate(DeactivationReason reason)
IsRollingBack()
IsReplicating()
IsRollingForth()
GetActivationSummary()                    // all (de)activations of this state during the current frame
OnBecameActiveThisTick()                  // called by the client sync mgr at END of frame if activation
OnBecameInactiveThisTick()                //   status differs from start of frame → use for output-only
                                          //   states (VFX, sound, UI) to avoid pops during rollback/roll-forth
PutUpdate(DataStore* dataStore)           // manual server→client serialization of state data
GetUpdate(DataStore* dataStore)
```

**`StatescriptAction`:**

```cpp
DoRollback()          // invoked during rollback
GetRollbackStorage()  // temporary per-action storage, since actions are stateless singletons;
                      // used e.g. to avoid replaying a sound during replication/roll forth
```

Reed admitted this breaks the "actions have no state" rule. Predictable actions like these are rare anyway [T].

**Mirrored data** [S58] replaces `PutUpdate`/`GetUpdate` (crossed out on the slide) with `WriteMirroredData()`/`ReadMirroredData()`. It is an STU `mirror` type that **replicates server → client automatically and rolls back automatically**:

```csharp
// .stu
mirror MStatescriptStateChaseVar
{
    Vec3A m_curVal;
    s64   m_lastTickTime;
    s32   m_timeRemainingMS;
    bool  m_isVec;
    bool  m_hasDuration;
    bool  m_hasReachedDestination;
};
```

```cpp
// .cpp
MStatescriptStateChaseVar* data = WriteMirrorData();
Vec3A curVal = data->GetCurVal();
bool isVec;
Vec3A destination = __BuildVal(stu->m_destination, stu->m_destinationBone,
                               stu->m_destinationObject, &isVec);
data->SetCurVal(destination);
data->SetTimeRemainingMS(0);
data->SetHasReachedDestination(true);
```

This tells us the `Chase Var` STU has at least `m_destination`, `m_destinationBone` and `m_destinationObject`, and that generated mirror classes get `Get*/Set*` accessors.

**Weapon Volley example** [S59, T]: while rolling forth, `WeaponVolley` keeps ticking on every ICF but **does not emit shots it already emitted**. Projectiles are simulated and synced by a separate system and **are not rolled back** like Statescript states, so the state has to account for that itself.

### 8.7 Historical data during re-simulation [S60]

| Historically accurate (rewound per CF) | Not historical (latest value only) |
|---|---|
| All variables and states on the **local** entity | Variables and states on **remote** entities |
| Button input and aim | Data from **other components** (for example **health** and **filtering**) |
| **Positions and poses of all entities** | |

- The server also keeps historical positions and poses and knows each client's RTT. It evaluates abilities against the **same** positions the client saw, which avoids mispredictions.
- Non-historical data is fine as it is. The server never rewound that data for the client either, so using historical copies would *increase* mispredictions.

### 8.8 Rollback implementation notes [Q&A]

- They don't snapshot and restore the full state. Every state is its own class, and a full copy would be slow.
- Predictions are stored as an **undo list** ("the previous value of a variable or of a state") and **replayed backwards** on rollback.
- Rollback costs **about as much as forward simulation**, but only the **one local entity** does it. Remote entity deserialization and processing is the bulk of the cost.
- The rollback code is "one file, **2,000 lines**".

## 9. Bandwidth efficiency [S62–S68]

- Deltas + ghosts + packets tolerate loss without resending every intermediate packet (because of unions).
- **Scripts are analysed at compile time** to find what needs syncing:
  - **Local** entity packets must contain *everything*, so prediction stays accurate. Proving which nodes affect only the server was too hard, and remote optimization matters more: 1 local hero against 11 remote ones.
  - **Remote** entities **don't simulate** synchronized instances. They tick registered states but don't pulse links. Their packets only need the **states and actions remote instances care about, plus the variables those nodes reference**.

### 9.1 `SYNC_ALL` attribute [S64–S65]

```csharp
[STUStatescriptNode::SYNC_ALL]
class STUStatescriptStateRaycast : STUStatescriptState
{
    [Constraint(typeof(STUConfigVarVecBase))]  embed<STUConfigVar> m_startPosWS;
    [Constraint(typeof(STUConfigVarVecBase))]  embed<STUConfigVar> m_dirWS;
    [Constraint(typeof(STUConfigVarNumeric))]  embed<STUConfigVar> m_castLength;
    [Constraint(typeof(STUConfigVarEntityID))] array<embed<STUConfigVar>> m_ignoreEntities;
    [Constraint(typeof(STUConfigVarNumeric))]  embed<STUConfigVar> m_castRadius;
    embed<STUConfigVarFilter>  m_castFilter;
    embed<STUConfigVarDynamic> m_out_hitSomething;   // "m_out_*" = output written to a variable
    embed<STUConfigVarDynamic> m_out_hitEntity;
    embed<STUConfigVarDynamic> m_out_hitPointWS;
    embed<STUConfigVarDynamic> m_out_hitNormal;
};
```

| | States | Actions |
|---|---|---|
| **With `SYNC_ALL`** | transmitted locally **and** remotely | transmitted locally **and** remotely |
| **Without** | transmitted **locally only** | **not transmitted** |

Runtime objects can **opt out** of remote transmission, for example when the variable they modify isn't needed by remote clients yet [T].

Other things this listing shows:

- ConfigVar constraint classes: `STUConfigVarNumeric`, `STUConfigVarVecBase`, `STUConfigVarEntityID`.
- Special property types: `STUConfigVarFilter` and `STUConfigVarDynamic` (a variable reference used as an output).
- Naming: `WS` means world space.

### 9.2 Compiled sync metadata in `STUStatescriptGraph` [S66]

During compilation of each graph asset:

1. Reflection finds the nodes that must reach remote clients.
2. Those nodes are scanned for the variables they reference, which become the remote-syncable var list.
3. The compiler works out how many bits are needed to index these in a packet.

The result is effectively **a unique, optimized message protocol for each script asset**. This works because server and client share the same assets.

```csharp
array<internal<STUStatescriptNode>>   m_remoteSyncNodes;
array<inline<STUStatescriptSyncVar>>  m_syncVars;
s32 m_nodesBitCount;
s32 m_statesBitCount;
s32 m_remoteSyncNodesBitCount;
s32 m_syncVarsBitCount;
```

### 9.3 Numbers [S67–S68]

In the debugger demo, a remote Tracer in client view lights up far fewer nodes and has a much shorter instance-variable list than the server view. That saves client CPU as well as bandwidth.

Tracer firing a full clip and reloading (exactly 2 s):

| | Local | Remote |
|---|---|---|
| States | 463 bits | 190 bits |
| Instance vars | 519 bits | 246 bits |
| Owner vars | 54 bits | 54 bits |
| Events | 431 bits | **0 bits** |
| Other | 561 bits | 316 bits |
| **Total** | **2028 bits (1.0 kb/s)** | **806 bits (0.4 kb/s)** |

12 Tracers all firing (1 local + 11 remote) ≈ **5.4 kb/s** of Statescript traffic [T]. Note that "Events" are sent only for the local entity.

## 10. Benefits [S69–S75]

- **Rapid iteration**: new heroes go from prototype to shippable with little or no new code.
- **Automatic syncing and state machines**: most new node types are **server-only or client-only** implementations, so hand-written sync is rare. A few heavy states run their own internal C++ state machine, but "for the most part, Statescript is our state machine".
- **Fewer lifetime and desync bugs**: state machine and sync logic live in one place and were "hammered on for years".

### 10.1 New node types per hero, in development order [S71–S72, img]

The list is Reed's own and "by no means complete". Hero names were identified from the slide portraits.

| # | Hero | New node types |
|---|---|---|
| 1 | Tracer | State, Wait, Set Var, Chase Var, Boolean Switch, Cosmetic Entity, Logical Button State, Data Flow Mapping, Invisible, Stack, Weapon Volley, Movement Mod Action, Blink Effect, History Builder, History Rewinder, Health Pool, Animation |
| 2 | Reaper | Track Targets, Modify Health, Target Effect, Phase Out, Movement Mod State, Teleport, Find Ground, Camera 3P, Send Game Message, Effect State, Effect Action, Switch, Watch, Eject Cosmetic Entity |
| 3 | Widowmaker | Aim Speed, Camera Transform, Extending Ledge Finder, Move To, Apply Aura, UX - Screen, UX - View Mode, UX - UI In World, Destroy Entity |
| 4 | Pharah | — |
| 5 | Reinhardt | Animated Camera, Steer, Track Slamming, Camera Look At, Play Script, Create Entity State, Shockwave, Suppress Movement Prediction |
| 6 | Mercy | Beam Effect, Resurrect, Create Entity Action |
| 7 | Torbjörn | Override Model Look, Object Placement Validation, Pet |
| 8 | Winston | Track Movement |
| 9 | Hanzo | Wall Move |
| 10 | Symmetra | — |
| 11 | Bastion | Limit Aim |
| 12 | Zenyatta | Combat Mod Filter, Restart Anim |
| 13 | McCree | — |
| 14 | Zarya | Track Ray, Contact Set State |
| 15 | Soldier: 76 | — |
| 16 | Lúcio | — |
| 17 | Roadhog | Raycast Action |
| 18 | Junkrat | Possess Control, Possess View |
| 19 | Genji | Deflect Projectiles, Deflect Projectiles Effects, Pause Anim |
| 20 | Mei | Raycast State |
| 21 | D.Va | — |
| 22 | Ana | — |
| 23 | Sombra | Track Validated Position, Teleport Over Duration |

Q&A extras:

- Engineers choose **code over script** for complex loops (possible in script, but messy) and for performance-sensitive work. Raycasts are the canonical example: a fire-and-forget **Raycast Action**, or a **Raycast State** that ticks a result every frame, with the heavy lifting in C++.
- A **"block rewinding"** node exists. Example: after Reaper uses Wraith Form, lag-compensated hits from shooters are not honoured, so the defender can escape.
- New heroes are "almost entirely in script". Native code is added only for new node types.

## 11. Challenges [S76–S80]

- **Up-front cost**: the runtime, an editor and a debugger.
  - Reed spent about half his time from late 2013 on the runtime, sync manager, compiler, debugger and core features.
  - Several person-months per year of tools-engineer support go to the editor.
  - The **debugger was written in C++ with no external tools**. It records every entity's history and can step through it with a server/client toggle [Q&A].
- **Learning curve**:
  - Deciding code versus script. Don't build one monolithic node per feature, but don't over-split simple tasks either. State machines generally belong in script.
  - Getting used to the declarative style.
- **Occasional workaround**: the model is **state-based, not event-based**. It sends **end-of-frame variable values, then states and actions**, not a blow-by-blow history.
  - Example (Symmetra charged secondary fire) [S80, img]: `Set Var VisualScalar = Scalar` → `Weapon Volley` → `Set Var Scalar = 0`. On remote clients `Scalar` replicates **before** the Weapon Volley state, so the projectiles appear at size 0.
  - Fix: a second variable, `VisualScalar`, which the volley reads through `ProjectileVisual → FixedDataFlow[0] (STUStatescriptWeaponProjectileDataFlow) → Answer = VisualScalar (Dynamic)`.
  - Weapon Volley properties visible in that screenshot: `AimID 0`, `ProjectileSpeed 10`, `NumProjectilesPerShot 1`, `NumShotsPerSecond 1`, `ProjectileEntity: STUStatescriptWeaponProjectileEntity`, `ProjectileVisual: STUStatescriptWeaponProjectileVisual { AnimRecoilImpulseFactor, FixedDataFlow, ShowHUDWarningIndicator }`, `Flight { AimID, ProjectileLifetime }`, `StateGroup [No StatescriptStateGroup]`, `Unique Id 672`. Weapon Volley's output plugs are `OnMinShots`, `OnAbort`, `OnFinished`, `Transition [0]`, `OnBegin`, `OnEnd`, `Subgraph`.
- **Asset workflow [Q&A]**:
  - Scripts are **exclusive-checkout** assets. A "local checkout" mode allows uncommitted experiments.
  - The mitigation for contention is splitting scripts into smaller ones.
  - The system is **not portable**. It depends heavily on the asset system, STU and the networking layer.
- **Verdict**: declarative scripting fits eventual consistency well, and "we'd probably end up with something very similar" if they did it again.

---

## 12. Relevance to Prometheus / the Overwatch 0.8 binary (hypotheses)

The talk describes the 2017 live game. Build 24919 (beta 0.8, 2015) predates it, but it already has most of this machinery. The table links talk concepts to what `prometheus/Prometheus-Core/Statescript.h`, `StatescriptVar.h` and the Atlas headers already model. **Verify in Ghidra before relying on any row.**

| Talk concept | Prometheus / binary counterpart | Notes |
|---|---|---|
| `StatescriptComponent` | `Component_23_Statescript` (component **0x23**), with `StatescriptSyncMgr ss_inner` at `+0xC0` | Matches the slide: the component embeds the sync manager |
| `StatescriptSyncMgr` (deltas/ghosts/packets on server) | `StatescriptSyncMgr` with `g1_instanceArr`, `cf_timestamp @0x2D0`, `rid_entity_varbag @0x2E0`, logical-button fields, `entity_admin_scope` | We only see the **client**. The ghost and packet machinery is server-side and may be absent or dead in the client. `cf_timestamp` is a good candidate for `m_internalCommandFrame` (ICF) |
| `m_ownerVars` | `StatescriptSyncMgr::rid_entity_varbag` | "RID" appears to be Prometheus's name for a var/identifier |
| `StatescriptInstance { m_instanceID, m_stuGraph, m_states, m_futureEvents, m_instanceVars }` | `StatescriptInstance { instance_id (short) @0x18, graph @0x160, state_impl_list @0xA0, updates_1/2 @0xE8/0xF0, rid_instance_varbag @0x98 }` | The instance ID is **16-bit**, which fits Prometheus's "script IDs are 16-bit" note. `parent_instance_id @0x1A` supports parent-child instances |
| `StatescriptEvent` list (`m_futureEvents`) | `StatescriptNotification_*` (`Timer`, `Lifetime{0 begin, 1 finish, 2 abort}`, `SetVar`, `SetVarArray`, `LogicalButton`, `AttrDependencyChanged`, `EvaluateExits`, `TraverseFrom`), each with `m_timestamp` (CF) and `m_instanceId` | `Lifetime` begin/finish/abort match `Enqueue{Abort,Finish}StateEvent`. `Timer` matches `EnqueueTimerEvent`. The `ToString()` vfunc can be used for logging |
| 128-bit primitive union | `StatescriptPrimitive { i64 value; i32 field_8; type }` = 16 bytes | Matches exactly. Types include `FLT16VEC4`, `ColorRGBA` and `ENTITY` |
| 16-bit var identifier | `StatescriptNotification_SetVar::variable_guid` (short) | The identifier maps to an asset GUID in the talk. Resolve it via hashlibrary/OWLib |
| State virtuals `OnActivate/OnDeactivate/OnTimerEvent/OnFrameTick/OnInternalDependencyChanged/GetStateDefinedValue` | `StatescriptState_vt`: `OnTick_mby @0x138`, `OnTimerTick @0x140`, `PreBeginState @0x160`, `PreFinishState @0x168`, `OnAttributeDependencyChanged @0x170`, `OnDependencyChanged @0x180`, `GetStateDefinedValueArray @0x230`, `SubscribeToVariables @0x218` / `UnsubscribeFromVariables @0x220` | Subscribe/Unsubscribe is probably the lazy `DependencyListener` registration. `PreFinishState`'s arg (1 or 6) looks like a `DeactivationReason` |
| `PutUpdate/GetUpdate(DataStore*)` | `StatescriptState_vt::bsDataStore_WriteData @0x1F0` / `bsDataStore_readData @0x1F8` | The name in the binary is **`bsDataStore`** |
| `DoRollback` / `GetRollbackStorage` on actions | not mapped yet | Look in `StatescriptAction_vt` after `0x100` |
| `OnBecameActiveThisTick` etc. | `needs_clientonly_update @0x210`? unconfirmed | Look for an end-of-frame loop in the sync manager that compares activation at frame start and end |
| `IMPLEMENT_STATESCRIPT_FACTORY_AND_RTTI` | `StatescriptRTTI { next_instance, factory_func, stuclass_inherit, implementation_type }` linked list starting near `0x18f8e88` | Matches: one record per node class links the runtime factory to the STU inheritance info |
| `STUStatescriptGraph` sync metadata | `STUStatescriptGraph::m_remoteSyncNodes`, `m_syncVars`, `m_nodesBitCount`, `m_statesBitCount`, `m_remoteSyncNodesBitCount`, `m_syncVarsBitCount`, plus `m_predictionBehavior`, `m_publicSchema`, `m_playScriptHotReloadBehavior` | **Exact field-name match** with slide 66. These are good for decoding the per-graph packet bit widths |
| Client-only and server-only containers | `STUStatescriptBase::m_clientOnly @0x75`, `m_serverOnly @0x76`, `SyncMgr::has_clientonly_scripts` | These are node flags. The client binary may still contain server-only node data |
| `Logical Button State` | `StatescriptNotification_LogicalButton{m_logicalButton, m_bButtonGoingDown}`, `SyncMgr::logicalBtn_list`, `OnLogicalButtonChanged @0x1C8`, the `statescript_fake_logicalbtn` window | Button input is the *only* gameplay input the client sends (§6.3). Faking it locally drives predicted scripts |
| `Stack` state | `STUStatescriptStateStack`, `StatescriptStateStack`, `SyncMgr::stack_states[7]`, `STUConfigVarStackPriority` | Matches the `Priority: [StackPriority]` in S14 |
| Expressions `dist({0},{1}) > 3` | `STUConfigVarExpression { m_expression, m_configVars }` and `StatescriptExpressionParser.h` | Matches the `{n}`-indexed nested ConfigVar list |
| `STUConfigVarDynamic` (variable reference / `m_out_*`) | `STUConfigVarDynamic` / `STUConfigVarDynamic_Editable` | Matches |
| `SYNC_ALL` attribute | not mapped | STU attributes may survive in the reflection data (`InheritanceInfo`/STU RTTI). Search for an attribute flag on node classes |
| Asset GUID `0x0580000000000D5B` for a Statescript graph | — | If the top 12 bits are the asset type, graphs are type **0x058**. Check this against OWLib's type table and the resource IDs passed to `Statescript_vt::load_graph` |

### RE leads

- **Strings and class names from the slides to search for in Ghidra:** `StatescriptStateWait`, `STUStatescriptStateWait`, `StatescriptStateRaycast`, `MStatescriptStateChaseVar` (mirror data), `STUStatescriptSyncVar`, `STUStatescriptOutputPlug`, `STUConfigVarNumeric`, `STUConfigVarVecBase`, `STUConfigVarEntityID`, `STUConfigVarFilter`, `STUStatescriptWeaponProjectileEntity`, `STUStatescriptWeaponProjectileVisual`, `STUStatescriptWeaponProjectileDataFlow`, `ECSingletonContact_C`, `MAINTAINED_USER_STATESCRIPT`.
- **Activation and deactivation reasons**: look for small enums passed into the begin/finish/abort paths. Candidates are link traversal, rollback, replication and entity destruction.
- **Rollback undo list**: the talk says predictions are stored as "previous value of var/state" records replayed backwards. Look for a list of `{instance, var id, old primitive}` or `{instance, state index, old active}` entries on the sync manager, and a loop that walks it in reverse.
- **Packet decode (client side)**: the local/remote flag, a CF range, then bit-packed indices whose widths come from `m_nodesBitCount` / `m_statesBitCount` (local) or `m_remoteSyncNodesBitCount` / `m_syncVarsBitCount` (remote). Array vars carry an index range. Find the reader by xref'ing those four fields.
- **Replication skips link traversal.** Code paths gated by an "is replicating" flag should skip `ActionOrEntry_ExecuteAndContinue`.
- **Remote instances don't pulse links**, except in client-only subgraphs. That explains why many nodes never execute on non-local entities in Prometheus's graph window.
- **Node catalogue**: §10.1 is a ready-made list of runtime node names to match against `StatescriptRTTI` entries and STU class hashes.
- **Caveat**: the talk describes the 2017 live game. Beta 0.8 is earlier, and features such as mirrored data or `SYNC_ALL` may be incomplete or named differently.
