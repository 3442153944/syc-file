# sys_core：FileSync 共享操作内核

共享文件和上传内核只在本目录维护。桌面直接链接 rlib，Android 的 JNI 包装链接同一 crate，Go 直接链接本目录的 staticlib。不同操作系统和 CPU 需要分别编译产物；不共享同一个二进制文件。

## 模块与边界

- `src/file_ops.rs`：文件预分配、句柄缓存、定位写、blake3/Merkle 校验、描述计算及稳定 `fc_*` C ABI。
- `src/file.rs`：Rust 类型化文件描述、整文件哈希、范围校验和完整分片读取；与 C ABI 共用计算实现。
- `src/upload_planner.rs`：测速决策、父链瓶颈、同后端校验、选片、AIMD、超时和重试预算。
- `src/upload/`（`upload` feature）：固定/自适应上传执行循环、秒传、会话重建、取消、旧尝试放弃、普通/快传收尾和入口下载检查。
- `src/upload/host.rs`：宿主 HTTP、节点切换取消、日志和状态持久化接口，不读取登录凭证或全局配置。
- `src/upload/bridge.rs`：外部运行时事件桥；Rust 持有任务与请求 ID，宿主执行请求后回报原始响应信封。分片字节通过单独接口取出，不用 JSON/base64 搬大文件。
- `src/node_selection.rs`、`src/sys_info.rs`：节点选择及平台系统采集。
- `src/mesh.rs`：桌面与 Android 共用的 mesh plan：候选端点轮换、CIDR 排序、握手确认。探测期间中继持续承载目标网段，直连 peer 暂不持有 AllowedIPs；确认后切直连，不再周期性删除探测 peer。
- `examples/mesh_plan.rs` 与 `tests/mesh/nat-acceptance.py`：同一 planner 驱动真实隔离 WireGuard/NAT。运行方法与端口预测边界见 [NAT-ACCEPTANCE.md](tests/mesh/NAT-ACCEPTANCE.md)。
- `include/sys_core.h`：唯一维护的 C ABI 声明，现有 `fc_*` 名称和 ABI v4 保持兼容。

宿主保留 HTTP 连接/平台证书、登录身份、权限、界面、进度显示、配置落点和应用生命周期。服务端保留 HTTP 鉴权、Redis 会话、数据库及同步业务编排。桌面 `sync_engine`、watcher、WS、剪贴板和窗口等应用模块不属于此次上传/文件内核迁移范围。

## 使用与产物

桌面 `filesync-desktop/src-tauri/Cargo.toml` 直接依赖 `sys_core` 并启用 `upload`；`chunked_uploader.rs` 保留原调用接口，只实现宿主适配。

Android `Android/sync_core_jni` 依赖同一核心。`SysUpload.kt` 只泵送 HTTP/进度/会话/持久化事件，`ChunkedUploader.kt` 是平台门面和节点注册表。缺失或过旧的上传 JNI 产物会明确报错，不再回退另一份 Kotlin 上传循环。现有 `SyncCore` 的哈希 JNI 入口与其他业务中的 Java 流式哈希兼容调用仍保留。

```powershell
# 测试共享内核（文件、C ABI、调度、执行桥及 mesh plan；最近一次 49 项通过）
cargo test --manifest-path sys_core/Cargo.toml --features upload --offline
# 桌面检查，需满足当前 Tauri 依赖的工具链要求
cargo +1.90.0 check --manifest-path filesync-desktop/src-tauri/Cargo.toml --offline
# Windows Android 完整构建（自动处理 Java UDS 临时目录）
.\Android\gradle-local.ps1 :app:assembleDebug --offline --console=plain --no-daemon
# Android：从 Android/sync_core_jni 执行，需配置 NDK 与 cargo-ndk
cargo ndk -t arm64-v8a -t x86_64 -o ../app/src/main/jniLibs build --release --offline
```

Android 产出每个 ABI 的 `libsync_core_jni.so`，保持现有 `System.loadLibrary("sync_core_jni")` 名称。JNI `nativeBridgeVersion=1` 检查新增上传桥入口，独立于已有 C ABI v4。最近一次双 ABI 重建：`arm64-v8a` 1,997,760 B、`x86_64` 2,129,608 B，均含 `MeshPlanner_nativePlan` 入口；尺寸、sha256 与验收环境见 `VALIDATION.md`。

Go 直接使用本目录的 C ABI 产物：仓库根执行 `./sys_core/build.ps1`（Windows GNU）或 `bash sys_core/build.sh`（Linux），生成 `sys_core/lib/libsys_core.a`。`--offline` 等额外参数传递给 Cargo；脚本尊重 `CARGO_TARGET_DIR`，并显式选择本机目标。`lib/` 与 `target/` 为本机产物，不入 Git；`build.sh`、`build.ps1` 随源码跟踪。

开发启动器每次调用 Cargo 检查共享源码，由 Cargo 自己复用未变缓存，再检查库的平台。Go 构建入口用库的 SHA256 作为链接参数，使 Rust-only 更新也会重新链接。旧 `new_server/sync_core/build.*` 仅转发到本目录并复制兼容名称；当前 Go、Docker、build/deploy 直接使用根 sys_core，不再依赖旧库。

后端 Docker 构建仍以 `new_server/` 为上下文，额外传共享核心的命名上下文；标准 `new_server/docker/build.sh` 已自动传入：

```sh
cd new_server
docker build --build-context sys_core=../sys_core -t filesync-server:dev .
```

## 调用约定

- FFI 维持 `panic="unwind"` 与边界 `catch_unwind`，字符串由匹配的 `fc_free_string` 释放。
- 上传描述与传输期间源文件必须保持稳定；读取完整范围，尺寸变化或短读报错，不补零上传。
- `missing=[]` 表示分片已收齐，直接收尾；非法、重复或越界索引拒绝进入执行循环。
- 暂停/取消时释放 Rust Future 或桥任务句柄；固定及自适应路径的子任务均归 `JoinSet` 管理。
- C ABI 删除/移动与定位写的并发约束仍见 `file_ops.rs`；迁移不改变原协议和服务端会话规则。
