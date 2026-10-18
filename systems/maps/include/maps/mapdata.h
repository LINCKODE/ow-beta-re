#pragma once
// maps::MapData: the map data resource of build 24919 (type 0x002, GUIDs 0x080…, "MAPDATA").
//
// One blob per map (2.6–119 MB): a fixed header (bounds, a default camera, the map environment, the
// record table), 14 kinds of placement records in 16 record-type sections, the entities' embedded
// instance data (STUD blobs), the static collision (triangle mesh + BVH, convex hulls, capsules,
// spheres), the AI navigation and tactical data (two STUD blobs) and the sound-space table.
// Format, with the client code each field was read in: docs/Maps.md §2.
//
// Parse() is bounds-checked (malformed input returns an error, never reads out of range) and copies
// nothing: offsets are into the caller's bytes. The writer functions edit a copy of the bytes in place.
#include <array>
#include <cstdint>
#include <optional>
#include <span>
#include <string>
#include <vector>

namespace maps {

using Guid = uint64_t;
using Bytes = std::vector<uint8_t>;

struct Vec3 {
    float x = 0, y = 0, z = 0;
};
struct Vec4 {
    float x = 0, y = 0, z = 0, w = 0;
};
struct Quat {
    float x = 0, y = 0, z = 0, w = 1;
};
// Placement transform as the records store it: position, scale, rotation (quaternion x, y, z, w).
struct Transform {
    Vec3 pos;
    Vec3 scale{1, 1, 1};
    Quat rot;
};
struct Uuid {
    std::array<uint8_t, 16> b{};
    bool IsZero() const;
    std::string Hex() const;  // 32 hex digits, byte order as stored
    bool operator==(const Uuid&) const = default;
};

// Resource type as the client dispatches it: bitreverse12((guid >> 48) & 0xFFF) + 1.
uint32_t GuidType(Guid g);

// Record types (record +0x13). Names from the client code that consumes each type (docs/Maps.md §2.4).
enum RecordType : uint8_t {
    kModelGroup = 1,      // a model + per-look instance lists (static, instanced)
    kModel = 2,           // one static model
    kOccluder = 3,        // occluder quad (oriented box, flattened along its thinnest axis)
    kReflectionProbe = 4, // two textures + influence box
    kType5 = 5,           // counted by the loader, no records in 0.8, no reader found
    kTextLabel = 6,       // inline text (char[32]) + colour
    kMapText = 7,         // localized string + STUMapFont + material
    kPlacedModel = 8,     // model spawned as an entity {01 03 04}
    kLight = 9,           // inline entity {01 05}
    kSoundVolume = 10,    // shapes (boxes, spheres, capsules) → a STUSoundSpace, entity {01 06}
    kEntity = 11,         // entity from an STUEntityDefinition
    kSoundEmitter = 12,   // sound (0x02C) at a transform, via the sound system
    kEffect = 13,         // inline entity {01 03 0d}
    kVolume44 = 14,       // entity {01 44}: box + colour + texture (purpose not identified)
    kVolume45 = 15,       // entity {01 45}: box (purpose not identified)
};
const char* RecordTypeName(uint8_t type);
inline constexpr int kNumRecordTypes = 16;
inline constexpr uint8_t kFirstPlacedType = 8;  // types >= 8 are in the client's placed-entry list

// A decoded field of a record (for dumps and viewers). `at` is the offset inside the record.
struct Field {
    enum Kind : uint8_t { U32, I32, F32, Hex32, Guid64, U64, Vec3f, Vec4f, Text } kind = U32;
    std::string name;
    uint32_t at = 0;
    uint64_t u = 0;                // U32, I32 (sign-extended), Hex32, Guid64, U64
    std::array<float, 4> f{};      // F32 (f[0]), Vec3f, Vec4f
    std::string text;              // Text
    Field() = default;
    Field(Kind k, std::string n, uint32_t a, uint64_t v = 0) : kind(k), name(std::move(n)), at(a), u(v) {}
};

// One instance of a model group (type 1): the group's look + the instance transform.
struct GroupInstance {
    Guid look = 0;
    Transform xf;
    Uuid uuid;          // instance +0x28
    uint32_t at = 0;    // offset of the instance in the map data
};

// One override blob of an entity placement: {u32 slot, u32 offset} at record +0x80 + 8·i; the blob (a STUD
// file, loaded in place by the MAPDATA constructor) starts at the pair + offset.
struct InstanceData {
    uint32_t slot = 0;      // component slot (the client matches it against a per-component table)
    uint32_t offset = 0;    // absolute offset of the blob in the map data
    uint32_t size = 0;      // up to the next blob / the end of the blob region
};

// A shape of a sound volume (type 10), in world space.
struct Shape {
    enum Kind : uint8_t { Box, Sphere, Capsule } kind;
    Quat rot;               // boxes and capsules
    Vec3 pos;
    Vec3 half;              // box half extents; sphere: x = radius; capsule: x = radius, y = half height
};

struct Record {
    uint32_t offset = 0;    // in the map data
    uint32_t size = 0;
    uint8_t type = 0;
    uint8_t flags = 0;      // +0x10: bit0 gameplay, bit1 presentation, bit3 large (always-drawn class)
    uint8_t b11 = 0;        // +0x11 (0 in 0.8)
    uint8_t phase = 0;      // +0x12: load pass the record is spawned in (header +0x1F8 = last pass)
    Uuid uuid;              // placement GUID (= entity +0x18 of what it spawns); zero on types 1 and 2… (see doc)
    int32_t index = -1;     // index in the section
    int32_t placed = -1;    // placed-entry index (types >= 8, in type order): what a server from-map create sends

    bool has_xf = false;    // the record has a world transform (below)
    Transform xf;
    uint32_t xf_at = 0;     // offset of pos (xf_layout says where scale/rot are)
    uint8_t xf_layout = 0;  // see XfLayout in mapdata.cpp: which of pos/scale/rot exist and where
    Guid primary = 0;       // definition / model / effect / sound / string, by type
    Guid secondary = 0;     // look / font / skin, by type

    std::vector<Field> fields;              // every decoded field (for dumps/viewers)
    std::vector<GroupInstance> instances;   // type 1
    std::vector<InstanceData> instance_data;// type 11
    std::vector<Shape> shapes;              // type 10
};

struct Aabb {
    Vec4 min, max;
};

// Map environment (header +0xF0…+0x1EF): copied as one block into the world's map state; which world code
// uses each field: docs/Maps.md §2.2.
struct Environment {
    Guid gamemode_def = 0;   // +0xF0  entity definition of the map's gamemode brain (Anubis: 0x…039)
    Guid texture_f8 = 0;     // +0xF8
    Guid lighting = 0;       // +0x100 LIGHTINGDATA (0x00E), acquired by the static world at load
    Guid texture_108 = 0;    // +0x108
    Guid sky_model = 0;      // +0x110 model + look of the sky entity the world creates
    Guid sky_look = 0;       // +0x118
    Guid sound_space = 0;    // +0x120 default STUSoundSpace (0x055)
    Guid texture_128 = 0, texture_130 = 0;
    Guid name = 0;           // +0x138 string (0x07C)
    Vec3 sun_dir;            // +0x14C (unit vector) [inferred]
    std::array<Guid, 3> textures_180{};  // +0x180…+0x190
    std::array<float, 44> raw{};         // +0x140…+0x1EF as floats (GUID slots included), for dumps
};

// Static collision (header +0x1FC… counts, +0x2D8… tables), consumed by the physics world
// (FUN_7ff650a239a0) as four shape kinds. Pointers are map data offsets (fixed up at load).
struct CollisionSphere { uint32_t at; Vec3 center; float radius; };
struct CollisionCapsule { uint32_t at; Vec3 p0, p1; float radius; };
// Convex hull item (0x90): ptr[0] vertices (packed vec3), ptr[1] face planes {normal, d} (n·x = d, outward),
// ptr[2] half-edges (4 B: s8 twin delta (+1 / -1), u8 origin vertex, u8 face, u8 next), ptr[3] u8 first half-edge per
// face (T066: all hulls of the 19 maps close and pair up, tests/test_install.cpp).
struct CollisionHull { uint32_t at; Vec3 center; uint32_t verts, faces, half_edges; std::array<uint64_t, 4> ptr; };
// Triangle mesh item (0x58): +0x00 BVH nodes, +0x08 vertices, +0x10 triangles, +0x18 node count, +0x1C vertex count,
// +0x20 triangle count, +0x24 BVH depth, +0x28 u32 (T062: FUN_7ff6511adca0 copies them in this order).
// BVH node (32 B, pre-order, root = 0): min xyz, max xyz (f32), +0x18 i32 (internal: right child = this node +
// offset, left = next node; leaf: first triangle), +0x1C u16 mask (OR of the subtree's triangle masks), +0x1E u8
// (0 / 1 / 2 on internal nodes [? split axis], 3 on leaves), +0x1F u8 triangle count (0 = internal).
// Triangle (32 B): +0x00 3 × i32 vertex indices, +0x0C 3 × i32 wing vertices (-1 = open edge), +0x18 u16 material,
// +0x1A u16 surface flags, +0x1C u16 mask. Vertex: vec4 (xyz + pad).
struct CollisionMesh { uint32_t at; uint64_t bvh, verts, tris; uint32_t node_count, vert_count, tri_count; uint32_t depth, u28; };
struct CollisionNode { Vec3 min, max; int32_t index; uint16_t mask; uint8_t u1e, tri_count; };
struct CollisionTriangle { std::array<int32_t, 3> v, wing; uint16_t material, flags, mask; };
struct Collision {
    std::vector<CollisionSphere> spheres;    // 0x60-byte items (+0x2D8, count +0x1FC)
    std::vector<CollisionCapsule> capsules;  // 0x68 (+0x2E0, +0x200)
    std::vector<CollisionHull> hulls;        // 0x90 (+0x2E8, +0x204)
    std::vector<CollisionMesh> meshes;       // 0x58 (+0x2F0, +0x208): the map's level geometry (BVH nodes, vec4 verts, 32-byte tris)
};

// Header +0x308/+0x310: sound volume (type 10) placement GUID → STUSoundSpace (0x055).
struct SoundSpaceLink {
    Uuid volume;
    Guid space = 0;
};

struct Header {
    std::array<Aabb, 5> bounds{};   // +0x00, +0x20, +0x40, +0x60, +0x80 (first: whole map incl. sky; see doc)
    Vec4 camera_pos;                // +0xA0 default camera position [inferred]
    std::array<float, 16> camera_rot{}; // +0xB0 4×4 rotation [inferred]
    Environment env;
    uint32_t u1f0 = 0;              // +0x1F0 (static world flag)
    uint32_t u1f4 = 0;
    uint16_t last_phase = 0;        // +0x1F8 last load pass (0 on every map)
    uint16_t u1fa = 0;
    std::array<uint32_t, 4> collision_counts{};  // +0x1FC
    uint32_t record_count = 0;      // +0x20C
    std::array<uint32_t, kNumRecordTypes> offsets{}, counts{}, sizes{};  // +0x210, +0x250, +0x290
    uint32_t u2d0 = 0, u2d4 = 0;
    std::array<uint64_t, 4> collision_tables{};  // +0x2D8…+0x2F0
    uint64_t nav = 0;               // +0x2F8 STUD: 0x5a41c3a6 {m_cellData: []STUAICellData, m_collisionCrc}
    uint64_t tactical = 0;          // +0x300 STUD: STUAITacticalVolumeMap
    uint64_t sound_space_count = 0; // +0x308
    uint64_t sound_space_table = 0; // +0x310
    uint64_t u318 = 0;
};

struct MapData {
    uint32_t size = 0;
    Header header;
    std::vector<Record> records;    // section order (type 0…15), file order inside a section
    Collision collision;
    std::vector<SoundSpaceLink> sound_spaces;
    uint32_t nav_size = 0, tactical_size = 0;   // byte sizes of the two AI blobs
    uint32_t blob_begin = 0, blob_end = 0;      // region of the entities' instance data blobs
    uint32_t placed = 0;                        // records of type >= 8

    const Record* ByPlaced(uint32_t placed_index) const;
    const Record* ByUuid(const Uuid& u) const;
};

struct Error {
    std::string message;
};

// Parses and validates a map data blob. `data` must outlive nothing (MapData holds offsets and copies).
std::optional<MapData> Parse(std::span<const uint8_t> data, Error* err = nullptr);

// JSON of a parsed map (records, collision summary, header). `records` = include every record (large:
// Dorado ~5,000 records); `fields` = include the per-record decoded field lists.
std::string ToJson(const MapData& m, bool records = true, bool fields = false);

// Elements of a parsed collision mesh (`data` = the bytes it was parsed from; i < the count, unchecked beyond the
// bounds Parse validated).
Vec3 CollisionVertex(std::span<const uint8_t> data, const CollisionMesh& c, uint32_t i);
CollisionNode CollisionBvhNode(std::span<const uint8_t> data, const CollisionMesh& c, uint32_t i);
CollisionTriangle CollisionTri(std::span<const uint8_t> data, const CollisionMesh& c, uint32_t i);

// --- writer ---------------------------------------------------------------------------------------

// Rewrites a record's transform in place (the parts the record stores: e.g. lights have no scale).
// `data` must be the bytes `m` was parsed from. Returns false if the record has no transform.
bool SetTransform(Bytes& data, const Record& r, const Transform& xf, Error* err = nullptr);

// Sets an entity record's definition (type 11 +0x18) in place.
bool SetDefinition(Bytes& data, const Record& r, Guid def, Error* err = nullptr);

// Appends a copy of entity record `src` (type 11) with a new placement GUID and transform at the end of
// the entity section; every later section, the instance-data blobs, the collision/nav/sound tables and
// every stored offset are relocated. Placed indexes of the records after the entity section shift by one.
bool AddEntity(Bytes& data, const MapData& m, const Record& src, const Uuid& uuid, const Transform& xf,
               Error* err = nullptr);

}  // namespace maps
