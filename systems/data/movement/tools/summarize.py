#!/usr/bin/env python3
"""Compact per-CF view of a recording: cf, input (fwd/right/buttons), flags, pos, velocity (+0x230). Prints changes only."""
import json, sys
d = json.load(open(sys.argv[1])); last = None
for f in d["frames"]:
    i = f["input"] or {}; r = f["record"]
    row = (i.get("forward"), i.get("right"), hex(i.get("buttons", 0)), hex(r["flags"]),
           tuple(round(x, 3) for x in r["pos"]), tuple(round(x, 3) for x in r["v230"]))
    if row != last:
        print(f["cf"], *row)
    last = row
print("gaps", d["gaps"])
