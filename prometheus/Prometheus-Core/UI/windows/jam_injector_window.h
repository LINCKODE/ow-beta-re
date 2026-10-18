#pragma once
#include "../window_manager/window_manager.h"
#include "JAM.h"
#include <vector>
#include <string>

class jam_injector_window : public window {
public:
	WINDOW_DEFINE(jam_injector_window, "Jam", "JAM Message Injector", true);

private:
	// Pool-based dispatch function (RVA 0xF23680).
	typedef void(__fastcall* dispatchFn)(__int64 handler, JamMessage* msg);

	struct FieldEditor {
		std::string name;
		int type_id = 0;
		int offset = 0;
		int size = 0;
		JamMessageItemInfo* inner_data_ptr = nullptr;

		bool val_bool = false;
		int val_int = 0;
		float val_float = 0.0f;
		double val_double = 0.0;
		__int64 val_qword = 0;
		char val_string[1024] = {};
		unsigned char val_raw[256] = {};
		int val_raw_size = 0;

		std::vector<FieldEditor> nested;
	};

	struct CachedMessageEntry {
		short msg_id = 0;
		std::string display_name;
		JamMessageInfo* info = nullptr;
	};

	int _selected_pool_idx = -1;
	int _selected_msg_idx = -1;
	std::string _selected_pool_name;
	int _selected_pool_crc = 0;

	std::vector<std::string> _pool_names;
	std::vector<std::pair<__int64, __int64>> _pool_data;

	std::vector<CachedMessageEntry> _cached_messages;
	JamMessage* _template_msg = nullptr;

	std::vector<FieldEditor> _field_editors;

	std::string _status;
	ImVec4 _status_color = ImVec4(0.7f, 0.7f, 0.7f, 1.0f);

	static std::string get_type_name(int type) {
		switch (type) {
		case 0:  return "BOOL";
		case 1: case 2: case 3: return "BYTE";
		case 4: case 5: return "SHORT";
		case 6: case 7: return "INT";
		case 8: case 11: return "DOUBLE";
		case 9:  return "QWORD";
		case 10: return "FLOAT";
		case 12: return "STRING_ARR";
		case 13: return "STRING";
		case 14: return "STRUCT";
		case 15: return "object?";
		case 16: return "DYNAMIC";
		default: return "UNKNOWN";
		}
	}

	static ImVec4 get_type_color(int type) {
		switch (type) {
		case 0:  return ImVec4(0.4f, 0.8f, 0.4f, 1.0f);
		case 1: case 2: case 3: return ImVec4(0.6f, 0.6f, 0.9f, 1.0f);
		case 4: case 5: return ImVec4(0.5f, 0.7f, 1.0f, 1.0f);
		case 6: case 7: return ImVec4(0.3f, 0.6f, 1.0f, 1.0f);
		case 8: case 11: return ImVec4(1.0f, 0.7f, 0.3f, 1.0f);
		case 9:  return ImVec4(0.3f, 0.5f, 1.0f, 1.0f);
		case 10: return ImVec4(1.0f, 0.8f, 0.2f, 1.0f);
		case 12: return ImVec4(0.9f, 0.4f, 0.9f, 1.0f);
		case 13: return ImVec4(0.9f, 0.5f, 0.7f, 1.0f);
		case 14: return ImVec4(1.0f, 1.0f, 0.4f, 1.0f);
		case 16: return ImVec4(0.9f, 0.3f, 0.3f, 1.0f);
		default: return ImVec4(0.7f, 0.7f, 0.7f, 1.0f);
		}
	}

	bool pool_has_receivers(int idx) {
		if (idx < 0 || idx >= (int)_pool_data.size()) return false;
		__int64 instance = *(__int64*)_pool_data[idx].first;
		JamPoolVtable* vtable = *(JamPoolVtable**)(instance);
		int crc = vtable->pool_crc();
		auto it = poolReceivers.find(crc);
		return it != poolReceivers.end() && !it->second.empty();
	}

	void refresh_pool_list() {
		_pool_names.clear();
		_pool_data.clear();
		for (auto& [name, pair] : jamOrderedInstances) {
			_pool_names.push_back(name);
			_pool_data.push_back(pair);
		}
	}

	void load_messages_for_pool(int pool_idx) {
		_cached_messages.clear();
		free_template_msg();
		_field_editors.clear();
		_selected_msg_idx = -1;

		if (pool_idx < 0 || pool_idx >= (int)_pool_data.size()) return;

		__int64 instance = *(__int64*)_pool_data[pool_idx].first;
		JamPoolVtable* vtable = *(JamPoolVtable**)(instance);
		short min_id = vtable->min_msgId();
		short max_id = vtable->max_msgId();
		_selected_pool_crc = vtable->pool_crc();
		_selected_pool_name = _pool_names[pool_idx];

		for (short id = min_id; id < max_id; id++) {
			JamMessage* msg = vtable->allocate_pool(instance, id);
			if (!msg) continue;

			JamMessageInfo* info = msg->vfptr->get_message_info();
			if (!info) {
				vtable->deallocate_pool(instance, (__int64)msg);
				continue;
			}

			CachedMessageEntry entry;
			entry.msg_id = id;
			entry.info = info;

			char buf[256];
			if (info->message_name && info->message_name[0] != '\0') {
				sprintf_s(buf, "%d %s", (int)id, info->message_name);
			} else {
				sprintf_s(buf, "%d (unnamed)", (int)id);
			}
			entry.display_name = buf;

			_cached_messages.push_back(entry);
			vtable->deallocate_pool(instance, (__int64)msg);
		}
	}

	void select_message(int msg_idx) {
		free_template_msg();
		_field_editors.clear();

		if (msg_idx < 0 || msg_idx >= (int)_cached_messages.size()) return;
		if (_selected_pool_idx < 0 || _selected_pool_idx >= (int)_pool_data.size()) return;

		__int64 instance = *(__int64*)_pool_data[_selected_pool_idx].first;
		JamPoolVtable* vtable = *(JamPoolVtable**)(instance);
		_template_msg = vtable->allocate_pool(instance, _cached_messages[msg_idx].msg_id);

		if (!_template_msg) return;

		JamMessageInfo* info = _template_msg->vfptr->get_message_info();
		if (info && info->items) {
			_field_editors = build_field_editors(info->items);
		}
	}

	std::vector<FieldEditor> build_field_editors(JamMessageItemInfo* items) {
		std::vector<FieldEditor> result;
		JamMessageItemInfo* item = items;
		while (item->message_type != 0x11) {
			FieldEditor fe;
			fe.name = item->name_str ? item->name_str : "(unnamed)";
			fe.type_id = item->message_type;
			fe.offset = item->offset;
			fe.size = item->size;
			fe.inner_data_ptr = item->inner_data_ptr;

			if (fe.type_id == 15 || fe.type_id == 16) {
				fe.val_raw_size = (fe.size > 0 && fe.size <= (int)sizeof(fe.val_raw)) ? fe.size : (int)sizeof(fe.val_raw);
			}

			if (item->inner_data_ptr && fe.type_id == 14) {
				fe.nested = build_field_editors(item->inner_data_ptr);
			}

			result.push_back(std::move(fe));
			item++;
		}
		return result;
	}

	void free_template_msg() {
		if (_template_msg && _selected_pool_idx >= 0 && _selected_pool_idx < (int)_pool_data.size()) {
			__int64 instance = *(__int64*)_pool_data[_selected_pool_idx].first;
			JamPoolVtable* vtable = *(JamPoolVtable**)(instance);
			vtable->deallocate_pool(instance, (__int64)_template_msg);
			_template_msg = nullptr;
		}
	}

	void write_fields_to_message(JamMessage* msg, const std::vector<FieldEditor>& editors) {
		char* base = (char*)msg;
		for (const auto& fe : editors) {
			char* field_ptr = base + fe.offset;
			switch (fe.type_id) {
			case 0:
				*(bool*)field_ptr = fe.val_bool;
				break;
			case 1: case 2: case 3:
				*(char*)field_ptr = (char)(fe.val_int & 0xFF);
				break;
			case 4: case 5:
				*(short*)field_ptr = (short)(fe.val_int & 0xFFFF);
				break;
			case 6: case 7:
				*(int*)field_ptr = fe.val_int;
				break;
			case 8: case 11:
				*(double*)field_ptr = fe.val_double;
				break;
			case 9:
				*(__int64*)field_ptr = fe.val_qword;
				break;
			case 10:
				*(float*)field_ptr = fe.val_float;
				break;
			case 13: {
				JamType13String* jam_str = (JamType13String*)field_ptr;
				int len = (int)strlen(fe.val_string);
				if (len < 8) {
					jam_str->length = len;
					jam_str->is_ptr = len;
					memset(&jam_str->string_literal_or_ptr, 0, 8);
					memcpy(&jam_str->string_literal_or_ptr, fe.val_string, len);
				} else {
					__int64 str_mem = ow_memalloc(len + 1);
					if (str_mem) {
						memcpy((void*)str_mem, fe.val_string, len + 1);
						jam_str->length = len;
						jam_str->is_ptr = len | 0x80000000;
						jam_str->string_literal_or_ptr = str_mem;
					}
				}
				break;
			}
			case 12:
			case 15: case 16:
				if (fe.val_raw_size > 0) {
					int copy_size = fe.val_raw_size;
					if (copy_size > fe.size && fe.size > 0) copy_size = fe.size;
					memcpy(field_ptr, fe.val_raw, copy_size);
				}
				break;
			case 14:
				write_fields_to_message(msg, fe.nested);
				break;
			}
		}
	}

	void inject_message() {
		if (!_template_msg) {
			_status = "Error: No message selected";
			_status_color = ImVec4(1.0f, 0.3f, 0.3f, 1.0f);
			return;
		}

		write_fields_to_message(_template_msg, _field_editors);

		JamMessageInfo* info = _template_msg->vfptr->get_message_info();

		if (g_jamLink) {
			JamMessage* to_dispatch = _template_msg->vfptr->clone_instance();
			if (!to_dispatch) to_dispatch = _template_msg;

			auto dispatch = (dispatchFn)(globals::gameBase + 0xF23680);
			dispatch(g_jamLink, to_dispatch);

			char buf[256];
			sprintf_s(buf, "Dispatched via JamLink: %s (ID %d)",
				(info->message_name && info->message_name[0]) ? info->message_name : "(unnamed)",
				info->message_id);
			_status = buf;
			_status_color = ImVec4(0.4f, 0.9f, 0.4f, 1.0f);

			if (to_dispatch == _template_msg) {
				_template_msg = nullptr;
				if (_selected_msg_idx >= 0) select_message(_selected_msg_idx);
			}
		} else {
			int crc = info->crc;
			auto it = poolReceivers.find(crc);
			if (it == poolReceivers.end() || it->second.empty()) {
				char buf[256];
				sprintf_s(buf, "No receivers registered for CRC 0x%08x", crc);
				_status = buf;
				_status_color = ImVec4(1.0f, 0.3f, 0.3f, 1.0f);
				return;
			}

			initFakeJamHandler();

			JamMessage* to_dispatch = _template_msg->vfptr->clone_instance();
			if (!to_dispatch) to_dispatch = _template_msg;

			typedef void(__fastcall* receiverHandlerFn)(__int64, __int64, JamMessage*, int);
			int count = 0;
			for (__int64 receiver : it->second) {
				__int64 vtable = *(__int64*)receiver;
				auto fn = (receiverHandlerFn)(*(__int64*)(vtable + 0x10));
				fn(receiver, (__int64)g_fakeJamHandler, to_dispatch, 0);
				count++;
			}

			char buf[256];
			sprintf_s(buf, "Direct dispatch to %d receiver(s): %s (ID %d)",
				count,
				(info->message_name && info->message_name[0]) ? info->message_name : "(unnamed)",
				info->message_id);
			_status = buf;
			_status_color = ImVec4(0.4f, 0.9f, 0.4f, 1.0f);

			if (to_dispatch == _template_msg) {
				_template_msg = nullptr;
				if (_selected_msg_idx >= 0) select_message(_selected_msg_idx);
			}
		}
	}

	void render_field_editors(std::vector<FieldEditor>& editors, const char* id_prefix = "fields") {
		ImGui::PushID(id_prefix);
		for (int i = 0; i < (int)editors.size(); i++) {
			auto& fe = editors[i];
			ImGui::PushID(i);

			ImGui::TableNextRow();
			ImGui::TableNextColumn();

			if (fe.type_id == 14 && !fe.nested.empty()) {
				bool open = ImGui::TreeNodeEx(fe.name.c_str(), ImGuiTreeNodeFlags_SpanAllColumns);
				ImGui::TableNextColumn();
				ImGui::TextColored(get_type_color(fe.type_id), "%s", get_type_name(fe.type_id).c_str());
				ImGui::TableNextColumn();
				ImGui::Text("0x%02x", fe.offset);
				ImGui::TableNextColumn();

				if (open) {
					render_field_editors(fe.nested, "nested");
					ImGui::TreePop();
				}
			} else {
				ImGui::TreeNodeEx(fe.name.c_str(),
					ImGuiTreeNodeFlags_Leaf | ImGuiTreeNodeFlags_NoTreePushOnOpen |
					ImGuiTreeNodeFlags_SpanAllColumns);

				ImGui::TableNextColumn();
				ImGui::TextColored(get_type_color(fe.type_id), "%s", get_type_name(fe.type_id).c_str());

				ImGui::TableNextColumn();
				ImGui::Text("0x%02x", fe.offset);

				ImGui::TableNextColumn();
				render_field_input(fe);
			}

			ImGui::PopID();
		}
		ImGui::PopID();
	}

	void render_field_input(FieldEditor& fe) {
		ImGui::SetNextItemWidth(-1);
		switch (fe.type_id) {
		case 0:
			ImGui::Checkbox("##val", &fe.val_bool);
			break;
		case 1: case 2: case 3: {
			ImGui::SliderInt("##val", &fe.val_int, 0, 255);
			break;
		}
		case 4: case 5: {
			ImGui::InputInt("##val", &fe.val_int);
			if (fe.val_int > 32767) fe.val_int = 32767;
			if (fe.val_int < -32768) fe.val_int = -32768;
			break;
		}
		case 6: case 7:
			ImGui::InputInt("##val", &fe.val_int);
			break;
		case 8: case 11:
			ImGui::InputDouble("##val", &fe.val_double, 0.0, 0.0, "%.6f");
			break;
		case 9:
			ImGui::InputScalar("##val", ImGuiDataType_S64, &fe.val_qword);
			break;
		case 10:
			ImGui::InputFloat("##val", &fe.val_float, 0.0f, 0.0f, "%.6f");
			break;
		case 13:
			ImGui::InputText("##val", fe.val_string, sizeof(fe.val_string));
			break;
		case 12:
		case 15: case 16: {
			int display_bytes = fe.val_raw_size;
			if (display_bytes <= 0) display_bytes = (fe.size > 0 && fe.size <= (int)sizeof(fe.val_raw)) ? fe.size : 16;
			char hex_buf[768] = {};
			int pos = 0;
			for (int b = 0; b < display_bytes && pos < (int)sizeof(hex_buf) - 4; b++) {
				if (b > 0) hex_buf[pos++] = ' ';
				sprintf_s(hex_buf + pos, sizeof(hex_buf) - pos, "%02X", fe.val_raw[b]);
				pos += 2;
			}
			if (ImGui::InputText("##val", hex_buf, sizeof(hex_buf))) {
				int byte_idx = 0;
				char* tok = hex_buf;
				while (*tok && byte_idx < (int)sizeof(fe.val_raw)) {
					while (*tok == ' ') tok++;
					if (!*tok) break;
					unsigned int val = 0;
					if (sscanf_s(tok, "%02X", &val) == 1) {
						fe.val_raw[byte_idx++] = (unsigned char)val;
					}
					while (*tok && *tok != ' ') tok++;
				}
				fe.val_raw_size = byte_idx;
			}
			break;
		}
		default:
			ImGui::TextDisabled("(unsupported type %d)", fe.type_id);
			break;
		}
	}

public:
	inline void render() override {
		for (auto& item : jamPoolInstances) {
			__int64 instance = *(__int64*)item.first;
			JamPoolVtable* vtable = *(JamPoolVtable**)(instance);
			char* static_name = vtable->pool_name();
			jamOrderedInstances.emplace(std::string(static_name), std::pair<__int64, __int64>{ item.first, item.second });
		}
		jamPoolInstances.clear();

		if (open_window(nullptr, 0, ImVec2(650, 500))) {
			if (_pool_names.size() != jamOrderedInstances.size()) {
				int prev_pool = _selected_pool_idx;
				refresh_pool_list();
				if (prev_pool >= 0 && prev_pool < (int)_pool_names.size()) {
					_selected_pool_idx = prev_pool;
				} else {
					_selected_pool_idx = -1;
				}
			}

			if (g_jamLink) {
				ImGui::TextColored(ImVec4(0.4f, 0.9f, 0.4f, 1.0f), "Mode: JamLink dispatch (0x%p)", (void*)g_jamLink);
			} else if (!poolReceivers.empty()) {
				int total_receivers = 0;
				for (auto& [crc, vec] : poolReceivers) total_receivers += (int)vec.size();
				ImGui::TextColored(ImVec4(0.3f, 0.8f, 1.0f, 1.0f),
					"Mode: Direct receiver dispatch (%d receivers across %d pools)",
					total_receivers, (int)poolReceivers.size());
			} else {
				ImGui::TextColored(ImVec4(1.0f, 0.4f, 0.4f, 1.0f), "No JamLink or receivers available.");
			}

			ImGui::Separator();

			char pool_preview[256] = "Select pool...";
			if (_selected_pool_idx >= 0) {
				sprintf_s(pool_preview, "%s %s",
					pool_has_receivers(_selected_pool_idx) ? "*" : " ",
					_pool_names[_selected_pool_idx].c_str());
			}
			if (ImGui::BeginCombo("Pool", pool_preview)) {
				for (int i = 0; i < (int)_pool_names.size(); i++) {
					bool selected = (i == _selected_pool_idx);
					char pool_label[256];
					sprintf_s(pool_label, "%s %s",
						pool_has_receivers(i) ? "*" : " ",
						_pool_names[i].c_str());
					if (ImGui::Selectable(pool_label, selected)) {
						if (i != _selected_pool_idx) {
							_selected_pool_idx = i;
							load_messages_for_pool(i);
						}
					}
					if (selected) ImGui::SetItemDefaultFocus();
				}
				ImGui::EndCombo();
			}

			if (_selected_pool_idx >= 0) {
				bool pool_recv = pool_has_receivers(_selected_pool_idx);
				ImGui::SameLine();
				ImGui::SetNextItemWidth(280);
				char msg_preview[256] = "Select message...";
				if (_selected_msg_idx >= 0) {
					sprintf_s(msg_preview, "%s %s",
						pool_recv ? "*" : " ",
						_cached_messages[_selected_msg_idx].display_name.c_str());
				}
				if (ImGui::BeginCombo("Message", msg_preview)) {
					for (int i = 0; i < (int)_cached_messages.size(); i++) {
						bool selected = (i == _selected_msg_idx);
						char msg_label[256];
						sprintf_s(msg_label, "%s %s",
							pool_recv ? "*" : " ",
							_cached_messages[i].display_name.c_str());
						if (ImGui::Selectable(msg_label, selected)) {
							if (i != _selected_msg_idx) {
								_selected_msg_idx = i;
								select_message(i);
							}
						}
						if (selected) ImGui::SetItemDefaultFocus();
					}
					ImGui::EndCombo();
				}
			}

			if (_selected_msg_idx >= 0 && _selected_msg_idx < (int)_cached_messages.size()) {
				auto& entry = _cached_messages[_selected_msg_idx];
				ImGui::TextDisabled("Pool: %s | CRC: 0x%08x | ID: %d",
					_selected_pool_name.c_str(), _selected_pool_crc, (int)entry.msg_id);
			}

			ImGui::Separator();

			if (_template_msg && !_field_editors.empty()) {
				ImGui::TextUnformatted("Fields:");

				ImGuiTableFlags flags =
					ImGuiTableFlags_BordersV |
					ImGuiTableFlags_BordersOuterH |
					ImGuiTableFlags_RowBg |
					ImGuiTableFlags_Resizable |
					ImGuiTableFlags_NoBordersInBody;

				ImGui::BeginChild("field_scroll", ImVec2(0, -ImGui::GetFrameHeightWithSpacing() * 2 - 8), ImGuiChildFlags_None);
				if (ImGui::BeginTable("field_editors", 4, flags)) {
					ImGui::TableSetupColumn("Name", ImGuiTableColumnFlags_NoHide);
					ImGui::TableSetupColumn("Type", ImGuiTableColumnFlags_WidthFixed, 90.0f);
					ImGui::TableSetupColumn("Offset", ImGuiTableColumnFlags_WidthFixed, 55.0f);
					ImGui::TableSetupColumn("Value", ImGuiTableColumnFlags_WidthStretch);
					ImGui::TableHeadersRow();

					render_field_editors(_field_editors);

					ImGui::EndTable();
				}
				ImGui::EndChild();
			} else if (_selected_pool_idx >= 0 && _selected_msg_idx >= 0) {
				ImGui::TextDisabled("(no editable fields)");
			} else {
				ImGui::TextDisabled("Select a pool and message to begin.");
			}

			ImGui::Separator();
			bool has_receivers = false;
			if (_template_msg && _selected_msg_idx >= 0 && _selected_msg_idx < (int)_cached_messages.size()) {
				int crc = _cached_messages[_selected_msg_idx].info->crc;
				auto it = poolReceivers.find(crc);
				has_receivers = (it != poolReceivers.end() && !it->second.empty());
			}
			bool can_inject = _template_msg && (g_jamLink || has_receivers);
			if (!can_inject) ImGui::BeginDisabled();
			if (ImGui::Button("Inject Message")) {
				inject_message();
			}
			if (!can_inject) ImGui::EndDisabled();

			if (!_status.empty()) {
				ImGui::SameLine();
				ImGui::TextColored(_status_color, "%s", _status.c_str());
			}
		}
		ImGui::End();
	}
};

WINDOW_REGISTER(jam_injector_window);
