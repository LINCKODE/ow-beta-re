// cpp-httplib needs Win8.1+ (GetAddrInfoExCancel etc). Project-wide WINVER is
// 0x0601 (Win7); bump just this TU before any Windows headers are pulled in.
#ifdef _WIN32_WINNT
#undef _WIN32_WINNT
#endif
#define _WIN32_WINNT 0x0A00
#ifdef WINVER
#undef WINVER
#endif
#define WINVER 0x0A00

// LLVM-MinGW's ws2tcpip.h is missing GetAddrInfoExCancel even at Win8.1+
// (Microsoft's SDK has it, MinGW-w64 hasn't caught up). httplib.h references
// it inside an async-resolve code path we never hit (Server, not Client).
// Forward-declare so the header parses; never actually called at runtime.
#include <winsock2.h>
#include <ws2tcpip.h>
extern "C" WINSOCK_API_LINKAGE INT WSAAPI GetAddrInfoExCancel(LPHANDLE);

#include <httplib.h>
#include "McpServer.h"
#include "McpTools.h"
#include "../Logs/Logs.h"
#include <chrono>

namespace prom_mcp {

McpServer& McpServer::instance() {
    static McpServer s;
    return s;
}

int McpServer::queue_depth() const {
    // Best-effort, intentionally not locking to keep observability cheap.
    return (int)const_cast<McpServer*>(this)->_queue.size();
}

nlohmann::json McpServer::invoke_on_ui_thread(const std::string& tool,
                                              const nlohmann::json& args,
                                              int timeout_ms) {
    auto call = std::make_shared<PendingCall>();
    call->tool_name = tool;
    call->args = args;
    auto fut = call->result.get_future();
    {
        std::lock_guard<std::mutex> lock(_queue_mtx);
        _queue.push_back(call);
    }
    auto status = fut.wait_for(std::chrono::milliseconds(timeout_ms));
    if (status == std::future_status::timeout) {
        return { {"error", "timeout waiting for UI thread"} };
    }
    return fut.get();
}

void McpServer::process_pending() {
    if (!_running.load(std::memory_order_relaxed)) return;
    std::vector<std::shared_ptr<PendingCall>> batch;
    {
        std::lock_guard<std::mutex> lock(_queue_mtx);
        std::swap(batch, _queue);
    }
    for (auto& call : batch) {
        nlohmann::json result;
        auto* tool = find_tool(call->tool_name);
        if (!tool) {
            result = { {"error", "unknown tool: " + call->tool_name} };
        } else {
            try {
                result = tool->handler(call->args);
            } catch (const std::exception& e) {
                result = { {"error", std::string("exception: ") + e.what()} };
            } catch (...) {
                result = { {"error", "unknown exception"} };
            }
        }
        if (result.contains("error")) _requests_failed++;
        call->result.set_value(std::move(result));
    }
}

void McpServer::run_server() {
    httplib::Server svr;

    svr.Post("/mcp/tools/list", [this](const httplib::Request&, httplib::Response& res) {
        _requests_total++;
        nlohmann::json arr = nlohmann::json::array();
        for (auto& t : all_tools()) {
            arr.push_back({
                {"name", t.name},
                {"description", t.description},
                {"inputSchema", t.input_schema},
            });
        }
        nlohmann::json body = { {"tools", arr} };
        res.set_content(body.dump(), "application/json");
    });

    svr.Post("/mcp/tools/call", [this](const httplib::Request& req, httplib::Response& res) {
        _requests_total++;
        nlohmann::json body;
        try {
            body = nlohmann::json::parse(req.body);
        } catch (const std::exception& e) {
            res.status = 400;
            res.set_content(nlohmann::json({ {"error", std::string("bad json: ") + e.what()} }).dump(), "application/json");
            _requests_failed++;
            return;
        }
        std::string name = body.value("name", "");
        nlohmann::json args = body.value("arguments", nlohmann::json::object());
        if (name.empty()) {
            res.status = 400;
            res.set_content(nlohmann::json({ {"error", "name required"} }).dump(), "application/json");
            _requests_failed++;
            return;
        }
        nlohmann::json result = invoke_on_ui_thread(name, args);
        nlohmann::json out;
        if (result.contains("error")) {
            out = { {"isError", true}, {"error", result["error"]} };
        } else {
            out = { {"isError", false}, {"result", result} };
        }
        // Game strings needn't be UTF-8: replace bad bytes (U+FFFD) instead of throwing (-> HTTP 500).
        res.set_content(out.dump(-1, ' ', false, nlohmann::json::error_handler_t::replace), "application/json");
    });

    svr.Get("/mcp/health", [this](const httplib::Request&, httplib::Response& res) {
        nlohmann::json body = {
            {"ok", true},
            {"port", _port},
            {"requests_total", _requests_total.load()},
            {"requests_failed", _requests_failed.load()},
            {"queue_depth", queue_depth()},
            {"writes_enabled", writes_allowed()},
#ifdef PROMETHEUS_MCP_NO_WRITES
            {"writes_compiled_out", true},
#else
            {"writes_compiled_out", false},
#endif
        };
        res.set_content(body.dump(), "application/json");
    });

    LOG_CORE(Info, "[MCP] server listening on 127.0.0.1:{}", _port);
    if (!svr.listen("127.0.0.1", _port)) {
        LOG_CORE(Error, "[MCP] failed to bind 127.0.0.1:{}", _port);
        _running.store(false);
    }
}

void McpServer::start(int port) {
    if (_running.exchange(true)) return;
    _port = port;
    _server_thread = std::thread([this] { run_server(); });
}

void McpServer::stop() {
    if (!_running.exchange(false)) return;
    // cpp-httplib's listen() blocks; we'd need to stop the server. For now,
    // detach and let process exit reclaim it (this is a debug DLL anyway).
    if (_server_thread.joinable()) _server_thread.detach();
    // Drain pending with errors so any blocked HTTP threads unstick.
    std::vector<std::shared_ptr<PendingCall>> batch;
    {
        std::lock_guard<std::mutex> lock(_queue_mtx);
        std::swap(batch, _queue);
    }
    for (auto& call : batch) {
        call->result.set_value({ {"error", "server stopping"} });
    }
}

} // namespace prom_mcp
