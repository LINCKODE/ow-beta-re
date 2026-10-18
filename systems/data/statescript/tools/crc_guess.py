import sys,zlib,itertools,json
# usage: guess.py HASH[,HASH] "prefix1 prefix2" word1 word2 ...   (tries prefix + 1..3 words; also with '_' after prefix)
hs={h.lower() for h in sys.argv[1].split(',')}
pre=sys.argv[2].split()
ws=sys.argv[3:]
n=0
for p in pre:
  for sep in ['','_']:
    c0=zlib.crc32((p+sep).lower().encode())
    for k in (1,2,3):
        for combo in itertools.product(ws,repeat=k):
            s=''.join(combo); n+=1
            h='%08x'%zlib.crc32(s.lower().encode(),c0)
            if h in hs: print('HIT',h,p+sep+s)
print('tried',n,file=sys.stderr)
