# 内嵌 ffmpeg（固定版本）

后端通过 `go:embed` 把 ffmpeg/ffprobe 打进二进制，运行阶段解压到用户缓存目录，
不依赖服务器安装 ffmpeg。归档本身随仓库提交，版本与 SHA256 记录在
[`FFMPEG.lock`](FFMPEG.lock)，构建脚本只做校验，不再联网下载。

## 当前版本

- `N-127650-geceaa54a38-20261018`（BtbN/FFmpeg-Builds master 快照，2026-10-18 构建）
- `linux_amd64/ffmpeg.tar.xz`：Linux amd64 共享版（含 `lib/*.so`）
- `windows_amd64/ffmpeg.zip`：Windows amd64 共享版（含 `*.dll`）

两个归档来自同一构建批次，Windows 与 Linux 的行为保持一致。

## 校验

```bash
bash tools/fetch-ffmpeg.sh linux      # 或 all / windows
```

默认只按 `FFMPEG.lock` 校验 SHA256，不下载、不读取系统 ffmpeg。

## 升级

1. 取得新的 BtbN 归档；
2. 执行 `bash tools/fetch-ffmpeg.sh linux --source /path/to/archive --update-lock`
   （Windows 用 `tools/fetch-ffmpeg.ps1 -Source ... -UpdateLock`）；
3. 修改 `FFMPEG.lock` 的 `version` / `built` 字段，并在本文件记录变更；
4. 重新构建，确认 `ffmpeg -version` 与锁文件一致后提交。

归档文件较大（Linux 约 72 MB，Windows 约 91 MB），提交前确认对仓库体积的影响。