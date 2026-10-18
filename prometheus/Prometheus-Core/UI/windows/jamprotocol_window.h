#pragma once
#include "../window_manager/window_manager.h"
#include "jam_messagedetail_window.h"
#include "jam_protocol_dump.h"
#include <MinHook.h>
#include <map>
#include "JAM.h"

class jamprotocol_window : public window {
public:
	WINDOW_DEFINE(jamprotocol_window, "Jam", "Jam Protocol", true);
	
	typedef __int64(__fastcall* addJamProtocolPool_fn)(__int64 linkedList, __int64 dispatchInfo);
	static inline addJamProtocolPool_fn addJamProtocolPool_orig;
	static inline addJamProtocolPool_fn associatePool_orig;

	// JamLinkTick hook (RVA 0xF28590) — captures the JamLink pointer each frame.
	// Only fires when network manager state == 2 (connected).
	typedef void(__fastcall* jamLinkTick_fn)(__int64 jamLink, unsigned __int64 currentTime);
	static inline jamLinkTick_fn jamLinkTick_orig;
	static void __fastcall jamLinkTickHook(__int64 jamLink, unsigned __int64 currentTime) {
		g_jamLink = jamLink;
		jamLinkTick_orig(jamLink, currentTime);
	}

	// Manager tick hook (RVA 0xF22340) — captures the network manager pointer every frame,
	// regardless of state. If JamLink hasn't been captured via the tick hook yet, walks the
	// manager's JamLink connection tree to find one.
	typedef void(__fastcall* managerTick_fn)(__int64 manager);
	static inline managerTick_fn managerTick_orig;
	static void __fastcall managerTickHook(__int64 manager) {
		g_networkManager = manager;
		if (g_jamLink == 0 && manager != 0) {
			tryFindJamLinkFromManager(manager);
		}
		managerTick_orig(manager);
	}

	// JamLink connection tree at manager + 0xC0. Sentinel node is inline at that address;
	// root = *(manager + 0xC0). Tree nodes: +0x00 parent, +0x08 left, +0x10 right,
	// +0x20 key, +0x30 data_ptr. data_ptr -> wrapper with JamLink at +0x08.
	static void tryFindJamLinkFromManager(__int64 manager) {
		__try {
			__int64 sentinelAddr = manager + 0xC0;
			__int64 root = *(__int64*)(manager + 0xC0);
			if (root == 0 || root == sentinelAddr) return;
			__int64 node = root;
			while (*(__int64*)(node + 0x08) != 0 && *(__int64*)(node + 0x08) != sentinelAddr) {
				node = *(__int64*)(node + 0x08); // leftmost in-order
			}
			__int64 wrapper = *(__int64*)(node + 0x30);
			if (wrapper == 0) return;
			__int64 jamLink = *(__int64*)(wrapper + 0x08);
			if (jamLink != 0 && !IsBadReadPtr((void*)jamLink, 0x98)) {
				g_jamLink = jamLink;
			}
		} __except (EXCEPTION_EXECUTE_HANDLER) {
			// pointers may not be initialized yet
		}
	}

	static __int64 __fastcall addJamProtocolHook(__int64 linkedList, __int64 dispatchInfo) {
		__int64 result = addJamProtocolPool_orig(linkedList, dispatchInfo);
		if (jamPoolInstances.contains(dispatchInfo)) {
			//printf("DISPATCH INFO ALREADY EXISTS!\n"); never happens
		}
		jamPoolInstances[result] = dispatchInfo;
		return result;
	}

	// associatePool hook — captures both function pointer (poolAssociations) and full
	// receiver object (poolReceivers) for direct dispatch without JamLink.
	// poolEntry layout: +0x00 CRC, +0x08 name_ptr, +0x10 receiver_array, +0x18 count
	// receiverObj layout: +0x00 vtable*, +0x08 refcount(int), +0x20 handler_func_ptr
	static __int64 __fastcall associatePoolHook(__int64 poolEntry, __int64 receiverObj) {
		int crc = *(int*)(poolEntry);
		__int64 receiverFunc = *(__int64*)(receiverObj + 0x20);
		poolAssociations[crc] = receiverFunc;

		// Bump refcount so the receiver stays alive for direct dispatch
		InterlockedIncrement((volatile long*)(receiverObj + 8));
		poolReceivers[crc].push_back(receiverObj);

		__int64 result = associatePool_orig(poolEntry, receiverObj);
		return result;
	}

	inline void preStartInitialize() override {
		MH_CreateHook((void*)(globals::gameBase + 0x8a27d0), (LPVOID)addJamProtocolHook, (void**)(&addJamProtocolPool_orig));
		MH_EnableHook((void*)(globals::gameBase + 0x8a27d0));
		MH_CreateHook((void*)(globals::gameBase + 0xf25ba0), (LPVOID)associatePoolHook, (void**)(&associatePool_orig));
		MH_EnableHook((void*)(globals::gameBase + 0xf25ba0));
		MH_CreateHook((void*)(globals::gameBase + 0xF28590), (LPVOID)jamLinkTickHook, (void**)(&jamLinkTick_orig));
		MH_EnableHook((void*)(globals::gameBase + 0xF28590));
		MH_CreateHook((void*)(globals::gameBase + 0xF22340), (LPVOID)managerTickHook, (void**)(&managerTick_orig));
		MH_EnableHook((void*)(globals::gameBase + 0xF22340));
	}

	inline void initialize() override {

	}

	inline void render() override {
		for (auto& item : jamPoolInstances) {
			__int64 instance = *(__int64*)item.first;
			JamPoolVtable* vtable = *(JamPoolVtable**)(instance);
			char* static_name = vtable->pool_name();
			jamOrderedInstances.emplace(std::string(static_name), std::pair<__int64, __int64> { item.first, item.second });
		}
		jamPoolInstances.clear();
		if (open_window()) {
			ImGui::BeginTable("JAMMING TABLE", 10, ImGuiTableFlags_Resizable | ImGuiTableFlags_BordersH);
			ImGui::TableSetupColumn("Instance");
			ImGui::TableSetupColumn("Dispatch Info");
			ImGui::TableSetupColumn("Pool Name");
			ImGui::TableSetupColumn("Pool static name");
			ImGui::TableSetupColumn("Min ID");
			ImGui::TableSetupColumn("Max ID");
			ImGui::TableSetupColumn("Zero?");
			ImGui::TableSetupColumn("Count");
			ImGui::TableSetupColumn("Actions");
			ImGui::TableSetupColumn("ASSOCIATE");
			ImGui::TableHeadersRow();
			for (auto& item1 : jamOrderedInstances) {

				auto& item = item1.second;
				ImGui::PushID(*(__int64*)item.first);
				ImGui::TableNextRow();
				__int64 instance = *(__int64*)item.first;
				JamPoolVtable* vtable = *(JamPoolVtable**)(instance);

				ImGui::TableNextColumn();
				display_addr(item.first);
				
				ImGui::TableNextColumn();
				display_addr(item.second);

				ImGui::TableNextColumn();
				ImGui::Text("%s", *(char**)(instance + 0x18));

				ImGui::TableNextColumn();
				char* static_name = vtable->pool_name();
				ImGui::Text("%s", static_name == nullptr ? "NULL" : static_name);

				ImGui::TableNextColumn();
				short min = vtable->min_msgId();
				ImGui::Text("%hx %hd", min, min);

				ImGui::TableNextColumn();
				short max = vtable->max_msgId();
				ImGui::Text("%hx %hd", max, max);

				ImGui::TableNextColumn();
				int zero = vtable->returns_zero();
				ImGui::Text("%d", zero);

				ImGui::TableNextColumn();
				ImGui::Text("%d", *(int*)(instance + 0x30));

				ImGui::TableNextColumn();
				char buf[32];
				sprintf(buf, "Open %d messages", max - min);
				if (ImGui::Button(buf)) {
					std::vector<__int64> instances{};
					for (short i = min; i < max; i++) {
						instances.push_back((__int64)vtable->allocate_pool(instance, i));
					}
					window_manager::add_window(new jam_messagedetail_window(std::move(instances), static_name), this);
				}

				ImGui::TableNextColumn();
				if (poolAssociations.contains(vtable->pool_crc())) {
					display_addr(poolAssociations[vtable->pool_crc()]);
				}
				ImGui::PopID();
			}
			ImGui::EndTable();

			if (ImGui::Button("Dump all to JSON")) {
				jam_protocol_dump::dump_all("Prometheus/jam_protocols.json");
			}
			ImGui::SameLine();
			ImGui::TextDisabled("-> Prometheus/jam_protocols.json");

			if (imgui_helpers::CenteredButton("Close")) {
				queue_deletion();
			}
		}
		ImGui::End();
	}
};

WINDOW_REGISTER(jamprotocol_window);