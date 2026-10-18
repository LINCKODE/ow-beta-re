# Storage explorer (`explorer/`)

A desktop app for browsing the 0.8 install offline, over `systems/content`, `systems/stu` and `systems/maps`. Build,
run and usage are in [explorer/README.md](../explorer/README.md). This page covers the architecture and how to extend
it (T034, 2026-09-26; map view T045; reference index, dependency graph and navigation T046, 2026-09-28; textures T063;
3D map mode T066; MCP server and Ask Claude T067; models, materials, looks and the map's visual geometry T064; audio T065).

## Architecture

```
frontend/ (Nuxt 3 SPA, Vue 3, Tailwind v4) ──Wails bindings (JSON)──▶ app.go (App: bound methods)
                                                                        │
                        internal/catalog:  install model (indices, names, search, doc cache, neighbours, type stats)
                        internal/refindex: reference index: sources → CSR graph, disk cache (see below)
                        internal/view:     STU → UI structures (tree, graph, entity, text, reference paths)
                        internal/mapview:  map data → map view (T045), 3D collision + ray picks (T066)
                        internal/texture:  textures + payloads → images, thumbnails, index source (T063)
                        internal/model:    models, materials, looks → meshes, glTF / OBJ, software render, index source (T064)
                        internal/audio:    WEM → Vorbis (codebook library) → PCM / Ogg / WAV, Wwise banks, sounds, index source (T065)
                        internal/names:    names from systems/data + systems/stu/names
                                                                        │
                        owcontent/content (cgo) ── systems/content   owstu/stu (cgo) ── systems/stu
```

- **Stack**: Wails v2.16, with Go on the backend and the system webview (webkit2gtk-4.1, build tag `webkit2_41`).
  - Nuxt 3.21 runs in SPA mode (`ssr: false`). `nuxt generate` writes `frontend/dist`, which `main.go` embeds.
  - Tailwind v4 via `@tailwindcss/vite`. Fonts are bundled from @fontsource (Instrument Sans, JetBrains Mono).
  - Nuxt's SPA integration worked as is, so there was no need to fall back to Wails' Vue + Vite template.
- **GUIDs cross the boundary as 16-digit hex strings.** JavaScript numbers can't hold a u64.
- **`internal/catalog`**:
  - The sorted resource list and per-type rows. Each type is labelled with the root STU type of its smallest file (read at open), or `fixedTypes` for non-STU types.
  - Package rows, and a paged `List(Query)` over type, package and search. The last filter is cached, so scrolling a 152k-row list only fetches pages.
  - An LRU of 12 parsed `stu.Document`s, each with its own lock (documents are not thread-safe).
  - The **reference index** (`internal/refindex`, next section), loaded from the disk cache or built in the background
    once per install. Names found in the content (string assets, name fields, package owners) come with it.
  - [L] Opening the install takes 0.7 s; the index builds in 7.0 s the first time and loads from the cache in 25 ms.
- **`internal/view`**: pure functions from a `stu.Document` to JSON-friendly structs.
  - `Describe`: the instance table.
  - `Tree`: `Node`s. Every node has a stable `Path` (`i<instance>.f<fieldhash>.[i].k<key>`) for a future editor.
  - `BuildGraph`: nodes in `m_nodes` order and their kind (via `stu.IsA` on the base types). Plugs are the node fields whose target is an `STUGraphPlug`. Config vars get one-line summaries. Links go output plug → input plug, from `m_graph.m_links`. Containers come from `m_graph.m_items`.
  - `BuildEntity`: `m_componentMap`, with graphs from `STUStatescriptComponent` and all references.
  - `Refs`.
- **Names** (`internal/names`):
  - STU names come from the registry (`stu.Name`), then the Statescript catalogue's CRC-recovered names (marked as guesses, with their confidence).
  - Resource names come from the TSVs, string assets and name fields.
  - [L] Every name hash (types, fields, enums) is CRC32 of the lower-cased name: all 2,192 OWLib names match. The code uses `names.Hash("m_nodes")` instead of hard-coded hashes where the name is known.
- **Package membership** needed an addition to `systems/content` (T034, [ContentAccess.md](ContentAccess.md) §6): `PackageResources`, `ResourcePackages`. [L] `ResourcePackages` is a linear scan, ~13 ms.
- **Frontend**:
  - State lives in one reactive object in `composables/useExplorer.ts`: the active document tab's resource, view tab,
    selected instance / tree path / graph node and history; the other document tabs (`docs`) keep theirs. This is the
    selection model an editor would build on.
  - Components: `Rail`, `ResourceList`, `ResourceView` (+ `DocTabs`) and one component per view tab (`InfoView`,
    `StuTree` + `TreeNode`, `GraphView`, `EntityView`, `MapView`, `DepGraph`, `RefsView` + `NeighborList`, `HexView`).
    `VirtualList` is a fixed-row-height virtual list with keyboard navigation. `Guid` draws a GUID coloured by its type
    and is the link (see Navigation); `GuidMenu` is its right-click menu.
- **Tests** (`make test`, install required except `refindex`): every one of the 370 graphs and 967 entity definitions
  goes through `Describe`, `Tree` (every instance) and `BuildGraph` / `BuildEntity`, with 0 errors and 0 unresolved
  links; the reference index is built on the whole install (0 failures, every kind used, known edges present, cache
  round trip, a second open loads the cache); one resource of each of the 96 types goes through the info view's
  queries; hex and name search, reveal (`IndexOf`), package listing; `refindex` unit tests (CSR, merge, cache).

## Reference index  (T046)

`internal/refindex` is a directed graph over every resource of the install: "A uses B". It is built by **sources**,
one per format, and cached on disk.

| source | reads | kinds (edge labels) | [L] pairs on 0.8 |
|---|---|---|---|
| `strings` | string assets 0x07C, 0x0A9 | none: names only (3,364) | |
| `stu` | every STUD resource (30,834) via `systems/stu` | `stu.field` | 177,123 |
| `maps` | the 19 map data (0x002) via `systems/maps` (2 at a time: up to 119 MB each) | `map.placement` (record's primary GUID: definition, model, effect, sound, string), `map.asset` (look / font / skin, model-group looks, GUIDs in record fields), `map.env` (environment + sound spaces), `map.instance` (references in entity instance data) | 8,133 / 15,163 / 437 / 1,160 |
| `model` (T064) | the 2,972 models, 11,150 materials and every other STU document (for model + look pairs) | `model.effect` (model → the 0x08E resources its loader requests), `material.texture` (aux = binding index), `material.shader`, `material.ref` (+0x08), `model.look` (model → look, from any STU object naming both, e.g. `STUModelComponent`) | 144 / 51,189 / 11,150 / 5,243 / 605 |
| `maps` (T064 addition) | as above | `map.look`: model → look placed together (record types 1, 2, 8, the sky); from = the model | 3,049 |
| `texture` (T063) | the 16,183 textures with payloads (32-byte headers; inline textures reference nothing) | `texture.payload` (texture → its payloads, aux = payload index), `texture.base` (delta texture → its base texture and the base's payloads) | 24,138 / 680 |
| `audio` (T065) | the 3,821 sounds (0x02C: their STUSound, bank and WEM ids) and the 87 switch / state groups, switches and parameters (0x02D–0x030) | `audio.source` (bank → the WEMs its Sound objects play: HIRC source id = the sound's WEM id), `audio.group` (bank → the 0x02D–0x030 resources whose Wwise id occurs in its HIRC bytes [?]); names for 0x02D–0x030 ("WEAPON_TAIL") | 49,659 / 5,895 (+ 75 names; 0.1 s) |
| `packages` | the 1,332 asset packages: APM owner (`Package.Asset`, ContentAccess.md §6), records, the blob header's dependencies and bundle | `package.member`, `package.dep`, `package.bundle`, `package.owner` (asset → its load package) | 1,397,709 / 3,385 / 1,160 / 1,324 |

- **Storage**: a pair (from, to) is stored once, with a bit mask of its kinds (≤ 32 kinds) and an `aux` value (maps:
  record index + 1, so "used by" a map can open the map at the placing record). Outgoing edges in CSR form (node
  offsets + targets, sorted); the incoming side is derived at load. [L] 1,597,442 pairs, 7.0 s to build (strings
  0.0 s, stu 1.5 s, maps 4.9 s, packages 0.1 s), 0 failures. With T063's and T064's sources: [L] 1,693,569 pairs, 8.8 s to
  build, 0 failures. With T065's: [L] 1,749,123 pairs, 9.6 s, 0 failures.
- **Cache**: `os.UserCacheDir()/owexplorer/refindex-<sha1(key)>.gob` (gob; written to a temp file and renamed). The key is
  the index format version + the install's `Describe()` (backend, version, build) + every source's id and version, so a
  source change or another build rebuilds it. [L] 25 ms to load.
- **Names** found by sources: string assets name themselves; a root field naming a string (`m_name`,
  `m_displayName`, …) names an STU resource, and a map header also names its `m_map`; a package is named after its
  owner ("Package of TEMPLE OF ANUBIS", "Package of Reinhardt"), a bundle after its package. The repository's names
  (`internal/names`) come first: now also `systems/data/maps/maps.json` (map data + header names with mode and kind as a
  note, gamemode brains) and `systems/data/heroes/heroes.json` (heroes and hero entity definitions).
- **Queries** (`catalog.Neighbors`): one side of a resource, filtered by kinds, types, package membership, a search and
  "only resources" (drops ids that are not resources: Statescript config-var ids and similar), with counts by kind and
  by type and paging. `App.RefDetail(from, to)` says why: the STU field paths (`view.RefPaths`) or the map record.

### Plugging in a format (T063 textures, T064 models / materials, T065 audio)

1. Implement `refindex.Source` in `internal/refindex` (or a package of the format):
   `ID()`, `Version()`, `Kinds()` (names `<source>.<what>`, a UI label), `Select(ctx, resource)`, `Workers()`,
   `Scan(ctx, resource, emitter)`. `Scan` reads through `ctx.Store`, calls `e.Ref(from, to, kind, aux)` for every
   reference and `e.Name(guid, text, source, guess)` for names; optional `Prepare(ctx)` runs once before the scans.
2. Append it to `catalog.Sources()`. Sources run in order; names of earlier sources are visible through `ctx.Name`.
3. Bump `Version()` whenever the output changes: that changes the cache key.
4. Give the kind a colour in `frontend/utils/format.ts` (`KIND_COLORS`; others get a hashed hue) and, if the type had a
   placeholder label, set it in `fixedTypes` (catalog.go) and its decoder task in `planned` (app_graph.go).
Everything else (References tab, dependency graph, info view, counts in the header) picks the kinds up by itself.

## Navigation  (T046)

- **Links**: every GUID (`<Guid>`) opens its resource; Ctrl/⌘-click or middle-click opens it in a background tab
  (Ctrl+Shift-click: foreground); right-click opens the GUID menu: open, new tab, dependency graph, references, copy
  (GUID, `0x…`, `ow://`), reveal in the type list or in its package. `link=false` GUIDs (rows that select something)
  still take Ctrl/middle/right-click. Statescript graph parameters list the config vars' references as links.
- **Document tabs** above the resource, each with its own back/forward history: Ctrl+W closes, Ctrl+Tab /
  Ctrl+Shift+Tab / Ctrl+1…9 switch, middle-click closes a tab. **History**: Alt+← / Alt+→, the mouse's back / forward
  buttons, Backspace. **Breadcrumbs**: the tab's last six steps, clickable.
- **Header**: name + note (e.g. "map data · assault game map"), copy, Reveal (selects the row in the type list), the
  packages with their owner names and a "list" link, reference counts (click: dependency graph).
- `\` (or « in the tab strip) hides the rail and the list: more room for the graph and the map.
- **New window**: Shift-click a GUID, the GUID menu or the header's "New window". Wails v2 has one window per process
  (its runtime has no call to create a window; multi-window is Wails v3), so `App.OpenWindow(link)` starts a second
  `owexplorer` process: `ow://<guid>[/<tab>[/<instance>]] --child --install=<this window's install>`. It opens in about a
  second (the reference index loads from the disk cache) and is fully independent (own memory: a map view holds up to
  ~120 MB). A child window never changes the remembered install. Pins, recents and panel sizes are shared through
  `state.json`: every change re-reads the file, applies itself and writes it back atomically, so windows don't
  overwrite each other's changes.
- **Split view**: "Split" in the header (or "Open in split view" in the GUID menu) opens the resource in a second pane on
  the right with its own document tabs and history. It keeps its resource while the main pane navigates. The rail, list and
  search always open into the main pane; keys and the top bar's back / forward act on the pane last clicked (outlined).
  "Close pane" (or closing its last tab) ends the split. State: `useExplorer.ts` keeps one `Pane` per pane (resource, view
  tab, instance, history, document tabs); `PaneHost` provides it and every component reads it through `useExplorer()`
  (`s.tab`, `s.resource`… are the pane's, `s.types`, `s.filter`… the app's).
- **Moving tabs** (T067):
  - **Drag** a document tab: within its strip to reorder, onto the other pane's tab strip (a marker shows where) or
    body to move it there.
  - It keeps its history, view tab and selection (instance / record / mip).
  - The split pane closes when its last tab leaves; the main pane can be emptied.
  - **Right-click** a tab: "Move to the split pane / main pane / a new split pane", "Move to a new window" (opens its
    current `ow://` link in a child window and closes the tab), "Close".
  - The drag uses pointer events, not HTML5 drag and drop. The tab's mousedown prevents text selection: WebKit would
    otherwise start a native drag of the selected text and swallow the `mouseup`.
  - `moveDoc` / `moveDocToWindow` are in `useExplorer.ts`.
  - [L] Tested with xdotool: reorder; onto the split's strip; onto the split body over the 3D map and over a graph
    canvas; the context-menu moves; emptying the split closes it.
- **Resizable panels**: every splitter (rail, list, split pane, the side panels of the graph / dependency / map views,
  the tree's instance list, the References columns, the map's placement list vs record detail) can be dragged;
  double-click resets it. Sizes are kept per panel id in `state.json` (`panels`, `App.SetPanel`), so they survive
  restarts (localStorage isn't used: the webview's storage isn't reliably persistent under Wails).

*(screenshot not included: split view)*

## Dependency graph  (T046)

Tab **Dependencies** on every resource (`components/DepGraph.vue`). The resource is in the middle, what it uses grows to
the right, what uses it to the left; edges always point left → right and are coloured by kind.
- Each node shows its name (or type label), GUID, note, and its in / out counts on the left / right handles: clicking a
  handle expands or collapses that side (depth on demand, never the closure). Click selects (side panel: what it is,
  why it is linked: kinds, STU field paths, "open the map at record N"), double-click opens, Ctrl-click opens a tab,
  right-click is the GUID menu.
- A side with more than 30 neighbours shows one **group node per type** ("5,144 × VoiceLine"): a group of ≤ 30 expands
  in place, a bigger one opens the **list** (the side panel's `NeighborList`: paged, virtualised, kind / type chips with
  counts, search, "why", "+ graph" to add one node).
- **Filters**: kinds (package membership is off by default, except when the resource is a package: every resource is in
  a package), types present in the graph, "non-resources" (off by default), "only in package". Changing a filter replays
  the expansions.

*(screenshot not included: dependency graph)*
*(screenshot not included: list mode)*

## Info view  (T046)

Tab **Info** on every resource: decoder status (STU: instances, layout CRCs, field errors; map data; string; asset
package: records, dependencies, bundle, APM owner, manifest; otherwise "unknown format: no decoder yet" and the task
that plans one), the type's label and where it comes from, count and size statistics (min / median / max), and what the
resource uses / is used by, by kind and by type. It is the first tab for resources without a decoder.

*(screenshot not included: info)*

## Adding a view

1. Backend: add a pure function in `internal/view` (input `*stu.Document` + `Resolver`, output a JSON-tagged struct).
2. Expose it as a method on `App` in `app.go` (use `a.withDoc(guid, …)`), then run `make build` (or `.bin/wails generate module -tags webkit2_41`) to regenerate `frontend/wailsjs`.
3. Frontend:
   - Add a component in `frontend/components/` that calls `api.<Method>(guid)` on mount.
   - Add its tab to `tabsFor()` in `useExplorer.ts` and to the switch in `ResourceView.vue`.
   - Use `<Guid :guid>` for GUIDs, so every reference stays a link.

## Graph view: layout  (T067)

**Selection** (T064 fix): a click on a node selects it; a click on the empty canvas (without dragging), `Esc` (in the
focused pane, not in a text box) or ✕ in the side panel clears it, and the Ask chip follows. [L] Checked on Xvfb with
xdotool: canvas click, `Esc` and ✕ each cleared node #57 of `0x1DB`, a canvas drag kept it.

The Graph tab's canvas uses a **layered (Sugiyama) layout** by default (`internal/view/layout.go`; `App.Graph` returns
it as `layout`). Links flow left → right, entries come first, and crossings are few.
- **Algorithm**:
  1. Break cycles: a DFS from the entries reverses the back edges.
  2. Layers by longest path; sources that aren't entries are pulled next to their first successor.
  3. A link spanning several layers gets a dummy node in each layer between.
  4. Order each layer with barycenter sweeps (12 rounds), keeping the order with the fewest crossings.
  5. y: each node moves towards the mean of its neighbours, overlaps are pushed apart.

  **Connected parts** get their own layout and are packed in rows, largest first, about 1.8 × as wide as tall. So a
  graph of many small entry chains comes out wide, not as one tall column.
- **Routes**: each link runs from its output port through its dummy nodes to its input port. It is drawn as curves
  that leave and enter the ports horizontally.
- **Toolbar**: "Layered | Editor" (Editor = the editor's `m_pos`, remembered as panel `graph.editorLayout`) and "Fit".
  Fit fits the layered graph into both dimensions (centred), and the editor layout to the width.
  - The editor's container frames (`STUGraphContainer`) are drawn only in the Editor layout; in the layered one they
    would be large overlapping boxes.
- **Dragging a node** moves it: view only, never game data.
  - Positions are saved per graph and layout in `~/.config/owexplorer/graph-layout/<guid>-<layered|editor>.json`
    (`App.GraphPositions` / `SetGraphPositions`).
  - A dragged node's links become direct curves. "Reset layout" deletes the file.
- `ow://<graph>/graph/<node index>` (and the Ask panel's node links, and MCP `open` with `tab: graph, select: N`)
  selects the node and centres it. For the Graph tab, the pane's instance is an m_nodes index.
- [D] Tests (catalog `TestViewsOnAllGraphsAndEntities`), on all 370 graphs (14,099 nodes): every node is placed, no two
  boxes of a column overlap, and every route joins its ports. 30 ms for all of them, 1,607 crossings, 221 back edges.
  `view` `TestLayout` covers a synthetic graph: layers, a cycle, and a long link with two bends.
- [L] Tested: the door graph `0x042` came out in four packed parts. The gamemode graph `0x028` (186 nodes) was readable
  when zoomed in. A dragged node was saved, reset and reloaded; the Editor toggle worked.

## Graph view: network side  (T067)

Every Statescript node shows which side runs it: a dot in its top-right corner, with a legend. The side is also the
details' "Network" row, the node list's dot, and MCP `statescript_graph` `net` / `netWhy` / `netConflict` (plus
`byNet` counts).
- **Sources** (`view.netSide`, in this order):
  1. `m_clientOnly` (node `+0x75`, the flag the runtime copies into the state and uses to pick the client-only lists,
     StatescriptRuntime.md §1.3) → **client**;
  2. `m_serverOnly` (node `+0x76`) → **server**;
  3. a node type **without a client class** (`systems/data/statescript/statescript_node_catalogue.json`, `runtime`
     empty: the client falls back to the base class and does nothing, StatescriptRuntime.md §1.5) → **server**;
  4. otherwise **both**. A type missing from the catalogue counts as having a client class, and `netWhy` says so.
- **Conflicts** (`netConflict`, red ring around the dot): both flags set, or the client-only flag on a type without a
  client class.
  - [D] Over all 370 graphs (14,099 nodes):
    - both 11,201;
    - client 1,000;
    - server by `m_serverOnly` 577, by the flag and no client class 133, by no client class alone 1,188;
    - 3 conflicts, all `STUStatescriptActionHack` with the client-only flag: `0x028` #180, `0x139` #132,
      `0x55a` #73.
  - [?] Either the Hack action is a client-predicted effect with no client class in 0.8, or the flag is stale data.
    Not investigated.
- **Filter**: "All | Client | Both | Server" in the toolbar. From All, a click shows only that side; further clicks
  add or remove sides. Nodes of the other sides are greyed out, and so are links touching them; nothing is hidden.
- [L] Tested: the door graph `0x042` is 28 × both, so the Server filter greys everything. `0x028` (47 client,
  107 both, 32 server): the Client filter leaves the client-only UXViewModel chain lit.

## Map view

Tab **Map** on map data (type 002) and map headers (09F, which open their `m_map`) (T045, docs/Maps.md §9).
- Backend: `internal/mapview` over `owmaps/maps` (systems/maps). `App.MapView(guid)` returns the view: placements
  with class, definition/model names, position, yaw; model-group instances as extra plot points; referenced assets;
  environment; sections; collision summary. `App.MapRecord(guid, i)` returns one record's decoded fields and its
  instance data (STU JSON). One parsed map is cached (map data is up to 119 MB).
- **Origin and cursor** (T046): an axes gizmo marks the world origin (0, 0, 0): +X right (red), +Z down (blue), +Y up
  out of the plot (green). When the origin is off screen, an arrow on the edge points to it. The status bar shows the world
  x / z under the cursor, and the full position of the placement under it (the plot has no height, so y comes only
  from placements).
- **Flip** (T046): "⇆ Flip H" / "⇅ Flip V" in the map toolbar, or the keys H / V (in the focused pane), mirror the view
  left ↔ right / top ↔ bottom around the plot's centre. Only the view is mirrored: the cursor coordinates, the placements
  under the cursor, picking, the facing lines and the origin gizmo's arrows (they point along +X / +Z as drawn) follow it.
  The flips are remembered with the panel sizes (`state.json` `panels`: `map.flipH`, `map.flipV` = 1).
- Classes: `networked` (definition has STUSpawnComponent / SpawnPointInfo: the server creates it), `mover`
  (movement state), `gameplay` (record flag bit 0), `prop`, `static` (model groups and models), and one class per
  other record type.
- Frontend `components/MapView.vue`: a canvas top-down plot (x → right, z → down) with layer toggles, pan/zoom,
  click to select; placement list (filter by name, GUID, class, type, placed index, override type); record detail;
  assets; header. `ow://<map>/map/<record index>` selects a record.
- Test: `internal/mapview` builds the view of all 19 maps; Anubis' classes match the catalogue (22 networked, 7 movers).

*(screenshot not included: map view)*

## 3D map mode  (T066)

"2D / 3D" in the map toolbar (remembered: panel `map.3d`). The 3D mode draws the map's **static collision**
([Maps.md](Maps.md) §2.6) with the placements as markers. It is step 1 of a 3D map viewer: real models and textures
come after T064.
- **Data**:
  - `systems/maps/go/maps` `DecodeGeometry` (pure Go over the map bytes): the spheres, capsules and hulls (faces as vertex
    loops from the half-edges) and the triangle mesh with its BVH. `Mesh.Raycast` walks the game's own BVH (node masks
    included).
  - `internal/mapview/geometry.go`: `Shapes` (JSON: shapes + mesh summary), `MeshBinary` and `Ray`.
  - Bindings `App.MapShapes`, `App.MapRay(guid, origin, dir, parts)`. The mesh goes through the asset server like the
    textures: `/mapgeo/<guid>.bin`, little-endian `"OWCM"`, version 1, V, T, then `f32[3V]` vertices, `u32[3T]` indices,
    and `u16[T]` masks, surface flags and materials. Anubis is 13 MB.
- **Mesh parts**, by triangle mask (`mapview.TriPart`; heroes are categories 0x08 / 0x10):
  - **solid**: blocks heroes and other categories;
  - **heroes only (clip)**: mask ⊆ 0x001A (invisible walls and roofs). Drawn translucent;
  - **not heroes**: no hero bit, e.g. 0x0440, the majority of every map.

  Each part is a layer. The cursor ray skips the translucent clip, and shapes with no hero bit are drawn translucent too.
- **Colours** (panel `map3d.color`):
  - *surface*: floor / wall / ceiling by the normal. Upward faces without surface flag bit 0 (the ground probe's
    [? walkable] bit) are tinted red. Clip is pink, not-heroes teal;
  - *height*: a ramp over the mesh bounds;
  - *mask* / *material*: one colour per value, with a legend of the most common ones.

  Shading: a hemisphere light and a fixed sun, `MeshLambertMaterial` with flat shading. Wireframe is optional.
- **Markers**: the same classes, colours, layer toggles, selection and placement list as the 2D plot, plus the
  model-group instances as small static dots (the backend `Point` now carries y).
  - Occluded markers show faint when "hidden markers" is on.
  - Networked entities and movers have a 1.5 m facing line.
  - "names" labels them within 160 m, and the selected placement always has a label.
  - A marker's label is its name, or its type and short GUID (`entity 167` = definition `0x0400…0167`).
  - Click selects, like the 2D plot: the nearest marker within 11 px. Markers behind the surface under the cursor and
    minor classes are weighted down. Selecting in the list moves the camera to the placement (orbit: target, ≤ 18 m).
  - `ow://<map>/map/<record>` opens the 3D view focused on that record.
- **Cameras** (`C` toggles; `R` resets to the 2D plot's framing: x → right, z → down, seen from 60° above). Every
  3D open and every reset starts in **fly** mode at that framing (T064; before, orbit):
  - *orbit*: drag rotates, right-drag / Shift-drag pans, the wheel zooms toward the point under the cursor (unless it
    lies far behind the target, e.g. the boundary walls), double-click centres there, and WASD moves the target over the
    ground (Q / E vertically);
  - *fly*: drag looks, WASD / Q E move, Shift ×4, the wheel sets the speed (shown in the status bar);
  - *FPS* (noclip; `N` toggles it, or the FPS button): a click in the view captures the mouse (Pointer Lock API) and the
    mouse looks around (yaw / pitch, no roll, pitch clamped at ±89°). WASD moves along the view direction, Shift ×4,
    the wheel sets the speed, and a click selects the marker under the crosshair. `Esc` releases the mouse, and only the
    mouse: it doesn't leave maximise. The status bar shows the **camera position** plus the point under the crosshair.
    - Fallback: "capture mouse" off (panel `map3d.nocapture`) turns FPS into drag-to-look.
    - Sensitivity (round 3): a slider in the FPS options, 0.1×–10× on a log scale, value shown, default 3×
      (1× = 0.0025 rad per pixel). It applies to the captured mouse and to the drag-to-look fallback; double-click
      resets it. Stored as panel `map3d.sens` = 1000 + 100·log10(×).
    - [L] The pointer lock works in webkit2gtk: it engages, `movementX/Y` arrive and `Esc` releases it. But on
      Xvfb / X11 the window stopped repainting after the first capture, even after the release, while the JS kept
      running. The user's Wayland session is untested.
  - In every mode, **Space / Ctrl move straight up / down** (world Y), alongside E / Q.
    - The 3D view's key handler runs in the capture phase. While Ctrl is held over the view, Ctrl+W/A/S/D/Q/E move
      the camera and are stopped there, so Ctrl+W doesn't close the tab.
    - Ctrl pressed with the mouse outside the view keeps the normal shortcuts. Ctrl+Tab, Ctrl+1…9 and Ctrl+K always
      work.
    - Space is `preventDefault`ed, so it doesn't click a focused button.
- **Cursor** (status bar): the world position under the cursor, ray-cast in Go on the mesh through its BVH (`MapRay`, one
  request in flight). It also shows floor / wall / ceiling, the triangle index, mask (blocks heroes or not), surface
  flags, material, and the placement under the cursor. A solid shape in front of the mesh is reported instead (`hull #12
  mask 0x199f`).
- **Origin**: axes at (0, 0, 0) (X red, Y green, Z blue, drawn over the geometry, with labels), plus an orientation gizmo in
  the corner.
- **Maximise, fullscreen and pop out** (CHANGES round 1):
  - "⤢ Maximise" or `F` makes the map (3D or 2D) fill the whole window above the status bar, so the cursor coordinates
    stay visible. A bar at the top has the map name, 2D / 3D, Mirror, Fullscreen, Pop out and Restore. `Esc` or `F`
    returns.
  - `F11` or "⛶ Fullscreen" maximises the view and makes the window fullscreen through Wails' `WindowFullscreen`.
    `Esc` leaves both.
  - State: `mapMax` in the app state, holding the id of the pane that is maximised.
  - "⧉ Pop out" (`App.OpenMap3DWindow(guid, record)`) starts a child window, like T046's new window, on
    `ow://<map>/map[/<record>]` with `--map3d`. The child's first map view opens in 3D, maximised, in wide mode,
    focused on the selected record. It doesn't save the 3D mode to the shared state (`App.StartupMap3D`).
- **Mirror**: the 2D flips mirror the screen. In 3D only a handedness flip means anything (both flips = a rotation), so
  the world's x is mirrored when exactly one of Flip H / Flip V is on. "⇆ Mirror" and the keys H / V toggle them. The
  view keeps looking at the same game point, and coordinates stay game coordinates.
- **Models (visual geometry, T064)**: three toggles at the top of the 3D panel choose the geometry (panel `map3d.geo`,
  remembered; default: models only):
  - **models (visual)**: the map's placed models, i.e. record types 1 (model groups: one look per group), 2 (models)
    and 8 (placed models). Each model + look is drawn once as a `THREE.InstancedMesh` (`App.MapVisual` → batches of
    instances: position, quaternion, scale) with the look's colour textures and opacity masks, at the model's finest
    LOD, fetching 4 meshes at a time;
  - **textures 256 / 512 / 1024 / full**: the largest mip up to that size. It starts at 256 on every open (not
    remembered); raising it swaps the textures of the loaded models and frees the other size's. GPU memory grows ~4×
    per step (Anubis: 187 distinct textures, ≈ 65 MB at 256, ≈ 260 MB at 512, ≈ 1 GB at 1024: estimates for RGBA8 +
    mips);
  - **shading simple | material** (T071; off, i.e. simple, on every open: not remembered):
    - *simple* is T064's colour texture with Lambert.
    - *material* runs every slot's game material through the Material tab's builder (`utils/owmat.ts`, mapping in
      "Material view"):
      - one shared `MeshStandardMaterial` per game material (Anubis: 231);
      - maps at min(texture size, 512) px, and only the ones the builder uses: colour, normal, emissive, opacity and one
        packed `orm` image (`/mprev/<material>/orm.png`: R = AO when it is on UV0, G = roughness, B = metalness).
    - It is built progressively and nearest first:
      - the mesh loader itself now takes the batch nearest the camera next;
      - previews come in bulk (`App.MaterialPreviews`, 64 per call);
      - meshes switch to their materials nearest first, with at most 8 ms of work per frame.
    - Lighting in material mode is the generated studio environment (`RoomEnvironment`, intensity 0.45) plus the sun
      (0.9); the hemisphere light is off. **It is studio lighting, not the game's probes, lightmaps or GI**
      (Rendering.md §8).
    - Inputs the game reads on UV1 (e.g. 0x21's `baseao`) are left out: the mesh carries UV0 only.
    - [L] Anubis on Xvfb, on the host GPU (NVIDIA through WebKit's EGL render node; Xvfb does not mean software GL
      here):
      - 354 batches, 2.44 M triangles, reset overview, maximised;
      - GPU-synced frame time, 3 runs each: **simple 3.8–4.6 ms, material 5.2–6.0 ms** (256 and 512 px alike); both are
        above the 160 fps cap of the benchmark;
      - all 231 materials were built within 5 s of switching.
    - *(screenshot not included: Anubis, material shading)*
  - **lighting studio | map** (T071 round 3; panel `map3d.light`, remembered, default map). *map* lights the map with
    its own environment block ([Maps.md](Maps.md) §2 environment, `App.MapEnvironment`, `app_env.go`,
    `utils/owenv.ts`):
    - **sun**: direction `+0x14C` and HDR colour `+0x158` (Anubis 3.29, 2.23, 1.61, which is where its orange light
      comes from). **Shadows** (off on every open, not remembered) use one 4096² PCF-soft shadow map over the playable area (the collision bounds). It is
      re-rendered only when models load, hide or change.
    - **sky** (`+0x180`, a 64 px HDR cube): the background, served as an HDR equirectangular panorama
      (`/envmap/<texture>.bin`: "OWEQ", w, h, RGBA float32).
    - **ambient**:
      - The ambient cube (`+0x0F8`) is the image-based lighting (material mode) or a hemisphere light (simple mode).
      - A warm **bounce** term from below is added: sun colour × 0.5 × the sun's elevation / π. This is an estimate
        [?] standing in for the game's baked GI (LIGHTINGDATA 0x00E, not decoded): in game, surfaces in shadow are warm,
        not sky-blue.
      - The sky cube as lighting was about 3× too bright (its sunlit sand ≈ 1.5) and washed the map out.
    - **tone mapping** ACES / neutral / AgX (`map3d.tone`), **exposure** (default 0.40, `map3d.exposure`), then the map's
      **colour-grading LUT** (`+0x108`, 32³, three's `LUTPass`, toggle "grading"), through an `EffectComposer`.
    - Still missing next to the game: baked GI and lightmaps, reflection probes, SSAO, fog, the game's own tone curve
      and auto-exposure. The sunlit sand comes out redder than in game; exposure and tone mapping are the knobs.
    - *studio* is round 2's neutral light (hemisphere or studio environment + white sun, no tone mapping).
    - [L] The near-white stone textures (e.g. `0C…05DD`, mean (0.91, 0.90, 0.85)) are correct. In game their colour
      comes from this lighting and grading, so "grey buildings" meant missing lighting, not missing textures. The only
      Anubis slots without a colour texture are 44 decals.
    - *(screenshot not included: Anubis, map lighting (ACES))*
    - *(screenshot not included: tone mapping: ACES, neutral, AgX)*
  - **Reflections** (T071 round 5; checkbox "reflections", off on every open, not remembered; material shading with map
    lighting):
    - Source: the map's **reflection probes** (record type 4, layout in [Maps.md](Maps.md): 98 on Hanamura, 65 on
      Anubis) plus the map-wide capture (environment `+0x130`). `App.MapProbes` and `/probes/<map>.bin` serve their HDR
      cubes as panorama atlases at 3 blur levels (cube mips 2 / 4 / 6), in half floats (Hanamura 6.9 MB, 0.9 s).
    - **Brightness** (round 6) [? inferred]:
      - The captures are stored about 50× darker than the ambient cube: mean 0.004–0.03 vs 0.62 on Gibraltar.
      - One factor per map, 2^(median of +0xA4), mostly 2^6 = 64, brings a typical probe to the ambient level.
        Gibraltar's median then comes out at 0.70.
      - Per-probe 2^(+0xA4) would spread them 0.005…60, so +0xA4 is not a plain per-probe exposure.
      - The map-wide capture is scaled to the ambient cube's mean.
      - The fifth float (+0xA8) tracks each capture's brightness.
    - The shader (`utils/owprobes.ts`) replaces three's image-based specular **and ambient** (round 6: the local
      probe's blurriest level towards the normal, so interiors get their own light instead of the sky's):
      - per pixel, the two smallest probe boxes containing the point (a bounding-sphere check first), the inner one faded
        out over the last 1.5 m to its edge (round 6, hides the seams; the game's own blend is not traced);
      - the reflection ray intersected with that box (box projection), and the capture sampled towards the hit;
      - roughness blends the blur levels;
      - outside every box, the map-wide capture.
      - The ambient diffuse stays the ambient cube.
    - **mirror** (not remembered) turns every surface into a perfect mirror, to see what the probes reflect.
    - [D] Orientation: +Y up as for the sky cube. An indoor probe on Anubis (record 485) shows its ceiling lamp on +Y and
      its floor on −Y.
    - [?] The game's blend between neighbouring probes, the fade values (+0x98…) and the local transform (+0x40) are
      not traced; the viewer switches hard at box edges.
    - [L] Cost on the Anubis overview: 10.6 ms per frame with reflections vs 7.2 ms without (GPU-synced benchmark).
      With the blend and the probe ambient (round 6), a Gibraltar hangar view takes about 18 ms.
    - Still different from the game: local lights (Gibraltar has 371 light records, none drawn), the game's screen-space
      reflections (SSLR, Rendering.md §8.1: sharp reflections of on-screen geometry in puddles), fog, baked GI.
    - *(screenshot not included: Gibraltar hangar, probes as reflections and ambient)*
    - *(screenshot not included: mirror test: probe reflections)*
  - **Layered materials** (T071 round 4; the Model tab and the 3D map, simple and material mode alike):
    - The mesh now carries vertex colours and UV1: `/model/<guid>.bin` is **OWMD v2**, with RGBA8 colours and UV set 1
      after the indices.
    - `utils/owmat.ts` `patchLayers` patches the shader after three's `map_fragment`:
      - **0x25**: albedo = lerp(basecolor, layer B × D44A3918, t), with t = sat((height − (1 − vertex.a)) /
        clamp(vertex.g, 0.01, 1) + vertex.a); layer B and the height map on uv0 × C251F501 + 811D50FA [D bytecode];
      - **0x24**: basecolor × overlay × 2, with the overlay on UV1 when 859FBB9D = 2 [D].
    - Inputs the game reads on UV1 (0x21 AO, hero emissive) are now on in model and map views (three's UV channel 1).
    - [L] Hanamura building `0D…0CCC`: the walls are layer B (plaster) because vertex alpha averages 0.94. Before, the
      explorer showed layer A everywhere.
      *(screenshot not included: Hanamura building, layers blended)*
    - **Open**: the red / brown painted timber of Hanamura (material `0E…05F6`). Its textures are pale / brown wood, its
      G-buffer shader has no tint, and the vertex colour RGB is unread. Material `+0x40` holds a matching colour
      ((0.37, 0.17, 0.10)), but `Material_GetRenderObject` doesn't read it [G]. Where the game's red comes from is not
      found.
  - **measure frame time** (T071):
    - It renders frames back to back for 3 s, each ending in a 1-pixel `readPixels` that waits for the GPU (`gl.finish()`
      doesn't block in WebKit's WebGL).
    - It shows the time per frame and the rate it allows, capped at 160 fps ("+" = at the cap).
    - The result is also in MCP `current_view` (`view.frameTime`) and the log.
  - **collision**: the collision mesh and shapes (all of T066's layers; the cursor ray only runs while it is on);
  - **helpers**: the submeshes whose material is a helper / editor material ([AssetFormats.md](AssetFormats.md) §3.5:
    the "Player Only / No Walk / No Grapple" boxes and the checker placeholder). Off by default; each batch is split
    into a normal and a helper `InstancedMesh`.
  - [L] Every map resolves, e.g. Anubis 354 model × look batches, 2,623 placed (868 with helper parts), 2.59 M
    triangles; Dorado 469 / 6,787 / 3.99 M; Gibraltar 493 / 5,886 / 4.65 M; 0 failed (`TestMapVisual`).
  - **Picking**: a click selects the model instance under the cursor, or under the crosshair in FPS with a captured
    mouse. A marker near the cursor wins when it lies in front of the model hit.
    - The hit is three.js' raycast of the instanced meshes; groups drawn with the invisible material (the other
      layer's parts) are skipped.
    - The selection is the map record (the list and the 2D plot follow) plus the instance (`selInst`). The record's
      instances are tinted and the picked one gets a box.
    - The selection card adds, for model records: "instance k of n at (x, y, z)", the look (link, Model tab, triangle
      count) and the look's material slots with thumbnails and links.
    - MCP `current_view` reports `view.modelInstance` (instance, model, look, position) and the placement's `model`
      section (model, looks with instance counts, slots → material → colour texture, helper). The Ask chip's subject
      carries the instance.
    - [L] Checked with xdotool in orbit and FPS (drag-to-look) modes: Anubis record 218 instance 4, record 19 instance 22
      of 31, and helper record 97 when helpers are shown.
  - [L] With the helpers shown, Anubis' blockout text reads mirrored in the default view and correctly with Mirror on,
    and the faces are front faces in both (checked by drawing with front-face culling). No placement has a negative
    scale. The D.Va mech's decals read correctly in the Model tab without mirroring. Which view matches the game's
    handedness is for the user to confirm in game [?].
  - Not drawn: entities' models (networked / gameplay entities with a model component), map text (type 7), effects.
- **Hiding models** (T064): the selection card of a model placement has "hide" / "hide instance" (`Del`) and "hide
  model everywhere" (`Shift+Del`). Without a picked instance, "hide" hides the whole record (every instance of a
  model group).
  - Hidden instances get a zero matrix: they are not drawn, not pickable and not highlighted.
  - They are listed under "Hidden (n)" in the 3D panel, with "show" per row and "unhide all".
  - The list lasts for the session of that map view (not remembered).
  - MCP `current_view` reports it as `view.hidden`.
  - [L] Anubis: `Del` on record 218 instance 4, and the same click then picked record 92 behind it; "hide model
    everywhere" on `0D…02BC`; "unhide all" emptied the list.
- **Camera position** (T064 round 1): an overlay above the corner gizmo shows the camera's position in game coordinates,
  its yaw / pitch and, in fly / FPS, the speed, in every camera mode and in the maximised, fullscreen and popped-out
  views. Click copies "x, y, z".
- **Performance** [L, Xvfb + Mesa llvmpipe, i.e. software GL; the user's GPU is untested]: every one of the 19 maps
  loads. Fetch, decode and GPU buffers take 83 ms (0x223, 12,512 triangles), 308 ms (Anubis, 522,648) and 766 ms
  (Dorado, 1,334,323). The mesh is drawn non-indexed (per-triangle colours), so Dorado's buffers are ~60 MB.
  Frame times measured with `gl.finish()` were 2 ms, which is not believable under llvmpipe: WebKit renders WebGL out of
  process.
- **Tests**:
  - `systems/maps`:
    - `install_collision_meshes`: every map's BVH, triangles, masks and depth;
    - `install_collision_hulls`: the topology of all 1,978 hulls;
    - Go `TestGeometry`: `DecodeGeometry` against the C++ summary on 19 maps; BVH ray = brute force.
  - `internal/mapview` `TestGeometry`: binary sizes on 19 maps, plus Anubis' spawn points standing on hero-blocking
    floors.
- Fixed on the way: the status-bar cursor text was stored in `cursor`. That name is also a pane key (the history index),
  so hovering the map overwrote the pane's back / forward position. It is now `mapCursor`.

*(screenshot not included: 3D map mode: Anubis)*
*(screenshot not included: 3D close-up: the ship door (placed 0x1d7) selected)*
*(screenshot not included: 3D, mask colours: Hanamura)*
*(screenshot not included: 3D, models layer: Anubis)*
*(screenshot not included: 3D, helpers shown and a model instance picked (card))*

## Texture view  (T063)

Tab **Texture** on textures (004) and texture payloads (04D, which open their texture with the payload's mips
highlighted). Format: [AssetFormats.md](AssetFormats.md) §1.
- **Backend** `internal/texture`:
  - header, payloads, split-block planes and delta skins → mips × surfaces of bytes;
  - decoders for the 15 formats of 0.8 (BC1/3/4/5, BC6H_UF16, 8-bit, half and float) → RGBA float32 → 8-bit;
  - PNG encoding.
  - Display: HDR formats are scaled by the exposure in stops, with "auto" mapping the 99.5th percentile to 1, then clamped
    and sRGB-encoded. One-channel formats are drawn grey. Two-channel UNORM (BC5, R8G8: normal maps) get
    "Z from XY" (blue = sqrt(1 − x² − y²)) by default.
- **Images don't go through the bindings**. The asset server's handler (`imageHandler` in `app_texture.go`) serves:
  - `/tex/<guid>/<mip>/<surface>.png?exp=&auto=&z=`, one surface of one mip;
  - `/thumb/<guid>.png`, 64 px, from payload 0 alone (`LoadSmall`).

  The webview loads them as `<img>`. Loaded textures are kept in a 256 MB LRU and thumbnails in a 48 MB one. At most 6
  thumbnails decode at a time.
  - [L] Loading a 2048² delta texture (4 payload reads + the patch) takes 55 ms; decoding mip 0 and encoding the PNG
    takes 200 ms; a thumbnail takes 15 ms.
- **Viewer** (`components/TextureView.vue`):
  - canvas with zoom (wheel), pan (drag), a pixel grid from 12×, and a checkerboard under alpha;
  - mip and surface pickers (cube faces +X…−Z; "cross" unfolds the six faces of a cube);
  - channels RGBA / RGB / R / G / B / A: the canvas only masks the decoded image;
  - exposure slider and auto (HDR); Z from XY (two-channel);
  - pixel inspector (x, y, 8-bit RGBA);
  - "Export PNG" (the current mip / surface as displayed) and "Export all" (every mip × surface into a directory).
  - Side panel: header fields and flags, the raw 32 header bytes, base texture (deltas), variants (0x000F), mips with
    their payload, payloads with their mip range.
  - Keys: `[` / `]` mip, `F` fit, `1` actual size. `ow://<texture>/texture/<mip>` opens a mip.
- **Lists**: texture and payload rows show a thumbnail (material rows too: a rendered sphere, T071). In the 004 / 04D type lists, "▦ large" switches to 72 px rows
  with 64 px thumbnails (remembered: panel `list.largeThumbs`).
- **Index**: the `texture` source (table above) adds texture → payload and delta → base edges, so the dependency graph
  shows a skin's base and every payload's users.
- **Tests** (`make test`): every texture through load, decode, thumbnail and the mip-consistency check
  ([AssetFormats.md](AssetFormats.md) §1.8); known texture edges in the catalog test. `go run ./cmd/texscan` prints load
  statistics and dumps PNGs or DDS reference files.

*(screenshot not included: texture)*
*(screenshot not included: cube as a cross)*
*(screenshot not included: thumbnails)*

## Model view  (T064)

Tab **Model** on models (00C) and on model looks (01A, which open the first model placed with them). Formats:
[AssetFormats.md](AssetFormats.md) §2–§4.
- **Backend** `internal/model`: `Parse` (header, vertex / index buffers, submeshes, material keys, skeleton, loader
  references), `Mesh(lod)` (the submeshes of one LOD flattened: positions, normals, UV 0, u32 indices, groups),
  `ParseMaterial`, `ParseLook` (STU), `Resolve` (material keys → materials through a look), `GLB`, `OBJ`, `Render`.
  `App.Model(guid, look)`, `App.Material(guid)`, `App.ExportModel`, `App.MapVisual` are in `app_model.go`.
- **The mesh doesn't go through the bindings**: `/model/<guid>.bin?lod=<mask>` (little-endian `"OWMD"`, version 1, V, I,
  G, G × {first index, count, material slot, submesh}, f32 positions, normals, UVs, u32 indices) and the colour
  textures as `/mtex/<guid>.png?max=<px>` (the largest available mip ≤ max; `utils/owmd.ts` turns both into three.js).
- **Viewer** (`components/ModelView.vue`, three.js + OrbitControls): LOD buttons (LOD0 = finest; "all" when the model
  has no LODs), shading *texture* (each slot's colour texture: `basecolor` / `color`, else its first sRGB texture; its
  `opacity` mask as alpha; normal-only decals, with opacity but no colour, are not drawn; a slot the look has no entry
  for is dull red; AssetFormats.md §3.4); the proxy submeshes (flag 0x10, AssetFormats.md §2.5) are left out unless "proxy" is on, *material* (a colour per slot), *normal*, *plain*; wireframe; skeleton (bind-pose bones as lines);
  mirror X; Fit. Click selects the submesh under the cursor (again: deselect; ✕ in its card).
  - The per-slot colour mode's button is now labelled "slots".
  - **simple | material (PBR)** (T071; simple on every open, not remembered), under *texture* shading:
    - *simple* is the colour texture with Lambert.
    - *material* gives each slot's game material a `MeshPhysicalMaterial` from the Material tab's builder
      (`utils/owmat.ts`: normal, roughness, metalness, specular, AO on UV0, emissive, alpha test, decals; 1024 px maps).
    - It is lit by the studio environment plus the sun, with ACES tone mapping; the hemisphere and fill lights are off.
      Normal-only decals stay hidden.
    - *(screenshot not included: model: simple vs material)*
  `ow://<model>/model/<submesh>` opens with a submesh selected.
- **Which look**: the look picker lists the looks placed with the model (index kinds `model.look`: "STU", `map.look`:
  "map"), best first by the number of the model's material keys they resolve (e.g. `09800000000014b0 · STU · 11/14`).
  With none, it falls back to the looks sharing the most keys ("keys", at most 12): keys are shared widely, so these
  are guesses.
- **Side panel**: size (and the CPU part), LODs with triangle counts, the look, material slots (key, submeshes, material,
  colour texture with thumbnail, tags "helper", "alpha", "decal"), the submesh table marks proxies with "p", the submesh table (triangles, VB, IB, slot, LOD; click selects), vertex buffers with
  their elements (`semantic:format@stream+offset`), index buffers, the skeleton (bone, parent, id, bind position) and
  the GUIDs the loader requests.
- **Export**: "Export glTF" (binary `.glb`: one mesh, a primitive per submesh with positions, normals, UV 0, indices, a
  material per slot with the colour texture embedded as PNG, the game's Y-up axes) and "Export OBJ" (`.obj` + `.mtl` +
  the PNGs next to it; V flipped). Both export the LOD shown with the look shown. `go run ./cmd/modelscan -export
  MODEL:LOOK[:LOD] -out x.glb` does the same from the command line.
  [L] The D.Va mech (`0D000000000011CE` + look `09800000000014B0`) exported as GLB imports in Blender 5.2 as one mesh
  with 47,160 polygons, 12 materials and 6 images, and renders textured; the OBJ imports with the same 47,160 faces.
- **Info** tab: decoder status for models and materials (submeshes, LODs, triangles, keys, bones / shader, textures,
  constant buffers). 0x007 is labelled "Skeleton (model format)": the SKELETON handler shares the model construct.

*(screenshot not included: model view: the D.Va mech, submesh 14 selected)*

## Material view  (T064, preview T071)

Tab **Material** on materials (008): a live **preview** on the left, and the material's data in the side panel on the
right. Formats: [AssetFormats.md](AssetFormats.md) §3. The channel evidence comes from the shader bytecode (T071).

*(screenshot not included: material preview: a D.Va mech material)*

- **Side panel**:
  - shader (0x088) and its **family** (purpose, mode, confidence);
  - the +0x08 GUID, the +0x40 floats, the +0x68 flags and notes;
  - the **preview inputs**: input, slot, channel transform, UV set, intensity, confidence;
  - the bindings no input reads;
  - all texture bindings (thumbnail, slot name, format, size, flags);
  - the **parameter stream** (+0x10) with the known uses;
  - the constant buffers.
- **Viewer** (`components/MaterialPreview.vue`, three.js `MeshPhysicalMaterial`):
  - sphere / plane / cube, orbit controls, and a camera that fits the pane;
  - a studio environment generated in code (`RoomEnvironment` through PMREM; no downloads);
  - a directional key light with azimuth / elevation sliders and an environment intensity slider;
  - a toggle per input: colour, opacity, normal, roughness, metal, specular, AO, emissive;
  - a **channel view**: "lit", or one input's image as stored;
  - **flip green** (the normal map's convention is unknown [?]);
  - UV tiling, and the material's own UV scale / offset (on by default where it has them);
  - background dark / studio / light / checker, and a texture size of 256…2048;
  - the options panel folds away (remembered: panel `material.options`).
  - The geometries have V flipped: image row 0 is at the top, in D3D texture coordinates like the Model tab.
- **Inputs are resolved in Go** (`internal/model/preview.go`: `Material.Preview`) and served one image per input as
  `/mprev/<material>/<input>.png?max=<px>` (`app_material.go` `previewImage`):
  - colour and emissive in sRGB;
  - normal as an encoded unit vector (Z already decoded);
  - the scalar inputs grey: roughness, metalness, specular (= F0 / 0.08, used as `specularColorMap` × 2), AO.
  - A three.js material reads them directly (roughness in G, metalness in B, AO in R).
- **Thumbnails**: material rows in lists show a 64 px sphere (`/thumb/<material>.png`, the software renderer below, from
  the textures' small mips, cached with the texture thumbnails). The 008 list has "▦ large".
  *(screenshot not included: material thumbnails)*
- **The empty-tab bug**: 3,039 of 11,150 materials came back with a `null` list (2,983 without constant buffers, 29
  without textures, 27 without both). The template read `null.length`, so the tab stayed empty. The lists are now always
  arrays, a load error shows as a message, and `TestMaterialViews` checks all 11,150 materials [L].

### Slot → input mapping

The rules come from the shader bytecode (T071): the G-buffer fill pixel shaders of each family, their t-register →
slot records (0x086), and the lighting shaders that read the G-buffer back. Tags: **[D]** read in the bytecode or the
data; **[?]** inferred.

**Channel transforms** (the same in every family):

| transform | what the game does | confidence |
|---|---|---|
| colour `rgb` | sRGB albedo | [D] |
| `normal` | inverse stereographic decode: s = 1/(1+31.875·B) flattens; x, y = 2((RG − 0.5)·s + 0.5) − 1; n = (2x, 2y, 2 − d)/d with d = 1+x²+y². **Not** z = √(1−x²−y²), which gives about half the slope | [D] |
| `gloss` (packed G, B) | gloss = G, or sat((G+B)/2) when B > 0; roughness = 0.015 + 0.985·(1 − gloss) | [D] |
| `metal` (packed R = s) | metal = sat(2s − 1)² | [D] |
| `spec` (packed R = s) | dielectric F0 = 0.02 + 0.06·sat(s − 0.25), specular × sat(4s) (s = 0: none) | [D] |
| `emissive` | mask.r^2.2 × albedo × the intensity parameter (the game adds it in the composite) | [D] |
| green direction | no shader flips it. D3D or GL depends on the mesh tangents, not checked; default = D3D (green down the image) | [?] |

**Inputs**:

| input | slots (families) | channel | confidence |
|---|---|---|---|
| colour | `basecolor` 0xAD10F602 (0x21, 0x2C, 0x2B, 0x24 × `baseoverlay` × 2, 0x25 layer A, 0x27, 0x2F, 0x34); `color` 0x665648E9 (decals 0x22); `diffusecolor` 0x49E5C1E3 (0x33); 0xE870032E (0x32); 0xB85EA8CF (0x35); `emissivecolor` 0xEDCD5483 (0x38 glow); 0x37: lerp(lerp(`layer2color`, `layer1color`, l1.a), basecolor, base.a) | rgb | [D] |
| colour (fallbacks) | `diffusemap`, `colorsource`, `cloudcolor`, `foamcolor`; texture_0/1 (0x2A); else the first sRGB texture; else the first LDR texture | rgb | [?] |
| opacity | `opacity` 0x43FD6D66 .r (0x22, 0x35); `basemask` .r (0x27, alpha test 0.5); `diffusecolor` .a (0x33, coverage); the first one-channel `texture_N` .r (0x28 effects) | r / a | [D] |
| opacity (fallback) | the colour texture's alpha on effect / unlit / alpha-test families without an opacity slot | a | [?] |
| normal | `basenormal` 0x169615AA, `normal` 0x218568E4, `normalmap` 0x0DB044E6, 0xEF1DC737 (0x32) | normal | [D] |
| roughness / metalness / specular | packed: 0x20AF06CE (0x21 0x24 0x25 0x27 0x2B 0x2C 0x2F), `baseroughnessspecular` (0x34), `roughnessspecular` (0x37), 0x2D82D9D6 (0x35), 0xB96FB351 (0x22), 0x7614968D (0x32), 0x49E57F4E (0x26) | gloss / metal / spec | [D] |
| roughness | `roughness` 0x6B8F3240 (0x65 water) | r | [?] |
| AO | `basecolor` .a (0x2C, 0x2B; UV set = int 0x8CA81224); `baseao` 0xC6D17199 .r (0x21, 0x2F: **UV1**); `ao` 0xE03234D0 .r (0x27, 0x37, 0x22, 0x33; 0x35 on UV1); `overlayao` (0x25, UV set = int 0xE98CD32E) | a / r | [D] |
| emissive | `baseemissive` 0xBCBE747D .r ^2.2 × 0x91D6C98F (0x21, 0x2F) or × 0xC1BE91CC (0x2C, UV set = int 0xC7526E90, mostly UV1); `emissive` .r × 0xC1BE91CC without the pow (0x37); `emissivecolor` .r (decals) | emissive | [D] |

- **UV sets**: the primitives have one UV set. An input the game reads on UV1 (0x21's `baseao`, most hero emissive
  masks) starts **off**, with a note; its toggle turns it on anyway.
- **Parameters** (the +0x10 stream, [AssetFormats.md](AssetFormats.md) §3.3; the hashes are the b4 variable hashes, not
  CRCs of lower-case names), [D]:
  - 0x704029EA / 0x811D50FA: UV scale / offset (0x21 family); 0x3BC81C35: UV scroll × time;
  - 0x91D6C98F / 0xC1BE91CC: emissive intensity (0 on 1,415 of 1,870 0x21 / 0x2F materials: no emission in game);
  - 0x8CA81224 / 0xC7526E90 / 0xE98CD32E: UV set ints;
  - 0x656F1D9A / 0x9432648A / 0x859FBB9D: the 0x24 overlay's UV;
  - 0x21B43C8E: decal colour factor (2 = a multiply decal);
  - 0x72955E70: the character flag.
- **Not used** (listed under "not used by the preview"):
  - 0x2CE679FF, a gradient LUT (RGBA16F) of the ramp permutations of 0x2C / 0x26 / 0x32 / 0x36;
  - 0x12BBC7D3, the effects' over-lifetime curve (sampled in the VS);
  - `flowmap` (hair strand direction);
  - layer B of 0x25 (height-blended in by vertex colour);
  - the subsurface colour and thickness of 0x2B;
  - `baseoverlay`'s detail normal on 0x24;
  - the probe cube maps of 0x2F.

### Shader families (defaults)

| family | purpose | preview mode, shape | confidence |
|---|---|---|---|
| 0x21 | standard opaque PBR (world, props) | lit, sphere | [D] |
| 0x2C | hero / character PBR (basecolor.a = AO) | lit, sphere | [D] |
| 0x2B | skin (subsurface colour, thickness) | lit, sphere | [D] |
| 0x27 | masked PBR (`basemask` alpha test) | alpha test, sphere | [D] |
| 0x2F | forward-lit world (probe cube maps) | lit, sphere | [D] |
| 0x24 | world: basecolor × overlay × 2 + detail normal | lit, plane | [D] |
| 0x25 | world: two layers height-blended by vertex colour (layer A shown) | lit, plane | [D] |
| 0x37 | layered world colours (parallax ignored) | lit, sphere | [D] |
| 0x34 | cloth-type (roughness 0.9–1) | lit, sphere | [?] |
| 0x33 | hair (coverage in diffusecolor.a, flowmap, two lobes) | alpha test, sphere | [D] |
| 0x35 | alpha-tested foliage | alpha test, plane | [D] |
| 0x22 | decal: drawn over a grey base plane; normal-only decals show their normals | decal, plane | [D] |
| 0x28 | effect / particle: the BC4 mask additive, colour white (the colour constant is in a constant-buffer record whose layout needs the shader instance: not resolved) | effect, plane | [D] |
| 0x38 | facing-ratio glow (emissivecolor × pow(N·V)) | effect, sphere | [D] |
| 0x2A, 0x29, 0x30 | overlay / effect variants | effect, plane | [?] |
| 0x26, 0x32 | layered / entity materials (gradient LUT ramps) | lit, sphere | [?] |
| 0x36 | glass / water (transmission) | glass, sphere | [?] |
| 0x2E | sky / clouds | unlit, sphere | [?] |
| 0x23 | helper (colorsource) | unlit, cube | [D] |
| 0x65, 0x2D | water, special | lit / unlit | [?] |

*(screenshot not included: families: decal, foliage, effect, hair)*

**Coverage** [L, `TestMaterialViews`]:
- colour on 8,409 of 11,150 materials, normal on 6,906, roughness / metalness / specular on 6,461–6,463, AO on 5,476,
  opacity on 4,141, emissive on 3,326;
- 56 materials have no input: they have no textures (0x29 × 53, 0x2E × 3) and show a plain surface.

**Software renderer** (`internal/model/matrender.go` `RenderMaterial`): the same inputs on an analytically ray-cast
sphere, plane or cube, shaded with GGX + a hemisphere ambient + ACES. Used for the thumbnails and `material_image`. It
approximates the three.js view closely enough to recognise a material; it is not the game's shader.

Refs: who uses a material (looks) and what it uses are in References / Dependencies through the `material.*` kinds.

## Audio view  (T065)

Tab **Audio** on WEMs (03F, voice 0B2), sounds (02C), sound banks (043) and switch groups / switches / game parameters /
state groups (02D–030). Formats: [AssetFormats.md](AssetFormats.md) §5.
- **Backend** `internal/audio` (Go, no third-party code, no external tools):
  - `ParseWEM`;
  - `LoadCodebooks`: the Wwise codebook library, read from the install's `GameClientApp.exe`;
  - `Convert`: WEM → Vorbis I packets;
  - `Vorbis.Ogg` and `Vorbis.Decode`: a Vorbis I decoder (floor 1, residues 0–2, FFT IMDCT);
  - `ParseBank` (BKHD / HIRC; Sound source, parent, Event, Action);
  - `ReadSound` / `ReadNamed` (the STU side);
  - `WwiseID` (FNV-1);
  - the index `Source`.

  `app_audio.go`: `App.Audio`, `App.AudioPeaks`, `ExportAudio`, `ExportSoundAll`.
- **Sound doesn't go through the bindings**: `/audio/<guid>.pcm` ("OWPC", channels, rate, frames, s16 planar) is what
  the player plays; `/audio/<guid>.wav` and `.ogg` serve the files. Decoded WEMs are kept in a 384 MB LRU.
  [L] A 13.7 s stereo WEM converts and decodes in ~10 ms.
- **Player** (`components/AudioPlayer.vue`):
  - the waveform (min / max per pixel column, computed in Go) with the playhead; click to seek;
  - play / pause (Space), loop (L), volume, peak and RMS level, Export WAV (decoded, 16-bit PCM) / Export OGG (rebuilt,
    not re-encoded).
  - Playback is **WebAudio** from an `AudioBuffer` filled with the PCM. WebKitGTK's `<audio>` fails to load from the
    asset server's scheme (GStreamer `FormatError`), and this way the webview decodes nothing.
  - One player sounds at a time.
- **By type**:
  - *WEM*: the player; the header (codec, channels + mask, rate, length, bitrate, block sizes, packets, mod signal,
    uid, the Wwise id a sound gives it, loop points) and the RIFF chunks; the sounds that list it, the banks whose
    Sound objects play it, other users (a voice WEM's voice-line STU).
  - *Sound*: its WEMs (▶ plays one; Export all WAV / OGG into a directory), bank, bank id, play / stop event ids, and the
    **event trees** from its bank: Event → Actions (Play / Stop / Set state …, Wwise's action numbering [?]) → target →
    children by parent id (containers, actor-mixers) → Sound objects with their plugin (Vorbis / silence / external
    source), source id and WEM (▶).
  - *Bank*: header, chunks, the sounds using it, object counts, every event tree, the full HIRC object table (filter by
    id / type / detail) and the switch / state / parameter ids found in it.
  - *Switch group, switch, parameter, state group*: name, Wwise id with the FNV-1 check, a switch's group, a group's
    switches, other resources with the same id, and the banks holding the id.
- **Info** tab: decoder status for WEMs and banks. Type labels: 03F "WEM (Wwise Vorbis)", 043 "Sound bank (Wwise)",
  0B2 "Voice WEM (Wwise Vorbis)"; 02C–030 keep their STU root names. With the STU string fix (docs/STU.md §1),
  switch groups, switches and parameters are named in every list ("WEAPON_TAIL").
- **MCP**:
  - `describe` summarises sounds (WEMs, events, trees), banks (counts, event trees, sounds, group ids), WEMs (the
    Audio tab's data) and the 02D–030 types.
  - **`audio_export`** writes a WEM, a sound's WEMs or a bank's WEMs as WAV / Ogg to
    `$XDG_RUNTIME_DIR/owexplorer/<wem>.<format>` and returns the paths with length, channels, rate, and peak / RMS in
    dB.
  - The player's state (`kind`, `wem`, `playing`, `position`, in 0.5 s steps) is in `current_view`.
- **Tests** (`make test`):
  - `internal/audio` `TestAllWEMs`: all 18,607 WEMs convert and decode to their exact sample count, 30 s.
  - `TestBanksAndSource`: every bank parses, every parent and event action resolves, the index source matches all
    52,030 Vorbis Sound objects to WEMs.
  - `TestIMDCT` (against the O(N²) definition), `TestWwiseID`.
  - `app_audio_test.go` (WAV / PCM encoding).
  - The comparison with ffmpeg is a tool, not a test (it needs ffmpeg): `go run ./cmd/audioscan -all -ffmpeg DIR -every
    1`. All 18,607 match, at most 5.4e-7 apart.
  - `cmd/audioscan` also prints WEM / bank / group statistics.
- Testing on Xvfb: the webview's GStreamer would play to the desktop's sound server. For silent tests set
  `GST_PLUGIN_FEATURE_RANK=fakeaudiosink:MAX,pulsesink:NONE,pipewiresink:NONE,alsasink:NONE` (real-time clock, no
  output). [L] With it, play → pause after ~3.2 s reported `position: 3.204`.

*(screenshot not included: audio: a WEM playing)*
*(screenshot not included: audio: a sound's WEMs and event tree)*
*(screenshot not included: audio: a bank)*
*(screenshot not included: audio: a switch group)*

## MCP server  (T067)

Every explorer window runs an MCP server on localhost, so a Claude Code session can see what the user is looking at,
explain it, and (when the user allows it) navigate the UI. Code: `internal/mcp` (transport), `app_mcp.go` (server, view
state, drive tools), `app_mcp_tools.go` (read tools), `app_ask.go` (Ask Claude, next section).

- **Transports** (same port):
  - MCP streamable HTTP, JSON responses: `POST /mcp` (JSON-RPC 2.0: `initialize`, `tools/list`, `tools/call`, `ping`;
    notifications get 202; batches). `GET /mcp` is 405 (the server never pushes over MCP).
  - Prometheus' plain shape: `POST /mcp/tools/list`, `POST /mcp/tools/call {"name", "arguments"}` →
    `{"isError", "result" | "error"}`, `GET /mcp/health`.
  - Requests with a non-local `Origin` get 403 and POSTs must be `application/json`, so a web page can't call the tools.
- **Port**: `127.0.0.1:17889` (Prometheus is 17888). Every window takes the first free port of 17889…17904; a window that
  didn't get 17889 retries it every 3 s, so the configured URL works again when the first window closes.
  `$OWEXPLORER_MCP=0` disables the server, `=<port>` moves the base.
- **Claude Code**: `.mcp.json` entry `"owexplorer": {"type": "http", "url": "http://127.0.0.1:17889/mcp"}` (no bridge
  script). Like every new project server, Claude Code asks once to approve it.
- **Status bar**: `● MCP :17889 <calls> drive off|on`. The tooltip shows the ports and the last tool. A click toggles the
  **drive gate**. The gate is off at every start, like Prometheus' write toggle: while it is off, `open` / `select`
  answer "drive tools are off".
- **What the window shows**: the frontend pushes a snapshot of its state (`mcpSnapshot` in `useExplorer.ts`: panes, open
  resource, tab, instance / path / graph node, document tabs, map cursor text) to `App.SetView`, debounced 120 ms.
  Every view also puts its own state into the pane's `extra[<tab>]`:
  - map: record, 2D / 3D, maximised, mirrored, the 2D plot's centre and zoom; in 3D, the camera (mode, position,
    target, yaw / pitch) and `aim`, the collision point under the cursor or crosshair (position, floor / wall /
    ceiling, triangle, mask, flags, material);
  - texture: mip, surface, channels, exposure, hover pixel;
  - dependency graph: the selected node.
- **Tools** (results are JSON, lists paged with `offset` / `limit`, trees cut at `depth`, 512 KB cap per result;
  arguments are strict, so an unknown field is an error; GUIDs as 16 hex digits, `0x…`, `ow://…`):

| tool | what |
|---|---|
| `status` | install, index state, this window (port, hub or not), the other windows (probed on 17889…17904), the drive gate |
| `current_view` | every window (or `window` = its port): panes with the resource, tab, **selection explained** (placement + its definition's components and graphs, graph node with parameters and links, tree instance / field, texture mip size, dependency node) and the view's own state |
| `describe` | name (+ source, guessed), type, size, packages, reference counts by kind / type, and a summary per kind: entity (components, graphs), graph (entries, states, node kinds, config vars, referenced resources), map (classes, networked entities, environment, collision), texture (format, size, mips, flags), model (LODs, submeshes, the looks placed with it, material slots → materials → colour textures, bones; T064), material (shader, textures with slots; T064; `preview`: family and the slot → input mapping, T071), string (text), package, other STU (root type, top-level fields) |
| `refs` | the reference index: `direction` out / in / both, `depth` 1–3 (breadth-first edges), kinds, types (number or hex), search; package membership is off unless asked for |
| `search` | name / note / GUID substring, optional type |
| `entity` | an entity definition: components, their graphs with node counts, references with their field paths |
| `statescript_graph` | `summary` / `nodes` / `full` (parameters = config-var summaries, plugs), or one `node` (m_nodes index) with its links in and out |
| `stu_tree` | the instance table, or one instance / `path` cut at `depth` |
| `map_placements` | a map's placements filtered by class, record type, text, or a sphere `near: [x, y, z, r]` (sorted by distance) |
| `placement` | one record: what it places, position, fields, instance data (overrides), the definition's components and graphs |
| `texture_image` | a texture (or a payload's texture) decoded by the texture view's decoder, returned as **MCP image content** (PNG) plus a text part with the info and the **path of the saved PNG** (`$XDG_RUNTIME_DIR/owexplorer/<guid>-m<mip>[-s<surface>\|-cross<n>][-<channels>].png`), for clients that don't show MCP images. `mip` (default: the smallest available mip that fills `max_size`), `surface`, `cross` (a cube's six faces unfolded, 4:3), `channels` (rgba / rgb / r / g / b / a; one channel as grey), `max_size` (default 512, box-filtered in Go), `exposure` (HDR, default auto), `z_from_xy`, `save`. Deltas come out composed |
| `material_image` (T071) | a material previewed by the software renderer (the Material tab's inputs; GGX + hemisphere ambient) on a sphere, plane or cube (default: the family's), as MCP image content plus the info (the resolved preview: family, inputs with slot / channel / confidence, parameters, notes) and the saved PNG's path (`$XDG_RUNTIME_DIR/owexplorer/<material>-material-<geometry>.png`). `geometry`, `size` (default 384, max 1024), `light_yaw` / `light_pitch`, `flip_green`, `inputs` (a subset), `save` |
| `model_image` (T064) | a model (or a look's model) rendered by a software rasteriser in Go (`internal/model` `Render`: z-buffer, Lambert, the look's colour textures, nearest sampling) as MCP image content plus the info and the saved PNG's path (`$XDG_RUNTIME_DIR/owexplorer/<model>-model-l<lod>-y<yaw>-p<pitch>.png`). `look` (default: the Model tab's choice), `lod` (mask; default the finest), `yaw` / `pitch` (default 35° / 20°), `size` (default 512, max 1024), `submesh` (highlighted), `mirror`, `pbr` (T071: each slot material's PBR inputs through the material renderer's lighting, per pixel; no normal maps; effects not drawn; alpha test from opacity), `save`. [L] the D.Va mech (47,160 triangles) at 512 px: 116 ms per call (mean of 3) |
| `audio_export` (T065) | a WEM, a sound's WEMs or a bank's WEMs decoded to WAV (16-bit PCM) or rebuilt as Ogg Vorbis in `$XDG_RUNTIME_DIR/owexplorer/<wem>.<format>`: the paths with length, channels, rate, peak / RMS dB. `format` (wav / ogg), `limit` (default 20, max 200). `describe` summarises WEMs, sounds (WEMs, event trees), banks (object counts, event trees, sounds, switch / state / parameter ids) and 0x02D–0x030 |
| `open` (drive) | open a GUID: `where` = current / tab / background / split / window, optional `tab` and `select` (record / instance / mip) |
| `select` (drive) | in a pane: the tab, a map record (the map focuses it), a tree instance / path, a graph node (m_nodes index; the canvas centres on it), a texture mip, a model submesh (T064) |
| `reply`, `asks` | Ask Claude (next section) |

  The drive tools wait up to 3–4 s until the window reports the change and return the pane as `current_view` shows it.
  `window: <port>` forwards the call to another window, where that window's own gate applies.
- **Windows**: every window answers the read tools itself. The data is the same install, so any window's server can
  describe anything. `current_view` without `window` collects every window's view.
- **Image results**: a tool can return `mcp.Content` (text and image parts; 8 MB cap) instead of one JSON text part.
  The plain endpoint returns it as `{"content": [...]}`.
- **Tests**:
  - `internal/mcp`: JSON-RPC, the plain endpoints, the gate, the result cap, strict arguments, origin / content-type,
    image content parts, the port scan and the takeover of 17889.
  - `TestTextureImage` (install): one texture per format (BC1, BC3, BC5, BC6H, R8), found by scanning the headers; the
    PNG part is at most `max_size` and equals the saved file. Also options (one channel, small size, no save, HDR
    exposure), a cube cross (4:3), a delta texture, a payload GUID, and the error cases.
    [D] BC6H only occurs in cube maps (no single-surface BC6H texture in 0.8).
  - `internal/texture` `TestMaskAndCross`.
  - `app_mcp_test.go` (install required): every tool through JSON-RPC against the install, with a fake frontend that
    reports snapshots and applies the drive commands. It checks the Anubis ship door (record 1021, placed `0x1d7`),
    the brain `0x39`, its graph, a texture, a string, a package, and 22 networked placements. `make test` now runs
    the main package too.

## Ask Claude  (T067)

A **✦ Ask** button on the selection: the header of every resource, the map's selection card (2D and 3D), a graph node,
the dependency graph's node, the texture toolbar, the tree's selected field, and the GUID menu. The status bar's
"✦ Ask" opens the panel. The question goes to **the Claude Code session that is already connected to the explorer's
MCP server**: there is no second Claude process, no API use and no special launch flag. That session answers with its
own model, and the usage counts against the user's subscription.

- **Subscribing**: the MCP server's instructions tell the session how to subscribe. The user says "watch the
  explorer", and the session starts its Monitor tool on
  `while true; do curl -sN http://127.0.0.1:17889/mcp/events; sleep 2; done` (30 min per watch, re-armed on expiry;
  the loop reconnects when the explorer restarts).
- **Stream** (`GET /mcp/events`, NDJSON, no heartbeats: every line wakes the session):
  - on connect, a `subscribed` line (with the number of pending questions), then every unanswered ask;
  - then one line per new event:
    - `ask`: `{type, id, question, about, subject, followUpOf, next}`;
    - `cancel`: `{type, id}`.
  - [L] A Monitor notification shows only the start of a long line: a first version put the context in the line, and
    the id and question were cut off. The line stays short, question first. The context is in the tool **`asks`**:
    the unanswered questions with what the selection is (placement brief, graph node, `describe`, tree field).
- **Panel**: a column docked on the right of the panes, so it takes space only while expanded.
  - Its width has a splitter (panel `ask.width`, default 420 px).
  - It is expanded or collapsed by the **thumb**, a small "✦ Ask" tab that is always visible: on the window's right
    edge when collapsed, on the panel's left edge when expanded. `Ctrl+J`, the status bar's ✦ Ask and the header's ⇥
    also toggle it.
  - The open state is remembered as panel `ask.open`.
  - A reply that arrives while the panel is collapsed puts a count badge on the thumb.
  - [L] Tested: restart → expanded again with the input focused; Ctrl+J from the text box → collapsed; a reply while
    collapsed → badge "1".
- **Subject** (round 2): the panel's chip "Asking about …" **follows the focused pane's selection**, so you click a
  thing, then type:
  - the selection can be a placement (2D, or a 3D marker), a graph node, a tree instance / field, a texture mip, or a
    dependency-graph node; with none, the open resource;
  - each view publishes its selection as `pane.extra.ask` (for its tab), and `useAsk` `liveSubject` reads it;
  - the chip's pin (📌) keeps the subject while you select other things;
  - an ✦ Ask button focuses the input, and pins its subject when it isn't the current selection (e.g. a GUID from
    the right-click menu);
  - restoring the panel at start-up doesn't take the keyboard focus.
- **Badges** (rounds 3–4): node references render as small tags (3 px corners, the raised-surface background;
  hover adds a border, the selection highlight uses the focus colour) with a dot in the node kind's colour (the graph view's
  colours): "#57 Wait" (the link's text, or the number plus a CamelCase type name that follows it in the reply, else
  the node's short type from `App.GraphNodes`). Map records (⌖, green), tree instances (⌥) and texture mips (▦) have
  their own pill styles; plain GUIDs stay GUID links.
- **Two-way** (round 3): the badges pointing at the current selection (the live one, not the pinned subject: graph
  node, placement, tree instance, texture mip) are highlighted.
  - A bar "the selection is mentioned n× · i/n ↑↓" steps through them, and the first scrolls into view.
  - The panel scrolls to a badge only when the selection changes or the stepper is clicked. New replies and re-renders
    only recount the badges, so reading or scrolling the conversation is never pulled back (fix during T064).
  - Clearing the selection removes the highlight.
  - [L] Selecting node #6 of `0x042` highlighted its two badges ("2×").
- **References in replies** (round 2; the syntax is in the MCP instructions and the `reply` description):
  - 16-hex-digit GUIDs → links;
  - `ow://<graph>/graph/<node index>`, bare or as `[#57 Wait](ow://…)` → opens the graph and centres the node;
  - `ow://<map>/map/<record>` → focuses the placement;
  - `ow://<guid>/tree/<instance>`, `ow://<texture>/texture/<mip>`;
  - a bare **`#N`** → node N of the last graph GUID named before it in the reply, else of the graph the question was
    about.

  A click opens in the focused pane; Ctrl-click opens a new tab. (`utils/mdlite.ts`.)
  - [L] A reply to a question pinned on node #6 of `0x042` linked `#6`, `#5`, `#7`, `[#1 Bool]` to `0x042`, and
    `#13` after "graph …028" to `0x028`. Clicking `#6` selected node 6; the map link selected record 1021.
  - Tree paths aren't links yet (the tree doesn't expand to a path).
- **Answer**: the session calls **`reply {id, text}`**, with markdown and an optional `final: false` for an interim
  note. The panel renders headings, lists, code, bold and italic; 16-digit GUIDs become links.
  - A follow-up carries `followUpOf` and continues the session's conversation.
  - "stop" withdraws a question (a `cancel` line), and "New" starts a new conversation.
  - The panel shows how many sessions listen, and the subscribe command when none does.
- **Windows**: the stream is served by the window holding 17889 (the hub). Another window posts its asks to the hub
  (`POST /mcp/events/publish`); ask ids are `<port>-<n>`, so the hub forwards the reply to the window that asked.
- [L] Tested live (Xvfb, `owexplorer-t067`), with a Claude Code session subscribed through Monitor:
  - an ask from a child window's header, answered through the hub and shown in the child's panel;
  - an ask from the 3D map's selection card on the ship door, then a follow-up. The follow-up event carried
    `followUpOf`, and both replies were shown in order.
  - `TestAsk` covers subscribe, the short line, `asks` with its context, interim and final replies, replay on
    reconnect, cancel, and a second window through the hub.
- **Open the selection elsewhere** (`components/OpenIn.vue`: ⧉ tab, ◫ split, ⊞ window): on the map's selection card
  (the placement's definition and each of its graphs; 2D, 3D and maximised), on a graph node (its instance in the
  tree), and on the dependency graph's node. Every GUID's right-click menu already had them.
  - [L] From the 3D card, "split" opened the door's graph in the split pane on the Graph tab.
  - [L] "window" started a child on `ow://040000000000005b/entity`, which appeared in `status` as the window on
    :17890.

## Towards an editor

These are not implemented yet; this is what is in place for them:
- Tree nodes carry stable paths plus field / type hashes.
- Selection is central state.
- Documents are cached per GUID.

Still missing:
- a writer in `systems/stu`, which is out of scope here;
- a change set keyed by path, for undo;
- a way to persist edited resources, e.g. as an override directory next to the install.

## Known gaps

- 3D mode (T066): the models layer (T064) draws the static world's models, not entities' models, map text or effects,
  and the handedness question above is open. Categories other than the heroes' are
  unnamed. Shapes can't be clicked, only hovered. Markers are not depth-sorted for picking beyond the cursor-surface
  test. The status bar's cursor ray is one Go call per mouse move (coalesced). WebGL is required; without it the view
  says so.

- The graph canvas uses the editor's `m_pos` with fixed-size boxes. Large graphs start fitted to their width and need zooming.
- Enum values show as numbers: there are no enum value names (none in `systems/stu/names`).
- Model view (T064): the colour texture only (no normal maps, no shaders or constant buffers, no alpha test), no
  animation or skinning (bones are drawn in bind pose). Looks for models that no STU object or map pairs with a look are
  guessed from shared material keys.
- Material preview (T071): a lit approximation, not the game's shaders.
  - Effects show their mask in white: the colour constant needs the shader instance's constant-buffer layout.
  - Inputs on UV1 start off.
  - Not used: 0x25's layer B, 0x24's detail normal, hair's flowmap / two lobes, 0x2B's subsurface colour, the gradient LUT
    ramps of 0x2C / 0x26 / 0x32 / 0x36, the probe cube maps of 0x2F.
  - The normal map's green convention is unconfirmed ("flip green").
  - Model tab / 3D map "material" modes: the mesh has UV0 only, so AO / emissive on UV1 are left out; vertex-colour
    blending (0x25 layer B) is not drawn; the lighting is studio lighting, not the game's.
- Types 0x00F (1) and 0x050 (3) have no label ("unknown", with size statistics). 0x04A / 0x08E / 0x08F are
  labelled "effect-like" from their header shape only; 0x085–0x087 "shader" from Maps.md §7 + OWLib names (unverified).
- Audio (T065): only the Sound source, the audio nodes' parent, Events and Actions of a bank's HIRC objects are parsed. The
  node parameters (props, positioning, state groups, RTPCs) and the container bodies (playlists, switch assignments)
  are shown as sizes. The bank → switch / state / parameter links are byte matches [?]. Sound, event and bank names
  are stripped in 0.8. Playback is only tested on Xvfb with a fake audio sink: whether it sounds right is for the user
  to confirm.
- The in / out counts on graph nodes and in the header count every kind, also the ones the graph filters out.
- The dependency graph's layout is columns + sibling stacking; with many expansions nodes of a column can drift far
  from their parent (pan, or "Graph from here").
- Not in the index yet: references inside effects and animations, and
  the APM's entry table (no reader known, ContentAccess.md §4).
- Fixed in T046: GUIDs were coloured by the wrong type (`guidType` read hex digits 0–2 instead of 1–3, i.e. bits
  52–63 instead of 48–59).
