package admin

import (
	"strings"
	"testing"

	"syc-file/internal/model"
)

func TestVpsNormalizePath(t *testing.T) {
	cases := map[string]string{
		"":       "/",
		"/":      "/",
		"abc":    "/abc/",
		"/abc":   "/abc/",
		"/a/b/":  "/a/b/",
		" /x/y ": "/x/y/",
	}
	for in, want := range cases {
		if got := vpsNormalizePath(in); got != want {
			t.Errorf("vpsNormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVpsEntryURL(t *testing.T) {
	base := model.VpsNode{
		Code: "jp", Host: "jp.sunyuanling.cn", AccessPath: "/p/abc123/", GatewayPort: 443,
	}
	if got, want := vpsEntryURL(&base, "KEY"), "https://jp.sunyuanling.cn/p/abc123/?k=KEY"; got != want {
		t.Errorf("entry url = %q, want %q", got, want)
	}

	alt := base
	alt.GatewayPort = 9443
	if got, want := vpsEntryURL(&alt, "KEY"), "https://jp.sunyuanling.cn:9443/p/abc123/?k=KEY"; got != want {
		t.Errorf("entry url (9443) = %q, want %q", got, want)
	}

	plain := base
	plain.GatewayPort = 80
	if got, want := vpsEntryURL(&plain, "KEY"), "http://jp.sunyuanling.cn/p/abc123/?k=KEY"; got != want {
		t.Errorf("entry url (80) = %q, want %q", got, want)
	}

	gw := base
	gw.GatewayHost = "edge.sunyuanling.cn"
	if got, want := vpsEntryURL(&gw, "KEY"), "https://edge.sunyuanling.cn/p/abc123/?k=KEY"; got != want {
		t.Errorf("entry url (gateway host) = %q, want %q", got, want)
	}
}

func TestVpsCookieName(t *testing.T) {
	if got := vpsCookieName("jp"); got != "vp_jp" {
		t.Errorf("cookie name = %q", got)
	}
	if got := vpsCookieName("a-b.c"); got != "vp_a_b_c" {
		t.Errorf("cookie name with separators = %q", got)
	}
	// 同一个节点 + 同一把秘钥必须稳定，换节点或换秘钥必须变
	a := vpsCookieValue("jp", "K1")
	if a != vpsCookieValue("jp", "K1") {
		t.Error("cookie value not stable")
	}
	if a == vpsCookieValue("jp", "K2") || a == vpsCookieValue("ny", "K1") {
		t.Error("cookie value should change with code or key")
	}
}

func TestBuildVpsSnippetPathMode(t *testing.T) {
	n := &model.VpsNode{
		Code: "jp", Title: "东京-1", Host: "jp.sunyuanling.cn",
		PanelScheme: "http", PanelPort: 2053, PanelPath: "/1fa597d456cc34fc/",
		GatewayPort: 443, AccessPath: "/p/abc123def456/",
	}
	got := buildVpsSnippet(n, "KEY-XYZ")

	for _, want := range []string{
		"location /p/abc123def456/",
		`if ($arg_k != "KEY-XYZ") { return 403; }`,
		"return 302 /1fa597d456cc34fc/",
		`Set-Cookie "vp_jp=`,
		"location /1fa597d456cc34fc/",
		`if ($cookie_vp_jp != "`,
		"proxy_pass http://127.0.0.1:2053/1fa597d456cc34fc/;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("snippet missing %q\n---\n%s", want, got)
		}
	}
	if strings.Contains(got, "listen 443 ssl") {
		t.Error("path-mode snippet should not declare its own server block")
	}
}

func TestBuildVpsSnippetRootMode(t *testing.T) {
	n := &model.VpsNode{
		Code: "ny", Title: "纽约", Host: "ny.sunyuanling.cn",
		PanelScheme: "http", PanelPort: 2053, PanelPath: "/",
		GatewayPort: 9443, AccessPath: "/p/0011aabbccdd/",
	}
	got := buildVpsSnippet(n, "KEY-ROOT")

	for _, want := range []string{
		"listen 9443 ssl;",
		"server_name ny.sunyuanling.cn;",
		"location /p/0011aabbccdd/",
		"return 302 /",
		"proxy_pass http://127.0.0.1:2053/;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("root-mode snippet missing %q\n---\n%s", want, got)
		}
	}
}

func TestVpsNodeReqClean(t *testing.T) {
	r := vpsNodeReq{Code: " JP ", Title: " 东京 ", Host: " jp.sunyuanling.cn ", PanelScheme: "HTTP", PanelPath: "abc"}
	r.clean()
	if r.Code != "jp" || r.Title != "东京" || r.Host != "jp.sunyuanling.cn" {
		t.Errorf("trim failed: %+v", r)
	}
	if r.PanelScheme != "http" {
		t.Errorf("scheme = %q", r.PanelScheme)
	}
	if r.PanelPath != "/abc/" {
		t.Errorf("panel path = %q", r.PanelPath)
	}
	if r.PanelPort != 443 || r.GatewayPort != 443 {
		t.Errorf("defaults: panel=%d gateway=%d", r.PanelPort, r.GatewayPort)
	}

	bad := vpsNodeReq{Code: "JP!", Title: "x", Host: "h"}
	bad.clean()
	if err := bad.validate(); err == nil {
		t.Error("validate should reject bad code")
	}
	if bad.SubPort != 2096 {
		t.Errorf("sub port default = %d", bad.SubPort)
	}
}

func TestBuildVpsSnippetWithSubPath(t *testing.T) {
	n := &model.VpsNode{
		Code: "jp", Title: "东京-1", Host: "jp.sunyuanling.cn",
		PanelScheme: "http", PanelPort: 2053, PanelPath: "/1fa/",
		GatewayPort: 443, AccessPath: "/p/abc/",
		SubURL: "/sub/xyz/", SubPort: 2096,
	}
	got := buildVpsSnippet(n, "K")
	for _, want := range []string{
		"location /sub/xyz/",
		"proxy_pass http://127.0.0.1:2096/sub/xyz/;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("订阅片段缺少 %q\n---\n%s", want, got)
		}
	}
}

func TestBuildVpsSnippetNoSubForAbsoluteURL(t *testing.T) {
	n := &model.VpsNode{
		Code: "jp", Title: "东京-1", Host: "jp.sunyuanling.cn",
		PanelScheme: "http", PanelPort: 2053, PanelPath: "/1fa/",
		GatewayPort: 443, AccessPath: "/p/abc/",
		SubURL: "https://example.com/sub/xyz",
	}
	got := buildVpsSnippet(n, "K")
	if strings.Contains(got, "location https://") || strings.Contains(got, "订阅入口") {
		t.Errorf("完整地址的订阅不该生成反代\n---\n%s", got)
	}
}

func TestFetchNodeUsageRequiresSubURL(t *testing.T) {
	if _, _, _, _, err := fetchNodeUsage(&model.VpsNode{Code: "jp"}, "K"); err == nil {
		t.Error("未配置订阅地址时应报错")
	}
}
