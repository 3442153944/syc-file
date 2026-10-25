# 2026-10-25 验证记录

## 已通过

- `cargo test --manifest-path sys_core/Cargo.toml --features upload --offline`：35 项测试通过（27 单元、5 执行桥、3 文件/C ABI 契约）。默认 feature 的 29 项测试也通过，`cargo fmt --check`、`git diff --check` 通过。
- 桌面使用本机已有 `1.90.0` 工具链执行完整 `cargo check`；默认 1.88 低于当前仓库 Tauri 依赖要求。
- Android JNI 的 `arm64-v8a`、`x86_64` release 编译并打包到 `Android/app/src/main/jniLibs/`。
- 新 Kotlin 上传门面和事件适配器通过独立编译检查；随后修复 Windows Java Unix-domain socket 临时目录问题，完整 `:app:compileDebugKotlin`（23s）及 `:app:assembleDebug`（45s）均返回 BUILD SUCCESSFUL。项目入口为 `Android/gradle-local.ps1`。
- 兼容 `sync_core` staticlib 编译，C 头文件声明的 11 个 `fc_*` 符号均存在；此为 Windows MSVC 产物检查，不代表 Windows GNU/cgo 或 Linux 服务构建已验证。
- `192.168.31.182:15555` 的 Android 15 / x86_64 / SDY_AN00 模拟器：JNI ABI、文件哈希、Merkle 断言通过。真实 HTTP fixture 收齐并逐字节校验 3 次 5,242,893 B 文件（自适应 2 片、固定 11 片、快传 2 片）及空文件；注入 404 后重建会话成功，快传 Range 下载及附件头验证成功，取消句柄拒绝后续调用。

## 未完成的验证

- 正式 debug APK 已覆盖安装到模拟器，并冷启动进入系统权限页；尚未完成权限引导及真实账户上传/同步端到端测试。
- 独立 `app_process` 在断言通过后退出为 SIGSEGV/139；Git 原有 JNI 和本次 JNI 的最小 `hash-only` 均复现；后续不加载 Rust 的纯 Java 图标渲染器也在图片写出后复现同样退出段错误。保留为待定位的模拟器 app_process 退出路径问题，不宣称进程正常退出。
- 未执行 Linux VM 部署、Docker 镜像构建、生产 HTTP 接口或跨端真实账户同步测试。

## 模拟器开放配置

用户明确授权无资料模拟器使用 root、关闭网络过滤/锁屏、默认信任外部 adb 连接。已放行 IPv4/IPv6 filter、raw 及带宽 BPF 过滤入口；保留网络路由/NAT 所需机制。原 SELinux 已是 Permissive。

- adbd 同时监听 `15555` / `5557` / `16416`，`ro.adb.secure=0`、`ro.debuggable=1`，锁屏禁用。
- 开机脚本：`/data/adb/service.d/90-filesync-test-open-adb.sh`。
- 原规则、属性及恢复脚本：`/data/local/tmp/filesync-emulator-network-20261025-01a171a2/`。
- 经过 `adb reboot` 后重新回读，三个端口都连接成功；不提供任何密钥的原始 CNXN 握手在三个端口均直接收到 CNXN。
- 恢复网络规则/adb 属性：以 root 执行上述目录的 `restore.sh`；会移除开机脚本并重启 adbd，恢复仅监听原 15555 端口及 adb 认证。

工作区原有 `.claude/` 未处理。没有创建/删除分支，没有 commit 或 push。

## Android 图标裁剪修复

截图中的裁剪来自前景 PNG 直接作为自适应图标前景：有效绘制范围超出 66dp 安全圆。新增 `drawable/ic_launcher_foreground_safe.xml`，四边各内缩 19%，普通和 round 自适应图标都引用此包装，不改原 PNG 或配色。

- 五个 density 的全部非透明像素，缩放后均落在 33dp 安全半径内。
- AAPT2 编译及资源链接通过；独立预览 APK 在模拟器上安装成功。
- Android `AdaptiveIconDrawable` 原生渲染修改前、修改后及圆形资源，确认箭头与文档均完整；渲染 PNG 保留在 `sys_core/target/icon-preview/`。
- 临时图标预览应用和设备临时文件已清理。完整 debug APK 已重新构建并覆盖安装，回到模拟器主屏实看确认“私有云”图标的外圈与文档均完整，截图位于 `sys_core/target/loopback-diagnostic/launcher-after-install.png`。

## Windows Gradle loopback 修复

底层异常为 `Selector.open -> WEPollSelectorImpl -> PipeImpl -> UnixDomainSockets.connect0: Invalid argument: connect`。最小 `Selector.open()` 在默认临时目录下复现；仅增加 `-Djdk.net.unixdomain.tmpdir=有效绝对目录` 后，Microsoft JDK 21.0.7 与 JetBrains JBR 21.0.11 均通过。

`Android/gradle-local.ps1` 创建项目内 `.gradle/uds`，通过临时 `JAVA_TOOL_OPTIONS` 将该路径传给 wrapper/daemon/worker JVM，结束后恢复环境变量；GRADLE_USER_HOME 未指定时使用 USERPROFILE 下现有缓存。未更改 Windows 防火墙、代理、JDK 安装或全局环境变量。

实际验证命令：`./gradle-local.ps1 :app:assembleDebug --offline --console=plain --no-daemon`。产物 `Android/app/build/outputs/apk/debug/app-debug.apk`，42,079,631 B；APK 内确认两个 ABI 的新 SysUpload JNI 入口、SysUpload DEX 类及 foreground_safe 图标资源存在。`adb install --no-incremental -r` 成功。

## 2026-10-25 接手验证（DSH）

本小节只记录接手后实际执行的检查，上文原有记录保留；静态/构建结果与设备实测分开表述。

### 静态检查与构建（非设备验收）

- `cargo fmt --check` 通过。
- `cargo test --manifest-path sys_core/Cargo.toml --features upload --offline`：47 项通过（lib 39、执行桥 5、文件/C ABI 3）。
- 新增 mesh CIDR 排序单测覆盖：同 LAN 优先；public/DNS 先于其他 private；server/relay 候选同 rank；`confirmed_endpoint` 在候选内保留（即使 rank 较低），候选外忽略并继续轮换；非法 CIDR、IPv6、loopback、端口 0 被过滤；无 `local_networks` 时保持旧顺序不回归。
- JNI 双 ABI 重建：在 `Android/sync_core_jni` 下设置 `ANDROID_NDK_HOME=F:\download\android-ndk-r27d`，执行 `cargo ndk -t arm64-v8a -t x86_64 -o ../app/src/main/jniLibs build --release --offline`。
  - `arm64-v8a/libsync_core_jni.so`：1,997,760 B，sha256 `D90D7ABF868659189DD3EAD4FC2D15CA4EA0AEAA57DA7D16F18CEC577CA287CC`。
  - `x86_64/libsync_core_jni.so`：2,129,608 B，sha256 `7648BE02F3EA305ADD12B6053A0C57A072ED15DEC4E3DC04DA90440B7E3D8EF1`。
  - `llvm-nm -D` 确认两个产物均含 `Java_..._MeshPlanner_nativePlan`。
- APK 与 instrumentation：`gradlew :app:assembleDebug :app:assembleDebugAndroidTest` BUILD SUCCESSFUL。仅证明可构建，不代表设备验收。

### 设备实测（模拟器 192.168.31.182:5557，KernelSU root；Clash Meta 2.11.13，tun0 172.19.0.1/30 运行中）

- `MeshDeviceTest#rootTunnelAndManualNodeSelection` 两轮均 `OK (1 test)`：LAN/API 路径约 20.098 s；`testMeshViaProxy=true` 云路径约 20.251 s。
- fixture `172.1.0.254:18080` 返回 `filesync-mesh-fixture`，HTTP 200。
- `expectClash` 断言通过：Clash VPN handle 在 Mesh 前、中、后保持不变。
- 云路径容器观察到的 WG 源为 `192.168.31.100:61784`（非 Android `192.168.31.182`），说明丢失原始 IP 仍可通信。

### Clash 共存结论与限制

- root 独立接口流量会被 Clash 默认网络规则抢走。`MeshController` 增加仅 `to 172.1.0.0/16 lookup filesync pref 1000` 的临时 ip rule，断开时按 selector 循环清理；不修改 Clash 全局规则。
- Clash 给系统设置 HTTP 代理时，OkHttp 默认走系统代理会截获 plain HTTP；访问组网网段的请求需使用 `NO_PROXY` 客户端（本次测试用 directClient）。

### 本轮未验证

- 无 root 全 mesh 与“防抢占”行为未实测。
- Windows LAN peer（`172.1.0.4:18082`）本轮未重建临时 WG 接口复测。

### 后端全链路与清理说明

- 为验证最新 Go 源码：重建 Rust 静态库 `new_server/sync_core/lib/libsync_core.a`（windows/amd64，2026-10-25），并用 `go run ./cmd/dev -build` 以 CGO_ENABLED=1 成功构建 `new_server/syc-file.exe`（161,685,614 B）。
- 但启动第二个后端实例（8993 端口冒烟）时，本机到 VM MySQL 3306 的连接被环境拦截：`connectex: An attempt was made to access a socket in a way forbidden by its access permissions` / i/o timeout，15 次重试后该实例按设计退出。因此未切换 8992 后端：上方设备实测运行的后端仍是 `syc-mesh-test3.exe`（16:18 构建，早于最新 Go 源码）；最新 Go 改动仅由单元测试与 cgo 构建验证，运行期未验证。
- 清理：已停止闲置的 18991→8991 SSH 反向隧道和 `172.1.0.4:18082` 孤立 HTTP fixture；8992 测试后端、18992/18993 反向隧道、云 UDP 12000、VM 测试容器保留供继续验收。`new_server/sync_core/lib/libsync_core.a` 现为重建后的新文件，属未提交变更。


## 2026-10-25 NAT 自动决策验收（后续轮次）

- 旧 planner 未握手就让直连 peer 接管目标 /32，在真实对称 NAT 下复现 ping 失败。现让探测 peer 保持空 AllowedIPs，中继持续承载业务；握手确认才切直连，并取消周期删除探测 peer 的窗口。
- examples/mesh_plan.rs 直接调用共享 planner，tests/mesh/nat-acceptance.py 应用结果到 VM 自有 Docker netns 的真实 WG，不再手工指定回退路由。
- 最终15秒 cadence测试12次 probe全部成功：对称NAT六轮relay TTL63；映射改善后自动direct/direct TTL64；再次对称重绑定且改变端口后四轮relay TTL63。证据 target/mesh-test/nat-final.jsonl，failures=[]。该重绑定包含端点观测更新，未证明旧候选不变时立即检测失效。
- 最终Android与Clash共存：LAN/API OK (1 test)，20.079 s；云12000 OK (1 test)，20.17 s；Android+Windows真实LAN peer OK (1 test)，51.368 s。Windows记录172.1.0.3 GET /probe.txt HTTP200，WG endpoint为192.168.31.182:51821；Clash VPN前/中/后不变。
- Android宿主支持空AllowedIPs，增加当前端点尝试时间校验；wg-quick重配的临时DOWN回调不再发布为用户断开。双ABI、APK与instrumentation重新构建并安装成功。
- 共享核心49项测试通过（41+5+3），core/CLI fmt检查通过，CLI3项测试通过，桌面最终cargo +1.90.0 check --offline通过。
- CLI补充修正：STUN公网IP加本地WG监听端口仍不等于NAT外部端点。nat --report现要求显式--observed-wg-endpoint，保留外部ip:port；--wg-port仅作诊断，避免同NAT下两设备监听同端口时误报。
- Windows自有fs-mesh-test、精确防火墙规则、临时私钥配置与HTTP进程已清，原syc0运行。Android无filesync接口或优先rule残留，Clash tun0保持。NAT脚本finally清自有natwg并恢复fixture规则，容器与原测试基础设施保留。
- 仍未验收：生产端并发端口预测、多设备共享NAT且步长不定的预测命中率、无root完整mesh、桌面GUI。最新Go运行期未切换的限制沿用。完整说明见 tests/mesh/NAT-ACCEPTANCE.md。
