package file

import (
	"net/http"
	"testing"

	"syc-file/internal/storage"
)

func TestTraverseDirectoryEmptyItemsIsArray(t *testing.T) {
	e, _ := managedEnv(t)
	for _, req := range []map[string]any{
		{"path": storage.VirtualRoot},
		{"path": storage.VirtualRoot, "page": 1, "page_size": 100},
	} {
		_, body := doJSON(t, e.router, http.MethodPost, "/traverse-directory", req)
		if body["code"] != float64(200) {
			t.Fatalf("遍历空目录失败: %v", body)
		}
		data := body["data"].(map[string]any)
		items, ok := data["items"].([]any)
		if !ok || len(items) != 0 {
			t.Fatalf("空目录 items 应为 []，实际为 %v", data["items"])
		}
	}
}
