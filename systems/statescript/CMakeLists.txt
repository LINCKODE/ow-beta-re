cmake_minimum_required(VERSION 3.25)
project(ow_statescript C CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

# The STU reader (graphs are STU assets) and the ECS core (instances live on entities, component 0x23),
# compiled from their source trees (their own CMake projects would also register their tests here).
set(SS_STU ${CMAKE_CURRENT_SOURCE_DIR}/../stu)
set(SS_ECS ${CMAKE_CURRENT_SOURCE_DIR}/../ecs)
add_library(ss_dep_stu STATIC ${SS_STU}/src/registry.cpp ${SS_STU}/src/reader.cpp ${SS_STU}/src/value.cpp)
target_include_directories(ss_dep_stu PUBLIC ${SS_STU}/include PRIVATE ${SS_STU}/generated)
add_library(ss_dep_ecs STATIC ${SS_ECS}/src/entity_admin.cpp ${SS_ECS}/src/command_frame_systems.cpp
            ${SS_ECS}/src/manifest.cpp)
target_include_directories(ss_dep_ecs PUBLIC ${SS_ECS}/include PRIVATE ${SS_ECS}/generated)

# Core runtime (C++ API).
add_library(ow_statescript STATIC
    src/value.cpp
    src/graph.cpp
    src/vars.cpp
    src/config_vars.cpp
    src/runtime.cpp
    src/states.cpp
    src/states_hero.cpp
    src/nodes.cpp
    src/capabilities.cpp
    src/states_match.cpp
    src/states_host.cpp
    src/nodes_match.cpp
    src/capabilities_match.cpp
    src/match_world.cpp)
target_include_directories(ow_statescript PUBLIC include PRIVATE generated)
target_link_libraries(ow_statescript PUBLIC ss_dep_stu)
target_compile_options(ow_statescript PRIVATE -Wall -Wextra -Wno-missing-field-initializers)

# ECS bridge: component 0x23 owning a sync manager.
add_library(ow_statescript_ecs STATIC src/ecs_component.cpp)
target_link_libraries(ow_statescript_ecs PUBLIC ow_statescript ss_dep_ecs)
target_compile_options(ow_statescript_ecs PRIVATE -Wall -Wextra -Wno-missing-field-initializers)

# C API for cgo / ctypes: shared + static.
add_library(ow_statescript_c SHARED src/capi.cpp)
target_include_directories(ow_statescript_c PUBLIC capi)
target_link_libraries(ow_statescript_c PRIVATE ow_statescript_ecs)
target_compile_options(ow_statescript_c PRIVATE -Wall -Wextra -Wno-missing-field-initializers)
add_library(ow_statescript_c_static STATIC src/capi.cpp)
target_include_directories(ow_statescript_c_static PUBLIC capi)
target_link_libraries(ow_statescript_c_static PRIVATE ow_statescript_ecs)
target_compile_options(ow_statescript_c_static PRIVATE -Wall -Wextra -Wno-missing-field-initializers)

enable_testing()
set(SS_DATA ${CMAKE_CURRENT_SOURCE_DIR}/../data/live_anubis/graphs)
set(SS_FIXTURES ${CMAKE_CURRENT_SOURCE_DIR}/fixtures)
# Fixtures are client recordings / client data, not distributed: record your own (README.md "Tests").
set(SS_HAVE_FIXTURES FALSE)
if (EXISTS ${SS_FIXTURES} AND EXISTS ${SS_DATA})
    set(SS_HAVE_FIXTURES TRUE)
else()
    message(STATUS "statescript: no fixtures/ or data/live_anubis/graphs, tests built but not registered")
endif()

add_executable(statescript_tests tests/test_main.cpp tests/test_runtime.cpp tests/test_replay.cpp tests/test_hero.cpp tests/test_nodes.cpp tests/test_match.cpp tests/test_host.cpp)
target_link_libraries(statescript_tests PRIVATE ow_statescript_ecs)
target_compile_definitions(statescript_tests PRIVATE SS_DATA="${SS_DATA}" SS_FIXTURES="${SS_FIXTURES}")
if (SS_HAVE_FIXTURES)
    add_test(NAME statescript_tests COMMAND statescript_tests)
endif()

add_executable(statescript_capi_tests tests/test_capi.c)
target_link_libraries(statescript_capi_tests PRIVATE ow_statescript_c_static ow_statescript_ecs stdc++)
target_compile_definitions(statescript_capi_tests PRIVATE SS_DATA="${SS_DATA}")
set_target_properties(statescript_capi_tests PROPERTIES C_STANDARD 99 C_STANDARD_REQUIRED ON LINKER_LANGUAGE CXX)
if (SS_HAVE_FIXTURES)
    add_test(NAME statescript_capi_tests COMMAND statescript_capi_tests)
endif()

add_executable(ss_scan tools/ss_scan.cpp)
target_link_libraries(ss_scan PRIVATE ow_statescript)

add_executable(ss_census tools/ss_census.cpp)
target_link_libraries(ss_census PRIVATE ow_statescript)

add_executable(statescript_capi_match_tests tests/test_capi_match.c)
target_link_libraries(statescript_capi_match_tests PRIVATE ow_statescript_c_static ow_statescript_ecs stdc++)
target_compile_definitions(statescript_capi_match_tests PRIVATE SS_FIXTURES="${SS_FIXTURES}")
set_target_properties(statescript_capi_match_tests PROPERTIES C_STANDARD 99 C_STANDARD_REQUIRED ON LINKER_LANGUAGE CXX)
if (SS_HAVE_FIXTURES)
    add_test(NAME statescript_capi_match_tests COMMAND statescript_capi_match_tests)
endif()
