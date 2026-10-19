# Windows 内嵌隧道依赖

把官方的 `wireguard.exe`（WireGuard for Windows 安装目录里的那个）放到本目录：

```
src-tauri/resources/wireguard/wireguard.exe
```

打包时会随应用分发；运行时 `vpn_up_embedded` 依次查找：

1. `<resource_dir>/wireguard/wireguard.exe`
2. `<exe 所在目录>/wireguard/wireguard.exe`
3. `C:\Program Files\WireGuard\wireguard.exe`

找不到会返回明确错误，不会静默失败。

获取方式：
- 安装 WireGuard for Windows 后从安装目录复制；
- 或在 Linux 上用 `msiextract` 解包官方 MSI（`tools/fetch-wireguard-windows.sh`）。
