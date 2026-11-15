# Android 独立 JNI/HTTP 验证

此测试通过 `app_process` 加载构建后的 JNI 库，不使用已安装 FileSync 应用、账户或会话。`SysUpload.java` 仅适配 JNI 事件到真实 HTTP；调度、文件读取、会话恢复及收尾使用 `sys_core::upload`。

1. 用 Android SDK 的 `android.jar` 编译 `SyncCore.java` / `SysUpload.java`，用 d8 生成包含 `classes.dex` 的 `harness.jar`。
2. 在本机运行 `python fixture_server.py --report REPORT.json`。服务只绑定 127.0.0.1，输出随机端口。
3. 向设备独立测试目录推送 `harness.jar` 和与设备 ABI 匹配的 `libsync_core_jni.so`；通过 `adb reverse tcp:28765 tcp:FIXTURE_PORT` 转发服务。
4. 执行 `CLASSPATH=DEVICE_DIR/harness.jar app_process /data/local/tmp com.sunyuanling.filesync.core.SysUpload DEVICE_DIR http://127.0.0.1:28765`。
5. 额外传 `hash-only` 可仅调用旧哈希 JNI 入口，对比历史二进制。

测试覆盖：ABI、描述/文件哈希/Merkle 一致、5 MiB+13 B 自适应传输、固定 512 KiB 分片及 404 会话重建、快传 Range/附件头校验、空文件和取消。HTTP fixture 逐块验证确定性原始字节并输出收齐统计。

验收必须同时检查断言、服务端报告和进程退出状态。2026-10-25 在 SDY_AN00 / Android 15 / x86_64 模拟器上，功能断言及服务端字节校验通过，但独立 `app_process` 退出为 SIGSEGV/139。Git 原有 JNI 库与新库在 `hash-only` 下均复现同样退出问题。这不是完整 APK/界面生命周期验证，也不能把断言成功字符串当作进程正常退出。
