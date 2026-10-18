// Component 0x23 (STUStatescriptComponent) on systems/ecs entities: owns the entity's sync manager.
#pragma once
#include <memory>

#include "ecs/component.h"
#include "statescript/runtime.h"

namespace ss {

inline constexpr ecs::ComponentId kStatescriptComponentId = 0x23;

class StatescriptComponent : public ecs::Component {
public:
    StatescriptComponent(const EvaluatorRegistry& evaluators, Role role) : evals_(&evaluators), role_(role) {}
    // The sync manager is created in OnCreate, when the entity id is known.
    void OnCreate(const ecs::EntityLoader&) override;
    SyncManager* sync_manager() const { return sm_.get(); }

private:
    const EvaluatorRegistry* evals_;
    Role role_;
    std::unique_ptr<SyncManager> sm_;
};

// Replaces the registry's type 0x23 (keeping the client's dependencies) with StatescriptComponent.
// `evaluators` must outlive the registry's entities.
void RegisterStatescriptComponent(ecs::ComponentRegistry& registry, const EvaluatorRegistry& evaluators,
                                  Role role = Role::Server);

// The sync manager of an entity's component 0x23, or nullptr.
SyncManager* SyncManagerOf(const ecs::Entity& e);

}  // namespace ss
