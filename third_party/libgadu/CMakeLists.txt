#
# Vendored libgadu (https://github.com/wojtekka/libgadu, commit 9eb2aa0), LGPL 2.1 - see COPYING.
# Upstream is archived; this copy is maintained here. Bundles the protobuf-c 1.5.2 runtime
# (BSD-2 - see COPYING.protobuf-c); packets.pb-c.{c,h} are generated from packets.proto by
# protoc-c 1.5.2 and committed. Built static, linked into gadu_protocol only.
#

include (CheckSymbolExists)

find_package (GnuTLS 3.0 REQUIRED)
find_package (ZLIB REQUIRED)
if (NOT WIN32)
	find_package (Threads REQUIRED)
endif ()

set (GG_LIBGADU_VERSION "1.12.2")

set (GG_CONFIG_HAVE_C99_VSNPRINTF ON)
set (GG_CONFIG_HAVE_VA_COPY ON)
set (GG_CONFIG_HAVE_LONG_LONG ON)
set (GG_CONFIG_HAVE_UINT64_T ON)
set (GG_CONFIG_HAVE_STDINT_H ON)
set (GG_CONFIG_HAVE_GNUTLS ON)
set (GG_CONFIG_HAVE_ZLIB ON)
set (GG_CONFIG_IS_GPL_COMPLIANT ON)
if (CMAKE_C_BYTE_ORDER STREQUAL "BIG_ENDIAN")
	set (GG_CONFIG_BIGENDIAN ON)
endif ()
if (NOT WIN32)
	set (GG_CONFIG_HAVE_PTHREAD ON)
	check_symbol_exists (fork "unistd.h" GG_CONFIG_HAVE_FORK)
	check_symbol_exists (gethostbyname_r "netdb.h" GG_CONFIG_HAVE_GETHOSTBYNAME_R)
endif ()

check_symbol_exists (strtoull "stdlib.h" HAVE_STRTOULL)
check_symbol_exists (_strtoui64 "stdlib.h" HAVE__STRTOUI64)
check_symbol_exists (_vscprintf "stdio.h" HAVE__VSCPRINTF)
check_symbol_exists (_exit "unistd.h" HAVE__EXIT)
check_symbol_exists (mkstemp "stdlib.h" HAVE_MKSTEMP)

configure_file (include/libgadu.h.in "${CMAKE_CURRENT_BINARY_DIR}/include/libgadu.h" @ONLY)
configure_file (include/config.h.in "${CMAKE_CURRENT_BINARY_DIR}/private/config.h")

add_library (libgadu STATIC
	src/common.c
	src/dcc.c
	src/dcc7.c
	src/debug.c
	src/deflate.c
	src/encoding.c
	src/endian.c
	src/events.c
	src/handlers.c
	src/http.c
	src/libgadu.c
	src/message.c
	src/network.c
	src/obsolete.c
	src/packets.pb-c.c
	src/protobuf-c.c
	src/protobuf.c
	src/pubdir.c
	src/pubdir50.c
	src/resolver.c
	src/sha1.c
	src/tvbuff.c
	src/tvbuilder.c
)

set_target_properties (libgadu PROPERTIES
	OUTPUT_NAME gadu
	POSITION_INDEPENDENT_CODE ON
	C_VISIBILITY_PRESET hidden
)

if (GG_CONFIG_BIGENDIAN)
	target_compile_definitions (libgadu PRIVATE WORDS_BIGENDIAN)
endif ()
target_compile_definitions (libgadu PRIVATE GG_IGNORE_DEPRECATED)

set (LIBGADU_INCLUDE_DIRS
	"${CMAKE_CURRENT_BINARY_DIR}/include" "${CMAKE_CURRENT_SOURCE_DIR}/include" "${CMAKE_CURRENT_BINARY_DIR}/private"
)

target_include_directories (libgadu
	PUBLIC "${CMAKE_CURRENT_BINARY_DIR}/include"
	PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}/include" "${CMAKE_CURRENT_BINARY_DIR}/private"
)

target_link_libraries (libgadu PRIVATE GnuTLS::GnuTLS ZLIB::ZLIB)
if (WIN32)
	target_link_libraries (libgadu PUBLIC ws2_32)
else ()
	target_link_libraries (libgadu PRIVATE Threads::Threads)
endif ()

if (ENABLE_TESTS)
	add_subdirectory (test)
endif ()
