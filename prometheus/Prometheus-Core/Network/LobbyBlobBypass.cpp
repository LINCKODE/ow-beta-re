#include "LobbyBlobBypass.h"

#include <MinHook.h>

#include "Logs/Logs.h"
#include "globals.h"

namespace Network::LobbyBlobBypass
{
    // bool RsaPublicTransform(RsaKey* key, uint8_t* data, size_t size): data = data^e mod n.
    // Returns whether data was in range; the handshake ignores the result.
    constexpr uintptr_t RsaPublicTransform_Addr = 0x1359B10;

    using RsaPublicTransform_fn = bool(__fastcall*)(__int64 key, unsigned char* data, unsigned __int64 size);
    static RsaPublicTransform_fn RsaPublicTransform_orig;

    static bool __fastcall RsaPublicTransform_hook(__int64 key, unsigned char* data, unsigned __int64 size)
    {
        LOG_CORE(Info, "Lobby state blob ({} bytes): skipping the RSA transform.", size);
        return true;
    }

    void Install()
    {
        MH_VERIFY(MH_CreateHook((PVOID)(globals::gameBase + RsaPublicTransform_Addr), (LPVOID)RsaPublicTransform_hook, (PVOID*)&RsaPublicTransform_orig));
        MH_VERIFY(MH_EnableHook((PVOID)(globals::gameBase + RsaPublicTransform_Addr)));
        LOG_CORE(Debug, "Lobby state blob RSA hook\n");
    }
}
