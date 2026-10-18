// Variable storage: var bags, vars, writer ("user") and subscriptions. docs/StatescriptRuntime.md §1.7, §3.8.
#pragma once
#include <cstdint>
#include <functional>
#include <map>
#include <memory>
#include <span>
#include <vector>

#include "statescript/value.h"

namespace ss {

// Special path identifiers (0x0D8 GUIDs), §1.7.
namespace varid {
inline constexpr uint64_t InstanceBag = 0x0D80000000000003;
inline constexpr uint64_t EntityBag = 0x0D80000000000004;
inline constexpr uint64_t ChildBag = 0x0D80000000000005;
inline constexpr uint64_t InstanceRef = 0x0D80000000000006;
inline constexpr uint64_t X = 0x0D80000000000008, Y = 0x0D80000000000009, Z = 0x0D8000000000000A;
inline constexpr uint64_t DictSize = 0x0D8000000000000B;
inline constexpr uint64_t Owner = 0x0D8000000000001B;  // instance bag: owner entity (ENTITY)
inline constexpr uint64_t Make(uint64_t low) { return 0x0D80000000000000ull | (low & 0xFFFFFFFFFFFFull); }
}  // namespace varid

// Who wrote a var (var +0x88, §1.7): instance id, m_states index of the writing state, write priority
// (bits 0-6) and "state-defined value" (bit 7). A null user has priority 1.
struct VarUser {
    int16_t instance = 0;
    int16_t state = 0;
    uint8_t priority = 1;
    bool state_defined = false;
    // Bits 40+ of the client's user word: which of the writing state's values the var is (the index passed to
    // its +0x228; the sync's state reference carries it as "small extra").
    uint8_t extra = 0;
    bool operator==(const VarUser&) const = default;
};

class Var;

// A state subscribed to a var: list 0 (node +0xA8 paths) gets OnVarChanged (state vfunc +0x170),
// list 1 (node +0xB8) gets OnVarDependencyChanged (+0x178) [G] (0xf3de20, 0xf3e980).
class VarSubscriber {
public:
    virtual ~VarSubscriber() = default;
    virtual void OnVarChanged(Var& var) = 0;
    virtual void OnVarDependencyChanged(Var& var) = 0;
};

class VarBag;

// The stack registry a var with a stack slot belongs to (SyncManager, StatescriptRuntime.md §5.9): a write to
// the stored value re-folds the stack and notifies the subscribers.
class VarStackHost {
public:
    virtual ~VarStackHost() = default;
    virtual void OnStoredChanged(Var& var) = 0;
};

class Var {
public:
    enum class Kind : uint8_t { Primitive, InstanceReference, Dictionary, Bag };

    uint64_t id() const { return id_; }
    Kind kind() const { return kind_; }
    // What readers see (0xf5e2f0): the stack fold when the var has a stack slot, else the stored value.
    const Primitive& value() const { return stack_ ? stack_value_ : value_; }
    // The stored primitive (var +0x110): what the sync serializers send, and the base of a stack fold.
    const Primitive& stored() const { return value_; }
    // Stack slot (var +0x90, set / clear +0x188 / +0x198): reads go to `folded` until cleared.
    uint32_t stack_key() const { return stack_key_; }
    void SetStackSlot(VarStackHost* host, uint32_t key) { stack_ = host, stack_key_ = key; }
    void ClearStackSlot() { stack_ = nullptr, stack_key_ = 0; }
    void SetFolded(const Primitive& v) { stack_value_ = v; }
    // Notify list-0 then list-1 subscribers (as after a write).
    void Notify();
    const VarUser& user() const { return user_; }
    size_t dict_size() const { return dict_.size(); }
    // Entries {key, value} of a Dictionary var.
    const std::vector<std::pair<Primitive, Primitive>>& dict() const { return dict_; }
    VarBag* child() const { return child_.get(); }
    bool dirty() const { return dirty_; }
    void clear_dirty() { dirty_ = false; }

private:
    friend class VarBag;
    uint64_t id_ = 0;
    Kind kind_ = Kind::Primitive;
    Primitive value_;  // INT 0 when created
    VarUser user_;
    bool dirty_ = true;
    std::vector<std::pair<Primitive, Primitive>> dict_;
    std::unique_ptr<VarBag> child_;
    std::vector<VarSubscriber*> subs_[2];
    VarStackHost* stack_ = nullptr;
    uint32_t stack_key_ = 0;
    Primitive stack_value_;
};

enum class SetResult : uint8_t { Unchanged, Changed, Rejected };

class VarBag {
public:
    explicit VarBag(uint64_t id = 0) : id_(id) {}
    uint64_t id() const { return id_; }

    Var* Find(uint64_t id) const;
    // Creates a Primitive var (INT 0) if missing (subscribe path, 0xf43bd0).
    Var& GetOrCreate(uint64_t id);
    // Child bag behind path id 0x…05 (created on write / subscribe).
    VarBag& Child(uint64_t id);
    VarBag* FindChild(uint64_t id) const;

    // Write (0xf3f270 / 0xf3f650) [G]: lower priority than the current writer is rejected (0xf5eb00);
    // same writer + identical value changes nothing; otherwise store, mark dirty and notify list 0 then
    // list 1 subscribers, each in subscription order. Replaces a non-Primitive var (subscribers kept).
    SetResult Set(uint64_t id, const Primitive& value, const VarUser& user = {});
    // Raw store without notifications or priority checks (setup / fixtures).
    void Store(uint64_t id, const Primitive& value);
    void SetDictionarySize(uint64_t id, size_t n);  // tests / seeding only
    // A Dictionary var with these {key, value} entries (seeding / start-up import; no notification).
    void SetDictionary(uint64_t id, std::vector<std::pair<Primitive, Primitive>> entries);
    // ArrayClear (bag vfunc +0x140): the var becomes an empty Dictionary (created if missing; a var of another
    // class is replaced, subscribers kept). Notifies like a write when something changed [?].
    SetResult ClearDictionary(uint64_t id, const VarUser& user = {});
    void Remove(uint64_t id);
    // A dictionary write (bag +0x170 → var +0x170, 0xf633b0 / 0xf61530) [G]: the var becomes a Dictionary
    // (created, or a var of another class replaced with its subscribers kept: 0xf62cb0); a lower write priority
    // than the current writer's is rejected (null). The caller edits the entries, then CommitDictionary.
    // `existing_only`: no creation (the keyed remove, bag +0x138 = 0xf62f10, needs an existing Dictionary).
    std::vector<std::pair<Primitive, Primitive>>* BeginDictionaryWrite(uint64_t id, const VarUser& user, Var** var,
                                                                        bool existing_only = false);
    // End of a dictionary write (var +0x178, 0xf61660): dirty, then list-0 and list-1 subscribers are notified
    // (always, changed or not).
    void CommitDictionary(Var& v);

    void Subscribe(uint64_t id, VarSubscriber* s, int list);  // creates the var
    void Unsubscribe(uint64_t id, VarSubscriber* s, int list);

    // Vars ordered by id (the client hashes into 16 buckets; order is not observable).
    const std::map<uint64_t, std::unique_ptr<Var>>& vars() const { return vars_; }
    // Visits every Primitive/InstanceReference/Dictionary var, recursing into child bags (key = path).
    void ForEach(const std::function<void(std::span<const uint64_t> path, const Var&)>& fn) const;

private:
    void ForEachImpl(std::vector<uint64_t>& path,
                     const std::function<void(std::span<const uint64_t>, const Var&)>& fn) const;
    uint64_t id_;
    std::map<uint64_t, std::unique_ptr<Var>> vars_;
};

}  // namespace ss
