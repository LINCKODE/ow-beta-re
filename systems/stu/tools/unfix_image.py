#!/usr/bin/env python3
"""Turn an as-loaded STU image (dumped from the client's memory) back into a raw STUD v3 file
by undoing the loader's fixup (docs/STU.md §2.1, §6). Inverse of the per-kind fixup routines
(constraint vfunc 26) read in Ghidra for T006.

Input: <image>.mem (bytes from `root-0x10` to the end of the instance table) and <image>.mem.json
({"base": <address of the image>, ...}), as produced by dump_live.py.
Output: a .stud file the offline reader parses like a CASC file.

Known approximations (see report T006): the raw file's list descriptor flag (we write --list-flag,
default 0; the loader overwrites it with 2), teString encoding (no example yet), runtime-only fields
the placement constructor zeroes, and the 4 bytes after an instance's type hash (we write 0).
"""
import argparse
import json
import os
import struct
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
REG_PATH = os.path.join(HERE, "..", "..", "data", "client_stu_registry.json")
MODULE_LO, MODULE_HI = 0x140000000, 0x142000000  # game module (static sentinels, vtables)

PRIM_SIZE = {0x3b9327d2: 1, 0x6dc98054: 1, 0xa49ce182: 2, 0xa0119d30: 2, 0x91c74719: 4, 0x954a3bab: 4,
             0x05d31669: 8, 0x015e6adb: 8, 0x8fa75a30: 4, 0x1bb30b40: 8, 0x0c525b48: 4, 0xf6ec8926: 8,
             0x81ebb9b0: 16, 0x2357e676: 16, 0x1f8f2c13: 16, 0xda9494c8: 16, 0x525114ab: 12,
             0xa9e19537: 16, 0xe1b9e763: 8, 0x20b3d5a9: 16, 0x809899f3: 16, 0xbbca304b: 16, 0x6fb9880b: 64}


class Unfixer:
    def __init__(self, reg, img, base, list_flag):
        self.reg = reg
        self.img = bytearray(img)
        self.base = base
        self.list_flag = list_flag
        self.done_lists = set()
        self.warnings = []

    # --- helpers -----------------------------------------------------------------------
    def q(self, off):
        return struct.unpack_from("<Q", self.img, off)[0]

    def put_q(self, off, v):
        struct.pack_into("<Q", self.img, off, v)

    def in_image(self, ptr):
        return self.base <= ptr < self.base + len(self.img)

    def to_off(self, ptr, what):
        if ptr == 0:
            return 0
        if self.in_image(ptr):
            return ptr - self.base
        self.warnings.append("%s: pointer %x outside image" % (what, ptr))
        return 0

    def fields_of(self, h):
        out = []
        while h:
            t = self.reg[h]
            out = [f for f in t["fields"]] + out
            h = t["parent"]
        return out

    # --- object -------------------------------------------------------------------------
    def object(self, off, type_hash):
        """Undo the fixup of one object (instance or inline) of type `type_hash` at image offset `off`."""
        v = self.q(off)
        if MODULE_LO <= v < MODULE_HI:  # vtable written by the placement constructor -> type hash
            self.put_q(off, int(type_hash, 16))
        for f in self.fields_of(type_hash):
            self.field(off + f["offset"], f)

    def field(self, fo, f):
        k = f["ctype"]
        if k == 0:
            if f["elem"] == "bbca304b":
                self.string(fo)
        elif k == 2:
            self.object_ptr(fo)
        elif k == 4:
            self.object(fo, f["elem"])
        elif k in (10, 12):
            self.ref(fo)
        elif k in (1, 3, 5, 9, 11, 13):
            self.list(fo, f)
        elif k == 7:
            self.map(fo)
        # 8 (enum) and other primitives: raw values, nothing to undo

    def object_ptr(self, fo):
        p = self.q(fo)
        if p == 0:
            self.put_q(fo, 0xFFFFFFFF)
        else:
            self.put_q(fo, self.to_off(p, "object"))

    def ref(self, fo):
        self.put_q(fo, 0xFFFFFFFFFFFFFFFF)  # handle: -1 in the file, filled by the resolve pass

    def string(self, fo):
        p, ty = struct.unpack_from("<qq", self.img, fo)
        if p == 0 or not self.in_image(p):
            self.put_q(fo, 0)  # empty: the loader points it at a static empty string
            return
        so = p - self.base
        self.put_q(fo, so)
        h, ln, buf = struct.unpack_from("<Iiq", self.img, so)
        struct.pack_into("<q", self.img, so + 8, self.to_off(buf, "string buffer"))

    def list(self, fo, f):
        p, flag = struct.unpack_from("<qq", self.img, fo)
        struct.pack_into("<q", self.img, fo + 8, self.list_flag)
        if p == 0 or not self.in_image(p):  # empty list -> static per-type sentinel
            self.put_q(fo, 0)
            return
        ho = p - self.base
        self.put_q(fo, ho)
        if ho in self.done_lists:
            return
        self.done_lists.add(ho)
        count, _, data = struct.unpack_from("<iiq", self.img, ho)
        if count <= 0 or data == 0:
            struct.pack_into("<q", self.img, ho + 8, 0)
            return
        do = self.to_off(data, "list data")
        struct.pack_into("<q", self.img, ho + 8, do)
        k = f["ctype"]
        if k == 3:
            for i in range(count):
                self.object_ptr(do + 8 * i)
        elif k == 5:
            size = self.reg[f["elem"]]["size"]
            for i in range(count):
                self.object(do + size * i, f["elem"])
        elif k in (11, 13):
            for i in range(count):
                self.ref(do + 16 * i)
        # 1 (primitives; strings in lists not handled), 9 (enums): raw

    def map(self, fo):
        p, flag = struct.unpack_from("<qq", self.img, fo)
        struct.pack_into("<q", self.img, fo + 8, self.list_flag)
        if p == 0 or not self.in_image(p):
            self.put_q(fo, 0)
            return
        ho = p - self.base
        self.put_q(fo, ho)
        nb, _, buckets, items = struct.unpack_from("<iiqq", self.img, ho)
        if nb == 0:
            return
        bo = self.to_off(buckets, "map buckets")
        io = self.to_off(items, "map items")
        struct.pack_into("<qq", self.img, ho + 8, bo, io)
        n = struct.unpack_from("<i", self.img, bo + 4 * (nb - 1))[0]
        for j in range(n):
            self.object_ptr(io + 16 * j + 8)

    # --- whole image --------------------------------------------------------------------
    def run(self):
        magic, ver, table = struct.unpack_from("<IIq", self.img, 0)
        assert magic == 0x53545544 and ver == 3, "not an STUD v3 image"
        to = table - self.base
        struct.pack_into("<q", self.img, 8, to)
        count, _, entries = struct.unpack_from("<IIq", self.img, to)
        eo = entries - self.base
        struct.pack_into("<q", self.img, to + 8, eo)
        crc2type = {int(t["layout_crc"], 16): h for h, t in self.reg.items()}
        for i in range(count):
            off, flags, crc = struct.unpack_from("<IIQ", self.img, eo + 16 * i)
            h = crc2type.get(crc)
            if h is None:
                raise SystemExit("instance %d: unknown layout CRC %016x" % (i, crc))
            self.object(off, h)
        return bytes(self.img)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("image")
    ap.add_argument("out")
    ap.add_argument("--registry", default=REG_PATH)
    ap.add_argument("--list-flag", type=int, default=0)
    a = ap.parse_args()
    reg = json.load(open(a.registry))
    img = open(a.image, "rb").read()
    meta = json.load(open(a.image + ".json"))
    u = Unfixer(reg, img, meta["base"], a.list_flag)
    out = u.run()
    open(a.out, "wb").write(out)
    for w in u.warnings:
        print("warning:", w, file=sys.stderr)
    print("%s: %d bytes -> %s" % (a.image, len(out), a.out), file=sys.stderr)


if __name__ == "__main__":
    main()
