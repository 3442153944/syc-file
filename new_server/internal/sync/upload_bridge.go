package sync

import (
	"path/filepath"
	"strings"

	"syc-file/internal/model"
)

// findFolderForPath 找到包含 fullPath 的启用中同步文件夹，返回文件夹、相对路径（/ 分隔）、是否命中。
func (e *Engine) findFolderForPath(userID uint, fullPath string) (model.SyncFolder, string, bool) {
	var folders []model.SyncFolder
	e.db.Where("user_id = ? AND enabled = ?", userID, true).Find(&folders)

	target := filepath.ToSlash(filepath.Clean(fullPath))
	for _, f := range folders {
		prefix := filepath.ToSlash(filepath.Clean(f.RemotePath))
		if prefix == "" {
			continue
		}
		if target == prefix || strings.HasPrefix(target, prefix+"/") {
			rel := strings.TrimPrefix(target, prefix)
			rel = strings.TrimPrefix(rel, "/")
			if rel == "" {
				continue
			}
			return f, rel, true
		}
	}
	return model.SyncFolder{}, "", false
}

// HandleUploadComplete 分片上传落盘后调用：若目标落在某启用同步文件夹内，则把它当成
// 「源设备发来的一次文件变更」，走 trunk 维护 + 向其它在线设备派发拉取任务（复用现有
// HandleFileChange / dispatchToOthers）。文件内容已由上传流程写到 fullPath，此处只更新
// trunk 与派发，不重复写盘。
//
// 返回 handled=false 表示目标不在任何同步文件夹（或为 download_only），调用方按普通存储处理。
func (e *Engine) HandleUploadComplete(userID uint, deviceID, fullPath, fileName string, size int64, fileHash string) (bool, error) {
	folder, rel, ok := e.findFolderForPath(userID, fullPath)
	if !ok || folder.Direction == model.DirectionDownloadOnly {
		return false, nil
	}
	r := FileChangeReport{
		FolderID:     folder.ID,
		RelativePath: rel,
		FileName:     fileName,
		Action:       model.FileChangeCreate, // create/modify 由 HandleFileChange 内部按 trunk 是否已存在决定
		FileSize:     size,
		FileHash:     fileHash,
		IsDir:        false,
	}
	return true, e.HandleFileChange(userID, deviceID, r)
}
