package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"syc-file/internal/model"
	"syc-file/internal/storage"
	"syc-file/pkg/token"
)

func TestStorageAdministrationUsesLiveSuperLevel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "admin.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.AutoMigrate(&model.User{}, &model.StoragePool{}, &model.UploadSpace{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{ID: 1, Username: "admin", Password: "test", Level: model.LevelAdmin, Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	h := NewAPIHandler(db, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("UserInfo", &token.Claims{UserID: 1, Roles: []string{"admin"}}) })
	r.GET("/pools", h.ListStoragePools)
	r.POST("/pools", h.CreateStoragePool)
	r.PUT("/spaces/:id", h.PutUploadSpace)
	do := func(method, path string, body any) map[string]any {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var result map[string]any
		if json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal(w.Body.String())
		}
		return result
	}
	if got := do(http.MethodPost, "/pools", map[string]any{"name": "越权", "root_path": t.TempDir()}); got["code"] != float64(403) {
		t.Fatalf("普通管理员不得配置实际磁盘: %v", got)
	}
	if err := db.Model(&model.User{}).Where("id = 1").Update("level", model.LevelSuper).Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "uploads")
	pool := do(http.MethodPost, "/pools", map[string]any{"name": "测试磁盘", "root_path": path})
	if pool["code"] != float64(200) {
		t.Fatalf("系统管理员创建失败: %v", pool)
	}
	id := pool["data"].(map[string]any)["id"]
	space := do(http.MethodPut, "/spaces/1", map[string]any{"pool_id": id, "quota_bytes": 1024, "enabled": true})
	if space["code"] != float64(200) {
		t.Fatalf("空间分配失败: %v", space)
	}
	if err := db.Model(&model.User{}).Where("id = 1").Update("level", model.LevelAdmin).Error; err != nil {
		t.Fatal(err)
	}
	if got := do(http.MethodPut, "/spaces/1", map[string]any{"pool_id": id, "quota_bytes": 999999, "enabled": true}); got["code"] != float64(403) {
		t.Fatalf("降级后仍可通过旧令牌修改配额: %v", got)
	}
	for _, u := range []model.User{{ID: 2, Username: "child", Password: "test", Status: 1, Level: model.LevelUser}, {ID: 3, Username: "child2", Password: "test", Status: 1, Level: model.LevelUser}, {ID: 4, Username: "peer", Password: "test", Status: 1, Level: model.LevelAdmin}} {
		if db.Create(&u).Error != nil {
			t.Fatal("创建测试账号失败")
		}
	}
	if got := do(http.MethodPut, "/spaces/2", map[string]any{"pool_id": id, "quota_bytes": 600, "enabled": true}); got["code"] != float64(200) {
		t.Fatalf("向下分配失败: %v", got)
	}
	if got := do(http.MethodPut, "/spaces/3", map[string]any{"pool_id": id, "quota_bytes": 500, "enabled": true}); got["code"] == float64(200) {
		t.Fatal("下级额度总和不得超出父空间")
	}
	if got := do(http.MethodPut, "/spaces/4", map[string]any{"pool_id": id, "quota_bytes": 100, "enabled": true}); got["code"] != float64(403) {
		t.Fatal("不得向平级分配")
	}
	parent, err := storage.Mine(db, 1)
	if err != nil || parent.AllocatedBytes != 600 || parent.UsedBytes != 600 {
		t.Fatalf("父空间未划减下级额度: %+v %v", parent, err)
	}
	child, err := storage.Mine(db, 2)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if err := storage.Allocate(db, 1, filepath.Join(parent.Path, "own.bin"), 500, func() error { called = true; return nil }); !errors.Is(err, storage.ErrQuota) || called {
		t.Fatal("上级自身上传必须扣除已经划给下级的额度", err)
	}
	if err := os.WriteFile(filepath.Join(child.Path, "data.bin"), make([]byte, 400), 0600); err != nil {
		t.Fatal(err)
	}
	if v, err := storage.Mine(db, 1); err != nil || v.UsedBytes != 600 {
		t.Fatalf("下级实际文件不能重复扣减父空间: %+v %v", v, err)
	}
	if err := storage.Authorize(db, 1, child.Path); !errors.Is(err, storage.ErrDenied) {
		t.Fatal("上级分配者不能直接读取下级内容")
	}
	if err := db.Model(&model.UploadSpace{}).Where("user_id = 1").Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if err := storage.Authorize(db, 2, child.Path); !errors.Is(err, storage.ErrFrozen) {
		t.Fatal("子空间不得绕过父空间冻结", err)
	}
}
