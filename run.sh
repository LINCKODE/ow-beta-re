#!/usr/bin/env bash
# Start Prometheus through umu + GE-Proton (DXVK; plain Wine's wined3d crashes).
#
# Usage: ./run.sh [Release|Debug] [-- <game arguments...>]
#   Without "--" the launcher window opens as usual.
#   With "--" the game launches straight away using the saved launcher config,
#   with everything after "--" passed to the game, e.g.
#     ./run.sh -- --BNetServer=127.0.0.1:1119 --lobbyServer=127.0.0.1:3724
#
# Override WINEPREFIX, PROTONPATH or UMU_RUN in the environment if needed.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG=Release
if [[ "${1:-}" == Release || "${1:-}" == Debug ]]; then
    CONFIG="$1"
    shift
fi
LAUNCHER_ARGS=()
if [[ "${1:-}" == "--" ]]; then
    LAUNCHER_ARGS=(--launch "$@")
elif [[ $# -gt 0 ]]; then
    echo "Usage: $0 [Release|Debug] [-- <game arguments...>]" >&2
    exit 1
fi
BIN="$ROOT/prometheus/output/$CONFIG/bin"

export WINEPREFIX="${WINEPREFIX:-$HOME/.prometheus.wineprefix}"
export PROTONPATH="${PROTONPATH:-$HOME/.local/share/Steam/compatibilitytools.d/GE-Proton10-29}"
export GAMEID="${GAMEID:-umu-default}"
UMU_RUN="${UMU_RUN:-$HOME/.local/share/lutris/runtime/umu/umu-run}"

if [[ ! -f "$BIN/Prometheus.exe" ]]; then
    echo "Prometheus.exe not found in $BIN - run ./build.sh first." >&2
    exit 1
fi

cd "$BIN"
exec "$UMU_RUN" ./Prometheus.exe "${LAUNCHER_ARGS[@]}"
