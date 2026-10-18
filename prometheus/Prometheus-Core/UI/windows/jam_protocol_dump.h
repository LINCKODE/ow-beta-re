#pragma once
#include <fstream>
#include <string>
#include <nlohmann/json.hpp>
#include "JAM.h"
#include "Logs/Logs.h"

// Dumps every registered JAM protocol with its messages and field layouts to JSON,
// so the definitions can be used outside the game (e.g. by the lobby server).
//
// Wire encodings follow the client's binary decoder (FUN_7ff650f0c7d0 and callers):
// all values little-endian and unpadded; dynamic arrays are [u32 count][elements].
namespace jam_protocol_dump
{
    // Field type codes (JamMessageItemInfo::message_type) and how the binary decoder reads them.
    inline const char* wire_encoding(int type)
    {
        switch (type)
        {
        case 0:          return "bool (bit-packed, consecutive bools share a byte from bit 0)";
        case 1: case 2:
        case 3:          return "1 byte";
        case 4: case 5:  return "2 bytes";
        case 6: case 7:  return "4 bytes (int)";
        case 10:         return "4 bytes (float)";
        case 8: case 9:
        case 11:         return "8 bytes";
        case 12:         return "string: null-terminated";
        case 13:         return "fixed-size blob (size bytes)";
        case 14:         return "struct (fields below)";
        case 15:         return "length-prefixed: [u32 length][bytes]";
        case 16:         return "tagged id: [u8 kind 1-5][1/2/4/8/16 bytes]";
        case 17:         return "end of fields";
        default:         return "unknown";
        }
    }

    inline nlohmann::json fields(JamMessageItemInfo* item, int depth = 0)
    {
        nlohmann::json out = nlohmann::json::array();
        for (; item && item->message_type != 0x11 && depth < 8; item++)
        {
            nlohmann::json f = {
                {"name", item->name_str ? item->name_str : ""},
                {"type", item->message_type},
                {"encoding", wire_encoding(item->message_type)},
                {"offset", item->offset},
                {"size", item->size},
            };
            // has_extra_flag: 0 = single value, -1 = dynamic array [u32 count][elements], n = fixed array of n
            if (item->has_extra_flag == -1)
                f["array"] = "dynamic";
            else if (item->has_extra_flag != 0)
                f["array"] = item->has_extra_flag;
            if (item->inner_data_ptr)
                f["fields"] = fields(item->inner_data_ptr, depth + 1);
            out.push_back(std::move(f));
        }
        return out;
    }

    // Writes all protocols in jamOrderedInstances to path. Returns the number of protocols written.
    inline size_t dump_all(const std::string& path)
    {
        nlohmann::json protocols = nlohmann::json::array();
        for (auto& [static_name, entry] : jamOrderedInstances)
        {
            __int64 instance = *(__int64*)entry.first;
            JamPoolVtable* vtable = *(JamPoolVtable**)instance;
            short min = vtable->min_msgId();
            short max = vtable->max_msgId();
            char* name = *(char**)(instance + 0x18);

            nlohmann::json messages = nlohmann::json::array();
            for (short id = min; id < max; id++)
            {
                JamMessage* msg = vtable->allocate_pool(instance, id);
                if (!msg)
                    continue;
                JamMessageInfo* info = msg->vfptr->get_message_info();
                messages.push_back({
                    {"id", info->message_id},
                    {"index", id - min},
                    {"name", info->message_name ? info->message_name : ""},
                    {"crc", info->crc},
                    {"fields", fields(info->items)},
                });
                vtable->deallocate_pool(instance, (__int64)msg);
            }

            protocols.push_back({
                {"pool", static_name},
                {"name", name ? name : ""},
                {"crc", (unsigned)vtable->pool_crc()},
                {"min_id", min},
                {"max_id", max},
                {"messages", std::move(messages)},
            });
        }

        std::ofstream file(path, std::ios::out | std::ios::trunc);
        file << protocols.dump(2);
        LOG_CORE(Info, "Dumped {} JAM protocols to {}", protocols.size(), path);
        return protocols.size();
    }
}
