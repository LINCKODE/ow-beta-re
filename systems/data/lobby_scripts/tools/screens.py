import sys, re, json; sys.path.insert(0,'.')
from cstore import *
def br12(x): return int(f"{x:012b}"[::-1], 2)
def typ(prefix3): return br12((prefix3 << 4) & 0xfff) + 1
S = {int(l.split('\t')[0],16): l.rstrip('\n').split('\t',1)[1] for l in open('strings.tsv')}
U = {int(l.split('\t')[0],16): l.rstrip('\n').split('\t',1)[1] for l in open('uivars.tsv')}
def refs_of(b, g):
    refs = []
    for i in range(0, len(b) - 7):
        v = int.from_bytes(b[i:i+8], 'little')
        if (v >> 52) in (0x0de, 0x0ac, 0x09a, 0x058, 0x0ba, 0x024, 0x02e, 0x0a4, 0x0a5) and (v & 0x000fffff00000000) == 0 and v != g and v not in refs:
            refs.append(v)
    return refs
if __name__ == "__main__":
    t = typ(0x09a); gs = of_type(t); print(hex(t), len(gs))
    idx = {}
    for g in gs:
        b = read(g); idx[g] = refs_of(b, g)
    json.dump({hex(k): [hex(x) for x in v] for k, v in idx.items()}, open('screens_refs.json', 'w'))
