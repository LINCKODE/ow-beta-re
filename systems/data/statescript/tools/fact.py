from bin import *; import re,json
def body(f,maxn=0x400):
    out=[]
    for i in dis(f,maxn):
        out.append(i)
        if i.mnemonic in('ret','int3'): break
    return out
def parse_factory(f):
    ins=[]
    # linear until int3 padding, including branches
    for i in dis(f,0x200):
        if i.mnemonic=='int3': break
        ins.append(i)
    size=None; ctor=None
    for i in ins:
        m=re.match(r'edx, (0x[0-9a-f]+|\d+)$',i.op_str)
        if i.mnemonic=='mov' and m and size is None: size=int(m[1],0)
        if i.mnemonic in('jmp','call') and i.op_str.startswith('0x'):
            t=int(i.op_str,16)-IB
            if not (0xd00000>t or False) or True:
                if i.mnemonic=='jmp' or ctor is None: ctor=t if i.mnemonic=='jmp' else ctor
    return size,ctor,ins
def vtable_of(ctor,depth=0,stop_ret=True):
    if ctor is None or depth>3: return None
    regs={}; last=None
    ins=[]
    for i in dis(ctor,0x800):
        if i.mnemonic=='int3': break
        ins.append(i)
        if i.mnemonic=='ret' and stop_ret: break
    for i in ins:
        m=re.match(r'(\w+), \[rip \+ (0x[0-9a-f]+)\]',i.op_str)
        if i.mnemonic=='lea' and m: regs[m[1]]=i.address-IB+i.size+int(m[2],16); continue
        m=re.match(r'qword ptr \[(\w+)\], (\w+)$',i.op_str)
        if i.mnemonic=='mov' and m and m[2] in regs: last=regs[m[2]]
    if last is None and ins and ins[-1].mnemonic=='jmp' and ins[-1].op_str.startswith('0x'):
        return vtable_of(int(ins[-1].op_str,16)-IB,depth+1)
    return last
if __name__=='__main__':
    for f in [0xd00e50,0xd01040,0x6174d0]:
        s,c,_=parse_factory(f); print(hex(f),s,hex(c) if c else c, hex(vtable_of(c)) if c and vtable_of(c) else None)
