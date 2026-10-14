package file

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"syc-file/internal/model"
	"sync/atomic"
	"testing"
)

func TestQuickShareCompleteResponseLostCanRetryAndDownload(t *testing.T) {
	env := setupQuickShareUploadTest(t)
	content := []byte("complete-response-lost-content")
	leaves, root, hash := describeContent(t, content, len(content))
	code, response := doJSON(t, env.router, http.MethodPost, "/upload/init", map[string]any{
		"name": "retry.bin", "quick_share": true, "total_size": len(content),
		"chunk_size": len(content), "chunk_count": 1, "merkle_root": root,
		"file_hash": hash, "leaf_hashes": leaves,
	})
	if code != 200 {
		t.Fatalf("init failed: %v", response)
	}
	id := response["data"].(map[string]any)["upload_id"].(string)
	chunk := httptest.NewRecorder()
	env.router.ServeHTTP(chunk, httptest.NewRequest(http.MethodPost, fmt.Sprintf("/upload/chunk?upload_id=%s&index=0", id), bytes.NewReader(content)))
	var chunkResult map[string]any
	if json.Unmarshal(chunk.Body.Bytes(), &chunkResult) != nil || chunkResult["code"] != float64(200) {
		t.Fatalf("chunk failed: %s", chunk.Body)
	}
	var dropped atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upload/complete" && !dropped.Swap(true) {
			recorder := httptest.NewRecorder()
			env.router.ServeHTTP(recorder, r)
			// The backend has committed. Lose the TCP connection before the response reaches the client.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack failed: %v", err)
				return
			}
			conn.Close()
			return
		}
		env.router.ServeHTTP(w, r)
	}))
	defer server.Close()
	body, _ := json.Marshal(map[string]any{"upload_id": id})
	if first, err := http.Post(server.URL+"/upload/complete", "application/json", bytes.NewReader(body)); err == nil {
		first.Body.Close()
		t.Fatal("expected lost response")
	}
	retry, err := http.Post(server.URL+"/upload/complete", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer retry.Body.Close()
	var result map[string]any
	if json.NewDecoder(retry.Body).Decode(&result) != nil || result["code"] != float64(200) {
		t.Fatalf("retry failed: %v", result)
	}
	shareCode := result["data"].(map[string]any)["share_code"].(string)
	download, err := http.Get(server.URL + "/share/" + shareCode)
	if err != nil {
		t.Fatal(err)
	}
	defer download.Body.Close()
	downloaded, _ := io.ReadAll(download.Body)
	if download.StatusCode != 200 || !bytes.Equal(downloaded, content) {
		t.Fatalf("share download failed: %d", download.StatusCode)
	}
	var count int64
	env.db.Model(&model.ShareLink{}).Count(&count)
	if count != 1 {
		t.Fatalf("lost response created duplicate links: %d", count)
	}
}
