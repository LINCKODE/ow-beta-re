import json,struct,glob,collections,os
reg=json.load(open('systems/data/client_stu_registry.json'))
crc2h=collections.defaultdict(list)
for h,v in reg.items(): crc2h[v['layout_crc']].append(h)
amb=[k for k,v in crc2h.items() if len(v)>1]
print('ambiguous crcs',len(amb))
meta=json.load(open('graphs/meta.json'))
use=collections.Counter(); graphs=collections.defaultdict(set); unk=collections.Counter()
for fn in glob.glob('graphs/*.mem'):
    g='0x'+os.path.basename(fn)[:-4]; b=open(fn,'rb').read(); base=meta[g]['base']
    magic,ver,tab=struct.unpack_from('<IIq',b,0)
    count,pad,ent=struct.unpack_from('<IIq',b,tab-base)
    for i in range(count):
        off,flags,crc=struct.unpack_from('<IIQ',b,ent-base+16*i)
        hs=crc2h.get('%016x'%crc)
        if not hs: unk['%016x'%crc]+=1; continue
        h=hs[0] if len(hs)==1 else '|'.join(hs)
        use[h]+=1; graphs[h].add(g)
print('graphs',len(meta),'types used',len(use),'unknown crcs',len(unk),sum(unk.values()))
json.dump({'instances':use,'graphs':{k:len(v) for k,v in graphs.items()}},open('usage.json','w'))
