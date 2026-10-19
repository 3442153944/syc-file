# Android 开发进度

更新日期：2026-10-20（Asia/Shanghai）

本轮结论：快传、悬浮投递、分享历史和自动灾备已实现，最终构建与相关回归测试通过。调试版已覆盖安装到 MuMu 开发实例 `192.168.31.182:15555`。本轮到此收尾；未创建或删除分支，未提交或推送，未部署后端，未实现 Android 组网。

## 本轮实现

- **共享 Rust 决策**：上传沿用 `new_server/sync_core/src/upload_planner.rs`，Android `core/SyncPlanner.kt` 通过 JNI 调用；快传与普通上传复用 `ChunkedUploader`。没有在 Kotlin 重写分片选路、AIMD、DRR 或父链带宽折算。
- **共享灾备选路**：将 Windows 现有“当前入口健康则保持，故障时选择最低延迟可用入口”的决策抽到 `new_server/sync_core/src/node_selection.rs`。Windows `src-tauri/src/net.rs` 与 Android `NodeSelector` JNI 调用同一函数。平台代码只执行 HTTP 探测、配置持久化、连接和状态展示。
- **自动灾备**：服务器设置新增开关和备用入口编辑，最多 8 个；应用进程存活期间每 30 秒巡检，请求传输失败会触发节流检查。探测不带登录令牌，Android 自动接管只接受已确认属于同一后端的入口。尚未确认后端身份或所有入口不可用时保持当前配置并展示状态。健康的当前入口保持稳定，不因另一个入口更快而来回切换。
- **多节点快传**：快传初始化带 `quick_share=true`，多线路探测、同后端筛选、分片并发和失败重排仍由 Rust 上传规划器决定。内置节点及自定义备用入口进入规划池，保留原有父链关系。
- **会话与收尾**：记录服务端 `upload_id`；暂停、继续、取消并删除、失败重试和进程退出后的手动恢复均可使用。恢复时先查询原会话完成结果或缺片状态，保留原分片大小。收尾在规划器确认可用的同后端入口间重试，保存入口候选；复制前实际请求 Range 下载并检查附件响应头，拒绝失效链接。
- **可见队列**：入口位于首页“快传与分享管理”。文件先复制到应用私有暂存目录，保留原文件名，同名文件独立存放；队列和会话落盘，最多同时进行 2 个文件任务，每个文件内部仍可多线路并发。完成后清理暂存文件，失败或暂停保留供恢复。退出登录暂停任务，恢复前校验原服务器与账号。
- **悬浮窗**：空闲时仅显示约 52 dp 的半透明应用图标（图标 48 dp、整体透明度 0.65），可拖动位置。文件进入投递区域才展开，显示投递提示、上传进度和管理入口；投递上传结束后收回图标。普通文件选择上传不会自行展开悬浮图标。权限由用户在系统页面开启，可在快传页关闭。
- **系统分享**：支持 `ACTION_SEND` / `ACTION_SEND_MULTIPLE` 的文件及媒体 URI。悬浮投递由内部 Activity 获取 `DragAndDropPermissions`，完成暂存后释放授权。来源应用不支持全局拖拽时，使用系统“分享 → FileSync 快传”。
- **剪贴板**：默认在上传与链接验证完成后写入分享链接，界面可关闭自动复制；不会读取或持续监听用户剪贴板。
- **分享管理**：分页读取服务器分享历史，显示快传/普通分享、有效/过期/撤销状态及到期时间，支持复制和确认撤销。快传筛选作用于当前服务器分页；历史来自服务端，不局限于本机创建的任务。
- **实测修复**：修正快传前台服务被初始空队列误停的启动竞态；任务结束时取消无关慢线路探测，避免小文件已上传仍等待网络超时。分片协程取消会关闭实际 HTTP 请求。

## 验证结果

| 验证 | 结果 | 证据和覆盖范围 |
| --- | --- | --- |
| 共享 Rust 核心 | 26 项通过 | `cargo test --offline --lib`；含 3 项入口选择测试与原有上传规划测试 |
| Windows 兼容性 | `cargo check --offline` 通过 | 在 `filesync-desktop/src-tauri` 执行；本轮没有构建或安装新的 Windows 发布包 |
| Android Rust JNI | 两个 ABI 编译通过 | `arm64-v8a`、`x86_64`，已随调试包打包；实际设备执行验证为 x86_64 MuMu |
| Android JVM | 7 项通过 | 现有示例 1、文件安全 4、服务器地址 2，无失败 |
| Android 构建 | 通过 | `:app:testDebugUnitTest :app:assembleDebug :app:assembleDebugAndroidTest`，最终日志 `build/quick-share-build.log` |
| Android 设备回归 | **6 项通过，8.221 秒** | 最终输出 `build/quick-share-device-tests.log`，`OK (6 tests)` |
| 真实灾备 | 通过 | 设备内两个独立 TCP 入口，关闭主入口后，实际 JNI 决策切换到同后端备用入口；不同后端和禁用场景被拒绝 |
| 真实多线路分片 | 通过 | 40 MiB 文件经两条独立 TCP 入口共同传输；逐片对照源文件内容，收齐 10 片；不同后端第三入口收到 0 片 |
| 完成响应丢失 | 通过 | 接收端首次 complete 收到请求后断开连接，客户端跨可用入口重试，返回同一个分享码与入口候选 |
| 实际 Go 服务 | 通过 | 使用开发实例已登录账号，经当前配置入口上传测试文件，下载逐字节一致，分享历史出现快传记录；仅撤销本测试创建的链接 |
| 跨应用拖放与剪贴板 | 通过 | 独立测试 APK/UID 的受保护 ContentProvider → 长按拖入图标 → 系统 URI 授权 → 暂存 → 实际上传 → 链接验证 → 自动复制断言；随后撤销测试链接、删除本机测试任务 |
| 既有 Android 修复回归 | 通过 | Android 文件系统暂存/冲突恢复，以及不重启切换服务器地址的实际 HTTP 请求 |
| 差异格式检查 | 通过 | `git diff --check` 无格式错误（Git 仍提示仓库既有 LF/CRLF 转换策略） |

测试只操作开发实例，未操作游戏实例 `127.0.0.1:16384`。真实服务的测试链接已撤销；服务端按既有审计逻辑保留撤销历史。

最终快传页面和半透明图标已实际查看，截图为 `build/quick-share-final.png`。开发实例保留快传页与已开启的悬浮图标，便于直接体验；关闭入口在快传页。

## 复现命令

Windows PowerShell，Android 构建需本机 JDK21；以下变量仅在当前进程生效。`disabled-uds` 目录应保持不存在，用于绕过本机 JBR Unix socket 连接异常。

```powershell
cd E:/fileSynchronization/project/Android
$env:JAVA_HOME='C:/Program Files/Android/Android Studio2/jbr'
$env:PATH="$env:JAVA_HOME/bin;$env:PATH"
$env:JAVA_TOOL_OPTIONS='-Djdk.net.unixdomain.tmpdir=E:/fileSynchronization/project/Android/.gradle/disabled-uds'
./gradlew.bat :app:testDebugUnitTest :app:assembleDebug :app:assembleDebugAndroidTest --offline --no-daemon --console=plain

C:/platform-tools/adb.exe -s 192.168.31.182:15555 install -r app/build/outputs/apk/debug/app-debug.apk
C:/platform-tools/adb.exe -s 192.168.31.182:15555 install -r app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk
C:/platform-tools/adb.exe -s 192.168.31.182:15555 shell am instrument -w -e class com.sunyuanling.filesync.QuickShareDeviceTest,com.sunyuanling.filesync.QuickShareOverlayDeviceTest,com.sunyuanling.filesync.FileSafetyDeviceTest,com.sunyuanling.filesync.ServerConfigurationDeviceTest com.sunyuanling.filesync.test/androidx.test.runner.AndroidJUnitRunner
```

设备测试需要已登录开发账号和已授予本应用悬浮窗权限。测试不包含账号密码，不导出令牌或登录存储。

```powershell
cd E:/fileSynchronization/project/Android/sync_core_jni
$env:ANDROID_NDK_HOME='F:/download/android-ndk-r27d'
cargo ndk -t arm64-v8a -t x86_64 -o ../app/src/main/jniLibs build --release --offline

cd E:/fileSynchronization/project/new_server/sync_core
cargo test --offline --lib

cd E:/fileSynchronization/project/filesync-desktop/src-tauri
cargo check --offline
```

## 已知验证边界与后续事项

- 跨应用拖拽取决于来源应用是否提供 Android 全局拖拽和 URI 读授权；已实测独立应用来源，未声称所有图库或文件管理器均支持。系统分享是兼容入口。[Android Activity 拖放权限接口](https://developer.android.com/reference/android/app/Activity#requestDragAndDropPermissions(android.view.DragEvent))。
- 暂存可避免来源 URI 授权失效，但需要与所选文件大小相应的本机空间；Android 不采用 Windows 原始文件路径直读方式。
- 本轮验证 MuMu Android 15，未测试真机厂商后台限制、整机重启后自动恢复、长时间后台驻留或公网所有线路吞吐；中断任务默认保留并等待手动继续。
- Android 组网继续暂缓，原准备文档为 `MESH_INTEGRATION_PLAN.md`。
- 关键新增文件均低于 1000 行；此次扩展的 `ChunkedUploader.kt` 为约 847 行。代码注释使用中文。

## 本轮之前的 Android 成果

服务器地址保存后立即生效、上传空间权限读取、独立缓存和冲突文件安全恢复，详见 `FILE_HANDLING_VALIDATION.md`。开发实例 ADB 端口固定为 15555，当前桥接 IP 为 `192.168.31.182`；IP 本身仍取决于局域网 DHCP。
