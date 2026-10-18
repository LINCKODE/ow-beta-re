from bin import *
import re,json
HEAD=0x18f8e88
xs=[int(m,16)-IB for m in re.findall(r'From (7ff[0-9a-f]+) \[WRITE\]',open('rtti_xrefs.txt').read())]
recs=[]
for w in xs:
    # find function start: scan back for int3 run
    s=w
    while not (rd(s-1,1)==b'\xcc'): s-=1
    ins=dis(s,0x200)
    regs={}; stores={}
    for i in ins:
        if i.mnemonic=='ret': break
        m=re.match(r'lea (\w+), \[rip \+ (0x[0-9a-f]+)\]',i.mnemonic+' '+i.op_str)
        if m: regs[m[1]]=i.address-IB+i.size+int(m[2],16); continue
        m=re.match(r'mov (\w+), qword ptr \[rip \+ (0x[0-9a-f]+)\]',i.mnemonic+' '+i.op_str)
        if m: regs[m[1]]=('load',i.address-IB+i.size+int(m[2],16)); continue
        m=re.match(r'mov (?:qword|dword) ptr \[rip \+ (0x[0-9a-f]+)\], (\w+)',i.mnemonic+' '+i.op_str)
        if m:
            a=i.address-IB+i.size+int(m[1],16); v=m[2]
            stores[a]=regs.get(v, int(v,16) if v.startswith('0x') or v.isdigit() else v)
    # node: store to HEAD = node addr
    node=stores.get(HEAD)
    rec=stores.get(node+8) if isinstance(node,int) else None
    if rec is None: print('??',hex(s)); continue
    r={'init':s,'node':node,'rec':rec,'factory':stores.get(rec),'inherit':stores.get(rec+8),'prio':stores.get(rec+0x10),'f18':stores.get(rec+0x18),'f20':stores.get(rec+0x20),'f28':stores.get(rec+0x28)}
    recs.append(r)
print(len(recs))
for r in recs[:5]: print({k:(hex(v) if isinstance(v,int) else v) for k,v in r.items()})
json.dump(recs,open('rtti_recs.json','w'))
