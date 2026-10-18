#pragma once
#include "../window_manager/window_manager.h"
#include "../../globals.h"
#include "crc32_discovery.h"
#include <MinHook.h>
#include <imgui.h>
#include <vector>
#include <string>
#include <algorithm>
#include <ctime>
#include <functional>
#include <sstream>

// Game's internal string type (SSO - Small String Optimization).
// Capacity <= 0x10 uses the inline buffer; > 0x10 uses the heap pointer.
struct GameString {
    union {
        char buf[16];
        char* ptr;
    };
    uint32_t size;
    uint32_t capacity;

    const char* get() const {
        return (capacity > 0x10) ? ptr : buf;
    }
};

// Console command entry as stored in the hash table at RVA 0x181E600.
struct ConsoleCommand {
    char _pad[0xA8];

    void*& func_ptr() { return *(void**)((char*)this + 0x00); }
    GameString& name() { return *(GameString*)((char*)this + 0x08); }
    GameString& usage() { return *(GameString*)((char*)this + 0x48); }
    uint32_t& hash() { return *(uint32_t*)((char*)this + 0x88); }
    uint8_t& min_args() { return *(uint8_t*)((char*)this + 0x98); }
    uint8_t& max_args() { return *(uint8_t*)((char*)this + 0x99); }
    ConsoleCommand*& next() { return *(ConsoleCommand**)((char*)this + 0xA0); }
};

struct CommandLogEntry {
    std::time_t timestamp;
    std::string command;
    int argc;
    std::string result;
};

class console_debug : public window {
    WINDOW_DEFINE(console_debug, "Debug", "Console Debug", true);

private:
    static inline uint64_t (*ConsoleCommandHandler_orig)(int argc, uint64_t* argv) = nullptr;
    static inline void (*ConsoleCheatCommandCheck_orig)(int64_t* param_1, int64_t* param_2) = nullptr;

    static inline bool g_bypassCheatCheck = false;
    static inline std::vector<CommandLogEntry> g_commandLog;
    static inline bool g_autoScrollLog = true;
    static inline ImGuiTextFilter g_commandFilter;
    static inline int g_maxLogEntries = 1000;

    std::vector<ConsoleCommand*> m_cachedCommands;
    bool m_needsRefresh = true;

    static constexpr uint64_t RVA_ConsoleCommandHandler = 0x7E6160;
    static constexpr uint64_t RVA_ConsoleCheatCommandCheck = 0xC70A40;
    static constexpr uint64_t RVA_ConsoleCheatCommandExecute = 0xC70840;
    static constexpr uint64_t RVA_CommandHashTable = 0x181E600;
    static constexpr uint64_t RVA_CommandCount = 0x181EE00;
    static constexpr uint64_t RVA_ConsolePrintFormatted = 0x7E5EA0;

    static void consolePrint(const char* fmt, ...) {
        va_list args;
        va_start(args, fmt);
        char buf[1024];
        vsnprintf(buf, sizeof(buf), fmt, args);
        va_end(args);
        auto printDirect = (void(*)(int, const char*))(globals::gameBase + 0x7E5C90);
        printDirect(0, buf);
    }

    struct CustomCommand {
        std::string name;
        std::string usage;
        std::string description;
        std::function<bool(int argc, uint64_t* argv)> handler;
    };

    static inline std::vector<CustomCommand> g_customCommands;
    static inline bool g_commandsRegistered = false;

    struct ParsedArg {
        uint64_t value;
        std::string str_storage;
    };

    static std::vector<ParsedArg> parseCallArgs(int argc, uint64_t* argv, int start_index) {
        std::vector<ParsedArg> args;
        args.reserve(argc - start_index);
        for (int i = start_index; i < argc; i++) {
            const char* arg = (const char*)argv[i];
            if (!arg) continue;

            ParsedArg pa;

            if (arg[0] == '"') {
                pa.str_storage = std::string(arg + 1);
                if (!pa.str_storage.empty() && pa.str_storage.back() == '"') {
                    pa.str_storage.pop_back();
                }
                pa.value = 0;
            } else if (arg[0] == 's' && arg[1] == ':') {
                pa.str_storage = std::string(arg + 2);
                pa.value = 0;
            } else {
                char* end = nullptr;
                uint64_t val = strtoull(arg, &end, 0);
                if (end && *end == '\0') {
                    pa.value = val;
                } else {
                    pa.str_storage = arg;
                    pa.value = 0;
                }
            }
            args.push_back(std::move(pa));
        }
        // Fix up string pointers AFTER all moves complete (SSO buffer addr changes on move).
        for (auto& a : args) {
            if (!a.str_storage.empty()) {
                a.value = (uint64_t)a.str_storage.c_str();
            }
        }
        return args;
    }

    static void registerCustomCommands() {
        if (g_commandsRegistered) return;
        g_commandsRegistered = true;

        g_customCommands.push_back({"help", "help [command]", "List all Prometheus commands", [](int argc, uint64_t* argv) -> bool {
            consolePrint("--- Prometheus Commands ---");
            for (const auto& cmd : g_customCommands) {
                consolePrint("  %-20s %s", cmd.name.c_str(), cmd.description.c_str());
            }
            consolePrint("--- %d commands available ---", (int)g_customCommands.size());
            return true;
        }});

        g_customCommands.push_back({"echo", "echo <text>", "Print text to console", [](int argc, uint64_t* argv) -> bool {
            if (argc < 2) {
                consolePrint("Usage: echo <text>");
                return false;
            }
            std::string msg;
            for (int i = 1; i < argc; i++) {
                if (i > 1) msg += " ";
                msg += (const char*)argv[i];
            }
            consolePrint("%s", msg.c_str());
            return true;
        }});

        g_customCommands.push_back({"prometheus", "prometheus", "Show Prometheus info", [](int argc, uint64_t* argv) -> bool {
            consolePrint("Prometheus - Overwatch Beta 0.8 Research Tool");
            consolePrint("  Game base: 0x%llX", globals::gameBase);
            consolePrint("  DLL base:  0x%llX", globals::modBase);
            consolePrint("  Custom commands: %d", (int)g_customCommands.size());
            consolePrint("  Discovered hashes: %d", (int)crc32_discovery::getDiscoveredCommands().size());
            return true;
        }});

        g_customCommands.push_back({"bypass_cheat", "bypass_cheat [0|1]", "Toggle cheat command bypass", [](int argc, uint64_t* argv) -> bool {
            if (argc >= 2) {
                const char* val = (const char*)argv[1];
                g_bypassCheatCheck = (val[0] == '1' || val[0] == 't' || val[0] == 'T');
            } else {
                g_bypassCheatCheck = !g_bypassCheatCheck;
            }
            consolePrint("Cheat bypass: %s", g_bypassCheatCheck ? "ENABLED" : "DISABLED");
            return true;
        }});

        g_customCommands.push_back({"clear", "clear", "Clear the command log", [](int argc, uint64_t* argv) -> bool {
            g_commandLog.clear();
            consolePrint("Log cleared.");
            return true;
        }});

        g_customCommands.push_back({"discovered", "discovered [filter]", "List discovered CRC32 hash entries", [](int argc, uint64_t* argv) -> bool {
            const auto& discovered = crc32_discovery::getDiscoveredCommands();
            const char* filter = (argc >= 2) ? (const char*)argv[1] : nullptr;
            int count = 0;
            for (const auto& dc : discovered) {
                if (filter) {
                    std::string name_lower = dc.name;
                    std::string filter_lower = filter;
                    std::transform(name_lower.begin(), name_lower.end(), name_lower.begin(), ::tolower);
                    std::transform(filter_lower.begin(), filter_lower.end(), filter_lower.begin(), ::tolower);
                    if (name_lower.find(filter_lower) == std::string::npos) continue;
                }
                consolePrint("  %-40s %08X", dc.name.c_str(), dc.hash);
                count++;
            }
            consolePrint("--- %d entries%s ---", count, filter ? " (filtered)" : "");
            return true;
        }});

        g_customCommands.push_back({"gamebase", "gamebase", "Print game module base address", [](int argc, uint64_t* argv) -> bool {
            consolePrint("Game base: 0x%llX", globals::gameBase);
            return true;
        }});

        g_customCommands.push_back({"read", "read <rva> [count]", "Read bytes at RVA offset from game base", [](int argc, uint64_t* argv) -> bool {
            if (argc < 2) {
                consolePrint("Usage: read <rva_hex> [byte_count]");
                return false;
            }
            uint64_t rva = strtoull((const char*)argv[1], nullptr, 16);
            int count = (argc >= 3) ? atoi((const char*)argv[2]) : 16;
            if (count > 256) count = 256;
            if (count < 1) count = 1;

            uint8_t* addr = (uint8_t*)(globals::gameBase + rva);
            if (IsBadReadPtr(addr, count)) {
                consolePrint("Cannot read %d bytes at RVA 0x%llX", count, rva);
                return false;
            }

            for (int i = 0; i < count; i += 16) {
                char hex[64] = {0};
                char ascii[20] = {0};
                int len = 0;
                for (int j = 0; j < 16 && (i + j) < count; j++) {
                    uint8_t b = addr[i + j];
                    len += snprintf(hex + len, sizeof(hex) - len, "%02X ", b);
                    ascii[j] = (b >= 0x20 && b < 0x7F) ? (char)b : '.';
                }
                consolePrint("  %08llX: %-48s %s", rva + i, hex, ascii);
            }
            return true;
        }});

        g_customCommands.push_back({"write", "write <rva> <byte1> [byte2] ...", "Write bytes at RVA offset from game base", [](int argc, uint64_t* argv) -> bool {
            if (argc < 3) {
                consolePrint("Usage: write <rva_hex> <byte1_hex> [byte2_hex] ...");
                consolePrint("  Example: write 7E5C90 90 90 90  (writes 3 NOP bytes)");
                return false;
            }
            uint64_t rva = strtoull((const char*)argv[1], nullptr, 16);

            uint8_t* addr = (uint8_t*)(globals::gameBase + rva);
            if (IsBadReadPtr(addr, 1)) {
                consolePrint("Invalid address: RVA 0x%llX", rva);
                return false;
            }

            std::vector<uint8_t> bytes;
            for (int i = 2; i < argc; i++) {
                uint8_t b = (uint8_t)strtoul((const char*)argv[i], nullptr, 16);
                bytes.push_back(b);
            }

            DWORD oldProtect;
            if (!VirtualProtect(addr, bytes.size(), PAGE_EXECUTE_READWRITE, &oldProtect)) {
                consolePrint("VirtualProtect failed at RVA 0x%llX (error %d)", rva, GetLastError());
                return false;
            }

            memcpy(addr, bytes.data(), bytes.size());
            VirtualProtect(addr, bytes.size(), oldProtect, &oldProtect);

            consolePrint("Wrote %d bytes at RVA 0x%llX", (int)bytes.size(), rva);

            char hex[256] = {0};
            int len = 0;
            for (size_t i = 0; i < bytes.size() && len < 240; i++) {
                len += snprintf(hex + len, sizeof(hex) - len, "%02X ", bytes[i]);
            }
            consolePrint("  %s", hex);
            return true;
        }});

        g_customCommands.push_back({"call", "call <rva> [args...]", "Call function at RVA. Args: hex/dec numbers, s:string for strings", [](int argc, uint64_t* argv) -> bool {
            if (argc < 2) {
                consolePrint("Usage: call <rva_hex> [arg1] [arg2] ...");
                consolePrint("  Numbers: 0x1A or 26     Strings: s:hello");
                consolePrint("  Example: call 7E5C90 0 s:Hello!");
                consolePrint("  Returns uint64 result in hex.");
                return false;
            }

            uint64_t rva = strtoull((const char*)argv[1], nullptr, 16);
            void* fn_addr = (void*)(globals::gameBase + rva);

            if (IsBadReadPtr(fn_addr, 1)) {
                consolePrint("Invalid address: RVA 0x%llX", rva);
                return false;
            }

            auto args = parseCallArgs(argc, argv, 2);
            int nargs = (int)args.size();

            consolePrint("Calling RVA 0x%llX with %d args...", rva, nargs);

            uint64_t result = 0;
            switch (nargs) {
                case 0: { auto fn = (uint64_t(*)())fn_addr; result = fn(); break; }
                case 1: { auto fn = (uint64_t(*)(uint64_t))fn_addr; result = fn(args[0].value); break; }
                case 2: { auto fn = (uint64_t(*)(uint64_t, uint64_t))fn_addr; result = fn(args[0].value, args[1].value); break; }
                case 3: { auto fn = (uint64_t(*)(uint64_t, uint64_t, uint64_t))fn_addr; result = fn(args[0].value, args[1].value, args[2].value); break; }
                case 4: { auto fn = (uint64_t(*)(uint64_t, uint64_t, uint64_t, uint64_t))fn_addr; result = fn(args[0].value, args[1].value, args[2].value, args[3].value); break; }
                case 5: { auto fn = (uint64_t(*)(uint64_t, uint64_t, uint64_t, uint64_t, uint64_t))fn_addr; result = fn(args[0].value, args[1].value, args[2].value, args[3].value, args[4].value); break; }
                case 6: { auto fn = (uint64_t(*)(uint64_t, uint64_t, uint64_t, uint64_t, uint64_t, uint64_t))fn_addr; result = fn(args[0].value, args[1].value, args[2].value, args[3].value, args[4].value, args[5].value); break; }
                case 7: { auto fn = (uint64_t(*)(uint64_t, uint64_t, uint64_t, uint64_t, uint64_t, uint64_t, uint64_t))fn_addr; result = fn(args[0].value, args[1].value, args[2].value, args[3].value, args[4].value, args[5].value, args[6].value); break; }
                case 8: { auto fn = (uint64_t(*)(uint64_t, uint64_t, uint64_t, uint64_t, uint64_t, uint64_t, uint64_t, uint64_t))fn_addr; result = fn(args[0].value, args[1].value, args[2].value, args[3].value, args[4].value, args[5].value, args[6].value, args[7].value); break; }
                default:
                    consolePrint("Too many arguments (max 8)");
                    return false;
            }

            consolePrint("  Result: 0x%llX (%llu)", result, result);
            return true;
        }});

        g_customCommands.push_back({"callv", "callv <rva> [args...]", "Call void function at RVA. Same arg syntax as 'call'", [](int argc, uint64_t* argv) -> bool {
            if (argc < 2) {
                consolePrint("Usage: callv <rva_hex> [arg1] [arg2] ...");
                consolePrint("  Same as 'call' but treats function as void return.");
                return false;
            }

            uint64_t rva = strtoull((const char*)argv[1], nullptr, 16);
            void* fn_addr = (void*)(globals::gameBase + rva);

            if (IsBadReadPtr(fn_addr, 1)) {
                consolePrint("Invalid address: RVA 0x%llX", rva);
                return false;
            }

            auto args = parseCallArgs(argc, argv, 2);
            int nargs = (int)args.size();

            consolePrint("Calling (void) RVA 0x%llX with %d args...", rva, nargs);

            switch (nargs) {
                case 0: { auto fn = (void(*)())fn_addr; fn(); break; }
                case 1: { auto fn = (void(*)(uint64_t))fn_addr; fn(args[0].value); break; }
                case 2: { auto fn = (void(*)(uint64_t, uint64_t))fn_addr; fn(args[0].value, args[1].value); break; }
                case 3: { auto fn = (void(*)(uint64_t, uint64_t, uint64_t))fn_addr; fn(args[0].value, args[1].value, args[2].value); break; }
                case 4: { auto fn = (void(*)(uint64_t, uint64_t, uint64_t, uint64_t))fn_addr; fn(args[0].value, args[1].value, args[2].value, args[3].value); break; }
                case 5: { auto fn = (void(*)(uint64_t, uint64_t, uint64_t, uint64_t, uint64_t))fn_addr; fn(args[0].value, args[1].value, args[2].value, args[3].value, args[4].value); break; }
                case 6: { auto fn = (void(*)(uint64_t, uint64_t, uint64_t, uint64_t, uint64_t, uint64_t))fn_addr; fn(args[0].value, args[1].value, args[2].value, args[3].value, args[4].value, args[5].value); break; }
                case 7: { auto fn = (void(*)(uint64_t, uint64_t, uint64_t, uint64_t, uint64_t, uint64_t, uint64_t))fn_addr; fn(args[0].value, args[1].value, args[2].value, args[3].value, args[4].value, args[5].value, args[6].value); break; }
                case 8: { auto fn = (void(*)(uint64_t, uint64_t, uint64_t, uint64_t, uint64_t, uint64_t, uint64_t, uint64_t))fn_addr; fn(args[0].value, args[1].value, args[2].value, args[3].value, args[4].value, args[5].value, args[6].value, args[7].value); break; }
                default:
                    consolePrint("Too many arguments (max 8)");
                    return false;
            }

            consolePrint("  Done.");
            return true;
        }});

        g_customCommands.push_back({"debug", "debug <subcommand>", "Control debug overlays (fps/screen/list/read)", [](int argc, uint64_t* argv) -> bool {
            static constexpr uint64_t RVA_RenderSystemPtr = 0x181E3E0;
            static constexpr uint64_t OFF_DebugFlags = 0x950;
            static constexpr uint64_t OFF_ScreenListHead = 0x8F0;
            static constexpr uint64_t RVA_GameState = 0x18582EC;

            uint64_t* pRenderObj = (uint64_t*)(globals::gameBase + RVA_RenderSystemPtr);
            if (IsBadReadPtr(pRenderObj, 8) || *pRenderObj == 0) {
                consolePrint("Render system not initialized (ptr at RVA 0x%llX is null)", RVA_RenderSystemPtr);
                return false;
            }
            uint64_t renderObj = *pRenderObj;

            if (argc < 2) {
                uint32_t* flags = (uint32_t*)(renderObj + OFF_DebugFlags);
                uint32_t gameState = *(uint32_t*)(globals::gameBase + RVA_GameState);
                consolePrint("--- Debug Overlay Info ---");
                consolePrint("  Render object: 0x%llX", renderObj);
                consolePrint("  Game state: %u (need 4 or 5 for overlays)", gameState);
                consolePrint("  Debug flags: 0x%08X", *flags);
                consolePrint("  FPS counter: %s (bit 7)", (*flags & 0x80) ? "ON" : "OFF");
                consolePrint("  Screen bits: %u (bits 4-6)", (*flags >> 4) & 0x7);

                uint64_t node = *(uint64_t*)(renderObj + OFF_ScreenListHead);
                int count = 0;
                while (node != 0 && count < 32) {
                    if (IsBadReadPtr((void*)node, 0x28)) break;
                    uint8_t visible = *(uint8_t*)(node + 0x18);
                    float width = *(float*)(node + 0x10);
                    float height = *(float*)(node + 0x14);
                    uint64_t vtable = *(uint64_t*)node;
                    uint64_t vtableRva = vtable - globals::gameBase;
                    consolePrint("  Screen[%d]: vis=%d w=%.0f h=%.0f vtable=0x%llX", count, visible, width, height, vtableRva);
                    node = *(uint64_t*)(node + 0x20);
                    count++;
                }
                if (count == 0) consolePrint("  No debug screens registered in linked list");

                consolePrint("Subcommands: fps, screen <0-7>, show <idx>, hide <idx>");
                return true;
            }

            const char* sub = (const char*)argv[1];

            if (_stricmp(sub, "fps") == 0) {
                uint32_t* flags = (uint32_t*)(renderObj + OFF_DebugFlags);
                *flags ^= 0x80;
                consolePrint("FPS counter: %s", (*flags & 0x80) ? "ON" : "OFF");
                return true;
            }

            if (_stricmp(sub, "screen") == 0) {
                uint32_t* flags = (uint32_t*)(renderObj + OFF_DebugFlags);
                if (argc >= 3) {
                    int val = atoi((const char*)argv[2]) & 0x7;
                    *flags = (*flags & ~0x70) | (val << 4);
                    consolePrint("Debug screen bits set to %d (flags=0x%08X)", val, *flags);
                } else {
                    int cur = (*flags >> 4) & 0x7;
                    int next = (cur + 1) & 0x7;
                    *flags = (*flags & ~0x70) | (next << 4);
                    consolePrint("Debug screen bits: %d -> %d (flags=0x%08X)", cur, next, *flags);
                }
                return true;
            }

            if (_stricmp(sub, "show") == 0 || _stricmp(sub, "hide") == 0) {
                bool setVisible = (_stricmp(sub, "show") == 0);
                int targetIdx = (argc >= 3) ? atoi((const char*)argv[2]) : -1;

                uint64_t node = *(uint64_t*)(renderObj + OFF_ScreenListHead);
                int idx = 0;
                while (node != 0 && idx < 32) {
                    if (IsBadReadPtr((void*)node, 0x28)) break;
                    if (idx == targetIdx || targetIdx == -1) {
                        *(uint8_t*)(node + 0x18) = setVisible ? 1 : 0;
                        consolePrint("Screen[%d] visibility set to %d", idx, setVisible ? 1 : 0);
                    }
                    node = *(uint64_t*)(node + 0x20);
                    idx++;
                }
                if (targetIdx >= idx) consolePrint("Screen index %d out of range (0-%d)", targetIdx, idx - 1);
                return true;
            }

            if (_stricmp(sub, "read") == 0) {
                if (argc < 3) {
                    consolePrint("Usage: debug read <offset_hex> [count]");
                    return false;
                }
                uint64_t offset = strtoull((const char*)argv[2], nullptr, 16);
                int count = (argc >= 4) ? atoi((const char*)argv[3]) : 16;
                if (count > 256) count = 256;
                uint8_t* addr = (uint8_t*)(renderObj + offset);
                if (IsBadReadPtr(addr, count)) {
                    consolePrint("Cannot read at render+0x%llX", offset);
                    return false;
                }
                for (int i = 0; i < count; i += 16) {
                    char hex[64] = {0};
                    int len = 0;
                    for (int j = 0; j < 16 && (i + j) < count; j++) {
                        len += snprintf(hex + len, sizeof(hex) - len, "%02X ", addr[i + j]);
                    }
                    consolePrint("  render+%04llX: %s", offset + i, hex);
                }
                return true;
            }

            consolePrint("Unknown debug subcommand: %s", sub);
            consolePrint("Subcommands: fps, screen [0-7], show [idx], hide [idx], read <offset> [count]");
            return false;
        }});

        g_customCommands.push_back({"reada", "reada <addr> [count]", "Read bytes at absolute address", [](int argc, uint64_t* argv) -> bool {
            if (argc < 2) {
                consolePrint("Usage: reada <address_hex> [byte_count]");
                return false;
            }
            uint64_t addr_val = strtoull((const char*)argv[1], nullptr, 16);
            int count = (argc >= 3) ? atoi((const char*)argv[2]) : 16;
            if (count > 256) count = 256;
            if (count < 1) count = 1;

            uint8_t* addr = (uint8_t*)addr_val;
            if (IsBadReadPtr(addr, count)) {
                consolePrint("Cannot read %d bytes at 0x%llX", count, addr_val);
                return false;
            }

            for (int i = 0; i < count; i += 16) {
                char hex[64] = {0};
                char ascii[20] = {0};
                int len = 0;
                for (int j = 0; j < 16 && (i + j) < count; j++) {
                    uint8_t b = addr[i + j];
                    len += snprintf(hex + len, sizeof(hex) - len, "%02X ", b);
                    ascii[j] = (b >= 0x20 && b < 0x7F) ? (char)b : '.';
                }
                consolePrint("  %016llX: %-48s %s", addr_val + i, hex, ascii);
            }
            return true;
        }});
    }

    static bool dispatchCustomCommand(int argc, uint64_t* argv) {
        if (!argv || argc < 1 || !argv[0]) return false;

        const char* cmd_name = (const char*)argv[0];

        for (const auto& cmd : g_customCommands) {
            if (_stricmp(cmd_name, cmd.name.c_str()) == 0) {
                cmd.handler(argc, argv);
                return true;
            }
        }
        return false;
    }

    static uint64_t ConsoleCommandHandler_hook(int argc, uint64_t* argv) {
        if (!g_commandsRegistered) registerCustomCommands();

        if (dispatchCustomCommand(argc, argv)) {
            CommandLogEntry entry;
            entry.timestamp = std::time(nullptr);
            entry.argc = argc;
            entry.command = (argv && argv[0]) ? (const char*)argv[0] : "<null>";
            entry.result = "Prometheus";
            g_commandLog.push_back(entry);
            if (g_commandLog.size() > (size_t)g_maxLogEntries) {
                g_commandLog.erase(g_commandLog.begin());
            }
            return 1;
        }

        uint64_t result = 0;
        if (ConsoleCommandHandler_orig) {
            result = ConsoleCommandHandler_orig(argc, argv);
        }

        if (!argv) {
            return result;
        }

        CommandLogEntry entry;
        entry.timestamp = std::time(nullptr);
        entry.argc = argc;

        if (*argv) {
            const char* cmd_name = (const char*)*argv;
            entry.command = cmd_name ? cmd_name : "<null>";
        } else {
            entry.command = "<null argv>";
        }

        entry.result = (result == 0) ? "Not Found/Failed" : "Executed";

        g_commandLog.push_back(entry);
        if (g_commandLog.size() > (size_t)g_maxLogEntries) {
            g_commandLog.erase(g_commandLog.begin());
        }

        return result;
    }

    static void ConsoleCheatCommandCheck_hook(int64_t* param_1, int64_t* param_2) {
        if (g_bypassCheatCheck) {
            auto execute = (void(*)(int64_t*, int64_t*))(globals::gameBase + RVA_ConsoleCheatCommandExecute);
            execute(param_1 - 0x54, param_2);

            CommandLogEntry entry;
            entry.timestamp = std::time(nullptr);
            entry.command = "<cheat command>";
            entry.argc = 0;
            entry.result = "Bypass Enabled";

            g_commandLog.push_back(entry);
            if (g_commandLog.size() > (size_t)g_maxLogEntries) {
                g_commandLog.erase(g_commandLog.begin());
            }
            return;
        }

        if (ConsoleCheatCommandCheck_orig) {
            ConsoleCheatCommandCheck_orig(param_1, param_2);
        }
    }

    void refreshCommands() {
        m_cachedCommands.clear();

        if (globals::gameBase == 0) {
            m_needsRefresh = false;
            return;
        }

        ConsoleCommand** hashTable = (ConsoleCommand**)(globals::gameBase + RVA_CommandHashTable);

        if (IsBadReadPtr(hashTable, sizeof(void*) * 256)) {
            m_needsRefresh = false;
            return;
        }

        for (int bucket = 0; bucket < 256; bucket++) {
            ConsoleCommand* cmd = hashTable[bucket];

            while (cmd != nullptr) {
                if (IsBadReadPtr(cmd, sizeof(ConsoleCommand))) {
                    break;
                }

                m_cachedCommands.push_back(cmd);
                cmd = cmd->next();
            }
        }

        m_needsRefresh = false;
    }

public:
    inline void preStartInitialize() override {
        MH_VERIFY(MH_CreateHook(
            (PVOID)(globals::gameBase + RVA_ConsoleCommandHandler),
            (PVOID)ConsoleCommandHandler_hook,
            (PVOID*)&ConsoleCommandHandler_orig));
        MH_VERIFY(MH_EnableHook((PVOID)(globals::gameBase + RVA_ConsoleCommandHandler)));

        MH_VERIFY(MH_CreateHook(
            (PVOID)(globals::gameBase + RVA_ConsoleCheatCommandCheck),
            (PVOID)ConsoleCheatCommandCheck_hook,
            (PVOID*)&ConsoleCheatCommandCheck_orig));
        MH_VERIFY(MH_EnableHook((PVOID)(globals::gameBase + RVA_ConsoleCheatCommandCheck)));
    }

    inline void render() override {
        if (open_window()) {
            if (ImGui::CollapsingHeader("Controls", ImGuiTreeNodeFlags_DefaultOpen)) {
                ImGui::Checkbox("Bypass Cheat Command Check", &g_bypassCheatCheck);
                ImGui::SameLine();
                if (ImGui::Button("?")) {
                    ImGui::SetTooltip("Allows cheat commands to execute without server connection.\n"
                                     "WARNING: Commands may still have no effect without a server.");
                }

                if (ImGui::Button("Refresh Commands")) {
                    m_needsRefresh = true;
                }
                ImGui::SameLine();
                ImGui::Text("Registered Commands: %d", (int)m_cachedCommands.size());

                ImGui::Checkbox("Auto-scroll Log", &g_autoScrollLog);
                ImGui::SameLine();
                if (ImGui::Button("Clear Log")) {
                    g_commandLog.clear();
                }
            }

            ImGui::Separator();

            if (ImGui::CollapsingHeader("Registered Commands", ImGuiTreeNodeFlags_DefaultOpen)) {
                if (m_needsRefresh && globals::gameBase != 0) {
                    refreshCommands();
                }

                g_commandFilter.Draw("Filter");

                if (ImGui::BeginTable("CommandsTable", 7,
                    ImGuiTableFlags_ScrollY | ImGuiTableFlags_Sortable |
                    ImGuiTableFlags_Resizable | ImGuiTableFlags_BordersH |
                    ImGuiTableFlags_HighlightHoveredColumn | ImGuiTableFlags_RowBg,
                    ImVec2(0, 300))) {

                    ImGui::TableSetupColumn("Bucket", ImGuiTableColumnFlags_WidthFixed, 50.0f);
                    ImGui::TableSetupColumn("Name", ImGuiTableColumnFlags_WidthStretch);
                    ImGui::TableSetupColumn("Hash", ImGuiTableColumnFlags_WidthFixed, 80.0f);
                    ImGui::TableSetupColumn("Min", ImGuiTableColumnFlags_WidthFixed, 40.0f);
                    ImGui::TableSetupColumn("Max", ImGuiTableColumnFlags_WidthFixed, 40.0f);
                    ImGui::TableSetupColumn("Usage", ImGuiTableColumnFlags_WidthStretch);
                    ImGui::TableSetupColumn("Function", ImGuiTableColumnFlags_WidthFixed, 100.0f);
                    ImGui::TableSetupScrollFreeze(0, 1);
                    ImGui::TableHeadersRow();

                    for (auto* cmd : m_cachedCommands) {
                        const char* name = cmd->name().get();
                        if (!name || name[0] == '\0') continue;

                        if (!g_commandFilter.PassFilter(name)) continue;

                        ImGui::TableNextRow();

                        ImGui::TableNextColumn();
                        ImGui::Text("%d", cmd->hash() & 0xFF);

                        ImGui::TableNextColumn();
                        ImGui::Text("%s", name);

                        ImGui::TableNextColumn();
                        ImGui::Text("%08X", cmd->hash());

                        ImGui::TableNextColumn();
                        ImGui::Text("%d", cmd->min_args());

                        ImGui::TableNextColumn();
                        ImGui::Text("%d", cmd->max_args());

                        ImGui::TableNextColumn();
                        const char* usage = cmd->usage().get();
                        ImGui::TextWrapped("%s", usage ? usage : "");

                        ImGui::TableNextColumn();
                        uint64_t rva = (uint64_t)cmd->func_ptr() - globals::gameBase;
                        ImGui::Text("%08llX", rva);
                    }

                    ImGui::EndTable();
                }
            }

            ImGui::Separator();

            if (ImGui::CollapsingHeader("Command Execution Log", ImGuiTreeNodeFlags_DefaultOpen)) {
                if (ImGui::BeginTable("LogTable", 4,
                    ImGuiTableFlags_ScrollY | ImGuiTableFlags_Resizable |
                    ImGuiTableFlags_BordersH | ImGuiTableFlags_RowBg,
                    ImVec2(0, 200))) {

                    ImGui::TableSetupColumn("#", ImGuiTableColumnFlags_WidthFixed, 40.0f);
                    ImGui::TableSetupColumn("Command", ImGuiTableColumnFlags_WidthStretch);
                    ImGui::TableSetupColumn("Args", ImGuiTableColumnFlags_WidthFixed, 50.0f);
                    ImGui::TableSetupColumn("Result", ImGuiTableColumnFlags_WidthFixed, 150.0f);
                    ImGui::TableSetupScrollFreeze(0, 1);
                    ImGui::TableHeadersRow();

                    for (size_t i = 0; i < g_commandLog.size(); i++) {
                        const auto& entry = g_commandLog[i];

                        ImGui::TableNextRow();

                        ImGui::TableNextColumn();
                        ImGui::Text("%zu", i);

                        ImGui::TableNextColumn();
                        ImGui::Text("%s", entry.command.c_str());

                        ImGui::TableNextColumn();
                        ImGui::Text("%d", entry.argc);

                        ImGui::TableNextColumn();
                        if (entry.result == "Executed") {
                            ImGui::TextColored(ImVec4(0.0f, 1.0f, 0.0f, 1.0f), "%s", entry.result.c_str());
                        } else if (entry.result == "Prometheus") {
                            ImGui::TextColored(ImVec4(0.4f, 0.7f, 1.0f, 1.0f), "%s", entry.result.c_str());
                        } else if (entry.result == "Bypass Enabled") {
                            ImGui::TextColored(ImVec4(1.0f, 0.5f, 0.0f, 1.0f), "%s", entry.result.c_str());
                        } else {
                            ImGui::TextColored(ImVec4(1.0f, 0.0f, 0.0f, 1.0f), "%s", entry.result.c_str());
                        }
                    }

                    if (g_autoScrollLog && ImGui::GetScrollY() >= ImGui::GetScrollMaxY()) {
                        ImGui::SetScrollHereY(1.0f);
                    }

                    ImGui::EndTable();
                }
            }
        }
        ImGui::End();
    }
};

WINDOW_REGISTER(console_debug);
