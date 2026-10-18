#pragma once
#include <map>
#include <vector>
#include "globals.h"

//TODO that may not be right. Never got around to look into it.
struct JamType13String {
	int length;
	int is_ptr;
	__int64 string_literal_or_ptr;
};

inline char* readJamType13String(JamType13String* str) {
	/*printf("len %d %x\n", str->length, str->length);
	printf("ptr %d %x\n", str->is_ptr, str->is_ptr);*/
	if ((str->is_ptr & 0x7FFFFFFF) >= 8) {
		return (char*)str->string_literal_or_ptr;
	}
	return (char*)&str->string_literal_or_ptr;
}

struct JamMessage;
struct JamMessageInfo;
struct JamMessageVTable { //0x30 pool item count
	__int64 destructor_1;
	__int64 destructor_outer;
	JamMessageInfo* (*get_message_info)();
	JamMessage* (*clone_instance)();
};

struct JamMessage {
	JamMessageVTable* vfptr;
};

struct JamMessageItemInfo {
	char* name_str;
	int message_type;
	int offset; //relative to JamMessage*
	int size;
	int has_extra_flag;
	JamMessageItemInfo* inner_data_ptr;
	__int64 decoding_func;
};

// Layout verified via Ghidra xrefs at 0x7ff65170cd70 (Command pool static entries, stride 0x20).
// Most pools have message_name/message_protocol pointing to a shared empty-string sentinel
// (DAT_7ff651434b48) — names are stripped in this build. Only Command, CommandOut, Log, and
// LogControl pools retain meaningful names.
struct JamMessageInfo {
	char* message_name;        // +0x00
	char* message_protocol;    // +0x08
	JamMessageItemInfo* items; // +0x10
	int crc;                   // +0x18
	int message_id;            // +0x1C
};


struct JamPoolVtable { //0x30 pool item count
	__int64 destructor_1;
	__int64 destructor_outer;
	char* (*pool_name)();
	int (*pool_crc)();
	int(*returns_zero)();
	short(*min_msgId)();
	short(*max_msgId)();
	JamMessage*(__fastcall* allocate_pool)(__int64 instance, short msgId);
	__int64(__fastcall* deallocate_pool)(__int64 instance, __int64 messageInstance);
};

static inline std::map<__int64, __int64> jamPoolInstances{};
static inline std::map<std::string, std::pair<__int64, __int64>> jamOrderedInstances{};
static inline std::map<int, __int64> poolAssociations{};

// Receiver objects captured from associate pool hook, keyed by protocol CRC.
// Used for direct receiver dispatch when no JamLink is available.
// Each receiver's vtable[2] can be called as: fn(receiver, handler, msg, 0).
static inline std::map<int, std::vector<__int64>> poolReceivers{};

// Fake handler buffer for direct receiver dispatch when no JamLink exists.
// Has an initialized CriticalSection at +0x48 matching the JamLink handler layout.
static inline char g_fakeJamHandler[0x200] = {};
static inline bool g_fakeJamHandlerInit = false;
inline void initFakeJamHandler() {
	if (!g_fakeJamHandlerInit) {
		memset(g_fakeJamHandler, 0, sizeof(g_fakeJamHandler));
		InitializeCriticalSection((LPCRITICAL_SECTION)(g_fakeJamHandler + 0x48));
		g_fakeJamHandlerInit = true;
	}
}

// JamLink pointer captured from JamLinkTick hook (RVA 0xF28590) or manager tree scan.
// Used by jam_injector_window to dispatch messages via the pool-based receiver dispatch.
static inline __int64 g_jamLink = 0;

// Network manager pointer captured from manager tick hook (RVA 0xF22340).
// Global DAT_7ff651786930 at RVA 0x1786930. State at +0x290 must be 2 for JamLink tick to fire.
static inline __int64 g_networkManager = 0;