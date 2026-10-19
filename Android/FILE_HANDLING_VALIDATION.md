# Android 文件处理修复与验证（2026-10-19）

## 工具和测试设备

- Android Studio 自带 JDK 21，项目 SDK 路径可用，平台工具 adb 36.0.0。
- 用户指定 `127.0.0.1:16416` 为开发实例，Android 15 / API 35，型号 SDY-AN00。
- `127.0.0.1:16384` 是长期运行游戏的实例，`emulator-5554` 是它的重复连接。确认用途后不再操作这两个连接；未重启 adb 或模拟器。
- 应用和仪器测试包均仅安装到 `16416`。此次不需要 root。

## 改动

- 上传页读取 `/storage/space`，采用服务端默认目录，显示容量、有效期和不可用状态；超级管理员无独立空间时使用服务端允许的目录。
- 删除上传页根据手机 root 状态猜测远端路径的逻辑。上传队列启动时固定文件列表和远端目录，避免途中切换导致文件发往不同位置。
- 文件选择只提交 URI，元数据读取及缓存复制在 IO 协程完成。每次导入使用独立目录，保留文件名；失败和取消清理未完成副本，移除/清空队列及页面释放时清理自有缓存。
- 冲突副本同时保存完整相对路径；恢复先写临时文件，再原子替换原路径。失败不删除隔离副本，上传和上报成功后才清理。旧格式缺少路径时保留供手动恢复。
- 同步下载的显式日志及 HTTP 错误文本去掉 URL query，避免输出 token。
- 本轮代码注释统一中文，新增文件按职责拆分，单文件小于 1000 行。

## 已通过

- `:app:testDebugUnitTest`：新增文件处理业务测试 4 项，失败 0、错误 0。
- `:app:assembleDebug`、`:app:assembleDebugAndroidTest` 构建通过。
- adb 对 `16416` 安装应用与测试包成功。
- 指定 `FileSafetyDeviceTest` 在 `16416` 执行：`OK (1 test)`。在 Android 文件系统实际验证同名缓存互不覆盖、清理隔离、子目录冲突恢复及替换已存在的服务端副本。
- 应用冷启动成功，版本 `1.3.4 / 10304`，进程存在；首次启动停留在系统权限申请界面。未自动批准敏感权限或登录账号。
- 后续复查时 `16416` 转为 offline，针对该端口重连返回连接拒绝；没有重启 adb 服务或操作游戏实例。登录后的联调需等待开发实例恢复。
- 此后确认开发实例使用桥接网络，地址 `192.168.31.182`。按用户授权以 root 设置 `persist.adb.tcp.port=15555`，并通过重启开发 Android 验证持久生效。当前连接命令为 `adb connect 192.168.31.182:15555`；仅端口固定，IP 仍由桥接 DHCP 分配。游戏实例不变。
- `git diff --check` 通过。

## 构建环境注意

JBR 在 Windows 的 Unix domain socket 连接报 `Invalid argument: connect`，导致 Gradle 出现 `Unable to establish loopback connection`。
此次仅对构建进程设置 `JAVA_TOOL_OPTIONS=-Djdk.net.unixdomain.tmpdir=E:/fileSynchronization/project/Android/.gradle/disabled-uds`，该目录不创建，使 Pipe 初始化回退为 TCP；系统网络和全局 Java 设置均未修改。

依据：[OpenJDK Pipe 实现](https://android.googlesource.com/platform/libcore/+/be0538d7c8e2cbfa599dbcb4c5c69533678a8421/src/java.base/windows/classes/sun/nio/ch/PipeImpl.java)、[Java 网络属性](https://docs.oracle.com/en/java/javase/16/core/networking-properties.html)。

## 尚未验证

- 登录真实测试账号后的上传空间默认目录、容量、到期提示及真实文件上传。
- 跨设备同步冲突的完整网络流程、上传失败后的再次恢复。
- 真机安装、后台保活及长期运行表现。

本轮不实现组网，不修改后端或生产配置，不提交或推送代码。

## 服务器设置即时生效修复

用户实测发现：保存新服务器后直接登录仍访问旧地址，重启应用才生效。
根因为 `Request.baseUrl` 和静态资源地址在单例初始化时缓存；现改为每次构造请求时读取当前配置。

- 统一 HTTP、静态资源与 WebSocket 的主机、端口、协议和路由前缀，修复 WebSocket 写死 `/file` 的问题。
- 保存时校验地址和端口，提示保存结果；已连接的 WebSocket 在切换服务器后重连。
- 连接测试使用当前输入框的路径访问 `/v1/ping`，同时核对 HTTP 状态和健康检查 JSON，不再把 404 或普通网页当作后端正常。
- 地址与健康检查单元测试新增 2 项，通过。
- 在开发实例 `192.168.31.182:15555` 运行真实 HTTP 请求测试：先初始化请求单例，再不重启地切换两个主机名、端口与路径，两次请求均到达对应测试服务。
- 与文件系统回归一起执行，仪器测试结果 `OK (2 tests)`。应用与测试 APK 已覆盖安装，未清除应用数据。
- 未使用或落盘用户在对话中提供的账号密码。
