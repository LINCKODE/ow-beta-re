# systems/content: offline content access

Reads the game's resources by GUID from a local Overwatch beta 0.8 (build 24919) install, without a running
client. A small interface (`content::Store`) hides the storage; the default implementation reads the
install's local CASC/TACT/APM with our own code (no CascLib/TACTLib; zlib for inflate). Formats and findings:
[docs/ContentAccess.md](../../docs/ContentAccess.md).

```
cmake -S systems/content -B systems/content/build -G Ninja && cmake --build systems/content/build
ctest --test-dir systems/content/build            # unit tests + tests on game/owbetaclient (skipped if absent)
(cd systems/content/go && go test -count=1 ./...)  # Go wrapper (needs systems/stu/build too: tests parse with owstu)

B=systems/content/build
$B/content_dump --info                             # build, index sizes, 1332 packages, 152058 resources (150898 GUIDs + 1160 bundles)
$B/content_dump --list-types                       # count + bytes per resource type
$B/content_dump --list 1B                          # all Statescript graphs (GUID type size flags CKey package)
$B/content_dump --read 0x058000000000000F g.stud   # a resource's bytes
systems/stu/build/stu_dump g.stud                  # … parsed by the STU reader
$B/content_dump --check                            # read + MD5-verify every resource (~30 s)
```
`content_dump` takes the install from `--install DIR`, else `$OW_INSTALL`, else `game/owbetaclient` (run it
from `new-stuff/`).

| path | what |
|---|---|
| `include/content/store.h` | **the interface**: `Store` (Packages, Resources, Find, Read(guid), ReadCKey, PackageResources, ResourcePackages), `GuidType`, `OpenOw08Install` |
| `include/content/ow08/ow08.h` | the 0.8 backend's layers, public for tests/tools: crypto (Salsa20, MD5, keyring), CASC (idx, records, BLTE), TACT (build info/config, encoding, root), APM + packages, `Ow08Store` |
| `src/` | `crypto.cpp`, `casc.cpp`, `tact.cpp`, `apm.cpp`, `store.cpp`, `capi.cpp` |
| `capi/content.h` | C API (`content_open_ow08[_ex]` with options/extra keys, `content_find`, `content_read`, bulk and by-type resource lists, …) for cgo / ctypes; `libow_content_c.so` / `libow_content_c_static.a` |
| `go/` | Go module `owcontent`, package `content` (cgo over the static C API): `Open`, `Find`, `Read`, `ReadCKey`, `Resources`, `ResourcesOfType`, `Packages`, `PackageResources`, `ResourcePackages` |
| `tools/content_dump.cpp` | CLI |
| `tests/` | synthetic unit tests; install tests (layer counts, both test resources equal the STU fixtures' decoded trees, all 370 graphs + 967 entity definitions parse with every layout CRC, MD5 sample); C API test (C99) |

## Using it

```cpp
#include "content/store.h"
content::Error err;
auto store = content::OpenOw08Install("game/owbetaclient", {}, &err);   // ~0.2 s (warm cache)
content::Bytes graph;
if (!store->Read(0x058000000000000F, graph, &err)) { /* err.message */ }
for (auto& r : store->Resources()) if (content::GuidType(r.guid) == 0x01B) { /* every graph */ }
```
- `Read` is thread-safe (each read opens its own file stream).
- Options (`Ow08Options`): region/locale of the manifests (default `RDEV` / `enUS`; `RCN` lists the same
  CKeys), speech manifest on/off, MD5 verification on every read, keyring (built-in 0.8 keys + extra keys).

From Go (`systems/content/go`, links `build/libow_content_c_static.a` + `libow_content.a` + zlib):
```go
import "owcontent/content"
s, err := content.Open("game/owbetaclient", nil)        // *content.Options: region/locale, VerifyMD5, ExtraKeys…
defer s.Close()
b, err := s.Read(0x058000000000000F)                    // Go copy; errors.Is(err, content.ErrNotFound)
graphs := s.ResourcesOfType(content.TypeStatescriptGraph) // 370, sorted by GUID
```
A `Store` is safe for concurrent use; `Close` waits for running calls, later calls return `ErrClosed`.

## Another backend

Derive from `content::Store` (and add an `Open…` factory next to `OpenOw08Install`). Nothing above the
interface knows about CASC: the C API, the tools and the tests only use `Store` (except `content_dump
--info/--root/--read-file`, which print backend details when the store is an `Ow08Store`).
