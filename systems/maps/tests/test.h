#pragma once
// Minimal test harness (no external dependencies).
#include <cstdio>
#include <functional>
#include <string>
#include <vector>

namespace test {

struct Case {
    const char* name;
    std::function<void()> fn;
};

inline std::vector<Case>& cases() {
    static std::vector<Case> v;
    return v;
}
inline int& failures() {
    static int n = 0;
    return n;
}

struct Register {
    Register(const char* name, std::function<void()> fn) { cases().push_back({name, std::move(fn)}); }
};

} // namespace test

#define TEST_CAT2(a, b) a##b
#define TEST_CAT(a, b) TEST_CAT2(a, b)
#define TEST(name)                                                              \
    static void name();                                                         \
    static test::Register TEST_CAT(reg_, name)(#name, name);                    \
    static void name()

#define CHECK(cond)                                                             \
    do {                                                                        \
        if (!(cond)) {                                                          \
            std::printf("  FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);       \
            test::failures()++;                                                 \
        }                                                                       \
    } while (0)

#define CHECK_EQ(a, b)                                                          \
    do {                                                                        \
        auto _a = (a);                                                          \
        auto _b = (b);                                                          \
        if (!(_a == _b)) {                                                      \
            std::printf("  FAIL %s:%d: %s == %s (%s vs %s)\n", __FILE__, __LINE__, #a, #b, \
                        std::to_string(_a).c_str(), std::to_string(_b).c_str()); \
            test::failures()++;                                                 \
        }                                                                       \
    } while (0)
