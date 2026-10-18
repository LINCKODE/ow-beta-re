# 01 — Static analysis tips and launch options

Techniques that worked for reading the 0.8 client (`GameClientApp.exe`, build 24919) and the launch options it
accepts. Building and running Prometheus and the servers is in the [top-level README](../../README.md) and
[PrometheusChanges.md](../PrometheusChanges.md) (Linux build).

## Static analysis of the client

### Ghidra

- Ghidra is loaded with the decrypted dump of the client. The GhidraMCP plugin serves `http://127.0.0.1:8080/`, and an MCP bridge connects MCP clients to it.
- **Ghidra image base: `0x7ff64ffe0000`.** RVA = Ghidra address − `0x7ff64ffe0000`.
- **Runtime game base: `0x140000000`** (logged by Prometheus). Runtime address = `0x140000000` + RVA. Prometheus hooks use `globals::gameBase + RVA`.
- Function and message names are mostly stripped, but the Battle.net SDK, JAM and login code keep many strings.

### Reading the dump file directly

- **File offsets are not RVAs** in this dump. Map an address through the PE section table (`raw + (rva − va)` for the section containing it).
- `llvm-objdump` from llvm-mingw can disassemble any range, including code Ghidra hasn't defined as a function:
  `llvm-objdump -d --no-show-raw-insn --start-address=0x… --stop-address=0x… GameClientApp_dump_SCY.exetreated.exe` (addresses are Ghidra VAs; the dump's PE image base is `0x7ff64ffe0000`).

### XOR-encrypted strings

Many client strings are stored encrypted and decrypted in place on first use:
```
[8-byte XOR key][u32: length (low 24 bits) | encrypted flag (top byte)][string bytes]
```
The code refers to the string at `key + 12`. Plaintext byte `i` = stored byte `i` XOR `key[i & 7]` while the flag byte is non-zero. The dump was taken after many strings had been decrypted, so most are already plaintext. Some short ones (e.g. `hostv4`, `type`) aren't listed as strings by Ghidra and have to be read at `key + 12`.

### Tricks that worked well

- **Method ID tables:** services register each method as a 64-bit immediate `(proto-name FNV hash << 32) | [0x80000000 |] method_id` next to a pointer to the method name. Scanning `.text` for `mov r64, imm64` with a known hash in the high dword recovered every service's method IDs. See [02-battlenet-login.md](02-battlenet-login.md) (Method IDs).
- **Protocol registrations:** each JAM protocol is registered by a function that pushes `{crc, base, max, …, server type}` as immediates right before calling `FUN_7ff650f064a0`. Disassembling around the CRC constant gives base, max and server type.
- **Runtime diagnostics beat guessing.** A hook that logs the call stack (as RVAs) of `JamLink::Close` found a rejection reason in one run. See [PrometheusChanges.md](../PrometheusChanges.md) (`JamDiagnostics`).

## Game command-line options (reported by the client)

The ones that matter for local servers: `--BNetServer`, `--BNetEnabled`, `--lobbyServer`, `--instanceServer`, `--cluster`, `--BNetSegKey`, `--account`, `--QuickLogin`, `--QuickLoginUsername`, `--noautoconnect`, `--console`, `--tank_logpath`, `--matchmake`, `--key`.

Read by the feature-flag code ([04-lobby-and-instance-notes.md](04-lobby-and-instance-notes.md#feature-flags-and-show-floor-kiosk-mode)):
`--tank_FeatureFlags=id,value;id,value`, `--tank_ShowMode`, `--tank_Console` and `disableReplay`.
