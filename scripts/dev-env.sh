#!/bin/sh
# Source this file to build against a libgadu that is unpacked into .deps/root
# (see README, "Build without root access").
root="$(cd "$(dirname "$0")/.." 2>/dev/null && pwd)/.deps/root"
[ -d "$root" ] || root="$PWD/.deps/root"
libdir="$root/usr/lib/$(gcc -dumpmachine)"
export PKG_CONFIG_PATH="$libdir/pkgconfig${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
export PKG_CONFIG_SYSROOT_DIR="$root"
export LD_LIBRARY_PATH="$libdir${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
