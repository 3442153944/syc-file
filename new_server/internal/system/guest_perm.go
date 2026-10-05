package system

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"syc-file/internal/model"
)

// 游客的接口放行规则：**默认拒绝**。
//
// 游客被授权一个页面，只代表他能看到这个页面入口；页面背后调的接口仍要放行，否则页面是空的。
// 页面 → 接口的对应关系由 Route.Perm 指向下面的分组，一处维护：
// 新增一个允许游客使用的页面时，在 guestAPIs 里加上它要调用的接口前缀即可。
// 没出现在这里的接口（含全部 /admin、删除、同步上报等），游客一律 403。

// guestAlways 任何游客都能调的接口：自检、取自己的路由表、公开分享下载。
var guestAlways = []string{
	"/v1/ping",
	"/v1/user/verify",
	"/v1/routes",
	"/v1/system/status",
	"/v1/file/share-link/download/",
}

// guestAPIs perm → 接口路径前缀。
var guestAPIs = map[string][]string{
	"dashboard":        {"/v1/file/available-disks", "/v1/file/download-history", "/v1/file/thumbnail", "/v1/monitor/system", "/v1/ws/connect", "/v1/ws/my-devices"},
	"file.browse":      {"/v1/file/available-disks", "/v1/file/traverse-directory", "/v1/file/download", "/v1/file/thumbnail", "/v1/file/text/read"},
	"file.upload":      {"/v1/file/upload", "/v1/file/text/save"},
	"share":            {"/v1/file/share-link/"},
	"share.quick":      {"/v1/file/quick-share/", "/v1/user/quick-share-settings"},
	"sync":             {"/v1/sync/", "/v1/ws/connect"},
	"clipboard":        {"/v1/clipboard/", "/v1/ws/connect"},
	"monitor":          {"/v1/monitor/", "/v1/ws/connect"},
	"transfers":        {"/v1/file/download-history", "/v1/file/thumbnail"},
	"account.password": {"/v1/user/change-password"},
}

// guestPermLabels perm 的中文名，供「路由管理」新增 / 编辑路由时下拉选择（只能选这里有的，不能凭空编造分组）。
var guestPermLabels = map[string]string{
	"dashboard":        "首页（磁盘概览 / 监控 / 在线设备）",
	"file.browse":      "浏览与下载文件",
	"file.upload":      "上传文件",
	"share":            "分享链接",
	"share.quick":      "快速分享",
	"sync":             "文件同步",
	"clipboard":        "剪贴板同步",
	"monitor":          "系统监控",
	"transfers":        "传输列表",
	"account.password": "修改密码",
}

// guestAlive 供 middleware.SetGuestAlive 使用：访客账号此刻是否仍有效（启用、未过期、仍是访客）。
// 每个访客请求都查库——访客账号数量很少，换来「禁用 / 到期 / 删除立刻生效」，不必等 token 过期。
func (h *Handler) guestAlive(userID int64) bool {
	var u model.User
	if err := h.db.Select("id", "status", "level", "expires_at").First(&u, userID).Error; err != nil {
		return false
	}
	if u.Status != 1 || u.Level != model.LevelGuest {
		return false
	}
	return u.ExpiresAt != nil && time.Now().Before(*u.ExpiresAt)
}

// guestGuard 供 middleware.SetGuestGuard 使用：访客请求的接口放行判定（默认拒绝）。
// 账号有效性已在认证阶段由 guestAlive 检查过，这里只看路径。
func (h *Handler) guestGuard(c *gin.Context, userID int64) bool {
	path := c.Request.URL.Path
	for _, p := range guestAlways {
		if strings.HasPrefix(path, p) {
			return true
		}
	}

	var codes []string
	h.db.Model(&model.UserRoute{}).Where("user_id = ?", userID).Pluck("route_code", &codes)
	if len(codes) == 0 {
		return false
	}
	var perms []string
	h.db.Model(&model.Route{}).Where("code IN ? AND enabled = ?", codes, true).Pluck("perm", &perms)
	for _, perm := range perms {
		for _, p := range guestAPIs[perm] {
			if strings.HasPrefix(path, p) {
				return true
			}
		}
	}
	return false
}

// lookup 供 middleware.SetUserLookup 使用：续期 token 时取账号最新的角色 / 级别 / 状态。
func (h *Handler) lookup(userID int64) (role string, level int8, status int8, ok bool) {
	var u model.User
	if err := h.db.Select("id", "role", "level", "status").First(&u, userID).Error; err != nil {
		return "", 0, 0, false
	}
	return u.Role, u.Level, u.Status, true
}
