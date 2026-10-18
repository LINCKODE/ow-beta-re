#include "test.h"

int main() {
    for (auto& c : test::cases()) {
        int before = test::failures();
        c.fn();
        std::printf("%s %s\n", test::failures() == before ? "ok  " : "FAIL", c.name);
    }
    std::printf("%zu tests, %d failed checks\n", test::cases().size(), test::failures());
    return test::failures() ? 1 : 0;
}
