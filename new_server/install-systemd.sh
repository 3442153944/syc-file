#!/usr/bin/env bash
# 把发布包安装成 systemd 服务（目标机首次部署用，root 执行）。
#
# 用法：
#   sudo bash install-systemd.sh [安装目录]
#   sudo DEST=/opt/syc-file bash install-systemd.sh
#
# 做的事：
#   1. 复制二进制与配置模板到安装目录；
#   2. 首次安装时生成 SYC_VPN_MASTER_KEY 写入 /etc/syc-file.env（600）；
#   3. 写 /etc/systemd/system/syc-file.service 并 enable --now；
#   4. 已有 config.yaml 不会被覆盖（避免冲掉数据库口令）。
#
# 升级：日常升级仍用 deploy.sh（构建 → 原子替换 → 重启 → 健康检查 → 回滚），
# 不需要重跑本脚本。
set -euo pipefail

[ "$(id -u)" -eq 0 ] || { echo "需要 root 运行" >&2; exit 1; }

SRC=$(cd "$(dirname "$0")" && pwd)
DEST=${1:-${DEST:-/opt/syc-file}}
SERVICE=syc-file

echo "==> 安装到 $DEST"
mkdir -p "$DEST/config" "$DEST/log"
install -m 755 "$SRC/syc-file" "$DEST/syc-file"

if [ -f "$SRC/config/config.example.yaml" ]; then
  cp -n "$SRC/config/config.example.yaml" "$DEST/config/config.example.yaml" || true
fi

if [ ! -f "$DEST/config/config.yaml" ]; then
  cp "$DEST/config/config.example.yaml" "$DEST/config/config.yaml"
  chmod 600 "$DEST/config/config.yaml"
  echo "已生成 $DEST/config/config.yaml —— 请先填 MySQL/Redis 连接与 file.allowed_paths，再启动服务"
  NEED_EDIT=1
else
  echo "保留已有 $DEST/config/config.yaml"
  NEED_EDIT=0
fi

if [ ! -f /etc/syc-file.env ]; then
  {
    echo "# 组网主密钥：私钥入库时的加密密钥，必须与数据库分离保存；丢失则已托管私钥无法解密"
    echo "SYC_VPN_MASTER_KEY=$(head -c 32 /dev/urandom | base64)"
  } > /etc/syc-file.env
  chmod 600 /etc/syc-file.env
  echo "已生成 /etc/syc-file.env（600）"
fi

cat > "/etc/systemd/system/$SERVICE.service" <<EOF
[Unit]
Description=CloudLadder FileSync Backend (syc-file)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=$DEST
EnvironmentFile=-/etc/syc-file.env
ExecStart=$DEST/syc-file
Restart=always
RestartSec=5
LimitNOFILE=1048576
TimeoutStopSec=30

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$SERVICE"

if [ "${NEED_EDIT:-0}" = "1" ]; then
  echo
  echo "配置文件还没填，先不要启动。填好后执行："
  echo "  sudo systemctl restart $SERVICE"
  exit 0
fi

systemctl restart "$SERVICE"
sleep 2
systemctl --no-pager --lines=10 status "$SERVICE" || true
echo
echo "完成。健康检查：curl -fsS http://127.0.0.1:<server.port>/v1/ping"
