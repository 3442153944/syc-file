package system

import (
	"strings"
	"testing"

	"syc-file/internal/model"
)

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{"/a/b/": "/a/b", "/a/b": "/a/b", "/": "/", "  /x/ ": "/x"}
	for in, want := range cases {
		if got := normalizePath(in); got != want {
			t.Errorf("normalizePath(%q)=%q 期望 %q", in, got, want)
		}
	}
}

func TestValidateBasicPath(t *testing.T) {
	good := []string{"/a", "/admin/report", "/a-b/c_d/e.f", "/grp.one"}
	bad := []string{"a", "", "/a/../b", "/a//b", "/a b", "/中文", "/" + strings.Repeat("x", 201)}
	for _, p := range good {
		if msg := validateBasicPath(p); msg != "" {
			t.Errorf("%q 应合法，得到: %s", p, msg)
		}
	}
	for _, p := range bad {
		if validateBasicPath(p) == "" {
			t.Errorf("%q 应被拒绝", p)
		}
	}
}

func TestValidateComponent(t *testing.T) {
	for _, c := range []string{"", "views/a/B.vue", "views/admin/UserManage.vue", "views/x.y/Z_1.vue"} {
		if msg := validateComponent(c); msg != "" {
			t.Errorf("%q 应合法，得到: %s", c, msg)
		}
	}
	for _, c := range []string{"foo.vue", "views/a/B.js", "views/../x.vue", "/views/a/B.vue", "views/a/B.vue/..", "competent/home.vue", "views/a/ B.vue"} {
		if validateComponent(c) == "" {
			t.Errorf("%q 应被拒绝", c)
		}
	}
}

func TestRouteCodeRe(t *testing.T) {
	for _, c := range []string{"ab", "admin.report", "a-b_c.d", "x1"} {
		if !routeCodeRe.MatchString(c) {
			t.Errorf("编码 %q 应合法", c)
		}
	}
	for _, c := range []string{"", "a", "Admin", ".ab", "-ab", "a b", "a/b", strings.Repeat("x", 64)} {
		if routeCodeRe.MatchString(c) {
			t.Errorf("编码 %q 应被拒绝", c)
		}
	}
}

func TestCustomRouteName(t *testing.T) {
	cases := map[string]string{
		"admin.report": "CustomAdminReport",
		"foo-bar_baz":  "CustomFooBarBaz",
		"x1":           "CustomX1",
	}
	for code, want := range cases {
		got := customRouteName(code)
		if got != want {
			t.Errorf("customRouteName(%q)=%q 期望 %q", code, got, want)
		}
		if !routeNameRe.MatchString(got) {
			t.Errorf("生成的路由名 %q 必须能通过路由名校验", got)
		}
	}
}

func TestValidatePerm(t *testing.T) {
	if validatePerm("") != "" || validatePerm("file.browse") != "" {
		t.Error("空 / 已知分组应合法")
	}
	if validatePerm("root.all") == "" {
		t.Error("未知分组应被拒绝")
	}
	// 每个可选的 perm 都必须有中文名，否则下拉里会显示空白
	for k := range guestAPIs {
		if guestPermLabels[k] == "" {
			t.Errorf("perm %q 缺少中文名", k)
		}
	}
}

func TestReservedBaseRoutes(t *testing.T) {
	for _, p := range []string{"/", "/login", "/register", "/reset", "/init", "/empty"} {
		if !reservedPaths[p] {
			t.Errorf("基础路径 %s 必须保留，否则下发的路由能盖住登录页", p)
		}
	}
	for _, n := range []string{"Home", "Login", "Register", "ResetPassword", "Init", "NotFound", "Empty"} {
		if !reservedNames[n] {
			t.Errorf("基础路由名 %s 必须保留", n)
		}
	}
}

// 内置目录自身必须能通过新增路由时用的校验口径，否则「内置」与「自定义」的规则会自相矛盾。
func TestCatalogPassesOwnValidation(t *testing.T) {
	codes, paths, names := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, r := range defaultRoutes {
		if !routeCodeRe.MatchString(r.Code) {
			t.Errorf("内置编码 %q 不符合编码规则", r.Code)
		}
		if codes[r.Code] {
			t.Errorf("内置编码重复: %s", r.Code)
		}
		codes[r.Code] = true
		if msg := validateBasicPath(r.Path); msg != "" {
			t.Errorf("内置路径 %q: %s", r.Path, msg)
		}
		if r.Component != "" {
			if msg := validateComponent(r.Component); msg != "" {
				t.Errorf("内置组件 %q: %s", r.Component, msg)
			}
			if !routeNameRe.MatchString(r.Name) || reservedNames[r.Name] {
				t.Errorf("内置路由名 %q 非法或与基础路由冲突", r.Name)
			}
			if names[r.Name] || paths[strings.ToLower(r.Path)] {
				t.Errorf("内置页面 name/path 重复: %s %s", r.Name, r.Path)
			}
			names[r.Name], paths[strings.ToLower(r.Path)] = true, true
			if reservedPaths[strings.ToLower(r.Path)] {
				t.Errorf("内置路径 %q 占用了基础路由", r.Path)
			}
		}
		if msg := validatePerm(r.Perm); msg != "" {
			t.Errorf("内置路由 %s 的 perm: %s", r.Code, msg)
		}
	}
}

func routeSet(rs []routeDTO) map[string]bool {
	m := map[string]bool{}
	for _, r := range rs {
		m[r.Code] = true
	}
	return m
}

func TestVisibleRoutes(t *testing.T) {
	all := []model.Route{
		{Code: "g", Title: "分组", Enabled: true, MinLevel: 1},
		{Code: "g.user", ParentCode: "g", Component: "views/a/U.vue", Path: "/g/u", Enabled: true, MinLevel: 1},
		{Code: "g.admin", ParentCode: "g", Component: "views/a/A.vue", Path: "/g/a", Enabled: true, MinLevel: 2},
		{Code: "empty", Title: "空分组", Enabled: true, MinLevel: 1},
		{Code: "top", Component: "views/a/T.vue", Path: "/t", Enabled: true, MinLevel: 3},
		{Code: "orphan", ParentCode: "gone", Component: "views/a/O.vue", Path: "/o", Enabled: true, MinLevel: 1},
	}

	user := routeSet(visibleRoutes(all, model.LevelUser, nil))
	if !user["g"] || !user["g.user"] || user["g.admin"] || user["top"] {
		t.Errorf("普通用户可见集合不对: %v", user)
	}
	if user["empty"] {
		t.Error("没有可见子页面的分组不应出现")
	}
	if user["orphan"] {
		t.Error("父级不存在（被停用）的页面不应出现")
	}

	admin := routeSet(visibleRoutes(all, model.LevelAdmin, nil))
	if !admin["g.admin"] || admin["top"] {
		t.Errorf("管理员可见集合不对: %v", admin)
	}

	super := routeSet(visibleRoutes(all, model.LevelSuper, nil))
	if !super["top"] || !super["g.admin"] {
		t.Errorf("超级管理员应看到全部页面: %v", super)
	}

	// 访客：与 MinLevel 无关，只看被授权的页面（连同其父级分组）
	guest := routeSet(visibleRoutes(all, model.LevelGuest, map[string]bool{"g.admin": true}))
	if !guest["g.admin"] || !guest["g"] || guest["g.user"] || guest["top"] {
		t.Errorf("访客只应看到被授权页面及其父级: %v", guest)
	}
	if got := visibleRoutes(all, model.LevelGuest, nil); len(got) != 0 {
		t.Errorf("没有任何授权的访客不应看到任何路由: %v", got)
	}
}
