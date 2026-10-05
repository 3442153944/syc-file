package system

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"syc-file/internal/middleware"
	"syc-file/internal/model"
	"syc-file/pkg/token"
)

// routeDTO 下发给客户端的路由条目（不含内部字段）。
type routeDTO struct {
	Code       string `json:"code"`
	ParentCode string `json:"parent_code"`
	Path       string `json:"path"`
	Name       string `json:"name"`
	Component  string `json:"component"`
	Title      string `json:"title"`
	Icon       string `json:"icon"`
	Sort       int    `json:"sort"`
	Hidden     bool   `json:"hidden"`
}

// MyRoutes GET /v1/routes（免登录入口，未登录返回空表）
//
// 返回当前调用者可见的路由，平铺 + parent_code，客户端自己拼树。
//   - 用户 / 管理员 / 超级管理员：enabled 且 min_level <= 自己级别的页面；
//   - 游客：只有被明确授权（user_route）的页面；
//   - 目录只在其下至少有一个可见页面时才出现；任一祖先被停用，整棵子树都不出现。
func (h *Handler) MyRoutes(c *gin.Context) {
	auth, _ := c.Get("Auth")
	claimsAny, ok := c.Get("UserInfo")
	if auth != true || !ok {
		jsonOK(c, gin.H{"logged_in": false, "level": 0, "version": "", "routes": []routeDTO{}})
		return
	}
	claims := claimsAny.(*token.Claims)
	level := middleware.LevelOf(c)

	var all []model.Route
	if err := h.db.Where("enabled = ?", true).Order("sort asc, id asc").Find(&all).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}

	granted := map[string]bool{}
	if level == model.LevelGuest {
		var rows []model.UserRoute
		h.db.Where("user_id = ?", claims.UserID).Find(&rows)
		for _, r := range rows {
			granted[r.RouteCode] = true
		}
	}

	routes := visibleRoutes(all, level, granted)
	b, _ := json.Marshal(routes)
	sum := sha1.Sum(b)
	jsonOK(c, gin.H{
		"logged_in": true,
		"level":     level,
		"version":   hex.EncodeToString(sum[:])[:12],
		"routes":    routes,
	})
}

// visibleRoutes 纯函数：从已启用的全部路由里筛出某级别可见的那部分。
func visibleRoutes(all []model.Route, level int8, granted map[string]bool) []routeDTO {
	byCode := make(map[string]model.Route, len(all))
	for _, r := range all {
		byCode[r.Code] = r
	}
	// 祖先链上任何一个被停用（不在 byCode 里）或成环，都视为不可见
	chainOK := func(r model.Route) bool {
		seen := map[string]bool{r.Code: true}
		for p := r.ParentCode; p != ""; {
			pr, ok := byCode[p]
			if !ok || seen[p] {
				return false
			}
			seen[p] = true
			p = pr.ParentCode
		}
		return true
	}

	keep := map[string]bool{}
	for _, r := range all {
		if r.Component == "" || !chainOK(r) {
			continue // 目录先不判，等有可见子页面时再带上
		}
		if level == model.LevelGuest {
			if !granted[r.Code] {
				continue
			}
		} else if r.MinLevel > level {
			continue
		}
		keep[r.Code] = true
		for p := r.ParentCode; p != ""; p = byCode[p].ParentCode {
			keep[p] = true
		}
	}

	out := make([]routeDTO, 0, len(keep))
	for _, r := range all { // all 已按 sort 排序
		if !keep[r.Code] {
			continue
		}
		out = append(out, routeDTO{
			Code: r.Code, ParentCode: r.ParentCode, Path: r.Path, Name: r.Name,
			Component: r.Component, Title: r.Title, Icon: r.Icon, Sort: r.Sort, Hidden: r.Hidden,
		})
	}
	return out
}

// GuestGrantableRoutes GET /v1/admin/guest-routes —— 可以授权给访客的页面（启用、是真页面、普通用户级别即可见），
// 管理员及以上可读，供「访客管理」勾选。与 validateGuestRoutes 的校验口径一致。
func (h *Handler) GuestGrantableRoutes(c *gin.Context) {
	if !requireLevel(c, model.LevelAdmin) {
		return
	}
	var all []model.Route
	if err := h.db.Where("enabled = ?", true).Order("sort asc, id asc").Find(&all).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	out := make([]routeDTO, 0, len(all))
	for _, r := range all {
		if r.Component == "" || r.MinLevel > model.LevelUser {
			continue
		}
		out = append(out, routeDTO{
			Code: r.Code, ParentCode: r.ParentCode, Path: r.Path, Name: r.Name,
			Component: r.Component, Title: r.Title, Icon: r.Icon, Sort: r.Sort, Hidden: r.Hidden,
		})
	}
	jsonOK(c, out)
}

// AdminListRoutes GET /v1/admin/routes —— 全部路由（含停用的），仅超级管理员。
func (h *Handler) AdminListRoutes(c *gin.Context) {
	if !requireLevel(c, model.LevelSuper) {
		return
	}
	var all []model.Route
	if err := h.db.Order("sort asc, id asc").Find(&all).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	jsonOK(c, all)
}

// AdminUpdateRoute PUT /v1/admin/routes/:id —— 仅超级管理员。
//
// 所有路由都能改：标题 / 图标 / 排序 / 是否在菜单显示 / 最低级别 / 启停。
// 自定义路由（builtin=false）还能改：path / 页面组件 / 父级分组 / 路由名 / 游客接口分组。
// 内置路由的这几项客户端依赖，不允许改。
func (h *Handler) AdminUpdateRoute(c *gin.Context) {
	if !requireLevel(c, model.LevelSuper) {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		Title    *string `json:"title"`
		Icon     *string `json:"icon"`
		Sort     *int    `json:"sort"`
		Hidden   *bool   `json:"hidden"`
		MinLevel *int8   `json:"min_level"`
		Enabled  *bool   `json:"enabled"`
		// 以下仅自定义路由可改
		Path       *string `json:"path"`
		Component  *string `json:"component"`
		ParentCode *string `json:"parent_code"`
		Name       *string `json:"name"`
		Perm       *string `json:"perm"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}
	var r model.Route
	if err := h.db.First(&r, id).Error; err != nil {
		jsonErr(c, 404, "路由不存在")
		return
	}
	updates := map[string]interface{}{}
	if req.Path != nil || req.Component != nil || req.ParentCode != nil || req.Name != nil || req.Perm != nil {
		if r.Builtin {
			jsonErr(c, 400, "内置路由不能修改 path / 页面组件 / 父级 / 路由名 / 接口分组（只能停用或调整展示）")
			return
		}
		if msg := h.applyStructuralEdits(&r, req.Path, req.Component, req.ParentCode, req.Name, req.Perm, updates); msg != "" {
			jsonErr(c, 400, msg)
			return
		}
	}
	if req.Title != nil {
		if strings.TrimSpace(*req.Title) == "" {
			jsonErr(c, 400, "标题不能为空")
			return
		}
		updates["title"] = strings.TrimSpace(*req.Title)
	}
	if req.Icon != nil {
		updates["icon"] = *req.Icon
	}
	if req.Sort != nil {
		updates["sort"] = *req.Sort
	}
	if req.Hidden != nil {
		updates["hidden"] = *req.Hidden
	}
	if req.MinLevel != nil {
		if *req.MinLevel < model.LevelUser || *req.MinLevel > model.LevelSuper {
			jsonErr(c, 400, "min_level 只能是 1(用户) / 2(管理员) / 3(超级管理员)")
			return
		}
		updates["min_level"] = *req.MinLevel
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if len(updates) == 0 {
		jsonErr(c, 400, "没有需要更新的字段")
		return
	}
	if err := h.db.Model(&r).Updates(updates).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	jsonOK(c, nil)
}

// requireLevel 调用者级别不足则写 403 并返回 false。
func requireLevel(c *gin.Context, min int8) bool {
	if auth, _ := c.Get("Auth"); auth != true {
		jsonErr(c, 401, "未授权")
		return false
	}
	if middleware.LevelOf(c) < min {
		jsonErr(c, 403, "权限不足")
		return false
	}
	return true
}

func callerID(c *gin.Context) uint {
	if v, ok := c.Get("UserInfo"); ok {
		return uint(v.(*token.Claims).UserID)
	}
	return 0
}

func idParam(c *gin.Context) (uint64, bool) {
	var id uint64
	for _, ch := range c.Param("id") {
		if ch < '0' || ch > '9' {
			jsonErr(c, 400, "无效的 ID")
			return 0, false
		}
		id = id*10 + uint64(ch-'0')
	}
	if id == 0 {
		jsonErr(c, 400, "无效的 ID")
		return 0, false
	}
	return id, true
}

var _ = gorm.ErrRecordNotFound
