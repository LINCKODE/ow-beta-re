#include "EcsTrace.h"
#include "../globals.h"
#include "../entity_admin.h"
#include <windows.h>
#include <MinHook.h>
#include <mutex>
#include <vector>
#include <unordered_map>
#include <format>

namespace ecs_trace {

// Engine functions (RVAs; Ghidra = 0x7ff64ffe0000 + RVA). See docs/ECS.md.
static constexpr uintptr_t RVA_TICK          = 0x8115d0; // EntityAdmin::Tick(ea, float dt)
static constexpr uintptr_t RVA_SPAWN         = 0x80f7c0; // Entity* Spawn(ea, u32* id, EntityLoader*)
static constexpr uintptr_t RVA_ADD_COMPONENT = 0x829a30; // Component* AddComponent(Entity*, u8* id)
static constexpr uintptr_t RVA_DELETE        = 0x80ff50; // DeleteRequest(ea, Entity*, bool immediate)
static constexpr uintptr_t RVA_DESTROY       = 0x810880; // Destroy(ea, Entity*)
static constexpr uintptr_t RVA_FLUSH_CREATED = 0x810600; // FlushCreated(ea)
static constexpr uintptr_t RVA_FLUSH_DESTROY = 0x810d30; // FlushDestroy(ea)
static constexpr uintptr_t RVA_SPAWN_INLINE  = 0x810030; // Entity* SpawnInline(ea, u8* comps, u32 count, EntityLoader*)
static constexpr uintptr_t RVA_CLOCK_DILATE  = 0xd063a0;  // ClockDilate(clock, float dt): dt -> µs (dilation flags clock+0xa8)
static constexpr uintptr_t RVA_CLOCK_ADVANCE = 0x102e950; // ClockAdvance(clock, i64 µs): accumulate, push pending CFs
static constexpr uintptr_t RVA_CLOCK_RESYNC  = 0xd064a0;  // ClockResync(clock, u32 cf): set CF, renumber pending
static constexpr uintptr_t RVA_CF_RANGE      = 0x100ea30; // RunCommandFrames(sys, first, last)
static constexpr uintptr_t SESSION_GLOBAL    = 0x17b7fc0; // session object; its EA at +0xB8

enum Phase : int { PH_NONE, PH_FLUSH_DESTROY, PH_FLUSH_CREATED, PH_SYSTEM_TICK, PH_TICK_OTHER,
                   PH_CF_LISTENER, PH_CF_PRE, PH_CF_POST };
static const char* kPhaseNames[] = { "none", "flush_destroy", "flush_created", "system_tick", "tick",
                                     "cf_listener", "cf_pre", "cf_post" };

struct Event {
    uint64_t seq;
    uint32_t tid;
    uintptr_t ea;
    uint64_t tick;
    int phase;
    int phase_sys;   // system index (system_tick) or listener index (cf_*)
    uint32_t phase_cf; // command frame when phase == cf_listener
    uintptr_t phase_ea; // EA whose tick/flush/system the phase belongs to (systems can act on other EAs)
    const char* kind;
    int sys;         // system index for system callbacks, else -1
    uint32_t ent;
    uint32_t parent; // id of the entity's parent at the time of the event (0 = none)
    uint64_t a, b;   // kind-specific
    std::vector<uint8_t> comps;
};

static std::mutex g_mtx;
static std::vector<Event> g_events;
static uint64_t g_seq = 0;
static size_t g_cap = 200000;
static bool g_enabled = false;
static bool g_hooks_installed = false;
static bool g_record_ticks = false;
static uint64_t g_dropped = 0;
static bool g_watch_parents = false;
static bool g_record_cf = false; // clock + command-frame listener events (high volume)

struct PhaseState { int phase = PH_NONE; int sys = -1; uintptr_t ea = 0; uint32_t cf = 0; };
static thread_local PhaseState t_phase;

// --- helpers --------------------------------------------------------------

static uintptr_t session_ea() {
    auto s = *(uintptr_t*)(globals::gameBase + SESSION_GLOBAL);
    return s ? *(uintptr_t*)(s + 0xb8) : 0;
}

static std::string ea_name(uintptr_t ea) {
    if (ea == (uintptr_t)GameEntityAdmin()) return "game";
    if (ea == (uintptr_t)LobbyEntityAdmin()) return "lobby";
    if (ea && ea == session_ea()) return "session";
    return std::format("0x{:x}", ea);
}

static int system_index(uintptr_t ea, uintptr_t sys) {
    if (!ea) return -1;
    auto arr = *(uintptr_t**)(ea + 0xd8);
    int n = *(int*)(ea + 0xe0);
    for (int i = 0; i < n; i++) if (arr[i] == sys) return i;
    return -1;
}

static uint32_t ent_id(void* e) { return e ? *(uint32_t*)((uintptr_t)e + 0x94) : 0; }
static uintptr_t ent_ea(void* e) { return e ? *(uintptr_t*)((uintptr_t)e + 0x28) : 0; }

static std::vector<uint8_t> ent_comps(void* e) {
    std::vector<uint8_t> out;
    if (!e) return out;
    auto list = (uintptr_t*)*(uintptr_t*)((uintptr_t)e + 0x50);
    int n = *(int*)((uintptr_t)e + 0x58);
    for (int i = 0; list && i < n && i < 256; i++)
        if (list[i]) out.push_back(*(uint8_t*)(list[i] + 0x10));
    return out;
}

static void record(const char* kind, uintptr_t ea, int sys, void* ent, uint64_t a = 0, uint64_t b = 0,
                   bool with_comps = false) {
    if (!g_enabled) return;
    Event ev{};
    ev.tid = GetCurrentThreadId();
    ev.ea = ea;
    ev.tick = ea ? *(uint64_t*)(ea + 0xc8) : 0;
    ev.phase = t_phase.phase;
    ev.phase_sys = t_phase.sys;
    ev.phase_ea = t_phase.ea;
    ev.phase_cf = t_phase.cf;
    ev.kind = kind;
    ev.sys = sys;
    ev.ent = ent_id(ent);
    ev.parent = ent ? ent_id(*(void**)ent) : 0;
    ev.a = a;
    ev.b = b;
    if (with_comps) ev.comps = ent_comps(ent);
    std::lock_guard lk(g_mtx);
    if (g_events.size() >= g_cap) { g_dropped++; return; }
    ev.seq = ++g_seq;
    g_events.push_back(std::move(ev));
}

struct PhaseScope {
    PhaseState saved;
    PhaseScope(int ph, uintptr_t ea, int sys = -1, uint32_t cf = 0) : saved(t_phase) {
        t_phase.phase = ph; t_phase.sys = sys; t_phase.ea = ea; t_phase.cf = cf;
    }
    ~PhaseScope() { t_phase = saved; }
};

// --- per-system vtable copies ---------------------------------------------

static constexpr int VT_COPY_SLOTS = 64;
static std::mutex g_vt_mtx;
static std::unordered_map<uintptr_t, uintptr_t*> g_orig_vt; // system instance -> original vtable

static uintptr_t* orig_vt(void* sys) {
    std::lock_guard lk(g_vt_mtx);
    auto it = g_orig_vt.find((uintptr_t)sys);
    return it != g_orig_vt.end() ? it->second : nullptr;
}

template <int Slot>
static void __fastcall sys_entity_cb(void* sys, void* ent) {
    static const char* kNames[] = { "on_added", "on_created", "on_deleting", "on_removed" };
    auto vt = orig_vt(sys);
    uintptr_t ea = *(uintptr_t*)((uintptr_t)sys + 8);
    record(kNames[Slot - 9], ea, system_index(ea, (uintptr_t)sys), ent);
    ((void(__fastcall*)(void*, void*))vt[Slot])(sys, ent);
}

// Parent watch: entity -> parent entity, per EA, compared around every system OnTick. A change
// on an entity that already existed = reparenting outside Spawn (recorded as parent_changed).
static std::unordered_map<uintptr_t, std::unordered_map<uintptr_t, uintptr_t>> g_parents;

static std::unordered_map<uintptr_t, uintptr_t> snapshot_parents(uintptr_t ea) {
    std::unordered_map<uintptr_t, uintptr_t> m;
    auto buckets = *(teList<EntityIntermediate>**)(ea + 0x10);
    for (int b = 0; b < ENTITYGROUP_MAX; b++)
        for (int i = 0; i < buckets[b].num; i++)
            if (auto e = (uintptr_t)buckets[b].ptr[i].entity) m[e] = *(uintptr_t*)e;
    return m;
}

static void check_parents(uintptr_t ea, int sys) {
    auto now = snapshot_parents(ea);
    auto& before = g_parents[ea];
    for (auto& [e, parent] : now) {
        auto it = before.find(e);
        if (it != before.end() && it->second != parent)
            record("parent_changed", ea, sys, (void*)e, it->second ? ent_id((void*)it->second) : 0,
                   parent ? ent_id((void*)parent) : 0);
    }
    before = std::move(now);
}

static void __fastcall sys_tick_cb(void* sys, float dt) {
    auto vt = orig_vt(sys);
    uintptr_t ea = *(uintptr_t*)((uintptr_t)sys + 8);
    int idx = system_index(ea, (uintptr_t)sys);
    PhaseScope ps(PH_SYSTEM_TICK, ea, idx);
    if (g_enabled && g_watch_parents) {
        for (auto e : { (uintptr_t)GameEntityAdmin(), (uintptr_t)LobbyEntityAdmin(), session_ea() })
            if (e) check_parents(e, -1); // changes made between systems (outside any OnTick)
    }
    ((void(__fastcall*)(void*, float))vt[13])(sys, dt);
    if (g_enabled && g_watch_parents) {
        for (auto e : { (uintptr_t)GameEntityAdmin(), (uintptr_t)LobbyEntityAdmin(), session_ea() })
            if (e) check_parents(e, idx);  // this system (of `ea`) changed a parent in some EA
    }
}

uintptr_t* original_vtable(void* sys) {
    auto vt = orig_vt(sys);
    return vt ? vt : *(uintptr_t**)sys;
}

// Give every game-module system of this EA a private vtable copy with slots 9-13 redirected.
// Copies are never freed or reverted: with recording off they behave exactly like the original.
static void instrument_systems(uintptr_t ea) {
    auto arr = *(uintptr_t**)(ea + 0xd8);
    int n = *(int*)(ea + 0xe0);
    for (int i = 0; i < n; i++) {
        auto sys = (uintptr_t*)arr[i];
        if (!sys) continue;
        auto vt = (uintptr_t*)*sys;
        if ((uintptr_t)vt - globals::gameBase >= globals::gameSize) continue; // ours or already swapped
        {
            std::lock_guard lk(g_vt_mtx);
            if (g_orig_vt.count((uintptr_t)sys)) continue;
            g_orig_vt[(uintptr_t)sys] = vt;
        }
        auto copy = new uintptr_t[VT_COPY_SLOTS];
        for (int s = 0; s < VT_COPY_SLOTS; s++)
            copy[s] = IsBadReadPtr(&vt[s], 8) ? 0 : vt[s];
        copy[9] = (uintptr_t)&sys_entity_cb<9>;
        copy[10] = (uintptr_t)&sys_entity_cb<10>;
        copy[11] = (uintptr_t)&sys_entity_cb<11>;
        copy[12] = (uintptr_t)&sys_entity_cb<12>;
        copy[13] = (uintptr_t)&sys_tick_cb;
        InterlockedExchangePointer((PVOID*)sys, copy);
    }
}

// --- command-frame listeners (EA+0xF0): per-instance vtable copies, slots 0-2 ----------------

struct ListenerInfo { uintptr_t* vt; uintptr_t ea; };
static std::unordered_map<uintptr_t, ListenerInfo> g_listeners;

static int listener_index(uintptr_t ea, uintptr_t l) {
    auto arr = *(uintptr_t**)(ea + 0xf0);
    int n = *(int*)(ea + 0xf8);
    for (int i = 0; i < n; i++) if (arr[i] == l) return i;
    return -1;
}

static ListenerInfo listener_info(void* l) {
    std::lock_guard lk(g_vt_mtx);
    return g_listeners[(uintptr_t)l];
}

// Generic pass-through (up to 3 args after this): slot 0 = OnCommandFrame(cf, flags), 1 = pre, 2 = post.
template <int Slot>
static uint64_t __fastcall listener_cb(void* l, uint64_t a1, uint64_t a2, uint64_t a3) {
    static const char* kNames[] = { "cf_listener", "cf_pre", "cf_post" };
    auto info = listener_info(l);
    int idx = listener_index(info.ea, (uintptr_t)l);
    if (!g_record_cf) {
    } else if (Slot == 0) record(kNames[Slot], info.ea, idx, nullptr, (uint32_t)a1, (uint32_t)a2);
    else record(kNames[Slot], info.ea, idx, nullptr);
    // Work done inside the listener is attributed to it (and to the CF for OnCommandFrame).
    static const int kPhases[] = { PH_CF_LISTENER, PH_CF_PRE, PH_CF_POST };
    PhaseScope ps(kPhases[Slot], info.ea, idx, Slot == 0 ? (uint32_t)a1 : 0);
    return ((uint64_t(__fastcall*)(void*, uint64_t, uint64_t, uint64_t))info.vt[Slot])(l, a1, a2, a3);
}

static void instrument_listeners(uintptr_t ea) {
    auto arr = *(uintptr_t**)(ea + 0xf0);
    int n = *(int*)(ea + 0xf8);
    for (int i = 0; i < n; i++) {
        auto l = (uintptr_t*)arr[i];
        if (!l) continue;
        auto vt = (uintptr_t*)*l;
        if ((uintptr_t)vt - globals::gameBase >= globals::gameSize) continue; // already swapped
        {
            std::lock_guard lk(g_vt_mtx);
            if (g_listeners.count((uintptr_t)l)) continue;
            g_listeners[(uintptr_t)l] = { vt, ea };
        }
        auto copy = new uintptr_t[16];
        for (int k = 0; k < 16; k++) copy[k] = IsBadReadPtr(&vt[k], 8) ? 0 : vt[k];
        copy[0] = (uintptr_t)&listener_cb<0>;
        copy[1] = (uintptr_t)&listener_cb<1>;
        copy[2] = (uintptr_t)&listener_cb<2>;
        InterlockedExchangePointer((PVOID*)l, copy);
    }
}

// The EA a netcode clock (timing+0x50) belongs to.
static uintptr_t clock_ea(uintptr_t clock) {
    for (auto ea : { (uintptr_t)GameEntityAdmin(), (uintptr_t)LobbyEntityAdmin(), session_ea() })
        if (ea && *(uintptr_t*)(ea + 0x218) && *(uintptr_t*)(ea + 0x218) + 0x50 == clock) return ea;
    return 0;
}

// --- engine hooks -----------------------------------------------------------

static void(__fastcall* o_tick)(uintptr_t, float);
static void __fastcall h_tick(uintptr_t ea, float dt) {
    if (g_enabled) {
        instrument_systems(ea);
        instrument_listeners(ea);
        if (g_record_ticks) record("tick", ea, -1, nullptr, *(uint32_t*)&dt);
    }
    PhaseScope ps(PH_TICK_OTHER, ea);
    o_tick(ea, dt);
}

static void(__fastcall* o_clock_dilate)(uintptr_t, float);
static void __fastcall h_clock_dilate(uintptr_t clock, float dt) {
    record("clock_dilate", clock_ea(clock), -1, nullptr, *(uint32_t*)&dt, *(uint8_t*)(clock + 0xa8));
    o_clock_dilate(clock, dt);
}

static void(__fastcall* o_clock_advance)(uintptr_t, int64_t);
static void __fastcall h_clock_advance(uintptr_t clock, int64_t us) {
    // a = µs requested, b = CF before (clock+0x10) | accumulator before (clock+0x00) << 32
    record("clock_advance", clock_ea(clock), -1, nullptr, (uint64_t)us,
           (uint64_t)*(uint32_t*)(clock + 0x10) | ((uint64_t)*(uint32_t*)clock << 32));
    o_clock_advance(clock, us);
}

static void(__fastcall* o_clock_resync)(uintptr_t, uint32_t);
static void __fastcall h_clock_resync(uintptr_t clock, uint32_t cf) {
    record("clock_resync", clock_ea(clock), -1, nullptr, cf, *(uint32_t*)(clock + 0x10));
    o_clock_resync(clock, cf);
}

static int(__fastcall* o_cf_range)(uintptr_t, uint32_t, uint32_t);
static int __fastcall h_cf_range(uintptr_t sys, uint32_t first, uint32_t last) {
    record("cf_range", *(uintptr_t*)(sys + 8), -1, nullptr, first, last);
    return o_cf_range(sys, first, last);
}

static void*(__fastcall* o_spawn)(uintptr_t, uint32_t*, void*);
static void* __fastcall h_spawn(uintptr_t ea, uint32_t* id, void* loader) {
    record("spawn_begin", ea, -1, nullptr, id ? *id : 0, (uintptr_t)loader);
    void* e = o_spawn(ea, id, loader);
    record("spawn_end", ea, -1, e, id ? *id : 0, (uintptr_t)loader, true);
    return e;
}

static void*(__fastcall* o_add_component)(void*, uint8_t*);
static void* __fastcall h_add_component(void* ent, uint8_t* id) {
    void* c = o_add_component(ent, id);
    record("add_component", ent_ea(ent), -1, ent, *id, c != nullptr);
    return c;
}

static void(__fastcall* o_delete)(uintptr_t, void*, char);
static void __fastcall h_delete(uintptr_t ea, void* ent, char immediate) {
    record("delete_request", ea, -1, ent, (uint8_t)immediate, *(uint32_t*)((uintptr_t)ent + 0xb4));
    o_delete(ea, ent, immediate);
    record("delete_request_end", ea, -1, ent, 0, *(uint32_t*)((uintptr_t)ent + 0xb4));
}

static void(__fastcall* o_destroy)(uintptr_t, void*);
static void __fastcall h_destroy(uintptr_t ea, void* ent) {
    record("destroy", ea, -1, ent, 0, *(uint32_t*)((uintptr_t)ent + 0xb4), true);
    o_destroy(ea, ent);
}

// Lightweight entity: entity + components in one allocation, no dependencies/callbacks/queues.
static void*(__fastcall* o_spawn_inline)(uintptr_t, uint8_t*, uint32_t, void*);
static void* __fastcall h_spawn_inline(uintptr_t ea, uint8_t* comps, uint32_t count, void* loader) {
    record("spawn_inline_begin", ea, -1, nullptr, count, (uintptr_t)loader);
    void* e = o_spawn_inline(ea, comps, count, loader);
    record("spawn_inline_end", ea, -1, e, count, e ? *(uint32_t*)((uintptr_t)e + 0xb4) : 0, true);
    return e;
}

static void(__fastcall* o_flush_created)(uintptr_t);
static void __fastcall h_flush_created(uintptr_t ea) {
    int n = *(int*)(ea + 0x98);
    if (n) record("flush_created", ea, -1, nullptr, n);
    PhaseScope ps(PH_FLUSH_CREATED, ea);
    o_flush_created(ea);
}

static void(__fastcall* o_flush_destroy)(uintptr_t);
static void __fastcall h_flush_destroy(uintptr_t ea) {
    int req = *(int*)(ea + 0x50), pend = *(int*)(ea + 0x78);
    if (req || pend) record("flush_destroy", ea, -1, nullptr, req, pend);
    PhaseScope ps(PH_FLUSH_DESTROY, ea);
    o_flush_destroy(ea);
}

static bool hook(uintptr_t rva, void* detour, void** orig) {
    auto target = (void*)(globals::gameBase + rva);
    if (MH_CreateHook(target, detour, orig) != MH_OK) return false;
    return MH_EnableHook(target) == MH_OK;
}

static bool install_hooks() {
    if (g_hooks_installed) return true;
    bool ok = true;
    ok &= hook(RVA_TICK, (void*)&h_tick, (void**)&o_tick);
    ok &= hook(RVA_SPAWN, (void*)&h_spawn, (void**)&o_spawn);
    ok &= hook(RVA_ADD_COMPONENT, (void*)&h_add_component, (void**)&o_add_component);
    ok &= hook(RVA_DELETE, (void*)&h_delete, (void**)&o_delete);
    ok &= hook(RVA_DESTROY, (void*)&h_destroy, (void**)&o_destroy);
    ok &= hook(RVA_CLOCK_DILATE, (void*)&h_clock_dilate, (void**)&o_clock_dilate);
    ok &= hook(RVA_CLOCK_ADVANCE, (void*)&h_clock_advance, (void**)&o_clock_advance);
    ok &= hook(RVA_CLOCK_RESYNC, (void*)&h_clock_resync, (void**)&o_clock_resync);
    ok &= hook(RVA_CF_RANGE, (void*)&h_cf_range, (void**)&o_cf_range);
    ok &= hook(RVA_SPAWN_INLINE, (void*)&h_spawn_inline, (void**)&o_spawn_inline);
    ok &= hook(RVA_FLUSH_CREATED, (void*)&h_flush_created, (void**)&o_flush_created);
    ok &= hook(RVA_FLUSH_DESTROY, (void*)&h_flush_destroy, (void**)&o_flush_destroy);
    g_hooks_installed = ok;
    return ok;
}

// --- control ----------------------------------------------------------------

static nlohmann::json event_json(const Event& e) {
    nlohmann::json j = {
        {"seq", e.seq}, {"tid", e.tid}, {"ea", ea_name(e.ea)}, {"tick", e.tick},
        {"phase", kPhaseNames[e.phase]}, {"kind", e.kind},
    };
    if (e.phase == PH_SYSTEM_TICK || e.phase >= PH_CF_LISTENER) j["phase_sys"] = e.phase_sys;
    if (e.phase == PH_CF_LISTENER) j["phase_cf"] = e.phase_cf;
    if (e.phase != PH_NONE && e.phase_ea != e.ea) j["phase_ea"] = ea_name(e.phase_ea);
    if (e.sys >= 0) j["sys"] = e.sys;
    if (e.ent) j["ent"] = std::format("0x{:x}", e.ent);
    if (e.parent) j["parent"] = std::format("0x{:x}", e.parent);
    if (e.a || e.b) { j["a"] = std::format("0x{:x}", e.a); j["b"] = std::format("0x{:x}", e.b); }
    if (!e.comps.empty()) {
        std::string s;
        for (auto c : e.comps) s += std::format("{}{:02x}", s.empty() ? "" : ",", c);
        j["comps"] = s;
    }
    return j;
}

nlohmann::json control(const nlohmann::json& args) {
    std::string action = args.value("action", "status");
    if (action == "start") {
        if (!install_hooks()) return { {"error", "hook install failed"} };
        g_record_ticks = args.value("ticks", false);
        g_watch_parents = args.value("watch_parents", false);
        g_record_cf = args.value("cf", false);
        g_parents.clear();
        if (args.contains("cap")) g_cap = args["cap"].get<size_t>();
        for (auto ea : { (uintptr_t)GameEntityAdmin(), (uintptr_t)LobbyEntityAdmin(), session_ea() })
            if (ea) { instrument_systems(ea); instrument_listeners(ea); }
        g_enabled = true;
        // Replay needs each EA's starting point: a = entity id counter (EA+0xC0), b = entity count.
        for (auto ea : { (uintptr_t)GameEntityAdmin(), (uintptr_t)LobbyEntityAdmin(), session_ea() })
            if (ea) record("trace_start", ea, -1, nullptr, *(uint32_t*)(ea + 0xc0), *(uint32_t*)(ea + 0xc));
    } else if (action == "stop") {
        g_enabled = false;
    } else if (action == "clear") {
        std::lock_guard lk(g_mtx);
        g_events.clear();
        g_dropped = 0;
    } else if (action == "fetch") {
        uint64_t since = args.value("since", (uint64_t)0);
        size_t limit = args.value("limit", (size_t)2000);
        std::string kind = args.value("kind", "");
        nlohmann::json arr = nlohmann::json::array();
        uint64_t last = since;
        std::lock_guard lk(g_mtx);
        for (auto& e : g_events) {
            if (e.seq <= since) continue;
            if (arr.size() >= limit) break;
            last = e.seq;
            if (!kind.empty() && kind != e.kind) continue;
            arr.push_back(event_json(e));
        }
        return { {"events", arr}, {"last_seq", last}, {"total", g_seq} };
    } else if (action == "save") {
        // Write events after `since` straight to a file (JSON lines, same fields as fetch): no
        // HTTP transfer, so the UI thread is blocked only for the file write.
        std::string path = args.value("path", "");
        uint64_t since = args.value("since", (uint64_t)0);
        FILE* f = fopen(path.c_str(), "wb");
        if (!f) return { {"error", "cannot open " + path} };
        std::lock_guard lk(g_mtx);
        uint64_t n = 0, last = since;
        std::unordered_map<uintptr_t, std::string> names;
        for (auto& e : g_events) {
            if (e.seq <= since) continue;
            auto it = names.find(e.ea);
            if (it == names.end()) it = names.emplace(e.ea, ea_name(e.ea)).first;
            fprintf(f, "{\"seq\":%llu,\"tid\":%u,\"ea\":\"%s\",\"tick\":%llu,\"phase\":\"%s\",\"kind\":\"%s\"",
                    (unsigned long long)e.seq, e.tid, it->second.c_str(), (unsigned long long)e.tick, kPhaseNames[e.phase], e.kind);
            if (e.phase == PH_SYSTEM_TICK || e.phase >= PH_CF_LISTENER) fprintf(f, ",\"phase_sys\":%d", e.phase_sys);
            if (e.phase == PH_CF_LISTENER) fprintf(f, ",\"phase_cf\":%u", e.phase_cf);
            if (e.phase != PH_NONE && e.phase_ea != e.ea) {
                auto pit = names.find(e.phase_ea);
                if (pit == names.end()) pit = names.emplace(e.phase_ea, ea_name(e.phase_ea)).first;
                fprintf(f, ",\"phase_ea\":\"%s\"", pit->second.c_str());
            }
            if (e.sys >= 0) fprintf(f, ",\"sys\":%d", e.sys);
            if (e.ent) fprintf(f, ",\"ent\":\"0x%x\"", e.ent);
            if (e.parent) fprintf(f, ",\"parent\":\"0x%x\"", e.parent);
            if (e.a || e.b) fprintf(f, ",\"a\":\"0x%llx\",\"b\":\"0x%llx\"", (unsigned long long)e.a, (unsigned long long)e.b);
            if (!e.comps.empty()) {
                fputs(",\"comps\":\"", f);
                for (size_t i = 0; i < e.comps.size(); i++) fprintf(f, i ? ",%02x" : "%02x", e.comps[i]);
                fputc('"', f);
            }
            fputs("}\n", f);
            n++;
            last = e.seq;
        }
        fclose(f);
        return { {"saved", n}, {"last_seq", last}, {"path", path} };
    } else if (action != "status") {
        return { {"error", "action must be start|stop|clear|status|fetch|save"} };
    }
    std::lock_guard lk(g_mtx);
    return { {"enabled", g_enabled}, {"hooks_installed", g_hooks_installed}, {"events", g_events.size()},
             {"total", g_seq}, {"dropped", g_dropped}, {"instrumented_systems", g_orig_vt.size()} };
}

} // namespace ecs_trace
