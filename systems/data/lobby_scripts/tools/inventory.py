# Per-screen inventory of all STUUXScreen assets (0x09A…) for systems/data/lobby_scripts/screens.txt
import json,glob,collections,os,re
def strat(b,s):
    if isinstance(s,str): return s
    if isinstance(s,dict) and '$error' in s:
        o=int(re.search(r'0x[0-9a-f]+',s['$error']).group(0),16); n=int.from_bytes(b[o:o+4],'little'); return b[o+16:o+16+n].decode('utf-8','replace')
    return '?'
S={int(l.split('\t')[0],16):l.rstrip('\n').split('\t',1)[1] for l in open('strings.tsv')}
U={int(l.split('\t')[0],16):l.rstrip('\n').split('\t',1)[1] for l in open('uivars.tsv')}
OP={int(k,16):v for k,v in json.load(open('screen_openers.json')).items()}
def guids(v,acc):
    if isinstance(v,dict):
        if '$guid' in v: acc.append(int(v['$guid'],16)); return
        for x in v.values(): guids(x,acc)
    elif isinstance(v,list):
        for x in v: guids(x,acc)
def keytable(R,raw):
    out=[]
    f=R[0]['value'].get('$fields',{})
    for k,v in zip(f.get('m_keys',[]),f.get('m_values',[])):
        ko=R[k['$instance']]['value'].get('$fields',{}); vo=R[v['$instance']]['value'].get('$fields',{})
        kn=strat(raw,ko.get('m_value'))
        if 'm_linkGUID' in vo: g=int(vo['m_linkGUID']['$guid'],16); out.append(f"{kn}=cmd:{U.get(g,hex(g))}")
        elif 'm_displayText' in vo: g=int(vo['m_displayText']['$guid'],16); out.append(f'{kn}="{S.get(g,hex(g))}"')
    return out
lines=[]
for f in sorted(glob.glob('scr/*.json')):
    gid=int(os.path.basename(f)[:-5],16)
    try: J=json.load(open(f)); R=J['root']
    except Exception as e: lines.append(f"{gid:#018x}: unreadable ({e})"); continue
    size=os.path.getsize(f[:-5]+'.stu')
    types=collections.Counter(r['type'] for r in R)
    named=[f"{t.replace('STUUX','')}×{n}" for t,n in types.most_common() if t.startswith('STUUX') and t not in('STUUXString','STUUXScreen','STUUXBinding','STUUXPropertyPath','STUUXCollection','STUUXBool','STUUXKeyTime','STUUXIDResource','STUUXStoryboard')]
    acc=[]; guids(R,acc)
    seen=[]; [seen.append(g) for g in acc if g not in seen and g]
    strs=[S.get(g,hex(g)) for g in seen if g>>52==0x0de]
    uis=[U.get(g,f"ui{g&0xffff:04x}") for g in seen if g>>52==0x0ac]
    subs=[f"{g&0xffff:03x}" for g in seen if g>>52==0x09a and g!=gid]
    dicts=[f"{g&0xffff:03x}" for g in seen if g>>52==0x0ba]
    other=collections.Counter(f"{g>>52:03x}" for g in seen if g>>52 not in (0x0de,0x0ac,0x09a,0x0ba))
    root=R[R[0]['value']['$fields']['m_rootElement']['$instance']]['type'] if R[0]['value'].get('$fields',{}).get('m_rootElement') else '-'
    ops=[f"{g&0xffff:04x}" for g in (int(x,16) for x in OP.get(gid,[]))]
    lines.append(f"{gid:#018x} ({size} B, {len(R)} instances, root {root.replace('STUUX','')})")
    lines.append(f"  opened by graphs: {', '.join(ops) or '-'}")
    lines.append(f"  elements: {', '.join(named)}")
    kt=keytable(R,open(f[:-5]+'.stu','rb').read())
    if kt: lines.append(f"  key table: {'; '.join(kt)}")
    if strs: lines.append(f"  strings: {' | '.join(strs)}")
    if uis: lines.append(f"  ui variables: {', '.join(uis)}")
    if subs or dicts: lines.append(f"  sub-screens: {', '.join(subs) or '-'}; dictionaries (0x0BA): {', '.join(dicts) or '-'}")
    if other: lines.append(f"  other refs: {dict(other)}")
open('screens.txt','w').write('\n'.join(lines)+'\n')
print(len(lines))
