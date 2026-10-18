package file

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"syc-file/config"
	"syc-file/internal/model"
)

func TestAccountCleanupRevokesOtherOwnersSharesOfDeletedSpace(t *testing.T) {
	e, root := managedEnv(t)
	oldTemp := config.Conf.Share.TempPath
	config.Conf.Share.TempPath = t.TempDir()
	t.Cleanup(func() { config.Conf.Share.TempPath = oldTemp })
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	for _, link := range []model.ShareLink{
		{UserID: 7, ShareCode: "own", OriginalPath: filepath.Join(root, "a"), TempName: "own.bin", FileName: "a", Status: 1, ExpireTime: time.Now().Add(time.Hour)},
		{UserID: 8, ShareCode: "delegated", OriginalPath: filepath.Join(root, "b"), TempName: "delegated.bin", FileName: "b", Status: 1, ExpireTime: time.Now().Add(time.Hour)},
		{UserID: 8, ShareCode: "keep", OriginalPath: filepath.Join(filepath.Dir(root), "8", "c"), TempName: "keep.bin", FileName: "c", Status: 1, ExpireTime: time.Now().Add(time.Hour)},
	} {
		if err := e.db.Create(&link).Error; err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(config.Conf.Share.TempPath, link.TempName), []byte("content"), 0600); err != nil {
			t.Fatal(err)
		}
		mr.Set(shareLinkRedisPrefix+link.ShareCode, "cached")
	}
	if err := CleanupAccountShares(e.db, rdb, 7); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"own", "delegated"} {
		if _, err := os.Stat(filepath.Join(config.Conf.Share.TempPath, code+".bin")); !os.IsNotExist(err) {
			t.Fatalf("分享副本未删除: %s %v", code, err)
		}
		if mr.Exists(shareLinkRedisPrefix + code) {
			t.Fatalf("缓存残留: %s", code)
		}
		var row model.ShareLink
		if err := e.db.Where("share_code = ?", code).First(&row).Error; err != nil || row.Status != shareStatusRevoked {
			t.Fatal("分享未吊销", code, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(config.Conf.Share.TempPath, "keep.bin")); err != nil || string(data) != "content" {
		t.Fatal("误删其他空间的分享")
	}
	if !mr.Exists(shareLinkRedisPrefix + "keep") {
		t.Fatal("误删其他空间的缓存")
	}
}
