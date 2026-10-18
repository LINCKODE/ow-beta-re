# Build environment for ../prometheus (Release-MinGW / Debug-MinGW presets).
# Mirrors the `environment` block of prometheus/flake.nix without nix.
#
# Usage (bash/zsh):  source ../prometheus-prerequisites/env.sh

_PREREQ_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-${(%):-%x}}")" && pwd)"

export LLVM_MINGW="$_PREREQ_DIR/llvm-mingw-20260407-ucrt-ubuntu-22.04-x86_64"
export PROMETHEUS_ROOT="$(cd "$_PREREQ_DIR/../prometheus" && pwd)"

# llvm-mingw must come before /usr/bin: vcpkg's mingw toolchain looks up
# x86_64-w64-mingw32-gcc/g++ on PATH, and Arch's GCC mingw-w64 also provides them.
export PATH="$LLVM_MINGW/bin:$PATH"

# bin/pwsh is a no-op stand-in for PowerShell (see the comment inside it).
export PATH="$_PREREQ_DIR/bin:$PATH"

export CC="x86_64-w64-mingw32-clang"
export CXX="x86_64-w64-mingw32-clang++"
export RC="x86_64-w64-mingw32-windres"

export LIBRARY_PATH="$LLVM_MINGW/lib"
export C_INCLUDE_PATH="$LLVM_MINGW/include"
export CMAKE_PREFIX_PATH="$LLVM_MINGW/x86_64-w64-mingw32"

export VCPKG_ROOT="$PROMETHEUS_ROOT/external/vcpkg"
export VCPKG_DISABLE_METRICS=1

unset _PREREQ_DIR
