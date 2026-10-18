#include "statescript.h"

#include <algorithm>
#include <bit>
#include <cstring>
#include <format>
#include <map>
#include <string>
#include <memory>

#include "ecs/entity.h"
#include "ecs/entity_admin.h"
#include "ecs/loader.h"
#include "ecs/manifest.h"
#include "statescript/capabilities.h"
#include "statescript/ecs_component.h"
#include "statescript/match_world.h"
#include "statescript/runtime.h"

using namespace ss;

struct ss_runtime {
    EvaluatorRegistry evals = EvaluatorRegistry::Builtin();
    std::map<uint64_t, std::unique_ptr<Graph>> graphs;
    std::map<uint64_t, SyncManager::StackPriority> stack_priorities;
};

struct ss_sm {
    ss_runtime* rt = nullptr;
    SyncManager* sm = nullptr;
    std::unique_ptr<SyncManager> owned;
    std::string trace;  // buffered JSON lines (ss_sm_trace_enable)
};

struct ss_world {
    ss_runtime* rt = nullptr;
    ecs::ComponentRegistry registry;
    std::unique_ptr<ecs::EntityAdmin> ea;
    std::map<uint32_t, std::unique_ptr<ss_sm>> handles;
};

namespace {
Primitive ToPrim(const ss_value& v) { return Primitive::Make(PrimType(v.type), v.bits, v.extra); }
ss_value FromPrim(const Primitive& p) { return ss_value{uint32_t(p.type), p.bits, p.extra}; }
Instance* Find(ss_sm* sm, int16_t id) { return sm && sm->sm ? sm->sm->FindInstance(id) : nullptr; }

// State data bits LSB first into `out` (cap in bits); returns the full bit count.
int CopyBits(const BitOut& b, uint8_t* out, int cap_bits) {
    if (out && cap_bits > 0) {
        std::memset(out, 0, size_t(cap_bits + 7) / 8);
        for (int k = 0; k < b.nbits && k < cap_bits; k++)
            if (b.bytes[k / 8] >> (k % 8) & 1) out[k / 8] |= uint8_t(1u << (k % 8));
    }
    return b.nbits;
}

// Every loaded graph is available to PlayScript / SubScript.
void Provide(ss_sm* h) {
    ss_runtime* rt = h->rt;
    h->sm->stack_priorities = rt->stack_priorities;
    h->sm->graph_provider = [rt](uint64_t guid) -> const Graph* {
        auto it = rt->graphs.find(guid);
        return it == rt->graphs.end() ? nullptr : it->second.get();
    };
}

void CollectRefs(const stu::Value& v, std::vector<uint64_t>& out) {
    if (v.type == stu::Value::Type::Ref && (v.guid >> 52) == 0x058 && v.guid)
        if (std::find(out.begin(), out.end(), v.guid) == out.end()) out.push_back(v.guid);
    for (auto& c : v.items) CollectRefs(c, out);
}
}  // namespace

extern "C" {

ss_runtime* ss_runtime_create(void) { return new ss_runtime(); }
void ss_runtime_destroy(ss_runtime* rt) { delete rt; }

int ss_runtime_load_graph(ss_runtime* rt, uint64_t guid, const uint8_t* bytes, size_t len, int mode, uint64_t base) {
    if (!rt || !bytes) return SS_ERR_ARG;
    std::string err;
    auto g = Graph::Load(std::vector<uint8_t>(bytes, bytes + len), guid, err,
                         mode == SS_MODE_LOADED ? stu::Mode::Loaded : stu::Mode::File, base);
    if (!g) return SS_ERR_PARSE;
    rt->graphs[guid] = std::move(g);
    return SS_OK;
}

int ss_runtime_set_stack_priority(ss_runtime* rt, uint64_t guid, float priority, int wins_ties) {
    if (!rt) return SS_ERR_ARG;
    rt->stack_priorities[guid] = {priority, wins_ties != 0};
    return SS_OK;
}

int ss_runtime_set_constant(ss_runtime* rt, uint32_t cv_type, ss_value value) {
    if (!rt) return SS_ERR_ARG;
    rt->evals.RegisterConstant(cv_type, ToPrim(value));
    return SS_OK;
}

ss_sm* ss_sm_create(ss_runtime* rt, uint32_t entity, int role) {
    if (!rt) return nullptr;
    auto h = new ss_sm();
    h->rt = rt;
    h->owned = std::make_unique<SyncManager>(entity, rt->evals, role == SS_ROLE_SERVER ? Role::Server : Role::Client);
    h->sm = h->owned.get();
    Provide(h);
    return h;
}

void ss_sm_destroy(ss_sm* sm) {
    if (sm && sm->owned) delete sm;
}

void ss_sm_set_clock(ss_sm* sm, uint32_t cf, int64_t ms) {
    if (sm) sm->sm->SetClock(cf, ms);
}

int32_t ss_sm_create_instance(ss_sm* sm, uint64_t graph, const uint64_t* vars, const ss_value* values, int count,
                              int16_t id) {
    if (!sm || count < 0 || (count && (!vars || !values))) return SS_ERR_ARG * 0x10000;
    auto it = sm->rt->graphs.find(graph);
    if (it == sm->rt->graphs.end()) return SS_ERR_NOT_FOUND * 0x10000;
    std::vector<Override> ov;
    for (int i = 0; i < count; i++) ov.push_back({vars[i], ToPrim(values[i])});
    Instance* inst = sm->sm->CreateInstance(*it->second, ov, id);
    return inst ? inst->id() : SS_ERR_EXISTS * 0x10000;
}

void ss_sm_run(ss_sm* sm, uint32_t cf, uint32_t n) {
    if (sm) sm->sm->RunCommandFrames(cf, n);
}

int ss_sm_deliver_message(ss_sm* sm, uint64_t message, const uint64_t* keys, const ss_value* values, int count,
                          uint32_t sender, uint32_t instigator) {
    if (!sm || count < 0 || (count && (!keys || !values))) return SS_ERR_ARG;
    GameMessage m{.id = message, .sender = sender, .instigator = instigator};
    for (int i = 0; i < count; i++) m.params.emplace_back(keys[i], ToPrim(values[i]));
    return sm->sm->DeliverGameMessage(m);
}

int ss_sm_set_var(ss_sm* sm, int16_t instance, uint64_t var, ss_value value) {
    if (!sm) return SS_ERR_ARG;
    VarBag* bag = nullptr;
    if (instance == 0) bag = &sm->sm->entity_bag();
    else if (Instance* i = Find(sm, instance)) bag = &i->bag();
    if (!bag) return SS_ERR_NOT_FOUND;
    bag->Set(var, ToPrim(value));
    return SS_OK;
}

int ss_sm_set_dict(ss_sm* sm, int16_t instance, uint64_t var, const ss_value* keys, const ss_value* values, int count) {
    if (!sm || count < 0 || (count && (!keys || !values))) return SS_ERR_ARG;
    VarBag* bag = nullptr;
    if (instance == 0) bag = &sm->sm->entity_bag();
    else if (Instance* i = Find(sm, instance)) bag = &i->bag();
    if (!bag) return SS_ERR_NOT_FOUND;
    std::vector<std::pair<Primitive, Primitive>> e;
    for (int k = 0; k < count; k++) e.emplace_back(ToPrim(keys[k]), ToPrim(values[k]));
    bag->SetDictionary(var, std::move(e));
    return SS_OK;
}

int ss_sm_get_var(ss_sm* sm, int16_t instance, uint64_t var, ss_value* out) {
    if (!sm || !out) return SS_ERR_ARG;
    const VarBag* bag = nullptr;
    if (instance == 0) bag = &sm->sm->entity_bag();
    else if (Instance* i = Find(sm, instance)) bag = &i->bag();
    if (!bag) return SS_ERR_NOT_FOUND;
    const Var* v = bag->Find(var);
    if (!v || v->kind() == Var::Kind::Bag || v->kind() == Var::Kind::Dictionary) return SS_ERR_NOT_FOUND;
    *out = FromPrim(v->value());
    return SS_OK;
}

int ss_sm_sync_vars(ss_sm* sm, int16_t instance, uint64_t* vars, ss_value* values, int cap) {
    Instance* i = Find(sm, instance);
    if (!i) return SS_ERR_NOT_FOUND;
    auto sv = i->SyncVars();
    for (int k = 0; k < int(sv.size()) && k < cap; k++) {
        if (vars) vars[k] = sv[k].first;
        if (values) values[k] = FromPrim(sv[k].second);
    }
    return int(sv.size());
}

int ss_sm_active_remote_states(ss_sm* sm, int16_t instance, int32_t* remote_index, int32_t* state_index, int cap) {
    Instance* i = Find(sm, instance);
    if (!i) return SS_ERR_NOT_FOUND;
    auto rs = i->ActiveRemoteStates();
    for (int k = 0; k < int(rs.size()) && k < cap; k++) {
        if (remote_index) remote_index[k] = rs[k].first;
        if (state_index) state_index[k] = rs[k].second;
    }
    return int(rs.size());
}

int ss_sm_active_states(ss_sm* sm, int16_t instance, int32_t* states, int cap) {
    Instance* i = Find(sm, instance);
    if (!i) return SS_ERR_NOT_FOUND;
    auto as = i->ActiveStates();
    for (int k = 0; k < int(as.size()) && k < cap; k++)
        if (states) states[k] = as[k];
    return int(as.size());
}

int ss_sm_state_changes(ss_sm* sm, int16_t instance, int32_t state) {
    Instance* i = Find(sm, instance);
    if (!i) return SS_ERR_NOT_FOUND;
    State* s = i->state(state);
    return s ? int(s->changes() & 0x7fffffff) : 0;
}

uint32_t ss_sm_command_frame(const ss_sm* sm) { return sm ? sm->sm->cf() : 0; }
int64_t ss_sm_time(const ss_sm* sm) { return sm ? sm->sm->time() : 0; }

int ss_runtime_graph_refs(ss_runtime* rt, uint64_t graph, uint64_t* out, int cap) {
    if (!rt) return SS_ERR_ARG;
    auto it = rt->graphs.find(graph);
    if (it == rt->graphs.end()) return SS_ERR_NOT_FOUND;
    const Graph& g = *it->second;
    std::vector<uint64_t> refs;
    for (int i = 0; i < g.instance_count(); i++) CollectRefs(g.value(i), refs);
    std::erase(refs, graph);
    for (int k = 0; k < int(refs.size()) && k < cap; k++)
        if (out) out[k] = refs[k];
    return int(refs.size());
}

namespace {
const Graph* GraphOf(ss_runtime* rt, uint64_t guid) {
    if (!rt) return nullptr;
    auto it = rt->graphs.find(guid);
    return it == rt->graphs.end() ? nullptr : it->second.get();
}
}  // namespace

namespace {
int32_t StateKind(const Graph& g, const Node& node) {
    int32_t kind = node.client_only ? SS_STATE_CLIENT_ONLY : node.server_only ? SS_STATE_SERVER_ONLY : SS_STATE_PLAIN;
    if (kind == SS_STATE_PLAIN)
        for (uint32_t t : {0x7bf31629u, 0x860ee5fcu, 0xffbfa88du})
            if (g.IsA(node.instance, t)) kind = SS_STATE_HAS_UPDATE;
    return kind;
}
}  // namespace

int ss_runtime_graph_sync_slots(ss_runtime* rt, uint64_t graph, ss_sync_slot* out, int cap) {
    const Graph* g = GraphOf(rt, graph);
    if (!g) return rt ? int(SS_ERR_NOT_FOUND) : int(SS_ERR_ARG);
    auto sv = g->sync_vars();
    for (int k = 0; k < int(sv.size()) && k < cap; k++)
        if (out) out[k] = ss_sync_slot{sv[k].var, sv[k].mode, sv[k].flag};
    return int(sv.size());
}

int ss_runtime_graph_states(ss_runtime* rt, uint64_t graph, ss_state_layout* out, int cap) {
    const Graph* g = GraphOf(rt, graph);
    if (!g) return rt ? int(SS_ERR_NOT_FOUND) : int(SS_ERR_ARG);
    int n = int(g->states().size());
    for (int k = 0; k < n && k < cap && out; k++) {
        const Node& node = g->state_node(k);
        out[k] = ss_state_layout{node.type, StateKind(*g, node), node.remote_index, node.index};
    }
    return n;
}


int ss_runtime_graph_remote_nodes(ss_runtime* rt, uint64_t graph, ss_remote_node* out, int cap) {
    const Graph* g = GraphOf(rt, graph);
    if (!g) return rt ? int(SS_ERR_NOT_FOUND) : int(SS_ERR_ARG);
    auto rn = g->remote_nodes();
    for (int k = 0; k < int(rn.size()) && k < cap && out; k++) {
        if (rn[k] < 0) {  // not a node of this graph (never seen)
            out[k] = ss_remote_node{-1, -1, -1};
            continue;
        }
        const Node& node = g->node(rn[k]);
        bool st = node.kind == NodeKind::State;
        out[k] = ss_remote_node{node.index, st ? node.state_index : -1, st ? StateKind(*g, node) : -1};
    }
    return int(rn.size());
}

int ss_runtime_graph_bit_counts(ss_runtime* rt, uint64_t graph, int32_t out[4]) {
    const Graph* g = GraphOf(rt, graph);
    if (!g) return rt ? int(SS_ERR_NOT_FOUND) : int(SS_ERR_ARG);
    if (!out) return SS_ERR_ARG;
    for (int k = 0; k < 4; k++) out[k] = g->bit_counts()[k];
    return SS_OK;
}

int ss_runtime_graph_index_bits(ss_runtime* rt, uint64_t graph) {
    const Graph* g = GraphOf(rt, graph);
    if (!g) return rt ? int(SS_ERR_NOT_FOUND) : int(SS_ERR_ARG);
    uint32_t n = uint32_t(g->states().size());
    return n > 1 ? int(std::bit_width(n - 1)) : 1;
}

int32_t ss_sm_import_instance(ss_sm* sm, uint64_t graph, int16_t id, int16_t parent, uint32_t slot) {
    if (!sm || id == 0) return SS_ERR_ARG * 0x10000;
    auto it = sm->rt->graphs.find(graph);
    if (it == sm->rt->graphs.end()) return SS_ERR_NOT_FOUND * 0x10000;
    Instance* inst = sm->sm->ImportInstance(*it->second, id, parent, slot);
    return inst ? inst->id() : SS_ERR_EXISTS * 0x10000;
}

int ss_sm_import_state(ss_sm* sm, int16_t instance, int32_t state, const uint8_t* data, int nbits) {
    Instance* i = Find(sm, instance);
    if (!i) return SS_ERR_NOT_FOUND;
    if (state < 0 || state >= int(i->graph().states().size()) || nbits < 0) return SS_ERR_ARG;
    i->EnsureState(state).Import(data, nbits);
    return SS_OK;
}

int ss_sm_input(ss_sm* sm, uint32_t cf, uint16_t buttons, uint8_t action) {
    if (!sm) return SS_ERR_ARG;
    sm->sm->Input(cf, buttons, action);
    return SS_OK;
}

int ss_sm_instances(ss_sm* sm, int16_t* ids, int cap) {
    if (!sm) return SS_ERR_ARG;
    int n = 0;
    for (auto& i : sm->sm->instances()) {
        if (i->stopped()) continue;
        if (ids && n < cap) ids[n] = i->id();
        n++;
    }
    return n;
}

int ss_sm_instance_info(ss_sm* sm, int16_t instance, ss_instance_info* out) {
    Instance* i = Find(sm, instance);
    if (!i) return SS_ERR_NOT_FOUND;
    if (!out) return SS_ERR_ARG;
    *out = ss_instance_info{i->graph().guid(), i->parent(), i->slot(), i->flags()};
    return SS_OK;
}

int ss_sm_vars(ss_sm* sm, int16_t instance, uint64_t* vars, ss_value* values, int cap) {
    if (!sm) return SS_ERR_ARG;
    const VarBag* bag = nullptr;
    if (instance == 0) bag = &sm->sm->entity_bag();
    else if (Instance* i = Find(sm, instance)) bag = &i->bag();
    if (!bag) return SS_ERR_NOT_FOUND;
    int n = 0;
    for (auto& [id, v] : bag->vars()) {
        if (v->kind() != Var::Kind::Primitive) continue;
        if (n < cap) {
            if (vars) vars[n] = id;
            // The stored primitive (var +0x110): what the client's sync writes and keeps. A read through an owner
            // (Stack fold, an Ability's adopted cooldown) is local to the reader (T051 [L]: sending the fold put
            // a float cooldown into the client's INT $3d).
            if (values) values[n] = FromPrim(v->stored());
        }
        n++;
    }
    return n;
}

int ss_sm_var_users(ss_sm* sm, int16_t instance, uint64_t* vars, ss_var_user* users, int cap) {
    if (!sm) return SS_ERR_ARG;
    const VarBag* bag = nullptr;
    if (instance == 0) bag = &sm->sm->entity_bag();
    else if (Instance* i = Find(sm, instance)) bag = &i->bag();
    if (!bag) return SS_ERR_NOT_FOUND;
    int n = 0;
    for (auto& [id, v] : bag->vars()) {
        if (v->kind() != Var::Kind::Primitive) continue;
        if (n < cap) {
            if (vars) vars[n] = id;
            const VarUser& u = v->user();
            if (users) users[n] = ss_var_user{u.instance, u.state, u.priority, uint8_t(u.state_defined), u.extra};
        }
        n++;
    }
    return n;
}

int ss_sm_state_data(ss_sm* sm, int16_t instance, int32_t state, int full, uint8_t* out, int cap_bits) {
    Instance* i = Find(sm, instance);
    if (!i) return SS_ERR_NOT_FOUND;
    State* s = i->state(state);
    if (!s) return SS_ERR_NOT_FOUND;  // an ended state keeps its fields (a "has update" end carries them)
    BitOut b;
    s->WriteData(b, full != 0);
    return CopyBits(b, out, cap_bits);
}

int ss_sm_state_data2(ss_sm* sm, int16_t instance, int32_t state, int local, int full, uint8_t* out, int cap_bits) {
    Instance* i = Find(sm, instance);
    if (!i) return SS_ERR_NOT_FOUND;
    State* s = i->state(state);
    if (!s) return SS_ERR_NOT_FOUND;
    TypeCap cap = Capability(s->node().type, &sm->rt->evals);
    DataCap d = local ? cap.local : cap.remote;
    BitOut b;
    if (d == DataCap::None) return 0;
    if (local) {
        if (d == DataCap::Echo && !s->imported_data()) return SS_ERR_UNIMPLEMENTED;
        s->WriteData(b, full != 0);
    } else {
        if (d == DataCap::Echo) return SS_ERR_UNIMPLEMENTED;  // imported bits are the local format
        const ClientType* ct = FindClientType(s->node().type);
        if (ct && ct->same) s->WriteData(b, full != 0);
        else if (!s->WriteRemoteData(b, full != 0)) return SS_ERR_UNIMPLEMENTED;
    }
    return CopyBits(b, out, cap_bits);
}

int ss_sm_state_data_refs(ss_sm* sm, int16_t instance, int32_t state, int local, int16_t* ids, int cap) {
    Instance* i = Find(sm, instance);
    if (!i) return SS_ERR_NOT_FOUND;
    State* s = i->state(state);
    if (!s) return SS_ERR_NOT_FOUND;
    std::vector<int16_t> refs;
    s->DataRefs(local != 0, refs);
    for (int k = 0; k < int(refs.size()) && k < cap; k++)
        if (ids) ids[k] = refs[k];
    return int(refs.size());
}

int ss_sm_host_events(ss_sm* sm, uint32_t since_cf, ss_host_event* out, int cap) {
    if (!sm || !sm->sm) return SS_ERR_ARG;
    int n = 0;
    for (auto& e : sm->sm->host_events()) {
        if (e.cf <= since_cf) continue;
        if (n < cap && out) out[n] = ss_host_event{e.cf, e.instance, e.node, e.state, e.type, e.kind};
        n++;
    }
    return n;
}

namespace {
int FieldCv(Instance* i, int32_t node, uint32_t field) {
    if (!i || node < 0 || node >= int(i->graph().nodes().size())) return -2;
    const Node& n = i->graph().node(node);
    return n.fields ? i->graph().Object(*n.fields, field) : -1;
}
}  // namespace

int ss_sm_eval_field(ss_sm* sm, int16_t instance, int32_t node, uint32_t field_hash, ss_value* out) {
    Instance* i = Find(sm, instance);
    int cv = FieldCv(i, node, field_hash);
    if (cv < 0) return SS_ERR_NOT_FOUND;
    if (!out) return SS_ERR_ARG;
    EvalContext ctx{i};
    Primitive v;
    if (!Evaluate(ctx, cv, v)) return 1;
    *out = FromPrim(v);
    return SS_OK;
}

int ss_sm_iterate_field(ss_sm* sm, int16_t instance, int32_t node, uint32_t field_hash, ss_value* keys, ss_value* values,
                        int cap) {
    Instance* i = Find(sm, instance);
    int cv = FieldCv(i, node, field_hash);
    if (cv < 0) return SS_ERR_NOT_FOUND;
    EvalContext ctx{i};
    auto entries = Iterate(ctx, cv);
    for (int k = 0; k < int(entries.size()) && k < cap; k++) {
        if (keys) keys[k] = FromPrim(entries[k].first);
        if (values) values[k] = FromPrim(entries[k].second);
    }
    return int(entries.size());
}

int ss_sm_end_state(ss_sm* sm, int16_t instance, int32_t state, int finish) {
    Instance* i = Find(sm, instance);
    if (!i) return SS_ERR_NOT_FOUND;
    State* s = i->state(state);
    if (!s || !s->active()) return SS_ERR_NOT_FOUND;
    s->RequestEnd(finish != 0);
    return SS_OK;
}

int ss_sm_actions(ss_sm* sm, uint32_t since_cf, ss_action* out, int cap) {
    if (!sm || !sm->sm) return SS_ERR_ARG;
    int n = 0;
    for (auto& a : sm->sm->actions()) {
        if (a.cf <= since_cf) continue;
        if (n < cap && out) {
            const Instance* i = sm->sm->FindInstance(a.instance);
            int remote = i && a.node < int(i->graph().nodes().size()) ? i->graph().node(a.node).remote_index : -1;
            out[n] = ss_action{a.cf, a.instance, a.node, remote};
        }
        n++;
    }
    return n;
}

namespace {
ss_type_cap ToCap(const TypeCap& c) {
    uint8_t kind = c.kind == TypeKind::State ? SS_KIND_STATE : c.kind == TypeKind::ConfigVar ? SS_KIND_CONFIG_VAR : SS_KIND_NODE;
    return ss_type_cap{c.type, kind, uint8_t(c.level), uint8_t(c.local), uint8_t(c.remote), uint8_t(c.has_update), uint8_t(c.host)};
}
}  // namespace

int ss_runtime_type_caps(ss_runtime* rt, ss_type_cap* out, int cap) {
    if (!rt) return SS_ERR_ARG;
    auto types = ClientTypes();
    for (int k = 0; k < int(types.size()) && k < cap; k++)
        if (out) out[k] = ToCap(Capability(types[k].type, &rt->evals));
    return int(types.size());
}

int ss_runtime_type_cap(ss_runtime* rt, uint32_t type, ss_type_cap* out) {
    if (!rt || !out) return SS_ERR_ARG;
    TypeCap c = Capability(type, &rt->evals);
    if (!c.known) return SS_ERR_NOT_FOUND;
    *out = ToCap(c);
    return SS_OK;
}

int ss_runtime_graph_node_types(ss_runtime* rt, uint64_t graph, uint32_t* types, int cap) {
    if (!rt) return SS_ERR_ARG;
    auto it = rt->graphs.find(graph);
    if (it == rt->graphs.end()) return SS_ERR_NOT_FOUND;
    auto nodes = it->second->nodes();
    for (int k = 0; k < int(nodes.size()) && k < cap; k++)
        if (types) types[k] = nodes[k].type;
    return int(nodes.size());
}

int ss_sm_events(ss_sm* sm, int16_t instance, ss_event* out, int cap) {
    Instance* i = Find(sm, instance);
    if (!i) return SS_ERR_NOT_FOUND;
    int n = 0;
    for (auto& q : i->queue()) {
        int32_t kind = q.type == NotifType::Timer ? 1
                       : (q.type == NotifType::Lifetime && q.lifetime == LifetimeKind::Finish) ? 2 : 0;
        if (!kind) continue;
        if (out && n < cap) out[n] = ss_event{kind, q.state, q.data, q.time};
        n++;
    }
    return n;
}

namespace {
void JsonString(std::string& out, const std::string& v) {
    out += '"';
    for (char c : v) {
        if (c == '"' || c == '\\') out += '\\';
        if (static_cast<unsigned char>(c) < 0x20) continue;
        out += c;
    }
    out += '"';
}
}  // namespace

int ss_sm_trace_enable(ss_sm* sm, int on) {
    if (!sm) return SS_ERR_ARG;
    if (!on) {
        sm->sm->trace = nullptr;
        return SS_OK;
    }
    sm->sm->trace = [sm](const TraceEvent& e) {
        std::string& o = sm->trace;
        o += "{\"kind\":";
        JsonString(o, e.kind);
        o += std::format(",\"cf\":{},\"ms\":{},\"inst\":{},\"a\":{},\"b\":{},\"c\":{},\"idx\":{},\"s\":", e.cf, e.ms,
                         e.instance, e.a, e.b, e.c, e.idx);
        JsonString(o, e.text);
        o += "}\n";
    };
    return SS_OK;
}

int ss_sm_trace_drain(ss_sm* sm, char* buf, int cap) {
    if (!sm) return SS_ERR_ARG;
    int need = int(sm->trace.size()) + 1;
    if (!buf || cap < need) return -need;
    std::memcpy(buf, sm->trace.data(), sm->trace.size());
    buf[sm->trace.size()] = 0;
    sm->trace.clear();
    return need - 1;
}

int ss_sm_unmodelled(ss_sm* sm, int kind, uint32_t* types, int32_t* counts, int cap) {
    if (!sm || kind < 0 || kind > 2) return SS_ERR_ARG;
    const auto& m = kind == 0 ? sm->sm->unmodelled_nodes : kind == 1 ? sm->sm->unmodelled_states
                                                                     : sm->sm->unmodelled_config_vars;
    int n = 0;
    for (auto& [t, c] : m) {
        if (n < cap) {
            if (types) types[n] = t;
            if (counts) counts[n] = c;
        }
        n++;
    }
    return n;
}

ss_world* ss_world_create(ss_runtime* rt, int role) {
    if (!rt) return nullptr;
    auto w = new ss_world();
    w->rt = rt;
    ecs::manifest::RegisterComponents(w->registry);
    RegisterStatescriptComponent(w->registry, rt->evals, role == SS_ROLE_SERVER ? Role::Server : Role::Client);
    w->ea = std::make_unique<ecs::EntityAdmin>(w->registry);
    w->ea->Initialize();
    return w;
}

void ss_world_destroy(ss_world* w) { delete w; }

uint32_t ss_world_spawn(ss_world* w, const uint8_t* components, int count) {
    if (!w || count < 0 || (count && !components)) return 0;
    ecs::BasicLoader loader(std::vector<ecs::ComponentId>(components, components + count));
    ecs::Entity* e = w->ea->Spawn(loader);
    return e ? e->id() : 0;
}

ss_sm* ss_world_sm(ss_world* w, uint32_t entity) {
    if (!w) return nullptr;
    ecs::Entity* e = w->ea->Find(entity);
    SyncManager* sm = e ? SyncManagerOf(*e) : nullptr;
    if (!sm) return nullptr;
    auto& h = w->handles[entity];
    if (!h || h->sm != sm) {
        h = std::make_unique<ss_sm>();
        h->rt = w->rt;
        h->sm = sm;
        Provide(h.get());
    }
    return h.get();
}

// ---- match hosting (T057) ----

}  // extern "C"

struct ss_match {
    ss_runtime* rt = nullptr;
    std::unique_ptr<MatchWorld> w;
    std::map<uint32_t, std::unique_ptr<ss_sm>> handles;
    size_t drained = 0;  // effects already handed out
};

namespace {
MatchWorld::Entity* MatchFind(ss_match* m, uint32_t id) { return m ? m->w->Find(id) : nullptr; }
}  // namespace

extern "C" {

ss_match* ss_match_create(ss_runtime* rt, uint32_t cf, uint32_t seed) {
    if (!rt) return nullptr;
    auto m = new ss_match();
    m->rt = rt;
    m->w = std::make_unique<MatchWorld>(rt->evals, [rt](uint64_t guid) -> const Graph* {
        auto it = rt->graphs.find(guid);
        return it == rt->graphs.end() ? nullptr : it->second.get();
    });
    m->w->stack_priorities = rt->stack_priorities;
    m->w->mirrored_ids = true;
    m->w->SetFrame(cf);
    m->w->seed = seed ? seed : 12345;
    return m;
}

void ss_match_destroy(ss_match* m) {
    if (!m) return;
    m->handles.clear();
    delete m;
}

int ss_match_add(ss_match* m, uint32_t id, uint64_t def, uint32_t flags) {
    if (!m || !id) return SS_ERR_ARG;
    if (m->w->Find(id)) return SS_ERR_EXISTS;
    auto& e = m->w->Add(id, def, flags & SS_ME_SM);
    e.player = flags & SS_ME_PLAYER;
    e.pvp = flags & SS_ME_PVP;
    if (flags & SS_ME_GLOBAL) m->w->global_entity = id;
    return SS_OK;
}

int ss_match_remove(ss_match* m, uint32_t id) {
    if (!m) return SS_ERR_ARG;
    m->handles.erase(id);
    return m->w->Remove(id) ? SS_OK : SS_ERR_NOT_FOUND;
}

ss_sm* ss_match_sm(ss_match* m, uint32_t id) {
    auto* e = MatchFind(m, id);
    if (!e || !e->sm) return nullptr;
    auto& h = m->handles[id];
    if (!h || h->sm != e->sm.get()) {
        h = std::make_unique<ss_sm>();
        h->rt = m->rt;
        h->sm = e->sm.get();
    }
    return h.get();
}

int ss_match_set_transform(ss_match* m, uint32_t id, float x, float y, float z, float yaw) {
    auto* e = MatchFind(m, id);
    if (!e) return m ? SS_ERR_NOT_FOUND : SS_ERR_ARG;
    e->pos = {x, y, z}, e->yaw = yaw;
    return SS_OK;
}

int ss_match_set_team(ss_match* m, uint32_t id, int32_t team) {
    auto* e = MatchFind(m, id);
    if (!e) return m ? SS_ERR_NOT_FOUND : SS_ERR_ARG;
    e->team = team;
    return SS_OK;
}

int ss_match_set_filter_bits(ss_match* m, uint32_t id, uint64_t extra) {
    auto* e = MatchFind(m, id);
    if (!e) return m ? SS_ERR_NOT_FOUND : SS_ERR_ARG;
    e->extra_bits = extra;
    return SS_OK;
}

int ss_match_set_alive(ss_match* m, uint32_t id, int alive) {
    if (!m) return SS_ERR_ARG;
    // Dead players stay in the world (filters see bit 0x10 [?]); only removal takes an entity out.
    auto* e = m->w->Find(id);
    if (!e) return SS_ERR_NOT_FOUND;
    e->extra_bits = alive ? e->extra_bits & ~0x10ull : e->extra_bits | 0x10ull;
    return SS_OK;
}

int ss_match_set_tags(ss_match* m, uint32_t id, const uint64_t* tags, int count) {
    auto* e = MatchFind(m, id);
    if (!e || count < 0 || (count && !tags)) return !e && m ? SS_ERR_NOT_FOUND : SS_ERR_ARG;
    e->tags.assign(tags, tags + count);
    return SS_OK;
}

int ss_match_set_player(ss_match* m, uint32_t hero, uint32_t controller, uint64_t hero_guid) {
    auto* e = MatchFind(m, hero);
    if (!e) return m ? SS_ERR_NOT_FOUND : SS_ERR_ARG;
    e->player = true, e->controller = controller, e->hero_guid = hero_guid;
    return SS_OK;
}

int ss_match_set_health(ss_match* m, uint32_t id, int32_t pool, float current, float max) {
    auto* e = MatchFind(m, id);
    if (!e) return m ? SS_ERR_NOT_FOUND : SS_ERR_ARG;
    e->health[pool] = {current, max};
    return SS_OK;
}

int ss_match_set_throttle(ss_match* m, uint32_t id, float x, float y) {
    auto* e = MatchFind(m, id);
    if (!e) return m ? SS_ERR_NOT_FOUND : SS_ERR_ARG;
    e->throttle = {x, y, 0};
    return SS_OK;
}

int ss_match_set_capture_area(ss_match* m, uint32_t id, const ss_capture_rules* r) {
    auto* e = MatchFind(m, id);
    if (!e || !r) return !e && m ? SS_ERR_NOT_FOUND : SS_ERR_ARG;
    e->capture_area = true;
    e->capture_radius = r->radius;
    e->rules = CaptureAreaRules{.decay_time = r->decay_time, .f349aaca6 = r->f349aaca6, .segments = r->segments,
                                .extra_player = r->extra_player, .capture_time = r->capture_time,
                                .f5f080cc5 = r->f5f080cc5, .decay_delay = r->decay_delay, .f2ca7fc17 = r->f2ca7fc17,
                                .fe6e7f301 = r->fe6e7f301, .f3a691fdf = r->f3a691fdf != 0,
                                .max_players = r->max_players > 0 ? r->max_players : 3};
    return SS_OK;
}

int ss_match_reserve_ids(ss_match* m, uint32_t first, uint32_t count) {
    if (!m || !count) return SS_ERR_ARG;
    m->w->next_id = first, m->w->id_end = first + count;
    return SS_OK;
}

int32_t ss_match_start(ss_match* m, uint32_t id, uint64_t graph, const uint64_t* vars, const ss_value* values,
                       int count) {
    auto* e = MatchFind(m, id);
    if (!e || !e->sm) return m ? int32_t(SS_ERR_NOT_FOUND) * 0x10000 : int32_t(SS_ERR_ARG) * 0x10000;
    if (count < 0 || (count && (!vars || !values))) return int32_t(SS_ERR_ARG) * 0x10000;
    if (!m->rt->graphs.count(graph)) return int32_t(SS_ERR_NOT_FOUND) * 0x10000;
    std::vector<Override> ov;
    for (int k = 0; k < count; k++) ov.push_back(Override{vars[k], ToPrim(values[k])});
    Instance* inst = m->w->Start(id, graph, ov);
    return inst ? inst->id() : int32_t(SS_ERR_NOT_FOUND) * 0x10000;
}

int ss_match_send(ss_match* m, uint32_t target, uint64_t message, const uint64_t* keys, const ss_value* values,
                  int count) {
    if (!m || count < 0 || (count && (!keys || !values))) return SS_ERR_ARG;
    GameMessage msg;
    msg.id = message;
    for (int k = 0; k < count; k++) msg.params.emplace_back(keys[k], ToPrim(values[k]));
    m->w->Send(msg, target);
    return SS_OK;
}

int ss_match_player_event(ss_match* m, int kind, uint32_t player, uint32_t other) {
    if (!m || kind < 0 || kind > 2) return SS_ERR_ARG;
    m->w->PlayerEvent(PlayerEvent(kind), player, other);
    return SS_OK;
}

void ss_match_step(ss_match* m, uint32_t cf) {
    if (m && cf > m->w->cf()) m->w->Step(cf - m->w->cf());
}

uint32_t ss_match_frame(const ss_match* m) { return m ? m->w->cf() : 0; }

int ss_match_effects(ss_match* m, ss_match_effect* out, int cap) {
    if (!m || cap < 0 || (cap && !out)) return SS_ERR_ARG;
    auto& fx = m->w->effects;
    int n = 0;
    for (; n < cap && m->drained < fx.size(); n++, m->drained++) {
        const auto& e = fx[m->drained];
        ss_match_effect& o = out[n];
        o.seq = e.seq, o.cf = e.cf, o.kind = int32_t(e.kind), o.entity = e.entity, o.other = e.other;
        o.i0 = e.i0, o.i1 = e.i1, o.u = e.u;
        std::memcpy(o.f, e.f, sizeof o.f);
    }
    if (m->drained == fx.size()) fx.clear(), m->drained = 0;
    return n;
}

int ss_match_capture(ss_match* m, uint32_t id, ss_capture_state* out) {
    auto* e = MatchFind(m, id);
    if (!e || !out) return !e && m ? SS_ERR_NOT_FOUND : SS_ERR_ARG;
    if (!e->capture_area) return SS_ERR_NOT_FOUND;
    *out = ss_capture_state{e->capture_enabled, e->capture_owner, e->capture_team, e->capture_contested,
                            e->capture_progress};
    return SS_OK;
}

}  // extern "C"
