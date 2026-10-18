#!/usr/bin/env python3
"""Triage of the client's Statescript node classes (T054, docs/StatescriptNodes.md §2): for every state / action /
condition / entry type with a client class, which virtual hooks its class overrides against the base class, read
from the vtables in the treated binary (pefile). A state class that overrides nothing but presentation hooks runs
as the base state; one that overrides begin / timer / tick / var-changed / … has behaviour to read in Ghidra.
usage: tools/class_hooks.py [type …]   (JSON on stdout with --json)"""
import json, os, struct, sys

here = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(here, "../../.."))
BIN = os.path.join(ROOT, "RE-binary/GameClientApp_dump_SCY.exetreated.exe")
cat = json.load(open(os.path.join(ROOT, "systems/data/statescript/statescript_node_catalogue.json")))
byh = {t["hash"]: t for t in cat}

# State vfuncs (docs/StatescriptRuntime.md §1.8, §3.6).
STATE_SLOTS = {0xD0: "check_exits", 0xF8: "execute", 0x100: "init", 0x108: "begin_pre", 0x110: "begin",
               0x118: "reenter", 0x120: "tick", 0x128: "?128", 0x130: "frame_tick", 0x138: "?138", 0x140: "timer",
               0x148: "finish_hook", 0x150: "abort_hook", 0x158: "end_hook", 0x160: "activate", 0x168: "deactivate",
               0x170: "var_changed", 0x178: "var_dep_changed", 0x180: "lost_ownership", 0x188: "?188", 0x190: "elapsed",
               0x198: "?198", 0x1A0: "?1a0", 0x1A8: "cache_flags", 0x1B0: "?1b0", 0x1B8: "?1b8", 0x1C0: "?1c0",
               0x1C8: "logical_button", 0x1D0: "sched_begin", 0x1D8: "sched_abort", 0x1E0: "?1e0", 0x1E8: "eval_exits",
               0x1F0: "write_data", 0x1F8: "read_data", 0x200: "?200", 0x208: "mirror?", 0x210: "keep_listed",
               0x218: "subscribe", 0x220: "unsubscribe", 0x228: "state_value", 0x230: "?230", 0x238: "?238",
               0x240: "handles_input", 0x248: "trav_abort", 0x250: "trav_finished",
               # past the base vtable: class-specific (stack members: +0x258 fold, +0x260 became top, +0x268 under)
               0x258: "x258", 0x260: "x260", 0x268: "x268", 0x270: "x270", 0x278: "x278", 0x280: "x280", 0x288: "x288"}
NODE_SLOTS = {0xD0: "?d0", 0xF0: "evaluate", 0xF8: "execute", 0x100: "do_action", 0x108: "?108", 0x110: "?110",
              0x118: "?118", 0x120: "?120", 0x128: "pre_action", 0x130: "?130"}
BASES = {"State": "a7213568", "Action": "2ecefc8b", "Terminator": "2ecefc8b", "Condition": "6f10080f", "Entry": "2f937ae3"}


class PE:
    def __init__(self, path):
        import pefile
        pe = pefile.PE(path, fast_load=True)
        self.img, self.ib = pe.get_memory_mapped_image(), pe.OPTIONAL_HEADER.ImageBase
        text = next(s for s in pe.sections if s.Name.startswith(b".text"))
        self.text = (text.VirtualAddress, text.VirtualAddress + text.Misc_VirtualSize)

    def slot(self, vt, off):
        v = struct.unpack_from("<Q", self.img, vt + off)[0] - self.ib
        return v if self.text[0] <= v < self.text[1] else None


def winner(t):
    """The class the client instantiates for type t (exact, else nearest registered ancestor)."""
    p = t
    while p is not None and not p["runtime"]:
        p = byh.get(p.get("parent"))
    return p


def vtable(t):
    r = max(t["runtime"], key=lambda r: r["prio"])
    return int(r["vtable"], 16)


def main():
    pe = PE(BIN)
    want = [a for a in sys.argv[1:] if not a.startswith("--")]
    out = []
    for t in cat:
        if t["kind"] not in BASES or (want and t["hash"] not in want):
            continue
        w = winner(t)
        if w is None or w["hash"] == BASES[t["kind"]] and not want:
            continue
        base = byh[BASES[t["kind"]]]
        while base and not base["runtime"]:  # a base without a class of its own: compare with its ancestors'
            base = byh.get(base["parent"])
        slots = STATE_SLOTS if t["kind"] == "State" else NODE_SLOTS
        vt, bvt = vtable(w), vtable(base) if base else None
        over = {}
        for off, name in slots.items():
            a, b = pe.slot(vt, off), pe.slot(bvt, off) if bvt else None
            if a is not None and a != b:
                over[name] = "0x%x" % a
        out.append({"type": t["hash"], "name": t["name"], "kind": t["kind"], "class": w["hash"],
                    "vtable": "0x%x" % vt, "uses": t["instances"], "overrides": over})
    if "--json" in sys.argv:
        json.dump(out, sys.stdout, indent=1)
        return
    for o in out:
        print(f"{o['type']} {o['name'] or '?'} ({o['kind']}, class {o['class']} vt {o['vtable']}, {o['uses']} uses)")
        print("    " + ", ".join(f"{k} {v}" for k, v in o["overrides"].items()))


main()
