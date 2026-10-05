package sync

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"syc-file/config"
	"syc-file/internal/model"
)

// findFolderForPath 找到包含 fullPath 的启用中同步文件夹，返回文件夹、相对路径（/ 分隔）、是否命中。
// FolderForPath 是 findFolderForPath 的导出版本：给 file 域（版本回滚等）判断
// 某个绝对路径是否落在启用的同步文件夹内，并拿到它的相对路径。
func (e *Engine) FolderForPath(userID uint, fullPath string) (model.SyncFolder, string, bool) {
	return e.findFolderForPath(userID, fullPath)
}

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

// ReportUploadConflict 同步客户端的覆盖式上传在 init 阶段发现与服务端版本分叉（客户端基线 ≠ 服务端当前版本，
// 或客户端没有基线而服务端已有不同内容）：登记冲突待办，并通知该设备把本地副本隔离到 .syncpending、
// 主目录收敛为服务端版本，之后由用户选择保留哪一个（见 ResolveConflict）。trunk 不动。
// 同一设备同一文件已有未处理的冲突时复用那条记录，只重发通知，避免反复追赶堆出一串待办。
// 返回是否登记/通知成功（目标不在启用的同步文件夹内或 trunk 无记录时返回 false）。
func (e *Engine) ReportUploadConflict(userID uint, deviceID, fullPath, fileName string, size int64, localHash, baseHash string) bool {
	if deviceID == "" {
		return false
	}
	folder, rel, ok := e.findFolderForPath(userID, fullPath)
	if !ok {
		return false
	}
	var file model.File
	if err := e.db.Where("user_id = ? AND file_path = ? AND is_deleted = ?", userID, fullPath, false).First(&file).Error; err != nil {
		return false
	}
	r := FileChangeReport{
		FolderID:     folder.ID,
		RelativePath: rel,
		FileName:     fileName,
		Action:       model.FileChangeModify,
		FileSize:     size,
		FileHash:     localHash,
		BaseHash:     baseHash,
	}
	var pending model.SyncConflict
	if err := e.db.Where("user_id = ? AND device_id = ? AND folder_id = ? AND relative_path = ? AND status = ?",
		userID, deviceID, folder.ID, rel, model.ConflictStatusPending).First(&pending).Error; err == nil {
		e.db.Model(&pending).Updates(map[string]interface{}{"local_hash": ptrStr(localHash), "server_hash": file.FileHash, "server_version": file.Version})
		e.notifyConflict(deviceID, folder, r, file, pending.ID)
		return true
	}
	e.handleConflict(userID, deviceID, folder, r, file)
	return true
}

// matchFolderByLocalPath 在 folders 里找 local_path 包含 fullPath 的那个，返回文件夹和相对路径（/ 分隔）。
// local_path 是设备本地镜像目录。服务端所在机器自己也跑着同步客户端时，镜像目录就在服务端磁盘上，
// 用户在网页里浏览/编辑到的正是这份镜像，而不是 trunk（remote_path）。别的机器的本地路径
// （如 Windows 盘符路径）不可能是服务端磁盘上的绝对路径，天然匹配不上。
func matchFolderByLocalPath(folders []model.SyncFolder, fullPath string) (model.SyncFolder, string, bool) {
	target := filepath.ToSlash(filepath.Clean(fullPath))
	for _, f := range folders {
		if !strings.HasPrefix(f.LocalPath, "/") {
			continue
		}
		prefix := filepath.ToSlash(filepath.Clean(f.LocalPath))
		if prefix == "/" || !strings.HasPrefix(target, prefix+"/") {
			continue
		}
		rel := strings.TrimPrefix(target, prefix+"/")
		if rel == "" {
			continue
		}
		return f, rel, true
	}
	return model.SyncFolder{}, "", false
}

// HandleMirrorEdit 处理「直接改了某同步文件夹本地镜像目录里的文件」（如网页在线编辑服务端机器上的镜像）：
// 这份改动没经过任何同步客户端，trunk 和其它设备都不知道。这里把文件内容复制进 trunk，
// 再按「服务端发起的一次文件变更」走 HandleFileChange——更新 trunk、记版本历史、向所有在线设备派发拉取，
// 离线设备上线后的 scan 会按 trunk 补齐。来源记为服务端，不排除镜像所属设备：它拿到的是同内容的下载，无副作用。
//
// 返回 handled=false 表示 fullPath 不在任何启用且可上行的同步文件夹的镜像目录内。
func (e *Engine) HandleMirrorEdit(userID uint, fullPath, fileName string, size int64, fileHash string) (bool, error) {
	var folders []model.SyncFolder
	e.db.Where("user_id = ? AND enabled = ? AND direction <> ?", userID, true, model.DirectionDownloadOnly).Find(&folders)
	folder, rel, ok := matchFolderByLocalPath(folders, fullPath)
	if !ok {
		return false, nil
	}
	trunkPath := joinRemotePath(folder.RemotePath, rel)
	if !config.Conf.IsPathAllowed(trunkPath) {
		return false, fmt.Errorf("path not allowed: %s", trunkPath)
	}
	if err := copyFileAtomic(fullPath, trunkPath); err != nil {
		return false, fmt.Errorf("复制到 trunk 失败: %w", err)
	}
	r := FileChangeReport{
		FolderID:     folder.ID,
		RelativePath: rel,
		FileName:     fileName,
		Action:       model.FileChangeCreate, // create/modify 由 HandleFileChange 内部按 trunk 是否已存在决定
		FileSize:     size,
		FileHash:     fileHash,
	}
	return true, e.HandleFileChange(userID, SourceServer, r)
}

// copyFileAtomic 把 src 复制到 dst：先写同目录的临时文件再改名，读取方不会看到写了一半的 trunk 文件。
func copyFileAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".syncedit-*.part")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o666); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
