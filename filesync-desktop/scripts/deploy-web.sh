#!/bin/bash
# 构建 Web 端，并把 dist 的内容放进 nginx 站点目录。
#
# 用法：  bash scripts/deploy-web.sh
# 环境变量：WEB_ROOT 站点根目录，默认 /mnt/data/file_sync/web（见 /etc/nginx/conf.d/sunyuanling.conf）
#
# Web 模式下 API 走相对路径 /file，由 nginx 反代到后端；前端是 hash 路由，nginx 无需额外配置，
# 静态文件也不用 reload。
set -euo pipefail

cd "$(dirname "$0")/.."
WEB_ROOT="${WEB_ROOT:-/mnt/data/file_sync/web}"

pnpm build
[ -f dist/index.html ] || { echo "构建产物里没有 dist/index.html" >&2; exit 1; }

mkdir -p "$WEB_ROOT"
# 不保留属主 / 权限：站点目录约定全员可读写，别让 root 跑一次就把后续部署的权限锁死
cp -rf --no-preserve=ownership,mode dist/. "$WEB_ROOT/"
chmod -R a+rwX "$WEB_ROOT" 2>/dev/null || true

echo "已部署：dist/ -> $WEB_ROOT"
