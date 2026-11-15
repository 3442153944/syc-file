cmake_minimum_required(VERSION 3.5.0)
project(filecore)

# Compile the HarmonyOS NAPI wrapper and link the shared sys_core static library
# built by ../../build.ps1 for each OHOS target. The archive retains the legacy
# libfilecore.a filename so existing package and NAPI loading names stay stable.
# It exposes the C ABI declared in filecore.h.
#
# Output: libfilecore.so, loadable from ArkTS via `import filecore from 'libfilecore.so'`.

set(CMAKE_CXX_STANDARD 17)

include_directories(${CMAKE_CURRENT_SOURCE_DIR})

# OHOS ABI -> prebuilts subdirectory. OHOS_ARCH is set by the build system per target.
if(NOT DEFINED OHOS_ARCH)
    # Fall back to cmake system processor detection if OHOS_ARCH not provided.
    set(OHOS_ARCH ${CMAKE_SYSTEM_PROCESSOR})
    if(OHOS_ARCH STREQUAL "aarch64")
        set(OHOS_ARCH "arm64-v8a")
    elseif(OHOS_ARCH STREQUAL "arm")
        set(OHOS_ARCH "armeabi-v7a")
    elseif(OHOS_ARCH STREQUAL "x86_64" OR OHOS_ARCH STREQUAL "x64")
        set(OHOS_ARCH "x86_64")
    endif()
endif()

set(PREBUILT_DIR ${CMAKE_CURRENT_SOURCE_DIR}/prebuilts/${OHOS_ARCH})
set(PREBUILT_LIB ${PREBUILT_DIR}/libfilecore.a)

if(NOT EXISTS ${PREBUILT_LIB})
    message(FATAL_ERROR
        "Prebuilt libfilecore.a not found at ${PREBUILT_LIB}. "
        "Run ../../build.ps1 (from harmony/products/sunyuanling) to build the Rust static lib for OHOS targets.")
endif()

message(STATUS "Using prebuilt libfilecore.a: ${PREBUILT_LIB}")

add_library(filecore SHARED
    napi_init.cpp
)

# Link against the Rust static lib + the OHOS NAPI runtime (ace_napi).
# ace_napi.z.so is provided by the OHOS native sysroot; the OHOS build system
# automatically links the sysroot libraries when building for OHOS targets.
target_link_libraries(filecore
    ${PREBUILT_LIB}
    ace_napi.z
)
