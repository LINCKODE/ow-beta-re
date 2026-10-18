// A Statescript graph asset (STUStatescriptGraph, GUID type 0x058) as the runtime sees it: nodes by
// m_nodes index, states by m_states index, entries, plugs with their field offsets and links, config-var
// references. Immutable once loaded; shared by every instance. docs/StatescriptRuntime.md §1.2–1.4.
#pragma once
#include <cstdint>
#include <memory>
#include <span>
#include <string>
#include <string_view>
#include <vector>

#include "stu/reader.h"

namespace ss {

// CRC32 of the lower-cased name: how STU type and field hashes are formed (docs/STU.md).
constexpr uint32_t Hash(std::string_view name) {
    uint32_t crc = 0xFFFFFFFFu;
    for (char ch : name) {
        uint8_t c = static_cast<uint8_t>(ch >= 'A' && ch <= 'Z' ? ch - 'A' + 'a' : ch);
        crc ^= c;
        for (int k = 0; k < 8; k++) crc = (crc >> 1) ^ (0xEDB88320u & (0u - (crc & 1u)));
    }
    return ~crc;
}

// STU type hashes the runtime dispatches on (catalogue: docs/StatescriptNodeCatalogue.md).
namespace stype {
inline constexpr uint32_t Graph = 0x0df7e24f;
inline constexpr uint32_t Base = 0xa97578e7;              // STUStatescriptBase
inline constexpr uint32_t Entry = 0x2f937ae3;             // STUStatescriptEntry
inline constexpr uint32_t EntryGameMessage = 0xbe49a4c2;  // STUStatescriptEntryGameMessage
inline constexpr uint32_t Condition = 0x6f10080f;
inline constexpr uint32_t ActionTerminator = 0x15b44727;
inline constexpr uint32_t Action = 0x2ecefc8b;
inline constexpr uint32_t State = 0xa7213568;
inline constexpr uint32_t GraphPlug = 0x84201dca;
inline constexpr uint32_t OutputPlug = 0x3e490486;
inline constexpr uint32_t InputPlug = 0x11d7f052;
inline constexpr uint32_t InputPlugAbort = 0xe51f3e8d;
inline constexpr uint32_t SubgraphPlug = 0x24ab1f0b;  // + i32 list of m_states indices (field 0xeaece4c0)
inline constexpr uint32_t CasePlug = 0x136270d8;      // subgraph plug + m_value config var
inline constexpr uint32_t ConfigVar = 0xbfd9aadd;     // STUConfigVar (all config vars derive from it)
}  // namespace stype

enum class NodeKind : uint8_t { Entry, Condition, Action, Terminator, State, Other };
const char* NodeKindName(NodeKind k);

struct Link {
    int target = -1;           // m_nodes index of the input plug's parent node
    int input_instance = -1;   // STU instance of the input plug
    uint32_t input_type = 0;   // its type (stype::InputPlugAbort = the state's Abort plug)
    bool abort() const { return input_type == stype::InputPlugAbort; }
};

// An output plug field of a node (single, or one element of a plug list such as m_transitionPlug).
struct OutPlug {
    uint32_t offset = 0;  // byte offset of the field in the STU node (what ss_trace records as `plug`)
    int index = -1;       // element index for plug lists, -1 for single plug fields
    uint32_t field = 0;   // field hash
    int instance = -1;    // STU instance of the plug
    uint32_t type = 0;
    std::vector<Link> links;           // m_links, in array order
    std::vector<int32_t> subgraph;     // SubgraphPlug: m_states indices of the states it contains
    int value = -1;                    // CasePlug: config var instance
};

struct Node {
    int index = -1;      // m_nodes index (node +0x68)
    int instance = -1;   // STU instance
    uint32_t type = 0;
    NodeKind kind = NodeKind::Other;
    int remote_index = -1;  // m_remoteSyncNodes index (+0x6C)
    int state_index = -1;   // m_states index (+0xF8), states only
    uint32_t unique_id = 0; // m_uniqueID
    uint32_t ea_scope = 0;  // +0x70
    bool client_only = false, server_only = false;
    uint32_t world_flags = 0;  // states: +0xFC (0x36391723), world-change bits this state depends on
    std::vector<OutPlug> plugs;
    const stu::Value* fields = nullptr;  // the decoded node (Inline, all fields)

    const OutPlug* Plug(uint32_t offset, int index = -1) const;
    std::vector<const OutPlug*> PlugList(uint32_t offset) const;  // elements of a plug list, in order
    const stu::Value* Field(uint32_t field_hash) const { return fields ? fields->Find(field_hash) : nullptr; }
};

struct SchemaEntry {
    uint64_t var = 0;  // 0x0D8 identifier
    int value = -1;    // config var instance
};

struct SyncVar {
    uint64_t var = 0;
    uint32_t mode = 0;  // enum at +0x10
    uint8_t flag = 0;   // u8 at +0x14
};

class Graph {
public:
    // Loads a graph from a STUD image (Mode::File = CASC bytes, Mode::Loaded = image dumped from the
    // client at `base`). Returns nullptr and sets `error` if the image is not a STUStatescriptGraph.
    static std::unique_ptr<Graph> Load(std::vector<uint8_t> bytes, uint64_t guid, std::string& error,
                                       stu::Mode mode = stu::Mode::File, uint64_t base = 0,
                                       const stu::Registry& reg = stu::Registry::Client());

    uint64_t guid() const { return guid_; }
    const stu::Registry& registry() const { return *reg_; }
    std::span<const Node> nodes() const { return nodes_; }
    const Node& node(int i) const { return nodes_[i]; }
    std::span<const int> states() const { return states_; }    // m_states: node index per state index
    std::span<const int> entries() const { return entries_; }  // m_entries: node indices
    std::span<const int> remote_nodes() const { return remote_nodes_; }
    std::span<const SyncVar> sync_vars() const { return sync_vars_; }
    std::span<const SchemaEntry> schema() const { return schema_; }
    std::span<const int> world_states() const { return world_states_; }  // 0x229d2b04: state indices
    // Logical-button routing (graph +0xE0 / +0xF0, graph data) [G]: the LogicalButton condition nodes
    // (e03b7f7b, m_nodes indices) and the LogicalButton + Ability states (d9b17c50, m_states indices).
    std::span<const int> button_conditions() const { return button_conditions_; }
    std::span<const int> button_states() const { return button_states_; }
    uint32_t world_flags() const { return world_flags_; }                // graph 0x36391723
    uint32_t prediction_behavior() const { return prediction_behavior_; }
    // Index widths of the sync packets (graph +0x178…+0x184, compiled into the asset): nodes, states, remote
    // sync nodes, sync vars.
    std::span<const int32_t, 4> bit_counts() const { return bit_counts_; }
    const Node& state_node(int state_index) const { return nodes_[states_[state_index]]; }

    // STU instances (config vars, plugs, …).
    int instance_count() const { return static_cast<int>(values_.size()); }
    const stu::Value& value(int instance) const { return values_[instance]; }
    uint32_t type_of(int instance) const;
    bool IsA(int instance, uint32_t ancestor) const;

    // Field helpers on a decoded object.
    static int ObjectOf(const stu::Value* v) { return v && v->type == stu::Value::Type::Object ? v->instance : -1; }
    int Object(const stu::Value& obj, uint32_t field) const { return ObjectOf(obj.Find(field)); }
    std::vector<int> Objects(const stu::Value& obj, uint32_t field) const;
    // Dynamic var path (STUConfigVarDynamic 0x4f6799b5: identifier list).
    std::vector<uint64_t> DynamicPath(int config_var) const;
    // Identifier / resource GUID held by a Ref field (0 if absent).
    static uint64_t RefGuid(const stu::Value* v) { return v && v->type == stu::Value::Type::Ref ? v->guid : 0; }

private:
    Graph() = default;
    bool Build(std::string& error);

    uint64_t guid_ = 0;
    const stu::Registry* reg_ = nullptr;
    std::vector<uint8_t> bytes_;
    stu::Document doc_;
    std::vector<stu::Value> values_;  // every instance, decoded
    std::vector<Node> nodes_;
    std::vector<int> states_, entries_, remote_nodes_, world_states_, button_conditions_, button_states_;
    std::vector<SyncVar> sync_vars_;
    std::vector<SchemaEntry> schema_;
    uint32_t world_flags_ = 0, prediction_behavior_ = 0;
    int32_t bit_counts_[4] = {-1, -1, -1, -1};
};

}  // namespace ss
