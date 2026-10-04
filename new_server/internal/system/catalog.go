package system

import (
	"fmt"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/internal/model"
	"syc-file/pkg/logger"
)

// 内置路由目录：与桌面端 / Web 端「页面注册表」里的 component key 一一对应
// （key = 相对 src/ 的 .vue 路径）。新增页面时在这里加一行，启动时按 Code 缺失才插入，
// 已有条目（可能被超级管理员改过标题 / 级别 / 开关）不会被覆盖。
//
// Component 为空表示纯菜单分组（目录）。目录本身不做权限判定：只在其下至少有一个可见页面时才出现。
// MinLevel 是登录用户（level>=1）的最低可见级别；游客(0)只能看到被明确授权的页面，与 MinLevel 无关。
// Perm 决定游客拿到该页面后额外放行哪些接口（见 guest_perm.go），空 = 不放行任何额外接口。
var defaultRoutes = []model.Route{
	{Code: "dashboard", Path: "/dashboard", Name: "Dashboard", Component: "views/Dashboard.vue", Title: "首页", Icon: "home", Sort: 10, MinLevel: 1, Perm: "dashboard"},

	{Code: "file", Path: "/file", Title: "文件管理", Icon: "file", Sort: 20, MinLevel: 1},
	{Code: "file.list", ParentCode: "file", Path: "/file/list", Name: "FileList", Component: "views/file/List.vue", Title: "文件列表", Icon: "file-list", Sort: 21, MinLevel: 1, Perm: "file.browse"},
	{Code: "file.upload", ParentCode: "file", Path: "/file/upload", Name: "FileUpload", Component: "views/file/Upload.vue", Title: "上传管理", Icon: "file-list", Sort: 22, MinLevel: 1, Perm: "file.upload"},
	{Code: "file.catalog", ParentCode: "file", Path: "/file/catalog", Name: "Catalog", Component: "views/catalog/ViewCatalog.vue", Title: "文件目录", Icon: "file-list", Sort: 23, Hidden: true, MinLevel: 1, Perm: "file.browse"},
	{Code: "file.share", ParentCode: "file", Path: "/file/share", Name: "FileShareManage", Component: "views/share/ShareManage.vue", Title: "分享管理", Icon: "file-list", Sort: 24, MinLevel: 1, Perm: "share"},
	{Code: "file.quick-share", ParentCode: "file", Path: "/file/quick-share", Name: "QuickShare", Component: "views/share/QuickPaste.vue", Title: "快速分享", Icon: "file-list", Sort: 25, MinLevel: 1, Perm: "share.quick"},

	{Code: "sync", Path: "/sync", Title: "文件同步", Icon: "sync", Sort: 30, MinLevel: 1},
	{Code: "sync.manage", ParentCode: "sync", Path: "/sync/manage", Name: "SyncManage", Component: "views/sync/SyncManage.vue", Title: "同步管理", Icon: "sync", Sort: 31, MinLevel: 1, Perm: "sync"},
	{Code: "sync.watch", ParentCode: "sync", Path: "/sync/watch", Name: "SyncWatch", Component: "views/sync/SyncWatch.vue", Title: "目录监听", Icon: "sync", Sort: 32, MinLevel: 1, Perm: "sync"},

	{Code: "update", Path: "/update", Title: "应用更新", Icon: "setting", Sort: 40, MinLevel: 2},
	{Code: "update.manage", ParentCode: "update", Path: "/update/manage", Name: "AppUpdateManage", Component: "views/update/AppUpdateManage.vue", Title: "版本发布", Icon: "setting", Sort: 41, MinLevel: 2},

	{Code: "clipboard", Path: "/clipboard", Name: "ClipboardSync", Component: "views/clipboard/ClipboardSync.vue", Title: "剪贴板同步", Icon: "sync", Sort: 50, MinLevel: 1, Perm: "clipboard"},

	{Code: "monitor", Path: "/monitor", Title: "系统监控", Icon: "setting", Sort: 60, MinLevel: 1},
	{Code: "monitor.system", ParentCode: "monitor", Path: "/monitor/system", Name: "MonitorSystem", Component: "views/monitor/System.vue", Title: "系统状态", Icon: "setting", Sort: 61, MinLevel: 1, Perm: "monitor"},
	{Code: "monitor.network", ParentCode: "monitor", Path: "/monitor/network", Name: "MonitorNetwork", Component: "views/monitor/Network.vue", Title: "网络监控", Icon: "setting", Sort: 62, MinLevel: 1, Perm: "monitor"},
	{Code: "monitor.cache", ParentCode: "monitor", Path: "/monitor/cache", Name: "MonitorCacheManage", Component: "views/monitor/CacheManage.vue", Title: "缓存管理", Icon: "setting", Sort: 63, MinLevel: 2},

	{Code: "admin", Path: "/admin", Title: "系统管理", Icon: "setting", Sort: 80, MinLevel: 2},
	{Code: "admin.users", ParentCode: "admin", Path: "/admin/users", Name: "AdminUsers", Component: "views/admin/UserManage.vue", Title: "用户管理", Icon: "setting", Sort: 81, MinLevel: 2},
	{Code: "admin.guests", ParentCode: "admin", Path: "/admin/guests", Name: "AdminGuests", Component: "views/admin/GuestManage.vue", Title: "访客管理", Icon: "setting", Sort: 82, MinLevel: 2},
	{Code: "admin.routes", ParentCode: "admin", Path: "/admin/routes", Name: "AdminRoutes", Component: "views/admin/RouteManage.vue", Title: "路由管理", Icon: "setting", Sort: 83, MinLevel: 3},

	{Code: "transfers", Path: "/transfers", Name: "Transfers", Component: "views/transfer/TransferList.vue", Title: "传输列表", Icon: "file-list", Sort: 70, Hidden: true, MinLevel: 1, Perm: "transfers"},

	{Code: "person.center", Path: "/person/center", Name: "PersonCenter", Component: "views/person/PersonalCenter.vue", Title: "个人中心", Sort: 90, Hidden: true, MinLevel: 1},
	{Code: "person.edit", Path: "/person/edit", Name: "PersonEdit", Component: "views/person/EditProfile.vue", Title: "编辑资料", Sort: 91, Hidden: true, MinLevel: 1},
	{Code: "person.password", Path: "/person/password", Name: "PersonPassword", Component: "views/person/ChangePassword.vue", Title: "修改密码", Sort: 92, Hidden: true, MinLevel: 1, Perm: "account.password"},
	{Code: "person.quick-share", Path: "/person/quick-share", Name: "PersonQuickShareSettings", Component: "views/person/QuickShareSettings.vue", Title: "快传设置", Sort: 93, Hidden: true, MinLevel: 1, Perm: "share.quick"},
}

// seedRoutes 按 Code 缺失才插入。
func seedRoutes(db *gorm.DB) error {
	added := 0
	for _, r := range defaultRoutes {
		var n int64
		if err := db.Model(&model.Route{}).Where("code = ?", r.Code).Count(&n).Error; err != nil {
			return fmt.Errorf("检查路由 %s 失败: %w", r.Code, err)
		}
		if n > 0 {
			continue
		}
		r.Enabled = true
		if err := db.Create(&r).Error; err != nil {
			return fmt.Errorf("播种路由 %s 失败: %w", r.Code, err)
		}
		added++
	}
	if added > 0 {
		logger.Logger.Info("已播种内置路由", zap.Int("added", added))
	}
	return nil
}
