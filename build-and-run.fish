#!/usr/bin/env fish
# Build Prometheus, then start it if the build succeeded.
# Usage: ./build-and-run.fish [Release|Debug] [-- <game arguments...>]   (see run script)

set -l root (realpath (dirname (status filename)))

"$root/build.fish" $argv; or exit 1
exec "$root/run.fish" $argv
