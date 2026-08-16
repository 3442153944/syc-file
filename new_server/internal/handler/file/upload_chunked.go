package file

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/internal/sync"
	"syc-file/pkg/filecore"
	"syc-file/pkg/logger"
	"syc-file/pkg/token"
	"syc-file/pkg/upload_store"
)

// uploadSessionTTL 会话闲置多久后过期（元数据+位图+临时文件视作可回收）。
const uploadSessionTTL = 24 * time.Hour

// 目标同名文件的处理策略（uploadInitReq.OnConflict）。
const (
	// onConflictReject 默认：拒绝，让调用方自己删/改名。
	// 同步引擎依赖这条语义——它是 delete-before-upload，不该出现同名残留；
	// 真出现了必须报错而不是悄悄改名，否则同步目录里会凭空多出一个文件并被派发到所有设备。
	onConflictReject = "reject"
	// onConflictTimestamp 自动在文件名后加时间戳区分（发布 APK 这类「同名是常态」的场景用）。
	onConflictTimestamp = "timestamp"
)

// uploadInitReq 前端上传前提交的“描述信息”。
type uploadInitReq struct {
	Path string `json:"path" binding:"required"`
	Name string `json:"name" binding:"required"`
	// OnConflict 目标已存在时怎么办：reject（默认）/ timestamp（自动加时间戳）。
	OnConflict string   `json:"on_conflict"`
	TotalSize  int64    `json:"total_size" binding:"required"`
	ChunkSize  int64    `json:"chunk_size" binding:"required"`
	ChunkCount int      `json:"chunk_count" binding:"required"`
	MerkleRoot string   `json:"merkle_root" binding:"required"` // hex blake3 树根
	FileHash   string   `json:"file_hash" binding:"required"`   // hex blake3 整文件哈希
	LeafHashes []string `json:"leaf_hashes" binding:"required"` // 每片叶子哈希 hex，len==ChunkCount
	DeviceID   string   `json:"device_id"`                      // 源设备（秒传时同步派发需排除）
}

// HandlerFuncUploadInit 分片上传第一步：校验描述信息 → 秒传查重 → 建/续会话 → 预分配临时文件。
func HandlerFuncUploadInit(db *gorm.DB, _ *redis.Client, engine *sync.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "请先登录", "data": nil})
			return
		}
		userID := uint(claims.(*token.Claims).UserID)

		var req uploadInitReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数解析失败", "data": nil})
			return
		}

		fullPath := resolveUploadPath(req.Path, req.Name)

		// 基础校验：路径、扩展名、文件名长度、大小
		if !isPathAllowedDownload(fullPath) {
			c.JSON(http.StatusOK, gin.H{"code": 403, "message": "无权访问该路径", "data": nil})
			return
		}
		if !config.Conf.IsExtensionAllowed(filepath.Ext(req.Name)) {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "不允许上传该类型的文件", "data": nil})
			return
		}
		if len(req.Name) > config.Conf.File.Upload.MaxFilenameLength {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "文件名过长", "data": nil})
			return
		}
		if req.TotalSize <= 0 || req.TotalSize > config.Conf.File.Upload.MaxFileSize {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "文件大小超出限制", "data": nil})
			return
		}

		// 分片参数一致性：chunk_count 须等于 ceil(total/chunk)，叶子数须匹配
		if req.ChunkSize <= 0 || req.ChunkCount <= 0 {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "分片参数无效", "data": nil})
			return
		}
		wantCount := int((req.TotalSize + req.ChunkSize - 1) / req.ChunkSize)
		if req.ChunkCount != wantCount || len(req.LeafHashes) != req.ChunkCount {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "分片数量与描述不一致", "data": nil})
			return
		}

		// 校验描述信息自洽：用叶子哈希重算 Merkle 树根，须与提交的树根一致
		leavesBytes, err := decodeLeaves(req.LeafHashes)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "叶子哈希格式错误", "data": nil})
			return
		}
		wantRoot, err := hex.DecodeString(req.MerkleRoot)
		if err != nil || len(wantRoot) != filecore.HashSize {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "树根格式错误", "data": nil})
			return
		}
		gotRoot, err := filecore.MerkleRoot(leavesBytes)
		if err != nil || !bytes.Equal(gotRoot, wantRoot) {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "描述信息树根不一致，请重新计算", "data": nil})
			return
		}

		ctx := context.Background()

		// 目标已存在时怎么办。判据只看**磁盘**（os.Stat），不看 file 表——
		// 库里有记录而盘上没有（被手工删过/清理过）不该挡住上传，反过来也一样。
		if st, statErr := os.Stat(fullPath); statErr == nil {
			switch {
			case st.Size() == req.TotalSize && pathHashEquals(db, userID, fullPath, req.FileHash):
				// 同路径 + 同大小 + 同哈希 = 要传的东西本来就在那儿（重复发布同一个包、
				// init 重发）。这不是错误，直接当秒传成功返回，既不重传也不产生副本。
				respondInstant(c, db, engine, userID, &req, fullPath,
					upload_store.SessionID(userID, fullPath, req.FileHash, req.TotalSize, req.ChunkSize), "文件已是最新")
				return
			case req.OnConflict == onConflictTimestamp:
				// 「同名是常态」的场景（发布 APK：每次 build 出来都叫 app-release.apk）。
				// 报错逼用户改名没有意义，加时间戳区分即可，两个版本都留在盘上。
				newName, newPath, err := uniqueNameWithTimestamp(req.Path, req.Name)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"code": 500, "message": "生成不重名文件名失败: " + err.Error(), "data": nil})
					return
				}
				logger.Logger.Info("目标同名，自动加时间戳区分",
					zap.String("old", req.Name), zap.String("new", newName))
				req.Name = newName
				fullPath = newPath
			default:
				c.JSON(http.StatusOK, gin.H{"code": 400, "message": "文件已存在，请先删除或重命名", "data": nil})
				return
			}
		}

		id := upload_store.SessionID(userID, fullPath, req.FileHash, req.TotalSize, req.ChunkSize)

		// 秒传：同用户已有相同 file_hash + 大小的文件且物理存在 → 直接复制落盘。
		// 注意：秒传不建会话，客户端收到 instant=true 后【不得】再调 complete。
		if src, ok := findDedupSource(db, userID, req.FileHash, req.TotalSize); ok && src != fullPath {
			if err := copyFile(src, fullPath); err != nil {
				logger.Logger.Warn("秒传复制失败，回退普通上传", zap.String("src", src), zap.Error(err))
			} else {
				respondInstant(c, db, engine, userID, &req, fullPath, id, "秒传成功")
				return
			}
		}

		// 续传：已有会话且描述一致、临时文件仍在 → 返回缺失分片
		if sess, err := upload_store.Global.Get(ctx, id); err == nil {
			_, tempErr := os.Stat(sess.TempPath)
			if tempErr == nil && sess.MerkleRoot == req.MerkleRoot && sess.FileHash == req.FileHash &&
				sess.TotalSize == req.TotalSize && sess.ChunkSize == req.ChunkSize {
				_ = upload_store.Global.Touch(ctx, id, uploadSessionTTL)
				bitmap, _ := upload_store.Global.Bitmap(ctx, id)
				missing := upload_store.MissingChunks(bitmap, sess.ChunkCount)
				logger.Logger.Info("上传续传", zap.String("upload_id", id), zap.Int("missing", len(missing)))
				c.JSON(http.StatusOK, gin.H{"code": 200, "message": "续传", "data": gin.H{
					"upload_id": id, "instant": false, "chunk_size": sess.ChunkSize,
					"chunk_count": sess.ChunkCount, "missing": missing,
				}})
				return
			}
			// 描述对不上或临时文件已丢 → 丢弃旧会话，重新开始（客户端整份重传）
			_ = upload_store.Global.Delete(ctx, id)
			_ = filecore.Evict(sess.TempPath) // 先关 Rust 侧缓存句柄再删文件
			_ = os.Remove(sess.TempPath)
		}

		// 新建会话：预分配临时文件（与目标同盘，便于原子 rename）
		tempPath := tempPathFor(fullPath, id)
		if err := filecore.Preallocate(tempPath, uint64(req.TotalSize)); err != nil {
			logger.Logger.Error("预分配临时文件失败", zap.String("temp", tempPath), zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "预分配失败", "data": nil})
			return
		}

		historyID := createUploadHistoryUploading(db, userID, req.Name, fullPath, req.TotalSize, id, req.ChunkCount, c)
		sess := &upload_store.Session{
			ID: id, UserID: userID, FileName: req.Name, TargetPath: fullPath, TempPath: tempPath,
			TotalSize: req.TotalSize, ChunkSize: req.ChunkSize, ChunkCount: req.ChunkCount,
			MerkleRoot: req.MerkleRoot, FileHash: req.FileHash,
			Leaves: hex.EncodeToString(leavesBytes), Status: "uploading",
			HistoryID: historyID, CreatedAt: time.Now().Unix(),
		}
		if err := upload_store.Global.Create(ctx, sess, uploadSessionTTL); err != nil {
			logger.Logger.Error("创建上传会话失败", zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "创建会话失败", "data": nil})
			return
		}

		missing := make([]int, req.ChunkCount)
		for i := range missing {
			missing[i] = i
		}
		logger.Logger.Info("上传会话已创建", zap.String("upload_id", id), zap.String("path", fullPath), zap.Int("chunks", req.ChunkCount))
		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": gin.H{
			"upload_id": id, "instant": false, "chunk_size": req.ChunkSize,
			"chunk_count": req.ChunkCount, "missing": missing,
		}})
	}
}

// HandlerFuncUploadStatus 查询会话进度与缺失分片，供断点续传。
func HandlerFuncUploadStatus(_ *gorm.DB, _ *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := c.Get("UserInfo"); !ok {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "请先登录", "data": nil})
			return
		}
		id := c.Query("upload_id")
		if id == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "缺少 upload_id", "data": nil})
			return
		}
		ctx := context.Background()
		sess, err := upload_store.Global.Get(ctx, id)
		if errors.Is(err, upload_store.ErrNotFound) {
			c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": gin.H{"found": false}})
			return
		}
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询会话失败", "data": nil})
			return
		}
		_ = upload_store.Global.Touch(ctx, id, uploadSessionTTL)
		bitmap, _ := upload_store.Global.Bitmap(ctx, id)
		missing := upload_store.MissingChunks(bitmap, sess.ChunkCount)
		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": gin.H{
			"found": true, "status": sess.Status, "chunk_size": sess.ChunkSize,
			"chunk_count": sess.ChunkCount, "received": sess.ChunkCount - len(missing),
			"missing": missing, "total_size": sess.TotalSize,
		}})
	}
}

// ---- helpers ----

// respondInstant 「内容已经在目标位置了」的统一收尾：登记 file 记录 / 交同步引擎派发 /
// 写上传历史，然后按秒传形状返回（instant=true，客户端**不得**再调 complete）。
// 用于两处：跨路径秒传复制完成后，以及目标路径本来就是同一份内容时。
func respondInstant(c *gin.Context, db *gorm.DB, engine *sync.Engine, userID uint,
	req *uploadInitReq, fullPath, id, message string) {
	// 与 complete 相同的双分支：同步目录内 → 引擎维护 trunk + 派发拉取；否则普通落库
	var fileID uint64
	var handled bool
	if engine != nil {
		var herr error
		handled, herr = engine.HandleUploadComplete(userID, req.DeviceID, fullPath, req.Name, req.TotalSize, req.FileHash)
		if herr != nil {
			logger.Logger.Warn("秒传同步派发失败", zap.String("path", fullPath), zap.Error(herr))
		}
		if handled {
			fileID = lookupFileID(db, userID, fullPath)
		}
	}
	if !handled {
		fileID, _ = upsertFileRecord(db, userID, fullPath, req.Name, req.TotalSize, req.FileHash)
	}
	writeUploadHistoryCompleted(db, userID, req.Name, fullPath, req.TotalSize, id, req.ChunkCount, c)
	logger.Logger.Info(message, zap.Uint("user_id", userID), zap.String("path", fullPath),
		zap.Uint64("file_id", fileID), zap.Bool("synced", handled))
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": message, "data": gin.H{
		"upload_id": id, "instant": true, "chunk_size": req.ChunkSize,
		"chunk_count": req.ChunkCount, "missing": []int{}, "synced": handled,
		"file_name": req.Name, "storage_path": fullPath,
	}})
}

// pathHashEquals 该路径在 file 表里记录的哈希是否等于 want。
// 只作为「同大小」之外的第二判据用：磁盘大小相同 + 库里哈希相同才认定是同一份内容，
// 避免为了判断而去读几十 MB 的文件重算哈希。库里没记录则返回 false（宁可重传）。
func pathHashEquals(db *gorm.DB, userID uint, fullPath, want string) bool {
	if want == "" {
		return false
	}
	var f model.File
	if err := db.Where("user_id = ? AND file_path = ? AND is_deleted = ?", userID, fullPath, false).
		First(&f).Error; err != nil {
		return false
	}
	return f.FileHash != nil && *f.FileHash == want
}

// uniqueNameWithTimestamp 给同名文件加时间戳：`app-release.apk` → `app-release_20260816-225015.apk`。
// 同一秒内再撞（连点两次发布）就往后加序号。返回新文件名与新全路径。
func uniqueNameWithTimestamp(reqPath, name string) (string, string, error) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	stamp := time.Now().Format("20060102-150405")
	for i := 0; i < 100; i++ {
		candidate := fmt.Sprintf("%s_%s%s", base, stamp, ext)
		if i > 0 {
			candidate = fmt.Sprintf("%s_%s-%d%s", base, stamp, i, ext)
		}
		full := resolveUploadPath(reqPath, candidate)
		if _, err := os.Stat(full); os.IsNotExist(err) {
			return candidate, full, nil
		}
	}
	return "", "", fmt.Errorf("同名文件过多，无法生成唯一文件名")
}

// resolveUploadPath 与旧单发上传一致：name 已是 path 末段则直接用，否则拼接。
func resolveUploadPath(reqPath, name string) string {
	p := filepath.FromSlash(reqPath)
	if filepath.Base(p) == name {
		return p
	}
	return filepath.Join(p, name)
}

// tempPathFor 临时文件放目标同盘的 <盘>/BasePath/TempPath 下，命名为 <id>.part。
func tempPathFor(fullPath, id string) string {
	vol := filepath.VolumeName(fullPath)
	dir := filepath.Join(vol+string(filepath.Separator), config.Conf.File.Storage.BasePath, config.Conf.File.Storage.TempPath)
	return filepath.Join(dir, id+".part")
}

// decodeLeaves 把叶子哈希 hex 列表解码并拼成连续字节（每片 32B）。
func decodeLeaves(hexes []string) ([]byte, error) {
	out := make([]byte, 0, len(hexes)*filecore.HashSize)
	for _, h := range hexes {
		b, err := hex.DecodeString(h)
		if err != nil || len(b) != filecore.HashSize {
			return nil, errors.New("invalid leaf hash")
		}
		out = append(out, b...)
	}
	return out, nil
}

// findDedupSource 查同用户相同 file_hash + 大小的现存文件路径（秒传源）。
func findDedupSource(db *gorm.DB, userID uint, fileHash string, size int64) (string, bool) {
	var f model.File
	err := db.Where("user_id = ? AND file_hash = ? AND file_size = ? AND is_deleted = ? AND is_directory = ?",
		userID, fileHash, size, false, false).First(&f).Error
	if err != nil {
		return "", false
	}
	if _, statErr := os.Stat(f.FilePath); statErr != nil {
		return "", false // 记录在但物理文件已丢，不能秒传
	}
	return f.FilePath, true
}

// copyFile 复制 src 到 dst（秒传落盘），确保父目录存在。
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// upsertFileRecord 建立或更新 file 表记录（秒传与 complete 复用）。返回 file id。
func upsertFileRecord(db *gorm.DB, userID uint, fullPath, name string, size int64, hashHex string) (uint64, error) {
	var f model.File
	err := db.Where("user_id = ? AND file_path = ?", userID, fullPath).First(&f).Error
	if err == nil {
		db.Model(&f).Updates(map[string]interface{}{
			"file_name":  name,
			"file_size":  size,
			"file_hash":  hashHex,
			"is_deleted": false,
			"deleted_at": nil,
			"version":    f.Version + 1,
		})
		return f.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	sz := size
	hh := hashHex
	nf := model.File{
		UserID: userID, FileName: name, FilePath: fullPath,
		FileSize: &sz, FileHash: &hh, IsDirectory: false, Version: 1,
	}
	if err := db.Create(&nf).Error; err != nil {
		return 0, err
	}
	return nf.ID, nil
}

// createUploadHistoryUploading 建一条 uploading 的上传历史，返回 id。
func createUploadHistoryUploading(db *gorm.DB, userID uint, name, path string, size int64, uploadID string, chunkCount int, c *gin.Context) uint64 {
	ip := c.ClientIP()
	ua := c.GetHeader("User-Agent")
	uid := uploadID
	cc := chunkCount
	sz := size
	sp := path
	h := &model.UploadHistory{
		UserID: userID, FileName: name, FileSize: &sz, StoragePath: &sp,
		UploadID: &uid, ChunkCount: &cc, UploadStatus: "uploading",
		IPAddress: &ip, UserAgent: &ua,
	}
	if err := db.Create(h).Error; err != nil {
		logger.Logger.Warn("创建上传历史失败", zap.Error(err))
		return 0
	}
	return h.ID
}

// writeUploadHistoryCompleted 秒传时直接写一条 completed 历史。
func writeUploadHistoryCompleted(db *gorm.DB, userID uint, name, path string, size int64, uploadID string, chunkCount int, c *gin.Context) {
	ip := c.ClientIP()
	ua := c.GetHeader("User-Agent")
	uid := uploadID
	cc := chunkCount
	sz := size
	sp := path
	now := time.Now()
	h := &model.UploadHistory{
		UserID: userID, FileName: name, FileSize: &sz, StoragePath: &sp,
		UploadID: &uid, ChunkCount: &cc, UploadStatus: "completed", Progress: 100,
		IPAddress: &ip, UserAgent: &ua, CompletedAt: &now,
	}
	if err := db.Create(h).Error; err != nil {
		logger.Logger.Warn("写入秒传历史失败", zap.Error(err))
	}
}
