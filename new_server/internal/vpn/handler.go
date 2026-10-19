package vpn

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/pkg/response"
	"syc-file/pkg/token"
)

const enrollTTL = 5 * time.Minute

// sessionTTL 设备会话有效期：与登录 token 一致（默认 7 天）。
// heartbeat 每 30s 续期一次，用户真正离线超过这个时间，peer 才会被服务器移除。
func sessionTTL() time.Duration {
	days := config.Conf.Auth.TokenExpire
	if days <= 0 {
		days = 7
	}
	return time.Duration(days) * 24 * time.Hour
}

// API 组网接口。所有路由都挂在现有 /v1 认证组下，复用 JWT + Device-Id。
type API struct {
	db *gorm.DB
}

func NewAPI(db *gorm.DB) *API { return &API{db: db} }

// ── 通用辅助 ───────────────────────────────────────────────────

func claimsOf(c *gin.Context) (*token.Claims, bool) {
	v, ok := c.Get("UserInfo")
	if !ok {
		return nil, false
	}
	claims, ok := v.(*token.Claims)
	return claims, ok
}

func (a *API) currentUser(c *gin.Context) (*model.User, bool) {
	claims, ok := claimsOf(c)
	if !ok || claims.UserID == 0 {
		response.Fail(c, 401, "未登录")
		return nil, false
	}
	var u model.User
	if err := a.db.First(&u, claims.UserID).Error; err != nil {
		response.Fail(c, 401, "账号不存在")
		return nil, false
	}
	return &u, true
}

func (a *API) memberRole(userID, groupID uint) (string, error) {
	var m model.VPNMember
	if err := a.db.Where("user_id = ? AND group_id = ?", userID, groupID).First(&m).Error; err != nil {
		return "", err
	}
	return m.Role, nil
}

// orgRole 取用户在某个机构内的最高角色（org_admin 优先）。
func (a *API) orgRole(userID, orgID uint) (string, error) {
	var m model.VPNMember
	err := a.db.Table("vpn_member AS m").
		Joins("JOIN vpn_group AS g ON g.id = m.group_id").
		Where("m.user_id = ? AND g.org_id = ?", userID, orgID).
		Order("CASE m.role WHEN 'org_admin' THEN 0 WHEN 'group_admin' THEN 1 ELSE 2 END").
		Select("m.*").
		First(&m).Error
	if err != nil {
		return "", err
	}
	return m.Role, nil
}

func (a *API) canManageGroup(u *model.User, groupID uint) bool {
	if u.Level >= model.LevelSuper {
		return true
	}
	role, err := a.memberRole(u.ID, groupID)
	return err == nil && IsGroupAdmin(role)
}

func (a *API) canManageOrg(u *model.User, orgID uint) bool {
	if u.Level >= model.LevelSuper {
		return true
	}
	role, err := a.orgRole(u.ID, orgID)
	return err == nil && role == model.VPNRoleOrgAdmin
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
