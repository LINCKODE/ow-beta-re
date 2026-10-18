cmake_minimum_required(VERSION 3.25)
project(ow_ecs CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

# Core library (C++ API).
add_library(ow_ecs STATIC
    src/entity_admin.cpp
    src/command_frame_systems.cpp
    src/manifest.cpp)
target_include_directories(ow_ecs PUBLIC include PRIVATE generated)
target_compile_options(ow_ecs PRIVATE -Wall -Wextra)

# C API for cgo / ctypes.
add_library(ow_ecs_c SHARED src/capi.cpp)
target_include_directories(ow_ecs_c PUBLIC capi)
target_link_libraries(ow_ecs_c PRIVATE ow_ecs)
target_compile_options(ow_ecs_c PRIVATE -Wall -Wextra -Wno-missing-field-initializers)

# Static C API archive for cgo (links into the Go binary).
add_library(ow_ecs_c_static STATIC src/capi.cpp)
target_include_directories(ow_ecs_c_static PUBLIC capi)
target_link_libraries(ow_ecs_c_static PRIVATE ow_ecs)

enable_testing()
add_executable(ecs_tests tests/test_main.cpp tests/test_lifecycle.cpp tests/test_command_frames.cpp)
target_link_libraries(ecs_tests PRIVATE ow_ecs)
target_include_directories(ecs_tests PRIVATE generated)
add_test(NAME ecs_tests COMMAND ecs_tests)

find_package(Python3 COMPONENTS Interpreter)
# Fixtures are client recordings / client data, not distributed: record your own (README.md "Tests").
if (Python3_FOUND AND EXISTS ${CMAKE_CURRENT_SOURCE_DIR}/fixtures)
    add_test(NAME replay_fixtures
             COMMAND ${Python3_EXECUTABLE} ${CMAKE_CURRENT_SOURCE_DIR}/tests/replay_fixture.py
                     $<TARGET_FILE:ow_ecs_c> ${CMAKE_CURRENT_SOURCE_DIR}/fixtures)
endif()
