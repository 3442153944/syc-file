package vpn

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
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
		if err := tx.Model(&model.VPNDevice{}).Where("group_id = ?", group.ID).
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
			exp := now.Add(sessionTTL())
			dev.LastSeen = &now
			dev.SessionExpiresAt = &exp
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
			exp := now.Add(sessionTTL())
			dev.LastSeen = &now
			dev.SessionExpiresAt = &exp
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
	if err := a.db.Where("group_id IN ? AND status = ? AND session_expires_at > ?", groupIDs, model.VPNDeviceActive, time.Now()).Find(&devices).Error; err != nil {
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
	if sp := ServerPeer(c); sp != nil {
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
		Updates(map[string]any{
			"endpoint":           strings.TrimSpace(req.Endpoint),
			"last_seen":          now,
			"session_expires_at": now.Add(sessionTTL()),
		})
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
func ServerPeer(c *gin.Context) gin.H {
	cfg := config.Conf.Vpn
	if strings.TrimSpace(cfg.ServerPubKey) == "" {
		return nil
	}
	addr := strings.TrimSpace(cfg.ServerAddress)
	return gin.H{
		"device_id":   "syc-server",
		"pubkey":      cfg.ServerPubKey,
		"endpoint":    serverEndpointFor(c, cfg),
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
	a.db.Where("group_id IN ? AND status = ? AND session_expires_at > ?", groupIDs, model.VPNDeviceActive, time.Now()).Find(&devices)
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

	serverEP := serverEndpointFor(c, config.Conf.Vpn)
	rows := make([]gin.H, 0, len(visible)+1)
	mode := "direct"
	for _, p := range visible {
		m := "direct"
		if serverEP != "" && strings.TrimSpace(p.Endpoint) == serverEP {
			m, mode = "relay", "relay"
		}
		rows = append(rows, gin.H{"device_id": p.DeviceID, "endpoint": p.Endpoint, "mode": m})
	}
	// 服务器自身也是一个可用对端（172.1.0.254），否则界面会显示「暂无对端」
	if sp := ServerPeer(c); sp != nil {
		rows = append(rows, gin.H{
			"device_id": "syc-server",
			"endpoint":  sp["endpoint"],
			"mode":      "direct",
			"role":      "server",
		})
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

// serverEndpointFor 决定服务器 peer 的端点：
//   - 配置了 vpn.server_endpoint 就用它（显式覆盖）；
//   - 否则用「客户端本次请求实际使用的 Host」+ wg 端口，入口换成 LAN / ddns / 别的域名都自动跟随。
func serverEndpointFor(c *gin.Context, cfg config.VpnConfig) string {
	if ep := strings.TrimSpace(cfg.ServerEndpoint); ep != "" {
		return ep
	}
	host := strings.TrimSpace(c.GetHeader("X-Forwarded-Host"))
	if host == "" {
		host = c.Request.Host
	}
	host = strings.TrimSpace(strings.Split(host, ",")[0])
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return ""
	}
	port := cfg.ServerWGPort
	if port == 0 {
		port = 51820
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// AutoEnroll 给桌面端用的自动入组：用登录设备号（Device-Id 头）作为组网设备名，
// 选该用户第一个已加入的组，服务端生成并托管密钥，直接完成登记。
//
// 目的：登录后客户端不需要再让用户手选组、手填设备名；没有加入任何组时返回 409，
// 由管理端先把人加进组。
func (a *API) AutoEnroll(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	deviceID := strings.TrimSpace(c.GetHeader("Device-Id"))
	if deviceID == "" {
		response.Fail(c, 400, "缺少 Device-Id")
		return
	}
	// 已登记：地址保持不变（IP 持久），被吊销的走重新激活
	var existing model.VPNDevice
	if err := a.db.Where("user_id = ? AND device_id = ?", u.ID, deviceID).First(&existing).Error; err == nil {
		if existing.Status == model.VPNDeviceActive && strings.TrimSpace(existing.PrivKeyEnc) != "" {
			now := time.Now()
			exp := now.Add(sessionTTL())
			a.db.Model(&existing).Updates(map[string]any{
				"last_seen": now, "session_expires_at": exp,
			})
			response.OK(c, gin.H{
				"device_id": existing.DeviceID, "address": existing.Address,
				"pubkey": existing.PubKey, "group_id": existing.GroupID, "existing": true,
			})
			return
		}
		// 被吊销过：保留原地址，重新生成密钥并激活
		priv, pub, err := GenerateKeypair()
		if err != nil {
			response.Fail(c, 500, "生成密钥失败")
			return
		}
		enc, err := EncryptPrivate(mustMasterKey(), priv, deviceID)
		if err != nil {
			response.Fail(c, 500, "加密私钥失败")
			return
		}
		now := time.Now()
		exp := now.Add(sessionTTL())
		a.db.Model(&existing).Updates(map[string]any{
			"pubkey": pub, "priv_key_enc": enc, "status": model.VPNDeviceActive,
			"key_source": model.VPNKeySourceServer, "last_seen": now, "session_expires_at": exp,
		})
		response.OK(c, gin.H{
			"device_id": existing.DeviceID, "address": existing.Address, "pubkey": pub,
			"group_id": existing.GroupID, "private_key": priv, "existing": true, "reactivated": true,
		})
		return
	}

	var m model.VPNMember
	if err := a.db.Where("user_id = ?", u.ID).Order("id").First(&m).Error; err != nil {
		// 管理员/超管不该被「没加组」卡住：自动落到第一个组，没有组就建默认机构+默认组
		if u.Level < model.LevelAdmin {
			response.Fail(c, 409, "还没有加入任何组，请让管理员先把你加入组")
			return
		}
		gid, err := a.ensureDefaultGroup(u)
		if err != nil {
			response.Fail(c, 500, "创建默认组失败: "+err.Error())
			return
		}
		m = model.VPNMember{GroupID: gid, UserID: u.ID, Role: model.VPNRoleOrgAdmin}
		if err := a.db.Create(&m).Error; err != nil {
			response.Fail(c, 500, "加入默认组失败")
			return
		}
	}
	var group model.VPNGroup
	if err := a.db.First(&group, m.GroupID).Error; err != nil {
		response.Fail(c, 409, "所在组不存在")
		return
	}
	if _, err := MasterKey(); err != nil {
		response.Fail(c, 503, err.Error())
		return
	}
	priv, pub, err := GenerateKeypair()
	if err != nil {
		response.Fail(c, 500, "生成密钥失败")
		return
	}
	enc, err := EncryptPrivate(mustMasterKey(), priv, deviceID)
	if err != nil {
		response.Fail(c, 500, "加密私钥失败")
		return
	}
	var used []string
	a.db.Model(&model.VPNDevice{}).Where("group_id = ?", group.ID).Pluck("address", &used)
	usedSet := make(map[string]bool, len(used))
	for _, addr := range used {
		usedSet[addr] = true
	}
	addr, err := NextAddress(group.Subnet, usedSet)
	if err != nil {
		response.Fail(c, 409, err.Error())
		return
	}
	now := time.Now()
	exp := now.Add(sessionTTL())
	dev := model.VPNDevice{
		UserID: u.ID, GroupID: group.ID, DeviceID: deviceID,
		PubKey: pub, PrivKeyEnc: enc, KeySource: model.VPNKeySourceServer,
		Address: addr, Status: model.VPNDeviceActive,
		LastSeen: &now, SessionExpiresAt: &exp,
	}
	if err := a.db.Create(&dev).Error; err != nil {
		response.Fail(c, 500, "登记设备失败")
		return
	}
	response.OK(c, gin.H{
		"device_id": dev.DeviceID, "address": dev.Address, "pubkey": dev.PubKey,
		"group_id": dev.GroupID, "subnet": group.Subnet, "private_key": priv, "existing": false,
	})
}

func mustMasterKey() []byte {
	k, _ := MasterKey()
	return k
}

// ensureDefaultGroup 给没有组的管理员兜底：优先用系统里第一个组；一个组都没有就建默认机构+默认组。
func (a *API) ensureDefaultGroup(u *model.User) (uint, error) {
	var g model.VPNGroup
	if err := a.db.Order("id").First(&g).Error; err == nil {
		return g.ID, nil
	}
	org := model.VPNOrg{Name: "默认机构", OwnerUserID: u.ID}
	if err := a.db.Create(&org).Error; err != nil {
		// 并发下可能已被别的请求建出来
		if err := a.db.Where("name = ?", "默认机构").First(&org).Error; err != nil {
			return 0, err
		}
	}
	var used []string
	a.db.Model(&model.VPNGroup{}).Pluck("subnet", &used)
	subnet, err := AllocateSubnet(config.Conf.Vpn.Pool, used)
	if err != nil {
		return 0, err
	}
	g = model.VPNGroup{OrgID: org.ID, Name: "默认组", Subnet: subnet}
	if err := a.db.Create(&g).Error; err != nil {
		return 0, err
	}
	return g.ID, nil
}

// ReleaseDevice 客户端显式放弃地址：删除设备（地址随即释放，下一次分配才可能复用）。
func (a *API) ReleaseDevice(c *gin.Context) {
	u, ok := a.currentUser(c)
	if !ok {
		return
	}
	idStr := strings.TrimSpace(c.Param("id"))
	var id uint64
	if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil || id == 0 {
		response.Fail(c, 400, "id 非法")
		return
	}
	var dev model.VPNDevice
	if err := a.db.First(&dev, uint(id)).Error; err != nil {
		response.Fail(c, 404, "设备不存在")
		return
	}
	if dev.UserID != u.ID && u.Level < model.LevelAdmin {
		response.Fail(c, 403, "无权释放该设备")
		return
	}
	if err := a.db.Delete(&dev).Error; err != nil {
		response.Fail(c, 500, "释放失败")
		return
	}
	response.OK(c, gin.H{"released": true, "address": dev.Address})
}
