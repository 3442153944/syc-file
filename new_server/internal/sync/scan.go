package sync

import (
	"fmt"
	"path/filepath"
	"strings"

	"syc-file/internal/model"
)

// HandleScan 处理离线重连后的全量扫描比对：以 trunk 为权威，给该设备补派缺失/过期/多余的任务。
//   - trunk 有、本地无         → download / mkdir
//   - trunk 有、本地 hash 不同  → download（trunk 为准）
//   - trunk 无、本地有         → delete（trunk 已删除）
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
	like := strings.ReplaceAll(filepath.ToSlash(remotePrefix), "/", "\\") + string(filepath.Separator) + "%"
	e.db.Where("user_id = ? AND file_path LIKE ?", userID, like).Find(&files)

	relToFile := make(map[string]model.File)
	for _, f := range files {
		rel := relFromPath(remotePrefix, f.FilePath)
		if rel == "" {
			continue
		}
		relToFile[rel] = f
	}

	items := report.Items

	// trunk 侧：补派本地缺失或过期的内容
	for rel, f := range relToFile {
		if f.IsDeleted {
			continue
		}
		it, ok := findItem(items, rel)
		if !ok {
			e.createAndEnqueueTask(userID, SourceServer, deviceID, folder, reportFromFolder(folder, f, rel), f.ID, taskTypeForFile(f), hashOf(f))
		} else if !f.IsDirectory && hashOf(f) != "" && hashOf(f) != it.FileHash {
			e.createAndEnqueueTask(userID, SourceServer, deviceID, folder, reportFromFolder(folder, f, rel), f.ID, model.TaskTypeDownload, hashOf(f))
		}
	}

	// 本地侧：trunk 已删除的，派 delete 让设备删本地残留
	for _, it := range items {
		f, ok := relToFile[it.RelativePath]
		if !ok || f.IsDeleted {
			r := FileChangeReport{
				FolderID:     folder.ID,
				RelativePath: it.RelativePath,
				FileName:     it.FileName,
				FileSize:     it.FileSize,
				IsDir:        it.IsDir,
				Action:       model.FileChangeDelete,
			}
			fileID := uint64(0)
			if ok {
				fileID = f.ID
			}
			e.createAndEnqueueTask(userID, SourceServer, deviceID, folder, r, fileID, model.TaskTypeDelete, "")
		}
	}
	return nil
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
