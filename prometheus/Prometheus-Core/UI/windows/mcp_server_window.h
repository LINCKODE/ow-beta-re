#pragma once
#include "../window_manager/window_manager.h"
#include "../../MCP/McpServer.h"
#include "../../MCP/McpTools.h"

class mcp_server_window : public window {
    WINDOW_DEFINE(mcp_server_window, "Tools", "MCP Server", true);

public:
    inline void render() override {
        if (open_window(nullptr, 0, ImVec2(620, 360))) {
            auto& s = prom_mcp::McpServer::instance();
            bool running = s.is_running();

            ImGui::TextColored(running ? ImVec4(0.4f, 0.9f, 0.4f, 1.0f)
                                       : ImVec4(0.9f, 0.4f, 0.4f, 1.0f),
                running ? "Running on 127.0.0.1:%d" : "Not running", s.port());

            ImGui::Separator();
            ImGui::Text("Requests: %d total | %d failed | %d pending",
                s.requests_total(), s.requests_failed(), s.queue_depth());

            ImGui::Separator();
            ImGui::TextWrapped("Bridge for MCP hosts (e.g. Claude Code):");
            ImGui::BulletText("Script: bridge_mcp_prometheus.py");
            ImGui::BulletText("Requires: pip install mcp requests");
            ImGui::BulletText("Default URL: http://127.0.0.1:%d (env PROMETHEUS_MCP_URL to override)", s.port());
            ImGui::BulletText("Health endpoint: GET /mcp/health");

            ImGui::Separator();
            ImGui::Text("Writes (DANGEROUS):");
#ifdef PROMETHEUS_MCP_NO_WRITES
            // Compiled out (MCP/McpTools.h): the toggle is shown greyed out and cannot be switched on.
            ImGui::BeginDisabled();
            bool off = false;
            ImGui::Checkbox("Allow MCP write tools", &off);
            ImGui::EndDisabled();
            ImGui::SameLine();
            ImGui::TextDisabled("compiled out");
            ImGui::TextWrapped("Write tools (memory/state writes, call_function, input, resources, trace files) are "
                               "compiled out of this build. See PROMETHEUS_MCP_NO_WRITES in MCP/McpTools.h and "
                               "docs/PrometheusChanges.md before changing that: it lets any local process and the "
                               "lobby server you connect to run code in the game.");
#else
            ImGui::Checkbox("Allow MCP write tools", &prom_mcp::g_write_enabled);
            ImGui::SameLine();
            if (prom_mcp::writes_allowed()) {
                ImGui::TextColored(ImVec4(1.f, 0.6f, 0.2f, 1.f), "ENABLED");
            } else {
                ImGui::TextDisabled("disabled");
            }
#endif

            ImGui::Separator();
            ImGui::Text("Exposed tools (%d):", (int)prom_mcp::all_tools().size());
            if (ImGui::BeginChild("tools", ImVec2(0, 0), ImGuiChildFlags_Border)) {
                for (auto& t : prom_mcp::all_tools()) {
                    ImGui::Bullet();
                    ImGui::TextUnformatted(t.name.c_str());
                    if (ImGui::IsItemHovered()) {
                        ImGui::SetTooltip("%s", t.description.c_str());
                    }
                    ImGui::Indent();
                    ImGui::TextWrapped("%s", t.description.c_str());
                    ImGui::Unindent();
                }
            }
            ImGui::EndChild();
        }
        ImGui::End();
    }
};

WINDOW_REGISTER(mcp_server_window);
