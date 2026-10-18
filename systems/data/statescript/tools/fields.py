import json,sys
reg=json.load(open('systems/data/client_stu_registry.json'))
N='systems/stu/names/'
tn={};fn={}
for l in open(N+'KnownTypes.csv'):
    p=[x.strip() for x in l.split(',')]
    if len(p)>=2: tn[p[0].lower()]=p[1]
for h,v in json.load(open('names_brute.json')).items(): tn.setdefault(h,v)
for l in open(N+'KnownFields.csv'):
    p=[x.strip() for x in l.split(',')]
    if len(p)>=2: fn[p[0].lower()]=p[1]
K={0:'prim',1:'prim[]',2:'obj',3:'obj[]',4:'inline',5:'inline[]',7:'map',8:'enum',9:'enum[]',10:'fileref',11:'fileref[]',12:'resref',13:'resref[]'}
def show(h,own=False):
    v=reg[h]; print('%s %s size=0x%x parent=%s'%(h,tn.get(h,'?'),v['size'],tn.get(v['parent'],v['parent'])))
    for f in v['fields']:
        print('   +0x%-4x %-24s %-9s %s flags=%d'%(f['offset'],fn.get(f['hash'],f['hash']),K.get(f['ctype'],f['ctype']),tn.get(f['elem'],f['elem']) if f['elem'] else '',f['flags']))
if __name__=='__main__':
    for h in sys.argv[1:]: show(h)
