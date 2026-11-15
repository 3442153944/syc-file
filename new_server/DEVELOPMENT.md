# 后端开发启动

Windows：在 `new_server` 目录执行 `./run.ps1`；只构建用 `./run.ps1 -Build`。
Linux：执行 `bash run.sh`；只构建用 `bash run.sh -build`。
也可以直接执行 `go run ./cmd/dev`，或 `go run ./cmd/dev -prepare` 只准备核心库。

启动器只依赖 Go 标准库，在编译实际服务前调用根目录 `sys_core/build.ps1`（Windows）或
`sys_core/build.sh`（Linux），由 Cargo 的增量缓存判断共享源码是否更新，再读取
`sys_core/lib/libsys_core.a` 的 ELF / COFF 目标头，核对当前操作系统和 CPU。
编译失败、库缺失、目标平台不匹配时均停止，不能复用同架构旧库掩盖源码编译失败。

Go 的 cgo 直接使用根目录 `sys_core/include/sys_core.h` 和 `lib/libsys_core.a`。
官方开发/部署/Docker入口将静态库 SHA256 写入 Go 链接参数，使只改 Rust 也会重新链接，
避免外部 `.a` 不参与 Go 源码缓存检测而使用旧链接结果。

Windows 需要 Rust 的 `x86_64-pc-windows-gnu` target 及 MinGW-w64 GCC；
Linux 需要 Rust 和 GCC。自动重建不自动安装系统工具链。
Windows 的 `run.ps1` 会刷新 PATH；直接运行 Go 启动器时，如果找不到 GCC，
也会补充当前系统和用户的 PATH，兼容安装 MinGW 后尚未重启的 IDE。

## 内嵌 ffmpeg

`internal/thumb/embedded/` 下的 Windows/Linux 归档随仓库提交，版本与 SHA256 固定在
`FFMPEG.lock`。构建前 `tools/fetch-ffmpeg.*` 只做校验，不联网、也不读取系统 ffmpeg，
因此 Windows、Linux、Docker 与 systemd 部署使用的是同一份二进制。

- 校验：`bash tools/fetch-ffmpeg.sh linux`（Windows：`tools/fetch-ffmpeg.ps1 -Target windows`）。
- 临时换档：`./run.ps1 -FfmpegSource D:\ffmpeg目录或压缩包`，或
  `bash build.sh --ffmpeg-source /路径`；换档不会自动改锁，需按
  `internal/thumb/embedded/README.md` 更新版本与 SHA256。
- 升级流程见 `internal/thumb/embedded/README.md`。归档缺失时编译和部署都会直接失败。

## GoLand

将开发运行配置的 Go 文件改成 `new_server/cmd/dev/main.go`，或将包改成
`syc-file/cmd/dev`。工作目录可设为 `new_server`，启动器也会从自身源码位置定位项目。
程序参数 `-prepare` 只检查/重建核心，`-build` 生成服务二进制；其他服务参数放在 `--` 后。

`cmd/main.go` 保留为实际服务入口，生产部署、Docker 和现有构建脚本继续编译它。
直接 `go run ./cmd` 或在 GoLand 运行原来的 `cmd/main.go` 会绕过预检：cgo 链接在
`main()` 执行之前发生，无法在服务运行时修复链接所需的库。
开发启动器会构建并启动独立服务进程，因此在启动器上调试不会进入服务断点；
需要调试服务时，先用 `go run ./cmd/dev -prepare` 准备核心，再调试原来的服务入口。

共享库 `sys_core/lib/` 与兼容复制目录 `new_server/sync_core/lib/` 是当前平台的可重建产物，
不应随 Git 跨系统同步。`new_server/sync_core/build.*` 仅作为旧命令的转发入口，
当前 Go 构建不再链接旧目录。根目录的构建脚本必须随仓库跟踪。
