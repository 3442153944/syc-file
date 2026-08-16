package admin

import (
	"strings"

	"github.com/gin-gonic/gin"

	"syc-file/internal/model"
)

// RBAC：role / permission / role_permission / user_role 四张表建库就有，
// 但从来没有业务代码，middleware.RequireRole 也没接线（ARCHITECTURE §6.3 的公网安全项）。
//
// 现阶段的鉴权仍以 user.role 这个字符串字段为准（token 里的 roles 由它签发），
// 本组接口先把「角色/权限怎么配」这层管起来，并提供 EffectivePermissions 供后续
// 把 RequireRole/RequirePermission 逐个挂到路由上。**改配置不会立刻改变现有鉴权行为**，
// 这一点必须清楚，否则会误以为配了就生效了。

type roleWithPerms struct {
	model.Role
	Permissions []model.Permission `json:"permissions"`
	UserCount   int64              `json:"user_count"`
}

// ListRoles GET /v1/admin/roles —— 角色 + 其权限 + 绑定人数。
func (h *APIHandler) ListRoles(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	var roles []model.Role
	if err := h.db.Order("id asc").Find(&roles).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	out := make([]roleWithPerms, 0, len(roles))
	for _, r := range roles {
		item := roleWithPerms{Role: r, Permissions: h.permissionsOfRole(r.ID)}
		h.db.Model(&model.UserRole{}).Where("role_id = ?", r.ID).Count(&item.UserCount)
		out = append(out, item)
	}
	jsonOK(c, out)
}

// CreateRole POST /v1/admin/roles
func (h *APIHandler) CreateRole(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	var req struct {
		RoleCode    string `json:"role_code"`
		RoleName    string `json:"role_name"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}
	req.RoleCode = strings.TrimSpace(req.RoleCode)
	if req.RoleCode == "" || strings.TrimSpace(req.RoleName) == "" {
		jsonErr(c, 400, "role_code 与 role_name 必填")
		return
	}
	role := model.Role{RoleCode: req.RoleCode, RoleName: req.RoleName, Status: 1}
	if req.Description != "" {
		role.Description = &req.Description
	}
	if err := h.db.Create(&role).Error; err != nil {
		jsonErr(c, 500, "创建失败（role_code 可能已存在）: "+err.Error())
		return
	}
	jsonOK(c, role)
}

// UpdateRole PUT /v1/admin/roles/:id —— 改名/描述/启停，并可整体覆盖权限集合。
func (h *APIHandler) UpdateRole(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		RoleName      *string `json:"role_name"`
		Description   *string `json:"description"`
		Status        *int8   `json:"status"`
		PermissionIDs *[]uint `json:"permission_ids"` // 传了就整体替换
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}
	var role model.Role
	if err := h.db.First(&role, id).Error; err != nil {
		jsonErr(c, 404, "角色不存在")
		return
	}
	updates := map[string]interface{}{}
	if req.RoleName != nil {
		updates["role_name"] = *req.RoleName
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if len(updates) > 0 {
		if err := h.db.Model(&role).Updates(updates).Error; err != nil {
			jsonErr(c, 500, err.Error())
			return
		}
	}
	if req.PermissionIDs != nil {
		h.db.Where("role_id = ?", role.ID).Delete(&model.RolePermission{})
		for _, pid := range *req.PermissionIDs {
			h.db.Create(&model.RolePermission{RoleID: role.ID, PermissionID: pid})
		}
	}
	jsonOK(c, nil)
}

// DeleteRole DELETE /v1/admin/roles/:id —— 连带清掉它的权限绑定与用户绑定。
func (h *APIHandler) DeleteRole(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var role model.Role
	if err := h.db.First(&role, id).Error; err != nil {
		jsonErr(c, 404, "角色不存在")
		return
	}
	if role.RoleCode == "admin" {
		jsonErr(c, 400, "内置管理员角色不可删除")
		return
	}
	if err := h.db.Delete(&role).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	h.db.Where("role_id = ?", id).Delete(&model.RolePermission{})
	h.db.Where("role_id = ?", id).Delete(&model.UserRole{})
	jsonOK(c, nil)
}

// ListPermissions GET /v1/admin/permissions
func (h *APIHandler) ListPermissions(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	var perms []model.Permission
	if err := h.db.Order("sort_order asc, id asc").Find(&perms).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	jsonOK(c, perms)
}

// CreatePermission POST /v1/admin/permissions
func (h *APIHandler) CreatePermission(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	var req struct {
		PermissionCode string `json:"permission_code"`
		PermissionName string `json:"permission_name"`
		PermissionType string `json:"permission_type"`
		Description    string `json:"description"`
		SortOrder      int    `json:"sort_order"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}
	if strings.TrimSpace(req.PermissionCode) == "" || strings.TrimSpace(req.PermissionName) == "" {
		jsonErr(c, 400, "permission_code 与 permission_name 必填")
		return
	}
	p := model.Permission{
		PermissionCode: req.PermissionCode, PermissionName: req.PermissionName,
		SortOrder: req.SortOrder, Status: 1,
	}
	if req.PermissionType != "" {
		p.PermissionType = &req.PermissionType
	}
	if req.Description != "" {
		p.Description = &req.Description
	}
	if err := h.db.Create(&p).Error; err != nil {
		jsonErr(c, 500, "创建失败（code 可能已存在）: "+err.Error())
		return
	}
	jsonOK(c, p)
}

// DeletePermission DELETE /v1/admin/permissions/:id
func (h *APIHandler) DeletePermission(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := h.db.Delete(&model.Permission{}, id).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	h.db.Where("permission_id = ?", id).Delete(&model.RolePermission{})
	jsonOK(c, nil)
}

// AssignUserRoles PUT /v1/admin/users/:id/roles —— 整体替换某用户的角色绑定。
//
// ⚠ 同时会把 user.role 这个字符串字段同步成「是否含 admin 角色」，
// 因为登录签发 token 用的是它——只改关联表而不改这个字段，等于配了个不生效的权限。
func (h *APIHandler) AssignUserRoles(c *gin.Context) {
	adminID, ok := requireAdmin(c)
	if !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		RoleIDs []uint `json:"role_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}
	var user model.User
	if err := h.db.First(&user, id).Error; err != nil {
		jsonErr(c, 404, "用户不存在")
		return
	}

	isAdmin := false
	for _, rid := range req.RoleIDs {
		var r model.Role
		if err := h.db.First(&r, rid).Error; err == nil && r.RoleCode == "admin" {
			isAdmin = true
		}
	}
	if uint(id) == adminID && !isAdmin {
		jsonErr(c, 400, "不能撤销自己的管理员角色")
		return
	}

	h.db.Where("user_id = ?", id).Delete(&model.UserRole{})
	for _, rid := range req.RoleIDs {
		h.db.Create(&model.UserRole{UserID: uint(id), RoleID: rid})
	}
	role := "user"
	if isAdmin {
		role = "admin"
	}
	h.db.Model(&user).Update("role", role)
	jsonOK(c, gin.H{"role": role, "note": "角色变更在用户下次登录换发 token 后完全生效"})
}

// UserRoles GET /v1/admin/users/:id/roles
func (h *APIHandler) UserRoles(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var links []model.UserRole
	h.db.Where("user_id = ?", id).Find(&links)
	ids := make([]uint, 0, len(links))
	for _, l := range links {
		ids = append(ids, l.RoleID)
	}
	roles := make([]model.Role, 0, len(ids))
	if len(ids) > 0 {
		h.db.Where("id IN ?", ids).Find(&roles)
	}
	jsonOK(c, roles)
}

// permissionsOfRole 某角色的权限列表。
func (h *APIHandler) permissionsOfRole(roleID uint) []model.Permission {
	var links []model.RolePermission
	h.db.Where("role_id = ?", roleID).Find(&links)
	if len(links) == 0 {
		return []model.Permission{}
	}
	ids := make([]uint, 0, len(links))
	for _, l := range links {
		ids = append(ids, l.PermissionID)
	}
	var perms []model.Permission
	h.db.Where("id IN ?", ids).Order("sort_order asc").Find(&perms)
	return perms
}
