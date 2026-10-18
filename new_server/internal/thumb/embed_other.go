//go:build !(windows && amd64) && !(linux && amd64)

package thumb

import "io/fs"

func platformEmbeddedArchive() (string, []byte, error) {
	return "", nil, fs.ErrNotExist
}
