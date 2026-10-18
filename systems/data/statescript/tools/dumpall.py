from mcp import *; import json,struct,os
G=json.load(open('systems/data/live_anubis/graphs_live.json'))['resources']
meta={}
for g in G:
    guid=g['resource_id']; fn='graphs/%s.mem'%guid[2:]
    if os.path.exists(fn): continue
    try:
        root=int(call('read_stu_object',{'resource_id':guid,'depth':0,'max_list':1})['addr'],16)
    except Exception as e: print('fail',guid,e); continue
    base=root-0x10
    magic,ver,tab=struct.unpack('<IIq',read(base,16))
    count,pad,entries=struct.unpack('<IIq',read(tab,16))
    end=entries+16*count
    img=read(base,end-base); open(fn,'wb').write(img)
    meta[guid]={'base':base,'size':end-base,'count':count}
    print(guid,count,end-base,flush=True)
json.dump(meta,open('graphs/meta.json','w'))
