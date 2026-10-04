#!/bin/bash
# 等 MySQL / Redis 端口就绪再起后端（supervisord 的 priority 只管启动顺序，不等就绪）
set -u
wait_port() {
    local port=$1 name=$2 i
    for i in $(seq 1 120); do
        (exec 3<>/dev/tcp/127.0.0.1/"$port") 2>/dev/null && return 0
        sleep 1
    done
    echo "等待 $name($port) 超时" >&2
    return 1
}
wait_port 3306 MySQL || exit 1
wait_port 6379 Redis || exit 1
export SYC_CONFIG_DIR=/data/config
cd /data
exec /opt/syc/syc-file
