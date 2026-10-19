package admin

import (
	"errors"
	"github.com/gin-gonic/gin"
	"syc-file/internal/model"
	"syc-file/internal/storage"
)

// storageRow 兼容旧客户端字段，实际配额统一读取父子上传空间。
type storageRow struct {
	model.StorageConfig
	Username    string  `json:"username"`
	UsedPercent float64 `json:"used_percent"`
}

func compatibleStorage(uid uint, v storage.View) model.StorageConfig {
	return model.StorageConfig{UserID: uid, TotalQuota: v.QuotaBytes, UsedQuota: v.UsedBytes, FileCount: int(v.FileCount)}
}

func (h *APIHandler) ListStorage(c *gin.Context) {
	if !h.requireStorageSuper(c) {
		return
	}
	p := parsePage(c)
	var total int64
	if h.db.Model(&model.User{}).Count(&total).Error != nil {
		jsonErr(c, 500, "读取用户失败")
		return
	}
	var users []model.User
	if h.db.Select("id, username").Order("id").Offset(p.offset()).Limit(p.PageSize).Find(&users).Error != nil {
		jsonErr(c, 500, "读取用户失败")
		return
	}
	rows := make([]storageRow, 0, len(users))
	for _, u := range users {
		v, err := storage.Mine(h.db, u.ID)
		if err != nil && !errors.Is(err, storage.ErrUnassigned) {
			jsonErr(c, 500, "读取空间用量失败")
			return
		}
		row := storageRow{StorageConfig: compatibleStorage(u.ID, v), Username: u.Username}
		if v.QuotaBytes > 0 {
			row.UsedPercent = float64(v.UsedBytes) / float64(v.QuotaBytes) * 100
		}
		rows = append(rows, row)
	}
	jsonPage(c, rows, total, p)
}

func (h *APIHandler) MyStorage(c *gin.Context) {
	uid, ok := currentUser(c)
	if !ok {
		return
	}
	if _, err := storage.User(h.db, uid); err != nil {
		jsonErr(c, 403, "账号不可用")
		return
	}
	v, err := storage.Mine(h.db, uid)
	if err != nil && !errors.Is(err, storage.ErrUnassigned) {
		jsonErr(c, 500, "读取空间用量失败")
		return
	}
	percent := 0.0
	if v.QuotaBytes > 0 {
		percent = float64(v.UsedBytes) / float64(v.QuotaBytes) * 100
	}
	jsonOK(c, gin.H{"config": compatibleStorage(uid, v), "used_percent": percent})
}

// UpdateQuota 拒绝旧接口绕过父空间额度、有效期及级别校验。
func (h *APIHandler) UpdateQuota(c *gin.Context) {
	if !h.requireStorageSuper(c) {
		return
	}
	jsonErr(c, 410, "请使用空间管理修改容量和有效期，旧接口不支持父子额度校验")
}

func (h *APIHandler) RecalcStorage(c *gin.Context) {
	if !h.requireStorageSuper(c) {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	v, err := storage.Mine(h.db, uint(id))
	if err != nil {
		jsonErr(c, 400, "读取空间用量失败")
		return
	}
	jsonOK(c, gin.H{"used_quota": v.UsedBytes, "file_count": v.FileCount})
}
