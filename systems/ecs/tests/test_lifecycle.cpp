// Behaviour tests: each pins down one client behaviour from docs/ECS.md.
#include "ecs/entity_admin.h"
#include "ecs/manifest.h"
#include "manifest_data.h"
#include "test.h"
#include <string>

using namespace ecs;

namespace {

std::vector<std::string>* g_log = nullptr;
void log(std::string s) {
    if (g_log) g_log->push_back(std::move(s));
}

struct LoggingComponent : Component {
    bool allow_delete = true;
    void OnCreate(const EntityLoader&) override { log("create " + std::to_string(id()) + "@" + std::to_string(entity()->id())); }
    void OnPostCreate() override { log("post " + std::to_string(id())); }
    bool CanDelete() override { return allow_delete; }
};

struct Recorder : System {
    std::string name;
    std::vector<ComponentId> subs;
    bool init_ok = true;
    std::function<void(Recorder&)> tick;
    Recorder(std::string n, std::vector<ComponentId> s) : name(std::move(n)), subs(std::move(s)) {}
    std::span<const ComponentId> Subscriptions() const override { return subs; }
    bool OnInitialize() override { return init_ok; }
    void OnEntityCreated(Entity& e) override { log(name + " created " + std::to_string(e.id())); }
    void OnEntityAdded(Entity& e) override { log(name + " added " + std::to_string(e.id())); }
    void OnEntityDeleting(Entity& e) override { log(name + " deleting " + std::to_string(e.id())); }
    void OnEntityRemoved(Entity& e) override { log(name + " removed " + std::to_string(e.id())); }
    void OnTick(float) override {
        if (tick) tick(*this);
    }
};
struct OtherSystem : Recorder {
    using Recorder::Recorder;
};

ComponentRegistry MakeRegistry() {
    ComponentRegistry r;
    for (ComponentId id : {1, 2, 3, 5, 9, 70, 130}) {
        ComponentType t;
        t.id = id;
        t.create = [] { return std::make_unique<LoggingComponent>(); };
        if (id == 9) t.dependencies = {3, 1};
        if (id == 3) t.dependencies = {2};
        if (id == 5) t.inline_size = 64;
        r.Register(std::move(t));
    }
    return r;
}

std::vector<ComponentId> ids(const Entity& e) {
    std::vector<ComponentId> v;
    for (auto* c : e.components()) v.push_back(c->id());
    return v;
}

} // namespace

TEST(components_sorted_with_dependencies_and_rank_lookup) {
    auto reg = MakeRegistry();
    EntityAdmin ea(reg);
    ea.Initialize();
    VectorTraceSink sink;
    ea.SetTraceSink(&sink);
    BasicLoader l({130, 9, 70});
    Entity* e = ea.Spawn(l);
    CHECK(e);
    CHECK((ids(*e) == std::vector<ComponentId>{1, 2, 3, 9, 70, 130}));
    // Add order: loader components first, then dependencies depth-first (9 -> 3 -> 2, then 1).
    std::vector<uint64_t> added;
    for (auto& ev : sink.events)
        if (ev.kind == TraceKind::AddComponent) added.push_back(ev.a);
    CHECK((added == std::vector<uint64_t>{130, 9, 70, 3, 2, 1}));
    for (ComponentId id : {1, 2, 3, 9, 70, 130}) {
        CHECK(e->Get(id));
        CHECK_EQ(int(e->Get(id)->id()), int(id));
    }
    CHECK(!e->Get(5));
    CHECK(!e->Get(4));
}

TEST(entity_id_allocation) {
    auto reg = MakeRegistry();
    EntityAdmin ea(reg);
    ea.Initialize();
    BasicLoader l({1}, 0x80000000);
    CHECK_EQ(ea.Spawn(l)->id(), 0x80000001u);
    CHECK_EQ(ea.Spawn(l, 0x1fffffff)->id(), 0x80000002u); // low 29 bits all set = allocate
    CHECK_EQ(ea.Spawn(l, 0x40000001)->id(), 0x40000001u);
    CHECK(ea.Spawn(l, 0x40000001) == nullptr);             // explicit id in use: spawn fails
    CHECK_EQ(ea.entity_count(), size_t(3));
}

TEST(callbacks_per_matching_component_in_id_then_registration_order) {
    auto reg = MakeRegistry();
    EntityAdmin ea(reg);
    auto* a = static_cast<Recorder*>(ea.AppendSystem(std::make_unique<Recorder>("A", std::vector<ComponentId>{70, 1})));
    ea.AppendSystem(std::make_unique<Recorder>("B", std::vector<ComponentId>{1}));
    ea.AppendSystem(std::make_unique<Recorder>("All", std::vector<ComponentId>{kSubscribeAll}));
    (void)a;
    CHECK(ea.Initialize());
    std::vector<std::string> out;
    g_log = &out;
    BasicLoader l({70, 1});
    ea.Spawn(l);
    g_log = nullptr;
    std::vector<std::string> want = {
        "create 1@1", "create 70@1", "post 1", "post 70",
        "All created 1", "A created 1", "B created 1", "A created 1", // A twice: subscribed to 1 and 70
    };
    CHECK(out == want);
}

TEST(spawn_in_tick_is_created_now_added_at_next_flush) {
    auto reg = MakeRegistry();
    EntityAdmin ea(reg);
    auto* spawner = static_cast<Recorder*>(ea.AppendSystem(std::make_unique<Recorder>("S", std::vector<ComponentId>{2})));
    ea.Initialize();
    BasicLoader l({2});
    bool once = true;
    spawner->tick = [&](Recorder&) {
        if (once) ea.Spawn(l);
        once = false;
    };
    std::vector<std::string> out;
    g_log = &out;
    ea.Tick(0.016f);
    CHECK((out == std::vector<std::string>{"create 2@1", "post 2", "S created 1"}));
    CHECK(ea.ComponentsOfType(2).empty());
    CHECK(ea.Find(1)->flags() & kFlagPendingCreated);
    out.clear();
    ea.Tick(0.016f);
    g_log = nullptr;
    CHECK((out == std::vector<std::string>{"S added 1"}));
    CHECK_EQ(ea.ComponentsOfType(2).size(), size_t(1));
    CHECK(!(ea.Find(1)->flags() & kFlagPendingCreated));
}

TEST(untracked_types_never_enter_iteration_lists) {
    auto reg = MakeRegistry();
    EntityAdmin ea(reg);
    ea.Initialize();
    BasicLoader l({5, 1});
    ea.Spawn(l);
    ea.FlushCreated();
    CHECK(ea.ComponentsOfType(5).empty());
    CHECK_EQ(ea.ComponentsOfType(1).size(), size_t(1));
}

TEST(inline_entities_skip_dependencies_callbacks_and_queues) {
    auto reg = MakeRegistry();
    EntityAdmin ea(reg);
    ea.AppendSystem(std::make_unique<Recorder>("S", std::vector<ComponentId>{kSubscribeAll}));
    ea.Initialize();
    ea.set_id_counter(0x10);
    std::vector<std::string> out;
    g_log = &out;
    BasicLoader l;
    std::vector<ComponentId> comps = {9, 5}; // given order kept, 9's dependencies not added
    Entity* e = ea.SpawnInline(comps, l);
    CHECK_EQ(e->id(), 0x80000011u);
    CHECK((ids(*e) == std::vector<ComponentId>{9, 5}));
    CHECK(e->flags() == (kFlagExternalStorage | kFlagCreated));
    ea.Tick(0.016f);
    CHECK((out == std::vector<std::string>{"create 9@2147483665", "create 5@2147483665", "post 9", "post 5"}));
    CHECK(ea.ComponentsOfType(9).empty());
    out.clear();
    ea.RequestDelete(*e);
    ea.FlushDestroy();
    g_log = nullptr;
    CHECK((out == std::vector<std::string>{"S deleting 2147483665"})); // no on_removed (0x80)
    CHECK(!ea.Find(e->id()));
}

TEST(delete_is_two_phase_and_gated_by_can_delete) {
    auto reg = MakeRegistry();
    EntityAdmin ea(reg);
    ea.AppendSystem(std::make_unique<Recorder>("S", std::vector<ComponentId>{1}));
    ea.Initialize();
    BasicLoader l({1, 2});
    Entity* e = ea.Spawn(l);
    ea.Tick(0.016f);
    std::vector<std::string> out;
    g_log = &out;
    auto* blocker = e->Get<LoggingComponent>(2);
    blocker->allow_delete = false;
    ea.RequestDelete(*e);
    CHECK((out == std::vector<std::string>{"S deleting 1"}));
    CHECK_EQ(e->flags() & (kFlagDeleteRequested | kFlagPendingDestroy), uint32_t(kFlagDeleteRequested));
    ea.Tick(0.016f);
    CHECK(ea.Find(1)); // still alive: a component refused
    // A second request runs the deleting callbacks again (only 0x10/0x20 make it a no-op).
    blocker->allow_delete = true;
    out.clear();
    ea.RequestDelete(*e);
    CHECK((out == std::vector<std::string>{"S deleting 1"}));
    CHECK(e->flags() & kFlagPendingDestroy);
    out.clear();
    ea.Tick(0.016f);
    g_log = nullptr;
    CHECK((out == std::vector<std::string>{"S removed 1"}));
    CHECK(!ea.Find(1));
    CHECK(ea.ComponentsOfType(1).empty());
}

TEST(children_spawn_first_are_prepended_and_deleted_with_parent) {
    auto reg = MakeRegistry();
    EntityAdmin ea(reg);
    ea.Initialize();
    BasicLoader c1({2}), c2({3});
    c2.child_tag = 7;
    BasicLoader parent({1});
    parent.children = {&c1, &c2};
    std::vector<std::string> out;
    g_log = &out;
    Entity* p = ea.Spawn(parent);
    g_log = nullptr;
    // Ids: parent allocated first (1), then children 2, 3; children finalize before the parent.
    CHECK_EQ(p->id(), 1u);
    CHECK(out.front() == "create 2@2");
    CHECK(out.back() == "post 1");
    CHECK_EQ(p->first_child()->id(), 3u); // prepended
    CHECK_EQ(p->first_child()->next_sibling()->id(), 2u);
    CHECK(p->flags() & kFlagHasTaggedChild);
    CHECK_EQ(p->first_child()->child_tag(), 7u);
    ea.RequestDelete(*p);
    ea.FlushDestroy();
    CHECK_EQ(ea.entity_count(), size_t(0));
}

TEST(insert_before_missing_type_drops_system) {
    auto reg = MakeRegistry();
    EntityAdmin ea(reg);
    ea.AppendSystem(std::make_unique<Recorder>("A", std::vector<ComponentId>{}));
    CHECK(ea.InsertSystemBefore<OtherSystem>(std::make_unique<Recorder>("X", std::vector<ComponentId>{})) == nullptr);
    ea.AppendSystem(std::make_unique<OtherSystem>("O", std::vector<ComponentId>{}));
    auto* b = ea.InsertSystemBefore<OtherSystem>(std::make_unique<Recorder>("B", std::vector<ComponentId>{}));
    CHECK(b);
    CHECK_EQ(ea.SystemIndex(b), 1);
    CHECK_EQ(ea.systems().size(), size_t(3));
}

TEST(initialize_fails_on_first_false_without_subscriptions) {
    auto reg = MakeRegistry();
    EntityAdmin ea(reg);
    auto* s = static_cast<Recorder*>(ea.AppendSystem(std::make_unique<Recorder>("S", std::vector<ComponentId>{1})));
    s->init_ok = false;
    CHECK(!ea.Initialize());
    CHECK(!ea.initialized());
    std::vector<std::string> out;
    g_log = &out;
    BasicLoader l({1});
    ea.Spawn(l);
    g_log = nullptr;
    CHECK((out == std::vector<std::string>{"create 1@1", "post 1"})); // no subscriber lists built
}

TEST(manifest_matches_client_dump) {
    ComponentRegistry reg;
    manifest::RegisterComponents(reg);
    EntityAdmin ea(reg);
    CHECK(manifest::AddSystems(ea, "game"));
    CHECK_EQ(ea.systems().size(), size_t(51));
    auto* s9 = static_cast<manifest::ManifestSystem*>(ea.systems()[9].get());
    CHECK_EQ(s9->vtable_rva(), 0x15c2528u);
    CHECK_EQ(s9->Subscriptions().size(), size_t(3));
    CHECK(reg.Get(0x15));
    CHECK(!reg.Get(0x01)->tracked());
    CHECK_EQ(reg.Get(0x03)->inline_size, 1616u);
    CHECK(!manifest::AddSystems(ea, "nope"));
}
