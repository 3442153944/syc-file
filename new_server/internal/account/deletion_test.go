package account

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"syc-file/internal/model"
	"syc-file/internal/storage"
	"syc-file/pkg/logger"
)

func deletionDB(t *testing.T) (*gorm.DB, model.StoragePool, model.UploadSpace, model.UploadSpace) {
	t.Helper()
	logger.Logger = zap.NewNop()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "account.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { _ = conn.Close() })
	err = db.AutoMigrate(&model.User{}, &model.UploadSpace{}, &model.StoragePool{}, &model.UserRole{}, &model.UserRoute{}, &model.Device{}, &model.SyncTask{}, &model.SyncConflict{}, &model.SyncFolder{}, &model.File{}, &model.FileVersion{}, &model.StorageConfig{}, &model.UploadHistory{}, &model.DownloadHistory{}, &model.ShareRecord{}, &model.ShareLink{})
	if err != nil {
		t.Fatal(err)
	}
	by := uint(2)
	for _, u := range []model.User{
		{ID: 1, Username: "super", Password: "test", Level: model.LevelSuper, Status: 1},
		{ID: 2, Username: "admin", Password: "test", Level: model.LevelAdmin, Status: 1},
		{ID: 3, Username: "user", Password: "test", Level: model.LevelUser, Status: 1, CreatedBy: &by},
		{ID: 4, Username: "other_admin", Password: "test", Level: model.LevelAdmin, Status: 1},
	} {
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}
	p := model.StoragePool{Name: "测试盘", RootPath: t.TempDir(), Enabled: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	parent := model.UploadSpace{UserID: 2, PoolID: p.ID, QuotaBytes: 100, Enabled: true}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}
	child := model.UploadSpace{UserID: 3, PoolID: p.ID, ParentID: &parent.ID, Directory: filepath.Join("users", "2", ".children", "3"), QuotaBytes: 60, Enabled: true}
	if err := db.Create(&child).Error; err != nil {
		t.Fatal(err)
	}
	for _, s := range []model.UploadSpace{parent, child} {
		root := storage.Root(p, s)
		if err := os.MkdirAll(filepath.Join(root, ".uploads"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".uploads", "part"), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return db, p, parent, child
}

func TestDeleteCleansOnlyOwnedSpaceAndReleasesParentQuota(t *testing.T) {
	db, p, parent, child := deletionDB(t)
	size := int64(1)
	f := model.File{UserID: 3, FileName: "a", FilePath: filepath.Join(storage.Root(p, child), "a"), FileSize: &size}
	if err := db.Create(&f).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.FileVersion{FileID: f.ID, Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.UserRoute{UserID: 3, RouteCode: "file.list"}).Error; err != nil {
		t.Fatal(err)
	}
	result, err := Delete(db, 2, 3, nil)
	if err != nil || result.CleanupPending {
		t.Fatalf("删除失败: %+v %v", result, err)
	}
	if _, err := os.Stat(storage.Root(p, child)); !os.IsNotExist(err) {
		t.Fatalf("子目录仍存在: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storage.Root(p, parent), ".uploads", "part")); err != nil {
		t.Fatalf("误删父空间文件: %v", err)
	}
	if n, err := storage.Allocated(db, parent.ID); err != nil || n != 0 {
		t.Fatalf("额度未归还: %d %v", n, err)
	}
	for _, row := range []any{&model.User{}, &model.UploadSpace{}, &model.File{}, &model.FileVersion{}, &model.UserRoute{}} {
		var n int64
		q := db.Model(row)
		switch row.(type) {
		case *model.User:
			q = q.Where("id = ?", 3)
		case *model.FileVersion:
			q = q.Where("file_id = ?", f.ID)
		default:
			q = q.Where("user_id = ?", 3)
		}
		if err := q.Count(&n).Error; err != nil || n != 0 {
			t.Fatalf("关联数据残留 %T: %d %v", row, n, err)
		}
	}
}

func TestDeleteFailureRetainsDurableStateAndQuotaUntilRetry(t *testing.T) {
	db, p, parent, child := deletionDB(t)
	result, err := Delete(db, 2, 3, func(uint) error { return errors.New("模拟文件占用") })
	if err != nil || !result.CleanupPending {
		t.Fatalf("未返回待清理状态: %+v %v", result, err)
	}
	var u model.User
	if err := db.First(&u, 3).Error; err != nil || u.Status != model.UserDeleting {
		t.Fatalf("删除状态未持久化: %+v %v", u, err)
	}
	if _, err := storage.User(db, 3); err == nil {
		t.Fatal("删除中账号仍可访问")
	}
	if n, _ := storage.Allocated(db, parent.ID); n != 60 {
		t.Fatalf("文件未清理就归还额度: %d", n)
	}
	if err := Purge(db, 3, nil); err != nil {
		t.Fatal(err)
	}
	if err := Purge(db, 3, nil); err != nil {
		t.Fatalf("重试不幂等: %v", err)
	}
	if _, err := os.Stat(storage.Root(p, child)); !os.IsNotExist(err) {
		t.Fatal("重试后文件未清理")
	}
}

func TestDeleteRejectsOtherAdminsSelfAndParentsWithChildren(t *testing.T) {
	db, p, _, child := deletionDB(t)
	for _, ids := range [][2]uint{{4, 3}, {2, 2}, {2, 4}, {2, 1}, {1, 2}} {
		if _, err := Delete(db, ids[0], ids[1], nil); err == nil {
			t.Fatalf("越权或连带删除未拒绝: %v", ids)
		}
	}
	if _, err := os.Stat(storage.Root(p, child)); err != nil {
		t.Fatal("拒绝操作后文件丢失")
	}
	if _, err := storage.User(db, 2); err != nil {
		t.Fatal("删除被拒绝后账号应保持可用")
	}
}

func TestPurgeRefusesPoolRootAndDoesNotDeleteOtherFiles(t *testing.T) {
	db, p, _, child := deletionDB(t)
	outside := filepath.Join(p.RootPath, "important")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&child).Update("directory", ".").Error; err != nil {
		t.Fatal(err)
	}
	result, err := Delete(db, 1, 3, nil)
	if err != nil || !result.CleanupPending {
		t.Fatalf("危险目录未被拒绝: %+v %v", result, err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "keep" {
		t.Fatal("误删存储池文件")
	}
}
