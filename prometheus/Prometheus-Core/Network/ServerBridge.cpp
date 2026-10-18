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
#include "ServerBridge.h"
#include "../MCP/McpTools.h"
#include "../Logs/Logs.h"
#include <atomic>
#include <chrono>
#include <condition_variable>
#include <format>
#include <mutex>
#include <optional>
#include <thread>
#include <vector>

namespace ServerBridge {

namespace {

constexpr const char* kHost = "127.0.0.1";
constexpr int kPort = 3728;
constexpr int kEchoEveryFrames = 3;                            // ~50 ms at 60 fps
constexpr auto kPollInterval = std::chrono::milliseconds(200);

struct Pending {
    uint32_t entity_id;
    int frames_left;
};

std::vector<Pending> g_pending; // UI thread only
std::mutex g_status_mtx;
std::string g_status = "idle";

void set_status(std::string s) {
    std::lock_guard lock(g_status_mtx);
    g_status = std::move(s);
}

// The live connection (state "connected", most recently seen): its ID, the server's
// frame and the client's latest input frame (the client's own, predicted, frame).
struct ConnInfo {
    int conn = 0;
    uint32_t frame = 0;
    uint32_t input_frame = 0;
};

std::optional<ConnInfo> server_conn() {
    httplib::Client cli(kHost, kPort);
    cli.set_connection_timeout(0, 300000);
    cli.set_read_timeout(0, 300000);
    auto res = cli.Get("/conns");
    if (!res || res->status != 200)
        return std::nullopt;
    auto conns = nlohmann::json::parse(res->body, nullptr, false);
    if (!conns.is_array())
        return std::nullopt;
    std::optional<ConnInfo> best;
    std::string best_seen;
    for (auto& c : conns) {
        if (c.value("state", "") != "connected")
            continue;
        std::string seen = c.value("last_seen", "");
        if (best && seen <= best_seen)
            continue;
        best_seen = seen;
        best = ConnInfo{ c.value("conn_id", 0), c.value("frame", 0u), c.value("input_frame", 0u) };
    }
    return best;
}

// --- Sender: posts the newest queued body, older unsent ones are dropped. ---
std::mutex g_send_mtx;
std::condition_variable g_send_cv;
std::optional<std::pair<std::string, std::string>> g_send_next; // body, summary
std::once_flag g_sender_started;

void sender_loop() {
    httplib::Client cli(kHost, kPort);
    cli.set_connection_timeout(1);
    cli.set_read_timeout(2);
    cli.set_keep_alive(true);
    for (;;) {
        std::pair<std::string, std::string> job;
        {
            std::unique_lock lock(g_send_mtx);
            g_send_cv.wait(lock, [] { return g_send_next.has_value(); });
            job = std::move(*g_send_next);
            g_send_next.reset();
        }
        auto res = cli.Post("/scripts", job.first, "application/json");
        if (res && res->status == 200)
            set_status("sent " + job.second);
        else
            set_status(std::format("send failed ({}): {}", res ? res->status : -1, res ? res->body : "no response"));
    }
}

void send_async(std::string body, std::string summary) {
    std::call_once(g_sender_started, [] { std::thread(sender_loop).detach(); });
    {
        std::lock_guard lock(g_send_mtx);
        g_send_next = { std::move(body), std::move(summary) };
    }
    g_send_cv.notify_one();
}

// Serialize the entity (ss_serialize_local) ending at end_cf (0 = the client's current
// frame): a full update, or with baseline "diff" a delta of what changed since the
// last "store"/"diff" capture. Returns the tool's result, or nothing (status set).
std::optional<nlohmann::json> serialize(uint32_t entity_id, uint32_t end_cf, const char* baseline = "none") {
    auto tool = prom_mcp::find_tool("ss_serialize_local");
    if (!tool) {
        set_status("ss_serialize_local tool missing");
        return std::nullopt;
    }
    bool delta = std::string(baseline) == "diff";
    auto r = tool->handler({ {"entity_id", std::format("0x{:x}", entity_id)}, {"end_cf", end_cf}, {"ea", "game"},
                             {"delta", delta}, {"baseline", baseline} });
    if (!r.contains("payload_hex")) {
        set_status("capture failed: " + r.dump());
        return std::nullopt;
    }
    return r;
}

void send_capture(const nlohmann::json& r, uint32_t entity_id, int conn, uint32_t start_cf, uint32_t end_cf, const char* what) {
    int active = 0;
    for (auto& i : r["instances"])
        active += (int)i["active"].size();
    nlohmann::json body = { {"conn", conn}, {"start_cf", start_cf}, {"end_cf", end_cf},
                            {"payload_hex", r["payload_hex"]}, {"payload_bits", r["payload_bits"]} };
    send_async(body.dump(), std::format("{} entity {:x}: {} instances, {} {} states, conn {} CF {}-{}",
                                        what, entity_id, r["instances"].size(), active, start_cf ? "changed" : "active", conn, start_cf, end_cf));
}

// One-shot: stamped just past the server's frame (the client applies a packet only
// when its end CF is past the last one it applied).
void capture(uint32_t entity_id) {
    auto c = server_conn();
    if (!c) {
        set_status("no connected game server (control API 127.0.0.1:3728)");
        return;
    }
    uint32_t end_cf = c->frame + 2;
    if (auto r = serialize(entity_id, end_cf))
        send_capture(*r, entity_id, c->conn, 0, end_cf, "sent");
}

// --- Echo mode ---
// The client predicts everything its local hero does and rolls it back unless the
// server confirms it. Echo mode confirms it all, continuously: a full update first
// (which also resets the owner vars once), then every few frames a delta of just what
// changed since the previous echo (start CF = the previous echo's end CF), stamped
// with the client's own current frame (Statescript component +0x384). An empty delta
// still confirms that nothing happened up to that frame.
std::atomic<bool> g_echo_on{ false };
std::atomic<uint32_t> g_echo_entity{ 0 };
int g_echo_frames = 0;     // UI thread only
uint32_t g_echo_last_cf = 0; // end CF of the last echo; 0 = send a full update next
int g_echo_conn = 0;

// The poller's view of the live connection (its ID changes on every rejoin).
std::mutex g_poll_mtx;
std::optional<ConnInfo> g_poll_conn;
std::once_flag g_poller_started;

void poller_loop() {
    for (;;) {
        if (g_echo_on) {
            auto c = server_conn();
            std::lock_guard lock(g_poll_mtx);
            g_poll_conn = c;
        }
        std::this_thread::sleep_for(kPollInterval);
    }
}

void echo_tick() {
    if (!g_echo_on || ++g_echo_frames < kEchoEveryFrames)
        return;
    g_echo_frames = 0;
    std::optional<ConnInfo> c;
    {
        std::lock_guard lock(g_poll_mtx);
        c = g_poll_conn;
    }
    if (!c) {
        set_status("echo: waiting for a connected game server");
        return;
    }
    if (c->conn != g_echo_conn) {
        g_echo_conn = c->conn;
        g_echo_last_cf = 0;
    }
    uint32_t entity = g_echo_entity;
    bool full = g_echo_last_cf == 0;
    auto r = serialize(entity, 0, full ? "store" : "diff");
    if (!r)
        return;
    uint32_t end_cf = (*r)["end_cf"];
    uint32_t start_cf = full ? 0 : g_echo_last_cf;
    // The client applies a packet only when its end CF is past the last one.
    if (!full && end_cf <= start_cf)
        end_cf = start_cf + 1;
    g_echo_last_cf = end_cf;
    send_capture(*r, entity, c->conn, start_cf, end_cf, full ? "echo (full)" : "echo");
}

}

void queue_capture(uint32_t entity_id, int delay_frames) {
    g_pending.push_back({ entity_id, delay_frames });
    set_status(std::format("queued capture of entity {:x}", entity_id));
}

void set_echo(uint32_t entity_id, bool enabled) {
    g_echo_entity = entity_id;
    g_echo_last_cf = 0;
    if (enabled)
        std::call_once(g_poller_started, [] { std::thread(poller_loop).detach(); });
    g_echo_on = enabled;
    set_status(enabled ? std::format("echo on for entity {:x}", entity_id) : "echo off");
    LOG_CORE(Info, "[ServerBridge] echo {} (entity {:x})", enabled ? "on" : "off", entity_id);
}

bool echo_enabled() {
    return g_echo_on;
}

uint32_t echo_entity() {
    return g_echo_entity;
}

void process() {
    for (size_t i = 0; i < g_pending.size();) {
        if (--g_pending[i].frames_left > 0) {
            i++;
            continue;
        }
        uint32_t id = g_pending[i].entity_id;
        g_pending.erase(g_pending.begin() + i);
        capture(id);
    }
    echo_tick();
}

std::string last_status() {
    std::lock_guard lock(g_status_mtx);
    return g_status;
}

}
