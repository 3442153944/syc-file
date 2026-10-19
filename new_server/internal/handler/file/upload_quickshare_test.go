package file

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/pkg/logger"
	"syc-file/pkg/synccore"
	"syc-file/pkg/token"
	"syc-file/pkg/upload_store"

	"github.com/glebarez/sqlite"
)

// 快传分片上传端到端（init→chunk→complete）：会话走 miniredis，DB 走 SQLite，
// 断言分享记录创建、无 file 记录、文件落在 BasePath/<userID>/ 下。
// 形状与旧单发快传接口（quick_share.go）对拍：同一份响应字段、同一条分享记录。

type qsTestEnv struct {
	db       *gorm.DB
	router   *gin.Engine
	basePath string
	userID   uint
}

func setupQuickShareUploadTest(t *testing.T) *qsTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger.Logger = zap.NewNop()

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	upload_store.Init(rdb)

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开 SQLite 失败: %v", err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.AutoMigrate(&model.ShareLink{}, &model.File{}, &model.UploadHistory{}, &model.User{}); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}

	basePath := t.TempDir()
	restore := config.Conf.QuickShare
	config.Conf.QuickShare = config.QuickShareConfig{
		BasePath:             basePath,
		MaxCapacityBytes:     0, // 走 10GB 兜底
		DefaultExpireMinutes: 60,
	}
	t.Cleanup(func() { config.Conf.QuickShare = restore })

	// 分片临时文件目录（tempPathFor = <volroot>/<BasePath>/<TempPath>）
	restoreStorage := config.Conf.File.Storage
	config.Conf.File.Storage.BasePath = t.TempDir()
	config.Conf.File.Storage.TempPath = "temp"
	t.Cleanup(func() { config.Conf.File.Storage = restoreStorage })

	env := &qsTestEnv{db: db, basePath: basePath, userID: 7}
	if err := db.Create(&model.User{ID: 7, Username: "user7", Password: "test", Status: 1, Level: model.LevelUser}).Error; err != nil {
		t.Fatal(err)
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("UserInfo", &token.Claims{UserID: int64(env.userID)})
		c.Next()
	})
	r.POST("/upload/init", HandlerFuncUploadInit(db, rdb, nil))
	r.POST("/upload/chunk", HandlerFuncUploadChunk(db, rdb))
	r.POST("/upload/abandon", HandlerFuncUploadAbandon(rdb))
	r.POST("/upload/complete", HandlerFuncUploadComplete(db, rdb, nil))
	r.GET("/share/:code", HandlerFuncShareDownload(db, rdb))
	env.router = r
	return env
}

// 分片描述信息：叶子/树根/整文件哈希全走 sync_core，与服务端校验同一套算法。
func describeContent(t *testing.T, content []byte, chunkSize int) (leavesHex []string, rootHex, fileHashHex string) {
	t.Helper()
	leavesBytes := make([]byte, 0)
	for off := 0; off < len(content); off += chunkSize {
		end := min(off+chunkSize, len(content))
		leaf, err := synccore.HashChunk(content[off:end])
		if err != nil {
			t.Fatalf("HashChunk: %v", err)
		}
		leavesBytes = append(leavesBytes, leaf...)
		leavesHex = append(leavesHex, hex.EncodeToString(leaf))
	}
	root, err := synccore.MerkleRoot(leavesBytes)
	if err != nil {
		t.Fatalf("MerkleRoot: %v", err)
	}
	// 整文件哈希 == Finalize 对同一内容重算的结果（text_test.go 已证明两者对拍一致）
	src := filepath.Join(t.TempDir(), "src.bin")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}
	fileHash, _, err := synccore.Finalize(src, uint64(chunkSize), uint64(len(content)), nil, nil)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return leavesHex, hex.EncodeToString(root), hex.EncodeToString(fileHash)
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是 JSON: %v (%s)", err, rec.Body.String())
	}
	return int(out["code"].(float64)), out
}

func TestQuickShareChunkedUploadCreatesShareLink(t *testing.T) {
	env := setupQuickShareUploadTest(t)
	content := bytes.Repeat([]byte("quick-share-by-chunk!"), 40000) // 840000B
	chunkSize := 400000                                             // 3 片：400000/400000/40000
	leavesHex, rootHex, fileHashHex := describeContent(t, content, chunkSize)

	// ── init：quick_share=true，path 可为空（服务端自算目标）──
	code, resp := doJSON(t, env.router, http.MethodPost, "/upload/init", map[string]any{
		"name":        "截图.png",
		"quick_share": true,
		"total_size":  len(content),
		"chunk_size":  chunkSize,
		"chunk_count": 3,
		"merkle_root": rootHex,
		"file_hash":   fileHashHex,
		"leaf_hashes": leavesHex,
	})
	if code != 200 {
		t.Fatalf("init code=%d msg=%v", code, resp["message"])
	}
	data := resp["data"].(map[string]any)
	if data["instant"].(bool) {
		t.Fatal("快传不应走 instant")
	}
	uploadID := data["upload_id"].(string)
	missing := data["missing"].([]any)
	if len(missing) != 3 {
		t.Fatalf("missing 应为全部 3 片, got %v", missing)
	}

	// ── chunk：乱序传两片，再补末片 ──
	for _, idx := range []int{2, 0, 1} {
		off := idx * chunkSize
		end := min(off+chunkSize, len(content))
		req := httptest.NewRequest(http.MethodPost,
			fmt.Sprintf("/upload/chunk?upload_id=%s&index=%d", uploadID, idx), bytes.NewReader(content[off:end]))
		rec := httptest.NewRecorder()
		env.router.ServeHTTP(rec, req)
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || int(out["code"].(float64)) != 200 {
			t.Fatalf("chunk %d 失败: %s", idx, rec.Body.String())
		}
	}

	// ── complete：响应形状与旧单发快传接口一致 ──
	code, resp = doJSON(t, env.router, http.MethodPost, "/upload/complete", map[string]any{
		"upload_id": uploadID,
	})
	if code != 200 {
		t.Fatalf("complete code=%d msg=%v", code, resp["message"])
	}
	data = resp["data"].(map[string]any)
	shareCode, _ := data["share_code"].(string)
	if shareCode == "" || data["url_path"] != "/v1/file/share-link/download/"+shareCode {
		t.Fatalf("响应缺少分享字段: %v", data)
	}
	if data["file_name"] != "截图.png" || int64(data["file_size"].(float64)) != int64(len(content)) {
		t.Fatalf("响应文件名/大小不对: %v", data)
	}
	if _, ok := data["expire_time"].(string); !ok {
		t.Fatalf("响应缺少 expire_time: %v", data)
	}

	// ── 断言分享记录：IsQuickShare=true、OriginalPath 在 BasePath/<userID>/ 下 ──
	var link model.ShareLink
	if err := env.db.Where("share_code = ?", shareCode).First(&link).Error; err != nil {
		t.Fatalf("分享记录未创建: %v", err)
	}
	if !link.IsQuickShare || link.StorageKind != "disk" {
		t.Fatalf("分享记录标记不对: %+v", link)
	}
	wantPrefix := filepath.Join(env.basePath, fmt.Sprintf("%d", env.userID)) + string(filepath.Separator)
	if !strings.HasPrefix(link.OriginalPath, wantPrefix) {
		t.Fatalf("OriginalPath 不在快传目录下: %s (期望前缀 %s)", link.OriginalPath, wantPrefix)
	}
	if link.TempName != filepath.Base(link.OriginalPath) || !strings.HasSuffix(link.TempName, ".png") {
		t.Fatalf("TempName 与目标文件名不符: %+v", link)
	}
	if link.FileSize != int64(len(content)) {
		t.Fatalf("FileSize=%d, 期望 %d", link.FileSize, len(content))
	}
	disk, err := os.ReadFile(link.OriginalPath)
	if err != nil || !bytes.Equal(disk, content) {
		t.Fatalf("落盘内容与原文件不一致: err=%v", err)
	}

	// ── 断言没有 file 记录、没有上传历史（快传不进普通存储树）──
	var fileCount, historyCount int64
	env.db.Model(&model.File{}).Where("user_id = ?", env.userID).Count(&fileCount)
	env.db.Model(&model.UploadHistory{}).Where("user_id = ?", env.userID).Count(&historyCount)
	if fileCount != 0 || historyCount != 0 {
		t.Fatalf("快传不应产生 file/history 记录: file=%d history=%d", fileCount, historyCount)
	}

	// 模拟完成响应丢失/入口切换：重试取回原分享，不生成第二条记录。
	session, err := upload_store.Global.Get(t.Context(), uploadID)
	if err != nil || session.Status != "completed" {
		t.Fatalf("完成结果应保留: %v", err)
	}
	for i := 0; i < 3; i++ {
		code, retried := doJSON(t, env.router, http.MethodPost, "/upload/complete", map[string]any{"upload_id": uploadID})
		if code != 200 || retried["data"].(map[string]any)["share_code"] != shareCode {
			t.Fatalf("重试结果不一致: %v", retried)
		}
	}
	var count int64
	env.db.Model(&model.ShareLink{}).Where("original_path = ?", link.OriginalPath).Count(&count)
	if count != 1 {
		t.Fatalf("重试创建了多条分享: %d", count)
	}
	originalUser := env.userID
	env.userID++
	forbidden, _ := doJSON(t, env.router, http.MethodPost, "/upload/complete", map[string]any{"upload_id": uploadID})
	env.userID = originalUser
	if forbidden != 403 {
		t.Fatalf("完成结果被其它用户读取: %d", forbidden)
	}
	download := httptest.NewRecorder()
	env.router.ServeHTTP(download, httptest.NewRequest(http.MethodGet, "/share/"+shareCode, nil))
	if download.Code != 200 || !bytes.Equal(download.Body.Bytes(), content) {
		t.Fatalf("重试后链接不可下载: %d", download.Code)
	}
}

// 命中秒传（服务端已有同 hash 同大小的文件）：不走 instant，照常建会话返回 missing=空，
// complete 收齐 0 片后直接建分享。
func TestQuickShareChunkedUploadDedupGoesToComplete(t *testing.T) {
	env := setupQuickShareUploadTest(t)
	content := []byte("already-on-server-content")
	leavesHex, rootHex, fileHashHex := describeContent(t, content, 1024)

	// 服务端已有同内容文件（普通上传的 file 记录 + 物理文件）
	srcPath := filepath.Join(t.TempDir(), "existing.bin")
	if err := os.WriteFile(srcPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	size := int64(len(content))
	hash := fileHashHex
	if err := env.db.Create(&model.File{
		UserID: env.userID, FileName: "existing.bin", FilePath: srcPath,
		FileSize: &size, FileHash: &hash, IsDirectory: false, Version: 1,
	}).Error; err != nil {
		t.Fatal(err)
	}

	code, resp := doJSON(t, env.router, http.MethodPost, "/upload/init", map[string]any{
		"name":        "dup.txt",
		"quick_share": true,
		"total_size":  len(content),
		"chunk_size":  1024,
		"chunk_count": 1,
		"merkle_root": rootHex,
		"file_hash":   fileHashHex,
		"leaf_hashes": leavesHex,
	})
	if code != 200 {
		t.Fatalf("init code=%d msg=%v", code, resp["message"])
	}
	data := resp["data"].(map[string]any)
	if data["instant"].(bool) {
		t.Fatal("快传命中秒传也不应 instant（分享记录要 complete 建）")
	}
	if m := data["missing"].([]any); len(m) != 0 {
		t.Fatalf("秒传复制后 missing 应为空, got %v", m)
	}
	uploadID := data["upload_id"].(string)

	// 客户端不调 chunk，直接 complete
	code, resp = doJSON(t, env.router, http.MethodPost, "/upload/complete", map[string]any{
		"upload_id": uploadID,
	})
	if code != 200 {
		t.Fatalf("complete code=%d msg=%v", code, resp["message"])
	}
	data = resp["data"].(map[string]any)
	shareCode, _ := data["share_code"].(string)
	if shareCode == "" {
		t.Fatalf("complete 未返回分享字段: %v", data)
	}
	var link model.ShareLink
	if err := env.db.Where("share_code = ?", shareCode).First(&link).Error; err != nil {
		t.Fatalf("分享记录未创建: %v", err)
	}
	if link.FileName != "dup.txt" {
		t.Fatalf("分享记录文件名应为原始名: %+v", link)
	}
	disk, err := os.ReadFile(link.OriginalPath)
	if err != nil || !bytes.Equal(disk, content) {
		t.Fatalf("秒传复制的落盘内容不一致: err=%v", err)
	}
}

func TestQuickShareChunkedUploadQuotaPreCheck(t *testing.T) {
	env := setupQuickShareUploadTest(t)
	content := bytes.Repeat([]byte("x"), 1000)
	leavesHex, rootHex, fileHashHex := describeContent(t, content, 1000)
	initBody := map[string]any{
		"name":        "big.bin",
		"quick_share": true,
		"total_size":  len(content),
		"chunk_size":  1000,
		"chunk_count": 1,
		"merkle_root": rootHex,
		"file_hash":   fileHashHex,
		"leaf_hashes": leavesHex,
	}

	// 容量上限比文件小 → 400 超出剩余配额
	config.Conf.QuickShare.MaxCapacityBytes = 500
	code, resp := doJSON(t, env.router, http.MethodPost, "/upload/init", initBody)
	if code != 400 || resp["message"] != "超出剩余配额" {
		t.Fatalf("配额预检失效: code=%d msg=%v", code, resp["message"])
	}

	// BasePath 为空 → 500 未配置
	config.Conf.QuickShare.MaxCapacityBytes = 0
	config.Conf.QuickShare.BasePath = ""
	code, resp = doJSON(t, env.router, http.MethodPost, "/upload/init", initBody)
	if code != 500 || !strings.Contains(fmt.Sprint(resp["message"]), "未配置粘贴快传目录") {
		t.Fatalf("BasePath 预检失效: code=%d msg=%v", code, resp["message"])
	}
}
