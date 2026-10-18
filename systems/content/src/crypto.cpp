// Salsa20, MD5 and the 0.8 TACT keyring.
#include "content/ow08/ow08.h"

#include <cstring>

namespace content::ow08 {

namespace {

inline uint32_t Rotl(uint32_t v, int c) { return (v << c) | (v >> (32 - c)); }
inline uint32_t Le32(const uint8_t* p) { return p[0] | p[1] << 8 | p[2] << 16 | uint32_t(p[3]) << 24; }

void SalsaBlock(const uint32_t in[16], uint8_t out[64]) {
    uint32_t x[16];
    std::memcpy(x, in, sizeof x);
    auto qr = [&](int a, int b, int c, int d) {
        x[b] ^= Rotl(x[a] + x[d], 7);
        x[c] ^= Rotl(x[b] + x[a], 9);
        x[d] ^= Rotl(x[c] + x[b], 13);
        x[a] ^= Rotl(x[d] + x[c], 18);
    };
    for (int i = 0; i < 10; ++i) {
        qr(0, 4, 8, 12), qr(5, 9, 13, 1), qr(10, 14, 2, 6), qr(15, 3, 7, 11);
        qr(0, 1, 2, 3), qr(5, 6, 7, 4), qr(10, 11, 8, 9), qr(15, 12, 13, 14);
    }
    for (int i = 0; i < 16; ++i) {
        uint32_t v = x[i] + in[i];
        out[4 * i] = uint8_t(v), out[4 * i + 1] = uint8_t(v >> 8);
        out[4 * i + 2] = uint8_t(v >> 16), out[4 * i + 3] = uint8_t(v >> 24);
    }
}

} // namespace

void Salsa20Xor(const uint8_t key[16], const uint8_t nonce[8], uint8_t* data, size_t size) {
    static const uint8_t tau[] = "expand 16-byte k";
    uint32_t st[16];
    st[0] = Le32(tau), st[5] = Le32(tau + 4), st[10] = Le32(tau + 8), st[15] = Le32(tau + 12);
    for (int i = 0; i < 4; ++i) st[1 + i] = st[11 + i] = Le32(key + 4 * i);
    st[6] = Le32(nonce), st[7] = Le32(nonce + 4);
    uint64_t counter = 0;
    uint8_t ks[64];
    for (size_t pos = 0; pos < size; pos += 64, ++counter) {
        st[8] = uint32_t(counter), st[9] = uint32_t(counter >> 32);
        SalsaBlock(st, ks);
        size_t n = size - pos < 64 ? size - pos : 64;
        for (size_t i = 0; i < n; ++i) data[pos + i] ^= ks[i];
    }
}

// RFC 1321.
CKey Md5(const uint8_t* data, size_t size) {
    static const uint32_t K[64] = {
        0xd76aa478, 0xe8c7b756, 0x242070db, 0xc1bdceee, 0xf57c0faf, 0x4787c62a, 0xa8304613, 0xfd469501,
        0x698098d8, 0x8b44f7af, 0xffff5bb1, 0x895cd7be, 0x6b901122, 0xfd987193, 0xa679438e, 0x49b40821,
        0xf61e2562, 0xc040b340, 0x265e5a51, 0xe9b6c7aa, 0xd62f105d, 0x02441453, 0xd8a1e681, 0xe7d3fbc8,
        0x21e1cde6, 0xc33707d6, 0xf4d50d87, 0x455a14ed, 0xa9e3e905, 0xfcefa3f8, 0x676f02d9, 0x8d2a4c8a,
        0xfffa3942, 0x8771f681, 0x6d9d6122, 0xfde5380c, 0xa4beea44, 0x4bdecfa9, 0xf6bb4b60, 0xbebfbc70,
        0x289b7ec6, 0xeaa127fa, 0xd4ef3085, 0x04881d05, 0xd9d4d039, 0xe6db99e5, 0x1fa27cf8, 0xc4ac5665,
        0xf4292244, 0x432aff97, 0xab9423a7, 0xfc93a039, 0x655b59c3, 0x8f0ccc92, 0xffeff47d, 0x85845dd1,
        0x6fa87e4f, 0xfe2ce6e0, 0xa3014314, 0x4e0811a1, 0xf7537e82, 0xbd3af235, 0x2ad7d2bb, 0xeb86d391};
    static const int R[64] = {7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22,
                              5, 9,  14, 20, 5, 9,  14, 20, 5, 9,  14, 20, 5, 9,  14, 20,
                              4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23,
                              6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21};
    uint32_t h[4] = {0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476};
    auto block = [&](const uint8_t* p) {
        uint32_t m[16];
        for (int i = 0; i < 16; ++i) m[i] = Le32(p + 4 * i);
        uint32_t a = h[0], b = h[1], c = h[2], d = h[3];
        for (int i = 0; i < 64; ++i) {
            uint32_t f;
            int g;
            if (i < 16) f = (b & c) | (~b & d), g = i;
            else if (i < 32) f = (d & b) | (~d & c), g = (5 * i + 1) & 15;
            else if (i < 48) f = b ^ c ^ d, g = (3 * i + 5) & 15;
            else f = c ^ (b | ~d), g = (7 * i) & 15;
            uint32_t t = d;
            d = c, c = b;
            b = b + Rotl(a + f + K[i] + m[g], R[i]);
            a = t;
        }
        h[0] += a, h[1] += b, h[2] += c, h[3] += d;
    };
    size_t full = size & ~size_t(63);
    for (size_t i = 0; i < full; i += 64) block(data + i);
    uint8_t tail[128] = {};
    size_t rest = size - full;
    std::memcpy(tail, data + full, rest);
    tail[rest] = 0x80;
    size_t tl = rest < 56 ? 64 : 128;
    uint64_t bits = uint64_t(size) * 8;
    for (int i = 0; i < 8; ++i) tail[tl - 8 + i] = uint8_t(bits >> (8 * i));
    for (size_t i = 0; i < tl; i += 64) block(tail + i);
    CKey out;
    for (int i = 0; i < 4; ++i)
        for (int j = 0; j < 4; ++j) out.b[4 * i + j] = uint8_t(h[i] >> (8 * j));
    return out;
}

Keyring Keyring::Builtin() {
    struct K {
        uint64_t name;
        uint8_t key[16];
    };
    // FB680CB6A8BF81F3 is added by the client itself (ContainerInit 0x9ce9d0); the others were streamed
    // by the live service (prometheus/Prometheus-Core/dllmain.cpp `casc_keys`).
    static const K kKeys[] = {
        {0xFB680CB6A8BF81F3, {0x62, 0xD9, 0x0E, 0xFA, 0x7F, 0x36, 0xD7, 0x1C, 0x39, 0x8A, 0xE2, 0xF1, 0xFE, 0x37, 0xBD, 0xB9}},
        {0x402CD9D8D6BFED98, {0xAE, 0xB0, 0xEA, 0xDE, 0xA4, 0x76, 0x12, 0xFE, 0x6C, 0x04, 0x1A, 0x03, 0x95, 0x8D, 0xF2, 0x41}},
        {0xDBD3371554F60306, {0x34, 0xE3, 0x97, 0xAC, 0xE6, 0xDD, 0x30, 0xEE, 0xFD, 0xC9, 0x8A, 0x2A, 0xB0, 0x93, 0xCD, 0x3C}},
        {0x11A9203C9881710A, {0x2E, 0x2C, 0xB8, 0xC3, 0x97, 0xC2, 0xF2, 0x4E, 0xD0, 0xB5, 0xE4, 0x52, 0xF1, 0x8D, 0xC2, 0x67}},
        {0xA19C4F859F6EFA54, {0x01, 0x96, 0xCB, 0x6F, 0x5E, 0xCB, 0xAD, 0x7C, 0xB5, 0x28, 0x38, 0x91, 0xB9, 0x71, 0x2B, 0x4B}},
        {0x87AEBBC9C4E6B601, {0x68, 0x5E, 0x86, 0xC6, 0x06, 0x3D, 0xFD, 0xA6, 0xC9, 0xE8, 0x52, 0x98, 0x07, 0x6B, 0x3D, 0x42}},
        {0xDEE3A0521EFF6F03, {0xAD, 0x74, 0x0C, 0xE3, 0xFF, 0xFF, 0x92, 0x31, 0x46, 0x81, 0x26, 0x98, 0x57, 0x08, 0xE1, 0xB9}},
        {0x8C9106108AA84F07, {0x53, 0xD8, 0x59, 0xDD, 0xA2, 0x63, 0x5A, 0x38, 0xDC, 0x32, 0xE7, 0x2B, 0x11, 0xB3, 0x2F, 0x29}},
        {0x49166D358A34D815, {0x66, 0x78, 0x68, 0xCD, 0x94, 0xEA, 0x01, 0x35, 0xB9, 0xB1, 0x6C, 0x93, 0xB1, 0x12, 0x4A, 0xBA}},
    };
    Keyring r;
    for (auto& k : kKeys) {
        std::array<uint8_t, 16> a;
        std::memcpy(a.data(), k.key, 16);
        r.Add(k.name, a);
    }
    return r;
}

} // namespace content::ow08
