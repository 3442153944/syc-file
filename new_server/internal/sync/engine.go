package sync

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/ws"
	"syc-file/pkg/logger"
	"syc-file/pkg/sync_store"
)

// SourceServer 表示「服务端发起」的任务来源（离线扫描补齐、冲突解决补派等）。
const SourceServer = "server"

// Engine 同步引擎：接收客户端上报 → 维护 trunk → 编排任务派发给目标设备。
type Engine struct {
	db    *gorm.DB
	rdb   *redis.Client
	store *sync_store.SyncStore
	cfg   config.SyncConfig
	hub   *ws.Hub
}

var Global *Engine

// InitSync 初始化引擎、注入 WS 同步消息处理器，并启动 worker 与 Reaper。
func InitSync(db *gorm.DB, rdb *redis.Client, cfg config.SyncConfig) *Engine {
	conc := cfg.WorkerConcurrency
	if conc <= 0 {
		conc = 4
	}
	e := &Engine{
		db:    db,
		rdb:   rdb,
		store: sync_store.New(rdb),
		cfg:   cfg,
		hub:   ws.GetHub(),
	}
	ws.SetFileSyncHandler(e.handleWSMessage)

	w := &Worker{engine: e}
	for i := 0; i < conc; i++ {
		go w.Run(context.Background())
	}
	go w.Reaper(context.Background())
	Global = e
	logger.Logger.Info("同步引擎初始化完成", zap.Int("workers", conc))
	return e
}

// handleWSMessage 是 file_sync 消息的总分发入口，按 event 路由到各处理器。
func (e *Engine) handleWSMessage(conn *ws.Connection, msg *ws.Message, event string, content map[string]interface{}) {
	userID := conn.UserID
	deviceID := conn.Device.DeviceID
	switch event {
	case ws.SyncEventFileChanged:
		var r FileChangeReport
		if bindContent(content, &r) {
			if err := e.HandleFileChange(userID, deviceID, r); err != nil {
				logger.Logger.Warn("处理文件变更上报失败", zap.Uint("user_id", userID), zap.Error(err))
			}
		}
	case ws.SyncEventTaskCompleted:
		var p struct {
			TaskID   uint64 `json:"task_id"`
			FileHash string `json:"file_hash"`
		}
		if bindContent(content, &p) {
			e.CompleteTask(p.TaskID, p.FileHash)
		}
	case ws.SyncEventTaskFailed:
		var p struct {
			TaskID uint64 `json:"task_id"`
			Error  string `json:"error"`
		}
		if bindContent(content, &p) {
			e.FailTask(p.TaskID, p.Error)
		}
	case ws.SyncEventTaskBlocked:
		var p struct {
			TaskID uint64 `json:"task_id"`
			Reason string `json:"reason"`
		}
		if bindContent(content, &p) {
			e.BlockTask(p.TaskID, p.Reason)
		}
	case ws.SyncEventTaskProgress:
		var p struct {
			TaskID   uint64 `json:"task_id"`
			Progress int    `json:"progress"`
			Bytes    int64  `json:"bytes"`
		}
		if bindContent(content, &p) {
			e.UpdateTaskProgress(p.TaskID, p.Progress, p.Bytes)
		}
	case ws.SyncEventScanResult:
		var r ScanReport
		if bindContent(content, &r) {
			e.HandleScan(userID, deviceID, r)
		}
	}
}

// ----- 包内共享小工具 -----

// cleanRelPath 清洗相对路径，去掉 `.`/`..` 段防止路径穿越。
func cleanRelPath(rel string) string {
	rel = filepath.ToSlash(rel)
	rel = filepath.Clean(rel)
	rel = strings.TrimPrefix(rel, "/")
	parts := strings.Split(rel, "/")
	var safe []string
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			continue
		}
		safe = append(safe, p)
	}
	return strings.Join(safe, "/")
}

// joinRemotePath 把 folder 远端根与相对路径拼成本地绝对路径。
func joinRemotePath(folderRemote, rel string) string {
	return filepath.Join(filepath.FromSlash(folderRemote), filepath.FromSlash(rel))
}

func ptrStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// bindContent 把 WS content（map）按 JSON 反序列化进目标结构体。
func bindContent(content map[string]interface{}, v interface{}) bool {
	data, err := json.Marshal(content)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, v) == nil
}
