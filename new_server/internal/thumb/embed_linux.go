//go:build linux && amd64

package thumb

import "embed"

//go:embed embedded/linux_amd64
var embeddedLinux embed.FS

func platformEmbeddedArchive() (string, []byte, error) {
	const name = "ffmpeg.tar.xz"
	data, err := embeddedLinux.ReadFile("embedded/linux_amd64/" + name)
	return name, data, err
}
