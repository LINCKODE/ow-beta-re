#!/usr/bin/env fish
# Build Prometheus (launcher + PrometheusCore.dll) with llvm-mingw.
# Usage: ./build.fish [Release|Debug] [...]   (default: Release; anything else is ignored)

set -l root (realpath (dirname (status filename)))
set -l config Release
if contains -- "$argv[1]" Release Debug
    set config $argv[1]
end

source "$root/prometheus-prerequisites/env.fish"; or exit 1
cd "$root/prometheus"; or exit 1

# Configure only when the build directory hasn't been generated yet.
if not test -f "output/$config/build.ninja"
    cmake --preset "$config-MinGW"; or exit 1
end

cmake --build "output/$config" --target Prometheus-Launcher
