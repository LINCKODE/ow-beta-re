#!/usr/bin/env bash
# Download the llvm-mingw toolchain Prometheus is built with (env.sh / env.fish expect it next to them).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
VER=20260407
NAME="llvm-mingw-$VER-ucrt-ubuntu-22.04-x86_64"
if [[ -d "$NAME" ]]; then echo "$NAME already present"; exit 0; fi
curl -fL -o "$NAME.tar.xz" "https://github.com/mstorsjo/llvm-mingw/releases/download/$VER/$NAME.tar.xz"
tar -xf "$NAME.tar.xz"
rm "$NAME.tar.xz"
echo "ok: $NAME"
