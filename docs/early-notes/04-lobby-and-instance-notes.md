# 04 — Lobby and instance-link details not in the main docs

Facts from the first lobby and game-server bring-up that the main documents don't carry. Each section says
where the main coverage of its subject is. Everything was read in the client (Ghidra) and, where it says
*confirmed*, seen live or in game. Addresses are Ghidra addresses (`0x7ff64ffe0000 + RVA`).

## The first lobby message: `ClientOutLobbyConnect` 21800

Sent by the client on channel 29 as soon as the JAM link is up (147 bytes; it decodes with no byte left over).
Layout from Prometheus' protocol dump, values from a real run:

| Offset | Type | Value | Meaning (inferred) |
|---|---|---|---|
| `0x68` | cstring | `""` | |
| `0x78` | struct `{seq u64, src u64}` | `0, 0` | request envelope |
| `0x88` | u32 | `0` | |
| `0x90` | struct → cstring | `""` | |
| | struct → cstring | `"Overwatch/0.8.0.0 Beta/24919"` | client version |
| | struct → `u8[]` | 64 bytes | probably the Battle.net `session_key` from `OnLogonComplete` (unverified) |
| | struct → u32 ×3 | `0x0057696E` "Win", `0x656E5553` "enUS", `0x00006157` | platform, locale, unknown fourcc |
| | struct → `{u64, u64}` | `0x9B4D66`, `0x020000010050726F` | the game account EntityId (`low`, `high`) |

The connect sequence that answers it (20500, 20501, 20503) and every `ClientInLobby*` handler are in
[TitleScreen.md](../TitleScreen.md) §5; the server side is in [LobbyServer.md](../LobbyServer.md) and
`server/README.md` §6.2–6.3.

### Connection status table (`FUN_7ff6510b2c20`)

`FUN_7ff6510b2c20(table, slot, status record, reason string)` stores a per-connection status and notifies
listeners. Slot 2 = lobby, slot 3 = game instance ("ConnectToInstance"). Record `+0` status: 1 = disconnected,
2 = connecting (with an address string), 3 = connected, 4 = (with extra data), 5 = failed. Callers:
`FUN_7ff650c458f0` (JAM hand-off: lobby → 3, "Authentication add lobby connection", when the auth object's
`+0x94 == 1`), `FUN_7ff650c45a70` (lobby disconnect, also the 20502 handler), `FUN_7ff650c4fab0` (instance
connecting), `FUN_7ff650c4fc50` (instance disconnect); not read: `FUN_7ff650c45dc0`, `FUN_7ff650c469a0`,
`FUN_7ff650c46c10`, `FUN_7ff650c4fd40`, `FUN_7ff650c4ff50`. The lobby slot is already "connected" at
hand-off time; "Authenticating" on screen waits for 20501.

## What the social and chat UI sends

The receive side of chat, party and friends is in [TitleScreen.md](../TitleScreen.md) §5 and the screens in
[LobbyScripts.md](../LobbyScripts.md). Confirmed in game, the client sends:

| UI action | Message | Payload / reply that works |
|---|---|---|
| PLAY | `ClientOutLobbyMatchmaking` 22000 | `{{u64 seq, u64 src}, bool}`, 17 zero bytes; answer 25600 ([LobbyServer.md](../LobbyServer.md)) |
| Cancel matchmaking | 22001 | empty |
| PRIVATE GAME / dev private game / PRACTICE VS AI / TUTORIAL | `ClientOutLobbyCustomGame` 24001 | `{i8 type, cstring, cstring}`, type 1 / 2 / 3 / 4, empty strings |
| Cancel (custom-game flows) | 24007 | empty; answer 23308 |
| Add friend | `ClientOutLobbyFriends` 27000 | `{u64 cookie, u32 kind (1 BattleTag, 2 e-mail), str target, str message}` |
| Accept a friend request | 27001 | `{u64 cookie, ID, u32 0}`; answer 27104 (remove the request), 27101 (friend entry), 27109 (an account presence record + a game-account record with parent = the account) |
| `/removefriend` | 27002 | `{u64 cookie, ID}`; answer 27102 `{ID}` |
| Whisper (party-member menu) | 27004 | `{u64 cookie, ID account, ID game account, cstr text}` |
| Favourite | 27005 | `{u64 cookie, ID, u64 flags}` (1 = favourite); answer 27105 `{u32 1, u64 flags, ID}` |
| "+" on a friend, `/invite` | `ClientOutLobbyParty` 22101 | `{ID, bool}`; answer a new 20700 with the member added |
| Remove from party (X) | 22102 | `{ID}`; answer 20700 without the member |
| Promote to leader | 22103 | `{ID}`; answer 20700 with the leader bool moved |
| Leave party | 22104 | empty; answer a party of one |
| Chat line | `ClientOutLobbyChat` 21700 | `{channel {i32 type, ID}, cstr text}` (the client shows its own lines itself) |
| `/list` | 21701 | answer 20401 `{channel, []{cstr name, ID}}` |

**Presence records** (27100 list 3, 27109; handler `FUN_7ff650bf90a0`). Each attribute is a serialized bgs
`FieldKey {program 0x424E "BN", group, field}` plus a `Variant`. The second ID is the record's **parent**: null =
an account entry, otherwise a game-account entry under that account. **A record that is its own parent makes
a cycle and the client hangs.** Fields: `BN/1/4` = BattleTag (shown without `#1234`), `BN/1/1` = a "real name"
shown in its place with the tag in brackets, `BN/2/1` = online ("N ONLINE", the "IN GAMES" group), `BN/2/8` =
rich presence (a `$N` template expanded by `FUN_7ff650bf8da0`); `BN/1/3` is watched, meaning unknown.
- Names in the friends list come from the player-name cache (`FUN_7ff650cb5690(cache, ID, name)`), and the
  "+" on a friend without a name crashed the client.
- 27100 resets the friends list but not the client's presence map: a re-used game-account ID leaves a new
  friend without a game account, and "+" then says "Player Not Found" without sending anything. Fake-player
  IDs must be unique across server restarts.

**Chat channels are data**: the chat config asset (`FUN_7ff650cbcc00` → `FUN_7ff650c2b590(game)+0x88`) has, at
`+0x08`, 32-byte entries `{display string ref, RGB floats, i32 type}`: 1 Error (red), 2 System (yellow),
3 Whisper (pink; its name is the whisper target), 4 Party (light green), 5 Team (blue), 6 Match (orange),
7 Guild (neon green), 8 Guild Officer (dark green), 9 General (white), 11 and 12 Private Game (purple).
Type 0 isn't a channel. Joined channels (20403, flags 0) plus whisper targets are what TAB cycles through.

**Slash commands are data too**: the same asset's `+0x18` array, 20 entries of 56 bytes `{name ref,
description ref, aliases[], i32 action, i32 flag}`: `general` (`all`, `a`, `1`), `match` (`m`), `party` (`p`),
`team` (`t`), `privategame` (`pg`), `whisper` (`w`, `tell`, `send`), `reply` (`r`), `invite`, `kick`, `leader`
(`promote`), `leave`, `accept`, `decline`, `suggest`, `join`, `joinchannel`, `leavechannel`, `list`,
`removefriend`, `logout`. A server can't add slash commands (`/help` gives "Unknown chat command"; our servers
use `!` commands instead, `server/README.md` §4). `/join <name>` joins the matchmaking queue; `/joinchannel`
and `/leavechannel` send nothing.

**The dev console** sends its cheat commands over the *instance* connection (`ClientOutCheats`; "Ignoring
attempt to send cheat command" without one, `FUN_7ff650c50a40`). The `Command` / `CommandOut` JAM protocols
belong to a debug service hosted *by the game* (`FUN_7ff650882fd0`) for external tools, not to the lobby.

Display strings (`0x0DE…`) can be read live: a reference's load entry (pointer `& ~0x3F`) has the ID at
`+0x10`, and the text is at `**(entry+0)`.

## Feature flags and show-floor (kiosk) mode

- **Source:** the launch argument `--tank_FeatureFlags=id,value;id,value` (`0x` hex allowed), parsed by
  `FUN_7ff6507c48b0`, loaded by `FUN_7ff6507c4650`. The table is at RVA `0x1859D40`: about 31 records
  `{i32 id, i32 type, i32 value}`, defaults set by `FUN_7ff6507c4300` (0 = 1, 1 = 1, 2 = 0, 3 = 0, 4 = 0,
  5 = 1, 6 = 0, 7 = 1, 8 = 0, 9 (type 2) = 0, …). The value of flag `id` is at `0x7ff651839d48 + 12·id`.
- **Readers:** 0 chat manager; 6 Statescript accessor `FUN_7ff650f3cce0` (= !ShowMode && flag 6); 7
  `FUN_7ff650d55770` → view-model property `0xDA61E176`; 8 `LobbyMainMenuVM` `0x8C0B4AD2` (confirmed);
  **9 = ShowMode** (27 readers; the config var `0x221F6FB0`, [StatescriptConfigVarCatalogue.md](../StatescriptConfigVarCatalogue.md)); 10
  `FUN_7ff650d82b30` → `0x821D4BBE`; 11 `FUN_7ff650f3cb50` (= ShowMode && flag 11) and 4 others; 16 lobby
  connection code `FUN_7ff6510b3a90`; 18 lobby chat (`FUN_7ff650c5be50`, `c5bf40`, `c66330`, with the
  settings bytes 20802 writes at profile `+0x238`); 19 Statescript accessor (config var `0xf3f92e66`,
  gating PRIVATE GAME, [LobbyScripts.md](../LobbyScripts.md)); 26 console (`tank_Console`); 30 "publish" mode
  (`FUN_7ff6507c0b60`). Flags 1–5, 12–15, 17 and 20–29 have no readers. `tank_ShowMode` (which also resets
  many flags), `tank_Console` and `disableReplay` are read in the same place.
- **Show-floor mode** (`--tank_ShowMode=1`, flag 9), confirmed:
  - a Blizzard splash with START and POD / PARTY fields (Party 1 or 2). The pod → lobby table is
    `showmodeLobbyLocations.json` in the game's working directory, a JSON object `{"<pod id>": "host:port", …}`
    (loaded by `FUN_7ff650d8aa30`, parsed by `FUN_7ff650d8b620`, value checked by `FUN_7ff650d8a6b0`); without
    it: "An error occurred loading the config file";
  - START (`FUN_7ff650d8af70`) uses `--lobbyServer` if given, else the pod's address, and opens an ENTER A
    NAME screen (`LobbyShowModeEnterNameVM`; graph `0194`, [LobbyScripts.md](../LobbyScripts.md));
  - Enter Game (Statescript action `FUN_7ff650d8c390`) asserts `[game+0x1A0]+0x10 != 0` (`int3` at RVA
    `0xDAC517`, which silently kills the process under Proton), then passes the name to the lobby connection
    and connects. With that byte set to 1 live (the real setter is unknown; `--BNetEnabled=0` does not set it)
    the client connects **straight to the lobby with `cid 0` and empty session keys**, which our gateway
    accepts. Its 21800 is a different, 89-byte form (typed name, a LAN IPv4 as u32, the version string,
    "Win"/"enUS", no Battle.net key or account), and after the normal connect replies it joins the matchmaking
    queue by itself (22000). The console variables `showmode` / `PodId` are registered at `0x7ff6517986d0`.

## The instance link: transport and stream details

The main coverage is [InstanceServer.md](../InstanceServer.md) (§2 `inst/link`, §4), [CommandFrames.md](../CommandFrames.md),
[StatescriptSync.md](../StatescriptSync.md), [MatchLogic.md](../MatchLogic.md) §5–§8 and `server/README.md` §8.4–8.5.

**UDP transport functions:** `UdpTransport_OnDatagram_VerifyMAC` `0x7ff650e32ee0` (drops datagrams under 0x22
bytes, finds the connection by `header>>4`, verifies HMAC-MD5 with the key at conn+0xb), `UdpConn_HandleControlFlags`
`0x7ff650e34470` (handshake state machine), `UdpConn_SendControlPacket` `0x7ff650e34260`, `UdpConn_TickControl`
`0x7ff650e32a40` (resend every 250 ms, 10 s timeout), `UdpTransport_Tick` `0x7ff650e32510`,
`UdpTransport_SendDataPacket` `0x7ff650e327c0`; the ack walk is `FUN_7ff650e34a20` (sent-packet history of 0xa0-byte
entries against the peer's ack and 64-bit mask, bit i = seq ack−1−i). MD5 is the second implementation in the
binary (`MD5_Update` `0x7ff651330e50`).

Control packet (34 bytes): `[12] MAC | u32 connID<<4 | u32 seq | u32 ack | u64 ack mask | u8 flags | u8 0xad`,
flags SYN 1, ACK 2, FIN 4. Connection states (conn+0x50):

| state | sends every 250 ms | on receive |
|---|---|---|
| 0 | | SYN → 2 |
| 1 connecting | SYN | SYN\|ACK → 3, "connected" event |
| 2 | SYN\|ACK | ACK → 3 |
| 3 connected | ACK | FIN → 8 |
| 8 | FIN\|ACK | ACK → 10 |

States 1 and 2 die (10) after 10 s of silence, and a connected connection is dropped after 10 s without any
server packet (no FIN). A second 20600 while connected makes the client send FIN and return to the title
screen.

**Stream readers** (game system 45's link, `+0x20`; global read vfunc `+0x30`, entity read `+0x38`):

| stream | owner | global read | entity read |
|---|---|---|---|
| 0 | JAM | `FUN_7ff6510304c0` | — |
| 1 | system 22 (Statescript) | — | `FUN_7ff650c10110` → `c10140`; buffered up to 8 s if the entity or component isn't there yet |
| 2 | | — | `FUN_7ff650c14b70`: two optional parts, `d21e90` and `d16540` |
| 3 | system 15 (input ack, clock) | `FUN_7ff650c07430` | `c059b0` → `c05bb0` |
| 4 | system 8 (MirrorSystem) | `FUN_7ff650c02100` | `c002e0` → `bfff40`: entity operations |
| 5 | | `FUN_7ff650c53ed0` (header flag 0xF only) | — |

The server → client packet reader is `FUN_7ff650c500a0`; the client builds its own packets in system 45
(slot 13 `FUN_7ff650c52bc0`, an LSB-first bit writer).

**Definition create** (stream 4 op 1, `FUN_7ff650c00d20`, create bit 0): a prefix number (widths 12/20/32,
`0x7ff651716838`) = the definition index (GUID `0x0400000000000000 | index`), then `FUN_7ff650c018f0` reads a
**u7 flags** byte:
- `0x01`: an extra resource of type 0x75, or type 0xA5 plus a bit if `0x40` is set;
- `0x08`: vec3 position + vec4 rotation, then a vec3 (scale);
- `0x04`: only that last vec3;
- `0x02`: the movement / mirror snapshot (`FUN_7ff65100aba0`, MatchLogic.md §8.3);
- `0x10`: the **player loader** (vtable `0x7ff6515a2978`, the one Prometheus' `EntityLoader::Create` uses): adds
  the local-player components 0x2F + 0x33 when the entity is the local entity (`FUN_7ff650bfd7a0`), mode 3
  instead of 5;
- `0x20`: PlayerInfo (component 0x3F).

Flags `0x38` on the controller definition `0x…0E`, named local by 20301, give it components 0x1, 0x3, 0xE,
0x10, 0x13, 0x20, 0x21, 0x2F, 0x33, 0x34, 0x35, 0x3E, 0x3F, 0x79, 0x7A (confirmed). Op 3 (destroy,
`FUN_7ff650c00c80`) has no length and no data.

**Mirrored component updates** (op 2, `FUN_7ff650c00560`; every create also ends with this list): `u8 N`, then
N × (`u8 component ID`, component data); an unknown component ID asserts "Failed to deserialize component update
… k of N". From the next byte, `u8 L` + L bytes of **changed bits, one per field** of the mirror's STU type (own
fields in order, then the parent's; `FUN_7ff6508147d0`), then the changed fields' values, each through its field
type's reader (vfunc `+0xB0`) ([STU.md](../STU.md) §5). Layouts seen:
- **0x20** (mirror type `0x7ff65174d180`): 3 entity IDs at mirror `+0x78/+0x7C/+0x80` = component `+0x90` aim,
  `+0x94` camera, `+0x98` movement attach, each 32 raw bits. Update = `20 | pad | 01 07 | u32 × 3`. Its
  post-deserialize callback (`FUN_7ff650c08a40`) tells Statescript which entity is local when the aim field
  changes and writes the new entity's component 0x1F backref, so the backref itself is not sent.
- **0x1F** (mirror type `0x7ff65174d200`): one field at mirror `+0x78` (component `+0x98`).
- **0x28 health** (mirror at component `+0x20`, type `0x7ff65174d470`), 52 fields: 16 health parts (mirror
  `+0x78`), 16 armour (`+0x1D8`), 16 shield (`+0x318`), each 0x14 bytes (name hashes `0x3c195720` / `0x6fbcac70`
  / `0xc35434fe`), u32s at `+0x438/+0x43C`, bytes at `+0x440/+0x441`. A part is a nested struct (type
  `0x7ff65174d3f0`) written as all 7 fields with no changed bits: `u32 enabled | f32 max | f32 current | u32 |
  u8 | u8 | u8`. Update `28 | pad | 07 | mask 01 00 00 00 00 00 00 | part` with enabled 1, max = current = 200
  and the two flag bytes 1 removed the blood vignette on a server-spawned hero (confirmed).
