import json,struct,collections
reg=json.load(open('systems/data/client_stu_registry.json'))
crc2h={v['layout_crc']:h for h,v in reg.items()}
meta=json.load(open('graphs/meta.json'))
class Img:
    def __init__(s,g):
        s.g=g; s.b=open('graphs/%s.mem'%g[2:],'rb').read(); s.base=meta[g]['base']; s.root=s.base+0x10
        magic,ver,tab=struct.unpack_from('<IIq',s.b,0)
        count,pad,ent=struct.unpack_from('<IIq',s.b,tab-s.base)
        s.types={}
        for i in range(count):
            off,flags,crc=struct.unpack_from('<IIQ',s.b,ent-s.base+16*i)
            s.types[s.base+0x10+off if False else None]=None
        # instance offsets are relative to blob start (root-0x10)?
        s.inst=[]
        for i in range(count):
            off,flags,crc=struct.unpack_from('<IIQ',s.b,ent-s.base+16*i)
            s.inst.append((off,crc2h['%016x'%crc]))
    def ok(s,a): return s.base<=a<s.base+len(s.b)
    def Q(s,a): return struct.unpack_from('<Q',s.b,a-s.base)[0]
    def I(s,a): return struct.unpack_from('<I',s.b,a-s.base)[0]
    def i(s,a): return struct.unpack_from('<i',s.b,a-s.base)[0]
    def B(s,a): return s.b[a-s.base]
    def lst(s,a):
        p=s.Q(a)
        if not s.ok(p): return []
        n=s.I(p); d=s.Q(p+8)
        return [s.Q(d+8*k) for k in range(n)] if s.ok(d) else []
    def typeat(s,a):
        return s.bytype.get(a)
    def build(s):
        # map instance address -> type: try both conventions
        s.bytype={}
        for off,h in s.inst: s.bytype[s.base+off]=h
        return s
def lst32(m,a):
    p=m.Q(a)
    if not m.ok(p): return []
    n=m.I(p); d=m.Q(p+8)
    return [m.i(d+4*k) for k in range(n)] if m.ok(d) else []
Img.lst32=lst32
