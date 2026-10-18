# The client's UI system (UX), and how to add our own UI

How the 0.8 client (build 24919) builds its menus and HUD screens, and what it would take to add UI of our
own. T032. Builds on [LobbyScripts.md](LobbyScripts.md),
[StatescriptRuntime.md](StatescriptRuntime.md), [STU.md](STU.md), [ResourceLoading.md](ResourceLoading.md).

**Tags**: [G] read in code (Ghidra / PE), [D] read from game data offline, [L] measured live (Prometheus MCP),
[H] confirmed by the user in game, [?] inferred. **Addresses**: Ghidra (`0x7ff64ffe0000 + RVA`) unless marked RVA.

---

## 1. Summary

- The UI ("UX") is an **in-engine WPF/XAML clone**: screens are compiled XAML stored as STU assets
  (`STUUXScreen`, GUID prefix `0x09A…`, 124 of them) with a visual tree (Grid, StackPanel, Button,
  ContentControl, TextBox, ItemsControl, …), dependency properties (`Visibility`, `Margin`, `Command`, …),
  bindings, visual states and storyboards. [G][D]
- **Data** reaches a screen through a **view model (VM)**: a key → value bag (`ViewModelRoot`, RVA `0x18a7070`).
  Keys are UI variables (`0x0AC…` assets, e.g. `EnablePlayButton`) or CRC32 names. [G][L]
- A **Statescript UX state** (`STUStatescriptStateUXBase` and subclasses) is the whole binding layer: it opens
  its screen (`m_screen`) on a layer (`m_layer`), creates the screen's VM, **writes VM values from Statescript
  expressions** (list `+0x100`), and turns **button commands into graph links** (list `+0x110`). [G][D]
- Native lobby states (main menu, party, heroes, social, matchmaking, profile) use their own VM classes, filled
  by native code from lobby data. [G]
- **Adding UI**: the realistic route is **runtime asset injection through Prometheus** (the engine already has
  "create resource from memory" and a runtime GUID allocator) plus an **STU writer/patcher** for new screens,
  and a Statescript UX state (or a small native hook) to open them. Patching live element objects works for
  small changes; the lobby server alone cannot add UI (§5).

## 2. Assets [D]

| asset | GUID prefix / type | what | notes |
|---|---|---|---|
| UI screen | `0x09A…` (STU root `STUUXScreen` `0x1c68e74d`) | compiled XAML: `m_rootElement` (visual tree), `m_keys`/`m_values` (resource dictionary: command links + texts), `STUUXReferences` (links, resource dictionaries, resources), `m_loadBehavior` | inventory of all 124: `systems/data/lobby_scripts/screens.txt` (not published: regenerate with `systems/data/lobby_scripts/tools/inventory.py`) (elements, which graphs open it, key tables, strings, UI vars) |
| resource dictionary | `0x0BA…` (type 0x05E) | shared styles/templates (`108`, `249`, `24b`, `11e`, …) | referenced by every screen |
| UI variable | `0x0AC…` (type 0x036) | a named key: VM property or command (`PlayPressed`, `EnablePlayButton`, `IsViewActive`) | 331 names in `ui_variables.tsv`; names live in the UX assets, not in the exe [G] |
| display text | `0x0DE…` (type 0x07C) | strings (`strings_enUS.tsv`) | referenced lazily (handle −1 until built) [L] |
| textures / fonts | `0x0C0…` (0x004) / type 0x050 | images (`TextureSource`), fonts | |

**Element tree** (main menu `0x09A…0B2`, readable dump via `tools/screen_tree.py`): e.g. the TUTORIAL button is a
`STUUXButton` with local values `Style = MainMenuSmallButton`, `IsEnabled = Binding(Values.EnableTutorialButton)`,
`Pulse` binding, `Command = {0xf1fdd0e5 m_linkGUID = TutorialPressed}`; its label comes from a
`STUUXString{m_displayText = 0x0DE…11D1 "TUTORIAL"}`; visibility via a converter (`HideButton` → Hidden/Visible). [D]

**Property keys** (`m_propertyKey`, u32) = `crc32(lowercase(name)) XOR ownerClassHash` [G: name hash
`FUN_7ff6507d88e0`; key built in DependencyProperty ctor `FUN_7ff650ad7ad0`, registration `FUN_7ff650ad7500`].
The owner hash equals the STU type hash of the owner UX class, e.g. `0x6f37773e` = UIElement/FrameworkElement
(Visibility, Width, Margin, Opacity, DataContext, Style, …), `0x7dfc365b` ItemsControl (ItemsSource,
ItemTemplate, …), `0xbb1c8761` Control/TextElement (Foreground, FontSize, …), `0x0da969f2` ButtonBase (Command,
CommandParameter), `0x2444ba0f` Grid (Column, ColumnSpan). All 248 keys used by the 124 screens resolve from the
property-name strings in `.rdata` (`0x7ff651575000–0x7ff651590000`). [G][D]

## 3. Runtime

### 3.1 Screen lifetime [G][L]
- The screen **asset is loaded while its state runs and unloaded afterwards**: `0x09A…0B2` (main menu) is
  resident on page 1 and gone while page 10 is shown [L]. The loaded object is the STUD file buffer (STU.md §2).
- In the loaded asset, references are **lazy** `{handle −1, GUID}` pairs: the Button's command link
  `0xf1fdd0e5` = `{vtable RVA 0x14f4c70, −1, GUID}`, `STUUXString` = `{vtable, teString, {−1, GUID}}` [L].
- When the screen is **built**, values are copied into runtime objects. Command links become 32-byte boxed values
  `{vtable RVA 0x1593ce8, 0x8000000000000001, vtable RVA 0x15938d0, GUID}` in a heap pool (live: the main menu's
  `CancelPressed`, `PlayPressed`, `TutorialPressed` ×2, `CustomPlayPressed`, `CancelCustomGameJoin`, …) [L].
  Consequence: **patching the asset after the screen is built has no effect** [H] (§6).

### 3.1b Build pipeline and hosts [G] (subagent report, not checked live)
- **Open**: the VM stores the screen GUID at `+0x68`; `FUN_7ff650a5c470(UXRuntime+0x10, vm)` (called by `d12e60`
  and ~35 native VM ctors) checks type 0x09A, acquires the resource (`9a2d30`) and queues a **CreateView** message
  (vtable `0x7ff651571898`, carries a VM property snapshot `a54ef0`) to the UX thread. `UXRuntime` =
  `DAT_7ff651887070` = `DAT_7ff651886688` (ctor `a59430`, per frame `a434b0 → a5a830`, unload a view `a5a590`).
- UX thread: `a5c6b0` builds a **UI-side VM** (vtable `0x7ff651571748`); `a5a960` → `a5c8d0` per pending view:
  gets the loaded screen (`a5e760`), computes its **host key** from `m_loadBehavior` (`a5c850`), calls the hosts
  registered under that key.
- **Instantiate**: `FUN_7ff650ac6880(host, screenSTU, &uiVm)`: resource dictionary (`a910c0`), wrapper element
  (vtable `0x7ff651576cf0`), `DataContext` = UI-side VM, root element from `m_rootElement` through the factory,
  `LoadFromSTU` recursively.
- **Factory**: STU type hash → runtime class map (`adaf70`, map `DAT_7ff65178e108`, filled by `ad90e0` from the
  static table `0x7ff65178ceb0`: 292 pairs, UX classes STUUXArc … VisualStateManager;
  `systems/data/ui/factory_table.json`, not published); `ad92b0(&out, stu)` turns any
  STU object into its runtime object. `LoadFromSTU` (inner vtable `+0x20`, base `acb8d0`) applies `m_localValues`
  {value, propertyKey} via **SetValue `acbba0`** (DP registry `DAT_7ff65178ce48`); Panel `a938f0` creates
  `m_children` (panel `+0x448`). Objects are ref-counted, pooled per class.
- **Hosts / "layers"**: `STUUXViewSource` placeholders inside container screens register (`ac6420`) under a key:
  `m_id` (a UI variable used as a named slot), `m_source` (a screen GUID), or −1 (`m_isRoot`). A screen's key
  from `m_loadBehavior`: `0xa9237b62` (105 screens) = its own GUID; `0x5c36b48c` (18) = slot `0x0AC…46E`.
  Tree [D]: root `0x09A…273` hosts `0x29B` (in-game container, 58 slots incl. HUD `0x0EE`), **`0x29C` (lobby
  container, 48 slots incl. the main menu `0x0B2`)**, `0x1BF`. So **a new screen only shows if some ViewSource
  hosts its GUID or slot**: an injected screen must reuse an existing GUID/slot, or a container must be patched.
- Modal/input: `IsModal` DP (`a8b720`) → modal set `UXRuntime+0x900`; `RequiresMouse`, `IsCanvasLayer`,
  `VirtualScreenWidth/Height` DPs; input routing `a5b4a0`. `m_layer` (+0x146) use not found [?].
- No XAML parser (Expat exists, unrelated): all UI comes from STU. STU objects can be built in code
  (`ac7460` STUUXGrid, `b7e270` STUUXTextBlock).
- Element creators: TextBlock `b0e350`, Button `b30300`, Grid `b4c850`, StackPanel `b704d0`, Image `b4eb90`,
  ContentControl `aea680`, ViewSource `ac6f90`; `Panel.Children` add `a93f20(panel,&child)`; DPs DataContext
  `a85e00`, Content `aea720`, Command `b30250`. Rendering: vector canvas "teCanvas" (`a4d680`, settings `a42250`),
  FreeType linked; draw traversal not traced.

### 3.2 The UX state [G]
`STUStatescriptStateUXBase` (`8ba08edd`, STU fields: `+0x100` VM values, `+0x110` commands, `+0x120 m_screen`,
`+0x128` filter, `+0x138 m_out_Handle`, `+0x146 m_layer`, flags). Runtime class: virtual inheritance, main vtable
RVA `0x15ae5a0` (UXBase) / `0x15ae2a8` (UXScreen, ctor `FUN_7ff650cf76e0`); standard state slots
(StatescriptRuntime.md §3.6) plus UX slots `0x258…0x2e8`:
- `0x278` `FUN_7ff650cfbf10`: evaluates `m_screen`, calls **slot `0x2e0` = create the VM** (default
  `FUN_7ff650d12e60`: a 0x4b0 `StatescriptScreenVM`, vtable `0x7ff65157b080`, key = screen GUID, events `Pulse`
  `0xdad7e5de` and `Deactivate` `0xb16bd083`), stores it at state `+0x390`, subscribes `cfc290`/`cfc2b0`.
- `FUN_7ff650cfb1e0`: registers the state with the **screen/layer manager** (reached via the state manager
  `+0x450 → +8 → +0x28`, RTTI `0x7ff6517b28d8`; its lists at `+0x30`, `+0x48`, `+0x60`, `+0x78`), parent/child
  screen stacking (`+0x130` chain, `+0x385/+0x386` dirty flags).
- `0x258`/`0x260` (`cfab10`/`cfaac0`): set `IsViewActive` (`0x0AC…8CE`) on the VM.
- **VM values** (`+0x100`, entries `1e89514b` = {UI var GUID `+0x10`, ConfigVar `+0x18`}): `cff960`/`cff770`
  evaluate each ConfigVar and mark changes; `cffb40` → `d148e0` (RVA `0xd348e0`) dispatches on the var type to
  the typed setter. Graph text shows them as `0x6fa954ac=[EnablePlayButton={IsPartyLeader() && …}, …]`.
  They are re-pushed whenever the expression changes, which is why a one-off `write_viewmodel` doesn't stick.
- **Commands** (`+0x110`, entries `af12fa9c` = {output plug, command UI var `+0x30`, u32 `+0x38` 1 = end state}):
  the screen raises `Pulse(GUID)` on the VM → state slot `0x2e8` `FUN_7ff650cfc070` queues the GUID at `+0x398`
  → tick slot `0x128` `FUN_7ff650cfbce0` matches it and fires the plug (`FUN_7ff650f270b0`), ending the state if
  `+0x38 == 1`. `Deactivate` sets `+0x3b0`.
- `STUStatescriptStateUXViewModel` (`c2a01675`): binds to a **child VM or array element** of its parent state's VM
  (`+0x150` key, `+0x158` index) and fills it with its own `+0x100` list: how Statescript fills list items /
  nested views.

### 3.3 View models [G][L]
- Base ctor `FUN_7ff650a543e0`; layout: `+0x10` native property listeners, `+0x28` event listeners, `+0x48` id,
  `+0x50` property list `{u64 key, {i64 value, u8 type, u8 has_value}}`, `+0x68` key/type, `+0x80` outbox to the
  UI layer. 118 VM classes (names de-XORed by vtable slot 4).
- Setters `(vm, key, value, onlyIfChanged)`: `a56020` bool/char, `a560b0` int, `a56140` float, `a561e0` string,
  `a562e0` uint/colour, `a56370` long (RVAs `0xa76…`); each queues the change (`a54710` → `ViewModelRoot+0x10`)
  and notifies native listeners (`a55530`). Lookup `a542a0` is linear and adds missing keys. Child VM `a56730`,
  array `a567d0`/`a566d0`. **No schema asset**: defaults are set in native ctors.
- Native lobby states override slot `0x2e0` with their own VM classes: `d876a0` LobbyMainMenuVM, `d850d0`
  LobbyMyHeroesHeroVM, `d85fa0` LobbyPartyVM, `d87db0` LobbyAllHeroesVM (array key `0x50208af1` of
  LobbyAllHeroesHeroVM), `d9d6a0` SocialVM, `d01d50` MatchmakingVM, `d893f0` LobbyProfileVM; they fill them from
  lobby data (e.g. `FUN_7ff650d87210`: VersionNumber, player id, IsMaxLevel).
- UI → VM: packets in `ViewModelRoot+0x10` (`+0xf8`), `a5adc0` → `a9a3e0` → `a55480` RaiseEvent (commands), or
  `a9a6f0` (two-way binding writes).
- Live at the title screen [L]: ~100 VMs (ConsoleVM, ChatVM/ChatItemVM, SocialVM, VoiceChat*, LobbyPartyVM,
  LobbyMainMenuVM (25 props), LobbyPartyMemberMeVM, 4 StatescriptScreenVM); on the private-game page the
  CustomGame* VMs (slots, rules, maps, heroes).

### 3.4 Rendering
The UX tree is drawn by the game's own renderer (not traced in detail [?]). Prometheus' ImGui overlay is unrelated:
it hooks D3D11 `Present` and draws on top of the finished frame (`Prometheus-Core/dllmain.cpp`), so it can only be
a fallback "UI" outside the game's input/focus and style.

## 4. The engine's runtime-resource path [G]
- `FUN_7ff6509a5de0(u64* outHandle, desc*)` = **create a resource from memory**: `desc = {u64 guid, void* buf,
  u32 size, u8 ownsBuf, u8 flag}`; it resolves the handler by GUID type and runs `CreateFromMemory`
  (`FUN_7ff6509a9130`: new load entry straight to state 9 "Awaiting Creation", then the normal construct/ready
  path). Used by e.g. `FUN_7ff650840a80` (runtime textures).
- `FUN_7ff6509a6160(type)` = **runtime GUID allocator**: returns `type bits | 0x1000000000000000 | index`
  (bit 60 set; type dispatch ignores bits 60–63).
- So Prometheus can create an STU screen (or strings, textures) under a fresh or an existing GUID without
  touching CASC, and keep it resident by holding a reference.
- **Implemented (T045)**: the Prometheus MCP tool `inject_resource {guid, path}` does exactly this for any file
  (gated by the write toggle, reference held until exit). It was first used for an edited map data, docs/Maps.md §10.

## 5. Adding our own UI: the three routes

| route | what | feasibility / effort | breaks / risks | verdict |
|---|---|---|---|---|
| **a. Data** | author a `STUUXScreen` (+ strings) and open it from a UX state | Needs an **STU writer** (STUD v3 is a memory image: lay out instances, table, list descriptors; the reader and the full type registry exist; est. medium, ~600–1000 lines C++ in `systems/stu`) or a smaller **patcher** (append instances to an existing screen, relocate lists). Delivery: **runtime injection via Prometheus** with §4 (small: allocate GUID, CreateFromMemory, hold ref, ~100 lines). Opening: a UX state whose `m_screen` points at it (patch a loaded graph's ConfigVar, or inject a graph too), or retarget an existing unreachable state. Offline CASC patching (encoding, index, APM package record + CKey) is possible in principle but heavy and pointless while Prometheus is present | layout CRC per instance must match the registry; handles must be −1/lazy; asset unloads when unreferenced (hold a ref) | **recommended** long-term route |
| **b. Runtime** | build/modify element objects natively from Prometheus | Patching live element objects **works** (command GUIDs sit in plain boxed values, §3.1; TUTORIAL rewired to MY HEROES [H], §6); creating new elements: build STU objects in memory and run them through the factory (`ad92b0`), or call the element creators, then `Panel.Children` add `a93f20` + `SetValue` `acbba0`, on the UX thread (§3.1b; not tried) | fragile (addresses per build, object lifetime tied to the screen), must run on the UI thread | good for small tweaks and prototypes |
| **c. Server** | lobby server only | Can reach existing screens only through lobby data and the few messages that post game messages (LobbyServer.md "Server-driven navigation"); texts only via chat-line notices; cannot set VM values or add elements | none | not a way to add UI |

Hooking a command: any UI variable we choose can be a command (the state's `+0x110` list matches GUIDs); a
native hook on `FUN_7ff650cfc070` (Pulse) or `a55480` (RaiseEvent) sees every command, which is the hook point for
"a button that does something Prometheus/our server decides" (links to T033, custom JAM messages).

## 6. Prototype attempt (TUTORIAL → MY HEROES)
1. Patched the **loaded asset** `0x09A…0B2`: the TUTORIAL button's command link (`0x303818d0`: `TutorialPressed` →
   `MyHeroesPressed` `0x0AC…22D`) and its text (`0x30381350`: `0x0DE…11D1` → `0x0DE…DCB` "MY HEROES"). Written and
   read back [L]; **no effect** [H]: the button still read TUTORIAL and fired the tutorial request. The built
   screen had copied the values (§3.1).
2. Patched the **built screen's boxed command values** instead: after a client relaunch both `TutorialPressed`
   values of the main-menu pool were at `0x44387f8` / `0x44388f8`; wrote `MyHeroesPressed` (`0x0AC…22D`) to both
   (2026-09-26, user-approved). Clicking the 4th button (still labelled TUTORIAL) **changed the page** [H]; the
   loaded screens switched from `0x09A…0B2` to `0x09A…0B6` = **hero gallery** (page 2) [L]. So a button can be
   rewired at runtime by patching its built command value; the UX state's `+0x110` list routes any command GUID
   it knows.
   **Rule**: asset bindings are copied when the screen is built; the built objects are the patch point (or hook
   the build / patch the asset between load and build). The patch lives as long as the built screen (a page change
   rebuilds from the asset).
3. Not done: the label (the built TextBlock's text, or the asset patched before the build), and a genuinely new
   button (needs an STU patcher + injection, or element creation through the factory, §3.1b).

## Status and open questions
Open:
- §3.1b is from a static read only: verify live (factory map, a host registered for `0x0B2`, the UX thread).
- The UI-side command path (Button Execute → `Pulse` on the VM), the root host (−1), `m_layer`, the draw traversal.
- Who builds the in-memory UI-variable name table (names are in the UX assets).
- The label of a rewired button; a real new button via an STU patcher + injection.

Related: UI and in-world presentation values that are not UX view-model variables travel through **DataFlow**
([DataFlow.md](DataFlow.md)): the lobby's global data-flow values (graph `0B68`) land in the GlobalVariableStore
(comp 0x52, §4.2.3 there); the capture ring's mapped progress `0x0FD` has no reader in 0.8 (§7.2 there).

## New read-only findings (2026-09-29, UI reference research)  [D, G]
Read-only research (explorer MCP over the install, Ghidra reads).
- **Screen census** [D]: the 124 screens (`0x09A…`) hold 16,067 STU instances of 145 types: 5,283 property
  assignments, 895 texts, 601 binding paths, 336 bindings, 94 storyboards with 465 keyframes (45 screens), 28 buttons;
  99 slots that host other screens, in only 4 container screens.
- **Fonts** [D]: three TrueType files behind a 64-byte header (`0x0F2…`, type 0x050): Big Noodle Too, Futura No2 D Demi
  Bold, and a 16 MB face (probably CJK [?]); four font-family records (`0x00A…`, type 0x051) name them.
- **`m_layer`** [D]: values 4, 5, 6, 7, 9 in the lobby graphs: main pages 5; dialogs / overlays / ESC menu 7; settings 9.
  Looks like a z-band; no reader found [?].
- **The UX state tree** [G]: the screen manager keeps all active UX states, a root list and a list waiting for their
  parent; a UX state attaches under its parent UX state, so on-screen nesting follows the *script* nesting, not the
  screens' asset nesting. Opening a screen / creating its view model is skipped without a UI manager.
- **Match HUD `0x1E1`** [D]: the HUD screen state targets `GetLocalPlayer()` and writes a handle var; a client-only
  `UXToastQueue` branch binds a child view model "Toast" and finds its parent screen through that same var (how a
  script adds a sub-panel to an open screen).
