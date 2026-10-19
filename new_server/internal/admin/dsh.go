package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"syc-file/internal/model"
	"syc-file/pkg/token"
)

// DSH（DeepSeek Harness）访问令牌。
//
// 背景：dsh web 每次启动都会生成一个新的 launch token 并打印在日志里，管理员要进后台
// 就得去翻 journalctl，没法发给同事或手机用。这里改成由 FileSync 管理端签发：
//   - 管理端创建 token（一次性短链 / 长期 token），只存 SHA-256；
//   - 浏览器打开 GET /dsh/auth?t=<token>，服务端校验后签发 DSH 的会话 cookie 并 302 到 /dsh/；
//   - cookie 用 DSH 自己的 browser-session 密钥签名，DSH 无感，不需要改 DSH 代码。
//
// 入口约定（nginx 把 /dsh/auth 反代到本服务，Host 必须原样透传）：
//   https://ali.sunyuanling.cn/dsh/auth?t=...
//   https://ddns.sunyuanling.cn:8443/dsh/auth?t=...
const (
	dshTokenKindOneTime   = "one_time"
	dshTokenKindLongLived = "long_lived"

	dshDefaultCredentials = "/root/.dsh/.credentials.yaml"
	dshCookieMaxAgeDays   = 30
	dshCookiePrefix       = "dsh-auth-"
)

// dshCookiePayload 必须与 @deepseek-ai/dsh-client-connection 的 CookiePayload 一致。
type dshCookiePayload struct {
	Version   int    `json:"version"`
	Authority string `json:"authority"`
	IssuedAt  int64  `json:"issuedAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

// ── 配置（环境变量，留空用默认）────────────────────────────────

func dshCredentialsPath() string {
	if p := strings.TrimSpace(os.Getenv("DSH_CREDENTIALS")); p != "" {
		return p
	}
	return dshDefaultCredentials
}

func dshAllowedHosts() map[string]bool {
	raw := strings.TrimSpace(os.Getenv("DSH_ALLOWED_HOSTS"))
	if raw == "" {
		raw = "ali.sunyuanling.cn,ddns.sunyuanling.cn"
	}
	out := map[string]bool{}
	for _, h := range strings.Split(raw, ",") {
		if h = strings.TrimSpace(h); h != "" {
			out[h] = true
		}
	}
	return out
}

func dshPublicBases() []string {
	raw := strings.TrimSpace(os.Getenv("DSH_PUBLIC_BASES"))
	if raw == "" {
		raw = "https://ali.sunyuanling.cn,https://ddns.sunyuanling.cn:8443"
	}
	out := make([]string, 0, 2)
	for _, b := range strings.Split(raw, ",") {
		if b = strings.TrimRight(strings.TrimSpace(b), "/"); b != "" {
			out = append(out, b)
		}
	}
	return out
}

// ── 签名 ────────────────────────────────────────────────────

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// dshSecret 读取 DSH 的 browser-session 签名密钥（base64url，32 字节）。
func dshSecret() ([]byte, error) {
	path := dshCredentialsPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 DSH 凭证失败: %w", err)
	}
	var doc struct {
		Records map[string]struct {
			Kind    string `yaml:"kind"`
			Payload struct {
				Version int    `yaml:"version"`
				Secret  string `yaml:"secret"`
			} `yaml:"payload"`
		} `yaml:"records"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("解析 DSH 凭证失败: %w", err)
	}
	rec, ok := doc.Records["client-connection/browser-session"]
	if !ok || rec.Kind != "grant" || rec.Payload.Version != 1 {
		return nil, fmt.Errorf("DSH 凭证缺少可用的 browser-session 记录")
	}
	secret, err := base64.RawURLEncoding.DecodeString(rec.Payload.Secret)
	if err != nil || len(secret) != 32 {
		return nil, fmt.Errorf("DSH browser-session 密钥格式不正确")
	}
	return secret, nil
}

// dshSessionCookie 生成 DSH 认可的会话 cookie（名称 + 值）。
// authority 必须是 DSH 看到的 Host（不带端口，nginx 的 $host 语义）。
func dshSessionCookie(authority string, ttl time.Duration) (string, string, time.Time, error) {
	secret, err := dshSecret()
	if err != nil {
		return "", "", time.Time{}, err
	}
	now := time.Now()
	expires := now.Add(ttl)
	payload := dshCookiePayload{
		Version:   1,
		Authority: authority,
		IssuedAt:  now.UnixMilli(),
		ExpiresAt: expires.UnixMilli(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", time.Time{}, err
	}
	encoded := b64url(body)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(encoded))
	nameSum := sha256.Sum256([]byte(authority))
	name := dshCookiePrefix + b64url(nameSum[:])
	value := fmt.Sprintf("v%d.%s.%s", payload.Version, encoded, b64url(mac.Sum(nil)))
	return name, value, expires, nil
}

// requestAuthority 取 nginx 透传的 Host，去掉端口。
func requestAuthority(c *gin.Context) string {
	host := strings.TrimSpace(c.Request.Host)
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.Contains(host[i:], "]") {
		// 兼容 IPv4/hostname:port；IPv6 字面量不会出现在这两个入口
		host = host[:i]
	}
	return strings.ToLower(host)
}

// dshTokenMinLevel 允许管理/兑换 DSH 令牌的最低权限级别。
// 默认 3（超级管理员）：普通管理员（level 2）既看不到入口，也不能签发或兑换。
func dshTokenMinLevel() int8 {
	v := strings.TrimSpace(os.Getenv("DSH_TOKEN_MIN_LEVEL"))
	if v == "" {
		return model.LevelSuper
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < int(model.LevelUser) || n > int(model.LevelSuper) {
		return model.LevelSuper
	}
	return int8(n)
}

// requireDSHTokenAdmin 令牌管理接口的鉴权：按等级判定，role=="admin" 不再足够。
func requireDSHTokenAdmin(c *gin.Context) (uint, bool) {
	claimsAny, exists := c.Get("UserInfo")
	if !exists || claimsAny == nil {
		jsonErr(c, 401, "未授权")
		return 0, false
	}
	claims := claimsAny.(*token.Claims)
	if claims.EffectiveLevel() < dshTokenMinLevel() {
		jsonErr(c, 403, "权限不足，仅超级管理员可管理 DSH 令牌")
		return 0, false
	}
	return uint(claims.UserID), true
}

// ── 管理端接口 ──────────────────────────────────────────────

type dshTokenDTO struct {
	model.DSHToken
	Status string `json:"status"`
}

func dshTokenStatus(t model.DSHToken, now time.Time) string {
	switch {
	case t.RevokedAt != nil:
		return "revoked"
	case now.After(t.ExpiresAt):
		return "expired"
	case t.Kind == dshTokenKindOneTime && t.UsedAt != nil:
		return "used"
	default:
		return "active"
	}
}

// ListDSHTokens GET /v1/admin/dsh/tokens
func (h *APIHandler) ListDSHTokens(c *gin.Context) {
	if _, ok := requireDSHTokenAdmin(c); !ok {
		return
	}
	p := parsePage(c)
	var rows []model.DSHToken
	if err := h.db.Order("id desc").Offset(p.offset()).Limit(p.PageSize).Find(&rows).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	var total int64
	if err := h.db.Model(&model.DSHToken{}).Count(&total).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	now := time.Now()
	list := make([]dshTokenDTO, 0, len(rows))
	for _, r := range rows {
		list = append(list, dshTokenDTO{DSHToken: r, Status: dshTokenStatus(r, now)})
	}
	jsonPage(c, list, total, p)
}

// CreateDSHToken POST /v1/admin/dsh/tokens
func (h *APIHandler) CreateDSHToken(c *gin.Context) {
	uid, ok := requireDSHTokenAdmin(c)
	if !ok {
		return
	}
	var req struct {
		Kind         string `json:"kind"`
		Label        string `json:"label"`
		ExpiresHours int    `json:"expires_hours"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}
	kind := strings.TrimSpace(req.Kind)
	if kind == "" {
		kind = dshTokenKindOneTime
	}
	if kind != dshTokenKindOneTime && kind != dshTokenKindLongLived {
		jsonErr(c, 400, "kind 只能是 one_time 或 long_lived")
		return
	}
	hours := req.ExpiresHours
	if hours <= 0 {
		if kind == dshTokenKindOneTime {
			hours = 24
		} else {
			hours = 24 * 30
		}
	}
	if hours > 24*365 {
		jsonErr(c, 400, "有效期最长一年")
		return
	}
	label := strings.TrimSpace(req.Label)
	if len([]rune(label)) > 60 {
		jsonErr(c, 400, "备注最长 60 字")
		return
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		jsonErr(c, 500, "生成令牌失败")
		return
	}
	plain := b64url(buf)
	row := model.DSHToken{
		TokenHash: sha256hex(plain),
		Kind:      kind,
		Label:     label,
		CreatedBy: uid,
		ExpiresAt: time.Now().Add(time.Duration(hours) * time.Hour),
	}
	if err := h.db.Create(&row).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}

	links := make([]gin.H, 0, 2)
	for _, base := range dshPublicBases() {
		links = append(links, gin.H{
			"base": base,
			"url":  fmt.Sprintf("%s/dsh/auth?t=%s", base, plain),
		})
	}
	jsonOK(c, gin.H{
		"id":         row.ID,
		"token":      plain,
		"kind":       row.Kind,
		"label":      row.Label,
		"expires_at": row.ExpiresAt,
		"links":      links,
	})
}

// RevokeDSHToken DELETE /v1/admin/dsh/tokens/:id
func (h *APIHandler) RevokeDSHToken(c *gin.Context) {
	if _, ok := requireDSHTokenAdmin(c); !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var row model.DSHToken
	if err := h.db.First(&row, id).Error; err != nil {
		jsonErr(c, 404, "令牌不存在")
		return
	}
	if row.RevokedAt == nil {
		now := time.Now()
		if err := h.db.Model(&row).Update("revoked_at", now).Error; err != nil {
			jsonErr(c, 500, err.Error())
			return
		}
	}
	jsonOK(c, nil)
}

// ── 兑换入口（公开）─────────────────────────────────────────

// DSHAuth GET /dsh/auth?t=<token>&next=/dsh/
//
// 校验 token → 签发 DSH 会话 cookie → 302 到 next。一次性 token 兑换后立即作废。
func (h *APIHandler) DSHAuth(c *gin.Context) {
	plain := strings.TrimSpace(c.Query("t"))
	if plain == "" {
		c.String(http.StatusUnauthorized, "缺少 token")
		return
	}
	var row model.DSHToken
	if err := h.db.Where("token_hash = ?", sha256hex(plain)).First(&row).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.String(http.StatusUnauthorized, "token 无效")
			return
		}
		c.String(http.StatusInternalServerError, "查询 token 失败")
		return
	}
	now := time.Now()
	switch {
	case row.RevokedAt != nil:
		c.String(http.StatusGone, "token 已被吊销")
		return
	case now.After(row.ExpiresAt):
		c.String(http.StatusGone, "token 已过期")
		return
	case row.Kind == dshTokenKindOneTime && row.UsedAt != nil:
		c.String(http.StatusGone, "token 已被使用")
		return
	}

	authority := requestAuthority(c)
	if !dshAllowedHosts()[authority] {
		c.String(http.StatusForbidden, "不支持的访问入口")
		return
	}
	// 令牌只在签发者仍具备管理级别时有效：签发人被降级、停用或删除后，旧链接立即失效。
	var creator model.User
	if err := h.db.Select("id", "level", "status").First(&creator, row.CreatedBy).Error; err != nil ||
		creator.Status != 1 || creator.Level < dshTokenMinLevel() {
		c.String(http.StatusForbidden, "令牌已失效")
		return
	}
	name, value, expires, err := dshSessionCookie(authority, dshCookieMaxAgeDays*24*time.Hour)
	if err != nil {
		c.String(http.StatusInternalServerError, "签发会话失败")
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   dshCookieMaxAgeDays * 24 * 3600,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})

	updates := map[string]any{"last_used_at": now}
	if row.Kind == dshTokenKindOneTime {
		updates["used_at"] = now
	}
	h.db.Model(&row).Updates(updates)

	next := strings.TrimSpace(c.Query("next"))
	if !strings.HasPrefix(next, "/dsh") || strings.HasPrefix(next, "//") {
		next = "/dsh/"
	}
	c.Redirect(http.StatusFound, next)
}

// RegisterDSHPublicRouter 注册免登录的 token 兑换入口（挂在 engine 上，绕过 /v1 认证）。
func RegisterDSHPublicRouter(r *gin.Engine, db *gorm.DB) {
	h := NewAPIHandler(db, nil)
	r.GET("/dsh/auth", h.DSHAuth)
}
