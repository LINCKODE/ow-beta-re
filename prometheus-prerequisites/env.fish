# Build environment for ../prometheus (Release-MinGW / Debug-MinGW presets).
# Mirrors the `environment` block of prometheus/flake.nix without nix.
#
# Usage (fish):  source ../prometheus-prerequisites/env.fish

set -l prereq_dir (realpath (dirname (status filename)))

set -gx LLVM_MINGW "$prereq_dir/llvm-mingw-20260407-ucrt-ubuntu-22.04-x86_64"
set -gx PROMETHEUS_ROOT (realpath "$prereq_dir/../prometheus")

# llvm-mingw must come before /usr/bin: vcpkg's mingw toolchain looks up
# x86_64-w64-mingw32-gcc/g++ on PATH, and Arch's GCC mingw-w64 also provides them.
set -gx PATH "$LLVM_MINGW/bin" $PATH

# bin/pwsh is a no-op stand-in for PowerShell (see the comment inside it).
set -gx PATH "$prereq_dir/bin" $PATH

set -gx CC x86_64-w64-mingw32-clang
set -gx CXX x86_64-w64-mingw32-clang++
set -gx RC x86_64-w64-mingw32-windres

set -gx LIBRARY_PATH "$LLVM_MINGW/lib"
set -gx C_INCLUDE_PATH "$LLVM_MINGW/include"
set -gx CMAKE_PREFIX_PATH "$LLVM_MINGW/x86_64-w64-mingw32"

set -gx VCPKG_ROOT "$PROMETHEUS_ROOT/external/vcpkg"
set -gx VCPKG_DISABLE_METRICS 1
