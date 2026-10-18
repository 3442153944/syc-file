package storage

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"syc-file/config"
	"syc-file/internal/model"
)

func setup(t *testing.T) (*gorm.DB, model.StoragePool) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "space.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.AutoMigrate(&model.User{}, &model.StoragePool{}, &model.UploadSpace{}); err != nil {
		t.Fatal(err)
	}
	p := model.StoragePool{Name: "测试磁盘", RootPath: t.TempDir(), Enabled: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	for _, u := range []model.User{{ID: 1, Username: "user", Password: "test", Level: model.LevelUser, Status: 1}, {ID: 2, Username: "admin", Password: "test", Level: model.LevelAdmin, Status: 1}, {ID: 3, Username: "super", Password: "test", Level: model.LevelSuper, Status: 1}} {
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
		s := model.UploadSpace{UserID: u.ID, PoolID: p.ID, QuotaBytes: 10, Enabled: true}
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(Root(p, s), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return db, p
}

func TestOwnershipAndCurrentLevel(t *testing.T) {
	db, p := setup(t)
	own := filepath.Join(p.RootPath, "users", "1", "a.txt")
	other := filepath.Join(p.RootPath, "users", "2", "a.txt")
	if err := Authorize(db, 1, own); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		uid  uint
		path string
	}{{1, other}, {2, own}, {1, filepath.Join(p.RootPath, "users", "10", "a")}, {1, filepath.Join(p.RootPath, "users", "1", ".uploads", "secret.part")}} {
		if err := Authorize(db, tc.uid, tc.path); !errors.Is(err, ErrDenied) {
			t.Fatalf("越权路径未被拒绝: uid=%d err=%v", tc.uid, err)
		}
	}
	if err := Authorize(db, 3, own); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.User{}).Where("id = 3").Update("level", model.LevelAdmin).Error; err != nil {
		t.Fatal(err)
	}
	if err := Authorize(db, 3, own); !errors.Is(err, ErrDenied) {
		t.Fatal("降级后的旧令牌不能继续跨用户访问")
	}
}

func TestDefaultFolderPreservesFilesAndDoesNotRecreateMissingRoot(t *testing.T) {
	db, p := setup(t)
	v, err := Mine(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(p.RootPath, "users", "1", DefaultFolder)
	if v.DefaultPath != want {
		t.Fatalf("默认路径错误: %s", v.DefaultPath)
	}
	file := filepath.Join(want, "keep.txt")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Mine(db, 1); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "keep" {
		t.Fatal("初始化覆盖了现有文件")
	}
	if err := os.RemoveAll(v.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := Mine(db, 1); !os.IsNotExist(err) {
		t.Fatalf("根目录丢失应报错而非自动重建: %v", err)
	}
}

func TestExpiredSpacePreservesFiles(t *testing.T) {
	db, p := setup(t)
	path := filepath.Join(p.RootPath, "users", "1", "saved.txt")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	expired := time.Now().Add(-time.Second)
	if err := db.Model(&model.UploadSpace{}).Where("user_id = 1").Update("expires_at", expired).Error; err != nil {
		t.Fatal(err)
	}
	if err := Authorize(db, 1, path); !errors.Is(err, ErrFrozen) {
		t.Fatalf("到期访问未拒绝: %v", err)
	}
	if err := SharedPathActive(db, path); !errors.Is(err, ErrFrozen) {
		t.Fatal("公开分享也必须遵守源空间有效期", err)
	}
	called := false
	if err := Allocate(db, 1, path, 4, func() error { called = true; return nil }); !errors.Is(err, ErrFrozen) || called {
		t.Fatalf("到期仍执行写入: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "keep" {
		t.Fatal("到期不得删除或修改文件")
	}
}

func TestQuotaCountsStagedUploadsAndReplacement(t *testing.T) {
	db, p := setup(t)
	root := filepath.Join(p.RootPath, "users", "1")
	stage := filepath.Join(root, ".uploads", "session.part")
	write := func(path string, n int) func() error {
		return func() error { return os.WriteFile(path, make([]byte, n), 0600) }
	}
	if err := Allocate(db, 1, stage, 7, write(stage, 7)); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "b.bin")
	if err := Allocate(db, 1, target, 4, write(target, 4)); !errors.Is(err, ErrQuota) {
		t.Fatalf("进行中的上传未计入配额: %v", err)
	}
	if err := Allocate(db, 1, stage, 7, write(stage, 7)); err != nil {
		t.Fatalf("相同暂存文件不得重复占用容量: %v", err)
	}
	if err := os.Rename(stage, filepath.Join(root, "a.bin")); err != nil {
		t.Fatal(err)
	}
	if err := Allocate(db, 1, target, 3, write(target, 3)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := Allocate(db, 1, target, 3, write(target, 3)); err != nil {
		t.Fatal("删除后应恢复可用容量", err)
	}
}

func TestConcurrentAllocationCannotOversubscribe(t *testing.T) {
	db, p := setup(t)
	var success atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := filepath.Join(p.RootPath, "users", "1", string(rune('a'+i)))
			if Allocate(db, 1, path, 6, func() error { return os.WriteFile(path, make([]byte, 6), 0600) }) == nil {
				success.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatalf("容量 10 只能成功分配一个大小 6 的文件，实际 %d", success.Load())
	}
	used, _, err := Usage(filepath.Join(p.RootPath, "users", "1"))
	if err != nil || used != 6 {
		t.Fatalf("used=%d err=%v", used, err)
	}
}

func TestTraversalAndSymlink(t *testing.T) {
	root := t.TempDir()
	if Within(root, root+"-other") || Within(root, filepath.Join(root, "..", "outside")) || Within(root, filepath.Join(root, "a:stream")) {
		t.Fatal("路径边界判断错误")
	}
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("当前系统不允许创建测试符号链接")
	}
	if Within(root, filepath.Join(link, "secret")) {
		t.Fatal("符号链接越界未被拒绝")
	}
}

func TestUnassignedIsDeniedEvenWhenGloballyAllowed(t *testing.T) {
	db, p := setup(t)
	before := config.Conf.File.AllowedPaths
	config.Conf.File.AllowedPaths = []string{p.RootPath}
	t.Cleanup(func() { config.Conf.File.AllowedPaths = before })
	if err := db.Where("user_id = 1").Delete(&model.UploadSpace{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := Authorize(db, 1, p.RootPath); !errors.Is(err, ErrDenied) {
		t.Fatal("全局白名单不能替代用户空间授权")
	}
}
