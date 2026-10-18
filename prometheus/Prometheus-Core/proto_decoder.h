#pragma once
#include <string>
#include <vector>
#include <cstdint>
#include <utility>

// Pure protobuf wire-format decoder for FileDescriptorProto blobs captured by
// proto_capture.h. No dependency on Google's protobuf runtime.
namespace proto_decoder {

	// --- Data structures matching descriptor.proto field numbers ---

	struct ProtoField {
		std::string name;        // field 1
		int number = 0;          // field 3
		int type = 0;            // field 5
		int label = 0;           // field 4
		std::string type_name;   // field 6
		std::string default_value; // field 7
	};

	struct ProtoEnum {
		std::string name;
		std::vector<std::pair<std::string, int>> values;
	};

	struct ProtoMessage {
		std::string name;
		std::vector<ProtoField> fields;
		std::vector<ProtoMessage> nested;
		std::vector<ProtoEnum> enums;
	};

	struct ProtoMethod {
		std::string name;        // field 1
		std::string input_type;  // field 2
		std::string output_type; // field 3
	};

	struct ProtoService {
		std::string name;
		std::vector<ProtoMethod> methods;
	};

	struct ProtoFile {
		std::string name;        // field 1
		std::string package;     // field 2
		std::vector<std::string> dependencies; // field 3
		std::vector<ProtoMessage> messages;    // field 4
		std::vector<ProtoEnum> enums;          // field 5
		std::vector<ProtoService> services;    // field 6
	};

	// --- Wire format helpers ---

	struct Reader {
		const uint8_t* data;
		size_t size;
		size_t pos;

		Reader(const uint8_t* d, size_t s) : data(d), size(s), pos(0) {}

		bool has_data() const { return pos < size; }

		bool read_varint(uint64_t& out) {
			out = 0;
			int shift = 0;
			while (pos < size) {
				uint8_t b = data[pos++];
				out |= (uint64_t)(b & 0x7F) << shift;
				if ((b & 0x80) == 0) return true;
				shift += 7;
				if (shift >= 64) return false;
			}
			return false;
		}

		bool read_tag(uint32_t& field_number, uint32_t& wire_type) {
			uint64_t v;
			if (!read_varint(v)) return false;
			wire_type = (uint32_t)(v & 0x7);
			field_number = (uint32_t)(v >> 3);
			return true;
		}

		bool read_length_delimited(const uint8_t*& out_data, size_t& out_len) {
			uint64_t len;
			if (!read_varint(len)) return false;
			if (pos + len > size) return false;
			out_data = data + pos;
			out_len = (size_t)len;
			pos += (size_t)len;
			return true;
		}

		bool read_string(std::string& out) {
			const uint8_t* d;
			size_t len;
			if (!read_length_delimited(d, len)) return false;
			out.assign((const char*)d, len);
			return true;
		}

		bool skip_field(uint32_t wire_type) {
			switch (wire_type) {
			case 0: { // varint
				uint64_t v;
				return read_varint(v);
			}
			case 1: { // 64-bit
				if (pos + 8 > size) return false;
				pos += 8;
				return true;
			}
			case 2: { // length-delimited
				const uint8_t* d;
				size_t len;
				return read_length_delimited(d, len);
			}
			case 5: { // 32-bit
				if (pos + 4 > size) return false;
				pos += 4;
				return true;
			}
			default:
				return false;
			}
		}
	};

	// --- Decoders for each proto message type ---

	inline std::pair<std::string, int> decode_enum_value_descriptor(const uint8_t* data, size_t size) {
		Reader r(data, size);
		std::string name;
		int number = 0;
		while (r.has_data()) {
			uint32_t field_num, wire_type;
			if (!r.read_tag(field_num, wire_type)) break;
			switch (field_num) {
			case 1: // name
				if (wire_type == 2) r.read_string(name);
				else r.skip_field(wire_type);
				break;
			case 2: // number
				if (wire_type == 0) {
					uint64_t v; r.read_varint(v);
					number = (int)(int32_t)v;
				}
				else r.skip_field(wire_type);
				break;
			default:
				r.skip_field(wire_type);
				break;
			}
		}
		return { name, number };
	}

	inline ProtoEnum decode_enum_descriptor(const uint8_t* data, size_t size) {
		Reader r(data, size);
		ProtoEnum e;
		while (r.has_data()) {
			uint32_t field_num, wire_type;
			if (!r.read_tag(field_num, wire_type)) break;
			switch (field_num) {
			case 1: // name
				if (wire_type == 2) r.read_string(e.name);
				else r.skip_field(wire_type);
				break;
			case 2: // value (EnumValueDescriptorProto)
				if (wire_type == 2) {
					const uint8_t* d; size_t len;
					if (r.read_length_delimited(d, len)) {
						e.values.push_back(decode_enum_value_descriptor(d, len));
					}
				}
				else r.skip_field(wire_type);
				break;
			default:
				r.skip_field(wire_type);
				break;
			}
		}
		return e;
	}

	inline ProtoField decode_field_descriptor(const uint8_t* data, size_t size) {
		Reader r(data, size);
		ProtoField f;
		while (r.has_data()) {
			uint32_t field_num, wire_type;
			if (!r.read_tag(field_num, wire_type)) break;
			switch (field_num) {
			case 1: // name
				if (wire_type == 2) r.read_string(f.name);
				else r.skip_field(wire_type);
				break;
			case 3: // number
				if (wire_type == 0) {
					uint64_t v; r.read_varint(v);
					f.number = (int)v;
				}
				else r.skip_field(wire_type);
				break;
			case 4: // label
				if (wire_type == 0) {
					uint64_t v; r.read_varint(v);
					f.label = (int)v;
				}
				else r.skip_field(wire_type);
				break;
			case 5: // type
				if (wire_type == 0) {
					uint64_t v; r.read_varint(v);
					f.type = (int)v;
				}
				else r.skip_field(wire_type);
				break;
			case 6: // type_name
				if (wire_type == 2) r.read_string(f.type_name);
				else r.skip_field(wire_type);
				break;
			case 7: // default_value
				if (wire_type == 2) r.read_string(f.default_value);
				else r.skip_field(wire_type);
				break;
			default:
				r.skip_field(wire_type);
				break;
			}
		}
		return f;
	}

	inline ProtoMessage decode_message_descriptor(const uint8_t* data, size_t size) {
		Reader r(data, size);
		ProtoMessage msg;
		while (r.has_data()) {
			uint32_t field_num, wire_type;
			if (!r.read_tag(field_num, wire_type)) break;
			switch (field_num) {
			case 1: // name
				if (wire_type == 2) r.read_string(msg.name);
				else r.skip_field(wire_type);
				break;
			case 2: // field
				if (wire_type == 2) {
					const uint8_t* d; size_t len;
					if (r.read_length_delimited(d, len)) {
						msg.fields.push_back(decode_field_descriptor(d, len));
					}
				}
				else r.skip_field(wire_type);
				break;
			case 3: // nested_type
				if (wire_type == 2) {
					const uint8_t* d; size_t len;
					if (r.read_length_delimited(d, len)) {
						msg.nested.push_back(decode_message_descriptor(d, len));
					}
				}
				else r.skip_field(wire_type);
				break;
			case 4: // enum_type
				if (wire_type == 2) {
					const uint8_t* d; size_t len;
					if (r.read_length_delimited(d, len)) {
						msg.enums.push_back(decode_enum_descriptor(d, len));
					}
				}
				else r.skip_field(wire_type);
				break;
			default:
				r.skip_field(wire_type);
				break;
			}
		}
		return msg;
	}

	inline ProtoMethod decode_method_descriptor(const uint8_t* data, size_t size) {
		Reader r(data, size);
		ProtoMethod m;
		while (r.has_data()) {
			uint32_t field_num, wire_type;
			if (!r.read_tag(field_num, wire_type)) break;
			switch (field_num) {
			case 1: // name
				if (wire_type == 2) r.read_string(m.name);
				else r.skip_field(wire_type);
				break;
			case 2: // input_type
				if (wire_type == 2) r.read_string(m.input_type);
				else r.skip_field(wire_type);
				break;
			case 3: // output_type
				if (wire_type == 2) r.read_string(m.output_type);
				else r.skip_field(wire_type);
				break;
			default:
				r.skip_field(wire_type);
				break;
			}
		}
		return m;
	}

	inline ProtoService decode_service_descriptor(const uint8_t* data, size_t size) {
		Reader r(data, size);
		ProtoService svc;
		while (r.has_data()) {
			uint32_t field_num, wire_type;
			if (!r.read_tag(field_num, wire_type)) break;
			switch (field_num) {
			case 1: // name
				if (wire_type == 2) r.read_string(svc.name);
				else r.skip_field(wire_type);
				break;
			case 2: // method
				if (wire_type == 2) {
					const uint8_t* d; size_t len;
					if (r.read_length_delimited(d, len)) {
						svc.methods.push_back(decode_method_descriptor(d, len));
					}
				}
				else r.skip_field(wire_type);
				break;
			default:
				r.skip_field(wire_type);
				break;
			}
		}
		return svc;
	}

	inline ProtoFile decode_file_descriptor(const uint8_t* data, size_t size) {
		Reader r(data, size);
		ProtoFile file;
		while (r.has_data()) {
			uint32_t field_num, wire_type;
			if (!r.read_tag(field_num, wire_type)) break;
			switch (field_num) {
			case 1: // name
				if (wire_type == 2) r.read_string(file.name);
				else r.skip_field(wire_type);
				break;
			case 2: // package
				if (wire_type == 2) r.read_string(file.package);
				else r.skip_field(wire_type);
				break;
			case 3: // dependency
				if (wire_type == 2) {
					std::string dep;
					r.read_string(dep);
					file.dependencies.push_back(std::move(dep));
				}
				else r.skip_field(wire_type);
				break;
			case 4: // message_type
				if (wire_type == 2) {
					const uint8_t* d; size_t len;
					if (r.read_length_delimited(d, len)) {
						file.messages.push_back(decode_message_descriptor(d, len));
					}
				}
				else r.skip_field(wire_type);
				break;
			case 5: // enum_type
				if (wire_type == 2) {
					const uint8_t* d; size_t len;
					if (r.read_length_delimited(d, len)) {
						file.enums.push_back(decode_enum_descriptor(d, len));
					}
				}
				else r.skip_field(wire_type);
				break;
			case 6: // service
				if (wire_type == 2) {
					const uint8_t* d; size_t len;
					if (r.read_length_delimited(d, len)) {
						file.services.push_back(decode_service_descriptor(d, len));
					}
				}
				else r.skip_field(wire_type);
				break;
			default:
				r.skip_field(wire_type);
				break;
			}
		}
		return file;
	}

	// --- Human-readable type names ---

	inline const char* field_type_name(int type) {
		switch (type) {
		case 1:  return "double";
		case 2:  return "float";
		case 3:  return "int64";
		case 4:  return "uint64";
		case 5:  return "int32";
		case 6:  return "fixed64";
		case 7:  return "fixed32";
		case 8:  return "bool";
		case 9:  return "string";
		case 10: return "group";
		case 11: return "message";
		case 12: return "bytes";
		case 13: return "uint32";
		case 14: return "enum";
		case 15: return "sfixed32";
		case 16: return "sfixed64";
		case 17: return "sint32";
		case 18: return "sint64";
		default: return "unknown";
		}
	}

	inline const char* field_label_name(int label) {
		switch (label) {
		case 1: return "optional";
		case 2: return "required";
		case 3: return "repeated";
		default: return "";
		}
	}

	// --- .proto text reconstruction ---

	inline void write_indent(std::string& out, int depth) {
		for (int i = 0; i < depth; i++) out += "  ";
	}

	inline void reconstruct_enum(std::string& out, const ProtoEnum& e, int depth) {
		write_indent(out, depth);
		out += "enum " + e.name + " {\n";
		for (const auto& v : e.values) {
			write_indent(out, depth + 1);
			out += v.first + " = " + std::to_string(v.second) + ";\n";
		}
		write_indent(out, depth);
		out += "}\n";
	}

	inline void reconstruct_message(std::string& out, const ProtoMessage& msg, int depth) {
		write_indent(out, depth);
		out += "message " + msg.name + " {\n";

		for (const auto& e : msg.enums) {
			reconstruct_enum(out, e, depth + 1);
		}

		for (const auto& nested : msg.nested) {
			reconstruct_message(out, nested, depth + 1);
		}

		for (const auto& f : msg.fields) {
			write_indent(out, depth + 1);
			std::string label = field_label_name(f.label);
			if (!label.empty()) out += label + " ";

			if (f.type == 11 || f.type == 14) {
				// message or enum - use type_name
				out += f.type_name;
			}
			else {
				out += field_type_name(f.type);
			}
			out += " " + f.name + " = " + std::to_string(f.number);
			if (!f.default_value.empty()) {
				out += " [default = " + f.default_value + "]";
			}
			out += ";\n";
		}

		write_indent(out, depth);
		out += "}\n";
	}

	inline std::string reconstruct_proto(const ProtoFile& file) {
		std::string out;
		out += "// Reconstructed from Overwatch beta 0.8 (build 24919)\n";
		out += "// Source: " + file.name + "\n\n";
		out += "syntax = \"proto2\";\n\n";

		if (!file.package.empty()) {
			out += "package " + file.package + ";\n\n";
		}

		for (const auto& dep : file.dependencies) {
			out += "import \"" + dep + "\";\n";
		}
		if (!file.dependencies.empty()) out += "\n";

		for (const auto& e : file.enums) {
			reconstruct_enum(out, e, 0);
			out += "\n";
		}

		for (const auto& msg : file.messages) {
			reconstruct_message(out, msg, 0);
			out += "\n";
		}

		for (const auto& svc : file.services) {
			out += "service " + svc.name + " {\n";
			for (const auto& m : svc.methods) {
				out += "  rpc " + m.name + " (" + m.input_type + ") returns (" + m.output_type + ");\n";
			}
			out += "}\n\n";
		}

		return out;
	}

}
