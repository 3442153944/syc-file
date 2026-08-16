package admin

import (
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"syc-file/internal/model"
)

// 存储配额：storage_config 表建库就有但一直没人写，配额/用量从来没被统计过。
//
// 这里只做「统计 + 展示 + 改配额」，**暂不做上传拦截**——拦截要动上传链路的多个入口
// （单发/分片/秒传/同步落盘），且配额算错会直接把用户挡在门外，得先让用量统计跑准一段时间。
// 拦截点留在 upload_chunked 的 init（TODO 见 EnforceQuota）。

type storageRow struct {
	model.StorageConfig
	Username    string  `json:"username"`
	UsedPercent float64 `json:"used_percent"`
}

// ListStorage GET /v1/admin/storage —— 每个用户的配额与实时用量。
func (h *APIHandler) ListStorage(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	p := parsePage(c)

	var total int64
	h.db.Model(&model.User{}).Count(&total)

	var users []model.User
	if err := h.db.Select("id, username").Order("id asc").
		Offset(p.offset()).Limit(p.PageSize).Find(&users).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}

	rows := make([]storageRow, 0, len(users))
	for _, u := range users {
		cfg := h.ensureConfig(u.ID)
		row := storageRow{StorageConfig: cfg, Username: u.Username}
		if cfg.TotalQuota > 0 {
			row.UsedPercent = float64(cfg.UsedQuota) / float64(cfg.TotalQuota) * 100
		}
		rows = append(rows, row)
	}
	jsonPage(c, rows, total, p)
}

// MyStorage GET /v1/storage/mine —— 当前用户自己的配额与用量（普通用户可看）。
func (h *APIHandler) MyStorage(c *gin.Context) {
	uid, ok := currentUser(c)
	if !ok {
		return
	}
	cfg := h.ensureConfig(uid)
	percent := 0.0
	if cfg.TotalQuota > 0 {
		percent = float64(cfg.UsedQuota) / float64(cfg.TotalQuota) * 100
	}
	jsonOK(c, gin.H{"config": cfg, "used_percent": percent})
}

// UpdateQuota PUT /v1/admin/storage/:user_id —— 改某用户的配额上限。
func (h *APIHandler) UpdateQuota(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		TotalQuota int64 `json:"total_quota"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}
	if req.TotalQuota < 0 {
		jsonErr(c, 400, "配额不能为负（0 表示不限）")
		return
	}
	cfg := h.ensureConfig(uint(id))
	if err := h.db.Model(&cfg).Update("total_quota", req.TotalQuota).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	jsonOK(c, nil)
}

// RecalcStorage POST /v1/admin/storage/:user_id/recalc —— 按 file 表重算用量。
//
// 用量的权威来源是 file 表（未删除行的 file_size 之和）。之所以要有「重算」而不是
// 每次上传增量累加：增量一旦漏一次（进程重启/失败回滚）就永久偏了，必须有个能对齐的入口。
func (h *APIHandler) RecalcStorage(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	used, count := h.calcUsage(uint(id))
	cfg := h.ensureConfig(uint(id))
	now := time.Now()
	if err := h.db.Model(&cfg).Updates(map[string]interface{}{
		"used_quota": used, "file_count": count, "last_sync": now,
	}).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	jsonOK(c, gin.H{"used_quota": used, "file_count": count})
}

// ensureConfig 取用户的配额行，没有就按默认值建一行。
func (h *APIHandler) ensureConfig(userID uint) model.StorageConfig {
	var cfg model.StorageConfig
	err := h.db.Where("user_id = ?", userID).First(&cfg).Error
	if err == nil {
		return cfg
	}
	if err != gorm.ErrRecordNotFound {
		return cfg
	}
	used, count := h.calcUsage(userID)
	now := time.Now()
	cfg = model.StorageConfig{
		UserID: userID, TotalQuota: 10 << 30, // 默认 10GiB，与建表默认值一致
		UsedQuota: used, FileCount: count, LastSync: &now,
	}
	h.db.Create(&cfg)
	return cfg
}

// calcUsage 按 file 表算某用户的占用字节与文件数（不含目录与已删除）。
func (h *APIHandler) calcUsage(userID uint) (int64, int) {
	var result struct {
		Total int64
		Cnt   int64
	}
	h.db.Model(&model.File{}).
		Select("COALESCE(SUM(file_size),0) as total, COUNT(*) as cnt").
		Where("user_id = ? AND is_deleted = ? AND is_directory = ?", userID, false, false).
		Scan(&result)
	return result.Total, int(result.Cnt)
}
