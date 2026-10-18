#pragma once
#include <nlohmann/json.hpp>

// Logical-button injection for the MCP tool `logical_button` (see docs/StatescriptRuntime.md §4.1).
//
// Two ways to press a button:
// - mode "input" (default): the client's input query "is logical button held" (RVA 0xcec330, reads the input
//   singleton comp 0x50) is hooked and also answers true for injected buttons, and the LocalPlayerInputSystem
//   tick (0xc9df40) samples while a button is injected even with the window unfocused and gets one
//   went-down edge per press. Everything downstream is the real path: the command's button mask (comp 0x2f
//   +0x147a), action code (+0x147c: reload, weapon switch, quick melee, emotes, ...) and movement axes go to
//   the server, and the hero's Statescript gets its LogicalButton notifications as for a key.
// - mode "notify": the statescript_fake_logicalbtn window's path: a LogicalButton notification (type 7) is
//   queued directly on every instance of one entity's Statescript component. Local only (the server and
//   the command stream never see it), works for any button id.
namespace logical_button {

// action: list | press | release | tap | status   (button: name or id; hold_ms; mode; entity_id)
nlohmann::json control(const nlohmann::json& args);

} // namespace logical_button
