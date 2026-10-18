#include "statescript/ecs_component.h"

#include "ecs/entity.h"

namespace ss {

void StatescriptComponent::OnCreate(const ecs::EntityLoader&) {
    sm_ = std::make_unique<SyncManager>(entity()->id(), *evals_, role_);
}

void RegisterStatescriptComponent(ecs::ComponentRegistry& registry, const EvaluatorRegistry& evaluators, Role role) {
    ecs::ComponentType t;
    if (const ecs::ComponentType* old = registry.Get(kStatescriptComponentId)) t = *old;
    t.id = kStatescriptComponentId;
    t.create = [&evaluators, role] { return std::make_unique<StatescriptComponent>(evaluators, role); };
    registry.Register(std::move(t));
}

SyncManager* SyncManagerOf(const ecs::Entity& e) {
    auto* c = dynamic_cast<StatescriptComponent*>(e.Get(kStatescriptComponentId));
    return c ? c->sync_manager() : nullptr;
}

}  // namespace ss
