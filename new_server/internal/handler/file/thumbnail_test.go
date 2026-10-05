package file

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"syc-file/config"
	"syc-file/internal/thumb"
	"syc-file/pkg/logger"
)

// 缩略图接口的 HTTP 层行为：状态码、缓存头、路径安全边界。
func TestThumbnailEndpoint(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("没有 ffmpeg，跳过")
	}
	gin.SetMode(gin.TestMode)
	logger.Logger = zap.NewNop()

	root := t.TempDir()
	config.Conf.File.AllowedPaths = []string{root}
	defer func() { config.Conf.File.AllowedPaths = nil; thumb.Global = nil }()

	if _, err := thumb.Init(config.ThumbnailConfig{Dir: filepath.Join(root, "thumbs"), TempDir: filepath.Join(root, "tmpthumbs"), Workers: 2}, nil); err != nil {
		t.Fatal(err)
	}
	if thumb.Global == nil {
		t.Fatal("thumb 服务应已启动")
	}

	// 一张 800x600 的图 + 一个文本文件
	img := image.NewNRGBA(image.Rect(0, 0, 800, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 800; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x % 256), G: uint8(y % 256), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	picDir := filepath.Join(root, "pics")
	_ = os.MkdirAll(picDir, 0o755)
	_ = os.WriteFile(filepath.Join(picDir, "a.png"), buf.Bytes(), 0o644)
	_ = os.WriteFile(filepath.Join(picDir, "a.txt"), []byte("hi"), 0o644)

	newRouter := func(authed bool) *gin.Engine {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			if authed {
				c.Set("UserInfo", struct{}{})
			}
		})
		r.GET("/thumbnail", HandlerFuncThumbnail(nil, nil))
		return r
	}
	get := func(r *gin.Engine, q url.Values, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/thumbnail?"+q.Encode(), nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	q := func(path, name, w string) url.Values {
		v := url.Values{"path": {path}, "name": {name}}
		if w != "" {
			v.Set("w", w)
		}
		return v
	}

	r := newRouter(true)

	// 200：合法 JPEG、长边不超过请求宽度、带缓存头；path 是目录或完整路径都行
	for _, path := range []string{picDir, filepath.Join(picDir, "a.png")} {
		w := get(r, q(path, "a.png", "128"), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("path=%s 期望 200，实际 %d: %s", path, w.Code, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
			t.Errorf("Content-Type = %q", ct)
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
		if err != nil {
			t.Fatalf("返回内容不是 JPEG: %v", err)
		}
		if cfg.Width > 128 || cfg.Height > 128 || cfg.Width != 128 {
			t.Errorf("w=128 的缩略图尺寸应为长边 128，实际 %dx%d", cfg.Width, cfg.Height)
		}
		if w.Header().Get("Cache-Control") == "" || w.Header().Get("ETag") == "" {
			t.Error("缺少缓存头")
		}
	}

	// 304：带上 ETag 再请求
	first := get(r, q(picDir, "a.png", ""), nil)
	etag := first.Header().Get("ETag")
	if w := get(r, q(picDir, "a.png", ""), map[string]string{"If-None-Match": etag}); w.Code != http.StatusNotModified {
		t.Errorf("命中 ETag 应 304，实际 %d", w.Code)
	}
	// 不同宽度是不同的缩略图，ETag 不同
	if get(r, q(picDir, "a.png", "512"), nil).Header().Get("ETag") == etag {
		t.Error("不同宽度应有不同 ETag")
	}

	// 错误状态码：必须是真实 HTTP 状态码，不能是 200+JSON
	cases := []struct {
		name   string
		router *gin.Engine
		q      url.Values
		want   int
	}{
		{"未登录", newRouter(false), q(picDir, "a.png", ""), http.StatusUnauthorized},
		{"缺参数", r, url.Values{"path": {picDir}}, http.StatusBadRequest},
		{"非图片", r, q(picDir, "a.txt", ""), http.StatusUnsupportedMediaType},
		{"文件不存在", r, q(picDir, "nope.png", ""), http.StatusNotFound},
		{"路径不在允许范围", r, q("/etc", "passwd.png", ""), http.StatusForbidden},
		{"目录穿越", r, q(filepath.Join(picDir, "..", "..", ".."), "x.png", ""), http.StatusForbidden},
	}
	for _, c := range cases {
		if w := get(c.router, c.q, nil); w.Code != c.want {
			t.Errorf("%s: 期望 %d，实际 %d (%s)", c.name, c.want, w.Code, w.Body.String())
		}
	}
}
