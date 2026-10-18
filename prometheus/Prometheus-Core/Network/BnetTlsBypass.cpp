#include "BnetTlsBypass.h"

#include <MinHook.h>

#include "Logs/Logs.h"
#include "globals.h"

namespace Network::BnetTlsBypass
{
    // int VerifyCertificate(SslFilter* filter): 0 = trusted, otherwise an error code.
    // Its only caller (RVA 0x10FFB90) marks the connection as established on 0.
    constexpr uintptr_t VerifyCertificate_Addr = 0x10FFDA0;

    using VerifyCertificate_fn = int(__fastcall*)(__int64 filter);
    static VerifyCertificate_fn VerifyCertificate_orig;

    static const char* describe(int result)
    {
        switch (result)
        {
        case 1:     return "remote address is not a hostname";
        case 0x3F5: return "no certificate";
        case 0x3F6: return "certificate has an empty common name";
        case 0x3F7: return "common name does not match the host";
        case 0x3F9: return "public key pinning check failed";
        case 0x3FC: return "chain does not contain the Battle.net Certificate Authority";
        default:    return "chain validation failed";
        }
    }

    static int __fastcall VerifyCertificate_hook(__int64 filter)
    {
        int result = VerifyCertificate_orig(filter);
        if (result == 0)
            LOG_CORE(Info, "Battle.net TLS certificate verified.");
        else
            LOG_CORE(Warn, "Battle.net TLS certificate check failed with {:#x} ({}), accepting it anyway.", result, describe(result));
        return 0;
    }

    void Install()
    {
        MH_VERIFY(MH_CreateHook((PVOID)(globals::gameBase + VerifyCertificate_Addr), (LPVOID)VerifyCertificate_hook, (PVOID*)&VerifyCertificate_orig));
        MH_VERIFY(MH_EnableHook((PVOID)(globals::gameBase + VerifyCertificate_Addr)));
        LOG_CORE(Debug, "Battle.net TLS certificate check hook\n");
    }
}
