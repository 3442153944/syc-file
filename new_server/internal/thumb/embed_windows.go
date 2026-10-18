//go:build windows && amd64

package thumb

import "embed"

//go:embed embedded/windows_amd64
var embeddedWindows embed.FS

func platformEmbeddedArchive() (string, []byte, error) {
	const name = "ffmpeg.zip"
	data, err := embeddedWindows.ReadFile("embedded/windows_amd64/" + name)
	return name, data, err
}
