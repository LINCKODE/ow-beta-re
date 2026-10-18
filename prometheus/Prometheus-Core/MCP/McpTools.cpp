#include "McpTools.h"
#include <map>
#include "../globals.h"
#include "../stringhash_library.h"
#include "../STU.h"
#include "../Statescript.h"
#include "../StatescriptVar.h"
#include "../Network/ServerBridge.h"
#include "../player_spawner.h"
#include "../entity_admin.h"
#include "../stu_resources.h"
#include "../STU_Editable.h"
#include "../Viewmodel.h"
#include "../game.h"
#include "../Logs/Logs.h"
#include "../Utility/SafeMemory.h"
#include "EcsTrace.h"
#include "SsTrace.h"
#include "LogicalButton.h"
#include "../Systems/DebugDraw.h"
#include <windows.h>
#define STATESCRIPT_DUMPER_NO_REGISTER
#include "../UI/windows/statescript_dumper.h"
#include <sstream>
#include <iomanip>
#include <set>
#include <algorithm>
#include "../Network/CustomJam.h"
#include "../Systems/ServerView.h"

namespace prom_mcp {

bool g_write_enabled = false;

bool writes_allowed() {
#ifdef PROMETHEUS_MCP_NO_WRITES
    return false;
#else
    return g_write_enabled;
#endif
}

static const char* writes_refused_message() {
#ifdef PROMETHEUS_MCP_NO_WRITES
    return "writes are compiled out of this build (PROMETHEUS_MCP_NO_WRITES in MCP/McpTools.h; read the risks there first)";
#else
    return "writes disabled (toggle in MCP Server window)";
#endif
}

// Every tool, or tool action, that changes game memory or state, runs game code, injects input or resources, or
// writes files. Read-only tools and the overlay-only ones (debug_draw, add_hash: in-memory names) are not listed.
bool is_write_call(const std::string& tool, const nlohmann::json& args) {
    static const std::set<std::string> always = {
        "call_function", "call_function_void", "write_memory", "inject_resource",
        "execute_node", "start_stop_state", "play_script",
        "execute_node_server", "hero_attach_weapons", "server_echo",
        "write_viewmodel", "construct_component",
    };
    if (always.count(tool)) return true;
    std::string action = args.is_object() ? args.value("action", std::string{}) : std::string{};
    if (tool == "logical_button") return action == "press" || action == "release" || action == "tap";
    if (tool == "ecs_trace" || tool == "ss_trace") return action == "save";  // writes a file at a caller-given path
    return false;
}

// Wraps every tool's handler so write calls are refused centrally, whoever calls (HTTP API, PrometheusLobby
// channel, other modules through find_tool).
static std::vector<ToolDef> gate_writes(std::vector<ToolDef> tools) {
    for (auto& t : tools) {
        auto inner = t.handler;
        std::string name = t.name;
        t.handler = [inner, name](const nlohmann::json& args) -> nlohmann::json {
            if (is_write_call(name, args) && !writes_allowed())
                return { {"error", std::string(writes_refused_message())}, {"tool", name} };
            return inner(args);
        };
    }
    return tools;
}

// --- Helpers --------------------------------------------------------------

static uint64_t parse_u64(const nlohmann::json& v) {
    if (v.is_number_unsigned()) return v.get<uint64_t>();
    if (v.is_number_integer())  return (uint64_t)v.get<int64_t>();
    if (v.is_string()) {
        std::string s = v.get<std::string>();
        int base = 10;
        const char* p = s.c_str();
        if (s.size() > 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X')) { base = 16; p += 2; }
        return strtoull(p, nullptr, base);
    }
    return 0;
}

static int parse_int(const nlohmann::json& v, int dflt = 0) {
    if (v.is_number_integer()) return v.get<int>();
    if (v.is_string()) return (int)parse_u64(v);
    return dflt;
}

static std::string hex_dump(const uint8_t* p, size_t n) {
    std::ostringstream os;
    os << std::hex << std::setfill('0');
    for (size_t i = 0; i < n; i++) {
        if (i) os << " ";
        os << std::setw(2) << (int)p[i];
    }
    return os.str();
}

static nlohmann::json err(const std::string& msg) {
    return { {"error", msg} };
}

// Memory checks for the tools. Never IsBadReadPtr: under Wine it touches the pages (a guard page escapes
// its handler and takes the thread down), and __try (seh_guard) is a no-op with llvm-mingw. See
// Utility/SafeMemory.h.
template<typename T>
static bool bad_read(T p, size_t n) {
    return !Utility::SafeMemory::IsReadable((const void*)(uintptr_t)p, n);
}

// Copy n bytes of (possibly unreadable) memory: the bytes, or an error saying which page is bad.
static bool read_bytes(uintptr_t at, size_t n, std::vector<uint8_t>& out, std::string& why) {
    out.resize(n);
    return Utility::SafeMemory::Read(out.data(), (const void*)at, n, &why);
}

// "game+0x<rva>" / "prometheus+0x<off>" / absolute: code pointers comparable across runs and with Ghidra
// (Ghidra addr = 0x7ff64ffe0000 + rva).
static std::string fmt_code_ptr(uintptr_t p) {
    if (p >= globals::gameBase && p - globals::gameBase < globals::gameSize)
        return std::format("game+0x{:x}", p - globals::gameBase);
    if (p >= globals::modBase && p - globals::modBase < globals::modSize)
        return std::format("prometheus+0x{:x}", p - globals::modBase);
    return std::format("0x{:x}", p);
}

// SEH wrapper: runs body in __try, returns err json on AV. NOTE: with llvm-mingw (our build) __try/__except
// compile to nothing, so this catches nothing: memory reads must go through bad_read/read_bytes.
template<typename F>
static nlohmann::json seh_guard(const char* what, F&& f) {
    nlohmann::json result;
    __try {
        result = f();
    }
    __except (EXCEPTION_EXECUTE_HANDLER) {
        result = err(std::string("seh: ") + what + " faulted");
    }
    return result;
}

// --- Tool implementations -------------------------------------------------

// Read N bytes at RVA. Returns hex string.
static nlohmann::json tool_read_memory(const nlohmann::json& a) {
    uint64_t rva = parse_u64(a.value("rva", nlohmann::json(0)));
    int len = parse_int(a.value("length", nlohmann::json(16)));
    if (len <= 0 || len > 4096) return err("length must be 1..4096");
    uintptr_t addr = globals::gameBase + rva;
    std::vector<uint8_t> buf;
    std::string why;
    if (!read_bytes(addr, len, buf, why)) return err("unreadable: " + why);
    return {
        {"rva", std::format("0x{:x}", rva)},
        {"abs", std::format("0x{:x}", addr)},
        {"length", len},
        {"hex", hex_dump(buf.data(), len)},
    };
}

// Read N bytes at absolute address.
static nlohmann::json tool_read_memory_abs(const nlohmann::json& a) {
    uint64_t abs = parse_u64(a.value("addr", nlohmann::json(0)));
    int len = parse_int(a.value("length", nlohmann::json(16)));
    if (len <= 0 || len > 4096) return err("length must be 1..4096");
    std::vector<uint8_t> buf;
    std::string why;
    if (!read_bytes(abs, len, buf, why)) return err("unreadable: " + why);
    return {
        {"abs", std::format("0x{:x}", abs)},
        {"length", len},
        {"hex", hex_dump(buf.data(), len)},
    };
}

// Read null-terminated string at RVA.
static nlohmann::json tool_read_string(const nlohmann::json& a) {
    uint64_t rva = parse_u64(a.value("rva", nlohmann::json(0)));
    int max_len = parse_int(a.value("max_len", nlohmann::json(256)));
    if (max_len <= 0 || max_len > 8192) return err("max_len must be 1..8192");
    uintptr_t addr = globals::gameBase + rva;
    // Copy what is readable up to max_len (a short string may sit right before an unreadable page).
    size_t span = Utility::SafeMemory::ReadableSpan((const void*)addr, max_len);
    std::vector<uint8_t> buf;
    std::string why;
    if (!span) {
        Utility::SafeMemory::IsReadable((const void*)addr, 1, &why);
        return err("unreadable: " + why);
    }
    if (!read_bytes(addr, span, buf, why)) return err("unreadable: " + why);
    size_t n = strnlen_s((const char*)buf.data(), span);
    if (n == span && span < (size_t)max_len) return err(std::format("unterminated: {} readable bytes, no NUL", span));
    return {
        {"rva", std::format("0x{:x}", rva)},
        {"length", (int)n},
        {"truncated", n == (size_t)max_len},
        {"value", std::string((const char*)buf.data(), n)},
    };
}

// Walk a pointer chain: read [base + offsets[0]], then [result + offsets[1]], ...
static nlohmann::json tool_read_pointer_chain(const nlohmann::json& a) {
    bool use_rva = a.contains("base_rva");
    uint64_t base = use_rva
        ? globals::gameBase + parse_u64(a.value("base_rva", nlohmann::json(0)))
        : parse_u64(a.value("base", nlohmann::json(0)));
    if (!a.contains("offsets") || !a["offsets"].is_array()) return err("offsets array required");
    return seh_guard("read_pointer_chain", [&]() -> nlohmann::json {
        std::vector<std::string> steps;
        uint64_t cur = base;
        steps.push_back(std::format("0x{:x}", cur));
        for (const auto& off : a["offsets"]) {
            uint64_t o = parse_u64(off);
            uint64_t at = cur + o;
            std::string why;
            if (!Utility::SafeMemory::Read(&cur, (const void*)at, 8, &why)) return err(std::format("unreadable @ 0x{:x}: {}", at, why));
            steps.push_back(std::format("0x{:x}", cur));
        }
        return {
            {"final", std::format("0x{:x}", cur)},
            {"steps", steps},
        };
    });
}

// Resolve a CRC32 hash via the project's hash library.
static nlohmann::json tool_resolve_hash(const nlohmann::json& a) {
    uint32_t h = (uint32_t)parse_u64(a.value("hash", nlohmann::json(0)));
    auto it = stringhash_library::hashes.find((int)h);
    if (it == stringhash_library::hashes.end()) return { {"hash", std::format("0x{:08x}", h)}, {"resolved", false} };
    return { {"hash", std::format("0x{:08x}", h)}, {"resolved", true}, {"name", it->second} };
}

// Add a hash → string entry at runtime (extends in-memory only; persisted to disk on next save if hashlib supports it).
static nlohmann::json tool_add_hash(const nlohmann::json& a) {
    uint32_t h = (uint32_t)parse_u64(a.value("hash", nlohmann::json(0)));
    if (!a.contains("name") || !a["name"].is_string()) return err("name required");
    std::string name = a["name"].get<std::string>();
    stringhash_library::hashes[(int)h] = name;
    return { {"ok", true}, {"hash", std::format("0x{:08x}", h)}, {"name", name} };
}

// List loaded statescripts on an EA.
static nlohmann::json tool_list_loaded_scripts(const nlohmann::json& a) {
    std::string ea_name = a.value("ea", "lobby");
    EntityAdminBase* ea = (ea_name == "game") ? GameEntityAdmin() : LobbyEntityAdmin();
    if (!ea) return err("entity admin null");
    auto comp = ea->getSingletonComponent<Component_23_Statescript>(0x23);
    if (!comp) return err("Component_23_Statescript not found");
    return seh_guard("list_loaded_scripts", [&]() -> nlohmann::json {
        nlohmann::json arr = nlohmann::json::array();
        for (int i = 0; i < comp->ss_inner.g1_instanceArr.num; i++) {
            auto inst = comp->ss_inner.g1_instanceArr.ptr[i];
            if (!inst) continue;
            arr.push_back({
                {"script_id", std::format("0x{:016x}", inst->script_id)},
                {"instance_id", (int)inst->instance_id},
                {"nodes", inst->graph ? inst->graph->m_nodes.count() : 0},
            });
        }
        return { {"ea", ea_name}, {"scripts", arr} };
    });
}

// Full node dump (types, plugs, links, config vars with live values) of one
// loaded script instance, via the Statescript dumper's extractor.
static nlohmann::json tool_dump_script(const nlohmann::json& a) {
    std::string ea_name = a.value("ea", "lobby");
    uint64_t script_id = parse_u64(a.value("script_id", nlohmann::json(0)));
    EntityAdminBase* ea = (ea_name == "game") ? GameEntityAdmin() : LobbyEntityAdmin();
    if (!ea) return err("entity admin null");
    Component_23_Statescript* comp = nullptr;
    if (a.contains("entity_id")) {
        // an entity's own Statescript component (e.g. a hero spawned by the server)
        Entity* ent = ea->getEntById((uint32)parse_u64(a["entity_id"]));
        if (!ent) return err("entity not found on this ea");
        comp = ent->getById<Component_23_Statescript>(0x23);
    } else {
        comp = ea->getSingletonComponent<Component_23_Statescript>(0x23);
    }
    if (!comp) return err("Component_23_Statescript not found");
    return seh_guard("dump_script", [&]() -> nlohmann::json {
        for (int i = 0; i < comp->ss_inner.g1_instanceArr.num; i++) {
            auto inst = comp->ss_inner.g1_instanceArr.ptr[i];
            if (inst && inst->graph && (uint64_t)inst->script_id == script_id)
                return statescript_dumper::dump_instance(inst);
        }
        return err(std::format("script 0x{:016x} is not loaded on the {} EA", script_id, ea_name));
    });
}

// Look up STU info by hash.
static nlohmann::json tool_lookup_stu_type(const nlohmann::json& a) {
    uint32_t h = (uint32_t)parse_u64(a.value("hash", nlohmann::json(0)));
    return seh_guard("lookup_stu_type", [&]() -> nlohmann::json {
        auto* info = STURegistry::Get()->GetSTUInfoByHash(h);
        if (!info) return err("hash not found in STU registry");
        nlohmann::json args = nlohmann::json::array();
        for (auto [owner, arg] : info->RangeArgs()) {
            nlohmann::json a;
            if (arg->Name_str) a["name"] = arg->Name_str;
            a["hash"] = std::format("0x{:08x}", arg->Hash);
            a["offset"] = std::format("0x{:x}", arg->Offset);
            if (owner) a["declared_by_hash"] = std::format("0x{:08x}", owner->Hash);
            args.push_back(a);
        }
        return {
            {"hash", std::format("0x{:08x}", h)},
            {"name", info->Name_str ? info->Name_str : ""},
            {"args", args},
        };
    });
}

// High-level game state snapshot.
static nlohmann::json tool_game_state(const nlohmann::json& /*a*/) {
    return seh_guard("game_state", [&]() -> nlohmann::json {
        nlohmann::json j;
        j["game_base"] = std::format("0x{:x}", globals::gameBase);
        j["module_base"] = std::format("0x{:x}", globals::modBase);
        j["game_size"] = globals::gameSize;
        auto lobby = LobbyEntityAdmin();
        auto game = GameEntityAdmin();
        if (lobby) {
            auto c = lobby->getSingletonComponent<Component_23_Statescript>(0x23);
            j["lobby_ea"] = { {"present", true}, {"loaded_scripts", c ? c->ss_inner.g1_instanceArr.num : 0} };
        } else {
            j["lobby_ea"] = { {"present", false} };
        }
        if (game) {
            auto c = game->getSingletonComponent<Component_23_Statescript>(0x23);
            j["game_ea"] = { {"present", true}, {"loaded_scripts", c ? c->ss_inner.g1_instanceArr.num : 0} };
        } else {
            j["game_ea"] = { {"present", false} };
        }
        return j;
    });
}

// --- Phase 2: call_function & friends -----------------------------------------

// Call a function at RVA with up to 8 uint64 args. Returns uint64 result.
static nlohmann::json tool_call_function(const nlohmann::json& a) {
    uint64_t rva = parse_u64(a.value("rva", nlohmann::json(0)));
    if (!a.contains("args") || !a["args"].is_array()) return err("args array required");
    auto& arr = a["args"];
    if (arr.size() > 8) return err("max 8 args");
    uint64_t v[8] = {};
    for (size_t i = 0; i < arr.size(); i++) v[i] = parse_u64(arr[i]);
    void* fn = (void*)(globals::gameBase + rva);
    if (bad_read(fn, 1)) return err("unreadable function address");
    return seh_guard("call_function", [&]() -> nlohmann::json {
        uint64_t r = 0;
        switch (arr.size()) {
        case 0: r = ((uint64_t(*)())fn)(); break;
        case 1: r = ((uint64_t(*)(uint64_t))fn)(v[0]); break;
        case 2: r = ((uint64_t(*)(uint64_t,uint64_t))fn)(v[0],v[1]); break;
        case 3: r = ((uint64_t(*)(uint64_t,uint64_t,uint64_t))fn)(v[0],v[1],v[2]); break;
        case 4: r = ((uint64_t(*)(uint64_t,uint64_t,uint64_t,uint64_t))fn)(v[0],v[1],v[2],v[3]); break;
        case 5: r = ((uint64_t(*)(uint64_t,uint64_t,uint64_t,uint64_t,uint64_t))fn)(v[0],v[1],v[2],v[3],v[4]); break;
        case 6: r = ((uint64_t(*)(uint64_t,uint64_t,uint64_t,uint64_t,uint64_t,uint64_t))fn)(v[0],v[1],v[2],v[3],v[4],v[5]); break;
        case 7: r = ((uint64_t(*)(uint64_t,uint64_t,uint64_t,uint64_t,uint64_t,uint64_t,uint64_t))fn)(v[0],v[1],v[2],v[3],v[4],v[5],v[6]); break;
        case 8: r = ((uint64_t(*)(uint64_t,uint64_t,uint64_t,uint64_t,uint64_t,uint64_t,uint64_t,uint64_t))fn)(v[0],v[1],v[2],v[3],v[4],v[5],v[6],v[7]); break;
        }
        return {
            {"rva", std::format("0x{:x}", rva)},
            {"args", a["args"]},
            {"result", std::format("0x{:x}", r)},
            {"result_u64", r},
        };
    });
}

// Same as call_function but treats function as void (still returns whatever rax holds).
static nlohmann::json tool_call_function_void(const nlohmann::json& a) {
    return tool_call_function(a);
}

// Write bytes at RVA. Gated by g_write_enabled toggle.
static nlohmann::json tool_write_memory(const nlohmann::json& a) {
    if (!writes_allowed()) return err(writes_refused_message());
    uint64_t rva = parse_u64(a.value("rva", nlohmann::json(0)));
    if (!a.contains("hex") || !a["hex"].is_string()) return err("hex string required");
    std::string hex = a["hex"].get<std::string>();
    std::vector<uint8_t> bytes;
    for (size_t i = 0; i < hex.size(); ) {
        while (i < hex.size() && hex[i] == ' ') i++;
        if (i + 1 >= hex.size()) break;
        char buf[3] = { hex[i], hex[i+1], 0 };
        bytes.push_back((uint8_t)strtoul(buf, nullptr, 16));
        i += 2;
    }
    if (bytes.empty()) return err("no bytes parsed");
    uint8_t* addr = (uint8_t*)(globals::gameBase + rva);
    if (bad_read(addr, 1)) return err("unreadable address");
    DWORD old;
    if (!VirtualProtect(addr, bytes.size(), PAGE_EXECUTE_READWRITE, &old))
        return err(std::format("VirtualProtect failed: {}", GetLastError()));
    return seh_guard("write_memory", [&]() -> nlohmann::json {
        memcpy(addr, bytes.data(), bytes.size());
        VirtualProtect(addr, bytes.size(), old, &old);
        LOG_CORE(Warn, "[MCP] wrote {} bytes at gameBase+0x{:x}", bytes.size(), rva);
        return {
            {"rva", std::format("0x{:x}", rva)},
            {"length", (int)bytes.size()},
            {"written_hex", hex_dump(bytes.data(), bytes.size())},
        };
    });
}

// Create a resource from a file under a GUID (T045, docs/Maps.md §5: runtime delivery of edited assets).
// Engine path (docs/UISystem.md §4): FUN_7ff6509a5de0(u64* handle, desc*) finds or creates the resource entry
// for the GUID and runs CreateFromMemory (FUN_7ff6509a9130): the buffer goes straight to "awaiting creation",
// then the handler's normal construct/ready path. The entry must not be loaded (e.g. inject a map's data while
// another map or the lobby is up). The returned handle holds a reference, kept here so the resource stays
// resident; later AcquireHandle calls for the GUID find it loaded. Gated by the write toggle.
static std::map<uint64_t, uint64_t> g_injected;  // guid -> handle (reference held)
static nlohmann::json tool_inject_resource(const nlohmann::json& a) {
    if (!writes_allowed()) return err(writes_refused_message());
    uint64_t guid = parse_u64(a.value("guid", nlohmann::json(0)));
    if (!guid) return err("guid required");
    if (!a.contains("path") || !a["path"].is_string()) return err("path string required (Windows/Wine path of the file)");
    if (g_injected.count(guid)) return err("already injected (handle held); restart the client to replace it");
    std::string path = a["path"].get<std::string>();
    FILE* f = fopen(path.c_str(), "rb");
    if (!f) return err("cannot open " + path);
    fseek(f, 0, SEEK_END);
    long size = ftell(f);
    fseek(f, 0, SEEK_SET);
    if (size <= 0 || size > 0x7fff0000) { fclose(f); return err("bad file size"); }
    auto buf = (uint8_t*)ow_memalloc((int)size);
    if (!buf) { fclose(f); return err("allocation failed"); }
    size_t got = fread(buf, 1, size, f);
    fclose(f);
    if (got != (size_t)size) { ow_dealloc((__int64)buf); return err("short read"); }
#pragma pack(push, 1)
    struct Desc { uint64_t guid; void* buf; uint32_t size; uint8_t owns; uint8_t flag; uint8_t pad[2]; } d{guid, buf, (uint32_t)size, 1, 0, {}};
#pragma pack(pop)
    static_assert(sizeof(Desc) == 0x18);
    using create_fn = int (*)(uint64_t*, Desc*);
    auto create = (create_fn)(globals::gameBase + 0x9c5de0);
    uint64_t handle = 0;
    int rc = create(&handle, &d);  // on failure the engine frees the buffer (owns = 1)
    LOG_CORE(Warn, "[MCP] inject_resource {:016x} ({} bytes from {}): rc {} handle {:x}", guid, size, path, rc, handle);
    if (rc < 0 || !handle) return err(std::format("create from memory failed: rc {}", rc));
    g_injected[guid] = handle;
    return {{"guid", std::format("0x{:016x}", guid)}, {"size", size}, {"rc", rc}, {"handle", std::format("0x{:x}", handle)}};
}

// Execute a node in a loaded script. Useful for triggering Statescript actions.
static nlohmann::json tool_execute_node(const nlohmann::json& a) {
    uint64_t script_id = parse_u64(a.value("script_id", nlohmann::json(0)));
    int node_idx = parse_int(a.value("node_idx", nlohmann::json(-1)));
    std::string ea_name = a.value("ea", "lobby");
    uint32_t ent_id = (uint32_t)parse_u64(a.value("entity_id", nlohmann::json(0)));
    if (node_idx < 0) return err("node_idx required");
    EntityAdminBase* ea = (ea_name == "game") ? GameEntityAdmin() : LobbyEntityAdmin();
    if (!ea) return err("entity admin null");
    Component_23_Statescript* comp = nullptr;
    if (ent_id) { Entity* e = ea->getEntById(ent_id); comp = e ? e->getById<Component_23_Statescript>(0x23) : nullptr; }
    else comp = ea->getSingletonComponent<Component_23_Statescript>(0x23);
    if (!comp) return err(ent_id ? "entity not found or has no statescript component" : "Component_23_Statescript not found");
    auto inst = comp->ss_inner.getByResourceId((__int64)script_id);
    if (!inst) return err("script not loaded on this component");
    return seh_guard("execute_node", [&]() -> nlohmann::json {
        inst->ExecuteNode(node_idx);
        return { {"ok", true}, {"script_id", std::format("0x{:x}", script_id)}, {"node_idx", node_idx} };
    });
}

static nlohmann::json tool_start_stop_state(const nlohmann::json& a) {
    uint64_t script_id = parse_u64(a.value("script_id", nlohmann::json(0)));
    int node_idx = parse_int(a.value("node_idx", nlohmann::json(-1)));
    bool start = a.value("start", true);
    std::string ea_name = a.value("ea", "lobby");
    uint32_t ent_id = (uint32_t)parse_u64(a.value("entity_id", nlohmann::json(0)));
    if (node_idx < 0) return err("node_idx required");
    EntityAdminBase* ea = (ea_name == "game") ? GameEntityAdmin() : LobbyEntityAdmin();
    if (!ea) return err("entity admin null");
    Component_23_Statescript* comp = nullptr;
    if (ent_id) { Entity* e = ea->getEntById(ent_id); comp = e ? e->getById<Component_23_Statescript>(0x23) : nullptr; }
    else comp = ea->getSingletonComponent<Component_23_Statescript>(0x23);
    if (!comp) return err(ent_id ? "entity not found or has no statescript component" : "Component_23_Statescript not found");
    auto inst = comp->ss_inner.getByResourceId((__int64)script_id);
    if (!inst) return err("script not loaded on this component");
    return seh_guard("start_stop_state", [&]() -> nlohmann::json {
        inst->StartStopState(node_idx, start);
        return { {"ok", true}, {"script_id", std::format("0x{:x}", script_id)}, {"node_idx", node_idx}, {"start", start} };
    });
}

// execute_node / start_stop_state on a game EA entity, then (a couple of frames later,
// once the node has run) capture the entity and send it to our game server as an
// authoritative full update, so the client keeps the result instead of rolling it back.
// Without node_idx it only captures and sends.
static nlohmann::json tool_execute_node_server(const nlohmann::json& a) {
    uint32_t ent_id = (uint32_t)parse_u64(a.value("entity_id", nlohmann::json(0)));
    uint64_t script_id = parse_u64(a.value("script_id", nlohmann::json(0)));
    int node_idx = parse_int(a.value("node_idx", nlohmann::json(-1)));
    int delay = parse_int(a.value("delay_frames", nlohmann::json(2)));
    EntityAdminBase* ea = GameEntityAdmin();
    if (!ea) return err("game entity admin null");
    return seh_guard("execute_node_server", [&]() -> nlohmann::json {
        Entity* e = ea->getEntById(ent_id);
        auto comp = e ? e->getById<Component_23_Statescript>(0x23) : nullptr;
        if (!comp) return err("entity not found or has no statescript component");
        nlohmann::json j = { {"entity_id", std::format("0x{:x}", ent_id)}, {"delay_frames", delay} };
        if (node_idx >= 0) {
            auto inst = comp->ss_inner.getByResourceId((__int64)script_id);
            if (!inst) return err("script not loaded on this entity");
            if (a.contains("start")) inst->StartStopState(node_idx, a["start"].get<bool>());
            else inst->ExecuteNode(node_idx);
            j["script_id"] = std::format("0x{:x}", script_id);
            j["node_idx"] = node_idx;
        }
        ServerBridge::queue_capture(ent_id, delay);
        j["queued"] = true;
        j["note"] = "capture and send happen after delay_frames; check with ss_serialize_local (last_applied_cf) or server_bridge_status";
        return j;
    });
}

static nlohmann::json tool_server_bridge_status(const nlohmann::json&) {
    return { {"status", ServerBridge::last_status()}, {"echo", ServerBridge::echo_enabled()},
             {"echo_entity", std::format("0x{:x}", ServerBridge::echo_entity())} };
}

static nlohmann::json tool_server_echo(const nlohmann::json& a) {
    uint32_t ent_id = (uint32_t)parse_u64(a.value("entity_id", nlohmann::json(0)));
    bool enabled = a.value("enabled", true);
    if (enabled && !ent_id) return err("entity_id required");
    ServerBridge::set_echo(ent_id, enabled);
    return tool_server_bridge_status({});
}

// Instantiate ("play") a statescript graph by resource id on an entity admin.
// Calls the engine's Component_23_Statescript::loadScript (vtable load path). This is
// how the game spawns statescripts; lets us force-instantiate scripts that are loaded
// only as graph resources (e.g. server-side gamemode logic that never spawns offline).
static nlohmann::json tool_play_script(const nlohmann::json& a) {
    uint64_t script_id = parse_u64(a.value("script_id", nlohmann::json(0)));
    std::string ea_name = a.value("ea", "game");
    uint64_t exec_flags = parse_u64(a.value("exec_flags", nlohmann::json(0)));
    uint32_t ent_id = (uint32_t)parse_u64(a.value("entity_id", nlohmann::json(0)));
    if (!script_id) return err("script_id required");
    EntityAdminBase* ea = (ea_name == "game") ? GameEntityAdmin() : LobbyEntityAdmin();
    if (!ea) return err("entity admin null");
    auto comp = ea->getSingletonComponent<Component_23_Statescript>(0x23);
    if (!comp) return err("Component_23_Statescript not found");
    int before = comp->ss_inner.g1_instanceArr.num;
    return seh_guard("play_script", [&]() -> nlohmann::json {
        comp->loadScript((int64)script_id, exec_flags, ent_id);
        int after = comp->ss_inner.g1_instanceArr.num;
        // Did an instance for this script_id appear?
        int new_instance_id = 0; bool present = false;
        for (int i = 0; i < comp->ss_inner.g1_instanceArr.num; i++) {
            auto inst = comp->ss_inner.g1_instanceArr.ptr[i];
            if (inst && (uint64_t)inst->script_id == script_id) { present = true; new_instance_id = (int)inst->instance_id; }
        }
        LOG_CORE(Warn, "[MCP] play_script 0x{:x} on {} ent=0x{:x}: instances {}->{} present={}",
            script_id, ea_name, ent_id, before, after, present);
        return {
            {"ok", true},
            {"script_id", std::format("0x{:016x}", script_id)},
            {"ea", ea_name},
            {"entity_id", std::format("0x{:x}", ent_id)},
            {"exec_flags", std::format("0x{:x}", exec_flags)},
            {"instances_before", before},
            {"instances_after", after},
            {"instance_present", present},
            {"new_instance_id", new_instance_id},
        };
    });
}

// --- ECS: entities, components, systems -----------------------------------

// 'game' | 'lobby' | 'session' (the EA owned by the game session object, ticked first each frame)
// | an absolute EA address ("0x...").
static EntityAdminBase* ea_by_name(const std::string& n) {
    if (n == "game") return GameEntityAdmin();
    if (n == "session") {
        auto session = *(uintptr_t*)(globals::gameBase + 0x17b7fc0);
        return session ? *(EntityAdminBase**)(session + 0xb8) : nullptr;
    }
    if (n.size() > 2 && n[0] == '0' && (n[1] == 'x' || n[1] == 'X'))
        return (EntityAdminBase*)parse_u64(n);
    return LobbyEntityAdmin();
}

// The entity-definition resource id (what the entity *is*), via resload_entry.
static uint64_t entity_def_id(Entity* ent) {
    if (!ent) return 0;
    auto rle = ent->resload_entry;
    if (!rle || bad_read(rle, sizeof(MisalignedResourceLoadEntry))) return 0;
    if (!rle->valid()) return 0;
    auto a = rle->align();
    if (!a || bad_read(a, sizeof(MisalignedResourceLoadEntry))) return 0;
    return (uint64_t)a->resource_id;
}

// What Prometheus's player spawner does to a hero it spawns, for a hero our server
// spawned: load the STUWeaponComponent's manager and weapon scripts (weapon index
// vars 0x1c / 0x1d) and set owner var 0xFD. Local only; send the result with
// execute_node_server (no node_idx) to make it stick.
static nlohmann::json tool_hero_attach_weapons(const nlohmann::json& a) {
    uint32_t ent_id = (uint32_t)parse_u64(a.value("entity_id", nlohmann::json(0)));
    bool fixup = a.value("fixup", true);
    EntityAdminBase* ea = GameEntityAdmin();
    if (!ea) return err("game entity admin null");
    return seh_guard("hero_attach_weapons", [&]() -> nlohmann::json {
        Entity* ent = ea->getEntById(ent_id);
        if (!ent) return err("entity not found");
        auto comp = ent->getById<Component_23_Statescript>(0x23);
        if (!comp) return err("entity has no statescript component");
        uint64_t def = entity_def_id(ent);
        if (!def) return err("entity definition unknown");
        player_spawner::attach_weapons(ent, (__int64)def);
        if (fixup)
            player_spawner::fixup_player_statescript(ent);
        nlohmann::json insts = nlohmann::json::array();
        for (int i = 0; i < comp->ss_inner.g1_instanceArr.num; i++) {
            auto inst = comp->ss_inner.g1_instanceArr.ptr[i];
            if (inst) insts.push_back({ {"instance", inst->instance_id}, {"graph", std::format("0x{:016x}", inst->script_id)} });
        }
        return { {"entity_id", std::format("0x{:x}", ent_id)}, {"def", std::format("0x{:016x}", def)}, {"fixup", fixup}, {"instances", insts} };
    });
}

static nlohmann::json entity_brief(Entity* ent) {
    nlohmann::json e;
    e["entity_id"] = std::format("0x{:x}", ent->entity_id);
    e["global_id"] = std::format("0x{:x}", ent->entity_global_id);
    uint64_t def = entity_def_id(ent);
    if (def) e["def"] = std::format("0x{:016x}", def);
    nlohmann::json cids = nlohmann::json::array();
    int cn = ent->component_list.num - 1;
    if (cn > 0 && cn < 4096) {
        for (int c = 0; c < cn; c++) {
            auto cb = ent->component_list.ptr[c];
            if (cb && !bad_read(cb, sizeof(ComponentBase)))
                cids.push_back(std::format("0x{:x}", (int)cb->component_id));
        }
    }
    e["component_ids"] = cids;
    return e;
}

// Enumerate entities on an EA. Paginated.
static nlohmann::json tool_list_entities(const nlohmann::json& a) {
    std::string ea_name = a.value("ea", "game");
    int limit = parse_int(a.value("limit", nlohmann::json(100)));
    int offset = parse_int(a.value("offset", nlohmann::json(0)));
    int has_comp = parse_int(a.value("has_component", nlohmann::json(-1)));
    if (limit <= 0 || limit > 1000) limit = 100;
    EntityAdminBase* ea = ea_by_name(ea_name);
    if (!ea) return err("entity admin null");
    return seh_guard("list_entities", [&]() -> nlohmann::json {
        nlohmann::json arr = nlohmann::json::array();
        int total = ea->entity_count, matched = 0, emitted = 0;
        for (auto ent : *ea) {
            if (emitted >= limit) break;
            if (!ent || bad_read(ent, 0x98)) continue;
            if (has_comp >= 0 && !ent->hasComponent(has_comp)) continue; // filter, then page over matches
            if (matched++ < offset) continue;
            arr.push_back(entity_brief(ent));
            emitted++;
        }
        nlohmann::json j = { {"ea", ea_name}, {"entity_count", total}, {"offset", offset}, {"returned", emitted}, {"entities", arr} };
        if (has_comp >= 0) j["filter_component"] = std::format("0x{:x}", has_comp);
        return j;
    });
}

// Detail for one entity: components, ids.
static nlohmann::json tool_get_entity(const nlohmann::json& a) {
    uint32_t id = (uint32_t)parse_u64(a.value("entity_id", nlohmann::json(0)));
    std::string ea_name = a.value("ea", "game");
    EntityAdminBase* ea = ea_by_name(ea_name);
    if (!ea) return err("entity admin null");
    return seh_guard("get_entity", [&]() -> nlohmann::json {
        Entity* ent = ea->getEntById(id);
        if (!ent) return err("entity not found on this ea");
        auto j = entity_brief(ent);
        j["ea"] = ea_name;
        if (ent->parent && !bad_read(ent->parent, 0x98))
            j["parent_entity_id"] = std::format("0x{:x}", ent->parent->entity_id);
        return j;
    });
}

// Find a special entity. by = "local_player" (the controller entity) currently.
static nlohmann::json tool_find_entity(const nlohmann::json& a) {
    std::string by = a.value("by", "local_player");
    std::string ea_name = a.value("ea", "game");
    EntityAdminBase* ea = ea_by_name(ea_name);
    if (!ea) return err("entity admin null");
    return seh_guard("find_entity", [&]() -> nlohmann::json {
        Entity* ent = nullptr;
        if (by == "local_player" || by == "local" || by == "controller") ent = ea->getLocalEnt();
        else return err("unknown 'by' (supported: local_player)");
        if (!ent) return err("not found");
        auto j = entity_brief(ent);
        j["ea"] = ea_name; j["by"] = by;
        return j;
    });
}

// Raw hex of a component on an entity (decode via known Component_X structs offline).
static nlohmann::json tool_read_component(const nlohmann::json& a) {
    uint32_t id = (uint32_t)parse_u64(a.value("entity_id", nlohmann::json(0)));
    int comp_id = parse_int(a.value("component_id", nlohmann::json(-1)));
    int len = parse_int(a.value("length", nlohmann::json(256)));
    std::string ea_name = a.value("ea", "game");
    if (comp_id < 0) return err("component_id required");
    if (len <= 0 || len > 4096) len = 256;
    EntityAdminBase* ea = ea_by_name(ea_name);
    if (!ea) return err("entity admin null");
    return seh_guard("read_component", [&]() -> nlohmann::json {
        Entity* ent = ea->getEntById(id);
        if (!ent) return err("entity not found");
        auto cb = ent->getById(comp_id);
        if (!cb) return err("component not on entity");
        if (bad_read(cb, len)) len = 256;
        std::vector<uint8_t> buf;
        std::string why;
        if (!read_bytes((uintptr_t)cb, len, buf, why)) return err("component unreadable: " + why);
        return {
            {"entity_id", std::format("0x{:x}", id)},
            {"component_id", std::format("0x{:x}", comp_id)},
            {"addr", std::format("0x{:x}", (uintptr_t)cb)},
            {"length", len},
            {"hex", hex_dump(buf.data(), len)},
        };
    });
}

// List the EA's registered systems (addresses; decode via Ghidra/the systems window).
static nlohmann::json tool_list_systems(const nlohmann::json& a) {
    std::string ea_name = a.value("ea", "game");
    EntityAdminBase* ea = ea_by_name(ea_name);
    if (!ea) return err("entity admin null");
    return seh_guard("list_systems", [&]() -> nlohmann::json {
        nlohmann::json arr = nlohmann::json::array();
        int n = ea->systems_array.num;
        if (n < 0 || n > 4096) n = 0;
        for (int i = 0; i < n; i++) {
            auto s = ea->systems_array.ptr[i];
            arr.push_back({ {"index", i}, {"instance", std::format("0x{:x}", (uintptr_t)s)},
                            {"vtable", fmt_code_ptr(s ? (uintptr_t)*s : 0)} });
        }
        return { {"ea", ea_name}, {"system_count", n}, {"systems", arr} };
    });
}

// Full ECS snapshot of one EA (see docs/ECS.md): header counters, systems in tick order with
// subscriptions and which callback slots they override, the per-component subscriber table
// (EA+0xB8), and per-component-type data (creator EA+0x38, dependencies from creation info,
// live instance count from the iteration list EA+0xB0).
static nlohmann::json tool_ecs_dump(const nlohmann::json& a) {
    std::string ea_name = a.value("ea", "game");
    EntityAdminBase* ea = ea_by_name(ea_name);
    if (!ea) return err("entity admin null");
    return seh_guard("ecs_dump", [&]() -> nlohmann::json {
        auto base = (uintptr_t)ea;
        auto rd32 = [&](size_t off) { return *(int*)(base + off); };
        auto rd64 = [&](size_t off) { return *(uintptr_t*)(base + off); };

        nlohmann::json j;
        j["ea"] = ea_name;
        j["addr"] = std::format("0x{:x}", base);
        j["vtable"] = fmt_code_ptr(rd64(0));
        j["entity_count"] = rd32(0xc);
        j["tick_count"] = rd64(0xc8);
        j["next_entity_counter"] = rd32(0xc0);
        j["initialized"] = *(uint8_t*)(base + 0xd0) != 0;
        j["pending_delete_requests"] = rd32(0x50);
        j["pending_destroy"] = rd32(0x78);
        j["pending_created"] = rd32(0x98);

        // Systems, in systems_array (= tick) order.
        // Slots: 5 OnInitialize, 7 PreDelete, 8 GetSubscriptions, 9 OnEntityAdded (at flush),
        // 10 OnEntityCreated, 11 OnEntityDeleting, 12 OnEntityRemoved, 13 OnTick.
        static const std::pair<int, const char*> kSlots[] = {
            {5, "on_initialize"}, {7, "pre_delete"}, {9, "on_added"}, {10, "on_created"},
            {11, "on_deleting"}, {12, "on_removed"}, {13, "on_tick"} };
        int n = ea->systems_array.num;
        if (n < 0 || n > 4096) n = 0;
        nlohmann::json systems = nlohmann::json::array();
        std::map<uintptr_t, int> sys_index;
        for (int i = 0; i < n; i++) {
            auto s = (uintptr_t*)ea->systems_array.ptr[i];
            sys_index[(uintptr_t)s] = i;
            systems.push_back(seh_guard("ecs_dump system", [&]() -> nlohmann::json {
                nlohmann::json e;
                e["index"] = i;
                e["instance"] = std::format("0x{:x}", (uintptr_t)s);
                if (!s || bad_read(s, 16)) return e;
                auto vt = ecs_trace::original_vtable(s);
                e["vtable"] = fmt_code_ptr((uintptr_t)vt);
                nlohmann::json slots = nlohmann::json::object();
                for (auto& [idx, name] : kSlots) slots[name] = fmt_code_ptr(vt[idx]);
                e["slots"] = slots;
                int count = 0;
                auto subs = ((uint8_t*(*)(void*, int*))vt[8])(s, &count);
                nlohmann::json sj = nlohmann::json::array();
                for (int k = 0; subs && k < count && k < 256; k++) sj.push_back(std::format("0x{:02x}", subs[k]));
                e["subscriptions"] = sj;
                return e;
            }));
        }
        j["systems"] = systems;

        // Component types. creation_info+0x20 = number of component ids (valid ids 0..count-1).
        auto ci = rd64(0x60);
        int id_count = ci ? *(uint8_t*)(ci + 0x20) : 0;
        j["component_id_count"] = id_count;
        auto creators = (uintptr_t*)rd64(0x38);
        auto deps = ci ? *(uintptr_t*)(ci + 0x18) : 0;
        auto iter = rd64(0xb0);
        auto subtab = rd64(0xb8);
        nlohmann::json comps = nlohmann::json::array();
        for (int id = 0; id < id_count; id++) {
          auto c = seh_guard("ecs_dump component", [&]() -> nlohmann::json {
            nlohmann::json c;
            c["id"] = std::format("0x{:02x}", id);
            uintptr_t creator = creators ? creators[id] : 0;
            c["creator_vtable"] = creator ? fmt_code_ptr(*(uintptr_t*)creator) : "";
            if (deps) {
                auto dl = (teList<uint8_t>*)(deps + id * 0x10);
                nlohmann::json dj = nlohmann::json::array();
                for (int k = 0; k < dl->num && k < 256; k++) dj.push_back(std::format("0x{:02x}", dl->ptr[k]));
                c["dependencies"] = dj;
            }
            if (iter) {
                auto il = (teList<uintptr_t>*)(iter + id * 0x18);
                c["live_count"] = il->num;
                // Iteration order (sorted by component address): owning entity ids.
                nlohmann::json order = nlohmann::json::array();
                for (int k = 0; k < il->num && k < 100000; k++)
                    order.push_back(*(uint32_t*)(*(uintptr_t*)(il->ptr[k] + 8) + 0x94));
                c["order"] = order;
            }
            // Global per-type table (RVA 0x1828ec0, stride 0x20): +0 custom handler, +8 != 0 means the
            // type has its own storage and is kept out of the EA iteration lists.
            auto storage = globals::gameBase + 0x1828ec0 + id * 0x20;
            if (*(int*)(storage + 8)) {
                c["untracked"] = *(int*)(storage + 8);
                c["storage_handler"] = fmt_code_ptr(*(uintptr_t*)storage);
            }
            if (subtab) {
                auto sl = (teList<uintptr_t>*)(subtab + id * 0x18);
                nlohmann::json subj = nlohmann::json::array();
                for (int k = 0; k < sl->num && k < 4096; k++) {
                    auto it = sys_index.find(sl->ptr[k]);
                    subj.push_back(it != sys_index.end() ? nlohmann::json(it->second)
                                                         : nlohmann::json(std::format("0x{:x}", sl->ptr[k])));
                }
                c["subscribers"] = subj;
            }
            return c;
          });
          if (c.contains("error")) { c["id"] = std::format("0x{:02x}", id); comps.push_back(c); continue; }
          if (c["creator_vtable"] == "" && c.value("live_count", 0) == 0 && c["subscribers"].empty()) continue;
          comps.push_back(c);
        }
        j["components"] = comps;
        return j;
    });
}

// Which statescript graph(s) are running on a specific entity (its own 0x23 component).
// Maps an entity to its logic -> identify scripted objects (payload, objective, pickup...).
static nlohmann::json tool_entity_statescripts(const nlohmann::json& a) {
    uint32_t id = (uint32_t)parse_u64(a.value("entity_id", nlohmann::json(0)));
    std::string ea_name = a.value("ea", "game");
    EntityAdminBase* ea = ea_by_name(ea_name);
    if (!ea) return err("entity admin null");
    return seh_guard("entity_statescripts", [&]() -> nlohmann::json {
        Entity* ent = ea->getEntById(id);
        if (!ent) return err("entity not found");
        auto comp = ent->getById<Component_23_Statescript>(0x23);
        if (!comp) return err("entity has no statescript component (0x23)");
        nlohmann::json arr = nlohmann::json::array();
        int n = comp->ss_inner.g1_instanceArr.num;
        if (n < 0 || n > 100000) n = 0;
        for (int i = 0; i < n; i++) {
            auto inst = comp->ss_inner.g1_instanceArr.ptr[i];
            if (!inst || bad_read(inst, sizeof(void*))) continue;
            nlohmann::json e;
            e["script_id"] = std::format("0x{:016x}", inst->script_id);
            e["instance_id"] = (int)inst->instance_id;
            if (inst->graph && !bad_read(inst->graph, sizeof(void*)))
                e["nodes"] = inst->graph->m_nodes.count();
            arr.push_back(e);
        }
        return { {"entity_id", std::format("0x{:x}", id)}, {"ea", ea_name}, {"script_count", n}, {"scripts", arr} };
    });
}

// --- STU resources --------------------------------------------------------

// List loaded STU resources, optionally filtered to a type (assignable-to hash).
static nlohmann::json tool_list_stu_resources(const nlohmann::json& a) {
    uint32_t type_hash = (uint32_t)parse_u64(a.value("type_hash", nlohmann::json(0)));
    int limit = parse_int(a.value("limit", nlohmann::json(200)));
    int offset = parse_int(a.value("offset", nlohmann::json(0)));
    if (limit <= 0 || limit > 2000) limit = 200;
    return seh_guard("list_stu_resources", [&]() -> nlohmann::json {
        auto all = type_hash ? stu_resources::GetIfAssignableTo(type_hash) : stu_resources::GetAll();
        nlohmann::json arr = nlohmann::json::array();
        int i = 0, emitted = 0;
        for (auto& [rid, base] : all) {
            if (emitted >= limit) break;
            if (i++ < offset) continue;
            nlohmann::json e;
            e["resource_id"] = std::format("0x{:016x}", (uint64_t)rid);
            if (base && base->valid()) {
                auto info = base->vfptr_stubase->GetSTUInfo();
                if (info) {
                    e["type_hash"] = std::format("0x{:08x}", info->Hash);
                    if (info->Name_str && !bad_read((void*)info->Name_str, 1)) e["type"] = info->Name_str;
                    else { auto hit = stringhash_library::hashes.find((int)info->Hash); if (hit != stringhash_library::hashes.end()) e["type"] = hit->second; }
                }
            }
            arr.push_back(e); emitted++;
        }
        return { {"total", (int)all.size()}, {"type_hash", std::format("0x{:08x}", type_hash)}, {"offset", offset}, {"returned", emitted}, {"resources", arr} };
    });
}

// Dump an STU object's top-level fields (name/hash/offset + raw value) via RangeArgs.
// STU field kinds (constraint vfunc +0x30 -> kind << 32 | element hash; docs/STU.md §1).
static uint32_t stu_field_kind(STUArgumentInfo* arg, uint32_t* elem_hash = nullptr) {
    auto c = (uintptr_t*)arg->Constraint;
    if (!c || bad_read(c, 8)) return 0xffffffff;
    auto vt = (uintptr_t*)*c;
    uint64_t r = ((uint64_t(*)(void*))vt[6])(c);
    if (elem_hash) *elem_hash = (uint32_t)r;
    return (uint32_t)(r >> 32);
}

static uint32_t guid_type(uint64_t g) {
    uint32_t v = (uint32_t)((g >> 48) & 0xfff), r = 0;
    for (int i = 0; i < 12; i++) r |= ((v >> i) & 1) << (11 - i);
    return r + 1;
}

static nlohmann::json stu_ref_json(char* at) {
    uint64_t handle = *(uint64_t*)at, guid = *(uint64_t*)(at + 8);
    nlohmann::json r;
    r["guid"] = std::format("0x{:016x}", guid);
    r["type"] = std::format("0x{:03x}", guid_type(guid));
    r["handle"] = handle == ~0ull ? "none" : std::format("0x{:x}", handle);
    r["loaded"] = guid && GetLoadedResource<char>((__int64)guid) != nullptr;
    return r;
}

// A loaded STU list {ptr, mode}: mode 1 = inline {data, pad, count}, else {count, pad, data}.
static char* stu_list(char* at, int& n) {
    char* p = *(char**)at;
    int64_t mode = *(int64_t*)(at + 8);
    n = 0;
    if (!p || bad_read(p, 16)) return nullptr;
    if (mode == 1) { n = ((int*)p)[2]; return *(char**)p; }
    n = *(int*)p;
    return ((char**)p)[1];
}

static nlohmann::json stu_fields_json(STUInfo* info, char* obj, int depth, int max_list);

static nlohmann::json stu_object_json(char* obj, int depth, int max_list) {
    nlohmann::json o;
    o["addr"] = std::format("0x{:x}", (uintptr_t)obj);
    if (!obj || bad_read(obj, 8)) return o;
    auto base = (STUBase<>*)obj;
    auto info = base->vfptr_stubase ? base->vfptr_stubase->GetSTUInfo() : nullptr;
    if (!info) return o;
    o["type_hash"] = std::format("0x{:08x}", info->Hash);
    if (depth > 0) o["fields"] = stu_fields_json(info, obj, depth - 1, max_list);
    return o;
}

static nlohmann::json stu_fields_json(STUInfo* info, char* obj, int depth, int max_list) {
    nlohmann::json fields = nlohmann::json::array();
    for (auto [owner, arg] : info->RangeArgs()) {
        if (!arg) continue;
        nlohmann::json f;
        if (arg->Name_str && !bad_read((void*)arg->Name_str, 1) && *arg->Name_str) f["name"] = arg->Name_str;
        else { auto hit = stringhash_library::hashes.find((int)arg->Hash); if (hit != stringhash_library::hashes.end()) f["name"] = hit->second; }
        f["hash"] = std::format("0x{:08x}", arg->Hash);
        f["offset"] = std::format("0x{:x}", arg->Offset);
        if (owner) f["declared_by"] = std::format("0x{:08x}", owner->Hash);
        uint32_t elem = 0;
        uint32_t kind = stu_field_kind(arg, &elem);
        f["kind"] = kind;
        char* at = obj + arg->Offset;
        if (bad_read(at, 16)) { fields.push_back(f); continue; }
        auto elem_info = [&]() -> STUInfo* {
            auto c = (char*)arg->Constraint;
            auto ei = c ? *(STUInfo**)(c + 0x28) : nullptr;
            return (ei && !bad_read(ei, 0x60)) ? ei : nullptr;
        };
        switch (kind) {
        case 10: case 12: // file / resource reference {handle, guid}
            f["ref"] = stu_ref_json(at);
            break;
        case 11: case 13: { // list of references
            int n; char* d = stu_list(at, n);
            f["count"] = n;
            nlohmann::json refs = nlohmann::json::array();
            for (int i = 0; d && i < n && i < max_list; i++) refs.push_back(stu_ref_json(d + i * 16));
            f["refs"] = refs;
            break;
        }
        case 2: { // object pointer
            char* p = *(char**)at;
            f["object"] = p ? stu_object_json(p, depth, max_list) : nlohmann::json(nullptr);
            break;
        }
        case 4: { // inline object
            auto ei = elem_info();
            f["elem_type"] = std::format("0x{:08x}", elem);
            if (ei && depth > 0) f["inline"] = stu_fields_json(ei, at, depth - 1, max_list);
            break;
        }
        case 3: case 5: { // list of objects / inline objects
            int n; char* d = stu_list(at, n);
            f["count"] = n;
            f["elem_type"] = std::format("0x{:08x}", elem);
            if (depth > 0 && d) {
                nlohmann::json items = nlohmann::json::array();
                auto ei = elem_info();
                uint32_t stride = ei ? *(uint32_t*)((char*)ei + 0x5c) : 0;
                for (int i = 0; i < n && i < max_list; i++) {
                    if (kind == 3) items.push_back(stu_object_json(((char**)d)[i], depth - 1, max_list));
                    else if (ei && stride) items.push_back(stu_fields_json(ei, d + (size_t)i * stride, depth - 1, max_list));
                }
                f["items"] = items;
            }
            break;
        }
        case 1: case 9: case 7: { // list of primitives / enums, map: count only
            int n; stu_list(at, n);
            f["count"] = n;
            f["raw_u64"] = std::format("0x{:x}", *(uint64_t*)at);
            break;
        }
        default:
            f["raw_u64"] = std::format("0x{:x}", *(uint64_t*)at);
        }
        fields.push_back(f);
    }
    return fields;
}

static nlohmann::json tool_read_stu_object(const nlohmann::json& a) {
    uint64_t rid = parse_u64(a.value("resource_id", nlohmann::json(0)));
    int depth = parse_int(a.value("depth", nlohmann::json(1)));
    int max_list = parse_int(a.value("max_list", nlohmann::json(32)));
    if (!rid) return err("resource_id required");
    return seh_guard("read_stu_object", [&]() -> nlohmann::json {
        auto base = stu_resources::GetByID((__int64)rid);
        if (!base || !base->valid()) base = (STUBase<>*)GetLoadedResource<char>((__int64)rid);
        if (!base) return err("resource not loaded");
        auto info = base->vfptr_stubase->GetSTUInfo();
        if (!info) return err("no STU info");
        nlohmann::json j;
        j["resource_id"] = std::format("0x{:016x}", rid);
        j["addr"] = std::format("0x{:x}", (uintptr_t)base);
        j["type_hash"] = std::format("0x{:08x}", info->Hash);
        if (info->Name_str && !bad_read((void*)info->Name_str, 1) && *info->Name_str) j["type"] = info->Name_str;
        j["fields"] = stu_fields_json(info, (char*)base, depth, max_list);
        return j;
    });
}

// Sync layout of a Statescript graph, as the client's full-update decoder walks it
// (ced430 -> cedf30 instance vars, cedbe0 states): eligible sync vars
// (m_syncVars @+0x130, 0x18-byte entries: var ptr +8 != 0 and +0x10 == 0) and
// eligible remote sync nodes (m_remoteSyncNodes @+0x120: a state node, i.e.
// vfunc +0x10(node, game+0x1766e40), neither client-only +0x75 nor server-only +0x76).
// STU arrays here have an inline form when the count field (+8) is 1.
static nlohmann::json graph_sync_json(uint64_t gid) {
    nlohmann::json g;
    g["graph"] = std::format("0x{:016x}", gid);
    g["index"] = (uint32_t)(gid & 0xffffffff);
    char* graph = GetLoadedResource<char>((__int64)gid); // the object (game+0x9c6470)
    if (!graph) { g["error"] = "not loaded"; return g; }
    auto arr = [&](int off, int stride, int& n) -> char* {
        char* p = *(char**)(graph + off);
        __int64 mode = *(__int64*)(graph + off + 8);
        if (!p) { n = 0; return nullptr; }
        if (mode == 1) { n = ((int*)p)[2]; return *(char**)p; }
        n = *(int*)p;
        return ((char**)p)[1];
    };
    auto type_of = [](char* obj, nlohmann::json& out) {
        auto info = ((STUBase<STUBase_vt>*)obj)->vfptr->GetSTUInfo();
        if (!info) return;
        out["type_hash"] = std::format("0x{:08x}", info->Hash);
        // Name_str is often an empty string; then try the hash library (unresolved: no "type").
        if (info->Name_str && !bad_read((void*)info->Name_str, 1) && info->Name_str[0]) out["type"] = info->Name_str;
        else { auto hit = stringhash_library::hashes.find((int)info->Hash); if (hit != stringhash_library::hashes.end()) out["type"] = hit->second; }
    };
    // m_syncVars entries: +8 var id (0x0D8 GUID), +0x10 enum (0 = sent in the sync-var section),
    // +0x14 u8 (1 on owner vars, inferred from usage). "slot" = position among the eligible ones.
    int nvars = 0, eligibleVars = 0;
    char* vars = arr(0x130, 0x18, nvars);
    nlohmann::json varList = nlohmann::json::array();
    for (int i = 0; vars && i < nvars; i++) {
        char* e = vars + i * 0x18;
        uint64_t vid = *(uint64_t*)(e + 8);
        int kind = *(int*)(e + 0x10);
        bool eligible = vid != 0 && kind == 0;
        nlohmann::json v = { {"entry", i}, {"var", std::format("0x{:04x}", (uint32_t)(vid & 0xffff))},
            {"var_guid", std::format("0x{:016x}", vid)}, {"enum", kind}, {"owner_var", *(uint8_t*)(e + 0x14)},
            {"eligible", eligible} };
        if (eligible) v["slot"] = eligibleVars++;
        varList.push_back(v);
    }
    // m_remoteSyncNodes: node +0x68 index in m_nodes, +0x6C index in m_remoteSyncNodes, state +0xF8
    // index in m_states. "update_index" = position among the eligible ones, the index a remote full
    // update's state section (and /scripts "active") uses.
    int nnodes = 0, eligibleNodes = 0;
    char* nodes = arr(0x120, 8, nnodes);
    auto isa = (char(__fastcall*)(void*, void*))nullptr;
    nlohmann::json nodeList = nlohmann::json::array();
    for (int i = 0; nodes && i < nnodes; i++) {
        char* node = ((char**)nodes)[i];
        if (!node) continue;
        void** vt = *(void***)node;
        isa = (char(__fastcall*)(void*, void*))vt[2];
        bool isState = isa(node, (void*)(globals::gameBase + 0x1766e40)) != 0;
        bool clientOnly = node[0x75] != 0, serverOnly = node[0x76] != 0;
        bool eligible = isState && !clientOnly && !serverOnly;
        nlohmann::json n = { {"entry", i}, {"node", *(int32_t*)(node + 0x68)}, {"remote_index", *(int32_t*)(node + 0x6C)},
            {"is_state", isState}, {"client_only", clientOnly}, {"server_only", serverOnly}, {"eligible", eligible} };
        if (isState) n["state"] = *(int32_t*)(node + 0xF8);
        type_of(node, n);
        if (eligible) n["update_index"] = eligibleNodes++;
        nodeList.push_back(n);
    }
    g["sync_vars"] = nvars;
    g["eligible_sync_vars"] = eligibleVars;
    g["remote_sync_nodes"] = nnodes;
    g["eligible_remote_nodes"] = eligibleNodes;
    g["sync_var_list"] = varList;
    g["remote_node_list"] = nodeList;
    return g;
}

// The Statescript graphs an entity definition references (its component map:
// STUStatescriptComponent graphs, STUWeaponComponent manager + weapon scripts),
// each with its sync layout (graph_sync_json).
static nlohmann::json tool_graph_sync_info(const nlohmann::json& a) {
    uint64_t rid = parse_u64(a.value("resource_id", nlohmann::json(0)));
    if (!rid) return err("resource_id required (entity definition 0x040... or graph 0x058...)");
    return seh_guard("graph_sync_info", [&]() -> nlohmann::json {
        if ((rid >> 52) == 0x058) return graph_sync_json(rid);
        auto base = stu_resources::GetByID((__int64)rid);
        if (!base || !base->valid()) return err("resource not loaded");
        auto def = base->to_editable();
        auto cmap = def.get_argument_map("m_componentMap");
        nlohmann::json comps = nlohmann::json::array();
        auto graphs_in = [&](char* obj, nlohmann::json& out) {
            // any 16-byte STU reference whose GUID (+8) is a graph (type 0x058)
            auto info = ((STUBase<STUBase_vt>*)obj)->vfptr->GetSTUInfo();
            if (!info) return;
            for (auto [owner, arg] : info->RangeArgs()) {
                if (!arg) continue;
                char* at = obj + arg->Offset;
                if (bad_read(at, 16)) continue;
                uint64_t guid = *(uint64_t*)(at + 8);
                if ((guid >> 52) == 0x058) {
                    auto g = graph_sync_json(guid);
                    g["field"] = std::format("0x{:08x}", arg->Hash);
                    out.push_back(g);
                }
            }
        };
        for (auto [key, obj] : cmap) {
            nlohmann::json c;
            c["component"] = std::format("0x{:08x}", (uint32_t)key);
            nlohmann::json graphs = nlohmann::json::array();
            char* raw = (char*)obj.value;
            if (raw) graphs_in(raw, graphs);
            if ((uint32_t)key == stringHash("STUWeaponComponent")) {
                auto w = obj;
                for (auto weapon : w.get_argument_objectlist("m_weapons")) {
                    auto sc = weapon.get_argument_resource("m_script");
                    if (sc && sc->has_resource()) {
                        auto g = graph_sync_json(sc->resource_id);
                        g["field"] = "m_weapons[].m_script";
                        graphs.push_back(g);
                    }
                }
            }
            c["graphs"] = graphs;
            comps.push_back(c);
        }
        nlohmann::json j;
        j["resource_id"] = std::format("0x{:016x}", rid);
        j["components"] = comps;
        return j;
    });
}

// --- Statescript local full update, written with the client's own writers ---
//
// A bit stream laid out like the game's (writers game+0xe492e0 / +0xe49350): vtable,
// +8 buffer, +0x10 capacity, +0x14 whole bytes written, +0x20 bit position in the
// current byte, +0x21 current byte. vtable +0x20 grows the buffer; ours has a fixed one.
struct SsBitStream {
    void** vfptr;
    uint8_t* buf;
    uint32_t cap;
    uint32_t len;
    uint32_t x18;
    uint32_t align;
    uint8_t bit;
    uint8_t cur;
    uint8_t pad[6];
};

static bool __fastcall ss_stream_grow(SsBitStream* s, uint32_t need, uint8_t** /*buf*/, uint32_t* /*cap*/) {
    return need <= s->cap;
}
static void __fastcall ss_stream_noop() {}
static void* g_ss_stream_vt[8] = {
    (void*)ss_stream_noop, (void*)ss_stream_noop, (void*)ss_stream_noop, (void*)ss_stream_noop,
    (void*)ss_stream_grow, (void*)ss_stream_noop, (void*)ss_stream_noop, (void*)ss_stream_noop,
};

struct SsWriter {
    std::vector<uint8_t> store = std::vector<uint8_t>(64 * 1024);
    SsBitStream s{};
    SsWriter() { s.vfptr = g_ss_stream_vt; s.buf = store.data(); s.cap = (uint32_t)store.size(); s.x18 = 0xffffffff; s.align = 1; }
    void bits(uint32_t v, uint32_t n) {
        ((void(__fastcall*)(SsBitStream*, uint32_t, uint32_t))(globals::gameBase + 0xe49350))(&s, v, n);
    }
    void bit(bool b) {
        ((void(__fastcall*)(SsBitStream*, char))(globals::gameBase + 0xe492e0))(&s, b ? 1 : 0);
    }
    // 0 + w0 bits | 1 0 + w1 bits | 1 1 + w2 bits (FUN_7ff651050590 / 7e0 / a30)
    void prefix3(uint32_t v, uint32_t w0, uint32_t w1, uint32_t w2) {
        if (v < (1ull << w0)) { bit(0); bits(v, w0); }
        else if (v < (1ull << w1)) { bit(1); bit(0); bits(v, w1); }
        else { bit(1); bit(1); bits(v, w2); }
    }
    void count(uint32_t v) { prefix3(v, 8, 16, 32); } // 7e0
    void small(uint32_t v) { prefix3(v, 4, 8, 16); }  // a30
    uint64_t nbits() const { return (uint64_t)s.len * 8 + s.bit; }
    void append(const SsWriter& o) {
        uint64_t n = o.nbits();
        for (uint64_t i = 0; i < n; i++) {
            uint8_t byte = i / 8 < o.s.len ? o.store[i / 8] : o.s.cur;
            bit((byte >> (i % 8)) & 1);
        }
    }
    std::string hex() const {
        std::vector<uint8_t> out(store.begin(), store.begin() + s.len);
        if (s.bit) out.push_back(s.cur);
        std::ostringstream os;
        os << std::hex << std::setfill('0');
        for (auto b : out) os << std::setw(2) << (int)b;
        return os.str();
    }
};

// STU array at graph+off: {ptr, mode}; mode 1 = inline {elems, ?, count}, else {count, pad, elems}.
static char** ss_graph_list(char* graph, int off, int& n) {
    char* p = *(char**)(graph + off);
    __int64 mode = *(__int64*)(graph + off + 8);
    if (!p) { n = 0; return nullptr; }
    if (mode == 1) { n = ((int*)p)[2]; return *(char***)p; }
    n = *(int*)p;
    return ((char***)p)[1];
}

// The state data FUN_7ff650f289e0 reads, written in reverse: the state's mirror
// objects (vfunc +0x208) field by field (field type vfunc +0x98; flag 8 fields are
// skipped, local), then the state's own writer (vfunc +0x1F0).
static void ss_write_state_data(SsWriter& w, char* impl, bool full) {
    void** vt = *(void***)impl;
    struct { void** ptr; int count; int cap; void* inline_buf[16]; } list{};
    list.ptr = list.inline_buf; list.cap = 16;
    ((void(__fastcall*)(char*, void*))vt[0x208 / 8])(impl, &list);
    for (int m = 0; m < list.count && m < 16; m++) {
        char* mo = (char*)list.ptr[m];
        void** mvt = *(void***)mo;
        char* ti = ((char* (__fastcall*)(char*))mvt[0x28 / 8])(mo);
        uint32_t nf = *(uint32_t*)(ti + 0x54);
        char* fields = *(char**)(ti + 0x28);
        for (uint32_t f = 0; fields && f < nf; f++) {
            char* e = fields + 0xa0 * f;
            uint32_t flags = *(uint32_t*)(e + 0x18);
            if (flags & 8) continue;
            if ((flags & 0x20) && !full) continue;
            char* ft = *(char**)(e + 0x10);
            void** fvt = *(void***)ft;
            ((void(__fastcall*)(char*, SsBitStream*, __int64, char*, uint32_t))fvt[0x98 / 8])(
                ft, &w.s, 0, mo + *(uint32_t*)(e + 0xc), *(uint32_t*)(e + 0x1c));
        }
    }
    ((void(__fastcall*)(char*, SsBitStream*, char, char))vt[0x1F0 / 8])(impl, &w.s, 1, full ? 1 : 0);
}

// A var bag's plain (primitive) variables by 16-bit ID (GUID 0x0D8...|id at +0x98,
// value at +0xA0). Nested bags, dictionaries and instance references are skipped.
static std::map<uint16_t, StatescriptPrimitive> ss_bag_values(StatescriptVar_VarBag* vb) {
    std::map<uint16_t, StatescriptPrimitive> out;
    if (!vb) return out;
    for (int i = 0; i < 16; i++) {
        auto& items = vb->sub_items[i];
        for (int j = 0; items.arr_ptr && j < items.item_count && j < 4096; j++) {
            StatescriptVar_Base* v = items.arr_ptr[j].ptr;
            if (v && v->is_cvd())
                out[(uint16_t)(v->var_id & 0xffff)] = ((StatescriptVar_Primitive*)v)->global_var;
        }
    }
    return out;
}

// Whether ss_write_value can encode this primitive.
static bool ss_value_supported(const StatescriptPrimitive& p) {
    return p.type == StatescriptPrimitive_INT || p.type == StatescriptPrimitive_BYTE || p.type == StatescriptPrimitive_FLT;
}

// A variable value as FUN_7ff650cef290 reads it: no state reference (cf1520 bit 0),
// a u4 tag, the value (cef780). Integers: 1 u4, 2 s8, 3 s16, 4 s32 (all set as INT
// by the client, so BYTE vars come back as INT); floats: 8 f32.
static void ss_write_value(SsWriter& w, const StatescriptPrimitive& p) {
    w.bit(0);
    if (p.type == StatescriptPrimitive_FLT) {
        w.bits(8, 4);
        w.bits((uint32_t)p.value, 32);
        return;
    }
    int32_t v = (int32_t)p.value;
    if (v >= 0 && v < 16) { w.bits(1, 4); w.bits((uint32_t)v, 4); }
    else if (v >= -128 && v < 128) { w.bits(2, 4); w.bits((uint32_t)v & 0xff, 8); }
    else if (v >= -32768 && v < 32768) { w.bits(3, 4); w.bits((uint32_t)v & 0xffff, 16); }
    else { w.bits(4, 4); w.bits((uint32_t)v, 32); }
}

static nlohmann::json ss_value_json(uint16_t id, const StatescriptPrimitive& p) {
    StatescriptPrimitive copy = p;
    return { {"id", std::format("{:04x}", id)}, {"type", copy.get_type_str()}, {"value", copy.get_value_str()}, {"sent", ss_value_supported(p)} };
}

// Build the payload of a LOCAL full update (what FUN_7ff650ced430 reads) from an
// entity's live Statescript state: the server's side of the sync, produced with
// the client's writers. A full update carries the owner vars and the instance vars
// (INT/BYTE/FLT values; other types are listed in the report but not sent); a
// delta leaves variables alone. Nodes act on the next Statescript tick, so capture
// a frame or two after changing something (execute_node_server does that).
// What the last "store"/"diff" capture of an entity sent, so a "diff" delta carries
// only what changed since (echo mode): owner vars, and per instance its vars and the
// activation + data of each m_states entry.
struct SsInstBaseline {
    std::map<uint16_t, StatescriptPrimitive> vars;
    std::map<int, std::string> states; // m_states index -> data hex, active ones only
};
struct SsBaseline {
    std::map<uint16_t, StatescriptPrimitive> owner;
    std::map<int, SsInstBaseline> insts;
};
static std::map<uint32_t, SsBaseline> g_ss_baselines;

static bool ss_same(const StatescriptPrimitive& x, const StatescriptPrimitive& y) {
    return x.type == y.type && x.value == y.value && x.field_8 == y.field_8;
}

static nlohmann::json tool_ss_serialize_local(const nlohmann::json& a) {
    uint32_t id = (uint32_t)parse_u64(a.value("entity_id", nlohmann::json(0)));
    uint32_t end_cf = (uint32_t)parse_u64(a.value("end_cf", nlohmann::json(0)));
    bool delta = a.value("delta", false);
    // "store": remember what this capture sends; "diff" (delta only): send only what
    // changed since the stored capture, then store this one.
    std::string baseline_mode = a.value("baseline", "none");
    bool diff = delta && baseline_mode == "diff";
    bool store = baseline_mode == "store" || diff;
    std::string ea_name = a.value("ea", "game");
    EntityAdminBase* ea = ea_by_name(ea_name);
    if (!ea) return err("entity admin null");
    return seh_guard("ss_serialize_local", [&]() -> nlohmann::json {
        Entity* ent = ea->getEntById(id);
        if (!ent) return err("entity not found");
        auto comp = ent->getById<Component_23_Statescript>(0x23);
        if (!comp) return err("entity has no statescript component (0x23)");
        char* owner = (char*)comp + 0xC0; // the sync owner (ss_inner)
        // The client's current command frame (sync owner +0x2C4; +0x2D0 holds it x tick).
        uint32_t client_cf = *(uint32_t*)((char*)comp + 0x384);
        if (end_cf == 0)
            end_cf = client_cf;
        SsBaseline& prior = g_ss_baselines[id];
        SsBaseline next;
        int64_t tick = *(int64_t*)(owner + 0x360);
        int64_t base = (int64_t)end_cf * tick; // the events reader's time base (sync +0x980 = end CF)
        auto getImpl = (char* (__fastcall*)(void*, uint32_t))(globals::gameBase + 0xf4c820);
        auto hasUpdate = (char(__fastcall*)(char*))(globals::gameBase + 0xeefee0);

        std::vector<StatescriptInstance*> insts;
        nlohmann::json skipped = nlohmann::json::array();
        int n = comp->ss_inner.g1_instanceArr.num;
        for (int i = 0; i < n && i < 4096; i++) {
            auto inst = comp->ss_inner.g1_instanceArr.ptr[i];
            if (!inst) continue;
            insts.push_back(inst);
        }
        std::stable_sort(insts.begin(), insts.end(), [](auto x, auto y) { return (uint16_t)x->instance_id < (uint16_t)y->instance_id; });
        // The instance list is delta-coded and a 0 delta ends it, so every ID may appear
        // once: drop stopped / stopping instances (flags 0x4, 0x400, 0x10000, 0x20000) and any
        // later duplicate of an ID (seen after a SubScript was started twice).
        {
            std::vector<StatescriptInstance*> keep;
            for (auto inst : insts) {
                if (inst->flags & (SSI_STOPPED | SSI_STOP_REQUESTED | SSI_SHUTTING_DOWN | SSI_SHUT_DOWN)) { skipped.push_back(std::format("instance {} (stopped, flags 0x{:x})", inst->instance_id, inst->flags)); continue; }
                if (!keep.empty() && (uint16_t)keep.back()->instance_id == (uint16_t)inst->instance_id) { skipped.push_back(std::format("instance {} (duplicate ID, graph 0x{:x})", inst->instance_id, inst->script_id)); continue; }
                keep.push_back(inst);
            }
            insts.swap(keep);
        }
        // The owner's instance-by-ID list (+0x120 {ptr, short id} x count +0x128), which
        // state references (f2b230 / f2b360) resolve through.
        nlohmann::json ownerList = nlohmann::json::array();
        std::set<int> written;
        for (auto inst : insts) written.insert((uint16_t)inst->instance_id);
        {
            int cnt = *(int*)(owner + 0x128);
            char* lst = *(char**)(owner + 0x120);
            for (int i = 0; lst && i < cnt && i < 4096; i++) {
                char* ip = *(char**)(lst + 16 * i);
                short iid = *(short*)(lst + 16 * i + 8);
                ownerList.push_back({ {"id", iid}, {"parent", ip ? *(short*)(ip + 0x1A) : -1}, {"graph", ip ? std::format("0x{:x}", ((StatescriptInstance*)ip)->script_id) : ""} });
            }
        }

        // The client's server-acknowledged instances (sync object comp+0x5C0: +0x58
        // entries of 0x28 bytes, ID at +4, instance at +0x20, count +0x60). A delta may
        // name those by ID alone; others need a full entry.
        char* sync = (char*)comp + 0x5C0;
        std::set<int> known;
        {
            int cnt = *(int*)(sync + 0x60);
            char* lst = *(char**)(sync + 0x58);
            for (int i = 0; lst && i < cnt && i < 4096; i++)
                if (*(__int64*)(lst + 0x28 * i + 0x20)) known.insert(*(uint16_t*)(lst + 0x28 * i + 4));
        }

        SsWriter w;
        w.bit(0); w.bit(1); w.bit(0); // (ignored), local, +0x51
        w.count(0);                    // frame offset
        auto writeEntry = [&](StatescriptInstance* inst) {
            w.bits((uint32_t)(inst->script_id & 0xffff), 16);
            w.bit(0); // default owner value
            // child instances (SubScripts): parent instance ID + the u32 at +0x1C (the
            // parent's SubScript slot), which the reader matches existing instances on
            uint16_t parent = *(uint16_t*)((char*)inst + 0x1A);
            if (parent) {
                w.bit(1);
                w.count(parent);
                w.count(*(uint32_t*)((char*)inst + 0x1C));
            } else {
                w.bit(0);
            }
        };
        uint32_t prev = 0;
        nlohmann::json newEntries = nlohmann::json::array();
        for (auto inst : insts) {
            w.small((uint16_t)inst->instance_id - prev);
            prev = (uint16_t)inst->instance_id;
            if (!delta) { writeEntry(inst); continue; }
            // cecba0: 1 = DESTROY (the known entry is copied with its +0x29 flag set);
            // 0 then cee0e0: 0 = known instance, unchanged; 1 + entry = new instance
            if (known.count((uint16_t)inst->instance_id)) { w.bit(0); w.bit(0); continue; }
            w.bit(0); w.bit(1);
            writeEntry(inst);
            newEntries.push_back(inst->instance_id);
        }
        w.small(0);
        // owner vars: 7e0 id + value until 7e0(0). A full update (cee050) resets the
        // owner bag first, so it sends them all; a delta (ced600) doesn't, so a plain
        // delta sends none and a diff the changed ones.
        nlohmann::json ownerVars = nlohmann::json::array();
        {
            auto ownerValues = ss_bag_values(*(StatescriptVar_VarBag**)(owner + 0x2E0));
            for (auto& [vid, val] : ownerValues) {
                ownerVars.push_back(ss_value_json(vid, val));
                if (!ss_value_supported(val) || vid == 0) continue;
                if (store) next.owner[vid] = val;
                if (delta && !diff) continue;
                if (diff) {
                    auto it = prior.owner.find(vid);
                    if (it != prior.owner.end() && ss_same(it->second, val)) continue;
                }
                w.count(vid);
                ss_write_value(w, val);
            }
        }
        w.count(0);

        // A set of changed bits as FUN_7ff651050c90 reads it: n <= 8: n bits; else a
        // present bit per 8-bit chunk, then each present chunk's bits.
        auto writeSet = [&](const std::vector<bool>& set) {
            size_t n = set.size();
            if (n <= 8) { for (bool b : set) w.bit(b); return; }
            size_t chunks = (n - 1) / 8 + 1;
            std::vector<bool> present(chunks);
            for (size_t i = 0; i < n; i++) if (set[i]) present[i / 8] = true;
            for (bool b : present) w.bit(b);
            for (size_t c = 0; c < chunks; c++)
                if (present[c]) for (size_t i = c * 8; i < n && i < c * 8 + 8; i++) w.bit(set[i]);
        };

        nlohmann::json report = nlohmann::json::array();
        std::vector<SsWriter> eventBits;
        for (auto inst : insts) {
            char* graph = (char*)inst->graph;
            nlohmann::json r;
            r["instance"] = inst->instance_id;
            r["graph"] = std::format("0x{:016x}", inst->script_id);
            r["idx_bits"] = *(int32_t*)(graph + 0x17C);
            r["parent"] = *(uint16_t*)((char*)inst + 0x1A);
            // instance vars: full (cedf30) a changed bit per eligible sync var in slot
            // order, then the other variables as 7e0 id + value (local only), 7e0(0)
            // ends; delta (cee790) a changed set over all sync vars, left unchanged.
            // A diff's delta (cee790) has a changed set over every m_syncVars slot, the
            // changed values in slot order, then changed extras by ID.
            int nv = 0, eligible = 0;
            char** vars = ss_graph_list(graph, 0x130, nv);
            auto values = ss_bag_values(inst->rid_instance_varbag);
            int iid = (uint16_t)inst->instance_id;
            SsInstBaseline* was = prior.insts.count(iid) ? &prior.insts[iid] : nullptr;
            SsInstBaseline& now_inst = next.insts[iid];
            auto var_changed = [&](uint16_t vid, const StatescriptPrimitive& val) {
                if (!was) return true;
                auto it = was->vars.find(vid);
                return it == was->vars.end() || !ss_same(it->second, val);
            };
            for (auto& [vid, val] : values)
                if (ss_value_supported(val) && vid) now_inst.vars[vid] = val;
            std::set<uint16_t> synced;
            nlohmann::json varReport = nlohmann::json::array();
            if (delta) {
                std::vector<bool> set(nv > 0 ? nv : 0, false);
                std::vector<uint16_t> slotVar(set.size(), 0);
                for (int i = 0; vars && i < nv; i++) {
                    char* e = (char*)vars + i * 0x18;
                    if (*(__int64*)(e + 8) == 0) continue;
                    if (*(int*)(e + 0x10) == 0) eligible++;
                    uint16_t vid = (uint16_t)(*(__int64*)(e + 8) & 0xffff);
                    synced.insert(vid);
                    slotVar[i] = vid;
                    auto it = values.find(vid);
                    set[i] = diff && it != values.end() && ss_value_supported(it->second) && var_changed(vid, it->second);
                }
                writeSet(set);
                for (size_t i = 0; i < set.size(); i++)
                    if (set[i]) ss_write_value(w, values[slotVar[i]]);
            }
            for (int i = 0; !delta && vars && i < nv; i++) {
                char* e = (char*)vars + i * 0x18;
                if (*(__int64*)(e + 8) == 0 || *(int*)(e + 0x10) != 0) continue;
                eligible++;
                uint16_t vid = (uint16_t)(*(__int64*)(e + 8) & 0xffff);
                synced.insert(vid);
                auto it = values.find(vid);
                bool send = it != values.end() && ss_value_supported(it->second);
                w.bit(send);
                if (send) ss_write_value(w, it->second);
            }
            for (auto& [vid, val] : values) {
                auto vj = ss_value_json(vid, val);
                vj["synced"] = synced.count(vid) > 0;
                varReport.push_back(vj);
                if (synced.count(vid) || !ss_value_supported(val) || vid == 0) continue;
                if (delta && !(diff && var_changed(vid, val))) continue;
                w.count(vid);
                ss_write_value(w, val);
            }
            r["sync_vars"] = delta ? nv : eligible;
            r["vars"] = varReport;
            w.count(0);
            // states: full (cedbe0, local) every m_states node but client-/server-only
            // ones; delta (cee430) a changed set over all m_states, then the same per
            // changed state plus one trailing bit after the data
            int ns = 0;
            char** states = ss_graph_list(graph, 0x60, ns);
            nlohmann::json active = nlohmann::json::array();
            std::string layout;
            std::vector<bool> changed(ns > 0 ? ns : 0, false);
            for (int i = 0; states && i < ns; i++) {
                char* node = states[i];
                if (!node) { layout += '-'; continue; }
                layout += node[0x75] ? 'C' : node[0x76] ? 'V' : hasUpdate(node) ? 'S' : '.';
                changed[i] = !node[0x75] && !node[0x76];
                if (!changed[i] || !store) continue;
                char* impl = getImpl(inst, *(uint32_t*)(node + 0x68));
                bool on = impl && (impl[0x40] & 1);
                std::string data;
                if (on) {
                    SsWriter probe;
                    ss_write_state_data(probe, impl, !delta);
                    data = probe.hex();
                    now_inst.states[i] = data;
                }
                if (!diff) continue;
                bool wasOn = was && was->states.count(i);
                // "has update" states can only be sent active here, so a diff leaves their
                // deactivation to the client.
                if (layout[i] == 'S' && !on) { changed[i] = false; continue; }
                changed[i] = on != wasOn || (on && was->states[i] != data);
            }
            if (delta) writeSet(changed);
            for (int i = 0; states && i < ns; i++) {
                if (!changed[i]) continue;
                char* node = states[i];
                char* impl = getImpl(inst, *(uint32_t*)(node + 0x68));
                bool on = impl && (impl[0x40] & 1);
                // "has update" types (ecfee0): after a 1 the client reads the active bit
                // and then the state data whether active or not, so inactive ones get a
                // 0 (no update) instead.
                if (layout[i] == 'S') {
                    w.bit(on);
                    if (!on) continue;
                }
                w.bit(on);
                if (on) {
                    uint64_t before = w.nbits();
                    SsWriter probe;
                    ss_write_state_data(probe, impl, !delta);
                    ss_write_state_data(w, impl, !delta);
                    uint64_t bits = w.nbits() - before;
                    if (delta) w.bit(0); // read and dropped after the data
                    active.push_back({ {"state", i}, {"node", *(uint32_t*)(node + 0x68)}, {"data_bits", bits}, {"data_hex", probe.hex()} });
                }
            }
            r["active"] = active;
            r["layout"] = layout;
            // events (cf1620): Timer and Lifetime(finish) only
            SsWriter ew;
            int32_t idxBits = *(int32_t*)(graph + 0x17C);
            int64_t t = base;
            nlohmann::json evs = nlohmann::json::array();
            auto ev = inst->notif_head;
            for (int k = 0; ev && k < 1024; k++, ev = ev->child) {
                char* e = (char*)ev;
                uint8_t type = *(uint8_t*)(e + 0x22);
                int64_t ts = *(int64_t*)(e + 0x18);
                if (type == ETYPE_TIMER) {
                    uint32_t ud = *(uint32_t*)(e + 0x30);
                    if (ud == 1) { ew.bits(0, 2); }
                    else { ew.bits(1, 2); if (ud == 0) ew.bit(1); else { ew.bit(0); ew.small(ud); } }
                    ew.bits(*(uint16_t*)(e + 0x34), idxBits);
                } else if (type == ETYPE_LIFETIME && *(uint16_t*)(e + 0x30) == 1) {
                    ew.bits(2, 2);
                    ew.bits(*(uint16_t*)(e + 0x32), idxBits);
                } else {
                    evs.push_back({ {"skipped_type", type} });
                    continue;
                }
                int64_t d = ts - t;
                if (d == 0) ew.bit(1);
                else if (d > 0) { ew.bit(0); ew.bit(1); ew.count((uint32_t)d); }
                else { ew.bit(0); ew.bit(0); ew.count((uint32_t)-d); }
                t = ts;
                evs.push_back({ {"type", type}, {"time", ts}, {"rel_ms", ts - base} });
            }
            ew.bits(3, 2);
            if (delta) eventBits.push_back(std::move(ew));
            else w.append(ew); // full update: right after the instance's states
            r["events"] = evs;
            report.push_back(r);
        }
        if (delta) {
            // local tail (ced600): bit 1 = events follow, per instance sign bit + a30 ID
            // delta then its events (replacing its pending ones), 0 + a30(0) ends; then
            // bit 0 = no further segment, then cee920
            w.bit(1);
            uint32_t pid = 0;
            for (size_t i = 0; i < insts.size(); i++) {
                w.bit(0);
                w.small((uint16_t)insts[i]->instance_id - pid);
                pid = (uint16_t)insts[i]->instance_id;
                w.append(eventBits[i]);
            }
            w.bit(0); w.small(0);
            w.bit(0);
        }
        w.bit(0); // cee920: nothing

        nlohmann::json j;
        j["entity_id"] = std::format("0x{:x}", id);
        j["end_cf"] = end_cf;
        j["delta"] = delta;
        j["last_applied_cf"] = *(uint32_t*)((char*)comp + 0x5C0 + 0x984);
        j["client_cf"] = client_cf;
        j["baseline"] = baseline_mode;
        if (store)
            prior = std::move(next);
        j["new_entries"] = newEntries;
        j["owner_vars"] = ownerVars;
        j["tick"] = tick;
        j["payload_bits"] = w.nbits();
        j["payload_hex"] = w.hex();
        j["instances"] = report;
        j["skipped"] = skipped;
        j["owner_instances"] = ownerList;
        return j;
    });
}

// Same reflection as read_stu_object, but for an STU object at any address
// (e.g. a node's config var from dump_script's "addr"). Each field also gets its
// first 16 bytes, so inline arrays/structs can be decoded.
static nlohmann::json tool_read_stu_at(const nlohmann::json& a) {
    uint64_t addr = parse_u64(a.value("addr", nlohmann::json(0)));
    if (!addr || bad_read((void*)addr, 8)) return err("addr unreadable");
    return seh_guard("read_stu_at", [&]() -> nlohmann::json {
        auto base = (STUBase<STUBase_vt>*)addr;
        if (bad_read(base->vfptr, sizeof(void*))) return err("no vtable at addr");
        auto info = base->vfptr->GetSTUInfo();
        if (!info) return err("no STU info");
        char* obj = (char*)addr;
        nlohmann::json fields = nlohmann::json::array();
        for (auto [owner, arg] : info->RangeArgs()) {
            if (!arg) continue;
            nlohmann::json f;
            if (arg->Name_str && !bad_read((void*)arg->Name_str, 1)) f["name"] = arg->Name_str;
            else { auto hit = stringhash_library::hashes.find((int)arg->Hash); if (hit != stringhash_library::hashes.end()) f["name"] = hit->second; }
            f["hash"] = std::format("0x{:08x}", arg->Hash);
            f["offset"] = std::format("0x{:x}", arg->Offset);
            if (owner) f["declared_by"] = std::format("0x{:08x}", owner->Hash);
            unsigned char at[16];
            if (Utility::SafeMemory::Read(at, obj + arg->Offset, 16)) {
                std::string hex;
                for (int i = 0; i < 16; i++) hex += std::format("{:02x}", at[i]);
                f["bytes16"] = hex;
            }
            fields.push_back(f);
        }
        nlohmann::json j;
        j["addr"] = std::format("0x{:x}", addr);
        j["type_hash"] = std::format("0x{:08x}", info->Hash);
        if (info->Name_str && !bad_read((void*)info->Name_str, 1)) j["type"] = info->Name_str;
        j["fields"] = fields;
        return j;
    });
}

// Scan committed, readable memory for a byte pattern (hex). Returns up to
// max_hits absolute addresses. Runs on the frame thread, so a full scan stalls
// the game for a moment.
static nlohmann::json tool_search_memory(const nlohmann::json& a) {
    std::string hex = a.value("hex", "");
    std::vector<unsigned char> pat;
    for (size_t i = 0; i + 1 < hex.size(); ) {
        if (hex[i] == ' ') { i++; continue; }
        pat.push_back((unsigned char)std::stoul(hex.substr(i, 2), nullptr, 16));
        i += 2;
    }
    if (pat.empty()) return err("hex pattern required");
    int max_hits = a.value("max_hits", 50);
    nlohmann::json hits = nlohmann::json::array();
    uint64_t scanned = 0;
    constexpr size_t kChunk = 1 << 20;
    std::vector<unsigned char> chunk(kChunk + pat.size() - 1);
    MEMORY_BASIC_INFORMATION mbi;
    for (char* p = nullptr; VirtualQuery(p, &mbi, sizeof(mbi)) == sizeof(mbi); p = (char*)mbi.BaseAddress + mbi.RegionSize) {
        if ((uint64_t)mbi.BaseAddress + mbi.RegionSize < (uint64_t)p) break; // wrapped
        bool readable = mbi.State == MEM_COMMIT && !(mbi.Protect & (PAGE_GUARD | PAGE_NOACCESS)) &&
            (mbi.Protect & (PAGE_READONLY | PAGE_READWRITE | PAGE_WRITECOPY | PAGE_EXECUTE_READ | PAGE_EXECUTE_READWRITE | PAGE_EXECUTE_WRITECOPY));
        if (!readable) continue;
        const uintptr_t base = (uintptr_t)mbi.BaseAddress;
        const size_t size = mbi.RegionSize;
        if (size < pat.size()) continue;
        scanned += size;
        // Scan through safe copies (a page can be freed or re-protected mid-scan): chunks of kChunk
        // match starts, each copied with pat.size()-1 bytes of overlap.
        for (size_t off = 0; off + pat.size() <= size; off += kChunk) {
            size_t n = std::min(kChunk + pat.size() - 1, size - off);
            if (!Utility::SafeMemory::Read(chunk.data(), (const void*)(base + off), n)) break; // region changed
            const unsigned char* c = chunk.data();
            for (const unsigned char* q = c; q + pat.size() <= c + n; q++) {
                q = (const unsigned char*)memchr(q, pat[0], (c + n) - q - pat.size() + 1);
                if (!q) break;
                if (memcmp(q, pat.data(), pat.size()) != 0) continue;
                uint64_t at = base + off + (q - c);
                if (at >= (uintptr_t)chunk.data() && at < (uintptr_t)chunk.data() + chunk.size()) continue; // our own copy
                std::string where = "heap";
                if (at >= globals::gameBase && at < globals::gameBase + 0x2000000) where = std::format("game+0x{:x}", at - globals::gameBase);
                else if (at >= globals::modBase && at < globals::modBase + 0x1000000) where = "prometheus";
                hits.push_back({ {"addr", std::format("0x{:x}", at)}, {"where", where} });
                if ((int)hits.size() >= max_hits) goto done;
            }
        }
    }
done:
    return { {"hits", hits}, {"count", hits.size()}, {"scanned_mb", scanned >> 20} };
}

// --- ViewModels (statescript-controlled UI) -------------------------------

static const char* safe_vm_name(ViewModel* vm) {
    if (!vm || bad_read(vm, 0x70)) return nullptr;
    if (bad_read(vm->vfptr, sizeof(void*))) return nullptr;
    const char* name = vm->vfptr->GetName();
    if (!name || bad_read((void*)name, 1)) return nullptr;
    return name;
}

static nlohmann::json tool_list_viewmodels(const nlohmann::json& a) {
    std::string filter = a.value("filter", "");
    int limit = parse_int(a.value("limit", nlohmann::json(300)));
    int offset = parse_int(a.value("offset", nlohmann::json(0)));
    if (limit <= 0 || limit > 4000) limit = 300;
    return seh_guard("list_viewmodels", [&]() -> nlohmann::json {
        auto root = GetViewModelRoot();
        if (!root) return err("viewmodel root null");
        nlohmann::json arr = nlohmann::json::array();
        int n = root->viewmodel_list.num, i = 0, emitted = 0;
        if (n < 0 || n > 100000) n = 0;
        for (int k = 0; k < n; k++) {
            if (emitted >= limit) break;
            auto vm = root->viewmodel_list.ptr[k];
            const char* name = safe_vm_name(vm);
            if (!name) continue;
            if (!filter.empty() && !strstr(name, filter.c_str())) continue;
            if (i++ < offset) continue;
            arr.push_back({ {"id", (int)vm->id}, {"name", name}, {"property_count", vm->properties.num} });
            emitted++;
        }
        return { {"total", n}, {"offset", offset}, {"returned", emitted}, {"viewmodels", arr} };
    });
}

static nlohmann::json vm_prop_value(const ViewModelProperty& p) {
    nlohmann::json v;
    v["type"] = (int)p.type;
    v["has_value"] = (bool)p.has_value;
    v["raw"] = std::format("0x{:x}", (uint64_t)p.value);
    switch (p.type) {
    case ViewModelPropertyType_CHAR:  v["value"] = (int)(char)p.value; break;
    case ViewModelPropertyType_INT:   v["value"] = (int)p.value; break;
    case ViewModelPropertyType_UINT:  v["value"] = (uint32_t)p.value; break;
    case ViewModelPropertyType_FLT:   { float f; uint32_t u = (uint32_t)p.value; memcpy(&f, &u, 4); v["value"] = f; } break;
    case ViewModelPropertyType_LONG:  v["value"] = (int64_t)p.value; break;
    case ViewModelPropertyType_ULONG: v["value"] = (uint64_t)p.value; break;
    default: break; // STRING_REP etc: raw only (resolving needs string-rep deref)
    }
    return v;
}

static nlohmann::json tool_read_viewmodel(const nlohmann::json& a) {
    int id = parse_int(a.value("id", nlohmann::json(-1)));
    if (id < 0) return err("id required (from list_viewmodels)");
    return seh_guard("read_viewmodel", [&]() -> nlohmann::json {
        auto root = GetViewModelRoot();
        if (!root) return err("viewmodel root null");
        auto vm = root->vmById(id);
        if (!vm) return err("viewmodel id not found");
        const char* name = safe_vm_name(vm);
        nlohmann::json props = nlohmann::json::array();
        int n = vm->properties.num;
        if (n < 0 || n > 100000) n = 0;
        for (int k = 0; k < n; k++) {
            auto& item = vm->properties.ptr[k];
            nlohmann::json pj;
            pj["key"] = std::format("0x{:x}", (uint64_t)item.key);
            auto hit = stringhash_library::hashes.find((int)(uint32_t)item.key);
            if (hit != stringhash_library::hashes.end()) pj["key_name"] = hit->second;
            pj["prop"] = vm_prop_value(item.property);
            props.push_back(pj);
        }
        return { {"id", id}, {"name", name ? name : "?"}, {"property_count", n}, {"properties", props} };
    });
}

// Write a viewmodel property's value (gated). Sets the raw 64-bit value for the key.
static nlohmann::json tool_write_viewmodel(const nlohmann::json& a) {
    if (!writes_allowed()) return err(writes_refused_message());
    int id = parse_int(a.value("id", nlohmann::json(-1)));
    uint64_t key = parse_u64(a.value("key", nlohmann::json(0)));
    if (id < 0 || !key) return err("id and key required");
    if (!a.contains("value")) return err("value required");
    uint64_t val = parse_u64(a["value"]);
    return seh_guard("write_viewmodel", [&]() -> nlohmann::json {
        auto root = GetViewModelRoot();
        if (!root) return err("viewmodel root null");
        auto vm = root->vmById(id);
        if (!vm) return err("viewmodel id not found");
        auto item = vm->getByKey((__int64)key);
        if (!item) return err("key not found on viewmodel");
        uint64_t old = (uint64_t)item->property.value;
        item->property.value = (__int64)val;
        item->property.has_value = 1;
        LOG_CORE(Warn, "[MCP] write_viewmodel id={} key=0x{:x}: 0x{:x} -> 0x{:x}", id, key, old, val);
        return { {"ok", true}, {"id", id}, {"key", std::format("0x{:x}", key)}, {"old", std::format("0x{:x}", old)}, {"new", std::format("0x{:x}", val)} };
    });
}

// --- Component / entity construction --------------------------------------
// RE: FUN_7ff6507ef7c0 (= EntityAdmin_MagicalVodoo_SpawnEntity, entity_admin.h) is the
// engine's whole-entity create path; it builds an entity's component SET from an
// EntityLoader manifest. There is NO engine routine that grafts a single loose component
// onto an existing entity, so "construct a component" == spawn an entity that owns it.
//   def_id mode      : EntityLoader::Create(def_id)->Spawn(ea)  (repo-proven; full prefab)
//   component_id mode: bare loader + load_entity_into_loader(descriptor) + Spawn
//                      (mirrors FUN_7ff650d14e10; 1-component entity; experimental)
// Gated by g_write_enabled; seh_guard'd. DANGEROUS: runs component ctors offline; a
// server-authoritative component (e.g. PvPGame 0x24) may assert/crash without match/world.
static nlohmann::json tool_construct_component(const nlohmann::json& a) {
    if (!writes_allowed()) return err(writes_refused_message());
    std::string ea_name = a.value("ea", "game");
    uint64_t def_id  = parse_u64(a.value("def_id", nlohmann::json(0)));
    int      comp_id = parse_int(a.value("component_id", nlohmann::json(-1)));
    bool     dry_run = a.value("dry_run", false);
    EntityAdminBase* ea = ea_by_name(ea_name);
    if (!ea) return err("entity admin null");
    if (!def_id && comp_id < 0) return err("provide def_id (hex) or component_id (int)");
    if (def_id && comp_id >= 0) return err("provide only one of def_id / component_id");

    if (comp_id >= 0 && comp_id >= 0xc0) return err("component_id out of range (0..0xbf)");
    int dump_id = parse_int(a.value("dump_id", nlohmann::json(-1)));
    // Optional extra component ids to inject into a def spawn (def_id mode), e.g. [0x24] to
    // add PvPGame onto a well-formed host. player_spawner 'additional_compids' idiom.
    std::vector<int> extras;
    if (a.contains("extra_components") && a["extra_components"].is_array())
        for (auto& v : a["extra_components"]) { int id = parse_int(v); if (id > 0 && id < 0xc0) extras.push_back(id); }

    return seh_guard("construct_component", [&]() -> nlohmann::json {
        // Capture a freshly-built component's vtable + header in THIS call, so we record
        // PvPGame's live ctor/vtable/state even if a later engine tick crashes.
        auto dump_comp = [](nlohmann::json& j, Entity* e, int id) {
            if (id < 0 || !e) return;
            ComponentBase* cb = e->getById(id);
            nlohmann::json d; d["component_id"] = std::format("0x{:x}", id); d["present"] = (cb != nullptr);
            if (cb && !bad_read(cb, 0x80)) {
                d["addr"] = std::format("0x{:x}", (uintptr_t)cb);
                d["vtable"] = std::format("0x{:x}", *(uint64_t*)cb);
                d["hex"] = hex_dump((const uint8_t*)cb, 0x80);
            }
            j["dumped_component"] = d;
        };

        // ---- def_id mode: spawn a whole entity from an entity-definition resource ----
        // Proven pattern (pachipachi/player_spawner): resolve the resource, pass it as
        // stu_instance so EntityLoader::Create populates the manifest, then Spawn.
        if (def_id) {
            __int64 res = try_load_resource((__int64)def_id);
            if (!res) return err("try_load_resource returned 0 (def not loaded on this map)");
            auto loader = EntityLoader::Create((__int64)def_id, res, false, false);
            if (!loader) return err("EntityLoader::Create failed");
            for (int id : extras) loader->loader_entries[id].component_id = (unsigned char)id; // inject extras
            // Report the manifest the loader actually resolved (which component ids it will build).
            nlohmann::json manifest = nlohmann::json::array();
            for (int i = 0; i < 0xc0; i++)
                if (loader->loader_entries[i].component_id != 0)
                    manifest.push_back(std::format("0x{:x}", (int)loader->loader_entries[i].component_id));
            if (dry_run)
                return { {"mode","def_id"}, {"ea",ea_name}, {"def_id", std::format("0x{:016x}", def_id)},
                         {"resource", std::format("0x{:x}", (uint64_t)res)},
                         {"manifest_component_ids", manifest}, {"dry_run", true}, {"note","loader built; Spawn skipped"} };
            Entity* spawned = loader->Spawn(ea);
            if (!spawned || bad_read(spawned, 0x10)) return err("spawn returned null/bad");
            auto j = entity_brief(spawned);
            j["mode"] = "def_id"; j["ea"] = ea_name;
            j["def_id"] = std::format("0x{:016x}", def_id);
            j["manifest_component_ids"] = manifest;
            dump_comp(j, spawned, dump_id >= 0 ? dump_id : (extras.empty() ? -1 : extras[0]));
            return j;
        }

        // ---- component_id mode: bare entity owning one component (experimental) ----
        // No def. Build a loader like Create does, then mark one component present via the
        // id-indexed slot array (loader_entries[id], stride 0x20) -- the player_spawner idiom.
        // WARNING: a one-component entity is structurally invalid (no siblings/backref) and
        // faults the engine tick + our UI shortly after; use only to probe construction.
        auto loader = std::unique_ptr<EntityLoader>(new EntityLoader);
        ((void(__fastcall*)(EntityLoader*))(globals::gameBase + 0x9d4c10))(loader.get()); // ctor 0x9d4c10
        loader->vfptr = (EntityLoader_vt*)(globals::gameBase + 0x15c2978);
        loader->field_1850 = 5; loader->field_1854 = 1;
        loader->loader_entries[comp_id].component_id = (unsigned char)comp_id; // slot +0x8+id*0x20
        if (dry_run)
            return { {"mode","component_id"}, {"ea",ea_name}, {"component_id", std::format("0x{:x}", comp_id)},
                     {"dry_run", true}, {"note","bare loader built with one component entry; Spawn skipped"} };
        Entity* spawned = EntityAdmin_MagicalVodoo_SpawnEntity(ea, nullptr, loader.get());
        if (!spawned || bad_read(spawned, 0x10)) return err("spawn returned null/bad");
        auto j = entity_brief(spawned);
        j["mode"] = "component_id"; j["ea"] = ea_name;
        j["component_id"] = std::format("0x{:x}", comp_id);
        ComponentBase* cb = spawned->getById(comp_id);
        j["component_attached"] = (cb != nullptr);
        if (cb) j["component_ptr"] = std::format("0x{:x}", (uintptr_t)cb);
        dump_comp(j, spawned, dump_id >= 0 ? dump_id : comp_id);
        return j;
    });
}

// --- Registry -------------------------------------------------------------

static nlohmann::json schema_object(std::initializer_list<std::pair<const char*, const char*>> props,
                                    std::initializer_list<const char*> required = {}) {
    nlohmann::json props_j = nlohmann::json::object();
    for (auto& p : props) {
        props_j[p.first] = { {"type", p.second} };
    }
    nlohmann::json sch = { {"type", "object"}, {"properties", props_j} };
    if (required.size()) {
        nlohmann::json req = nlohmann::json::array();
        for (auto* r : required) req.push_back(r);
        sch["required"] = req;
    }
    return sch;
}

const std::vector<ToolDef>& all_tools() {
    static const std::vector<ToolDef> tbl = gate_writes({
        {
            "read_memory",
            "Read N bytes from gameBase+rva. Returns hex string. length max 4096.",
            schema_object({{"rva","string"},{"length","integer"}}, {"rva","length"}),
            tool_read_memory,
        },
        {
            "read_memory_abs",
            "Read N bytes from absolute address. length max 4096.",
            schema_object({{"addr","string"},{"length","integer"}}, {"addr","length"}),
            tool_read_memory_abs,
        },
        {
            "read_string",
            "Read null-terminated string at gameBase+rva. max_len caps the read.",
            schema_object({{"rva","string"},{"max_len","integer"}}, {"rva"}),
            tool_read_string,
        },
        {
            "read_pointer_chain",
            "Walk a pointer chain. Start at base or gameBase+base_rva, dereference offsets in sequence. Each offset adds to the current value, then reads uint64.",
            schema_object({{"base","string"},{"base_rva","string"},{"offsets","array"}}, {"offsets"}),
            tool_read_pointer_chain,
        },
        {
            "resolve_hash",
            "Look up a CRC32 hash in the project's hash library.",
            schema_object({{"hash","string"}}, {"hash"}),
            tool_resolve_hash,
        },
        {
            "add_hash",
            "Add a hash -> name entry to the in-memory hash library (extends what subsequent dumps/lookups can resolve).",
            schema_object({{"hash","string"},{"name","string"}}, {"hash","name"}),
            tool_add_hash,
        },
        {
            "list_loaded_scripts",
            "List statescript instances currently loaded on an entity admin. ea = 'lobby' or 'game'.",
            schema_object({{"ea","string"}}),
            tool_list_loaded_scripts,
        },
        {
            "dump_script",
            "Full node dump of a loaded statescript instance (script_id from list_loaded_scripts or entity_statescripts): nodes with type, flags, output plugs + links, incoming links, config vars with live values. ea = 'lobby' or 'game'; entity_id = an entity's own Statescript component instead of the EA singleton.",
            schema_object({{"script_id","string"},{"ea","string"},{"entity_id","string"}}, {"script_id"}),
            tool_dump_script,
        },
        {
            "lookup_stu_type",
            "Look up an STU type by its hash. Returns name (often empty in release builds), args list with names/offsets.",
            schema_object({{"hash","string"}}, {"hash"}),
            tool_lookup_stu_type,
        },
        {
            "game_state",
            "High-level snapshot: game base, module base, EA presence, loaded-script counts.",
            schema_object({}),
            tool_game_state,
        },
        {
            "call_function",
            "Call a function at gameBase+rva with up to 8 uint64 args. Returns uint64 result. DANGEROUS: arbitrary code execution in the game process.",
            schema_object({{"rva","string"},{"args","array"}}, {"rva","args"}),
            tool_call_function,
        },
        {
            "call_function_void",
            "Same as call_function but explicitly marked void in intent. Same dispatch.",
            schema_object({{"rva","string"},{"args","array"}}, {"rva","args"}),
            tool_call_function_void,
        },
        {
            "write_memory",
            "Write hex bytes at gameBase+rva. Requires writes-enabled toggle in the MCP Server window (off by default).",
            schema_object({{"rva","string"},{"hex","string"}}, {"rva","hex"}),
            tool_write_memory,
        },
        {
            "inject_resource",
            "Create a resource from a file under a GUID (CreateFromMemory; e.g. an edited map data before the map loads). The GUID must not be loaded at the time. Holds a reference until the client exits. Requires the writes-enabled toggle.",
            schema_object({{"guid","string"},{"path","string"}}, {"guid","path"}),
            tool_inject_resource,
        },
        {
            "execute_node",
            "Execute a statescript node by index on a loaded script. Useful for triggering Actions/Entries. Optional entity_id targets that entity's own 0x23 component (for per-entity scripts like the payload/objective); omit it to use the EA singleton.",
            schema_object({{"script_id","string"},{"node_idx","integer"},{"ea","string"},{"entity_id","string"}}, {"script_id","node_idx"}),
            tool_execute_node,
        },
        {
            "start_stop_state",
            "Start (start=true) or stop (start=false) a State node by index on a loaded script. Optional entity_id targets that entity's own 0x23 component (per-entity scripts like the payload/objective).",
            schema_object({{"script_id","string"},{"node_idx","integer"},{"start","boolean"},{"ea","string"},{"entity_id","string"}}, {"script_id","node_idx"}),
            tool_start_stop_state,
        },
        {
            "play_script",
            "Instantiate (play) a statescript graph by resource id on an entity admin (ea='game'|'lobby'). Calls the engine load/play function so it works even for scripts that are loaded only as graph resources (uninstantiated server-side gamemode logic). Optional entity_id (uint32 owner) and exec_flags. DANGEROUS: instantiates server-authoritative logic; may assert/crash or sit inert without proper objective/entity context.",
            schema_object({{"script_id","string"},{"ea","string"},{"entity_id","string"},{"exec_flags","string"}}, {"script_id"}),
            tool_play_script,
        },
        {
            "list_entities",
            "Enumerate entities on an entity admin (ea='game'|'lobby'). Returns entity_id, global_id, def (entity-definition resource id), and component_ids. Optional has_component (int) filters to entities owning that component id (e.g. 0x23=Statescript, 0x28=Health, 0x3f=PlayerInfo). Paginated via limit/offset (over matches).",
            schema_object({{"ea","string"},{"limit","integer"},{"offset","integer"},{"has_component","integer"}}),
            tool_list_entities,
        },
        {
            "get_entity",
            "Detail for one entity by entity_id (hex/int): components and parent. ea='game'|'lobby'.",
            schema_object({{"entity_id","string"},{"ea","string"}}, {"entity_id"}),
            tool_get_entity,
        },
        {
            "find_entity",
            "Locate a special entity. by='local_player' returns the controller (local player) entity's id. ea='game'|'lobby'.",
            schema_object({{"by","string"},{"ea","string"}}),
            tool_find_entity,
        },
        {
            "read_component",
            "Raw hex of a component (by component_id) on an entity. Decode offsets via the Component_X structs. ea='game'|'lobby'. length max 4096.",
            schema_object({{"entity_id","string"},{"component_id","integer"},{"length","integer"},{"ea","string"}}, {"entity_id","component_id"}),
            tool_read_component,
        },
        {
            "list_systems",
            "List the EA's registered systems in tick order: instance pointer + vtable (game+rva). ea='game'|'lobby'|'session'|'0x<EA addr>'.",
            schema_object({{"ea","string"}}),
            tool_list_systems,
        },
        {
            "ecs_dump",
            "Full ECS snapshot of an EA: counters, pending queues, systems in tick order (subscriptions, overridden callback slots), and per component type: creator, dependencies, live count, subscribed systems. ea='game'|'lobby'|'session'|'0x<EA addr>'.",
            schema_object({{"ea","string"}}),
            tool_ecs_dump,
        },
        {
            "ecs_trace",
            "ECS event tracer (docs/ECS.md). action=start|stop|clear|status|fetch|save (save: path=<Windows path>, since=<seq>: writes JSON lines directly, fast). start: hooks spawn/add_component/delete_request/destroy/flushes and gives every system a private vtable copy to record on_created/on_added/on_deleting/on_removed with tick + phase (system_tick phase_sys = which system's OnTick caused it); ticks=true also records every EA tick (noisy); clock_dilate/clock_advance/cf_range are always recorded (one per EA tick); cf=true also records every CF listener call (cf_pre/cf_listener/cf_post; high volume); watch_parents=true snapshots every entity's parent around each system OnTick and records parent_changed (slow; research only). fetch: since=<seq>, limit, kind filter.",
            schema_object({{"action","string"},{"path","string"},{"since","integer"},{"limit","integer"},{"kind","string"},{"ticks","boolean"},{"cap","integer"},{"watch_parents","boolean"},{"cf","boolean"}}),
            [](const nlohmann::json& a) { return ecs_trace::control(a); },
        },
        {
            "ss_trace",
            "Statescript execution tracer (docs/StatescriptRuntime.md §4). action=start|stop|clear|status|fetch|save. start: graphs=[graph GUIDs], entities=[entity ids], all=bool (trace every instance), cap=max events; hooks the sync manager command-frame loop (cf/cf_end), notification execution (notif: a=type, b=time ms, s=decoded), state begin/reenter/finish/abort (a=m_states idx, b=m_nodes idx), link traversal (link: a=source node, b=plug offset | transition idx<<32, c=target node; link_done), instantiate, game-message delivery (message: a=message GUID, b=result) and per-CF variable changes (var: s=bag:id=value). Every event carries cf, ms (sync manager clock), ent, inst. fetch: since, limit (keep small); save: path=<Windows path>, since: JSON lines.",
            schema_object({{"action","string"},{"graphs","array"},{"entities","array"},{"all","boolean"},{"cap","integer"},{"since","integer"},{"limit","integer"},{"path","string"}}),
            [](const nlohmann::json& a) { return ss_trace::control(a); },
        },
        {
            "logical_button",
            "Press logical buttons without the keyboard (repeatable Statescript recordings; docs/StatescriptRuntime.md §4.1.1). action=list (ids, names, command_bit / action_code / axis; all=true includes unnamed) | press | release | tap (press, auto-release after hold_ms, default 100) | status. button = name (e.g. \"fire\", \"ability_1\", \"reload\"), id or \"0x5d\". mode=input (default): the real input path (hooks the is-held query 0xcec330 and the input system tick, also with the window unfocused): command -> server + the hero's LogicalButton notifications, for buttons with a command_bit, action_code or axis. mode=notify: the Fake LogicalButton window's path, queues LogicalButton notifications (type 7, flags 1) on every instance of entity_id's Statescript component (default: the local hero); local only, any id.",
            schema_object({{"action","string"},{"button","string"},{"hold_ms","integer"},{"mode","string"},{"entity_id","string"},{"all","boolean"}}),
            [](const nlohmann::json& a) { return logical_button::control(a); },
        },
        {
            "debug_draw",
            "World-space debug shapes drawn by the engine's own PrimDraw, depth-tested in the game camera (docs/DebugDrawing.md; window Tools > Debug Draw). action=status (shapes=true lists them) | add (one shape, or shapes=[...]; ttl=seconds, 0 = until cleared; shape = {kind: sphere|box|capsule|cylinder|line|slot, pos:[x,y,z], radius, height, half:[x,y,z], yaw (rad), to:[x,y,z] (line), color:[r,g,b,a] 0-1 (a<1 translucent), group, label, flags (PrimDraw flags 0x20/0x100), slot (kind slot: raw render-vtable offset 0x440..0x498)}) | clear (group=name, default all local shapes) | poll (on, host, port (3729 = instance control API), conn, interval_ms: GET /debug/draw from the game server) | enable (on) | groups (visible={name: bool}) | settings (box_scale, cylinder_slot, cylinder_centred). World space: metres, y up. Screen space (engine debug-overlay pass, pixels, origin top left): hud (text, or lines=[{text, color, group}]; clear=true [group]; ttl; on=bool: the Prometheus HUD overlay, engine debug font) | screen (prims=[{kind: rect, rect:[x,y,w,h]} or {kind: lines, points:[[x,y],...]}, color, group]; clear, ttl) | overlays (the engine's 10 built-in overlays fps wt ts rb tm mma ci rm il gt: set={code: bool} or {code: {on, mode}}, fps_line=bool; lists them).",
            schema_object({{"action","string"},{"shapes","array"},{"kind","string"},{"pos","array"},{"to","array"},{"half","array"},{"radius","number"},{"height","number"},{"yaw","number"},{"color","array"},{"group","string"},{"label","string"},{"flags","integer"},{"slot","string"},{"ttl","number"},{"on","boolean"},{"host","string"},{"port","integer"},{"conn","integer"},{"interval_ms","integer"},{"visible","object"},{"box_scale","number"},{"cylinder_slot","string"},{"cylinder_centred","boolean"},{"text","string"},{"lines","array"},{"prims","array"},{"clear","boolean"},{"set","object"},{"fps_line","boolean"}}),
            [](const nlohmann::json& a) { return debug_draw::control(a); },
        },
        {
            "entity_statescripts",
            "List the statescript graph(s) running on a specific entity's own 0x23 component (script_id + node count). Maps an entity to its logic to identify scripted objects (payload, objective, pickup). ea='game'|'lobby'.",
            schema_object({{"entity_id","string"},{"ea","string"}}, {"entity_id"}),
            tool_entity_statescripts,
        },
        {
            "list_stu_resources",
            "List loaded STU resources. Optional type_hash filters to types assignable-to that hash (e.g. stringHash of an STU type); 0 = all. Returns resource_id + type. Paginated.",
            schema_object({{"type_hash","string"},{"limit","integer"},{"offset","integer"}}),
            tool_list_stu_resources,
        },
        {
            "read_stu_object",
            "Dump a loaded STU object by resource_id via STU reflection: address, fields (hash/offset/kind); resource references decoded to {guid, type, handle, loaded} (also lists of them); object / inline / list-of-object fields expanded up to `depth` (default 1); lists capped at max_list (default 32).",
            schema_object({{"resource_id","string"},{"depth","integer"},{"max_list","integer"}}, {"resource_id"}),
            tool_read_stu_object,
        },
        {
            "graph_sync_info",
            "For an entity definition (0x040...): its components and the Statescript graphs they reference (incl. STUWeaponComponent weapon scripts); for a graph (0x058...): just that graph. Each graph: index and sync layout (eligible sync vars / remote sync nodes, as the client's Statescript full-update decoder counts them), plus sync_var_list (var id, enum, owner_var flag +0x14, eligible, slot) and remote_node_list (m_remoteSyncNodes: node index +0x68, state index +0xF8, remote index +0x6C, type, client_only +0x75 / server_only +0x76, eligible, update_index = the index a remote full update / /scripts \"active\" uses).",
            schema_object({{"resource_id","string"}}, {"resource_id"}),
            tool_graph_sync_info,
        },
        {
            "ss_serialize_local",
            "Serialize an entity's live Statescript state as the payload of a LOCAL full update (or, with delta=true, a delta update that leaves variables alone; send it with start CF = last_applied_cf) (what the client's full-update reader FUN_7ff650ced430 reads), using the client's own state/mirror writers. end_cf = the packet's end command frame (event time base). Returns payload_bits + payload_hex (LSB-first bits) and a per-instance report (active states, variables). Includes child instances (parent + slot), owner vars and instance vars (INT/BYTE/FLT sent; other types reported only). To make a node's effect stick, use execute_node_server instead. end_cf 0 = the client's current frame (reported as client_cf). baseline: 'store' remembers what this capture sends; 'diff' (with delta=true) sends only what changed since the stored capture (states whose activation/data changed, changed owner/instance vars, new instances), then stores this one.",
            schema_object({{"entity_id","string"},{"end_cf","integer"},{"delta","boolean"},{"ea","string"},{"baseline","string"}}, {"entity_id"}),
            tool_ss_serialize_local,
        },
        {
            "execute_node_server",
            "Execute a statescript node (or with start=true/false start/stop a state) on a game EA entity, then delay_frames (default 2) frames later capture the entity (ss_serialize_local) and send it to our game server (control API 127.0.0.1:3728 POST /scripts) as an authoritative full update, so the client keeps the result instead of rolling it back. Without node_idx it only captures and sends. Returns once queued; see server_bridge_status.",
            schema_object({{"entity_id","string"},{"script_id","string"},{"node_idx","integer"},{"start","boolean"},{"delay_frames","integer"}}, {"entity_id"}),
            tool_execute_node_server,
        },
        {
            "hero_attach_weapons",
            "Do what Prometheus's player spawner does to a hero, on an existing game EA hero entity (e.g. the one our server spawned): load its STUWeaponComponent manager script and weapon scripts (weapon index instance var 0x1c, owner array 0x1d) and, unless fixup=false, set owner var 0xFD = 1. Local only: follow with execute_node_server (entity only) to make it stick.",
            schema_object({{"entity_id","string"},{"fixup","boolean"}}, {"entity_id"}),
            tool_hero_attach_weapons,
        },
        {
            "server_echo",
            "Echo mode on/off for a game EA entity (the local hero): our game server sends its Statescript state back as authoritative, a full update first and then every few frames a delta of what changed, stamped with the client's own frame, so what the client predicts (firing, abilities) is confirmed instead of rolled back. Dev stand-in for server-side simulation.",
            schema_object({{"entity_id","string"},{"enabled","boolean"}}, {}),
            tool_server_echo,
        },
        {
            "custom_jam_status",
            "State of the PrometheusLobby custom JAM protocol (docs/JAM.md §13): registration, route config, calls received/answered, last call.",
            schema_object({}),
            [](const nlohmann::json&) -> nlohmann::json { return Network::CustomJam::status(); },
        },
        {
            "server_view",
            "Live server view (docs/DebugDrawing.md §5): the instance server's view of the match, streamed every tick over PrometheusLobby 32002 after !serverview on. {} → latest frame (header, body: clock, game, entities, the server's hero position), the client's own state (CF, clock, hero position, error vs the server in m) and stats (rate, latency last/avg/p95 split instance→lobbylogic→client, drops, coalesced, sizes); history=n adds the last n headers; action=draw {on}; action=reset (stats); action=hello (lobbylogic's capability check).",
            schema_object({{"action","string"},{"history","integer"},{"on","boolean"}}),
            [](const nlohmann::json& a) -> nlohmann::json { return server_view::control(a); },
        },
        {
            "server_bridge_status",
            "Outcome of the last server bridge capture (execute_node_server / the graph view's server buttons).",
            schema_object({}),
            tool_server_bridge_status,
        },
        {
            "search_memory",
            "Scan committed readable memory for a hex byte pattern (little-endian as in memory, e.g. a u64 id 0x0ac0000000000916 = '160900000000c00a'). Returns up to max_hits (default 50) addresses. Stalls the game briefly.",
            schema_object({{"hex","string"},{"max_hits","integer"}}, {"hex"}),
            tool_search_memory,
        },
        {
            "read_stu_at",
            "Reflect an STU object at an absolute address (e.g. a config var 'addr' from dump_script): type + fields (hash, offset, first 16 bytes).",
            schema_object({{"addr","string"}}, {"addr"}),
            tool_read_stu_at,
        },
        {
            "list_viewmodels",
            "List statescript-controlled UI ViewModels (name, id, property_count). Optional substring filter. Paginated.",
            schema_object({{"filter","string"},{"limit","integer"},{"offset","integer"}}),
            tool_list_viewmodels,
        },
        {
            "read_viewmodel",
            "Read a ViewModel's properties (key, type, value) by id (from list_viewmodels).",
            schema_object({{"id","integer"}}, {"id"}),
            tool_read_viewmodel,
        },
        {
            "write_viewmodel",
            "Set a ViewModel property's 64-bit value by id + key. Gated by writes-enabled toggle. DANGEROUS: mutates live UI state.",
            schema_object({{"id","integer"},{"key","string"},{"value","string"}}, {"id","key","value"}),
            tool_write_viewmodel,
        },
        {
            "construct_component",
            "Spawn an entity via the engine create path (EntityAdmin_MagicalVodoo_SpawnEntity / "
            "FUN_7ff6507ef7c0). Provide def_id (hex entity-definition resource id, e.g. "
            "0x0400000000000225 = Genji's gameplay entity, STUHero.m_gameplayEntity; NOT a gamemode "
            "host: the gamemode brains are 0x0400000000000013 and 0x0400000000000039, see "
            "docs/MatchLogic.md section 2) to spawn that full prefab, OR component_id (int, "
            "e.g. 0x24=PvPGame) to spawn a bare entity owning just that component (experimental — a "
            "one-component entity is structurally invalid and crashes the tick/UI shortly after). "
            "extra_components=[ids] injects more components into a def spawn (e.g. [36] adds PvPGame "
            "0x24 onto the spawned entity so it has valid siblings). dump_id=N hex-dumps that component's vtable "
            "+ first 0x80 bytes in the SAME call (capture state pre-crash; defaults to first extra / "
            "the component_id). ea='game'|'lobby'. dry_run=true builds/resolves WITHOUT spawning (safe). "
            "Gated by writes-enabled. DANGEROUS: runs component constructors offline; may assert/crash.",
            schema_object({{"def_id","string"},{"component_id","integer"},{"extra_components","array"},{"dump_id","integer"},{"ea","string"},{"dry_run","boolean"}}),
            tool_construct_component,
        },
    });
    return tbl;
}

const ToolDef* find_tool(const std::string& name) {
    for (auto& t : all_tools()) if (t.name == name) return &t;
    return nullptr;
}

} // namespace prom_mcp
