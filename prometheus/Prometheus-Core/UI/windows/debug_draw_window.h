#pragma once
#include "../window_manager/window_manager.h"
#include "../../Systems/DebugDraw.h"
#include "../../entity_admin.h"
#include "../../game.h"
#include "../../ImguiRenderer.h"
#include <cmath>
#include <format>

// World-space debug shapes through the engine's PrimDraw (docs/DebugDrawing.md): the game server's
// view (GET /debug/draw on the instance control API), the client's own hero next to it, test shapes.
class debug_draw_window : public window {
    WINDOW_DEFINE(debug_draw_window, "Tools", "Debug Draw", true);

    bool _client_hero = true;
    bool _labels = false;
    char _host[64] = "127.0.0.1";
    int _port = 3729;
    int _conn = 0;
    int _interval = 250;
    int _slot = 0x440;
    float _test_size = 1.0f;

    // The local hero's position (scene component 0x01 of the controller's camera-attach entity).
    static bool local_hero_pos(std::array<float, 3>& out) {
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
        return std::isfinite(out[0]) && std::isfinite(out[1]) && std::isfinite(out[2]);
    }

    static ImU32 col32(const std::array<float, 4>& c) {
        return IM_COL32((int)(c[0] * 255), (int)(c[1] * 255), (int)(c[2] * 255), 255);
    }

    void test_shapes(const std::array<float, 3>& p) {
        using debug_draw::Shape;
        float d = 3.0f * _test_size, r = 0.5f * _test_size;
        auto at = [&](float dx) { return std::array<float, 3>{ p[0] + dx, p[1] + 1.0f, p[2] + 2.0f * _test_size }; };
        Shape s;
        s.group = "test";
        s.kind = "sphere"; s.pos = at(-2 * d); s.radius = r; s.color = { 1, 0.2f, 0.2f, 1 }; s.label = "sphere r"; debug_draw::add_local(s);
        s.kind = "box"; s.pos = at(-d); s.half = { r, r, r }; s.color = { 0.2f, 1, 0.2f, 1 }; s.label = "box half r"; debug_draw::add_local(s);
        s.kind = "capsule"; s.pos = at(0); s.height = 2 * r; s.color = { 0.2f, 0.4f, 1, 1 }; s.label = "capsule r, h 2r"; debug_draw::add_local(s);
        s.kind = "cylinder"; s.pos = at(d); s.pos[1] -= r; s.height = 2 * r; s.color = { 1, 1, 0.2f, 1 }; s.label = "cylinder r, h 2r"; debug_draw::add_local(s);
        s.kind = "line"; s.pos = at(2 * d); s.to = s.pos; s.to[1] += 2 * r; s.color = { 1, 1, 1, 1 }; s.label = "line up 2r"; debug_draw::add_local(s);
    }

public:
    inline void render() override {
        if (open_window(nullptr, 0, ImVec2(460, 520))) {
            bool on = debug_draw::enabled();
            if (ImGui::Checkbox("Draw", &on)) debug_draw::set_enabled(on);
            ImGui::SameLine();
            ImGui::Checkbox("Client hero", &_client_hero);
            ImGui::SameLine();
            ImGui::Checkbox("Labels", &_labels);
            auto st = debug_draw::stats();
            ImGui::TextDisabled("%d primitives last frame, %llu frames", st["last_frame_primitives"].get<int>(),
                                (unsigned long long)st["frames"].get<uint64_t>());

            ImGui::SeparatorText("Game server (GET /debug/draw)");
            auto cfg = debug_draw::poll_config();
            bool poll = cfg.on;
            if (ImGui::Checkbox("Poll", &poll)) {
                cfg.on = poll;
                cfg.host = _host;
                cfg.port = _port;
                cfg.conn = (uint32_t)_conn;
                cfg.interval_ms = _interval;
                debug_draw::set_poll(cfg);
            }
            ImGui::SameLine();
            ImGui::SetNextItemWidth(120);
            ImGui::InputText("##host", _host, sizeof(_host));
            ImGui::SameLine();
            ImGui::SetNextItemWidth(70);
            ImGui::InputInt("##port", &_port, 0);
            ImGui::SameLine();
            ImGui::SetNextItemWidth(50);
            ImGui::InputInt("conn", &_conn, 0);
            ImGui::TextWrapped("%s", debug_draw::poll_status().c_str());

            ImGui::SeparatorText("Screen (engine debug overlays)");
            bool scr = debug_draw::screen_enabled();
            if (ImGui::Checkbox("Prometheus HUD", &scr)) debug_draw::set_screen_enabled(scr);
            ImGui::SameLine();
            bool fps = debug_draw::fps_line();
            if (ImGui::Checkbox("FPS / RTT line", &fps)) debug_draw::set_fps_line(fps);
            if (ImGui::BeginTable("overlays", 3, ImGuiTableFlags_SizingFixedFit)) {
                for (auto& e : debug_draw::engine_overlays()) {
                    ImGui::TableNextRow();
                    ImGui::TableNextColumn();
                    bool en = e.enabled;
                    ImGui::PushID(e.code.c_str());
                    if (ImGui::Checkbox(e.code.c_str(), &en)) debug_draw::set_engine_overlay(e.code, en);
                    ImGui::TableNextColumn();
                    if (e.code == "fps" || e.code == "il") {
                        int mode = e.mode;
                        ImGui::SetNextItemWidth(60);
                        if (ImGui::InputInt("##mode", &mode, 1, 1)) debug_draw::set_engine_overlay(e.code, e.enabled, std::max(0, mode));
                    }
                    ImGui::TableNextColumn();
                    ImGui::TextDisabled("%s", e.help.c_str());
                    ImGui::PopID();
                }
                ImGui::EndTable();
            }

            ImGui::SeparatorText("Legend");
            for (auto& g : debug_draw::groups()) {
                bool vis = g.visible;
                ImGui::PushID(g.name.c_str());
                if (ImGui::Checkbox("##vis", &vis)) debug_draw::set_group_visible(g.name, vis);
                ImGui::SameLine();
                ImGui::ColorButton("##c", ImVec4(g.color[0], g.color[1], g.color[2], 1), ImGuiColorEditFlags_NoTooltip, ImVec2(14, 14));
                ImGui::SameLine();
                ImGui::Text("%s (%d)%s%s", g.name.c_str(), g.count, g.label.empty() ? "" : ": ", g.label.c_str());
                ImGui::PopID();
            }

            ImGui::SeparatorText("Test shapes (in front of the hero)");
            ImGui::SetNextItemWidth(100);
            ImGui::InputFloat("size", &_test_size, 0.25f);
            ImGui::SameLine();
            std::array<float, 3> p{};
            bool have = local_hero_pos(p);
            if (ImGui::Button("Add") && have) test_shapes(p);
            ImGui::SameLine();
            if (ImGui::Button("Clear")) debug_draw::clear_local("test");
            ImGui::SetNextItemWidth(100);
            ImGui::InputInt("PrimDraw slot", &_slot, 8, 8, ImGuiInputTextFlags_CharsHexadecimal);
            ImGui::SameLine();
            if (ImGui::Button("Add slot") && have) {
                debug_draw::Shape s;
                s.group = "test";
                s.kind = "slot";
                s.slot = (uint32_t)_slot;
                s.pos = { p[0], p[1] + 1.0f, p[2] + 2.0f };
                s.to = { p[0], p[1] + 2.0f, p[2] + 2.0f };
                s.radius = 0.5f * _test_size;
                s.height = _test_size;
                s.half = { 0.5f * _test_size, 0.5f * _test_size, 0.5f * _test_size };
                s.color = { 1, 0.5f, 0, 1 };
                s.label = std::format("slot {:#x}", _slot);
                debug_draw::add_local(s);
            }
            if (have) ImGui::TextDisabled("hero at %.2f %.2f %.2f", p[0], p[1], p[2]);
            else ImGui::TextDisabled("no local hero");

            // The client's own hero, next to the server's idea of it (group "heroes").
            debug_draw::clear_local("client");
            if (_client_hero && have) {
                debug_draw::Shape s;
                s.group = "client";
                s.kind = "capsule";
                s.pos = { p[0], p[1] + 0.9f, p[2] };
                s.radius = 0.37f;
                s.height = 1.1f;
                s.color = { 0.2f, 1, 0.3f, 0.35f };
                s.label = "client hero";
                debug_draw::add_local(s);
            }
        }
        ImGui::End();

        if (_labels && debug_draw::enabled()) draw_labels();
    }

    void draw_labels() {
        auto ea = GameEntityAdmin();
        if (!ea) return;
        auto camera = ea->vfptr->GetCameraComponent(ea);
        if (!camera) return;
        auto groups = debug_draw::groups();
        auto visible = [&](const std::string& n) {
            for (auto& g : groups)
                if (g.name == n) return g.visible;
            return true;
        };
        auto dl = ImGui::GetForegroundDrawList(ImGui::GetMainViewport());
        auto size = ImGui::GetMainViewport()->Size;
        for (auto& s : debug_draw::shapes()) {
            if (s.label.empty() || !visible(s.group)) continue;
            Vector4 in{};
            in.X = s.pos[0]; in.Y = s.pos[1]; in.Z = s.pos[2]; in.W = 1;
            auto o = camera->WorldToScreen(in);
            if (!std::isfinite(o.X) || !std::isfinite(o.Y) || o.X < 0 || o.Y < 0 || o.X > size.x || o.Y > size.y) continue;
            dl->AddText(ImVec2(o.X + 1, o.Y + 1), IM_COL32(0, 0, 0, 200), s.label.c_str());
            dl->AddText(ImVec2(o.X, o.Y), col32(s.color), s.label.c_str());
        }
    }
};

WINDOW_REGISTER(debug_draw_window);
