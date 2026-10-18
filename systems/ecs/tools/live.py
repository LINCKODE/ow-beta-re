#!/usr/bin/env python3
"""Drive the live client for ECS research (see systems/README.md).

  live.py launch               start the client (build + run, auto login), wait for lobby + Prometheus MCP
  live.py stop                 kill the client
  live.py instance [map]       send the client into a game (lobby gateway inject of "!instance")
  live.py setup                game server spawns the local controller + map networked entities
  live.py dump [ea...]         ecs_dump game/session/lobby into ../manifest/ecs_<ea>.json
  live.py trace start|stop|status
  live.py trace save <file> [since]   write all recorded events (after seq `since`) to a JSON file
  live.py record-cf [seconds]  (in a match) trace the clock + command-frame listeners for N s (default 5)
                               into ../fixtures/trace_command_frames.json
  live.py mcp <tool> [json]    call any Prometheus MCP tool
  live.py record [--watch-parents]
                               launch, dump manifests, trace a map load (!instance) and the game
                               server's /setup into ../fixtures (+ ECS dumps at trace start and after
                               the map load); --watch-parents records reparenting instead, into
                               ../fixtures/watch_parents_*.json (slow)

Servers must be running (`cd server && make lobby` + `make games`, or `make run`).
"""
import json, os, subprocess, sys, time, urllib.request

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "../../.."))
MANIFEST = os.path.join(ROOT, "systems/ecs/manifest")
PROM = "http://127.0.0.1:17888/mcp/tools"
GATEWAY = "http://127.0.0.1:3726"
GAME = "http://127.0.0.1:3728"


def http(url, body=None, method=None):
    data = body.encode() if isinstance(body, str) else (json.dumps(body).encode() if body is not None else None)
    req = urllib.request.Request(url, data=data, method=method or ("POST" if data is not None else "GET"))
    with urllib.request.urlopen(req, timeout=30) as r:
        return r.read().decode()


def mcp(tool, args=None):
    r = json.loads(http(f"{PROM}/call", {"name": tool, "arguments": args or {}}))
    return r.get("result", r)


def up(fn):
    try:
        return fn()
    except Exception:
        return False


def wait(what, fn, timeout=240):
    t = time.time()
    while time.time() - t < timeout:
        if up(fn):
            return
        time.sleep(2)
    sys.exit(f"timeout waiting for {what}")


def client_pids():
    out = subprocess.run(["pgrep", "-f", "[G]ameClientApp.exe"], capture_output=True, text=True).stdout
    return [int(p) for p in out.split()]


def main(a):
    cmd = a[0] if a else "help"
    if cmd == "launch":
        log = open(os.path.join(ROOT, "run/live-client.log"), "w") if os.path.isdir(os.path.join(ROOT, "run")) else subprocess.DEVNULL
        subprocess.Popen([os.path.join(ROOT, "build-and-run-with-server.fish")], cwd=ROOT, stdout=log, stderr=log,
                         start_new_session=True)
        wait("lobby session", lambda: any(s.get("negotiated") for s in json.loads(http(f"{GATEWAY}/sessions"))))
        wait("Prometheus MCP", lambda: "tools" in http(f"{PROM}/list", {}))
        time.sleep(10)  # let the title screen settle
        print("client up")
    elif cmd == "stop":
        for p in client_pids():
            os.kill(p, 15)
        print("stopped", client_pids() or "")
    elif cmd == "instance":
        line = "instance" + (" " + a[1] if len(a) > 1 else "")
        print(http(f"{GATEWAY}/eval", f'command("{line}")') or "sent")
        newest = lambda: max(json.loads(http(f"{GAME}/conns")), key=lambda c: c["conn_id"])
        wait("game connection", lambda: newest().get("state") == "connected")
        print(json.dumps(newest(), indent=1))
    elif cmd == "setup":
        conn = max(json.loads(http(f"{GAME}/conns")), key=lambda c: c["conn_id"])["conn_id"]
        print(http(f"{GAME}/setup?conn={conn}", ""))
    elif cmd == "dump":
        out_dir, prefix = MANIFEST, "ecs_"
        if a[1:2] == ["--to"]:
            out_dir, prefix, a = os.path.dirname(a[2]), os.path.basename(a[2]), a[2:]
        os.makedirs(out_dir, exist_ok=True)
        for ea in a[1:] or ["game", "session", "lobby"]:
            j = mcp("ecs_dump", {"ea": ea})
            path = os.path.join(out_dir, f"{prefix}{ea}.json")
            json.dump(j, open(path, "w"), indent=1)
            print(ea, path, "error" in j and j["error"] or f'{len(j["systems"])} systems, {j["entity_count"]} entities')
    elif cmd == "trace":
        sub = a[1] if len(a) > 1 else "status"
        if sub == "save":
            # The game writes the events itself (JSON lines, Wine path of a local file): no HTTP paging.
            since, t0 = int(a[3]) if len(a) > 3 else 0, time.time()
            tmp = os.path.abspath(a[2]) + ".jsonl"
            r = mcp("ecs_trace", {"action": "save", "since": since, "path": "Z:" + tmp.replace("/", "\\")})
            if "error" in r and "compiled out" in r["error"]:
                # Prometheus built without MCP writes (the default): page the events over HTTP instead (slower).
                events, last = [], since
                while True:
                    page = mcp("ecs_trace", {"action": "fetch", "since": last, "limit": 2000})
                    if "error" in page:
                        sys.exit(f"trace fetch: {page['error']}")
                    events += page["events"]
                    if page["last_seq"] == last:
                        break
                    last = page["last_seq"]
                r = {"last_seq": last}
            elif "error" in r:
                sys.exit(f"trace save: {r['error']}")
            else:
                events = [json.loads(line) for line in open(tmp)]
                os.remove(tmp)
            json.dump(events, open(a[2], "w"))
            print(len(events), "events ->", a[2], "last_seq", r["last_seq"], f"({time.time() - t0:.1f} s)")
        else:
            print(json.dumps(mcp("ecs_trace", {"action": sub}), indent=1))
    elif cmd == "record":
        t_start = time.time()
        fixtures = os.path.join(ROOT, "systems/ecs/fixtures")
        watch = "--watch-parents" in a
        pre = "watch_parents_" if watch else ""
        if not client_pids():
            main(["launch"])
        main(["dump"])
        main(["dump", "--to", os.path.join(fixtures, f"{pre}dump_mapload_start_")])
        mcp("ecs_trace", {"action": "clear"})
        mcp("ecs_trace", {"action": "start", "watch_parents": watch, "cap": 1000000})
        main(["instance"])
        time.sleep(60 if watch else 40)  # map load settles
        main(["trace", "save", os.path.join(fixtures, f"{pre}trace_mapload_anubis.json")])
        main(["dump", "--to", os.path.join(fixtures, f"{pre}dump_mapload_end_"), "game", "session"])
        if watch:
            main(["setup"])
            time.sleep(15)
            main(["trace", "save", os.path.join(fixtures, "watch_parents_trace_all.json")])
            return
        last = mcp("ecs_trace", {"action": "status"})["total"]
        mcp("ecs_trace", {"action": "clear"})  # the map load is saved; free the buffer
        main(["setup"])
        time.sleep(8)
        main(["trace", "save", os.path.join(fixtures, "trace_setup_controller.json"), str(last)])
        main(["dump", "game", "session"])
        print(f"record took {time.time() - t_start:.0f} s")
    elif cmd == "record-cf":
        secs = float(a[1]) if len(a) > 1 else 5
        mcp("ecs_trace", {"action": "stop"})
        mcp("ecs_trace", {"action": "clear"})
        mcp("ecs_trace", {"action": "start", "cf": True, "cap": 1000000})
        time.sleep(secs)
        mcp("ecs_trace", {"action": "stop"})
        main(["trace", "save", os.path.join(ROOT, "systems/ecs/fixtures/trace_command_frames.json")])
    elif cmd == "mcp":
        print(json.dumps(mcp(a[1], json.loads(a[2]) if len(a) > 2 else {}), indent=1))
    else:
        print(__doc__)


if __name__ == "__main__":
    main(sys.argv[1:])
