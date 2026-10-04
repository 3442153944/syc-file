#!/bin/bash
# 调试前释放 8991：先停 systemd 服务，再清掉本项目上一次调试残留的进程。
sudo -n systemctl stop syc-file 2>/dev/null || true

port_busy() { ss -ltn 2>/dev/null | grep -q ':8991 '; }

# 上一次 F5 的调试会话没退出（又按了一次 F5、或 VS Code 连接断开）时，dlv 拉起的 __debug_bin* 仍占着端口。
# 只匹配本项目 new_server/cmd 下的调试二进制，其它进程一律不碰。
stale=$(pgrep -f 'new_server/cmd/__debug_bin' || true)
if [ -n "$stale" ]; then
    echo "发现上一次调试残留的进程，正在结束：$stale"
    kill $stale 2>/dev/null || true
    for _ in 1 2 3 4 5 6 7 8 9 10; do
        port_busy || break
        sleep 0.5
    done
    if port_busy; then
        echo "优雅退出超时，强制结束"
        kill -9 $stale 2>/dev/null || true
        sleep 1
    fi
fi

if port_busy; then
    echo "端口 8991 仍被占用（不是本项目的调试进程，需手动处理）："
    ss -tlnp 2>/dev/null | grep ':8991 ' || true
    exit 1
fi
echo "端口 8991 空闲，可以启动调试"
