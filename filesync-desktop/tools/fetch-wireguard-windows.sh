#!/usr/bin/env bash
# 取 WireGuard for Windows 的 wireguard.exe，放进 src-tauri/resources/wireguard/。
# 需要 msitools（Debian/Ubuntu: apt install msitools）。
#
# 用法：  bash tools/fetch-wireguard-windows.sh [MSI_URL]
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION=1.1.1
URL=${1:-https://download.wireguard.com/windows-client/wireguard-amd64-${VERSION}.msi}
# 固定版本的校验值：下载内容变了就拒绝，避免“同版本不同二进制”
MSI_SHA256=7bfed60ad61b785c914b38b61555a975488e1d3ec472dbfb2fcdf498fca75242
EXE_SHA256=04c71fb1a555a6ace40d3ae9113c90c4829d7ca1d6c903fd20a8d41a0e2bef94
OUT=src-tauri/resources/wireguard/wireguard.exe

[ -f "$OUT" ] && [ "${1:-}" != "--force" ] && {
  echo "已存在 $OUT（核对 sha256）"
  sha256sum -c <<<"$EXE_SHA256  $OUT" && exit 0 || echo "校验不符，重新下载" >&2
}
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

command -v msiextract >/dev/null || { echo "缺少 msiextract（apt install msitools）" >&2; exit 1; }
echo "==> 下载 $URL"
https_proxy=${https_proxy:-} http_proxy=${http_proxy:-} curl -fL --retry 3 -o "$TMP/wg.msi" "$URL"
echo "$MSI_SHA256  $TMP/wg.msi" | sha256sum -c -
echo "==> 解包"
msiextract -C "$TMP/out" "$TMP/wg.msi" >/dev/null
EXE=$(find "$TMP/out" -iname 'wireguard.exe' | head -1)
[ -n "$EXE" ] || { echo "MSI 里没找到 wireguard.exe" >&2; exit 1; }
mkdir -p src-tauri/resources/wireguard
cp "$EXE" "$OUT"
echo "$EXE_SHA256  $OUT" | sha256sum -c -
echo "==> 已放入 $OUT（WireGuard for Windows $VERSION）"
