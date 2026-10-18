#pragma once
#include "../window_manager/window_manager.h"
#include "../../Statescript.h"
#include "../../entity_admin.h"
#include "../../game.h"
#include "../../stringhash_library.h"
#include "../../STU.h"
#include "../../stu_resources.h"
#include "../../Logs/Logs.h"
#include <nlohmann/json.hpp>
#include "../../STUConfigVarDynamic_Custom.h"
#include <fstream>
#include <filesystem>
#include <set>
#include <unordered_map>
#include <map>
#include <shellapi.h>

// Walks every entity admin's Component_23_Statescript, dedupes by script_id, and
// dumps each unique script's graph to JSON for offline analysis. Per-script files
// + combined _all.json + lightweight _index.json. Aimed at feeding the data into
// an AI for game-logic reverse engineering.
class statescript_dumper : public window {
    WINDOW_DEFINE(statescript_dumper, "Tools", "Statescript Dumper", true);

    struct DumpStats {
        int instances_seen = 0;
        int unique_scripts = 0;
        int dumped = 0;
        int failed = 0;
    };

    DumpStats _stats;
    std::string _status;
    ImVec4 _status_color = ImVec4(0.7f, 0.7f, 0.7f, 1.0f);
    bool _include_lobby = true;
    bool _include_game = true;

    // Session accumulator: every script captured during this process's lifetime,
    // keyed by script_id, latest snapshot per script. Scripts that get unloaded
    // from the game stay in the cache so a single dump captures everything we've
    // ever seen this session.
    std::map<__int64, nlohmann::json> _session_cache;
    bool _auto_scan = true;
    int _scan_interval_frames = 60; // ~1s at 60fps
    int _frames_since_scan = 0;
    int _last_lobby_count = -1;
    int _last_game_count = -1;
    int _last_graph_count = -1;        // # of loaded STUStatescriptGraph resources at last graph scan
    bool _include_graphs = true;       // also capture uninstantiated loaded graphs (server-side gamemode etc.)
    int _scan_count = 0; // diagnostic

    // --- Hash → name lookups (built once) -------------------------------------

    static const std::unordered_map<uint32_t, const char*>& stu_type_table() {
        // Hand-curated from STU.h::STU_NAME — these resolve the vast majority of
        // node STU types seen in real dumps. Add to this as more are reversed.
        static const std::unordered_map<uint32_t, const char*> tbl = {
            { 0x3E490486, "STUStatescriptOutputPlug" },
            { 0x4698e4b6, "STUStatescriptActionSetVar" },
            { 0xe811ecbb, "STUStatescriptStateBooleanSwitch" },
            { 0xbe49a4c2, "STUStatescriptEntryGameMessage" },
            { 0xff7876f9, "STUConfigVarGameMessage" },
            { 0xc739baac, "STUStatescriptStateUXScreen" },
            { 0x8ba08edd, "STUStatescriptStateUXBase" },
            { 0x3db862c4, "STUStatescriptStateSubScript" },
            { 0x65f158da, "STUStatescriptStateWait" },
            { 0x11d7f052, "STUStatescriptInputPlug" },
            { 0x26367267, "STUStatescriptStateSwitch" },
            { 0xffbfa88d, "STUStatescriptStateEffect" },
            { 0x9b38211b, "STUStatescriptComponent" },
            { 0x8F23FA30, "STUStatescriptStateUXHeroSelectScreen" },
            { 0xff77458c, "STUStatescriptStateLogicalButton" },
            { 0x5c7964ff, "STUConfigVarLogicalButton" },
            { 0xcc3a08f4, "STUConfigVarExpression" },
            { 0xa7213568, "STUStatescriptState" },
            { 0x2ecefc8b, "STUStatescriptAction" },
            { 0xbfd9aadd, "STUConfigVar" },
            { 0xf8cc195d, "STUConfigVarDynamic" },
            { 0x66b5f581, "STUAction_PlayEffect" },
            { 0x2501a4db, "STUAction_RandomFromBucket" },
            { 0x5bea9b4b, "STUAction_SendGameMsgConditional" },
            { 0x24ab1f0b, "Subgraph_Plug" },
            { 0xb8f35b20, "Subgraph_Plug_2" },
            { 0x5732771b, "ClientOnly_Subgraph_Plug" },
            { 0x26c93342, "ClientOnly_Subgraph_Plug_2" },
            { 0x4e5feee5, "ServerOnly_Subgraph_Plug" },
            { 0x34D7F9EE, "STUIdentifier" },
            { 0xb1032daf, "Simple_Condition" },     // STU.h:132
            { 0x48663e64, "Effect_To_Play_Var" },   // STU.h:133
            // Inferred from dump structural patterns (each appears in role-consistent positions
            // across 100+ scripts). Names are best-guess flow labels, safe to rename if RE proves
            // otherwise. Source: re-analysis report 2026-06.
            { 0x460c9ed6, "Plug_OnStart" },          // every Entry node's start trigger
            { 0x3f6ffa05, "Plug_Transition" },       // every State node's "next" plug
            { 0xdc7ab998, "Plug_BoolSwitch_True" },  // STUStatescriptStateBooleanSwitch true branch
            { 0xde163a4c, "Plug_BoolSwitch_False" }, // STUStatescriptStateBooleanSwitch false branch
            { 0x98c4b8a6, "Plug_Condition_True" },   // Simple_Condition true branch
            { 0xb0fea54e, "Plug_Condition_False" },  // Simple_Condition false branch
        };
        return tbl;
    }

    // Best-effort resolution priority: 1) STUInfo::Name_str (live, may be
    // stripped), 2) hand-curated STU type table, 3) project hash library,
    // 4) fall back to hex.
    static std::string resolve_stu_type(STUInfo* stu) {
        if (stu) {
            const char* live_name = stu->Name_str;
            if (live_name && !IsBadReadPtr((void*)live_name, 1) && live_name[0] >= 0x20 && live_name[0] < 0x7F) {
                return live_name;
            }
            const auto& tbl = stu_type_table();
            auto it = tbl.find(stu->Hash);
            if (it != tbl.end()) return it->second;
            auto hit = stringhash_library::hashes.find((int)stu->Hash);
            if (hit != stringhash_library::hashes.end()) return hit->second;
            return std::format("0x{:08x}", stu->Hash);
        }
        return "(null)";
    }

    static std::string resolve_hash(uint32_t hash) {
        const auto& tbl = stu_type_table();
        auto it = tbl.find(hash);
        if (it != tbl.end()) return it->second;
        auto hit = stringhash_library::hashes.find((int)hash);
        if (hit != stringhash_library::hashes.end()) return hit->second;
        return std::format("0x{:08x}", hash);
    }

    // --- Resource ID kind tagging ---------------------------------------------
    // OW data resources use the high 12 bits of the 64-bit ID as a type code
    // (per overtools/OWLib conventions). For 0x0580_...., the kind code is 0x058
    // — shift by 52, not 48. (Previous version shifted by 48 and produced 0x580
    // which never matched any table entry → tagged 0/782 INT64 literals.)
    static const char* resource_kind(uint64_t id) {
        uint32_t type = (uint32_t)(id >> 52) & 0xFFF;
        switch (type) {
        case 0x003: return "Map";
        case 0x020: return "HeroSelect";
        case 0x042: return "Cosmetic";
        case 0x058: return "Statescript";
        case 0x09A: return "UIScreen";
        case 0x0AC: return "String";
        case 0x0C4: return "Hero";
        case 0x1C9: return "Announcer";
        default:    return nullptr;
        }
    }

    static const char* node_type_str(StatescriptNodeType t) {
        switch (t) {
        case StatescriptNodeType_Entry:     return "Entry";
        case StatescriptNodeType_Action:    return "Action";
        case StatescriptNodeType_State:     return "State";
        case StatescriptNodeType_Condition: return "Condition";
        case StatescriptNodeType_Other:     return "Other";
        default:                            return "Unknown";
        }
    }

    static const char* known_script_name(__int64 script_id) {
        switch (script_id) {
        case 0x580000000000158: return "Lobby Menu";
        case 0x580000000000138: return "Social Menu";
        case 0x5800000000002d8: return "Chat";
        case 0x58000000000041a: return "Developer Mode";
        case 0x5800000000004f6: return "Welcome Message";
        case 0x5800000000001e5: return "Music Controller";
        case 0x580000000000b47: return "Map Screen";
        default:                return nullptr;
        }
    }

    // Reads an editor-set string pointer (m_debugText / m_displayName / m_comment).
    // Some are nominally __int64 in our reverse, but live as const char* in memory.
    static std::optional<std::string> read_editor_str(__int64 raw_ptr) {
        if (!raw_ptr) return std::nullopt;
        const char* s = (const char*)raw_ptr;
        __try {
            if (IsBadReadPtr(s, 1)) return std::nullopt;
            if (s[0] < 0x20 || s[0] > 0x7E) return std::nullopt;
            size_t len = strnlen_s(s, 512);
            if (len == 0 || len >= 512) return std::nullopt;
            return std::string(s, len);
        }
        __except (EXCEPTION_EXECUTE_HANDLER) {
            return std::nullopt;
        }
    }

    // --- Expression bytecode dump ---------------------------------------------

    static nlohmann::json dump_expression(STUConfigVarExpression* expr) {
        nlohmann::json j;
        __try {
            // Raw opcode stream — see Statescript.h:380-449 for opcode reference.
            nlohmann::json ops = nlohmann::json::array();
            int oc = expr->m_expression.m_opcodes.count();
            unsigned char* opl = expr->m_expression.m_opcodes.list();
            for (int i = 0; i < oc; i++) ops.push_back((int)opl[i]);
            j["opcodes"] = ops;
            j["opcode_count"] = oc;

            nlohmann::json consts = nlohmann::json::array();
            int fc = expr->m_expression.float_constants.count();
            float* fcl = expr->m_expression.float_constants.list();
            for (int i = 0; i < fc; i++) consts.push_back(fcl[i]);
            j["float_constants"] = consts;

            j["dynamic_var_count"] = expr->m_expression.m_dynamicVars.count();
            j["fragment_count"] = expr->m_expression.m_fragments.count();
            j["config_var_count"] = expr->m_configVars.count();
        }
        __except (EXCEPTION_EXECUTE_HANDLER) {
            j["error"] = "expression read failed";
        }
        return j;
    }

    static nlohmann::json extract_config_var(STUConfigVar* cv, StatescriptInstance* inst) {
        nlohmann::json j;
        __try {
            if (cv->is_expression()) {
                j["kind"] = "expression";
                j["expression"] = dump_expression((STUConfigVarExpression*)cv);
            }
            else {
                j["kind"] = "literal";
                // The config var's own STU type (Literal, Dynamic, a Utility accessor, ...).
                if (auto info = cv->base.vfptr->GetSTUInfo()) {
                    j["cv_type_hash"] = std::format("0x{:08x}", info->Hash);
                    auto name = resolve_hash(info->Hash);
                    if (!name.empty() && name[0] != '0') j["cv_type"] = name;
                }
                // A Dynamic is a variable reference: report WHICH variable (its GUID
                // path), not just its current value. Read through STUBullshitListFull: game
                // assets use flag 2 ({count, list}), Prometheus-built vars flag 1 ({list, count}).
                j["addr"] = std::format("0x{:x}", (uint64_t)cv);
                if (cv->is_dynamic()) {
                    j["kind"] = "dynamic";
                    auto& path = ((STUConfigVarDynamic*)cv)->item_location;
                    nlohmann::json ids = nlohmann::json::array();
                    if (path.list_ptr) {
                        int count = path.count();
                        STUResourceReference* refs = path.list();
                        if (refs && count > 0 && count < 16)
                            for (int i = 0; i < count; i++)
                                ids.push_back(std::format("{:016x}", (uint64_t)refs[i].resource_id));
                    }
                    j["var_ids"] = ids;
                }
                StatescriptPrimitive val{};
                if (cv->get_value(inst, &val)) {
                    j["type"] = val.get_type_str();
                    j["value"] = val.get_value_str();
                    // Tag int64 resource IDs with their kind when recognized.
                    if (val.type == StatescriptPrimitive_INT64 || val.type == StatescriptPrimitive_INT) {
                        if (auto kind = resource_kind((uint64_t)val.value)) {
                            j["resource_kind"] = kind;
                        }
                    }
                }
                else {
                    j["value"] = "<eval returned false>";
                }
            }
        }
        __except (EXCEPTION_EXECUTE_HANDLER) {
            j["value"] = "<eval threw>";
        }
        return j;
    }

    // Pre-pass: build map of dst_idx → [{from_idx, plug_name}] for incoming links.
    struct IncomingLink { int from_idx; std::string plug_name; uint32_t plug_hash; };
    static std::map<int, std::vector<IncomingLink>> build_incoming_map(STUStatescriptGraph* graph) {
        std::map<int, std::vector<IncomingLink>> m;
        __try {
            for (auto* src : graph->m_nodes) {
                if (!src) continue;
                auto conns = src->get_output_plugs();
                for (auto& [plug_hash, plugs] : conns) {
                    std::string pname = resolve_hash((uint32_t)plug_hash);
                    for (auto* plug : plugs) {
                        if (!plug) continue;
                        for (auto* link : plug->m_links) {
                            if (!link || !link->m_inputPlug) continue;
                            auto dst = link->m_inputPlug->plug_base.m_parentNode;
                            if (!dst) continue;
                            m[dst->idx_in_nodes].push_back({ src->idx_in_nodes, pname, (uint32_t)plug_hash });
                        }
                    }
                }
            }
        }
        __except (EXCEPTION_EXECUTE_HANDLER) {}
        return m;
    }

    static nlohmann::json extract_node(StatescriptInstance* inst, STUStatescriptBase* node,
                                       const std::vector<IncomingLink>* incoming) {
        nlohmann::json j;
        j["idx"] = node->idx_in_nodes;
        j["type"] = node_type_str(node->get_type());

        auto stu = node->graph_node.base.vfptr->GetSTUInfo();
        std::string stype = resolve_stu_type(stu);
        j["stu_type"] = stype;
        if (stype.rfind("0x", 0) != 0) {
            j["stu_type_hash"] = std::format("0x{:08x}", stu->Hash);
        }

        // Editor labels (often stripped in release; emit only when present and printable).
        if (auto s = read_editor_str(node->graph_node.m_displayName)) j["display_name"] = *s;
        if (auto s = read_editor_str(node->graph_node.m_comment))     j["comment"]      = *s;
        if (auto s = read_editor_str(node->m_debugText))              j["debug_text"]   = *s;

        // Flags — only emit when true to keep the JSON terse.
        nlohmann::json flags = nlohmann::json::object();
        if (node->m_isState)     flags["is_state"]     = true;
        if (node->m_clientOnly)  flags["client_only"]  = true;
        if (node->m_serverOnly)  flags["server_only"]  = true;
        if (!flags.empty()) j["flags"] = flags;

        // Outputs and links
        nlohmann::json outputs = nlohmann::json::array();
        __try {
            auto connections = node->get_output_plugs();
            for (auto& [plug_hash, plugs] : connections) {
                nlohmann::json plug_entry;
                std::string pname = resolve_hash((uint32_t)plug_hash);
                plug_entry["plug_name"] = pname;
                if (pname.rfind("0x", 0) != 0) {
                    plug_entry["plug_name_hash"] = std::format("0x{:08x}", (uint32_t)plug_hash);
                }

                nlohmann::json links = nlohmann::json::array();
                for (auto* plug : plugs) {
                    if (!plug) continue;
                    for (auto* link : plug->m_links) {
                        if (!link || !link->m_inputPlug) continue;
                        auto target = link->m_inputPlug->plug_base.m_parentNode;
                        if (!target) continue;
                        nlohmann::json l;
                        l["to_node_idx"] = target->idx_in_nodes;
                        l["to_node_type"] = node_type_str(target->get_type());
                        links.push_back(l);
                    }
                }
                plug_entry["links"] = links;
                outputs.push_back(plug_entry);
            }
        }
        __except (EXCEPTION_EXECUTE_HANDLER) {}
        j["outputs"] = outputs;

        // Derived: incoming links (so analyzers don't have to scan every node).
        if (incoming && !incoming->empty()) {
            nlohmann::json inc = nlohmann::json::array();
            for (auto& il : *incoming) {
                nlohmann::json e;
                e["from_node_idx"] = il.from_idx;
                e["via_plug"] = il.plug_name;
                inc.push_back(e);
            }
            j["incoming_links"] = inc;
        }

        // Config vars (arguments on the node that resolve to STUConfigVar)
        nlohmann::json config_vars = nlohmann::json::array();
        __try {
            for (auto [info, arg] : stu->RangeArgs()) {
                if (!arg || !arg->Constraint) continue;
                // Skip args inherited from the base statescript types — they're noise.
                if (info->Hash == STU_NAME::STUStatescriptState ||
                    info->Hash == STU_NAME::STUStatescriptAction) {
                    continue;
                }
                if (arg->Constraint->ToConstraintType() != STU_ConstraintType_Object) continue;

                auto arg_typ = STURegistry::Get()->GetSTUInfoByHash(arg->Constraint->GetSTUType());
                if (!arg_typ || !arg_typ->AssignableToHash(STU_NAME::STUConfigVar)) continue;

                STUConfigVar* cv = *(STUConfigVar**)((__int64)node + arg->Offset);
                if (!cv) continue;

                nlohmann::json cv_j;
                if (arg->Name_str && arg->Name_str[0]) {
                    cv_j["arg_name"] = arg->Name_str;
                }
                else {
                    // Release builds strip Name_str — fall back to the curated table +
                    // hashlibrary (resolve_hash returns a "0x..." hex string when unknown).
                    std::string rn = resolve_hash(arg->Hash);
                    if (rn.rfind("0x", 0) != 0)
                        cv_j["arg_name"] = rn;
                }
                cv_j["arg_hash"] = std::format("0x{:08x}", arg->Hash);
                auto extracted = extract_config_var(cv, inst);
                for (auto& [k, v] : extracted.items()) cv_j[k] = v;
                config_vars.push_back(cv_j);
            }
        }
        __except (EXCEPTION_EXECUTE_HANDLER) {}
        if (!config_vars.empty()) j["config_vars"] = config_vars;

        return j;
    }

    static nlohmann::json extract_script(StatescriptInstance* inst) {
        nlohmann::json j;
        j["script_id"] = std::format("0x{:016x}", inst->script_id);
        if (auto name = known_script_name(inst->script_id))
            j["name_hint"] = name;
        if (auto kind = resource_kind((uint64_t)inst->script_id))
            j["script_id_kind"] = kind;
        j["source"] = "instance";

        __try {
            if (inst->ss_inner && inst->ss_inner->component_ref &&
                inst->ss_inner->component_ref->base.entity_backref) {
                j["entity_id"] = std::format("0x{:x}",
                    inst->ss_inner->component_ref->base.entity_backref->entity_id);
            }
            if (inst->ss_inner) j["ea_scope"] = inst->ss_inner->entity_admin_scope;
        }
        __except (EXCEPTION_EXECUTE_HANDLER) {}

        auto graph = inst->graph;
        j["node_count"] = graph->m_nodes.count();
        j["state_count"] = graph->m_states.count();
        j["entry_count"] = graph->m_entries.count();

        // Build incoming-link map once for this script.
        auto inc_map = build_incoming_map(inst->graph);

        nlohmann::json nodes = nlohmann::json::array();
        for (auto* node : graph->m_nodes) {
            if (!node) continue;
            __try {
                const std::vector<IncomingLink>* incoming = nullptr;
                auto it = inc_map.find(node->idx_in_nodes);
                if (it != inc_map.end()) incoming = &it->second;
                nodes.push_back(extract_node(inst, node, incoming));
            }
            __except (EXCEPTION_EXECUTE_HANDLER) {}
        }
        j["nodes"] = nodes;

        return j;
    }

    // Dump a statescript GRAPH directly from a loaded STU resource — no instance
    // required. Captures uninstantiated scripts (e.g. server-side gamemode logic)
    // whose graph resource is resident in memory. Authored config-var values only;
    // no live instance variables, entity context, or runtime link state.
    static nlohmann::json extract_graph(STUStatescriptGraph* graph, __int64 resource_id) {
        nlohmann::json j;
        j["script_id"] = std::format("0x{:016x}", resource_id);
        if (auto name = known_script_name(resource_id)) j["name_hint"] = name;
        if (auto kind = resource_kind((uint64_t)resource_id)) j["script_id_kind"] = kind;
        j["source"] = "graph";

        j["node_count"]  = graph->m_nodes.count();
        j["state_count"] = graph->m_states.count();
        j["entry_count"] = graph->m_entries.count();

        auto inc_map = build_incoming_map(graph);

        nlohmann::json nodes = nlohmann::json::array();
        for (auto* node : graph->m_nodes) {
            if (!node) continue;
            __try {
                const std::vector<IncomingLink>* incoming = nullptr;
                auto it = inc_map.find(node->idx_in_nodes);
                if (it != inc_map.end()) incoming = &it->second;
                nodes.push_back(extract_node(nullptr, node, incoming)); // no instance
            }
            __except (EXCEPTION_EXECUTE_HANDLER) {}
        }
        j["nodes"] = nodes;
        return j;
    }

    // Capture every loaded script from one EA into the session cache.
    // Latest snapshot wins for a given script_id.
    void scan_ea_into_cache(EntityAdminBase* ea, const char* ea_name) {
        if (!ea) return;
        auto ss_comp = ea->getSingletonComponent<Component_23_Statescript>(0x23);
        if (!ss_comp) return;

        char ts_buf[64];
        time_t now_t = time(nullptr);
        struct tm tm_buf;
        localtime_s(&tm_buf, &now_t);
        strftime(ts_buf, sizeof(ts_buf), "%Y-%m-%dT%H:%M:%S", &tm_buf);

        for (int i = 0; i < ss_comp->ss_inner.g1_instanceArr.num; i++) {
            auto inst = ss_comp->ss_inner.g1_instanceArr.ptr[i];
            if (!inst || !inst->graph) continue;
            __try {
                nlohmann::json j = extract_script(inst);
                j["captured_from"] = ea_name;
                j["captured_at"] = ts_buf;
                _session_cache[inst->script_id] = std::move(j);
            }
            __except (EXCEPTION_EXECUTE_HANDLER) {}
        }
    }

    // Enumerate EVERY loaded STUStatescriptGraph resource (instantiated or not) via
    // the STU resource tracker and gap-fill the session cache with graph-sourced
    // dumps. Instance captures are richer (entity/EA/live values), so we never
    // overwrite an instance entry with a graph one — only fill script_ids we haven't
    // seen instantiated this session.
    void scan_all_graphs_into_cache() {
        auto graphs = stu_resources::GetIfAssignableTo(stringHash("STUStatescriptGraph"));
        _last_graph_count = (int)graphs.size();

        char ts_buf[64];
        time_t now_t = time(nullptr);
        struct tm tm_buf;
        localtime_s(&tm_buf, &now_t);
        strftime(ts_buf, sizeof(ts_buf), "%Y-%m-%dT%H:%M:%S", &tm_buf);

        for (auto& [resid, stubase] : graphs) {
            if (!stubase) continue;
            if (_session_cache.count(resid) && _session_cache[resid].value("source", "") != "graph")
                continue; // keep the richer instance capture
            __try {
                auto graph = (STUStatescriptGraph*)stubase;
                nlohmann::json j = extract_graph(graph, resid);
                j["captured_from"] = "graph";
                j["captured_at"] = ts_buf;
                _session_cache[resid] = std::move(j);
            }
            __except (EXCEPTION_EXECUTE_HANDLER) {}
        }
    }

    // Cheap check: did the instance count change on either EA we care about?
    // If yes, do a full scan. Called periodically from render().
    void maybe_auto_scan() {
        if (!_auto_scan) return;
        if (++_frames_since_scan < _scan_interval_frames) return;
        _frames_since_scan = 0;

        EntityAdminBase* lobby = _include_lobby ? LobbyEntityAdmin() : nullptr;
        EntityAdminBase* game = _include_game ? GameEntityAdmin() : nullptr;

        int lobby_count = -1, game_count = -1;
        if (lobby) {
            auto c = lobby->getSingletonComponent<Component_23_Statescript>(0x23);
            if (c) lobby_count = c->ss_inner.g1_instanceArr.num;
        }
        if (game) {
            auto c = game->getSingletonComponent<Component_23_Statescript>(0x23);
            if (c) game_count = c->ss_inner.g1_instanceArr.num;
        }

        if (lobby_count != _last_lobby_count || game_count != _last_game_count) {
            if (lobby) scan_ea_into_cache(lobby, "lobby");
            if (game) scan_ea_into_cache(game, "game");
            _last_lobby_count = lobby_count;
            _last_game_count = game_count;
            _scan_count++;
        }
    }

    // Force a full rescan regardless of count change. Useful when scripts
    // mutate in place (rare) or for a manual refresh.
    void force_scan() {
        if (_include_lobby) scan_ea_into_cache(LobbyEntityAdmin(), "lobby");
        if (_include_game) scan_ea_into_cache(GameEntityAdmin(), "game");
        _scan_count++;
    }

    // Read existing _all.json into a script_id → json map (merge target).
    static std::map<__int64, nlohmann::json> read_existing_combined() {
        std::map<__int64, nlohmann::json> out;
        std::filesystem::path p = "./statescript_dump/_all.json";
        std::error_code ec;
        if (!std::filesystem::exists(p, ec)) return out;
        try {
            std::ifstream in(p);
            nlohmann::json existing;
            in >> existing;
            if (existing.contains("scripts") && existing["scripts"].is_array()) {
                for (auto& s : existing["scripts"]) {
                    if (!s.contains("script_id")) continue;
                    std::string idstr = s["script_id"];
                    // Format: "0x05800000000001e5"
                    __int64 id = (__int64)strtoull(idstr.c_str() + 2, nullptr, 16);
                    if (id != 0) out[id] = s;
                }
            }
        }
        catch (...) {
            LOG_CORE(Warn, "[StatescriptDumper] failed to parse existing _all.json; treating as empty");
        }
        return out;
    }

    void do_dump() {
        _stats = {};
        std::filesystem::create_directories("./statescript_dump");

        // Force a fresh scan so the cache reflects current memory state.
        force_scan();
        // Also capture every loaded graph (incl. uninstantiated server-side scripts).
        if (_include_graphs) scan_all_graphs_into_cache();

        // Merge: existing on-disk dump + session cache (cache wins on overlap), but
        // never let a graph-sourced entry clobber a richer instance dump on disk.
        auto merged = read_existing_combined();
        int from_disk = (int)merged.size();
        int from_session = 0;
        int new_this_dump = 0;
        for (auto& [id, j] : _session_cache) {
            bool incoming_graph = j.value("source", "") == "graph";
            if (incoming_graph && merged.count(id) && merged[id].value("source", "") != "graph")
                continue; // existing instance dump is richer — keep it
            if (!merged.count(id)) new_this_dump++;
            merged[id] = j;
            from_session++;
        }

        auto now = std::chrono::system_clock::now();
        auto time_t_now = std::chrono::system_clock::to_time_t(now);
        struct tm tm_buf;
        localtime_s(&tm_buf, &time_t_now);
        char time_str[64];
        strftime(time_str, sizeof(time_str), "%Y-%m-%dT%H:%M:%S", &tm_buf);

        nlohmann::json index;
        index["tool"] = "Prometheus Statescript Dumper";
        index["build"] = 24919;
        index["timestamp"] = time_str;
        index["scripts"] = nlohmann::json::array();

        nlohmann::json combined;
        combined["tool"] = "Prometheus Statescript Dumper";
        combined["build"] = 24919;
        combined["timestamp"] = time_str;
        combined["notes"] = "Expression opcode reference: see Prometheus-Core/Statescript.h lines 380-449";
        combined["scripts"] = nlohmann::json::array();

        for (auto& [id, script_j] : merged) {
            // Per-script file
            char filename[256];
            sprintf_s(filename, "./statescript_dump/%llx.json", id);
            std::ofstream f(filename, std::ios::trunc);
            f << script_j.dump(2);
            f.close();

            // Index entry
            nlohmann::json idx_entry;
            if (script_j.contains("script_id")) idx_entry["script_id"] = script_j["script_id"];
            if (script_j.contains("captured_from")) idx_entry["captured_from"] = script_j["captured_from"];
            if (script_j.contains("captured_at")) idx_entry["captured_at"] = script_j["captured_at"];
            if (script_j.contains("node_count")) idx_entry["node_count"] = script_j["node_count"];
            if (script_j.contains("entry_count")) idx_entry["entry_count"] = script_j["entry_count"];
            if (script_j.contains("state_count")) idx_entry["state_count"] = script_j["state_count"];
            if (script_j.contains("name_hint")) idx_entry["name_hint"] = script_j["name_hint"];
            if (script_j.contains("script_id_kind")) idx_entry["script_id_kind"] = script_j["script_id_kind"];
            if (script_j.contains("source")) idx_entry["source"] = script_j["source"];
            index["scripts"].push_back(idx_entry);

            // Combined
            combined["scripts"].push_back(script_j);
        }

        {
            std::ofstream f("./statescript_dump/_index.json", std::ios::trunc);
            f << index.dump(2);
        }
        {
            std::ofstream f("./statescript_dump/_all.json", std::ios::trunc);
            f << combined.dump(2);
        }

        _stats.unique_scripts = (int)merged.size();
        _stats.dumped = (int)merged.size();
        _status = std::format("Dumped {} total scripts ({} new this dump, {} session-cached, {} from prior runs)",
            (int)merged.size(), new_this_dump, from_session, from_disk);
        _status_color = ImVec4(0.4f, 0.9f, 0.4f, 1.0f);
        LOG_CORE(Info, "[StatescriptDumper] {}", _status);
    }

public:
    // Used by the MCP dump_script tool.
    static nlohmann::json dump_instance(StatescriptInstance* inst) { return extract_script(inst); }

    void render() override {
        maybe_auto_scan();

        if (open_window(nullptr, 0, ImVec2(620, 420))) {
            ImGui::TextWrapped("Continuously captures loaded statescripts into an in-memory cache. "
                "'Dump All' merges the cache with any existing ./statescript_dump/_all.json on disk and "
                "writes everything back. Scripts persist across game restarts (disk merge) and within a "
                "session even after they unload (cache). Navigate to different maps/menus and dump "
                "periodically to accumulate full coverage.");

            ImGui::Separator();
            ImGui::Text("Sources:");
            ImGui::Checkbox("Lobby Entity Admin", &_include_lobby);
            ImGui::SameLine();
            ImGui::Checkbox("Game Entity Admin", &_include_game);
            ImGui::Checkbox("All loaded graphs (incl. uninstantiated / server-side)", &_include_graphs);
            ImGui::SameLine();
            ImGui::TextDisabled("(?)");
            if (ImGui::IsItemHovered())
                ImGui::SetTooltip("Dumps every resident STUStatescriptGraph, not just instantiated ones.\n"
                    "Captures gamemode/objective scripts that never spawn without a server.\n"
                    "Graph dumps have authored values only (no live instance variables).");

            ImGui::Separator();
            ImGui::Text("Auto-scan:");
            ImGui::Checkbox("Enabled##autoscan", &_auto_scan);
            ImGui::SameLine();
            ImGui::SetNextItemWidth(80);
            ImGui::InputInt("interval (frames)##scanint", &_scan_interval_frames, 0, 0);
            if (_scan_interval_frames < 1) _scan_interval_frames = 1;

            ImGui::Text("Session cache: %d scripts captured | scans this session: %d",
                (int)_session_cache.size(), _scan_count);
            if (_last_graph_count >= 0)
                ImGui::Text("Loaded graphs at last scan: %d (vs instances above = uninstantiated trove)",
                    _last_graph_count);

            ImGui::Separator();

            bool can_dump = _include_lobby || _include_game;
            if (!can_dump) ImGui::BeginDisabled();
            if (ImGui::Button("Dump All (merge with disk)")) {
                do_dump();
            }
            if (!can_dump) ImGui::EndDisabled();

            ImGui::SameLine();
            if (ImGui::Button("Force Scan Now")) {
                force_scan();
            }

            ImGui::SameLine();
            if (ImGui::Button("Scan Loaded Graphs")) {
                scan_all_graphs_into_cache();
                _status = std::format("Graph scan: {} loaded graphs; session cache now {} scripts",
                    _last_graph_count, (int)_session_cache.size());
                _status_color = ImVec4(0.4f, 0.9f, 0.4f, 1.0f);
            }

            ImGui::SameLine();
            if (ImGui::Button("Open Folder")) {
                char cwd[MAX_PATH];
                GetCurrentDirectoryA(MAX_PATH, cwd);
                std::string path = std::string(cwd) + "\\statescript_dump";
                ShellExecuteA(NULL, "explore", path.c_str(), NULL, NULL, SW_SHOWNORMAL);
            }

            ImGui::SameLine();
            if (ImGui::Button("Clear Session Cache")) {
                _session_cache.clear();
                _last_lobby_count = -1;
                _last_game_count = -1;
            }

            if (!_status.empty()) {
                ImGui::Separator();
                ImGui::TextColored(_status_color, "%s", _status.c_str());
            }
        }
        ImGui::End();
    }
};

// Included by MCP/McpTools.cpp too, which defines this so the window is registered once.
#ifndef STATESCRIPT_DUMPER_NO_REGISTER
WINDOW_REGISTER(statescript_dumper);
#endif
