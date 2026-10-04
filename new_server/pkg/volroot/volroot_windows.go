//go:build windows

package volroot

import "path/filepath"

func RootOf(p string) string {
	return filepath.VolumeName(p) + string(filepath.Separator)
}
