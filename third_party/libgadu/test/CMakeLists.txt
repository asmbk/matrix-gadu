#
# libgadu's automatic tests (upstream test/automatic). Some compile library sources directly to reach
# internals; the rest link the static library and override libc functions (connect, gethostbyname,
# send, recv) to simulate servers. Exit code 77 means "skipped", as under automake.
#

include (CheckFunctionExists)

find_package (LibXml2)
find_package (Perl)

set (LIBGADU_SRC_DIR "${CMAKE_CURRENT_SOURCE_DIR}/../src")

function (libgadu_add_test name)
	cmake_parse_arguments (ARG "" "" "SOURCES;LIBRARIES" ${ARGN})

	set (test_name "libgadu-${name}")

	add_executable (${test_name} ${ARG_SOURCES})
	target_include_directories (${test_name} PRIVATE
		"${CMAKE_CURRENT_SOURCE_DIR}" "${LIBGADU_INCLUDE_DIRS}"
	)
	target_compile_definitions (${test_name} PRIVATE GG_IGNORE_DEPRECATED)
	target_link_libraries (${test_name} PRIVATE ${ARG_LIBRARIES})

	add_test (NAME ${test_name} COMMAND ${test_name})
	set_tests_properties (${test_name} PROPERTIES
		SKIP_RETURN_CODE 77
		TIMEOUT 120
		ENVIRONMENT "srcdir=${CMAKE_CURRENT_SOURCE_DIR}"
	)
endfunction ()

libgadu_add_test (base64 SOURCES base64.c LIBRARIES libgadu)
libgadu_add_test (convert SOURCES convert.c "${LIBGADU_SRC_DIR}/encoding.c")
libgadu_add_test (deflate SOURCES deflate.c LIBRARIES libgadu)
libgadu_add_test (endian1 SOURCES endian1.c "${LIBGADU_SRC_DIR}/endian.c")
libgadu_add_test (hash
	SOURCES hash.c "${LIBGADU_SRC_DIR}/sha1.c" "${LIBGADU_SRC_DIR}/endian.c"
	LIBRARIES GnuTLS::GnuTLS
)
libgadu_add_test (message1 SOURCES message1.c)
libgadu_add_test (packet SOURCES packet.c LIBRARIES libgadu)
libgadu_add_test (resolver SOURCES resolver.c LIBRARIES libgadu)

if (LibXml2_FOUND)
	libgadu_add_test (message2
		SOURCES message2.c "${LIBGADU_SRC_DIR}/message.c"
		LIBRARIES LibXml2::LibXml2
	)
endif ()

if (PERL_FOUND)
	file (GLOB _scripts "${CMAKE_CURRENT_SOURCE_DIR}/script/*.scr")
	add_custom_command (
		OUTPUT "${CMAKE_CURRENT_BINARY_DIR}/script.c"
		COMMAND "${PERL_EXECUTABLE}" "${CMAKE_CURRENT_SOURCE_DIR}/script/compile" ${_scripts}
			> "${CMAKE_CURRENT_BINARY_DIR}/script.c"
		DEPENDS "${CMAKE_CURRENT_SOURCE_DIR}/script/compile" ${_scripts}
	)
	libgadu_add_test (protocol
		SOURCES protocol.c "${CMAKE_CURRENT_BINARY_DIR}/script.c" "${LIBGADU_SRC_DIR}/network.c"
		LIBRARIES libgadu
	)
	target_include_directories (libgadu-protocol PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}/script")
endif ()

# overrides connect() and forwards to glibc's __connect
check_function_exists (__connect HAVE___CONNECT)
if (HAVE___CONNECT)
	find_package (Threads REQUIRED)
	libgadu_add_test (connect
		SOURCES connect.c "${LIBGADU_SRC_DIR}/network.c"
		LIBRARIES libgadu GnuTLS::GnuTLS Threads::Threads
	)
endif ()

if (NOT WIN32)
	find_package (Threads REQUIRED)
	libgadu_add_test (tls-send SOURCES tls-send.c LIBRARIES libgadu GnuTLS::GnuTLS Threads::Threads)
endif ()
