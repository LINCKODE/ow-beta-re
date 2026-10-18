#!/usr/bin/env python3
"""Record the local hero's movement per command frame (T062, docs/Movement.md §6).

Reads, through Prometheus MCP (read-only), the client's own per-CF state while driving inputs with
`logical_button`:
  - the input history of the controller (component 0x2f +0x810: 128 entries of 0x18 bytes) -> the command of each CF;
  - the pawn's predicted movement records (component 0x12 +0xa300 history: 64 records of 0x270 bytes at +0xa720)
    -> the record the mover produced for each CF;
  - the mover component 0x15 (0x10e0 bytes) and the 0x12 header at the start and the end.
The rings hold ~1 s (records) / ~2 s (inputs), so they are read in full every --poll seconds and merged by CF.

    record_movement.py NAME [--steps JSON] [--seconds S] [--out DIR]
    steps: [[t, "press"|"release"|"tap", "button"], ...]   (t in seconds from the start)
e.g. record_movement.py walk_forward --steps '[[0.5,"press","move_forward"],[2.5,"release","move_forward"]]' --seconds 4
"""
import argparse, json, os, struct, sys, time, urllib.request

PROM = "http://127.0.0.1:17888/mcp/tools/call"
REC, NREC, REC_OFF, HIST2 = 0x270, 64, 0xa720, 0xa300
INP, NINP, INP_OFF = 0x18, 128, 0x830


def mcp(tool, args):
    req = urllib.request.Request(PROM, data=json.dumps({"name": tool, "arguments": args}).encode(),
                                 headers={"Content-Type": "application/json"})
    j = json.loads(urllib.request.urlopen(req, timeout=10).read())
    return j.get("result", j)


def rd(addr, n, unit=1):
    """Read n bytes in MCP calls of at most 4096 bytes, each a whole number of `unit`-sized items: one call
    runs in one go on the game thread, so an item is never torn across two frames."""
    out = b""
    step = 4096 // unit * unit
    while n > 0:
        k = min(n, step)
        r = mcp("read_memory_abs", {"addr": hex(addr), "length": k})
        if "hex" not in r:
            raise RuntimeError(f"read {addr:#x}: {r}")
        out += bytes.fromhex(r["hex"].replace(" ", ""))
        addr, n = addr + k, n - k
    return out


def comp_addr(ent, cid):
    r = mcp("read_component", {"entity_id": ent, "component_id": cid, "length": 16, "ea": "game"})
    if "addr" not in r:
        raise RuntimeError(f"component {cid:#x} of {ent}: {r}")
    return int(r["addr"], 16)


def find():
    ctrl = mcp("find_entity", {"by": "local_player", "ea": "game"})["entity_id"]
    for e in mcp("list_entities", {"ea": "game"})["entities"]:
        if "0x15" in e["component_ids"] and "0x12" in e["component_ids"]:
            return ctrl, e
    raise RuntimeError("no entity with a character mover (lock in a hero first)")


def decode_input(b):
    cf, seq, yaw, pitch, right, fwd, buttons, action, _, tail = struct.unpack("<IIhhbbHBBxxH2x", b)
    return {"cf": cf, "seq": seq, "yaw": yaw, "pitch": pitch, "right": right, "forward": fwd,
            "buttons": buttons, "action": action, "tail": tail}


def decode_record(b):
    return {"cf": struct.unpack_from("<I", b, 0x1a0)[0],
            "flags": struct.unpack_from("<I", b, 0x198)[0],
            "rot": struct.unpack_from("<4f", b, 0x200), "pos": struct.unpack_from("<3f", b, 0x220),
            "v230": struct.unpack_from("<3f", b, 0x230)}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("name")
    ap.add_argument("--steps", default="[]")
    ap.add_argument("--seconds", type=float, default=3.0)
    ap.add_argument("--poll", type=float, default=0.3)
    ap.add_argument("--out", default=os.path.join(os.path.dirname(__file__), ".."))
    ap.add_argument("--note", default="")
    a = ap.parse_args()
    steps = sorted(json.loads(a.steps), key=lambda s: s[0])

    ctrl, pawn = find()
    ms, mv, lp = comp_addr(pawn["entity_id"], 0x12), comp_addr(pawn["entity_id"], 0x15), comp_addr(ctrl, 0x2f)
    start = {"mover": rd(mv, 0x10e0).hex(), "ms_head": rd(ms, 0x440).hex()}
    records, revisions, inputs, log = {}, {}, {}, []
    t0 = time.time()
    si = 0
    while True:
        now = time.time() - t0
        while si < len(steps) and steps[si][0] <= now:
            t, act, btn = steps[si][:3]
            args = {"action": act, "button": btn}
            if act == "tap":
                args["hold_ms"] = steps[si][3] if len(steps[si]) > 3 else 50
            r = mcp("logical_button", args)
            log.append({"t": round(now, 3), "step": steps[si], "result": r})
            si += 1
        ring = rd(ms + REC_OFF, NREC * REC, REC)
        for i in range(NREC):
            b = ring[i * REC:(i + 1) * REC]
            cf = struct.unpack_from("<I", b, 0x1a0)[0]
            if cf:
                vs = revisions.setdefault(cf, [])
                if not vs or vs[-1] != b:
                    vs.append(b)  # a changed record for a CF already seen = a rewind / replay
                records[cf] = b
        ib = rd(lp + INP_OFF, NINP * INP, INP)
        for i in range(NINP):
            e = decode_input(ib[i * INP:(i + 1) * INP])
            if e["cf"]:
                inputs[e["cf"]] = e
        if now >= a.seconds and si >= len(steps):
            break
        time.sleep(max(0.0, a.poll - (time.time() - t0 - now)))
    end = {"mover": rd(mv, 0x10e0).hex(), "ms_head": rd(ms, 0x440).hex()}

    cfs = sorted(records)
    frames = [{"cf": cf, "input": inputs.get(cf), "record": decode_record(records[cf]), "raw": records[cf].hex(),
               "replaced": [{"record": decode_record(b), "raw": b.hex()} for b in revisions[cf][:-1]]}
              for cf in cfs]
    gaps = [(x, y) for x, y in zip(cfs, cfs[1:]) if y != x + 1]
    out = {"name": a.name, "note": a.note, "controller": ctrl, "pawn": pawn["entity_id"], "def": pawn.get("def"),
           "addrs": {"ms": hex(ms), "mover": hex(mv), "lp": hex(lp)}, "steps": steps, "log": log,
           "start": start, "end": end, "frames": frames, "gaps": gaps}
    os.makedirs(a.out, exist_ok=True)
    path = os.path.join(a.out, f"{a.name}.json")
    json.dump(out, open(path, "w"), indent=1)
    p0, p1 = frames[0]["record"]["pos"], frames[-1]["record"]["pos"]
    nrep = sum(1 for f in frames if f["replaced"])
    print(f"{path}: {len(frames)} CFs {cfs[0]}..{cfs[-1]}, {nrep} rewritten, gaps {gaps[:5]}, inputs {len(inputs)}, pos {p0} -> {p1}")


if __name__ == "__main__":
    main()
