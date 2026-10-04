#!/bin/bash
# 构建云梯后端一体镜像（Go 后端 + MySQL 8.0 + Redis）。
#
# 用法：  bash docker/build.sh [镜像名:标签]        （默认 filesync-server:dev）
# 环境变量（都有默认值，按需覆盖）：
#   IMAGE     镜像名:标签
#   GOPROXY   Go 模块代理，默认 goproxy.cn；境外构建用 https://proxy.golang.org,direct
#   GO_DL     Go 工具链下载地址，默认 golang.google.cn；境外构建用 https://go.dev/dl
#
# 在 VS Code 里：运行配置「构建: 后端 Docker 镜像」。
# 运行镜像：  docker run -d --name filesync -p 9999:9999 -v filesync-data:/data <镜像名:标签>
set -euo pipefail

cd "$(dirname "$0")/.."   # 构建上下文固定为 new_server/

IMAGE="${1:-${IMAGE:-filesync-server:dev}}"
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
GO_DL="${GO_DL:-https://golang.google.cn/dl}"

# 基础镜像先 pull：部分镜像加速器对 BuildKit 的元数据请求（HEAD）返回 403，而 docker pull 正常。
for base in rust:1-bookworm ubuntu:24.04; do
    docker image inspect "$base" >/dev/null 2>&1 || docker pull "$base"
done

echo "==> docker build -> $IMAGE"
docker build -t "$IMAGE" \
    --build-arg GOPROXY="$GOPROXY" \
    --build-arg GO_DL="$GO_DL" \
    .

echo "==> built: $IMAGE"
docker images "${IMAGE%%:*}" --format "    {{.Repository}}:{{.Tag}}  {{.Size}}"
