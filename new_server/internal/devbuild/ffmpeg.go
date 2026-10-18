package devbuild

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const minimumFFmpegArchive = 50_000_000

func EnsureFFmpeg(root string) error {
	if runtime.GOARCH != "amd64" {
		return nil
	}
	var name, script string
	switch runtime.GOOS {
	case "windows":
		name = filepath.Join("internal", "thumb", "embedded", "windows_amd64", "ffmpeg.zip")
		script = filepath.Join("tools", "fetch-ffmpeg.ps1")
	case "linux":
		name = filepath.Join("internal", "thumb", "embedded", "linux_amd64", "ffmpeg.tar.xz")
		script = filepath.Join("tools", "fetch-ffmpeg.sh")
	default:
		return nil
	}

	archive := filepath.Join(root, name)
	if os.Getenv("FFMPEG_SOURCE") == "" && ffmpegArchiveReady(archive) {
		fmt.Printf("==> ffmpeg embed: %s verified\n", runtime.GOOS)
		return nil
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(root, script), "-Target", "windows")
	} else {
		cmd = exec.Command("bash", filepath.Join(root, script), "linux")
	}
	cmd.Dir = root
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("prepare ffmpeg embed: %w", err)
	}
	return nil
}

func ffmpegArchiveReady(archive string) bool {
	info, err := os.Stat(archive)
	if err != nil {
		return false
	}
	if _, err := os.Stat(archive + ".ready"); err == nil {
		return true
	}
	return info.Size() >= minimumFFmpegArchive
}
