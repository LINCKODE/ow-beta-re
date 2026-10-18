from bin import *; from fact import *; import json,collections
reg=json.load(open('systems/data/client_stu_registry.json'))
inh={int(k):v for k,v in json.load(open('inhmap.json')).items()}
names={}
for l in open('systems/stu/names/KnownTypes.csv'):
    p=[x.strip() for x in l.split(',')]
    if len(p)>=2: names[p[0].lower()]=(p[1],'owlib')
for h,v in json.load(open('names_brute.json')).items():
    if h not in names: names[h]=(v,'crc')
def nm(h): return names.get(h,(h,None))[0]
def chain(h):
    c=[]
    while h: c.append(h); h=reg.get(h,{}).get('parent')
    return c
kinds={'2f937ae3':'Entry','2ecefc8b':'Action','6f10080f':'Condition','a7213568':'State','15b44727':'ActionTerminator','bfd9aadd':'ConfigVar','a97578e7':'Base','84201dca':'Plug'}
R=json.load(open('rtti_recs.json'))
out=[]
for r in R:
    h=inh[r['inherit']]; c=chain(h)
    k=[kinds[x] for x in c if x in kinds]
    size,ctor,_=parse_factory(r['factory'])
    vt=vtable_of(ctor) if ctor else vtable_of(r['factory'],stop_ret=False)
    out.append(dict(r,hash=h,name=nm(h),namesrc=names.get(h,(0,None))[1],chain=[nm(x) for x in c],kind=k[0] if k else 'other',rt_size=size,ctor=ctor,vtable=vt,stu_size=reg[h]['size']))
json.dump(out,open('cat2.json','w'),indent=0)
print(collections.Counter(o['kind'] for o in out))
print('no vt',sum(1 for o in out if o['vtable'] is None),'no size',sum(1 for o in out if o['rt_size'] is None))
dup=collections.Counter(o['hash'] for o in out); print('dups',[(nm(h),n) for h,n in dup.items() if n>1][:30])
