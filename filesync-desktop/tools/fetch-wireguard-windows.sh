#!/usr/bin/env bash
# 取 WireGuard for Windows 的 wireguard.exe，放进 src-tauri/resources/wireguard/。
# 需要 msitools（Debian/Ubuntu: apt install msitools）。
#
# 用法：  bash tools/fetch-wireguard-windows.sh [MSI_URL]
set -euo pipefail
cd "$(dirname "$0")/.."

URL=${1:-https://download.wireguard.com/windows-client/wireguard-amd64-0.5.3.msi}
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

command -v msiextract >/dev/null || { echo "缺少 msiextract（apt install msitools）" >&2; exit 1; }
echo "==> 下载 $URL"
curl -fL --retry 3 -o "$TMP/wg.msi" "$URL"
echo "==> 解包"
msiextract -C "$TMP/out" "$TMP/wg.msi" >/dev/null
EXE=$(find "$TMP/out" -iname 'wireguard.exe' | head -1)
[ -n "$EXE" ] || { echo "MSI 里没找到 wireguard.exe" >&2; exit 1; }
mkdir -p src-tauri/resources/wireguard
cp "$EXE" src-tauri/resources/wireguard/wireguard.exe
echo "==> 已放入 src-tauri/resources/wireguard/wireguard.exe"
