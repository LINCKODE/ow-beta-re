// StatescriptPrimitive: the value type of variables, config vars and expressions.
// docs/StatescriptRuntime.md §1.7 (types, truthiness), §5 (comparison / conversion rules).
#pragma once
#include <cstdint>
#include <string>

namespace ss {

enum class PrimType : uint32_t {
    None = 0,        // never written by the client's constructors; seen in a live bag (var 0x007)
    Byte = 1,        // bool
    Int = 2,
    Float = 3,
    Int64 = 4,       // GUIDs
    String = 5,
    Vec3 = 6,
    IntVec4 = 7,     // 4 x i16
    InstanceId = 8,  // SS_INSTANCEID: 2 x i16 (lo, hi)
    Flt16Vec4 = 9,
    ColorRGBA = 10,
    Entity = 11,     // u32 entity id
};

// 16-byte value: payload (+0), extra payload (+8, VEC3.z) and type (+0xC) in the client. The string
// payload is owned here instead of by the client's string allocator.
struct Primitive {
    PrimType type = PrimType::Int;  // default-constructed vars are INT 0 (ctor 0xf5ef40)
    uint64_t bits = 0;              // payload bytes 0..7 (little endian, as in the client)
    uint32_t extra = 0;             // payload bytes 8..11
    std::string str;                // PrimType::String

    static Primitive Byte(bool v) { return Make(PrimType::Byte, v ? 1 : 0); }
    static Primitive Int(int32_t v) { return Make(PrimType::Int, static_cast<uint32_t>(v)); }
    static Primitive Float(float v);
    static Primitive Int64(uint64_t v) { return Make(PrimType::Int64, v); }
    static Primitive Entity(uint32_t v) { return Make(PrimType::Entity, v); }
    static Primitive InstanceId(int16_t lo, int16_t hi) {
        return Make(PrimType::InstanceId, static_cast<uint16_t>(lo) | (uint64_t(static_cast<uint16_t>(hi)) << 16));
    }
    static Primitive Vec3(float x, float y, float z);
    static Primitive String(std::string s) {
        Primitive p;
        p.type = PrimType::String;
        p.str = std::move(s);
        return p;
    }
    static Primitive Make(PrimType t, uint64_t bits, uint32_t extra = 0) {
        Primitive p;
        p.type = t;
        p.bits = bits;
        p.extra = extra;
        return p;
    }

    int32_t AsInt() const { return static_cast<int32_t>(static_cast<uint32_t>(bits)); }
    float AsFloat() const;  // raw FLT payload
    uint32_t AsU32() const { return static_cast<uint32_t>(bits); }

    // Numeric conversion used by the expression interpreters and comparisons: BYTE -> 0/1,
    // INT -> (float), FLT; every other type -> 0 [G] (0xf4c7b0, 0xf4adc0, 0xf3d7a0).
    float ToFloat() const;
    // Truthiness (0xee97b0 / 0xec8a80) [G]: §1.7 table.
    bool Truthy() const;
};

// Epsilon of the float comparisons (DAT 0x162c6e8 = 2^-16.35 = 1.1920929e-05) [G].
inline constexpr float kFloatEpsilon = 1.1920928955078125e-05f;

// Strict identity (0xec9bb0) [G]: same type and payload. Used to skip var writes that change nothing.
bool Identical(const Primitive& a, const Primitive& b);
// Loose equality (0xec99c0) [G]: BYTE compares by truthiness against any type, INT/FLT across types within
// kFloatEpsilon, INT64/STRING/ENTITY/... by payload. Used by the `==` opcode and the Switch cases.
bool LooselyEqual(const Primitive& a, const Primitive& b);

// "TYPE payload" as Prometheus' ss_trace prints it (fixture `var` values, setvar notification text).
std::string ToTraceText(const Primitive& p);

}  // namespace ss
