#!/usr/bin/env bash
# 校验或安装内嵌 ffmpeg 归档（版本与 SHA256 见 internal/thumb/embedded/FFMPEG.lock）。
#
# 归档随仓库提交，默认只校验，不联网、也不使用系统 ffmpeg；Windows、Linux、
# Docker 与 systemd 部署因此使用完全相同的二进制。
#
# 用法: bash tools/fetch-ffmpeg.sh [windows|linux|all] [--source PATH] [--update-lock]
#   --source PATH   用指定目录/压缩包替换归档（临时换档或升级时使用）
#   --update-lock   替换归档后把新的 SHA256 写回 FFMPEG.lock
set -euo pipefail

target="linux"
source_path="${FFMPEG_SOURCE:-}"
update_lock=0

usage() {
    echo "用法: $0 [windows|linux|all] [--source PATH] [--update-lock]" >&2
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        windows|linux|all) target="$1" ;;
        --source) source_path="${2:?--source 需要路径}"; shift ;;
        --source=*) source_path="${1#--source=}" ;;
        --update-lock) update_lock=1 ;;
        -h|--help) usage; exit 0 ;;
        *) usage; exit 2 ;;
    esac
    shift
done

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
lock="$root/internal/thumb/embedded/FFMPEG.lock"
[[ -f "$lock" ]] || { echo "缺少版本锁: $lock" >&2; exit 1; }

lock_value() {
    sed -n "s/^$1=//p" "$lock" | head -1
}

version="$(lock_value version)"
[[ -n "$version" ]] || { echo "版本锁缺少 version 字段" >&2; exit 1; }

declare -A dests=(
    [windows]="internal/thumb/embedded/windows_amd64/ffmpeg.zip"
    [linux]="internal/thumb/embedded/linux_amd64/ffmpeg.tar.xz"
)
declare -A hash_keys=(
    [windows]="windows_amd64_sha256"
    [linux]="linux_amd64_sha256"
)

sha256_of() {
    sha256sum "$1" | awk '{ print $1 }'
}

set_lock_sha() {
    local key="$1" value="$2" tmp
    tmp="$(mktemp)"
    awk -v k="$key" -v v="$value" 'BEGIN { FS=OFS="=" } $1 == k { print k "=" v; next } { print }' "$lock" > "$tmp"
    mv "$tmp" "$lock"
}

find_bin() {
    local dir="$1"
    local name="$2"
    if [[ -f "$dir/bin/$name" ]]; then
        printf '%s\n' "$dir/bin/$name"
    elif [[ -f "$dir/$name" ]]; then
        printf '%s\n' "$dir/$name"
    else
        return 1
    fi
}

make_linux_archive() {
    local src="$1"
    local dest="$2"
    local ffmpeg ffprobe stage
    ffmpeg="$(find_bin "$src" ffmpeg)" || { echo "源目录中没有 ffmpeg: $src" >&2; return 1; }
    ffprobe="$(find_bin "$src" ffprobe)" || { echo "源目录中没有 ffprobe: $src" >&2; return 1; }

    stage="$(mktemp -d)"
    mkdir -p "$stage/bin"
    cp -f "$ffmpeg" "$stage/bin/ffmpeg"
    cp -f "$ffprobe" "$stage/bin/ffprobe"
    if [[ -d "$src/lib" ]]; then
        mkdir -p "$stage/lib"
        find "$src/lib" -maxdepth 1 \( -type f -o -type l \) \( -name '*.so' -o -name '*.so.*' \) -exec cp -a {} "$stage/lib/" \;
    fi

    local paths=(bin)
    [[ -d "$stage/lib" ]] && paths+=(lib)
    if ! tar -cJf "$dest.part" -C "$stage" "${paths[@]}"; then
        rm -rf "$stage"
        return 1
    fi
    rm -rf "$stage"
    mv -f "$dest.part" "$dest"
}

install_from_source() {
    local src="$1"
    local dest="$2"
    local kind="$3"
    if [[ -d "$src" ]]; then
        local archive="$src/ffmpeg.tar.xz"
        if [[ "$kind" == windows ]]; then
            archive="$src/ffmpeg.zip"
        fi
        if [[ -f "$archive" ]]; then
            cp -f "$archive" "$dest"
            return
        fi
        if [[ "$kind" != linux ]]; then
            echo "Windows 平台请直接传入 .zip 归档" >&2
            return 1
        fi
        make_linux_archive "$src" "$dest"
        return
    fi

    case "$kind:$src" in
        linux:*.tar.xz|windows:*.zip) cp -f "$src" "$dest" ;;
        *) echo "不支持的 ffmpeg 安装包/压缩包: $src" >&2; return 1 ;;
    esac
}

verify_target() {
    local key="$1" dest="$root/${dests[$key]}" expected actual
    expected="$(lock_value "${hash_keys[$key]}")"
    [[ -n "$expected" ]] || { echo "版本锁缺少 ${hash_keys[$key]}" >&2; return 1; }
    if [[ ! -f "$dest" ]]; then
        echo "缺少内嵌 ffmpeg 归档: $dest" >&2
        echo "从仓库恢复: git checkout -- ${dests[$key]}" >&2
        return 1
    fi
    actual="$(sha256_of "$dest")"
    if [[ "$actual" != "$expected" ]]; then
        echo "SHA256 不匹配: $dest" >&2
        echo "  期望: $expected" >&2
        echo "  实际: $actual" >&2
        echo "从仓库恢复: git checkout -- ${dests[$key]}" >&2
        echo "如为有意升级，请按 internal/thumb/embedded/README.md 更新 FFMPEG.lock" >&2
        return 1
    fi
    echo "==> ffmpeg $key 已固定: $version ($(du -h "$dest" | cut -f1), sha256 ${actual:0:12}...)"
}

install_target() {
    local key="$1" dest="$root/${dests[$key]}" actual
    echo "==> 用指定来源替换 ffmpeg $key: $source_path"
    install_from_source "$source_path" "$dest" "$key"
    actual="$(sha256_of "$dest")"
    echo "==> 新归档 SHA256: $actual"
    if [[ $update_lock -eq 1 ]]; then
        set_lock_sha "${hash_keys[$key]}" "$actual"
        echo "==> 已写入 FFMPEG.lock；如版本变化，请同时更新 version/built"
    else
        echo "提示: 未更新 FFMPEG.lock；提交前请写入 ${hash_keys[$key]}=$actual 并核对 version" >&2
    fi
}

case "$target" in
    windows|linux) targets=("$target") ;;
    all) targets=(windows linux) ;;
esac

if [[ -n "$source_path" && ${#targets[@]} -gt 1 ]]; then
    echo "指定 --source 时请一次只处理一个平台" >&2
    exit 2
fi

if [[ -n "$source_path" ]]; then
    install_target "${targets[0]}"
else
    for key in "${targets[@]}"; do
        verify_target "$key"
    done
fi