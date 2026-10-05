package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"syc-file/internal/model"
	"syc-file/pkg/logger"
)

// HandleScan 处理离线重连后的全量扫描比对：以 trunk 为权威，给该设备补派缺失/过期/多余的任务。
//   - trunk 有、本地无         → download / mkdir
//   - trunk 有、本地 hash 不同  → download（trunk 为准）
//   - trunk 曾登记过且已软删、本地仍在 → delete
//   - trunk 从未登记过的本地路径（新文件）→ 忽略，等客户端的 upload/notify 自行登记
//
// 注意最后一条：不能把「trunk 无记录」当成「trunk 已删除」。否则本地刚出现、上传还没登记
// 的文件会被派 delete 删掉；客户端为规避它把待上传文件从清单里剔除，服务端又会按
// 「trunk 有、本地无」补派 download，导致同 hash 内容全量重下。
func (e *Engine) HandleScan(userID uint, deviceID string, report ScanReport) error {
	var folder model.SyncFolder
	if err := e.db.First(&folder, report.FolderID).Error; err != nil {
		return err
	}
	if folder.UserID != userID {
		return fmt.Errorf("sync folder unavailable")
	}
	if folder.Direction == model.DirectionUploadOnly {
		return nil
	}

	remotePrefix := filepath.Clean(folder.RemotePath)
	var files []model.File
	// MySQL LIKE 默认以 '\' 为转义符：Windows 路径 'E:\FileSync\%' 里 '\F'/'\%' 全被
	// 当成转义序列，永远匹配不到反斜杠路径（曾导致 scan 恒为 0 行、离线追赶失效）。
	// 改用 ESCAPE '|' 让反斜杠恢复字面量，并转义前缀里可能出现的 LIKE 元字符。
	esc := strings.NewReplacer("|", "||", "%", "|%", "_", "|_").Replace(remotePrefix)
	like := esc + string(filepath.Separator) + "%"
	e.db.Where("user_id = ? AND file_path LIKE ? ESCAPE '|'", userID, like).Find(&files)

	relToFile := make(map[string]model.File)
	for _, f := range files {
		rel := relFromPath(remotePrefix, f.FilePath)
		if rel == "" {
			continue
		}
		relToFile[rel] = f
	}

	items := report.Items
	live := liveDirs(relToFile)

	// trunk 侧：补派本地缺失或过期的内容
	for rel, f := range relToFile {
		if f.IsDeleted {
			continue
		}
		// 自愈：trunk 有记录但服务端物理文件已丢（历史清理/半途删除残留）——
		// 软删 trunk 并跳过，否则会永远派发注定失败的 download，Reaper 重试打转。
		if !f.IsDirectory {
			if _, statErr := os.Stat(f.FilePath); os.IsNotExist(statErr) {
				now := time.Now()
				e.db.Model(&f).Updates(map[string]interface{}{
					"is_deleted": true, "deleted_at": now, "version": f.Version + 1,
				})
				logger.Logger.Warn("trunk 记录的物理文件缺失，已软删自愈", zap.String("path", f.FilePath))
				continue
			}
		}
		it, ok := findItem(items, rel)
		if !ok {
			e.createAndEnqueueTask(userID, SourceServer, deviceID, folder, reportFromFolder(folder, f, rel), f.ID, taskTypeForFile(f), hashOf(f))
		} else if !f.IsDirectory && hashOf(f) != "" && hashOf(f) != it.FileHash {
			e.createAndEnqueueTask(userID, SourceServer, deviceID, folder, reportFromFolder(folder, f, rel), f.ID, model.TaskTypeDownload, hashOf(f))
		}
	}

	// 本地侧：trunk 里曾登记过、现已被软删的，派 delete 让设备删本地残留。
	// 从未登记过的路径（新文件）直接跳过，等上传/notify 登记。
	for _, it := range items {
		f, ok := relToFile[it.RelativePath]
		if !ok {
			continue
		}
		if f.IsDeleted {
			// 目录里还有未删除的文件，说明它没被删：上传子目录里的文件时 trunk 只登记文件、
			// 不单独登记目录，这种目录没有自己的记录，不能因此判成「trunk 已删除」。
			if it.IsDir && live[it.RelativePath] {
				continue
			}
			r := FileChangeReport{
				FolderID:     folder.ID,
				RelativePath: it.RelativePath,
				FileName:     it.FileName,
				FileSize:     it.FileSize,
				IsDir:        it.IsDir,
				Action:       model.FileChangeDelete,
			}
			e.createAndEnqueueTask(userID, SourceServer, deviceID, folder, r, f.ID, model.TaskTypeDelete, "")
		}
	}
	return nil
}

// liveDirs 返回 trunk 中仍有未删除内容的目录（含各级祖先），键为相对路径（正斜杠）。
func liveDirs(relToFile map[string]model.File) map[string]bool {
	dirs := make(map[string]bool)
	for rel, f := range relToFile {
		if f.IsDeleted {
			continue
		}
		for i := len(rel) - 1; i > 0; i-- {
			if rel[i] == '/' {
				dirs[rel[:i]] = true
			}
		}
	}
	return dirs
}

func findItem(items []ScanItem, rel string) (ScanItem, bool) {
	for _, it := range items {
		if it.RelativePath == rel {
			return it, true
		}
	}
	return ScanItem{}, false
}

// reportFromFolder 把一条 trunk File 转成 FileChangeReport，供服务端发起的任务复用派发逻辑。
func reportFromFolder(folder model.SyncFolder, f model.File, rel string) FileChangeReport {
	return FileChangeReport{
		FolderID:     folder.ID,
		RelativePath: rel,
		FileName:     f.FileName,
		FileSize:     sizeOf(f),
		FileHash:     hashOf(f),
		IsDir:        f.IsDirectory,
	}
}

func hashOf(f model.File) string {
	if f.FileHash != nil {
		return *f.FileHash
	}
	return ""
}

func sizeOf(f model.File) int64 {
	if f.FileSize != nil {
		return *f.FileSize
	}
	return 0
}

func taskTypeForFile(f model.File) string {
	if f.IsDirectory {
		return model.TaskTypeMkdir
	}
	return model.TaskTypeDownload
}

// relFromPath 从绝对路径中剥离 folder 远端根，得到 `/` 分隔的相对路径。
func relFromPath(prefix, fullPath string) string {
	prefixSlash := filepath.ToSlash(filepath.Clean(prefix))
	fullSlash := filepath.ToSlash(filepath.Clean(fullPath))
	rel := strings.TrimPrefix(fullSlash, prefixSlash)
	rel = strings.TrimPrefix(rel, "/")
	return rel
}
