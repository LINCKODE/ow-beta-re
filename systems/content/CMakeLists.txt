cmake_minimum_required(VERSION 3.25)
project(ow_content C CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

find_package(ZLIB REQUIRED)
find_package(Threads REQUIRED)

# content::Store + the default 0.8 local-install backend (own CASC/TACT/APM code; zlib for inflate).
add_library(ow_content STATIC src/crypto.cpp src/casc.cpp src/tact.cpp src/apm.cpp src/store.cpp)
target_include_directories(ow_content PUBLIC include)
target_link_libraries(ow_content PUBLIC ZLIB::ZLIB Threads::Threads)
target_compile_options(ow_content PRIVATE -Wall -Wextra)

# C API for cgo / ctypes.
add_library(ow_content_c SHARED src/capi.cpp)
target_include_directories(ow_content_c PUBLIC capi)
target_link_libraries(ow_content_c PRIVATE ow_content)
target_compile_options(ow_content_c PRIVATE -Wall -Wextra)
add_library(ow_content_c_static STATIC src/capi.cpp)
target_include_directories(ow_content_c_static PUBLIC capi)
target_link_libraries(ow_content_c_static PUBLIC ow_content)

add_executable(content_dump tools/content_dump.cpp)
target_link_libraries(content_dump PRIVATE ow_content)

# Tests: unit tests on synthetic data always; the install tests read game/owbetaclient (skipped if absent).
set(OW_INSTALL "${CMAKE_CURRENT_SOURCE_DIR}/../../game/owbetaclient" CACHE PATH "Overwatch 0.8 install")
set(STU_FIXTURES "${CMAKE_CURRENT_SOURCE_DIR}/../stu/fixtures")
enable_testing()
# The install tests parse the STU blobs they read with the STU reader (systems/stu, compiled in; read-only use).
set(STU_DIR "${CMAKE_CURRENT_SOURCE_DIR}/../stu")
add_executable(content_tests tests/test_main.cpp tests/test_units.cpp tests/test_install.cpp
    ${STU_DIR}/src/registry.cpp ${STU_DIR}/src/reader.cpp ${STU_DIR}/src/value.cpp)
target_link_libraries(content_tests PRIVATE ow_content)
target_include_directories(content_tests PRIVATE src ${STU_DIR}/include ${STU_DIR}/generated)
target_compile_definitions(content_tests PRIVATE OW_INSTALL="${OW_INSTALL}" STU_FIXTURES="${STU_FIXTURES}")
add_test(NAME content_tests COMMAND content_tests)

add_executable(content_capi_test tests/test_capi.c)
set_target_properties(content_capi_test PROPERTIES C_STANDARD 99 C_STANDARD_REQUIRED ON LINKER_LANGUAGE CXX)
target_link_libraries(content_capi_test PRIVATE ow_content_c)
target_compile_definitions(content_capi_test PRIVATE OW_INSTALL="${OW_INSTALL}")
add_test(NAME content_capi_test COMMAND content_capi_test)
