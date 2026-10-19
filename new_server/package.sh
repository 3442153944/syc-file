#!/usr/bin/env bash
# 打出可直接分发的服务器发布包（二进制 + 配置模板 + 部署/安装脚本 + 静态资源）。
#
# 用法：  bash package.sh [输出目录]
# 产物：  <输出目录>/syc-file-<version>-<shortsha>.tar.gz
# 部署：  解包后 `sudo bash install-systemd.sh [目录]` 首次安装；
#         日常升级用包内 deploy.sh（原子替换 + 健康检查 + 回滚）。
set -euo pipefail

export PATH="$HOME/.cargo/bin:/usr/local/go/bin:$PATH"
cd "$(dirname "$0")"

OUT=${1:-/mnt/data/release}
mkdir -p "$OUT"

VERSION=$(awk -F'"' '/^const Version/{print $2}' config/config.go 2>/dev/null || true)
[ -n "${VERSION:-}" ] || VERSION=1.0.0
SHA=$(git -C .. rev-parse --short HEAD 2>/dev/null || echo nogit)
NAME="syc-file-${VERSION}-${SHA}"

STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT

echo "==> 构建二进制（输出到打包目录，不覆盖正在运行的 syc-file）"
CGO_ENABLED=1 CC=gcc go build -o "$STAGE/syc-file" ./cmd

tar --exclude='*.a' -cf - deploy.sh install-systemd.sh config/config.example.yaml static sql \
  | tar -xf - -C "$STAGE"
[ -f deploy/vpn-server.sh ] && install -m 755 deploy/vpn-server.sh "$STAGE/vpn-server.sh" || true

PKG="$OUT/${NAME}.tar.gz"
tar -czf "$PKG" -C "$STAGE" .
echo "==> 已打包：$PKG"
tar tzf "$PKG" | head -12
