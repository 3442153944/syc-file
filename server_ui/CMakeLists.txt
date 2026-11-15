cmake_minimum_required(VERSION 3.21)

project(server_ui VERSION 0.1.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_AUTOMOC ON)
set(CMAKE_AUTORCC ON)

find_package(Qt6 REQUIRED COMPONENTS Core Gui Widgets Network)
qt_standard_project_setup()

# Windows：应用图标 + 版本信息（图标同时用于 exe 资源与 Qt 资源）。
# 只在 Windows 上生成/编译 .rc；Linux 没有 exe 资源，图标走 resources.qrc。
set(SERVER_UI_PLATFORM_SOURCES)
if(WIN32)
    set(APP_ICON_ICO "${CMAKE_CURRENT_SOURCE_DIR}/resources/appicon.ico")
    configure_file("${CMAKE_CURRENT_SOURCE_DIR}/resources/app.rc.in" "${CMAKE_CURRENT_BINARY_DIR}/app.rc" @ONLY)
    list(APPEND SERVER_UI_PLATFORM_SOURCES "${CMAKE_CURRENT_BINARY_DIR}/app.rc")
endif()

# 可复用的界面源码（不含 main）：主程序与离屏截图工具共用，避免两套代码。
set(SERVER_UI_SOURCES
    resources/resources.qrc
    src/backendclient.cpp
    src/backendclient.h
    src/configfile.cpp
    src/configfile.h
    src/configpage.cpp
    src/configpage.h
    src/icons.cpp
    src/icons.h
    src/ipcclient.cpp
    src/ipcclient.h
    src/logline.cpp
    src/logline.h
    src/logpage.cpp
    src/logpage.h
    src/mainwindow.cpp
    src/mainwindow.h
    src/overviewpage.cpp
    src/overviewpage.h
    src/theme.cpp
    src/theme.h
    src/widgets.cpp
    src/widgets.h
)

# WIN32 = 不弹控制台窗口（/SUBSYSTEM:WINDOWS），和之前 Go 版 -H=windowsgui 等价。
qt_add_executable(server_ui WIN32
    ${SERVER_UI_PLATFORM_SOURCES}
    src/main.cpp
    ${SERVER_UI_SOURCES}
)

target_link_libraries(server_ui PRIVATE Qt6::Widgets Qt6::Network)
target_include_directories(server_ui PRIVATE src)

# 产物名保持 syc-file-gui(.exe)：开始菜单快捷方式、HKCU Run、后端 desktoplaunch 都指向这个名字。
set_target_properties(server_ui PROPERTIES
    OUTPUT_NAME "syc-file-gui"
    WIN32_EXECUTABLE TRUE
)

# 开发自检工具：离屏渲染页面为 PNG（不进安装包）。用法见 tests/snapshot.cpp。
qt_add_executable(server_ui_snapshot
    tests/snapshot.cpp
    ${SERVER_UI_SOURCES}
)
target_link_libraries(server_ui_snapshot PRIVATE Qt6::Widgets Qt6::Network)
target_include_directories(server_ui_snapshot PRIVATE src)
set_target_properties(server_ui_snapshot PROPERTIES WIN32_EXECUTABLE FALSE)

if(MSVC)
    target_compile_options(server_ui PRIVATE /utf-8)
    target_compile_options(server_ui_snapshot PRIVATE /utf-8)
endif()