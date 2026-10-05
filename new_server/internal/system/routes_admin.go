package system

import (
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/internal/model"
	"syc-file/pkg/logger"
)

// 路由管理里「新增 / 删除 / 结构性编辑」。仅超级管理员。
//
// 服务器能下发的是「入口」，页面代码仍在客户端里：Component 必须是客户端页面注册表里的 key
// （相对 src/ 的 .vue 路径）。服务端没法知道客户端到底有哪些页面，只校验格式；
// 客户端找不到对应组件的条目会被忽略，所以填错只会让这个入口不出现，不会让客户端崩。

var (
	routeCodeRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,62}$`)
	routePathRe = regexp.MustCompile(`^/[A-Za-z0-9_./-]*$`)
	routeNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	routeCompRe = regexp.MustCompile(`^views/[A-Za-z0-9_./-]+\.vue$`)
	nonAlnumRe  = regexp.MustCompile(`[^A-Za-z0-9]+`)
)

// 客户端内置的基础路由（登录 / 注册 / 初始化 / 兜底页），下发的路由不能占用它们的 path 和路由名，
// 否则会盖住登录页之类，把所有人锁在门外。
var reservedPaths = map[string]bool{
	"/": true, "/login": true, "/register": true, "/reset": true, "/init": true, "/empty": true,
}
var reservedNames = map[string]bool{
	"Home": true, "Empty": true, "Login": true, "Register": true, "ResetPassword": true, "Init": true, "NotFound": true,
}

// customRouteName 没填路由名时按编码生成：Custom + 编码各段首字母大写（foo.bar-baz → CustomFooBarBaz）。
func customRouteName(code string) string {
	var b strings.Builder
	b.WriteString("Custom")
	for _, seg := range nonAlnumRe.Split(code, -1) {
		if seg == "" {
			continue
		}
		b.WriteString(strings.ToUpper(seg[:1]))
		b.WriteString(seg[1:])
	}
	return b.String()
}

func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return p
}

func validateBasicPath(p string) string {
	if len(p) > 200 || !routePathRe.MatchString(p) || strings.Contains(p, "..") || strings.Contains(p, "//") {
		return "path 必须以 / 开头，只能包含字母、数字和 - _ . /，且不超过 200 字符"
	}
	return ""
}

func validateComponent(c string) string {
	if c == "" {
		return ""
	}
	if len(c) > 120 || !routeCompRe.MatchString(c) || strings.Contains(c, "..") {
		return "页面组件格式应为 views/xxx/Yyy.vue（客户端页面注册表里的 key）"
	}
	return ""
}

func validatePerm(p string) string {
	if p == "" {
		return ""
	}
	if _, ok := guestAPIs[p]; !ok {
		return "未知的游客接口分组: " + p
	}
	return ""
}

// pathTaken path 是否已被别的路由占用（不区分大小写，vue-router 默认也不区分）。
func (h *Handler) pathTaken(p string, exceptID uint) bool {
	var n int64
	h.db.Model(&model.Route{}).Where("LOWER(path) = ? AND id <> ?", strings.ToLower(p), exceptID).Count(&n)
	return n > 0
}

func (h *Handler) nameTaken(name string, exceptID uint) bool {
	var n int64
	h.db.Model(&model.Route{}).Where("name = ? AND id <> ?", name, exceptID).Count(&n)
	return n > 0
}

// validateParent 父级必须是已存在的菜单分组，且不能形成环。selfCode 为空表示新建。
func (h *Handler) validateParent(selfCode, parent string) string {
	if parent == "" {
		return ""
	}
	if parent == selfCode {
		return "不能以自己为父级"
	}
	var p model.Route
	if err := h.db.Where("code = ?", parent).First(&p).Error; err != nil {
		return "父级分组不存在: " + parent
	}
	if p.Component != "" {
		return "父级必须是菜单分组，不能是页面"
	}
	seen := map[string]bool{}
	for cur := p; cur.ParentCode != ""; {
		if selfCode != "" && cur.ParentCode == selfCode {
			return "不能形成循环的父子关系"
		}
		if seen[cur.Code] {
			break
		}
		seen[cur.Code] = true
		var next model.Route
		if err := h.db.Where("code = ?", cur.ParentCode).First(&next).Error; err != nil {
			break
		}
		cur = next
	}
	return ""
}

// validatePageIdentity 页面的 path / 路由名校验（分组不需要）。
func (h *Handler) validatePageIdentity(path, name string, exceptID uint) string {
	if msg := validateBasicPath(path); msg != "" {
		return msg
	}
	if reservedPaths[strings.ToLower(path)] {
		return "path 与客户端基础页面冲突: " + path
	}
	if h.pathTaken(path, exceptID) {
		return "path 已被其它路由使用: " + path
	}
	if !routeNameRe.MatchString(name) {
		return "路由名需以字母开头，只能包含字母、数字、下划线"
	}
	if reservedNames[name] {
		return "路由名与客户端基础页面冲突: " + name
	}
	if h.nameTaken(name, exceptID) {
		return "路由名已被使用: " + name
	}
	return ""
}

// CreateRoute POST /v1/admin/routes —— 新增自定义路由（页面或菜单分组）。
func (h *Handler) CreateRoute(c *gin.Context) {
	if !requireLevel(c, model.LevelSuper) {
		return
	}
	var req struct {
		Code       string `json:"code"`
		ParentCode string `json:"parent_code"`
		Path       string `json:"path"`
		Name       string `json:"name"`
		Component  string `json:"component"` // 空 = 菜单分组
		Title      string `json:"title"`
		Icon       string `json:"icon"`
		Sort       int    `json:"sort"`
		Hidden     bool   `json:"hidden"`
		MinLevel   int8   `json:"min_level"`
		Perm       string `json:"perm"`
		Enabled    *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}
	req.Code = strings.TrimSpace(req.Code)
	req.Title = strings.TrimSpace(req.Title)
	req.Component = strings.TrimSpace(req.Component)
	req.Name = strings.TrimSpace(req.Name)
	req.Path = normalizePath(req.Path)
	if !routeCodeRe.MatchString(req.Code) {
		jsonErr(c, 400, "编码需为 2~63 位小写字母 / 数字 / . _ -，且以字母或数字开头")
		return
	}
	var n int64
	h.db.Model(&model.Route{}).Where("code = ?", req.Code).Count(&n)
	if n > 0 {
		jsonErr(c, 400, "编码已存在: "+req.Code)
		return
	}
	if req.Title == "" || len([]rune(req.Title)) > 64 {
		jsonErr(c, 400, "标题不能为空，且不超过 64 个字符")
		return
	}
	if req.MinLevel == 0 {
		req.MinLevel = model.LevelUser
	}
	if req.MinLevel < model.LevelUser || req.MinLevel > model.LevelSuper {
		jsonErr(c, 400, "min_level 只能是 1(用户) / 2(管理员) / 3(超级管理员)")
		return
	}
	if msg := validateComponent(req.Component); msg != "" {
		jsonErr(c, 400, msg)
		return
	}
	if msg := h.validateParent("", strings.TrimSpace(req.ParentCode)); msg != "" {
		jsonErr(c, 400, msg)
		return
	}
	if msg := validatePerm(req.Perm); msg != "" {
		jsonErr(c, 400, msg)
		return
	}

	isPage := req.Component != ""
	if isPage {
		if req.Path == "" {
			jsonErr(c, 400, "页面必须填写 path")
			return
		}
		if req.Name == "" {
			req.Name = customRouteName(req.Code)
		}
		if msg := h.validatePageIdentity(req.Path, req.Name, 0); msg != "" {
			jsonErr(c, 400, msg)
			return
		}
	} else {
		// 菜单分组：不是真实页面，path 只作标识，没填就用 /<code>；不需要路由名，也不放行任何游客接口
		if req.Path == "" {
			req.Path = "/" + req.Code
		}
		if msg := validateBasicPath(req.Path); msg != "" {
			jsonErr(c, 400, msg)
			return
		}
		req.Name, req.Perm = "", ""
	}

	r := model.Route{
		Code: req.Code, ParentCode: strings.TrimSpace(req.ParentCode), Path: req.Path, Name: req.Name,
		Component: req.Component, Title: req.Title, Icon: req.Icon, Sort: req.Sort,
		Hidden: req.Hidden, MinLevel: req.MinLevel, Perm: req.Perm, Enabled: true, Builtin: false,
	}
	if err := h.db.Create(&r).Error; err != nil {
		logger.Logger.Error("新增路由失败", zap.Error(err))
		jsonErr(c, 500, "新增失败: "+err.Error())
		return
	}
	// enabled 的 gorm 默认值是 true，传 false 会被当零值套成 true，必须显式更新
	if req.Enabled != nil && !*req.Enabled {
		if err := h.db.Model(&r).Update("enabled", false).Error; err != nil {
			jsonErr(c, 500, err.Error())
			return
		}
		r.Enabled = false
	}
	logger.Logger.Info("已新增自定义路由", zap.String("code", r.Code), zap.String("path", r.Path), zap.Uint("by", callerID(c)))
	jsonOK(c, r)
}

// DeleteRoute DELETE /v1/admin/routes/:id —— 只能删自定义路由，且其下不能还有子路由。
func (h *Handler) DeleteRoute(c *gin.Context) {
	if !requireLevel(c, model.LevelSuper) {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var r model.Route
	if err := h.db.First(&r, id).Error; err != nil {
		jsonErr(c, 404, "路由不存在")
		return
	}
	if r.Builtin {
		jsonErr(c, 400, "内置路由不能删除，可以停用它")
		return
	}
	var children int64
	h.db.Model(&model.Route{}).Where("parent_code = ?", r.Code).Count(&children)
	if children > 0 {
		jsonErr(c, 400, "该分组下还有子路由，请先删除或移走它们")
		return
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		// 被授权给访客的记录一并清掉，否则会留下指向不存在路由的孤儿授权
		if err := tx.Where("route_code = ?", r.Code).Delete(&model.UserRoute{}).Error; err != nil {
			return err
		}
		return tx.Delete(&r).Error
	}); err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	logger.Logger.Info("已删除自定义路由", zap.String("code", r.Code), zap.Uint("by", callerID(c)))
	jsonOK(c, nil)
}

// RoutePerms GET /v1/admin/route-perms —— 可选的「游客接口分组」，供新增 / 编辑路由时下拉选择。
func (h *Handler) RoutePerms(c *gin.Context) {
	if !requireLevel(c, model.LevelSuper) {
		return
	}
	type item struct {
		Key   string `json:"key"`
		Label string `json:"label"`
	}
	out := make([]item, 0, len(guestAPIs))
	for k := range guestAPIs {
		out = append(out, item{Key: k, Label: guestPermLabels[k]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	jsonOK(c, out)
}

// applyStructuralEdits 处理自定义路由的结构性编辑，把要写库的字段放进 updates。成功返回空串，否则返回错误文案。
// 注意 r 是更新前的状态：页面 / 分组的类型不允许互转，所以 r.Component 是否为空在整个过程中不变。
func (h *Handler) applyStructuralEdits(r *model.Route, path, comp, parent, name, perm *string, updates map[string]interface{}) string {
	isPage := r.Component != ""

	if comp != nil {
		c := strings.TrimSpace(*comp)
		if msg := validateComponent(c); msg != "" {
			return msg
		}
		if (c != "") != isPage {
			return "不能在页面和菜单分组之间转换，请删除后重新创建"
		}
		updates["component"] = c
	}

	newPath, newName := r.Path, r.Name
	if path != nil {
		newPath = normalizePath(*path)
	}
	if name != nil {
		newName = strings.TrimSpace(*name)
	}
	if path != nil || name != nil {
		if isPage {
			if msg := h.validatePageIdentity(newPath, newName, r.ID); msg != "" {
				return msg
			}
		} else {
			if msg := validateBasicPath(newPath); msg != "" {
				return msg
			}
			if newName != "" {
				return "菜单分组没有路由名"
			}
		}
		if path != nil {
			updates["path"] = newPath
		}
		if name != nil {
			updates["name"] = newName
		}
	}

	if parent != nil {
		p := strings.TrimSpace(*parent)
		if msg := h.validateParent(r.Code, p); msg != "" {
			return msg
		}
		updates["parent_code"] = p
	}

	if perm != nil {
		if msg := validatePerm(*perm); msg != "" {
			return msg
		}
		if !isPage && *perm != "" {
			return "菜单分组不能设置游客接口分组"
		}
		updates["perm"] = *perm
	}
	return ""
}
