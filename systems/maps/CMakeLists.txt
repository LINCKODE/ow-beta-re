cmake_minimum_required(VERSION 3.25)
project(ow_maps C CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

# Map data reader/writer (docs/Maps.md §2, §7). No dependencies.
add_library(ow_maps STATIC src/mapdata.cpp src/json.cpp src/writer.cpp)
target_include_directories(ow_maps PUBLIC include)
target_compile_options(ow_maps PRIVATE -Wall -Wextra)

# C API for cgo.
add_library(ow_maps_c SHARED src/capi.cpp)
target_include_directories(ow_maps_c PUBLIC capi)
target_link_libraries(ow_maps_c PRIVATE ow_maps)
add_library(ow_maps_c_static STATIC src/capi.cpp)
target_include_directories(ow_maps_c_static PUBLIC capi)
target_link_libraries(ow_maps_c_static PUBLIC ow_maps)

# map_dump and the install tests read the install through systems/content (built there first:
# cmake --build systems/content/build) and parse STU blobs with systems/stu (compiled in, read-only use).
set(CONTENT_DIR "${CMAKE_CURRENT_SOURCE_DIR}/../content")
set(STU_DIR "${CMAKE_CURRENT_SOURCE_DIR}/../stu")
find_package(ZLIB REQUIRED)
find_package(Threads REQUIRED)
add_library(ow_content_prebuilt STATIC IMPORTED)
set_target_properties(ow_content_prebuilt PROPERTIES IMPORTED_LOCATION "${CONTENT_DIR}/build/libow_content.a"
    INTERFACE_INCLUDE_DIRECTORIES "${CONTENT_DIR}/include"
    INTERFACE_LINK_LIBRARIES "ZLIB::ZLIB;Threads::Threads")

add_executable(map_dump tools/map_dump.cpp)
target_link_libraries(map_dump PRIVATE ow_maps ow_content_prebuilt)

set(OW_INSTALL "${CMAKE_CURRENT_SOURCE_DIR}/../../game/owbetaclient" CACHE PATH "Overwatch 0.8 install")
enable_testing()
add_executable(maps_tests tests/test_main.cpp tests/test_units.cpp tests/test_install.cpp
    ${STU_DIR}/src/registry.cpp ${STU_DIR}/src/reader.cpp ${STU_DIR}/src/value.cpp)
target_link_libraries(maps_tests PRIVATE ow_maps ow_content_prebuilt)
target_include_directories(maps_tests PRIVATE src ${STU_DIR}/include ${STU_DIR}/generated)
target_compile_definitions(maps_tests PRIVATE OW_INSTALL="${OW_INSTALL}")
add_test(NAME maps_tests COMMAND maps_tests)

add_executable(maps_capi_test tests/test_capi.c)
set_target_properties(maps_capi_test PROPERTIES C_STANDARD 99 C_STANDARD_REQUIRED ON LINKER_LANGUAGE CXX)
target_link_libraries(maps_capi_test PRIVATE ow_maps_c)
add_test(NAME maps_capi_test COMMAND maps_capi_test)
