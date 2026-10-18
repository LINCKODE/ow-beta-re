import pefile,struct,capstone
P='RE-binary/GameClientApp_dump_SCY.exetreated.exe'
pe=pefile.PE(P,fast_load=True); IB=0x7ff64ffe0000
data=open(P,'rb').read()
def off(rva):
    for s in pe.sections:
        if s.VirtualAddress<=rva<s.VirtualAddress+max(s.Misc_VirtualSize,s.SizeOfRawData):
            o=rva-s.VirtualAddress
            if o>=s.SizeOfRawData: return None
            return s.PointerToRawData+o
def rd(rva,n):
    o=off(rva); return data[o:o+n] if o is not None else b'\0'*n
def q(rva): return struct.unpack('<Q',rd(rva,8))[0]
def d(rva): return struct.unpack('<I',rd(rva,4))[0]
md=capstone.Cs(capstone.CS_ARCH_X86,capstone.CS_MODE_64); md.detail=False
def dis(rva,n):
    return list(md.disasm(rd(rva,n),IB+rva))
