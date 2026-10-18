// cpp-httplib needs Win8.1+; see MCP/McpServer.cpp for this preamble.
#ifdef _WIN32_WINNT
#undef _WIN32_WINNT
#endif
#define _WIN32_WINNT 0x0A00
#ifdef WINVER
#undef WINVER
#endif
#define WINVER 0x0A00
#include <winsock2.h>
#include <ws2tcpip.h>
extern "C" WINSOCK_API_LINKAGE INT WSAAPI GetAddrInfoExCancel(LPHANDLE);
#include <httplib.h>
#include "DebugDraw.h"
#include "../globals.h"
#include "../Logs/Logs.h"
#include <MinHook.h>
#include <atomic>
#include <chrono>
#include <algorithm>
#include <cmath>
#include <format>
#include <map>
#include <mutex>
#include <thread>

namespace debug_draw {
namespace {

// Game addresses (RVAs; docs/DebugDrawing.md §1.1, §2.1).
constexpr uintptr_t kRenderMgrPtr = 0x181e3e0;   // render manager object pointer
constexpr uintptr_t kGameSubmitFrame = 0xbff7b0; // void Game_SubmitFrame(App*, float dt)

// PrimDraw shape functions: render-manager vtable slots.
using FnPosR = void (*)(uintptr_t rm, const float* pos4, float r, const float* rgba, uint32_t flags);
using FnMtxR = void (*)(uintptr_t rm, const float* m16, float r, const float* rgba, uint32_t flags);
using FnMtxRH = void (*)(uintptr_t rm, const float* m16, float r, float h, const float* rgba, uint32_t flags);
using FnMtxV3 = void (*)(uintptr_t rm, const float* m16, const float* s3, const float* rgba, uint32_t flags);
using FnLine = void (*)(uintptr_t rm, const float* p0, const float* p1, const float* rgba, uint32_t flags);

enum SlotClass { kPosR, kMtxR, kMtxRH, kMtxV3, kLine, kBad };
SlotClass slot_class(uint32_t slot) {
    switch (slot) {
    case 0x440: return kPosR;
    case 0x448: case 0x450: case 0x480: case 0x488: case 0x490: return kMtxR;
    case 0x458: case 0x460: case 0x468: return kMtxRH;
    case 0x470: case 0x498: return kMtxV3;
    case 0x478: return kLine;
    }
    return kBad;
}

std::mutex g_mtx;
std::vector<Shape> g_server, g_local, g_stream; // g_stream: the live server view (ServerView)
std::map<std::string, Group> g_server_groups;
std::map<std::string, bool> g_hidden;
std::atomic<bool> g_enabled{ true };
PollConfig g_poll;
std::string g_poll_status = "off";
double g_last_ok = 0;
std::thread g_poll_thread;
std::atomic<bool> g_poll_thread_started{ false };

std::atomic<uint64_t> g_frames{ 0 };
std::atomic<int> g_last_count{ 0 };
std::atomic<uint64_t> g_skipped{ 0 };

// Settings for the shapes whose PrimDraw mesh size is not known yet (docs/DebugDrawing.md §4).
float g_box_scale = 1.0f;     // PrimDraw scale per unit of half extent (the box mesh spans -1..1 [L])
uint32_t g_cyl_slot = 0x458;  // slot drawing a cylinder (radius, height)
bool g_cyl_centred = true;    // the cylinder mesh is centred on its origin (pass base + h/2)

double now_s() { return (double)GetTickCount64() / 1000.0; }

// ---- Screen space (docs/DebugDrawing.md §2.4) ----

// DebugPrintV(x, y, rgba, shadow rgba or null, unused, fmt, va_list): the engine's debug-font text,
// valid inside the debug-overlay pass. Its return-address check divides by zero when the caller is
// outside the game's code; install_hook() patches the failure path (like dllmain's Anti-Anti-Debug).
constexpr uintptr_t kDebugPrintV = 0x8cd940;
constexpr uintptr_t kDebugPrintVCheckFail = 0x8cdc8f; // 12 NOPs, then the /0 block, then 0x8cdd39
using FnDebugPrintV = bool (*)(float x, float y, const float* rgba, const float* shadow, void* unused,
                               const char* fmt, char* va);
bool g_print_ok = false; // the patch is in

// Render manager fields: overlay list head (+0x8F0, linked through obj+0x20), flags (+0x950: bit 7 = the
// FPS/RTT line).
constexpr uintptr_t kOverlayList = 0x8f0;
constexpr uintptr_t kRenderFlags = 0x950;
constexpr float kLineHeight = 15.0f;
constexpr float kCharWidth = 7.5f;

struct OverlayInfo { uintptr_t vt_rva; const char* code; const char* help; };
constexpr OverlayInfo kOverlays[] = {
    { 0x158cea8, "fps", "Frames per second (mode: fps / sfps / mspf)" },
    { 0x158cde8, "wt", "Worker thread status (JOBS)" },
    { 0x158ce48, "ts", "Time scale" },
    { 0x158ce18, "rb", "Render buffer memory" },
    { 0x158ce78, "tm", "Texture / GPU memory" },
    { 0x158cd88, "mma", "Memory manager allocations" },
    { 0x158cdb8, "ci", "CPU idle" },
    { 0x158cd28, "rm", "Resource memory" },
    { 0x158cd58, "il", "Input lag (mode 1: detailed)" },
    { 0x158ccf8, "gt", "GPU temperature" },
};

std::vector<HudLine> g_server_hud, g_local_hud, g_stream_hud; // g_stream_hud: the live server view
std::vector<Prim2D> g_server_2d, g_local_2d;
std::atomic<bool> g_screen{ true };

// Our overlay object, laid out like the engine's (vtable, +0x10 width, +0x14 height, +0x18 enabled,
// +0x20 next, +0x28 mode). It lives in Prometheus' memory: the delete slots never free it.
struct PromOverlay {
    void** vt;
    uint64_t pad8;
    float width, height;
    uint8_t enabled;
    uint8_t pad19[7];
    PromOverlay* next;
    int32_t mode;
    uint8_t pad2c[0x14];
};
PromOverlay g_overlay{};
bool g_overlay_linked = false;

void* ov_dtor(void* self, uint64_t) { return nullptr; } // nothing to free
void ov_delete(void*) {}
void ov_draw(PromOverlay* self, float x, float y);
bool ov_toggle(void*, void*) { return false; }
const char* ov_help(void*) { return "Prometheus debug HUD"; }
void* g_overlay_vt[6] = { (void*)ov_dtor, (void*)ov_delete, (void*)ov_draw, (void*)ov_toggle, (void*)ov_help, nullptr };

uint32_t abgr(const std::array<float, 4>& c) {
    auto b = [](float v) { return (uint32_t)std::lround(std::clamp(v, 0.0f, 1.0f) * 255.0f); };
    return b(c[0]) | b(c[1]) << 8 | b(c[2]) << 16 | b(c[3]) << 24;
}

bool hidden(const std::string& group) {
    auto h = g_hidden.find(group);
    return h != g_hidden.end() && h->second;
}

// Main thread, in the overlay pass (teRender_EndFrame): the engine's 2D view is pushed.
void ov_draw(PromOverlay* self, float x, float y) {
    uintptr_t rm = *(uintptr_t*)(globals::gameBase + kRenderMgrPtr);
    if (!rm) return;
    double t = now_s();
    static const float shadow[4] = { 0, 0, 0, 1 };
    auto print = (FnDebugPrintV)(globals::gameBase + kDebugPrintV);
    auto vt = *(uintptr_t**)rm;
    std::lock_guard<std::mutex> lock(g_mtx);
    if (g_print_ok) {
        int i = 0;
        for (auto* list : { &g_stream_hud, &g_server_hud, &g_local_hud }) {
            for (auto& l : *list) {
                if ((l.expires != 0 && l.expires < t) || hidden(l.group)) continue;
                const char* args[1] = { l.text.c_str() };
                print(x, y + kLineHeight * i++, l.color.data(), shadow, nullptr, "%s", (char*)args);
            }
        }
    }
    for (auto* list : { &g_server_2d, &g_local_2d }) {
        for (auto& p : *list) {
            if ((p.expires != 0 && p.expires < t) || hidden(p.group)) continue;
            if (p.kind == "rect") {
                float r[4] = { p.rect[2], p.rect[3], p.rect[0], p.rect[1] }; // w, h, x, y
                ((void (*)(uintptr_t, const float*, uint32_t))vt[0x4b0 / 8])(rm, r, abgr(p.color));
            } else if (p.kind == "lines" && p.points.size() >= 2) {
                struct { const float* pts; uint32_t count, cap; } l{ &p.points[0][0], (uint32_t)p.points.size(),
                                                                     (uint32_t)p.points.size() };
                ((void (*)(uintptr_t, const void*, uint32_t))vt[0x4d8 / 8])(rm, &l, abgr(p.color));
            }
        }
    }
}

void patch_debug_print();

// Main thread (submit hook): link our overlay once, size it, switch it on while there is something to draw.
void update_overlay(uintptr_t rm, double t) {
    if (!g_overlay_linked) {
        if (!g_print_ok) patch_debug_print();
        g_overlay.vt = g_overlay_vt;
        g_overlay_vt[5] = (void*)(globals::gameBase + 0x9b8e30); // the engine's GetName-if-enabled
        auto head = (PromOverlay**)(rm + kOverlayList);
        while (*head) head = &(*head)->next;
        *head = &g_overlay;
        g_overlay_linked = true;
        LOG_CORE(Debug, "debug_draw: HUD overlay linked\n");
    }
    int lines = 0;
    size_t chars = 0;
    bool prims = false;
    if (g_screen && g_enabled) {
        for (auto* list : { &g_stream_hud, &g_server_hud, &g_local_hud })
            for (auto& l : *list)
                if (!(l.expires != 0 && l.expires < t) && !hidden(l.group)) {
                    lines++;
                    chars = std::max(chars, l.text.size());
                }
        for (auto* list : { &g_server_2d, &g_local_2d })
            for (auto& p : *list)
                if (!(p.expires != 0 && p.expires < t) && !hidden(p.group)) prims = true;
    }
    g_overlay.width = lines ? chars * kCharWidth + 4 : 1;
    g_overlay.height = lines * kLineHeight;
    g_overlay.enabled = (lines || prims) ? 1 : 0;
    std::erase_if(g_local_hud, [t](const HudLine& l) { return l.expires != 0 && l.expires < t; });
    std::erase_if(g_local_2d, [t](const Prim2D& p) { return p.expires != 0 && p.expires < t; });
}

void patch_debug_print() {
    // Live layout (differs from the treated dump, which shows NOPs for the junk): the check loop ends with
    // cmp r9d, eax; jl (0x8cdc86), then 12 junk bytes, the /0 block from 0x8cdc9b, and the success label
    // 0x8cdd39 (cmp [font], r15). The patch jumps from the fall-through straight to the success label.
    auto base = (uint8_t*)globals::gameBase;
    auto p = base + kDebugPrintVCheckFail;
    static const uint8_t loop_end[9] = { 0x44, 0x3b, 0xc8, 0x0f, 0x8c, 0xc1, 0xfe, 0xff, 0xff };
    static const uint8_t fail_blk[5] = { 0xc7, 0x44, 0x24, 0x78, 0x05 };
    static const uint8_t target[3] = { 0x4c, 0x39, 0x3d };
    static const uint8_t jmp[5] = { 0xe9, 0xa5, 0x00, 0x00, 0x00 }; // jmp 0x8cdd39 (the check passed)
    if (memcmp(p, jmp, 5) == 0) { g_print_ok = true; return; }
    if (memcmp(p - 9, loop_end, 9) != 0 || memcmp(p + 12, fail_blk, 5) != 0 || memcmp(p + 0xaa, target, 3) != 0) {
        LOG_CORE(Error, "debug_draw: DebugPrintV check bytes differ, HUD text disabled\n");
        return;
    }
    DWORD old;
    if (!VirtualProtect(p, 5, PAGE_EXECUTE_READWRITE, &old)) return;
    memcpy(p, jmp, 5);
    VirtualProtect(p, 5, old, &old);
    FlushInstructionCache(GetCurrentProcess(), p, 5);
    g_print_ok = true;
}

HudLine hud_from_json(const nlohmann::json& j) {
    HudLine l;
    l.group = j.value("group", "hud");
    l.text = j.value("text", "");
    if (j.contains("color") && j["color"].is_array())
        for (size_t i = 0; i < 4 && i < j["color"].size(); i++) l.color[i] = j["color"][i].get<float>();
    return l;
}

Prim2D prim_from_json(const nlohmann::json& j) {
    Prim2D p;
    p.group = j.value("group", "screen");
    p.kind = j.value("kind", "rect");
    if (j.contains("rect") && j["rect"].size() >= 4)
        for (int i = 0; i < 4; i++) p.rect[i] = j["rect"][i].get<float>();
    for (auto& pt : j.value("points", nlohmann::json::array()))
        if (pt.is_array() && pt.size() >= 2) p.points.push_back({ pt[0].get<float>(), pt[1].get<float>() });
    if (j.contains("color") && j["color"].is_array())
        for (size_t i = 0; i < 4 && i < j["color"].size(); i++) p.color[i] = j["color"][i].get<float>();
    return p;
}

void yaw_matrix(float* m, const std::array<float, 3>& pos, float yaw) {
    float c = std::cos(yaw), s = std::sin(yaw);
    float r[16] = { c, 0, -s, 0,  0, 1, 0, 0,  s, 0, c, 0,  pos[0], pos[1], pos[2], 1 };
    memcpy(m, r, sizeof(r));
}

void call_slot(uintptr_t rm, uint32_t slot, const Shape& s, const float* m, const float* rgba, uint32_t flags) {
    auto vt = *(uintptr_t**)rm;
    void* fn = (void*)vt[slot / 8];
    float p0[4] = { s.pos[0], s.pos[1], s.pos[2], 1 };
    float p1[4] = { s.to[0], s.to[1], s.to[2], 1 };
    float h3[3] = { s.half[0], s.half[1], s.half[2] };
    switch (slot_class(slot)) {
    case kPosR: ((FnPosR)fn)(rm, p0, s.radius, rgba, flags); break;
    case kMtxR: ((FnMtxR)fn)(rm, m, s.radius, rgba, flags); break;
    case kMtxRH: ((FnMtxRH)fn)(rm, m, s.radius, s.height, rgba, flags); break;
    case kMtxV3: ((FnMtxV3)fn)(rm, m, h3, rgba, flags); break;
    case kLine: ((FnLine)fn)(rm, p0, p1, rgba, flags); break;
    default: g_skipped++; break;
    }
}

int submit_shape(uintptr_t rm, const Shape& s) {
    float m[16];
    const float* rgba = s.color.data();
    uint32_t f = s.flags;
    if (s.kind == "sphere") {
        call_slot(rm, 0x440, s, nullptr, rgba, f);
    } else if (s.kind == "box") {
        Shape b = s;
        for (auto& h : b.half) h *= g_box_scale;
        yaw_matrix(m, s.pos, s.yaw);
        call_slot(rm, 0x470, b, m, rgba, f);
    } else if (s.kind == "capsule") {
        yaw_matrix(m, s.pos, s.yaw);
        call_slot(rm, 0x468, s, m, rgba, f);
    } else if (s.kind == "cylinder") {
        auto c = s.pos;
        if (g_cyl_centred) c[1] += s.height / 2;
        yaw_matrix(m, c, s.yaw);
        call_slot(rm, g_cyl_slot, s, m, rgba, f);
    } else if (s.kind == "line") {
        call_slot(rm, 0x478, s, nullptr, rgba, f);
    } else if (s.kind == "slot") {
        yaw_matrix(m, s.pos, s.yaw);
        call_slot(rm, s.slot, s, m, rgba, f);
    } else {
        g_skipped++;
        return 0;
    }
    return 1;
}

// Main thread, between BeginFrame and EndFrame: the engine's own PrimDraw callers' context.
void submit_frame() {
    uintptr_t rm = *(uintptr_t*)(globals::gameBase + kRenderMgrPtr);
    if (!rm || !*(uintptr_t*)rm) return;
    double t = now_s();
    int n = 0;
    std::lock_guard<std::mutex> lock(g_mtx);
    if (!g_enabled.load(std::memory_order_relaxed)) {
        update_overlay(rm, t);
        return;
    }
    for (auto* list : { &g_server, &g_local, &g_stream }) {
        for (auto& s : *list) {
            if (s.expires != 0 && s.expires < t) continue;
            auto h = g_hidden.find(s.group);
            if (h != g_hidden.end() && h->second) continue;
            n += submit_shape(rm, s);
        }
    }
    std::erase_if(g_local, [t](const Shape& s) { return s.expires != 0 && s.expires < t; });
    update_overlay(rm, t);
    g_last_count = n;
    g_frames++;
}

using SubmitFn = void (*)(void* app, float dt);
SubmitFn g_submit_orig = nullptr;
void submit_hook(void* app, float dt) {
    submit_frame();
    g_submit_orig(app, dt);
}

std::array<float, 3> vec3(const nlohmann::json& j, std::array<float, 3> def) {
    if (j.is_array() && j.size() >= 3) return { j[0].get<float>(), j[1].get<float>(), j[2].get<float>() };
    return def;
}

void poll_once(const PollConfig& cfg) {
    httplib::Client cli(cfg.host, cfg.port);
    cli.set_connection_timeout(0, 300000);
    cli.set_read_timeout(1, 0);
    std::string path = cfg.conn ? std::format("/debug/draw?conn={}", cfg.conn) : "/debug/draw";
    auto res = cli.Get(path);
    std::lock_guard<std::mutex> lock(g_mtx);
    if (!res || res->status != 200) {
        g_poll_status = res ? std::format("HTTP {}: {}", res->status, res->body.substr(0, 120))
                            : std::format("no reply from {}:{} ({})", cfg.host, cfg.port, httplib::to_string(res.error()));
        if (now_s() - g_last_ok > 2) {
            g_server.clear();
            g_server_hud.clear();
            g_server_2d.clear();
        }
        return;
    }
    try {
        auto j = nlohmann::json::parse(res->body);
        std::vector<Shape> shapes;
        for (auto& sj : j.value("shapes", nlohmann::json::array())) shapes.push_back(shape_from_json(sj));
        std::map<std::string, Group> groups;
        for (auto& gj : j.value("groups", nlohmann::json::array())) {
            Group g;
            g.name = gj.value("name", "");
            g.label = gj.value("label", "");
            if (gj.contains("color") && gj["color"].size() >= 4)
                for (int i = 0; i < 4; i++) g.color[i] = gj["color"][i].get<float>();
            g.server = true;
            groups[g.name] = g;
        }
        g_server_hud.clear();
        for (auto& hj : j.value("hud", nlohmann::json::array())) g_server_hud.push_back(hud_from_json(hj));
        g_server_2d.clear();
        for (auto& pj : j.value("screen", nlohmann::json::array())) g_server_2d.push_back(prim_from_json(pj));
        g_server = std::move(shapes);
        g_server_groups = std::move(groups);
        g_last_ok = now_s();
        g_poll_status = std::format("ok: {} shapes, {} (frame {})", g_server.size(), j.value("name", "no match"),
                                    j.value("frame", 0u));
    } catch (const std::exception& e) {
        g_poll_status = std::string("bad reply: ") + e.what();
    }
}

void poll_loop() {
    for (;;) {
        PollConfig cfg;
        {
            std::lock_guard<std::mutex> lock(g_mtx);
            cfg = g_poll;
        }
        if (!cfg.on) {
            std::this_thread::sleep_for(std::chrono::milliseconds(200));
            continue;
        }
        poll_once(cfg);
        std::this_thread::sleep_for(std::chrono::milliseconds(std::max(50, cfg.interval_ms)));
    }
}

} // namespace

void install_hook() {
    auto target = (void*)(globals::gameBase + kGameSubmitFrame);
    if (MH_CreateHook(target, (void*)submit_hook, (void**)&g_submit_orig) != MH_OK || MH_EnableHook(target) != MH_OK) {
        LOG_CORE(Error, "debug_draw: hooking Game_SubmitFrame failed\n");
        return;
    }
    LOG_CORE(Debug, "debug_draw: Game_SubmitFrame hook\n");
}

bool enabled() { return g_enabled; }
void set_enabled(bool on) { g_enabled = on; }

PollConfig poll_config() {
    std::lock_guard<std::mutex> lock(g_mtx);
    return g_poll;
}

void set_poll(const PollConfig& cfg) {
    {
        std::lock_guard<std::mutex> lock(g_mtx);
        g_poll = cfg;
        if (!cfg.on) {
            g_server.clear();
            g_server_hud.clear();
            g_server_2d.clear();
            g_poll_status = "off";
        }
    }
    if (cfg.on && !g_poll_thread_started.exchange(true)) {
        g_poll_thread = std::thread(poll_loop);
        g_poll_thread.detach();
    }
}

std::string poll_status() {
    std::lock_guard<std::mutex> lock(g_mtx);
    return g_poll_status;
}

void add_local(const Shape& s) {
    std::lock_guard<std::mutex> lock(g_mtx);
    g_local.push_back(s);
}

void clear_local(const std::string& group) {
    std::lock_guard<std::mutex> lock(g_mtx);
    if (group.empty()) g_local.clear();
    else std::erase_if(g_local, [&](const Shape& s) { return s.group == group; });
}

std::vector<Shape> shapes() {
    std::lock_guard<std::mutex> lock(g_mtx);
    std::vector<Shape> out = g_server;
    out.insert(out.end(), g_local.begin(), g_local.end());
    out.insert(out.end(), g_stream.begin(), g_stream.end());
    return out;
}

void set_stream(std::vector<Shape> s) {
    std::lock_guard<std::mutex> lock(g_mtx);
    g_stream = std::move(s);
}

std::vector<Group> groups() {
    std::lock_guard<std::mutex> lock(g_mtx);
    std::map<std::string, Group> all = g_server_groups;
    for (auto* list : { &g_server, &g_local, &g_stream }) {
        for (auto& s : *list) {
            auto& g = all[s.group];
            if (g.name.empty()) {
                g.name = s.group;
                g.color = s.color;
                g.server = list != &g_local;
            }
            g.count++;
        }
    }
    for (auto* list : { &g_stream_hud, &g_server_hud, &g_local_hud })
        for (auto& l : *list) {
            auto& g = all[l.group];
            if (g.name.empty()) {
                g.name = l.group;
                g.color = l.color;
                g.server = list != &g_local_hud;
            }
            g.count++;
        }
    std::vector<Group> out;
    for (auto& [name, g] : all) {
        auto h = g_hidden.find(name);
        g.visible = h == g_hidden.end() || !h->second;
        out.push_back(g);
    }
    return out;
}

void set_group_visible(const std::string& name, bool visible) {
    std::lock_guard<std::mutex> lock(g_mtx);
    g_hidden[name] = !visible;
}

void add_hud(const HudLine& l) {
    std::lock_guard<std::mutex> lock(g_mtx);
    g_local_hud.push_back(l);
}

void add_prim2d(const Prim2D& p) {
    std::lock_guard<std::mutex> lock(g_mtx);
    g_local_2d.push_back(p);
}

void clear_screen(const std::string& group) {
    std::lock_guard<std::mutex> lock(g_mtx);
    if (group.empty()) {
        g_local_hud.clear();
        g_local_2d.clear();
        return;
    }
    std::erase_if(g_local_hud, [&](const HudLine& l) { return l.group == group; });
    std::erase_if(g_local_2d, [&](const Prim2D& p) { return p.group == group; });
}

std::vector<HudLine> hud_lines() {
    std::lock_guard<std::mutex> lock(g_mtx);
    std::vector<HudLine> out = g_stream_hud;
    out.insert(out.end(), g_server_hud.begin(), g_server_hud.end());
    out.insert(out.end(), g_local_hud.begin(), g_local_hud.end());
    return out;
}

void set_stream_hud(std::vector<HudLine> l) {
    std::lock_guard<std::mutex> lock(g_mtx);
    g_stream_hud = std::move(l);
}

bool screen_enabled() { return g_screen; }
void set_screen_enabled(bool on) { g_screen = on; }

std::vector<EngineOverlay> engine_overlays() {
    std::vector<EngineOverlay> out;
    uintptr_t rm = *(uintptr_t*)(globals::gameBase + kRenderMgrPtr);
    if (!rm) return out;
    for (auto o = *(uintptr_t*)(rm + kOverlayList); o; o = *(uintptr_t*)(o + 0x20)) {
        if (o == (uintptr_t)&g_overlay) continue;
        EngineOverlay e;
        e.obj = o;
        uintptr_t vt = *(uintptr_t*)o - globals::gameBase;
        e.code = std::format("{:#x}", vt);
        for (auto& k : kOverlays)
            if (k.vt_rva == vt) { e.code = k.code; e.help = k.help; }
        e.enabled = *(uint8_t*)(o + 0x18) != 0;
        e.mode = *(int32_t*)(o + 0x28);
        out.push_back(e);
    }
    return out;
}

bool set_engine_overlay(const std::string& code, bool on, int mode) {
    for (auto& e : engine_overlays()) {
        if (e.code != code) continue;
        *(uint8_t*)(e.obj + 0x18) = on ? 1 : 0;
        if (mode >= 0) *(int32_t*)(e.obj + 0x28) = mode;
        return true;
    }
    return false;
}

bool fps_line() {
    uintptr_t rm = *(uintptr_t*)(globals::gameBase + kRenderMgrPtr);
    return rm && (*(uint32_t*)(rm + kRenderFlags) & 0x80);
}

void set_fps_line(bool on) {
    uintptr_t rm = *(uintptr_t*)(globals::gameBase + kRenderMgrPtr);
    if (!rm) return;
    auto f = (uint32_t*)(rm + kRenderFlags);
    *f = on ? (*f | 0x80) : (*f & ~0x80u);
}

nlohmann::json stats() {
    return { {"enabled", g_enabled.load()}, {"frames", g_frames.load()}, {"last_frame_primitives", g_last_count.load()},
             {"skipped", g_skipped.load()}, {"box_scale", g_box_scale}, {"cylinder_slot", g_cyl_slot},
             {"cylinder_centred", g_cyl_centred} };
}

Shape shape_from_json(const nlohmann::json& j) {
    Shape s;
    s.group = j.value("group", "local");
    s.kind = j.value("kind", "sphere");
    s.pos = vec3(j.value("pos", nlohmann::json()), s.pos);
    s.to = vec3(j.value("to", nlohmann::json()), s.to);
    s.half = vec3(j.value("half", nlohmann::json()), s.half);
    s.radius = j.value("radius", s.radius);
    s.height = j.value("height", s.height);
    s.yaw = j.value("yaw", s.yaw);
    if (j.contains("color") && j["color"].is_array())
        for (size_t i = 0; i < 4 && i < j["color"].size(); i++) s.color[i] = j["color"][i].get<float>();
    s.label = j.value("label", "");
    s.flags = j.value("flags", 0u);
    if (j.contains("slot")) {
        auto& v = j["slot"];
        s.slot = v.is_string() ? (uint32_t)std::stoul(v.get<std::string>(), nullptr, 0) : v.get<uint32_t>();
    }
    return s;
}

nlohmann::json shape_to_json(const Shape& s) {
    nlohmann::json j = { {"group", s.group}, {"kind", s.kind}, {"pos", s.pos}, {"color", s.color} };
    if (s.kind == "line") j["to"] = s.to;
    if (s.kind == "box" || s.kind == "slot") j["half"] = s.half;
    if (s.kind != "line" && s.kind != "box") j["radius"] = s.radius;
    if (s.kind == "capsule" || s.kind == "cylinder" || s.kind == "slot") j["height"] = s.height;
    if (s.yaw) j["yaw"] = s.yaw;
    if (s.flags) j["flags"] = s.flags;
    if (s.slot) j["slot"] = std::format("{:#x}", s.slot);
    if (!s.label.empty()) j["label"] = s.label;
    return j;
}

nlohmann::json control(const nlohmann::json& a) {
    std::string action = a.value("action", "status");
    if (action == "status") {
        auto st = stats();
        auto cfg = poll_config();
        st["poll"] = { {"on", cfg.on}, {"host", cfg.host}, {"port", cfg.port}, {"conn", cfg.conn},
                       {"interval_ms", cfg.interval_ms}, {"status", poll_status()} };
        nlohmann::json gs = nlohmann::json::array();
        for (auto& g : groups())
            gs.push_back({ {"name", g.name}, {"count", g.count}, {"visible", g.visible}, {"server", g.server}, {"label", g.label} });
        st["groups"] = gs;
        if (a.value("shapes", false)) {
            nlohmann::json ss = nlohmann::json::array();
            for (auto& s : shapes()) ss.push_back(shape_to_json(s));
            st["shapes"] = ss;
        }
        return st;
    }
    if (action == "add") {
        double ttl = a.value("ttl", 0.0);
        auto list = a.contains("shapes") ? a["shapes"] : nlohmann::json::array({ a });
        int n = 0;
        for (auto& sj : list) {
            Shape s = shape_from_json(sj);
            if (ttl > 0) s.expires = now_s() + ttl;
            add_local(s);
            n++;
        }
        return { {"added", n} };
    }
    if (action == "clear") {
        clear_local(a.value("group", ""));
        return { {"cleared", true} };
    }
    if (action == "enable") {
        set_enabled(a.value("on", true));
        return { {"enabled", enabled()} };
    }
    if (action == "groups") {
        auto vis = a.value("visible", nlohmann::json::object());
        for (auto& [k, v] : vis.items()) set_group_visible(k, v.get<bool>());
        return { {"ok", true} };
    }
    if (action == "poll") {
        auto cfg = poll_config();
        cfg.on = a.value("on", true);
        cfg.host = a.value("host", cfg.host);
        cfg.port = a.value("port", cfg.port);
        cfg.conn = a.value("conn", cfg.conn);
        cfg.interval_ms = a.value("interval_ms", cfg.interval_ms);
        set_poll(cfg);
        return { {"poll", cfg.on}, {"host", cfg.host}, {"port", cfg.port} };
    }
    if (action == "settings") {
        g_box_scale = a.value("box_scale", g_box_scale);
        if (a.contains("cylinder_slot")) {
            auto& v = a["cylinder_slot"];
            g_cyl_slot = v.is_string() ? (uint32_t)std::stoul(v.get<std::string>(), nullptr, 0) : v.get<uint32_t>();
        }
        g_cyl_centred = a.value("cylinder_centred", g_cyl_centred);
        return stats();
    }
    if (action == "hud") {
        // add lines (text, or lines=[{text, color, group}]), clear=true to drop the local ones first
        if (a.value("clear", false)) clear_screen(a.value("group", ""));
        double ttl = a.value("ttl", 0.0);
        auto list = a.contains("lines") ? a["lines"] : (a.contains("text") ? nlohmann::json::array({ a }) : nlohmann::json::array());
        for (auto& lj : list) {
            HudLine l = hud_from_json(lj);
            if (ttl > 0) l.expires = now_s() + ttl;
            add_hud(l);
        }
        if (a.contains("on")) set_screen_enabled(a["on"].get<bool>());
        nlohmann::json out = nlohmann::json::array();
        for (auto& l : hud_lines()) out.push_back({ {"group", l.group}, {"text", l.text} });
        return { {"screen", screen_enabled()}, {"text_patch", g_print_ok}, {"lines", out} };
    }
    if (action == "screen") {
        // 2D primitives: prims=[{kind: rect, rect:[x,y,w,h]} | {kind: lines, points:[[x,y],...]}, color, group]
        if (a.value("clear", false)) clear_screen(a.value("group", ""));
        double ttl = a.value("ttl", 0.0);
        int n = 0;
        for (auto& pj : a.value("prims", nlohmann::json::array())) {
            Prim2D p = prim_from_json(pj);
            if (ttl > 0) p.expires = now_s() + ttl;
            add_prim2d(p);
            n++;
        }
        return { {"added", n} };
    }
    if (action == "overlays") {
        // set={code: bool} or set={code: {"on": bool, "mode": n}}, fps_line=bool
        auto set = a.value("set", nlohmann::json::object());
        for (auto& [k, v] : set.items()) {
            if (v.is_boolean()) set_engine_overlay(k, v.get<bool>());
            else set_engine_overlay(k, v.value("on", true), v.value("mode", -1));
        }
        if (a.contains("fps_line")) set_fps_line(a["fps_line"].get<bool>());
        nlohmann::json out = nlohmann::json::array();
        for (auto& e : engine_overlays())
            out.push_back({ {"code", e.code}, {"help", e.help}, {"enabled", e.enabled}, {"mode", e.mode} });
        return { {"overlays", out}, {"fps_line", fps_line()} };
    }
    return { {"error", "unknown action: " + action} };
}

} // namespace debug_draw
