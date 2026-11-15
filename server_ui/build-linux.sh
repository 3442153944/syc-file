#!/usr/bin/env bash
# 构建「私有云服务器设置」桌面控制台（Linux）。
#
# 依赖：cmake >= 3.21、g++（C++20）、Qt6 开发包（Core Gui Widgets Network）、可选 ninja。
# 产物：build/syc-file-gui（无 .exe；运行时用系统 Qt6）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_DIR="$ROOT/build"
JOBS="$(command -v nproc >/dev/null 2>&1 && nproc || echo 2)"
PREFIX=""
CLEAN=0

usage() {
    cat <<'EOF'
用法: ./build-linux.sh [选项]

  --build-dir <dir>   构建目录（默认: ./build）
  --jobs <n>          并行编译任务数（默认: nproc）
  --clean             构建前删除构建目录
  --prefix <dir>      构建后把 syc-file-gui 安装到该目录
  -h, --help          显示本帮助

环境变量:
  QT_PREFIX           Qt6 安装前缀；不设时依次尝试 qtpaths6 / qtpaths / pkg-config，
                      都找不到就交给 CMake 自行搜索。

示例:
  ./build-linux.sh --clean --jobs 8
  QT_PREFIX=/opt/Qt/6.9.3/gcc_64 ./build-linux.sh --prefix /opt/syc-file-gui
EOF
}

log() { printf '\n==> %s\n' "$1"; }
die() { printf '错误: %s\n' "$1" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --build-dir) [[ $# -ge 2 ]] || die "--build-dir 缺少参数"; BUILD_DIR="$2"; shift 2 ;;
        --jobs|-j)   [[ $# -ge 2 ]] || die "--jobs 缺少参数"; JOBS="$2"; shift 2 ;;
        --prefix)    [[ $# -ge 2 ]] || die "--prefix 缺少参数"; PREFIX="$2"; shift 2 ;;
        --clean)     CLEAN=1; shift ;;
        -h|--help)   usage; exit 0 ;;
        *)           usage; die "未知参数: $1" ;;
    esac
done

command -v cmake >/dev/null 2>&1 || die "找不到 cmake（需要 3.21+）"
command -v g++ >/dev/null 2>&1 || die "找不到 g++（需要 C++20）"

QT_PREFIX="${QT_PREFIX:-}"
if [[ -z "$QT_PREFIX" ]]; then
    if command -v qtpaths6 >/dev/null 2>&1; then
        QT_PREFIX="$(qtpaths6 --query QT_INSTALL_PREFIX)"
    elif command -v qtpaths >/dev/null 2>&1; then
        QT_PREFIX="$(qtpaths --query QT_INSTALL_PREFIX)"
    elif command -v pkg-config >/dev/null 2>&1 && pkg-config --exists Qt6Core; then
        QT_PREFIX="$(pkg-config --variable=prefix Qt6Core)"
    fi
fi

if [[ "$CLEAN" -eq 1 && -d "$BUILD_DIR" ]]; then
    resolved="$(cd "$BUILD_DIR" && pwd)"
    case "$resolved" in
        "$ROOT"/*) rm -rf "$BUILD_DIR" ;;
        *) die "拒绝清理项目目录之外的路径: $resolved" ;;
    esac
fi

GEN=()
if command -v ninja >/dev/null 2>&1; then
    GEN=(-G Ninja)
fi

CMAKE_ARGS=(-S "$ROOT" -B "$BUILD_DIR" -DCMAKE_BUILD_TYPE=Release)
if [[ ${#GEN[@]} -gt 0 ]]; then
    CMAKE_ARGS+=("${GEN[@]}")
fi
if [[ -n "$QT_PREFIX" ]]; then
    if [[ -f "$QT_PREFIX/lib/cmake/Qt6/Qt6Config.cmake" || -f "$QT_PREFIX/lib64/cmake/Qt6/Qt6Config.cmake" ]]; then
        log "Qt6: $QT_PREFIX"
    else
        log "警告: 未在 $QT_PREFIX 找到 Qt6Config.cmake，仍交给 CMake 解析"
    fi
    CMAKE_ARGS+=("-DCMAKE_PREFIX_PATH=$QT_PREFIX")
else
    log "未显式定位 Qt6，交给 CMake 查找（可设 QT_PREFIX=...）"
fi

log "CMake 配置（${GEN[*]:-默认生成器}）"
cmake "${CMAKE_ARGS[@]}"

log "编译（并行 $JOBS）"
cmake --build "$BUILD_DIR" --parallel "$JOBS"

EXE="$BUILD_DIR/syc-file-gui"
[[ -x "$EXE" ]] || die "构建完成但找不到可执行文件: $EXE"

if [[ -n "$PREFIX" ]]; then
    mkdir -p "$PREFIX"
    cp "$EXE" "$PREFIX/"
    log "已安装到 $PREFIX/syc-file-gui（运行库使用系统 Qt6）"
fi

printf '\n完成\n  可执行文件: %s\n' "$EXE"
printf '  运行      : %s --config <config.yaml>\n' "$EXE"
printf '  默认配置  : <程序目录>/config/config.yaml\n'
printf '  通信      : 本机 Unix socket + runtime.json，无需端口\n'