#include "stu/value.h"

#include "stu/reader.h"

#include <cstdio>

namespace stu {

namespace {

std::string Hex(uint64_t v, int width) {
    char buf[32];
    std::snprintf(buf, sizeof buf, "\"0x%0*llx\"", width, static_cast<unsigned long long>(v));
    return buf;
}

std::string Quote(const std::string& s) {
    std::string o = "\"";
    for (unsigned char c : s) {
        if (c == '"' || c == '\\') {
            o += '\\';
            o += static_cast<char>(c);
        } else if (c < 0x20) {
            char buf[8];
            std::snprintf(buf, sizeof buf, "\\u%04x", c);
            o += buf;
        } else {
            o += static_cast<char>(c);
        }
    }
    return o + "\"";
}

std::string Num(double d) {
    char buf[64];
    std::snprintf(buf, sizeof buf, "%.9g", d);
    std::string s = buf;
    if (s == "nan" || s == "-nan" || s == "inf" || s == "-inf") return Quote(s);
    return s;
}

// Primitive value as JSON (numbers, arrays of floats for vectors, hex for ids).
std::string PrimitiveJson(const Value& v) {
    auto f32s = [&](int n) {
        std::string o = "[";
        for (int i = 0; i < n && static_cast<size_t>(4 * i + 4) <= v.raw.size(); i++) {
            float x;
            std::memcpy(&x, v.raw.data() + 4 * i, 4);
            o += (i ? ", " : "") + Num(x);
        }
        return o + "]";
    };
    switch (v.type_hash) {
        case prim::u8: return std::to_string(v.As<uint8_t>());
        case prim::s8: return std::to_string(v.As<int8_t>());
        case prim::u16: return std::to_string(v.As<uint16_t>());
        case prim::s16: return std::to_string(v.As<int16_t>());
        case prim::u32: return std::to_string(v.As<uint32_t>());
        case prim::s32: return std::to_string(v.As<int32_t>());
        case prim::u64: return Hex(v.As<uint64_t>(), 16);
        case prim::s64: return std::to_string(v.As<int64_t>());
        case prim::f32: return Num(v.As<float>());
        case prim::f64: return Num(v.As<double>());
        case prim::teEntityID: return Hex(v.As<uint32_t>(), 8);
        case prim::teVec2: return f32s(2);
        case prim::teVec3:
        case prim::teVec3A:
        case prim::teColorRGB: return f32s(3);
        case prim::teVec4:
        case prim::teQuat:
        case prim::teColorRGBA: return f32s(4);
        case prim::teMtx43A: return f32s(16);
        default: {
            std::string o = "\"";
            char buf[4];
            for (uint8_t b : v.raw) {
                std::snprintf(buf, sizeof buf, "%02x", b);
                o += buf;
            }
            return o + "\"";
        }
    }
}

void Emit(std::string& o, const Value& v, const Registry& reg, bool handles, int ind);

void Pad(std::string& o, int ind) { o.append(static_cast<size_t>(ind) * 2, ' '); }

void EmitItems(std::string& o, const std::vector<Value>& items, const Registry& reg, bool handles, int ind,
               bool named) {
    if (items.empty()) {
        o += named ? "{}" : "[]";
        return;
    }
    o += named ? "{\n" : "[\n";
    for (size_t i = 0; i < items.size(); i++) {
        Pad(o, ind + 1);
        if (named) o += Quote(reg.NameOr(items[i].field_hash)) + ": ";
        Emit(o, items[i], reg, handles, ind + 1);
        o += i + 1 < items.size() ? ",\n" : "\n";
    }
    Pad(o, ind);
    o += named ? "}" : "]";
}

void Emit(std::string& o, const Value& v, const Registry& reg, bool handles, int ind) {
    switch (v.type) {
        case Value::Type::Null: o += "null"; break;
        case Value::Type::Primitive: o += PrimitiveJson(v); break;
        case Value::Type::String: o += Quote(v.str); break;
        case Value::Type::Enum: o += Hex(v.AsU32(), 8); break;
        case Value::Type::Object: o += "{\"$instance\": " + std::to_string(v.instance) + "}"; break;
        case Value::Type::Ref:
            o += "{\"$guid\": " + Hex(v.guid, 16);
            if (handles) o += ", \"$handle\": " + Hex(v.handle, 16);
            o += "}";
            break;
        case Value::Type::Error: o += "{\"$error\": " + Quote(v.str) + "}"; break;
        case Value::Type::Inline: {
            o += "{\"$type\": " + Quote(reg.NameOr(v.type_hash)) + ", \"$fields\": ";
            EmitItems(o, v.items, reg, handles, ind, true);
            o += "}";
            break;
        }
        case Value::Type::List: EmitItems(o, v.items, reg, handles, ind, false); break;
        case Value::Type::Map: {
            if (v.items.empty()) {
                o += "{}";
                break;
            }
            o += "{\n";
            for (size_t i = 0; i < v.items.size(); i++) {
                Pad(o, ind + 1);
                char key[16];
                std::snprintf(key, sizeof key, "0x%08x", v.items[i].map_key);
                o += Quote(key) + ": ";
                Emit(o, v.items[i], reg, handles, ind + 1);
                o += i + 1 < v.items.size() ? ",\n" : "\n";
            }
            Pad(o, ind);
            o += "}";
            break;
        }
    }
}

}  // namespace

std::string ToJson(const Value& v, const Registry& reg, bool with_handles, int indent) {
    std::string o;
    Emit(o, v, reg, with_handles, indent);
    return o;
}

std::string DocumentJson(const Document& doc, bool with_handles, bool summary) {
    const Registry& reg = doc.registry();
    const auto& ins = doc.instances();
    int bad = 0;
    for (const auto& in : ins) bad += !in.crc_ok;
    std::string o = "{\"instances\": " + std::to_string(ins.size()) + ", \"crc_mismatch\": " + std::to_string(bad) +
                    ", \"root\": [\n";
    for (size_t i = 0; i < ins.size(); i++) {
        const auto& in = ins[i];
        char buf[64];
        std::snprintf(buf, sizeof buf, "0x%x", in.offset);
        o += "  {\"index\": " + std::to_string(i) + ", \"offset\": \"" + buf + "\", \"flags\": " +
             std::to_string(in.flags) + ", \"type\": " + Quote(in.type ? reg.NameOr(in.type->hash) : "?") +
             ", \"crc_ok\": " + (in.crc_ok ? "true" : "false");
        if (!summary) o += ", \"value\": " + ToJson(doc.Decode(static_cast<int>(i)), reg, with_handles, 1);
        o += i + 1 < ins.size() ? "},\n" : "}\n";
    }
    return o + "]}\n";
}

}  // namespace stu
