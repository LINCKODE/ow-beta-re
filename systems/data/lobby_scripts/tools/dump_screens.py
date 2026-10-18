import sys,subprocess,json; sys.path.insert(0,'.')
from cstore import *
from screens import typ
import os
os.makedirs('scr',exist_ok=True)
for t,p in ((typ(0x09a),'09a'),):
  for g in of_type(t):
    b=read(g)
    if not b: continue
    fn=f'scr/{g:016x}'
    open(fn+'.stu','wb').write(b)
    out=subprocess.run(['systems/stu/build/stu_dump',fn+'.stu'],capture_output=True,text=True).stdout
    open(fn+'.json','w').write(out)
