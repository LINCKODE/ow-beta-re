import requests,time
MCP='http://127.0.0.1:17888/mcp/tools/call'
def call(name,args,timeout=20):
    r=requests.post(MCP,json={"name":name,"arguments":args},timeout=timeout).json()
    if r.get('isError'): raise RuntimeError(r)
    return r['result']
def read(addr,n):
    out=b''
    while n>0:
        k=min(n,4096)
        h=call('read_memory_abs',{'addr':'0x%x'%addr,'length':k})['hex']
        out+=bytes.fromhex(h.replace(' ','').replace('\n','')); addr+=k; n-=k
        time.sleep(0.01)
    return out
