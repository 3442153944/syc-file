// Package devbuild prepares native dependencies before cgo compiles the server.
// It deliberately depends only on the Go standard library.
package devbuild

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// ArchiveTarget reads actual object headers, rather than trusting the .a suffix.
// Both GNU long names (/offset) and BSD extended names (#1/length) are supported.
func ArchiveTarget(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return "", err
	}
	magic := make([]byte, 8)
	if _, err = io.ReadFull(f, magic); err != nil {
		return "", err
	}
	if string(magic) != "!<arch>\n" {
		return "", fmt.Errorf("invalid static archive")
	}
	for {
		header := make([]byte, 60)
		if _, err = io.ReadFull(f, header); err != nil {
			return "", fmt.Errorf("no supported object: %w", err)
		}
		if string(header[58:]) != "`\n" {
			return "", fmt.Errorf("invalid archive member")
		}
		size, err := strconv.ParseInt(strings.TrimSpace(string(header[48:58])), 10, 64)
		if err != nil || size < 0 {
			return "", fmt.Errorf("invalid archive member size")
		}
		offset, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			return "", err
		}
		if size > stat.Size()-offset {
			return "", fmt.Errorf("truncated archive member")
		}
		name := strings.TrimSpace(string(header[:16]))
		payload := size
		if strings.HasPrefix(name, "#1/") {
			n, err := strconv.ParseInt(strings.TrimPrefix(name, "#1/"), 10, 64)
			if err != nil || n < 0 || n > size {
				return "", fmt.Errorf("invalid extended member name")
			}
			if _, err := io.CopyN(io.Discard, f, n); err != nil {
				return "", err
			}
			payload -= n
		}
		prefix := make([]byte, min(payload, 20))
		if _, err := io.ReadFull(f, prefix); err != nil {
			return "", err
		}
		// Skip archive symbol/name tables, whose contents are not object headers.
		if name != "/" && name != "//" && name != "/SYM64/" && !strings.HasPrefix(name, "__.SYMDEF") {
			if target := objectTarget(prefix); target != "" {
				return target, nil
			}
		}
		if _, err := io.CopyN(io.Discard, f, payload-int64(len(prefix))+size%2); err != nil {
			return "", err
		}
	}
}

func objectTarget(b []byte) string {
	if len(b) >= 20 && bytes.Equal(b[:4], []byte{0x7f, 'E', 'L', 'F'}) && b[5] == 1 {
		// ELF OSABI=0 is used by Linux Rust objects; Darwin uses Mach-O.
		switch binary.LittleEndian.Uint16(b[18:20]) {
		case 62:
			return "linux/amd64"
		case 183:
			return "linux/arm64"
		}
	}
	if len(b) >= 20 {
		switch binary.LittleEndian.Uint16(b[:2]) {
		case 0x8664:
			return "windows/amd64"
		case 0xaa64:
			return "windows/arm64"
		}
	}
	return ""
}

// EnsureCore asks Cargo to check shared sources before Go invokes cgo. Cargo's
// own incremental cache handles unchanged sources; architecture alone is not freshness.
func EnsureCore(root string) error {
	want := runtime.GOOS + "/" + runtime.GOARCH
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		return fmt.Errorf("unsupported development host: %s", want)
	}
	return ensureCore(root, want, func() error { return buildCore(root) })
}

func ensureCore(root, want string, build func() error) error {
	lib := filepath.Join(root, "..", "sys_core", "lib", "libsys_core.a")
	got, checkErr := ArchiveTarget(lib)
	fmt.Printf("==> checking shared sys_core sources: have %q (%v), need %s\n", got, checkErr, want)
	if err := build(); err != nil {
		return err
	}
	got, err := ArchiveTarget(lib)
	if err != nil || got != want {
		return fmt.Errorf("shared sys_core target %q, expected %s (error: %v)", got, want, err)
	}
	return nil
}

func buildCore(root string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		if runtime.GOARCH != "amd64" {
			return fmt.Errorf("Windows build script currently supports amd64 only")
		}
		cmd = exec.Command("powershell.exe", "-NoProfile", "-File", filepath.Join(root, "..", "sys_core", "build.ps1"))
	} else {
		cmd = exec.Command("bash", filepath.Join(root, "..", "sys_core", "build.sh"))
	}
	cmd.Dir = root
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build sync_core: %w", err)
	}
	return nil
}

// CoreLinkerFlags makes Rust-only changes participate in Go's final link cache key.
func CoreLinkerFlags(root string) (string, error) {
	f, err := os.Open(filepath.Join(root, "..", "sys_core", "lib", "libsys_core.a"))
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "-X syc-file/pkg/synccore.CoreArchiveSHA256=" + hex.EncodeToString(h.Sum(nil)), nil
}
