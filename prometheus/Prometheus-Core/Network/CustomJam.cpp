#include "CustomJam.h"

#include <windows.h>
#include <MinHook.h>

#include <atomic>
#include <chrono>
#include <cstring>
#include <deque>
#include <format>
#include <mutex>
#include <string>
#include <vector>

#include "imgui.h"
#include "globals.h"
#include "../Logs/Logs.h"
#include "../MCP/McpTools.h"
#include "../Utility/SafeMemory.h"
#include "../Systems/ServerView.h"

// Layouts and functions: docs/JAM.md §13 (all RVAs relative to gameBase).
namespace Network::CustomJam
{
    namespace
    {
        // ---- game functions ------------------------------------------------------------------
        constexpr uintptr_t LobbyRegisterProtocols_Addr = 0xC66560; // FUN_7ff650c46560(lobbySys)
        constexpr uintptr_t RouteFindPool_Addr = 0xF26960;          // FUN_7ff650f06960(route, crc) -> pool
        constexpr uintptr_t TreeInsertU32_Addr = 0x8A27D0;          // FUN_7ff6508827d0(tree, &key) -> &value
        constexpr uintptr_t RouteAddInbound_Addr = 0xF26170;        // FUN_7ff650f06170(route, pool, 1, receiver)
        constexpr uintptr_t RouteAddEntry_Addr = 0xF264A0;          // FUN_7ff650f064a0(route, &entry, dir)
        constexpr uintptr_t JamLinkSend_Addr = 0xF28AF0;            // FUN_7ff650f08af0(link, msg)
        constexpr uintptr_t NullJamID_Addr = 0x18AFC18;             // DAT_7ff65188fc18 (16 bytes)

        using LobbyRegister_fn = void(__fastcall*)(int64_t lobbySys);
        using RouteFindPool_fn = int64_t(__fastcall*)(int64_t route, uint32_t crc);
        using TreeInsertU32_fn = int64_t*(__fastcall*)(int64_t tree, const uint32_t* key);
        using RouteAddInbound_fn = void(__fastcall*)(int64_t route, void* pool, int mode, void* receiver);
        using RouteAddEntry_fn = void(__fastcall*)(int64_t route, void* entry, uint32_t dir);
        using JamLinkSend_fn = uint64_t(__fastcall*)(int64_t link, void* msg);

        LobbyRegister_fn LobbyRegister_orig;

        template <class F> F game_fn(uintptr_t rva) { return (F)(globals::gameBase + rva); }

        // ---- engine structures ---------------------------------------------------------------
        // Engine string: len (bit 31 = growable), cap (bit 31 = inline buffer at +8), data.
        // Ours are fixed buffers: not growable, pointer form, so the decoder copies into them
        // (truncating at cap) and never touches the game heap.
        struct EngineString {
            uint32_t len;
            uint32_t cap;
            char* ptr;
        };
        static_assert(sizeof(EngineString) == 0x10);

        struct ItemInfo { // JamMessageItemInfo, 0x28 bytes
            const char* name;
            int32_t type;
            int32_t offset;
            int32_t size;
            int32_t array;
            const ItemInfo* nested;
            void* func;
        };
        static_assert(sizeof(ItemInfo) == 0x28);

        struct MessageInfo { // JamMessageInfo
            const char* name;
            const char* protocol;
            const ItemInfo* items;
            uint32_t crc; // protocol CRC (+0x18)
            uint16_t id;  // +0x1C
            uint16_t pad;
        };

        struct RouteEntry { // FUN_7ff650f064a0's argument
            uint32_t crc, base, max, flags, serverType, pad;
            EngineString name;
        };
        static_assert(offsetof(RouteEntry, name) == 0x18 && sizeof(RouteEntry) == 0x28);

        // Stock JamMessage base (0x68 bytes): fields start at +0x68.
        struct MessageBase {
            void** vtable;       // +0x00
            volatile long refs;  // +0x08
            uint32_t pad0;
            uint8_t peer[16];    // +0x10 JamID
            uint8_t channel;     // +0x20
            uint8_t pad1[0x1F];  // +0x21 .. +0x3F header fields
            uint32_t frameLen;   // +0x40
            uint32_t mode;       // +0x44
            uint64_t pad2;       // +0x48
            uint64_t list[2];    // +0x50
            void* pool;          // +0x60
        };
        static_assert(sizeof(MessageBase) == 0x68);

        // ---- the PrometheusLobby protocol ----------------------------------------------------
        constexpr const char* ProtocolName = "PrometheusLobby";
        constexpr uint32_t ProtocolCRC = 0xF4456D46; // 0xF0000000 | (crc32(name) & 0x0FFFFFFF)
        constexpr uint16_t IdBase = 32000, IdMax = 32016;
        constexpr uint16_t IdCall = 32000, IdResult = 32001, IdView = 32002;
        constexpr uint32_t ServerTypeLobby = 13;

        constexpr size_t ToolCap = 255;
        constexpr size_t ArgsCap = 60 * 1024;
        constexpr size_t ResultCap = 16 * 1024;
        constexpr size_t ViewBodyCap = 8 * 1024;

        struct CallMsg {
            MessageBase base;
            uint32_t seq;          // +0x68
            uint32_t pad;
            EngineString tool;     // +0x70
            EngineString args;     // +0x80
            char toolBuf[ToolCap + 1];
            char argsBuf[ArgsCap + 1];
        };
        static_assert(offsetof(CallMsg, seq) == 0x68 && offsetof(CallMsg, tool) == 0x70 && offsetof(CallMsg, args) == 0x80);

        struct ResultMsg {
            MessageBase base;
            uint32_t seq;          // +0x68
            uint8_t ok;            // +0x6C
            uint8_t pad[3];
            EngineString result;   // +0x70
            char resultBuf[ResultCap + 1];
        };
        static_assert(offsetof(ResultMsg, ok) == 0x6C && offsetof(ResultMsg, result) == 0x70);

        // PrometheusServerView (docs/JAM.md §13.8): the live server view, one per server tick.
        struct ViewMsg {
            MessageBase base;
            uint32_t seq;          // +0x68
            uint32_t frame;        // +0x6C
            uint64_t sentUs;       // +0x70
            uint64_t relayUs;      // +0x78
            EngineString body;     // +0x80
            char bodyBuf[ViewBodyCap + 1];
            int64_t arrivalUs;     // DLL only: stamped by the receiver (network thread)
        };
        static_assert(offsetof(ViewMsg, frame) == 0x6C && offsetof(ViewMsg, sentUs) == 0x70 &&
                      offsetof(ViewMsg, relayUs) == 0x78 && offsetof(ViewMsg, body) == 0x80);

        constexpr ItemInfo CallItems[] = {
            {"seq", 6, 0x68, 4, 0, nullptr, nullptr},
            {"tool", 12, 0x70, 16, 0, nullptr, nullptr},
            {"args", 12, 0x80, 16, 0, nullptr, nullptr},
            {nullptr, 17, 0, 0, 0, nullptr, nullptr},
        };
        constexpr ItemInfo ResultItems[] = {
            {"seq", 6, 0x68, 4, 0, nullptr, nullptr},
            {"ok", 0, 0x6C, 1, 0, nullptr, nullptr},
            {"result", 12, 0x70, 16, 0, nullptr, nullptr},
            {nullptr, 17, 0, 0, 0, nullptr, nullptr},
        };
        constexpr ItemInfo ViewItems[] = {
            {"seq", 6, 0x68, 4, 0, nullptr, nullptr},
            {"frame", 6, 0x6C, 4, 0, nullptr, nullptr},
            {"sent_us", 8, 0x70, 8, 0, nullptr, nullptr},
            {"relay_us", 8, 0x78, 8, 0, nullptr, nullptr},
            {"body", 12, 0x80, 16, 0, nullptr, nullptr},
            {nullptr, 17, 0, 0, 0, nullptr, nullptr},
        };
        const MessageInfo ViewInfo{"PrometheusServerView", ProtocolName, ViewItems, ProtocolCRC, IdView, 0};
        const MessageInfo CallInfo{"PrometheusCall", ProtocolName, CallItems, ProtocolCRC, IdCall, 0};
        const MessageInfo ResultInfo{"PrometheusResult", ProtocolName, ResultItems, ProtocolCRC, IdResult, 0};

        // The message base is the first member, so a MessageBase* is the message's address.
        CallMsg* as_call(MessageBase* m) { return reinterpret_cast<CallMsg*>(m); }
        ResultMsg* as_result(MessageBase* m) { return reinterpret_cast<ResultMsg*>(m); }
        ViewMsg* as_view(MessageBase* m) { return reinterpret_cast<ViewMsg*>(m); }

        uint8_t g_nullJamID[16];

        void set_fixed(EngineString& s, char* buf, size_t cap)
        {
            buf[0] = 0;
            s.len = 0;
            s.cap = (uint32_t)cap;
            s.ptr = buf;
        }

        std::string get_str(const EngineString& s)
        {
            uint32_t n = s.len & 0x7fffffff;
            const char* p = (s.cap & 0x80000000) ? (const char*)&s.ptr : s.ptr;
            return std::string(p, n);
        }

        void put_str(EngineString& s, const std::string& v)
        {
            size_t n = v.size() < s.cap ? v.size() : s.cap;
            memcpy(s.ptr, v.data(), n);
            s.ptr[n] = 0;
            s.len = (uint32_t)n;
        }

        // ---- message vtable (0 dtor, 1 destroy, 2 info, 3 clone) ----------------------------
        void* __fastcall msg_dtor(MessageBase* m, uint64_t flags);
        void __fastcall msg_destroy(MessageBase* m);
        const MessageInfo* __fastcall msg_info(MessageBase* m);
        MessageBase* __fastcall msg_clone(MessageBase* m);

        void* CallVtable[] = {(void*)msg_dtor, (void*)msg_destroy, (void*)msg_info, (void*)msg_clone};
        void* ResultVtable[] = {(void*)msg_dtor, (void*)msg_destroy, (void*)msg_info, (void*)msg_clone};
        void* ViewVtable[] = {(void*)msg_dtor, (void*)msg_destroy, (void*)msg_info, (void*)msg_clone};

        uint16_t msg_id(MessageBase* m)
        {
            return m->vtable == CallVtable ? IdCall : m->vtable == ViewVtable ? IdView : IdResult;
        }

        struct Pool;
        Pool* g_pool = nullptr;

        MessageBase* new_message(uint16_t id)
        {
            MessageBase* m = nullptr;
            if (id == IdCall) {
                auto c = new CallMsg{};
                c->base.vtable = CallVtable;
                set_fixed(c->tool, c->toolBuf, ToolCap);
                set_fixed(c->args, c->argsBuf, ArgsCap);
                m = &c->base;
            } else if (id == IdResult) {
                auto r = new ResultMsg{};
                r->base.vtable = ResultVtable;
                set_fixed(r->result, r->resultBuf, ResultCap);
                m = &r->base;
            } else if (id == IdView) {
                auto v = new ViewMsg{};
                v->base.vtable = ViewVtable;
                set_fixed(v->body, v->bodyBuf, ViewBodyCap);
                m = &v->base;
            } else {
                return nullptr; // unknown id: the client drops the link, like a stock pool
            }
            m->refs = 0;
            memcpy(m->peer, g_nullJamID, 16);
            m->channel = 0xFF;
            m->pool = g_pool;
            return m;
        }

        void delete_message(MessageBase* m)
        {
            if (m->vtable == CallVtable) delete as_call(m);
            else if (m->vtable == ViewVtable) delete as_view(m);
            else delete as_result(m);
        }

        void* __fastcall msg_dtor(MessageBase* m, uint64_t flags)
        {
            if (flags & 1) delete_message(m);
            return m;
        }
        void __fastcall msg_destroy(MessageBase* m) { delete_message(m); }
        const MessageInfo* __fastcall msg_info(MessageBase* m)
        {
            return m->vtable == CallVtable ? &CallInfo : m->vtable == ViewVtable ? &ViewInfo : &ResultInfo;
        }
        MessageBase* __fastcall msg_clone(MessageBase* m)
        {
            MessageBase* c = new_message(msg_id(m));
            if (m->vtable == ViewVtable) {
                auto s = as_view(m); auto d = as_view(c);
                d->seq = s->seq; d->frame = s->frame; d->sentUs = s->sentUs; d->relayUs = s->relayUs;
                d->arrivalUs = s->arrivalUs; put_str(d->body, get_str(s->body));
            } else if (m->vtable == CallVtable) {
                auto s = as_call(m); auto d = as_call(c);
                d->seq = s->seq; put_str(d->tool, get_str(s->tool)); put_str(d->args, get_str(s->args));
            } else {
                auto s = as_result(m); auto d = as_result(c);
                d->seq = s->seq; d->ok = s->ok; put_str(d->result, get_str(s->result));
            }
            return c;
        }

        void release(MessageBase* m)
        {
            if (InterlockedDecrement(&m->refs) == 0)
                msg_destroy(m);
        }

        // ---- pool (JamPool vtable, docs/JAM.md §13.1) ----------------------------------------
        struct Pool {
            void** vtable;
            volatile long refs;
            uint8_t rest[0x70 - 0x0C];
        };

        void* __fastcall pool_dtor(Pool* p, uint64_t) { return p; } // static: never freed
        void __fastcall pool_destroy(Pool*) {}
        const char* __fastcall pool_name(Pool*) { return ProtocolName; }
        uint32_t __fastcall pool_crc(Pool*) { return ProtocolCRC; }
        uint64_t __fastcall pool_zero(Pool*) { return 0; }
        uint16_t __fastcall pool_min(Pool*) { return IdBase; }
        uint16_t __fastcall pool_max(Pool*) { return IdMax; }
        MessageBase* __fastcall pool_allocate(Pool*, uint16_t id) { return new_message(id); }
        void __fastcall pool_release(Pool*, MessageBase* m) { delete_message(m); }
        MessageBase* __fastcall pool_clone(Pool*, MessageBase* m) { return msg_clone(m); }

        void* PoolVtable[] = {(void*)pool_dtor, (void*)pool_destroy, (void*)pool_name, (void*)pool_crc,
                              (void*)pool_zero, (void*)pool_min, (void*)pool_max, (void*)pool_allocate,
                              (void*)pool_release, (void*)pool_clone};
        Pool g_poolObject{PoolVtable, 1 << 20, {}};

        // ---- receiver (0 dtor, 1 destroy, 2 receive, 3 handler object) ---------------------
        struct Receiver {
            void** vtable;
            volatile long refs;
            uint32_t pad;
        };

        struct Pending {
            int64_t link;
            MessageBase* msg;
        };
        std::mutex g_mu;
        std::deque<Pending> g_queue;

        std::atomic<uint64_t> g_received{0}, g_answered{0}, g_sendFailed{0};
        std::atomic<int64_t> g_route{0};
        std::atomic<int64_t> g_lastLink{0};
        std::atomic<bool> g_registered{false};
        std::string g_lastCall; // UI thread only

        void* __fastcall recv_dtor(Receiver* r, uint64_t) { return r; }
        void __fastcall recv_destroy(Receiver*) {}
        uint64_t __fastcall recv_receive(Receiver*, int64_t link, MessageBase* msg, char)
        {
            if (msg->vtable == ViewVtable) as_view(msg)->arrivalUs = server_view::now_us();
            InterlockedIncrement(&msg->refs);
            {
                std::lock_guard lock(g_mu);
                g_queue.push_back({link, msg});
            }
            g_received++;
            return 0;
        }
        void* __fastcall recv_handler(Receiver*) { return nullptr; }

        void* ReceiverVtable[] = {(void*)recv_dtor, (void*)recv_destroy, (void*)recv_receive, (void*)recv_handler};
        Receiver g_receiver{ReceiverVtable, 1 << 20, 0};

        // ---- registration --------------------------------------------------------------------
        void register_on(int64_t route)
        {
            auto cs = (LPCRITICAL_SECTION)(route + 0x98);
            EnterCriticalSection(cs);
            if (game_fn<RouteFindPool_fn>(RouteFindPool_Addr)(route, ProtocolCRC) != 0) {
                LeaveCriticalSection(cs);
                return;
            }
            uint32_t crc = ProtocolCRC;
            *game_fn<TreeInsertU32_fn>(TreeInsertU32_Addr)(route + 0x58, &crc) = (int64_t)g_pool;
            InterlockedIncrement(&g_pool->refs);
            game_fn<RouteAddInbound_fn>(RouteAddInbound_Addr)(route, g_pool, 1, &g_receiver);

            RouteEntry entry{};
            entry.crc = ProtocolCRC;
            entry.base = IdBase;
            entry.max = IdMax;
            entry.serverType = ServerTypeLobby;
            entry.name.len = (uint32_t)strlen(ProtocolName);
            entry.name.cap = entry.name.len;
            entry.name.ptr = (char*)ProtocolName;
            game_fn<RouteAddEntry_fn>(RouteAddEntry_Addr)(route, &entry, 3);
            LeaveCriticalSection(cs);

            g_route = route;
            g_registered = true;
            LOG_CORE(Info, "CustomJam: registered {} (crc {:#x}, ids {}-{}, server type {}) on route config {:#x}",
                     ProtocolName, ProtocolCRC, IdBase, IdMax - 1, ServerTypeLobby, route);
        }

        void __fastcall LobbyRegister_hook(int64_t lobbySys)
        {
            LobbyRegister_orig(lobbySys);
            int64_t svc = 0, jamServer = 0;
            if (!Utility::SafeMemory::Read(&svc, (void*)(lobbySys + 0x10), 8) || !svc ||
                !Utility::SafeMemory::Read(&jamServer, (void*)(svc + 0x20), 8) || !jamServer) {
                LOG_CORE(Error, "CustomJam: lobby system {:#x} has no JAM service; {} not registered", lobbySys, ProtocolName);
                return;
            }
            register_on(jamServer + 0x1a8);
        }

        // ---- calls ---------------------------------------------------------------------------
        struct Notice {
            std::string text;
            ImU32 color;
            std::chrono::steady_clock::time_point until;
        };
        std::vector<Notice> g_notices; // UI thread only

        nlohmann::json run_tool(const std::string& tool, const nlohmann::json& args, bool& ok)
        {
            ok = true;
            if (tool == "ping")
                return {{"pong", true}, {"protocol", ProtocolName}};
            if (tool == "tools") {
                nlohmann::json names = {"notify", "ping", "tools"};
                for (auto& t : prom_mcp::all_tools()) names.push_back(t.name);
                return {{"tools", names}};
            }
            if (tool == "notify") {
                std::string text = args.value("text", std::string{});
                double seconds = args.value("seconds", 6.0);
                auto c = args.value("color", std::vector<float>{1.f, 0.85f, 0.2f, 1.f});
                while (c.size() < 4) c.push_back(1.f);
                g_notices.push_back({text, ImGui::ColorConvertFloat4ToU32({c[0], c[1], c[2], c[3]}),
                                     std::chrono::steady_clock::now() + std::chrono::milliseconds((int64_t)(seconds * 1000))});
                return {{"shown", text}, {"seconds", seconds}};
            }
            auto def = prom_mcp::find_tool(tool);
            if (!def) {
                ok = false;
                return {{"error", "unknown tool: " + tool}};
            }
            nlohmann::json r = def->handler(args);
            if (r.is_object() && r.contains("error")) ok = false;
            return r;
        }

        void answer(int64_t link, uint32_t seq, bool ok, const nlohmann::json& result)
        {
            if (!link) return;
            std::string text = result.dump(-1, ' ', false, nlohmann::json::error_handler_t::replace);
            if (text.size() > ResultCap) {
                nlohmann::json cut = {{"truncated", true}, {"size", text.size()}};
                std::string head = text.substr(0, ResultCap - 128);
                cut["head"] = head;
                text = cut.dump(-1, ' ', false, nlohmann::json::error_handler_t::replace);
                if (text.size() > ResultCap) text = R"({"truncated":true})";
            }
            auto r = as_result(new_message(IdResult));
            r->base.refs = 1;
            r->seq = seq;
            r->ok = ok ? 1 : 0;
            put_str(r->result, text);
            bool sent = game_fn<JamLinkSend_fn>(JamLinkSend_Addr)(link, &r->base) & 0xff;
            release(&r->base);
            if (sent) g_answered++;
            else {
                g_sendFailed++;
                LOG_CORE(Warn, "CustomJam: result {} not sent (link {:#x} not up?)", seq, link);
            }
        }

        void handle(int64_t link, MessageBase* msg)
        {
            if (msg->vtable == ViewVtable) {
                auto v = as_view(msg);
                server_view::on_frame(v->seq, v->frame, v->sentUs, v->relayUs, get_str(v->body), v->arrivalUs, msg->frameLen);
                return;
            }
            if (msg->vtable != CallVtable) {
                LOG_CORE(Warn, "CustomJam: ignoring a {} message from the server", msg_info(msg)->name);
                return;
            }
            auto call = as_call(msg);
            std::string tool = get_str(call->tool), argText = get_str(call->args);
            g_lastCall = std::format("#{} {} {}", call->seq, tool, argText.substr(0, 200));
            LOG_CORE(Info, "CustomJam: call #{} {} {}", call->seq, tool, argText.substr(0, 500));

            nlohmann::json args = argText.empty() ? nlohmann::json::object() : nlohmann::json::parse(argText, nullptr, false);
            bool ok = false;
            nlohmann::json result;
            if (args.is_discarded() || !args.is_object())
                result = {{"error", "args is not a JSON object"}};
            else
                result = run_tool(tool, args, ok);
            answer(link, call->seq, ok, result);
        }
    }

    void Install()
    {
        g_pool = &g_poolObject;
        memcpy(g_nullJamID, (void*)(globals::gameBase + NullJamID_Addr), 16);
        MH_VERIFY(MH_CreateHook((PVOID)(globals::gameBase + LobbyRegisterProtocols_Addr), (LPVOID)LobbyRegister_hook, (PVOID*)&LobbyRegister_orig));
        MH_VERIFY(MH_EnableHook((PVOID)(globals::gameBase + LobbyRegisterProtocols_Addr)));
        LOG_CORE(Debug, "CustomJam: lobby protocol registration hook\n");
    }

    void process()
    {
        std::deque<Pending> batch;
        {
            std::lock_guard lock(g_mu);
            std::swap(batch, g_queue);
        }
        for (auto& p : batch) {
            if (p.link) g_lastLink = p.link;
            handle(p.link, p.msg);
            release(p.msg);
        }
    }

    void on_link_closed(int64_t link)
    {
        int64_t expected = link;
        g_lastLink.compare_exchange_strong(expected, 0);
        std::lock_guard lock(g_mu);
        for (auto& p : g_queue)
            if (p.link == link) p.link = 0;
    }

    void render_overlay()
    {
        auto now = std::chrono::steady_clock::now();
        std::erase_if(g_notices, [&](const Notice& n) { return n.until <= now; });
        if (g_notices.empty()) return;
        auto vp = ImGui::GetMainViewport();
        auto dl = ImGui::GetForegroundDrawList(vp);
        float y = vp->Pos.y + vp->Size.y * 0.18f;
        float scale = 1.6f;
        ImFont* font = ImGui::GetFont();
        float size = ImGui::GetFontSize() * scale;
        for (auto& n : g_notices) {
            ImVec2 ts = font->CalcTextSizeA(size, FLT_MAX, 0.f, n.text.c_str());
            ImVec2 p{vp->Pos.x + (vp->Size.x - ts.x) * 0.5f, y};
            dl->AddRectFilled({p.x - 16, p.y - 8}, {p.x + ts.x + 16, p.y + ts.y + 8}, IM_COL32(0, 0, 0, 170), 6.f);
            dl->AddText(font, size, p, n.color, n.text.c_str());
            y += ts.y + 24;
        }
    }

    nlohmann::json status()
    {
        return {
            {"protocol", ProtocolName},
            {"crc", std::format("{:#x}", ProtocolCRC)},
            {"ids", {IdBase, IdMax - 1}},
            {"registered", g_registered.load()},
            {"route_config", std::format("{:#x}", g_route.load())},
            {"last_link", std::format("{:#x}", g_lastLink.load())},
            {"received", g_received.load()},
            {"answered", g_answered.load()},
            {"send_failed", g_sendFailed.load()},
            {"last_call", g_lastCall},
            {"notices", g_notices.size()},
        };
    }
}
