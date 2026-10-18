#pragma once
#include "../window_manager/window_manager.h"
#include "JAM.h"
#include <nlohmann/json.hpp>
#include <fstream>
#include <chrono>
#include <ctime>
#include <shellapi.h>

class jam_schema_dumper : public window {
public:
	WINDOW_DEFINE(jam_schema_dumper, "Jam", "JAM Protocol Analyzer", true);

private:
	struct CachedField {
		std::string name;
		int type_id = 0;
		std::string type_name;
		int offset = 0;
		int size = 0;
		int extra_flag = 0;
		std::vector<CachedField> nested;
	};

	struct CachedMessage {
		int id = 0;
		std::string name;
		std::string protocol;
		int crc = 0;
		int field_count = 0;
		std::vector<CachedField> fields;
	};

	struct CachedPool {
		std::string name;
		int crc = 0;
		short min_id = 0;
		short max_id = 0;
		__int64 instance_ptr = 0;
		bool loaded = false;
		std::vector<CachedMessage> messages;
	};

	std::vector<CachedPool> _pools;
	bool _pools_indexed = false;
	ImGuiTextFilter _filter;

	bool _dump_complete = false;
	std::string _dump_status;

	int _stat_total_messages = 0;
	int _stat_total_fields = 0;

	static std::string get_type_name(int type) {
		switch (type) {
		case 0:  return "BOOL";
		case 1:
		case 2:
		case 3:  return "BYTE";
		case 4:
		case 5:  return "SHORT";
		case 6:
		case 7:  return "INT";
		case 8:
		case 11: return "DOUBLE";
		case 9:  return "QWORD";
		case 10: return "FLOAT";
		case 12: return "STRING_ARR";
		case 13: return "STRING";
		case 14: return "STRUCT";
		case 15: return "object?";
		case 16: return "DYNAMIC";
		case 17: return "END";
		default: return "UNKNOWN";
		}
	}

	static ImVec4 get_type_color(int type) {
		switch (type) {
		case 0:  return ImVec4(0.4f, 0.8f, 0.4f, 1.0f);
		case 1: case 2: case 3:
			return ImVec4(0.6f, 0.6f, 0.9f, 1.0f);
		case 4: case 5:
			return ImVec4(0.5f, 0.7f, 1.0f, 1.0f);
		case 6: case 7:
			return ImVec4(0.3f, 0.6f, 1.0f, 1.0f);
		case 8: case 11:
			return ImVec4(1.0f, 0.7f, 0.3f, 1.0f);
		case 9:  return ImVec4(0.3f, 0.5f, 1.0f, 1.0f);
		case 10: return ImVec4(1.0f, 0.8f, 0.2f, 1.0f);
		case 12: return ImVec4(0.9f, 0.4f, 0.9f, 1.0f);
		case 13: return ImVec4(0.9f, 0.5f, 0.7f, 1.0f);
		case 14: return ImVec4(1.0f, 1.0f, 0.4f, 1.0f);
		case 16: return ImVec4(0.9f, 0.3f, 0.3f, 1.0f);
		default: return ImVec4(0.7f, 0.7f, 0.7f, 1.0f);
		}
	}

	static std::vector<CachedField> cache_fields(JamMessageItemInfo* items, int& count) {
		std::vector<CachedField> result;
		JamMessageItemInfo* item = items;
		while (item->message_type != 0x11) {
			CachedField f;
			f.name = item->name_str ? item->name_str : "";
			f.type_id = item->message_type;
			f.type_name = get_type_name(item->message_type);
			f.offset = item->offset;
			f.size = item->size;
			f.extra_flag = item->has_extra_flag;
			count++;

			if (item->inner_data_ptr) {
				f.nested = cache_fields(item->inner_data_ptr, count);
			}

			result.push_back(std::move(f));
			item++;
		}
		return result;
	}

	void load_pool(CachedPool& pool) {
		if (pool.loaded) return;

		__int64 instance = *(__int64*)pool.instance_ptr;
		JamPoolVtable* vtable = *(JamPoolVtable**)(instance);

		for (short msgId = pool.min_id; msgId < pool.max_id; msgId++) {
			JamMessage* msg = vtable->allocate_pool(instance, msgId);
			if (!msg) continue;

			JamMessageInfo* info = msg->vfptr->get_message_info();
			if (!info) {
				vtable->deallocate_pool(instance, (__int64)msg);
				continue;
			}

			CachedMessage cm;
			cm.id = info->message_id;
			cm.name = info->message_name ? info->message_name : "";
			cm.crc = info->crc;

			char* proto = info->message_protocol;
			if (proto && !IsBadReadPtr(proto, 1) && *proto >= 0x20 && *proto <= 0x7e) {
				cm.protocol = proto;
			}
			else {
				char buf[20];
				sprintf_s(buf, "0x%p", proto);
				cm.protocol = buf;
			}

			cm.field_count = 0;
			if (info->items) {
				cm.fields = cache_fields(info->items, cm.field_count);
			}

			_stat_total_fields += cm.field_count;
			_stat_total_messages++;
			pool.messages.push_back(std::move(cm));

			vtable->deallocate_pool(instance, (__int64)msg);
		}

		pool.loaded = true;
	}

	void index_pools() {
		_pools.clear();
		_stat_total_messages = 0;
		_stat_total_fields = 0;

		for (auto& [pool_key, pool_pair] : jamOrderedInstances) {
			__int64 instance = *(__int64*)pool_pair.first;
			JamPoolVtable* vtable = *(JamPoolVtable**)(instance);

			CachedPool cp;
			cp.name = vtable->pool_name() ? vtable->pool_name() : "(null)";
			cp.crc = vtable->pool_crc();
			cp.min_id = vtable->min_msgId();
			cp.max_id = vtable->max_msgId();
			cp.instance_ptr = pool_pair.first;

			_pools.push_back(std::move(cp));
		}
		_pools_indexed = true;
	}

	void render_fields(const std::vector<CachedField>& fields) {
		for (int i = 0; i < (int)fields.size(); i++) {
			const auto& f = fields[i];
			ImGui::PushID(i);

			if (f.type_id == 14 && !f.nested.empty()) {
				ImGui::TableNextRow();
				ImGui::TableNextColumn();

				bool open = ImGui::TreeNodeEx(
					f.name.empty() ? "(struct)" : f.name.c_str(),
					ImGuiTreeNodeFlags_SpanAllColumns);

				ImGui::TableNextColumn();
				ImGui::TextColored(get_type_color(f.type_id), "%s", f.type_name.c_str());

				ImGui::TableNextColumn();
				ImGui::Text("0x%02x", f.offset);

				ImGui::TableNextColumn();
				ImGui::Text("%d", f.size);

				ImGui::TableNextColumn();
				if (f.extra_flag) ImGui::Text("%d", f.extra_flag);

				if (open) {
					render_fields(f.nested);
					ImGui::TreePop();
				}
			}
			else {
				ImGui::TableNextRow();
				ImGui::TableNextColumn();
				ImGui::TreeNodeEx(f.name.empty() ? "(unnamed)" : f.name.c_str(),
					ImGuiTreeNodeFlags_Leaf | ImGuiTreeNodeFlags_NoTreePushOnOpen |
					ImGuiTreeNodeFlags_SpanAllColumns);

				ImGui::TableNextColumn();
				ImGui::TextColored(get_type_color(f.type_id), "%s", f.type_name.c_str());

				ImGui::TableNextColumn();
				ImGui::Text("0x%02x", f.offset);

				ImGui::TableNextColumn();
				ImGui::Text("%d", f.size);

				ImGui::TableNextColumn();
				if (f.extra_flag) ImGui::Text("%d", f.extra_flag);
			}

			ImGui::PopID();
		}
	}

	static nlohmann::json fields_to_json(const std::vector<CachedField>& fields) {
		nlohmann::json arr = nlohmann::json::array();
		for (const auto& f : fields) {
			nlohmann::json fj;
			fj["name"] = f.name.empty() ? "(unnamed)" : f.name;
			fj["type"] = f.type_name;
			fj["type_id"] = f.type_id;
			fj["offset"] = f.offset;
			fj["size"] = f.size;
			fj["extra_flag"] = f.extra_flag;
			if (!f.nested.empty()) {
				fj["nested_fields"] = fields_to_json(f.nested);
			}
			arr.push_back(fj);
		}
		return arr;
	}

	static void write_fields_txt(FILE* file, const std::vector<CachedField>& fields, int indent) {
		for (const auto& f : fields) {
			for (int i = 0; i < indent; i++) fprintf(file, "  ");
			fprintf(file, "%-28s %-12s offset=0x%02x  size=%d",
				f.name.empty() ? "(unnamed)" : f.name.c_str(),
				f.type_name.c_str(), f.offset, f.size);
			if (f.extra_flag) fprintf(file, "  extra=%d", f.extra_flag);
			fprintf(file, "\n");

			if (!f.nested.empty()) {
				for (int i = 0; i < indent; i++) fprintf(file, "  ");
				fprintf(file, "  => Nested struct:\n");
				write_fields_txt(file, f.nested, indent + 2);
			}
		}
	}

	void do_dump() {
		for (auto& pool : _pools) load_pool(pool);

		auto now = std::chrono::system_clock::now();
		auto time_t_now = std::chrono::system_clock::to_time_t(now);
		struct tm tm_buf;
		localtime_s(&tm_buf, &time_t_now);
		char time_str[64];
		strftime(time_str, sizeof(time_str), "%Y-%m-%dT%H:%M:%S", &tm_buf);

		nlohmann::json root;
		root["dump_info"]["tool"] = "Prometheus JAM Protocol Analyzer";
		root["dump_info"]["build"] = 24919;
		root["dump_info"]["timestamp"] = time_str;

		nlohmann::json pools_json = nlohmann::json::array();

		FILE* txt = fopen("jam_protocol_schema.txt", "w");
		if (txt) {
			fprintf(txt, "=== JAM Protocol Schema Dump ===\n");
			fprintf(txt, "Build: 24919\nTimestamp: %s\nPools: %d\n\n", time_str, (int)_pools.size());
		}

		for (const auto& pool : _pools) {
			nlohmann::json pj;
			pj["name"] = pool.name;
			char crc_str[16]; sprintf_s(crc_str, "0x%08x", pool.crc);
			pj["crc"] = crc_str;
			pj["min_id"] = pool.min_id;
			pj["max_id"] = pool.max_id;
			pj["message_count"] = (int)pool.messages.size();

			if (txt) {
				fprintf(txt, "================================================================================\n");
				fprintf(txt, "Pool: %s\n  CRC: 0x%08x\n  ID Range: %d - %d\n",
					pool.name.c_str(), pool.crc, pool.min_id, pool.max_id);
			}

			nlohmann::json msgs = nlohmann::json::array();
			for (const auto& msg : pool.messages) {
				nlohmann::json mj;
				mj["id"] = msg.id;
				mj["name"] = msg.name.empty() ? "(unnamed)" : msg.name;
				mj["protocol"] = msg.protocol;
				char mc[16]; sprintf_s(mc, "0x%08x", msg.crc);
				mj["crc"] = mc;
				mj["fields"] = fields_to_json(msg.fields);
				msgs.push_back(mj);

				if (txt) {
					fprintf(txt, "--------------------------------------------------------------------------------\n");
					fprintf(txt, "  [%d] %s (protocol: %s, crc: 0x%08x)\n",
						msg.id, msg.name.empty() ? "(unnamed)" : msg.name.c_str(),
						msg.protocol.c_str(), msg.crc);
					write_fields_txt(txt, msg.fields, 2);
				}
			}
			pj["messages"] = msgs;
			pools_json.push_back(pj);

			if (txt) fprintf(txt, "  Messages dumped: %d\n\n", (int)pool.messages.size());
		}

		root["pools"] = pools_json;
		root["summary"]["total_pools"] = (int)_pools.size();
		root["summary"]["total_messages"] = _stat_total_messages;
		root["summary"]["total_fields"] = _stat_total_fields;

		std::ofstream jf("jam_protocol_schema.json", std::ios::trunc);
		if (jf.is_open()) { jf << root.dump(2); jf.close(); }

		if (txt) {
			fprintf(txt, "================================================================================\n");
			fprintf(txt, "Summary: %d pools, %d messages, %d fields\n",
				(int)_pools.size(), _stat_total_messages, _stat_total_fields);
			fclose(txt);
		}

		_dump_status = "Exported: " + std::to_string(_pools.size()) + " pools, "
			+ std::to_string(_stat_total_messages) + " messages, "
			+ std::to_string(_stat_total_fields) + " fields";
		_dump_complete = true;
		LOG_CORE(Info, "[JAM Analyzer] {}", _dump_status);
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

		if (!_pools_indexed || _pools.size() != jamOrderedInstances.size()) {
			if (!jamOrderedInstances.empty()) {
				index_pools();
			}
		}

		if (open_window(nullptr, 0, ImVec2(750, 600))) {
			ImGui::Text("Pools: %d", (int)_pools.size());
			int loaded_count = 0;
			for (const auto& p : _pools) if (p.loaded) loaded_count++;
			if (loaded_count > 0) {
				ImGui::SameLine();
				ImGui::Text("| Loaded: %d | Messages: %d | Fields: %d",
					loaded_count, _stat_total_messages, _stat_total_fields);
			}

			if (_pools.empty()) {
				ImGui::BeginDisabled();
				ImGui::Button("Load All");
				ImGui::EndDisabled();
			}
			else {
				if (ImGui::Button("Load All")) {
					for (auto& pool : _pools) load_pool(pool);
				}
			}
			ImGui::SameLine();
			if (loaded_count == 0) {
				ImGui::BeginDisabled();
				ImGui::Button("Export to Files");
				ImGui::EndDisabled();
			}
			else {
				if (ImGui::Button("Export to Files")) {
					do_dump();
				}
			}
			if (_dump_complete) {
				ImGui::SameLine();
				if (ImGui::Button("Open in Explorer")) {
					char cwd[MAX_PATH];
					GetCurrentDirectoryA(MAX_PATH, cwd);
					ShellExecuteA(NULL, "explore", cwd, NULL, NULL, SW_SHOWNORMAL);
				}
				ImGui::SameLine();
				ImGui::TextColored(ImVec4(0.4f, 0.9f, 0.4f, 1.0f), "%s", _dump_status.c_str());
			}

			ImGui::Separator();
			_filter.Draw("Filter (pool/message name)##jam_filter", -1.0f);
			ImGui::Separator();

			ImGui::BeginChild("pool_tree", ImVec2(0, 0), ImGuiChildFlags_None);

			for (int pi = 0; pi < (int)_pools.size(); pi++) {
				auto& pool = _pools[pi];

				if (!_filter.PassFilter(pool.name.c_str())) {
					if (pool.loaded) {
						bool any_msg_match = false;
						for (const auto& msg : pool.messages) {
							if (_filter.PassFilter(msg.name.c_str())) {
								any_msg_match = true;
								break;
							}
						}
						if (!any_msg_match) continue;
					}
					else {
						continue;
					}
				}

				ImGui::PushID(pi);

				char pool_label[256];
				sprintf_s(pool_label, "%s  [0x%08x]  (%d msgs)###pool_%d",
					pool.name.c_str(), pool.crc, pool.max_id - pool.min_id, pi);

				bool pool_open = ImGui::TreeNodeEx(pool_label, ImGuiTreeNodeFlags_Framed);

				if (pool_open) {
					if (!pool.loaded) {
						load_pool(pool);
					}

					ImGui::TextDisabled("CRC: 0x%08x | IDs: %d - %d | Messages: %d",
						pool.crc, pool.min_id, pool.max_id, (int)pool.messages.size());

					for (int mi = 0; mi < (int)pool.messages.size(); mi++) {
						const auto& msg = pool.messages[mi];

						if (_filter.IsActive() && !_filter.PassFilter(pool.name.c_str()) &&
							!_filter.PassFilter(msg.name.c_str())) {
							continue;
						}

						ImGui::PushID(mi);

						char msg_label[256];
						if (!msg.name.empty()) {
							sprintf_s(msg_label, "[%d] %s  (%d fields)###msg_%d",
								msg.id, msg.name.c_str(), msg.field_count, mi);
						}
						else {
							sprintf_s(msg_label, "[%d] (unnamed)  (%d fields)###msg_%d",
								msg.id, msg.field_count, mi);
						}

						bool msg_open = ImGui::TreeNodeEx(msg_label, ImGuiTreeNodeFlags_None);

						if (msg_open) {
							ImGui::TextDisabled("Protocol: %s | CRC: 0x%08x | ID: %d",
								msg.protocol.c_str(), msg.crc, msg.id);

							if (!msg.fields.empty()) {
								ImGuiTableFlags table_flags =
									ImGuiTableFlags_BordersV |
									ImGuiTableFlags_BordersOuterH |
									ImGuiTableFlags_RowBg |
									ImGuiTableFlags_Resizable |
									ImGuiTableFlags_NoBordersInBody;

								if (ImGui::BeginTable("fields", 5, table_flags)) {
									ImGui::TableSetupColumn("Name", ImGuiTableColumnFlags_NoHide);
									ImGui::TableSetupColumn("Type", ImGuiTableColumnFlags_WidthFixed, 80.0f);
									ImGui::TableSetupColumn("Offset", ImGuiTableColumnFlags_WidthFixed, 55.0f);
									ImGui::TableSetupColumn("Size", ImGuiTableColumnFlags_WidthFixed, 40.0f);
									ImGui::TableSetupColumn("Extra", ImGuiTableColumnFlags_WidthFixed, 40.0f);
									ImGui::TableHeadersRow();

									render_fields(msg.fields);

									ImGui::EndTable();
								}
							}
							else {
								ImGui::TextDisabled("(no fields)");
							}

							ImGui::TreePop();
						}

						ImGui::PopID();
					}

					ImGui::TreePop();
				}

				ImGui::PopID();
			}

			ImGui::EndChild();
		}
		ImGui::End();
	}
};

WINDOW_REGISTER(jam_schema_dumper);
