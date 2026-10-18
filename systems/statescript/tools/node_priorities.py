#!/usr/bin/env python3
"""Priority list of Statescript node types for the runtime (T054, docs/StatescriptNodes.md §2).

Input: the census of all 370 graphs (build/ss_census over CASC graph bytes, content_dump --read), the heroes'
start-up graphs (systems/data/heroes/heroes.json), the brains' and objectives' graphs (docs/MatchLogic.md), the
client type table (catalogue) and the runtime's capabilities (build/ss_census --caps is not needed: the levels
come from `ss_census --caps`).
Per graph set (heroes, brains, objectives) the graphs are closed over their references (SubScript /
PlayScript / graph config vars). Output: a JSON table + a markdown table on stdout.
usage: tools/node_priorities.py census.jsonl caps.json out.json [--doc docs/StatescriptNodes.md] > table.md
(--doc rewrites the priority and coverage tables between their markers in the doc)"""
import collections, json, os, sys

here = os.path.dirname(os.path.abspath(__file__))
data = os.path.join(here, "../../data")
census = {j["graph"]: j for j in map(json.loads, open(sys.argv[1]))}
caps = {c["type"]: c for c in json.load(open(sys.argv[2]))}
cat = {t["hash"]: t for t in json.load(open(os.path.join(data, "statescript/statescript_node_catalogue.json")))}
heroes = json.load(open(os.path.join(data, "heroes/heroes.json")))["heroes"]
KIND = ["Entry", "Condition", "Action", "Terminator", "State", "Other"]

def G(short):
    return "0x%016x" % (0x0580000000000000 | int(short, 16))

def closure(roots):
    seen, todo = [], list(roots)
    while todo:
        g = todo.pop(0)
        if g in seen or g not in census:
            continue
        seen.append(g)
        todo += census[g]["refs"]
    return seen

sets = {}
for h in heroes:
    sets["hero:" + h["name"]] = closure([G(x["graph"]) for x in h["graphs"]])
BRAINS = {"brain A 0x13": ["0x00f", "0x014"], "brain B 0x39": ["0x029", "0x08f", "0x1e1"],
          "brain Dorado 0x12e3": ["0x10f", "0x1e1", "0x219"], "brain tutorial 0x128E": ["0xb41", "0xb55", "0xb56",
          "0xb57", "0xb58", "0xb59"]}
OBJECTIVES = {"capture point 0x028": ["0x028"], "payload 0x098/0xa33": ["0x098", "0xa33"], "door 0x042": ["0x042"],
              "spawn point 0x0d6": ["0x0d6"]}
for k, v in {**BRAINS, **OBJECTIVES}.items():
    sets[k] = closure([G(x) for x in v])

# Per type: instances per set (server-relevant = not client-only), heroes that use it.
rows = collections.defaultdict(lambda: {"hero_inst": 0, "heroes": set(), "brain_inst": 0, "brains": set(),
                                        "obj_inst": 0, "objs": set(), "all_inst": 0})
for g, j in census.items():
    for t, kind, flags in j["nodes"]:
        rows[t]["all_inst"] += 1
        rows[t]["kind"] = KIND[kind]
for name, graphs in sets.items():
    key = {"hero": "heroes", "brain": "brains"}.get(name.split(":")[0].split(" ")[0], "objs")
    for g in graphs:
        for t, kind, flags in census[g]["nodes"]:
            if not flags & 1:  # client-only nodes never run on a server
                rows[t][key].add(name.split(":")[-1])
# instance counts: each graph once per group
for group, pred in (("hero", lambda n: n.startswith("hero:")), ("brain", lambda n: n.startswith("brain")),
                    ("obj", lambda n: not n.startswith("hero:") and not n.startswith("brain"))):
    graphs = sorted({g for n, gs in sets.items() if pred(n) for g in gs})
    for g in graphs:
        for t, kind, flags in census[g]["nodes"]:
            if not flags & 1:
                rows[t][group + "_inst"] += 1

out = []
for t, r in rows.items():
    c = cat.get(t, {})
    cap = caps.get(t, {})
    # Weight: a type blocks a hero / mode when any of its instances is on a server path; heroes weigh most
    # (Phase A), then brains (Phase B), then objectives.
    score = 10 * len(r["heroes"]) + 2 * r["hero_inst"] + 8 * len(r["brains"]) + r["brain_inst"] + 6 * len(r["objs"]) + r["obj_inst"]
    out.append({"type": t, "name": c.get("name"), "kind": r.get("kind"), "client_class": bool(c.get("runtime")) or
                (c.get("impl_from") != t and c.get("impl_from_name") not in (None, "STUStatescriptState", "STUStatescriptAction")),
                "impl_from": c.get("impl_from_name"), "level": cap.get("level", 0), "heroes": sorted(r["heroes"]),
                "hero_inst": r["hero_inst"], "brains": sorted(r["brains"]), "brain_inst": r["brain_inst"],
                "objs": sorted(r["objs"]), "obj_inst": r["obj_inst"], "all_inst": r["all_inst"], "score": score})
out.sort(key=lambda x: -x["score"])
json.dump({"sets": {k: v for k, v in sets.items()}, "types": out}, open(sys.argv[3], "w"), indent=1)
print("| # | type | name | kind | client class | level | heroes | hero inst | brains | brain inst | objectives | obj inst | score |")
print("|---|---|---|---|---|---|---|---|---|---|---|---|---|")
for i, x in enumerate(out):
    if x["score"] == 0:
        continue
    print(f"| {i+1} | `{x['type']}` | {x['name'] or '?'} | {x['kind']} | {'yes' if x['client_class'] else '**no**'} | {x['level']} | "
          f"{len(x['heroes'])} | {x['hero_inst']} | {len(x['brains'])} | {x['brain_inst']} | {len(x['objs'])} | {x['obj_inst']} | {x['score']} |")


def table_rows():
    rows = ["| # | type | name | kind | client class | level | heroes / inst | brains / inst | objectives / inst |",
            "|---|---|---|---|---|---|---|---|---|"]
    n = 0
    for x in out:
        if x["score"] == 0 or n >= 90:
            continue
        n += 1
        rows.append(f"| {n} | `{x['type']}` | {x['name'] or '?'} | {x['kind']} | {'yes' if x['client_class'] else '**no**'} | "
                    f"{x['level']} | {len(x['heroes'])} / {x['hero_inst']} | {len(x['brains'])} / {x['brain_inst']} | "
                    f"{len(x['objs'])} / {x['obj_inst']} |")
    return rows


def coverage_rows():
    import re
    doc = open(sys.argv[sys.argv.index("--doc") + 1]).read()
    specd = set(re.findall(r"^### 3\.\d+ .*?\(`([0-9a-f]{8})`", doc, re.M))
    specd |= set(re.findall(r"^### 3\.\d+ .*?`([0-9a-f]{8})`", doc, re.M))
    LV = ["unmodelled", "presentation", "designed", "exact", "verified"]
    DC = ["–", "writer", "echo"]
    rows = ["| level | types | server-path instances (heroes + brains + objectives) |", "|---|---|---|"]
    used = [x for x in out if x["score"] > 0]
    for lv in range(4, -1, -1):
        xs = [x for x in used if x["level"] == lv]
        rows.append(f"| {lv} {LV[lv]} | {len(xs)} | {sum(x['hero_inst'] + x['brain_inst'] + x['obj_inst'] for x in xs)} |")
    rows += ["", "| type | name | kind | level | local data | remote data | spec |", "|---|---|---|---|---|---|---|"]
    for x in used:
        c = caps.get(x["type"], {})
        if x["level"] == 0 and x["type"] not in specd:
            continue
        rows.append(f"| `{x['type']}` | {x['name'] or '?'} | {x['kind']} | {x['level']} {LV[x['level']]} | "
                    f"{DC[c.get('local', 0)]} | {DC[c.get('remote', 0)]} | {'§3' if x['type'] in specd else ''} |")
    rows.append("")
    rows.append(f"Unmodelled (level 0) on server paths: {sum(1 for x in used if x['level'] == 0)} types; see the priority list.")
    return rows


if "--doc" in sys.argv:
    path = sys.argv[sys.argv.index("--doc") + 1]
    doc = open(path).read()
    for name, rows in (("priority table", table_rows()), ("coverage table", coverage_rows())):
        a = doc.index(f"<!-- {name}: tools/node_priorities.py --doc -->")
        a = doc.index("\n", a) + 1
        b = doc.index(f"<!-- /{name} -->")
        doc = doc[:a] + "\n".join(rows) + "\n" + doc[b:]
    open(path, "w").write(doc)
