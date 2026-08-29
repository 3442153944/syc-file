package admin

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"syc-file/internal/handler/file"
)

// 缓存管理：粘贴快传（quick-share）的缓存，管理员跨用户查看/手动清理，不用等自动过期。
// 名字里带"内存"是产品语义（用户视角看到的是"粘贴到内存里的快传"），实际列表也带上了
// 落盘的那部分——两者共用同一套过期/吊销生命周期，分开看意义不大，落盘的那部分同样
// 占着用户的 quick_share 配额，管理员也需要能清。

// quickShareRow 缓存管理列表行：share_link 表 join user 表拿用户名。
type quickShareRow struct {
	ID          uint64 `json:"id"`
	ShareCode   string `json:"share_code"`
	UserID      uint   `json:"user_id"`
	Username    string `json:"username"`
	FileName    string `json:"file_name"`
	FileSize    int64  `json:"file_size"`
	StorageKind string `json:"storage_kind"`
	ExpireTime  string `json:"expire_time"`
	CreatedAt   string `json:"created_at"`
}

// ListQuickShare GET /v1/admin/quick-share —— 所有用户当前有效的粘贴快传缓存，
// 支持 ?username= 按用户名模糊过滤，看某个具体用户占了多少。
func (h *APIHandler) ListQuickShare(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	p := parsePage(c)

	// status = 1：即 file 包里的 shareStatusActive，只看仍然有效的（已过期/已吊销的
	// 没有实际缓存内容了，管理页面看这些没有意义）
	base := h.db.Table("share_link").
		Joins("JOIN user ON user.id = share_link.user_id").
		Where("share_link.is_quick_share = ? AND share_link.status = ?", true, 1)
	if username := c.Query("username"); username != "" {
		base = base.Where("user.username LIKE ?", "%"+username+"%")
	}

	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}

	var rows []quickShareRow
	if err := base.Session(&gorm.Session{}).
		Select("share_link.id, share_link.share_code, share_link.user_id, user.username, " +
			"share_link.file_name, share_link.file_size, share_link.storage_kind, " +
			"share_link.expire_time, share_link.created_at").
		Order("share_link.created_at desc").
		Offset(p.offset()).Limit(p.PageSize).
		Scan(&rows).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	jsonPage(c, rows, total, p)
}

// RevokeQuickShare DELETE /v1/admin/quick-share/:code —— 强制清掉任意用户的一条
// 粘贴快传缓存（删内存内容/临时文件 + 标记失效 + 清 Redis），立即生效，不用等自动过期。
func (h *APIHandler) RevokeQuickShare(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	code := c.Param("code")
	if code == "" {
		jsonErr(c, 400, "缺少 share_code")
		return
	}
	if err := file.AdminDestroyShareLink(h.db, h.redisClient, code); err != nil {
		jsonErr(c, 404, "缓存不存在或已失效")
		return
	}
	jsonOK(c, nil)
}
