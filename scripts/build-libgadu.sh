#!/bin/sh
# Builds the vendored libgadu (third_party/libgadu) into a static library and
# writes a pkg-config file for it. Needs cmake, a C compiler, and the GnuTLS
# and zlib development files.
#
# Usage: scripts/build-libgadu.sh [prefix]
# The default prefix is .deps/libgadu. Then build the bridge with
#   PKG_CONFIG_PATH=<prefix>/lib/pkgconfig go build -tags goolm ./cmd/...
# or source scripts/dev-env.sh.
set -eu

src="$(cd "$(dirname "$0")/.." && pwd)"
prefix="${1:-$src/.deps/libgadu}"
build="$prefix/build"

cmake -S "$src/third_party" -B "$build" -DCMAKE_BUILD_TYPE=Release
cmake --build "$build" --parallel

mkdir -p "$prefix/lib/pkgconfig" "$prefix/include"
cp "$build/libgadu/libgadu.a" "$prefix/lib/"
cp "$build/libgadu/include/libgadu.h" "$prefix/include/"

# cgo does not pass --static to pkg-config, so the GnuTLS and zlib flags go
# into Libs and not into Libs.private.
cat > "$prefix/lib/pkgconfig/libgadu.pc" <<EOF
prefix=$prefix
libdir=\${prefix}/lib
includedir=\${prefix}/include

Name: libgadu
Description: Gadu-Gadu protocol library (vendored copy, third_party/libgadu)
Version: 1.12.2
Requires: gnutls zlib
Cflags: -I\${includedir}
Libs: -L\${libdir} -lgadu -lpthread
EOF

echo "libgadu built in $prefix"
