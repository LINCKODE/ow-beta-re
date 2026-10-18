# T019: re-apply statescript_names.csv to the existing catalogue JSON and re-render the markdown
# (no cat2/cat3/Ghidra needed). Run from anywhere: python3 apply_names.py
import json,os
from gen import NAMES,name,write_md
HERE=os.path.dirname(os.path.abspath(__file__))
J=os.path.join(HERE,'..','statescript_node_catalogue.json')
MD=os.path.join(HERE,'..','..','..','..','docs','StatescriptNodeCatalogue.md')
rows=json.load(open(J))
for o in rows:
    if o['hash'] in NAMES: o['name'],o['name_src']=name(o['hash'])
    r=NAMES.get(o['hash'])
    o['name_confidence']=r['confidence'] if r else None
    o['name_method']=r['method'] if r else None
    if o['parent']: o['parent_name']=name(o['parent'])[0] or o['parent_name']
    if o['impl_from']: o['impl_from_name']=name(o['impl_from'])[0] or o['impl_from_name']
json.dump(rows,open(J,'w'),indent=1)
print(*write_md(rows,MD))
print('named',sum(1 for o in rows if o['name']),'unnamed',sum(1 for o in rows if not o['name']),'unnamed+used',sum(1 for o in rows if not o['name'] and o['instances']))
