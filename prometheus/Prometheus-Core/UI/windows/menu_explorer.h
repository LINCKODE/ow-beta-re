#pragma once
#include "../window_manager/window_manager.h"
#include "../../Viewmodel.h"
#include "../../Statescript.h"
#include "../../entity_admin.h"
#include "../../game.h"
#include "../imgui_helpers.h"
#include "string_rep_edit.h"

class menu_explorer : public window {
	WINDOW_DEFINE(menu_explorer, "Tools", "Menu Explorer", true);

	struct KnownScript {
		__int64 resource_id;
		const char* name;
		const char* description;
	};

	// Known screen-controlling Statescript resource IDs discovered during RE.
	static constexpr KnownScript s_known_scripts[] = {
		{ 0x580000000000158, "Lobby Menu", "Main menu screen controller" },
		{ 0x580000000000138, "Social Menu", "Friends/social panel" },
		{ 0x5800000000002d8, "Chat", "Chat UI" },
		{ 0x58000000000041a, "Developer Mode", "State 9 enables dev features" },
		{ 0x5800000000004f6, "Welcome Message", "Node 1 plays welcome announcement" },
		{ 0x5800000000001e5, "Music Controller", "Node 5=match, 87=POTG music" },
		{ 0x580000000000b47, "Map Screen", "Lobby map background" },
	};

	static ImVec4 vm_color(const char* name) {
		if (strstr(name, "ScreenVM"))  return ImVec4(0.3f, 1.0f, 0.3f, 1.0f);
		if (strstr(name, "Lobby"))     return ImVec4(0.3f, 0.7f, 1.0f, 1.0f);
		if (strstr(name, "Hero") || strstr(name, "Scoreboard") || strstr(name, "KillCam") ||
			strstr(name, "InWorld") || strstr(name, "Radial"))
			return ImVec4(1.0f, 0.7f, 0.3f, 1.0f);
		if (strstr(name, "Settings") || strstr(name, "Gamma") || strstr(name, "Binding") ||
			strstr(name, "Resolution"))
			return ImVec4(0.7f, 0.7f, 1.0f, 1.0f);
		if (strstr(name, "Custom"))    return ImVec4(1.0f, 0.5f, 0.8f, 1.0f);
		if (strstr(name, "Progression") || strstr(name, "Loadout"))
			return ImVec4(1.0f, 1.0f, 0.3f, 1.0f);
		return ImVec4(0.8f, 0.8f, 0.8f, 1.0f);
	}

	ViewModel* _selected_vm = nullptr;
	char _vm_filter[128] = "";
	char _ss_filter[128] = "";
	int _execute_node_id = 0;
	int _state_id = 0;
	bool _show_children = true;

	static const char* safe_vm_name(ViewModel* vm) {
		if (!vm || IsBadReadPtr(vm, sizeof(ViewModel))) return nullptr;
		if (IsBadReadPtr(vm->vfptr, sizeof(ViewModel_vt))) return nullptr;
		const char* name = vm->vfptr->GetName();
		if (!name || IsBadReadPtr(name, 1)) return nullptr;
		return name;
	}

	void render_vm_list() {
		ImGui::InputText("Filter##vm", _vm_filter, sizeof(_vm_filter));

		ViewModelRoot* root = GetViewModelRoot();
		if (!root) {
			ImGui::TextColored(ImVec4(1, 0.3f, 0.3f, 1), "ViewModelRoot not available");
			return;
		}

		ImGui::Text("Active: %d", root->viewmodel_list.num);
		ImGui::SameLine();
		ImGui::Checkbox("Children", &_show_children);

		if (_selected_vm) {
			bool found = false;
			for (int i = 0; i < root->viewmodel_list.num; i++) {
				if (root->viewmodel_list.ptr[i] == _selected_vm) { found = true; break; }
			}
			if (!found) _selected_vm = nullptr;
		}

		if (ImGui::BeginListBox("##vmlist", ImVec2(-1, -1))) {
			for (int i = 0; i < root->viewmodel_list.num; i++) {
				ViewModel* vm = root->viewmodel_list.ptr[i];
				if (!_show_children && vm != vm->this_or_parent) continue;

				const char* name = safe_vm_name(vm);
				if (!name) continue;
				if (_vm_filter[0] != '\0' && !strstr(name, _vm_filter)) continue;

				ImGui::PushID(vm);

				bool is_child = (vm != vm->this_or_parent);
				if (is_child) ImGui::Indent(12.0f);

				char label[128];
				sprintf_s(label, "[%x] %s (%d props)", (int)vm->id, name, vm->properties.num);

				ImGui::PushStyleColor(ImGuiCol_Text, vm_color(name));
				if (ImGui::Selectable(label, _selected_vm == vm)) {
					_selected_vm = vm;
				}
				ImGui::PopStyleColor();

				if (is_child) ImGui::Unindent(12.0f);
				ImGui::PopID();
			}
			ImGui::EndListBox();
		}
	}

	void render_property_editor() {
		if (!_selected_vm) {
			ImGui::TextDisabled("Select a ViewModel from the list on the left");
			return;
		}

		if (IsBadReadPtr(_selected_vm, sizeof(ViewModel))) {
			ImGui::TextColored(ImVec4(1, 0.3f, 0.3f, 1), "Selected ViewModel became invalid");
			_selected_vm = nullptr;
			return;
		}

		const char* name = safe_vm_name(_selected_vm);
		if (!name) {
			ImGui::TextColored(ImVec4(1, 0.3f, 0.3f, 1), "ViewModel name unreadable");
			_selected_vm = nullptr;
			return;
		}

		ImGui::TextColored(vm_color(name), "%s", name);
		ImGui::SameLine();
		ImGui::TextDisabled("[id=%x]", (int)_selected_vm->id);
		display_addr((__int64)_selected_vm, "Addr");
		ImGui::SameLine();
		display_addr((__int64)_selected_vm->this_or_parent, "Parent");
		ImGui::Separator();

		if (imgui_helpers::beginTable("props", { "Key", "Type", "Has", "Value", "" })) {
			for (int j = 0; j < _selected_vm->properties.num; j++) {
				auto* prop = &_selected_vm->properties.ptr[j];
				ImGui::PushID(prop);
				ImGui::TableNextRow();

				ImGui::TableNextColumn();
				imgui_helpers::display_type(prop->key, true, true, false);

				ImGui::TableNextColumn();
				ImGui::TextUnformatted(prop->property.type_str().c_str());

				ImGui::TableNextColumn();
				if (prop->property.has_value) {
					if (ImGui::SmallButton(EMOJI_CHECK "##has")) {
						prop->property.has_value--;
						SendViewModelPropUpdate(_selected_vm, prop);
					}
				}
				else {
					if (ImGui::SmallButton(EMOJI_CROSS "##has")) {
						prop->property.has_value++;
						SendViewModelPropUpdate(_selected_vm, prop);
					}
				}

				ImGui::TableNextColumn();
				switch (prop->property.type) {
				case ViewModelPropertyType_CHAR: {
					bool val = prop->property.value != 0;
					if (ImGui::Checkbox("##val", &val)) {
						prop->property.value = val ? 1 : 0;
						SendViewModelPropUpdate(_selected_vm, prop);
					}
					break;
				}
				case ViewModelPropertyType_INT: {
					int val = (int)prop->property.value;
					ImGui::SetNextItemWidth(80);
					if (ImGui::InputInt("##val", &val, 0, 0)) {
						prop->property.value = val;
						SendViewModelPropUpdate(_selected_vm, prop);
					}
					break;
				}
				case ViewModelPropertyType_FLT: {
					float val;
					memcpy(&val, &prop->property.value, sizeof(float));
					ImGui::SetNextItemWidth(80);
					if (ImGui::InputFloat("##val", &val, 0, 0, "%.3f")) {
						memcpy(&prop->property.value, &val, sizeof(float));
						SendViewModelPropUpdate(_selected_vm, prop);
					}
					break;
				}
				case ViewModelPropertyType_LONG: {
					imgui_helpers::display_type(prop->property.value, true, true, false);
					break;
				}
				case ViewModelPropertyType_STRING_REP: {
					string_rep* rep = (string_rep*)prop->property.value;
					if (rep && !IsBadReadPtr(rep, sizeof(string_rep))) {
						ImGui::Text("%s", rep->get());
						ImGui::SameLine();
						if (imgui_helpers::TooltipButton(EMOJI_EDIT "##stredit", "Edit string")) {
							window_manager::add_window(new string_rep_edit(rep), this);
						}
					}
					else {
						ImGui::TextDisabled("(null)");
					}
					break;
				}
				case ViewModelPropertyType_VIEWMODEL: {
					ViewModel* child = (ViewModel*)prop->property.value;
					const char* child_name = safe_vm_name(child);
					if (child_name) {
						ImGui::TextColored(vm_color(child_name), "[%x] %s", (int)child->id, child_name);
						ImGui::SameLine();
						if (imgui_helpers::TooltipButton(EMOJI_FORWARD "##goto", "Select this VM")) {
							_selected_vm = child;
						}
					}
					else {
						display_addr(prop->property.value);
					}
					break;
				}
				case ViewModelPropertyType_ARRAY: {
					ViewModelArray* arr = (ViewModelArray*)prop->property.value;
					if (arr && !IsBadReadPtr(arr, sizeof(ViewModelArray))) {
						ImGui::Text("Array [%d items]", arr->viewmodel_list.num);
					}
					else {
						ImGui::TextDisabled("(null array)");
					}
					break;
				}
				default:
					display_addr(prop->property.value);
					break;
				}

				ImGui::TableNextColumn();
				if (imgui_helpers::TooltipButton(EMOJI_REFRESH "##send", "Send prop update")) {
					SendViewModelPropUpdate(_selected_vm, prop);
				}

				ImGui::PopID();
			}
			ImGui::EndTable();
		}
	}

	void render_screen_controls() {
		auto lobby_ea = LobbyEntityAdmin();
		if (!lobby_ea) {
			ImGui::TextColored(ImVec4(1, 0.3f, 0.3f, 1), "LobbyEntityAdmin not available");
			return;
		}

		auto ss_comp = lobby_ea->getSingletonComponent<Component_23_Statescript>(0x23);
		if (!ss_comp) {
			ImGui::TextColored(ImVec4(1, 0.3f, 0.3f, 1), "Statescript component (0x23) not found");
			return;
		}

		if (ImGui::BeginTabBar("ScreenCtrlTabs")) {
			if (ImGui::BeginTabItem("Quick Controls")) {
				ImGui::SetNextItemWidth(60);
				ImGui::InputInt("State##qc", &_state_id, 0, 0);
				ImGui::SameLine();
				ImGui::SetNextItemWidth(60);
				ImGui::InputInt("Node##qc", &_execute_node_id, 0, 0);
				ImGui::Separator();

				for (auto& known : s_known_scripts) {
					auto script = ss_comp->ss_inner.getByResourceId(known.resource_id);
					ImGui::PushID((int)known.resource_id);

					bool found = script != nullptr;
					ImGui::TextColored(found ? ImVec4(0.3f, 1.0f, 0.3f, 1.0f) : ImVec4(0.5f, 0.5f, 0.5f, 1.0f),
						found ? EMOJI_CHECK : EMOJI_CROSS);
					ImGui::SameLine();
					ImGui::Text("%-18s", known.name);
					ImGui::SameLine();
					ImGui::TextDisabled("[%llx]", known.resource_id);

					if (found) {
						ImGui::SameLine();
						if (imgui_helpers::TooltipButton(EMOJI_STOP, "Stop")) {
							script->Stop();
						}
						ImGui::SameLine();
						if (imgui_helpers::TooltipButton(EMOJI_PLAY, "Start state")) {
							script->StartStopState(_state_id, true);
						}
						ImGui::SameLine();
						if (imgui_helpers::TooltipButton(EMOJI_CROSS "##st", "Stop state")) {
							script->StartStopState(_state_id, false);
						}
						ImGui::SameLine();
						if (imgui_helpers::TooltipButton(EMOJI_REFRESH, "Execute node")) {
							script->ExecuteNode(_execute_node_id);
						}
					}

					ImGui::PopID();
				}
				ImGui::EndTabItem();
			}

			if (ImGui::BeginTabItem("All Scripts")) {
				ImGui::InputText("Filter##ss", _ss_filter, sizeof(_ss_filter));
				ImGui::SameLine();
				ImGui::SetNextItemWidth(60);
				ImGui::InputInt("State", &_state_id, 0, 0);
				ImGui::SameLine();
				ImGui::SetNextItemWidth(60);
				ImGui::InputInt("Node", &_execute_node_id, 0, 0);

				ImGui::Text("Instances: %d | Scope: %d", ss_comp->ss_inner.g1_instanceArr.num,
					ss_comp->ss_inner.entity_admin_scope);

				if (ImGui::BeginChild("script_list", ImVec2(0, 0), ImGuiChildFlags_None)) {
					if (imgui_helpers::beginTable("ss_all", { "ID", "Resource", "States", "Flags", "Actions" })) {
						for (int i = 0; i < ss_comp->ss_inner.g1_instanceArr.num; i++) {
							auto script = ss_comp->ss_inner.g1_instanceArr.ptr[i];
							if (!script) continue;

							char res_buf[32];
							sprintf_s(res_buf, "%llx", script->script_id);

							const char* known_name = nullptr;
							for (auto& known : s_known_scripts) {
								if (known.resource_id == script->script_id) {
									known_name = known.name;
									break;
								}
							}

							if (_ss_filter[0] != '\0') {
								bool match = strstr(res_buf, _ss_filter) != nullptr;
								if (known_name) match = match || strstr(known_name, _ss_filter) != nullptr;
								if (!match) continue;
							}

							ImGui::PushID(script);
							ImGui::TableNextRow();

							ImGui::TableNextColumn();
							ImGui::Text("%d", script->instance_id);

							ImGui::TableNextColumn();
							if (known_name) {
								ImGui::TextColored(ImVec4(0.3f, 1.0f, 0.3f, 1.0f), "%s", known_name);
								ImGui::SameLine();
								ImGui::TextDisabled("(%s)", res_buf);
							}
							else {
								ImGui::Text("%s", res_buf);
							}

							ImGui::TableNextColumn();
							int active = 0;
							for (int j = 0; j < script->state_impl_list.num; j++)
								if (script->state_impl_list.ptr[j]) active++;
							ImGui::Text("%d/%d", active, script->state_impl_list.num);

							ImGui::TableNextColumn();
							ImGui::Text("%x", script->flags);

							ImGui::TableNextColumn();
							if (imgui_helpers::TooltipButton(EMOJI_STOP, "Stop")) script->Stop();
							ImGui::SameLine();
							if (imgui_helpers::TooltipButton(EMOJI_PLAY, "Execute node")) script->ExecuteNode(_execute_node_id);
							ImGui::SameLine();
							if (imgui_helpers::TooltipButton(EMOJI_CHECK, "Start state")) script->StartStopState(_state_id, true);
							ImGui::SameLine();
							if (imgui_helpers::TooltipButton(EMOJI_CROSS "##s", "Stop state")) script->StartStopState(_state_id, false);
							ImGui::SameLine();
							if (imgui_helpers::TooltipButton(EMOJI_REFRESH, "Toggle state")) script->ToggleState(_state_id);

							ImGui::PopID();
						}
						ImGui::EndTable();
					}
				}
				ImGui::EndChild();
				ImGui::EndTabItem();
			}
			ImGui::EndTabBar();
		}
	}

	inline void render() override {
		if (open_window("Menu Explorer", 0, ImVec2(900, 600))) {
			if (ImGui::CollapsingHeader("Screen Controls (Statescript)", ImGuiTreeNodeFlags_DefaultOpen)) {
				render_screen_controls();
			}

			ImGui::Separator();

			float avail = ImGui::GetContentRegionAvail().y;
			if (avail < 100) avail = 300;

			if (ImGui::BeginChild("vm_browser", ImVec2(0, avail))) {
				float width = ImGui::GetContentRegionAvail().x;
				if (ImGui::BeginChild("vm_list_panel", ImVec2(width * 0.35f, 0), ImGuiChildFlags_Border | ImGuiChildFlags_ResizeX)) {
					render_vm_list();
				}
				ImGui::EndChild();

				ImGui::SameLine();

				if (ImGui::BeginChild("vm_props_panel", ImVec2(0, 0), ImGuiChildFlags_Border)) {
					render_property_editor();
				}
				ImGui::EndChild();
			}
			ImGui::EndChild();
		}
		ImGui::End();
	}
};

WINDOW_REGISTER(menu_explorer);
