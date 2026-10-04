#!/bin/bash
# 构建 Linux 桌面端并安装到系统：pnpm tauri build 出 deb，再用 apt 安装（系统级部署，不依赖构建目录里的二进制）。
#
# 用法：  bash scripts/install-linux-desktop.sh [--pin] [--desktop] [--no-build] [--uninstall]
#   （默认）     构建 → 安装 deb：/usr/bin/filesync-desktop + 应用菜单入口 + 图标
#   --pin        固定到 GNOME Dock（写 org.gnome.shell favorite-apps，需在桌面会话的终端里运行）
#   --desktop    在桌面上放一个快捷方式
#   --no-build   跳过构建，直接安装 src-tauri/target/release/bundle/deb 里最新的 deb
#   --uninstall  卸载 deb，并取消 Dock 固定、删除桌面快捷方式
#
# 说明：
#   - 安装用 apt-get（会自动装好 webkit2gtk 等依赖），并加 --reinstall：同版本号（比如一直是 0.1.0）也会
#     真正覆盖，否则 apt 会因为「已是最新版本」什么都不做。需要 root：非 root 时自动用 sudo（会提示输入密码）。
#   - deb 的入口文件叫 filesync-desktop.desktop：GNOME（Wayland）用窗口 app_id 去匹配 .desktop 文件名，
#     Tauri 窗口的 app_id 就是可执行文件名 filesync-desktop，对得上才能归并到图标、固定到 Dock。
#   - 正在运行的云梯不会被替换：装完需要退出并重新打开，才会用上新版本。
set -euo pipefail

APP_ID=filesync-desktop
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEB_DIR="$ROOT/src-tauri/target/release/bundle/deb"
PIN=0
DESK=0
BUILD=1
UNINSTALL=0

while [ $# -gt 0 ]; do
    case "$1" in
        --pin) PIN=1; shift ;;
        --desktop) DESK=1; shift ;;
        --no-build) BUILD=0; shift ;;
        --uninstall) UNINSTALL=1; shift ;;
        -h|--help) sed -n '2,17p' "$0"; exit 0 ;;
        *) echo "未知参数: $1（--help 查看用法）" >&2; exit 2 ;;
    esac
done

DATA="${XDG_DATA_HOME:-$HOME/.local/share}"
DESKTOP_DIR="$(xdg-user-dir DESKTOP 2>/dev/null || echo "$HOME/Desktop")"
SYS_ENTRY="/usr/share/applications/$APP_ID.desktop"
FAV_KEY="$APP_ID.desktop"

have() { command -v "$1" >/dev/null 2>&1; }
as_root() { if [ "$(id -u)" = 0 ]; then "$@"; else sudo "$@"; fi; }
fav_get() { gsettings get org.gnome.shell favorite-apps 2>/dev/null; }

unpin() {
    have gsettings || return 0
    local cur; cur="$(fav_get)" || return 0
    case "$cur" in *"'$FAV_KEY'"*) ;; *) return 0 ;; esac
    local new; new="$(echo "$cur" | sed -e "s/, *'$FAV_KEY'//" -e "s/'$FAV_KEY', *//" -e "s/'$FAV_KEY'//")"
    gsettings set org.gnome.shell favorite-apps "$new" && echo "已从 Dock 取消固定"
}

# 早期版本的脚本会在用户目录放一份指向构建目录的入口和图标；它会盖住系统级入口，必须清掉
remove_user_level_leftovers() {
    local f="$DATA/applications/$APP_ID.desktop"
    [ -f "$f" ] && { rm -f "$f"; echo "已清理旧的用户级入口：$f"; }
    local s
    for s in 32x32 128x128 256x256; do rm -f "$DATA/icons/hicolor/$s/apps/$APP_ID.png"; done
    return 0
}

if [ "$UNINSTALL" = 1 ]; then
    unpin || true
    rm -f "$DESKTOP_DIR/$APP_ID.desktop"
    remove_user_level_leftovers
    if dpkg -s "$APP_ID" >/dev/null 2>&1; then
        as_root apt-get remove -y "$APP_ID"
    else
        echo "系统里没有安装 $APP_ID"
    fi
    echo "已卸载"
    exit 0
fi

# ── 构建 ──────────────────────────────────────────────────────
if [ "$BUILD" = 1 ]; then
    echo "==> 构建 deb（pnpm tauri build --bundles deb）"
    ( cd "$ROOT" && pnpm tauri build --bundles deb )
fi

DEB="$(ls -t "$DEB_DIR"/${APP_ID}_*.deb 2>/dev/null | head -1 || true)"
if [ -z "$DEB" ]; then
    echo "找不到 deb：$DEB_DIR/${APP_ID}_*.deb（去掉 --no-build 重新构建）" >&2
    exit 1
fi
DEB="$(readlink -f "$DEB")"
echo "==> 安装 $DEB"

# ── 安装 ──────────────────────────────────────────────────────
remove_user_level_leftovers
as_root apt-get install -y --reinstall "$DEB"

[ -f "$SYS_ENTRY" ] || { echo "安装后没有找到 $SYS_ENTRY，请检查 deb" >&2; exit 1; }
echo "==> 已安装：$(dpkg-query -W -f='${Package} ${Version}' "$APP_ID")"
echo "    可执行文件：$(command -v "$APP_ID" || echo /usr/bin/$APP_ID)"
echo "    应用入口：  $SYS_ENTRY"

if pgrep -x "$APP_ID" >/dev/null 2>&1; then
    echo "注意：检测到云梯正在运行，它还是旧版本；退出后重新打开才会用上新安装的版本。"
fi

# ── 桌面快捷方式 ──────────────────────────────────────────────
if [ "$DESK" = 1 ]; then
    if [ -d "$DESKTOP_DIR" ]; then
        cp -f "$SYS_ENTRY" "$DESKTOP_DIR/$APP_ID.desktop"
        chmod +x "$DESKTOP_DIR/$APP_ID.desktop"
        # GNOME 需要标记为「可信」，否则桌面上的快捷方式是灰的、双击不会启动
        have gio && gio set "$DESKTOP_DIR/$APP_ID.desktop" metadata::trusted true 2>/dev/null || true
        echo "==> 已在桌面创建快捷方式：$DESKTOP_DIR/$APP_ID.desktop（若图标是灰色：右键 → 允许启动）"
    else
        echo "没有找到桌面目录 $DESKTOP_DIR，已跳过桌面快捷方式" >&2
    fi
fi

# ── 固定到 Dock ───────────────────────────────────────────────
if [ "$PIN" = 1 ]; then
    if have gsettings && cur="$(fav_get)"; then
        case "$cur" in
            *"'$FAV_KEY'"*) echo "==> Dock 里已经固定过了" ;;
            "@as []"|"[]") gsettings set org.gnome.shell favorite-apps "['$FAV_KEY']" && echo "==> 已固定到 Dock" ;;
            *) gsettings set org.gnome.shell favorite-apps "${cur%]}, '$FAV_KEY']" && echo "==> 已固定到 Dock" ;;
        esac
    else
        echo "无法写入 Dock 收藏（没有 gsettings，或不在桌面会话里，比如通过 ssh 运行，或用了 sudo 跑整个脚本）。" >&2
        echo "请在桌面的终端里运行：bash scripts/install-linux-desktop.sh --no-build --pin" >&2
        echo "或者：启动应用后在 Dock 图标上右键 → 添加到收藏夹。" >&2
    fi
fi

echo
echo "完成。在「显示应用程序」里搜「云梯」即可启动。"
