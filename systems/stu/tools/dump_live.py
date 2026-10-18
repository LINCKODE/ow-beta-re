#!/usr/bin/env python3
"""Dump a resident STU resource from the running client as an as-loaded image.

Usage: dump_live.py GUID OUT.mem   (e.g. 0x058000000000000F)
Writes OUT.mem (bytes from root-0x10 to the end of the instance table) and OUT.mem.json (base address).
Read-only: Prometheus MCP read_stu_object (root address) + read_memory_abs (4 KB per call, runs on the
game's UI thread). Needs the running client; ask the game-lock holder first.
"""
import json,sys,struct,requests
MCP='http://127.0.0.1:17888/mcp/tools/call'
def call(name,args):
    r=requests.post(MCP,json={"name":name,"arguments":args},timeout=20).json()
    if r.get('isError'): raise RuntimeError(r)
    return r['result']
def read(addr,n):
    out=b''
    while n>0:
        k=min(n,4096)
        h=call('read_memory_abs',{'addr':'0x%x'%addr,'length':k})['hex']
        b=bytes.fromhex(h.replace(' ','').replace('\n',''))
        assert len(b)==k,(len(b),k)
        out+=b; addr+=k; n-=k
    return out
guid=sys.argv[1]; out=sys.argv[2]
root=int(call('read_stu_object',{'resource_id':guid,'depth':0,'max_list':1})['addr'],16)
base=root-0x10
hdr=read(base,16); magic,ver,tab=struct.unpack('<IIq',hdr); print('magic %x ver %d table %x'%(magic,ver,tab))
th=read(tab,16); count,pad,entries=struct.unpack('<IIq',th); print('count',count,'entries %x'%entries)
end=entries+16*count
print('base %x size %x'%(base,end-base))
img=read(base,end-base)
open(out,'wb').write(img); json.dump({'guid':guid,'base':base,'size':end-base,'table':tab,'entries':entries,'count':count},open(out+'.json','w'))
