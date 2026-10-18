#pragma once
#include <vector>
#include <cstdint>
#include <cstring>
#include <mutex>
#include "Logs/Logs.h"

// Captures every protobuf FileDescriptorProto blob the game registers at startup
// by hooking InternalAddGeneratedFile (RVA 0xE8C8C0). The hook itself is installed
// from StartHook in dllmain.cpp (must run after .text decryption).
namespace proto_capture {

	struct CapturedProtoBlob {
		std::vector<uint8_t> data;
	};

	inline std::vector<CapturedProtoBlob> g_capturedProtos;
	inline std::mutex g_protoMutex;

	typedef void (*InternalAddGeneratedFile_t)(const void* data, int size);
	inline InternalAddGeneratedFile_t InternalAddGeneratedFile_orig = nullptr;

	inline void InternalAddGeneratedFile_hook(const void* data, int size) {
		if (data && size > 0) {
			std::lock_guard<std::mutex> lock(g_protoMutex);
			CapturedProtoBlob blob;
			blob.data.resize(size);
			memcpy(blob.data.data(), data, size);
			g_capturedProtos.push_back(std::move(blob));
			LOG_CORE(Info, "[ProtoCapture] Captured blob #{} ({} bytes)",
				g_capturedProtos.size(), size);
		}
		InternalAddGeneratedFile_orig(data, size);
	}

}
