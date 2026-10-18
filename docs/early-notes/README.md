# Early notes: login, lobby handshake and first-bring-up details

The first notes of the project, written while getting the Overwatch 0.8 beta client (build 24919) to log in
and connect to our own servers. What the main documents in [`docs/`](../README.md) now cover has been removed
from here; these files are kept because their content is still correct and not written down anywhere else.
Unless a statement is marked *assumption*, *inferred* or *unverified*, it was confirmed against the client's
code (Ghidra), the running client, or both.

| File | Contents | Main docs on the same subject |
|---|---|---|
| [01-static-analysis.md](01-static-analysis.md) | Ghidra and address conventions, reading the dump file directly, the XOR-encrypted string format, RE tricks (method-ID tables, protocol registrations), the client's launch options | [README](../README.md) (conventions) |
| [02-battlenet-login.md](02-battlenet-login.md) | Battle.net RPC transport, the certificate check, service hashes and method IDs, the web login (JSON, cookies), login states, `OnLogonComplete` checks, the lobby referral (`k0`–`k3`, `cid`, `hostv4`), auto login, function table | `server/README.md` §6.1, §8.1 |
| [03-lobby-handshake.md](03-lobby-handshake.md) | The `HELLO PRO` negotiation, proof of work, MACs, cipher keys, the JAM stream cipher (known-answer test), the 292-byte state message (blob layout, HMAC key, RSA step, JamID trailer), failure behaviour, function table | `server/README.md` §8.2 |
| [04-lobby-and-instance-notes.md](04-lobby-and-instance-notes.md) | `ClientOutLobbyConnect` 21800 decoded, the connection status table, what the social and chat UI sends, presence fields, chat channel and slash-command data, feature flags and show-floor (kiosk) mode, UDP transport states, stream readers, the definition-create flags, mirrored component updates (0x20, 0x1F, 0x28 health) | [TitleScreen.md](../TitleScreen.md), [LobbyServer.md](../LobbyServer.md), [LobbyScripts.md](../LobbyScripts.md), [InstanceServer.md](../InstanceServer.md), [MatchLogic.md](../MatchLogic.md) |

The JAM frame format, the negotiation protocol `0x11D82194`, the link state machine and the lobby channel table
from these notes are now in [JAM.md](../JAM.md) §12.5. The Battle.net protos extracted from the client are in
[`protos/`](../../protos/), the generated Go code in `server/gen/`.
