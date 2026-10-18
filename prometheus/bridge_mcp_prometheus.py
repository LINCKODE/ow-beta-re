#!/usr/bin/env python3
"""
MCP bridge for the in-process HTTP server in PrometheusCore.dll.

Talks MCP (stdio) to the host (e.g. Claude Code) and translates each tool call
into a POST to http://127.0.0.1:17888/mcp/tools/call. Tool list is sourced
dynamically from /mcp/tools/list so the bridge auto-updates when new tools are
added in C++ without changes here.

Install requirements:
    pip install mcp requests

Then configure your MCP host to launch this script.
"""

import asyncio
import json
import os
import sys
import requests

from mcp.server import Server
from mcp.server.stdio import stdio_server
from mcp.types import Tool, TextContent

PROMETHEUS_URL = os.environ.get("PROMETHEUS_MCP_URL", "http://127.0.0.1:17888")
TIMEOUT_S = float(os.environ.get("PROMETHEUS_MCP_TIMEOUT", "10"))

app = Server("prometheus")


def _post(path: str, body: dict | None = None) -> dict:
    url = f"{PROMETHEUS_URL}{path}"
    try:
        r = requests.post(url, json=body or {}, timeout=TIMEOUT_S)
        r.raise_for_status()
        return r.json()
    except requests.RequestException as e:
        return {"isError": True, "error": f"transport: {e}"}


@app.list_tools()
async def list_tools() -> list[Tool]:
    data = _post("/mcp/tools/list")
    tools = data.get("tools", [])
    out = []
    for t in tools:
        out.append(Tool(
            name=t["name"],
            description=t.get("description", ""),
            inputSchema=t.get("inputSchema", {"type": "object"}),
        ))
    return out


@app.call_tool()
async def call_tool(name: str, arguments: dict) -> list[TextContent]:
    data = _post("/mcp/tools/call", {"name": name, "arguments": arguments})
    if data.get("isError"):
        return [TextContent(type="text", text=f"Error: {data.get('error', 'unknown')}")]
    result = data.get("result", data)
    return [TextContent(type="text", text=json.dumps(result, indent=2))]


async def main():
    async with stdio_server() as (read_stream, write_stream):
        await app.run(read_stream, write_stream, app.create_initialization_options())


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except (KeyboardInterrupt, asyncio.CancelledError):
        sys.exit(0)
