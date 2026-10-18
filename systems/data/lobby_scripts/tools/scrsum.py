import sys, json, subprocess, collections; sys.path.insert(0,'.')
from screens import *
SC = {int(k,16): [int(x,16) for x in v] for k,v in json.load(open('screens_refs.json')).items()}
def summary(sid):
    b = read(sid)
    if b is None: return f"{sid:#x}: unreadable"
    open('_s.stu','wb').write(b)
    j = json.loads(subprocess.run(["systems/stu/build/stu_dump", "_s.stu"], capture_output=True, text=True).stdout)
    types = collections.Counter(r["type"] for r in j["root"])
    refs = SC.get(sid, [])
    strs = [S[r] for r in refs if r in S]
    uis = [U.get(r, f"ui{r & 0xffff:04x}") for r in refs if r >> 52 == 0x0ac]
    subs = [f"{r & 0xffff:04x}" for r in refs if r >> 52 == 0x09a]
    ba = [f"{r & 0xffff:04x}" for r in refs if r >> 52 in (0x0ba,)]
    el = [t.replace("STUUX", "") for t, _ in types.most_common() if t.startswith("STUUX") and t not in ("STUUXString", "STUUXScreen")][:10]
    return f"{sid & 0xffff:04x} ({len(b)} B)\n  strings: {strs}\n  uivars: {uis}\n  subscreens: {subs} dicts: {ba}\n  elements: {el}"
if __name__ == "__main__":
    for a in sys.argv[1:]: print(summary(int(a, 16) | 0x09a0000000000000))
