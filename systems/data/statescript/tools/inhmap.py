from bin import *; import json,struct
reg=json.load(open('systems/data/client_stu_registry.json'))
s=[x for x in pe.sections if x.Name.startswith(b'.data')][0]
base=s.VirtualAddress; raw=data[s.PointerToRawData:s.PointerToRawData+s.SizeOfRawData]
key=struct.pack('<Q',IB+0x1454b48)
m={}; i=raw.find(key)
while i!=-1:
    if i%8==0:
        rva=base+i; inh=q(rva+8)-IB; h='%08x'%d(rva+0x50)
        if h in reg: m[inh]=h
    i=raw.find(key,i+1)
print(len(m), len(set(m.values())))
json.dump({str(k):v for k,v in m.items()},open('inhmap.json','w'))
R=json.load(open('rtti_recs.json'))
print('unmapped',[hex(r['inherit']) for r in R if r['inherit'] not in m])
