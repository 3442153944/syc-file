package sync

import (
	"testing"

	"syc-file/internal/model"
)

func TestLiveDirs(t *testing.T) {
	rel := map[string]model.File{
		"表情包/a.jpg":   {},
		"表情包/子/b.jpg": {},
		"已删目录/c.jpg":  {IsDeleted: true},
		"根文件.txt":     {},
		"空目录":         {IsDirectory: true},
	}
	got := liveDirs(rel)

	for _, want := range []string{"表情包", "表情包/子"} {
		if !got[want] {
			t.Errorf("%q 里有未删除文件，应判为存在", want)
		}
	}
	for _, not := range []string{"已删目录", "空目录", "根文件.txt"} {
		if got[not] {
			t.Errorf("%q 不应出现在存在目录集合里", not)
		}
	}
}
