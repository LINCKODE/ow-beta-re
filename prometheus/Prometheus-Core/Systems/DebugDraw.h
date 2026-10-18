#pragma once
#include <array>
#include <cstdint>
#include <string>
#include <vector>
#include <nlohmann/json.hpp>

// World-space debug shapes drawn by the engine's own immediate-mode renderer, PrimDraw
// (docs/DebugDrawing.md). Shapes come from two sources: the game server (polled from the
// instance server's control API, GET /debug/draw) and local ones (MCP tool debug_draw, the
// Debug Draw window). They are replayed into PrimDraw every frame from a hook on
// Game_SubmitFrame (main thread, between the render manager's BeginFrame and EndFrame),
// so the engine projects and depth-tests them in the game camera.
namespace debug_draw {

// One primitive (world space, metres, y up). Kinds:
//   sphere   pos = centre, radius
//   box      pos = centre, half = half extents, yaw (radians about y)
//   capsule  pos = centre, radius, height = distance between the cap centres (y axis)
//   cylinder pos = base centre, radius, height (y axis)
//   line     pos -> to
//   slot     raw PrimDraw call (tests): slot = vtable offset 0x440..0x498, pos, radius, height, half
struct Shape {
    std::string group;
    std::string kind;
    std::array<float, 3> pos{};
    std::array<float, 3> to{};
    std::array<float, 3> half{ 0.5f, 0.5f, 0.5f };
    float radius = 0.5f;
    float height = 1.0f;
    float yaw = 0.0f;
    std::array<float, 4> color{ 1, 1, 1, 1 };
    std::string label;
    uint32_t flags = 0;  // extra PrimDraw flags (0x20, 0x100: docs/DebugDrawing.md §1.1)
    uint32_t slot = 0;   // kind "slot"
    double expires = 0;  // local shapes: GetTickCount64()/1000 deadline, 0 = until cleared
};

struct Group {
    std::string name, label;
    std::array<float, 4> color{ 1, 1, 1, 1 };
    bool visible = true;
    int count = 0;
    bool server = false;
};

// Screen space (docs/DebugDrawing.md §2.4): text lines drawn with the engine's debug font
// (DebugPrintV) and 2D primitives (render vtable +0x4b0 rect, +0x4d8 line list), all inside the
// engine's debug-overlay pass through an overlay object Prometheus adds to the engine's list.
// Coordinates in pixels, origin top left. HUD lines stack under the engine's own overlays.
struct HudLine {
    std::string group;
    std::string text;
    std::array<float, 4> color{ 1, 1, 1, 1 };
    double expires = 0;
};
struct Prim2D {
    std::string group;
    std::string kind;                       // "rect" (filled) | "lines" (a connected polyline [L])
    std::array<float, 4> rect{};            // x, y, w, h
    std::vector<std::array<float, 2>> points;
    std::array<float, 4> color{ 1, 1, 1, 1 };
    double expires = 0;
};
void add_hud(const HudLine& l);
void add_prim2d(const Prim2D& p);
void clear_screen(const std::string& group = "");  // local HUD lines and 2D primitives; "" = all
std::vector<HudLine> hud_lines();                   // server + local
bool screen_enabled();
void set_screen_enabled(bool on);

// The engine's 10 built-in debug overlays (render manager list +0x8F0), and the FPS/RTT line.
struct EngineOverlay {
    std::string code, help;
    uintptr_t obj = 0;
    bool enabled = false;
    int mode = 0;
};
std::vector<EngineOverlay> engine_overlays();
bool set_engine_overlay(const std::string& code, bool enabled, int mode = -1);
bool fps_line();
void set_fps_line(bool on);

// Installs the Game_SubmitFrame hook (call once from the hook setup).
void install_hook();

// Master switch (off: nothing is submitted).
bool enabled();
void set_enabled(bool on);

// Server poll: GET http://host:port/debug/draw?conn=N every interval_ms on a background thread.
struct PollConfig {
    bool on = false;
    std::string host = "127.0.0.1";
    int port = 3729;
    uint32_t conn = 0;
    int interval_ms = 250;
};
PollConfig poll_config();
void set_poll(const PollConfig& cfg);
std::string poll_status();

// Live server view shapes (Systems/ServerView, docs/DebugDrawing.md §5): replaced as a whole every UI frame.
void set_stream(std::vector<Shape> s);
void set_stream_hud(std::vector<HudLine> l);  // its screen text, drawn first

// Local shapes.
void add_local(const Shape& s);
void clear_local(const std::string& group = "");  // "" = all local shapes

// Snapshot of every shape and group (for the window / MCP), and group visibility.
std::vector<Shape> shapes();
std::vector<Group> groups();
void set_group_visible(const std::string& name, bool visible);

// Counters: frames submitted, primitives submitted in the last frame, submit errors.
nlohmann::json stats();

Shape shape_from_json(const nlohmann::json& j);
nlohmann::json shape_to_json(const Shape& s);

// MCP tool entry (action = status | add | clear | poll | enable | groups | settings | hud | screen |
// overlays).
nlohmann::json control(const nlohmann::json& args);

}
