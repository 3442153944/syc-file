package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
)

func speedtestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/v1/net/speedtest/upload", HandlerSpeedtestUpload())
	r.GET("/v1/net/speedtest/download", HandlerSpeedtestDownload())
	return r
}

func TestSpeedtestUpload(t *testing.T) {
	r := speedtestRouter()

	// 正常：默认 1MiB
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/net/speedtest/upload", bytes.NewReader(make([]byte, 1<<20)))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("默认样本应 200，got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	data := resp["data"].(map[string]any)
	if data["bytes"].(float64) != 1<<20 {
		t.Fatalf("bytes 不符: %v", data["bytes"])
	}
	if data["node"].(string) == "" {
		t.Fatal("node 不应为空")
	}

	// bytes=0：立即空响应（RTT 微样本）
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/net/speedtest/upload?bytes=0", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("bytes=0 应 200，got %d", w.Code)
	}

	// 超上限
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/net/speedtest/upload?bytes="+strconv.Itoa((8<<20)+1), nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("超上限应 400，got %d", w.Code)
	}

	// 非法参数
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/net/speedtest/upload?bytes=abc", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法参数应 400，got %d", w.Code)
	}
}

func TestSpeedtestDownload(t *testing.T) {
	r := speedtestRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/net/speedtest/download?bytes=65536", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("download 应 200，got %d", w.Code)
	}
	if w.Body.Len() != 65536 {
		t.Fatalf("download 字节数不符: %d", w.Body.Len())
	}
	if got := w.Header().Get("Content-Length"); got != "65536" {
		t.Fatalf("Content-Length 不符: %s", got)
	}

	// bytes=0
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/net/speedtest/download?bytes=0", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatalf("bytes=0 应 200 空体, got %d len=%d", w.Code, w.Body.Len())
	}

	// 超上限
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/net/speedtest/download?bytes=999999999", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("超上限应 400，got %d", w.Code)
	}
}
