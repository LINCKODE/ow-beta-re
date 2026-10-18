// STUD v3 reader: header, instance table, layout-CRC check, field decoding. See docs/STU.md §1, §6.
#pragma once
#include <cstdint>
#include <span>
#include <string>
#include <vector>

#include "stu/registry.h"
#include "stu/value.h"

namespace stu {

// How pointers in the image are encoded.
enum class Mode {
    File,    // raw STUD file: offsets from the start of the file (what CASC holds)
    Loaded,  // as-loaded image dumped from the client: absolute pointers (base = address of the image)
};

struct Instance {
    uint32_t offset = 0;    // from the start of the image
    uint32_t flags = 0;     // bit 0: has references to other resources (resolve pass)
    uint64_t layout_crc = 0;
    const TypeInfo* type = nullptr;  // nullptr if unknown
    bool crc_ok = false;    // type found and its layout CRC matches
};

class Document {
public:
    // Parses the header and instance table; the bytes must outlive the Document.
    // Returns false and sets `error` on a malformed image. Instances whose type is unknown or whose
    // layout CRC does not match are kept (crc_ok = false); AllCrcOk() tells whether the client would
    // load the file.
    static bool Parse(std::span<const uint8_t> bytes, const Registry& reg, Document& out, std::string& error,
                      Mode mode = Mode::File, uint64_t base = 0);

    const std::vector<Instance>& instances() const { return instances_; }
    bool AllCrcOk() const;
    int RootInstance() const { return instances_.empty() ? -1 : 0; }
    // Instance index starting at `offset`, or -1.
    int InstanceAt(uint64_t offset) const;
    Mode mode() const { return mode_; }
    const Registry& registry() const { return *reg_; }
    size_t size() const { return bytes_.size(); }

    // Decodes an instance into a tree (Value::Type::Inline with the instance's fields).
    Value Decode(int instance) const;
    // Decodes an object of `type` at `offset` (inline objects / instances).
    Value DecodeObject(uint64_t offset, const TypeInfo& type) const;

private:
    Value DecodeField(uint64_t obj, const FieldInfo& f) const;
    Value DecodePrimitive(uint64_t at, uint32_t elem) const;
    Value DecodeString(uint64_t at) const;
    Value DecodeObjectPtr(uint64_t at, uint32_t elem) const;
    Value DecodeRef(uint64_t at, uint32_t elem) const;
    Value DecodeList(uint64_t at, const FieldInfo& f) const;
    Value DecodeMap(uint64_t at, const FieldInfo& f) const;
    // Descriptor pointer (lists, maps, strings): image offset, or -1 for null / outside the image.
    int64_t Ptr(uint64_t at) const;
    bool In(uint64_t off, uint64_t len) const { return off <= bytes_.size() && len <= bytes_.size() - off; }
    template <typename T>
    T Read(uint64_t off) const;

    std::span<const uint8_t> bytes_;
    const Registry* reg_ = nullptr;
    Mode mode_ = Mode::File;
    uint64_t base_ = 0;
    std::vector<Instance> instances_;
};

// The whole document as JSON: {"instances": N, "crc_mismatch": M, "root": [{index, offset, flags, type,
// crc_ok, value}...]} (stu_dump's output; `summary` leaves out the values).
std::string DocumentJson(const Document& doc, bool with_handles = false, bool summary = false);

// Reads a whole file; empty on failure.
std::vector<uint8_t> ReadFile(const std::string& path);

}  // namespace stu
