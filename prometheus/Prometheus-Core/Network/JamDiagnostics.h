#pragma once

// Logs JAM link events (up/down, RVA 0xF29510) and why the client drops a link.
// Every JAM-level close goes through JamLink::Close
// (RVA 0xF288F0); the hook logs the link state and the call stack as game RVAs,
// which identifies the failed check (unknown channel, no inbound route, wrong
// negotiation state, decode failure, ...).
// Also logs TerminateProcess/ExitProcess of the game itself with the game call
// stack, for fatal errors that leave no exception (e.g. show-mode "Enter Game").
namespace Network::JamDiagnostics
{
    void Install();
}
