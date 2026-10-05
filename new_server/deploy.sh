#!/bin/bash
# 后端非 Docker 部署（本机，由 systemd 的 syc-file.service 托管）：
#   检查端口 → 构建 → 原子替换二进制 → 重启服务 → 健康检查，失败自动回滚到上一版。
#
# 用法：  bash deploy.sh [--build-only] [--no-filecore]
#   --build-only   只构建并校验能编译，不替换二进制、不重启服务
#   --no-filecore  跳过 Rust 核心库（libfilecore.a）重建；file_lib 没改过时可加，省一点时间
#
# 说明：
#   - 端口取自 config/config.yaml 的 server.port（默认 8991）。
#   - 数据库表结构由后端启动时 AutoMigrate 自动升级，不需要手工迁移。
#   - 只用免密 sudo 允许的三条命令：systemctl stop|start|restart syc-file（见 /etc/sudoers.d/syc-dev）。
#   - 替换用 mv（原子 rename）：正在运行的旧进程继续用旧文件，直到重启，不会出现 "text file busy"。
#   - 上一版保存在 syc-file.prev；部署失败会自动换回它并重启。
set -euo pipefail

export PATH="$HOME/.cargo/bin:/usr/local/go/bin:$PATH"
cd "$(dirname "$0")"

SERVICE=syc-file
BIN=syc-file
BUILD_ONLY=0
FILECORE=1
for a in "$@"; do
    case "$a" in
        --build-only) BUILD_ONLY=1 ;;
        --no-filecore) FILECORE=0 ;;
        -h|--help) sed -n '2,15p' "$0"; exit 0 ;;
        *) echo "未知参数: $a（--help 查看用法）" >&2; exit 2 ;;
    esac
done

PORT="$(awk '/^server:/{f=1;next} f&&/^[^ #]/{f=0} f&&$1=="port:"{print $2;exit}' config/config.yaml 2>/dev/null || true)"
PORT="${PORT:-8991}"

step() { echo; echo "==> $*"; }
health() { curl -fsS -m 2 "http://127.0.0.1:$PORT/v1/ping" 2>/dev/null; }

# 等服务起来并通过健康检查，最多 $1 秒
wait_healthy() {
    local i
    for i in $(seq 1 "$1"); do
        if systemctl is-active --quiet "$SERVICE" && health >/dev/null; then return 0; fi
        sleep 1
    done
    return 1
}

# ── 0. 端口占用检查（先于构建，尽早失败）─────────────────────────
# 端口被 F5 调试进程（__debug_bin*）占着时，重启服务只会因为端口冲突反复失败；
# 这里不替你结束调试会话（那是你正在用的），提示后退出。
if [ "$BUILD_ONLY" = 0 ]; then
    owner="$(ss -ltnp "sport = :$PORT" 2>/dev/null | grep -o 'users:(("[^"]*"' | head -1 || true)"
    case "$owner" in
        *__debug_bin*)
            echo "中止：端口 $PORT 正被调试进程占用（$owner）。" >&2
            echo "      请先结束 VS Code 的调试会话（或运行 bash ../.vscode/stop-service.sh 清理残留），再部署。" >&2
            exit 1 ;;
    esac
fi

# ── 1. 构建 ──────────────────────────────────────────────────
if [ "$FILECORE" = 1 ]; then
    step "构建 Rust 核心库 libfilecore.a"
    bash file_lib/build.sh
fi
step "构建后端（go build）"
CGO_ENABLED=1 CC=gcc go build -trimpath -o "$BIN.new" ./cmd
echo "构建完成：$(pwd)/$BIN.new ($(du -h "$BIN.new" | cut -f1))"

if [ "$BUILD_ONLY" = 1 ]; then
    rm -f "$BIN.new"
    echo "--build-only：编译通过，未替换二进制、未重启服务。"
    exit 0
fi

# ── 2. 替换二进制（保留上一版）────────────────────────────────
step "替换二进制（上一版 -> $BIN.prev）"
HAD_PREV=0
# 不用 cp -p：它要复制时间戳，而 .prev 可能是别的用户（如 root）留下的，非属主改不了时间戳会直接报错中止。
# 回滚只需要内容和可执行位，先删掉旧的 .prev 再只保留权限位。
if [ -f "$BIN" ]; then rm -f "$BIN.prev"; cp --preserve=mode "$BIN" "$BIN.prev"; HAD_PREV=1; fi
mv -f "$BIN.new" "$BIN"

# ── 3. 重启并健康检查 ─────────────────────────────────────────
step "重启服务 $SERVICE 并等待健康检查（端口 $PORT）"
sudo -n systemctl restart "$SERVICE"
if wait_healthy 40; then
    echo "部署成功：$(health)"
    exit 0
fi

# ── 4. 失败：打印日志并回滚 ───────────────────────────────────
echo >&2
echo "部署失败：服务没有在 40 秒内通过健康检查。最近的服务日志：" >&2
journalctl -u "$SERVICE" -n 40 --no-pager >&2 || true

if [ "$HAD_PREV" = 1 ]; then
    step "回滚到上一版"
    mv -f "$BIN.prev" "$BIN"
    sudo -n systemctl restart "$SERVICE"
    if wait_healthy 40; then
        echo "已回滚到上一版并恢复正常：$(health)" >&2
    else
        echo "回滚后服务仍不健康，请查看：journalctl -u $SERVICE -n 100" >&2
    fi
else
    echo "没有上一版可回滚（这是首次部署）。" >&2
fi
exit 1
