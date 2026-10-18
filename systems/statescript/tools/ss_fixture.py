#!/usr/bin/env python3
"""Turn a raw Prometheus `ss_trace` save (JSON lines) into a per-entity Statescript fixture.

usage: ss_fixture.py <raw.jsonl> <entity id> <out.jsonl> [--graph 0x058...] [--note text]

Output: JSON lines. Line 1 is a header {"fixture": ..., "entity": ..., "graph": ..., "note": ...};
then one event per line with decoded fields (docs/StatescriptRuntime.md §4):
  cf        {cf, n, instances}         RunCommandFrames(cf, n) of the entity's sync manager starts
  cf_end    {cf}                        ... ends
  notif     {type, time, text}          a notification is executed (time = its scheduled ms)
  begin / reenter / finish / abort      {state, node}  (m_states / m_nodes index) ; *_done after
  link      {src, plug, idx, dst}       TraverseLink: source node, plug offset in the source STU node,
                                        transition index (-1 = none), target node ; link_done {dst, ret}
  instantiate {guid, parent, flags} / instantiate_done {flags, queued}
  message   {id, result, sender, instigator}
  var       {bag, id, value}            value changed during the CF (bag = inst | ent | inst:<child>...)
Every event also has seq (global order), cf and ms (the sync manager clock when it happened), inst.
"""
import json, sys

def decode(e):
    k = e["kind"]; a, b, c = e["a"], e["b"], e["c"]
    d = {"seq": e["seq"], "kind": k, "cf": e["cf"], "ms": e["ms"], "inst": e["inst"]}
    if k == "cf": d.update(cf=a, n=b, instances=c)
    elif k == "cf_end": d.update(cf=a)
    elif k == "notif": d.update(type=a, time=b, text=e.get("s", ""))
    elif k in ("begin", "reenter", "finish", "abort"): d.update(state=a, node=b, **({"offset": c} if k in ("begin", "reenter") else {}))
    elif k in ("finish_done", "abort_done"): d.update(state=a)
    elif k == "link":
        d.update(src=a, plug=b & 0xFFFFFFFF, idx=((b >> 32) + 2**31) % 2**32 - 2**31, dst=c)
    elif k == "link_done": d.update(dst=a, ret=b)
    elif k == "instantiate": d.update(guid=f"0x{a & (2**64-1):016x}", parent=b, flags=f"0x{c:x}")
    elif k == "instantiate_done": d.update(flags=f"0x{a:x}", queued=b)
    elif k == "message": d.update(id=f"0x{a & (2**64-1):016x}", result=b, sender=f"0x{c & 0xFFFFFFFF:x}", instigator=f"0x{(c >> 32) & 0xFFFFFFFF:x}")
    elif k == "var":
        s = e["s"]
        if s.endswith(" removed"): bag_id, val = s[:-8], None
        else: bag_id, val = s.split("=", 1)
        bag, vid = bag_id.rsplit(":", 1)
        d.update(bag=bag, id=vid, value=val)
    elif k == "notif_requeued": pass
    return d

def main(a):
    if len(a) < 3: sys.exit(__doc__)
    raw, ent, out = a[0], a[1].lower(), a[2]
    graph = note = None
    if "--graph" in a: graph = a[a.index("--graph") + 1]
    if "--note" in a: note = a[a.index("--note") + 1]
    evs = [json.loads(l) for l in open(raw)]
    evs = [e for e in evs if e["ent"].lower() == ent]
    with open(out, "w") as f:
        f.write(json.dumps({"fixture": "statescript-trace/1", "entity": ent, "graph": graph, "note": note,
                            "source": raw.split("/")[-1], "events": len(evs)}) + "\n")
        for e in evs: f.write(json.dumps(decode(e)) + "\n")
    print(out, len(evs))

if __name__ == "__main__": main(sys.argv[1:])
