package vpn

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/pkg/response"
)

// ── 设备侧接口 ─────────────────────────────────────────────────

type enrollReq struct {
	GroupID uint `json:"group_id"`
}

// Enroll 签发一次性入组令牌。必须是该组成员（管理员需先把人加进组）。
func (a *API) Enroll(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	var req enrollReq
	if err := c.ShouldBindJSON(&req); err != nil || req.GroupID == 0 {
		response.Fail(c, 400, "group_id 必填")
		return
	}
	if _, err := a.memberRole(u.ID, req.GroupID); err != nil {
		response.Fail(c, 403, "你不在该组内")
		return
	}
	var group model.VPNGroup
	if err := a.db.First(&group, req.GroupID).Error; err != nil {
		response.Fail(c, 404, "组不存在")
		return
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		response.Fail(c, 500, "生成令牌失败")
		return
	}
	rawToken := hex.EncodeToString(raw)
	row := model.VPNEnrollToken{
		UserID:    u.ID,
		GroupID:   req.GroupID,
		TokenHash: hashToken(rawToken),
		ExpiresAt: time.Now().Add(enrollTTL),
	}
	if err := a.db.Create(&row).Error; err != nil {
		response.Fail(c, 500, "保存令牌失败")
		return
	}
	response.OK(c, gin.H{
		"token":      rawToken,
		"expires_in": int(enrollTTL.Seconds()),
		"group_id":   group.ID,
		"subnet":     group.Subnet,
	})
}

type registerReq struct {
	Token     string `json:"token"`
	DeviceID  string `json:"device_id"`
	KeySource string `json:"key_source"`
	PubKey    string `json:"pubkey"`
	Endpoint  string `json:"endpoint"`
}

// Register 用一次性令牌登记设备，分配组内地址，必要时生成并托管密钥。
func (a *API) Register(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, "请求体非法")
		return
	}
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	if req.DeviceID == "" {
		response.Fail(c, 400, "device_id 必填")
		return
	}
	if req.KeySource == "" {
		req.KeySource = model.VPNKeySourceClient
	}
	if req.KeySource != model.VPNKeySourceClient && req.KeySource != model.VPNKeySourceServer {
		response.Fail(c, 400, "key_source 只能是 client 或 server")
		return
	}

	var issued model.VPNEnrollToken
	if err := a.db.Where("token_hash = ?", hashToken(strings.TrimSpace(req.Token))).First(&issued).Error; err != nil {
		response.Fail(c, 403, "令牌无效")
		return
	}
	if issued.UserID != u.ID {
		response.Fail(c, 403, "令牌不属于当前账号")
		return
	}
	if issued.UsedAt != nil {
		response.Fail(c, 403, "令牌已使用")
		return
	}
	if time.Now().After(issued.ExpiresAt) {
		response.Fail(c, 403, "令牌已过期")
		return
	}
	var group model.VPNGroup
	if err := a.db.First(&group, issued.GroupID).Error; err != nil {
		response.Fail(c, 404, "组不存在")
		return
	}
	if _, err := a.memberRole(u.ID, group.ID); err != nil {
		response.Fail(c, 403, "你已不在该组内")
		return
	}

	var privateKey, pubKey string
	var err error
	switch req.KeySource {
	case model.VPNKeySourceServer:
		if _, err := MasterKey(); err != nil {
			response.Fail(c, 503, err.Error())
			return
		}
		privateKey, pubKey, err = GenerateKeypair()
		if err != nil {
			response.Fail(c, 500, "生成密钥失败")
			return
		}
		if req.PubKey != "" && strings.TrimSpace(req.PubKey) != pubKey {
			response.Fail(c, 400, "key_source=server 时不要同时提交公钥")
			return
		}
	case model.VPNKeySourceClient:
		if err := ValidatePubKey(req.PubKey); err != nil {
			response.Fail(c, 400, err.Error())
			return
		}
		pubKey = strings.TrimSpace(req.PubKey)
	}

	var out gin.H
	err = a.db.Transaction(func(tx *gorm.DB) error {
		// 地址分配：同组已用地址排除
		var usedAddrs []string
		if err := tx.Model(&model.VPNDevice{}).Where("group_id = ? AND status = ?", group.ID, model.VPNDeviceActive).
			Pluck("address", &usedAddrs).Error; err != nil {
			return err
		}
		used := make(map[string]bool, len(usedAddrs))
		for _, addr := range usedAddrs {
			used[addr] = true
		}

		var dev model.VPNDevice
		err := tx.Where("user_id = ? AND device_id = ?", u.ID, req.DeviceID).First(&dev).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			addr, aerr := NextAddress(group.Subnet, used)
			if aerr != nil {
				return aerr
			}
			dev = model.VPNDevice{
				UserID:    u.ID,
				GroupID:   group.ID,
				DeviceID:  req.DeviceID,
				PubKey:    pubKey,
				KeySource: req.KeySource,
				Address:   addr,
				Endpoint:  strings.TrimSpace(req.Endpoint),
				Status:    model.VPNDeviceActive,
			}
			if req.KeySource == model.VPNKeySourceServer {
				master, _ := MasterKey()
				enc, eerr := EncryptPrivate(master, privateKey, req.DeviceID)
				if eerr != nil {
					return eerr
				}
				dev.PrivKeyEnc = enc
			}
			now := time.Now()
			dev.LastSeen = &now
			if err := tx.Create(&dev).Error; err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			// 同一设备重复登记：公钥变了就记历史并轮换，地址保持不变
			if dev.PubKey != pubKey {
				if err := tx.Create(&model.VPNDeviceHistory{
					UserID: dev.UserID, DeviceID: dev.DeviceID, PubKey: dev.PubKey, Reason: "rotate",
				}).Error; err != nil {
					return err
				}
				dev.PubKey = pubKey
				dev.KeySource = req.KeySource
				if req.KeySource == model.VPNKeySourceServer {
					master, _ := MasterKey()
					enc, eerr := EncryptPrivate(master, privateKey, req.DeviceID)
					if eerr != nil {
						return eerr
					}
					dev.PrivKeyEnc = enc
				} else {
					dev.PrivKeyEnc = ""
				}
			}
			dev.GroupID = group.ID
			dev.Status = model.VPNDeviceActive
			if ep := strings.TrimSpace(req.Endpoint); ep != "" {
				dev.Endpoint = ep
			}
			now := time.Now()
			dev.LastSeen = &now
			if err := tx.Save(&dev).Error; err != nil {
				return err
			}
		}

		now := time.Now()
		if err := tx.Model(&issued).Update("used_at", now).Error; err != nil {
			return err
		}
		out = gin.H{
			"device_id":   dev.DeviceID,
			"group_id":    dev.GroupID,
			"subnet":      group.Subnet,
			"address":     dev.Address,
			"pubkey":      dev.PubKey,
			"key_source":  dev.KeySource,
			"private_key": privateKey, // 仅 key_source=server 且本次生成时有值，只出现这一次
		}
		return nil
	})
	if err != nil {
		response.Fail(c, 400, err.Error())
		return
	}
	response.OK(c, out)
}

// Peers 返回按 ACL 裁剪后的对端列表。
func (a *API) Peers(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	deviceID := strings.TrimSpace(c.Query("device_id"))
	if deviceID == "" {
		response.Fail(c, 400, "device_id 必填")
		return
	}
	var self model.VPNDevice
	if err := a.db.Where("user_id = ? AND device_id = ? AND status = ?", u.ID, deviceID, model.VPNDeviceActive).
		First(&self).Error; err != nil {
		response.Fail(c, 404, "设备未登记")
		return
	}
	var group model.VPNGroup
	if err := a.db.First(&group, self.GroupID).Error; err != nil {
		response.Fail(c, 404, "组不存在")
		return
	}

	// 本机构所有组与设备
	var groupIDs []uint
	if err := a.db.Model(&model.VPNGroup{}).Where("org_id = ?", group.OrgID).Pluck("id", &groupIDs).Error; err != nil {
		response.Fail(c, 500, "读取组失败")
		return
	}
	var devices []model.VPNDevice
	if err := a.db.Where("group_id IN ? AND status = ?", groupIDs, model.VPNDeviceActive).Find(&devices).Error; err != nil {
		response.Fail(c, 500, "读取设备失败")
		return
	}
	var members []model.VPNMember
	if err := a.db.Where("group_id IN ?", groupIDs).Find(&members).Error; err != nil {
		response.Fail(c, 500, "读取成员失败")
		return
	}
	roleOf := make(map[[2]uint]string, len(members))
	for _, m := range members {
		roleOf[[2]uint{m.GroupID, m.UserID}] = m.Role
	}
	groupOrg := make(map[uint]uint, len(groupIDs))
	for _, id := range groupIDs {
		groupOrg[id] = group.OrgID
	}

	all := make([]DeviceRef, 0, len(devices))
	for _, d := range devices {
		all = append(all, DeviceRef{
			DeviceID: d.DeviceID,
			PubKey:   d.PubKey,
			Address:  d.Address,
			Endpoint: d.Endpoint,
			UserID:   d.UserID,
			GroupID:  d.GroupID,
			OrgID:    groupOrg[d.GroupID],
			Role:     roleOf[[2]uint{d.GroupID, d.UserID}],
			Status:   d.Status,
		})
	}
	var interops []model.VPNInterop
	if err := a.db.Where("org_id = ? AND enabled = ?", group.OrgID, true).Find(&interops).Error; err != nil {
		response.Fail(c, 500, "读取互通策略失败")
		return
	}

	selfRef := DeviceRef{
		DeviceID: self.DeviceID, PubKey: self.PubKey, Address: self.Address, Endpoint: self.Endpoint,
		UserID: self.UserID, GroupID: self.GroupID, OrgID: group.OrgID,
		Role: roleOf[[2]uint{self.GroupID, self.UserID}], Status: self.Status,
	}
	visible := VisiblePeers(selfRef, all, interops)

	rows := make([]gin.H, 0, len(visible)+1)
	for _, p := range visible {
		rows = append(rows, gin.H{
			"device_id":   p.DeviceID,
			"pubkey":      p.PubKey,
			"endpoint":    p.Endpoint,
			"address":     p.Address,
			"allowed_ips": AllowedIPsOf(p.Address),
			"role":        p.Role,
		})
	}
	// 服务器节点：固定地址（默认 172.1.0.254），让普通 HTTP/DB 业务也能走 P2P。
	if sp := ServerPeer(); sp != nil {
		rows = append(rows, sp)
	}
	response.OK(c, gin.H{
		"self": gin.H{
			"device_id":   self.DeviceID,
			"address":     self.Address,
			"pubkey":      self.PubKey,
			"group_id":    self.GroupID,
			"subnet":      group.Subnet,
			"allowed_ips": AllowedIPsOf(self.Address),
			"keepalive":   5,
			"mtu":         1280,
		},
		"peers": rows,
	})
}

type heartbeatReq struct {
	DeviceID string `json:"device_id"`
	Endpoint string `json:"endpoint"`
}

// Heartbeat 更新端点与在线时间；WireGuard 的端点漫游依赖它。
func (a *API) Heartbeat(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	var req heartbeatReq
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.DeviceID) == "" {
		response.Fail(c, 400, "device_id 必填")
		return
	}
	now := time.Now()
	res := a.db.Model(&model.VPNDevice{}).
		Where("user_id = ? AND device_id = ? AND status = ?", u.ID, req.DeviceID, model.VPNDeviceActive).
		Updates(map[string]any{"endpoint": strings.TrimSpace(req.Endpoint), "last_seen": now})
	if res.Error != nil {
		response.Fail(c, 500, "更新失败")
		return
	}
	if res.RowsAffected == 0 {
		response.Fail(c, 404, "设备未登记")
		return
	}
	response.OK(c, gin.H{"ok": true})
}

// MyDevices 当前账号已登记的设备。
func (a *API) MyDevices(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	var devices []model.VPNDevice
	if err := a.db.Where("user_id = ?", u.ID).Order("id").Find(&devices).Error; err != nil {
		response.Fail(c, 500, "读取设备失败")
		return
	}
	response.OK(c, devices)
}

// ServerPeer 服务器在覆盖网里的固定节点（默认 172.1.0.254）。
// 未配置 ServerPubKey 时返回 nil，客户端就只拿普通对端。
func ServerPeer() gin.H {
	cfg := config.Conf.Vpn
	if strings.TrimSpace(cfg.ServerPubKey) == "" {
		return nil
	}
	addr := strings.TrimSpace(cfg.ServerAddress)
	return gin.H{
		"device_id":   "syc-server",
		"pubkey":      cfg.ServerPubKey,
		"endpoint":    cfg.ServerEndpoint,
		"address":     addr,
		"allowed_ips": []string{addr + "/32"},
		"role":        "server",
	}
}

// Link 返回当前设备各对端的连通模式：direct=直连、relay=经服务器中继。
// Windows 菜单栏的「直连/中继」指示器用它；也方便排查「为什么速度只有几百 KB」。
func (a *API) Link(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	deviceID := strings.TrimSpace(c.Query("device_id"))
	var self model.VPNDevice
	if err := a.db.Where("user_id = ? AND device_id = ? AND status = ?", u.ID, deviceID, model.VPNDeviceActive).
		First(&self).Error; err != nil {
		response.Fail(c, 404, "设备未登记")
		return
	}
	var group model.VPNGroup
	if err := a.db.First(&group, self.GroupID).Error; err != nil {
		response.Fail(c, 404, "组不存在")
		return
	}
	var groupIDs []uint
	a.db.Model(&model.VPNGroup{}).Where("org_id = ?", group.OrgID).Pluck("id", &groupIDs)
	var devices []model.VPNDevice
	a.db.Where("group_id IN ? AND status = ?", groupIDs, model.VPNDeviceActive).Find(&devices)
	var members []model.VPNMember
	a.db.Where("group_id IN ?", groupIDs).Find(&members)
	roleOf := make(map[[2]uint]string, len(members))
	for _, m := range members {
		roleOf[[2]uint{m.GroupID, m.UserID}] = m.Role
	}
	groupOrg := make(map[uint]uint, len(groupIDs))
	for _, id := range groupIDs {
		groupOrg[id] = group.OrgID
	}
	all := make([]DeviceRef, 0, len(devices))
	for _, d := range devices {
		all = append(all, DeviceRef{
			DeviceID: d.DeviceID, PubKey: d.PubKey, Address: d.Address, Endpoint: d.Endpoint,
			UserID: d.UserID, GroupID: d.GroupID, OrgID: groupOrg[d.GroupID],
			Role: roleOf[[2]uint{d.GroupID, d.UserID}], Status: d.Status,
		})
	}
	var interops []model.VPNInterop
	a.db.Where("org_id = ? AND enabled = ?", group.OrgID, true).Find(&interops)
	selfRef := DeviceRef{
		DeviceID: self.DeviceID, PubKey: self.PubKey, Address: self.Address, Endpoint: self.Endpoint,
		UserID: self.UserID, GroupID: self.GroupID, OrgID: group.OrgID,
		Role: roleOf[[2]uint{self.GroupID, self.UserID}], Status: self.Status,
	}
	visible := VisiblePeers(selfRef, all, interops)

	serverEP := strings.TrimSpace(config.Conf.Vpn.ServerEndpoint)
	rows := make([]gin.H, 0, len(visible))
	mode := "direct"
	for _, p := range visible {
		m := "direct"
		if serverEP != "" && strings.TrimSpace(p.Endpoint) == serverEP {
			m, mode = "relay", "relay"
		}
		rows = append(rows, gin.H{"device_id": p.DeviceID, "endpoint": p.Endpoint, "mode": m})
	}
	if len(rows) == 0 {
		mode = "idle"
	}
	response.OK(c, gin.H{
		"mode":           mode,
		"self":           gin.H{"device_id": self.DeviceID, "address": self.Address},
		"server_address": config.Conf.Vpn.ServerAddress,
		"peers":          rows,
	})
}
