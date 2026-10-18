# 02 — Battle.net login

The 0.8 client has the Battle.net client SDK built in (`3rdparty\Battle.net.client\vendor\src\low\…`). It logs in through Battle.net's **web authentication**, then asks Battle.net where the lobby is. We emulate all of this in `server/cmd/loginserver`.

## Complete flow (confirmed end to end)

```
C: TCP+TLS to --BNetServer (default beta.actual.battle.net:1119)
C→S ConnectionService.Connect            {use_bindless_rpc: true}
S→C   ConnectResponse
C→S AuthenticationService.Logon          {program "Pro", platform "Win", locale "enUS", version "1.0",
                                          application_version 24919, web_client_verification: true}
S→C   NoData
S→C ChallengeListener.OnExternalChallenge {payload_type "web_auth_url", payload = login URL}
C: HTTP GET  login URL  → JSON login form (the client draws its own login screen from it)
C: HTTP POST login URL  → {"authentication_state":"DONE","login_ticket":"…"}
C→S AuthenticationService.VerifyWebCredentials {web_credentials = ticket}
S→C   NoData
S→C AuthenticationListener.OnLogonComplete {error 0, account, game account(s), BattleTag, session_key, …}
C→S GameUtilitiesService.ProcessClientRequest {attribute build = 24919, game_account_id}
S→C   ClientResponse {k0, k1, k2, k3 (64-byte blobs), cid (uint), hostv4 (string)}
C: connects to hostv4 → lobby (see 03-lobby-handshake.md)
C→S ConnectionService.KeepAlive every ~21 s while idle
```

## Transport

### TLS

- The Battle.net SDK uses **Windows SChannel** (`vendor\src\low\ssl\ssl_impl\schannel\schannel_filter.cpp`). Under Proton, SChannel is Wine's, so the TLS handshake itself works with a Go server.
- The client speaks TLS on port 1119 from the first byte, offering **TLS 1.0–1.2**. It negotiated `TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256` with our Go server.
- **After the handshake, the SDK runs its own certificate check** (`FUN_7ff6510dfda0`, RVA `0x10FFDA0`). It returns 0 = trusted, otherwise an error code:

| Code | Meaning |
|---|---|
| 1 | remote address is not a hostname (**an IP like 127.0.0.1 always fails**) |
| `0x3F5` / `0x3F6` | no certificate / empty common name |
| `0x3F7` | common name doesn't match the host |
| `0x3FC` | chain doesn't contain "Battle.net Certificate Authority" (checked when pinning is off) |
| `0x3F9` | public-key pinning failed (checked when pinning is on) |

  Its only caller (`FUN_7ff6510dfb90`) marks the connection as established (state 3) on 0. Our Prometheus hook runs the original check, logs the result and returns 0. With our self-signed certificate it logs `0x3F7`. The server generates an RSA-2048 self-signed certificate at startup, so there are no certificate files to manage.

### Framing

```
[u16 big-endian header length][bgs.protocol.Header protobuf][payload, Header.size bytes]
```
- Requests: `service_id = 0`, `service_hash` = wire hash, `method_id`, `token`.
- Responses: `service_id = 0xFE`, same `token`. Error responses set `status`.
- The server calls the client's listener services the same way, with its own token counter.
- Bindless RPC (`use_bindless_rpc: true`): services are addressed by hash, with no Bind step.

### Service hashes

On the wire the client uses the **FNV-1a hash of the original `bnet.protocol.*` name**, not of the `bgs.protocol.*` name in the protos. Both sets of hashes are present in the binary; the old ones are what gets sent.

| Proto service | Wire name | Wire hash | Proto-name hash |
|---|---|---|---|
| connection.v1.ConnectionService | `bnet.protocol.connection.ConnectionService` | `0x65446991` | `0x2782094B` |
| authentication.v1.AuthenticationService | `bnet.protocol.authentication.AuthenticationServer` | `0x0DECFC01` | `0xFF5A6AC3` |
| authentication.v1.AuthenticationListener | `bnet.protocol.authentication.AuthenticationClient` | `0x71240E35` | `0x4DA86228` |
| challenge.v1.ChallengeService | `bnet.protocol.challenge.ChallengeService` | `0xDBBF6F19` | `0x71BB6833` |
| challenge.v1.ChallengeListener | `bnet.protocol.challenge.ChallengeNotify` | `0xBBDA171F` | `0xC6D90AB8` |
| game_utilities.v1.GameUtilitiesService | `bnet.protocol.game_utilities.GameUtilities` | `0x3FC1274D` | `0x51923A28` |

The client registers **exactly these six services**. The extracted protos define exactly these six; the other `*_types.proto` files only provide message types.

### Method IDs

These **do not follow declaration order**. They were read from the client's registration tables (e.g. `FUN_7ff6510c9960` for the AuthenticationListener), where each method appears as `(proto-name hash << 32) | [0x80000000 |] id`. Methods not listed aren't registered by this client.

| Service | Method IDs |
|---|---|
| ConnectionService | 1 Connect, 3 Echo, 4 ForceDisconnect, 5 KeepAlive, 7 RequestDisconnect |
| AuthenticationService | 1 Logon, 5 GenerateSSOToken, 7 VerifyWebCredentials, 8 GenerateWebCredentials |
| AuthenticationListener | 4 OnServerStateChange, **5 OnLogonComplete**, 6 OnMemModuleLoad, 10 OnLogonUpdate, 11 OnVersionInfoUpdated, 12 OnLogonQueueUpdate, 13 OnLogonQueueEnd |
| ChallengeService | 1 ChallengePicked, 2 ChallengeAnswered, 3 ChallengeCancelled, 4 SendChallengeToUser |
| ChallengeListener | 1 OnChallengeUser, 2 OnChallengeResult, 3 OnExternalChallenge, 4 OnExternalChallengeResult |
| GameUtilitiesService | 1 ProcessClientRequest, 2 PresenceChannelCreated, 3 GetPlayerVariables, **6** ProcessServerRequest, 7 OnGameAccountOnline, 8 OnGameAccountOffline, 9 GetAchievementsFile, 10 GetAllValuesForAttribute |

Lesson learned: `OnLogonComplete` sent as method 4 is read as `OnServerStateChange`, and the client just times out ("Time out communicating with Battle.net").

## The protos

The `.proto` files extracted from the client (`bgs/low/pb/client/*.proto`, copies in [`protos/`](../../protos/)) are faithful, with two issues:
- `channel_types.proto`: `[default = default]` must be `[default = "default"]`.
- The `global_extensions/*_options.proto` files are empty, so they carry no method IDs (see the table above).

`server/proto/` holds a copy rearranged into one directory per proto package (Go requires it, and names like `Identity` clash between packages), with rewritten imports and the fix applied. The reconstructed `google/protobuf/descriptor.proto` is dropped in favour of buf's standard one. Go code is generated with buf's managed mode (`go_package_prefix owserver/gen`).

## Login states (client)

The login system's state names come from a string table (pointer table at `0x7ff6517b2ab8`):

| # | State | # | State |
|---|---|---|---|
| 0 | WAIT_TO_START | 9 | WAITING_FOR_USER_CAPTCHA |
| 1 | FAILED | 10 | WAITING_FOR_USER_AUTHENTICATOR |
| 2 | INITIAL_PSN_OR_XBL_AUTH | 11 | WAITING_FOR_BNET_LEGAL |
| 3 | CONNECTING_TO_BNET | 12 | WAITING_FOR_USER_LEGAL |
| 4 | WAITING_FOR_BNET_EXTERNAL_CHALLENGE | 13 | WAITING_FOR_HEADLESS_ACCOUNT_CREATION |
| 5 | WAITING_FOR_PSN_OR_XBL_AUTH | 14 | ATTEMPTING_LOGIN |
| 6 | WAITING_FOR_BNET_JSON_AUTH_GET | 15 | VERIFY_CREDENTIAL |
| 7 | WAITING_FOR_USER_ACCOUNT_AND_PASSWORD | 16 | AUTHENTICATED |
| 8 | WAITING_FOR_BNET_CAPTCHA | | |

Error `0xDE0000000001BB1` is shown as "invalid response received" and is used by every JSON/cookie parsing failure.

## The web login

### External challenge

`OnExternalChallenge` handler: `FUN_7ff6510b4920`. It copies **field 3 (`payload`) as the URL** (`request_token`, `payload_type` and `payload` are the message's three strings at `+0x18/+0x20/+0x28`), sets login state 6 and starts an HTTP job (`FUN_7ff6510b6610`, libcurl). The job sends `Accept-Language`, `User-Agent: Mozilla/4.0`, `Content-type: application/json;charset=utf-8` and `Accept: application/json;charset=utf-8`. `payload_type` isn't checked. **Plain `http://` works.**

### Cookies (required on every response)

`FUN_7ff6510b8e00` searches the **raw response headers** for `JSESSIONID`, then `web.id`, and reads each value from after `=` up to the next `;`. If either is missing it fails ("No jsessionID" / "No WebID"), which shows up as "invalid response received". So each value **must be followed by `;`**, e.g.:
```
Set-Cookie: JSESSIONID=<hex>; Path=/
Set-Cookie: web.id=<hex>; Path=/
```
The client keeps both values (the format string `Cookie: JSESSIONID=%s; web.id=%s` exists) and sends them back.

### GET → login form

`__ParseJsonAuthGet` (`FUN_7ff6510b9d80`) and `__ExtractJsonAuthGetInputs` (`FUN_7ff6510b9220`):
```json
{"type":"LOGIN_FORM","inputs":[
  {"input_id":"account_name","type":"text","label":"E-mail","max_length":320},
  {"input_id":"password","type":"password","label":"Password","max_length":128},
  {"input_id":"log_in_submit","type":"submit","label":"Log In"}]}
```
- The root must be an object with `"type":"LOGIN_FORM"` and an `"inputs"` array.
- Each input needs a string `input_id` and a string `label`. `max_length` must be an integer if present, and it's **required** for `account_name`, `password` and `captcha`.
- Accepted `input_id`s: `account_name`, `password`, `captcha`, `log_in_submit`. Anything else → "unknown input_id".
- An optional root `"captcha_url"` switches to the captcha flow. Without it: state 7 (the login screen).

### POST ← credentials

Built by `FUN_7ff6510b5e50` from these format strings, in order:
```
{ "version": "1.0", "program_id": "Pro", "platform_id": "Win", "inputs": [ { "input_id": "…", "value": "…" }, … ] }
```

### POST → result

`__ParseJsonAuthPost` accepts an `authentication_state` of:
- `DONE`: requires `login_ticket`.
- `LOGIN`: requires `error_code` and `error_message`; the form is shown again.
- `LEGAL` (`legal_form`, `next_url`, agreements).
- `AUTHENTICATOR` (`authenticator_form`, `next_url`).
- `CAPTCHA_REQUIRED` (`url`).

## `OnLogonComplete` checks (`FUN_7ff6510b5580`)

- `error_code` must be 0 (else "LogonComplete with an error").
- At least one `game_account_id` must be an Overwatch account (else "LogonComplete with no prometheus game accounts"; Prometheus was Overwatch's codename):
  - `high >> 56` (type) == `0x02`
  - `high & 0xFFFFFFFF` (program fourcc) == `0x50726F` ("Pro")
  - `(high >> 32) & 0xFF` (region) ≠ 0
  - `low` ≠ 0
- We send account `high = 0x0100000000000000`, game account `high = 0x020000010050726F` (region 1), `low` = a stable ID derived from the account name.
- On success: login state 16 `AUTHENTICATED`, credentials wiped (`FUN_7ff6510b5ac0`).

## Lobby referral (`GameUtilitiesService.ProcessClientRequest`)

The lobby connection system (`LobbyClientConnectionSystem`) states: `DISCONNECTED`, `WAIT_REFERRAL`, `AUTH_LOBBY`, `CONNECTING_LOBBY`, `HANDING_OFF_JAM`, `CONNECTED`, `FAILED`.

- Request (built by `FUN_7ff6510bfcd0`): attribute `build` (uint 24919), optionally `segregation_key` and `cluster`, plus the game account. While in `WAIT_REFERRAL` and unanswered, it's **re-sent every 60 s**.
- Response callback `FUN_7ff6510c01c0` requires **all six** attributes, otherwise the referral fails:

| Attribute | Variant field | Content |
|---|---|---|
| `k0` `k1` `k2` `k3` | `blob_value`, **exactly 64 bytes** | lobby session keys |
| `cid` | `uint_value` | client ID (the first thing the client sends the lobby) |
| `hostv4` | `string_value` | lobby address `host:port` (e.g. `127.0.0.1:3724`) |

  The attribute names are XOR-encrypted strings in the binary ([01-static-analysis.md](01-static-analysis.md#xor-encrypted-strings)).
- After a referral the client connects to `hostv4`. If the lobby connection fails, it asks for a **new referral about every 5 s**, getting a fresh `cid` each time.

Our key scheme (a server-side choice, not required by the client): `k_i = HMAC-SHA512(shared secret, "k<i>" ‖ cid as u64 LE)`. The lobby server recomputes the keys from the `cid` the client presents, so the two servers share only the secret.


## Auto login (2026-09-24)

The client needs no web login at all: if the server answers `Logon` by sending `OnLogonComplete` straight away (the same call normally sent after `VerifyWebCredentials`), the client goes directly from "Connecting" to the lobby, with no login form. Confirmed in game.

- `loginserver --web-auto-login` (default **true**) logs everyone in as `--web-default-account` (default `player@local`).
- With `--web-auto-login=false` the web login flow runs. The client always draws its **own** native e-mail/password form: our `LOGIN_FORM` input list does not change what it shows (a button-only list still showed both fields). Empty fields are accepted and mapped to the default account.

## Functions and data (Ghidra addresses; RVA = address − `0x7ff64ffe0000`)

| Ghidra | Purpose |
|---|---|
| `0x7ff6510dfda0` | Verify the server certificate after the SChannel handshake (hooked by Prometheus' `BnetTlsBypass`) |
| `0x7ff6510dfb90` | SChannel filter step; only caller of the check (0 → connection state 3) |
| `0x7ff6510dea70` | "Chain contains 'Battle.net Certificate Authority'" |
| `0x7ff6510c9960` / `0x7ff6510c7910` | Register the AuthenticationListener / AuthenticationService method IDs |
| `0x7ff6510b4920` | OnExternalChallenge handler (payload → URL, state 6) |
| `0x7ff6510b6610` | HTTP GET job for the web login URL |
| `0x7ff6510b8e00` | Extract `JSESSIONID` / `web.id` from the response headers |
| `0x7ff6510b9d80` / `0x7ff6510b9220` | `__ParseJsonAuthGet` (`LOGIN_FORM`) / `__ExtractJsonAuthGetInputs` |
| `0x7ff6510b5e50` | Build the POST body (`__SubmitLogin`) |
| `0x7ff6510b5580` | OnLogonComplete handler (game account check) |
| `0x7ff6510b5ac0` | Wipe the typed credentials |
| `0x7ff6510bfcd0` | Build ProcessClientRequest (`build`, `segregation_key`, `cluster`) |
| `0x7ff6510c01c0` | Parse the referral ClientResponse (`k0`–`k3`, `cid`, `hostv4`) |
| `0x7ff650d8af70` | Region/lobby picker using `--lobbyServer` (Statescript-driven) |
| `0x7ff6517b2ab8` | Pointer table of the login state names |
| `0x7ff651604848` | "Battle.net Certificate Authority" |
| `0x7ff6517e8bdc…8cec` | `AuthenticationSystem`, `LobbyClientConnectionSystem` state names |
