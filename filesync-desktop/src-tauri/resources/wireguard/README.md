# Windows 内嵌隧道依赖

`wg.exe` 随包提供握手查询与 `syncconf`，无需依赖用户系统安装目录。
当前工具为官方签名有效的 wireguard-tools v1.0.20260223，SHA256
`37ddc3a1a456de26c8927f25001a86518c5027dcb6eb91bb77780fcf7ca9eb17`。
本次从本机已有的 WireGuard LLC 签名工具复制并核验；获取脚本也会从已校验的官方 MSI 解出配套 `wg.exe`。

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
