import json,sys,re
S={int(l.split('\t')[0],16):l.rstrip('\n').split('\t',1)[1] for l in open('strings.tsv')}
U={int(l.split('\t')[0],16):l.rstrip('\n').split('\t',1)[1] for l in open('uivars.tsv')}
raw=open(sys.argv[2],'rb').read() if len(sys.argv)>2 else None
R=json.load(open(sys.argv[1]))['root']
seen=set()
def strat(s):
    if isinstance(s,dict) and '$error' in s and raw:
        o=int(re.search(r"0x[0-9a-f]+",s['$error']).group(0),16); n=int.from_bytes(raw[o:o+4],'little'); return repr(raw[o+16:o+16+n].decode('utf-8','replace'))
    return None
def g(x):
    v=int(x,16); p=v>>52
    if p==0x0de: return f'"{S.get(v,hex(v))}"'
    if p==0x0ac: return f'ui:{U.get(v,hex(v))}'
    return x
def fmt(x,d):
    if isinstance(x,dict):
        if '$instance' in x: return node(x['$instance'],d)
        if '$guid' in x: return g(x['$guid'])
        s=strat(x)
        if s: return s
        if '$type' in x:
            return '{'+x['$type']+' '+', '.join(f"{k}={fmt(v,d)}" for k,v in x.get('$fields',{}).items())+'}'
        return json.dumps(x)[:80]
    if isinstance(x,list):
        return '['+', '.join(fmt(v,d) for v in x)+']'
    return json.dumps(x)
def node(i,d):
    if i in seen: return f'<#{i}>'
    seen.add(i)
    r=R[i]; v=r['value']; pad='\n'+'  '*(d+1)
    out=f"#{i} {r['type']}"
    if isinstance(v,dict):
        for k,x in v.get('$fields',{}).items():
            out+=pad+f".{k} = "+fmt(x,d+1)
    else: out+=' '+json.dumps(v)[:80]
    return out
print(node(0,0))
