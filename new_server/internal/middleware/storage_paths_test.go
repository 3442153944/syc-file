package middleware

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"syc-file/internal/model"
	"syc-file/internal/storage"
	"syc-file/pkg/response"
	"syc-file/pkg/token"
)

func TestOnlySuperSeesSystemPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "paths.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if db.AutoMigrate(&model.User{}, &model.StoragePool{}, &model.UploadSpace{}) != nil {
		t.Fatal("建表失败")
	}
	u := model.User{ID: 1, Username: "guest", Password: "test", Status: 1, Level: model.LevelUser}
	db.Create(&u)
	p := model.StoragePool{Name: "数据盘", RootPath: t.TempDir(), Enabled: true}
	db.Create(&p)
	s := model.UploadSpace{UserID: 1, PoolID: p.ID, QuotaBytes: 100, Enabled: true}
	db.Create(&s)
	root := storage.Root(p, s)
	if os.MkdirAll(root, 0700) != nil {
		t.Fatal("创建目录失败")
	}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("UserInfo", &token.Claims{UserID: 1}) })
	r.Use(StoragePaths(db))
	r.GET("/paths", func(c *gin.Context) {
		response.OK(c, gin.H{"path": filepath.Join(root, "a.txt"), "default_path": filepath.Join(root, storage.DefaultFolder), "parent_path": p.RootPath, "local_path": "用户设备目录", "disks": []string{p.RootPath}})
	})
	for _, level := range []int8{model.LevelGuest, model.LevelUser, model.LevelAdmin, model.LevelSuper} {
		db.Model(&model.User{}).Where("id = 1").Update("level", level)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/paths", nil))
		var body struct {
			Data map[string]any `json:"data"`
		}
		if json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatal(w.Body.String())
		}
		if level < model.LevelSuper {
			if body.Data["default_path"] != "/@space/我的文件" {
				t.Fatal("默认目录泄露物理路径", w.Body.String())
			}
			if body.Data["path"] != "/@space/a.txt" || body.Data["parent_path"] != "" || len(body.Data["disks"].([]any)) != 0 {
				t.Fatalf("低权限账号暴露系统目录: %s", w.Body.String())
			}
			if bytes.Contains(w.Body.Bytes(), []byte(strings.ReplaceAll(p.RootPath, "\\", "\\\\"))) {
				t.Fatal("实际磁盘路径泄露")
			}
		} else if body.Data["path"] != filepath.Join(root, "a.txt") {
			t.Fatal("系统管理员应看到实际目录")
		}
	}
	full, err := storage.ResolveVirtual(db, 1, "/@space/a.txt")
	if err != nil || full != filepath.Join(root, "a.txt") {
		t.Fatal("虚拟路径解析错误", full, err)
	}
	for _, path := range []string{"/@space/../secret", "/@space/.children/2/a", "/@space/.uploads/a", "/@space/.CHILDREN/2/a", "/@space/.UPLOADS/a"} {
		if _, err := storage.ResolveVirtual(db, 1, path); err == nil {
			t.Fatal("非法虚拟路径未拒绝", path)
		}
	}
}
