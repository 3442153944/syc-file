package sync

import (
	"go.uber.org/zap"

	"syc-file/internal/model"
	"syc-file/pkg/logger"
)

// appendVersion 在 trunk 每次接受新内容后追加一条版本历史（file_version 表），
// 形成 git 式的线性提交历史。失败仅告警，不阻断主流程。
func (e *Engine) appendVersion(fileID uint64, version int, size int64, hash string, createdBy uint) {
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
	if err := e.db.Create(v).Error; err != nil {
		logger.Logger.Warn("写入文件版本历史失败", zap.Uint64("file_id", fileID), zap.Error(err))
	}
}
