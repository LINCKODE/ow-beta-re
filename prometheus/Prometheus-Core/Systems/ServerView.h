#pragma once
#include <array>
#include <cstdint>
#include <string>
#include <vector>
#include <nlohmann/json.hpp>

// Live server view (docs/DebugDrawing.md §5, T072): the instance server's view of the match, one frame per server
// tick, relayed by lobbylogic as PrometheusServerView 32002 (docs/JAM.md §13.8, received by Network/CustomJam).
// Keeps the latest frame, a short history and the timing stats; every UI frame it compares the server's state with
// the client's own (hero position, command frame, entity count) and re-submits the world shapes (group "view")
// through debug_draw, since PrimDraw shapes live one frame.
namespace server_view {

// Unix µs (GetSystemTimePreciseAsFileTime): the same clock as the servers' sent_us / relay_us on this machine.
int64_t now_us();

// A received frame (UI thread; arrival_us was stamped on the network thread, wire = the JAM frame length).
void on_frame(uint32_t seq, uint32_t frame, uint64_t sent_us, uint64_t relay_us, std::string body, int64_t arrival_us,
              uint32_t wire);

// Once per UI frame (UI thread): coalescing count, client state, world shapes.
void update();

// World markers (default off) and the screen text (default on).
bool draw();
void set_draw(bool on);
bool text();
void set_text(bool on);

// Everything the window shows, as JSON (latest header + body, client state, stats); history = the last n headers.
nlohmann::json snapshot(int history = 0);

// MCP tool server_view: {} / {"history": n} → snapshot; {"action": "hello"} → the capability answer lobbylogic's
// !serverview handshake checks ({"message": 32002}); {"action": "draw" | "text", "on": bool}; {"action": "reset"} (stats).
nlohmann::json control(const nlohmann::json& args);

}
