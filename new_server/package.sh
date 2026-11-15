#!/usr/bin/env bash
# 打出可直接分发的服务器发布包（二进制 + 配置模板 + 部署/安装脚本 + 静态资源）。
#
# 构建前先打包共享 Rust 核心 sys_core（与 build.sh / deploy.sh 同一链路），
# 再用其静态库链接 Go 二进制并把核心归档 SHA256 写进链接参数，保证发布包与最新 Rust 核心一致。
#
# 用法：  bash package.sh [输出目录]
#         bash package.sh --no-sync_core [输出目录]   # 跳过 Rust 核心重建（核心未改时省时间）
# 产物：  <输出目录>/syc-file-<version>-<shortsha>.tar.gz
# 部署：  解包后 `sudo bash install-systemd.sh [目录]` 首次安装；
#         日常升级：源码环境下用 new_server/deploy.sh（原子替换 + 健康检查 + 回滚）。
set -euo pipefail

export PATH="$HOME/.cargo/bin:/usr/local/go/bin:$PATH"
cd "$(dirname "$0")"

OUT_ARG=""
FILECORE=1
while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-sync_core|--no-filecore) FILECORE=0 ;;
        -h|--help) sed -n '2,13p' "$0"; exit 0 ;;
        --*) echo "未知参数: $1（--help 查看用法）" >&2; exit 2 ;;
        *) OUT_ARG="$1" ;;
    esac
    shift
done
OUT=${OUT_ARG:-/mnt/data/release}
mkdir -p "$OUT"

VERSION=$(awk -F'"' '/^const Version/{print $2}' config/config.go 2>/dev/null || true)
[ -n "${VERSION:-}" ] || VERSION=1.0.0
SHA=$(git -C .. rev-parse --short HEAD 2>/dev/null || echo nogit)
NAME="syc-file-${VERSION}-${SHA}"

STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT

if [ "$FILECORE" = 1 ]; then
    echo "==> 打包共享 Rust 核心 sys_core/lib/libsys_core.a（构建前必备）"
    bash ../sys_core/build.sh
fi
CORE_SHA=$(sha256sum ../sys_core/lib/libsys_core.a | cut -d' ' -f1)
echo "==> 核心归档 SHA256=$CORE_SHA"

echo "==> 构建二进制（输出到打包目录，不覆盖正在运行的 syc-file）"
CGO_ENABLED=1 CC=gcc go build -trimpath \
    -ldflags "-X syc-file/pkg/synccore.CoreArchiveSHA256=$CORE_SHA" \
    -o "$STAGE/syc-file" ./cmd

tar --exclude='*.a' -cf - deploy.sh install-systemd.sh config/config.example.yaml static sql \
  | tar -xf - -C "$STAGE"
[ -f deploy/vpn-server.sh ] && install -m 755 deploy/vpn-server.sh "$STAGE/vpn-server.sh" || true

PKG="$OUT/${NAME}.tar.gz"
tar -czf "$PKG" -C "$STAGE" .
echo "==> 已打包：$PKG"
# 用 sed 而不是 head：set -o pipefail 下 head 读够 12 行就关管道，tar 会收到 SIGPIPE
# 使脚本在打包成功后仍以 141 退出（曾导致 CI/部署判定失败）。
tar tzf "$PKG" | sed -n '1,12p'
