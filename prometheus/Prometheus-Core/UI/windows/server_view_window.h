#pragma once
#include "../window_manager/window_manager.h"
#include "../../Systems/ServerView.h"
#include <format>

// The live server view (docs/DebugDrawing.md §5): the instance server's state every tick next to the client's own,
// and the stream's timing. Start it with !serverview on in the lobby chat.
class server_view_window : public window {
    WINDOW_DEFINE(server_view_window, "Tools", "Server View", true);

    static std::string str(const nlohmann::json& j) {
        if (j.is_null()) return "-";
        if (j.is_string()) return j.get<std::string>();
        return j.dump();
    }
    static std::string pos(const nlohmann::json& p) {
        if (!p.is_array() || p.size() < 3) return "-";
        return std::format("{:.2f} {:.2f} {:.2f}", p[0].get<double>(), p[1].get<double>(), p[2].get<double>());
    }
    static void row(const char* name, const std::string& server, const std::string& client) {
        ImGui::TableNextRow();
        ImGui::TableNextColumn();
        ImGui::TextDisabled("%s", name);
        ImGui::TableNextColumn();
        ImGui::TextUnformatted(server.c_str());
        ImGui::TableNextColumn();
        ImGui::TextUnformatted(client.c_str());
    }
    static std::string lat(const nlohmann::json& l) {
        return std::format("{:.2f} / {:.2f} / {:.2f} / {:.2f}", l.value("last", 0.0), l.value("avg", 0.0), l.value("p95", 0.0),
                           l.value("max", 0.0));
    }

public:
    inline void render() override {
        if (open_window(nullptr, 0, ImVec2(480, 520))) {
            auto s = server_view::snapshot();
            bool text = server_view::text();
            if (ImGui::Checkbox("Screen text", &text)) server_view::set_text(text);
            ImGui::SameLine();
            bool draw = server_view::draw();
            if (ImGui::Checkbox("World markers", &draw)) server_view::set_draw(draw);
            ImGui::SameLine();
            if (ImGui::SmallButton("Reset stats")) server_view::control({ {"action", "reset"} });
            ImGui::SameLine();
            bool live = s.value("receiving", false);
            ImGui::TextColored(live ? ImVec4(0.4f, 1, 0.4f, 1) : ImVec4(1, 0.5f, 0.3f, 1), "%s",
                               live ? "receiving" : "no stream (!serverview on in chat)");

            auto& st = s["stats"];
            ImGui::SeparatorText("Stream");
            ImGui::Text("%.0f frames/s   %llu frames   drops %llu   coalesced %llu   restarts %llu", st.value("rate_per_s", 0.0),
                        (unsigned long long)st.value("frames", 0ull), (unsigned long long)st.value("drops", 0ull),
                        (unsigned long long)st.value("coalesced", 0ull), (unsigned long long)st.value("restarts", 0ull));
            ImGui::Text("size last %u B, avg %.0f B", st.value("size_last", 0u), st.value("size_avg", 0.0));
            ImGui::TextDisabled("latency ms (last / avg / p95 / max over %d frames)", st.value("window", 0));
            ImGui::Text("total             %s", lat(st["latency_ms"]).c_str());
            ImGui::Text("inst -> logic     %s", lat(st["instance_to_lobbylogic_ms"]).c_str());
            ImGui::Text("logic -> client   %s", lat(st["lobbylogic_to_client_ms"]).c_str());

            if (s.contains("header")) {
                auto& h = s["header"];
                auto& b = s["body"];
                auto& c = s["client"];
                ImGui::SeparatorText(std::format("{}  (age {:.0f} ms)", s.value("map", std::string{}), h.value("age_ms", 0.0)).c_str());
                if (ImGui::BeginTable("sv", 3, ImGuiTableFlags_SizingStretchProp | ImGuiTableFlags_RowBg)) {
                    ImGui::TableSetupColumn("");
                    ImGui::TableSetupColumn("server");
                    ImGui::TableSetupColumn("client");
                    ImGui::TableHeadersRow();
                    row("command frame", str(h["frame"]), std::format("{} ({:+})", str(c["cf"]), c.value("cf_minus_server_frame", 0ll)));
                    nlohmann::json clk = b.value("clk", nlohmann::json::object());
                    row("lead", std::format("{} (min {})", str(clk["lead"]), str(clk["min"])), str(c["lead"]));
                    row("clock flags", std::format("{:#x}", clk.value("fl", 0)), std::format("{:#x}", c.value("clock_flags", 0)));
                    row("P / input CF", str(clk["in"]), str(c["server_frame_p"]));
                    row("resyncs / buffer", str(clk["rs"]), std::format("{:.3f} s", c.value("safety_buffer_s", 0.0)));
                    row("player", str(clk["st"]), "");
                    std::string me = b.contains("me") ? pos(b["me"]["p"]) : "-";
                    row("hero position", me, c.contains("hero") ? pos(c["hero"]) : "-");
                    if (c.contains("hero_error_m")) row("hero error", std::format("{:.2f} m", c["hero_error_m"].get<double>()), "");
                    if (b.contains("xf")) row("21601 (load)", std::format("{} @ {}", pos(b["xf"]["p"]), str(b["xf"]["f"])), "");
                    row("entities", std::format("{} known, ecs {}", str(s["entities_known"]), str(b["ecs"])), str(c["entity_count"]));
                    row("server time", std::format("{} ms, late ticks {}", str(b["ms"]), str(b["late"])), "");
                    ImGui::EndTable();
                }
                if (b.contains("game")) {
                    auto& g = b["game"];
                    ImGui::SeparatorText("Match");
                    ImGui::Text("phase %s, %.1f s in phase", str(g["ph"]).c_str(), g.value("pf", 0) * 0.016);
                    for (auto& p : g.value("pts", nlohmann::json::array()))
                        ImGui::Text("point %c: %s owner %d capturing %d%s %.0f%%", 'A' + p.value("o", 0), p.value("on", false) ? "on " : "off",
                                    p.value("own", 0), p.value("cap", 0), p.value("ct", false) ? " contested" : "", p.value("p", 0.0) * 100);
                }
            }
        }
        ImGui::End();
    }
};

WINDOW_REGISTER(server_view_window);
