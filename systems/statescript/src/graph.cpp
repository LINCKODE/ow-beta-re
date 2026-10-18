#include "statescript/graph.h"

#include <unordered_map>

namespace ss {

namespace {
// STUStatescriptGraph fields.
constexpr uint32_t kNodes = Hash("m_nodes"), kStates = Hash("m_states"), kEntries = Hash("m_entries"),
                   kRemoteSyncNodes = Hash("m_remoteSyncNodes"), kSyncVars = Hash("m_syncVars"),
                   kPublicSchema = Hash("m_publicSchema"), kPrediction = Hash("m_predictionBehavior"),
                   kWorldStates = 0x229d2b04, kGraphWorldFlags = 0x36391723;
// STUStatescriptBase / State fields.
constexpr uint32_t kNodeIndex = 0x53bfb779, kRemoteIndex = 0x4b062861, kEaScope = 0xefd56e99,
                   kClientOnly = Hash("m_clientOnly"), kServerOnly = Hash("m_serverOnly"),
                   kUniqueId = Hash("m_uniqueID"), kStateIndex = 0x63d0b555, kStateWorldFlags = 0x36391723;
// Plugs, links, schema, sync vars.
constexpr uint32_t kLinks = Hash("m_links"), kParentNode = Hash("m_parentNode"), kInputPlug = Hash("m_inputPlug"),
                   kSubgraphStates = 0xeaece4c0, kValue = Hash("m_value"), kSchemaVar = 0x4f6799b5,
                   kSchemaEntries = Hash("m_entries"), kSyncVarId = 0x4f6799b5, kDynamicPath = 0x4f6799b5;

int32_t I32(const stu::Value* v) { return v ? v->AsI32() : 0; }
}  // namespace

const char* NodeKindName(NodeKind k) {
    switch (k) {
    case NodeKind::Entry: return "entry";
    case NodeKind::Condition: return "condition";
    case NodeKind::Action: return "action";
    case NodeKind::Terminator: return "terminator";
    case NodeKind::State: return "state";
    default: return "other";
    }
}

const OutPlug* Node::Plug(uint32_t offset, int index) const {
    for (auto& p : plugs)
        if (p.offset == offset && p.index == index) return &p;
    return nullptr;
}

std::vector<const OutPlug*> Node::PlugList(uint32_t offset) const {
    std::vector<const OutPlug*> out;
    for (auto& p : plugs)
        if (p.offset == offset && p.index >= 0) out.push_back(&p);
    return out;
}

uint32_t Graph::type_of(int instance) const {
    if (instance < 0 || instance >= instance_count()) return 0;
    auto* t = doc_.instances()[instance].type;
    return t ? t->hash : 0;
}

bool Graph::IsA(int instance, uint32_t ancestor) const { return reg_->IsA(type_of(instance), ancestor); }

std::vector<int> Graph::Objects(const stu::Value& obj, uint32_t field) const {
    std::vector<int> out;
    if (auto* v = obj.Find(field); v && v->type == stu::Value::Type::List)
        for (auto& it : v->items) out.push_back(ObjectOf(&it));
    return out;
}

std::vector<uint64_t> Graph::DynamicPath(int cv) const {
    std::vector<uint64_t> out;
    if (cv < 0) return out;
    if (auto* v = values_[cv].Find(kDynamicPath); v && v->type == stu::Value::Type::List)
        for (auto& it : v->items) out.push_back(RefGuid(&it));
    return out;
}

std::unique_ptr<Graph> Graph::Load(std::vector<uint8_t> bytes, uint64_t guid, std::string& error, stu::Mode mode,
                                   uint64_t base, const stu::Registry& reg) {
    std::unique_ptr<Graph> g(new Graph());
    g->guid_ = guid;
    g->reg_ = &reg;
    g->bytes_ = std::move(bytes);
    if (!stu::Document::Parse(g->bytes_, reg, g->doc_, error, mode, base)) return nullptr;
    if (!g->Build(error)) return nullptr;
    return g;
}

bool Graph::Build(std::string& error) {
    const auto& inst = doc_.instances();
    if (inst.empty() || !inst[0].type || inst[0].type->hash != stype::Graph) {
        error = "root instance is not a STUStatescriptGraph";
        return false;
    }
    values_.reserve(inst.size());
    for (int i = 0; i < static_cast<int>(inst.size()); i++) values_.push_back(doc_.Decode(i));
    const stu::Value& root = values_[0];

    std::vector<int> node_instances = Objects(root, kNodes);
    std::unordered_map<int, int> node_of;  // instance -> m_nodes index
    for (int i = 0; i < static_cast<int>(node_instances.size()); i++) node_of[node_instances[i]] = i;

    nodes_.resize(node_instances.size());
    for (int i = 0; i < static_cast<int>(node_instances.size()); i++) {
        Node& n = nodes_[i];
        int in = node_instances[i];
        n.index = i;
        n.instance = in;
        n.type = type_of(in);
        if (in < 0) {
            error = "null entry in m_nodes";
            return false;
        }
        n.fields = &values_[in];
        if (reg_->IsA(n.type, stype::Entry)) n.kind = NodeKind::Entry;
        else if (reg_->IsA(n.type, stype::Condition)) n.kind = NodeKind::Condition;
        else if (reg_->IsA(n.type, stype::Action)) n.kind = NodeKind::Action;
        else if (reg_->IsA(n.type, stype::ActionTerminator)) n.kind = NodeKind::Terminator;
        else if (reg_->IsA(n.type, stype::State)) n.kind = NodeKind::State;
        if (I32(n.Field(kNodeIndex)) != i) {
            error = "node +0x68 does not match its m_nodes index";
            return false;
        }
        n.remote_index = I32(n.Field(kRemoteIndex));
        n.ea_scope = static_cast<uint32_t>(I32(n.Field(kEaScope)));
        n.client_only = n.Field(kClientOnly) && n.Field(kClientOnly)->AsU8();
        n.server_only = n.Field(kServerOnly) && n.Field(kServerOnly)->AsU8();
        n.unique_id = static_cast<uint32_t>(I32(n.Field(kUniqueId)));
        if (n.kind == NodeKind::State) {
            n.state_index = I32(n.Field(kStateIndex));
            n.world_flags = static_cast<uint32_t>(I32(n.Field(kStateWorldFlags)));
        }
    }

    // Output plugs of every node: fields whose value is (a list of) STUGraphPlug objects of an output type.
    for (Node& n : nodes_) {
        const stu::TypeInfo* t = reg_->Find(n.type);
        if (!t) continue;
        for (const stu::FieldInfo* f : reg_->AllFields(*t)) {
            bool single = f->kind == stu::Kind::Object, list = f->kind == stu::Kind::ObjectList;
            if ((!single && !list) || !reg_->IsA(f->elem, stype::GraphPlug)) continue;
            std::vector<int> plug_instances;
            if (single) plug_instances.push_back(Object(*n.fields, f->hash));
            else plug_instances = Objects(*n.fields, f->hash);
            for (int k = 0; k < static_cast<int>(plug_instances.size()); k++) {
                int pi = plug_instances[k];
                if (pi < 0 || !IsA(pi, stype::OutputPlug)) continue;
                OutPlug p;
                p.offset = f->offset;
                p.index = list ? k : -1;
                p.field = f->hash;
                p.instance = pi;
                p.type = type_of(pi);
                const stu::Value& pv = values_[pi];
                for (int li : Objects(pv, kLinks)) {
                    if (li < 0) continue;
                    int ip = Object(values_[li], kInputPlug);
                    if (ip < 0) continue;
                    Link l;
                    l.input_instance = ip;
                    l.input_type = type_of(ip);
                    auto it = node_of.find(Object(values_[ip], kParentNode));
                    l.target = it == node_of.end() ? -1 : it->second;
                    p.links.push_back(l);
                }
                if (auto* sv = pv.Find(kSubgraphStates); sv && sv->type == stu::Value::Type::List)
                    for (auto& e : sv->items) p.subgraph.push_back(e.AsI32());
                p.value = Object(pv, kValue);
                n.plugs.push_back(std::move(p));
            }
        }
    }

    for (int si : Objects(root, kStates)) {
        auto it = node_of.find(si);
        if (it == node_of.end()) {
            error = "m_states entry is not in m_nodes";
            return false;
        }
        if (nodes_[it->second].state_index != static_cast<int>(states_.size())) {
            error = "state +0xF8 does not match its m_states index";
            return false;
        }
        states_.push_back(it->second);
    }
    for (int ei : Objects(root, kEntries)) {
        auto it = node_of.find(ei);
        if (it != node_of.end()) entries_.push_back(it->second);
    }
    for (int ri : Objects(root, kRemoteSyncNodes)) {
        auto it = node_of.find(ri);
        remote_nodes_.push_back(it == node_of.end() ? -1 : it->second);
    }
    if (auto* sv = root.Find(kSyncVars); sv && sv->type == stu::Value::Type::List)
        for (auto& e : sv->items) {
            SyncVar s;
            s.var = RefGuid(e.Find(kSyncVarId));
            for (auto& f : e.items) {
                if (f.type == stu::Value::Type::Enum) s.mode = f.AsU32();
                else if (f.type == stu::Value::Type::Primitive && f.raw.size() == 1) s.flag = f.AsU8();
            }
            sync_vars_.push_back(s);
        }
    if (int schema = Object(root, kPublicSchema); schema >= 0)
        for (int e : Objects(values_[schema], kSchemaEntries)) {
            if (e < 0) continue;
            SchemaEntry s;
            s.var = RefGuid(values_[e].Find(kSchemaVar));
            s.value = Object(values_[e], kValue);
            schema_.push_back(s);
        }
    if (auto* wv = root.Find(kWorldStates); wv && wv->type == stu::Value::Type::List)
        for (auto& e : wv->items) world_states_.push_back(e.AsI32());
    if (auto* bc = root.Find(0xe03b7f7b); bc && bc->type == stu::Value::Type::List)
        for (auto& e : bc->items) button_conditions_.push_back(e.AsI32());
    if (auto* bs = root.Find(0xd9b17c50); bs && bs->type == stu::Value::Type::List)
        for (auto& e : bs->items) button_states_.push_back(e.AsI32());
    if (auto* v = root.Find(kGraphWorldFlags)) world_flags_ = v->AsU32();
    if (auto* v = root.Find(kPrediction)) prediction_behavior_ = v->AsU32();
    // m_nodesBitCount, m_statesBitCount, m_remoteSyncNodesBitCount, m_syncVarsBitCount (+0x178…+0x184).
    const char* bits[4] = {"m_nodesBitCount", "m_statesBitCount", "m_remoteSyncNodesBitCount", "m_syncVarsBitCount"};
    for (int k = 0; k < 4; k++)
        if (auto* v = root.Find(Hash(bits[k]))) bit_counts_[k] = v->AsI32();
    return true;
}

}  // namespace ss
