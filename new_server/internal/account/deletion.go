// Package account 统一账号删除与隔离空间清理，普通用户和访客共用同一流程。
package account

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"syc-file/internal/model"
	"syc-file/internal/storage"
	"syc-file/internal/ws"
	"syc-file/pkg/logger"
	"syc-file/pkg/synccore"
)

type Result struct {
	CleanupPending bool `json:"cleanup_pending"`
}

// Delete 先持久撤销账号访问，再清文件；失败保留删除中状态和空间额度供后台重试。
// 删除中账号不能恢复启用，避免清理时重新产生文件。
func Delete(db *gorm.DB, actorID, targetID uint, cleanShares func(uint) error) (Result, error) {
	if actorID == targetID {
		return Result{}, errors.New("不能删除自己")
	}
	err := storage.LockedTransaction(db, func(tx *gorm.DB) error {
		actor, err := storage.User(tx, actorID)
		if err != nil || actor.Level < model.LevelAdmin {
			return storage.ErrDenied
		}
		var target model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&target, targetID).Error; err != nil {
			return errors.New("目标账号不存在")
		}
		if target.Level >= actor.Level {
			return errors.New("只能删除权限低于自己的账号")
		}
		var s model.UploadSpace
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", targetID).First(&s).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if actor.Level < model.LevelSuper {
			owned := target.CreatedBy != nil && *target.CreatedBy == actorID
			if s.ID != 0 {
				var parent model.UploadSpace
				owned = s.ParentID != nil && tx.First(&parent, *s.ParentID).Error == nil && parent.UserID == actorID
			}
			if !owned {
				return errors.New("只能删除自己创建或从自己空间分配的下级账号")
			}
		}
		var children int64
		if err := tx.Model(&model.User{}).Where("created_by = ?", targetID).Count(&children).Error; err != nil {
			return err
		}
		if children > 0 {
			return errors.New("账号仍有下级账号，请先处理下级账号再删除")
		}
		if s.ID != 0 {
			if err := tx.Model(&model.UploadSpace{}).Where("parent_id = ?", s.ID).Count(&children).Error; err != nil {
				return err
			}
			if children > 0 {
				return errors.New("账号仍有下级空间，请先处理下级账号和空间再删除")
			}
			if err := tx.Model(&s).Update("enabled", false).Error; err != nil {
				return err
			}
		}
		return tx.Model(&target).Update("status", model.UserDeleting).Error
	})
	if err != nil {
		return Result{}, err
	}
	ws.GetHub().DisconnectUser(targetID)
	err = Purge(db, targetID, cleanShares)
	if err != nil {
		logger.Logger.Warn("账号已撤销访问，文件清理等待重试", zap.Uint("user_id", targetID), zap.Error(err))
	}
	return Result{CleanupPending: err != nil}, nil
}

var purgeMu sync.Mutex

// Purge 幂等清理删除中的账号；文件删除成功后才释放父空间的已分配额度。
// 只删除隔离空间，历史同步目录和客户端磁盘不在此清理范围内。
func Purge(db *gorm.DB, uid uint, cleanShares func(uint) error) error {
	purgeMu.Lock()
	defer purgeMu.Unlock()
	var u model.User
	if err := db.First(&u, uid).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if u.Status != model.UserDeleting {
		return storage.ErrDenied
	}
	if cleanShares != nil {
		if err := cleanShares(uid); err != nil {
			return err
		}
	}
	return storage.LockedTransaction(db, func(tx *gorm.DB) error {
		var children int64
		if err := tx.Model(&model.User{}).Where("created_by = ?", uid).Count(&children).Error; err != nil {
			return err
		}
		if children > 0 {
			return errors.New("账号仍有下级账号，等待处理后再清理")
		}
		var s model.UploadSpace
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", uid).First(&s).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if s.ID != 0 {
			var pool model.StoragePool
			if err := tx.First(&pool, s.PoolID).Error; err != nil {
				return err
			}
			// 存储池未挂载时不能把“目录不存在”误判为文件已经清空。
			if info, err := os.Stat(pool.RootPath); err != nil || !info.IsDir() {
				return errors.New("存储位置不可访问，稍后重试清理")
			}
			root := storage.Root(pool, s)
			if filepath.Clean(root) == filepath.Clean(pool.RootPath) || filepath.Base(root) != fmt.Sprint(uid) || !storage.Within(pool.RootPath, root) {
				return storage.ErrDenied
			}
			var others []model.UploadSpace
			if err := tx.Where("pool_id = ? AND id <> ?", s.PoolID, s.ID).Find(&others).Error; err != nil {
				return err
			}
			for _, other := range others {
				if storage.Within(root, storage.Root(pool, other)) {
					return errors.New("空间仍包含其他账号目录，拒绝清理")
				}
			}
			if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if os.IsNotExist(err) {
					return nil
				}
				if err != nil {
					return err
				}
				if !d.IsDir() && d.Type().IsRegular() {
					return synccore.Evict(path)
				}
				return nil
			}); err != nil {
				return err
			}
			if err := os.RemoveAll(root); err != nil {
				return err
			}
		}
		// 版本通过文件编号关联，先清版本，审计日志保留。
		if err := tx.Where("file_id IN (?)", tx.Model(&model.File{}).Select("id").Where("user_id = ?", uid)).Delete(&model.FileVersion{}).Error; err != nil {
			return err
		}
		for _, row := range []any{&model.UserRole{}, &model.UserRoute{}, &model.Device{}, &model.SyncTask{}, &model.SyncConflict{}, &model.SyncFolder{}, &model.File{}, &model.StorageConfig{}, &model.UploadHistory{}, &model.DownloadHistory{}, &model.ShareRecord{}, &model.ShareLink{}, &model.UploadSpace{}} {
			if err := tx.Where("user_id = ?", uid).Delete(row).Error; err != nil {
				return err
			}
		}
		return tx.Where("id = ? AND status = ?", uid, model.UserDeleting).Delete(&model.User{}).Error
	})
}

// StartCleanupWorker 服务重启后继续未完成的删除，每分钟重试，不依赖内存任务。
func StartCleanupWorker(db *gorm.DB, cleanShares func(uint) error) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			var users []model.User
			if err := db.Where("status = ?", model.UserDeleting).Find(&users).Error; err != nil {
				logger.Logger.Warn("扫描待清理账号失败", zap.Error(err))
			} else {
				for _, u := range users {
					if err := Purge(db, u.ID, cleanShares); err != nil {
						logger.Logger.Warn("重试账号清理失败", zap.Uint("user_id", u.ID), zap.Error(err))
					}
				}
			}
			<-ticker.C
		}
	}()
}
