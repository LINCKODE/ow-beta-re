# systems/maps: map data reader/writer

Offline reader and writer for the beta 0.8 (build 24919) map data (resource type 0x002, GUIDs `0x080…`): the header,
the map environment, all 14 record types (model groups, models, occluders, reflection probes, text, placed models,
lights, sound volumes, entities, sound emitters, effects, volumes), the entities' instance-data blobs, static
collision, AI blobs and the sound-space table. Format: [docs/Maps.md](../../docs/Maps.md) §2; tools and edits: §9, §10.

```
cmake --build systems/content/build                  # map_dump and the tests read the install through systems/content
cmake -S systems/maps -B systems/maps/build -G Ninja && cmake --build systems/maps/build
ctest --test-dir systems/maps/build                  # synthetic + all 19 maps (~10 s)
(cd systems/maps/go && go test -count=1 ./...)       # cgo wrapper
systems/maps/build/map_dump --summary 080000000000005B          # or a file; --records / --fields for JSON
```

| path | what |
|---|---|
| `include/maps/mapdata.h` | `maps::Parse` → `MapData` (header, records with typed members + decoded field list, collision, links), `ToJson`, collision elements (`CollisionVertex`, `CollisionBvhNode`, `CollisionTri`), writer: `SetTransform`, `SetDefinition`, `AddEntity` |
| `src/` | parser (`mapdata.cpp`), JSON (`json.cpp`), writer with relocation (`writer.cpp`), C API (`capi.cpp`) |
| `capi/maps.h` | C API (`libow_maps_c.so`, `libow_maps_c_static.a`): open/close, JSON, bytes, edits |
| `go/` | Go module `owmaps`, package `maps` (cgo): `Open` → `Doc` (`Map`, `JSON`, `Bytes`, `SetTransform`, `SetDefinition`, `AddEntity`); `DecodeGeometry` (pure Go, `collision.go`): spheres, capsules, hulls (faces), the mesh with its BVH, `Mesh.Raycast` |
| `tools/map_dump.cpp` | JSON / summary dump |
| `tools/mapcomp` | Go: what a map pulls in (package contents, direct references) |
| `tools/mapedit` | Go: write an edited map data (`-move`, `-add`) for runtime injection (Prometheus `inject_resource`) |
| `edits/` | generated edited map data (safe to delete) |
