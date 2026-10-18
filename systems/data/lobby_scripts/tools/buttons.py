import json, sys, subprocess
sys.path.insert(0, '.')
from screens import S, U
from cstore import read
def load(sid):
    open('_b.stu', 'wb').write(read(sid))
    return json.loads(subprocess.run(["systems/stu/build/stu_dump", "_b.stu"], capture_output=True, text=True).stdout)["root"]
def collect(R, v, out, depth=0, stop=("STUUXButton", "STUUXRadioButton")):
    if depth > 40: return
    if isinstance(v, dict):
        if "$instance" in v:
            r = R[v["$instance"]]
            if depth > 0 and r["type"] in stop: return
            collect(R, r["value"], out, depth + 1, stop); return
        if "$guid" in v:
            g = int(v["$guid"], 16)
            if g >> 52 == 0x0de: out.append('"' + S.get(g, hex(g)) + '"')
            elif g >> 52 == 0x0ac: out.append(U.get(g, f"ui{g & 0xffff:04x}"))
            elif g >> 52 == 0x09a: out.append(f"screen {g & 0xffff:04x}")
            return
        for k, x in v.items():
            if k == "m_propertyKey": continue
            collect(R, x, out, depth + 1, stop)
    elif isinstance(v, list):
        for x in v: collect(R, x, out, depth + 1, stop)
sid = int(sys.argv[1], 16) | 0x09a0000000000000
R = load(sid)
for r in R:
    if r["type"] in ("STUUXButton", "STUUXRadioButton"):
        out = []; collect(R, r["value"], out)
        seen = []; [seen.append(x) for x in out if x not in seen]
        print(f"{r['type'][5:]:12} #{r['index']}: {', '.join(seen)}")
