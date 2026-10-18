#!/usr/bin/env python3
"""Statescript expression bytecode: decoder (to text) and reference evaluator (T038).

Format and semantics: docs/StatescriptConfigVars.md §3. Works on stu_dump JSON of a graph
(`content_dump --read 0x058… g.stud; stu_dump g.stud > g.json`).

usage:
  expr.py g.json                 # every STUConfigVarExpression(Typed) of the graph, decoded
  expr.py g.json --ops           # also print the raw opcodes
Library:
  decode(ops, consts, dyn, cvs) -> str   (dyn/cvs: lists of operand display strings)
"""
import json, sys

# opcode -> (mnemonic, has 1-byte argument)
OPS = {
    0: ("end", 0), 1: ("jmp", 1), 2: ("and", 1), 3: ("or", 1), 4: ("jf", 1), 5: ("jt", 1),
    30: ("cv", 1), 31: ("var", 1), 32: ("var[]", 1), 33: ("var[].key", 1), 34: ("var[].idx", 1),
    35: ("on", 0), 36: ("provider", 1), 37: ("cv[]", 1), 38: ("cv[].key", 1), 39: ("cv[].idx", 1),
    40: ("count", 1), 70: ("const", 1),
    100: ("+", 0), 101: ("-", 0), 102: ("*", 0), 103: ("/", 0), 104: ("%", 0), 105: ("pow", 0),
    106: ("neg", 0), 120: ("!", 0),
    140: ("<=", 0), 141: ("<", 0), 142: (">=", 0), 143: (">", 0), 144: ("==", 0), 145: ("!=", 0),
    160: ("sqrt", 0), 161: ("sin", 0), 162: ("cos", 0), 163: ("sind", 0), 164: ("cosd", 0),
    165: ("abs", 0), 166: ("len", 0), 167: ("dist", 0), 168: ("dot", 0), 169: ("cross", 0),
    170: ("norm", 0), 171: ("vec3", 0), 172: ("half4", 0), 173: ("x", 0), 174: ("y", 0), 175: ("z", 0),
    200: ("min", 0), 201: ("max", 0), 202: ("clamp", 0), 203: ("floor", 0), 204: ("ceil", 0),
    205: ("round", 0), 206: ("random", 0), 207: ("randint", 0),
}
INFIX = {100: "+", 101: "-", 102: "*", 103: "/", 104: "%", 140: "<=", 141: "<", 142: ">=", 143: ">",
         144: "==", 145: "!="}
UNARY = {106: "-", 120: "!"}
FUNC1 = {160, 161, 162, 163, 164, 165, 166, 170, 173, 174, 175, 203, 204, 205}
FUNC2 = {105, 167, 168, 169, 200, 201, 206, 207}
FUNC3 = {171: 3, 172: 4, 202: 3}


def size(op):
    return 2 if OPS.get(op, ("", 0))[1] else 1


def fmt_const(f):
    if f == int(f) and abs(f) < 1e7:
        return str(int(f))
    return repr(round(f, 6))


def decode(ops, consts, dyn, cvs):
    """Symbolic execution of the stack machine; returns the expression text.
    Stack operands are strings; branches of and/or/jf/jt are decoded recursively."""
    def run(pc, stop, st, on):
        while pc < len(ops) and pc != stop:
            op = ops[pc]
            a = ops[pc + 1] if size(op) == 2 and pc + 1 < len(ops) else None
            if op == 0:
                return st, pc, "end"
            if op == 1:
                return st, pc, "jmp"
            if op in (2, 3):                       # short-circuit and / or: keep lhs if it decides
                lhs = st.pop()
                tgt = pc + a
                sub, _, _ = run(pc + 2, tgt, [], None)
                st.append(f"({lhs} {'&&' if op == 2 else '||'} {sub[-1] if sub else '?'})")
                pc = tgt
                continue
            if op in (4, 5):                       # pop condition; jf: falsy -> jump (else), jt: truthy -> jump
                c = st.pop()
                tgt = pc + a
                thn, jpc, how = run(pc + 2, tgt, [], None)
                end = None
                if how == "jmp":
                    end = jpc + ops[jpc + 1]
                els, _, how2 = run(tgt, end if end is not None else -1, [], None)
                t = thn[-1] if thn else "?"
                e = els[-1] if els else "?"
                st.append(f"({c} ? {t} : {e})" if op == 4 else f"({c} ? {e} : {t})")
                if end is None:
                    return st, len(ops), "end"
                pc = end
                continue
            if op == 30:
                st.append(cvs[a] if a < len(cvs) else f"cv{a}")
            elif op == 31:
                v = dyn[a] if a < len(dyn) else f"var{a}"
                st.append(f"{on}.{v}" if on else v); on = None
            elif op in (32, 33, 34):
                k = st.pop(); v = dyn[a] if a < len(dyn) else f"var{a}"
                v = f"{on}.{v}" if on else v; on = None
                st.append(f"{v}[{k}]" + {32: "", 33: ".key", 34: ".index"}[op])
            elif op == 35:
                on = st.pop()
            elif op == 36:
                st.append("provider(" + (dyn[a] if a < len(dyn) else f"var{a}") + ")")
            elif op in (37, 38, 39):
                k = st.pop(); v = cvs[a] if a < len(cvs) else f"cv{a}"
                st.append(f"{v}[{k}]" + {37: "", 38: ".key", 39: ".index"}[op])
            elif op == 40:
                st.append(f"count({cvs[a] if a < len(cvs) else f'cv{a}'})")
            elif op == 70:
                st.append(fmt_const(consts[a]) if a < len(consts) else f"k{a}")
            elif op in INFIX:
                r = st.pop(); l = st.pop()
                st.append(f"({l} {INFIX[op]} {r})")
            elif op in UNARY:
                x = st.pop(); st.append(f"{UNARY[op]}{x}")
            elif op in FUNC1:
                x = st.pop(); st.append(f"{OPS[op][0]}({x})")
            elif op in FUNC2:
                r = st.pop(); l = st.pop(); st.append(f"{OPS[op][0]}({l}, {r})")
            elif op in FUNC3:
                n = FUNC3[op]; args = st[-n:]; del st[-n:]
                st.append(f"{OPS[op][0]}({', '.join(args)})")
            else:
                st.append(f"<op{op}>")
            pc += size(op)
        return st, pc, "stop"
    if not ops:
        return "<empty: FLT 0>"
    try:
        st, _, _ = run(0, -1, [], None)
    except IndexError:
        return "<stack underflow>"
    return st[-1] if st else "<empty>"


# ---- stu_dump JSON helpers -------------------------------------------------------------------
SPECIAL = {0x0D80000000000003: "inst", 0x0D80000000000004: "ent", 0x0D80000000000005: "child",
           0x0D80000000000006: "ref", 0x0D80000000000008: "x", 0x0D80000000000009: "y",
           0x0D8000000000000A: "z", 0x0D8000000000000B: "size"}


def var_path(ids):
    """STUConfigVarDynamic path -> `$xxx` (instance/entity bag prefix dropped when it is the default)."""
    parts = []
    for g in ids:
        v = int(g["$guid"] if isinstance(g, dict) else g, 16)
        parts.append(SPECIAL.get(v, "%x" % (v & 0xFFFFFFFFFFF)))
    if parts and parts[0] == "ent" and len(parts) > 1:
        parts = ["ent"] + parts[1:]
    return "$" + ".".join(parts)


def main():
    d = json.load(open(sys.argv[1])); R = d["root"]
    names = {}
    try:
        cat = json.load(open(__file__.rsplit("/", 2)[0] + "/statescript_node_catalogue.json"))
        names = {"0x" + c["hash"]: c["name"] for c in cat if c.get("name")}
    except Exception:
        pass

    def obj(r):
        return R[r["$instance"]]["value"] if isinstance(r, dict) and "$instance" in r else r

    def cvtext(c, depth=0):
        c = obj(c)
        if c is None:
            return "null"
        t = c.get("$type"); f = c.get("$fields", {})
        if t == "STUConfigVarDynamic":
            return var_path(f.get("0x4f6799b5", []))
        if "m_expression" in f and depth < 4:
            return "{" + expr_text(c, depth + 1) + "}"
        n = names.get(t, t).replace("STUConfigVar", "")
        vals = [v for k, v in f.items() if k != "0x83e83924"]
        if len(vals) == 1 and not isinstance(vals[0], (dict, list)):
            return f"{n}({vals[0]})"
        if len(vals) == 1 and isinstance(vals[0], dict) and "$guid" in vals[0]:
            return f"{n}({vals[0]['$guid']})"
        return n + ("(…)" if vals else "()")

    def expr_text(c, depth=0):
        f = c["$fields"]; ed = f["m_expression"]["$fields"]
        dyn = [var_path(obj(x)["$fields"].get("0x4f6799b5", [])) if obj(x) else "null"
               for x in ed.get("m_dynamicVars", [])]
        cvs = [cvtext(x, depth) for x in f.get("m_configVars", [])]
        return decode(ed.get("m_opcodes", []), ed.get("0x1b183605", []), dyn, cvs)

    for x in R:
        v = x["value"]
        if v.get("$type") in ("0xec502a81", "STUConfigVarExpression"):
            kind = "typed" if v["$type"] == "0xec502a81" else "float"
            line = f"[{x['index']}] {kind}: {expr_text(v)}"
            if "--ops" in sys.argv:
                line += f"   ops={v['$fields']['m_expression']['$fields'].get('m_opcodes')}"
            print(line)


if __name__ == "__main__":
    main()
