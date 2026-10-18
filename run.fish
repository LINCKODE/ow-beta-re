#!/usr/bin/env fish
# Start Prometheus through umu + GE-Proton (DXVK; plain Wine's wined3d crashes).
#
# Usage: ./run.fish [Release|Debug] [-- <game arguments...>]
#   Without "--" the launcher window opens as usual.
#   With "--" the game launches straight away using the saved launcher config,
#   with everything after "--" passed to the game, e.g.
#     ./run.fish -- --BNetServer=127.0.0.1:1119 --lobbyServer=127.0.0.1:3724
#
# Override WINEPREFIX, PROTONPATH or UMU_RUN in the environment if needed.

set -l root (realpath (dirname (status filename)))
set -l config Release
if contains -- "$argv[1]" Release Debug
    set config $argv[1]
    set -e argv[1]
end
set -l launcher_args
if test "$argv[1]" = --
    set launcher_args --launch $argv
else if test (count $argv) -gt 0
    echo "Usage: "(status filename)" [Release|Debug] [-- <game arguments...>]" >&2
    exit 1
end
set -l bin "$root/prometheus/output/$config/bin"

set -q WINEPREFIX; or set -gx WINEPREFIX "$HOME/.prometheus.wineprefix"
set -q PROTONPATH; or set -gx PROTONPATH "$HOME/.local/share/Steam/compatibilitytools.d/GE-Proton10-29"
set -q GAMEID; or set -gx GAMEID umu-default
set -q UMU_RUN; or set -l UMU_RUN "$HOME/.local/share/lutris/runtime/umu/umu-run"

if not test -f "$bin/Prometheus.exe"
    echo "Prometheus.exe not found in $bin - run ./build.fish first." >&2
    exit 1
end

cd "$bin"; or exit 1
exec $UMU_RUN ./Prometheus.exe $launcher_args
