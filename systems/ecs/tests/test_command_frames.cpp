// Command frames: clock (FUN_7ff650ce63a0 / FUN_7ff65100e950), CommandFrameSystem
// (FUN_7ff650c167d0 / FUN_7ff650feea30 / FUN_7ff650fef420). See docs/ECS.md §8.
#include "ecs/command_frame_systems.h"
#include "test.h"
#include <string>

using namespace ecs;

namespace {

struct LogListener : CommandFrameListener {
    std::string name;
    std::vector<std::string>* log;
    LogListener(std::string n, std::vector<std::string>* l) : name(std::move(n)), log(l) {}
    void OnCommandFrame(uint32_t cf, uint32_t flags) override {
        log->push_back(name + " cf " + std::to_string(cf) + " f" + std::to_string(flags));
    }
    void OnPreCommandFrames() override { log->push_back(name + " pre"); }
    void OnPostCommandFrames() override { log->push_back(name + " post"); }
};

} // namespace

TEST(clock_dilation_and_accumulation) {
    CHECK_EQ(CommandFrameClock::DilatedMicros(0.016f, 0), int64_t(16000));
    CHECK_EQ(CommandFrameClock::DilatedMicros(0.016f, kClockSlowDown), int64_t(15800));
    CHECK_EQ(CommandFrameClock::DilatedMicros(0.016f, kClockSlowDown | kClockStrong), int64_t(15000));
    CHECK_EQ(CommandFrameClock::DilatedMicros(0.016f, kClockSpeedUp), int64_t(16200));
    CHECK_EQ(CommandFrameClock::DilatedMicros(0.016f, kClockSpeedUp | kClockSlowDown), int64_t(15800));

    CommandFrameClock c;
    c.Seed(704, 14423);
    c.Advance(81135); // recorded live: 5 CFs, 15558 µs left
    CHECK((c.pending == std::vector<uint32_t>{705, 706, 707, 708, 709}));
    CHECK_EQ(c.accumulator_us, int64_t(15558));
    CHECK_EQ(c.simulated_us, int64_t(80000));
    c.Advance(100);
    CHECK(c.pending.empty());
    c.Advance(-5000); // negative: nothing
    CHECK_EQ(c.accumulator_us, int64_t(15658));
    c.Advance(10'000'000); // clamped to 250 ms
    CHECK_EQ(c.pending.size(), size_t((15658 + 250000) / 16000));
    size_t n = c.pending.size();
    c.Resync(412000); // pending CFs renumbered after the new CF, accumulator kept
    CHECK_EQ(c.pending.size(), n);
    CHECK_EQ(c.pending.front(), 412001u);
    CHECK_EQ(c.command_frame, uint32_t(412000 + n));
}

TEST(command_frame_system_runs_listeners_per_frame) {
    ComponentRegistry reg;
    ComponentType t;
    t.id = kCommandFrameClockComponent;
    t.create = [] { return std::make_unique<CommandFrameClockComponent>(); };
    reg.Register(t);
    EntityAdmin ea(reg);
    ea.AppendSystem(std::make_unique<GameClockSystem>());
    ea.AppendSystem(std::make_unique<CommandFrameSystem>());
    std::vector<std::string> log;
    LogListener a("A", &log), b("B", &log);
    ea.AddCommandFrameListener(&a);
    ea.AddCommandFrameListener(&b);
    ea.Initialize();
    BasicLoader singleton({kCommandFrameClockComponent});
    ea.Spawn(singleton);
    ea.FlushCreated();
    ea.clock().Seed(100, 0);

    ea.Tick(0.040f); // 40 ms: CFs 101, 102 (8 ms left)
    std::vector<std::string> want = {"A pre", "B pre",
                                     "A cf 101 f1", "B cf 101 f1", "A cf 102 f2", "B cf 102 f2",
                                     "A post", "B post"};
    CHECK(log == want);
    auto* state = static_cast<CommandFrameClockComponent*>(ea.ComponentsOfType(kCommandFrameClockComponent).front());
    CHECK_EQ(state->command_frame, 102u);
    CHECK_EQ(state->time_ms, int64_t(102 * 16));
    CHECK_EQ(state->phase, 2);

    log.clear();
    ea.Tick(0.004f); // 12 ms accumulated: no CF, but pre/post still run
    CHECK((log == std::vector<std::string>{"A pre", "B pre", "A post", "B post"}));

    log.clear();
    ea.Tick(0.004f); // 16 ms: exactly one CF, both first and last
    CHECK((log == std::vector<std::string>{"A pre", "B pre", "A cf 103 f3", "B cf 103 f3", "A post", "B post"}));
}

TEST(deferred_queue_pruned_per_command_frame) {
    ComponentRegistry reg;
    EntityAdmin ea(reg);
    auto& q = ea.deferred_command_frame_queue();
    q.push_back({1, 0, 50});
    q.push_back({2, 0, 0});  // frame 0: kept
    q.push_back({3, 0, 60});
    q.push_back({4, 0, 49});
    ea.RunCommandFrame(55, 3);
    CHECK_EQ(q.size(), size_t(2));
    CHECK(q[0].a == 2 || q[1].a == 2);
    CHECK(q[0].a == 3 || q[1].a == 3);
}
