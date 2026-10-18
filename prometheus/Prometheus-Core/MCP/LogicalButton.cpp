#include "LogicalButton.h"
#include "../globals.h"
#include "../game.h"
#include "../entity_admin.h"
#include "../Statescript.h"
#include "../Utility/SafeMemory.h"
#include <windows.h>
#include <MinHook.h>
#include <atomic>
#include <format>
#include <string>

namespace logical_button {

// Engine (RVAs; Ghidra = 0x7ff64ffe0000 + RVA).
// bool IsLogicalButtonHeld(input comp 0x50, int button) (0xcec330): tick = comp+0x40 (0/1);
//   comp[tick*0xC3 + 0x2CE + button] (went down this frame) || comp[tick*0xC3 + 0x148 + button] (held).
// Callers: LocalPlayerInputSystem's samplers 0xc9e580 (command button mask, comp 0x2f +0x147a) and 0xc9e6f0
// (movement axes), GameHUDSystem, replay controls, UI.
// LocalPlayerInputSystem tick (0xc9df40): samples nothing while the input manager (*0x18a6688) +0x95a says
// input is captured (game window unfocused, UI) unless comp 0x50 +0x5e5 is set; 0xc9ece0 (inside the tick)
// recomputes +0x95a.
// Action code (comp 0x2f +0x147c, 0xc9e0e0): the first entry of the table at 0x173e680 ({u8 code, i32 button} x 45:
// equip_weapon_0..9, next/previous_weapon, reload, quick_weapon, weapon_view, emotes, spray, open_map, spectate_*,
// hero_select, help) whose button went down this frame (comp 0x50 [tick*0xC3 + 0x2CE + button], read directly).
static constexpr uintptr_t RVA_IS_HELD = 0xcec330, RVA_INPUT_TICK = 0xc9df40, RVA_INPUT_MGR = 0x18a6688,
                           RVA_ACTION_TABLE = 0x173e680;
static constexpr int kActionEntries = 45;
static constexpr int kButtons = 0xC3;

// Command button mask (comp 0x2f +0x147a, built by 0xc9e580): button id -> bit.
static const struct { int button; uint16_t bit; } kMask[] = {
    {0x5d, 0x0001}, {0x65, 0x0002}, {0x73, 0x0004}, {0x66, 0x0008}, {0x77, 0x0010}, {0x78, 0x0020},
    {0x79, 0x0040}, {0x7a, 0x0080}, {0x42, 0x0100}, {0x4c, 0x0200}, {0x7b, 0x0400}, {0x7c, 0x0800},
    {0x7d, 0x1000},
};
// Movement axes (0xc9e6f0): forward 0x3d/0x3e, back 0x3f, right 0x41, left 0x40.

// Injected held state per button: 0 = not injected, UINT64_MAX = until release, else GetTickCount64() deadline.
static std::atomic<uint64_t> g_until[kButtons];
static std::atomic<uint64_t> g_hits[kButtons]; // how often the hook answered true for an injected button
static std::atomic<bool> g_edge[kButtons];     // press not yet seen by an input tick (action-code buttons)
static bool g_hook_installed = false;

static bool injected(int b) {
    uint64_t u = g_until[b].load(std::memory_order_relaxed);
    if (!u) return false;
    if (u != UINT64_MAX && GetTickCount64() >= u) { g_until[b].store(0); return false; }
    return true;
}

static bool any_injected() {
    for (int b = 0; b < kButtons; b++) if (injected(b)) return true;
    return false;
}

// Replaces 0xcec330 (never calls the original: its integrity check faults when the return address is
// outside the game's code sections, and our detour would be that caller).
static std::atomic<uintptr_t> g_input_comp; // the input component the game passes (0xc4ae30's result)
static bool __fastcall h_is_held(uintptr_t comp, int b) {
    g_input_comp.store(comp, std::memory_order_relaxed);
    if (b >= 0 && b < kButtons && injected(b)) { g_hits[b]++; return true; }
    uintptr_t base = comp + (uintptr_t)(*(int*)(comp + 0x40) * kButtons);
    return *(char*)(base + 0x2CE + b) != 0 || *(char*)(base + 0x148 + b) != 0;
}

static int action_code(int id) {
    auto t = (const uint8_t*)(globals::gameBase + RVA_ACTION_TABLE);
    for (int i = 0; i < kActionEntries; i++)
        if (*(const int32_t*)(t + i * 8 + 4) == id) return t[i * 8];
    return 0;
}

// While a button is injected, the input system samples even if input is captured (unfocused window), so
// recordings run without the game in the foreground; the flag is restored right after the tick. An injected
// action-code button gets its went-down byte for the first tick of the press only (one edge, like a key).
static std::atomic<uint64_t> g_ticks, g_edges_applied;
static std::atomic<int> g_last_action = 0, g_last_captured = -1;
static void(__fastcall* o_input_tick)(uintptr_t);
static void set_is_held_hook(bool on);
static void __fastcall h_input_tick(uintptr_t sys) {
    uintptr_t mgr = *(uintptr_t*)(globals::gameBase + RVA_INPUT_MGR);
    if (!any_injected()) set_is_held_hook(false); // nothing injected: the game's own query again
    if (!mgr || !any_injected()) return o_input_tick(sys);
    // The system runs once per entity admin that has it; only the one holding the local player's comp 0x2f
    // (sys+8 = its EA) turns the edge into a command, so the edge stays pending until that tick ran.
    auto sea = *(EntityAdminBase**)(sys + 8);
    Entity* le = sea ? sea->getLocalEnt() : nullptr;
    auto lp = le ? le->getById(0x2f) : nullptr;
    auto input = (Component_50_Input*)g_input_comp.load();
    char* went_down = input ? (char*)input + 0x2CE + input->tick10 * kButtons : nullptr;
    int edges[kButtons], n = 0;
    char saved[kButtons];
    for (int b = 0; lp && went_down && b < kButtons; b++)
        if (g_edge[b] && injected(b)) { edges[n++] = b; saved[b] = went_down[b]; went_down[b] = 1; }
    // 0xc9ece0 (called inside the tick) recomputes +0x95a; comp 0x50 +0x5e5 is the tick's "sample anyway" gate.
    char captured = *(char*)(mgr + 0x95a);
    char force = input ? *((char*)input + 0x5e5) : 0;
    *(char*)(mgr + 0x95a) = 0;
    if (input) *((char*)input + 0x5e5) = 1;
    o_input_tick(sys);
    *(char*)(mgr + 0x95a) = captured;
    if (input) *((char*)input + 0x5e5) = force;
    for (int i = 0; i < n; i++) { went_down[edges[i]] = saved[edges[i]]; g_edge[edges[i]] = false; }
    g_ticks++; g_edges_applied += n; g_last_captured = captured;
    if (lp) { // diagnostics: action code | accumulated action << 8 | button mask << 16
        int v = *(uint8_t*)((char*)lp + 0x147c) | (*(uint8_t*)((char*)lp + 0x146c) << 8) |
                (*(uint16_t*)((char*)lp + 0x147a) << 16);
        if (v) g_last_action = v;
    }
}

static bool hook(uintptr_t rva, void* detour, void** orig) {
    auto target = (void*)(globals::gameBase + rva);
    MH_STATUS s = MH_CreateHook(target, detour, orig);
    if (s != MH_OK && s != MH_ERROR_ALREADY_CREATED) return false;
    s = MH_EnableHook(target);
    return s == MH_OK || s == MH_ERROR_ENABLED;
}

// The is-held replacement is enabled only while a button is injected: replacing the query for good broke the
// hero-select screen's hero preview (no model until the client restarted) [H, T051]. The input tick hook stays
// (it calls the original when nothing is injected) and turns the replacement off once no button is injected.
static bool g_is_held_on = false;
static void set_is_held_hook(bool on) {
    if (on == g_is_held_on) return;
    auto target = (void*)(globals::gameBase + RVA_IS_HELD);
    MH_STATUS s = on ? MH_EnableHook(target) : MH_DisableHook(target);
    if (s == MH_OK || s == (on ? MH_ERROR_ENABLED : MH_ERROR_DISABLED)) g_is_held_on = on;
}

static bool install_hook() {
    if (!g_hook_installed) {
        g_hook_installed = hook(RVA_IS_HELD, (void*)&h_is_held, nullptr) &&
                           hook(RVA_INPUT_TICK, (void*)&h_input_tick, (void**)&o_input_tick);
        if (g_hook_installed) g_is_held_on = true; // hook() just enabled it
    }
    // Later presses: the input tick may have disabled the replacement after a release (T062: held buttons
    // reached the command for one CF only), so enable it again.
    if (g_hook_installed) set_is_held_hook(true);
    return g_hook_installed;
}

static nlohmann::json err(const std::string& m) { return { {"error", m} }; }

static bool readable(const void* p, size_t n) { return Utility::SafeMemory::IsReadable(p, n); }

static const char* name_of(int id) {
    auto* e = LogicalButtonById(id);
    return (e->key_id == id && e->name && readable(e->name, 1)) ? e->name : "?";
}

static int mask_bit(int id) {
    for (auto& m : kMask) if (m.button == id) return m.bit;
    return 0;
}

// button: integer id, "0x5d", or a name from the client's table (exact match).
static int parse_button(const nlohmann::json& v) {
    if (v.is_number_integer()) return v.get<int>();
    if (!v.is_string()) return -1;
    std::string s = v.get<std::string>();
    if (s.empty()) return -1;
    if (s.size() > 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X')) return (int)strtol(s.c_str() + 2, nullptr, 16);
    if (isdigit((unsigned char)s[0])) return atoi(s.c_str());
    auto* e = LogicalButtonByName(s);
    return e->key_id;
}

// --- mode notify: the statescript_fake_logicalbtn path -----------------------------------------------------

static Entity* local_hero(EntityAdminBase* ea) {
    Entity* ctrl = ea->getLocalEnt();
    if (!ctrl) return nullptr;
    auto mr = ctrl->getById<Component_20_ModelReference>(0x20);
    if (!mr) return nullptr;
    for (uint32_t id : { mr->movement_attach_entid, mr->aim_entid, mr->cam_attach_entid }) {
        if (!id) continue;
        Entity* e = ea->getEntById(id);
        if (e && e->getById<Component_23_Statescript>(0x23)) return e;
    }
    return nullptr;
}

// Queue one LogicalButton notification (type 7) on every instance of the component, delay_ms after the start
// of the next command frame. The client's own button notifications carry time = the start of their command
// frame (CF x 16; SyncMgr time_ms is CF x 16 + 15 between steps) and flags byte +0x28 = 1 [L, recordings].
// Uses the instance's sorted insert (vfunc +0x140: FIFO among equal times, so a later "up" lands behind the
// "down").
static int notify(Component_23_Statescript* comp, int button, bool down, int64_t delay_ms) {
    auto sm = &comp->ss_inner;
    int64_t at = (sm->time_ms / 16 + 1) * 16 + (delay_ms + 15) / 16 * 16;
    int n = 0;
    for (auto inst : *sm) {
        if (!inst || !readable(inst, 0x20)) continue;
        auto nt = sm->vfptr->Allocate_StatescriptE(sm, ETYPE_LOGICALBUTTON);
        if (!nt) continue;
        *(int64_t*)((char*)nt + 0x18) = at;
        nt->m_instanceId = inst->instance_id;
        nt->field_28 = 1;
        auto lb = (StatescriptNotification_LogicalButton*)nt;
        lb->m_logicalButton = button;
        lb->m_bButtonGoingDown = down;
        inst->vfptr->EmplaceStatescriptE(inst, nt);
        n++;
    }
    return n;
}

// --- control ------------------------------------------------------------------------------------------------

static nlohmann::json button_json(int id) {
    nlohmann::json j = { {"id", id}, {"hex", std::format("0x{:x}", id)}, {"name", name_of(id)} };
    if (int bit = mask_bit(id)) j["command_bit"] = std::format("0x{:x}", bit);
    if (int code = action_code(id)) j["action_code"] = code;
    if (id >= 0x3d && id <= 0x41) j["axis"] = true;
    return j;
}

static nlohmann::json status() {
    nlohmann::json held = nlohmann::json::array();
    uint64_t now = GetTickCount64();
    for (int b = 0; b < kButtons; b++) {
        uint64_t u = g_until[b].load();
        if (!u) continue;
        auto j = button_json(b);
        j["remaining_ms"] = u == UINT64_MAX ? -1 : (int64_t)(u > now ? u - now : 0);
        j["hook_hits"] = g_hits[b].load();
        held.push_back(j);
    }
    return { {"hook_installed", g_hook_installed}, {"held", held},
             {"input_ticks", g_ticks.load()},
             {"input_comp", std::format("0x{:x}", g_input_comp.load())},
             {"lobby_singleton_0x50", std::format("0x{:x}", (uintptr_t)(LobbyEntityAdmin() ?
                 LobbyEntityAdmin()->getSingletonComponent<Component_50_Input>(0x50) : nullptr))}, {"edges_applied", g_edges_applied.load()},
             {"captured_before_tick", g_last_captured.load()},
             {"last_cmd_after_tick", std::format("0x{:x}", (uint32_t)g_last_action.load())} };
}

nlohmann::json control(const nlohmann::json& a) {
    std::string action = a.value("action", "tap");
    if (action == "status") return status();
    if (action == "list") {
        bool all = a.value("all", false);
        nlohmann::json arr = nlohmann::json::array();
        auto list = LogicalButtonList();
        for (int i = 0; i < kButtons; i++) {
            int id = list[i].key_id;
            if (!all && std::string(name_of(id)) == "?") continue;
            arr.push_back(button_json(id));
        }
        return { {"count", arr.size()}, {"buttons", arr},
                 {"note", "mode input reaches the command (server + Statescript) for buttons with command_bit (held "
                          "mask, comp 0x2f +0x147a), action_code (one per press, +0x147c) or axis (movement); "
                          "any id works with mode notify (local only)"} };
    }
    if (action != "press" && action != "release" && action != "tap")
        return err("action: list | press | release | tap | status");
    if (!a.contains("button")) return err("button required (name or id)");
    int button = parse_button(a["button"]);
    if (button < 0 || button >= kButtons) return err("unknown button");
    int hold_ms = a.value("hold_ms", 100);
    if (hold_ms < 1) hold_ms = 1;
    std::string mode = a.value("mode", "input");
    nlohmann::json out = button_json(button);
    out["action"] = action;
    out["mode"] = mode;

    if (mode == "input") {
        if (!install_hook()) return err("hooking the input functions failed");
        if (action == "press") g_until[button] = UINT64_MAX;
        else if (action == "release") g_until[button] = 0;
        else { g_until[button] = GetTickCount64() + hold_ms; out["hold_ms"] = hold_ms; }
        if (action != "release") { g_hits[button] = 0; g_edge[button] = true; }
        if (!mask_bit(button) && !action_code(button) && !(button >= 0x3d && button <= 0x41))
            out["warning"] = "not sampled into the command (no command_bit / action_code / axis): use mode notify";
        return out;
    }
    if (mode != "notify") return err("mode: input | notify");

    EntityAdminBase* ea = GameEntityAdmin();
    if (!ea) return err("no game entity admin (not in a match)");
    Entity* ent = nullptr;
    if (a.contains("entity_id")) {
        auto& v = a["entity_id"];
        uint32_t id = v.is_string() ? (uint32_t)strtoul(v.get<std::string>().c_str(), nullptr, 0) : v.get<uint32_t>();
        ent = ea->getEntById(id);
    } else {
        ent = local_hero(ea);
    }
    if (!ent) return err("entity not found (pass entity_id; default = the local controller's hero via comp 0x20)");
    auto comp = ent->getById<Component_23_Statescript>(0x23);
    if (!comp) return err("entity has no statescript component (0x23)");
    int n = 0;
    if (action == "press") n = notify(comp, button, true, 0);
    else if (action == "release") n = notify(comp, button, false, 0);
    else { n = notify(comp, button, true, 0); notify(comp, button, false, hold_ms); out["hold_ms"] = hold_ms; }
    out["entity_id"] = std::format("0x{:x}", *(uint32_t*)((char*)ent + 0x94));
    out["instances"] = n;
    out["time_ms"] = (comp->ss_inner.time_ms / 16 + 1) * 16;
    return out;
}

} // namespace logical_button
