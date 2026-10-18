#include "JamDiagnostics.h"

#include <MinHook.h>
#include <string>

#include "Logs/Logs.h"
#include "globals.h"
#include "CustomJam.h"

namespace Network::JamDiagnostics
{
    // void JamLink::Close(JamLink* link). Link state at +0x18 (5 = connected, 2-4 = negotiating).
    constexpr uintptr_t JamLinkClose_Addr = 0xF288F0;

    using JamLinkClose_fn = void(__fastcall*)(__int64 link);
    static JamLinkClose_fn JamLinkClose_orig;

    // The caller chain within the game module, as RVAs.
    static std::string game_stack()
    {
        void* frames[12];
        USHORT count = RtlCaptureStackBackTrace(2, 12, frames, nullptr);
        std::string stack;
        for (USHORT i = 0; i < count; i++)
        {
            auto addr = (uintptr_t)frames[i];
            if (addr >= globals::gameBase && addr < globals::gameBase + globals::gameSize)
                stack += fmt::format(" {:#x}", addr - globals::gameBase);
        }
        return stack;
    }

    static void __fastcall JamLinkClose_hook(__int64 link)
    {
        LOG_CORE(Warn, "JAM link {:#x} closed in state {}; game call stack (RVAs):{}", link, *(int*)(link + 0x18), game_stack());
        CustomJam::on_link_closed(link);
        JamLinkClose_orig(link);
    }

    // void JamLink::Notify(JamLink* link, int event, JamID* peer, char reason).
    // Events seen in the negotiation code: 0 = link up (after the peer's protocol
    // list is accepted), 1 = down while connected, 2/3 = down or error while negotiating.
    constexpr uintptr_t JamLinkNotify_Addr = 0xF29510;

    using JamLinkNotify_fn = void(__fastcall*)(__int64 link, int event, __int64 peer, char reason);
    static JamLinkNotify_fn JamLinkNotify_orig;

    static void __fastcall JamLinkNotify_hook(__int64 link, int event, __int64 peer, char reason)
    {
        LOG_CORE(Warn, "JAM link {:#x} event {} (reason {}) in state {}; game call stack (RVAs):{}",
                 link, event, (int)reason, *(int*)(link + 0x18), game_stack());
        JamLinkNotify_orig(link, event, peer, reason);
    }

    // Process exit tracing: an abort()/fatal error that terminates the process leaves
    // no exception for the VEH to log, so log who is exiting (and the game stack).
    static std::string deep_game_stack()
    {
        void* frames[32];
        USHORT count = RtlCaptureStackBackTrace(1, 32, frames, nullptr);
        std::string stack;
        for (USHORT i = 0; i < count; i++)
        {
            auto addr = (uintptr_t)frames[i];
            if (addr >= globals::gameBase && addr < globals::gameBase + globals::gameSize)
                stack += fmt::format(" {:#x}", addr - globals::gameBase);
        }
        return stack;
    }

    using TerminateProcess_fn = BOOL(WINAPI*)(HANDLE, UINT);
    static TerminateProcess_fn TerminateProcess_orig;
    static BOOL WINAPI TerminateProcess_hook(HANDLE process, UINT code)
    {
        if (process == GetCurrentProcess() || GetProcessId(process) == GetCurrentProcessId())
        {
            LOG_CORE(Error, "TerminateProcess(self, {:#x}); game call stack (RVAs):{}", code, deep_game_stack());
            Logs::Core->Flush();
        }
        return TerminateProcess_orig(process, code);
    }

    using ExitProcess_fn = void(WINAPI*)(UINT);
    static ExitProcess_fn ExitProcess_orig;
    static void WINAPI ExitProcess_hook(UINT code)
    {
        LOG_CORE(Error, "ExitProcess({:#x}); game call stack (RVAs):{}", code, deep_game_stack());
        ExitProcess_orig(code);
    }

    void Install()
    {
        MH_VERIFY(MH_CreateHook((PVOID)&TerminateProcess, (LPVOID)TerminateProcess_hook, (PVOID*)&TerminateProcess_orig));
        MH_VERIFY(MH_EnableHook((PVOID)&TerminateProcess));
        MH_VERIFY(MH_CreateHook((PVOID)(globals::gameBase + JamLinkNotify_Addr), (LPVOID)JamLinkNotify_hook, (PVOID*)&JamLinkNotify_orig));
        MH_VERIFY(MH_EnableHook((PVOID)(globals::gameBase + JamLinkNotify_Addr)));
        MH_VERIFY(MH_CreateHook((PVOID)(globals::gameBase + JamLinkClose_Addr), (LPVOID)JamLinkClose_hook, (PVOID*)&JamLinkClose_orig));
        MH_VERIFY(MH_EnableHook((PVOID)(globals::gameBase + JamLinkClose_Addr)));
        LOG_CORE(Debug, "JAM link close diagnostics hook\n");
    }
}
