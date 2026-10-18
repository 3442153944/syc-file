package file

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"syc-file/internal/model"
	"syc-file/internal/storage"
)

func TestCreateDirectoryAndNavigateVirtualSpace(t *testing.T) {
	e, root := managedEnv(t)
	e.router.POST("/create-directory", HandlerFuncCreateDirectory(e.db))
	_, body := doJSON(t, e.router, http.MethodPost, "/create-directory", map[string]any{"path": storage.VirtualRoot, "name": "测试目录"})
	if body["code"] != float64(200) {
		t.Fatal(body)
	}
	if info, err := os.Stat(filepath.Join(root, "测试目录")); err != nil || !info.IsDir() {
		t.Fatal("目录未创建", err)
	}
	_, body = doJSON(t, e.router, http.MethodPost, "/traverse-directory", map[string]any{"path": storage.VirtualRoot + "/测试目录"})
	if body["code"] != float64(200) {
		t.Fatal("目录不能进入", body)
	}
	data := body["data"].(map[string]any)
	if data["parent_path"] != root {
		t.Fatal("返回上级目录错误", data)
	}
	_, body = doJSON(t, e.router, http.MethodPost, "/create-directory", map[string]any{"path": storage.VirtualRoot, "name": "测试目录"})
	if body["code"] != float64(409) {
		t.Fatal("同名目录应拒绝覆盖", body)
	}
	file := filepath.Join(root, "existing")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	_, body = doJSON(t, e.router, http.MethodPost, "/create-directory", map[string]any{"path": storage.VirtualRoot, "name": "existing"})
	if body["code"] != float64(409) {
		t.Fatal("同名文件应拒绝覆盖", body)
	}
	if content, err := os.ReadFile(file); err != nil || string(content) != "keep" {
		t.Fatal("同名文件被改动")
	}
}

func TestCreateDirectoryRejectsEscapeOtherSpaceAndFrozen(t *testing.T) {
	e, root := managedEnv(t)
	e.router.POST("/create-directory", HandlerFuncCreateDirectory(e.db))
	for _, name := range []string{"../escape", "a/b", "a\\b", ".children", ".UPLOADS", ".versions", "CON.txt", "LPT1", "name.", "name:", "a\x00b"} {
		_, body := doJSON(t, e.router, http.MethodPost, "/create-directory", map[string]any{"path": storage.VirtualRoot, "name": name})
		if body["code"] != float64(400) {
			t.Fatalf("非法名称未拒绝 %q: %v", name, body)
		}
	}
	_, body := doJSON(t, e.router, http.MethodPost, "/create-directory", map[string]any{"path": filepath.Join(filepath.Dir(root), "8"), "name": "escape"})
	if body["code"] != float64(403) {
		t.Fatal("可在其他用户空间创建目录", body)
	}
	_, body = doJSON(t, e.router, http.MethodPost, "/create-directory", map[string]any{"path": storage.VirtualRoot + "/.CHILDREN/8", "name": "escape"})
	if body["code"] != float64(403) {
		t.Fatal("内部目录大小写变体未拒绝", body)
	}
	_, body = doJSON(t, e.router, http.MethodPost, "/traverse-directory", map[string]any{"path": storage.VirtualRoot + "/../8"})
	if body["code"] != float64(403) {
		t.Fatal("虚拟路径穿越未拒绝", body)
	}
	if err := e.db.Model(&model.UploadSpace{}).Where("user_id = ?", 7).Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	_, body = doJSON(t, e.router, http.MethodPost, "/create-directory", map[string]any{"path": storage.VirtualRoot, "name": "expired"})
	if body["code"] != float64(403) {
		t.Fatal("冻结空间仍能创建目录", body)
	}
	if _, err := os.Stat(filepath.Join(root, "expired")); !os.IsNotExist(err) {
		t.Fatal("冻结后创建了实际目录")
	}
}
