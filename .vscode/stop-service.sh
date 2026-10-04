#!/bin/bash
sudo -n systemctl stop syc-file 2>/dev/null || true
sleep 1
if ss -ltn 2>/dev/null | grep -q ':8991 '; then
    echo "端口 8991 仍被占用（非系统服务）："
    ss -tlnp 2>/dev/null | grep ':8991 ' || true
    exit 1
fi
echo "系统服务已停止，端口 8991 空闲"
