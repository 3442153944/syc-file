# 文件同步前后端接口规约（Go 后端 ↔ Rust 桌面端）

> 本文档为实时同步基座的对接契约。Go 后端为编排方，Rust 桌面端为探测+执行方。
> 字段统一 snake_case（JSON）。所有时间戳为 Unix 秒。文件 hash 为 sha256 hex（小写）。
> 响应信封统一 `{code,message,data}`，`code==200` 成功。

---

## 1. 同步流程总览

### 1.1 正常双向同步时序
```
设备A本地文件变化
  → [A] 上传字节到服务端  POST /v1/file/upload  (multipart)
  → [A] 上报变更到服务端  WS file_changed (带 sha256)   ← 必须在上传成功后
  → [Go] 登记/更新 File 元数据 + hash
  → [Go] 给设备A的其它在线设备生成 pending SyncTask(download)
  → [Go] worker 取队 → 设备B在线 → WS 推 task_created 给设备B
  → [B] 执行  GET /v1/file/download (Range)  落到本地映射目录
  → [B] 回调 WS task_completed(file_hash)
  → [Go] 标记 completed，释放文件锁
```

### 1.2 离线重连补齐
```
设备B重连上线
  → [B] 对每个 folder 全量扫描本地 → WS scan_result(items[])
  → [Go] 与服务端 File 元数据比对：
         - 服务端有、本地无  → 给B派 download task
         - 服务端有、本地有但 hash 不同 → 给B派 download task（服务端为准）
         - 服务端无、本地有  → 给B派 delete task（让B删本地多余文件？否，应反向：B上报 file_changed 让服务端补）
  → [Go] 同时把 B 离线期间积压的 pending task 派发
```

### 1.3 冲突处理
```
设备A上报 file_changed(hash=A)，服务端 File 当前 hash=B（B≠A 且都非空）
  → [Go] 判定冲突：不覆盖服务端文件
  → [Go] WS 推 conflict 给设备A
  → [A] 把本地文件改名加 .conflict.<ts> 后缀，重新上传+上报 file_changed
  → 后续可作为残留清理（DELETE /sync/conflicts/:id）
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

**Rust 必须传稳定的 `device_id`**（如 机器名+用户名 sha256[:16]），用于任务定向派发。
缺 device_id 的连接无法接收 task_created。

### 2.2 消息信封
```json
{"id":"<uuid>","type":"file_sync","content":{...},"timestamp":1700000000}
```
Rust 只需解析 `type` 与 `content`。`type=="file_sync"` 走同步协议，其它忽略。

---

## 3. WS 同步事件协议（content 内必有 `event` 字段）

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
  "file_hash": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
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
| file_hash | string | 否 | sha256 hex（dir 可省；delete 可省） |
| is_dir | bool | 否 | 目录标记 |
| mtime | int64 | 否 | 本地修改时间 |

时序约束：`action=create/modify` 时，**必须先 POST /file/upload 成功再发此事件**，否则目标设备下载会拿不到文件。

#### 3.1.2 scan_result（全量清单）
```json
{"event":"scan_result","folder_id":1,"items":[
  {"relative_path":"a.txt","file_name":"a.txt","file_size":1024,"file_hash":"<sha256>","is_dir":false,"mtime":1700000000}
]}
```

#### 3.1.3 task_progress（任务进度，可选）
```json
{"event":"task_progress","task_id":1,"progress":50,"bytes":512}
```

#### 3.1.4 task_completed（任务完成）
```json
{"event":"task_completed","task_id":1,"file_hash":"<sha256>"}
```

#### 3.1.5 task_failed（任务失败）
```json
{"event":"task_failed","task_id":1,"error":"..."}
```

### 3.2 S→C 服务端派发

#### 3.2.1 task_created（要求设备执行任务）
```json
{
  "event": "task_created",
  "task_id": 1,
  "task_type": "download",
  "direction": "download",
  "folder_id": 1,
  "relative_path": "sub/a.txt",
  "file_name": "a.txt",
  "file_size": 1024,
  "file_hash": "<sha256>",
  "remote_path": "E:\\FileSync\\myfolder\\sub\\a.txt",
  "remote_dir": "E:\\FileSync\\myfolder\\sub"
}
```
| task_type | 设备执行动作 |
|---|---|
| `download` | GET /v1/file/download?path=<remote_dir>&name=<file_name>&token= ，落到本地映射目录 |
| `delete` | 删除本地映射目录下对应 relative_path 的文件 |
| `mkdir` | 在本地映射目录建对应 relative_path 目录 |

执行完回调 task_completed/task_failed。download 完成时回传本地落盘后的 file_hash 供服务端校验。

#### 3.2.2 conflict（冲突通知）
```json
{
  "event":"conflict",
  "folder_id":1,
  "relative_path":"sub/a.txt",
  "file_name":"a.txt",
  "server_hash":"<sha256>",
  "local_hash":"<sha256>"
}
```

---

## 4. REST 接口（/v1/sync/*，需 Token 头）

| 方法 | 路径 | 入参 | 返回 data |
|---|---|---|---|
| POST | /folders | `{name,local_path,remote_path,direction,owner_device_id}` | SyncFolder |
| GET | /folders | — | `[SyncFolder]` |
| PUT | /folders/:id | `{enabled?,direction?,name?}` | nil |
| DELETE | /folders/:id | — | nil |
| POST | /notify | `{device_id, folder_id, relative_path, file_name, action, file_size, file_hash, is_dir, mtime}` | nil |
| POST | /scan | `{device_id, folder_id, items:[ScanItem]}` | nil |
| GET | /tasks | `?status=&device_id=&limit=` | `[SyncTask]` |
| GET | /tasks/pending | `?device_id=` | `[SyncTask]` |
| POST | /tasks/:id/complete | `{file_hash}` | nil |
| POST | /tasks/:id/failed | `{error}` | nil |
| GET | /conflicts | — | `[SyncTask]` |
| DELETE | /conflicts/:id | — | nil |

`/notify` 与 `/scan` 是 WS 不可用时的 HTTP 回退，字段同 3.1.1/3.1.2。
`direction`: `two_way`(默认) / `upload_only` / `download_only`（鸿蒙等只下载客户端用 download_only）。

SyncFolder 结构：
```json
{"id":1,"user_id":1,"name":"我的文档","local_path":"C:/Users/me/Documents",
 "remote_path":"E:/FileSync/docs","direction":"two_way","enabled":true,
 "owner_device_id":"abc123","created_at":"...","updated_at":"..."}
```

---

## 5. 传输接口复用（不变）

- 上传：`POST /v1/file/upload` multipart `path=远端目录&name=文件名&action=upload` + file part
- 下载：`GET /v1/file/download?path=远端目录&name=文件名&device_id=&token=` 支持 Range

---

## 6. Rust 侧最小改动清单（前期测试对接）

1. **sync_config.rs**：`FolderMapping` 加 `server_folder_id: u64` 字段；启动时 POST `/sync/folders` 注册拿 id 回填（或 GET 匹配 remote_path）。
2. **生成稳定 device_id**：机器标识 → sha256[:16]，存入 SyncConfig，WS 连接带上。
3. **upload_worker.rs**：上传成功后**追加** `file_changed` 上报（WS 或 POST /sync/notify）；上传前算 sha256。
4. **sync_engine.rs**：`remove` 事件不再 skip，改为上报 `file_changed(action=delete)`（不需上传）。
5. **ws_client.rs**：连接 URL 加 `device_id`；扩展 `FileSyncContent` 解析 `event/task_id/task_type/remote_dir/relative_path/file_name/file_hash`；按 task_type 执行 download/delete/mkdir；完成后回 `task_completed`/`task_failed`。
6. **hash 依赖**：Cargo.toml 加 `sha2 = "0.10"`。
7. **路径穿越防护**：Rust 侧 strip_prefix 后 join 本地路径时，丢弃 `..` 段。

---

## 7. Go 后端已实现位置

| 能力 | 位置 |
|---|---|
| WS 事件路由 | `internal/ws/sync_events.go` + `init.go` |
| 同步引擎 | `internal/sync/engine.go` |
| Worker 调度 | `internal/sync/worker.go` |
| 文件夹/任务/冲突操作 | `internal/sync/operations.go` |
| REST handler | `internal/sync/handler.go` + `router.go` |
| Redis 队列/锁/进度 | `pkg/sync_store/sync_store.go` |
| 模型 | `internal/model/SyncTask.go` / `SyncFolder.go` |

