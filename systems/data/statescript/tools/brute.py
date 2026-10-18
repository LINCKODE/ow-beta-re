import zlib,re,json,itertools
N='systems/stu/names/'
reg=json.load(open('systems/data/client_stu_registry.json'))
known={}
for f in [N+'KnownTypes.csv','TankLibHelper_Data_KnownTypes.csv']:
    for l in open(f):
        p=[x.strip() for x in l.split(',')]
        if len(p)>=2: known[p[1]]=1
targets=set(int(h,16) for h in reg)
named={}
for n in known:
    h=zlib.crc32(n.lower().encode())
    if h in targets: named[h]=n
print('known names hitting registry',len(named))
toks=set()
def split(n):
    return re.findall(r'[A-Z]+(?=[A-Z][a-z])|[A-Z]?[a-z]+|[A-Z]+|\d+',n)
srcs=list(known)
for f in [N+'KnownFields.csv',N+'KnownEnums.csv','TankLibHelper_Data_KnownFields.csv']:
    for l in open(f):
        p=[x.strip() for x in l.split(',')]
        if len(p)>=2: srcs.append(p[1])
extra=open('docs/Statescript.md').read()
srcs+=re.findall(r'[A-Za-z][A-Za-z0-9]+',extra)
for s in srcs:
    for t in split(s.replace('m_','')):
        if len(t)>1 or t in 'XYZ': toks.add(t[0].upper()+t[1:])
toks=sorted(toks); print('tokens',len(toks))
pre=['STUStatescriptState','STUStatescriptAction','STUStatescriptEntry','STUStatescriptCondition','STUConfigVar','STUStatescript','MStatescriptState','STUStatescriptStateUX','STUStatescriptActionUX','STUConfigVarUX','STU','STUStatescriptStateEffect','STUStatescriptActionEffect','STUConfigVarEntity','STUConfigVarGet']
tl=[t.lower().encode() for t in toks]
for p in pre:
    pc=zlib.crc32(p.lower().encode())
    for i,a in enumerate(tl):
        ca=zlib.crc32(a,pc)
        if ca in targets and ca not in named: named[ca]=p+toks[i]
        for j,b in enumerate(tl):
            cb=zlib.crc32(b,ca)
            if cb in targets and cb not in named: named[cb]=p+toks[i]+toks[j]
    print(p,len(named),flush=True)
json.dump({'%08x'%k:v for k,v in named.items()},open('names_brute.json','w'),indent=0)
