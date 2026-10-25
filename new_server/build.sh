#!/bin/bash
set -e
export PATH="$HOME/.cargo/bin:/usr/local/go/bin:$PATH"
cd "$(dirname "$0")"

ffmpeg_source="${FFMPEG_SOURCE:-}"
while [[ $# -gt 0 ]]; do
    case "$1" in
        --ffmpeg-source) ffmpeg_source="${2:?--ffmpeg-source 需要路径}"; shift ;;
        --ffmpeg-source=*) ffmpeg_source="${1#--ffmpeg-source=}" ;;
        -h|--help)
            echo "用法: $0 [--ffmpeg-source PATH]" >&2
            exit 0
            ;;
        *)
            echo "未知参数: $1" >&2
            exit 2
            ;;
    esac
    shift
done

bash ../sys_core/build.sh
if [[ -n "$ffmpeg_source" ]]; then
    bash tools/fetch-ffmpeg.sh linux --source "$ffmpeg_source"
else
    bash tools/fetch-ffmpeg.sh linux
fi
core_sha="$(sha256sum ../sys_core/lib/libsys_core.a | cut -d' ' -f1)"
CGO_ENABLED=1 CC=gcc go build -ldflags "-X syc-file/pkg/synccore.CoreArchiveSHA256=$core_sha" -o syc-file ./cmd
echo "==> built: $(pwd)/syc-file"
