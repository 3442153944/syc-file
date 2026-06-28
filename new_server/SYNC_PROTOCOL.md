# 文件同步前后端接口规约（Go 后端 ↔ Rust 桌面端 / Android）

> 本文档为实时同步基座的对接契约。Go 后端为编排方与权威 trunk，客户端为探测+执行方。
> 字段统一 snake_case（JSON）。所有时间戳为 Unix 秒。文件 hash 为 sha256 hex（小写）。
> 响应信封统一 `{code,message,data}`，`code==200` 成功。

---

## 0. 设计模型（git 简化版）

二进制文件（Word/Excel 等）无法内容合并，因此本系统借鉴 git 但简化为：
**单一线性 trunk（服务端 File 表）+ 乐观并发（base 版本 CAS）+ 冲突保留两者（本地隔离）。**

核心不变量：

1. **服务端 `file` 表是唯一权威 trunk**，每个路径维护单调递增 `version` 与当前 `file_hash`；历史进 `file_version` 表。
2. **乐观并发**：客户端上报变更时必须带 `base_hash`（它修改前看到的服务端版本）。服务端做 CAS：
   - `base_hash == 服务端当前 hash` → **快进**，接受，version+1，写入历史。
   - 否则 → **冲突**，服务端不动 trunk，记录 `sync_conflict` 待办并通知源设备。
3. **冲突保留两者，且冲突产物留在本地**：出冲突的设备把自己的分叉版本拷进本地隔离目录 `.syncpending/`，主目录收敛为服务端版本，挂一条待办。冲突副本**不进 trunk、不广播**给其它设备（与 Dropbox 不同，避免污染所有端）。
4. **原子发布**：下载来的新版先落本地临时目录 `.synctmp/`，校验 hash 后**原子 rename** 到主目录。用户永远打不开半截文件。
5. **被占用即延迟**：目标文件被本地程序（如 Word）独占锁定无法写入时，任务转 `waiting_unlock` 非终态，长期重试但**不消耗重试次数**，绝不强盖打开中的文档。

### 客户端两个本地目录（均不参与同步，必须在忽略名单内）

| 目录 | 用途 | 生命周期 |
|---|---|---|
| `.synctmp/` | 下载落盘暂存，校验后原子 rename 到主目录 | 瞬时，用完即删 |
| `.syncpending/` | 冲突时本地分叉版本隔离 + 待办源数据 | 持久，待用户处理后删 |

> ⚠ 这两个目录若放在同步文件夹内会被 watcher 探测并递归同步。**必须排除**：放同步根之外，或写入 `SyncFolder.excludes`。

---

## 1. 同步流程总览

### 1.1 正常双向同步（快进）
```
设备A本地文件变化（已稳定，非保存中间态）
  → [A] 上传字节   POST /v1/file/upload (multipart)
  → [A] 上报变更   WS file_changed(file_hash=新, base_hash=A改前看到的服务端版本)
  → [Go] CAS：base_hash == trunk 当前 hash ?
         是 → 接受：更新 file.hash/version，写 file_version 历史
         否 → 转 §1.3 冲突
  → [Go] 给A的其它在线设备生成 pending SyncTask(download)
  → [Go] worker 取队 → 目标在线 + 抢文件锁 → WS 推 task_created
  → [B] 原子下载：写 .synctmp → 校验 hash → rename 到主目录
  → [B] WS task_completed(file_hash)
  → [Go] 标记 completed，释放文件锁
```

### 1.2 离线重连补齐
```
设备B重连上线
  → [B] 对每个 folder 全量扫描本地 → WS scan_result(items[])
  → [Go] 与 trunk 比对：
         - trunk 有、本地无            → 给B派 download/mkdir
         - trunk 有、本地 hash 不同      → 给B派 download（trunk 为准）
         - trunk 无、本地有            → 给B派 delete（trunk 已删）
  → [Go] 同时把 B 离线期间积压的 pending/waiting_unlock 任务派发
```

### 1.3 冲突处理（base CAS 失败）
```
设备A上报 file_changed(file_hash=A, base_hash=X)，trunk 当前 hash=H 且 H≠X
  → [Go] 判冲突：不动 trunk
  → [Go] 写 sync_conflict 待办（server_hash=H, local_hash=A, base_hash=X, server_version）
  → [Go] WS 推 conflict 给设备A（带 conflict_id、server_hash、server_version）
  → [A] 把本地分叉文件拷入 .syncpending/，主目录收敛为 trunk 版本（下载 H）
  → [A] 在「同步待办」列表看到该冲突，二选一：
         · accept_server：丢弃 .syncpending 副本（主目录已是服务端版）
         · keep_local   ：以「当前 trunk hash」为 base 重新上传隔离副本 → 成为新 trunk 版本，广播各端
```

### 1.4 文件被占用（apply 阶段本地锁）
```
[Go] 推 task_created(download) 给设备B
  → [B] 目标文件被 Word 独占，rename 盖不上去
  → [B] WS task_blocked(task_id, reason="locked")
  → [Go] 任务转 waiting_unlock（释放文件锁，retry_count 不变）
  → [B] 检测到文件释放（~$锁文件消失/可独占打开）后自行重试或等 Reaper 重新派发
```

---

## 2. WebSocket 连接

### 2.1 连接端点
```
GET /v1/ws/connect
Query: token=<jwt>  device_id=<稳定设备ID>  device_type=desktop
       device_name=<名称>  platform=windows  app_version=<v>
Header: Token: <jwt>   (可选，query token 为主)
```
**客户端必须传稳定 `device_id`**（如 机器名+用户名 sha256[:16]），用于任务定向派发。缺 device_id 的连接收不到 task_created。

### 2.2 消息信封
```json
{"id":"<uuid>","type":"file_sync","content":{"...":"..."},"timestamp":1700000000}
```
`type=="file_sync"` 走同步协议，`content` 内必有 `event` 字段；其它 type 忽略。

---

## 3. WS 同步事件协议

### 3.1 C→S 客户端上报

#### 3.1.1 file_changed（单文件变更，必须在上传成功后）
```json
{
  "event": "file_changed",
  "folder_id": 1,
  "relative_path": "sub/a.txt",
  "file_name": "a.txt",
  "action": "create",
  "file_size": 1024,
  "file_hash": "9f86...a08",
  "base_hash": "3bf4...015",
  "is_dir": false,
  "mtime": 1700000000
}
```
| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| folder_id | uint64 | 是 | 服务端 SyncFolder.id |
| relative_path | string | 是 | 相对 folder 远端根，`/` 分隔，禁 `..` |
| file_name | string | 是 | 文件名 |
| action | string | 是 | `create`/`modify`/`delete` |
| file_size | int64 | 否 | 字节数（dir 可省） |
| file_hash | string | 否 | 新内容 sha256（dir/delete 可省） |
| **base_hash** | string | modify 必填 | **修改前客户端看到的服务端版本 hash**；create 留空；空+服务端已有不同版本→判冲突 |
| is_dir | bool | 否 | 目录标记 |
| mtime | int64 | 否 | 本地修改时间 |

时序约束：`action=create/modify` 必须**先 POST /file/upload 成功再发此事件**。

#### 3.1.2 scan_result（全量清单）
```json
{"event":"scan_result","folder_id":1,"items":[
  {"relative_path":"a.txt","file_name":"a.txt","file_size":1024,"file_hash":"<sha256>","is_dir":false,"mtime":1700000000}
]}
```

#### 3.1.3 task_progress / task_completed / task_failed
```json
{"event":"task_progress","task_id":1,"progress":50,"bytes":512}
{"event":"task_completed","task_id":1,"file_hash":"<sha256>"}
{"event":"task_failed","task_id":1,"error":"..."}
```

#### 3.1.4 task_blocked（目标文件被占用，转 waiting_unlock）
```json
{"event":"task_blocked","task_id":1,"reason":"locked"}
```
与 task_failed 区别：**不计入重试次数**，长期等待文件释放。

### 3.2 S→C 服务端派发

#### 3.2.1 task_created
```json
{
  "event":"task_created","task_id":1,"task_type":"download","direction":"download",
  "folder_id":1,"relative_path":"sub/a.txt","file_name":"a.txt","file_size":1024,
  "file_hash":"<sha256>",
  "remote_path":"E:\\FileSync\\myfolder\\sub\\a.txt",
  "remote_dir":"E:\\FileSync\\myfolder\\sub"
}
```
| task_type | 设备执行动作 |
|---|---|
| `download` | 写 `.synctmp` → 校验 hash → 原子 rename 到本地映射目录；被占用则回 task_blocked |
| `delete` | 删除本地对应 relative_path 文件 |
| `mkdir` | 建对应目录 |

执行完回 task_completed/task_failed/task_blocked。download 完成回传落盘 hash 供校验。

#### 3.2.2 conflict（冲突通知，要求隔离本地副本）
```json
{
  "event":"conflict","conflict_id":7,"folder_id":1,
  "relative_path":"sub/a.txt","file_name":"a.txt",
  "server_hash":"<trunk当前hash>","server_version":5,
  "base_hash":"<客户端上报的base>","local_hash":"<客户端本地hash>"
}
```
客户端：把本地分叉拷入 `.syncpending/`，主目录收敛 server_hash，记录待办（引用 conflict_id）。

#### 3.2.3 conflict_resolved（服务端确认待办处理结果）
```json
{"event":"conflict_resolved","conflict_id":7,"resolution":"keep_local","server_hash":"<当前trunk hash>"}
```
| resolution | 客户端动作 |
|---|---|
| `accept_server` | 丢弃 `.syncpending` 副本（主目录已是服务端版） |
| `keep_local` | 以 `server_hash` 为 base，重新上传 `.syncpending` 副本 + file_changed → 成为新 trunk 版本 |

---

## 4. REST 接口（/v1/sync/*，需 Token 头）

| 方法 | 路径 | 入参 | 返回 data |
|---|---|---|---|
| POST | /folders | `{name,local_path,remote_path,direction,owner_device_id}` | SyncFolder |
| GET | /folders | — | `[SyncFolder]` |
| PUT | /folders/:id | `{enabled?,direction?,name?,excludes?}` | nil |
| DELETE | /folders/:id | — | nil |
| POST | /notify | file_changed 字段 + `device_id` | nil（WS 不可用时的 HTTP 回退） |
| POST | /scan | `{device_id,folder_id,items:[ScanItem]}` | nil |
| GET | /tasks | `?status=&device_id=&limit=` | `[SyncTask]` |
| GET | /tasks/pending | `?device_id=` | `[SyncTask]` |
| POST | /tasks/:id/complete | `{file_hash}` | nil |
| POST | /tasks/:id/failed | `{error}` | nil |
| POST | /tasks/:id/blocked | `{reason}` | nil（转 waiting_unlock） |
| GET | /conflicts | — | `[SyncConflict]`（status=pending 的待办） |
| POST | /conflicts/:id/resolve | `{resolution:"accept_server"\|"keep_local"}` | nil |
| DELETE | /conflicts/:id | — | nil（清理残留记录） |

`direction`: `two_way`(默认) / `upload_only` / `download_only`。

SyncFolder：
```json
{"id":1,"user_id":1,"name":"我的文档","local_path":"C:/Users/me/Documents",
 "remote_path":"E:/FileSync/docs","direction":"two_way","enabled":true,
 "excludes":".synctmp/\n.syncpending/","owner_device_id":"abc123",
 "created_at":"...","updated_at":"..."}
```

SyncConflict：
```json
{"id":7,"user_id":1,"device_id":"abc123","folder_id":1,"file_id":42,
 "relative_path":"sub/a.txt","file_name":"a.txt",
 "server_hash":"<H>","local_hash":"<A>","base_hash":"<X>","server_version":5,
 "status":"pending","resolution":null,"created_at":"...","resolved_at":null}
```

---

## 5. 传输接口复用（不变）

- 上传：`POST /v1/file/upload` multipart `path=远端目录&name=文件名&action=upload` + file part
- 下载：`GET /v1/file/download?path=远端目录&name=文件名&device_id=&token=` 支持 Range

---

## 6. 客户端侧改动清单（待接）

1. **生成稳定 device_id**：机器标识 → sha256[:16]，WS 连接带上。
2. **上传后上报 file_changed**：上传前算 sha256，带 `base_hash`（修改前的服务端版本 hash，由上次同步记录得知）。
3. **原子发布**：下载写 `.synctmp` → 校验 → rename；被占用回 `task_blocked`。
4. **冲突处理**：收 conflict → 隔离到 `.syncpending` + 主目录收敛 + 记待办；处理时调 `/conflicts/:id/resolve`。
5. **稳定窗口 + 忽略 Office 锁文件**：Word 等保存是「写临时文件 + rename + `~$` 锁文件」，watcher 探测后须**等文件稳定**（size/mtime 连续 N 秒不变 / `~$*` 消失）再算 hash，避免抓到中间态。
6. **忽略目录**：`.synctmp/`、`.syncpending/`、`~$*` 不上报。
7. **hash 依赖**：Rust 端 `sha2 = "0.10"`。

---

## 7. Go 后端实现位置（模块化）

| 能力 | 位置 |
|---|---|
| WS 事件常量/注入 | `internal/ws/sync_events.go` |
| 引擎核心 + WS 分发 | `internal/sync/engine.go` |
| 文件变更处理 + CAS + 派发 | `internal/sync/filechange.go` |
| 冲突检测/记录/解决 | `internal/sync/conflict.go` |
| 离线扫描比对 | `internal/sync/scan.go` |
| 文件夹 CRUD | `internal/sync/folder.go` |
| 任务回调/查询/进度/blocked | `internal/sync/task.go` |
| Worker 调度 + Reaper + 锁 | `internal/sync/worker.go` |
| 版本历史写入 | `internal/sync/version.go` |
| Redis 队列/锁(令牌)/进度 | `pkg/sync_store/sync_store.go` |
| 模型 | `internal/model/{SyncTask,SyncFolder,SyncConflict,File,FileVersion}.go` |
