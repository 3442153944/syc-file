package admin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/v3/disk"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/internal/storage"
)

func (h *APIHandler) requireStorageSuper(c *gin.Context) bool {
	uid, ok := currentUser(c)
	if !ok {
		return false
	}
	u, err := storage.User(h.db, uid)
	if err != nil || u.Level < model.LevelSuper {
		jsonErr(c, 403, "仅系统管理员可配置上传空间")
		return false
	}
	return true
}

func (h *APIHandler) StoragePartitions(c *gin.Context) {
	if !h.requireStorageSuper(c) {
		return
	}
	parts, err := disk.Partitions(false)
	if err != nil {
		jsonErr(c, 500, "读取服务器分区失败")
		return
	}
	rows := make([]gin.H, 0, len(parts))
	for _, p := range parts {
		if u, err := disk.Usage(p.Mountpoint); err == nil {
			rows = append(rows, gin.H{"path": p.Mountpoint, "device": p.Device, "total": u.Total, "free": u.Free})
		}
	}
	jsonOK(c, rows)
}

func (h *APIHandler) ListStoragePools(c *gin.Context) {
	actor, ok := h.spaceAllocator(c)
	if !ok {
		return
	}
	if actor.Level < model.LevelSuper {
		s, p, err := storage.Load(h.db, actor.ID)
		if err != nil {
			jsonOK(c, []model.StoragePool{})
			return
		}
		jsonOK(c, []model.StoragePool{{ID: s.PoolID, Name: p.Name, RootPath: storage.VirtualRoot, Enabled: p.Enabled}})
		return
	}

	var rows []model.StoragePool
	if err := h.db.Order("id").Find(&rows).Error; err != nil {
		jsonErr(c, 500, "读取存储位置失败")
		return
	}
	jsonOK(c, rows)
}

func (h *APIHandler) CreateStoragePool(c *gin.Context) {
	if !h.requireStorageSuper(c) {
		return
	}
	var req struct {
		Name     string `json:"name"`
		RootPath string `json:"root_path"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.Name) == "" || len(req.Name) > 100 || !filepath.IsAbs(req.RootPath) {
		jsonErr(c, 400, "请填写名称和服务器绝对目录")
		return
	}
	root := filepath.Clean(req.RootPath)
	// 上传空间不能落入公开静态目录或分享暂存目录，否则可绕过文件接口直接取文件。
	for _, public := range []string{config.Conf.User.AvatarPath, config.Conf.Share.TempPath} {
		if public == "" {
			continue
		}
		absolute, err := filepath.Abs(public)
		if err != nil {
			jsonErr(c, 400, "静态目录配置无效")
			return
		}
		if storage.Within(absolute, root) || storage.Within(root, absolute) {
			jsonErr(c, 400, "上传根目录不可与公开静态目录或分享临时目录重叠")
			return
		}
	}
	if len(root) > 450 || !storage.Within(root, root) || filepath.Dir(root) == root {
		jsonErr(c, 400, "请选择磁盘下的独立目录，不可直接使用磁盘根目录或符号链接")
		return
	}
	entries, err := os.ReadDir(root)
	if err == nil && len(entries) > 0 {
		jsonErr(c, 400, "上传根目录必须为空，避免暴露或接管已有文件")
		return
	}
	if err != nil && !os.IsNotExist(err) {
		jsonErr(c, 400, "目录不可访问")
		return
	}
	// 禁止存储位置重叠，也禁止把已有或已停用的位置包含在新目录中。
	var pools []model.StoragePool
	if h.db.Find(&pools).Error != nil {
		jsonErr(c, 500, "读取存储位置失败")
		return
	}
	for _, p := range pools {
		if storage.Within(p.RootPath, root) || storage.Within(root, p.RootPath) {
			jsonErr(c, 400, "存储位置不可重叠")
			return
		}
	}
	if os.MkdirAll(root, 0700) != nil {
		jsonErr(c, 400, "无法创建上传根目录，请检查挂载状态和服务账号权限")
		return
	}
	probe, err := os.CreateTemp(root, ".write-check-")
	if err != nil {
		jsonErr(c, 400, "上传根目录不可写")
		return
	}
	probe.Close()
	_ = os.Remove(probe.Name())
	p := model.StoragePool{Name: strings.TrimSpace(req.Name), RootPath: root, Enabled: true}
	if h.db.Create(&p).Error != nil {
		jsonErr(c, 409, "存储位置已存在或保存失败")
		return
	}
	jsonOK(c, p)
}

func (h *APIHandler) UpdateStoragePool(c *gin.Context) {
	if !h.requireStorageSuper(c) {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Enabled == nil {
		jsonErr(c, 400, "缺少 enabled")
		return
	}
	result := h.db.Model(&model.StoragePool{}).Where("id = ?", id).Update("enabled", *req.Enabled)
	if result.Error != nil {
		jsonErr(c, 500, "更新失败")
		return
	}
	if result.RowsAffected == 0 {
		jsonErr(c, 404, "存储位置不存在或未变化")
		return
	}
	jsonOK(c, nil)
}

// spaceAllocator 仅系统管理员和普通管理员可管理下级额度，级别取数据库实时值。
func (h *APIHandler) spaceAllocator(c *gin.Context) (model.User, bool) {
	uid, ok := currentUser(c)
	if !ok {
		return model.User{}, false
	}
	u, err := storage.User(h.db, uid)
	if err != nil || u.Level < model.LevelAdmin {
		jsonErr(c, 403, "仅管理员可分配下级空间")
		return u, false
	}
	return u, true
}

func (h *APIHandler) ListUploadSpaces(c *gin.Context) {
	actor, ok := h.spaceAllocator(c)
	if !ok {
		return
	}
	var users []model.User
	query := h.db.Select("id, username, level")
	if actor.Level < model.LevelSuper {
		query = query.Where("level < ?", actor.Level)
	}
	if query.Order("id").Find(&users).Error != nil {
		jsonErr(c, 500, "读取用户失败")
		return
	}
	var parent model.UploadSpace
	if actor.Level < model.LevelSuper {
		var err error
		parent, _, err = storage.Load(h.db, actor.ID)
		if err != nil {
			jsonErr(c, 403, "请先由系统管理员分配空间")
			return
		}
	}
	rows := make([]gin.H, 0, len(users))
	for _, u := range users {
		assigned, _, loadErr := storage.Load(h.db, u.ID)
		if actor.Level < model.LevelSuper && loadErr == nil && (assigned.ParentID == nil || *assigned.ParentID != parent.ID) {
			continue
		}
		if loadErr != nil && !errors.Is(loadErr, storage.ErrUnassigned) {
			jsonErr(c, 500, "读取空间分配失败")
			return
		}
		row := gin.H{"user_id": u.ID, "username": u.Username, "level": u.Level, "space": nil}
		v, err := storage.Mine(h.db, u.ID)
		if err == nil {
			row["space"] = v
		} else if !errors.Is(err, storage.ErrUnassigned) {
			row["space"] = storage.View{UploadSpace: assigned, Frozen: true}
			row["error"] = "空间目录不可访问，请检查挂载和权限"
		}
		rows = append(rows, row)
	}
	jsonOK(c, rows)
}

func (h *APIHandler) PutUploadSpace(c *gin.Context) {
	actor, ok := h.spaceAllocator(c)
	if !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		PoolID     uint       `json:"pool_id"`
		QuotaBytes int64      `json:"quota_bytes"`
		ExpiresAt  *time.Time `json:"expires_at"`
		Enabled    bool       `json:"enabled"`
	}
	if c.ShouldBindJSON(&req) != nil || req.QuotaBytes <= 0 {
		jsonErr(c, 400, "容量必须大于零")
		return
	}
	var target model.User
	if h.db.Select("id, level").First(&target, id).Error != nil {
		jsonErr(c, 404, "目标账号不存在")
		return
	}
	if target.Level >= actor.Level && !(actor.Level == model.LevelSuper && target.ID == actor.ID) {
		jsonErr(c, 403, "只能向更低权限账号分配空间")
		return
	}
	err := storage.LockedTransaction(h.db, func(tx *gorm.DB) error {
		// 锁定目标账号；父空间行锁负责串行核算所有下级额度。
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&target, id).Error; err != nil {
			return err
		}
		if target.Status == model.UserDeleting {
			return errors.New("账号正在删除，不能再分配空间")
		}
		liveActor, err := storage.User(tx, actor.ID)
		if err != nil || liveActor.Level < model.LevelAdmin || liveActor.Level != actor.Level {
			return storage.ErrDenied
		}
		if target.Level >= liveActor.Level && !(liveActor.Level == model.LevelSuper && target.ID == liveActor.ID) {
			return storage.ErrDenied
		}
		var s model.UploadSpace
		err = tx.Where("user_id = ?", id).First(&s).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		oldQuota := s.QuotaBytes
		var parent model.UploadSpace
		if actor.Level < model.LevelSuper {
			if tx.Where("user_id = ?", actor.ID).First(&parent).Error != nil {
				return storage.ErrUnassigned
			}
			if s.ID != 0 && (s.ParentID == nil || *s.ParentID != parent.ID) {
				return storage.ErrDenied
			}
			if req.PoolID != 0 && req.PoolID != parent.PoolID {
				return storage.ErrDenied
			}
			req.PoolID = parent.PoolID
		} else if s.ParentID != nil {
			if err := tx.First(&parent, *s.ParentID).Error; err != nil {
				return err
			}
		}
		if parent.ID != 0 {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&parent, parent.ID).Error; err != nil {
				return err
			}
			var pool model.StoragePool
			if err := tx.First(&pool, parent.PoolID).Error; err != nil {
				return err
			}
			if actor.Level < model.LevelSuper {
				if err := storage.ActiveTree(tx, parent, pool); err != nil {
					return err
				}
			}
			if parent.ExpiresAt != nil && (req.ExpiresAt == nil || req.ExpiresAt.After(*parent.ExpiresAt)) {
				return errors.New("下级空间有效期不能晚于父空间，请设置相同或更早的截止时间")
			}
			own, _, err := storage.Usage(storage.Root(pool, parent))
			if err != nil {
				return err
			}
			allocated, err := storage.Allocated(tx, parent.ID)
			if err != nil {
				return err
			}
			if req.QuotaBytes > parent.QuotaBytes || own > parent.QuotaBytes-req.QuotaBytes || allocated-oldQuota > parent.QuotaBytes-req.QuotaBytes-own {
				return errors.New("父空间可分配容量不足，需扣除自身文件和其他下级已分配额度")
			}
			pid := parent.ID
			s.ParentID = &pid
			if s.ID == 0 {
				relative, err := filepath.Rel(pool.RootPath, storage.Root(pool, parent))
				if err != nil {
					return err
				}
				s.Directory = filepath.Join(relative, ".children", fmt.Sprint(id))
			}
		}
		if req.PoolID == 0 {
			return errors.New("请选择存储位置")
		}
		var pool model.StoragePool
		if tx.First(&pool, req.PoolID).Error != nil {
			return errors.New("存储位置不存在")
		}
		if s.ID != 0 && s.PoolID != req.PoolID {
			return errors.New("已有空间不可直接更换磁盘，需单独迁移文件")
		}
		s.UserID = uint(id)
		s.PoolID = req.PoolID
		s.QuotaBytes = req.QuotaBytes
		s.ExpiresAt = req.ExpiresAt
		s.Enabled = req.Enabled
		if s.ID == 0 {
			s.AllocatedBy = actor.ID
		}
		root := storage.Root(pool, s)
		if !storage.Within(pool.RootPath, root) {
			return storage.ErrDenied
		}
		if err := os.MkdirAll(root, 0700); err != nil {
			return errors.New("无法创建空间目录，请检查挂载和权限")
		}
		if err := storage.EnsureDefaultFolder(root); err != nil {
			return errors.New("无法创建默认文件夹，请检查目录和权限")
		}
		own, _, err := storage.Usage(root)
		if err != nil {
			return err
		}
		allocated := int64(0)
		if s.ID != 0 {
			allocated, err = storage.Allocated(tx, s.ID)
			if err != nil {
				return err
			}
		}
		if own > req.QuotaBytes || allocated > req.QuotaBytes-own {
			return errors.New("容量不能低于自身已用空间与已划给下级的额度之和")
		}
		return tx.Save(&s).Error
	})
	if err != nil {
		jsonErr(c, 400, err.Error())
		return
	}
	jsonOK(c, nil)
}

func (h *APIHandler) MyUploadSpace(c *gin.Context) {
	uid, ok := currentUser(c)
	if !ok {
		return
	}
	if _, err := storage.User(h.db, uid); err != nil {
		jsonErr(c, 403, "账号不可用")
		return
	}
	v, err := storage.Mine(h.db, uid)
	if errors.Is(err, storage.ErrUnassigned) {
		jsonOK(c, nil)
		return
	}
	if err != nil {
		jsonErr(c, 500, "上传空间不可访问，请联系系统管理员检查挂载和权限")
		return
	}
	jsonOK(c, v)
}
