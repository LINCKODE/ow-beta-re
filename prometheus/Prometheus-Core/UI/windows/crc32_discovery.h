#pragma once
#include "../window_manager/window_manager.h"
#include "../../globals.h"
#include <MinHook.h>
#include <imgui.h>
#include <vector>
#include <string>
#include <intrin.h>

// Hooks the console command CRC32 hash function (RVA 0x7F88E0) to log every
// non-empty string the game ever hashes — gives us the full set of command
// names without parsing the descriptor tables manually.
struct DiscoveredCommand {
    std::string name;
    uint32_t hash;
    uint64_t caller_rva;
    uint64_t descriptor_rva;
};

class crc32_discovery : public window {
    WINDOW_DEFINE(crc32_discovery, "Debug", "CRC32 Discovery", true);

private:
    static inline uint32_t (*ConsoleCommandHash_CRC32_orig)(const char* str) = nullptr;

    static inline std::vector<DiscoveredCommand> g_discoveredCommands;
    static inline ImGuiTextFilter g_discoveredFilter;

    static constexpr uint64_t RVA_ConsoleCommandHash_CRC32 = 0x7F88E0;

    static uint32_t ConsoleCommandHash_CRC32_hook(const char* str) {
        uint32_t hash = 0;
        if (ConsoleCommandHash_CRC32_orig) {
            hash = ConsoleCommandHash_CRC32_orig(str);
        }

        if (str && str[0] != '\0') {
            uint64_t caller = (uint64_t)_ReturnAddress();
            uint64_t caller_rva = caller - globals::gameBase;

            // Skip ConsoleCommandHandler (0x7E6000-0x7E7000) — only want descriptor initializers
            if (caller_rva < 0x7E6000 || caller_rva > 0x7E7000) {
                bool found = false;
                for (const auto& cmd : g_discoveredCommands) {
                    if (cmd.name == str) {
                        found = true;
                        break;
                    }
                }

                if (!found) {
                    DiscoveredCommand dc;
                    dc.name = str;
                    dc.hash = hash;
                    dc.caller_rva = caller_rva;
                    dc.descriptor_rva = 0;
                    g_discoveredCommands.push_back(dc);
                }
            }
        }

        return hash;
    }

public:
    static const std::vector<DiscoveredCommand>& getDiscoveredCommands() {
        return g_discoveredCommands;
    }

    inline void preStartInitialize() override {
        MH_VERIFY(MH_CreateHook(
            (PVOID)(globals::gameBase + RVA_ConsoleCommandHash_CRC32),
            (PVOID)ConsoleCommandHash_CRC32_hook,
            (PVOID*)&ConsoleCommandHash_CRC32_orig));
        MH_VERIFY(MH_EnableHook((PVOID)(globals::gameBase + RVA_ConsoleCommandHash_CRC32)));
    }

    inline void render() override {
        if (open_window()) {
            ImGui::Text("CRC32 hash entries captured at runtime: %d", (int)g_discoveredCommands.size());
            ImGui::SameLine();
            if (ImGui::Button("Clear")) {
                g_discoveredCommands.clear();
            }
            ImGui::SameLine();
            if (ImGui::Button("Export to File")) {
                FILE* f = fopen("discovered_commands.txt", "w");
                if (f) {
                    fprintf(f, "%-40s %-10s %-12s %-6s\n", "Name", "Hash", "Caller RVA", "Bucket");
                    fprintf(f, "%-40s %-10s %-12s %-6s\n", "----", "----", "----------", "------");
                    for (const auto& dc : g_discoveredCommands) {
                        fprintf(f, "%-40s %08X   %08llX     %d\n",
                            dc.name.c_str(), dc.hash, dc.caller_rva, dc.hash & 0xFF);
                    }
                    fclose(f);
                }
            }

            g_discoveredFilter.Draw("Filter##discovered");

            if (ImGui::BeginTable("DiscoveredTable", 4,
                ImGuiTableFlags_ScrollY | ImGuiTableFlags_Sortable |
                ImGuiTableFlags_Resizable | ImGuiTableFlags_BordersH |
                ImGuiTableFlags_HighlightHoveredColumn | ImGuiTableFlags_RowBg,
                ImVec2(0, 0))) {

                ImGui::TableSetupColumn("Name", ImGuiTableColumnFlags_WidthStretch);
                ImGui::TableSetupColumn("Hash", ImGuiTableColumnFlags_WidthFixed, 90.0f);
                ImGui::TableSetupColumn("Caller RVA", ImGuiTableColumnFlags_WidthFixed, 100.0f);
                ImGui::TableSetupColumn("Bucket", ImGuiTableColumnFlags_WidthFixed, 50.0f);
                ImGui::TableSetupScrollFreeze(0, 1);
                ImGui::TableHeadersRow();

                for (const auto& dc : g_discoveredCommands) {
                    if (!g_discoveredFilter.PassFilter(dc.name.c_str())) continue;

                    ImGui::TableNextRow();

                    ImGui::TableNextColumn();
                    ImGui::Text("%s", dc.name.c_str());

                    ImGui::TableNextColumn();
                    ImGui::Text("%08X", dc.hash);

                    ImGui::TableNextColumn();
                    ImGui::Text("%08llX", dc.caller_rva);

                    ImGui::TableNextColumn();
                    ImGui::Text("%d", dc.hash & 0xFF);
                }

                ImGui::EndTable();
            }
        }
        ImGui::End();
    }
};

WINDOW_REGISTER(crc32_discovery);
