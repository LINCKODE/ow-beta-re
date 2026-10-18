#!/usr/bin/env python3
"""Position bridge (T057, docs/InstanceServer.md §12.9): test scaffolding until the server models movement.

Reads the local hero's position from the client through Prometheus (MCP read_component 0x01 of the hero body: scene
component +0x60 = position, the same offset the capture points showed [L]) and posts it to the instance server's
control API (POST /game/pos), ~10 times a second. The unmodified client alone does not do this.

    scripts/posbridge.py [--hz 10] [--control http://127.0.0.1:3729] [--prometheus http://127.0.0.1:17888]
"""
import argparse, json, struct, sys, time, urllib.request


def http(url, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method="POST" if data is not None else "GET",
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=2) as r:
        return json.loads(r.read() or b"null")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--hz", type=float, default=10)
    ap.add_argument("--control", default="http://127.0.0.1:3729")
    ap.add_argument("--prometheus", default="http://127.0.0.1:17888")
    a = ap.parse_args()
    last = None
    while True:
        try:
            conns = http(f"{a.control}/conns") or []
            bodies = [c.get("body") for c in conns if c.get("body")]
            if bodies:
                body = int(str(bodies[-1]), 0)
                r = http(f"{a.prometheus}/mcp/tools/call", {"name": "read_component", "arguments": {
                    "ea": "game", "entity_id": hex(body), "component_id": 1, "length": 0x70}})
                hexs = (r.get("result") or {}).get("hex", "")
                b = bytes.fromhex(hexs.replace(" ", ""))
                if len(b) >= 0x6c:
                    pos = list(struct.unpack_from("<3f", b, 0x60))
                    http(f"{a.control}/game/pos", {"body": body, "pos": pos})
                    if last is None or sum((p - q) ** 2 for p, q in zip(pos, last)) > 1:
                        print(f"{time.strftime('%H:%M:%S')} body {body:#x} at ({pos[0]:.1f}, {pos[1]:.1f}, {pos[2]:.1f})", flush=True)
                        last = pos
        except Exception as e:  # the client or server restarting: keep trying
            print(f"{time.strftime('%H:%M:%S')} {e}", file=sys.stderr, flush=True)
            time.sleep(1)
        time.sleep(1 / a.hz)


if __name__ == "__main__":
    main()
