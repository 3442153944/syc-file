package vpn

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/pkg/response"
)

// ── 管理接口 ───────────────────────────────────────────────────

type orgReq struct {
	Name        string `json:"name"`
	OwnerUserID uint   `json:"owner_user_id"`
}

// CreateOrg 仅超级管理员可建机构（D4：跨机构永不互通）。
func (a *API) CreateOrg(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	if u.Level < model.LevelSuper {
		response.Fail(c, 403, "仅系统管理员可创建机构")
		return
	}
	var req orgReq
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		response.Fail(c, 400, "name 必填")
		return
	}
	owner := req.OwnerUserID
	if owner == 0 {
		owner = u.ID
	}
	org := model.VPNOrg{Name: strings.TrimSpace(req.Name), OwnerUserID: owner}
	if err := a.db.Create(&org).Error; err != nil {
		response.Fail(c, 400, "机构创建失败（名称可能重复）")
		return
	}
	response.OK(c, org)
}

// ListOrgs 超管看全部；其他人只看自己担任 org_admin 的机构。
func (a *API) ListOrgs(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	var orgs []model.VPNOrg
	q := a.db.Order("id")
	if u.Level < model.LevelSuper {
		q = q.Where("id IN (?)",
			a.db.Table("vpn_group AS g").
				Select("g.org_id").
				Joins("JOIN vpn_member AS m ON m.group_id = g.id").
				Where("m.user_id = ? AND m.role = ?", u.ID, model.VPNRoleOrgAdmin),
		)
	}
	if err := q.Find(&orgs).Error; err != nil {
		response.Fail(c, 500, "读取机构失败")
		return
	}
	response.OK(c, orgs)
}

type groupReq struct {
	OrgID  uint   `json:"org_id"`
	Name   string `json:"name"`
	Subnet string `json:"subnet"`
}

// CreateGroup 机构管理员或超管建组。
func (a *API) CreateGroup(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	var req groupReq
	if err := c.ShouldBindJSON(&req); err != nil || req.OrgID == 0 || strings.TrimSpace(req.Name) == "" {
		response.Fail(c, 400, "org_id 与 name 必填")
		return
	}
	if !a.canManageOrg(u, req.OrgID) {
		response.Fail(c, 403, "无此机构管理权限")
		return
	}
	pool := config.Conf.Vpn.Pool
	subnet := strings.TrimSpace(req.Subnet)
	if subnet == "" {
		// 未指定就从池里切一个空闲 /24（池默认 172.1.0.0/16）
		var used []string
		if err := a.db.Model(&model.VPNGroup{}).Pluck("subnet", &used).Error; err != nil {
			response.Fail(c, 500, "读取已用网段失败")
			return
		}
		allocated, err := AllocateSubnet(pool, used)
		if err != nil {
			response.Fail(c, 400, err.Error())
			return
		}
		subnet = allocated
	} else {
		if err := ValidateSubnet(subnet); err != nil {
			response.Fail(c, 400, err.Error())
			return
		}
		if !SubnetInPool(subnet, pool) {
			response.Fail(c, 400, "网段必须落在地址池 "+pool+" 内")
			return
		}
	}
	// 网段全局唯一：两个组用同一段会让不同组的设备撞地址，跨组互通时尤其危险。
	var dup int64
	if err := a.db.Model(&model.VPNGroup{}).Where("subnet = ?", subnet).Count(&dup).Error; err != nil {
		response.Fail(c, 500, "校验网段失败")
		return
	}
	if dup > 0 {
		response.Fail(c, 400, "网段 "+subnet+" 已被占用")
		return
	}
	g := model.VPNGroup{OrgID: req.OrgID, Name: strings.TrimSpace(req.Name), Subnet: subnet}
	if err := a.db.Create(&g).Error; err != nil {
		response.Fail(c, 400, "组创建失败（同机构内名称可能重复）")
		return
	}
	response.OK(c, g)
}

// ListGroups 按机构列出组；必须是该机构成员或超管。
func (a *API) ListGroups(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	var groups []model.VPNGroup
	if err := a.db.Order("id").Find(&groups).Error; err != nil {
		response.Fail(c, 500, "读取组失败")
		return
	}
	if u.Level >= model.LevelSuper {
		response.OK(c, groups)
		return
	}
	var mine []model.VPNMember
	if err := a.db.Where("user_id = ?", u.ID).Find(&mine).Error; err != nil {
		response.Fail(c, 500, "读取成员关系失败")
		return
	}
	allowed := make(map[uint]bool, len(mine))
	for _, m := range mine {
		allowed[m.GroupID] = true
	}
	out := make([]model.VPNGroup, 0, len(groups))
	for _, g := range groups {
		if allowed[g.ID] {
			out = append(out, g)
		}
	}
	response.OK(c, out)
}

type memberReq struct {
	GroupID uint   `json:"group_id"`
	UserID  uint   `json:"user_id"`
	Role    string `json:"role"`
}

// AddMember 组管理员/机构管理员/超管把用户加入组。
func (a *API) AddMember(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	var req memberReq
	if err := c.ShouldBindJSON(&req); err != nil || req.GroupID == 0 || req.UserID == 0 {
		response.Fail(c, 400, "group_id 与 user_id 必填")
		return
	}
	if req.Role == "" {
		req.Role = model.VPNRoleMember
	}
	if req.Role != model.VPNRoleMember && req.Role != model.VPNRoleGroupAdmin && req.Role != model.VPNRoleOrgAdmin {
		response.Fail(c, 400, "role 非法")
		return
	}
	if !a.canManageGroup(u, req.GroupID) {
		response.Fail(c, 403, "无此组管理权限")
		return
	}
	var target model.User
	if err := a.db.First(&target, req.UserID).Error; err != nil {
		response.Fail(c, 404, "用户不存在")
		return
	}
	var existing model.VPNMember
	err := a.db.Where("group_id = ? AND user_id = ?", req.GroupID, req.UserID).First(&existing).Error
	switch {
	case err == nil:
		existing.Role = req.Role
		a.db.Save(&existing)
	case errors.Is(err, gorm.ErrRecordNotFound):
		if err := a.db.Create(&model.VPNMember{GroupID: req.GroupID, UserID: req.UserID, Role: req.Role}).Error; err != nil {
			response.Fail(c, 500, "加入失败")
			return
		}
	default:
		response.Fail(c, 500, "读取成员失败")
		return
	}
	response.OK(c, gin.H{"ok": true})
}

// RemoveMember 移出组；同时吊销该用户在此组的设备。
func (a *API) RemoveMember(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	var req memberReq
	gid, gerr := strconv.ParseUint(c.Query("group_id"), 10, 64)
	uid, uerr := strconv.ParseUint(c.Query("user_id"), 10, 64)
	if gerr != nil || uerr != nil || gid == 0 || uid == 0 {
		response.Fail(c, 400, "group_id 与 user_id 必填")
		return
	}
	req.GroupID, req.UserID = uint(gid), uint(uid)
	if !a.canManageGroup(u, req.GroupID) {
		response.Fail(c, 403, "无此组管理权限")
		return
	}
	if err := a.db.Where("group_id = ? AND user_id = ?", req.GroupID, req.UserID).Delete(&model.VPNMember{}).Error; err != nil {
		response.Fail(c, 500, "移除失败")
		return
	}
	a.revokeGroupDevices(u.ID, req.GroupID, req.UserID, "removed")
	response.OK(c, gin.H{"ok": true})
}

// ListDevices 管理员查看设备（?group_id= 或 ?org_id=）。
func (a *API) ListDevices(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	var devices []model.VPNDevice
	q := a.db.Order("id")
	if groupID := c.Query("group_id"); groupID != "" {
		gid64, err := strconv.ParseUint(groupID, 10, 64)
		if err != nil {
			response.Fail(c, 400, "group_id 非法")
			return
		}
		gid := uint(gid64)
		if !a.canManageGroup(u, gid) {
			response.Fail(c, 403, "无此组管理权限")
			return
		}
		q = q.Where("group_id = ?", gid)
	} else if orgID := c.Query("org_id"); orgID != "" {
		oid64, err := strconv.ParseUint(orgID, 10, 64)
		if err != nil {
			response.Fail(c, 400, "org_id 非法")
			return
		}
		oid := uint(oid64)
		if !a.canManageOrg(u, oid) {
			response.Fail(c, 403, "无此机构管理权限")
			return
		}
		q = q.Where("group_id IN (?)", a.db.Model(&model.VPNGroup{}).Select("id").Where("org_id = ?", oid))
	} else if u.Level < model.LevelAdmin {
		q = q.Where("user_id = ?", u.ID)
	}
	if err := q.Find(&devices).Error; err != nil {
		response.Fail(c, 500, "读取设备失败")
		return
	}
	response.OK(c, devices)
}

// RevokeDevice 吊销设备：标记 revoked 并留历史；对端下次同步即移除该 peer。
func (a *API) RevokeDevice(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	var req struct {
		ID uint `json:"id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == 0 {
		response.Fail(c, 400, "id 必填")
		return
	}
	var dev model.VPNDevice
	if err := a.db.First(&dev, req.ID).Error; err != nil {
		response.Fail(c, 404, "设备不存在")
		return
	}
	if !a.canManageGroup(u, dev.GroupID) {
		response.Fail(c, 403, "无此组管理权限")
		return
	}
	if err := a.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&model.VPNDeviceHistory{
			UserID: dev.UserID, DeviceID: dev.DeviceID, PubKey: dev.PubKey, Reason: "revoked",
		}).Error; err != nil {
			return err
		}
		return tx.Model(&dev).Updates(map[string]any{"status": model.VPNDeviceRevoked, "priv_key_enc": ""}).Error
	}); err != nil {
		response.Fail(c, 500, "吊销失败")
		return
	}
	response.OK(c, gin.H{"ok": true})
}

// SetInterop 超管设置两个组之间的互通策略，双向生效。
func (a *API) SetInterop(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	if u.Level < model.LevelSuper {
		response.Fail(c, 403, "仅系统管理员可设置互通")
		return
	}
	var req struct {
		AGroupID uint   `json:"a_group_id"`
		BGroupID uint   `json:"b_group_id"`
		Scope    string `json:"scope"`
		Enabled  bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.AGroupID == 0 || req.BGroupID == 0 || req.AGroupID == req.BGroupID {
		response.Fail(c, 400, "两个不同的 group_id 必填")
		return
	}
	if req.Scope == "" {
		req.Scope = model.VPNInteropAdminsOnly
	}
	if req.Scope != model.VPNInteropAdminsOnly && req.Scope != model.VPNInteropFull {
		response.Fail(c, 400, "scope 只能是 admins_only 或 full")
		return
	}
	var ga, gb model.VPNGroup
	if err := a.db.First(&ga, req.AGroupID).Error; err != nil {
		response.Fail(c, 404, "A 组不存在")
		return
	}
	if err := a.db.First(&gb, req.BGroupID).Error; err != nil {
		response.Fail(c, 404, "B 组不存在")
		return
	}
	if ga.OrgID != gb.OrgID {
		response.Fail(c, 400, "只能在同一机构内设置互通")
		return
	}
	aID, bID := ga.ID, gb.ID
	if aID > bID {
		aID, bID = bID, aID
	}
	var row model.VPNInterop
	err := a.db.Where("a_group_id = ? AND b_group_id = ?", aID, bID).First(&row).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		row = model.VPNInterop{
			OrgID: ga.OrgID, AGroupID: aID, BGroupID: bID,
			Scope: req.Scope, Enabled: req.Enabled, CreatedBy: u.ID,
		}
		if err := a.db.Create(&row).Error; err != nil {
			response.Fail(c, 500, "保存失败")
			return
		}
	case err != nil:
		response.Fail(c, 500, "读取失败")
		return
	default:
		row.Scope = req.Scope
		row.Enabled = req.Enabled
		if err := a.db.Save(&row).Error; err != nil {
			response.Fail(c, 500, "保存失败")
			return
		}
	}
	response.OK(c, row)
}

// ListInterops 超管查看当前机构的互通策略。
func (a *API) ListInterops(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	if u.Level < model.LevelSuper {
		response.Fail(c, 403, "仅系统管理员可查看")
		return
	}
	var rows []model.VPNInterop
	if err := a.db.Order("id").Find(&rows).Error; err != nil {
		response.Fail(c, 500, "读取失败")
		return
	}
	response.OK(c, rows)
}

func (a *API) revokeGroupDevices(actor, groupID, userID uint, reason string) {
	var devices []model.VPNDevice
	if err := a.db.Where("group_id = ? AND user_id = ? AND status = ?", groupID, userID, model.VPNDeviceActive).
		Find(&devices).Error; err != nil {
		return
	}
	for _, dev := range devices {
		a.db.Create(&model.VPNDeviceHistory{UserID: dev.UserID, DeviceID: dev.DeviceID, PubKey: dev.PubKey, Reason: reason})
		a.db.Model(&dev).Updates(map[string]any{"status": model.VPNDeviceRevoked, "priv_key_enc": ""})
	}
}

// ServerPeers 服务器侧 wg0 同步用：返回所有在用设备的公钥与地址（仅超管）。
// 部署脚本周期性拉它，把客户端登记成 wg peer，服务器才能在覆盖网里收发。
func (a *API) ServerPeers(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	if u.Level < model.LevelSuper {
		response.Fail(c, 403, "仅系统管理员可读取")
		return
	}
	var devices []model.VPNDevice
	if err := a.db.Where("status = ? AND session_expires_at IS NOT NULL AND session_expires_at > ?", model.VPNDeviceActive, time.Now()).Find(&devices).Error; err != nil {
		response.Fail(c, 500, "读取设备失败")
		return
	}
	rows := make([]gin.H, 0, len(devices))
	for _, d := range devices {
		rows = append(rows, gin.H{"device_id": d.DeviceID, "pubkey": d.PubKey, "address": d.Address})
	}
	response.OK(c, rows)
}

// Overview 组网总览：设备、用户、组、地址、在线状态（管理员/超管）。
// 超管看全量；管理员看自己 org_admin 的机构；其余只看自己的设备。
func (a *API) Overview(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	q := a.db.Model(&model.VPNDevice{}).Order("id")
	if u.Level < model.LevelSuper {
		var orgIDs []uint
		a.db.Table("vpn_member AS m").
			Joins("JOIN vpn_group AS g ON g.id = m.group_id").
			Where("m.user_id = ? AND m.role = ?", u.ID, model.VPNRoleOrgAdmin).
			Distinct().Pluck("g.org_id", &orgIDs)
		if len(orgIDs) == 0 {
			q = q.Where("user_id = ?", u.ID)
		} else {
			var gids []uint
			a.db.Model(&model.VPNGroup{}).Where("org_id IN ?", orgIDs).Pluck("id", &gids)
			q = q.Where("group_id IN ?", gids)
		}
	}
	var devices []model.VPNDevice
	if err := q.Find(&devices).Error; err != nil {
		response.Fail(c, 500, "读取设备失败")
		return
	}

	var users []model.User
	a.db.Select("id", "username", "level").Find(&users)
	userName := make(map[uint]string, len(users))
	for _, x := range users {
		userName[x.ID] = x.Username
	}
	var groups []model.VPNGroup
	a.db.Find(&groups)
	groupName := make(map[uint]string, len(groups))
	groupOrg := make(map[uint]uint, len(groups))
	for _, g := range groups {
		groupName[g.ID] = g.Name
		groupOrg[g.ID] = g.OrgID
	}
	var orgs []model.VPNOrg
	a.db.Find(&orgs)
	orgName := make(map[uint]string, len(orgs))
	for _, o := range orgs {
		orgName[o.ID] = o.Name
	}

	now := time.Now()
	rows := make([]gin.H, 0, len(devices))
	for _, d := range devices {
		online := d.Status == model.VPNDeviceActive && d.LastSeen != nil &&
			now.Sub(*d.LastSeen) < 90*time.Second &&
			d.SessionExpiresAt != nil && d.SessionExpiresAt.After(now)
		rows = append(rows, gin.H{
			"id": d.ID, "device_id": d.DeviceID, "user_id": d.UserID, "username": userName[d.UserID],
			"group": groupName[d.GroupID], "org": orgName[groupOrg[d.GroupID]],
			"address": d.Address, "status": d.Status, "online": online,
			"endpoint": d.Endpoint, "last_seen": d.LastSeen, "session_expires_at": d.SessionExpiresAt,
		})
	}
	response.OK(c, gin.H{"devices": rows, "server_address": config.Conf.Vpn.ServerAddress})
}

// kickPeer 立即从服务器 wg 接口移除一个公钥（用于踢下线/停用/封禁）。
func kickPeer(pub string) {
	iface := strings.TrimSpace(config.Conf.Vpn.ServerInterface)
	if iface == "" || strings.TrimSpace(pub) == "" || os.Geteuid() != 0 {
		return
	}
	wg, err := ensureWgBinary()
	if err != nil {
		return
	}
	_ = exec.Command(wg, "set", iface, "peer", pub, "remove").Run()
}

func (a *API) adminDevice(c *gin.Context) (*model.VPNDevice, bool) {
	u, ok := a.currentUser(c)
	if !ok {
		return nil, false
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.Fail(c, 400, "id 非法")
		return nil, false
	}
	var dev model.VPNDevice
	if err := a.db.First(&dev, uint(id)).Error; err != nil {
		response.Fail(c, 404, "设备不存在")
		return nil, false
	}
	if !a.canManageGroup(u, dev.GroupID) && u.ID != dev.UserID {
		response.Fail(c, 403, "无此组管理权限")
		return nil, false
	}
	return &dev, true
}

// KickDevice 立即断开当前隧道，但保留设备与地址，客户端可马上重连。
func (a *API) KickDevice(c *gin.Context) {
	dev, ok := a.adminDevice(c)
	if !ok {
		return
	}
	kickPeer(dev.PubKey)
	a.db.Model(dev).Updates(map[string]any{"endpoint": "", "last_seen": nil})
	response.OK(c, gin.H{"kicked": true})
}

// SuspendDevice 临时停用：到期后由 reconciler 自动恢复，地址保留。
func (a *API) SuspendDevice(c *gin.Context) {
	dev, ok := a.adminDevice(c)
	if !ok {
		return
	}
	var req struct {
		Minutes int `json:"minutes"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.Minutes <= 0 {
		req.Minutes = 30
	}
	until := time.Now().Add(time.Duration(req.Minutes) * time.Minute)
	kickPeer(dev.PubKey)
	a.db.Model(dev).Updates(map[string]any{
		"status": model.VPNDeviceSuspended, "suspended_until": until,
		"endpoint": "", "last_seen": nil,
	})
	response.OK(c, gin.H{"suspended_until": until})
}

// BanDevice 封禁/解封：封禁后不能重新登记，需管理员解封。
func (a *API) BanDevice(c *gin.Context) {
	dev, ok := a.adminDevice(c)
	if !ok {
		return
	}
	var req struct {
		Banned bool `json:"banned"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.Banned {
		kickPeer(dev.PubKey)
		a.db.Model(dev).Updates(map[string]any{
			"status": model.VPNDeviceBanned, "suspended_until": nil,
			"endpoint": "", "last_seen": nil,
		})
	} else {
		a.db.Model(dev).Updates(map[string]any{"status": model.VPNDeviceActive})
	}
	response.OK(c, gin.H{"banned": req.Banned})
}

// ReleaseAdminDevice 永久释放：删除设备并交回地址。
func (a *API) ReleaseAdminDevice(c *gin.Context) {
	dev, ok := a.adminDevice(c)
	if !ok {
		return
	}
	kickPeer(dev.PubKey)
	if err := a.db.Delete(dev).Error; err != nil {
		response.Fail(c, 500, "释放失败")
		return
	}
	response.OK(c, gin.H{"released": true, "address": dev.Address})
}
