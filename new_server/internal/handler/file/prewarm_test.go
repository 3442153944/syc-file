package file

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"syc-file/config"
	"syc-file/internal/thumb"
	"syc-file/pkg/logger"
)

func countFilesUnder(dir string) int {
	n := 0
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// 浏览目录时：前端拿到哪一页，就只为那一页里的图片生成临时缩略图；
// 目录、非图片不处理；产物进临时目录，不进持久目录。
func TestTraversePrewarmsOnlyListedImagesIntoTempDir(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("没有 ffmpeg，跳过")
	}
	logger.Logger = zap.NewNop()
	root := t.TempDir()
	permDir, tmpDir := filepath.Join(root, "thumbs"), filepath.Join(root, "tmpthumbs")
	config.Conf.File.AllowedPaths = []string{root}
	defer func() { config.Conf.File.AllowedPaths = nil; thumb.Global = nil }()
	if _, err := thumb.Init(config.ThumbnailConfig{Dir: permDir, TempDir: tmpDir, Workers: 2}, nil); err != nil || thumb.Global == nil {
		t.Fatalf("thumb 初始化失败: %v", err)
	}

	pics := filepath.Join(root, "pics")
	_ = os.MkdirAll(filepath.Join(pics, "sub"), 0o755)
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 300, 200)))
	for _, n := range []string{"a.png", "b.png"} {
		_ = os.WriteFile(filepath.Join(pics, n), buf.Bytes(), 0o644)
	}
	_ = os.WriteFile(filepath.Join(pics, "c.txt"), []byte("hi"), 0o644)
	// 第三张图不在「这一页」里：模拟翻页时没被请求到的文件
	_ = os.WriteFile(filepath.Join(pics, "not_requested.png"), buf.Bytes(), 0o644)

	page := []fileItem{
		{Name: "sub", Path: filepath.Join(pics, "sub"), IsDir: true},
		{Name: "a.png", Path: filepath.Join(pics, "a.png")},
		{Name: "b.png", Path: filepath.Join(pics, "b.png")},
		{Name: "c.txt", Path: filepath.Join(pics, "c.txt")},
	}
	prewarmThumbnails(page)

	deadline := time.Now().Add(10 * time.Second)
	for countFilesUnder(tmpDir) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("应为这一页的 2 张图片生成临时缩略图，实际 %d", countFilesUnder(tmpDir))
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond) // 留出时间让多余的（不该有的）产物暴露出来
	if n := countFilesUnder(tmpDir); n != 2 {
		t.Errorf("没被请求的图片不该生成，临时缩略图应恰好 2 个，实际 %d", n)
	}
	if n := countFilesUnder(permDir); n != 0 {
		t.Errorf("浏览触发的缩略图不该进持久目录，实际 %d", n)
	}
}
