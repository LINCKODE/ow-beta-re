#!/usr/bin/env python3
"""Generate ../state_data_formats.json: the Statescript state-data format of every state type (T052).

The state data a Statescript packet carries after a state's active bit (docs/StatescriptSync.md §2.6, §4) is
    reflected mirror fields (vfunc +0x208 lists mirror objects; each field is read unless flagged)
  + the class's own reader (vfunc +0x1F8, called with local = packet local flag, full = start CF 0).

Mechanical inputs:
  - the node catalogue (state types, their runtime classes per priority layer, nearest implemented ancestor),
  - the client binary (vtable slots +0x1F0 writer / +0x1F8 reader / +0x208 mirror list of each effective class),
  - the client STU registry (mirror types' fields and flags: 0x8 never read, 0x10 local only, 0x20 full only).
Hand-read input (Ghidra, one entry per distinct reader function): READERS below. Each class reader was
decompiled and its bit grammar written down in the small DSL described at FIELD KINDS. MIRRORS maps each distinct
mirror-list function to the mirror object's offset in the state and its STU type (found by scanning the state
constructors with capstone during T052).

Checks:
  - every reader / mirror-list function met in a vtable must be in READERS / MIRRORS (else it fails);
  - the formats are replayed against every active state in the live ss_serialize_local snapshots
    (systems/statescript/fixtures/snap_genji_*.json, hero_serialize_local.json): the model must consume exactly the
    recorded bits (types whose client writer differs from its reader are reported, not failed);
  - the live-verified formats (VERIFIED) must match.

Usage: gen_state_data_formats.py   (needs pefile)
"""
import json, os, struct, sys, collections

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), '../../../..'))
BIN = os.path.join(ROOT, 'RE-binary/GameClientApp_dump_SCY.exetreated.exe')
CAT = os.path.join(ROOT, 'systems/data/statescript/statescript_node_catalogue.json')
REG = os.path.join(ROOT, 'systems/data/client_stu_registry.json')
HEROES = os.path.join(ROOT, 'systems/data/heroes/heroes.json')
FIX = os.path.join(ROOT, 'systems/statescript/fixtures')
OUT = os.path.join(ROOT, 'systems/data/statescript/state_data_formats.json')

GHIDRA = 0x7ff64ffe0000  # Ghidra address = GHIDRA + RVA
NOOP = 0x5a0c20          # RVA of the empty vfunc (Ghidra 0x7ff650580c20)
RET0 = 0x5a45e0          # RVA of "return 0" (Ghidra 0x7ff6505845e0)
HAS_UPDATE = {'7bf31629', '860ee5fc', 'ffbfa88d'}  # G:ecfee0: ScaleTime, WeaponVolley, Effect

# FIELD KINDS
#   {"k": "bit"} | {"k": "u", "n": N}                     raw bits
#   {"k": "small"}  0+4 | 10+8 | 11+16                    (G:1050a30 / G:f2ab10 / G:ee4a70 / G:f2b6f0)
#   {"k": "count"}  0+8 | 10+16 | 11+32                   (G:10507e0 / G:ee4ff0)
#   {"k": "scount"} sign bit, then count                   (G:f2b4f0)
#   {"k": "pred"}   stack predecessor (G:f2b360): bit; if 1: small(instance id), then the predecessor's state index:
#                   local u<its graph's m_statesBitCount>, remote u<m_remoteSyncNodesBitCount> (index into m_remoteSyncNodes)
#   {"k": "if", "bit": "<name of an earlier bit>", "then": [...], "else": [...]}
#   {"k": "ifnode", "param": "<node property>", "then": [...]}  depends on the STU node, not on the stream
#   {"k": "cases"}  one bit per m_cases entry (node +0x100 list), at most 64
#   {"k": "custom", "text": ...}  described in text (Sequencer)
# "name" labels a field. Bits name the fields an "if" refers to.
def B(name): return {'k': 'bit', 'name': name}
def U(n, name): return {'k': 'u', 'n': n, 'name': name}
def IF(bit, then, els=None): return {'k': 'if', 'bit': bit, 'then': then, 'else': els or []}
PRED = {'k': 'pred', 'name': 'stack predecessor'}

# Distinct +0x1F8 readers (Ghidra address), read in Ghidra (T052). local/remote = format by the packet's local flag.
READERS = {
    0x7ff650f2af00: dict(fn='stack member (pred only)', local=[PRED], remote=[PRED],
                         note='reads the predecessor and re-links the state in the stack registry'),
    0x7ff650f35560: dict(fn='bool state', local=[B('value')], remote=[B('value')], note='+0x120, fires the value-changed callback'),
    0x7ff651075dc0: dict(fn='Extender', local=[{'k': 'scount', 'name': 'CF*16 - begin'}], remote=[],
                         note='begin time (+0x48) = CF*16 - value'),
    0x7ff650d4b140: dict(fn='TrackRay', local=[], remote=[], note='reads nothing; prunes its entries older than the CF'),
    0x7ff65106ec30: dict(fn='TrackTargets', local=[B('has'), IF('has', [{'k': 'small', 'name': 'n'},
                         {'k': 'repeat', 'count': 'n', 'body': [U(32, 'entity id'), {'k': 'count', 'name': 'age ms'}]}])],
                         remote='=local', note='0 = no targets (1 bit idle); target time = now - age'),
    0x7ff650d48bc0: dict(fn='BlinkEffect', local=[], remote=[U(32, 'id')], note='remote: id pushed into a sorted list'),
    0x7ff650d47d30: dict(fn='Invisible', local=[PRED], remote=[PRED], note='then applies (no more bits)'),
    0x7ff650d473d0: dict(fn='HistoryRewinder', local=[U(16, 'value')], remote=[U(16, 'value')], note='+0x5C0 = value / const'),
    0x7ff6510671b0: dict(fn='Ability',
                         local=[{'k': 'ifnode', 'param': 'ability_priority', 'then': [U(32, 'priority'), B('priority flag')]},
                                B('cooldown'), IF('cooldown', [U(32, 'cooldown f32'), B('has rate'), IF('has rate', [U(32, 'rate f32')]),
                                                              {'k': 'count', 'name': '(CF+1)*16 - start'}]), PRED],
                         remote=[B('cooldown'), IF('cooldown', [U(32, 'cooldown f32'), B('has rate'), IF('has rate', [U(32, 'rate f32')]),
                                                               {'k': 'count', 'name': '(CF+1)*16 - start'}]), PRED],
                         note='ability_priority: node 0x92c13ac6 (+0x1E0) set and not a STUConfigVarStackPriority; rate defaults to 1.0, cooldown to 0'),
    0x7ff651084c80: dict(fn='Anim', local=[U(3, 'serial'), B('has anim'), IF('has anim', [B('has time'), IF('has time', [{'k': 'count', 'name': 'time ms'}])])],
                         remote='=local', note='serial = low 3 bits of the state serial (+0x44)'),
    0x7ff650da4640: dict(fn='CameraChase', local=[PRED], remote=[PRED]),
    0x7ff650da4990: dict(fn='CameraTransform', local=[PRED], remote=[PRED]),
    0x7ff650d638c0: dict(fn='SprayEffect (G:1083c20)', local=[B('has'), IF('has', [U(16, 'dir1 a'), U(16, 'dir1 b'), U(32, '+0xC0'), U(32, '+0xC4'),
                         U(32, '+0xC8'), U(16, 'dir2 a'), U(16, 'dir2 b'), B('has entity'),
                         IF('has entity', [B('flag'), {'k': 'count', 'name': 'entity'}, B('has entity2'), IF('has entity2', [{'k': 'count', 'name': 'entity2'}])]),
                         B('+0xE9')])], remote='=local'),
    0x7ff650d5de40: dict(fn='Effect', local=[U(2, 'serial')], remote='=local',
                         note='low 2 bits of the state serial; the client class has no writer (+0x1F0 is the no-op)'),
    0x7ff650ce3f40: dict(fn='3f67b31b', local=[B('b'), U(8, 'value')], remote='=local'),
    0x7ff650f37e80: dict(fn='Switch', local=[{'k': 'cases', 'name': 'case bits'}, B('+0xB0')], remote='=local',
                         note='writer G:f37dc0 writes the same: case bits (<= 64) then one more bit'),
    0x7ff650f37710: dict(fn='Counter', local=[{'k': 'count', 'name': 'value'}, B('+0xAC')], remote='=local'),
    0x7ff650f36c70: dict(fn='Sequencer', local=[{'k': 'custom', 'text': 'small(n), bit, bit b; w = bit length of n-1; if n-1 > 0: (if b and n: n x u<w>), u<w>'}],
                         remote='=local'),
    0x7ff650f35bf0: dict(fn='Stack', local=[PRED, {'k': 'ifnode', 'param': 'stack_priority', 'then': [U(32, 'priority'), B('priority flag')]},
                                            B('on top'), B('under')], remote=[PRED],
                         note='stack_priority: node m_priority (+0x108) set and not a STUConfigVarStackPriority'),
    0x7ff650f33c40: dict(fn='ChaseVar', local=[B('isVec'), B('hasDuration'), B('reached'),
                         IF('isVec', [U(32, 'x f32'), U(32, 'y f32'), U(32, 'z f32')], [U(32, 'x f32')]),
                         IF('hasDuration', [B('wide'), IF('wide', [U(32, 'remaining ms')], [U(16, 'remaining ms')])]),
                         B('closed form'), IF('closed form', [{'k': 'count', 'name': '(CF+1)*16 - last tick'}])], remote='=local'),
    0x7ff650f50f40: dict(fn='SubScript', local=[{'k': 'small', 'name': 'child instance id'}, B('+0xAA')], remote=[],
                         note='remote: nothing'),
    0x7ff651058570: dict(fn='WeaponVolley', local=[{'k': 'scount', 'name': 'CF*16 - begin'}, B('counter != 1'),
                         IF('counter != 1', [{'k': 'small', 'name': 'volley counter'}]), U(6, 'serial')],
                         remote=[B('counter != 1'), IF('counter != 1', [{'k': 'small', 'name': 'volley counter'}]), U(6, 'serial')]),
    0x7ff65107be80: dict(fn='ScaleTime', local=[B('which'), U(32, 'value (+0xB0 if 1, +0xB4 if 0)')], remote='=local'),
    0x7ff650d02370: dict(fn='UXFade', local=[B('+0x3D8')], remote='=local', note='the client class has no writer'),
    0x7ff650cfe700: dict(fn='2785fbdd', local=[PRED], remote=[PRED]),
    0x7ff650f29520: dict(fn='UXHeroHelp', local=[], remote=[], note='reads nothing'),
}

# Distinct +0x208 mirror-list functions (Ghidra address): mirror object offset in the state, its STU type.
# Derived by scanning the state constructors for the sub-object constructor called with rcx = this + offset and
# the vtable it installs (vtable +0x28 returns the STUInfo, type hash at +0x50).
MIRRORS = {
    0x7ff650f32000: (0xA8, {'8de7e50f': '9a97a6c3', 'e811ecbb': 'ff61af77'}),  # TrackMovement, BooleanSwitch
    0x7ff650d40a00: (0xD0, {'f53c5da8': 'efd4b39b'}),                          # Ability
    0x7ff650ce0720: (0xB0, {'970ec198': '28c44860', 'c6366130': '4d83830d'}),  # ChaseVar, c6366130
}

# Live-verified formats (docs/MatchLogic.md §14.7, StatescriptRuntime.md §5.10, T041/T044/T051), local format:
# (type hash, example, the recorded bit count).
VERIFIED = [
    ('f7d6498c', 'Stack idle: no predecessor, on top', 3),
    ('f53c5da8', 'Ability idle: flags 0, no cooldown, no predecessor', 10),
    ('f53c5da8', 'Ability dash cooldown: flags 0, cooldown 0.97, rate 1.0, t = 30', 52),
    ('860ee5fc', 'WeaponVolley: begin offset < 256, counter 1, serial', 17),
    ('970ec198', 'ChaseVar scalar, no duration, closed form, t < 256', 45),
    ('7fb4bf64', 'Anim with time (32-bit form)', 39),
    ('8de7e50f', 'TrackMovement', 1),
    ('a5f89e3f', 'CameraTransform, no predecessor', 1),
    ('429991a7', 'BlinkEffect local', 0),
    ('f6e1e486', 'LedgeClimb', 0),
    ('c739baac', 'UXScreen', 0),
    ('e811ecbb', 'BooleanSwitch', 1),
    ('498b421a', 'TrackTargets idle', 1),
    ('7331cb24', 'Invisible, no predecessor', 1),
]


class PE:
    def __init__(self, path):
        import pefile
        pe = pefile.PE(path, fast_load=True)
        self.img = pe.get_memory_mapped_image()
        self.ib = pe.OPTIONAL_HEADER.ImageBase
        self.pe = pe

    def q(self, rva): return struct.unpack_from('<Q', self.img, rva)[0]
    def d(self, rva): return struct.unpack_from('<I', self.img, rva)[0]
    def slot(self, vt_rva, off): return self.q(vt_rva + off) - self.ib  # -> RVA


def load():
    cat = json.load(open(CAT))
    reg = json.load(open(REG))
    return cat, {n['hash']: n for n in cat}, reg


def effective(n, byh):
    """The runtime class the client instantiates: the type's highest-priority registration, else the nearest
    registered ancestor's (StatescriptRuntime.md §1.8)."""
    src, seen = n, 0
    while not src['runtime'] and src.get('impl_from') and src['impl_from'] != src['hash'] and seen < 16:
        src = byh.get(src['impl_from'], src); seen += 1
    p = src
    while p is not None and not p['runtime']:
        p = byh.get(p.get('parent'))
    if p is None:
        return None, None
    return p, max(p['runtime'], key=lambda r: r['prio'])


def mirror_fields(reg, mtype):
    t = reg.get(mtype)
    if t is None:
        raise SystemExit(f'mirror type {mtype} not in the registry')
    out = []
    for f in t['fields']:
        fl = f['flags']
        read = 'never' if fl & 8 else ('local, full only' if fl & 0x30 == 0x30 else 'local only' if fl & 0x10
                                       else 'full only' if fl & 0x20 else 'always')
        out.append({'hash': f['hash'], 'offset': f['offset'], 'elem': f['elem'], 'ctype': f['ctype'], 'flags': fl, 'read': read})
    return out


# Network widths of reflected primitive fields that are read (field type vfunc +0xB0). Only the ones met in
# mirrors whose fields are read: Ability's flags byte (elem 3b9327d2), 8 bits [G + L: T044].
ELEM_BITS = {'3b9327d2': 8}


def reflected_items(fields, local, full):
    items = []
    for f in fields:
        if f['flags'] & 8: continue
        if f['flags'] & 0x10 and not local: continue
        if f['flags'] & 0x20 and not full: continue
        n = ELEM_BITS.get(f['elem'])
        if n is None:
            raise SystemExit(f'no network width for reflected field element type {f["elem"]}')
        items.append(U(n, f'mirror field {f["hash"]}'))
    return items


# ---- evaluation of the DSL: minimum length, and parsing recorded bits -----------------------------------------
class Bits:
    def __init__(self, hexs, n):
        self.b = bytes.fromhex(hexs) if hexs else b''
        self.n, self.p = n, 0

    def take(self, k):
        if self.p + k > self.n: raise EOFError
        v = 0
        for i in range(k):
            v |= ((self.b[(self.p + i) // 8] >> ((self.p + i) % 8)) & 1) << i
        self.p += k
        return v


def pfx(bits, w0, w1, w2):
    if bits.take(1) == 0: return bits.take(w0)
    if bits.take(1) == 0: return bits.take(w1)
    return bits.take(w2)


def parse(items, bits, env, params):
    for it in items:
        k = it['k']
        if k == 'bit': env[it['name']] = bits.take(1)
        elif k == 'u': env[it['name']] = bits.take(it['n'])
        elif k == 'small': env[it['name']] = pfx(bits, 4, 8, 16)
        elif k == 'count': env[it['name']] = pfx(bits, 8, 16, 32)
        elif k == 'scount': bits.take(1); env[it['name']] = pfx(bits, 8, 16, 32)
        elif k == 'pred':
            if bits.take(1):
                pfx(bits, 4, 8, 16); bits.take(params['states_bits'])
        elif k == 'if': parse(it['then'] if env[it['bit']] else it['else'], bits, env, params)
        elif k == 'ifnode':
            if params.get(it['param']): parse(it['then'], bits, env, params)
        elif k == 'repeat':
            for _ in range(env[it['count']]): parse(it['body'], bits, env, params)
        elif k == 'cases': bits.take(min(params['cases'], 64))
        else: raise ValueError(k)


def min_bits(items):
    n = 0
    for it in items:
        k = it['k']
        n += {'bit': 1, 'small': 5, 'count': 9, 'scount': 10, 'pred': 1}.get(k, 0)
        if k == 'u': n += it['n']
        if k == 'if': n += min(min_bits(it['then']), min_bits(it['else']))
    return n


def fixed(items):
    """True when the length never depends on values or node properties."""
    return all(it['k'] in ('bit', 'u') for it in items)


def main():

    cat, byh, reg = load()
    pe = PE(BIN)
    states = [n for n in cat if n['kind'] == 'State']
    out, unknown = [], set()
    for n in states:
        src, r = effective(n, byh)
        vt = int(r['vtable'], 16)
        rd, wr, ml = pe.slot(vt, 0x1F8), pe.slot(vt, 0x1F0), pe.slot(vt, 0x208)
        e = {'hash': n['hash'], 'name': n['name'], 'uses_resident': n.get('instances'),
             'class': {'impl_type': src['hash'], 'prio': r['prio'], 'vtable': hex(vt), 'size': r['size']},
             'reader': None if rd == NOOP else hex(rd), 'writer': None if wr == NOOP else hex(wr),
             'has_update': n['hash'] in HAS_UPDATE}
        mirrors, local_items, remote_items, notes = [], [], [], []
        if ml not in (NOOP, RET0):
            key = GHIDRA + ml
            if key not in MIRRORS:
                unknown.add(('mirror list', hex(key))); continue
            off, types = MIRRORS[key]
            mt = types.get(src['hash'])
            if mt is None:
                unknown.add(('mirror type for', src['hash'])); continue
            fields = mirror_fields(reg, mt)
            mirrors.append({'offset': hex(off), 'type': mt, 'fields': fields})
        if rd == NOOP:
            tail_l, tail_r = [], []
            fn = None
        else:
            key = GHIDRA + rd
            if key not in READERS:
                unknown.add(('reader', hex(key))); continue
            R = READERS[key]
            fn = R['fn']
            tail_l = R['local']
            tail_r = tail_l if R['remote'] == '=local' else R['remote']
            if R.get('note'): notes.append(R['note'])
        refl_l = sum((reflected_items(m['fields'], True, False) for m in mirrors), [])
        refl_r = sum((reflected_items(m['fields'], False, False) for m in mirrors), [])
        local_items, remote_items = refl_l + tail_l, refl_r + tail_r
        e.update({'reader_fn': fn, 'mirrors': mirrors, 'local': local_items, 'remote': remote_items,
                  'full_differs': False, 'local_min_bits': min_bits(local_items), 'remote_min_bits': min_bits(remote_items),
                  'local_fixed': fixed(local_items), 'remote_fixed': fixed(remote_items),
                  'writer_matches_reader': (rd == NOOP) == (wr == NOOP), 'notes': notes})
        if (rd == NOOP) != (wr == NOOP):
            e['notes'].append('client writer/reader asymmetry: the client never writes this data; '
                              'a client-side serialization of it (Prometheus ss_serialize_local) is not what the reader expects')
        out.append(e)
    if unknown:
        raise SystemExit('unhandled: ' + ', '.join(f'{a} {b}' for a, b in sorted(unknown)))
    by = {e['hash']: e for e in out}

    # Check 1: live-verified formats.
    problems = []
    for h, what, nbits in VERIFIED:
        e = by.get(h)
        if e is None:
            problems.append(f'verified type {h} missing'); continue
        e.setdefault('verified_live', []).append({'example': what, 'bits': nbits})
    for e in out:
        for v in e.get('verified_live', []):
            if e['local_fixed'] and e['local_min_bits'] != v['bits']:
                problems.append(f"{e['name'] or e['hash']}: fixed {e['local_min_bits']} bits, live {v['bits']} ({v['example']})")
            if not e['local_fixed'] and v['bits'] < e['local_min_bits']:
                problems.append(f"{e['name'] or e['hash']}: minimum {e['local_min_bits']} bits > live {v['bits']} ({v['example']})")

    # Check 2: replay every active state of the live snapshots through the model.
    heroes = json.load(open(HEROES))
    graphs = {int(g['graph'], 16): g for g in heroes['graphs']}
    replay = collections.Counter(); asym = collections.Counter()
    for fn in ('snap_genji_fire2', 'snap_genji_dash', 'snap_genji_deflect', 'hero_serialize_local'):
        path = os.path.join(FIX, fn + '.json')
        if not os.path.exists(path): continue
        snap = json.load(open(path))
        for inst in snap['instances']:
            g = graphs.get(int(inst['graph'], 16) & 0xFFFF)
            if g is None: continue
            sb = inst.get('idx_bits', 0)
            for a in inst['active']:
                h = g['state_types'][a['state']][2:]
                e = by.get(h)
                if e is None: continue
                ok = False
                for prio in (False, True):
                    params = {'states_bits': sb, 'stack_priority': prio, 'ability_priority': prio, 'cases': max(0, a['data_bits'] - 1)}
                    b = Bits(a['data_hex'], a['data_bits'])
                    try:
                        parse(e['local'], b, {}, params)
                        ok = b.p == a['data_bits']
                    except (EOFError, KeyError):
                        ok = False
                    if ok: break
                if ok:
                    replay[(e['name'] or h, 'ok')] += 1
                elif not e['writer_matches_reader']:
                    asym[e['name'] or h] += 1
                else:
                    problems.append(f"{fn} instance {inst['instance']} state {a['state']} ({e['name'] or h}): "
                                    f"{a['data_bits']} bits {a['data_hex']!r} don't parse")
    doc = {
        'source': 'systems/data/statescript/tools/gen_state_data_formats.py (T052); docs/StatescriptSync.md §4',
        'grammar': 'field kinds: see the generator header. Data = the fields of `local` or `remote` (by the packet local flag); '
                   '`full` never changes a format in this build (no mirror field is flagged 0x20, no reader tests it for bits).',
        'has_update_types': sorted(HAS_UPDATE),
        'counts': {'state_types': len(out), 'with_data_local': sum(1 for e in out if e['local']),
                   'with_data_remote': sum(1 for e in out if e['remote'])},
        'live_replay': {f'{k[0]}': v for k, v in sorted(replay.items())},
        'live_replay_skipped_asymmetric': dict(asym),
        'types': sorted(out, key=lambda e: (-(e['uses_resident'] or 0), e['hash'])),
    }
    json.dump(doc, open(OUT, 'w'), indent=1)
    print(f"{len(out)} state types, {doc['counts']['with_data_local']} with local data, "
          f"{doc['counts']['with_data_remote']} with remote data; live replay: {sum(replay.values())} states ok")
    if problems:
        print('PROBLEMS:\n  ' + '\n  '.join(problems)); sys.exit(1)


if __name__ == '__main__':
    main()
