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
	"syc-file/pkg/response"
	"time"

	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/internal/storage"
	"syc-file/internal/sync"
	"syc-file/internal/thumb"
	"syc-file/pkg/logger"
	"syc-file/pkg/synccore"
	"syc-file/pkg/token"
	"syc-file/pkg/upload_store"
	"syc-file/pkg/volroot"
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
	// Path 目标目录（name 已是 path 末段则直接用）。QuickShare 模式下服务端自算目标路径，可为空。
	Path string `json:"path"`
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

	// Sync 标记这是同步客户端的覆盖式上传：目标已存在且内容不同时不是一律拒绝，
	// 而是比对 BaseHash（客户端上次与服务端对齐时的版本）与服务端当前版本——一致说明
	// 是基于最新版本的修改（含离线编辑），允许覆盖；不一致或没有基线说明与其它设备分叉，走冲突待办。
	Sync     bool   `json:"sync"`
	BaseHash string `json:"base_hash"`

	// QuickShare 粘贴快传模式：目标路径由服务端算（quick_share.base_path/<userID>/<uuid><ext>），
	// 跳过普通路径的目录权限/扩展名/同名检查；complete 时不建 file 记录，直接生成分享记录。
	QuickShare bool `json:"quick_share"`
}

// HandlerFuncUploadInit 分片上传第一步：校验描述信息 → 秒传查重 → 建/续会话 → 预分配临时文件。
func HandlerFuncUploadInit(db *gorm.DB, _ *redis.Client, engine *sync.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			response.WriteHTTP(c, http.StatusOK, 401, "请先登录", nil)
			return
		}
		userID := uint(claims.(*token.Claims).UserID)

		var req uploadInitReq
		if err := c.ShouldBindJSON(&req); err != nil {
			response.WriteHTTP(c, http.StatusOK, 400, "参数解析失败", nil)
			return
		}

		var fullPath string
		if req.QuickShare {
			// 快传：目标由服务端自算（可信），独立于普通路径的 resolveUploadPath/IsPathAllowed。
			target, terr := quickShareTargetPath(userID, req.Name)
			if terr != nil {
				response.WriteHTTP(c, http.StatusOK, 500, terr.Error(), nil)
				return
			}
			fullPath = target
		} else {
			if req.Path == "" {
				response.WriteHTTP(c, http.StatusOK, 400, "缺少目标路径", nil)
				return
			}
			fullPath = resolveUploadPath(req.Path, req.Name)
		}

		// 基础校验：路径、扩展名、文件名长度、大小（快传跳过前两项——它不进普通存储树，
		// 落地目录与配额由 quick_share 配置独立约束）
		if !req.QuickShare {
			var allowed bool
			fullPath, allowed = resolveFilePath(c, db, fullPath)
			if !allowed {
				return
			}
			req.Path = filepath.Dir(fullPath)
			if !config.Conf.IsExtensionAllowed(filepath.Ext(req.Name)) {
				response.WriteHTTP(c, http.StatusOK, 400, "不允许上传该类型的文件", nil)
				return
			}
			if len(req.Name) > config.Conf.File.Upload.MaxFilenameLength {
				response.WriteHTTP(c, http.StatusOK, 400, "文件名过长", nil)
				return
			}
		}
		if req.TotalSize <= 0 || (!req.QuickShare && req.TotalSize > config.Conf.File.Upload.MaxFileSize) {
			response.WriteHTTP(c, http.StatusOK, 400, "文件大小超出限制", nil)
			return
		}
		if req.QuickShare {
			// 按 total_size 预检剩余配额（与旧单发快传接口同一套口径），不足直接拒，省得白传。
			remaining, qerr := quickShareRemainingBytes(db, userID)
			if qerr != nil {
				logger.Logger.Error("查询快传配额失败", zap.Error(qerr))
				response.WriteHTTP(c, http.StatusOK, 500, "查询配额失败", nil)
				return
			}
			if remaining <= 0 {
				response.WriteHTTP(c, http.StatusOK, 400, "快速共享空间已满，请等待旧分享过期或前往分享管理提前清理", nil)
				return
			}
			if req.TotalSize > remaining {
				response.WriteHTTP(c, http.StatusOK, 400, "超出剩余配额", nil)
				return
			}
		}

		// 分片参数一致性：chunk_count 须等于 ceil(total/chunk)，叶子数须匹配
		if req.ChunkSize <= 0 || req.ChunkCount <= 0 {
			response.WriteHTTP(c, http.StatusOK, 400, "分片参数无效", nil)
			return
		}
		wantCount := int((req.TotalSize + req.ChunkSize - 1) / req.ChunkSize)
		if req.ChunkCount != wantCount || len(req.LeafHashes) != req.ChunkCount {
			response.WriteHTTP(c, http.StatusOK, 400, "分片数量与描述不一致", nil)
			return
		}

		// 校验描述信息自洽：用叶子哈希重算 Merkle 树根，须与提交的树根一致
		leavesBytes, err := decodeLeaves(req.LeafHashes)
		if err != nil {
			response.WriteHTTP(c, http.StatusOK, 400, "叶子哈希格式错误", nil)
			return
		}
		wantRoot, err := hex.DecodeString(req.MerkleRoot)
		if err != nil || len(wantRoot) != synccore.HashSize {
			response.WriteHTTP(c, http.StatusOK, 400, "树根格式错误", nil)
			return
		}
		gotRoot, err := synccore.MerkleRoot(leavesBytes)
		if err != nil || !bytes.Equal(gotRoot, wantRoot) {
			response.WriteHTTP(c, http.StatusOK, 400, "描述信息树根不一致，请重新计算", nil)
			return
		}

		ctx := context.Background()

		// 目标已存在时怎么办。判据只看**磁盘**（os.Stat），不看 file 表——
		// 库里有记录而盘上没有（被手工删过/清理过）不该挡住上传，反过来也一样。
		// 快传目标是 uuid 文件名，不存在这套语义，跳过。
		if !req.QuickShare {
			if st, statErr := os.Stat(fullPath); statErr == nil {
				switch {
				case st.Size() == req.TotalSize && pathHashEquals(db, userID, fullPath, req.FileHash):
					// 同路径 + 同大小 + 同哈希 = 要传的东西本来就在那儿（重复发布同一个包、
					// init 重发）。这不是错误，直接当秒传成功返回，既不重传也不产生副本。
					respondInstant(c, db, engine, userID, &req, fullPath,
						upload_store.SessionID(userID, fullPath, req.FileHash, req.TotalSize, req.ChunkSize), "文件已是最新")
					return
				case req.Sync:
					cur := pathHash(db, userID, fullPath)
					if req.BaseHash != "" && req.BaseHash == cur {
						break // 基于服务端当前版本的修改：快进，complete 时原子替换
					}
					if engine != nil {
						engine.ReportUploadConflict(userID, req.DeviceID, fullPath, req.Name, req.TotalSize, req.FileHash, req.BaseHash)
					}
					logger.Logger.Warn("同步上传与服务端版本冲突，转入冲突待办", zap.String("path", fullPath),
						zap.String("device_id", req.DeviceID), zap.String("base_hash", req.BaseHash), zap.String("server_hash", cur))
					response.WriteHTTP(c, http.StatusOK, 409, "同步冲突：服务端版本已被其它设备修改，本地副本将转入冲突待办", gin.H{"conflict": true, "server_hash": cur})
					return
				case req.OnConflict == onConflictTimestamp:
					// 「同名是常态」的场景（发布 APK：每次 build 出来都叫 app-release.apk）。
					// 报错逼用户改名没有意义，加时间戳区分即可，两个版本都留在盘上。
					newName, newPath, err := uniqueNameWithTimestamp(req.Path, req.Name)
					if err != nil {
						response.WriteHTTP(c, http.StatusOK, 500, "生成不重名文件名失败: "+err.Error(), nil)
						return
					}
					logger.Logger.Info("目标同名，自动加时间戳区分",
						zap.String("old", req.Name), zap.String("new", newName))
					req.Name = newName
					fullPath = newPath
				default:
					response.WriteHTTP(c, http.StatusOK, 400, "文件已存在，请先删除或重命名", nil)
					return
				}
			}
		}

		id := upload_store.SessionID(userID, fullPath, req.FileHash, req.TotalSize, req.ChunkSize)

		// 秒传：同用户已有相同 file_hash + 大小的文件且物理存在。
		// 普通路径：直接复制落盘并 instant 返回（不建会话，客户端收到 instant=true 后【不得】再调 complete）。
		// 快传：分享记录必须由 complete 建，【不走 instant】——内容复制进会话临时文件、照常建会话
		// 并预置全部位图，客户端见 missing=空 不调 chunk，complete 校验落盘后直接走分享创建。
		var quickShareSrc string
		if src, ok := findDedupSource(db, userID, req.FileHash, req.TotalSize); ok && src != fullPath && (req.QuickShare || storage.Authorize(db, userID, src) == nil) {
			if req.QuickShare {
				quickShareSrc = src
			} else if err := storage.Allocate(db, userID, fullPath, req.TotalSize, func() error { return copyFile(src, fullPath) }); err != nil {
				logger.Logger.Warn("秒传复制失败，回退普通上传", zap.String("src", src), zap.Error(err))
			} else {
				respondInstant(c, db, engine, userID, &req, fullPath, id, "秒传成功")
				return
			}
		}

		// 续传：已有会话且描述一致、临时文件仍在 → 返回缺失分片
		if sess, err := upload_store.Global.Get(ctx, id); err == nil {
			_, tempErr := os.Stat(sess.TempPath)
			if !req.QuickShare {
				if expected, managed := storage.Temp(db, userID, fullPath, id); managed && filepath.Clean(expected) != filepath.Clean(sess.TempPath) {
					tempErr = os.ErrNotExist
				}
			}
			if tempErr == nil && sess.MerkleRoot == req.MerkleRoot && sess.FileHash == req.FileHash &&
				sess.TotalSize == req.TotalSize && sess.ChunkSize == req.ChunkSize {
				_ = upload_store.Global.Touch(ctx, id, uploadSessionTTL)
				bitmap, _ := upload_store.Global.Bitmap(ctx, id)
				missing := upload_store.MissingChunks(bitmap, sess.ChunkCount)
				logger.Logger.Info("上传续传", zap.String("upload_id", id), zap.Int("missing", len(missing)))
				response.WriteHTTP(c, http.StatusOK, 200, "续传", gin.H{
					"upload_id": id, "instant": false, "chunk_size": sess.ChunkSize,
					"chunk_count": sess.ChunkCount, "missing": missing,
				})
				return
			}
			// 描述对不上或临时文件已丢 → 丢弃旧会话，重新开始（客户端整份重传）
			_ = upload_store.Global.Delete(ctx, id)
			_ = synccore.Evict(sess.TempPath) // 先关 Rust 侧缓存句柄再删文件
			_ = os.Remove(sess.TempPath)
		}

		// 新建会话：内容进临时文件（与目标同盘，便于原子 rename）。普通路径预分配占位；
		// 快传命中秒传源时直接复制进临时文件（复制失败回退预分配，照常分片上传）。
		tempPath := tempPathFor(fullPath, id)
		if !req.QuickShare {
			if managed, ok := storage.Temp(db, userID, fullPath, id); ok {
				tempPath = managed
			}
		}
		quickShareCopied := false
		if quickShareSrc != "" {
			if err := copyFile(quickShareSrc, tempPath); err != nil {
				logger.Logger.Warn("快传秒传复制失败，回退普通上传", zap.String("src", quickShareSrc), zap.Error(err))
			} else {
				quickShareCopied = true
			}
		}
		if !quickShareCopied {
			allocate := func() error { return synccore.Preallocate(tempPath, uint64(req.TotalSize)) }
			if err := allocateUpload(db, userID, tempPath, req.TotalSize, req.QuickShare, allocate); err != nil {
				logger.Logger.Error("预分配临时文件失败", zap.String("temp", tempPath), zap.Error(err))
				response.WriteHTTP(c, http.StatusOK, 500, err.Error(), nil)
				return
			}
		}

		// 快传不写上传历史（与旧单发快传接口一致），HistoryID 为 0，complete 据此跳过历史收尾。
		var historyID uint64
		if !req.QuickShare {
			historyID = createUploadHistoryUploading(db, userID, req.Name, fullPath, req.TotalSize, id, req.ChunkCount, c)
		}
		sess := &upload_store.Session{
			ID: id, UserID: userID, FileName: req.Name, TargetPath: fullPath, TempPath: tempPath,
			TotalSize: req.TotalSize, ChunkSize: req.ChunkSize, ChunkCount: req.ChunkCount,
			MerkleRoot: req.MerkleRoot, FileHash: req.FileHash,
			Leaves: hex.EncodeToString(leavesBytes), Status: "uploading",
			HistoryID: historyID, CreatedAt: time.Now().Unix(),
			QuickShare: req.QuickShare,
		}
		if err := upload_store.Global.Create(ctx, sess, uploadSessionTTL); err != nil {
			logger.Logger.Error("创建上传会话失败", zap.Error(err))
			response.WriteHTTP(c, http.StatusOK, 500, "创建会话失败", nil)
			return
		}

		missing := make([]int, req.ChunkCount)
		for i := range missing {
			missing[i] = i
		}
		if quickShareCopied {
			// 秒传内容已在临时文件里：预置全部位图并通告 missing=空，客户端直接进 complete。
			for i := 0; i < req.ChunkCount; i++ {
				_ = upload_store.Global.MarkChunk(ctx, id, i)
			}
			missing = []int{}
		}
		logger.Logger.Info("上传会话已创建", zap.String("upload_id", id), zap.String("path", fullPath), zap.Int("chunks", req.ChunkCount), zap.Bool("quick_share", req.QuickShare))
		response.WriteHTTP(c, http.StatusOK, 200, "ok", gin.H{
			"upload_id": id, "instant": false, "chunk_size": req.ChunkSize,
			"chunk_count": req.ChunkCount, "missing": missing,
		})
	}
}

// HandlerFuncUploadStatus 查询会话进度与缺失分片，供断点续传。
func HandlerFuncUploadStatus(db *gorm.DB, _ *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := c.Get("UserInfo"); !ok {
			response.WriteHTTP(c, http.StatusOK, 401, "请先登录", nil)
			return
		}
		id := c.Query("upload_id")
		if id == "" {
			response.WriteHTTP(c, http.StatusOK, 400, "缺少 upload_id", nil)
			return
		}
		ctx := context.Background()
		sess, err := upload_store.Global.Get(ctx, id)
		if errors.Is(err, upload_store.ErrNotFound) {
			response.WriteHTTP(c, http.StatusOK, 200, "ok", gin.H{"found": false})
			return
		}
		if err != nil {
			response.WriteHTTP(c, http.StatusOK, 500, "查询会话失败", nil)
			return
		}
		claims, _ := c.Get("UserInfo")
		if claims == nil || sess.UserID != uint(claims.(*token.Claims).UserID) {
			response.WriteHTTP(c, http.StatusOK, 403, "无权访问该上传会话", nil)
			return
		}
		if !sess.QuickShare && !checkFilePath(c, db, sess.TargetPath) {
			return
		}
		if !checkUploadStage(c, db, sess) {
			return
		}
		now := time.Now()
		_ = os.Chtimes(sess.TempPath, now, now)
		_ = upload_store.Global.Touch(ctx, id, uploadSessionTTL)
		bitmap, _ := upload_store.Global.Bitmap(ctx, id)
		missing := upload_store.MissingChunks(bitmap, sess.ChunkCount)
		response.WriteHTTP(c, http.StatusOK, 200, "ok", gin.H{
			"found": true, "status": sess.Status, "chunk_size": sess.ChunkSize,
			"chunk_count": sess.ChunkCount, "received": sess.ChunkCount - len(missing),
			"missing": missing, "total_size": sess.TotalSize,
		})
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
		mirrorToTrunk(engine, userID, fullPath, req.Name, req.TotalSize, req.FileHash)
		fileID, _ = upsertFileRecord(db, userID, fullPath, req.Name, req.TotalSize, req.FileHash)
	}
	writeUploadHistoryCompleted(db, userID, req.Name, fullPath, req.TotalSize, id, req.ChunkCount, c)
	thumb.Enqueue(fullPath)
	logger.Logger.Info(message, zap.Uint("user_id", userID), zap.String("path", fullPath),
		zap.Uint64("file_id", fileID), zap.Bool("synced", handled))
	response.WriteHTTP(c, http.StatusOK, 200, message, gin.H{
		"upload_id": id, "instant": true, "chunk_size": req.ChunkSize,
		"chunk_count": req.ChunkCount, "missing": []int{}, "synced": handled,
		"file_name": req.Name, "storage_path": fullPath,
	})
}

// pathHashEquals 该路径在 file 表里记录的哈希是否等于 want。
// 只作为「同大小」之外的第二判据用：磁盘大小相同 + 库里哈希相同才认定是同一份内容，
// 避免为了判断而去读几十 MB 的文件重算哈希。库里没记录则返回 false（宁可重传）。
// pathHash 返回该路径当前在库里记录的整文件哈希，没有记录/已删除返回 ""。
func pathHash(db *gorm.DB, userID uint, fullPath string) string {
	var f model.File
	if err := db.Where("user_id = ? AND file_path = ? AND is_deleted = ?", userID, fullPath, false).
		First(&f).Error; err != nil || f.FileHash == nil {
		return ""
	}
	return *f.FileHash
}

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

// quickShareTargetPath 快传分片上传的目标路径：quick_share.base_path/<userID>/<uuid><ext>。
// 与旧单发快传接口同一套落地规则。BasePath 未配置返回错误（调用方回 500）。
func quickShareTargetPath(userID uint, name string) (string, error) {
	qs := config.Conf.QuickShare
	if qs.BasePath == "" {
		return "", errors.New("未配置粘贴快传目录 quick_share.base_path")
	}
	base := filepath.Base(name)
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "pasted-file"
	}
	return filepath.Join(qs.BasePath, strconv.FormatUint(uint64(userID), 10), uuid.NewString()+filepath.Ext(base)), nil
}

// quickShareRemainingBytes 快传剩余配额 = 容量上限（config 未配时 10GB 兜底）- 已用。
func quickShareRemainingBytes(db *gorm.DB, userID uint) (int64, error) {
	maxCapacity := config.Conf.QuickShare.MaxCapacityBytes
	if maxCapacity <= 0 {
		maxCapacity = defaultQuickShareCapacityBytes
	}
	used, err := quickShareUsedBytes(db, userID)
	if err != nil {
		return 0, err
	}
	return maxCapacity - used, nil
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
	vol := volroot.RootOf(fullPath)
	base := config.Conf.File.Storage.BasePath
	if !filepath.IsAbs(base) {
		base = filepath.Join(vol, base)
	}
	dir := filepath.Join(base, config.Conf.File.Storage.TempPath)
	return filepath.Join(dir, id+".part")
}

// decodeLeaves 把叶子哈希 hex 列表解码并拼成连续字节（每片 32B）。
func decodeLeaves(hexes []string) ([]byte, error) {
	out := make([]byte, 0, len(hexes)*synccore.HashSize)
	for _, h := range hexes {
		b, err := hex.DecodeString(h)
		if err != nil || len(b) != synccore.HashSize {
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
