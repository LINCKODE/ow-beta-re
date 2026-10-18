#pragma once
// The live client's component types and per-EA system lists (generated/manifest_data.h), as
// stand-ins: components carry no data, systems only have the client's subscriptions. Used to
// replay client traces and as the skeleton real implementations replace piece by piece.
#include "ecs/entity_admin.h"
#include <functional>
#include <string_view>

namespace ecs::manifest {

struct SystemInfo;

class DataComponent : public Component {
public:
    // The client's CanDelete depends on real component state (e.g. pending resource unloads);
    // stand-ins take the answer from outside (trace replay).
    bool allow_delete = true;
    // One-shot work done when asked CanDelete (client components delete entities they own here).
    std::function<void()> on_can_delete;
    bool CanDelete() override {
        if (auto fn = std::move(on_can_delete)) {
            on_can_delete = nullptr;
            fn();
        }
        return allow_delete;
    }
};

// Stand-in command-frame listener (the client's listeners are subobjects of their systems).
class ManifestListener : public CommandFrameListener {
public:
    void OnCommandFrame(uint32_t cf, uint32_t flags) override {
        if (on_command_frame) on_command_frame(cf, flags);
    }
    void OnPreCommandFrames() override {
        if (on_pre) on_pre();
    }
    void OnPostCommandFrames() override {
        if (on_post) on_post();
    }
    std::function<void(uint32_t cf, uint32_t flags)> on_command_frame;
    std::function<void()> on_pre, on_post;
};

class ManifestSystem : public System {
public:
    ManifestSystem(int index, const SystemInfo& info);
    std::span<const ComponentId> Subscriptions() const override { return subs_; }
    void OnTick(float dt) override {
        if (on_tick) on_tick(*this, dt);
    }

    int index() const { return index_; }
    uint32_t vtable_rva() const { return vtable_rva_; }
    // Test hook: work this system does in its OnTick (e.g. spawn, as the client's system 8 does).
    std::function<void(ManifestSystem&, float)> on_tick;
    // Set when the client system registers a command-frame listener.
    std::unique_ptr<ManifestListener> listener;

private:
    int index_;
    uint32_t vtable_rva_;
    std::span<const ComponentId> subs_;
};

// Every component type the client registers, with its dependencies and storage flag.
void RegisterComponents(ComponentRegistry& registry);

// Appends the systems of the named client EA ("game", "session", "lobby") in the client's tick
// order: the library's real implementations where they exist (GameClockSystem,
// CommandFrameSystem), ManifestSystem stand-ins otherwise; then registers the command-frame
// listeners in the client's order. Returns false for an unknown name.
bool AddSystems(EntityAdmin& ea, std::string_view admin_name);

} // namespace ecs::manifest
