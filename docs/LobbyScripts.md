# Lobby Statescript: the front-end graphs on the lobby entity `0x80000002`

What every Statescript graph on the lobby EntityAdmin's singleton entity does in the 0.8 client (build 24919):
the instance tree, the menu "pages" and their UI screens, how each screen is reached (the trigger chain from a
lobby JAM message or a button down to the state), which screens nothing reaches, and what the lobby server
would have to send for the rest. T023; builds on [TitleScreen.md](TitleScreen.md) (§5 = the `ClientInLobby*`
catalogue this doc refers to by message id).

**Tags**: [G] read in code (Ghidra), [D] read from the game data offline (`systems/content` + `systems/stu`),
[L] measured live (Prometheus MCP), [U] confirmed by the user in game, [?] inferred.
**Addresses**: Ghidra (`0x7ff64ffe0000 + RVA`) unless marked RVA.
**Data**: every graph rendered as text, the UI-variable and string tables and the tools are in
`systems/data/lobby_scripts/` (not published; generators in its `tools/`). Node numbers `#n` below are
`m_nodes` indices (= Prometheus `dump_script` idx). `$04f1` = Statescript variable `0x0D8…04F1`.

---

## 1. Summary

- The whole front end is one **page switch**: graph `0x046B` (UXLobby) holds a Switch on variable **`$04f1`**
  whose cases run one sub-script per page (main menu, hero gallery, hero page, shop, avatar/profile, private
  game, custom servers, …). Buttons change pages by `SetVar $04f1 = <page constant>`. [D][L]
- The page-changing buttons are UX screen **commands** (UI variables `0x0AC…`, e.g. `StorePressed`,
  `MyHeroesPressed`); the screens bind their buttons to those commands by name (`STUUXScreen.m_keys/m_values`). [D]
- **Unreachable** (no path from the real UI) [D][U]:
  - **pages 2–8** (hero gallery, all heroes, hero page / details / customize, shop): the main-menu screen
    `0x09A…0B2` defines the commands `MyHeroesPressed`, `StorePressed`, `PracticePressed` and their texts
    (MY HEROES, SHOP, PRACTICE) in its resource table, but **no button element uses them**, so the 0.8 main menu
    shows only PLAY / PRIVATE GAME / PRACTICE VS AI / TUTORIAL (user screenshot, 2026-09-25). The graph handlers
    (`0134` #2/#3) still exist; Prometheus `execute_node 0134 #3` opens the hero gallery [L];
  - page 9 **Custom Servers** (`0x088E`, screen `0x09A…3B3`: "Private Games / Custom Servers / Manage My Servers /
    Refresh / Create / Join / Leave / Profile"): no graph ever sets `$04f1 = 9`;
  - the **HOME / BACK bar** of the social menu (`0x0138` #3, screen `0x09A…0B4`): a state with no incoming link
    (only in the graph's remote-sync list);
  - selecting a hero in **ALL HEROES** (`0x0142`): its Watch on the selection is not wired to anything.
- **Gated by lobby data** [G][D]:
  - hero page (`0x0144`): **hero details / change mods** (`0x0141` → `0x015F`) only for a hero in the **owned-product
    set** (26401); otherwise the hero sheet with an **Unlock** button (→ purchase flow `0x017D`);
  - main-menu **"PLAY THE TUTORIAL?"** dialog while 24300's bool (profile `+0x148`, "tutorial done") is 0: the
    user saw it on every lobby arrival [U]; lobbylogic now sends 24300 with the bool = 1 during the connect sequence
    (the client's own 24200 request comes ~50 s later, after the menu has checked it) [L];
  - **CAIS "Healthy Gaming" warning** screen `0x09A…4B9` when the 20804 play-time tracker is enabled;
  - **Custom (private game) button** needs a client global (`DAT_7ff651839e2c`), and the private-game lobby
    page 10 needs 23303 (custom-game type 1/2);
  - party / friend / invite toasts and dialogs are driven by 20702 (party invite received), 20701, 23302,
    27103 (game messages, §6); 20700 is the party state, 20703 clears a pending invite (corrected by T031).
- The **avatar / profile screen** (`0x0135`, screen `0x09A…0C2`: PLAYER AVATAR, HEROES, AVATARS, HIGHEST HERO,
  EDIT AVATAR) opens only when the **party panel** (`0x02BA` UXPartyScreen) writes a player to `$067e` [D]; clicking
  (left or right) the own entry does not do that in 0.8 [U], so the screen is unreachable too.
- The **hero gallery**, forced open with Prometheus, is **empty**: only ALL HEROES, two blank bars and "Search (NYI)";
  no hero tiles, nothing to click [U]. Its list is filled by the page's native class (`0x58d7d94f`), and with our
  24900 records + 26401 products it lists nothing; one `LobbyMyHeroesHeroVM` exists [L]. So the 26401 gate of the
  hero page (§4.3) could not be exercised in game.
- "custom server" strings: `0x0DE…1065` "Custom Servers" (screen `0x3B3`), `0x0DE…E39` "Banned from custom server
  %s Reason: %s" (23306 popup), `0x0DE…C99` "Custom server not loaded." (20803 index 39),
  `0x0DE…E3C` "Custom server settings updated, will take effect next match." (no code or data reference found). [D][G]
- lobbylogic now sends **26401 with every hero owned** (`server/internal/lobbylogic/store.go`). Result in §8.

## 2. Instance tree (live, title screen) [L][D]

`list_loaded_scripts ea=lobby` on the title screen: 17 instances. Parent → child from the graphs' SubScript
states [D]; "(page n)" = a case of the `$04f1` switch, loaded only while that page is shown.

```
-1  041A  root ("Developer Mode" in old notes; really the front-end root)
    ├─ -3  0147  loading screen / tips (UXNextMap, screen 0x0C0) — game msgs 0x09B/0x128/0x40B
    ├─ -4  046A  boot warnings: screen 0x0BE, 0x4BB "unreleased content" (Windows), → 0151 (login/options/quit) until $08e3
    │        (0151: login-flow screens, CAIS warning 0x4B9 gated by 20804, OPTIONS/QUIT 0x027)
    ├─  ·  0194  POD/party dev flow (only if ShowMode()); → 04F6 welcome, dev screen 0x4C1 (gate 0x7a4d3a04: two globals)
    ├─ -6  01E5  music controller
    ├─ -7  0278  ESC/options menu (0x028: RETURN TO GAME / HERO SELECT / settings), settings screens 0x05D 0x148 0x139 0x187 0x454 0x4BE
    ├─ -8  0B68  data-flow switch on $1a6e (which front-end "context" is active)
    └─  ·  (#11/#12 replay filter stacks)
-2  041B  lobby root (all children unconditional; 046B only when !ShowMode())
    ├─ -9  046B  UXLobby: the page switch (§3)
    │    ├─ -10 02D8  chat control (UXChatControl)
    │    ├─ -12 0138  social menu bar (0x0B5: social button, counts) — only when $08e3; hosts:
    │    │    ├─ -13 0158  navigation history (page / hero / player stacks for BACK)
    │    │    │     └─ the $04f1 Switch → one page sub-script:
    │    │    │          -14 0134 (page 1 main menu) | 0137 (2) | 0142 (3) | 0135 (4) | 0144 (5) → 0141 | 0143 (6)
    │    │    │          | 0155 (7) | 015F (8) | 088E (9) | 08CA (10)
    │    │    └─  ·  017D  unlock/purchase flow (while $054a)
    │    └─ -11 0B47  title-screen 3D scene (TitleScreen.md §2) — only when $0a0d
    ├─ -15 02BA  social panel: friends list 0x093, party panel 0x0C1, toasts/invites 0x18C
    ├─ -16 02DA  matchmaking status (0x18D "Searching / Match Found / Entering Match")
    ├─ -17 03F2  map-load / instance-connect waits (game msgs 0x122, 0x117, 0x22B)
    └─ -18 0B63  voice-chat screen 0x4C2
```

Variables `$04d2`, `$08e3`, `$0a0d` gate this tree and are written by no graph (native code) [D]; all three are
`true` at the title screen [L].

## 3. The page switch (`0x046B` #2) [D][L]

`0x046B` #2 = `STUStatescriptStateSwitch` on `$04f1`. The page constants are set once at start by `0x0138`'s
OnBegin chain (`$04f3 = 1`, `$04f4 = 2`, `$050c = 3`, `$04f5 = 4`, `$0511 = 5`, `$0510 = 6`, `$053b = 7`,
`$0544 = 8`, `$1560 = 9`, `$15d8 = 10`), then `$04f1 = (CustomGameType is 1 or 2) ? 10 : 1`. Live: `$04f1 = 1`. [L]

| page | sub-script | state → UI screen | what it is (strings / type) | set by (button → command → node) |
|---|---|---|---|---|
| 1 (default) | `0134` | #8 `0x73661e44` → `0x0B2` | **main menu**: PLAY, PRIVATE GAME, PRACTICE VS AI, TUTORIAL, SHOP, MY HEROES | start; HOME/BACK; `0135` BackPressed; `08CA` when not in a private game |
| 2 | `0137` | #1 `UXHeroesScreen?`(`0x58d7d94f`) → `0x0B6` | **hero gallery**: MY HEROES / ALL HEROES / COLLECTED | main menu **MY HEROES** (`MyHeroesPressed` → `0134` #3); `0142` MyHeroesPressed; `0141` MyHeroesPressed |
| 3 | `0142` | #3 `UXAllHeroesScreen` → `0x0BA` | **all heroes** grid; OnBegin sends game msg `0x0A4` | `0137` AllHeroesPressed; `0155` button |
| 4 | `0135` | #1 `UXProfileScreen` → `0x0C2` | **player profile / avatar**: PLAYER AVATAR, HEROES, AVATARS, STATS, HIGHEST HERO, EDIT AVATAR, APPLY | `02BA` #30, when the party panel writes a player to `$067e` (§4.4) |
| 5 | `0144` | #5 BooleanSwitch **owned(hero)** → `0141` or #2 `UXHeroSheetScreen` → `0x0BC` | **hero page** | gallery selection (`$03ac` Watch in `0137` → `$0540`); `0142`/`015F` back |
| — | `0141` (in 5) | #3 `0x35851126`(hero GUID) → `0x0B9` | **hero details** (owned hero): MyHeroes / HeroDetails / **ChangeMods** | `0144` when owned |
| 6 | `0143` | #1 UXScreen → `0x0BB` | hero details view (empty grid screen) | `0144`/`0141` HeroDetailsPressed |
| 7 | `0155` | #1 UXScreen → `0x0C3` | **shop** page (one button → ALL HEROES) | main menu **SHOP** (`StorePressed` → `0134` #2) |
| 8 | `015F` | #3 `0x1c214767`(hero, loadout) → `0x0A3` | **hero customize (mods)**; OnBegin action `0x119cc7b4` loads the loadout, PressedEnter → action `0x346caded` (save) | `0141` ChangeModsPressed |
| 9 | `088E` | #1 `0x5e494675` → `0x3B3` | **Custom Servers** browser (§5) | **nothing** |
| 10 | `08CA` | #4 → `0x4BD` / `0x4C3` | **private game lobby**: PRIVATE GAME, VS, Add Player / Add Spectator | custom-game join (23303 → CustomGameType 1/2): `0134` #25, `088E` #7/#9, `046B` #10 |

- **Back navigation** [D]: `0158` pushes the previous page / hero (`$0540`) / player (`$067e`) onto three arrays
  whenever one changes; `0138`'s HOME/BACK bar pops them. That bar is unreachable (§1), so every page carries its
  own back button (`BackPressed`, `MyHeroesPressed`, …).
- **UXLobby state** `0x046B` #30 (`0e78be20`, two outputs `$066e`, `$066d`) has no incoming link [D]; `$066e` / `$066d`
  are read by pages 2/3/4/7 into `$0677` (and `$0676 = -60`), probably the lobby hero-preview camera (not followed).

## 4. Per graph: states, triggers, gates

"Trigger ← source" reads outside-in: the root is a JAM message, a button, native code or a start entry.

### 4.1 `0134` main menu (page 1) [D][G]

| state / node | UI | trigger ← source | gate |
|---|---|---|---|
| #8 main screen `0x0B2` | buttons below | #9 BooleanSwitch false ← Entry | shown unless (party leader **and** CustomGameType == 3) |
| #12 practice-vs-AI setup `0x377` (DIFFICULTY, AI OPPONENTS, MAP, START) | `Loading = $1aa7` | #9 true ← **23303** with type 3 (CustomGameType, `FUN_7ff650d747b0` reads custom-game obj `+0x90`) ← **24001 type 3** sent by PRACTICE VS AI (`CreateBotMatchPressed` → #35 → #13 action `0x2ba7bad2`(3)) | IsPartyLeader (20700) |
| #17 dialog `0x183` "PLAY THE TUTORIAL? / PLAY TUTORIAL / SKIP" | | #8 OnBegin → #5 game msg `0x116` → #27 → #30 IsPlatformWindows → #16 `0x7e8dcef9` | **profile `+0x148 == 0`** = **24300** A.bool false [G] |
| #14 CAIS warning `0x4B9` | | #15 BooleanSwitch `0x4abdb7da` | 20804 play-time tracker enabled (profile `+0x18`) [G] |
| #4 / #5 SendGameMessage `0x115`, `0x116` | | Play / OnBegin | welcome + music ([K] old notes) |

Button → command → effect (`0x0B2` key table [D]; enable conditions = view-model bindings of #8):

| button (text) | command | handler | enabled when (binding) |
|---|---|---|---|
| PLAY | `PlayPressed` | #1 action `0x4b33a20a` = `FUN_7ff650c67220` (matchmaking; **22000**) | `EnablePlayButton = IsPartyLeader && !InCustomGame && !InMatchmaking` |
| CANCEL | `CancelPressed` | #7 `0xc2e4a7f7` = `FUN_7ff650c67810` (**22001**) | `EnableCancelButton = IsPartyLeader && InMatchmaking` |
| PRIVATE GAME | `CustomPlayPressed` | #21 `0x2ba7bad2`(1) (**24001** type 1) → `$1614` → page 10 once in the game | `EnableCustomButton = IsPartyLeader && 0xf3f92e66 && !InMatchmaking && !InCustomGame` |
| Private Game (DEV ONLY) | `DevCustomPlayPressed` | #26 → `0x2ba7bad2`(2) | `EnableDevCustomButton = 0xfedf6743()` (evaluator not decompiled) |
| PRACTICE VS AI | `CreateBotMatchPressed` | #35 → `0x2ba7bad2`(3) | `EnablePracticeButton = IsPartyLeader && !InCustomGame && !InMatchmaking` |
| TUTORIAL | `TutorialPressed` | #19 `0x2ba7bad2`(4) | `EnableTutorialButton = … && !IsInParty` |
| (SHOP: **no button**) | `StorePressed` | #2 `$04f1 = 7` | — |
| (MY HEROES: **no button**) | `MyHeroesPressed` | #3 `$04f1 = 2` | — |
| (cancel join) | `CancelCustomGameJoin` | #38 `0x97bf9643` = `FUN_7ff650bed5b0` (leave custom game) | |
| (PRACTICE: **no button**) | `PracticePressed` | **no handler** in `0134` | |

Measured on the user's screen [U]: PLAY, PRIVATE GAME, PRACTICE VS AI, TUTORIAL; "PLAYER" + portrait top right
(the party panel `0x0C1`); PRESS ENTER TO CHAT; SOCIAL (0 ONLINE). The social panel (FRIENDS / CHANNELS / mail
count, "YOU HAVE NO FRIENDS YET!") is `02BA` #0; right-clicking the own entry gives WHISPER, SEND FRIEND REQUEST,
PROMOTE TO LEADER, REMOVE FROM PARTY, LEAVE PARTY (native context menu).

`0x2ba7bad2` = send `ClientOutLobbyCustomGame` 24001 with the enum type (types 1–4 match the button table in
[early-notes/04-lobby-and-instance-notes.md](early-notes/04-lobby-and-instance-notes.md)) [K][D]. `0xf3f92e66` reads the global byte `DAT_7ff651839e2c` (a console-variable
like flag; its setter is not traced) [G].

### 4.2 `0138` social menu bar (always on the lobby) [D]

| state | UI | trigger ← source | gate |
|---|---|---|---|
| #2 `0x0B5` social button bar (`OnlineFriendCount`, `PendingInviteCount`, `SocialButton` = logical button `0xA1`) | `OpenFriendsPressed` → #20 toggles `$099b` (IsSocialMenuOpen) | Entry #16 | filter `!$1a1b` (`$1a1b` = options menu open, `0278` #29) |
| **#3 `0x0B4` HOME / BACK bar** | `HomePressed` → page 1, `BackPressed` → pop history | **none** (no incoming link; remote-sync node) | **unreachable** |
| #14 SubScript `017D` (purchase) | §4.5 | #13 BooleanSwitch `$054a` ← `0144` `UnlockPressed` | |

Counts: `OnlineFriendCount` = `0xfcf6f7fd` (friends manager `FUN_7ff650c27c00` → `FUN_7ff650cb8e50`),
`PendingInviteCount` = `0x52cd7ccf` (length of the friends manager's `+0xa8` list) [G]: both from 27100.

### 4.3 `0137` / `0142` / `0144` / `0141` / `0143` / `015F` heroes [D][G]

| state | trigger ← source | gate |
|---|---|---|
| `0137` #1 hero gallery | page 2 ← MY HEROES | — |
| `0137` Watch `$03ac` (selected hero) → `$0540`, page 5 | clicking a hero in the gallery (native screen writes `cebf46c8` → `$03ac`) | — |
| `0142` #3 all heroes | page 3 ← `0137` ALL HEROES / `0155` | — |
| `0142` #4 Watch `$03ac` → page 5 | **not wired** (orphan) | clicking a hero in ALL HEROES does nothing [D] |
| `0144` #5 BooleanSwitch | page 5 | `0xd1211491($0540)` = `FUN_7ff650cc7fe0(profile, hero)`: hero GUID in the **owned-product set** (profile `+0x90`, filled by **26401**) |
| `0144` #2 hero sheet `0x0BC` (UnlockPressed, HeroDetailsPressed) | #5 false (not owned) | — |
| `0141` #3 hero details `0x0B9` (MyHeroes / HeroDetails / **ChangeMods**) | #5 true (owned) | **26401** contains the hero |
| `0143` #1 `0x0BB` | page 6 ← HeroDetailsPressed | — |
| `015F` #3 hero customize `0x0A3` | page 8 ← `0141` ChangeModsPressed | owned hero |

### 4.4 `02BA` social panel, party panel, invites, avatar screen [D][G]

Runs from `041B` (not a page). Entries #1/#45/#46 go through `ShowMode()` (false [L]).

| state | UI | trigger ← source | gate |
|---|---|---|---|
| #0 friends list `0x093` (PLAYING OVERWATCH, IN GAMES, BATTLE.NET, "YOU HAVE NO FRIENDS YET!") | InviteFriend, Accept/Decline party invite, tabs | #33 BooleanSwitch `0x54ce444c` ← #62 ← Entry #1 | **friends list loaded** (friends manager `+0x58`, set by **27100**) [G]; true [L] |
| #2 dialog "ADD A FRIEND / SEND REQUEST" | → #4 `UXFriendRequest` (27000) | `InviteFriendPressed` | |
| **#49 party panel `0x0C1`** (LEAVE PARTY; member list) | `EscapePressed` | #47 BooleanSwitch `($099b || $09ba)` ← #64 ← Entry #46 | social menu open **or** a menu page up (`$09ba`, set by `0134`/`0135`/`088E`/`08CA`) |
| #29 Watch `$067e` → #30 **page 4 (avatar/profile screen)** | | the `UXPartyScreen` class writes the clicked member to `$067e` (its output `0x68ea86fe`) | a member in the party panel (20700) |
| #9 / #23 toast area `0x18C` + #10 toast queue | | #22 ← Entry #45 | |
| toasts "FRIEND REQUEST FROM %s" | | Entry #11 game msg **`0x196`** ← `FUN_7ff650bf7230` = **27103** | |
| toasts "JOIN %s'S PARTY?" | | Entry #12 game msg **`0x197`** ← posted by the **20702** handler `FUN_7ff650c0b7a0` (party invite received; corrected by T031, was "20700") | 20702 [H]; cleared by 20703 [H] |
| toasts "%s SUGGESTS %s TO THE PARTY?" (+ Invite/Don't invite) | | Entry #56 game msg **`0x59F`** ← **20701** (`FUN_7ff650c0b400`) | |
| toast "%s INVITED YOU TO THEIR PRIVATE GAME LOBBY." (+ Accept/Decline) | | Entry #65 game msg **`0x5BF`** ← **23302** (`FUN_7ff650bef530`) | |
| `$099b` = 0 | | Entry #15 game msg `0x09B` (loading screen starts, `0147`) | |
| logical buttons `0xA1` / `0xA2` → toggle social menu, tab friends / channels | | Entry #74 | |

The **profile/avatar screen** (`0135` #1, `UXProfileScreen`, runtime vtable RVA `0x15e90c8`, param `$067e`)
shows the profile of the clicked player; its lists (heroes, avatars = `STUUnlock_AvatarPortrait`, highest hero)
are filled by the screen's native class, not by the graph. The candidates are profile `+0x150` (equipped icon)
/ `+0x158` owned account unlocks (24300), the hero records (24900), and stats `+0x208` (27202) [?].

### 4.5 `017D` unlock / purchase (in `0138`) [D]

`$054a` ← `0144` UnlockPressed. Switch `$003b`: 1 → `UXPurchaseScreen 0x0C5` (UnlockPressed → `UXPrepPurchase`,
PressedEsc → `UXCancelPurchase`), 0 → `0x0C9`, 2 → `0x0CA` (confirm → `UXConfirmPurchase`), 3 → `0x0CB`.
Game messages: `0x0A3` → 2 (**26402** status 1 "begin purchase succeeded", `FUN_7ff650c76d00`), `0x0A2` → 3
(**26403** purchase complete), `0x0A1` → close (**26402** status 3 web challenge / `c77459`). Out of scope
(store flow).

### 4.6 `088E` Custom Servers (page 9) [D][G]

| command | action | native | needs |
|---|---|---|---|
| `RefreshCustomGames` (also on start) | `0xefa122bf` | `FUN_7ff650bed270`: clears the custom-game object's game list (`+0x18`, count `+0x20`), sends a lobby request (message vtable `PTR_FUN_7ff6515efac8`) | the reply fills the list: **23301** `[]{u16×4, gameinfo}` (`FUN_7ff650cb4200`, "adds entries to +0x18") [G] |
| `JoinCustomGame` (selected `$1601`) | `0xb90243cd` | index < list count → `FUN_7ff650bed340` (join request) | a listed game; then 23303 → page 10 |
| `LeaveCustomGame` | `0x97bf9643` | `FUN_7ff650bed5b0` (not in a game → 20803-style notice 0x1F "Not in a private game.") | |
| `CustomGameProfile` | page 10 | | |
| `CreateCustomGame` | **no handler** | | |

Nothing sets `$04f1 = 9` (grep over all 370 graphs [D]), so the browser is unreachable in 0.8. With Prometheus
(`start_stop_state`/writing `$04f1`) it can be opened; a server that answers the refresh with 23301 would fill it [?].

### 4.7 Other lobby graphs (short) [D]

| graph | what | triggers |
|---|---|---|
| `0147` | loading screen with map name / flag / random tip (`PopulateTips`, `UXNextMap` `0x0C0`) | game msg `0x09B` show; `0x128` / `0x40B` hide (posted in `FUN_7ff650c9f980`/`c9fa60`: instance map load) [G] |
| `046A` → `0151` | pre-lobby: warnings `0x0BE`/`0x4BB`; login / options / quit `0x027`, login sub-screens driven by `$07f2` and game msgs `0x0FF`, `0x136`, `0x139`, `0x13C` (posted by `FUN_7ff650c656f0`, login status) and `0x138` (error text) | until `$08e3` (native) |
| `0278` | ESC menu `0x028` (RETURN TO GAME, HERO SELECT, MOUSE, KEY BINDINGS, GRAPHICS, GAMEPAD, AUDIO, LEAVE GAME, EXIT) | game msg `0x164` (Escape; posted by action `0x0f4597e7` and `FUN_7ff650c7b8f0`) |
| `02D8` | `UXChatControl` (chat box visibility / mode) | `$1a3c`/`$09ba`/`$099b`/hero select |
| `02DA` | matchmaking status `0x18D` | `InMatchmaking` (25600) and game msgs `0x19F`/`0x1A0` (`FUN_7ff650c678c0`, matchmaking UI refresh) |
| `03F2` | waits around instance connect / map load | game msgs `0x122`, `0x117`, `0x22B` |
| `0B63` | `UXVoiceChatScreen` `0x4C2` | start |
| `0B68` | global data-flow values by front-end context `$1a6e` | stacks in pages set `$1a6e` |
| `0B47` | title scene | [TitleScreen.md](TitleScreen.md) §2 |

## 5. Decoded ConfigVars used as gates [G]

Evaluators are vtable slot 5 + 0x10 (slot 5 is a thunk) of the runtime classes listed in
[StatescriptNodeCatalogue.md](StatescriptNodeCatalogue.md).

| hash | evaluator | returns | lobby data behind it |
|---|---|---|---|
| `1a163481`(m_type) | `FUN_7ff650d747b0` | custom-game type (`FUN_7ff650c29670()+0x90`) == m_type; false with no custom game (`+0x208 == 0` and null ID) | 23303 |
| `7eaf2c13` | `FUN_7ff650d74670` | custom-game `+0x208 != 0` (join pending) | 24001 / 23303 |
| `88dcbae4` | `FUN_7ff650d730d0` | party `+0x28/+0x30` ≠ null ID: a pending party invite | 20702 sets / 20703 clears |
| `4abdb7da` | `FUN_7ff650d78140` | play-time tracker enabled (profile `+0x18`), or a platform value `DAT_7ff6517816e4 == 6` | 20804 |
| `7e8dcef9` | `FUN_7ff650d78540` | profile `+0x148 == 0` ("tutorial not done") | 24300 A.bool |
| `f3f92e66` | `FUN_7ff650d789d0` | global byte `DAT_7ff651839e2c` | client setting (not lobby data) |
| `54ce444c` | `FUN_7ff650d6f0c0` | friends manager `+0x58` (list loaded) | 27100 |
| `fcf6f7fd` / `52cd7ccf` | `…d72ca0` / `…d72dd0` | online friend count / pending-invite count | 27100–27105 |
| `d1211491`(hero) | `FUN_7ff650d6a290` → `FUN_7ff650cc7fe0` | hero GUID in the owned-product set (profile `+0x90`) | 26401 / 26403 / 26404 |
| `e1524276` | `FUN_7ff650d74a90` | first matchmaking ticket state == 6 | 25600 |
| `6f24eb76` | `FUN_7ff650d6df70` | `FUN_7ff650bdce10() == 6` | ? |
| `7a4d3a04` | `FUN_7ff650f3cb50` | two global flags (`DAT_7ff651839db4`, `…dcc`): dev screen `0x4C1` | client flags |
| `29969708` | `FUN_7ff650d78680` | featured hero | TitleScreen.md §3 |

## 6. Game messages the lobby graphs listen for [G][D]

| game msg | posted by | listened in |
|---|---|---|
| `0x096` / `0x097` | 25600 ticket 0→1 / 1→0 (`FUN_7ff650c49a00`); `0x096` also by Play (`c67220`) | `046B` (`$0512` "in matchmaking" for the Play label) |
| `0x098` | `FUN_7ff650c67170` | `046B` |
| `0x09B` | `FUN_7ff650c9f980` (map load start) | `0147`, `02BA` |
| `0x0A1` / `0x0A2` / `0x0A3` | 26402 / 26403 | `017D` |
| `0x0FF`, `0x136`, `0x139`, `0x13C` / `0x138` | `FUN_7ff650c656f0` / `c24ef7` region (login status) | `0151` |
| `0x164` | action `0x0f4597e7`, `FUN_7ff650c7b8f0` | `0278` |
| `0x196` | 27103 friend request | `02BA` |
| `0x197` | 20702 handler (`FUN_7ff650c0b7a0`; corrected by T031) | `02BA` |
| `0x19F` / `0x1A0` | `FUN_7ff650c678c0` (matchmaking UI) | `02DA` |
| `0x599` | inside `FUN_7ff650c5cd…` (lobby connect area) | `046B` #38 (no action linked) |
| `0x59F` | 20701 | `02BA` |
| `0x5BF` | 23302 | `02BA` |
| `0x122`, `0x117`, `0x22B`, `0x128`, `0x40B` | instance / map-load code | `03F2`, `0147` |

## 7. The "custom server" strings [D][G]

| string | GUID | used by |
|---|---|---|
| "Custom Servers", "Manage My Servers", "Private Games" | `0x0DE…1065`, … | screen `0x09A…3B3` (page 9, unreachable) |
| "Banned from custom server %1$s Reason: %2$s" | `0x0DE…E39` | 23306 popup `FUN_7ff650bef7e0` |
| "Custom server not loaded." | `0x0DE…C99` | index 39 of the notice table `DAT_7ff6515a3fb0` (20803 `u8` → popup; also used client-side by `FUN_7ff650c5bdb0(…, idx)`) |
| "Custom server settings updated, will take effect next match." | `0x0DE…E3C` | no reference found in the binary or the screens |

The notice table `DAT_7ff6515a3fb0` (52 entries, 20803 index → text) is listed in
`systems/data/lobby_scripts/notice_table.tsv`.

## 8. What the lobby server can send, and results

| screen / feature | gate | message | status |
|---|---|---|---|
| hero details + change mods (`0141`, `015F`) | hero in owned products | **26401** `{[]u64 products = hero GUIDs, []{u16,f32}}` | **sent** by lobbylogic on connect and by `!products [none]`; not observable (gallery empty) |
| no "PLAY THE TUTORIAL?" dialog | profile `+0x148` | 24300 A.bool = 1 | **sent** on connect; dialog gone [U] |
| hero gallery list | native `0x58d7d94f` class | ? | open |
| party / friend / private-game invites (toasts, dialogs) | game msgs | 20702 (invite received), 20703 (cleared), 20701 (suggestion; flashes), 23302, 27103 | 20702/20703/23302 confirmed [H] (T031, `!msg`); 27103 sent by social.go |
| practice-vs-AI setup page | CustomGameType 3 | 23303 with type 3 after 24001 type 3 | lobbylogic answers Practice with a notice + 23308 today |
| private-game lobby page 10 | CustomGameType 1/2 | 23303 | sent for Custom / Dev custom |
| custom servers list | — | 23301 (page unreachable without Prometheus) | not done |
| avatar / profile screen content | native `UXProfileScreen` | 24300 unlocks / icon, 24900, 27202 (?) | open |

Results in game (2026-09-25):
- 24300 tutorial-done = 1: **the "PLAY THE TUTORIAL?" dialog no longer appears** on lobby arrival [U].
- 26401 all heroes owned: sent [L], but not observable: the hero page is behind the hero gallery, which has no
  menu button and lists no heroes even when forced [U]. The 26401 change stays (harmless, and the right data for
  the owned-hero branch).
- No screen made reachable: every unreachable screen found is gated by the **UI layout or graph wiring** (buttons
  missing from `0x0B2`, no `$04f1 = 9` writer, orphan states, party-panel click not producing `$067e`), not by lobby
  data. Only Prometheus (`execute_node` on the page SetVars, `start_stop_state`) opens them.

## 9. Open questions

- What fills the hero gallery (`0x0137` state `0x58d7d94f`, `LobbyMyHeroesHeroVM`): the class's own vtable slots are
  RTTI helpers (runtime vtable RVA `0x15ec5a0`, slots 1–5 `lea rax; ret`); the list logic sits in the class's other
  bases / view model (compare the party screen's constructor `FUN_7ff650d7fd60`, which installs three vtables), not traced.
- UXPartyScreen (vtable RVA `0x15ea9f0`): slot 36 `FUN_7ff650d85e10` handles a clicked member index (`+0x3ec`) over the
  party list (party `+0xb0`) and calls `FUN_7ff650c5cb70` (the member context menu?); where it writes its output
  `$067e` was not found.
- Who writes `$04d2`, `$08e3`, `$0a0d` (native writers of Statescript variables in the lobby bag).
- `UXProfileScreen` / `UXPartyScreen` native classes: which profile fields they list and when the party screen writes
  `$067e`.
- `0xfedf6743` (EnableDevCustomButton) evaluator (no function at slot 5 + 0x10 in Ghidra), `0x4b0fa643`,
  `0x01e1f3cf` (FocusPlayButton delay condition).
- The lobby request message types behind the vtables `PTR_FUN_7ff6515efac8` (refresh), `…efbe8` (leave),
  `…efb88` (`FUN_7ff650bef430`) and `…f2168` (cancel matchmaking): map to `ClientOutLobbyCustomGame` ids live.
- `0x0DE…E3C` has no user found.
- `stu_dump` does not decode `STUUXString.m_value` in UI screens ("string buffer out of range"): the record at the
  reported offset is `{u32 len, u32 hash, u64, chars}`; `tools/keys.py` works around it.
