package file

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/internal/storage"
	"syc-file/pkg/synccore"
)

func managedEnv(t *testing.T) (*qsTestEnv, string) {
	t.Helper()
	e := setupQuickShareUploadTest(t)
	if err := e.db.AutoMigrate(&model.StoragePool{}, &model.UploadSpace{}); err != nil {
		t.Fatal(err)
	}
	pool := model.StoragePool{Name: "测试存储", RootPath: t.TempDir(), Enabled: true}
	if err := e.db.Create(&pool).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint{7, 8} {
		u := model.User{ID: id, Username: fmt.Sprint("user", id), Password: "test", Status: 1, Level: model.LevelUser}
		if err := e.db.FirstOrCreate(&u, id).Error; err != nil {
			t.Fatal(err)
		}
		s := model.UploadSpace{UserID: id, PoolID: pool.ID, QuotaBytes: 20, Enabled: true}
		if err := e.db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(storage.Root(pool, s), 0700); err != nil {
			t.Fatal(err)
		}
	}
	before := config.Conf.File.Upload
	config.Conf.File.Upload.MaxFileSize = 1024 * 1024
	config.Conf.File.Upload.MaxFilenameLength = 255
	t.Cleanup(func() { config.Conf.File.Upload = before })
	e.router.GET("/upload/status", HandlerFuncUploadStatus(e.db, nil))
	e.router.POST("/traverse-directory", HandlerFuncTraverseDirectory(e.db, nil))
	return e, filepath.Join(pool.RootPath, "users", "7")
}

func initManaged(t *testing.T, e *qsTestEnv, path, name string, data []byte) map[string]any {
	t.Helper()
	hash, err := synccore.HashChunk(data)
	if err != nil {
		t.Fatal(err)
	}
	_, body := doJSON(t, e.router, http.MethodPost, "/upload/init", map[string]any{
		"path": path, "name": name, "total_size": len(data), "chunk_size": 4 << 20, "chunk_count": 1,
		"merkle_root": hex.EncodeToString(hash), "file_hash": hex.EncodeToString(hash), "leaf_hashes": []string{hex.EncodeToString(hash)},
	})
	return body
}

func TestManagedUploadAndConcurrentReservation(t *testing.T) {
	e, root := managedEnv(t)
	data := []byte("twelve-bytes")
	first := initManaged(t, e, storage.VirtualRoot, "a.txt", data)
	if first["code"] != float64(200) {
		t.Fatalf("init: %v", first)
	}
	if second := initManaged(t, e, root, "b.txt", data); second["code"] == float64(200) {
		t.Fatal("两个上传共24字节不得超过20字节配额")
	}
	id := first["data"].(map[string]any)["upload_id"].(string)
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/upload/chunk?upload_id="+id+"&index=0", bytes.NewReader(data)))
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"code":200`)) {
		t.Fatalf("chunk: %s", w.Body.String())
	}
	_, done := doJSON(t, e.router, http.MethodPost, "/upload/complete", map[string]any{"upload_id": id})
	if done["code"] != float64(200) {
		t.Fatalf("complete: %v", done)
	}
	actual, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil || !bytes.Equal(data, actual) {
		t.Fatal("上传内容不一致", err)
	}
	used, _, err := storage.Usage(root)
	if err != nil || used != int64(len(data)) {
		t.Fatalf("合并后不得重复占用容量 used=%d err=%v", used, err)
	}
}

func TestManagedSpaceCrossUserAndExpiry(t *testing.T) {
	e, root := managedEnv(t)
	other := filepath.Join(filepath.Dir(root), "8")
	if result := initManaged(t, e, other, "a.txt", []byte("test")); result["code"] != float64(403) {
		t.Fatalf("跨用户初始化未拒绝: %v", result)
	}
	_, listing := doJSON(t, e.router, http.MethodPost, "/traverse-directory", map[string]any{"path": other})
	if listing["code"] != float64(403) {
		t.Fatalf("跨用户目录未拒绝: %v", listing)
	}
	first := initManaged(t, e, root, "a.txt", []byte("test"))
	if first["code"] != float64(200) {
		t.Fatalf("init: %v", first)
	}
	id := first["data"].(map[string]any)["upload_id"].(string)
	e.userID = 8
	status := httptest.NewRecorder()
	e.router.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/upload/status?upload_id="+id, nil))
	if !bytes.Contains(status.Body.Bytes(), []byte(`"code":403`)) {
		t.Fatalf("跨用户会话状态泄露: %s", status.Body.String())
	}
	e.userID = 7
	if err := e.db.Model(&model.UploadSpace{}).Where("user_id = 7").Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/upload/chunk?upload_id="+id+"&index=0", bytes.NewBufferString("test")))
	if !bytes.Contains(w.Body.Bytes(), []byte(`"code":403`)) {
		t.Fatalf("上传中到期未冻结: %s", w.Body.String())
	}
	_, done := doJSON(t, e.router, http.MethodPost, "/upload/complete", map[string]any{"upload_id": id})
	if done["code"] != float64(403) {
		t.Fatalf("到期合并未拒绝: %v", done)
	}
}
