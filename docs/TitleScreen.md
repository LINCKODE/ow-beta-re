# The title screen: lobby scene, featured hero, lobby JAM messages

How the Overwatch beta 0.8 client (build 24919) builds its title-screen 3D scene (background map, camera,
the featured hero), what it needs from the lobby server for it, and what each lobby JAM message does on
receipt.

**Addresses**: Ghidra addresses (`0x7ff64ffe0000 + RVA`) unless marked RVA.
**Tags**: [G] read in code, [D] read from the game data offline (`systems/content`, `systems/stu`),
[L] measured live, [U] confirmed by the user in game, [K] from [early-notes/](early-notes/README.md) or lobbylogic comments, [?] inferred.

Related: [ECS_Components.md](ECS_Components.md) `0x54`, [ECS_Systems.md](ECS_Systems.md)
LobbyBackgroundSceneSystem, [StatescriptNodeCatalogue.md](StatescriptNodeCatalogue.md),
[ContentAccess.md](ContentAccess.md) (keys), [JAM.md](JAM.md) §12.5 (lobby channels), [early-notes/04-lobby-and-instance-notes.md](early-notes/04-lobby-and-instance-notes.md).

---

## 1. Summary

- The title-screen scene is a **small map loaded into the lobby's own world** (component `0x54`), started by
  lobby Statescript graph `0x058…B47`, not by a JAM message. [G][D]
- The hero standing in it is spawned by the title map's own graph `0x058…B4A` as a **cosmetic entity**:
  the "featured hero" (picked at random from the hero records of `ClientInLobbyHeroProgression` 24900) in
  its **equipped skin**. The hero only appears when that skin is set: the 24900 hero record's u64 at
  `+0x20`. [G][D]
- Our lobby logic sent 0 there, so the scene was empty. Sending each hero's skin (`STUHero.m_skins`) makes the
  hero appear: **confirmed in game (Winston), 2026-09-25**. [U]
- `ClientInLobbyConnect` 20503 is **not** "data packs": it delivers TACT decryption keys (key name + 16-byte
  key) into the client keyring, then loads the lobby asset package. Prometheus already injects all nine keys,
  so an empty 20503 is enough for us. [G]

## 2. The scene chain

```
lobby EA: LobbyFrontendStartupSystem (#19) spawns def 0x0400…01F1, starts graphs 0x058…41A, 0x058…41B
  0x41B ──▶ 0x058…46B (UXLobby) ──▶ 0x058…B47 (title scene)
     Entry → SetVar(var 0x0D8…, [map 0x080…677, 0x080…664, 0x080…675])
           → StateLobbyWorld(m_map = expression over that var) ── "loaded" ──▶ StatePlacedCamera ⊃ state b0054407
                 │ OnEnter: 0x54+0x30 = map, create world (0x54+0x18)
                 ▼
  title map 0x080…677 (package 06E00000570676D3) places:
     0x040…12B3  camera: STUTargetTagComponent (tag 0x0D6…2A4) + STUCameraComponent
     0x040…12B4  STUStatescriptComponent → graph 0x058…B4A (featured hero, §4)
```

### 2.1 Component `0x54` (LobbyMap)  [G]

Lobby-EA singleton; accessor `FUN_7ff650c25400(lobbyEA)` (lobby EA global `DAT_7ff651813e18`). Fields:

| offset | meaning | written by |
|---|---|---|
| `+0x18` | the scene world (render world + map) | `FUN_7ff650cc6ef0` (create), `FUN_7ff650cc74a0` (destroy) |
| `+0x20` | helper object (0xa0 bytes) bound to the world | create/destroy |
| `+0x28` | nested EntityAdmin ticked by LobbyBackgroundSceneSystem | (not traced; passed into world creation) |
| `+0x30` | map GUID to load | LobbyWorld state OnEnter |
| `+0x38`, `+0x3c` | two entity ids created with the world (scene entity, camera/second entity) | create |
| `+0x48` | resource handle, `+0x50`/`+0x54` two ints, `+0x58` flag | LobbyBackgroundSceneSystem interface slots `FUN_7ff650c65260` (set) / `FUN_7ff650c65310` (clear) |
| `+0x59` | set to 1 by the 20503 handler | `FUN_7ff650c5f090` |

- **LobbyBackgroundSceneSystem** (lobby system 0, vtable `0x7ff6515a4440`) ticks the nested EA and the world
  (`FUN_7ff650c64940`); its extra interface slots return the world (`c649b0`, `c64e70`) and set/clear the
  `+0x48` handle (`c65260`, `c65310`).
- **World creation** `FUN_7ff650cc6ef0(comp)`: only when `DAT_7ff6518382ec < 6`, no world yet and `+0x30 != 0`.
  It creates the world for the map (`FUN_7ff6509b72e0`, world id `0x3fe2`), attaches it to the renderer,
  creates two entities (ids at `+0x38`/`+0x3c`) and runs a first world update. **Destruction**
  `FUN_7ff650cc74a0` undoes all of it and resets `+0x48..+0x58`.

### 2.2 `STUStatescriptStateLobbyWorld` (`e452cc91`)  [G]

State node, runtime vtable RVA `0x15eb328`, fields `+0x100 m_map` (ConfigVar), `+0x108` output plug "loaded".
- OnEnter `FUN_7ff650d863e0`: `0x54+0x30 = eval(m_map)`, then `FUN_7ff650cc6ef0` (create the world).
- Tick `FUN_7ff650d864f0`: once the world's state (`world+0x3a8 → +0x160`) is 4 (loaded), fire the output
  plug (once).
- OnExit `FUN_7ff650d86620`: `FUN_7ff650cc74a0` (destroy the world). This is why the scene disappears when the
  lobby graphs stop (e.g. in a match).

Only one resident graph uses it: **`0x058…B47`** (child of `0x058…46B`, the `STUStatescriptStateUXLobby` graph
under `0x058…41B`). [D: `systems/data/live_anubis/graphs`]

### 2.3 The three title maps  [D]

B47's SetVar writes an array of three `STUConfigVarMap`s; LobbyWorld's `m_map` is an expression over that
variable (presumably a random pick [?]):

| map (`m_map`) | map header (`0x079…`, type 0x09F) | map data size | notes |
|---|---|---|---|
| `0x0800000000000677` | `0x0790000000000677` | 23.5 MB | package `06E00000570676D3`: entity defs `0x040…12B3`, `…12B4`, graph `0x058…B4A` |
| `0x0800000000000664` | `0x0790000000000664` | 13.0 MB | |
| `0x0800000000000675` | `0x0790000000000675` | 40.2 MB | |

All three are present in the local CASC and readable with the nine keys.

## 3. The featured hero

### 3.1 Graph `0x058…B4A` (on title-map entity `0x040…12B4`)  [D]

```
Entry → BooleanSwitch InCustomGame?                                    ── false ─▶
        BooleanSwitch GetHeroPreview(F) != EntityDef(0)                ── true ──▶
        BooleanSwitch GetHeroPreview(F) == var 0x0D8…1A7F   (on true: SetVar 1A7F = preview;
                                                              action 585a5b74: preview → offset/pitch/yaw/roll vars)
          ⊃ BooleanSwitch IsLoaded(F)                                  ── true ──▶
            BooleanSwitch IsLoaded(EquippedSkin(F))                    ── true ──▶
              StatescriptStateCosmeticEntity(m_entityDef = GetHeroPreview(F), m_skin = SkinFor(F),
                                             offset/pitch/yaw/roll vars, parent = owner)
F = ConfigVar 0x29969708 "featured hero"
```

### 3.2 The ConfigVars  [G]

| type hash | runtime vtable (RVA) | evaluator | meaning |
|---|---|---|---|
| `29969708` (STUConfigVarResource) | `0x15df010` | `FUN_7ff650d78680` | **featured hero**: lobby profile (`FUN_7ff650c24f80()`) `+0x230`; if 0, `FUN_7ff650cc93b0` picks it at random from the hero-record map (`+0x108/+0x110`), preferring heroes with record `+0x10 != 0` or level > 1. 0 while there is no profile |
| `a661d814` (bool) | `0x161d6f8` | `FUN_7ff6510a6b30` | **IsLoaded(resource)**: the resource entry exists, is loaded (`+0xa9`) and has no error (`+0xa4 == 0`) |
| `ae8e9502` (resource) | `0x15df8d0` | `FUN_7ff650d77340` | **equipped skin of hero**: hero record (profile `+0xF8` map, `FUN_7ff650595cc0`) `+0x20` |
| `8c92c925` (resource) | `0x15df9b0` | `FUN_7ff650d771a0` | **skin to render**: `FUN_7ff650cc8d20(profile, hero, equipped skin)`: variant from the record's `[]{u64 skin, u64}` list (default 1), then the matching entry of the skin asset; 0 if the equipped skin is 0 |
| `09e7eb88` | `0x15e0fa0` | – | STUConfigVarInCustomGame |

### 3.3 What the server must send  [G][U]

`ClientInLobbyHeroProgression` 24900, per hero record (wire = in-memory layout, copied by `FUN_7ff650f9b9e0`):

| record offset | wire | meaning |
|---|---|---|
| `+0x00` | i8 | ? |
| `+0x04` | u32 | level |
| `+0x08` | u64 | hero GUID (`0x02E…`) |
| `+0x10`, `+0x18` | u64, u64 | shown on the title screen (`0x4ACDC2D3`, `0x09CA2621`; XP-like) |
| **`+0x20`** | **u64** | **equipped skin** (`0x0AE…`, `STUHero.m_skins`); **also set by 24902 `{hero, skin}`** |
| `+0x28` | u64 | equipped SkinTheme unlock (24903) |
| `+0x30`/`+0x38`/`+0x40` | 3 × u64 | equipped HeroMod unlocks, slots 0–2 (24903) |
| `+0x48`/`+0x50`/`+0x58`/`+0x60` | 4 × u64 | equipped POTGAnimation / SprayPaint / class `0x5F38A9D5` / Trophy unlocks (24903) |
| `+0x68` | `[]{u64 ctx, u64 theme}` | per-context (skin?) SkinTheme overrides (24903 with ctx ≠ 0); `FUN_7ff650cc8d20` picks the render variant from it |
| `+0x80` | `[]unlock` | owned unlocks |

The 21 heroes of 0.8 and their only skin [D] are in `server/internal/lobbylogic/progression.go` (`heroSkins`).
With them the title screen shows the featured hero (Winston in the first test) [U].

## 4. `ClientInLobbyConnect` 20503 and 20504  [G]

**20503** `{u64[] keyNames, u8[] keyBytes}`, handler `FUN_7ff650c5f090`:
1. for each key name `i`: insert `{name, keyBytes[16·i .. 16·i+16], 1}` into the TACT keyring
   (`g_tact = DAT_7ff65180ca50`, keyring at `+0x58`, emplace `FUN_7ff6509b0470`). The `u8[]` is the flat
   16-byte keys. These are the "streamed from server" keys of [ContentAccess.md](ContentAccess.md) §2;
2. re-acquire the asset packages `0x06E…08D` and `0x06E…0B5` (`FUN_7ff6509a4670`; they re-open when they
   were waiting for a key);
3. queue the asset package `0x06E…079` on the lobby load group `DAT_7ff651797fe8` (`FUN_7ff650f1eb10`; when
   all of the group's packages have loaded it calls its vfunc `+0x20`);
4. set `0x54+0x59 = 1`.

For us the keys are already injected by Prometheus (`casc_keys` in `Prometheus-Core/dllmain.cpp`), so two
empty arrays suffice. Without Prometheus the server would have to send the seven streamed keys here.

**20504** `{u64[] ×4}`, handler `FUN_7ff650c5f1f0`: copies the four arrays and passes them to
`FUN_7ff650c33390(DAT_7ff651797fe8, a, b, c, d)`, the same lobby load group. Not needed for the title screen;
meaning of the four lists open [?].

## 5. Lobby JAM messages the client receives

Catalogue of the 16 `ClientInLobby*` protocols (T022; tag **[K]** = taken from the early lobby notes or lobbylogic comments). "We send" = `server/internal/lobbylogic` sends it (2026-09-25).

### Conventions

- **Fields** come from `game/owbetaclient/Prometheus/jam_protocols.json` . `[]X` = dynamic array (`u32 count` + elements), `X[n]` = fixed array (no count), `{…}` = nested struct, `ID` = `{u64, u64}` (JamID/EntityId), `str` = cstring, `bytes` = `u32 len + bytes`. Signedness per JAM type, from the client's JSON encoder (T049 [G], `FUN_7ff650f0ce90`): 1 = char, 2 = u8, 3 = i8, **4 = u16, 5 = i16** (earlier text had these two swapped), 6 = u32, 7 = i32, 8 = u64, 9 = i64; the authoritative table is `systems/data/jam/catalogue.json` (docs/JAM.md §12).
- A message's first field is at message object **+0x68** (`param_2[0xd]`). Dispatchers read the ID at `msg+0x1c`.
- **Receivers → dispatchers [G].** Each receiver (`FUN_7ff650c47xa0`) returns early if `msg+0x48 != 0`. It then finds its lobby system object by RTTI through a finder, which the dispatcher gets as `param_3`, and calls the dispatcher:

| Protocol (CRC) | IDs | Receiver | Dispatcher | Base |
|---|---|---|---|---|
| Chat (dbe61f10) | 20400–20407 | `c476a0` | `FUN_7ff650c48990` | 0x4fb0 |
| Connect (4e958daf) | 20500–20505 | `c47aa0` | `FUN_7ff650c495a0` | 0x5014 |
| Instance (6e5d38b1) | 20600–20603 | `c47ba0` | `FUN_7ff650c49720` | 0x5078 |
| Party (c988e4b3) | 20700–20707 | `c47ea0` | `FUN_7ff650c49c20` | 0x50dc |
| Player (912cea67) | 20800–20805 | `c47ca0` | `FUN_7ff650c497f0` | 0x5140 |
| CustomGame (74c43f11) | 23300–23312 | `c477a0` | `FUN_7ff650c48c20` | 0x5b04 |
| PlayerProgression (bd212ab1) | 24300–24304 | `c47fa0` | `FUN_7ff650c49e10` | 0x5eec |
| HeroProgression (70519a68) | 24900–24908 | `c479a0` | `FUN_7ff650c49340` | 0x6144 |
| Matchmaking (5fcc2ef6) | 25600–25607 | `c47da0` | `FUN_7ff650c49a00` | 0x6400 |
| Store (7845d37e) | 26400–26409 | `c482a0` | `FUN_7ff650c4a390` | 0x6720 |
| Friends (585b3816) | 27100–27117 | `c478a0` | `FUN_7ff650c48ee0` | 0x69dc |
| Stats (0d111732) | 27200–27202 | `c481a0` | `FUN_7ff650c4a240` | 0x6a40 |
| Achievement (42d8c448) | 28000–28002 | `c474a0` | `FUN_7ff650c48720` | 0x6d60 |
| Challenge (75e8ed61) | 28200–28202 | `c475a0` | `FUN_7ff650c48890` | 0x6e28 |
| FreeAssets (e79948f2) | 30500 | `c483a0` | `FUN_7ff650c4a580` | 0x7724 |
| Relationship (8a89bc90) | 31800–31804 | `c480a0` | `FUN_7ff650c49fe0` | 0x7c38 |

- **Out-of-range IDs.** Most dispatchers return 1 on an ID outside their range, and Chat, Connect, Player and Achievement also call `JamLink::Close` (`FUN_7ff650f088f0`) **[G]**. The registration's max ID is exclusive, so the last ID in the `04` table (e.g. 20408, 24909, 26410) doesn't exist; the dump confirms this **[G]**.
- **"Profile"** = `FUN_7ff650c24f80()`, the lobby connection/profile object **[K][G]**. Its map of loaded unlock assets is at **+0x188** (hash table `+0x198`/`+0x1a0`); every list of unlocks the client receives loads its assets there **[G]**.
- **"Print only"** means the handler formats a console/log table (`FUN_7ff6507c5c90`/`c5ea0`, strings such as "Stats for Hero…") and stores nothing. These are replies to dev console commands **[G]**.

---

### ClientInLobbyConnect (ch 10)

| id | handler | fields | on receipt | we send |
|---|---|---|---|---|
| 20500 | `c5d0e0` | `ID, str` | Connect accepted: stores the ID at profile +0x60/+0x68 (= "self" for chat/party) and the string at +0x70, sets flag 0x10000, flushes pending items **[K]** | yes |
| 20501 | inline | (empty) | profile +0x23c = 1 (leaves "Authenticating"). If global `DAT_7ff651811dd7` is set it auto-presses Play (`c67220`) **[G][K]** | yes |
| 20502 | inline → `c45a70` | `bool` | Lobby disconnect response: sets the lobby status slot to 1 or 5. If the bool is set, global `DAT_7ff651797fe0 = 1` **[G][K]** | no |
| 20503 | `c5f090` | `[]u64 keyNames, []u8 keys` (16 bytes per name) | **TACT key delivery** into the keyring `[g_tact=DAT_7ff65180ca50]+0x58`, then re-acquires packages `0x06E…08D`/`…0B5`, loads package `0x06E…079` on the lobby load group `DAT_7ff651797fe8` and sets `0x54+0x59 = 1` (§4) **[G]**. Sending it empty reaches the title screen (keys injected by Prometheus) **[K]** | yes (empty) |
| 20504 | `c5f1f0` | `[]u64 ×4` | Copies the 4 GUID lists and calls `FUN_7ff650c33390(DAT_7ff651797fe8, l1, l2, l3, l4)`. That feeds every GUID to the same data-pack loader (`f1eb10`) in the order l3, l1, l2, l4, then re-arms the loader: **extra data packs to load** **[G]** (meaning of the 4 lists **[?]**) | no |
| 20505 | — | (empty) | Nothing (the switch falls through, returns 0) **[G]** | no |

### ClientInLobbyChat (ch 30)

Chat manager = `FUN_7ff650c26e90()`.

| id | handler | fields | on receipt | we send |
|---|---|---|---|---|
| 20400 | `c5b700` | `{i32 type, ID} chan, {str name, ID} sender, str text` | Shows a chat line, unless the sender ID equals self (profile +0x60/+0x68). Registers the sender name in the name cache **[G][K]** | yes |
| 20401 | `c5c040` | `{i32, ID} chan, []{str, ID}` | `/list` reply: registers the names (`cb5690`) and prints the channel's member list (text 0x0DE…441, UI 0x0AC…5D5) **[G][K]** | yes |
| 20402 | `c5c450` | `{{bool,bool},{bool},{i32,ID}}` | Joins one channel (`cbbdd0`) plus a notice (`c5c670`) **[G]** | no |
| 20403 | loop → `cbbdd0` | `[]{{bool,bool},{bool},{i32,ID}}` | Joins each channel (flags must be 0) **[G][K]** | yes |
| 20404 | `cbbe70` | `{i32,ID}, {bool,bool}` | Sets a channel's flag bits 0–1 (disable) **[G][K]** | no |
| 20405 | `cbbf80` | `{i32,ID}, {bool}` | Sets a channel's flag bit 2 **[G][K]** | no |
| 20406 | `cbc080` | `{i32,ID}` | Leaves a channel **[G][K]** | no |
| 20407 | loop → `cb99b0(chat+0x128)` | `[]ID` | Appends IDs, without duplicates, to the chat manager's ID list +0x128 (probably a mute/ignore list **[?]**) **[G]** | no |

### ClientInLobbyInstance (ch 15)

| id | handler | fields | on receipt | we send |
|---|---|---|---|---|
| 20600 | `c5d580` (`ClientInLobbyInstance_20600_Connect`) | `u64 map, str host, i16 port, u32 connID, u8[20] key, ID` | Game found: sets the wanted map, connects the UDP instance transport (HMAC key), stores the ID at lobby +0x228/+0x230 **[K]** | yes |
| 20601 | `c461f0` | `u64` | Shows a UI popup (0x0AC…5D5) with the string ID u64 (same as ClientInInstance 20307) **[G]** | no |
| 20602 | `c5d800` | `[]{str, i32, i32, u64, ID, ID}` | Print only: an "Instance / Address / Index / Players" table **[G]** | no |
| 20603 | `c5e2b0` (`…_20603_PrintInfo`) | `{str type, u64 map, ID, []{str,str,i32,ID,ID}}` | Print only: "Map Name / Instance Type / players" **[K]** | no |

### ClientInLobbyParty (ch 28)

Party object = `FUN_7ff650c27500(DAT_7ff651813e18)`.

| id | handler | fields | on receipt | we send |
|---|---|---|---|---|
| 20700 | `cba220(party, +0x68, +0x78)` | `ID party, []{str tag, bool leader, u8 status, u64 icon, u64, ID player, ID}` | Full party state: enables Play/Custom/Practice/Tutorial. The title-screen name and portrait come from the member (`+0x20` = icon) **[K][G]** | yes |
| 20701 | `c0b400` | `ID suggester, str, ID suggested, str` | **Party suggestion** ("%s suggests %s to the party"): registers the suggested name, returns silently unless the suggester is already in the name cache, then game message 0x024…59F → toast **[G][H: toast shows and closes at once]** (T031) | no (`!msg`) |
| 20702 | `c0b7a0` | `ID, ID inviter, str battletag` | **Party invite received**: registers the name, sets the party's pending invite, game message 0x024…197 → toast "JOIN %s'S PARTY?" (accept/decline) **[G][H]** (T031; was mis-described as declined/failed) | no (`!msg`) |
| 20703 | `c0a550(party+0x28)` | `ID` | **Invite cleared**: resets the party's pending-invite sub-object (2 IDs to null, 3 strings, +0x10 = 0); the invite toast closes **[G][H]** (T031). The ID isn't read | no (`!msg`) |
| 20704 | loop → `FUN_7ff6507c5c90(1,str)` | `[]str` | Print only: one console line per string **[G]** | no |
| 20705 | `c0bcd0` | `ID, []member (as 20700)` | Print only: a "Local view / Server view party" debug dump (roles Member/Invitee/Leaving…) **[G]** | no |
| 20706 | — | `ID, ID` | Ignored (the switch falls through) **[G]** | no |
| 20707 | — | `ID, str` | Ignored **[G]** | no |

### ClientInLobbyPlayer (ch 21)

| id | handler | fields | on receipt | we send |
|---|---|---|---|---|
| 20800 | `c5d180` | `[]str` | Print only: a one-column "Name" table **[G]** | no |
| 20801 | `c5ed80` | `ID, str name, ID persistentID, u64 version, ID, []{u32 lvl, u64 hero, u64 xp}` | Print only: "ID / Name / Persistent ID / Persistent Version / Hero Progression: Hero 0x… xp … level …" **[G]** | no |
| 20802 | `cced30` on `FUN_7ff650c2ae30()` + inline | `f32, f32, f32, bool, bool, {bool,bool,bool}, {bool}, []{bool,bool,bool,u64,u64}` | **Saved player settings.** f32 @0x68 clamped → settings +0x6dc (probably sensitivity **[?]**); bool @0x74 → +0x5da and `[+0x38]+0x6c`; f32s @0x6c/@0x70 → `[+0x38]+0x60/+0x64`; bool @0x75 → `[+0x38]+0x6c`; the list → +0xa0 = **key bindings**, applied through an action table of 0x82 entries at `DAT_7ff6517061e0` (the 3 bools → modifier flags 0x20000000/0x10000000/0x40000000) **[G]**. The bytes @0x76–0x79 → **profile +0x238..+0x23b** (read by lobby chat with feature flag 18 **[K]**) **[G]** | no |
| 20803 | `c5bdb0` | `u8` | Popup (UI 0x0AC…5D5) with string `DAT_7ff6515a3fb0[u8]` (an indexed notice/error) **[G]** | no |
| 20804 | inline | `{bool, u64, u64}` | **CAIS play-time tracker**: `{bool enabled, u64 remaining ms, u64 played ms}` → profile +0x18/+0x20/+0x28; the CAIS "Healthy Gaming" screen shows while enabled, and 3 h / 5 h threshold popups are client-side **[G][L][H]** (T030, docs/LobbyServer.md) | no |
| 20805 | `cb10d0` on `FUN_7ff650c29ad0()` | `{str, u32, []u64, []u64, ID, stats×2, {bool,u64,[]unlock}, []hero record}` | Reply to a pending player-profile request: accepted only when the object's pending flag +0x18 is set and the ID matches +0x68/+0x70; copies the profile into +0x20 **[G]**. It looks like "inspect player" (career-like: stats + progression) **[?]** | no |

### ClientInLobbyCustomGame (ch 17)

Custom-game object = `FUN_7ff650c29670()` (the Play guard also reads it: +0x208).

| id | handler | fields | on receipt | we send |
|---|---|---|---|---|
| 23300 | `cb45f0` ×2 | `[]u64, []u64` | Replaces the u64 lists at obj +0x40 and +0x58 (probably the allowed maps / heroes **[?]**) **[G]** | no |
| 23301 | `cb4200` | `[]{u16×4, gameinfo}` | Adds entries (0x50 bytes each) to the obj +0x18 list if not present (the game browser list **[?]**) **[G]** | no |
| 23302 | `bef530` | `ID, str, u64[2] game` | **Private-game invite**: chat notice (text 0x0DE…1924), stores the invite on the custom-game object, game message 0x024…5BF → toast "%s INVITED YOU TO THEIR PRIVATE GAME LOBBY." with Accept/Decline, stays **[G][H]** (T031) | no (`!msg`) |
| 23303 | inline | `gameinfo, settings` | "You're in this game": +0x208 = 0, gameinfo → +0x70, settings → +0xb8 and +0x210, dirty +0x2d9 **[G][K]** | yes |
| 23304 | `f9c550(obj+0x180)` | `{…lobby state…}` | Copies the lobby state struct → +0x180, dirty **[G]** | no |
| 23305 | inline | `settings` | Settings → +0xb8 and +0x210, dirty **[G]** | no |
| 23306 | `bef7e0` | `str, str` | Popup "Banned from custom server %s Reason: %s" (0x0DE…E39) **[K][G]** | no |
| 23307 | `bef8b0` | `u64` | Popup with the string ID **[K][G]** | no |
| 23308 | `cb42c0` | `u64[2]` | Resets the custom-game object (the reply to Cancel) **[G][K]** | yes |
| 23309 | `bef950` | `bytes` | Print only: decodes the settings blob and prints them (Name, Maps, Team sizes, Access list…) **[G]** | no |
| 23310 | `bf1870` | `[]{str,str,u8,u8,i32,i32,ID,ID,ID}` | Print only: players (Battletag / Player ID / State / Assigned/Registered Instance…) **[G]** | no |
| 23311 | `bf2630` | `[]{str, i32, u64, ID}` | Print only: instances (Instance ID / Map GUID / Player Count / State) **[G]** | no |
| 23312 | `bf2fa0` | as 23301 | Print only: game list (Owner / Description) **[G]** | no |

### ClientInLobbyPlayerProgression (ch 26)

| id | handler | fields | on receipt | we send |
|---|---|---|---|---|
| 24300 | `cc8770` + `cc88d0` | `{bool, u64, []unlock}, {[]unlock}` (unlock = `{bool, i32 level, u64 asset, u64}`) | A: profile **+0x148 = bool**, **+0x150 = u64 = equipped player icon** (`STUUnlock_AvatarPortrait`; see 24903), list → **+0x158** (owned account unlocks). B: list → **+0x170**. All assets are loaded into +0x188 **[G]** | yes (all zero) |
| 24301 | `cc8a10` | `u64` | Adds one unlock to the owned list +0x158 (and loads it) **[G]** | no |
| 24302 | `f07240([c28dc0]+0x40)` | `{[]u64}` | Copies the u64 list into `[FUN_7ff650c28dc0()]+0x40` (meaning **[?]**) **[G]** | no |
| 24303 | `c0a290` | `[]{str, u64}` | Print only: "Default Unlocks" **[G]** | no |
| 24304 | `c0a3b0` | `[]{str, u64}` | Print only: "Active Unlocks" **[G]** | no |

### ClientInLobbyHeroProgression (ch 16)

Hero record = `FUN_7ff650cc95a0(profile, hero)`. It is **find-or-insert** in the map at profile +0xF8: a missing hero gets a zeroed record with +8 = hero **[G]**. The wire record (0x98 bytes, copied field for field by `f9b9e0` **[G]**):

| rec off | wire | meaning |
|---|---|---|
| +0x00 | u8 | ? |
| +0x04 | u32 | level (shown; also set by 24904) **[G][K]** |
| +0x08 | u64 | hero GUID `0x02E…` **[G]** |
| +0x10 | u64 | → LobbyMainMenuVM `0x4ACDC2D3` **[K]**; set by 24904 **[G]**. XP? **[?]** |
| +0x18 | u64 | → VM `0x9CA2621` **[K]**. XP to next level? **[?]** |
| +0x20 | u64 | **equipped skin** (`STUHeroSkin 0x0AE…`), set by 24902, read by ConfigVars `d771a0`/`d77340` **[G][K]** |
| +0x28 | u64 | **equipped `STUUnlock_SkinTheme`** (default context) **[G]** |
| +0x30 / +0x38 / +0x40 | u64 ×3 | **equipped `STUUnlock_HeroMod` slots 0 / 1 / 2** (slot = the unlock's int at +0x58, field `0x32a11a46`) **[G]** |
| +0x48 | u64 | **equipped `STUUnlock_POTGAnimation`** (highlight intro) **[G]** |
| +0x50 | u64 | **equipped `STUUnlock_SprayPaint`** **[G]** |
| +0x58 | u64 | **equipped unlock of class `0x5F38A9D5`** (no fields beyond the unlock base; name unknown: emote/voice line/pose? **[?]**) **[G]** |
| +0x60 | u64 | **equipped `STUUnlock_Trophy`** **[G]** |
| +0x68 | `[]ID` = `[]{u64 ctx, u64 theme}` | per-skin SkinTheme entries (24903 with ctx ≠ 0): **ctx = the skin GUID** (confirmed: the renderer `FUN_7ff650cc8d20` looks up the entry whose ctx = equipped skin, then the theme's slot → the skin's `STUSkinTheme`) **[G][H]** (T026). `+0x28` is not read by the renderer |
| +0x80 | `[]unlock` | owned hero unlocks (24901 appends) **[G]** |

This comes from 24903's setter `FUN_7ff650cc8540` and the matching check "is this unlock equipped?" `FUN_7ff650cc8aa0`. Both look the unlock up in profile +0x188, require the `STUUnlock` base (RTTI `0x7ff651732b78`), and branch on the class RTTI:
- `0x7ff6517330a0` AvatarPortrait `8CDAA871` → **profile +0x150** (not per hero);
- `…2ca0` HeroMod `3482B4E9`;
- `…2d20` PortraitFrame `3ECCEB5D`: the setter ignores it, and the check always returns true;
- `…2da0` SkinTheme `8B9DEB02`;
- `…2e20` `5F38A9D5`;
- `…2ea0` POTGAnimation `4EE84DC0`;
- `…2fa0` SprayPaint `15720E8A`;
- `…3020` Trophy `7979C642`.

Class names are from `systems/stu/names/KnownTypes.csv` **[G]**.

| id | handler | fields | on receipt | we send |
|---|---|---|---|---|
| 24900 | inline + `cc80d0` + `cc8290` | `i32 cap, []record, []{u64 hero, []unlock}` | profile **+0xF0 = level cap**. Records: clears the +0xF8 map, then copies each record in (find-or-insert). Reward tracks: clears the +0x120 map, `hero → []unlock` (the "next reward" source). All unlock assets in both lists are loaded into +0x188 **[G][K]** | yes |
| 24901 | `cc96c0` | `u64 hero, u64 unlock` | Loads the unlock (`cc9640`) and appends it to the record's owned list +0x80 if it isn't there, then `cc9770` (probably a notify **[?]**) **[G]** | no |
| 24902 | inline | `u64 hero, u64 skin` | Record **+0x20 = skin** (equip skin) **[G]** | no |
| 24903 | `cc8540` | `u64 hero, u64 ctx, u64 unlock` | **Equip unlock** by class (table above). Needs the unlock already loaded in +0x188; otherwise a no-op **[G]** | no |
| 24904 | `cb22b0` + `cc8480` | `[]{u32 level, u32, u64 hero, u64, u64, []u64 unlocks, []{u32,u64,u64,u64}}` (0x50 each) | **Progression update** (probably post-match **[?]**): the whole list is copied to `[FUN_7ff650c28dc0()]+0x28`; per entry, record **+0x04 = level**, **+0x10 = entry u64 @+0x10**, and each u64 in `unlocks` is granted (as 24901) **[G]** | no |
| 24905 | `c096c0` | `[]u64` | Print only: a "Level / Delta Experience Needed" table (the XP curve) **[G]** | no |
| 24906 | `c09970` | `u64 hero, []{str, u64}` | Print only: "All Unlocks for 0x…" **[G]** | no |
| 24907 | `c09aa0` | `u64 hero, []{str, u64}` | Print only: "Active Unlocks for 0x…" **[G]** | no |
| 24908 | `c09bd0` | `u64 hero, []{[]{str, u64}}` | Print only: "Current Unlocks for 0x…" by level **[G]** | no |

### ClientInLobbyMatchmaking (ch 13)

Matchmaking object = `[DAT_7ff651813e18+0x1B8]`, passed as `param_3`.

| id | handler | fields (abridged) | on receipt | we send |
|---|---|---|---|---|
| 25600 | `d33b90(mm+0x18)` | `{u32 status, i32, []{u8,i32,i32,i32,f32,f32}, ticket, []ticket, []ticket, []{…}}` | Queue-ticket update. 0→1 tickets: clears pending +0x33, game message 0x024…096. 1→0: 0x024…097. Refreshes the UI (`c678c0`) **[G][K]** | yes |
| 25601 | `c67d70` | server info … | Matchmaking server info (printed if the bool is set, else UI refresh) **[K]** | no |
| 25602 | `c6bca0` | ticket dump | Print only: "Matchmaking tickets:" **[K]** | no |
| 25603 | `c70280` | `[]{u8, f32, i32×3, u64, f64×7, u64, f64}` | Print only: "Ratings" (Rating / Variance / Volatility / Streak …) **[G]** | no |
| 25604 | inline | `u64` | `[FUN_7ff650c28dc0()]+0x18 = u64` **[G]** (meaning **[?]**) | no |
| 25605 | inline | `u32` | Global `DAT_7ff651813dfc = u32` **[G]** (meaning **[?]**) | no |
| 25606 | `c72040` | `[]{u8, u64, []u64}` | Print only: map bucket catalog ("AutomationStressTest", "Map 0x… %s") **[G]** | no |
| 25607 | `c722e0` | `ID, []{u8, u64, []u64}` | Print only: "Matchmaker %s updated %s map cat…" **[K]** | no |

### ClientInLobbyStore (ch 19)

Currency balances live at profile +0xd8 (`{u16 currency, f32 amount}` ×n, count +0xe0) and the owned-product set at profile +0x90 (`cca6d0`) **[G]**.

| id | handler | fields | on receipt | we send |
|---|---|---|---|---|
| 26400 | `cc8040` | `{u16 currency, f32 amount}` | Sets or adds the balance for that currency **[G]** | no |
| 26401 | `c774b0` | `[]u64 products, []{u16, f32}` | Replaces the owned-product set and all balances, fires a game message (`c24dd0`) **[G]** | no |
| 26402 | inline | `i32 status, str, str, i64` | Purchase flow: status 1 → "Begin purchase succeeded" (`c76d00`); 3 → web challenge (URL/JSON, `c76d80`, game message 0x024…0A1) **[G]** | no |
| 26403 | inline | `u64` | Adds a product to the owned set, game message **0x024…0A2** (purchase complete **[?]**) **[G]** | no |
| 26404 | inline | `u64` | Adds a product to the owned set (silent) **[G]** | no |
| 26405 | `c773a0` | `u16` | "Purchase Failed (Reason %d)" **[G]** | no |
| 26406 | `c775c0` | `[]{str,str,[]{u16,i64,i64}}` | Print only: products (Base/Current Price) **[G]** | no |
| 26407 | `c77e60` | `[]{str,u64}, []{u16,f32}` | Print only: licenses / currency **[G]** | no |
| 26408 | `c789d0` | `[]{str,u8,u8,u8,[]u8,i64}` | Print only: transaction records **[G]** | no |
| 26409 | `c79730` | `[]{str,str,u16,i64}` | Print only: active holds **[G]** | no |

### ClientInLobbyFriends (ch 11)

Friends manager = `FUN_7ff650c27c00()`. After the list handlers (27101–27105), listeners at +0x90/+0x98 are notified **[G]**.

| id | handler | fields | on receipt | we send |
|---|---|---|---|---|
| 27100 | `bf9ae0` | `[]friend, []request, []presence, []{ID}` | Full state: clears, adds the 4 lists, sets "loaded" +0x58 **[K]** | yes |
| 27101 | `bf6d80` | `{u32, u64, ID}` | Adds a friend **[K]** | yes |
| 27102 | `bf6f30` | `ID` | Removes a friend **[K]** | yes |
| 27103 | `bf7230` | `{str,str,str,u32,u64,ID,ID}` | Adds an incoming request (game message 0x024…196, asset 0x0A4…075) **[K][G]** | yes |
| 27104 | `bf74a0` | `ID` | Removes a request **[K]** | yes |
| 27105 | inline | `{u32, u64 flags, ID}` | Updates a friend entry: +0x30 u32, +0x38 flags (favourite), +0x40/+0x48 ID, +0x2c++ **[G][K]** | yes |
| 27106 | `bf9c30` | `u64 cookie, u64 text, u64 arg` | Completes a pending request (+0x28 map, by cookie) and shows a result popup for text IDs 0x0DE…363–36A / 17B6 **[G]** | no |
| 27107 | `bf9d70` | `u64 cookie, u32` | Request result popup (0x0DE…436/437/71E/71F) **[G]** | no |
| 27108 | `bf9f10` | `u64 cookie, u32` | Request result popup (0x0DE…434/720) **[G]** | no |
| 27109 | `bfa080` | `[]{[]{bytes key, bytes variant, u32 op}, ID, ID}` | Presence records (BN field keys) **[K]** | yes |
| 27110 | `bfa120` | `u32 status, u64 cookie, bytes` | Completes a pending query. For a type-1 entry at +0xf0 with status 0 it decodes `bytes` (`e681c0`), else marks it failed (4) **[G]** (payload meaning **[?]**) | no |
| 27111 | inline | `u64 cookie, u64 text, u64` | Completes a pending request; popup via `bf9990` unless the text is 0x0DE…311 **[G]** | no |
| 27112 | inline | `u64 cookie, u32` | Silently completes/removes a pending request **[G]** | no |
| 27113 | `bfa210` | `ID account, str text` | Registers the account's BattleTag (presence BN/1/4) in the name cache and passes the text to the chat manager `cbd820`: probably an incoming whisper **[?]** **[G]** | no |
| 27114 | `bfa360` | `ID account, str text` | Same, with `cbd9e0`: probably whisper echo/failure **[?]** **[G]** | no |
| 27115 | `bfa4b0` | (empty) | Walks the manager's +0xd0/+0xd8 table and refreshes/cleans entries (`bf6c00`) **[G]** (meaning **[?]**) | no |
| 27116 | `bfa740` | `u64 text, ID` | Popup with the player's name. Text 0x0DE…1A70 → adds the ID to the block list +0x120; …1A71 → removes it (`cb9450`) **[G]** | no |
| 27117 | `bfa860` | `bool` | Popup 0x0DE…1AB7 / 1AB8 **[G]** | no |

### ClientInLobbyStats (ch 3)

| id | handler | fields | on receipt | we send |
|---|---|---|---|---|
| 27200 | `c0cf50` | `ID, str, []{u64 hero, []{u64 stat, u64, f64}}` | Print only: "Total Hero Stats / Stats for Hero" **[G]** | no |
| 27201 | `c0d330` | same | Print only: "Total Player Map Stats / Stats for Map" **[G]** | no |
| 27202 | `cc9200` | `[]{u64, []{u64, u64, f64}}` | Stores into the profile +0x208 map (key u64 → stat list) and loads the referenced assets **[G]** | no |

### ClientInLobbyAchievement (ch 9)

| id | handler | fields | on receipt | we send |
|---|---|---|---|---|
| 28000 | `be77b0` | `ID, str, []u64` | Print only: "Achievements" of a player **[G]** | no |
| 28001 | `cc8ee0` | `[]u64` | Replaces the profile's sorted list +0x1b0 (earned achievements) and loads each asset **[G]** | no |
| 28002 | `be7c20` | `u64` | Adds the achievement (`cc90d0`), logs "Completed achievement %s", shows a toast (UI 0x0AC…5D5) **[G]** | no |

### ClientInLobbyChallenge (ch 18)

| id | handler | fields | on receipt | we send |
|---|---|---|---|---|
| 28200 | — | `[]ID` | Accepted and ignored **[G][K]** | no |
| 28201 | — | `ID` | Accepted and ignored **[G]** | no |
| 28202 | — | `[]{u64, u64, f64, ID}` | Accepted and ignored **[G]** | no |

### ClientInLobbyFreeAssets (ch 31)

| id | handler | fields | on receipt | we send |
|---|---|---|---|---|
| 30500 | `cc7f60` | `[]u64` | Replaces the profile's u64 list +0xc0 (free/unlocked-for-free assets) **[G]** | no |

### ClientInLobbyRelationship (ch 20)

The target is a player-record cache found by RTTI `0x7ff651799758`, per `[K]` the player cache `[DAT_7ff651813e18+0x1C0]`. Records are keyed by ID (+0x18 map). The record layout is `{{str name}, {u32}, {u64}, ID, {u32,u32,i32,[]ID}, {ID group, []ID}, {[]mm-ticket}}`.

| id | handler | fields | on receipt | we send |
|---|---|---|---|---|
| 31800 | `c74410` + `c749f0` + `c74f90` | `[]record, []ID` | Full state: clears the cache, adds the records, and sets the ID list at +0x60 **[G]** | no |
| 31801 | `c749f0`×2, `c74c40`, `c74d30`, `c74e50` | `[]record, []record, []ID, []ID, []ID` | Delta: upserts two record lists, removes records by ID, adds to / removes from the +0x60 list **[G]** (which list is which **[?]**) | no |
| 31802 | `d33b90(rec+0x60)` | `ID, mm-ticket` | Updates the record's matchmaking tickets (same code as 25600) **[G]** | no |
| 31803 | inline + `c74f90` | `ID, {ID, []ID}` | Record +0x78/+0x80 = ID (group/party), +0x88 = ID list **[G]** | no |
| 31804 | inline | `ID, {u64}` | Record **+0xa0 = u64** (the portrait icon shown for friends **[K]**) **[G]** | no |

---

### Relevant to the title screen / main menu

1. **20500 / 20501 / 20503** are the connect sequence. 20501 also auto-presses Play when `DAT_7ff651811dd7` is set **[G]**.
2. **20503** delivers TACT keys (§4) and loads package `0x06E…079`; **20504** feeds 4 more GUID lists to the same package loader (extra asset packages). Neither is needed for the title-screen hero **[G][U]**.
3. **20700 Party** enables the menu buttons and supplies the title name and portrait (member `+0x20`) **[K]**.
4. **24900 HeroProgression**:
   - level cap → profile +0xF0; records → +0xF8; reward tracks → +0x120;
   - the featured hero is profile +0x230, picked by `FUN_7ff650cc93b0`;
   - record +0x20 = equipped skin, which the title scene needs to spawn the hero **[K]**.
   - **24902** changes the skin live. **24903** equips a SkinTheme/HeroMod/POTG/Spray/Trophy/`5F38A9D5` unlock into record +0x28..+0x60, but only for unlocks already in the loaded-unlock map (profile +0x188, filled from the unlock lists of 24900/24300/24301/24901/27202/28001).
   - **24904** changes level/+0x10 and grants unlocks.
   - Our 24900 sends +0x28..+0x60 as 0, so no theme, weapon mod (HeroMod) or spray is equipped. If the featured hero's cosmetic ConfigVars read these, the title hero shows defaults **[?]**.
5. **24300 PlayerProgression**:
   - A.u64 = **profile +0x150 = equipped player icon** (AvatarPortrait). We send 0, so the icon is unset.
   - A.bool → +0x148; owned lists → +0x158 / +0x170.
   - The title-screen portrait itself comes from the party member (20700), not +0x150 **[K]**. Whether other UI reads +0x150 is **[?]**.
6. **20802 Player settings** (sensitivity-like floats, toggles, key bindings, 4 chat/social option bytes at profile +0x238..+0x23b) is the only "settings from server" message. We don't send it, so the defaults apply **[G]**.
7. **20804** play-time warnings (profile +0x18..+0x58). Harmless unless enabled **[G]**.
8. **Popups we could use for server notices:** 20601 (u64 string ID) and 20803 (u8 index into a fixed string table); CustomGame 23307 (u64 string ID) **[G]**.
9. **Store/Achievement/Stats/FreeAssets** only fill profile data (0.8 has no UI for them **[K]**). The debug-print messages (20704, 20705, 20800, 20801, 20602, 20603, 23309–23312, 24303, 24304, 24905–24908, 25602, 25603, 25606, 25607, 26406–26409, 27200, 27201, 28000) are harmless and useful as "echo" channels for dev-console output.


### Open questions (catalogue)


- What the 4 lists of 20504 mean (all fed to the same package loader, in the order l3, l1, l2, l4).
- Class `0x5F38A9D5` (hero record +0x58) has no name in `KnownTypes.csv`. Candidates: emote, voice line or victory pose. Check which loaded unlock assets carry that class.
- The SkinTheme context in record +0x68 (`[]{ctx, theme}`): is ctx the skin GUID?
- Hero record +0x00 (u8), +0x10/+0x18 (XP vs XP-next?), and 24904's second u32 and trailing `[]{u32,u64,u64,u64}` (probably per-match XP breakdown), stored at `[FUN_7ff650c28dc0()]+0x28`. `FUN_7ff650c28dc0` is obfuscated/anti-tamper code in Ghidra; identify the object it returns (also used by 24302 +0x40 and 25604 +0x18).
- Profile +0x148 (24300 A.bool) and whether anything shows +0x150 (equipped icon).
- 20802 float/bool semantics (sensitivity, invert-Y?) and the 0x82-entry binding action table at `DAT_7ff6517061e0`.
- The exact meaning of 20701/20702 (party invite in/declined?), 23302 (custom-game invite?), 27113/27114 (whisper in/echo?), 27110/27115, 20407 (+0x128 list = mute?), 25604/25605.
- Checked: 20505 and 20706/20707 have no handler logic; Challenge 28200–28202 are ignored.

## 6. Open questions

- The hero record's i8 at `+0`, and class `0x5F38A9D5` (record `+0x58`).
- LobbyWorld's `m_map` expression (random pick of the three maps?) and whether the camera (`0x040…12B3`,
  PlacedCamera) is driven by server-synced state `b0054407` (B47's only remote sync node).
- Who fills the nested EntityAdmin at `0x54+0x28` (the title map's placed entities presumably go there [?]).
- 20504's four GUID lists.
