#pragma once

// Accepts any TLS certificate from the Battle.net server, so the client can log in
// against a local server (e.g. --BNetServer=127.0.0.1:1119).
//
// The Battle.net SDK verifies the server after the SChannel handshake in
// VerifyCertificate (RVA 0x10FFDA0), which requires a hostname (an IP address always
// fails), a matching common name, a valid chain and either the
// "Battle.net Certificate Authority" in that chain or a pinned public key.
// The hook still runs the original check and logs its result, then reports success.
namespace Network::BnetTlsBypass
{
    void Install();
}
