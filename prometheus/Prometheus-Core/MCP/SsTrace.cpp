#include "SsTrace.h"
#include "../globals.h"
#include <windows.h>
#include <MinHook.h>
#include <mutex>
#include <vector>
#include <string>
#include <set>
#include <map>
#include <unordered_map>
#include <format>

namespace ss_trace {

// Engine functions (RVAs; Ghidra = 0x7ff64ffe0000 + RVA). See docs/StatescriptRuntime.md §3.
static constexpr uintptr_t RVA_CF_LOOP     = 0xf44ef0; // SyncMgr::RunCommandFrames(sm, u32 cf, u32 n)
static constexpr uintptr_t RVA_EXEC_NOTIF  = 0xf4f5b0; // bool Instance::ExecuteNotification(inst, notif) (vfunc +0x158)
static constexpr uintptr_t RVA_BEGIN       = 0xf495c0; // State::DoBeginState(state, i64 time_offset)
static constexpr uintptr_t RVA_FINISH      = 0xf499d0; // State::DoFinishState(state)
static constexpr uintptr_t RVA_ABORT       = 0xf49df0; // State::DoAbortState(state)
static constexpr uintptr_t RVA_TRAVERSE    = 0xf471f0; // bool TraverseLink(src, inst, src_node, plug_off, in_plug, idx)
static constexpr uintptr_t RVA_INSTANTIATE = 0xf4e370; // Instance::InstantiateGraph(inst): fresh states, queue entries
static constexpr uintptr_t RVA_MSG_DELIVER = 0xf50770; // int Instance::DeliverGameMessage(inst, record, u32)
static constexpr uintptr_t RVA_VT_PRIMITIVE = 0x1608248, RVA_VT_INSTREF = 0x1607fb8,
                           RVA_VT_DICT = 0x1607d20, RVA_VT_VARBAG = 0x1607a90;

struct Event {
    uint64_t seq;
    const char* kind;
    uint32_t cf;       // sync manager current CF (+0x2C4)
    int64_t ms;        // sync manager time (+0x2D0)
    uint32_t ent;      // owner entity id
    int32_t inst;      // instance id (i16), 0 = none
    int64_t a, b, c;
    std::string s;
};

static std::recursive_mutex g_mtx;
static std::vector<Event> g_events;
static uint64_t g_seq = 0, g_dropped = 0;
static size_t g_cap = 300000;
static bool g_enabled = false, g_hooks_installed = false, g_all = false;
static std::set<uint64_t> g_graphs;
static std::set<uint32_t> g_ents;
// last seen value per variable: key "<bag role>:<id>" -> text, per instance / per sync manager
static std::unordered_map<uintptr_t, std::map<std::string, std::string>> g_vars;

template <class T> static T rd(uintptr_t p, size_t off) { return *(T*)(p + off); }

// STU array field (loaded in place): header pointer + flag word; the flag selects the layout.
static bool stu_arr(uintptr_t field, uintptr_t& data, uint32_t& count) {
    uintptr_t hdr = rd<uintptr_t>(field, 0);
    if (!hdr) return false;
    if (rd<int64_t>(field, 8) == 1) { data = rd<uintptr_t>(hdr, 0); count = rd<uint32_t>(hdr, 8); }
    else { count = rd<uint32_t>(hdr, 0); data = rd<uintptr_t>(hdr, 8); }
    return data != 0;
}

static uintptr_t inst_sm(uintptr_t inst) { return rd<uintptr_t>(inst, 0x10); }
static uint32_t sm_ent(uintptr_t sm) {
    uintptr_t comp = rd<uintptr_t>(sm, 0x450);
    uintptr_t ent = comp ? rd<uintptr_t>(comp, 0x08) : 0;
    return ent ? rd<uint32_t>(ent, 0x94) : 0;
}
static uint64_t inst_guid(uintptr_t inst) { return rd<uint64_t>(inst, 0x150); }

static bool traced(uintptr_t inst) {
    if (!g_enabled || !inst) return false;
    if (g_all) return true;
    if (g_graphs.count(inst_guid(inst))) return true;
    uintptr_t sm = inst_sm(inst);
    return sm && !g_ents.empty() && g_ents.count(sm_ent(sm));
}
static bool sm_traced(uintptr_t sm) {
    if (!g_enabled || !sm) return false;
    uintptr_t list = rd<uintptr_t>(sm, 0xF0);
    int n = rd<int>(sm, 0xF8);
    for (int i = 0; list && i < n; i++) if (traced(rd<uintptr_t>(list, i * 8))) return true;
    return false;
}

// node index (m_nodes) of the state object's STU state
static int state_node(uintptr_t state) {
    uintptr_t inst = rd<uintptr_t>(state, 0x78), graph = inst ? rd<uintptr_t>(inst, 0x160) : 0;
    uintptr_t data; uint32_t n; int si = rd<int>(state, 0x70);
    if (!graph || !stu_arr(graph + 0x60, data, n) || si < 0 || (uint32_t)si >= n) return -1;
    uintptr_t node = rd<uintptr_t>(data, si * 8);
    return node ? rd<int>(node, 0x68) : -1;
}

static void record(const char* kind, uintptr_t inst, uintptr_t sm, int64_t a = 0, int64_t b = 0, int64_t c = 0,
                   std::string s = {}) {
    Event ev{};
    ev.kind = kind;
    if (!sm && inst) sm = inst_sm(inst);
    if (sm) { ev.cf = rd<uint32_t>(sm, 0x2C4); ev.ms = rd<int64_t>(sm, 0x2D0); ev.ent = sm_ent(sm); }
    ev.inst = inst ? rd<int16_t>(inst, 0x18) : 0;
    ev.a = a; ev.b = b; ev.c = c; ev.s = std::move(s);
    std::lock_guard lk(g_mtx);
    if (g_events.size() >= g_cap) { g_dropped++; return; }
    ev.seq = ++g_seq;
    g_events.push_back(std::move(ev));
}

// --- variables -----------------------------------------------------------

static std::string prim_text(uintptr_t p) { // StatescriptPrimitive (16 B, type at +0xC)
    uint32_t t = rd<uint32_t>(p, 0xC);
    switch (t) {
    case 1: return std::format("BYTE {}", rd<uint8_t>(p, 0));
    case 2: return std::format("INT {}", rd<int32_t>(p, 0));
    case 3: return std::format("FLT {}", rd<float>(p, 0));
    case 4: return std::format("INT64 0x{:x}", rd<uint64_t>(p, 0));
    case 5: { auto str = rd<const char*>(p, 0); return std::format("STRING {}", str ? std::string(str, strnlen(str, 64)) : ""); }
    case 6: return std::format("VEC3 {} {} {}", rd<float>(p, 0), rd<float>(p, 4), rd<float>(p, 8));
    case 8: return std::format("INSTID {} {}", rd<int16_t>(p, 0), rd<int16_t>(p, 2));
    case 11: return std::format("ENTITY 0x{:x}", rd<uint32_t>(p, 0));
    default: return std::format("T{} 0x{:x} 0x{:x}", t, rd<uint64_t>(p, 0), rd<uint32_t>(p, 8));
    }
}

static void walk_bag(uintptr_t bag, const std::string& role, std::map<std::string, std::string>& out, int depth) {
    if (!bag || depth > 3) return;
    uintptr_t base = globals::gameBase;
    for (int b = 0; b < 16; b++) {
        uintptr_t ptr = rd<uintptr_t>(bag, 0xA0 + b * 0x30);
        int cnt = rd<int>(bag, 0xA8 + b * 0x30);
        for (int i = 0; ptr && i < cnt && i < 4096; i++) {
            uint64_t id = rd<uint64_t>(ptr, i * 16);
            uintptr_t var = rd<uintptr_t>(ptr, i * 16 + 8);
            if (!var) continue;
            uintptr_t vt = rd<uintptr_t>(var, 0) - base;
            std::string key = std::format("{}:{:x}", role, id & 0xFFFFFFFFFFFFull);
            if (vt == RVA_VT_PRIMITIVE || vt == RVA_VT_INSTREF) out[key] = prim_text(var + 0xA0);
            else if (vt == RVA_VT_DICT) out[key] = std::format("DICT n={}", rd<int>(var, 0xA8));
            else if (vt == RVA_VT_VARBAG) walk_bag(var, key, out, depth + 1);
            else out[key] = std::format("?vt 0x{:x}", vt);
        }
    }
}

static void diff_vars(uintptr_t owner, uintptr_t bag, const char* role, uintptr_t inst, uintptr_t sm) {
    std::map<std::string, std::string> now;
    walk_bag(bag, role, now, 0);
    std::lock_guard lk(g_mtx);
    auto& before = g_vars[owner];
    for (auto& [k, v] : now) {
        auto it = before.find(k);
        if (it == before.end() || it->second != v) record("var", inst, sm, 0, 0, 0, k + "=" + v);
    }
    for (auto& [k, v] : before) if (!now.count(k)) record("var", inst, sm, 0, 0, 0, k + " removed");
    before = std::move(now);
}

static void snapshot_sm(uintptr_t sm) {
    uintptr_t list = rd<uintptr_t>(sm, 0xF0);
    int n = rd<int>(sm, 0xF8);
    bool any = false;
    for (int i = 0; list && i < n; i++) {
        uintptr_t inst = rd<uintptr_t>(list, i * 8);
        if (!traced(inst)) continue;
        any = true;
        diff_vars(inst, rd<uintptr_t>(inst, 0x98), "inst", inst, sm);
    }
    if (any) diff_vars(sm, rd<uintptr_t>(sm, 0x2E0), "ent", 0, sm);
}

// --- hooks ---------------------------------------------------------------

static void(__fastcall* o_cf_loop)(uintptr_t, uint32_t, uint32_t);
static void __fastcall h_cf_loop(uintptr_t sm, uint32_t cf, uint32_t n) {
    bool t = sm_traced(sm);
    if (t) record("cf", 0, sm, cf, n, rd<int>(sm, 0xF8));
    o_cf_loop(sm, cf, n);
    if (t && g_enabled) { record("cf_end", 0, sm, cf); snapshot_sm(sm); }
}

static bool(__fastcall* o_exec_notif)(uintptr_t, uintptr_t);
static bool __fastcall h_exec_notif(uintptr_t inst, uintptr_t nt) {
    bool t = traced(inst);
    if (t) {
        // a = type (+0x22), b = notification time (+0x18), c = raw +0x30..+0x37; s = decoded
        uint8_t type = rd<uint8_t>(nt, 0x22);
        uint16_t w0 = rd<uint16_t>(nt, 0x30), w1 = rd<uint16_t>(nt, 0x32), w2 = rd<uint16_t>(nt, 0x34);
        std::string s;
        static const char* kLife[] = { "begin", "finish", "abort" };
        switch (type) {
        case 0: s = std::format("lifetime {} state {}", w0 < 3 ? kLife[w0] : "?", w1); break;
        case 1: s = std::format("timer state {} data {}", w2, rd<int32_t>(nt, 0x30)); break;
        case 2: s = std::format("evalexits state {}", w0); break;
        case 3: s = std::format("traversefrom node {}", w0); break;
        case 4: s = std::format("setvar inst {} var {:x} = {}", (int16_t)w0, w2, prim_text(nt + 0x40)); break;
        case 5: s = std::format("setvararray inst {} var {:x}", (int16_t)w0, w2); break;
        case 6: s = "stopinstance"; break;
        case 7: s = std::format("button {} down {}", rd<int32_t>(nt, 0x30), rd<uint8_t>(nt, 0x34)); break;
        case 8: s = std::format("depchanged state {}", w0); break;
        default: s = std::format("type {}", type);
        }
        s += std::format(" flags {:x}", rd<uint8_t>(nt, 0x28));
        record("notif", inst, 0, type, rd<int64_t>(nt, 0x18), rd<int64_t>(nt, 0x30), std::move(s));
    }
    bool r = o_exec_notif(inst, nt); // frees the notification (or re-queues it on a deferred begin)
    if (t && !r) record("notif_requeued", inst, 0);
    return r;
}

static uint64_t(__fastcall* o_begin)(uintptr_t, int64_t);
static uint64_t __fastcall h_begin(uintptr_t st, int64_t off) {
    uintptr_t inst = rd<uintptr_t>(st, 0x78);
    if (traced(inst))
        record((rd<uint32_t>(st, 0x40) & 1) ? "reenter" : "begin", inst, 0, rd<int>(st, 0x70), state_node(st), off);
    return o_begin(st, off);
}

static void(__fastcall* o_finish)(uintptr_t);
static void __fastcall h_finish(uintptr_t st) {
    uintptr_t inst = rd<uintptr_t>(st, 0x78);
    uint32_t f = rd<uint32_t>(st, 0x40);
    bool t = traced(inst) && (f & 1) && !(f & 2);
    if (t) record("finish", inst, 0, rd<int>(st, 0x70), state_node(st));
    o_finish(st);
    if (t) record("finish_done", inst, 0, rd<int>(st, 0x70));
}

static void(__fastcall* o_abort)(uintptr_t);
static void __fastcall h_abort(uintptr_t st) {
    uintptr_t inst = rd<uintptr_t>(st, 0x78);
    uint32_t f = rd<uint32_t>(st, 0x40);
    bool t = traced(inst) && (f & 1) && !(f & 2);
    if (t) record("abort", inst, 0, rd<int>(st, 0x70), state_node(st));
    o_abort(st);
    if (t) record("abort_done", inst, 0, rd<int>(st, 0x70));
}

static uint8_t(__fastcall* o_traverse)(uintptr_t, uintptr_t, uintptr_t, int64_t, uintptr_t, int32_t);
static uint8_t __fastcall h_traverse(uintptr_t src, uintptr_t inst, uintptr_t node, int64_t plug_off, uintptr_t in_plug,
                                     int32_t idx) {
    bool t = traced(inst);
    int64_t target = -1;
    if (t) {
        uintptr_t tn = in_plug ? rd<uintptr_t>(in_plug, 0x18) : 0;
        target = tn ? rd<int>(tn, 0x68) : -1;
        // a = source node (m_nodes idx), b = source plug offset in the STU node (| idx << 32), c = target node
        record("link", inst, 0, node ? rd<int>(node, 0x68) : -1, (plug_off & 0xFFFFFFFF) | ((int64_t)idx << 32), target);
    }
    uint8_t r = o_traverse(src, inst, node, plug_off, in_plug, idx);
    if (t) record("link_done", inst, 0, target, r);
    return r;
}

static void(__fastcall* o_instantiate)(uintptr_t);
static void __fastcall h_instantiate(uintptr_t inst) {
    bool t = traced(inst);
    if (t) record("instantiate", inst, 0, (int64_t)inst_guid(inst), rd<int16_t>(inst, 0x1A), rd<uint32_t>(inst, 0x258));
    o_instantiate(inst);
    if (t) {
        record("instantiate_done", inst, 0, rd<uint32_t>(inst, 0x258), rd<int>(inst, 0xF8));
        { std::lock_guard lk(g_mtx); g_vars.erase(inst); }
        diff_vars(inst, rd<uintptr_t>(inst, 0x98), "inst", inst, 0);
    }
}

static int(__fastcall* o_msg)(uintptr_t, uintptr_t, uint32_t);
static int __fastcall h_msg(uintptr_t inst, uintptr_t rec, uint32_t p3) {
    int r = o_msg(inst, rec, p3);
    if (traced(inst))
        record("message", inst, 0, rd<int64_t>(rec, 0x20), r,
               ((int64_t)rd<uint32_t>(rec, 0x214) << 32) | rd<uint32_t>(rec, 0x210));
    return r;
}

static bool hook(uintptr_t rva, void* detour, void** orig) {
    auto target = (void*)(globals::gameBase + rva);
    if (MH_CreateHook(target, detour, orig) != MH_OK) return false;
    return MH_EnableHook(target) == MH_OK;
}

static bool install_hooks() {
    if (g_hooks_installed) return true;
    bool ok = true;
    ok &= hook(RVA_CF_LOOP, (void*)&h_cf_loop, (void**)&o_cf_loop);
    ok &= hook(RVA_EXEC_NOTIF, (void*)&h_exec_notif, (void**)&o_exec_notif);
    ok &= hook(RVA_BEGIN, (void*)&h_begin, (void**)&o_begin);
    ok &= hook(RVA_FINISH, (void*)&h_finish, (void**)&o_finish);
    ok &= hook(RVA_ABORT, (void*)&h_abort, (void**)&o_abort);
    ok &= hook(RVA_TRAVERSE, (void*)&h_traverse, (void**)&o_traverse);
    ok &= hook(RVA_INSTANTIATE, (void*)&h_instantiate, (void**)&o_instantiate);
    ok &= hook(RVA_MSG_DELIVER, (void*)&h_msg, (void**)&o_msg);
    g_hooks_installed = ok;
    return ok;
}

// --- control ---------------------------------------------------------------

static std::string event_line(const Event& e) {
    std::string j = std::format("{{\"seq\":{},\"kind\":\"{}\",\"cf\":{},\"ms\":{},\"ent\":\"0x{:x}\",\"inst\":{},\"a\":{},\"b\":{},\"c\":{}",
                                e.seq, e.kind, e.cf, e.ms, e.ent, e.inst, e.a, e.b, e.c);
    if (!e.s.empty()) {
        j += ",\"s\":\"";
        for (char ch : e.s) { if (ch == '"' || ch == '\\') j += '\\'; if ((unsigned char)ch >= 0x20) j += ch; }
        j += "\"";
    }
    return j + "}";
}

static uint64_t parse_u64(const nlohmann::json& v) {
    if (v.is_number()) return v.get<uint64_t>();
    if (v.is_string()) return std::stoull(v.get<std::string>(), nullptr, 0);
    return 0;
}

nlohmann::json control(const nlohmann::json& args) {
    std::string action = args.value("action", "status");
    if (action == "start") {
        if (!install_hooks()) return { {"error", "hook install failed"} };
        std::lock_guard lk(g_mtx);
        g_graphs.clear(); g_ents.clear(); g_vars.clear();
        if (args.contains("graphs")) for (auto& g : args["graphs"]) g_graphs.insert(parse_u64(g));
        if (args.contains("entities")) for (auto& e : args["entities"]) g_ents.insert((uint32_t)parse_u64(e));
        g_all = args.value("all", false);
        if (args.contains("cap")) g_cap = args["cap"].get<size_t>();
        g_enabled = true;
    } else if (action == "stop") {
        g_enabled = false;
    } else if (action == "clear") {
        std::lock_guard lk(g_mtx);
        g_events.clear(); g_dropped = 0;
    } else if (action == "fetch") {
        uint64_t since = args.value("since", (uint64_t)0);
        size_t limit = args.value("limit", (size_t)500);
        nlohmann::json arr = nlohmann::json::array();
        uint64_t last = since;
        std::lock_guard lk(g_mtx);
        for (auto& e : g_events) {
            if (e.seq <= since) continue;
            if (arr.size() >= limit) break;
            last = e.seq;
            arr.push_back(nlohmann::json::parse(event_line(e)));
        }
        return { {"events", arr}, {"last_seq", last}, {"total", g_seq} };
    } else if (action == "save") {
        std::string path = args.value("path", "");
        uint64_t since = args.value("since", (uint64_t)0);
        FILE* f = fopen(path.c_str(), "wb");
        if (!f) return { {"error", "cannot open " + path} };
        std::lock_guard lk(g_mtx);
        uint64_t n = 0, last = since;
        for (auto& e : g_events) {
            if (e.seq <= since) continue;
            auto line = event_line(e);
            fputs(line.c_str(), f); fputc('\n', f);
            n++; last = e.seq;
        }
        fclose(f);
        return { {"saved", n}, {"last_seq", last}, {"path", path} };
    } else if (action != "status") {
        return { {"error", "action must be start|stop|clear|status|fetch|save"} };
    }
    std::lock_guard lk(g_mtx);
    nlohmann::json graphs = nlohmann::json::array();
    for (auto g : g_graphs) graphs.push_back(std::format("0x{:016x}", g));
    return { {"enabled", g_enabled}, {"hooks_installed", g_hooks_installed}, {"events", g_events.size()},
             {"total", g_seq}, {"dropped", g_dropped}, {"graphs", graphs}, {"entities", g_ents.size()}, {"all", g_all} };
}

} // namespace ss_trace
