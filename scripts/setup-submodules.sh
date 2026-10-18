#!/usr/bin/env bash
# Run once after cloning (if the submodules are missing) or right after `git init` of a fresh copy:
# adds Prometheus' third-party submodules at the commits this project was built with.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
add() { # url path commit
    if [[ ! -e "$2/.git" ]]; then
        rmdir "$2" 2>/dev/null || true
        git submodule add -f "$1" "$2"
    fi
    git -C "$2" fetch -q origin "$3" 2>/dev/null || true
    git -C "$2" checkout -q "$3"
}
add https://github.com/microsoft/vcpkg       prometheus/external/vcpkg   11bbc873e00e9e58d4e9dffb30b7a5493a030e0b
add https://github.com/BreakingBread0/imnodes prometheus/external/imnodes b2ec254ce576ac3d42dfb7aef61deadbff8e7211
git submodule update --init --recursive
echo "submodules ready"
