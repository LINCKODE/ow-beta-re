#!/usr/bin/env python3
"""Config-var evaluator catalogue (T038): docs/StatescriptConfigVars.md §4.

Merges, per STUConfigVar type:
  - statescript_node_catalogue.json (name, parent, fields, client runtime class, resident uses),
  - the client binary: the evaluator vtable's GetValue (+0xD0) and DependsOnExternal (+0xD8) slots,
  - usage over all graphs of the build (stu_dump JSONs in a directory),
  - configvar_semantics.json (what each evaluator computes; hand/agent-read from the decompiles),
and writes ../configvar_catalogue.json (+ prints a markdown table with --md).

usage (from a work dir with a venv that has pefile + capstone, see README.md):
  configvar_catalogue.py GRAPH_JSON_DIR [--md]
GRAPH_JSON_DIR: `content_dump --list 1B` → `content_dump --read` → `stu_dump` of every graph.
"""
import collections, glob, json, os, sys

HERE = os.path.dirname(os.path.abspath(__file__))
DATA = os.path.dirname(HERE)
sys.path.insert(0, HERE)
from bin import q, IB  # noqa: E402  (RE-binary reader, capstone/pefile)

SHARED = {  # GetValue functions shared by many types (read in Ghidra, T038)
    "0xf69420": dict(output="INT64", computes="the GUID of the referenced asset (the reference's +0x08, i.e. cv+0x18)",
                     reads="literal", family="resource/asset", tag="G"),
    "0xd99240": dict(output="INT", computes="the enum / int field at cv+0x10", reads="literal", family="literal", tag="G"),
    "0xd92900": dict(output="BYTE", computes="constant false (BYTE 0)", reads="literal", family="literal", tag="G"),
    "0x6119c0": dict(output="none", computes="no client evaluator: GetValue returns false, so every getter yields its default",
                     reads="", family="server-only", tag="G"),
    "0xf5c330": dict(output="varies", computes="the value of the first element of m_configVars (false if empty); "
                     "as an iterable it yields all elements, nested arrays flattened (depth ≤ 4)",
                     reads="pure", family="math/logic", tag="G"),
    "0xf5a6e0": dict(output="varies", computes="iterable: GetValue tail-calls the evaluator's own slot +0xE0 (type-specific)",
                     reads="", family="other", tag="G"),
    "0xf6adc0": dict(output="FLT", computes="float-only expression interpreter (§3)", reads="pure", family="math/logic", tag="G"),
    "0xf6c7b0": dict(output="varies", computes="typed expression interpreter (§3)", reads="pure", family="math/logic", tag="G"),
}


def main():
    gdir = sys.argv[1]
    cat = json.load(open(f"{DATA}/statescript_node_catalogue.json"))
    byh = {c["hash"]: c for c in cat}
    sem = {}
    sp = f"{DATA}/configvar_semantics.json"
    if os.path.exists(sp):
        sem = {r["hash"]: r for r in json.load(open(sp))}

    use, graphs = collections.Counter(), collections.defaultdict(set)
    for f in glob.glob(f"{gdir}/*.json"):
        for x in json.load(open(f))["root"]:
            t = x["value"].get("$type", "")
            use[t] += 1
            graphs[t].add(os.path.basename(f))

    out = []
    for c in cat:
        if c["kind"] != "ConfigVar":
            continue
        keys = ["0x" + c["hash"]] + ([c["name"]] if c.get("name") else [])
        n = sum(use[k] for k in keys)
        g = len(set().union(*[graphs[k] for k in keys]))
        impl = byh.get(c["impl_from"], {})
        rts = sorted(impl.get("runtime") or [], key=lambda r: -r["prio"])
        gv = d8 = vt = None
        if rts:
            vt = int(rts[0]["vtable"], 16)
            gv, d8 = "0x%x" % (q(vt + 0xD0) - IB), "0x%x" % (q(vt + 0xD8) - IB)
        row = dict(hash=c["hash"], name=c.get("name"), name_confidence=c.get("name_confidence"),
                   parent=c.get("parent_name") or c.get("parent"), stu_size=c["stu_size"], fields=c["fields"],
                   client_class=c["impl_from"] == c["hash"], evaluator_vtable=hex(vt) if vt else None,
                   getvalue=gv, depends_on_external=d8,
                   uses_resident=c["instances"], graphs_resident=c["graphs"], uses_all=n, graphs_all=g)
        s = dict(SHARED.get(gv, {}))
        s.update({k: v for k, v in sem.get(c["hash"], {}).items() if k != "hash" and v not in (None, "")})
        for k in ("output", "inputs", "computes", "reads", "source", "family", "tag", "calls", "notes"):
            row[k] = s.get(k, "")
        out.append(row)
    out.sort(key=lambda r: (-r["uses_all"], r["hash"]))
    json.dump(out, open(f"{DATA}/configvar_catalogue.json", "w"), indent=1)
    used = [r for r in out if r["uses_all"]]
    print(f"{len(out)} types, {len(used)} used in some graph, "
          f"{sum(1 for r in out if r['uses_resident'])} resident; "
          f"{sum(1 for r in used if not r['output'])} used types without semantics", file=sys.stderr)
    if "--md" in sys.argv:
        md(used)


def md(rows):
    fam = collections.defaultdict(list)
    for r in rows:
        fam[r["family"] or "?"].append(r)
    order = ["literal", "var access", "math/logic", "entity", "team", "hero/player", "match/gamemode", "time",
             "input", "UI", "resource/asset", "other", "server-only", "?"]
    for f in sorted(fam, key=lambda x: order.index(x) if x in order else 99):
        print(f"\n#### {f} ({len(fam[f])})\n")
        print("| hash | name | inputs | → | computes | reads | class | uses | tag |")
        print("|---|---|---|---|---|---|---|---|---|")
        for r in sorted(fam[f], key=lambda r: -r["uses_all"]):
            nm = r["name"] or ""
            if r.get("name_confidence") not in (None, "exact"):
                nm = f"*{nm}*" if nm else ""
            cls = "yes" if r["client_class"] else "**no**"
            cell = lambda s: (s or "").replace("|", "\\|").replace("\n", " ")
            print(f"| `{r['hash']}` | {nm.replace('STUConfigVar', '')} | {cell(r['inputs'])} | {r['output']} | "
                  f"{cell(r['computes'])} | {cell(r['reads'])} | {cls} | {r['uses_resident']}/{r['uses_all']} | "
                  f"{r['tag']} |")


if __name__ == "__main__":
    main()
