# 03 — Lobby handshake (before JAM)

After the referral, the client opens a plain TCP connection to `hostv4`. Before any JAM traffic, the client's `LobbyClientPacketPipe` runs a fixed-size negotiation that authenticates both sides with `k0`/`k1`, sets up the stream cipher from `k2`/`k3`, and ends with a 292-byte encrypted "state message". Implemented in `server/internal/lobby/handshake.go`; **works against the real client**.

Client code: `FUN_7ff6510c2500` (RVA `0x10E2500`), a state machine with the state at `+0x60` of the connection object. The first message is sent by `FUN_7ff6510c23f0`.

## Sequence

```
C→S  "HELLO PRO CLIENT\0"                               17 bytes
S→C  "HELLO PRO SERVER\0"                               17        client state 0 → 1
C→S  [u64 LE cid][32-byte cn]                           40        cn = client nonce (at +0x190)
S→C  [32-byte challenge][u8 difficulty][32-byte sn]     65        client state 1 → 2
C→S  [u64 LE proof-of-work counter][MAC1]               40
S→C  MAC2                                               32        client state 2 → 4
S→C  JAM_s2c( blob[256] ‖ JamID ‖ JamID ‖ u32 )         292       client state 4 → 6, hand-off to JAM
```

- `cid` is the `cid` from the referral. The lobby server uses it to recompute `k0`–`k3`.
- **Keys:** `k0`–`k3` sit in the connection object at `+0x68`, `+0xA8`, `+0xE8` and `+0x128`, 64 bytes each.
- **MAC1** = `HMAC-SHA256(k0, cn ‖ sn)`. **MAC2** = `HMAC-SHA256(k1, sn ‖ cn)`. MAC2 is sent in the clear.
- **Cipher keys** (`FUN_7ff6510c30f0`, called before MAC1 is sent):
  - server→client: `HMAC-SHA256(k2, cn ‖ sn)`
  - client→server: `HMAC-SHA256(k3, sn ‖ cn)`
- The keystream **runs continuously** across the whole connection: the blob, then every JAM frame, in each direction.

### Proof of work (`FUN_7ff6510c2fa0`)

The client hashes `challenge ‖ counter` (a 20-byte hash, likely SHA-1), with the counter starting at 1, until the hash has at least `difficulty` zero bits (counted from bit 0 of byte 0 upward). Difficulties above `0xA0` fail. **With difficulty 0 the client sends counter 1 without searching.** The server doesn't verify it.

## The JAM stream cipher

A 32-byte-key stream cipher: 16-word state, a 32-slot ring of 8 words, `absorb` ×2 of the key words, then `permute` ×32. Each keystream block is 32 bytes (state words 14, 10, 6, 2, 15, 11, 7, 3), followed by `permute(1)`. Our implementation is `server/internal/lobby/jam.go`.

- Known-answer test (key `00 01 … 1f`), first 64 bytes of keystream:
  `f07e5df1c8d51c0d754da6ddde34cca01db0ed26334eb94ae277135a0764c6a6794d7ab0cc0978ef43de30766a7ee1d7c90e159df239d4016d450fa3d8207842`
- It works with the real client in both directions: the client decrypts our blob and JAM frames, and we decrypt its frames.

## The state message (292 bytes)

It's encrypted with the server→client cipher, sent right after MAC2, and read by the client in state 4.

```
blob [256]  |  JamID A [16]  |  JamID B [16]  |  u32 [4]
```

### Blob layout

| Offset | Content |
|---|---|
| `0x00` | address type: 1 = IPv4 (4 bytes), 2 = IPv6 (16 bytes), 3 = null-terminated hostname |
| `0x01` | address (we send IPv4 `127.0.0.1`) |
| after the address | u16 port (we send it little-endian) |
| … | anything (we send zeros) |
| `0xB0..0xD0` | `HMAC-SHA256(blobKey, blob[0x00:0xB0] ‖ blob[0xD0:0x100])` |
| `0xD0..0x100` | anything (covered by the HMAC) |

- Address parser: `FUN_7ff650e3dc40`, reading from a reader limited to `0x13` bytes. The client rejects the blob only if **both** the address and the port differ from its connection's.
- **`blobKey`** (64 bytes at `0x7ff6514bcb80`, checked by `FUN_7ff65110a400`) is:
  `3586f3628a631b705712405b8acc71d40fd1670cc1b03ea384974a6fb1a76196b142f0b72310ea8116d00a4c352f09acdbfb50a63ec5153e62e4d67fe09beecc`

### The RSA step (bypassed)

Before the HMAC check, the client runs **`FUN_7ff651339b10(key object, blob, 0x100)`**, RVA `0x1359B10`. It imports the blob as a big number, checks it's below the modulus (`key+0x10`), computes **`blob^e mod n`** (exponent at `key+0x30`) and writes the result back. It's timed with `QueryPerformanceCounter` on both sides.

A real server sends a blob made with Blizzard's **private key**, which we don't have. The function has **no other callers**, so Prometheus hooks it to return without changing the blob (`Network/LobbyBlobBypass`). The blob then only has to pass the HMAC.

A cleaner alternative, not implemented: replace the modulus/exponent in memory with our own RSA public key and sign the blob server-side.

### Trailer: the two JamIDs

After the blob, the client reads two 16-byte values (as four u64s into `+0x1D0..+0x1E8`) and a u32. It passes the two IDs to the JAM hand-off (`FUN_7ff6510c3340` → `vtable+0x98`). They are the **client's JamID and the server's JamID, in that order**. Confirmed experimentally: with the server's first, the client didn't see a lobby server type.

The **server type is read from bits 40–55 of the first u64 of the server's JamID**. It must be **13 (lobby)**, otherwise the client offers no lobby protocols in the JAM negotiation (see [JAM.md](../JAM.md) §12.5). We send random JamIDs with type 13 for the server and type 0 for the client. The u32 is zero.

After state 6 the client switches to JAM. From here on every byte is JAM-encrypted and framed (see [JAM.md](../JAM.md) §12.5).

## Failure behaviour

- Any mismatch (MAC1, blob HMAC, address+port) → the client closes the connection. The lobby connection system then requests a new referral from Battle.net about every 5 seconds.
- If the negotiation completes but JAM fails, the same 5-second retry loop runs.

## Functions and data (Ghidra addresses; RVA = address − `0x7ff64ffe0000`)

| Ghidra | Purpose |
|---|---|
| `0x7ff6510c23f0` | Pipe event handler (sends `HELLO PRO CLIENT`) |
| `0x7ff6510c2500` | Negotiation state machine (states 0/1/2/4 → 6) |
| `0x7ff6510c2fa0` | Proof-of-work solver |
| `0x7ff6510c30f0` | Derive the stream cipher keys (`k2`/`k3`) |
| `0x7ff6510c3340` | Hand-off to JAM (`vtable+0x98` with the two JamIDs) |
| `0x7ff65110a400` | State blob HMAC validation |
| `0x7ff651339b10` | RSA public transform on the blob (hooked by Prometheus' `LobbyBlobBypass`) |
| `0x7ff650e3dc40` | Read a type-tagged address from a buffer |
| `0x7ff6514bcb80` | The 64-byte blob HMAC key |
| `0x7ff6517e8e2c` / `…8e4c` | "HELLO PRO CLIENT" / "HELLO PRO SERVER" |
