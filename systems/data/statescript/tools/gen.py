import json,collections,csv,os
HERE=os.path.dirname(os.path.abspath(__file__))
# T019: names file (hash -> name, confidence, provenance); applied rows win over the lookups below
NAMES={r['hash']:r for r in csv.DictReader(open(os.path.join(HERE,'..','statescript_names.csv'))) if r['applied']=='yes'}
reg=json.load(open('systems/data/client_stu_registry.json'))
N='systems/stu/names/'
REJECT={'STUStatescriptStateUXWishedInt','STUStatescriptStatePaulStar','STUStatescriptStateEffectWidthsInactive','STUStatescriptActionProjectECS','STUStatescriptActionMirroredSpelling','STUStatescriptElementsSeason','STUConfigVarEntityPadCompression','STUStatescriptActionUXMatchmaking68','STUStatescriptActionWraithEnlighten'}
FIX={'ENTITY':'Entity','FINISHED':'Finished','ENDMatch':'EndMatch'}
tn={};fn={}
for l in open(N+'KnownTypes.csv'):
    p=[x.strip() for x in l.split(',')]
    if len(p)>=2: tn[p[0].lower()]=p[1]
for l in open(N+'KnownFields.csv'):
    p=[x.strip() for x in l.split(',')]
    if len(p)>=2: fn[p[0].lower()]=p[1]
brute=json.load(open('names_brute.json')) if os.path.exists('names_brute.json') else {}
extra={'4e5feee5':'STUStatescriptSubgraphServerOnly','e51f3e8d':'STUStatescriptInputPlugAbort'}
def name(h):
    if h in NAMES: return NAMES[h]['name'],('owlib' if NAMES[h]['method']=='owlib-table' else 'crc')
    if h in tn: return tn[h],'owlib'
    if h in extra: return extra[h],'crc'
    if h in brute and brute[h] not in REJECT:
        n=brute[h]
        for a,b in FIX.items(): n=n.replace(a,b)
        return n,'crc'
    return None,None
K={0:'',1:'[]',2:'*',3:'*[]',4:'inline',5:'inline[]',7:'map',8:'enum',9:'enum[]',10:'fileref',11:'fileref[]',12:'ref',13:'ref[]'}
PRIM={'3b9327d2':'u8','954a3bab':'i32','91c74719':'u32','8fa75a30':'f32','bbca304b':'str','f6ec8926':'vec2','05d31669':'u64'}
def fdesc(f):
    n=fn.get(f['hash'],f['hash'])
    e=f['elem']; en=tn.get(e) or PRIM.get(e) or (e or '')
    en=en.replace('STUStatescript','SS').replace('STUConfigVar','CV')
    k=f['ctype']
    t={0:en,1:en+'[]',2:en+'*',3:en+'*[]',4:en,5:en+'[]',8:'enum',9:'enum[]',12:'ref',13:'ref[]',10:'fileref',11:'fileref[]',7:'map'}.get(k,str(k))
    return '+%x %s:%s'%(f['offset'],n,t)
def write_md(rows,path='StatescriptNodeCatalogue.md'):
    order=['Entry','Condition','Action','Terminator','State','ConfigVar','Base']
    title={'Entry':'Entries','Condition':'Conditions','Action':'Actions','Terminator':'Action terminators (no output plug)','State':'States','ConfigVar':'Config vars','Base':'Other STUStatescriptBase subclasses'}
    L=[]
    L.append('# Statescript node catalogue (client build 24919)\n')
    L.append('Generated from the client binary, `systems/data/client_stu_registry.json` and the 296 graphs resident in the Temple of Anubis snapshot. How it was produced and what the columns mean: [StatescriptRuntime.md](StatescriptRuntime.md) §2. Machine-readable form: `systems/data/statescript/statescript_node_catalogue.json`.\n')
    L.append('Columns: **hash** = STU type hash (CRC32 of the lower-cased name); **name** = OWLib KnownTypes, *italic* = recovered by CRC32 match of a guessed name (likely, not certain; confidence and provenance per name in `systems/data/statescript/statescript_names.csv`), blank = unknown; **STU** = size of the STU (asset) object; **own fields** = fields this type adds to its parent (`+offset name:type`, `*` = object pointer, `[]` = list, `ref` = resource reference; CV = STUConfigVar, SS = STUStatescript); **runtime** = client class: vtable RVA, object size, priority layer (highest wins); `→ X` = no own class, the nearest registered ancestor X is used; **uses** = instances in resident graphs / number of graphs.\n')
    cnt=collections.Counter(o['kind'] for o in rows)
    used=collections.Counter(o['kind'] for o in rows if o['instances'])
    L.append('| kind | types | used in resident graphs | with own client class |\n|---|---|---|---|')
    for k in order: L.append('| %s | %d | %d | %d |'%(k,cnt[k],used[k],sum(1 for o in rows if o['kind']==k and o['runtime'])))
    L.append('| **total** | **%d** | **%d** | **%d** |\n'%(len(rows),sum(used.values()),sum(1 for o in rows if o['runtime'])))
    for k in order:
        rs=[o for o in rows if o['kind']==k]
        rs.sort(key=lambda o:(-o['instances'],o['name'] or 'zz',o['hash']))
        L.append('## %s (%d)\n'%(title[k],len(rs)))
        L.append('| hash | name | parent | STU | own fields | runtime | uses |\n|---|---|---|---|---|---|---|')
        for o in rs:
            nm=o['name'] or ''
            if o['name_src']=='crc': nm='*%s*'%nm
            pn=o['parent_name'] or o['parent'] or ''
            rt='; '.join('`%s` 0x%x p%d'%(x['vtable'],x['size'],x['prio']) for x in o['runtime']) if o['runtime'] else ('→ %s'%(o['impl_from_name'] or o['impl_from']) if o['impl_from'] else '–')
            fl=', '.join(o['fields']) if k!='ConfigVar' or len(o['fields'])<=6 else ', '.join(o['fields'][:6])+', …'
            L.append('| `%s` | %s | %s | 0x%x | %s | %s | %s |'%(o['hash'],nm,pn.replace('STUStatescript','SS').replace('STUConfigVar','CV'),o['stu_size'],fl.replace('|','\\|'),rt,'%d / %d'%(o['instances'],o['graphs']) if o['instances'] else '–'))
        L.append('')
    open(path,'w').write('\n'.join(L))
    return cnt,used

if __name__=='__main__':
    R=json.load(open('cat3.json')); C=json.load(open('cat2.json'))
    wins={}
    recs=collections.defaultdict(list)
    for c in C: recs[c['hash']].append(c)
    rows=[]
    for r in R:
        n,src=name(r['hash'])
        o=dict(hash=r['hash'],name=n,name_src=src,kind=r['kind'],parent=r['parent'],parent_name=name(r['parent'])[0] if r['parent'] else None,
           stu_size=r['stu_size'],fields=[fdesc(f) for f in reg[r['hash']]['fields']],
           runtime=[dict(prio=c['prio'],factory='0x%x'%c['factory'],vtable='0x%x'%c['vtable'],size=c['rt_size'],init='0x%x'%c['init']) for c in sorted(recs[r['hash']],key=lambda c:-c['prio'])],
           impl_from=r['impl'],impl_from_name=name(r['impl'])[0] if r['impl'] else None,instances=r['inst'],graphs=r['graphs'])
        rows.append(o)
    json.dump(rows,open('statescript_node_catalogue.json','w'),indent=1)
    print(len(rows), *write_md(rows))
