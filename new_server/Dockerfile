#
# 云梯后端一体镜像：Go 后端 + MySQL 8.0 + Redis，一个容器跑完。
# 构建统一用脚本：  bash docker/build.sh   （VS Code 运行配置「构建: 后端 Docker 镜像」）
# 手动构建时上下文是 new_server/，国内需要 --build-arg GOPROXY=... --build-arg GO_DL=...，参数见脚本。
# 运行：  docker run -d --name filesync -p 9999:9999 -v filesync-data:/data filesync-server

# ── 构建阶段：Rust 编译 libfilecore.a → Go(cgo) 链接 ────────────────
FROM rust:1-bookworm AS builder
ARG TARGETARCH
ARG GO_VERSION=1.25.3
ARG GO_DL=https://go.dev/dl
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} PATH=/usr/local/go/bin:$PATH

RUN curl -fsSL "${GO_DL}/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" | tar -C /usr/local -xz

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# Rust 核心单独成层：只改 Go 代码时不重编 Rust
COPY file_lib ./file_lib
RUN bash file_lib/build.sh
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/syc-file ./cmd

# ── 运行阶段 ───────────────────────────────────────────────────
FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive TZ=Asia/Shanghai LANG=C.UTF-8 SYC_MODE=prod
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        mysql-server redis-server supervisor openssl curl ca-certificates tzdata ffmpeg \
    && rm -rf /var/lib/mysql /var/lib/apt/lists/* \
    && mkdir -p /opt/syc /data

COPY --from=builder /out/syc-file /opt/syc/syc-file
COPY docker/config.template.yaml docker/run-backend.sh docker/entrypoint.sh docker/healthcheck.sh /opt/syc/
COPY docker/supervisord.conf /etc/supervisor/supervisord.conf
COPY docker/mysql.cnf /etc/mysql/conf.d/zz-syc.cnf
RUN chmod +x /opt/syc/*.sh

VOLUME /data
# 默认端口 9999，可在 /data/config/config.yaml 的 server.port 修改（同时调整 -p 映射）
EXPOSE 9999
HEALTHCHECK --interval=30s --timeout=5s --start-period=90s --retries=3 \
    CMD /opt/syc/healthcheck.sh
STOPSIGNAL SIGTERM
ENTRYPOINT ["/opt/syc/entrypoint.sh"]
