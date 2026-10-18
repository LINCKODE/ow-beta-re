#!/usr/bin/env python3
"""Coverage of the match / objective node families (T055, docs/StatescriptNodes.md §5.8): every node / state type
and config var on a server path of the brain and objective graph sets, with the runtime's level.

Input: the census of all graphs (ss_census over CASC graph bytes) and the capabilities (ss_census --caps, from a
build that has the T055 rows). Per set the graphs are closed over their references; client-only nodes are left
out (they never run on a server). Config vars are counted per graph (census "cvs").
usage: tools/match_coverage.py census.jsonl caps.json [--doc docs/StatescriptNodes.md] > table.md
(--doc rewrites the table between the "match coverage table" markers)"""
import collections, json, os, sys

here = os.path.dirname(os.path.abspath(__file__))
data = os.path.join(here, "../../data")
census = {int(j["graph"], 16) & 0xFFF: j for j in map(json.loads, open(sys.argv[1]))}
caps = {c["type"]: c for c in json.load(open(sys.argv[2]))}
cat = {t["hash"]: t for t in json.load(open(os.path.join(data, "statescript/statescript_node_catalogue.json")))}
KIND = ["Entry", "Condition", "Action", "Terminator", "State", "Other"]
LEVEL = ["0 unmodelled", "1 presentation", "2 designed", "3 exact", "4 verified"]
SETS = {  # docs/MatchLogic.md §10–13, Maps.md §4–6
    "brain A": [0x00f, 0x014], "brain B": [0x029, 0x08f, 0x1e1], "Dorado": [0x10f, 0x1e1, 0x219],
    "tutorial": [0xb41, 0xb40, 0xb55, 0xb56, 0xb57, 0xb58, 0xb59], "capture": [0x028], "payload": [0x098, 0xa33],
    "waypoint": [0x0d2], "spawn": [0x0d6], "spawn room": [0x1ce], "heal": [0x0a7], "door": [0x042]}

def closure(roots):
    seen, todo = [], list(roots)
    while todo:
        g = todo.pop(0)
        if g in seen or g not in census:
            continue
        seen.append(g)
        todo += [int(r, 16) & 0xFFF for r in census[g]["refs"]]
    return seen

rows = collections.defaultdict(lambda: collections.Counter())
kinds = {}
for name, roots in SETS.items():
    for g in closure(roots):
        for t, kind, flags in census[g]["nodes"]:
            if not flags & 1:
                rows[t][name] += 1
                kinds[t] = KIND[kind]
        for t, n in census[g]["cvs"].items():
            if t in cat and cat[t]["kind"] == "ConfigVar":
                rows[t][name] += n
                kinds[t] = "ConfigVar"

def level(t):
    c = caps.get(t)
    return c["level"] if c else 0

out = []
tot = collections.Counter()
for t, cnt in sorted(rows.items(), key=lambda kv: (-level(kv[0]) if False else 0, -sum(kv[1].values()))):
    lv = level(t)
    tot[lv] += sum(cnt.values())
    name = (cat.get(t, {}).get("name") or "?").replace("STUStatescript", "").replace("STUConfigVar", "CV ")
    sets = ", ".join(f"{k} {v}" for k, v in cnt.items())
    out.append(f"| `{t}` | {name} | {kinds[t]} | {LEVEL[lv]} | {sum(cnt.values())} | {sets} |")
summary = ["| level | instances (nodes + config vars, server paths of the sets) |", "|---|---|"]
for lv in range(4, -1, -1):
    summary.append(f"| {LEVEL[lv]} | {tot[lv]} |")
table = "\n".join(summary + ["", "| type | name | kind | level | instances | per set |", "|---|---|---|---|---|---|"] + out)
if "--doc" in sys.argv:
    doc = sys.argv[sys.argv.index("--doc") + 1]
    s = open(doc).read()
    a, b = "<!-- match coverage table: tools/match_coverage.py --doc -->", "<!-- /match coverage table -->"
    i, j = s.index(a) + len(a), s.index(b)
    open(doc, "w").write(s[:i] + "\n" + table + "\n" + s[j:])
print(table)
