#!/usr/bin/env bash
# Build Prometheus, then start it if the build succeeded.
# Usage: ./build-and-run.sh [Release|Debug] [-- <game arguments...>]   (see run script)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

"$ROOT/build.sh" "$@"
exec "$ROOT/run.sh" "$@"
