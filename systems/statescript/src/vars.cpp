#include "statescript/vars.h"

#include <algorithm>

namespace ss {

Var* VarBag::Find(uint64_t id) const {
    auto it = vars_.find(id);
    return it == vars_.end() ? nullptr : it->second.get();
}

Var& VarBag::GetOrCreate(uint64_t id) {
    auto& slot = vars_[id];
    if (!slot) {
        slot = std::make_unique<Var>();
        slot->id_ = id;
    }
    return *slot;
}

VarBag& VarBag::Child(uint64_t id) {
    Var& v = GetOrCreate(id);
    if (v.kind_ != Var::Kind::Bag || !v.child_) {
        v.kind_ = Var::Kind::Bag;
        v.child_ = std::make_unique<VarBag>(id);
    }
    return *v.child_;
}

VarBag* VarBag::FindChild(uint64_t id) const {
    Var* v = Find(id);
    return v && v->kind_ == Var::Kind::Bag ? v->child_.get() : nullptr;
}

SetResult VarBag::Set(uint64_t id, const Primitive& value, const VarUser& user) {
    Var* v = Find(id);
    Var::Kind want = value.type == PrimType::InstanceId ? Var::Kind::InstanceReference : Var::Kind::Primitive;
    if (v) {
        // [?] the writer itself may lower its priority (a state baking the var it owned, 0xf48190).
        bool same_writer = v->user_.state_defined && v->user_.instance == user.instance && v->user_.state == user.state;
        if (v->user_.priority > user.priority && !same_writer) return SetResult::Rejected;
        if (v->kind_ == want && v->user_ == user && Identical(v->value_, value)) return SetResult::Unchanged;
    } else {
        v = &GetOrCreate(id);
    }
    if (v->kind_ != want) {  // replaced by a var of the right class; subscribers carried over (0xf3e280)
        v->kind_ = want;
        v->child_.reset();
        v->dict_.clear();
    }
    v->value_ = value;
    v->user_ = user;
    v->dirty_ = true;
    if (v->stack_) v->stack_->OnStoredChanged(*v);  // re-fold, then notify
    else v->Notify();
    return SetResult::Changed;
}

void Var::Notify() {
    auto subs0 = subs_[0], subs1 = subs_[1];  // callbacks may (un)subscribe
    for (VarSubscriber* s : subs0) s->OnVarChanged(*this);
    for (VarSubscriber* s : subs1) s->OnVarDependencyChanged(*this);
}

void VarBag::Store(uint64_t id, const Primitive& value) {
    Var& v = GetOrCreate(id);
    v.kind_ = value.type == PrimType::InstanceId ? Var::Kind::InstanceReference : Var::Kind::Primitive;
    v.value_ = value;
    v.dirty_ = true;
}

void VarBag::SetDictionarySize(uint64_t id, size_t n) {
    Var& v = GetOrCreate(id);
    v.kind_ = Var::Kind::Dictionary;
    v.dict_.resize(n);
}

void VarBag::SetDictionary(uint64_t id, std::vector<std::pair<Primitive, Primitive>> entries) {
    Var& v = GetOrCreate(id);
    v.kind_ = Var::Kind::Dictionary;
    v.dict_ = std::move(entries);
    v.dirty_ = true;
}

SetResult VarBag::ClearDictionary(uint64_t id, const VarUser& user) {
    Var* v = Find(id);
    if (v && v->user_.priority > user.priority) return SetResult::Rejected;
    bool existed = v != nullptr;
    if (!v) v = &GetOrCreate(id);
    if (existed && v->kind_ == Var::Kind::Dictionary && v->dict_.empty()) return SetResult::Unchanged;
    v->kind_ = Var::Kind::Dictionary;
    v->child_.reset();
    v->dict_.clear();
    v->value_ = Primitive();
    v->user_ = user;
    v->dirty_ = true;
    if (existed) {
        auto subs0 = v->subs_[0], subs1 = v->subs_[1];
        for (VarSubscriber* s : subs0) s->OnVarChanged(*v);
        for (VarSubscriber* s : subs1) s->OnVarDependencyChanged(*v);
    }
    return SetResult::Changed;
}

void VarBag::Remove(uint64_t id) { vars_.erase(id); }

std::vector<std::pair<Primitive, Primitive>>* VarBag::BeginDictionaryWrite(uint64_t id, const VarUser& user, Var** var,
                                                                             bool existing_only) {
    Var* v = Find(id);
    if (existing_only && (!v || v->kind_ != Var::Kind::Dictionary)) return nullptr;
    if (v && v->user_.priority > user.priority) return nullptr;
    if (!v) v = &GetOrCreate(id);
    if (v->kind_ != Var::Kind::Dictionary) {
        v->kind_ = Var::Kind::Dictionary;
        v->child_.reset();
        v->dict_.clear();
        v->value_ = Primitive();
    }
    v->user_ = user;
    if (var) *var = v;
    return &v->dict_;
}

void VarBag::CommitDictionary(Var& v) {
    v.dirty_ = true;
    v.Notify();
}

void VarBag::Subscribe(uint64_t id, VarSubscriber* s, int list) {
    auto& subs = GetOrCreate(id).subs_[list];
    if (std::find(subs.begin(), subs.end(), s) == subs.end()) subs.push_back(s);
}

void VarBag::Unsubscribe(uint64_t id, VarSubscriber* s, int list) {
    if (Var* v = Find(id)) std::erase(v->subs_[list], s);
}

void VarBag::ForEach(const std::function<void(std::span<const uint64_t>, const Var&)>& fn) const {
    std::vector<uint64_t> path;
    ForEachImpl(path, fn);
}

void VarBag::ForEachImpl(std::vector<uint64_t>& path,
                         const std::function<void(std::span<const uint64_t>, const Var&)>& fn) const {
    for (auto& [id, v] : vars_) {
        path.push_back(id);
        if (v->kind_ == Var::Kind::Bag) {
            if (v->child_) v->child_->ForEachImpl(path, fn);
        } else {
            fn(path, *v);
        }
        path.pop_back();
    }
}

}  // namespace ss
