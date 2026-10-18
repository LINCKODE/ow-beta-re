import json, sys, subprocess, re
sys.path.insert(0, '.')
from cstore import *
from screens import typ
def strat(b, s):
    if isinstance(s, str): return s
    if isinstance(s, dict) and "$error" in s:
        o = int(re.search(r"0x[0-9a-f]+", s["$error"]).group(0), 16)
        n = int.from_bytes(b[o:o+4], "little"); return b[o+16:o+16+n].decode("utf-8", "replace")
    return None
def keymap(guid):
    b = read(guid)
    if not b: return []
    open('_k.stu', 'wb').write(b)
    try: R = json.loads(subprocess.run(["systems/stu/build/stu_dump", "_k.stu"], capture_output=True, text=True).stdout)["root"]
    except Exception: return []
    out = []
    for r in R:
        f = r["value"].get("$fields", {}) if isinstance(r["value"], dict) else {}
        if "m_keys" in f and "m_values" in f:
            for k, v in zip(f["m_keys"], f["m_values"]):
                ko = R[k["$instance"]]["value"]["$fields"]; vo = R[v["$instance"]]["value"]
                name = strat(b, ko.get("m_value"))
                vf = vo.get("$fields", {})
                if "m_linkGUID" in vf: out.append((int(vf["m_linkGUID"]["$guid"], 16), name, "link"))
                elif "m_displayText" in vf: out.append((int(vf["m_displayText"]["$guid"], 16), name, "text"))
    return out
if __name__ == "__main__":
    names = {}; texts = []
    for t in (typ(0x09a), typ(0x0ba)):
        for g in of_type(t):
            for gid, name, kind in keymap(g):
                if kind == "link" and gid: names.setdefault(gid, set()).add(name)
                if kind == "text": texts.append((g, name, gid))
    with open("uivars_keys.tsv", "w") as f:
        for k in sorted(names): f.write(f"{k:#018x}\t{'/'.join(sorted(names[k]))}\n")
    with open("screen_texts.tsv", "w") as f:
        for g, n, t in texts: f.write(f"{g:#018x}\t{n}\t{t:#018x}\n")
    print(len(names), len(texts))
