package file

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"syc-file/pkg/upload_store"
	"testing"
	"time"
)

type delayedChunkBody struct {
	started chan struct{}
	release chan struct{}
	data    []byte
	read    bool
}

func (b *delayedChunkBody) Read(p []byte) (int, error) {
	if b.read {
		return 0, io.EOF
	}
	close(b.started)
	<-b.release
	b.read = true
	return copy(p, b.data), nil
}
func (b *delayedChunkBody) Close() error { return nil }

func TestQuickShareAbandonFencesDelayedAttempts(t *testing.T) {
	env := setupQuickShareUploadTest(t)
	content := []byte("cancel-old-attempt")
	leaves, root, hash := describeContent(t, content, len(content))
	code, resp := doJSON(t, env.router, http.MethodPost, "/upload/init", map[string]any{
		"name": "abandon.bin", "quick_share": true, "total_size": len(content), "chunk_size": len(content), "chunk_count": 1,
		"merkle_root": root, "file_hash": hash, "leaf_hashes": leaves,
	})
	if code != 200 {
		t.Fatalf("init: %v", resp)
	}
	id := resp["data"].(map[string]any)["upload_id"].(string)
	sess, err := upload_store.Global.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	old := &delayedChunkBody{started: make(chan struct{}), release: make(chan struct{}), data: content}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/upload/chunk?upload_id=%s&index=0&attempt_id=old", id), nil)
	req.Body = old
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { rec := httptest.NewRecorder(); env.router.ServeHTTP(rec, req); done <- rec }()
	select {
	case <-old.started:
	case <-time.After(time.Second):
		t.Fatal("old request did not start")
	}
	code, resp = doJSON(t, env.router, http.MethodPost, "/upload/abandon", map[string]any{"upload_id": id, "attempts": []any{map[string]any{"index": 0, "attempt_id": "old"}}})
	if code != 200 {
		t.Fatalf("abandon: %v", resp)
	}
	// 幂等重复放弃；新尝试不得被旧尝试的放弃状态影响。
	code, _ = doJSON(t, env.router, http.MethodPost, "/upload/abandon", map[string]any{"upload_id": id, "attempts": []any{map[string]any{"index": 0, "attempt_id": "old"}}})
	if code != 200 {
		t.Fatal("abandon must be idempotent")
	}
	fresh := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/upload/chunk?upload_id=%s&index=0&attempt_id=new", id), bytes.NewReader(content))
	rec := httptest.NewRecorder()
	env.router.ServeHTTP(rec, fresh)
	var result map[string]any
	json.Unmarshal(rec.Body.Bytes(), &result)
	if result["code"] != float64(200) {
		t.Fatalf("new attempt: %s", rec.Body.String())
	}
	close(old.release)
	select {
	case rec = <-done:
	case <-time.After(time.Second):
		t.Fatal("old request did not finish")
	}
	json.Unmarshal(rec.Body.Bytes(), &result)
	if result["code"] != float64(409) {
		t.Fatalf("old attempt must be rejected: %s", rec.Body.String())
	}
	// 模拟另一个旧请求读取 body 时，文件先完成并移动。
	late := &delayedChunkBody{started: make(chan struct{}), release: make(chan struct{}), data: content}
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/upload/chunk?upload_id=%s&index=0&attempt_id=late", id), nil)
	req.Body = late
	go func() { rec := httptest.NewRecorder(); env.router.ServeHTTP(rec, req); done <- rec }()
	<-late.started
	code, resp = doJSON(t, env.router, http.MethodPost, "/upload/complete", map[string]any{"upload_id": id})
	if code != 200 {
		t.Fatalf("complete: %v", resp)
	}
	close(late.release)
	select {
	case rec = <-done:
	case <-time.After(time.Second):
		t.Fatal("late request did not finish")
	}
	json.Unmarshal(rec.Body.Bytes(), &result)
	if result["code"] != float64(404) {
		t.Fatalf("completed session must reject late write: %s", rec.Body.String())
	}
	if _, err = os.Stat(sess.TempPath); !os.IsNotExist(err) {
		t.Fatalf("late write recreated temp file: %v", err)
	}
	actual, err := os.ReadFile(sess.TargetPath)
	if err != nil || !bytes.Equal(actual, content) {
		t.Fatalf("final content changed: %v", err)
	}
}
