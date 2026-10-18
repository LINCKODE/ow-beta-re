import sys,json,struct; sys.path.insert(0,'.')
from cstore import *
from screens import typ
out={}
gs=of_type(typ(0x058)); print(len(gs))
for g in gs:
    b=read(g)
    if not b: continue
    refs=set()
    for i in range(0,len(b)-7):
        v=int.from_bytes(b[i:i+8],'little')
        if v>>52==0x09a and (v&0x000fffff00000000)==0: refs.add(v)
    for r in refs: out.setdefault(hex(r),[]).append(hex(g))
json.dump(out,open('screen_openers.json','w'),indent=0)
print(len(out))
