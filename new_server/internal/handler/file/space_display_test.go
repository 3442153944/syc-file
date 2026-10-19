package file

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"syc-file/internal/model"
	"syc-file/internal/storage"
	"syc-file/pkg/token"
)

func TestVirtualDiskIncludesFormattedCapacityAndActualPercent(t *testing.T) {
	e, root := managedEnv(t)
	if err := os.WriteFile(filepath.Join(root, "data"), []byte("123456"), 0600); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("UserInfo", &token.Claims{UserID: 7}) })
	r.POST("/disks", HandlerFuncAvailableDisks(e.db, nil))
	_, body := doJSON(t, r, http.MethodPost, "/disks", nil)
	if body["code"] != float64(200) {
		t.Fatal(body)
	}
	d := body["data"].(map[string]any)["all_disks"].([]any)[0].(map[string]any)
	if d["total"] != float64(20) || d["free"] != float64(14) || d["used_percent"] != float64(30) || d["total_gb"] != "20 B" || d["free_gb"] != "14 B" {
		t.Fatalf("容量字段不完整或百分比错误: %v", d)
	}
	if info, err := os.Stat(filepath.Join(root, storage.DefaultFolder)); err != nil || !info.IsDir() {
		t.Fatalf("默认文件夹未初始化: %v", err)
	}
}

func TestDeletedAccountCannotDownloadCachedQuickShare(t *testing.T) {
	e := setupQuickShareUploadTest(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	code := "deleted-account-cache"
	quickShareMemStore.Store(code, []byte("secret"))
	t.Cleanup(func() { quickShareMemStore.Delete(code) })
	meta, _ := json.Marshal(shareLinkMeta{UserID: 7, TempName: "memory.bin", FileName: "a", IsQuickShare: true, StorageKind: "memory"})
	mr.Set(shareLinkRedisPrefix+code, string(meta))
	mr.SetTTL(shareLinkRedisPrefix+code, time.Hour)
	r := gin.New()
	r.GET("/share/:code", HandlerFuncShareDownload(e.db, rdb))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/share/"+code, nil))
	if w.Code != http.StatusOK || w.Body.String() != "secret" {
		t.Fatal("测试分享应先可用", w.Code, w.Body.String())
	}
	if err := e.db.Model(&model.User{}).Where("id = ?", 7).Update("status", model.UserDeleting).Error; err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/share/"+code, nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("旧分享缓存绕过账号删除: %d %s", w.Code, w.Body.String())
	}
}
