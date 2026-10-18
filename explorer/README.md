# Storage explorer

A desktop app for browsing the 0.8 beta's content offline. It's built on our own readers:
- `systems/content` (CASC/TACT/APM: every resource by GUID, type, package);
- `systems/stu` (STUD parse and decode against the client's type registry).

It's meant as the base for later tools, including an editor. Architecture and how to add a view:
[docs/Explorer.md](../docs/Explorer.md).

![browse](screenshots/browse.png)

## Build and run (Linux)

Requirements:
- Go ≥ 1.27, node + pnpm, CMake + Ninja (for the C++ readers);
- GTK 3 and **webkit2gtk-4.1** (Arch: `gtk3 webkit2gtk-4.1`).

Wails needs **v2.9 or newer** for webkit2gtk-4.1 (build tag `webkit2_41`). `make tools` installs Wails v2.16.0
into `explorer/.bin`. The global `~/go/bin/wails` 2.8.1 is too old.

```
cd explorer
make libs      # build systems/stu and systems/content (static libraries the cgo wrappers link)
make build     # installs .bin/wails if missing; pnpm install + nuxt generate + go build -> build/bin/owexplorer
make run       # or: ./build/bin/owexplorer [ow://<guid>[/<tab>[/<instance>]]]
make dev       # hot reload: Nuxt dev server on :3000 inside the Wails window
make test      # Go tests: every graph + entity definition through every view; index + search
```

The install is taken from, in order:
1. the last one opened;
2. `$OW_INSTALL`;
3. `<repo>/game/owbetaclient`.

**Open install…** picks another one. Names come from the repository's data files (`systems/data`,
`systems/stu/names`). The repo is found by walking up from the working directory or the binary; set
`$OW_REPO` to override. Pins and recents are stored in `~/.config/owexplorer/state.json`.

Command-line deep link: `owexplorer ow://058000000000000f/graph` opens a resource on a tab. Tabs: `info`, `map`, `texture`, `model`, `material`, `audio`, `graph`,
`entity`, `tree`, `deps`, `refs`, `hex`, `text`; with `tree` a third part selects an instance
(`ow://0400000000000039/tree/2`), with `map` a record (`ow://080000000000005b/map/1027`), with `texture` a mip (`ow://0c0000000000007e/texture/2`), with `model` a submesh
(`ow://0d000000000011ce/model/14`).

## Using it

- **Claude** (docs/Explorer.md "MCP server", "Ask Claude"): every window runs an MCP server on `127.0.0.1:17889` (next
  free port for more windows). With the `owexplorer` entry in `.mcp.json`, a Claude Code session can see what you are
  looking at (`current_view`) and explain it (`describe`, `entity`, `statescript_graph`, `placement`, …). It can open
  resources and change the selection (`open`, `select`) only while you enable "drive" on the status bar's MCP chip.
  **✦ Ask** on a selection sends the question to that session: tell it "watch the explorer" once, and its answers
  appear in the Ask panel.

- **Browse**: the left rail has four sections:
  - **Types** shows every resource type with its count. Labels come from the root STU type of each type's files; non-STU types use a small table, whose tooltip gives the source.
  - **Packages** shows 1332 asset packages, named after the asset they belong to ("TEMPLE OF ANUBIS", "Reinhardt").
    Picking one lists *every* resource it contains.
  - **Pinned** and **Recent**.
- **Search** (`/` or `Ctrl+K`):
  - Any part of a GUID's 16 hex digits, or a name.
  - A full GUID or `ow://…` link plus Enter opens it.
  - Names come from string assets, `m_name…` fields, package owners, the TSVs in `systems/data/lobby_scripts`, the map
    catalogue (`systems/data/maps/maps.json`: map names with mode) and `systems/data/heroes/heroes.json`. Italic names
    are guesses; the grey note says what a resource is ("map data · assault game map", "hero entity definition").
- **Links and tabs**: every GUID is a link. Ctrl- or middle-click opens it in a background tab, right-click gives a menu
  (new tab, dependency graph, copy GUID / `0x…` / `ow://`, reveal in the type list or package). Each document tab has its
  own history: `Alt+← / Alt+→`, the mouse's back / forward buttons or Backspace; breadcrumbs above the GUID.
  `Ctrl+W` closes a tab, `Ctrl+Tab` / `Ctrl+1…9` switch, `\` hides the left panes.
  Shift-click (or the menu / "New window") opens a second window; "Split" keeps a resource in a second pane on the right
  while the main one navigates. Every panel border can be dragged (double-click resets); sizes are remembered.
- **Resource view**:
  - The header shows the name and what it is, type, size, content key, reference counts and every package the resource
    is in (with its owner's name). Reveal selects it in the type list. Export saves the bytes, or for STU resources the
    decoded JSON (`stu_dump` format).
  - **Info** (every resource): decoder status, the type's size statistics, what it uses and what uses it by kind and type.
  - **Dependencies** (every resource): the dependency graph from the reference index: what it uses (right), what uses it
    (left), expanded node by node; big sides become one node per type, which opens a list. Filters by kind, type,
    package. docs/Explorer.md "Dependency graph".
  - **Tree**: the instance table (CRC status, errors) and the selected instance's fields. Names are resolved; `→ n` jumps to an instance; GUIDs open their resource; `ow://` links inside strings are clickable.
  - **Graph** (Statescript graphs):
    - A canvas at the editor's positions: drag to pan, scroll to zoom.
    - A node list by kind.
    - Node details: plugs and links in and out, parameters with a one-line summary of each config var.
  - **Map** (map data 002, map headers 09F): a top-down plot of every placement coloured by class (networked, mover,
    gameplay, prop, static, lights, effects, sound, …) with layer toggles; the placement list with definition names and
    placed indexes; a record's decoded fields and instance data; referenced assets; the header and environment
    (docs/Maps.md §2, docs/Explorer.md "Map view"). **3D** (toolbar 2D / 3D): the map's static collision mesh and
    shapes with the placements as markers, orbit / fly camera, the world position under the cursor (docs/Explorer.md
    "3D map mode"). `F` maximises the map to the window (`Esc` returns), `F11` goes fullscreen, and "Pop out" opens
    the 3D view in its own window.
  - **Texture** (textures 004, payloads 04D): the decoded image of any mip and surface (cube faces, or the whole cube
    unfolded as a cross), zoom / pan, channel view, HDR exposure, "Z from XY" for normal maps, pixel inspector; header,
    mips, payloads, delta base; Export PNG (this mip / surface) or Export all. Texture rows in lists show thumbnails
    ("▦ large" in the texture list for big ones). Every texture of the install decodes (docs/AssetFormats.md §1,
    docs/Explorer.md "Texture view").
  - **Model** (models 00C, looks 01A): a 3D preview of any LOD with the look's colour textures (look picker: the looks
    placed with the model), material / normal / plain shading, wireframe, skeleton, click-to-select submeshes; material
    slots, submeshes, vertex layouts, bones; Export glTF (.glb, textures inside) or OBJ (docs/AssetFormats.md §2–§4,
    docs/Explorer.md "Model view"). The 3D map mode has a "models (visual)" layer drawing the map's placed models.
  - **Material** (008): a live preview on a sphere / plane / cube (simple PBR from the material's textures, per-input
    toggles, channel view, light, tiling; T071), the shader family, the slot → input mapping with its confidence, texture
    bindings with slot names and thumbnails, the parameter stream, constant buffers. Material rows in lists get a
    rendered-sphere thumbnail.
  - **Audio** (WEMs 03F / 0B2, sounds 02C, banks 043, switch / state groups, switches, parameters 02D–030): play any WEM
    (waveform, seek, loop; decoded in Go, no external tools), export WAV / Ogg; a sound's WEMs and its event trees
    (Event → Actions → containers → Sound objects → WEMs); a bank's HIRC objects; a group's name, Wwise id and switches
    (docs/AssetFormats.md §5, docs/Explorer.md "Audio view").
  - **Components** (entity definitions): components, their graphs (with override counts) and references.
  - **References**: what the resource uses and what uses it, as lists with kind / type filters, search and "why" (the STU
    field path or map record). The reference index (STU fields, map placements / environment / instance data, packages)
    is built once in ~7 s and then cached (`~/.cache/owexplorer`, ~25 ms to load).
  - **Hex**: paged dump. **Text** shows string assets.
- Every GUID is drawn the same way: the first four digits in its type's colour, leading zeros faint.
- **Keys**:
  - `↑ ↓ PgUp PgDn Home End` in lists;
  - `Alt+← / Alt+→`, mouse back / forward, Backspace for history;
  - `Ctrl+W`, `Ctrl+Tab`, `Ctrl+1…9` for tabs; `\` hides the left panes;
  - `P` to pin;
  - `Esc` clears the search.

![dependency graph](screenshots/deps.png)
