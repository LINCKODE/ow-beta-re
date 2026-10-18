#!/usr/bin/env python3
"""Dump the entity definitions (0x040..., type 0x003) resident in the running client (Prometheus MCP
list_stu_resources, paged) to a JSON list of GUID strings (docs/InstanceServer.md §12.15).

    systems/data/maps/tools/resident.py systems/data/maps/resident/<name>.json

Measured sets (2026-09-27, T059): title.json (title screen, fresh client), anubis.json, tutorial.json,
dorado.json (in a match on that map). mapgen derives the global set from title ∩ anubis ∩ tutorial.
"""
import json
import sys
import urllib.request

PROM = "http://127.0.0.1:17888/mcp/tools/call"


def call(name, args):
    req = urllib.request.Request(PROM, data=json.dumps({"name": name, "arguments": args}).encode(),
                                 headers={"Content-Type": "application/json"})
    return json.load(urllib.request.urlopen(req, timeout=20))


def main():
    defs, off = set(), 0
    while True:
        items = call("list_stu_resources", {"limit": 2000, "offset": off})["result"]["resources"]
        for e in items:
            rid = int(e["resource_id"], 16)
            if rid >> 48 == 0x0400:
                defs.add(rid)
        off += len(items)
        if len(items) < 2000:
            break
    print(off, "resources,", len(defs), "entity definitions", file=sys.stderr)
    with open(sys.argv[1], "w") as f:
        json.dump(sorted("0x%016x" % d for d in defs), f, indent=0)


if __name__ == "__main__":
    main()
