import ctypes as C
ROOT = "."
L = C.CDLL(f"{ROOT}/systems/content/build/libow_content_c.so")
class Res(C.Structure):
    _fields_ = [("guid", C.c_uint64), ("package", C.c_uint64), ("type", C.c_uint32), ("size", C.c_uint32),
                ("flags", C.c_uint32), ("ckey", C.c_uint8 * 16)]
L.content_open_ow08.restype = C.c_void_p
L.content_open_ow08.argtypes = [C.c_char_p, C.c_char_p, C.c_char_p, C.c_char_p, C.c_size_t]
L.content_resources_of_type.argtypes = [C.c_void_p, C.c_uint32, C.POINTER(Res), C.c_size_t]
L.content_resources_of_type.restype = C.c_size_t
L.content_read.argtypes = [C.c_void_p, C.c_uint64, C.POINTER(C.POINTER(C.c_uint8)), C.POINTER(C.c_size_t), C.c_char_p, C.c_size_t]
L.content_free.argtypes = [C.c_void_p]
err = C.create_string_buffer(512)
S = L.content_open_ow08(f"{ROOT}/game/owbetaclient".encode(), None, None, err, 512)
assert S, err.value
def of_type(t):
    n = L.content_resources_of_type(S, t, None, 0)
    arr = (Res * n)(); L.content_resources_of_type(S, t, arr, n)
    return [r.guid for r in arr]
def read(guid):
    p = C.POINTER(C.c_uint8)(); sz = C.c_size_t()
    if not L.content_read(S, guid, C.byref(p), C.byref(sz), err, 512): return None
    b = C.string_at(p, sz.value); L.content_free(p); return b
