# Windows 一键构建/部署

四个动作，全部通过仓库根 `deploy.ps1` 或直接调用子脚本：

```powershell
# 一键部署后端（构建 sys_core + Go 后端，生成 Windows 配置，启动 8991；nginx 已反代）
powershell -NoProfile -File deploy.ps1 -Target backend -Force

# 一键部署 web（默认发布 filesync-desktop\dist 到 E:\fileSynchronization\web）
powershell -NoProfile -File deploy.ps1 -Target web -Build

# 一键打包 Android
powershell -NoProfile -File deploy.ps1 -Target android

# 一键打包 HarmonyOS
powershell -NoProfile -File deploy.ps1 -Target harmony
```

也可以直接调用：

```powershell
powershell -NoProfile -File new_server\deploy-win.ps1 -SkipBuild -NoSupervisor -Force
powershell -NoProfile -File new_server\deploy-web.ps1 -DistDir filesync-desktop\dist
powershell -NoProfile -File Android\build.ps1 -NativeOnly
powershell -NoProfile -File harmony\build.ps1 -Hap
```

## 后端前置条件

- Go、MinGW gcc、cargo 在 PATH（脚本会自己补 Machine/User PATH 和 `~/.cargo/bin`）。
- 本机 MySQL / Redis / nginx 已由宝塔安装；redis 服务未启动时脚本会尝试拉起。
- `ssh vm` 免密，且生产配置在 `/mnt/data/project/new_server/config/config.yaml`；
  也可用 `-ConfigPath` 指定本地配置。
- 管理员权限用于启动 redis 服务；普通会话下脚本会尝试直接启动 `redis-server.exe`。
- 数据库已经迁移过（生产 `syncfile` 和 `syncfile-dev` 都在本机 MySQL），脚本不再处理数据库。

## 后端参数

| 参数 | 说明 |
| --- | --- |
| `-InstallDir` | 安装目录，默认 `E:\fileSynchronization\syc-file-win` |
| `-Port` | 后端端口，默认 8991 |
| `-SkipBuild` | 跳过构建，直接用安装目录里的 `syc-file.exe` |
| `-Force` | 端口被旧 `syc-file` 进程占用时自动停掉 |
| `-NoSupervisor` | 生成配置时关闭 supervisor（本地首验不抢生产 frpc 隧道） |
| `-ConfigPath` | 用本地配置做源（默认 ssh vm 取生产 config） |
| `-SkipTools` | 跳过 frpc/ddns-go 存在性检查 |

配置生成时会保留生产 `auth.secret` / 数据库密码（不打印），把 db/redis 指向 `127.0.0.1`、
文件路径改成 Windows 盘符、supervisor 指向本机 frpc/ddns-go，并删除 vpn 段。

## web 参数

| 参数 | 说明 |
| --- | --- |
| `-DistDir` | 构建产物目录，默认自动探测 `filesync-desktop\dist` |
| `-WebRoot` | 发布目录，默认 `E:\fileSynchronization\web` |
| `-Build` | 先在 filesync-desktop 执行 `pnpm build` |
| `-Force` | 跳过“目录非空时备份”的提示（仍会备份） |

部署前会备份现有 web 到 `E:\fileSynchronization\backup\web-<时间戳>`，失败自动回滚。

## 说明

- 证书续签/推送由现有 ACME（Windows win-acme / Linux acme.sh）负责，不在本目录脚本内。
- 生产库里有部分历史文件 `original_path` 是 Linux 路径（`/mnt/data/...`），只迁移数据库时这些旧文件的下载/同步会失效；新上传不受影响。
- 域名/nginx 已由宝塔站点配置（`www.sunyuanling.com.conf` 反代 `localhost:8991`），脚本只打印提示，不修改。

## 数据目录

生产数据已迁移到 `F:\file_sync`（uploads/versions/quickshare/share_temp/sync/web 等），数据库里的历史路径也已改写成 `F:/file_sync/...`；脚本生成配置时默认使用该目录，重新部署不会回退。
