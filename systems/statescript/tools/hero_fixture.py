#!/usr/bin/env python3
"""Hero press fixture: a client snapshot + an ss_trace of one press → a flat text fixture for the replay test.

    hero_fixture.py <snapshot.json> <raw.jsonl> <out.fixture> [--note "..."] [--from CF] [--extern bag:id,…]

<snapshot.json> = Prometheus `ss_serialize_local {"entity_id": body}` taken while the hero is idle, with the
`ss_trace` of <raw.jsonl> already running on the body (so the trace's first per-CF var diff holds every var).
With --from CF the trace may hold earlier events (the ss_trace buffer is not cleared by start): the vars
are the trace's first full dump with every later change before CF applied, and only presses from CF on count.
--extern lists vars written by client code outside Statescript (e.g. ent:1a1 — the movement system's owner
flags): their changes in the window become `extern` inputs applied before that CF, not expectations.
The fixture replays the press on the imported state and compares with the client's prediction up to the CF
where the client replays or drops it (the next `down 1` of the button, or the rollback that undoes the press).

Lines (space separated; `#` comments):
    entity <id>
    graph <guid>                                   graphs to load (the snapshot's instances)
    instance <id> <graph guid> <parent id>
    var <inst> <var id hex> <TYPE> <payload>       the first diff: TYPE payload as ss_trace prints it (inst 0 = owner bag)
    state <inst> <m_states index> <nbits> <hex>    active states with their local-format data (LSB-first bits)
    clock <cf>                                     the sync manager clock: the CF before the first input
    button <cf> <button id> <down 0|1> <inst,...>  a LogicalButton notification (recipients as recorded)
    until <cf>                                     last CF to run and compare
    expect <cf> <inst> <begin|finish|abort|reenter> <state>
    expect <cf> <inst> var <bag:id=TYPE payload>   per-CF var changes (bag = inst / ent)
    extern <cf> <inst> <var id hex> <TYPE> <payload>  a write by client code, applied before the CF runs
"""
import json
import sys


def main(argv):
    if len(argv) < 4:
        print(__doc__)
        return 2
    snap = json.load(open(argv[1]))
    ev = [json.loads(l) for l in open(argv[2])]
    note = argv[argv.index("--note") + 1] if "--note" in argv else ""
    start = int(argv[argv.index("--from") + 1]) if "--from" in argv else None
    extern = set(argv[argv.index("--extern") + 1].split(",")) if "--extern" in argv else set()
    out = []
    out.append(f"# hero fixture from {argv[1].split('/')[-1]} + {argv[2].split('/')[-1]}")
    if note:
        out.append(f"# {note}")
    out.append(f"entity {snap['entity_id']}")
    graphs = []
    for o in snap["owner_instances"]:
        g = int(o["graph"], 16)
        if g not in graphs:
            graphs.append(g)
    for g in graphs:
        out.append(f"graph 0x{g:016x}")
    for o in snap["owner_instances"]:
        out.append(f"instance {o['id']} 0x{int(o['graph'], 16):016x} {o['parent']}")

    first_cf = min(e["cf"] for e in ev if e["kind"] == "var")
    last = first_cf if start is None else start - 1
    vals = {}
    for e in ev:
        if e["kind"] != "var" or e["cf"] > last:
            continue
        bag, rest = e["s"].split(":", 1)
        if "=" not in rest:  # "… removed"
            vals.pop((0 if bag == "ent" else e["inst"], rest.split()[0]), None)
            continue
        vid, val = rest.split("=", 1)
        vals[(0 if bag == "ent" else e["inst"], vid)] = val
    for (inst, vid), val in sorted(vals.items(), key=lambda kv: (kv[0][0], int(kv[0][1], 16))):
        out.append(f"var {inst} {vid} {val}")
    if start is not None:
        ev = [e for e in ev if e["cf"] >= start]
    for i in snap["instances"]:
        for a in i["active"]:
            out.append(f"state {i['instance']} {a['state']} {a['data_bits']} {a['data_hex'] or '-'}")

    buttons = [e for e in ev if e["kind"] == "notif" and e["a"] == 7]
    if not buttons:
        print("no button notifications in the trace")
        return 1
    press_cf = buttons[0]["cf"]
    btn = int(buttons[0]["s"].split()[1])
    # The client replays the press (a second `down 1` after an up) or drops it: compare up to the CF before.
    seen_up, until = False, max(e["cf"] for e in ev)
    for e in buttons:
        down = e["s"].split()[3] == "1"
        if not down and e["cf"] > press_cf:
            seen_up = True
        if down and seen_up:
            until = e["cf"] - 1
            break
    out.append(f"clock {press_cf - 1}")
    by_cf = {}
    for e in buttons:
        if e["cf"] > until:
            continue
        down = e["s"].split()[3]
        by_cf.setdefault((e["cf"], int(e["s"].split()[1]), down), []).append(str(e["inst"]))
    for (cf, b, down), insts in sorted(by_cf.items()):
        out.append(f"button {cf} {b} {down} {','.join(insts)}")
    out.append(f"until {until}")
    for e in ev:
        if not (press_cf <= e["cf"] <= until):
            continue
        if e["kind"] in ("begin", "finish", "abort", "reenter"):
            out.append(f"expect {e['cf']} {e['inst']} {e['kind']} {e['a']}")
        elif e["kind"] == "var":
            key = e["s"].split("=", 1)[0]
            if key in extern:
                bag, vid = key.split(":")
                out.append(f"extern {e['cf']} {0 if bag == 'ent' else e['inst']} {vid} {e['s'].split('=', 1)[1]}")
            else:
                out.append(f"expect {e['cf']} {e['inst']} var {e['s']}")
    open(argv[3], "w").write("\n".join(out) + "\n")
    print(f"{argv[3]}: {len(graphs)} graphs, press button {btn} at CF {press_cf}, compare to CF {until}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
