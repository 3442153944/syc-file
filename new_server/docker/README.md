# 云梯后端 Docker 镜像使用说明

一体镜像：Go 后端 + MySQL 8.0 + Redis + supervisord + ffmpeg（缩略图），一个容器跑完。
所有数据（数据库、文件、版本库、配置、密钥）都在 `/data` 数据卷里，删除/重建容器不丢数据。

---

## 1. 构建

```bash
bash docker/build.sh                          # 默认 filesync-server:dev
bash docker/build.sh filesync-server:1.0.0    # 指定 镜像名:标签
```

- 构建上下文固定为 `new_server/`；首次构建要编译 Rust 核心 + Go 并安装 MySQL/ffmpeg，需几分钟。
- 国内默认走 `goproxy.cn` / `golang.google.cn`；境外构建可覆盖：
  `GOPROXY=https://proxy.golang.org,direct GO_DL=https://go.dev/dl bash docker/build.sh ...`
- VS Code 里有对应运行配置「构建: 后端 Docker 镜像」。

## 2. 运行

```bash
docker run -d --name filesync --restart unless-stopped \
  -p 9999:9999 \
  -v filesync-data:/data \
  filesync-server:1.0.0
```

- `-v filesync-data:/data` 必须挂：数据全在卷里；没挂卷容器删除即丢，启动日志会警告。
- 首次启动自动完成（约 20~40 秒）：
  - 生成随机 MySQL/Redis 密码 → `/data/config/secrets.env`
  - 生成默认配置 → `/data/config/config.yaml`
  - 初始化内置 MySQL（建库、建应用账号）
  - 应用代码：`docker volume inspect filesync-data --format '{{.Mountpoint}}'`
- 用 `docker inspect --format '{{.State.Health.Status}}' filesync` 直到 `healthy`。

### 首次初始化（超级管理员）

未初始化时启动日志会打印一次性初始化码（防抢注，连续输错 5 次锁 60 秒）：

```bash
docker logs filesync 2>&1 | grep setup_code
```

也可以用环境变量预指定（仅首次启动前有效）：

```bash
docker run -d ... -e SYC_SETUP_CODE=MY-CODE-1234 filesync-server:1.0.0
```

拿到初始化码后，在客户端/网页的「初始化向导」里输入并创建超级管理员。

## 3. 配置

- 配置都在数据卷里：编辑 `<卷>/config/config.yaml`，然后 `docker restart filesync`。
- 常见修改：
  - `server.port`：容器内服务端口（改完 `-p` 映射要对应调整）
  - `server.name`：节点名（`/v1/ping` 回给客户端的名字）
  - `thumbnail.workers`：缩略图并发（默认 8，小内存机器调低）
- 挂载额外磁盘：`-v /mnt/disk1:/mnt/disk1`，再把 `/mnt/disk1` 追加进 `allowed_paths`，重启容器。
- 时区默认 `Asia/Shanghai`，可 `-e TZ=Asia/Tokyo` 覆盖。
- **不要手改数据库/Redis 密码**：`secrets.env` 与库内账号是配套生成的。

## 4. 数据卷目录结构

```
/data
├── config/
│   ├── config.yaml       后端配置（0600）
│   └── secrets.env       随机凭据（0600）
├── mysql/                内置 MySQL 数据
├── redis/                Redis 持久化（AOF）
├── log/                  后端日志（按大小轮转）
├── static/avatar/        头像
└── file_sync/            文件存储
    ├── sync/             同步目录
    ├── versions/         文件版本库
    ├── quickshare/       粘贴快传
    ├── share_temp/       分享链接硬链接
    ├── temp/             上传临时文件 + 临时缩略图
    └── thumbs/           缩略图缓存
```

## 5. 日志与健康检查

- `docker logs -f filesync`：MySQL、Redis、后端输出都汇总到这里；`[init]` 前缀是入口初始化脚本。
- 镜像内健康检查每 30s 请求一次 `http://127.0.0.1:<server.port>/v1/ping`，从 `config.yaml` 读端口。
- 后端另有文件日志在卷内 `/data/log/`。

## 6. 备份（重要）

数据全在 `/data`，**备份 = 备份这个卷**。MySQL 运行中直接打包可能拿到不一致的库文件，推荐冷备：

```bash
docker stop filesync
docker run --rm -v filesync-data:/data -v /backup:/backup alpine \
  tar czf /backup/filesync-data-$(date +%F).tar.gz -C /data .
docker start filesync
```

- 体积主要来自 `file_sync/`（文件 + 版本库），会随使用增长；大文件多时可以单独 rsync 卷里的
  `file_sync/`，数据库部分只做 mysqldump（容器内 `mysqldump`，密码见 `secrets.env`）。
- 建议备份到**另一台机器**：本地同盘备份挡不住硬盘故障。

## 7. 升级 / 回滚

```bash
docker stop filesync && docker rm filesync        # 数据卷不受影响
docker run -d --name filesync --restart unless-stopped \
  -p 9999:9999 -v filesync-data:/data filesync-server:1.0.1
```

- 数据库表结构由后端启动时 AutoMigrate 自动升级，无需手工迁移。
- **升级前先做一次第 6 节的备份**。
- 回滚：换回旧镜像 tag 按同样命令重跑即可。

## 8. 对外发布安全

- 内置 MySQL/Redis 只监听容器内 `127.0.0.1`，不对外暴露；对外只有你 `-p` 的端口。
- 公网部署建议前置 nginx/caddy 做 TLS 终止，客户端支持 https，不要明文裸奔。
- 首次初始化完成前不要长时间暴露在公网（初始化码是唯一防线），初始化完再开放更稳妥。

## 9. 排障

| 现象 | 排查 |
| --- | --- |
| 启动后一直 unhealthy | `docker logs filesync`，看 `[init]` 走到哪一步；MySQL 初始化失败会打印原因 |
| 改了配置不生效 | 确认改的是**数据卷里**的 config.yaml（不是仓库里的模板），改完必须 restart |
| 缩略图失败 | `docker exec filesync ffmpeg -version` 应输出版本；调低 `thumbnail.workers` 减少内存压力 |
| 时间不对 | `-e TZ=...` |
| 端口冲突 | `server.port` + `-p` 一起改 |

## 10. 冒烟验证记录（1.0）

- 首次启动 → healthy 约 20 秒；初始化码 / 向导 / 登录正常
- 上传 → 下载 字节级一致；PNG 上传后按需生成 256×192 JPEG 缩略图（ffmpeg 6.1.1）
- WebSocket 升级返回 101
- 容器重启后 10 秒恢复 healthy，登录态与文件均保持
- 空闲资源：约 415MB 内存（主要是 MySQL buffer pool 256M）/ 1.2% CPU
