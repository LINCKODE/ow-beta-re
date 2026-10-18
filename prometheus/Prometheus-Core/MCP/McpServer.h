#pragma once
#include <atomic>
#include <future>
#include <mutex>
#include <string>
#include <thread>
#include <vector>
#include <nlohmann/json.hpp>

// MCP server hosted inside PrometheusCore.dll. Talks HTTP/JSON on localhost.
// External Python bridge (bridge_mcp_prometheus.py) translates to MCP for Claude.
//
// Threading: HTTP requests arrive on cpp-httplib worker threads. Each request
// builds a PendingCall with a promise, pushes to a queue, then blocks on the
// promise. The UI thread (PrometheusCore's existing single ImGui+game-state
// owner) drains the queue from process_pending() each frame, executes the tool
// (which can safely touch any game memory), and fulfills the promise.
namespace prom_mcp {

class McpServer {
public:
    static McpServer& instance();

    // Spawn the HTTP server thread. Idempotent (later calls are no-ops).
    void start(int port = 17888);

    // Stop the HTTP server thread, drain pending calls with errors.
    void stop();

    bool is_running() const { return _running.load(); }
    int port() const { return _port; }
    int requests_total() const { return _requests_total.load(); }
    int requests_failed() const { return _requests_failed.load(); }
    int queue_depth() const;

    // Called from the UI thread once per frame. Drains the pending-call queue,
    // executes each tool's handler on this thread, fulfills the promises.
    void process_pending();

private:
    McpServer() = default;
    McpServer(const McpServer&) = delete;
    McpServer& operator=(const McpServer&) = delete;

    struct PendingCall {
        std::string tool_name;
        nlohmann::json args;
        std::promise<nlohmann::json> result;
    };

    std::mutex _queue_mtx;
    std::vector<std::shared_ptr<PendingCall>> _queue;

    std::thread _server_thread;
    std::atomic<bool> _running{false};
    int _port = 17888;

    std::atomic<int> _requests_total{0};
    std::atomic<int> _requests_failed{0};

    // Called from HTTP worker thread: queues a tool call for the UI thread,
    // blocks until result is ready or timeout elapses.
    nlohmann::json invoke_on_ui_thread(const std::string& tool,
                                       const nlohmann::json& args,
                                       int timeout_ms = 5000);

    void run_server();
};

} // namespace prom_mcp
