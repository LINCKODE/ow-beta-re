#pragma once
// Minimal reader for flat JSON objects (one fixture event per line): string, integer, number, bool, null.
#include <cstdint>
#include <cstdlib>
#include <fstream>
#include <map>
#include <string>
#include <vector>

namespace fx {

struct Value {
    enum Type { Null, Str, Int, Num, Bool } type = Null;
    std::string s;
    int64_t i = 0;
    double d = 0;
};

using Object = std::map<std::string, Value>;

inline bool ParseObject(const std::string& line, Object& out) {
    size_t p = 0;
    auto ws = [&] { while (p < line.size() && (line[p] == ' ' || line[p] == '\t' || line[p] == '\r')) p++; };
    auto str = [&](std::string& s) {
        if (line[p] != '"') return false;
        p++;
        while (p < line.size() && line[p] != '"') {
            if (line[p] == '\\' && p + 1 < line.size()) p++;
            s += line[p++];
        }
        p++;
        return true;
    };
    ws();
    if (p >= line.size() || line[p] != '{') return false;
    p++;
    for (;;) {
        ws();
        if (p < line.size() && line[p] == '}') return true;
        std::string key;
        if (!str(key)) return false;
        ws();
        if (line[p] != ':') return false;
        p++;
        ws();
        Value v;
        if (line[p] == '"') {
            v.type = Value::Str;
            if (!str(v.s)) return false;
        } else if (line.compare(p, 4, "null") == 0) {
            p += 4;
        } else if (line.compare(p, 4, "true") == 0 || line.compare(p, 5, "false") == 0) {
            v.type = Value::Bool;
            v.i = line[p] == 't';
            p += v.i ? 4 : 5;
        } else {
            size_t q = p;
            while (q < line.size() && line[q] != ',' && line[q] != '}') q++;
            std::string num = line.substr(p, q - p);
            while (!num.empty() && num.back() == ' ') num.pop_back();
            if (num.find_first_of(".eE") != std::string::npos) {
                v.type = Value::Num;
                v.d = std::strtod(num.c_str(), nullptr);
            } else {
                v.type = Value::Int;
                v.i = std::strtoll(num.c_str(), nullptr, 10);
            }
            p = q;
        }
        out[key] = v;
        ws();
        if (line[p] == ',') {
            p++;
            continue;
        }
        return line[p] == '}';
    }
}

inline std::vector<Object> ReadLines(const std::string& path) {
    std::vector<Object> out;
    std::ifstream f(path);
    std::string line;
    while (std::getline(f, line)) {
        if (line.empty()) continue;
        Object o;
        if (ParseObject(line, o)) out.push_back(std::move(o));
    }
    return out;
}

inline std::string S(const Object& o, const char* k) {
    auto it = o.find(k);
    if (it == o.end()) return "";
    if (it->second.type == Value::Str) return it->second.s;
    if (it->second.type == Value::Null) return "null";
    return std::to_string(it->second.i);
}
inline int64_t I(const Object& o, const char* k) {
    auto it = o.find(k);
    return it == o.end() ? 0 : it->second.i;
}

}  // namespace fx
