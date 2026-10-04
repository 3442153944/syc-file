//go:build !windows

package volroot

import (
	"os"
	"path/filepath"
	"syscall"
)

func deviceOf(path string) (uint64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}

func RootOf(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return string(filepath.Separator)
	}
	dir := abs
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		dir = filepath.Dir(dir)
	}
	cur := dir
	dev, ok := uint64(0), false
	for {
		if dev, ok = deviceOf(cur); ok {
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return string(filepath.Separator)
		}
		cur = parent
	}
	root := cur
	for {
		parent := filepath.Dir(root)
		if parent == root {
			return root
		}
		pdev, ok := deviceOf(parent)
		if !ok || pdev != dev {
			return root
		}
		root = parent
	}
}
