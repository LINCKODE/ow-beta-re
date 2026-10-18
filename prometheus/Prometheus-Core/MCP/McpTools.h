#pragma once
#include <functional>
#include <string>
#include <vector>
#include <nlohmann/json.hpp>

// Tool registry. Each tool runs on the UI thread when invoked via the HTTP API.
// Handlers should never block on external IO and should use __try/__except for
// game-memory dereferences that might fault.
namespace prom_mcp {

struct ToolDef {
    std::string name;
    std::string description;
    nlohmann::json input_schema;
    std::function<nlohmann::json(const nlohmann::json& args)> handler;
};

const std::vector<ToolDef>& all_tools();
const ToolDef* find_tool(const std::string& name);

// MCP write tools are COMPILED OUT in this release. "Write tool" = anything that changes game memory or game
// state, runs game code, injects input or resources, or writes files (the full list: is_write_call() in
// McpTools.cpp, docs/PrometheusChanges.md "MCP write gating"). With PROMETHEUS_MCP_NO_WRITES defined, every write
// call is refused and the toggle in the MCP Server window is greyed out.
//
// The MCP server has no authentication, and the PrometheusLobby JAM channel lets the connected lobby server call
// any MCP tool. With writes enabled, whoever reaches 127.0.0.1:17888, or runs the lobby server you connect to,
// can run arbitrary code in your game process (call_function) and write files (trace save). Removing the define
// below is NOT advised. Do it only if you fully understand and accept these risks, connect only to servers you
// run yourself, and never expose the port.
#define PROMETHEUS_MCP_NO_WRITES

// Runtime write toggle (MCP Server window). Off at startup; has no effect while writes are compiled out.
extern bool g_write_enabled;

// True if write calls are accepted right now: compiled in AND the toggle is on.
bool writes_allowed();

// True if this call (tool + arguments) is a write: see the list in McpTools.cpp.
bool is_write_call(const std::string& tool, const nlohmann::json& args);

} // namespace prom_mcp
