// Development entry point. Unlike cmd/main.go, this compiles without cgo so it
// can repair the Rust archive before compiling the actual server.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syc-file/internal/devbuild"
	"syscall"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if e, ok := err.(*exec.ExitError); ok {
			os.Exit(e.ExitCode())
		}
		os.Exit(1)
	}
}

func run() error {
	build := flag.Bool("build", false, "build syc-file without starting it")
	prepare := flag.Bool("prepare", false, "only check and prepare the Rust core")
	flag.Parse()
	// Resolve from this source path so GoLand can use either repository root or
	// new_server as its working directory, including single-file run configs.
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return fmt.Errorf("cannot locate development source directory")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	// GoLand may have inherited PATH before winget installed MinGW. Refresh
	// only when needed, and preserve the IDE's existing tool paths.
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("gcc"); err != nil {
			path, err := exec.Command("powershell.exe", "-NoProfile", "-Command", "[Environment]::GetEnvironmentVariable('Path','Machine') + ';' + [Environment]::GetEnvironmentVariable('Path','User')").Output()
			if err != nil {
				return fmt.Errorf("refresh compiler PATH: %w", err)
			}
			if err := os.Setenv("PATH", os.Getenv("PATH")+";"+strings.TrimSpace(string(path))); err != nil {
				return err
			}
		}
	}
	if err := devbuild.EnsureCore(root); err != nil {
		return err
	}
	if err := devbuild.EnsureFFmpeg(root); err != nil {
		fmt.Fprintln(os.Stderr, "ffmpeg 内嵌资源准备失败，继续使用配置或系统 ffmpeg:", err)
	}
	if *prepare {
		return nil
	}
	goexe := filepath.Join(runtime.GOROOT(), "bin", "go")
	ext := ""
	if runtime.GOOS == "windows" {
		goexe += ".exe"
		ext = ".exe"
	}
	output := filepath.Join(root, "syc-file"+ext)
	if !*build {
		dir, err := os.MkdirTemp("", "filesync-dev-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		output = filepath.Join(dir, "syc-file"+ext)
	}
	cmd := exec.Command(goexe, "build", "-o", output, "./cmd")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	fmt.Println("==> building server")
	if err := cmd.Run(); err != nil {
		return err
	}
	if *build {
		fmt.Println("==> built:", output)
		return nil
	}
	server := exec.Command(output, flag.Args()...)
	server.Dir = root
	server.Stdin, server.Stdout, server.Stderr = os.Stdin, os.Stdout, os.Stderr
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := server.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case sig := <-signals:
				// Windows console Ctrl+C already reaches the child; Unix needs forwarding.
				if runtime.GOOS != "windows" {
					_ = server.Process.Signal(sig)
				}
			case <-done:
				return
			}
		}
	}()
	return server.Wait()
}
