# Migration Plan: server → new_server

## Status: READY TO EXECUTE

This plan covers the full migration of the old `server/` project (PostgreSQL, Java-style inheritance patterns, AES-256-GCM tokens) to the new `new_server/` project (MySQL, idiomatic Go, JWT auth).

## Execution Checklist

**Approve this plan to exit plan mode and start file creation.**

---

## Phase 1: Database Migration (PostgreSQL → MySQL)

### 1a. Create `sql/init_mysql.sql`
- Convert all 13 tables from PG to MySQL syntax
- SERIAL→INT AUTO_INCREMENT, BIGSERIAL→BIGINT AUTO_INCREMENT, SMALLINT→TINYINT
- TIMESTAMP→DATETIME, remove PG triggers/functions
- Include seed data (dict_type, dict_data, role, permission)

### 1b. Create 11 missing Go models in `internal/model/`:

| File | Table | Key Fields |
|---|---|---|
| `Device.go` | device | ID, UserID, DeviceName, DeviceType, DeviceID, OSVersion, AppVersion, IPAddress, LastActive, Status |
| `FileVersion.go` | file_version | ID, FileID, Version, FileSize, FileHash, StoragePath, CreatedBy |
| `SyncTask.go` | sync_task | ID, UserID, DeviceID, FileID, TaskType, SyncStatus, Progress, ErrorMessage |
| `Permission.go` | permission | ID, PermissionCode, PermissionName, ParentID, PermissionType, Description, SortOrder |
| `Role.go` | role | ID, RoleCode, RoleName, Description, Status |
| `RolePermission.go` | role_permission | ID, RoleID, PermissionID |
| `DictType.go` | dict_type | ID, DictCode, DictName, Description, Status |
| `DictData.go` | dict_data | ID, DictTypeID, DictLabel, DictValue, DictSort, CssClass, TagType, Remark |
| `OperationLog.go` | operation_log | ID, UserID, DeviceID, OperationType, OperationModule, OperationDesc, RequestMethod, RequestURL |
| `StorageConfig.go` | storage_config | ID, UserID, TotalQuota, UsedQuota, FileCount, LastSync |
| `ShareRecord.go` | share_record | ID, UserID, FileID, ShareCode, SharePassword, ExpireTime, DownloadLimit, DownloadCount, VisitCount |

**Pattern** (follow existing `user.go` style): pointer types for nullable, `gorm:"autoCreateTime/autoUpdateTime"` tags, `json` tags, `TableName()` method.

### 1c. Add AutoMigrate in `cmd/main.go`
Add after DB connection:
```go
db.AutoMigrate(
    &model.User{}, &model.Device{}, &model.File{},
    &model.FileVersion{}, &model.SyncTask{},
    &model.UploadHistory{}, &model.DownloadHistory{},
    &model.Permission{}, &model.Role{}, &model.RolePermission{},
    &model.UserRole{}, &model.DictType{}, &model.DictData{},
    &model.OperationLog{}, &model.StorageConfig{}, &model.ShareRecord{},
)
```

---

## Phase 2: Config Enhancement

### 2a. Extend `config/config.go`
Add structs:
```go
type FileConfig struct {
    AllowedPaths []string   `mapstructure:"allowed_paths"`
    Storage      StorageCfg `mapstructure:"storage"`
    Upload       UploadCfg  `mapstructure:"upload"`
}
type StorageCfg struct {
    BasePath   string `mapstructure:"base_path"`
    UploadPath string `mapstructure:"upload_path"`
    TempPath   string `mapstructure:"temp_path"`
    TrashPath  string `mapstructure:"trash_path"`
}
type UploadCfg struct {
    MaxFileSize        int64    `mapstructure:"max_file_size"`
    MaxFilenameLength  int      `mapstructure:"max_filename_length"`
    AllowedExtensions  []string `mapstructure:"allowed_extensions"`
    ForbiddenExtensions []string `mapstructure:"forbidden_extensions"`
}
type UserCfg struct {
    AvatarPath        string   `mapstructure:"avatar_path"`
    AllowedExtensions []string `mapstructure:"allowed_extensions"`
    MaxSize           int64    `mapstructure:"max_size"`
}
```
Add `File FileConfig` and `User UserCfg` fields to `Config` struct.

### 2b. Update `config/config.yaml`
Add file and user config sections with sensible defaults (Windows paths: D:,E:,F:,G: / max upload 10GB / avatar max 10MB).

---

## Phase 3: File Module API

### 3a. Create directory `internal/handler/file/`

### 3b. Create `router.go`
```go
func RegisterFileRouter(rg *gin.RouterGroup, db *gorm.DB, redisClient *redis.Client) {
    f := rg.Group("/file")
    f.POST("/available-disks", HandlerFuncAvailableDisks(db, redisClient))
    f.POST("/traverse-directory", HandlerFuncTraverseDirectory(db, redisClient))
    f.GET("/download", HandlerFuncDownload(db, redisClient))
    f.POST("/upload", HandlerFuncUpload(db, redisClient))
    f.POST("/download-history", HandlerFuncDownloadHistory(db, redisClient))
}
```

### 3c. Create 5 handler files:

**`available_disks.go`** — `POST /v1/file/available-disks`
- Uses `gopsutil/disk` for partition info
- Supports `detailed=true` and `disk_path` params
- Auth required, gets userID from `token.Claims`
- Returns `{code, message, data}` format

**`traverse_directory.go`** — `POST /v1/file/traverse-directory`
- Takes `path` (required), `page`, `page_size` (optional)
- Uses `os.ReadDir` for single-level traversal
- Sorts: directories first, then alphabetically
- Returns paginated or full results

**`download.go`** — `GET /v1/file/download`
- Takes `path`, `name`, `device_id` query params
- Supports Range header (resumable download)
- Creates `DownloadHistory` record
- Sends file via `io.Copy` to `c.Writer`
- Path security check against config

**`upload.go`** — `POST /v1/file/upload`
- Takes JSON body or multipart form: `path`, `name`, `action: check|upload`
- `action=check`: return file existence status
- `action=upload`: save file via `c.FormFile("file")`
- Validates extension, filename length, file size
- Creates `UploadHistory` record
- Path security check

**`download_history.go`** — `POST /v1/file/download-history`
- Takes `pageNum`, `pageSize` in body
- Queries `DownloadHistory` for current user
- Returns paginated list with total count

### 3d. Register in `internal/handler/routers.go`
Add `file.RegisterFileRouter(private, db, redisClient)` in the private group.

---

## Phase 4: User Module Enhancement

### 4a. Create `internal/handler/user/updateUserInfo.go`
- `POST /v1/user/update-info` (RequireAuth)
- Accepts multipart form: `username`, `email`, `phone`, `avatar` (file)
- Avatar: validates size + extension, converts to PNG, saves to `static/avatar/`
- Updates user record in DB

### 4b. Add route in `user/router.go`
```go
u.POST("/update-info", HandlerFuncUpdateUserInfo(db, redisClient))
```

---

## Phase 5: WebSocket Module

### 5a. Create directory `internal/ws/`

### 5b. Migrate files (rewrite to use `token.Claims` instead of `token.TokenPayload`):

| File | Purpose |
|---|---|
| `types.go` | Message/MessageType/Target/DeviceInfo/ConnectionInfo structs, constants, errors |
| `hub.go` | Hub connection pool: register/unregister/broadcast, singleton via sync.Once |
| `connection.go` | Server-side WebSocket connection wrapper, readPump/writePump |
| `handler.go` | HTTP handlers: Connect, SendMessage, Broadcast, GetOnlineUsers, etc. |
| `init.go` | Global helper functions: `SendToUser(userID, evt, data)` for file handler integration |
| `router.go` | `RegisterWSRouter(private, db, redis)` |

### 5c. Key changes from old server:
- `tokenPkg.TokenPayload` → `token.Claims`
- `response.Success/Error()` → `{code, message, data}` JSON
- Remove GORM hooks (`BeforeCreate`, `BeforeUpdate` etc.) from Device model
- Keep `gorilla/websocket` as dependency (add to go.mod)

### 5d. Register WS routes in `routers.go`
Add `ws.RegisterWSRouter(private, db, redisClient)` in the private group.

### 5e. Integrate WS with File handlers
In file upload/download handlers, replace commented WS calls with:
```go
ws.SendToUser(uint(userID), "file_upload", gin.H{...})
```

---

## Phase 6: Verification

1. Run `go mod tidy` to add new dependencies (`gopsutil`, `gorilla/websocket`)
2. Run `go build ./...` to verify compilation
3. Start MySQL, create database `syncfile`, run `init_mysql.sql`
4. Start server and test endpoints

---

## New Dependencies Required

```
github.com/shirou/gopsutil/v3/disk  (already in old server go.sum)
github.com/gorilla/websocket         (already in old server go.sum)
```

## Files to Create (total: ~18)

```
sql/init_mysql.sql
internal/model/{Device,FileVersion,SyncTask,Permission,Role,RolePermission,DictType,DictData,OperationLog,StorageConfig,ShareRecord}.go
internal/handler/file/{router,available_disks,traverse_directory,download,upload,download_history}.go
internal/handler/user/updateUserInfo.go
internal/ws/{types,hub,connection,handler,init,router}.go
```

## Files to Modify (total: ~4)

```
cmd/main.go            — add AutoMigrate
config/config.go       — add FileConfig/UserCfg structs
config/config.yaml     — add file/user config sections
internal/handler/routers.go — register file + ws routes
```

---

**READY**: Approve this plan to start implementing.
