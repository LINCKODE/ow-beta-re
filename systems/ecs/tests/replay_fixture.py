#!/usr/bin/env python3
"""Replay client traces (fixtures/*.json, Prometheus `ecs_trace`) through libow_ecs_c and
compare the event streams.

From a trace we reconstruct what the client *did*: per tick and per system, which entities it
spawned (loader trees, components in add order, inline entities), explicit FlushCreated calls
and delete requests; plus actions outside any EA tick. The library then has to produce the same
*consequences*: ids, callbacks (system, entity, order, phase), flushes, destroys, component lists.

usage: replay_fixture.py <libow_ecs_c.so> <fixtures dir> [-v]
"""
import ctypes, json, os, struct, sys


class Event(ctypes.Structure):
    _fields_ = [("seq", ctypes.c_uint64), ("tick", ctypes.c_uint64), ("phase", ctypes.c_int32),
                ("phase_sys", ctypes.c_int32), ("kind", ctypes.c_int32), ("sys", ctypes.c_int32),
                ("ent", ctypes.c_uint32), ("phase_cf", ctypes.c_uint32), ("comp_count", ctypes.c_uint32), ("a", ctypes.c_uint64),
                ("b", ctypes.c_uint64), ("comps", ctypes.c_uint8 * 64)]


P, U32, I = ctypes.c_void_p, ctypes.c_uint32, ctypes.c_int
SIGS = {
    "ecs_admin_create": (P, [ctypes.c_char_p]),
    "ecs_admin_destroy": (None, [P]),
    "ecs_tick": (None, [P, ctypes.c_float]),
    "ecs_loader_create": (P, [ctypes.c_char_p, I, U32, U32]),
    "ecs_loader_add_child": (None, [P, P]),
    "ecs_loader_destroy": (None, [P]),
    "ecs_spawn_loader": (U32, [P, U32, P]),
    "ecs_spawn_inline": (U32, [P, ctypes.c_char_p, I]),
    "ecs_request_delete": (I, [P, U32, I]),
    "ecs_queue_delete": (I, [P, U32]),
    "ecs_flush_created": (None, [P]),
    "ecs_set_id_counter": (None, [P, U32]),
    "ecs_schedule_spawn_loader": (None, [P, I, U32, P]),
    "ecs_schedule_spawn_inline": (None, [P, I, ctypes.c_char_p, I]),
    "ecs_schedule_delete": (None, [P, I, U32, I]),
    "ecs_schedule_flush_created": (None, [P, I]),
    "ecs_trace_take": (I, [P, ctypes.POINTER(Event), I]),
    "ecs_system_count": (I, [P]),
    "ecs_system_vtable_rva": (U32, [P, I]),
    "ecs_set_can_delete": (I, [P, U32, I]),
    "ecs_schedule_can_delete": (None, [P, U32, I]),
    "ecs_set_external": (None, [P, I]),
    "ecs_schedule_id_counter": (None, [P, U32]),
    "ecs_set_parent": (I, [P, U32, U32]),
    "ecs_schedule_target_listener": (None, [P, I, I, U32]),
    "ecs_clock_seed": (None, [P, U32, ctypes.c_int64]),
    "ecs_clock_resync": (None, [P, U32]),
    "ecs_schedule_clock_resync": (None, [P, I, U32]),
    "ecs_clock_set_dilation": (None, [P, ctypes.c_uint8]),
    "ecs_command_frame_listener_count": (I, [P]),
    "ecs_schedule_can_delete_side_effect": (None, [P, U32, U32, I]),
    "ecs_schedule_set_parent": (None, [P, U32, U32]),
    "ecs_iteration": (I, [P, ctypes.c_uint8, ctypes.POINTER(U32), I]),
    "ecs_reserve_slots": (None, [P, ctypes.c_uint8, I]),
    "ecs_trace_kind_name": (ctypes.c_char_p, [ctypes.c_int32]),
    "ecs_trace_phase_name": (ctypes.c_char_p, [ctypes.c_int32]),
}


def load_lib(path):
    lib = ctypes.CDLL(path)
    for name, (res, args) in SIGS.items():
        f = getattr(lib, name)
        f.restype, f.argtypes = res, args
    return lib


COMPARED = {"spawn_begin", "spawn_end", "add_component", "on_created", "on_added", "on_deleting",
            "on_removed", "delete_request", "delete_request_end", "destroy", "flush_created",
            "flush_destroy", "spawn_inline_begin", "spawn_inline_end"}


def load_types(fixtures):
    """component id -> (has_creator, deps) from the game EA manifest."""
    j = json.load(open(os.path.join(fixtures, "../manifest/ecs_game.json")))
    return {int(c["id"], 16): (bool(c.get("creator_vtable")), [int(d, 16) for d in c.get("dependencies", [])])
            for c in j["components"] if "error" not in c}


def simulate_adds(loader, types):
    """The client's add sequence for a loader list: loader components, then missing dependencies
    depth-first (failed adds are retried by later dependents)."""
    have, seq = set(), []

    def add(c):
        seq.append(c)
        ok = types.get(c, (False, []))[0]
        if ok:
            have.add(c)
        return ok

    def deps(c):
        for d in types.get(c, (False, []))[1]:
            if d not in have and add(d):
                deps(d)
    for c in loader:
        add(c)
    for c in loader:
        deps(c)
    return seq


def loader_from_adds(adds, types):
    for k in range(1, len(adds) + 1):
        if simulate_adds(adds[:k], types) == adds:
            return adds[:k]
    return adds


LISTENER_PHASES = {"cf_listener": 0, "cf_pre": 1, "cf_post": 2}


class ListenerSlot:
    """Appending to it tags the action with its listener target."""
    def __init__(self, lst, target):
        self.lst, self.target = lst, target

    def append(self, act):
        act["listener"] = self.target
        self.lst.append(act)


def reconstruct(events, types):
    """-> {tick: {"before": [...], "sys": {i: [...]}, "after": [...]}}, starting id counter.
    Actions are dicts: kind spawn (id, node) / inline (comps) / flush / delete (ent, immediate) /
    queue_delete (ent); plus external (ran on another thread) and can_delete [(ent, allow)].

    Work from other threads (the map loader) interleaves with the main thread under the EA lock;
    it is placed where the main thread was at that moment, in sequence order."""
    tids = {}
    for e in events:
        tids[e["tid"]] = tids.get(e["tid"], 0) + 1
    main_tid = max(tids, key=tids.get)
    plan, counter = {}, None
    stack, inline_depth, dels, open_inline = {}, {}, {}, {}   # per thread
    main_where = None                        # (tick, phase, phase_sys) of the main thread

    def ticks(t):
        return plan.setdefault(t, {"before": [], "sys": {}, "after": [], "listeners": []})

    def slot(e):
        # Another thread, or a system/flush of another EA acting on this one: external work.
        external = e["tid"] != main_tid or "phase_ea" in e
        tick, phase, sysi = (e["tick"], e["phase"], e.get("phase_sys", -1))
        if e["tid"] != main_tid and main_where:
            tick, phase, sysi = main_where
        elif "phase_ea" in e:
            phase = "none"
        t = ticks(tick)
        if phase in LISTENER_PHASES and not external:
            # inside a command-frame listener: queue it for that listener (and CF)
            target = (LISTENER_PHASES[phase], e.get("phase_sys", -1), e.get("phase_cf", 0))
            return ListenerSlot(t["listeners"], target), external
        if phase == "system_tick":
            return t["sys"].setdefault(sysi, []), external
        if phase == "none" or external:
            return t["after"], external
        return None, external  # inside Tick outside systems: consequences only

    def emit(e, act):
        s_, external = slot(e)
        if s_ is not None:
            act["external"] = external
            s_.append(act)

    for e in events:
        k, tid = e["kind"], e["tid"]
        st = stack.setdefault(tid, [])
        if tid == main_tid:
            main_where = (e["tick"], e["phase"], e.get("phase_sys", -1))
        if k == "trace_start":
            counter = int(e["a"], 16)
        elif k == "spawn_begin":
            node = {"id": int(e.get("a", "0"), 16), "adds": [], "children": [], "ev": e}
            if not st:  # scheduled at the begin position; the allocated id is filled in at the end
                node["act"] = {"kind": "spawn", "id": node["id"], "node": node}
                emit(e, node["act"])
            st.append(node)
        elif k == "add_component" and st and not inline_depth.get(tid):
            st[-1]["adds"].append(int(e["a"], 16))
        elif k == "spawn_end":
            node = st.pop()
            node["loader"] = loader_from_adds(node["adds"], types)
            node["high"] = int(e.get("ent", "0x0"), 16) & ~0x1fffffff
            allocated = node["id"] == 0 or (node["id"] & 0x1fffffff) == 0x1fffffff
            if "act" in node and allocated and "ent" in e:
                node["act"]["counter"] = (int(e["ent"], 16) & 0x1fffffff) - 1
            if st:
                st[-1]["children"].append(node)
        elif k == "spawn_inline_begin":
            inline_depth[tid] = inline_depth.get(tid, 0) + 1
            if not st:
                act = {"kind": "inline", "comps": []}
                emit(e, act)
                open_inline.setdefault(tid, []).append(act)
        elif k == "spawn_inline_end":
            inline_depth[tid] -= 1
            if open_inline.get(tid):
                act = open_inline[tid].pop()
                act["comps"] = [int(c, 16) for c in e.get("comps", "").split(",") if c]
                if "ent" in e:
                    act["counter"] = (int(e["ent"], 16) & 0x1fffffff) - 1
        elif k == "clock_resync":
            emit(e, {"kind": "resync", "cf": int(e.get("a", "0x0"), 16)})
        elif k == "flush_created" and not st and e["phase"] == "system_tick":
            emit(e, {"kind": "flush"})
        elif k == "delete_request":
            chain = dels.setdefault(tid, [])   # stack of (root action, entity) per open request
            ent, parent = int(e["ent"], 16), int(e.get("parent", "0x0"), 16)
            if chain:
                root, outer = chain[-1]
                if parent == outer:   # a child, deleted by the recursion: record the link
                    root["parents"].append((ent, parent))
                else:                 # not a child: a component of `outer` deleted it in CanDelete
                    root["side_effects"].append((outer, ent, int(e.get("a", "0x0"), 16)))
                chain.append((root, ent))
                continue
            if st:
                chain.append(({"parents": [], "side_effects": [], "can_delete": []}, ent))
                continue
            act = {"kind": "delete", "ent": ent, "immediate": int(e.get("a", "0x0"), 16),
                   "can_delete": [], "parents": [(ent, parent)] if parent else [], "side_effects": []}
            if e["phase"] == "flush_destroy":
                act["kind"] = "queue_delete"
                ticks(e["tick"])["before"].append(act)
            else:
                emit(e, act)
            chain.append((act, ent))
        elif k == "delete_request_end":
            chain = dels.get(tid, [])
            if chain:
                root, ent = chain.pop()
                root["can_delete"].append((ent, bool(int(e.get("b", "0x0"), 16) & 0x10)))
    return plan, counter


class Replayer:
    def __init__(self, lib, ea_name):
        self.lib, self.h = lib, lib.ecs_admin_create(ea_name.encode())
        self.skip_sys = {i for i in range(lib.ecs_system_count(self.h)) if lib.ecs_system_vtable_rva(self.h, i) == 0}
        self.buf = (Event * 4096)()
        self.kinds = COMPARED

    def loader(self, node):
        comps = bytes(node["loader"])
        l = self.lib.ecs_loader_create(comps, len(comps), node["high"], 0)
        for c in node["children"]:
            self.lib.ecs_loader_add_child(l, self.loader(c))
        return l

    def run(self, act, sys=None):
        lib, h = self.lib, self.h
        kind = act["kind"]
        for ent, allow in act.get("can_delete", []):
            if sys is None:
                lib.ecs_set_can_delete(h, ent, allow)
            else:
                lib.ecs_schedule_can_delete(h, ent, allow)
        # Parent links as recorded at the request (children first, so the chain is rebuilt below the root).
        for outer, target, imm in act.get("side_effects", []):
            lib.ecs_schedule_can_delete_side_effect(h, outer, target, imm)  # direct calls: applied at next action
        for child, parent in reversed(act.get("parents", [])):
            (lib.ecs_set_parent(h, child, parent) if sys is None else lib.ecs_schedule_set_parent(h, child, parent))
        lib.ecs_set_external(h, 1 if act.get("external") else 0)
        if act.get("listener"):
            sys = 0  # scheduled; routed to the listener by the target below
            lib.ecs_schedule_target_listener(h, *act["listener"])
        if "counter" in act:  # the client's id counter is shared by racing threads: take its value as input
            (lib.ecs_set_id_counter(h, act["counter"]) if sys is None else lib.ecs_schedule_id_counter(h, act["counter"]))
        if kind == "spawn":
            l = self.loader(act["node"])
            if sys is None:
                lib.ecs_spawn_loader(h, act["id"], l)
                lib.ecs_loader_destroy(l)
            else:
                lib.ecs_schedule_spawn_loader(h, sys, act["id"], l)
        elif kind == "inline":
            c = bytes(act["comps"])
            (lib.ecs_spawn_inline(h, c, len(c)) if sys is None else lib.ecs_schedule_spawn_inline(h, sys, c, len(c)))
        elif kind == "flush":
            lib.ecs_schedule_flush_created(h, sys)
        elif kind == "delete":
            (lib.ecs_request_delete(h, act["ent"], act["immediate"]) if sys is None
             else lib.ecs_schedule_delete(h, sys, act["ent"], act["immediate"]))
        elif kind == "resync":
            (lib.ecs_clock_resync(h, act["cf"]) if sys is None else lib.ecs_schedule_clock_resync(h, sys, act["cf"]))
        elif kind == "queue_delete":
            lib.ecs_queue_delete(h, act["ent"])
        lib.ecs_set_external(h, 0)

    def take(self, out):
        lib = self.lib
        while True:
            got = lib.ecs_trace_take(self.h, self.buf, len(self.buf))
            for e in self.buf[:got]:
                if e.sys in self.skip_sys:
                    continue  # the client trace can't see Prometheus's own system
                kind = lib.ecs_trace_kind_name(e.kind).decode()
                if kind not in self.kinds:
                    continue
                comps = ",".join(f"{c:02x}" for c in e.comps[:e.comp_count])
                if kind == "add_component":
                    comps = f"{e.a:02x}:{e.b}"
                out.append((int(e.tick), lib.ecs_trace_phase_name(e.phase).decode(), e.phase_sys if e.phase_sys >= 0 else -1,
                            int(e.phase_cf), kind, e.sys, f"0x{e.ent:x}" if e.ent and kind != "spawn_begin" else "", comps))
            if got < len(self.buf):
                return

    def close(self):
        self.lib.ecs_admin_destroy(self.h)


def client_event(e, tick_map):  # tick_map: client tick -> our tick
    comps = e.get("comps", "")
    if "phase_ea" in e:  # work done from another EA's tick: this EA sees it as external
        e = {**e, "phase": "none"}
        e.pop("phase_sys", None)
    if e["kind"] == "add_component":
        comps = f'{int(e["a"], 16):02x}:{int(e["b"], 16)}'
    return (tick_map[e["tick"]], e["phase"], e.get("phase_sys", -1), e.get("phase_cf", 0), e["kind"], e.get("sys", -1),
            e.get("ent", "") if e["kind"] != "spawn_begin" else "", comps)


def canonical(evs):
    """Split into the main-thread stream (exact order) and external work (phase "none": loader
    threads that race the main thread and each other, so their begin/end interleave by
    scheduling). External work is compared per tick as a set of completed operations."""
    main = [e for e in evs if e[1] != "none"]
    ext = sorted((e for e in evs if e[1] == "none" and not e[4].endswith("_begin")), key=lambda x: (x[0], x[6], x[4], x[7]))
    return main + [("external",)] + ext


def drop_destroy_side_effects(events):
    """[?] Destroying an inline entity runs its components' destructors, and one of them asks to
    delete its own entity again (a no-op: kFlagPendingDestroy is set). That's component
    behaviour, not ECS behaviour: drop delete_request/_end pairs right after the entity's destroy."""
    out, i = [], 0
    while i < len(events):
        e = events[i]
        out.append(e)
        if e["kind"] == "destroy":
            j = i + 1
            if j + 1 < len(events) and events[j]["kind"] == "delete_request" and events[j].get("ent") == e.get("ent") \
                    and events[j + 1]["kind"] == "delete_request_end":
                i = j + 2
                continue
        i += 1
    return out


def iteration_check(lib, h, events, start, end):
    """Per component type, the order of the EA iteration list (sorted by component address in the
    client) for entities created during the trace, against the client's dump after the trace."""
    created = {int(e["ent"], 16) for e in events if e["kind"] in ("spawn_end", "spawn_inline_end") and "ent" in e}
    bad, checked = [], 0
    buf = (U32 * 100000)()
    for c in end["components"]:
        client = [i for i in c.get("order", []) if i in created]
        if not client:
            continue
        n = lib.ecs_iteration(h, int(c["id"], 16), buf, len(buf))
        present = set(c.get("order", []))
        ours = [i for i in buf[:n] if i in created and i in present]
        checked += 1
        if ours != client:
            bad.append((c["id"], len(client), next((k for k, (x, y) in enumerate(zip(client, ours)) if x != y), None)))
    return checked, bad


def replay(lib, events, ea_name, types, verbose=False, dumps=None):
    events = drop_destroy_side_effects([e for e in events if e["ea"] == ea_name])
    plan, counter = reconstruct(events, types)
    if not plan:
        return None
    r = Replayer(lib, ea_name)
    if counter is not None:
        lib.ecs_set_id_counter(r.h, counter)
    if dumps:  # slots taken by entities that existed before the trace
        for c in dumps[0]["components"]:
            if c.get("live_count"):
                lib.ecs_reserve_slots(r.h, int(c["id"], 16), c["live_count"])
    ours = []
    # The client's clock (always recorded): resync to its starting point and feed every tick's dt
    # and dilation flags, so the same command frames run (listener work is keyed by CF).
    clock = {e["tick"]: e for e in events if e["kind"] == "clock_dilate"}
    first_adv = next((e for e in events if e["kind"] == "clock_advance"), None)
    if first_adv:
        b = int(first_adv["b"], 16)
        lib.ecs_clock_seed(r.h, b & 0xffffffff, b >> 32)
    # One library tick per client tick (every tick when the clock is known); our tick k <-> ticks[k-1].
    ticks = sorted({*plan, *(e["tick"] for e in events if e["kind"] in COMPARED), *clock})
    for t in ticks:
        p = plan.get(t, {"before": [], "sys": {}, "after": [], "listeners": []})
        for a in p["before"]:
            r.run(a)
        for sysi, acts in p["sys"].items():
            for a in acts:
                r.run(a, sysi)
        for a in p.get("listeners", []):
            r.run(a, 0)
        dt = 0.016
        if t in clock:
            lib.ecs_clock_set_dilation(r.h, int(clock[t].get("b", "0x0"), 16))
            dt = struct.unpack("<f", struct.pack("<I", int(clock[t]["a"], 16)))[0]
        lib.ecs_tick(r.h, dt)
        for a in p["after"]:
            r.run(a)
        r.take(ours)
    iteration = iteration_check(lib, r.h, events, *dumps) if dumps else None
    r.close()
    # our tick counter = index in `ticks` (+1); map client ticks the same way
    idx = {t: i + 1 for i, t in enumerate(ticks)}
    client = canonical([client_event(e, idx) for e in events if e["kind"] in COMPARED])
    ours = canonical(ours)
    mismatch = next((i for i, (a, b) in enumerate(zip(client, ours)) if a != b), None)
    if mismatch is None and len(client) != len(ours):
        mismatch = min(len(client), len(ours))
    if verbose or mismatch is not None:
        lo = max(0, (mismatch or 0) - 6)
        for i in range(lo, min(lo + 16, max(len(client), len(ours)))):
            c = client[i] if i < len(client) else None
            o = ours[i] if i < len(ours) else None
            mark = "  " if c == o else "!!"
            print(f"  {mark} {i:6} client {c}\n  {mark}        ours   {o}")
    return len(client), len(ours), mismatch, iteration


CF_KINDS = {"clock_dilate", "clock_advance", "cf_range", "cf_pre", "cf_listener", "cf_post", "clock_resync"}


def replay_command_frames(lib, events, ea_name, verbose=False):
    """Clock + command frames: feed the client's per-tick dt and dilation flags, compare every
    clock_advance (µs, CF, accumulator), CF range and listener call (index, CF, flags)."""
    E = [e for e in events if e["ea"] == ea_name and e["kind"] in CF_KINDS]
    if not E:
        return None
    h = lib.ecs_admin_create(ea_name.encode())
    n_listeners = 1 + max((e.get("sys", -1) for e in E if e["kind"] == "cf_pre"), default=-1)
    first_adv = next(e for e in E if e["kind"] == "clock_advance")
    b = int(first_adv["b"], 16)
    lib.ecs_clock_seed(h, b & 0xffffffff, b >> 32)
    ticks = [e for e in E if e["kind"] == "clock_dilate"]
    idx = {e["tick"]: k + 1 for k, e in enumerate(ticks)}
    buf = (Event * 4096)()
    ours = []
    for e in ticks:
        lib.ecs_clock_set_dilation(h, int(e.get("b", "0x0"), 16))
        dt = struct.unpack("<f", struct.pack("<I", int(e["a"], 16)))[0]
        lib.ecs_tick(h, dt)
        while True:
            got = lib.ecs_trace_take(h, buf, len(buf))
            for x in buf[:got]:
                kind = lib.ecs_trace_kind_name(x.kind).decode()
                if kind in CF_KINDS:
                    ours.append((int(x.tick), lib.ecs_trace_phase_name(x.phase).decode(), x.phase_sys if x.phase_sys >= 0 else -1,
                                 kind, x.sys, int(x.a), int(x.b)))
            if got < len(buf):
                break
    ours_listeners = lib.ecs_command_frame_listener_count(h)
    lib.ecs_admin_destroy(h)
    client = [(idx.get(e["tick"]), e["phase"], e.get("phase_sys", -1), e["kind"], e.get("sys", -1),
               int(e.get("a", "0x0"), 16), int(e.get("b", "0x0"), 16)) for e in E if e["tick"] in idx]
    mismatch = next((i for i, (x, y) in enumerate(zip(client, ours)) if x != y), None)
    if mismatch is None and len(client) != len(ours):
        mismatch = min(len(client), len(ours))
    if n_listeners != ours_listeners:
        print(f"  listener count: client {n_listeners}, ours {ours_listeners}")
        mismatch = mismatch if mismatch is not None else 0
    if verbose or mismatch is not None:
        lo = max(0, (mismatch or 0) - 4)
        for i in range(lo, min(lo + 12, max(len(client), len(ours)))):
            c = client[i] if i < len(client) else None
            o = ours[i] if i < len(ours) else None
            print(f"  {'  ' if c == o else '!!'} {i:6} client {c}\n  {'  ' if c == o else '!!'}        ours   {o}")
    return len(client), len(ours), mismatch


# fixture -> EAs that must replay exactly
CASES = {
    "trace_setup_controller.json": ["game"],
    "trace_mapload_anubis.json": ["game", "session", "lobby"],
}


def main():
    lib_path, fixtures = sys.argv[1], sys.argv[2]
    verbose = "-v" in sys.argv
    lib = load_lib(lib_path)
    types = load_types(fixtures)
    failed = False
    for name, eas in CASES.items():
        events = json.load(open(os.path.join(fixtures, name)))
        for ea in eas:
            dumps = None
            start = os.path.join(fixtures, f"dump_mapload_start_{ea}.json")
            end = os.path.join(fixtures, f"dump_mapload_end_{ea}.json")
            if name.startswith("trace_mapload") and os.path.exists(start) and os.path.exists(end):
                dumps = (json.load(open(start)), json.load(open(end)))
            res = replay(lib, events, ea, types, verbose, dumps)
            if res is None:
                print(f"skip {name} [{ea}]: no events")
                continue
            n_client, n_ours, mismatch, iteration = res
            ok = mismatch is None
            failed |= not ok
            print(f"{'ok  ' if ok else 'FAIL'} {name} [{ea}]: {n_client} client events, {n_ours} ours"
                  + ("" if ok else f", first difference at #{mismatch}"))
            if iteration:
                checked, bad = iteration
                failed |= bool(bad)
                print(f"{'ok  ' if not bad else 'FAIL'} {name} [{ea}] iteration order: {checked} component types"
                      + ("" if not bad else f", {len(bad)} differ: {bad[:8]}"))
    cf_path = os.path.join(fixtures, "trace_command_frames.json")
    if os.path.exists(cf_path):
        events = json.load(open(cf_path))
        for ea in ("game", "session"):
            res = replay_command_frames(lib, events, ea, verbose)
            if res is None:
                continue
            n_client, n_ours, mismatch = res
            failed |= mismatch is not None
            print(f"{'ok  ' if mismatch is None else 'FAIL'} trace_command_frames.json [{ea}]: {n_client} client clock/CF events, {n_ours} ours"
                  + ("" if mismatch is None else f", first difference at #{mismatch}"))
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
