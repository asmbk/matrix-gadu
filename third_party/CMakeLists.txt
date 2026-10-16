#
# Builds the vendored libgadu (third_party/libgadu) as a static library for
# the bridge. scripts/build-libgadu.sh runs this and writes a libgadu.pc
# file, so that cgo finds the library with pkg-config.
#

cmake_minimum_required (VERSION 3.16)
project (matrix-gadu-libgadu C)

# -DENABLE_TESTS=ON builds libgadu's own tests; run them with ctest.
if (ENABLE_TESTS)
	enable_testing ()
endif ()

add_subdirectory (libgadu)
