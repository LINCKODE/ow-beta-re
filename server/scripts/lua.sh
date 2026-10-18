#!/usr/bin/env bash
# Runs Lua in the lobby gateway's persistent state and prints the result.
#   scripts/lua.sh 'sessions()'
#   scripts/lua.sh < experiment.lua
# LOBBY_CONTROL overrides the control API address (default 127.0.0.1:3726).
addr=${LOBBY_CONTROL:-127.0.0.1:3726}
if [[ $# -gt 0 ]]; then src="$*"; else src=$(cat); fi
curl -sS "http://$addr/eval" --data-binary "$src"
