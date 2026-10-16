#!/bin/sh
# Source this file to build against the vendored libgadu that
# scripts/build-libgadu.sh built into .deps/libgadu.
root="$(cd "$(dirname "$0")/.." 2>/dev/null && pwd)/.deps/libgadu"
[ -d "$root" ] || root="$PWD/.deps/libgadu"
export PKG_CONFIG_PATH="$root/lib/pkgconfig${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
