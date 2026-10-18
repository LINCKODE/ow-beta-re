#pragma once
#include "../window_manager/window_manager.h"
#include "../../proto_capture.h"
#include "../../proto_decoder.h"
#include <nlohmann/json.hpp>
#include <fstream>
#include <shellapi.h>
#include <filesystem>

class proto_viewer_window : public window {
public:
	WINDOW_DEFINE(proto_viewer_window, "Networking", "Proto Viewer", true);

private:
	struct DecodedFile {
		proto_decoder::ProtoFile file;
		size_t blob_size = 0;
		bool valid = false;
	};

	std::vector<DecodedFile> _decoded;
	int _selected = -1;
	bool _decoded_once = false;
	std::string _status;
	ImGuiTextFilter _filter;

	void decode_all() {
		std::lock_guard<std::mutex> lock(proto_capture::g_protoMutex);
		_decoded.clear();
		for (const auto& blob : proto_capture::g_capturedProtos) {
			DecodedFile df;
			df.blob_size = blob.data.size();
			df.file = proto_decoder::decode_file_descriptor(blob.data.data(), blob.data.size());
			df.valid = !df.file.name.empty();
			_decoded.push_back(std::move(df));
		}
		_decoded_once = true;
		_status = "Decoded " + std::to_string(_decoded.size()) + " proto blobs";
	}

	static ImVec4 get_type_color(int type) {
		switch (type) {
		case 1: case 2: case 10:             // double, float
			return ImVec4(1.0f, 0.8f, 0.2f, 1.0f);
		case 3: case 4: case 5: case 13:     // int types
		case 6: case 7: case 15: case 16:
		case 17: case 18:
			return ImVec4(0.4f, 0.7f, 1.0f, 1.0f);
		case 8:                               // bool
			return ImVec4(0.4f, 0.9f, 0.4f, 1.0f);
		case 9: case 12:                      // string, bytes
			return ImVec4(0.9f, 0.5f, 0.7f, 1.0f);
		case 11:                              // message
			return ImVec4(1.0f, 1.0f, 0.4f, 1.0f);
		case 14:                              // enum
			return ImVec4(0.9f, 0.6f, 0.3f, 1.0f);
		default:
			return ImVec4(0.7f, 0.7f, 0.7f, 1.0f);
		}
	}

	void render_fields(const std::vector<proto_decoder::ProtoField>& fields) {
		for (int i = 0; i < (int)fields.size(); i++) {
			const auto& f = fields[i];
			ImGui::TableNextRow();
			ImGui::TableNextColumn();

			std::string display_type;
			if (f.type == 11 || f.type == 14) {
				display_type = f.type_name;
			}
			else {
				display_type = proto_decoder::field_type_name(f.type);
			}

			ImGui::TreeNodeEx(f.name.c_str(),
				ImGuiTreeNodeFlags_Leaf | ImGuiTreeNodeFlags_NoTreePushOnOpen |
				ImGuiTreeNodeFlags_SpanAllColumns);

			ImGui::TableNextColumn();
			ImGui::Text("%d", f.number);

			ImGui::TableNextColumn();
			ImGui::TextColored(get_type_color(f.type), "%s", display_type.c_str());

			ImGui::TableNextColumn();
			const char* label = proto_decoder::field_label_name(f.label);
			if (label[0]) ImGui::TextUnformatted(label);

			ImGui::TableNextColumn();
			if (!f.default_value.empty()) {
				ImGui::TextUnformatted(f.default_value.c_str());
			}
		}
	}

	void render_enum(const proto_decoder::ProtoEnum& e) {
		if (ImGui::TreeNodeEx(e.name.c_str(), ImGuiTreeNodeFlags_None)) {
			for (const auto& v : e.values) {
				ImGui::BulletText("%s = %d", v.first.c_str(), v.second);
			}
			ImGui::TreePop();
		}
	}

	void render_message(const proto_decoder::ProtoMessage& msg) {
		if (ImGui::TreeNodeEx(msg.name.c_str(), ImGuiTreeNodeFlags_None)) {
			for (const auto& e : msg.enums) {
				ImGui::PushStyleColor(ImGuiCol_Text, ImVec4(0.9f, 0.6f, 0.3f, 1.0f));
				ImGui::TextUnformatted("enum");
				ImGui::PopStyleColor();
				ImGui::SameLine();
				render_enum(e);
			}

			for (const auto& nested : msg.nested) {
				ImGui::PushStyleColor(ImGuiCol_Text, ImVec4(1.0f, 1.0f, 0.4f, 1.0f));
				ImGui::TextUnformatted("message");
				ImGui::PopStyleColor();
				ImGui::SameLine();
				render_message(nested);
			}

			if (!msg.fields.empty()) {
				ImGuiTableFlags table_flags =
					ImGuiTableFlags_BordersV |
					ImGuiTableFlags_BordersOuterH |
					ImGuiTableFlags_RowBg |
					ImGuiTableFlags_Resizable |
					ImGuiTableFlags_NoBordersInBody;

				ImGui::PushID(msg.name.c_str());
				if (ImGui::BeginTable("fields", 5, table_flags)) {
					ImGui::TableSetupColumn("Name", ImGuiTableColumnFlags_NoHide);
					ImGui::TableSetupColumn("#", ImGuiTableColumnFlags_WidthFixed, 35.0f);
					ImGui::TableSetupColumn("Type", ImGuiTableColumnFlags_WidthFixed, 180.0f);
					ImGui::TableSetupColumn("Label", ImGuiTableColumnFlags_WidthFixed, 65.0f);
					ImGui::TableSetupColumn("Default", ImGuiTableColumnFlags_WidthFixed, 80.0f);
					ImGui::TableHeadersRow();
					render_fields(msg.fields);
					ImGui::EndTable();
				}
				ImGui::PopID();
			}

			ImGui::TreePop();
		}
	}

	void render_service(const proto_decoder::ProtoService& svc) {
		if (ImGui::TreeNodeEx(svc.name.c_str(), ImGuiTreeNodeFlags_None)) {
			for (const auto& m : svc.methods) {
				ImGui::BulletText("rpc %s(%s) -> %s",
					m.name.c_str(), m.input_type.c_str(), m.output_type.c_str());
			}
			ImGui::TreePop();
		}
	}

	void dump_raw_blobs() {
		std::lock_guard<std::mutex> lock(proto_capture::g_protoMutex);
		std::filesystem::create_directories("proto_dump");
		int count = 0;
		for (size_t i = 0; i < proto_capture::g_capturedProtos.size(); i++) {
			const auto& blob = proto_capture::g_capturedProtos[i];
			std::string filename = "proto_dump/blob_" + std::to_string(i) + ".bin";
			std::ofstream f(filename, std::ios::binary);
			if (f.is_open()) {
				f.write((const char*)blob.data.data(), blob.data.size());
				count++;
			}
		}
		_status = "Dumped " + std::to_string(count) + " raw blobs to proto_dump/";
	}

	void dump_as_proto() {
		if (!_decoded_once) decode_all();
		std::filesystem::create_directories("proto_dump");
		int count = 0;
		for (const auto& df : _decoded) {
			if (!df.valid) continue;
			std::filesystem::path p("proto_dump/" + df.file.name);
			std::filesystem::create_directories(p.parent_path());

			std::string text = proto_decoder::reconstruct_proto(df.file);
			std::ofstream f(p.string());
			if (f.is_open()) {
				f << text;
				count++;
			}
		}
		_status = "Dumped " + std::to_string(count) + " .proto files to proto_dump/";
	}

	static nlohmann::json enums_to_json(const std::vector<proto_decoder::ProtoEnum>& enums) {
		nlohmann::json arr = nlohmann::json::array();
		for (const auto& e : enums) {
			nlohmann::json ej;
			ej["name"] = e.name;
			nlohmann::json vals = nlohmann::json::array();
			for (const auto& v : e.values) {
				nlohmann::json vj;
				vj["name"] = v.first;
				vj["number"] = v.second;
				vals.push_back(vj);
			}
			ej["values"] = vals;
			arr.push_back(ej);
		}
		return arr;
	}

	static nlohmann::json messages_to_json(const std::vector<proto_decoder::ProtoMessage>& messages) {
		nlohmann::json arr = nlohmann::json::array();
		for (const auto& msg : messages) {
			nlohmann::json mj;
			mj["name"] = msg.name;

			nlohmann::json fields_arr = nlohmann::json::array();
			for (const auto& f : msg.fields) {
				nlohmann::json fj;
				fj["name"] = f.name;
				fj["number"] = f.number;
				fj["type"] = proto_decoder::field_type_name(f.type);
				fj["type_id"] = f.type;
				fj["label"] = proto_decoder::field_label_name(f.label);
				if (!f.type_name.empty()) fj["type_name"] = f.type_name;
				if (!f.default_value.empty()) fj["default_value"] = f.default_value;
				fields_arr.push_back(fj);
			}
			mj["fields"] = fields_arr;
			mj["nested_messages"] = messages_to_json(msg.nested);
			mj["enums"] = enums_to_json(msg.enums);
			arr.push_back(mj);
		}
		return arr;
	}

	static nlohmann::json services_to_json(const std::vector<proto_decoder::ProtoService>& services) {
		nlohmann::json arr = nlohmann::json::array();
		for (const auto& svc : services) {
			nlohmann::json sj;
			sj["name"] = svc.name;
			nlohmann::json methods = nlohmann::json::array();
			for (const auto& m : svc.methods) {
				nlohmann::json mj;
				mj["name"] = m.name;
				mj["input_type"] = m.input_type;
				mj["output_type"] = m.output_type;
				methods.push_back(mj);
			}
			sj["methods"] = methods;
			arr.push_back(sj);
		}
		return arr;
	}

	void dump_as_json() {
		if (!_decoded_once) decode_all();
		nlohmann::json root;
		root["tool"] = "Prometheus Proto Viewer";
		root["build"] = 24919;
		root["proto_count"] = (int)_decoded.size();

		nlohmann::json files_arr = nlohmann::json::array();
		for (const auto& df : _decoded) {
			nlohmann::json fj;
			fj["name"] = df.file.name;
			fj["package"] = df.file.package;
			fj["blob_size"] = df.blob_size;
			fj["dependencies"] = df.file.dependencies;

			fj["messages"] = messages_to_json(df.file.messages);
			fj["enums"] = enums_to_json(df.file.enums);
			fj["services"] = services_to_json(df.file.services);

			files_arr.push_back(fj);
		}
		root["files"] = files_arr;

		std::filesystem::create_directories("proto_dump");
		std::ofstream f("proto_dump/proto_schema.json");
		if (f.is_open()) {
			f << root.dump(2);
		}
		_status = "Dumped JSON schema to proto_dump/proto_schema.json";
	}

public:
	inline void render() override {
		if (open_window(nullptr, 0, ImVec2(900, 650))) {
			size_t blob_count;
			{
				std::lock_guard<std::mutex> lock(proto_capture::g_protoMutex);
				blob_count = proto_capture::g_capturedProtos.size();
			}

			ImGui::Text("Captured Blobs: %zu", blob_count);
			if (_decoded_once) {
				ImGui::SameLine();
				int valid_count = 0;
				for (const auto& d : _decoded) if (d.valid) valid_count++;
				ImGui::Text("| Decoded: %d/%d valid", valid_count, (int)_decoded.size());
			}

			if (blob_count == 0) {
				ImGui::BeginDisabled();
				ImGui::Button("Decode All");
				ImGui::EndDisabled();
			}
			else {
				if (ImGui::Button("Decode All")) {
					decode_all();
				}
			}

			bool has_decoded = _decoded_once && !_decoded.empty();

			ImGui::SameLine();
			if (!has_decoded) ImGui::BeginDisabled();
			if (ImGui::Button("Dump Raw Blobs")) dump_raw_blobs();
			ImGui::SameLine();
			if (ImGui::Button("Dump as .proto")) dump_as_proto();
			ImGui::SameLine();
			if (ImGui::Button("Dump as JSON")) dump_as_json();
			if (!has_decoded) ImGui::EndDisabled();

			if (!_status.empty()) {
				ImGui::SameLine();
				if (ImGui::Button("Open Folder")) {
					char cwd[MAX_PATH];
					GetCurrentDirectoryA(MAX_PATH, cwd);
					std::string path = std::string(cwd) + "\\proto_dump";
					ShellExecuteA(NULL, "explore", path.c_str(), NULL, NULL, SW_SHOWNORMAL);
				}
				ImGui::SameLine();
				ImGui::TextColored(ImVec4(0.4f, 0.9f, 0.4f, 1.0f), "%s", _status.c_str());
			}

			ImGui::Separator();
			_filter.Draw("Filter##proto_filter", -1.0f);
			ImGui::Separator();

			if (!_decoded_once) {
				if (blob_count > 0) {
					ImGui::TextDisabled("Click 'Decode All' to parse captured protobuf blobs.");
				}
				else {
					ImGui::TextDisabled("No protobuf blobs captured yet. They are captured at game startup.");
				}
			}
			else {
				float panel_width = ImGui::GetContentRegionAvail().x;
				float left_width = panel_width * 0.3f;

				ImGui::BeginChild("proto_list", ImVec2(left_width, 0), ImGuiChildFlags_Border | ImGuiChildFlags_ResizeX);
				for (int i = 0; i < (int)_decoded.size(); i++) {
					const auto& df = _decoded[i];
					if (!df.valid) continue;

					if (_filter.IsActive() && !_filter.PassFilter(df.file.name.c_str()) &&
						!_filter.PassFilter(df.file.package.c_str())) {
						continue;
					}

					ImGui::PushID(i);
					bool selected = (_selected == i);

					std::string display_name = df.file.name;
					size_t last_slash = display_name.find_last_of('/');
					if (last_slash != std::string::npos) {
						display_name = display_name.substr(last_slash + 1);
					}

					char label[256];
					sprintf_s(label, "%s (%zu B)", display_name.c_str(), df.blob_size);

					if (ImGui::Selectable(label, selected)) {
						_selected = i;
					}
					if (ImGui::IsItemHovered()) {
						ImGui::SetTooltip("%s\nPackage: %s\nSize: %zu bytes\nMessages: %d\nEnums: %d\nServices: %d",
							df.file.name.c_str(),
							df.file.package.c_str(),
							df.blob_size,
							(int)df.file.messages.size(),
							(int)df.file.enums.size(),
							(int)df.file.services.size());
					}
					ImGui::PopID();
				}
				ImGui::EndChild();

				ImGui::SameLine();

				ImGui::BeginChild("proto_detail", ImVec2(0, 0), ImGuiChildFlags_Border);
				if (_selected >= 0 && _selected < (int)_decoded.size() && _decoded[_selected].valid) {
					const auto& df = _decoded[_selected];

					ImGui::TextColored(ImVec4(0.4f, 0.8f, 1.0f, 1.0f), "%s", df.file.name.c_str());
					if (!df.file.package.empty()) {
						ImGui::SameLine();
						ImGui::TextDisabled("(%s)", df.file.package.c_str());
					}
					ImGui::Text("Size: %zu bytes | Messages: %d | Enums: %d | Services: %d",
						df.blob_size,
						(int)df.file.messages.size(),
						(int)df.file.enums.size(),
						(int)df.file.services.size());

					if (!df.file.dependencies.empty()) {
						if (ImGui::TreeNodeEx("Dependencies", ImGuiTreeNodeFlags_None)) {
							for (const auto& dep : df.file.dependencies) {
								ImGui::BulletText("%s", dep.c_str());
							}
							ImGui::TreePop();
						}
					}

					ImGui::Separator();

					if (!df.file.messages.empty()) {
						if (ImGui::TreeNodeEx("Messages", ImGuiTreeNodeFlags_DefaultOpen)) {
							for (const auto& msg : df.file.messages) {
								render_message(msg);
							}
							ImGui::TreePop();
						}
					}

					if (!df.file.enums.empty()) {
						if (ImGui::TreeNodeEx("Enums", ImGuiTreeNodeFlags_DefaultOpen)) {
							for (const auto& e : df.file.enums) {
								render_enum(e);
							}
							ImGui::TreePop();
						}
					}

					if (!df.file.services.empty()) {
						if (ImGui::TreeNodeEx("Services", ImGuiTreeNodeFlags_DefaultOpen)) {
							for (const auto& svc : df.file.services) {
								render_service(svc);
							}
							ImGui::TreePop();
						}
					}
				}
				else {
					ImGui::TextDisabled("Select a proto file from the list.");
				}
				ImGui::EndChild();
			}
		}
		ImGui::End();
	}
};

WINDOW_REGISTER(proto_viewer_window);
