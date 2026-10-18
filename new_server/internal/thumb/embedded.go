package thumb

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ulikunitz/xz"
)

func extractEmbeddedFFmpeg() (string, string, error) {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		cacheRoot = os.TempDir()
	}
	return extractEmbeddedFFmpegTo(cacheRoot)
}

func extractEmbeddedFFmpegTo(cacheRoot string) (string, string, error) {
	name, data, err := platformEmbeddedArchive()
	if err != nil {
		return "", "", err
	}
	if len(data) == 0 {
		return "", "", fs.ErrNotExist
	}

	parent := filepath.Join(cacheRoot, "syc-file", "ffmpeg")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", "", err
	}

	sum := sha256.Sum256(data)
	dir := filepath.Join(parent, hex.EncodeToString(sum[:8]))
	ffmpeg, ffprobe := embeddedExecutables(dir)
	if fileOK(ffmpeg) && fileOK(ffprobe) {
		return ffmpeg, ffprobe, nil
	}

	tmp, err := os.MkdirTemp(parent, ".extract-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)

	switch {
	case strings.HasSuffix(name, ".zip"):
		err = extractZipArchive(tmp, data)
	case strings.HasSuffix(name, ".tar.xz"):
		err = extractTarXZArchive(tmp, data)
	default:
		err = fmt.Errorf("thumb: 未知内嵌压缩格式 %q", name)
	}
	if err != nil {
		return "", "", err
	}

	_ = os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		return "", "", err
	}
	ffmpeg, ffprobe = embeddedExecutables(dir)
	if !fileOK(ffmpeg) || !fileOK(ffprobe) {
		return "", "", errors.New("thumb: 内嵌 ffmpeg 解压不完整")
	}
	return ffmpeg, ffprobe, nil
}

func embeddedExecutables(dir string) (string, string) {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	return filepath.Join(dir, "bin", "ffmpeg"+suffix), filepath.Join(dir, "bin", "ffprobe"+suffix)
}

func extractZipArchive(dir string, data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	for _, entry := range zr.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		base := path.Base(strings.ReplaceAll(entry.Name, "\\", "/"))
		lower := strings.ToLower(base)
		if lower != "ffmpeg.exe" && lower != "ffprobe.exe" && !strings.HasSuffix(lower, ".dll") {
			continue
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(lower, ".exe") {
			mode = 0o755
		}
		if err := extractZipEntry(entry, filepath.Join(binDir, base), mode); err != nil {
			return err
		}
	}
	return nil
}

func extractZipEntry(entry *zip.File, dst string, mode os.FileMode) error {
	rc, err := entry.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

type tarLink struct {
	path   string
	target string
}

func extractTarXZArchive(dir string, data []byte) error {
	xr, err := xz.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	tr := tar.NewReader(xr)
	var links []tarLink
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.ReplaceAll(header.Name, "\\", "/")
		base := path.Base(name)
		lower := strings.ToLower(base)
		inLib := inArchiveDir(name, "lib") && strings.Contains(lower, ".so")

		switch {
		case header.Typeflag == tar.TypeReg && inArchiveDir(name, "bin") && (base == "ffmpeg" || base == "ffprobe"):
			if err := writeTarFile(tr, filepath.Join(dir, "bin", base), 0o755); err != nil {
				return err
			}
		case header.Typeflag == tar.TypeReg && inLib:
			if err := writeTarFile(tr, filepath.Join(dir, "lib", base), 0o644); err != nil {
				return err
			}
		case header.Typeflag == tar.TypeSymlink && inLib:
			links = append(links, tarLink{
				path:   filepath.Join(dir, "lib", base),
				target: path.Base(header.Linkname),
			})
		}
	}

	for _, link := range links {
		if err := os.Symlink(link.target, link.path); err == nil {
			continue
		}
		target := filepath.Join(filepath.Dir(link.path), link.target)
		if err := copyFile(target, link.path); err != nil {
			return err
		}
	}
	return nil
}

func inArchiveDir(name, dir string) bool {
	return strings.HasPrefix(name, dir+"/") || strings.Contains(name, "/"+dir+"/")
}

func writeTarFile(r io.Reader, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
