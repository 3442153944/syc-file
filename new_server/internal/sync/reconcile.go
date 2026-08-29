package sync

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/internal/model"
	"syc-file/pkg/logger"
)

// ReconcileFolderFilesystem 把远端目录的物理内容"收编"进 trunk。
//
// 远端目录是权威存储位置：文件夹刚创建/重新保存时，磁盘上可能已经有早于本次登记的内容
// （历史遗留、手工拷贝等），trunk（model.File）里没有对应记录。HandleScan 只按 trunk 记录
// 跟设备上报的清单比对，trunk 没记录的文件永远不会被判定为"需要下发"——磁盘上有文件，
// 但没人告诉过引擎该文件存在。这里做一次单向收编：磁盘有、trunk 无 → 建 trunk 记录
// （hash 留空，下载方拿到的 expected_hash 为空时会跳过校验、直接采信内容，首个下载完成的
// 设备回报的 complete 会把 hash 填回 trunk）；trunk 已有的路径不动，避免覆盖真实同步产生的
// 版本历史。收编完成后立即给该用户所有在线设备派发缺失内容，不必等它们下次重连触发 scan。
func (e *Engine) ReconcileFolderFilesystem(folder model.SyncFolder) (adopted int, err error) {
	root := filepath.Clean(folder.RemotePath)
	info, statErr := os.Stat(root)
	if statErr != nil || !info.IsDir() {
		return 0, nil
	}

	var adoptedFiles []model.File
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // 单个条目读失败不中断整体收编
		}
		if path == root {
			return nil
		}
		name := d.Name()
		if d.IsDir() && (name == ".synctmp" || name == ".syncpending") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasPrefix(name, "~$") {
			return nil
		}
		rel := relFromPath(root, path)
		if rel == "" {
			return nil
		}

		var existing model.File
		lookupErr := e.db.Where("user_id = ? AND file_path = ?", folder.UserID, path).First(&existing).Error
		if lookupErr == nil {
			return nil // trunk 已有记录，不动
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return nil
		}

		f := model.File{
			UserID:      folder.UserID,
			FileName:    name,
			FilePath:    path,
			IsDirectory: d.IsDir(),
			Version:     1,
		}
		if !d.IsDir() {
			if fi, infoErr := d.Info(); infoErr == nil {
				size := fi.Size()
				f.FileSize = &size
			}
		}
		if createErr := e.db.Create(&f).Error; createErr != nil {
			logger.Logger.Warn("收编远端已有文件失败", zap.String("path", path), zap.Error(createErr))
			return nil
		}
		adoptedFiles = append(adoptedFiles, f)
		return nil
	})
	if walkErr != nil {
		return len(adoptedFiles), walkErr
	}
	if len(adoptedFiles) == 0 {
		return 0, nil
	}

	// 立即给所有在线设备派发，不等它们下次重连做 scan 比对
	conns := e.hub.GetUserConnections(folder.UserID)
	for _, f := range adoptedFiles {
		rel := relFromPath(root, f.FilePath)
		r := reportFromFolder(folder, f, rel)
		for _, conn := range conns {
			dev := conn.Device.DeviceID
			if dev == "" {
				continue
			}
			e.createAndEnqueueTask(folder.UserID, SourceServer, dev, folder, r, f.ID, taskTypeForFile(f), hashOf(f))
		}
	}
	return len(adoptedFiles), nil
}
