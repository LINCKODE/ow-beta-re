#pragma once

// Skips the RSA step on the lobby server's state blob, so a local lobby server
// can complete the handshake.
//
// In the lobby negotiation (FUN_7ff6510c2500, state 4) the client transforms the
// 256-byte blob with an RSA public-key operation (blob^e mod n, RVA 0x1359B10) and
// then checks an HMAC over the result. Producing a valid blob would need Blizzard's
// private key, so the hook leaves the blob unchanged and the server sends it with
// just the HMAC. The function has no other callers.
namespace Network::LobbyBlobBypass
{
    void Install();
}
