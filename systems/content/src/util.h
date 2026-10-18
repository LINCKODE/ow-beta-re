#pragma once
// Internal helpers: byte order, errors, file I/O, zlib.
#include "content/store.h"

#include <cstdio>
#include <cstring>
#include <fstream>
#include <string>

namespace content {

inline uint16_t Le16(const uint8_t* p) { return uint16_t(p[0] | p[1] << 8); }
inline uint32_t Le32(const uint8_t* p) { return p[0] | p[1] << 8 | p[2] << 16 | uint32_t(p[3]) << 24; }
inline uint64_t Le64(const uint8_t* p) { return Le32(p) | uint64_t(Le32(p + 4)) << 32; }
inline uint32_t Be24(const uint8_t* p) { return uint32_t(p[0]) << 16 | p[1] << 8 | p[2]; }
inline uint32_t Be32(const uint8_t* p) { return uint32_t(p[0]) << 24 | p[1] << 16 | p[2] << 8 | p[3]; }

inline bool Fail(Error* err, std::string msg) {
    if (err) err->message = std::move(msg);
    return false;
}

inline std::string Hex64(uint64_t v) {
    char b[20];
    std::snprintf(b, sizeof b, "%016llX", static_cast<unsigned long long>(v));
    return b;
}

inline bool ReadFile(const std::string& path, Bytes& out) {
    std::ifstream f(path, std::ios::binary | std::ios::ate);
    if (!f) return false;
    auto n = f.tellg();
    if (n < 0) return false;
    out.resize(size_t(n));
    f.seekg(0);
    return bool(f.read(reinterpret_cast<char*>(out.data()), n));
}

inline bool ReadAt(const std::string& path, uint64_t offset, size_t size, Bytes& out) {
    std::ifstream f(path, std::ios::binary);
    if (!f) return false;
    f.seekg(std::streamoff(offset));
    out.resize(size);
    return bool(f.read(reinterpret_cast<char*>(out.data()), std::streamsize(size)));
}

// zlib (gzip = false) or gzip stream; appends to out. Stops at the end of the (first) stream.
bool Inflate(const uint8_t* data, size_t size, bool gzip, Bytes& out, Error* err, size_t* consumed = nullptr);

} // namespace content
