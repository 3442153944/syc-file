package sync

import (
	"os"
	"path/filepath"
	"testing"

	"syc-file/internal/model"
)

func TestMatchFolderByLocalPath(t *testing.T) {
	folders := []model.SyncFolder{
		{ID: 1, LocalPath: `F:\同步目录`, RemotePath: "/mnt/data/file_sync/a"},
		{ID: 2, LocalPath: "/mnt/data/同步映射目录", RemotePath: "/mnt/data/file_sync/sync"},
		{ID: 3, LocalPath: "", RemotePath: "/mnt/data/file_sync/b"},
	}

	f, rel, ok := matchFolderByLocalPath(folders, "/mnt/data/同步映射目录/文本协作测试/1.txt")
	if !ok || f.ID != 2 || rel != "文本协作测试/1.txt" {
		t.Fatalf("应命中文件夹 2、相对路径 文本协作测试/1.txt，得到 %v %q %v", f.ID, rel, ok)
	}

	for _, p := range []string{
		"/mnt/data/同步映射目录",               // 目录本身
		"/mnt/data/同步映射目录2/x.txt",        // 前缀相同但不是子目录
		"/mnt/data/file_sync/sync/x.txt", // trunk 路径不归这里管
		"/mnt/data/其它/x.txt",
	} {
		if _, _, ok := matchFolderByLocalPath(folders, p); ok {
			t.Errorf("%q 不应命中任何镜像目录", p)
		}
	}
}

func TestCopyFileAtomic(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "trunk", "子目录", "dst.txt")
	if err := os.WriteFile(src, []byte("11123456\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 目标已有旧内容、父目录不存在，都要处理
	if err := copyFileAtomic(src, dst); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFileAtomic(src, dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "v2" {
		t.Fatalf("目标内容应被覆盖为 v2，得到 %q", got)
	}
	ents, _ := os.ReadDir(filepath.Dir(dst))
	if len(ents) != 1 {
		t.Fatalf("不应遗留临时文件，目录里有 %d 项", len(ents))
	}
}
