#pragma once
#include <cstdint>
#include <nlohmann/json.hpp>

// Our own JAM protocol on the lobby link (docs/JAM.md §13, T033).
//
// After the client registers its lobby protocols (FUN_7ff650c46560, RVA 0xC66560),
// the DLL registers `PrometheusLobby` (CRC 0xF4456D46, IDs 32000..32015, server
// type 13) on the same JAM route config: a pool implemented here, one receiver and
// the route entry. The client then offers the CRC in its negotiation list and the
// lobby server gets a channel for it (33, after the 32 stock ones).
//
//   32000 PrometheusCall   server -> client  {u32 seq, str tool, str args(JSON)}
//   32001 PrometheusResult client -> server  {u32 seq, bool ok, str result(JSON)}
//
// A call runs a Prometheus tool (every MCP tool, plus `notify` / `ping` / `tools`)
// on the UI thread and answers with a result on the link it came from. Write-type
// MCP tools keep their own write-toggle check.
namespace Network::CustomJam
{
    void Install();

    // Runs queued calls; call once per UI frame on the UI thread.
    void process();

    // Draws `notify` messages on top of the game (UI thread, inside an ImGui frame).
    void render_overlay();

    // JamLink::Close hook (JamDiagnostics): forget the link so no result is sent on it.
    void on_link_closed(int64_t link);

    // For the MCP tool custom_jam_status.
    nlohmann::json status();
}
