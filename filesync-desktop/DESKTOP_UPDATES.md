# 应用发布与桌面更新

Android、Windows、Linux 共用「应用更新」发布页、版本列表和上架/下架管理。
Windows 使用 NSIS `.exe`，Linux 使用 `.deb` 或 AppImage；每个桌面包按平台、架构和包类型独立登记。

## 本机发布

1. 在 `filesync-desktop` 运行 `pnpm build-update --version 0.1.1`（也可以使用 `pnpm build-exe --version 0.1.1`）。
2. Windows 产物在 `src-tauri/target/release/bundle/nsis`；Linux 在 `bundle/deb` 和 `bundle/appimage`。
3. 打开「应用更新」，选平台和安装包。桌面端自动读取同路径的 `安装包.sig`，并从 Tauri 文件名识别版本与架构；核对后发布。
4. Web 发布时同时选择安装包和对应 `.sig`，或用签名选择按钮补充；Android 仍只需 APK。

也支持 `npm run build-update -- --version 0.1.1`；npm 与 pnpm 共用同一个构建脚本。
更新 Tauri 依赖可运行 `npm run update` 或 `pnpm run update`，脚本会顺序更新前端包和 Rust 插件；随后应重新打包验证版本兼容性。

安装包上传到服务器 `/mnt/data/file_sync/updates`（目录必须已存在并在允许路径内）。上传使用既有分片接口，不改变原文件内容。
同一个桌面版本的 x64、ARM64、deb、AppImage 可以分别发布；重复发布同一平台/架构/包类型/版本会更新对应记录。

桌面版本采用稳定的 `x.y.z`，次版本和修订号小于 100；整数版本号为 `x*10000+y*100+z`。`--version` 会直接写进打包配置，确保客户端读到的版本与发布页面一致。
Android 的整数 versionCode 保持原有规则，并允许手动填写。

## 签名与构建机

桌面客户端内置固定公钥，下载后由 Tauri updater 验证安装包签名及签名中的版本号。
普通文件 hash 不能替代更新签名，管理页面填写的版本名也必须与构建版本一致。

本机私钥保存在 `C:/Users/sunyuanling/.filesync-signing/desktop.key`，公钥在同名 `.pub`；两者不可重新生成覆盖，除非准备重新分发包含新公钥的首版安装包。
私钥不在仓库中。其他构建机使用同一私钥，通过 `FILESYNC_SIGNING_KEY_PATH` 指定文件，或设置 `TAURI_SIGNING_PRIVATE_KEY`；有密码时设置 `TAURI_SIGNING_PRIVATE_KEY_PASSWORD`。

CI 的同名 secrets 配置好后，现有 desktop-build workflow 会输出安装包和 `.sig`；未配置时仅输出普通安装包，不能用于应用内更新。
推 `desktop-v0.1.1` 标签或在手动 workflow 中填写版本号，会把对应版本写入产物；workflow 仍只保存 artifacts，不会自动公开发布。
Rust 工具链固定为桌面项目内的 1.90.0，不修改全局默认版本。

## 客户端行为

- 登录/验证成功后检查更新；收到本平台、架构和包类型的 WS `app_update` 时再次检查。
- 个人中心有「检查桌面端更新」入口。
- 下载与安装分开：显示下载进度 → 验证签名 → 用户点击「安装并重启」。
- 还有进行中或暂停的上传、下载或同步上传时拒绝安装；先结束或取消传输。
- Windows 由 NSIS 安装器升级并重新启动，系统级安装可能需要管理员确认；Linux deb 由 updater 调用包管理工具并申请权限，AppImage 替换后重启。
- 主窗口关闭再打开时，更新状态和已下载的包仍由 Rust 进程保存；完全退出应用后重新检查/下载。

首个包含更新功能的桌面版本需要手动安装一次，此后可应用内更新。不要在测试中调用真实安装命令去升级开发环境。

## 服务端

AppRelease 新增 `arch`、`bundle_type`、`signature`，由现有 AutoMigrate 添加列；旧 Android 记录使用空架构/包类型。
桌面检查接口 `/v1/update/desktop` 返回 Tauri 原始 manifest 或 204，无业务信封。
安装包接口 `/v1/update/package/:id` 使用原有登录/设备鉴权、限制发布路径，并支持 HTTP 文件下载。
客户端将下载 URL 固定到当前配置的服务器入口，保留反代 `/file` 前缀和 `:8443` 端口。

后端源码在 VM `/mnt/data/project/new_server` 修改和验证，部署使用既有 `deploy.sh`。数据库新增列和新接口要在部署后才生效。
官方 updater 机制：https://v2.tauri.app/plugin/updater/
