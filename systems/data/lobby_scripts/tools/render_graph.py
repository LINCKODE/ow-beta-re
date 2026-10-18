#!/usr/bin/env python3
"""Render an offline Statescript graph (stu_dump JSON) as readable text.

usage: render.py <graph.txt from stu_dump> [--full]
"""
import csv, json, sys, os

ROOT = "."
TYPES, FIELDS = {}, {}
for line in open(f"{ROOT}/systems/stu/names/KnownTypes.csv"):
    p = [x.strip() for x in line.split(",")]
    if len(p) >= 2: TYPES[p[0].lower()] = p[1]
for line in open(f"{ROOT}/systems/stu/names/KnownFields.csv"):
    p = [x.strip() for x in line.split(",")]
    if len(p) >= 2: FIELDS[p[0].lower()] = p[1]
for c in json.load(open(f"{ROOT}/systems/data/statescript/statescript_node_catalogue.json")):
    if c.get("name"): TYPES[c["hash"].lower()] = c["name"]

def tname(t):
    if t.startswith("0x"):
        return TYPES.get(t[2:].lower(), t)
    return t

def short(t):
    for p in ("STUStatescriptState", "STUStatescriptAction", "STUStatescriptCondition_", "STUStatescript", "STUConfigVar"):
        if t.startswith(p) and len(t) > len(p): return t[len(p):]
    return t

def fname(f):
    if f.startswith("0x"): return FIELDS.get(f[2:].lower(), f)
    return f

NIDX = {}
d = json.load(open(sys.argv[1]))
FULL = "--full" in sys.argv
R = d["root"]
def obj(ref):
    return R[ref["$instance"]]["value"] if isinstance(ref, dict) and "$instance" in ref else ref

def g(x):  # short guid
    s = x["$guid"] if isinstance(x, dict) else x
    return s

def var(v):
    # STUConfigVarDynamic: 0x4f6799b5 = [bag guid, var guid]
    ids = v["$fields"].get("0x4f6799b5")
    if isinstance(ids, list) and len(ids) == 0: return "$()"
    if isinstance(ids, list) and len(ids) == 1:
        return f"${g(ids[0])[-4:]}"
    if isinstance(ids, list) and len(ids) > 2:
        return "$" + ":".join(g(x)[-4:] for x in ids)
    if isinstance(ids, list):
        bag, vid = g(ids[0]), g(ids[1])
        b = {"0x0d80000000000004": "", "0x0000000000000000": ""}.get(bag, bag[-4:] + ":")
        return f"${b}{vid[-4:]}"
    return "$?"

BOILER = {"m_displayName", "m_comment", "m_pos", "m_uniqueID", "m_debugText", "0xf8f89f89", "0xa56dc772",
          "0x53bfb779", "0x4b062861", "0xefd56e99", "m_isState", "m_stateGroup", "0x065d841c",
          "0x652a186c", "0x3882159e", "0xa2eca5de", "0x63d0b555", "0x36391723", "m_inPlug", "m_beginPlug",
          "m_abortPlug", "0x83e83924"}

def expr(e):
    ed = e["$fields"]
    ops = ed.get("m_opcodes", [])
    dyn = [obj(x) for x in ed.get("m_dynamicVars", [])]
    cvs = [obj(x) for x in e.get("_cvs", [])]
    consts = ed.get("0x1b183605", [])
    ARG = {1, 2, 3, 4, 5, 30, 31, 32, 33, 34, 36, 37, 38, 39, 40, 70}
    BIN = {100: "+", 101: "-", 102: "*", 103: "/", 104: "%", 105: "**", 144: "==", 145: "!=",
           140: ">=", 141: ">", 142: "<=", 143: "<", 200: "min", 201: "max", 206: "rand", 207: "randi",
           167: "dist", 168: "dot", 169: "cross"}
    UN = {106: "-", 120: "!", 160: "invsqrt", 161: "sin", 162: "cos", 163: "sind", 164: "cosd", 165: "len",
          166: "len", 170: "norm", 173: "x", 174: "y", 175: "z", 203: "floor", 204: "ceil", 205: "round"}
    def dv(i): return var(dyn[i]) if i < len(dyn) and dyn[i] else f"dyn[{i}]"
    def cv(i): return cvr(cvs[i]) if i < len(cvs) and cvs[i] else f"cv[{i}]"
    def run(pc, end, st):
        ent = None
        while pc < end and pc < len(ops):
            op = ops[pc]; a = ops[pc + 1] if op in ARG and pc + 1 < len(ops) else None
            n = 2 if op in ARG else 1
            if op == 0: return st, pc
            if op == 30: st.append(cv(a))
            elif op in (31, 36): st.append(dv(a) if not ent else f"{ent}.{dv(a)}"); ent = None
            elif op in (32, 33, 34):
                k = st.pop() if st else "?"; st.append(f"{dv(a)}[{k}]" + {32: "", 33: ".key", 34: ".idx"}[op])
            elif op == 35: ent = st.pop() if st else "?"
            elif op in (37, 38, 39):
                k = st.pop() if st else "?"; st.append(f"{cv(a)}[{k}]" + {37: "", 38: ".key", 39: ".idx"}[op])
            elif op == 40: st.append(f"count({cv(a)})")
            elif op == 70: st.append(str(consts[a]) if a < len(consts) else f"f[{a}]")
            elif op in BIN:
                r = st.pop() if st else "?"; l = st.pop() if st else "?"
                # per parser: 140/141 are value1 (top) >= value2 → r >= l
                if op in (140, 141): st.append(f"({r} {BIN[op]} {l})")
                elif op in (200, 201, 206, 207, 167, 168, 169): st.append(f"{BIN[op]}({l}, {r})")
                else: st.append(f"({l} {BIN[op]} {r})")
            elif op in UN:
                x = st.pop() if st else "?"; st.append(f"{UN[op]}({x})" if op != 120 else f"!{x}")
            elif op == 202:
                c_ = st.pop(); b_ = st.pop(); a_ = st.pop(); st.append(f"clamp({a_}, {b_}, {c_})")
            elif op == 171:
                z = st.pop(); y = st.pop(); x = st.pop(); st.append(f"vec({x},{y},{z})")
            elif op in (2, 3):
                l = st.pop() if st else "?"; tgt = pc + a
                sub, _ = run(pc + n, tgt, [])
                st.append(f"({l} {'&&' if op == 2 else '||'} {sub[-1] if sub else '?'})"); pc = tgt; continue
            elif op in (4, 5):
                c = st.pop() if st else "?"; tgt = pc + a
                # then-branch runs to a jump (op 1) right before tgt
                thn, jpc = run(pc + n, tgt, [])
                els_end = len(ops)
                if jpc is not None and jpc < len(ops) and ops[jpc] == 1: els_end = jpc + ops[jpc + 1]
                els, _ = run(tgt, els_end, [])
                cc = c if op == 4 else f"!{c}"
                st.append(f"({cc} ? {thn[-1] if thn else '?'} : {els[-1] if els else '?'})"); pc = els_end; continue
            elif op == 1: return st, pc
            else: st.append(f"<op{op}>")
            pc += n
        return st, pc
    st, _ = run(0, len(ops), [])
    return " ; ".join(st) if st else "<empty>"

def cvr(c, depth=0):
    if c is None: return "null"
    if not isinstance(c, dict) or "$type" not in c: return json.dumps(c)
    t = tname(c["$type"]); f = c["$fields"]
    if t == "STUConfigVarDynamic": return var(c)
    if c["$type"] == "0x1e89514b":  # view-model binding {m_link: UI var, m_value}
        return f"{uiv(f.get('m_link'))}={val(f.get('m_value'), depth + 1)}"
    if t == "STUConfigVarFilter":
        return "Filter(" + val(f.get("m_condition"), depth + 1) + ")"
    if t == "STUConfigVarUXScreen": return "screen:" + g(f["m_screen"])[2:]
    if "m_expression" in f:
        e = dict(f["m_expression"]); e["_cvs"] = f.get("m_configVars", [])
        return "{" + expr(e) + "}"
    vals = []
    for k, v in f.items():
        if k in BOILER: continue
        vals.append(f"{fname(k)}={val(v, depth + 1)}" if len(f) > 1 else val(v, depth + 1))
    return f"{short(t)}({', '.join(vals)})"

UIV = {}
try:
    for line in open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "ui_variables.tsv")):
        k, n = line.rstrip("\n").split("\t")[:2]; UIV[int(k, 16)] = n
except FileNotFoundError: pass
def uiv(x):
    if x is None: return "?"
    gg = int(g(x), 16)
    return UIV.get(gg, "ui" + hex(gg)[-4:])

def val(v, depth=0):
    if isinstance(v, dict):
        if "$instance" in v:
            o = obj(v); t = tname(o.get("$type", "")) if isinstance(o, dict) else ""
            if isinstance(o, dict) and "m_parentNode" in o.get("$fields", {}): return "<plug>"
            if v["$instance"] in NIDX: return f"#{NIDX[v['$instance']]}"
            if t in ("STUStatescriptOutputPlug", "STUStatescriptInputPlug", "STUGraphLink") or depth > 8:
                return f"<{short(t)}>"
            return cvr(o, depth)
        if "$guid" in v: return v["$guid"][2:]
        if "$type" in v: return cvr(v, depth)
        return json.dumps(v)
    if isinstance(v, list):
        return "[" + ", ".join(val(x, depth) for x in v) + "]"
    return json.dumps(v) if isinstance(v, str) else str(v)

graph = R[0]["value"]["$fields"]
nodes = [n["$instance"] for n in graph["m_nodes"]]
nidx = {inst: i for i, inst in enumerate(nodes)}
NIDX.update(nidx)
states = {x["$instance"] for x in graph.get("m_states", [])}
entries = {x["$instance"] for x in graph.get("m_entries", [])}
remote = [nidx.get(x["$instance"]) for x in graph.get("m_remoteSyncNodes", [])]

def plug_targets(p):
    po = obj(p)
    out = []
    for l in po["$fields"].get("m_links", []):
        lo = obj(l)["$fields"]
        ip = obj(lo["m_inputPlug"])["$fields"]
        tgt = ip["m_parentNode"]["$instance"]
        out.append(f"#{nidx.get(tgt, 'i' + str(tgt))}")
    return out

def isplug(v):
    if isinstance(v, dict) and "$instance" in v:
        o = obj(v); return isinstance(o, dict) and "m_parentNode" in o.get("$fields", {})
    return False

print(f"# graph {os.path.basename(sys.argv[1])}: {len(nodes)} nodes, remote sync nodes {remote}")
sv = graph.get("m_syncVars", [])
if sv:
    print("syncVars:", ", ".join(g(s["$fields"]["0x4f6799b5"])[-4:] for s in sv))
for i, inst in enumerate(nodes):
    o = R[inst]["value"]; t = tname(o["$type"]); f = o["$fields"]
    flags = []
    if f.get("m_clientOnly"): flags.append("clientOnly")
    if f.get("m_serverOnly"): flags.append("serverOnly")
    kind = "E" if inst in entries else ("S" if inst in states else "")
    head = f"#{i:<3}{kind:1} {short(t)}" + (f" [{' '.join(flags)}]" if flags else "")
    if f.get("m_displayName"): head += f" \"{f['m_displayName']}\""
    if f.get("m_comment"): head += f" //{f['m_comment']}"
    parts, plugs = [], []
    for k, v in f.items():
        if k in BOILER or k in ("m_clientOnly", "m_serverOnly"): continue
        if isplug(v):
            tg = plug_targets(v)
            if tg: plugs.append(f"{fname(k).replace('m_','').replace('Plug','')}→{','.join(tg)}")
            continue
        if isinstance(v, list) and v and all(isplug(x) for x in v):
            for x in v:
                tg = plug_targets(x); cmd = obj(x)["$fields"].get("m_command")
                lab = fname(k) if not cmd else "cmd " + g(cmd)[-4:]
                if tg or cmd: plugs.append(f"{lab}→{','.join(tg) or '-'}")
            continue
        if v in (None, [], "", 0) and not FULL: continue
        parts.append(f"{fname(k)}={val(v)}")
    print(head + ("  " + "; ".join(parts) if parts else ""))
    if plugs: print("       " + "  ".join(plugs))
