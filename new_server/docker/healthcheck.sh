#!/bin/bash
# 端口以 /data/config/config.yaml 的 server.port 为准（用户可能改过），读不到则退回 9999
port=$(awk "/^server:/{f=1;next} f&&/^[^ ]/{f=0} f&&\$1==\"port:\"{print \$2;exit}" /data/config/config.yaml 2>/dev/null)
exec curl -fsS "http://127.0.0.1:${port:-9999}/v1/ping" >/dev/null
