#include "statescript_window.h"
#include "stringhash_library.h"
#include "stu_explorer.h"
#include "statescript_info.h"
#include "../external/imnodes/imnodes_internal.h"
#include "cvexpression_viewer.h"
#include "CVD_List.h"
#include "STU_Editable.h"
#include "Network/ServerBridge.h"
#include <algorithm>
#include <cfloat>
#include <functional>

void statescript_window::render() {
    std::string title;
    if (!_instance_invalid)
        title = std::format("{:x} on Entity {:x}", _curr_instance->script_id, _curr_instance->ss_inner->component_ref->base.entity_backref->entity_id);
    else
        title = "Invalid Statescript Graph";
    if (open_window(title.c_str(), ImGuiWindowFlags_MenuBar, ImVec2(1500, 800))) {
        if (_instance_invalid) {
            ImGui::Text("Invalid instance!");
            ImGui::End();
            return;
        }

        if (ImGui::BeginMenuBar()) {
            if (ImGui::BeginMenu("Graph View")) {
                ImGui::MenuItem("Addresses", nullptr, &_display_settings.addresses);
                ImGui::MenuItem("STU Args", nullptr, &_display_settings.stu_args);
                ImGui::MenuItem("Inspector", nullptr, &_show_inspector);

                ImGui::EndMenu();
            }
            if (ImGui::BeginMenu("Tools")) {
                if (ImGui::MenuItem("Sort Graph")) {
                    sort_graph();
                }
                if (ImGui::MenuItem("Re-Nav to node")) {
                    nav_to_node();
                }
                if (ImGui::MenuItem("Reset Zoom")) {
                    set_zoom(1.0f, _canvas_center);
                }

                ImGui::EndMenu();
            }
            if (ImGui::BeginMenu("Modals")) {
                if (ImGui::MenuItem("Information")) {
                    this->dock_item_right(statescript_info::get_latest_or_create(this, true, true), 0.8f);
                }

                if (ImGui::MenuItem("Instance VarBag")) {
                    this->dock_item_down(CVD_List::get_latest_or_create(this, true, true)->set(_curr_instance->rid_instance_varbag), 0.7f);
                }

                if (ImGui::MenuItem("Extra VarBag (+0x280)", nullptr, false, _curr_instance->third_inst_varbag != nullptr)) {
                    this->dock_item_down(CVD_List::get_latest_or_create(this, true, true)->set(_curr_instance->third_inst_varbag), 0.7f);
                }

                ImGui::EndMenu();
            }

            ImGui::EndMenuBar();
        }

        ImNodes::EditorContextSet(_nodes_context);

        // Graph on the left, the selected node's inspector on the right.
        const float inspector_width = 380.0f;
        ImGui::BeginChild("##graph", ImVec2(_show_inspector ? -inspector_width : 0.0f, 0.0f));

        // Alt + left drag pans like the middle mouse button.
        ImNodes::GetIO().EmulateThreeButtonMouse.Modifier = &ImGui::GetIO().KeyAlt;

        static const ImNodesStyle base_style = [] {
            ImNodesStyle s = ImNodes::GetStyle();
            s.PinCircleRadius = 6;
            s.PinTriangleSideLength = 8;
            s.NodeBorderThickness = 3;
            return s;
        }();
        auto& style = ImNodes::GetStyle();
        style = base_style;
        style.GridSpacing *= _zoom;
        style.NodeCornerRounding *= _zoom;
        style.NodePadding = style.NodePadding * _zoom;
        style.NodeBorderThickness *= _zoom;
        style.LinkThickness *= _zoom;
        style.LinkHoverDistance *= _zoom;
        style.PinCircleRadius *= _zoom;
        style.PinQuadSideLength *= _zoom;
        style.PinTriangleSideLength *= _zoom;
        style.PinLineThickness *= _zoom;
        style.PinHoverRadius *= _zoom;
        style.PinOffset *= _zoom;

        ImNodes::BeginNodeEditor();
        ImGui::SetWindowFontScale(_zoom);
        ImGui::PushStyleVar(ImGuiStyleVar_FramePadding, ImGui::GetStyle().FramePadding * _zoom);
        ImGui::PushStyleVar(ImGuiStyleVar_ItemSpacing, ImGui::GetStyle().ItemSpacing * _zoom);
        ImGui::PushStyleVar(ImGuiStyleVar_ItemInnerSpacing, ImGui::GetStyle().ItemInnerSpacing * _zoom);

        int link_counter = 0;
        auto script_state = statescript_logger::GetScriptState(_curr_instance);
        for (auto node : _curr_instance->graph->m_nodes) {
            ImGui::PushID(node);
            auto type = node->get_type();
            auto stu = node->graph_node.base.vfptr->GetSTUInfo();

            switch (type) {
            case StatescriptNodeType_Action:
                ImNodes::PushColorStyle(ImNodesCol_TitleBar, IM_COL32(0xef, 0x47, 0x6f, 200));
                ImNodes::PushColorStyle(ImNodesCol_TitleBarSelected, IM_COL32(0xef, 0x47, 0x6f, 100));
                break;
            case StatescriptNodeType_Entry:
                ImNodes::PushColorStyle(ImNodesCol_TitleBar, IM_COL32(6, 0xd6, 0xa0, 200));
                ImNodes::PushColorStyle(ImNodesCol_TitleBarSelected, IM_COL32(6, 0xd6, 0xa0, 100));
                break;
            case StatescriptNodeType_State: {
                ImNodes::PushColorStyle(ImNodesCol_TitleBar, IM_COL32(0xff, 0xd1, 0x66, 200));
                ImNodes::PushColorStyle(ImNodesCol_TitleBarSelected, IM_COL32(0xff, 0xd1, 0x66, 100));
                ImGui::PushStyleColor(ImGuiCol_Text, ImVec4(0, 0, 0, 255));
                break;
            }
            case StatescriptNodeType_Condition:
                ImNodes::PushColorStyle(ImNodesCol_TitleBar, IM_COL32(0x11, 0x8a, 0xb2, 200));
                ImNodes::PushColorStyle(ImNodesCol_TitleBarSelected, IM_COL32(0x11, 0x8a, 0xb2, 100));
                break;
            case StatescriptNodeType_Other:
                ImNodes::PushColorStyle(ImNodesCol_TitleBar, IM_COL32(7, 0x3b, 0x4c, 200));
                ImNodes::PushColorStyle(ImNodesCol_TitleBarSelected, IM_COL32(7, 0x3b, 0x4c, 100));
                break;
            case StatescriptNodeType_Unknown:
                ImNodes::PushColorStyle(ImNodesCol_TitleBar, IM_COL32(0, 0, 0, 0));
                ImNodes::PushColorStyle(ImNodesCol_TitleBarSelected, IM_COL32(0, 0, 0, 0));
                break;
            }

            auto& node_state = script_state[node->idx_in_nodes];
            if (node->m_isState) {
                if (node_state.is_finish_state)
                    ImNodes::PushColorStyle(ImNodesCol_NodeOutline, imgui_helpers::color_fade(ImVec4(255, 0, 0, 255), ImVec4(50, 50, 50, 255), 600, _curr_instance->ss_inner->time_ms - node_state.change_timestamp));
                else
                    ImNodes::PushColorStyle(ImNodesCol_NodeOutline, IM_COL32(0, 255, 0, 255));
            }
            else
                ImNodes::PushColorStyle(ImNodesCol_NodeOutline, imgui_helpers::color_fade(ImVec4(255, 0, 255, 255), ImVec4(50, 50, 50, 255), 600, _curr_instance->ss_inner->time_ms - node_state.change_timestamp));

            ImNodes::BeginNode(node->idx_in_nodes);
            ImNodes::BeginNodeTitleBar();
            if (type == StatescriptNodeType_Entry || type == StatescriptNodeType_Other || type == StatescriptNodeType_Action) {
                if (imgui_helpers::TooltipButton(EMOJI_PLAY "##action", "Execute as Action")) {
                    _curr_instance->ExecuteNode(node->idx_in_nodes);
                }
                ImGui::SameLine();
                if (imgui_helpers::TooltipButton(EMOJI_SHARE "##action_server", "Execute as Action + apply on the game server (kept instead of rolled back)")) {
                    _curr_instance->ExecuteNode(node->idx_in_nodes);
                    ServerBridge::queue_capture(owner_entity_id());
                }
                ImGui::SameLine();
            }
            if (node->m_isState) {
                bool is_running = !script_state[node->idx_in_nodes].is_finish_state;
                if (imgui_helpers::TooltipButton(is_running ? EMOJI_STOP : EMOJI_PLAY, "Run / Stop as State")) {
                    _curr_instance->StartStopState(node->idx_in_nodes, !is_running);
                }
                ImGui::SameLine();
                if (imgui_helpers::TooltipButton(EMOJI_SHARE "##state_server", "Run / Stop as State + apply on the game server (kept instead of rolled back)")) {
                    _curr_instance->StartStopState(node->idx_in_nodes, !is_running);
                    ServerBridge::queue_capture(owner_entity_id());
                }
                ImGui::SameLine();
            }
            if (node->m_clientOnly) {
                ImGui::PushFont(imgui_helpers::BoldFont);
                ImGui::Text("[CO]");
                ImGui::SameLine();
                ImGui::PopFont();
            }
            if (node->m_serverOnly) {
                ImGui::PushFont(imgui_helpers::BoldFont);
                ImGui::Text("[SERVER]");
                ImGui::SameLine();
                ImGui::PopFont();
            }
            print_node_location(node);
            ImGui::SameLine();
            imgui_helpers::display_type(stu->Hash, false, true, true);
            if (node->m_isState && node->graph_node.base.vfptr->rtti.base.rtti_assignable_to((__int64)node, reinterpret_cast<InheritanceInfo*>(globals::gameBase + STU_RTTI::STUStatescriptStateUXBase))) {
                ImGui::PushFont(imgui_helpers::BoldFont);
                ImGui::SameLine();
                ImGui::Text("(UX)");
                ImGui::PopFont();
            }
            ImNodes::EndNodeTitleBar();
            ImNodes::PopColorStyle();
            ImNodes::PopColorStyle();
            ImNodes::PopColorStyle();
            //ImNodes::PopColorStyle();
            if (node->m_isState) {
                ImGui::PopStyleColor();
            }

            if (type != StatescriptNodeType_Entry) {
                ImNodes::BeginInputAttribute(0xE000 | node->idx_in_nodes, ImNodesPinShape_CircleFilled);
                ImGui::TextUnformatted("In");
                ImNodes::EndInputAttribute();
            }

            auto connections = node->get_output_plugs();
            int outplug_i = 0;
            for (auto out_node : connections) {
                bool empty = true;
                for (auto& item : out_node.second) {
                    if (item->m_links.count() != 0) {
                        empty = false;
                        break;
                    }
                }
                if (empty)
                    continue;
                int outplug_id = 0xD000 | node->idx_in_nodes | outplug_i++ << 20;
                ImNodes::BeginOutputAttribute(outplug_id, ImNodesPinShape_CircleFilled);
                std::string plug_name;
                if (stringhash_library::hashes.find(out_node.first) != stringhash_library::hashes.end()) {
                    plug_name = stringhash_library::hashes[out_node.first];
                }
                else {
                    plug_name = std::format("{:x}", out_node.first);
                }
                if (stringhash_library::comments.find(out_node.first) != stringhash_library::comments.end()) {
                    plug_name += " (" + stringhash_library::comments[out_node.first] + ")";
                }
                float indent_size = ImNodes::GetNodeDimensions(node->idx_in_nodes).x - ImGui::CalcTextSize(plug_name.c_str()).x - 20 * _zoom;
                if (indent_size < 10 * _zoom)
                    indent_size = 10 * _zoom;

                ImGui::Indent(indent_size);
                ImGui::PushStyleColor(ImGuiCol_Text, ImVec4(100, 255, 0, 255));
                ImGui::TextUnformatted(plug_name.c_str());
                ImGui::PopStyleColor();
                ImGui::Unindent();

                //display_type(out_node.first, true, false, false);
                auto plug_type = out_node.second.front()->base.to_editable().struct_info;
                bool plug_has_color = false;
                ImNodes::PushColorStyle(ImNodesCol_LinkSelected, IM_COL32(180, 180, 180, 255));
                if (plug_type->AssignableToHashes({ STU_NAME::Subgraph_Plug_, STU_NAME::Subgraph_Plug_2_ })) {
                    plug_has_color = true;
                    int col = IM_COL32(110, 70, 70, 255);
                    if (plug_type->AssignableToHashes({ STU_NAME::ClientOnly_Subgraph_Plug_, STU_NAME::ClientOnly_Subgraph_Plug_2_ })) {
                        col = IM_COL32(0, 120, 0, 255);
                    }
                    else if (plug_type->AssignableToHashes({STU_NAME::ServerOnly_Subgraph_Plug_})) {
                        col = IM_COL32(255, 20, 220, 255);
                    }
                    ImNodes::PushColorStyle(ImNodesCol_Link, col);
                    ImNodes::PushColorStyle(ImNodesCol_LinkHovered, (col & ~IM_COL32_A_MASK) | (150 << IM_COL32_A_SHIFT));
                }

                if (out_node.second.size() == 1) {
                    for (auto link : out_node.second[0]->m_links) {
                        ImNodes::Link(0xC000 | link_counter++, outplug_id, 0xE000 | link->m_inputPlug->plug_base.m_parentNode->idx_in_nodes);
                    }
                }
                else {
                    int plug_index = 0;
                    for (auto out_path : out_node.second) {
                        ImNodes::EndOutputAttribute();
                        outplug_id = 0xD000 | node->idx_in_nodes | outplug_i++ << 20;
                        ImNodes::BeginOutputAttribute(outplug_id, ImNodesPinShape_Circle);

                        ImGui::Indent(indent_size);
                        ImGui::Text("Index %x", plug_index++);
                        ImGui::Unindent();

                        for (auto link : out_path->m_links) {
                            ImNodes::Link(0xC000 | link_counter++, outplug_id, 0xE000 | link->m_inputPlug->plug_base.m_parentNode->idx_in_nodes);
                        }
                    }
                }
                ImNodes::EndOutputAttribute();
                ImNodes::PopColorStyle();
                if (plug_has_color) {
                    ImNodes::PopColorStyle();
                    ImNodes::PopColorStyle();
                }
            }


            if (_display_settings.addresses) {
                display_addr((__int64)node, "STU Instance");
                ImGui::SameLine();
                if (ImGui::Button("Explore"))
                    stu_explorer::get_latest_or_create(this)->navigate_to(node->graph_node.base.vfptr->GetSTUInfo(), (__int64)node, _curr_instance);

                if (type == StatescriptNodeType_Action) {
                    auto impl = GetStatescriptActionImplNode(_curr_instance, (STUStatescriptAction*)node);
                    display_addr((__int64)impl, "Action Implementation");
                }
                else if (type == StatescriptNodeType_State) {
                    auto impl = _curr_instance->stateImplFromSTU((STUStatescriptState*)node);
                    if (impl) {
                        display_addr((__int64)impl, "State Implementation");
                    }
                    else {
                        ImGui::Text("State Impl not allocated.");
                    }
                }
            }
            if (_display_settings.stu_args) {
                render_node_args(node);
            }

            ImNodes::EndNode();
            if (ImNodes::NumSelectedNodes() == 1 && ImNodes::IsNodeSelected(node->idx_in_nodes) && _selected_node != node->idx_in_nodes) {
                _selected_node = node->idx_in_nodes;
                /*auto explorer = stu_explorer::get_latest(this);
                if (explorer) {
                    explorer->navigate_to(node->graph_node.base.vfptr->GetSTUInfo(), (__int64)node);
                }*/
            }
            ImGui::PopID();
        }

        ImGui::PopStyleVar(3);
        ImNodes::MiniMap(0.2f, ImNodesMiniMapLocation_BottomRight);
        ImNodes::EndNodeEditor();
        style = base_style;
        _canvas_origin = GImNodes->CanvasOriginScreenSpace;
        _canvas_center = GImNodes->CanvasRectScreenSpace.GetCenter();

        // Mouse wheel zooms around the cursor. The editor is a child window, so check
        // hovering including children (ImNodes::IsEditorHovered only works inside it).
        const float wheel = ImGui::GetIO().MouseWheel;
        if (wheel != 0.f && ImGui::IsWindowHovered(ImGuiHoveredFlags_ChildWindows)
            && GImNodes->CanvasRectScreenSpace.Contains(ImGui::GetMousePos())) {
            set_zoom(_zoom * powf(1.15f, wheel), ImGui::GetMousePos());
        }

        if (_sort_pending) {
            _sort_pending = false;
            sort_graph();
            if (_nav_node)
                nav_to_node();
        }
        ImGui::EndChild();

        if (_show_inspector) {
            ImGui::SameLine();
            ImGui::BeginChild("##inspector", ImVec2(0.0f, 0.0f), ImGuiChildFlags_Borders);
            render_inspector();
            ImGui::EndChild();
        }
    }
    ImGui::End();
}

void statescript_window::display_instance(StatescriptInstance* ss, STUStatescriptBase* stu) {
    dealloc_old();
    _comments.clear();
    _zoom = 1.0f;
    _sort_pending = false;
    if (ss && ss->graph && is_graph_valid(ss)) {
        char buf[64];
        sprintf_s(buf, "./statescript/%p.json", ss->script_id);
        std::ifstream input(buf, std::ios::in);
        if (input.is_open()) {
            try {
                nlohmann::json json;
                json << input;
                std::vector<ImVec2> node_locations = json["locations"].get<std::vector<ImVec2>>();
                ImNodes::EditorContextSet(_nodes_context);
                for (int i = 0; i < node_locations.size(); i++) {
                    ImNodes::SetNodeScreenSpacePos(i, node_locations[i]);
                }
                // A layout saved from an unsorted graph has every node on one spot.
                bool piled = node_locations.size() > 1;
                for (auto& loc : node_locations) {
                    if (loc.x != node_locations[0].x || loc.y != node_locations[0].y) {
                        piled = false;
                        break;
                    }
                }
                _sort_pending = piled || node_locations.size() != (size_t)ss->graph->m_nodes.count();
                std::vector<GraphComment> comments = json["comments"].get<std::vector<GraphComment>>();
                for (int i = 0; i < comments.size(); i++) {
                    ImNodes::SetNodeScreenSpacePos(0xF000 | i, comments[i].location);

                    char* buf = new char[256];
                    strcpy_s(buf, 256, comments[i].comment.c_str());
                    _comments.push_back(buf);
                }
            }
            catch (nlohmann::json::exception& ex) {
                printf("Failed to load statescript graph locations! %p\n", ss->script_id);
            }
        }
        else {
            printf("No config file found for statescript script %p.\n", ss->script_id);
            ImNodes::EditorContextSet(_nodes_context);
            for (int i = 0; i < ss->graph->m_nodes.count(); i++) {
                ImNodes::SetNodeScreenSpacePos(i, ImVec2());
            }
            _sort_pending = true;
        }

        _curr_instance = ss;
        _instance_invalid = false;

        if (stu) {
            _nav_node = stu;
            nav_to_node();
        }

        statescript_logger::Subscribe(_curr_instance);
    }
}

void statescript_window::nav_to_node() {
    if (_nav_node && !_instance_invalid && _curr_instance->graph->m_nodes.contains(_nav_node)) {
        ImNodes::EditorContextSet(_nodes_context);
        ImVec2 target = ImNodes::GetNodeGridSpacePos(_nav_node->idx_in_nodes);
        ImVec2 screen = GImNodes->CanvasRectScreenSpace.GetSize() / 2;
        _nodes_context->Panning = -target + screen;
    }
}

// Zooms to `zoom` keeping the grid point under `anchor` (screen space) in place: every
// node origin (grid space) is scaled and the panning adjusted to match.
void statescript_window::set_zoom(float zoom, ImVec2 anchor) {
    zoom = std::clamp(zoom, 0.2f, 3.0f);
    const float factor = zoom / _zoom;
    if (factor == 1.0f)
        return;
    const ImVec2 anchor_grid = anchor - _canvas_origin - _nodes_context->Panning;
    auto& nodes = _nodes_context->Nodes;
    for (int i = 0; i < nodes.Pool.size(); i++) {
        if (nodes.InUse[i])
            nodes.Pool[i].Origin = nodes.Pool[i].Origin * factor;
    }
    _nodes_context->Panning = anchor - _canvas_origin - anchor_grid * factor;
    _zoom = zoom;
}

statescript_window::statescript_window() {
    _curr_instance = nullptr;
    _instance_invalid = true;
    _nodes_context = ImNodes::EditorContextCreate();
    _deletion_callback = std::make_shared<std::function<void(StatescriptInstance*)>>([&](StatescriptInstance* deleted_script) {
        if (_curr_instance == deleted_script) {
            _curr_instance = nullptr;
            _instance_invalid = true;
        }
    });
    statescript_logger::SubscribeDeallocation(_deletion_callback);
}

statescript_window::~statescript_window() {
    dealloc_old();
    ImNodes::EditorContextFree(_nodes_context);
}

// Layered (Sugiyama) layout, flowing left to right:
//  1. cycle removal: a DFS from the entries reverses edges back into the DFS stack
//  2. layering: longest path from the sources (every edge goes one column or more right)
//  3. dummy nodes split edges spanning several columns, so long links take part in
//     the crossing reduction
//  4. crossing reduction: barycenter sweeps, alternating left-to-right and right-to-left
//  5. placement: each node at the mean height of its predecessors, pushed down to keep
//     its column's order without overlap; columns as wide as their widest node
void statescript_window::sort_graph() {
    // Also reached from the menu bar, before render() sets the context.
    ImNodes::EditorContextSet(_nodes_context);
    auto graph = _curr_instance->graph;
    const int n = graph->m_nodes.count();
    if (n == 0)
        return;

    std::vector<std::vector<int>> out(n);
    for (auto node : graph->m_nodes) {
        int s = node->idx_in_nodes;
        for (auto& [plug_hash, paths] : node->get_output_plugs())
            for (auto path : paths)
                for (auto link : path->m_links) {
                    int t = link->m_inputPlug->plug_base.m_parentNode->idx_in_nodes;
                    if (s >= 0 && s < n && t >= 0 && t < n && t != s &&
                        std::find(out[s].begin(), out[s].end(), t) == out[s].end())
                        out[s].push_back(t);
                }
    }

    // 1. DAG edges; DFS order doubles as the initial vertical order.
    std::vector<std::pair<int, int>> edges;
    std::vector<int> mark(n, 0), dfs_order(n, 0);
    int visit = 0;
    std::function<void(int)> dfs = [&](int u) {
        mark[u] = 1;
        dfs_order[u] = visit++;
        for (int v : out[u]) {
            if (mark[v] == 1) { edges.push_back({ v, u }); continue; }
            edges.push_back({ u, v });
            if (mark[v] == 0) dfs(v);
        }
        mark[u] = 2;
    };
    for (auto entry : graph->m_entries)
        if (mark[entry->idx_in_nodes] == 0) dfs(entry->idx_in_nodes);
    for (int u = 0; u < n; u++)
        if (mark[u] == 0) dfs(u);

    // 2. longest-path layers (Kahn order over the DAG)
    std::vector<std::vector<int>> succ(n);
    std::vector<int> indeg(n, 0), layer(n, 0);
    for (auto [u, v] : edges) { succ[u].push_back(v); indeg[v]++; }
    std::vector<int> queue;
    for (int u = 0; u < n; u++) if (indeg[u] == 0) queue.push_back(u);
    for (size_t q = 0; q < queue.size(); q++) {
        int u = queue[q];
        for (int v : succ[u]) {
            layer[v] = std::max(layer[v], layer[u] + 1);
            if (--indeg[v] == 0) queue.push_back(v);
        }
    }

    // 3. dummies: vertex ids >= n
    std::vector<int> vlayer = layer;
    std::vector<std::vector<int>> vsucc(n), vpred(n);
    auto add_vertex = [&](int l) {
        vlayer.push_back(l); vsucc.emplace_back(); vpred.emplace_back();
        return (int)vlayer.size() - 1;
    };
    for (auto [u, v] : edges) {
        int prev = u;
        for (int l = layer[u] + 1; l < layer[v]; l++) {
            int d = add_vertex(l);
            vsucc[prev].push_back(d); vpred[d].push_back(prev);
            prev = d;
        }
        vsucc[prev].push_back(v); vpred[v].push_back(prev);
    }
    const int nv = (int)vlayer.size();
    int nlayers = 0;
    for (int l : vlayer) nlayers = std::max(nlayers, l + 1);
    std::vector<std::vector<int>> layers(nlayers);
    std::vector<float> key(nv, 0);
    for (int v = 0; v < nv; v++) {
        layers[vlayer[v]].push_back(v);
        // a dummy follows its predecessor (created earlier, so already keyed)
        key[v] = v < n ? (float)dfs_order[v] : key[vpred[v][0]];
    }
    std::vector<float> pos(nv, 0);
    auto renumber = [&](int l) {
        std::stable_sort(layers[l].begin(), layers[l].end(), [&](int a, int b) { return key[a] < key[b]; });
        for (size_t i = 0; i < layers[l].size(); i++) pos[layers[l][i]] = (float)i;
    };
    for (int l = 0; l < nlayers; l++) renumber(l);

    // 4. barycenter sweeps
    auto barycenter = [&](int v, const std::vector<std::vector<int>>& nb) {
        if (nb[v].empty()) return pos[v];
        float sum = 0;
        for (int u : nb[v]) sum += pos[u];
        return sum / nb[v].size();
    };
    for (int iter = 0; iter < 12; iter++) {
        if (iter % 2 == 0) {
            for (int l = 1; l < nlayers; l++) {
                for (int v : layers[l]) key[v] = barycenter(v, vpred);
                renumber(l);
            }
        } else {
            for (int l = nlayers - 2; l >= 0; l--) {
                for (int v : layers[l]) key[v] = barycenter(v, vsucc);
                renumber(l);
            }
        }
    }

    // 5. placement (grid space)
    const float gap_x = 80 * _zoom, gap_y = 30 * _zoom, dummy_h = 12 * _zoom;
    std::vector<ImVec2> dim(nv, ImVec2(0, dummy_h));
    for (int v = 0; v < n; v++) dim[v] = ImNodes::GetNodeDimensions(v);
    std::vector<float> y(nv, 0);
    for (int l = 0; l < nlayers; l++) {
        float bottom = -FLT_MAX;
        for (int v : layers[l]) {
            float want = bottom == -FLT_MAX ? 0.0f : bottom;
            if (!vpred[v].empty()) {
                float sum = 0;
                for (int u : vpred[v]) sum += y[u] + dim[u].y / 2;
                want = sum / vpred[v].size() - dim[v].y / 2;
            }
            if (bottom != -FLT_MAX)
                want = std::max(want, bottom);
            y[v] = want;
            bottom = want + dim[v].y + (v < n ? gap_y : gap_y / 3);
        }
    }
    float x = 0;
    for (int l = 0; l < nlayers; l++) {
        float width = 0;
        for (int v : layers[l]) {
            if (v < n) {
                ImNodes::SetNodeGridSpacePos(v, ImVec2(x, y[v]));
                width = std::max(width, dim[v].x);
            }
        }
        x += width + gap_x;
    }
    // Grid space from (0, 0): show that corner.
    _nodes_context->Panning = ImVec2(20.0f, 20.0f);
}

// The node's config var arguments with their live values (display_cv), plus the
// variable path of Dynamic ones (0x0D8... GUIDs, "0004/0152" = owner var 0152).
void statescript_window::render_node_args(STUStatescriptBase* node) {
    for (auto [info, arg] : node->graph_node.base.vfptr->GetSTUInfo()->RangeArgs()) {
        if (info->Hash == STU_NAME::STUStatescriptState ||
            info->Hash == STU_NAME::STUStatescriptAction)
            break;
        if (arg->Constraint->ToConstraintType() != STU_ConstraintType_Object)
            continue;
        auto arg_typ = STURegistry::Get()->GetSTUInfoByHash(arg->Constraint->GetSTUType());
        if (!arg_typ || !arg_typ->AssignableToHash(STU_NAME::STUConfigVar))
            continue;
        STUConfigVar* cv = *(STUConfigVar**)((__int64)node + arg->Offset);
        if (!cv)
            continue;
        ImGui::PushID(cv);
        imgui_helpers::display_cv(cv, _curr_instance, arg, false);
        if (cv->is_expression()) {
            ImGui::SameLine();
            if (ImGui::Button("Expression")) {
                cvexpression_viewer::get_latest_or_create(this)->display_expression((STUConfigVarExpression*)cv, _curr_instance);
            }
        }
        else if (cv->is_dynamic()) {
            // Game assets store the path list with flag 2 ({count, list}); see STUBullshitListFull.
            auto& path = ((STUConfigVarDynamic*)cv)->item_location;
            if (path.list_ptr) {
                int count = path.count();
                STUResourceReference* refs = path.list();
                std::string ids;
                for (int i = 0; refs && i < count && i < 16; i++)
                    ids += std::format("{}{:04x}", i ? "/" : "", (uint64_t)refs[i].resource_id & 0xffff);
                ImGui::SameLine();
                ImGui::TextDisabled("var %s", ids.c_str());
            }
        }
        ImGui::PopID();
    }
}

// The entity whose Statescript component runs this instance.
uint32_t statescript_window::owner_entity_id() {
    return _curr_instance->ss_inner->component_ref->base.entity_backref->entity_id;
}

// Details of the selected node: what it is, whether it runs, its arguments and links.
void statescript_window::render_inspector() {
    ImGui::Text("Instance %d  graph %llx", _curr_instance->instance_id, (unsigned long long)_curr_instance->script_id);
    if (_curr_instance->parent_instance_id)
        ImGui::Text("Child of instance %d", _curr_instance->parent_instance_id);
    ImGui::Text("CF %lld", (long long)_curr_instance->ss_inner->time_ms);
    ImGui::Separator();

    STUStatescriptBase* node = nullptr;
    for (auto n : _curr_instance->graph->m_nodes) {
        if (n->idx_in_nodes == _selected_node) {
            node = n;
            break;
        }
    }
    if (!node) {
        ImGui::TextWrapped("Select a node to see its state and values.");
        return;
    }
    auto stu = node->graph_node.base.vfptr->GetSTUInfo();
    ImGui::Text("#%d", node->idx_in_nodes);
    ImGui::SameLine();
    imgui_helpers::display_type(stu->Hash, false, true, true);
    std::string flags;
    if (node->m_isState) flags += "state ";
    if (node->m_clientOnly) flags += "client-only ";
    if (node->m_serverOnly) flags += "server-only ";
    if (!flags.empty())
        ImGui::TextDisabled("%s", flags.c_str());

    if (node->m_isState) {
        auto& states = statescript_logger::GetScriptState(_curr_instance);
        auto it = states.find(node->idx_in_nodes);
        bool active = it != states.end() && !it->second.is_finish_state;
        ImGui::TextColored(active ? ImVec4(0, 1, 0, 1) : ImVec4(0.6f, 0.6f, 0.6f, 1), active ? "ACTIVE" : "inactive");
        if (it != states.end()) {
            ImGui::SameLine();
            ImGui::TextDisabled("(changed at CF %lld)", (long long)it->second.change_timestamp);
        }
        if (ImGui::Button(active ? "Stop state" : "Start state"))
            _curr_instance->StartStopState(node->idx_in_nodes, !active);
        ImGui::SameLine();
        if (ImGui::Button(active ? "Stop + server" : "Start + server")) {
            _curr_instance->StartStopState(node->idx_in_nodes, !active);
            ServerBridge::queue_capture(owner_entity_id());
        }
    }
    else {
        if (ImGui::Button("Execute"))
            _curr_instance->ExecuteNode(node->idx_in_nodes);
        ImGui::SameLine();
        if (ImGui::Button("Execute + server")) {
            _curr_instance->ExecuteNode(node->idx_in_nodes);
            ServerBridge::queue_capture(owner_entity_id());
        }
    }
    if (ImGui::SmallButton("Send entity state to server"))
        ServerBridge::queue_capture(owner_entity_id(), 1);
    bool echo = ServerBridge::echo_enabled() && ServerBridge::echo_entity() == owner_entity_id();
    if (ImGui::Checkbox("Echo mode (server keeps confirming this entity)", &echo))
        ServerBridge::set_echo(owner_entity_id(), echo);
    ImGui::TextWrapped("Server: %s", ServerBridge::last_status().c_str());

    ImGui::SeparatorText("Arguments");
    render_node_args(node);

    ImGui::SeparatorText("Outputs");
    for (auto& [plug_hash, paths] : node->get_output_plugs()) {
        for (auto path : paths) {
            for (auto link : path->m_links) {
                auto target = link->m_inputPlug->plug_base.m_parentNode;
                auto name = stringhash_library::hashes.contains(plug_hash) ? stringhash_library::hashes[plug_hash] : std::format("{:x}", plug_hash);
                ImGui::PushID(link);
                if (ImGui::SmallButton(std::format("{} -> #{}", name, target->idx_in_nodes).c_str())) {
                    _selected_node = target->idx_in_nodes;
                    _nav_node = target;
                    // Move the editor's selection too, or the next frame selects the old node again.
                    ImNodes::EditorContextSet(_nodes_context);
                    ImNodes::ClearNodeSelection();
                    ImNodes::SelectNode(target->idx_in_nodes);
                    nav_to_node();
                }
                ImGui::PopID();
            }
        }
    }
}

void statescript_window::save_inst() {
    if (_curr_instance) {
        std::vector<ImVec2> node_locations{};
        std::vector<GraphComment> comments{};
        ImNodes::EditorContextSet(_nodes_context);
        // Save the layout as it would be at zoom 1, so it loads the same at any zoom.
        auto unzoomed = [&](int id) {
            ImVec2 grid = ImNodes::GetNodeGridSpacePos(id);
            return ImNodes::GetNodeScreenSpacePos(id) - grid + grid / _zoom;
        };
        for (int i = 0; i < _curr_instance->graph->m_nodes.count(); i++) {
            node_locations.push_back(unzoomed(i));
        }
        for (int i = 0; i < _comments.size(); i++) {
            comments.push_back(GraphComment{ std::string(_comments[i]), unzoomed(0xF000 | i) });
        }
        char buf[64];
        sprintf_s(buf, "statescript/%p.json", _curr_instance->script_id);
        std::ofstream output(buf, std::ios::out | std::ios::trunc);
        if (output.is_open()) {
            nlohmann::json json;
            json["locations"] = node_locations;
            json["comments"] = comments;
            output << json.dump();
        }
    }
}

void statescript_window::print_node_location(STUStatescriptBase* node) {
    if (node->m_isState) {
        int states_idx = _curr_instance->graph->m_states.indexof((STUStatescriptState*)node);
        ImGui::Text("<%d, %d>", node->idx_in_nodes, states_idx);
    }
    else {
        ImGui::Text("<%d>", node->idx_in_nodes);
    }
}

bool statescript_window::is_graph_valid(StatescriptInstance* ss) {
    __try {
        if (IsBadReadPtr(ss->graph, 8))
            return false;
        assert(ss->graph->m_nodes.count() < 0x1000);
        assert(ss->graph->m_states.count() < ss->graph->m_nodes.count());
        if (ss->graph->m_nodes.count() >= 0x1000)
            return false;
        if (ss->graph->m_states.count() > ss->graph->m_nodes.count())
            return false;
    }
    __except (EXCEPTION_EXECUTE_HANDLER) {
        return false;
    }
    return true;
}

void statescript_window::dealloc_old() {
    if (_curr_instance) {
        statescript_logger::Unsubscribe(_curr_instance);
        if (_curr_instance)
            save_inst();
        for (int i = 0; i < _comments.size(); i++) {
            delete[] _comments[i];
        }
        _curr_instance = nullptr;
    }
    _instance_invalid = true;
}
