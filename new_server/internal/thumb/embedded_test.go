package thumb

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ulikunitz/xz"
)

func TestEmbeddedFFmpegExtractsAndRuns(t *testing.T) {
	ffmpeg, ffprobe, err := extractEmbeddedFFmpegTo(t.TempDir())
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("内嵌 ffmpeg 尚未下载")
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, bin := range []string{ffmpeg, ffprobe} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		out, err := exec.CommandContext(ctx, bin, "-version").CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("%s 无法运行: %v (%s)", bin, err, out)
		}
	}
}

func TestEmbeddedFFmpegGeneratesThumbnail(t *testing.T) {
	ffmpeg, ffprobe, err := extractEmbeddedFFmpegTo(t.TempDir())
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("内嵌 ffmpeg 尚未下载")
	}
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{
		dir:          t.TempDir(),
		tmpDir:       t.TempDir(),
		tmpTTL:       time.Hour,
		ffmpeg:       ffmpeg,
		ffprobe:      ffprobe,
		defaultWidth: 256,
		sem:          make(chan struct{}, 1),
		inflight:     make(map[string]*call),
	}
	src := filepath.Join(t.TempDir(), "a.png")
	writeAlphaPNG(t, src)
	out, err := s.Get(context.Background(), src, 256)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, err := jpeg.DecodeConfig(f)
	if err != nil {
		t.Fatalf("输出不是 JPEG: %v", err)
	}
	if cfg.Width > 256 || cfg.Height > 256 {
		t.Fatalf("缩略图尺寸超出限制: %dx%d", cfg.Width, cfg.Height)
	}
}

func TestExtractTarXZArchiveTopLevel(t *testing.T) {
	var buf bytes.Buffer
	xw, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(xw)
	write := func(name, body string, mode int64) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	write("bin/ffmpeg", "ffmpeg", 0o755)
	write("bin/ffprobe", "ffprobe", 0o755)
	write("lib/libx.so.1.2.3", "lib", 0o644)
	if err := tw.WriteHeader(&tar.Header{Name: "lib/libx.so.1", Linkname: "libx.so.1.2.3", Typeflag: tar.TypeSymlink, Mode: 0o777}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := xw.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := extractTarXZArchive(dir, buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		filepath.Join(dir, "bin", "ffmpeg"),
		filepath.Join(dir, "bin", "ffprobe"),
		filepath.Join(dir, "lib", "libx.so.1"),
	} {
		if !fileOK(name) {
			t.Fatalf("缺少解压文件: %s", name)
		}
	}
}

func TestExtractLinuxFFmpegArchive(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("embedded", "linux_amd64", "ffmpeg.tar.xz"))
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("Linux 内嵌 ffmpeg 尚未下载")
	}
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := extractTarXZArchive(dir, data); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		filepath.Join(dir, "bin", "ffmpeg"),
		filepath.Join(dir, "bin", "ffprobe"),
		filepath.Join(dir, "lib", "libavcodec.so.63"),
		filepath.Join(dir, "lib", "libavutil.so.61"),
	} {
		if !fileOK(name) {
			t.Fatalf("缺少解压文件: %s", name)
		}
	}
}
