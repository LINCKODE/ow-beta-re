#include "ServerView.h"

#include <windows.h>

#include <algorithm>
#include <cmath>
#include <deque>
#include <format>
#include <map>

#include "DebugDraw.h"
#include "../globals.h"
#include "../entity_admin.h"
#include "../game.h"
#include "../Logs/Logs.h"
#include "../Utility/SafeMemory.h"

// docs/DebugDrawing.md §5. Everything here runs on the UI thread except now_us().
namespace server_view {
namespace {

constexpr size_t HistoryCap = 256;
constexpr int64_t StaleUs = 2'000'000; // no frame for 2 s: the stream is off, shapes go

struct Header {
    uint32_t seq, frame, size;
    int64_t sent, relay, arrival;
};

struct Ent {
    uint32_t id = 0;
    std::string kind;
    uint32_t def = 0;
    int team = 0;
    std::array<float, 3> pos{};
    float yaw = 0;
};

struct Client {
    bool have_hero = false;
    std::array<float, 3> hero{};
    float yaw = 0; // radians about +y, from the scene rotation quaternion
    uint32_t cf = 0, p = 0, lead = 0;
    uint8_t flags = 0;
    float buffer = 0;
    int entities = -1;
};

std::deque<Header> g_hist;
Header g_last{};
nlohmann::json g_body;       // latest parsed body
std::map<uint32_t, Ent> g_ents;
std::string g_map;
bool g_have = false;
bool g_draw = false; // world markers (opt-in)
bool g_text = true;  // screen text
Client g_client;

// Stats.
uint64_t g_frames = 0, g_drops = 0, g_restarts = 0, g_coalesced = 0, g_bad = 0, g_bytes = 0;
int g_pending = 0; // frames taken since the last update()

std::array<float, 3> vec3(const nlohmann::json& j) {
    std::array<float, 3> v{};
    if (j.is_array())
        for (size_t i = 0; i < 3 && i < j.size(); i++) v[i] = j[i].get<float>();
    return v;
}

// The local hero: scene component 0x01 of the controller's camera-attach entity (as the Debug Draw window).
bool local_hero(std::array<float, 3>& out, float& yaw) {
    auto ea = GameEntityAdmin();
    if (!ea) return false;
    auto ctrl = ea->getEntById(ea->local_entid);
    if (!ctrl) return false;
    auto mr = ctrl->getById<Component_20_ModelReference>(0x20);
    if (!mr) return false;
    auto hero = ea->getEntById(mr->cam_attach_entid);
    if (!hero) return false;
    auto sc = hero->getById<Component_1_SceneRendering>(1);
    if (!sc) return false;
    out = { sc->position.X, sc->position.Y, sc->position.Z };
    auto q = sc->rotation; // x, y, z, w
    yaw = std::atan2(2 * (q.W * q.Y + q.X * q.Z), 1 - 2 * (q.X * q.X + q.Y * q.Y));
    return std::isfinite(out[0]) && std::isfinite(out[1]) && std::isfinite(out[2]);
}

// The client's netcode clock: *(EA+0x218) + 0x50 (docs/CommandFrames.md §2).
void read_clock(Client& c) {
    auto ea = (uintptr_t)GameEntityAdmin();
    uintptr_t timing = 0;
    if (!ea || !Utility::SafeMemory::Read(&timing, (void*)(ea + 0x218), 8) || !timing) return;
    uintptr_t clock = timing + 0x50;
    Utility::SafeMemory::Read(&c.cf, (void*)(clock + 0x10), 4);
    Utility::SafeMemory::Read(&c.buffer, (void*)(clock + 0x88), 4);
    Utility::SafeMemory::Read(&c.p, (void*)(clock + 0x94), 4);
    Utility::SafeMemory::Read(&c.lead, (void*)(clock + 0x98), 4);
    Utility::SafeMemory::Read(&c.flags, (void*)(clock + 0xa8), 1);
}

double ms(int64_t us) { return us / 1000.0; }

struct Lat {
    double last = 0, avg = 0, p95 = 0, max = 0;
};
Lat latency(int64_t Header::*to, int64_t Header::*from) {
    Lat l;
    if (g_hist.empty()) return l;
    std::vector<double> v;
    v.reserve(g_hist.size());
    for (auto& h : g_hist) v.push_back(ms(h.*to - h.*from));
    l.last = v.back();
    double sum = 0;
    for (double x : v) sum += x;
    l.avg = sum / v.size();
    std::sort(v.begin(), v.end());
    l.p95 = v[std::min(v.size() - 1, (size_t)std::ceil(v.size() * 0.95) - 1)];
    l.max = v.back();
    return l;
}

nlohmann::json lat_json(const Lat& l) {
    auto r = [](double x) { return std::round(x * 100) / 100; };
    return { {"last", r(l.last)}, {"avg", r(l.avg)}, {"p95", r(l.p95)}, {"max", r(l.max)} };
}

// Frames that arrived during the last second.
double rate() {
    if (g_hist.empty()) return 0;
    int64_t now = now_us();
    int n = 0;
    for (auto it = g_hist.rbegin(); it != g_hist.rend() && now - it->arrival <= 1'000'000; ++it) n++;
    return n;
}

void build_shapes() {
    std::vector<debug_draw::Shape> out;
    if (!g_draw || !g_have || now_us() - g_last.arrival > StaleUs) {
        debug_draw::set_stream({});
        return;
    }
    using debug_draw::Shape;
    for (auto& [id, e] : g_ents) {
        Shape s;
        s.group = "view";
        s.flags = 0x20; // unlit: exact colours
        s.pos = e.pos;
        if (e.kind == "hero") {
            s.kind = "capsule";
            s.pos[1] += 0.9f;
            s.radius = 0.35f;
            s.height = 1.1f;
            s.color = { 1, 0.1f, 0.9f, 0.6f };
            s.label = std::format("server hero {:#x}", id);
        } else {
            s.kind = "sphere";
            s.radius = 0.2f;
            s.pos[1] += 0.5f;
            s.color = e.kind == "objective" ? std::array<float, 4>{ 1, 1, 0.1f, 1 }
                    : e.kind == "placement" ? std::array<float, 4>{ 0.6f, 0.6f, 0.6f, 1 }
                                            : std::array<float, 4>{ 1, 1, 1, 1 };
            s.label = std::format("{} {:#x} def {:#x}", e.kind, id, e.def);
        }
        out.push_back(s);
    }
    // The local hero: server capsule vs the real one, a line and the error.
    auto me = g_body.find("me");
    if (me != g_body.end() && g_client.have_hero) {
        auto sp = vec3((*me)["p"]);
        auto& cp = g_client.hero;
        float dx = sp[0] - cp[0], dy = sp[1] - cp[1], dz = sp[2] - cp[2];
        float err = std::sqrt(dx * dx + dy * dy + dz * dz);
        Shape c;
        c.group = "view";
        c.kind = "capsule";
        c.pos = { cp[0], cp[1] + 0.9f, cp[2] };
        c.radius = 0.37f;
        c.height = 1.1f;
        c.color = { 0.2f, 1, 0.3f, 0.35f };
        c.label = "client hero";
        out.push_back(c);
        Shape l;
        l.group = "view";
        l.kind = "line";
        l.flags = 0x20;
        l.pos = { sp[0], sp[1] + 0.9f, sp[2] };
        l.to = c.pos;
        l.color = { 1, 1, 1, 1 };
        l.label = std::format("server - client {:.2f} m", err);
        out.push_back(l);
    }
    debug_draw::set_stream(std::move(out));
}

// Screen text (engine debug font, docs/DebugDrawing.md §5.3): the server's own lines (its txt), then the client's.
void build_text() {
    std::vector<debug_draw::HudLine> out;
    if (!g_text || !g_have || now_us() - g_last.arrival > StaleUs) {
        debug_draw::set_stream_hud({});
        return;
    }
    const std::array<float, 4> server{ 0.6f, 1, 0.6f, 1 }, client{ 0.5f, 0.85f, 1, 1 }, warn{ 1, 0.6f, 0.3f, 1 };
    for (auto& t : g_body.value("txt", nlohmann::json::array()))
        if (t.is_string()) out.push_back({ "view", t.get<std::string>(), server });
    if (auto e = g_body.find("error"); e != g_body.end()) out.push_back({ "view", "server frame error: " + e->dump(), warn });
    auto& c = g_client;
    if (c.have_hero) {
        std::string line = std::format("CLIENT hero at {:.2f} {:.2f} {:.2f}  yaw {:.0f} deg", c.hero[0], c.hero[1], c.hero[2],
                                       c.yaw * 180 / 3.14159265f);
        if (auto me = g_body.find("me"); me != g_body.end()) {
            auto sp = vec3((*me)["p"]);
            float dx = sp[0] - c.hero[0], dy = sp[1] - c.hero[1], dz = sp[2] - c.hero[2];
            line += std::format("  (server off by {:.2f} m)", std::sqrt(dx * dx + dy * dy + dz * dz));
        }
        out.push_back({ "view-client", line, client });
    } else {
        out.push_back({ "view-client", "CLIENT no local hero", client });
    }
    out.push_back({ "view-client", std::format("CLIENT CF {} ({:+} vs server)  lead {}  flags {:#x}  buffer {:.0f} ms  entities {}", c.cf,
                                                (int64_t)c.cf - (int64_t)g_last.frame, c.lead, c.flags, c.buffer * 1000, c.entities), client });
    auto l = latency(&Header::arrival, &Header::sent);
    out.push_back({ "view-client", std::format("STREAM {:.0f}/s  latency {:.2f} ms (p95 {:.2f})  drops {}  {} B", rate(), l.avg, l.p95,
                                                g_drops, g_last.size), client });
    debug_draw::set_stream_hud(std::move(out));
}

}

int64_t now_us() {
    FILETIME ft;
    GetSystemTimePreciseAsFileTime(&ft);
    uint64_t t = ((uint64_t)ft.dwHighDateTime << 32) | ft.dwLowDateTime; // 100 ns since 1601
    return (int64_t)(t / 10) - 11'644'473'600'000'000LL;
}

void on_frame(uint32_t seq, uint32_t frame, uint64_t sent_us, uint64_t relay_us, std::string body, int64_t arrival_us,
              uint32_t wire) {
    if (g_have) {
        if (seq > g_last.seq + 1) g_drops += seq - g_last.seq - 1;
        else if (seq <= g_last.seq) g_restarts++; // a new subscription / game server restart
    }
    Header h{ seq, frame, wire ? wire : (uint32_t)body.size(), (int64_t)sent_us, (int64_t)relay_us, arrival_us };
    g_last = h;
    g_hist.push_back(h);
    while (g_hist.size() > HistoryCap) g_hist.pop_front();
    g_frames++;
    g_bytes += h.size;
    g_pending++;
    g_have = true;

    auto j = nlohmann::json::parse(body, nullptr, false);
    if (j.is_discarded() || !j.is_object()) {
        g_bad++;
        return;
    }
    if (j.value("full", false)) {
        g_ents.clear();
        g_map = j.value("map", std::string{});
    }
    for (auto& e : j.value("ents", nlohmann::json::array())) {
        Ent x;
        x.id = e.value("id", 0u);
        x.kind = e.value("k", std::string{});
        x.def = e.value("d", 0u);
        x.team = e.value("tm", 0);
        x.pos = vec3(e.value("p", nlohmann::json::array()));
        x.yaw = e.value("y", 0.0f);
        g_ents[x.id] = x;
    }
    g_body = std::move(j);
}

void update() {
    if (g_pending > 1) g_coalesced += g_pending - 1;
    g_pending = 0;
    bool live = g_have && now_us() - g_last.arrival <= StaleUs;
    if (live) {
        Client c;
        c.have_hero = local_hero(c.hero, c.yaw);
        read_clock(c);
        if (auto ea = GameEntityAdmin()) c.entities = ea->entity_count;
        g_client = c;
    }
    build_shapes();
    build_text();
}

bool draw() { return g_draw; }
void set_draw(bool on) { g_draw = on; }
bool text() { return g_text; }
void set_text(bool on) { g_text = on; }

nlohmann::json snapshot(int history) {
    auto r2 = [](double x) { return std::round(x * 100) / 100; };
    nlohmann::json j;
    j["receiving"] = g_have && now_us() - g_last.arrival <= StaleUs;
    j["stats"] = {
        {"frames", g_frames}, {"rate_per_s", rate()}, {"drops", g_drops}, {"restarts", g_restarts},
        {"coalesced", g_coalesced}, {"bad_bodies", g_bad}, {"bytes", g_bytes},
        {"size_last", g_last.size},
        {"size_avg", g_frames ? r2((double)g_bytes / g_frames) : 0},
        {"latency_ms", lat_json(latency(&Header::arrival, &Header::sent))},
        {"instance_to_lobbylogic_ms", lat_json(latency(&Header::relay, &Header::sent))},
        {"lobbylogic_to_client_ms", lat_json(latency(&Header::arrival, &Header::relay))},
        {"window", g_hist.size()},
    };
    if (!g_have) return j;
    j["header"] = { {"seq", g_last.seq}, {"frame", g_last.frame}, {"sent_us", g_last.sent}, {"relay_us", g_last.relay},
                    {"arrival_us", g_last.arrival}, {"size", g_last.size}, {"age_ms", r2(ms(now_us() - g_last.arrival))} };
    j["body"] = g_body;
    j["map"] = g_map;
    j["entities_known"] = g_ents.size();
    nlohmann::json c = { {"cf", g_client.cf}, {"server_frame_p", g_client.p}, {"lead", g_client.lead},
                         {"clock_flags", g_client.flags}, {"safety_buffer_s", g_client.buffer}, {"entity_count", g_client.entities} };
    if (g_client.have_hero) {
        c["hero"] = { r2(g_client.hero[0]), r2(g_client.hero[1]), r2(g_client.hero[2]) };
        auto me = g_body.find("me");
        if (me != g_body.end()) {
            auto sp = vec3((*me)["p"]);
            float dx = sp[0] - g_client.hero[0], dy = sp[1] - g_client.hero[1], dz = sp[2] - g_client.hero[2];
            c["hero_error_m"] = r2(std::sqrt(dx * dx + dy * dy + dz * dz));
        }
    }
    c["cf_minus_server_frame"] = (int64_t)g_client.cf - (int64_t)g_last.frame;
    j["client"] = c;
    if (history > 0) {
        auto h = nlohmann::json::array();
        size_t from = g_hist.size() > (size_t)history ? g_hist.size() - history : 0;
        for (size_t i = from; i < g_hist.size(); i++) {
            auto& x = g_hist[i];
            h.push_back({ x.seq, x.frame, x.size, r2(ms(x.relay - x.sent)), r2(ms(x.arrival - x.relay)), r2(ms(x.arrival - x.sent)) });
        }
        j["history"] = { {"columns", {"seq", "frame", "size", "inst_to_logic_ms", "logic_to_client_ms", "latency_ms"}}, {"rows", h} };
    }
    return j;
}

nlohmann::json control(const nlohmann::json& args) {
    std::string action = args.value("action", std::string{});
    if (action == "hello")
        return { {"message", 32002}, {"protocol", "PrometheusLobby"}, {"body_version", 1} };
    if (action == "draw") {
        set_draw(args.value("on", true));
        return { {"draw", g_draw} };
    }
    if (action == "text") {
        set_text(args.value("on", true));
        return { {"text", g_text} };
    }
    if (action == "reset") {
        g_hist.clear();
        g_frames = g_drops = g_restarts = g_coalesced = g_bad = g_bytes = 0;
        return { {"reset", true} };
    }
    int history = 0;
    if (args.contains("history") && args["history"].is_number_integer()) history = std::clamp(args["history"].get<int>(), 0, (int)HistoryCap);
    return snapshot(history);
}

}
