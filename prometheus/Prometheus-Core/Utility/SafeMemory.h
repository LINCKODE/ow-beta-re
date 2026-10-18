#pragma once

#include <windows.h>
#include <cstddef>
#include <string>

// Reading arbitrary (possibly unmapped) memory without taking the process down.
//
// Why not IsBadReadPtr + __try: with llvm-mingw (clang, x86_64-w64-mingw32) `__try/__except` compiles to
// nothing (no handler is emitted, the __except branch is dropped), and Wine's IsBadReadPtr only catches
// access violations: touching a guard page (e.g. another thread's stack guard) escapes it and kills or
// hangs the calling thread. So: check pages with VirtualQuery (touches nothing), copy with a small leaf
// routine whose faults the Prometheus VEH recovers (RecoverFault).
namespace Utility::SafeMemory
{
    // true when every page of [p, p+n) is committed, readable and not a guard / no-access page.
    // On false, `why` (optional) gets the first bad page and its state/protection.
    bool IsReadable(const void* p, size_t n, std::string* why = nullptr);

    // Number of readable bytes starting at p, at most max (stops at the first bad page).
    size_t ReadableSpan(const void* p, size_t max);

    // Copy n bytes from src to dst. false (dst partially written) if a page is unreadable or the copy
    // faults anyway (page freed between check and copy).
    bool Read(void* dst, const void* src, size_t n, std::string* why = nullptr);

    // Called first thing in the vectored exception handler: if the fault happened inside Read's copy,
    // redirect it to the failure return and return true (caller returns EXCEPTION_CONTINUE_EXECUTION).
    bool RecoverFault(EXCEPTION_POINTERS* exception);
}
