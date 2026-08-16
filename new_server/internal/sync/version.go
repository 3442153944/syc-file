package sync

import (
	"io"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/pkg/logger"
)

// 版本历史与版本内容仓库。
//
// ── 为什么要存内容 ──────────────────────────────────────────
// 原来 appendVersion 只写 file_id/version/size/hash，`storage_path` 一直是 NULL，
// 也就是说「版本历史」只有元数据、**没有任何一个旧版本的内容**——历史看得见、回不去。
// 要能回滚就必须在内容成为 trunk 的那一刻把它留一份。
//
// ── 仓库形态：内容寻址（CAS）──────────────────────────────
// blob 路径 = <目标盘>/<BasePath>/versions/<hash[:2]>/<hash>
// 用整文件 blake3 做键，天然去重：同一份内容被反复上传/回滚只占一份空间；
// 已存在同名 blob 直接跳过复制。回滚时把 blob 复制回目标路径即可。
//
// ── 代价与上限 ──────────────────────────────────────────────
// 每接受一个新版本要多写一份文件。大文件（视频、镜像）留历史代价过高且几乎无用，
// 超过 maxVersionedSize 的直接跳过存内容（版本行照常记，只是 storage_path 为空、不可回滚）。

// maxVersionedSize 超过这个大小的文件不留版本内容（仍记版本元数据）。
const maxVersionedSize = 200 << 20 // 200 MiB

// versionsDirName 版本仓库在 BasePath 下的目录名。
const versionsDirName = "versions"

// appendVersion 在 trunk 每次接受新内容后追加一条版本历史（file_version 表），
// 并把这一版的内容存进版本仓库（失败只告警，不阻断同步主流程）。
//
// currentPath 是该版本内容此刻在磁盘上的位置（trunk 的绝对路径）。
func (e *Engine) appendVersion(fileID uint64, version int, size int64, hash string, createdBy uint) {
	e.appendVersionWithContent(fileID, version, size, hash, createdBy, "")
}

// appendVersionWithContent 同 appendVersion，额外把 currentPath 指向的内容存进版本仓库。
func (e *Engine) appendVersionWithContent(fileID uint64, version int, size int64, hash string, createdBy uint, currentPath string) {
	v := &model.FileVersion{
		FileID:   fileID,
		Version:  version,
		FileSize: &size,
		FileHash: ptrStr(hash),
	}
	if createdBy != 0 {
		cb := createdBy
		v.CreatedBy = &cb
	}
	if blob := preserveBlob(currentPath, hash, size); blob != "" {
		v.StoragePath = &blob
	}
	if err := e.db.Create(v).Error; err != nil {
		logger.Logger.Warn("写入文件版本历史失败", zap.Uint64("file_id", fileID), zap.Error(err))
	}
}

// preserveBlob 把 srcPath 的内容按内容寻址存进版本仓库，返回 blob 路径；
// 不满足条件（无路径/无哈希/过大/复制失败）时返回空串，调用方按「本版无内容」处理。
func preserveBlob(srcPath, hash string, size int64) string {
	if srcPath == "" || hash == "" {
		return ""
	}
	if size > maxVersionedSize {
		logger.Logger.Info("文件过大，跳过版本内容留存",
			zap.String("path", srcPath), zap.Int64("size", size))
		return ""
	}
	st, err := os.Stat(srcPath)
	if err != nil || st.IsDir() {
		return ""
	}

	blobPath := BlobPathFor(srcPath, hash)
	if blobPath == "" {
		return ""
	}
	// 内容寻址：同 hash 的 blob 已存在就直接复用，不重复占空间
	if _, err := os.Stat(blobPath); err == nil {
		return blobPath
	}
	if err := os.MkdirAll(filepath.Dir(blobPath), 0o755); err != nil {
		logger.Logger.Warn("创建版本仓库目录失败", zap.String("dir", filepath.Dir(blobPath)), zap.Error(err))
		return ""
	}
	// 先写 .tmp 再改名：中途挂掉不会在仓库里留下一个「哈希对不上内容」的坏 blob
	tmp := blobPath + ".tmp"
	if err := copyFileTo(srcPath, tmp); err != nil {
		logger.Logger.Warn("留存版本内容失败", zap.String("src", srcPath), zap.Error(err))
		_ = os.Remove(tmp)
		return ""
	}
	if err := os.Rename(tmp, blobPath); err != nil {
		_ = os.Remove(tmp)
		return ""
	}
	return blobPath
}

// BlobPathFor 计算版本 blob 的存放路径。与目标文件同盘，回滚时的复制不跨盘。
func BlobPathFor(sameVolumeAs, hash string) string {
	if len(hash) < 2 {
		return ""
	}
	vol := filepath.VolumeName(sameVolumeAs)
	base := config.Conf.File.Storage.BasePath
	return filepath.Join(vol+string(filepath.Separator), base, versionsDirName, hash[:2], hash)
}

func copyFileTo(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
