#include "SafeMemory.h"

#include <format>

// prom_probe_copy(dst rcx, src rdx, n r8) -> eax 1. A leaf: no stack use, the non-volatile rdi/rsi live in
// r10/r11 during the copy, so a fault on the `rep movsb` can be resumed at prom_probe_copy_fault (which
// restores them and returns 0) without any unwind data.
extern "C" int prom_probe_copy(void* dst, const void* src, size_t n);
extern "C" char prom_probe_copy_fault[];
extern "C" char prom_probe_copy_end[];

asm(R"(
    .text
    .p2align 4
    .globl prom_probe_copy
    .globl prom_probe_copy_fault
    .globl prom_probe_copy_end
prom_probe_copy:
    movq %rdi, %r10
    movq %rsi, %r11
    movq %rcx, %rdi
    movq %rdx, %rsi
    movq %r8, %rcx
    rep movsb
    movq %r10, %rdi
    movq %r11, %rsi
    movl $1, %eax
    ret
prom_probe_copy_fault:
    movq %r10, %rdi
    movq %r11, %rsi
    xorl %eax, %eax
    ret
prom_probe_copy_end:
)");

namespace Utility::SafeMemory
{
    namespace
    {
        bool PageReadable(const MEMORY_BASIC_INFORMATION& mbi)
        {
            constexpr DWORD readable = PAGE_READONLY | PAGE_READWRITE | PAGE_WRITECOPY |
                PAGE_EXECUTE_READ | PAGE_EXECUTE_READWRITE | PAGE_EXECUTE_WRITECOPY;
            return mbi.State == MEM_COMMIT && !(mbi.Protect & (PAGE_GUARD | PAGE_NOACCESS)) && (mbi.Protect & readable);
        }

        const char* StateName(DWORD state)
        {
            switch (state) {
                case MEM_COMMIT:  return "commit";
                case MEM_RESERVE: return "reserve";
                case MEM_FREE:    return "free";
                default:          return "?";
            }
        }

        // Walk the regions covering [p, p+n); returns the readable prefix length.
        size_t Walk(const void* p, size_t n, std::string* why)
        {
            uintptr_t at = reinterpret_cast<uintptr_t>(p), end = at + n;
            if (end < at) end = UINTPTR_MAX; // wraps
            while (at < end) {
                MEMORY_BASIC_INFORMATION mbi;
                if (VirtualQuery(reinterpret_cast<void*>(at), &mbi, sizeof(mbi)) != sizeof(mbi)) {
                    if (why) *why = std::format("0x{:x}: no region (VirtualQuery failed)", at);
                    break;
                }
                if (!PageReadable(mbi)) {
                    if (why) *why = std::format("0x{:x}: {} region 0x{:x}+0x{:x}, protect 0x{:x}{}", at, StateName(mbi.State),
                        reinterpret_cast<uintptr_t>(mbi.BaseAddress), mbi.RegionSize, mbi.Protect,
                        (mbi.Protect & PAGE_GUARD) ? " (guard page)" : "");
                    break;
                }
                uintptr_t region_end = reinterpret_cast<uintptr_t>(mbi.BaseAddress) + mbi.RegionSize;
                if (region_end <= at) break;
                at = region_end;
            }
            if (at > end) at = end;
            return at - reinterpret_cast<uintptr_t>(p);
        }
    }

    bool IsReadable(const void* p, size_t n, std::string* why)
    {
        if (!p) {
            if (why) *why = "null pointer";
            return false;
        }
        if (!n) return true;
        return Walk(p, n, why) >= n;
    }

    size_t ReadableSpan(const void* p, size_t max)
    {
        if (!p || !max) return 0;
        return Walk(p, max, nullptr);
    }

    bool Read(void* dst, const void* src, size_t n, std::string* why)
    {
        if (!IsReadable(src, n, why)) return false;
        if (!n) return true;
        if (prom_probe_copy(dst, src, n)) return true;
        if (why) *why = std::format("0x{:x}: faulted during copy (page changed after the check)", reinterpret_cast<uintptr_t>(src));
        return false;
    }

    bool RecoverFault(EXCEPTION_POINTERS* exception)
    {
        auto* rec = exception->ExceptionRecord;
        auto* ctx = exception->ContextRecord;
        if (rec->ExceptionCode != EXCEPTION_ACCESS_VIOLATION && rec->ExceptionCode != EXCEPTION_GUARD_PAGE &&
            rec->ExceptionCode != EXCEPTION_IN_PAGE_ERROR)
            return false;
        if (ctx->Rip < reinterpret_cast<DWORD64>(prom_probe_copy) || ctx->Rip >= reinterpret_cast<DWORD64>(prom_probe_copy_fault))
            return false;
        // Touching a guard page clears its guard bit (it's a one-shot trap, e.g. stack growth): re-arm it.
        if (rec->ExceptionCode == EXCEPTION_GUARD_PAGE && rec->NumberParameters >= 2) {
            MEMORY_BASIC_INFORMATION mbi;
            void* page = reinterpret_cast<void*>(rec->ExceptionInformation[1] & ~uintptr_t(0xfff));
            DWORD old;
            if (VirtualQuery(page, &mbi, sizeof(mbi)) == sizeof(mbi))
                VirtualProtect(page, 0x1000, mbi.Protect | PAGE_GUARD, &old);
        }
        ctx->Rip = reinterpret_cast<DWORD64>(prom_probe_copy_fault);
        return true;
    }
}
