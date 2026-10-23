package admin

// VPS 节点面板入口的登记与下发。
//
// 目标：不再靠人肉 SSH 隧道去开面板。每条节点记录自带
//   1) 一个随机入口路径（AccessPath，唯一）
//   2) 一把随机访问秘钥（AccessKeyEnc，AES-GCM 加密入库）
// 访问时点「打开面板」→ 后端用秘钥拼出入口 URL → 节点上的 nginx 校验秘钥、
// 种 cookie、302 到面板真实路径（PanelPath），面板路径再按同一个 cookie 放行。
//
// 入口路径不能直接反代面板：x-ui 的静态资源是绝对路径 /<webBasePath>/assets/*，
// 多套一层前缀会全部 404。所以入口只做「校验 + 跳转」，不承载资源。
//
// 所有写操作都 requireAdmin。

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"syc-file/internal/model"
	"syc-file/pkg/token"
)

const (
	vpsAccessPathPrefix = "/p/"
	vpsCookiePrefix     = "vp_"
	vpsProbeTimeout     = 8 * time.Second
)

var vpsCodeRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ── 随机量与派生值 ────────────────────────────────────────────────

func vpsRandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func vpsRandomKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// vpsCookieName 每条节点一个 cookie 名，避免互相串用。
func vpsCookieName(code string) string {
	return vpsCookiePrefix + strings.NewReplacer("-", "_", ".", "_").Replace(code)
}

// vpsCookieValue 绑定「节点 + 秘钥」的短哈希，写进 cookie 也写进 nginx 片段。
func vpsCookieValue(code, key string) string {
	sum := sha256.Sum256([]byte("vps-cookie:" + code + ":" + key))
	return hex.EncodeToString(sum[:8])
}

// normalizePath 统一成「以 / 开头、以 / 结尾」，根路径返回 "/"。
func vpsNormalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

// ── 视图与请求体 ──────────────────────────────────────────────────

// vpsNodeView 出接口的结构：藏掉密文，附上可直接打开的入口地址。
type vpsNodeView struct {
	model.VpsNode
	EntryURL string `json:"entry_url"` // 每次现签，含明文秘钥
}

func (h *APIHandler) vpsView(n *model.VpsNode) vpsNodeView {
	v := vpsNodeView{VpsNode: *n}
	if key, err := token.OpenString(n.AccessKeyEnc); err == nil {
		v.EntryURL = vpsEntryURL(n, key)
	}
	return v
}

type vpsNodeReq struct {
	Code        string `json:"code"`
	Title       string `json:"title"`
	Region      string `json:"region"`
	Provider    string `json:"provider"`
	Host        string `json:"host"`
	GatewayHost string `json:"gateway_host"`
	PanelScheme string `json:"panel_scheme"`
	PanelPort   int    `json:"panel_port"`
	PanelPath   string `json:"panel_path"`
	GatewayPort int    `json:"gateway_port"`
	SubURL      string `json:"sub_url"`
	SubPort     int    `json:"sub_port"`
	Remark      string `json:"remark"`
	Sort        int    `json:"sort"`
	Enabled     *bool  `json:"enabled"`
}

func (r *vpsNodeReq) clean() {
	r.Code = strings.ToLower(strings.TrimSpace(r.Code))
	r.Title = strings.TrimSpace(r.Title)
	r.Region = strings.TrimSpace(r.Region)
	r.Provider = strings.TrimSpace(r.Provider)
	r.Host = strings.TrimSpace(r.Host)
	r.GatewayHost = strings.TrimSpace(r.GatewayHost)
	r.SubURL = strings.TrimSpace(r.SubURL)
	if r.SubPort <= 0 || r.SubPort > 65535 {
		r.SubPort = 2096
	}
	r.Remark = strings.TrimSpace(r.Remark)
	r.PanelScheme = strings.ToLower(strings.TrimSpace(r.PanelScheme))
	if r.PanelScheme != "http" && r.PanelScheme != "https" {
		r.PanelScheme = "https"
	}
	r.PanelPath = vpsNormalizePath(r.PanelPath)
	if r.PanelPort <= 0 || r.PanelPort > 65535 {
		r.PanelPort = 443
	}
	if r.GatewayPort <= 0 || r.GatewayPort > 65535 {
		r.GatewayPort = 443
	}
}

func (r *vpsNodeReq) validate() error {
	if !vpsCodeRe.MatchString(r.Code) {
		return errors.New("节点标识只能是 1-32 位小写字母/数字/-/_")
	}
	if r.Title == "" {
		return errors.New("请填写节点名称")
	}
	if r.Host == "" {
		return errors.New("请填写节点域名或 IP")
	}
	return nil
}

// ── 入口 URL 与 nginx 片段 ────────────────────────────────────────

func vpsGatewayHost(n *model.VpsNode) string {
	if h := strings.TrimSpace(n.GatewayHost); h != "" {
		return h
	}
	return n.Host
}

// vpsEntryURL 拼出「入口路径 + 秘钥」的完整地址。
func vpsEntryURL(n *model.VpsNode, key string) string {
	return vpsGatewayBase(n) + n.AccessPath + "?k=" + key
}

// VpsNodeSnippet 生成节点上要落地的 nginx 片段（人工/脚本粘贴）。
//
// 面板路径为 "/" 时（ny 那种）不能挂在站点根上，片段会给一个独立端口的 server 块。
func (h *APIHandler) VpsNodeSnippet(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var n model.VpsNode
	if err := h.db.First(&n, id).Error; err != nil {
		jsonErr(c, 404, "节点不存在")
		return
	}
	key, err := token.OpenString(n.AccessKeyEnc)
	if err != nil {
		jsonErr(c, 500, "秘钥解密失败："+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "ok",
		"data": gin.H{
			"entry_url": vpsEntryURL(&n, key),
			"snippet":   buildVpsSnippet(&n, key),
		},
	})
}

// VpsAllSnippets 一次性导出所有启用节点的网关片段：4 台机器逐个手点太麻烦，
// 这里拼成一段文本，复制后按注释分派到各自 nginx。
func (h *APIHandler) VpsAllSnippets(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	var rows []model.VpsNode
	if err := h.db.Where("enabled = ?", true).Order("sort asc, id asc").Find(&rows).Error; err != nil {
		jsonErr(c, 500, "查询失败："+err.Error())
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# VPS 面板网关片段 —— 共 %d 个节点\n", len(rows))
	b.WriteString("# 每段贴到对应节点 nginx 的 server 块内，替换 CERT_FULLCHAIN / CERT_PRIVKEY，\n")
	b.WriteString("# 然后 nginx -t && systemctl reload nginx\n")
	for i := range rows {
		key, err := token.OpenString(rows[i].AccessKeyEnc)
		if err != nil {
			continue
		}
		b.WriteString("\n")
		b.WriteString(buildVpsSnippet(&rows[i], key))
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"text": b.String()}})
}

func buildVpsSnippet(n *model.VpsNode, key string) string {
	ck := vpsCookieName(n.Code)
	cv := vpsCookieValue(n.Code, key)
	upstream := fmt.Sprintf("%s://127.0.0.1:%d", n.PanelScheme, n.PanelPort)
	host := vpsGatewayHost(n)

	header := fmt.Sprintf(`# ── %s（%s）面板入口 ─────────────────────────────
# 入口：https://%s%s?k=<访问秘钥>
# 落地后请把 %s 换成该机实际证书路径
#   jp: /root/cert.crt + /root/private.key
#   其它: /etc/nginx/ssl/fullchain.pem + /etc/nginx/ssl/privkey.pem
`, n.Title, n.Code, host, n.AccessPath, "CERT")

	entry := fmt.Sprintf(`
location %s {
    if ($arg_k != "%s") { return 403; }
    add_header Set-Cookie "%s=%s; Path=/; HttpOnly; SameSite=Lax" always;
    return 302 %s;
}`, n.AccessPath, key, ck, cv, n.PanelPath)

	panel := fmt.Sprintf(`
location %s {
    if ($cookie_%s != "%s") { return 403; }
    proxy_pass %s%s;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_read_timeout 300s;
}`, n.PanelPath, ck, cv, upstream, n.PanelPath)

	// 订阅：只在填了「面板侧相对路径」时生成，和面板共用同一个 cookie。
	// 这样订阅端口继续只监听回环，后端凭 cookie 经网关读到 Subscription-Userinfo。
	sub := ""
	if strings.HasPrefix(n.SubURL, "/") {
		sub = fmt.Sprintf(`
# 订阅入口（供后端读流量，同样按 cookie 放行）
location %s {
    if ($cookie_%s != "%s") { return 403; }
    proxy_pass http://127.0.0.1:%d%s;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
}`, n.SubURL, ck, cv, n.SubPort, n.SubURL)
	}

	// 面板在根路径：独立端口，避免和站点根冲突。
	if n.PanelPath == "/" {
		return fmt.Sprintf(`%s
server {
    listen %d ssl;
    listen [::]:%d ssl;
    server_name %s;

    ssl_certificate     CERT_FULLCHAIN;
    ssl_certificate_key CERT_PRIVKEY;
    server_tokens off;
%s%s

    location / {
        if ($cookie_%s != "%s") { return 403; }
        proxy_pass %s/;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_read_timeout 300s;
    }
}`, header, n.GatewayPort, n.GatewayPort, host, entry, sub, ck, cv, upstream)
	}

	return header + entry + panel + sub
}

// ── CRUD ──────────────────────────────────────────────────────────

func (h *APIHandler) ListVpsNodes(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	p := parsePage(c)
	base := h.db.Model(&model.VpsNode{})
	if kw := strings.TrimSpace(c.Query("keyword")); kw != "" {
		like := "%" + kw + "%"
		base = base.Where("code LIKE ? OR title LIKE ? OR host LIKE ? OR provider LIKE ?", like, like, like, like)
	}
	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		jsonErr(c, 500, "统计失败："+err.Error())
		return
	}
	var rows []model.VpsNode
	if err := base.Session(&gorm.Session{}).Order("sort asc, id asc").
		Offset(p.offset()).Limit(p.PageSize).Find(&rows).Error; err != nil {
		jsonErr(c, 500, "查询失败："+err.Error())
		return
	}
	views := make([]vpsNodeView, 0, len(rows))
	for i := range rows {
		views = append(views, h.vpsView(&rows[i]))
	}
	jsonPage(c, views, total, p)
}

func (h *APIHandler) CreateVpsNode(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	var req vpsNodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "请求体有误："+err.Error())
		return
	}
	req.clean()
	if err := req.validate(); err != nil {
		jsonErr(c, 400, err.Error())
		return
	}
	var dup int64
	h.db.Model(&model.VpsNode{}).Where("code = ?", req.Code).Count(&dup)
	if dup > 0 {
		jsonErr(c, 409, "节点标识已存在")
		return
	}

	suffix, err := vpsRandomHex(6)
	if err != nil {
		jsonErr(c, 500, "生成随机路径失败")
		return
	}
	key, err := vpsRandomKey()
	if err != nil {
		jsonErr(c, 500, "生成访问秘钥失败")
		return
	}
	enc, err := token.SealString(key)
	if err != nil {
		jsonErr(c, 500, "秘钥加密失败："+err.Error())
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	n := model.VpsNode{
		Code: req.Code, Title: req.Title, Region: req.Region, Provider: req.Provider,
		Host: req.Host, GatewayHost: req.GatewayHost,
		PanelScheme: req.PanelScheme, PanelPort: req.PanelPort,
		PanelPath: req.PanelPath, GatewayPort: req.GatewayPort,
		AccessPath: vpsAccessPathPrefix + suffix + "/", AccessKeyEnc: enc,
		SubURL: req.SubURL, SubPort: req.SubPort, Remark: req.Remark, Enabled: enabled, Sort: req.Sort,
		LastStatus: "unknown",
	}
	if err := h.db.Create(&n).Error; err != nil {
		jsonErr(c, 500, "创建失败："+err.Error())
		return
	}
	jsonOK(c, gin.H{"node": h.vpsView(&n), "access_key": key})
}

func (h *APIHandler) UpdateVpsNode(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var n model.VpsNode
	if err := h.db.First(&n, id).Error; err != nil {
		jsonErr(c, 404, "节点不存在")
		return
	}
	var req vpsNodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "请求体有误："+err.Error())
		return
	}
	// code 不允许改（cookie 名与它绑定）
	req.Code = n.Code
	req.clean()
	if strings.TrimSpace(req.Title) == "" {
		jsonErr(c, 400, "请填写节点名称")
		return
	}
	if strings.TrimSpace(req.Host) == "" {
		jsonErr(c, 400, "请填写节点域名或 IP")
		return
	}
	n.Title, n.Region, n.Provider = req.Title, req.Region, req.Provider
	n.Host, n.GatewayHost = req.Host, req.GatewayHost
	n.PanelScheme, n.PanelPort, n.PanelPath, n.GatewayPort = req.PanelScheme, req.PanelPort, req.PanelPath, req.GatewayPort
	n.SubURL, n.SubPort, n.Remark, n.Sort = req.SubURL, req.SubPort, req.Remark, req.Sort
	if req.Enabled != nil {
		n.Enabled = *req.Enabled
	}
	if err := h.db.Save(&n).Error; err != nil {
		jsonErr(c, 500, "保存失败："+err.Error())
		return
	}
	jsonOK(c, h.vpsView(&n))
}

func (h *APIHandler) DeleteVpsNode(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := h.db.Delete(&model.VpsNode{}, id).Error; err != nil {
		jsonErr(c, 500, "删除失败："+err.Error())
		return
	}
	jsonOK(c, nil)
}

// RotateVpsNode 重新生成入口路径与访问秘钥（怀疑秘钥外泄时用）。
func (h *APIHandler) RotateVpsNode(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var n model.VpsNode
	if err := h.db.First(&n, id).Error; err != nil {
		jsonErr(c, 404, "节点不存在")
		return
	}
	suffix, err := vpsRandomHex(6)
	if err != nil {
		jsonErr(c, 500, "生成随机路径失败")
		return
	}
	key, err := vpsRandomKey()
	if err != nil {
		jsonErr(c, 500, "生成访问秘钥失败")
		return
	}
	enc, err := token.SealString(key)
	if err != nil {
		jsonErr(c, 500, "秘钥加密失败："+err.Error())
		return
	}
	n.AccessPath = vpsAccessPathPrefix + suffix + "/"
	n.AccessKeyEnc = enc
	if err := h.db.Save(&n).Error; err != nil {
		jsonErr(c, 500, "保存失败："+err.Error())
		return
	}
	jsonOK(c, gin.H{"node": h.vpsView(&n), "access_key": key, "snippet": buildVpsSnippet(&n, key)})
}

// CheckVpsNode 探活：请求入口地址（正确秘钥应得到 302），顺带读订阅流量。
func (h *APIHandler) CheckVpsNode(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var n model.VpsNode
	if err := h.db.First(&n, id).Error; err != nil {
		jsonErr(c, 404, "节点不存在")
		return
	}
	key, err := token.OpenString(n.AccessKeyEnc)
	if err != nil {
		jsonErr(c, 500, "秘钥解密失败："+err.Error())
		return
	}
	url := vpsEntryURL(&n, key)
	client := &http.Client{
		Timeout: vpsProbeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // 只看第一跳
		},
	}
	start := time.Now()
	resp, err := client.Get(url)
	latency := int(time.Since(start).Milliseconds())
	now := time.Now()
	n.LastCheckedAt = &now
	n.LastLatencyMs = latency
	if err != nil {
		n.LastStatus = "fail"
		n.LastError = truncateStr(err.Error(), 240)
	} else {
		resp.Body.Close()
		if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusTemporaryRedirect {
			n.LastStatus = "ok"
			n.LastError = ""
		} else {
			n.LastStatus = "fail"
			n.LastError = fmt.Sprintf("入口返回 %d，检查 nginx 片段是否已生效", resp.StatusCode)
		}
	}
	// 订阅流量（可选）：x-ui 的订阅响应头 Subscription-Userinfo。
	// SubURL 为相对路径时经同一个网关取（先过入口拿 cookie），订阅端口无需对公网开放。
	if n.LastStatus == "ok" && strings.TrimSpace(n.SubURL) != "" {
		if up, down, total, expire, e := fetchNodeUsage(&n, key); e == nil {
			n.TrafficUp, n.TrafficDown, n.TrafficTotal = up, down, total
			n.TrafficExpire = expire
		}
	}
	h.db.Save(&n)
	jsonOK(c, h.vpsView(&n))
}

// vpsGatewayBase 网关基址：协议 + 域名 +（非默认端口才带端口）。
func vpsGatewayBase(n *model.VpsNode) string {
	scheme := "https"
	if n.GatewayPort == 80 {
		scheme = "http"
	}
	if (scheme == "https" && n.GatewayPort == 443) || (scheme == "http" && n.GatewayPort == 80) {
		return scheme + "://" + vpsGatewayHost(n)
	}
	return fmt.Sprintf("%s://%s:%d", scheme, vpsGatewayHost(n), n.GatewayPort)
}

// fetchNodeUsage 取该节点的订阅流量。
//
//   - SubURL 是完整地址：直接取；
//   - SubURL 是面板侧相对路径：先访问入口让 nginx 种下 cookie，再带 cookie 取订阅，
//     这样订阅端口可以继续只监听 127.0.0.1。
func fetchNodeUsage(n *model.VpsNode, key string) (int64, int64, int64, *time.Time, error) {
	sub := strings.TrimSpace(n.SubURL)
	if sub == "" {
		return 0, 0, 0, nil, errors.New("未配置订阅地址")
	}
	if strings.HasPrefix(sub, "http://") || strings.HasPrefix(sub, "https://") {
		return fetchSubUsage(http.DefaultClient, sub)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return 0, 0, 0, nil, err
	}
	client := &http.Client{Timeout: vpsProbeTimeout, Jar: jar}
	// 1) 过入口拿 cookie（跟随 302 到面板首页）
	if resp, e := client.Get(vpsEntryURL(n, key)); e == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
	}
	// 2) 带 cookie 取订阅
	return fetchSubUsage(client, vpsGatewayBase(n)+sub)
}

// fetchSubUsage 读 x-ui / v2ray 订阅响应头：upload=..; download=..; total=..; expire=..
func fetchSubUsage(client *http.Client, subURL string) (int64, int64, int64, *time.Time, error) {
	req, err := http.NewRequest(http.MethodGet, subURL, nil)
	if err != nil {
		return 0, 0, 0, nil, err
	}
	req.Header.Set("User-Agent", "v2rayN/6.0")
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, 0, nil, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	raw := resp.Header.Get("Subscription-Userinfo")
	if raw == "" {
		return 0, 0, 0, nil, gorm.ErrRecordNotFound
	}
	var up, down, total, expire int64
	for _, part := range strings.Split(raw, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		v, _ := strconv.ParseInt(strings.TrimSpace(kv[1]), 10, 64)
		switch strings.ToLower(strings.TrimSpace(kv[0])) {
		case "upload":
			up = v
		case "download":
			down = v
		case "total":
			total = v
		case "expire":
			expire = v
		}
	}
	var exp *time.Time
	if expire > 0 {
		t := time.Unix(expire, 0)
		exp = &t
	}
	return up, down, total, exp, nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
