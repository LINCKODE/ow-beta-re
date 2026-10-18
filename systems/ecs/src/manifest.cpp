#include "ecs/manifest.h"
#include "ecs/command_frame_systems.h"
#include "manifest_data.h"

namespace ecs::manifest {

ManifestSystem::ManifestSystem(int index, const SystemInfo& info)
    : index_(index), vtable_rva_(info.vtable_rva), subs_(info.subs, info.sub_count) {}

void RegisterComponents(ComponentRegistry& registry) {
    for (const auto& c : kComponents) {
        if (!c.has_creator) continue;
        ComponentType t;
        t.id = c.id;
        t.create = [] { return std::make_unique<DataComponent>(); };
        t.dependencies.assign(c.deps, c.deps + c.dep_count);
        t.inline_size = c.inline_size;
        t.inline_entity_allocator = c.inline_entity_allocator;
        registry.Register(std::move(t));
    }
}

bool AddSystems(EntityAdmin& ea, std::string_view admin_name) {
    for (const auto& a : kAdmins) {
        if (admin_name != a.name) continue;
        std::vector<ManifestSystem*> stand_ins;
        for (int i = 0; i < a.system_count; i++) {
            const SystemInfo& s = a.systems[i];
            if (s.vtable_rva == 0x15c4be8) {
                ea.AppendSystem(std::make_unique<GameClockSystem>());
            } else if (s.vtable_rva == 0x15c1b48) {
                ea.AppendSystem(std::make_unique<CommandFrameSystem>());
            } else {
                auto* m = static_cast<ManifestSystem*>(ea.AppendSystem(std::make_unique<ManifestSystem>(i, s)));
                stand_ins.push_back(m);
            }
        }
        for (int k = 0; k < a.cf_listener_count; k++)
            for (auto* m : stand_ins)
                if (m->vtable_rva() == a.cf_listeners[k] && !m->listener) {
                    m->listener = std::make_unique<ManifestListener>();
                    ea.AddCommandFrameListener(m->listener.get());
                    break;
                }
        return true;
    }
    return false;
}

} // namespace ecs::manifest
