// CRC32 meet-in-the-middle name search.
// T019. Build: gcc -O2 -o crc_mitm crc_mitm.c
// usage: crc_mitm prefixes vocabF depthF vocabB targets  -> lines: target\tprefix\tfwd words\tlast word
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
static uint32_t T[256]; static uint8_t INV[256];
static void init(){for(uint32_t i=0;i<256;i++){uint32_t c=i;for(int k=0;k<8;k++)c=c&1?0xEDB88320u^(c>>1):c>>1;T[i]=c;INV[c>>24]=i;}}
static uint32_t fwd(uint32_t r,const char*s){while(*s)r=T[(r^(uint8_t)*s++)&0xff]^(r>>8);return r;}
static uint32_t bwd(uint32_t r,const char*s){int n=strlen(s);for(int k=n-1;k>=0;k--){uint8_t i=INV[r>>24];r=((r^T[i])<<8)|((i^(uint8_t)s[k])&0xff);}return r;}
static char**rd(const char*f,int*n){FILE*F=fopen(f,"r");char b[512];int cap=1024;char**v=malloc(cap*sizeof*v);*n=0;while(fgets(b,512,F)){b[strcspn(b,"\r\n")]=0;if(*n==cap)v=realloc(v,(cap*=2)*sizeof*v);v[(*n)++]=strdup(b);}fclose(F);return v;}
typedef struct{uint32_t s;int32_t a,b;}E;
static E*H;static uint64_t HM;
static void ins(uint32_t s,int a,int b){uint64_t h=(s*2654435761u)&HM;while(H[h].a!=-2){h=(h+1)&HM;}H[h].s=s;H[h].a=a;H[h].b=b;}
int main(int c,char**v){init();int np,nf,nb,nt;char**P=rd(v[1],&np),**F=rd(v[2],&nf),**B=rd(v[4],&nb),**Tg=rd(v[5],&nt);int d=atoi(v[3]);
 uint64_t need=1+nf+(d>=2?(uint64_t)nf*nf:0);uint64_t sz=1;while(sz<need*2)sz<<=1;HM=sz-1;H=malloc(sz*sizeof(E));
 uint32_t*tg=malloc(nt*4);for(int i=0;i<nt;i++)tg[i]=~(uint32_t)strtoul(Tg[i],0,16);
 for(int p=0;p<np;p++){for(uint64_t i=0;i<sz;i++)H[i].a=-2;
  uint32_t r0=fwd(0xffffffffu,P[p]);ins(r0,-1,-1);
  for(int i=0;i<nf;i++){uint32_t r1=fwd(r0,F[i]);ins(r1,i,-1);if(d>=2)for(int j=0;j<nf;j++)ins(fwd(r1,F[j]),i,j);}
  for(int t=0;t<nt;t++)for(int k=-1;k<nb;k++){uint32_t s=k<0?tg[t]:bwd(tg[t],B[k]);uint64_t h=(s*2654435761u)&HM;
   while(H[h].a!=-2){if(H[h].s==s)printf("%08x\t%s\t%s\t%s\t%s\n",~tg[t],P[p],H[h].a>=0?F[H[h].a]:"",H[h].b>=0?F[H[h].b]:"",k<0?"":B[k]);h=(h+1)&HM;}}
  fprintf(stderr,"%s done\n",P[p]);}
}
