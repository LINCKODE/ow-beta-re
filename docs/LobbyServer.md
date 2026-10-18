# Lobby server features

What our lobby logic (`server/internal/lobbylogic`) sends to the 0.8 client, one section per feature:
the messages and their encoding, the chat commands, and what the user saw in game. The message catalogue
(every `ClientInLobby*` message and its handler) is [TitleScreen.md §5](TitleScreen.md#5-lobby-jam-messages-the-client-receives);
which screen each message reaches is [LobbyScripts.md](LobbyScripts.md).

Tags: **[G]** read in Ghidra, **[L]** measured live, **[U]** seen by the user in game, **[?]** inferred.
Chat commands are typed in the lobby chat (`!help` lists them) or run from a shell:
`curl -X POST --data 'command("notice 39")' http://127.0.0.1:3726/eval`.

## Server notices

Three lobby messages show a localized string as a **notice line in the chat area** (not a dialog) **[H]**. Each
handler resolves the string (`FUN_7ff650832ee0`) and posts it through the chat manager (`FUN_7ff650c26e90` →
`FUN_7ff650cc07e0(chat, 0, 0x0AC00000000005D5)` / `cc0e10`; the same UI `/list` replies use) **[G]**:

| message | payload | handler | text |
|---|---|---|---|
| `ClientInLobbyInstance` **20601** | `u64 string` | `FUN_7ff650c461f0` | the string; **nothing shown if it resolves empty** |
| `ClientInLobbyPlayer` **20803** | `u8 index` | `FUN_7ff650c5bdb0` | notice table `DAT_7ff6515a3fb0[index]` (52 entries, `systems/data/lobby_scripts/notice_table.tsv`, not published); **the client does not bounds-check the index** |
| `ClientInLobbyCustomGame` **23307** | `u64 string` | `FUN_7ff650bef8b0` | the string (no empty check) |

String IDs are STU string GUIDs `0x0DE…` (texts: `systems/data/lobby_scripts/strings_enUS.tsv`). Only
pre-made strings can be shown: there is no free-text notice. Free text goes to chat (`SystemChat`, 20400
channel type 2).

**Encoding**: 20803 = 1 byte; 20601 / 23307 = 8 bytes little-endian (`notice.go`: `noticeMessage`,
`stringMessage`, tested in `notice_test.go`).

**Go API** (`notice.go`), for any handler:
- `c.Notice(n Notice)`: 20803; refuses `n >= 52`. Named constants for the useful entries
  (`NoticePrivateGameServerDown` 19, `NoticeUnableToJoinPractice` 15, `NoticeCustomServerNotLoaded` 39,
  `NoticeUnableToAssignInstance` 51, …).
- `c.Popup(stringID)`: 20601. `c.CustomGamePopup(stringID)`: 23307.

Used today: Start in a practice / private game shows notice 19 "Private game server is down." when no game
server is connected, and Tutorial shows notice 15 "Unable to join practice mode" (plus a chat line) (`customgame.go`).

**Chat commands**:
- `!notice <0-51>`: 20803.
- `!popup <string id> [custom]`: 20601, or 23307 with `custom`. Short IDs get the string type:
  `!popup c99` = `0x0DE0000000000C99` "Custom server not loaded.".

**In game** (2026-09-26, title screen) **[H]**: `!notice 19` → "Private game server is down.", `!popup c99`
(20601) and `!popup c99 custom` (23307) → "Custom server not loaded.": all three appear as ordinary chat lines,
with the same style, and no dialog opens. Whether they look different in a match or in a custom-game lobby is untested.

## Player identity (name and icon)

**Startup override (2026-09-26)**: `lobbylogic --name <BattleTag> --icon <name|GUID>` (env `LOGIC_NAME`, `LOGIC_ICON`;
with `make run`: `LOGIC_ARGS="--name Tracr --icon Mei" make run`). The flags override `lobby-identity.json` for
the process without writing it; `!name` / `!icon` still change and persist. `#1234` is added to a name without a
tag; an unknown icon name is a startup error (`!icon list` for the 23 names).


The local player's BattleTag and player icon are set in one place (`identity.go`). They are kept in a JSON
file (`$LOGIC_IDENTITY`, default `server/lobby-identity.json`), so lobbylogic restarts keep them:

```json
{ "name": "Player#1234", "icon": "0x0250000000000306" }
```

`icon` can also be an icon name (`"Mei"`); an invalid file falls back to the default.

The default is `Player#1234` with the Tracer icon `0x0250000000000306`. Before T025 we sent the "Overwatch Logo" icon `0x…2F7`,
which is why the title screen showed "PLAYER" and the logo.

**Where the client reads them**:

| source | stored at | shown by |
|---|---|---|
| **20700** party member `{str tag, bool leader, u8 status, u64 icon, u64, ID player, ID}` | party manager `+0xb0` | the **title-screen party panel** (`UXPartyScreen`, `FUN_7ff650d85700`): for each member the name before `#` (copied into a 64-byte buffer) → UI var `0xfe11d138`, and the icon (member `+0x20`) resolved by `FUN_7ff650d52060` → its `+0x20` → UI var `0x11db7719`; an icon that doesn't resolve sets the rebuild flag (`+0x30`), so the panel rebuilds every frame **[G]** |
| **24300** A.u64 (equipped icon) | profile `+0x150` | no reader found yet: not the party panel **[G]**; candidate: the profile / avatar screen (`UXProfileScreen`, LobbyScripts §4.4) **[?]** |
| **24300** A.list (owned account unlocks) | profile `+0x158`, assets → `+0x188` | 24903 can equip only unlocks loaded in `+0x188` **[G]** |
| **20500** string | profile `+0x70` | the **social panel header** and the **name on the player's own chat lines** (both without `#…`) **[H]**; re-sending 20500 mid-session updates both **[H]** |

So the title screen shows **two names**: the party panel (top right) reads the 20700 member, and the
social panel and chat read 20500 **[G][H]**. The portrait comes from 20700 only; the social panel shows no
portrait of the player **[H]**. No reader of 24300's equipped icon (+0x150) is known.

We send the identity in 20500 (string), 20700 (our member), 24300 (equipped icon + all 23 icons owned)
and in the name we give the game server for the match. Other clients see the same name in relayed General
chat and `/list`: there is one identity per lobbylogic process.

**The 23 player icons** (`STUUnlock_AvatarPortrait` `0x8CDAA871`; names from each unlock's `m_name`,
read with the content reader): `2F7` Overwatch Logo, `2F9` Logo 2, `304` McCree, `305` Mercy, `306` Tracer,
`307` Reaper, `308` Widowmaker, `309` Pharah, `30A` Reinhardt, `30B` Hanzo, `30C` Torbjörn, `30D` Winston,
`30E` Zenyatta, `30F` Symmetra, `310` Bastion, `311` Zarya, `312` Soldier: 76, `313` Lúcio, `314` Roadhog,
`315` Junkrat, `316` Genji, `317` D.Va, `318` Mei (all `0x025000000000xxxx`). The other `0x025…` unlocks
in between are sprays (`0x15720E8A`).

**Encoding**: 24300 = `{bool tutorialDone, u64 icon, u32 n, n × {bool, i32 level, u64 asset, u64}}`,
`{u32 0}`; 20500 = `ID self, cstring` (`identity.go`, tested in `identity_test.go`).

**Chat commands**:
- `!name [BattleTag]`: show or set the name (`#1234` added if missing); re-sends 20500 and 20700. Names
  can't contain spaces (chat arguments split on whitespace).
- `!icon [name|guid|list]`: show, list or set the icon (`!icon mei`, `!icon 318`); re-sends 24300 and 20700.

**In game** (2026-09-26) **[H]**:
- On connect: "PLAYER" with the Tracer portrait (before: the Overwatch Logo).
- `!name Tracr`, sending only 20700: the party panel shows "TRACR", but the social panel header and the chat prefix
  still show "Player". After re-sending 20500 as well, both show "TRACR" / "[Tracr]". Nothing else changed.
- `!icon mei`: the party panel portrait shows Mei. The social panel shows no own portrait.
- Friends' portraits in the social panel are still empty: friend entries (27100/27101/27109) carry no icon.
  The friend portrait is the player-cache record's `+0xa0` (ClientInLobbyRelationship 31804 / 31800
  records, TitleScreen.md §5 **[K]**), which we don't send yet.

## Server-driven navigation: which messages move the client between pages / open dialogs (T031)

**Question**: can the lobby server tell the client which UI page or dialog to show? **Short answer: not
generically.** Live results are tagged [H] (seen by the user) or [L] (Statescript trace, `ss_trace`), 2026-09-25 22:24–22:32. There is no lobby message that sets the page variable (`$04f1`), any other Statescript
variable, or a UI variable. The server can only reach the UI through the few messages whose handlers post a
**game message** that a lobby graph listens for, or that change **lobby data** a graph condition reads. The
table below lists all of them. Measured: the server **can** open page 10 (private game lobby) and the
practice-vs-AI setup with 23303 and return to the main menu with 23308, and it can raise the invite toasts.
Pages 2–9 (hero gallery, heroes, shop, profile, Custom Servers) have no such path.

### Why there is no generic "go to page N" [G][D]

1. **The lobby link only carries `ClientInLobby*` protocols.** The client offers, in its negotiation CRC
   list, only the protocols registered for the peer's server type (13 = lobby). The 16 `ClientInLobby*`
   protocols are all of them (`FUN_7ff650f064a0` registrations; e.g. Chat in `FUN_7ff650c4aff0`:
   `{0xdbe61f10, 0x4fb0, 0x4fb8, type 0xd}`). Every one of their handlers has been read
   ([TitleScreen.md §5](TitleScreen.md#5-lobby-jam-messages-the-client-receives)). None writes a Statescript
   or UI variable, and none carries Statescript sync.
2. **Native code never names the page variables.** A scan of the binary for 64-bit constants
   `0x0D80000000000xxx` finds 45 variable ids used by native code. `$04f1` (page), `$15d8`, `$04d2`,
   `$08e3`, `$0a0d`, `$067e`, `$054a` are not among them. Only graph `SetVar` / `Stack` nodes write `$04f1`.
3. **`$04f1` is a sync var of graph `0x046B`, but that doesn't help.** `0x046B` lists `$15d8, $04f1` in
   `m_syncVars` and `#42` (the `Stack $04f1 = $15d8` = page 10) as its remote sync node. Statescript
   sync reaches only mirrored instances, through the game server's replication
   ([StatescriptRuntime.md §3.4](StatescriptRuntime.md), [MatchLogic.md §9](MatchLogic.md)). The lobby
   entity `0x80000002` belongs to the local lobby EntityAdmin, which has no replication source: there is
   no game-server connection on the title screen, and no lobby message carries sync [G]. All 17 lobby
   instances have **negative instance ids** (`list_loaded_scripts ea=lobby`: −1 … −18), i.e. ids allocated
   by the client (local instances), not mirrored server instances (positive ids) [L].
4. **The `Command` JAM protocol (1000 `{str m_command}`, runs a console command) is not reachable from the
   lobby.** It is registered for server type 0 (`FUN_7ff650884320`, `{0x9c08000f, 1000, 1004, type 0}`) on
   the game's own debug JAM service (`FUN_7ff650882fd0`, for external tools; [early-notes/04](early-notes/04-lobby-and-instance-notes.md)),
   not on the lobby link. A real server couldn't use it either.
5. **The popups are chat lines** (T025 [H]: 20803 / 20601 / 23307 render as chat lines on the title screen). The "popup" messages 20601 / 20803 / 23307 / 27106–27108 / 28002 (and the
   20702 notices) call `FUN_7ff650cc07e0` on the chat manager. That function creates an entry in the chat
   log list (capped at 300 lines, oldest dropped) with the style UI variable `0x0AC…5D5`. None of them is a
   modal dialog [G].

### Message → game message → page / dialog [G][D]

Every native `0x024…` game-message constant in the lobby code range, mapped to the JAM message whose
handler posts it and to the lobby graph that listens (graph texts in `systems/data/lobby_scripts/graphs`).
The rest are posted by client actions (Play `c67220`, find game `c67690`, Esc `c7b8f0`/`d01600`, login
status `c656f0`, map load `c9f980`/`c9fa60`) or by console/job tables (`c45110`, `c67170`, `c9fc20`).

| JAM message (fields) | handler → game msg | listener → effect | in game |
|---|---|---|---|
| **20702** Party `{ID a, ID b, str battletag}` | `FUN_7ff650c0b7a0` registers b's name, sets the party's pending invite (`cba390`: party `+0x28` = b, `+0x38` = a, name, …) → **`0x197`** if b ≠ null; 2 chat notices `0x0DE…43C` / `…43D` | `02BA` #12 → toast **"JOIN %s'S PARTY?"** (invite received) | **toast shown** [H] |
| **20703** Party `{ID}` | `c0a550`: clears the pending invite (party `+0x28` …) | ConfigVar `88dcbae4` (pending invite) false → the invite toast closes | **toast gone** [H] |
| **20701** Party `{ID suggester, str, ID suggested, str}` | `c0b400` registers the suggested player's name, then **returns silently unless the suggester's ID is already in the name cache** (`FUN_7ff650bfac20(FUN_7ff650c281a0()+0x58, …)`, e.g. registered by an earlier 20702); then chat notice `0x0DE…1A50` and **`0x59F`** | `02BA` #56 → #55 → #57 toast **"%s SUGGESTS %s TO THE PARTY?"** | unknown suggester: nothing [H]; known: the toast **flashes for a fraction of a second** [H]. Trace [L]: `0x59F` delivered, vars set, toast `#57` posted. The toast's own logic closes it (not the graph; not the 10000 duration: 23302 uses the same and stays). Lead: `#57` (template `0x900`) vs `#58` (`0x901`, which also queues the player in `$19c1`) |
| **23302** CustomGame `{ID, str, u64[2]}` | `bef530`: registers the name, chat notice `0x0DE…1924`, stores the invite on the custom-game object, → **`0x5BF`** (no gate) | `02BA` #65 → toast **"%s INVITED YOU TO THEIR PRIVATE GAME LOBBY."** | **toast stays**, top centre, (Y) ACCEPT / (N) DECLINE [H]; 23308 does not clear it; opening the practice page replaced it |
| **27103** Friends `{str,str,str,u32,u64,ID,ID}` | `bf7230` → **`0x196`** | `02BA` #11 → toast **"FRIEND REQUEST FROM %s"** | seen before (T023, `!request`) |
| **25600** Matchmaking (ticket 0→1 / 1→0) | `c49a00` → **`0x096`** / **`0x097`**, + `c678c0` → `0x19F`/`0x1A0` | `046B` `$0512` (Play ↔ Cancel label); `02DA` status panel `0x18D` "Searching…" | seen before (`!queue`) |
| **26402** Store status 1 / 3, **26405** | `c76d00` → **`0x0A3`**; `c76d80` / `c773a0` → **`0x0A1`** | `017D` purchase dialog (next step / close), **only while `017D` runs** (after UNLOCK on a hero page, unreachable) | – |
| **26403** Store `{u64}` | `c4a390` → **`0x0A2`** | `017D` "purchase complete" step (same condition) | – |
| **26401** Store | `c774b0` → **`0x0A5`** | no lobby graph listens | – |
| **20600** Instance connect | map load → `0x09B` (show) / `0x128`, `0x40B` (hide), `0x117`, `0x122`, `0x22B` | `0147` loading screen (map, tips); `02BA` closes the social menu; `03F2` connect waits | seen (every `!instance`) |

Transitions through **lobby data** (a graph condition reads what the message stored):

| JAM message | data | graph → effect | in game |
|---|---|---|---|
| **23303** `{gameinfo {str, str, i8 type, u64[2] game, ID}, settings}` with type 1/2 and game ≠ 0 | custom-game obj `+0x70..`, `+0x90` = type, `+0x98` = game | `046B` #41 → `Stack $04f1 = 10`: **page 10, private game lobby** | **page 10 shown** [H]: PRIVATE GAME, 2 × 6 EMPTY slots (ADD PLAYER blue/red), ADD SPECTATOR, START (disabled), BACK. Trace [L]: `#41 → #42`, case 2 aborted, `#37` (`08CA`) begins |
| **23303** with type 3 | same | `0134` #9 → #45 → #12 **practice-vs-AI setup** `0x377` (party leader only) | **shown** [H]: PRACTICE VS AI, AI OPPONENTS = 1, DIFFICULTY = EASY, **MAP empty**, START / CANCEL. The map list must come from another message (candidates 23300 `[]u64, []u64`, 23304; for T028) [?] |
| **23308** | resets the custom-game object | leaves page 10 / the practice setup | **main menu back** [H] (from page 10; trace [L]: `#42` and `08CA` stop, case `#3` = `0134` begins); from the practice setup: sent, not looked at |
| **24300** A.bool = 0 | profile `+0x148` | **"PLAY THE TUTORIAL?"** dialog on every main-menu entry | seen (T023) |
| **20804** `{bool on, u64, u64}` | play-time tracker, profile `+0x18` | CAIS "Healthy Gaming" warning `0x4B9` while the main menu is up (`0134` #14); `c5cf70` also shows threshold notices | **shown** [H] (T030, see "Play-time warnings") |
| **27100** | friends list loaded (`+0x58`) | `02BA` friends panel content | seen |

The type check (`FUN_7ff650d747b0`, ConfigVar `1a163481`) is false while the custom-game object has
`+0x208 == 0` **and** a null game id at `+0x98`. Our `customGameInfo` sends the game id `u64[2] = 0, 0`,
so the dev-custom 23303 we send today never matches type 1/2 [G].

### The profile page: a missing link in the shipped graph [D][G]

Clicking a player (party panel `UXPartyScreen` slot 36 `FUN_7ff650d85e10`, also `cc1f50` and `d9d910`)
calls `FUN_7ff650c5cb70`. That function arms the player-profile object (`FUN_7ff650c29ad0`: pending
`+0x18 = 1`, ID `+0x68`, name), sends a lobby request (message vtable `PTR_FUN_7ff6515f2480`) and posts
game message **`0x599`**. The server's answer is **20805** (profile; accepted only while pending with the
same ID). In `0x046B`, entry `#38` listens for `0x599` and has **no out link**. The next node, `#39
SetVar $04f1 = $04f5` (= page 4, profile), has no incoming link. The link `#38 → #39` that would open the
profile page is missing from the 0.8 data. This explains why clicking the own entry never opens the avatar
screen (LobbyScripts §4.4) [U]. Nothing the server sends can restore it.

### Tutorial / practice map loads

The server can load any map at any time with **20600** (connect to the game server with a map GUID; `!instance [map]`).
This is measured for Temple of Anubis. The Tutorial and Practice buttons only send 24001 type 4 / 3. The
client loads nothing by itself: the map and the game server always come from 20600. So the tutorial is a
matter of the server sending 20600 with the tutorial map and running it (T028 for practice). The tutorial
map's GUID: not looked up [?].

### `!msg`: send any lobby message

`!msg <id> [tokens]` (`msg.go`, tested in `msg_test.go`). The protocol comes from the id; the payload is the
tokens concatenated: bare hex, `u8:`/`i8:`/`u16:`/`i16:`/`u32:`/`i32:`/`n:` (array count)/`u64:`/`f32:`
numbers (Go syntax, `0x…`, negatives), `id:A,B` (two u64), `str:TEXT` (cstring, `\s` = space). Remember
that consecutive bool fields pack into one byte (bit 0 first).

**A payload the client can't decode drops the lobby link** [L]: `!msg 20400` with an empty payload
(20400 needs `{i32, ID}, {str, ID}, str`) made the client close the connection at once (gateway
"client disconnected session=15", 2026-09-25 22:12:24); the client stayed at the title screen but had to log
in again. So `!msg` refuses an empty payload unless you write `empty`, and it only sends `ClientInLobby*`
messages. Check the fields in `game/owbetaclient/Prometheus/jam_protocols.json` first. Examples:

```
!msg 20702 id:0,0 id:0x100,0x200 str:Friend#1234      # join-party toast
!msg 20703 id:0,0                                      # clear the pending invite
!msg 20701 id:0x100,0x200 str:Friend#1234 id:3,4 str:Bob#2  # suggestion (suggester must be known: after the 20702)
!msg 23302 id:0x100,0x200 str:Friend#1234 u64:0x10 u64:0x20  # private-game invite toast
!msg 23303 str:Test str: u8:1 u64:1 u64:1 id:7,7 <190 hex zeros>  # page 10 (type 3: practice setup)
!msg 23308 u64:0 u64:0                                 # back to the main menu
```

## Practice vs AI and Private Game (T028)

Map choice (all maps, the dropdown's 24008, START with the chosen map): [Maps.md](Maps.md) §5 (T035).

PRACTICE VS AI and PRIVATE GAME lead into a match on our game server without chat commands **[H]**: practice
setup page → START → Temple of Anubis; private game lobby (page 10) → START → Temple of Anubis; Esc → LEAVE
GAME → back in the lobby. Code: `customgame.go` (handlers 24001 / 24007 / 24008 / 24013), `matchmaking.go`
(`onLeaveGame`), tests in `customgame_test.go`.

### Message sequence [L]

Recorded 2026-09-26 (lobby log). `cg` = the client's custom-game object `FUN_7ff650c29670()`.

```
client                                   server
PRACTICE VS AI / PRIVATE GAME
  24001 {u8 type, str "", str ""}   ──▶   type 3 = practice, 1 = private, 2 = dev private
                                    ◀──   23300 {[]u64 custom maps, []u64 practice maps}  (MAP picker)
                                    ◀──   23303 {gameinfo(type, game id), settings}       (page opens)
                                    ◀──   23304 {lobby state: game id, slots}             (START works)
START (practice)
  24008 {settings, start bit set}   ──▶   (only when the UI changed the settings)
  24013 {u8 1, ID game}             ──▶
                                    ◀──   23305 {settings}  (answer to 24008: the client's two copies agree)
                                    ◀──   20600 {map, host, port, conn, key}  (as !instance)
START (page 10, settings unchanged)
  24013 {u8 1, ID game}             ──▶   ◀── 20600
CANCEL / BACK (not in a game)             local reset only (FUN_7ff650bed5b0), nothing sent
in the match (~12-18 s after START, cause unknown)
  24007 {}                          ──▶   ◀── 23308 {u64[2] game}  (reset cg)
Esc → LEAVE GAME
  21801 {}                          ──▶   game server sends FIN; ◀── 23308
```

Practice 24008 as sent by the client (1 opponent, Easy, Temple of Anubis), 116 bytes: `01` (start bit) … bot count
`1` in the third 5×i32 slot … bots `[1] {00, 01000000, 0x0500000000000079}` … map mode `1`, maps `[1] 0x080000000000005B`.
With 6 opponents it is 181 bytes (6 bots). START sends 24013 **right after** 24008 in the same frame.

### Encodings [G][L]

All little-endian; cstrings are NUL-terminated; `ID` = two u64; `u64[2]` is a fixed array (no count); `[]` = u32
count + elements. Field layouts from `jam_protocols.json`, meanings from the client code named.

- **gameinfo** (`cg +0x70`, copied by `FUN_7ff650f9bf90`): `{str name, str, u8 type (+0x90), u64[2] game (+0x98),
  ID owner}`. The type checks (`FUN_7ff650d747b0`, ConfigVar `1a163481`) are false while `+0x208 == 0` and the
  game id is null, so **the game id must be non-zero** or no page opens. We send `{7, n}` (n unique per game) and
  the player's JamID as owner.
- **settings** (0xC8 bytes in memory; `+0xB8` = server copy, `+0x210` = the copy the UI edits):

  | wire | memory | meaning (setter) |
  |---|---|---|
  | 1 byte: 6 bools | `+0x00` | bit 0 = start (`FUN_7ff650beebf0`) |
  | `{i32}` | `+0x08` | ? |
  | `{bool, i32, i32}` | `+0x0C` | ? |
  | `{5 × i32}` | `+0x18` | [1] = AI opponent count (`FUN_7ff650beeb40`) |
  | `u64[2]` | `+0x30` | ? |
  | `{i32, i32, []u64}` | `+0x40` | ? |
  | `{[]{u8 difficulty, i32 (1), u64 hero}}` | `+0x60` | AI players (`FUN_7ff650beed50`; cleared by `beeca0`) |
  | `{4 × i32, []u64 maps}` | `+0x78` | [0] = 1 once a map is set; maps = the chosen map (`FUN_7ff650bedf90`) |
  | `{4 bools, i32, []ID}` | `+0xA0` | ? |

- **23300**: two `[]u64` → `cg +0x40` (MAP choices for types 1/2) and `+0x58` (type 3). The picker
  (`FUN_7ff650d8d9e0`) maps each id to its `0x079…` map header and skips `0x08000000000000D4`. We offer
  every playable map of the catalogue in both, Anubis first (T035: [Maps.md](Maps.md) §5).
- **23304** lobby state (`cg +0x180`, `FUN_7ff650f9c550`): `{1 byte (4 bools), u8, 5 × i32, u64, ID game, {i32,
  i32, []u64}, {[]{u8, i32, u64}}, []slot}`. The game id at `+0x1A8` is what START sends back; while it is null,
  START only shows notice 0x1F "Not in a private game." (`FUN_7ff650bef430`).
- **slot** (0x38 bytes; page-10 filler `FUN_7ff650d935d0`): `{str name, bool, u8 kind, u8, i32 team, u64 bot,
  ID player}`. `bool` picks the status image (`0x0AC…90D` set / `…90E` clear); `kind` 1 = player in a team
  column (anything else: spectator column); `team` 0 / 1 (2 = spectator); `bot` ≠ 0 = AI player; `player` is
  compared with the party members (the INVITE dialog `FUN_7ff650d8ee30` lists party members not in a slot). We
  send one slot: the local player, kind 1, team 0.
- **24013** `{u8 (1), ID game}`: sent by `FUN_7ff650bef430` (Statescript action `0x2515c992`: practice
  `0134` #20 after START, page 10 `08CA` Start).
- **24008**: settings, sent by `FUN_7ff650beee60` only when `+0x210` differs from `+0xB8`.
- **23308** `u64[2]`: resets `cg` (`FUN_7ff650cb42c0`): the practice page / page 10 close.

### What START does in the client [G]

Practice START = command `CustomGameEnterInstance` → action `0x4f961c19` (execute at `0x7ff650d8d020`, not a
function in Ghidra; read with objdump): reads `AI OPPONENTS` (0–6), the map GUID and `DIFFICULTY` from its
ConfigVars, sets the map (`bedf90`), clears the bots (`beeca0`), adds N bots cycling through heroes
`0x0500000000000079`, `…065`, `…03D`, … (`beed50`), sets the count (`beeb40`), sets the start bit (`beebf0`),
sends the settings if changed (`beee60` → 24008); then action `0x2515c992` → `bef430` → 24013, and `$1aa7`
(Loading) shows "JOINING PRACTICE VS. AI" until 20600 arrives. The server does nothing with the bots (AI is out of
scope).

### In game [H]

- PRACTICE VS AI → setup page: AI OPPONENTS 1, DIFFICULTY EASY, MAP TEMPLE OF ANUBIS, START / CANCEL; START →
  "JOINING PRACTICE VS. AI" → Temple of Anubis on our game server; Esc → LEAVE GAME → back in the lobby.
- Without 23300 the MAP dropdown is empty (T031's probe); without 24013 handled, START stays on "JOINING".
- LEAVE GAME returned the client to the lobby at once in 3 of 4 runs. Once it stayed in the match for a while
  after acknowledging the game server's FIN. The server's answer was the same each time [H][L].
- PRIVATE GAME → page 10: Temple of Anubis preview image and MAP dropdown on the left, "TRACR" in blue slot 1,
  the other slots EMPTY, **START enabled**; START → Temple of Anubis on our game server.
- Page 10 empty (T031's probe, no 23304): 6 + 6 EMPTY team slots, 2 × 2 spectator slots, START disabled;
  ADD PLAYER opens "INVITE A PLAYER" with the party's members.

### Open

- The INVITE request (ADD PLAYER → INVITE) and the slot dropdowns' requests are not mapped (the candidates are
  24002–24006, 24009–24012, 24014 by their fields).
- Tutorial (24001 type 4) still answers notice 15: its map GUID is not known.
- In the match the client sends **24007** (empty) 15–18 s after START, long before LEAVE GAME (four runs:
  +12 s, +15 s, +17 s, +18 s) [L]. We answer 23308, which resets `cg` mid-match without visible effect. What sends it
  (the "JOINING" dialog's CANCEL, or `FUN_7ff650bed5b0` once the instance state reaches 4) is not known.

## Play-time warnings (T030)

The client has a play-time tracker ("CAIS": the Chinese anti-addiction system's "healthy gaming" warnings).
The server switches it on with **ClientInLobbyPlayer 20804**. It is off by default: we only send it on command.

### Encoding and client state [G][L]

20804 = `{bool enabled, u64 remaining, u64 played}` (17 bytes, times in **milliseconds**). The handler (inline in the
Player dispatcher `FUN_7ff650c497f0`) writes the profile (`FUN_7ff650c24f80`; found live at the object whose `+0x70`
is the BattleTag):

| profile | field |
|---|---|
| `+0x18` | bool enabled |
| `+0x20` | u64 remaining ms (counts down, floored at 0) |
| `+0x28` | u64 played ms (counts up) |
| `+0x30`, `+0x48` | two warning records `{bool fired, u64 threshold ms, string}`, filled by the client: **3 h** (10800000, `0x0DE…16BF` "3 Hour CAIS warning goes here.") and **5 h** (18000000, `0x0DE…16C0` "5 Hour CAIS warning goes here.") [L] |

20804 re-arms (`fired = 0`) every record whose threshold is above the new `played`. While enabled, `FUN_7ff650c5cf70`
adds each frame's time (s × 1000, constant `0x162d2e8` = 1000.0 [L]) to played, subtracts it from remaining, and
shows a popup (UI `0x0AC…5D5`) with a record's string once played passes its threshold. Measured: after
`!playtime on 0 1` the profile read `+0x18 = 1`, remaining 3586 s, played 13.6 s [L].

### What the user sees [G][H]

The main menu (graph `0134`) runs BooleanSwitch #15 on ConfigVar `0x4abdb7da` (`FUN_7ff650d78140`: the profile's
`+0x18` when `[DAT_7ff651797f90+0x170]+0x94 == 4`, measured 4 at the title screen [L]; otherwise
`DAT_7ff6517816e4 == 6`, a platform value). True shows the CAIS screen `0x09A…4B9` (#14). The screen has no button
and stays up as long as the flag is set; the switch reacts at once, no menu re-entry is needed.
**The user saw it** with the beta's placeholder text "CAIS Healthy Gaming Warning message goes here. …"
(`0x0DE…1610`) [H]. The 3 h / 5 h threshold popups were not tried.

### Chat command

`!playtime on [played h] [remaining h]` (defaults 0 and 24 h; also remembered for `sendPlayTime`, see below),
`!playtime off`, `!playtime warn [s]` (on for s seconds, default 10, then off: the warning shown once).
Code: `playtime.go`. `sendPlayTime(c)` sends the configured tracker; it is not in the connect sequence yet
(`handlers.go` belonged to another task).

## Matchmaking: Play → Searching → Match Found → in a match (T030)

PLAY (`ClientOutLobbyMatchmaking` **22000** `{{u64, u64}, bool}`, all zero) starts a simulated search; after
8 s the client is sent to our game server with the same hand-off as `!instance` and custom-game START
(`sendToGame` → 20600, Temple of Anubis). CANCEL (**22001**, empty) stops it. Code: `matchmaking.go`.

### The panel's states [G]

Every **25600** (layout in `matchmaking.go`, `queueStatus`) updates the ticket and refreshes the matchmaking view
model (`FUN_7ff650c67930`; VM getter `FUN_7ff650c25b70`). VM `+0x20` picks the text of the status panel
(screen `0x09A…18D`, graph `02DA`, filler `FUN_7ff650d01dc0`). "Game info" is the ticket's last array
(`{3 bools, i8, i8, u32, 4 × i32, u32[], u32[], u32, u32, ID}`, record `+0x78`/`+0x80`):

| ticket status | game info[0] | VM state | panel (UI var → string) |
|---|---|---|---|
| no ticket, request pending (`+0x33`) | – | 1 | Searching (no stats) |
| 0 | – | 1 | Searching (no stats) |
| 1–4 | – | 1 | `MatchmakingSearching` "Searching" + the queue stats |
| 5 | none | 2, or **3** when this refresh comes ≥ 1700 ms after the first status-5 refresh | `MatchmakingMatchFound` "Match Found" / `MatchmakingEnteringMatch` "Entering Match" |
| 5 | `+0x14 ≥ 0` | 6 | `MatchmakingReinstancing` "Reinstancing" |
| 5 | `+0x18 ≥ 0` | 5 | `MatchmakingRebalancing` "Rebalancing" |
| any | byte `+4 == 6` | 1 | Searching (also ConfigVar `e1524276`) |
| 6, 7, 8 | – | ticket removed (1 → 0 tickets: game msg `0x097`) | panel gone, PLAY enabled |
| other | – | 0 | no panel |

Entering and leaving states 5/6 posts game messages `0x19F` / `0x1A0` (`FUN_7ff650c678c0`), which set `$09f0` in
`02DA` (show the panel even outside the main menu). The client has no clock for the panel: "Entering Match" only
appears on a refresh, so the server sends status 5 twice. The queue stats (first element of the stats array):
bucket byte 1 = `MatchmakingSearchBucketTypeNewbie` ("FINDING A GOOD MATCH"), else `…Normal`; found / needed
("N OF 12 PLAYERS FOUND"); fill / 4 = `MatchmakingSearchExpansionLevel0–6` (the hexagon images); the two
f32s are not read by the filler [G]. No elapsed or estimated-time field was found in the VM filler [G]; the user
didn't notice a timer [H].

### Message sequence [L]

```
22000 PLAY
25600 status 1, stats found 1/12 level 0      "Searching"
25600 status 1, every second: found rises to 12, level to 6
25600 status 5 (after 8 s)                    "Match Found"
25600 status 5 (+2 s)                         "Entering Match"
20600 (+2 s)                                  map load → Temple of Anubis on our game server
25600 status 6                                ticket removed
22001 CANCEL (before the hand-off) → 25600 status 6, search stopped
```

Timing is in `mmSearchTime` / `mmFoundTime` / `mmEnterTime`. `!queue` / `!unqueue` run the same handlers.

### In game [H]

PLAY → Searching → Anubis on our game server after about 8 s; LEAVE GAME → main menu; PLAY then CANCEL →
the panel closed cleanly and PLAY was enabled again. (The user's report didn't mention the intermediate
Match Found / Entering Match texts separately.)


## Title hero and cosmetics (T026)

What the featured title-screen hero is and wears, from `ClientInLobbyHeroProgression` (24900 records, 24902, 24903).
Code: `server/internal/lobbylogic/cosmetics.go` (state, commands) and `progression.go` (24900). Tables:
`herounlocks_gen.go`, generated by `systems/data/lobby_scripts/tools/hero_unlocks.py` together with
`unlocks.tsv`, `hero_unlocks.tsv`, `hero_skins.tsv` in `systems/data/lobby_scripts/` (not published: regenerate with that tool).

### What 0.8 ships [D]

- **21 heroes**, each with **one skin** (`STUHero.m_skins`). Each skin has **4 variants** (`STUSkin.m_skinThemes` →
  `STUSkinTheme`, `m_slot` 1–4, named by colour, e.g. Mei Blue / Pink / Red / Green, Lúcio Green / Orange / Purple / White).
- **320 unlocks** (`0x025…`, type 0x0A5): 211 `SprayPaint`, 42 `SkinTheme`, 42 `POTGAnimation` (highlight intros),
  23 `AvatarPortrait` (player icons), 2 `PortraitFrame`. There are **no** `HeroMod`, `Trophy` or class `0x5F38A9D5`
  unlocks, so record `+0x30..+0x40`, `+0x58` and `+0x60` can't be filled with anything real.
- Per hero, `m_heroProgression` (`STUProgressionUnlocks`, 11 level groups) lists 17 unlocks: SkinTheme "BASE SLOT"
  (slot 1) and "SLOT 1" (slot 2), POTG "DEFAULT" plus one named intro, the hero's player icon, ~10 sprays and both
  portrait frames (shared by all heroes). So only **variants 1 and 2** of each skin can be equipped; 3 and 4 have no unlock.
- SkinTheme unlock field `0xbd085e06` (`+0x38` at runtime) = the variant slot it selects.
- STU type hashes are **CRC32 of the lower-cased name** (`stuunlock_spraypaint` = `15720E8A`, `stuunlock_skintheme` =
  `8B9DEB02`). `0x5F38A9D5` matched none of ~90 guessed names (emote, voiceline, pose, victorypose, …): still unnamed.
  The client builds a UI view model for it like the other classes (`FUN_7ff650d10430` → `FUN_7ff650aae130`) [G].

### The featured hero: fixed per client process [G][L]

- Profile `+0x230`, read by ConfigVar `0x29969708` (`FUN_7ff650d78680`). **Only two writers**: the profile constructor
  `FUN_7ff650cc77e0` (0) and the picker `FUN_7ff650cc93b0`, which the ConfigVar calls while `+0x230 == 0`. 24900 doesn't
  reset it, and neither does the lobby-disconnect reset `FUN_7ff650cc7d60` (it clears `+0x148/+0x150`, the maps, …
  but not `+0x230`). A scan of every `mov [reg+0x230], …` in the lobby code range finds no other writer to this object.
- Picker: collects the records of the `+0xF8` map; **candidates = records with `+0x10 != 0` or level > 1**; picks at
  random among the candidates (or among all records if there are none). With an empty map it picks nothing and retries later.
- Live (2026-09-25): profile at `0x1be13e00`, `+0x230 = 0x02E…79` (Lúcio).
- So the server **can't change the title hero live**; it can decide the next pick. With a featured hero set, our 24900
  gives only that hero `+0x10 = 500` and its level; every other record gets level 1 and `+0x10 = 0`. At the next client
  launch that hero is the only candidate.

### What the hero wears [G][L][H]

The title map's graph `0x058…B4A` builds a `StatescriptStateCosmeticEntity` with `m_skin = ConfigVar 0x8c92c925` =
`FUN_7ff650cc8d20(profile, hero, equipped skin)`:
1. find the record's `+0x68` entry `{ctx, theme}` with **ctx == the equipped skin** (record `+0x20`);
2. slot = that theme unlock's `+0x38` (default **1** without an entry);
3. return the skin's `STUSkinTheme` variant whose `+0xa0` (`m_slot`) equals the slot.

Record `+0x28` (theme equipped with ctx 0) is **not** read here. 24903 `{hero, ctx, unlock}` (`FUN_7ff650cc8540`) needs
the unlock in the loaded-unlock map (profile `+0x188`); by class it writes:

| class | written to | notes |
|---|---|---|
| SkinTheme | ctx 0 → `+0x28`; ctx ≠ 0 → `+0x68` entry for ctx (replaced, or appended) | the renderer reads `+0x68` with ctx = skin. Sending the theme that is already equipped appends a duplicate entry (client quirk, harmless) |
| HeroMod | `+0x30/+0x38/+0x40` by the unlock's int at `+0x58` (0–2) | none in 0.8 |
| POTGAnimation | `+0x48` | |
| SprayPaint | `+0x50` | |
| `0x5F38A9D5` | `+0x58` | none in 0.8 |
| Trophy | `+0x60` | none in 0.8 |
| AvatarPortrait | profile `+0x150` (not per hero; 24300's icon) | |
| PortraitFrame | ignored | |

**Live rebuild [H]**: the CosmeticEntity reads the variant only when it is built, so a theme change alone doesn't show.
But the graph gates the entity on `IsLoaded(EquippedSkin(F))`, and 24902 `{hero, skin}` sets record `+0x20` live:
- 24902 `{Lúcio, 0}` removed the title hero (the name text stayed);
- 24902 `{Lúcio, skin}` brought him back **in the newly equipped variant** (orange).

`!skin` therefore sends 24903 (ctx = skin, and ctx 0), then 24902 `{hero, 0}`, waits 0.5 s, then 24902 `{hero, skin}`.

### Messages we send

- **24900 on connect** (and `!level`, `!hero`), per record: `{u8 0, u32 level, u64 hero, u64 xp, u64 xpNext, u64 skin,
  u64 equipped[8] (+0x28..+0x60), u32 n + n × {u64 skin, u64 theme} (+0x68, the equipped theme), u32 n + n × unlock (+0x80)}`,
  unlock = `{bool 0, i32 0, u64 asset, u64 0}`. **Every record owns all 17 of its hero's unlocks**: that loads them into
  `+0x188` (`FUN_7ff650cc80d0`), which 24903 requires. Reward lists as before (player icons per level).
- **24903** `{u64 hero, u64 ctx, u64 unlock}` and **24902** `{u64 hero, u64 skin}` from the commands.

The loadout is persisted in `$LOGIC_COSMETICS` (default `server/lobby-cosmetics.json`), by hero name:
```json
{ "featured": "Mei", "heroes": { "Mei": { "theme": "0x025000000000011e" }, "Lúcio": { "intro": "0x0250000000000147", "spray": "0x025000000000018f" } } }
```

### Chat commands

- `!hero [name|random|list]`: show / set the featured hero (applies at the next client launch; re-sends 24900).
- `!skin <hero> [1|2|variant]`: without a variant, lists the 4 variants and the ones that have an unlock; with one,
  equips it (`!skin mei pink`, `!skin lucio 2`) and rebuilds the title hero live.
- `!equip <hero> <unlock>`: equips an intro / spray / theme by name, short or full GUID, `class:name` when names clash
  (`!equip lucio sound barrier`, `!equip lucio spray:icon`, `!equip mei 156`).
- `!unlocks <hero>`: lists the hero's unlocks by class (`*` = equipped).
- Hero names ignore case, accents and punctuation, and a unique prefix is enough (`lucio`, `soldier76`, `torb`, `dva`).

### In game (2026-09-25)

- `!skin lucio 2` (24903 only): the live record took it (`+0x28`, `+0x68` = 1 entry) [L], but Lúcio stayed green [H, weak].
- 24902 `{Lúcio, 0}` → Lúcio disappeared, name text stayed [H]; 24902 `{Lúcio, skin}` → back, **orange** [H].
- `!skin lucio green` (the one-command version) → Lúcio flickered and came back **green** [H].
- `!equip lucio sound barrier` / `spray:icon`: record `+0x48 = 0x…147`, `+0x50 = 0x…18F` [L]. Nothing on the title screen
  shows intros or sprays; the hero customize page (page 8) is unreachable (LobbyScripts §3).
- `!hero mei` + `!skin mei pink`, then a client relaunch: profile `+0x230 = 0x02E…DD` (Mei) [L]; the title screen shows
  **Mei in pink** [H]. The featured hero and the loadout persist across client launches and lobbylogic restarts.

## Game server availability: the title screen's match buttons (T058)

The main menu's PLAY, PRIVATE GAME, PRACTICE VS AI and TUTORIAL follow whether the active game server
(`--game-server` / `!gameserver`) is linked to lobbylogic: greyed while it is not, enabled while it is, in every
connected client, without a client restart.

### The lever: the party state 20700 [L][U]
The four buttons' enable bindings on screen `0x0B2` ([LobbyScripts.md](LobbyScripts.md) §4.1) depend on the party
state. Stage 0 of T058 (2026-09-27, client in the main menu, sent through the gateway's Lua `send()`):

| sent | payload (hex) | what the user saw |
|---|---|---|
| no party: null party ID, 0 members | `00…00` (16) `00000000` | all four buttons greyed; the player's name and icon at the top right disappear |
| party of one (lobbylogic's usual) | `0100000000000000 0200000000000000` `01000000` + member (`Tracr#1234\0`, leader 1, status 5, icon `0x0250000000000318`, 0, JamID ×2) | all four back, name and icon back |

So the client re-evaluates the bindings whenever a 20700 arrives (the view-model values are re-pushed on change,
UISystem.md), and **TUTORIAL needs the party too**: the `!IsInParty` reading of its condition in LobbyScripts.md is
wrong or incomplete (a party of one enables it, no party disables it) [U]. PRIVATE GAME's extra flag `0xf3f92e66`
was set in this client (it came back with the others). Clicking TUTORIAL afterwards crashed the client, for an
unrelated reason (the instance created an assault brain on the tutorial map; InstanceServer.md §12.14).

Try it by hand:
```
curl -X POST --data 'send(nil, 20700, unhex("00000000000000000000000000000000 00000000"))' http://127.0.0.1:3726/eval
```

### What lobbylogic does [code]
- `Instances` (instances.go) tracks the availability of the active server: **online** is reported as soon as it
  links (hello), **offline** only after `--offline-after` (default 2 s) without it, so a rebuild (the watcher starts
  the new binary right after stopping the old; the new one links ~1 s later) doesn't flicker. Changing the active
  server (`!gameserver name`) re-evaluates it. After a lobbylogic start the first report waits for a server to
  link, at most `--offline-after`.
- On each report, `Logic.AvailabilityChanged` (availability.go) sends every connected client its party state and a
  system chat line (`Game server "instance" is online.` / `… is offline: PLAY, … are disabled until it is back.`).
- `sendParty` (social.go; also used at login, on resync and by the party commands) sends the real party only
  while the server is available, the empty party while it is not, and nothing while that isn't known yet (the
  first report sends it). Sessions that log in while no server is linked start greyed.
- A dead server is noticed by the TCP link closing; a hung one by the gamelink heartbeat (InstanceServer.md
  "Lobbylogic link"): lobbylogic drops a server that announced pings after 3 intervals (6 s) of silence.
- Side effect: without a game server the party panel is empty too (the same message carries both); party
  invites etc. only show once a server is back.

## Choosing the game server from the social panel  (2026-09-27)  [code; confirmed in game by the user]
lobbylogic always shows two incoming friend requests, **R&D#0001** (→ `gameserver`, the R&D server) and
**GAME#0001** (→ `instance`). They are sent in the login friends state (27100 list 2) and on resync, in place of the
old Tracer demo request (`server/internal/lobbylogic/serverchoice.go`).
- Accept (27001 action 0): new matches go to that server (`Instances.SetActive`); a chat line confirms; the choice is
  saved to `server/game-server.json` (`{"game_server": "..."}`, `--game-server-file`). `!gameserver <name>` saves too.
- Accept or decline: lobbylogic removes the request (27104) and re-sends it (27103), so both stay in the list.
- On start a saved choice overrides `--game-server`, whose default is now `instance`.
- Availability (the menu buttons) follows the active server as before: switching to a server that isn't linked greys
  the buttons out after the debounce.

## Gateway logging and the quiet list  (2026-09-29)
The gateway logs every message it forwards (`recv` / `send` / `inject`) with the payload as hex (`internal/lobby/hub.go`),
a reverse-engineering aid. High-rate streams would flood the log (T072's server view, `PrometheusServerView` 32002 at
62.5/s ≈ 1 GB/h), so `--quiet-msgs` (default `32002`) lists message ids that are logged only as one summary line per id
every 10 s (`send (quiet summary)`: messages, bytes, per second). They still go to the `/traffic` ring as usual.
