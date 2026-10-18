package system

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"syc-file/internal/middleware"
	"syc-file/internal/model"
	"syc-file/pkg/token"
)

// 菜单路径与文件路径共用 path 字段，但菜单 URL 不应被磁盘路径脱敏清空。
func TestRouteURLsSurviveStorageResponseTransform(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "routes.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.AutoMigrate(&model.User{}, &model.Route{}, &model.UserRoute{}, &model.StoragePool{}, &model.UploadSpace{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{ID: 7, Username: "admin_test", Password: "test", Level: model.LevelAdmin, Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []model.Route{
		{Code: "file.list", Path: "/file/list", Component: "views/file/List.vue", Title: "文件管理", MinLevel: model.LevelUser, Enabled: true},
		{Code: "admin.users", Path: "/admin/users", Component: "views/admin/UserManage.vue", Title: "用户管理", MinLevel: model.LevelAdmin, Enabled: true},
		{Code: "admin.routes", Path: "/admin/routes", Component: "views/admin/RouteManage.vue", Title: "路由管理", MinLevel: model.LevelSuper, Enabled: true},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.UserRoute{UserID: 7, RouteCode: "file.list"}).Error; err != nil {
		t.Fatal(err)
	}
	level := int8(model.LevelAdmin)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("Auth", true)
		c.Set("UserInfo", &token.Claims{UserID: 7})
		c.Set("UserLevel", level)
	})
	r.Use(middleware.StoragePaths(db))
	h := &Handler{db: db}
	r.GET("/v1/routes", h.MyRoutes)
	r.GET("/v1/admin/guest-routes", h.GuestGrantableRoutes)
	for _, lv := range []int8{model.LevelGuest, model.LevelUser, model.LevelAdmin, model.LevelSuper} {
		level = lv
		if err := db.Model(&model.User{}).Where("id = ?", 7).Update("level", lv).Error; err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/routes", nil))
		var body struct {
			Code int `json:"code"`
			Data struct {
				LoggedIn bool       `json:"logged_in"`
				Level    int8       `json:"level"`
				Routes   []routeDTO `json:"routes"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Code != 200 || !body.Data.LoggedIn || body.Data.Level != lv {
			t.Fatal("身份信息错误", w.Body.String())
		}
		wantCount := 1
		if lv >= model.LevelAdmin {
			wantCount = 2
		}
		if lv >= model.LevelSuper {
			wantCount = 3
		}
		if len(body.Data.Routes) != wantCount {
			t.Fatalf("level=%d 菜单权限筛选错误: %s", lv, w.Body.String())
		}
		for _, row := range body.Data.Routes {
			want := map[string]string{"file.list": "/file/list", "admin.users": "/admin/users", "admin.routes": "/admin/routes"}[row.Code]
			if row.Path != want {
				t.Errorf("level=%d 菜单路径被错误清空: code=%s path=%q want=%q", lv, row.Code, row.Path, want)
			}
		}
	}
	level = model.LevelAdmin
	if err := db.Model(&model.User{}).Where("id = ?", 7).Update("level", level).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/admin/guest-routes", nil))
	var guests struct {
		Code int        `json:"code"`
		Data []routeDTO `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &guests); err != nil {
		t.Fatal(err)
	}
	if guests.Code != 200 || len(guests.Data) != 1 || guests.Data[0].Path != "/file/list" {
		t.Fatal("访客授权页面路径被清空", w.Body.String())
	}
}
