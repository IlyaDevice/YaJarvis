# Cross-build bundled Opus with the same Zig wrapper as ring and Lua.
# CMAKE_TOOLCHAIN_FILE_aarch64_unknown_linux_musl=<absolute path to this file>
# CMAKE_GENERATOR_aarch64_unknown_linux_musl=Ninja
set(CMAKE_SYSTEM_NAME Linux)
set(CMAKE_SYSTEM_PROCESSOR aarch64)
set(CMAKE_TRY_COMPILE_TARGET_TYPE STATIC_LIBRARY)
if(NOT DEFINED ENV{CC_aarch64_unknown_linux_musl})
  message(FATAL_ERROR "Set CC_aarch64_unknown_linux_musl to the Zig C wrapper")
endif()
set(CMAKE_C_COMPILER "$ENV{CC_aarch64_unknown_linux_musl}")
set(CMAKE_CXX_COMPILER "$ENV{CC_aarch64_unknown_linux_musl}")
set(CMAKE_ASM_COMPILER "$ENV{CC_aarch64_unknown_linux_musl}")
set(CMAKE_AR "$ENV{AR_aarch64_unknown_linux_musl}")
set(CMAKE_RANLIB "" CACHE FILEPATH "Archive index is written by Zig ar")
