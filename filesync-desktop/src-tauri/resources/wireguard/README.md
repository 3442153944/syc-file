# Windows 内嵌隧道依赖

**当前固定版本：WireGuard for Windows 1.1.1**，`wireguard.exe` 已随仓库提交（9.0 MB，
sha256 `04c71fb1…`），不需要每次打包重新下载。版本与校验值见同目录 `VERSION`。

如需更换版本：改 `tools/fetch-wireguard-windows.sh` 里的 `VERSION` 与两个 `SHA256`，
再执行 `bash tools/fetch-wireguard-windows.sh --force`，并同步更新 `VERSION`。

文件位置：

```
src-tauri/resources/wireguard/wireguard.exe
```

打包时会随应用分发；运行时 `vpn_up_embedded` 依次查找：

1. `<resource_dir>/resources/wireguard/wireguard.exe`（安装包布局；Windows 上 `resource_dir` 即安装目录）
2. `<resource_dir>/wireguard/wireguard.exe`（手工绿色包/旧布局兼容）
3. `<exe 所在目录>/resources/wireguard/wireguard.exe`
4. 开发模式（仅 debug 构建）：仓库 `src-tauri/resources/wireguard/wireguard.exe`

找不到会返回明确错误并列出实际查找过的路径，不会静默失败。

获取方式：
- 安装 WireGuard for Windows 后从安装目录复制；
- 或在 Linux 上用 `msiextract` 解包官方 MSI（`tools/fetch-wireguard-windows.sh`）。
