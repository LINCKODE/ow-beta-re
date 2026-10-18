import json,collections
reg=json.load(open('systems/data/client_stu_registry.json'))
C=json.load(open('cat2.json')); U=json.load(open('usage.json'))
N='systems/stu/names/'
tn={}
for l in open(N+'KnownTypes.csv'):
    p=[x.strip() for x in l.split(',')]
    if len(p)>=2: tn[p[0].lower()]=(p[1],'owlib')
for h,v in json.load(open('names_brute.json')).items(): tn.setdefault(h,(v,'crc'))
def ch(h):
    c=[]
    while h: c.append(h); h=reg[h]['parent']
    return c
KIND={'2f937ae3':'Entry','2ecefc8b':'Action','6f10080f':'Condition','a7213568':'State','15b44727':'Terminator','bfd9aadd':'ConfigVar','a97578e7':'Base'}
win={}
for c in C:
    h=c['hash']
    if h not in win or c['prio']>=win[h]['prio']: win[h]=c
rows=[]
for h in reg:
    c=ch(h)
    if 'a97578e7' not in c and 'bfd9aadd' not in c: continue
    k=next(KIND[x] for x in c if x in KIND)
    # resolved impl: nearest ancestor (incl self) with record
    impl=next((x for x in c if x in win),None)
    r=win.get(h)
    rows.append(dict(hash=h,name=tn.get(h,(h,None))[0],src=tn.get(h,(0,None))[1],kind=k,parent=reg[h]['parent'],
        stu_size=reg[h]['size'],nfields=len(reg[h]['fields']),
        rec=bool(r),prio=r['prio'] if r else None,nrec=sum(1 for x in C if x['hash']==h),
        vtable=r['vtable'] if r else None,rt_size=r['rt_size'] if r else None,factory=r['factory'] if r else None,
        impl=impl,inst=U['instances'].get(h,0),graphs=U['graphs'].get(h,0)))
json.dump(rows,open('cat3.json','w'),indent=0)
cnt=collections.Counter((r['kind'],r['rec'],r['inst']>0) for r in rows)
for k,v in sorted(cnt.items()): print(k,v)
print('total',len(rows))
# used types lacking own record: which impl
for r in rows:
    if r['inst'] and not r['rec']: print('norec used',r['kind'],r['name'],r['inst'],'impl->',tn.get(r['impl'],(r['impl'],))[0] if r['impl'] else None)
