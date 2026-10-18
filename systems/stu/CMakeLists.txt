cmake_minimum_required(VERSION 3.25)
project(ow_stu C CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

# STUD v3 reader + the client's type registry (generated/registry_data.h, from
# tools/gen_registry.py).
add_library(ow_stu STATIC src/registry.cpp src/reader.cpp src/value.cpp)
target_include_directories(ow_stu PUBLIC include PRIVATE generated)
target_compile_options(ow_stu PRIVATE -Wall -Wextra)

# C API (capi/stu.h) for cgo (go/) and the Statescript runtime: shared + static (the static archive
# links into Go binaries).
add_library(ow_stu_c SHARED src/capi.cpp)
target_include_directories(ow_stu_c PUBLIC capi)
target_link_libraries(ow_stu_c PRIVATE ow_stu)
target_compile_options(ow_stu_c PRIVATE -Wall -Wextra)
add_library(ow_stu_c_static STATIC src/capi.cpp)
target_include_directories(ow_stu_c_static PUBLIC capi)
target_link_libraries(ow_stu_c_static PRIVATE ow_stu)
target_compile_options(ow_stu_c_static PRIVATE -Wall -Wextra)

add_executable(stu_dump tools/stu_dump.cpp)
target_link_libraries(stu_dump PRIVATE ow_stu)

enable_testing()
# Fixtures are client recordings / client data, not distributed: record your own (README.md "Tests").
set(STU_HAVE_FIXTURES FALSE)
if (EXISTS ${CMAKE_CURRENT_SOURCE_DIR}/fixtures)
    set(STU_HAVE_FIXTURES TRUE)
else()
    message(STATUS "stu: no fixtures/, tests built but not registered")
endif()
add_executable(stu_tests tests/test_main.cpp tests/test_registry.cpp tests/test_reader.cpp)
target_link_libraries(stu_tests PRIVATE ow_stu)
target_compile_definitions(stu_tests PRIVATE STU_FIXTURES="${CMAKE_CURRENT_SOURCE_DIR}/fixtures")
if (STU_HAVE_FIXTURES)
    add_test(NAME stu_tests COMMAND stu_tests)
endif()

add_executable(stu_capi_tests tests/test_capi.c)
target_link_libraries(stu_capi_tests PRIVATE ow_stu_c_static ow_stu stdc++)
target_compile_options(stu_capi_tests PRIVATE -Wall -Wextra)
target_compile_definitions(stu_capi_tests PRIVATE STU_FIXTURES="${CMAKE_CURRENT_SOURCE_DIR}/fixtures")
set_target_properties(stu_capi_tests PROPERTIES C_STANDARD 99 C_STANDARD_REQUIRED ON)
if (STU_HAVE_FIXTURES)
    add_test(NAME stu_capi_tests COMMAND stu_capi_tests)
endif()
