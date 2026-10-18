#!/usr/bin/env bash
# Build Prometheus (launcher + PrometheusCore.dll) with llvm-mingw.
# Usage: ./build.sh [Release|Debug] [...]   (default: Release; anything else is ignored)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG=Release
if [[ "${1:-}" == Release || "${1:-}" == Debug ]]; then
    CONFIG="$1"
fi

source "$ROOT/prometheus-prerequisites/env.sh"
cd "$ROOT/prometheus"

# Configure only when the build directory hasn't been generated yet.
if [[ ! -f "output/$CONFIG/build.ninja" ]]; then
    cmake --preset "$CONFIG-MinGW"
fi

cmake --build "output/$CONFIG" --target Prometheus-Launcher
