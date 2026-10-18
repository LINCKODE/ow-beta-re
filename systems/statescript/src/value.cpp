#include "statescript/value.h"

#include <bit>
#include <cmath>
#include <format>

namespace ss {

Primitive Primitive::Float(float v) { return Make(PrimType::Float, std::bit_cast<uint32_t>(v)); }

Primitive Primitive::Vec3(float x, float y, float z) {
    return Make(PrimType::Vec3, std::bit_cast<uint32_t>(x) | (uint64_t(std::bit_cast<uint32_t>(y)) << 32),
                std::bit_cast<uint32_t>(z));
}

float Primitive::AsFloat() const { return std::bit_cast<float>(static_cast<uint32_t>(bits)); }

float Primitive::ToFloat() const {
    switch (type) {
    case PrimType::Byte: return (bits & 0xFF) ? 1.0f : 0.0f;
    case PrimType::Int: return static_cast<float>(AsInt());
    case PrimType::Float: return AsFloat();
    default: return 0.0f;
    }
}

bool Primitive::Truthy() const {
    switch (type) {
    case PrimType::Byte: return (bits & 0xFF) != 0;
    case PrimType::Int: return AsInt() != 0;
    case PrimType::Float: return std::fabs(AsFloat()) > kFloatEpsilon;
    case PrimType::Int64: return bits != 0;
    case PrimType::String: return !str.empty();
    case PrimType::Vec3: return bits != 0 || extra != 0;  // [?] "any component != 0" on the raw bits
    case PrimType::IntVec4: return bits != 0;
    case PrimType::InstanceId: return ((bits >> 16) & 0xFFFF) != 0;
    case PrimType::Flt16Vec4: return bits != 0;
    case PrimType::ColorRGBA: return (bits & 0xFFFFFFFF) != 0;
    case PrimType::Entity: return AsU32() != 0;  // [?] "valid id"
    default: return false;
    }
}

namespace {
bool SameBytes(const Primitive& a, const Primitive& b) {
    return a.type == b.type && a.bits == b.bits && a.extra == b.extra;
}
}  // namespace

bool Identical(const Primitive& a, const Primitive& b) {
    if (a.type != b.type) return false;
    switch (a.type) {
    case PrimType::Byte: return (a.bits & 0xFF) == (b.bits & 0xFF);
    case PrimType::Int:
    case PrimType::InstanceId:
    case PrimType::Entity: return a.AsU32() == b.AsU32();
    case PrimType::Float: return a.AsFloat() == b.AsFloat();
    case PrimType::Int64:
    case PrimType::IntVec4: return a.bits == b.bits;
    case PrimType::String: return a.str == b.str;
    default: return SameBytes(a, b);
    }
}

bool LooselyEqual(const Primitive& a, const Primitive& b) {
    if (b.type == PrimType::Byte) return a.Truthy() == ((b.bits & 0xFF) != 0);
    switch (a.type) {
    case PrimType::Byte: return ((a.bits & 0xFF) != 0) == b.Truthy();
    case PrimType::Int:
        if (b.type == PrimType::Int) return a.AsInt() == b.AsInt();
        if (b.type == PrimType::Float) return std::fabs(float(a.AsInt()) - b.AsFloat()) <= kFloatEpsilon;
        return false;
    case PrimType::Float:
        if (b.type == PrimType::Int) return std::fabs(a.AsFloat() - float(b.AsInt())) <= kFloatEpsilon;
        if (b.type == PrimType::Float)
            return !(a.AsFloat() < b.AsFloat() - kFloatEpsilon) && a.AsFloat() <= b.AsFloat() + kFloatEpsilon;
        return false;
    case PrimType::Int64: return b.type == PrimType::Int64 && a.bits == b.bits;
    case PrimType::String: return b.type == PrimType::String && !a.str.empty() && !b.str.empty() && a.str == b.str;
    case PrimType::IntVec4: return b.type == PrimType::IntVec4 && a.bits == b.bits;
    case PrimType::InstanceId:
    case PrimType::Entity: return b.type == a.type && a.AsU32() == b.AsU32();
    default: return SameBytes(a, b);
    }
}

std::string ToTraceText(const Primitive& p) {
    switch (p.type) {
    case PrimType::Byte: return std::format("BYTE {}", unsigned(p.bits & 0xFF));
    case PrimType::Int: return std::format("INT {}", p.AsInt());
    case PrimType::Float: return std::format("FLT {}", p.AsFloat());
    case PrimType::Int64: return std::format("INT64 0x{:x}", p.bits);
    case PrimType::String: return std::format("STRING {}", p.str.substr(0, 64));
    case PrimType::Vec3:
        return std::format("VEC3 {} {} {}", std::bit_cast<float>(uint32_t(p.bits)),
                           std::bit_cast<float>(uint32_t(p.bits >> 32)), std::bit_cast<float>(p.extra));
    case PrimType::InstanceId:
        return std::format("INSTID {} {}", int16_t(p.bits & 0xFFFF), int16_t((p.bits >> 16) & 0xFFFF));
    case PrimType::Entity: return std::format("ENTITY 0x{:x}", p.AsU32());
    default: return std::format("T{} 0x{:x} 0x{:x}", uint32_t(p.type), p.bits, p.extra);
    }
}

}  // namespace ss
